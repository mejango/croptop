import AppKit
import Foundation
import SwiftUI
import Vision
import XCTest
@testable import Croptop

@MainActor
private final class PhoneClientFixture: PhoneConnectionClient {
    let date: Date
    var prepared: [(String, String, Bool, Bool)] = []
    var cancelled: [(String, String)] = []
    var confirmed: [(String, String, String)] = []
    var prepareBody: ((String, String) async throws -> PhonePreparation)?
    var preparationBody: ((String, String) async throws -> PhonePreparation)?
    var statusBody: (() async throws -> PhonePairingStatus)?
    var confirmBody: (() async throws -> Void)?
    init(date: Date) { self.date = date }
    static let site = Site(id: "11111111-2222-3333-4444-555555555555", name: "Private site", ipns: "k51fixture")
    static let token = String(repeating: "A", count: 43)
    static let phoneHost = "croptop-phone-923c1bafd14ea328.fixture.workers.example"
    var pair: PhonePairing {
        PhonePairing(id: Self.token,
            url: "https://\(Self.phoneHost)/#pair=\(Self.token).\(Self.token)",
            expiresAt: date.timeIntervalSince1970 + 600, state: "waiting", ipns: Self.site.ipns, name: Self.site.name)
    }
    func result(siteID: String, id: String, state: String = "ready", connection: PhonePairing? = nil) -> PhonePreparation {
        PhonePreparation(id: id, siteID: siteID, state: state, stage: "pairing",
            startedAt: date.timeIntervalSince1970, deadline: date.timeIntervalSince1970 + 300,
            connection: state == "ready" ? (connection ?? pair) : nil)
    }
    func prepare(siteID: String, id: String, enableHosting: Bool, allowPublish: Bool) async throws -> PhonePreparation {
        prepared.append((siteID, id, enableHosting, allowPublish))
        if let prepareBody { return try await prepareBody(siteID, id) }
        return result(siteID: siteID, id: id)
    }
    func preparation(siteID: String, id: String) async throws -> PhonePreparation {
        if let preparationBody { return try await preparationBody(siteID, id) }
        return result(siteID: siteID, id: id, state: cancelled.contains { $0.1 == id } ? "cancelled" : "preparing")
    }
    func cancel(siteID: String, id: String) async throws { cancelled.append((siteID, id)) }
    func status(siteID: String, pairingID: String) async throws -> PhonePairingStatus {
        if let statusBody { return try await statusBody() }
        return PhonePairingStatus(state: "claimed", ipns: Self.site.ipns, expiresAt: pair.expiresAt)
    }
    func confirm(siteID: String, pairingID: String, code: String) async throws {
        confirmed.append((siteID, pairingID, code))
        try await confirmBody?()
    }
}

final class PhoneConnectionTests: XCTestCase {
    private enum FixtureError: Error { case callbackTimedOut }
    @MainActor private func waitUntil(_ predicate: () -> Bool, file: StaticString = #filePath, line: UInt = #line) async throws {
        let deadline = Date().addingTimeInterval(3)
        while !predicate(), Date() < deadline { try await Task.sleep(nanoseconds: 1_000_000) }
        guard predicate() else {
            XCTFail("Expected fixture callback within three seconds", file: file, line: line)
            throw FixtureError.callbackTimedOut
        }
    }

    @MainActor private func fixture(storage: String? = nil) -> (PhoneConnectionModel, PhoneClientFixture, NSPasteboard) {
        let date = Date()
        let client = PhoneClientFixture(date: date)
        let pasteboard = NSPasteboard(name: .init("CroptopPhoneTests." + UUID().uuidString))
        var site = PhoneClientFixture.site
        site.croptopStorage = storage
        let model = PhoneConnectionModel(site: site, client: client,
            now: { date }, clipboard: PhoneConnectionClipboard(pasteboard: pasteboard))
        return (model, client, pasteboard)
    }

    @MainActor private func render(_ model: PhoneConnectionModel, embedded: Bool = false) throws -> NSBitmapImageRep {
        _ = NSApplication.shared
        AppFonts.register()
        let content: AnyView
        if embedded {
            content = AnyView(ScrollView {
                PhoneConnectionView(model: model, presentation: .settings, onClose: {}, onCleanup: {})
                    .padding(Theme.content)
            })
        } else { content = AnyView(PhoneConnectionSheet(model: model)) }
        let view = NSHostingView(rootView: content.environmentObject(AppModel()))
        view.frame = NSRect(x: 0, y: 0, width: 560, height: 660)
        view.layoutSubtreeIfNeeded()
        let bitmap = try XCTUnwrap(view.bitmapImageRepForCachingDisplay(in: view.bounds))
        view.cacheDisplay(in: view.bounds, to: bitmap)
        return bitmap
    }

    @MainActor private func visibleText(_ model: PhoneConnectionModel) throws -> [String] {
        let bitmap = try render(model)
        let request = VNRecognizeTextRequest()
        request.recognitionLevel = .accurate
        request.recognitionLanguages = ["en-US"]
        try VNImageRequestHandler(cgImage: XCTUnwrap(bitmap.cgImage)).perform([request])
        return (request.results ?? []).compactMap { $0.topCandidates(1).first?.string }
    }

    @MainActor func testExplicitConsentAndSiteIdentityBeforePreparing() async {
        let (model, client, _) = fixture()
        XCTAssertFalse(model.canStart)
        XCTAssertNil(model.begin())
        XCTAssertTrue(client.prepared.isEmpty)
        model.consent = true
        await model.begin()?.value
        XCTAssertEqual(model.phase, .scan)
        XCTAssertEqual(client.prepared.count, 1)
        XCTAssertEqual(client.prepared.first?.0, PhoneClientFixture.site.id)
        XCTAssertEqual(client.prepared.first?.2, true)
        XCTAssertEqual(client.prepared.first?.3, false, "Hosting consent does not authorize publishing saved changes")
        XCTAssertNil(model.begin(), "A live pairing cannot start a duplicate preparation")
    }

    @MainActor func testSavedHostingPermissionConnectsWithoutNewConsentOrPublication() async {
        let (model, client, _) = fixture(storage: SiteStorage.hosted.rawValue)
        XCTAssertFalse(model.consent)
        XCTAssertFalse(model.needsHostingConsent)
        XCTAssertTrue(model.canStart)
        XCTAssertFalse(model.canPublish)
        XCTAssertNil(model.publishAndConnect())
        await model.begin()?.value
        XCTAssertEqual(model.phase, .scan)
        XCTAssertEqual(client.prepared.count, 1)
        XCTAssertEqual(client.prepared.first?.2, false, "Do not rewrite the site's saved hosting choice")
        XCTAssertEqual(client.prepared.first?.3, false, "Connecting an existing hosted site must not publish local edits")
    }

    @MainActor func testMissingUnknownAndP2PStorageDoNotInferHostingConsent() async {
        for storage in [nil, "", "unknown", SiteStorage.p2p.rawValue] as [String?] {
            let (model, client, _) = fixture(storage: storage)
            XCTAssertTrue(model.needsHostingConsent, storage ?? "missing")
            XCTAssertFalse(model.canStart)
            XCTAssertNil(model.begin())
            XCTAssertNil(model.publishAndConnect())
            XCTAssertTrue(client.prepared.isEmpty)
            model.consent = true
            await model.begin()?.value
            XCTAssertEqual(client.prepared.first?.2, true)
            XCTAssertEqual(client.prepared.first?.3, false)
        }
    }

    @MainActor func testPublicationRequiresDistinctExplicitActionAndNewPreparationID() async {
        for storage in [SiteStorage.hosted.rawValue, SiteStorage.p2p.rawValue] {
            for delivery in ["prepare", "poll", "error", "pollError"] {
                let (model, client, _) = fixture(storage: storage)
                model.consent = model.needsHostingConsent
                client.prepareBody = { siteID, id in
                    if client.prepared.count > 1 { return client.result(siteID: siteID, id: id) }
                    if delivery == "error" { throw PhoneConnectionError(status: 409, code: "publication_required") }
                    var value = client.result(siteID: siteID, id: id, state: delivery.hasPrefix("poll") ? "preparing" : "failed")
                    value.code = "publication_required"
                    return value
                }
                client.preparationBody = { siteID, id in
                    if delivery == "pollError", !client.cancelled.contains(where: { $0.1 == id }) {
                        throw PhoneConnectionError(status: 409, code: "publication_required")
                    }
                    var value = client.result(siteID: siteID, id: id,
                        state: client.cancelled.contains { $0.1 == id } ? "cancelled" : "failed")
                    value.code = "publication_required"
                    return value
                }
                await model.begin()?.value
                if delivery.hasPrefix("poll") { await model.refresh() }
                XCTAssertEqual(model.phase, .publicationRequired, "\(storage): \(delivery)")
                XCTAssertNil(model.error, "A prerequisite is a choice, not a generic failure")
                XCTAssertFalse(model.canStart)
                XCTAssertTrue(model.canPublish)
                XCTAssertNil(model.begin())
                await model.refresh()
                XCTAssertEqual(client.prepared.count, 1, "Never automatically publish after discovering the prerequisite")
                XCTAssertEqual(client.prepared[0].3, false)
                let firstID = client.prepared[0].1
                await model.publishAndConnect()?.value
                XCTAssertEqual(model.phase, .scan)
                XCTAssertEqual(client.prepared.count, 2)
                guard client.prepared.count == 2 else { continue }
                XCTAssertNotEqual(client.prepared[1].1, firstID, "Changed consent requires a new idempotency identity")
                XCTAssertEqual(client.prepared[1].2, storage == SiteStorage.p2p.rawValue)
                XCTAssertTrue(client.prepared[1].3)
                XCTAssertTrue(client.cancelled.contains { $0.1 == firstID })
                XCTAssertNil(model.publishAndConnect(), "An active pairing cannot publish again")
            }
        }
    }

    @MainActor func testServerHostingRequirementDiscardsStalePermissionAndAsksAgain() async {
        for delivery in ["prepare", "poll", "error", "pollError"] {
            let (model, client, _) = fixture(storage: SiteStorage.hosted.rawValue)
            model.consent = true // Even an old checked UI value is not permission to reverse a later opt-out.
            client.prepareBody = { siteID, id in
                if client.prepared.count > 1 { return client.result(siteID: siteID, id: id) }
                if delivery == "error" { throw PhoneConnectionError(status: 409, code: "hosting_required") }
                var value = client.result(siteID: siteID, id: id, state: delivery.hasPrefix("poll") ? "preparing" : "failed")
                value.code = "hosting_required"
                return value
            }
            client.preparationBody = { siteID, id in
                if delivery == "pollError", !client.cancelled.contains(where: { $0.1 == id }) {
                    throw PhoneConnectionError(status: 409, code: "hosting_required")
                }
                var value = client.result(siteID: siteID, id: id,
                    state: client.cancelled.contains { $0.1 == id } ? "cancelled" : "failed")
                value.code = "hosting_required"
                return value
            }
            await model.begin()?.value
            if delivery.hasPrefix("poll") { await model.refresh() }
            XCTAssertTrue(model.needsHostingConsent, delivery)
            XCTAssertFalse(model.consent)
            XCTAssertFalse(model.canStart)
            XCTAssertFalse(model.canPublish)
            XCTAssertNil(model.begin())
            XCTAssertEqual(client.prepared.count, 1)
            XCTAssertEqual(client.prepared[0].2, false)
            model.consent = true
            await model.begin()?.value
            XCTAssertEqual(model.phase, .scan)
            XCTAssertEqual(client.prepared.last?.2, true)
            XCTAssertEqual(client.prepared.last?.3, false)
        }
    }

    @MainActor func testPublicationConsentDoesNotCarryIntoRetryAfterServiceFailure() async {
        let (model, client, _) = fixture(storage: SiteStorage.hosted.rawValue)
        client.prepareBody = { siteID, id in
            var value = client.result(siteID: siteID, id: id, state: "failed")
            value.code = client.prepared.count == 1 ? "publication_required" : "service_timeout"
            return value
        }
        await model.begin()?.value
        await model.publishAndConnect()?.value
        XCTAssertEqual(model.phase, .failed)
        XCTAssertFalse(model.canPublish)
        XCTAssertTrue(model.canStart)
        await model.begin()?.value
        XCTAssertEqual(client.prepared.map { $0.3 }, [false, true, false])
        XCTAssertEqual(Set(client.prepared.map { $0.1 }).count, 3)
    }

    @MainActor func testUnrelatedFailureCannotAuthorizePublication() async {
        for code in ["site_not_ready", "service_timeout", "hosted_publication_required", "hosting_update_required", "site_changed"] {
            let (model, client, _) = fixture(storage: SiteStorage.hosted.rawValue)
            client.prepareBody = { siteID, id in
                var value = client.result(siteID: siteID, id: id, state: "failed")
                value.code = code
                return value
            }
            await model.begin()?.value
            XCTAssertEqual(model.phase, .failed, code)
            XCTAssertFalse(model.canPublish, code)
            XCTAssertNil(model.publishAndConnect(), code)
            XCTAssertEqual(client.prepared.count, 1)
            XCTAssertFalse(client.prepared[0].3)
        }
    }

    @MainActor func testPublicationActionStillRequiresAnyMissingHostingPermission() async {
        let (model, client, _) = fixture(storage: SiteStorage.p2p.rawValue)
        model.consent = true
        client.prepareBody = { siteID, id in
            var value = client.result(siteID: siteID, id: id, state: "failed")
            value.code = "publication_required"
            return value
        }
        await model.begin()?.value
        XCTAssertTrue(model.canPublish)
        model.consent = false
        XCTAssertFalse(model.canPublish)
        XCTAssertNil(model.publishAndConnect())
        XCTAssertEqual(client.prepared.count, 1)
    }

    @MainActor func testRenderedHostedSheetUsesShortCopyWithoutRedundantHostingPrompt() throws {
        let (model, _, _) = fixture(storage: SiteStorage.hosted.rawValue)
        let lines = try visibleText(model)
        let text = lines.joined(separator: " ")
        XCTAssertTrue(lines.contains("Post from your phone"), text)
        XCTAssertTrue(lines.contains("Connect"), text)
        XCTAssertFalse(text.contains("anywhere"), text)
        XCTAssertFalse(text.contains("Connect your published site"), text)
        XCTAssertFalse(text.contains("Allow crop.top"), text)
        XCTAssertFalse(text.contains("Keep this Mac"), text)
        XCTAssertFalse(text.contains("does not revoke"), text)
        XCTAssertTrue(text.contains("publishing key"), "Keep the distinct phone-key disclosure: \(text)")
    }

    @MainActor func testRenderedUnhostedSheetOnlyAsksForHostingPermission() throws {
        let (model, _, _) = fixture(storage: SiteStorage.p2p.rawValue)
        let text = try visibleText(model).joined(separator: " ")
        XCTAssertTrue(text.contains("Post from your phone"), text)
        XCTAssertTrue(text.contains("Allow crop.top to host this site."), text)
        XCTAssertFalse(text.contains("and publish it"), text)
        XCTAssertFalse(text.contains("Connect your published site"), text)
    }

    @MainActor func testRenderedPublicationPromptSeparatesSavedChangesFromHostingConsent() async throws {
        let (model, client, _) = fixture(storage: SiteStorage.hosted.rawValue)
        client.prepareBody = { siteID, id in
            var value = client.result(siteID: siteID, id: id, state: "failed")
            value.code = "publication_required"
            return value
        }
        await model.begin()?.value
        let text = try visibleText(model).joined(separator: " ")
        XCTAssertTrue(text.contains("Publish and connect"), text)
        XCTAssertTrue(text.contains("saved changes"), text)
        XCTAssertFalse(text.contains("Allow crop.top"), text)
        _ = await model.close()
    }

    @MainActor func testMismatchedPreparationOrPairingCannotShowQR() async {
        for mismatch in ["site", "job", "ipns"] {
            let (model, client, _) = fixture()
            client.prepareBody = { siteID, id in
                let pair = client.pair
                let wrong = PhonePairing(id: pair.id, url: pair.url, expiresAt: pair.expiresAt,
                    state: pair.state, ipns: "other-site", name: pair.name)
                return client.result(siteID: mismatch == "site" ? "other" : siteID,
                    id: mismatch == "job" ? "other" : id, connection: mismatch == "ipns" ? wrong : pair)
            }
            model.consent = true
            await model.begin()?.value
            XCTAssertEqual(model.phase, .failed, mismatch)
            XCTAssertNil(model.pairing, mismatch)
            XCTAssertTrue(client.confirmed.isEmpty)
        }
    }

    @MainActor func testOnlyEightASCIIDigitsCanAuthorizeTransfer() async {
        let (model, client, _) = fixture()
        model.consent = true
        await model.begin()?.value
        await model.refresh()
        XCTAssertEqual(model.phase, .confirm)
        for invalid in ["123456", "123456789", "１２３４５６７８", "1234\t5678", "1234567a"] {
            model.code = invalid
            XCTAssertFalse(model.canConfirm, invalid)
            XCTAssertNil(model.confirm())
        }
        model.code = " 1234 5678 "
        XCTAssertTrue(model.canConfirm)
        await model.confirm()?.value
        XCTAssertEqual(client.confirmed.first?.2, "12345678")
        XCTAssertEqual(model.phase, .sent)
        XCTAssertEqual(model.code, "")
        XCTAssertEqual(model.pairing?.url, "", "Discard capability after sending the key")
    }

    @MainActor func testRejectedCodeRemainsRetryableWithoutLeakingDiagnostics() async {
        let (model, client, _) = fixture()
        client.confirmBody = { throw NSError(domain: "secret-key-https://private.example/#capability", code: 1) }
        model.consent = true
        await model.begin()?.value
        await model.refresh()
        model.code = "12345678"
        await model.confirm()?.value
        XCTAssertEqual(model.phase, .confirm)
        XCTAssertTrue(model.canConfirm)
        XCTAssertFalse(model.error?.contains("secret-key") ?? true)
        XCTAssertFalse(model.error?.contains("private.example") ?? true)
    }

    @MainActor func testLateConfirmationCannotRegressDeliveredState() async throws {
        let (model, client, _) = fixture()
        model.consent = true
        await model.begin()?.value
        await model.refresh()
        var statusContinuation: CheckedContinuation<PhonePairingStatus, Error>?
        var confirmationContinuation: CheckedContinuation<Void, Error>?
        client.statusBody = { try await withCheckedThrowingContinuation { statusContinuation = $0 } }
        client.confirmBody = { try await withCheckedThrowingContinuation { confirmationContinuation = $0 } }
        let refresh = Task { await model.refresh() }
        try await waitUntil { statusContinuation != nil }
        model.code = "12345678"
        let confirmation = model.confirm()
        try await waitUntil { confirmationContinuation != nil }
        statusContinuation?.resume(returning: PhonePairingStatus(state: "consumed", ipns: model.site.ipns, expiresAt: client.pair.expiresAt))
        await refresh.value
        XCTAssertEqual(model.phase, .delivered)
        confirmationContinuation?.resume(returning: ())
        await confirmation?.value
        XCTAssertEqual(model.phase, .delivered)
        XCTAssertNil(model.pairing)
    }

    @MainActor func testCloseInvalidatesLatePrepareAndClearsPrivateClipboard() async throws {
        let (model, client, _) = fixture()
        var continuation: CheckedContinuation<PhonePreparation, Error>?
        client.prepareBody = { _, _ in try await withCheckedThrowingContinuation { continuation = $0 } }
        model.consent = true
        let begin = model.begin()
        try await waitUntil { continuation != nil }
        let id = client.prepared[0].1
        let closed = await model.close()
        XCTAssertTrue(closed)
        XCTAssertEqual(model.phase, .closed)
        continuation?.resume(returning: client.result(siteID: model.site.id, id: id))
        await begin?.value
        XCTAssertEqual(model.phase, .closed)
        XCTAssertNil(model.pairing)
        XCTAssertTrue(client.cancelled.allSatisfy { $0.0 == model.site.id && $0.1 == id })
    }

    @MainActor func testExpiryClearsCodeQRAndOwnedClipboardButNotSomeoneElsesCopy() async {
        var date = Date()
        let client = PhoneClientFixture(date: date)
        let pasteboard = NSPasteboard(name: .init("CroptopPhoneTests." + UUID().uuidString))
        let model = PhoneConnectionModel(site: PhoneClientFixture.site, client: client,
            now: { date }, clipboard: PhoneConnectionClipboard(pasteboard: pasteboard))
        model.consent = true
        await model.begin()?.value
        model.copyLink()
        XCTAssertEqual(pasteboard.string(forType: .string), client.pair.url)
        XCTAssertNotNil(pasteboard.data(forType: .init("org.nspasteboard.ConcealedType")))
        XCTAssertNotNil(pasteboard.data(forType: .init("org.nspasteboard.TransientType")))
        model.code = "12345678"
        date = date.addingTimeInterval(601)
        await model.refresh()
        XCTAssertEqual(model.phase, .expired)
        XCTAssertNil(model.pairing)
        XCTAssertEqual(model.code, "")
        XCTAssertNil(pasteboard.string(forType: .string))
        let clipboard = PhoneConnectionClipboard(pasteboard: pasteboard)
        XCTAssertTrue(clipboard.copy(client.pair.url))
        pasteboard.clearContents()
        pasteboard.setString("User's next copy", forType: .string)
        clipboard.clearIfOwned()
        XCTAssertEqual(pasteboard.string(forType: .string), "User's next copy")
        pasteboard.clearContents()
    }

    @MainActor func testExpiredOrUnsafePairingURLsAreRejected() {
        let client = PhoneClientFixture(date: Date())
        let pair = client.pair
        XCTAssertTrue(PhoneConnectionModel.valid(pair, now: client.date))
        for url in [pair.url.replacingOccurrences(of: "https:", with: "http:"),
                    pair.url.replacingOccurrences(of: PhoneClientFixture.phoneHost, with: "user:password@" + PhoneClientFixture.phoneHost),
                    pair.url.replacingOccurrences(of: "/#", with: "/?tracking=yes#"),
                    pair.url.replacingOccurrences(of: "/#", with: "/wrong#"),
                    pair.url + ".extra"] {
            let invalid = PhonePairing(id: pair.id, url: url, expiresAt: pair.expiresAt, state: pair.state, ipns: pair.ipns, name: pair.name)
            XCTAssertFalse(PhoneConnectionModel.valid(invalid, now: client.date), url)
        }
        XCTAssertFalse(PhoneConnectionModel.valid(pair, now: client.date.addingTimeInterval(601)))
    }

    @MainActor func testCloseWaitsForActualCancellationAndBlocksUpdater() async throws {
        let (model, client, _) = fixture()
        model.consent = true
        await model.begin()?.value
        var continuation: CheckedContinuation<PhonePreparation, Error>?
        client.preparationBody = { _, _ in try await withCheckedThrowingContinuation { continuation = $0 } }
        let closing = Task { await model.close() }
        try await waitUntil { continuation != nil }
        XCTAssertEqual(model.phase, .stopping)
        XCTAssertTrue(AppUpdater.blocksRelaunch(screen: .settings(model.site.id), sheet: nil, publishing: [], phonePreparationCleanups: 1))
        continuation?.resume(returning: client.result(siteID: model.site.id, id: client.prepared[0].1, state: "cancelled"))
        let closed = await closing.value
        XCTAssertTrue(closed)
        XCTAssertEqual(model.phase, .closed)
    }

    @MainActor func testClosingDuringRetryStillDrainsPreviousPreparation() async throws {
        let (model, client, _) = fixture()
        client.prepareBody = { _, _ in throw PhoneConnectionError(status: 0, code: "transport") }
        model.consent = true
        await model.begin()?.value
        XCTAssertEqual(model.phase, .failed)
        let originalID = client.prepared[0].1
        var pending: [CheckedContinuation<PhonePreparation, Error>] = []
        client.preparationBody = { _, _ in try await withCheckedThrowingContinuation { pending.append($0) } }
        let retry = model.begin()
        try await waitUntil { !pending.isEmpty }
        let closing = Task { await model.close() }
        try await waitUntil { pending.count >= 2 }
        XCTAssertEqual(model.phase, .stopping)
        XCTAssertEqual(client.prepared.count, 1, "Do not start the second preparation before the first stops")
        XCTAssertEqual(client.cancelled.count, 2)
        XCTAssertTrue(client.cancelled.allSatisfy { $0.1 == originalID }, "Close must still target the in-flight old preparation")
        let stopped = client.result(siteID: model.site.id, id: originalID, state: "cancelled")
        pending[1].resume(returning: stopped)
        let closed = await closing.value
        XCTAssertTrue(closed)
        pending[0].resume(returning: stopped)
        await retry?.value
        XCTAssertEqual(model.phase, .closed)
        XCTAssertEqual(client.prepared.count, 1)
    }

    @MainActor func testBackgroundPhoneCleanupContinuesToBlockUpdaterWithoutSheet() {
        XCTAssertTrue(AppUpdater.blocksRelaunch(screen: .site("site"), sheet: nil, publishing: [], phonePreparationCleanups: 1))
        XCTAssertFalse(AppUpdater.blocksRelaunch(screen: .site("site"), sheet: nil, publishing: [], phonePreparationCleanups: 0))
    }

    @MainActor func testStatusDoesNotDecodeServerConfirmationCode() throws {
        let decoded = try JSONDecoder().decode(PhonePairingStatus.self,
            from: Data(#"{"state":"claimed","ipns":"k51fixture","expiresAt":1234567890,"code":"12345678"}"#.utf8))
        XCTAssertEqual(decoded.state, "claimed")
        XCTAssertFalse(Mirror(reflecting: decoded).children.contains { $0.label == "code" })
    }

    @MainActor func testFailureCopyDoesNotInventAnUploadForCompatibilityOrServiceErrors() async {
        for code in ["site_not_ready", "service_timeout"] {
            let (model, client, _) = fixture()
            client.prepareBody = { siteID, id in
                var value = client.result(siteID: siteID, id: id, state: "failed")
                value.code = code
                return value
            }
            model.consent = true
            await model.begin()?.value
            XCTAssertEqual(model.phase, .failed)
            XCTAssertFalse(model.error?.contains("upload finishes") ?? true)
            if code == "site_not_ready" { XCTAssertTrue(model.error?.contains("template") ?? false) }
            else { XCTAssertTrue(model.error?.contains("respond") ?? false) }
        }
    }

    @MainActor func testGeneratedNativeQRCanBeDecodedWithoutNetwork() throws {
        let client = PhoneClientFixture(date: Date())
        let image = try XCTUnwrap(PhoneConnectionQR.image(for: client.pair.url))
        let cgImage = try XCTUnwrap(image.cgImage(forProposedRect: nil, context: nil, hints: nil))
        let request = VNDetectBarcodesRequest()
        request.symbologies = [.qr]
        try VNImageRequestHandler(cgImage: cgImage).perform([request])
        XCTAssertEqual(request.results?.first?.payloadStringValue, client.pair.url)
    }

    @MainActor func testQRInRenderedNativeSheetCanBeDecodedAtActualDisplaySize() async throws {
        let (model, client, _) = fixture()
        model.consent = true
        await model.begin()?.value
        await model.refresh()
        let bitmap = try render(model)
        let request = VNDetectBarcodesRequest()
        request.symbologies = [.qr]
        try VNImageRequestHandler(cgImage: XCTUnwrap(bitmap.cgImage)).perform([request])
        XCTAssertEqual(request.results?.first?.payloadStringValue, client.pair.url)
        _ = await model.close()
    }

    @MainActor func testRenderedPairingShowsOnlyQRAndExpiryWithoutLosingConfirmationOrCancellation() async throws {
        for claimed in [false, true] {
            let (model, client, _) = fixture(storage: SiteStorage.hosted.rawValue)
            await model.begin()?.value
            if claimed { await model.refresh() }
            XCTAssertEqual(model.phase, claimed ? .confirm : .scan)

            let bitmap = try render(model)
            let textRequest = VNRecognizeTextRequest()
            textRequest.recognitionLevel = .accurate
            textRequest.recognitionLanguages = ["en-US"]
            let qrRequest = VNDetectBarcodesRequest()
            qrRequest.symbologies = [.qr]
            try VNImageRequestHandler(cgImage: XCTUnwrap(bitmap.cgImage)).perform([textRequest, qrRequest])
            let observations = textRequest.results ?? []
            let lines = observations.compactMap { $0.topCandidates(1).first?.string }
            let text = lines.joined(separator: " ")
            XCTAssertTrue(lines.contains(claimed ? "Confirm your phone" : "Scan with your phone"), text)
            for removed in ["camera", "Open the connection", "private link expires", "Copy private link",
                            "Copied privately", "Share only", "Waiting for your phone", "Keep this Mac",
                            "full publishing access", "does not revoke"] {
                XCTAssertFalse(text.contains(removed), "Removed copy '\(removed)' is visible: \(text)")
            }

            let qr = try XCTUnwrap(qrRequest.results?.first)
            XCTAssertEqual(qrRequest.results?.count, 1)
            XCTAssertEqual(qr.payloadStringValue, client.pair.url, "Decode synthetic pairing data only")
            let expiryLines = observations.filter { $0.topCandidates(1).first?.string.hasPrefix("Expires at ") == true }
            XCTAssertEqual(expiryLines.count, 1, text)
            let expiry = try XCTUnwrap(expiryLines.first)
            // Vision coordinates have their origin at the bottom left. Expiry
            // must sit below the QR, not return to a separate right-hand column.
            XCTAssertLessThan(expiry.boundingBox.maxY, qr.boundingBox.minY)
            XCTAssertGreaterThanOrEqual(expiry.boundingBox.minX, qr.boundingBox.minX - 0.03)
            XCTAssertLessThanOrEqual(expiry.boundingBox.maxX, qr.boundingBox.maxX + 0.03)

            if claimed {
                XCTAssertTrue(text.contains("Eight-digit code shown on your phone"), text)
                XCTAssertTrue(text.contains("Give this phone publishing access"), text)
                XCTAssertFalse(model.canConfirm, "The code is still required before granting access")
                model.code = "12345678"
                XCTAssertTrue(model.canConfirm)
            } else {
                XCTAssertFalse(text.contains("Eight-digit code"), text)
                XCTAssertFalse(model.canConfirm)
            }
            XCTAssertTrue(client.confirmed.isEmpty, "Rendering the pairing must not grant access")
            let closed = await model.close()
            XCTAssertTrue(closed)
            XCTAssertEqual(model.phase, .closed)
            XCTAssertNil(model.pairing)
            XCTAssertTrue(client.cancelled.contains { $0.0 == model.site.id && $0.1 == client.prepared.first?.1 })
        }
    }

    @MainActor func testMobileEmbedsConnectionWithoutSheetChrome() async throws {
        XCTAssertEqual(SiteSettingsView.sections.filter { $0 == "Mobile" }.count, 1)
        for phase in ["hosted", "scan", "confirm"] {
            let (model, client, _) = fixture(storage: SiteStorage.hosted.rawValue)
            if phase != "hosted" { await model.begin()?.value }
            if phase == "confirm" { await model.refresh() }
            let bitmap = try render(model, embedded: true)
            let textRequest = VNRecognizeTextRequest()
            textRequest.recognitionLevel = .accurate
            textRequest.recognitionLanguages = ["en-US"]
            let qrRequest = VNDetectBarcodesRequest()
            qrRequest.symbologies = [.qr]
            try VNImageRequestHandler(cgImage: XCTUnwrap(bitmap.cgImage)).perform([textRequest, qrRequest])
            let observations = textRequest.results ?? []
            let text = observations.compactMap { $0.topCandidates(1).first?.string }.joined(separator: " ")
            XCTAssertFalse(text.contains("Connect phone"), text)
            XCTAssertFalse(text.contains(model.site.name), "Settings already names the site; do not repeat modal chrome")
            XCTAssertFalse(text.contains("Waiting for your phone"), text)
            if phase == "hosted" {
                XCTAssertTrue(text.contains("Post from your phone"), text)
                XCTAssertFalse(text.contains("Allow crop.top"), "Saved hosting permission is still reused")
                XCTAssertTrue(model.canStart)
            } else {
                XCTAssertTrue(text.contains(phase == "scan" ? "Scan with your phone" : "Confirm your phone"), text)
                let qr = try XCTUnwrap(qrRequest.results?.first)
                XCTAssertEqual(qr.payloadStringValue, client.pair.url)
                let expiry = try XCTUnwrap(observations.first { $0.topCandidates(1).first?.string.hasPrefix("Expires at ") == true })
                XCTAssertLessThan(expiry.boundingBox.maxY, qr.boundingBox.minY)
                if phase == "confirm" {
                    XCTAssertTrue(text.contains("Eight-digit code shown on your phone"), text)
                    XCTAssertTrue(text.contains("Give this phone publishing access"), text)
                    XCTAssertFalse(model.canConfirm)
                }
            }
            XCTAssertTrue(client.confirmed.isEmpty)
            _ = await model.close()
        }
    }

    @MainActor func testLeavingMobileClearsSecretsAndHoldsGateUntilCancellationFinishes() async throws {
        _ = NSApplication.shared
        let (connection, client, _) = fixture(storage: SiteStorage.hosted.rawValue)
        await connection.begin()?.value
        await connection.refresh()
        connection.code = "12345678"
        let app = AppModel()
        let visibility = PhonePaneVisibility()
        var cleaned = 0
        var acknowledgement: CheckedContinuation<PhonePreparation, Error>?
        client.preparationBody = { _, _ in
            try await withCheckedThrowingContinuation { acknowledgement = $0 }
        }
        let host = NSHostingView(rootView: PhonePaneHarness(visibility: visibility, connection: connection) {
            cleaned += 1
        }.environmentObject(app))
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 720, height: 640),
                              styleMask: [.borderless], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.contentView = host
        host.layoutSubtreeIfNeeded()
        defer { acknowledgement?.resume(throwing: CancellationError()); window.contentView = nil; window.close() }
        try await waitUntil { app.phonePreparationCleanups == 1 }
        visibility.visible = false
        host.layoutSubtreeIfNeeded()
        try await waitUntil { acknowledgement != nil }
        XCTAssertEqual(connection.phase, .stopping)
        XCTAssertNil(connection.pairing)
        XCTAssertEqual(connection.code, "")
        XCTAssertEqual(app.phonePreparationCleanups, 1)
        XCTAssertTrue(AppUpdater.blocksRelaunch(screen: .site(connection.site.id), sheet: nil, publishing: [],
                                              phonePreparationCleanups: app.phonePreparationCleanups))
        XCTAssertEqual(cleaned, 0)
        let preparationID = try XCTUnwrap(client.prepared.first?.1)
        acknowledgement?.resume(returning: client.result(siteID: connection.site.id, id: preparationID, state: "cancelled"))
        acknowledgement = nil
        try await waitUntil { app.phonePreparationCleanups == 0 }
        XCTAssertEqual(connection.phase, .closed)
        XCTAssertEqual(cleaned, 1)
        XCTAssertEqual(client.cancelled.count, 1)
        XCTAssertEqual(client.cancelled.first?.1, preparationID)
    }

    @MainActor func testNativeSheetSnapshotsWhenRequested() async throws {
        guard let directory = ProcessInfo.processInfo.environment["CROPTOP_PHONE_SNAPSHOT_DIR"] else { return }
        _ = NSApplication.shared
        try FileManager.default.createDirectory(atPath: directory, withIntermediateDirectories: true)
        for phase in ["hosted", "unhosted", "publication", "preparing", "scan", "confirm"] {
            let (model, client, _) = fixture(storage: phase == "unhosted" ? SiteStorage.p2p.rawValue : SiteStorage.hosted.rawValue)
            if phase == "preparing" {
                client.prepareBody = { siteID, id in client.result(siteID: siteID, id: id, state: "preparing") }
            }
            if phase == "publication" {
                client.prepareBody = { siteID, id in
                    var value = client.result(siteID: siteID, id: id, state: "failed")
                    value.code = "publication_required"
                    return value
                }
            }
            if !["hosted", "unhosted"].contains(phase) {
                await model.begin()?.value
            }
            if phase == "confirm" { await model.refresh() }
            let bitmap = try render(model)
            let data = try XCTUnwrap(bitmap.representation(using: .png, properties: [:]))
            try data.write(to: URL(fileURLWithPath: directory).appendingPathComponent(phase + ".png"))
            let embedded = try render(model, embedded: true)
            let embeddedData = try XCTUnwrap(embedded.representation(using: .png, properties: [:]))
            try embeddedData.write(to: URL(fileURLWithPath: directory).appendingPathComponent("mobile-" + phase + ".png"))
            _ = await model.close()
        }
    }
}

@MainActor private final class PhonePaneVisibility: ObservableObject {
    @Published var visible = true
}

private struct PhonePaneHarness: View {
    @ObservedObject var visibility: PhonePaneVisibility
    let connection: PhoneConnectionModel
    let onCleanup: () async -> Void

    var body: some View {
        if visibility.visible {
            PhoneConnectionView(model: connection, presentation: .settings, onClose: {}, onCleanup: onCleanup)
        }
    }
}

private final class PhoneTestURLProtocol: URLProtocol {
    static var handler: ((URLRequest) throws -> (Int, Data))?
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        do {
            let (status, data) = try XCTUnwrap(Self.handler)(request)
            let response = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": "application/json"])!
            client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: data)
            client?.urlProtocolDidFinishLoading(self)
        } catch { client?.urlProtocol(self, didFailWithError: error) }
    }
    override func stopLoading() {}

    static func body(of request: URLRequest) -> Data {
        if let body = request.httpBody { return body }
        guard let stream = request.httpBodyStream else { return Data() }
        stream.open()
        defer { stream.close() }
        var result = Data()
        var buffer = [UInt8](repeating: 0, count: 1024)
        while stream.hasBytesAvailable {
            let count = stream.read(&buffer, maxLength: buffer.count)
            if count <= 0 { break }
            result.append(buffer, count: count)
        }
        return result
    }
}

final class PhoneConnectionTransportTests: XCTestCase {
    @MainActor private func client(base: String = "http://127.0.0.1:8086") -> LocalPhoneConnectionClient {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [PhoneTestURLProtocol.self]
        return LocalPhoneConnectionClient(base: URL(string: base)!, configuration: configuration)
    }

    @MainActor func testPreparationSendsExplicitPermissionsAndNativeSecurityHeader() async throws {
        let siteID = PhoneClientFixture.site.id
        defer { PhoneTestURLProtocol.handler = nil }
        for (enableHosting, allowPublish) in [(false, false), (true, false), (false, true), (true, true)] {
            let id = UUID().uuidString
            PhoneTestURLProtocol.handler = { request in
                XCTAssertEqual(request.httpMethod, "POST")
                XCTAssertEqual(request.url?.path, "/v0/croptop/sites/\(siteID)/phone/preparations")
                XCTAssertEqual(request.value(forHTTPHeaderField: "X-Croptop-Phone"), "1")
                XCTAssertEqual(request.value(forHTTPHeaderField: "Cache-Control"), "no-store")
                XCTAssertEqual(request.value(forHTTPHeaderField: "Content-Type"), "application/json")
                let body = try XCTUnwrap(JSONSerialization.jsonObject(with: PhoneTestURLProtocol.body(of: request)) as? [String: Any])
                XCTAssertEqual(body["id"] as? String, id)
                XCTAssertEqual(body["enableHosting"] as? Bool, enableHosting)
                XCTAssertEqual(body["allowPublish"] as? Bool, allowPublish, "Hosting and publication are independent permissions")
                let response: [String: Any] = ["id": id, "siteID": siteID, "state": "preparing", "stage": "waiting", "startedAt": 10, "deadline": 310]
                return (202, try JSONSerialization.data(withJSONObject: response))
            }
            let result = try await client().prepare(siteID: siteID, id: id, enableHosting: enableHosting, allowPublish: allowPublish)
            XCTAssertEqual(result.id, id)
        }
    }

    @MainActor func testRemotePlaintextOriginAndPathInjectionNeverSendRequests() async {
        var requests = 0
        PhoneTestURLProtocol.handler = { _ in requests += 1; return (200, Data()) }
        defer { PhoneTestURLProtocol.handler = nil }
        do {
            try await client(base: "http://phone.example").confirm(siteID: PhoneClientFixture.site.id, pairingID: "safe", code: "12345678")
            XCTFail("A confirmation code must not cross plaintext remote transport")
        } catch {}
        do {
            try await client().confirm(siteID: "../other", pairingID: "safe", code: "12345678")
            XCTFail("Reject non-UUID site paths")
        } catch {}
        do {
            try await client().confirm(siteID: PhoneClientFixture.site.id, pairingID: "../other?x=1", code: "12345678")
            XCTFail("Reject injectable pairing paths")
        } catch {}
        XCTAssertEqual(requests, 0)
    }

    @MainActor func testTransportDiscardsRawErrorBodiesAndOversizedSuccess() async {
        let secret = "https://private.example/#pair=SECRET"
        PhoneTestURLProtocol.handler = { _ in
            (502, Data("{\"error\":\"\(secret)\",\"code\":\"upstream\"}".utf8))
        }
        defer { PhoneTestURLProtocol.handler = nil }
        do {
            _ = try await client().preparation(siteID: PhoneClientFixture.site.id, id: UUID().uuidString)
            XCTFail("Expected sanitized error")
        } catch {
            XCTAssertEqual((error as? PhoneConnectionError)?.status, 502)
            XCTAssertEqual((error as? PhoneConnectionError)?.code, "upstream")
            XCTAssertFalse(String(reflecting: error).contains(secret))
        }
        PhoneTestURLProtocol.handler = { _ in (200, Data(repeating: 32, count: 65_537)) }
        do {
            _ = try await client().preparation(siteID: PhoneClientFixture.site.id, id: UUID().uuidString)
            XCTFail("Expected oversized response rejection")
        } catch { XCTAssertEqual((error as? PhoneConnectionError)?.code, "invalid_response") }
    }
}
