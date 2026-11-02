import SwiftUI

// Views as the sidebar lists them (contracts/api.md `/v1/views`): grouped under their
// section, each with its icon. The web's `viewSections.ts`, `ViewIcon.tsx` and `viewIcons.ts`.

/// A view as a list of views reads it: the saved views of the inbox and of Profile → Views.
protocol ListedView: Identifiable where ID == String {
    var name: String { get }
    var section: String? { get }
    var icon: ViewIcon? { get }
    var order: Int { get }
}

extension SavedView: ListedView {
    var order: Int { position ?? 0 }
}

extension AccountTicketView: ListedView {
    var order: Int { position }
}

/// One entry of a views list: a view on its own, or a section and its views.
enum ViewEntry<V: ListedView>: Identifiable {
    case view(V)
    /// `key` matches every spelling of the name; `name` is its first view's.
    case section(key: String, name: String, views: [V])

    var id: String {
        switch self {
        case let .view(view): "view:" + view.id
        case let .section(key, _, _): "section:" + key
        }
    }
}

enum ViewSections {
    /// The longest section name the API takes.
    static let maxLength = 60

    /// A section's name as typed: spaces collapsed, blank for none.
    static func clean(_ raw: String?) -> String {
        (raw ?? "").split(whereSeparator: \.isWhitespace).joined(separator: " ")
    }

    /// What views of one section share: the name in any case.
    static func key(_ raw: String?) -> String { clean(raw).lowercased() }

    /// The views as the sidebar reads them: sections and views without one in position
    /// order, a section at its first view's place, and its views by position inside it.
    static func group<V: ListedView>(_ views: [V]) -> [ViewEntry<V>] {
        var out: [ViewEntry<V>] = []
        var index: [String: Int] = [:]
        let ordered = views.enumerated().sorted { a, b in a.element.order != b.element.order ? a.element.order < b.element.order : a.offset < b.offset }.map(\.element)
        for view in ordered {
            let name = clean(view.section)
            guard !name.isEmpty else {
                out.append(.view(view))
                continue
            }
            let key = key(name)
            if let at = index[key], case let .section(_, first, list) = out[at] {
                out[at] = .section(key: key, name: first, views: list + [view])
            } else {
                index[key] = out.count
                out.append(.section(key: key, name: name, views: [view]))
            }
        }
        return out
    }

    /// The sections in use, in list order, each spelled as its first view spells it.
    static func names<V: ListedView>(_ views: [V]) -> [String] {
        group(views).compactMap { if case let .section(_, name, _) = $0 { name } else { nil } }
    }

    /// The spelling a typed section takes: an existing section's when it names one, so case never splits a section.
    static func match(_ typed: String, in existing: [String]) -> String {
        let name = clean(typed)
        return existing.first { key($0) == key(name) } ?? name
    }

    /// The sections a member folded in this list, kept per server, workspace and member.
    static func folded(key: String) -> Set<String> {
        Set(InboxDefaults.store.stringArray(forKey: key) ?? [])
    }

    static func setFolded(_ folded: Set<String>, key: String) {
        if folded.isEmpty { InboxDefaults.store.removeObject(forKey: key) } else { InboxDefaults.store.set(folded.sorted(), forKey: key) }
    }
}

/// The curated icons a view can wear (`View.icon` kind `icon`), by the API's names, drawn
/// with the closest SF Symbol. A name not here shows the default icon.
enum ViewIconCatalog {
    struct Item: Identifiable, Hashable {
        let name: String
        let symbol: String
        let words: String
        var id: String { name }
    }

    /// The default icon of a view without one.
    static let defaultSymbol = "square.stack"

    static let items: [Item] = [
        // Conversations
        Item(name: "inbox", symbol: "tray", words: "inbox queue incoming"),
        Item(name: "mail", symbol: "envelope", words: "mail email envelope"),
        Item(name: "send", symbol: "paperplane", words: "send sent outgoing reply"),
        Item(name: "archive", symbol: "archivebox", words: "archive box stored"),
        Item(name: "message-circle", symbol: "bubble.left", words: "message chat bubble conversation"),
        Item(name: "messages-square", symbol: "bubble.left.and.bubble.right", words: "messages chat conversations thread"),
        Item(name: "phone", symbol: "phone", words: "phone call voice"),
        Item(name: "at-sign", symbol: "at", words: "at mention address email"),
        Item(name: "megaphone", symbol: "megaphone", words: "megaphone announce marketing campaign"),
        Item(name: "bell", symbol: "bell", words: "bell notification alert reminder"),
        // Orders and shipping
        Item(name: "package", symbol: "shippingbox", words: "package parcel order shipment"),
        Item(name: "package-check", symbol: "shippingbox.fill", words: "package delivered shipped done"),
        Item(name: "package-x", symbol: "xmark.bin", words: "package lost damaged cancelled"),
        Item(name: "box", symbol: "cube", words: "box product item"),
        Item(name: "truck", symbol: "truck.box", words: "truck delivery shipping transport carrier"),
        Item(name: "undo-2", symbol: "arrow.uturn.backward", words: "return undo back refund"),
        Item(name: "repeat", symbol: "repeat", words: "repeat exchange swap recurring subscription"),
        Item(name: "warehouse", symbol: "building", words: "warehouse stock inventory"),
        Item(name: "map-pin", symbol: "mappin.and.ellipse", words: "map pin location address"),
        // Commerce and billing
        Item(name: "shopping-cart", symbol: "cart", words: "cart shopping order checkout"),
        Item(name: "shopping-bag", symbol: "bag", words: "bag shopping purchase"),
        Item(name: "store", symbol: "storefront", words: "store shop retail"),
        Item(name: "credit-card", symbol: "creditcard", words: "card payment billing credit"),
        Item(name: "receipt", symbol: "receipt", words: "receipt invoice bill"),
        Item(name: "banknote", symbol: "banknote", words: "banknote cash money refund"),
        Item(name: "euro", symbol: "eurosign", words: "euro money price currency"),
        Item(name: "dollar-sign", symbol: "dollarsign", words: "dollar money price currency"),
        Item(name: "badge-percent", symbol: "percent", words: "discount promo sale coupon"),
        Item(name: "tag", symbol: "tag", words: "tag label price"),
        Item(name: "gift", symbol: "gift", words: "gift present voucher"),
        // Urgency and status
        Item(name: "triangle-alert", symbol: "exclamationmark.triangle", words: "alert warning problem"),
        Item(name: "circle-alert", symbol: "exclamationmark.circle", words: "alert error issue"),
        Item(name: "shield-alert", symbol: "exclamationmark.shield", words: "shield security fraud risk"),
        Item(name: "siren", symbol: "light.beacon.max", words: "siren emergency urgent incident"),
        Item(name: "flame", symbol: "flame", words: "flame fire hot urgent"),
        Item(name: "zap", symbol: "bolt", words: "zap fast lightning quick"),
        Item(name: "clock", symbol: "clock", words: "clock time waiting sla"),
        Item(name: "hourglass", symbol: "hourglass", words: "hourglass waiting pending"),
        Item(name: "timer", symbol: "timer", words: "timer deadline due"),
        Item(name: "calendar", symbol: "calendar", words: "calendar date schedule"),
        Item(name: "circle-pause", symbol: "pause.circle", words: "pause hold on hold paused"),
        Item(name: "circle-check", symbol: "checkmark.circle", words: "check done solved resolved"),
        Item(name: "ban", symbol: "nosign", words: "ban blocked spam cancelled"),
        // People and companies
        Item(name: "user", symbol: "person", words: "user person customer contact"),
        Item(name: "users", symbol: "person.2", words: "users team people group"),
        Item(name: "user-check", symbol: "person.fill.checkmark", words: "user assigned verified"),
        Item(name: "user-plus", symbol: "person.badge.plus", words: "user new signup onboarding"),
        Item(name: "building-2", symbol: "building.2", words: "building company business enterprise b2b"),
        Item(name: "briefcase", symbol: "briefcase", words: "briefcase business work pro"),
        Item(name: "handshake", symbol: "person.2.circle", words: "handshake partner deal sales"),
        Item(name: "crown", symbol: "crown", words: "crown vip premium"),
        Item(name: "heart", symbol: "heart", words: "heart love favourite"),
        Item(name: "smile", symbol: "face.smiling", words: "smile happy satisfaction feedback"),
        Item(name: "frown", symbol: "face.dashed", words: "frown unhappy complaint"),
        Item(name: "thumbs-up", symbol: "hand.thumbsup", words: "thumbs up good positive review"),
        Item(name: "thumbs-down", symbol: "hand.thumbsdown", words: "thumbs down bad negative review"),
        // Product and tech
        Item(name: "bug", symbol: "ladybug", words: "bug defect issue engineering"),
        Item(name: "wrench", symbol: "wrench.adjustable", words: "wrench repair maintenance warranty"),
        Item(name: "settings", symbol: "gearshape", words: "settings gear configuration"),
        Item(name: "smartphone", symbol: "iphone", words: "smartphone mobile phone app"),
        Item(name: "plug", symbol: "powerplug", words: "plug integration connection"),
        Item(name: "key", symbol: "key", words: "key password access login"),
        Item(name: "lock", symbol: "lock", words: "lock private security"),
        Item(name: "lightbulb", symbol: "lightbulb", words: "lightbulb idea feature request"),
        // Other
        Item(name: "layers", symbol: "square.stack.3d.up", words: "layers view default stack"),
        Item(name: "star", symbol: "star", words: "star favourite important"),
        Item(name: "flag", symbol: "flag", words: "flag flagged report"),
        Item(name: "bookmark", symbol: "bookmark", words: "bookmark saved"),
        Item(name: "sparkles", symbol: "sparkles", words: "sparkles new ai magic"),
        Item(name: "globe", symbol: "globe", words: "globe world international website"),
        Item(name: "languages", symbol: "character.bubble", words: "languages translation international"),
        Item(name: "file-text", symbol: "doc.text", words: "file document text contract"),
        Item(name: "folder", symbol: "folder", words: "folder files"),
        Item(name: "scale", symbol: "scalemass", words: "scale legal law compliance"),
        Item(name: "graduation-cap", symbol: "graduationcap", words: "graduation school education training"),
        Item(name: "eye", symbol: "eye", words: "eye watch review monitor"),
        Item(name: "search", symbol: "magnifyingglass", words: "search find investigate"),
    ]

    private static let byName = Dictionary(uniqueKeysWithValues: items.map { ($0.name, $0) })

    static func item(_ name: String) -> Item? { byName[name] }

    /// Icons whose name or words start with each word of the query.
    static func search(_ query: String) -> [Item] {
        let terms = query.lowercased().split(whereSeparator: \.isWhitespace).map(String.init)
        guard !terms.isEmpty else { return items }
        return items.filter { item in
            let words = (item.name.replacingOccurrences(of: "-", with: " ") + " " + item.words).split(separator: " ")
            return terms.allSatisfy { term in words.contains { $0.hasPrefix(term) } }
        }
    }

    /// Whether `text` is one emoji, near enough to what the API takes; the API has the last word.
    static func isOneEmoji(_ text: String) -> Bool {
        guard !text.isEmpty, text.utf8.count <= 64, text.count == 1, let first = text.unicodeScalars.first else { return false }
        if text.unicodeScalars.contains(where: { CharacterSet.letters.contains($0) && !$0.properties.isEmoji }) { return false }
        return first.properties.isEmojiPresentation || text.unicodeScalars.count > 1 && first.properties.isEmoji
    }

    /// Whether `text` is one or two letters or digits, as a text icon takes.
    static func isShortText(_ text: String) -> Bool {
        (1 ... 2).contains(text.count) && text.allSatisfy { $0.isLetter || $0.isNumber }
    }
}

/// A view's icon: its emoji, its icon in the view's colour, or one or two characters on a
/// small tile; the default icon when it has none.
struct ViewIconMark: View {
    let icon: ViewIcon?
    var size: CGFloat = 15
    /// The default icon's colour (the row's own).
    var tint: Color = Theme.fg3

    var body: some View {
        switch icon?.kind {
        case .emoji?:
            Text(icon?.value ?? "")
                .font(.system(size: size * 0.92))
                .frame(width: size + 3, height: size + 3)
        case .text?:
            let value = icon?.value ?? ""
            let tone = Tone(label: icon?.color)
            Text(value)
                .font(ODFont.sans(size * (value.count > 1 ? 0.56 : 0.7), weight: .semibold))
                .tracking(-0.2)
                .foregroundStyle(tone.text)
                .frame(width: size + 3, height: size + 3)
                .background(tone.soft, in: RoundedRectangle(cornerRadius: 4, style: .continuous))
        case .icon?:
            let symbol = icon.flatMap { ViewIconCatalog.item($0.value)?.symbol } ?? ViewIconCatalog.defaultSymbol
            Image(systemName: symbol)
                .font(.system(size: size))
                .foregroundStyle(icon?.color == nil ? tint : Tone(label: icon?.color).solid)
        case nil:
            Image(systemName: ViewIconCatalog.defaultSymbol)
                .font(.system(size: size))
                .foregroundStyle(tint)
        }
    }
}

extension ListedView {
    /// The name to show beside the icon: a name that starts with the view's own emoji (as
    /// people wrote them before views had icons) does not show it twice.
    var displayName: String {
        guard icon?.kind == .emoji, let emoji = icon?.value, !emoji.isEmpty else { return name }
        let strip: (String) -> String = { $0.replacingOccurrences(of: "\u{FE0F}", with: "") }
        let plain = strip(name)
        let want = strip(emoji)
        guard plain.hasPrefix(want) else { return name }
        let rest = plain.dropFirst(want.count)
        guard rest.first?.isWhitespace == true else { return name }
        let trimmed = rest.trimmingCharacters(in: .whitespaces)
        return trimmed.isEmpty ? name : trimmed
    }
}
