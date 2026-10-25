#if DEBUG
import Foundation

// The inbox's extra routes: the list's full filters and sorts, team counts, new tickets,
// bulk changes, the addresses, and search (by words and by meaning).

/// Data for new tickets, bulk changes and search.
struct DemoInboxExtras: Sendable {
    /// Contacts made by new tickets in this run.
    var contacts: [Contact] = []
    /// Help center articles search finds (the knowledge base area serves the articles themselves).
    var articles: [SearchArticle] = []

    static func seed(_ ws: DemoWorkspace) -> DemoInboxExtras {
        var extras = DemoInboxExtras()
        guard ws.tickets.contains(where: { $0.mailboxID == DemoSeed.supportMailbox }) else { return extras }
        extras.articles = [
            SearchArticle(id: demoID(.made, 9101), title: "Request a refund", excerpt: "How refunds work for annual and monthly plans, and what happens after a duplicate charge."),
            SearchArticle(id: demoID(.made, 9102), title: "Reset your password", excerpt: "Sign-in links, reset emails that don't arrive, and what to try when a reset link has expired."),
            SearchArticle(id: demoID(.made, 9103), title: "Set up a custom domain", excerpt: "Point your publication at your own domain with a CNAME record. Certificates are issued for you."),
            SearchArticle(id: demoID(.made, 9104), title: "Export your data", excerpt: "Download your posts, subscribers and analytics as CSV or JSON, any time."),
            SearchArticle(id: demoID(.made, 9105), title: "Invoices, VAT and billing details", excerpt: "Change the billing email, add a VAT number, and download past invoices."),
            SearchArticle(id: demoID(.made, 9106), title: "Two-step sign-in", excerpt: "Turn on an authenticator app or a passkey, and use recovery codes if you lose your phone."),
            SearchArticle(id: demoID(.made, 9107), title: "Change your plan", excerpt: "Upgrade or downgrade between Team and Business. Changes are prorated to the day.", status: "draft"),
        ]
        return extras
    }
}

extension DemoBackend {
    /// The answer (nil inside for 204), or nil when the route isn't this area's.
    func inboxRoutes(_ call: Call, in ws: inout DemoWorkspace) throws -> Data?? {
        let query = call.query
        if call.match("GET", "tickets/counts") != nil { return .some(encode(inboxCounts(ws))) }
        if call.match("GET", "tickets") != nil {
            let matching = inboxSorted(ws.tickets.filter { inboxMatches($0, query, in: ws) }, query)
            let offset = Int(query["cursor"] ?? "") ?? 0
            let limit = max(1, Int(query["limit"] ?? "") ?? 50)
            let slice = Array(matching.dropFirst(offset).prefix(limit))
            let next = offset + slice.count < matching.count ? String(offset + slice.count) : nil
            return .some(encode(ListBody(data: slice, nextCursor: next)))
        }
        if call.match("GET", "mailboxes") != nil { return .some(encode(ListBody(data: inboxMailboxes(ws)))) }
        if call.match("GET", "search") != nil { return .some(encode(inboxSearch(query["q"] ?? "", in: ws))) }
        if call.match("GET", "ai/search") != nil { return .some(encode(inboxMeaningSearch(query["q"] ?? "", in: ws))) }
        if call.match("POST", "tickets") != nil { return .some(encode(try inboxCreate(call.body, in: &ws))) }
        if call.match("POST", "tickets/bulk") != nil {
            try inboxBulk(call.body, in: &ws)
            return .some(nil)
        }
        return nil
    }

    // MARK: List

    private func inboxCounts(_ ws: DemoWorkspace) -> JSONValue {
        guard case var .object(base) = counts(ws) else { return counts(ws) }
        let mine = Set(ws.teams.filter { $0.memberIDs?.contains(world.user.id) == true }.map(\.id))
        let live = ws.tickets.filter { !$0.spam }
        func count(_ tickets: [Ticket]) -> JSONValue {
            .object([
                "open": .number(Double(tickets.filter { $0.status == .open }.count)),
                "unassigned": .number(Double(tickets.filter { ($0.status == .open || $0.status == .pending) && $0.assignee == nil }.count)),
            ])
        }
        var teams: [String: JSONValue] = [:]
        for team in ws.teams { teams[team.id] = count(live.filter { $0.teamID == team.id }) }
        base["teams"] = .object(teams)
        base["my_teams"] = count(live.filter { $0.teamID.map(mine.contains) ?? false })
        return .object(base)
    }

    /// `GET /v1/tickets` with what the shared matcher leaves out: team lists (`mine`, `none`),
    /// severity `none`, the address written to, and custom fields.
    private func inboxMatches(_ ticket: Ticket, _ query: [String: String], in ws: DemoWorkspace) -> Bool {
        var rest = query
        if let raw = rest.removeValue(forKey: "team_id"), !raw.isEmpty {
            let mine = Set(ws.teams.filter { $0.memberIDs?.contains(world.user.id) == true }.map(\.id))
            let hit = raw.split(separator: ",").map(String.init).contains { value in
                switch value {
                case "none": ticket.teamID == nil
                case "mine": ticket.teamID.map(mine.contains) ?? false
                default: ticket.teamID == value
                }
            }
            if !hit { return false }
        }
        if let raw = rest.removeValue(forKey: "severity"), !raw.isEmpty {
            if !raw.split(separator: ",").map(String.init).contains(ticket.severity?.rawValue ?? "none") { return false }
        }
        if let address = rest.removeValue(forKey: "received_at"), !address.isEmpty {
            let mailbox = inboxMailboxes(ws).first { $0["id"]?.string == ticket.mailboxID }
            let written = mailbox?["email"]?.string ?? ""
            if written.caseInsensitiveCompare(address) != .orderedSame { return false }
        }
        for (key, value) in query where key.hasPrefix("field.") {
            rest[key] = nil
            let name = key.dropFirst(6).split(separator: "[").first.map(String.init) ?? ""
            let op = key.split(separator: "[").dropFirst().first.map { String($0.dropLast()) } ?? "eq"
            let stored = ticket.fields[name]
            let text = stored?.string ?? stored.map { "\($0)" } ?? ""
            switch op {
            case "any": if !value.split(separator: ",").map(String.init).contains(text) { return false }
            case "contains": if !text.localizedCaseInsensitiveContains(value) { return false }
            case "set": if stored == nil { return false }
            case "not_set": if stored != nil { return false }
            default: if text != value { return false }
            }
        }
        return matches(ticket, rest, in: ws)
    }

    private func inboxSorted(_ tickets: [Ticket], _ query: [String: String]) -> [Ticket] {
        let ascending = query["order"] == "asc"
        func ordered<T: Comparable>(_ a: T, _ b: T) -> Bool { ascending ? a < b : a > b }
        func severity(_ ticket: Ticket) -> Int { ticket.severity.map { 5 - (Severity.allCases.firstIndex(of: $0) ?? 4) } ?? 0 }
        switch query["sort"] ?? "" {
        case "created_at":
            return tickets.sorted { ordered($0.createdAt, $1.createdAt) }
        case "priority":
            return tickets.sorted { $0.priority.rank != $1.priority.rank ? ordered($0.priority.rank, $1.priority.rank) : $0.lastMessageAt > $1.lastMessageAt }
        case "severity":
            return tickets.sorted { severity($0) != severity($1) ? ordered(severity($0), severity($1)) : $0.lastMessageAt > $1.lastMessageAt }
        case "due":
            func due(_ ticket: Ticket) -> Date { SLAState.of(ticket)?.due ?? .distantFuture }
            // Tickets without an SLA stay last either way.
            let latestFirst = query["order"] == "desc"
            return tickets.sorted {
                let a = due($0), b = due($1)
                if a == b { return $0.number > $1.number }
                if a == .distantFuture { return false }
                if b == .distantFuture { return true }
                return latestFirst ? a > b : a < b
            }
        default:
            return tickets.sorted { ordered($0.lastMessageAt, $1.lastMessageAt) }
        }
    }

    private func inboxMailboxes(_ ws: DemoWorkspace) -> [JSONValue] {
        let used = Set(ws.tickets.compactMap(\.mailboxID))
        guard used.contains(DemoSeed.supportMailbox) || used.contains(DemoSeed.billingMailbox) else { return [] }
        func mailbox(_ id: String, _ name: String, _ email: String, _ team: String) -> JSONValue {
            .object([
                "id": .string(id), "name": .string(name), "email": .string(email), "team_id": .string(team),
                "status": .string("active"), "sending": .string("ok"), "private": .bool(false),
            ])
        }
        return [
            mailbox(DemoSeed.supportMailbox, "Support", "support@margin.app", DemoSeed.support),
            mailbox(DemoSeed.billingMailbox, "Billing", "billing@margin.app", DemoSeed.billing),
        ]
    }

    // MARK: Search

    /// Everyone tickets name, plus contacts made in this run.
    private func inboxContacts(_ ws: DemoWorkspace) -> [Contact] {
        var seen: [String: Contact] = [:]
        for contact in ws.inboxExtras.contacts { seen[contact.id] = contact }
        for ticket in ws.tickets.sorted(by: { $0.createdAt < $1.createdAt }) {
            guard let ref = ticket.contact else { continue }
            if var known = seen[ref.id] {
                known.ticketCount += 1
                seen[ref.id] = known
            } else {
                seen[ref.id] = Contact(id: ref.id, email: ref.email, name: ref.name, avatarURL: ref.avatarURL, ticketCount: 1, createdAt: ticket.createdAt)
            }
        }
        return seen.values.sorted { $0.displayName.localizedCaseInsensitiveCompare($1.displayName) == .orderedAscending }
    }

    private func inboxSearch(_ raw: String, in ws: DemoWorkspace) -> JSONValue {
        let text = raw.trimmingCharacters(in: .whitespaces)
        guard !text.isEmpty else { return .object(["tickets": .array([]), "contacts": .array([]), "articles": .array([])]) }
        let tickets = ws.tickets.filter { !$0.spam && matches($0, ["q": text], in: ws) }
            .sorted { $0.lastMessageAt > $1.lastMessageAt }
        let contacts = inboxContacts(ws).filter { ($0.name + " " + ($0.email ?? "")).localizedCaseInsensitiveContains(text) }
        let articles = ws.inboxExtras.articles.filter { ($0.title + " " + $0.excerpt).localizedCaseInsensitiveContains(text) }
        return .object([
            "tickets": DemoSeed.json(Array(tickets.prefix(5))),
            "contacts": DemoSeed.json(Array(contacts.prefix(5))),
            "articles": DemoSeed.json(Array(articles.prefix(5))),
        ])
    }

    /// Search by meaning: the words of the question and their neighbours, scored against
    /// what each ticket and article is about.
    private func inboxMeaningSearch(_ raw: String, in ws: DemoWorkspace) -> JSONValue {
        let neighbours: [String: [String]] = [
            "charged": ["charge", "refund", "billing", "payment", "invoice", "duplicate"],
            "twice": ["duplicate", "double", "refund"],
            "money": ["refund", "charge", "payment", "billing"],
            "sign": ["login", "password", "reset", "2fa", "two-step", "locked", "sso"],
            "login": ["sign", "password", "reset", "locked", "sso"],
            "password": ["reset", "sign", "login", "locked"],
            "export": ["download", "csv", "data", "backup", "migrate"],
            "data": ["export", "csv", "download", "gdpr", "delete"],
            "domain": ["dns", "cname", "custom", "certificate"],
            "slow": ["performance", "timeout", "loading", "lag"],
            "price": ["pricing", "plan", "upgrade", "seats", "discount"],
            "cancel": ["refund", "downgrade", "subscription"],
        ]
        let stop: Set<String> = ["the", "a", "an", "who", "were", "was", "to", "for", "of", "after", "with", "and", "or", "my", "is", "are", "requests", "customers", "problems"]
        let words = raw.lowercased().split(whereSeparator: { !$0.isLetter && !$0.isNumber }).map(String.init).filter { $0.count > 2 && !stop.contains($0) }
        var terms: [String: Double] = [:]
        for word in words {
            let stem = word.hasSuffix("s") ? String(word.dropLast()) : word
            terms[stem, default: 0] += 2
            for near in neighbours[word] ?? neighbours[stem] ?? [] { terms[near, default: 0] += 1 }
        }
        func score(_ haystack: String) -> Double {
            let text = haystack.lowercased()
            return terms.reduce(0) { $0 + (text.contains($1.key) ? $1.value : 0) }
        }
        func about(_ ticket: Ticket) -> String {
            let labels = ticket.tags.map(\.name).joined(separator: " ")
            return ticket.subject + " " + ticket.lastMessagePreview + " " + labels
        }
        var scored: [(ticket: Ticket, score: Double)] = []
        for ticket in ws.tickets where !ticket.spam {
            let value = score(about(ticket))
            if value >= 2 { scored.append((ticket, value)) }
        }
        scored.sort { a, b in a.score != b.score ? a.score > b.score : a.ticket.lastMessageAt > b.ticket.lastMessageAt }
        let tickets = scored.map(\.ticket)
        let articles = ws.inboxExtras.articles
            .map { ($0, score($0.title + " " + $0.excerpt)) }
            .filter { $0.1 >= 2 }
            .sorted { $0.1 > $1.1 }
            .map(\.0)
        return .object([
            "tickets": DemoSeed.json(Array(tickets.prefix(5))),
            "contacts": .array([]),
            "articles": DemoSeed.json(Array(articles.prefix(3))),
        ])
    }

    // MARK: Changes

    /// `POST /v1/tickets`: the ticket, its first message (a reply sent from the address, or a note), and the events.
    private func inboxCreate(_ body: JSONValue, in ws: inout DemoWorkspace) throws -> Ticket {
        let subject = (body["subject"]?.string ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        guard !subject.isEmpty else { throw Failure(status: 422, code: "invalid_input", detail: "Add a subject.") }
        let contacts = inboxContacts(ws)
        var contact: ContactRef?
        if let id = body["contact_id"]?.string {
            guard let found = contacts.first(where: { $0.id == id }) else { throw notFound("contact") }
            contact = found.ref
        } else if let email = body["contact"]?["email"]?.string?.trimmingCharacters(in: .whitespaces), !email.isEmpty {
            if let found = contacts.first(where: { $0.email?.caseInsensitiveCompare(email) == .orderedSame }) {
                contact = found.ref
            } else {
                let fresh = Contact(id: made(), email: email, name: body["contact"]?["name"]?.string ?? "", createdAt: now())
                ws.inboxExtras.contacts.append(fresh)
                contact = fresh.ref
            }
        } else {
            throw Failure(status: 422, code: "invalid_input", detail: "Add a recipient.")
        }

        let at = now()
        let number = (ws.tickets.map(\.number).max() ?? 1000) + 1
        var ticket = Ticket(demoID: made(), number: number, subject: subject, createdAt: at)
        ticket.contact = contact
        ticket.channel = body["channel"]?.string.flatMap(Channel.init(rawValue:)) ?? .api
        ticket.mailboxID = body["mailbox_id"]?.string
        ticket.priority = body["priority"]?.string.flatMap(Priority.init(rawValue:)) ?? .normal
        ticket.status = body["status"]?.string.flatMap(TicketStatus.init(rawValue:)) ?? .open
        ticket.teamID = body["team_id"]?.string.flatMap { id in ws.teams.first { $0.id == id }?.id }
        ticket.assignee = body["assignee_id"]?.string.flatMap { id in ws.members.first { $0.id == id }?.ref }
        let html = body["body_html"]?.string ?? ""
        ticket.lastMessagePreview = DemoSeed.preview(html)
        ws.tickets.append(ticket)
        let index = ws.tickets.count - 1
        publish("ticket.created", ["ticket": DemoSeed.json(ticket)], in: &ws)
        let kind = MessageKind(rawValue: body["kind"]?.string ?? "reply") ?? .reply
        _ = send(kind: kind, html: html, attachments: [], on: index, in: &ws)
        return ws.tickets[index]
    }

    /// `POST /v1/tickets/bulk`: the same change on each ticket, or deletion.
    private func inboxBulk(_ body: JSONValue, in ws: inout DemoWorkspace) throws {
        let ids = body["ids"]?.array?.compactMap(\.string) ?? []
        guard !ids.isEmpty else { throw Failure(status: 422, code: "invalid_input", detail: "Pick at least one ticket.") }
        if body["delete"]?.bool == true {
            let gone = Set(ids)
            for id in ids where ws.tickets.contains(where: { $0.id == id }) {
                publish("ticket.deleted", ["ticket_id": .string(id)], in: &ws)
            }
            ws.tickets.removeAll { gone.contains($0.id) }
            return
        }
        let patch = body["patch"]?.object ?? [:]
        for id in ids {
            guard let index = ws.tickets.firstIndex(where: { $0.id == id }) else { continue }
            update(index, with: patch, by: ActivityActor(type: "user", user: me), in: &ws)
        }
    }
}
#endif
