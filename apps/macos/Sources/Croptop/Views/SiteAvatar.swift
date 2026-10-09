import SwiftUI

struct SiteAvatar: View {
    // Separate from post previews so scrolling a large grid cannot evict logos.
    private static let cache = PostImageCache()
    var url: URL
    var name: String
    var size: CGFloat = 56
    var revision: String? = nil
    @State private var loaded: (url: URL, image: NSImage)?

    private var key: URL {
        guard let revision, var components = URLComponents(url: url, resolvingAgainstBaseURL: true) else { return url }
        components.queryItems = (components.queryItems ?? []) + [URLQueryItem(name: "avatarRevision", value: revision)]
        return components.url ?? url
    }

    var body: some View {
        ZStack {
            Theme.paper
            if let image = Self.cache.cached(key) ?? (loaded?.url == key ? loaded?.image : nil) {
                Image(nsImage: image).resizable().scaledToFit()
            } else {
                Theme.rule.opacity(0.35)
                Text(String(name.trimmingCharacters(in: .whitespacesAndNewlines).prefix(1)).uppercased())
                    .font(Theme.heading(20)).foregroundColor(Theme.muted)
            }
        }
        .frame(width: size, height: size).clipped().accessibilityHidden(true)
        .task(id: key) {
            let requested = key
            if let image = await Self.cache.image(requested), !Task.isCancelled {
                loaded = (requested, image)
            }
        }
    }
}
