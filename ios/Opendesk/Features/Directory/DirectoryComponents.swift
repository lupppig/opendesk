import SwiftUI
import UIKit

// Pieces the contacts and companies screens share.

// MARK: - Pictures

/// A contact's picture (round) or a company's logo (a soft square), with the
/// initials on a stable tint underneath while it loads or when there is none.
/// Reads `https` URLs, API-relative `/v1/images/…` paths and `data:` URLs (fresh uploads in the demo).
struct DirectoryAvatar: View {
    let name: String
    var url: String?
    var seed: String = ""
    var size: CGFloat = 36
    var square = false

    @Environment(AppEnvironment.self) private var env
    @State private var inline: UIImage?

    private var shape: AnyShape {
        square ? AnyShape(RoundedRectangle(cornerRadius: size * 0.26, style: .continuous)) : AnyShape(Circle())
    }

    var body: some View {
        let tone = Theme.avatarTone(for: seed.isEmpty ? name : seed)
        ZStack {
            shape.fill(tone.soft)
            shape.stroke(tone.line, lineWidth: 1)
            Text(square ? String(Format.initials(name).prefix(1)) : Format.initials(name))
                .font(ODFont.sans(size * (square ? 0.44 : 0.38), weight: .semibold))
                .foregroundStyle(tone.text)
            if let inline {
                Image(uiImage: inline).resizable().scaledToFill().clipShape(shape).transition(.opacity)
            } else if let remote {
                AsyncImage(url: remote, transaction: Transaction(animation: Motion.fade)) { phase in
                    if case let .success(image) = phase {
                        image.resizable().scaledToFill().transition(.opacity)
                    }
                }
                .clipShape(shape)
            }
        }
        .frame(width: size, height: size)
        .background(square ? Theme.surface : .clear, in: shape)
        .task(id: url) { inline = Self.decodeInline(url) }
        .accessibilityHidden(true)
    }

    private var remote: URL? {
        guard let url, !url.isEmpty, !url.hasPrefix("data:") else { return nil }
        if let absolute = URL(string: url), absolute.scheme == "https" || absolute.scheme == "http" { return absolute }
        if url.hasPrefix("/") { return URL(string: url, relativeTo: env.session.instance.apiURL)?.absoluteURL }
        return nil
    }

    private static func decodeInline(_ url: String?) -> UIImage? {
        guard let url, url.hasPrefix("data:"), let comma = url.firstIndex(of: ",") else { return nil }
        guard let data = Data(base64Encoded: String(url[url.index(after: comma)...])) else { return nil }
        return UIImage(data: data)
    }
}

// MARK: - Layout

/// Lays chips out in rows, wrapping to the next line.
struct DirectoryFlow: Layout {
    var spacing: CGFloat = 6
    var lineSpacing: CGFloat = 6
    var alignment: HorizontalAlignment = .leading

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let rows = rows(for: proposal.width ?? .infinity, subviews: subviews)
        let width = rows.map(\.width).max() ?? 0
        let height = rows.map(\.height).reduce(0, +) + CGFloat(max(rows.count - 1, 0)) * lineSpacing
        return CGSize(width: min(width, proposal.width ?? width), height: height)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var y = bounds.minY
        for row in rows(for: bounds.width, subviews: subviews) {
            var x: CGFloat = switch alignment {
            case .center: bounds.minX + (bounds.width - row.width) / 2
            case .trailing: bounds.maxX - row.width
            default: bounds.minX
            }
            for index in row.items {
                let size = subviews[index].sizeThatFits(.unspecified)
                subviews[index].place(at: CGPoint(x: x, y: y + (row.height - size.height) / 2), proposal: ProposedViewSize(size))
                x += size.width + spacing
            }
            y += row.height + lineSpacing
        }
    }

    private struct Row {
        var items: [Int] = []
        var width: CGFloat = 0
        var height: CGFloat = 0
    }

    private func rows(for maxWidth: CGFloat, subviews: Subviews) -> [Row] {
        var rows: [Row] = []
        var current = Row()
        for index in subviews.indices {
            let size = subviews[index].sizeThatFits(.unspecified)
            let added = current.items.isEmpty ? size.width : current.width + spacing + size.width
            if added > maxWidth, !current.items.isEmpty {
                rows.append(current)
                current = Row(items: [index], width: size.width, height: size.height)
            } else {
                current.items.append(index)
                current.width = added
                current.height = max(current.height, size.height)
            }
        }
        if !current.items.isEmpty { rows.append(current) }
        return rows
    }
}

// MARK: - Labels

/// A record's labels as chips, with an "Add label" chip that opens the picker.
struct DirectoryLabelsRow: View {
    let tags: [TagRef]
    var canEdit = true
    var alignment: HorizontalAlignment = .leading
    let onEdit: () -> Void

    var body: some View {
        DirectoryFlow(spacing: 6, lineSpacing: 6, alignment: alignment) {
            ForEach(tags) { tag in
                LabelChip(name: tag.name, color: tag.color)
                    .transition(.scale(scale: 0.85).combined(with: .opacity))
            }
            if canEdit {
                Button(action: onEdit) {
                    HStack(spacing: 4) {
                        Image(systemName: tags.isEmpty ? "plus" : "tag")
                            .font(.system(size: 9.5, weight: .semibold))
                        Text(tags.isEmpty ? "Add label" : "Labels")
                            .font(ODFont.sans(11.5, weight: .medium))
                    }
                    .foregroundStyle(Theme.fg3)
                    .padding(.horizontal, 8)
                    .frame(height: 21)
                    .overlay(
                        RoundedRectangle(cornerRadius: Theme.Radius.pill, style: .continuous)
                            .strokeBorder(Theme.lineStrong, style: StrokeStyle(lineWidth: 1, dash: [3, 2.5]))
                    )
                    .contentShape(Rectangle())
                }
                .buttonStyle(TapScaleStyle())
                .accessibilityLabel(tags.isEmpty ? "Add label" : "Edit labels")
            }
        }
        .motion(Motion.snappy, value: tags.map(\.id))
    }
}

// MARK: - Detail pieces

/// A small caps-free heading over a panel, with an optional trailing note or action.
struct DirectorySectionHeader<Trailing: View>: View {
    let title: String
    var count: Int?
    @ViewBuilder var trailing: () -> Trailing

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 6) {
            Text(title)
                .font(ODFont.sans(13, weight: .semibold))
                .foregroundStyle(Theme.fg2)
            if let count {
                Text("\(count)")
                    .font(ODFont.mono(11.5))
                    .monospacedDigit()
                    .foregroundStyle(Theme.fg4)
                    .contentTransition(.numericText())
            }
            Spacer(minLength: 8)
            trailing()
        }
        .padding(.horizontal, 4)
        .padding(.bottom, 2)
    }
}

extension DirectorySectionHeader where Trailing == EmptyView {
    init(title: String, count: Int? = nil) {
        self.init(title: title, count: count, trailing: { EmptyView() })
    }
}

/// One label and value line in a details panel; tappable when `action` is set.
struct DirectoryInfoRow<Value: View>: View {
    let label: String
    var symbol: String?
    var action: (() -> Void)?
    var showsChevron = false
    @ViewBuilder var value: () -> Value

    var body: some View {
        if let action {
            Button(action: action) { content }
                .buttonStyle(RowPressStyle())
        } else {
            content
        }
    }

    private var content: some View {
        HStack(spacing: 12) {
            if let symbol {
                Image(systemName: symbol)
                    .font(.system(size: 13.5))
                    .foregroundStyle(Theme.fg3)
                    .frame(width: 20)
            }
            Text(label)
                .font(ODFont.sans(14))
                .foregroundStyle(Theme.fg3)
                .lineLimit(1)
                .frame(width: symbol == nil ? 112 : 96, alignment: .leading)
            value()
                .frame(maxWidth: .infinity, alignment: .leading)
            if showsChevron { Chevron() }
        }
        .padding(.horizontal, 14)
        .frame(minHeight: 46)
        .contentShape(Rectangle())
    }
}

/// A quiet dash for an empty value.
struct DirectoryNone: View {
    var text = "None"

    var body: some View {
        Text(text).font(ODFont.sans(14)).foregroundStyle(Theme.fg4)
    }
}

/// One of the round actions under a detail header (email, call, copy, new ticket).
struct DirectoryQuickAction: View {
    let title: String
    let symbol: String
    var enabled = true
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            VStack(spacing: 6) {
                Image(systemName: symbol)
                    .font(.system(size: 16, weight: .medium))
                    .foregroundStyle(enabled ? Theme.fg : Theme.fg4)
                    .frame(height: 20)
                Text(title)
                    .font(ODFont.sans(11.5, weight: .medium))
                    .foregroundStyle(enabled ? Theme.fg2 : Theme.fg4)
                    .lineLimit(1)
            }
            .frame(maxWidth: .infinity)
            .frame(height: 62)
            .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.control + 2, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control + 2, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
            .contentShape(Rectangle())
        }
        .buttonStyle(TapScaleStyle(scale: 0.95))
        .disabled(!enabled)
    }
}

/// Three figures side by side in one panel (tickets, open, since).
struct DirectoryStats: View {
    struct Item: Identifiable {
        let label: String
        let value: String
        var tone: Tone?
        var id: String { label }
    }

    let items: [Item]

    var body: some View {
        HStack(spacing: 0) {
            ForEach(Array(items.enumerated()), id: \.element.id) { index, item in
                if index > 0 { Rectangle().fill(Theme.lineSoft).frame(width: 1).padding(.vertical, 10) }
                VStack(alignment: .leading, spacing: 3) {
                    Text(item.label)
                        .font(ODFont.sans(11.5, weight: .medium))
                        .foregroundStyle(Theme.fg3)
                        .lineLimit(1)
                    HStack(spacing: 5) {
                        if let tone = item.tone { Circle().fill(tone.solid).frame(width: 6, height: 6) }
                        Text(item.value)
                            .font(ODFont.sans(17, weight: .semibold))
                            .tracking(-0.3)
                            .monospacedDigit()
                            .foregroundStyle(Theme.fg)
                            .lineLimit(1)
                            .minimumScaleFactor(0.8)
                            .contentTransition(.numericText())
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, 14)
                .padding(.vertical, 11)
            }
        }
        .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
    }
}

/// The notes panel: the text, or a dashed invitation to add one.
struct DirectoryNotes: View {
    let notes: String
    var canEdit: Bool
    var placeholder = "Add a note your team will see on every ticket"
    let onEdit: () -> Void

    var body: some View {
        if notes.isEmpty {
            if canEdit {
                Button(action: onEdit) {
                    HStack(spacing: 9) {
                        Image(systemName: "note.text").font(.system(size: 14))
                        Text(placeholder).font(ODFont.sans(14)).multilineTextAlignment(.leading)
                        Spacer(minLength: 0)
                    }
                    .foregroundStyle(Theme.fg3)
                    .padding(14)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .overlay(
                        RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous)
                            .strokeBorder(Theme.lineStrong, style: StrokeStyle(lineWidth: 1, dash: [4, 3]))
                    )
                    .contentShape(Rectangle())
                }
                .buttonStyle(TapScaleStyle(scale: 0.98))
            } else {
                Panel(padding: 14) { DirectoryNone(text: "No notes") }
            }
        } else {
            Button(action: onEdit) {
                Text(notes)
                    .font(ODFont.sans(14.5))
                    .foregroundStyle(Theme.fg2)
                    .lineSpacing(3)
                    .multilineTextAlignment(.leading)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(14)
                    .background(Theme.note, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
                    .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
                    .contentShape(Rectangle())
            }
            .buttonStyle(TapScaleStyle(scale: 0.99))
            .disabled(!canEdit)
        }
    }
}

/// A filter in effect, shown under a list header with a way to clear it.
struct DirectoryFilterChip<Leading: View>: View {
    let title: String
    let onClear: () -> Void
    @ViewBuilder var leading: () -> Leading

    var body: some View {
        HStack(spacing: 6) {
            leading()
            Text(title)
                .font(ODFont.sans(13, weight: .medium))
                .foregroundStyle(Theme.fg)
                .lineLimit(1)
            Button(action: onClear) {
                Image(systemName: "xmark")
                    .font(.system(size: 9, weight: .bold))
                    .foregroundStyle(Theme.fg3)
                    .frame(width: 20, height: 20)
                    .background(Theme.active, in: Circle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel("Clear \(title)")
        }
        .padding(.leading, 8)
        .padding(.trailing, 4)
        .frame(height: 30)
        .glassEffect(.regular, in: Capsule())
        .transition(.scale(scale: 0.9).combined(with: .opacity))
    }
}

// MARK: - Actions

@MainActor
enum DirectoryActions {
    static func copy(_ text: String, what: String, toasts: ToastCenter) {
        UIPasteboard.general.string = text
        Haptics.success()
        toasts.show("\(what) copied", kind: .success)
    }

    static func open(_ raw: String) {
        guard let url = URL(string: raw) else { return }
        UIApplication.shared.open(url)
    }

    static func telURL(_ phone: String) -> String {
        "tel:" + phone.filter { $0.isNumber || $0 == "+" }
    }

    /// The website of a domain as a link (`https://domain`).
    static func website(_ domain: String) -> String {
        domain.hasPrefix("http") ? domain : "https://" + domain
    }
}

/// A field value as one line of text, by its definition.
enum DirectoryFieldText {
    static func text(_ value: JSONValue?, field: CustomField, members: [Member]) -> String? {
        guard let value, !value.isNull else { return nil }
        switch field.type {
        case .select:
            guard let id = value.string else { return value.displayText }
            return field.options.first { $0.id == id }?.label ?? id
        case .multiSelect:
            let ids = value.array?.compactMap(\.string) ?? []
            let labels = ids.map { id in field.options.first { $0.id == id }?.label ?? id }
            return labels.isEmpty ? nil : labels.joined(separator: ", ")
        case .checkbox:
            return value.bool == true ? "Yes" : "No"
        case .date:
            guard let raw = value.string, let date = date(raw) else { return value.displayText }
            return date.formatted(.dateTime.month(.abbreviated).day().year())
        case .user:
            guard let id = value.string else { return nil }
            return members.first { $0.id == id }?.user.name ?? "Someone"
        default:
            let text = value.displayText ?? ""
            return text.isEmpty ? nil : text
        }
    }

    static func date(_ raw: String) -> Date? {
        let formatter = DateFormatter()
        formatter.calendar = Calendar(identifier: .gregorian)
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.timeZone = .current
        formatter.dateFormat = "yyyy-MM-dd"
        return formatter.date(from: String(raw.prefix(10)))
    }

    static func string(_ date: Date) -> String {
        let formatter = DateFormatter()
        formatter.calendar = Calendar(identifier: .gregorian)
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.timeZone = .current
        formatter.dateFormat = "yyyy-MM-dd"
        return formatter.string(from: date)
    }
}
