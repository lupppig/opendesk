import Foundation

/// The inbox folders, as on the web (`web/src/lib/folders.ts`).
enum Folder: String, CaseIterable, Identifiable, Sendable, Codable {
    case unassigned, mine, open, pending, solved, spam

    var id: String { rawValue }

    var title: String {
        switch self {
        case .unassigned: "Unassigned"
        case .mine: "My tickets"
        case .open: "All open"
        case .pending: "Pending"
        case .solved: "Solved"
        case .spam: "Spam"
        }
    }

    var symbol: String {
        switch self {
        case .unassigned: "tray"
        case .mine: "person.crop.circle"
        case .open: "circle"
        case .pending: "clock"
        case .solved: "checkmark.circle"
        case .spam: "xmark.octagon"
        }
    }

    var query: [URLQueryItem] {
        switch self {
        case .unassigned: [.init(name: "status", value: "open,pending,on_hold"), .init(name: "assignee", value: "none")]
        case .mine: [.init(name: "status", value: "open,pending,on_hold"), .init(name: "assignee", value: "me")]
        case .open: [.init(name: "status", value: "open")]
        case .pending: [.init(name: "status", value: "pending,on_hold")]
        case .solved: [.init(name: "status", value: "solved,closed")]
        case .spam: [.init(name: "spam", value: "true")]
        }
    }

    func count(in counts: TicketCounts?) -> Int? {
        guard let counts else { return nil }
        switch self {
        case .unassigned: return counts.unassigned
        case .mine: return counts.mine
        case .open: return counts.open
        case .pending: return counts.pending + counts.onHold
        case .solved: return nil
        case .spam: return counts.spam
        }
    }

    /// Whether a ticket belongs here, to keep a loaded list right as events arrive.
    func contains(_ ticket: Ticket, me: String?) -> Bool {
        if self == .spam { return ticket.spam }
        if ticket.spam { return false }
        switch self {
        case .unassigned: return [.open, .pending, .onHold].contains(ticket.status) && ticket.assignee == nil
        case .mine: return [.open, .pending, .onHold].contains(ticket.status) && ticket.assignee?.id == me && me != nil
        case .open: return ticket.status == .open
        case .pending: return ticket.status == .pending || ticket.status == .onHold
        case .solved: return ticket.status.isDone
        case .spam: return ticket.spam
        }
    }
}

/// A team inbox's tabs (`/teams/<id>?tab=`), as the web's `teamTabs`.
enum TeamTab: String, CaseIterable, Identifiable, Sendable {
    case unassigned, open, pending, all

    var id: String { rawValue }

    var title: String {
        switch self {
        case .unassigned: "Unassigned"
        case .open: "Open"
        case .pending: "Pending"
        case .all: "All"
        }
    }

    /// `teamQuery` on the web.
    func query(team: String) -> [URLQueryItem] {
        let team = URLQueryItem(name: "team_id", value: team)
        switch self {
        case .unassigned: return [team, .init(name: "status", value: "open,pending"), .init(name: "assignee", value: "none")]
        case .open: return [team, .init(name: "status", value: "open")]
        case .pending: return [team, .init(name: "status", value: "pending,on_hold")]
        case .all: return [team]
        }
    }

    func contains(_ ticket: Ticket) -> Bool {
        if ticket.spam { return false }
        switch self {
        case .unassigned: return (ticket.status == .open || ticket.status == .pending) && ticket.assignee == nil
        case .open: return ticket.status == .open
        case .pending: return ticket.status == .pending || ticket.status == .onHold
        case .all: return true
        }
    }
}

/// What the inbox list shows: a folder, a saved view, or a team inbox
/// (one team, or `"mine"` for every team the caller is on).
enum InboxScope: Hashable, Sendable {
    case folder(Folder)
    case view(String)
    case team(String, TeamTab)

    static let myTeams = "mine"

    var key: String {
        switch self {
        case let .folder(folder): "folder:" + folder.rawValue
        case let .view(id): "view:" + id
        case let .team(id, tab): "team:" + id + ":" + tab.rawValue
        }
    }

    init?(key: String) {
        if key.hasPrefix("folder:"), let folder = Folder(rawValue: String(key.dropFirst(7))) {
            self = .folder(folder)
        } else if key.hasPrefix("view:") {
            self = .view(String(key.dropFirst(5)))
        } else if key.hasPrefix("team:") {
            let parts = key.dropFirst(5).split(separator: ":").map(String.init)
            guard let id = parts.first, !id.isEmpty else { return nil }
            self = .team(id, parts.count > 1 ? TeamTab(rawValue: parts[1]) ?? .unassigned : .unassigned)
        } else {
            return nil
        }
    }

    /// The team of a team inbox (`"mine"` for My teams).
    var teamID: String? {
        if case let .team(id, _) = self { return id }
        return nil
    }

    var teamTab: TeamTab? {
        if case let .team(_, tab) = self { return tab }
        return nil
    }

    /// The web path of a ticket in this scope (`/inbox/<folder>/<id>`, `/views/<view>/<id>`, `/teams/<team>/<id>`).
    func webPath(ticketID: String? = nil) -> String {
        let base: String
        var search = ""
        switch self {
        case let .folder(folder): base = "/inbox/\(folder.rawValue)"
        case let .view(id): base = "/views/\(id)"
        case let .team(id, tab):
            base = "/teams/\(id)"
            if tab != .unassigned { search = "?tab=" + tab.rawValue }
        }
        guard let ticketID else { return base + search }
        return base + "/" + ticketID + search
    }
}

/// What the list sorts by: the API's `sort` (contracts/api.md), custom fields included.
enum TicketSortKey: Hashable, Sendable {
    case lastMessage, created, priority, severity, due
    case field(String)

    static let builtIn: [TicketSortKey] = [.lastMessage, .created, .priority, .severity, .due]

    var raw: String {
        switch self {
        case .lastMessage: "last_message_at"
        case .created: "created_at"
        case .priority: "priority"
        case .severity: "severity"
        case .due: "due"
        case let .field(key): "field." + key
        }
    }

    var title: String {
        switch self {
        case .lastMessage: "Last activity"
        case .created: "Created"
        case .priority: "Priority"
        case .severity: "Severity"
        case .due: "SLA due"
        case let .field(key): key
        }
    }

    /// The direction a fresh pick starts in: due soonest first, everything else highest or newest first.
    var defaultDescending: Bool { self != .due }

    /// "Newest first" / "Oldest first" and the like for this key.
    func directionTitle(descending: Bool) -> String {
        switch self {
        case .lastMessage, .created: descending ? "Newest first" : "Oldest first"
        case .due: descending ? "Latest first" : "Soonest first"
        case .priority, .severity: descending ? "Highest first" : "Lowest first"
        case .field: descending ? "Descending" : "Ascending"
        }
    }
}

/// The list's order: a key and a direction.
struct TicketSort: Hashable, Sendable {
    var key: TicketSortKey = .lastMessage
    var descending = true

    static let latest = TicketSort()
    static let due = TicketSort(key: .due, descending: false)

    var isDefault: Bool { self == .latest }

    var query: [URLQueryItem] {
        [.init(name: "sort", value: key.raw), .init(name: "order", value: descending ? "desc" : "asc")]
    }
}

/// Extra filters on top of a folder, view or team inbox (the web's `InboxSearch`).
struct InboxFilters: Hashable, Sendable {
    var priorities: Set<Priority> = []
    /// `sev1`..`sev4`, and `none` for tickets without one.
    var severities: Set<String> = []
    var channel: Channel?
    /// Team ids, `mine` for the caller's teams and `none` for no team.
    var teams: Set<String> = []
    var tag: String?
    var mailboxID: String?
    /// The address a ticket was written to (an alias).
    var receivedAt: String?
    /// Custom field filters, one per field.
    var fields: [FieldFilter] = []

    static let noSeverity = "none"
    static let noTeam = "none"

    var isEmpty: Bool { count == 0 }

    var count: Int {
        priorities.count + severities.count + (channel == nil ? 0 : 1) + teams.count + (tag == nil ? 0 : 1)
            + (mailboxID == nil ? 0 : 1) + (receivedAt == nil ? 0 : 1) + fields.count
    }

    var query: [URLQueryItem] {
        var items: [URLQueryItem] = []
        if !priorities.isEmpty {
            items.append(.init(name: "priority", value: Priority.allCases.filter(priorities.contains).map(\.rawValue).joined(separator: ",")))
        }
        if !severities.isEmpty {
            let order = Severity.allCases.map(\.rawValue) + [Self.noSeverity]
            items.append(.init(name: "severity", value: order.filter(severities.contains).joined(separator: ",")))
        }
        if let channel { items.append(.init(name: "channel", value: channel.rawValue)) }
        if !teams.isEmpty { items.append(.init(name: "team_id", value: teams.sorted().joined(separator: ","))) }
        if let tag { items.append(.init(name: "tag", value: tag)) }
        if let mailboxID { items.append(.init(name: "mailbox_id", value: mailboxID)) }
        if let receivedAt { items.append(.init(name: "received_at", value: receivedAt)) }
        for field in fields {
            items.append(.init(name: "field.\(field.key)[\(field.op)]", value: field.value))
        }
        return items
    }

    /// The values a field filter holds (option ids, `true`).
    func values(of key: String) -> [String] {
        fields.first { $0.key == key }?.value.split(separator: ",").map(String.init) ?? []
    }

    /// Toggles one value of a select, multi-select, user or checkbox field, as the web's `toggleField`.
    mutating func toggle(field: CustomField, value: String) {
        let current = values(of: field.key)
        fields.removeAll { $0.key == field.key }
        if field.type == .checkbox {
            if !current.contains(value) { fields.append(FieldFilter(key: field.key, op: "eq", value: value)) }
            return
        }
        let next = current.contains(value) ? current.filter { $0 != value } : current + [value]
        if !next.isEmpty { fields.append(FieldFilter(key: field.key, op: "any", value: next.joined(separator: ","))) }
    }

    /// Whether a ticket passes the filters that can be checked here (custom fields are left to the server).
    func matches(_ ticket: Ticket, myTeams: Set<String> = []) -> Bool {
        if !priorities.isEmpty, !priorities.contains(ticket.priority) { return false }
        if !severities.isEmpty, !severities.contains(ticket.severity?.rawValue ?? Self.noSeverity) { return false }
        if let channel, ticket.channel != channel { return false }
        if !teams.isEmpty {
            let hit = teams.contains { value in
                switch value {
                case Self.noTeam: ticket.teamID == nil
                case InboxScope.myTeams: ticket.teamID.map(myTeams.contains) ?? false
                default: ticket.teamID == value
                }
            }
            if !hit { return false }
        }
        if let tag, !ticket.tags.contains(where: { $0.name.caseInsensitiveCompare(tag) == .orderedSame }) { return false }
        if let mailboxID, ticket.mailboxID != mailboxID { return false }
        return true
    }
}
