import SwiftUI
import UIKit

/// The light markdown Ollie writes, drawn natively (never as HTML): headings,
/// paragraphs, `-` and `1.` lists (nested by indent), quotes, rules, fenced
/// code, simple pipe tables, and inline **bold**, *italic*, ~~strike~~,
/// `code` and [links](https://…). `#123` becomes a ticket link
/// (`MarkdownText.ticketURL`), and an app path such as `/inbox/mine` a link
/// into the app (`MarkdownText.appURL`); the screen answers both through
/// `openURL`. Only http(s), mailto and app paths stay links.
struct MarkdownText: View {
    let text: String
    var size: CGFloat = 15.5
    var color: Color = Theme.fg
    /// A soft caret after the last word while the text is still arriving.
    var caret = false

    nonisolated static let scheme = "opendesk-ref"

    nonisolated static func ticketURL(_ number: Int) -> URL? { URL(string: "\(scheme)://ticket/\(number)") }

    nonisolated static func appURL(_ path: String) -> URL? {
        var components = URLComponents()
        components.scheme = scheme
        components.host = "app"
        components.queryItems = [URLQueryItem(name: "path", value: path)]
        return components.url
    }

    var body: some View {
        let blocks = MarkdownParser.blocks(text)
        VStack(alignment: .leading, spacing: size * 0.62) {
            ForEach(Array(blocks.enumerated()), id: \.offset) { index, block in
                view(for: block, last: index == blocks.count - 1)
            }
            if blocks.isEmpty, caret {
                Text(caretRun)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private var style: MarkdownInline.Style { MarkdownInline.Style(size: size, color: color) }

    private var caretRun: AttributedString {
        var run = AttributedString(" \u{25CF}")
        run[MDFont.self] = .system(size: size * 0.55)
        run[MDColor.self] = Tone.violet.solid
        return run
    }

    private func line(_ source: String, weight: Font.Weight = .regular, size override: CGFloat? = nil, color tint: Color? = nil, withCaret: Bool) -> Text {
        var style = style
        if let override { style.size = override }
        if let tint { style.color = tint }
        style.weight = weight
        var attributed = MarkdownInline.attributed(source, style: style)
        if withCaret { attributed.append(caretRun) }
        return Text(attributed)
    }

    @ViewBuilder
    private func view(for block: MarkdownParser.Block, last: Bool) -> some View {
        switch block {
        case let .heading(level, text):
            line(text, weight: .semibold, size: size + (level == 1 ? 3 : level == 2 ? 1.5 : 0), withCaret: caret && last)
                .tracking(-0.2)
                .padding(.top, level <= 2 ? 4 : 2)
                .fixedSize(horizontal: false, vertical: true)
        case let .paragraph(text):
            line(text, withCaret: caret && last)
                .lineSpacing(size * 0.22)
                .fixedSize(horizontal: false, vertical: true)
        case let .list(items):
            VStack(alignment: .leading, spacing: size * 0.32) {
                ForEach(Array(items.enumerated()), id: \.offset) { index, item in
                    HStack(alignment: .firstTextBaseline, spacing: 8) {
                        marker(item)
                        line(item.text, withCaret: caret && last && index == items.count - 1)
                            .lineSpacing(size * 0.2)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    .padding(.leading, CGFloat(item.level) * 18)
                }
            }
        case let .quote(text):
            HStack(alignment: .top, spacing: 10) {
                RoundedRectangle(cornerRadius: 1).fill(Theme.lineStrong).frame(width: 2.5)
                line(text, color: Theme.fg2, withCaret: caret && last)
                    .lineSpacing(size * 0.2)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .fixedSize(horizontal: false, vertical: true)
        case .rule:
            Rectangle().fill(Theme.line).frame(height: 1).padding(.vertical, 4)
        case let .code(language, code):
            MarkdownCodeBlock(language: language, code: code, size: size)
        case let .table(header, rows):
            MarkdownTable(header: header, rows: rows, style: style)
        }
    }

    @ViewBuilder
    private func marker(_ item: MarkdownParser.Item) -> some View {
        if let number = item.number {
            Text("\(number).")
                .font(ODFont.mono(size * 0.86, weight: .medium))
                .foregroundStyle(Theme.fg3)
                .frame(minWidth: size * 1.1, alignment: .trailing)
        } else {
            Circle()
                .fill(item.level == 0 ? Theme.fg3 : Theme.fg4)
                .frame(width: size * 0.32, height: size * 0.32)
                .frame(width: size * 0.7)
                .alignmentGuide(.firstTextBaseline) { d in d[.bottom] + size * 0.2 }
        }
    }
}

// MARK: - Blocks

enum MarkdownParser {
    struct Item: Hashable {
        var text: String
        var level: Int
        var number: Int?
    }

    enum Block: Hashable {
        case heading(Int, String)
        case paragraph(String)
        case list([Item])
        case quote(String)
        case rule
        case code(String?, String)
        case table([String], [[String]])
    }

    static func blocks(_ markdown: String) -> [Block] {
        var blocks: [Block] = []
        var paragraph: [String] = []
        var quote: [String] = []
        let lines = markdown.replacingOccurrences(of: "\r\n", with: "\n").components(separatedBy: "\n")

        func flush() {
            if !paragraph.isEmpty { blocks.append(.paragraph(paragraph.joined(separator: "\n"))) }
            paragraph = []
            if !quote.isEmpty { blocks.append(.quote(quote.joined(separator: "\n"))) }
            quote = []
        }

        var index = 0
        while index < lines.count {
            let raw = lines[index]
            let line = raw.trimmingCharacters(in: .whitespaces)
            index += 1

            if line.hasPrefix("```") || line.hasPrefix("~~~") {
                flush()
                let fence = String(line.prefix(3))
                let language = line.dropFirst(3).trimmingCharacters(in: .whitespaces)
                var code: [String] = []
                while index < lines.count, !lines[index].trimmingCharacters(in: .whitespaces).hasPrefix(fence) {
                    code.append(lines[index])
                    index += 1
                }
                index += 1
                blocks.append(.code(language.isEmpty ? nil : language, code.joined(separator: "\n")))
                continue
            }
            if line.isEmpty {
                flush()
                continue
            }
            if let heading = line.firstMatch(of: #/^(#{1,6})\s+(.+?)\s*#*$/#) {
                flush()
                blocks.append(.heading(heading.1.count, String(heading.2)))
                continue
            }
            if line.wholeMatch(of: #/(?:[-*_]\s*){3,}/#) != nil, paragraph.isEmpty || !line.hasPrefix("-") {
                flush()
                blocks.append(.rule)
                continue
            }
            if line.hasPrefix(">") {
                if !paragraph.isEmpty { flush() }
                quote.append(String(line.dropFirst()).trimmingCharacters(in: .whitespaces))
                continue
            }
            if line.hasPrefix("|"), index < lines.count, isTableSeparator(lines[index]) {
                flush()
                let header = cells(line)
                index += 1
                var rows: [[String]] = []
                while index < lines.count, lines[index].trimmingCharacters(in: .whitespaces).hasPrefix("|") {
                    rows.append(cells(lines[index]))
                    index += 1
                }
                blocks.append(.table(header, rows))
                continue
            }
            let indent = raw.prefix { $0 == " " || $0 == "\t" }.reduce(0) { $0 + ($1 == "\t" ? 4 : 1) }
            if let bullet = line.firstMatch(of: #/^[-*+\u{2022}]\s+(.*)$/#) {
                if !paragraph.isEmpty || !quote.isEmpty { flush() }
                append(Item(text: String(bullet.1), level: min(indent / 2, 3), number: nil), to: &blocks)
                continue
            }
            if let numbered = line.firstMatch(of: #/^(\d{1,4})[.)]\s+(.*)$/#) {
                if !paragraph.isEmpty || !quote.isEmpty { flush() }
                append(Item(text: String(numbered.2), level: min(indent / 2, 3), number: Int(numbered.1)), to: &blocks)
                continue
            }
            // An indented line under a list item continues it.
            if paragraph.isEmpty, indent >= 2, case var .list(items)? = blocks.last, !items.isEmpty {
                items[items.count - 1].text += " " + line
                blocks[blocks.count - 1] = .list(items)
                continue
            }
            if !quote.isEmpty { flush() }
            paragraph.append(line)
        }
        flush()
        return blocks
    }

    private static func append(_ item: Item, to blocks: inout [Block]) {
        if case var .list(items)? = blocks.last {
            items.append(item)
            blocks[blocks.count - 1] = .list(items)
        } else {
            blocks.append(.list([item]))
        }
    }

    private static func isTableSeparator(_ line: String) -> Bool {
        let trimmed = line.trimmingCharacters(in: .whitespaces)
        return trimmed.hasPrefix("|") && trimmed.contains("-") && trimmed.allSatisfy { "|-: ".contains($0) }
    }

    private static func cells(_ line: String) -> [String] {
        var trimmed = line.trimmingCharacters(in: .whitespaces)
        if trimmed.hasPrefix("|") { trimmed.removeFirst() }
        if trimmed.hasSuffix("|") { trimmed.removeLast() }
        return trimmed.components(separatedBy: "|").map { $0.trimmingCharacters(in: .whitespaces) }
    }
}

// MARK: - Inline

// Attributes by type, so no key path crosses an isolation boundary.
private typealias MDFont = AttributeScopes.SwiftUIAttributes.FontAttribute
private typealias MDColor = AttributeScopes.SwiftUIAttributes.ForegroundColorAttribute
private typealias MDBackground = AttributeScopes.SwiftUIAttributes.BackgroundColorAttribute
private typealias MDStrike = AttributeScopes.SwiftUIAttributes.StrikethroughStyleAttribute
private typealias MDUnderline = AttributeScopes.SwiftUIAttributes.UnderlineStyleAttribute
private typealias MDLink = AttributeScopes.FoundationAttributes.LinkAttribute
private typealias MDIntent = AttributeScopes.FoundationAttributes.InlinePresentationIntentAttribute

enum MarkdownInline {
    struct Style {
        var size: CGFloat
        var color: Color
        var weight: Font.Weight = .regular
    }

    /// One block's inline markdown as styled text.
    static func attributed(_ source: String, style: Style) -> AttributedString {
        let options = AttributedString.MarkdownParsingOptions(allowsExtendedAttributes: false, interpretedSyntax: .inlineOnlyPreservingWhitespace, failurePolicy: .returnPartiallyParsedIfPossible)
        var attributed = (try? AttributedString(markdown: linkTickets(source), options: options)) ?? AttributedString(source)
        attributed[MDFont.self] = ODFont.sans(style.size, weight: style.weight)
        attributed[MDColor.self] = style.color

        let runs = attributed.runs.map { ($0.range, $0[MDIntent.self], $0[MDLink.self]) }
        for (range, intent, link) in runs {
            let strong = intent?.contains(.stronglyEmphasized) == true
            let emphasized = intent?.contains(.emphasized) == true
            var font = ODFont.sans(style.size, weight: strong ? .semibold : style.weight)
            if emphasized { font = font.italic() }
            attributed[range][MDFont.self] = font
            if strong { attributed[range][MDColor.self] = Theme.fg }
            if intent?.contains(.strikethrough) == true {
                attributed[range][MDStrike.self] = Text.LineStyle.single
                attributed[range][MDColor.self] = Theme.fg3
            }
            if intent?.contains(.code) == true {
                attributed[range][MDFont.self] = ODFont.mono(style.size * 0.88)
                attributed[range][MDBackground.self] = Theme.active
                attributed[range][MDColor.self] = Theme.fg
            }
            guard let link else { continue }
            if link.scheme == MarkdownText.scheme, link.host() == "ticket" {
                attributed[range][MDFont.self] = ODFont.mono(style.size * 0.9, weight: .medium)
                attributed[range][MDColor.self] = Tone.violet.text
                attributed[range][MDBackground.self] = Tone.violet.soft
            } else if let safe = safeLink(link) {
                attributed[range][MDLink.self] = safe
                attributed[range][MDColor.self] = Theme.fg
                attributed[range][MDUnderline.self] = Text.LineStyle.single
            } else {
                attributed[range][MDLink.self] = nil
            }
        }
        return attributed
    }

    /// Only http(s), mailto and app paths stay links; an app path opens in the app.
    static func safeLink(_ url: URL) -> URL? {
        switch url.scheme?.lowercased() {
        case "https", "http", "mailto": return url
        case MarkdownText.scheme: return url
        case nil:
            let path = url.relativeString
            guard path.hasPrefix("/"), !path.hasPrefix("//"), !path.hasPrefix("/\\") else { return nil }
            return MarkdownText.appURL(path)
        default: return nil
        }
    }

    /// `#123` outside code and links becomes a ticket link.
    static func linkTickets(_ source: String) -> String {
        guard source.contains("#") else { return source }
        var out = ""
        var last = source.startIndex
        for match in source.matches(of: #/`[^`\n]+`|\[[^\]\n]*\]\([^)\s]*\)|#(\d{1,9})\b/#) {
            out += source[last ..< match.range.lowerBound]
            // "#123" only on its own: not inside a word, an entity or an anchor.
            let before = match.range.lowerBound > source.startIndex ? source[source.index(before: match.range.lowerBound)] : " "
            let standalone = !(before.isLetter || before.isNumber || "_&/#".contains(before))
            if let number = match.output.1, standalone, let url = MarkdownText.ticketURL(Int(number) ?? 0) {
                out += "[#\(number)](\(url.absoluteString))"
            } else {
                out += source[match.range]
            }
            last = match.range.upperBound
        }
        out += source[last...]
        return out
    }
}

// MARK: - Code and tables

private struct MarkdownCodeBlock: View {
    let language: String?
    let code: String
    let size: CGFloat
    @State private var copied = false

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text(language ?? "code")
                    .font(ODFont.mono(11, weight: .medium))
                    .foregroundStyle(Theme.fg4)
                Spacer()
                Button {
                    UIPasteboard.general.string = code
                    Haptics.selection()
                    copied = true
                    Task {
                        try? await Task.sleep(for: .seconds(1.6))
                        copied = false
                    }
                } label: {
                    Image(systemName: copied ? "checkmark" : "doc.on.doc")
                        .font(.system(size: 11.5, weight: .medium))
                        .foregroundStyle(copied ? Tone.green.text : Theme.fg3)
                        .frame(width: 28, height: 24)
                        .contentShape(Rectangle())
                        .contentTransition(.symbolEffect(.replace))
                }
                .buttonStyle(.plain)
                .accessibilityLabel(copied ? "Copied" : "Copy code")
            }
            .padding(.leading, 12)
            .padding(.trailing, 4)
            .padding(.top, 4)
            ScrollView(.horizontal, showsIndicators: false) {
                Text(code)
                    .font(ODFont.mono(size * 0.8))
                    .foregroundStyle(Theme.fg)
                    .lineSpacing(3)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: true, vertical: true)
                    .padding(.horizontal, 12)
                    .padding(.bottom, 11)
                    .padding(.top, 2)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Theme.sunken, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
    }
}

private struct MarkdownTable: View {
    let header: [String]
    let rows: [[String]]
    let style: MarkdownInline.Style

    private var columns: Int { max(header.count, rows.map(\.count).max() ?? 0) }

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            Grid(alignment: .leading, horizontalSpacing: 0, verticalSpacing: 0) {
                GridRow {
                    ForEach(0 ..< columns, id: \.self) { column in
                        cell(column < header.count ? header[column] : "", heading: true, column: column)
                    }
                }
                .background(Theme.sunken)
                ForEach(Array(rows.enumerated()), id: \.offset) { _, row in
                    Divider().overlay(Theme.lineSoft).gridCellUnsizedAxes(.horizontal)
                    GridRow {
                        ForEach(0 ..< columns, id: \.self) { column in
                            cell(column < row.count ? row[column] : "", heading: false, column: column)
                        }
                    }
                }
            }
            .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
            .clipShape(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
        }
    }

    private func cell(_ text: String, heading: Bool, column: Int) -> some View {
        var cellStyle = style
        cellStyle.size = style.size * 0.88
        cellStyle.weight = heading ? .medium : .regular
        cellStyle.color = heading ? Theme.fg2 : Theme.fg
        return Text(MarkdownInline.attributed(text, style: cellStyle))
            .monospacedDigit()
            .lineLimit(3)
            .frame(minWidth: 64, maxWidth: 220, alignment: .leading)
            .padding(.horizontal, 10)
            .padding(.vertical, 7)
    }
}
