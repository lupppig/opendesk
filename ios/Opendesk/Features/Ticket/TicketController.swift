import PhotosUI
import SwiftUI
import UniformTypeIdentifiers

/// Everything the ticket screen can open over itself.
enum TicketSheet: Identifiable {
    case details, status, priority, severity, assignee, team, labels, snooze, merge
    case savedReplies, ask, sides, newSide, thread(String), guidedDraft
    case field(CustomField), required([CustomField])
    case editNote(Message)

    var id: String {
        switch self {
        case .details: "details"
        case .status: "status"
        case .priority: "priority"
        case .severity: "severity"
        case .assignee: "assignee"
        case .team: "team"
        case .labels: "labels"
        case .snooze: "snooze"
        case .merge: "merge"
        case .savedReplies: "saved-replies"
        case .ask: "ask"
        case .sides: "sides"
        case .newSide: "new-side"
        case let .thread(id): "thread-" + id
        case .guidedDraft: "guided-draft"
        case let .field(field): "field-" + field.key
        case let .required(fields): "required-" + fields.map(\.key).joined(separator: ",")
        case let .editNote(message): "edit-" + message.id
        }
    }
}

/// A reply that names teammates, waiting for "send anyway" or "turn into a note".
struct MentionCheck: Identifiable {
    let id = UUID()
    var names: [String]
    var status: TicketStatus?
}

/// A scroll the screen should make (to the newest message after sending, or to the top).
struct ScrollRequest: Equatable {
    enum Target: Equatable { case top, bottom }
    var target: Target
    var id = UUID()
}

/// The ticket screen's state and actions, shared by the screen and its sheets. Network
/// work goes through `TicketStore`; this decides what to show and where to go next.
@MainActor
@Observable
final class TicketController {
    let env: AppEnvironment
    let store: TicketStore
    let composer: TicketComposerStore

    var sheet: TicketSheet?
    /// A picker opened from the details sheet, over it.
    var detailSheet: TicketSheet?
    var confirmDelete = false
    var mentionCheck: MentionCheck?
    var pendingDelete: Message?
    var previewURL: URL?
    var opening: String?
    var remoteImages: Set<String> = []
    var editingSubject = false
    var ollieExpanded = true
    var retrying: Set<String> = []
    var sendingSuggestion = false
    var scroll: ScrollRequest?
    var composerFocus = false

    @ObservationIgnored var onClose: () -> Void = {}

    init(env: AppEnvironment, store: TicketStore, composer: TicketComposerStore) {
        self.env = env
        self.store = store
        self.composer = composer
    }

    var members: [Member] { env.directory.members }
    var me: UserRef? { env.session.userRef }
    var canWrite: Bool { env.session.canWrite }
    var ticket: Ticket? { store.ticket }

    // MARK: Presenting

    func open(_ sheet: TicketSheet) {
        Haptics.tap()
        if self.sheet?.id == TicketSheet.details.id {
            detailSheet = sheet
        } else {
            self.sheet = sheet
        }
    }

    /// Swaps the sheet on top for another (from a list to one of its items).
    func replace(with sheet: TicketSheet) {
        if detailSheet != nil { detailSheet = sheet } else { self.sheet = sheet }
    }

    // MARK: Properties

    /// Applies a change to the ticket; `undo` puts the previous value back from a toast.
    func apply(_ change: TicketPatch, toast: String? = nil, undo: TicketPatch? = nil) {
        Task {
            do {
                try await store.patch(change, members: members, me: me)
                if let ticket = store.ticket { env.inbox.place(ticket) }
                if let toast {
                    Haptics.success()
                    if let undo {
                        env.toasts.show(Toast(message: toast, kind: .success, actionTitle: "Undo", action: { [self] in self.apply(undo) }), seconds: 5)
                    } else {
                        env.toasts.show(toast, kind: .success)
                    }
                }
            } catch {
                failed(error)
            }
        }
    }

    func setPriority(_ priority: Priority) {
        var change = TicketPatch()
        change.priority = priority
        apply(change)
    }

    func setSeverity(_ severity: Severity?) {
        var change = TicketPatch()
        change.severity = severity.map { .set($0) } ?? .clear
        apply(change)
    }

    func assign(_ id: String?) {
        var change = TicketPatch()
        change.assigneeID = id.map { .set($0) } ?? .clear
        apply(change)
    }

    func assignToMe() {
        guard let me else { return }
        assign(me.id)
        env.toasts.show("Assigned to you", kind: .success)
    }

    func setTeam(_ id: String?) {
        var change = TicketPatch()
        change.teamID = id.map { .set($0) } ?? .clear
        apply(change)
    }

    func setLabels(add: [String], remove: [String]) {
        guard !add.isEmpty || !remove.isEmpty else { return }
        var change = TicketPatch()
        change.addTags = add.isEmpty ? nil : add
        change.removeTags = remove.isEmpty ? nil : remove
        apply(change)
    }

    func rename(_ subject: String) {
        var change = TicketPatch()
        change.subject = subject
        apply(change)
    }

    func setField(_ key: String, _ value: JSONValue?) {
        var change = TicketPatch()
        change.fields = [key: value ?? .null]
        apply(change)
    }

    // MARK: Status

    /// Fields that must be filled before solving and aren't.
    func missingRequired(_ ticket: Ticket) -> [CustomField] {
        env.directory.fields(for: "ticket").filter { $0.requiredOnSolve && TicketFieldValue.isEmpty(ticket.fields[$0.key], type: $0.type) }
    }

    func solve() {
        guard let ticket else { return }
        let missing = missingRequired(ticket)
        guard missing.isEmpty else {
            Haptics.warning()
            sheet = .required(missing)
            return
        }
        setStatus(.solved, advance: true)
    }

    func reopen() { setStatus(.open, advance: false) }

    /// Sets the status with an Undo, and after solving moves on to the next ticket that needs an answer.
    func setStatus(_ status: TicketStatus, advance: Bool = false, fields: [String: JSONValue]? = nil) {
        guard let current = ticket, status != current.status || fields != nil else { return }
        let next = advance ? nextTicket() : nil
        var change = TicketPatch()
        change.status = status
        change.fields = fields
        let before = current.status
        let number = current.number
        Task {
            do {
                try await store.patch(change, members: members, me: me)
                guard let updated = store.ticket else { return }
                env.inbox.place(updated)
                Haptics.success()
                let message = status == .solved ? "Solved #\(number)" : status == .open ? "Reopened #\(number)" : "Marked #\(number) \(status.title.lowercased())"
                let inbox = env.inbox
                let members = members
                let me = me
                env.toasts.show(Toast(message: message, kind: .success, actionTitle: "Undo", action: {
                    var revert = TicketPatch()
                    revert.status = before
                    Task {
                        if let back = try? await inbox.patch(updated, revert, members: members, meRef: me) { inbox.place(back) }
                    }
                }), seconds: 5)
                if advance { go(to: next) }
            } catch {
                failed(error)
            }
        }
    }

    /// Fills the required fields and solves in one change.
    func solve(with values: [String: JSONValue?]) async -> Bool {
        var fields: [String: JSONValue] = [:]
        for (key, value) in values { fields[key] = value ?? .null }
        guard let current = ticket else { return false }
        let next = nextTicket()
        var change = TicketPatch()
        change.status = .solved
        change.fields = fields
        do {
            try await store.patch(change, members: members, me: me)
            if let updated = store.ticket { env.inbox.place(updated) }
            Haptics.success()
            env.toasts.show("Solved #\(current.number)", kind: .success)
            go(to: next)
            return true
        } catch {
            env.toasts.error(error)
            return false
        }
    }

    /// The next ticket in the list behind this one that still needs work.
    func nextTicket() -> Ticket? {
        let list = env.inbox.tickets
        let id = store.resolvedID
        let open = { (ticket: Ticket) in ticket.id != id && !ticket.status.isDone && !ticket.spam }
        if let index = list.firstIndex(where: { $0.id == id }) {
            return list[list.index(after: index)...].first(where: open) ?? list[..<index].last(where: open)
        }
        return list.first(where: open)
    }

    /// Replaces this ticket with `next` on the stack, or goes back.
    func go(to next: Ticket?) {
        guard let next else { return onClose() }
        let router = env.router
        if router.tab == .notifications, !router.notificationsPath.isEmpty {
            router.notificationsPath[router.notificationsPath.count - 1] = .ticket(next)
        } else if !router.inboxPath.isEmpty {
            router.inboxPath[router.inboxPath.count - 1] = .ticket(next)
        } else {
            onClose()
        }
    }

    // MARK: More

    func snooze(until date: Date) {
        guard let current = ticket else { return }
        let next = nextTicket()
        var change = TicketPatch()
        change.snoozedUntil = .set(date)
        var undo = TicketPatch()
        undo.snoozedUntil = .clear
        undo.status = current.status
        apply(change, toast: "Snoozed until \(Format.shortDate(date)), \(Format.clock(date))", undo: undo)
        go(to: next)
    }

    func unsnooze() {
        var change = TicketPatch()
        change.snoozedUntil = .clear
        change.status = .open
        apply(change, toast: "Snooze ended")
    }

    func setSpam(_ spam: Bool) {
        var change = TicketPatch()
        change.spam = spam
        var undo = TicketPatch()
        undo.spam = !spam
        if spam {
            let next = nextTicket()
            apply(change, toast: "Marked as spam", undo: undo)
            go(to: next)
        } else {
            apply(change, toast: "Moved out of spam", undo: undo)
        }
    }

    func copyLink() {
        let path = env.inbox.scope.webPath(ticketID: store.resolvedID)
        guard let url = env.session.instance.webURL(path: path) else { return }
        UIPasteboard.general.url = url
        Haptics.success()
        env.toasts.show("Link copied", kind: .success)
    }

    func deleteTicket() {
        guard let number = ticket?.number else { return }
        Task {
            do {
                try await store.delete()
                env.inbox.remove(store.resolvedID)
                Haptics.success()
                env.toasts.show("Deleted #\(number)", kind: .success)
                onClose()
            } catch {
                env.toasts.error(error)
            }
        }
    }

    func merge(into target: Ticket) async -> Bool {
        guard let number = ticket?.number else { return false }
        do {
            try await store.merge(into: target)
            env.inbox.remove(store.resolvedID)
            Haptics.success()
            env.toasts.show("Merged #\(number) into #\(target.number)", kind: .success)
            go(to: target)
            return true
        } catch {
            env.toasts.error(error)
            return false
        }
    }

    func openContact() {
        guard let id = ticket?.contact?.id else { return }
        sheet = nil
        env.router.push(.contact(id))
    }

    func openTicket(_ other: Ticket) {
        sheet = nil
        env.router.push(.ticket(other))
    }

    /// A change the server refused: a field needed before solving opens that field, anything else is a toast.
    func failed(_ error: Error) {
        if let api = error as? APIError, api.code == "field_required" {
            let keys = api.fieldErrors.compactMap { $0.location?.split(separator: ".").last.map(String.init) }
            let all = env.directory.fields(for: "ticket")
            let fields = keys.isEmpty ? all.filter(\.requiredOnSolve) : all.filter { keys.contains($0.key) }
            if !fields.isEmpty {
                Haptics.warning()
                sheet = .required(fields)
                return
            }
        }
        env.toasts.error(error)
    }

    // MARK: Messages

    func handle(_ action: MessageAction, on message: Message) {
        switch action {
        case .copy:
            UIPasteboard.general.string = message.bodyText.isEmpty ? HTMLText.plain(from: message.bodyHTML) : message.bodyText
            Haptics.success()
            env.toasts.show("Copied", kind: .success)
        case .translate:
            Task {
                do { try await store.translate(message) } catch { env.toasts.error(error) }
            }
        case .showOriginalText:
            store.showOriginalText(message)
        case .loadOriginal:
            Task {
                do { try await store.loadOriginal(message) } catch { env.toasts.error(error) }
            }
        case .hideOriginal:
            store.hideOriginal(message)
        case .editNote:
            sheet = .editNote(message)
        case .sendAsReply:
            composer.startReply(fromNote: message)
            composerFocus = true
        case .retract:
            Task {
                do {
                    try await store.retract(message)
                    env.toasts.show("Turned into an internal note", kind: .success)
                } catch { env.toasts.error(error) }
            }
        case .delete:
            pendingDelete = message
        case .retry:
            guard let me else { return }
            retrying.insert(message.id)
            Task {
                defer { retrying.remove(message.id) }
                do { try await store.retry(message, author: me) } catch { env.toasts.error(error) }
            }
        case .discard:
            store.discardFailed(message)
        case let .open(attachment):
            preview(attachment)
        }
    }

    func deleteMessage(_ message: Message) {
        Task {
            do {
                try await store.deleteMessage(message)
                Haptics.success()
                env.toasts.show(message.isNote ? "Note deleted" : "Message deleted", kind: .success)
            } catch { env.toasts.error(error) }
        }
    }

    func saveNote(_ message: Message, text: String) async -> Bool {
        do {
            try await store.editNote(message, html: HTMLText.html(fromPlain: text))
            return true
        } catch {
            env.toasts.error(error)
            return false
        }
    }

    func preview(_ attachment: Attachment) {
        guard opening == nil else { return }
        opening = attachment.id
        Task {
            defer { opening = nil }
            do {
                previewURL = try await store.file(for: attachment, base: env.session.instance.apiURL)
            } catch {
                env.toasts.error(error)
            }
        }
    }

    // MARK: Sending

    /// Sends what the composer holds, after the checks: a reply that names a teammate asks first,
    /// and solving with required fields empty sends the reply and then asks for them.
    func send(status: TicketStatus?, checked: Bool = false) {
        guard composer.canSend, let me, let ticket else { return }
        if composer.mode == .reply, !checked {
            let named = composer.mentioned(in: members.filter { $0.id != me.id })
            if !named.isEmpty {
                Haptics.warning()
                mentionCheck = MentionCheck(names: named.map(\.user.name), status: status)
                return
            }
        }
        var status = status
        var askForFields: [CustomField] = []
        if status == .solved {
            askForFields = missingRequired(ticket)
            if !askForFields.isEmpty { status = nil }
        }
        let html = composer.html(members: members)
        let text = composer.trimmed
        let ready = composer.attachments.compactMap(\.uploaded)
        let ids = composer.readyAttachmentIDs
        scroll = ScrollRequest(target: .bottom)

        switch composer.mode {
        case .note:
            let outgoing = OutgoingMessage(kind: .note, html: html, text: text, attachmentIDs: ids, attachments: ready, status: status)
            composer.clear()
            Haptics.success()
            Task { await deliver(outgoing, author: me) }
        case .reply:
            let channel = ticket.channel
            let mailbox = ticket.mailboxID
            let cc = TicketComposerStore.addresses(composer.cc)
            let bcc = TicketComposerStore.addresses(composer.bcc)
            let fromNote = composer.fromNoteID
            Task {
                let rendered = await store.render(html)
                var outgoing = OutgoingMessage(kind: .reply, html: rendered, text: HTMLText.plain(from: rendered), cc: cc, bcc: bcc, attachmentIDs: ids, attachments: ready, status: status, mailboxID: mailbox)
                outgoing.fromNoteID = fromNote
                Haptics.success()
                if channel == .email {
                    composer.hold(outgoing, seconds: 5) { [self] held in await self.deliver(held, author: me) }
                } else {
                    composer.clear()
                    await deliver(outgoing, author: me)
                }
            }
        case .side:
            sendSide(html: html, ids: ids, author: me)
        }
        if !askForFields.isEmpty { sheet = .required(askForFields) }
    }

    private func sendSide(html: String, ids: [String], author: UserRef) {
        if let threadID = composer.sideThreadID {
            composer.clear()
            Task {
                do { try await store.replyInThread(threadID, html: html, author: author) } catch { env.toasts.error(error) }
            }
            return
        }
        let to = TicketComposerStore.addresses(composer.sideTo)
        let subject = composer.sideSubject.trimmingCharacters(in: .whitespaces).isEmpty ? (ticket?.subject ?? "") : composer.sideSubject
        let history = composer.includeHistory
        let draft = composer.draft
        composer.clear()
        Task {
            do {
                let thread = try await store.startThread(to: to, cc: [], subject: subject, html: html, includeHistory: history, attachmentIDs: ids)
                Haptics.success()
                composer.sideThreadID = thread.id
                env.toasts.show(Toast(message: "Side conversation started", kind: .success, actionTitle: "Open", action: { [self] in self.sheet = .thread(thread.id) }))
            } catch {
                composer.restore(draft)
                env.toasts.error(error)
            }
        }
    }

    func deliver(_ outgoing: OutgoingMessage, author: UserRef) async {
        do {
            try await store.send(outgoing, author: author)
            if let ticket = store.ticket { env.inbox.place(ticket) }
        } catch {
            env.toasts.error(error)
        }
    }

    func sendHeldNow() {
        guard let me else { return }
        composer.sendHeldNow { [weak self] held in await self?.deliver(held, author: me) }
    }

    func undoSend() {
        composer.undo()
        composerFocus = true
    }

    func turnIntoNote() {
        composer.mode = .note
        composerFocus = true
    }

    func useSharedDraft(_ draft: SharedDraft) {
        composer.mode = .reply
        composer.text = draft.text
        composerFocus = true
    }

    func discardSharedDraft() {
        Task {
            do { try await store.discardDraft() } catch { env.toasts.error(error) }
        }
    }

    func insertSavedReply(_ reply: SavedReply) {
        Task {
            let html = await store.render(reply.bodyHTML)
            let text = HTMLText.plain(from: html)
            if composer.token.map({ if case .savedReply = $0 { return true } else { return false } }) == true {
                composer.complete(with: text)
            } else {
                composer.text = composer.trimmed.isEmpty ? text : composer.text + (composer.text.hasSuffix("\n") ? "" : "\n\n") + text
            }
            composerFocus = true
        }
    }

    func insertNote(_ text: String) {
        composer.mode = .note
        composer.text = composer.trimmed.isEmpty ? text : composer.text + "\n\n" + text
        composerFocus = true
    }

    // MARK: Attachments

    func addPhotos(_ items: [PhotosPickerItem]) {
        for item in items {
            let type = item.supportedContentTypes.first { $0.conforms(to: .image) } ?? .jpeg
            let name = "Photo \(composer.attachments.count + 1).\(type.preferredFilenameExtension ?? "jpg")"
            let pending = PendingAttachment(filename: name, mimeType: type.preferredMIMEType ?? "image/jpeg", size: 0)
            composer.attachments.append(pending)
            Task {
                guard let data = try? await item.loadTransferable(type: Data.self) else {
                    mark(pending.id, .failed("Couldn't read the photo"))
                    return
                }
                await upload(pending.id, data: data)
            }
        }
    }

    func addFiles(_ urls: [URL]) {
        for url in urls {
            let access = url.startAccessingSecurityScopedResource()
            defer { if access { url.stopAccessingSecurityScopedResource() } }
            guard let data = try? Data(contentsOf: url) else {
                env.toasts.show("Couldn't read \(url.lastPathComponent)", kind: .error)
                continue
            }
            let type = UTType(filenameExtension: url.pathExtension) ?? .data
            let pending = PendingAttachment(filename: url.lastPathComponent, mimeType: type.preferredMIMEType ?? "application/octet-stream", size: data.count)
            composer.attachments.append(pending)
            Task { await upload(pending.id, data: data) }
        }
    }

    private func upload(_ id: UUID, data: Data) async {
        guard let index = composer.attachments.firstIndex(where: { $0.id == id }) else { return }
        let file = composer.attachments[index]
        composer.attachments[index].size = data.count
        if file.isImage { composer.attachments[index].preview = Self.thumbnail(data) }
        do {
            let uploaded = try await store.upload(data: data, filename: file.filename, mimeType: file.mimeType)
            guard let at = composer.attachments.firstIndex(where: { $0.id == id }) else { return }
            composer.attachments[at].state = .ready(id: uploaded.id)
            composer.attachments[at].uploaded = uploaded
        } catch {
            mark(id, .failed(error.userMessage))
        }
    }

    private func mark(_ id: UUID, _ state: PendingAttachment.State) {
        guard let index = composer.attachments.firstIndex(where: { $0.id == id }) else { return }
        composer.attachments[index].state = state
    }

    private static func thumbnail(_ data: Data) -> Data? {
        guard let image = UIImage(data: data) else { return nil }
        let side: CGFloat = 96
        let scale = side / max(image.size.width, image.size.height, 1)
        let size = CGSize(width: image.size.width * min(scale, 1), height: image.size.height * min(scale, 1))
        return UIGraphicsImageRenderer(size: size).image { _ in image.draw(in: CGRect(origin: .zero, size: size)) }.jpegData(compressionQuality: 0.7)
    }

    // MARK: Ollie

    func ollie(_ action: ComposerOllieAction) {
        switch action {
        case .draft:
            draft(instructions: nil)
        case .guidedDraft:
            sheet = .guidedDraft
        case let .rewrite(mode):
            let text = composer.trimmed
            Task {
                do {
                    composer.text = try await store.rewrite(text, mode: mode)
                    Haptics.success()
                } catch { env.toasts.error(error) }
            }
        case let .translate(code, _):
            let text = composer.trimmed
            Task {
                do {
                    composer.text = try await store.translateDraft(text, to: code)
                    Haptics.success()
                } catch { env.toasts.error(error) }
            }
        case .ask:
            sheet = .ask
        }
    }

    func draft(instructions: String?) {
        Task {
            if let text = await store.draft(instructions: instructions) {
                composer.mode = .reply
                composer.text = text
                composerFocus = true
                Haptics.success()
            } else if let message = store.aiError {
                env.toasts.show(message, kind: .error)
            }
        }
    }

    func summarize() {
        Task { await store.summarize() }
    }

    func triage() {
        Task {
            await store.triage(apply: false)
            if let message = store.aiError { env.toasts.show(message, kind: .error) } else {
                ollieExpanded = true
                env.toasts.show(store.suggestions.isEmpty ? "Ollie has nothing to change" : "Ollie has suggestions", kind: .success)
            }
        }
    }

    func accept(_ suggestion: AISuggestion) {
        Task {
            do {
                try await store.accept(suggestion, members: members, me: me)
                if let ticket = store.ticket { env.inbox.place(ticket) }
                Haptics.success()
            } catch { failed(error) }
        }
    }

    func acceptAll() {
        Task {
            do {
                try await store.acceptAll(members: members, me: me)
                if let ticket = store.ticket { env.inbox.place(ticket) }
                Haptics.success()
                env.toasts.show("Applied Ollie's suggestions", kind: .success)
            } catch { failed(error) }
        }
    }

    func skip(_ suggestion: AISuggestion) {
        Task {
            do { try await store.dismiss(suggestion) } catch { env.toasts.error(error) }
        }
    }

    func sendSuggestedReply() {
        sendingSuggestion = true
        scroll = ScrollRequest(target: .bottom)
        Task {
            defer { sendingSuggestion = false }
            do {
                try await store.sendSuggestedReply()
                if let ticket = store.ticket { env.inbox.place(ticket) }
                Haptics.success()
            } catch { env.toasts.error(error) }
        }
    }

    func editSuggestedReply() {
        guard let text = store.takeSuggestedReply() else { return }
        composer.mode = .reply
        composer.text = text
        composerFocus = true
    }

    func discardSuggestedReply() {
        Task {
            do {
                try await store.discardSuggestedReply()
                env.toasts.show("Suggestion discarded")
            } catch { env.toasts.error(error) }
        }
    }
}
