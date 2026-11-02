import SwiftUI

/// The workspace's ticket panels in the details (contracts/integrations.md "Ticket panels"):
/// read-only facts about the ticket's customer from Shopify or a URL of the workspace's, each
/// with a refresh, asked in the reader's language.
struct TicketPanelsSection: View {
    let store: TicketStore

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            ForEach(store.panels) { panel in
                TicketPanelView(store: store, panel: panel)
            }
        }
        .task { await store.loadPanels() }
    }
}

private struct TicketPanelView: View {
    let store: TicketStore
    let panel: TicketPanel

    var body: some View {
        let data = store.panelData[panel.id]
        let loading = store.panelsLoading.contains(panel.id)
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 6) {
                SectionLabel(panel.name.isEmpty ? panel.host : panel.name)
                Spacer(minLength: 6)
                Button {
                    Haptics.tap()
                    Task { await store.loadPanel(panel.id, refresh: true) }
                } label: {
                    Group {
                        if loading && data != nil {
                            ProgressView().controlSize(.mini)
                        } else {
                            Image(systemName: "arrow.clockwise").font(.system(size: 11.5, weight: .semibold))
                        }
                    }
                    .foregroundStyle(Theme.fg3)
                    .frame(width: 28, height: 22)
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .disabled(loading)
                .accessibilityLabel("Refresh \(panel.name)")
            }
            if let error = store.panelErrors[panel.id], data == nil {
                PanelQuietNote(symbol: "exclamationmark.triangle", text: "Couldn't load it. \(error)")
            } else if let data {
                content(data)
            } else {
                Panel { HStack { ProgressView().controlSize(.small); Spacer() }.padding(14) }
            }
        }
    }

    @ViewBuilder private func content(_ data: TicketPanelData) -> some View {
        switch data.state {
        case "no_contact":
            PanelQuietNote(symbol: "person.crop.circle.badge.questionmark", text: data.message.isEmpty ? "This ticket has no customer email to look up." : data.message)
        case "error":
            PanelQuietNote(symbol: "exclamationmark.triangle", text: data.message.isEmpty ? "\(panel.name) didn't answer. Try again in a moment." : data.message)
        default:
            if data.isEmpty {
                PanelQuietNote(symbol: "tray", text: data.message.isEmpty ? "Nothing here for this customer." : data.message)
            } else if panel.isShopify {
                ShopifyPanelContent(data: data)
            } else {
                VStack(alignment: .leading, spacing: 10) {
                    ForEach(Array(data.sections.enumerated()), id: \.offset) { _, section in
                        PanelSectionView(section: section)
                    }
                    ForEach(Array(data.blocks.enumerated()), id: \.offset) { _, block in
                        PanelBlockView(block: block, scope: "\(store.ticketID)\u{1F}\(panel.id)")
                    }
                }
            }
        }
    }
}

/// An empty, missing or failed panel: one quiet line, not a card.
private struct PanelQuietNote: View {
    let symbol: String
    let text: String

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 7) {
            Image(systemName: symbol).font(.system(size: 11, weight: .medium))
            Text(text).fixedSize(horizontal: false, vertical: true)
        }
        .font(ODFont.sans(13))
        .foregroundStyle(Theme.fg3)
        .padding(.horizontal, 2)
    }
}

// MARK: - Shopify

private struct ShopifyPanelContent: View {
    let data: TicketPanelData

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            if let customer = data.customer {
                Panel {
                    PanelLinkWrap(url: customer.url) {
                        HStack(spacing: 10) {
                            VStack(alignment: .leading, spacing: 2) {
                                Text(customer.name.isEmpty ? (customer.email ?? "Customer") : customer.name)
                                    .font(ODFont.sans(14.5, weight: .semibold)).foregroundStyle(Theme.fg).lineLimit(1)
                                Text(summary(customer)).font(ODFont.sans(12.5)).foregroundStyle(Theme.fg3).lineLimit(1)
                            }
                            Spacer(minLength: 6)
                            if !customer.url.isEmpty { PanelOpenMark() }
                        }
                        .padding(.horizontal, 14)
                        .frame(minHeight: 52)
                    }
                    if !customer.tags.isEmpty {
                        Hairline(inset: 14)
                        FlowLayout(spacing: 5) {
                            ForEach(customer.tags, id: \.self) { tag in TonePill(text: tag, tone: .gray) { EmptyView() } }
                        }
                        .padding(.horizontal, 14)
                        .padding(.vertical, 10)
                    }
                }
            }
            if !data.orders.isEmpty {
                Panel {
                    ForEach(Array(data.orders.enumerated()), id: \.element.id) { index, order in
                        if index > 0 { Hairline(inset: 14) }
                        ShopifyOrderRow(order: order)
                    }
                }
            }
        }
    }

    private func summary(_ customer: PanelCustomer) -> String {
        var parts: [String] = []
        if let count = customer.ordersCount { parts.append(count == 1 ? "1 order" : "\(count) orders") }
        if let spent = customer.totalSpent, let currency = customer.currency { parts.append("\(PanelFormat.money(spent, currency)) spent") }
        if parts.isEmpty, let email = customer.email { parts.append(email) }
        return parts.joined(separator: " · ")
    }
}

private struct ShopifyOrderRow: View {
    let order: PanelOrder
    @State private var open = false

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Button {
                Haptics.tap()
                withAnimation(Motion.snappy) { open.toggle() }
            } label: {
                HStack(spacing: 8) {
                    VStack(alignment: .leading, spacing: 2) {
                        HStack(spacing: 6) {
                            Text(order.number).font(ODFont.mono(13.5)).foregroundStyle(Theme.fg)
                            if order.cancelled { TonePill(text: "Cancelled", tone: .red) { EmptyView() } }
                        }
                        Text([order.createdAt.map { Format.shortDate($0) }, PanelFormat.money(order.total, order.currency)].compactMap { $0 }.joined(separator: " · "))
                            .font(ODFont.sans(12.5)).foregroundStyle(Theme.fg3).lineLimit(1)
                    }
                    Spacer(minLength: 6)
                    if let status = order.financialStatus { TonePill(text: PanelFormat.words(status), tone: status == "paid" ? .green : status.contains("refund") ? .amber : .gray) { EmptyView() } }
                    if let status = order.fulfillmentStatus { TonePill(text: PanelFormat.words(status), tone: status == "fulfilled" ? .blue : .gray) { EmptyView() } }
                    Image(systemName: "chevron.down").font(.system(size: 10, weight: .semibold)).foregroundStyle(Theme.fg4)
                        .rotationEffect(.degrees(open ? 180 : 0))
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityHint(open ? "Hides the order" : "Shows the order")
            if open {
                VStack(alignment: .leading, spacing: 6) {
                    ForEach(Array(order.items.enumerated()), id: \.offset) { _, item in
                        HStack(alignment: .firstTextBaseline, spacing: 6) {
                            Text("\(item.quantity)×").font(ODFont.mono(12)).foregroundStyle(Theme.fg3)
                            VStack(alignment: .leading, spacing: 1) {
                                Text(item.title).font(ODFont.sans(13)).foregroundStyle(Theme.fg)
                                if let variant = item.variant, !variant.isEmpty { Text(variant).font(ODFont.sans(12)).foregroundStyle(Theme.fg3) }
                            }
                            Spacer(minLength: 6)
                            if let price = item.price { Text(PanelFormat.money(price, order.currency)).font(ODFont.sans(12.5)).foregroundStyle(Theme.fg2) }
                        }
                    }
                    if let refunded = order.refunded, (Decimal(string: refunded) ?? 0) > 0 {
                        PanelFact(label: "Refunded", value: PanelFormat.money(refunded, order.currency))
                    }
                    if !order.discountCodes.isEmpty { PanelFact(label: "Discount", value: order.discountCodes.joined(separator: ", ")) }
                    if let shipping = order.shipping, let place = [shipping.city, shipping.country].compactMap({ $0 }).joined(separator: ", ").nilIfEmpty {
                        PanelFact(label: "Ships to", value: [place, shipping.method].compactMap { $0 }.joined(separator: " · "))
                    }
                    ForEach(Array(order.tracking.enumerated()), id: \.offset) { _, tracking in
                        PanelLinkWrap(url: tracking.url ?? "") {
                            PanelFact(label: tracking.company ?? "Tracking", value: tracking.number ?? "Track", link: tracking.url != nil)
                        }
                    }
                    if let url = URL(string: order.url), url.scheme == "https" {
                        Link(destination: url) {
                            Label("Open in Shopify", systemImage: "arrow.up.right").font(ODFont.sans(12.5, weight: .medium))
                        }
                        .foregroundStyle(Theme.fg2)
                    }
                }
                .padding(.leading, 2)
                .transition(.opacity)
            }
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 11)
    }
}

// MARK: - URL panels

private struct PanelSectionView: View {
    let section: PanelSection

    var body: some View {
        Panel {
            if !section.title.isEmpty {
                Text(section.title).font(ODFont.sans(13, weight: .semibold)).foregroundStyle(Theme.fg2)
                    .padding(.horizontal, 14).padding(.top, 10).padding(.bottom, 2)
            }
            ForEach(Array(section.items.enumerated()), id: \.offset) { index, item in
                if index > 0 || !section.title.isEmpty { Hairline(inset: 14) }
                PanelLinkWrap(url: item.url ?? "") {
                    PanelFact(label: item.label, value: item.value, link: item.url != nil)
                        .padding(.horizontal, 14)
                        .frame(minHeight: 40)
                }
            }
        }
    }
}

private struct PanelBlockView: View {
    let block: PanelBlock
    /// The ticket and panel the block is in, which cards keep their folding by.
    let scope: String

    var body: some View {
        switch block {
        case let .card(card):
            PanelCardView(card: card, scope: scope)
        case let .fields(title, items):
            Panel {
                if let title, !title.isEmpty { blockTitle(title) }
                ForEach(Array(items.enumerated()), id: \.offset) { index, field in
                    if index > 0 || title?.isEmpty == false { Hairline(inset: 14) }
                    PanelFieldRow(field: field).padding(.horizontal, 14).frame(minHeight: 40)
                }
            }
        case let .list(title, items):
            Panel {
                if let title, !title.isEmpty { blockTitle(title) }
                ForEach(Array(items.enumerated()), id: \.offset) { index, row in
                    if index > 0 || title?.isEmpty == false { Hairline(inset: 14) }
                    PanelListRowView(row: row)
                }
            }
        case let .note(text, tone):
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Image(systemName: tone == .danger || tone == .warning ? "exclamationmark.triangle" : "info.circle").font(.system(size: 11.5, weight: .medium))
                Text(text).fixedSize(horizontal: false, vertical: true)
            }
            .font(ODFont.sans(13))
            .foregroundStyle(tone == .neutral ? Theme.fg2 : PanelFormat.tone(tone).text)
            .padding(10)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(tone == .neutral ? Theme.surface : PanelFormat.tone(tone).soft, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(tone == .neutral ? Theme.line : PanelFormat.tone(tone).line, lineWidth: 1))
        case .unknown:
            EmptyView()
        }
    }

    private func blockTitle(_ title: String) -> some View {
        Text(title).font(ODFont.sans(13, weight: .semibold)).foregroundStyle(Theme.fg2)
            .padding(.horizontal, 14).padding(.top, 10).padding(.bottom, 2)
    }
}

/// A card: a header with badges and links, and a body that folds away from the arrow. It
/// starts as the answer asks and stays as the agent leaves it (`PanelCardFolds`).
private struct PanelCardView: View {
    let card: PanelCard
    let scope: String

    private var open: Bool { PanelCardFolds.shared.isOpen(card, in: scope) }

    var body: some View {
        Panel {
            HStack(alignment: .top, spacing: 8) {
                PanelLinkWrap(url: card.url ?? "") {
                    VStack(alignment: .leading, spacing: 3) {
                        HStack(spacing: 5) {
                            Text(card.title).font(ODFont.sans(14.5, weight: .semibold)).foregroundStyle(Theme.fg).lineLimit(2)
                            if card.url != nil { PanelOpenMark() }
                        }
                        if let subtitle = card.subtitle, !subtitle.isEmpty {
                            Text(subtitle).font(ODFont.sans(12.5)).foregroundStyle(Theme.fg3)
                        }
                        if !card.badges.isEmpty {
                            FlowLayout(spacing: 5) {
                                ForEach(Array(card.badges.enumerated()), id: \.offset) { _, badge in PanelBadgeView(badge: badge) }
                            }
                            .padding(.top, 2)
                        }
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                }
                PanelLinkButtons(links: card.links)
                if card.hasBody {
                    Button {
                        Haptics.tap()
                        withAnimation(Motion.snappy) { PanelCardFolds.shared.set(!open, for: card, in: scope) }
                    } label: {
                        Image(systemName: "chevron.down").font(.system(size: 10.5, weight: .semibold)).foregroundStyle(Theme.fg3)
                            .rotationEffect(.degrees(open ? 180 : 0))
                            .frame(width: 26, height: 26).contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel(card.title)
                    .accessibilityValue(open ? "Open" : "Folded")
                    .accessibilityHint(open ? "Folds the card to its header" : "Shows the rest of the card")
                }
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 11)
            if open, card.hasBody {
                ForEach(Array(card.fields.enumerated()), id: \.offset) { _, field in
                    Hairline(inset: 14)
                    PanelFieldRow(field: field).padding(.horizontal, 14).frame(minHeight: 40)
                }
                ForEach(Array(card.lines.enumerated()), id: \.offset) { _, line in
                    Hairline(inset: 14)
                    PanelLinkWrap(url: line.url ?? "") {
                        HStack(alignment: .firstTextBaseline, spacing: 6) {
                            if let quantity = line.quantity { Text("\(quantity.formatted())×").font(ODFont.mono(12)).foregroundStyle(Theme.fg3) }
                            VStack(alignment: .leading, spacing: 1) {
                                Text(line.title).font(ODFont.sans(13.5)).foregroundStyle(Theme.fg)
                                if let subtitle = line.subtitle, !subtitle.isEmpty { Text(subtitle).font(ODFont.sans(12)).foregroundStyle(Theme.fg3) }
                            }
                            Spacer(minLength: 6)
                            if let value = line.value { PanelValueView(value: value) }
                        }
                        .padding(.horizontal, 14)
                        .padding(.vertical, 9)
                    }
                }
                ForEach(Array(card.footer.enumerated()), id: \.offset) { _, field in
                    Hairline(inset: 14)
                    PanelFieldRow(field: field, strong: true).padding(.horizontal, 14).frame(minHeight: 40)
                }
            }
        }
    }
}

private struct PanelListRowView: View {
    let row: PanelListRow

    var body: some View {
        HStack(spacing: 8) {
            PanelLinkWrap(url: row.url ?? "") {
                HStack(spacing: 8) {
                    VStack(alignment: .leading, spacing: 1) {
                        Text(row.title).font(ODFont.sans(13.5, weight: .medium)).foregroundStyle(Theme.fg).lineLimit(2)
                        if let subtitle = row.subtitle, !subtitle.isEmpty { Text(subtitle).font(ODFont.sans(12)).foregroundStyle(Theme.fg3).lineLimit(2) }
                    }
                    Spacer(minLength: 6)
                    if let value = row.value { PanelValueView(value: value) }
                    if let badge = row.badge { PanelBadgeView(badge: badge) }
                }
            }
            PanelLinkButtons(links: row.links)
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 9)
    }
}

private struct PanelFieldRow: View {
    let field: PanelField
    var strong = false

    var body: some View {
        PanelLinkWrap(url: field.url ?? "") {
            HStack(spacing: 8) {
                Text(field.label).font(ODFont.sans(13.5, weight: strong ? .semibold : .regular)).foregroundStyle(strong ? Theme.fg : Theme.fg2).lineLimit(1)
                Spacer(minLength: 8)
                PanelValueView(value: field.value, strong: strong)
                if field.url != nil { PanelOpenMark() }
            }
        }
    }
}

private struct PanelValueView: View {
    let value: PanelValue
    var strong = false

    var body: some View {
        switch value {
        case let .badge(badge):
            PanelBadgeView(badge: badge)
        default:
            Text(PanelFormat.text(value))
                .font(ODFont.sans(13.5, weight: strong ? .semibold : .medium))
                .foregroundStyle(Theme.fg)
                .lineLimit(2)
                .multilineTextAlignment(.trailing)
        }
    }
}

private struct PanelBadgeView: View {
    let badge: PanelBadge

    var body: some View {
        TonePill(text: badge.text, tone: PanelFormat.tone(badge.tone)) { EmptyView() }
    }
}

/// "Plan  Enterprise": a label and its value, on one line.
private struct PanelFact: View {
    let label: String
    let value: String
    var link = false

    var body: some View {
        HStack(spacing: 8) {
            Text(label).font(ODFont.sans(13)).foregroundStyle(Theme.fg3).lineLimit(1)
            Spacer(minLength: 8)
            Text(value).font(ODFont.sans(13, weight: .medium)).foregroundStyle(Theme.fg).lineLimit(1).truncationMode(.middle)
            if link { PanelOpenMark() }
        }
    }
}

/// The links of a card or row: a letter tile each, as the web shows a link without a logo.
private struct PanelLinkButtons: View {
    let links: [PanelLink]

    var body: some View {
        if !links.isEmpty {
            HStack(spacing: 4) {
                ForEach(Array(links.prefix(3).enumerated()), id: \.offset) { _, link in
                    if let url = URL(string: link.url), url.scheme == "https" {
                        Link(destination: url) {
                            Text(link.label.prefix(1).uppercased())
                                .font(ODFont.sans(11, weight: .semibold))
                                .foregroundStyle(Theme.fg2)
                                .frame(width: 24, height: 24)
                                .background(Theme.active, in: RoundedRectangle(cornerRadius: 6, style: .continuous))
                        }
                        .accessibilityLabel("Open \(link.label)")
                    }
                }
            }
        }
    }
}

private struct PanelOpenMark: View {
    var body: some View {
        Image(systemName: "arrow.up.right").font(.system(size: 9.5, weight: .semibold)).foregroundStyle(Theme.fg4)
    }
}

/// Makes its content a link when `url` is an https address; anything else stays plain.
private struct PanelLinkWrap<Content: View>: View {
    let url: String
    @ViewBuilder var content: () -> Content

    var body: some View {
        if let target = URL(string: url), target.scheme == "https" {
            Link(destination: target) { content().contentShape(Rectangle()) }
                .buttonStyle(RowPressStyle())
        } else {
            content()
        }
    }
}

enum PanelFormat {
    static func tone(_ tone: PanelTone) -> Tone {
        switch tone {
        case .neutral: .gray
        case .info: .blue
        case .success: .green
        case .warning: .amber
        case .danger: .red
        }
    }

    static func money(_ amount: String, _ currency: String) -> String {
        guard let value = Decimal(string: amount), !currency.isEmpty else { return currency.isEmpty ? amount : "\(amount) \(currency)" }
        return value.formatted(.currency(code: currency))
    }

    /// "partially_refunded" → "Partially refunded".
    static func words(_ raw: String) -> String {
        raw.replacingOccurrences(of: "_", with: " ").capitalizedFirst
    }

    static func text(_ value: PanelValue) -> String {
        switch value {
        case let .text(text): text
        case let .money(amount, currency): money(amount, currency)
        case let .badge(badge): badge.text
        case let .date(raw, relative):
            if raw.count == 10, let day = TicketFieldValue.day(raw) {
                // A day as it is, never shifted by the reader's time zone.
                day.formatted(.dateTime.month(.abbreviated).day().year())
            } else if let date = instant(raw) {
                relative ? Format.relLong(date) : Format.fullDate(date)
            } else {
                raw
            }
        }
    }

    private static func instant(_ raw: String) -> Date? {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let date = formatter.date(from: raw) { return date }
        formatter.formatOptions = [.withInternetDateTime]
        return formatter.date(from: raw)
    }
}

private extension String {
    var nilIfEmpty: String? { isEmpty ? nil : self }
}
