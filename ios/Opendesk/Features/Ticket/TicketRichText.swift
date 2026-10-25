import SwiftUI
import UIKit

/// Stored message HTML (already sanitized server-side) drawn as native text when it is
/// only paragraphs, links, emphasis, code, lists and mentions. Anything richer (tables,
/// images, designed emails) goes to `MessageWebView` instead.
enum TicketRichText {
    /// Whether the body needs the web view: a designed email, images, tables, preformatted text.
    static func needsWebView(_ html: String) -> Bool {
        HTMLText.isDesignedEmail(html)
            || html.range(of: #"(?i)<(img|table|pre|h[1-6]|hr|font|center)\b"#, options: .regularExpression) != nil
    }

    /// Images from other sites in the body (`<img src="https:">`, CSS `url(https:)`), not the instance's own.
    static func hasRemoteImages(_ html: String, own: URL?) -> Bool {
        let pattern = #"(?i)(<img[^>]+src\s*=\s*["']?\s*(https?:)?//[^"'\s>]+|url\(\s*["']?\s*(https?:)?//[^)"']+)"#
        guard let regex = try? NSRegularExpression(pattern: pattern) else { return false }
        let ns = html as NSString
        let host = own?.host()?.lowercased()
        for match in regex.matches(in: html, range: NSRange(location: 0, length: ns.length)) {
            let found = ns.substring(with: match.range).lowercased()
            if let host, found.contains("//" + host) { continue }
            return true
        }
        return false
    }

    private static func capture(_ pattern: String, in text: String) -> String? {
        guard let regex = try? NSRegularExpression(pattern: pattern),
              let match = regex.firstMatch(in: text, range: NSRange(text.startIndex..., in: text)),
              let range = Range(match.range(at: 1), in: text)
        else { return nil }
        return String(text[range])
    }

    struct Style {
        var font: Font = ODFont.sans(15.5)
        var code: Font = ODFont.mono(13.5)
        var color: Color = Theme.fg
        var quote: Color = Theme.fg3
        var link: Color = Tone.blue.text
        var mention: Color = Tone.blue.text
        var mentionBackground: Color = Tone.blue.soft
    }

    /// The body as an attributed string.
    static func attributed(_ html: String, style: Style = Style()) -> AttributedString {
        var out = AttributedString()
        var bold = 0, italic = 0, code = 0, quote = 0, mention = 0
        var links: [URL?] = []
        var lists: [(ordered: Bool, index: Int)] = []
        var pendingBreak = ""

        func append(_ raw: String) {
            var text = HTMLText.decodeEntities(raw).replacingOccurrences(of: "\n", with: " ")
            text = text.replacingOccurrences(of: #" {2,}"#, with: " ", options: .regularExpression)
            guard !text.isEmpty else { return }
            if !pendingBreak.isEmpty {
                if !out.characters.isEmpty { out.append(AttributedString(pendingBreak)) }
                pendingBreak = ""
                text = String(text.drop { $0 == " " })
                guard !text.isEmpty else { return }
            }
            var piece = AttributedString(text)
            piece.font = code > 0 ? style.code : style.font
            if bold > 0 { piece.font = (code > 0 ? style.code : style.font).weight(.semibold) }
            if italic > 0 { piece.font = (piece.font ?? style.font).italic() }
            piece.foregroundColor = quote > 0 ? style.quote : style.color
            if code > 0 { piece.backgroundColor = Theme.active }
            if mention > 0 {
                piece.foregroundColor = style.mention
                piece.backgroundColor = style.mentionBackground
                piece.font = style.font.weight(.medium)
            }
            if let link = links.last ?? nil {
                piece.link = link
                piece.foregroundColor = style.link
                piece.underlineStyle = .single
            }
            out.append(piece)
        }

        func block(_ separator: String) {
            guard !out.characters.isEmpty else { return }
            if separator.count > pendingBreak.count { pendingBreak = separator }
        }

        let pattern = #"<(/?)([a-zA-Z0-9]+)([^>]*)>"#
        guard let regex = try? NSRegularExpression(pattern: pattern) else { return AttributedString(HTMLText.plain(from: html)) }
        let ns = html as NSString
        var cursor = 0
        for match in regex.matches(in: html, range: NSRange(location: 0, length: ns.length)) {
            if match.range.location > cursor {
                append(ns.substring(with: NSRange(location: cursor, length: match.range.location - cursor)))
            }
            cursor = match.range.location + match.range.length
            let closing = ns.substring(with: match.range(at: 1)) == "/"
            let tag = ns.substring(with: match.range(at: 2)).lowercased()
            let attributes = ns.substring(with: match.range(at: 3))
            switch tag {
            case "br": block("\n")
            case "p", "div": block("\n\n")
            case "strong", "b": bold += closing ? -1 : 1
            case "em", "i": italic += closing ? -1 : 1
            case "code": code += closing ? -1 : 1
            case "blockquote":
                quote += closing ? -1 : 1
                block("\n\n")
            case "a":
                if closing {
                    _ = links.popLast()
                } else {
                    let url = capture(#"(?i)href\s*=\s*["']([^"']*)["']"#, in: attributes).map(HTMLText.decodeEntities).flatMap(URL.init(string:)).flatMap { ["http", "https", "mailto", "tel"].contains($0.scheme?.lowercased() ?? "") ? $0 : nil }
                    links.append(url)
                }
            case "span":
                if closing {
                    if mention > 0 { mention -= 1 }
                } else if attributes.contains("mention") {
                    mention += 1
                }
            case "ul", "ol":
                if closing {
                    _ = lists.popLast()
                    block("\n\n")
                } else {
                    lists.append((tag == "ol", 0))
                    block("\n")
                }
            case "li":
                if closing {
                    block("\n")
                } else {
                    block("\n")
                    if !lists.isEmpty { lists[lists.count - 1].index += 1 }
                    let marker = lists.last.map { $0.ordered ? "\($0.index). " : "\u{2022}  " } ?? "\u{2022}  "
                    if !out.characters.isEmpty { out.append(AttributedString(pendingBreak)) }
                    pendingBreak = ""
                    var bullet = AttributedString(marker)
                    bullet.font = style.font
                    bullet.foregroundColor = Theme.fg3
                    out.append(bullet)
                }
            default: break
            }
        }
        if cursor < ns.length { append(ns.substring(from: cursor)) }
        return out
    }
}

/// A message body: native text when it is simple, else the sandboxed web view; inbound
/// images from other sites wait for "Load images".
struct TicketMessageBody: View {
    let html: String
    var inbound: Bool
    var baseURL: URL?
    var textColor: Color = Theme.fg
    @Binding var remoteImages: Bool
    @State private var height: CGFloat = 40

    var body: some View {
        let web = TicketRichText.needsWebView(html)
        VStack(alignment: .leading, spacing: 8) {
            if web, inbound, !remoteImages, TicketRichText.hasRemoteImages(html, own: baseURL) {
                RemoteImagesNotice { remoteImages = true }
            }
            if web {
                MessageWebView(html: html, baseURL: baseURL, remoteImages: !inbound || remoteImages, height: $height)
                    .frame(height: max(height, 24))
                    .frame(maxWidth: .infinity)
                    .clipShape(RoundedRectangle(cornerRadius: HTMLText.isDesignedEmail(html) ? 8 : 0, style: .continuous))
            } else {
                Text(TicketRichText.attributed(html, style: TicketRichText.Style(color: textColor)))
                    .lineSpacing(3)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
    }
}

/// "Remote images are hidden to protect your privacy." with a way to load them.
struct RemoteImagesNotice: View {
    let load: () -> Void

    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: "photo.badge.exclamationmark").font(.system(size: 12.5))
                .foregroundStyle(Theme.fg3)
            Text("Remote images are hidden to protect your privacy.")
                .font(ODFont.sans(12.5))
                .foregroundStyle(Theme.fg2)
                .fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 4)
            Button("Load images") {
                Haptics.tap()
                load()
            }
            .font(ODFont.sans(12.5, weight: .semibold))
            .foregroundStyle(Theme.fg)
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 8)
        .background(Theme.sunken, in: RoundedRectangle(cornerRadius: 8, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 8, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
    }
}
