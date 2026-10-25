import Foundation

// Knowledge base shapes from contracts/api.md (`KbCollection`, `KbArticle`) and
// contracts/ai.md (the KB draft). Keys are copied through explicit CodingKeys.

enum KBStatus: String, LenientEnum, CaseIterable, Identifiable {
    case draft, published
    static let fallback = KBStatus.draft
    var id: String { rawValue }

    var title: String {
        switch self {
        case .draft: "Draft"
        case .published: "Published"
        }
    }

    var tone: Tone { self == .published ? .green : .amber }
}

struct KBCollection: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var name: String
    var slug: String
    var description: String
    var position: Int
    var articleCount: Int
    var brandID: String?

    enum CodingKeys: String, CodingKey {
        case id, name, slug, description, position
        case articleCount = "article_count"
        case brandID = "brand_id"
    }

    init(id: String, name: String, slug: String, description: String, position: Int, articleCount: Int, brandID: String? = nil) {
        self.id = id
        self.name = name
        self.slug = slug
        self.description = description
        self.position = position
        self.articleCount = articleCount
        self.brandID = brandID
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = c.lenient(String.self, forKey: .name) ?? ""
        slug = c.lenient(String.self, forKey: .slug) ?? ""
        description = c.lenient(String.self, forKey: .description) ?? ""
        position = c.lenient(Int.self, forKey: .position) ?? 0
        articleCount = c.lenient(Int.self, forKey: .articleCount) ?? 0
        brandID = c.lenient(String.self, forKey: .brandID)
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(id, forKey: .id)
        try c.encode(name, forKey: .name)
        try c.encode(slug, forKey: .slug)
        try c.encode(description, forKey: .description)
        try c.encode(position, forKey: .position)
        try c.encode(articleCount, forKey: .articleCount)
        try c.encode(brandID, forKey: .brandID)
    }
}

struct KBArticle: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var collectionID: String?
    var title: String
    var slug: String
    var excerpt: String
    var bodyHTML: String
    var status: KBStatus
    var author: UserRef?
    var views: Int
    var publishedAt: Date?
    var updatedAt: Date
    var brandID: String?

    enum CodingKeys: String, CodingKey {
        case id, title, slug, excerpt, status, author, views
        case collectionID = "collection_id"
        case bodyHTML = "body_html"
        case publishedAt = "published_at"
        case updatedAt = "updated_at"
        case brandID = "brand_id"
    }

    init(id: String, collectionID: String?, title: String, slug: String, excerpt: String, bodyHTML: String, status: KBStatus, author: UserRef?, views: Int, publishedAt: Date?, updatedAt: Date, brandID: String? = nil) {
        self.id = id
        self.collectionID = collectionID
        self.title = title
        self.slug = slug
        self.excerpt = excerpt
        self.bodyHTML = bodyHTML
        self.status = status
        self.author = author
        self.views = views
        self.publishedAt = publishedAt
        self.updatedAt = updatedAt
        self.brandID = brandID
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        collectionID = c.lenient(String.self, forKey: .collectionID)
        title = c.lenient(String.self, forKey: .title) ?? ""
        slug = c.lenient(String.self, forKey: .slug) ?? ""
        excerpt = c.lenient(String.self, forKey: .excerpt) ?? ""
        bodyHTML = c.lenient(String.self, forKey: .bodyHTML) ?? ""
        status = c.lenient(KBStatus.self, forKey: .status) ?? .draft
        author = c.lenient(UserRef.self, forKey: .author)
        views = c.lenient(Int.self, forKey: .views) ?? 0
        publishedAt = c.lenient(Date.self, forKey: .publishedAt)
        updatedAt = c.lenient(Date.self, forKey: .updatedAt) ?? .now
        brandID = c.lenient(String.self, forKey: .brandID)
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(id, forKey: .id)
        try c.encode(collectionID, forKey: .collectionID)
        try c.encode(title, forKey: .title)
        try c.encode(slug, forKey: .slug)
        try c.encode(excerpt, forKey: .excerpt)
        try c.encode(bodyHTML, forKey: .bodyHTML)
        try c.encode(status, forKey: .status)
        try c.encode(author, forKey: .author)
        try c.encode(views, forKey: .views)
        try c.encode(publishedAt, forKey: .publishedAt)
        try c.encode(updatedAt, forKey: .updatedAt)
        try c.encode(brandID, forKey: .brandID)
    }

    var displayTitle: String { title.isEmpty ? "Untitled" : title }
}

/// Body of `POST /v1/kb/articles` and `PATCH /v1/kb/articles/{id}`.
struct KBArticleInput: Encodable, Sendable {
    var title: String
    var slug: String?
    var excerpt: String
    var collectionID: String?
    var bodyHTML: String
    var status: KBStatus
    /// On update an empty `collection_id` takes the article out of its collection (null means unchanged).
    var isUpdate: Bool

    enum CodingKeys: String, CodingKey {
        case title, slug, excerpt, status
        case collectionID = "collection_id"
        case bodyHTML = "body_html"
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(title, forKey: .title)
        if let slug, !slug.isEmpty { try c.encode(slug, forKey: .slug) }
        try c.encode(excerpt, forKey: .excerpt)
        if isUpdate {
            try c.encode(collectionID ?? "", forKey: .collectionID)
        } else {
            try c.encode(collectionID, forKey: .collectionID)
        }
        try c.encode(bodyHTML, forKey: .bodyHTML)
        try c.encode(status, forKey: .status)
    }
}

/// Body of `POST /v1/kb/collections` and its PATCH.
struct KBCollectionInput: Encodable, Sendable {
    var name: String
    var description: String
}

/// `POST /v1/kb/articles/ai/draft` (contracts/ai.md): a draft handed to the editor, never saved by the server.
struct KBDraftRequest: Encodable, Sendable {
    var ticketIDs: [String]?
    var topic: String?

    enum CodingKeys: String, CodingKey {
        case topic
        case ticketIDs = "ticket_ids"
    }
}

struct KBArticleDraft: Codable, Hashable, Sendable {
    var title: String
    var bodyHTML: String
    var excerpt: String

    enum CodingKeys: String, CodingKey {
        case title, excerpt
        case bodyHTML = "body_html"
    }
}

/// The parts of a brand (contracts/teams.md) the knowledge base links to: its help center.
struct KBBrand: Decodable, Hashable, Sendable, Identifiable {
    struct HelpCenter: Decodable, Hashable, Sendable {
        var enabled: Bool
        var url: String

        enum CodingKeys: String, CodingKey { case enabled, url }

        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            enabled = c.lenient(Bool.self, forKey: .enabled) ?? false
            url = c.lenient(String.self, forKey: .url) ?? ""
        }
    }

    var id: String
    var isDefault: Bool
    var helpCenter: HelpCenter?

    enum CodingKeys: String, CodingKey {
        case id
        case isDefault = "is_default"
        case helpCenter = "help_center"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        isDefault = c.lenient(Bool.self, forKey: .isDefault) ?? false
        helpCenter = c.lenient(HelpCenter.self, forKey: .helpCenter)
    }
}

/// The status filter of the article list.
enum KBStatusFilter: String, CaseIterable, Identifiable, Sendable {
    case all, published, draft
    var id: String { rawValue }

    var title: String {
        switch self {
        case .all: "All"
        case .published: "Published"
        case .draft: "Drafts"
        }
    }
}

enum KBSlug {
    /// The web's `slugify`: lowercase ASCII words joined by dashes, at most 80 characters.
    static func make(_ text: String) -> String {
        let folded = text.folding(options: [.diacriticInsensitive, .caseInsensitive, .widthInsensitive], locale: .init(identifier: "en_US_POSIX")).lowercased()
        var out = ""
        var dash = false
        for scalar in folded.unicodeScalars {
            if (scalar >= "a" && scalar <= "z") || (scalar >= "0" && scalar <= "9") {
                if dash, !out.isEmpty { out.append("-") }
                dash = false
                out.unicodeScalars.append(scalar)
            } else {
                dash = true
            }
        }
        return String(out.prefix(80)).trimmingCharacters(in: CharacterSet(charactersIn: "-"))
    }
}
