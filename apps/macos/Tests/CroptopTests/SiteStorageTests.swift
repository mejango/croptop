import XCTest
@testable import Croptop

final class SiteStorageTests: XCTestCase {
    private func site(storage: Any? = nil) throws -> Site {
        var json: [String: Any] = [
            "id": "site", "name": "My site", "ipns": "k51site",
            "croptopHost": "https://crop.top", "croptopGateway": "crop.top"
        ]
        if let storage { json["croptopStorage"] = storage }
        return try JSONDecoder().decode(Site.self, from: JSONSerialization.data(withJSONObject: json))
    }

    func testLegacyHostingSettingsDoNotOptIn() throws {
        XCTAssertEqual(try site().storage, .p2p)
        XCTAssertEqual(try site(storage: NSNull()).storage, .p2p)
        for value in ["p2p", "", "automatic", "HOSTED", " hosted "] {
            XCTAssertEqual(try site(storage: value).storage, .p2p, value)
        }
        XCTAssertEqual(try site(storage: "hosted").storage, .hosted)
    }

    func testStorageRoundTripKeepsExplicitChoice() throws {
        for mode in [SiteStorage.p2p, .hosted] {
            let saved = try site(storage: mode.rawValue)
            let restored = try JSONDecoder().decode(Site.self, from: JSONEncoder().encode(saved))
            XCTAssertEqual(restored.croptopStorage, mode.rawValue)
            XCTAssertEqual(restored.storage, mode)
        }
    }

    func testP2PGatewayFallsBackWithoutChangingConfiguredPreference() throws {
        let legacy = try site()
        XCTAssertEqual(legacy.storage.effectiveGateway(legacy.croptopGateway), "sucks")
        XCTAssertEqual(legacy.croptopGateway, "crop.top")
        XCTAssertEqual(SiteStorage.p2p.effectiveGateway(nil), "sucks")
        XCTAssertEqual(SiteStorage.p2p.effectiveGateway(""), "sucks")
        XCTAssertEqual(SiteStorage.p2p.effectiveGateway("ipfs.io"), "ipfs.io")
        XCTAssertEqual(SiteStorage.hosted.effectiveGateway(legacy.croptopGateway), "crop.top")
    }

    func testOnlyDefaultOrCanonicalCropTopHostsAreRecognized() {
        XCTAssertEqual(SiteStorage.publishingHost, "https://crop.top")
        for host in [nil, "", " \n", "https://crop.top", "https://crop.top/", " https://crop.top/\n"] as [String?] {
            XCTAssertTrue(SiteStorage.isCroptopHost(host), host ?? "default")
        }
        for host in ["https://other.example", "http://crop.top", "https://crop.top:443",
                     "https://crop.top/path", "https://crop.top?source=custom", "https://crop.top.evil.example",
                     "https://crop.top@other.example", "https://CROP.TOP", "crop.top"] {
            XCTAssertFalse(SiteStorage.isCroptopHost(host), host)
        }
    }

    func testChangingAndRevertingEitherSavedHostingChoice() throws {
        for mode in [SiteStorage.p2p, .hosted] {
            let original = try site(storage: mode.rawValue)
            let savedChoice = mode == .hosted
            let unchanged = SiteStorage.choosingCroptop(savedChoice, original: original)
            XCTAssertEqual(unchanged.storage, mode)
            XCTAssertFalse(unchanged.changed)
            XCTAssertEqual(mode.usesCroptop(original: original), savedChoice)

            let changed = SiteStorage.choosingCroptop(!savedChoice, original: original)
            XCTAssertEqual(changed.storage, savedChoice ? .p2p : .hosted)
            XCTAssertTrue(changed.changed)
            XCTAssertEqual(changed.storage.usesCroptop(original: original, explicitlyChanged: changed.changed), !savedChoice)

            let reverted = SiteStorage.choosingCroptop(savedChoice, original: original)
            XCTAssertEqual(reverted.storage, original.storage)
            XCTAssertFalse(reverted.changed, "Returning to the saved choice hides the inline action")
            XCTAssertEqual(reverted.storage.usesCroptop(original: original, explicitlyChanged: reverted.changed), savedChoice)
        }
    }

    func testDefaultHostDoesNotImplyHostingConsent() {
        for host in [nil, "", "https://crop.top", "https://crop.top/"] as [String?] {
            let original = Site(id: "site", name: "My site", ipns: "k51site", croptopHost: host)
            XCTAssertFalse(original.storage.usesCroptop(original: original))
            XCTAssertFalse(SiteStorage.p2p.usesCroptop(original: original, explicitlyChanged: true))
            XCTAssertEqual(SiteStorage.hostSettingChange(original: original, explicitlyChanged: false), "https://crop.top")
        }
    }

    func testCustomHostIsPreservedUntilExplicitCropTopChoiceAndAfterReverting() throws {
        for mode in [SiteStorage.p2p, .hosted] {
            var original = try site(storage: mode.rawValue)
            original.croptopHost = "https://custom.example/publisher"
            original.croptopName = "custom-name"
            XCTAssertFalse(original.storage.usesCroptop(original: original))
            XCTAssertNil(SiteStorage.hostSettingChange(original: original, explicitlyChanged: false))

            let enabled = SiteStorage.choosingCroptop(true, original: original)
            XCTAssertEqual(enabled.storage, .hosted)
            XCTAssertTrue(enabled.changed, "A custom hosted site has not opted into crop.top")
            XCTAssertTrue(enabled.storage.usesCroptop(original: original, explicitlyChanged: enabled.changed))
            XCTAssertEqual(SiteStorage.hostSettingChange(original: original, explicitlyChanged: enabled.changed), "https://crop.top")

            let reverted = SiteStorage.choosingCroptop(false, original: original)
            XCTAssertEqual(reverted.storage, mode, "Reverting must restore the custom host's original storage mode")
            XCTAssertFalse(reverted.changed)
            XCTAssertFalse(reverted.storage.usesCroptop(original: original, explicitlyChanged: reverted.changed))
            XCTAssertNil(SiteStorage.hostSettingChange(original: original, explicitlyChanged: reverted.changed))
            XCTAssertEqual(original.croptopHost, "https://custom.example/publisher")
            XCTAssertEqual(original.croptopName, "custom-name")
        }
    }

    func testRevertingCheckboxDoesNotAcknowledgeLegacyStorageReview() throws {
        var original = try site(storage: "p2p")
        original.croptopStorageNeedsReview = true
        let enabled = SiteStorage.choosingCroptop(true, original: original)
        XCTAssertEqual(enabled.storage.settingChange(original: original, explicitlyChanged: enabled.changed), "hosted")
        let reverted = SiteStorage.choosingCroptop(false, original: original)
        XCTAssertFalse(reverted.changed)
        XCTAssertNil(reverted.storage.settingChange(original: original, explicitlyChanged: reverted.changed))
        XCTAssertEqual(original.croptopStorageNeedsReview, true)
    }
}
