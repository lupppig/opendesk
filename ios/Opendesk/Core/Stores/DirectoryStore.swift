import Foundation
import Observation

/// The workspace's people, teams, labels, views, saved replies, folder counts
/// and AI switches: everything a list or a picker needs, kept live.
@MainActor
@Observable
final class DirectoryStore {
    private(set) var members: [Member] = []
    private(set) var teams: [Team] = []
    private(set) var tags: [Tag] = []
    private(set) var views: [SavedView] = []
    private(set) var savedReplies: [SavedReply] = []
    /// Custom field definitions for tickets, contacts and companies, archived ones included.
    private(set) var fields: [CustomField] = []
    private(set) var counts: TicketCounts?
    private(set) var ai: AIConfig = .off
    /// Collab mail for this workspace; the Mail area shows only when it is on.
    private(set) var collab: CollabAvailability = .off
    private(set) var loaded = false

    @ObservationIgnored private var api: APIClient?
    @ObservationIgnored private var countsTask: Task<Void, Never>?

    func start(api: APIClient) {
        self.api = api
        reset()
        Task { await load() }
    }

    func reset() {
        members = []
        teams = []
        tags = []
        views = []
        savedReplies = []
        fields = []
        counts = nil
        ai = .off
        collab = .off
        loaded = false
    }

    func load() async {
        guard let api else { return }
        async let members: Page<Member>? = try? api.get("v1/members", query: [URLQueryItem(name: "limit", value: "100")])
        async let teams: Page<Team>? = try? api.get("v1/teams", query: [URLQueryItem(name: "limit", value: "100")])
        async let tags: Page<Tag>? = try? api.get("v1/tags", query: [
            URLQueryItem(name: "archived", value: "false"), URLQueryItem(name: "applies_to", value: "tickets"), URLQueryItem(name: "limit", value: "100"),
        ])
        async let views: Page<SavedView>? = try? api.get("v1/views", query: [URLQueryItem(name: "limit", value: "100")])
        async let replies: Page<SavedReply>? = try? api.get("v1/saved-replies", query: [URLQueryItem(name: "limit", value: "100")])
        async let fields: Page<CustomField>? = try? api.get("v1/custom-fields")
        async let ai: AIConfig? = try? api.get("v1/ai/config")
        async let collab: CollabAvailability? = try? api.get("v1/collab")
        async let counts: TicketCounts? = try? api.get("v1/tickets/counts")

        if let page = await members { self.members = page.data }
        if let page = await teams { self.teams = page.data.sorted { $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending } }
        if let page = await tags { self.tags = page.data.sorted { $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending } }
        if let page = await views { self.views = page.data.sorted { ($0.position ?? 0) < ($1.position ?? 0) } }
        if let page = await replies { self.savedReplies = page.data.sorted { $0.title.localizedCaseInsensitiveCompare($1.title) == .orderedAscending } }
        if let page = await fields { self.fields = page.data.sorted { $0.position < $1.position } }
        if let config = await ai { self.ai = config }
        if let status = await collab { self.collab = status }
        if let value = await counts { self.counts = value }
        loaded = true
    }

    func refreshCounts() async {
        guard let api else { return }
        if let value: TicketCounts = try? await api.get("v1/tickets/counts") { counts = value }
    }

    /// Coalesces bursts (bulk edits) into one counts refetch.
    func scheduleCounts() {
        guard countsTask == nil else { return }
        countsTask = Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(400))
            guard let self else { return }
            self.countsTask = nil
            await self.refreshCounts()
        }
    }

    // MARK: Lookups

    func member(_ id: String?) -> Member? {
        guard let id else { return nil }
        return members.first { $0.id == id }
    }

    func team(_ id: String?) -> Team? {
        guard let id else { return nil }
        return teams.first { $0.id == id }
    }

    func view(_ id: String) -> SavedView? { views.first { $0.id == id } }

    /// Lets the Mail area refresh what `GET /v1/collab` says (after connecting an account on the web).
    func setCollab(_ status: CollabAvailability) { collab = status }

    /// Live (unarchived) custom fields for `entity` (`ticket`, `contact`, `company`), in order.
    func fields(for entity: String) -> [CustomField] { fields.filter { $0.entity == entity && !$0.archived } }

    func tag(named name: String) -> Tag? {
        tags.first { $0.name.caseInsensitiveCompare(name) == .orderedSame }
    }

    /// Members who can own tickets.
    var assignable: [Member] {
        members.filter { $0.role != .viewer }.sorted { $0.user.name.localizedCaseInsensitiveCompare($1.user.name) == .orderedAscending }
    }

    func setMember(_ member: Member) {
        if let index = members.firstIndex(where: { $0.id == member.id }) { members[index] = member } else { members.append(member) }
    }

    // MARK: Realtime

    func handle(_ update: RealtimeUpdate) {
        switch update {
        case .resync:
            Task { await load() }
        case let .envelope(envelope):
            apply(envelope)
        }
    }

    private func apply(_ envelope: Envelope) {
        let type = envelope.type
        if type.hasPrefix("ticket.") {
            scheduleCounts()
            return
        }
        switch type {
        case "member.added", "member.updated":
            if let member = envelope.member("member", as: Member.self) { setMember(member) }
        case "member.removed":
            if let id = envelope.string("user_id") { members.removeAll { $0.id == id } }
        case "team.created", "team.updated":
            if let team = envelope.member("team", as: Team.self) { upsert(&teams, team) }
        case "team.deleted":
            if let id = envelope.string("team_id") { teams.removeAll { $0.id == id } }
        case "tag.created", "tag.updated":
            if let tag = envelope.member("tag", as: Tag.self) {
                if tag.archived == true { tags.removeAll { $0.id == tag.id } } else { upsert(&tags, tag) }
            }
        case "tag.deleted":
            if let id = envelope.string("tag_id") { tags.removeAll { $0.id == id } }
        case "tag.merged":
            if let id = envelope.string("tag_id") { tags.removeAll { $0.id == id } }
            if let into = envelope.member("into", as: Tag.self) { upsert(&tags, into) }
        case "view.created", "view.updated":
            if let view = envelope.member("view", as: SavedView.self) { upsert(&views, view) }
            scheduleCounts()
        case "view.deleted":
            if let id = envelope.string("view_id") { views.removeAll { $0.id == id } }
        case "saved_reply.created", "saved_reply.updated":
            if let reply = envelope.member("saved_reply", as: SavedReply.self) { upsert(&savedReplies, reply) }
        case "saved_reply.deleted":
            if let id = envelope.string("saved_reply_id") { savedReplies.removeAll { $0.id == id } }
        case "workspace.updated":
            Task { [weak self] in
                guard let self, let api = self.api else { return }
                if let config: AIConfig = try? await api.get("v1/ai/config") { self.ai = config }
            }
        default:
            break
        }
    }

    private func upsert<T: Identifiable>(_ list: inout [T], _ item: T) where T.ID == String {
        if let index = list.firstIndex(where: { $0.id == item.id }) { list[index] = item } else { list.append(item) }
    }
}
