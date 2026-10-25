import Foundation

/// The article editor's text form: a small, strict markdown (headings 1 to 3,
/// paragraphs with line breaks, bold, italic, inline code, links, bullet and
/// numbered lists, quotes, code blocks and rules) that maps one to one onto the
/// HTML the knowledge base stores.
///
/// `markdown(from:)` answers nil whenever the stored HTML has anything the
/// markdown can't carry (images, tables, nested lists, attributes, unknown
/// tags), and also when converting back would not give the same document. The
/// editor then edits the HTML itself, so nothing is ever lost.
enum KBMarkdown {
    // MARK: - Markdown to HTML

    static func html(from markdown: String) -> String {
        let lines = markdown.replacingOccurrences(of: "\r\n", with: "\n").components(separatedBy: "\n")
        var out = ""
        var i = 0
        while i < lines.count {
            let line = lines[i]
            let trimmed = line.trimmingCharacters(in: .whitespaces)
            if trimmed.isEmpty {
                i += 1
                continue
            }
            if trimmed.hasPrefix("```") {
                let language = trimmed.dropFirst(3).trimmingCharacters(in: .whitespaces)
                var code: [String] = []
                i += 1
                while i < lines.count, !lines[i].trimmingCharacters(in: .whitespaces).hasPrefix("```") {
                    code.append(lines[i])
                    i += 1
                }
                i += 1
                let attribute = language.isEmpty ? "" : " class=\"language-\(HTMLText.escape(language))\""
                out += "<pre><code\(attribute)>" + HTMLText.escape(code.joined(separator: "\n")) + "</code></pre>"
                continue
            }
            if trimmed == "---" || trimmed == "***" {
                out += "<hr>"
                i += 1
                continue
            }
            if let (level, text) = heading(trimmed) {
                out += "<h\(level)>" + inline(text) + "</h\(level)>"
                i += 1
                continue
            }
            if bullet(trimmed) != nil {
                var items: [String] = []
                while i < lines.count, let item = bullet(lines[i].trimmingCharacters(in: .whitespaces)) {
                    items.append(item)
                    i += 1
                }
                out += "<ul>" + items.map { "<li>" + inline($0) + "</li>" }.joined() + "</ul>"
                continue
            }
            if ordered(trimmed) != nil {
                var items: [String] = []
                while i < lines.count, let item = ordered(lines[i].trimmingCharacters(in: .whitespaces)) {
                    items.append(item)
                    i += 1
                }
                out += "<ol>" + items.map { "<li>" + inline($0) + "</li>" }.joined() + "</ol>"
                continue
            }
            if trimmed.hasPrefix(">") {
                var paragraphs: [[String]] = [[]]
                while i < lines.count {
                    let current = lines[i].trimmingCharacters(in: .whitespaces)
                    guard current.hasPrefix(">") else { break }
                    var rest = current.dropFirst()
                    if rest.hasPrefix(" ") { rest = rest.dropFirst() }
                    let text = rest.trimmingCharacters(in: .whitespaces)
                    if text.isEmpty {
                        if !(paragraphs.last ?? []).isEmpty { paragraphs.append([]) }
                    } else {
                        paragraphs[paragraphs.count - 1].append(text)
                    }
                    i += 1
                }
                let body = paragraphs.filter { !$0.isEmpty }.map { "<p>" + $0.map(inline).joined(separator: "<br>") + "</p>" }.joined()
                out += "<blockquote>" + body + "</blockquote>"
                continue
            }
            var paragraph: [String] = []
            while i < lines.count {
                let current = lines[i].trimmingCharacters(in: .whitespaces)
                if current.isEmpty || (!paragraph.isEmpty && startsBlock(current)) { break }
                paragraph.append(current)
                i += 1
            }
            out += "<p>" + paragraph.map(inline).joined(separator: "<br>") + "</p>"
        }
        return out
    }

    private static func startsBlock(_ line: String) -> Bool {
        line.hasPrefix("```") || line == "---" || line == "***" || heading(line) != nil || bullet(line) != nil || ordered(line) != nil || line.hasPrefix(">")
    }

    private static func heading(_ line: String) -> (Int, String)? {
        for level in (1 ... 3).reversed() {
            let marker = String(repeating: "#", count: level) + " "
            if line.hasPrefix(marker) {
                let text = line.dropFirst(marker.count).trimmingCharacters(in: .whitespaces)
                return text.isEmpty ? nil : (level, text)
            }
        }
        return nil
    }

    private static func bullet(_ line: String) -> String? {
        for marker in ["- ", "* ", "+ "] where line.hasPrefix(marker) {
            return line.dropFirst(2).trimmingCharacters(in: .whitespaces)
        }
        return nil
    }

    static func ordered(_ line: String) -> String? {
        var digits = 0
        var index = line.startIndex
        while index < line.endIndex, line[index].isASCII, line[index].isNumber {
            digits += 1
            index = line.index(after: index)
        }
        guard digits > 0, digits < 4, index < line.endIndex, line[index] == "." || line[index] == ")" else { return nil }
        let after = line.index(after: index)
        guard after < line.endIndex, line[after] == " " else { return nil }
        return line[line.index(after: after)...].trimmingCharacters(in: .whitespaces)
    }

    /// Characters a backslash makes literal.
    static let escapable: Set<Character> = ["\\", "`", "*", "[", "]", "#", ">", "-", "+", ".", ")"]

    /// Inline markdown to HTML: `**bold**`, `*italic*`, `` `code` ``, `[text](url)` and backslash escapes.
    static func inline(_ text: String) -> String {
        let chars = Array(text)
        var out = ""
        var i = 0
        while i < chars.count {
            let c = chars[i]
            if c == "\\", i + 1 < chars.count, escapable.contains(chars[i + 1]) {
                out += HTMLText.escape(String(chars[i + 1]))
                i += 2
                continue
            }
            if c == "`", let close = find("`", in: chars, from: i + 1, plain: true), close > i + 1 {
                out += "<code>" + HTMLText.escape(String(chars[(i + 1) ..< close])) + "</code>"
                i = close + 1
                continue
            }
            if c == "*", i + 1 < chars.count, chars[i + 1] == "*", let close = find("**", in: chars, from: i + 2), close > i + 2 {
                out += "<strong>" + inline(String(chars[(i + 2) ..< close])) + "</strong>"
                i = close + 2
                continue
            }
            if c == "*", let close = find("*", in: chars, from: i + 1), close > i + 1 {
                out += "<em>" + inline(String(chars[(i + 1) ..< close])) + "</em>"
                i = close + 1
                continue
            }
            if c == "[", let link = link(in: chars, at: i) {
                out += "<a href=\"\(HTMLText.escape(link.url))\">" + inline(link.text) + "</a>"
                i = link.end
                continue
            }
            out += HTMLText.escape(String(c))
            i += 1
        }
        return out
    }

    /// The index of `marker` from `start`, skipping escapes and code spans (and, for a single `*`, any `**`).
    private static func find(_ marker: String, in chars: [Character], from start: Int, plain: Bool = false) -> Int? {
        let m = Array(marker)
        var j = start
        while j < chars.count {
            if !plain {
                if chars[j] == "\\" { j += 2; continue }
                if chars[j] == "`", let close = find("`", in: chars, from: j + 1, plain: true) { j = close + 1; continue }
                if m == ["*"], chars[j] == "*", j + 1 < chars.count, chars[j + 1] == "*" {
                    // A `**` pair inside italics belongs to bold: skip past its close.
                    if let close = find("**", in: chars, from: j + 2) { j = close + 2 } else { j += 2 }
                    continue
                }
            }
            if j + m.count <= chars.count, Array(chars[j ..< j + m.count]) == m { return j }
            j += 1
        }
        return nil
    }

    private static func link(in chars: [Character], at start: Int) -> (text: String, url: String, end: Int)? {
        guard let close = find("]", in: chars, from: start + 1), close + 1 < chars.count, chars[close + 1] == "(" else { return nil }
        var j = close + 2
        while j < chars.count, chars[j] != ")" {
            if chars[j] == " " { return nil }
            j += 1
        }
        guard j < chars.count, j > close + 2 else { return nil }
        return (String(chars[(start + 1) ..< close]), String(chars[(close + 2) ..< j]), j + 1)
    }

    // MARK: - HTML to Markdown

    /// The markdown for stored HTML, or nil when the markdown can't carry it exactly.
    static func markdown(from html: String) -> String? {
        let trimmed = html.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.isEmpty { return "" }
        guard let nodes = HTMLNode.parse(trimmed) else { return nil }
        guard let converted = try? blocks(nodes) else { return nil }
        let markdown = converted.map(\.markdown).joined(separator: "\n\n")
        let canonical = converted.map(\.html).joined()
        // Only a lossless round trip is edited as markdown.
        guard self.html(from: markdown) == canonical else { return nil }
        return markdown
    }

    private struct Unsupported: Error {}

    private struct Converted {
        var markdown: String
        var html: String
    }

    private static let blockTags: Set<String> = ["p", "h1", "h2", "h3", "ul", "ol", "blockquote", "pre", "hr"]

    private static func blocks(_ nodes: [HTMLNode]) throws -> [Converted] {
        var result: [Converted] = []
        var run: [HTMLNode] = []
        func flush() throws {
            defer { run = [] }
            guard run.contains(where: { !$0.isBlankText }) else { return }
            if let paragraph = try paragraph(run) { result.append(paragraph) }
        }
        for node in nodes {
            guard case let .element(name, attributes, children) = node, blockTags.contains(name) else {
                run.append(node)
                continue
            }
            try flush()
            switch name {
            case "p":
                if let paragraph = try paragraph(children) { result.append(paragraph) }
            case "h1", "h2", "h3":
                let level = name == "h1" ? 1 : name == "h2" ? 2 : 3
                let text = try inline(trim(children))
                guard !text.markdown.isEmpty, !text.markdown.contains("\n") else { throw Unsupported() }
                result.append(Converted(markdown: String(repeating: "#", count: level) + " " + text.markdown, html: "<\(name)>" + text.html + "</\(name)>"))
            case "ul", "ol":
                var md: [String] = []
                var html = ""
                var number = 0
                for child in children where !child.isBlankText {
                    guard case let .element("li", _, content) = child else { throw Unsupported() }
                    var inner = content.filter { !$0.isBlankText }
                    // A list item may hold its text in one paragraph (rich editors write it so).
                    if inner.count == 1, case let .element("p", _, paragraph) = inner[0] { inner = paragraph }
                    guard inner.allSatisfy(\.isInline) else { throw Unsupported() }
                    let text = try inline(trim(inner))
                    guard !text.markdown.isEmpty, !text.markdown.contains("\n") else { throw Unsupported() }
                    number += 1
                    md.append((name == "ul" ? "- " : "\(number). ") + text.markdown)
                    html += "<li>" + text.html + "</li>"
                }
                guard number > 0 else { continue }
                result.append(Converted(markdown: md.joined(separator: "\n"), html: "<\(name)>" + html + "</\(name)>"))
            case "blockquote":
                var paragraphs: [Converted] = []
                var loose: [HTMLNode] = []
                for child in children {
                    if case let .element("p", _, content) = child {
                        if loose.contains(where: { !$0.isBlankText }), let p = try paragraph(loose) { paragraphs.append(p) }
                        loose = []
                        if let p = try paragraph(content) { paragraphs.append(p) }
                    } else if !child.isInline {
                        throw Unsupported()
                    } else {
                        loose.append(child)
                    }
                }
                if loose.contains(where: { !$0.isBlankText }), let p = try paragraph(loose) { paragraphs.append(p) }
                guard !paragraphs.isEmpty else { continue }
                let md = paragraphs.map { p in p.markdown.components(separatedBy: "\n").map { "> " + $0 }.joined(separator: "\n") }.joined(separator: "\n>\n")
                result.append(Converted(markdown: md, html: "<blockquote>" + paragraphs.map(\.html).joined() + "</blockquote>"))
            case "pre":
                var language = ""
                var code = ""
                let inner = children.filter { !$0.isBlankText }
                if inner.count == 1, case let .element("code", codeAttributes, content) = inner[0] {
                    if let value = codeAttributes["class"] {
                        guard value.hasPrefix("language-"), !value.contains(" ") else { throw Unsupported() }
                        language = String(value.dropFirst("language-".count))
                    }
                    code = try rawText(content)
                } else {
                    code = try rawText(children)
                }
                guard attributes.isEmpty, !code.contains("```") else { throw Unsupported() }
                let attribute = language.isEmpty ? "" : " class=\"language-\(HTMLText.escape(language))\""
                result.append(Converted(markdown: "```\(language)\n\(code)\n```", html: "<pre><code\(attribute)>" + HTMLText.escape(code) + "</code></pre>"))
            case "hr":
                result.append(Converted(markdown: "---", html: "<hr>"))
            default:
                throw Unsupported()
            }
        }
        try flush()
        return result
    }

    /// A paragraph from inline nodes; nil when it is empty.
    private static func paragraph(_ nodes: [HTMLNode]) throws -> Converted? {
        guard nodes.allSatisfy(\.isInline) else { throw Unsupported() }
        let text = try inline(trim(nodes))
        guard !text.markdown.isEmpty else { return nil }
        let lines = text.markdown.components(separatedBy: "\n").map(escapeLineStart)
        return Converted(markdown: lines.joined(separator: "\n"), html: "<p>" + text.html + "</p>")
    }

    /// A line that would read as a block marker gets a backslash.
    private static func escapeLineStart(_ line: String) -> String {
        if line.hasPrefix("#") || line.hasPrefix(">") || line.hasPrefix("- ") || line.hasPrefix("+ ") || line == "---" { return "\\" + line }
        if ordered(line) != nil, let mark = line.firstIndex(where: { $0 == "." || $0 == ")" }) {
            var copy = line
            copy.insert("\\", at: mark)
            return copy
        }
        return line
    }

    /// Collapses whitespace and trims it at the run's ends and around line breaks.
    private static func trim(_ nodes: [HTMLNode]) -> [HTMLNode] {
        var out = nodes.map { node -> HTMLNode in
            if case let .text(value) = node { return .text(value.replacingOccurrences(of: #"\s+"#, with: " ", options: .regularExpression)) }
            return node
        }
        for index in out.indices {
            guard case let .text(value) = out[index] else { continue }
            var text = value
            let afterBreak = index == 0 || out[index - 1].isBreak
            let beforeBreak = index == out.count - 1 || out[index + 1].isBreak
            if afterBreak { text = String(text.drop(while: { $0 == " " })) }
            if beforeBreak { while text.hasSuffix(" ") { text.removeLast() } }
            out[index] = .text(text)
        }
        return out.filter { if case let .text(value) = $0 { return !value.isEmpty } else { return true } }
    }

    private static func inline(_ nodes: [HTMLNode]) throws -> Converted {
        var md = ""
        var html = ""
        for node in nodes {
            switch node {
            case let .text(value):
                let decoded = HTMLText.decodeEntities(value).replacingOccurrences(of: #"\s+"#, with: " ", options: .regularExpression)
                md += decoded.map { ["\\", "`", "*", "["].contains($0) ? "\\\($0)" : String($0) }.joined()
                html += HTMLText.escape(decoded)
            case let .element(name, attributes, children):
                switch name {
                case "br":
                    md += "\n"
                    html += "<br>"
                case "strong", "b":
                    let inner = try inline(children)
                    guard !inner.markdown.isEmpty, !inner.markdown.contains("\n") else { throw Unsupported() }
                    md += "**" + inner.markdown + "**"
                    html += "<strong>" + inner.html + "</strong>"
                case "em", "i":
                    let inner = try inline(children)
                    guard !inner.markdown.isEmpty, !inner.markdown.contains("\n") else { throw Unsupported() }
                    md += "*" + inner.markdown + "*"
                    html += "<em>" + inner.html + "</em>"
                case "code":
                    let text = try rawText(children)
                    guard !text.isEmpty, !text.contains("`"), !text.contains("\n") else { throw Unsupported() }
                    md += "`" + text + "`"
                    html += "<code>" + HTMLText.escape(text) + "</code>"
                case "a":
                    guard let href = attributes["href"].map(HTMLText.decodeEntities), !href.isEmpty, !href.contains(" "), !href.contains(")") else { throw Unsupported() }
                    let inner = try inline(children)
                    guard !inner.markdown.isEmpty, !inner.markdown.contains("\n") else { throw Unsupported() }
                    md += "[" + inner.markdown + "](" + href + ")"
                    html += "<a href=\"\(HTMLText.escape(href))\">" + inner.html + "</a>"
                default:
                    throw Unsupported()
                }
            }
        }
        return Converted(markdown: md, html: html)
    }

    private static func rawText(_ nodes: [HTMLNode]) throws -> String {
        try nodes.map { node in
            guard case let .text(value) = node else { throw Unsupported() }
            return HTMLText.decodeEntities(value)
        }.joined()
    }
}

/// A tiny HTML tree for the tags the editor knows; anything else fails the parse.
indirect enum HTMLNode: Equatable {
    case text(String)
    case element(String, [String: String], [HTMLNode])

    private static let allowed: [String: Set<String>] = [
        "p": [], "br": [], "h1": [], "h2": [], "h3": [], "strong": [], "b": [], "em": [], "i": [],
        "code": ["class"], "pre": [], "ul": [], "ol": [], "li": [], "blockquote": [], "hr": [],
        "a": ["href", "rel", "target"],
    ]
    private static let void: Set<String> = ["br", "hr"]

    var isBlankText: Bool {
        if case let .text(value) = self { return value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }
        return false
    }

    var isBreak: Bool {
        if case let .element(name, _, _) = self { return name == "br" }
        return false
    }

    var isInline: Bool {
        switch self {
        case .text: true
        case let .element(name, _, _): ["br", "strong", "b", "em", "i", "code", "a"].contains(name)
        }
    }

    static func parse(_ html: String) -> [HTMLNode]? {
        var stack: [(name: String, attributes: [String: String], children: [HTMLNode])] = [("", [:], [])]
        var index = html.startIndex
        while index < html.endIndex {
            guard let open = html[index...].firstIndex(of: "<") else {
                stack[stack.count - 1].children.append(.text(String(html[index...])))
                break
            }
            if open > index { stack[stack.count - 1].children.append(.text(String(html[index ..< open]))) }
            guard let close = html[open...].firstIndex(of: ">") else { return nil }
            var tag = html[html.index(after: open) ..< close].trimmingCharacters(in: .whitespacesAndNewlines)
            index = html.index(after: close)
            if tag.hasPrefix("/") {
                let name = tag.dropFirst().trimmingCharacters(in: .whitespaces).lowercased()
                guard stack.count > 1, stack[stack.count - 1].name == name else { return nil }
                let done = stack.removeLast()
                stack[stack.count - 1].children.append(.element(done.name, done.attributes, done.children))
                continue
            }
            var selfClosing = false
            if tag.hasSuffix("/") {
                selfClosing = true
                tag.removeLast()
            }
            let nameEnd = tag.firstIndex(where: { $0.isWhitespace }) ?? tag.endIndex
            let name = tag[..<nameEnd].lowercased()
            guard let permitted = allowed[name], let attributes = attributes(String(tag[nameEnd...])) else { return nil }
            guard attributes.keys.allSatisfy(permitted.contains) else { return nil }
            var kept = attributes
            kept["rel"] = nil
            kept["target"] = nil
            if void.contains(name) || selfClosing {
                stack[stack.count - 1].children.append(.element(name, kept, []))
            } else {
                stack.append((name, kept, []))
            }
        }
        guard stack.count == 1 else { return nil }
        return stack[0].children
    }

    private static func attributes(_ raw: String) -> [String: String]? {
        var result: [String: String] = [:]
        let pattern = #"([a-zA-Z_:][-a-zA-Z0-9_:.]*)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>`]+)))?"#
        guard let regex = try? NSRegularExpression(pattern: pattern) else { return nil }
        let ns = raw as NSString
        var covered = 0
        for match in regex.matches(in: raw, range: NSRange(location: 0, length: ns.length)) {
            let gap = ns.substring(with: NSRange(location: covered, length: match.range.location - covered))
            guard gap.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return nil }
            let key = ns.substring(with: match.range(at: 1)).lowercased()
            var value = ""
            for group in 2 ... 4 where match.range(at: group).location != NSNotFound {
                value = ns.substring(with: match.range(at: group))
            }
            result[key] = value
            covered = match.range.location + match.range.length
        }
        guard ns.substring(from: covered).trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return nil }
        return result
    }
}
