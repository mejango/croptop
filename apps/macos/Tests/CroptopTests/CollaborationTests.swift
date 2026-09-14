import XCTest
@testable import Croptop

final class CollaborationTests: XCTestCase {
    @MainActor func testOpenCollaborationDefersAppRelaunch() {
        XCTAssertTrue(AppUpdater.blocksRelaunch(screen: .site("site"), sheet: nil, publishing: [], collaborationOpen: true))
        XCTAssertFalse(AppUpdater.blocksRelaunch(screen: .site("site"), sheet: nil, publishing: [], collaborationOpen: false))
    }

    func testLegacyCompositeAndOriginDecode() throws {
        let site = try JSONDecoder().decode(Site.self, from: Data(#"{"id":"site","name":"Together","ipns":"owner","aggregation":["source"]}"#.utf8))
        XCTAssertTrue(site.isCollaborative)
        let post = try JSONDecoder().decode(Post.self, from: Data(#"{"id":"post","title":"A post","content":"Text","created":1,"articleType":0,"link":"/post/","attachments":[],"originalSiteName":"Maya","originalSiteDomain":"k51qzi5uqu5dlm2dzx1irz57ujwo3r11kifvxzf5l9z8g97u3f3ppx0540awwt","originalPostID":"22222222-2222-2222-2222-222222222222"}"#.utf8))
        XCTAssertEqual(post.originalSiteName, "Maya")
        XCTAssertEqual(post.originalURL?.scheme, "https")
        var unsafe = post
        unsafe.originalSiteDomain = "evil.example/path?redirect="
        XCTAssertNil(unsafe.originalURL)
        unsafe = post
        unsafe.originalPostID = "../../settings"
        XCTAssertNil(unsafe.originalURL)
    }

    func testSimpleSourceListDecodes() throws {
        let state = try JSONDecoder().decode(CollaborationState.self, from: Data(#"{"contributors":[{"ipns":"source","name":"Maya","mode":"all"}],"errors":{},"updated":1}"#.utf8))
        XCTAssertEqual(state.contributors.first?.label, "Maya")
    }
}
