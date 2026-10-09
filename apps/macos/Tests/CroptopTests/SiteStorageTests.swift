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
}
