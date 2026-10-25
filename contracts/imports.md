# Imports (`internal/helpdesk/imports`)

A team moving to opendesk brings its history along: tickets with every reply and internal
note, the customers who wrote them, their companies, labels and attachments, and the help
center articles. Sources are **Zendesk**, **Help Scout**, **Freshdesk**, **Intercom** and **Front**, read
through their public APIs, and a **CSV** file for everything else. Same code on the cloud and
on a self-host; no plan gates it, and imported tickets count toward no limit.

An import runs in the background (River, `import.run`), checkpoints after every page and
resumes where it stopped after a restart, a deploy or a rate limit. Running it again never
duplicates anything: every row it writes is recorded by its source id (`import_records`).
One import runs per workspace at a time: another one that is running, paused or undoing
answers `409 import_running`.

## Flow

1. **Check** the source: `POST /v1/imports/check` signs in with the pasted credentials and
   answers what is there: the account, ticket and article counts, the source's agents and
   groups (each matched to a member by email or a team by name, case-insensitive) and up to
   three sample tickets with their messages, as they will look here. Nothing is stored.
   A CSV file is uploaded first (`POST /v1/imports/csv`); the answer is its columns, a few
   values of each, the column mapping guessed from the headers and a preview. A check of a
   CSV upload (`{source: "csv", upload_id, mapping?}`) answers an `ImportCheck` for a mapping.
2. **Start** it: `POST /v1/imports` with the same credentials (or the upload), the options
   and the mapping. It checks the source once more, answers the `Import` at once and runs in
   the background: help center articles first, then tickets.
3. **Watch** it: `import.updated` events carry the whole `Import` as it moves, at most once
   a second while it writes and after every page. When it completes, fails or needs new
   credentials, the member who started it is notified (`import_finished`).
4. **Bring over what changed** (`POST /v1/imports/{id}/sync`) while the team switches over:
   tickets updated in the source since shortly before the last run began get their new
   messages, the source's status and any new labels; new tickets and new articles are
   imported. Tickets that did not move in the source are skipped, articles already imported
   are left as they are, and a ticket deleted here stays deleted. CSV imports run once.
5. **Undo** (`POST /v1/imports/{id}/undo`) removes everything the import created: tickets
   (with every message and attachment on them; a ticket with a message no import wrote, a
   reply or note added here since, stays and is no longer counted as imported), the
   contacts that only it brought and that have no other tickets or messages, the companies
   no contact belongs to any more, its labels that no ticket, contact or company uses, its
   articles, and its collections that hold no other article. Records it matched but did not
   create (an existing contact, label, company or collection) are left alone.

## Shapes

```ts
type ImportSource = "zendesk" | "helpscout" | "freshdesk" | "intercom" | "front" | "csv";
type ImportStatus = "running" | "paused" | "completed" | "failed" | "canceled" | "undoing" | "undone";

interface Import {
  id: string; source: ImportSource;
  account: { name: string; url: string };          // "Acme Support", "https://acme.zendesk.com"; a CSV import: its filename, ""
  status: ImportStatus;
  phase: "articles" | "tickets" | "done";
  run: number;                                      // 1, then +1 per sync
  options: ImportOptions; mapping: ImportMapping;
  totals: { tickets: number | null; articles: number | null }; // what the source said it has; null = unknown or not imported
  counts: ImportCounts;                             // over all runs
  problems: ImportProblem[];                        // the latest 50, newest first
  last_error: string;                               // why it failed, paused itself or is retrying, plain words; "" otherwise
  needs_credentials: boolean;                       // the source refused the credentials: resume with new ones
  waiting_until: string | null;                     // waiting out the source's rate limit until then
  can_sync: boolean;                                // not CSV, credentials kept, and running, paused or completed
  started_by: UserRef | null;
  started_at: string /* of the latest run */; finished_at: string | null; created_at: string; updated_at: string;
}
interface ImportCounts { tickets: number; updated: number /* by a sync */; messages: number; contacts: number; companies: number;
  articles: number; attachments: number; skipped: number /* deleted or spam in the source, CSV rows left out */; failed: number }
interface ImportProblem { at: string; item: string /* "Ticket #1234", "Article Billing FAQ", "Row 18", "Label vip", "Help center" */; message: string }

interface ImportOptions {
  tickets: boolean;              // default true
  articles: boolean;             // default true; false for CSV and for an account without a help center
  attachments: boolean;          // default true; programs, files over 25 MB and files past the plan's import file limits are skipped
  since: string | null;          // only tickets created on or after this day (YYYY-MM-DD)
  mailbox_id: string | null;     // email tickets reply from this mailbox (one of the workspace's)
  tag: string;                   // label added to every imported ticket; "" = none. Default "<source>-import"
  statuses: "keep" | "closed";   // keep each ticket's status, or import everything closed. Default "keep"
  articles_as_drafts: boolean;   // import published articles as drafts to review before publishing
}
// At least one of tickets and articles. Options are fixed when the import starts.
interface ImportMapping {
  agents: Record<string, string | null>;  // source agent id -> opendesk user id; null keeps the name only.
                                          // An agent left out maps to the member with their email, if any
  groups: Record<string, string | null>;  // source group id -> team id; null or left out = no team
  csv?: CsvMapping;                       // CSV only; default the mapping guessed from the headers
}
// A CSV column name for each field; body or subject is required. Rows that share a ticket_id
// become one ticket, its messages in created_at order; without ticket_id every row is a ticket.
// A CSV import's agents are keyed by email (agent rows' from_email, assignee_email) and its
// groups by the team column's value.
interface CsvMapping { ticket_id?: string; subject?: string; body?: string; from_email?: string; from_name?: string;
  author?: string /* agent, customer or note per row (below); else customer */; status?: string; priority?: string;
  tags?: string /* separated by , ; or | */; created_at?: string; assignee_email?: string; team?: string;
  company?: string /* the requester's company, from a customer row */;
  message_id?: string; in_reply_to?: string; references?: string /* each message's email headers (below) */ }

interface ImportCheck {
  account: { name: string; url: string };
  totals: { tickets: number | null; articles: number | null };
  agents: { id: string; name: string; email: string; user_id: string | null /* matched member */ }[];
  groups: { id: string; name: string; team_id: string | null /* matched team */ }[];
  samples: ImportSample[];        // up to 3; empty when they could not be read in time (the check itself passed)
  has_articles: boolean;          // the credentials can read a help center
}
interface ImportSample { ref: string /* "#1234" */; subject: string; status: TicketStatus; created_at: string;
  requester: { name: string; email: string } | null;
  messages: { author: string; agent: boolean; note: boolean; text: string /* first 400 chars */; created_at: string; attachments: number }[]; // the first 6
  message_count: number; tags: string[] }
interface CsvUpload { upload_id: string; filename: string; rows: number; columns: { name: string; samples: string[] /* up to 3 values */ }[];
  suggested: CsvMapping; preview: ImportSample[] /* [] when the guessed mapping has no body or subject */ }
```

Credentials by source (all strings):

| source | fields |
| --- | --- |
| `zendesk` | `subdomain` (`acme`, `acme.zendesk.com` or its URL), `email` (an admin's), `token` (Admin Center → Apps and integrations → APIs → Zendesk API: token access on, Add API token). The user must be an admin: the incremental export is for admins only |
| `helpscout` | `app_id`, `app_secret` (Your Profile → My Apps → Create My App, any redirect URL), `docs_key` (optional, Docs API key for articles: Your Profile → Authentication → API Keys). A refused Docs key leaves the tickets importable |
| `freshdesk` | `domain` (`acme`, `acme.freshdesk.com` or its URL), `api_key` (profile picture → Profile settings → View API key). An admin's key also reads the account's name |
| `intercom` | `token` (Developer Hub → your app → Configure → Authentication → access token), `region` (`us` default, `eu`, `au`) |
| `front` | `token` (an admin: Settings → Developers → API Tokens → Create API token, Access resources, Shared resources with all shared workspaces, read permission). A sync reads the events feed and walks the inboxes when the token cannot. Articles are not imported |

## Endpoints

Reading imports needs an admin (`settings:read`, open to API keys with that scope). Every
other route needs an **admin user** (`settings:write`); API keys get `403`, since the flow
pastes another product's credentials.

| method | path | body → response |
| --- | --- | --- |
| POST | `/v1/imports/check` | `{source, credentials}`, or `{source: "csv", upload_id, mapping?}` → `ImportCheck`. 30 per workspace per hour |
| POST | `/v1/imports/csv` | multipart `file` (`.csv`, `.tsv` or `.txt`, at most 50 MB and 500,000 rows, a header row, UTF-8 with or without a byte order mark or UTF-16 with one, `,` `;` or tab separated) → 201 `CsvUpload`. 20 per workspace per hour. An upload no import started is deleted after 24 hours |
| GET | `/v1/imports` | → `{data: Import[]}`, the latest 100, newest first |
| GET | `/v1/imports/{id}` | → `Import` |
| POST | `/v1/imports` | `{source, credentials?, upload_id?, options?, mapping?}` → 201 `Import` |
| POST | `/v1/imports/{id}/pause` | → `Import` (finishes the ticket in hand, then stops). Only a running import |
| POST | `/v1/imports/{id}/resume` | `{credentials?}` → `Import`. A paused or failed import goes on where it stopped. New credentials must reach the same account (`422 validation_failed` otherwise); a field left empty keeps its stored value, so an expired token is all that is sent again; without stored credentials it is `422 import_auth_failed` |
| POST | `/v1/imports/{id}/cancel` | → `Import`. A running or paused import stops for good and its credentials and CSV file go. What is already imported stays; undo removes it |
| POST | `/v1/imports/{id}/sync` | → `Import` (`run` + 1). `409 conflict` unless completed, not CSV, with its credentials |
| POST | `/v1/imports/{id}/undo` | → `Import` (`undoing`, then `undone`). Not while running (pause or cancel first) or once undone. Its credentials go at once |
| DELETE | `/v1/imports/{id}/credentials` | → `Import` (`can_sync` false). Not while running. Credentials also go on cancel and undo, and 30 days after the import last ran |

Errors: `422 import_auth_failed` (the source refused the credentials; the detail may say why, as
"sign in as a Zendesk admin"), `422 import_not_found` (no such account), `422 validation_failed`
(a credential missing or malformed carries `errors: [{location: "body.credentials.<field>", message}]`;
also options and mappings), `502 import_unreachable` (the source did not answer as expected),
`429 import_rate_limited` (the source's own limit; the detail says how many seconds to wait),
`429 rate_limited` (our limit on checks or uploads), `409 import_running` (another import is
running, paused or undoing), `409 conflict` (the state does not allow it). Uploads: `413 too_large`
(over 50 MB), `422 file_type_refused` (not a CSV file), `422 csv_unreadable` (the detail says why:
the encoding, a line that cannot be read, more than 500,000 rows, no rows under the header),
`404 not_found` (an `upload_id` that is gone: used by an import or older than 24 hours).

## What is brought over

| opendesk | Zendesk | Help Scout | Freshdesk | Intercom | Front |
| --- | --- | --- | --- | --- | --- |
| ticket | ticket | conversation (email, chat, phone) | ticket | conversation | conversation |
| reply / note | public / private comment | customer and reply threads / note and forward threads | description and conversations, `private` = note | source and parts / note parts | messages / comments |
| contact | requester | primary customer | requester | first contact (user or lead) | recipient (contact) |
| company | requester's organization | none | ticket's company | contact's first company | none |
| assignee | assignee | assignee | responder | admin assignee | assignee |
| team | group | mailbox | group | team assignee | inbox |
| labels | tags | tags | tags | tags | tags |
| CSAT | satisfaction good 5, bad 1 | rating great 5, okay 3, not good 1 | none | conversation rating 1..5 | none |
| article, collection | Guide article, section | Docs article, collection (with `docs_key`) | solution article, folder | article, collection | none |

- Statuses: Zendesk `new`/`open` → open, `pending` → pending, `hold` → on_hold, `solved` → solved,
  `closed` → closed; Help Scout `active` → open, `pending` → pending, `closed` → closed; Freshdesk 2 → open,
  3 → pending, 4 → solved, 5 → closed, custom statuses → pending; Intercom `open` → open, `snoozed` → on_hold,
  `closed` → closed; Front `archived` → closed (on_hold with a reminder set), `snoozed` → on_hold, else by
  ticket status category: `open` → open, `waiting` → pending, `resolved` → solved; CSV by name (below), closed without a status. A status opendesk does not know is closed;
  `statuses: "closed"` closes them all. Solved and closed tickets keep when they were solved.
- Skipped (`counts.skipped`): Zendesk deleted tickets; Help Scout spam, deleted and draft conversations;
  Freshdesk spam and deleted tickets; Front spam, deleted and trashed conversations, discussions and tasks, and conversations merged into another one.
- Priorities map to low, normal, high, urgent: Zendesk as they are (none is normal); Freshdesk 1 low,
  2 normal, 3 high, 4 urgent; Intercom `priority` is high, else normal; Help Scout and Front normal.
- Channels by how the ticket arrived: email, chat, api (phone calls, API and messages the team started)
  or portal (web forms, the help center, Help Scout Beacon); CSV tickets are email.
- Messages keep their author, time, recipients and HTML (sanitized like every email body). A message
  by an agent mapped to a member is that member's; an unmapped agent's keeps their name and address.
  Intercom bot replies (Fin, workflows) are the team's, named Fin. At most 2,000 messages per ticket
  (Intercom answers at most 500 parts of a conversation); the rest is listed in `problems`. An email
  reply's quoted history and signature are cut the way an inbound email's are, with the whole original
  kept behind it (`full_html`); notes, chats and forwards keep everything. Front drafts
  are left out; a Front conversation that is not email (chat, SMS, social) is tagged `front-<channel>`.
  Front's read-receipt pixel is dropped and images it proxied get their original link back. Front emails in
  conversations last active within 180 days carry `message_id`, `in_reply_to` and `references`, read from
  `GET /messages/{id}` as `message/rfc822` (headers only).
- Attachments are copied here (25 MB per file, programs refused); inline images point at the copies.
  The plan bounds how many files one message brings, their size in all, and what imports bring into a
  workspace in all (contracts/billing.md "Imports"); a self-hosted instance has none of these limits.
  Files past the first ones are left out, and so is each file that would take the message or the
  workspace past its size (a smaller one after it still comes); a declared size that cannot fit is not
  downloaded. Each limit a message reaches is one entry in `problems` with how many files it left out;
  the workspace's limit is listed once.
- Every file an import copies is recorded (`import_records` kind `file`) as soon as it is stored, so a
  ticket stopped while its files copy (a rate limit, the run's time) goes on with the files it has;
  files no ticket took are removed after a day like any unattached upload. Copied files are marked
  `attachments.imported` and count against the workspace's limit while they are kept. Article HTML is
  sanitized as it is; its images stay where the source hosts them.
- Requesters become contacts, found by email (an existing contact is matched and only gets a name
  or phone it lacks) or created with their name and phone; companies are found by domain or name, or created.
- NUL bytes (U+0000) are dropped from every text a ticket or article saves (subjects, bodies, names,
  addresses, tags, file names, titles), since Postgres cannot store them. Source ids are kept as they are.
- Agents are not invited and teams are not created: they map to existing members and teams.
  Automations, macros and saved replies, SLA policies, views, custom fields and their values, business
  hours, side conversations and the source's settings are not imported.
- Articles keep their title, body, collection and dates; published ones are published unless
  `articles_as_drafts`, drafts stay drafts.
- Each ticket keeps where it came from in `custom.import`: `{source, id, ref, url, import_id}`; the
  ticket page shows "Imported from Zendesk #1234" linking to it. Tickets get new opendesk numbers,
  their source dates (created, last message, solved, closed) and the import's label.
- Imported history is written quietly: no emails, automations, SLA targets, auto-replies, AI triage,
  notifications, webhooks or per-ticket realtime events (`domain.NewTicket.Imported`,
  `domain.NewMessage.Imported`, `domain.ImportContactByEmail`). Lists refresh from `import.updated`.
- A ticket or article that cannot be read or written is listed in `problems` and counted in `failed`;
  the import goes on. A label past the workspace's limit is left out and listed.
- When the source asks to slow down, the import sets `waiting_until` (the source's wait, at most 15
  minutes) and goes on by itself. The source refusing the credentials pauses the import with
  `needs_credentials`. The source not answering is retried with a growing wait (`last_error` says so);
  five failures in a row, any other error of the source, or the account gone fail it with `last_error`.
  A failed import can be resumed.
- Freshdesk serves at most 300 pages of a listing: the import moves through tickets by their updated
  time, so accounts past 30,000 tickets come over whole.

### CSV

- Headers are matched to fields case-insensitively, with spaces, `_`, `-` and `.` alike; a column is used
  for one field only. The names each field is guessed from, in order:

  | field | headers |
  | --- | --- |
  | `ticket_id` | ticket id, ticket, ticket number, ticket no, conversation id, conversation, thread id, case id, case number, id, number, # |
  | `subject` | subject, title, summary, topic, ticket subject |
  | `body` | body, message, description, content, text, comment, html, message body, body html, body text, reply |
  | `from_email` | from email, email, from, sender email, sender, requester email, customer email, contact email, author email, user email, e mail |
  | `from_name` | from name, name, sender name, requester name, customer name, contact name, author name, author, requester, customer, user name |
  | `author` | author type, sender type, from type, direction, role, type, message type, is agent, visibility |
  | `status` | status, state, ticket status |
  | `priority` | priority, urgency, importance |
  | `tags` | tags, labels, tag, label, categories |
  | `created_at` | created at, created, date, created date, timestamp, time, sent at, sent, date created, message date, received at |
  | `assignee_email` | assignee email, assignee, agent email, agent, assigned to, owner, owner email, responder |
  | `team` | team, group, department, queue, inbox, mailbox |
  | `company` | company, organization, organisation, org, account, company name |
  | `message_id` | message id, rfc message id, email message id, internet message id, rfc 822 message id, msg id |
  | `in_reply_to` | in reply to, in reply to id, in reply to message id, parent message id |
  | `references` | references, email references, references header |

- `author`: agent, staff, admin, support, operator, teammate, team, outbound, out, outgoing, reply, public reply,
  sent, employee, true, yes, 1 → an agent's reply; note, internal, private, internal note, private note → a note;
  anything else → the customer.
- `status`: open (new, active, in progress, assigned, reopened, unresolved, unassigned, todo), pending (waiting on
  customer, awaiting customer, awaiting reply), on_hold (on hold, hold, waiting, snoozed, waiting on third party,
  paused, blocked), solved (resolved, done, fixed, completed, answered); anything else closed.
  `priority`: low (p4, minor, lowest, 1), high (p2, important, major, 3), urgent (p1, critical, highest, blocker,
  emergency, 4); anything else normal.
- `created_at`: ISO 8601 (`2024-03-01T14:05:00Z`, `2024-03-01 14:05`, `2024-03-01`), `2024/03/01`, US
  `03/01/2024` (month first, 24-hour or AM/PM), `01.03.2024` (day first), `Mar 1, 2024 2:05 PM`, `1 Mar 2024`,
  `March 1, 2024`, email dates (RFC 1123), or unix seconds or milliseconds. Without a zone a time is UTC. A
  value that cannot be read, before 1990 or in the future is no date; the message is then dated when imported.
- `message_id`, `in_reply_to`, `references`: the email headers of each message, as a header writes them
  (`<id@host>`, several separated by spaces) or bare (`id@host`, several separated by spaces, commas or
  semicolons). A value without an `@` is not a Message-ID and is left out; `references` keeps the latest 50.
  They are stored on the message (`email_message_id`, `in_reply_to`, `references`), so a reply to imported
  history threads into its ticket like a reply to any other (contracts/api.md "Inbound email"), and a
  mailbox's first sync skips an email an imported message already holds. A Message-ID another message of
  the workspace already holds (the live email, or an earlier row) stays with that one.
- A body with HTML tags is HTML, else text. A ticket's subject is its first subject, else the start of its first
  message; its requester is the first customer row with an email or a name. Rows with neither body nor subject
  are left out and listed as problems (`Row <line>`). Blank rows are skipped.

## Security

- Credentials are sealed with `d.Box`, never returned, logged or audited. They are deleted when the
  user removes them, on cancel and undo, and by an hourly sweep 30 days after the import last ran.
- Every request goes to the source's own API host over https (`<sub>.zendesk.com`,
  `api.helpscout.net`, `docsapi.helpscout.net`, `<domain>.freshdesk.com`, `api.intercom.io`,
  `api.eu.intercom.io`, `api.au.intercom.io`, `api2.frontapp.com` and `<company>.api.frontapp.com`,
  which Front's next-page links use; requests to Front are spaced at most 60 a minute); a next-page link off that host is refused. Attachment
  downloads follow the outbound URL rules (https, public addresses, at most three redirects, each
  re-checked) and send the source's credentials only to its API host.
- A subdomain or domain is letters, digits and hyphens.
- A CSV upload is stored like an attachment, read only within its workspace, and deleted after 24 hours
  unless an import started from it. The import's file is deleted when it completes, is canceled or
  undone, or by the sweep 30 days after the import last ran; until then a paused or failed import
  keeps it to resume. Deleting the workspace deletes both.
- Audit: `import.started`, `import.synced`, `import.canceled`, `import.undone`,
  `import.credentials_removed` (source and account name only).

## Events

| type | data |
| --- | --- |
| `import.created` / `import.updated` | `{import: Import}` (never credentials) |

Visibility `agents`, topic `workspace:<wid>`. `import.updated` also tells clients that tickets, contacts
and articles changed in bulk: refetch lists.

Notification `import_finished` (to who started it, group `workspace`, admins only): `{import_id, source,
source_name, account, status: "completed" | "failed" | "needs_credentials", tickets, updated, articles,
failed, error}`. Canceling and undoing notify no one.
