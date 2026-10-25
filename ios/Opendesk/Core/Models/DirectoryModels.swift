import Foundation

/// A company as a contact or a list carries it (contracts/api.md `CompanyRef`).
struct CompanyRef: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var name: String
    var domain: String
    var avatarURL: String?

    enum CodingKeys: String, CodingKey {
        case id, name, domain
        case avatarURL = "avatar_url"
    }
}

/// A customer (contracts/api.md `Contact`, `/v1/contacts`).
struct Contact: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var email: String?
    var name: String
    var phone: String
    var avatarURL: String?
    var avatarSource: String?
    var company: CompanyRef?
    var externalID: String?
    var notes: String
    var tags: [TagRef]
    var fields: [String: JSONValue]
    var blocked: Bool
    var ticketCount: Int
    var createdAt: Date
    var updatedAt: Date?

    enum CodingKeys: String, CodingKey {
        case id, email, name, phone, company, notes, tags, fields, blocked
        case avatarURL = "avatar_url"
        case avatarSource = "avatar_source"
        case externalID = "external_id"
        case ticketCount = "ticket_count"
        case createdAt = "created_at"
        case updatedAt = "updated_at"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        email = try c.decodeIfPresent(String.self, forKey: .email)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        phone = try c.decodeIfPresent(String.self, forKey: .phone) ?? ""
        avatarURL = try c.decodeIfPresent(String.self, forKey: .avatarURL)
        avatarSource = try c.decodeIfPresent(String.self, forKey: .avatarSource)
        company = try c.decodeIfPresent(CompanyRef.self, forKey: .company)
        externalID = try c.decodeIfPresent(String.self, forKey: .externalID)
        notes = try c.decodeIfPresent(String.self, forKey: .notes) ?? ""
        tags = try c.decodeIfPresent([TagRef].self, forKey: .tags) ?? []
        fields = try c.decodeIfPresent([String: JSONValue].self, forKey: .fields) ?? [:]
        blocked = try c.decodeIfPresent(Bool.self, forKey: .blocked) ?? false
        ticketCount = try c.decodeIfPresent(Int.self, forKey: .ticketCount) ?? 0
        createdAt = try c.decode(Date.self, forKey: .createdAt)
        updatedAt = try c.decodeIfPresent(Date.self, forKey: .updatedAt)
    }

    init(id: String, email: String?, name: String, phone: String = "", avatarURL: String? = nil, avatarSource: String? = nil,
         company: CompanyRef? = nil, externalID: String? = nil, notes: String = "", tags: [TagRef] = [],
         fields: [String: JSONValue] = [:], blocked: Bool = false, ticketCount: Int = 0, createdAt: Date, updatedAt: Date? = nil) {
        self.id = id
        self.email = email
        self.name = name
        self.phone = phone
        self.avatarURL = avatarURL
        self.avatarSource = avatarSource
        self.company = company
        self.externalID = externalID
        self.notes = notes
        self.tags = tags
        self.fields = fields
        self.blocked = blocked
        self.ticketCount = ticketCount
        self.createdAt = createdAt
        self.updatedAt = updatedAt
    }

    var displayName: String {
        if !name.isEmpty { return name }
        return email ?? "Unknown"
    }

    var ref: ContactRef { ContactRef(id: id, email: email, name: name, avatarURL: avatarURL) }
}

/// A company (`/v1/companies`).
struct Company: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var name: String
    var domain: String
    var avatarURL: String?
    var avatarSource: String?
    var notes: String
    var tags: [TagRef]
    var fields: [String: JSONValue]
    var contactCount: Int
    var createdAt: Date
    var updatedAt: Date?

    enum CodingKeys: String, CodingKey {
        case id, name, domain, notes, tags, fields
        case avatarURL = "avatar_url"
        case avatarSource = "avatar_source"
        case contactCount = "contact_count"
        case createdAt = "created_at"
        case updatedAt = "updated_at"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        domain = try c.decodeIfPresent(String.self, forKey: .domain) ?? ""
        avatarURL = try c.decodeIfPresent(String.self, forKey: .avatarURL)
        avatarSource = try c.decodeIfPresent(String.self, forKey: .avatarSource)
        notes = try c.decodeIfPresent(String.self, forKey: .notes) ?? ""
        tags = try c.decodeIfPresent([TagRef].self, forKey: .tags) ?? []
        fields = try c.decodeIfPresent([String: JSONValue].self, forKey: .fields) ?? [:]
        contactCount = try c.decodeIfPresent(Int.self, forKey: .contactCount) ?? 0
        createdAt = try c.decode(Date.self, forKey: .createdAt)
        updatedAt = try c.decodeIfPresent(Date.self, forKey: .updatedAt)
    }

    init(id: String, name: String, domain: String, avatarURL: String? = nil, avatarSource: String? = nil, notes: String = "",
         tags: [TagRef] = [], fields: [String: JSONValue] = [:], contactCount: Int = 0, createdAt: Date, updatedAt: Date? = nil) {
        self.id = id
        self.name = name
        self.domain = domain
        self.avatarURL = avatarURL
        self.avatarSource = avatarSource
        self.notes = notes
        self.tags = tags
        self.fields = fields
        self.contactCount = contactCount
        self.createdAt = createdAt
        self.updatedAt = updatedAt
    }

    var ref: CompanyRef { CompanyRef(id: id, name: name, domain: domain, avatarURL: avatarURL) }
}

/// One choice of a select or multi-select field.
struct FieldOption: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var label: String
    var color: String
}

/// A custom field definition (`GET /v1/custom-fields`) for tickets, contacts or companies.
struct CustomField: Codable, Hashable, Sendable, Identifiable {
    enum Kind: String, Codable, Sendable {
        case text, longText = "long_text", number, select, multiSelect = "multi_select", date, checkbox, url, email, user
    }

    var id: String
    var entity: String
    var key: String
    var name: String
    var type: Kind
    var options: [FieldOption]
    var requiredOnSolve: Bool
    var description: String
    var position: Int
    var archived: Bool

    enum CodingKeys: String, CodingKey {
        case id, entity, key, name, type, options, description, position, archived
        case requiredOnSolve = "required_on_solve"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        entity = try c.decodeIfPresent(String.self, forKey: .entity) ?? "ticket"
        key = try c.decode(String.self, forKey: .key)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? key
        type = (try? c.decode(Kind.self, forKey: .type)) ?? .text
        options = try c.decodeIfPresent([FieldOption].self, forKey: .options) ?? []
        requiredOnSolve = try c.decodeIfPresent(Bool.self, forKey: .requiredOnSolve) ?? false
        description = try c.decodeIfPresent(String.self, forKey: .description) ?? ""
        position = try c.decodeIfPresent(Int.self, forKey: .position) ?? 0
        archived = try c.decodeIfPresent(Bool.self, forKey: .archived) ?? false
    }

    init(id: String, entity: String, key: String, name: String, type: Kind, options: [FieldOption] = [],
         requiredOnSolve: Bool = false, description: String = "", position: Int = 0, archived: Bool = false) {
        self.id = id
        self.entity = entity
        self.key = key
        self.name = name
        self.type = type
        self.options = options
        self.requiredOnSolve = requiredOnSolve
        self.description = description
        self.position = position
        self.archived = archived
    }
}

/// Whether Collab mail is on for the workspace (`GET /v1/collab`, the part the menu needs).
struct CollabAvailability: Codable, Hashable, Sendable {
    var enabled: Bool
    var reason: String?
    var accounts: Int

    init(enabled: Bool, reason: String? = nil, accounts: Int = 0) {
        self.enabled = enabled
        self.reason = reason
        self.accounts = accounts
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        enabled = try c.decodeIfPresent(Bool.self, forKey: .enabled) ?? false
        reason = try c.decodeIfPresent(String.self, forKey: .reason)
        accounts = try c.decodeIfPresent(Int.self, forKey: .accounts) ?? 0
    }

    static let off = CollabAvailability(enabled: false)
}

// MARK: - Directory helpers

extension Contact {
    /// The first word of the name, for "Email Dana"; the email's local part without a name.
    var firstName: String {
        if let first = name.split(separator: " ").first { return String(first) }
        return email.map { String($0.split(separator: "@").first ?? "") } ?? "them"
    }

    /// The email's domain, lowercased (`oakridgehealth.org`).
    var emailDomain: String? {
        guard let email, let at = email.lastIndex(of: "@") else { return nil }
        return String(email[email.index(after: at)...]).lowercased()
    }
}

/// A contact or a company: the two records the directory edits the same way.
enum DirectoryEntity: String, Sendable {
    case contact, company

    /// The custom field entity (`CustomField.entity`).
    var fieldEntity: String { rawValue }
    /// The label scope (`Tag.applies_to`).
    var tagScope: String { self == .contact ? "contacts" : "companies" }
    /// The collection path under `/v1`.
    var path: String { self == .contact ? "v1/contacts" : "v1/companies" }
}

/// A write body for a contact or company: only the members set are sent, `null` clears.
struct DirectoryPatch: Encodable, Sendable {
    private(set) var values: [String: JSONValue] = [:]

    init() {}

    var isEmpty: Bool { values.isEmpty }

    subscript(key: String) -> JSONValue? {
        get { values[key] }
        set { values[key] = newValue }
    }

    /// A text member; `clearsWhenEmpty` sends `null` for an empty value (email, company, external id).
    mutating func text(_ key: String, _ value: String, clearsWhenEmpty: Bool = false) {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        values[key] = trimmed.isEmpty && clearsWhenEmpty ? .null : .string(trimmed)
    }

    /// Replaces the labels by name.
    static func tags(_ names: [String]) -> DirectoryPatch {
        var patch = DirectoryPatch()
        patch["tags"] = .array(names.map(JSONValue.string))
        return patch
    }

    /// Sets one custom field (`nil` clears it).
    static func field(_ key: String, _ value: JSONValue?) -> DirectoryPatch {
        var patch = DirectoryPatch()
        patch["fields"] = .object([key: value ?? .null])
        return patch
    }

    func encode(to encoder: Encoder) throws {
        try JSONValue.object(values).encode(to: encoder)
    }
}

/// How the contacts list is ordered: newest first (the API's order) or by name in letter sections.
enum ContactSort: String, CaseIterable, Identifiable, Sendable {
    case recent, name

    var id: String { rawValue }

    var title: String {
        switch self {
        case .recent: "Recently added"
        case .name: "Name"
        }
    }

    var symbol: String {
        switch self {
        case .recent: "clock"
        case .name: "textformat"
        }
    }
}

extension CustomField.Kind {
    var symbol: String {
        switch self {
        case .text: "textformat"
        case .longText: "text.alignleft"
        case .number: "number"
        case .select: "chevron.up.chevron.down"
        case .multiSelect: "checklist"
        case .date: "calendar"
        case .checkbox: "checkmark.square"
        case .url: "link"
        case .email: "at"
        case .user: "person.crop.circle"
        }
    }
}
