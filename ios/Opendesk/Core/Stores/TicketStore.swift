import Foundation
import Observation

/// An attachment picked in the composer, uploaded before sending.
struct PendingAttachment: Identifiable, Hashable, Sendable {
    enum State: Hashable, Sendable {
        case uploading
        case ready(id: String)
        case failed(String)
    }

    var id = UUID()
    var filename: String
    var mimeType: String
    var size: Int
    var state: State = .uploading
    var preview: Data?
    var uploaded: Attachment?

    var attachmentID: String? {
        if case let .ready(id) = state { return id }
        return nil
    }

    var isImage: Bool { mimeType.hasPrefix("image/") }
}

/// One entry of the thread: a message or an activity line.
enum ThreadItem: Identifiable, Hashable {
    case message(Message)
    case activity(Activity)

    var id: String {
        switch self {
        case let .message(message): "m-" + message.id
        case let .activity(activity): "a-" + activity.id
        }
    }

    var date: Date {
        switch self {
        case let .message(message): message.createdAt
        case let .activity(activity): activity.createdAt
        }
    }
}

/// What the composer hands the store to send: a reply or note body and its options.
struct OutgoingMessage: Sendable, Hashable {
    var kind: MessageKind
    var html: String
    var text: String
    var cc: [String] = []
    var bcc: [String] = []
    var attachmentIDs: [String] = []
    var attachments: [Attachment] = []
    var status: TicketStatus?
    var mailboxID: String?
    /// The caller's own note this reply is written from (it becomes a marker once sent).
    var fromNoteID: String?
}

/// The open ticket: its record, thread, side conversations, shared draft, the
/// customer, Ollie's suggestions, and every action on it.
@MainActor
@Observable
final class TicketStore {
    let ticketID: String
    private(set) var ticket: Ticket?
    private(set) var messages: [Message] = []
    private(set) var activities: [Activity] = []
    private(set) var suggestedReply: SuggestedReply?
    private(set) var loading = true
    private(set) var error: String?
    private(set) var notFound = false
    /// This screen removed the ticket (deleted, or merged into `mergedInto`).
    private(set) var removed = false
    private(set) var mergedInto: Int?

    // Around the ticket
    private(set) var threads: [SideThread] = []
    private(set) var sharedDraft: SharedDraft?
    private(set) var links: [TicketLink] = []
    private(set) var contact: Contact?
    private(set) var contactTickets: [Ticket] = []
    private(set) var contactLoading = false
    private(set) var replyAddresses: [ReplyAddress] = []
    private(set) var variables: TicketVariables?

    // Per message
    private(set) var originals: [String: String] = [:]
    private(set) var translations: [String: Translation] = [:]
    private(set) var translating: Set<String> = []
    private(set) var retried: Set<String> = []

    // Ollie
    private(set) var summarizing = false
    private(set) var drafting = false
    private(set) var triaging = false
    private(set) var rewriting = false
    private(set) var asking = false
    private(set) var aiError: String?

    @ObservationIgnored private let api: APIClient
    @ObservationIgnored private var listener: UUID?
    @ObservationIgnored private weak var realtime: RealtimeService?
    @ObservationIgnored private var messagesReload: Task<Void, Never>?
    @ObservationIgnored private var outgoing: [String: OutgoingMessage] = [:]

    init(ticketID: String, api: APIClient, seed: Ticket? = nil) {
        self.ticketID = ticketID
        self.api = api
        ticket = seed
    }

    /// The ticket's uuid once loaded (a link may open it by number).
    var resolvedID: String { ticket?.id ?? ticketID }

    /// The conversation with the customer: side conversations keep their own messages.
    var thread: [ThreadItem] {
        let shownActivities = activities.filter { !Self.hiddenActivityTypes.contains($0.type) }
        let shownMessages = messages.filter { $0.threadID == nil }
        return (shownMessages.map(ThreadItem.message) + shownActivities.map(ThreadItem.activity)).sorted { $0.date < $1.date }
    }

    func threadMessages(_ id: String) -> [Message] {
        messages.filter { $0.threadID == id }
    }

    /// Bookkeeping lines the web keeps to its full timeline.
    static let hiddenActivityTypes: Set<String> = ["custom_updated", "field_changed", "created", "message_created", "note_edited", "note_sent_as_reply"]

    var summary: AISummary? {
        guard let raw = ticket?.custom["ai_summary"], !raw.isNull else { return nil }
        return try? raw.decode(AISummary.self)
    }

    var suggestions: [AISuggestion] { AISuggestion.all(in: ticket?.custom ?? [:]) }

    /// The address replies go out from.
    var replyAddress: ReplyAddress? {
        replyAddresses.first { $0.id == ticket?.mailboxID } ?? replyAddresses.first
    }

    /// The customer's other tickets, newest first.
    var otherTickets: [Ticket] {
        contactTickets.filter { $0.id != resolvedID }.sorted { $0.lastMessageAt > $1.lastMessageAt }
    }

    // MARK: Loading

    func start(realtime: RealtimeService) {
        self.realtime = realtime
        listener = realtime.subscribe { [weak self] update in self?.handle(update) }
        Task {
            await load()
            await loadAround()
        }
    }

    func stop() {
        realtime?.unsubscribe(listener)
        listener = nil
    }

    func load() async {
        loading = ticket == nil
        do {
            async let ticket: Ticket = api.get("v1/tickets/\(ticketID)")
            async let messages: Page<Message> = api.get("v1/tickets/\(ticketID)/messages", query: [URLQueryItem(name: "limit", value: "100")])
            async let activities: Page<Activity>? = try? api.get("v1/tickets/\(ticketID)/activities", query: [URLQueryItem(name: "limit", value: "100")])
            async let suggestion: SuggestedReplyEnvelope? = try? api.get("v1/tickets/\(ticketID)/ai/suggested-reply")
            self.ticket = try await ticket
            var all = try await messages
            // Long threads: follow the cursor so the latest messages are always there.
            var cursor = all.nextCursor
            var guardCount = 0
            while let next = cursor, guardCount < 10 {
                let page: Page<Message> = try await api.get("v1/tickets/\(ticketID)/messages", query: [URLQueryItem(name: "limit", value: "100"), URLQueryItem(name: "cursor", value: next)])
                all.data += page.data
                cursor = page.nextCursor
                guardCount += 1
            }
            let local = self.messages.filter(\.isLocal)
            self.messages = (all.data.filter { !$0.isHidden } + local).sorted { $0.createdAt < $1.createdAt }
            self.activities = (await activities)?.data ?? []
            self.suggestedReply = (await suggestion)?.suggestedReply.flatMap { $0.isPending ? $0 : nil }
            error = nil
            notFound = false
        } catch let error as APIError where error.status == 404 {
            notFound = true
        } catch {
            if !error.isCancellation { self.error = error.userMessage }
        }
        loading = false
    }

    /// What sits around the conversation: side conversations, the shared draft, links,
    /// the customer and their tickets, and the addresses replies can go from.
    func loadAround() async {
        guard ticket != nil else { return }
        let id = resolvedID
        async let threads: Page<SideThread>? = try? api.get("v1/tickets/\(id)/threads")
        async let draft: SharedDraftEnvelope? = try? api.get("v1/tickets/\(id)/draft")
        async let links: Page<TicketLink>? = try? api.get("v1/tickets/\(id)/links")
        async let addresses: Page<ReplyAddress>? = try? api.get("v1/mailboxes")
        if let page = await threads { self.threads = page.data.sorted { $0.createdAt < $1.createdAt } }
        if let envelope = await draft { sharedDraft = envelope.draft }
        if let page = await links { self.links = page.data }
        if let page = await addresses { replyAddresses = page.data }
        await loadContact()
    }

    func loadContact() async {
        guard let id = ticket?.contact?.id else { return }
        contactLoading = contact == nil
        defer { contactLoading = false }
        async let contact: Contact? = try? api.get("v1/contacts/\(id)")
        async let tickets: Page<Ticket>? = try? api.get("v1/contacts/\(id)/tickets", query: [URLQueryItem(name: "limit", value: "20")])
        if let value = await contact { self.contact = value }
        if let page = await tickets { contactTickets = page.data }
    }

    /// The ticket's template values for `{{ }}` suggestions, loaded once.
    func loadVariables() async {
        guard variables == nil else { return }
        variables = try? await api.get("v1/tickets/\(resolvedID)/variables")
    }

    private func reloadMessagesSoon() {
        messagesReload?.cancel()
        messagesReload = Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(300))
            guard let self, !Task.isCancelled else { return }
            guard let page: Page<Message> = try? await self.api.get("v1/tickets/\(self.ticketID)/messages", query: [URLQueryItem(name: "limit", value: "100")]) else { return }
            let fresh = Dictionary(page.data.map { ($0.id, $0) }, uniquingKeysWith: { first, _ in first })
            var merged = self.messages.map { fresh[$0.id] ?? $0 }
            let known = Set(merged.map(\.id))
            merged += page.data.filter { !known.contains($0.id) }
            self.messages = merged.filter { !$0.isHidden }.sorted { $0.createdAt < $1.createdAt }
        }
    }

    // MARK: Realtime

    func handle(_ update: RealtimeUpdate) {
        switch update {
        case .resync:
            Task {
                await load()
                await loadAround()
            }
        case let .envelope(envelope):
            apply(envelope)
        }
    }

    private func apply(_ envelope: Envelope) {
        let current = resolvedID
        let mine = { (id: String?) in id == current }
        switch envelope.type {
        case "ticket.updated", "ticket.created":
            guard let fresh = envelope.member("ticket", as: Ticket.self), fresh.id == resolvedID else { return }
            let contactChanged = fresh.contact?.id != ticket?.contact?.id
            ticket = fresh
            if contactChanged {
                contact = nil
                contactTickets = []
                Task { await loadContact() }
            }
        case "ticket.deleted":
            if mine(envelope.string("ticket_id")) { removed = true }
        case "message.created", "message.updated":
            guard let message = envelope.member("message", as: Message.self),
                  mine(envelope.string("ticket_id") ?? message.ticketID)
            else { return }
            merge(message)
            if envelope.type == "message.created", message.kind == .reply, message.threadID == nil, let pending = suggestedReply, pending.messageID != message.id {
                // A newer message means Ollie's suggestion no longer answers the latest one.
                suggestedReply = nil
            }
            if message.ai { realtime?.clearLive(ticketID: current, messageID: message.id) }
        case "activity.created":
            guard let activity = envelope.member("activity", as: Activity.self),
                  mine(envelope.string("ticket_id") ?? activity.ticketID),
                  !activities.contains(where: { $0.id == activity.id })
            else { return }
            activities.append(activity)
        case "ai_suggested_reply.created", "ai_suggested_reply.updated":
            guard let suggestion = envelope.member("suggested_reply", as: SuggestedReply.self),
                  mine(envelope.string("ticket_id") ?? suggestion.ticketID)
            else { return }
            if suggestion.isPending {
                suggestedReply = suggestion
            } else if suggestedReply?.id == suggestion.id {
                suggestedReply = nil
            }
        case "thread.created", "thread.updated":
            guard let thread = envelope.member("thread", as: SideThread.self), mine(envelope.string("ticket_id") ?? thread.ticketID) else { return }
            upsert(thread)
        case "ticket_draft.updated":
            guard mine(envelope.string("ticket_id")) else { return }
            sharedDraft = envelope.member("draft", as: SharedDraft.self)
        case "ticket_link.created":
            guard mine(envelope.string("ticket_id")), let link = envelope.member("link", as: TicketLink.self), !links.contains(where: { $0.id == link.id }) else { return }
            links.append(link)
        case "ticket_link.deleted":
            guard mine(envelope.string("ticket_id")), let id = envelope.string("link_id") else { return }
            links.removeAll { $0.id == id }
        default:
            break
        }
    }

    /// Merge by id. Chat replies arrive customer-safe (no recipients, no attachments), so a
    /// stored copy keeps its members, and a new partial one triggers a reload of the thread.
    private func merge(_ message: Message) {
        // A deleted message (or a note sent as a reply) leaves the thread.
        if message.isHidden {
            messages.removeAll { $0.id == message.id }
            return
        }
        if let index = messages.firstIndex(where: { $0.id == message.id }) {
            messages[index] = messages[index].merged(with: message)
            return
        }
        // Our own send can arrive before its POST answers: it takes the optimistic copy's place.
        if message.author.type == "user",
           let temp = messages.firstIndex(where: { $0.isLocal && $0.kind == message.kind && $0.threadID == message.threadID && $0.author.user?.id == message.author.user?.id && $0.deliveryStatus != "failed" }) {
            outgoing[messages[temp].id] = nil
            messages[temp] = message
        } else {
            messages.append(message)
            messages.sort { $0.createdAt < $1.createdAt }
        }
        if message.isPartial { reloadMessagesSoon() }
    }

    private func upsert(_ thread: SideThread) {
        if let index = threads.firstIndex(where: { $0.id == thread.id }) {
            threads[index] = thread
        } else {
            threads.append(thread)
        }
    }

    // MARK: Ticket actions

    func patch(_ change: TicketPatch, members: [Member], me: UserRef?) async throws {
        guard let current = ticket else { return }
        ticket = change.applied(to: current, me: me, members: members)
        do {
            ticket = try await api.patch("v1/tickets/\(resolvedID)", body: change)
        } catch {
            ticket = current
            throw error
        }
    }

    /// Moves this ticket's conversation into another one; this ticket closes and points to it.
    func merge(into target: Ticket) async throws {
        struct Body: Encodable, Sendable { let into_id: String }
        let _: JSONValue? = try await api.post("v1/tickets/\(resolvedID)/merge", body: Body(into_id: target.id))
        mergedInto = target.number
        removed = true
    }

    func delete() async throws {
        try await api.delete("v1/tickets/\(resolvedID)")
        removed = true
    }

    // MARK: Sending

    func upload(data: Data, filename: String, mimeType: String) async throws -> Attachment {
        try await api.upload("v1/attachments", data: data, filename: filename, mimeType: mimeType)
    }

    /// Sends a reply or note; shows it at once and swaps in the server's copy. A send that
    /// fails stays in the thread as not delivered, with Retry.
    @discardableResult
    func send(_ outgoing: OutgoingMessage, author: UserRef) async throws -> Message? {
        var local = Message(localID: "tmp-" + UUID().uuidString, ticketID: resolvedID, kind: outgoing.kind, html: outgoing.html, text: outgoing.text, author: author)
        local.cc = outgoing.cc
        local.attachments = outgoing.attachments
        if outgoing.kind == .note { local.deliveryStatus = "none" }
        if outgoing.kind == .reply, ticket?.channel == .email, let email = ticket?.contact?.email { local.to = [email] }
        messages.append(local)
        self.outgoing[local.id] = outgoing
        if let fromNote = outgoing.fromNoteID { messages.removeAll { $0.id == fromNote } }
        return try await deliver(outgoing, as: local.id)
    }

    private func deliver(_ outgoing: OutgoingMessage, as localID: String) async throws -> Message? {
        struct Body: Encodable, Sendable {
            let kind: MessageKind
            let body_html: String
            let cc: [String]?
            let bcc: [String]?
            let attachment_ids: [String]?
            let status: TicketStatus?
            let mailbox_id: String?
            let from_note_id: String?
        }
        let body = Body(
            kind: outgoing.kind, body_html: outgoing.html,
            cc: outgoing.cc.isEmpty ? nil : outgoing.cc, bcc: outgoing.bcc.isEmpty ? nil : outgoing.bcc,
            attachment_ids: outgoing.attachmentIDs.isEmpty ? nil : outgoing.attachmentIDs,
            status: outgoing.status, mailbox_id: outgoing.mailboxID, from_note_id: outgoing.fromNoteID
        )
        do {
            let sent: Message = try await api.post("v1/tickets/\(resolvedID)/messages", body: body, idempotencyKey: localID)
            self.outgoing[localID] = nil
            if let index = messages.firstIndex(where: { $0.id == localID }) {
                if messages.contains(where: { $0.id == sent.id }) {
                    messages.remove(at: index)
                } else {
                    messages[index] = sent
                }
            } else if !messages.contains(where: { $0.id == sent.id }) {
                messages.append(sent)
            }
            if let status = outgoing.status, var current = ticket {
                current.status = status
                ticket = current
            }
            if outgoing.kind == .reply { sharedDraft = nil }
            return sent
        } catch {
            if let index = messages.firstIndex(where: { $0.id == localID }) {
                messages[index].deliveryStatus = "failed"
                messages[index].deliveryError = error.userMessage
            }
            throw error
        }
    }

    /// Sends a failed message again: a local one in its place, a bounced one as a new message.
    func retry(_ message: Message, author: UserRef) async throws {
        if let threadID = message.threadID {
            retried.insert(message.id)
            try await replyInThread(threadID, html: message.bodyHTML, author: author)
            return
        }
        if message.isLocal, let outgoing = outgoing[message.id] {
            if let index = messages.firstIndex(where: { $0.id == message.id }) {
                messages[index].deliveryStatus = "pending"
                messages[index].deliveryError = ""
            }
            _ = try await deliver(outgoing, as: message.id)
            return
        }
        retried.insert(message.id)
        let again = OutgoingMessage(kind: message.kind, html: message.bodyHTML, text: message.bodyText, cc: message.cc)
        do {
            try await send(again, author: author)
        } catch {
            retried.remove(message.id)
            throw error
        }
    }

    /// Drops a local message that failed to send.
    func discardFailed(_ message: Message) {
        guard message.isLocal else { return }
        outgoing[message.id] = nil
        messages.removeAll { $0.id == message.id }
    }

    // MARK: Message actions

    func editNote(_ message: Message, html: String) async throws {
        struct Body: Encodable, Sendable { let body_html: String }
        guard let index = messages.firstIndex(where: { $0.id == message.id }) else { return }
        let before = messages[index]
        messages[index].bodyHTML = html
        messages[index].bodyText = HTMLText.plain(from: html)
        messages[index].editedAt = AppClock.now
        do {
            let saved: Message = try await api.patch("v1/tickets/\(resolvedID)/messages/\(message.id)", body: Body(body_html: html))
            merge(saved)
        } catch {
            if let index = messages.firstIndex(where: { $0.id == message.id }) { messages[index] = before }
            throw error
        }
    }

    func deleteMessage(_ message: Message) async throws {
        let before = messages
        messages.removeAll { $0.id == message.id }
        do {
            try await api.delete("v1/tickets/\(resolvedID)/messages/\(message.id)")
        } catch {
            messages = before
            throw error
        }
    }

    /// Takes a sent chat reply back from the visitor: it stays here as an internal note.
    func retract(_ message: Message) async throws {
        guard let index = messages.firstIndex(where: { $0.id == message.id }) else { return }
        let before = messages[index]
        messages[index].kind = .note
        do {
            let saved: Message = try await api.post("v1/tickets/\(resolvedID)/messages/\(message.id)/retract")
            merge(saved)
        } catch {
            if let index = messages.firstIndex(where: { $0.id == message.id }) { messages[index] = before }
            throw error
        }
    }

    /// The full original of an email, with the quoted history the thread trims.
    func loadOriginal(_ message: Message) async throws {
        guard originals[message.id] == nil else { return }
        let result: HTMLResult = try await api.get("v1/messages/\(message.id)/original")
        originals[message.id] = result.html
    }

    func hideOriginal(_ message: Message) {
        originals[message.id] = nil
    }

    /// Ollie translates a customer's message into the reader's language.
    func translate(_ message: Message) async throws {
        struct Body: Encodable, Sendable { let html: String; let target: String }
        guard translations[message.id] == nil, !translating.contains(message.id) else { return }
        translating.insert(message.id)
        defer { translating.remove(message.id) }
        let html = message.bodyHTML.isEmpty ? HTMLText.html(fromPlain: message.bodyText) : message.bodyHTML
        let result: Translation = try await api.post("v1/ai/translate", body: Body(html: html, target: Self.readerLanguage))
        translations[message.id] = result
    }

    func showOriginalText(_ message: Message) {
        translations[message.id] = nil
    }

    nonisolated static var readerLanguage: String {
        Locale.preferredLanguages.first.map { Locale(identifier: $0).language.languageCode?.identifier ?? "en" } ?? "en"
    }

    // MARK: Side conversations

    @discardableResult
    func startThread(to: [String], cc: [String], subject: String, html: String, includeHistory: Bool, attachmentIDs: [String]) async throws -> SideThread {
        struct Body: Encodable, Sendable {
            let to: [String]
            let cc: [String]?
            let subject: String
            let body_html: String
            let include_history: Bool
            let attachment_ids: [String]?
        }
        let body = Body(to: to, cc: cc.isEmpty ? nil : cc, subject: subject, body_html: html, include_history: includeHistory, attachment_ids: attachmentIDs.isEmpty ? nil : attachmentIDs)
        let created: SideThreadCreated = try await api.post("v1/tickets/\(resolvedID)/threads", body: body)
        upsert(created.thread)
        merge(created.message)
        return created.thread
    }

    func replyInThread(_ threadID: String, html: String, author: UserRef) async throws {
        struct Body: Encodable, Sendable { let body_html: String }
        var local = Message(localID: "tmp-" + UUID().uuidString, ticketID: resolvedID, kind: .note, html: html, text: HTMLText.plain(from: html), author: author)
        local.threadID = threadID
        messages.append(local)
        do {
            let sent: Message = try await api.post("v1/tickets/\(resolvedID)/threads/\(threadID)/messages", body: Body(body_html: html), idempotencyKey: local.id)
            if let index = messages.firstIndex(where: { $0.id == local.id }) {
                if messages.contains(where: { $0.id == sent.id }) { messages.remove(at: index) } else { messages[index] = sent }
            }
            if let index = threads.firstIndex(where: { $0.id == threadID }) {
                threads[index].status = .open
                threads[index].messageCount += 1
                threads[index].lastMessageAt = sent.createdAt
            }
        } catch {
            messages.removeAll { $0.id == local.id }
            throw error
        }
    }

    func setThread(_ thread: SideThread, status: SideThread.Status) async throws {
        struct Body: Encodable, Sendable { let status: SideThread.Status }
        guard let index = threads.firstIndex(where: { $0.id == thread.id }) else { return }
        let before = threads[index]
        threads[index].status = status
        do {
            let saved: SideThread = try await api.patch("v1/tickets/\(resolvedID)/threads/\(thread.id)", body: Body(status: status))
            upsert(saved)
        } catch {
            upsert(before)
            throw error
        }
    }

    // MARK: Shared draft

    /// Stages the composer's text as the ticket's shared draft, for whoever opens it next.
    func stageDraft(html: String) async throws {
        struct Body: Encodable, Sendable { let body_html: String }
        let request = APIRequest(method: "PUT", path: "v1/tickets/\(resolvedID)/draft", body: try APICoding.encoder.encode(Body(body_html: html)))
        let saved: SharedDraftEnvelope = try await api.send(request)
        if let draft = saved.draft { sharedDraft = draft }
    }

    func discardDraft() async throws {
        let before = sharedDraft
        sharedDraft = nil
        do {
            try await api.delete("v1/tickets/\(resolvedID)/draft")
        } catch {
            sharedDraft = before
            throw error
        }
    }

    // MARK: Templates

    /// A saved reply's body with this ticket's details filled in.
    func render(_ html: String) async -> String {
        struct Body: Encodable, Sendable { let body_html: String }
        guard html.contains("{{") else { return html }
        let result: HTMLResult? = try? await api.post("v1/tickets/\(resolvedID)/render", body: Body(body_html: html))
        return result?.html ?? html
    }

    // MARK: Lookups

    /// Tickets to merge this one into: matches for `query`, without this ticket.
    func searchTickets(_ query: String) async throws -> [Ticket] {
        let text = query.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return [] }
        let results: SearchResults = try await api.get("v1/search", query: [URLQueryItem(name: "q", value: text), URLQueryItem(name: "limit", value: "20")])
        return results.tickets.filter { $0.id != resolvedID && !$0.spam }
    }

    /// An attachment saved to a temporary file for preview. Its link is signed, so no token rides along.
    func file(for attachment: Attachment, base: URL) async throws -> URL {
        guard let source = URL(string: attachment.url, relativeTo: base)?.absoluteURL else { throw APIError.decoding }
        let (data, response) = try await URLSession.shared.data(from: source)
        if let http = response as? HTTPURLResponse, !(200 ..< 300).contains(http.statusCode) {
            throw APIError(status: http.statusCode, code: nil, title: nil, detail: "That file couldn't be opened.", fieldErrors: [])
        }
        let folder = FileManager.default.temporaryDirectory.appending(path: "attachments/\(attachment.id)", directoryHint: .isDirectory)
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        let name = attachment.filename.replacingOccurrences(of: "/", with: "-")
        let target = folder.appending(path: name.isEmpty ? "file" : name)
        try data.write(to: target, options: .atomic)
        return target
    }

    // MARK: Ollie

    func summarize() async {
        summarizing = true
        aiError = nil
        defer { summarizing = false }
        do {
            let summary: AISummary = try await api.post("v1/tickets/\(resolvedID)/ai/summary")
            if var current = ticket, let value = try? JSONValue.from(summary) {
                current.custom["ai_summary"] = value
                ticket = current
            }
        } catch {
            aiError = error.userMessage
        }
    }

    func draft(instructions: String?) async -> String? {
        struct Body: Encodable, Sendable { let instructions: String? }
        drafting = true
        aiError = nil
        defer { drafting = false }
        do {
            let draft: AIDraft = try await api.post("v1/tickets/\(resolvedID)/ai/draft", body: Body(instructions: instructions?.isEmpty == true ? nil : instructions))
            return draft.bodyText.isEmpty ? HTMLText.plain(from: draft.bodyHTML) : draft.bodyText
        } catch {
            aiError = error.userMessage
            return nil
        }
    }

    /// Ollie rewrites the composer's text in another tone or length.
    func rewrite(_ text: String, mode: RewriteMode, instructions: String? = nil) async throws -> String {
        struct Body: Encodable, Sendable { let html: String; let mode: String; let instructions: String? }
        rewriting = true
        defer { rewriting = false }
        let result: HTMLResult = try await api.post("v1/ai/rewrite", body: Body(html: HTMLText.html(fromPlain: text), mode: mode.rawValue, instructions: instructions))
        return HTMLText.plain(from: result.html)
    }

    /// Ollie translates the composer's text into `target` (BCP 47).
    func translateDraft(_ text: String, to target: String) async throws -> String {
        struct Body: Encodable, Sendable { let html: String; let target: String }
        rewriting = true
        defer { rewriting = false }
        let result: Translation = try await api.post("v1/ai/translate", body: Body(html: HTMLText.html(fromPlain: text), target: target))
        if let html = result.html { return HTMLText.plain(from: html) }
        return result.text ?? text
    }

    /// Asks Ollie about this ticket (copilot): an answer, with the articles it drew on.
    func ask(_ question: String) async throws -> TicketAnswer {
        struct Body: Encodable, Sendable { let question: String }
        asking = true
        defer { asking = false }
        return try await api.post("v1/tickets/\(resolvedID)/ai/ask", body: Body(question: question))
    }

    func triage(apply: Bool) async {
        struct Body: Encodable, Sendable { let apply: Bool }
        triaging = true
        aiError = nil
        defer { triaging = false }
        do {
            let _: JSONValue = try await api.post("v1/tickets/\(resolvedID)/ai/triage", body: Body(apply: apply))
            if let fresh: Ticket = try? await api.get("v1/tickets/\(resolvedID)") { ticket = fresh }
        } catch {
            aiError = error.userMessage
        }
    }

    /// Accepting a suggestion is setting the field; the server then drops the suggestion.
    func accept(_ suggestion: AISuggestion, members: [Member], me: UserRef?) async throws {
        try await patch(Self.change(for: suggestion), members: members, me: me)
    }

    /// Every pending suggestion in one change.
    func acceptAll(members: [Member], me: UserRef?) async throws {
        var change = TicketPatch()
        for suggestion in suggestions {
            let one = Self.change(for: suggestion)
            change.priority = one.priority ?? change.priority
            change.severity = one.severity ?? change.severity
            change.teamID = one.teamID ?? change.teamID
            change.spam = one.spam ?? change.spam
            if let add = one.addTags { change.addTags = (change.addTags ?? []) + add }
        }
        try await patch(change, members: members, me: me)
    }

    private static func change(for suggestion: AISuggestion) -> TicketPatch {
        var change = TicketPatch()
        switch suggestion.field {
        case .priority:
            change.priority = suggestion.value.string.flatMap(Priority.init(rawValue:))
        case .severity:
            change.severity = suggestion.value.string.flatMap(Severity.init(rawValue:)).map { .set($0) }
        case .team:
            change.teamID = suggestion.value.string.map { .set($0) }
        case .labels:
            let names = suggestion.value.array?.compactMap(\.string) ?? suggestion.value.string.map { [$0] } ?? []
            change.addTags = names.isEmpty ? nil : names
        case .spam:
            change.spam = true
        }
        return change
    }

    func dismiss(_ suggestion: AISuggestion) async throws {
        if var current = ticket, var object = current.custom["ai_suggestions"]?.object {
            object[suggestion.field.rawValue] = nil
            current.custom["ai_suggestions"] = .object(object)
            ticket = current
        }
        try await api.delete("v1/tickets/\(resolvedID)/ai/suggestions/\(suggestion.field.rawValue)")
    }

    func sendSuggestedReply() async throws {
        guard let suggestion = suggestedReply else { return }
        suggestedReply = nil
        do {
            let sent: Message = try await api.post("v1/tickets/\(resolvedID)/ai/suggested-reply/\(suggestion.id)/send")
            merge(sent)
        } catch {
            suggestedReply = suggestion
            throw error
        }
    }

    func discardSuggestedReply() async throws {
        guard let suggestion = suggestedReply else { return }
        suggestedReply = nil
        do {
            try await api.delete("v1/tickets/\(resolvedID)/ai/suggested-reply/\(suggestion.id)")
        } catch {
            suggestedReply = suggestion
            throw error
        }
    }

    /// The suggestion's text for the composer; the teammate sends it as their own reply.
    func takeSuggestedReply() -> String? {
        guard let suggestion = suggestedReply else { return nil }
        suggestedReply = nil
        let api = api
        let path = "v1/tickets/\(resolvedID)/ai/suggested-reply/\(suggestion.id)"
        Task { try? await api.delete(path) }
        return HTMLText.plain(from: suggestion.bodyHTML)
    }
}
