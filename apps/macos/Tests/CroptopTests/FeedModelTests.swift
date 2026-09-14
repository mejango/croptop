import XCTest
@testable import Croptop

final class FeedModelTests: XCTestCase {
    private func item(siteID: String? = nil, ipns: String = "k51source", complete: Bool = true) throws -> FeedItem {
        var record: [String: Any] = ["ipns": ipns, "site": "A site", "id": "same-post", "title": "Post", "summary": "Short", "link": "/f/\(ipns)/same-post/", "url": "https://example.com/same-post/", "created": 1234, "preview": false, "pinned": false]
        if let siteID { record["siteID"] = siteID }
        if complete { record["content"] = "The complete post"; record["attachments"] = ["photo.png"] }
        return try JSONDecoder().decode(FeedItem.self, from: JSONSerialization.data(withJSONObject: record))
    }

    func testIdenticalPostIDsRemainDistinctAcrossSources() throws {
        let items = try [item(siteID: "a"), item(siteID: "b"), item(ipns: "a"), item(ipns: "b")]
        XCTAssertEqual(Set(items.map(\.id)).count, 4)
        XCTAssertTrue(items.allSatisfy { $0.postID == "same-post" })
    }

    func testCompleteReaderContentAndWireIdentityRoundTrip() throws {
        let post = try item(siteID: "owned-site")
        XCTAssertEqual(post.content, "The complete post")
        XCTAssertEqual(post.attachments, ["photo.png"])
        let encoded = try XCTUnwrap(JSONSerialization.jsonObject(with: JSONEncoder().encode(post)) as? [String: Any])
        XCTAssertEqual(encoded["id"] as? String, "same-post")
        XCTAssertNil(encoded["postID"])
    }

    func testLegacyFeedCanDecodeWithoutInventingFullContent() throws {
        let post = try item(complete: false)
        XCTAssertNil(post.content)
        XCTAssertNil(post.attachments)
        XCTAssertNil(post.siteID)
        XCTAssertEqual(post.summary, "Short")
    }

    @MainActor func testFollowedAndAggregateScreensDoNotSelectAnOwnedSite() {
        let suite = "FeedModelTests." + UUID().uuidString
        let preferences = UserDefaults(suiteName: suite)!
        defer { preferences.removePersistentDomain(forName: suite) }
        let model = AppModel(preferences: preferences)
        model.sites = [Site(id: "a", name: "Owned", ipns: "shared-ipns")]
        for screen in [Screen.feed, .ownedFeed, .followingSite("shared-ipns")] {
            model.screen = screen
            XCTAssertNil(model.currentSiteID)
            XCTAssertNil(model.currentSite)
        }
        model.screen = .site("a")
        XCTAssertEqual(model.currentSiteID, "a")
    }
}
