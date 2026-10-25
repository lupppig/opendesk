import Foundation
import Observation

enum MailComposeMode: String, Sendable {
    case new
    case reply
    case replyAll = "reply_all"
    case forward

    var title: String {
        switch self {
        case .new: "New message"
        case .reply: "Reply"
        case .replyAll: "Reply all"
        case .forward: "Forward"
        }
    }

    var symbol: String {
        switch self {
        case .new: "square.and.pencil"
        case .reply: "arrowshape.turn.up.left"
        case .replyAll: "arrowshape.turn.up.left.2"
        case .forward: "arrowshape.turn.up.right"
        }
    }
}

/// An address the composer can send from: an account and one of its addresses.
struct MailSender: Hashable, Sendable, Identifiable {
    var accountID: String
    var accountLabel: String
    var color: String
    var email: String
    var name: String
    var signatureHTML: String

    var id: String { accountID + "/" + email }
    var line: String { name.isEmpty ? email : "\(name) <\(email)>" }
}

/// A file attached in the composer: uploading, uploaded, failed, or one the draft or the forwarded message already has.
struct MailComposeFile: Identifiable, Hashable, Sendable {
    enum State: Hashable, Sendable { case uploading, ready, failed }

    var id: String
    var name: String
    var size: Int
    var contentType: String
    var state: State
    /// The upload's id (`POST /v1/attachments`).
    var uploadID: String?
    /// A file the saved draft already has (`MailAttachment.id`), kept with `keep_attachments`.
    var draftIndex: String?
}

/// One message being written: new mail, a reply, reply all or a forward. Drafts are
/// saved at the provider about two seconds after typing stops and when it closes.
@MainActor
@Observable
final class MailComposeStore {
    enum SaveState: Equatable { case idle, saving, saved, failed }

    let mode: MailComposeMode
    let threadID: String?
    /// The message answered or forwarded.
    let original: MailMessage?

    var senders: [MailSender] = []
    var sender: MailSender? {
        didSet {
            guard sender != oldValue, let sender else { return }
            // The signature follows the address, unless the person took it out.
            if !signatureRemoved { signatureHTML = sender.signatureHTML }
            changed()
        }
    }

    var to: [String] = [] { didSet { if to != oldValue { changed() } } }
    var cc: [String] = [] { didSet { if cc != oldValue { changed() } } }
    var bcc: [String] = [] { didSet { if bcc != oldValue { changed() } } }
    var subject = "" { didSet { if subject != oldValue { changed() } } }
    var body = "" { didSet { if body != oldValue { changed() } } }
    var signatureHTML = ""
    var signatureRemoved = false
    var forwardAttachments = true
    private(set) var files: [MailComposeFile] = []

    private(set) var draftID: String?
    private(set) var saveState: SaveState = .idle
    private(set) var sending = false
    private(set) var sent: MailMessage?

    @ObservationIgnored private let api: APIClient
    @ObservationIgnored private var saveTask: Task<Void, Never>?
    @ObservationIgnored private var dirty = false
    @ObservationIgnored private var saving: Task<Void, Never>?

    init(api: APIClient, mode: MailComposeMode, threadID: String? = nil, original: MailMessage? = nil, senders: [MailSender], ownEmails: Set<String>, initialTo: [String] = []) {
        self.api = api
        self.mode = mode
        self.threadID = threadID
        self.original = original
        self.senders = senders
        let preferred = Self.preferredSender(senders, original: original)
        sender = preferred
        signatureHTML = preferred?.signatureHTML ?? ""
        if let original {
            let subjectLine = original.subject
            switch mode {
            case .new: break
            case .reply, .replyAll:
                subject = subjectLine.lowercased().hasPrefix("re:") ? subjectLine : "Re: " + subjectLine
            case .forward:
                subject = subjectLine.lowercased().hasPrefix("fwd:") ? subjectLine : "Fwd: " + subjectLine
            }
            let own = ownEmails
            let fromSelf = own.contains(original.from.email.lowercased())
            switch mode {
            case .reply:
                let target = !original.replyTo.isEmpty ? original.replyTo : (fromSelf ? original.to : [original.from])
                to = Self.unique(target.map(\.email), excluding: [])
            case .replyAll:
                let primary = !original.replyTo.isEmpty ? original.replyTo : (fromSelf ? original.to : [original.from])
                to = Self.unique(primary.map(\.email), excluding: own)
                let rest = (fromSelf ? [] : original.to) + original.cc
                cc = Self.unique(rest.map(\.email), excluding: own.union(to.map { $0.lowercased() }))
            case .forward, .new:
                break
            }
        }
        if !initialTo.isEmpty { to = initialTo }
        dirty = false
    }

    /// Continues a saved draft.
    convenience init(api: APIClient, draft: MailMessage, threadID: String?, original: MailMessage?, senders: [MailSender], ownEmails: Set<String>) {
        let mode: MailComposeMode = original == nil ? .new : .reply
        self.init(api: api, mode: mode, threadID: threadID, original: original, senders: senders, ownEmails: ownEmails)
        draftID = draft.draftID ?? draft.id
        if let match = senders.first(where: { $0.email == (draft.ownAddress ?? draft.from.email).lowercased() && $0.accountID == draft.accountID }) {
            sender = match
        }
        to = draft.to.map(\.email)
        cc = draft.cc.map(\.email)
        bcc = draft.bcc.map(\.email)
        subject = draft.subject
        let (text, signature) = Self.split(draft.bodyHTML)
        body = text
        if let signature { signatureHTML = signature } else { signatureRemoved = true; signatureHTML = "" }
        files = draft.files.map { MailComposeFile(id: "draft-" + $0.id, name: $0.filename, size: $0.size, contentType: $0.contentType, state: .ready, draftIndex: $0.id) }
        saveState = .saved
        dirty = false
    }

    private static func preferredSender(_ senders: [MailSender], original: MailMessage?) -> MailSender? {
        if let original {
            let own = original.ownAddress?.lowercased()
            if let match = senders.first(where: { $0.accountID == original.accountID && $0.email == own }) { return match }
            if let match = senders.first(where: { $0.accountID == original.accountID }) { return match }
        }
        return senders.first
    }

    private static func unique(_ emails: [String], excluding: Set<String>) -> [String] {
        var seen = Set<String>()
        return emails.filter { email in
            let key = email.lowercased()
            guard !key.isEmpty, !excluding.contains(key), !seen.contains(key) else { return false }
            seen.insert(key)
            return true
        }
    }

    /// A draft's text and its signature block.
    static func split(_ html: String) -> (String, String?) {
        let pattern = #"(?is)<div class="od-signature">(.*)</div>\s*$"#
        guard let range = html.range(of: pattern, options: .regularExpression) else { return (HTMLText.plain(from: html), nil) }
        let block = String(html[range])
        let inner = block.replacingOccurrences(of: #"(?is)^<div class="od-signature">"#, with: "", options: .regularExpression)
            .replacingOccurrences(of: #"(?is)</div>\s*$"#, with: "", options: .regularExpression)
        return (HTMLText.plain(from: String(html[..<range.lowerBound])), inner)
    }

    // MARK: State

    var hasRecipient: Bool { !(to + cc + bcc).isEmpty }
    var uploading: Bool { files.contains { $0.state == .uploading } }
    var isEmpty: Bool {
        body.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && files.isEmpty && (mode != .new || (to.isEmpty && subject.isEmpty))
    }

    /// Everything the server sends, as written: the text, the signature block, and for a forward the original.
    var html: String {
        var html = HTMLText.html(fromPlain: body)
        if !signatureRemoved, !signatureHTML.isEmpty {
            html += #"<div class="od-signature">"# + signatureHTML + "</div>"
        }
        if mode == .forward, let original { html += Self.forwardBlock(original) }
        return html
    }

    static func forwardBlock(_ message: MailMessage) -> String {
        func list(_ addresses: [MailAddress]) -> String {
            addresses.map { HTMLText.escape($0.name.isEmpty ? $0.email : "\($0.name) <\($0.email)>") }.joined(separator: ", ")
        }
        let header = [
            "---------- Forwarded message ---------",
            "From: " + list([message.from]),
            "Date: " + HTMLText.escape(Format.fullDate(message.sentAt)),
            "Subject: " + HTMLText.escape(message.subject),
            "To: " + list(message.to),
        ].joined(separator: "<br>")
        let original = message.bodyHTML.isEmpty ? HTMLText.html(fromPlain: message.bodyText) : message.bodyHTML
        return "<br><div class=\"od-forward\">\(header)<br><br>\(original)</div>"
    }

    // MARK: Files

    func attach(data: Data, filename: String, mimeType: String) async {
        let local = MailComposeFile(id: UUID().uuidString, name: filename, size: data.count, contentType: mimeType, state: .uploading)
        files.append(local)
        do {
            let uploaded: Attachment = try await api.upload("v1/attachments", data: data, filename: filename, mimeType: mimeType)
            if let index = files.firstIndex(where: { $0.id == local.id }) {
                files[index].state = .ready
                files[index].uploadID = uploaded.id
            }
            changed()
        } catch {
            if let index = files.firstIndex(where: { $0.id == local.id }) { files[index].state = .failed }
        }
    }

    func remove(_ file: MailComposeFile) {
        files.removeAll { $0.id == file.id }
        changed()
    }

    func removeSignature() {
        signatureRemoved = true
        signatureHTML = ""
        changed()
    }

    // MARK: Drafts

    private func changed() {
        dirty = true
        saveTask?.cancel()
        saveTask = Task { [weak self] in
            try? await Task.sleep(for: .seconds(2))
            guard !Task.isCancelled else { return }
            await self?.saveDraft()
        }
    }

    private struct DraftBody: Encodable, Sendable {
        var account_id: String?
        var mode: String?
        var message_id: String?
        var to: [String]
        var cc: [String]
        var bcc: [String]
        var subject: String
        var body_html: String
        var attachment_ids: [String]?
        var keep_attachments: [String]?
        var forward_attachments: Bool?
        var from_email: String?
    }

    private func draftBody(forNew: Bool) -> DraftBody {
        DraftBody(
            account_id: forNew ? sender?.accountID : nil,
            mode: forNew ? mode.rawValue : nil,
            message_id: forNew ? original?.id : nil,
            to: to, cc: cc, bcc: bcc, subject: subject, body_html: html,
            attachment_ids: files.compactMap(\.uploadID).nilIfEmpty,
            keep_attachments: forNew ? nil : files.compactMap(\.draftIndex),
            forward_attachments: mode == .forward ? forwardAttachments : nil,
            from_email: sender?.email
        )
    }

    /// Saves what is written at the provider; nothing when nothing changed or nothing is written.
    func saveDraft() async {
        if let saving { await saving.value }
        guard dirty, !sending, sent == nil, sender != nil else { return }
        if draftID == nil, isEmpty { return }
        dirty = false
        saveState = .saving
        let task = Task { [self] in
            do {
                let message: MailMessage
                if let draftID {
                    let body = try APICoding.encoder.encode(draftBody(forNew: false))
                    message = try await api.send(APIRequest(method: "PUT", path: "v1/collab/drafts/\(draftID)", body: body))
                } else {
                    message = try await api.post("v1/collab/drafts", body: draftBody(forNew: true))
                }
                draftID = message.draftID ?? message.id
                // Uploads are part of the draft now: keep them as its files.
                for (index, file) in files.enumerated() where file.uploadID != nil {
                    if let match = message.files.first(where: { $0.filename == file.name }) {
                        files[index].uploadID = nil
                        files[index].draftIndex = match.id
                    }
                }
                saveState = .saved
            } catch {
                dirty = true
                saveState = .failed
            }
        }
        saving = task
        await task.value
        saving = nil
    }

    /// Sends the message (the draft when there is one). Throws with the provider's reason.
    func send() async throws {
        guard let sender else { throw MailComposeError.noAccount }
        guard hasRecipient else { throw MailComposeError.noRecipient }
        guard !uploading else { throw MailComposeError.uploading }
        saveTask?.cancel()
        if let saving { await saving.value }
        sending = true
        defer { sending = false }
        if let draftID {
            let body = try APICoding.encoder.encode(draftBody(forNew: false))
            sent = try await api.send(APIRequest(method: "POST", path: "v1/collab/drafts/\(draftID)/send", body: body))
        } else {
            struct Body: Encodable, Sendable {
                let account_id: String
                let mode: String
                let message_id: String?
                let to: [String]
                let cc: [String]?
                let bcc: [String]?
                let subject: String
                let body_html: String
                let attachment_ids: [String]?
                let forward_attachments: Bool?
                let from_email: String
            }
            let body = Body(
                account_id: sender.accountID, mode: mode.rawValue, message_id: original?.id, to: to,
                cc: cc.nilIfEmpty, bcc: bcc.nilIfEmpty, subject: subject, body_html: html,
                attachment_ids: files.compactMap(\.uploadID).nilIfEmpty,
                forward_attachments: mode == .forward ? forwardAttachments : nil, from_email: sender.email
            )
            sent = try await api.post("v1/collab/messages", body: body)
        }
        dirty = false
    }

    /// Discards the draft at the provider too.
    func discard() async throws {
        saveTask?.cancel()
        dirty = false
        if let saving { await saving.value }
        guard let draftID else { return }
        try await api.delete("v1/collab/drafts/\(draftID)")
        self.draftID = nil
    }

    /// Closing keeps what was written as a draft.
    func close() async {
        saveTask?.cancel()
        await saveDraft()
    }
}

enum MailComposeError: LocalizedError {
    case noAccount, noRecipient, uploading

    var errorDescription: String? {
        switch self {
        case .noAccount: "Connect a mail account to send mail"
        case .noRecipient: "Add at least one recipient"
        case .uploading: "Wait for the uploads to finish"
        }
    }
}

fileprivate extension Array {
    /// nil for an empty list, so the request leaves the member out.
    var nilIfEmpty: [Element]? { isEmpty ? nil : self }
}

extension MailStore {
    /// Every address the person can send from, account by account.
    var senders: [MailSender] {
        accounts.flatMap { account in
            Self.senders(accountID: account.id, label: account.label, color: account.color, email: account.email, displayName: account.displayName, signature: account.signatureHTML, identities: account.identities, defaultFrom: account.defaultFrom)
        }
    }

    static func senders(of account: MailThreadAccount) -> [MailSender] {
        senders(accountID: account.id, label: account.label, color: account.color, email: account.email, displayName: account.displayName, signature: account.signatureHTML, identities: account.identities, defaultFrom: account.defaultFrom)
    }

    private static func senders(accountID: String, label: String, color: String, email: String, displayName: String, signature: String, identities: [MailIdentity], defaultFrom: String) -> [MailSender] {
        let usable = identities.filter(\.canSend)
        if usable.isEmpty {
            return [MailSender(accountID: accountID, accountLabel: label, color: color, email: email.lowercased(), name: displayName, signatureHTML: signature)]
        }
        let ordered = usable.sorted { a, b in
            let da = a.email == defaultFrom.lowercased() || a.isDefault
            let db = b.email == defaultFrom.lowercased() || b.isDefault
            if da != db { return da }
            return a.primary && !b.primary
        }
        return ordered.map { identity in
            MailSender(
                accountID: accountID, accountLabel: label, color: color, email: identity.email,
                name: identity.fromName.isEmpty ? (identity.displayName.isEmpty ? displayName : identity.displayName) : identity.fromName,
                signatureHTML: identity.primary ? (identity.filledSignatureHTML ?? signature) : identity.signature
            )
        }
    }
}
