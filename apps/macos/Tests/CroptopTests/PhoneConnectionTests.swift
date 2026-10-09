import AppKit
import Foundation
import SwiftUI
import Vision
import XCTest
@testable import Croptop

@MainActor
private final class PhoneClientFixture: PhoneConnectionClient {
    let date: Date
    var prepared: [(String, String, Bool)] = []
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
    func prepare(siteID: String, id: String, enableHosting: Bool) async throws -> PhonePreparation {
        prepared.append((siteID, id, enableHosting))
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

    @MainActor private func fixture() -> (PhoneConnectionModel, PhoneClientFixture, NSPasteboard) {
        let date = Date()
        let client = PhoneClientFixture(date: date)
        let pasteboard = NSPasteboard(name: .init("CroptopPhoneTests." + UUID().uuidString))
        let model = PhoneConnectionModel(site: PhoneClientFixture.site, client: client,
            now: { date }, clipboard: PhoneConnectionClipboard(pasteboard: pasteboard))
        return (model, client, pasteboard)
    }

    @MainActor private func render(_ model: PhoneConnectionModel) throws -> NSBitmapImageRep {
        _ = NSApplication.shared
        let view = NSHostingView(rootView: PhoneConnectionSheet(model: model).environmentObject(AppModel()))
        view.frame = NSRect(x: 0, y: 0, width: 560, height: 660)
        view.layoutSubtreeIfNeeded()
        let bitmap = try XCTUnwrap(view.bitmapImageRepForCachingDisplay(in: view.bounds))
        view.cacheDisplay(in: view.bounds, to: bitmap)
        return bitmap
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
        XCTAssertNil(model.begin(), "A live pairing cannot start a duplicate preparation")
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
        XCTAssertTrue(AppUpdater.blocksRelaunch(screen: .site(model.site.id), sheet: .connectPhone(model.site), publishing: []))
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

    @MainActor func testNativeSheetSnapshotsWhenRequested() async throws {
        guard let directory = ProcessInfo.processInfo.environment["CROPTOP_PHONE_SNAPSHOT_DIR"] else { return }
        _ = NSApplication.shared
        try FileManager.default.createDirectory(atPath: directory, withIntermediateDirectories: true)
        for phase in ["consent", "preparing", "confirm"] {
            let (model, client, _) = fixture()
            if phase == "preparing" {
                client.prepareBody = { siteID, id in client.result(siteID: siteID, id: id, state: "preparing") }
            }
            if phase != "consent" {
                model.consent = true
                await model.begin()?.value
            }
            if phase == "confirm" { await model.refresh() }
            let bitmap = try render(model)
            let data = try XCTUnwrap(bitmap.representation(using: .png, properties: [:]))
            try data.write(to: URL(fileURLWithPath: directory).appendingPathComponent(phase + ".png"))
            _ = await model.close()
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
        let id = UUID().uuidString
        let siteID = PhoneClientFixture.site.id
        PhoneTestURLProtocol.handler = { request in
            XCTAssertEqual(request.httpMethod, "POST")
            XCTAssertEqual(request.url?.path, "/v0/croptop/sites/\(siteID)/phone/preparations")
            XCTAssertEqual(request.value(forHTTPHeaderField: "X-Croptop-Phone"), "1")
            XCTAssertEqual(request.value(forHTTPHeaderField: "Cache-Control"), "no-store")
            XCTAssertEqual(request.value(forHTTPHeaderField: "Content-Type"), "application/json")
            let body = try XCTUnwrap(JSONSerialization.jsonObject(with: PhoneTestURLProtocol.body(of: request)) as? [String: Any])
            XCTAssertEqual(body["id"] as? String, id)
            XCTAssertEqual(body["enableHosting"] as? Bool, true)
            XCTAssertEqual(body["allowPublish"] as? Bool, true)
            let response: [String: Any] = ["id": id, "siteID": siteID, "state": "preparing", "stage": "waiting", "startedAt": 10, "deadline": 310]
            return (202, try JSONSerialization.data(withJSONObject: response))
        }
        defer { PhoneTestURLProtocol.handler = nil }
        let result = try await client().prepare(siteID: siteID, id: id, enableHosting: true)
        XCTAssertEqual(result.id, id)
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
