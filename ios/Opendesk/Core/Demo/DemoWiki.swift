#if DEBUG
import Foundation

// The wiki of the demo (contracts/wiki.md). Margin has a small tree of runbooks,
// policies and onboarding pages written by different people, each with a little
// history; Lumen Labs one page. Page ids are `0197a0c0-0023-7000-8000-0000000000<nn>`:
//
//   01 Escalation process           02  Incident runbook        03  On-call rota
//   04 Refund policy                05  Refunds over 100 euros
//   06 Onboarding                   07  First week checklist    08  Tone of voice
//   09 SSO troubleshooting          10  Release notes process

struct DemoWiki: Sendable {
    var pages: [WikiPage] = []
    /// Each page's kept versions, newest first, with their bodies.
    var revisions: [String: [WikiRevision]] = [:]

    static func pageID(_ n: Int) -> String { demoKBID(0x23, n) }
    static func revisionID(_ n: Int, _ r: Int) -> String { demoKBID(0x24, n * 100 + r) }

    static func seed(_ ws: DemoWorkspace) -> DemoWiki {
        ws.id == DemoSeed.marginID ? margin() : ws.id == DemoSeed.lumenID ? lumen() : DemoWiki()
    }

    private struct Spec {
        var n: Int
        var parent: Int?
        var position: Int
        var icon: String
        var title: String
        var body: String
        var writer: DemoPerson
        var editor: DemoPerson?
        var created: Double
        var updated: Double
        /// Earlier versions' bodies, oldest first, each a day apart before `updated`.
        var earlier: [String] = []
    }

    private static func build(_ specs: [Spec]) -> DemoWiki {
        var wiki = DemoWiki()
        for spec in specs {
            let editor = spec.editor ?? spec.writer
            let created = DemoSeed.ago(spec.created * 1440)
            let updated = DemoSeed.ago(spec.updated * 1440)
            let html = KBMarkdown.html(from: spec.body)
            wiki.pages.append(WikiPage(
                id: pageID(spec.n), parentID: spec.parent.map(pageID), title: spec.title, icon: spec.icon, bodyHTML: html,
                position: spec.position, version: spec.earlier.count + 1, createdBy: spec.writer.ref, updatedBy: editor.ref,
                createdAt: created, updatedAt: updated, breadcrumbs: [], children: []
            ))
            var kept: [WikiRevision] = []
            for (index, body) in spec.earlier.enumerated() {
                let at = created.addingTimeInterval(Double(index) * 86400)
                kept.append(WikiRevision(id: revisionID(spec.n, index + 1), title: spec.title, icon: spec.icon, bodyHTML: KBMarkdown.html(from: body), author: spec.writer.ref, createdAt: at, updatedAt: at.addingTimeInterval(600)))
            }
            kept.append(WikiRevision(id: revisionID(spec.n, spec.earlier.count + 1), title: spec.title, icon: spec.icon, bodyHTML: html, author: editor.ref, createdAt: updated.addingTimeInterval(-900), updatedAt: updated))
            wiki.revisions[pageID(spec.n)] = kept.reversed()
        }
        return wiki
    }

    // MARK: Margin

    static func margin() -> DemoWiki {
        build([
            Spec(n: 1, parent: nil, position: 0, icon: "🚨", title: "Escalation process", body: """
            Escalate when a customer is blocked, money is at stake, or you have tried twice without progress.

            ## Before you escalate

            - Read the ticket's history and search the wiki.
            - Write down what you tried in an internal note.

            ## Steps

            1. Set the ticket to **urgent**.
            2. Leave an internal note with what the customer needs.
            3. Assign it to the lead on call (see the *On-call rota*).
            4. Tell the customer who has it now and when they will hear back.

            > Sev 1 incidents skip the queue: follow the incident runbook.
            """, writer: .jonas, editor: .maya, created: 120, updated: 2, earlier: [
                "Escalate when a customer is blocked or money is at stake.\n\n## Steps\n\n1. Set the ticket to **urgent**.\n2. Assign it to the lead on call.",
            ]),
            Spec(n: 2, parent: 1, position: 0, icon: "🔥", title: "Incident runbook", body: """
            Use this when many customers report the same outage, or the status page shows one.

            ## The first ten minutes

            1. Post in `#incidents` with what you see and since when.
            2. Tag tickets about it with **incident** so they stay together.
            3. Put a short notice on the status page.

            ## While it lasts

            - Answer new tickets with the *Known outage* saved reply.
            - Update the status page every 30 minutes, even with no news.

            ## Afterwards

            Reply to every tagged ticket once it is fixed, and write a short review in this wiki within two days.
            """, writer: .jonas, created: 90, updated: 6),
            Spec(n: 3, parent: 1, position: 1, icon: "📞", title: "On-call rota", body: """
            The lead on call takes escalations outside the shift that owns them.

            - **Weekdays 08:00 to 17:00 CET**: Jonas
            - **Weekdays 17:00 to 23:00 CET**: Priya
            - **Weekends**: rotates weekly, see the shared calendar

            Swap a shift by asking in `#support-leads` and updating the calendar yourself.
            """, writer: .priya, created: 60, updated: 9),
            Spec(n: 4, parent: nil, position: 1, icon: "💸", title: "Refund policy", body: """
            Who can refund what, and when.

            ## The rule

            Refund any charge within **30 days**, no questions asked.

            ## Exceptions

            - Duplicate charges: refund at once and apologize.
            - Over 100 euros: a lead approves it first.
            - Chargebacks: don't refund twice. Hand the ticket to billing.

            ## How to refund

            1. Find the charge in Stripe.
            2. Refund it and note the amount and reference on the ticket.
            """, writer: .lea, editor: .lea, created: 200, updated: 14, earlier: [
                "Refund any charge within 14 days.\n\n## How to refund\n\n1. Find the charge in Stripe.\n2. Refund it.",
                "Refund any charge within 30 days, no questions asked.\n\n## How to refund\n\n1. Find the charge in Stripe.\n2. Refund it and note the amount on the ticket.",
            ]),
            Spec(n: 5, parent: 4, position: 0, icon: "🧾", title: "Refunds over 100 euros", body: """
            Larger refunds need a second pair of eyes.

            1. Add an internal note with the amount, the reason and the invoice number.
            2. Assign the ticket to the **Billing** team with the label *refund approval*.
            3. Billing approves or asks a question within one business day.

            Annual plans refunded in full within 14 days don't need approval.
            """, writer: .lea, created: 150, updated: 30),
            Spec(n: 6, parent: nil, position: 2, icon: "👋", title: "Onboarding", body: """
            Everything a new teammate needs in their first two weeks. Start with the checklist, then read the pages under this one.
            """, writer: .maya, created: 300, updated: 45),
            Spec(n: 7, parent: 6, position: 0, icon: "📋", title: "First week checklist", body: """
            ## Day one

            - Get access to opendesk, Slack, Stripe and Linear.
            - Read the escalation process and the refund policy.

            ## The rest of the week

            - Shadow a teammate for two shifts.
            - Answer ten tickets with a buddy reviewing your drafts.
            - Take your own queue for half a day on Friday.
            """, writer: .maya, editor: .sam, created: 280, updated: 1),
            Spec(n: 8, parent: 6, position: 1, icon: "🧭", title: "Tone of voice", body: """
            We write like a helpful colleague, not a company.

            - Use the customer's name once, at the start.
            - Say what happened and what you did, in that order.
            - No jargon, no exclamation marks, no "unfortunately".
            - Close with the next step, not with "let me know if you have questions".
            """, writer: .sam, created: 260, updated: 21),
            Spec(n: 9, parent: nil, position: 3, icon: "🔐", title: "SSO troubleshooting", body: """
            Most SSO tickets are one of three things. Check them in this order.

            1. **ACS URL**: it must be exactly `https://margin.app/sso/acs`.
            2. **Certificate**: a rotated certificate needs the new metadata URL.
            3. **Cookies**: Safari's cross-site tracking prevention can drop the session.

            Send the customer the *Fix an SSO login loop* article when it is the third.
            """, writer: .jonas, created: 70, updated: 4),
            Spec(n: 10, parent: nil, position: 4, icon: "", title: "Release notes process", body: """
            Product writes the changelog; support checks it before it goes out.

            1. Product drafts the update in **Knowledge base**, then **Changelog**.
            2. Support reads it for anything customers will ask about.
            3. Schedule it for the release day.
            """, writer: .priya, created: 20, updated: 0.4),
        ])
    }

    static func lumen() -> DemoWiki {
        let ruth = UserRef(id: demoID(.user, 20), name: "Ruth Adler", email: nil, avatarURL: nil)
        let at = DemoSeed.ago(5000)
        let html = KBMarkdown.html(from: "Answer within four business hours. Escalate anything about data loss to Ruth.")
        var wiki = DemoWiki()
        wiki.pages = [WikiPage(id: pageID(101), parentID: nil, title: "Support basics", icon: "📘", bodyHTML: html, position: 0, version: 1, createdBy: ruth, updatedBy: ruth, createdAt: at, updatedAt: at, breadcrumbs: [], children: [])]
        wiki.revisions[pageID(101)] = [WikiRevision(id: revisionID(101, 1), title: "Support basics", icon: "📘", bodyHTML: html, author: ruth, createdAt: at, updatedAt: at)]
        return wiki
    }

    // MARK: Tree

    func page(_ id: String) -> WikiPage? { pages.first { $0.id == id } }

    func children(of parent: String?) -> [WikiPage] {
        pages.filter { $0.parentID == parent }.sorted { $0.position < $1.position }
    }

    func ancestors(of id: String) -> [WikiPage] {
        var out: [WikiPage] = []
        var current = page(id)?.parentID
        while let parent = current, let found = page(parent), !out.contains(where: { $0.id == parent }) {
            out.insert(found, at: 0)
            current = found.parentID
        }
        return out
    }

    func descendants(of id: String) -> [String] {
        children(of: id).flatMap { [$0.id] + descendants(of: $0.id) }
    }

    /// The page as the API answers it: breadcrumbs and subpages filled in.
    func full(_ id: String) -> WikiPage? {
        guard var page = page(id) else { return nil }
        page.breadcrumbs = ancestors(of: id).map { WikiRef(id: $0.id, title: $0.title, icon: $0.icon) }
        page.children = children(of: id).map { WikiRef(id: $0.id, title: $0.title, icon: $0.icon) }
        return page
    }

    mutating func renumber(_ parent: String?) {
        for (position, child) in children(of: parent).enumerated() {
            if let index = pages.firstIndex(where: { $0.id == child.id }) { pages[index].position = position }
        }
    }
}

// MARK: - Routes

extension DemoBackend {
    func wikiRoutes(_ call: Call, in ws: inout DemoWorkspace) throws -> Data?? {
        if call.match("GET", "wiki/pages") != nil {
            return .some(encode(ListBody(data: ws.kb.wiki.pages.map(\.node))))
        }
        if call.match("GET", "wiki/search") != nil {
            return .some(encode(ListBody(data: wikiSearch(call.query["q"] ?? "", in: ws.kb.wiki))))
        }
        if let id = call.match("GET", "wiki/pages/*")?.first {
            guard let page = ws.kb.wiki.full(id) else { throw notFound("page") }
            return .some(encode(page))
        }
        if call.match("POST", "wiki/pages") != nil {
            let parent = call.string("parent_id").flatMap { $0.isEmpty ? nil : $0 }
            if let parent, ws.kb.wiki.page(parent) == nil { throw Failure(status: 422, code: "invalid", detail: "That parent page isn't in this wiki.") }
            let at = now()
            let page = WikiPage(
                id: made(), parentID: parent, title: String((call.string("title") ?? "").prefix(200)), icon: call.string("icon") ?? "",
                bodyHTML: call.string("body_html") ?? "", position: ws.kb.wiki.children(of: parent).count, version: 1,
                createdBy: me, updatedBy: me, createdAt: at, updatedAt: at, breadcrumbs: [], children: []
            )
            ws.kb.wiki.pages.append(page)
            ws.kb.wiki.revisions[page.id] = [WikiRevision(id: made(), title: page.title, icon: page.icon, bodyHTML: page.bodyHTML, author: me, createdAt: at, updatedAt: at)]
            publish("wiki_page.created", ["wiki_page": DemoSeed.json(page.node), "updated_by": DemoSeed.json(me)], in: &ws)
            return .some(encode(ws.kb.wiki.full(page.id)))
        }
        if let id = call.match("PATCH", "wiki/pages/*")?.first {
            guard let index = ws.kb.wiki.pages.firstIndex(where: { $0.id == id }) else { throw notFound("page") }
            var page = ws.kb.wiki.pages[index]
            if let version = call.body["version"]?.int, version != page.version {
                throw Failure(status: 409, code: "page_changed", detail: "Someone else changed this page since you opened it. Reload it to see their changes.")
            }
            var changed = false
            if let title = call.string("title"), title != page.title {
                page.title = String(title.prefix(200))
                changed = true
            }
            if let icon = call.string("icon"), icon != page.icon {
                page.icon = icon
                changed = true
            }
            if let body = call.string("body_html"), body != page.bodyHTML {
                page.bodyHTML = body
                changed = true
            }
            guard changed else { return .some(encode(ws.kb.wiki.full(id))) }
            page.version += 1
            page.updatedAt = now()
            page.updatedBy = me
            ws.kb.wiki.pages[index] = page
            keepRevision(of: page, in: &ws)
            publish("wiki_page.updated", ["wiki_page": DemoSeed.json(page.node), "updated_by": DemoSeed.json(me)], in: &ws)
            return .some(encode(ws.kb.wiki.full(id)))
        }
        if let id = call.match("POST", "wiki/pages/*/move")?.first {
            guard let index = ws.kb.wiki.pages.firstIndex(where: { $0.id == id }) else { throw notFound("page") }
            let parent = call.body["parent_id"]?.string
            if let parent {
                guard ws.kb.wiki.page(parent) != nil else { throw Failure(status: 422, code: "invalid", detail: "That parent page isn't in this wiki.") }
                if parent == id || ws.kb.wiki.descendants(of: id).contains(parent) {
                    throw Failure(status: 422, code: "page_cycle", detail: "A page can't move under itself or one of its subpages.")
                }
            }
            let old = ws.kb.wiki.pages[index].parentID
            ws.kb.wiki.pages[index].parentID = parent
            var siblings = ws.kb.wiki.children(of: parent).filter { $0.id != id }
            let position = max(0, min(call.body["position"]?.int ?? siblings.count, siblings.count))
            siblings.insert(ws.kb.wiki.pages[index], at: position)
            for (order, sibling) in siblings.enumerated() {
                if let i = ws.kb.wiki.pages.firstIndex(where: { $0.id == sibling.id }) { ws.kb.wiki.pages[i].position = order }
            }
            if old != parent { ws.kb.wiki.renumber(old) }
            let page = ws.kb.wiki.pages[index]
            publish("wiki_page.moved", ["wiki_page": DemoSeed.json(page.node), "updated_by": DemoSeed.json(me)], in: &ws)
            return .some(encode(ws.kb.wiki.full(id)))
        }
        if let id = call.match("DELETE", "wiki/pages/*")?.first {
            guard let page = ws.kb.wiki.page(id) else { throw notFound("page") }
            let gone = [id] + ws.kb.wiki.descendants(of: id)
            ws.kb.wiki.pages.removeAll { gone.contains($0.id) }
            for page in gone { ws.kb.wiki.revisions[page] = nil }
            ws.kb.wiki.renumber(page.parentID)
            publish("wiki_page.deleted", ["wiki_page_id": .string(id), "wiki_page_ids": .array(gone.map(JSONValue.string))], in: &ws)
            return .some(nil)
        }
        if let id = call.match("GET", "wiki/pages/*/revisions")?.first {
            guard ws.kb.wiki.page(id) != nil else { throw notFound("page") }
            let list = (ws.kb.wiki.revisions[id] ?? []).map { revision -> WikiRevision in
                var copy = revision
                copy.bodyHTML = nil
                return copy
            }
            return .some(encode(ListBody(data: list)))
        }
        if let ids = call.match("GET", "wiki/pages/*/revisions/*") {
            guard let revision = ws.kb.wiki.revisions[ids[0]]?.first(where: { $0.id == ids[1] }) else { throw notFound("version") }
            return .some(encode(revision))
        }
        if let ids = call.match("POST", "wiki/pages/*/revisions/*/restore") {
            guard let index = ws.kb.wiki.pages.firstIndex(where: { $0.id == ids[0] }),
                  let revision = ws.kb.wiki.revisions[ids[0]]?.first(where: { $0.id == ids[1] })
            else { throw notFound("version") }
            var page = ws.kb.wiki.pages[index]
            page.title = revision.title
            page.icon = revision.icon
            page.bodyHTML = revision.bodyHTML ?? ""
            page.version += 1
            page.updatedAt = now()
            page.updatedBy = me
            ws.kb.wiki.pages[index] = page
            // A restore always starts a new revision.
            ws.kb.wiki.revisions[page.id, default: []].insert(WikiRevision(id: made(), title: page.title, icon: page.icon, bodyHTML: page.bodyHTML, author: me, createdAt: page.updatedAt, updatedAt: page.updatedAt), at: 0)
            publish("wiki_page.updated", ["wiki_page": DemoSeed.json(page.node), "updated_by": DemoSeed.json(me)], in: &ws)
            return .some(encode(ws.kb.wiki.full(page.id)))
        }
        return nil
    }

    /// One person's edits less than ten minutes apart are one revision (contracts/wiki.md "History").
    private func keepRevision(of page: WikiPage, in ws: inout DemoWorkspace) {
        var list = ws.kb.wiki.revisions[page.id] ?? []
        if var latest = list.first, latest.author?.id == me.id, page.updatedAt.timeIntervalSince(latest.updatedAt) < 600, page.updatedAt.timeIntervalSince(latest.createdAt) < 3600 {
            latest.title = page.title
            latest.icon = page.icon
            latest.bodyHTML = page.bodyHTML
            latest.updatedAt = page.updatedAt
            list[0] = latest
        } else {
            list.insert(WikiRevision(id: made(), title: page.title, icon: page.icon, bodyHTML: page.bodyHTML, author: me, createdAt: page.updatedAt, updatedAt: page.updatedAt), at: 0)
        }
        ws.kb.wiki.revisions[page.id] = Array(list.prefix(100))
    }

    /// Titles first, then text; every word also as a prefix.
    func wikiSearch(_ q: String, in wiki: DemoWiki) -> [WikiSearchHit] {
        let words = q.lowercased().split(whereSeparator: \.isWhitespace).map(String.init)
        guard !words.isEmpty else { return [] }
        func matches(_ text: String) -> Bool {
            let tokens = text.lowercased().split { !$0.isLetter && !$0.isNumber }
            return words.allSatisfy { word in tokens.contains { $0.hasPrefix(word) } }
        }
        let scored: [(WikiPage, Int)] = wiki.pages.compactMap { page in
            let text = HTMLText.plain(from: page.bodyHTML)
            if matches(page.title) { return (page, 2) }
            if matches(page.title + " " + text) { return (page, 1) }
            return nil
        }
        return scored.sorted { $0.1 != $1.1 ? $0.1 > $1.1 : $0.0.updatedAt > $1.0.updatedAt }.prefix(30).map { page, _ in
            let text = HTMLText.plain(from: page.bodyHTML).replacingOccurrences(of: "\n", with: " ")
            var snippet = String(text.prefix(140))
            if let first = words.first, let range = text.range(of: first, options: .caseInsensitive) {
                let start = text.index(range.lowerBound, offsetBy: -40, limitedBy: text.startIndex) ?? text.startIndex
                let end = text.index(range.upperBound, offsetBy: 100, limitedBy: text.endIndex) ?? text.endIndex
                snippet = (start > text.startIndex ? "…" : "") + String(text[start ..< end]) + (end < text.endIndex ? "…" : "")
            }
            return WikiSearchHit(
                id: page.id, parentID: page.parentID, title: page.title, icon: page.icon, snippet: snippet,
                path: wiki.ancestors(of: page.id).map { WikiRef(id: $0.id, title: $0.title, icon: $0.icon) }, updatedAt: page.updatedAt
            )
        }
    }
}
#endif
