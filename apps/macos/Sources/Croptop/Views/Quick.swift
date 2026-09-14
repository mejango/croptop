// Quick post: media handed to the app, a site, a title, a few words, tags.
import SwiftUI
import AVKit

struct QuickView: View {
    @EnvironmentObject var model: AppModel
    var groupID: String
    var presentedAsSheet = false
    @Environment(\.dismiss) private var dismiss
    @State private var files: [String] = []
    @State private var site = ""
    @State private var title = ""
    @State private var words = ""
    @State private var tags = ""
    @State private var busy = false
    @State private var gone = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 24) {
                ScreenHead(title: "New post", subtitle: "Choose a site, then add a title or a few words.") { EmptyView() }
                if gone {
                    EmptyState(text: "These files are gone. Drop them on Croptop again.")
                } else {
                    VStack(alignment: .leading, spacing: 8) {
                        ForEach(files, id: \.self) { name in MediaPreview(url: API.shared.quickFile(groupID, name), name: name) }
                    }
                    Labeled(title: "Post to") {
                        Picker("", selection: $site) {
                            ForEach(model.sites) { s in Text(s.name).tag(s.id) }
                        }
                        .font(Theme.body(14)).labelsHidden().fixedSize()
                    }
                    Labeled(title: "Title") { TextField("Title (optional)", text: $title).field() }
                    Labeled(title: "Body") {
                        TextEditor(text: $words).font(Theme.body(14)).frame(minHeight: 90).padding(8).fieldOutline()
                    }
                    VStack(alignment: .leading, spacing: 8) {
                        Text("Tags").font(Theme.formLabel)
                        TagSelector(tags: $tags, choices: model.tagChoices(for: site))
                    }
                    HStack(spacing: 16) {
                        HStack(spacing: 8) {
                            IconActionButton("Discard", systemImage: "xmark") {
                                Task { await API.shared.discardQuick(groupID); if presentedAsSheet { dismiss() } else { model.screen = .feed } }
                            }
                            IconActionButton(busy ? "Posting…" : "Post", systemImage: "checkmark") { submit(publish: false) }
                                .disabled(busy).keyboardShortcut(.return, modifiers: .command)
                        }
                        Button("Post and publish") { submit(publish: true) }.buttonStyle(BorderedButton()).disabled(busy).keyboardShortcut(.return, modifiers: [.command, .shift])
                    }.frame(maxWidth: .infinity, alignment: .trailing)
                }
            }
            .padding(Theme.content)
            .frame(maxWidth: 720, alignment: .leading)
        }
        .task(id: site) { if !site.isEmpty && model.posts[site] == nil { await model.loadPosts(site) } }
        .task {
            if let g = try? await API.shared.quick(groupID) { files = g.files } else { gone = true }
            site = UserDefaults.standard.string(forKey: "quickSite").flatMap { id in model.sites.first { $0.id == id }?.id } ?? model.sites.first?.id ?? ""
        }
    }

    static func kind(_ name: String) -> String {
        switch (name as NSString).pathExtension.lowercased() {
        case "png", "jpg", "jpeg", "gif", "webp", "avif", "heic": return "image"
        case "mp4", "mov", "webm", "m4v": return "video"
        case "mp3", "m4a", "wav", "ogg", "aac", "flac": return "audio"
        default: return "file"
        }
    }

    func submit(publish: Bool) {
        guard !site.isEmpty else { model.toast("Pick a site first.", error: true); return }
        busy = true
        UserDefaults.standard.set(site, forKey: "quickSite")
        Task {
            do {
                let f = Multipart()
                var inline: [String] = []
                var hero = ""
                for name in files {
                    let (data, _) = try await URLSession.shared.data(from: API.shared.quickFile(groupID, name))
                    f.file("attachments", filename: name, data: data)
                    let esc = name.replacingOccurrences(of: "\"", with: "&quot;")
                    switch Self.kind(name) {
                    case "image": inline.append("<img alt=\"\(esc)\" src=\"\(esc)\">"); if hero.isEmpty { hero = name }
                    case "video": inline.append("<video controls playsinline src=\"\(esc)\"></video>")
                    case "audio": inline.append("<audio controls src=\"\(esc)\"></audio>")
                    default: inline.append("<a href=\"\(esc)\">\(name)</a>")
                    }
                }
                f.field("title", title); f.field("content", inline.joined(separator: "\n") + "\n\n" + words); f.field("tags", tags)
                if !hero.isEmpty { f.field("heroImage", hero) }
                f.close()
                let post = try await API.shared.savePost(site: site, id: nil, form: f)
                await API.shared.discardQuick(groupID)
                await model.loadPosts(site)
                if presentedAsSheet { dismiss() } else { model.screen = .editor(site: site, post: post.id) }
                if publish { model.publish(site) } else { model.toast("Posted. Publish the site when you're ready.") }
            } catch { model.show(error) }
            busy = false
        }
    }
}

struct MediaPreview: View {
    var url: URL
    var name: String
    var body: some View {
        switch QuickView.kind(name) {
        case "image":
            AsyncImage(url: url) { i in i.resizable().scaledToFit() } placeholder: { Rectangle().fill(Theme.rule).frame(height: 160) }
                .frame(maxHeight: 360).overlay(Rectangle().stroke(Theme.ink, lineWidth: Theme.border))
        case "video":
            VideoPlayer(player: AVPlayer(url: url)).frame(height: 320).overlay(Rectangle().stroke(Theme.ink, lineWidth: Theme.border))
        case "audio":
            VideoPlayer(player: AVPlayer(url: url)).frame(height: 60).overlay(Rectangle().stroke(Theme.ink, lineWidth: Theme.border))
        default:
            Text(name).font(Theme.body(14)).padding(8).overlay(Rectangle().strokeBorder(style: StrokeStyle(lineWidth: Theme.border, dash: [6, 4])).foregroundColor(Theme.ink))
        }
    }
}
