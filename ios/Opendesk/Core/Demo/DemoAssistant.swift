#if DEBUG
import Foundation

// Ollie's routes beyond the core run and decision streams: past
// conversations with their transcripts, tips from the queue, and scripted
// runs for replies, notes, help articles, SLA and the weekly report.

/// One stored conversation, as `GET /v1/assistant/sessions/{id}` answers it.
struct DemoAssistantConversation: Sendable {
    var session: AssistantSession
    var items: [AssistantItem]
    var approval: AssistantApproval?
}

/// A change one of the scripted runs proposed, made when it is approved.
struct DemoAssistantAction: Sendable {
    enum Kind: Sendable { case reply, note, article }

    var kind: Kind
    var ticketID: String?
    var body: String
    var done: String
    var step: AssistantStep
}

/// Ollie's past conversations and tips.
struct DemoAssistantExtras: Sendable {
    var conversations: [DemoAssistantConversation] = []
    var actions: [String: DemoAssistantAction] = [:]
    /// The next run fails partway (`POST assistant/demo/fail-next`), to show the retry.
    var failNext = false

    static func seed(_ ws: DemoWorkspace) -> DemoAssistantExtras {
        var extras = DemoAssistantExtras()
        guard ws.id == DemoSeed.marginID else { return extras }
        func ticket(_ number: Int) -> AssistantRef? {
            ws.tickets.first { $0.number == number }.map { AssistantRef(kind: "ticket", id: $0.id, label: $0.subject, number: $0.number) }
        }
        func refs(_ numbers: [Int]) -> [AssistantRef] { numbers.compactMap(ticket) }
        func step(_ n: Int, _ tool: String, _ label: String, summary: String?, refs: [AssistantRef] = [], status: String = "done") -> AssistantStep {
            AssistantStep(id: "call_seed\(n)", tool: tool, label: label, status: status, summary: summary, refs: refs.isEmpty ? nil : refs)
        }
        func session(_ n: Int, _ title: String, minutesAgo: Double) -> AssistantSession {
            let at = DemoMode.anchor.addingTimeInterval(-minutesAgo * 60)
            return AssistantSession(id: demoID(.session, 900 + n), title: title, createdAt: at.addingTimeInterval(-120), updatedAt: at)
        }
        let inbox = AssistantPageContext(page: "inbox", path: "/inbox/mine", folder: "mine")

        extras.conversations = [
            DemoAssistantConversation(session: session(1, "What needs my attention right now?", minutesAgo: 42), items: [
                AssistantItem(role: "user", text: "What needs my attention right now? Look at SLA and urgent tickets.", context: inbox),
                AssistantItem(role: "assistant", text: "", steps: [step(1, "search_tickets", "Searched tickets", summary: "Found 4 tickets", refs: refs([1072, 1078, 1067, 1062]))]),
                AssistantItem(role: "assistant", text: """
                Four tickets need you first:

                1. #1078 **Two-factor codes not accepted** has breached its first reply SLA. Will is locked out.
                2. #1072 **SSO login loops back to sign-in page** is urgent and the customer confirmed the new certificate.
                3. #1067 **Mobile app crashes** is unassigned and breached.
                4. #1062 is due within the hour, a quick answer will do.

                Want me to find an owner for #1067?
                """),
            ]),
            DemoAssistantConversation(session: session(2, "Who should take the pricing chat?", minutesAgo: 60 * 26), items: [
                AssistantItem(role: "user", text: "Who should take #1086?"),
                AssistantItem(role: "assistant", text: "", steps: [
                    step(2, "get_ticket", "Read the ticket", summary: "#1086", refs: refs([1086])),
                    step(3, "list_team", "Checked the team's workload", summary: "5 teammates"),
                ]),
                AssistantItem(role: "assistant", text: "**Sam Okafor** has the lightest load right now, with 3 open tickets, and handled the last two pricing chats. I can assign #1086 to Sam.", steps: [
                    step(4, "update_ticket", "Updated the ticket", summary: "Assignee: Sam Okafor"),
                ]),
                AssistantItem(role: "assistant", text: "Done. #1086 is now with **Sam Okafor**."),
            ]),
            DemoAssistantConversation(session: session(3, "How are we doing this week?", minutesAgo: 60 * 24 * 3 + 30), items: [
                AssistantItem(role: "user", text: "How are we doing this week? Volume, response times, SLA and CSAT."),
                AssistantItem(role: "assistant", text: "", steps: [step(5, "get_report", "Pulled the numbers", summary: "Last 7 days")]),
                AssistantItem(role: "assistant", text: weeklyReport),
            ]),
            DemoAssistantConversation(session: session(4, "Draft a help article about SSO", minutesAgo: 60 * 24 * 8), items: [
                AssistantItem(role: "user", text: "Draft a help article from recently solved tickets about the most common question"),
                AssistantItem(role: "assistant", text: "", steps: [
                    step(6, "search_tickets", "Searched solved tickets", summary: "Found 9 about SSO", refs: refs([1049])),
                    step(7, "create_kb_draft", "Saved a help article draft", summary: "Fixing SSO sign-in loops"),
                ]),
                AssistantItem(role: "assistant", text: "Saved **Fixing SSO sign-in loops** as a draft in Getting started. It covers certificate rotation, the Okta tile and the redirect URL, the three causes in last month's tickets."),
            ]),
            DemoAssistantConversation(session: session(5, "Label this week's billing tickets", minutesAgo: 60 * 24 * 12), items: [
                AssistantItem(role: "user", text: "Label all billing tickets from this week"),
                AssistantItem(role: "assistant", text: "", steps: [
                    step(8, "search_tickets", "Searched tickets", summary: "Found 3 tickets", refs: refs([1058, 1075, 1081])),
                    step(9, "bulk_update_tickets", "Updated 3 tickets", summary: "+ Billing"),
                ]),
                AssistantItem(role: "assistant", text: "Done. Labeled 3 tickets **Billing**."),
            ]),
        ]
        return extras
    }

    static let weeklyReport = """
    ### This week at a glance

    **41 tickets** came in, 12% fewer than last week, and the team solved 38 of them.

    | Metric | This week | Last week |
    | --- | --- | --- |
    | First reply, median | 42m | 58m |
    | Resolution, median | 5h 10m | 6h 02m |
    | SLA met | 96% | 91% |
    | CSAT | 94% | 92% |

    - **First replies are faster** since SSO questions get a drafted answer
    - **Billing** is the busiest queue, with 14 tickets and 3 still open
    - #1072 and #1067 are the two urgent tickets left

    Want me to look at what is slowing down billing?
    """
}

extension DemoBackend {
    /// The answer (nil inside for 204), or nil when the route isn't this area's.
    func assistantRoutes(_ call: Call, in ws: inout DemoWorkspace) throws -> Data?? {
        if call.match("POST", "assistant/sessions") != nil {
            let session = AssistantSession(id: made(), title: call.string("title"), createdAt: now(), updatedAt: nil)
            ws.assistantExtras.conversations.insert(DemoAssistantConversation(session: session, items: []), at: 0)
            return .some(encode(session))
        }
        if call.match("GET", "assistant/sessions") != nil {
            let limit = Int(call.query["limit"] ?? "") ?? 50
            let sessions = ws.assistantExtras.conversations
                .filter { !$0.items.isEmpty }
                .map(\.session)
                .sorted { ($0.updatedAt ?? $0.createdAt ?? .distantPast) > ($1.updatedAt ?? $1.createdAt ?? .distantPast) }
            return .some(encode(ListBody(data: Array(sessions.prefix(limit)))))
        }
        if let id = call.match("GET", "assistant/sessions/*")?.first {
            guard let conversation = ws.assistantExtras.conversations.first(where: { $0.session.id == id }) else { throw notFound("conversation") }
            return .some(encode(JSONValue.object([
                "session": DemoSeed.json(conversation.session),
                "items": DemoSeed.json(conversation.items),
                "approval": conversation.approval.map(DemoSeed.json) ?? .null,
            ])))
        }
        if let id = call.match("PATCH", "assistant/sessions/*")?.first {
            guard let index = ws.assistantExtras.conversations.firstIndex(where: { $0.session.id == id }) else { throw notFound("conversation") }
            let title = (call.string("title") ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
            guard !title.isEmpty else { throw Failure(status: 422, code: "invalid", detail: "Give the conversation a name.") }
            ws.assistantExtras.conversations[index].session.title = String(title.prefix(120))
            return .some(encode(ws.assistantExtras.conversations[index].session))
        }
        if let id = call.match("DELETE", "assistant/sessions/*")?.first {
            ws.assistantExtras.conversations.removeAll { $0.session.id == id }
            return .some(nil)
        }
        if call.match("GET", "assistant/tips") != nil {
            return .some(encode(AssistantTips(tips: tips(in: ws))))
        }
        if call.match("POST", "assistant/demo/fail-next") != nil {
            ws.assistantExtras.failNext = true
            return .some(nil)
        }
        return nil
    }

    /// Counts from the queue, never the model (contracts/ai.md tips).
    func tips(in ws: DemoWorkspace) -> [AssistantTip] {
        let open = ws.tickets.filter { !$0.spam && !$0.status.isDone }
        var tips: [AssistantTip] = []
        let due = open.filter { SLAState.of($0)?.kind == .dueSoon || SLAState.of($0)?.kind == .breached }.count
        if due > 0 {
            tips.append(AssistantTip(key: "sla_due_soon", text: due == 1 ? "1 ticket breaches SLA within the hour." : "\(due) tickets breach SLA within the hour or already have.", prompt: AssistantPrompt.breach.prompt, severity: "high", count: due))
        }
        let old = open.filter { $0.assignee == nil && DemoMode.anchor.timeIntervalSince($0.createdAt) > 86400 }.count
        if old > 0 {
            tips.append(AssistantTip(key: "unassigned_old", text: "\(Format.plural(old, "unassigned ticket")) waited more than a day.", prompt: AssistantPrompt.unassigned.prompt, severity: "medium", count: old))
        }
        let waiting = open.filter { $0.assignee?.id == world.user.id && $0.waitingSince != nil }.count
        if waiting > 0 {
            tips.append(AssistantTip(key: "mine_waiting", text: waiting == 1 ? "1 of your tickets is waiting on you." : "\(waiting) of your tickets are waiting on you.", prompt: AssistantPrompt.mine.prompt, severity: "low", count: waiting))
        }
        return tips
    }
}
#endif

#if DEBUG
extension DemoBackend {
    /// Streams (`text/event-stream`) beyond the assistant run and decision: nil when not one.
    /// Ollie's runs are answered here first, for the scripted ones, and recorded either way
    /// so the history opens them again.
    func streamRoutes(_ call: Call) throws -> [Beat]? {
        guard let wid = call.workspace, let index = world.workspaces.firstIndex(where: { $0.id == wid }) else { return nil }
        if let session = call.match("POST", "assistant/sessions/*/messages")?.first {
            let beats: [Beat]
            if world.workspaces[index].assistantExtras.failNext {
                world.workspaces[index].assistantExtras.failNext = false
                beats = [sessionBeat(call, session: session, in: index)]
                    + step("search_tickets", running: "Searching tickets", done: "Searched tickets", summary: "Found 3 tickets")
                    + [Beat(event: "error", data: .object(["status": .number(503), "code": .string("ai_busy"), "title": .string("ai_busy"), "detail": .string("Ollie is busy right now. Try again in a moment.")]), pause: 500)]
            } else if let scripted = try scriptedRun(call, session: session, in: index) {
                beats = scripted
            } else {
                beats = try assistantRun(call, session: session)
            }
            record(session: session, question: call.string("text") ?? "", context: call.body["context"], beats: beats, in: index)
            return beats
        }
        if let action = call.match("POST", "assistant/actions/*/decision")?.first {
            let beats = try scriptedDecision(call, action: action, in: index) ?? assistantDecision(call, action: action)
            recordDecision(action: action, beats: beats, in: index)
            return beats
        }
        return nil
    }

    private func sessionBeat(_ call: Call, session: String, in index: Int) -> Beat {
        let stored = world.workspaces[index].assistantExtras.conversations.first { $0.session.id == session }?.session
        let title = stored?.title ?? call.string("text").map { String($0.prefix(48)) }
        return Beat(event: "session", data: DemoSeed.json(AssistantSession(id: session, title: title, createdAt: stored?.createdAt ?? DemoMode.anchor, updatedAt: DemoMode.anchor)), pause: 60)
    }

    /// Replies, notes, help articles, SLA, "waiting on me" and the weekly report.
    private func scriptedRun(_ call: Call, session: String, in index: Int) throws -> [Beat]? {
        let ws = world.workspaces[index]
        let text = (call.string("text") ?? "").lowercased()
        let open = ws.tickets.filter { !$0.spam && !$0.status.isDone }.sorted { $0.lastMessageAt > $1.lastMessageAt }
        let context = call.body["context"]?["ticket_id"]?.string.flatMap { key in ws.tickets.first { $0.id == key || String($0.number) == key } }
        let current = context ?? open.first { $0.priority == .urgent } ?? open.first
        var beats = [sessionBeat(call, session: session, in: index)]

        if text.contains("draft a reply") || text.contains("reply to"), let current {
            let who = current.contact?.name ?? "the customer"
            beats += step("get_ticket", running: "Reading the ticket", done: "Read the ticket", summary: "#\(current.number), \(current.messageCount) messages", refs: [ref(current)])
            beats += step("search_kb", running: "Searching the knowledge base", done: "Searched the knowledge base", summary: "2 articles")
            beats += words("Here's a reply for \(who.split(separator: " ").first.map(String.init) ?? who). It covers what they asked and the next step. Nothing is sent until you approve it.")
            let body = draft(for: current, instructions: nil)
            return beats + propose(.reply, tool: "draft_reply", label: "Preparing a reply", ticket: current, body: body, done: "Sent the reply to **\(who)** on #\(current.number).", preview: ApprovalPreview(
                title: "Reply to \(who)",
                summary: "#\(current.number) \(current.subject)",
                changes: current.status == .open ? [PreviewChange(field: "Status", from: "Open", to: "Pending")] : nil,
                body: body,
                targets: [ref(current)],
                external: true
            ), in: index)
        }
        if text.contains("note"), let current {
            beats += step("get_ticket", running: "Reading the ticket", done: "Read the ticket", summary: "#\(current.number)", refs: [ref(current)])
            let summary = summarize(current, in: ws)
            let body = "**Where it stands:** \(summary.text)\n\n" + summary.keyPoints.map { "- \($0)" }.joined(separator: "\n")
            beats += words("I wrote up what we know so far as an internal note. Only your team sees it.")
            return beats + propose(.note, tool: "add_note", label: "Preparing a note", ticket: current, body: body, done: "Added the note to #\(current.number).", preview: ApprovalPreview(
                title: "Add a note to #\(current.number)",
                summary: current.subject,
                body: body,
                targets: [ref(current)]
            ), in: index)
        }
        if text.contains("help article") || text.contains("kb") {
            let solved = ws.tickets.filter { $0.status.isDone && !$0.spam }.prefix(3)
            beats += step("search_tickets", running: "Searching solved tickets", done: "Searched solved tickets", summary: "Found 9 about SSO", refs: solved.map(ref))
            beats += step("search_kb", running: "Checking existing articles", done: "Checked existing articles", summary: "No article covers it")
            beats += words("SSO sign-in loops came up 9 times last month, and no article covers them yet. Here's a draft for Getting started:")
            let body = """
            ## Fixing SSO sign-in loops

            If people land back on the sign-in page after choosing your identity provider, one of these is usually the cause.

            1. **The signing certificate changed.** Upload the new certificate in Settings, Security, and keep the old one until it expires.
            2. **The redirect URL is different.** It must be `https://app.opendesk.sh/auth/sso/callback`.
            3. **The app tile is out of date.** Remove and add the Okta tile again.
            """
            return beats + propose(.article, tool: "create_kb_draft", label: "Preparing a help article", ticket: nil, body: body, done: "Saved **Fixing SSO sign-in loops** as a draft.", preview: ApprovalPreview(
                title: "Save a help article draft",
                summary: "Getting started",
                changes: [PreviewChange(field: "Title", from: "", to: "Fixing SSO sign-in loops"), PreviewChange(field: "Status", from: "", to: "Draft")],
                body: body
            ), in: index)
        }
        if text.contains("waiting on me") || text.contains("my tickets") {
            let mine = open.filter { $0.assignee?.id == world.user.id && $0.waitingSince != nil }
            beats += step("search_tickets", running: "Searching your tickets", done: "Searched your tickets", summary: "\(mine.count) waiting on you", refs: mine.map(ref))
            guard !mine.isEmpty else { return beats + words("Nothing is waiting on you. Nice.") }
            let lines = mine.prefix(5).map { "- #\($0.number) \($0.subject), from \($0.contact?.name ?? "a customer")" }
            return beats + words("These customers are waiting on your reply, oldest first:\n\n\(lines.joined(separator: "\n"))")
        }
        if text.range(of: #"\b(report|how are we|this week)\b"#, options: .regularExpression) != nil, !text.contains("label") {
            beats += step("get_report", running: "Pulling the numbers", done: "Pulled the numbers", summary: "Last 7 days")
            beats += step("get_report", running: "Comparing with last week", done: "Compared with last week", summary: "SLA and CSAT")
            return beats + words(DemoAssistantExtras.weeklyReport)
        }
        if text.contains("breach") {
            let due = open.filter { SLAState.of($0).map { $0.kind != .onTrack } ?? false }.sorted { (SLAState.of($0)?.due ?? .distantFuture) < (SLAState.of($1)?.due ?? .distantFuture) }
            beats += step("search_tickets", running: "Checking SLA clocks", done: "Checked SLA clocks", summary: "\(due.count) due or overdue", refs: due.map(ref))
            guard !due.isEmpty else { return beats + words("Nothing breaches SLA in the next hour. Every clock is on track.") }
            let lines = due.prefix(5).map { ticket -> String in
                let state = SLAState.of(ticket)
                return "- #\(ticket.number) \(ticket.subject): **\(state?.label() ?? "due soon")**, \(ticket.assignee?.name ?? "unassigned")"
            }
            return beats + words("\(Format.plural(due.count, "ticket")) need an answer before their SLA slips:\n\n\(lines.joined(separator: "\n"))\n\nThe overdue ones first. Want me to draft replies?")
        }
        return nil
    }

    private func propose(_ kind: DemoAssistantAction.Kind, tool: String, label: String, ticket: Ticket?, body: String, done: String, preview: ApprovalPreview, in index: Int) -> [Beat] {
        let action = made()
        let pendingStep = AssistantStep(id: "call_" + action.suffix(8), tool: tool, label: label, status: "awaiting_approval", summary: nil, refs: nil, actionID: action)
        world.workspaces[index].assistantExtras.actions[action] = DemoAssistantAction(kind: kind, ticketID: ticket?.id, body: body, done: done, step: pendingStep)
        let approval = AssistantApproval(actionID: action, callID: pendingStep.id, tool: tool, preview: preview, token: "demo-" + action, expiresAt: DemoMode.anchor.addingTimeInterval(900))
        return [Beat(event: "tool", data: DemoSeed.json(pendingStep), pause: 80), Beat(event: "approval", data: DemoSeed.json(approval), pause: 80)]
    }

    private func scriptedDecision(_ call: Call, action: String, in index: Int) throws -> [Beat]? {
        guard let job = world.workspaces[index].assistantExtras.actions[action] else { return nil }
        guard call.string("token") == "demo-" + action else { throw Failure(status: 403, code: "approval_invalid", detail: "That approval isn't valid any more.") }
        world.workspaces[index].assistantExtras.actions[action] = nil
        var step = job.step
        guard call.string("decision") == "approve" else {
            step.status = "denied"
            return [Beat(event: "tool", data: DemoSeed.json(step), pause: 80)] + words("Okay, I left everything as it was.")
        }
        var ws = world.workspaces[index]
        switch job.kind {
        case .reply, .note:
            if let id = job.ticketID, let ticket = ws.tickets.firstIndex(where: { $0.id == id }) {
                _ = send(kind: job.kind == .reply ? .reply : .note, html: HTMLText.html(fromPlain: job.body), attachments: [], on: ticket, in: &ws)
                if job.kind == .reply, ws.tickets[ticket].status == .open {
                    update(ticket, with: ["status": .string("pending")], by: ActivityActor(type: "ai", user: me), in: &ws)
                }
            }
            step.label = job.kind == .reply ? "Sent the reply" : "Added the note"
        case .article:
            step.label = "Saved the draft"
        }
        world.workspaces[index] = ws
        step.status = "done"
        return [Beat(event: "tool", data: DemoSeed.json(step), pause: 380)] + words("Done. \(job.done)")
    }

    // MARK: Recording

    /// What a stream said, as transcript pieces: the text, each step's last state, the approval.
    private func fold(_ beats: [Beat]) -> (text: String, steps: [AssistantStep], approval: AssistantApproval?) {
        var text = ""
        var steps: [AssistantStep] = []
        var approval: AssistantApproval?
        for beat in beats {
            switch beat.event {
            case "text": text += beat.data["text"]?.string ?? ""
            case "tool":
                guard let step = try? beat.data.decode(AssistantStep.self) else { continue }
                if let at = steps.firstIndex(where: { $0.id == step.id }) { steps[at] = step } else { steps.append(step) }
            case "approval": approval = try? beat.data.decode(AssistantApproval.self)
            default: break
            }
        }
        return (text, steps, approval)
    }

    private func record(session: String, question: String, context: JSONValue?, beats: [Beat], in index: Int) {
        var conversations = world.workspaces[index].assistantExtras.conversations
        guard let at = conversations.firstIndex(where: { $0.session.id == session }) else { return }
        let (text, steps, approval) = fold(beats)
        let pageContext = context.flatMap { try? $0.decode(AssistantPageContext.self) }
        conversations[at].items.append(AssistantItem(role: "user", text: question, context: pageContext))
        if !text.isEmpty || !steps.isEmpty { conversations[at].items.append(AssistantItem(role: "assistant", text: text, steps: steps)) }
        conversations[at].approval = approval
        if conversations[at].session.title?.isEmpty ?? true { conversations[at].session.title = String(question.prefix(48)) }
        conversations[at].session.updatedAt = now()
        world.workspaces[index].assistantExtras.conversations = conversations
    }

    private func recordDecision(action: String, beats: [Beat], in index: Int) {
        var conversations = world.workspaces[index].assistantExtras.conversations
        guard let at = conversations.firstIndex(where: { $0.approval?.actionID == action }) else { return }
        let (text, steps, _) = fold(beats)
        var fresh: [AssistantStep] = []
        for step in steps {
            // The proposal's own step changes where it was; anything new follows.
            if let item = conversations[at].items.lastIndex(where: { $0.steps?.contains { $0.id == step.id } == true }),
               let position = conversations[at].items[item].steps?.firstIndex(where: { $0.id == step.id }) {
                conversations[at].items[item].steps?[position] = step
            } else {
                fresh.append(step)
            }
        }
        if !text.isEmpty || !fresh.isEmpty { conversations[at].items.append(AssistantItem(role: "assistant", text: text, steps: fresh)) }
        conversations[at].approval = nil
        conversations[at].session.updatedAt = now()
        world.workspaces[index].assistantExtras.conversations = conversations
    }
}
#endif
