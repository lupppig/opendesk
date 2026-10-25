import SwiftUI
import WebKit

/// Stored email HTML (sanitized by the server with bluemonday) in a web view
/// with JavaScript off and a CSP that only lets images load (https, data:,
/// and the API's own attachments), with no referrer. With `remoteImages` off,
/// images from other sites (including CSS `url()`) stay blocked until the
/// reader asks. It sizes itself to the content; links open outside the app.
struct MessageWebView: UIViewRepresentable {
    let html: String
    let baseURL: URL?
    var remoteImages = true
    @Binding var height: CGFloat
    @Environment(\.colorScheme) private var scheme

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    func makeUIView(context: Context) -> WKWebView {
        let configuration = WKWebViewConfiguration()
        configuration.defaultWebpagePreferences.allowsContentJavaScript = false
        configuration.websiteDataStore = .nonPersistent()
        configuration.dataDetectorTypes = [.link, .phoneNumber]
        // A realistic first frame: a zero-sized web view created mid-transition can blank its first load.
        let webView = WKWebView(frame: CGRect(x: 0, y: 0, width: 340, height: 60), configuration: configuration)
        webView.navigationDelegate = context.coordinator
        webView.scrollView.isScrollEnabled = false
        webView.scrollView.bounces = false
        webView.isOpaque = false
        webView.backgroundColor = .clear
        webView.scrollView.backgroundColor = .clear
        webView.scrollView.contentInsetAdjustmentBehavior = .never
        let coordinator = context.coordinator
        coordinator.observation = webView.scrollView.observe(\.contentSize, options: [.new]) { [weak coordinator] _, change in
            guard let measured = change.newValue?.height else { return }
            Task { @MainActor [weak coordinator] in
                guard let coordinator, measured > 1, abs(coordinator.parent.height - measured) > 1 else { return }
                coordinator.parent.height = measured
            }
        }
        return webView
    }

    func updateUIView(_ webView: WKWebView, context: Context) {
        context.coordinator.parent = self
        let dark = scheme == .dark
        let traits = UITraitCollection(userInterfaceStyle: dark ? .dark : .light)
        var document = HTMLText.document(
            body: html,
            dark: dark,
            textColor: UIColor(Theme.fg).resolvedColor(with: traits).cssHex,
            linkColor: UIColor(Tone.blue.solid).resolvedColor(with: traits).cssHex,
            quoteColor: UIColor(Theme.fg3).resolvedColor(with: traits).cssHex
        )
        if !remoteImages {
            // Only the instance's own files and inline data load until the reader asks.
            let own = baseURL.flatMap { url in url.host().map { "\(url.scheme ?? "https")://\($0)\(url.port.map { ":\($0)" } ?? "")" } } ?? ""
            document = document.replacingOccurrences(of: "img-src 'self' https: data:", with: "img-src 'self' \(own) data:")
                .replacingOccurrences(of: "font-src https: data:", with: "font-src data:")
        }
        guard document != context.coordinator.loaded else { return }
        context.coordinator.loaded = document
        webView.loadHTMLString(document, baseURL: baseURL)
    }

    static func dismantleUIView(_ webView: WKWebView, coordinator: Coordinator) {
        coordinator.observation = nil
        webView.navigationDelegate = nil
    }

    @MainActor
    final class Coordinator: NSObject, WKNavigationDelegate {
        var parent: MessageWebView
        var loaded: String?
        var observation: NSKeyValueObservation?

        init(_ parent: MessageWebView) {
            self.parent = parent
        }

        func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction) async -> WKNavigationActionPolicy {
            // The document itself loads; every link the person taps opens outside the app.
            if action.navigationType == .other { return .allow }
            if let url = action.request.url, ["http", "https", "mailto", "tel"].contains(url.scheme?.lowercased() ?? "") {
                await UIApplication.shared.open(url)
            }
            return .cancel
        }
    }
}
