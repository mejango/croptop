import Foundation

/// Feed metadata can originate on another site. Resource requests must stay inside
/// that post's public tree, independently of URLs supplied by its author.
enum FeedPostResources {
    static func widgetSource(for item: FeedItem, baseURL: URL) -> WidgetPreviewSource? {
        guard item.preview, let scope = scope(for: item),
              let index = resourceURL("index.html", path: scope.site, baseURL: baseURL) else { return nil }
        return WidgetPreviewSource(siteRoot: index.deletingLastPathComponent(), postID: item.postID)
    }

    static func attachments(for item: FeedItem, baseURL: URL) -> [PreviewAttachment] {
        guard let scope = scope(for: item) else { return [] }
        var seen = Set<String>()
        return (item.attachments ?? []).compactMap { name in
            guard seen.insert(name).inserted,
                  let url = resourceURL(name, path: scope.post, baseURL: baseURL) else { return nil }
            return PreviewAttachment(name: name, url: url)
        }
    }

    static func heroURL(for item: FeedItem, baseURL: URL) -> URL? {
        guard let scope = scope(for: item), let hero = item.hero else { return nil }
        // Generated audio/video thumbnails need not appear in the attachment list.
        return resourceURL(hero, path: scope.post, baseURL: baseURL)
    }

    static func avatarURL(for item: FeedItem, baseURL: URL) -> URL? {
        guard let scope = scope(for: item) else { return nil }
        return resourceURL("avatar.png", path: scope.site, baseURL: baseURL)
    }

    static func websiteURL(for item: FeedItem) -> URL? {
        guard !item.url.unicodeScalars.contains(where: { CharacterSet.controlCharacters.contains($0) }),
              let components = URLComponents(string: item.url),
              isWebURL(components), components.user == nil, components.password == nil else { return nil }
        return components.url
    }

    private static func scope(for item: FeedItem) -> (site: String, post: String)? {
        guard matches(item.postID, "[A-Za-z0-9_-]{1,128}") else { return nil }
        let site: String
        if let siteID = item.siteID {
            guard UUID(uuidString: siteID) != nil else { return nil }
            site = "/\(siteID)/"
        } else {
            guard matches(item.ipns, "(?:k51[a-z0-9]{20,100}|Qm[1-9A-HJ-NP-Za-km-z]{44})") else { return nil }
            site = "/f/\(item.ipns)/"
        }
        let post = site + item.postID + "/"
        guard item.link == post else { return nil }
        return (site, post)
    }

    private static func resourceURL(_ name: String, path: String, baseURL: URL) -> URL? {
        guard validFilename(name),
              let encodedName = name.addingPercentEncoding(withAllowedCharacters: componentCharacters),
              var components = URLComponents(url: baseURL, resolvingAgainstBaseURL: true),
              isWebURL(components) else { return nil }
        components.percentEncodedPath = path + encodedName
        components.query = nil
        components.fragment = nil
        return components.url
    }

    private static let componentCharacters = CharacterSet(charactersIn: "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~")

    private static func validFilename(_ name: String) -> Bool {
        guard !name.isEmpty else { return false }
        var candidate = name
        while true {
            guard candidate != ".", candidate != "..",
                  !candidate.contains("/"), !candidate.contains("\\"),
                  !candidate.unicodeScalars.contains(where: { CharacterSet.controlCharacters.contains($0) }) else { return false }
            // Reject traversal even if an intermediary decodes escaped input again.
            guard let decoded = candidate.removingPercentEncoding, decoded != candidate else { return true }
            candidate = decoded
        }
    }

    private static func isWebURL(_ components: URLComponents) -> Bool {
        guard let scheme = components.scheme?.lowercased(),
              scheme == "http" || scheme == "https",
              let host = components.host, !host.isEmpty else { return false }
        return true
    }

    private static func matches(_ value: String, _ pattern: String) -> Bool {
        value.range(of: "\\A(?:\(pattern))\\z", options: .regularExpression) != nil
    }
}
