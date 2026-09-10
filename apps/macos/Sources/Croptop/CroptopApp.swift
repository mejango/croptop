// Entry point, node lifecycle, and the things only AppKit can do: files
// handed to the app, fonts, quitting. The design lives in docs/design.
import SwiftUI
import AppKit
import CoreText

@main
struct CroptopApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) var delegate

    var body: some Scene {
        WindowGroup("Croptop") {
            RootView()
                .environmentObject(delegate.model)
                .frame(minWidth: 860, minHeight: 560)
                .background(Theme.paper)
                .foregroundColor(Theme.ink)
                .preferredColorScheme(.light)   // the design is paper and ink; no dark mode yet
        }
        .windowStyle(.hiddenTitleBar)
        .defaultSize(width: 1180, height: 800)
        .commands {
            CommandGroup(replacing: .newItem) {
                Button("New Post") { delegate.model.newPost() }.keyboardShortcut("n")
                Button("New Site…") { delegate.model.sheet = .newSite }.keyboardShortcut("n", modifiers: [.command, .shift])
                Button("Follow a Site…") { delegate.model.sheet = .follow }.keyboardShortcut("f", modifiers: [.command, .shift])
                Divider()
                Button("Post Files…") { delegate.pickFiles() }.keyboardShortcut("o")
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

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    let model = AppModel()
    private var node: Process?
    private var pending: [URL] = []

    func applicationDidFinishLaunching(_ note: Notification) {
        registerFonts()
        NSApp.appearance = NSAppearance(named: .aqua)
        NSApp.setActivationPolicy(.regular)
        Task { await startNode() }
    }

    // The app owns the node: start it if nothing answers, stop it on quit.
    func startNode() async {
        let bin = Node.binary
        if !(await API.shared.ping()), let bin = bin {
            // A login service holds the port and the datastore; retire it and wait for the lock to free.
            Node.run(bin, ["service", "uninstall"]).waitUntilExit()
            for _ in 0..<40 where await API.shared.ping() { try? await Task.sleep(nanoseconds: 250_000_000) }
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
            model.ready = true
            await model.load()
            // CROPTOP_SCREEN=site:<id> | editor:<site>[:<post>] | quick:<id> opens the app on a screen (for review).
            if let want = ProcessInfo.processInfo.environment["CROPTOP_SCREEN"] {
                let p = want.split(separator: ":").map(String.init)
                switch p.first {
                case "site" where p.count > 1: model.screen = .site(p[1])
                case "editor" where p.count > 1: model.screen = .editor(site: p[1], post: p.count > 2 ? p[2] : nil)
                case "quick" where p.count > 1: model.screen = .quick(p[1])
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

    // The brand fonts ship in Contents/Resources/fonts; a dev build finds them in the repo.
    private func registerFonts() {
        var dirs: [URL] = []
        if let r = Bundle.main.resourceURL { dirs.append(r.appendingPathComponent("fonts")) }
        let src = URL(fileURLWithPath: #filePath)
        dirs.append(src.deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
            .deletingLastPathComponent().deletingLastPathComponent().appendingPathComponent("installer/fonts"))
        for d in dirs {
            guard let files = try? FileManager.default.contentsOfDirectory(at: d, includingPropertiesForKeys: nil) else { continue }
            for f in files where f.pathExtension == "ttf" {
                CTFontManagerRegisterFontsForURL(f as CFURL, .process, nil)
            }
            if !files.isEmpty { break }
        }
    }
}

enum Node {
    static let dataDir = FileManager.default.homeDirectoryForCurrentUser
        .appendingPathComponent("Library/Application Support/croptop", isDirectory: true)

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
