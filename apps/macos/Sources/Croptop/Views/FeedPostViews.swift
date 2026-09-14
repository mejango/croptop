import SwiftUI

struct FeedPostTile: View {
    var item: FeedItem
    var square = false
    var focused = false
    var revision: Double

    private var fallbackText: String {
        if !item.summary.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty { return item.summary }
        return item.title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? "Untitled" : item.title
    }

    var body: some View {
        PostMediaTile(
            imageURL: FeedPostResources.heroURL(for: item, baseURL: API.shared.base),
            revision: revision,
            widgetSource: FeedPostResources.widgetSource(for: item, baseURL: API.shared.base),
            fallbackText: fallbackText,
            pinned: item.pinned,
            square: square,
            focused: focused
        ) {
            PostMediaCaption(
                title: item.title,
                date: item.date,
                author: item.site,
                authorAvatarURL: FeedPostResources.avatarURL(for: item, baseURL: API.shared.base),
                avatarRevision: String(revision)
            )
        }
    }
}

struct FeedPostRow: View {
    var item: FeedItem
    var revision: Double

    private var fallbackSymbol: String {
        let audioTypes: Set<String> = ["mp3", "m4a", "wav", "ogg", "aac", "flac", "aiff", "aif", "opus"]
        if (item.attachments ?? []).contains(where: { audioTypes.contains(($0 as NSString).pathExtension.lowercased()) }) {
            return "waveform"
        }
        return item.preview ? "play.rectangle" : "doc.text"
    }

    var body: some View {
        PostMediaRow(
            imageURL: FeedPostResources.heroURL(for: item, baseURL: API.shared.base),
            revision: revision,
            widgetSource: FeedPostResources.widgetSource(for: item, baseURL: API.shared.base),
            fallbackSymbol: fallbackSymbol,
            pinned: item.pinned
        ) {
            VStack(alignment: .leading, spacing: 6) {
                if item.title.isEmpty { Text("Untitled").font(Theme.bold(14)) }
                PostMediaCaption(
                    title: item.title,
                    date: item.date,
                    author: item.site,
                    authorAvatarURL: FeedPostResources.avatarURL(for: item, baseURL: API.shared.base),
                    avatarRevision: String(revision),
                    onImage: false
                )
                if !item.summary.isEmpty {
                    Text(item.summary).font(Theme.body(13)).foregroundColor(Theme.muted).lineLimit(2)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }
}
