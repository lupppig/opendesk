#if DEBUG
import Foundation

// The ticket composer's and details' routes: ticket panels (contracts/integrations.md
// "Ticket panels"), the reply preview, the ticket's reference, and replies sent from one's
// own Collab address.

extension DemoBackend {
    func ticketDetailRoutes(_ call: Call, in ws: inout DemoWorkspace) throws -> Data?? {
        if !ws.ticketExtras.detailsApplied { applyTicketDetails(&ws) }

        if call.match("GET", "ticket-panels") != nil {
            return .some(encode(JSONValue.object(["data": .array(Self.demoPanels)])))
        }
        if let captured = call.match("GET", "tickets/*/panels/*") {
            let index = try ticket(captured[0], in: ws)
            return .some(encode(panelData(captured[1], ticket: ws.tickets[index])))
        }
        if call.match("POST", "templates/preview") != nil, call.string("kind") == "reply" {
            return .some(encode(replyPreview(call, in: ws)))
        }
        // The reference: checked here, then the core edit applies the rest.
        if let key = call.match("PATCH", "tickets/*")?.first, let value = call.body["alias"] {
            let index = try ticket(key, in: ws)
            let alias = value.string?.trimmingCharacters(in: .whitespaces) ?? ""
            if !alias.isEmpty, alias.range(of: #"^(?=.*[A-Za-z])[A-Za-z0-9-]{2,40}$"#, options: .regularExpression) == nil {
                throw Failure(status: 422, code: "validation_failed", detail: "Use 2 to 40 letters, numbers or hyphens, including at least one letter.")
            }
            if !alias.isEmpty, ws.tickets.contains(where: { $0.id != ws.tickets[index].id && $0.alias?.caseInsensitiveCompare(alias) == .orderedSame }) {
                throw Failure(status: 409, code: "alias_taken", detail: "Another ticket already uses that reference.")
            }
            ws.tickets[index].alias = alias.isEmpty ? nil : alias
            return nil
        }
        // A reply from one of the member's own addresses: sent as any reply, then named after it.
        if let key = call.match("POST", "tickets/*/messages")?.first, let from = call.body["from_account"]?.object, let accountID = from["account_id"]?.string {
            let index = try ticket(key, in: ws)
            guard let account = ws.mail.visibleAccounts.first(where: { $0.id == accountID }) else {
                throw Failure(status: 422, code: "validation_failed", detail: "That address can't send replies.")
            }
            let email = from["email"]?.string?.lowercased() ?? account.email.lowercased()
            let ids = call.body["attachment_ids"]?.array?.compactMap(\.string) ?? []
            var message = send(kind: .reply, html: call.string("body_html") ?? "", attachments: ids.compactMap { uploads[$0] }, on: index, in: &ws)
            let ticketID = ws.tickets[index].id
            message.fromEmail = email
            message.fromName = account.displayName.isEmpty ? world.user.name : account.displayName
            message.fromAccount = MessageFromAccount(accountID: account.id, email: email, replyTo: from["reply_to"]?.string ?? "")
            if let at = ws.messages[ticketID]?.lastIndex(where: { $0.id == message.id }) { ws.messages[ticketID]?[at] = message }
            publish("message.updated", ["ticket_id": .string(ticketID), "message": DemoSeed.json(message)], in: &ws)
            if let status = call.string("status") {
                update(index, with: ["status": .string(status)], by: ActivityActor(type: "user", user: me), in: &ws)
            }
            return .some(encode(message))
        }
        return nil
    }

    // MARK: Seed

    /// Ticket 1072 carries a reference and needs SSO skills; the support mailbox lets members
    /// answer from their own addresses.
    private func applyTicketDetails(_ ws: inout DemoWorkspace) {
        ws.ticketExtras.detailsApplied = true
        if let index = ws.tickets.firstIndex(where: { $0.number == 1072 }) {
            ws.tickets[index].alias = "OAK-SSO"
            ws.tickets[index].requiredSkills = [TicketSkill(id: demoID(.made, 9701), name: "SSO"), TicketSkill(id: demoID(.made, 9702), name: "Enterprise")]
        }
        for (at, mailbox) in ws.ticketExtras.mailboxes.enumerated() {
            guard var object = mailbox.object, object["id"]?.string == DemoSeed.supportMailbox else { continue }
            object["aliases"] = .array([.string("[EMAIL]")])
            object["personal_replies"] = .object(["enabled": .null, "reply_to": .null, "accounts": .string("any")])
            ws.ticketExtras.mailboxes[at] = .object(object)
        }
    }

    private static let shopifyPanel = demoID(.made, 9711)
    private static let accountPanel = demoID(.made, 9712)

    private static var demoPanels: [JSONValue] {
        [
            .object(["id": .string(shopifyPanel), "kind": .string("shopify"), "name": .string("Shopify"), "host": .string("margin-store.myshopify.com"), "enabled": .bool(true), "position": .number(0)]),
            .object(["id": .string(accountPanel), "kind": .string("http"), "name": .string("Margin account"), "host": .string("admin.margin.app"), "enabled": .bool(true), "position": .number(1)]),
        ]
    }

    private func panelData(_ id: String, ticket: Ticket) -> JSONValue {
        func time(_ minutes: Double) -> JSONValue { DemoSeed.json(DemoSeed.ago(minutes)) }
        guard ticket.contact?.email != nil else {
            return .object(["panel_id": .string(id), "state": .string("no_contact"), "fetched_at": time(0)])
        }
        switch id {
        case Self.shopifyPanel:
            guard ticket.number == 1072 || ticket.number == 1068 else {
                return .object(["panel_id": .string(id), "state": .string("empty"), "message": .string("No Shopify customer with this email."), "fetched_at": time(0)])
            }
            return .object([
                "panel_id": .string(id), "state": .string("ok"), "fetched_at": time(2),
                "customer": .object([
                    "name": .string(ticket.contactName), "url": .string("https://admin.shopify.com/store/margin-store/customers/7731"),
                    "email": .string(ticket.contact?.email ?? ""), "orders_count": .number(3), "total_spent": .string("1240.00"), "currency": .string("USD"),
                    "tags": .array([.string("VIP"), .string("Wholesale")]),
                ]),
                "orders": .array([
                    .object([
                        "id": .string("5512"), "number": .string("#1042"), "url": .string("https://admin.shopify.com/store/margin-store/orders/5512"),
                        "created_at": time(3 * 1440), "total": .string("420.00"), "currency": .string("USD"),
                        "financial_status": .string("paid"), "fulfillment_status": .string("fulfilled"), "cancelled": .bool(false),
                        "items": .array([
                            .object(["title": .string("Hardware security key"), "variant": .string("USB-C"), "quantity": .number(4), "price": .string("60.00")]),
                            .object(["title": .string("Setup session"), "quantity": .number(1), "price": .string("180.00")]),
                        ]),
                        "shipping": .object(["city": .string("Portland"), "country": .string("US"), "method": .string("UPS Ground")]),
                        "tracking": .array([.object(["company": .string("UPS"), "number": .string("1Z999AA10123456784"), "url": .string("https://www.ups.com/track?tracknum=1Z999AA10123456784")])]),
                    ]),
                    .object([
                        "id": .string("5307"), "number": .string("#0987"), "url": .string("https://admin.shopify.com/store/margin-store/orders/5307"),
                        "created_at": time(41 * 1440), "total": .string("820.00"), "currency": .string("USD"),
                        "financial_status": .string("partially_refunded"), "fulfillment_status": .string("fulfilled"), "cancelled": .bool(false),
                        "refunded": .string("120.00"), "discount_codes": .array([.string("SPRING10")]),
                        "items": .array([.object(["title": .string("Annual plan, 10 seats"), "quantity": .number(1), "price": .string("820.00")])]),
                    ]),
                ]),
            ])
        default:
            guard ticket.number == 1072 else {
                return .object(["panel_id": .string(id), "state": .string("empty"), "message": .string("No Margin account uses this email."), "fetched_at": time(0)])
            }
            return .object([
                "panel_id": .string(id), "state": .string("ok"), "fetched_at": time(1),
                "blocks": .array([
                    .object([
                        "type": .string("card"), "title": .string("Oakridge Health"), "url": .string("https://admin.margin.app/accounts/acct_oak_2291"),
                        "subtitle": .string("Enterprise, 180 seats"),
                        "badges": .array([.object(["text": .string("Active"), "tone": .string("success")]), .object(["text": .string("SSO"), "tone": .string("info")])]),
                        "links": .array([.object(["label": .string("Admin"), "url": .string("https://admin.margin.app/accounts/acct_oak_2291")])]),
                        "fields": .array([
                            .object(["label": .string("Plan"), "value": .string("Enterprise")]),
                            .object(["label": .string("Renews"), "value": .object(["type": .string("date"), "value": .string("2026-12-01")])]),
                            .object(["label": .string("Annual value"), "value": .object(["type": .string("money"), "amount": .string("43200.00"), "currency": .string("USD")])]),
                        ]),
                    ]),
                    .object([
                        "type": .string("card"), "id": .string("inv-2291-0925"), "title": .string("Invoice September"), "collapsed": .bool(true),
                        "badges": .array([.object(["text": .string("Paid"), "tone": .string("success")])]),
                        "footer": .array([.object(["label": .string("Total"), "value": .object(["type": .string("money"), "amount": .string("3600.00"), "currency": .string("USD")])])]),
                    ]),
                    .object([
                        "type": .string("list"), "title": .string("Recent sign-ins"),
                        "items": .array([
                            .object(["title": .string("SAML sign-in failed"), "subtitle": .string("Okta, certificate mismatch"), "badge": .object(["text": .string("Error"), "tone": .string("danger")])]),
                            .object(["title": .string("SAML sign-in"), "subtitle": .string("Okta"), "value": .object(["type": .string("date"), "value": DemoSeed.json(DemoSeed.ago(1500)), "relative": .bool(true)])]),
                        ]),
                    ]),
                    .object(["type": .string("note"), "text": .string("The SSO certificate was rotated yesterday."), "tone": .string("warning")]),
                ]),
            ])
        }
    }

    /// The reply as the team's address sends it: the brand's layout around the reply and the signature.
    private func replyPreview(_ call: Call, in ws: DemoWorkspace) -> JSONValue {
        let ticket = ws.tickets.first { $0.id == call.string("ticket_id") }
        let body = call.string("body_html") ?? ""
        let signature = "<p>\(HTMLText.escape(world.user.name))<br>Support, \(HTMLText.escape(ws.workspace.name))</p>"
        let html = """
        <div style="font-family:-apple-system,Helvetica,sans-serif;max-width:560px;margin:0 auto;color:#18181b">
        <div style="padding:16px 0;border-bottom:1px solid #e4e4e7;font-weight:600">\(HTMLText.escape(ws.workspace.name))</div>
        <div style="padding:16px 0">\(body)\(signature)</div>
        <div style="padding:12px 0;border-top:1px solid #e4e4e7;color:#71717a;font-size:12px">Ticket #\(ticket?.number ?? 0). Reply to this email to answer.</div>
        </div>
        """
        let problems: [JSONValue] = body.contains("{{") ? [.string("A variable in the reply has no value for this ticket.")] : []
        return .object(["subject": .string("Re: \(ticket?.subject ?? "") [#\(ticket?.number ?? 0)]"), "html": .string(html), "text": .string(HTMLText.plain(from: body)), "problems": .array(problems)])
    }
}
#endif
