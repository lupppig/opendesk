import Foundation
import Observation

/// The knowledge base's changelog (contracts/api.md "Changelog"): every entry of
/// the open workspace, filtered here by status and category, searched by the
/// server, and the categories in use. Shared by the list and the entry screens
/// (`ChangelogStore.shared`), kept live from `changelog_entry.*` events.
@MainActor
@Observable
final class ChangelogStore {
    static let shared = ChangelogStore()

    /// Every entry, newest first.
    private(set) var entries: [ChangelogEntry] = []
    /// The categories the workspace's entries use, most used first.
    private(set) var categories: [ChangelogCategory] = []
    private(set) var loading = false
    private(set) var loadedOnce = false
    private(set) var error: String?
    /// The server's best matches while searching.
    private(set) var matches: [ChangelogEntry]?
    private(set) var search = ""

    var filter: ChangelogFilter = .all
    var category: String?

    @ObservationIgnored private var api: APIClient?
    @ObservationIgnored private var workspaceID: String?
    @ObservationIgnored private var loadTask: Task<Void, Never>?
    @ObservationIgnored private var searchTask: Task<Void, Never>?

    /// Binds the store to the session's workspace; a new workspace starts from nothing.
    func attach(api: APIClient, workspaceID: String?) {
        if self.workspaceID == workspaceID, self.api != nil { return }
        self.api = api
        self.workspaceID = workspaceID
        entries = []
        categories = []
        matches = nil
        search = ""
        filter = .all
        category = nil
        loadedOnce = false
        error = nil
        reload()
    }

    // MARK: Loading

    func reload() {
        loadTask?.cancel()
        loadTask = Task { await load() }
    }

    func refresh() async {
        loadTask?.cancel()
        await load()
    }

    private func load() async {
        guard let api else { return }
        loading = true
        defer { loading = false }
        do {
            var all: [ChangelogEntry] = []
            var cursor: String?
            // Up to 500 entries, a page of 100 at a time.
            for _ in 0 ..< 5 {
                var query = [URLQueryItem(name: "limit", value: "100")]
                if let cursor { query.append(URLQueryItem(name: "cursor", value: cursor)) }
                let page: Page<ChangelogEntry> = try await api.get("v1/changelog/entries", query: query)
                all += page.data
                cursor = page.nextCursor
                if cursor == nil { break }
            }
            guard !Task.isCancelled else { return }
            entries = all
            sort()
            error = nil
            loadedOnce = true
            await loadCategories()
        } catch {
            guard !error.isCancellation, !Task.isCancelled else { return }
            self.error = error.userMessage
        }
    }

    private func loadCategories() async {
        guard let api else { return }
        if let list: Page<ChangelogCategory> = try? await api.get("v1/changelog/categories") {
            categories = list.data.filter { !$0.name.isEmpty }
        }
    }

    func setSearch(_ text: String) {
        let value = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard value != search else { return }
        searchTask?.cancel()
        guard !value.isEmpty else {
            search = ""
            matches = nil
            return
        }
        searchTask = Task {
            try? await Task.sleep(for: .milliseconds(250))
            guard !Task.isCancelled, let api else { return }
            let found: Page<ChangelogEntry>? = try? await api.get("v1/changelog/entries", query: [URLQueryItem(name: "q", value: value), URLQueryItem(name: "limit", value: "50")])
            guard !Task.isCancelled else { return }
            search = value
            matches = found?.data ?? []
        }
    }

    // MARK: Lists

    /// What the list shows: the search's matches or every entry, with the filters applied.
    func list(now: Date = AppClock.now) -> [ChangelogEntry] {
        (matches ?? entries).filter { inCategory($0) && filter.matches($0, now: now) }
    }

    func count(_ filter: ChangelogFilter, now: Date = AppClock.now) -> Int {
        entries.filter { inCategory($0) && filter.matches($0, now: now) }.count
    }

    private func inCategory(_ entry: ChangelogEntry) -> Bool {
        guard let category else { return true }
        return entry.categories.contains { $0.caseInsensitiveCompare(category) == .orderedSame }
    }

    func cached(_ id: String) -> ChangelogEntry? {
        entries.first { $0.id == id } ?? matches?.first { $0.id == id }
    }

    /// The entry's page on the help center, once it is live there.
    func publicURL(for entry: ChangelogEntry, helpCenter: URL?) -> URL? {
        guard entry.state() == .published, let base = helpCenter else { return nil }
        return URL(string: base.absoluteString.trimmingCharacters(in: CharacterSet(charactersIn: "/")) + "/changelog/" + entry.slug)
    }

    // MARK: Entries

    func entry(_ id: String) async throws -> ChangelogEntry {
        guard let api else { throw APIError.signedOut }
        let entry: ChangelogEntry = try await api.get("v1/changelog/entries/\(id)")
        upsert(entry)
        return entry
    }

    func save(_ input: ChangelogEntryInput, id: String?) async throws -> ChangelogEntry {
        guard let api else { throw APIError.signedOut }
        let entry: ChangelogEntry
        if let id {
            entry = try await api.patch("v1/changelog/entries/\(id)", body: input)
        } else {
            entry = try await api.post("v1/changelog/entries", body: input)
        }
        upsert(entry)
        Task { await loadCategories() }
        return entry
    }

    func delete(_ entry: ChangelogEntry) async throws {
        guard let api else { throw APIError.signedOut }
        try await api.delete("v1/changelog/entries/\(entry.id)")
        remove(entry.id)
        Task { await loadCategories() }
    }

    // MARK: Cache

    private func sort() {
        entries.sort { $0.sortDate != $1.sortDate ? $0.sortDate > $1.sortDate : $0.id > $1.id }
    }

    private func upsert(_ entry: ChangelogEntry) {
        if let index = entries.firstIndex(where: { $0.id == entry.id }) { entries[index] = entry } else { entries.append(entry) }
        if let index = matches?.firstIndex(where: { $0.id == entry.id }) { matches?[index] = entry }
        sort()
    }

    private func remove(_ id: String) {
        entries.removeAll { $0.id == id }
        matches?.removeAll { $0.id == id }
    }

    // MARK: Realtime

    func handle(_ update: RealtimeUpdate) {
        switch update {
        case .resync:
            reload()
        case let .envelope(envelope):
            switch envelope.type {
            case "changelog_entry.created", "changelog_entry.updated":
                if let entry = envelope.member("changelog_entry", as: ChangelogEntry.self) { upsert(entry) }
                Task { await loadCategories() }
            case "changelog_entry.deleted":
                if let id = envelope.string("changelog_entry_id") { remove(id) }
                Task { await loadCategories() }
            default:
                break
            }
        }
    }
}
