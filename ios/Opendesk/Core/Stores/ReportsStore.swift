import Foundation
import Observation

/// Reports for a period and team: the overview, the same-length period before
/// it for deltas, and the open work behind the backlog, priority and label cards.
@MainActor
@Observable
final class ReportsStore {
    private(set) var overview: ReportOverview?
    private(set) var previous: ReportOverview?
    /// The open tickets (open, pending, on hold) the report's filter narrows to.
    private(set) var openTickets: [Ticket] = []
    /// More open tickets exist than were loaded; counts from them are a sample.
    private(set) var openPartial = false
    private(set) var loading = false
    private(set) var loadingOpen = false
    private(set) var error: String?

    var range: ReportRange = .preset(.month) { didSet { if oldValue != range { reload() } } }
    var team: ReportTeamFilter = .all { didSet { if oldValue != team { reload() } } }

    @ObservationIgnored private var api: APIClient?
    @ObservationIgnored private var workspaceID: String?
    @ObservationIgnored private var task: Task<Void, Never>?

    func attach(api: APIClient, workspaceID: String?) {
        guard self.workspaceID != workspaceID || self.api == nil else { return }
        self.api = api
        self.workspaceID = workspaceID
        overview = nil
        previous = nil
        openTickets = []
        error = nil
        reload()
    }

    func reload() {
        task?.cancel()
        task = Task { await load() }
    }

    func refresh() async {
        task?.cancel()
        await load()
    }

    private func query(_ range: ReportRange) -> [URLQueryItem] {
        var items = range.query
        if let value = team.queryValue { items.append(URLQueryItem(name: "team_id", value: value)) }
        return items
    }

    private func load() async {
        guard let api else { return }
        loading = true
        loadingOpen = true
        defer {
            loading = false
            loadingOpen = false
        }
        async let current: ReportOverview = api.get("v1/reports/overview", query: query(range))
        async let before: ReportOverview? = try? api.get("v1/reports/overview", query: query(range.previous))
        async let open = loadOpen(api)
        do {
            let value = try await current
            guard !Task.isCancelled else { return }
            overview = value
            error = nil
        } catch {
            guard !error.isCancellation, !Task.isCancelled else { return }
            self.error = error.userMessage
        }
        let earlier = await before
        guard !Task.isCancelled else { return }
        previous = earlier
        let (tickets, partial) = await open
        guard !Task.isCancelled else { return }
        openTickets = tickets
        openPartial = partial
    }

    /// Up to three pages of open work, narrowed like the report.
    private func loadOpen(_ api: APIClient) async -> ([Ticket], Bool) {
        var tickets: [Ticket] = []
        var cursor: String?
        for _ in 0 ..< 3 {
            var items = [URLQueryItem(name: "status", value: "open,pending,on_hold"), URLQueryItem(name: "limit", value: "100"), URLQueryItem(name: "spam", value: "false")]
            switch team {
            case .all, .none: break
            case let .team(id): items.append(URLQueryItem(name: "team_id", value: id))
            }
            if let cursor { items.append(URLQueryItem(name: "cursor", value: cursor)) }
            guard let page: Page<Ticket> = try? await api.get("v1/tickets", query: items) else { break }
            tickets += page.data
            cursor = page.nextCursor
            if cursor == nil { break }
        }
        if case .none = team { tickets = tickets.filter { $0.teamID == nil } }
        return (tickets, cursor != nil)
    }
}
