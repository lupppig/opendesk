#if DEBUG
import Foundation

// The ticket view's routes beyond the core ones in DemoBackend.swift: custom fields,
// editing (subject, fields, solve checks), delete and merge, side conversations, the
// shared draft, links, message edits, originals, template filling and Ollie's writing tools.

/// Ticket data beyond the core seed: custom field values, side conversations, drafts, merges.
struct DemoTicketExtras: Sendable {
    var fields: [CustomField] = []
    var values: [Int: [String: JSONValue]] = [:]
    var threads: [String: [JSONValue]] = [:]
    var threadMessages: [Int: [ThreadSeed]] = [:]
    var drafts: [String: JSONValue] = [:]
    var links: [String: [JSONValue]] = [:]
    var contacts: [String: JSONValue] = [:]
    var mailboxes: [JSONValue] = []
    var applied = false

    struct ThreadSeed: Sendable {
        var thread: String
        var inbound: Bool
        var from: String
        var minutes: Double
        var html: String
    }

    static func seed(_ ws: DemoWorkspace) -> DemoTicketExtras {
        var extras = DemoTicketExtras()
        guard ws.tickets.contains(where: { $0.number == 1072 }) else { return extras }
        let option = { (id: String, label: String, color: String) in FieldOption(id: id, label: label, color: color) }
        extras.fields = [
            CustomField(id: demoID(.made, 9101), entity: "ticket", key: "root_cause", name: "Root cause", type: .select,
                        options: [option("configuration", "Configuration", "blue"), option("bug", "Bug", "red"), option("customer_setup", "Customer setup", "amber"), option("third_party", "Third party", "violet")],
                        requiredOnSolve: true, description: "What caused it, for the weekly review.", position: 1),
            CustomField(id: demoID(.made, 9102), entity: "ticket", key: "plan", name: "Plan", type: .select,
                        options: [option("starter", "Starter", "gray"), option("business", "Business", "blue"), option("enterprise", "Enterprise", "violet")], position: 2),
            CustomField(id: demoID(.made, 9103), entity: "ticket", key: "account_id", name: "Account ID", type: .text, position: 3),
            CustomField(id: demoID(.made, 9104), entity: "ticket", key: "affected_users", name: "Affected users", type: .number, position: 4),
            CustomField(id: demoID(.made, 9105), entity: "ticket", key: "escalated_to", name: "Escalated to", type: .user, position: 5),
            CustomField(id: demoID(.made, 9106), entity: "ticket", key: "follow_up", name: "Follow up on", type: .date, position: 6),
            CustomField(id: demoID(.made, 9107), entity: "ticket", key: "billing_verified", name: "Billing verified", type: .checkbox, position: 7),
            CustomField(id: demoID(.made, 9111), entity: "contact", key: "region", name: "Region", type: .text, position: 1),
            CustomField(id: demoID(.made, 9112), entity: "contact", key: "seats", name: "Seats", type: .number, position: 2),
        ]
        extras.values = [
            1072: ["plan": .string("enterprise"), "account_id": .string("acct_oak_2291"), "affected_users": .number(180), "escalated_to": .string(DemoPerson.jonas.id)],
            1068: ["plan": .string("business"), "account_id": .string("acct_bri_0418"), "billing_verified": .bool(true), "follow_up": .string("2026-06-17")],
            1086: ["plan": .string("business")],
            1066: ["root_cause": .string("customer_setup"), "plan": .string("starter"), "account_id": .string("acct_mer_1180")],
        ]

        let maya = DemoSeed.json(UserRef(id: DemoPerson.maya.id, name: DemoPerson.maya.name, email: "maya@margin.app", avatarURL: nil))
        let t1072 = DemoSeed.ticketID(1072)
        let t1068 = DemoSeed.ticketID(1068)
        let side1 = demoID(.made, 9201)
        let side2 = demoID(.made, 9202)
        extras.threads = [
            t1072: [thread(side1, ticket: t1072, subject: "Okta certificate rotated early", to: ["identity@oakridgehealth.org"], status: "open", by: maya, count: 2, last: 38, created: 74)],
            t1068: [thread(side2, ticket: t1068, subject: "Brightline duplicate renewal, refund reference", to: ["finance@margin.app"], status: "closed", by: maya, count: 2, last: 250, created: 258)],
        ]
        extras.threadMessages = [
            1072: [
                ThreadSeed(thread: side1, inbound: false, from: "", minutes: 74, html: "<p>Hi team,</p><p>Dana mentioned your Okta signing certificate was rotated last night. Can you confirm the time it changed and whether the old certificate stays valid until it expires next week?</p><p>Thanks,<br>Maya</p>"),
                ThreadSeed(thread: side1, inbound: true, from: "Ravi Patel", minutes: 38, html: "<p>Hi Maya,</p><p>Rotated at 07:36 CEST. The old certificate is valid until June 18, so keeping both active until then is perfect.</p><p>Ravi, Identity team</p>"),
            ],
            1068: [
                ThreadSeed(thread: side2, inbound: false, from: "", minutes: 258, html: "<p>Could you send me the refund reference for Brightline's second renewal charge (€4,320, card ending 4417)?</p>"),
                ThreadSeed(thread: side2, inbound: true, from: "Margin Finance", minutes: 250, html: "<p>Refund issued, reference <code>re_3PqL7k2</code>. It should reach the card in 5 to 10 business days.</p>"),
            ],
        ]
        extras.drafts = [
            t1068: .object([
                "ticket_id": .string(t1068),
                "body_html": .string("<p>Hi Tomás,</p><p>Sorry, our first reply didn't reach you. The duplicate charge of €4,320 was refunded on June 10 (reference re_3PqL7k2) and should show within 5 to 10 business days.</p><p>Lea</p>"),
                "body_text": .string("Hi Tomás,\n\nSorry, our first reply didn't reach you. The duplicate charge of €4,320 was refunded on June 10 (reference re_3PqL7k2) and should show within 5 to 10 business days.\n\nLea"),
                "by": .object(["type": .string("user"), "name": .string(DemoPerson.lea.name), "user": DemoSeed.json(UserRef(id: DemoPerson.lea.id, name: DemoPerson.lea.name, email: "lea@margin.app", avatarURL: nil))]),
                "updated_at": DemoSeed.json(DemoSeed.ago(20)),
            ]),
        ]
        extras.links = [
            t1072: [link(demoID(.made, 9301), ticket: t1072, provider: "linear", url: "https://linear.app/margin/issue/AUTH-412", title: "AUTH-412 Accept a rotated SAML certificate without downtime", actor: "user", by: DemoPerson.jonas, minutes: 58)],
            t1068: [link(demoID(.made, 9302), ticket: t1068, provider: "stripe", url: "https://dashboard.stripe.com/refunds/re_3PqL7k2", title: "Refund re_3PqL7k2, €4,320.00", actor: "ai", by: nil, minutes: 255)],
        ]
        extras.contacts = [
            DemoSeed.dana.id: contact(DemoSeed.dana, phone: "+1 503 555 0142", company: ("Oakridge Health", "oakridgehealth.org"), tags: ["VIP", "Enterprise"], fields: ["region": .string("US West"), "seats": .number(180)]),
            DemoSeed.tomas.id: contact(DemoSeed.tomas, phone: "+34 911 555 207", company: ("Brightline Logistics", "brightline.io"), tags: ["Annual plan"], fields: ["region": .string("EU South"), "seats": .number(12)]),
            DemoSeed.yusuf.id: contact(DemoSeed.yusuf, phone: "", company: nil, tags: [], fields: [:]),
            DemoSeed.ben.id: contact(DemoSeed.ben, phone: "", company: ("Meridian University", "meridian.edu"), tags: ["Education"], fields: ["region": .string("EU Central")]),
        ]
        extras.mailboxes = [
            .object(["id": .string(DemoSeed.supportMailbox), "name": .string("Support"), "email": .string("support@margin.app"), "display_name": .string("Margin Support")]),
            .object(["id": .string(DemoSeed.billingMailbox), "name": .string("Billing"), "email": .string("billing@margin.app"), "display_name": .string("Margin Billing")]),
        ]
        return extras
    }

    private static func thread(_ id: String, ticket: String, subject: String, to: [String], status: String, by: JSONValue, count: Int, last: Double, created: Double) -> JSONValue {
        .object([
            "id": .string(id), "ticket_id": .string(ticket), "subject": .string(subject),
            "to": .array(to.map(JSONValue.string)), "cc": .array([]), "status": .string(status),
            "include_history": .bool(false), "created_by": by, "message_count": .number(Double(count)),
            "last_message_at": DemoSeed.json(DemoSeed.ago(last)), "created_at": DemoSeed.json(DemoSeed.ago(created)), "updated_at": DemoSeed.json(DemoSeed.ago(last)),
        ])
    }

    private static func link(_ id: String, ticket: String, provider: String, url: String, title: String, actor: String, by: DemoPerson?, minutes: Double) -> JSONValue {
        .object([
            "id": .string(id), "ticket_id": .string(ticket), "provider": .string(provider), "url": .string(url), "title": .string(title),
            "actor_type": .string(actor),
            "created_by": by.map { DemoSeed.json(UserRef(id: $0.id, name: $0.name, email: "", avatarURL: nil)) } ?? .null,
            "created_at": DemoSeed.json(DemoSeed.ago(minutes)),
        ])
    }

    private static func contact(_ ref: ContactRef, phone: String, company: (String, String)?, tags: [String], fields: [String: JSONValue]) -> JSONValue {
        .object([
            "id": .string(ref.id), "email": ref.email.map(JSONValue.string) ?? .null, "name": .string(ref.name), "phone": .string(phone),
            "company": company.map { .object(["id": .string(demoID(.made, 9400 + abs($0.0.hashValue % 90))), "name": .string($0.0), "domain": .string($0.1)]) } ?? .null,
            "notes": .string(""),
            "tags": .array(tags.enumerated().map { .object(["id": .string(demoID(.made, 9500 + $0.offset)), "name": .string($0.element), "color": .string(["violet", "blue", "green"][$0.offset % 3])]) }),
            "fields": .object(fields), "blocked": .bool(false), "ticket_count": .number(0),
            "created_at": DemoSeed.json(DemoSeed.ago(200 * 1440)),
        ])
    }
}

extension DemoBackend {
    /// The answer (nil inside for 204), or nil when the route isn't this area's.
    func ticketRoutes(_ call: Call, in ws: inout DemoWorkspace) throws -> Data?? {
        if !ws.ticketExtras.applied { applyTicketExtras(&ws) }
        let actor = ActivityActor(type: "user", user: me)

        // Definitions and lookups another area may answer too: theirs wins.
        if call.match("GET", "custom-fields") != nil {
            // Ticket fields live here, contact and company fields in the directory area.
            switch call.query["entity"] {
            case "ticket": return .some(encode(ListBody(data: ws.ticketExtras.fields.filter { $0.entity == "ticket" })))
            case nil: return .some(encode(ListBody(data: ws.ticketExtras.fields.filter { $0.entity == "ticket" } + ws.directory.fields)))
            default: return try directoryRoutes(call, in: &ws)
            }
        }
        if call.match("GET", "mailboxes") != nil {
            if let answer = try accountRoutes(call, in: &ws) { return answer }
            return .some(encode(JSONValue.object(["data": .array(ws.ticketExtras.mailboxes)])))
        }
        if let id = call.match("GET", "contacts/*")?.first {
            if let answer = try directoryRoutes(call, in: &ws) { return answer }
            return .some(encode(contactJSON(id, in: ws)))
        }
        if let id = call.match("GET", "contacts/*/tickets")?.first {
            if let answer = try directoryRoutes(call, in: &ws) { return answer }
            let tickets = ws.tickets.filter { $0.contact?.id == id && !$0.spam }.sorted { $0.lastMessageAt > $1.lastMessageAt }
            return .some(encode(ListBody(data: tickets)))
        }

        // Editing a ticket.
        if let key = call.match("PATCH", "tickets/*")?.first {
            let index = try ticket(key, in: ws)
            var body = call.body.object ?? [:]
            var changes: [String] = []
            if let subject = body.removeValue(forKey: "subject")?.string?.trimmingCharacters(in: .whitespacesAndNewlines), !subject.isEmpty, subject != ws.tickets[index].subject {
                ws.tickets[index].subject = subject
                changes.append("subject")
            }
            var fields = ws.tickets[index].fields
            if let edits = body.removeValue(forKey: "fields")?.object {
                for (key, value) in edits { fields[key] = value.isNull ? nil : value }
            }
            if let raw = body["status"]?.string, let status = TicketStatus(rawValue: raw), status.isDone, !ws.tickets[index].status.isDone {
                let missing = ws.ticketExtras.fields.filter { $0.entity == "ticket" && $0.requiredOnSolve && Self.empty(fields[$0.key]) }
                if let first = missing.first {
                    throw Failure(status: 422, code: "field_required", detail: "Fill in \(first.name) before solving.")
                }
            }
            if fields != ws.tickets[index].fields {
                ws.tickets[index].fields = fields
                changes.append("fields")
            }
            update(index, with: body, by: actor, in: &ws)
            if !changes.isEmpty { touched(index, in: &ws, changes: changes) }
            return .some(encode(ws.tickets[index]))
        }
        if let key = call.match("DELETE", "tickets/*")?.first {
            let index = try ticket(key, in: ws)
            let id = ws.tickets[index].id
            ws.tickets.remove(at: index)
            ws.messages[id] = nil
            ws.activities[id] = nil
            publish("ticket.deleted", ["ticket_id": .string(id)], in: &ws)
            return .some(nil)
        }
        if let key = call.match("POST", "tickets/*/merge")?.first {
            let source = try ticket(key, in: ws)
            let target = try ticket(call.string("into_id") ?? "", in: ws)
            guard source != target else { throw Failure(status: 422, code: "invalid_merge", detail: "A ticket can't be merged into itself.") }
            let from = ws.tickets[source]
            let into = ws.tickets[target].id
            let moved = (ws.messages[from.id] ?? []).map { message -> Message in
                var message = message
                message.ticketID = into
                return message
            }
            ws.messages[into, default: []] += moved
            ws.messages[from.id] = []
            ws.tickets[target].messageCount += moved.count
            ws.tickets[target].lastMessageAt = max(ws.tickets[target].lastMessageAt, from.lastMessageAt)
            let activity = Activity(demoID: made(), ticketID: into, actor: actor, type: "merged", data: ["from": .number(Double(from.number)), "into": .number(Double(ws.tickets[target].number))], createdAt: now())
            ws.activities[into, default: []].append(activity)
            update(source, with: ["status": .string(TicketStatus.closed.rawValue)], by: actor, in: &ws)
            touched(target, in: &ws, changes: ["messages"])
            publish("activity.created", ["ticket_id": .string(into), "activity": DemoSeed.json(activity)], in: &ws)
            return .some(encode(ws.tickets[target]))
        }

        // Sending on a chat: a reply stays retractable until the visitor's widget picks it up.
        if let key = call.match("POST", "tickets/*/messages")?.first, ws.tickets[try ticket(key, in: ws)].channel == .chat {
            let index = try ticket(key, in: ws)
            let ids = call.body["attachment_ids"]?.array?.compactMap(\.string) ?? []
            let kind = MessageKind(rawValue: call.string("kind") ?? "reply") ?? .reply
            var message = send(kind: kind, html: call.string("body_html") ?? "", attachments: ids.compactMap { uploads[$0] }, on: index, in: &ws)
            message.deliveryStatus = "none"
            let id = ws.tickets[index].id
            if let at = ws.messages[id]?.firstIndex(where: { $0.id == message.id }) { ws.messages[id]?[at] = message }
            if let status = call.string("status") {
                update(index, with: ["status": .string(status)], by: actor, in: &ws)
            }
            return .some(encode(message))
        }

        // One message.
        if let captured = call.match("PATCH", "tickets/*/messages/*") {
            let (id, at) = try messageIndex(captured, in: ws)
            guard let html = call.string("body_html") else { throw Failure(status: 422, code: "invalid", detail: "Write something first.") }
            ws.messages[id]?[at].bodyHTML = html
            ws.messages[id]?[at].bodyText = HTMLText.plain(from: html)
            ws.messages[id]?[at].editedAt = now()
            guard let message = ws.messages[id]?[at] else { throw notFound("message") }
            publish("message.updated", ["ticket_id": .string(id), "message": DemoSeed.json(message)], in: &ws)
            return .some(encode(message))
        }
        if let captured = call.match("DELETE", "tickets/*/messages/*") {
            let (id, at) = try messageIndex(captured, in: ws)
            ws.messages[id]?.remove(at: at)
            if let index = ws.tickets.firstIndex(where: { $0.id == id }) {
                ws.tickets[index].messageCount = max(0, ws.tickets[index].messageCount - 1)
                touched(index, in: &ws)
            }
            return .some(nil)
        }
        if let captured = call.match("POST", "tickets/*/messages/*/retract") {
            let (id, at) = try messageIndex(captured, in: ws)
            guard let message = ws.messages[id]?[at], message.kind == .reply, message.deliveryStatus == "none" else {
                throw Failure(status: 422, code: "not_retractable", detail: "The visitor has already seen this reply.")
            }
            ws.messages[id]?[at].kind = .note
            guard let saved = ws.messages[id]?[at] else { throw notFound("message") }
            publish("message.updated", ["ticket_id": .string(id), "message": DemoSeed.json(saved)], in: &ws)
            return .some(encode(saved))
        }
        if let id = call.match("GET", "messages/*/original")?.first {
            return .some(encode(JSONValue.object(["html": .string(try original(id, in: ws))])))
        }

        // Template values.
        if let key = call.match("GET", "tickets/*/variables")?.first {
            let values = variables(ws.tickets[try ticket(key, in: ws)], in: ws)
            return .some(encode(JSONValue.object(["values": .object(values.mapValues(JSONValue.string)), "signature_html": .string("<p>Maya Chen<br>Support, Margin</p>")])))
        }
        if let key = call.match("POST", "tickets/*/render")?.first {
            let values = variables(ws.tickets[try ticket(key, in: ws)], in: ws)
            return .some(encode(JSONValue.object(["body_html": .string(Self.fill(call.string("body_html") ?? "", values))])))
        }

        // The shared draft.
        if let key = call.match("GET", "tickets/*/draft")?.first {
            let id = ws.tickets[try ticket(key, in: ws)].id
            return .some(encode(JSONValue.object(["draft": ws.ticketExtras.drafts[id] ?? .null])))
        }
        if let key = call.match("PUT", "tickets/*/draft")?.first {
            let id = ws.tickets[try ticket(key, in: ws)].id
            let html = call.string("body_html") ?? ""
            let draft = JSONValue.object([
                "ticket_id": .string(id), "body_html": .string(html), "body_text": .string(HTMLText.plain(from: html)),
                "by": .object(["type": .string("user"), "name": .string(me.name), "user": DemoSeed.json(me)]),
                "updated_at": DemoSeed.json(now()),
            ])
            ws.ticketExtras.drafts[id] = draft
            publish("ticket_draft.updated", ["ticket_id": .string(id), "draft": draft], in: &ws)
            return .some(encode(JSONValue.object(["draft": draft])))
        }
        if let key = call.match("DELETE", "tickets/*/draft")?.first {
            let id = ws.tickets[try ticket(key, in: ws)].id
            ws.ticketExtras.drafts[id] = nil
            publish("ticket_draft.updated", ["ticket_id": .string(id), "draft": .null], in: &ws)
            return .some(nil)
        }

        // Links to connected products.
        if let key = call.match("GET", "tickets/*/links")?.first {
            let id = ws.tickets[try ticket(key, in: ws)].id
            return .some(encode(JSONValue.object(["data": .array(ws.ticketExtras.links[id] ?? [])])))
        }

        // Side conversations.
        if let key = call.match("GET", "tickets/*/threads")?.first {
            let id = ws.tickets[try ticket(key, in: ws)].id
            return .some(encode(JSONValue.object(["data": .array(ws.ticketExtras.threads[id] ?? [])])))
        }
        if let key = call.match("POST", "tickets/*/threads")?.first {
            let index = try ticket(key, in: ws)
            let ticketID = ws.tickets[index].id
            let to = call.body["to"]?.array?.compactMap(\.string) ?? []
            guard !to.isEmpty else { throw Failure(status: 422, code: "invalid", detail: "Add someone to send it to.") }
            let at = now()
            let threadID = made()
            let thread = JSONValue.object([
                "id": .string(threadID), "ticket_id": .string(ticketID), "subject": .string(call.string("subject") ?? ws.tickets[index].subject),
                "to": .array(to.map(JSONValue.string)), "cc": call.body["cc"] ?? .array([]), "status": .string("open"),
                "include_history": call.body["include_history"] ?? .bool(false), "created_by": DemoSeed.json(me),
                "message_count": .number(1), "last_message_at": DemoSeed.json(at), "created_at": DemoSeed.json(at), "updated_at": DemoSeed.json(at),
            ])
            ws.ticketExtras.threads[ticketID, default: []].append(thread)
            let message = sideMessage(ticketID: ticketID, thread: threadID, html: call.string("body_html") ?? "", to: to, at: at, in: &ws)
            publish("thread.created", ["ticket_id": .string(ticketID), "thread": thread], in: &ws)
            return .some(encode(JSONValue.object(["thread": thread, "message": DemoSeed.json(message)])))
        }
        if let captured = call.match("POST", "tickets/*/threads/*/messages") {
            let ticketID = ws.tickets[try ticket(captured[0], in: ws)].id
            guard let at = ws.ticketExtras.threads[ticketID]?.firstIndex(where: { $0["id"]?.string == captured[1] }),
                  var thread = ws.ticketExtras.threads[ticketID]?[at].object else { throw notFound("conversation") }
            let when = now()
            let to = thread["to"]?.array?.compactMap(\.string) ?? []
            let message = sideMessage(ticketID: ticketID, thread: captured[1], html: call.string("body_html") ?? "", to: to, at: when, in: &ws)
            thread["status"] = .string("open")
            thread["message_count"] = .number((thread["message_count"]?.double ?? 0) + 1)
            thread["last_message_at"] = DemoSeed.json(when)
            ws.ticketExtras.threads[ticketID]?[at] = .object(thread)
            publish("thread.updated", ["ticket_id": .string(ticketID), "thread": .object(thread)], in: &ws)
            return .some(encode(message))
        }
        if let captured = call.match("PATCH", "tickets/*/threads/*") {
            let ticketID = ws.tickets[try ticket(captured[0], in: ws)].id
            guard let at = ws.ticketExtras.threads[ticketID]?.firstIndex(where: { $0["id"]?.string == captured[1] }),
                  var thread = ws.ticketExtras.threads[ticketID]?[at].object else { throw notFound("conversation") }
            if let status = call.string("status") { thread["status"] = .string(status) }
            thread["updated_at"] = DemoSeed.json(now())
            ws.ticketExtras.threads[ticketID]?[at] = .object(thread)
            publish("thread.updated", ["ticket_id": .string(ticketID), "thread": .object(thread)], in: &ws)
            return .some(encode(JSONValue.object(thread)))
        }

        // Ollie's writing tools.
        if call.match("POST", "ai/rewrite") != nil {
            let text = HTMLText.plain(from: call.string("html") ?? "")
            return .some(encode(JSONValue.object(["html": .string(HTMLText.html(fromPlain: Self.rewrite(text, mode: call.string("mode") ?? "")))])))
        }
        if call.match("POST", "ai/translate") != nil {
            let html = call.string("html") ?? ""
            let target = call.string("target") ?? "en"
            let text = HTMLText.plain(from: html)
            let translated = Self.translate(text, to: target)
            return .some(encode(JSONValue.object(["html": .string(HTMLText.html(fromPlain: translated.text)), "text": .string(translated.text), "source_language": .string(translated.source)])))
        }
        if let key = call.match("POST", "tickets/*/ai/ask")?.first {
            let ticket = ws.tickets[try ticket(key, in: ws)]
            return .some(encode(Self.answer(call.string("question") ?? "", ticket: ticket)))
        }
        return nil
    }

    // MARK: Helpers

    private func applyTicketExtras(_ ws: inout DemoWorkspace) {
        ws.ticketExtras.applied = true
        for (number, values) in ws.ticketExtras.values {
            guard let index = ws.tickets.firstIndex(where: { $0.number == number }) else { continue }
            ws.tickets[index].fields.merge(values) { _, new in new }
        }
        // The forwarded receipt carries the sender's open-tracking pixel, held until the reader asks.
        let t1068 = DemoSeed.ticketID(1068)
        if let at = ws.messages[t1068]?.firstIndex(where: { $0.id == demoID(.message, 106_802) }) {
            ws.messages[t1068]?[at].bodyHTML += "<img src=\"https://t.stripe-mail.com/o/9f2c7a.gif\" width=\"1\" height=\"1\" alt=\"\">"
        }
        // Chat replies the visitor hasn't picked up yet can still be taken back.
        for ticket in ws.tickets where ticket.channel == .chat && !ticket.status.isDone {
            for (at, message) in (ws.messages[ticket.id] ?? []).enumerated() where message.kind == .reply && message.author.type == "user" {
                ws.messages[ticket.id]?[at].deliveryStatus = "none"
            }
        }
        // Side conversation messages.
        for (number, seeds) in ws.ticketExtras.threadMessages {
            let ticketID = DemoSeed.ticketID(number)
            guard let ticket = ws.tickets.first(where: { $0.id == ticketID }) else { continue }
            for (offset, seed) in seeds.enumerated() {
                var message = Message(demoID: demoID(.made, 9600 + number % 100 * 10 + offset), ticketID: ticketID, kind: .note, html: seed.html, createdAt: DemoSeed.ago(seed.minutes))
                message.threadID = seed.thread
                message.subject = ticket.subject
                if seed.inbound {
                    message.direction = "inbound"
                    message.author = MessageAuthor(type: "external", user: nil, contact: nil)
                    message.fromName = seed.from
                } else {
                    message.author = MessageAuthor(type: "user", user: me, contact: nil)
                    message.fromName = "\(me.name) at \(ws.workspace.name)"
                    message.deliveryStatus = "sent"
                }
                ws.messages[ticketID, default: []].append(message)
            }
        }
        ws.ticketExtras.threadMessages = [:]
    }

    private func messageIndex(_ captured: [String], in ws: DemoWorkspace) throws -> (String, Int) {
        let id = ws.tickets[try ticket(captured[0], in: ws)].id
        guard let at = ws.messages[id]?.firstIndex(where: { $0.id == captured[1] }) else { throw notFound("message") }
        return (id, at)
    }

    private func sideMessage(ticketID: String, thread: String, html: String, to: [String], at: Date, in ws: inout DemoWorkspace) -> Message {
        var message = Message(demoID: made(), ticketID: ticketID, kind: .note, html: html, createdAt: at)
        message.threadID = thread
        message.author = MessageAuthor(type: "user", user: me, contact: nil)
        message.fromName = "\(me.name) at \(ws.workspace.name)"
        message.to = to
        message.deliveryStatus = "sent"
        ws.messages[ticketID, default: []].append(message)
        publish("message.created", ["ticket_id": .string(ticketID), "message": DemoSeed.json(message)], in: &ws)
        return message
    }

    private func original(_ id: String, in ws: DemoWorkspace) throws -> String {
        for (_, list) in ws.messages {
            guard let index = list.firstIndex(where: { $0.id == id }) else { continue }
            let message = list[index]
            let earlier = list[..<index].last { $0.direction == "outbound" && !$0.isNote && $0.threadID == nil }
            guard let earlier else { return message.bodyHTML }
            let when = earlier.createdAt.formatted(date: .abbreviated, time: .shortened)
            return message.bodyHTML + "<blockquote><p>On \(when), \(earlier.author.user?.name ?? "Margin Support") wrote:</p>\(earlier.bodyHTML)</blockquote>"
        }
        throw notFound("message")
    }

    private func contactJSON(_ id: String, in ws: DemoWorkspace) -> JSONValue {
        let count = ws.tickets.filter { $0.contact?.id == id }.count
        if case var .object(stored) = ws.ticketExtras.contacts[id] ?? .null {
            stored["ticket_count"] = .number(Double(count))
            return .object(stored)
        }
        let ref = ws.tickets.compactMap(\.contact).first { $0.id == id }
        return .object([
            "id": .string(id), "email": ref?.email.map(JSONValue.string) ?? .null, "name": .string(ref?.name ?? "Customer"),
            "phone": .string(""), "company": .null, "notes": .string(""), "tags": .array([]), "fields": .object([:]),
            "blocked": .bool(false), "ticket_count": .number(Double(count)), "created_at": DemoSeed.json(DemoSeed.ago(90 * 1440)),
        ])
    }

    private func variables(_ ticket: Ticket, in ws: DemoWorkspace) -> [String: String] {
        let name = ticket.contact?.name ?? ""
        let company = ticket.contact.flatMap { ws.ticketExtras.contacts[$0.id]?["company"]?["name"]?.string } ?? ""
        return [
            "contact.first_name": name.split(separator: " ").first.map(String.init) ?? "",
            "contact.name": name,
            "contact.email": ticket.contact?.email ?? "",
            "company.name": company,
            "ticket.number": String(ticket.number),
            "ticket.subject": ticket.subject,
            "agent.first_name": me.name.split(separator: " ").first.map(String.init) ?? "",
            "agent.name": me.name,
            "workspace.name": ws.workspace.name,
        ]
    }

    /// One pass over the text, as the templates package fills it: unknown names stay empty.
    private static func fill(_ html: String, _ values: [String: String]) -> String {
        guard let pattern = try? NSRegularExpression(pattern: #"\{\{\s*([a-z_.]+)\s*\}\}"#) else { return html }
        var result = ""
        var last = html.startIndex
        for match in pattern.matches(in: html, range: NSRange(html.startIndex..., in: html)) {
            guard let whole = Range(match.range, in: html), let key = Range(match.range(at: 1), in: html) else { continue }
            result += html[last ..< whole.lowerBound]
            result += HTMLText.escape(values[String(html[key])] ?? "")
            last = whole.upperBound
        }
        return result + html[last...]
    }

    private static func empty(_ value: JSONValue?) -> Bool {
        guard let value, !value.isNull else { return true }
        if let text = value.string { return text.trimmingCharacters(in: .whitespaces).isEmpty }
        if let list = value.array { return list.isEmpty }
        return false
    }

    private static func rewrite(_ text: String, mode: String) -> String {
        let body = text.trimmingCharacters(in: .whitespacesAndNewlines)
        let sentences = body.split(separator: ".", omittingEmptySubsequences: true).map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }
        switch mode {
        case "friendlier": return "Thanks so much for bearing with us! " + body + "\n\nLet me know if there's anything else I can do."
        case "more_formal": return "Hello,\n\n" + body + "\n\nKind regards"
        case "shorter": return sentences.prefix(max(1, sentences.count / 2)).joined(separator: ". ") + "."
        case "longer": return body + "\n\nIf anything still looks off after that, reply here with a screenshot and I'll take another look right away."
        case "fix_grammar":
            let fixed = body.prefix(1).uppercased() + body.dropFirst()
            return fixed.hasSuffix(".") || fixed.hasSuffix("?") || fixed.hasSuffix("!") ? fixed : fixed + "."
        default: return sentences.map { $0.replacingOccurrences(of: "approximately", with: "about").replacingOccurrences(of: "utilize", with: "use") }.joined(separator: ". ") + "."
        }
    }

    private static func translate(_ text: String, to target: String) -> (text: String, source: String) {
        let greetings: [String: (String, String)] = [
            "es": ("Hola", "Gracias por tu paciencia."), "de": ("Hallo", "Danke für deine Geduld."), "fr": ("Bonjour", "Merci pour votre patience."),
            "pt": ("Olá", "Obrigado pela paciência."), "it": ("Ciao", "Grazie per la pazienza."), "nl": ("Hallo", "Bedankt voor je geduld."), "ja": ("こんにちは", "お待たせしました。"),
        ]
        guard let (hello, thanks) = greetings[target] else { return (text, "en") }
        let rest = text.replacingOccurrences(of: #"^(Hi|Hello|Hey)\b[^,\n]*,?"#, with: "", options: .regularExpression).trimmingCharacters(in: .whitespacesAndNewlines)
        return ("\(hello),\n\n\(thanks) \(rest)", "en")
    }

    private static func answer(_ question: String, ticket: Ticket) -> JSONValue {
        let sso = ticket.tags.contains { $0.name == "SSO" } || question.localizedCaseInsensitiveContains("certificate")
        let html = sso
            ? "<p>Add the new certificate under <strong>Settings, Security, SAML</strong> and keep the old one active until it expires on June 18. Both are accepted while two are listed, so nobody is signed out.</p><p>Dana already sent the new fingerprint: <code>4F:1A:9C:22:E0:7B:51:D3</code>.</p>"
            : "<p>Based on the conversation, the customer is waiting on a direct answer from the team. Confirm what changed on your side and the next step, then set the ticket to pending.</p>"
        let sources: [JSONValue] = sso
            ? [.object(["article_id": .string(demoID(.made, 9701)), "title": .string("Rotate your SAML signing certificate"), "url": .string("https://help.margin.app/sso/rotate-certificate")]),
               .object(["article_id": .string(demoID(.made, 9702)), "title": .string("Troubleshooting SSO sign-in loops"), "url": .string("https://help.margin.app/sso/loops")])]
            : [.object(["article_id": .string(demoID(.made, 9703)), "title": .string("Replying to customers"), "url": .string("https://help.margin.app/inbox/replying")])]
        return .object(["answer_html": .string(html), "sources": .array(sources)])
    }
}
#endif
