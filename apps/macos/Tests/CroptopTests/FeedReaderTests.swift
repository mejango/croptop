import XCTest
@testable import Croptop

final class FeedReaderTests: XCTestCase {
    func testScopesRejectPostsFromAnotherCollectionOrFollowedSite() throws {
        let followed = try item(ipns: "first")
        let other = try item(ipns: "second")
        let owned = try item(ipns: "first", siteID: "owned-site")
        XCTAssertTrue(FeedScope.following.contains(followed))
        XCTAssertFalse(FeedScope.following.contains(owned))
        XCTAssertTrue(FeedScope.owned.contains(owned))
        XCTAssertFalse(FeedScope.owned.contains(followed))
        XCTAssertTrue(FeedScope.site("first").contains(followed))
        XCTAssertFalse(FeedScope.site("first").contains(other))
        XCTAssertFalse(FeedScope.site("first").contains(owned))
        XCTAssertEqual(Set([followed.id, other.id, owned.id]).count, 3)
    }

    func testMediaOnlyPostDisplaysImagesVideoAndAudioWithoutDroppingText() {
        let attachments = ["photo.png", "movie.mp4", "song.mp3"].map(attachment)
        let body = FeedReaderContent.withMedia("The complete text.", attachments: attachments)
        XCTAssertTrue(body.hasPrefix("The complete text."))
        XCTAssertTrue(body.contains("<img "))
        XCTAssertTrue(body.contains("<video controls"))
        XCTAssertTrue(body.contains("<audio controls"))
        for name in ["photo.png", "movie.mp4", "song.mp3"] {
            XCTAssertTrue(body.contains("croptop-preview://attachment/" + name))
        }
    }

    func testEmbeddedMediaIsNotRepeatedAndFilenameCannotInjectMarkup() {
        let content = "![Photo](photo%20one.png)\n\nFull text."
        XCTAssertEqual(FeedReaderContent.withMedia(content, attachments: [attachment("photo one.png")]), content)
        let body = FeedReaderContent.withMedia("", attachments: [attachment("photo\" onerror=\"alert(1).png")])
        XCTAssertFalse(body.contains("onerror=\""))
        XCTAssertTrue(body.contains("%22"))
        XCTAssertTrue(body.contains("%3D"))
    }

    func testRemoteReferencesPlainMentionsAndCodeKeepLocalMediaFallback() {
        let contents = [
            "The attachment is photo.png.",
            "![Photo](https://example.com/photo.png)",
            "<img src=\"http://127.0.0.1:8086/photo.png\">",
            "<video src=\"https://example.com/movie.mp4\"></video>",
            "`![Photo](photo.png)`",
            "```html\n<img src=\"photo.png\">\n```",
            "<!-- <img src=\"photo.png\"> -->"
        ]
        for content in contents {
            let body = FeedReaderContent.withMedia(content, attachments: [attachment("photo.png"), attachment("movie.mp4")])
            XCTAssertTrue(body.hasPrefix(content))
            XCTAssertTrue(body.contains("croptop-preview://attachment/photo.png"), content)
            XCTAssertTrue(body.contains("croptop-preview://attachment/movie.mp4"), content)
        }
    }

    func testVerifiedLocalMediaEmbedsSuppressOnlyTheirOwnFallbacks() {
        for content in [
            "![Photo](./photo%20one.png)",
            "![Photo](croptop-preview://attachment/photo%20one.png)",
            "<img src='photo%20one.png'>",
            "<img src=\"croptop-preview://attachment/photo%20one.png\">"
        ] {
            XCTAssertEqual(FeedReaderContent.withMedia(content, attachments: [attachment("photo one.png")]), content)
        }
        let video = "<video poster=\"photo.png\" src=\"movie.mp4\"></video>"
        XCTAssertEqual(FeedReaderContent.withMedia(video, attachments: [attachment("photo.png"), attachment("movie.mp4")]), video)
        let linked = "[Download photo](photo.png)"
        XCTAssertTrue(FeedReaderContent.withMedia(linked, attachments: [attachment("photo.png")]).contains("<img "))
    }

    private func attachment(_ name: String) -> PreviewAttachment {
        PreviewAttachment(name: name, url: URL(string: "http://127.0.0.1:8086/photo.png")!)
    }

    private func item(ipns: String, siteID: String? = nil) throws -> FeedItem {
        var fields: [String: Any] = ["ipns": ipns, "site": "Site", "id": "same-post", "title": "Post",
                                     "summary": "", "link": "/", "url": "https://example.com/", "created": 0,
                                     "preview": false, "pinned": false]
        if let siteID { fields["siteID"] = siteID }
        return try JSONDecoder().decode(FeedItem.self, from: JSONSerialization.data(withJSONObject: fields))
    }
}
