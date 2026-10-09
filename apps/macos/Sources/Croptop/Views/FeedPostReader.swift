import SwiftUI
import AppKit

struct FeedPostReader: View {
    var item: FeedItem
    @EnvironmentObject var model: AppModel
    @Environment(\.dismiss) private var dismiss

    private var attachments: [PreviewAttachment] {
        FeedPostResources.attachments(for: item, baseURL: API.shared.base)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack(alignment: .top, spacing: 16) {
                VStack(alignment: .leading, spacing: 6) {
                    Text(item.site).font(Theme.bold(18)).lineLimit(1)
                    Text(item.date.formatted(date: .abbreviated, time: .shortened))
                        .font(Theme.body(13)).foregroundColor(Theme.muted)
                }
                Spacer(minLength: 0)
                if let siteID = item.siteID, model.sites.contains(where: { $0.id == siteID }) {
                    Button {
                        dismiss()
                        model.screen = .editor(site: siteID, post: item.postID)
                    } label: { Label("Edit", systemImage: "pencil") }
                    .buttonStyle(TextActionButtonStyle())
                }
                if let url = FeedPostResources.websiteURL(for: item) {
                    Button { NSWorkspace.shared.open(url) } label: {
                        Label("Website", systemImage: "globe")
                    }.buttonStyle(TextActionButtonStyle()).help("Open this post on its website")
                }
                Button { dismiss() } label: { Label("Done", systemImage: "checkmark") }
                    .buttonStyle(TextActionButtonStyle()).keyboardShortcut(.cancelAction)
            }
            if item.preview {
                Text("Interactive elements are available on the website.")
                    .font(Theme.body(13)).foregroundColor(Theme.muted)
            }
            if item.content == nil {
                Text("The full text is unavailable in this local copy. Refresh or open the website.")
                    .font(Theme.body(14)).foregroundColor(Theme.muted)
            }
            PostContentPreview(
                title: item.title,
                content: FeedReaderContent.withMedia(item.content ?? "", attachments: attachments),
                attachments: attachments,
                allowRemoteMedia: false
            )
            .overlay(Rectangle().strokeBorder(Theme.rule, lineWidth: 1))
        }
        .padding(Theme.content)
        .frame(minWidth: 700, idealWidth: 1040, minHeight: 560, idealHeight: 780)
        .background(Theme.paper)
    }
}

enum FeedReaderContent {
    /// Media-only posts keep their attachments visible even without a Markdown embed.
    static func withMedia(_ content: String, attachments: [PreviewAttachment]) -> String {
        let imageTypes: Set<String> = ["png", "jpg", "jpeg", "gif", "webp", "avif", "svg"]
        let videoTypes: Set<String> = ["mp4", "m4v", "mov", "webm"]
        let audioTypes: Set<String> = ["mp3", "m4a", "wav", "ogg", "aac"]
        let componentCharacters = CharacterSet(charactersIn: "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~")
        let embedded = localMediaNames(in: content)
        var result = content
        for attachment in attachments {
            guard let name = attachment.name.addingPercentEncoding(withAllowedCharacters: componentCharacters),
                  !embedded.contains(attachment.name) else { continue }
            let source = "croptop-preview://attachment/" + name
            let ext = (attachment.name as NSString).pathExtension.lowercased()
            if imageTypes.contains(ext) {
                result += "\n\n<img src=\"\(source)\" alt=\"\">"
            } else if videoTypes.contains(ext) {
                result += "\n\n<video controls preload=\"metadata\" src=\"\(source)\"></video>"
            } else if audioTypes.contains(ext) {
                result += "\n\n<audio controls preload=\"metadata\" src=\"\(source)\"></audio>"
            }
        }
        return result
    }

    private static func localMediaNames(in content: String) -> Set<String> {
        // Code examples and comments do not display their apparent media tags.
        let visible = content.replacingOccurrences(of: #"(?s)```.*?```|~~~.*?~~~|<!--.*?-->|`[^`\n]*`"#, with: "", options: .regularExpression)
        var references = captures(#"!\[[^\]\n]*\]\(\s*(?:<([^>\n]+)>|([^\s)]+))"#, in: visible)
        for tag in captures(#"(?is)(<(?:img|video|audio|source)\b[^>]*>)"#, in: visible) {
            references += captures(#"(?is)\s(?:src|poster)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>`]+))"#, in: tag)
        }
        let base = URL(string: "croptop-preview://attachment/")!
        return Set(references.compactMap { reference in
            guard let url = URL(string: reference, relativeTo: base)?.absoluteURL,
                  let parts = URLComponents(url: url, resolvingAgainstBaseURL: true),
                  parts.scheme == "croptop-preview", parts.host == "attachment",
                  parts.user == nil, parts.password == nil, parts.port == nil,
                  parts.query == nil, parts.fragment == nil, parts.path.hasPrefix("/") else { return nil }
            return String(parts.path.dropFirst())
        })
    }

    private static func captures(_ pattern: String, in content: String) -> [String] {
        guard let expression = try? NSRegularExpression(pattern: pattern) else { return [] }
        let source = content as NSString
        return expression.matches(in: content, range: NSRange(location: 0, length: source.length)).compactMap { match in
            for group in 1..<match.numberOfRanges {
                let range = match.range(at: group)
                if range.location != NSNotFound, range.length > 0 { return source.substring(with: range) }
            }
            return nil
        }
    }
}
