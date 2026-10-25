#if DEBUG
import CoreGraphics
import Foundation
import ImageIO
import UniformTypeIdentifiers

// The demo world, after the web app's mock (`web/src/mock/seed.ts`): Maya Chen
// (maya@margin.app), owner of Margin, with Support, Billing and Partnerships,
// and an agent in Lumen Labs. Every time is minutes before `DemoMode.anchor`.
//
// Ticket ids are `0197a0c0-0007-7000-8000-00000000<number>`, and a deep link
// may name the number instead (`/inbox/mine/1072`):
//
//   Mine (Maya)          1072 SSO login loops (urgent, email, full thread, Ollie summary and draft)
//                        1068 Refund for duplicate annual charge (failed delivery, designed receipt)
//                        1045 Volume pricing, 1078 Two-factor codes (SLA breached), 1062 Search in images
//                        (SLA due soon), 1063 DPA (pending), 1074, 1060 (snoozed), 1049 (pending)
//   Unassigned           1086 Pricing question (live chat from Yusuf Moreau, handed off by Ollie)
//                        1069 Nested tags (live chat, waiting), 1067 Mobile crash (SLA breached)
//                        1071 PDF export, 1033 API token permissions (Zoe Petrova), 1085, 1084, 1081, 1075, 1052
//   Teammates            1065 API 429 (Priya), 1064 Webhook signature (on hold, Jonas), 1070 VAT (Lea), and more
//   Solved and closed    1066 (CSAT 5), 1061 (live chat, CSAT 4), 1059, 1057, 1054, 1050, 1047, 1041
//   Spam                 1087, 1053, 1046
//
// Views: Urgent (0197a0c0-0005-…-000000000001), Billing queue (…02), Waiting on engineering (…03).

/// Who a demo id belongs to (the second group of the id).
enum DemoKind: Int {
    case workspace = 1, user, team, tag, view, contact, ticket, message, activity, notification, savedReply, attachment, suggestion, session, device, mailbox, made
}

func demoID(_ kind: DemoKind, _ n: Int) -> String {
    String(format: "0197a0c0-%04x-7000-8000-%012d", kind.rawValue, n)
}

/// One workspace of the demo backend.
struct DemoWorkspace: Sendable {
    var workspace: Workspace
    var role: Role
    var members: [Member] = []
    var teams: [Team] = []
    var tags: [Tag] = []
    var views: [SavedView] = []
    var savedReplies: [SavedReply] = []
    var tickets: [Ticket] = []
    var messages: [String: [Message]] = [:]
    var activities: [String: [Activity]] = [:]
    var suggestedReplies: [String: SuggestedReply] = [:]
    var notifications: [AppNotification] = []
    var presence: [String: [PresenceViewer]] = [:]
    var seq = 0
    // Each area's own data, defined and seeded in its file (`DemoTickets.swift` and the rest).
    var ticketExtras = DemoTicketExtras()
    var inboxExtras = DemoInboxExtras()
    var accountExtras = DemoAccountWorkspace()
    var assistantExtras = DemoAssistantExtras()
    var directory = DemoDirectory()
    var mail = DemoMail()
    var kb = DemoKB()

    var id: String { workspace.id }
}

struct DemoWorld: Sendable {
    var user: User
    var workspaces: [DemoWorkspace]
    var devices: [PushDevice]
    var sessions: [SessionInfo]
    var account = DemoAccount()
}

/// The people of Margin.
enum DemoPerson: Int, CaseIterable, Sendable {
    case maya = 1, jonas, priya, sam, lea, theo

    var id: String { demoID(.user, rawValue) }

    var name: String {
        switch self {
        case .maya: "Maya Chen"
        case .jonas: "Jonas Weber"
        case .priya: "Priya Raman"
        case .sam: "Sam Okafor"
        case .lea: "Lea Martin"
        case .theo: "Theo Brandt"
        }
    }

    var first: String { String(name.split(separator: " ")[0]) }
    var email: String { first.lowercased() + "@margin.app" }

    var role: Role {
        switch self {
        case .maya: .owner
        case .jonas: .admin
        case .theo: .viewer
        default: .agent
        }
    }

    var availability: Availability {
        switch self {
        case .priya: .away
        case .lea: .offline
        default: .online
        }
    }

    var ref: UserRef { UserRef(id: id, name: name, email: email, avatarURL: nil) }
}

enum DemoSeed {
    static let marginID = demoID(.workspace, 1)
    static let lumenID = demoID(.workspace, 2)
    static var me: DemoPerson { .maya }

    static let support = demoID(.team, 1)
    static let billing = demoID(.team, 2)
    static let partnerships = demoID(.team, 3)

    static let supportMailbox = demoID(.mailbox, 1)
    static let billingMailbox = demoID(.mailbox, 2)

    static func ticketID(_ number: Int) -> String { demoID(.ticket, number) }

    static func ago(_ minutes: Double) -> Date { DemoMode.anchor.addingTimeInterval(-minutes * 60) }
    static func ahead(_ minutes: Double) -> Date { DemoMode.anchor.addingTimeInterval(minutes * 60) }

    static func world() -> DemoWorld {
        let user = User(
            id: me.id, email: me.email, name: me.name, avatarURL: nil, avatarSource: nil,
            emailVerified: true, totpEnabled: true, createdAt: ago(380 * 1440)
        )
        let device = PushDevice(demoID: demoID(.device, 1), name: "iPhone", appVersion: AppInfo.version, createdAt: ago(12 * 1440))
        let sessions = [
            SessionInfo(id: demoID(.session, 1), userAgent: "opendesk iOS", ip: "84.152.33.90", createdAt: ago(12 * 1440), lastUsedAt: ago(0), current: true, client: "ios"),
            SessionInfo(id: demoID(.session, 2), userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 15_6) AppleWebKit/605.1.15 Chrome/141.0 Safari/537.36", ip: "84.152.33.17", createdAt: ago(3 * 1440), lastUsedAt: ago(1), current: false, client: "web"),
            SessionInfo(id: demoID(.session, 3), userAgent: "opendesk-desktop/1.4.0", ip: "62.214.8.201", createdAt: ago(40 * 1440), lastUsedAt: ago(9 * 1440), current: false, client: "desktop"),
        ]
        var workspaces = [margin(), lumen()]
        for index in workspaces.indices { seedAreas(&workspaces[index]) }
        var world = DemoWorld(user: user, workspaces: workspaces, devices: [device], sessions: sessions)
        world.account = DemoAccount.seed(user: user)
        return world
    }

    /// Fills each area's data once the workspace's people, labels and tickets exist.
    static func seedAreas(_ ws: inout DemoWorkspace) {
        ws.ticketExtras = DemoTicketExtras.seed(ws)
        ws.inboxExtras = DemoInboxExtras.seed(ws)
        ws.accountExtras = DemoAccountWorkspace.seed(ws)
        ws.assistantExtras = DemoAssistantExtras.seed(ws)
        ws.directory = DemoDirectory.seed(ws)
        ws.mail = DemoMail.seed(ws)
        ws.kb = DemoKB.seed(ws)
    }
}

// MARK: - Margin

extension DemoSeed {
    static func margin() -> DemoWorkspace {
        let workspace = Workspace(
            id: marginID, name: "Margin", slug: "margin", avatarURL: nil,
            settings: WorkspaceSettings(brandColor: "#4f46e5", timezone: DemoMode.timeZone, statusLabels: nil, priorityLabels: nil),
            createdAt: ago(400 * 1440)
        )
        var ws = DemoWorkspace(workspace: workspace, role: .owner)
        ws.members = DemoPerson.allCases.enumerated().map { index, person in
            Member(
                user: MemberUser(id: person.id, name: person.name, email: person.email, avatarURL: nil),
                role: person.role, availability: person.availability, presence: person.availability,
                openTickets: nil, lastSeenAt: ago(Double(index + 1) * 7)
            )
        }
        ws.teams = [
            Team(id: support, name: "Support", memberIDs: [DemoPerson.maya, .jonas, .priya, .sam, .theo].map(\.id)),
            Team(id: billing, name: "Billing", memberIDs: [DemoPerson.lea, .maya, .sam].map(\.id)),
            Team(id: partnerships, name: "Partnerships", memberIDs: [DemoPerson.sam, .maya, .jonas].map(\.id)),
        ]
        let labels: [(String, String, String)] = [
            ("Question", "blue", "How do I, can I, where is"),
            ("Problem", "orange", "Something is not working as expected"),
            ("Bug", "red", "A defect in the product"),
            ("Feature request", "violet", "Ideas and asks for something new"),
            ("Billing", "amber", "Invoices, plans and payments"),
            ("Refund", "rose", "Money goes back to the customer"),
            ("Escalated", "red", "Handed to engineering or a lead"),
            ("VIP", "amber", "A customer to look after closely"),
            ("Feedback", "teal", "Praise, complaints and suggestions"),
            ("SSO", "indigo", "SAML, Okta and Azure AD sign-in"),
            ("API", "cyan", "REST API, webhooks and tokens"),
            ("Sync", "teal", "Sync between devices and the web app"),
            ("Export", "lime", "PDF, Markdown and backup exports"),
            ("Mobile", "pink", "iOS and Android apps"),
            ("Onboarding", "yellow", "Workspace setup and first steps"),
        ]
        ws.tags = labels.enumerated().map { index, label in
            Tag(id: demoID(.tag, index + 1), name: label.0, color: label.1, description: label.2, groupID: nil, archived: false, appliesTo: ["tickets"])
        }
        ws.views = [
            SavedView(id: demoID(.view, 1), name: "Urgent", shared: true, filters: TicketFilters(status: ["open", "pending"], priority: ["urgent", "high"]), position: 0),
            SavedView(id: demoID(.view, 2), name: "Billing queue", shared: false, filters: TicketFilters(status: ["open", "pending", "on_hold"], teamID: billing), position: 1),
            SavedView(id: demoID(.view, 3), name: "Waiting on engineering", shared: true, filters: TicketFilters(status: ["on_hold", "pending"], tag: "Bug"), position: 2),
        ]
        ws.savedReplies = [
            ("Greeting and thanks", "thanks", "<p>Hi {{contact.first_name}},</p><p>Thanks for reaching out. I'm looking into this now and will get back to you shortly.</p>"),
            ("Ask for steps to reproduce", "repro", "<p>Could you share the steps that lead to this, the browser or app version you're on, and roughly when it happened? A screenshot helps too.</p>"),
            ("Refund processed", "refund", "<p>I've processed the refund. It usually shows on your statement within 5 to 10 business days, depending on your bank.</p>"),
            ("SSO setup guide", "sso", "<p>Here is our step-by-step guide for setting up SAML single sign-on: <a href=\"https://help.margin.app/sso\">help.margin.app/sso</a>. Your ACS URL is shown in Settings, then Security.</p>"),
            ("Feature request logged", "fr", "<p>Thanks for the idea. I've logged it with our product team and added your use case. We'll let you know if it lands on the roadmap.</p>"),
            ("Closing, no reply", "close", "<p>I haven't heard back in a few days, so I'll close this for now. Just reply to this email if you need anything else and it will reopen.</p>"),
        ].enumerated().map { index, reply in
            SavedReply(id: demoID(.savedReply, index + 1), title: reply.0, shortcut: reply.1, bodyHTML: reply.2)
        }

        for spec in marginTickets() { add(spec, to: &ws) }
        ws.suggestedReplies = [
            ticketID(1072): SuggestedReply(
                demoID: demoID(.suggestion, 1072), ticketID: ticketID(1072), messageID: demoID(.message, 107_205),
                bodyHTML: p("Hi Dana,", "Thanks for confirming. I've added the new certificate (4F:1A:9C:22:E0:7B:51:D3) to your SSO connection and kept the old one active until it expires next week, so nothing else needs to change on your side.", "Could you ask one person to try the Okta tile again and tell me it works?", "Maya"),
                createdAt: ago(5)
            ),
            ticketID(1068): SuggestedReply(
                demoID: demoID(.suggestion, 1068), ticketID: ticketID(1068), messageID: demoID(.message, 106_804),
                bodyHTML: p("Hi Tomás,", "Sorry for the silence. My earlier reply didn't reach you. The duplicate charge of €4,320 was refunded on June 12 and should be on your statement within 5 to 10 business days.", "The refund reference is RF-2026-06-0193, in case your bank asks.", "Maya"),
                createdAt: ago(34)
            ),
        ]
        ws.presence = [
            ticketID(1072): [PresenceViewer(userID: DemoPerson.priya.id, name: DemoPerson.priya.name, avatar: nil, state: .replying)],
            ticketID(1068): [PresenceViewer(userID: DemoPerson.lea.id, name: DemoPerson.lea.name, avatar: nil, state: .viewing)],
            ticketID(1067): [PresenceViewer(userID: DemoPerson.jonas.id, name: DemoPerson.jonas.name, avatar: nil, state: .viewing)],
            ticketID(1086): [PresenceViewer(userID: DemoPerson.sam.id, name: DemoPerson.sam.name, avatar: nil, state: .viewing)],
        ]
        ws.notifications = marginNotifications(ws)
        return ws
    }

    static func marginNotifications(_ ws: DemoWorkspace) -> [AppNotification] {
        func ticket(_ number: Int) -> NotificationTicket? {
            ws.tickets.first { $0.number == number }.map { NotificationTicket(id: $0.id, number: $0.number, subject: $0.subject) }
        }
        func contact(_ number: Int) -> JSONValue {
            let ref = ws.tickets.first { $0.number == number }?.contact
            return .object(["id": .string(ref?.id ?? ""), "name": .string(ref?.name ?? "")])
        }
        let jonas = json(DemoPerson.jonas.ref)
        let rows: [(String, Int?, [String: JSONValue], Double, Double?)] = [
            ("ai_handoff", 1086, ["via": .string("contact"), "contact": contact(1086), "preview": .string("Thanks. We're 140 people, so can I talk to someone about volume pricing?")], 3.6, nil),
            ("customer_replied", 1072, ["contact": contact(1072), "message_id": .string(demoID(.message, 107_205)), "preview": .string("You were right, our identity team rotated the certificate last night without telling us.")], 6, nil),
            ("new_ticket", 1071, ["channel": .string("email"), "contact": contact(1071)], 14, nil),
            ("customer_replied", 1045, ["contact": contact(1045), "preview": .string("That works for us. Can you send a quote for 520 seats, billed yearly?")], 25, nil),
            ("customer_replied", 1068, ["contact": contact(1068), "preview": .string("Hi, just checking in on this. I haven't heard back yet. Is the refund being processed?")], 35, nil),
            ("mentioned", 1072, ["actor": jonas, "preview": .string("Confirmed. Every assertion since 07:38 is signed with a cert we don't have.")], 61, nil),
            ("sla_breached", 1067, ["kind": .string("first_response")], 130, nil),
            ("team_joined", nil, ["team_id": .string(partnerships), "team": .object(["id": .string(partnerships), "name": .string("Partnerships"), "private": .bool(false)]), "user": .object(["id": .string(DemoPerson.jonas.id), "name": .string(DemoPerson.jonas.name)]), "by": .string(DemoPerson.jonas.id)], 180, 150),
            ("assigned", 1068, ["actor": .null], 299, 240),
            ("assigned", 1062, ["actor": jonas], 435, 400),
            ("mentioned", 1070, ["actor": json(DemoPerson.lea.ref), "preview": .string("Filed with billing engineering: VAT changes should regenerate open invoices.")], 1495, 1200),
            ("import_finished", nil, ["source": .string("zendesk"), "source_name": .string("Zendesk"), "status": .string("completed"), "tickets": .number(1284), "articles": .number(42)], 2880, 2800),
            ("customer_replied", 1063, ["contact": contact(1063), "preview": .string("Thanks Maya, our compliance team is reviewing the DPA this week.")], 4080, 3600),
        ]
        return rows.enumerated().map { index, row in
            AppNotification(demoID: demoID(.notification, index + 1), type: row.0, ticket: row.1.flatMap(ticket), data: row.2, readAt: row.4.map(ago), createdAt: ago(row.3))
        }
    }
}

// MARK: - Lumen Labs

extension DemoSeed {
    static func lumen() -> DemoWorkspace {
        let workspace = Workspace(
            id: lumenID, name: "Lumen Labs", slug: "lumen", avatarURL: nil,
            settings: WorkspaceSettings(brandColor: "#0c9488", timezone: "America/New_York", statusLabels: nil, priorityLabels: nil),
            createdAt: ago(200 * 1440)
        )
        var ws = DemoWorkspace(workspace: workspace, role: .agent)
        let owner = MemberUser(id: demoID(.user, 20), name: "Ruth Adler", email: "ruth@lumenlabs.io", avatarURL: nil)
        ws.members = [
            Member(user: owner, role: .owner, availability: .online, presence: .online, openTickets: nil, lastSeenAt: ago(12)),
            Member(user: MemberUser(id: me.id, name: me.name, email: me.email, avatarURL: nil), role: .agent, availability: .online, presence: .online, openTickets: nil, lastSeenAt: ago(0)),
        ]
        ws.tags = [Tag(id: demoID(.tag, 101), name: "Question", color: "blue", description: nil, groupID: nil, archived: false, appliesTo: ["tickets"])]
        let ruth = UserRef(id: owner.id, name: owner.name, email: owner.email, avatarURL: nil)
        let specs = [
            Spec(number: 214, subject: "Dashboard export times out", contact: lumenContact(1, "Owen Price", "owen@hollis.co"), status: .open, priority: .high,
                 thread: [c(50, p("Hi,", "Exporting the usage dashboard as CSV spins for a minute, then fails. Smaller date ranges work.", "Owen"))]),
            Spec(number: 213, subject: "Add a second admin", contact: lumenContact(2, "Mei Tan", "mei@quarry.dev"), status: .pending, priority: .normal, assignee: .maya,
                 thread: [c(600, p("Hello,", "Our CTO is leaving. How do we make someone else an admin before then?", "Mei")), a(.maya, 560, p("Hi Mei,", "An owner can change roles under Settings, then Members. I've asked Ruth to promote Dan for you.", "Maya"))]),
            Spec(number: 212, subject: "Webhook for new signups", contact: lumenContact(3, "Ivo Brandt", "ivo@fernway.io"), status: .open, priority: .normal, assignee: .maya,
                 thread: [c(1500, p("Is there a webhook when someone signs up for our workspace? We'd like to post it to Slack.", "Ivo"))]),
            Spec(number: 211, subject: "Invoice address change", contact: lumenContact(4, "Sara Lund", "sara@northgate.se"), status: .solved, priority: .low, assignee: .maya,
                 thread: [c(4000, p("Please put our new office address on future invoices.", "Sara")), a(.maya, 3900, p("Hi Sara,", "Done, the next invoice will show the new address.", "Maya"))]),
        ]
        for spec in specs { add(spec, to: &ws, mailbox: nil, triage: false) }
        let ticket214 = ws.tickets.first { $0.number == 214 }
        let ticket212 = ws.tickets.first { $0.number == 212 }
        ws.notifications = [
            AppNotification(demoID: demoID(.notification, 101), type: "customer_replied", ticket: ticket214.map { NotificationTicket(id: $0.id, number: $0.number, subject: $0.subject) }, data: ["contact": .object(["name": .string("Owen Price")])], readAt: nil, createdAt: ago(50)),
            AppNotification(demoID: demoID(.notification, 102), type: "assigned", ticket: ticket212.map { NotificationTicket(id: $0.id, number: $0.number, subject: $0.subject) }, data: ["actor": json(ruth)], readAt: nil, createdAt: ago(1490)),
        ]
        return ws
    }

    static func lumenContact(_ n: Int, _ name: String, _ email: String) -> ContactRef {
        ContactRef(id: demoID(.contact, 100 + n), email: email, name: name, avatarURL: nil)
    }
}

// MARK: - Margin's tickets

extension DemoSeed {
    static func contact(_ n: Int, _ name: String, _ email: String) -> ContactRef {
        ContactRef(id: demoID(.contact, n), email: email, name: name, avatarURL: nil)
    }

    static let dana = contact(1, "Dana Whitfield", "dana@oakridgehealth.org")
    static let marcus = contact(2, "Marcus Lindqvist", "marcus@northwind.com")
    static let tomas = contact(3, "Tomás García", "tomas@brightline.io")
    static let elena = contact(4, "Elena Rossi", "elena@halcyon.studio")
    static let hugo = contact(5, "Hugo Moreau", "hugo@vantagelegal.com")
    static let chloe = contact(6, "Chloé Dubois", "chloe@paperplane.co")
    static let ben = contact(7, "Ben Hartmann", "ben@meridian.edu")
    static let freya = contact(8, "Freya Nilsen", "freya@fjord.ai")
    static let gabriel = contact(9, "Gabriel Laurent", "gabriel@copperleaf.dev")
    static let aisha = contact(10, "Aisha Bello", "aisha@kestrel.tech")
    static let isaac = contact(11, "Isaac Levy", "isaac@tidewater.fm")
    static let julia = contact(12, "Julia Novak", "julia@solsticebakery.com")
    static let yusuf = contact(13, "Yusuf Moreau", "yusuf.moreau@gmail.com")
    static let zoe = contact(14, "Zoe Petrova", "zoe.petrova@proton.me")
    static let nina = contact(15, "Nina Fischer", "nina.fischer@icloud.com")
    static let laura = contact(16, "Laura Silva", "laura.silva@fastmail.com")
    static let mateo = contact(17, "Mateo Costa", "mateo.costa@outlook.com")
    static let oliver = contact(18, "Oliver Grant", "oliver@kestrel.tech")
    static let paula = contact(19, "Paula Ortega", "paula@paperplane.co")
    static let stefan = contact(20, "Stefan Bauer", "stefan@northwind.com")
    static let tara = contact(21, "Tara Singh", "tara.singh@gmail.com")
    static let umar = contact(22, "Umar Haddad", "umar@fjord.ai")
    static let vera = contact(23, "Vera Ivanova", "vera@halcyon.studio")
    static let will = contact(24, "Will Turner", "will.turner@proton.me")
    static let rosa = contact(25, "Rosa Reyes", "rosa@brightline.io")
    static let kofi = contact(26, "Kofi Mensah", "kofi@meridian.edu")
    static let daniel = contact(27, "Daniel Kim", "daniel.kim@gmail.com")
    static let hana = contact(28, "Hana Sato", "hana@copperleaf.dev")
    static let anders = contact(29, "Anders Holm", "anders@oakridgehealth.org")
    static let ines = contact(30, "Ines Martins", "ines.martins@gmail.com")

    static func mention(_ person: DemoPerson) -> String {
        "<span class=\"mention\" data-id=\"\(person.id)\">@\(person.name)</span>"
    }

    static func summary(_ text: String, _ points: [String], _ sentiment: Sentiment, at minutes: Double) -> AISummary {
        AISummary(text: text, keyPoints: points, sentiment: sentiment, updatedAt: ago(minutes))
    }

    static func suggestion(_ value: JSONValue, _ confidence: Double, at minutes: Double) -> JSONValue {
        .object(["value": value, "confidence": .number(confidence), "probability": .number(min(0.99, confidence + 0.08)), "model": .string("jev"), "decided_at": .string(stamp(ago(minutes)))])
    }

    fileprivate static func marginTickets() -> [Spec] {
        [
            // Mine.
            Spec(number: 1072, subject: "SSO login loops back to sign-in page", contact: dana, status: .open, priority: .urgent, severity: .sev1, assignee: .maya, team: support, tags: ["SSO", "Escalated", "Problem"], ollieRaised: true,
                 thread: [
                     c(95, p("Hi Margin team,", "Since this morning nobody at Oakridge can sign in through Okta. After entering credentials we land on the Margin sign-in page again, and it loops. Clearing cookies doesn't help.") + "<ul><li>Started around 07:40 CEST</li><li>Affects all 180 users on the Okta tile</li><li>Password login for our admin account still works</li></ul>" + p("Screenshot of the network tab attached. This is blocking our clinical documentation team, so any help is appreciated.", "Dana Whitfield<br>IT Operations, Oakridge Health"), files: ["okta-network-tab.png"]),
                     a(.maya, 82, p("Hi Dana,", "Thanks for the detailed report and the screenshot, that helps a lot. I can see the SAML responses reaching us, but they're being rejected before a session is created.", "Could you check whether the signing certificate in your Okta app was rotated recently? If it was, send me the new certificate fingerprint and I'll compare it with what we have on file.", "Best,<br>Maya")),
                     n(.maya, 78, p("\(mention(.jonas)) can you pull the SAML assertion logs for Oakridge from the last 3 hours? I suspect a certificate rotation on their side, but the error code is <code>invalid_signature</code> on every request.")),
                     n(.jonas, 61, p("Confirmed. Every assertion since 07:38 is signed with a cert we don't have (fingerprint starts with <code>4F:1A:9C</code>). The old one expires next week, so they rotated early. Once she confirms we can add the new cert and keep both active.")),
                     c(6, p("Hi Maya,", "You were right, our identity team rotated the certificate last night without telling us. The new fingerprint is <code>4F:1A:9C:22:E0:7B:51:D3</code>.", "Can you add it on your side? We'd rather not roll back.", "Thanks,<br>Dana"), quoted: true),
                 ],
                 summary: summary("Nobody at Oakridge Health can sign in through Okta since 07:40: every SAML assertion is signed with a certificate Margin doesn't have yet. Dana confirmed their identity team rotated it early and sent the new fingerprint.", ["All 180 users on the Okta tile are blocked; password login still works", "Jonas confirmed every assertion since 07:38 uses the new certificate", "Next: add 4F:1A:9C:22:E0:7B:51:D3 and keep both certificates active until the old one expires"], .negative, at: 5)),
            Spec(number: 1068, subject: "Refund for duplicate annual charge", contact: tomas, status: .open, priority: .high, severity: .sev3, assignee: .maya, team: billing, tags: ["Billing", "Refund"], mailbox: billingMailbox,
                 thread: [
                     c(300, p("Hello,", "We were charged twice for our annual renewal on June 9 (2 × €4,320). Can you refund the duplicate? Card ending 4417.", "Tomás García, Brightline Logistics")),
                     c(298, receipt),
                     a(.maya, 260, p("Hi Tomás,", "You're right, the renewal retried after a timeout and both attempts went through. I've refunded the second charge of €4,320. It usually shows on your statement within 5 to 10 business days.", "Apologies for the trouble.", "Maya"), delivery: "failed"),
                     c(35, p("Hi, just checking in on this. I haven't heard back yet. Is the refund being processed?")),
                 ],
                 summary: summary("Brightline Logistics was charged twice for its annual renewal. Maya refunded the duplicate, but her reply bounced, so Tomás hasn't heard back and is asking again.", ["Duplicate charge of €4,320 on June 9, card ending 4417", "Maya's reply failed to deliver", "Next: resend the refund confirmation"], .frustrated, at: 34)),
            Spec(number: 1045, subject: "Volume pricing for 500 seats", contact: stefan, status: .open, priority: .high, assignee: .maya, team: billing, tags: ["Billing", "VIP", "Question"], mailbox: billingMailbox,
                 thread: [
                     c(190, p("Hi,", "We're evaluating Margin for the whole company. Do you offer volume pricing over 500 seats?", "Stefan Bauer<br>Northwind Traders")),
                     a(.maya, 140, p("Hi Stefan,", "We do. Above 500 seats the Business plan drops to $11 per member per month on a yearly contract, and you get a named contact for onboarding.", "Happy to send a quote whenever you're ready.", "Maya")),
                     c(25, p("That works for us. Can you send a quote for 520 seats, billed yearly?", "Stefan")),
                 ]),
            Spec(number: 1078, subject: "Two-factor codes not accepted", contact: will, status: .open, priority: .high, assignee: .maya, team: support, tags: ["Problem"],
                 thread: [
                     c(1800, p("Hi,", "My authenticator codes are rejected every time since I got a new phone. I moved the app over with the transfer tool.", "Thanks,<br>Will")),
                     a(.maya, 1680, p("Hi Will,", "Transferred authenticators sometimes keep the old phone's clock offset. Could you turn on automatic time on the new phone and try a fresh code?", "Maya")),
                     c(50, p("I tried that but no luck. Same result as before.")),
                 ]),
            Spec(number: 1062, subject: "Search doesn't find text inside images", contact: isaac, status: .open, priority: .normal, assignee: .maya, team: support, tags: ["Feature request"],
                 thread: [c(440, p("We scan a lot of handwritten notes into Margin. Is there a way to search the text inside those images? Right now search only finds the captions.", "Isaac"))]),
            Spec(number: 1063, subject: "Can we get a DPA signed?", contact: hugo, status: .pending, priority: .normal, assignee: .maya, tags: ["Question"],
                 thread: [
                     c(4200, p("Hello,", "Before we roll Margin out firm-wide our compliance team needs a signed data processing agreement, and confirmation that our data stays in the EU region.", "Hugo Moreau, Vantage Legal")),
                     a(.maya, 4100, p("Hi Hugo,", "Of course. Our standard DPA is attached, already countersigned on our side. Your workspace is hosted in Frankfurt and backups stay in the EU.", "Once your team signs, reply with the PDF and we'll file it.", "Maya"), files: ["margin-dpa-2026.pdf"]),
                 ]),
            Spec(number: 1074, subject: "Offline mode on Android", contact: tara, status: .open, priority: .normal, assignee: .maya, team: support, tags: ["Mobile", "Bug"], sla: false,
                 thread: [
                     c(2900, p("Hi,", "Notes I write offline on Android disappear when I reconnect. This has happened three times now.", "Tara")),
                     a(.maya, 2700, p("Hi Tara,", "Thanks for reaching out. I've reproduced this on our side and passed it to the engineering team with your details. I'll update you as soon as there's a fix.", "Maya")),
                 ]),
            Spec(number: 1060, subject: "Imported Evernote notes lost formatting", contact: vera, status: .onHold, priority: .normal, assignee: .maya, team: support, tags: ["Bug", "Onboarding"], sla: false, snoozedUntil: Date(timeIntervalSince1970: 1_781_679_600),
                 thread: [
                     c(5800, p("Hi,", "We imported 3,000 notes from Evernote. Tables and checklists came through as plain text.", "Vera")),
                     a(.maya, 5600, p("Hi Vera,", "The importer team has a fix for tables in this week's release. I'll check back with you tomorrow morning once it's out.", "Maya")),
                 ]),
            Spec(number: 1049, subject: "Setting up SAML with Google Workspace", contact: anders, status: .pending, priority: .normal, assignee: .maya, team: support, tags: ["SSO", "Question"], sla: false,
                 thread: [
                     c(7400, p("Following your guide, but Google asks for an ACS URL that isn't listed. Where can I find it?", "Anders")),
                     a(.maya, 7300, p("Hi Anders,", "Here is our step-by-step guide for setting up SAML single sign-on: help.margin.app/sso. Your ACS URL is shown in Settings, then Security.", "Maya")),
                 ]),

            // Unassigned.
            Spec(number: 1086, subject: "Pricing question", contact: yusuf, status: .open, priority: .normal, channel: .chat, team: billing, tags: ["Question"], handoffAt: 3 + 40.0 / 60,
                 thread: [
                     c(9, p("Hi! Is the Business plan billed per member or per workspace?")),
                     o(8.5, p("The Business plan is billed per member: $15 per member per month, or $12 when billed yearly. Guests are free, and you only pay for members who can sign in.", "Pricing details are at margin.app/pricing.")),
                     c(4, p("Thanks. We're 140 people, so can I talk to someone about volume pricing?")),
                     o(3 + 40.0 / 60, p("Of course. I'll bring in someone from the team. They'll reply right here.")),
                 ],
                 summary: summary("Yusuf is comparing plans for a 140-person company. Ollie answered that Business is billed per member, then handed the chat to the team for volume pricing.", ["Asked whether Business is billed per member or per workspace", "Wants to talk to a person about volume pricing for 140 people", "Waiting in live chat since 09:26"], .positive, at: 3)),
            Spec(number: 1069, subject: "Feature request: nested tags", contact: chloe, status: .open, priority: .low, channel: .chat, tags: ["Feature request"],
                 thread: [
                     c(41, p("hey! quick one: is there any way to nest tags? like <code>clients/acme</code> and <code>clients/globex</code>")),
                     c(40, p("we have ~200 tags now and the sidebar is getting wild 😅")),
                 ],
                 suggestions: ["team": suggestion(.string(support), 0.62, at: 40)]),
            Spec(number: 1067, subject: "Mobile app crashes when opening shared notebook", contact: aisha, status: .open, priority: .urgent, severity: .sev2, team: support, tags: ["Bug", "Mobile"], ollieRaised: true,
                 thread: [c(190, p("Hi,", "The iOS app (4.12.0) crashes immediately when any of our field engineers open the \u{201C}Site inspections\u{201D} shared notebook. Other notebooks open fine. iPad and iPhone, iOS 26.0.1.", "Crash screenshot and the log exported from TestFlight attached. About 30 people are affected and they can't fill in their reports.", "Aisha Bello<br>Kestrel Robotics"), files: ["crash-screenshot.png", "crash-report.log"])],
                 summary: summary("About 30 field engineers at Kestrel Robotics can't open the \u{201C}Site inspections\u{201D} notebook: the iOS app crashes on open. Other notebooks work.", ["iOS app 4.12.0 on iOS 26.0.1, iPhone and iPad", "Crash log from TestFlight attached", "No reply yet and the first response target has passed"], .negative, at: 120)),
            Spec(number: 1071, subject: "Can't export notebook to PDF", contact: elena, status: .open, priority: .high, team: support, tags: ["Bug", "Export"],
                 thread: [c(14, p("Hello,", "When I export our \u{201C}Client brief\u{201D} notebook to PDF the download starts, then fails with \u{201C}Export failed, try again later\u{201D}. Smaller notebooks export fine. This one has about 60 pages and a lot of images.", "I attached what I get when I export a single page from it, the images are missing. We need this for a client presentation tomorrow morning.", "Elena"), files: ["export-preview.png", "client-brief-page-1.pdf"])],
                 suggestions: ["severity": suggestion(.string("sev2"), 0.58, at: 13)]),
            Spec(number: 1085, subject: "Question about team pricing", contact: nina, status: .open, priority: .normal, team: support, tags: ["Question"],
                 thread: [c(35, p("Name: Nina Fischer<br>Topic: Pricing", "We're a team of 14 writers. Do you offer a discount for nonprofits, and can we pay yearly by invoice?"))]),
            Spec(number: 1084, subject: "Newsletter scheduled for 9:00 went out at 10:00", contact: laura, status: .open, priority: .high, team: support, tags: ["Bug"], ollieRaised: true,
                 thread: [c(100, p("Hi,", "My Tuesday issue was scheduled for 9:00 Berlin time but subscribers got it an hour late.", "Laura"))]),
            Spec(number: 1081, subject: "Refund for a paid subscriber", contact: mateo, status: .open, priority: .normal, team: billing, tags: ["Refund"], mailbox: billingMailbox, sla: false,
                 thread: [c(660, p("Hi,", "A reader paid twice for an annual subscription. How do I refund one of the charges?", "Mateo"))]),
            Spec(number: 1075, subject: "Upgrade from Team to Business mid-cycle", contact: daniel, status: .open, priority: .normal, team: billing, tags: ["Question"], mailbox: billingMailbox,
                 thread: [c(300, p("Hi,", "If we upgrade halfway through the month, how is the difference billed?", "Daniel"))],
                 suggestions: ["labels": suggestion(.array([.string("Billing")]), 0.64, at: 299)]),
            Spec(number: 1033, subject: "API token permissions", contact: zoe, status: .open, priority: .normal, team: support, tags: ["API", "Question"],
                 thread: [
                     c(2880, p("Hi,", "Is it possible to create a read-only API token that can only access one notebook?", "Thanks,<br>Zoe")),
                     a(.sam, 2700, p("Hi Zoe,", "Good question. Tokens can be read-only today, but they see every notebook the creator can. I've asked the API team whether per-notebook tokens are planned.", "Sam")),
                     c(180, p("Any update on this? We'd like to give our contractor a token this week.")),
                 ]),
            Spec(number: 1052, subject: "Wrong timezone on reminders", contact: ines, status: .open, priority: .low, team: support, tags: ["Bug"],
                 thread: [
                     c(5900, p("Reminders fire an hour late since the clocks changed. My timezone is set to Europe/Lisbon.", "Ines")),
                     a(.priya, 5700, p("Hi Ines,", "Sorry for the trouble. Could you send me the exact time it happened and the browser you were using? That will help us find it in the logs.", "Priya")),
                     c(4300, p("It was this morning at 8:00, in Safari on my Mac.")),
                 ]),

            // Teammates.
            Spec(number: 1065, subject: "API returns 429 when syncing notes", contact: freya, status: .open, priority: .high, channel: .api, assignee: .priya, team: support, tags: ["API"],
                 thread: [
                     c(120, p("Our nightly sync started failing with <code>429 Too Many Requests</code> after about 1,200 calls. We're well under the documented 10k/hour.") + "<pre><code>GET /v1/notes?updated_since=2026-06-15T00:00:00Z\nHTTP/1.1 429\nRateLimit-Remaining: 0\nRateLimit-Reset: 3600</code></pre>" + p("Has something changed?")),
                     a(.priya, 1, p("Hi Freya,", "Thanks for the request log. The limit you're hitting is the per-endpoint burst limit on <code>/v1/notes</code> (1,200 per 10 minutes), not the hourly one. If you page with <code>cursor</code> instead of <code>updated_since</code> windows, you'll need about a tenth of the calls.", "I've also raised your burst limit to 3,000 for tonight's run.", "Priya"), delivery: "pending"),
                 ]),
            Spec(number: 1064, subject: "Webhook signature doesn't validate", contact: gabriel, status: .onHold, priority: .normal, assignee: .jonas, team: support, tags: ["API", "Bug"], sla: false, snoozedUntil: Date(timeIntervalSince1970: 1_781_766_000),
                 thread: [
                     c(3000, p("We're verifying <code>OD-Signature</code> exactly like the docs show, but about 1 in 20 deliveries fails validation. The failing ones all have unicode in the note title.")),
                     a(.jonas, 2820, p("Hi Gabriel,", "Good catch. Make sure you compute the HMAC over the raw request bytes, before any JSON parsing or re-encoding. Some frameworks normalize unicode escapes, which changes the body.", "If you're already doing that, send me two event ids that failed and I'll compare them with what we signed.")),
                     c(1800, p("We use the raw body. Failing ids: <code>0192f0c4-77ab</code> and <code>0192f0c9-1e02</code>.")),
                     n(.jonas, 1740, p("Reproduced. Our relay re-serializes titles with escaped unicode. Fix is in review, putting this on hold until it ships.")),
                 ]),
            Spec(number: 1070, subject: "Invoice shows wrong VAT number", contact: marcus, status: .pending, priority: .normal, assignee: .lea, team: billing, tags: ["Billing"], mailbox: billingMailbox,
                 thread: [
                     c(1560, p("Hi,", "Our May invoice (INV-2026-05-0412) lists our old VAT number DE298113420. We changed it in the billing settings two weeks ago. Our accounting can't book the invoice like this.", "Could you reissue it?", "Marcus Lindqvist<br>Finance, Northwind Traders")),
                     a(.lea, 1500, p("Hi Marcus,", "Sorry about that. The change was saved after the invoice was generated, so the May run still used the old number. I've voided INV-2026-05-0412 and issued INV-2026-05-0498 with DE311402967.", "Can you confirm the new one works for your accounting team?", "Lea")),
                     n(.lea, 1495, p("Filed with billing engineering: VAT changes should regenerate open invoices. Linked in the billing queue. \(mention(.maya)) FYI for the Northwind renewal.")),
                 ]),
            Spec(number: 1083, subject: "Custom domain for my publication", contact: paula, status: .open, priority: .normal, channel: .chat, assignee: .priya, team: support, tags: ["Question"],
                 thread: [
                     c(420, p("I added the CNAME record two days ago but the domain still shows as pending.")),
                     a(.priya, 410, p("Hi Paula! DNS changes can take up to 48 hours. Once the CNAME points at domains.pressroom.app, the domain switches to active on its own. I'll keep an eye on it.")),
                 ]),
            Spec(number: 1080, subject: "Reseller agreement for the Nordics", contact: umar, status: .open, priority: .normal, assignee: .sam, team: partnerships, tags: ["Question"],
                 thread: [c(1560, p("Hi,", "We'd like to resell Margin to our customers in Norway and Sweden. Who should we talk to about terms?", "Umar"))]),
            Spec(number: 1079, subject: "Co-marketing webinar in September", contact: paula, status: .pending, priority: .low, assignee: .sam, team: partnerships, sla: false,
                 thread: [
                     c(3000, p("Paperplane would love to run a joint webinar about note-taking for startups. Interested?", "Paula")),
                     a(.sam, 2960, p("Hi Paula,", "We'd love that. I'll come back with a few dates after our planning meeting on Thursday.", "Sam")),
                 ]),
            Spec(number: 1082, subject: "Import subscribers from Substack", contact: kofi, status: .pending, priority: .normal, assignee: .sam, team: support, tags: ["Onboarding"],
                 thread: [
                     c(1680, p("I exported my list from Substack as CSV. The import says 0 subscribers found.", "Kofi")),
                     a(.sam, 1640, p("Hi Kofi,", "Thanks, I'm on it and will get back to you shortly.", "Sam")),
                 ]),
            Spec(number: 1077, subject: "Change billing email address", contact: rosa, status: .pending, priority: .low, assignee: .lea, team: billing, tags: ["Billing"], mailbox: billingMailbox, sla: false,
                 thread: [
                     c(2200, p("Invoices still go to our former office manager. Can you send them to accounts@ from now on?", "Rosa")),
                     a(.lea, 2100, p("Hi Rosa,", "I've made the change on our side. It may take a few minutes to show up. Let me know if anything still looks off.", "Lea")),
                 ]),
            Spec(number: 1076, subject: "Calendar integration stopped syncing", contact: oliver, status: .open, priority: .normal, assignee: .jonas, team: support, tags: ["Bug", "Sync"],
                 thread: [c(900, p("Meeting notes no longer appear for events since Tuesday. Google Calendar is still connected in settings.", "Oliver"))]),
            Spec(number: 1073, subject: "Slow loading on large notebooks", contact: hana, status: .pending, priority: .normal, assignee: .priya, team: support, tags: ["Bug"], sla: false,
                 thread: [
                     c(3400, p("Our wiki notebook with 900 pages takes almost 20 seconds to open in the web app.", "Hana")),
                     a(.priya, 3300, p("Hi Hana,", "Could you share the steps that lead to this, the browser you're on, and roughly when it happened? A screenshot helps too.", "Priya")),
                 ]),
            Spec(number: 1058, subject: "Payment failed but card is valid", contact: umar, status: .open, priority: .high, assignee: .lea, team: billing, tags: ["Billing"], mailbox: billingMailbox,
                 thread: [c(240, p("We got a payment failed email but the card works everywhere else. Can you retry the charge?", "Umar"))]),
            Spec(number: 1055, subject: "Webhook retries flooding our endpoint", contact: hana, status: .open, priority: .high, assignee: .jonas, team: support, tags: ["API"],
                 thread: [
                     c(330, p("Our endpoint was down for an hour and now we're receiving hundreds of retried deliveries at once.", "Hana")),
                     a(.jonas, 300, p("Hi Hana,", "I've paused redelivery for your endpoint and will release the backlog at 10 per second once you're ready.", "Jonas")),
                     c(200, p("Ready when you are.")),
                 ]),

            // Solved and closed.
            Spec(number: 1066, subject: "How do I invite my team?", contact: ben, status: .solved, priority: .normal, channel: .portal, assignee: .sam, team: support, tags: ["Onboarding", "Question"], csat: CSAT(score: 5, comment: "Super quick, thanks Sam!"),
                 thread: [
                     c(1800, p("Hi, we just upgraded to the Education plan. How do I invite the other teachers? I can't find it in settings.")),
                     a(.sam, 1740, p("Hi Ben,", "Welcome aboard! Go to <strong>Settings → Members</strong> and click <strong>Invite</strong>. You can paste a list of addresses separated by commas, and choose their role.") + "<ol><li>Open Settings from the bottom of the sidebar</li><li>Choose Members</li><li>Paste the addresses and press Send invites</li></ol>" + p("If your school uses Google Workspace you can also turn on domain sign-up so teachers join without an invite.", "Sam")),
                     c(1680, p("Perfect, all 14 invites went out. Thank you!")),
                 ]),
            Spec(number: 1061, subject: "Thank you!", contact: julia, status: .solved, priority: .low, channel: .chat, assignee: .maya, tags: ["Feedback"], csat: CSAT(score: 4, comment: ""),
                 thread: [
                     c(1200, p("Just wanted to say the new recipe templates are lovely. Our bakers use them every morning.")),
                     a(.maya, 1140, p("That made our day, thank you for telling us! I'll pass it on to the team that built them.")),
                 ]),
            Spec(number: 1059, subject: "Export all data before cancelling", contact: mateo, status: .solved, priority: .normal, assignee: .sam, team: support, tags: ["Export"], csat: CSAT(score: 4, comment: "Took a while but solved."),
                 thread: [
                     c(9000, p("We're consolidating tools. What's the best way to export everything, including attachments?", "Mateo")),
                     a(.sam, 8800, p("Hi Mateo,", "Settings, then Workspace, then Export gives you a full archive with attachments. It's emailed to you when ready.", "Sam")),
                     c(8600, p("Thanks, that worked!")),
                 ]),
            Spec(number: 1057, subject: "Handwriting recognition on iPad", contact: oliver, status: .solved, priority: .normal, assignee: .priya, team: support, tags: ["Mobile"],
                 thread: [
                     c(10_000, p("Apple Pencil notes are not converted to text even though the setting is on.", "Oliver")),
                     a(.priya, 9800, p("Hi Oliver,", "Recognition runs once the note is closed. If it still doesn't, reinstall the language pack under Settings, then Handwriting.", "Priya")),
                     c(9700, p("All sorted now, thank you.")),
                 ]),
            Spec(number: 1054, subject: "Rename workspace URL", contact: stefan, status: .solved, priority: .low, assignee: .maya, team: support, tags: ["Question"], csat: CSAT(score: 5, comment: "Fast and friendly."),
                 thread: [
                     c(11_000, p("We rebranded. Can we change our workspace URL from oldname to our new name?", "Stefan")),
                     a(.maya, 10_900, p("Hi Stefan,", "Done: your workspace now lives at northwind.margin.app, and the old address redirects for 90 days.", "Maya")),
                 ]),
            Spec(number: 1050, subject: "Account locked after password reset", contact: tara, status: .closed, priority: .normal, assignee: .jonas, team: support,
                 thread: [
                     c(14_000, p("I reset my password and now it says my account is locked for 30 minutes.", "Tara")),
                     a(.jonas, 13_900, p("Hi Tara,", "I've unlocked it. The lock comes from too many tries with the old password on another device.", "Jonas")),
                 ]),
            Spec(number: 1047, subject: "Student discount eligibility", contact: daniel, status: .solved, priority: .low, assignee: .lea, team: billing, tags: ["Billing", "Question"], mailbox: billingMailbox,
                 thread: [
                     c(15_000, p("I'm a PhD student. Does the education discount apply to individual plans?", "Daniel")),
                     a(.lea, 14_900, p("Hi Daniel,", "It does: send us your university email and we'll apply 50% to your plan.", "Lea")),
                 ]),
            Spec(number: 1041, subject: "Markdown export drops images", contact: vera, status: .solved, priority: .normal, assignee: .priya, team: support, tags: ["Export", "Bug"],
                 thread: [
                     c(20_000, p("Exporting to Markdown gives me the text but every image link points to a temporary URL.", "Vera")),
                     a(.priya, 19_800, p("Hi Vera,", "Fixed in today's release: images now come in a separate folder inside the export.", "Priya")),
                     c(19_600, p("Great, appreciate the quick reply.")),
                 ]),

            // Spam.
            Spec(number: 1087, subject: "Boost your SEO ranking today 🚀", contact: contact(91, "Rank Boost", "growth@rank-boost-pro.biz"), status: .open, priority: .low, sla: false, spam: true,
                 thread: [c(70, p("We guarantee first page on Google in 7 days. Reply YES for a free audit!!!"))]),
            Spec(number: 1053, subject: "You have won a $500 gift card", contact: contact(92, "Prize Center", "rewards@prize-center.top"), status: .open, priority: .low, sla: false, spam: true,
                 thread: [c(1300, p("Congratulations! Click the link to claim your $500 gift card before it expires."))]),
            Spec(number: 1046, subject: "URGENT: verify your account", contact: contact(93, "Margin Security", "security@rnargin-support.com"), status: .open, priority: .low, sla: false, spam: true,
                 thread: [c(2600, p("Your account will be suspended in 24 hours. Verify your password here immediately."))]),
        ]
    }

    /// A designed email as the server stores it: its cleaned style sheet first, then the body.
    static let receipt: String = {
        func row(_ label: String, _ value: String, strong: Bool = false) -> String {
            let style = "padding:10px 0;font-size:14px;color:#414552;" + (strong ? "font-weight:600;border-top:1px solid #ebeef1" : "")
            return "<tr><td style=\"\(style)\">\(label)</td><td align=\"right\" style=\"\(style)\">\(value)</td></tr>"
        }
        return "<style>.od-wrap{width:100% !important}@media only screen and (max-width:620px){.od-card{width:100% !important}.od-pad{padding:24px 20px !important}}</style>"
            + "<p>Forwarding the receipt for the second charge.</p>"
            + "<div style=\"background-color:#f6f9fc;padding:32px 0;font-family:-apple-system, 'Segoe UI', Helvetica, Arial, sans-serif\">"
            + "<table class=\"od-card\" width=\"600\" align=\"center\" cellpadding=\"0\" cellspacing=\"0\" border=\"0\" bgcolor=\"#ffffff\" style=\"border-radius:12px;border:1px solid #ebeef1\">"
            + "<tr><td align=\"center\" style=\"background-color:#1f2937;border-radius:12px 12px 0 0;padding:28px 0\"><span style=\"display:inline-block;width:44px;height:44px;line-height:44px;border-radius:22px;background-color:#ffffff;color:#1f2937;font-weight:700;font-size:20px\">M</span></td></tr>"
            + "<tr><td class=\"od-pad\" style=\"padding:32px 48px\">"
            + "<p style=\"margin:0;font-size:13px;color:#687385\">Receipt from Margin Labs GmbH</p>"
            + "<p style=\"margin:4px 0 24px;font-size:28px;font-weight:600;color:#1a1f36\">€4,320.00</p>"
            + "<p style=\"margin:0 0 8px;font-size:12px;color:#687385;text-transform:uppercase;letter-spacing:0.04em\">Summary</p>"
            + "<table width=\"100%\" cellpadding=\"0\" cellspacing=\"0\" border=\"0\" style=\"background-color:#f6f8fa;border-radius:8px;padding:4px 16px\">"
            + row("Margin Business, annual (12 seats)", "€4,320.00") + row("Date paid", "Jun 9, 2026, 08:14") + row("Amount paid", "€4,320.00", strong: true)
            + "</table></td></tr></table></div>"
    }()
}

// MARK: - Building tickets

private struct Spec {
    var number: Int
    var subject: String
    var contact: ContactRef
    var status: TicketStatus
    var priority: Priority
    var severity: Severity?
    var channel: Channel = .email
    var assignee: DemoPerson?
    var team: String?
    var tags: [String] = []
    var mailbox: String?
    var sla = true
    var snoozedUntil: Date?
    var csat: CSAT?
    var spam = false
    /// Ollie raised the priority when it triaged the ticket (the row's Ollie mark).
    var ollieRaised = false
    /// When Ollie handed a live chat to the team (minutes ago).
    var handoffAt: Double?
    var thread: [Line]
    var summary: AISummary?
    var suggestions: [String: JSONValue] = [:]
}

private struct Line {
    enum From { case customer, agent(DemoPerson), ollie }

    var from: From
    var minutes: Double
    var html: String
    var note = false
    var delivery: String?
    var files: [String] = []
    var quoted = false
}

private func c(_ minutes: Double, _ html: String, files: [String] = [], quoted: Bool = false) -> Line {
    Line(from: .customer, minutes: minutes, html: html, files: files, quoted: quoted)
}

private func a(_ person: DemoPerson, _ minutes: Double, _ html: String, delivery: String? = nil, files: [String] = []) -> Line {
    Line(from: .agent(person), minutes: minutes, html: html, delivery: delivery, files: files)
}

private func n(_ person: DemoPerson, _ minutes: Double, _ html: String) -> Line {
    Line(from: .agent(person), minutes: minutes, html: html, note: true)
}

private func o(_ minutes: Double, _ html: String) -> Line {
    Line(from: .ollie, minutes: minutes, html: html)
}

/// Paragraphs, the way the composer and the mail cleaner store them.
private func p(_ paragraphs: String...) -> String {
    paragraphs.map { "<p>\($0)</p>" }.joined()
}

extension DemoSeed {
    static func json(_ value: some Encodable) -> JSONValue {
        (try? JSONValue.from(value)) ?? .null
    }

    static func stamp(_ date: Date) -> String {
        date.formatted(Date.ISO8601FormatStyle(includingFractionalSeconds: false))
    }

    /// Ollie as a message author and an activity actor.
    static let ollie: BotRef = BotRef(demoID: demoID(.user, 99), name: "Ollie")

    /// The SLA targets by priority (first response, resolution), in minutes.
    static func targets(_ priority: Priority) -> (Double, Double) {
        switch priority {
        case .urgent: (60, 8 * 60)
        case .high: (2 * 60, 24 * 60)
        case .normal, .low: (8 * 60, 72 * 60)
        }
    }

    fileprivate static func add(_ spec: Spec, to ws: inout DemoWorkspace, mailbox: String? = supportMailbox, triage: Bool = true) {
        let id = ticketID(spec.number)
        let created = ago(spec.thread.first?.minutes ?? 0)
        var ticket = Ticket(demoID: id, number: spec.number, subject: spec.subject, createdAt: created)
        ticket.status = spec.status
        ticket.priority = spec.priority
        ticket.severity = spec.severity
        ticket.channel = spec.channel
        ticket.mailboxID = spec.mailbox ?? mailbox
        ticket.contact = spec.contact
        ticket.assignee = spec.assignee?.ref
        ticket.teamID = spec.team
        ticket.spam = spec.spam
        ticket.snoozedUntil = spec.snoozedUntil
        ticket.csat = spec.csat
        ticket.tags = spec.tags.compactMap { name in ws.tags.first { $0.name == name }?.ref }

        var messages: [Message] = []
        for (index, line) in spec.thread.enumerated() {
            let messageID = demoID(.message, spec.number * 100 + index + 1)
            var message = Message(demoID: messageID, ticketID: id, kind: line.note ? .note : .reply, html: line.html, createdAt: ago(line.minutes))
            switch line.from {
            case .customer:
                message.direction = "inbound"
                message.author = MessageAuthor(type: "contact", user: nil, contact: spec.contact)
                message.fromEmail = spec.contact.email ?? ""
                message.fromName = spec.contact.name
                message.to = [spec.channel == .chat ? "" : (spec.mailbox == billingMailbox ? "billing@margin.app" : "support@margin.app")].filter { !$0.isEmpty }
                message.deliveryStatus = "none"
            case let .agent(person):
                message.direction = "outbound"
                message.author = MessageAuthor(type: "user", user: person.ref, contact: nil)
                message.fromEmail = spec.mailbox == billingMailbox ? "billing@margin.app" : "support@margin.app"
                message.fromName = "\(person.name) at Margin"
                message.to = line.note || spec.channel == .chat ? [] : [spec.contact.email ?? ""]
                message.deliveryStatus = line.note ? "none" : (line.delivery ?? "sent")
                if line.delivery == "failed" { message.deliveryError = "550 5.1.1 The recipient's mail server refused the message." }
            case .ollie:
                message.direction = "outbound"
                message.author = MessageAuthor(type: "system", user: nil, contact: nil, bot: ollie)
                message.fromName = "Ollie"
                message.ai = true
                message.deliveryStatus = "sent"
            }
            message.subject = spec.subject
            message.hasQuoted = line.quoted
            message.attachments = line.files.enumerated().map { offset, name in
                DemoSeed.attachment(id: demoID(.attachment, spec.number * 100 + index * 10 + offset), filename: name)
            }
            messages.append(message)
        }
        let replies = messages.filter { !$0.isNote }
        let last = replies.last
        ticket.lastMessageAt = last?.createdAt ?? created
        ticket.lastMessagePreview = last.map { preview($0.bodyHTML) } ?? ""
        ticket.messageCount = messages.count
        ticket.lastCustomerMessageAt = replies.last(where: \.isInbound)?.createdAt
        ticket.firstRespondedAt = replies.first { !$0.isInbound && !$0.ai }?.createdAt
        if let last, last.isInbound {
            let lastAnswer = replies.last { !$0.isInbound }?.createdAt ?? .distantPast
            ticket.waitingSince = replies.first { $0.isInbound && $0.createdAt > lastAnswer }?.createdAt
        }
        if spec.status.isDone { ticket.solvedAt = ticket.lastMessageAt.addingTimeInterval(60) }
        ticket.updatedAt = ticket.lastMessageAt

        if spec.sla, !spec.spam {
            let (first, resolution) = targets(spec.priority)
            let firstDue = created.addingTimeInterval(first * 60)
            let resolutionDue = created.addingTimeInterval(resolution * 60)
            let late = !spec.status.isDone && ((ticket.firstRespondedAt == nil && firstDue < DemoMode.anchor) || resolutionDue < DemoMode.anchor)
            ticket.sla = TicketSLA(policyID: demoID(.made, 500 + spec.priority.rank), firstResponseDueAt: firstDue, resolutionDueAt: resolutionDue, breached: late)
        }

        var custom: [String: JSONValue] = [:]
        if let summary = spec.summary { custom["ai_summary"] = json(summary) }
        if !spec.suggestions.isEmpty { custom["ai_suggestions"] = .object(spec.suggestions) }
        if let handoff = spec.handoffAt {
            custom["ai_handoff"] = .bool(true)
            custom["ai_handoff_at"] = .string(stamp(ago(handoff)))
        }
        let triaged = triage && !spec.spam && (!spec.status.isDone || spec.number % 3 == 0)
        let triagedAt = created.addingTimeInterval(40)
        if triaged {
            custom["ai_triaged_at"] = .string(stamp(triagedAt))
            custom["ai_sentiment"] = .string(spec.summary?.sentiment?.rawValue ?? "neutral")
            custom["language"] = .string("en")
            custom["ai_decisions"] = .object([
                "engine": .string("jev"), "model": .string("jev"), "threshold": .number(0.7), "decided_at": .string(stamp(triagedAt)),
                "fields": .object([
                    "priority": .object(["value": .string(spec.priority.rawValue), "confidence": .number(0.86), "applied": .bool(spec.ollieRaised)]),
                    "severity": .object(["value": spec.severity.map { .string($0.rawValue) } ?? .null, "confidence": .number(0.74), "applied": .bool(spec.severity != nil)]),
                    "sentiment": .object(["value": .string(spec.summary?.sentiment?.rawValue ?? "neutral"), "confidence": .number(0.9), "applied": .bool(true)]),
                ]),
            ])
        }
        ticket.custom = custom

        // The timeline around the messages.
        var activities: [Activity] = []
        func activity(_ type: String, _ data: [String: JSONValue], actor: ActivityActor, at date: Date) {
            activities.append(Activity(demoID: demoID(.activity, spec.number * 100 + activities.count + 1), ticketID: id, actor: actor, type: type, data: data, createdAt: date))
        }
        let system = ActivityActor(type: "system", user: nil)
        let ai = ActivityActor(type: "ai", user: nil)
        if triaged {
            activity("ai_triaged", ["category": .string("problem"), "sentiment": custom["ai_sentiment"] ?? .null, "language": .string("en"), "priority": .string(spec.priority.rawValue), "via": .string("auto_triage"), "model": .string("jev")], actor: ai, at: triagedAt)
        }
        if spec.ollieRaised {
            let from: Priority = spec.priority == .urgent ? .high : .normal
            activity("priority_changed", ["from": .string(from.rawValue), "to": .string(spec.priority.rawValue), "via": .string("auto_triage"), "model": .string("jev"), "confidence": .number(0.86)], actor: ai, at: triagedAt)
        }
        if let severity = spec.severity, triaged {
            activity("severity_changed", ["from": .null, "to": .string(severity.rawValue), "via": .string("auto_triage"), "model": .string("jev"), "confidence": .number(0.74)], actor: ai, at: triagedAt)
        }
        for tag in ticket.tags { activity("tagged", ["tag": .string(tag.name)], actor: system, at: created.addingTimeInterval(30)) }
        if let person = spec.assignee {
            activity("assigned", ["from": .null, "to": json(person.ref), "via": .string("round_robin")], actor: system, at: created.addingTimeInterval(60))
        }
        if let handoff = spec.handoffAt {
            activity("ai_handoff", [:], actor: ai, at: ago(handoff))
        }
        if spec.status != .open {
            let by = spec.assignee.map { ActivityActor(type: "user", user: $0.ref) } ?? system
            activity("status_changed", ["from": .string("open"), "to": .string(spec.status.rawValue)], actor: by, at: ticket.lastMessageAt.addingTimeInterval(60))
        }
        if let sla = ticket.sla, sla.breached {
            let firstMissed = ticket.firstRespondedAt == nil
            activity("sla_breached", ["kind": .string(firstMissed ? "first_response" : "resolution")], actor: system, at: (firstMissed ? sla.firstResponseDueAt : sla.resolutionDueAt) ?? created)
        }
        if let csat = spec.csat {
            activity("csat_received", ["score": .number(Double(csat.score)), "comment": .string(csat.comment ?? "")], actor: system, at: ticket.lastMessageAt.addingTimeInterval(3 * 3600))
        }

        ws.tickets.append(ticket)
        ws.messages[id] = messages
        ws.activities[id] = activities
    }

    /// The list preview: the message's text on one line.
    static func preview(_ html: String) -> String {
        let text = HTMLText.plain(from: html).split(whereSeparator: \.isNewline).joined(separator: " ")
        return text.count > 160 ? String(text.prefix(159)) + "\u{2026}" : text
    }

    static func attachment(id: String, filename: String) -> Attachment {
        let ext = (filename as NSString).pathExtension.lowercased()
        switch ext {
        case "png":
            return Attachment(demoID: id, filename: filename, contentType: "image/png", sizeBytes: 284_311, url: "data:image/png;base64," + screenshot.base64EncodedString())
        case "pdf":
            return Attachment(demoID: id, filename: filename, contentType: "application/pdf", sizeBytes: 1_204_118, url: "data:text/plain;charset=utf-8,Demo%20PDF%20attachment")
        default:
            return Attachment(demoID: id, filename: filename, contentType: "text/plain", sizeBytes: 48_220, url: "data:text/plain;charset=utf-8,Thread%200%20Crashed%3A%20EXC_BAD_ACCESS")
        }
    }

    /// A small grey app window for image attachments, drawn here so nothing is fetched.
    static let screenshot: Data = {
        let width = 480, height = 300
        guard let context = CGContext(data: nil, width: width, height: height, bitsPerComponent: 8, bytesPerRow: 0, space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return Data() }
        func fill(_ gray: CGFloat, _ rect: CGRect) {
            context.setFillColor(CGColor(gray: gray, alpha: 1))
            context.fill(rect)
        }
        fill(0.98, CGRect(x: 0, y: 0, width: width, height: height))
        fill(1, CGRect(x: 0, y: 0, width: 100, height: height))
        fill(0.04, CGRect(x: 12, y: 270, width: 56, height: 6))
        for row in 0 ..< 6 { fill(0.83, CGRect(x: 12, y: 240 - row * 17, width: 60 - row * 4, height: 5)) }
        fill(1, CGRect(x: 116, y: 14, width: 350, height: 272))
        fill(0.04, CGRect(x: 132, y: 254, width: 150, height: 8))
        for row in 0 ..< 8 { fill(0.83, CGRect(x: 132, y: 226 - row * 13, width: 300 - (row % 3) * 60, height: 5)) }
        fill(0.96, CGRect(x: 132, y: 30, width: 310, height: 80))
        guard let image = context.makeImage() else { return Data() }
        let data = NSMutableData()
        guard let destination = CGImageDestinationCreateWithData(data, UTType.png.identifier as CFString, 1, nil) else { return Data() }
        CGImageDestinationAddImage(destination, image, nil)
        CGImageDestinationFinalize(destination)
        return data as Data
    }()
}

// MARK: - Models the demo builds

extension Ticket {
    /// A blank open ticket the seed and the demo backend fill in.
    init(demoID id: String, number: Int, subject: String, createdAt: Date) {
        self.id = id
        self.number = number
        self.subject = subject
        status = .open
        priority = .normal
        severity = nil
        channel = .email
        mailboxID = nil
        contact = nil
        assignee = nil
        teamID = nil
        tags = []
        fields = [:]
        sla = nil
        firstRespondedAt = nil
        solvedAt = nil
        snoozedUntil = nil
        lastMessageAt = createdAt
        lastCustomerMessageAt = nil
        waitingSince = nil
        lastMessagePreview = ""
        messageCount = 0
        spam = false
        csat = nil
        custom = [:]
        self.createdAt = createdAt
        updatedAt = createdAt
    }
}

extension TicketSLA {
    init(policyID: String?, firstResponseDueAt: Date?, resolutionDueAt: Date?, breached: Bool) {
        self.policyID = policyID
        self.firstResponseDueAt = firstResponseDueAt
        self.resolutionDueAt = resolutionDueAt
        self.breached = breached
    }
}

extension Message {
    init(demoID id: String, ticketID: String, kind: MessageKind, html: String, createdAt: Date) {
        self.id = id
        self.ticketID = ticketID
        self.kind = kind
        direction = "outbound"
        author = MessageAuthor(type: "system", user: nil, contact: nil)
        bodyHTML = html
        bodyText = HTMLText.plain(from: html)
        hasQuoted = false
        subject = ""
        fromEmail = ""
        fromName = ""
        to = []
        cc = []
        attachments = []
        deliveryStatus = "none"
        deliveryError = ""
        ai = false
        self.createdAt = createdAt
    }
}

extension Attachment {
    init(demoID id: String, filename: String, contentType: String, sizeBytes: Int, url: String) {
        self.id = id
        self.filename = filename
        self.contentType = contentType
        self.sizeBytes = sizeBytes
        inline = false
        self.url = url
    }
}

extension BotRef {
    init(demoID id: String, name: String) {
        self.id = id
        self.name = name
        avatarURL = ""
        lookKind = "built"
        emoji = ""
    }
}

extension Activity {
    init(demoID id: String, ticketID: String, actor: ActivityActor, type: String, data: [String: JSONValue], createdAt: Date) {
        self.id = id
        self.ticketID = ticketID
        self.actor = actor
        self.type = type
        self.data = data
        self.createdAt = createdAt
    }
}

extension AppNotification {
    init(demoID id: String, type: String, ticket: NotificationTicket?, data: [String: JSONValue], readAt: Date?, createdAt: Date) {
        self.id = id
        self.type = type
        self.ticket = ticket
        self.data = data
        self.readAt = readAt
        self.createdAt = createdAt
    }
}

extension SuggestedReply {
    init(demoID id: String, ticketID: String, messageID: String?, bodyHTML: String, createdAt: Date) {
        self.id = id
        self.ticketID = ticketID
        self.messageID = messageID
        self.bodyHTML = bodyHTML
        status = "pending"
        self.createdAt = createdAt
    }
}

extension PushDevice {
    init(demoID id: String, name: String, appVersion: String, createdAt: Date) {
        self.id = id
        platform = "ios"
        self.name = name
        self.appVersion = appVersion
        environment = AppInfo.pushEnvironment
        mutedTypes = []
        current = true
        self.createdAt = createdAt
        lastPushAt = nil
    }
}

extension Envelope {
    init(demoID id: String, seq: Int, workspaceID: String, type: String, data: [String: JSONValue], occurredAt: Date) {
        self.id = id
        self.seq = seq
        self.workspaceID = workspaceID
        self.type = type
        visibility = nil
        topics = nil
        self.data = .object(data)
        self.occurredAt = occurredAt
    }
}
#endif
