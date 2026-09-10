// The rail: brand, Feed, my sites, who I follow, actions, node line, update banner.
import SwiftUI
import AppKit

struct RailView: View {
    @EnvironmentObject var model: AppModel

    var body: some View {
        VStack(alignment: .leading, spacing: 22) {
            Text("Croptop").font(Theme.pixel(22)).padding(.leading, 10)

            Button { model.screen = .feed } label: {
                Text("Feed").font(Theme.bold(16)).frame(maxWidth: .infinity)
            }
            .buttonStyle(BorderedButton(current: isFeed))

            ScrollView(.vertical, showsIndicators: false) {
                VStack(alignment: .leading, spacing: 2) {
                    ForEach(model.sites) { s in
                        RailItem(title: s.name, second: s.secondLine, current: model.currentSiteID == s.id) { model.screen = .site(s.id) }
                    }
                    if !model.following.isEmpty {
                        Text("Following").font(Theme.pixel(12)).foregroundColor(Theme.muted).padding(.top, 14).padding(.horizontal, 10).padding(.bottom, 4)
                        ForEach(model.following) { f in
                            RailItem(title: f.label, second: f.secondLine, current: false) {
                                if let u = URL(string: API.shared.url("/f/\(f.ipns)/").absoluteString) { NSWorkspace.shared.open(u) }
                            }
                            .contextMenu {
                                Button("Check for new posts") { Task { try? await API.shared.refreshFollowing(f.ipns); await model.load() } }
                                Button("Unfollow") { Task { try? await API.shared.unfollow(f.ipns); await model.load() } }
                            }
                        }
                    }
                }
            }

            VStack(alignment: .leading, spacing: 6) {
                Button { model.sheet = .newSite } label: { Text("New site").frame(maxWidth: .infinity) }.buttonStyle(BorderedButton())
                Button { model.sheet = .follow } label: { Text("Follow").frame(maxWidth: .infinity) }.buttonStyle(BorderedButton())
                if let st = model.status, st.update, let latest = st.latest {
                    UpdateBanner(version: latest)
                }
                NodeLine(status: model.status)
            }
        }
        .padding(.vertical, 20).padding(.horizontal, 16)
        .frame(width: Theme.rail)
        .background(Theme.paper)
    }

    var isFeed: Bool { if case .feed = model.screen { return true }; return false }
}

struct RailItem: View {
    var title: String
    var second: String
    var current: Bool
    var action: () -> Void
    var body: some View {
        Button(action: action) {
            HStack(spacing: 0) {
                Rectangle().fill(current ? Theme.hot : .clear).frame(width: 4)
                VStack(alignment: .leading, spacing: 2) {
                    Text(title).font(Theme.body()).lineLimit(1)
                    Text(second).font(Theme.body(13)).foregroundColor(Theme.muted).lineLimit(1)
                }
                .padding(.vertical, 8).padding(.horizontal, 10)
                Spacer(minLength: 0)
            }
            .background(current ? Theme.hotWash : Theme.paper)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }
}

struct NodeLine: View {
    var status: Status?
    var body: some View {
        HStack(spacing: 6) {
            Circle().fill(status?.ipfs.running == true ? Theme.live : Theme.muted).frame(width: 8, height: 8)
            Text(status.map { "\($0.ipfs.peers) peers" } ?? "starting").font(Theme.body(13)).foregroundColor(Theme.muted)
            Spacer()
            Text(status?.version ?? "").font(Theme.body(13)).foregroundColor(Theme.muted)
        }
        .padding(.horizontal, 4)
    }
}

struct UpdateBanner: View {
    @EnvironmentObject var model: AppModel
    var version: String
    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Croptop \(version) is out.").font(Theme.body(14))
            Button("Get the update") { model.update() }
                .buttonStyle(BorderedButton())
        }
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Theme.attention)
    }
}
