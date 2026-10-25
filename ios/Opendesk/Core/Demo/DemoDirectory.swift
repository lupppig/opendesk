#if DEBUG
import Foundation

// Contacts and companies routes.

/// Contacts and companies: everyone who wrote in on the workspace's tickets,
/// with phones, titles, notes, labels and fields, grouped by business email
/// domain into companies. Ticket and people counts are worked out live.
struct DemoDirectory: Sendable {
    var contacts: [Contact] = []
    var companies: [Company] = []
    /// Labels for contacts and companies (`applies_to`), apart from the ticket labels in `DemoWorkspace.tags`.
    var tags: [Tag] = []
    var fields: [CustomField] = []

    // MARK: Ids

    static func companyID(_ n: Int) -> String { String(format: "0197a0c0-%04x-7000-8000-%012d", 0x20, n) }
    static func fieldID(_ n: Int) -> String { String(format: "0197a0c0-%04x-7000-8000-%012d", 0x21, n) }
    static func tagID(_ n: Int) -> String { String(format: "0197a0c0-%04x-7000-8000-%012d", 0x22, n) }
    static func extraContactID(_ n: Int) -> String { String(format: "0197a0c0-%04x-7000-8000-%012d", 0x23, n) }

    /// Mail providers, and the spammers' domains, never become companies.
    static let personalDomains: Set<String> = [
        "gmail.com", "googlemail.com", "proton.me", "protonmail.com", "icloud.com", "me.com", "fastmail.com",
        "outlook.com", "hotmail.com", "live.com", "yahoo.com", "gmx.de", "web.de",
        "rank-boost-pro.biz", "prize-center.top", "rnargin-support.com",
    ]

    static let knownDomains = [
        "oakridgehealth.org", "northwind.com", "brightline.io", "halcyon.studio", "vantagelegal.com", "paperplane.co",
        "meridian.edu", "fjord.ai", "copperleaf.dev", "kestrel.tech", "tidewater.fm", "solsticebakery.com",
    ]

    static let companyNames: [String: String] = [
        "oakridgehealth.org": "Oakridge Health", "northwind.com": "Northwind Traders", "brightline.io": "Brightline Logistics",
        "halcyon.studio": "Halcyon Studio", "vantagelegal.com": "Vantage Legal", "paperplane.co": "Paperplane",
        "meridian.edu": "Meridian University", "fjord.ai": "Fjord AI", "copperleaf.dev": "Copperleaf",
        "kestrel.tech": "Kestrel", "tidewater.fm": "Tidewater FM", "solsticebakery.com": "Solstice Bakery",
    ]

    struct Person {
        var phone = ""
        var title = ""
        var plan: String?
        var language = "en"
        var beta = false
        var tags: [String] = []
        var notes = ""
        var external: String?
    }

    static let people: [String: Person] = [
        "dana@oakridgehealth.org": Person(phone: "+1 503 555 0142", title: "IT director", plan: "enterprise", tags: ["Decision maker", "VIP"],
                                          notes: "Prefers email over calls. Loop in Anders on anything to do with SSO or the audit log.", external: "crm-10482"),
        "anders@oakridgehealth.org": Person(phone: "+1 503 555 0187", title: "Security engineer", plan: "enterprise", tags: ["Champion"], external: "crm-10497"),
        "marcus@northwind.com": Person(phone: "+46 8 555 210 44", title: "Head of operations", plan: "business", language: "sv", tags: ["Decision maker"],
                                       notes: "Evaluating a move from three regional inboxes to one. Wants a quote before the end of the quarter.", external: "crm-10233"),
        "stefan@northwind.com": Person(phone: "+49 30 5557 2190", title: "Procurement", plan: "business", language: "de", external: "crm-10234"),
        "tomas@brightline.io": Person(phone: "+34 91 555 0193", title: "Logistics lead", plan: "team", language: "es", beta: true, tags: ["Beta tester"], external: "crm-10871"),
        "rosa@brightline.io": Person(title: "Operations", plan: "team", language: "es", external: "crm-10872"),
        "elena@halcyon.studio": Person(phone: "+39 02 5550 3317", title: "Studio manager", plan: "team", language: "it", tags: ["Champion"],
                                       notes: "Runs support for the studio herself. Very responsive on chat in the mornings (CET).", external: "crm-11020"),
        "vera@halcyon.studio": Person(title: "Designer", plan: "team", language: "it", beta: true, tags: ["Beta tester"]),
        "hugo@vantagelegal.com": Person(phone: "+33 1 55 50 28 61", title: "Partner", plan: "business", language: "fr", tags: ["VIP", "Decision maker"],
                                        notes: "Needs the DPA countersigned before any data leaves the EU region.", external: "crm-10655"),
        "chloe@paperplane.co": Person(phone: "+33 6 55 51 90 22", title: "Product designer", plan: "team", language: "fr", beta: true, tags: ["Beta tester"], external: "crm-10910"),
        "paula@paperplane.co": Person(phone: "+34 655 510 873", title: "Finance", plan: "team", language: "es", tags: ["Churn risk"],
                                      notes: "Asked twice about downgrading. Offer the annual discount before the renewal.", external: "crm-10911"),
        "ben@meridian.edu": Person(phone: "+49 89 5550 1180", title: "Systems administrator", plan: "business", language: "de", external: "crm-10301"),
        "kofi@meridian.edu": Person(title: "Lecturer", plan: "business"),
        "freya@fjord.ai": Person(phone: "+47 22 55 50 91", title: "Founder", plan: "business", language: "no", tags: ["VIP", "Decision maker"],
                                 notes: "Early customer and a friend of the team. Happy to be a reference.", external: "crm-10077"),
        "umar@fjord.ai": Person(title: "ML engineer", plan: "business", beta: true, tags: ["Beta tester"], external: "crm-10078"),
        "gabriel@copperleaf.dev": Person(phone: "+33 4 55 50 72 18", title: "Engineering manager", plan: "team", language: "fr", tags: ["Champion"], external: "crm-10544"),
        "hana@copperleaf.dev": Person(title: "Developer", plan: "team", beta: true, tags: ["Beta tester"]),
        "aisha@kestrel.tech": Person(phone: "+44 20 7946 0321", title: "CTO", plan: "enterprise", tags: ["Decision maker"],
                                     notes: "Security review in progress. Send anything about subprocessors to her directly.", external: "crm-10760"),
        "oliver@kestrel.tech": Person(phone: "+44 20 7946 0388", title: "Developer", plan: "enterprise", external: "crm-10761"),
        "isaac@tidewater.fm": Person(phone: "+1 212 555 0156", title: "Producer", plan: "team", tags: ["Churn risk"], external: "crm-11102"),
        "julia@solsticebakery.com": Person(phone: "+420 2 5550 4471", title: "Owner", plan: "free", language: "cs",
                                           notes: "Small team of four. Asked about the nonprofit discount once; they don't qualify, but be kind about it."),
        "yusuf.moreau@gmail.com": Person(phone: "+33 6 55 52 11 09", plan: "free", language: "fr"),
        "zoe.petrova@proton.me": Person(plan: "free", beta: true, tags: ["Beta tester"]),
        "nina.fischer@icloud.com": Person(phone: "+49 151 5550 2290", plan: "free", language: "de"),
        "laura.silva@fastmail.com": Person(plan: "team", language: "pt"),
        "mateo.costa@outlook.com": Person(phone: "+351 91 555 0712", plan: "free", language: "pt"),
        "tara.singh@gmail.com": Person(plan: "free"),
        "will.turner@proton.me": Person(phone: "+44 7700 900 512", plan: "free"),
        "daniel.kim@gmail.com": Person(plan: "free"),
        "ines.martins@gmail.com": Person(plan: "free", language: "pt", notes: "Wrote in about a charge from a different service. Kind, but not a customer."),
        "security@rnargin-support.com": Person(notes: "Phishing. Pretends to be Margin's own security team. Never click the links."),
        "rewards@prize-center.top": Person(notes: "Gift card scam."),
    ]

    struct CompanyDetail {
        var plan: String
        var seats: Int
        var renewalDays: Int
        var tags: [String]
        var notes = ""
    }

    static let companyDetails: [String: CompanyDetail] = [
        "oakridgehealth.org": CompanyDetail(plan: "enterprise", seats: 140, renewalDays: 32, tags: ["Enterprise", "Strategic"],
                                            notes: "Healthcare: HIPAA questions go to Aisha's security pack. Renews every January, signed by Dana."),
        "northwind.com": CompanyDetail(plan: "business", seats: 520, renewalDays: 75, tags: ["Strategic", "Renewal due"],
                                       notes: "Consolidating three regional inboxes. Quote for 520 seats, billed yearly, is with procurement."),
        "brightline.io": CompanyDetail(plan: "team", seats: 18, renewalDays: 140, tags: ["Partner"]),
        "halcyon.studio": CompanyDetail(plan: "team", seats: 9, renewalDays: 210, tags: []),
        "vantagelegal.com": CompanyDetail(plan: "business", seats: 60, renewalDays: 19, tags: ["Renewal due"], notes: "DPA under review by their compliance team."),
        "paperplane.co": CompanyDetail(plan: "team", seats: 24, renewalDays: 48, tags: ["Trial"]),
        "meridian.edu": CompanyDetail(plan: "business", seats: 300, renewalDays: 260, tags: ["Enterprise"]),
        "fjord.ai": CompanyDetail(plan: "business", seats: 45, renewalDays: 300, tags: ["Strategic", "Partner"], notes: "Design partner for the AI triage beta."),
        "copperleaf.dev": CompanyDetail(plan: "team", seats: 12, renewalDays: 95, tags: []),
        "kestrel.tech": CompanyDetail(plan: "enterprise", seats: 210, renewalDays: 160, tags: ["Enterprise"]),
        "tidewater.fm": CompanyDetail(plan: "team", seats: 7, renewalDays: 11, tags: ["Renewal due"]),
        "solsticebakery.com": CompanyDetail(plan: "free", seats: 4, renewalDays: 365, tags: []),
    ]

    static let contactTags: [(String, String, String)] = [
        ("VIP", "violet", "Answer first, and loop in their account owner"),
        ("Decision maker", "blue", "Signs off on plans and renewals"),
        ("Champion", "green", "Our advocate inside the company"),
        ("Beta tester", "amber", "Has early features turned on"),
        ("Churn risk", "red", "Unhappy or looking at alternatives"),
    ]

    static let companyTags: [(String, String, String)] = [
        ("Enterprise", "indigo", "On an enterprise contract"),
        ("Strategic", "violet", "Accounts we plan around"),
        ("Partner", "teal", "Resells or integrates with us"),
        ("Trial", "amber", "Still trying the product"),
        ("Renewal due", "orange", "Renews within three months"),
    ]

    static func options(_ values: [(String, String, String)]) -> [FieldOption] {
        values.map { FieldOption(id: $0.0, label: $0.1, color: $0.2) }
    }

    static let planOptions = options([("free", "Free", "gray"), ("team", "Team", "blue"), ("business", "Business", "violet"), ("enterprise", "Enterprise", "indigo")])

    // MARK: Seed

    static func seed(_ ws: DemoWorkspace) -> DemoDirectory {
        var directory = DemoDirectory()
        directory.tags = contactTags.enumerated().map { index, tag in
            Tag(id: tagID(index + 1), name: tag.0, color: tag.1, description: tag.2, groupID: nil, archived: false, appliesTo: ["contacts"])
        } + companyTags.enumerated().map { index, tag in
            Tag(id: tagID(index + 21), name: tag.0, color: tag.1, description: tag.2, groupID: nil, archived: false, appliesTo: ["companies"])
        }
        directory.fields = [
            CustomField(id: fieldID(1), entity: "contact", key: "job_title", name: "Job title", type: .text, position: 0),
            CustomField(id: fieldID(2), entity: "contact", key: "plan", name: "Plan", type: .select, options: planOptions, position: 1),
            CustomField(id: fieldID(3), entity: "contact", key: "language", name: "Language", type: .select, options: options([
                ("en", "English", "gray"), ("de", "German", "gray"), ("fr", "French", "gray"), ("es", "Spanish", "gray"), ("it", "Italian", "gray"),
                ("pt", "Portuguese", "gray"), ("sv", "Swedish", "gray"), ("no", "Norwegian", "gray"), ("cs", "Czech", "gray"),
            ]), position: 2),
            CustomField(id: fieldID(4), entity: "contact", key: "beta", name: "Beta features", type: .checkbox, description: "Early features are on for them", position: 3),
            CustomField(id: fieldID(11), entity: "company", key: "plan", name: "Plan", type: .select, options: planOptions, position: 0),
            CustomField(id: fieldID(12), entity: "company", key: "seats", name: "Seats", type: .number, position: 1),
            CustomField(id: fieldID(13), entity: "company", key: "renewal", name: "Renewal", type: .date, position: 2),
            CustomField(id: fieldID(14), entity: "company", key: "account_owner", name: "Account owner", type: .user, position: 3),
            CustomField(id: fieldID(15), entity: "company", key: "website", name: "Website", type: .url, position: 4),
        ]

        // Everyone who wrote in, first ticket first.
        var refs: [ContactRef] = []
        var firstSeen: [String: Date] = [:]
        for ticket in ws.tickets.sorted(by: { $0.createdAt < $1.createdAt }) {
            guard let contact = ticket.contact else { continue }
            if firstSeen[contact.id] == nil { refs.append(contact) }
            firstSeen[contact.id] = min(firstSeen[contact.id] ?? ticket.createdAt, ticket.createdAt)
        }
        if ws.id == DemoSeed.marginID {
            refs.append(ContactRef(id: extraContactID(1), email: "lucas@northwind.com", name: "Lucas Ferreira", avatarURL: nil))
            refs.append(ContactRef(id: extraContactID(2), email: "emma@oakridgehealth.org", name: "Emma Sato", avatarURL: nil))
        }

        // Companies by business domain, in the order their first person wrote in.
        var companyByDomain: [String: Company] = [:]
        var owners = ws.members.map(\.user.id)
        if owners.isEmpty { owners = [""] }
        for ref in refs {
            guard let domain = domain(of: ref.email), !personalDomains.contains(domain), companyByDomain[domain] == nil else { continue }
            // Known companies keep a fixed id (Oakridge Health is 1), others follow.
            let n = (knownDomains.firstIndex(of: domain).map { $0 + 1 }) ?? (50 + companyByDomain.count)
            let detail = companyDetails[domain]
            var fields: [String: JSONValue] = ["website": .string("https://" + domain)]
            if let detail {
                fields["plan"] = .string(detail.plan)
                fields["seats"] = .number(Double(detail.seats))
                fields["renewal"] = .string(DirectoryFieldText.string(DemoMode.anchor.addingTimeInterval(Double(detail.renewalDays) * 86400)))
                fields["account_owner"] = .string(owners[(n - 1) % owners.count])
            }
            let created = (firstSeen[ref.id] ?? DemoMode.anchor).addingTimeInterval(-Double(90 + (n * 61) % 600) * 86400)
            companyByDomain[domain] = Company(
                id: companyID(n + (ws.id == DemoSeed.marginID ? 0 : 100)),
                name: companyNames[domain] ?? title(for: domain),
                domain: domain,
                notes: detail?.notes ?? "",
                tags: (detail?.tags ?? []).compactMap { name in directory.tags.first { $0.name == name }?.ref },
                fields: fields,
                createdAt: created,
                updatedAt: created
            )
        }
        directory.companies = companyByDomain.values.sorted { $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending }

        for (index, ref) in refs.enumerated() {
            let email = ref.email?.lowercased() ?? ""
            let person = people[email] ?? Person()
            let company = domain(of: email).flatMap { companyByDomain[$0] }
            var fields: [String: JSONValue] = [:]
            if !person.title.isEmpty { fields["job_title"] = .string(person.title) }
            if let plan = person.plan { fields["plan"] = .string(plan) }
            if !email.isEmpty, !personalDomains.contains(domain(of: email) ?? "") || person.plan != nil {
                fields["language"] = .string(person.language)
            }
            if person.beta { fields["beta"] = .bool(true) }
            let seen = firstSeen[ref.id] ?? DemoSeed.ago(Double(3 + index) * 1440)
            let spam = ["rewards@prize-center.top", "security@rnargin-support.com", "growth@rank-boost-pro.biz"].contains(email)
            // Customers were around long before their latest ticket; spammers turn up with theirs.
            let days = spam ? 0 : 30 + (index * 47) % 520
            let created = seen.addingTimeInterval(-Double(days) * 86400 - 3600)
            directory.contacts.append(Contact(
                id: ref.id,
                email: ref.email,
                name: ref.name,
                phone: person.phone,
                avatarURL: ref.avatarURL,
                company: company?.ref,
                externalID: person.external,
                notes: person.notes,
                tags: person.tags.compactMap { name in directory.tags.first { $0.name == name }?.ref },
                fields: fields,
                blocked: spam && email != "growth@rank-boost-pro.biz",
                createdAt: created,
                updatedAt: created
            ))
        }
        directory.contacts.sort { $0.createdAt > $1.createdAt }
        return directory
    }

    static func domain(of email: String?) -> String? {
        guard let email, let at = email.lastIndex(of: "@") else { return nil }
        let domain = email[email.index(after: at)...].lowercased()
        return domain.isEmpty ? nil : domain
    }

    static func title(for domain: String) -> String {
        let first = domain.split(separator: ".").first.map(String.init) ?? domain
        return first.prefix(1).uppercased() + first.dropFirst()
    }
}

// MARK: - Routes

extension DemoBackend {
    /// The answer (nil inside for 204), or nil when the route isn't this area's.
    func directoryRoutes(_ call: Call, in ws: inout DemoWorkspace) throws -> Data?? {
        let query = call.query

        // Labels and fields for contacts and companies; ticket labels stay with the core routes.
        if call.match("GET", "tags") != nil {
            guard let scope = query["applies_to"], scope == "contacts" || scope == "companies" else { return nil }
            let tags = (ws.directory.tags + ws.tags).filter { $0.appliesTo?.contains(scope) == true && $0.archived != true }
            return .some(encode(ListBody(data: tags)))
        }
        if call.match("GET", "custom-fields") != nil {
            let entity = query["entity"]
            guard entity == nil || entity == "contact" || entity == "company" else { return nil }
            let fields = ws.directory.fields.filter { entity == nil || $0.entity == entity }
            return .some(encode(ListBody(data: fields)))
        }

        // Contacts.
        if call.match("GET", "contacts") != nil {
            var list = ws.directory.contacts
            if let q = query["q"]?.trimmingCharacters(in: .whitespaces), !q.isEmpty {
                list = list.filter { contact in
                    [contact.name, contact.email ?? "", contact.phone, contact.company?.name ?? "", contact.externalID ?? ""]
                        .contains { $0.localizedCaseInsensitiveContains(q) }
                }
            }
            if let company = query["company_id"], !company.isEmpty { list = list.filter { $0.company?.id == company } }
            if let tag = query["tag"], !tag.isEmpty { list = list.filter { $0.tags.contains { $0.name.caseInsensitiveCompare(tag) == .orderedSame || $0.id == tag } } }
            for (key, value) in query where key.hasPrefix("field.") {
                let field = String(key.dropFirst(6))
                list = list.filter { $0.fields[field]?.displayText == value || $0.fields[field]?.string == value }
            }
            list.sort { $0.createdAt > $1.createdAt }
            return .some(page(list.map { live($0, in: ws) }, query))
        }
        if call.match("POST", "contacts") != nil {
            var contact = Contact(id: made(), email: nil, name: "", createdAt: now())
            let body = call.body.object ?? [:]
            try apply(body, to: &contact, in: &ws, creating: true)
            contact.updatedAt = contact.createdAt
            ws.directory.contacts.insert(contact, at: 0)
            let answer = live(contact, in: ws)
            publish("contact.created", ["contact": DemoSeed.json(answer)], in: &ws)
            return .some(encode(answer))
        }
        if let id = call.match("GET", "contacts/*/tickets")?.first {
            guard ws.directory.contacts.contains(where: { $0.id == id }) else { throw notFound("contact") }
            let tickets = ws.tickets.filter { $0.contact?.id == id }.sorted { $0.lastMessageAt > $1.lastMessageAt }
            return .some(page(tickets, query))
        }
        if let id = call.match("PUT", "contacts/*/avatar")?.first {
            let index = try contactIndex(id, in: ws)
            let picture = upload(call)
            guard picture.contentType.hasPrefix("image/") else {
                throw Failure(status: 422, code: "invalid_image", detail: "Choose a PNG, JPEG, GIF or WebP image.")
            }
            ws.directory.contacts[index].avatarURL = picture.url
            ws.directory.contacts[index].avatarSource = "upload"
            return .some(contactChanged(index, in: &ws))
        }
        if let id = call.match("DELETE", "contacts/*/avatar")?.first {
            let index = try contactIndex(id, in: ws)
            ws.directory.contacts[index].avatarURL = nil
            ws.directory.contacts[index].avatarSource = nil
            return .some(contactChanged(index, in: &ws))
        }
        if let id = call.match("GET", "contacts/*")?.first {
            return .some(encode(live(ws.directory.contacts[try contactIndex(id, in: ws)], in: ws)))
        }
        if let id = call.match("PATCH", "contacts/*")?.first {
            let index = try contactIndex(id, in: ws)
            var contact = ws.directory.contacts[index]
            try apply(call.body.object ?? [:], to: &contact, in: &ws, creating: false)
            ws.directory.contacts[index] = contact
            return .some(contactChanged(index, in: &ws))
        }
        if let id = call.match("DELETE", "contacts/*")?.first {
            let index = try contactIndex(id, in: ws)
            ws.directory.contacts.remove(at: index)
            publish("contact.deleted", ["contact_id": .string(id)], in: &ws)
            return .some(nil)
        }

        // Companies.
        if call.match("GET", "companies") != nil {
            var list = ws.directory.companies
            if let q = query["q"]?.trimmingCharacters(in: .whitespaces), !q.isEmpty {
                list = list.filter { $0.name.localizedCaseInsensitiveContains(q) || $0.domain.localizedCaseInsensitiveContains(q) }
            }
            if let tag = query["tag"], !tag.isEmpty { list = list.filter { $0.tags.contains { $0.name.caseInsensitiveCompare(tag) == .orderedSame || $0.id == tag } } }
            list.sort { $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending }
            return .some(page(list.map { live($0, in: ws) }, query))
        }
        if call.match("POST", "companies") != nil {
            var company = Company(id: made(), name: "", domain: "", createdAt: now())
            try apply(call.body.object ?? [:], to: &company, in: &ws, creating: true)
            company.updatedAt = company.createdAt
            ws.directory.companies.append(company)
            let answer = live(company, in: ws)
            publish("company.created", ["company": DemoSeed.json(answer)], in: &ws)
            return .some(encode(answer))
        }
        if let id = call.match("PUT", "companies/*/avatar")?.first {
            let index = try companyIndex(id, in: ws)
            let picture = upload(call)
            guard picture.contentType.hasPrefix("image/") else {
                throw Failure(status: 422, code: "invalid_image", detail: "Choose a PNG, JPEG, GIF or WebP image.")
            }
            ws.directory.companies[index].avatarURL = picture.url
            ws.directory.companies[index].avatarSource = "upload"
            return .some(companyChanged(index, in: &ws))
        }
        if let id = call.match("DELETE", "companies/*/avatar")?.first {
            let index = try companyIndex(id, in: ws)
            ws.directory.companies[index].avatarURL = nil
            ws.directory.companies[index].avatarSource = nil
            return .some(companyChanged(index, in: &ws))
        }
        if let id = call.match("GET", "companies/*")?.first {
            return .some(encode(live(ws.directory.companies[try companyIndex(id, in: ws)], in: ws)))
        }
        if let id = call.match("PATCH", "companies/*")?.first {
            let index = try companyIndex(id, in: ws)
            var company = ws.directory.companies[index]
            try apply(call.body.object ?? [:], to: &company, in: &ws, creating: false)
            ws.directory.companies[index] = company
            return .some(companyChanged(index, in: &ws))
        }
        if let id = call.match("DELETE", "companies/*")?.first {
            let index = try companyIndex(id, in: ws)
            ws.directory.companies.remove(at: index)
            for contact in ws.directory.contacts.indices where ws.directory.contacts[contact].company?.id == id {
                ws.directory.contacts[contact].company = nil
            }
            publish("company.deleted", ["company_id": .string(id)], in: &ws)
            return .some(nil)
        }
        return nil
    }

    // MARK: Lookups

    private func contactIndex(_ id: String, in ws: DemoWorkspace) throws -> Int {
        guard let index = ws.directory.contacts.firstIndex(where: { $0.id == id }) else { throw notFound("contact") }
        return index
    }

    private func companyIndex(_ id: String, in ws: DemoWorkspace) throws -> Int {
        guard let index = ws.directory.companies.firstIndex(where: { $0.id == id }) else { throw notFound("company") }
        return index
    }

    /// A contact with its live ticket count.
    private func live(_ contact: Contact, in ws: DemoWorkspace) -> Contact {
        var contact = contact
        contact.ticketCount = ws.tickets.filter { $0.contact?.id == contact.id }.count
        return contact
    }

    /// A company with its live people count.
    private func live(_ company: Company, in ws: DemoWorkspace) -> Company {
        var company = company
        company.contactCount = ws.directory.contacts.filter { $0.company?.id == company.id }.count
        return company
    }

    private func page<T: Encodable>(_ items: [T], _ query: [String: String]) -> Data {
        let offset = Int(query["cursor"] ?? "") ?? 0
        let limit = min(200, max(1, Int(query["limit"] ?? "") ?? 50))
        let slice = Array(items.dropFirst(offset).prefix(limit))
        let next = offset + slice.count < items.count ? String(offset + slice.count) : nil
        return encode(ListBody(data: slice, nextCursor: next))
    }

    // MARK: Writes

    private func contactChanged(_ index: Int, in ws: inout DemoWorkspace) -> Data {
        ws.directory.contacts[index].updatedAt = now()
        let contact = ws.directory.contacts[index]
        // Tickets show the contact as it is now.
        for ticket in ws.tickets.indices where ws.tickets[ticket].contact?.id == contact.id {
            ws.tickets[ticket].contact = contact.ref
        }
        let answer = live(contact, in: ws)
        publish("contact.updated", ["contact": DemoSeed.json(answer)], in: &ws)
        return encode(answer)
    }

    private func companyChanged(_ index: Int, in ws: inout DemoWorkspace) -> Data {
        ws.directory.companies[index].updatedAt = now()
        let company = ws.directory.companies[index]
        for contact in ws.directory.contacts.indices where ws.directory.contacts[contact].company?.id == company.id {
            ws.directory.contacts[contact].company = company.ref
        }
        let answer = live(company, in: ws)
        publish("company.updated", ["company": DemoSeed.json(answer)], in: &ws)
        return encode(answer)
    }

    private func invalid(_ field: String, _ detail: String) -> Failure {
        Failure(status: 422, code: "invalid_\(field)", detail: detail)
    }

    private func apply(_ body: [String: JSONValue], to contact: inout Contact, in ws: inout DemoWorkspace, creating: Bool) throws {
        if let name = body["name"]?.string { contact.name = name.trimmingCharacters(in: .whitespacesAndNewlines) }
        if let value = body["email"] {
            let email = value.string?.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() ?? ""
            if email.isEmpty {
                contact.email = nil
            } else {
                guard email.range(of: #"^[^\s@]+@[^\s@]+\.[^\s@]+$"#, options: .regularExpression) != nil else {
                    throw invalid("email", "That email doesn't look right.")
                }
                if ws.directory.contacts.contains(where: { $0.id != contact.id && $0.email?.lowercased() == email }) {
                    throw Failure(status: 409, code: "contact_exists", detail: "A contact with this email already exists.")
                }
                contact.email = email
            }
        }
        guard !contact.name.isEmpty || contact.email != nil else { throw invalid("contact", "A contact needs a name or an email.") }
        if let phone = body["phone"]?.string { contact.phone = phone.trimmingCharacters(in: .whitespacesAndNewlines) }
        if let notes = body["notes"]?.string { contact.notes = notes }
        if let value = body["external_id"] {
            let external = value.string?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            contact.externalID = external.isEmpty ? nil : external
        }
        if let blocked = body["blocked"]?.bool { contact.blocked = blocked }
        if let value = body["company_id"] {
            if let id = value.string, !id.isEmpty {
                guard let company = ws.directory.companies.first(where: { $0.id == id }) else { throw invalid("company", "That company isn't in this workspace.") }
                contact.company = company.ref
            } else {
                contact.company = nil
            }
        } else if creating, let domain = DemoDirectory.domain(of: contact.email),
                  let company = ws.directory.companies.first(where: { $0.domain == domain }) {
            // A new contact joins the company of its email domain.
            contact.company = company.ref
        }
        contact.tags = labels(body, current: contact.tags, scope: "contacts", in: &ws)
        if let fields = body["fields"]?.object {
            for (key, value) in fields {
                if value.isNull { contact.fields[key] = nil } else { contact.fields[key] = value }
            }
        }
    }

    private func apply(_ body: [String: JSONValue], to company: inout Company, in ws: inout DemoWorkspace, creating: Bool) throws {
        if let name = body["name"]?.string { company.name = name.trimmingCharacters(in: .whitespacesAndNewlines) }
        guard !company.name.isEmpty else { throw invalid("name", "A company needs a name.") }
        if let domain = body["domain"]?.string {
            let clean = domain.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
            if !clean.isEmpty {
                guard clean.contains("."), !clean.contains(" "), !clean.contains("/") else { throw invalid("domain", "Enter a domain like acme.com.") }
                if ws.directory.companies.contains(where: { $0.id != company.id && $0.domain == clean }) {
                    throw Failure(status: 409, code: "company_exists", detail: "Another company already has this domain.")
                }
            }
            company.domain = clean
        }
        if let notes = body["notes"]?.string { company.notes = notes }
        company.tags = labels(body, current: company.tags, scope: "companies", in: &ws)
        if let fields = body["fields"]?.object {
            for (key, value) in fields {
                if value.isNull { company.fields[key] = nil } else { company.fields[key] = value }
            }
        }
    }

    /// `tags` replaces, `add_tags` and `remove_tags` adjust; a name nobody has made yet becomes a label.
    private func labels(_ body: [String: JSONValue], current: [TagRef], scope: String, in ws: inout DemoWorkspace) -> [TagRef] {
        var names = current.map(\.name)
        if let replace = body["tags"]?.array?.compactMap(\.string) { names = replace }
        for name in body["add_tags"]?.array?.compactMap(\.string) ?? [] where !names.contains(where: { $0.caseInsensitiveCompare(name) == .orderedSame }) {
            names.append(name)
        }
        let removed = body["remove_tags"]?.array?.compactMap(\.string) ?? []
        names.removeAll { name in removed.contains { $0.caseInsensitiveCompare(name) == .orderedSame } }

        var refs: [TagRef] = []
        let palette = ["blue", "green", "violet", "amber", "teal", "pink", "orange", "indigo"]
        for raw in names {
            let name = raw.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !name.isEmpty, !refs.contains(where: { $0.name.caseInsensitiveCompare(name) == .orderedSame }) else { continue }
            if let tag = ws.directory.tags.first(where: { $0.name.caseInsensitiveCompare(name) == .orderedSame && $0.appliesTo?.contains(scope) == true }) {
                refs.append(tag.ref)
            } else {
                let tag = Tag(id: made(), name: name, color: palette[ws.directory.tags.count % palette.count], description: nil, groupID: nil, archived: false, appliesTo: [scope])
                ws.directory.tags.append(tag)
                refs.append(tag.ref)
            }
        }
        return refs
    }
}
#endif
