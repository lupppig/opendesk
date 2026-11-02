import Foundation

// The team's internal wiki (contracts/wiki.md): `WikiNode`, `WikiRef`, `WikiPage`,
// `WikiRevision` and `WikiSearchHit`. Keys are copied through explicit CodingKeys.

/// A page in the tree, without its body.
struct WikiNode: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var parentID: String?
    var title: String
    var icon: String
    var position: Int
    var version: Int
    var updatedAt: Date
    var createdByID: String?

    enum CodingKeys: String, CodingKey {
        case id, title, icon, position, version
        case parentID = "parent_id"
        case updatedAt = "updated_at"
        case createdByID = "created_by_id"
    }

    init(id: String, parentID: String?, title: String, icon: String, position: Int, version: Int, updatedAt: Date, createdByID: String?) {
        self.id = id
        self.parentID = parentID
        self.title = title
        self.icon = icon
        self.position = position
        self.version = version
        self.updatedAt = updatedAt
        self.createdByID = createdByID
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        parentID = c.lenient(String.self, forKey: .parentID)
        title = c.lenient(String.self, forKey: .title) ?? ""
        icon = c.lenient(String.self, forKey: .icon) ?? ""
        position = c.lenient(Int.self, forKey: .position) ?? 0
        version = c.lenient(Int.self, forKey: .version) ?? 1
        updatedAt = c.lenient(Date.self, forKey: .updatedAt) ?? .now
        createdByID = c.lenient(String.self, forKey: .createdByID)
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(id, forKey: .id)
        try c.encode(parentID, forKey: .parentID)
        try c.encode(title, forKey: .title)
        try c.encode(icon, forKey: .icon)
        try c.encode(position, forKey: .position)
        try c.encode(version, forKey: .version)
        try c.encode(updatedAt, forKey: .updatedAt)
        try c.encode(createdByID, forKey: .createdByID)
    }

    var displayTitle: String { WikiText.title(title) }
}

struct WikiRef: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var title: String
    var icon: String

    enum CodingKeys: String, CodingKey { case id, title, icon }

    init(id: String, title: String, icon: String) {
        self.id = id
        self.title = title
        self.icon = icon
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        title = c.lenient(String.self, forKey: .title) ?? ""
        icon = c.lenient(String.self, forKey: .icon) ?? ""
    }

    var displayTitle: String { WikiText.title(title) }
}

/// One page with its body, breadcrumbs and subpages.
struct WikiPage: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var parentID: String?
    var title: String
    var icon: String
    var bodyHTML: String
    var position: Int
    var version: Int
    var createdBy: UserRef?
    var updatedBy: UserRef?
    var createdAt: Date
    var updatedAt: Date
    var breadcrumbs: [WikiRef]
    var children: [WikiRef]

    enum CodingKeys: String, CodingKey {
        case id, title, icon, position, version, breadcrumbs, children
        case parentID = "parent_id"
        case bodyHTML = "body_html"
        case createdBy = "created_by"
        case updatedBy = "updated_by"
        case createdAt = "created_at"
        case updatedAt = "updated_at"
    }

    init(id: String, parentID: String?, title: String, icon: String, bodyHTML: String, position: Int, version: Int, createdBy: UserRef?, updatedBy: UserRef?, createdAt: Date, updatedAt: Date, breadcrumbs: [WikiRef], children: [WikiRef]) {
        self.id = id
        self.parentID = parentID
        self.title = title
        self.icon = icon
        self.bodyHTML = bodyHTML
        self.position = position
        self.version = version
        self.createdBy = createdBy
        self.updatedBy = updatedBy
        self.createdAt = createdAt
        self.updatedAt = updatedAt
        self.breadcrumbs = breadcrumbs
        self.children = children
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        parentID = c.lenient(String.self, forKey: .parentID)
        title = c.lenient(String.self, forKey: .title) ?? ""
        icon = c.lenient(String.self, forKey: .icon) ?? ""
        bodyHTML = c.lenient(String.self, forKey: .bodyHTML) ?? ""
        position = c.lenient(Int.self, forKey: .position) ?? 0
        version = c.lenient(Int.self, forKey: .version) ?? 1
        createdBy = c.lenient(UserRef.self, forKey: .createdBy)
        updatedBy = c.lenient(UserRef.self, forKey: .updatedBy)
        createdAt = c.lenient(Date.self, forKey: .createdAt) ?? .now
        updatedAt = c.lenient(Date.self, forKey: .updatedAt) ?? createdAt
        breadcrumbs = c.lenient([WikiRef].self, forKey: .breadcrumbs) ?? []
        children = c.lenient([WikiRef].self, forKey: .children) ?? []
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(id, forKey: .id)
        try c.encode(parentID, forKey: .parentID)
        try c.encode(title, forKey: .title)
        try c.encode(icon, forKey: .icon)
        try c.encode(bodyHTML, forKey: .bodyHTML)
        try c.encode(position, forKey: .position)
        try c.encode(version, forKey: .version)
        try c.encode(createdBy, forKey: .createdBy)
        try c.encode(updatedBy, forKey: .updatedBy)
        try c.encode(createdAt, forKey: .createdAt)
        try c.encode(updatedAt, forKey: .updatedAt)
        try c.encode(breadcrumbs, forKey: .breadcrumbs)
        try c.encode(children, forKey: .children)
    }

    /// The page as the tree holds it.
    var node: WikiNode {
        WikiNode(id: id, parentID: parentID, title: title, icon: icon, position: position, version: version, updatedAt: updatedAt, createdByID: createdBy?.id)
    }

    var displayTitle: String { WikiText.title(title) }
}

/// A kept version of a page; `bodyHTML` only when one revision is read.
struct WikiRevision: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var title: String
    var icon: String
    var bodyHTML: String?
    var author: UserRef?
    var createdAt: Date
    var updatedAt: Date

    enum CodingKeys: String, CodingKey {
        case id, title, icon, author
        case bodyHTML = "body_html"
        case createdAt = "created_at"
        case updatedAt = "updated_at"
    }

    init(id: String, title: String, icon: String, bodyHTML: String?, author: UserRef?, createdAt: Date, updatedAt: Date) {
        self.id = id
        self.title = title
        self.icon = icon
        self.bodyHTML = bodyHTML
        self.author = author
        self.createdAt = createdAt
        self.updatedAt = updatedAt
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        title = c.lenient(String.self, forKey: .title) ?? ""
        icon = c.lenient(String.self, forKey: .icon) ?? ""
        bodyHTML = c.lenient(String.self, forKey: .bodyHTML)
        author = c.lenient(UserRef.self, forKey: .author)
        createdAt = c.lenient(Date.self, forKey: .createdAt) ?? .now
        updatedAt = c.lenient(Date.self, forKey: .updatedAt) ?? createdAt
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(id, forKey: .id)
        try c.encode(title, forKey: .title)
        try c.encode(icon, forKey: .icon)
        try c.encodeIfPresent(bodyHTML, forKey: .bodyHTML)
        try c.encode(author, forKey: .author)
        try c.encode(createdAt, forKey: .createdAt)
        try c.encode(updatedAt, forKey: .updatedAt)
    }

    var displayTitle: String { WikiText.title(title) }
}

struct WikiSearchHit: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var parentID: String?
    var title: String
    var icon: String
    var snippet: String
    var path: [WikiRef]
    var updatedAt: Date

    enum CodingKeys: String, CodingKey {
        case id, title, icon, snippet, path
        case parentID = "parent_id"
        case updatedAt = "updated_at"
    }

    init(id: String, parentID: String?, title: String, icon: String, snippet: String, path: [WikiRef], updatedAt: Date) {
        self.id = id
        self.parentID = parentID
        self.title = title
        self.icon = icon
        self.snippet = snippet
        self.path = path
        self.updatedAt = updatedAt
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        parentID = c.lenient(String.self, forKey: .parentID)
        title = c.lenient(String.self, forKey: .title) ?? ""
        icon = c.lenient(String.self, forKey: .icon) ?? ""
        snippet = c.lenient(String.self, forKey: .snippet) ?? ""
        path = c.lenient([WikiRef].self, forKey: .path) ?? []
        updatedAt = c.lenient(Date.self, forKey: .updatedAt) ?? .now
    }

    var displayTitle: String { WikiText.title(title) }
}

/// Body of `POST /v1/wiki/pages`.
struct WikiPageCreate: Encodable, Sendable {
    var parentID: String?
    var title: String?
    var icon: String?
    var bodyHTML: String?

    enum CodingKeys: String, CodingKey {
        case title, icon
        case parentID = "parent_id"
        case bodyHTML = "body_html"
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encodeIfPresent(parentID, forKey: .parentID)
        try c.encodeIfPresent(title, forKey: .title)
        try c.encodeIfPresent(icon, forKey: .icon)
        try c.encodeIfPresent(bodyHTML, forKey: .bodyHTML)
    }
}

/// Body of `PATCH /v1/wiki/pages/{id}`: only what changed, with the version it edited.
struct WikiPagePatch: Encodable, Sendable {
    var title: String?
    var icon: String?
    var bodyHTML: String?
    var version: Int

    enum CodingKeys: String, CodingKey {
        case title, icon, version
        case bodyHTML = "body_html"
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encodeIfPresent(title, forKey: .title)
        try c.encodeIfPresent(icon, forKey: .icon)
        try c.encodeIfPresent(bodyHTML, forKey: .bodyHTML)
        try c.encode(version, forKey: .version)
    }

    var isEmpty: Bool { title == nil && icon == nil && bodyHTML == nil }
}

/// Body of `POST /v1/wiki/pages/{id}/move`; a null parent is the top level.
struct WikiMove: Encodable, Sendable {
    var parentID: String?
    var position: Int

    enum CodingKeys: String, CodingKey {
        case position
        case parentID = "parent_id"
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(parentID, forKey: .parentID)
        try c.encode(position, forKey: .position)
    }
}

enum WikiText {
    /// The longest title the API takes.
    static let maxTitle = 200
    /// How deep pages nest.
    static let maxDepth = 10

    /// A page's title for display: "Untitled" until it has one.
    static func title(_ title: String) -> String {
        let trimmed = title.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? "Untitled" : trimmed
    }
}

/// Starter pages a new wiki offers (web `components/wiki/templates.ts`): an outline to fill in.
struct WikiTemplate: Identifiable, Sendable {
    let id: String
    let icon: String
    let title: String
    let hint: String
    let bodyHTML: String

    static let all: [WikiTemplate] = [
        WikiTemplate(
            id: "escalation", icon: "🚨", title: "Escalation process", hint: "When and how to hand a ticket up.",
            bodyHTML: "<p>Escalate when a customer is blocked, money is at stake, or you have tried twice without progress.</p><h2>Before you escalate</h2><ul><li><p>Read the ticket's history and search the wiki.</p></li><li><p>Write down what you tried.</p></li></ul><h2>Steps</h2><ol><li><p>Set the ticket to urgent.</p></li><li><p>Leave an internal note with what the customer needs.</p></li><li><p>Assign it to the lead on call.</p></li><li><p>Tell the customer who has it now and when they will hear back.</p></li></ol><h2>Who is on call</h2><p>Add your rota, or a link to it, here.</p>"
        ),
        WikiTemplate(
            id: "refunds", icon: "💸", title: "Refund policy", hint: "Who can refund what, and when.",
            bodyHTML: "<p>Who can refund what, and when.</p><h2>The rule</h2><p>Refund any charge within 30 days, no questions asked.</p><h2>Exceptions</h2><ul><li><p>Duplicate charges: refund at once and apologize.</p></li><li><p>Over 100 euros: a lead approves it first.</p></li><li><p>Chargebacks: don't refund twice. Hand the ticket to billing.</p></li></ul><h2>How to refund</h2><ol><li><p>Find the charge in the billing tool.</p></li><li><p>Refund it and note the amount on the ticket.</p></li></ol>"
        ),
        WikiTemplate(
            id: "onboarding", icon: "👋", title: "Onboarding checklist", hint: "A new teammate's first two weeks.",
            bodyHTML: "<p>What a new teammate does in their first two weeks.</p><h2>Week one</h2><ul><li><p>Get access to the helpdesk, chat and billing tools.</p></li><li><p>Read the escalation process and the refund policy.</p></li><li><p>Shadow a teammate for two shifts.</p></li></ul><h2>Week two</h2><ul><li><p>Answer tickets with a buddy reviewing your drafts.</p></li><li><p>Take your own queue for half a day.</p></li></ul>"
        ),
    ]
}
