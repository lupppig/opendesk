import Foundation
import Observation

/// What the composer writes: a reply to the customer, an internal note, or a message in a
/// side conversation with someone outside the team.
enum ComposerMode: String, CaseIterable, Identifiable, Sendable {
    case reply, note, side
    var id: String { rawValue }

    var title: String {
        switch self {
        case .reply: "Reply"
        case .note: "Note"
        case .side: "Side conversation"
        }
    }

    var short: String {
        switch self {
        case .reply: "Reply"
        case .note: "Note"
        case .side: "Side"
        }
    }

    var symbol: String {
        switch self {
        case .reply: "arrowshape.turn.up.left"
        case .note: "lock"
        case .side: "arrow.triangle.branch"
        }
    }
}

/// The token being typed at the end of the text that opens a suggestion list.
enum ComposerToken: Equatable, Sendable {
    /// `/shortcut` at the start of a line: saved replies.
    case savedReply(String)
    /// `@name` in a note: teammates.
    case mention(String)
    /// `{{ticket.` : the ticket's template values.
    case variable(String)
}

/// A composer's unsent state, kept per ticket for the life of the app.
struct ComposerDraft: Sendable {
    var mode: ComposerMode = .reply
    var text = ""
    var cc = ""
    var bcc = ""
    var showCopies = false
    var mentions: [String: String] = [:]
    var fromNoteID: String?
    var sideThreadID: String?
    var sideTo = ""
    var sideSubject = ""
    var includeHistory = false
}

/// A reply held for the undo window before it goes out.
struct HeldSend: Identifiable, Sendable {
    let id = UUID()
    var outgoing: OutgoingMessage
    var draft: ComposerDraft
    var until: Date
}

/// The composer of one ticket: mode, text, copies, mentions, attachments, the
/// suggestion token, and the undo window of a send.
@MainActor
@Observable
final class TicketComposerStore {
    /// Unsent drafts by ticket, so leaving a ticket keeps what was typed.
    private static var drafts: [String: ComposerDraft] = [:]

    let ticketID: String
    var mode: ComposerMode = .reply { didSet { save() } }
    var text = "" { didSet { save() } }
    var cc = "" { didSet { save() } }
    var bcc = "" { didSet { save() } }
    var showCopies = false { didSet { save() } }
    var sideTo = "" { didSet { save() } }
    var sideSubject = "" { didSet { save() } }
    var includeHistory = false { didSet { save() } }
    /// The open side conversation a `.side` message answers; nil starts a new one.
    var sideThreadID: String? { didSet { save() } }
    private(set) var mentions: [String: String] = [:]
    /// The caller's own note this reply was started from.
    private(set) var fromNoteID: String?
    var attachments: [PendingAttachment] = []
    private(set) var held: HeldSend?

    @ObservationIgnored private var holdTask: Task<Void, Never>?
    @ObservationIgnored private var restoring = false

    init(ticketID: String) {
        self.ticketID = ticketID
        if let draft = Self.drafts[ticketID] { restore(draft) }
    }

    var draft: ComposerDraft {
        ComposerDraft(mode: mode, text: text, cc: cc, bcc: bcc, showCopies: showCopies, mentions: mentions, fromNoteID: fromNoteID,
                      sideThreadID: sideThreadID, sideTo: sideTo, sideSubject: sideSubject, includeHistory: includeHistory)
    }

    func restore(_ draft: ComposerDraft) {
        restoring = true
        mode = draft.mode
        text = draft.text
        cc = draft.cc
        bcc = draft.bcc
        showCopies = draft.showCopies
        mentions = draft.mentions
        fromNoteID = draft.fromNoteID
        sideThreadID = draft.sideThreadID
        sideTo = draft.sideTo
        sideSubject = draft.sideSubject
        includeHistory = draft.includeHistory
        restoring = false
        save()
    }

    private func save() {
        guard !restoring else { return }
        let value = draft
        if value.text.isEmpty, value.mode == .reply, value.cc.isEmpty, value.bcc.isEmpty {
            Self.drafts[ticketID] = nil
        } else {
            Self.drafts[ticketID] = value
        }
    }

    var trimmed: String { text.trimmingCharacters(in: .whitespacesAndNewlines) }
    var uploading: Bool { attachments.contains { $0.state == .uploading } }
    var readyAttachmentIDs: [String] { attachments.compactMap(\.attachmentID) }
    var hasContent: Bool { !trimmed.isEmpty || !readyAttachmentIDs.isEmpty }

    var canSend: Bool {
        guard hasContent, !uploading else { return false }
        if mode == .side, sideThreadID == nil {
            return !Self.addresses(sideTo).isEmpty && !trimmed.isEmpty
        }
        return true
    }

    func clear() {
        restoring = true
        text = ""
        cc = ""
        bcc = ""
        showCopies = false
        mentions = [:]
        fromNoteID = nil
        sideTo = ""
        sideSubject = ""
        includeHistory = false
        if mode == .side { sideThreadID = nil }
        attachments = []
        restoring = false
        save()
    }

    // MARK: Tokens

    /// The suggestion token being typed at the end of the text.
    var token: ComposerToken? {
        guard !text.isEmpty else { return nil }
        if let range = text.range(of: #"\{\{\s*([a-z_.]*)$"#, options: .regularExpression) {
            let raw = String(text[range]).dropFirst(2).trimmingCharacters(in: .whitespaces)
            return mode == .note ? nil : .variable(raw)
        }
        if mode == .note, let range = text.range(of: #"(^|\s)@([\p{L}\p{N}._-]{0,30})$"#, options: .regularExpression) {
            let raw = String(text[range]).trimmingCharacters(in: .whitespaces).dropFirst()
            return .mention(String(raw))
        }
        if mode != .side, let range = text.range(of: #"(^|\n)/([\p{L}\p{N}_-]{0,30})$"#, options: .regularExpression) {
            let raw = String(text[range]).trimmingCharacters(in: .whitespacesAndNewlines).dropFirst()
            return .savedReply(String(raw))
        }
        return nil
    }

    /// Replaces the token at the end of the text.
    func complete(with replacement: String) {
        guard let token else { return }
        let pattern: String
        switch token {
        case .variable: pattern = #"\{\{\s*[a-z_.]*$"#
        case .mention: pattern = #"@[\p{L}\p{N}._-]{0,30}$"#
        case .savedReply: pattern = #"/[\p{L}\p{N}_-]{0,30}$"#
        }
        guard let range = text.range(of: pattern, options: .regularExpression) else { return }
        text.replaceSubrange(range, with: replacement)
    }

    func mention(_ member: Member) {
        let name = member.user.name
        mentions[name] = member.id
        complete(with: "@\(name) ")
    }

    /// Teammates named with an @ in the text.
    func mentioned(in members: [Member]) -> [Member] {
        members.filter { member in
            let name = member.user.name
            guard !name.isEmpty else { return false }
            if text.range(of: "@" + name, options: [.caseInsensitive]) != nil { return true }
            guard let first = name.split(separator: " ").first else { return false }
            let pattern = #"(^|\s)@"# + NSRegularExpression.escapedPattern(for: String(first)) + #"(?![\p{L}\p{N}])"#
            return text.range(of: pattern, options: [.regularExpression, .caseInsensitive]) != nil
        }
    }

    // MARK: Modes

    /// Starts a reply from one of the caller's own notes (it becomes a marker once sent).
    func startReply(fromNote message: Message) {
        let body = HTMLText.plain(from: message.bodyHTML)
        mode = .reply
        text = text.isEmpty ? body : text + "\n\n" + body
        fromNoteID = message.id
    }

    func cancelFromNote() { fromNoteID = nil }

    func answer(thread: SideThread) {
        mode = .side
        sideThreadID = thread.id
    }

    // MARK: Building

    /// The body as stored HTML: paragraphs, links, and mentions as the API marks them.
    func html(members: [Member]) -> String {
        var html = HTMLText.html(fromPlain: trimmed)
        guard mode == .note else { return html }
        for member in mentioned(in: members) {
            let name = HTMLText.escape(member.user.name)
            let span = "<span class=\"mention\" data-id=\"\(HTMLText.escape(member.id))\">@\(name)</span>"
            html = html.replacingOccurrences(of: "@" + name, with: span)
        }
        return html
    }

    static func addresses(_ raw: String) -> [String] {
        raw.split(whereSeparator: { $0 == "," || $0 == ";" || $0 == " " || $0 == "\n" })
            .map { $0.trimmingCharacters(in: .whitespaces).lowercased() }
            .filter { $0.contains("@") && $0.contains(".") }
    }

    // MARK: Undo send

    /// Holds a reply for `seconds`, then sends it; `undo()` within the window puts it back.
    func hold(_ outgoing: OutgoingMessage, seconds: Double, send: @escaping @MainActor (OutgoingMessage) async -> Void) {
        let snapshot = draft
        let pending = HeldSend(outgoing: outgoing, draft: snapshot, until: AppClock.now.addingTimeInterval(seconds))
        held = pending
        clear()
        holdTask?.cancel()
        holdTask = Task { [weak self] in
            try? await Task.sleep(for: .seconds(seconds))
            guard !Task.isCancelled else { return }
            if self?.held?.id == pending.id { self?.held = nil }
            await send(outgoing)
        }
    }

    /// Sends the held reply now.
    func sendHeldNow(send: @escaping @MainActor (OutgoingMessage) async -> Void) {
        guard let pending = held else { return }
        holdTask?.cancel()
        held = nil
        Task { await send(pending.outgoing) }
    }

    /// Takes the held reply back into the composer.
    func undo() {
        guard let pending = held else { return }
        holdTask?.cancel()
        held = nil
        restore(pending.draft)
        attachments = pending.outgoing.attachments.map { file in
            PendingAttachment(filename: file.filename, mimeType: file.contentType, size: file.sizeBytes, state: .ready(id: file.id), uploaded: file)
        }
    }
}
