import Foundation
import Observation

/// The contacts and companies routes, shared by the list and detail stores.
enum DirectoryRequests {
    static func create<T: Decodable & Sendable>(_ entity: DirectoryEntity, _ body: DirectoryPatch, api: APIClient) async throws -> T {
        try await api.post(entity.path, body: body)
    }

    static func update<T: Decodable & Sendable>(_ entity: DirectoryEntity, id: String, _ body: DirectoryPatch, api: APIClient) async throws -> T {
        try await api.patch("\(entity.path)/\(id)", body: body)
    }

    static func delete(_ entity: DirectoryEntity, id: String, api: APIClient) async throws {
        try await api.delete("\(entity.path)/\(id)")
    }

    /// `PUT …/avatar` with one multipart `file` part.
    static func uploadAvatar<T: Decodable & Sendable>(_ entity: DirectoryEntity, id: String, data: Data, mimeType: String, api: APIClient) async throws -> T {
        let boundary = "od-" + UUID().uuidString
        let ext = mimeType == "image/png" ? "png" : "jpg"
        var body = Data()
        body.append(Data("--\(boundary)\r\n".utf8))
        body.append(Data("Content-Disposition: form-data; name=\"file\"; filename=\"picture.\(ext)\"\r\n".utf8))
        body.append(Data("Content-Type: \(mimeType)\r\n\r\n".utf8))
        body.append(data)
        body.append(Data("\r\n--\(boundary)--\r\n".utf8))
        var request = APIRequest(method: "PUT", path: "\(entity.path)/\(id)/avatar", body: body, contentType: "multipart/form-data; boundary=\(boundary)")
        request.timeout = 120
        return try await api.send(request)
    }

    /// `DELETE …/avatar`: the record with the automatic picture (if any) back.
    static func removeAvatar<T: Decodable & Sendable>(_ entity: DirectoryEntity, id: String, api: APIClient) async throws -> T {
        try await api.send(APIRequest(method: "DELETE", path: "\(entity.path)/\(id)/avatar", body: nil, contentType: nil))
    }
}

/// The contacts list: search, a company or label filter, newest first or by
/// name, paged, and patched in place by `contact.*` events.
@MainActor
@Observable
final class ContactsStore {
    private(set) var contacts: [Contact] = []
    private(set) var nextCursor: String?
    private(set) var loading = false
    private(set) var loadingMore = false
    private(set) var loadedOnce = false
    private(set) var error: String?
    private(set) var search = ""
    var sort: ContactSort = .recent
    /// A label name the list is narrowed to.
    var tag: String? {
        didSet { if tag != oldValue { reload() } }
    }

    /// A company the list is narrowed to.
    var company: CompanyRef? {
        didSet { if company?.id != oldValue?.id { reload() } }
    }

    @ObservationIgnored private var api: APIClient?
    @ObservationIgnored private weak var realtime: RealtimeService?
    @ObservationIgnored private var listener: UUID?
    @ObservationIgnored private var generation = 0
    @ObservationIgnored private var reloadTask: Task<Void, Never>?
    @ObservationIgnored private var searchTask: Task<Void, Never>?

    var isFiltered: Bool { !search.isEmpty || tag != nil || company != nil }

    /// Subscribes and loads once; later calls only resubscribe after `stop()`.
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
        if let company { items.append(URLQueryItem(name: "company_id", value: company.id)) }
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
            let page: Page<Contact> = try await api.get("v1/contacts", query: query)
            guard current == generation else { return }
            contacts = page.data
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
        do {
            let page: Page<Contact> = try await api.get("v1/contacts", query: query + [URLQueryItem(name: "cursor", value: cursor)])
            guard current == generation else { return }
            let known = Set(contacts.map(\.id))
            contacts += page.data.filter { !known.contains($0.id) }
            nextCursor = page.nextCursor
        } catch {
            // The next scroll to the end tries again.
        }
    }

    // MARK: Ordering

    /// Contacts in the chosen order.
    var ordered: [Contact] {
        switch sort {
        case .recent: contacts
        case .name: contacts.sorted { $0.displayName.localizedCaseInsensitiveCompare($1.displayName) == .orderedAscending }
        }
    }

    /// Letter sections for the name order ("#" for anything that isn't a letter).
    var sections: [(letter: String, contacts: [Contact])] {
        var result: [(letter: String, contacts: [Contact])] = []
        for contact in ordered {
            let first = contact.displayName.folding(options: [.diacriticInsensitive, .caseInsensitive], locale: .current).first.map { String($0).uppercased() } ?? "#"
            let letter = first.rangeOfCharacter(from: .letters) == nil ? "#" : first
            if result.last?.letter == letter { result[result.count - 1].contacts.append(contact) } else { result.append((letter, [contact])) }
        }
        return result
    }

    // MARK: Changes

    func create(_ body: DirectoryPatch) async throws -> Contact {
        guard let api else { throw APIError.signedOut }
        let contact: Contact = try await DirectoryRequests.create(.contact, body, api: api)
        upsert(contact, insertAtTop: true)
        return contact
    }

    /// Patches a contact, showing `optimistic` until the answer lands (rolled back on error).
    @discardableResult
    func update(_ contact: Contact, _ body: DirectoryPatch, optimistic: Contact? = nil) async throws -> Contact {
        guard let api else { throw APIError.signedOut }
        if let optimistic { replace(optimistic) }
        do {
            let updated: Contact = try await DirectoryRequests.update(.contact, id: contact.id, body, api: api)
            replace(updated)
            return updated
        } catch {
            replace(contact)
            throw error
        }
    }

    func setBlocked(_ contact: Contact, _ blocked: Bool) async throws {
        var body = DirectoryPatch()
        body["blocked"] = .bool(blocked)
        var next = contact
        next.blocked = blocked
        try await update(contact, body, optimistic: next)
    }

    func delete(_ contact: Contact) async throws {
        guard let api else { throw APIError.signedOut }
        let index = contacts.firstIndex { $0.id == contact.id }
        contacts.removeAll { $0.id == contact.id }
        do {
            try await DirectoryRequests.delete(.contact, id: contact.id, api: api)
        } catch {
            if let index { contacts.insert(contact, at: min(index, contacts.count)) }
            throw error
        }
    }

    private func replace(_ contact: Contact) {
        guard let index = contacts.firstIndex(where: { $0.id == contact.id }) else { return }
        if matches(contact) { contacts[index] = contact } else { contacts.remove(at: index) }
    }

    private func upsert(_ contact: Contact, insertAtTop: Bool) {
        if contacts.contains(where: { $0.id == contact.id }) {
            replace(contact)
        } else if insertAtTop, matches(contact) {
            contacts.insert(contact, at: 0)
        }
    }

    /// Whether a contact belongs in the list as filtered (for live inserts).
    private func matches(_ contact: Contact) -> Bool {
        if let company, contact.company?.id != company.id { return false }
        if let tag, !contact.tags.contains(where: { $0.name.caseInsensitiveCompare(tag) == .orderedSame }) { return false }
        if !search.isEmpty {
            let haystack = [contact.name, contact.email ?? "", contact.company?.name ?? "", contact.phone]
            if !haystack.contains(where: { $0.localizedCaseInsensitiveContains(search) }) { return false }
        }
        return true
    }

    // MARK: Realtime

    func handle(_ update: RealtimeUpdate) {
        switch update {
        case .resync:
            reload()
        case let .envelope(envelope):
            switch envelope.type {
            case "contact.created":
                if let contact = envelope.member("contact", as: Contact.self) { upsert(contact, insertAtTop: true) }
            case "contact.updated":
                if let contact = envelope.member("contact", as: Contact.self) { replace(contact) }
            case "contact.deleted":
                if let id = envelope.string("contact_id") { contacts.removeAll { $0.id == id } }
            case "company.updated":
                if let company = envelope.member("company", as: Company.self) {
                    for index in contacts.indices where contacts[index].company?.id == company.id { contacts[index].company = company.ref }
                    if self.company?.id == company.id { self.company = company.ref }
                }
            case "company.deleted":
                if let id = envelope.string("company_id") {
                    for index in contacts.indices where contacts[index].company?.id == id { contacts[index].company = nil }
                    if company?.id == id { company = nil }
                }
            default:
                break
            }
        }
    }
}
