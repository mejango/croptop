import AppKit
import Foundation
import XCTest
@testable import Croptop

@MainActor private final class MobileSettingsPhoneClient: PhoneConnectionClient {
    private func unexpectedRequest() -> PhoneConnectionError {
        XCTFail("Loading Mobile settings must not start a phone connection")
        return PhoneConnectionError(status: 0, code: "fixture")
    }

    func prepare(siteID: String, id: String, enableHosting: Bool, allowPublish: Bool) async throws -> PhonePreparation {
        throw unexpectedRequest()
    }
    func preparation(siteID: String, id: String) async throws -> PhonePreparation { throw unexpectedRequest() }
    func cancel(siteID: String, id: String) async throws { throw unexpectedRequest() }
    func status(siteID: String, pairingID: String) async throws -> PhonePairingStatus { throw unexpectedRequest() }
    func confirm(siteID: String, pairingID: String, code: String) async throws { throw unexpectedRequest() }
}

@MainActor final class MobileSettingsTests: XCTestCase {
    private enum FixtureError: Error { case callbackTimedOut }

    func testMobileSelectionBlocksSettingsMutationsBeforeItsConnectionGateAppears() {
        let ordinary = SiteSettingsOperationState(busy: false, publishing: false, phonePreparationCleanups: 0, mobileSelected: false)
        XCTAssertFalse(ordinary.blocksMutation)
        XCTAssertTrue(ordinary.canEnterMobile)
        let loadingMobile = SiteSettingsOperationState(busy: false, publishing: false, phonePreparationCleanups: 0, mobileSelected: true)
        XCTAssertTrue(loadingMobile.blocksMutation, "Save/delete/claim must not race the initial saved-site read")
        XCTAssertTrue(loadingMobile.canEnterMobile, "The selected Mobile tab must not disable its own Connect action")
    }

    func testSavingDeletingAndPublishingBlockMobileEntryWhileCleanupCanBeWaitedOut() {
        for (busy, publishing) in [(true, false), (false, true), (true, true)] {
            let operation = SiteSettingsOperationState(busy: busy, publishing: publishing, phonePreparationCleanups: 0, mobileSelected: false)
            XCTAssertTrue(operation.blocksMutation)
            XCTAssertFalse(operation.canEnterMobile, "Do not start a Mobile load during another settings operation")
        }
        let draining = SiteSettingsOperationState(busy: false, publishing: false, phonePreparationCleanups: 1, mobileSelected: false)
        XCTAssertTrue(draining.blocksMutation)
        XCTAssertTrue(draining.canEnterMobile, "Mobile may open its waiting state while the loader waits for cleanup")
    }

    private func site(id: String = "11111111-2222-3333-4444-555555555555",
                      storage: String? = "p2p", gateway: String? = "sucks") -> Site {
        Site(id: id, name: "Mobile settings fixture", ipns: "k51fixture",
             croptopGateway: gateway, croptopHost: SiteStorage.publishingHost, croptopStorage: storage)
    }

    private func connection(for saved: Site) -> PhoneConnectionModel {
        PhoneConnectionModel(site: saved, client: MobileSettingsPhoneClient(),
            clipboard: PhoneConnectionClipboard(pasteboard: NSPasteboard(name: .init("MobileSettingsTests." + UUID().uuidString))))
    }

    private func waitUntil(_ predicate: () -> Bool, file: StaticString = #filePath, line: UInt = #line) async throws {
        let deadline = Date().addingTimeInterval(3)
        while !predicate(), Date() < deadline { try await Task.sleep(nanoseconds: 1_000_000) }
        guard predicate() else {
            XCTFail("Expected fixture callback within three seconds", file: file, line: line)
            throw FixtureError.callbackTimedOut
        }
    }

    func testLoaderWaitsForEveryPreviousCleanupBeforeFetchingSavedSite() async throws {
        let app = AppModel()
        app.phonePreparationCleanups = 2
        let saved = site(storage: "hosted", gateway: "crop.top")
        var requested: [String] = []
        var constructed: [Site] = []
        var adopted: [Site] = []
        let mobile = MobileSettingsModel(loadSite: { id in
            requested.append(id)
            return saved
        }, makeConnection: { value in
            constructed.append(value)
            return self.connection(for: value)
        })
        let loading = Task { await mobile.load(siteID: saved.id, app: app) { adopted.append($0) } }
        defer { loading.cancel() }
        try await waitUntil { mobile.waitingForCleanup }
        XCTAssertTrue(requested.isEmpty)
        XCTAssertNil(mobile.connection)
        app.phonePreparationCleanups = 1
        try await Task.sleep(nanoseconds: 120_000_000)
        XCTAssertTrue(requested.isEmpty, "One remaining cleanup must still block a new saved-site read")
        XCTAssertTrue(constructed.isEmpty)
        XCTAssertTrue(adopted.isEmpty)

        app.phonePreparationCleanups = 0
        await loading.value
        XCTAssertFalse(mobile.waitingForCleanup)
        XCTAssertEqual(requested, [saved.id])
        XCTAssertEqual(constructed, [saved])
        XCTAssertEqual(adopted, [saved])
        XCTAssertEqual(mobile.connection?.site, saved)
        XCTAssertNil(mobile.error)
    }

    func testLeavingMobileWhileWaitingDoesNotFetchOrConstructAfterCleanupFinishes() async throws {
        let app = AppModel()
        app.phonePreparationCleanups = 1
        let saved = site()
        var requests = 0
        var constructions = 0
        var adoptions = 0
        let mobile = MobileSettingsModel(loadSite: { _ in requests += 1; return saved }, makeConnection: { value in
            constructions += 1
            return self.connection(for: value)
        })
        let loading = Task { await mobile.load(siteID: saved.id, app: app) { _ in adoptions += 1 } }
        defer { loading.cancel() }
        try await waitUntil { mobile.waitingForCleanup }
        loading.cancel()
        await loading.value
        app.phonePreparationCleanups = 0
        await Task.yield()
        XCTAssertEqual(requests, 0)
        XCTAssertEqual(constructions, 0)
        XCTAssertEqual(adoptions, 0)
        XCTAssertNil(mobile.connection)
        XCTAssertNil(mobile.error, "Leaving the tab is not a user-visible load failure")
    }

    func testLateSavedSiteResponseAfterLeavingMobileCannotAdoptOrMountConnection() async throws {
        let app = AppModel()
        let saved = site(storage: "hosted")
        var response: CheckedContinuation<Site, Error>?
        var constructions = 0
        var adoptions = 0
        let mobile = MobileSettingsModel(loadSite: { _ in
            try await withCheckedThrowingContinuation { response = $0 }
        }, makeConnection: { value in
            constructions += 1
            return self.connection(for: value)
        })
        let loading = Task { await mobile.load(siteID: saved.id, app: app) { _ in adoptions += 1 } }
        defer { loading.cancel(); response?.resume(throwing: CancellationError()) }
        try await waitUntil { response != nil }
        loading.cancel()
        response?.resume(returning: saved)
        response = nil
        await loading.value
        XCTAssertEqual(constructions, 0)
        XCTAssertEqual(adoptions, 0)
        XCTAssertNil(mobile.connection)
        XCTAssertNil(mobile.error)
    }

    func testLateLoadErrorAfterLeavingMobileIsNotShown() async throws {
        let app = AppModel()
        let saved = site()
        var response: CheckedContinuation<Site, Error>?
        let mobile = MobileSettingsModel(loadSite: { _ in
            try await withCheckedThrowingContinuation { response = $0 }
        }, makeConnection: { value in
            XCTFail("A failed load cannot create a connection")
            return self.connection(for: value)
        })
        let loading = Task { await mobile.load(siteID: saved.id, app: app) { _ in XCTFail("A failed load cannot merge saved settings") } }
        defer { loading.cancel(); response?.resume(throwing: CancellationError()) }
        try await waitUntil { response != nil }
        loading.cancel()
        response?.resume(throwing: URLError(.notConnectedToInternet))
        response = nil
        await loading.value
        XCTAssertNil(mobile.connection)
        XCTAssertNil(mobile.error)
    }

    func testConnectionConsentComesFromFreshSavedSiteNotAnEarlierSettingsSnapshot() async throws {
        for savedStorage in [nil, "p2p", "hosted"] as [String?] {
            let stale = site(storage: savedStorage == "hosted" ? "p2p" : "hosted")
            var saved = site(storage: savedStorage)
            saved.name = "Fresh saved name"
            let mobile = MobileSettingsModel(loadSite: { id in
                XCTAssertEqual(id, stale.id)
                return saved
            }, makeConnection: { self.connection(for: $0) })
            var adopted: Site?
            await mobile.load(siteID: stale.id, app: AppModel()) { adopted = $0 }
            let model = try XCTUnwrap(mobile.connection)
            XCTAssertEqual(model.site, saved)
            XCTAssertEqual(adopted, saved)
            XCTAssertEqual(model.needsHostingConsent, savedStorage != "hosted")
            XCTAssertEqual(model.canStart, savedStorage == "hosted")
            XCTAssertFalse(model.consent, "Loading settings must not manufacture fresh hosting consent")
            XCTAssertEqual(model.phase, .consent)
            XCTAssertNil(mobile.error)
        }
    }

    func testWrongSiteIsRejectedBeforeSavedSettingsMergeOrConnectionFactory() async {
        let requested = site()
        let wrong = site(id: "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE", storage: "hosted", gateway: "crop.top")
        var didMerge = false
        var constructions = 0
        let mobile = MobileSettingsModel(loadSite: { _ in wrong }, makeConnection: { value in
            constructions += 1
            return self.connection(for: value)
        })
        await mobile.load(siteID: requested.id, app: AppModel()) { value in
            didMerge = true
            _ = MobileSettingsSavedSiteMerge(saved: value, previous: requested,
                storage: requested.storage, storageChoiceChanged: false, gateway: "sucks")
        }
        XCTAssertFalse(didMerge, "Identity must be checked before a saved-site callback can alter draft baselines")
        XCTAssertEqual(constructions, 0)
        XCTAssertNil(mobile.connection)
        XCTAssertEqual(mobile.error, "The saved site changed. Open Mobile again to retry.")
    }

    func testFailedReadCanRetryWithoutConstructingOrMergingOnFailure() async {
        let saved = site(storage: "hosted")
        var requests = 0
        var constructions = 0
        var adoptions = 0
        let mobile = MobileSettingsModel(loadSite: { _ in
            requests += 1
            if requests == 1 { throw APIError(message: "Fixture unavailable") }
            return saved
        }, makeConnection: { value in
            constructions += 1
            return self.connection(for: value)
        })
        let app = AppModel()
        await mobile.load(siteID: saved.id, app: app) { _ in adoptions += 1 }
        XCTAssertEqual(mobile.error, "Fixture unavailable")
        XCTAssertNil(mobile.connection)
        XCTAssertEqual(constructions, 0)
        XCTAssertEqual(adoptions, 0)
        await mobile.load(siteID: saved.id, app: app) { _ in adoptions += 1 }
        XCTAssertNil(mobile.error)
        XCTAssertEqual(mobile.connection?.site, saved)
        XCTAssertEqual(requests, 2)
        XCTAssertEqual(constructions, 1)
        XCTAssertEqual(adoptions, 1)
    }

    func testAlreadyLoadedConnectionIsNotReplacedByAnotherLoad() async throws {
        let saved = site(storage: "hosted")
        var requests = 0
        var adoptions = 0
        let mobile = MobileSettingsModel(loadSite: { _ in requests += 1; return saved },
                                         makeConnection: { self.connection(for: $0) })
        let app = AppModel()
        await mobile.load(siteID: saved.id, app: app) { _ in adoptions += 1 }
        let first = try XCTUnwrap(mobile.connection)
        await mobile.load(siteID: saved.id, app: app) { _ in adoptions += 1 }
        XCTAssertTrue(mobile.connection === first)
        XCTAssertEqual(requests, 1)
        XCTAssertEqual(adoptions, 1)
    }

    func testSavedHostingAndGatewayAreAdoptedWhenTheirDraftsAreUntouched() {
        let previous = site(storage: "p2p", gateway: "sucks")
        let saved = site(storage: "hosted", gateway: "crop.top")
        let merge = MobileSettingsSavedSiteMerge(saved: saved, previous: previous,
            storage: .p2p, storageChoiceChanged: false, gateway: "sucks")
        XCTAssertEqual(merge.storage, .hosted)
        XCTAssertEqual(merge.gateway, "crop.top")
        XCTAssertFalse(merge.storageChoiceChanged)
    }

    func testExplicitPendingHostingToggleIsPreservedEvenIfItsValueMatchesOldBaseline() {
        let previous = site(storage: "p2p", gateway: "sucks")
        let saved = site(storage: "hosted", gateway: "crop.top")
        let merge = MobileSettingsSavedSiteMerge(saved: saved, previous: previous,
            storage: .p2p, storageChoiceChanged: true, gateway: "sucks")
        XCTAssertEqual(merge.storage, .p2p, "An explicit pending choice must not be replaced by phone setup")
        XCTAssertEqual(merge.gateway, "crop.top", "An untouched gateway can still adopt its fresh saved value")
        XCTAssertTrue(merge.storageChoiceChanged, "The saved hosting value still differs from the pending choice")
    }

    func testChangedHostingDraftAndGatewayRemainPendingWhenSavedValuesStillDiffer() {
        let previous = site(storage: "hosted", gateway: "crop.top")
        let saved = site(storage: "hosted", gateway: "sucks")
        let merge = MobileSettingsSavedSiteMerge(saved: saved, previous: previous,
            storage: .p2p, storageChoiceChanged: true, gateway: "ipfs.io")
        XCTAssertEqual(merge.storage, .p2p)
        XCTAssertEqual(merge.gateway, "ipfs.io")
        XCTAssertTrue(merge.storageChoiceChanged)
    }

    func testPendingChoiceBecomesCleanWhenFreshSavedHostingAlreadyMatchesIntent() {
        for (oldStorage, newStorage, pending) in [("p2p", "hosted", SiteStorage.hosted), ("hosted", "p2p", SiteStorage.p2p)] {
            let previous = site(storage: oldStorage)
            let saved = site(storage: newStorage)
            let merge = MobileSettingsSavedSiteMerge(saved: saved, previous: previous,
                storage: pending, storageChoiceChanged: true, gateway: "sucks")
            XCTAssertEqual(merge.storage, pending)
            XCTAssertFalse(merge.storageChoiceChanged, "Do not keep a Save and publish action for an already-saved choice")
        }
    }

    func testPendingCropTopConsentStillDiffersFromFreshSavedCustomHosting() {
        let previous = site(storage: "p2p")
        var saved = site(storage: "hosted")
        saved.croptopHost = "https://publisher.example"
        let merge = MobileSettingsSavedSiteMerge(saved: saved, previous: previous,
            storage: .hosted, storageChoiceChanged: true, gateway: "sucks")
        XCTAssertEqual(merge.storage, .hosted)
        XCTAssertTrue(merge.storageChoiceChanged, "Matching raw hosted mode is not the same as consent to crop.top")
    }

    func testUntouchedStorageCanAdoptWhileChangedGatewayStaysPending() {
        let previous = site(storage: "p2p", gateway: "sucks")
        let saved = site(storage: "hosted", gateway: "crop.top")
        let merge = MobileSettingsSavedSiteMerge(saved: saved, previous: previous,
            storage: .p2p, storageChoiceChanged: false, gateway: "ipfs.io")
        XCTAssertEqual(merge.storage, .hosted)
        XCTAssertEqual(merge.gateway, "ipfs.io")
        XCTAssertFalse(merge.storageChoiceChanged)
    }

    func testMissingSavedGatewaysUseTheExistingCropTopDefault() {
        let previous = site(gateway: nil)
        let saved = site(storage: "hosted", gateway: nil)
        let merge = MobileSettingsSavedSiteMerge(saved: saved, previous: previous,
            storage: .p2p, storageChoiceChanged: false, gateway: "crop.top")
        XCTAssertEqual(merge.storage, .hosted)
        XCTAssertEqual(merge.gateway, "crop.top")

        let changed = MobileSettingsSavedSiteMerge(saved: saved, previous: previous,
            storage: .p2p, storageChoiceChanged: false, gateway: "ipfs.io")
        XCTAssertEqual(changed.gateway, "ipfs.io")
    }
}
