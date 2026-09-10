// The post editor: title, markdown, attachments, tags, hero, navigation, pin.
import SwiftUI
import AppKit
import UniformTypeIdentifiers

struct EditorView: View {
    @EnvironmentObject var model: AppModel
    var siteID: String
    var postID: String?

    @State private var title = ""
    @State private var content = ""
    @State private var tags = ""
    @State private var hero = ""
    @State private var inNav = false
    @State private var pinned = false
    @State private var existing: [String] = []       // attachments already on the post
    @State private var added: [URL] = []             // files to upload on save
    @State private var removed: Set<String> = []
    @State private var saving = false
    @State private var loaded = false
    @State private var dropping = false

    var isNew: Bool { postID == nil }
    var images: [String] { (existing.filter { !removed.contains($0) } + added.map { $0.lastPathComponent }).filter { ["png", "jpg", "jpeg", "gif", "webp"].contains(($0 as NSString).pathExtension.lowercased()) } }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 12) {
                ScreenHead(title: isNew ? "New post" : "Edit post", subtitle: model.sites.first { $0.id == siteID }?.name) {
                    HStack(spacing: 8) {
                        Button("Cancel") { model.screen = .site(siteID) }.buttonStyle(BorderedButton(kind: .quiet))
                        if !isNew {
                            Button("Delete") {
                                Task { try? await API.shared.deletePost(site: siteID, id: postID!); model.screen = .site(siteID); model.toast("Deleted.") }
                            }.buttonStyle(BorderedButton(kind: .quiet))
                        }
                        Button(saving ? "Saving…" : "Save") { save(publish: false) }.buttonStyle(BorderedButton()).disabled(saving).keyboardShortcut("s")
                        Button("Save and publish") { save(publish: true) }.buttonStyle(BorderedButton(kind: .hot)).disabled(saving).keyboardShortcut("s", modifiers: [.command, .shift])
                    }
                }
                Labeled(title: "Title") { TextField("Title (optional)", text: $title).field() }
                Labeled(title: "Content", help: "Markdown. Attachments are referenced by file name.") {
                    TextEditor(text: $content).font(Theme.code).frame(minHeight: 260).padding(6).overlay(Rectangle().stroke(Theme.ink, lineWidth: Theme.border))
                }
                Labeled(title: "Attachments", help: "Drop files here, or add them.") {
                    VStack(alignment: .leading, spacing: 8) {
                        if existing.filter({ !removed.contains($0) }).isEmpty && added.isEmpty {
                            Text("None yet.").font(Theme.body(13)).foregroundColor(Theme.muted)
                        }
                        ForEach(existing.filter { !removed.contains($0) }, id: \.self) { name in
                            AttachmentRow(name: name, url: API.shared.siteFile(siteID, "\(postID ?? "")/\(name)"), isHero: hero == name, canHero: images.contains(name)) { hero = name } remove: { removed.insert(name); if hero == name { hero = "" } }
                        }
                        ForEach(added, id: \.self) { u in
                            AttachmentRow(name: u.lastPathComponent, url: u, isHero: hero == u.lastPathComponent, canHero: images.contains(u.lastPathComponent)) { hero = u.lastPathComponent } remove: { added.removeAll { $0 == u }; if hero == u.lastPathComponent { hero = "" } }
                        }
                        Button("Add files…") {
                            let p = NSOpenPanel(); p.allowsMultipleSelection = true
                            if p.runModal() == .OK { added += p.urls }
                        }.buttonStyle(BorderedButton(kind: .quiet))
                    }
                    .padding(12)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .overlay(Rectangle().strokeBorder(style: StrokeStyle(lineWidth: Theme.border, dash: dropping ? [] : [6, 4])).foregroundColor(dropping ? Theme.hot : Theme.rule))
                    .onDrop(of: [UTType.fileURL], isTargeted: $dropping) { providers in
                        Task {
                            for p in providers {
                                if let d = try? await p.loadItem(forTypeIdentifier: UTType.fileURL.identifier) as? Data, let u = URL(dataRepresentation: d, relativeTo: nil) { added.append(u) }
                            }
                        }
                        return true
                    }
                }
                Labeled(title: "Tags", help: "Comma separated.") { TextField("tags", text: $tags).field() }
                HStack(spacing: 22) {
                    Toggle("Include in navigation", isOn: $inNav)
                    Toggle("Pin to the top", isOn: $pinned)
                }.font(Theme.body(14)).toggleStyle(.checkbox)
            }
            .padding(Theme.content)
            .frame(maxWidth: 820, alignment: .leading)
        }
        .task { await loadPost() }
    }

    func loadPost() async {
        guard let id = postID, !loaded else { loaded = true; return }
        loaded = true
        guard let p = try? await API.shared.post(site: siteID, id: id) else { return }
        title = p.title; content = p.content; tags = p.tagList.joined(separator: ", ")
        hero = p.heroImage ?? ""; inNav = p.isIncludedInNavigation ?? false; pinned = p.pinned != nil
        existing = p.attachments
    }

    func save(publish: Bool) {
        saving = true
        Task {
            do {
                let f = Multipart()
                f.field("title", title); f.field("content", content); f.field("tags", tags)
                f.field("includeInNavigation", inNav ? "true" : "false"); f.field("pinned", pinned ? "true" : "false")
                f.field("heroImage", hero)
                for u in added { f.file("attachments", u) }
                f.close()
                for name in removed { if let id = postID { try? await API.shared.deleteAttachment(site: siteID, post: id, name: name) } }
                _ = try await API.shared.savePost(site: siteID, id: postID, form: f)
                await model.loadPosts(siteID)
                model.screen = .site(siteID)
                if publish { model.publish(siteID) } else { model.toast("Saved. Publish the site when you're ready.") }
            } catch { model.show(error) }
            saving = false
        }
    }
}

struct AttachmentRow: View {
    var name: String
    var url: URL
    var isHero: Bool
    var canHero: Bool
    var makeHero: () -> Void
    var remove: () -> Void
    var body: some View {
        HStack(spacing: 10) {
            if canHero {
                AsyncImage(url: url) { i in i.resizable().scaledToFill() } placeholder: { Rectangle().fill(Theme.rule) }
                    .frame(width: 44, height: 44).clipped().overlay(Rectangle().stroke(Theme.ink, lineWidth: 1))
            } else {
                Rectangle().fill(Theme.rule).frame(width: 44, height: 44).overlay(Text((name as NSString).pathExtension.uppercased()).font(Theme.pixel(11)))
            }
            Text(name).font(Theme.body(14)).lineLimit(1)
            Spacer()
            if canHero {
                Button(isHero ? "Hero" : "Set as hero", action: makeHero).buttonStyle(BorderedButton(kind: isHero ? .hot : .quiet))
            }
            Button("Remove", action: remove).buttonStyle(BorderedButton(kind: .quiet))
        }
    }
}
