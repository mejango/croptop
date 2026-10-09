import AppKit
import SwiftUI
import Vision
import XCTest
@testable import Croptop

@MainActor private final class PhoneStageFixture: PhoneConnectionClient {
    var stage: String
    let startedAt: Date
    var cancelled = false

    init(stage: String, startedAt: Date) {
        self.stage = stage
        self.startedAt = startedAt
    }

    private func result(siteID: String, id: String) -> PhonePreparation {
        PhonePreparation(id: id, siteID: siteID, state: cancelled ? "cancelled" : "preparing", stage: stage,
                         startedAt: startedAt.timeIntervalSince1970, deadline: startedAt.timeIntervalSince1970 + 300)
    }
    func prepare(siteID: String, id: String, enableHosting: Bool, allowPublish: Bool) async throws -> PhonePreparation {
        XCTAssertFalse(enableHosting, "The fixture is already hosted")
        XCTAssertFalse(allowPublish, "Observing a stage never grants publication permission")
        return result(siteID: siteID, id: id)
    }
    func preparation(siteID: String, id: String) async throws -> PhonePreparation { result(siteID: siteID, id: id) }
    func cancel(siteID: String, id: String) async throws { cancelled = true }
    func status(siteID: String, pairingID: String) async throws -> PhonePairingStatus {
        XCTFail("Preparation-stage tests must not request a pairing")
        throw PhoneConnectionError(status: 0, code: "fixture")
    }
    func confirm(siteID: String, pairingID: String, code: String) async throws {
        XCTFail("Preparation-stage tests must not confirm a pairing")
    }
}

final class PhoneConnectionStageTests: XCTestCase {
    @MainActor private func fixture(stage: String) async -> (PhoneConnectionModel, PhoneStageFixture) {
        let date = Date()
        let client = PhoneStageFixture(stage: stage, startedAt: date)
        let site = Site(id: "11111111-2222-3333-4444-555555555555", name: "Stage fixture", ipns: "k51fixture", croptopStorage: "hosted")
        let model = PhoneConnectionModel(site: site, client: client, now: { date })
        await model.begin()?.value
        return (model, client)
    }

    @MainActor func testUploadHostVerificationAndPhoneVerificationHaveDistinctTruthfulCopy() async {
        let cases = [
            ("uploading", "Sending site to hosting", "Preparing and sending site files, then waiting for the host to respond."),
            ("verifying_host", "Verifying hosted publication", "Checking which version the host accepted before continuing."),
            ("verifying_phone", "Checking phone compatibility", "Checking that the phone service can use the hosted version.")
        ]
        for (stage, title, detail) in cases {
            let (model, _) = await fixture(stage: stage)
            XCTAssertEqual(model.phase, .preparing)
            XCTAssertEqual(model.stageTitle, title)
            XCTAssertEqual(model.stageDetail, detail)
            XCTAssertFalse(model.stageDetail.lowercased().contains("upload complete"))
            _ = await model.close()
        }
    }

    @MainActor func testLegacyHostingAndActualWaitingRemainRecognized() async {
        let (model, client) = await fixture(stage: "hosting")
        XCTAssertEqual(model.stageTitle, "Uploading and checking your site")
        XCTAssertEqual(model.stageDetail, "Uploading and verifying your hosted site. Larger sites can take a few minutes.")
        client.stage = "waiting"
        await model.refresh()
        XCTAssertEqual(model.stageTitle, "Waiting for the publisher")
        _ = await model.close()
    }

    @MainActor func testUnknownStageDoesNotInventWaitingUploadingOrSuccess() async {
        let (model, _) = await fixture(stage: "future_stage")
        XCTAssertEqual(model.phase, .preparing)
        XCTAssertEqual(model.stageTitle, "Preparing phone connection")
        XCTAssertEqual(model.stageDetail, "Keep Croptop open and this Mac awake until the QR code appears.")
        _ = await model.close()
    }

    @MainActor func testElapsedTimeRemainsTotalAcrossStageChangesAndIsLabeledTotal() async throws {
        var date = Date()
        let client = PhoneStageFixture(stage: "uploading", startedAt: date)
        let site = Site(id: "11111111-2222-3333-4444-555555555555", name: "Stage fixture", ipns: "k51fixture", croptopStorage: "hosted")
        let model = PhoneConnectionModel(site: site, client: client, now: { date })
        await model.begin()?.value
        date = date.addingTimeInterval(200)
        await model.refresh()
        XCTAssertEqual(model.elapsed, 200)
        client.stage = "verifying_phone"
        date = date.addingTimeInterval(44)
        await model.refresh()
        XCTAssertEqual(model.elapsed, 244, "Changing stage must not reset the attempt's clock")
        XCTAssertEqual(model.stageTitle, "Checking phone compatibility")

        _ = NSApplication.shared
        let view = NSHostingView(rootView: PhoneConnectionSheet(model: model).environmentObject(AppModel()))
        view.frame = NSRect(x: 0, y: 0, width: 560, height: 660)
        view.layoutSubtreeIfNeeded()
        let bitmap = try XCTUnwrap(view.bitmapImageRepForCachingDisplay(in: view.bounds))
        view.cacheDisplay(in: view.bounds, to: bitmap)
        let request = VNRecognizeTextRequest()
        request.recognitionLevel = .accurate
        try VNImageRequestHandler(cgImage: XCTUnwrap(bitmap.cgImage), options: [:]).perform([request])
        let text = request.results?.compactMap { $0.topCandidates(1).first?.string }.joined(separator: " ") ?? ""
        XCTAssertTrue(text.contains("244s total"), text)
        XCTAssertFalse(text.contains("244s elapsed"), text)
        _ = await model.close()
    }
}
