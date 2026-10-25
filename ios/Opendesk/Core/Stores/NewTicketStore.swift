import Foundation
import Observation

/// An address the team writes from (`GET /v1/mailboxes`, the part a new ticket needs).
struct InboxMailbox: Decodable, Hashable, Sendable, Identifiable {
    var id: String
    var name: String
    var email: String
    var teamID: String?
    var status: String
    var sending: String
    var isPrivate: Bool

    enum CodingKeys: String, CodingKey {
        case id, name, email, status, sending
        case teamID = "team_id"
        case isPrivate = "private"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = c.lenient(String.self, forKey: .name) ?? ""
        email = c.lenient(String.self, forKey: .email) ?? ""
        teamID = c.lenient(String.self, forKey: .teamID)
        status = c.lenient(String.self, forKey: .status) ?? "active"
        sending = c.lenient(String.self, forKey: .sending) ?? "ok"
        isPrivate = c.lenient(Bool.self, forKey: .isPrivate) ?? false
    }

    /// "Support · support@margin.app", as the web's `addressLabel`.
    var label: String { name.isEmpty || name == email ? email : "\(name) · \(email)" }
    var canSend: Bool { sending != "system_off" }
}

/// Who a new ticket goes to: a contact found by search, or a new address.
enum NewTicketRecipient: Hashable, Sendable {
    case contact(Contact)
    case email(String)

    var name: String {
        switch self {
        case let .contact(contact): contact.name
        case .email: ""
        }
    }

    var email: String {
        switch self {
        case let .contact(contact): contact.email ?? ""
        case let .email(address): address
        }
    }

    var display: String { name.isEmpty ? email : name }
}

/// `POST /v1/tickets` body.
private struct NewTicketBody: Encodable, Sendable {
    struct NewContact: Encodable, Sendable {
        var email: String
        var name: String
    }

    var subject: String
    var contactID: String?
    var contact: NewContact?
    var channel: String
    var mailboxID: String?
    var bodyHTML: String
    var kind: String
    var priority: String
    var assigneeID: String?
    var teamID: String?
    var status: String

    enum CodingKeys: String, CodingKey {
        case subject, contact, channel, kind, priority, status
        case contactID = "contact_id"
        case mailboxID = "mailbox_id"
        case bodyHTML = "body_html"
        case assigneeID = "assignee_id"
        case teamID = "team_id"
    }
}

/// A new ticket: email the customer, or log it internally (the web's `NewTicketDialog`).
@MainActor
@Observable
final class NewTicketStore {
    enum Kind: String, CaseIterable, Identifiable, Sendable {
        case reply, note
        var id: String { rawValue }
        var title: String { self == .reply ? "Email the customer" : "Log internally" }
    }

    var kind: Kind = .reply
    var recipient: NewTicketRecipient?
    var subject = ""
    var body = ""
    var mailboxID: String?
    var priority: Priority = .normal
    /// Unset until someone picks one: the team follows the address's default team.
    var teamChoice: String??
    var assigneeID: String?
    private(set) var mailboxes: [InboxMailbox] = []
    private(set) var contactQuery = ""
    private(set) var contacts: [Contact] = []
    private(set) var searchingContacts = false
    private(set) var creating = false
    var error: String?

    @ObservationIgnored private var api: APIClient?
    @ObservationIgnored private var contactTask: Task<Void, Never>?

    func start(api: APIClient, me: String?) {
        self.api = api
        assigneeID = me
        Task { await loadMailboxes() }
    }

    private func loadMailboxes() async {
        guard let api, let page: Page<InboxMailbox> = try? await api.get("v1/mailboxes") else { return }
        mailboxes = page.data.filter { $0.status != "disabled" }
        if mailboxID == nil {
            mailboxID = (mailboxes.first { $0.status == "active" && $0.canSend } ?? mailboxes.first)?.id
        }
    }

    var mailbox: InboxMailbox? { mailboxes.first { $0.id == mailboxID } }

    /// The team the ticket gets: the one picked, else the address's own.
    var teamID: String? {
        if let teamChoice { return teamChoice }
        return mailbox?.teamID
    }

    /// An address with no way to send can't email the first message.
    var cannotSend: Bool { kind == .reply && mailbox.map { !$0.canSend } == true }

    var isDirty: Bool { recipient != nil || !subject.isEmpty || !body.isEmpty }

    static func isEmail(_ text: String) -> Bool {
        text.range(of: #"^[^\s@]+@[^\s@]+\.[^\s@]+$"#, options: .regularExpression) != nil
    }

    /// Looks contacts up as the To field is typed.
    func searchContacts(_ text: String) {
        contactQuery = text
        contactTask?.cancel()
        let trimmed = text.trimmingCharacters(in: .whitespaces)
        guard !trimmed.isEmpty else {
            contacts = []
            searchingContacts = false
            return
        }
        searchingContacts = true
        contactTask = Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(220))
            guard !Task.isCancelled, let self, let api = self.api else { return }
            let hits: SearchHits? = try? await api.get("v1/search", query: [URLQueryItem(name: "q", value: trimmed)])
            guard !Task.isCancelled, trimmed == self.contactQuery.trimmingCharacters(in: .whitespaces) else { return }
            self.contacts = hits?.contacts ?? []
            self.searchingContacts = false
        }
    }

    /// The first problem with the form, in the web's words.
    var problem: String? {
        if recipient == nil { return "Add a recipient." }
        if subject.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty { return "Add a subject." }
        if body.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty { return "Write a message." }
        if cannotSend { return "This address can't send email yet. Pick another, or ask an admin to connect a way to send." }
        return nil
    }

    func create() async -> Ticket? {
        error = nil
        if let problem {
            error = problem
            return nil
        }
        guard let api, let recipient else { return nil }
        creating = true
        defer { creating = false }
        var contactID: String?
        var contact: NewTicketBody.NewContact?
        switch recipient {
        case let .contact(found): contactID = found.id
        case let .email(address): contact = .init(email: address, name: "")
        }
        let body = NewTicketBody(
            subject: subject.trimmingCharacters(in: .whitespacesAndNewlines),
            contactID: contactID,
            contact: contact,
            // An email ticket needs its mailbox (later replies go out through it), also when it starts as a note.
            channel: mailboxID == nil ? "api" : "email",
            mailboxID: mailboxID,
            bodyHTML: HTMLText.html(fromPlain: self.body),
            kind: kind.rawValue,
            priority: priority.rawValue,
            assigneeID: assigneeID,
            teamID: teamID,
            status: kind == .reply ? "pending" : "open"
        )
        do {
            return try await api.post("v1/tickets", body: body)
        } catch {
            self.error = error.userMessage
            return nil
        }
    }
}
