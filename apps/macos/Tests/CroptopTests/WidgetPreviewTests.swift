import XCTest
import WebKit
@testable import Croptop

final class WidgetPreviewTests: XCTestCase {
    func testRecognizesBothPublishedWidgetFormats() {
        XCTAssertTrue(WidgetPreviewSource.hasPreview(content: "", attachments: ["_cover.png", "preview.js"]))
        XCTAssertTrue(WidgetPreviewSource.hasPreview(content: #"<script type="croptop/preview">export default ()=>{}</script>"#, attachments: []))
        XCTAssertTrue(WidgetPreviewSource.hasPreview(content: "<SCRIPT type='croptop/preview'>", attachments: []))
        XCTAssertFalse(WidgetPreviewSource.hasPreview(content: "ordinary text", attachments: ["_cover.png", "script.js"]))
        XCTAssertFalse(WidgetPreviewSource.hasPreview(content: "<script type='module'>", attachments: []))
    }

    func testOwnedWidgetUsesPublicSiteRootAndIgnoresGeneratedCover() throws {
        let post = try makePost()
        XCTAssertEqual(post.tileImage, "_cover.png")
        XCTAssertTrue(post.hasWidgetPreview)
        let source = try XCTUnwrap(WidgetPreviewSource.owned(siteID: "11111111-1111-1111-1111-111111111111", post: post,
                                                         baseURL: URL(string: "http://127.0.0.1:8086/v0/status?token=private#fragment")!))
        let url = source.url(revision: 4, compact: true)
        XCTAssertEqual(url.path, "/11111111-1111-1111-1111-111111111111")
        XCTAssertTrue(source.siteRoot.absoluteString.hasSuffix("/"))
        let query = URLComponents(url: url, resolvingAgainstBaseURL: false)!.queryItems!
        XCTAssertEqual(query.first(where: { $0.name == "preview" })?.value, post.id)
        XCTAssertEqual(query.first(where: { $0.name == "preview-size" })?.value, "row")
        XCTAssertNil(query.first(where: { $0.name == "token" }))
        XCTAssertNil(url.fragment)
    }

    func testRejectsSiteAndPostPathsOutsidePublicTree() throws {
        let base = URL(string: "http://127.0.0.1:8086")!
        for id in ["../v0", "v0/status", "https://evil.test", "%2e%2e", ""] {
            XCTAssertNil(WidgetPreviewSource.owned(siteID: id, post: try makePost(), baseURL: base))
            XCTAssertNil(WidgetPreviewSource.owned(siteID: "11111111-1111-1111-1111-111111111111", post: try makePost(id: id), baseURL: base))
        }
    }

    @MainActor func testResourceRulesCompileInWebKit() async throws {
        let source = WidgetPreviewSource(siteRoot: URL(string: "http://127.0.0.1:8086/11111111-1111-1111-1111-111111111111/")!, postID: "post")
        let id = "test-widget-" + UUID().uuidString
        let rules = try await WKContentRuleListStore.default().compileContentRuleList(forIdentifier: id, encodedContentRuleList: source.contentRules)
        XCTAssertNotNil(rules)
        try await WKContentRuleListStore.default().removeContentRuleList(forIdentifier: id)
    }

    private func makePost(id: String = "22222222-2222-2222-2222-222222222222") throws -> Post {
        let object: [String: Any] = ["id": id, "title": "Widget", "content": "", "created": 0,
                                     "articleType": 0, "link": "/post/", "attachments": ["preview.js"], "heroImage": "_cover.png"]
        return try JSONDecoder().decode(Post.self, from: JSONSerialization.data(withJSONObject: object))
    }
}
