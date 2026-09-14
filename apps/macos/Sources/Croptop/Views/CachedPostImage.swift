import SwiftUI
import AppKit
import ImageIO

// Lazy grids recreate off-screen cells. Keep decoded thumbnails outside the
// cells so a cache hit can draw in the very first frame after scrolling back.
@MainActor
final class PostImageCache {
    static let shared = PostImageCache()
    private let images: NSCache<NSURL, NSImage>
    @MainActor private final class LoadedImage {
        let image: NSImage
        init(_ image: NSImage) { self.image = image }
    }
    private var loading: [URL: Task<LoadedImage?, Never>] = [:]
    private let session: URLSession

    init(session: URLSession? = nil, images: NSCache<NSURL, NSImage> = NSCache()) {
        self.images = images
        images.totalCostLimit = 128 * 1024 * 1024
        images.countLimit = 512
        if let session { self.session = session }
        else {
            let configuration = URLSessionConfiguration.default
            configuration.urlCache = URLCache(memoryCapacity: 16 * 1024 * 1024, diskCapacity: 256 * 1024 * 1024, diskPath: "croptop-post-previews")
            configuration.timeoutIntervalForRequest = 30
            self.session = URLSession(configuration: configuration)
        }
    }

    func cached(_ url: URL) -> NSImage? { images.object(forKey: url as NSURL) }

    func image(_ url: URL) async -> NSImage? {
        if let image = cached(url) { return image }
        if let task = loading[url] { return await task.value?.image }
        // An individual cell disappearing must not cancel the shared download.
        let task = Task<LoadedImage?, Never> { [session] in
            do {
                let (data, response) = try await session.data(from: url)
                guard (response as? HTTPURLResponse)?.statusCode == 200 else { return nil }
                let thumbnail = await Task.detached(priority: .userInitiated) {
                    guard let source = CGImageSourceCreateWithData(data as CFData, nil) else { return nil as CGImage? }
                    return CGImageSourceCreateThumbnailAtIndex(source, 0, [
                        kCGImageSourceCreateThumbnailFromImageAlways: true,
                        kCGImageSourceCreateThumbnailWithTransform: true,
                        kCGImageSourceThumbnailMaxPixelSize: 1024,
                        kCGImageSourceShouldCacheImmediately: true
                    ] as CFDictionary)
                }.value
                guard let thumbnail else { return nil }
                let image = NSImage(cgImage: thumbnail, size: NSSize(width: thumbnail.width, height: thumbnail.height))
                images.setObject(image, forKey: url as NSURL, cost: thumbnail.bytesPerRow * thumbnail.height)
                return LoadedImage(image) // NSCache may evict immediately under memory pressure.
            } catch { return nil }
        }
        loading[url] = task
        let result = await task.value
        loading[url] = nil
        return result?.image
    }
}

struct CachedPostImage: View {
    var url: URL
    var revision: Double
    var contentMode: ContentMode = .fill
    var fallbackText: String? = nil
    var onImageSize: ((CGSize) -> Void)? = nil
    @State private var loaded: (url: URL, image: NSImage)?
    @State private var failedURL: URL?

    // A saved edit can replace an attachment at the same path. Version the
    // request as well as the decoded cache so it cannot reuse the old image.
    static func requestURL(_ url: URL, revision: Double) -> URL {
        var components = URLComponents(url: url, resolvingAgainstBaseURL: true)!
        components.queryItems = (components.queryItems ?? []) + [URLQueryItem(name: "previewRevision", value: String(revision))]
        return components.url!
    }
    private var key: URL { Self.requestURL(url, revision: revision) }
    var body: some View {
        Group {
            if let image = PostImageCache.shared.cached(key) ?? (loaded?.url == key ? loaded?.image : nil) {
                Image(nsImage: image).resizable().aspectRatio(contentMode: contentMode)
            } else if failedURL == key {
                Text(fallbackText ?? "Image unavailable")
                    .font(Theme.body(14)).lineLimit(12).padding(16)
                    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
                    .background(Theme.paper)
            } else {
                LoadingTicker(accessibilityLabel: "Loading image")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .background(Theme.paper)
            }
        }
        .task(id: key) {
            let requested = key
            failedURL = nil
            let image = await PostImageCache.shared.image(requested)
            guard !Task.isCancelled else { return }
            if let image {
                loaded = (requested, image)
                onImageSize?(image.size)
            } else {
                failedURL = requested
            }
        }
    }
}
