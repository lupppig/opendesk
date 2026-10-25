import Foundation

// Resource shapes from contracts/api.md. Keys are copied exactly through
// explicit CodingKeys (no snake_case conversion: it would also rewrite the
// keys inside `custom`, `fields` and `data`). Members the app can live
// without are optional so a server that grows or trims a field never fails a
// whole screen.

// MARK: - Lists

struct Page<T: Decodable & Sendable>: Decodable, Sendable {
    var data: [T]
    var nextCursor: String?

    enum CodingKeys: String, CodingKey {
        case data
        case nextCursor = "next_cursor"
    }

    init(data: [T], nextCursor: String? = nil) {
        self.data = data
        self.nextCursor = nextCursor
    }

    /// Accepts both `{data, next_cursor}` and a bare array.
    init(from decoder: Decoder) throws {
        if let container = try? decoder.container(keyedBy: CodingKeys.self), container.contains(.data) {
            data = try container.decode([T].self, forKey: .data)
            nextCursor = container.lenient(String.self, forKey: .nextCursor)
        } else {
            data = try decoder.singleValueContainer().decode([T].self)
            nextCursor = nil
        }
    }
}

/// A 204 or any body the caller ignores.
struct EmptyBody: Codable, Sendable {}

// MARK: - People and workspaces

enum Role: String, LenientEnum, CaseIterable {
    case owner, admin, agent, viewer
    static let fallback = Role.viewer

    var canWrite: Bool { self != .viewer }
    var isAdmin: Bool { self == .owner || self == .admin }

    var title: String {
        switch self {
        case .owner: "Owner"
        case .admin: "Admin"
        case .agent: "Agent"
        case .viewer: "Viewer"
        }
    }
}

enum Availability: String, LenientEnum, CaseIterable {
    case online, away, offline
    static let fallback = Availability.offline

    var title: String {
        switch self {
        case .online: "Online"
        case .away: "Away"
        case .offline: "Offline"
        }
    }
}

struct User: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var email: String
    var name: String
    var avatarURL: String?
    var avatarSource: String?
    var emailVerified: Bool?
    var totpEnabled: Bool?
    var createdAt: Date?

    enum CodingKeys: String, CodingKey {
        case id, email, name
        case avatarURL = "avatar_url"
        case avatarSource = "avatar_source"
        case emailVerified = "email_verified"
        case totpEnabled = "totp_enabled"
        case createdAt = "created_at"
    }

    var displayName: String { name.isEmpty ? email : name }
}

struct UserRef: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var name: String
    var email: String?
    var avatarURL: String?

    enum CodingKeys: String, CodingKey {
        case id, name, email
        case avatarURL = "avatar_url"
    }

    var displayName: String { name.isEmpty ? (email ?? "Someone") : name }
}

struct DisplayOverride: Codable, Hashable, Sendable {
    var label: String?
    var color: String?
}

struct WorkspaceSettings: Codable, Hashable, Sendable {
    var brandColor: String?
    var timezone: String?
    var statusLabels: [String: DisplayOverride]?
    var priorityLabels: [String: DisplayOverride]?

    enum CodingKeys: String, CodingKey {
        case brandColor = "brand_color"
        case timezone
        case statusLabels = "status_labels"
        case priorityLabels = "priority_labels"
    }
}

struct Workspace: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var name: String
    var slug: String?
    var avatarURL: String?
    var settings: WorkspaceSettings?
    var createdAt: Date?

    enum CodingKeys: String, CodingKey {
        case id, name, slug, settings
        case avatarURL = "avatar_url"
        case createdAt = "created_at"
    }
}

struct Membership: Codable, Hashable, Sendable, Identifiable {
    var workspace: Workspace
    var role: Role
    var id: String { workspace.id }
}

struct Me: Codable, Sendable {
    var user: User
    var memberships: [Membership]
}

struct MemberUser: Codable, Hashable, Sendable {
    var id: String
    var name: String
    var email: String?
    var avatarURL: String?

    enum CodingKeys: String, CodingKey {
        case id, name, email
        case avatarURL = "avatar_url"
    }
}

struct Member: Codable, Hashable, Sendable, Identifiable {
    var user: MemberUser
    var role: Role
    var availability: Availability?
    var presence: Availability?
    var openTickets: Int?
    var lastSeenAt: Date?

    enum CodingKeys: String, CodingKey {
        case user, role, availability, presence
        case openTickets = "open_tickets"
        case lastSeenAt = "last_seen_at"
    }

    var id: String { user.id }
    var ref: UserRef { UserRef(id: user.id, name: user.name, email: user.email, avatarURL: user.avatarURL) }
}

struct Team: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var name: String
    var memberIDs: [String]?

    enum CodingKeys: String, CodingKey {
        case id, name
        case memberIDs = "member_ids"
    }
}

// MARK: - Labels

struct TagRef: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var name: String
    var color: String?
    var groupID: String?

    enum CodingKeys: String, CodingKey {
        case id, name, color
        case groupID = "group_id"
    }
}

struct Tag: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var name: String
    var color: String?
    var description: String?
    var groupID: String?
    var archived: Bool?
    var appliesTo: [String]?

    enum CodingKeys: String, CodingKey {
        case id, name, color, description, archived
        case groupID = "group_id"
        case appliesTo = "applies_to"
    }

    var ref: TagRef { TagRef(id: id, name: name, color: color, groupID: groupID) }
}

// MARK: - Tickets

enum TicketStatus: String, LenientEnum, CaseIterable, Identifiable {
    case open, pending
    case onHold = "on_hold"
    case solved, closed
    static let fallback = TicketStatus.open
    var id: String { rawValue }

    var title: String {
        switch self {
        case .open: "Open"
        case .pending: "Pending"
        case .onHold: "On hold"
        case .solved: "Solved"
        case .closed: "Closed"
        }
    }

    var isDone: Bool { self == .solved || self == .closed }
}

enum Priority: String, LenientEnum, CaseIterable, Identifiable {
    case urgent, high, normal, low
    static let fallback = Priority.normal
    var id: String { rawValue }

    var title: String {
        switch self {
        case .urgent: "Urgent"
        case .high: "High"
        case .normal: "Normal"
        case .low: "Low"
        }
    }

    var rank: Int {
        switch self {
        case .urgent: 3
        case .high: 2
        case .normal: 1
        case .low: 0
        }
    }
}

enum Severity: String, LenientEnum, CaseIterable, Identifiable {
    case sev1, sev2, sev3, sev4
    static let fallback = Severity.sev4
    var id: String { rawValue }

    var title: String { "Sev " + rawValue.dropFirst(3) }
    var short: String { "S" + rawValue.dropFirst(3) }

    var hint: String {
        switch self {
        case .sev1: "Critical outage"
        case .sev2: "Major impact"
        case .sev3: "Minor impact"
        case .sev4: "Low impact"
        }
    }
}

enum Channel: String, LenientEnum, CaseIterable, Identifiable {
    case email, chat, api, portal
    static let fallback = Channel.email
    var id: String { rawValue }

    var title: String {
        switch self {
        case .email: "Email"
        case .chat: "Chat"
        case .api: "API"
        case .portal: "Portal"
        }
    }
}

struct ContactRef: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var email: String?
    var name: String
    var avatarURL: String?

    enum CodingKeys: String, CodingKey {
        case id, email, name
        case avatarURL = "avatar_url"
    }

    var displayName: String {
        if !name.isEmpty { return name }
        return email ?? "Unknown"
    }
}

struct TicketSLA: Codable, Hashable, Sendable {
    var policyID: String?
    var firstResponseDueAt: Date?
    var resolutionDueAt: Date?
    var breached: Bool

    enum CodingKeys: String, CodingKey {
        case policyID = "policy_id"
        case firstResponseDueAt = "first_response_due_at"
        case resolutionDueAt = "resolution_due_at"
        case breached
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        policyID = c.lenient(String.self, forKey: .policyID)
        firstResponseDueAt = c.lenient(Date.self, forKey: .firstResponseDueAt)
        resolutionDueAt = c.lenient(Date.self, forKey: .resolutionDueAt)
        breached = c.lenient(Bool.self, forKey: .breached) ?? false
    }
}

struct CSAT: Codable, Hashable, Sendable {
    var score: Int
    var comment: String?
}

struct Ticket: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var number: Int
    var subject: String
    var status: TicketStatus
    var priority: Priority
    var severity: Severity?
    var channel: Channel
    var mailboxID: String?
    var contact: ContactRef?
    var assignee: UserRef?
    var teamID: String?
    var tags: [TagRef]
    var fields: [String: JSONValue]
    var sla: TicketSLA?
    var firstRespondedAt: Date?
    var solvedAt: Date?
    var snoozedUntil: Date?
    var lastMessageAt: Date
    var lastCustomerMessageAt: Date?
    var waitingSince: Date?
    var lastMessagePreview: String
    var messageCount: Int
    var spam: Bool
    var csat: CSAT?
    var custom: [String: JSONValue]
    var createdAt: Date
    var updatedAt: Date

    enum CodingKeys: String, CodingKey {
        case id, number, subject, status, priority, severity, channel, contact, assignee, tags, fields, sla, spam, csat, custom
        case mailboxID = "mailbox_id"
        case teamID = "team_id"
        case firstRespondedAt = "first_responded_at"
        case solvedAt = "solved_at"
        case snoozedUntil = "snoozed_until"
        case lastMessageAt = "last_message_at"
        case lastCustomerMessageAt = "last_customer_message_at"
        case waitingSince = "waiting_since"
        case lastMessagePreview = "last_message_preview"
        case messageCount = "message_count"
        case createdAt = "created_at"
        case updatedAt = "updated_at"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        number = c.lenient(Int.self, forKey: .number) ?? 0
        subject = c.lenient(String.self, forKey: .subject) ?? ""
        status = c.lenient(TicketStatus.self, forKey: .status) ?? .open
        priority = c.lenient(Priority.self, forKey: .priority) ?? .normal
        severity = c.lenient(Severity.self, forKey: .severity)
        channel = c.lenient(Channel.self, forKey: .channel) ?? .email
        mailboxID = c.lenient(String.self, forKey: .mailboxID)
        contact = c.lenient(ContactRef.self, forKey: .contact)
        assignee = c.lenient(UserRef.self, forKey: .assignee)
        teamID = c.lenient(String.self, forKey: .teamID)
        tags = c.lenient([TagRef].self, forKey: .tags) ?? []
        fields = c.lenient([String: JSONValue].self, forKey: .fields) ?? [:]
        sla = c.lenient(TicketSLA.self, forKey: .sla)
        firstRespondedAt = c.lenient(Date.self, forKey: .firstRespondedAt)
        solvedAt = c.lenient(Date.self, forKey: .solvedAt)
        snoozedUntil = c.lenient(Date.self, forKey: .snoozedUntil)
        createdAt = c.lenient(Date.self, forKey: .createdAt) ?? .now
        updatedAt = c.lenient(Date.self, forKey: .updatedAt) ?? createdAt
        lastMessageAt = c.lenient(Date.self, forKey: .lastMessageAt) ?? updatedAt
        lastCustomerMessageAt = c.lenient(Date.self, forKey: .lastCustomerMessageAt)
        waitingSince = c.lenient(Date.self, forKey: .waitingSince)
        lastMessagePreview = c.lenient(String.self, forKey: .lastMessagePreview) ?? ""
        messageCount = c.lenient(Int.self, forKey: .messageCount) ?? 0
        spam = c.lenient(Bool.self, forKey: .spam) ?? false
        csat = c.lenient(CSAT.self, forKey: .csat)
        custom = c.lenient([String: JSONValue].self, forKey: .custom) ?? [:]
    }

    var displaySubject: String { subject.isEmpty ? "(no subject)" : subject }
    var contactName: String { contact?.displayName ?? "Unknown" }

    /// The customer wrote last and nobody has answered yet (the web's awaitingReply).
    var awaitingReply: Bool {
        guard status == .open || status == .pending, let last = lastCustomerMessageAt else { return false }
        return last >= lastMessageAt.addingTimeInterval(-1)
    }

    /// The AI agent handed this conversation to the team (`custom.ai_handoff`).
    var aiHandedOff: Bool { custom["ai_handoff"]?.bool == true }

    var isSnoozed: Bool {
        guard let snoozedUntil else { return false }
        return snoozedUntil > AppClock.now
    }
}

/// Body of `PATCH /v1/tickets/{id}`. Only the members set are sent; a
/// `.clear` reference is sent as JSON null (assignee, team, severity, snooze).
struct TicketPatch: Encodable, Sendable {
    enum Reference<Value: Encodable & Sendable>: Sendable {
        case set(Value)
        case clear
    }

    var status: TicketStatus?
    var priority: Priority?
    var severity: Reference<Severity>?
    var assigneeID: Reference<String>?
    var teamID: Reference<String>?
    var snoozedUntil: Reference<Date>?
    var spam: Bool?
    var tags: [String]?
    var addTags: [String]?
    var removeTags: [String]?
    var subject: String?
    /// Custom field values, merged; `.null` clears one.
    var fields: [String: JSONValue]?

    enum CodingKeys: String, CodingKey {
        case status, priority, severity, spam, tags, subject, fields
        case assigneeID = "assignee_id"
        case teamID = "team_id"
        case snoozedUntil = "snoozed_until"
        case addTags = "add_tags"
        case removeTags = "remove_tags"
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encodeIfPresent(status, forKey: .status)
        try c.encodeIfPresent(priority, forKey: .priority)
        try Self.encode(severity, forKey: .severity, in: &c)
        try Self.encode(assigneeID, forKey: .assigneeID, in: &c)
        try Self.encode(teamID, forKey: .teamID, in: &c)
        try Self.encode(snoozedUntil, forKey: .snoozedUntil, in: &c)
        try c.encodeIfPresent(spam, forKey: .spam)
        try c.encodeIfPresent(tags, forKey: .tags)
        try c.encodeIfPresent(addTags, forKey: .addTags)
        try c.encodeIfPresent(removeTags, forKey: .removeTags)
        try c.encodeIfPresent(subject, forKey: .subject)
        try c.encodeIfPresent(fields, forKey: .fields)
    }

    private static func encode<V>(_ value: Reference<V>?, forKey key: CodingKeys, in c: inout KeyedEncodingContainer<CodingKeys>) throws {
        switch value {
        case nil: break
        case let .set(inner)?: try c.encode(inner, forKey: key)
        case .clear?: try c.encodeNil(forKey: key)
        }
    }

    /// Applies the patch to a local copy for an optimistic update.
    func applied(to ticket: Ticket, me: UserRef?, members: [Member]) -> Ticket {
        var next = ticket
        if let status {
            next.status = status
            if status != .onHold { next.snoozedUntil = nil }
        }
        if let priority { next.priority = priority }
        switch severity {
        case let .set(value)?: next.severity = value
        case .clear?: next.severity = nil
        case nil: break
        }
        switch assigneeID {
        case let .set(id)?:
            if let me, me.id == id { next.assignee = me }
            else if let member = members.first(where: { $0.id == id }) { next.assignee = member.ref }
        case .clear?: next.assignee = nil
        case nil: break
        }
        switch teamID {
        case let .set(id)?: next.teamID = id
        case .clear?: next.teamID = nil
        case nil: break
        }
        switch snoozedUntil {
        case let .set(date)?:
            next.snoozedUntil = date
            if status == nil { next.status = .onHold }
        case .clear?: next.snoozedUntil = nil
        case nil: break
        }
        if let spam { next.spam = spam }
        if let subject { next.subject = subject }
        for (key, value) in fields ?? [:] {
            next.fields[key] = value.isNull ? nil : value
        }
        return next
    }
}

struct TicketCounts: Codable, Hashable, Sendable {
    var unassigned: Int
    var mine: Int
    var open: Int
    var pending: Int
    var onHold: Int
    var solved: Int
    var spam: Int
    var views: [String: Int]
    /// Open and unassigned tickets per team the caller can see, and summed over the caller's teams.
    var teams: [String: TeamCount] = [:]
    var myTeams: TeamCount?

    struct TeamCount: Codable, Hashable, Sendable {
        var open: Int
        var unassigned: Int
    }

    enum CodingKeys: String, CodingKey {
        case unassigned, mine, open, pending, solved, spam, views, teams
        case onHold = "on_hold"
        case myTeams = "my_teams"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        unassigned = c.lenient(Int.self, forKey: .unassigned) ?? 0
        mine = c.lenient(Int.self, forKey: .mine) ?? 0
        open = c.lenient(Int.self, forKey: .open) ?? 0
        pending = c.lenient(Int.self, forKey: .pending) ?? 0
        onHold = c.lenient(Int.self, forKey: .onHold) ?? 0
        solved = c.lenient(Int.self, forKey: .solved) ?? 0
        spam = c.lenient(Int.self, forKey: .spam) ?? 0
        views = c.lenient([String: Int].self, forKey: .views) ?? [:]
        teams = c.lenient([String: TeamCount].self, forKey: .teams) ?? [:]
        myTeams = c.lenient(TeamCount.self, forKey: .myTeams)
    }
}

struct FieldFilter: Codable, Hashable, Sendable {
    var key: String
    var op: String
    var value: String
}

struct TicketFilters: Codable, Hashable, Sendable {
    var status: [String]?
    var priority: [String]?
    var severity: [String]?
    var assignee: String?
    var teamID: String?
    var mailboxID: String?
    var tag: String?
    var channel: String?
    var q: String?
    var fields: [FieldFilter]?
    var receivedAt: String?
    var brandID: String?
    var widgetID: String?

    enum CodingKeys: String, CodingKey {
        case status, priority, severity, assignee, tag, channel, q, fields
        case teamID = "team_id"
        case mailboxID = "mailbox_id"
        case receivedAt = "received_at"
        case brandID = "brand_id"
        case widgetID = "widget_id"
    }
}

struct SavedView: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var name: String
    var shared: Bool?
    var filters: TicketFilters?
    var position: Int?
}

struct SavedReply: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var title: String
    var shortcut: String?
    var bodyHTML: String

    enum CodingKeys: String, CodingKey {
        case id, title, shortcut
        case bodyHTML = "body_html"
    }
}

// MARK: - Messages and activity

struct Attachment: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var filename: String
    var contentType: String
    var sizeBytes: Int
    var inline: Bool
    var url: String

    enum CodingKeys: String, CodingKey {
        case id, filename, inline, url
        case contentType = "content_type"
        case sizeBytes = "size_bytes"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        filename = c.lenient(String.self, forKey: .filename) ?? "file"
        contentType = c.lenient(String.self, forKey: .contentType) ?? "application/octet-stream"
        sizeBytes = c.lenient(Int.self, forKey: .sizeBytes) ?? 0
        inline = c.lenient(Bool.self, forKey: .inline) ?? false
        url = c.lenient(String.self, forKey: .url) ?? ""
    }

    var isImage: Bool { contentType.hasPrefix("image/") }
}

struct MessageAuthor: Codable, Hashable, Sendable {
    var type: String
    var user: UserRef?
    var contact: ContactRef?
    /// The bot an AI reply was sent as (contracts/bots.md "Where a bot shows").
    var bot: BotRef?

    enum CodingKeys: String, CodingKey { case type, user, contact, bot }

    init(type: String, user: UserRef?, contact: ContactRef?, bot: BotRef? = nil) {
        self.type = type
        self.user = user
        self.contact = contact
        self.bot = bot
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        type = try c.decode(String.self, forKey: .type)
        user = c.lenient(UserRef.self, forKey: .user)
        contact = c.lenient(ContactRef.self, forKey: .contact)
        bot = c.lenient(BotRef.self, forKey: .bot)
    }
}

/// A customer-facing bot as a message carries it: its name and a PNG of its
/// look (`avatar_url`). Only the look's kind and emoji are kept, for a native
/// emoji face; everything else comes from the server-rendered PNG.
struct BotRef: Codable, Hashable, Sendable {
    var id: String
    var name: String
    var avatarURL: String
    var lookKind: String
    var emoji: String

    enum CodingKeys: String, CodingKey {
        case id, name, look
        case avatarURL = "avatar_url"
    }

    private enum LookKeys: String, CodingKey { case kind, emoji }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = c.lenient(String.self, forKey: .id) ?? ""
        name = c.lenient(String.self, forKey: .name) ?? ""
        avatarURL = c.lenient(String.self, forKey: .avatarURL) ?? ""
        let look = try? c.nestedContainer(keyedBy: LookKeys.self, forKey: .look)
        lookKind = look?.lenient(String.self, forKey: .kind) ?? "built"
        emoji = look?.lenient(String.self, forKey: .emoji) ?? ""
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(id, forKey: .id)
        try c.encode(name, forKey: .name)
        try c.encode(avatarURL, forKey: .avatarURL)
        var look = c.nestedContainer(keyedBy: LookKeys.self, forKey: .look)
        try look.encode(lookKind, forKey: .kind)
        try look.encode(emoji, forKey: .emoji)
    }

    /// The emoji to draw natively: the server PNG of an emoji look shows only the initial.
    var nativeEmoji: String? { lookKind == "emoji" && !emoji.isEmpty ? emoji : nil }
}

enum MessageKind: String, LenientEnum {
    case reply, note
    static let fallback = MessageKind.reply
}

struct Message: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var ticketID: String
    var kind: MessageKind
    var direction: String
    var author: MessageAuthor
    var bodyHTML: String
    var bodyText: String
    var hasQuoted: Bool
    var subject: String
    var fromEmail: String
    var fromName: String
    var to: [String]
    var cc: [String]
    var attachments: [Attachment]
    var deliveryStatus: String
    var deliveryError: String
    var ai: Bool
    var createdAt: Date
    /// Members the event payload left out (chat replies arrive customer-safe).
    var isPartial = false
    /// A deleted message, or a note sent as a reply: a marker the app does not show yet.
    var isHidden = false
    /// Set on the messages of a side conversation (a third-party thread).
    var threadID: String?
    /// When the author last edited a note.
    var editedAt: Date?

    enum CodingKeys: String, CodingKey {
        case id, kind, direction, author, subject, to, cc, attachments, ai
        case ticketID = "ticket_id"
        case bodyHTML = "body_html"
        case bodyText = "body_text"
        case hasQuoted = "has_quoted"
        case fromEmail = "from_email"
        case fromName = "from_name"
        case deliveryStatus = "delivery_status"
        case deliveryError = "delivery_error"
        case createdAt = "created_at"
        case threadID = "thread_id"
        case editedAt = "edited_at"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        ticketID = c.lenient(String.self, forKey: .ticketID) ?? ""
        threadID = c.lenient(String.self, forKey: .threadID)
        editedAt = c.lenient(Date.self, forKey: .editedAt)
        kind = c.lenient(MessageKind.self, forKey: .kind) ?? .reply
        direction = c.lenient(String.self, forKey: .direction) ?? "inbound"
        let author = c.lenient(MessageAuthor.self, forKey: .author)
        self.author = author ?? MessageAuthor(type: "contact", user: nil, contact: nil)
        bodyHTML = c.lenient(String.self, forKey: .bodyHTML) ?? ""
        bodyText = c.lenient(String.self, forKey: .bodyText) ?? ""
        hasQuoted = c.lenient(Bool.self, forKey: .hasQuoted) ?? false
        subject = c.lenient(String.self, forKey: .subject) ?? ""
        fromEmail = c.lenient(String.self, forKey: .fromEmail) ?? ""
        fromName = c.lenient(String.self, forKey: .fromName) ?? ""
        let to = c.lenient([String].self, forKey: .to)
        self.to = to ?? []
        cc = c.lenient([String].self, forKey: .cc) ?? []
        let attachments = c.lenient([Attachment].self, forKey: .attachments)
        self.attachments = attachments ?? []
        deliveryStatus = c.lenient(String.self, forKey: .deliveryStatus) ?? "none"
        deliveryError = c.lenient(String.self, forKey: .deliveryError) ?? ""
        ai = c.lenient(Bool.self, forKey: .ai) ?? false
        createdAt = c.lenient(Date.self, forKey: .createdAt) ?? .now
        isPartial = author == nil || to == nil || attachments == nil
        let marker = try decoder.container(keyedBy: MarkerKeys.self)
        isHidden = (try? marker.decodeNil(forKey: .deletedAt)) == false || (try? marker.decodeNil(forKey: .sentAsReplyID)) == false
    }

    private enum MarkerKeys: String, CodingKey {
        case deletedAt = "deleted_at"
        case sentAsReplyID = "sent_as_reply_id"
    }

    init(localID: String, ticketID: String, kind: MessageKind, html: String, text: String, author: UserRef) {
        id = localID
        self.ticketID = ticketID
        self.kind = kind
        direction = "outbound"
        self.author = MessageAuthor(type: "user", user: author, contact: nil)
        bodyHTML = html
        bodyText = text
        hasQuoted = false
        subject = ""
        fromEmail = author.email ?? ""
        fromName = author.name
        to = []
        cc = []
        attachments = []
        deliveryStatus = "pending"
        deliveryError = ""
        ai = false
        createdAt = AppClock.now
    }

    var isNote: Bool { kind == .note }
    var isInbound: Bool { direction == "inbound" }
    var isLocal: Bool { id.hasPrefix("tmp-") }

    var authorName: String {
        if ai {
            if let bot = author.bot, !bot.name.isEmpty { return bot.name }
            return fromName.isEmpty ? "Ollie" : fromName
        }
        if let user = author.user { return user.displayName }
        if let contact = author.contact { return contact.displayName }
        if author.type == "system" { return "opendesk" }
        if author.type == "api" { return fromName.isEmpty ? "API" : fromName }
        if !fromName.isEmpty { return fromName }
        return fromEmail.isEmpty ? "Unknown" : fromEmail
    }

    var authorAvatar: String? {
        if ai { return author.bot.flatMap { $0.avatarURL.isEmpty ? nil : $0.avatarURL } }
        return author.user?.avatarURL ?? author.contact?.avatarURL
    }
    var authorSeed: String { author.bot?.id ?? author.user?.id ?? author.contact?.id ?? fromEmail }
    /// The bot an AI reply was sent as, shown with its name, avatar and an "AI" tint.
    var bot: BotRef? { ai ? author.bot : nil }

    /// Merges an event copy into the stored one, keeping members the event left out.
    func merged(with update: Message) -> Message {
        guard update.isPartial else { return update }
        var next = update
        if update.to.isEmpty { next.to = to }
        if update.cc.isEmpty { next.cc = cc }
        if update.attachments.isEmpty { next.attachments = attachments }
        if update.author.user == nil, update.author.contact == nil, update.author.bot == nil { next.author = author }
        if update.fromEmail.isEmpty { next.fromEmail = fromEmail }
        if update.threadID == nil { next.threadID = threadID }
        next.isPartial = false
        return next
    }
}

struct ActivityActor: Codable, Hashable, Sendable {
    var type: String
    var user: UserRef?
}

struct Activity: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var ticketID: String
    var actor: ActivityActor
    var type: String
    var data: [String: JSONValue]
    var createdAt: Date

    enum CodingKeys: String, CodingKey {
        case id, actor, type, data
        case ticketID = "ticket_id"
        case createdAt = "created_at"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        ticketID = c.lenient(String.self, forKey: .ticketID) ?? ""
        actor = c.lenient(ActivityActor.self, forKey: .actor) ?? ActivityActor(type: "system", user: nil)
        type = c.lenient(String.self, forKey: .type) ?? ""
        data = c.lenient([String: JSONValue].self, forKey: .data) ?? [:]
        createdAt = c.lenient(Date.self, forKey: .createdAt) ?? .now
    }

    var isAI: Bool { actor.type == "ai" }
}

// MARK: - Notifications

struct NotificationTicket: Codable, Hashable, Sendable {
    var id: String
    var number: Int
    var subject: String
}

/// The kinds of in-app notification (contracts/api.md `Notification.type`).
enum NotificationKind: String, CaseIterable, Sendable, Identifiable {
    case assigned
    case newTicket = "new_ticket"
    case mentioned
    case customerReplied = "customer_replied"
    case slaBreached = "sla_breached"
    case aiHandoff = "ai_handoff"
    case aiCapReached = "ai_cap_reached"
    case aiKeyFailed = "ai_key_failed"
    case aiBalanceLow = "ai_balance_low"
    case aiBalanceOut = "ai_balance_out"
    case autoRechargeFailed = "auto_recharge_failed"
    case teamJoined = "team_joined"
    case importFinished = "import_finished"
    case threadReplied = "thread_replied"
    case collabAssigned = "collab_assigned"
    case collabMention = "collab_mention"

    var id: String { rawValue }

    /// For the per-device switches.
    var settingTitle: String {
        switch self {
        case .assigned: "Assigned to you"
        case .newTicket: "New tickets"
        case .mentioned: "Mentions"
        case .customerReplied: "Customer replies"
        case .slaBreached: "SLA breaches"
        case .aiHandoff: "Handoffs from the AI agent"
        case .aiCapReached: "Ollie's monthly spending cap"
        case .aiKeyFailed: "AI provider key problems"
        case .aiBalanceLow: "AI balance low"
        case .aiBalanceOut: "AI balance used up"
        case .autoRechargeFailed: "Auto-recharge failed"
        case .teamJoined: "Team joins"
        case .importFinished: "Imports"
        case .threadReplied: "Side conversation answers"
        case .collabAssigned: "Mail assigned to you"
        case .collabMention: "Mail comment mentions"
        }
    }

    var settingHint: String {
        switch self {
        case .assigned: "A teammate or a rule gives you a ticket"
        case .newTicket: "A new ticket nobody is assigned to"
        case .mentioned: "Someone @mentions you in a note"
        case .customerReplied: "A customer answers on a ticket you own"
        case .slaBreached: "A ticket you own misses its target"
        case .aiHandoff: "A customer asks for a person"
        case .aiCapReached: "Owners and admins"
        case .aiKeyFailed: "Owners and admins"
        case .aiBalanceLow: "Owners and admins"
        case .aiBalanceOut: "Owners and admins"
        case .autoRechargeFailed: "Owners and admins"
        case .teamJoined: "An admin joins a team you lead"
        case .importFinished: "An import you started ends or needs new credentials"
        case .threadReplied: "Someone outside your team answers a side conversation on your ticket"
        case .collabAssigned: "A teammate assigns you a mail conversation"
        case .collabMention: "Someone mentions you in a comment on a mail conversation"
        }
    }

    var symbol: String {
        switch self {
        case .assigned: "person.crop.circle.badge.plus"
        case .newTicket: "tray.and.arrow.down"
        case .mentioned: "at"
        case .customerReplied: "arrowshape.turn.up.left"
        case .slaBreached: "clock.badge.exclamationmark"
        case .aiHandoff: "hand.raised"
        case .aiCapReached, .aiKeyFailed: "sparkles"
        case .aiBalanceLow, .aiBalanceOut, .autoRechargeFailed: "creditcard"
        case .teamJoined: "person.2"
        case .importFinished: "square.and.arrow.down"
        case .threadReplied: "bubble.left.and.bubble.right"
        case .collabAssigned: "envelope.badge.person.crop"
        case .collabMention: "at"
        }
    }

    /// Ticket-bound kinds; the rest are workspace alerts for owners and admins.
    var isAdminAlert: Bool {
        switch self {
        case .aiCapReached, .aiKeyFailed, .aiBalanceLow, .aiBalanceOut, .autoRechargeFailed, .importFinished: true
        default: false
        }
    }
}

struct AppNotification: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var type: String
    var ticket: NotificationTicket?
    var data: [String: JSONValue]
    var readAt: Date?
    var createdAt: Date

    enum CodingKeys: String, CodingKey {
        case id, type, ticket, data
        case readAt = "read_at"
        case createdAt = "created_at"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        type = c.lenient(String.self, forKey: .type) ?? ""
        ticket = c.lenient(NotificationTicket.self, forKey: .ticket)
        data = c.lenient([String: JSONValue].self, forKey: .data) ?? [:]
        readAt = c.lenient(Date.self, forKey: .readAt)
        createdAt = c.lenient(Date.self, forKey: .createdAt) ?? .now
    }

    var kind: NotificationKind? { NotificationKind(rawValue: type) }
    var isUnread: Bool { readAt == nil }

    private var actorName: String? {
        data["actor"]?["name"]?.string ?? data["contact"]?["name"]?.string ?? data["by"]?.string.flatMap { $0.isEmpty ? nil : $0 }
    }

    /// The line the web shows (shell.json notifications.type.*).
    var title: String {
        let who = actorName
        switch kind {
        case .assigned: return who.map { "\($0) assigned you a ticket" } ?? "A ticket was assigned to you"
        case .newTicket: return (data["contact"]?["name"]?.string).map { "New ticket from \($0)" } ?? "A new ticket came in"
        case .mentioned: return who.map { "\($0) mentioned you" } ?? "You were mentioned in a note"
        case .customerReplied: return (data["contact"]?["name"]?.string).map { "\($0) replied" } ?? "The customer replied"
        case .slaBreached: return "SLA breached"
        case .aiHandoff:
            return (data["contact"]?["name"]?.string).map { "\($0) asked for a person" } ?? "A customer asked for a person"
        case .aiCapReached: return "Ollie reached this month's agent spending cap"
        case .aiBalanceLow: return "AI balance is running low"
        case .aiBalanceOut: return "AI balance is used up: Ollie is paused"
        case .autoRechargeFailed: return "Auto-recharge failed and is now off"
        case .aiKeyFailed:
            return data["reason"]?.string == "no_funds"
                ? "Your AI provider account is out of funds"
                : "Your AI provider rejected the workspace's key"
        case .threadReplied:
            let from = data["from"]?["name"]?.string.flatMap { $0.isEmpty ? nil : $0 } ?? data["from"]?["email"]?.string.flatMap { $0.isEmpty ? nil : $0 }
            return from.map { "\($0) answered your side conversation" } ?? "Someone answered your side conversation"
        case .collabAssigned: return who.map { "\($0) assigned you a conversation" } ?? "A conversation was assigned to you"
        case .collabMention: return who.map { "\($0) mentioned you in a comment" } ?? "You were mentioned in a comment"
        case .teamJoined:
            let joiner = data["user"]?["name"]?.string.flatMap { $0.isEmpty ? nil : $0 } ?? "Someone"
            guard let team = data["team"]?["name"]?.string, !team.isEmpty else { return "\(joiner) joined a team you lead" }
            return data["team"]?["private"]?.bool == true
                ? "\(joiner) joined the private team \(team) and can now see its tickets"
                : "\(joiner) joined \(team)"
        case .importFinished:
            let source = data["source_name"]?.string.flatMap { $0.isEmpty ? nil : $0 } ?? "history"
            switch data["status"]?.string {
            case "completed": return "Your \(source) import is done"
            case "needs_credentials": return "Your \(source) import needs new credentials"
            default: return "Your \(source) import stopped"
            }
        case nil: return "Notification"
        }
    }

    var preview: String? {
        if let preview = data["preview"]?.string, !preview.isEmpty { return preview }
        return nil
    }
}

// MARK: - Account

struct SessionInfo: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var userAgent: String?
    var ip: String?
    var createdAt: Date?
    var lastUsedAt: Date?
    var current: Bool?
    var client: String?

    enum CodingKeys: String, CodingKey {
        case id, ip, current, client
        case userAgent = "user_agent"
        case createdAt = "created_at"
        case lastUsedAt = "last_used_at"
    }
}

struct PushDevice: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var platform: String?
    var name: String?
    var appVersion: String?
    var environment: String?
    var mutedTypes: [String]
    var current: Bool
    var createdAt: Date?
    var lastPushAt: Date?

    enum CodingKeys: String, CodingKey {
        case id, platform, name, environment, current
        case appVersion = "app_version"
        case mutedTypes = "muted_types"
        case createdAt = "created_at"
        case lastPushAt = "last_push_at"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        platform = c.lenient(String.self, forKey: .platform)
        name = c.lenient(String.self, forKey: .name)
        appVersion = c.lenient(String.self, forKey: .appVersion)
        environment = c.lenient(String.self, forKey: .environment)
        mutedTypes = c.lenient([String].self, forKey: .mutedTypes) ?? []
        current = c.lenient(Bool.self, forKey: .current) ?? false
        createdAt = c.lenient(Date.self, forKey: .createdAt)
        lastPushAt = c.lenient(Date.self, forKey: .lastPushAt)
    }
}

// MARK: - Events

struct Envelope: Codable, Sendable, Identifiable {
    var id: String
    var seq: Int
    var workspaceID: String
    var type: String
    var visibility: String?
    var topics: [String]?
    var data: JSONValue
    var occurredAt: Date?

    enum CodingKeys: String, CodingKey {
        case id, seq, type, visibility, topics, data
        case workspaceID = "workspace_id"
        case occurredAt = "occurred_at"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        seq = c.lenient(Int.self, forKey: .seq) ?? 0
        workspaceID = c.lenient(String.self, forKey: .workspaceID) ?? ""
        type = try c.decode(String.self, forKey: .type)
        visibility = c.lenient(String.self, forKey: .visibility)
        topics = c.lenient([String].self, forKey: .topics)
        data = c.lenient(JSONValue.self, forKey: .data) ?? .null
        occurredAt = c.lenient(Date.self, forKey: .occurredAt)
    }

    /// Decodes one member of `data` as a model.
    func member<T: Decodable>(_ key: String, as type: T.Type) -> T? {
        guard let value = data[key], !value.isNull else { return nil }
        return try? value.decode(T.self)
    }

    func string(_ key: String) -> String? { data[key]?.string }
}

struct EventsPage: Decodable, Sendable {
    var data: [Envelope]
    var latestSeq: Int
    var resync: Bool

    enum CodingKeys: String, CodingKey {
        case data, resync
        case latestSeq = "latest_seq"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        data = c.lenient([Envelope].self, forKey: .data) ?? []
        latestSeq = c.lenient(Int.self, forKey: .latestSeq) ?? 0
        resync = c.lenient(Bool.self, forKey: .resync) ?? false
    }
}

struct RealtimeToken: Decodable, Sendable {
    var token: String
    var url: String?
    var expiresAt: Date?

    enum CodingKeys: String, CodingKey {
        case token, url
        case expiresAt = "expires_at"
    }
}

// MARK: - Search

struct SearchResults: Decodable, Sendable {
    var tickets: [Ticket]

    enum CodingKeys: String, CodingKey { case tickets }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        tickets = c.lenient([Ticket].self, forKey: .tickets) ?? []
    }
}
