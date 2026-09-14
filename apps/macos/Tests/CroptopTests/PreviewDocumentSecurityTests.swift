import XCTest
@testable import Croptop

final class PreviewDocumentSecurityTests: XCTestCase {
    func testFeedPolicyRestrictsMediaToAttachmentSchemeAndImageData() throws {
        let html = PreviewDocument.html(
            title: "Followed post",
            body: "<img src=\"http://127.0.0.1:8086/v0/status\"><video src=\"https://example.com/video.mp4\"></video>",
            allowRemoteMedia: false
        )
        let policy = try directives(in: html)
        XCTAssertEqual(policy["img-src"], Set(["croptop-preview:", "data:"]))
        XCTAssertEqual(policy["media-src"], Set(["croptop-preview:"]))
        for directive in ["img-src", "media-src"] {
            let sources = try XCTUnwrap(policy[directive])
            XCTAssertFalse(sources.contains("http:"))
            XCTAssertFalse(sources.contains("https:"))
            XCTAssertTrue(sources.contains("croptop-preview:"))
        }
    }

    func testBothPoliciesBlockUnspecifiedResourcesAndFormActions() throws {
        for allowRemoteMedia in [false, true] {
            let policy = try directives(in: PreviewDocument.html(
                title: "", body: "<form action=\"https://example.com/submit\"></form>",
                allowRemoteMedia: allowRemoteMedia
            ))
            XCTAssertEqual(policy["default-src"], Set(["'none'"]))
            XCTAssertEqual(policy["form-action"], Set(["'none'"]))
            XCTAssertEqual(policy["font-src"], Set(["data:"]))
            XCTAssertNil(policy["script-src"])
        }
    }

    func testDefaultAndExplicitEditorPolicyPreserveRemoteMedia() throws {
        let defaultPolicy = try directives(in: PreviewDocument.html(title: "", body: ""))
        let explicitPolicy = try directives(in: PreviewDocument.html(title: "", body: "", allowRemoteMedia: true))
        XCTAssertEqual(defaultPolicy, explicitPolicy)
        XCTAssertEqual(defaultPolicy["img-src"], Set(["croptop-preview:", "data:", "http:", "https:"]))
        XCTAssertEqual(defaultPolicy["media-src"], Set(["croptop-preview:", "http:", "https:"]))
    }

    private func directives(in html: String) throws -> [String: Set<String>] {
        let expression = try NSRegularExpression(
            pattern: #"<meta\s+http-equiv="Content-Security-Policy"\s+content="([^"]*)""#,
            options: [.caseInsensitive]
        )
        let match = try XCTUnwrap(expression.firstMatch(in: html, range: NSRange(html.startIndex..., in: html)))
        let range = try XCTUnwrap(Range(match.range(at: 1), in: html))
        var result: [String: Set<String>] = [:]
        for directive in html[range].split(separator: ";") {
            let tokens = directive.split(whereSeparator: { $0.isWhitespace }).map(String.init)
            guard let name = tokens.first else { continue }
            XCTAssertNil(result[name], "Duplicate CSP directive: \(name)")
            result[name] = Set(tokens.dropFirst())
        }
        return result
    }
}
