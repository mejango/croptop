// One observable model for the window: what is loaded, what is shown, what just happened.
import SwiftUI
import AppKit

enum Screen: Hashable {
    case feed
    case ownedFeed
    case followingSite(String)
    case site(String)
    case settings(String)
    case editor(site: String, post: String?)
    case quick(String)
}

enum Sheet: Identifiable {
    case newSite, follow, curate
    case capture(String)
    var id: Int { switch self { case .newSite: return 0; case .follow: return 1; case .curate: return 2; case .capture: return 3 } }
}

struct Toast: Equatable { var text: String; var error = false }

@MainActor
final class AppModel: ObservableObject {
    @Published var captureShortcutEnabled = false
    @Published var capturing = false
    private var captureHotKey: CaptureHotKey?
    private var welcomeTask: Task<Void, Never>?

    @Published var ready = false
    @Published var fatal: String?
    @Published var sites: [Site] = []
    @Published var following: [Following] = []
    @Published var unfollowing: Set<String> = []
    @Published var status: Status?
    @Published var feed: [FeedItem] = []
    @Published var posts: [String: [Post]] = [:]     // by site id
    @Published var screen: Screen = .feed
    @Published var sheet: Sheet?
    @Published var toast: Toast?
    @Published var publishing: Set<String> = []
    @Published var droppedFiles: [URL] = []          // handed to the next new-post editor
    @Published var updating = false
    @Published var collaborationOpen = false

    // The console may be a separately installed, older engine. Its update flag
    // must not decide whether this signed app needs to be downloaded again.
    let appVersion: String?
    private let preferences: UserDefaults
    private var siteOrderKey: String { "siteOrder:" + API.shared.base.absoluteString }

    init(appVersion: String? = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String, preferences: UserDefaults = .standard) {
        self.appVersion = appVersion
        self.preferences = preferences
    }

    let updater = AppUpdater()

    var availableAppUpdate: String? { updater.availableVersion }

    private let api = API.shared
    private var toastTask: Task<Void, Never>?
    private var statusTask: Task<Void, Never>?

    var currentSite: Site? {
        switch screen {
        case .site(let id), .settings(let id), .editor(let id, _): return sites.first { $0.id == id }
        default: return nil
        }
    }
    var currentSiteID: String? {
        if case .site(let id) = screen { return id }
        if case .settings(let id) = screen { return id }
        if case .editor(let id, _) = screen { return id }
        return nil
    }

    func load() async {
        async let s = api.sites()
        async let f = api.following()
        async let st = api.status()
        async let fe = api.feed()
        do {
            sites = orderedSites(try await s.filter { $0.archived != true })
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

    func orderedSites(_ incoming: [Site]) -> [Site] {
        let order = preferences.stringArray(forKey: siteOrderKey) ?? []
        let positions = Dictionary(order.enumerated().map { ($0.element, $0.offset) }, uniquingKeysWith: min)
        return incoming.enumerated().sorted {
            let left = positions[$0.element.id] ?? Int.max, right = positions[$1.element.id] ?? Int.max
            return left == right ? $0.offset < $1.offset : left < right
        }.map(\.element)
    }

    func moveSite(_ id: String, by offset: Int) {
        guard let index = sites.firstIndex(where: { $0.id == id }) else { return }
        moveSite(id, to: max(0, min(sites.count - 1, index + offset)))
    }
    func moveSite(_ id: String, onto target: String) {
        guard let targetIndex = sites.firstIndex(where: { $0.id == target }) else { return }
        moveSite(id, to: targetIndex)
    }

    // Native table drops identify a gap in the original list, including its end.
    @discardableResult func moveSite(_ id: String, beforeRow row: Int) -> Bool {
        guard (0...sites.count).contains(row), let index = sites.firstIndex(where: { $0.id == id }) else { return false }
        return moveSite(id, to: row > index ? row - 1 : row)
    }

    @discardableResult private func moveSite(_ id: String, to destination: Int) -> Bool {
        guard sites.indices.contains(destination), let index = sites.firstIndex(where: { $0.id == id }), index != destination else { return false }
        var reordered = sites
        let site = reordered.remove(at: index)
        reordered.insert(site, at: destination)
        sites = reordered
        preferences.set(sites.map(\.id), forKey: siteOrderKey)
        return true
    }

    func tagChoices(for site: String) -> [String: String] {
        var choices = sites.first { $0.id == site }?.tags ?? [:]
        for post in posts[site] ?? [] {
            for (key, label) in post.tags ?? [:] where choices[key] == nil { choices[key] = label }
        }
        return choices
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

    func unfollow(_ ipns: String) async {
        guard unfollowing.insert(ipns).inserted else { return }
        defer { unfollowing.remove(ipns) }
        do {
            try await api.unfollow(ipns)
            if screen == .followingSite(ipns) { screen = .feed }
            following.removeAll { $0.ipns == ipns }
            feed.removeAll { $0.siteID == nil && $0.ipns == ipns }
            await load()
        } catch {
            toast("Couldn’t unfollow this site. " + error.localizedDescription, error: true)
        }
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
            do {
                let r = try await api.publish(site: site)
                // Publishing is complete; refreshing the feed must not keep the CTA busy.
                publishing.remove(site)
                let seq = r["sequence"].map { "\($0)" } ?? ""
                toast("Published" + (seq.isEmpty ? "." : " at sequence \(seq)."))
                await load()
            } catch {
                publishing.remove(site)
                show(error)
            }
        }
    }
    func publishCurrent() { if let id = currentSiteID { publish(id) } }

    func openCurrentInBrowser() {
        guard let id = currentSiteID else { return }
        Task { if let u = try? await api.siteURL(id), let url = URL(string: u) { NSWorkspace.shared.open(url) } }
    }

    func prepareFirstLaunch() async {
        let setup = FirstLaunchSetup(preferences: preferences, scope: api.base.absoluteString,
            sites: { try await API.shared.sites() }, following: { try await API.shared.following() },
            create: { try await API.shared.createSite(name: "Untitled", about: "") },
            follow: { try await API.shared.follow(FirstLaunchSetup.welcomeSite) })
        do {
            if let id = try await setup.prepare() {
                await load()
                if sites.contains(where: { $0.id == id }) { screen = .site(id) }
            }
            welcomeTask = Task { [weak self] in
                do {
                    try await setup.finishFollowing()
                    if let list = try? await API.shared.following() { self?.following = list }
                    if let posts = try? await API.shared.feed() { self?.feed = posts }
                } catch {
                    self?.toast("Your site is ready. Croptop’s welcome feed will retry next launch.", error: true)
                }
            }
        } catch { show(error) }
    }

    func restoreCaptureShortcut() {
        if preferences.bool(forKey: "captureShortcutEnabled") { enableCaptureShortcut() }
    }
    func enableCaptureShortcut() {
        guard captureHotKey == nil else { return }
        do {
            captureHotKey = try CaptureHotKey { [weak self] in self?.captureScreenshot() }
            captureShortcutEnabled = true
            preferences.set(true, forKey: "captureShortcutEnabled")
        } catch { show(error) }
    }
    func disableCaptureShortcut() {
        captureHotKey = nil; captureShortcutEnabled = false
        preferences.set(false, forKey: "captureShortcutEnabled")
    }
    func captureScreenshot() {
        guard ready, !capturing else { return }
        guard sheet == nil else { toast("Finish the open form first."); return }
        capturing = true
        let wasActive = NSApp.isActive
        NSApp.hide(nil)
        Task {
            defer { capturing = false }
            let directory = FileManager.default.temporaryDirectory.appendingPathComponent("croptop-capture-" + UUID().uuidString, isDirectory: true)
            defer { try? FileManager.default.removeItem(at: directory) }
            do {
                try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
                let format = DateFormatter(); format.dateFormat = "yyyy-MM-dd 'at' HH.mm.ss"
                let file = directory.appendingPathComponent("Screenshot " + format.string(from: Date()) + ".png")
                try await Task.sleep(nanoseconds: 200_000_000)
                guard try await ScreenshotCapture.capture(to: file) else {
                    if wasActive { NSApp.activate(ignoringOtherApps: true) }
                    return
                }
                let group = try await api.uploadQuick([file])
                // The capture is now staged by the engine; its temporary input can be removed.
                sheet = .capture(group)
                NSApp.activate(ignoringOtherApps: true)
                NSApp.windows.first(where: { $0.canBecomeMain })?.makeKeyAndOrderFront(nil)
            } catch {
                NSApp.activate(ignoringOtherApps: true)
                show(error)
            }
        }
    }

    // Files from the Dock, Finder, a drop, or File > Post Files open the post editor with them attached.
    func postFiles(_ urls: [URL]) {
        let media = urls.filter { !$0.hasDirectoryPath }
        guard !media.isEmpty else { return }
        guard let site = currentSiteID ?? preferences.string(forKey: "quickSite").flatMap({ id in sites.first { $0.id == id }?.id }) ?? sites.first?.id else { sheet = .newSite; return }
        droppedFiles = media
        screen = .editor(site: site, post: nil)
    }

    func update() {
        updater.check()
    }

    func toast(_ text: String, error: Bool = false) {
        toast = Toast(text: text, error: error)
        toastTask?.cancel()
        toastTask = Task { try? await Task.sleep(nanoseconds: 3_500_000_000); if !Task.isCancelled { toast = nil } }
    }
    func show(_ error: Error) { toast(error.localizedDescription, error: true) }
}

@MainActor func NSAppOpen(_ u: URL) { NSWorkspace.shared.open(u) }
