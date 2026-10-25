import Foundation
import Observation

/// One contact with its recent tickets and its field definitions, kept live by
/// `contact.*` and `ticket.*` events while the screen is on.
@MainActor
@Observable
final class ContactDetailStore {
    let id: String
    private(set) var contact: Contact?
    private(set) var tickets: [Ticket] = []
    private(set) var ticketsLoaded = false
    private(set) var moreTickets = false
    private(set) var fields: [CustomField] = []
    private(set) var error: String?
    /// Deleted here or elsewhere: the screen steps back.
    private(set) var deleted = false
    private(set) var avatarBusy = false

    @ObservationIgnored private var api: APIClient?
    @ObservationIgnored private weak var realtime: RealtimeService?
    @ObservationIgnored private var listener: UUID?

    init(id: String) {
        self.id = id
    }

    /// Subscribes, and loads on the first call (refreshes quietly when the screen comes back).
    func start(api: APIClient, realtime: RealtimeService) {
        let fresh = self.api == nil
        self.api = api
        self.realtime = realtime
        if listener == nil {
            listener = realtime.subscribe { [weak self] update in self?.handle(update) }
        }
        Task {
            if fresh {
                await load()
            } else {
                await refresh()
            }
        }
    }

    func stop() {
        realtime?.unsubscribe(listener)
        listener = nil
    }

    func load() async {
        guard let api else { return }
        error = nil
        async let contact: Contact = api.get("v1/contacts/\(id)")
        async let fields: Page<CustomField>? = try? api.get("v1/custom-fields", query: [URLQueryItem(name: "entity", value: "contact"), URLQueryItem(name: "archived", value: "false")])
        do {
            self.contact = try await contact
            if let page = await fields { self.fields = page.data.filter { $0.entity == "contact" && !$0.archived }.sorted { $0.position < $1.position } }
            await loadTickets()
        } catch {
            guard !error.isCancellation else { return }
            self.error = error.userMessage
        }
    }

    func refresh() async {
        guard let api else { return }
        if let fresh: Contact = try? await api.get("v1/contacts/\(id)") { contact = fresh }
        await loadTickets()
    }

    func loadTickets() async {
        guard let api else { return }
        if let page: Page<Ticket> = try? await api.get("v1/contacts/\(id)/tickets", query: [URLQueryItem(name: "limit", value: "25")]) {
            tickets = page.data.sorted { $0.lastMessageAt > $1.lastMessageAt }
            moreTickets = page.nextCursor != nil
        }
        ticketsLoaded = true
    }

    // MARK: Changes

    /// Sends `body`, showing `change` applied until the answer lands; rolls back on error.
    func patch(_ body: DirectoryPatch, _ change: (inout Contact) -> Void = { _ in }) async throws {
        guard let api, let before = contact else { return }
        var next = before
        change(&next)
        contact = next
        do {
            contact = try await DirectoryRequests.update(.contact, id: id, body, api: api) as Contact
        } catch {
            contact = before
            throw error
        }
    }

    func setTags(_ names: [String], known: [Tag]) async throws {
        try await patch(.tags(names)) { contact in
            let refs = Dictionary(contact.tags.map { ($0.name.lowercased(), $0) }, uniquingKeysWith: { a, _ in a })
            let all = Dictionary(known.map { ($0.name.lowercased(), $0.ref) }, uniquingKeysWith: { a, _ in a })
            contact.tags = names.map { refs[$0.lowercased()] ?? all[$0.lowercased()] ?? TagRef(id: "new-" + $0, name: $0, color: "gray", groupID: nil) }
        }
    }

    func setField(_ key: String, _ value: JSONValue?) async throws {
        try await patch(.field(key, value)) { contact in contact.fields[key] = value }
    }

    func setBlocked(_ blocked: Bool) async throws {
        var body = DirectoryPatch()
        body["blocked"] = .bool(blocked)
        try await patch(body) { $0.blocked = blocked }
    }

    /// The edit sheet's save: the answer replaces the contact.
    func save(_ body: DirectoryPatch) async throws {
        guard let api, !body.isEmpty else { return }
        contact = try await DirectoryRequests.update(.contact, id: id, body, api: api) as Contact
    }

    func uploadAvatar(_ data: Data, mimeType: String) async throws {
        guard let api else { return }
        avatarBusy = true
        defer { avatarBusy = false }
        contact = try await DirectoryRequests.uploadAvatar(.contact, id: id, data: data, mimeType: mimeType, api: api) as Contact
    }

    func removeAvatar() async throws {
        guard let api else { return }
        avatarBusy = true
        defer { avatarBusy = false }
        contact = try await DirectoryRequests.removeAvatar(.contact, id: id, api: api) as Contact
    }

    func delete() async throws {
        guard let api else { return }
        try await DirectoryRequests.delete(.contact, id: id, api: api)
        deleted = true
    }

    // MARK: Realtime

    private func handle(_ update: RealtimeUpdate) {
        switch update {
        case .resync:
            Task { await refresh() }
        case let .envelope(envelope):
            switch envelope.type {
            case "contact.updated":
                if let next = envelope.member("contact", as: Contact.self), next.id == id { contact = next }
            case "contact.deleted":
                if envelope.string("contact_id") == id { deleted = true }
            case "company.updated":
                if let company = envelope.member("company", as: Company.self), contact?.company?.id == company.id { contact?.company = company.ref }
            case "company.deleted":
                if let gone = envelope.string("company_id"), contact?.company?.id == gone { contact?.company = nil }
            case "ticket.created", "ticket.updated":
                guard let ticket = envelope.member("ticket", as: Ticket.self) else { return }
                if let index = tickets.firstIndex(where: { $0.id == ticket.id }) {
                    if ticket.contact?.id == id { tickets[index] = ticket } else { tickets.remove(at: index) }
                } else if ticket.contact?.id == id {
                    tickets.insert(ticket, at: 0)
                }
                tickets.sort { $0.lastMessageAt > $1.lastMessageAt }
            case "ticket.deleted":
                if let gone = envelope.string("ticket_id") { tickets.removeAll { $0.id == gone } }
            default:
                break
            }
        }
    }
}
