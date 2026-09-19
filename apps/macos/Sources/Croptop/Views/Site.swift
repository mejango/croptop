// One of my sites: header, tag filter, tiles, publish.
import SwiftUI
import AppKit

struct SiteView: View {
    @EnvironmentObject var model: AppModel
    var siteID: String
    @State private var tags: Set<String> = []
    @State private var viewMode: SitePostViewMode = .tiles
    @State private var savedViewMode: SitePostViewMode = .tiles
    @FocusState private var focusedPost: String?
    @State private var url = ""
    @State private var highlight = Theme.hot
    @State private var palette = SitePalette()
    @State private var showingContributors = false
    @State private var collaborationState: CollaborationState?

    var site: Site? { model.sites.first { $0.id == siteID } }
    var posts: [Post] { model.posts[siteID] ?? [] }
    var allTags: [String] { Array(Set(posts.flatMap { $0.tagList })).sorted() }
    var shown: [Post] { tags.isEmpty ? posts : posts.filter { tags.isSubset(of: Set($0.tagList)) } }
    var unpublished: Bool {
        guard let s = site else { return false }
        guard let last = s.lastPublished else { return true }
        return (s.updated ?? 0) > last || posts.contains { max($0.created, $0.modified ?? 0) > last }
    }

    private var contributorCount: Int { collaborationState?.contributors.count ?? ((site?.contributors?.count ?? 0) + (site?.aggregation?.count ?? 0)) }
    private var headerIdentity: some View {
        HStack(alignment: .top, spacing: 16) {
            SiteAvatar(url: API.shared.siteFile(siteID, "avatar.png"), name: site?.name ?? "", size: 64, revision: site?.updated.map { String($0) })
            VStack(alignment: .leading, spacing: 8) {
                Text(site?.name ?? "").font(Theme.heading(28))
                if let about = site?.about, !about.isEmpty {
                    Text(about).font(Theme.body()).foregroundColor(Theme.muted)
                        .fixedSize(horizontal: false, vertical: true)
                }
                if site?.isCollaborative == true {
                    Button { showingContributors = true } label: {
                        HStack(spacing: 6) {
                            HStack(spacing: -4) {
                                ForEach(Array((collaborationState?.contributors ?? site?.contributors ?? []).prefix(3))) { contributor in
                                    SiteAvatar(url: API.shared.url("/v0/croptop/sites/\(siteID)/collaboration/sources/\(contributor.ipns)/avatar"), name: contributor.label, size: 20)
                                        .clipShape(Circle()).overlay(Circle().stroke(Theme.paper, lineWidth: 1))
                                }
                            }
                            Image(systemName: "person.2")
                            Text("\(contributorCount) \(contributorCount == 1 ? "contributor" : "contributors")")
                        }.font(Theme.body(13)).foregroundColor(Theme.muted)
                    }.buttonStyle(.hover)
                }
            }.frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    private var headerActions: some View {
        HStack(spacing: 16) {
            HStack(spacing: 8) {
                if !url.isEmpty {
                    IconActionButton("Website", systemImage: "globe",
                                     accessibilityLabel: "Open site: " + url) {
                        if let u = URL(string: url) { NSWorkspace.shared.open(u) }
                    }
                }
                IconActionButton("Settings", systemImage: "gearshape") { model.screen = .settings(siteID) }
            }
            Button(model.publishing.contains(siteID) ? "Publishing…" : "Publish") { model.publish(siteID) }
                .buttonStyle(BorderedButton(kind: unpublished ? .plain : .quiet))
                .accessibilityValue(unpublished ? "Unpublished changes" : "Up to date")
                .disabled(model.publishing.contains(siteID))
            Button("New post") { model.screen = .editor(site: siteID, post: nil) }.buttonStyle(BorderedButton(kind: .hot))
        }.fixedSize(horizontal: true, vertical: false)
    }

    private var postToolbar: some View {
        let labels = model.tagChoices(for: siteID)
        return HStack(spacing: 16) {
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 4) {
                    if !allTags.isEmpty {
                        TagChip(title: "all", selected: tags.isEmpty, showsRemoveMark: false) { tags = [] }
                        ForEach(allTags, id: \.self) { tag in
                            let label = labels[tag].flatMap { $0.isEmpty ? nil : $0 } ?? tag
                            TagChip(title: label, selected: tags.contains(tag)) {
                                if tags.contains(tag) { tags.remove(tag) } else { tags.insert(tag) }
                            }
                        }
                    }
                }.padding(2)
            }
            SitePostViewPicker(selection: $viewMode)
                .fixedSize()
                .onChange(of: viewMode) { mode in
                    // The site opens in the owner's view: the picker writes the template setting.
                    guard mode != savedViewMode else { return }
                    savedViewMode = mode
                    Task { try? await API.shared.saveTemplateSettings(siteID, values: ["postViewMode": mode.rawValue]); await model.load() }
                }
        }.padding(.bottom, 20)
    }

    @ViewBuilder private func postButton(_ post: Post, aspectFloor: CGFloat? = nil) -> some View {
        Button {
            if let original = post.originalURL { NSWorkspace.shared.open(original) }
            else { model.screen = .editor(site: siteID, post: post.id) }
        } label: {
            if viewMode == .list {
                PostRow(siteID: siteID, post: post, widgetRevision: site?.lastPublished, authorFallback: site?.isCollaborative == true ? site?.name : nil)
            } else {
                PostTile(siteID: siteID, post: post, widgetRevision: site?.lastPublished, authorFallback: site?.isCollaborative == true ? site?.name : nil,
                         square: viewMode == .more, minimumAspectRatio: aspectFloor, accent: highlight, focused: focusedPost == post.id)
            }
        }
        .buttonStyle(.plain)
        .focused($focusedPost, equals: post.id)
        .accessibilityLabel([post.title.isEmpty ? "Untitled post" : post.title,
                             post.date.formatted(date: .abbreviated, time: .omitted),
                             post.tagList.joined(separator: ", ")].filter { !$0.isEmpty }.joined(separator: ", "))
        .accessibilityHint(post.originalURL == nil ? "Edit post" : "View original post")
        .contextMenu {
            if post.originalSiteDomain == nil { Button("Edit") { model.screen = .editor(site: siteID, post: post.id) } }
            if let original = post.originalURL { Button("View original post") { NSWorkspace.shared.open(original) } }
            Button("Copy link") {
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(url + post.link.trimmingCharacters(in: CharacterSet(charactersIn: "/")) + "/", forType: .string)
            }
            Divider()
            if post.originalSiteDomain == nil {
                Button("Delete", role: .destructive) {
                    Task { try? await API.shared.deletePost(site: siteID, id: post.id); await model.loadPosts(siteID); model.toast("Deleted.") }
                }
            }
        }
    }

    @ViewBuilder private func postContent(width: CGFloat, height: CGFloat) -> some View {
        // Single crops tall media to about a window height.
        let singleAspectFloor = width / max(1, height * 0.85)
        if viewMode == .list {
            LazyVStack(alignment: .leading, spacing: 0) {
                ForEach(shown) { post in postButton(post) }
            }
        } else if viewMode == .more {
            let count = viewMode.columnCount(for: width)
            LazyVGrid(columns: Array(repeating: GridItem(.flexible(), spacing: viewMode.spacing, alignment: .top), count: count),
                      alignment: .leading, spacing: viewMode.spacing) {
                ForEach(shown) { post in postButton(post) }
            }
        } else {
            let count = viewMode.columnCount(for: width)
            // The template assigns posts left to right, then stacks each column independently.
            let columns = SitePostColumns.distribute(shown.map { SitePostEntry(post: $0) }, count: count)
            HStack(alignment: .top, spacing: viewMode.spacing) {
                ForEach(columns.indices, id: \.self) { column in
                    LazyVStack(spacing: viewMode.spacing) {
                        ForEach(columns[column]) { entry in
                            if let post = entry.post { postButton(post, aspectFloor: viewMode == .single ? singleAspectFloor : nil) }
                        }
                    }.frame(maxWidth: .infinity)
                }
            }
        }
        if !tags.isEmpty && shown.isEmpty {
            EmptyState(text: "No posts match these tags.")
        }
    }

    var body: some View {
        GeometryReader { geometry in
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    HStack(alignment: .top, spacing: 16) {
                        headerIdentity.frame(maxWidth: .infinity, alignment: .leading)
                        headerActions
                    }.padding(.bottom, 22)
                    if let legacy = model.status?.legacy?.first(where: { $0.id == siteID }) {
                        LegacyNotice(legacy: legacy).padding(.bottom, 22)
                    }
                    if model.posts[siteID]?.isEmpty == true {
                        CaptureShortcutPrompt().padding(.bottom, 28)
                    }
                    postToolbar
                    postContent(width: max(1, geometry.size.width - 2 * Theme.content), height: geometry.size.height)
                        .id(viewMode)
                }
                .padding(Theme.content)
                .foregroundColor(palette.ink)
            }
            .background(palette.paper)
        }
        .environment(\.sitePalette, palette)
        .sheet(isPresented: $showingContributors, onDismiss: { Task { await model.load(); await model.loadPosts(siteID); collaborationState = try? await API.shared.collaboration(siteID) } }) {
            VStack(alignment: .leading, spacing: 16) {
                HStack { Text("Contributors").font(Theme.heading(24)); Spacer(); Button("Done") { showingContributors = false }.buttonStyle(BorderedButton()) }
                ScrollView { ContributorsView(siteID: siteID) }
            }.padding(24).frame(width: 660, height: 680)
        }
        .task(id: siteID) {
            highlight = Theme.hot
            palette = SitePalette()
            guard let settings = try? await API.shared.templateSettings(siteID) else { return }
            palette = SitePalette(settings: settings)
            savedViewMode = SitePostViewMode(rawValue: settings["postViewMode"] as? String ?? "") ?? .tiles
            viewMode = savedViewMode
            if let value = settings["highlightColor"] as? String,
               let hex = UInt32(value.trimmingCharacters(in: CharacterSet(charactersIn: "#")), radix: 16) {
                highlight = Color(hex: hex)
            }
        }
        .task {
            await model.loadPosts(siteID)
            if site?.isCollaborative == true {
                do { collaborationState = try await API.shared.mergeSources(siteID); await model.loadPosts(siteID) }
                catch { collaborationState = try? await API.shared.collaboration(siteID) }
            }
            url = (try? await API.shared.siteURL(siteID)) ?? ""
        }
    }
}

private struct SitePostEntry: Identifiable {
    let post: Post?
    var id: String { post?.id ?? "new-post" }
}

// Owned posts and feed posts share the same media, caption, and row presentation.
struct PostTile: View {
    var siteID: String
    var post: Post
    var widgetRevision: Double? = nil
    var authorFallback: String? = nil
    var square = false
    var minimumAspectRatio: CGFloat? = nil
    var accent = Theme.hot
    var focused = false

    var body: some View {
        PostMediaTile(
            imageURL: post.tileImage.map { API.shared.siteFile(siteID, "\(post.id)/\($0)") },
            revision: max(post.modified ?? post.created, widgetRevision ?? 0),
            widgetSource: WidgetPreviewSource.owned(siteID: siteID, post: post, baseURL: API.shared.base),
            fallbackText: post.content.isEmpty ? (post.title.isEmpty ? "Untitled" : post.title) : post.content,
            knownAspectRatio: post.hasWidgetPreview ? 4 / 3 : post.hasTileAspectRatio ? CGFloat(post.tileAspectRatio) : nil,
            pinned: post.pinned != nil, square: square, minimumAspectRatio: minimumAspectRatio, accent: accent, focused: focused
        ) {
            PostCaption(post: post, authorFallback: authorFallback)
        }
    }
}

// Large tiles keep the complete image; More uses a dense grid of square crops.
struct PostMediaTile<Caption: View>: View {
    var imageURL: URL?
    var revision: Double
    var widgetSource: WidgetPreviewSource? = nil
    var fallbackText: String
    var knownAspectRatio: CGFloat? = nil
    var pinned = false
    var square = false
    var minimumAspectRatio: CGFloat? = nil
    var accent = Theme.hot
    var focused = false
    @ViewBuilder var caption: Caption
    @State private var hovering = false
    @State private var measuredImage: (key: URL, ratio: CGFloat)?
    @Environment(\.sitePalette) private var palette

    private var imageKey: URL? { imageURL.map { CachedPostImage.requestURL($0, revision: revision) } }
    private var ratio: CGFloat { max(naturalRatio, minimumAspectRatio ?? 0) }
    private var naturalRatio: CGFloat {
        if square { return 1 }
        if let knownAspectRatio, knownAspectRatio.isFinite, knownAspectRatio > 0 { return knownAspectRatio }
        if let measuredImage, measuredImage.key == imageKey { return measuredImage.ratio }
        if let imageKey, let image = PostImageCache.shared.cached(imageKey), image.size.width > 0, image.size.height > 0 {
            return image.size.width / image.size.height
        }
        return 1
    }
    private var showCaption: Bool { square || hovering || focused }

    var body: some View {
        Color.clear
            .aspectRatio(ratio, contentMode: .fit)
            .background(palette.paper)
            .overlay {
                if let widgetSource {
                    WidgetPostPreview(source: widgetSource, revision: revision, compact: false)
                } else if let imageURL {
                    CachedPostImage(url: imageURL, revision: revision,
                                    contentMode: square || minimumAspectRatio != nil ? .fill : .fit, fallbackText: fallbackText) { size in
                        guard size.width > 0, size.height > 0, let key = imageKey else { return }
                        measuredImage = (key, size.width / size.height)
                    }
                } else {
                    Text(fallbackText)
                        .font(Theme.body(square ? 13 : 16)).lineLimit(square ? 9 : 16).padding(16)
                        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
                }
            }
            .clipped()
            .overlay(alignment: .bottomLeading) {
                caption
                    .padding(10)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(palette.ink.opacity(0.78))
                    .opacity(showCaption ? 1 : 0)
                    .allowsHitTesting(false)
            }
            .clipped()
            .overlay(alignment: .topTrailing) {
                if pinned {
                    Image(systemName: "pin.fill").font(.system(size: 11))
                        .padding(6).background(palette.paper).padding(6)
                        .accessibilityLabel("Pinned")
                }
            }
            .overlay(Rectangle().strokeBorder(hovering || focused ? accent : Theme.rule, lineWidth: 1))
            .contentShape(Rectangle())
            .onHover { hovering = $0 }
            .animation(.easeOut(duration: 0.2), value: showCaption)
    }
}

struct PostCaption: View {
    var post: Post
    var authorFallback: String? = nil
    var onImage = true

    private var authorAvatarURL: URL? {
        guard post.originalURL != nil, let source = post.originalSiteDomain else { return nil }
        return URL(string: "https://\(source).crop.top/avatar.png")
    }

    var body: some View {
        PostMediaCaption(
            title: post.title, date: post.date,
            author: post.originalSiteName ?? authorFallback, authorAvatarURL: authorAvatarURL,
            isPage: post.isPage, tags: post.tagList, onImage: onImage
        )
    }
}

struct PostMediaCaption: View {
    var title: String
    var date: Date
    var author: String? = nil
    var authorAvatarURL: URL? = nil
    var avatarRevision: String? = nil
    var isPage = false
    var tags: [String] = []
    var onImage = true
    @Environment(\.sitePalette) private var palette

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            if !title.isEmpty {
                Text(title).font(Theme.bold(14)).foregroundColor(onImage ? palette.paper : palette.ink).lineLimit(2)
            }
            if let name = author, !name.isEmpty {
                HStack(spacing: 5) {
                    if let authorAvatarURL {
                        SiteAvatar(url: authorAvatarURL, name: name, size: 16, revision: avatarRevision).clipShape(Circle())
                    } else { Image(systemName: "person.crop.circle").font(.system(size: 11)) }
                    Text(name).lineLimit(1)
                }.font(Theme.body(12)).foregroundColor(onImage ? palette.paper.opacity(0.9) : Theme.muted)
            }
            HStack(spacing: 6) {
                if isPage { Image(systemName: "doc.text").font(.system(size: 11)).help("Page") }
                Text(date.formatted(date: .numeric, time: .omitted)).fixedSize()
                if !tags.isEmpty { Text(tags.prefix(3).joined(separator: " · ")).lineLimit(1) }
            }.font(Theme.body(12)).foregroundColor(onImage ? palette.paper.opacity(0.85) : Theme.muted)
        }
    }
}

struct PostRow: View {
    var siteID: String
    var post: Post
    var widgetRevision: Double? = nil
    var authorFallback: String? = nil

    var body: some View {
        PostMediaRow(
            imageURL: post.tileImage.map { API.shared.siteFile(siteID, "\(post.id)/\($0)") },
            revision: max(post.modified ?? post.created, widgetRevision ?? 0),
            widgetSource: WidgetPreviewSource.owned(siteID: siteID, post: post, baseURL: API.shared.base),
            fallbackSymbol: post.audioFilename == nil ? "doc.text" : "waveform",
            pinned: post.pinned != nil
        ) {
            VStack(alignment: .leading, spacing: 6) {
                if post.title.isEmpty { Text("Untitled").font(Theme.bold(14)) }
                PostCaption(post: post, authorFallback: authorFallback, onImage: false)
                if let summary = post.summary, !summary.isEmpty {
                    Text(summary).font(Theme.body(13)).foregroundColor(Theme.muted).lineLimit(2)
                }
            }
        }
    }
}

struct PostMediaRow<Details: View>: View {
    var imageURL: URL?
    var revision: Double
    var widgetSource: WidgetPreviewSource? = nil
    var fallbackSymbol = "doc.text"
    var pinned = false
    @ViewBuilder var details: Details
    @State private var hovering = false

    var body: some View {
        HStack(alignment: .center, spacing: 16) {
            Color.clear.frame(width: 80, height: 80)
                .background(Theme.rule.opacity(0.3))
                .overlay {
                    if let widgetSource {
                    WidgetPostPreview(source: widgetSource, revision: revision, compact: true)
                } else if let imageURL {
                        CachedPostImage(url: imageURL, revision: revision)
                    } else {
                        Image(systemName: fallbackSymbol)
                            .font(.system(size: 24)).foregroundColor(Theme.muted)
                    }
                }.clipped()
            details.frame(maxWidth: .infinity, alignment: .leading)
            if pinned { Image(systemName: "pin.fill").foregroundColor(Theme.muted).accessibilityLabel("Pinned") }
        }
        .padding(.vertical, 12)
        .background(hovering ? Theme.rule.opacity(0.2) : Color.clear)
        .overlay(alignment: .bottom) { Rectangle().fill(Theme.rule).frame(height: 1) }
        .contentShape(Rectangle())
        .onHover { hovering = $0 }
    }
}


/// The old Planet-based Croptop app still publishes this site. Two apps on one
/// IPNS name overwrite each other, so offer the one-way move here.
struct LegacyNotice: View {
    @EnvironmentObject var model: AppModel
    var legacy: Status.Legacy
    @State private var working = false

    private var text: String {
        var t = "The old Croptop app also publishes this site"
        if legacy.newer > 0 { t += " and has \(legacy.newer) \(legacy.newer == 1 ? "post that isn’t" : "posts that aren’t") here" }
        return t + ". Two apps on one site overwrite each other. Quit it, then import its posts and retire it there."
    }

    var body: some View {
        HStack(alignment: .center, spacing: 16) {
            Text(text).font(Theme.body(14)).fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 0)
            Button(working ? "Importing…" : "Import and retire") {
                working = true
                Task {
                    do {
                        let merged = try await API.shared.retireLegacy(legacy.id)
                        model.toast("\(legacy.name) is now published from here only (\(merged) \(merged == 1 ? "post" : "posts") imported).")
                        await model.load(); await model.loadPosts(legacy.id)
                    } catch { model.toast(error.localizedDescription, error: true) }
                    working = false
                }
            }.buttonStyle(BorderedButton(kind: .hot)).disabled(working)
        }
        .padding(14)
        .background(Theme.hotWash)
        .overlay(Rectangle().stroke(Theme.ink, lineWidth: Theme.border))
    }
}
