import Foundation

// Ticket panels (contracts/integrations.md "Ticket panels"): what another product knows
// about the ticket's contact, read-only. A Shopify panel answers a customer and orders, a
// URL of the workspace's own answers sections (version 1) or blocks (version 2), already
// cleaned by the server; the app only renders them. Everything decodes leniently, and a
// block or value this version doesn't know is left out.

/// `GET /v1/ticket-panels` items, the part a ticket needs.
struct TicketPanel: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var kind: String
    var name: String
    var host: String
    var enabled: Bool
    var position: Int

    enum CodingKeys: String, CodingKey { case id, kind, name, host, enabled, position }

    init(id: String, kind: String, name: String, host: String, enabled: Bool = true, position: Int = 0) {
        self.id = id
        self.kind = kind
        self.name = name
        self.host = host
        self.enabled = enabled
        self.position = position
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        kind = c.lenient(String.self, forKey: .kind) ?? "http"
        name = c.lenient(String.self, forKey: .name) ?? ""
        host = c.lenient(String.self, forKey: .host) ?? ""
        enabled = c.lenient(Bool.self, forKey: .enabled) ?? true
        position = c.lenient(Int.self, forKey: .position) ?? 0
    }

    var isShopify: Bool { kind == "shopify" }
}

/// `GET /v1/tickets/{id}/panels/{panelId}`.
struct TicketPanelData: Decodable, Sendable {
    var panelID: String
    /// "ok", "empty", "no_contact" or "error".
    var state: String
    var message: String
    var customer: PanelCustomer?
    var orders: [PanelOrder]
    var sections: [PanelSection]
    var blocks: [PanelBlock]
    var fetchedAt: Date?

    enum CodingKeys: String, CodingKey {
        case state, message, customer, orders, sections, blocks
        case panelID = "panel_id"
        case fetchedAt = "fetched_at"
    }

    init(panelID: String, state: String, message: String = "", customer: PanelCustomer? = nil, orders: [PanelOrder] = [], sections: [PanelSection] = [], blocks: [PanelBlock] = []) {
        self.panelID = panelID
        self.state = state
        self.message = message
        self.customer = customer
        self.orders = orders
        self.sections = sections
        self.blocks = blocks
        fetchedAt = nil
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        panelID = c.lenient(String.self, forKey: .panelID) ?? ""
        state = c.lenient(String.self, forKey: .state) ?? "error"
        message = c.lenient(String.self, forKey: .message) ?? ""
        customer = c.lenient(PanelCustomer.self, forKey: .customer)
        orders = c.lenient(Lossy<PanelOrder>.self, forKey: .orders)?.items ?? []
        sections = c.lenient(Lossy<PanelSection>.self, forKey: .sections)?.items ?? []
        blocks = (c.lenient(Lossy<PanelBlock>.self, forKey: .blocks)?.items ?? []).filter { $0 != .unknown }
        fetchedAt = c.lenient(Date.self, forKey: .fetchedAt)
    }

    var isEmpty: Bool { customer == nil && orders.isEmpty && sections.isEmpty && blocks.isEmpty }
}

/// A list that drops the items it can't read instead of failing whole.
struct Lossy<Item: Decodable>: Decodable {
    var items: [Item]

    private struct Skip: Decodable {}

    init(from decoder: Decoder) throws {
        var c = try decoder.unkeyedContainer()
        var items: [Item] = []
        while !c.isAtEnd {
            if let item = try? c.decode(Item.self) { items.append(item) } else { _ = try? c.decode(Skip.self) }
        }
        self.items = items
    }
}

// MARK: - Shopify

struct PanelCustomer: Decodable, Hashable, Sendable {
    var name: String
    var url: String
    var email: String?
    var ordersCount: Int?
    var totalSpent: String?
    var currency: String?
    var tags: [String]

    enum CodingKeys: String, CodingKey {
        case name, url, email, currency, tags
        case ordersCount = "orders_count"
        case totalSpent = "total_spent"
    }

    init(name: String, url: String, email: String? = nil, ordersCount: Int? = nil, totalSpent: String? = nil, currency: String? = nil, tags: [String] = []) {
        self.name = name
        self.url = url
        self.email = email
        self.ordersCount = ordersCount
        self.totalSpent = totalSpent
        self.currency = currency
        self.tags = tags
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        name = c.lenient(String.self, forKey: .name) ?? ""
        url = c.lenient(String.self, forKey: .url) ?? ""
        email = c.lenient(String.self, forKey: .email)
        ordersCount = c.lenient(Int.self, forKey: .ordersCount)
        totalSpent = PanelDecimal.read(c, .totalSpent)
        currency = c.lenient(String.self, forKey: .currency)
        tags = c.lenient([String].self, forKey: .tags) ?? []
    }
}

struct PanelOrder: Decodable, Hashable, Sendable, Identifiable {
    struct Item: Decodable, Hashable, Sendable {
        var title: String
        var variant: String?
        var quantity: Int
        var price: String?

        enum CodingKeys: String, CodingKey { case title, variant, quantity, price }

        init(title: String, variant: String? = nil, quantity: Int, price: String? = nil) {
            self.title = title
            self.variant = variant
            self.quantity = quantity
            self.price = price
        }

        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            title = c.lenient(String.self, forKey: .title) ?? ""
            variant = c.lenient(String.self, forKey: .variant)
            quantity = c.lenient(Int.self, forKey: .quantity) ?? 1
            price = PanelDecimal.read(c, .price)
        }
    }

    struct Shipping: Decodable, Hashable, Sendable {
        var city: String?
        var country: String?
        var method: String?
    }

    struct Tracking: Decodable, Hashable, Sendable {
        var company: String?
        var number: String?
        var url: String?
    }

    var id: String
    var number: String
    var url: String
    var createdAt: Date?
    var total: String
    var currency: String
    var financialStatus: String?
    var fulfillmentStatus: String?
    var cancelled: Bool
    var refunded: String?
    var discountCodes: [String]
    var shipping: Shipping?
    var items: [Item]
    var tracking: [Tracking]

    enum CodingKeys: String, CodingKey {
        case id, number, url, total, currency, cancelled, refunded, shipping, items, tracking
        case createdAt = "created_at"
        case financialStatus = "financial_status"
        case fulfillmentStatus = "fulfillment_status"
        case discountCodes = "discount_codes"
    }

    init(id: String, number: String, url: String, createdAt: Date?, total: String, currency: String, financialStatus: String? = nil, fulfillmentStatus: String? = nil,
         cancelled: Bool = false, refunded: String? = nil, discountCodes: [String] = [], shipping: Shipping? = nil, items: [Item] = [], tracking: [Tracking] = []) {
        self.id = id
        self.number = number
        self.url = url
        self.createdAt = createdAt
        self.total = total
        self.currency = currency
        self.financialStatus = financialStatus
        self.fulfillmentStatus = fulfillmentStatus
        self.cancelled = cancelled
        self.refunded = refunded
        self.discountCodes = discountCodes
        self.shipping = shipping
        self.items = items
        self.tracking = tracking
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        number = c.lenient(String.self, forKey: .number) ?? ""
        url = c.lenient(String.self, forKey: .url) ?? ""
        createdAt = c.lenient(Date.self, forKey: .createdAt)
        total = PanelDecimal.read(c, .total) ?? "0"
        currency = c.lenient(String.self, forKey: .currency) ?? ""
        financialStatus = c.lenient(String.self, forKey: .financialStatus)
        fulfillmentStatus = c.lenient(String.self, forKey: .fulfillmentStatus)
        cancelled = c.lenient(Bool.self, forKey: .cancelled) ?? false
        refunded = PanelDecimal.read(c, .refunded)
        discountCodes = c.lenient([String].self, forKey: .discountCodes) ?? []
        shipping = c.lenient(Shipping.self, forKey: .shipping)
        items = c.lenient(Lossy<Item>.self, forKey: .items)?.items ?? []
        tracking = c.lenient(Lossy<Tracking>.self, forKey: .tracking)?.items ?? []
    }
}

/// A decimal the server writes as a string, read from a number too.
enum PanelDecimal {
    static func read<K: CodingKey>(_ c: KeyedDecodingContainer<K>, _ key: K) -> String? {
        if let text = c.lenient(String.self, forKey: key) { return text }
        if let number = c.lenient(Double.self, forKey: key) { return String(number) }
        return nil
    }
}

// MARK: - URL panels

/// A version 1 answer's section: labelled values.
struct PanelSection: Decodable, Hashable, Sendable {
    struct Item: Decodable, Hashable, Sendable {
        var label: String
        var value: String
        var url: String?

        enum CodingKeys: String, CodingKey { case label, value, url }

        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            label = c.lenient(String.self, forKey: .label) ?? ""
            value = c.lenient(String.self, forKey: .value) ?? c.lenient(Double.self, forKey: .value).map { String($0) } ?? c.lenient(Bool.self, forKey: .value).map { $0 ? "Yes" : "No" } ?? ""
            url = c.lenient(String.self, forKey: .url)
        }
    }

    var title: String
    var items: [Item]

    enum CodingKeys: String, CodingKey { case title, items }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        title = c.lenient(String.self, forKey: .title) ?? ""
        items = c.lenient(Lossy<Item>.self, forKey: .items)?.items ?? []
    }
}

enum PanelTone: String, LenientEnum {
    case neutral, info, success, warning, danger
    static let fallback = PanelTone.neutral
}

struct PanelBadge: Decodable, Hashable, Sendable {
    var text: String
    var tone: PanelTone

    enum CodingKeys: String, CodingKey { case text, tone }

    init(text: String, tone: PanelTone) {
        self.text = text
        self.tone = tone
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        text = c.lenient(String.self, forKey: .text) ?? ""
        tone = c.lenient(PanelTone.self, forKey: .tone) ?? .neutral
    }
}

/// A value the app formats: text, a date, money or a badge.
enum PanelValue: Decodable, Hashable, Sendable {
    case text(String)
    /// `YYYY-MM-DD` (a day as it is) or an RFC 3339 time.
    case date(String, relative: Bool)
    case money(amount: String, currency: String)
    case badge(PanelBadge)

    private enum CodingKeys: String, CodingKey { case type, text, value, relative, amount, currency, tone }

    init(from decoder: Decoder) throws {
        if let single = try? decoder.singleValueContainer() {
            if let text = try? single.decode(String.self) { self = .text(text); return }
            if let flag = try? single.decode(Bool.self) { self = .text(flag ? "Yes" : "No"); return }
            if let number = try? single.decode(Double.self) { self = .text(number.formatted()); return }
        }
        let c = try decoder.container(keyedBy: CodingKeys.self)
        switch c.lenient(String.self, forKey: .type) {
        case "text"?:
            self = .text(c.lenient(String.self, forKey: .text) ?? "")
        case "date"?:
            guard let raw = c.lenient(String.self, forKey: .value) else { throw DecodingError.dataCorrupted(.init(codingPath: c.codingPath, debugDescription: "date")) }
            self = .date(raw, relative: c.lenient(Bool.self, forKey: .relative) ?? false)
        case "money"?:
            guard let amount = PanelDecimal.read(c, .amount), let currency = c.lenient(String.self, forKey: .currency) else {
                throw DecodingError.dataCorrupted(.init(codingPath: c.codingPath, debugDescription: "money"))
            }
            self = .money(amount: amount, currency: currency)
        case "badge"?:
            self = .badge(PanelBadge(text: c.lenient(String.self, forKey: .text) ?? "", tone: c.lenient(PanelTone.self, forKey: .tone) ?? .neutral))
        default:
            throw DecodingError.dataCorrupted(.init(codingPath: c.codingPath, debugDescription: "value"))
        }
    }
}

struct PanelField: Decodable, Hashable, Sendable {
    var label: String
    var value: PanelValue
    var url: String?
}

struct PanelLink: Decodable, Hashable, Sendable {
    var label: String
    var url: String
    var icon: String?
}

struct PanelLine: Decodable, Hashable, Sendable {
    var quantity: Double?
    var title: String
    var subtitle: String?
    var value: PanelValue?
    var url: String?

    enum CodingKeys: String, CodingKey { case quantity, title, subtitle, value, url }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        quantity = c.lenient(Double.self, forKey: .quantity)
        title = try c.decode(String.self, forKey: .title)
        subtitle = c.lenient(String.self, forKey: .subtitle)
        value = c.lenient(PanelValue.self, forKey: .value)
        url = c.lenient(String.self, forKey: .url)
    }
}

struct PanelListRow: Decodable, Hashable, Sendable {
    var title: String
    var url: String?
    var subtitle: String?
    var value: PanelValue?
    var badge: PanelBadge?
    var links: [PanelLink]

    enum CodingKeys: String, CodingKey { case title, url, subtitle, value, badge, links }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        title = try c.decode(String.self, forKey: .title)
        url = c.lenient(String.self, forKey: .url)
        subtitle = c.lenient(String.self, forKey: .subtitle)
        value = c.lenient(PanelValue.self, forKey: .value)
        badge = c.lenient(PanelBadge.self, forKey: .badge)
        links = c.lenient(Lossy<PanelLink>.self, forKey: .links)?.items ?? []
    }
}

struct PanelCard: Hashable, Sendable {
    /// The record across answers, up to 80 characters; nil without one.
    var id: String?
    var title: String
    var url: String?
    var subtitle: String?
    var badges: [PanelBadge]
    var links: [PanelLink]
    var collapsed: Bool
    var fields: [PanelField]
    var lines: [PanelLine]
    var footer: [PanelField]

    /// Something under the header to fold away.
    var hasBody: Bool { !fields.isEmpty || !lines.isEmpty || !footer.isEmpty }
}

/// A version 2 answer's block.
enum PanelBlock: Decodable, Hashable, Sendable {
    case card(PanelCard)
    case fields(title: String?, items: [PanelField])
    case list(title: String?, items: [PanelListRow])
    case note(text: String, tone: PanelTone)
    case unknown

    private enum CodingKeys: String, CodingKey { case type, id, title, url, subtitle, badges, links, collapsed, fields, lines, footer, items, text, tone }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        let title = c.lenient(String.self, forKey: .title)
        switch c.lenient(String.self, forKey: .type) {
        case "card"?:
            let id = c.lenient(String.self, forKey: .id) ?? c.lenient(Int.self, forKey: .id).map(String.init)
            self = .card(PanelCard(
                id: id.flatMap { !$0.isEmpty && $0.count <= 80 ? $0 : nil },
                title: title ?? "", url: c.lenient(String.self, forKey: .url), subtitle: c.lenient(String.self, forKey: .subtitle),
                badges: c.lenient(Lossy<PanelBadge>.self, forKey: .badges)?.items ?? [],
                links: c.lenient(Lossy<PanelLink>.self, forKey: .links)?.items ?? [],
                collapsed: c.lenient(Bool.self, forKey: .collapsed) == true,
                fields: c.lenient(Lossy<PanelField>.self, forKey: .fields)?.items ?? [],
                lines: c.lenient(Lossy<PanelLine>.self, forKey: .lines)?.items ?? [],
                footer: c.lenient(Lossy<PanelField>.self, forKey: .footer)?.items ?? []
            ))
        case "fields"?:
            self = .fields(title: title, items: c.lenient(Lossy<PanelField>.self, forKey: .items)?.items ?? [])
        case "list"?:
            self = .list(title: title, items: c.lenient(Lossy<PanelListRow>.self, forKey: .items)?.items ?? [])
        case "note"?:
            self = .note(text: c.lenient(String.self, forKey: .text) ?? "", tone: c.lenient(PanelTone.self, forKey: .tone) ?? .neutral)
        default:
            self = .unknown
        }
    }
}
