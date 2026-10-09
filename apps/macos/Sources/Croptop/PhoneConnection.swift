import AppKit
import Combine
import Foundation

struct PhonePairing: Decodable, Equatable {
    let id: String
    let url: String
    let expiresAt: TimeInterval
    let state: String
    let ipns: String
    let name: String
}

struct PhonePreparation: Decodable {
    let id: String
    let siteID: String
    let state: String
    let stage: String
    let startedAt: TimeInterval
    let deadline: TimeInterval
    var connection: PhonePairing?
    var code: String?
}

struct PhonePairingStatus: Decodable {
    let state: String
    let ipns: String
    let expiresAt: TimeInterval
    // Deliberately do not decode the server's confirmation code. The user
    // must get it from their phone, never from this Mac's status response.
}

struct PhoneConnectionError: Error {
    let status: Int
    let code: String?
}

@MainActor protocol PhoneConnectionClient {
    func prepare(siteID: String, id: String, enableHosting: Bool) async throws -> PhonePreparation
    func preparation(siteID: String, id: String) async throws -> PhonePreparation
    func cancel(siteID: String, id: String) async throws
    func status(siteID: String, pairingID: String) async throws -> PhonePairingStatus
    func confirm(siteID: String, pairingID: String, code: String) async throws
}

/// A site-scoped, memory-only handoff. No root key reaches this model.
@MainActor final class PhoneConnectionModel: ObservableObject {
    enum Phase: Equatable {
        case consent, preparing, scan, confirm, sent, delivered, expired, failed, stopping, closed
    }
    let site: Site
    @Published var consent = false
    @Published var code = ""
    @Published private(set) var phase: Phase = .consent
    @Published private(set) var stage = "waiting"
    @Published private(set) var error: String?
    @Published private(set) var pairing: PhonePairing?
    @Published private(set) var busy = false
    @Published private(set) var copied = false
    @Published private(set) var elapsed = 0

    private let client: PhoneConnectionClient
    private let now: () -> Date
    private let clipboard: PhoneConnectionClipboard
    private var preparationID: String?
    private var startedAt: Date?
    private var deadline: Date?
    private var generation = UUID()
    private var action: Task<Void, Never>?
    private var refreshing = false

    init(site: Site, client: PhoneConnectionClient? = nil,
         now: @escaping () -> Date = Date.init, clipboard: PhoneConnectionClipboard? = nil) {
        self.site = site
        self.client = client ?? LocalPhoneConnectionClient()
        self.now = now
        self.clipboard = clipboard ?? PhoneConnectionClipboard()
    }

    var canStart: Bool { consent && !busy && [.consent, .failed, .expired].contains(phase) }
    var normalizedCode: String { code.replacingOccurrences(of: " ", with: "").trimmingCharacters(in: .whitespacesAndNewlines) }
    var canConfirm: Bool {
        !busy && phase == .confirm && normalizedCode.utf8.count == 8
            && normalizedCode.utf8.allSatisfy { (48...57).contains($0) }
            && pairing.map { $0.expiresAt > now().timeIntervalSince1970 } == true
    }
    var stageTitle: String {
        switch stage {
        case "service": return "Checking the phone service"
        case "checking": return "Checking your hosted site"
        case "publishing": return "Publishing your site"
        case "hosting": return "Uploading and checking your site"
        case "pairing": return "Creating a secure connection"
        default: return "Waiting for the publisher"
        }
    }
    var stageDetail: String {
        switch stage {
        case "publishing": return "Your site needs an updated hosted copy. Larger sites can take a few minutes."
        case "hosting": return "Uploading and verifying your hosted site. Larger sites can take a few minutes."
        case "checking": return "An already compatible hosted site can connect without republishing."
        default: return "Keep Croptop open and this Mac awake until the QR code appears."
        }
    }

    @discardableResult func begin() -> Task<Void, Never>? {
        guard canStart else { return nil }
        let previousID = preparationID
        generation = UUID()
        let current = generation
        let id = UUID().uuidString
        startedAt = now()
        deadline = now().addingTimeInterval(330)
        clearSecrets()
        phase = .preparing
        stage = "waiting"
        error = nil
        busy = true
        action = Task { [self] in
            defer { if current == generation { busy = false } }
            if let previousID, !(await stop(previousID)) {
                guard current == generation else { return }
                preparationID = previousID
                phase = .failed
                error = "The previous preparation has not stopped yet. Keep Croptop open and try again."
                return
            }
            guard current == generation, !Task.isCancelled else { return }
            preparationID = id
            do {
                let value = try await client.prepare(siteID: site.id, id: id, enableHosting: true)
                guard current == generation, !Task.isCancelled else {
                    try? await client.cancel(siteID: site.id, id: id)
                    return
                }
                try accept(value, id: id)
            } catch {
                guard current == generation, !Task.isCancelled else { return }
                phase = .failed
                self.error = message(for: error)
            }
        }
        return action
    }

    func monitor() async {
        while !Task.isCancelled && phase != .closed {
            await refresh()
            do { try await Task.sleep(nanoseconds: 1_500_000_000) }
            catch { return }
        }
    }

    func refresh() async {
        guard !refreshing, !busy, phase != .closed else { return }
        if let startedAt { elapsed = max(0, Int(now().timeIntervalSince(startedAt))) }
        if let pairing, now().timeIntervalSince1970 >= pairing.expiresAt,
           [.scan, .confirm, .sent].contains(phase) {
            expire()
            return
        }
        if phase == .preparing, let deadline, now() >= deadline {
            phase = .failed
            error = "Preparation took too long. Your publication may still be uploading. Keep this Mac awake, then try again."
            if let id = preparationID { try? await client.cancel(siteID: site.id, id: id) }
            return
        }
        let current = generation
        refreshing = true
        defer { refreshing = false }
        do {
            if phase == .preparing, let id = preparationID {
                let value = try await client.preparation(siteID: site.id, id: id)
                guard current == generation, !Task.isCancelled else { return }
                try accept(value, id: id)
            } else if [.scan, .confirm, .sent].contains(phase), let pairing {
                let value = try await client.status(siteID: site.id, pairingID: pairing.id)
                guard current == generation, !Task.isCancelled else { return }
                guard value.ipns == site.ipns, value.expiresAt == pairing.expiresAt else {
                    throw PhoneConnectionError(status: 409, code: "identity_changed")
                }
                if now().timeIntervalSince1970 >= value.expiresAt { expire(); return }
                switch value.state {
                case "claimed": if phase != .sent { phase = .confirm }
                case "ready": phase = .sent; clearSecrets(keepPairing: true)
                case "consumed": phase = .delivered; clearSecrets()
                case "waiting": break
                default: throw PhoneConnectionError(status: 409, code: "invalid_response")
                }
                error = nil
            }
        } catch {
            guard current == generation, !Task.isCancelled else { return }
            if let problem = error as? PhoneConnectionError, [404, 410].contains(problem.status) {
                expire()
            } else if ["identity_changed", "invalid_response"].contains((error as? PhoneConnectionError)?.code ?? "") {
                clearSecrets(); phase = .failed; self.error = message(for: error)
            } else {
                // Keep the same attempt after a transient read error. Creating a
                // second publication/key exchange is never an automatic retry.
                self.error = "Could not check progress. Retrying this connection…"
            }
        }
    }

    @discardableResult func confirm() -> Task<Void, Never>? {
        guard canConfirm, let pairing else { return nil }
        let current = generation
        let entered = normalizedCode
        busy = true
        error = nil
        action = Task { [self] in
            defer { if current == generation { busy = false } }
            do {
                try await client.confirm(siteID: site.id, pairingID: pairing.id, code: entered)
                guard current == generation, !Task.isCancelled else { return }
                guard phase == .confirm, self.pairing?.id == pairing.id else { return }
                if now().timeIntervalSince1970 >= pairing.expiresAt { expire() }
                else { phase = .sent; clearSecrets(keepPairing: true) }
            } catch {
                guard current == generation, !Task.isCancelled else { return }
                guard phase == .confirm, self.pairing?.id == pairing.id else { return }
                if let problem = error as? PhoneConnectionError, [404, 410].contains(problem.status) { expire() }
                else { self.error = "Could not confirm. Check the eight-digit code on your phone and try again. If you already confirmed, wait for the phone to finish." }
            }
        }
        return action
    }

    func copyLink() {
        guard [.scan, .confirm].contains(phase), let pairing,
              pairing.expiresAt > now().timeIntervalSince1970 else { return }
        copied = clipboard.copy(pairing.url)
        if !copied { error = "Could not copy the private link. Scan the QR code instead." }
    }

    /// Dismissal invalidates callbacks before awaiting best-effort cleanup.
    /// Already published content or a dispatched key transfer is not revoked.
    @discardableResult func close() async -> Bool {
        if phase == .closed { return true }
        if phase == .stopping && busy { return false }
        generation = UUID()
        action?.cancel()
        action = nil
        let id = preparationID
        phase = .stopping
        busy = true
        error = nil
        clearSecrets()
        defer { busy = false }
        guard let id else { phase = .closed; return true }
        if await stop(id) {
            preparationID = nil
            phase = .closed
            return true
        }
        error = "Could not verify that preparation stopped. Keep Croptop open and choose Close to retry. Any publication or key transfer already sent is not undone."
        return false
    }

    private func stop(_ id: String) async -> Bool {
        do {
            try await client.cancel(siteID: site.id, id: id)
            let stopDeadline = Date().addingTimeInterval(25)
            while Date() < stopDeadline {
                let value = try await client.preparation(siteID: site.id, id: id)
                guard value.id == id, value.siteID == site.id else { throw PhoneConnectionError(status: 0, code: "identity_changed") }
                if ["cancelled", "failed"].contains(value.state) {
                    return true
                }
                try await Task.sleep(nanoseconds: 500_000_000)
            }
        } catch let problem as PhoneConnectionError where problem.status == 404 {
            return true
        } catch {}
        return false
    }

    private func accept(_ value: PhonePreparation, id: String) throws {
        guard value.id == id, value.siteID == site.id, value.deadline.isFinite, value.startedAt.isFinite,
              value.deadline >= value.startedAt, value.deadline <= now().timeIntervalSince1970 + 660 else {
            throw PhoneConnectionError(status: 409, code: "invalid_response")
        }
        deadline = Date(timeIntervalSince1970: value.deadline)
        stage = value.stage
        error = nil
        switch value.state {
        case "preparing": phase = .preparing
        case "ready":
            guard let connection = value.connection, connection.ipns == site.ipns,
                  Self.valid(connection, now: now()) else {
                throw PhoneConnectionError(status: 409, code: "identity_changed")
            }
            pairing = connection
            phase = .scan
        case "failed":
            phase = .failed
            error = message(for: PhoneConnectionError(status: 409, code: value.code))
        case "cancelled": phase = .failed; error = "Connection preparation stopped. You can try again when your phone is ready."
        case "cancelling": phase = .stopping
        default: throw PhoneConnectionError(status: 409, code: "invalid_response")
        }
    }

    static func valid(_ pairing: PhonePairing, now: Date) -> Bool {
        guard pairing.expiresAt.isFinite, pairing.expiresAt > now.timeIntervalSince1970,
              pairing.expiresAt <= now.timeIntervalSince1970 + 660,
              opaqueToken(pairing.id), let url = URLComponents(string: pairing.url),
              url.scheme == "https", url.host?.isEmpty == false,
              url.user == nil, url.password == nil, url.query == nil,
              url.path == "/", let fragment = url.fragment, fragment.hasPrefix("pair=") else { return false }
        let parts = fragment.dropFirst(5).split(separator: ".", omittingEmptySubsequences: false)
        return parts.count == 2 && parts[0] == pairing.id && opaqueToken(String(parts[1]))
    }

    private static func opaqueToken(_ text: String) -> Bool {
        guard text.utf8.count == 43,
              text.utf8.allSatisfy({ (65...90).contains($0) || (97...122).contains($0) || (48...57).contains($0) || $0 == 45 || $0 == 95 }),
              let data = Data(base64Encoded: text.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/") + "=") else { return false }
        return data.count == 32 && data.base64EncodedString().replacingOccurrences(of: "+", with: "-").replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "=", with: "") == text
    }

    private func expire() {
        clearSecrets()
        phase = .expired
        error = nil
    }

    private func clearSecrets(keepPairing: Bool = false) {
        code = ""
        copied = false
        clipboard.clearIfOwned()
        if !keepPairing { pairing = nil }
        else if let value = pairing {
            pairing = PhonePairing(id: value.id, url: "", expiresAt: value.expiresAt, state: value.state, ipns: value.ipns, name: value.name)
        }
    }

    private func message(for error: Error) -> String {
        guard let problem = error as? PhoneConnectionError else {
            return "Could not prepare this connection. Keep Croptop open, check your internet connection and try again."
        }
        switch problem.code {
        case "hosting_required": return "Phone posting requires your permission to host this site on crop.top."
        case "unsupported_host": return "Phone posting currently supports sites hosted on crop.top. Your custom hosting settings have not been changed."
        case "rate_limited": return "The phone service is receiving too many requests. Wait a moment, then try again."
        case "service_timeout": return "The phone or hosting service took too long to respond. Keep this Mac awake and try again to check the published site."
        case "preparation_timeout": return "Preparing this site took too long. Keep this Mac awake and try again."
        case "site_not_ready": return "The phone service could not verify this hosted site. Try again; if this continues, check the site’s template and hosting settings."
        case "publication_required", "hosted_publication_required": return "This site needs to be published with phone support first. Publish it from Croptop, then try connecting again."
        case "hosting_update_required": return "The hosting service needs an update before it can safely connect this site. Try again after the service is updated."
        case "publication_outcome_unknown": return "Hosting has not confirmed this publication yet. Keep this Mac awake and wait before trying again. Avoid publishing from another device in the meantime."
        case "publication_conflict", "preparation_in_progress": return "Another publication or connection is in progress for this site. Let it finish, then try again."
        case "site_changed", "identity_changed", "invalid_response": return "The connection details did not match this site. Close this window and try again."
        default:
            if problem.status == 404 { return "This publisher needs the latest Croptop update before it can connect a phone here." }
            return "Could not prepare this connection. Your site may still be uploading. Keep this Mac awake and try again."
        }
    }
}

/// Mark the explicit clipboard copy private; clear only our unchanged copy.
@MainActor final class PhoneConnectionClipboard {
    private let pasteboard: NSPasteboard
    private var changeCount: Int?
    init(pasteboard: NSPasteboard = .general) { self.pasteboard = pasteboard }
    func copy(_ link: String) -> Bool {
        let item = NSPasteboardItem()
        item.setString(link, forType: .string)
        item.setData(Data(), forType: NSPasteboard.PasteboardType("org.nspasteboard.ConcealedType"))
        item.setData(Data(), forType: NSPasteboard.PasteboardType("org.nspasteboard.TransientType"))
        pasteboard.clearContents()
        guard pasteboard.writeObjects([item]) else { return false }
        changeCount = pasteboard.changeCount
        return true
    }
    func clearIfOwned() {
        if changeCount == pasteboard.changeCount { pasteboard.clearContents() }
        changeCount = nil
    }
}
