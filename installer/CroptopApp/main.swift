// Croptop for macOS: a window around the console. The app starts the node
// when it opens and stops it when it quits; files dropped on the Dock icon or
// the window become a post. Everything else is the web console at 127.0.0.1:8086.
import Cocoa
import WebKit

let console = URL(string: "http://127.0.0.1:8086")!
let binary = Bundle.main.url(forResource: "croptop", withExtension: nil)!.path
let dataDir = FileManager.default.homeDirectoryForCurrentUser
    .appendingPathComponent("Library/Application Support/croptop", isDirectory: true)

func ping() -> Bool {
    var ok = false
    let done = DispatchSemaphore(value: 0)
    var req = URLRequest(url: console.appendingPathComponent("v0/ping"))
    req.timeoutInterval = 2
    URLSession.shared.dataTask(with: req) { _, resp, _ in
        ok = (resp as? HTTPURLResponse)?.statusCode == 200
        done.signal()
    }.resume()
    done.wait()
    return ok
}

@discardableResult
func croptop(_ args: [String], wait: Bool = true) -> Process {
    let p = Process()
    p.executableURL = URL(fileURLWithPath: binary)
    p.arguments = args
    try? FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
    let logURL = dataDir.appendingPathComponent("app.log")
    if !FileManager.default.fileExists(atPath: logURL.path) {
        FileManager.default.createFile(atPath: logURL.path, contents: nil)
    }
    if let log = try? FileHandle(forWritingTo: logURL) {
        log.seekToEndOfFile()
        p.standardOutput = log
        p.standardError = log
    }
    try? p.run()
    if wait { p.waitUntilExit() }
    return p
}

// The window's content view: accepts file drops anywhere over the console.
final class DropView: NSView {
    var onDrop: (([URL]) -> Void)?
    override init(frame: NSRect) {
        super.init(frame: frame)
        registerForDraggedTypes([.fileURL])
    }
    required init?(coder: NSCoder) { fatalError() }
    override func draggingEntered(_ sender: NSDraggingInfo) -> NSDragOperation { .copy }
    override func performDragOperation(_ sender: NSDraggingInfo) -> Bool {
        let urls = sender.draggingPasteboard.readObjects(forClasses: [NSURL.self], options: [.urlReadingFileURLsOnly: true]) as? [URL] ?? []
        guard !urls.isEmpty else { return false }
        onDrop?(urls)
        return true
    }
}

final class App: NSObject, NSApplicationDelegate, NSWindowDelegate, WKNavigationDelegate, WKUIDelegate {
    var window: NSWindow!
    var web: WKWebView!
    var node: Process?
    var pending: [URL] = []
    var loaded = false

    func applicationDidFinishLaunching(_ note: Notification) {
        buildMenu()
        buildWindow()
        // The app owns the node from now on; a login service would fight it for the port.
        croptop(["service", "uninstall"])
        startNode()
    }

    func startNode() {
        if ping() { load(); return }
        node = croptop(["serve", "--no-open"], wait: false)
        DispatchQueue.global().async {
            for _ in 0..<120 {
                if ping() { DispatchQueue.main.async { self.load() }; return }
                Thread.sleep(forTimeInterval: 0.5)
            }
            DispatchQueue.main.async { self.failed() }
        }
    }

    func load() {
        loaded = true
        web.load(URLRequest(url: console))
        if !pending.isEmpty { let p = pending; pending = []; post(p) }
    }

    func failed() {
        let a = NSAlert()
        a.messageText = "Croptop could not start its node"
        a.informativeText = "See \(dataDir.appendingPathComponent("app.log").path)"
        a.runModal()
    }

    // Files from the Dock icon, Finder's Open With, or a drop on the window.
    func application(_ app: NSApplication, open urls: [URL]) { post(urls) }

    func post(_ urls: [URL]) {
        guard loaded else { pending += urls; return }
        let boundary = "croptop-\(UUID().uuidString)"
        var body = Data()
        for u in urls {
            guard let bytes = try? Data(contentsOf: u) else { continue }
            body.append("--\(boundary)\r\nContent-Disposition: form-data; name=\"files\"; filename=\"\(u.lastPathComponent)\"\r\nContent-Type: application/octet-stream\r\n\r\n".data(using: .utf8)!)
            body.append(bytes)
            body.append("\r\n".data(using: .utf8)!)
        }
        body.append("--\(boundary)--\r\n".data(using: .utf8)!)
        var req = URLRequest(url: console.appendingPathComponent("v0/croptop/quick"))
        req.httpMethod = "POST"
        req.setValue("multipart/form-data; boundary=\(boundary)", forHTTPHeaderField: "Content-Type")
        req.httpBody = body
        URLSession.shared.dataTask(with: req) { data, _, _ in
            guard let data = data,
                  let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                  let open = json["open"] as? String else { return }
            DispatchQueue.main.async {
                self.web.load(URLRequest(url: URL(string: open, relativeTo: console)!.absoluteURL))
                self.show()
            }
        }.resume()
    }

    func show() {
        NSApp.activate(ignoringOtherApps: true)
        window.makeKeyAndOrderFront(nil)
    }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows: Bool) -> Bool {
        show()
        return true
    }
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }

    func applicationWillTerminate(_ note: Notification) {
        // Ask the console to stop, then make sure the node we started is gone.
        var req = URLRequest(url: console.appendingPathComponent("v0/croptop/quit"))
        req.httpMethod = "POST"
        req.timeoutInterval = 3
        let done = DispatchSemaphore(value: 0)
        URLSession.shared.dataTask(with: req) { _, _, _ in done.signal() }.resume()
        _ = done.wait(timeout: .now() + 3)
        if let n = node, n.isRunning {
            for _ in 0..<20 where n.isRunning { Thread.sleep(forTimeInterval: 0.25) }
            if n.isRunning { n.terminate() }
        }
    }

    // Links that leave the console (sites, gateways, GitHub) open in the browser.
    func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction, decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
        if let url = action.request.url, let host = url.host, host != console.host, url.scheme?.hasPrefix("http") == true {
            NSWorkspace.shared.open(url)
            decisionHandler(.cancel)
            return
        }
        decisionHandler(.allow)
    }
    func webView(_ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration, for action: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? {
        if let url = action.request.url { NSWorkspace.shared.open(url) }
        return nil
    }

    func buildWindow() {
        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1180, height: 800),
                          styleMask: [.titled, .closable, .miniaturizable, .resizable], backing: .buffered, defer: false)
        window.title = "Croptop"
        window.minSize = NSSize(width: 720, height: 480)
        window.isReleasedWhenClosed = false
        window.delegate = self
        window.center()
        window.setFrameAutosaveName("Croptop") // restores the last frame when there is one
        let content = DropView(frame: window.contentView!.bounds)
        content.autoresizingMask = [.width, .height]
        content.onDrop = { [weak self] urls in self?.post(urls) }
        let conf = WKWebViewConfiguration()
        conf.preferences.setValue(true, forKey: "developerExtrasEnabled")
        web = WKWebView(frame: content.bounds, configuration: conf)
        web.autoresizingMask = [.width, .height]
        web.navigationDelegate = self
        web.uiDelegate = self
        content.addSubview(web)
        window.contentView = content
        show()
    }

    @objc func reload() { web.reload() }
    @objc func openInBrowser() { NSWorkspace.shared.open(console) }

    func buildMenu() {
        let bar = NSMenu()
        let app = NSMenuItem(); bar.addItem(app)
        let appMenu = NSMenu()
        appMenu.addItem(withTitle: "About Croptop", action: #selector(NSApplication.orderFrontStandardAboutPanel(_:)), keyEquivalent: "")
        appMenu.addItem(.separator())
        appMenu.addItem(withTitle: "Hide Croptop", action: #selector(NSApplication.hide(_:)), keyEquivalent: "h")
        appMenu.addItem(.separator())
        appMenu.addItem(withTitle: "Quit Croptop", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        app.submenu = appMenu

        let edit = NSMenuItem(); bar.addItem(edit)
        let editMenu = NSMenu(title: "Edit")
        editMenu.addItem(withTitle: "Undo", action: Selector(("undo:")), keyEquivalent: "z")
        editMenu.addItem(withTitle: "Redo", action: Selector(("redo:")), keyEquivalent: "Z")
        editMenu.addItem(.separator())
        editMenu.addItem(withTitle: "Cut", action: #selector(NSText.cut(_:)), keyEquivalent: "x")
        editMenu.addItem(withTitle: "Copy", action: #selector(NSText.copy(_:)), keyEquivalent: "c")
        editMenu.addItem(withTitle: "Paste", action: #selector(NSText.paste(_:)), keyEquivalent: "v")
        editMenu.addItem(withTitle: "Select All", action: #selector(NSText.selectAll(_:)), keyEquivalent: "a")
        edit.submenu = editMenu

        let view = NSMenuItem(); bar.addItem(view)
        let viewMenu = NSMenu(title: "View")
        viewMenu.addItem(withTitle: "Reload", action: #selector(reload), keyEquivalent: "r")
        viewMenu.addItem(withTitle: "Open in Browser", action: #selector(openInBrowser), keyEquivalent: "")
        view.submenu = viewMenu

        let win = NSMenuItem(); bar.addItem(win)
        let winMenu = NSMenu(title: "Window")
        winMenu.addItem(withTitle: "Minimize", action: #selector(NSWindow.miniaturize(_:)), keyEquivalent: "m")
        winMenu.addItem(withTitle: "Zoom", action: #selector(NSWindow.zoom(_:)), keyEquivalent: "")
        winMenu.addItem(withTitle: "Close", action: #selector(NSWindow.performClose(_:)), keyEquivalent: "w")
        win.submenu = winMenu
        NSApp.mainMenu = bar
        NSApp.windowsMenu = winMenu
    }
}

let app = NSApplication.shared
let delegate = App()
app.delegate = delegate
app.setActivationPolicy(.regular)
app.run()
