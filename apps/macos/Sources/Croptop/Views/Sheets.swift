// New site and Follow: the two small forms that come up as sheets.
import SwiftUI

struct NewSiteSheet: View {
    @EnvironmentObject var model: AppModel
    @State private var name = ""
    @State private var about = ""
    @State private var busy = false

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("New site").font(Theme.pixel(28))
            Text("A new IPNS key is generated on this machine. Export it from the site's settings before you switch computers.").font(Theme.body(14)).foregroundColor(Theme.muted)
            Labeled(title: "Name") { TextField("Name", text: $name).field() }
            Labeled(title: "About") { TextField("A line about it (optional)", text: $about).field() }
            HStack(spacing: 8) {
                Spacer()
                Button("Cancel") { model.sheet = nil }.buttonStyle(BorderedButton(kind: .quiet)).keyboardShortcut(.cancelAction)
                Button(busy ? "Creating…" : "Create") {
                    busy = true
                    Task {
                        do {
                            let s = try await API.shared.createSite(name: name, about: about)
                            await model.load()
                            model.sheet = nil
                            model.screen = .site(s.id)
                        } catch { model.show(error) }
                        busy = false
                    }
                }.buttonStyle(BorderedButton(kind: .hot)).disabled(name.trimmingCharacters(in: .whitespaces).isEmpty || busy).keyboardShortcut(.defaultAction)
            }
        }
        .padding(Theme.content)
        .frame(width: 520)
        .background(Theme.paper)
    }
}

struct FollowSheet: View {
    @EnvironmentObject var model: AppModel
    @State private var name = ""
    @State private var busy = false

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Follow a site").font(Theme.pixel(28))
            Labeled(title: "IPNS name or ENS domain") { TextField("k51… or theirsite.eth", text: $name).field() }
            HStack(spacing: 8) {
                Spacer()
                Button("Cancel") { model.sheet = nil }.buttonStyle(BorderedButton(kind: .quiet)).keyboardShortcut(.cancelAction)
                Button(busy ? "Following…" : "Follow") {
                    busy = true
                    Task {
                        do {
                            try await API.shared.follow(name.trimmingCharacters(in: .whitespaces))
                            await model.load()
                            model.sheet = nil
                            model.screen = .feed
                            await model.refreshFeed()
                        } catch { model.show(error) }
                        busy = false
                    }
                }.buttonStyle(BorderedButton(kind: .hot)).disabled(name.trimmingCharacters(in: .whitespaces).isEmpty || busy).keyboardShortcut(.defaultAction)
            }
        }
        .padding(Theme.content)
        .frame(width: 520)
        .background(Theme.paper)
    }
}
