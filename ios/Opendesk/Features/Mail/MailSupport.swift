import SafariServices
import SwiftUI

// MARK: - Colours

extension MailTone {
    /// A folder or account colour: a provider hex (`#16a766`) as it is, a tone name as its tone.
    static func color(_ key: String?) -> Color {
        guard let key, key.hasPrefix("#"), key.count == 7, let value = UInt32(key.dropFirst(), radix: 16) else {
            return of(key).solid
        }
        return Color(hex: value)
    }
}

/// A provider label or folder: its coloured dot and name.
struct MailLabelChip: View {
    let name: String
    var color: String?
    var height: CGFloat = 20

    var body: some View {
        HStack(spacing: 5) {
            Circle().fill(MailTone.color(color)).frame(width: 6.5, height: 6.5)
            Text(name).font(ODFont.sans(11.5, weight: .medium)).lineLimit(1)
        }
        .foregroundStyle(Theme.fg2)
        .padding(.horizontal, 7)
        .frame(height: height)
        .background(Theme.sunken, in: RoundedRectangle(cornerRadius: Theme.Radius.pill, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.pill, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
    }
}

/// An account's colour dot, the way it marks rows when the list spans accounts.
struct MailAccountDot: View {
    let color: String
    var size: CGFloat = 7

    var body: some View {
        Circle().fill(MailTone.color(color)).frame(width: size, height: size)
    }
}

// MARK: - Mail HTML

enum MailHTML {
    /// The HTML a message shows: its sanitized HTML, or its text as paragraphs.
    static func body(of message: MailMessage) -> String {
        let html = message.bodyHTML.trimmingCharacters(in: .whitespacesAndNewlines)
        if !html.isEmpty { return html }
        return HTMLText.html(fromPlain: message.bodyText)
    }

    private static let remoteImage = try? NSRegularExpression(pattern: #"(<img\b[^>]*?\s)src\s*=\s*(["'])\s*(?:https?:)?//[^"']*\2"#, options: [.caseInsensitive])
    private static let remoteSrcset = try? NSRegularExpression(pattern: #"\ssrcset\s*=\s*(["'])[^"']*\1"#, options: [.caseInsensitive])
    private static let remoteURL = try? NSRegularExpression(pattern: #"url\(\s*(["']?)\s*(?:https?:)?//[^)"']*\1\s*\)"#, options: [.caseInsensitive])
    private static let remoteBackground = try? NSRegularExpression(pattern: #"\sbackground\s*=\s*(["'])\s*(?:https?:)?//[^"']*\1"#, options: [.caseInsensitive])

    /// Images from other sites held back until the reader asks: the sender learns nothing
    /// from opening it. Images inside the message (`data:`) and its own attachments stay.
    static func holdingImages(_ html: String, attachmentHost: String?) -> (html: String, held: Int) {
        var out = html
        var held = 0
        func replace(_ regex: NSRegularExpression?, with template: (NSTextCheckingResult, NSString) -> String?) {
            guard let regex else { return }
            let ns = out as NSString
            var result = ""
            var last = 0
            for match in regex.matches(in: out, range: NSRange(location: 0, length: ns.length)) {
                guard let replacement = template(match, ns) else { continue }
                result += ns.substring(with: NSRange(location: last, length: match.range.location - last))
                result += replacement
                last = match.range.location + match.range.length
                held += 1
            }
            guard last > 0 else { return }
            result += ns.substring(from: last)
            out = result
        }
        func own(_ text: String) -> Bool {
            guard let attachmentHost, !attachmentHost.isEmpty else { return false }
            return text.contains("//" + attachmentHost + "/") && text.contains("/collab/attachments/")
        }
        replace(remoteImage) { match, ns in
            let whole = ns.substring(with: match.range)
            if own(whole) { return nil }
            return ns.substring(with: match.range(at: 1)) + #"data-od-held="1" src="data:,""#
        }
        replace(remoteSrcset) { _, _ in "" }
        replace(remoteURL) { match, ns in own(ns.substring(with: match.range)) ? nil : "none" }
        replace(remoteBackground) { _, _ in "" }
        if held > 0 { out = "<style>img[data-od-held]{display:none !important}</style>" + out }
        return (out, held)
    }
}

// MARK: - People

enum MailNames {
    /// The list line for a conversation: the other people first, "me" for your own addresses.
    static func line(_ thread: MailThread, own: Set<String>, sentBox: Bool) -> String {
        let people = thread.participants
        let others = people.filter { !own.contains($0.email.lowercased()) }
        let mine = people.contains { own.contains($0.email.lowercased()) }
        if sentBox, !others.isEmpty {
            return "To: " + names(others, short: others.count > 1)
        }
        var parts = others.prefix(3).map { others.count > 1 ? $0.short : $0.display }
        if mine, thread.messageCount > 1 || others.isEmpty, parts.count < 3 { parts.append("me") }
        if parts.isEmpty { return "me" }
        return parts.joined(separator: ", ")
    }

    static func names(_ people: [MailAddress], short: Bool) -> String {
        people.prefix(3).map { short ? $0.short : $0.display }.joined(separator: ", ") + (people.count > 3 ? " +\(people.count - 3)" : "")
    }

    /// The person a row's avatar shows: the last other person, or the account itself.
    static func lead(_ thread: MailThread, own: Set<String>) -> MailAddress? {
        thread.participants.first { !own.contains($0.email.lowercased()) } ?? thread.participants.first
    }

    /// "to me, Marco Ricci" under a sender.
    static func recipients(_ message: MailMessage, own: Set<String>) -> String {
        let list = message.to + message.cc
        guard !list.isEmpty else { return "to nobody" }
        let shown = list.prefix(3).map { own.contains($0.email.lowercased()) ? "me" : $0.short }
        return "to " + shown.joined(separator: ", ") + (list.count > 3 ? " +\(list.count - 3)" : "")
    }
}

// MARK: - Web pages

/// A web page in Safari's own sheet (connecting accounts stays on the web).
struct MailSafariView: UIViewControllerRepresentable {
    let url: URL

    func makeUIViewController(context: Context) -> SFSafariViewController {
        let controller = SFSafariViewController(url: url)
        controller.dismissButtonStyle = .done
        return controller
    }

    func updateUIViewController(_ controller: SFSafariViewController, context: Context) {}
}

/// A URL the person opens in `MailSafariView`.
struct MailWebPage: Identifiable {
    let url: URL
    var id: String { url.absoluteString }
}

// MARK: - Files

enum MailFiles {
    /// A local copy of an attachment for Quick Look: `data:` URLs decode here, signed links download.
    static func local(_ attachment: MailAttachment, apiURL: URL) async throws -> URL {
        let folder = FileManager.default.temporaryDirectory.appending(path: "mail-files", directoryHint: .isDirectory)
            .appending(path: String(attachment.url.hashValue.magnitude), directoryHint: .isDirectory)
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        let name = attachment.filename.replacingOccurrences(of: "/", with: "-")
        let target = folder.appending(path: name.isEmpty ? "file" : name)
        if FileManager.default.fileExists(atPath: target.path()) { return target }
        let data: Data
        if attachment.url.hasPrefix("data:") {
            data = try decodeDataURL(attachment.url)
        } else {
            guard let url = URL(string: attachment.url, relativeTo: apiURL)?.absoluteURL, ["https", "http"].contains(url.scheme ?? "") else {
                throw URLError(.badURL)
            }
            let (body, response) = try await URLSession.shared.data(from: url)
            guard (response as? HTTPURLResponse)?.statusCode ?? 200 < 400 else { throw URLError(.badServerResponse) }
            data = body
        }
        try data.write(to: target, options: .atomic)
        return target
    }

    private static func decodeDataURL(_ raw: String) throws -> Data {
        guard let comma = raw.firstIndex(of: ",") else { throw URLError(.cannotDecodeContentData) }
        let meta = raw[raw.index(raw.startIndex, offsetBy: 5)..<comma]
        let payload = String(raw[raw.index(after: comma)...])
        if meta.hasSuffix(";base64") {
            guard let data = Data(base64Encoded: payload) else { throw URLError(.cannotDecodeContentData) }
            return data
        }
        return Data((payload.removingPercentEncoding ?? payload).utf8)
    }
}

// MARK: - Message bodies

/// A message's body: plain mail as text, which reads crisper and selects natively; rich mail
/// in the sandboxed web view, measured again once it has its real width.
struct MailBodyView: View {
    let html: String
    @State private var height: CGFloat = 40
    @State private var settled = false

    /// Paragraphs as blocks, so the space between them is a gap rather than an empty line.
    private var paragraphs: [String] {
        HTMLText.plain(from: html)
            .replacingOccurrences(of: #"\n[ \t]*\n\s*"#, with: "\u{0}", options: .regularExpression)
            .split(separator: "\u{0}")
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }
    }

    var body: some View {
        if HTMLText.needsRichRendering(html) {
            MessageWebView(html: html, baseURL: nil, height: $height)
                .frame(height: max(height, 24))
                .onChange(of: height) { _, _ in
                    guard !settled else { return }
                    settled = true
                    Task {
                        try? await Task.sleep(for: .milliseconds(180))
                        height = 1
                    }
                }
                .onGeometryChange(for: CGFloat.self, of: { $0.size.width }) { _ in
                    if settled { height = 1 }
                }
        } else {
            VStack(alignment: .leading, spacing: 11) {
                ForEach(Array(paragraphs.enumerated()), id: \.offset) { _, paragraph in
                    Text(MailHTML.linked(paragraph))
                        .font(ODFont.sans(15.5))
                        .foregroundStyle(Theme.fg)
                        .lineSpacing(3.5)
                        .tint(Tone.blue.solid)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            .textSelection(.enabled)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }
}

extension MailHTML {
    private static let detector = try? NSDataDetector(types: NSTextCheckingResult.CheckingType.link.rawValue | NSTextCheckingResult.CheckingType.phoneNumber.rawValue)

    /// Text with its web addresses, mail addresses and phone numbers as links.
    static func linked(_ text: String) -> AttributedString {
        var out = AttributedString(text)
        guard let detector else { return out }
        let ns = text as NSString
        for match in detector.matches(in: text, range: NSRange(location: 0, length: ns.length)) {
            guard let range = Range(match.range, in: text), let lower = AttributedString.Index(range.lowerBound, within: out),
                  let upper = AttributedString.Index(range.upperBound, within: out) else { continue }
            if let url = match.url {
                out[lower..<upper].link = url
            } else if let phone = match.phoneNumber, let url = URL(string: "tel:" + phone.filter { $0.isNumber || $0 == "+" }) {
                out[lower..<upper].link = url
            }
        }
        return out
    }
}
