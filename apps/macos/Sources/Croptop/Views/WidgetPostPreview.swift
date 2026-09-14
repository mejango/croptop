import SwiftUI
import WebKit

struct WidgetPreviewSource: Equatable {
    let siteRoot: URL
    let postID: String

    static func owned(siteID: String, post: Post, baseURL: URL) -> Self? {
        guard post.hasWidgetPreview, UUID(uuidString: siteID) != nil,
              UUID(uuidString: post.id) != nil,
              var url = URLComponents(url: baseURL, resolvingAgainstBaseURL: true) else { return nil }
        url.path = "/\(siteID)/"; url.query = nil; url.fragment = nil
        guard let root = url.url else { return nil }
        return Self(siteRoot: root, postID: post.id)
    }

    static func hasPreview(content: String, attachments: [String]) -> Bool {
        attachments.contains("preview.js") || content.range(of: #"<script[^>]+type=["']croptop/preview["']"#, options: [.regularExpression, .caseInsensitive]) != nil
    }

    func url(revision: Double, compact: Bool) -> URL {
        var parts = URLComponents(url: siteRoot, resolvingAgainstBaseURL: true)!
        parts.queryItems = [URLQueryItem(name: "preview", value: postID),
                            URLQueryItem(name: "preview-size", value: compact ? "row" : "frame"),
                            URLQueryItem(name: "t", value: String(revision))]
        return parts.url!
    }

    // Widget code can fetch its public site tree and HTTPS resources, never the
    // local console API. Install these rules before loading any publisher code.
    var contentRules: String {
        var origin = URLComponents(url: siteRoot, resolvingAgainstBaseURL: true)!
        origin.path = "/"; origin.query = nil; origin.fragment = nil
        let rules: [[String: Any]] = [
            ["trigger": ["url-filter": ".*"], "action": ["type": "block"]],
            ["trigger": ["url-filter": "^https://"], "action": ["type": "ignore-previous-rules"]],
            ["trigger": ["url-filter": "^" + NSRegularExpression.escapedPattern(for: origin.url!.absoluteString)], "action": ["type": "block"]],
            ["trigger": ["url-filter": "^" + NSRegularExpression.escapedPattern(for: siteRoot.absoluteString)], "action": ["type": "ignore-previous-rules"]],
            ["trigger": ["url-filter": "^data:"], "action": ["type": "ignore-previous-rules"]],
            ["trigger": ["url-filter": "^blob:"], "action": ["type": "ignore-previous-rules"]],
        ]
        return String(data: try! JSONSerialization.data(withJSONObject: rules), encoding: .utf8)!
    }
}

struct WidgetPostPreview: View {
    var source: WidgetPreviewSource
    var revision: Double
    var compact = false
    @State private var loading = true
    @State private var failed = false

    var body: some View {
        WidgetPreviewWebView(source: source, revision: revision, compact: compact,
                             loading: $loading, failed: $failed)
            .overlay {
                if failed {
                    Text("Preview unavailable").font(Theme.body(13)).foregroundColor(Theme.muted)
                        .frame(maxWidth: .infinity, maxHeight: .infinity).background(Theme.paper)
                } else if loading {
                    LoadingTicker().frame(maxWidth: .infinity, maxHeight: .infinity).background(Theme.paper)
                }
            }
            .allowsHitTesting(false)
            .accessibilityHidden(true)
    }
}

struct WidgetPreviewWebView: NSViewRepresentable {
    var source: WidgetPreviewSource
    var revision: Double
    var compact: Bool
    @Binding var loading: Bool
    @Binding var failed: Bool

    func makeCoordinator() -> Coordinator { Coordinator(self) }
    func makeNSView(context: Context) -> WKWebView {
        let config = WKWebViewConfiguration()
        config.websiteDataStore = .nonPersistent()
        config.mediaTypesRequiringUserActionForPlayback = .all
        let view = PassiveWidgetWebView(frame: .zero, configuration: config)
        view.navigationDelegate = context.coordinator
        view.underPageBackgroundColor = .clear
        return view
    }
    func updateNSView(_ view: WKWebView, context: Context) {
        let coordinator = context.coordinator
        coordinator.parent = self
        let url = source.url(revision: revision, compact: compact)
        guard coordinator.url != url else { return }
        coordinator.url = url
        view.stopLoading()
        Task { @MainActor in
            guard coordinator.url == url else { return }
            loading = true; failed = false
            do {
                guard let rules = try await WKContentRuleListStore.default().compileContentRuleList(
                    forIdentifier: "widget-" + UUID().uuidString, encodedContentRuleList: source.contentRules) else { throw URLError(.cannotLoadFromNetwork) }
                guard coordinator.url == url else { return }
                view.configuration.userContentController.removeAllContentRuleLists()
                view.configuration.userContentController.add(rules)
                // The view retains the compiled list; don't accumulate one disk entry per tile.
                view.load(URLRequest(url: url, cachePolicy: .reloadIgnoringLocalCacheData))
                try? await WKContentRuleListStore.default().removeContentRuleList(forIdentifier: rules.identifier)
            } catch {
                guard coordinator.url == url else { return }
                loading = false; failed = true
            }
        }
    }
    static func dismantleNSView(_ view: WKWebView, coordinator: Coordinator) {
        coordinator.url = nil
        view.stopLoading(); view.navigationDelegate = nil
        view.configuration.userContentController.removeAllContentRuleLists()
        view.loadHTMLString("", baseURL: nil)
    }
    final class Coordinator: NSObject, WKNavigationDelegate {
        var parent: WidgetPreviewWebView
        var url: URL?
        init(_ parent: WidgetPreviewWebView) { self.parent = parent }
        func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction, decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
            // No forms, popups, external navigation, or native bridge from a tile.
            decisionHandler(action.targetFrame?.isMainFrame == true && action.request.url == url && action.navigationType == .other ? .allow : .cancel)
        }
        func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
            webView.evaluateJavaScript("document.querySelector('.post-frame-widget') !== null") { [weak self] result, error in
                guard let self, webView.url == self.url else { return }
                self.parent.loading = false
                self.parent.failed = error != nil || (result as? Bool) != true
            }
        }
        func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) { fail(error) }
        func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) { fail(error) }
        func webViewWebContentProcessDidTerminate(_ webView: WKWebView) { parent.loading = false; parent.failed = true }
        private func fail(_ error: Error) {
            guard (error as NSError).code != NSURLErrorCancelled else { return }
            parent.loading = false; parent.failed = true
        }
    }
}

private final class PassiveWidgetWebView: WKWebView {
    override func hitTest(_ point: NSPoint) -> NSView? { nil }
}
