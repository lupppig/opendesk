#if DEBUG
import Foundation

// Collab mail routes (contracts/collab.md).
//
// Margin has Collab on: Maya's private Gmail (maya@margin.app) and the shared Outlook
// partners@margin.app (Partnerships, managed by Jonas), plus one conversation of Jonas's
// own account shared with Maya on its own. Lumen Labs has Collab off.
//
// Conversation ids are `0197a0c0-0033-7000-8000-0000000000<n>`, and a demo path may name
// the number instead (`/mail/inbox/3`):
//
//   1  Series A data room (Investors, starred, unread, a PDF)        14 Podcast interview (unread)
//   2  Canvasly invoice (a designed receipt)                          17 Nordics reseller agreement (partners@, assigned to Maya, @mention)
//   3  Northlight contract renewal (shared with Jonas, comments)      18 Co-marketing webinar (to the partnerships@ alias)
//   4  Product review invite (calendar, unread)                       25 Office lease (Jonas's account, shared with Maya)
//   5  Ship Notes newsletter (remote images held)

/// Collab mail accounts, conversations, comments and drafts.
struct DemoMail: Sendable {
    enum Kind: Int { case account = 0x31, folder, thread, message, comment }

    static func id(_ kind: Kind, _ n: Int) -> String {
        String(format: "0197a0c0-%04x-7000-8000-%012d", kind.rawValue, n)
    }

    var enabled = false
    /// Every account, the ones Maya does not see included (Jonas's own).
    var accounts: [MailAccount] = []
    var folders: [MailFolder] = []
    var threads: [MailThread] = []
    var messages: [String: [MailMessage]] = [:]
    var comments: [String: [MailComment]] = [:]
    var shares: [String: MailAudience] = [:]
    var prefs = MailSidebarPrefs()

    static let gmail = id(.account, 1)
    static let partners = id(.account, 2)
    static let jonasAccount = id(.account, 3)
    static let maya = MailAddress(name: "Maya Chen", email: "maya@margin.app")
    static let partnersAddress = MailAddress(name: "Margin Partnerships", email: "partners@margin.app")

    static func seed(_ ws: DemoWorkspace) -> DemoMail {
        guard ws.id == DemoSeed.marginID else { return DemoMail() }
        var mail = DemoMail()
        mail.enabled = true
        mail.seedAccounts()
        mail.seedThreads()
        for thread in mail.threads { mail.derive(thread.id) }
        return mail
    }
}

// MARK: - Seed

private func p(_ lines: String...) -> String {
    lines.map { "<p>" + $0 + "</p>" }.joined()
}

private func ago(_ minutes: Double) -> Date { DemoSeed.ago(minutes) }

extension DemoMail {
    fileprivate static let mayaSignature = #"<p style="color:#737373;font-size:13px;line-height:1.45">Maya Chen<br>Founder, Margin<br><a href="https://margin.app">margin.app</a></p>"#
    fileprivate static let partnersSignature = #"<p style="color:#737373;font-size:13px;line-height:1.45">{agent} for Margin Partnerships<br>partners@margin.app</p>"#

    fileprivate mutating func seedAccounts() {
        let jonas = DemoPerson.jonas.ref
        accounts = [
            MailAccount(
                id: Self.gmail, provider: .google, email: "maya@margin.app", name: "Maya", displayName: "Maya Chen", color: "violet",
                owner: DemoPerson.maya.ref, visibility: "private", signatureHTML: Self.mayaSignature, status: "active",
                lastSyncedAt: ago(1), canManage: true,
                identities: [MailIdentity(identityID: nil, email: "maya@margin.app", displayName: "Maya Chen", signatureHTML: Self.mayaSignature, primary: true, isDefault: true, fromName: "Maya Chen")],
                defaultFrom: "maya@margin.app", oldestMessageAt: ago(31 * 1440)
            ),
            MailAccount(
                id: Self.partners, provider: .microsoft, email: "partners@margin.app", name: "Partners", displayName: "{agent} at Margin", color: "teal",
                owner: jonas, visibility: "shared", sharedWith: MailAudience(teamIDs: [DemoSeed.partnerships]),
                signatureHTML: Self.partnersSignature, status: "active", lastSyncedAt: ago(2), canManage: true,
                identities: [
                    MailIdentity(identityID: nil, email: "partners@margin.app", displayName: "{agent} at Margin", signatureHTML: Self.partnersSignature, primary: true, isDefault: true,
                                 fromName: "Maya Chen at Margin", filledSignatureHTML: Self.partnersSignature.replacingOccurrences(of: "{agent}", with: "Maya Chen")),
                    MailIdentity(identityID: "alias-1", email: "partnerships@margin.app", displayName: "", signatureHTML: "", fromName: "Maya Chen at Margin",
                                 filledSignatureHTML: Self.partnersSignature.replacingOccurrences(of: "{agent}", with: "Maya Chen").replacingOccurrences(of: "partners@", with: "partnerships@")),
                ],
                defaultFrom: "partners@margin.app", history: MailHistory(status: "done", since: nil), oldestMessageAt: ago(400 * 1440)
            ),
            MailAccount(
                id: Self.jonasAccount, provider: .google, email: "jonas@margin.app", name: "Jonas", displayName: "Jonas Weber", color: "blue",
                owner: jonas, visibility: "private", status: "active", lastSyncedAt: ago(3),
                identities: [MailIdentity(identityID: nil, email: "jonas@margin.app", displayName: "Jonas Weber", primary: true, isDefault: true, fromName: "Jonas Weber")],
                defaultFrom: "jonas@margin.app", oldestMessageAt: ago(60 * 1440)
            ),
        ]
        var n = 0
        func folder(_ account: String, _ name: String, _ role: MailFolderRole?, kind: String = "folder", color: String? = nil) {
            n += 1
            folders.append(MailFolder(id: Self.id(.folder, n), accountID: account, name: name, path: name, role: role, kind: kind, color: color, canRename: role == nil))
        }
        for account in [Self.gmail, Self.jonasAccount] {
            folder(account, "Inbox", .inbox, kind: "label")
            folder(account, "Starred", .starred, kind: "label")
            folder(account, "Important", .important, kind: "label")
            folder(account, "Sent", .sent, kind: "label")
            folder(account, "Drafts", .drafts, kind: "label")
            folder(account, "Spam", .spam, kind: "label")
            folder(account, "Trash", .trash, kind: "label")
        }
        folder(Self.gmail, "Investors", nil, kind: "label", color: "#16a766")
        folder(Self.gmail, "Vendors", nil, kind: "label", color: "#ffad47")
        folder(Self.gmail, "Hiring", nil, kind: "label", color: "#4a86e8")
        folder(Self.gmail, "Receipts", nil, kind: "label", color: "#999999")
        folder(Self.gmail, "Newsletters", nil, kind: "label", color: "#a479e2")
        folder(Self.partners, "Inbox", .inbox)
        folder(Self.partners, "Sent Items", .sent)
        folder(Self.partners, "Drafts", .drafts)
        folder(Self.partners, "Archive", .archive)
        folder(Self.partners, "Junk Email", .spam)
        folder(Self.partners, "Deleted Items", .trash)
        folder(Self.partners, "Resellers", nil)
        folder(Self.partners, "Events", nil)
        folder(Self.partners, "Follow up", nil, kind: "label", color: "#e74856")
        folder(Self.partners, "Contract", nil, kind: "label", color: "#0078d4")
    }

    fileprivate struct Spec {
        var n: Int
        var account: String
        var subject: String
        var boxes: [MailFolderRole]
        var labels: [String] = []
        var starred = false
        var messages: [Msg]
        var shares: MailAudience?
        var assignees: [DemoPerson] = []
        var comments: [(DemoPerson, Double, String)] = []
        var toAddress: String?
    }

    fileprivate struct Msg {
        var from: MailAddress
        var to: [MailAddress]
        var cc: [MailAddress] = []
        var ago: Double
        var html: String
        var files: [String] = []
        var unread = false
        var sentBy: DemoPerson?
        var draft = false
    }

    private static func a(_ name: String, _ email: String) -> MailAddress { MailAddress(name: name, email: email) }

    fileprivate mutating func seedThreads() {
        let me = Self.maya
        let partners = Self.partnersAddress
        let elif = Self.a("Elif Kaya", "elif@northwind.vc")
        let marco = Self.a("Marco Ricci", "marco@ricci.capital")
        let greta = Self.a("Greta Holm", "greta@northlightprint.de")
        let priya = Self.a("Priya Raman", "priya@margin.app")
        let jonas = Self.a("Jonas Weber", "jonas@margin.app")
        let sam = Self.a("Sam Okafor", "sam@margin.app")
        let hana = Self.a("Hana Sato", "hana@fieldnote.io")
        let tom = Self.a("Tom Becker", "tom@becker.studio")
        let nadia = Self.a("Nadia Okoro", "nadia@buildingcalm.fm")
        let ana = Self.a("Ana Ferreira", "ana@hirewell.io")
        let erik = Self.a("Erik Lindqvist", "erik@nordcloud-partners.se")
        let paula = Self.a("Paula Reyes", "paula@lumacast.io")
        let leo = Self.a("Leo Fischer", "leo@brightwave.agency")
        let oliver = Self.a("Oliver Grant", "oliver@grantpartners.co.uk")
        let kerstin = Self.a("Kerstin Vogel", "k.vogel@spreeoffices.de")
        let sig = Self.mayaSignature
        let psig = Self.partnersSignature.replacingOccurrences(of: "{agent}", with: "Maya Chen")

        let specs: [Spec] = [
            Spec(n: 1, account: Self.gmail, subject: "Series A follow-up: data room access", boxes: [.inbox, .important], labels: ["Investors"], starred: true, messages: [
                Msg(from: elif, to: [me], ago: 2950, html: p("Hi Maya,", "Thanks again for the time on Thursday. The partnership enjoyed the product walkthrough, and the retention numbers stood out.", "Could you share access to the data room so we can start diligence? We would mostly want the cohort tables and the last 12 months of revenue by plan.", "Best,<br>Elif")),
                Msg(from: me, to: [elif], ago: 2700, html: p("Hi Elif,", "Great to hear. I just invited you to the data room, it has the cohort tables, revenue by plan and our hiring plan for the next 18 months.", "Shout if anything is missing.") + sig, sentBy: .maya),
                Msg(from: elif, to: [me], cc: [marco], ago: 25, html: p("Maya,", "We went through the data room over the weekend, very clear, thank you. Attached is a first draft of the term sheet so you can see where we are heading. Marco is copied as he will join the board conversation.", "Do you have 30 minutes on Wednesday or Thursday to walk through it?", "Elif"), files: ["Margin-term-sheet-draft.pdf"], unread: true),
            ]),
            Spec(n: 2, account: Self.gmail, subject: "Your Canvasly receipt for June", boxes: [.inbox], labels: ["Receipts"], messages: [
                Msg(from: Self.a("Canvasly", "billing@canvasly.com"), to: [me], ago: 190, html: Self.receiptHTML),
            ]),
            Spec(n: 3, account: Self.gmail, subject: "Contract renewal with Northlight Print", boxes: [.inbox], labels: ["Vendors"], messages: [
                Msg(from: greta, to: [me], ago: 4320, html: p("Hello Maya,", "Our agreement for the printed onboarding kits ends on 31 July. Ahead of the renewal I attached our updated price list for the next 12 months.", "Paper costs went up quite a bit this spring, so the per-kit price is now €4.20 for up to 2,000 kits a quarter. Above that the volume tier still applies.", "Kind regards,<br>Greta Holm<br>Northlight Print"), files: ["Northlight-pricing-2026.pdf"]),
                Msg(from: me, to: [greta], ago: 4100, html: p("Hi Greta,", "Thanks for sending this ahead of time. Let me go through it with Jonas, who runs our operations, and come back to you this week.") + sig, sentBy: .maya),
                Msg(from: greta, to: [me], ago: 300, html: p("Hi Maya,", "Just checking in on the renewal. If it helps, we could hold last year's price for another quarter if you sign before the end of June.", "Greta"), unread: true),
            ], shares: MailAudience(userIDs: [DemoPerson.jonas.id]), comments: [
                (.jonas, 4000, "<p>Their new rate is 18% higher than last year. I would push back on the volume tier, we will cross 2,000 kits in Q3.</p>"),
                (.maya, 3900, #"<p><span data-type="mention" class="mention" data-id="\#(DemoPerson.jonas.id)" data-label="Jonas Weber">@Jonas Weber</span> can you pull last year's invoices so we can compare?</p>"#),
                (.jonas, 280, "<p>Done, they are in the ops folder. Holding last year's price for a quarter is worth taking.</p>"),
            ]),
            Spec(n: 4, account: Self.gmail, subject: "Invitation: Product review @ Wed 17 Jun 14:00", boxes: [.inbox], messages: [
                Msg(from: priya, to: [me, jonas, sam], ago: 75, html: Self.inviteHTML, files: ["invite.ics"], unread: true),
            ]),
            Spec(n: 5, account: Self.gmail, subject: "Ship Notes #142: the quiet power of written plans", boxes: [.inbox], labels: ["Newsletters"], messages: [
                Msg(from: Self.a("Ship Notes", "hello@shipnotes.news"), to: [me], ago: 420, html: Self.newsletterHTML),
            ]),
            Spec(n: 6, account: Self.gmail, subject: "Re: Intro: Margin <> Fieldnote", boxes: [.inbox], messages: [
                Msg(from: tom, to: [me, hana], ago: 2900, html: p("Maya, meet Hana. She leads partnerships at Fieldnote and I think there is a lot you two could do together, their users keep asking for a notes integration.", "Hana, meet Maya, founder of Margin. I'll let you take it from here!", "Tom")),
                Msg(from: me, to: [hana], cc: [tom], ago: 2600, html: p("Thanks Tom (moving you to cc).", "Hana, lovely to meet you. Would a call next week work? I'm free Tuesday or Wednesday afternoon, Berlin time.") + sig, sentBy: .maya),
                Msg(from: hana, to: [me], cc: [tom], ago: 1350, html: p("Hi Maya,", "Wednesday at 15:00 works well for me. I'll send an invite with a short agenda. Looking forward to it!", "Hana")),
            ]),
            Spec(n: 7, account: Self.gmail, subject: "Booking confirmed: Lisbon, 2 to 4 July", boxes: [], messages: [
                Msg(from: Self.a("Casa Alfama", "reservas@casaalfama.pt"), to: [me], ago: 5800, html: p("Olá Maya,", "Your booking is confirmed: 6 rooms from Thursday 2 July to Saturday 4 July, breakfast included. The meeting room is reserved for both days from 9:00.", "We attached the confirmation. See you soon in Lisbon!", "Casa Alfama"), files: ["booking-confirmation.pdf"]),
            ]),
            Spec(n: 8, account: Self.gmail, subject: "Candidate: Senior iOS engineer, Rui Costa", boxes: [.inbox], labels: ["Hiring"], messages: [
                Msg(from: ana, to: [me], ago: 2880, html: p("Hi Maya,", "As promised, here is the first candidate for the senior iOS role. Rui spent five years at a design tools company and shipped their offline sync. He is based in Porto and open to Berlin twice a month.", "CV attached. Shall I set up a first call?", "Ana"), files: ["Rui-Costa-CV.pdf"]),
            ]),
            Spec(n: 9, account: Self.gmail, subject: "Board update: Q2 draft", boxes: [.sent], messages: [
                Msg(from: me, to: [elif, marco], ago: 3100, html: p("Hi both,", "Ahead of next week's board call, the Q2 deck is attached. Highlights: net revenue retention at 118%, the Business plan is now 41% of revenue, and we hired our first two engineers in Lisbon.", "Happy to take questions before the call.") + sig, files: ["Margin-board-Q2.key"], sentBy: .maya),
            ]),
            Spec(n: 10, account: Self.gmail, subject: "Coffee next week?", boxes: [.inbox], starred: true, messages: [
                Msg(from: tom, to: [me], ago: 4400, html: p("Hey Maya,", "I'm in Berlin from Monday. Coffee at the usual place? Would love to hear how the fundraise is going.", "Tom")),
            ]),
            Spec(n: 11, account: Self.gmail, subject: "Security alert: new sign-in on Mac", boxes: [.inbox], messages: [
                Msg(from: Self.a("Google", "no-reply@accounts.google.com"), to: [me], ago: 5900, html: p("<b>New sign-in to your account</b>", "We noticed a new sign-in to maya@margin.app on a Mac. If this was you, you don't need to do anything. If not, we'll help you secure your account.", "Check activity at myaccount.google.com/notifications")),
            ]),
            Spec(n: 12, account: Self.gmail, subject: "Hiring plan for H2", boxes: [.drafts], messages: [
                Msg(from: me, to: [jonas], ago: 600, html: p("Jonas,", "Here is where I landed on H2: two more engineers in Lisbon, one designer, and a second support hire once we pass 400 Business workspaces.") + #"<div class="od-signature">"# + sig + "</div>", sentBy: .maya, draft: true),
            ]),
            Spec(n: 13, account: Self.gmail, subject: "Your Fjord Cloud invoice is ready", boxes: [.inbox], labels: ["Receipts"], messages: [
                Msg(from: Self.a("Fjord Cloud", "billing@fjordcloud.eu"), to: [me], ago: 7200, html: p("Hello,", "Your invoice for May 2026 is ready: €1,284.60 for 6 servers and 2 TB of backups. It will be charged to the card ending 4421 on 20 June.", "Thank you for using Fjord Cloud."), files: ["invoice-2026-05.pdf"]),
            ]),
            Spec(n: 14, account: Self.gmail, subject: "Podcast interview on 24 June", boxes: [.inbox], messages: [
                Msg(from: nadia, to: [me], ago: 360, html: p("Hi Maya,", "We're so glad you said yes! The episode is about building calm software teams. We record over video on 24 June at 17:00 Berlin time, it takes about an hour.", "Could you send a short bio and a photo by Friday?", "Nadia, Building Calm"), unread: true),
            ]),
            Spec(n: 15, account: Self.gmail, subject: "Congratulations! You've been selected for a MacBook Pro", boxes: [.spam], messages: [
                Msg(from: Self.a("Apple Rewards", "winner@apple-rewards-center.top"), to: [me], ago: 900, html: p("Dear customer,", "You have been selected to receive a brand new MacBook Pro. Claim your reward within 24 hours by confirming your shipping details.")),
            ]),
            Spec(n: 16, account: Self.gmail, subject: "Webinar replay: scaling async teams", boxes: [.trash], messages: [
                Msg(from: Self.a("Async Academy", "events@asyncacademy.io"), to: [me], ago: 8600, html: p("Thanks for registering! The replay of Scaling async teams is now available for 14 days.")),
            ]),
            Spec(n: 17, account: Self.partners, subject: "Reseller agreement: Nordics draft", boxes: [.inbox], labels: ["Contract"], messages: [
                Msg(from: erik, to: [partners], ago: 2950, html: p("Hello Margin team,", "Following our call, we would like to resell Margin to our customers in Sweden, Norway and Denmark. Could you send over your reseller terms?", "Best regards,<br>Erik Lindqvist<br>Nordcloud Partners")),
                Msg(from: Self.a("Jonas Weber at Margin", "partners@margin.app"), to: [erik], ago: 2800, html: p("Hi Erik,", "Great to hear. Our standard terms are a 20% margin on yearly plans, with co-branded onboarding for teams above 50 seats. A draft agreement follows this week."), sentBy: .jonas),
                Msg(from: erik, to: [partners], ago: 50, html: p("Hi again,", "Our legal team marked up the draft (attached). Two open points: the territory clause should include Finland, and we'd like a 30-day notice period instead of 90.", "Can we close this before the summer break?", "Erik"), files: ["Reseller-agreement-v2.docx"], unread: true),
            ], assignees: [.maya], comments: [
                (.jonas, 45, #"<p><span data-type="mention" class="mention" data-id="\#(DemoPerson.maya.id)" data-label="Maya Chen">@Maya Chen</span> you know Erik best, can you take this one? Finland is fine by me.</p>"#),
            ]),
            Spec(n: 18, account: Self.partners, subject: "Co-marketing webinar in September", boxes: [.inbox], labels: ["Follow up"], messages: [
                Msg(from: paula, to: [Self.a("", "partnerships@margin.app")], ago: 240, html: p("Hi there,", "Lumacast would love to run a joint webinar with Margin in September: how product teams turn meeting notes into decisions. We'd host it and bring about 800 registrants.", "Would someone from your team join as a speaker?", "Paula Reyes<br>Partner marketing, Lumacast")),
            ], toAddress: "partnerships@margin.app"),
            Spec(n: 19, account: Self.partners, subject: "Linkhub app directory: listing approved", boxes: [.inbox], messages: [
                Msg(from: Self.a("Linkhub Partners", "partners@linkhub.dev"), to: [partners], ago: 1500, html: p("Good news: Margin is now listed in the Linkhub app directory.", "Your listing is live at linkhub.dev/apps/margin. Partners with a demo video get about three times more installs, you can add one any time from the partner portal.")),
            ]),
            Spec(n: 20, account: Self.partners, subject: "Referral payout for May", boxes: [.inbox], messages: [
                Msg(from: Self.a("Fieldnote Payouts", "payouts@fieldnote.io"), to: [partners], ago: 2950, html: p("Hello Margin,", "Your referral payout for May is on its way: 14 referred workspaces, €2,380 in total. It reaches your account within 3 business days.") + #"<table style="border-collapse:collapse;font-size:14px"><tr><td style="padding:4px 16px 4px 0">Referred workspaces</td><td style="padding:4px 0"><b>14</b></td></tr><tr><td style="padding:4px 16px 4px 0">Payout</td><td style="padding:4px 0"><b>€2,380.00</b></td></tr></table>"#, files: ["payout-may-2026.csv"]),
            ]),
            Spec(n: 21, account: Self.partners, subject: "Your booth at SaaS Connect Berlin", boxes: [], labels: ["Events"], messages: [
                Msg(from: Self.a("SaaS Connect Berlin", "events@saasconnect.de"), to: [partners], ago: 4300, html: p("Dear exhibitor,", "Your booth B14 is confirmed for 9 and 10 September. Setup opens at 7:00 on the first day. Please send the names of your booth staff by 15 August.")),
            ]),
            Spec(n: 22, account: Self.partners, subject: "Re: Joint case study with Brightwave", boxes: [.inbox], messages: [
                Msg(from: leo, to: [partners], ago: 2000, html: p("Hi,", "Our design team now runs every client project in Margin. Would you like to write a case study together? We can share numbers on how much faster handovers got.", "Leo")),
                Msg(from: Self.a("Maya Chen at Margin", "partners@margin.app"), to: [leo], ago: 1400, html: p("Hi Leo,", "We'd love that! Our content lead will reach out this week to plan an interview.") + psig, sentBy: .maya),
            ]),
            Spec(n: 23, account: Self.partners, subject: "Partner newsletter: June update", boxes: [.sent], messages: [
                Msg(from: Self.a("Sam Okafor at Margin", "partners@margin.app"), to: [Self.a("Margin partners", "partner-list@margin.app")], ago: 7300, html: p("Hi partners,", "In June we shipped shared mail, a faster web clipper and Finnish. The partner portal now shows referral payouts as they happen.", "Sam, Margin Partnerships"), sentBy: .sam),
            ]),
            Spec(n: 24, account: Self.partners, subject: "Signed: reseller agreement UK", boxes: [.archive], messages: [
                Msg(from: oliver, to: [partners], ago: 20_000, html: p("Hello,", "Please find our countersigned reseller agreement attached. Looking forward to working together!", "Oliver Grant<br>Grant Partners"), files: ["Reseller-agreement-UK-signed.pdf"]),
            ]),
            Spec(n: 25, account: Self.jonasAccount, subject: "Fwd: Office lease renewal", boxes: [.inbox], messages: [
                Msg(from: kerstin, to: [jonas], ago: 1600, html: p("Dear Mr Weber,", "Your lease for the third floor at Spreeufer 12 ends on 30 September. We are happy to offer a renewal for three years at the current rent, or for one year at a 6% increase.", "Please let us know by 15 July.", "Kerstin Vogel<br>Spree Offices")),
            ], shares: MailAudience(userIDs: [DemoPerson.maya.id]), comments: [
                (.jonas, 1500, #"<p><span data-type="mention" class="mention" data-id="\#(DemoPerson.maya.id)" data-label="Maya Chen">@Maya Chen</span> do we want to commit to three more years? With the Lisbon office I'd lean to one.</p>"#),
            ]),
        ]
        for spec in specs { add(spec) }
    }

    fileprivate mutating func add(_ spec: Spec) {
        let threadID = Self.id(.thread, spec.n)
        let account = accounts.first { $0.id == spec.account }
        var folderIDs = spec.boxes.compactMap { role in folders.first { $0.accountID == spec.account && $0.role == role }?.id }
        folderIDs += spec.labels.compactMap { name in folders.first { $0.accountID == spec.account && $0.name == name }?.id }
        if spec.messages.contains(where: { $0.from.email == account?.email || $0.from.email.hasPrefix("partners@") }),
           let sent = folders.first(where: { $0.accountID == spec.account && $0.role == .sent })?.id, !folderIDs.contains(sent), !spec.messages.allSatisfy(\.draft) {
            folderIDs.append(sent)
        }
        if spec.starred, let starred = folders.first(where: { $0.accountID == spec.account && $0.role == .starred })?.id { folderIDs.append(starred) }
        threads.append(MailThread(id: threadID, accountID: spec.account, subject: spec.subject, lastMessageAt: ago(0), folderIDs: folderIDs, shared: spec.shares != nil,
                                  assignees: spec.assignees.map(\.ref), toAddress: spec.toAddress))
        var list: [MailMessage] = []
        for (index, msg) in spec.messages.enumerated() {
            let id = Self.id(.message, spec.n * 10 + index)
            let domain = msg.from.email.split(separator: "@").last.map(String.init) ?? "mail"
            let files = msg.files.enumerated().map { Self.attachment(index: $0.offset, filename: $0.element) }
            list.append(MailMessage(
                id: id, threadID: threadID, accountID: spec.account, messageID: "\(spec.n).\(index)@\(domain)",
                from: msg.from, to: msg.to, cc: msg.cc, subject: index == 0 || spec.subject.hasPrefix("Re:") ? spec.subject : "Re: " + spec.subject,
                snippet: DemoSeed.preview(msg.html), bodyHTML: msg.html, bodyText: HTMLText.plain(from: msg.html), sentAt: ago(msg.ago),
                unread: msg.unread, starred: spec.starred && index == spec.messages.count - 1, draft: msg.draft, folderIDs: folderIDs, attachments: files,
                sentBy: msg.sentBy?.ref, draftID: msg.draft ? id : nil, inReplyToID: index > 0 ? Self.id(.message, spec.n * 10 + index - 1) : nil,
                ownAddress: spec.toAddress ?? account?.email
            ))
        }
        messages[threadID] = list
        if let shares = spec.shares { self.shares[threadID] = shares }
        comments[threadID] = spec.comments.enumerated().map { index, comment in
            MailComment(id: Self.id(.comment, spec.n * 10 + index), threadID: threadID, author: comment.0.ref, bodyHTML: comment.2,
                        mentions: MailCommentText.mentions(in: comment.2).map(\.id), createdAt: ago(comment.1), updatedAt: ago(comment.1))
        }
    }

    static func attachment(index: Int, filename: String) -> MailAttachment {
        let ext = (filename as NSString).pathExtension.lowercased()
        let base = DemoSeed.attachment(id: "x", filename: filename)
        let (type, size): (String, Int) = switch ext {
        case "pdf": ("application/pdf", [184_220, 642_118, 96_400][index % 3])
        case "png": ("image/png", base.sizeBytes)
        case "docx": ("application/vnd.openxmlformats-officedocument.wordprocessingml.document", 88_310)
        case "key": ("application/vnd.apple.keynote", 4_812_004)
        case "ics": ("text/calendar", 1_204)
        case "csv": ("text/csv", 2_310)
        default: (base.contentType, base.sizeBytes)
        }
        let url: String = switch ext {
        case "csv": "data:text/csv;charset=utf-8," + "workspace,plan,payout%0AAtlas%20Studio,Business,240%0ANorth%20Bay,Team,120%0A"
        case "ics": "data:text/calendar;charset=utf-8,BEGIN%3AVCALENDAR%0ASUMMARY%3AProduct%20review%0AEND%3AVCALENDAR"
        case "png": base.url
        default: "data:text/plain;charset=utf-8," + (filename.addingPercentEncoding(withAllowedCharacters: .alphanumerics) ?? "file")
        }
        return MailAttachment(id: String(index), filename: filename, contentType: type, size: size, url: url)
    }

    fileprivate static let receiptHTML = #"""
    <table width="100%" cellpadding="0" cellspacing="0" bgcolor="#f4f4f5" style="background:#f4f4f5;padding:24px 0;font-family:-apple-system,Helvetica,Arial,sans-serif">
    <tr><td align="center">
    <table width="100%" cellpadding="0" cellspacing="0" style="max-width:520px;background:#ffffff;border-radius:12px;padding:28px">
    <tr><td style="font-size:20px;font-weight:700;color:#18181b;padding-bottom:4px">Canvasly</td></tr>
    <tr><td style="font-size:14px;color:#71717a;padding-bottom:20px">Receipt #CV-20481 · 16 June 2026</td></tr>
    <tr><td style="font-size:32px;font-weight:700;color:#18181b;padding-bottom:20px">€144.00</td></tr>
    <tr><td><table width="100%" cellpadding="0" cellspacing="0" style="font-size:14px;color:#3f3f46">
    <tr><td style="padding:8px 0;border-top:1px solid #e4e4e7">Professional plan, 8 editors</td><td align="right" style="padding:8px 0;border-top:1px solid #e4e4e7">€120.00</td></tr>
    <tr><td style="padding:8px 0;border-top:1px solid #e4e4e7">VAT (20%)</td><td align="right" style="padding:8px 0;border-top:1px solid #e4e4e7">€24.00</td></tr>
    <tr><td style="padding:8px 0;border-top:1px solid #e4e4e7;font-weight:700;color:#18181b">Total</td><td align="right" style="padding:8px 0;border-top:1px solid #e4e4e7;font-weight:700;color:#18181b">€144.00</td></tr>
    </table></td></tr>
    <tr><td style="padding-top:22px"><a href="https://canvasly.com/billing" style="display:inline-block;background:#18181b;color:#ffffff;text-decoration:none;font-size:14px;font-weight:600;padding:10px 18px;border-radius:8px">View invoice</a></td></tr>
    <tr><td style="font-size:12px;color:#a1a1aa;padding-top:22px">Charged to Visa ending 4421. Questions? Reply to this email.</td></tr>
    </table></td></tr></table>
    """#

    fileprivate static let inviteHTML = #"""
    <table cellpadding="0" cellspacing="0" style="width:100%;max-width:520px;border:1px solid #e4e4e7;border-radius:10px;font-family:-apple-system,Helvetica,Arial,sans-serif" bgcolor="#ffffff">
    <tr><td style="padding:18px 20px 6px;font-size:18px;font-weight:600;color:#18181b">Product review</td></tr>
    <tr><td style="padding:2px 20px;font-size:14px;color:#3f3f46"><b>When</b> Wednesday 17 June 2026, 14:00 to 15:00 (Berlin)</td></tr>
    <tr><td style="padding:2px 20px;font-size:14px;color:#3f3f46"><b>Where</b> Meeting room Spree, or meet.margin.app/review</td></tr>
    <tr><td style="padding:2px 20px 12px;font-size:14px;color:#3f3f46"><b>Who</b> Priya Raman (organizer), Maya Chen, Jonas Weber, Sam Okafor</td></tr>
    <tr><td style="padding:0 20px 14px;font-size:14px;color:#3f3f46">We will look at the new shared mail views and decide what ships in July.</td></tr>
    <tr><td style="padding:0 20px 18px;font-size:14px">Going? <a href="https://calendar.google.com" style="color:#1a73e8;font-weight:600;text-decoration:none">Yes</a> · <a href="https://calendar.google.com" style="color:#1a73e8;font-weight:600;text-decoration:none">Maybe</a> · <a href="https://calendar.google.com" style="color:#1a73e8;font-weight:600;text-decoration:none">No</a></td></tr>
    </table>
    """#

    fileprivate static let newsletterHTML = #"""
    <table width="100%" cellpadding="0" cellspacing="0" bgcolor="#fbfaf7" style="background:#fbfaf7;font-family:Georgia,serif">
    <tr><td align="center" style="padding:24px 16px">
    <table width="100%" cellpadding="0" cellspacing="0" style="max-width:540px">
    <tr><td><img src="https://images.shipnotes.news/header-142.png" width="540" height="120" alt="Ship Notes" style="display:block;width:100%;border-radius:8px"></td></tr>
    <tr><td style="font-size:24px;line-height:1.3;color:#1c1917;padding:22px 0 8px">The quiet power of written plans</td></tr>
    <tr><td style="font-size:16px;line-height:1.6;color:#44403c">Teams that write their plans down argue less and ship more. This week we talked to five product leads about how a one-page plan replaced their weekly status meeting, and what they keep out of it.</td></tr>
    <tr><td style="padding:18px 0"><img src="https://images.shipnotes.news/chart-142.png" width="540" height="240" alt="Chart: meetings per week" style="display:block;width:100%"></td></tr>
    <tr><td style="font-size:16px;line-height:1.6;color:#44403c">Also in this issue: the decision log template readers keep asking for, and a short read on async standups.</td></tr>
    <tr><td style="padding:20px 0"><a href="https://shipnotes.news/142" style="background:#1c1917;color:#fff;text-decoration:none;padding:10px 18px;border-radius:6px;font-family:-apple-system,Helvetica,sans-serif;font-size:14px">Read the issue</a></td></tr>
    <tr><td style="font-size:12px;color:#a8a29e;font-family:-apple-system,Helvetica,sans-serif">You get Ship Notes because you subscribed at shipnotes.news. Unsubscribe any time.</td></tr>
    </table></td></tr></table>
    """#
}

// MARK: - State

extension DemoMail {
    static let me = DemoPerson.maya.id

    func accountIndex(_ id: String) -> Int? { accounts.firstIndex { $0.id == id } }
    func threadIndex(_ key: String) -> Int? {
        if let index = threads.firstIndex(where: { $0.id == key }) { return index }
        if let n = Int(key) { return threads.firstIndex { $0.id == Self.id(.thread, n) } }
        return nil
    }

    func folderID(_ account: String, _ role: MailFolderRole) -> String? {
        folders.first { $0.accountID == account && $0.role == role }?.id
    }

    func roles(_ thread: MailThread) -> Set<MailFolderRole> {
        Set(thread.folderIDs.compactMap { id in folders.first { $0.id == id }?.role })
    }

    var visibleAccounts: [MailAccount] { accounts.filter { $0.id != Self.jonasAccount } }

    func sees(_ thread: MailThread) -> Bool {
        if visibleAccounts.contains(where: { $0.id == thread.accountID }) { return true }
        let shares = self.shares[thread.id] ?? .nobody
        return shares.everyone || shares.userIDs.contains(Self.me) || thread.assignees.contains { $0.id == Self.me }
    }

    /// Whether a member sees a conversation (for mentions).
    func sees(_ thread: MailThread, user: String, teams: [Team]) -> Bool {
        guard let account = accounts.first(where: { $0.id == thread.accountID }) else { return false }
        if account.owner?.id == user || thread.assignees.contains(where: { $0.id == user }) { return true }
        func reaches(_ audience: MailAudience) -> Bool {
            audience.everyone || audience.userIDs.contains(user) || teams.contains { audience.teamIDs.contains($0.id) && ($0.memberIDs ?? []).contains(user) }
        }
        if reaches(shares[thread.id] ?? .nobody) { return true }
        return account.visibility == "shared" && reaches(account.sharedWith)
    }

    /// Recomputes a conversation's list fields from its messages and comments.
    mutating func derive(_ id: String) {
        guard let index = threads.firstIndex(where: { $0.id == id }) else { return }
        let list = (messages[id] ?? []).sorted { $0.sentAt < $1.sentAt }
        messages[id] = list
        var thread = threads[index]
        let real = list.filter { !$0.draft }
        let shown = real.isEmpty ? list : real
        let own = accounts.first { $0.id == thread.accountID }?.addresses ?? []
        if let last = shown.last {
            thread.snippet = last.snippet
            thread.lastMessageAt = last.sentAt
        }
        thread.messageCount = max(1, shown.count)
        thread.unread = list.contains(where: \.unread)
        thread.starred = list.contains(where: \.starred)
        thread.hasAttachments = list.contains { !$0.files.isEmpty }
        var seen = Set<String>()
        var people: [MailAddress] = []
        var mine: [MailAddress] = []
        for message in shown.reversed() {
            for address in [message.from] + message.to + message.cc where !address.email.isEmpty {
                let key = address.email.lowercased()
                guard !seen.contains(key) else { continue }
                seen.insert(key)
                if own.contains(key) { mine.append(address) } else { people.append(address) }
            }
        }
        thread.participants = Array((people + mine).prefix(10))
        thread.commentCount = comments[id]?.count ?? 0
        thread.shared = !(shares[id] ?? .nobody).isEmpty
        // A draft keeps the conversation in Drafts.
        if let drafts = folderID(thread.accountID, .drafts) {
            if list.contains(where: \.draft) {
                if !thread.folderIDs.contains(drafts) { thread.folderIDs.append(drafts) }
            } else {
                thread.folderIDs.removeAll { $0 == drafts }
            }
        }
        threads[index] = thread
        for i in list.indices { messages[id]?[i].folderIDs = thread.folderIDs }
    }

    func matches(_ thread: MailThread, box: MailBox) -> Bool {
        let roles = roles(thread)
        let trashOrSpam = roles.contains(.trash) || roles.contains(.spam)
        let google = accounts.first { $0.id == thread.accountID }?.provider == .google
        switch box {
        case .inbox: return roles.contains(.inbox)
        case .unread: return thread.unread && !trashOrSpam
        case .starred: return thread.starred && !roles.contains(.trash)
        case .sent: return roles.contains(.sent)
        case .drafts: return roles.contains(.drafts)
        case .archive:
            if google { return !roles.contains(.inbox) && !trashOrSpam && !roles.contains(.drafts) && !roles.contains(.sent) }
            return roles.contains(.archive)
        case .trash: return roles.contains(.trash)
        case .spam: return roles.contains(.spam)
        case .all: return !trashOrSpam
        case .shared: return !(shares[thread.id] ?? .nobody).isEmpty && !trashOrSpam
        case .assigned: return thread.assignees.contains { $0.id == Self.me } && !trashOrSpam
        }
    }

    func search(_ thread: MailThread, _ text: String) -> Bool {
        let needle = text.lowercased()
        if thread.subject.lowercased().contains(needle) { return true }
        if thread.participants.contains(where: { $0.name.lowercased().contains(needle) || $0.email.lowercased().contains(needle) }) { return true }
        return (messages[thread.id] ?? []).contains { $0.bodyText.lowercased().contains(needle) || $0.from.display.lowercased().contains(needle) }
    }

    func involves(_ thread: MailThread, _ email: String) -> Bool {
        let key = email.lowercased()
        return (messages[thread.id] ?? []).contains { message in
            ([message.from] + message.to + message.cc).contains { $0.email.lowercased() == key }
        }
    }

    func account(for thread: MailThread) -> MailThreadAccount {
        let account = accounts.first { $0.id == thread.accountID }
        return MailThreadAccount(
            id: thread.accountID, email: account?.email ?? "", name: account?.name ?? "", displayName: account?.displayName ?? "",
            color: account?.color ?? "gray", provider: account?.provider ?? .imap, signatureHTML: account?.signatureHTML ?? "",
            identities: account?.identities ?? [], defaultFrom: account?.defaultFrom ?? ""
        )
    }

    func detail(_ index: Int) -> MailThreadDetail {
        let thread = threads[index]
        return MailThreadDetail(thread: thread, account: account(for: thread), messages: messages[thread.id] ?? [], comments: comments[thread.id] ?? [],
                                shares: shares[thread.id] ?? .nobody, canAct: true)
    }

    func accountsOut() -> [MailAccount] {
        visibleAccounts.map { account in
            var account = account
            account.unread = threads.filter { $0.accountID == account.id && $0.unread && roles($0).contains(.inbox) }.count
            return account
        }
    }

    func foldersOut(_ account: String) -> [MailFolder] {
        let order: [MailFolderRole] = [.inbox, .starred, .important, .sent, .drafts, .archive, .spam, .trash, .all]
        return folders.filter { $0.accountID == account }.map { folder in
            var folder = folder
            let inFolder = threads.filter { $0.folderIDs.contains(folder.id) }
            folder.total = inFolder.count
            folder.unread = inFolder.filter(\.unread).count
            return folder
        }.sorted { a, b in
            let ia = a.role.flatMap { order.firstIndex(of: $0) } ?? 99
            let ib = b.role.flatMap { order.firstIndex(of: $0) } ?? 99
            return ia != ib ? ia < ib : a.path < b.path
        }
    }

    func counts() -> MailCounts {
        let visible = threads.filter(sees)
        let inbox = visible.filter { roles($0).contains(.inbox) }
        let mine = Set(visibleAccounts.map(\.id))
        var counts = MailCounts()
        counts.boxes = [
            "inbox": inbox.filter { $0.unread && mine.contains($0.accountID) }.count,
            "unread": visible.filter { matches($0, box: .unread) && mine.contains($0.accountID) }.count,
            "shared": inbox.filter { $0.unread && matches($0, box: .shared) }.count,
            "assigned": inbox.filter { $0.unread && matches($0, box: .assigned) }.count,
            "drafts": visible.filter { matches($0, box: .drafts) }.count,
        ]
        for account in visibleAccounts {
            counts.accounts[account.id] = inbox.filter { $0.accountID == account.id && $0.unread }.count
        }
        for folder in folders {
            let unread = visible.filter { $0.folderIDs.contains(folder.id) && $0.unread }.count
            if unread > 0 { counts.folders[folder.id] = unread }
        }
        return counts
    }
}

// MARK: - Routes

extension DemoBackend {
    /// The answer (nil inside for 204), or nil when the route isn't this area's.
    func mailRoutes(_ call: Call, in ws: inout DemoWorkspace) throws -> Data?? {
        guard call.path.first == "collab" else { return nil }
        if call.match("GET", "collab") != nil {
            let enabled = ws.mail.enabled
            return .some(encode(JSONValue.object([
                "enabled": .bool(enabled), "reason": enabled ? .null : .string("plan"),
                "providers": .object(["google": .bool(true), "microsoft": .bool(true), "imap": .bool(true)]),
                "accounts": .number(Double(enabled ? ws.mail.visibleAccounts.count : 0)),
                "storage": .object(["used_bytes": .number(enabled ? 1_840_000_000 : 0), "limit_bytes": .number(0), "history_limited": .bool(false), "full": .bool(false)]),
            ])))
        }
        guard ws.mail.enabled else { throw Failure(status: 402, code: "collab_required", detail: "Collab is not on this workspace's plan.") }
        return .some(try collabRoute(call, in: &ws))
    }

    private func changed(_ ids: [String], account: String?, folders: Bool = true, in ws: inout DemoWorkspace) {
        publish("collab.changed", ["account_id": account.map(JSONValue.string) ?? .null, "thread_ids": .array(ids.map(JSONValue.string)), "folders": .bool(folders)], in: &ws)
    }

    private func thread(_ key: String, in ws: DemoWorkspace) throws -> Int {
        guard let index = ws.mail.threadIndex(key), ws.mail.sees(ws.mail.threads[index]) else { throw notFound("conversation") }
        return index
    }

    private func strings(_ call: Call, _ key: String) -> [String] {
        call.body[key]?.array?.compactMap(\.string) ?? []
    }

    private func addresses(_ emails: [String], known: [MailThread], in mail: DemoMail) -> [MailAddress] {
        emails.map { email in
            let key = email.lowercased()
            for thread in known {
                if let match = thread.participants.first(where: { $0.email.lowercased() == key }) { return match }
            }
            return MailAddress(name: "", email: email)
        }
    }

    private func collabRoute(_ call: Call, in ws: inout DemoWorkspace) throws -> Data? {
        let query = call.query

        // Accounts, folders, counts, sidebar.
        if call.match("GET", "collab/accounts") != nil { return encode(ws.mail.accountsOut()) }
        if let id = call.match("GET", "collab/accounts/*/folders")?.first {
            guard ws.mail.visibleAccounts.contains(where: { $0.id == id }) else { throw notFound("account") }
            return encode(ws.mail.foldersOut(id))
        }
        if let id = call.match("POST", "collab/accounts/*/folders")?.first {
            guard let account = ws.mail.accounts.first(where: { $0.id == id }) else { throw notFound("account") }
            let name = (call.string("name") ?? "").trimmingCharacters(in: .whitespaces)
            guard !name.isEmpty else { throw Failure(status: 422, code: "validation_failed", detail: "Give it a name.") }
            let kind = account.provider == .google ? "label" : (call.string("kind") ?? "folder")
            if kind == "label", account.provider == .imap { throw Failure(status: 422, code: "labels_unsupported", detail: "IMAP accounts have folders only.") }
            let folder = MailFolder(id: made(), accountID: id, name: name, path: name, role: nil, kind: kind, color: kind == "label" ? "#999999" : nil, canRename: true)
            ws.mail.folders.append(folder)
            changed([], account: id, in: &ws)
            return encode(folder)
        }
        if let id = call.match("POST", "collab/accounts/*/sync")?.first {
            if let index = ws.mail.accountIndex(id) { ws.mail.accounts[index].lastSyncedAt = now() }
            return nil
        }
        if let id = call.match("PATCH", "collab/folders/*")?.first {
            guard let index = ws.mail.folders.firstIndex(where: { $0.id == id }), ws.mail.folders[index].isCustom else { throw notFound("folder") }
            if let name = call.string("name"), !name.isEmpty {
                ws.mail.folders[index].name = name
                ws.mail.folders[index].path = name
            }
            changed([], account: ws.mail.folders[index].accountID, in: &ws)
            return encode(ws.mail.folders[index])
        }
        if let id = call.match("DELETE", "collab/folders/*")?.first {
            guard let folder = ws.mail.folders.first(where: { $0.id == id }), folder.isCustom else { throw notFound("folder") }
            ws.mail.folders.removeAll { $0.id == id }
            for index in ws.mail.threads.indices { ws.mail.threads[index].folderIDs.removeAll { $0 == id } }
            changed([], account: folder.accountID, in: &ws)
            return nil
        }
        if call.match("GET", "collab/counts") != nil { return encode(ws.mail.counts()) }
        if call.match("GET", "collab/sidebar") != nil { return encode(ws.mail.prefs) }
        if call.match("PUT", "collab/sidebar") != nil {
            if let value = try? call.body.decode(MailSidebarPrefs.self) { ws.mail.prefs = value }
            return encode(ws.mail.prefs)
        }

        // Conversations.
        if call.match("GET", "collab/threads") != nil {
            var list = ws.mail.threads.filter(ws.mail.sees)
            if let folder = query["folder_id"], !folder.isEmpty {
                list = list.filter { $0.folderIDs.contains(folder) }
            } else {
                let box = MailBox(rawValue: query["box"] ?? "inbox") ?? .inbox
                list = list.filter { ws.mail.matches($0, box: box) }
                if !box.spansAccounts, query["account_id"] == nil {
                    list = list.filter { thread in ws.mail.visibleAccounts.contains { $0.id == thread.accountID } }
                }
            }
            if let account = query["account_id"], !account.isEmpty { list = list.filter { $0.accountID == account } }
            if let text = query["q"]?.trimmingCharacters(in: .whitespaces), !text.isEmpty { list = list.filter { ws.mail.search($0, text) } }
            if let with = query["with"], !with.isEmpty { list = list.filter { ws.mail.involves($0, with) } }
            if let to = query["to"], !to.isEmpty { list = list.filter { ws.mail.involves($0, to) } }
            list.sort { $0.lastMessageAt > $1.lastMessageAt }
            let offset = Int(query["cursor"] ?? "") ?? 0
            let limit = max(1, min(100, Int(query["limit"] ?? "") ?? 50))
            let slice = Array(list.dropFirst(offset).prefix(limit))
            let next = offset + slice.count < list.count ? String(offset + slice.count) : nil
            return encode(ListBody(data: slice, nextCursor: next))
        }
        if call.match("POST", "collab/threads/actions") != nil {
            let ids = strings(call, "thread_ids")
            guard let action = MailAction(rawValue: call.string("action") ?? "") else { throw Failure(status: 422, code: "validation_failed", detail: "That action isn't one the demo knows.") }
            let indexes = try ids.map { try thread($0, in: ws) }
            for index in indexes { apply(action, folderID: call.string("folder_id"), to: index, in: &ws) }
            let out = indexes.map { ws.mail.threads[$0] }
            changed(out.map(\.id), account: out.first?.accountID, in: &ws)
            return encode(JSONValue.object(["threads": DemoSeed.json(out)]))
        }
        if let key = call.match("GET", "collab/threads/*")?.first {
            return encode(ws.mail.detail(try thread(key, in: ws)))
        }
        if let key = call.match("PUT", "collab/threads/*/shares")?.first {
            let index = try thread(key, in: ws)
            let id = ws.mail.threads[index].id
            let audience = (try? call.body["shares"]?.decode(MailAudience.self)) ?? .nobody
            ws.mail.shares[id] = audience.isEmpty ? nil : audience
            ws.mail.derive(id)
            changed([id], account: ws.mail.threads[index].accountID, folders: false, in: &ws)
            return encode(ws.mail.detail(index))
        }
        if let key = call.match("PUT", "collab/threads/*/assignees")?.first {
            let index = try thread(key, in: ws)
            let members = ws.members
            ws.mail.threads[index].assignees = strings(call, "user_ids").compactMap { id in members.first { $0.id == id }?.ref }
            changed([ws.mail.threads[index].id], account: ws.mail.threads[index].accountID, folders: false, in: &ws)
            return encode(ws.mail.detail(index))
        }
        if let key = call.match("POST", "collab/threads/*/comments")?.first {
            let index = try thread(key, in: ws)
            let id = ws.mail.threads[index].id
            let html = call.string("body_html") ?? ""
            guard !HTMLText.plain(from: html).isEmpty else { throw Failure(status: 422, code: "validation_failed", detail: "Write a comment first.") }
            let mentions = strings(call, "mentions")
            let at = now()
            let comment = MailComment(id: made(), threadID: id, author: me, bodyHTML: html, mentions: mentions, createdAt: at, updatedAt: at)
            ws.mail.comments[id, default: []].append(comment)
            // A mention of someone who cannot see it shares the conversation with them.
            let teams = ws.teams
            for user in mentions where !ws.mail.sees(ws.mail.threads[index], user: user, teams: teams) {
                var audience = ws.mail.shares[id] ?? .nobody
                if !audience.userIDs.contains(user) { audience.userIDs.append(user) }
                ws.mail.shares[id] = audience
            }
            ws.mail.derive(id)
            changed([id], account: ws.mail.threads[index].accountID, folders: false, in: &ws)
            return encode(comment)
        }
        if let id = call.match("PATCH", "collab/comments/*")?.first {
            for (thread, list) in ws.mail.comments {
                guard let index = list.firstIndex(where: { $0.id == id }) else { continue }
                ws.mail.comments[thread]?[index].bodyHTML = call.string("body_html") ?? list[index].bodyHTML
                ws.mail.comments[thread]?[index].updatedAt = now()
                changed([thread], account: nil, folders: false, in: &ws)
                return encode(ws.mail.comments[thread]?[index])
            }
            throw notFound("comment")
        }
        if let id = call.match("DELETE", "collab/comments/*")?.first {
            for (thread, list) in ws.mail.comments where list.contains(where: { $0.id == id }) {
                ws.mail.comments[thread]?.removeAll { $0.id == id }
                ws.mail.derive(thread)
                changed([thread], account: nil, folders: false, in: &ws)
                return nil
            }
            throw notFound("comment")
        }
        if call.match("GET", "collab/person") != nil {
            return encode(person(query["email"] ?? "", exclude: query["exclude"], limit: Int(query["limit"] ?? "") ?? 8, in: ws))
        }
        if let id = call.match("GET", "collab/messages/*/details")?.first {
            return encode(details(id, in: ws))
        }

        // Sending and drafts.
        if call.match("POST", "collab/messages") != nil {
            let message = try compose(call, draft: false, in: &ws)
            return encode(message)
        }
        if call.match("POST", "collab/drafts") != nil {
            return encode(try compose(call, draft: true, in: &ws))
        }
        if let id = call.match("PUT", "collab/drafts/*")?.first {
            return encode(try saveDraft(id, call, in: &ws))
        }
        if let id = call.match("POST", "collab/drafts/*/send")?.first {
            if call.body["to"] != nil { _ = try saveDraft(id, call, in: &ws) }
            guard let (thread, index) = locate(id, in: ws.mail) else { throw notFound("draft") }
            var message = ws.mail.messages[thread]![index]
            guard !(message.to + message.cc + message.bcc).isEmpty else { throw Failure(status: 422, code: "validation_failed", detail: "Add at least one recipient.") }
            message.draft = false
            message.draftID = nil
            message.sentAt = now()
            message.sentBy = me
            ws.mail.messages[thread]![index] = message
            if let t = ws.mail.threadIndex(thread), let sent = ws.mail.folderID(ws.mail.threads[t].accountID, .sent), !ws.mail.threads[t].folderIDs.contains(sent) {
                ws.mail.threads[t].folderIDs.append(sent)
            }
            ws.mail.derive(thread)
            changed([thread], account: message.accountID, in: &ws)
            return encode(ws.mail.messages[thread]?.first { $0.id == message.id })
        }
        if let id = call.match("DELETE", "collab/drafts/*")?.first {
            guard let (thread, _) = locate(id, in: ws.mail) else { throw notFound("draft") }
            ws.mail.messages[thread]?.removeAll { $0.id == id }
            let account = ws.mail.threads.first { $0.id == thread }?.accountID
            if ws.mail.messages[thread]?.isEmpty ?? true {
                ws.mail.threads.removeAll { $0.id == thread }
                ws.mail.messages[thread] = nil
            } else {
                ws.mail.derive(thread)
            }
            changed([thread], account: account, in: &ws)
            return nil
        }
        throw Failure(status: 404, code: "not_found", detail: "The demo has no mail answer for \(call.method) /v1/\(call.path.joined(separator: "/")).")
    }

    private func locate(_ messageID: String, in mail: DemoMail) -> (String, Int)? {
        for (thread, list) in mail.messages {
            if let index = list.firstIndex(where: { $0.id == messageID }) { return (thread, index) }
        }
        return nil
    }

    private func apply(_ action: MailAction, folderID: String?, to index: Int, in ws: inout DemoWorkspace) {
        var thread = ws.mail.threads[index]
        let mail = ws.mail
        let id = thread.id
        func role(_ role: MailFolderRole) -> String? { mail.folderID(thread.accountID, role) }
        func drop(_ roles: [MailFolderRole]) {
            let ids = Set(roles.compactMap(role))
            thread.folderIDs.removeAll { ids.contains($0) }
        }
        func add(_ folder: String?) { if let folder, !thread.folderIDs.contains(folder) { thread.folderIDs.append(folder) } }
        let google = mail.accounts.first { $0.id == thread.accountID }?.provider == .google
        var list = ws.mail.messages[id] ?? []
        switch action {
        case .read: for i in list.indices { list[i].unread = false }
        case .unread: if let last = list.indices.last(where: { !list[$0].draft }) { list[last].unread = true }
        case .star:
            if let last = list.indices.last { list[last].starred = true }
            add(role(.starred))
        case .unstar:
            for i in list.indices { list[i].starred = false }
            drop([.starred])
        case .archive:
            drop([.inbox])
            if !google { add(role(.archive)) }
        case .inbox, .notSpam:
            drop([.trash, .spam, .archive])
            add(role(.inbox))
        case .trash:
            drop([.inbox, .spam, .archive])
            add(role(.trash))
        case .spam:
            drop([.inbox, .trash, .archive])
            add(role(.spam))
        case .move:
            guard let folderID else { break }
            drop([.inbox, .archive, .trash, .spam])
            if !google {
                let custom = Set(mail.folders.filter { $0.accountID == thread.accountID && $0.isCustom && !$0.isLabel }.map(\.id))
                thread.folderIDs.removeAll { custom.contains($0) }
            }
            add(folderID)
        case .label: add(folderID)
        case .unlabel: thread.folderIDs.removeAll { $0 == folderID }
        }
        ws.mail.messages[id] = list
        ws.mail.threads[index] = thread
        ws.mail.derive(id)
    }

    /// A new message or draft from the composer's body (`POST /v1/collab/messages`, `/drafts`).
    private func compose(_ call: Call, draft: Bool, in ws: inout DemoWorkspace) throws -> MailMessage {
        let mode = MailComposeMode(rawValue: call.string("mode") ?? "new") ?? .new
        var accountID = call.string("account_id") ?? ""
        var threadID: String?
        var original: MailMessage?
        if mode != .new {
            guard let key = call.string("message_id"), let (thread, index) = locate(key, in: ws.mail) else { throw notFound("message") }
            original = ws.mail.messages[thread]?[index]
            accountID = original?.accountID ?? accountID
            if mode != .forward { threadID = thread }
        }
        guard let account = ws.mail.accounts.first(where: { $0.id == accountID }) else { throw notFound("account") }
        let to = strings(call, "to"), cc = strings(call, "cc"), bcc = strings(call, "bcc")
        if !draft, (to + cc + bcc).isEmpty { throw Failure(status: 422, code: "validation_failed", detail: "Add at least one recipient.") }
        let fromEmail = (call.string("from_email") ?? original?.ownAddress ?? account.defaultFrom).lowercased()
        let identity = account.identities.first { $0.email == fromEmail }
        let from = MailAddress(name: identity?.fromName ?? account.displayName, email: fromEmail)
        let known = ws.mail.threads
        let html = call.string("body_html") ?? ""
        var files = strings(call, "attachment_ids").enumerated().compactMap { offset, id -> MailAttachment? in
            guard let upload = uploads[id] else { return nil }
            return MailAttachment(id: String(offset), filename: upload.filename, contentType: upload.contentType, size: upload.sizeBytes, url: upload.url)
        }
        if mode == .forward, call.body["forward_attachments"]?.bool ?? true, let original {
            files += original.files.enumerated().map { offset, file in
                var file = file
                file.id = String(files.count + offset)
                return file
            }
        }
        let at = now()
        let thread: String
        if let threadID {
            thread = threadID
        } else {
            thread = made()
            ws.mail.threads.append(MailThread(id: thread, accountID: accountID, subject: call.string("subject") ?? "", lastMessageAt: at))
        }
        let id = made()
        let message = MailMessage(
            id: id, threadID: thread, accountID: accountID, messageID: "\(id)@\(fromEmail.split(separator: "@").last ?? "mail")",
            from: from, to: addresses(to, known: known, in: ws.mail), cc: addresses(cc, known: known, in: ws.mail), bcc: addresses(bcc, known: known, in: ws.mail),
            subject: call.string("subject") ?? "", snippet: DemoSeed.preview(html), bodyHTML: html, bodyText: HTMLText.plain(from: html), sentAt: at,
            draft: draft, attachments: files, sentBy: me, draftID: draft ? id : nil, inReplyToID: original?.id, ownAddress: fromEmail
        )
        ws.mail.messages[thread, default: []].append(message)
        if !draft, let index = ws.mail.threadIndex(thread), let sent = ws.mail.folderID(accountID, .sent), !ws.mail.threads[index].folderIDs.contains(sent) {
            ws.mail.threads[index].folderIDs.append(sent)
        }
        ws.mail.derive(thread)
        changed([thread], account: accountID, in: &ws)
        return ws.mail.messages[thread]?.first { $0.id == id } ?? message
    }

    private func saveDraft(_ id: String, _ call: Call, in ws: inout DemoWorkspace) throws -> MailMessage {
        guard let (thread, index) = locate(id, in: ws.mail), var message = ws.mail.messages[thread]?[index], message.draft else { throw notFound("draft") }
        let known = ws.mail.threads
        if call.body["to"] != nil { message.to = addresses(strings(call, "to"), known: known, in: ws.mail) }
        message.cc = addresses(strings(call, "cc"), known: known, in: ws.mail)
        message.bcc = addresses(strings(call, "bcc"), known: known, in: ws.mail)
        if let subject = call.string("subject") { message.subject = subject }
        if let html = call.string("body_html") {
            message.bodyHTML = html
            message.bodyText = HTMLText.plain(from: html)
            message.snippet = DemoSeed.preview(html)
        }
        if let keep = call.body["keep_attachments"]?.array?.compactMap(\.string) {
            message.attachments = message.attachments.filter { keep.contains($0.id) }
        }
        let added = strings(call, "attachment_ids").compactMap { uploads[$0] }
        for upload in added {
            message.attachments.append(MailAttachment(id: String(message.attachments.count), filename: upload.filename, contentType: upload.contentType, size: upload.sizeBytes, url: upload.url))
        }
        if let from = call.string("from_email")?.lowercased(), let account = ws.mail.accounts.first(where: { $0.id == message.accountID }),
           let identity = account.identities.first(where: { $0.email == from }) {
            message.from = MailAddress(name: identity.fromName, email: from)
            message.ownAddress = from
        }
        message.sentAt = now()
        ws.mail.messages[thread]?[index] = message
        if let t = ws.mail.threadIndex(thread), ws.mail.messages[thread]?.count == 1 { ws.mail.threads[t].subject = message.subject }
        ws.mail.derive(thread)
        changed([thread], account: message.accountID, in: &ws)
        return ws.mail.messages[thread]?.first { $0.id == id } ?? message
    }

    private func person(_ email: String, exclude: String?, limit: Int, in ws: DemoWorkspace) -> MailPerson {
        let key = email.lowercased()
        let mail = ws.mail
        let threads = mail.threads.filter { mail.sees($0) && mail.involves($0, key) && !mail.roles($0).contains(.trash) && !mail.roles($0).contains(.spam) }
            .sorted { $0.lastMessageAt > $1.lastMessageAt }
        let others = threads.filter { $0.id != exclude }
        var name = ""
        var dates: [Date] = []
        var files: [MailPerson.File] = []
        for thread in threads {
            for message in mail.messages[thread.id] ?? [] where !message.draft {
                let from = message.from.email.lowercased() == key
                if from || (message.to + message.cc).contains(where: { $0.email.lowercased() == key }) { dates.append(message.sentAt) }
                if from {
                    if name.isEmpty { name = message.from.name }
                    files += message.files.map { MailPerson.File(threadID: thread.id, messageID: message.id, sentAt: message.sentAt, attachment: $0) }
                }
            }
        }
        if name.isEmpty { name = threads.lazy.flatMap(\.participants).first { $0.email.lowercased() == key }?.name ?? "" }
        return MailPerson(email: key, name: name, conversations: threads.count, others: others.count, firstAt: dates.min(), lastAt: dates.max(),
                          threads: Array(others.prefix(max(1, min(20, limit)))), files: Array(files.sorted { $0.sentAt > $1.sentAt }.prefix(6)))
    }

    private func details(_ id: String, in ws: DemoWorkspace) -> MailMessageDetails {
        guard let (thread, index) = locate(id, in: ws.mail), let message = ws.mail.messages[thread]?[index] else { return MailMessageDetails() }
        let domain = String(message.from.email.split(separator: "@").last ?? "")
        var headers: [MailMessageDetails.Header] = [
            .init(name: "Delivered-To", value: message.ownAddress ?? ""),
            .init(name: "Received", value: "from mail.\(domain) (mail.\(domain) [185.12.64.10]) by mx.google.com with ESMTPS id \(abs(message.id.hashValue % 99999)); \(DemoSeed.stamp(message.sentAt))"),
            .init(name: "Authentication-Results", value: "mx.google.com; spf=pass smtp.mailfrom=\(domain); dkim=pass header.d=\(domain); dmarc=pass header.from=\(domain)"),
            .init(name: "From", value: message.from.name.isEmpty ? message.from.email : "\(message.from.name) <\(message.from.email)>"),
            .init(name: "To", value: message.to.map(\.email).joined(separator: ", ")),
            .init(name: "Subject", value: message.subject),
            .init(name: "Date", value: DemoSeed.stamp(message.sentAt)),
            .init(name: "Message-ID", value: "<\(message.messageID)>"),
            .init(name: "MIME-Version", value: "1.0"),
            .init(name: "Content-Type", value: message.files.isEmpty ? "text/html; charset=UTF-8" : "multipart/mixed; boundary=\"od-demo\""),
        ]
        if !message.cc.isEmpty { headers.insert(.init(name: "Cc", value: message.cc.map(\.email).joined(separator: ", ")), at: 5) }
        if ws.mail.accounts.contains(where: { $0.addresses.contains(message.from.email.lowercased()) }) {
            return MailMessageDetails(headers: headers.filter { $0.name != "Authentication-Results" && $0.name != "Received" })
        }
        if domain.hasSuffix(".top") {
            return MailMessageDetails(mailedBy: "bulk-sender.ru", signedBy: [], via: "bulk-sender.ru", spf: "fail", dkim: "none", dmarc: "fail", encryption: "none", warning: "spoofed", headers: headers)
        }
        return MailMessageDetails(mailedBy: domain, signedBy: [domain], spf: "pass", dkim: "pass", dmarc: "pass", encryption: "tls", headers: headers)
    }
}
#endif
