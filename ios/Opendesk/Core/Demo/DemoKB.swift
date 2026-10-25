#if DEBUG
import Foundation

// The knowledge base and reports of the demo. Margin (a writing and notes app)
// has five collections and twenty articles, published and drafts; Lumen Labs a
// small one. Collection ids are `0197a0c0-0020-7000-8000-00000000000<n>`,
// article ids `0197a0c0-0021-7000-8000-0000000000<nn>`:
//
//   01 Set up your workspace          05 Set up SAML single sign-on      10 Change your plan
//   06 Fix an SSO login loop           12 Refunds and duplicate charges   14 Plan limits (draft, a table: HTML editing)
//   15 Export to PDF and Markdown      17 The iPhone app closes on launch 18 Create an API token
//   19 Rate limits and 429 errors      20 Verify webhook signatures (draft)  21 What's new in June (draft, no collection)
//
// Reports are a believable daily series (weekday rhythm, slow growth, noise
// fixed per day) shaped by the team filter, with open work, channels and
// severities taken from the workspace's own tickets.

func demoKBID(_ kind: Int, _ n: Int) -> String {
    String(format: "0197a0c0-%04x-7000-8000-%012d", kind, n)
}

struct DemoKB: Sendable {
    var collections: [KBCollection] = []
    var articles: [KBArticle] = []
    var helpCenter: String?

    static func collectionID(_ n: Int) -> String { demoKBID(0x20, n) }
    static func articleID(_ n: Int) -> String { demoKBID(0x21, n) }

    static func seed(_ ws: DemoWorkspace) -> DemoKB {
        ws.id == DemoSeed.marginID ? margin() : ws.id == DemoSeed.lumenID ? lumen() : DemoKB()
    }

    private struct Spec {
        var n: Int
        var collection: Int?
        var title: String
        var excerpt: String
        var body: String
        var status: KBStatus = .published
        var author: DemoPerson = .maya
        var views = 0
        var updated: Double
        var html: String?
    }

    private static func article(_ spec: Spec) -> KBArticle {
        let updated = DemoSeed.ago(spec.updated * 1440)
        return KBArticle(
            id: articleID(spec.n), collectionID: spec.collection.map(collectionID), title: spec.title, slug: KBSlug.make(spec.title),
            excerpt: spec.excerpt, bodyHTML: spec.html ?? KBMarkdown.html(from: spec.body), status: spec.status, author: spec.author.ref,
            views: spec.status == .published ? spec.views : 0, publishedAt: spec.status == .published ? updated.addingTimeInterval(-3 * 3600) : nil, updatedAt: updated
        )
    }

    // MARK: Margin

    static func margin() -> DemoKB {
        var kb = DemoKB()
        kb.helpCenter = "https://help.margin.app"
        kb.collections = [
            KBCollection(id: collectionID(1), name: "Getting started", slug: "getting-started", description: "Your first week with Margin.", position: 0, articleCount: 0),
            KBCollection(id: collectionID(2), name: "Account and SSO", slug: "account-and-sso", description: "Signing in, single sign-on and security.", position: 1, articleCount: 0),
            KBCollection(id: collectionID(3), name: "Billing and plans", slug: "billing-and-plans", description: "Plans, invoices, refunds and tax.", position: 2, articleCount: 0),
            KBCollection(id: collectionID(4), name: "Exports and apps", slug: "exports-and-apps", description: "Exports, offline sync and the mobile apps.", position: 3, articleCount: 0),
            KBCollection(id: collectionID(5), name: "API and integrations", slug: "api-and-integrations", description: "Tokens, limits and webhooks for developers.", position: 4, articleCount: 0),
        ]
        let specs: [Spec] = [
            Spec(n: 1, collection: 1, title: "Set up your workspace", excerpt: "Name your workspace, pick a home page and create your first notebooks.", body: """
            A workspace holds your team's notebooks, members and settings. Most teams set one up in about ten minutes.

            ## Name and address

            Open **Settings**, then **Workspace**, and give it the name your team knows it by. The address (`yourteam.margin.app`) can be changed once a month.

            ## Create your first notebooks

            1. Click **New notebook** in the sidebar.
            2. Pick a template or start blank.
            3. Drag notebooks to put the ones you use most at the top.

            > Tip: start with three notebooks at most. It is easier to split a notebook later than to merge two.

            ## Next steps

            - [Invite your team](https://help.margin.app/a/invite-your-team)
            - [Import notes from Notion and Evernote](https://help.margin.app/a/import-notes-from-notion-and-evernote)
            """, author: .maya, views: 4210, updated: 21),
            Spec(n: 2, collection: 1, title: "Invite your team", excerpt: "Send invites by email or share a join link limited to your domain.", body: """
            Members see every shared notebook in the workspace. Guests only see the notebooks you share with them.

            ## Invite by email

            1. Open **Settings**, then **Members**.
            2. Click **Invite** and paste one or more addresses.
            3. Choose a role: *Member*, *Admin* or *Guest*.

            Invites expire after seven days. You can resend one from the same page.

            ## Share a join link

            Turn on **Join link** to let anyone with an address on your domain join as a member. Turn it off again at any time; people who already joined stay.
            """, author: .jonas, views: 2875, updated: 34),
            Spec(n: 3, collection: 1, title: "Import notes from Notion and Evernote", excerpt: "Bring pages, tags and attachments over in one go.", body: """
            Margin imports Notion exports (Markdown and CSV) and Evernote `.enex` files. Pages keep their headings, lists, links and attachments.

            ## From Notion

            1. In Notion, open **Settings**, then **Export all workspace content**, and pick *Markdown and CSV*.
            2. In Margin, open **Import** and drop the zip file.

            ## From Evernote

            1. Export each notebook as an `.enex` file.
            2. Drop the files on the **Import** page. Tags become labels.

            Large imports run in the background. We email you when they finish.
            """, author: .priya, views: 1932, updated: 12),
            Spec(n: 4, collection: 1, title: "Keyboard shortcuts", excerpt: "Move around, format and search without leaving the keyboard.", body: """
            Press `?` anywhere to see every shortcut. These are the ones people use most:

            - `Cmd K` opens search
            - `Cmd N` creates a note in the current notebook
            - `Cmd Shift L` toggles the sidebar
            - `Cmd /` turns the line into a heading, list or quote

            On Windows and Linux, use `Ctrl` instead of `Cmd`.
            """, author: .sam, views: 1544, updated: 48),
            Spec(n: 5, collection: 2, title: "Set up SAML single sign-on", excerpt: "Connect Okta, Azure AD or Google Workspace so your team signs in with one account.", body: """
            Single sign-on is available on the Business plan. You need to be a workspace owner and an admin in your identity provider.

            ## In your identity provider

            1. Create a new SAML app named *Margin*.
            2. Set the ACS URL to `https://margin.app/sso/acs` and the entity ID to `https://margin.app`.
            3. Map the `email` attribute to the user's primary address.

            ## In Margin

            1. Open **Settings**, then **Security**, then **Single sign-on**.
            2. Paste the metadata URL from your provider and click **Test**.
            3. When the test passes, click **Turn on**.

            > People who already have a password can still use it until you enforce SSO.
            """, author: .jonas, views: 3388, updated: 6),
            Spec(n: 6, collection: 2, title: "Fix an SSO login loop", excerpt: "When sign-in keeps returning to the login page, check the clock, the ACS URL and cookies.", body: """
            A loop means your identity provider signed you in, but Margin refused the answer and started over. Three causes cover almost every case.

            ## 1. The ACS URL does not match

            The ACS URL in your provider must be exactly `https://margin.app/sso/acs`, without a trailing slash.

            ## 2. The certificate was rotated

            If your provider rotated its signing certificate, paste the new metadata URL under **Settings**, then **Security**.

            ## 3. Third-party cookies are blocked

            Safari's *Prevent cross-site tracking* can drop the session during the redirect. Allow cookies for `margin.app` and try again.

            Still stuck? Reply to your ticket with the time you tried, and we will look at the sign-in log for you.
            """, author: .maya, views: 2140, updated: 2),
            Spec(n: 7, collection: 2, title: "Two-factor codes not arriving", excerpt: "Codes by text can be slow; an authenticator app always works.", body: """
            Text messages can take a few minutes, and some carriers drop them. An authenticator app never waits on a network.

            ## Switch to an authenticator app

            1. Open **Settings**, then **Account**, then **Two-factor**.
            2. Choose **Authenticator app** and scan the code with 1Password, Authy or Google Authenticator.

            ## Lost your phone?

            Use one of your recovery codes. Each code works once. If you have none left, ask a workspace owner to reset two-factor for you.
            """, author: .priya, views: 1270, updated: 9),
            Spec(n: 8, collection: 2, title: "Enforce SSO for your domain", excerpt: "Require everyone on your domain to sign in through your identity provider.", body: """
            Once SSO works, you can require it for every address on your verified domain.

            1. Verify your domain under **Settings**, then **Domains**.
            2. Turn on **Require single sign-on**.

            Owners keep a password as a way back in if your provider is down.
            """, status: .draft, author: .jonas, updated: 1),
            Spec(n: 9, collection: 2, title: "Delete your account", excerpt: "What is removed, what stays with your workspace, and how to start.", body: """
            Deleting your account removes your profile, your private notebooks and your sign-in methods. Shared notebooks stay with the workspace.

            Open **Settings**, then **Account**, and click **Delete account**. We ask for your password once more.

            If you own a workspace, transfer it to someone else first.
            """, author: .lea, views: 688, updated: 63),
            Spec(n: 10, collection: 3, title: "Change your plan", excerpt: "Upgrade, downgrade or switch between monthly and annual billing.", body: """
            Owners and billing admins can change the plan under **Settings**, then **Billing**.

            - **Upgrading** takes effect right away. You pay the difference for the rest of the period.
            - **Downgrading** takes effect at the end of the period. Nothing is deleted; notebooks over the limit become read only.
            - **Switching to annual** saves two months.
            """, author: .lea, views: 2604, updated: 15),
            Spec(n: 11, collection: 3, title: "Download invoices and receipts", excerpt: "Every invoice is a PDF under Settings, then Billing.", body: """
            Every charge has an invoice. Open **Settings**, then **Billing**, then **Invoices**, and click **Download** next to the one you need.

            Need invoices sent to your accounts team? Add their address under **Billing email** and we will copy them on every invoice.
            """, author: .lea, views: 1987, updated: 27),
            Spec(n: 12, collection: 3, title: "Refunds and duplicate charges", excerpt: "Duplicate charges are refunded automatically within two business days.", body: """
            If a payment retry charged you twice, we refund the duplicate automatically, usually within two business days.

            ## How long a refund takes

            Your bank shows the refund **5 to 10 business days** after we send it. The refund reference (for example `RF-2026-06-0193`) is on the invoice page.

            ## Asking for a refund

            Annual plans can be refunded in full within 14 days of the charge. Write to us from the address on the account and include the invoice number.
            """, author: .maya, views: 1451, updated: 4),
            Spec(n: 13, collection: 3, title: "Add a VAT ID", excerpt: "Add your VAT ID to get reverse-charge invoices in the EU.", body: """
            Businesses in the EU can add a VAT ID so invoices are reverse charged.

            1. Open **Settings**, then **Billing**, then **Tax details**.
            2. Enter the VAT ID with its country prefix, like `DE123456789`.

            We check the ID with VIES. Past invoices are not changed; the next one uses the new details.
            """, author: .lea, views: 942, updated: 40),
            Spec(n: 14, collection: 3, title: "Plan limits", excerpt: "Notebooks, members, storage and history on each plan.", body: "", status: .draft, author: .lea, updated: 0.3, html: """
            <p>Each plan differs only in limits. Every feature is on every plan.</p><table><thead><tr><th>Plan</th><th>Members</th><th>Storage</th><th>History</th></tr></thead><tbody><tr><td>Free</td><td>3</td><td>2 GB</td><td>30 days</td></tr><tr><td>Team</td><td>25</td><td>100 GB</td><td>1 year</td></tr><tr><td>Business</td><td>Unlimited</td><td>1 TB</td><td>Unlimited</td></tr></tbody></table><p>Need more? <a href="mailto:sales@margin.app">Talk to sales</a>.</p>
            """),
            Spec(n: 15, collection: 4, title: "Export to PDF and Markdown", excerpt: "Export one note, a notebook or the whole workspace.", body: """
            You can export a single note, a whole notebook or everything in the workspace.

            ## One note

            Open the note, click **Share**, then **Export**, and pick *PDF* or *Markdown*.

            ## A notebook or the workspace

            Owners can export everything from **Settings**, then **Data**. Big exports are prepared in the background and emailed as a download link that works for 24 hours.

            ```text
            margin-export/
              notebooks/
                product/
                  roadmap.md
              attachments/
            ```

            PDF exports keep fonts and images. Markdown exports put attachments in their own folder.
            """, author: .sam, views: 2318, updated: 8),
            Spec(n: 16, collection: 4, title: "How offline sync works", excerpt: "Edit without a connection; changes merge when you are back online.", body: """
            The desktop and mobile apps keep a copy of the notebooks you opened recently, so you can keep writing without a connection.

            When you are back online, Margin merges your changes with everyone else's, paragraph by paragraph. If two people changed the same paragraph, both versions are kept and marked for you to pick one.

            > Notebooks you never opened on a device are not available offline. Pin a notebook to keep it on the device.
            """, author: .priya, views: 1688, updated: 19),
            Spec(n: 17, collection: 4, title: "The iPhone app closes on launch", excerpt: "Update to 4.12.1, then sign out and in if it still closes.", body: """
            Version 4.12.0 could close right after launch on iOS 26 when a notebook had a very large attachment. Version **4.12.1** fixes it.

            1. Update Margin from the App Store.
            2. If it still closes, hold the app icon, choose **Remove app**, and install it again.
            3. Sign in. Your notes come back from the cloud.

            Notes you wrote offline and never synced are kept on the device until you sign in again.
            """, author: .sam, views: 976, updated: 3),
            Spec(n: 18, collection: 5, title: "Create an API token", excerpt: "Personal tokens for scripts, scoped to what they need.", body: """
            Tokens let scripts and integrations use the Margin API as you.

            1. Open **Settings**, then **Developer**, then **Tokens**.
            2. Click **New token**, name it, and choose its scopes.
            3. Copy the token. It is shown once.

            Send it in the `Authorization` header:

            ```bash
            curl https://api.margin.app/v1/notes \\
              -H "Authorization: Bearer mgn_live_..."
            ```

            Give each integration its own token, so you can revoke one without breaking the others.
            """, author: .priya, views: 1402, updated: 11),
            Spec(n: 19, collection: 5, title: "Rate limits and 429 errors", excerpt: "600 requests a minute per token; back off when you get a 429.", body: """
            Each token can make **600 requests a minute**. Over that, the API answers `429 Too Many Requests` with a `Retry-After` header.

            ## Handling a 429

            - Wait for the number of seconds in `Retry-After`, then retry.
            - Spread bulk jobs out instead of sending them all at once.
            - Use the list endpoints' `limit` (up to 200) to fetch more per request.

            Need a higher limit for a migration? Ask us and tell us for how long.
            """, author: .priya, views: 1133, updated: 5),
            Spec(n: 20, collection: 5, title: "Verify webhook signatures", excerpt: "Check the Margin-Signature header before trusting a webhook.", body: """
            Every webhook carries a `Margin-Signature` header: a timestamp and an HMAC-SHA256 of the body, signed with your endpoint's secret.

            1. Read `t` and `v1` from the header.
            2. Compute the HMAC of `t.body` with your secret.
            3. Compare it with `v1` in constant time, and refuse anything older than five minutes.

            ```js
            const expected = hmac("sha256", secret, `${t}.${body}`)
            if (!timingSafeEqual(expected, v1)) return reject()
            ```
            """, status: .draft, author: .jonas, updated: 0.8),
            Spec(n: 21, collection: nil, title: "What's new in June", excerpt: "Faster search, nested labels and a new export page.", body: """
            ## Faster search

            Search now finds words inside images and PDFs, and answers in under 200 ms for most workspaces.

            ## Nested labels

            Put labels inside labels, like *Clients* then *Acme*. Searching a parent label includes its children.

            ## A new export page

            Exports moved to **Settings**, then **Data**, with progress you can follow.
            """, status: .draft, author: .maya, updated: 0.1),
        ]
        kb.articles = specs.map(article).sorted { $0.updatedAt > $1.updatedAt }
        return kb
    }

    // MARK: Lumen

    static func lumen() -> DemoKB {
        var kb = DemoKB()
        kb.collections = [KBCollection(id: collectionID(101), name: "Basics", slug: "basics", description: "Start here.", position: 0, articleCount: 0)]
        let ruth = UserRef(id: demoID(.user, 20), name: "Ruth Adler", email: "ruth@lumenlabs.io", avatarURL: nil)
        kb.articles = [
            KBArticle(id: articleID(101), collectionID: collectionID(101), title: "Export a dashboard", slug: "export-a-dashboard", excerpt: "Download any dashboard as CSV or PNG.",
                      bodyHTML: KBMarkdown.html(from: "Open the dashboard, click **Export** and pick *CSV* or *PNG*.\n\nLarge ranges are emailed when ready."),
                      status: .published, author: ruth, views: 312, publishedAt: DemoSeed.ago(9000), updatedAt: DemoSeed.ago(9000)),
            KBArticle(id: articleID(102), collectionID: collectionID(101), title: "Roles and permissions", slug: "roles-and-permissions", excerpt: "What owners, admins and agents can do.",
                      bodyHTML: KBMarkdown.html(from: "- **Owners** manage billing and roles\n- **Admins** change settings\n- **Agents** work tickets"),
                      status: .draft, author: ruth, views: 0, publishedAt: nil, updatedAt: DemoSeed.ago(3000)),
        ]
        return kb
    }

    func withCounts(_ collection: KBCollection) -> KBCollection {
        var copy = collection
        copy.articleCount = articles.filter { $0.collectionID == collection.id }.count
        return copy
    }
}

// MARK: - Routes

extension DemoBackend {
    func kbRoutes(_ call: Call, in ws: inout DemoWorkspace) throws -> Data?? {
        if call.match("GET", "brands") != nil { return .some(brands(ws)) }
        if call.match("GET", "reports/overview") != nil { return .some(encode(reportOverview(call.query, in: ws))) }
        if call.path.first == "kb" { return try kbCall(call, in: &ws) }
        return nil
    }

    private func brands(_ ws: DemoWorkspace) -> Data {
        let url = ws.kb.helpCenter ?? "https://\(ws.workspace.slug ?? "help").opendesk.help"
        let brand: JSONValue = .object([
            "id": .string(demoKBID(0x22, ws.id == DemoSeed.marginID ? 1 : 2)),
            "name": .string(ws.workspace.name),
            "is_default": .bool(true),
            "help_center": .object([
                "enabled": .bool(ws.kb.helpCenter != nil),
                "title": .string("\(ws.workspace.name) help center"),
                "url": .string(url),
                "default_url": .string(url),
            ]),
        ])
        return encode(ListBody(data: [brand]))
    }

    private func kbCall(_ call: Call, in ws: inout DemoWorkspace) throws -> Data?? {
        // Collections
        if call.match("GET", "kb/collections") != nil {
            return .some(encode(ListBody(data: ws.kb.collections.sorted { $0.position < $1.position }.map(ws.kb.withCounts))))
        }
        if call.match("POST", "kb/collections") != nil {
            let name = (call.string("name") ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
            guard !name.isEmpty else { throw Failure(status: 422, code: "invalid", detail: "Give the collection a name.") }
            let collection = KBCollection(id: made(), name: name, slug: KBSlug.make(name), description: call.string("description") ?? "", position: (ws.kb.collections.map(\.position).max() ?? -1) + 1, articleCount: 0)
            ws.kb.collections.append(collection)
            publish("kb_collection.created", ["collection": DemoSeed.json(collection)], in: &ws)
            return .some(encode(collection))
        }
        if let id = call.match("PATCH", "kb/collections/*")?.first {
            guard let index = ws.kb.collections.firstIndex(where: { $0.id == id }) else { throw notFound("collection") }
            if let name = call.string("name")?.trimmingCharacters(in: .whitespacesAndNewlines), !name.isEmpty { ws.kb.collections[index].name = name }
            if let description = call.string("description") { ws.kb.collections[index].description = description }
            let collection = ws.kb.withCounts(ws.kb.collections[index])
            publish("kb_collection.updated", ["collection": DemoSeed.json(collection)], in: &ws)
            return .some(encode(collection))
        }
        if let id = call.match("DELETE", "kb/collections/*")?.first {
            guard ws.kb.collections.contains(where: { $0.id == id }) else { throw notFound("collection") }
            ws.kb.collections.removeAll { $0.id == id }
            for index in ws.kb.articles.indices where ws.kb.articles[index].collectionID == id { ws.kb.articles[index].collectionID = nil }
            publish("kb_collection.deleted", ["collection_id": .string(id)], in: &ws)
            return .some(nil)
        }

        // Ollie's draft
        if call.match("POST", "kb/articles/ai/draft") != nil {
            return .some(encode(draft(call, in: ws)))
        }

        // Articles
        if call.match("GET", "kb/articles") != nil {
            var list = ws.kb.articles
            if let collection = call.query["collection_id"], !collection.isEmpty { list = list.filter { $0.collectionID == collection } }
            if let status = call.query["status"], !status.isEmpty { list = list.filter { $0.status.rawValue == status } }
            if let q = call.query["q"]?.trimmingCharacters(in: .whitespaces), !q.isEmpty {
                list = list.filter { $0.title.localizedCaseInsensitiveContains(q) || $0.excerpt.localizedCaseInsensitiveContains(q) || HTMLText.plain(from: $0.bodyHTML).localizedCaseInsensitiveContains(q) }
            }
            list.sort { $0.updatedAt > $1.updatedAt }
            let limit = Int(call.query["limit"] ?? "") ?? 50
            return .some(encode(ListBody(data: Array(list.prefix(max(1, min(limit, 200)))))))
        }
        if let key = call.match("GET", "kb/articles/*")?.first {
            guard let article = ws.kb.articles.first(where: { $0.id == key || $0.slug == key }) else { throw notFound("article") }
            return .some(encode(article))
        }
        if call.match("POST", "kb/articles") != nil {
            let title = (call.string("title") ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
            guard !title.isEmpty else { throw Failure(status: 422, code: "invalid", detail: "Give the article a title.") }
            let status = KBStatus(rawValue: call.string("status") ?? "") ?? .draft
            let collection = call.string("collection_id").flatMap { $0.isEmpty ? nil : $0 }
            if let collection, !ws.kb.collections.contains(where: { $0.id == collection }) { throw notFound("collection") }
            let at = now()
            let article = KBArticle(
                id: made(), collectionID: collection, title: title, slug: uniqueSlug(call.string("slug") ?? title, except: nil, in: ws),
                excerpt: call.string("excerpt") ?? "", bodyHTML: call.string("body_html") ?? "", status: status, author: me,
                views: 0, publishedAt: status == .published ? at : nil, updatedAt: at
            )
            ws.kb.articles.insert(article, at: 0)
            publish("kb_article.created", ["article": DemoSeed.json(article)], in: &ws)
            return .some(encode(article))
        }
        if let id = call.match("PATCH", "kb/articles/*")?.first {
            guard let index = ws.kb.articles.firstIndex(where: { $0.id == id }) else { throw notFound("article") }
            var article = ws.kb.articles[index]
            if let title = call.string("title")?.trimmingCharacters(in: .whitespacesAndNewlines), !title.isEmpty { article.title = title }
            if let slug = call.string("slug"), !slug.isEmpty { article.slug = uniqueSlug(slug, except: id, in: ws) }
            if let excerpt = call.string("excerpt") { article.excerpt = excerpt }
            if let body = call.string("body_html") { article.bodyHTML = body }
            if let collection = call.string("collection_id") {
                if collection.isEmpty {
                    article.collectionID = nil
                } else {
                    guard ws.kb.collections.contains(where: { $0.id == collection }) else { throw notFound("collection") }
                    article.collectionID = collection
                }
            }
            if let raw = call.string("status"), let status = KBStatus(rawValue: raw), status != article.status {
                article.status = status
                article.publishedAt = status == .published ? now() : nil
            }
            article.updatedAt = now()
            ws.kb.articles[index] = article
            publish("kb_article.updated", ["article": DemoSeed.json(article)], in: &ws)
            return .some(encode(article))
        }
        if let id = call.match("DELETE", "kb/articles/*")?.first {
            guard ws.kb.articles.contains(where: { $0.id == id }) else { throw notFound("article") }
            ws.kb.articles.removeAll { $0.id == id }
            publish("kb_article.deleted", ["article_id": .string(id)], in: &ws)
            return .some(nil)
        }
        return nil
    }

    private func uniqueSlug(_ text: String, except id: String?, in ws: DemoWorkspace) -> String {
        let base = KBSlug.make(text).isEmpty ? "article" : KBSlug.make(text)
        var slug = base
        var n = 2
        while ws.kb.articles.contains(where: { $0.slug == slug && $0.id != id }) {
            slug = "\(base)-\(n)"
            n += 1
        }
        return slug
    }

    /// Ollie's article from the tickets' subjects and the topic, in the knowledge base's own HTML.
    private func draft(_ call: Call, in ws: DemoWorkspace) -> KBArticleDraft {
        let ids = call.body["ticket_ids"]?.array?.compactMap(\.string) ?? []
        let tickets = ids.compactMap { id in ws.tickets.first { $0.id == id } }
        let topic = (call.string("topic") ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        let subject = topic.isEmpty ? (tickets.first?.subject ?? "A common question") : topic
        let title = subject.prefix(1).uppercased() + subject.dropFirst()
        let lower = subject.lowercased()
        let steps: [String]
        if lower.contains("sso") || lower.contains("login") || lower.contains("sign") {
            steps = ["Open **Settings**, then **Security**, then **Single sign-on**.", "Check that the ACS URL is exactly `https://margin.app/sso/acs`.", "Paste the latest metadata URL from your identity provider and click **Test**."]
        } else if lower.contains("refund") || lower.contains("charge") || lower.contains("invoice") || lower.contains("vat") {
            steps = ["Open **Settings**, then **Billing**, then **Invoices**.", "Find the charge and check its status.", "Duplicate charges are refunded within two business days; banks show them 5 to 10 business days later."]
        } else if lower.contains("export") || lower.contains("pdf") {
            steps = ["Open the note or notebook and click **Share**, then **Export**.", "Pick *PDF* or *Markdown*.", "For the whole workspace, use **Settings**, then **Data**; we email a link when it is ready."]
        } else {
            steps = ["Open **Settings** and find the section the question is about.", "Make the change and save it.", "If it still does not work, reply to your ticket with what you tried."]
        }
        var markdown = "This article answers a question customers ask often"
        markdown += tickets.isEmpty ? "." : ", drawn from \(tickets.count) recent \(tickets.count == 1 ? "conversation" : "conversations")."
        markdown += "\n\n## What to do\n\n" + steps.enumerated().map { "\($0.offset + 1). \($0.element)" }.joined(separator: "\n")
        if !tickets.isEmpty {
            markdown += "\n\n## What customers mention\n\n" + tickets.prefix(4).map { "- " + KBDemoText.escape($0.subject) }.joined(separator: "\n")
        }
        markdown += "\n\n> Still stuck? Write to us and include the time it happened, so we can check the logs."
        return KBArticleDraft(title: String(title), bodyHTML: KBMarkdown.html(from: markdown), excerpt: "What to do when customers ask about \(KBDemoText.sentenceTail(subject)), step by step.")
    }

    // MARK: Reports

    func reportOverview(_ query: [String: String], in ws: DemoWorkspace) -> ReportOverview {
        let calendar = Calendar.current
        let today = calendar.startOfDay(for: DemoMode.anchor)
        let to = query["to"].flatMap(ReportRange.parse) ?? today
        let from = query["from"].flatMap(ReportRange.parse) ?? calendar.date(byAdding: .day, value: -29, to: to) ?? to
        let team = query["team_id"].flatMap { $0.isEmpty ? nil : $0 }
        let scale: Double = ws.id == DemoSeed.marginID ? 24 : 5
        let teamShare = DemoReports.share(team, teams: ws.teams)
        let periodKey = (query["from"] ?? "") + (query["to"] ?? "") + (team ?? "all")

        var days: [ReportDay] = []
        var day = calendar.startOfDay(for: min(from, to))
        let end = calendar.startOfDay(for: max(from, to))
        while day <= end, days.count < 400 {
            let key = ReportRange.key(day)
            var created = 0
            var solved = 0
            if day <= today {
                let age = Double(calendar.dateComponents([.day], from: day, to: today).day ?? 0)
                let weekday = [0.6, 1.14, 1.08, 1.04, 1.0, 0.95, 0.56][(calendar.component(.weekday, from: day) - 1) % 7]
                let growth = 1 / (1 + age * 0.0028)
                let partial = day == today ? 0.42 : 1
                // Day to day spread grows like the square root of the count, as real small queues do.
                let expected = scale * teamShare * weekday * growth * partial
                let spread = 0.62 * expected.squareRoot()
                let value = max(0, expected + spread * (2 * DemoReports.unit(key + (team ?? "")) - 1))
                created = Int(value.rounded())
                let rate = 0.86 + 0.18 * DemoReports.unit("rate" + key + (team ?? ""))
                let lag = spread * 0.8 * (2 * DemoReports.unit("solved" + key + (team ?? "")) - 1)
                solved = max(0, Int((value * rate + lag).rounded()))
            }
            days.append(ReportDay(date: key, created: created, solved: solved))
            guard let next = calendar.date(byAdding: .day, value: 1, to: day) else { break }
            day = next
        }
        let created = days.reduce(0) { $0 + $1.created }
        let solved = days.reduce(0) { $0 + $1.solved }

        let live = ws.tickets.filter { !$0.spam }
        let scoped = live.filter { ticket in
            switch team {
            case nil: true
            case "none"?: ticket.teamID == nil
            case let id?: ticket.teamID == id
            }
        }
        let openNow = scoped.filter { [.open, .pending, .onHold].contains($0.status) }.count

        let channelDefaults: [String: Double] = ["email": 0.6, "chat": 0.29, "api": 0.07, "portal": 0.04]
        let byChannel = DemoReports.split(created, DemoReports.blend(channelDefaults, observed: scoped.map(\.channel.rawValue)))
        let severityDefaults: [String: Double] = ["sev1": 0.02, "sev2": 0.07, "sev3": 0.2, "sev4": 0.17, "none": 0.54]
        let bySeverity = DemoReports.split(created, DemoReports.blend(severityDefaults, observed: scoped.map { $0.severity?.rawValue ?? "none" }))

        let wobble = 0.88 + 0.24 * DemoReports.unit(periodKey)
        let firstResponseBase: Double = switch team {
        case nil: 41
        case "none"?: 76
        default: [34.0, 52, 138][min(2, ws.teams.firstIndex { $0.id == team } ?? 0)]
        }
        let firstResponse = created > 0 ? (firstResponseBase * wobble).rounded() : nil
        let resolution = created > 0 ? (612 * (0.85 + 0.3 * DemoReports.unit("res" + periodKey))).rounded() : nil
        let csatCount = Int((Double(solved) * 0.19).rounded())
        let observedCSAT = scoped.compactMap { $0.csat?.score }
        let csatBase = observedCSAT.isEmpty ? 4.5 : (4.5 + Double(observedCSAT.reduce(0, +)) / Double(observedCSAT.count)) / 2
        let csat = csatCount > 0 ? ((csatBase - 0.12 + 0.24 * DemoReports.unit("csat" + periodKey)) * 100).rounded() / 100 : nil
        let slaBreached = Int((Double(created) * (0.02 + 0.03 * DemoReports.unit("sla" + periodKey))).rounded())

        // Agents: who solved what, narrowed to the team's members.
        let weights: [String: Double] = [DemoPerson.maya.id: 0.24, DemoPerson.jonas.id: 0.17, DemoPerson.priya.id: 0.23, DemoPerson.sam.id: 0.2, DemoPerson.lea.id: 0.16]
        let memberIDs = team.flatMap { id in ws.teams.first { $0.id == id }?.memberIDs }
        let agents = ws.members.filter { $0.role != .viewer && (memberIDs?.contains($0.user.id) ?? true) }
        let agentWeights = Dictionary(uniqueKeysWithValues: agents.map { ($0.user.id, weights[$0.user.id] ?? 0.2) })
        let solvedByAgent = DemoReports.split(solved, agentWeights)
        let byAgent = agents.map { member in
            let id = member.user.id
            let speed = 0.65 + 0.8 * DemoReports.unit("agent" + id + periodKey)
            return ReportAgentRow(
                user: UserRef(id: id, name: member.user.name, email: member.user.email, avatarURL: member.user.avatarURL),
                solved: solvedByAgent[id] ?? 0,
                firstResponseP50: (solvedByAgent[id] ?? 0) > 0 ? (firstResponseBase * speed).rounded() : nil
            )
        }.sorted { $0.solved > $1.solved }

        // Teams: each team's share of the same series, plus tickets without a team.
        var byTeam: [ReportTeamRow] = []
        if team == nil {
            let shares = ws.teams.enumerated().map { ($0.element.id as String?, DemoReports.share($0.element.id, teams: ws.teams)) } + [(nil, DemoReports.share("none", teams: ws.teams))]
            let total = shares.reduce(0) { $0 + $1.1 }
            for (index, entry) in shares.enumerated() {
                let part = entry.1 / max(total, 0.0001)
                let open = live.filter { [.open, .pending, .onHold].contains($0.status) && $0.teamID == entry.0 }.count
                let base = entry.0 == nil ? 76.0 : [34.0, 52, 138][min(2, index)]
                byTeam.append(ReportTeamRow(teamID: entry.0, created: Int((Double(created) * part).rounded()), solved: Int((Double(solved) * part).rounded()), openNow: open, firstResponseP50: created > 0 ? (base * wobble).rounded() : nil))
            }
        } else {
            byTeam = [ReportTeamRow(teamID: team == "none" ? nil : team, created: created, solved: solved, openNow: openNow, firstResponseP50: firstResponse)]
        }

        return ReportOverview(
            created: created, solved: solved, openNow: openNow, firstResponseP50: firstResponse, resolutionP50: resolution,
            csatAvg: csat, csatCount: csatCount, slaBreached: slaBreached, byDay: days, byChannel: byChannel, bySeverity: bySeverity,
            byAgent: byAgent, byTeam: byTeam
        )
    }
}

enum KBDemoText {
    /// A subject in the middle of a sentence: first letter lowered unless it starts an acronym, end marks dropped.
    static func sentenceTail(_ text: String) -> String {
        let trimmed = text.trimmingCharacters(in: CharacterSet(charactersIn: ".?! "))
        let chars = Array(trimmed)
        guard chars.count > 1, !chars[1].isUppercase else { return trimmed }
        return chars[0].lowercased() + String(chars.dropFirst())
    }

    /// A ticket subject as literal markdown text.
    static func escape(_ text: String) -> String {
        var out = ""
        for character in text {
            if KBMarkdown.escapable.contains(character) { out.append("\\") }
            out.append(character)
        }
        return out
    }
}

enum DemoReports {
    /// A stable number in 0..<1 for a key (FNV-1a), so a day always has the same figures.
    static func unit(_ key: String) -> Double {
        var hash: UInt64 = 0xcbf2_9ce4_8422_2325
        for byte in key.utf8 {
            hash ^= UInt64(byte)
            hash = hash &* 0x0000_0100_0000_01b3
        }
        return Double(hash % 10000) / 10000
    }

    /// The part of the workspace's volume a team filter keeps.
    static func share(_ team: String?, teams: [Team]) -> Double {
        guard let team else { return 1 }
        if team == "none" { return teams.isEmpty ? 1 : 0.07 }
        guard let index = teams.firstIndex(where: { $0.id == team }) else { return 0 }
        return [0.6, 0.25, 0.08, 0.05][min(3, index)]
    }

    /// Default proportions nudged toward what the workspace's tickets show.
    static func blend(_ defaults: [String: Double], observed: [String]) -> [String: Double] {
        guard !observed.isEmpty else { return defaults }
        var out = defaults.mapValues { $0 * 0.6 }
        for value in observed where out[value] != nil { out[value, default: 0] += 0.4 / Double(observed.count) }
        return out
    }

    /// `total` split by weights into whole numbers that add up to it.
    static func split(_ total: Int, _ weights: [String: Double]) -> [String: Int] {
        let sum = weights.values.reduce(0, +)
        guard sum > 0, total > 0 else { return weights.mapValues { _ in 0 } }
        var out = weights.mapValues { Int((Double(total) * $0 / sum).rounded(.down)) }
        var rest = total - out.values.reduce(0, +)
        for key in weights.sorted(by: { $0.value > $1.value }).map(\.key) where rest > 0 {
            out[key, default: 0] += 1
            rest -= 1
        }
        return out
    }
}
#endif
