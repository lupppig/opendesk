import Foundation
import Observation

/// The knowledge base of the open workspace: collections, the filtered article
/// list, every article for the stats, and the help center address. One store
/// per workspace, shared by the list and the article screens (`KBStore.shared`),
/// kept live from `kb_article.*` and `kb_collection.*` events while a KB screen is up.
@MainActor
@Observable
final class KBStore {
    static let shared = KBStore()

    private(set) var collections: [KBCollection] = []
    /// What the list shows: the collection, status and search filters applied by the server.
    private(set) var articles: [KBArticle] = []
    /// Every article, for the stats and per-collection counts.
    private(set) var everything: [KBArticle] = []
    private(set) var loading = false
    private(set) var loadedOnce = false
    private(set) var error: String?
    /// The help center's address (the default brand's), when it is on.
    private(set) var helpCenterURL: URL?

    var collectionID: String? { didSet { if oldValue != collectionID { reload() } } }
    var status: KBStatusFilter = .all { didSet { if oldValue != status { reload() } } }
    private(set) var search = ""

    @ObservationIgnored private var api: APIClient?
    @ObservationIgnored private var workspaceID: String?
    @ObservationIgnored private var loadTask: Task<Void, Never>?
    @ObservationIgnored private var searchTask: Task<Void, Never>?
    @ObservationIgnored private var fallbackHelpURL: URL?

    /// Binds the store to the session's workspace; a new workspace starts from nothing.
    func attach(api: APIClient, workspaceID: String?, helpFallback: URL?) {
        fallbackHelpURL = helpFallback
        if self.workspaceID == workspaceID, self.api != nil { return }
        self.api = api
        self.workspaceID = workspaceID
        collections = []
        articles = []
        everything = []
        loadedOnce = false
        error = nil
        helpCenterURL = helpFallback
        collectionID = nil
        status = .all
        search = ""
        reload()
        Task { await loadBrands() }
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

    func setSearch(_ text: String) {
        let value = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard value != search else { return }
        searchTask?.cancel()
        searchTask = Task {
            try? await Task.sleep(for: .milliseconds(250))
            guard !Task.isCancelled else { return }
            self.search = value
            self.reload()
        }
    }

    private func load() async {
        guard let api else { return }
        loading = true
        defer { loading = false }
        var query: [URLQueryItem] = [URLQueryItem(name: "limit", value: "200")]
        if let collectionID { query.append(URLQueryItem(name: "collection_id", value: collectionID)) }
        if status != .all { query.append(URLQueryItem(name: "status", value: status.rawValue)) }
        if !search.isEmpty { query.append(URLQueryItem(name: "q", value: search)) }
        let filtered = query
        async let collections: Page<KBCollection> = api.get("v1/kb/collections", query: [URLQueryItem(name: "limit", value: "100")])
        async let list: Page<KBArticle> = api.get("v1/kb/articles", query: filtered)
        async let all: Page<KBArticle>? = try? api.get("v1/kb/articles", query: [URLQueryItem(name: "limit", value: "200")])
        do {
            let (c, l) = try await (collections, list)
            guard !Task.isCancelled else { return }
            self.collections = c.data.sorted { $0.position != $1.position ? $0.position < $1.position : $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending }
            self.articles = l.data
            if let page = await all { self.everything = page.data }
            error = nil
            loadedOnce = true
        } catch {
            guard !error.isCancellation, !Task.isCancelled else { return }
            self.error = error.userMessage
        }
    }

    private func loadBrands() async {
        guard let api else { return }
        guard let page: Page<KBBrand> = try? await api.get("v1/brands") else { return }
        let brand = page.data.first(where: \.isDefault) ?? page.data.first
        if let help = brand?.helpCenter {
            helpCenterURL = help.enabled ? URL(string: help.url) : nil
        }
    }

    // MARK: Lookups

    func collection(_ id: String?) -> KBCollection? {
        guard let id else { return nil }
        return collections.first { $0.id == id }
    }

    /// A collection's hue by its place in the ordered list, so neighbours never share a colour (web `collectionTone`).
    func tone(for id: String?) -> Tone {
        guard let id, let index = collections.firstIndex(where: { $0.id == id }) else { return .gray }
        return Self.series[index % Self.series.count]
    }

    static let series: [Tone] = [.blue, .violet, .teal, .amber, .pink, .green]

    var publishedCount: Int { everything.filter { $0.status == .published }.count }
    var draftCount: Int { everything.count - publishedCount }
    var totalViews: Int { everything.filter { $0.status == .published }.reduce(0) { $0 + $1.views } }
    var topArticle: KBArticle? { everything.filter { $0.status == .published }.max { $0.views < $1.views } }

    func drafts(in collectionID: String) -> Int {
        everything.filter { $0.collectionID == collectionID && $0.status == .draft }.count
    }

    func views(in collectionID: String) -> Int {
        everything.filter { $0.collectionID == collectionID && $0.status == .published }.reduce(0) { $0 + $1.views }
    }

    /// The article's public page, when it is published and the help center is on.
    func publicURL(for article: KBArticle) -> URL? {
        guard article.status == .published, let base = helpCenterURL else { return nil }
        return URL(string: base.absoluteString.trimmingCharacters(in: CharacterSet(charactersIn: "/")) + "/a/" + article.slug)
    }

    func cached(_ id: String) -> KBArticle? {
        articles.first { $0.id == id } ?? everything.first { $0.id == id }
    }

    // MARK: Articles

    func article(_ id: String) async throws -> KBArticle {
        guard let api else { throw APIError.signedOut }
        let article: KBArticle = try await api.get("v1/kb/articles/\(id)")
        upsert(article)
        return article
    }

    func save(_ input: KBArticleInput, id: String?) async throws -> KBArticle {
        guard let api else { throw APIError.signedOut }
        let article: KBArticle
        if let id {
            article = try await api.patch("v1/kb/articles/\(id)", body: input)
        } else {
            article = try await api.post("v1/kb/articles", body: input)
        }
        upsert(article)
        Task { await reloadCollections() }
        return article
    }

    func delete(_ article: KBArticle) async throws {
        guard let api else { throw APIError.signedOut }
        try await api.delete("v1/kb/articles/\(article.id)")
        remove(articleID: article.id)
        Task { await reloadCollections() }
    }

    /// Ollie's draft from tickets and/or a topic (contracts/ai.md); never saved here.
    func draft(ticketIDs: [String], topic: String) async throws -> KBArticleDraft {
        guard let api else { throw APIError.signedOut }
        let request = KBDraftRequest(ticketIDs: ticketIDs.isEmpty ? nil : ticketIDs, topic: topic.isEmpty ? nil : topic)
        return try await api.post("v1/kb/articles/ai/draft", body: request)
    }

    /// Tickets to draft from, for the picker.
    func searchTickets(_ text: String) async -> [Ticket] {
        guard let api else { return [] }
        var query = [URLQueryItem(name: "limit", value: "8"), URLQueryItem(name: "spam", value: "false")]
        let trimmed = text.trimmingCharacters(in: .whitespaces)
        if !trimmed.isEmpty { query.append(URLQueryItem(name: "q", value: trimmed)) }
        let page: Page<Ticket>? = try? await api.get("v1/tickets", query: query)
        return page?.data ?? []
    }

    // MARK: Collections

    func createCollection(name: String, description: String) async throws -> KBCollection {
        guard let api else { throw APIError.signedOut }
        let collection: KBCollection = try await api.post("v1/kb/collections", body: KBCollectionInput(name: name, description: description))
        upsert(collection)
        return collection
    }

    func updateCollection(_ id: String, name: String, description: String) async throws {
        guard let api else { throw APIError.signedOut }
        let collection: KBCollection = try await api.patch("v1/kb/collections/\(id)", body: KBCollectionInput(name: name, description: description))
        upsert(collection)
    }

    func deleteCollection(_ collection: KBCollection) async throws {
        guard let api else { throw APIError.signedOut }
        let previous = collections
        collections.removeAll { $0.id == collection.id }
        if collectionID == collection.id { collectionID = nil }
        do {
            try await api.delete("v1/kb/collections/\(collection.id)")
            reload()
        } catch {
            collections = previous
            throw error
        }
    }

    private func reloadCollections() async {
        guard let api else { return }
        if let page: Page<KBCollection> = try? await api.get("v1/kb/collections", query: [URLQueryItem(name: "limit", value: "100")]) {
            collections = page.data.sorted { $0.position != $1.position ? $0.position < $1.position : $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending }
        }
    }

    // MARK: Cache

    private func upsert(_ collection: KBCollection) {
        if let index = collections.firstIndex(where: { $0.id == collection.id }) {
            collections[index] = collection
        } else {
            collections.append(collection)
        }
    }

    private func upsert(_ article: KBArticle) {
        if let index = everything.firstIndex(where: { $0.id == article.id }) { everything[index] = article } else { everything.insert(article, at: 0) }
        let fits = (collectionID == nil || article.collectionID == collectionID)
            && (status == .all || article.status.rawValue == status.rawValue)
            && (search.isEmpty || article.title.localizedCaseInsensitiveContains(search) || article.excerpt.localizedCaseInsensitiveContains(search))
        if let index = articles.firstIndex(where: { $0.id == article.id }) {
            if fits { articles[index] = article } else { articles.remove(at: index) }
        } else if fits {
            articles.insert(article, at: 0)
        }
        articles.sort { $0.updatedAt > $1.updatedAt }
    }

    private func remove(articleID: String) {
        articles.removeAll { $0.id == articleID }
        everything.removeAll { $0.id == articleID }
    }

    // MARK: Realtime

    func handle(_ update: RealtimeUpdate) {
        switch update {
        case .resync:
            reload()
        case let .envelope(envelope):
            switch envelope.type {
            case "kb_article.created", "kb_article.updated":
                if let article = envelope.member("article", as: KBArticle.self) ?? envelope.member("kb_article", as: KBArticle.self) { upsert(article) }
                Task { await reloadCollections() }
            case "kb_article.deleted":
                if let id = envelope.string("article_id") ?? envelope.string("kb_article_id") { remove(articleID: id) }
                Task { await reloadCollections() }
            case "kb_collection.created", "kb_collection.updated":
                if let collection = envelope.member("collection", as: KBCollection.self) ?? envelope.member("kb_collection", as: KBCollection.self) { upsert(collection) }
            case "kb_collection.deleted":
                if let id = envelope.string("collection_id") ?? envelope.string("kb_collection_id") {
                    collections.removeAll { $0.id == id }
                    if collectionID == id { collectionID = nil }
                }
            default:
                break
            }
        }
    }
}

/// Opens a new article with Ollie's draft sheet already up (the web's `requestKbDraft`).
@MainActor
enum KBDraftIntent {
    private static var requested = false

    static func request() { requested = true }

    static func take() -> Bool {
        defer { requested = false }
        return requested
    }
}
