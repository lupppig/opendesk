import Foundation

// The account's own shapes (contracts/api.md "Auth and account", "Notification
// preferences and emails", contracts/templates.md `MemberProfile`).

// MARK: - Workspace profile

/// The profile customers see in this workspace (title, phone, pronouns, personal signature).
struct MemberProfile: Codable, Hashable, Sendable {
    var title: String
    var phone: String
    var pronouns: String
    var signatureHTML: String

    enum CodingKeys: String, CodingKey {
        case title, phone, pronouns
        case signatureHTML = "signature_html"
    }

    static let empty = MemberProfile(title: "", phone: "", pronouns: "", signatureHTML: "")

    init(title: String, phone: String, pronouns: String, signatureHTML: String) {
        self.title = title
        self.phone = phone
        self.pronouns = pronouns
        self.signatureHTML = signatureHTML
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        title = c.lenient(String.self, forKey: .title) ?? ""
        phone = c.lenient(String.self, forKey: .phone) ?? ""
        pronouns = c.lenient(String.self, forKey: .pronouns) ?? ""
        signatureHTML = c.lenient(String.self, forKey: .signatureHTML) ?? ""
    }
}

/// A `Member` with its workspace profile (`GET /v1/members`, `PATCH /v1/members/me/profile`).
struct AccountMemberAnswer: Decodable, Sendable {
    var member: Member
    var profile: MemberProfile?

    private enum CodingKeys: String, CodingKey { case profile }

    init(from decoder: Decoder) throws {
        member = try Member(from: decoder)
        profile = try decoder.container(keyedBy: CodingKeys.self).lenient(MemberProfile.self, forKey: .profile)
    }
}

// MARK: - Security

/// `{secret, otpauth_url}` from `POST /v1/auth/totp/setup`.
struct TOTPSetup: Decodable, Sendable, Equatable {
    var secret: String
    var otpauthURL: String

    enum CodingKeys: String, CodingKey {
        case secret
        case otpauthURL = "otpauth_url"
    }

    /// The secret in groups of four, easier to type.
    var groupedSecret: String {
        stride(from: 0, to: secret.count, by: 4).map { start in
            let from = secret.index(secret.startIndex, offsetBy: start)
            let to = secret.index(from, offsetBy: min(4, secret.count - start))
            return String(secret[from ..< to])
        }.joined(separator: " ")
    }
}

struct RecoveryCodes: Decodable, Sendable {
    var recoveryCodes: [String]

    enum CodingKeys: String, CodingKey {
        case recoveryCodes = "recovery_codes"
    }
}

struct Passkey: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var name: String
    var createdAt: Date?
    var lastUsedAt: Date?
    var backedUp: Bool

    enum CodingKeys: String, CodingKey {
        case id, name
        case createdAt = "created_at"
        case lastUsedAt = "last_used_at"
        case backedUp = "backed_up"
    }

    init(id: String, name: String, createdAt: Date?, lastUsedAt: Date?, backedUp: Bool) {
        self.id = id
        self.name = name
        self.createdAt = createdAt
        self.lastUsedAt = lastUsedAt
        self.backedUp = backedUp
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = c.lenient(String.self, forKey: .name) ?? "Passkey"
        createdAt = c.lenient(Date.self, forKey: .createdAt)
        lastUsedAt = c.lenient(Date.self, forKey: .lastUsedAt)
        backedUp = c.lenient(Bool.self, forKey: .backedUp) ?? false
    }
}

extension SessionInfo {
    /// What kind of device the session is on, from its client and user agent.
    var deviceKind: AccountDeviceKind {
        switch client {
        case "ios": return .iPhone
        case "desktop": return .desktop
        default:
            let agent = userAgent ?? ""
            if agent.contains("iPhone") || agent.contains("Android") && agent.contains("Mobile") { return .phone }
            if agent.contains("iPad") { return .tablet }
            return .browser
        }
    }

    /// "Chrome on Mac", "opendesk for iPhone", "opendesk desktop".
    var deviceName: String {
        switch deviceKind {
        case .iPhone: return "opendesk for iPhone"
        case .desktop: return "opendesk desktop app"
        default:
            let agent = userAgent ?? ""
            let browser = agent.contains("Edg/") ? "Edge" : agent.contains("Firefox/") ? "Firefox" : agent.contains("Chrome/") ? "Chrome" : agent.contains("Safari/") ? "Safari" : "A browser"
            let os = agent.contains("iPhone") ? "iPhone" : agent.contains("iPad") ? "iPad" : agent.contains("Android") ? "Android" : agent.contains("Mac OS") ? "Mac" : agent.contains("Windows") ? "Windows" : agent.contains("Linux") ? "Linux" : nil
            return os.map { "\(browser) on \($0)" } ?? browser
        }
    }
}

enum AccountDeviceKind {
    case iPhone, phone, tablet, desktop, browser

    var symbol: String {
        switch self {
        case .iPhone, .phone: "iphone"
        case .tablet: "ipad"
        case .desktop: "macbook"
        case .browser: "globe"
        }
    }
}

// MARK: - Notification preferences

enum EmailCadence: String, LenientEnum, CaseIterable, Identifiable {
    case instant, hourly, daily, off
    static let fallback = EmailCadence.instant

    var id: String { rawValue }

    var title: String {
        switch self {
        case .instant: "Right away"
        case .hourly: "Hourly digest"
        case .daily: "Daily digest"
        case .off: "Off"
        }
    }

    var symbol: String {
        switch self {
        case .instant: "bolt"
        case .hourly: "clock"
        case .daily: "calendar.badge.clock"
        case .off: "bell.slash"
        }
    }

    var tone: Tone {
        switch self {
        case .instant: .blue
        case .hourly: .teal
        case .daily: .indigo
        case .off: .gray
        }
    }
}

/// The three channels besides the app (contracts/api.md).
enum NotificationChannel: String, CaseIterable, Identifiable, Sendable {
    case email, push, desktop

    var id: String { rawValue }

    var title: String {
        switch self {
        case .email: "Email"
        case .push: "Phone"
        case .desktop: "Desktop"
        }
    }

    var symbol: String {
        switch self {
        case .email: "envelope"
        case .push: "iphone"
        case .desktop: "macbook"
        }
    }
}

struct NotificationChannels: Codable, Hashable, Sendable {
    var email: Bool
    var push: Bool
    var desktop: Bool
}

struct NotificationTypePrefs: Codable, Hashable, Sendable, Identifiable {
    var type: String
    var group: String
    var email: Bool
    var push: Bool
    var desktop: Bool
    var urgent: Bool

    var id: String { type }

    enum CodingKeys: String, CodingKey {
        case type, group, email, push, desktop, urgent
    }

    init(type: String, group: String, email: Bool, push: Bool, desktop: Bool, urgent: Bool) {
        self.type = type
        self.group = group
        self.email = email
        self.push = push
        self.desktop = desktop
        self.urgent = urgent
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        type = try c.decode(String.self, forKey: .type)
        group = c.lenient(String.self, forKey: .group) ?? "tickets"
        email = c.lenient(Bool.self, forKey: .email) ?? true
        push = c.lenient(Bool.self, forKey: .push) ?? true
        desktop = c.lenient(Bool.self, forKey: .desktop) ?? true
        urgent = c.lenient(Bool.self, forKey: .urgent) ?? false
    }

    func value(_ channel: NotificationChannel) -> Bool {
        switch channel {
        case .email: email
        case .push: push
        case .desktop: desktop
        }
    }

    mutating func set(_ channel: NotificationChannel, _ on: Bool) {
        switch channel {
        case .email: email = on
        case .push: push = on
        case .desktop: desktop = on
        }
    }

    var kind: NotificationKind? { NotificationKind(rawValue: type) }

    /// The web's names and hints for the settings page (settings.json `kinds`).
    var label: String { Self.copy[type]?.0 ?? kind?.settingTitle ?? type.replacingOccurrences(of: "_", with: " ").capitalized }
    var hint: String { Self.copy[type]?.1 ?? kind?.settingHint ?? "" }

    private static let copy: [String: (String, String)] = [
        "assigned": ("Assigned to you", "A ticket is assigned to you"),
        "new_ticket": ("New tickets", "New tickets nobody is assigned to"),
        "mentioned": ("Mentions", "Someone mentions you in a note"),
        "customer_replied": ("Customer replies", "On tickets assigned to you, and on tickets nobody has yet"),
        "thread_replied": ("Side conversation answers", "Someone outside your team answers a side conversation on a ticket assigned to you"),
        "sla_breached": ("SLA breaches", "A ticket assigned to you misses a target"),
        "ai_handoff": ("Handoffs to a person", "Ollie hands a conversation to the team"),
        "collab_assigned": ("Mail assigned to you", "A teammate assigns you a conversation from a mail account"),
        "collab_mention": ("Mail comment mentions", "Someone mentions you in a comment on a mail conversation"),
        "team_joined": ("Team changes", "An admin joins a team you lead"),
        "ai_cap_reached": ("AI agent cap reached", "Ollie reached this month's spending cap"),
        "ai_key_failed": ("AI key problems", "The workspace's own AI key stopped working"),
        "ai_balance_low": ("AI balance low", "The AI balance is running low"),
        "ai_balance_out": ("AI balance used up", "Ollie pauses until AI usage is added"),
        "auto_recharge_failed": ("Auto-recharge failed", "An auto-recharge payment failed"),
        "import_finished": ("Imports", "An import you started is done, stopped or needs new credentials"),
    ]
}

struct NotificationPacing: Codable, Hashable, Sendable {
    var perHour: Int
    var perDay: Int

    enum CodingKeys: String, CodingKey {
        case perHour = "per_hour"
        case perDay = "per_day"
    }
}

struct NotificationPreferences: Codable, Hashable, Sendable {
    var emailCadence: EmailCadence
    var digestHour: Int
    var timezone: String
    var pausedUntil: Date?
    var email: String
    var emailVerified: Bool
    var delayMinutes: Int
    var pacing: NotificationPacing?
    var unsubscribedAt: Date?
    var types: [NotificationTypePrefs]

    enum CodingKeys: String, CodingKey {
        case timezone, email, pacing, types
        case emailCadence = "email_cadence"
        case digestHour = "digest_hour"
        case pausedUntil = "paused_until"
        case emailVerified = "email_verified"
        case delayMinutes = "delay_minutes"
        case unsubscribedAt = "unsubscribed_at"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        emailCadence = c.lenient(EmailCadence.self, forKey: .emailCadence) ?? .instant
        digestHour = c.lenient(Int.self, forKey: .digestHour) ?? 9
        timezone = c.lenient(String.self, forKey: .timezone) ?? "UTC"
        pausedUntil = c.lenient(Date.self, forKey: .pausedUntil)
        email = c.lenient(String.self, forKey: .email) ?? ""
        emailVerified = c.lenient(Bool.self, forKey: .emailVerified) ?? false
        delayMinutes = c.lenient(Int.self, forKey: .delayMinutes) ?? 5
        pacing = c.lenient(NotificationPacing.self, forKey: .pacing)
        unsubscribedAt = c.lenient(Date.self, forKey: .unsubscribedAt)
        types = c.lenient([NotificationTypePrefs].self, forKey: .types) ?? []
    }

    func isPaused(at now: Date) -> Bool { (pausedUntil ?? .distantPast) > now }

    /// The types in the web's group order, each group once.
    var groups: [(group: String, types: [NotificationTypePrefs])] {
        let order = ["tickets", "mail", "team", "ai_billing", "workspace"]
        let known = Set(order)
        let extra = types.map(\.group).filter { !known.contains($0) }
        var seen = Set<String>()
        return (order + extra).compactMap { group in
            guard seen.insert(group).inserted else { return nil }
            let rows = types.filter { $0.group == group }
            return rows.isEmpty ? nil : (group, rows)
        }
    }

    static func groupTitle(_ group: String) -> String {
        switch group {
        case "tickets": "Tickets"
        case "team": "Team"
        case "mail": "Mail"
        case "ai_billing": "AI and billing"
        case "workspace": "Workspace"
        default: group.replacingOccurrences(of: "_", with: " ").capitalized
        }
    }
}

/// `PATCH /v1/me/notification-preferences`: only what changed.
struct NotificationPreferencesPatch: Encodable, Sendable {
    struct TypeChange: Encodable, Sendable {
        var type: String
        var email: Bool?
        var push: Bool?
        var desktop: Bool?
    }

    var emailCadence: EmailCadence?
    var digestHour: Int?
    var timezone: String?
    /// A date pauses; `""` resumes.
    var pausedUntil: String?
    var types: [TypeChange]?

    enum CodingKeys: String, CodingKey {
        case timezone, types
        case emailCadence = "email_cadence"
        case digestHour = "digest_hour"
        case pausedUntil = "paused_until"
    }
}

// MARK: - Saved replies and views

/// A saved reply as the settings list reads it, with its team.
struct AccountSavedReply: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var title: String
    var shortcut: String
    var bodyHTML: String
    var teamID: String?
    var updatedAt: Date?

    enum CodingKeys: String, CodingKey {
        case id, title, shortcut
        case bodyHTML = "body_html"
        case teamID = "team_id"
        case updatedAt = "updated_at"
    }

    init(id: String, title: String, shortcut: String, bodyHTML: String, teamID: String?, updatedAt: Date?) {
        self.id = id
        self.title = title
        self.shortcut = shortcut
        self.bodyHTML = bodyHTML
        self.teamID = teamID
        self.updatedAt = updatedAt
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        title = c.lenient(String.self, forKey: .title) ?? ""
        shortcut = c.lenient(String.self, forKey: .shortcut) ?? ""
        bodyHTML = c.lenient(String.self, forKey: .bodyHTML) ?? ""
        teamID = c.lenient(String.self, forKey: .teamID)
        updatedAt = c.lenient(Date.self, forKey: .updatedAt)
    }

    var plainBody: String { HTMLText.plain(from: bodyHTML) }
}

/// A ticket view as the settings list reads it, with its team.
struct AccountTicketView: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var name: String
    var shared: Bool
    var filters: TicketFilters
    var position: Int
    var teamID: String?

    enum CodingKeys: String, CodingKey {
        case id, name, shared, filters, position
        case teamID = "team_id"
    }

    init(id: String, name: String, shared: Bool, filters: TicketFilters, position: Int, teamID: String?) {
        self.id = id
        self.name = name
        self.shared = shared
        self.filters = filters
        self.position = position
        self.teamID = teamID
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = c.lenient(String.self, forKey: .name) ?? ""
        shared = c.lenient(Bool.self, forKey: .shared) ?? false
        filters = c.lenient(TicketFilters.self, forKey: .filters) ?? TicketFilters()
        position = c.lenient(Int.self, forKey: .position) ?? 0
        teamID = c.lenient(String.self, forKey: .teamID)
    }

    enum Audience: String, CaseIterable, Identifiable {
        case personal, shared, team
        var id: String { rawValue }
        var title: String {
            switch self {
            case .personal: "Only me"
            case .shared: "Everyone"
            case .team: "A team"
            }
        }
    }

    var audience: Audience { teamID != nil ? .team : shared ? .shared : .personal }
}

extension TicketFilters {
    var isEmpty: Bool {
        (status ?? []).isEmpty && (priority ?? []).isEmpty && (severity ?? []).isEmpty && (assignee ?? "").isEmpty
            && (teamID ?? "").isEmpty && (mailboxID ?? "").isEmpty && (tag ?? "").isEmpty && (channel ?? "").isEmpty
            && (q ?? "").isEmpty && (fields ?? []).isEmpty && !hasOtherFilters
    }

    /// Filters set on the web that the iPhone keeps as they are without editing them.
    var hasOtherFilters: Bool {
        !(severity ?? []).isEmpty || !(mailboxID ?? "").isEmpty || !(channel ?? "").isEmpty || !(q ?? "").isEmpty
            || !(fields ?? []).isEmpty || !(receivedAt ?? "").isEmpty || !(brandID ?? "").isEmpty || !(widgetID ?? "").isEmpty
    }
}
