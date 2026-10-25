import Foundation
import Observation

/// One open conversation: its messages, comments, shares and assignees, kept live.
@MainActor
@Observable
final class MailThreadStore {
    /// The conversation's id; a link may name it some other way until it loads.
    private(set) var threadID: String
    private(set) var detail: MailThreadDetail?
    private(set) var loading = false
    private(set) var error: String?
    private(set) var notFound = false
    /// Comments that arrived while the conversation was open (they glow once).
    private(set) var freshComments: Set<String> = []
    /// Bumped when a comment is posted here, so the view scrolls to it.
    private(set) var postedComment: String?

    @ObservationIgnored private let api: APIClient
    @ObservationIgnored private let mail: MailStore
    @ObservationIgnored private weak var realtime: RealtimeService?
    @ObservationIgnored private var listener: UUID?
    @ObservationIgnored private var quietTask: Task<Void, Never>?
    @ObservationIgnored private var markedRead = false

    /// A comment not posted yet waits for its conversation while you read another.
    private static var unsent: [String: String] = [:]

    init(threadID: String, api: APIClient, mail: MailStore = .shared) {
        self.threadID = threadID
        self.api = api
        self.mail = mail
    }

    func start(realtime: RealtimeService) {
        guard listener == nil else { return }
        self.realtime = realtime
        listener = realtime.subscribe { [weak self] update in self?.handle(update) }
    }

    func stop() {
        realtime?.unsubscribe(listener)
        listener = nil
        quietTask?.cancel()
    }

    var thread: MailThread? { detail?.thread }
    var canAct: Bool { detail?.canAct ?? false }

    /// Messages that are not drafts, oldest first.
    var messages: [MailMessage] { detail?.messages.filter { !$0.draft } ?? [] }
    var drafts: [MailMessage] { detail?.messages.filter(\.draft) ?? [] }

    func load() async {
        loading = detail == nil
        defer { loading = false }
        do {
            let value: MailThreadDetail = try await api.get("v1/collab/threads/\(threadID)")
            if let old = detail {
                let known = Set(old.comments.map(\.id))
                freshComments.formUnion(value.comments.map(\.id).filter { !known.contains($0) })
            }
            detail = value
            threadID = value.thread.id
            error = nil
            notFound = false
            mail.update(value.thread)
        } catch let failure as APIError where failure.status == 404 {
            notFound = true
        } catch {
            guard !error.isCancellation, detail == nil else { return }
            self.error = error.userMessage
        }
    }

    /// Marks it read once when it opens, as the web does (`mark_read_on_open`).
    func markReadOnOpen() async {
        guard !markedRead, let detail, detail.canAct, detail.thread.unread, mail.prefs.markReadOnOpen else { return }
        markedRead = true
        try? await act(.read)
    }

    // MARK: Actions

    func act(_ action: MailAction, folderID: String? = nil) async throws {
        guard let detail else { return }
        let before = detail.thread
        self.detail?.thread = mail.applied(action, folderID: folderID, to: before)
        do {
            let threads = try await mail.act([threadID], action, folderID: folderID, known: [before])
            if let fresh = threads.first(where: { $0.id == threadID }) { self.detail?.thread = fresh }
        } catch {
            self.detail?.thread = before
            throw error
        }
    }

    func share(_ audience: MailAudience) async throws {
        struct Body: Encodable, Sendable { let shares: MailAudience }
        let value: MailThreadDetail = try await api.send(APIRequest(method: "PUT", path: "v1/collab/threads/\(threadID)/shares", body: try APICoding.encoder.encode(Body(shares: audience))))
        apply(value)
    }

    func assign(_ userIDs: [String], members: [Member]) async throws {
        guard let current = detail else { return }
        detail?.thread.assignees = userIDs.compactMap { id in
            current.thread.assignees.first { $0.id == id } ?? members.first { $0.id == id }?.ref
        }
        struct Body: Encodable, Sendable { let user_ids: [String] }
        do {
            let value: MailThreadDetail = try await api.send(APIRequest(method: "PUT", path: "v1/collab/threads/\(threadID)/assignees", body: try APICoding.encoder.encode(Body(user_ids: userIDs))))
            apply(value)
        } catch {
            detail = current
            throw error
        }
    }

    private func apply(_ value: MailThreadDetail) {
        detail = value
        mail.update(value.thread)
    }

    // MARK: Comments

    var unsentComment: String {
        get { Self.unsent[threadID] ?? "" }
        set {
            if newValue.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty { Self.unsent[threadID] = nil } else { Self.unsent[threadID] = newValue }
        }
    }

    func comment(text: String, mentions: [MailMention]) async throws {
        let html = MailCommentText.html(text: text, mentions: mentions)
        let ids = MailCommentText.mentionIDs(in: text, mentions: mentions)
        struct Body: Encodable, Sendable {
            let body_html: String
            let mentions: [String]
        }
        let comment: MailComment = try await api.post("v1/collab/threads/\(threadID)/comments", body: Body(body_html: html, mentions: ids))
        insert(comment)
        postedComment = comment.id
        Self.unsent[threadID] = nil
        // Mentioning someone who could not see it shared it with them: read the shares again.
        if !ids.isEmpty { await load() }
    }

    func editComment(_ comment: MailComment, text: String, mentions: [MailMention]) async throws {
        struct Body: Encodable, Sendable { let body_html: String }
        let html = MailCommentText.html(text: text, mentions: mentions)
        let saved: MailComment = try await api.patch("v1/collab/comments/\(comment.id)", body: Body(body_html: html))
        guard let index = detail?.comments.firstIndex(where: { $0.id == saved.id }) else { return }
        detail?.comments[index] = saved
    }

    func deleteComment(_ comment: MailComment) async throws {
        let before = detail
        detail?.comments.removeAll { $0.id == comment.id }
        detail?.thread.commentCount = detail?.comments.count ?? 0
        do {
            try await api.delete("v1/collab/comments/\(comment.id)")
            if let thread = detail?.thread { mail.update(thread) }
        } catch {
            detail = before
            throw error
        }
    }

    private func insert(_ comment: MailComment) {
        guard detail != nil, detail?.comments.contains(where: { $0.id == comment.id }) == false else { return }
        detail?.comments.append(comment)
        detail?.thread.commentCount = detail?.comments.count ?? 0
        freshComments.insert(comment.id)
        if let thread = detail?.thread { mail.update(thread) }
    }

    // MARK: Who sees it

    /// Whether a member sees this conversation: its account's audience, its own shares, its assignees.
    func canSee(_ member: Member, teams: [Team]) -> Bool {
        guard let detail else { return true }
        if detail.thread.assignees.contains(where: { $0.id == member.id }) { return true }
        func reaches(_ audience: MailAudience) -> Bool {
            if audience.everyone || audience.userIDs.contains(member.id) { return true }
            return teams.contains { audience.teamIDs.contains($0.id) && ($0.memberIDs ?? []).contains(member.id) }
        }
        if reaches(detail.shares) { return true }
        guard let account = mail.account(detail.account.id) else { return false }
        if account.owner?.id == member.id { return true }
        return !account.isPrivate && reaches(account.sharedWith)
    }

    // MARK: Reads

    func details(of message: MailMessage) async throws -> MailMessageDetails {
        try await api.get("v1/collab/messages/\(message.id)/details")
    }

    func person(_ email: String) async throws -> MailPerson {
        try await api.get("v1/collab/person", query: [
            URLQueryItem(name: "email", value: email),
            URLQueryItem(name: "exclude", value: threadID),
            URLQueryItem(name: "limit", value: "8"),
        ])
    }

    // MARK: Realtime

    func handle(_ update: RealtimeUpdate) {
        switch update {
        case .resync:
            scheduleQuiet()
        case let .envelope(envelope):
            switch envelope.type {
            case "collab.changed":
                let ids = envelope.data["thread_ids"]?.array?.compactMap(\.string) ?? []
                let account = envelope.string("account_id")
                if ids.contains(threadID) || (ids.isEmpty && account == detail?.account.id) { scheduleQuiet() }
            case "collab.accounts":
                if envelope.string("account_id") == detail?.account.id { scheduleQuiet() }
            default:
                break
            }
        }
    }

    private func scheduleQuiet() {
        quietTask?.cancel()
        quietTask = Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(350))
            guard !Task.isCancelled, let self else { return }
            await self.load()
        }
    }
}

/// A teammate picked from the @ list while writing a comment.
struct MailMention: Hashable, Sendable {
    var id: String
    var name: String
}

/// Comments are written as plain text with @ mentions and stored as HTML the web's editor reads.
enum MailCommentText {
    /// Paragraphs of escaped text, each picked mention as the editor's mention span.
    static func html(text: String, mentions: [MailMention]) -> String {
        var html = HTMLText.html(fromPlain: text)
        for mention in Set(mentions).sorted(by: { $0.name.count > $1.name.count }) {
            let token = "@" + HTMLText.escape(mention.name)
            let span = #"<span data-type="mention" class="mention" data-id="\#(mention.id)" data-label="\#(HTMLText.escape(mention.name))">\#(token)</span>"#
            html = html.replacingOccurrences(of: token, with: span)
        }
        return html
    }

    /// The ids of the picked mentions still in the text, in order.
    static func mentionIDs(in text: String, mentions: [MailMention]) -> [String] {
        var out: [String] = []
        for mention in mentions where text.contains("@" + mention.name) && !out.contains(mention.id) { out.append(mention.id) }
        return out
    }

    /// The mentions a stored comment carries, for editing it again.
    static func mentions(in html: String) -> [MailMention] {
        guard let regex = try? NSRegularExpression(pattern: #"<span\b[^>]*data-id="([^"]+)"[^>]*>@([^<]+)</span>"#) else { return [] }
        let ns = html as NSString
        return regex.matches(in: html, range: NSRange(location: 0, length: ns.length)).map {
            MailMention(id: ns.substring(with: $0.range(at: 1)), name: HTMLText.decodeEntities(ns.substring(with: $0.range(at: 2))))
        }
    }
}
