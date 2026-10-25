import Foundation
import Observation

/// A knowledge base article as search returns it (the part a result row needs).
struct SearchArticle: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var title: String
    var excerpt: String
    var status: String

    enum CodingKeys: String, CodingKey { case id, title, excerpt, status }

    init(id: String, title: String, excerpt: String = "", status: String = "published") {
        self.id = id
        self.title = title
        self.excerpt = excerpt
        self.status = status
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        title = c.lenient(String.self, forKey: .title) ?? ""
        excerpt = c.lenient(String.self, forKey: .excerpt) ?? ""
        status = c.lenient(String.self, forKey: .status) ?? "published"
    }

    var isDraft: Bool { status == "draft" }
}

/// `GET /v1/search` and `GET /v1/ai/search`: tickets, contacts and articles.
struct SearchHits: Decodable, Sendable {
    var tickets: [Ticket] = []
    var contacts: [Contact] = []
    var articles: [SearchArticle] = []

    enum CodingKeys: String, CodingKey { case tickets, contacts, articles }

    init() {}

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        tickets = c.lenient([Ticket].self, forKey: .tickets) ?? []
        contacts = c.lenient([Contact].self, forKey: .contacts) ?? []
        articles = c.lenient([SearchArticle].self, forKey: .articles) ?? []
    }

    var isEmpty: Bool { tickets.isEmpty && contacts.isEmpty && articles.isEmpty }
}

/// Small settings of the inbox and search (recent searches, the chat alert sound), in the
/// app's defaults (a suite of their own in demo mode, emptied at launch).
enum InboxDefaults {
    static var store: UserDefaults {
        #if DEBUG
        if DemoMode.isOn { return DemoMode.defaults }
        #endif
        return .standard
    }
}

/// Search across the workspace, by words or by meaning (Ollie).
@MainActor
@Observable
final class SearchStore {
    enum Mode: String, Sendable { case words, ollie }

    private(set) var query = ""
    private(set) var mode: Mode = .words
    private(set) var hits: SearchHits?
    private(set) var searching = false
    private(set) var error: String?
    private(set) var recent: [String] = []

    @ObservationIgnored private var api: APIClient?
    @ObservationIgnored private var task: Task<Void, Never>?
    @ObservationIgnored private var recentKey = "search.recent"

    func start(api: APIClient, workspaceID: String?) {
        self.api = api
        recentKey = "search.recent." + api.instance.storageKey + "." + (workspaceID ?? "")
        recent = InboxDefaults.store.stringArray(forKey: recentKey) ?? []
    }

    func setQuery(_ text: String) {
        guard text != query else { return }
        query = text
        schedule()
    }

    func setMode(_ next: Mode) {
        guard next != mode else { return }
        mode = next
        hits = nil
        schedule(delay: 0)
    }

    private var trimmed: String { query.trimmingCharacters(in: .whitespacesAndNewlines) }

    private func schedule(delay: Int = 250) {
        task?.cancel()
        let text = trimmed
        guard text.count > 1 else {
            hits = nil
            searching = false
            error = nil
            return
        }
        searching = true
        let mode = mode
        task = Task { [weak self] in
            if delay > 0 { try? await Task.sleep(for: .milliseconds(mode == .ollie ? delay + 200 : delay)) }
            guard !Task.isCancelled else { return }
            await self?.run(text, mode: mode)
        }
    }

    private func run(_ text: String, mode: Mode) async {
        guard let api else { return }
        do {
            let path = mode == .ollie ? "v1/ai/search" : "v1/search"
            let result: SearchHits = try await api.get(path, query: [URLQueryItem(name: "q", value: text)])
            guard !Task.isCancelled, text == trimmed, mode == self.mode else { return }
            hits = result
            error = nil
        } catch {
            guard !error.isCancellation, text == trimmed else { return }
            self.error = error.userMessage
        }
        if text == trimmed { searching = false }
    }

    func retry() { schedule(delay: 0) }

    /// Remembers what was searched once a result is opened or the search is submitted.
    func remember() {
        let text = trimmed
        guard text.count > 1 else { return }
        recent.removeAll { $0.caseInsensitiveCompare(text) == .orderedSame }
        recent.insert(text, at: 0)
        recent = Array(recent.prefix(8))
        InboxDefaults.store.set(recent, forKey: recentKey)
    }

    func forget(_ text: String) {
        recent.removeAll { $0 == text }
        InboxDefaults.store.set(recent, forKey: recentKey)
    }

    func clearRecent() {
        recent = []
        InboxDefaults.store.removeObject(forKey: recentKey)
    }
}
