import Foundation

/// Saved replies and ticket views, as the settings lists edit them.
extension AccountStore {
    // MARK: Saved replies

    struct ReplyDraft: Equatable {
        var title = ""
        var shortcut = ""
        var body = ""
        var teamID: String?

        init() {}

        init(_ reply: AccountSavedReply) {
            title = reply.title
            shortcut = reply.shortcut
            body = reply.plainBody
            teamID = reply.teamID
        }

        /// Lowercased, without the leading slash (the server's own rule).
        var cleanShortcut: String {
            var value = shortcut.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
            while value.hasPrefix("/") { value.removeFirst() }
            return value
        }

        var shortcutProblem: String? {
            let value = cleanShortcut
            guard !value.isEmpty else { return nil }
            if value.count > 32 { return "Keep the shortcut to 32 characters." }
            if value.contains(where: { !($0.isASCII && ($0.isLetter || $0.isNumber || $0 == "-" || $0 == "_")) }) {
                return "Use letters, numbers, dashes and underscores."
            }
            return nil
        }

        var isValid: Bool {
            !title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                && !body.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                && shortcutProblem == nil
        }
    }

    func loadReplies() async {
        guard let api else { return }
        do {
            let page: Page<AccountSavedReply> = try await api.get("v1/saved-replies", query: [URLQueryItem(name: "limit", value: "100")])
            replies = page.data.sorted { $0.title.localizedCaseInsensitiveCompare($1.title) == .orderedAscending }
            repliesError = nil
        } catch {
            if !error.isCancellation { repliesError = error.userMessage }
        }
        repliesLoaded = true
    }

    /// Creates a reply, or saves `existing` with the draft's values.
    func saveReply(_ draft: ReplyDraft, existing: AccountSavedReply?) async throws {
        guard let api else { return }
        struct Body: Encodable, Sendable {
            let title: String
            let shortcut: String
            let body_html: String
            let team_id: String?
        }
        struct Patch: Encodable, Sendable {
            let title: String
            let shortcut: String
            let body_html: String
            /// `""` clears the team.
            let team_id: String
        }
        let title = draft.title.trimmingCharacters(in: .whitespacesAndNewlines)
        let html = HTMLText.html(fromPlain: draft.body.trimmingCharacters(in: .whitespacesAndNewlines))
        let saved: AccountSavedReply
        if let existing {
            // Edits keep rich text the web wrote unless the body changed here.
            let body = draft.body == existing.plainBody ? existing.bodyHTML : html
            saved = try await api.patch("v1/saved-replies/\(existing.id)", body: Patch(title: title, shortcut: draft.cleanShortcut, body_html: body, team_id: draft.teamID ?? ""))
        } else {
            saved = try await api.post("v1/saved-replies", body: Body(title: title, shortcut: draft.cleanShortcut, body_html: html, team_id: draft.teamID))
        }
        upsertReply(saved)
    }

    func deleteReply(_ reply: AccountSavedReply) async throws {
        guard let api else { return }
        try await api.delete("v1/saved-replies/\(reply.id)")
        replies.removeAll { $0.id == reply.id }
    }

    /// Someone changed a reply elsewhere (realtime).
    func upsertReply(_ reply: AccountSavedReply) {
        if let index = replies.firstIndex(where: { $0.id == reply.id }) { replies[index] = reply } else { replies.append(reply) }
        replies.sort { $0.title.localizedCaseInsensitiveCompare($1.title) == .orderedAscending }
    }

    func canEdit(_ reply: AccountSavedReply) -> Bool { session?.canWrite ?? false }

    // MARK: Views

    struct ViewDraft: Equatable {
        var name = ""
        var audience: AccountTicketView.Audience = .personal
        var teamID: String?
        var filters = TicketFilters()

        init() {}

        init(_ view: AccountTicketView) {
            name = view.name
            audience = view.audience
            teamID = view.teamID
            filters = view.filters
        }

        var isValid: Bool {
            !name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && (audience != .team || teamID != nil)
        }
    }

    func loadViews() async {
        guard let api else { return }
        do {
            let page: Page<AccountTicketView> = try await api.get("v1/views", query: [URLQueryItem(name: "limit", value: "100")])
            views = page.data.sorted { $0.position < $1.position }
            viewsError = nil
        } catch {
            if !error.isCancellation { viewsError = error.userMessage }
        }
        viewsLoaded = true
    }

    func saveView(_ draft: ViewDraft, existing: AccountTicketView?) async throws {
        guard let api else { return }
        struct Body: Encodable, Sendable {
            let name: String
            let filters: TicketFilters
            let shared: Bool
            let team_id: String?
        }
        struct Patch: Encodable, Sendable {
            let name: String
            let filters: TicketFilters
            let shared: Bool
            let team_id: String
        }
        let name = draft.name.trimmingCharacters(in: .whitespacesAndNewlines)
        let shared = draft.audience != .personal
        let team = draft.audience == .team ? draft.teamID : nil
        let saved: AccountTicketView
        if let existing {
            saved = try await api.patch("v1/views/\(existing.id)", body: Patch(name: name, filters: draft.filters, shared: shared, team_id: team ?? ""))
        } else {
            saved = try await api.post("v1/views", body: Body(name: name, filters: draft.filters, shared: shared, team_id: team))
        }
        if let index = views.firstIndex(where: { $0.id == saved.id }) { views[index] = saved } else { views.append(saved) }
    }

    func deleteView(_ view: AccountTicketView) async throws {
        guard let api else { return }
        try await api.delete("v1/views/\(view.id)")
        views.removeAll { $0.id == view.id }
    }

    /// Moves views and saves the position of each one that moved.
    func moveViews(from source: IndexSet, to destination: Int) async {
        guard let api else { return }
        let before = views
        let moving = source.map { views[$0] }
        var next = views.enumerated().filter { !source.contains($0.offset) }.map(\.element)
        let insertAt = destination - source.filter { $0 < destination }.count
        next.insert(contentsOf: moving, at: max(0, min(insertAt, next.count)))
        for index in next.indices { next[index].position = index }
        views = next
        struct Body: Encodable, Sendable { let position: Int }
        do {
            for view in next where before.first(where: { $0.id == view.id })?.position != view.position && canEdit(view) {
                let _: AccountTicketView = try await api.patch("v1/views/\(view.id)", body: Body(position: view.position))
            }
        } catch {
            views = before
        }
    }

    /// Shared views are the admins'; personal views their creator's.
    func canEdit(_ view: AccountTicketView) -> Bool {
        guard let session, session.canWrite else { return false }
        return view.shared || view.teamID != nil ? session.isAdmin : true
    }

    var canShareViews: Bool { session?.isAdmin ?? false }
}
