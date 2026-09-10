// Home: every post from the sites this node follows, newest first.
import SwiftUI
import AppKit

struct FeedView: View {
    @EnvironmentObject var model: AppModel
    @State private var checking = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                ScreenHead(title: "Feed", subtitle: model.following.isEmpty ? nil : "\(model.following.count) site\(model.following.count == 1 ? "" : "s") you follow.") {
                    Button(checking ? "Checking…" : "Check for new posts") {
                        checking = true
                        Task { await model.refreshFeed(); checking = false }
                    }
                    .buttonStyle(BorderedButton()).disabled(checking)
                }
                if model.feed.isEmpty {
                    if model.following.isEmpty {
                        EmptyState(text: "You are not following anyone yet. A few to start with:")
                        HStack(spacing: 8) {
                            ForEach(["croptop.eth", "follo.eth", "jango.eth"], id: \.self) { n in
                                Button(n) { Task { try? await API.shared.follow(n); await model.load(); await model.refreshFeed() } }.buttonStyle(BorderedButton(kind: .quiet))
                            }
                        }.padding(.top, 10)
                    } else {
                        EmptyState(text: "Nothing yet. The sites you follow have no posts your node has fetched; try checking for new posts.")
                    }
                } else {
                    LazyVStack(alignment: .leading, spacing: 16) {
                        ForEach(model.feed) { item in FeedCard(item: item) }
                    }
                    .frame(maxWidth: 720, alignment: .leading)
                }
            }
            .padding(Theme.content)
        }
    }
}

struct FeedCard: View {
    var item: FeedItem
    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 8) {
                AsyncImage(url: API.shared.url("/f/\(item.ipns)/avatar.png")) { img in img.resizable().scaledToFill() } placeholder: { Circle().fill(Theme.rule) }
                    .frame(width: 20, height: 20).clipShape(Circle())
                Text(item.site).font(Theme.bold(14))
                Text(item.date.formatted(date: .abbreviated, time: .shortened)).font(Theme.body(13)).foregroundColor(Theme.muted)
                if item.pinned { Text("pinned").font(Theme.pixel(12)).foregroundColor(Theme.muted) }
                Spacer()
            }
            if !item.title.isEmpty { Text(item.title).font(Theme.pixel(18)) }
            if !item.summary.isEmpty { Text(item.summary).font(Theme.body()).lineLimit(4) }
            if let h = item.hero, !h.isEmpty {
                AsyncImage(url: API.shared.url(item.link + (h.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? h))) { i in i.resizable().scaledToFit() } placeholder: { Rectangle().fill(Theme.rule).frame(height: 120) }
                    .frame(maxHeight: 360).overlay(Rectangle().stroke(Theme.ink, lineWidth: Theme.border))
            }
            if item.preview { Text("interactive preview, open to see it").font(Theme.body(13)).foregroundColor(Theme.muted) }
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .overlay(Rectangle().stroke(Theme.rule, lineWidth: Theme.border))
        .contentShape(Rectangle())
        .onTapGesture { NSWorkspace.shared.open(API.shared.url(item.url)) }
    }
}
