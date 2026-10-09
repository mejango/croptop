import SwiftUI
import CroptopMobileCore

@MainActor
final class ComposerModel: ObservableObject {
    @Published var drafts: [LocalDraft] = []
    @Published var connection: SiteConnection?
    @Published var selectedID: String?
    @Published var busy = false
    @Published var message: String?
    @Published var fatalStorageError: String?
    @Published var pairingCode: String?
    @Published var pairingIPNS: String?
    @Published var previewImage: UIImage?
    private(set) var store: DraftStore?
    private var pairing: PairingReceiver?
    private var api: MobileAPI?

    var selected: LocalDraft? { drafts.first { $0.id == selectedID } }

    init() {
        do { store = try DeviceStorage.drafts(); try reload() }
        catch { fatalStorageError = error.localizedDescription }
    }

    func reload() throws {
        guard let store else { return }
        drafts = try store.list()
        let savedConnection = try store.connection()
        if savedConnection?.ipns != connection?.ipns || savedConnection?.origin != connection?.origin { api = nil }
        connection = savedConnection
        if selectedID == nil { selectedID = drafts.first(where: { $0.operation?.state != .published })?.id }
        updatePreview()
    }

    func select(_ id: String) { selectedID = id; message = nil; updatePreview() }

    func capture(_ data: Data) throws {
        guard let store else { throw MobileError.storage("Draft storage is unavailable.") }
        let type = try DeviceStorage.inspectImage(data)
        let draft = try store.create(image: data, contentType: type, destination: connection)
        selectedID = draft.id; try reload(); message = "Saved on this phone."
    }

    func replaceImage(_ data: Data) throws {
        guard let store, let selected else { return }
        let type = try DeviceStorage.inspectImage(data)
        try store.replaceImage(data, contentType: type, for: selected.id)
        try reload()
    }

    func save(title: String, caption: String, assignDestination: Bool = false) throws {
        guard let store, let selected else { return }
        guard !selected.submitted else { return }
        _ = try store.edit(selected.id, title: title, caption: caption,
                           destination: selected.destination ?? (assignDestination ? connection : nil))
        try reload()
    }

    func importKey(pem: String, originText: String) async {
        await perform {
            guard let url = URL(string: originText) else { throw MobileError.invalid("Enter the publishing service's HTTPS address.") }
            try await self.connect(identity: SiteIdentity(pem: pem), origin: url, fallbackName: "Your site")
        }
    }

    private func connect(identity: SiteIdentity, origin: URL, fallbackName: String) async throws {
        guard let store else { throw MobileError.storage("Draft storage is unavailable.") }
        if let current = connection, current.ipns != identity.ipns {
            throw MobileError.invalid("Remove the current connection before connecting another site. Existing drafts keep their destination.")
        }
        let client = try MobileAPI(origin: origin, identity: identity)
        let config = try await client.configuration()
        guard config.enabled else { throw MobileError.invalid("Phone posting is currently unavailable at this service.") }
        let status = try await client.site()
        guard status.ready else { throw MobileError.invalid(status.reason ?? "Republish this site from the updated Croptop publisher with hosted storage enabled.") }
        try DeviceStorage.saveIdentity(identity)
        let connected = SiteConnection(origin: try MobileAPI.validatedOrigin(origin), ipns: identity.ipns,
            name: status.name.isEmpty ? fallbackName : status.name, url: status.url, enabled: status.enabled)
        try store.setConnection(connected); api = client; try reload()
        message = status.enabled ? "Site connected." : "Site connected. Enable phone posting to give this service permission."
    }

    func claimPairing(link: String) async {
        await perform {
            let receiver = try PairingReceiver(link: link)
            let result = try await receiver.claim()
            if let current = self.connection, current.ipns != result.ipns {
                throw MobileError.invalid("Remove the existing connection before connecting a different site.")
            }
            self.pairing = receiver; self.pairingCode = result.code; self.pairingIPNS = result.ipns
            self.message = "Enter this code on your publisher. Then confirm here that the site and code match."
        }
    }

    func confirmPairing() async {
        await perform {
            guard let pairing = self.pairing else { throw MobileError.invalid("Paste a new connection link.") }
            let received = try await pairing.consumeConfirmed()
            // Securely retain the successfully transferred key before a network
            // readiness lookup; a transient error must not destroy a consumed transfer.
            try DeviceStorage.saveIdentity(received.identity)
            guard let store = self.store else { throw MobileError.storage("Draft storage is unavailable.") }
            if let current = self.connection, current.ipns != received.identity.ipns {
                throw MobileError.invalid("Remove the existing connection before connecting a different site.")
            }
            try store.setConnection(SiteConnection(origin: received.origin, ipns: received.identity.ipns, name: received.name))
            try self.reload()
            self.pairing = nil; self.pairingCode = nil; self.pairingIPNS = nil
            try await self.connect(identity: received.identity, origin: received.origin, fallbackName: received.name)
        }
    }

    func enable(_ enabled: Bool) async {
        await perform {
            let client = try self.client()
            let status = try await client.setEnabled(enabled)
            guard status.ipns == self.connection?.ipns else { throw MobileError.invalid("The service returned another site.") }
            self.connection?.enabled = status.enabled
            try self.store?.setConnection(self.connection); try self.reload()
            self.message = enabled ? "Phone posting enabled." : "Phone posting stopped. Previously published posts stay published."
        }
    }

    func removeConnection() {
        do {
            if let connection { try DeviceStorage.removeIdentity(ipns: connection.ipns) }
            try store?.setConnection(nil); api = nil; try reload()
            message = "Connection removed from this phone. Drafts are retained; reconnect the same site to publish them."
        } catch { message = error.localizedDescription }
    }

    func startNewAfterRejection() {
        do {
            guard let store, let selected else { return }
            let replacement = try store.startNewAfterRejection(selected.id)
            selectedID = replacement.id; try reload()
            message = "A new draft is ready. You can replace its image. The original operation is kept in your history."
        } catch { message = error.localizedDescription }
    }

    /// First submission or recovery. Every retry uses the persisted operation ID.
    func prepare(title: String, caption: String, confirmDestination: Bool) async {
        await perform {
            guard let store = self.store, var draft = self.selected else { return }
            try self.save(title: title, caption: caption, assignDestination: confirmDestination)
            draft = try store.load(draft.id)
            let client = try self.client(for: draft)
            let config = try await client.configuration()
            let attempt = try store.beginSubmissionAttempt(draft.id) { saved, image in
                if !saved.submitted {
                    try MobileAPI.validateUpload(saved, image: image, configuration: config)
                    try DeviceStorage.validatePixels(image, maxPixels: config.maxImagePixels)
                }
            }
            let wasSubmitted = attempt.wasSubmitted
            let image = attempt.image
            draft = attempt.draft
            try self.reload()
            let operation: MobileOperation
            if wasSubmitted {
                do { operation = try await client.status(id: draft.id) }
                catch MobileError.service(404, _, _) { operation = try await client.upload(draft, image: image, configuration: config) }
            } else {
                do { operation = try await client.upload(draft, image: image, configuration: config) }
                catch MobileError.service(let status, let code, let message) {
                    if ["invalid_text", "invalid_upload", "image_too_large", "invalid_request", "invalid_id"].contains(code) {
                        try store.retainRejectedLocalDraft(draft.id)
                    }
                    throw MobileError.service(status, code, message)
                }
            }
            _ = try store.record(operation, for: draft.id)
            try await self.handle(operation, draft: draft, client: client)
        }
    }

    func refresh() async {
        await perform {
            guard let draft = self.selected else { return }
            let client = try self.client(for: draft)
            _ = try await client.configuration()
            let operation = try await client.status(id: draft.id)
            _ = try self.store?.record(operation, for: draft.id)
            try await self.handle(operation, draft: draft, client: client)
        }
    }

    func publishReviewedPreview() async {
        await perform {
            guard let store = self.store, let draft = self.selected, let operation = draft.operation,
                  operation.state == .needsSignature else { return }
            let client = try self.client(for: draft)
            _ = try await client.configuration()
            // Send signatures only for the exact preview currently on screen.
            let image = try Data(contentsOf: store.previewURL(draft))
            guard let proposal = operation.proposal else { throw MobileError.invalid("Refresh the prepared preview.") }
            try store.beginCommit(draft.id, proposalID: proposal.id)
            try self.reload()
            var result = try await client.commit(operation, draft: draft, image: image)
            _ = try store.record(result, for: draft.id)
            result = try await self.pollWhileWorking(result, draft: draft, client: client)
            try self.reload()
            self.message = result.state == .published ? "Published." : "Publication is pending. You can close this view and check its status in Croptop."
        }
    }

    private func handle(_ original: MobileOperation, draft: LocalDraft, client: MobileAPI) async throws {
        guard let store else { return }
        var operation = try await pollWhileWorking(original, draft: draft, client: client)
        if ["draft_expired", "image_invalid", "heif_unavailable"].contains(operation.code ?? "") {
            message = operation.error ?? "This upload could not be published. Start a corrected draft from the image retained on your phone."
            try reload(); return
        }
        if operation.state == .failed || (operation.state == .needsSignature && (operation.proposal?.expiresAt ?? 0) <= Int64(Date().timeIntervalSince1970) + 15) {
            operation = try await client.prepare(id: draft.id)
            _ = try store.record(operation, for: draft.id)
            operation = try await pollWhileWorking(operation, draft: draft, client: client)
        }
        if operation.state == .needsSignature {
            let image = try await client.normalizedImage(id: draft.id)
            _ = try await client.validatedProposal(operation, draft: draft, image: image)
            try store.savePreview(image, for: draft)
            message = "Review the prepared image and text, then publish."
        } else if operation.state == .published { message = "Published." }
        else { message = "Your post is pending. Check status to continue; it is safe to close this view." }
        try reload()
    }

    private func pollWhileWorking(_ original: MobileOperation, draft: LocalDraft, client: MobileAPI) async throws -> MobileOperation {
        var operation = original
        for _ in 0..<12 {
            guard operation.state == .preparing || operation.state == .committing else { break }
            try reload()
            try await Task.sleep(for: .seconds(1))
            operation = try await client.status(id: draft.id)
            _ = try store?.record(operation, for: draft.id)
        }
        return operation
    }

    private func client(for draft: LocalDraft? = nil) throws -> MobileAPI {
        guard let connection else { throw MobileError.invalid("Open Croptop and connect your site. This image is saved as a draft.") }
        if let destination = draft?.destination, destination.ipns != connection.ipns || destination.origin != connection.origin {
            throw MobileError.invalid("Reconnect this draft's original site to publish it.")
        }
        if let api { return api }
        let newAPI = try MobileAPI(origin: connection.origin, identity: DeviceStorage.identity(ipns: connection.ipns))
        api = newAPI; return newAPI
    }

    private func updatePreview() {
        guard let draft = selected, let store else { previewImage = nil; return }
        let usePrepared = draft.operation?.state == .needsSignature
        previewImage = DeviceStorage.thumbnail(url: usePrepared ? store.previewURL(draft) : store.imageURL(draft))
    }

    private func perform(_ body: () async throws -> Void) async {
        guard !busy else { return }
        let operationDraftID = selectedID
        busy = true; message = nil
        defer { busy = false }
        do { try await body() }
        catch {
            message = error.localizedDescription
            let code: String?
            if case MobileError.service(_, let serviceCode, _) = error { code = serviceCode } else { code = nil }
            if let operationDraftID { try? store?.noteError(error.localizedDescription, code: code, for: operationDraftID) }
            try? reload()
        }
    }
}
