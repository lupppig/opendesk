package sample

// Written content for sample data, in the tone of the web app's demo (web/src/mock):
// teammates, customers, companies, ticket stories, generated tickets, chats, knowledge
// base articles and Collab mail. Placeholders are filled when a record is generated (fill):
// {first}, {name} and {company} are the customer's, {agent} the replying teammate's first
// name, {mention} a teammate mention, {product} the workspace's name and {site} its demo
// domain. Every address and link is on the reserved .invalid top-level domain.

type agentDef struct{ name, title string }

// agentNames are the generated teammates, in order.
var agentNames = []agentDef{
	{"Maya Chen", "Support lead"}, {"Jonas Weber", "Support engineer"}, {"Priya Raman", "Customer success"},
	{"Sam Okafor", "Support specialist"}, {"Lea Martin", "Billing specialist"}, {"Theo Brandt", "Product manager"},
	{"Nadia Petrov", "Support engineer"}, {"Lucas Ferreira", "Support specialist"}, {"Hannah Schulz", "Onboarding manager"},
	{"Omar Haddad", "Technical support"}, {"Sofia Russo", "Customer success"}, {"Ethan Park", "Support specialist"},
	{"Amara Nwosu", "Account manager"}, {"Felix Wagner", "Technical support"}, {"Ingrid Larsen", "Billing specialist"},
	{"Diego Alvarez", "Support specialist"}, {"Mei Tanaka", "Support engineer"}, {"Arjun Mehta", "Solutions engineer"},
	{"Clara Jensen", "Customer success"}, {"Mateus Costa", "Support specialist"}, {"Yara Saleh", "Security engineer"},
	{"Noah Becker", "Support specialist"}, {"Elif Kaya", "Onboarding specialist"}, {"Ruben Visser", "Partner manager"},
	{"Zara Ahmed", "Legal counsel"},
}

type teamDef struct {
	key, name, color, desc string
	private                bool
}

// teamDefs are the generated teams, in order; a size takes the first n.
var teamDefs = []teamDef{
	{"support", "Support", "blue", "First line for product questions, bugs and how-tos.", false},
	{"billing", "Billing", "amber", "Invoices, refunds, plan changes and payment problems.", false},
	{"technical", "Technical", "indigo", "API, integrations, SSO and anything that needs an engineer.", false},
	{"onboarding", "Onboarding", "green", "New workspaces, imports and team setup.", false},
	{"enterprise", "Enterprise", "violet", "Our largest customers, with named account managers.", false},
	{"partnerships", "Partnerships", "teal", "Resellers, integrations and co-marketing.", false},
	{"security", "Security", "red", "Vulnerability reports and account security. Reports are confidential until fixed.", true},
	{"legal", "Legal", "purple", "Contracts, DPAs, GDPR requests and anything a lawyer should read first.", false},
}

type companyDef struct{ name, notes string }

// companyDefs are the named companies; more are built from companyPrefixes and companySuffixes.
var companyDefs = []companyDef{
	{"Northwind Traders", "Enterprise plan, 240 seats. Renewal in March."},
	{"Halcyon Studio", "Design agency. Heavy PDF export users."},
	{"Brightline Logistics", "Annual billing via invoice."},
	{"Oakridge Health", "SSO through Okta. HIPAA addendum signed."},
	{"Paperplane", "Startup plan. Very active on feature requests."},
	{"Vantage Legal", "Needs DPA and data residency in the EU."},
	{"Copperleaf", "Builds on our API and webhooks."},
	{"Meridian Schools", "Education discount. 3 campuses."},
	{"Fjord Analytics", "API heavy, syncs notes nightly."},
	{"Tidewater Media", "Podcast network, 40 seats."},
	{"Kestrel Robotics", "Mostly mobile users in the field."},
	{"Solstice Bakery", "Small team, monthly plan."},
}

var companyPrefixes = []string{"Blue Harbor", "Silverline", "Northgate", "Brightwater", "Cedar", "Summit", "Maple", "Atlas", "Lumen",
	"Pinecrest", "Riverbend", "Granite", "Aurora", "Juniper", "Copperfield", "Willow", "Falcon", "Orbit", "Meadow", "Redwood"}

var companySuffixes = []string{"Labs", "Logistics", "Health", "Studio", "Systems", "Media", "Partners", "Foods", "Robotics",
	"Analytics", "Legal", "Schools", "Energy", "Capital", "Works", "Retail"}

var firstNames = []string{
	"Dana", "Marcus", "Elena", "Tomás", "Aisha", "Ben", "Chloé", "Daniel", "Freya", "Gabriel",
	"Hana", "Isaac", "Julia", "Kofi", "Laura", "Mateo", "Nina", "Oliver", "Paula", "Quentin",
	"Rosa", "Stefan", "Tara", "Umar", "Vera", "Will", "Ximena", "Yusuf", "Zoe", "Anders",
	"Bianca", "Carlos", "Delia", "Emil", "Farah", "Greta", "Hugo", "Ines", "Jakob", "Keiko",
	"Liam", "Mira", "Noah", "Olga", "Pablo",
}

var lastNames = []string{
	"Whitfield", "Lindqvist", "Rossi", "García", "Bello", "Hartmann", "Dubois", "Kim", "Nilsen", "Moreau",
	"Sato", "Levy", "Novak", "Mensah", "Fischer", "Silva", "Petrova", "Grant", "Costa", "Laurent",
	"Ortega", "Bauer", "Singh", "Haddad", "Ivanova", "Turner", "Reyes", "Demir", "Park", "Holm",
	"Conti", "Mendes", "Fox", "Berg", "Rahman", "Vogel", "Martins", "Ferreira", "Klein", "Tanaka",
	"Murphy", "Shah", "Andersson", "Kowalski", "Navarro",
}

// personalDomains are where customers without a company write from.
var personalDomains = []string{"mail.example.invalid", "inbox.example.invalid", "post.example.invalid", "home.example.invalid"}

var contactNotes = []string{
	"IT lead. Prefers email, very responsive.",
	"Decision maker for the renewal.",
	"Asked to be told about the offline mode beta.",
	"Escalate SSO issues to the technical team.",
	"Usually writes in German; replies in English are fine.",
}

// Labels: the Type group the workspace starts with, and topical ones.
const (
	labelQuestion = "Question"
	labelProblem  = "Problem"
	labelBug      = "Bug"
	labelFeature  = "Feature request"
	labelBilling  = "Billing"
	labelVIP      = "VIP"
	labelFeedback = "Feedback"
)

var topicLabels = map[string]string{"sso": "SSO", "api": "API", "mobile": "Mobile", "export": "Export",
	"onboarding": "Onboarding", "refund": "Refund"}

// allLabels are the labels sample tickets use, created when missing.
var allLabels = []string{labelQuestion, labelProblem, labelBug, labelFeature, labelBilling, labelVIP, labelFeedback,
	"SSO", "API", "Mobile", "Export", "Onboarding", "Refund"}

type storyMsg struct {
	from string // "c" (the customer), "a" (the assignee), "b" (a second teammate)
	note bool
	html string
	min  int // minutes ago
}

type story struct {
	subject  string
	status   string
	priority string
	severity string
	channel  string
	assigned bool
	team     string
	labels   []string
	csat     int
	comment  string
	thread   []storyMsg
}

// stories are the hand-written tickets every batch starts with.
var stories = []story{
	{subject: "SSO login loops back to sign-in page", status: "open", priority: "urgent", severity: "sev1", channel: "email", assigned: true,
		team: "technical", labels: []string{labelProblem, "SSO"}, thread: []storyMsg{
			{from: "c", min: 95, html: `<p>Hi {product} team,</p><p>Since this morning nobody at {company} can sign in through Okta. After entering credentials we land on the sign-in page again, and it loops. Clearing cookies doesn't help.</p><ul><li>Started around 07:40</li><li>Affects all 180 users on the Okta tile</li><li>Password login for our admin account still works</li></ul><p>This is blocking our clinical documentation team, so any help is appreciated.</p><p>{name}<br>IT Operations, {company}</p>`},
			{from: "a", min: 82, html: `<p>Hi {first},</p><p>Thanks for the detailed report, that helps a lot. I can see the SAML responses reaching us, but they're being rejected before a session is created.</p><p>Could you check whether the signing certificate in your Okta app was rotated recently? If it was, send me the new certificate fingerprint and I'll compare it with what we have on file.</p><p>Best,<br>{agent}</p>`},
			{from: "a", note: true, min: 78, html: `<p>{mention} can you pull the SAML assertion logs for {company} from the last 3 hours? I suspect a certificate rotation on their side, but the error code is <code>invalid_signature</code> on every request.</p>`},
			{from: "b", note: true, min: 61, html: `<p>Confirmed. Every assertion since 07:38 is signed with a cert we don't have (fingerprint starts with <code>4F:1A:9C</code>). The old one expires next week, so they rotated early. Once they confirm we can add the new cert and keep both active.</p>`},
			{from: "c", min: 6, html: `<p>Hi {agent},</p><p>You were right, our identity team rotated the certificate last night without telling us. The new fingerprint is <code>4F:1A:9C:22:E0:7B:51:D3</code>.</p><p>Can you add it on your side? We'd rather not roll back.</p><p>Thanks,<br>{first}</p>`},
		}},
	{subject: "Can't export notebook to PDF", status: "open", priority: "high", severity: "sev3", channel: "email", team: "support",
		labels: []string{labelBug, "Export"}, thread: []storyMsg{
			{from: "c", min: 14, html: `<p>Hello,</p><p>When I export our "Client brief" notebook to PDF the download starts, then fails with "Export failed, try again later". Smaller notebooks export fine. This one has about 60 pages and a lot of images.</p><p>We need this for a client presentation tomorrow morning.</p><p>{first}</p>`},
		}},
	{subject: "Invoice shows wrong VAT number", status: "pending", priority: "normal", channel: "email", assigned: true, team: "billing",
		labels: []string{labelBilling}, thread: []storyMsg{
			{from: "c", min: 60 * 26, html: `<p>Hi,</p><p>Our October invoice (INV-2026-10-0412) lists our old VAT number DE298113420. We changed it in the billing settings two weeks ago. Our accounting can't book the invoice like this.</p><p>Could you reissue it?</p><p>{name}<br>Finance, {company}</p>`},
			{from: "a", min: 60 * 25, html: `<p>Hi {first},</p><p>Sorry about that. The change was saved after the invoice was generated, so the October run still used the old number. I've voided INV-2026-10-0412 and issued INV-2026-10-0498 with DE311402967.</p><p>Can you confirm the new one works for your accounting team?</p><p>{agent}</p>`},
			{from: "a", note: true, min: 60*25 - 5, html: `<p>Filed with billing engineering: VAT changes should regenerate open invoices. {mention} for the billing queue.</p>`},
		}},
	{subject: "Refund for duplicate annual charge", status: "open", priority: "high", channel: "email", assigned: true, team: "billing",
		labels: []string{labelBilling, "Refund"}, thread: []storyMsg{
			{from: "c", min: 60 * 5, html: `<p>Hello,</p><p>We were charged twice for our annual renewal on October 9th (2 × €4,320). Can you refund the duplicate? Card ending 4417.</p><p>{name}, {company}</p>`},
			{from: "a", min: 60*4 + 20, html: `<p>Hi {first},</p><p>You're right, the renewal retried after a timeout and both attempts went through. I've refunded the second charge of €4,320. It usually shows on your statement within 5 to 10 business days.</p><p>Apologies for the trouble.</p><p>{agent}</p>`},
			{from: "c", min: 35, html: `<p>Hi, just checking in on this. The refund isn't on our statement yet. Is it being processed?</p>`},
		}},
	{subject: "Mobile app crashes when opening shared notebook", status: "open", priority: "urgent", severity: "sev2", channel: "email",
		team: "support", labels: []string{labelBug, "Mobile"}, thread: []storyMsg{
			{from: "c", min: 60*3 + 10, html: `<p>Hi,</p><p>The iOS app (4.12.0) crashes immediately when any of our field engineers open the "Site inspections" shared notebook. Other notebooks open fine. iPad and iPhone, iOS 26.0.1.</p><p>About 30 people are affected and they can't fill in their reports.</p><p>{name}<br>{company}</p>`},
		}},
	{subject: "How do I invite my team?", status: "solved", priority: "normal", channel: "portal", assigned: true, team: "onboarding",
		labels: []string{labelQuestion, "Onboarding"}, csat: 5, comment: "Super quick, thanks!", thread: []storyMsg{
			{from: "c", min: 60 * 30, html: `<p>Hi, we just upgraded to the Education plan. How do I invite the other teachers? I can't find it in settings.</p>`},
			{from: "a", min: 60 * 29, html: `<p>Hi {first},</p><p>Welcome aboard! Go to <strong>Settings → Members</strong> and click <strong>Invite</strong>. You can paste a list of addresses separated by commas, and choose their role.</p><ol><li>Open Settings from the bottom of the sidebar</li><li>Choose Members</li><li>Paste the addresses and press Send invites</li></ol><p>{agent}</p>`},
			{from: "c", min: 60 * 28, html: `<p>Perfect, all 14 invites went out. Thank you!</p>`},
		}},
	{subject: "API returns 429 when syncing notes", status: "open", priority: "high", severity: "sev3", channel: "api", assigned: true,
		team: "technical", labels: []string{labelProblem, "API"}, thread: []storyMsg{
			{from: "c", min: 60 * 2, html: `<p>Our nightly sync started failing with <code>429 Too Many Requests</code> after about 1,200 calls. We're well under the documented 10k/hour.</p><pre><code>GET /v1/notes?updated_since=2026-10-10T00:00:00Z
HTTP/1.1 429
RateLimit-Remaining: 0</code></pre><p>Has something changed?</p>`},
			{from: "a", min: 4, html: `<p>Hi {first},</p><p>Thanks for the request log. The limit you're hitting is the per-endpoint burst limit on <code>/v1/notes</code> (1,200 per 10 minutes), not the hourly one. If you page with <code>cursor</code> instead of <code>updated_since</code> windows, you'll need about a tenth of the calls.</p><p>I've also raised your burst limit to 3,000 for tonight's run.</p><p>{agent}</p>`},
		}},
	{subject: "Webhook signature doesn't validate", status: "on_hold", priority: "normal", severity: "sev3", channel: "email", assigned: true,
		team: "technical", labels: []string{labelBug, "API"}, thread: []storyMsg{
			{from: "c", min: 60 * 50, html: `<p>We're verifying the webhook signature exactly like the docs show, but about 1 in 20 deliveries fails validation. The failing ones all have unicode in the note title.</p>`},
			{from: "a", min: 60 * 47, html: `<p>Hi {first},</p><p>Good catch. Make sure you compute the HMAC over the raw request bytes, before any JSON parsing or re-encoding. Some frameworks normalize unicode escapes, which changes the body.</p><p>If you're already doing that, send me two event ids that failed and I'll compare them with what we signed.</p>`},
			{from: "c", min: 60 * 30, html: `<p>We use the raw body. Failing ids: <code>0192f0c4-77ab</code> and <code>0192f0c9-1e02</code>.</p>`},
			{from: "a", note: true, min: 60 * 29, html: `<p>Reproduced. Our relay re-serializes titles with escaped unicode. Fix is in review, {mention} putting this on hold until it ships.</p>`},
		}},
	{subject: "Can we get a DPA signed?", status: "pending", priority: "normal", channel: "email", assigned: true, team: "legal",
		labels: []string{labelQuestion}, thread: []storyMsg{
			{from: "c", min: 60 * 70, html: `<p>Hello,</p><p>Before we roll {product} out firm-wide our compliance team needs a signed data processing agreement, and confirmation that our data stays in the EU region.</p><p>{name}, {company}</p>`},
			{from: "a", min: 60 * 68, html: `<p>Hi {first},</p><p>Of course. Our standard DPA is on its way, already countersigned on our side. Your workspace is hosted in Frankfurt and backups stay in the EU.</p><p>Once your team signs, reply with the PDF and we'll file it.</p><p>{agent}</p>`},
		}},
	{subject: "Search doesn't find text inside images", status: "open", priority: "normal", channel: "email", assigned: true, team: "support",
		labels: []string{labelFeature}, thread: []storyMsg{
			{from: "c", min: 60 * 9, html: `<p>We scan a lot of handwritten notes into {product}. Is there a way to search the text inside those images? Right now search only finds the captions.</p>`},
		}},
	{subject: "Thank you!", status: "solved", priority: "low", channel: "email", assigned: true, team: "",
		labels: []string{labelFeedback}, csat: 4, thread: []storyMsg{
			{from: "c", min: 60 * 20, html: `<p>Just wanted to say the new recipe templates are lovely. Our bakers use them every morning.</p>`},
			{from: "a", min: 60 * 19, html: `<p>That made our day, thank you for telling us! I'll pass it on to the team that built them.</p>`},
		}},
}

type generated struct {
	subject, body string
	tags          []string
}

// generatedTickets are subjects and first messages for the rest of the tickets.
var generatedTickets = []generated{
	{"Two-factor codes not accepted", "My authenticator codes are rejected every time since I got a new phone. I moved the app over with the transfer tool.", nil},
	{"Change billing email address", "Invoices still go to our former office manager. Can you send them to accounts@ from now on?", []string{"billing"}},
	{"Notebook sharing link expired too soon", "I set a share link to expire in 30 days but our client says it stopped working after a week.", []string{"bug"}},
	{"Request: dark mode for the web clipper", "The browser clipper is blinding at night. Any chance of a dark theme?", []string{"feature-request"}},
	{"Imported Evernote notes lost formatting", "We imported 3,000 notes from Evernote. Tables and checklists came through as plain text.", []string{"bug", "onboarding"}},
	{"Upgrade from Team to Business mid-cycle", "If we upgrade halfway through the month, how is the difference billed?", []string{"billing"}},
	{"Calendar integration stopped syncing", "Meeting notes no longer appear for events since Tuesday. Google Calendar is still connected in settings.", []string{"bug"}},
	{"How do I archive old notebooks?", "We want to hide last year's project notebooks without deleting them. Is that possible?", []string{"onboarding"}},
	{"Slow loading on large notebooks", "Our wiki notebook with 900 pages takes almost 20 seconds to open in the web app.", []string{"bug"}},
	{"Add SCIM provisioning", "We'd like users created and deactivated from Azure AD automatically. Do you support SCIM?", []string{"sso", "feature-request"}},
	{"Refund request for unused seats", "We bought 20 extra seats in August but only used 6. Could we get a partial refund?", []string{"billing", "refund"}},
	{"Offline mode on Android", "Notes I write offline on Android disappear when I reconnect. This has happened three times now.", []string{"bug", "mobile"}},
	{"API token permissions", "Is it possible to create a read-only API token that can only access one notebook?", []string{"api"}},
	{"Templates missing after workspace merge", "After merging our two workspaces, the shared templates from the second one are gone.", []string{"bug"}},
	{"Student discount eligibility", "I'm a PhD student. Does the education discount apply to individual plans?", []string{"billing"}},
	{"Pasting from Google Docs adds extra spacing", "Every paragraph pasted from Docs gets a blank line after it. Very annoying for long documents.", []string{"bug"}},
	{"Can guests comment without an account?", "We want clients to leave comments on a shared notebook without signing up. Is that supported?", []string{"feature-request"}},
	{"Okta app missing from integration catalog", "Searching for {product} in the Okta integration network finds nothing. How do we set up SSO?", []string{"sso", "onboarding"}},
	{"Export all data before cancelling", "We're consolidating tools. What's the best way to export everything, including attachments?", []string{"export"}},
	{"Webhook retries flooding our endpoint", "Our endpoint was down for an hour and now we're receiving hundreds of retried deliveries at once.", []string{"api"}},
	{"Handwriting recognition on iPad", "Apple Pencil notes are not converted to text even though the setting is on.", []string{"mobile"}},
	{"Wrong timezone on reminders", "Reminders fire an hour late since the clocks changed. My timezone is set to Europe/Lisbon.", []string{"bug"}},
	{"Bulk move notes between notebooks", "Is there a way to select 50 notes and move them at once? Dragging one by one takes forever.", []string{"feature-request"}},
	{"Payment failed but card is valid", "We got a payment failed email but the card works everywhere else. Can you retry the charge?", []string{"billing"}},
	{"Attachment size limit", "I can't upload a 120 MB video to a note. What's the limit on our plan?", nil},
	{"Shared notebook shows as empty for one user", "One teammate sees the Roadmap notebook as empty. Everyone else sees all pages.", []string{"bug"}},
	{"Setting up SAML with Google Workspace", "Following your guide, but Google asks for an ACS URL that isn't listed. Where can I find it?", []string{"sso"}},
	{"Rename workspace URL", "We rebranded. Can we change our workspace URL from our old name to the new one?", nil},
	{"Markdown export drops images", "Exporting to Markdown gives me the text but every image link points to a temporary URL.", []string{"export", "bug"}},
	{"Zapier trigger for new notes", "The Zapier 'New note' trigger only fires for notes in the default notebook.", []string{"api"}},
	{"Account locked after password reset", "I reset my password and now it says my account is locked for 30 minutes.", nil},
	{"Volume pricing for 500 seats", "We're evaluating {product} for the whole company. Do you offer volume pricing over 500 seats?", []string{"billing"}},
	{"Widget for embedding notes in our intranet", "Is there an embed code to show a published note inside our SharePoint intranet?", []string{"feature-request"}},
	{"Checklist items reorder themselves", "When I tick a checklist item it jumps to the bottom. Can that be turned off?", nil},
	{"Duplicate notes after sync conflict", "I edited a note on my laptop and phone and now I have three copies of it.", []string{"bug", "mobile"}},
	{"Where is the audit log?", "Our security team wants to see who accessed which notebook last month.", nil},
	{"Invoice in USD instead of EUR", "Our invoices switched to USD this month. We're based in Spain and always paid in EUR.", []string{"billing"}},
	{"Keyboard shortcuts on Windows", "Half of the shortcuts listed in the help center don't work on Windows with a German keyboard.", []string{"bug"}},
	{"Onboarding call for new team", "We just signed up 25 people. Can we schedule a short onboarding session?", []string{"onboarding"}},
	{"API pagination returns duplicates", "When paging through /v1/notes with cursor, some notes show up on two pages.", []string{"api", "bug"}},
	{"Print layout cuts off tables", "Printing a note with a wide table cuts off the last two columns.", []string{"export"}},
	{"Can't remove former employee", "Removing a member fails with 'owner cannot be removed' but they're not the owner anymore.", nil},
	{"Feature request: version history diff", "Version history is great but it would help to see what changed between versions.", []string{"feature-request"}},
	{"Sync paused on macOS app", "The macOS app says 'Sync paused' and the resume button does nothing.", []string{"bug"}},
	{"Tax exemption certificate", "We're a non-profit. Where can we upload our tax exemption certificate?", []string{"billing"}},
	{"Mention notifications not arriving", "When colleagues @mention me I don't get an email, though notifications are on.", []string{"bug"}},
	{"Transfer ownership of workspace", "Our founder is leaving and owns the workspace. How do we transfer ownership?", nil},
	{"Search results ignore accents", "Searching 'cafe' doesn't find notes with 'café'. Is accent-insensitive search possible?", []string{"feature-request"}},
	{"Images blurry in shared links", "Images in a public share link look heavily compressed compared to inside the app.", nil},
	{"Connect Microsoft Teams", "Is there a Teams integration so we can share notes into channels?", []string{"feature-request"}},
	{"Downgrade to free plan", "We'd like to downgrade at the end of the billing period. What happens to our extra notebooks?", []string{"billing"}},
	{"Login with Apple fails on web", "Sign in with Apple works on iPhone but on the web I get 'invalid client'.", []string{"sso", "bug"}},
	{"Read receipts for shared notes", "Can I see whether a client has opened the note I shared with them?", []string{"feature-request"}},
	{"Notebook permissions for contractors", "We want contractors to edit one notebook only, without seeing the rest of the workspace.", []string{"onboarding"}},
	{"Data retention settings", "Can we automatically delete notes older than 3 years for compliance?", nil},
	{"Copying tables into Excel", "Copying a table from a note into Excel pastes everything into a single cell.", nil},
}

type spamDef struct{ subject, from, name, body string }

var spam = []spamDef{
	{"Boost your SEO ranking today 🚀", "growth@rank-boost-pro.example.invalid", "Rank Boost", "We guarantee first page on Google in 7 days. Reply YES for a free audit!!!"},
	{"You have won a $500 gift card", "rewards@prize-center.example.invalid", "Prize Center", "Congratulations! Click the link to claim your $500 gift card before it expires."},
	{"Partnership opportunity: guest posts", "outreach@linkfarm.example.invalid", "Kelly from LinkFarm", "We'd love to publish a sponsored article on your blog. Our rates start at $50."},
	{"URGENT: verify your account", "security@account-verify.example.invalid", "Account Security", "Your account will be suspended in 24 hours. Verify your password here immediately."},
}

var followUps = []string{
	"Any update on this? It's still happening on our side.",
	"I tried that but no luck. Same result as before.",
	"Following up here, we'd love a fix this week.",
	"That makes sense. One more question: does this also apply to the mobile app?",
	"Thanks. Is there a workaround we can use in the meantime?",
}

var thanks = []string{"Thanks, that worked!", "Great, appreciate the quick reply.", "All sorted now, thank you.", "Perfect, that's exactly what I needed."}

var agentReplies = []string{
	"Thanks for reaching out. I've reproduced this on our side and passed it to the engineering team with your details. I'll update you as soon as there's a fix.",
	"Good question. You can find this under Settings, then Workspace. If the option is greyed out, your role might need to be admin.",
	"Sorry for the trouble. Could you send me the exact time it happened and the browser you were using? That will help us find it in the logs.",
	"I've made the change on our side. It may take a few minutes to show up. Let me know if anything still looks off.",
	"That's not possible today, but I've added your vote to the request. It's something we're looking at for next quarter.",
	"Thanks for the details. This is a known issue and a fix ships in the next release, usually within a week. I'll let you know when it's out.",
	"I've checked your account and everything looks right on our side. Could you try signing out and back in, then let me know what you see?",
}

var secondReplies = []string{
	"Thanks for trying that. I've escalated it to our engineers and will get back to you by tomorrow.",
	"Good news: the fix is live. Could you check again and let me know?",
	"Understood. In the meantime, exporting the notebook in sections works around it.",
}

var notes = []string{
	"<p>{mention} can you take a look? Looks related to the sync issue from last week.</p>",
	"<p>Customer is on the Business plan, prioritize.</p>",
	"<p>Checked the logs, nothing unusual on our side yet. {mention} any ideas?</p>",
	"<p>{mention} this one needs a refund approval from you.</p>",
	"<p>Reproduced on staging and filed with engineering. {mention} for visibility.</p>",
}

const onHoldNote = "<p>Waiting on engineering for a fix. Putting this on hold until it ships.</p>"

var csatScores = []int{5, 5, 5, 5, 5, 5, 4, 4, 4, 3, 2, 1}

var csatComments = []string{"", "", "Fast and friendly.", "Super helpful, thank you!", "Solved my problem in minutes.", "Took a while but solved."}
var csatComplaints = []string{"Had to explain the problem twice.", "Still not fixed, but thanks for trying.", "Slow reply.", ""}

type chatLine struct {
	who  string // "v" visitor, "a" teammate, "o" Ollie
	text string
}

type chatScript struct {
	subject string
	page    string
	lines   []chatLine
}

// chatScripts are finished or ongoing chats.
var chatScripts = []chatScript{
	{"Pricing question", "/pricing", []chatLine{{"v", "Is the Business plan billed per member or per workspace?"},
		{"a", "Hi {first}! It's per member, billed monthly or yearly. Yearly saves about 15%."}, {"v", "Got it, thanks!"}}},
	{"Nested tags?", "/features", []chatLine{{"v", "hey! quick one: is there any way to nest tags? like clients/acme and clients/globex"},
		{"v", "we have ~200 tags now and the sidebar is getting wild 😅"},
		{"a", "Not yet, but it's on our list. For now many teams use a prefix like clients- and filter by it."}, {"v", "ok that works for now, thanks"}}},
	{"Locked out after password reset", "/login", []chatLine{{"v", "I reset my password and now it says my account is locked"},
		{"a", "Sorry about that! The lock lifts after 30 minutes, but I've just unlocked it for you. Try again now?"}, {"v", "worked, thank you!!"}}},
	{"Markdown export", "/help/export", []chatLine{{"v", "Does Markdown export include images?"},
		{"o", "Yes. Markdown export puts images in an assets folder next to each note. You'll find it under Settings, then Export."}, {"v", "perfect"}}},
	{"Switch to yearly billing", "/settings/billing", []chatLine{{"v", "How do I switch from monthly to yearly billing?"},
		{"o", "Owners can change it in Settings, then Billing. The switch takes effect at your next renewal."},
		{"v", "Can I get an invoice for the old months too?"},
		{"a", "Hi {first}, I'm {agent}. Sure, I've emailed all your invoices to the billing address on file."}}},
	{"Where's the audit log?", "/help", []chatLine{{"v", "Our security team needs to see who opened which notebook last month. Where is that?"},
		{"a", "It's in Settings, then Security, then Audit log, on the Business plan. You can export it as CSV."}, {"v", "found it, thanks"}}},
	{"Webhook retries", "/docs/webhooks", []chatLine{{"v", "our endpoint was down an hour, now we're getting flooded with retries"},
		{"a", "Retries back off over 24 hours, so the burst should calm down in a few minutes. You can also pause the webhook in Settings while you catch up."}}},
	{"Custom domain still pending", "/settings/domain", []chatLine{{"v", "I added the CNAME two days ago but the domain still says pending"},
		{"a", "Let me check. The record points to an old target. Can you change it to domains.{site}?"}, {"v", "changed it, let's see"}}},
	{"Offline notes on Android", "/download", []chatLine{{"v", "notes I write offline on Android disappear when I reconnect"},
		{"a", "That's not good. Which app version are you on? You'll find it under Settings, then About."}, {"v", "4.12.1"},
		{"a", "Thanks. That version has a sync bug we fixed in 4.12.2, rolling out today. Your notes are safe on the device until then."}}},
	{"Thank you!", "/", []chatLine{{"v", "Just wanted to say the new templates are lovely. Our team uses them every morning."},
		{"a", "That made our day, thank you for telling us!"}}},
	{"Import from Notion", "/help/import", []chatLine{{"v", "Can I bring my Notion workspace over?"},
		{"o", "Yes. Export your Notion workspace as Markdown and CSV, then upload the zip under Settings, then Import. Databases come over as tables."},
		{"v", "nice, trying it now"}}},
}

// waitingOpeners start the chats waiting for a teammate right now.
var waitingOpeners = []chatScript{
	{"Whole team logged out", "/login", []chatLine{{"v", "Hi, is anyone there? Our whole team got logged out and can't sign back in."}}},
	{"Pay by invoice?", "/pricing", []chatLine{{"v", "Hello! Can I pay by invoice instead of card?"}, {"v", "We need it for our procurement team."}}},
	{"Export stuck at 99%", "/notebooks", []chatLine{{"v", "my export has been stuck at 99% for 20 minutes"}}},
	{"Nonprofit discount", "/pricing", []chatLine{{"v", "Do you offer a discount for nonprofits?"}}},
	{"App keeps signing me out", "/download", []chatLine{{"v", "The mobile app keeps asking me to sign in again"}, {"v", "every few minutes, on iPhone"}}},
	{"Add a second admin", "/settings/members", []chatLine{{"v", "How do I add a second admin to our workspace?"}}},
	{"Restore a deleted notebook", "/notebooks", []chatLine{{"v", "is there a way to restore a deleted notebook?"}}},
	{"SSO for 300 people", "/enterprise", []chatLine{{"v", "We're evaluating {product} for 300 people. Can someone walk us through SSO?"}}},
}

type savedReplyDef struct{ title, shortcut, body string }

var savedReplies = []savedReplyDef{
	{"Greeting and thanks", "thanks", "<p>Hi {{contact.first_name}},</p><p>Thanks for reaching out. I'm looking into this now and will get back to you shortly.</p>"},
	{"Ask for steps to reproduce", "repro", "<p>Could you share the steps that lead to this, the browser or app version you're on, and roughly when it happened? A screenshot helps too.</p>"},
	{"Refund processed", "refund", "<p>I've processed the refund. It usually shows on your statement within 5 to 10 business days, depending on your bank.</p>"},
	{"SSO setup guide", "sso", `<p>Here is our step-by-step guide for setting up SAML single sign-on: <a href="https://help.{site}/sso">help.{site}/sso</a>. Your ACS URL is shown in Settings, then Security.</p>`},
	{"Feature request logged", "fr", "<p>Thanks for the idea. I've logged it with our product team and added your use case. We'll let you know if it lands on the roadmap.</p>"},
	{"Closing, no reply", "close", "<p>I haven't heard back in a few days, so I'll close this for now. Just reply to this email if you need anything else and it will reopen.</p>"},
	{"Known issue, fix coming", "known", "<p>This is a known issue and our engineers are on it. I'll update you here as soon as the fix is out.</p>"},
	{"Escalated to engineering", "escalate", "<p>I've passed this to our engineering team with everything you sent. I'll follow up as soon as I hear back.</p>"},
}

type collectionDef struct{ name, desc string }

var collections = []collectionDef{
	{"Getting started", "Set up your workspace and learn the basics."},
	{"Account & billing", "Plans, invoices, seats and payment methods."},
	{"Integrations", "Connect {product} with the tools you already use."},
	{"Troubleshooting", "Fixes for common problems with sync, export and sign-in."},
	{"Security & privacy", "Single sign-on, two-factor authentication and how we keep your data safe."},
	{"Mobile apps", "Use {product} on iPhone, iPad and Android."},
	{"Administration", "Members, roles, audit logs and workspace settings."},
	{"Release notes", "What changed in {product}, month by month."},
}

type articleDef struct {
	collection int
	title      string
	draft      bool
	body       string
}

// articles are the hand-written knowledge base articles.
var articles = []articleDef{
	{0, "Create your first notebook", false, `<p>Notebooks are the top-level containers in {product}. Every note lives in exactly one notebook, and every notebook has its own sharing settings.</p><h2>Create a notebook</h2><ol><li>Click <strong>New</strong> at the top of the sidebar.</li><li>Choose <strong>Notebook</strong> and give it a name.</li><li>Pick an icon, or leave the default.</li></ol><p>The notebook appears in your sidebar right away. Drag it to reorder.</p><h2>Share it</h2><p>Open the notebook and press <strong>Share</strong>. You can invite teammates by email or create a link anyone in your workspace can open.</p><blockquote>Tip: press <code>N</code> anywhere to create a new note in the current notebook.</blockquote>`},
	{0, "Invite your team", false, `<p>You can invite people from <strong>Settings → Members</strong>. Paste one or more addresses, choose a role and send.</p><h2>Roles</h2><ul><li><strong>Owner</strong> manages billing and can delete the workspace.</li><li><strong>Admin</strong> manages members and settings.</li><li><strong>Member</strong> creates and edits notes.</li><li><strong>Guest</strong> only sees notebooks shared with them.</li></ul><h2>Domain sign-up</h2><p>Turn on domain sign-up to let anyone with your company email join without an invite.</p>`},
	{0, "Keyboard shortcuts", false, `<p>Press <code>?</code> anywhere in the app to see every shortcut. These are the ones people use most:</p><ul><li><code>⌘ K</code> opens search and commands</li><li><code>N</code> creates a note</li><li><code>⌘ ⇧ L</code> toggles a checklist</li><li><code>⌘ /</code> opens the block menu</li></ul><p>On Windows and Linux, use <code>Ctrl</code> instead of <code>⌘</code>.</p>`},
	{0, "Import from Evernote and Notion", false, `<p>{product} imports <code>.enex</code> files from Evernote and the Markdown and CSV export from Notion.</p><h2>From Evernote</h2><ol><li>In Evernote, export a notebook as <code>.enex</code>.</li><li>In {product}, open <strong>Settings → Import</strong>.</li><li>Drop the file and choose a destination notebook.</li></ol><p>Tags become {product} tags. Checklists and tables keep their formatting.</p><h2>From Notion</h2><p>Export your workspace as <strong>Markdown &amp; CSV</strong>, then upload the zip. Databases are imported as tables.</p>`},
	{1, "Change your plan", false, `<p>Owners can change the plan from <strong>Settings → Billing</strong>.</p><h2>Upgrading</h2><p>Upgrades take effect immediately. You're charged the prorated difference for the rest of the current period.</p><h2>Downgrading</h2><p>Downgrades take effect at the end of the current period. If you're over the new plan's limits, extra notebooks become read-only until you archive some.</p>`},
	{1, "Update your VAT number and billing details", false, `<p>Billing details are used for every invoice generated after you save them. Invoices that were already issued are not changed automatically.</p><ol><li>Open <strong>Settings → Billing</strong>.</li><li>Choose <strong>Billing details</strong>.</li><li>Update your company name, address and VAT number.</li></ol><p>Need a corrected copy of an earlier invoice? Contact us and we'll reissue it.</p>`},
	{1, "Refunds and cancellations", false, `<p>Monthly plans can be cancelled at any time and stay active until the end of the month. Annual plans can be refunded in full within 14 days of purchase.</p><p>Duplicate or mistaken charges are always refunded. Reply to your receipt email and our billing team will take care of it.</p>`},
	{1, "Education and non-profit discounts", true, `<p>Accredited schools, universities and registered non-profits get 50% off Team and Business plans.</p><p>To apply, send proof of status from your organization's email address.</p>`},
	{2, "Set up SAML single sign-on", false, `<p>Business and Enterprise plans can require sign-in through your identity provider.</p><h2>What you need</h2><ul><li>Admin access to your identity provider</li><li>The ACS URL and Entity ID from <strong>Settings → Security</strong></li></ul><h2>Steps</h2><ol><li>Create a SAML app in your identity provider.</li><li>Paste the ACS URL and Entity ID.</li><li>Upload the IdP metadata XML to {product}.</li><li>Test with one account before enforcing SSO.</li></ol><h2>Certificate rotation</h2><p>When your identity provider rotates its signing certificate, add the new certificate in {product} <em>before</em> switching. Both stay valid during the overlap.</p>`},
	{2, "Using the API", false, `<p>Create a token in <strong>Settings → API</strong>. Send it as a bearer token:</p><pre><code>curl https://api.{site}/v1/notes \
  -H "Authorization: Bearer od_live_..."</code></pre><h2>Pagination</h2><p>Lists return a <code>next_cursor</code>. Pass it as <code>?cursor=</code> to get the next page.</p><h2>Rate limits</h2><p>10,000 requests per hour, with a burst limit of 1,200 per 10 minutes per endpoint. Check the <code>RateLimit-Remaining</code> header.</p>`},
	{2, "Webhooks", false, `<p>Webhooks send a POST request to your URL when something happens in your workspace.</p><h2>Verifying signatures</h2><p>Every request is signed. Compute an HMAC-SHA256 of <code>timestamp.body</code> with your secret, using the <strong>raw</strong> request body.</p><h2>Retries</h2><p>Failed deliveries are retried with exponential backoff for up to 24 hours.</p>`},
	{2, "Google Calendar integration", true, `<p>Connect Google Calendar in <strong>Settings → Integrations</strong> to create a meeting note for every event with two or more attendees.</p>`},
	{3, "PDF export fails for large notebooks", false, `<p>Exports run in the background. Very large notebooks, especially ones with many high-resolution images, can take several minutes.</p><h2>If the export fails</h2><ul><li>Export sections separately using <strong>Export selection</strong>.</li><li>Turn on <strong>Compress images</strong> in the export dialog.</li><li>Make sure every image has finished uploading.</li></ul><p>Still stuck? Contact us with the notebook name and we'll run the export for you.</p>`},
	{3, "Sync is paused or stuck", false, `<p>The apps pause sync when they can't reach our servers or when you're over your storage limit.</p><ol><li>Check your connection and any VPN or proxy.</li><li>Sign out and back in.</li><li>On desktop, choose <strong>Help → Reset local cache</strong>. Your notes are safe on the server.</li></ol><p>If you see duplicate notes after a conflict, the newest copy is the one with your latest edits.</p>`},
	{4, "Turn on two-factor authentication", false, `<p>Two-factor authentication asks for a code from your phone when you sign in.</p><ol><li>Open <strong>Settings → Security</strong>.</li><li>Choose <strong>Set up two-factor</strong> and scan the QR code with an authenticator app.</li><li>Enter the code it shows and save your recovery codes somewhere safe.</li></ol><p>Admins can require two-factor for everyone in the workspace.</p>`},
	{5, "Use {product} offline on your phone", false, `<p>The mobile apps keep your recent notebooks on the device, so you can read and write without a connection.</p><p>Changes sync as soon as you're back online. Pin a notebook to keep all of it available offline.</p>`},
	{6, "Read the audit log", false, `<p>The audit log records sign-ins, sharing changes and exports. Find it under <strong>Settings → Security → Audit log</strong> on the Business plan.</p><p>Filter by person or event, and export any range as CSV for your security team.</p>`},
}

type topicDef struct {
	collection int
	title      string
	summary    string
	steps      []string
}

// topics are shorter articles a larger knowledge base adds.
var topics = []topicDef{
	{0, "Organize notes with tags", "Tags group notes across notebooks.", []string{"Type # in a note and pick a tag, or create a new one.", "Click a tag in the sidebar to see every note that has it.", "Rename or merge tags under Settings, then Tags."}},
	{0, "Use templates", "Templates give recurring notes the same structure.", []string{"Open the block menu and choose Template.", "Pick one from the gallery or save the current note as a template.", "Share templates with the workspace so everyone starts from the same page."}},
	{0, "Search your notes", "Search finds words in titles, bodies and attachments.", []string{"Press ⌘ K and start typing.", "Use quotes for an exact phrase.", "Filter by notebook, tag or author with the chips under the search box."}},
	{0, "Set up your profile", "Your name and picture show on notes you edit.", []string{"Open Settings, then Profile.", "Upload a picture and set your display name.", "Choose your time zone so reminders fire on time."}},
	{0, "Create checklists", "Checklists track small tasks inside a note.", []string{"Type [] and a space to start a checklist.", "Press Tab to indent an item.", "Tick an item to mark it done."}},
	{0, "Link notes together", "Links connect related notes and show backlinks.", []string{"Type [[ and the title of another note.", "Press Enter to insert the link.", "Open the Backlinks panel to see every note that links here."}},
	{1, "Download invoices", "Every payment has an invoice you can download.", []string{"Open Settings, then Billing.", "Choose Invoices.", "Click the download icon next to any invoice."}},
	{1, "Add or remove seats", "Seats follow the members in your workspace.", []string{"Invite a member to add a seat.", "Remove a member to free their seat.", "Changes are prorated on your next invoice."}},
	{1, "Update your payment method", "Change the card or bank account we charge.", []string{"Open Settings, then Billing.", "Choose Payment method.", "Add the new card and set it as default."}},
	{1, "Pay by invoice", "Annual plans over 50 seats can pay by bank transfer.", []string{"Contact our billing team with your company details.", "We send a quote and, once accepted, an invoice.", "Pay within 30 days by bank transfer."}},
	{1, "Understand your bill", "What each line on your invoice means.", []string{"Plan lines show seats times the price per seat.", "Proration lines cover changes during the period.", "Tax lines follow your billing address."}},
	{2, "Connect Slack", "Share notes into Slack channels and get notified about comments.", []string{"Open Settings, then Integrations, then Slack.", "Authorize the workspace you use.", "Pick the channels notes can be shared to."}},
	{2, "Connect Zapier", "Automate {product} with thousands of apps.", []string{"Search for {product} in Zapier.", "Sign in with your {product} account.", "Pick a trigger such as New note."}},
	{2, "Connect Microsoft Teams", "Share notes into Teams channels.", []string{"Install the {product} app from the Teams store.", "Sign in when Teams asks.", "Use the message extension to share a note."}},
	{2, "Embed notes on a website", "Show a published note on your own site.", []string{"Publish the note from the Share menu.", "Copy the embed code.", "Paste it into your site's HTML."}},
	{2, "API tokens and scopes", "Tokens can be limited to what an integration needs.", []string{"Open Settings, then API.", "Create a token and choose its scopes.", "Store it somewhere safe: it is shown only once."}},
	{2, "Sync with Google Drive", "Attach Drive files to notes.", []string{"Open Settings, then Integrations, then Google Drive.", "Allow access to your Drive.", "Use the attach menu to pick a Drive file."}},
	{3, "Notes are missing after an import", "Most missing notes are in a different notebook.", []string{"Search for the note's title.", "Check the Imported notebook the import created.", "Run the import again: notes already imported are skipped."}},
	{3, "Images don't load", "Images may be blocked by a proxy or still uploading.", []string{"Reload the page.", "Check whether your network blocks our file domain.", "Re-upload the image if it shows a broken icon."}},
	{3, "Can't sign in", "Most sign-in problems are a locked account or a stale session.", []string{"Reset your password from the sign-in page.", "Wait 30 minutes if your account is locked.", "Ask your admin whether SSO is required."}},
	{3, "Reminders fire at the wrong time", "Reminders use the time zone in your profile.", []string{"Open Settings, then Profile.", "Check your time zone.", "Edit the reminder to save it again."}},
	{3, "The desktop app won't start", "A corrupted cache can stop the app.", []string{"Quit the app completely.", "Delete the cache folder listed in the help menu.", "Start the app again and sign in."}},
	{3, "Duplicate notes after a sync conflict", "Conflicts keep both versions so nothing is lost.", []string{"Open both copies.", "Keep the one with your latest edits.", "Delete the other copy."}},
	{3, "Web app is slow", "Large notebooks and browser extensions slow things down.", []string{"Try a private window to rule out extensions.", "Split very large notebooks.", "Update your browser."}},
	{4, "Require SSO for your workspace", "Everyone signs in through your identity provider.", []string{"Set up SAML under Settings, then Security.", "Test with one account.", "Turn on Require SSO."}},
	{4, "Where your data is stored", "Workspaces are hosted in the region chosen at sign-up.", []string{"See your region under Settings, then Workspace.", "Backups stay in the same region.", "Contact us to move to another region."}},
	{4, "Report a vulnerability", "We welcome reports from security researchers.", []string{"Email our security team with the details.", "Give us 90 days to fix it before publishing.", "We'll keep you updated on the fix."}},
	{4, "Review active sessions", "See where your account is signed in.", []string{"Open Settings, then Security.", "Look at Active sessions.", "Sign out any session you don't recognize."}},
	{4, "Request a data processing agreement", "Our DPA covers GDPR requirements.", []string{"Open Settings, then Workspace, then Legal.", "Download the DPA, already signed by us.", "Countersign it and send it back."}},
	{5, "Install the mobile apps", "{product} runs on iPhone, iPad and Android.", []string{"Download the app from the App Store or Google Play.", "Sign in with your account or SSO.", "Turn on notifications for mentions."}},
	{5, "Scan documents with your phone", "Turn paper into searchable notes.", []string{"Tap the camera button in a note.", "Frame the page and take the picture.", "Text in the scan becomes searchable within a minute."}},
	{5, "Handwriting on iPad", "Write with Apple Pencil and convert it to text.", []string{"Add a drawing block to a note.", "Write with the Pencil.", "Tap Convert to turn handwriting into text."}},
	{5, "Widgets on your home screen", "See recent notes without opening the app.", []string{"Long-press your home screen.", "Add the {product} widget.", "Pick the notebook it shows."}},
	{6, "Change a member's role", "Roles decide what someone can do.", []string{"Open Settings, then Members.", "Click the member's role.", "Pick a new role."}},
	{6, "Remove a member", "Their notes stay in the workspace.", []string{"Open Settings, then Members.", "Choose Remove next to their name.", "Reassign their private notebooks if asked."}},
	{6, "Transfer workspace ownership", "Owners can hand the workspace to another admin.", []string{"Make sure the new owner is an admin.", "Open Settings, then Workspace.", "Choose Transfer ownership and confirm."}},
	{6, "Set up domain sign-up", "Let colleagues join without an invite.", []string{"Open Settings, then Members.", "Turn on Domain sign-up.", "Add your company's email domain."}},
	{6, "Archive notebooks", "Hide old notebooks without deleting them.", []string{"Open the notebook menu.", "Choose Archive.", "Find archived notebooks at the bottom of the sidebar."}},
	{6, "Data retention", "Delete old notes automatically for compliance.", []string{"Open Settings, then Workspace, then Retention.", "Choose how long to keep notes.", "Notes older than that move to the trash once a day."}},
}

var releaseItems = []string{
	"Faster search across large workspaces", "Nested checklists", "PDF export keeps image quality", "Dark mode for the web clipper",
	"Offline mode improvements on Android", "Version history shows what changed", "Bulk move notes between notebooks",
	"Accent-insensitive search", "Read-only API tokens per notebook", "Comments for guests", "Calendar sync fixes",
	"Smoother handwriting on iPad", "New templates gallery", "Audit log export as CSV", "Keyboard shortcuts for every menu",
}

// Collab mail.

type mailPerson struct{ name, email, company string }

var mailPeople = []mailPerson{
	{"Elena Ruiz", "elena@northwind.example.invalid", "Northwind Traders"}, {"Marcus Lee", "marcus@halcyon.example.invalid", "Halcyon Studio"},
	{"Aisha Patel", "aisha@brightline.example.invalid", "Brightline Logistics"}, {"Tom Becker", "tom@oakridgehealth.example.invalid", "Oakridge Health"},
	{"Nora Kim", "nora@paperplane.example.invalid", "Paperplane"}, {"Daniel Svensson", "daniel@fjord-legal.example.invalid", "Fjord Legal"},
	{"Grace Ho", "grace@lumen-ventures.example.invalid", "Lumen Ventures"}, {"Ravi Nair", "ravi@copperleaf.example.invalid", "Copperleaf"},
	{"Sophie Laurent", "sophie@meridian.example.invalid", "Meridian Schools"}, {"Jonah Fischer", "jonah@tidewater.example.invalid", "Tidewater Media"},
	{"Lina Haddad", "lina@kestrel.example.invalid", "Kestrel Robotics"}, {"Peter Holm", "peter@solstice.example.invalid", "Solstice Bakery"},
}

// mailNotifiers send automated mail (receipts, reviews, digests).
var mailNotifiers = []mailPerson{
	{"Receipts", "receipts@payments.example.invalid", ""}, {"Code review", "noreply@code.example.invalid", ""},
	{"Tracker", "notifications@tracker.example.invalid", ""}, {"Calendar", "calendar@calendar.example.invalid", ""},
}

type mailStory struct {
	subject, body string
	automated     bool // a notification: no replies
}

var mailStories = []mailStory{
	{"Q4 renewal and seat count", "Hi there,\n\nWe are looking at the renewal for next year. Could you send over a quote for 260 seats with the annual discount?\n\nThanks,\n{first}", false},
	{"Contract redlines from {company}", "Please find our redlines below. The main change is the liability cap in section 9.\n\nBest,\n{first}", false},
	{"Intro: {product} x {company}", "Hi both,\n\nAs promised, connecting you two. You're both rolling out note-taking to field teams and should compare notes.\n\nI'll let you take it from here.\n\n{first}", false},
	{"Design review on Thursday?", "Would Thursday at 3pm work for the export flow review? I can share the mockups beforehand.\n\n{first}", false},
	{"Shipment delayed at the border", "Quick heads up: the hardware shipment is held at customs. We expect it to clear by Monday.\n\n{first}", false},
	{"Offsite agenda draft", "Here is the first draft of the offsite agenda. Add anything I missed before Friday.\n\n{first}", false},
	{"Security questionnaire from {company}", "Our compliance team sent over a questionnaire. Any chance you can fill it in by the end of the month?\n\n{first}", false},
	{"Lunch next week", "Free for lunch next Tuesday? There's a new ramen place near the office.\n\n{first}", false},
	{"Feedback on the new dashboard", "Love the new dashboard. One thing: the date picker is hard to use on a laptop trackpad.\n\n{first}", false},
	{"Partnership proposal", "We'd love to explore listing {product} in our marketplace. Do you have 30 minutes next week?\n\n{first}", false},
	{"Webinar co-hosting", "Would you be up for co-hosting a webinar on note-taking for remote teams in November?\n\n{first}", false},
	{"Pilot results at {company}", "Here are the results from our 6-week pilot. Adoption was 82% across the three teams, and the field team loved offline mode.\n\n{first}", false},
	{"Pricing for 3 campuses", "Could you confirm the per-seat price for 3 campuses on the education plan?\n\n{first}", false},
	{"Reference call", "One of our prospects would like to speak with a customer like you. Would you be open to a short call?\n\n{first}", false},
	{"Invoice question", "Our finance team spotted two charges for the same month on the last invoice. Could you take a look?\n\n{first}", false},
	{"Onboarding plan for {company}", "We're rolling out to 40 more people in January. Could we plan two training sessions with your team?\n\n{first}", false},
	{"Data export for our audit", "Our auditors need an export of every workspace change in Q3. What's the easiest way to get that?\n\n{first}", false},
	{"Quick question about SSO", "We're moving from Okta to Entra ID next month. Anything we should do on your side before the switch?\n\n{first}", false},
	{"Conference booth next spring", "We're putting together a shared booth at the spring conference. Interested in joining us?\n\n{first}", false},
	{"Case study draft", "Here's the first draft of the case study. Let me know if the numbers look right to you.\n\n{first}", false},
	{"Renewal paperwork", "Attached is the signed order form for next year. Can you send the countersigned copy?\n\n{first}", false},
	{"API access for our data team", "Our data team wants to sync notes into our warehouse nightly. Who can help us scope that?\n\n{first}", false},
	{"Holiday schedule", "Sharing our office closures over the holidays so you know when we're slow to reply.\n\n{first}", false},
	{"Thank you!", "Just wanted to say thanks for the help last week. The team is much happier with the new setup.\n\n{first}", false},
}

// mailNotices are automated mail: never replied to.
var mailNotices = []mailStory{
	{"Your receipt #{number}", "Receipt #{number}\n\nAmount paid: $1,240.00\nPayment method: card ending 4242", true},
	{"Review requested: fix sign-in redirect loop (#{number})", "A review was requested on this pull request.\n\nFixes the redirect loop when the session expires mid-flow.", true},
	{"Invoice INV-{number} overdue", "This is a friendly reminder that invoice INV-{number} is now 5 days overdue.", true},
	{"Weekly digest", "12 issues closed, 4 opened, 2 in review this week.", true},
	{"Invitation: planning sync", "You have been invited to Planning sync, Thursday 10:00 to 10:30.", true},
}

var mailReplies = []string{
	"Thanks, {first}. I'll get back to you shortly.",
	"Sounds good, let's do it.",
	"Thanks for the heads up. Keep me posted.",
	"Great, I've added it to the calendar.",
	"Thanks! Looping in the team so we can move this forward.",
}

var mailComments = []string{
	"<p>{mention} can you take this one? They asked about the annual discount.</p>",
	"<p>I'll reply to this tomorrow morning.</p>",
	"<p>{mention} do we have the signed copy somewhere?</p>",
	"<p>Looks good to me, go ahead.</p>",
}

var mailFolders = []string{"Clients", "Receipts", "Travel", "Follow up"}
