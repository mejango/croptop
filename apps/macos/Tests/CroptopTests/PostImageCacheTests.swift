import XCTest
import AppKit
@testable import Croptop

private final class PreviewProtocol: URLProtocol {
    static var requests = 0
    static var failNext = false
    static let png = Data(base64Encoded: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a2ioAAAAASUVORK5CYII=")!
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        Self.requests += 1
        let status = Self.failNext ? 503 : 200
        Self.failNext = false
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: nil)!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Self.png)
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}

private final class DiscardingImageCache: NSCache<NSURL, NSImage> {
    override func setObject(_ obj: NSImage, forKey key: NSURL, cost g: Int) {
        // Model immediate eviction under memory pressure.
    }
}

final class PostImageCacheTests: XCTestCase {
    @MainActor func testDownloadedImageSurvivesImmediateCacheEviction() async {
        PreviewProtocol.requests = 0
        PreviewProtocol.failNext = false
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [PreviewProtocol.self]
        let cache = PostImageCache(session: URLSession(configuration: config), images: DiscardingImageCache())
        let url = URL(string: "https://preview.test/evicted.png")!
        var image: NSImage?
        var duplicate: NSImage?
        let first = Task<Void, Never> { image = await cache.image(url) }
        let second = Task<Void, Never> { duplicate = await cache.image(url) }
        await first.value
        await second.value
        XCTAssertNotNil(image)
        XCTAssertTrue(image === duplicate)
        XCTAssertNil(cache.cached(url))
        XCTAssertEqual(PreviewProtocol.requests, 1)
    }

    @MainActor func testCoalescesAndKeepsDecodedImageForReturningCells() async {
        PreviewProtocol.requests = 0
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [PreviewProtocol.self]
        let cache = PostImageCache(session: URLSession(configuration: config))
        let url = URL(string: "https://preview.test/photo.png")!
        var image: NSImage?
        var duplicate: NSImage?
        let first = Task<Void, Never> { image = await cache.image(url) }
        let second = Task<Void, Never> { duplicate = await cache.image(url) }
        await first.value
        await second.value
        XCTAssertNotNil(image)
        XCTAssertEqual(image?.size, NSSize(width: 1, height: 1))
        XCTAssertTrue(image === duplicate)
        XCTAssertTrue(cache.cached(url) === image)
        let returning = await cache.image(url)
        XCTAssertTrue(returning === image)
        XCTAssertEqual(returning?.size, image?.size)
        XCTAssertEqual(PreviewProtocol.requests, 1)
        let revised = CachedPostImage.requestURL(url, revision: 2)
        XCTAssertNil(cache.cached(revised))
        let updated = await cache.image(revised)
        XCTAssertNotNil(updated)
        XCTAssertEqual(PreviewProtocol.requests, 2)
    }

    @MainActor func testFailedPreviewCanRetry() async {
        PreviewProtocol.failNext = true
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [PreviewProtocol.self]
        let cache = PostImageCache(session: URLSession(configuration: config))
        let url = URL(string: "https://preview.test/retry.png")!
        let failed = await cache.image(url)
        XCTAssertNil(failed)
        XCTAssertNil(cache.cached(url))
        let retry = await cache.image(url)
        XCTAssertNotNil(retry)
    }

    func testPostDecodesNaturalMediaDimensions() throws {
        let post = try decodePost(["heroImage": "cover.jpg", "heroImageWidth": 1920, "heroImageHeight": 1080])
        XCTAssertEqual(post.tileImage, "cover.jpg")
        XCTAssertTrue(post.hasTileAspectRatio)
        XCTAssertEqual(post.tileAspectRatio, 16.0 / 9.0, accuracy: 0.0001)
    }

    func testMissingAndInvalidDimensionsUseSquareFallback() throws {
        let dimensionSets: [[String: Any]] = [[:], ["heroImageWidth": 100], ["heroImageHeight": 100],
                                             ["heroImageWidth": 100, "heroImageHeight": 0],
                                             ["heroImageWidth": -100, "heroImageHeight": 50]]
        for dimensions in dimensionSets {
            let post = try decodePost(dimensions)
            XCTAssertFalse(post.hasTileAspectRatio)
            XCTAssertEqual(post.tileAspectRatio, 1)
        }
        var post = try decodePost(["heroImageWidth": 100, "heroImageHeight": 50])
        post.heroImageWidth = .infinity
        XCTAssertFalse(post.hasTileAspectRatio)
        XCTAssertEqual(post.tileAspectRatio, 1)
        post.heroImageWidth = .nan
        XCTAssertFalse(post.hasTileAspectRatio)
        XCTAssertEqual(post.tileAspectRatio, 1)
    }

    func testTileImageUsesHeroThenMediaThumbnailThenImageAttachment() throws {
        XCTAssertEqual(try decodePost(["heroImage": "chosen.jpg", "videoFilename": "clip.mp4"]).tileImage, "chosen.jpg")
        XCTAssertEqual(try decodePost(["videoFilename": "clip.mp4", "audioFilename": "song.mp3"]).tileImage, "_videoThumbnail.png")
        XCTAssertEqual(try decodePost(["heroImage": "", "audioFilename": "song.mp3"]).tileImage, "_audioThumbnail.png")
        XCTAssertEqual(try decodePost(["attachments": ["document.pdf", "PHOTO.AVIF", "other.png"]]).tileImage, "PHOTO.AVIF")
        XCTAssertNil(try decodePost(["attachments": ["document.pdf"]]).tileImage)
    }

    private func decodePost(_ fields: [String: Any]) throws -> Post {
        let required: [String: Any] = ["id": "test", "title": "", "content": "", "created": 0,
                                       "articleType": 0, "link": "/test/", "attachments": []]
        let data = try JSONSerialization.data(withJSONObject: required.merging(fields) { _, value in value })
        return try JSONDecoder().decode(Post.self, from: data)
    }
}
