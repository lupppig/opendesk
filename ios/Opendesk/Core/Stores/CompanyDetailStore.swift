import Foundation
import Observation

/// One company with its people and their recent tickets, kept live by
/// `company.*`, `contact.*` and `ticket.*` events while the screen is on.
@MainActor
@Observable
final class CompanyDetailStore {
    let id: String
    private(set) var company: Company?
    private(set) var people: [Contact] = []
    private(set) var morePeople = false
    private(set) var peopleLoaded = false
    private(set) var tickets: [Ticket] = []
    private(set) var ticketsLoaded = false
    private(set) var fields: [CustomField] = []
    private(set) var error: String?
    private(set) var deleted = false
    private(set) var avatarBusy = false

    @ObservationIgnored private var api: APIClient?
    @ObservationIgnored private weak var realtime: RealtimeService?
    @ObservationIgnored private var listener: UUID?
    @ObservationIgnored private var peopleTask: Task<Void, Never>?

    init(id: String) {
        self.id = id
    }

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
        async let company: Company = api.get("v1/companies/\(id)")
        async let fields: Page<CustomField>? = try? api.get("v1/custom-fields", query: [URLQueryItem(name: "entity", value: "company"), URLQueryItem(name: "archived", value: "false")])
        do {
            self.company = try await company
            if let page = await fields { self.fields = page.data.filter { $0.entity == "company" && !$0.archived }.sorted { $0.position < $1.position } }
            await loadPeople()
        } catch {
            guard !error.isCancellation else { return }
            self.error = error.userMessage
        }
    }

    func refresh() async {
        guard let api else { return }
        if let fresh: Company = try? await api.get("v1/companies/\(id)") { company = fresh }
        await loadPeople()
    }

    /// The company's people, then the latest tickets from any of them.
    func loadPeople() async {
        guard let api else { return }
        if let page: Page<Contact> = try? await api.get("v1/contacts", query: [URLQueryItem(name: "company_id", value: id), URLQueryItem(name: "limit", value: "100")]) {
            people = page.data.sorted { $0.displayName.localizedCaseInsensitiveCompare($1.displayName) == .orderedAscending }
            morePeople = page.nextCursor != nil
        }
        peopleLoaded = true
        let ids = people.filter { $0.ticketCount > 0 }.prefix(20).map(\.id)
        var found: [Ticket] = []
        await withTaskGroup(of: [Ticket].self) { group in
            for contact in ids {
                group.addTask {
                    let page: Page<Ticket>? = try? await api.get("v1/contacts/\(contact)/tickets", query: [URLQueryItem(name: "limit", value: "10")])
                    return page?.data ?? []
                }
            }
            for await batch in group { found += batch }
        }
        var seen = Set<String>()
        tickets = found.sorted { $0.lastMessageAt > $1.lastMessageAt }.filter { seen.insert($0.id).inserted }
        ticketsLoaded = true
    }

    private func schedulePeople() {
        peopleTask?.cancel()
        peopleTask = Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(350))
            guard !Task.isCancelled, let self else { return }
            await self.loadPeople()
        }
    }

    // MARK: Changes

    func patch(_ body: DirectoryPatch, _ change: (inout Company) -> Void = { _ in }) async throws {
        guard let api, let before = company else { return }
        var next = before
        change(&next)
        company = next
        do {
            company = try await DirectoryRequests.update(.company, id: id, body, api: api) as Company
        } catch {
            company = before
            throw error
        }
    }

    func setTags(_ names: [String], known: [Tag]) async throws {
        try await patch(.tags(names)) { company in
            let refs = Dictionary(company.tags.map { ($0.name.lowercased(), $0) }, uniquingKeysWith: { a, _ in a })
            let all = Dictionary(known.map { ($0.name.lowercased(), $0.ref) }, uniquingKeysWith: { a, _ in a })
            company.tags = names.map { refs[$0.lowercased()] ?? all[$0.lowercased()] ?? TagRef(id: "new-" + $0, name: $0, color: "gray", groupID: nil) }
        }
    }

    func setField(_ key: String, _ value: JSONValue?) async throws {
        try await patch(.field(key, value)) { company in company.fields[key] = value }
    }

    func save(_ body: DirectoryPatch) async throws {
        guard let api, !body.isEmpty else { return }
        company = try await DirectoryRequests.update(.company, id: id, body, api: api) as Company
    }

    func uploadAvatar(_ data: Data, mimeType: String) async throws {
        guard let api else { return }
        avatarBusy = true
        defer { avatarBusy = false }
        company = try await DirectoryRequests.uploadAvatar(.company, id: id, data: data, mimeType: mimeType, api: api) as Company
    }

    func removeAvatar() async throws {
        guard let api else { return }
        avatarBusy = true
        defer { avatarBusy = false }
        company = try await DirectoryRequests.removeAvatar(.company, id: id, api: api) as Company
    }

    func delete() async throws {
        guard let api else { return }
        try await DirectoryRequests.delete(.company, id: id, api: api)
        deleted = true
    }

    // MARK: Realtime

    private func handle(_ update: RealtimeUpdate) {
        switch update {
        case .resync:
            Task { await refresh() }
        case let .envelope(envelope):
            switch envelope.type {
            case "company.updated":
                if let next = envelope.member("company", as: Company.self), next.id == id { company = next }
            case "company.deleted":
                if envelope.string("company_id") == id { deleted = true }
            case "contact.created", "contact.updated":
                guard let contact = envelope.member("contact", as: Contact.self) else { return }
                let here = people.contains { $0.id == contact.id }
                if contact.company?.id == id || here { schedulePeople() }
            case "contact.deleted":
                if let gone = envelope.string("contact_id"), people.contains(where: { $0.id == gone }) { schedulePeople() }
            case "ticket.created", "ticket.updated":
                guard let ticket = envelope.member("ticket", as: Ticket.self) else { return }
                if let index = tickets.firstIndex(where: { $0.id == ticket.id }) {
                    tickets[index] = ticket
                } else if let contact = ticket.contact?.id, people.contains(where: { $0.id == contact }) {
                    tickets.insert(ticket, at: 0)
                }
                tickets.sort { $0.lastMessageAt > $1.lastMessageAt }
            default:
                break
            }
        }
    }
}
