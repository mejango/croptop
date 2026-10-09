import XCTest
import AppKit
@testable import Croptop

final class StoragePublishReviewTests: XCTestCase {
    private func site(review: Bool? = true, storage: String = "p2p", id: String = "legacy") -> Site {
        Site(id: id, name: "Existing site", ipns: "k51site", croptopStorage: storage, croptopStorageNeedsReview: review)
    }

    @MainActor func testCancelAndSettingsDoNotSaveOrAuthorizePublication() async throws {
        for choice in [StorageReviewChoice.cancel, .settings] {
            var writes = 0
            let result = try await StoragePublishReview.prepare(siteID: "legacy",
                read: { id in XCTAssertEqual(id, "legacy"); return self.site() },
                saveP2P: { _ in writes += 1 }, choose: { _ in choice }, isCurrent: { true })
            XCTAssertEqual(result, choice == .cancel ? .cancelled : .settings)
            XCTAssertEqual(writes, 0)
        }
    }

    @MainActor func testExplicitP2PChoicePersistsBeforePublicationAndOnlyOnce() async throws {
        var saved = site()
        var writes = 0
        var prompts = 0
        for _ in 0..<2 {
            let result = try await StoragePublishReview.prepare(siteID: "legacy", read: { _ in saved },
                saveP2P: { id in
                    XCTAssertEqual(id, "legacy")
                    writes += 1
                    saved.croptopStorage = "p2p"
                    saved.croptopStorageNeedsReview = false
                }, choose: { _ in prompts += 1; return .publishP2P }, isCurrent: { true })
            XCTAssertEqual(result, .publish)
        }
        XCTAssertEqual(writes, 1)
        XCTAssertEqual(prompts, 1)
        XCTAssertEqual(saved.storage, .p2p)
    }

    @MainActor func testAlreadyChosenStorageNeverPromptsOrWrites() async throws {
        for storage in ["p2p", "hosted"] {
            let result = try await StoragePublishReview.prepare(siteID: "legacy",
                read: { _ in self.site(review: false, storage: storage) },
                saveP2P: { _ in XCTFail("must not change explicit storage") },
                choose: { _ in XCTFail("must not prompt after explicit choice"); return .cancel }, isCurrent: { true })
            XCTAssertEqual(result, .publish)
        }
    }

    @MainActor func testSwitchingSitesWhileReviewIsOpenCancelsWithoutWriting() async throws {
        var current = true
        let result = try await StoragePublishReview.prepare(siteID: "legacy", read: { _ in self.site() },
            saveP2P: { _ in XCTFail("must not save after switching sites") },
            choose: { _ in current = false; return .publishP2P }, isCurrent: { current })
        XCTAssertEqual(result, .cancelled)
    }

    @MainActor func testMismatchedSiteResponseCannotAuthorizeOrSave() async throws {
        var reads = 0
        let result = try await StoragePublishReview.prepare(siteID: "legacy",
            read: { _ in reads += 1; return self.site(id: reads == 1 ? "legacy" : "other") },
            saveP2P: { _ in XCTFail("must not save for mismatched identity") },
            choose: { _ in .publishP2P }, isCurrent: { true })
        XCTAssertEqual(result, .cancelled)
    }

    @MainActor func testConcurrentExplicitChoiceIsNeverOverwritten() async throws {
        var reads = 0
        let result = try await StoragePublishReview.prepare(siteID: "legacy",
            read: { _ in reads += 1; return reads == 1 ? self.site() : self.site(review: false, storage: "hosted") },
            saveP2P: { _ in XCTFail("must not overwrite a concurrent explicit choice") },
            choose: { _ in .publishP2P }, isCurrent: { true })
        XCTAssertEqual(result, .publish)
    }

    @MainActor func testUnsavedChoiceStopsPublication() async throws {
        do {
            _ = try await StoragePublishReview.prepare(siteID: "legacy", read: { _ in self.site() },
                saveP2P: { _ in }, choose: { _ in .publishP2P }, isCurrent: { true })
            XCTFail("an unconfirmed storage save must stop publication")
        } catch {
            XCTAssertTrue(error.localizedDescription.contains("not saved"))
        }
    }

    @MainActor func testStorageReviewScreenKeepsSiteIdentityAndBlocksUpdaterRelaunch() {
        let model = AppModel()
        model.sites = [site()]
        model.screen = .storageSettings("legacy")
        XCTAssertEqual(model.currentSiteID, "legacy")
        XCTAssertEqual(model.currentSite?.id, "legacy")
        XCTAssertTrue(AppUpdater.blocksRelaunch(screen: model.screen, sheet: nil, publishing: []))
    }

    @MainActor func testReviewDialogOffersExplicitP2POrExistingSettingsWithoutHostingConsent() throws {
        _ = NSApplication.shared
        let alert = StoragePublishReview.alert(for: site())
        XCTAssertTrue(alert.messageText.contains("Existing site"))
        XCTAssertTrue(alert.informativeText.contains("does not update the reliable host"))
        XCTAssertTrue(alert.informativeText.contains("Saved changes stay on this Mac"))
        XCTAssertEqual(alert.buttons.map(\.title), ["Open Storage Settings", "Publish with P2P", "Cancel"])
        XCTAssertEqual(alert.buttons[2].keyEquivalent, "\u{1b}")

        // Optional disposable native-window fixture for visual release review.
        // No production API, library, settings or actual application is opened.
        if let path = ProcessInfo.processInfo.environment["CROPTOP_STORAGE_REVIEW_SNAPSHOT"] {
            alert.layout()
            let view = try XCTUnwrap(alert.window.contentView)
            view.layoutSubtreeIfNeeded()
            let bitmap = try XCTUnwrap(view.bitmapImageRepForCachingDisplay(in: view.bounds))
            view.cacheDisplay(in: view.bounds, to: bitmap)
            let data = try XCTUnwrap(bitmap.representation(using: .png, properties: [:]))
            try data.write(to: URL(fileURLWithPath: path))
        }
    }

    func testUnrelatedSettingsDoNotImplicitlyAcknowledgeLegacyChoice() {
        let original = site()
        XCTAssertNil(SiteStorage.p2p.settingChange(original: original, explicitlyChanged: false))
        XCTAssertEqual(SiteStorage.p2p.settingChange(original: original, explicitlyChanged: true), "p2p")
        XCTAssertEqual(SiteStorage.hosted.settingChange(original: original, explicitlyChanged: false), "hosted")
        XCTAssertEqual(SiteStorage.p2p.settingChange(original: site(review: false), explicitlyChanged: false), "p2p")
    }
}
