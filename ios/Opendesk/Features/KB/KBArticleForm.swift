import Foundation

/// What the editor holds for one article. The body is markdown while the
/// stored HTML converts without loss, otherwise the HTML itself (`raw`).
struct KBArticleForm: Equatable {
    var title = ""
    var excerpt = ""
    var slug = ""
    var slugEdited = false
    var collectionID: String?
    var markdown = ""
    var html = ""
    var raw = false

    static func from(_ article: KBArticle) -> KBArticleForm {
        var form = KBArticleForm(title: article.title, excerpt: article.excerpt, slug: article.slug, slugEdited: true, collectionID: article.collectionID)
        form.load(html: article.bodyHTML)
        return form
    }

    /// The body to store.
    var bodyHTML: String { raw ? html : KBMarkdown.html(from: markdown) }

    var isEmpty: Bool {
        title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && excerpt.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && HTMLText.plain(from: bodyHTML).trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    /// Puts stored HTML in the editor: as markdown when lossless, else as HTML.
    mutating func load(html stored: String) {
        if let text = KBMarkdown.markdown(from: stored) {
            markdown = text
            html = stored
            raw = false
        } else {
            markdown = ""
            html = stored
            raw = true
        }
    }

    /// Switches to editing the HTML, keeping what was written.
    mutating func useHTML() {
        guard !raw else { return }
        html = KBMarkdown.html(from: markdown)
        raw = true
    }

    /// Switches back to text when the HTML converts without loss; false when it can't.
    mutating func useText() -> Bool {
        guard raw else { return true }
        guard let text = KBMarkdown.markdown(from: html) else { return false }
        markdown = text
        raw = false
        return true
    }

    /// The same article, as far as saving goes.
    func sameContent(as other: KBArticleForm) -> Bool {
        title == other.title && excerpt == other.excerpt && slug == other.slug && collectionID == other.collectionID && bodyHTML == other.bodyHTML
    }
}

/// The formatting bar's edits on the markdown, by character offsets so a
/// selection survives the change.
enum KBFormat: String, CaseIterable, Identifiable {
    case heading, bold, italic, bullets, numbers, link, code, quote

    var id: String { rawValue }

    var symbol: String {
        switch self {
        case .heading: "textformat.size"
        case .bold: "bold"
        case .italic: "italic"
        case .bullets: "list.bullet"
        case .numbers: "list.number"
        case .link: "link"
        case .code: "chevron.left.forwardslash.chevron.right"
        case .quote: "text.quote"
        }
    }

    var label: String {
        switch self {
        case .heading: "Heading"
        case .bold: "Bold"
        case .italic: "Italic"
        case .bullets: "Bulleted list"
        case .numbers: "Numbered list"
        case .link: "Link"
        case .code: "Code"
        case .quote: "Quote"
        }
    }

    /// Applies the format to `text` around `range` (character offsets) and answers the new text and selection.
    func apply(to text: String, range: Range<Int>) -> (String, Range<Int>) {
        var chars = Array(text)
        let lower = max(0, min(range.lowerBound, chars.count))
        let upper = max(lower, min(range.upperBound, chars.count))
        switch self {
        case .bold: return Self.wrap(&chars, lower ..< upper, "**", "**", placeholder: "bold text")
        case .italic: return Self.wrap(&chars, lower ..< upper, "*", "*", placeholder: "italic text")
        case .link:
            if lower == upper {
                let insert = Array("[link text](https://)")
                chars.insert(contentsOf: insert, at: lower)
                return (String(chars), (lower + 1) ..< (lower + 10))
            }
            chars.insert(contentsOf: Array("](https://)"), at: upper)
            chars.insert("[", at: lower)
            let urlStart = upper + 1 + 2
            return (String(chars), urlStart ..< (urlStart + 8))
        case .code:
            let selected = chars[lower ..< upper]
            if selected.contains("\n") || (lower == upper && Self.lineIsEmpty(chars, at: lower)) {
                let body = lower == upper ? Array("code") : Array(selected)
                let block = Array("```\n") + body + Array("\n```")
                chars.replaceSubrange(lower ..< upper, with: block)
                return (String(chars), (lower + 4) ..< (lower + 4 + body.count))
            }
            return Self.wrap(&chars, lower ..< upper, "`", "`", placeholder: "code")
        case .heading, .bullets, .numbers, .quote:
            return lines(&chars, lower ..< upper)
        }
    }

    private static func lineIsEmpty(_ chars: [Character], at index: Int) -> Bool {
        let start = lineStart(chars, index)
        let end = lineEnd(chars, index)
        return chars[start ..< end].allSatisfy(\.isWhitespace)
    }

    private static func lineStart(_ chars: [Character], _ index: Int) -> Int {
        var i = index
        while i > 0, chars[i - 1] != "\n" { i -= 1 }
        return i
    }

    private static func lineEnd(_ chars: [Character], _ index: Int) -> Int {
        var i = index
        while i < chars.count, chars[i] != "\n" { i += 1 }
        return i
    }

    private static func wrap(_ chars: inout [Character], _ range: Range<Int>, _ left: String, _ right: String, placeholder: String) -> (String, Range<Int>) {
        let l = Array(left), r = Array(right)
        if range.isEmpty {
            chars.insert(contentsOf: l + Array(placeholder) + r, at: range.lowerBound)
            let start = range.lowerBound + l.count
            return (String(chars), start ..< (start + placeholder.count))
        }
        // Already wrapped: take the markers off.
        if range.lowerBound >= l.count, range.upperBound + r.count <= chars.count,
           Array(chars[(range.lowerBound - l.count) ..< range.lowerBound]) == l,
           Array(chars[range.upperBound ..< (range.upperBound + r.count)]) == r {
            chars.removeSubrange(range.upperBound ..< (range.upperBound + r.count))
            chars.removeSubrange((range.lowerBound - l.count) ..< range.lowerBound)
            return (String(chars), (range.lowerBound - l.count) ..< (range.upperBound - l.count))
        }
        chars.insert(contentsOf: r, at: range.upperBound)
        chars.insert(contentsOf: l, at: range.lowerBound)
        return (String(chars), (range.lowerBound + l.count) ..< (range.upperBound + l.count))
    }

    /// The block marker a line starts with (`## `, `- `, `3. `, `> `), if any.
    static func marker(of line: [Character]) -> Int {
        let text = String(line)
        for prefix in ["### ", "## ", "# ", "- ", "* ", "+ ", "> "] where text.hasPrefix(prefix) { return prefix.count }
        if text == ">" { return 1 }
        var digits = 0
        while digits < line.count, line[digits].isASCII, line[digits].isNumber { digits += 1 }
        if digits > 0, digits < 4, line.count > digits + 1, line[digits] == "." || line[digits] == ")", line[digits + 1] == " " { return digits + 2 }
        return 0
    }

    private func has(_ line: [Character]) -> Bool {
        let text = String(line)
        switch self {
        case .heading: return text.hasPrefix("## ") || text.hasPrefix("### ") || text.hasPrefix("# ")
        case .bullets: return text.hasPrefix("- ") || text.hasPrefix("* ") || text.hasPrefix("+ ")
        case .quote: return text.hasPrefix("> ") || text == ">"
        case .numbers: return Self.marker(of: line) > 0 && (line.first?.isNumber ?? false)
        default: return false
        }
    }

    private func lines(_ chars: inout [Character], _ range: Range<Int>) -> (String, Range<Int>) {
        let start = Self.lineStart(chars, range.lowerBound)
        let end = Self.lineEnd(chars, range.upperBound)
        let segment = Array(chars[start ..< end])
        var rows = segment.split(separator: "\n", omittingEmptySubsequences: false).map(Array.init)
        let filled = rows.filter { !$0.allSatisfy(\.isWhitespace) }
        let removing = !filled.isEmpty && filled.allSatisfy(has)
        var number = 1
        for index in rows.indices {
            var row = rows[index]
            if row.allSatisfy(\.isWhitespace), rows.count > 1 { continue }
            if self == .heading, removing {
                // Cycle: ## becomes ###, ### becomes text.
                let text = String(row)
                if text.hasPrefix("## ") { row = Array("### ") + row.dropFirst(3) } else { row.removeFirst(Self.marker(of: row)) }
                rows[index] = row
                continue
            }
            row.removeFirst(Self.marker(of: row))
            if !removing {
                let prefix: String = switch self {
                case .heading: "## "
                case .bullets: "- "
                case .numbers: "\(number). "
                default: "> "
                }
                row = Array(prefix) + row
                number += 1
            }
            rows[index] = row
        }
        let replaced = rows.map { String($0) }.joined(separator: "\n")
        chars.replaceSubrange(start ..< end, with: Array(replaced))
        if range.isEmpty, rows.count == 1 {
            let caret = start + replaced.count
            return (String(chars), caret ..< caret)
        }
        return (String(chars), start ..< (start + replaced.count))
    }
}
