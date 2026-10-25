import SwiftUI
import UIKit
import WebKit

/// A stored article (sanitized by the server before storage) in a web view with
/// JavaScript off and a CSP that only lets images (https, data:) and the bundled
/// Geist faces load. Article typography: headings, lists, quotes, code blocks
/// in Geist Mono on a sunken panel. It sizes itself to the content; links open
/// outside the app.
struct KBArticleWebView: UIViewRepresentable {
    let html: String
    @Binding var height: CGFloat
    var fontSize: CGFloat = 16
    @Environment(\.colorScheme) private var scheme

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    func makeUIView(context: Context) -> WKWebView {
        let configuration = WKWebViewConfiguration()
        configuration.defaultWebpagePreferences.allowsContentJavaScript = false
        configuration.websiteDataStore = .nonPersistent()
        let webView = WKWebView(frame: CGRect(x: 0, y: 0, width: 360, height: 80), configuration: configuration)
        webView.navigationDelegate = context.coordinator
        webView.scrollView.isScrollEnabled = false
        webView.scrollView.bounces = false
        webView.isOpaque = false
        webView.backgroundColor = .clear
        webView.scrollView.backgroundColor = .clear
        webView.scrollView.contentInsetAdjustmentBehavior = .never
        let coordinator = context.coordinator
        coordinator.observation = webView.scrollView.observe(\.contentSize, options: [.new]) { [weak coordinator] _, change in
            let measured = change.newValue?.height ?? 0
            Task { @MainActor in
                guard let coordinator, measured > 1, abs(coordinator.parent.height - measured) > 1 else { return }
                coordinator.parent.height = measured
            }
        }
        return webView
    }

    func updateUIView(_ webView: WKWebView, context: Context) {
        context.coordinator.parent = self
        let document = KBArticleDocument.make(body: html, dark: scheme == .dark, fontSize: fontSize)
        guard document != context.coordinator.loaded else { return }
        context.coordinator.loaded = document
        webView.loadHTMLString(document, baseURL: nil)
    }

    static func dismantleUIView(_ webView: WKWebView, coordinator: Coordinator) {
        coordinator.observation = nil
        webView.navigationDelegate = nil
    }

    @MainActor
    final class Coordinator: NSObject, WKNavigationDelegate {
        var parent: KBArticleWebView
        var loaded: String?
        var observation: NSKeyValueObservation?

        init(_ parent: KBArticleWebView) {
            self.parent = parent
        }

        func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction) async -> WKNavigationActionPolicy {
            if action.navigationType == .other { return .allow }
            if let url = action.request.url, ["http", "https", "mailto", "tel"].contains(url.scheme?.lowercased() ?? "") {
                await UIApplication.shared.open(url)
            }
            return .cancel
        }
    }
}

/// The article document: Geist from the app bundle (as data: URIs), the app's ink and tones.
@MainActor
enum KBArticleDocument {
    private static let fontFaces: String = {
        let faces: [(String, String, Int)] = [
            ("Geist-Regular", "Geist", 400), ("Geist-Medium", "Geist", 500), ("Geist-SemiBold", "Geist", 600),
            ("GeistMono-Regular", "Geist Mono", 400),
        ]
        return faces.compactMap { file, family, weight in
            let url = Bundle.main.url(forResource: file, withExtension: "ttf") ?? Bundle.main.url(forResource: file, withExtension: "ttf", subdirectory: "Fonts")
            guard let url, let data = try? Data(contentsOf: url) else { return nil }
            return "@font-face { font-family: '\(family)'; font-weight: \(weight); src: url(data:font/ttf;base64,\(data.base64EncodedString())) format('truetype'); }"
        }.joined(separator: "\n")
    }()

    private static func css(_ color: Color, dark: Bool) -> String {
        UIColor(color).resolvedColor(with: UITraitCollection(userInterfaceStyle: dark ? .dark : .light)).cssHex
    }

    static func make(body: String, dark: Bool, fontSize: CGFloat) -> String {
        let fg = css(Theme.fg, dark: dark)
        let fg2 = css(Theme.fg2, dark: dark)
        let fg3 = css(Theme.fg3, dark: dark)
        let line = css(Theme.line, dark: dark)
        let sunken = css(Theme.sunken, dark: dark)
        let hover = css(Theme.hover, dark: dark)
        let link = css(Tone.blue.solid, dark: dark)
        let quote = css(Tone.violet.solid, dark: dark)
        return """
        <!doctype html><html><head><meta charset="utf-8">
        <meta name="viewport" content="width=device-width, initial-scale=1, maximum-scale=1">
        <meta name="referrer" content="no-referrer">
        <meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src https: data:; style-src 'unsafe-inline'; font-src data:">
        <meta name="color-scheme" content="\(dark ? "dark" : "light")">
        <style>
        \(fontFaces)
        :root { color-scheme: \(dark ? "dark" : "light"); }
        html, body { margin: 0; padding: 0; background: transparent; }
        body { font-family: 'Geist', -apple-system, sans-serif; font-size: \(Int(fontSize))px; line-height: 1.62; color: \(fg2); letter-spacing: -0.005em; overflow-wrap: anywhere; -webkit-text-size-adjust: 100%; -webkit-font-smoothing: antialiased; }
        body > :first-child { margin-top: 0 !important; }
        body > :last-child { margin-bottom: 0 !important; }
        p { margin: 0 0 0.9em; }
        h1, h2, h3 { color: \(fg); font-weight: 600; letter-spacing: -0.02em; line-height: 1.25; }
        h1 { font-size: 1.5em; margin: 1.4em 0 0.5em; }
        h2 { font-size: 1.26em; margin: 1.5em 0 0.5em; }
        h3 { font-size: 1.07em; margin: 1.3em 0 0.4em; }
        strong, b { color: \(fg); font-weight: 600; }
        a { color: \(link); text-decoration: none; border-bottom: 1px solid \(link)44; }
        ul, ol { margin: 0 0 0.9em; padding-left: 1.35em; }
        li { margin: 0.28em 0; padding-left: 0.15em; }
        li::marker { color: \(fg3); }
        ol li::marker { font-family: 'Geist Mono', ui-monospace, monospace; font-size: 0.86em; }
        blockquote { margin: 1.1em 0; padding: 0.7em 1em; border-left: 3px solid \(quote); background: \(quote)14; border-radius: 0 8px 8px 0; color: \(fg); }
        blockquote p { margin: 0 0 0.5em; } blockquote p:last-child { margin: 0; }
        code { font-family: 'Geist Mono', ui-monospace, monospace; font-size: 0.86em; background: \(hover); border: 1px solid \(line); border-radius: 5px; padding: 0.08em 0.36em; color: \(fg); }
        pre { margin: 1.1em 0; padding: 0.95em 1.05em; background: \(sunken); border: 1px solid \(line); border-radius: 10px; overflow-x: auto; white-space: pre-wrap; }
        pre code { background: none; border: 0; padding: 0; font-size: 0.82em; line-height: 1.6; color: \(fg2); }
        hr { border: 0; height: 1px; background: \(line); margin: 1.8em 0; }
        img { max-width: 100%; height: auto; border-radius: 10px; border: 1px solid \(line); }
        table { width: 100%; border-collapse: collapse; margin: 1em 0; font-size: 0.92em; }
        th, td { border-bottom: 1px solid \(line); padding: 0.5em 0.4em; text-align: left; }
        th { color: \(fg); font-weight: 600; }
        </style></head><body>\(body)</body></html>
        """
    }
}
