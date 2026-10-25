import Foundation
import Observation

/// The companies list: search and a label filter, by name, paged, kept live by `company.*` events.
@MainActor
@Observable
final class CompaniesStore {
    private(set) var companies: [Company] = []
    private(set) var nextCursor: String?
    private(set) var loading = false
    private(set) var loadingMore = false
    private(set) var loadedOnce = false
    private(set) var error: String?
    private(set) var search = ""
    var tag: String? {
        didSet { if tag != oldValue { reload() } }
    }

    @ObservationIgnored private var api: APIClient?
    @ObservationIgnored private weak var realtime: RealtimeService?
    @ObservationIgnored private var listener: UUID?
    @ObservationIgnored private var generation = 0
    @ObservationIgnored private var reloadTask: Task<Void, Never>?
    @ObservationIgnored private var searchTask: Task<Void, Never>?
    @ObservationIgnored private var countTask: Task<Void, Never>?

    var isFiltered: Bool { !search.isEmpty || tag != nil }

    func start(api: APIClient, realtime: RealtimeService) {
        let fresh = self.api == nil
        self.api = api
        self.realtime = realtime
        if listener == nil {
            listener = realtime.subscribe { [weak self] update in self?.handle(update) }
        }
        if fresh { reload() }
    }

    func stop() {
        realtime?.unsubscribe(listener)
        listener = nil
    }

    func setSearch(_ text: String) {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard trimmed != search else { return }
        search = trimmed
        searchTask?.cancel()
        searchTask = Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(260))
            guard !Task.isCancelled else { return }
            self?.reload()
        }
    }

    private var query: [URLQueryItem] {
        var items = [URLQueryItem(name: "limit", value: "50")]
        if !search.isEmpty { items.append(URLQueryItem(name: "q", value: search)) }
        if let tag { items.append(URLQueryItem(name: "tag", value: tag)) }
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
            let page: Page<Company> = try await api.get("v1/companies", query: query)
            guard current == generation else { return }
            companies = page.data
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
        let current = generation
        loadingMore = true
        defer { loadingMore = false }
        if let page: Page<Company> = try? await api.get("v1/companies", query: query + [URLQueryItem(name: "cursor", value: cursor)]), current == generation {
            let known = Set(companies.map(\.id))
            companies += page.data.filter { !known.contains($0.id) }
            nextCursor = page.nextCursor
        }
    }

    // MARK: Changes

    func create(_ body: DirectoryPatch) async throws -> Company {
        guard let api else { throw APIError.signedOut }
        let company: Company = try await DirectoryRequests.create(.company, body, api: api)
        upsert(company)
        return company
    }

    func delete(_ company: Company) async throws {
        guard let api else { throw APIError.signedOut }
        let index = companies.firstIndex { $0.id == company.id }
        companies.removeAll { $0.id == company.id }
        do {
            try await DirectoryRequests.delete(.company, id: company.id, api: api)
        } catch {
            if let index { companies.insert(company, at: min(index, companies.count)) }
            throw error
        }
    }

    private func upsert(_ company: Company) {
        if let index = companies.firstIndex(where: { $0.id == company.id }) {
            if matches(company) { companies[index] = company } else { companies.remove(at: index) }
        } else if matches(company) {
            // The list is by name, as the API orders it.
            let index = companies.firstIndex { $0.name.localizedCaseInsensitiveCompare(company.name) == .orderedDescending } ?? companies.count
            guard index < companies.count || nextCursor == nil else { return }
            companies.insert(company, at: index)
        }
    }

    private func matches(_ company: Company) -> Bool {
        if let tag, !company.tags.contains(where: { $0.name.caseInsensitiveCompare(tag) == .orderedSame }) { return false }
        if !search.isEmpty, !company.name.localizedCaseInsensitiveContains(search), !company.domain.localizedCaseInsensitiveContains(search) { return false }
        return true
    }

    /// People counts move when contacts join or leave; one refetch of the first page for a burst.
    private func scheduleRefresh() {
        guard countTask == nil, companies.count <= 50 else { return }
        countTask = Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(500))
            guard let self else { return }
            self.countTask = nil
            await self.refresh()
        }
    }

    // MARK: Realtime

    func handle(_ update: RealtimeUpdate) {
        switch update {
        case .resync:
            reload()
        case let .envelope(envelope):
            switch envelope.type {
            case "company.created", "company.updated":
                if let company = envelope.member("company", as: Company.self) { upsert(company) }
            case "company.deleted":
                if let id = envelope.string("company_id") { companies.removeAll { $0.id == id } }
            case "contact.created", "contact.deleted":
                scheduleRefresh()
            case "contact.updated":
                if envelope.member("contact", as: Contact.self)?.company != nil { scheduleRefresh() }
            default:
                break
            }
        }
    }
}
