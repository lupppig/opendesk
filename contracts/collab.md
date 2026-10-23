# Collab: shared and private mail accounts

Collab turns opendesk into a team mail client. Each member connects their own email account
(Google, Microsoft or any IMAP server) and reads and handles it in opendesk like a real mailbox:
every folder and label, read and unread, stars, archive, move, trash and spam, replies and new
mail from the address, with each change made at the provider too. An account is **private**
(only the person who connected it sees it) or **shared** (with the whole workspace, or with teams
and people). Any single conversation can be shared on its own, assigned and discussed in
comments, so a teammate can help with one email without seeing the rest of the account. Each
member arranges their own sidebar.

Collab is the `collab` plan's (contracts/billing.md "Collab"). It is not a ticket feature: a
mail account is not a helpdesk mailbox (contracts/api.md "Mailboxes"), its mail never becomes
tickets, and nothing about tickets or the existing plans changes. What it adds to helpdesk
mailboxes is a way to receive: reading the inbox directly with its OAuth clients, or generic IMAP
("Providers and scopes").

## Who has it

`plan.Entitlements.Collab` (`d.Plan.Entitlements`) decides:

| instance | Collab |
| --- | --- |
| billing off (self-hosted) | on for every workspace while the instance has `COLLAB` on (default on) |
| billing on (opendesk Cloud) | on while the workspace is on the `collab` plan (a paid subscription), or has complimentary Collab access from an instance admin that has not ended (contracts/admin.md "Collab access") |

Without it every `/v1/collab/*` route except `GET /v1/collab` answers `402 collab_required`
("Collab is not on this workspace's plan."). Accounts connected while it was on are kept as they
are and stop syncing; nothing is deleted. Turning it back on resumes them.

## Providers and scopes

| provider | connects with | reads and changes mail through | sends through |
| --- | --- | --- | --- |
| `google` | OAuth: `openid email profile https://www.googleapis.com/auth/gmail.modify https://www.googleapis.com/auth/gmail.settings.basic` | Gmail API (labels, history) | Gmail API `messages.send` |
| `microsoft` | OAuth: `openid email offline_access https://graph.microsoft.com/Mail.ReadWrite https://graph.microsoft.com/Mail.Send https://graph.microsoft.com/User.Read https://graph.microsoft.com/MailboxSettings.ReadWrite` | Microsoft Graph (mail folders, delta) | Graph `sendMail` (MIME) |
| `imap` | address, IMAP and SMTP servers and a password (an app password where the host needs one) | IMAP (UID, flags, MOVE or COPY + EXPUNGE, CONDSTORE when offered) | SMTP, then APPEND to the Sent folder |

- `gmail.modify` reads, labels, archives, trashes and sends; it cannot delete mail for good, and
  Collab never does on any provider (trash is emptied by the provider's own rules).
- `profile` gives the Google account's name, the From name a new account starts with; when Google
  has none, the address's Gmail send-as name is used. Microsoft's comes from `/me` (`User.Read`).
- The settings scopes are the account's own settings ("Account settings"): `gmail.settings.basic`
  changes the Gmail signature, From name and vacation responder (`gmail.modify` only reads them);
  `MailboxSettings.ReadWrite` reads and changes Outlook's automatic replies and categories. A person
  may leave them out of the consent (Google), and accounts connected before they were asked for do
  not have them: such an account syncs its mail as usual and its settings are read only (Gmail) or
  not synced (Outlook automatic replies) until it is reconnected.
- The scopes the provider granted are recorded on the account (`collab_accounts.config.scopes`)
  from the code exchange and every refresh (the token response's `scope`). A Microsoft refresh asks
  for the Graph scopes the account was granted, never more (asking for more refuses the refresh).
- OAuth uses PKCE (S256), a signed `oauth_state` token and a browser-bound cookie, like mailbox
  OAuth. Redirect URIs: `<API_URL>/v1/collab/accounts/oauth/google/callback` and
  `<API_URL>/v1/collab/accounts/oauth/microsoft/callback`; register them with the OAuth clients.
- Clients: `COLLAB_GOOGLE_CLIENT_ID`/`COLLAB_GOOGLE_CLIENT_SECRET` and
  `COLLAB_MICROSOFT_CLIENT_ID`/`COLLAB_MICROSOFT_CLIENT_SECRET`, each defaulting to the mailbox
  client (`GOOGLE_MAILBOX_CLIENT_ID`, `MICROSOFT_MAILBOX_CLIENT_ID`, which default to the sign-in
  clients). A provider is offered when its client is configured. `COLLAB_IMAP` (default on)
  offers IMAP accounts. IMAP and SMTP hosts go through the same network guard as mailboxes:
  public addresses only, TLS 1.2+ verified, except on a development instance.
- Refresh tokens, access tokens and passwords are sealed with `d.Box`; the API never returns them.
  Disconnecting revokes the Google token (best effort) and removes the credentials.
- Support mailboxes use the Collab clients too: a helpdesk mailbox of a workspace with Collab can
  connect Google or Microsoft to read its inbox directly through the Gmail API or Graph
  (`oauth_grant: "api"`, contracts/api.md "Mailboxes"), with the mail scopes (not the settings ones), so its mail becomes
  tickets without forwarding. That mailbox is still a helpdesk mailbox, not a mail account here:
  its OAuth redirect is the mailbox one (`<API_URL>/v1/mailboxes/oauth/{google,microsoft}/callback`),
  which must be registered on these clients as well when they differ from the mailbox clients.
  It stays the same mailbox there: replies join the customer's Gmail or Outlook conversation and
  show in Sent, and it can mark read, label `opendesk` and archive its emails at the provider as its
  settings say (contracts/api.md "Reading the inbox directly"), but it never syncs folders, drafts or
  settings the way a Collab account does.
  With Collab a workspace's mailboxes may also receive over generic IMAP while `MAILBOX_IMAP` is
  off, as long as `COLLAB_IMAP` is on.

## Sync

A River job per account (`collab.sync_account`, queue `email`) runs when it is due: every minute
while any member with access was active in the last 30 minutes, else every 10 minutes, and at once
after a connect, a reconnect, `POST .../sync` or an action. One runs per account at a time.

- **First sync**: the folders and labels (at most 200), then the newest mail: the last 30 days,
  at most 500 messages per account, inbox and sent first. Older mail is not imported.
- **Then**: changes only. Gmail: `history.list` from the stored history id (a history id older
  than Gmail keeps means a first sync again). Microsoft: a delta link per folder. IMAP: per folder,
  new UIDs above the stored one and the flags of known ones (`CHANGEDSINCE` with CONDSTORE), with
  UIDVALIDITY resets handled as a first sync of that folder; at most 30 folders are synced
  (the special ones first).
- Changes made at the provider (read elsewhere, moved on the phone, deleted) arrive with the next
  sync. A message gone from every folder is removed from opendesk.
- Failures are stored on the account (`status: "error"`, a safe `last_error`), retried with
  backoff (1 minute doubling to 1 hour). A refused grant (`invalid_grant`, an IMAP login refused)
  needs a reconnect: `status: "error"`, `reconnect: true`.
- Each message is parsed with the email area's parser. Its HTML is sanitized with bluemonday before
  storage (the same policy as inbound email), and it renders in the sandboxed frame without scripts
  with remote images on request (`web/src/lib/sanitize.ts`). A message with attachments or inline
  images keeps its raw MIME in storage (`collab/<workspace>/<account>/<message>.eml`, deleted with
  the message) and its files are read from it. Messages over 30 MB are skipped.

## Conversations

Messages group into conversations (`MailThread`): Gmail threads, Microsoft `conversationId`, and
for IMAP the first message its `References` / `In-Reply-To` name that the account already has, else
a new conversation. A conversation's state is its messages': unread when any is unread, starred when
any is starred, in a folder when any message is.

### Actions

`POST /v1/collab/threads/actions` applies to whole conversations, at once in opendesk (published
right away) and at the provider by the job `collab.apply` (`jobs.CollabApplyArgs`), retried with
backoff. If the provider refuses for good, the next sync brings back the provider's state.

| action | Gmail | Microsoft | IMAP |
| --- | --- | --- | --- |
| `read` / `unread` | remove / add `UNREAD` | `isRead` | `\Seen` |
| `star` / `unstar` | add / remove `STARRED` | `flag.flagStatus` `flagged` / `notFlagged` | `\Flagged` |
| `archive` | remove `INBOX` | move to Archive | move to the Archive folder (`\Archive`, else `\All` is not used: a folder named Archive is created when missing) |
| `inbox` | add `INBOX` (and remove `TRASH`, `SPAM`) | move to Inbox | move to INBOX |
| `trash` | `threads.trash` | move to Deleted Items | move to Trash (`\Trash`) |
| `spam` / `not_spam` | add `SPAM` / remove `SPAM` and add `INBOX` | move to Junk Email / Inbox | move to Junk (`\Junk`) / INBOX |
| `move` (`folder_id`) | add the label, remove `INBOX` when the target is a label | move | move |
| `label` / `unlabel` (`folder_id`, a label) | add / remove the label | add / remove the category (the message's `categories` PATCHed) | `422 labels_unsupported` |

A `move` to an Outlook category is `422 validation_error` (a category is a label: add it with
`label`). Moving a Microsoft or IMAP conversation keeps its categories.

Opening a conversation does not mark it read; the web app sends `read` when it is shown (and the
user can turn that off in the sidebar settings, `mark_read_on_open`).

### Drafts

Drafts are kept at the provider and stored like synced mail: `draft: true`, in the Drafts folder,
a reply in its conversation, a new message or a forward in a conversation of its own.

| | Gmail | Microsoft | IMAP |
| --- | --- | --- | --- |
| save | `users.drafts.create` with the MIME (and the `threadId` of a reply) | `POST /me/messages` with the MIME | `APPEND` to Drafts with `\Draft \Seen` |
| save again | `users.drafts.update` (a new message id, the same draft) | `PATCH` subject, body and recipients when no files are involved, else a new draft from the MIME and the old one deleted | `APPEND` the new version, then `\Deleted` and `UID EXPUNGE` (`EXPUNGE` without UIDPLUS) of the old |
| discard | `users.drafts.delete` | `DELETE` the message | `\Deleted` and expunge |
| send | `users.drafts.send` | `POST /messages/{id}/send` (the id stays in Sent Items) | SMTP, `APPEND` to Sent, then the draft expunged |

- A draft keeps its opendesk id and its Message-ID across saves, and answers what it answered (its
  `In-Reply-To` and `References`). Its HTML is sanitized like sent mail, and the images it shows
  from stored messages (its own, the quoted original's) go as `cid:` parts, as when sending.
- Drafts made in Gmail, Outlook or another client arrive with the normal sync (Gmail's `DRAFT`
  label, Graph `isDraft`, IMAP `\Draft`) and open the same way: `draft_id` to save, send or
  discard them, `in_reply_to_id` for the message a reply draft answers. A Gmail draft opendesk did
  not make is found by its Message-ID.
- A draft saved, sent or discarded elsewhere meanwhile: saving again makes a new draft of what was
  written; sending or discarding it is `502` or done, and the next sync shows the provider's state.
- The web composer saves a draft about two seconds after typing stops and when it closes, and
  sends the draft when there is one.
- The composer puts the account's signature in as one block, `<div class="od-signature">` around
  `signature_html` exactly as stored: drawn as designed (tables, inline styles, sized images), never
  edited inline, removed whole, and swapped for the new account's when From changes. The stored email
  policy keeps the class, so a saved draft opens with its signature intact; a Gmail draft's own
  `gmail_signature` opens as a block too.
- Mail sent over SMTP never carries a `Bcc` header (Bcc is in the envelope only); the copy in Sent
  and drafts keep it.

### Labels and colours

- **Outlook categories are labels** (`kind: "label"`, remote id `category:<name>`): the account's
  master list (`/me/outlook/masterCategories`, which needs `MailboxSettings.ReadWrite`, part of the
  Collab Microsoft scopes) with each preset colour as a hex background, plus any category a message
  carries that the list lacks, kept while a message carries it. Messages' `categories` map to them
  on the first sync and with every delta. An account connected before the scope was asked for
  cannot read the list: its categories are the ones seen on messages, without colours, until it is
  connected again. Deleting a category label removes it from the master list (Outlook keeps the
  name on messages); categories cannot be renamed at Microsoft.
- **Colours** are opendesk tones (`gray`, `red`, `orange`, `amber`, `green`, `teal`, `blue`,
  `indigo`, `violet`, `pink`), set at the provider first and synced back with the next folder
  refresh. Gmail accepts only its own palette, so each tone is a Gmail background and text pair;
  Outlook takes a preset:

| tone | Gmail background / text | Outlook preset (hex) |
| --- | --- | --- |
| gray | `#999999` / `#ffffff` | `preset12` (`#a0aeb2`) |
| red | `#fb4c2f` / `#ffffff` | `preset0` (`#e74856`) |
| orange | `#ffad47` / `#ffffff` | `preset1` (`#f7630c`) |
| amber | `#fad165` / `#000000` | `preset3` (`#ffb900`) |
| green | `#16a766` / `#ffffff` | `preset4` (`#13a10e`) |
| teal | `#2da2bb` / `#ffffff` | `preset5` (`#00b294`) |
| blue | `#4a86e8` / `#ffffff` | `preset7` (`#0078d4`) |
| indigo | `#285bac` / `#ffffff` | `preset22` (`#004e8c`) |
| violet | `#a479e2` / `#ffffff` | `preset8` (`#8764b8`) |
| pink | `#f691b3` / `#ffffff` | `preset9` (`#c30052`) |

## Who sees what

- A **private** account and its conversations are visible to the member who connected it, and
  nobody else, admins included. A **shared** account to that member and its audience:
  `everyone` (every member of the workspace) or the listed teams (their members) and people.
- A conversation is also visible to whoever it is shared with on its own (`shares`: everyone,
  teams, people) and to its assignees.
- Viewers read; acting (actions, sending, sharing, assigning, comments) needs agent or above and
  sight of the conversation. Managing an account (rename, share, signature, settings, disconnect)
  is its owner's; for a shared account, workspace admins too.
- Anything not visible is `404`. Every row is read and written with `workspace_id` from the
  Principal; a referenced team, user, folder or attachment is checked to belong to the workspace.
- A member removed from the workspace, or a deleted user, loses their accounts: private accounts
  are deleted with their mail (the job `collab.cleanup`), shared ones are kept with `owner: null`
  for admins to manage or disconnect.
- API keys and AI clients over MCP have no access to Collab: every route needs a user session.

## Shapes

```ts
interface Audience { everyone: boolean; team_ids: string[]; user_ids: string[] }
interface Address { name: string; email: string }

interface MailAccount {
  id: string
  provider: "google" | "microsoft" | "imap"
  email: string
  name: string                 // how the sidebar names it; the address by default
  display_name: string         // the From name of its mail, as written: plain words or a format naming the sender ("Addresses")
  color: string                // a tone key: gray, red, orange, amber, green, teal, blue, indigo, violet, pink
  owner: UserRef | null        // who connected it; null after they left (shared accounts only)
  visibility: "private" | "shared"
  shared_with: Audience        // empty when private
  signature_html: string       // sanitized; added to new mail and replies by the composer
  status: "syncing" | "active" | "error" | "paused"   // paused: Collab is off for the workspace
  reconnect: boolean           // the grant or password was refused: connect it again
  last_error: string
  last_synced_at: string | null
  can_manage: boolean
  unread: number               // unread conversations in its inbox
  imap?: { imap_host: string; imap_port: number; imap_tls: "tls" | "starttls"; imap_username: string;
           smtp_host: string; smtp_port: number; smtp_tls: "tls" | "starttls"; smtp_username: string }  // imap accounts, can_manage only
  created_at: string
  // How its own settings sync with the provider ("Account settings"): two_way, read_only or none
  signature_sync: SettingSync          // google: two_way with gmail.settings.basic, else read_only; microsoft, imap: none
  signature_synced_at: string | null   // the last time the signature came from, or went to, Gmail
  display_name_sync: SettingSync       // google: like the signature; microsoft: read_only until a person changes it here, then none; imap: none
  out_of_office: OutOfOffice | null    // null for IMAP, without MailboxSettings, and until first read
  out_of_office_sync: SettingSync      // google: like the signature; microsoft: two_way with MailboxSettings.ReadWrite, else none; imap: none
  identities: MailIdentity[]   // the addresses it sends from, its own first ("Addresses")
  default_from: string         // the address new mail goes out from, lower case
}

interface MailIdentity {
  id: string | null            // null for the account's own address
  email: string                // lower case
  display_name: string         // the From name; "" uses the account's display_name
  signature_html: string       // sanitized with the signature allowlist
  reply_to: string             // replies go here when set ("" none)
  primary: boolean             // the account's own address: its From name and signature are the account's
  default: boolean             // new mail goes out from it (exactly one)
  source: "account" | "provider" | "manual"   // provider: Gmail's send-as list; manual: added by hand (Microsoft, IMAP)
  verification: "accepted" | "pending"         // pending: Gmail waits for the address's owner to confirm it
  can_send: boolean            // false while pending
  signature_sync: SettingSync | "pull"  // account: the account's; provider: pull; manual: none
  display_name_sync: SettingSync | "pull"
  signature_synced_at: string | null
  display_name_local: boolean  // Gmail: the From name has variables, so it is kept in opendesk and Gmail keeps its own
  signature_local: boolean     // Gmail: the signature has variables, the same
  from_name: string            // the From name (its own, else the account's) filled for the caller
  filled_signature_html: string | null  // the signature filled for the caller; null when it has no variables
}

type SettingSync = "two_way" | "read_only" | "none"
// An address's settings can also be "pull": read from the provider when they change there, a
// change made in opendesk kept in opendesk (Gmail's send-as addresses other than the primary).

interface OutOfOffice {        // the automatic reply as the provider keeps it
  enabled: boolean
  start: string | null         // replies only to mail received from then on
  end: string | null           // and until then
  subject: string              // always "" for Microsoft (Outlook's replies have no subject of their own)
  message_html: string         // sanitized with the signature allowlist
  contacts_only: boolean       // only people in the account's contacts
  internal_only: boolean       // only people in the account's own organization
}

interface MailFolder {
  id: string
  account_id: string
  name: string                 // the last part of the path
  path: string                 // "Clients/Acme"
  role: "inbox" | "sent" | "drafts" | "archive" | "trash" | "spam" | "starred" | "important" | "all" | null
  kind: "folder" | "label"     // Gmail's labels and Outlook categories are labels; everything else is a folder
  unread: number               // unread conversations
  total: number                // conversations
  color: string | null         // a label's colour (Gmail label, Outlook category), a hex background
  can_rename: boolean          // user folders and labels; never the special ones
}

interface MailThread {
  id: string
  account_id: string
  subject: string
  snippet: string
  participants: Address[]      // senders and recipients, newest first, at most 10, the account's own address last
  last_message_at: string
  message_count: number
  unread: boolean
  starred: boolean
  has_attachments: boolean
  folder_ids: string[]
  shared: boolean              // shared on its own beyond its account's audience
  assignees: UserRef[]
  comment_count: number
  to_address: string | null    // the account's address other than its own the conversation was written to or sent from (an alias), newest first
}

interface MailMessage {
  id: string
  thread_id: string
  account_id: string
  message_id: string           // the RFC 5322 Message-ID, without angle brackets
  from: Address
  to: Address[]
  cc: Address[]
  bcc: Address[]               // only on mail sent from the account
  reply_to: Address[]
  subject: string
  snippet: string
  body_html: string            // sanitized; inline images point at their attachment URL
  body_text: string
  sent_at: string
  unread: boolean
  starred: boolean
  draft: boolean
  folder_ids: string[]
  attachments: MailAttachment[]
  sent_by: UserRef | null      // the teammate who sent it from opendesk (for a draft, who saved it)
  draft_id: string | null      // drafts: the id the /v1/collab/drafts routes take (the message's id)
  in_reply_to_id: string | null  // the message of the conversation it answers (its In-Reply-To), when stored
  own_address: string | null   // the account's address (its own or an alias) it was sent from, else the first in its To, Cc, then Delivered-To / X-Original-To
}

interface MailAttachment {
  id: string                   // its index in the message, "0", "1", ...
  filename: string
  content_type: string
  size: number
  inline: boolean
  content_id: string
  url: string                  // signed, valid for an hour, works in <img> and as a download link
}

interface MailComment {
  id: string
  thread_id: string
  author: UserRef | null
  body_html: string            // sanitized like a note
  mentions: string[]           // user ids
  created_at: string
  updated_at: string
}

interface MailThreadDetail {
  thread: MailThread
  account: { id: string; email: string; name: string; display_name: string; color: string; provider: string; signature_html: string;
             identities: MailIdentity[]; default_from: string }
  messages: MailMessage[]      // oldest first
  comments: MailComment[]      // oldest first
  shares: Audience             // the conversation's own shares
  can_act: boolean
}

interface CollabStatus {
  enabled: boolean
  reason: "plan" | "disabled" | null   // why not: not on the plan, or off on this instance
  providers: { google: boolean; microsoft: boolean; imap: boolean }
  accounts: number             // accounts the caller sees
}

interface SidebarPrefs {
  order: string[]              // section ids, top to bottom: "favorites", "inbox", "mail", "teams", "views", "workspace" (the default)
  hidden: string[]             // "section:<id>", "box:<box>", "account:<id>", "folder:<id>", "folder:<inbox|starred|...>", "view:<id>", "team:<id>", "ticket_folder:<id>"
  pinned: { kind: "box" | "folder" | "account" | "view" | "team" | "ticket_folder"; id: string }[]  // shown under Favorites, in order, at most 30
  expanded_accounts: string[]  // account ids whose folders show in the sidebar; folded by default (the mail page has every folder)
  mark_read_on_open: boolean   // default true
  unified_inbox: boolean       // default true: "All inboxes" on top of the Mail section
  badges: Record<string, "unread" | "total" | "count" | "awaiting" | "urgent" | "overdue" | "open" | "none">  // the number beside a row; default {}
  mail_tabs: string[]          // the mail page's folder bar, in order; default ["inbox","starred","sent","drafts","archive"]
}
```

`badges` keys are a row (`box:inbox` for All inboxes, `box:shared`, `box:assigned`, `account:<id>`,
`folder:<id>`, `ticket_folder:<id>`, `view:<id>`, `team:<id>`) or a section's default (`section:mail`,
`section:inbox`, `section:teams`, `section:views`, `section:favorites`). A row without its own value
follows its section's, and without either shows what it always has: unread conversations for mail rows,
the ticket count for ticket rows. For mail rows `unread` is unread conversations, `total` every
conversation in that inbox or folder (All inboxes: the sum of the shown accounts' inbox totals; Shared
with me and Assigned to me: their counts), `none` no number. For ticket rows (inbox folders, teams, views)
`count` is the usual number (a team's unassigned tickets), `awaiting`, `urgent` and `overdue` the row's
`stats` from `GET /v1/tickets/counts` (contracts/api.md), `open` a team's open count (team rows only), and
`none` hides it. A row reads a value it has no number for (`total` on a team, `open` on a view, `awaiting`
on a mail row) as its default. The folder bar's tabs follow the same keys (`box:<box>`, `folder:<id>`): Inbox shows
unread by default, every other tab no number.

`mail_tabs` lists box names (`inbox`, `unread`, `starred`, `sent`, `drafts`, `archive`, `spam`, `trash`,
`all`) and `folder:<id>` (a folder or label of an account). A box tab shows in every scope, except for one
account whose provider lacks that folder; a `folder:<id>` tab shows while its account is in scope (that
account, one of its folders open, or the only account). Everything else stays in More.

## Message details

`GET /v1/collab/messages/{id}/details` reads how a message came from its source, as a mail app's
"show details" does:

```ts
interface MessageDetails {
  mailed_by: string            // the envelope sender's domain (SPF's smtp.mailfrom, else Return-Path)
  signed_by: string[]          // domains whose DKIM signature the receiving server verified
  via: string                  // the domain it came through when neither of those is the sender's own
  spf: string; dkim: string; dmarc: string   // the receiving server's results; "" when it reported none
  encryption: "tls" | "none" | ""            // the hop into the receiving server (its Received line)
  warning: "spoofed" | "unverified" | ""     // spoofed: DMARC failed; unverified: neither SPF nor DKIM passed
  headers: { name: string; value: string }[] // in order, unfolded, decoded; at most 300
}
```

Only the receiving server's own `Authentication-Results` counts: the topmost one above a `Received`
line (a header the sender wrote sits below all of them), and on Gmail only one from `mx.google.com`.
Without one, the checks and the warning are empty. Mail the account sent has no checks, and only it
keeps its `Bcc` header. The app shows the warning above the message and `via` beside the sender.

## Endpoints

Every route needs a user session (`auth.KindUser`) and membership. "see": the caller sees the
account or conversation; "act": sees it and is agent or above; "manage": "Who sees what".

| method | path | who | notes |
| --- | --- | --- | --- |
| GET | `/v1/collab` | member | `CollabStatus`; the only route without Collab |
| GET | `/v1/collab/accounts` | member | `MailAccount[]` the caller sees, theirs first. `?manage=true`: an admin also gets the shared accounts of people who left (`owner: null`), to manage or disconnect |
| POST | `/v1/collab/accounts/oauth/{provider}/start` | agent+ | `{visibility, shared_with? (at most 40 teams and people: it travels in the signed state; add more afterwards), name?, email? (login hint), account_id? (reconnect, manage)}` → `{url}`; sets the flow cookie (send with credentials). `422 provider_not_configured` |
| GET | `/v1/collab/accounts/oauth/{provider}/callback` | provider | redirects to `APP_URL/settings/mail-accounts?connected=<id>` or `?error=<code>`: `oauth_state`, `access_denied`, `provider_error`, `account_conflict` (the address is already connected in this workspace by someone else), `collab_required`, `forbidden`, `internal` |
| POST | `/v1/collab/accounts/imap` | agent+ | `{email, name?, display_name?, imap_host, imap_port, imap_tls, imap_username?, password, smtp_host, smtp_port, smtp_tls, smtp_username?, smtp_password?, visibility, shared_with?}` → `MailAccount`. Both servers are tried first: `422 connection_failed` with `detail`. `409 account_conflict`. `422 imap_unavailable` with `COLLAB_IMAP` off |
| PATCH | `/v1/collab/accounts/{id}` | manage | `{name?, display_name?, color?, signature_html?, visibility?, shared_with?, imap? {…same fields, password?, smtp_password?}, default_from?}` → `MailAccount`; `default_from` must be one of its addresses that can send (`422 validation_failed`) and is kept in opendesk (Gmail's own change of its default still comes in, "Addresses"); new IMAP settings are tried first. A changed `signature_html` or `display_name` that syncs `two_way` is made at Gmail first (sanitized): a refusal is `502 provider_error` and nothing of the change is saved. `read_only` and `none` save in opendesk only. A changed `display_name` stops a Microsoft account following its profile name. A `display_name` or `signature_html` with variables ("Who sends") is checked (`422 validation_failed`) and saved in opendesk only, never at Gmail |
| POST | `/v1/collab/accounts/{id}/identities` | manage | Add an address ("Addresses"): `{email, display_name?, signature_html?, reply_to?}` → `MailAccount`. Microsoft and IMAP only (`422 identities_from_provider` for Gmail); `409 identity_conflict` for the account's own or an existing address; at most 50 |
| PATCH | `/v1/collab/accounts/{id}/identities/{identity_id}` | manage | `{display_name?, signature_html?, reply_to? (hand-added only)}` → `MailAccount`. Saved in opendesk; a Gmail address's From name and signature are replaced by Gmail's when they next change there (`pull`) |
| POST | `/v1/collab/accounts/{id}/identities/preview` | manage | `{email?, display_name, signature_html?}` → `{from_name, from, signature_html, from_name_problem, problems: TemplateProblem[]}`: what is typed, filled for the caller and checked, nothing saved ("Who sends"). `email` is the address (default the account's own); an empty `display_name` uses the account's |
| POST | `/v1/collab/accounts/{id}/identities/{identity_id}/display-name/pull` | manage | → `MailAccount` with the address's From name replaced by Gmail's now (empty uses the account's). `422 display_name_sync_unsupported` for a hand-added address |
| POST | `/v1/collab/accounts/{id}/identities/{identity_id}/signature/pull` | manage | → `MailAccount` with the address's signature replaced by Gmail's now (`GET sendAs/{email}`). `422 signature_sync_unsupported` for a hand-added address; `502 provider_error` when Gmail refuses |
| DELETE | `/v1/collab/accounts/{id}/identities/{identity_id}` | manage | → `MailAccount`. Hand-added only (`422 identities_from_provider` for Gmail's); the default for new mail goes back to the account's own address when it was this one |
| GET | `/v1/collab/accounts/{id}/identities/suggestions` | manage | `{addresses: string[]}`: Microsoft's `proxyAddresses` aliases not added yet, when Graph answers; empty otherwise (and for other providers) |
| POST | `/v1/collab/accounts/{id}/signature/pull` | manage | → `MailAccount` with the signature replaced by Gmail's now. `422 signature_sync_unsupported` for Microsoft and IMAP; `502 provider_error` when Gmail refuses |
| POST | `/v1/collab/accounts/{id}/display-name/pull` | manage | → `MailAccount` with the From name replaced by Gmail's now ("Get from Gmail", in place of one with variables). `422 display_name_sync_unsupported` for Microsoft and IMAP; `422 validation_failed` when Gmail has no name of its own; `502 provider_error` |
| PUT | `/v1/collab/accounts/{id}/out-of-office` | manage | `OutOfOffice` → `MailAccount`. Written to the provider first (`502 provider_error`, nothing saved, when it refuses), then stored as the provider answered it. `422 out_of_office_read_only` (`read_only`), `422 out_of_office_unsupported` (`none`), `422` when the end is not after the start, or (Microsoft) when enabled with only one of start and end |
| DELETE | `/v1/collab/accounts/{id}` | manage | → 204. Removes the account and its mail from opendesk; nothing is deleted at the provider |
| POST | `/v1/collab/accounts/{id}/sync` | see | → 202, a sync now |
| GET | `/v1/collab/accounts/{id}/folders` | see | `MailFolder[]`, special ones first, then by path |
| POST | `/v1/collab/accounts/{id}/folders` | act | `{name: 1..100, parent_id?, kind?: "folder" \| "label", color?: tone}` → `MailFolder`, created at the provider first (`502 provider_error` with detail). Gmail makes labels either way; Microsoft makes a folder, or with `kind: "label"` an Outlook category (no `parent_id`); IMAP only folders (`422 labels_unsupported` for a label). `color` only for labels ("Labels and colours") |
| PATCH | `/v1/collab/folders/{id}` | act | `{name?, color?: tone}`, at least one → `MailFolder`; at the provider first, then stored. `422` for a special folder; `color` only on a Gmail label or an Outlook category (`422 validation_error` otherwise); an Outlook category cannot be renamed (`422 rename_unsupported`) |
| DELETE | `/v1/collab/folders/{id}` | act | → 204; its conversations stay in their other folders; `422` for a special folder |
| GET | `/v1/collab/threads` | member | `?box=&account_id=&folder_id=&q=&to=&limit=(1..100, 50)&cursor=` → `Page<MailThread>`, newest first. `to`: only conversations with that address on a message (From, To, Cc or where it was delivered), case-insensitive: one alias's mail. `box`: `inbox` (default), `unread`, `starred`, `sent`, `drafts`, `archive`, `trash`, `spam`, `all`, `shared`, `assigned`. Without `account_id` it spans every account the caller sees (for `shared` and `assigned`, every conversation they see). `folder_id` replaces `box`. `unread` and `all` leave out trash and spam, `starred` leaves out trash, and `shared` (conversations with shares of their own) and `assigned` leave out both. `archive` is the Archive folder, and for Gmail, which archives by removing `INBOX`, what is in none of inbox, trash, spam and drafts. `q` matches subject, participants, snippet and body text |
| GET | `/v1/collab/counts` | member | `{boxes: {inbox, unread, shared, assigned, drafts}: number, accounts: Record<id, number>, folders: Record<id, number>}`: unread conversations; `shared` and `assigned` count those still in an inbox, `drafts` every draft conversation; `folders` lists only folders with unread conversations (missing = 0) |
| GET | `/v1/collab/threads/{id}` | see | `MailThreadDetail` |
| POST | `/v1/collab/threads/actions` | act | `{thread_ids: string[1..100], action, folder_id?}` → `{threads: MailThread[]}`. Threads the caller cannot act on are `404` (all or nothing) |
| PUT | `/v1/collab/threads/{id}/shares` | act | `{shares: Audience}` → `MailThreadDetail`. Empty unshares. Audit `collab.thread_shared` (ids only) |
| PUT | `/v1/collab/threads/{id}/assignees` | act | `{user_ids: string[0..20]}` → `MailThreadDetail`; each must be a member (agent+); assigning lets them see it. Each new assignee gets a `collab_assigned` notification |
| POST | `/v1/collab/threads/{id}/comments` | act | `{body_html: 1..20000, mentions?: string[]}` → `MailComment`; each mentioned member who can see it gets a `collab_mention` notification; a mention of someone who cannot see it shares the conversation with them |
| PATCH | `/v1/collab/comments/{id}` | its author | `{body_html}` → `MailComment` |
| DELETE | `/v1/collab/comments/{id}` | its author, or an admin | → 204 |
| POST | `/v1/collab/messages` | act on the account | Send: `{account_id, mode: "new" \| "reply" \| "reply_all" \| "forward", message_id? (the `MailMessage.id` replied to or forwarded; required unless new), to: string[], cc?: string[], bcc?: string[], subject, body_html, attachment_ids?: string[] (POST /v1/attachments uploads of the caller), forward_attachments?: boolean, from_email?}` → `MailMessage`. `from_email` is the address it goes out from and must be one of the account's addresses that can send (`422 validation_failed`); left out, a reply or forward goes from the address the original was written to, new mail from the default ("Addresses"). At most 100 recipients, 25 MB of files. Sent through the provider within the request (`502 send_failed` with the provider's reason, nothing stored); then stored in the conversation (a reply stays in its conversation) and in Sent. The server sends `body_html` as written: the composer adds the signature and, for a forward, the original. Someone who sees only one conversation (shared or assigned) may reply to it or forward it; new mail needs sight of the account. At most 60 a minute per person (`429 rate_limited`) |
| POST | `/v1/collab/drafts` | act on the account | Save a new draft ("Drafts"): the body of `POST /v1/collab/messages`, recipients optional → `MailMessage` (`draft: true`, `draft_id`). Saved at the provider first (`502 provider_error` with detail, nothing stored). At most 120 saves a minute per person (`429 rate_limited`) |
| PUT | `/v1/collab/drafts/{id}` | act on its conversation | Save again: `{to, cc?, bcc?, subject, body_html, attachment_ids?, keep_attachments?: string[], from_email?}` (left out keeps the draft's From when it can still send) → `MailMessage`, the same id. `keep_attachments`: the draft's own files to keep (`MailAttachment.id`s); left out keeps every file that is not an inline image, and inline images stay while `body_html` shows them. `404` for a message that is not a draft. Counts toward the 120 a minute |
| DELETE | `/v1/collab/drafts/{id}` | act on its conversation | Discard → 204: deleted at the provider, then here (a new-mail draft's conversation with it) |
| POST | `/v1/collab/drafts/{id}/send` | act on its conversation | Send it; the body (as for `PUT`) is optional: when given the draft is saved with it first, else it goes as saved (composed again from what was saved when its From name names the sender, so it goes out with the sender's name). Needs a recipient. → `MailMessage`, the sent message, stored like `POST /v1/collab/messages` does (in the draft's conversation and in Sent) with the draft removed. `502 send_failed`; counts toward the 60 sends a minute |
| GET | `/v1/collab/messages/{id}/details` | see | `MessageDetails` ("Message details"), read from the message's source (stored, else the provider: `502 provider_error`) |
| GET | `/v1/collab/attachments/{message_id}/{index}` | signed (`exp`, `sig`) | the file, `Content-Disposition: attachment` unless an image; `?inline=1` for images. `404` when the signature is wrong or expired |
| GET | `/v1/collab/sidebar` | member | `SidebarPrefs` (defaults when never saved) |
| PUT | `/v1/collab/sidebar` | member | `SidebarPrefs` → `SidebarPrefs`; at most 16 KB; unknown ids are kept (an id of something gone is ignored by the app). `badges`: at most 300, keys of at most 100 characters with the prefixes above, values `unread`, `total`, `count`, `awaiting`, `urgent`, `overdue`, `open` or `none`; `mail_tabs`: at most 30, each a box above or `folder:<uuid>`, once. Leaving out `badges` or `mail_tabs` keeps the saved value; `422` otherwise |

Errors: `402 collab_required`, `404 not_found`, `422 labels_unsupported`, `422 connection_failed`,
`409 account_conflict`, `502 provider_error`, `502 send_failed`, `422 provider_not_configured`,
`422 imap_unavailable`, `422 rename_unsupported`, `422 drafts_unsupported`, `422 signature_sync_unsupported`,
`422 out_of_office_read_only`, `422 out_of_office_unsupported`, `422 identities_from_provider`, `409 identity_conflict`,
`422 display_name_sync_unsupported`.

## Account settings

An account's own settings sync with its provider where the provider shares them: the signature,
the From name and the automatic reply (out of office). `MailAccount.*_sync` says how each does.

| setting | Google | Microsoft | IMAP |
| --- | --- | --- | --- |
| signature | two-way with Gmail's sendAs `signature` (read only without `gmail.settings.basic`) | opendesk only: Graph has no signature API | opendesk only |
| From name | two-way with sendAs `displayName` (read only without the scope; an empty Gmail name changes nothing) | read from the profile (`/me` `displayName`) at connect and on each read, until a person changes it in opendesk | opendesk only |
| automatic reply | two-way with `users/me/settings/vacation` (read only without the scope) | two-way with `/me/mailboxSettings` `automaticRepliesSetting` (needs `MailboxSettings.ReadWrite`; none without it) | none |
| other addresses ("Addresses") | Gmail's send-as list; each one's From name and signature `pull`: Gmail's changes come in, edits stay in opendesk | added by hand, opendesk only | added by hand, opendesk only |
| default address for new mail | Gmail's `isDefault` read when it changes there; a choice here stays in opendesk | opendesk only | opendesk only |

The account's settings are read from the send-as list (`users.settings.sendAs`, one call): its own entry
is the account address's, else the primary one; the others are its other addresses. A write of the
account's own goes to `sendAs/{address}` (when Gmail does not know the address as one, the matching
entry of the list, else the primary one).

**The provider wins.** The account sync reads the settings on its first pass and then at most every
15 minutes (one sendAs list and one vacation read for Gmail; the profile and the automatic replies for
Microsoft), after the mail. Each value is compared with a hash of the value last seen at the provider:
a different hash replaces opendesk's copy (the signature and the automatic reply's message sanitized
with the signature allowlist, the name through `displayname.Clean`), stores the hash and publishes
`collab.accounts`; the same hash changes nothing. A write from opendesk stores the hash of the
provider's answer, so what opendesk wrote (in the provider's own form) is never pulled back as a
change. The first read keeps a signature written in opendesk when Gmail has none. A failed read is
logged with its reason only and never fails the sync. A reconnect reads the settings again on the
next sync.

**A value with variables stays in opendesk.** A From name or signature that names the sender
("Who sends") means nothing to Gmail, so it is never written there: saving one for a `two_way`
account changes it in opendesk only, and Gmail keeps its own (`MailIdentity.display_name_local`,
`signature_local`; the app says "Uses variables, so it is kept in opendesk; Gmail keeps its own"). A
plain field saved with it still goes to Gmail. It is the person's choice, so Gmail's later changes do
not replace it (the sync still stores their hash), for the account's own address and for Gmail's other
addresses alike; "Get from Gmail" (`…/signature/pull`, `…/display-name/pull`) brings Gmail's back, and
from then on the setting syncs as before. A literal `{agent}` or `{{ … }}` never reaches Gmail.

A signature is never changed by being opened: the settings page edits one the rich editor cannot
hold (tables, inline styles, sized images, divs) as HTML with a live preview, and sends
`signature_html` in the PATCH only when the person changed it, so opening and saving other settings
never writes a signature back to Gmail.

**Automatic reply mapping.** Neither provider can reply only to people outside the organization,
so the shared restriction is the other way round: `internal_only` replies only inside it.

| `OutOfOffice` | Gmail `VacationSettings` | Graph `automaticRepliesSetting` |
| --- | --- | --- |
| `enabled` | `enableAutoReply` | `status`: `disabled`, else `alwaysEnabled`, or `scheduled` with both times |
| `start`, `end` | `startTime`, `endTime` (epoch ms), either may be absent | `scheduledStartDateTime`, `scheduledEndDateTime`, written in UTC; read only while `scheduled` (other time zones converted), and Outlook needs both |
| `subject` | `responseSubject` | none (`""`) |
| `message_html` | `responseBodyHtml` (`responseBodyPlainText` when there is no HTML) | `internalReplyMessage` and `externalReplyMessage`, the same message for both (read: the internal one, else the external one) |
| `contacts_only` | `restrictToContacts` | `externalAudience: contactsOnly` (outside the organization only contacts get it; people inside always do) |
| `internal_only` | `restrictToDomain` (Google Workspace only; Gmail refuses it for other accounts) | `externalAudience: none` (wins over `contacts_only`) |
| neither | both `false` | `externalAudience: all` |

The web app shows a small Away pill beside an account in the sidebar while its automatic reply is
on (enabled, and inside its dates when it has them).

## Addresses

An account sends from its own address and its other addresses (aliases), `MailAccount.identities`.
Its own address (`primary`) is the account's: its From name and signature are `display_name` and
`signature_html`. The others are stored per account (`collab_identities`):

- **Gmail**: every send-as address Gmail lists for the account, read with its settings ("Account
  settings", the same send-as list call, at most every 15 minutes). An address Gmail no longer lists
  goes; a new one appears. Each one's From name and signature are `pull` (`signature_sync`,
  `display_name_sync`): read from Gmail when they change there (the hashes of Gmail's values as last
  seen decide, so Gmail's change wins over an edit made here), and a change made in opendesk is kept
  in opendesk and used by its composer. Gmail lets addresses other than the primary one be changed
  only by service accounts with domain-wide authority, so opendesk never writes them to Gmail (no
  `sendAs/{email}` PATCH, nothing refused). `POST …/identities/{identity_id}/signature/pull` puts
  Gmail's signature back now ("Get from Gmail"). Signatures are sanitized with the signature allowlist
  and names through `displayname.Clean`; an empty Gmail name uses the account's From name. Its
  reply-to (`replyToAddress`) is read only. The default for new mail is the same: Gmail's default
  (`isDefault`) is read when it changes there, and one chosen here is kept in opendesk (`isDefault`
  is a setting of the address made default, so Gmail would refuse it for any but the primary). An
  address whose `verificationStatus` is `pending` shows but cannot send until its owner confirms it
  from the link Gmail emailed. Addresses are added and removed in Gmail only.
- **Microsoft**: Graph lists no send-as addresses on the Collab scopes, so aliases are added by hand
  (address, From name, reply-to, signature; opendesk only). Sending from one sets the MIME `From`;
  Exchange sends it only where the organization allows sending from aliases (SendFromAlias) and the
  address is the mailbox's, else it refuses (`502 send_failed`, "…has to allow sending from aliases…",
  `ErrorSendAsDenied`) or may send from the primary address instead. The add form suggests the
  mailbox's `proxyAddresses` (`/me?$select=proxyAddresses`) when Graph shares them (work and school
  accounts; personal accounts have none; a refusal means no suggestions).
- **IMAP**: aliases added by hand; the SMTP envelope and `From` use the alias, and the SMTP server
  decides whether it accepts it (`502 send_failed` with its reason).

Sending (`POST /v1/collab/messages`, drafts): the message goes out from `from_email`, which must be
one of the account's addresses that can send (`422 validation_failed` otherwise). Left out: a draft
saved again keeps its own From, a reply or forward goes from the address the original was sent from
or written to (`MailMessage.own_address`: its From, else To, Cc, then `Delivered-To`, `X-Original-To`,
`X-Forwarded-To`), else from the default. The From name is the address's (else the account's), its
reply-to becomes the `Reply-To` header, and the Message-ID takes the address's domain. Gmail's
`threadId` is set exactly as before.

The composer: From lists every account the person can send from with each of its addresses that can
send (`Name <email>`). New mail starts from the default, a reply, reply all or forward from the
address the answered message was written to. The signature follows the chosen address: it is the
composer's signature block, swapped for the new address's on a change of From (left out when the
person removed it). Reply all leaves out every one of the account's addresses. A `collab.accounts`
event refreshes the account's open conversations too, so an open reply picks up a changed address
or signature.

### Who sends

A From name and a signature can name the member who sends the mail, so a shared support@ reads
"Maya Chen at Margin Support" with Maya's own title and phone, and her teammate's mail their own.
Both are stored as written and filled for the member writing (`internal/collab/personal.go`):

- **From names** are plain words or a format with the helpdesk's vocabulary (`internal/fromname`,
  contracts/teams.md "What the customer sees"): `{agent}` the sender's full name, `{agent_first}` its
  first word, `{mailbox}` the account's `name` (the address when unnamed), `{workspace}` the workspace's
  name. The same checks as a mailbox's `from_name_format`: up to 80 characters, only those variables, no
  `<`, `>` or control characters (`422 validation_failed`), and the same rule without a sender: the
  agent variable and the words up to the next variable are dropped, a format left with no variable
  reads `{mailbox}`. An address's empty From name uses the account's, format included.
- **Signatures** take the template variables that mean something without a ticket (contracts/templates.md,
  kind `collab_signature`): `agent.*` (the sender's name, first and last name, title, phone, pronouns,
  picture, and `agent.signature`, their personal signature), `mailbox.email` (the address it goes out
  from), `mailbox.name` (its From name, filled), `workspace.name` and `date.today`. Others are refused
  (`422 validation_failed`, "…has nothing to read here"). Tags are kept through the signature allowlist.
- **Filled for the caller**: `MailIdentity.from_name` and `filled_signature_html` in every account and
  conversation response, which the composer shows and puts in its signature block; the settings page
  previews what is typed with `POST …/identities/preview`.
- **The sender at send time**: the From name is filled again when the mail goes out, for the member
  sending it, whoever saved the draft; a draft sent as saved is composed again for its sender when its
  From name has variables. The signature is the one in the message: a composer that opens a saved draft
  whose address's signature has variables puts the opener's own in its signature block.

Reading: `MailThread.to_address` names the alias a conversation was written to or sent from (a small
chip in the list and the conversation), and `GET /v1/collab/threads?to=` shows one address's mail
(the mail page offers it for an account with more than one address).

## Events

Collab events carry ids only and reach only the members who see what changed: they are
published with `events.Event.Users` (contracts/events.md "Scoped to people"), so the workspace
channel gets the `event.hidden` stand-in, replay hides them from everyone else, and they never go to
webhooks.

| type | data | to |
| --- | --- | --- |
| `collab.changed` | `{account_id: string \| null, thread_ids: string[], folders: boolean}`: conversations changed (synced, acted on, sent, shared, assigned, commented); `folders` when folders or counts changed. Refetch what is shown | who sees the account, plus who sees the conversations |
| `collab.accounts` | `{account_id}`: an account was connected, changed, removed, or changed status | its owner and audience, before and after |

## Audit

`collab.account_connected` (`{provider, visibility}`), `collab.account_updated` (the changed field
names; `signature_html` with `from_provider` for a pull, `out_of_office` for the automatic reply,
`default_from` for the default address; an address added, changed or removed is `fields: ["identities"]`
with its `identity_id` and `added`, `removed` or the changed `identity_fields`),
`collab.account_disconnected`, `collab.thread_shared` (`{thread_id, everyone, team_ids,
user_ids}`), `collab.thread_assigned` (`{thread_id, user_ids}`). Never an address book entry,
subject, body or credential.

## Notifications

`collab_assigned` (`{thread_id, account_id, by, subject}`) and `collab_mention` (`{thread_id, comment_id, by, subject, preview}`), group `mail`:
in-app and push like other notifications, emailed by the member's notification preferences
(on by default). Their text names who assigned or mentioned and the conversation's subject.

## Data

- `collab_accounts`, `collab_folders`, `collab_threads`, `collab_messages`, `collab_message_folders`,
  `collab_shares`, `collab_assignees`, `collab_comments`, `collab_sidebar_prefs`
  (`migrations/000636_collab.up.sql`), `collab_identities` (`migrations/000654_collab_identities.up.sql`,
  with `collab_threads.addresses`: every address on its messages, lower case, newest first, at most 100,
  and `collab_messages.delivered_to`). Everything is the workspace's and is deleted with it.
- `collab_accounts.config` (not secret) holds the granted `scopes` and, under `settings`, the settings
  sync state: the hashes last seen at the provider, when they were read, whether a person chose the
  From name, the automatic reply, the default address for new mail (and the hash of Gmail's) and the
  account address's reply-to. Each `collab_identities` row keeps the hashes of its own address's
  Gmail values (`sync_state`). It is written with `jsonb_set` under the row lock.
- Deleting an account deletes its folders, conversations, messages, comments, shares and stored raw
  messages (`collab.cleanup` removes the storage objects).
