import SwiftUI
import WebKit
import AppKit

struct PostContentPreview: View {
    var title: String
    var content: String
    var attachments: [PreviewAttachment]
    var allowRemoteMedia = true
    @State private var rendered = ""
    @State private var error: String?
    @State private var loading = true
    @State private var retry = 0

    var body: some View {
        ZStack {
            RenderedPostHTML(html: PreviewDocument.html(title: title, body: rendered, allowRemoteMedia: allowRemoteMedia), attachments: attachments)
            if loading && rendered.isEmpty { LoadingTicker(accessibilityLabel: "Preparing preview").padding(16).background(Theme.paper) }
            if let error {
                VStack(spacing: 12) {
                    Text(error).font(Theme.body(14))
                    Button("Retry") { retry += 1 }.buttonStyle(BorderedButton())
                }.padding(22).frame(maxWidth: .infinity, maxHeight: .infinity).background(Theme.paper)
            }
        }
        .background(Theme.paper)
        .task(id: content + "\n" + String(retry)) {
            loading = true; error = nil
            do {
                try await Task.sleep(nanoseconds: 250_000_000)
                let html = try await API.shared.renderMarkdown(content)
                try Task.checkCancellation()
                rendered = html; loading = false
            } catch is CancellationError { }
            catch { if !Task.isCancelled { self.error = error.localizedDescription; loading = false } }
        }
    }
}

struct LargePostPreview: View {
    var title: String
    var content: String
    var attachments: [PreviewAttachment]
    @Environment(\.dismiss) private var dismiss
    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack {
                Text("Post preview").font(Theme.heading(22))
                Spacer()
                Button("Done") { dismiss() }.buttonStyle(BorderedButton()).keyboardShortcut(.cancelAction)
            }
            PostContentPreview(title: title, content: content, attachments: attachments)
                .overlay(Rectangle().stroke(Theme.rule, lineWidth: Theme.border))
        }
        .padding(Theme.content)
        .frame(minWidth: 700, idealWidth: 1040, minHeight: 560, idealHeight: 780)
        .background(Theme.paper)
    }
}

enum PreviewDocument {
    static func html(title: String, body: String, allowRemoteMedia: Bool = true) -> String {
        let safeTitle = title.replacingOccurrences(of: "&", with: "&amp;").replacingOccurrences(of: "<", with: "&lt;").replacingOccurrences(of: ">", with: "&gt;").replacingOccurrences(of: "\"", with: "&quot;")
        let remoteSources = allowRemoteMedia ? " http: https:" : ""
        return """
        <!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
        <meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src croptop-preview: data:\(remoteSources); media-src croptop-preview:\(remoteSources); font-src data:; style-src 'unsafe-inline'; base-uri croptop-preview:; form-action 'none'">
        <style>
        \(AppFonts.previewFontFaces)
        :root {color-scheme:light} * {box-sizing:border-box} body {margin:0;padding:24px;color:#171717;background:white;font:17px/1.6 'Simplon',-apple-system,Helvetica,Arial,sans-serif;overflow-wrap:anywhere}
        h1,h2,h3 {line-height:1.2;font-weight:700} h1 {font-size:28px;margin:0 0 24px} p {margin:0 0 16px} img,video,svg {max-width:100%!important;height:auto!important} img {object-fit:contain} video,audio {width:100%} pre {white-space:pre-wrap;padding:16px;background:#f5f5f5} pre,code {font-family:ui-monospace,Menlo,monospace} code {font-size:14px} a {color:#171717;text-decoration:underline} blockquote {margin:16px 0;padding-left:16px;border-left:2px solid #171717} table {border-collapse:collapse;max-width:100%} th,td {border:1px solid #e2e2e2;padding:8px} iframe,form,script {display:none!important}
        </style></head><body>\(safeTitle.isEmpty ? "" : "<h1>" + safeTitle + "</h1>")\(body)</body></html>
        """
    }
}

// Only the post content is rendered by WebKit; editor controls remain SwiftUI.
// Scripts and forms are disabled. The attachment scheme exposes exact names only;
// feed documents also block HTTP media so content cannot request the console API.
struct RenderedPostHTML: NSViewRepresentable {
    var html: String
    var attachments: [PreviewAttachment]

    func makeCoordinator() -> Coordinator { Coordinator() }
    func makeNSView(context: Context) -> WKWebView {
        let configuration = WKWebViewConfiguration()
        configuration.defaultWebpagePreferences.allowsContentJavaScript = false
        configuration.websiteDataStore = .nonPersistent()
        configuration.setURLSchemeHandler(context.coordinator.resources, forURLScheme: "croptop-preview")
        let view = WKWebView(frame: .zero, configuration: configuration)
        view.navigationDelegate = context.coordinator
        return view
    }
    func updateNSView(_ view: WKWebView, context: Context) {
        let mapping = Dictionary(attachments.map { ($0.name, $0.url) }, uniquingKeysWith: { _, latest in latest })
        guard context.coordinator.html != html || context.coordinator.resources.files != mapping else { return }
        context.coordinator.html = html
        context.coordinator.resources.files = mapping
        view.loadHTMLString(html, baseURL: URL(string: "croptop-preview://attachment/"))
    }
    final class Coordinator: NSObject, WKNavigationDelegate {
        var html = ""
        let resources = PreviewResources()
        func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction, decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
            if action.navigationType == .linkActivated {
                if let url = action.request.url, ["https", "http", "mailto"].contains(url.scheme) { NSWorkspace.shared.open(url) }
                decisionHandler(.cancel)
            } else if action.navigationType == .formSubmitted || action.navigationType == .formResubmitted {
                decisionHandler(.cancel)
            } else if let url = action.request.url, url.scheme != "about" && url.scheme != "croptop-preview" {
                decisionHandler(.cancel)
            } else { decisionHandler(.allow) }
        }
    }
}

final class PreviewResources: NSObject, WKURLSchemeHandler {
    var files: [String: URL] = [:]
    private var active: Set<ObjectIdentifier> = []
    private var tasks: [ObjectIdentifier: URLSessionDataTask] = [:]

    func webView(_ webView: WKWebView, start urlSchemeTask: WKURLSchemeTask) {
        let key = ObjectIdentifier(urlSchemeTask as AnyObject)
        guard let request = urlSchemeTask.request.url, request.host == "attachment",
              let source = files[String(request.path.dropFirst())] else {
            urlSchemeTask.didFailWithError(URLError(.fileDoesNotExist)); return
        }
        active.insert(key)
        let deliver: (Data?, URLResponse?, Error?) -> Void = { data, response, error in
            DispatchQueue.main.async {
                guard self.active.remove(key) != nil else { return }
                self.tasks.removeValue(forKey: key)
                if let error { urlSchemeTask.didFailWithError(error); return }
                guard let data else { urlSchemeTask.didFailWithError(URLError(.cannotDecodeContentData)); return }
                let mime = response?.mimeType ?? Self.mime(source.pathExtension)
                urlSchemeTask.didReceive(URLResponse(url: request, mimeType: mime, expectedContentLength: data.count, textEncodingName: nil))
                urlSchemeTask.didReceive(data)
                urlSchemeTask.didFinish()
            }
        }
        if source.isFileURL {
            DispatchQueue.global(qos: .userInitiated).async {
                do { deliver(try Data(contentsOf: source), nil, nil) } catch { deliver(nil, nil, error) }
            }
        } else {
            let task = URLSession.shared.dataTask(with: source, completionHandler: deliver)
            tasks[key] = task; task.resume()
        }
    }
    func webView(_ webView: WKWebView, stop urlSchemeTask: WKURLSchemeTask) {
        let key = ObjectIdentifier(urlSchemeTask as AnyObject)
        active.remove(key); tasks.removeValue(forKey: key)?.cancel()
    }
    private static func mime(_ extensionName: String) -> String {
        switch extensionName.lowercased() {
        case "png": return "image/png"
        case "jpg", "jpeg": return "image/jpeg"
        case "gif": return "image/gif"
        case "webp": return "image/webp"
        case "svg": return "image/svg+xml"
        case "mp4", "m4v": return "video/mp4"
        case "mov": return "video/quicktime"
        case "mp3": return "audio/mpeg"
        default: return "application/octet-stream"
        }
    }
}
