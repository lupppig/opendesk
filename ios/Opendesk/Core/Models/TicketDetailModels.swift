import Foundation

// Shapes the ticket screen reads beyond the ticket itself (contracts/api.md
// "Threads", "Drafts", links in contracts/integrations.md, and the AI routes
// in contracts/ai.md). Lenient like the rest: a member the server trims never
// fails the screen.

/// A side conversation: an email thread on the ticket with someone outside the team
/// (the API's `Thread`). Its messages are notes with `thread_id` set.
struct SideThread: Codable, Hashable, Sendable, Identifiable {
    enum Status: String, LenientEnum {
        case open, closed
        static let fallback = Status.open
    }

    var id: String
    var ticketID: String
    var subject: String
    var to: [String]
    var cc: [String]
    var mailboxID: String?
    var status: Status
    var includeHistory: Bool
    var createdBy: UserRef?
    var messageCount: Int
    var lastMessageAt: Date
    var createdAt: Date
    var updatedAt: Date

    enum CodingKeys: String, CodingKey {
        case id, subject, to, cc, status
        case ticketID = "ticket_id"
        case mailboxID = "mailbox_id"
        case includeHistory = "include_history"
        case createdBy = "created_by"
        case messageCount = "message_count"
        case lastMessageAt = "last_message_at"
        case createdAt = "created_at"
        case updatedAt = "updated_at"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        ticketID = c.lenient(String.self, forKey: .ticketID) ?? ""
        subject = c.lenient(String.self, forKey: .subject) ?? ""
        to = c.lenient([String].self, forKey: .to) ?? []
        cc = c.lenient([String].self, forKey: .cc) ?? []
        mailboxID = c.lenient(String.self, forKey: .mailboxID)
        status = c.lenient(Status.self, forKey: .status) ?? .open
        includeHistory = c.lenient(Bool.self, forKey: .includeHistory) ?? false
        createdBy = c.lenient(UserRef.self, forKey: .createdBy)
        messageCount = c.lenient(Int.self, forKey: .messageCount) ?? 0
        createdAt = c.lenient(Date.self, forKey: .createdAt) ?? .now
        updatedAt = c.lenient(Date.self, forKey: .updatedAt) ?? createdAt
        lastMessageAt = c.lenient(Date.self, forKey: .lastMessageAt) ?? updatedAt
    }

    init(id: String, ticketID: String, subject: String, to: [String], cc: [String] = [], mailboxID: String? = nil,
         status: Status = .open, includeHistory: Bool = false, createdBy: UserRef?, messageCount: Int, lastMessageAt: Date, createdAt: Date) {
        self.id = id
        self.ticketID = ticketID
        self.subject = subject
        self.to = to
        self.cc = cc
        self.mailboxID = mailboxID
        self.status = status
        self.includeHistory = includeHistory
        self.createdBy = createdBy
        self.messageCount = messageCount
        self.lastMessageAt = lastMessageAt
        self.createdAt = createdAt
        updatedAt = lastMessageAt
    }

    var displaySubject: String { subject.isEmpty ? "(no subject)" : subject }
}

/// `POST /v1/tickets/{id}/threads` answers the thread and its first message.
struct SideThreadCreated: Codable, Sendable {
    var thread: SideThread
    var message: Message
}

/// The ticket's shared draft: a reply staged by a teammate or an API client.
struct SharedDraft: Codable, Hashable, Sendable {
    struct By: Codable, Hashable, Sendable {
        var type: String
        var name: String
        var user: UserRef?
    }

    var ticketID: String
    var bodyHTML: String
    var bodyText: String
    var by: By
    var updatedAt: Date

    enum CodingKeys: String, CodingKey {
        case by
        case ticketID = "ticket_id"
        case bodyHTML = "body_html"
        case bodyText = "body_text"
        case updatedAt = "updated_at"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        ticketID = c.lenient(String.self, forKey: .ticketID) ?? ""
        bodyHTML = c.lenient(String.self, forKey: .bodyHTML) ?? ""
        bodyText = c.lenient(String.self, forKey: .bodyText) ?? ""
        by = c.lenient(By.self, forKey: .by) ?? By(type: "api", name: "", user: nil)
        updatedAt = c.lenient(Date.self, forKey: .updatedAt) ?? .now
    }

    init(ticketID: String, bodyHTML: String, by: By, updatedAt: Date) {
        self.ticketID = ticketID
        self.bodyHTML = bodyHTML
        bodyText = HTMLText.plain(from: bodyHTML)
        self.by = by
        self.updatedAt = updatedAt
    }

    /// Readable text for the composer.
    var text: String { bodyText.isEmpty ? HTMLText.plain(from: bodyHTML) : bodyText }
}

/// `GET /v1/tickets/{id}/draft`.
struct SharedDraftEnvelope: Decodable, Sendable {
    var draft: SharedDraft?

    enum CodingKeys: String, CodingKey { case draft }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        draft = c.lenient(SharedDraft.self, forKey: .draft)
    }
}

/// A link from the ticket to a record in a connected product (contracts/integrations.md `Link`).
struct TicketLink: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var ticketID: String
    var integrationID: String?
    var provider: String
    var url: String
    var title: String
    var actorType: String
    var createdBy: UserRef?
    var createdAt: Date

    enum CodingKeys: String, CodingKey {
        case id, provider, url, title
        case ticketID = "ticket_id"
        case integrationID = "integration_id"
        case actorType = "actor_type"
        case createdBy = "created_by"
        case createdAt = "created_at"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        ticketID = c.lenient(String.self, forKey: .ticketID) ?? ""
        integrationID = c.lenient(String.self, forKey: .integrationID)
        provider = c.lenient(String.self, forKey: .provider) ?? ""
        url = c.lenient(String.self, forKey: .url) ?? ""
        title = c.lenient(String.self, forKey: .title) ?? ""
        actorType = c.lenient(String.self, forKey: .actorType) ?? "user"
        createdBy = c.lenient(UserRef.self, forKey: .createdBy)
        createdAt = c.lenient(Date.self, forKey: .createdAt) ?? .now
    }

    init(id: String, ticketID: String, provider: String, url: String, title: String, actorType: String = "user", createdBy: UserRef?, createdAt: Date) {
        self.id = id
        self.ticketID = ticketID
        integrationID = nil
        self.provider = provider
        self.url = url
        self.title = title
        self.actorType = actorType
        self.createdBy = createdBy
        self.createdAt = createdAt
    }

    var displayTitle: String { title.isEmpty ? url : title }
}

/// A help center article an answer drew on.
struct KBSource: Codable, Hashable, Sendable, Identifiable {
    var articleID: String
    var title: String
    var url: String
    var id: String { articleID }

    enum CodingKeys: String, CodingKey {
        case title, url
        case articleID = "article_id"
    }
}

/// `POST /v1/tickets/{id}/ai/ask`.
struct TicketAnswer: Codable, Hashable, Sendable {
    var answerHTML: String
    var sources: [KBSource]

    enum CodingKeys: String, CodingKey {
        case sources
        case answerHTML = "answer_html"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        answerHTML = c.lenient(String.self, forKey: .answerHTML) ?? ""
        sources = c.lenient([KBSource].self, forKey: .sources) ?? []
    }

    init(answerHTML: String, sources: [KBSource]) {
        self.answerHTML = answerHTML
        self.sources = sources
    }
}

/// How Ollie rewrites the composer's text (`POST /v1/ai/rewrite` `mode`).
enum RewriteMode: String, CaseIterable, Identifiable, Sendable {
    case friendlier
    case moreFormal = "more_formal"
    case shorter, longer
    case fixGrammar = "fix_grammar"
    case simplify

    var id: String { rawValue }

    var title: String {
        switch self {
        case .friendlier: "Friendlier"
        case .moreFormal: "More formal"
        case .shorter: "Shorter"
        case .longer: "Longer"
        case .fixGrammar: "Fix spelling and grammar"
        case .simplify: "Simpler"
        }
    }

    var symbol: String {
        switch self {
        case .friendlier: "face.smiling"
        case .moreFormal: "briefcase"
        case .shorter: "arrow.down.right.and.arrow.up.left"
        case .longer: "arrow.up.left.and.arrow.down.right"
        case .fixGrammar: "checkmark.seal"
        case .simplify: "text.alignleft"
        }
    }
}

/// `POST /v1/ai/rewrite` → `{html}`; `/render` → `{body_html}`; `/v1/ai/translate` → `{html?, text?, source_language}`.
struct HTMLResult: Decodable, Sendable {
    var html: String

    enum CodingKeys: String, CodingKey {
        case html
        case bodyHTML = "body_html"
        case fullHTML = "full_html"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        html = c.lenient(String.self, forKey: .html) ?? c.lenient(String.self, forKey: .bodyHTML) ?? c.lenient(String.self, forKey: .fullHTML) ?? ""
    }
}

struct Translation: Codable, Hashable, Sendable {
    var html: String?
    var text: String?
    var sourceLanguage: String?

    enum CodingKeys: String, CodingKey {
        case html, text
        case sourceLanguage = "source_language"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        html = c.lenient(String.self, forKey: .html)
        text = c.lenient(String.self, forKey: .text)
        sourceLanguage = c.lenient(String.self, forKey: .sourceLanguage)
    }

    init(html: String?, text: String?, sourceLanguage: String?) {
        self.html = html
        self.text = text
        self.sourceLanguage = sourceLanguage
    }

    /// "Translated from Spanish" for a language code.
    var sourceName: String? {
        guard let code = sourceLanguage, !code.isEmpty else { return nil }
        return Locale.current.localizedString(forLanguageCode: code) ?? code
    }
}

/// An address the team sends from (`/v1/mailboxes`, the part the composer needs).
struct ReplyAddress: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var name: String
    var email: String
    var displayName: String

    enum CodingKeys: String, CodingKey {
        case id, name, email
        case displayName = "display_name"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = c.lenient(String.self, forKey: .name) ?? ""
        email = c.lenient(String.self, forKey: .email) ?? ""
        displayName = c.lenient(String.self, forKey: .displayName) ?? ""
    }

    init(id: String, name: String, email: String, displayName: String) {
        self.id = id
        self.name = name
        self.email = email
        self.displayName = displayName
    }

    var label: String { displayName.isEmpty ? (name.isEmpty ? email : name) : displayName }
}

/// `GET /v1/tickets/{id}/variables` → `{values, signature_html}`.
struct TicketVariables: Decodable, Sendable {
    var values: [String: String]
    var signatureHTML: String

    enum CodingKeys: String, CodingKey {
        case values
        case signatureHTML = "signature_html"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        let raw = c.lenient([String: JSONValue].self, forKey: .values) ?? [:]
        values = raw.compactMapValues { $0.string ?? $0.displayText }
        signatureHTML = c.lenient(String.self, forKey: .signatureHTML) ?? ""
    }

    init(values: [String: String], signatureHTML: String = "") {
        self.values = values
        self.signatureHTML = signatureHTML
    }
}
