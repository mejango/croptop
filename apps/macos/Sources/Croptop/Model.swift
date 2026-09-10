// One observable model for the window: what is loaded, what is shown, what just happened.
import SwiftUI
import AppKit

enum Screen: Hashable {
    case feed
    case site(String)
    case editor(site: String, post: String?)
    case quick(String)
}

enum Sheet: Identifiable {
    case newSite, follow
    var id: Int { self == .newSite ? 0 : 1 }
}

struct Toast: Equatable { var text: String; var error = false }

@MainActor
final class AppModel: ObservableObject {
    @Published var ready = false
    @Published var fatal: String?
    @Published var sites: [Site] = []
    @Published var following: [Following] = []
    @Published var status: Status?
    @Published var feed: [FeedItem] = []
    @Published var posts: [String: [Post]] = [:]     // by site id
    @Published var screen: Screen = .feed
    @Published var sheet: Sheet?
    @Published var toast: Toast?
    @Published var publishing: Set<String> = []
    @Published var updating = false

    private let api = API.shared
    private var toastTask: Task<Void, Never>?
    private var statusTask: Task<Void, Never>?

    var currentSite: Site? {
        switch screen {
        case .site(let id), .editor(let id, _): return sites.first { $0.id == id }
        default: return nil
        }
    }
    var currentSiteID: String? {
        if case .site(let id) = screen { return id }
        if case .editor(let id, _) = screen { return id }
        return nil
    }

    func load() async {
        async let s = api.sites()
        async let f = api.following()
        async let st = api.status()
        async let fe = api.feed()
        do {
            sites = try await s.filter { $0.archived != true }
            following = try await f
            status = try await st
            feed = try await fe
        } catch { show(error) }
        if statusTask == nil {
            statusTask = Task { [weak self] in
                while !Task.isCancelled {
                    try? await Task.sleep(nanoseconds: 30_000_000_000)
                    if let st = try? await API.shared.status() { self?.status = st }
                }
            }
        }
    }

    func loadPosts(_ site: String) async {
        do { posts[site] = try await api.posts(site: site).sorted { ($0.pinned ?? 0, $0.created) > ($1.pinned ?? 0, $1.created) } }
        catch { show(error) }
    }

    func refreshFeed() async {
        for f in following { try? await api.refreshFollowing(f.ipns) }
        do { feed = try await api.feed() } catch { show(error) }
        toast(feed.isEmpty ? "Nothing new." : "Feed refreshed.")
    }

    func newPost() {
        if let id = currentSiteID { screen = .editor(site: id, post: nil) }
        else if let s = sites.first { screen = .editor(site: s.id, post: nil) }
        else { sheet = .newSite }
    }

    func publish(_ site: String) {
        guard !publishing.contains(site) else { return }
        publishing.insert(site)
        Task {
            defer { publishing.remove(site) }
            do {
                let r = try await api.publish(site: site)
                let seq = r["sequence"].map { "\($0)" } ?? ""
                toast("Published" + (seq.isEmpty ? "." : " at sequence \(seq)."))
                await load()
            } catch { show(error) }
        }
    }
    func publishCurrent() { if let id = currentSiteID { publish(id) } }

    func openCurrentInBrowser() {
        guard let id = currentSiteID else { return }
        Task { if let u = try? await api.siteURL(id), let url = URL(string: u) { NSWorkspace.shared.open(url) } }
    }

    // Files from the Dock, Finder, a drop, or File > Post Files become one quick post.
    func postFiles(_ urls: [URL]) {
        let media = urls.filter { !$0.hasDirectoryPath }
        guard !media.isEmpty else { return }
        Task {
            do { screen = .quick(try await api.uploadQuick(media)) } catch { show(error) }
        }
    }

    func update() {
        updating = true
        Task { try? await api.update(); toast("Updating and restarting…") }
    }

    func toast(_ text: String, error: Bool = false) {
        toast = Toast(text: text, error: error)
        toastTask?.cancel()
        toastTask = Task { try? await Task.sleep(nanoseconds: 3_500_000_000); if !Task.isCancelled { toast = nil } }
    }
    func show(_ error: Error) { toast(error.localizedDescription, error: true) }
}
