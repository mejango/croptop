// The post editor: title, markdown, attachments, tags, hero, navigation, pin.
import SwiftUI
import AppKit
import UniformTypeIdentifiers

struct EditorView: View {
    @EnvironmentObject var model: AppModel
    @State var siteID: String
    var postID: String?
    init(siteID: String, postID: String?) { _siteID = State(initialValue: siteID); self.postID = postID }

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
    @State private var deleting = false
    @State private var loaded = false
    @State private var dropping = false
    @State private var editorMode = "Split"
    @State private var largePreview = false

    var isNew: Bool { postID == nil }
    var images: [String] { (existing.filter { !removed.contains($0) } + added.map { $0.lastPathComponent }).filter { ["png", "jpg", "jpeg", "gif", "webp"].contains(($0 as NSString).pathExtension.lowercased()) } }

    var previewAttachments: [PreviewAttachment] {
        existing.filter { !removed.contains($0) }.map { PreviewAttachment(name: $0, url: API.shared.siteFile(siteID, "\(postID ?? "")/\($0)")) }
            + added.map { PreviewAttachment(name: $0.lastPathComponent, url: $0) }
    }

    private var writingArea: some View {
        TextEditor(text: $content).font(Theme.code).padding(8)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .fieldOutline()
    }
    private var previewArea: some View {
        PostContentPreview(title: title, content: content, attachments: previewAttachments)
            .overlay(Rectangle().strokeBorder(Theme.rule, lineWidth: 1))
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 24) {
                ScreenHead(title: isNew ? "New post" : "Edit post", subtitle: isNew ? nil : model.sites.first { $0.id == siteID }?.name) {
                    HStack(spacing: 16) {
                        HStack(spacing: 8) {
                            IconActionButton("Cancel", systemImage: "xmark") { model.screen = .site(siteID) }
                            IconActionButton(saving ? "Saving…" : "Save", systemImage: "checkmark") { save(publish: false) }
                                .disabled(saving || deleting).keyboardShortcut("s")
                        }
                        Button("Save & publish") { save(publish: true) }.buttonStyle(BorderedButton(kind: .hot)).disabled(saving || deleting).keyboardShortcut("s", modifiers: [.command, .shift])
                    }
                }
                VStack(alignment: .leading, spacing: 8) {
                    if isNew && model.sites.count > 1 {
                        Labeled(title: "Post to") {
                            Picker("", selection: $siteID) { ForEach(model.sites) { s in Text(s.name).tag(s.id) } }
                                .font(Theme.body(14)).labelsHidden().fixedSize()
                        }
                    }
                    Labeled(title: "Title") { TextField("Title (optional)", text: $title).field() }
                    HStack(spacing: 22) {
                        Toggle("Include in navigation", isOn: $inNav)
                        Toggle("Pin to the top", isOn: $pinned)
                    }.font(Theme.formLabel).toggleStyle(.checkbox)
                }
                VStack(alignment: .leading, spacing: 8) {
                    HStack(spacing: 8) {
                        ForEach(["Write", "Split", "Preview"], id: \.self) { mode in
                            Button(mode) { editorMode = mode }.buttonStyle(BorderedButton(current: editorMode == mode))
                        }
                        Spacer()
                        Button { largePreview = true } label: {
                            Label("Expand preview", systemImage: "arrow.up.left.and.arrow.down.right")
                        }.buttonStyle(TextActionButtonStyle())
                    }
                    VStack(alignment: .leading, spacing: 6) {
                        Group {
                            if editorMode == "Write" { writingArea }
                            else if editorMode == "Preview" { previewArea }
                            else {
                                HSplitView {
                                    writingArea.frame(minWidth: 240)
                                    previewArea.frame(minWidth: 240)
                                }
                            }
                        }.frame(height: 460)
                        Text("Markdown and HTML. Use an attachment’s file name to include it.").font(Theme.body(13)).foregroundColor(Theme.muted)
                    }
                }
                VStack(alignment: .leading, spacing: 8) {
                    Text("Attachments").font(Theme.formLabel)
                    VStack(alignment: .leading, spacing: 6) {
                        VStack(alignment: .leading, spacing: 8) {
                            VStack(alignment: .leading, spacing: 16) {
                                if existing.filter({ !removed.contains($0) }).isEmpty && added.isEmpty {
                                    Text("None yet.").font(Theme.body(13)).foregroundColor(Theme.muted)
                                }
                                ForEach(existing.filter { !removed.contains($0) }, id: \.self) { name in
                                    AttachmentRow(name: name, url: API.shared.siteFile(siteID, "\(postID ?? "")/\(name)"), isHero: hero == name, canHero: images.contains(name)) { hero = name } remove: { removed.insert(name); if hero == name { hero = "" } }
                                }
                                ForEach(added, id: \.self) { u in
                                    AttachmentRow(name: u.lastPathComponent, url: u, isHero: hero == u.lastPathComponent, canHero: images.contains(u.lastPathComponent)) { hero = u.lastPathComponent } remove: { added.removeAll { $0 == u }; if hero == u.lastPathComponent { hero = "" } }
                                }
                            }
                            Button("Add files…") {
                                let p = NSOpenPanel(); p.allowsMultipleSelection = true
                                if p.runModal() == .OK { added += p.urls }
                            }.buttonStyle(BorderedButton(kind: .quiet))
                        }
                        .padding(16)
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
                        Text("Drop files here, or add them.").font(Theme.body(13)).foregroundColor(Theme.muted)
                    }
                }
                VStack(alignment: .leading, spacing: 8) {
                    Text("Tags").font(Theme.formLabel)
                    TagSelector(tags: $tags, choices: model.tagChoices(for: siteID))
                }
                if !isNew {
                    VStack(alignment: .leading, spacing: 16) {
                        Text("Danger zone").font(Theme.formLabel).foregroundColor(.red)
                        Text("Remove this post from this site.").font(Theme.formHelp).foregroundColor(Theme.muted)
                        Button(action: deletePost) {
                            Text(deleting ? "Deleting…" : "Delete post").foregroundColor(.red)
                        }.buttonStyle(BorderedButton(kind: .quiet)).disabled(saving || deleting)
                    }.padding(16).frame(maxWidth: 440, alignment: .leading)
                        .overlay(Rectangle().strokeBorder(Color.red, lineWidth: 1))
                }
            }
            .padding(Theme.content)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .sheet(isPresented: $largePreview) { LargePostPreview(title: title, content: content, attachments: previewAttachments) }
        .task { await loadPost(); if model.posts[siteID] == nil { await model.loadPosts(siteID) } }
        .task(id: siteID) { if model.posts[siteID] == nil { await model.loadPosts(siteID) } }
        .task(id: model.droppedFiles) { takeDroppedFiles() }
    }

    // Dropped media is attached and inlined at the top of the post; the first image is the hero.
    func takeDroppedFiles() {
        let files = model.droppedFiles
        guard isNew, !files.isEmpty else { return }
        model.droppedFiles = []
        added += files
        var inline: [String] = []
        for u in files {
            let name = u.lastPathComponent
            let esc = name.replacingOccurrences(of: "\"", with: "&quot;")
            switch QuickView.kind(name) {
            case "image": inline.append("<img alt=\"\(esc)\" src=\"\(esc)\">"); if hero.isEmpty { hero = name }
            case "video": inline.append("<video controls playsinline src=\"\(esc)\"></video>")
            case "audio": inline.append("<audio controls src=\"\(esc)\"></audio>")
            default: inline.append("<a href=\"\(esc)\">\(name)</a>")
            }
        }
        content = inline.joined(separator: "\n") + "\n\n" + content
    }

    func loadPost() async {
        guard let id = postID, !loaded else { loaded = true; return }
        loaded = true
        guard let p = try? await API.shared.post(site: siteID, id: id) else { return }
        title = p.title; content = p.content; tags = p.tagList.joined(separator: ", ")
        hero = p.heroImage ?? ""; inNav = p.isIncludedInNavigation ?? false; pinned = p.pinned != nil
        existing = p.attachments
    }

    private func deletePost() {
        guard let id = postID, !saving, !deleting else { return }
        deleting = true
        Task {
            defer { deleting = false }
            do {
                try await API.shared.deletePost(site: siteID, id: id)
                await model.loadPosts(siteID)
                if model.screen == .editor(site: siteID, post: id) { model.screen = .site(siteID) }
                model.toast("Deleted.")
            } catch { model.show(error) }
        }
    }

    func save(publish: Bool) {
        guard !saving, !deleting else { return }
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
                UserDefaults.standard.set(siteID, forKey: "quickSite")
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
    @State private var showingPreview = false
    var body: some View {
        HStack(spacing: 16) {
            if canHero {
                Button { showingPreview = true } label: {
                    AttachmentImage(url: url)
                        .frame(width: 160, height: 120)
                        .background(Theme.paper)
                        .overlay(Rectangle().stroke(Theme.rule, lineWidth: Theme.border))
                }
                .buttonStyle(.plain)
                .help("View full-size image")
                .accessibilityLabel("Preview " + name)
            } else {
                Rectangle().fill(Theme.rule).frame(width: 44, height: 44).overlay(Text((name as NSString).pathExtension.uppercased()).font(Theme.body(11)))
            }
            VStack(alignment: .leading, spacing: 8) {
                Text(name).font(Theme.body(14)).lineLimit(2)
                ChipFlowLayout {
                    if canHero {
                        Button("Preview") { showingPreview = true }.buttonStyle(BorderedButton(kind: .quiet))
                        Button(isHero ? "Hero" : "Set as hero", action: makeHero).buttonStyle(BorderedButton(kind: isHero ? .hot : .quiet))
                    }
                    Button("Remove", action: remove).buttonStyle(BorderedButton(kind: .quiet))
                }
            }
            Spacer(minLength: 0)
        }
        .sheet(isPresented: $showingPreview) { AttachmentPreview(attachment: PreviewAttachment(name: name, url: url)) }
    }
}
