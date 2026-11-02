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

/// Which list the knowledge base shows.
enum KBMode: String, CaseIterable, Identifiable, Sendable {
    case articles, changelog
    var id: String { rawValue }

    var title: String {
        switch self {
        case .articles: "Articles"
        case .changelog: "Changelog"
        }
    }
}

// MARK: - Changelog

/// A changelog entry (contracts/api.md `ChangelogEntry`): a product update the help center publishes.
struct ChangelogEntry: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var title: String
    var slug: String
    var excerpt: String
    var bodyHTML: String
    var categories: [String]
    var status: KBStatus
    /// The date it carries; a published entry dated later is scheduled.
    var publishedAt: Date?
    var author: UserRef?
    var brandID: String?
    var createdAt: Date
    var updatedAt: Date

    enum CodingKeys: String, CodingKey {
        case id, title, slug, excerpt, categories, status, author
        case bodyHTML = "body_html"
        case publishedAt = "published_at"
        case brandID = "brand_id"
        case createdAt = "created_at"
        case updatedAt = "updated_at"
    }

    init(id: String, title: String, slug: String, excerpt: String, bodyHTML: String, categories: [String], status: KBStatus, publishedAt: Date?, author: UserRef?, brandID: String? = nil, createdAt: Date, updatedAt: Date) {
        self.id = id
        self.title = title
        self.slug = slug
        self.excerpt = excerpt
        self.bodyHTML = bodyHTML
        self.categories = categories
        self.status = status
        self.publishedAt = publishedAt
        self.author = author
        self.brandID = brandID
        self.createdAt = createdAt
        self.updatedAt = updatedAt
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        title = c.lenient(String.self, forKey: .title) ?? ""
        slug = c.lenient(String.self, forKey: .slug) ?? ""
        excerpt = c.lenient(String.self, forKey: .excerpt) ?? ""
        bodyHTML = c.lenient(String.self, forKey: .bodyHTML) ?? ""
        categories = c.lenient([String].self, forKey: .categories) ?? []
        status = c.lenient(KBStatus.self, forKey: .status) ?? .draft
        publishedAt = c.lenient(Date.self, forKey: .publishedAt)
        author = c.lenient(UserRef.self, forKey: .author)
        brandID = c.lenient(String.self, forKey: .brandID)
        updatedAt = c.lenient(Date.self, forKey: .updatedAt) ?? .now
        createdAt = c.lenient(Date.self, forKey: .createdAt) ?? updatedAt
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(id, forKey: .id)
        try c.encode(title, forKey: .title)
        try c.encode(slug, forKey: .slug)
        try c.encode(excerpt, forKey: .excerpt)
        try c.encode(bodyHTML, forKey: .bodyHTML)
        try c.encode(categories, forKey: .categories)
        try c.encode(status, forKey: .status)
        try c.encode(publishedAt, forKey: .publishedAt)
        try c.encode(author, forKey: .author)
        try c.encode(brandID, forKey: .brandID)
        try c.encode(createdAt, forKey: .createdAt)
        try c.encode(updatedAt, forKey: .updatedAt)
    }

    var displayTitle: String { title.isEmpty ? "Untitled" : title }

    /// Draft, live, or published but waiting for its date.
    func state(now: Date = AppClock.now) -> ChangelogState {
        guard status == .published else { return .draft }
        if let publishedAt, publishedAt > now { return .scheduled }
        return .published
    }

    /// The date the list sorts by: the one it carries, else the last edit (drafts).
    var sortDate: Date { publishedAt ?? updatedAt }
}

/// What a reader sees of an entry.
enum ChangelogState: String, CaseIterable, Identifiable, Sendable {
    case draft, published, scheduled
    var id: String { rawValue }

    var title: String {
        switch self {
        case .draft: "Draft"
        case .published: "Published"
        case .scheduled: "Scheduled"
        }
    }

    var tone: Tone {
        switch self {
        case .draft: .amber
        case .published: .green
        case .scheduled: .blue
        }
    }
}

/// The status filter of the changelog list.
enum ChangelogFilter: String, CaseIterable, Identifiable, Sendable {
    case all, published, scheduled, draft
    var id: String { rawValue }

    var title: String {
        switch self {
        case .all: "All"
        case .published: "Published"
        case .scheduled: "Scheduled"
        case .draft: "Drafts"
        }
    }

    func matches(_ entry: ChangelogEntry, now: Date = AppClock.now) -> Bool {
        switch self {
        case .all: true
        case .published: entry.state(now: now) == .published
        case .scheduled: entry.state(now: now) == .scheduled
        case .draft: entry.status == .draft
        }
    }
}

/// `GET /v1/changelog/categories`: the names the workspace's entries use, most used first.
struct ChangelogCategory: Decodable, Hashable, Sendable, Identifiable {
    var name: String
    var count: Int
    var id: String { name.lowercased() }

    enum CodingKeys: String, CodingKey { case name, count }

    init(name: String, count: Int) {
        self.name = name
        self.count = count
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        name = c.lenient(String.self, forKey: .name) ?? ""
        count = c.lenient(Int.self, forKey: .count) ?? 0
    }
}

/// Body of `POST /v1/changelog/entries` and its PATCH. The excerpt (derived from the
/// body unless written) and the date (set when first published unless given) are
/// sent only when they were changed; `datePatched` with a nil date dates it when it
/// is next published.
struct ChangelogEntryInput: Encodable, Sendable {
    var title: String
    var slug: String?
    var bodyHTML: String
    var categories: [String]
    var status: KBStatus
    var excerpt: String?
    var publishedAt: Date?
    var datePatched = false

    enum CodingKeys: String, CodingKey {
        case title, slug, categories, status, excerpt
        case bodyHTML = "body_html"
        case publishedAt = "published_at"
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(title, forKey: .title)
        if let slug, !slug.isEmpty { try c.encode(slug, forKey: .slug) }
        try c.encode(bodyHTML, forKey: .bodyHTML)
        try c.encode(categories, forKey: .categories)
        try c.encode(status, forKey: .status)
        if let excerpt { try c.encode(excerpt, forKey: .excerpt) }
        if datePatched { try c.encode(publishedAt, forKey: .publishedAt) }
    }
}

/// Changelog categories as the app applies them (web `components/kb/changelog.ts`):
/// at most five, 32 characters each, compared without case.
enum ChangelogCategories {
    static let max = 5
    static let maxLength = 32

    /// A typed name with its whitespace collapsed; nil when nothing is left.
    static func clean(_ name: String) -> String? {
        let value = name.split(whereSeparator: \.isWhitespace).joined(separator: " ")
        return value.isEmpty ? nil : value
    }

    /// The list with the name added, or why it can't be.
    static func add(_ name: String, to list: [String]) -> Result<[String], Problem> {
        guard let value = clean(name) else { return .failure(.empty) }
        if value.count > maxLength { return .failure(.tooLong) }
        if list.contains(where: { $0.caseInsensitiveCompare(value) == .orderedSame }) { return .failure(.duplicate) }
        if list.count >= max { return .failure(.full) }
        return .success(list + [value])
    }

    enum Problem: Error {
        case empty, tooLong, duplicate, full

        var message: String {
            switch self {
            case .empty: "Type a name."
            case .tooLong: "Keep it to \(ChangelogCategories.maxLength) characters."
            case .duplicate: "That category is already here."
            case .full: "Up to \(ChangelogCategories.max) categories."
            }
        }
    }

    /// A stable tone for a name (web `toneFor`), so a category looks the same everywhere.
    static func tone(_ name: String) -> Tone {
        let tones: [Tone] = [.blue, .indigo, .violet, .pink, .orange, .amber, .lime, .green, .teal, .cyan]
        var hash: Int32 = 0
        for unit in (name.isEmpty ? "?" : name).utf16 { hash = hash &* 31 &+ Int32(unit) }
        let positive = hash == Int32.min ? Int(Int32.max) + 1 : Int(abs(hash))
        return tones[positive % tones.count]
    }
}
