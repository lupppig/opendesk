import Foundation

// Collab mail shapes (contracts/collab.md "Shapes"). Members the app can live
// without decode leniently, so a server that grows a field never fails a screen.
// Memberwise initialisers stay (the decoders live in extensions) for the demo.

/// Who sees an account or a conversation on its own.
struct MailAudience: Codable, Hashable, Sendable {
    var everyone: Bool = false
    var teamIDs: [String] = []
    var userIDs: [String] = []

    enum CodingKeys: String, CodingKey {
        case everyone
        case teamIDs = "team_ids"
        case userIDs = "user_ids"
    }

    var isEmpty: Bool { !everyone && teamIDs.isEmpty && userIDs.isEmpty }

    static let nobody = MailAudience()
}

extension MailAudience {
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        everyone = c.lenient(Bool.self, forKey: .everyone) ?? false
        teamIDs = c.lenient([String].self, forKey: .teamIDs) ?? []
        userIDs = c.lenient([String].self, forKey: .userIDs) ?? []
    }
}

/// A name and an address.
struct MailAddress: Codable, Hashable, Sendable {
    var name: String = ""
    var email: String

    /// The name, else the address.
    var display: String { name.isEmpty ? email : name }

    /// The first word of the name, else the part before the @.
    var short: String {
        if let first = name.split(separator: " ").first { return String(first) }
        return String(email.split(separator: "@").first ?? Substring(email))
    }
}

extension MailAddress {
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        name = c.lenient(String.self, forKey: .name) ?? ""
        email = c.lenient(String.self, forKey: .email) ?? ""
    }
}

enum MailProvider: String, LenientEnum, CaseIterable {
    case google, microsoft, imap
    static let fallback = MailProvider.imap

    var title: String {
        switch self {
        case .google: "Gmail"
        case .microsoft: "Outlook"
        case .imap: "IMAP"
        }
    }
}

/// An address an account sends from (contracts/collab.md "Addresses").
struct MailIdentity: Codable, Hashable, Sendable, Identifiable {
    var identityID: String?
    var email: String
    var displayName: String = ""
    var signatureHTML: String = ""
    var primary: Bool = false
    var isDefault: Bool = false
    var canSend: Bool = true
    var fromName: String = ""
    var filledSignatureHTML: String?

    enum CodingKeys: String, CodingKey {
        case email, primary
        case identityID = "id"
        case displayName = "display_name"
        case signatureHTML = "signature_html"
        case isDefault = "default"
        case canSend = "can_send"
        case fromName = "from_name"
        case filledSignatureHTML = "filled_signature_html"
    }

    var id: String { email }

    /// The signature as the composer puts it in: filled for the caller when it has variables.
    var signature: String { filledSignatureHTML ?? signatureHTML }
}

extension MailIdentity {
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        identityID = c.lenient(String.self, forKey: .identityID)
        email = (c.lenient(String.self, forKey: .email) ?? "").lowercased()
        displayName = c.lenient(String.self, forKey: .displayName) ?? ""
        signatureHTML = c.lenient(String.self, forKey: .signatureHTML) ?? ""
        primary = c.lenient(Bool.self, forKey: .primary) ?? false
        isDefault = c.lenient(Bool.self, forKey: .isDefault) ?? false
        canSend = c.lenient(Bool.self, forKey: .canSend) ?? true
        fromName = c.lenient(String.self, forKey: .fromName) ?? ""
        filledSignatureHTML = c.lenient(String.self, forKey: .filledSignatureHTML)
    }
}

/// The latest history import of an account (contracts/collab.md "History"), the part the app reads.
struct MailHistory: Codable, Hashable, Sendable {
    var status: String
    var since: Date?

    /// Every mail of the account is here: nothing older waits at the provider.
    var importedAll: Bool { status == "done" && since == nil }
}

extension MailHistory {
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        status = c.lenient(String.self, forKey: .status) ?? ""
        since = c.lenient(Date.self, forKey: .since)
    }
}

/// A member's own mail account (`GET /v1/collab/accounts`).
struct MailAccount: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var provider: MailProvider
    var email: String
    var name: String = ""
    var displayName: String = ""
    var color: String = "gray"
    var owner: UserRef?
    var visibility: String = "private"
    var sharedWith: MailAudience = .nobody
    var signatureHTML: String = ""
    var status: String = "active"
    var reconnect: Bool = false
    var lastError: String = ""
    var lastSyncedAt: Date?
    var canManage: Bool = false
    var unread: Int = 0
    var identities: [MailIdentity] = []
    var defaultFrom: String = ""
    var history: MailHistory?
    var oldestMessageAt: Date?

    enum CodingKeys: String, CodingKey {
        case id, provider, email, name, color, owner, visibility, status, reconnect, unread, identities, history
        case displayName = "display_name"
        case sharedWith = "shared_with"
        case signatureHTML = "signature_html"
        case lastError = "last_error"
        case lastSyncedAt = "last_synced_at"
        case canManage = "can_manage"
        case defaultFrom = "default_from"
        case oldestMessageAt = "oldest_message_at"
    }

    /// How the app names it: its name, else the address.
    var label: String { name.isEmpty ? email : name }
    var isPrivate: Bool { visibility != "shared" }
    var tone: Tone { MailTone.of(color) }

    /// Every address it sends from, its own included, lower case.
    var addresses: Set<String> {
        Set([email.lowercased()] + identities.map { $0.email.lowercased() })
    }
}

extension MailAccount {
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        provider = c.lenient(MailProvider.self, forKey: .provider) ?? .imap
        email = c.lenient(String.self, forKey: .email) ?? ""
        name = c.lenient(String.self, forKey: .name) ?? ""
        displayName = c.lenient(String.self, forKey: .displayName) ?? ""
        color = c.lenient(String.self, forKey: .color) ?? "gray"
        owner = c.lenient(UserRef.self, forKey: .owner)
        visibility = c.lenient(String.self, forKey: .visibility) ?? "private"
        sharedWith = c.lenient(MailAudience.self, forKey: .sharedWith) ?? .nobody
        signatureHTML = c.lenient(String.self, forKey: .signatureHTML) ?? ""
        status = c.lenient(String.self, forKey: .status) ?? "active"
        reconnect = c.lenient(Bool.self, forKey: .reconnect) ?? false
        lastError = c.lenient(String.self, forKey: .lastError) ?? ""
        lastSyncedAt = c.lenient(Date.self, forKey: .lastSyncedAt)
        canManage = c.lenient(Bool.self, forKey: .canManage) ?? false
        unread = c.lenient(Int.self, forKey: .unread) ?? 0
        identities = c.lenient([MailIdentity].self, forKey: .identities) ?? []
        defaultFrom = c.lenient(String.self, forKey: .defaultFrom) ?? ""
        history = c.lenient(MailHistory.self, forKey: .history)
        oldestMessageAt = c.lenient(Date.self, forKey: .oldestMessageAt)
    }
}

/// The account as a conversation carries it (`MailThreadDetail.account`).
struct MailThreadAccount: Codable, Hashable, Sendable {
    var id: String
    var email: String
    var name: String = ""
    var displayName: String = ""
    var color: String = "gray"
    var provider: MailProvider = .imap
    var signatureHTML: String = ""
    var identities: [MailIdentity] = []
    var defaultFrom: String = ""

    enum CodingKeys: String, CodingKey {
        case id, email, name, color, provider, identities
        case displayName = "display_name"
        case signatureHTML = "signature_html"
        case defaultFrom = "default_from"
    }

    var label: String { name.isEmpty ? email : name }
    var tone: Tone { MailTone.of(color) }

    var addresses: Set<String> {
        Set([email.lowercased()] + identities.map { $0.email.lowercased() })
    }
}

extension MailThreadAccount {
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        email = c.lenient(String.self, forKey: .email) ?? ""
        name = c.lenient(String.self, forKey: .name) ?? ""
        displayName = c.lenient(String.self, forKey: .displayName) ?? ""
        color = c.lenient(String.self, forKey: .color) ?? "gray"
        provider = c.lenient(MailProvider.self, forKey: .provider) ?? .imap
        signatureHTML = c.lenient(String.self, forKey: .signatureHTML) ?? ""
        identities = c.lenient([MailIdentity].self, forKey: .identities) ?? []
        defaultFrom = c.lenient(String.self, forKey: .defaultFrom) ?? ""
    }
}

/// The special folders every provider has.
enum MailFolderRole: String, LenientEnum {
    case inbox, sent, drafts, archive, trash, spam, starred, important, all, other
    static let fallback = MailFolderRole.other
}

/// A provider folder or label.
struct MailFolder: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var accountID: String
    var name: String
    var path: String = ""
    var role: MailFolderRole?
    var kind: String = "folder"
    var unread: Int = 0
    var total: Int = 0
    var color: String?
    var canRename: Bool = false

    enum CodingKeys: String, CodingKey {
        case id, name, path, role, kind, unread, total, color
        case accountID = "account_id"
        case canRename = "can_rename"
    }

    var isLabel: Bool { kind == "label" }
    /// A folder or label the person made (not inbox, sent and the like).
    var isCustom: Bool { role == nil || role == .other }
}

extension MailFolder {
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        accountID = c.lenient(String.self, forKey: .accountID) ?? ""
        name = c.lenient(String.self, forKey: .name) ?? ""
        path = c.lenient(String.self, forKey: .path) ?? ""
        role = c.lenient(MailFolderRole.self, forKey: .role)
        kind = c.lenient(String.self, forKey: .kind) ?? "folder"
        unread = c.lenient(Int.self, forKey: .unread) ?? 0
        total = c.lenient(Int.self, forKey: .total) ?? 0
        color = c.lenient(String.self, forKey: .color)
        canRename = c.lenient(Bool.self, forKey: .canRename) ?? false
    }
}

/// A conversation in a list.
struct MailThread: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var accountID: String
    var subject: String = ""
    var snippet: String = ""
    var participants: [MailAddress] = []
    var lastMessageAt: Date
    var messageCount: Int = 1
    var unread: Bool = false
    var starred: Bool = false
    var hasAttachments: Bool = false
    var folderIDs: [String] = []
    var shared: Bool = false
    var assignees: [UserRef] = []
    var commentCount: Int = 0
    var toAddress: String?

    enum CodingKeys: String, CodingKey {
        case id, subject, snippet, participants, unread, starred, shared, assignees
        case accountID = "account_id"
        case lastMessageAt = "last_message_at"
        case messageCount = "message_count"
        case hasAttachments = "has_attachments"
        case folderIDs = "folder_ids"
        case commentCount = "comment_count"
        case toAddress = "to_address"
    }

    var displaySubject: String { subject.isEmpty ? "(no subject)" : subject }
}

extension MailThread {
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        accountID = c.lenient(String.self, forKey: .accountID) ?? ""
        subject = c.lenient(String.self, forKey: .subject) ?? ""
        snippet = c.lenient(String.self, forKey: .snippet) ?? ""
        participants = c.lenient([MailAddress].self, forKey: .participants) ?? []
        lastMessageAt = c.lenient(Date.self, forKey: .lastMessageAt) ?? .distantPast
        messageCount = c.lenient(Int.self, forKey: .messageCount) ?? 1
        unread = c.lenient(Bool.self, forKey: .unread) ?? false
        starred = c.lenient(Bool.self, forKey: .starred) ?? false
        hasAttachments = c.lenient(Bool.self, forKey: .hasAttachments) ?? false
        folderIDs = c.lenient([String].self, forKey: .folderIDs) ?? []
        shared = c.lenient(Bool.self, forKey: .shared) ?? false
        assignees = c.lenient([UserRef].self, forKey: .assignees) ?? []
        commentCount = c.lenient(Int.self, forKey: .commentCount) ?? 0
        toAddress = c.lenient(String.self, forKey: .toAddress)
    }
}

/// A file on a message. `url` is signed and valid for an hour.
struct MailAttachment: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var filename: String
    var contentType: String = "application/octet-stream"
    var size: Int = 0
    var inline: Bool = false
    var contentID: String = ""
    var url: String = ""

    enum CodingKeys: String, CodingKey {
        case id, filename, size, inline, url
        case contentType = "content_type"
        case contentID = "content_id"
    }

    var isImage: Bool { contentType.hasPrefix("image/") }

    var symbol: String {
        let type = contentType.lowercased()
        if type.hasPrefix("image/") { return "photo" }
        if type.contains("pdf") { return "doc.richtext" }
        if type.contains("spreadsheet") || type.contains("excel") || type.contains("csv") { return "tablecells" }
        if type.contains("presentation") || type.contains("powerpoint") || type.contains("keynote") { return "rectangle.on.rectangle" }
        if type.contains("zip") || type.contains("compressed") { return "doc.zipper" }
        if type.contains("calendar") { return "calendar" }
        return "doc"
    }
}

extension MailAttachment {
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = c.lenient(String.self, forKey: .id) ?? "0"
        filename = c.lenient(String.self, forKey: .filename) ?? "file"
        contentType = c.lenient(String.self, forKey: .contentType) ?? "application/octet-stream"
        size = c.lenient(Int.self, forKey: .size) ?? 0
        inline = c.lenient(Bool.self, forKey: .inline) ?? false
        contentID = c.lenient(String.self, forKey: .contentID) ?? ""
        url = c.lenient(String.self, forKey: .url) ?? ""
    }
}

/// One message of a conversation.
struct MailMessage: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var threadID: String
    var accountID: String
    var messageID: String = ""
    var from: MailAddress
    var to: [MailAddress] = []
    var cc: [MailAddress] = []
    var bcc: [MailAddress] = []
    var replyTo: [MailAddress] = []
    var subject: String = ""
    var snippet: String = ""
    var bodyHTML: String = ""
    var bodyText: String = ""
    var sentAt: Date
    var unread: Bool = false
    var starred: Bool = false
    var draft: Bool = false
    var folderIDs: [String] = []
    var attachments: [MailAttachment] = []
    var sentBy: UserRef?
    var draftID: String?
    var inReplyToID: String?
    var ownAddress: String?

    enum CodingKeys: String, CodingKey {
        case id, from, to, cc, bcc, subject, snippet, unread, starred, draft, attachments
        case threadID = "thread_id"
        case accountID = "account_id"
        case messageID = "message_id"
        case replyTo = "reply_to"
        case bodyHTML = "body_html"
        case bodyText = "body_text"
        case sentAt = "sent_at"
        case folderIDs = "folder_ids"
        case sentBy = "sent_by"
        case draftID = "draft_id"
        case inReplyToID = "in_reply_to_id"
        case ownAddress = "own_address"
    }

    /// Files that are not images drawn in the body.
    var files: [MailAttachment] { attachments.filter { !$0.inline } }
}

extension MailMessage {
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        threadID = c.lenient(String.self, forKey: .threadID) ?? ""
        accountID = c.lenient(String.self, forKey: .accountID) ?? ""
        messageID = c.lenient(String.self, forKey: .messageID) ?? ""
        from = c.lenient(MailAddress.self, forKey: .from) ?? MailAddress(email: "")
        to = c.lenient([MailAddress].self, forKey: .to) ?? []
        cc = c.lenient([MailAddress].self, forKey: .cc) ?? []
        bcc = c.lenient([MailAddress].self, forKey: .bcc) ?? []
        replyTo = c.lenient([MailAddress].self, forKey: .replyTo) ?? []
        subject = c.lenient(String.self, forKey: .subject) ?? ""
        snippet = c.lenient(String.self, forKey: .snippet) ?? ""
        bodyHTML = c.lenient(String.self, forKey: .bodyHTML) ?? ""
        bodyText = c.lenient(String.self, forKey: .bodyText) ?? ""
        sentAt = c.lenient(Date.self, forKey: .sentAt) ?? .distantPast
        unread = c.lenient(Bool.self, forKey: .unread) ?? false
        starred = c.lenient(Bool.self, forKey: .starred) ?? false
        draft = c.lenient(Bool.self, forKey: .draft) ?? false
        folderIDs = c.lenient([String].self, forKey: .folderIDs) ?? []
        attachments = c.lenient([MailAttachment].self, forKey: .attachments) ?? []
        sentBy = c.lenient(UserRef.self, forKey: .sentBy)
        draftID = c.lenient(String.self, forKey: .draftID)
        inReplyToID = c.lenient(String.self, forKey: .inReplyToID)
        ownAddress = c.lenient(String.self, forKey: .ownAddress)
    }
}

/// A teammate's comment on a conversation, never seen by the sender.
struct MailComment: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var threadID: String
    var author: UserRef?
    var bodyHTML: String
    var mentions: [String] = []
    var createdAt: Date
    var updatedAt: Date

    enum CodingKeys: String, CodingKey {
        case id, author, mentions
        case threadID = "thread_id"
        case bodyHTML = "body_html"
        case createdAt = "created_at"
        case updatedAt = "updated_at"
    }

    var edited: Bool { updatedAt.timeIntervalSince(createdAt) > 1 }
}

extension MailComment {
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        threadID = c.lenient(String.self, forKey: .threadID) ?? ""
        author = c.lenient(UserRef.self, forKey: .author)
        bodyHTML = c.lenient(String.self, forKey: .bodyHTML) ?? ""
        mentions = c.lenient([String].self, forKey: .mentions) ?? []
        createdAt = c.lenient(Date.self, forKey: .createdAt) ?? .distantPast
        updatedAt = c.lenient(Date.self, forKey: .updatedAt) ?? createdAt
    }
}

/// A conversation with its messages, comments and shares (`GET /v1/collab/threads/{id}`).
struct MailThreadDetail: Codable, Hashable, Sendable {
    var thread: MailThread
    var account: MailThreadAccount
    var messages: [MailMessage] = []
    var comments: [MailComment] = []
    var shares: MailAudience = .nobody
    var canAct: Bool = false

    enum CodingKeys: String, CodingKey {
        case thread, account, messages, comments, shares
        case canAct = "can_act"
    }
}

extension MailThreadDetail {
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        thread = try c.decode(MailThread.self, forKey: .thread)
        account = try c.decode(MailThreadAccount.self, forKey: .account)
        messages = c.lenient([MailMessage].self, forKey: .messages) ?? []
        comments = c.lenient([MailComment].self, forKey: .comments) ?? []
        shares = c.lenient(MailAudience.self, forKey: .shares) ?? .nobody
        canAct = c.lenient(Bool.self, forKey: .canAct) ?? false
    }
}

/// `GET /v1/collab`: whether Collab is on, and the workspace's storage.
struct CollabStatus: Codable, Hashable, Sendable {
    struct Storage: Codable, Hashable, Sendable {
        var usedBytes: Int = 0
        var limitBytes: Int = 0
        var historyLimited: Bool = false
        var full: Bool = false

        enum CodingKeys: String, CodingKey {
            case full
            case usedBytes = "used_bytes"
            case limitBytes = "limit_bytes"
            case historyLimited = "history_limited"
        }
    }

    var enabled: Bool
    var reason: String?
    var accounts: Int = 0
    var storage: Storage?

    var availability: CollabAvailability { CollabAvailability(enabled: enabled, reason: reason, accounts: accounts) }
}

extension CollabStatus {
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        enabled = c.lenient(Bool.self, forKey: .enabled) ?? false
        reason = c.lenient(String.self, forKey: .reason)
        accounts = c.lenient(Int.self, forKey: .accounts) ?? 0
        storage = c.lenient(Storage.self, forKey: .storage)
    }
}

/// `GET /v1/collab/counts`: unread conversations per box, account and folder.
struct MailCounts: Codable, Hashable, Sendable {
    var boxes: [String: Int] = [:]
    var accounts: [String: Int] = [:]
    var folders: [String: Int] = [:]
}

extension MailCounts {
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        boxes = c.lenient([String: Int].self, forKey: .boxes) ?? [:]
        accounts = c.lenient([String: Int].self, forKey: .accounts) ?? [:]
        folders = c.lenient([String: Int].self, forKey: .folders) ?? [:]
    }
}

/// The member's mail preferences the app reads (`GET /v1/collab/sidebar`).
struct MailSidebarPrefs: Codable, Hashable, Sendable {
    var markReadOnOpen: Bool = true
    var mailTabs: [String] = MailSidebarPrefs.defaultTabs

    static let defaultTabs = ["inbox", "starred", "sent", "drafts", "archive"]

    enum CodingKeys: String, CodingKey {
        case markReadOnOpen = "mark_read_on_open"
        case mailTabs = "mail_tabs"
    }
}

extension MailSidebarPrefs {
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        markReadOnOpen = c.lenient(Bool.self, forKey: .markReadOnOpen) ?? true
        mailTabs = c.lenient([String].self, forKey: .mailTabs) ?? Self.defaultTabs
    }
}

/// One address on a conversation, with the caller's other mail with them (`GET /v1/collab/person`).
struct MailPerson: Codable, Hashable, Sendable {
    struct File: Codable, Hashable, Sendable, Identifiable {
        var threadID: String
        var messageID: String
        var sentAt: Date
        var attachment: MailAttachment

        enum CodingKeys: String, CodingKey {
            case attachment
            case threadID = "thread_id"
            case messageID = "message_id"
            case sentAt = "sent_at"
        }

        var id: String { messageID + "/" + attachment.id }
    }

    var email: String
    var name: String = ""
    var conversations: Int = 0
    var others: Int = 0
    var firstAt: Date?
    var lastAt: Date?
    var threads: [MailThread] = []
    var files: [File] = []

    enum CodingKeys: String, CodingKey {
        case email, name, conversations, others, threads, files
        case firstAt = "first_at"
        case lastAt = "last_at"
    }
}

extension MailPerson {
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        email = c.lenient(String.self, forKey: .email) ?? ""
        name = c.lenient(String.self, forKey: .name) ?? ""
        conversations = c.lenient(Int.self, forKey: .conversations) ?? 0
        others = c.lenient(Int.self, forKey: .others) ?? 0
        firstAt = c.lenient(Date.self, forKey: .firstAt)
        lastAt = c.lenient(Date.self, forKey: .lastAt)
        threads = c.lenient([MailThread].self, forKey: .threads) ?? []
        files = c.lenient([File].self, forKey: .files) ?? []
    }
}

/// How a message came from its source (`GET /v1/collab/messages/{id}/details`).
struct MailMessageDetails: Codable, Hashable, Sendable {
    struct Header: Codable, Hashable, Sendable {
        var name: String
        var value: String
    }

    var mailedBy: String = ""
    var signedBy: [String] = []
    var via: String = ""
    var spf: String = ""
    var dkim: String = ""
    var dmarc: String = ""
    var encryption: String = ""
    var warning: String = ""
    var headers: [Header] = []

    enum CodingKeys: String, CodingKey {
        case via, spf, dkim, dmarc, encryption, warning, headers
        case mailedBy = "mailed_by"
        case signedBy = "signed_by"
    }
}

extension MailMessageDetails {
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        mailedBy = c.lenient(String.self, forKey: .mailedBy) ?? ""
        signedBy = c.lenient([String].self, forKey: .signedBy) ?? []
        via = c.lenient(String.self, forKey: .via) ?? ""
        spf = c.lenient(String.self, forKey: .spf) ?? ""
        dkim = c.lenient(String.self, forKey: .dkim) ?? ""
        dmarc = c.lenient(String.self, forKey: .dmarc) ?? ""
        encryption = c.lenient(String.self, forKey: .encryption) ?? ""
        warning = c.lenient(String.self, forKey: .warning) ?? ""
        headers = c.lenient([Header].self, forKey: .headers) ?? []
    }
}

// MARK: - Boxes and actions

/// The boxes every scope has (`GET /v1/collab/threads?box=`).
enum MailBox: String, CaseIterable, Identifiable, Sendable {
    case inbox, unread, starred, sent, drafts, archive, trash, spam, all, shared, assigned

    var id: String { rawValue }

    var title: String {
        switch self {
        case .inbox: "Inbox"
        case .unread: "Unread"
        case .starred: "Starred"
        case .sent: "Sent"
        case .drafts: "Drafts"
        case .archive: "Archive"
        case .trash: "Trash"
        case .spam: "Spam"
        case .all: "All mail"
        case .shared: "Shared with me"
        case .assigned: "Assigned to me"
        }
    }

    var symbol: String {
        switch self {
        case .inbox: "tray"
        case .unread: "envelope.badge"
        case .starred: "star"
        case .sent: "paperplane"
        case .drafts: "doc.text"
        case .archive: "archivebox"
        case .trash: "trash"
        case .spam: "xmark.shield"
        case .all: "square.stack"
        case .shared: "person.2"
        case .assigned: "person.crop.circle.badge.checkmark"
        }
    }

    /// The folder role a box is, when it is one.
    var role: MailFolderRole? {
        switch self {
        case .inbox: .inbox
        case .sent: .sent
        case .drafts: .drafts
        case .archive: .archive
        case .trash: .trash
        case .spam: .spam
        default: nil
        }
    }

    /// Boxes that span every account the person sees, whatever account is picked.
    var spansAccounts: Bool { self == .shared || self == .assigned }
}

/// `POST /v1/collab/threads/actions` actions.
enum MailAction: String, Sendable {
    case read, unread, star, unstar, archive, inbox, trash, spam
    case notSpam = "not_spam"
    case move, label, unlabel

    /// Takes the conversation out of most boxes: the list offers Undo.
    var removes: Bool { [.archive, .trash, .spam, .move, .inbox, .notSpam].contains(self) }

    var undo: MailAction? {
        switch self {
        case .archive, .trash, .spam: .inbox
        case .read: .unread
        case .unread: .read
        case .star: .unstar
        case .unstar: .star
        default: nil
        }
    }

    func done(_ count: Int, folder: String? = nil) -> String? {
        let one = count == 1
        switch self {
        case .archive: return one ? "Conversation archived" : "\(count) conversations archived"
        case .trash: return one ? "Conversation moved to trash" : "\(count) conversations moved to trash"
        case .spam: return one ? "Conversation reported as spam" : "\(count) conversations reported as spam"
        case .inbox, .notSpam: return one ? "Conversation moved to inbox" : "\(count) conversations moved to inbox"
        case .move: return one ? "Conversation moved to \(folder ?? "folder")" : "\(count) conversations moved to \(folder ?? "folder")"
        default: return nil
        }
    }
}

/// The opendesk tones an account or label takes, as `Tone`s.
enum MailTone {
    static func of(_ key: String?) -> Tone {
        Tone(label: key)
    }
}
