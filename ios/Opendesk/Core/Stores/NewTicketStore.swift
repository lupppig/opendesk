import Foundation
import Observation
import UIKit

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
    /// Given only when set, as the web sends them.
    var severity: String?
    var tags: [String]?
    var fields: [String: JSONValue]?
    var attachmentIDs: [String]?

    enum CodingKeys: String, CodingKey {
        case subject, contact, channel, kind, priority, status, severity, tags, fields
        case contactID = "contact_id"
        case mailboxID = "mailbox_id"
        case bodyHTML = "body_html"
        case assigneeID = "assignee_id"
        case teamID = "team_id"
        case attachmentIDs = "attachment_ids"
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
    var severity: Severity?
    /// Label names, as `POST /v1/tickets` takes them.
    var tags: [String] = []
    /// Custom field values by key; only the ones set.
    var fields: [String: JSONValue] = [:]
    var attachments: [PendingAttachment] = []
    /// The contact's earlier tickets, newest first, while the ticket goes to a known contact.
    private(set) var pastTickets: [Ticket] = []
    private(set) var pastLoading = false
    private(set) var mailboxes: [InboxMailbox] = []
    private(set) var contactQuery = ""
    private(set) var contacts: [Contact] = []
    private(set) var searchingContacts = false
    private(set) var creating = false
    var error: String?

    @ObservationIgnored private var api: APIClient?
    @ObservationIgnored private var contactTask: Task<Void, Never>?
    @ObservationIgnored private var pastTask: Task<Void, Never>?

    func start(api: APIClient, me: String?) {
        self.api = api
        assigneeID = me
        Task { await loadMailboxes() }
    }

    /// The contact's earlier tickets for their card (`GET /v1/contacts/{id}/tickets`).
    func loadPast() {
        pastTask?.cancel()
        pastTickets = []
        guard case let .contact(contact) = recipient, let api else {
            pastLoading = false
            return
        }
        pastLoading = true
        pastTask = Task { [weak self] in
            let page: Page<Ticket>? = try? await api.get("v1/contacts/\(contact.id)/tickets", query: [URLQueryItem(name: "limit", value: "5")])
            guard !Task.isCancelled, let self else { return }
            self.pastTickets = page?.data ?? []
            self.pastLoading = false
        }
    }

    // MARK: Files

    func attach(data: Data?, filename: String, mimeType: String, preview: Data? = nil) {
        var pending = PendingAttachment(filename: filename, mimeType: mimeType, size: data?.count ?? 0)
        pending.preview = preview
        attachments.append(pending)
        guard let data else {
            mark(pending.id, .failed("Couldn't read the file"))
            return
        }
        let id = pending.id
        Task { await upload(id, data: data) }
    }

    /// A file whose data arrives later (a photo still loading from the library).
    func attachLater(filename: String, mimeType: String, load: @escaping @Sendable () async -> Data?) {
        let pending = PendingAttachment(filename: filename, mimeType: mimeType, size: 0)
        attachments.append(pending)
        let id = pending.id
        Task {
            guard let data = await load() else {
                mark(id, .failed("Couldn't read the photo"))
                return
            }
            if let index = attachments.firstIndex(where: { $0.id == id }) {
                attachments[index].size = data.count
                if attachments[index].isImage { attachments[index].preview = Self.thumbnail(data) }
            }
            await upload(id, data: data)
        }
    }

    private func upload(_ id: UUID, data: Data) async {
        guard let api, let file = attachments.first(where: { $0.id == id }) else { return }
        do {
            let uploaded: Attachment = try await api.upload("v1/attachments", data: data, filename: file.filename, mimeType: file.mimeType)
            guard let index = attachments.firstIndex(where: { $0.id == id }) else { return }
            attachments[index].state = .ready(id: uploaded.id)
            attachments[index].uploaded = uploaded
        } catch {
            mark(id, .failed(error.userMessage))
        }
    }

    private func mark(_ id: UUID, _ state: PendingAttachment.State) {
        guard let index = attachments.firstIndex(where: { $0.id == id }) else { return }
        attachments[index].state = state
    }

    /// A saved reply's text at the end of the message; its `{{ }}` variables are filled by
    /// the server when the ticket is created.
    func insert(_ reply: SavedReply) {
        let text = HTMLText.plain(from: reply.bodyHTML).trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return }
        body = body.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? text : body + (body.hasSuffix("\n") ? "\n" : "\n\n") + text
    }

    static func thumbnail(_ data: Data) -> Data? {
        guard let image = UIImage(data: data) else { return nil }
        let side: CGFloat = 96
        let scale = side / max(image.size.width, image.size.height, 1)
        let size = CGSize(width: image.size.width * min(scale, 1), height: image.size.height * min(scale, 1))
        return UIGraphicsImageRenderer(size: size).image { _ in image.draw(in: CGRect(origin: .zero, size: size)) }.jpegData(compressionQuality: 0.7)
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

    var isDirty: Bool { recipient != nil || !subject.isEmpty || !body.isEmpty || !tags.isEmpty || !fields.isEmpty || !attachments.isEmpty }

    var uploading: Bool { attachments.contains { $0.state == .uploading } }

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
        if uploading { return "Wait for the files to finish uploading." }
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
            status: kind == .reply ? "pending" : "open",
            severity: severity?.rawValue,
            tags: tags.isEmpty ? nil : tags,
            fields: fields.isEmpty ? nil : fields,
            attachmentIDs: { let ids = attachments.compactMap(\.attachmentID); return ids.isEmpty ? nil : ids }()
        )
        do {
            return try await api.post("v1/tickets", body: body)
        } catch {
            self.error = error.userMessage
            return nil
        }
    }
}
