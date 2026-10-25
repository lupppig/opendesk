#if DEBUG
import Foundation

/// The API in demo mode (`DemoMode`): answers `APIClient`'s requests from the
/// in-memory world of `DemoSeed`, in the shapes of contracts/api.md, so every
/// store and screen runs its real code. Changes (replies, notes, assignment,
/// status, priority, snooze, read notifications, Ollie's actions) update the
/// world and publish the events a server would, through `RealtimeService`.
///
/// Each area answers its own routes from an extension in its own file
/// (`DemoTickets.swift`, `DemoAccount.swift`, `DemoAssistant.swift`, `DemoDirectory.swift`,
/// `DemoMail.swift`, `DemoKB.swift`, `DemoInbox.swift`); those run before the routes below.
actor DemoBackend {
    static let shared = DemoBackend()
    static let refreshToken = "demo-refresh-token"

    var world = DemoSeed.world()
    var listener: (@Sendable (Envelope) -> Void)?
    var outbox: [Envelope] = []
    var pending: [String: PendingAction] = [:]
    var uploads: [String: Attachment] = [:]
    /// Seconds past the anchor for things made in this run, so they sort after the seed.
    var tick = 0
    var serial = 0

    struct PendingAction {
        var workspace: String
        var ticketIDs: [String]
        var patch: [String: JSONValue]
        var done: String
        var step: AssistantStep
    }

    struct Failure: Error {
        var status: Int
        var code: String
        var detail: String
    }

    struct Call {
        var method: String
        var path: [String]
        var query: [String: String]
        var workspace: String?
        var body: JSONValue
        var raw: Data

        /// The wildcards of `pattern` (`tickets/*/messages`) when the call is `method` on it.
        func match(_ method: String, _ pattern: String) -> [String]? {
            guard method == self.method else { return nil }
            let parts = pattern.split(separator: "/").map(String.init)
            guard parts.count == path.count else { return nil }
            var captured: [String] = []
            for (part, segment) in zip(parts, path) {
                if part == "*" { captured.append(segment) } else if part != segment { return nil }
            }
            return captured
        }

        func string(_ key: String) -> String? { body[key]?.string }
    }

    struct ListBody<T: Encodable>: Encodable {
        var data: [T]
        var nextCursor: String?

        enum CodingKeys: String, CodingKey {
            case data
            case nextCursor = "next_cursor"
        }
    }

    // MARK: Entry points

    /// The seed's world again, for a demo entered anew from the sign-in screen.
    func restart() {
        world = DemoSeed.world()
        outbox = []
        pending = [:]
        uploads = [:]
        tick = 0
        serial = 0
    }

    func listen(_ handler: @escaping @Sendable (Envelope) -> Void) {
        listener = handler
    }

    func presence(workspace: String) -> [String: [PresenceViewer]] {
        world.workspaces.first { $0.id == workspace }?.presence ?? [:]
    }

    func respond(to request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        let url = request.url ?? DemoMode.instance.apiURL
        let call = Self.call(from: request)
        var status = 200
        var data = Data()
        do {
            if let answer = try handle(call) {
                data = answer
            } else {
                status = 204
            }
        } catch let failure as Failure {
            status = failure.status
            data = encode(JSONValue.object(["title": .string(failure.code), "status": .number(Double(failure.status)), "code": .string(failure.code), "detail": .string(failure.detail)]))
        }
        flush()
        let response = HTTPURLResponse(url: url, statusCode: status, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": "application/json"])!
        return (data, response)
    }

    /// Ollie's streamed answers (`text/event-stream`), paced like a model writing.
    func stream(_ request: URLRequest) async throws -> AsyncThrowingStream<SSEEvent, Error> {
        let call = Self.call(from: request)
        let beats: [Beat]
        do {
            if let extra = try streamRoutes(call) {
                beats = extra
            } else if let session = call.match("POST", "assistant/sessions/*/messages")?.first {
                beats = try assistantRun(call, session: session)
            } else if let action = call.match("POST", "assistant/actions/*/decision")?.first {
                beats = try assistantDecision(call, action: action)
            } else {
                throw Failure(status: 404, code: "not_found", detail: "The demo has no stream at \(call.path.joined(separator: "/")).")
            }
        } catch let failure as Failure {
            throw APIError(status: failure.status, code: failure.code, title: nil, detail: failure.detail, fieldErrors: [])
        }
        flush()
        let events = beats.map { SSEEvent(event: $0.event, data: String(decoding: encode($0.data), as: UTF8.self)) }
        let pauses = beats.map(\.pause)
        return AsyncThrowingStream { continuation in
            let task = Task {
                for (event, pause) in zip(events, pauses) {
                    if pause > 0 { try? await Task.sleep(for: .milliseconds(pause)) }
                    if Task.isCancelled { break }
                    continuation.yield(event)
                }
                continuation.finish()
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }

    static func call(from request: URLRequest) -> Call {
        let components = request.url.flatMap { URLComponents(url: $0, resolvingAgainstBaseURL: false) }
        var path = (components?.path ?? "").split(separator: "/").map { String($0).removingPercentEncoding ?? String($0) }
        if path.first == "v1" { path.removeFirst() }
        var query: [String: String] = [:]
        for item in components?.queryItems ?? [] { query[item.name] = item.value ?? "" }
        let raw = request.httpBody ?? Data()
        let isJSON = request.value(forHTTPHeaderField: "Content-Type")?.hasPrefix("application/json") ?? false
        let body = isJSON ? ((try? JSONValue.parse(raw)) ?? .null) : .null
        return Call(method: request.httpMethod ?? "GET", path: path, query: query, workspace: request.value(forHTTPHeaderField: "OD-Workspace"), body: body, raw: raw)
    }

    // MARK: Routes

    /// The answer's body, or nil for 204.
    func handle(_ call: Call) throws -> Data? {
        if let answer = try meRoutes(call) { return answer }
        // Signing in: any email and password open Maya's account.
        for route in ["auth/login", "auth/register", "auth/mfa", "auth/oauth/exchange", "auth/oauth/link", "auth/passkey/login/finish", "auth/refresh"] where call.match("POST", route) != nil {
            return encode(JSONValue.object([
                "access_token": .string("demo-access-token"), "expires_in": .number(86400),
                "refresh_token": .string(Self.refreshToken), "user": DemoSeed.json(world.user),
            ]))
        }
        if call.match("POST", "auth/logout") != nil || call.match("POST", "auth/password/forgot") != nil || call.match("POST", "auth/password/reset") != nil { return nil }
        if call.path.first == "auth" { throw Failure(status: 400, code: "captcha_failed", detail: "The demo signs in with email and password.") }

        // The account.
        if call.match("GET", "me") != nil {
            return encode(Me(user: world.user, memberships: world.workspaces.map { Membership(workspace: $0.workspace, role: $0.role) }))
        }
        if call.match("PATCH", "me") != nil {
            if let name = call.string("name"), !name.isEmpty { world.user.name = name }
            return encode(world.user)
        }
        if call.match("DELETE", "me") != nil || call.match("POST", "me/heartbeat") != nil { return nil }
        if call.match("GET", "me/sessions") != nil { return encode(ListBody(data: world.sessions)) }
        if let id = call.match("DELETE", "me/sessions/*")?.first {
            world.sessions.removeAll { $0.id == id }
            return nil
        }
        if call.match("GET", "me/devices") != nil { return encode(world.devices) }
        if call.match("POST", "me/devices") != nil { return encode(world.devices[0]) }
        if let id = call.match("PATCH", "me/devices/*")?.first, let index = world.devices.firstIndex(where: { $0.id == id }) {
            world.devices[index].mutedTypes = call.body["muted_types"]?.array?.compactMap(\.string) ?? []
            return encode(world.devices[index])
        }
        if call.match("DELETE", "me/devices/*") != nil { return nil }
        if call.match("POST", "realtime/token") != nil { return encode(JSONValue.object(["token": .string("demo"), "url": .null])) }
        if call.match("POST", "workspaces") != nil { return createWorkspace(name: call.string("name") ?? "New workspace") }

        // Everything else belongs to a workspace.
        guard let wid = call.workspace, let index = world.workspaces.firstIndex(where: { $0.id == wid }) else {
            throw Failure(status: 400, code: "workspace_required", detail: "Pick a workspace first.")
        }
        var ws = world.workspaces[index]
        defer { world.workspaces[index] = ws }
        return try handle(call, in: &ws)
    }

    func handle(_ call: Call, in ws: inout DemoWorkspace) throws -> Data? {
        if let answer = try ticketRoutes(call, in: &ws) { return answer }
        if let answer = try inboxRoutes(call, in: &ws) { return answer }
        if let answer = try accountRoutes(call, in: &ws) { return answer }
        if let answer = try assistantRoutes(call, in: &ws) { return answer }
        if let answer = try directoryRoutes(call, in: &ws) { return answer }
        if let answer = try mailRoutes(call, in: &ws) { return answer }
        if let answer = try kbRoutes(call, in: &ws) { return answer }
        let query = call.query
        if call.match("GET", "members") != nil {
            let members = ws.members.map { member in
                var member = member
                member.openTickets = ws.tickets.filter { !$0.spam && !$0.status.isDone && $0.assignee?.id == member.id }.count
                return member
            }
            return encode(ListBody(data: members))
        }
        if call.match("PATCH", "members/me/availability") != nil {
            guard let member = ws.members.firstIndex(where: { $0.id == world.user.id }) else { throw notFound("member") }
            let value = call.string("availability").flatMap(Availability.init(rawValue:)) ?? .online
            ws.members[member].availability = value
            ws.members[member].presence = value
            publish("member.updated", ["member": DemoSeed.json(ws.members[member])], in: &ws)
            return encode(ws.members[member])
        }
        if call.match("GET", "teams") != nil { return encode(ListBody(data: ws.teams)) }
        if call.match("GET", "tags") != nil { return encode(ListBody(data: ws.tags)) }
        if call.match("GET", "views") != nil { return encode(ListBody(data: ws.views)) }
        if call.match("GET", "saved-replies") != nil { return encode(ListBody(data: ws.savedReplies)) }
        if call.match("GET", "ai/config") != nil {
            return encode(AIConfig(enabled: true, assistantName: "Ollie", features: AIFeatures(enabled: true, assistant: true, widgetAnswers: true, autoTriage: true)))
        }
        if call.match("GET", "events") != nil {
            return encode(JSONValue.object(["data": .array([]), "latest_seq": .number(Double(ws.seq)), "resync": .bool(false)]))
        }

        // Tickets.
        if call.match("GET", "tickets/counts") != nil { return encode(counts(ws)) }
        if call.match("GET", "tickets") != nil {
            let matching = sorted(ws.tickets.filter { matches($0, query, in: ws) }, query)
            let offset = Int(query["cursor"] ?? "") ?? 0
            let limit = max(1, Int(query["limit"] ?? "") ?? 50)
            let slice = Array(matching.dropFirst(offset).prefix(limit))
            let next = offset + slice.count < matching.count ? String(offset + slice.count) : nil
            return encode(ListBody(data: slice, nextCursor: next))
        }
        if call.match("GET", "search") != nil {
            return encode(JSONValue.object(["tickets": DemoSeed.json(ws.tickets.filter { matches($0, ["q": query["q"] ?? ""], in: ws) })]))
        }
        if let key = call.match("GET", "tickets/*")?.first {
            return encode(ws.tickets[try ticket(key, in: ws)])
        }
        if let key = call.match("PATCH", "tickets/*")?.first {
            let index = try ticket(key, in: ws)
            update(index, with: call.body.object ?? [:], by: ActivityActor(type: "user", user: me), in: &ws)
            return encode(ws.tickets[index])
        }
        if let key = call.match("GET", "tickets/*/messages")?.first {
            let id = ws.tickets[try ticket(key, in: ws)].id
            return encode(ListBody(data: (ws.messages[id] ?? []).sorted { $0.createdAt < $1.createdAt }))
        }
        if let key = call.match("POST", "tickets/*/messages")?.first {
            let index = try ticket(key, in: ws)
            let ids = call.body["attachment_ids"]?.array?.compactMap(\.string) ?? []
            let kind = MessageKind(rawValue: call.string("kind") ?? "reply") ?? .reply
            let message = send(kind: kind, html: call.string("body_html") ?? "", attachments: ids.compactMap { uploads[$0] }, on: index, in: &ws)
            if let status = call.string("status") {
                update(index, with: ["status": .string(status)], by: ActivityActor(type: "user", user: me), in: &ws)
            }
            return encode(message)
        }
        if let key = call.match("GET", "tickets/*/activities")?.first {
            let id = ws.tickets[try ticket(key, in: ws)].id
            return encode(ListBody(data: (ws.activities[id] ?? []).sorted { $0.createdAt < $1.createdAt }))
        }
        if call.match("POST", "attachments") != nil { return encode(upload(call)) }

        // Ollie on a ticket.
        if let key = call.match("GET", "tickets/*/ai/suggested-reply")?.first {
            let id = ws.tickets[try ticket(key, in: ws)].id
            let suggestion = ws.suggestedReplies[id].flatMap { $0.isPending ? $0 : nil }
            return encode(JSONValue.object(["suggested_reply": suggestion.map(DemoSeed.json) ?? .null]))
        }
        if let captured = call.match("POST", "tickets/*/ai/suggested-reply/*/send") {
            let index = try ticket(captured[0], in: ws)
            let id = ws.tickets[index].id
            guard let suggestion = ws.suggestedReplies[id], suggestion.id == captured[1], suggestion.isPending else {
                throw Failure(status: 409, code: "suggestion_not_pending", detail: "That suggestion was already handled.")
            }
            ws.suggestedReplies[id]?.status = "sent"
            return encode(send(kind: .reply, html: suggestion.bodyHTML, attachments: [], on: index, in: &ws))
        }
        if let captured = call.match("DELETE", "tickets/*/ai/suggested-reply/*") {
            let id = ws.tickets[try ticket(captured[0], in: ws)].id
            if ws.suggestedReplies[id]?.id == captured[1] { ws.suggestedReplies[id]?.status = "discarded" }
            return nil
        }
        if let captured = call.match("DELETE", "tickets/*/ai/suggestions/*") {
            let index = try ticket(captured[0], in: ws)
            if var suggestions = ws.tickets[index].custom["ai_suggestions"]?.object {
                suggestions[captured[1]] = nil
                ws.tickets[index].custom["ai_suggestions"] = .object(suggestions)
                touched(index, in: &ws)
            }
            return nil
        }
        if let key = call.match("POST", "tickets/*/ai/summary")?.first {
            let index = try ticket(key, in: ws)
            let summary = summarize(ws.tickets[index], in: ws)
            ws.tickets[index].custom["ai_summary"] = DemoSeed.json(summary)
            touched(index, in: &ws)
            return encode(summary)
        }
        if let key = call.match("POST", "tickets/*/ai/draft")?.first {
            let text = draft(for: ws.tickets[try ticket(key, in: ws)], instructions: call.string("instructions"))
            return encode(JSONValue.object(["body_html": .string(HTMLText.html(fromPlain: text)), "body_text": .string(text)]))
        }
        if let key = call.match("POST", "tickets/*/ai/triage")?.first {
            let index = try ticket(key, in: ws)
            let applied = triage(index, apply: call.body["apply"]?.bool ?? false, in: &ws)
            return encode(JSONValue.object(["applied": .array(applied.map(JSONValue.string))]))
        }

        // Notifications.
        if call.match("GET", "notifications") != nil {
            var items = ws.notifications.sorted { $0.createdAt > $1.createdAt }
            if query["unread"] == "true" { items = items.filter(\.isUnread) }
            let offset = Int(query["cursor"] ?? "") ?? 0
            let limit = max(1, Int(query["limit"] ?? "") ?? 50)
            let slice = Array(items.dropFirst(offset).prefix(limit))
            return encode(ListBody(data: slice, nextCursor: offset + slice.count < items.count ? String(offset + slice.count) : nil))
        }
        if call.match("POST", "notifications/read") != nil {
            let all = call.body["all"]?.bool ?? false
            let ids = Set(call.body["ids"]?.array?.compactMap(\.string) ?? [])
            let at = now()
            for index in ws.notifications.indices where ws.notifications[index].isUnread && (all || ids.contains(ws.notifications[index].id)) {
                ws.notifications[index].readAt = at
            }
            return nil
        }

        // Ollie, the assistant.
        if call.match("POST", "assistant/sessions") != nil {
            return encode(AssistantSession(id: made(), title: nil, createdAt: now(), updatedAt: nil))
        }
        if call.match("GET", "assistant/sessions") != nil { return encode(ListBody<AssistantSession>(data: [])) }

        throw Failure(status: 404, code: "not_found", detail: "The demo has no answer for \(call.method) /v1/\(call.path.joined(separator: "/")).")
    }

    // MARK: Plumbing

    var me: UserRef { UserRef(id: world.user.id, name: world.user.name, email: world.user.email, avatarURL: nil) }

    func encode(_ value: some Encodable) -> Data {
        (try? APICoding.encoder.encode(value)) ?? Data("{}".utf8)
    }

    func now() -> Date {
        tick += 1
        return DemoMode.anchor.addingTimeInterval(Double(tick))
    }

    func made() -> String {
        serial += 1
        return demoID(.made, serial)
    }

    func notFound(_ what: String) -> Failure {
        Failure(status: 404, code: "not_found", detail: "That \(what) isn't here any more.")
    }

    /// A ticket by id, or by number as links may name it.
    func ticket(_ key: String, in ws: DemoWorkspace) throws -> Int {
        let number = Int(key.trimmingCharacters(in: CharacterSet(charactersIn: "#")))
        guard let index = ws.tickets.firstIndex(where: { $0.id == key || $0.number == number }) else { throw notFound("ticket") }
        return index
    }

    func publish(_ type: String, _ data: [String: JSONValue], in ws: inout DemoWorkspace) {
        ws.seq += 1
        outbox.append(Envelope(demoID: made(), seq: ws.seq, workspaceID: ws.id, type: type, data: data, occurredAt: DemoMode.anchor.addingTimeInterval(Double(tick))))
    }

    /// Hands the events of the last change to realtime, after the answer is ready.
    func flush() {
        let events = outbox
        outbox = []
        guard let listener else { return }
        for envelope in events { listener(envelope) }
    }

    func touched(_ index: Int, in ws: inout DemoWorkspace, changes: [String] = []) {
        ws.tickets[index].updatedAt = now()
        publish("ticket.updated", ["ticket": DemoSeed.json(ws.tickets[index]), "changes": .array(changes.map(JSONValue.string))], in: &ws)
    }

    func createWorkspace(name: String) -> Data {
        let id = made()
        let slug = name.lowercased().split(whereSeparator: { !$0.isLetter && !$0.isNumber }).joined(separator: "-")
        let workspace = Workspace(id: id, name: name, slug: slug, avatarURL: nil, settings: WorkspaceSettings(brandColor: nil, timezone: DemoMode.timeZone, statusLabels: nil, priorityLabels: nil), createdAt: now())
        var ws = DemoWorkspace(workspace: workspace, role: .owner)
        ws.members = [Member(user: MemberUser(id: world.user.id, name: world.user.name, email: world.user.email, avatarURL: nil), role: .owner, availability: .online, presence: .online, openTickets: 0, lastSeenAt: DemoMode.anchor)]
        world.workspaces.append(ws)
        return encode(workspace)
    }

    // MARK: Lists

    func counts(_ ws: DemoWorkspace) -> JSONValue {
        let live = ws.tickets.filter { !$0.spam }
        let openish = live.filter { [.open, .pending, .onHold].contains($0.status) }
        var views: [String: JSONValue] = [:]
        for view in ws.views { views[view.id] = .number(Double(live.filter { matches($0, filters(of: view), in: ws) }.count)) }
        return .object([
            "unassigned": .number(Double(openish.filter { $0.assignee == nil }.count)),
            "mine": .number(Double(openish.filter { $0.assignee?.id == world.user.id }.count)),
            "open": .number(Double(live.filter { $0.status == .open }.count)),
            "pending": .number(Double(live.filter { $0.status == .pending }.count)),
            "on_hold": .number(Double(live.filter { $0.status == .onHold }.count)),
            "solved": .number(Double(live.filter(\.status.isDone).count)),
            "spam": .number(Double(ws.tickets.filter(\.spam).count)),
            "views": .object(views),
        ])
    }

    /// A saved view as list parameters.
    func filters(of view: SavedView) -> [String: String] {
        guard let filters = view.filters else { return [:] }
        var query: [String: String] = [:]
        if let status = filters.status { query["status"] = status.joined(separator: ",") }
        if let priority = filters.priority { query["priority"] = priority.joined(separator: ",") }
        if let severity = filters.severity { query["severity"] = severity.joined(separator: ",") }
        query["assignee"] = filters.assignee
        query["team_id"] = filters.teamID
        query["mailbox_id"] = filters.mailboxID
        query["tag"] = filters.tag
        query["channel"] = filters.channel
        query["q"] = filters.q
        return query
    }

    func matches(_ ticket: Ticket, _ query: [String: String], in ws: DemoWorkspace) -> Bool {
        if let id = query["view_id"] {
            guard let view = ws.views.first(where: { $0.id == id }) else { return false }
            var merged = filters(of: view)
            for (key, value) in query where key != "view_id" && !["sort", "order", "limit", "cursor"].contains(key) { merged[key] = value }
            return matches(ticket, merged, in: ws)
        }
        func list(_ key: String) -> Set<String>? {
            guard let raw = query[key], !raw.isEmpty else { return nil }
            return Set(raw.split(separator: ",").map(String.init))
        }
        if query["spam"] == "true" { guard ticket.spam else { return false } } else if ticket.spam { return false }
        if let statuses = list("status"), !statuses.contains(ticket.status.rawValue) { return false }
        if let priorities = list("priority"), !priorities.contains(ticket.priority.rawValue) { return false }
        if let severities = list("severity"), !severities.contains(ticket.severity?.rawValue ?? "") { return false }
        if let assignee = query["assignee"], !assignee.isEmpty {
            switch assignee {
            case "none": if ticket.assignee != nil { return false }
            case "me": if ticket.assignee?.id != world.user.id { return false }
            default: if ticket.assignee?.id != assignee { return false }
            }
        }
        if let team = query["team_id"], !team.isEmpty, ticket.teamID != team { return false }
        if let mailbox = query["mailbox_id"], !mailbox.isEmpty, ticket.mailboxID != mailbox { return false }
        if let channel = query["channel"], !channel.isEmpty, ticket.channel.rawValue != channel { return false }
        if let tag = query["tag"], !tag.isEmpty, !ticket.tags.contains(where: { $0.name.caseInsensitiveCompare(tag) == .orderedSame }) { return false }
        if let text = query["q"]?.trimmingCharacters(in: .whitespaces), !text.isEmpty {
            let haystack = [ticket.subject, ticket.contact?.name ?? "", ticket.contact?.email ?? "", ticket.lastMessagePreview, "#\(ticket.number)", String(ticket.number)]
            guard haystack.contains(where: { $0.localizedCaseInsensitiveContains(text) }) else { return false }
        }
        return true
    }

    func sorted(_ tickets: [Ticket], _ query: [String: String]) -> [Ticket] {
        let ascending = query["order"] == "asc"
        switch query["sort"] {
        case "priority":
            return tickets.sorted { $0.priority.rank != $1.priority.rank ? $0.priority.rank > $1.priority.rank : $0.lastMessageAt > $1.lastMessageAt }
        case "due":
            func due(_ ticket: Ticket) -> Date { SLAState.of(ticket)?.due ?? .distantFuture }
            return tickets.sorted { due($0) != due($1) ? due($0) < due($1) : $0.number > $1.number }
        default:
            return tickets.sorted { ascending ? $0.lastMessageAt < $1.lastMessageAt : $0.lastMessageAt > $1.lastMessageAt }
        }
    }

    // MARK: Changes

    /// `PATCH /v1/tickets/{id}`: applies the members present, records the activity and publishes `ticket.updated`.
    func update(_ index: Int, with body: [String: JSONValue], by actor: ActivityActor, in ws: inout DemoWorkspace) {
        var ticket = ws.tickets[index]
        let before = ticket
        var changes: [String] = []
        var activities: [Activity] = []
        func record(_ type: String, _ data: [String: JSONValue]) {
            activities.append(Activity(demoID: made(), ticketID: ticket.id, actor: actor, type: type, data: data, createdAt: now()))
        }
        func date(_ value: JSONValue?) -> Date? {
            guard let raw = value?.string else { return nil }
            return (try? Date.ISO8601FormatStyle(includingFractionalSeconds: true).parse(raw)) ?? (try? Date.ISO8601FormatStyle().parse(raw))
        }

        if let raw = body["status"]?.string, let status = TicketStatus(rawValue: raw), status != ticket.status {
            ticket.status = status
            changes.append("status")
        }
        if let raw = body["snoozed_until"] {
            ticket.snoozedUntil = date(raw)
            if ticket.snoozedUntil != nil, body["status"] == nil { ticket.status = .onHold }
            changes.append("snoozed_until")
        }
        if ticket.status != .onHold { ticket.snoozedUntil = nil }
        if ticket.status != before.status {
            ticket.solvedAt = ticket.status.isDone ? now() : nil
            if !changes.contains("status") { changes.append("status") }
            record("status_changed", ["from": .string(before.status.rawValue), "to": .string(ticket.status.rawValue)])
        }
        if let raw = body["priority"]?.string, let priority = Priority(rawValue: raw), priority != ticket.priority {
            ticket.priority = priority
            changes.append("priority")
            record("priority_changed", ["from": .string(before.priority.rawValue), "to": .string(priority.rawValue)])
        }
        if let raw = body["severity"] {
            let severity = raw.string.flatMap(Severity.init(rawValue:))
            if severity != ticket.severity {
                ticket.severity = severity
                changes.append("severity")
                record("severity_changed", ["from": before.severity.map { .string($0.rawValue) } ?? .null, "to": severity.map { .string($0.rawValue) } ?? .null])
            }
        }
        if let raw = body["assignee_id"] {
            let assignee = raw.string.flatMap { id in ws.members.first { $0.id == id }?.ref }
            if assignee?.id != ticket.assignee?.id {
                ticket.assignee = assignee
                changes.append("assignee")
                record("assigned", ["from": before.assignee.map(DemoSeed.json) ?? .null, "to": assignee.map(DemoSeed.json) ?? .null])
            }
        }
        if let raw = body["team_id"] {
            let team = raw.string.flatMap { id in ws.teams.first { $0.id == id } }
            if team?.id != ticket.teamID {
                let from = ws.teams.first { $0.id == ticket.teamID }?.name
                ticket.teamID = team?.id
                changes.append("team_id")
                record("team_changed", ["from": from.map(JSONValue.string) ?? .null, "to": team.map { .string($0.name) } ?? .null, "via": .string("agent")])
            }
        }
        if let spam = body["spam"]?.bool, spam != ticket.spam {
            ticket.spam = spam
            changes.append("spam")
        }
        var names = ticket.tags.map(\.name)
        if let replace = body["tags"]?.array?.compactMap(\.string) { names = replace }
        for name in body["add_tags"]?.array?.compactMap(\.string) ?? [] where !names.contains(where: { $0.caseInsensitiveCompare(name) == .orderedSame }) {
            names.append(name)
        }
        for name in body["remove_tags"]?.array?.compactMap(\.string) ?? [] {
            names.removeAll { $0.caseInsensitiveCompare(name) == .orderedSame }
        }
        if names != ticket.tags.map(\.name) {
            ticket.tags = names.map { tag(named: $0, in: &ws) }
            changes.append("tags")
            let old = Set(before.tags.map { $0.name.lowercased() })
            let new = Set(names.map { $0.lowercased() })
            for tag in ticket.tags where !old.contains(tag.name.lowercased()) { record("tagged", ["tag": .string(tag.name)]) }
            for tag in before.tags where !new.contains(tag.name.lowercased()) { record("untagged", ["tag": .string(tag.name)]) }
        }

        // Setting a field Ollie suggested answers the suggestion.
        if var suggestions = ticket.custom["ai_suggestions"]?.object {
            let answered: [String: String] = ["priority": "priority", "severity": "severity", "team": "team_id", "labels": "tags", "spam": "spam"]
            for (field, change) in answered where changes.contains(change) { suggestions[field] = nil }
            ticket.custom["ai_suggestions"] = .object(suggestions)
        }

        ws.tickets[index] = ticket
        ws.activities[ticket.id, default: []] += activities
        guard !changes.isEmpty else { return }
        touched(index, in: &ws, changes: changes)
        for activity in activities {
            publish("activity.created", ["ticket_id": .string(ticket.id), "activity": DemoSeed.json(activity)], in: &ws)
        }
    }

    func tag(named name: String, in ws: inout DemoWorkspace) -> TagRef {
        if let tag = ws.tags.first(where: { $0.name.caseInsensitiveCompare(name) == .orderedSame }) { return tag.ref }
        let tag = Tag(id: made(), name: name, color: "gray", description: nil, groupID: nil, archived: false, appliesTo: ["tickets"])
        ws.tags.append(tag)
        publish("tag.created", ["tag": DemoSeed.json(tag)], in: &ws)
        return tag.ref
    }

    /// A reply or note from Maya: stored, the ticket's clocks moved, `message.created` published.
    func send(kind: MessageKind, html: String, attachments: [Attachment], on index: Int, in ws: inout DemoWorkspace) -> Message {
        let ticket = ws.tickets[index]
        let at = now()
        var message = Message(demoID: made(), ticketID: ticket.id, kind: kind, html: html, createdAt: at)
        message.author = MessageAuthor(type: "user", user: me, contact: nil)
        message.subject = ticket.subject
        message.fromName = "\(world.user.name) at \(ws.workspace.name)"
        message.fromEmail = ticket.mailboxID == DemoSeed.billingMailbox ? "billing@margin.app" : "support@margin.app"
        message.to = kind == .reply && ticket.channel == .email ? [ticket.contact?.email].compactMap { $0 } : []
        message.deliveryStatus = kind == .reply ? "sent" : "none"
        message.attachments = attachments
        ws.messages[ticket.id, default: []].append(message)

        var next = ticket
        next.messageCount += 1
        if kind == .reply {
            next.lastMessageAt = at
            next.lastMessagePreview = DemoSeed.preview(html)
            next.waitingSince = nil
            if next.firstRespondedAt == nil { next.firstRespondedAt = at }
            if next.custom["ai_handoff"]?.bool == true { next.custom["ai_handoff"] = .bool(false) }
        }
        ws.tickets[index] = next
        publish("message.created", ["ticket_id": .string(ticket.id), "message": DemoSeed.json(message)], in: &ws)
        touched(index, in: &ws, changes: kind == .reply ? ["last_message_at"] : [])
        return message
    }

    func upload(_ call: Call) -> Attachment {
        let text = String(decoding: call.raw.prefix(4096), as: UTF8.self)
        func header(_ pattern: String) -> String? {
            guard let range = text.range(of: pattern, options: .regularExpression) else { return nil }
            return String(text[range])
        }
        let filename = header(#"(?<=filename=")[^"]*"#) ?? "attachment"
        let type = header(#"(?<=Content-Type: )[^\r\n]+"#) ?? "application/octet-stream"
        var content = Data()
        let separator = Data("\r\n\r\n".utf8)
        if let start = call.raw.range(of: separator)?.upperBound {
            content = call.raw[start...]
            if let end = content.range(of: Data("\r\n--".utf8), options: .backwards)?.lowerBound { content = content[..<end] }
        }
        let attachment = Attachment(demoID: made(), filename: filename, contentType: type, sizeBytes: content.count, url: "data:\(type);base64," + Data(content).base64EncodedString())
        uploads[attachment.id] = attachment
        return attachment
    }

    // MARK: Ollie on a ticket

    func summarize(_ ticket: Ticket, in ws: DemoWorkspace) -> AISummary {
        if let stored = ticket.custom["ai_summary"], var summary = try? stored.decode(AISummary.self) {
            summary.updatedAt = DemoMode.anchor
            return summary
        }
        let who = ticket.contact?.name ?? "The customer"
        let owner = ticket.assignee.map { "with \($0.name)" } ?? "and nobody has it yet"
        let firstMessage = (ws.messages[ticket.id] ?? []).first { $0.isInbound }.map { DemoSeed.preview($0.bodyHTML) } ?? ticket.lastMessagePreview
        return AISummary(
            text: "\(who) wrote in about \u{201C}\(ticket.subject)\u{201D}. \(firstMessage)",
            keyPoints: ["It is \(ticket.status.title.lowercased()), \(ticket.priority.title.lowercased()) priority, \(owner)", "Latest: \(ticket.lastMessagePreview)", ticket.assignee == nil ? "Next: assign it, then reply" : "Next: a reply from the assignee"],
            sentiment: Sentiment(rawValue: ticket.custom["ai_sentiment"]?.string ?? "") ?? .neutral,
            updatedAt: DemoMode.anchor
        )
    }

    func draft(for ticket: Ticket, instructions: String?) -> String {
        let first = ticket.contact?.name.split(separator: " ").first.map(String.init) ?? "there"
        let body: String
        switch ticket.number {
        case 1072:
            body = "Thanks for confirming. I've added the new certificate (4F:1A:9C:22:E0:7B:51:D3) to your SSO connection and kept the old one active until it expires next week, so nothing else needs to change on your side.\n\nCould you ask one person to try the Okta tile again and tell me it works?"
        case 1068:
            body = "Sorry for the silence. My earlier reply didn't reach you. The duplicate charge of €4,320 was refunded on June 12 and should be on your statement within 5 to 10 business days."
        case 1086:
            body = "I'm Maya from the Margin team. For 140 people we can offer the Business plan at $12 per member per month on a yearly contract, with SSO and a named contact for onboarding.\n\nWould a short call this week help? I can send a few times."
        default:
            body = "Thanks for writing in about \(ticket.subject.lowercased()). I'm looking into it now and will come back to you today with what I find."
        }
        let short = instructions?.localizedCaseInsensitiveContains("short") == true
        let text = short ? String(body.split(separator: "\n").first ?? "") : body
        return "Hi \(first),\n\n\(text)\n\n\(world.user.name.split(separator: " ").first.map(String.init) ?? "Maya")"
    }

    /// Ollie triages again; with `apply`, its pending suggestions are written as Ollie.
    func triage(_ index: Int, apply: Bool, in ws: inout DemoWorkspace) -> [String] {
        ws.tickets[index].custom["ai_triaged_at"] = .string(DemoSeed.stamp(DemoMode.anchor))
        guard apply, let suggestions = ws.tickets[index].custom["ai_suggestions"]?.object, !suggestions.isEmpty else {
            touched(index, in: &ws, changes: ["custom"])
            return []
        }
        var body: [String: JSONValue] = [:]
        for (field, entry) in suggestions {
            guard let value = entry["value"] else { continue }
            switch field {
            case "priority": body["priority"] = value
            case "severity": body["severity"] = value
            case "team": body["team_id"] = value
            case "labels": body["add_tags"] = value.array.map(JSONValue.array) ?? .array([value])
            case "spam": body["spam"] = value
            default: break
            }
        }
        update(index, with: body, by: ActivityActor(type: "ai", user: nil), in: &ws)
        return suggestions.keys.sorted()
    }

    // MARK: Ollie, the assistant

    struct Beat {
        var event: String
        var data: JSONValue
        var pause: Int
    }

    func words(_ text: String) -> [Beat] {
        var beats: [Beat] = []
        var chunk = ""
        for (index, word) in text.split(separator: " ", omittingEmptySubsequences: false).enumerated() {
            chunk += (index == 0 ? "" : " ") + word
            if index % 4 == 3 {
                beats.append(Beat(event: "text", data: .object(["text": .string(chunk)]), pause: 28))
                chunk = ""
            }
        }
        if !chunk.isEmpty { beats.append(Beat(event: "text", data: .object(["text": .string(chunk)]), pause: 28)) }
        return beats
    }

    func step(_ tool: String, running: String, done: String, summary: String?, refs: [AssistantRef] = []) -> [Beat] {
        let id = "call_" + made().suffix(8)
        let start = AssistantStep(id: id, tool: tool, label: running, status: "running", summary: nil, refs: nil, actionID: nil)
        let end = AssistantStep(id: id, tool: tool, label: done, status: "done", summary: summary, refs: refs.isEmpty ? nil : refs)
        return [Beat(event: "tool", data: DemoSeed.json(start), pause: 120), Beat(event: "tool", data: DemoSeed.json(end), pause: 550)]
    }

    func ref(_ ticket: Ticket) -> AssistantRef {
        AssistantRef(kind: "ticket", id: ticket.id, label: ticket.subject, number: ticket.number)
    }

    func assistantRun(_ call: Call, session: String) throws -> [Beat] {
        guard let wid = call.workspace, let index = world.workspaces.firstIndex(where: { $0.id == wid }) else {
            throw Failure(status: 400, code: "workspace_required", detail: "Pick a workspace first.")
        }
        let ws = world.workspaces[index]
        let text = (call.string("text") ?? "").lowercased()
        let open = ws.tickets.filter { !$0.spam && ($0.status == .open || $0.status == .pending) }.sorted { $0.lastMessageAt > $1.lastMessageAt }
        let context = call.body["context"]?["ticket_id"]?.string.flatMap { key in ws.tickets.first { $0.id == key || String($0.number) == key } }
        let current = context ?? open.first { $0.assignee == nil } ?? open.first
        var beats = [Beat(event: "session", data: DemoSeed.json(AssistantSession(id: session, title: call.string("text").map { String($0.prefix(48)) }, createdAt: DemoMode.anchor, updatedAt: DemoMode.anchor)), pause: 60)]

        if text.range(of: #"\b(who should|assign|take this|take it)\b"#, options: .regularExpression) != nil, let current {
            let available = ws.members.filter { $0.role != .viewer && $0.availability != .offline }
            let load: [(Member, Int)] = available.map { member in
                (member, open.filter { $0.assignee?.id == member.id }.count)
            }
            .sorted { (a: (Member, Int), b: (Member, Int)) -> Bool in a.1 != b.1 ? a.1 < b.1 : a.0.user.name < b.0.user.name }
            guard let pick = load.first else { return beats + words("Nobody is available to take it right now.") }
            beats += step("get_ticket", running: "Reading the ticket", done: "Read the ticket", summary: "#\(current.number)", refs: [ref(current)])
            beats += step("list_team", running: "Checking the team's workload", done: "Checked the team's workload", summary: "\(load.count) teammates")
            beats += words("**\(pick.0.user.name)** has the lightest load right now, with \(pick.1) open tickets. I can assign #\(current.number) to them and raise it to high, since the customer is waiting.")
            let action = made()
            let pendingStep = AssistantStep(id: "call_" + action.suffix(8), tool: "update_ticket", label: "Preparing a ticket change", status: "awaiting_approval", summary: nil, refs: nil, actionID: action)
            var changes = [PreviewChange(field: "Assignee", from: current.assignee?.name ?? "Unassigned", to: pick.0.user.name)]
            var patch: [String: JSONValue] = ["assignee_id": .string(pick.0.id)]
            if current.priority.rank < Priority.high.rank {
                changes.append(PreviewChange(field: "Priority", from: current.priority.title, to: "High"))
                patch["priority"] = .string("high")
            }
            pending[action] = PendingAction(workspace: wid, ticketIDs: [current.id], patch: patch, done: "#\(current.number) is now with **\(pick.0.user.name)**.", step: pendingStep)
            let approval = AssistantApproval(actionID: action, callID: pendingStep.id, tool: "update_ticket", preview: ApprovalPreview(title: "Update #\(current.number)", summary: current.subject, changes: changes, targets: [ref(current)]), token: "demo-" + action, expiresAt: DemoMode.anchor.addingTimeInterval(900))
            beats.append(Beat(event: "tool", data: DemoSeed.json(pendingStep), pause: 80))
            beats.append(Beat(event: "approval", data: DemoSeed.json(approval), pause: 80))
            return beats
        }
        if text.contains("label") || text.contains("tag") {
            let hits = open.filter { $0.subject.range(of: "bill|invoice|refund|charge|payment|pricing", options: [.regularExpression, .caseInsensitive]) != nil && !$0.tags.contains { $0.name == "Billing" } }
            beats += step("search_tickets", running: "Searching tickets", done: "Searched tickets", summary: "Found \(hits.count) tickets", refs: hits.map(ref))
            guard !hits.isEmpty else { return beats + words("Every open billing question already has the Billing label.") }
            beats += words("\(hits.count) open tickets look like billing questions without the Billing label. Here's the change:")
            let action = made()
            let pendingStep = AssistantStep(id: "call_" + action.suffix(8), tool: "bulk_update_tickets", label: "Preparing a bulk change", status: "awaiting_approval", summary: nil, refs: nil, actionID: action)
            pending[action] = PendingAction(workspace: wid, ticketIDs: hits.map(\.id), patch: ["add_tags": .array([.string("Billing")])], done: "Labeled \(hits.count) tickets **Billing**.", step: pendingStep)
            let approval = AssistantApproval(actionID: action, callID: pendingStep.id, tool: "bulk_update_tickets", preview: ApprovalPreview(title: "Label \(hits.count) tickets", summary: "Billing questions from this week", changes: [PreviewChange(field: "Labels", from: "", to: "+ Billing")], targets: hits.map(ref), total: hits.count), token: "demo-" + action, expiresAt: DemoMode.anchor.addingTimeInterval(900))
            beats.append(Beat(event: "tool", data: DemoSeed.json(pendingStep), pause: 80))
            beats.append(Beat(event: "approval", data: DemoSeed.json(approval), pause: 80))
            return beats
        }
        if text.contains("summar"), let current {
            let summary = summarize(current, in: ws)
            beats += step("get_ticket", running: "Reading the ticket", done: "Read the ticket", summary: "#\(current.number), \(current.messageCount) messages", refs: [ref(current)])
            return beats + words("\(summary.text)\n\n" + summary.keyPoints.map { "- \($0)" }.joined(separator: "\n"))
        }
        if text.range(of: #"\b(report|how are we|this week)\b"#, options: .regularExpression) != nil {
            beats += step("get_report", running: "Pulling the numbers", done: "Pulled the numbers", summary: "Last 7 days")
            let urgent = open.filter { $0.priority.rank >= Priority.high.rank }.count
            return beats + words("This week: **\(open.count) open** tickets, \(open.filter { $0.assignee == nil }.count) unassigned and \(urgent) high or urgent. Median first reply is 42 minutes, and CSAT is holding at 94%.")
        }
        let first = Array(open.filter { $0.priority.rank >= Priority.high.rank || $0.assignee == nil }.prefix(5))
        beats += step("search_tickets", running: "Searching tickets", done: "Searched tickets", summary: "Found \(first.count) tickets", refs: first.map(ref))
        let lines = first.map { "- #\($0.number) \($0.subject) (\($0.priority.rawValue)\($0.assignee.map { ", \($0.name)" } ?? ", unassigned"))" }
        return beats + words(first.isEmpty ? "Nothing urgent right now. The queue is in good shape." : "These need a look first:\n\n\(lines.joined(separator: "\n"))\n\nWant me to find owners for the unassigned ones?")
    }

    func assistantDecision(_ call: Call, action: String) throws -> [Beat] {
        guard let job = pending.removeValue(forKey: action), call.string("token") == "demo-" + action else {
            throw Failure(status: 409, code: "approval_not_pending", detail: "That action was already decided or expired.")
        }
        var step = job.step
        guard call.string("decision") == "approve" else {
            step.status = "denied"
            return [Beat(event: "tool", data: DemoSeed.json(step), pause: 80)] + words("Okay, I left everything as it was.")
        }
        if let index = world.workspaces.firstIndex(where: { $0.id == job.workspace }) {
            var ws = world.workspaces[index]
            for id in job.ticketIDs {
                if let ticket = ws.tickets.firstIndex(where: { $0.id == id }) {
                    update(ticket, with: job.patch, by: ActivityActor(type: "ai", user: nil), in: &ws)
                }
            }
            world.workspaces[index] = ws
        }
        step.status = "done"
        step.label = job.ticketIDs.count == 1 ? "Updated the ticket" : "Updated \(job.ticketIDs.count) tickets"
        return [Beat(event: "tool", data: DemoSeed.json(step), pause: 300)] + words("Done. \(job.done)")
    }
}
#endif
