import Foundation
import Observation

/// The ticket list for one folder, view or team inbox, with filters, sort and
/// search, patched in place by realtime events.
@MainActor
@Observable
final class InboxStore {
    private(set) var scope: InboxScope = .folder(.mine)
    var filters = InboxFilters() {
        didSet { if filters != oldValue { reload() } }
    }
    var sort: TicketSort = .latest {
        didSet { if sort != oldValue { reload() } }
    }
    private(set) var search = ""
    private(set) var tickets: [Ticket] = []
    private(set) var nextCursor: String?
    private(set) var loading = false
    private(set) var loadingMore = false
    private(set) var error: String?
    private(set) var loadedOnce = false
    /// The teams the signed-in member is on, for "My teams" filters and team inboxes kept live.
    var myTeamIDs: Set<String> = []
    /// The workspace's addresses, for the address filter (loaded when filters open).
    private(set) var mailboxes: [InboxMailbox] = []

    @ObservationIgnored private var api: APIClient?
    @ObservationIgnored private var me: String?
    @ObservationIgnored private var generation = 0
    @ObservationIgnored private var reloadTask: Task<Void, Never>?
    @ObservationIgnored private var searchTask: Task<Void, Never>?
    @ObservationIgnored private var instance: Instance?

    func start(api: APIClient, me: String) {
        self.api = api
        self.me = me
        instance = api.instance
        tickets = []
        nextCursor = nil
        loadedOnce = false
        error = nil
        mailboxes = []
        if let raw = Preferences.lastFolder(for: api.instance), let saved = InboxScope(key: raw) {
            scope = saved
        } else {
            scope = .folder(.mine)
        }
        if let debug = AppInfo.debugValue("OPENDESK_DEBUG_FOLDER"), let folder = Folder(rawValue: debug) {
            scope = .folder(folder)
        }
        reload()
    }

    func loadMailboxes() async {
        guard let api, let page: Page<InboxMailbox> = try? await api.get("v1/mailboxes") else { return }
        mailboxes = page.data
    }

    func stop() {
        generation += 1
        api = nil
        tickets = []
        nextCursor = nil
        loadedOnce = false
    }

    func setScope(_ next: InboxScope) {
        guard next != scope else { return }
        // Moving between a team inbox's tabs keeps the filters, as on the web.
        let sameTeam = next.teamID != nil && next.teamID == scope.teamID
        scope = next
        if let instance { Preferences.setLastFolder(next.key, for: instance) }
        tickets = []
        nextCursor = nil
        loadedOnce = false
        if !sameTeam {
            filters = InboxFilters()
            sort = .latest
        }
        reload()
    }

    /// A team inbox's tab.
    func setTeamTab(_ tab: TeamTab) {
        guard case let .team(id, current) = scope, current != tab else { return }
        setScope(.team(id, tab))
    }

    func setSearch(_ text: String) {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard trimmed != search else { return }
        search = trimmed
        searchTask?.cancel()
        searchTask = Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(280))
            guard !Task.isCancelled else { return }
            self?.reload()
        }
    }

    private var query: [URLQueryItem] {
        var items: [URLQueryItem] = []
        var filters = filters
        switch scope {
        case let .folder(folder): items += folder.query
        case let .view(id): items.append(URLQueryItem(name: "view_id", value: id))
        case let .team(id, tab):
            items += tab.query(team: id)
            // A team inbox is already one team (or the caller's teams).
            filters.teams = []
        }
        items += filters.query
        items += sort.query
        if !search.isEmpty { items.append(URLQueryItem(name: "q", value: search)) }
        items.append(URLQueryItem(name: "limit", value: "50"))
        return items
    }

    func reload() {
        reloadTask?.cancel()
        reloadTask = Task { await self.refresh() }
    }

    func refresh() async {
        guard let api else { return }
        generation += 1
        let current = generation
        loading = true
        defer { if current == generation { loading = false } }
        do {
            let page: Page<Ticket> = try await api.get("v1/tickets", query: query)
            guard current == generation else { return }
            tickets = page.data
            nextCursor = page.nextCursor
            error = nil
            loadedOnce = true
        } catch {
            guard current == generation, !error.isCancellation else { return }
            self.error = error.userMessage
            loadedOnce = true
        }
    }

    func loadMore() async {
        guard let api, let cursor = nextCursor, !loadingMore, !loading else { return }
        loadingMore = true
        defer { loadingMore = false }
        let current = generation
        var items = query
        items.append(URLQueryItem(name: "cursor", value: cursor))
        guard let page: Page<Ticket> = try? await api.get("v1/tickets", query: items), current == generation else { return }
        let known = Set(tickets.map(\.id))
        tickets += page.data.filter { !known.contains($0.id) }
        nextCursor = page.nextCursor
    }

    // MARK: Changes

    /// Applies a change at once and sends it; puts the ticket back if the server refuses.
    func patch(_ ticket: Ticket, _ change: TicketPatch, members: [Member], meRef: UserRef?) async throws -> Ticket {
        guard let api else { throw APIError.signedOut }
        let before = tickets
        let optimistic = change.applied(to: ticket, me: meRef, members: members)
        place(optimistic)
        do {
            let saved: Ticket = try await api.patch("v1/tickets/\(ticket.id)", body: change)
            place(saved)
            return saved
        } catch {
            tickets = before
            throw error
        }
    }

    /// `POST /v1/tickets/bulk`: the change shows on every row at once, and the list
    /// goes back as it was if the server refuses. Realtime brings the saved copies.
    func bulk(_ ids: [String], _ change: TicketPatch, tags: [TagRef] = [], members: [Member], meRef: UserRef?) async throws {
        guard let api else { throw APIError.signedOut }
        let before = tickets
        for id in ids {
            guard let ticket = tickets.first(where: { $0.id == id }) else { continue }
            var next = change.applied(to: ticket, me: meRef, members: members)
            for tag in tags where !next.tags.contains(where: { $0.name.caseInsensitiveCompare(tag.name) == .orderedSame }) {
                next.tags.append(tag)
            }
            place(next)
        }
        do {
            let _: EmptyBody = try await api.post("v1/tickets/bulk", body: InboxBulkBody(ids: ids, patch: change, delete: nil))
        } catch {
            tickets = before
            throw error
        }
    }

    /// Deletes tickets for everyone; they leave the list at once and come back if the server refuses.
    func bulkDelete(_ ids: [String]) async throws {
        guard let api else { throw APIError.signedOut }
        let before = tickets
        let gone = Set(ids)
        tickets.removeAll { gone.contains($0.id) }
        do {
            let _: EmptyBody = try await api.post("v1/tickets/bulk", body: InboxBulkBody(ids: ids, patch: nil, delete: true))
        } catch {
            tickets = before
            throw error
        }
    }

    /// Puts a fresh copy where it belongs: updated in place, inserted, or dropped when it left this list.
    func place(_ ticket: Ticket) {
        let belongs = belongsHere(ticket)
        if let index = tickets.firstIndex(where: { $0.id == ticket.id }) {
            if belongs == false {
                tickets.remove(at: index)
            } else {
                tickets[index] = ticket
                resort()
            }
        } else if belongs == true, search.isEmpty {
            tickets.append(ticket)
            resort()
        }
    }

    func remove(_ id: String) {
        tickets.removeAll { $0.id == id }
    }

    /// true / false when the scope can be checked here, nil for views (kept in place, the server decides on reload).
    private func belongsHere(_ ticket: Ticket) -> Bool? {
        switch scope {
        case let .folder(folder):
            return folder.contains(ticket, me: me) && filters.matches(ticket, myTeams: myTeamIDs)
        case let .team(id, tab):
            let inTeam = id == InboxScope.myTeams ? ticket.teamID.map(myTeamIDs.contains) ?? false : ticket.teamID == id
            var filters = filters
            filters.teams = []
            return inTeam && tab.contains(ticket) && filters.matches(ticket, myTeams: myTeamIDs)
        case .view:
            return nil
        }
    }

    private func resort() {
        let descending = sort.descending
        func ordered<T: Comparable>(_ a: T, _ b: T) -> Bool { descending ? a > b : a < b }
        switch sort.key {
        case .lastMessage:
            tickets.sort { ordered($0.lastMessageAt, $1.lastMessageAt) }
        case .created:
            tickets.sort { ordered($0.createdAt, $1.createdAt) }
        case .priority:
            tickets.sort { $0.priority.rank != $1.priority.rank ? ordered($0.priority.rank, $1.priority.rank) : $0.lastMessageAt > $1.lastMessageAt }
        case .severity:
            // sev1 first, none last (descending is "highest first").
            func rank(_ ticket: Ticket) -> Int { ticket.severity.map { 5 - (Severity.allCases.firstIndex(of: $0) ?? 4) } ?? 0 }
            tickets.sort { rank($0) != rank($1) ? ordered(rank($0), rank($1)) : $0.lastMessageAt > $1.lastMessageAt }
        case .due:
            func due(_ ticket: Ticket) -> Date { SLAState.of(ticket)?.due ?? .distantFuture }
            tickets.sort { ordered(due($0), due($1)) }
        case .field:
            // The server orders custom fields; rows that changed stay where they are until the next load.
            break
        }
    }

    // MARK: Realtime

    func handle(_ update: RealtimeUpdate) {
        switch update {
        case .resync:
            reload()
        case let .envelope(envelope):
            switch envelope.type {
            case "ticket.created", "ticket.updated":
                guard let ticket = envelope.member("ticket", as: Ticket.self) else { return }
                if case .view = scope, !tickets.contains(where: { $0.id == ticket.id }) {
                    scheduleQuietReload()
                } else {
                    place(ticket)
                }
            case "ticket.deleted", "ticket.hidden":
                if let id = envelope.string("ticket_id") { remove(id) }
            case "tag.merged", "tag.updated", "tag.deleted":
                scheduleQuietReload()
            default:
                break
            }
        }
    }

    @ObservationIgnored private var quietReload: Task<Void, Never>?

    private func scheduleQuietReload() {
        guard quietReload == nil else { return }
        quietReload = Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(600))
            guard let self else { return }
            self.quietReload = nil
            await self.refresh()
        }
    }
}

/// Body of `POST /v1/tickets/bulk`: `{ids, patch}` or `{ids, delete: true}`.
struct InboxBulkBody: Encodable, Sendable {
    var ids: [String]
    var patch: TicketPatch?
    var delete: Bool?

    enum CodingKeys: String, CodingKey { case ids, patch, delete }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(ids, forKey: .ids)
        try c.encodeIfPresent(patch, forKey: .patch)
        try c.encodeIfPresent(delete, forKey: .delete)
    }
}
