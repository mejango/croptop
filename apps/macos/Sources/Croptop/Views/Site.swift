// One of my sites: header, tag filter, tiles, publish.
import SwiftUI
import AppKit

struct SiteView: View {
    @EnvironmentObject var model: AppModel
    var siteID: String
    @State private var tags: Set<String> = []
    @State private var url = ""

    var site: Site? { model.sites.first { $0.id == siteID } }
    var posts: [Post] { model.posts[siteID] ?? [] }
    var allTags: [String] { Array(Set(posts.flatMap { $0.tagList })).sorted() }
    var shown: [Post] { tags.isEmpty ? posts : posts.filter { tags.isSubset(of: Set($0.tagList)) } }
    var unpublished: Bool {
        guard let s = site, let last = s.lastPublished else { return !posts.isEmpty }
        return posts.contains { $0.created > last }
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                ScreenHead(title: site?.name ?? "", subtitle: site?.about) {
                    HStack(spacing: 8) {
                        if !url.isEmpty {
                            Button(url.replacingOccurrences(of: "https://", with: "")) { if let u = URL(string: url) { NSWorkspace.shared.open(u) } }
                                .buttonStyle(BorderedButton(kind: .quiet))
                        }
                        Button(model.publishing.contains(siteID) ? "Publishing…" : "Publish") { model.publish(siteID) }
                            .buttonStyle(BorderedButton(kind: unpublished ? .hot : .plain))
                            .disabled(model.publishing.contains(siteID))
                    }
                }
                if unpublished && !model.publishing.contains(siteID) {
                    HStack(spacing: 6) {
                        Circle().fill(Theme.attention).frame(width: 8, height: 8)
                        Text("Changes not published yet.").font(Theme.body(13)).foregroundColor(Theme.muted)
                    }.padding(.bottom, 16)
                }
                if !allTags.isEmpty {
                    ScrollView(.horizontal, showsIndicators: false) {
                        HStack(spacing: 6) {
                            ForEach(allTags, id: \.self) { t in
                                Button(t) { if tags.contains(t) { tags.remove(t) } else { tags.insert(t) } }
                                    .buttonStyle(BorderedButton(kind: tags.contains(t) ? .hot : .quiet))
                            }
                            if !tags.isEmpty { Button("clear") { tags = [] }.buttonStyle(.plain).font(Theme.body(13)).foregroundColor(Theme.muted) }
                        }
                    }.padding(.bottom, 16)
                }
                LazyVGrid(columns: [GridItem(.adaptive(minimum: 200, maximum: 240), spacing: 16, alignment: .top)], alignment: .leading, spacing: 16) {
                    NewTile { model.screen = .editor(site: siteID, post: nil) }
                    ForEach(shown) { p in
                        PostTile(siteID: siteID, post: p)
                            .onTapGesture { model.screen = .editor(site: siteID, post: p.id) }
                            .contextMenu {
                                Button("Edit") { model.screen = .editor(site: siteID, post: p.id) }
                                Button("Copy link") {
                                    NSPasteboard.general.clearContents()
                                    NSPasteboard.general.setString(url + p.link.trimmingCharacters(in: CharacterSet(charactersIn: "/")) + "/", forType: .string)
                                }
                                Divider()
                                Button("Delete", role: .destructive) {
                                    Task { try? await API.shared.deletePost(site: siteID, id: p.id); await model.loadPosts(siteID); model.toast("Deleted.") }
                                }
                            }
                    }
                }
            }
            .padding(Theme.content)
        }
        .task {
            await model.loadPosts(siteID)
            url = (try? await API.shared.siteURL(siteID)) ?? ""
        }
    }
}

struct PostTile: View {
    var siteID: String
    var post: Post
    var image: String? {
        if let h = post.heroImage, !h.isEmpty { return h }
        return post.attachments.first { ["png", "jpg", "jpeg", "gif", "webp"].contains(($0 as NSString).pathExtension.lowercased()) }
    }
    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            // A square: the image covers it, or the first lines of the post stand in.
            Color.clear
                .aspectRatio(1, contentMode: .fit)
                .overlay {
                    if let img = image {
                        AsyncImage(url: API.shared.siteFile(siteID, "\(post.id)/\(img)")) { i in i.resizable().scaledToFill() } placeholder: { Rectangle().fill(Theme.rule) }
                    } else {
                        Text(post.content.isEmpty ? post.title : post.content)
                            .font(Theme.body(13)).lineLimit(9).padding(10)
                            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
                    }
                }
                .clipped()
                .overlay(Rectangle().stroke(Theme.ink, lineWidth: Theme.border))
                .overlay(alignment: .topTrailing) {
                    HStack(spacing: 4) {
                        if post.pinned != nil { Text("pinned").font(Theme.pixel(11)).padding(4).background(Theme.paper) }
                        if post.isPage { Text("page").font(Theme.pixel(11)).padding(4).background(Theme.paper) }
                    }.padding(6)
                }
            Text(post.title.isEmpty ? post.date.formatted(date: .abbreviated, time: .omitted) : post.title).font(Theme.body(14)).lineLimit(1)
            HStack(spacing: 6) {
                Text(post.date.formatted(date: .numeric, time: .omitted)).font(Theme.body(13)).foregroundColor(Theme.muted)
                ForEach(post.tagList.prefix(3), id: \.self) { t in Text(t).font(Theme.body(13)).foregroundColor(Theme.muted) }
            }.lineLimit(1)
        }
        .contentShape(Rectangle())
    }
}

struct NewTile: View {
    var action: () -> Void
    var body: some View {
        Button(action: action) {
            VStack(alignment: .leading, spacing: 8) {
                Color.clear
                    .aspectRatio(1, contentMode: .fit)
                    .overlay {
                        VStack(spacing: 8) {
                            Text("+").font(Theme.pixel(28))
                            Text("New post").font(Theme.pixel(14))
                        }
                    }
                    .overlay(Rectangle().strokeBorder(style: StrokeStyle(lineWidth: Theme.border, dash: [6, 4])).foregroundColor(Theme.ink))
                Text(" ").font(Theme.body(14))
                Text(" ").font(Theme.body(13))
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }
}
