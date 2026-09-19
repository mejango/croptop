// The rail: brand, actions and status, then independently scrolling site lists.
import SwiftUI
import AppKit

struct RailView: View {
    @EnvironmentObject var model: AppModel
    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            VStack(alignment: .leading, spacing: 22) {
                RailHeader()
                    .padding(.horizontal, 16)

                GeometryReader { geometry in
                    let availableHeight = max(0, geometry.size.height - 16)
                    let followingLimit = model.sites.isEmpty ? max(0, availableHeight - 110) : availableHeight * 0.42
                    let followingHeight = min(CGFloat(max(model.following.count, 1)) * 56 + 54, followingLimit)
                    VStack(alignment: .leading, spacing: 16) {
                        VStack(alignment: .leading, spacing: 0) {
                            sectionHeading("Following", allScreen: .feed, createSheet: .follow)
                            ScrollView(.vertical, showsIndicators: false) {
                                VStack(alignment: .leading, spacing: 0) {
                                    ForEach(model.following) { f in
                                        RailItem(title: f.label, second: f.secondLine, current: model.screen == .followingSite(f.ipns), logoURL: API.shared.url("/f/\(f.ipns)/avatar.png"), logoRevision: f.cid) {
                                            model.screen = .followingSite(f.ipns)
                                        }
                                        .contextMenu {
                                            Button("Refresh") { Task { try? await API.shared.refreshFollowing(f.ipns); await model.load() } }
                                                .disabled(model.unfollowing.contains(f.ipns))
                                            Button("Unfollow") {
                                                Task { await model.unfollow(f.ipns) }
                                            }.disabled(model.unfollowing.contains(f.ipns))
                                        }
                                    }
                                    if model.following.isEmpty { emptySection("Nothing yet.") }
                                }.frame(maxWidth: .infinity, alignment: .leading)
                            }
                        }.frame(height: followingHeight)

                        VStack(alignment: .leading, spacing: 0) {
                            sectionHeading("Your sites", allScreen: .ownedFeed, createSheet: .newSite)
                            if model.sites.isEmpty {
                                emptySection("Nothing yet.")
                                Spacer(minLength: 0)
                            } else {
                                ReorderableSiteList(model: model)
                            }
                        }.frame(height: max(0, availableHeight - followingHeight))
                    }
                }
            }
            RailFooter(status: model.status, updater: model.updater, appVersion: model.appVersion ?? "dev", engineVersion: model.status?.version ?? "unknown")
                .padding(.horizontal, 16)
        }
        .padding(.top, 4).padding(.bottom, 8)
        .frame(width: Theme.rail)
        .background(Theme.paper)
    }

    private func sectionHeading(_ title: String, allScreen: Screen, createSheet: Sheet) -> some View {
        HStack(spacing: 8) {
            Button { model.screen = allScreen } label: {
                Text(title).font(Theme.bold(18)).foregroundColor(Theme.ink)
                    .padding(.vertical, 10).padding(.leading, 16)
                    .contentShape(Rectangle())
            }.buttonStyle(.plain)
                .accessibilityLabel(title == "Following" ? "Following, all posts" : "Your sites, all posts")
                .accessibilityAddTraits(.isHeader)
                .accessibilityAddTraits(model.screen == allScreen ? .isSelected : [])
            Spacer(minLength: 8)
            Button { model.sheet = createSheet } label: {
                Image(systemName: "plus").font(Theme.body(16))
                    .frame(width: 24, height: 24).contentShape(Rectangle())
            }.buttonStyle(.plain)
                .accessibilityLabel(title == "Following" ? "Follow a site" : "Create a site or curation")
                .help(title == "Following" ? "Follow a site" : "New site")
        }.padding(.trailing, 16).padding(.bottom, 12)
    }

    private func emptySection(_ text: String) -> some View {
        Text(text).font(Theme.body(13)).foregroundColor(Theme.muted).padding(.leading, 16).padding(.top, 12)
    }

}

struct RailItem: View {
    var title: String
    var second: String
    var current: Bool
    var logoURL: URL? = nil
    var logoRevision: String? = nil
    var collaborative: Bool = false
    var action: () -> Void
    @FocusState private var focused: Bool
    var body: some View {
        Button(action: action) {
            RailItemContent(title: title, second: second, current: current, logoURL: logoURL, logoRevision: logoRevision, collaborative: collaborative)
            .overlay(alignment: .bottom) {
                Rectangle().fill(focused ? Theme.hot.opacity(0.35) : .clear).frame(height: 1)
                    .padding(.leading, logoURL == nil ? 0 : 56)
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .focused($focused)
        .accessibilityAddTraits(current ? .isSelected : [])
    }
}

struct RailItemContent: View {
    var title: String
    var second: String
    var current: Bool
    var logoURL: URL? = nil
    var logoRevision: String? = nil
    var collaborative: Bool = false
    var publishing: Bool = false

    var body: some View {
        HStack(spacing: 0) {
            if let logoURL { SiteAvatar(url: logoURL, name: title, revision: logoRevision).frame(width: 56, height: 56) }
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 5) {
                    Text(title).font(Theme.body()).foregroundColor(Theme.ink).lineLimit(1)
                    if collaborative { Image(systemName: "person.2").font(.system(size: 10)).foregroundColor(Theme.muted).help("Shared site") }
                }
                if publishing {
                    HStack(spacing: 5) {
                        ProgressView().controlSize(.mini).accessibilityHidden(true)
                        Text("Publishing…").font(Theme.body(13)).foregroundColor(Theme.hot).lineLimit(1)
                    }
                    .accessibilityElement(children: .ignore)
                    .accessibilityLabel("Publishing \(title)")
                } else if !second.isEmpty {
                    Text(second).font(Theme.body(13)).foregroundColor(Theme.muted).lineLimit(1)
                }
            }
            .padding(.vertical, 8).padding(.leading, logoURL == nil ? 16 : 12).padding(.trailing, 30)
            Spacer(minLength: 0)
        }
        .frame(minHeight: logoURL == nil ? 0 : 56)
        .background(current ? Theme.hotWash.opacity(0.65) : Theme.paper)
    }
}


private struct RailHeader: View {
    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            if let url = Bundle.module.url(forResource: "Scissors", withExtension: "png"), let image = NSImage(contentsOf: url) {
                Image(nsImage: image).resizable().interpolation(.high)
                    // Keep the supplied PNG intact while hiding its empty canvas.
                    .frame(width: 94, height: 94).offset(x: -6.6)
                    .frame(width: 84, height: 48, alignment: .topLeading).clipped()
                    .frame(height: 56, alignment: .center)
                    .accessibilityLabel("Croptop")
            }
            Spacer(minLength: 0)
        }
    }
}

private struct RailFooter: View {
    var status: Status?
    @ObservedObject var updater: AppUpdater
    var appVersion: String
    var engineVersion: String
    private var appBuild: String { Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String ?? "dev" }

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            NodeLine(status: status).fixedSize()
            Text("Version \(appVersion) (build \(appBuild))")
                .font(Theme.body(11)).foregroundColor(Theme.muted)
                .help("App \(appVersion) (build \(appBuild))\nEngine \(engineVersion)")
            UpdateIndicator(updater: updater)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
    }
}

struct NodeLine: View {
    var status: Status?
    var body: some View {
        HStack(spacing: 5) {
            Circle().fill(status?.ipfs.running == true ? Theme.live : Theme.muted).frame(width: 7, height: 7)
            Text(status.map { "\($0.ipfs.peers) peers" } ?? "starting").font(Theme.body(13)).foregroundColor(Theme.muted)
        }
    }
}
