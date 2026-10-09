import XCTest
@testable import Croptop

final class FeedPostResourcesTests: XCTestCase {
    private let base = URL(string: "http://127.0.0.1:8086")!
    private let ipns = "k51qzi5uqu5dhxiwvl4xx3sco13y50yoo28rbhe3sneirnj2qatinx75qpsqtb"
    private let siteID = "11111111-1111-1111-1111-111111111111"
    private let postID = "22222222-2222-2222-2222-222222222222"

    func testFollowedResourcesStayInPostTreeAndEncodeFilenames() throws {
        let names = ["cover +#&?.png", "café.png", "100% complete.png"]
        let item = try decode(["attachments": names + [names[0]]])
        let attachments = FeedPostResources.attachments(for: item, baseURL: base)
        XCTAssertEqual(attachments.map(\.name), names)
        XCTAssertEqual(attachments.first?.url.absoluteString,
                       "\(base)/f/\(ipns)/\(postID)/cover%20%2B%23%26%3F.png")
        for attachment in attachments {
            XCTAssertEqual(attachment.url.host, base.host)
            XCTAssertEqual(attachment.url.path, "/f/\(ipns)/\(postID)/\(attachment.name)")
            XCTAssertNil(attachment.url.query)
            XCTAssertNil(attachment.url.fragment)
        }
    }

    func testOwnedResourcesUseOwnedPublicRouteAndStripBaseQuery() throws {
        let item = try decode(["siteID": siteID, "link": "/\(siteID)/\(postID)/",
                               "attachments": ["photo.png"], "hero": "_videoThumbnail.png"])
        let configuredBase = URL(string: "http://127.0.0.1:8086/v0/status?secret=value#anchor")!
        XCTAssertEqual(FeedPostResources.attachments(for: item, baseURL: configuredBase).first?.url.absoluteString,
                       "\(base)/\(siteID)/\(postID)/photo.png")
        XCTAssertEqual(FeedPostResources.heroURL(for: item, baseURL: base)?.absoluteString,
                       "\(base)/\(siteID)/\(postID)/_videoThumbnail.png")
        XCTAssertEqual(FeedPostResources.avatarURL(for: item, baseURL: base)?.absoluteString,
                       "\(base)/\(siteID)/avatar.png")
    }

    func testRejectsRawAndRepeatedlyEscapedTraversal() throws {
        let filenames = ["", ".", "..", "../secret", "/v0/status", "\\secret", "a/b.png",
                         "a\\b.png", "a\u{0}.png", "a\n.png", "%2e%2e", "%2E%2E%2Fsecret",
                         "%252e%252e%252fsecret", "a%5Cb.png", "a%00.png", "https://evil.test/a.png"]
        for name in filenames {
            let item = try decode(["attachments": [name], "hero": name])
            XCTAssertTrue(FeedPostResources.attachments(for: item, baseURL: base).isEmpty, name)
            XCTAssertNil(FeedPostResources.heroURL(for: item, baseURL: base), name)
        }
    }

    func testRejectsSpoofedScopeAndConsoleAPILinks() throws {
        let invalid: [[String: Any]] = [
            ["link": "/v0/croptop/quit"], ["link": "https://evil.test/"],
            ["link": "//evil.test/"], ["link": "/f/\(ipns)/\(postID)/?x=1"],
            ["link": "/f/\(ipns)/\(postID)/#x"],
            ["ipns": "../v0", "link": "/f/../v0/\(postID)/"],
            ["ipns": ipns + "\n"], ["id": "../v0"], ["id": "%2e%2e"],
            ["id": "valid\n"], ["siteID": "v0"], ["siteID": siteID + "/.."],
            ["siteID": siteID, "link": "/f/\(ipns)/\(postID)/"]
        ]
        for fields in invalid {
            let item = try decode(fields.merging(["attachments": ["cover.png"], "hero": "cover.png"]) { _, new in new })
            XCTAssertTrue(FeedPostResources.attachments(for: item, baseURL: base).isEmpty, "\(fields)")
            XCTAssertNil(FeedPostResources.heroURL(for: item, baseURL: base), "\(fields)")
            XCTAssertNil(FeedPostResources.avatarURL(for: item, baseURL: base), "\(fields)")
        }
    }

    func testGeneratedThumbnailsAndFollowedAvatarDoNotRequireAttachments() throws {
        for hero in ["_videoThumbnail.png", "_audioThumbnail.png"] {
            let item = try decode(["hero": hero])
            XCTAssertEqual(FeedPostResources.heroURL(for: item, baseURL: base)?.lastPathComponent, hero)
            XCTAssertEqual(FeedPostResources.avatarURL(for: item, baseURL: base)?.path, "/f/\(ipns)/avatar.png")
        }
    }

    func testWebsiteURLsAllowOnlyAbsoluteWebAddresses() throws {
        for address in ["https://example.com/post/?a=1#part", "http://example.com/post/"] {
            XCTAssertEqual(FeedPostResources.websiteURL(for: try decode(["url": address]))?.absoluteString, address)
        }
        for address in ["", "/v0/status", "//example.com/post/", "file:///etc/passwd", "javascript:alert(1)",
                        "croptop://quit", "https://", "https://user:pass@example.com/", "https://example.com/\n"] {
            XCTAssertNil(FeedPostResources.websiteURL(for: try decode(["url": address])), address)
        }
        let item = try decode(["attachments": ["cover.png"], "hero": "cover.png"])
        XCTAssertTrue(FeedPostResources.attachments(for: item, baseURL: URL(string: "file:///tmp/")!).isEmpty)
    }

    private func decode(_ fields: [String: Any]) throws -> FeedItem {
        let required: [String: Any] = ["ipns": ipns, "site": "Example", "id": postID, "title": "Post",
                                       "summary": "", "link": "/f/\(ipns)/\(postID)/",
                                       "url": "https://example.com/\(postID)/", "created": 0,
                                       "preview": false, "pinned": false]
        return try JSONDecoder().decode(FeedItem.self, from: JSONSerialization.data(withJSONObject:
            required.merging(fields) { _, new in new }))
    }
}
