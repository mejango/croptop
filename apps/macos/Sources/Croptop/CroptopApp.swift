// Entry point, node lifecycle, and the things only AppKit can do: files
// handed to the app, fonts, quitting. The design lives in docs/design.
import SwiftUI
import AppKit

@main
struct CroptopApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) var delegate

    init() { AppFonts.register() }

    var body: some Scene {
        WindowGroup("Croptop") {
            RootView()
                .environmentObject(delegate.model)
                .font(Theme.body())
                .frame(minWidth: 860, minHeight: 560)
                .background(Theme.paper)
                .foregroundColor(Theme.ink)
                .preferredColorScheme(.light)   // the design is paper and ink; no dark mode yet
                .modifier(NoFocusRing())        // fields draw their own focus outline; nothing else gets the system ring
        }
        .windowStyle(.hiddenTitleBar)
        .handlesExternalEvents(matching: [])   // files opened from the Dock go to the delegate, not a new window
        .defaultSize(width: 1180, height: 800)
        .commands {
            CommandGroup(after: .appInfo) { CheckForUpdates(updater: delegate.model.updater) }
            CommandGroup(replacing: .newItem) {
                Button("New Post") { delegate.model.newPost() }.keyboardShortcut("n")
                Button("New Site…") { delegate.model.sheet = .newSite }.keyboardShortcut("n", modifiers: [.command, .shift])
                Button("Follow a Site…") { delegate.model.sheet = .follow }.keyboardShortcut("f", modifiers: [.command, .shift])
                Divider()
                Button("Post Files…") { delegate.pickFiles() }.keyboardShortcut("o")
                Button("Capture Screenshot…") { delegate.model.captureScreenshot() }
                    .disabled(!delegate.model.ready || delegate.model.capturing)
                Button(delegate.model.captureShortcutEnabled ? "Disable Screenshot Shortcut (⌘⇧C)" : "Enable Screenshot Shortcut (⌘⇧C)") {
                    if delegate.model.captureShortcutEnabled { delegate.model.disableCaptureShortcut() }
                    else { delegate.model.enableCaptureShortcut() }
                }
            }
            CommandMenu("Site") {
                Button("Publish") { delegate.model.publishCurrent() }
                    .keyboardShortcut("p", modifiers: [.command, .shift])
                    .disabled(delegate.model.currentSite == nil)
                Button("Open in Browser") { delegate.model.openCurrentInBrowser() }
                    .disabled(delegate.model.currentSite == nil)
            }
            CommandGroup(after: .toolbar) {
                Button("Reload") { Task { await delegate.model.load() } }.keyboardShortcut("r")
            }
        }
    }
}

private struct NoFocusRing: ViewModifier {
    @ViewBuilder func body(content: Content) -> some View {
        if #available(macOS 14, *) { content.focusEffectDisabled() } else { content }
    }
}

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    let model = AppModel()
    private var node: Process?
    private var pending: [URL] = []

    func applicationDidFinishLaunching(_ note: Notification) {
        model.updater.start(model: model)
        NSApp.appearance = NSAppearance(named: .aqua)
        NSApp.setActivationPolicy(.regular)
        Task { await startNode() }
    }

    // A relaunched app starts its own bundled engine, including the one-time
    // migration from the old login service. Explicit development consoles are untouched.
    func startNode() async {
        let bin = Node.binary
        let needsLaunch: Bool
        do {
            needsLaunch = try await EngineStartup.needsLaunch(
                externalConsole: ProcessInfo.processInfo.environment["CROPTOP_CONSOLE"] != nil,
                bundled: Node.bundledBinary != nil,
                legacyService: FileManager.default.fileExists(atPath: Node.legacyService.path),
                retire: {
                    guard let bin else { return }
                    let retirement = Node.run(bin, ["service", "uninstall"])
                    while retirement.isRunning { try await Task.sleep(nanoseconds: 200_000_000) }
                },
                ping: { await API.shared.ping() },
                pause: { try await Task.sleep(nanoseconds: 250_000_000) })
        } catch {
            model.fatal = error.localizedDescription
            return
        }
        if needsLaunch, let bin {
            // Start serve; if it exits fast (datastore still locked by the one just stopped), wait and retry.
            retry: for attempt in 0..<6 {
                let p = Node.run(bin, ["serve", "--no-open"])
                node = p
                for _ in 0..<40 {
                    if await API.shared.ping() { break retry }
                    if !p.isRunning { break }
                    try? await Task.sleep(nanoseconds: 500_000_000)
                }
                if p.isRunning { break }
                try? await Task.sleep(nanoseconds: UInt64(attempt + 1) * 1_000_000_000)
            }
        }
        if await API.shared.ping() {
            await model.load()
            await model.prepareFirstLaunch()
            // Open on the first site in the rail rather than an empty feed.
            if model.screen == .feed, let first = model.following.first.map({ Screen.followingSite($0.ipns) }) ?? model.sites.first.map({ Screen.site($0.id) }) { model.screen = first }
            model.ready = true
            model.restoreCaptureShortcut()
            // CROPTOP_SCREEN=site:<id> | editor:<site>[:<post>] opens the app on a screen (for review).
            if let want = ProcessInfo.processInfo.environment["CROPTOP_SCREEN"] {
                let p = want.split(separator: ":").map(String.init)
                switch p.first {
                case "newsite": model.sheet = .newSite
                case "follow": model.sheet = .follow
                case "settings" where p.count > 1: model.screen = .settings(p[1])
                case "site" where p.count > 1: model.screen = .site(p[1])
                case "editor" where p.count > 1: model.screen = .editor(site: p[1], post: p.count > 2 ? p[2] : nil)
                default: break
                }
            }
            if !pending.isEmpty { let p = pending; pending = []; model.postFiles(p) }
        } else {
            model.fatal = bin == nil
                ? "The croptop engine is not inside this app. Reinstall Croptop from crop.top."
                : "Croptop could not start its node. See \(Node.dataDir.appendingPathComponent("app.log").path)."
        }
    }

    func application(_ app: NSApplication, open urls: [URL]) {
        if model.ready { model.postFiles(urls) } else { pending += urls }
    }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows: Bool) -> Bool {
        if !hasVisibleWindows { NSApp.windows.first?.makeKeyAndOrderFront(nil) }
        return true
    }
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }

    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        guard AppUpdater.blocksRelaunch(screen: model.screen, sheet: model.sheet, publishing: model.publishing, collaborationOpen: model.collaborationOpen) else { return .terminateNow }
        let alert = NSAlert()
        if !model.publishing.isEmpty {
            alert.messageText = "Publishing is still in progress"
            alert.informativeText = "Wait for publishing to finish before quitting or installing an update."
            alert.addButton(withTitle: "Keep Croptop open")
            alert.runModal()
            return .terminateCancel
        }
        alert.messageText = "Finish editing before quitting?"
        alert.informativeText = "Unsaved changes will be lost if you quit now."
        alert.addButton(withTitle: "Keep editing")
        alert.addButton(withTitle: "Quit without saving")
        return alert.runModal() == .alertSecondButtonReturn ? .terminateNow : .terminateCancel
    }

    func applicationWillTerminate(_ note: Notification) {
        guard let n = node else { return }   // a console we found running is not ours to stop
        API.shared.quitSync()
        if n.isRunning {
            for _ in 0..<20 where n.isRunning { Thread.sleep(forTimeInterval: 0.25) }
            if n.isRunning { n.terminate() }
        }
    }

    func pickFiles() {
        let p = NSOpenPanel()
        p.allowsMultipleSelection = true
        p.canChooseDirectories = false
        p.message = "Choose images, video or audio for a new post"
        if p.runModal() == .OK { model.postFiles(p.urls) }
    }

}

enum Node {
    static let dataDir = FileManager.default.homeDirectoryForCurrentUser
        .appendingPathComponent("Library/Application Support/croptop", isDirectory: true)

    static var legacyService: URL {
        FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/LaunchAgents/top.crop.croptop.plist")
    }
    static var bundledBinary: String? {
        guard let path = Bundle.main.resourceURL?.appendingPathComponent("croptop").path,
              FileManager.default.isExecutableFile(atPath: path) else { return nil }
        return path
    }

    // The engine sits in the app bundle; a dev build falls back to the repo build or PATH.
    static var binary: String? {
        var candidates: [String] = []
        if let r = Bundle.main.resourceURL { candidates.append(r.appendingPathComponent("croptop").path) }
        let src = URL(fileURLWithPath: #filePath)
        candidates.append(src.deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
            .deletingLastPathComponent().deletingLastPathComponent().appendingPathComponent("croptop").path)
        candidates.append(FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent(".local/bin/croptop").path)
        return candidates.first { FileManager.default.isExecutableFile(atPath: $0) }
    }

    @discardableResult
    static func run(_ bin: String, _ args: [String]) -> Process {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: bin)
        p.arguments = args
        try? FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        let log = dataDir.appendingPathComponent("app.log")
        if !FileManager.default.fileExists(atPath: log.path) { FileManager.default.createFile(atPath: log.path, contents: nil) }
        if let h = try? FileHandle(forWritingTo: log) { h.seekToEndOfFile(); p.standardOutput = h; p.standardError = h }
        try? p.run()
        return p
    }
}
