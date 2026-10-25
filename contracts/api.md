# REST API (`api.opendesk.sh/v1`)

The web app uses exactly this public API. The Go server (huma v2) generates the
OpenAPI 3.1 spec at `GET /v1/openapi.json`; that spec is the source of truth once
it exists, and this file is the contract the parts are built against.

## Conventions

- JSON, `snake_case`, timestamps RFC 3339 UTC, ids are UUID strings.
- Errors: RFC 9457 problem+json `{ "status", "title", "detail", "code"?, "errors"? }`.
  `code` is a stable machine string (`invalid_credentials`, `mfa_required`, `forbidden`,
  `not_found`, `conflict`, `rate_limited`, `validation_failed`). A suspended workspace answers
  `403 workspace_suspended` on every workspace route, and a suspended account `403 account_suspended`
  to every sign-in (contracts/admin.md).
- Responses carry no `$schema` member, so a client can send an object it read back as a PATCH
  body; request bodies refuse unknown members (`422 validation_failed`).
- Lists: `?limit=` (default 50, max 100) and `?cursor=`; response
  `{ "data": [...], "next_cursor": "..." | null }`.
- Workspace-scoped routes need `OD-Workspace: <id>` (agents) or an API key. An `OD-Workspace` the caller is not a member of (or that no longer exists) is treated as absent: `400 workspace_required`, the same answer either way.
- PATCH bodies: an omitted or `null` member is left unchanged. Clear an optional reference with `""`
  (mailbox `team_id`, KB article `collection_id`) unless the route documents `null` (ticket PATCH, contact `company_id`).
- Idempotency: `Idempotency-Key` header (1..255 printable ASCII) on authenticated POST routes, kept 24h per
  workspace and principal. A retry with the same key, route and body replays the stored response with
  `Idempotent-Replayed: true`; a different body or route is `422 idempotency_key_reused`, a retry while the first
  is running `409 idempotency_in_progress`. `5xx`, `429`, streamed and >1 MiB responses are not stored. Not
  applied to `/v1/auth/*`, `/v1/me/password`, `/v1/me/passkeys/register/*`, `/v1/realtime/token`, inbound, Stripe, widget and public routes.
- Rate limits: `RateLimit-Limit`, `RateLimit-Remaining`, `RateLimit-Reset` headers.
- Roles: `owner` > `admin` > `agent` > `viewer`. Viewers read only. Settings routes
  (mailboxes, members, api keys, webhooks, automations, sla, business hours, assignment, triage, widget settings, labels, label groups, custom fields, workspace images) need `admin`.
- API key scopes: `tickets:read`, `tickets:write`, `contacts:read`, `contacts:write`,
  `kb:read`, `kb:write`, `settings:read`, `settings:write`, `realtime`.

## Resource shapes

```ts
// Pictures (see "Profile pictures and logos"). avatar_url is null when there is no picture: show initials.
type AvatarSource = "upload" | "oauth" | "gravatar" | "domain" | "widget";
type User = { id: string; email: string; name: string; avatar_url: string | null; avatar_source: AvatarSource | null; email_verified: boolean; totp_enabled: boolean;
  time_format: "auto" | "12h" | "24h" /* the clock times are shown in, in the app and in notification emails; "auto" (the default) follows the browser's language and region in the app and is 24-hour in emails */; created_at: string };
type UserRef = { id: string; name: string; email: string; avatar_url: string | null };
type Workspace = { id: string; name: string; slug: string; widget_key: string /* the default brand's first widget */; settings: WorkspaceSettings; avatar_url: string | null /* workspace logo */; avatar_source: "upload" | null;
  images: { help_center_logo: string | null; help_center_favicon: string | null /* the default brand's */; widget_avatar: string | null; widget_launcher: string | null /* the default brand's first widget's */; /* the AI agent's picture is its bot's (contracts/bots.md) */ }; created_at: string };
// settings.help_center_enabled, help_center_title, brand_color and locale are kept in step with the default brand
// (contracts/teams.md): a PATCH here updates it, and a PATCH of the default brand's help center updates them.
type WorkspaceSettings = { brand_color?: string; timezone?: string; help_center_enabled?: boolean; help_center_title?: string; csat_enabled?: boolean;
  auto_assign?: AssignMode /* the workspace default; see "Assignment" */; new_ticket_notify?: NewTicketNotify /* read here, set through /v1/assignment; see "Assignment" */; locale?: string /* BCP 47 default for customer-facing pages (help center, CSAT, widget); "" = the visitor's browser */; locale_fixed?: boolean /* default false; true: those pages always use locale (a brand's own, else the workspace's), whatever the visitor's browser: the help center renders only it, without a language menu or ?lang= */;
  gravatar?: boolean /* default false: look up contact pictures on Gravatar (sends a hash of the contact's email to Gravatar) */;
  domain_logos?: boolean /* default true: fetch company logos from the company's website */;
  label_creation?: "anyone" | "admins" /* default "anyone": applying an unknown label name creates it; "admins": only admins (and keys with settings:write) may, others get 422 unknown_label */;
  message_delete?: "off" | "own" | "all" /* default "own": which messages agents may delete: "own" their own internal notes, "all" any agent's notes and replies, "off" none (admins any message), unless an agent has their own rule (Member.message_delete); see DELETE /v1/tickets/{id}/messages/{messageId} */;
  collab_ticket_replies?: boolean /* default true: with Collab, members may answer email tickets from their own mail accounts (contracts/collab.md "Answering tickets") */;
  collab_ticket_reply_to?: "address" | "mailbox" /* default "address": where answers to such a reply go when it names no Reply-To: the address's own reply-to (else the address), or the ticket's reply address */;
  undo_send_seconds?: 0 | 5 | 10 | 20 | 30 /* default 5: the app holds an agent's reply on an email (or any other non-chat) ticket this long before it POSTs it, with Undo (back into the composer) and Turn into internal note; nothing reaches the server until then, so the API itself never delays; 0 sends at once */;
  undo_send_chat_seconds?: 0 | 5 | 10 | 20 | 30 /* default 0: the same for replies on chat tickets, which go at once unless set */;
  status_labels?: Partial<Record<TicketStatus, DisplayOverride>>; priority_labels?: Partial<Record<Priority, DisplayOverride>> /* display only; the API values never change */ };
type DisplayOverride = { label?: string /* 1..40, no < > */; color?: LabelColor };
type Membership = { workspace: Workspace; role: Role; suspended: boolean /* an instance admin suspended it: its routes answer 403 workspace_suspended (contracts/admin.md) */ };
type Role = "owner" | "admin" | "agent" | "viewer";
type Member = { user: UserRef & { avatar_source: AvatarSource | null }; role: Role;
  availability: Availability /* what the member set */; presence: Availability /* availability, or "away" when auto-away applies */;
  max_open_tickets: number | null /* auto-assignment cap on open + pending tickets; null = no cap */; open_tickets: number;
  message_delete: "off" | "own" | "all" | null /* an agent's own delete rule over settings.message_delete (an admin may grant "all", replies included); null follows the workspace */;
  profile: MemberProfile /* title, phone, pronouns, personal signature (contracts/templates.md) */;
  last_seen_at: string | null; created_at: string };
type Availability = "online" | "away" | "offline";
type Invitation = { id: string; email: string; role: Role; expires_at: string; created_at: string; invited_by: UserRef | null };
type Team = { id: string; name: string; member_ids: string[]; created_at: string }; // full shape, brands, widgets, routing and private teams: contracts/teams.md

type Contact = { id: string; email: string | null; name: string; phone: string; avatar_url: string | null; avatar_source: AvatarSource | null; company: CompanyRef | null; external_id: string | null; notes: string; custom: Record<string, unknown>; tags: TagRef[]; fields: FieldValues; blocked: boolean; ticket_count: number; created_at: string; updated_at: string };
type ContactRef = { id: string; email: string | null; name: string; avatar_url: string | null };
type Company = { id: string; name: string; domain: string; avatar_url: string | null /* logo */; avatar_source: "upload" | "domain" | null; notes: string; custom: Record<string, unknown>; tags: TagRef[]; fields: FieldValues; contact_count: number; created_at: string; updated_at: string };
type CompanyRef = { id: string; name: string; domain: string; avatar_url: string | null };

type TicketStatus = "open" | "pending" | "on_hold" | "solved" | "closed";
type Priority = "low" | "normal" | "high" | "urgent";
type Severity = "sev1" | "sev2" | "sev3" | "sev4"; // business impact, sev1 the most severe; a ticket may have none
type Channel = "email" | "chat" | "api" | "portal";
type Ticket = {
  id: string; number: number; subject: string; status: TicketStatus; priority: Priority; severity: Severity | null; channel: Channel;
  mailbox_id: string | null; contact: ContactRef | null; assignee: UserRef | null; team_id: string | null;
  tags: TagRef[]; fields: FieldValues; sla: { policy_id: string; first_response_due_at: string | null; resolution_due_at: string | null; breached: boolean } | null;
  first_responded_at: string | null; solved_at: string | null; snoozed_until: string | null;
  last_message_at: string; last_customer_message_at: string | null; waiting_since: string | null /* the customer's first unanswered public message; null once anyone (or the AI agent) replied */; unanswered_count: number /* the customer's public messages since the last outbound public reply; 0 once replied */; last_message_preview: string; message_count: number;
  spam: boolean; csat: { score: number; comment: string } | null; custom: Record<string, unknown>;
  created_at: string; updated_at: string;
};
type Message = {
  id: string; ticket_id: string; kind: "reply" | "note"; direction: "inbound" | "outbound";
  author: { type: "contact" | "user" | "system" | "api"; user: UserRef | null; contact: ContactRef | null;
    bot: { id: string; name: string; avatar_url: string; look: BotLook } | null /* the bot an AI reply was sent as (contracts/bots.md) */ };
  body_html: string; body_text: string; has_quoted: boolean /* an original to open: an inbound email's full text, or the copy of an email we sent as it went out */; subject: string;
  from_email: string; from_name: string; to: string[]; cc: string[]; bcc: string[];
  attachments: Attachment[]; delivery_status: "none" | "pending" | "sent" | "failed" | "bounced"; delivery_error: string;
  ai: boolean; // a reply from the widget AI agent (contracts/agent.md)
  ai_action: AIAction | null; // an action the agent asks the visitor to confirm (contracts/agent.md "Visitor actions"); public
  ai_rating: "up" | "down" | null; // the visitor's rating of an agent reply (contracts/agent.md "Ratings")
  reply_to: string; // inbound email: the Reply-To it named when that differs from from_email, else ""
  auth_failed: boolean; // inbound email: its From failed DMARC at a trusted receiving server ("Inbound email"); shown as unverified
  form_sender: string; // inbound email: the trusted contact form address it came through (from_email is then the visitor), else ""
  received_at: string; // inbound email: the address of ours it was written to (its mailbox's address or an alias, lowercased; contracts/teams.md "Routing"), else ""
  // reply_to, auth_failed, form_sender and received_at are for agents: the widget and public events always carry "", false, "" and "".
  thread_id: string | null; // the third-party thread a note belongs to ("Threads"), else null; public views always carry null
  from_account: { account_id: string; email: string; reply_to: string } | null; // an email reply sent from a member's own Collab address (contracts/collab.md "Answering tickets"): the account, the address and the Reply-To it asked for ("address", "mailbox" or an address); else null; public views always carry null
  created_at: string; edited_at: string | null; // edited_at is set only when an internal note was edited
  deleted_at: string | null; deleted_by: UserRef | null; purged_at: string | null; // a marker: a deleted message, or a note sent as a reply (then when and by whom); only admins get its content, until it is purged
  sent_as_reply_id: string | null; // a note sent as a reply: a marker pointing at the reply; its own words are kept, hidden like a deleted message's
};
type Attachment = { id: string; filename: string; content_type: string; size_bytes: number; inline: boolean; url: string };
type Activity = { id: string; ticket_id: string; actor: { type: string; user: UserRef | null }; type: string; data: Record<string, unknown>; created_at: string };
// activity types: status_changed{from,to, reason?: "sender_unverified" | "thread_replied", message_id?} (with "sender_unverified", from equals to: a reply that failed sender checks left the status where it was, actor "system"; "thread_replied": an answer in a third-party thread opened a pending or on-hold ticket, actor "system") priority_changed{from,to} severity_changed{from: Severity | null, to: Severity | null} assigned{from,to,via?: "round_robin"|"load_balanced"|"triage"} (actor "system", or "ai" for via "triage") team_changed{from,to,via: "agent"|"automation"|"api"|"triage"|..., mailbox_from?, mailbox_to? /* the reply address switched with the team */} brand_changed{from,to} mailbox_changed{from,to} /* the reply address */ tagged{tag} untagged{tag} sla_breached{kind} merged{into, into_number /* null when the target is in another, private team */} csat_received{score} linked{link_id,url,title,provider} unlinked{url,title,provider} contact_changed{from,to} /* each {id, name} (the name, else the email) or null */
// thread_started{thread_id, subject, to} thread_closed{thread_id, subject} thread_reopened{thread_id, subject} ("Threads"); note_edited{message_id} and note_deleted{message_id} record internal note changes without the note text; reply_retracted{message_id} records a chat reply taken back from the customer; message_deleted{message_id}, message_restored{message_id} and message_purged{message_id} record deletions of messages other than notes; note_sent_as_reply{message_id, reply_id} records a note sent as the reply written from it
// field_changed{key, name, from, to} (one per custom field; from/to are FieldValue | null)
// AI (actor.type "ai", contracts/ai.md): ai_triaged{category,sentiment,language,priority,severity,via,model?,engine?}; AI changes carry {via, model?: "jev", confidence?: number} ai_answered{message_id,sources,tools} ai_action{action_id,tool,status} ai_handoff (actor "ai" or "contact")

// Labels ("tags" in the API). See "Labels and custom fields" below.
type LabelColor = "gray" | "red" | "orange" | "amber" | "yellow" | "lime" | "green" | "teal" | "cyan" | "blue" | "indigo" | "violet" | "purple" | "pink" | "rose";
type LabelScope = "tickets" | "contacts" | "companies";
type Tag = {
  id: string; name: string; color: LabelColor; description: string; group_id: string | null; position: number;
  archived: boolean; applies_to: LabelScope[]; usage: { tickets: number; contacts: number; companies: number };
  created_at: string; updated_at: string;
};
type TagRef = { id: string; name: string; color: LabelColor; group_id: string | null }; // embedded in tickets, contacts, companies
type TagGroup = { id: string; name: string; color: LabelColor | null; exclusive: boolean; position: number; tag_count: number; created_at: string; updated_at: string };

// Custom fields. Values live under `fields` on Ticket, Contact and Company, keyed by CustomField.key.
type FieldEntity = "ticket" | "contact" | "company";
type FieldType = "text" | "long_text" | "number" | "select" | "multi_select" | "date" | "checkbox" | "url" | "email" | "user";
type FieldOption = { id: string; label: string; color: LabelColor };
type CustomField = {
  id: string; entity: FieldEntity; key: string; name: string; type: FieldType; options: FieldOption[];
  required_on_solve: boolean; description: string; position: number; archived: boolean; created_at: string; updated_at: string;
};
type FieldValue = string | number | boolean | string[];
type FieldValues = Record<string, FieldValue>; // only keys with a value; a cleared field is absent
type FieldFilter = { key: string; op: FieldOp; value: string };
type FieldOp = "eq" | "ne" | "contains" | "not_contains" | "gt" | "gte" | "lt" | "lte" | "any" | "all" | "none" | "set" | "not_set";

type SavedReply = { id: string; title: string; shortcut: string; body_html: string; team_id: string | null /* offered only on that team's tickets */; created_at: string; updated_at: string };
type View = { id: string; name: string; shared: boolean; filters: TicketFilters; position: number; team_id: string | null /* shared with that team's members; shared is then false */ };
type TicketFilters = { status?: TicketStatus[]; priority?: Priority[]; severity?: (Severity | "none")[] /* "none": no severity */; assignee?: "me" | "none" | string; team_id?: "mine" | "none" | string /* mine: the caller's teams */; brand_id?: string; widget_id?: string; mailbox_id?: string; received_at?: string /* the address a ticket was first written to */; tag?: string; channel?: Channel; q?: string; fields?: FieldFilter[] /* max 20, ANDed */ };
type SlaPolicy = { id: string; name: string; priority: Priority | null; team_id: string | null /* only that team's tickets; null = any */; first_response_minutes: number; resolution_minutes: number;
  business_hours: boolean /* count only open business hours (default false): the team's own hours, else the workspace's */; enabled: boolean; position: number };

// Triage, assignment and setup (see "Triage, assignment and onboarding").
type AssignMode = "none" | "round_robin" | "load_balanced";
type AssignmentRule = { target_type: "mailbox" | "team"; target_id: string; mode: AssignMode };
type NewTicketNotify = "auto" | "everyone" | "leads" | "off";
type Assignment = { mode: AssignMode /* = settings.auto_assign */; rules: AssignmentRule[]; auto_away_minutes: number /* 0 = off */;
  new_ticket_notify: NewTicketNotify /* = settings.new_ticket_notify; empty or unknown reads as "auto" */ };
type TriageField = "priority" | "severity" | "labels" | "team";
type DecisionEngine = { provider: "typesafe" /* Jev, direct or through OpenRouter */ | "llm" | "none"; model: string /* "jev-latest", "~typesafe/jev-latest", or the LLM fast model */; connected: boolean };
type TriageSettings = { auto_triage: boolean; fields: TriageField[]; assign_after_triage: boolean;
  threshold: number /* 0.5..0.99, default 0.7: confidence at or above it applies a decision, below it leaves a suggestion */;
  engine: DecisionEngine; available: boolean /* triage can run: AI or a decision engine is configured and AI is on for the workspace */ };
type Weekday = "mon" | "tue" | "wed" | "thu" | "fri" | "sat" | "sun";
type BusinessHours = { enabled: boolean; timezone: string /* the workspace timezone */; schedule: Record<Weekday, { start: string; end: string }[]> /* "HH:MM", 24h */; holidays: { date: string /* YYYY-MM-DD */; name: string }[] };
type OnboardingStepId = "connect_mailbox" | "invite_teammate" | "upload_photo" | "install_widget" | "publish_article" | "setup_triage" | "create_label" | "try_ollie" | "setup_ai_agent";
type OnboardingStep = { id: OnboardingStepId; title: string; description: string; done: boolean; href: string /* app path */; requires: "admin" | "agent" };
type Onboarding = { steps: OnboardingStep[]; done_count: number; total: number; dismissed: boolean };
type AgentSetupStepId = "ai_on" | "identity" | "knowledge" | "tools" | "playground" | "widget" | "answers_on" | "email_answers";
type AgentSetup = { steps: { id: AgentSetupStepId; title: string; description: string; done: boolean; optional: boolean; href: string }[];
  done_count: number; total: number; complete: boolean };  // GET /v1/ai/agent/setup; counts required steps
type OAuthClientInfo = { name: string; uri: string; redirect_host: string; local: boolean /* loopback redirect */ };
type OAuthRequest = { id: string; client: OAuthClientInfo; scopes: string[]; expires_at: string;
  workspaces: { id: string; name: string; role: Role; scopes: string[] /* requested ∩ role */ }[] };
type AIClientConnection = { id: string; client: OAuthClientInfo; user: UserRef; mine: boolean; scopes: string[];
  created_at: string; last_used_at: string | null };
type Automation = { id: string; name: string; trigger: "ticket_created" | "customer_replied" | "agent_replied" | "ticket_updated"; conditions: Condition[]; actions: Action[]; enabled: boolean; position: number; run_count: number };
type Condition = {
  field: "subject" | "body" | "from_email" | "channel" | "mailbox_id" | "priority" | "severity" | "status" | "tag" | "sentiment" | "ai_decide" | "assignee_id" | "team_id" | "brand_id" | "widget_id" | "received_at" | `field.${string}` | `contact.field.${string}`;
  op: "contains" | "not_contains" | "equals" | "not_equals" | "eq" | "ne" | "gt" | "gte" | "lt" | "lte" | "any" | "all" | "none" | "set" | "not_set";
  value: string;
  question?: string; // ai_decide only: a yes/no question, 3..300 characters of plain text
}; // severity: equals/not_equals with a Severity or "none"; ai_decide: gt/lt with value a probability 0.01..0.99 (contracts/ai.md "Automations"); sentiment: equals/not_equals only; assignee_id, team_id, widget_id: equals/not_equals with an id of the workspace or "none"; brand_id: equals/not_equals with a brand id; received_at (the address the first email was written to): equals/not_equals/contains, case-insensitive; eq = equals, ne = not_equals. field.<key>: a ticket custom field, contact.field.<key>: the ticket contact's; ops per field type (below)
type Action = { type: "set_status" | "set_priority" | "set_severity" /* value Severity, or "" to clear */ | "assign_user" | "assign_team" | "add_tag" | "remove_tag" | "send_reply" | "add_note" | "mark_spam" | "ai_triage" | "set_field" | "set_reply_address" /* value a mailbox id of the ticket's brand; skipped when removed or of another brand */; value: string; field?: string /* set_field: the ticket field key */ }; // assign_team records routed {via: "automation"}
type Mailbox = {
  id: string; name: string; email: string; display_name: string;
  inbound_kind: "forwarding" | "imap" | "google" | "microsoft"; outbound_kind: "smtp" | "google" | "microsoft" | "system";
  forwarding_address: string; config: { imap_host?: string; imap_port?: number; imap_username?: string; imap_tls?: "tls" | "starttls" | "none"; smtp_host?: string; smtp_port?: number; smtp_username?: string; smtp_tls?: "tls" | "starttls" | "none"; oauth_email?: string;
    oauth_grant?: "send" | "sync" | "api" /* read-only: what the Google or Microsoft connection may do; send = send only (gmail.send, Mail.Send), sync = read the inbox over IMAP and send, api = read the inbox through the Gmail API or Microsoft Graph and send through them (the Collab OAuth client, "Reading the inbox directly"); absent without a connection */;
    oauth_client?: "collab" /* read-only: the connection's tokens belong to the Collab OAuth client (an api grant); absent = the mailbox client */;
    provider_mark_read?: boolean; provider_archive_on_solve?: boolean; provider_label?: boolean /* tidy-up in Gmail or Outlook, only for a mailbox that reads its inbox through the provider's API ("Reading the inbox directly"); absent = off, the default */;
    sync_start?: "recent" | "new" /* where the first sync of a mailbox that reads its inbox (over IMAP, or through the Gmail API or Microsoft Graph) starts: recent (default) = the last 7 days of the inbox, at most 200 emails; new = only mail that arrives from then on, for history brought over by an import. It applies when the mailbox starts reading its inbox (connecting, or moving to another account or IMAP server); a changed UIDVALIDITY, or a Gmail history or Graph delta the provider no longer keeps, later reads the last 7 days again */ };
  team_id: string | null; signature_html: string; auto_reply_enabled: boolean; auto_reply_subject: string; auto_reply_html: string;
  ai_answers: boolean /* Ollie answers emails to this mailbox, default false (contracts/agent.md "Email") */; ai_answers_mode: "draft" | "send" /* default "draft" */;
  ai_answers_outside_hours: boolean /* only outside business hours, while business hours are on; default false */;
  bot_id: string | null /* the bot the AI agent answers as (contracts/bots.md); null = the default bot; PATCH checks it is a bot of the workspace */;
  status: "active" | "error" | "disabled"; last_error: string; last_synced_at: string | null;
  last_received_at: string | null /* when mail last arrived by any route (forwarding, IMAP, the Gmail and Microsoft APIs), filtered mail included; the app warns when an active, receiving address had none for 7 days, or none a day after it was added */; created_at: string;
  receiving: "ok" | "imap_off" /* read-only: imap_off while the mailbox receives over IMAP and that is off for its workspace: inbound_kind imap while neither the instance has MAILBOX_IMAP on nor the workspace has Collab, or a google or microsoft sync connection while MAILBOX_IMAP is off; it is kept, not polled, until PATCHed to forwarding. A mailbox that reads through the provider's API (oauth_grant api) is ok; while its workspace lacks Collab it is kept with status error and a last_error that says so */;
  sending: "ok" | "system_off" /* read-only: system_off while the mailbox sends through the platform mailer (outbound_kind system) and the instance has MAILBOX_SYSTEM_SEND off; it sends nothing until PATCHed to smtp or connected to Google or Microsoft */;
  brand_id: string /* contracts/teams.md */; aliases: string[] /* up to 20 addresses that deliver into this mailbox */;
  alias_teams: Record<string, string> /* an alias to the team its new tickets go to instead of team_id (contracts/teams.md "Routing") */;
  alias_settings: Record<string, AliasSettings> /* every alias, keyed lowercased: its own identity (contracts/teams.md "Aliases") */;
  provider_send_as: { readable: boolean /* the Google connection may read Gmail's send-as list: a sync or api grant, not a send grant */;
    checked_at: string | null; addresses: { email: string; display_name: string; verified: boolean }[] /* Gmail's send-as addresses other than the account's own */ } | null /* Google mailboxes, admins and keys with settings:read only; null otherwise */;
  from_name_format: string /* up to 80; {agent} {agent_first} {team} {brand} {mailbox}; default "{agent} at {mailbox}" */;
  private: boolean /* read-only: its team is private, so only that team's members send from it */;
  personal_replies: { enabled: boolean | null; reply_to: "address" | "mailbox" | null; accounts: "any" | "shared" | "domain" } /* replies on its tickets from members' own Collab addresses (contracts/collab.md "Answering tickets"): null follows the workspace's collab_ticket_replies / collab_ticket_reply_to; accounts: any, shared accounts only, or addresses at its own or an alias's domain */;
  trusted_form_senders: string[] /* admins only (others read []): website contact forms, exact addresses or "@domain", at most 50 ("Inbound email") */;
};
type AliasSettings = {
  display_name: string /* up to 64; "" = the mailbox's display_name: the From name's {mailbox} and {{ mailbox.name }} */;
  signature_html: string /* design allowlist, template variables, up to 64 KB; "" = none of its own */;
  brand_id: string | null /* new tickets written to the alias take this brand; null = the mailbox's */;
  send_as: "verified" | "unverified" | "unknown" /* whether replies go out From the alias (contracts/teams.md "Aliases") */;
  send_as_by: "provider" | "admin" | "" /* who decided: Gmail's list or Microsoft's refusal, or an admin; "" while unknown */;
};
type KbCollection = { id: string; name: string; slug: string; description: string; position: number; article_count: number; brand_id: string | null /* the brand whose help center shows it; null = every brand */ };
type KbArticle = { id: string; collection_id: string | null; title: string; slug: string; excerpt: string; body_html: string; status: "draft" | "published"; author: UserRef | null; views: number; published_at: string | null; updated_at: string;
  brand_id: string | null /* null = every brand. An article in a collection with a brand belongs to the collection's brand */ };
type ApiKey = { id: string; name: string; prefix: string; scopes: string[]; last_used_at: string | null; expires_at: string | null; created_at: string; created_by: UserRef | null };
type Webhook = { id: string; url: string; description: string; events: string[]; enabled: boolean; created_at: string };
type WebhookDelivery = { id: string; event_id: string; event_type: string; status: "pending" | "succeeded" | "failed"; attempts: number; response_code: number | null; duration_ms: number | null; created_at: string };
type AuditLog = { id: string; actor: { type: "user" | "api_key" | "system"; user: UserRef | null; api_key_id: string | null }; action: string; entity_type: string; entity_id: string | null; data: Record<string, unknown>; ip: string; created_at: string };
type Notification = { id: string; type: "assigned" | "new_ticket" /* data {channel, contact?: {id, name}}: a new ticket nobody is assigned to, see "Assignment" */ | "mentioned" | "customer_replied" | "thread_replied" /* data {message_id, thread_id, subject, preview, from: {name, email}}: someone outside the team answered a third-party thread on a ticket assigned to you */ | "sla_breached" | "ai_handoff" | "ai_cap_reached" | "ai_balance_low" | "ai_balance_out" | "auto_recharge_failed" | "ai_key_failed" | "team_joined" /* data {team_id, team: {id, name, private}, user: {id, name}, by}: an admin joined a team you lead (a private one to see its tickets) */ | "import_finished" /* data contracts/imports.md: a history import you started completed, failed or needs new credentials */ | "collab_assigned" | "collab_mention" /* Collab conversations, contracts/collab.md "Notifications" */; ticket: { id: string; number: number; subject: string } | null; data: Record<string, unknown>; read_at: string | null; created_at: string };
type Device = { id: string; platform: "ios"; name: string; app_version: string; environment: "production" | "sandbox";
  muted_types: string[]; current: boolean /* bound to the caller's session */; created_at: string; last_push_at: string | null };
// Notification preferences ("Notification preferences and emails" below). Channels per type, per workspace;
// the email schedule and the pause are the account's, across workspaces.
type NotificationChannels = { email: boolean; push: boolean /* the phone */; desktop: boolean /* the desktop app, or a browser that allowed notifications */ };
type NotificationTypePrefs = NotificationChannels & { type: Notification["type"]; group: "tickets" | "team" | "ai_billing" | "workspace"; defaults: NotificationChannels;
  urgent: boolean /* may be emailed before a cooldown ends ("Pacing" below) */ };
type NotificationPacing = { delay_minutes: number; cooldown_minutes: number[]; quiet_reset_minutes: number; urgent_gap_minutes: number;
  urgent_per_day: number; per_hour: number; per_day: number; active_minutes: number } /* this instance's pacing, NOTIFY_EMAIL_* */;
type NotificationPreferences = {
  email_cadence: "instant" | "hourly" | "daily" | "off"; digest_hour: number /* 0..23, daily digest, in timezone */;
  timezone: string /* IANA, e.g. "Europe/Budapest"; "UTC" until the app sends one */; paused_until: string | null;
  email: string /* where notification emails go */; email_verified: boolean /* notification emails go to a confirmed address only */;
  delay_minutes: number /* how long an unread notification waits before "instant" emails it */; pacing: NotificationPacing;
  unsubscribed_at: string | null /* an email's unsubscribe link turned off email from this workspace; resubscribe undoes it */;
  types: NotificationTypePrefs[] /* the types the caller can receive in this workspace: owners and admins also get the ai_billing and workspace groups */ };
// Notification.data: assigned {by, actor_type, actor?: {id, name}}; mentioned {message_id, by, actor?: {id, name}, preview};
// new_ticket {channel, contact?: {id, name}}; customer_replied {message_id?, preview?, contact?: {id, name}}; sla_breached {kind}; ai_handoff {via: "ai" | "contact", contact?: {id, name}}; ai_cap_reached {cap_usd} (ticket null; the widget agent reached `widget_monthly_cap_usd`, once a month to owners and admins); ai_balance_low {remaining_usd}, ai_balance_out {remaining_usd} (US dollars), auto_recharge_failed {reason} (ticket null; owners and admins, contracts/billing.md "Auto-recharge"); ai_key_failed {provider: "openrouter" | "openai" | "anthropic", reason: "rejected" | "no_funds"} (ticket null; the workspace's own AI key failed, at most once an hour to owners and admins, contracts/ai.md "Provider per workspace"); import_finished {import_id, source, source_name, account, status: "completed" | "failed" | "needs_credentials", tickets, updated, articles, failed, error} (ticket null; to the admin who started the import, contracts/imports.md); collab_assigned {thread_id, account_id, by, subject, actor?: {id, name}}, collab_mention {thread_id, comment_id, by, subject, preview, actor?: {id, name}} (ticket null; contracts/collab.md).
// Mentions in note HTML: <span data-type="mention" data-id="<user_id>">@Name</span> (also data-mention="<user_id>"), or "@<full name or email>" in text.
```

## Endpoints

### Auth and account (owner: backend-core)
| method | path | body → response |
| --- | --- | --- |
| GET | `/v1/auth/config` | → `{ providers: [{id:"google"|"microsoft"|"github"|"oidc", name}], password_login: bool, signup_enabled: bool, passkeys: bool, turnstile_site_key: string \| null, native: {app_attest: bool, push: "direct"\|"relay"\|"off", relay_url: string \| null}, web_push_key: string \| null }`. `passkeys` is true when passkey sign-in is available; `native` is for the native apps (`contracts/native.md`): `app_attest` when `APP_ATTEST_APP_IDS` is set, `push` from the APNs key / `PUSH_RELAY_URL`, `relay_url` only with `push: "relay"`; `web_push_key` is the VAPID public key browsers subscribe with (`contracts/native.md` "Web Push") |
| POST | `/v1/auth/register` | `{email,password,name,turnstile_token?,app_attest?,invite_token?}` → `{access_token, expires_in, user}` + the account's refresh cookie, `od_rt_<user id hex>` (native requests: `refresh_token` in the body, no cookie). `409 too_many_accounts` when the browser already holds 5 other accounts (see "Several accounts" below). `409 email_taken` (the address's owner is emailed about the attempt, at most 3 times a day). With signup off, only a valid `invite_token` for the same email may register (`403 signup_disabled`); an invite token also marks the email verified |
| POST | `/v1/auth/login` | `{email,password,turnstile_token?,app_attest?}` → `{access_token, expires_in, user, change_password?}` or `{mfa_required:true, mfa_token}`. `change_password: true` (here, or from `/v1/auth/mfa` after the code): the password is in the breached list or shorter than the rules now ask; the app asks for a new one |
| POST | `/v1/auth/mfa` | `{mfa_token, code}` → `{access_token, expires_in, user, redirect_to?, change_password?}`. `code` is a 6-digit TOTP code or a recovery code (`xxxx-xxxx-xxxx-xxxx`) |
| POST | `/v1/auth/refresh` | `{user_id?}` with the account's cookie, or `{refresh_token}` in the body when there is no cookie → `{access_token, expires_in, user}`. Rotates only that account's refresh token (native requests get the new `refresh_token` in the body). Without `user_id`, the account this browser used most recently. A refused refresh (`401 unauthenticated`) clears only that account's cookie |
| POST | `/v1/auth/accounts` | cookies → `{accounts: [{user: {id, name, email, avatar_url}, memberships: [{workspace: {id, name, slug, avatar_url}, role}]}]}`: the accounts this browser is signed in to, in the order they were added. Rotates nothing; accounts whose session ended are left out and their cookies cleared. Native requests get `[]` |
| POST | `/v1/auth/logout` | `{user_id?, all?, refresh_token?}` → 204. Signs out of the access token's account (else `user_id`'s, else the body token's session): revokes the session (and its push devices), closes its sockets, clears only that account's cookie. `{all: true}` signs out of every account in this browser |
| POST | `/v1/auth/password/forgot` | `{email, turnstile_token?, app_attest?}` → 204 always |
| POST | `/v1/auth/password/reset` | `{token, password, app_attest?}` → 204. A native request's `app_attest` proof is verified when sent (action `password_reset`); the emailed token is the gate |
| POST | `/v1/auth/app-attest/challenge` | `{}` → `{challenge}`: 32 random bytes, base64url, single use, 5 minutes. 60 per IP per hour. `404 app_attest_off` without `APP_ATTEST_APP_IDS` |
| POST | `/v1/auth/app-attest/keys` | `{key_id (base64), attestation (base64 CBOR), challenge}` → 204. Verifies Apple's attestation over `SHA256(challenge)` and stores the key; the challenge is spent either way. `400 invalid_attestation` (also for a key attested before). 20 per IP per hour |
| POST | `/v1/auth/email/verify` | `{token}` → 204 |
| POST | `/v1/auth/email/resend` | → 204 |
| GET | `/v1/auth/oauth/{provider}/start?redirect_to=&popup=&client=native&code_challenge=` | → 302 to provider. `redirect_to` is the app path to land on after sign-in (e.g. `/inbox/mine`), returned by exchange/link; the callback itself goes to `/auth/callback`. `popup` (16..64 of `[A-Za-z0-9_-]`, ignored otherwise and for native) marks a sign-in the web app runs in a popup window: the callback then goes to `/auth/popup?code=` or `?error=`, with `&popup=<id>`, which hands the code to the waiting tab (`window.opener`, else a same-origin `BroadcastChannel`) and finishes the sign-in itself when no tab answers. `client=native` with `code_challenge` (`BASE64URL(SHA256(verifier))`, 43 chars) starts a native sign-in (`contracts/native.md`); a missing or malformed challenge answers `opendesk://auth/callback?error=invalid_request` |
| GET | `/v1/auth/oauth/{provider}/callback` | → 302 to `APP_URL/auth/callback?code=<one-time>` or `?error=` (`APP_URL/auth/popup?…&popup=<id>` for a popup flow, read from the verified state only); for a flow started with `client=native` (read from the verified state only), to `opendesk://auth/callback?code=` or `?error=` |
| POST | `/v1/auth/oauth/exchange` | `{code, code_verifier?}` → `{access_token, expires_in, user, redirect_to?}` or `{mfa_required, mfa_token, redirect_to?}` or `{link_required: true, link_token, email, redirect_to?}` when the provider's verified email belongs to an existing password account. A code issued to a native flow needs `code_verifier` (S256, compared in constant time): `400 invalid_code` otherwise, and the code is spent. Unknown or spent codes: `400 invalid_token` |
| POST | `/v1/auth/oauth/link` | `{link_token, password}` → same as exchange (tokens or `mfa_required`). Binds the provider identity after the account password is presented; `401 invalid_credentials` on a wrong password (the link token stays usable for 10 min) |
| POST | `/v1/auth/passkey/login/begin` | `{}` → `{options: PublicKeyCredentialRequestOptionsJSON, passkey_token}`. Discoverable (no `allowCredentials`), so no email is needed and conditional UI works. `passkey_token` is single use, 5 min |
| POST | `/v1/auth/passkey/login/finish` | `{passkey_token, credential: AuthenticationResponseJSON}` → same as `/v1/auth/login` (`{access_token, expires_in, user}` + refresh cookie). A user-verified passkey is a complete multi-factor sign-in (the session counts as having passed 2FA); without user verification an account with 2FA on answers `{mfa_required:true, mfa_token}`. `401 invalid_credentials` for every refused attempt (unknown passkey, bad signature, spent or wrong token, sign counter not advancing) |
| POST | `/v1/auth/totp/setup` | → `{secret, otpauth_url}`. Needs a sign-in within 12 hours (`403 reauth_required`) |
| POST | `/v1/auth/totp/enable` | `{code}` → `{recovery_codes: string[]}` |
| POST | `/v1/auth/totp/disable` | `{code}` → 204 |
| GET | `/v1/me` | → `{user: User, memberships: Membership[]}` |
| PATCH | `/v1/me` | `{name?, avatar_url?, time_format?}` → `User`. `time_format` is account-wide, across workspaces; another value is `422`. `avatar_url` is a picture to copy: an https URL fetched and processed server-side (asynchronously; `member.updated` follows), `""` removes the picture |
| PUT | `/v1/me/avatar` | multipart `file` (PNG, JPEG, GIF or WebP, at most 5 MB) → `User` with `avatar_source: "upload"`. Audit `user.avatar_updated` |
| DELETE | `/v1/me/avatar` | → `User` with no picture; the next Google or GitHub sign-in brings the provider picture back. Audit `user.avatar_removed` |
| POST | `/v1/me/heartbeat` | → 204. Marks the user seen (auto-away). The app sends it every 5 minutes while a tab is visible |
| POST | `/v1/me/password` | `{current_password, new_password}` → 204 |
| GET | `/v1/me/sessions` / DELETE `/v1/me/sessions/{id}` | paginated `[{id, user_agent, ip, created_at, last_used_at, current, client: "web"\|"desktop"\|"ios"}]` / revoke (deletes the session's push devices) |
| POST | `/v1/me/devices` | `{platform: "ios", token?, relay_token?, environment: "production"\|"sandbox", app_version, name}` → `Device`. Exactly one of `token` (hex APNs token, `push: "direct"`) or `relay_token` (`push: "relay"`); the other mode answers `422 push_unavailable`. Upserts by token and binds the device to the calling session, which keeps one device (a new token replaces the old). Audit `push_device.registered` |
| GET | `/v1/me/devices` | → `Device[]` (not paginated; devices of live sessions) |
| PATCH | `/v1/me/devices/{id}` | `{muted_types?: string[]}` (notification types, at most 32) → `Device`. Mutes on that phone only, on top of the `push` channel of the notification preferences |
| DELETE | `/v1/me/devices/{id}` | → 204. Audit `push_device.removed`. Other users' devices answer `404` |
| GET | `/v1/me/passkeys` | paginated `Passkey[]`, newest first. `Passkey = {id, name, created_at, last_used_at: string \| null, backed_up: bool}` |
| POST | `/v1/me/passkeys/register/begin` | `{}` → `{options: PublicKeyCredentialCreationOptionsJSON, passkey_token}` (resident key required, user verification preferred, no attestation, the account's passkeys in `excludeCredentials`). Same gate as owner-level operations (`403 mfa_required`, `403 reauth_required`); `409 passkey_limit` at 25 passkeys |
| POST | `/v1/me/passkeys/register/finish` | `{passkey_token, credential: RegistrationResponseJSON, name?}` → 201 `Passkey`. `name` defaults to `"Passkey"`. `400 invalid_passkey` when the token is spent, expired or from another session, or the credential does not verify; `409 passkey_exists` |
| PATCH | `/v1/me/passkeys/{id}` | `{name}` → `Passkey` |
| DELETE | `/v1/me/passkeys/{id}` | → 204. Needs the second factor (when on) and a sign-in within 12 hours, like adding one (`403 mfa_required` / `reauth_required`) |
| GET / PUT | `/v1/me/assistant-peer` | workspace-scoped, Collab only → the caller's own agent-to-agent sharing: whether teammates' Ollie may ask theirs, who, from which of their own mail accounts and how, and their rules (contracts/ai.md "Asking a teammate"). Users only; Ollie, API keys and AI clients get `403`. `409 assistant_peer_off` to turn it on while the workspace does not allow it |
| GET | `/v1/me/assistant-peer/asks` | paginated asks made to the caller's Ollie, newest first, kept 90 days |
| DELETE | `/v1/me` | `{password?, code?}` → 204. `password` when the account has one, `code` when 2FA is on. Deletes the account and every workspace it solely owns (`409 sole_owner` while owner of a workspace with other members) |
| GET | `/v1/me/workspace-prefs` | `WorkspacePrefs` (below; the defaults until the person changes something). No `OD-Workspace` |
| PUT | `/v1/me/workspace-prefs` | any of `{mode, layout, order, hidden, collapsed, items, everywhere}` → `WorkspacePrefs`. Fields left out keep their value; `mode: ""` goes back to the default. `422` for an unknown mode or layout, more than 100 workspace ids, or an item id not shaped `<workspace id>/<kind>[:<id>]` (or `everywhere/<list>`), at most 500 items |
| GET | `/v1/me/overview` | `{workspaces: WorkspaceOverview[]}`: every workspace the person is in, in joining order (below). No `OD-Workspace` |

#### Workspaces together

A person in several workspaces can work them side by side in the web app ("Together"), or one at a
time ("Separate"). Every request still targets exactly one workspace (`OD-Workspace`), with that
workspace's role and private teams: a view across workspaces is made of one request per workspace.
The app keeps each workspace's data under its own cache keys and opens anything of another workspace
by moving its focus there, without reloading. Links to a workspace's pages carry `?ws=<id>` (above).

`WorkspacePrefs` is the person's own, one per user, whatever the workspace, and deleted with them:

```jsonc
{
  "mode": "together" | "separate",  // the default is together (it only shows with two or more workspaces)
  "mode_set": false,                // read only: whether the person chose the mode
  "layout": "grouped" | "mixed",    // grouped: a sidebar group per workspace; mixed: sections combined, rows badged
  "order": ["<wid>"],               // sidebar order; workspaces not listed follow in joining order
  "hidden": ["<wid>"],              // left out of the sidebar, the Everywhere lists and the bell
  "collapsed": ["<wid>" | "everywhere"],
  "items": { "hidden": ["<wid>/view:<id>"], "order": ["<wid>/team:<id>"] },
  "everywhere": true,               // the Everywhere lists: assigned to me, and all mail
  "updated_at": "..."
}
```

Ids in it only arrange the sidebar. They are never checked against memberships and never grant
anything: a workspace the person is not in is simply not shown.

`WorkspaceOverview` is one workspace's sidebar numbers, each computed as that workspace's own route
answers the person (a principal for that workspace alone: their role there, its private teams):

```jsonc
{ "id": "<wid>", "name": "Acme", "slug": "acme", "avatar_url": "", "role": "agent", "suspended": false,
  "unread_notifications": 3,
  "ticket_counts": TicketCounts | null,   // GET /v1/tickets/counts there; null while suspended
  "mail_counts": CollabCounts | null }    // GET /v1/collab/counts there; null without Collab
```

A suspended workspace is listed with its name and nothing else.

### Workspace (owner: backend-core)
| method | path | notes |
| --- | --- | --- |
| POST | `/v1/workspaces` | `{name}` → `Workspace` (caller becomes owner; seeds default views, tags, SLA, the default brand with the workspace slug and its widget "Website") |
| GET/PATCH | `/v1/workspace` | current workspace (`OD-Workspace`) `{name?, settings?}` |
| DELETE | `/v1/workspace` | owner + MFA, `{confirm_name}` → 204, removes all workspace data |
| POST | `/v1/workspace/transfer` | owner + MFA, `{user_id}` |
| POST | `/v1/workspace/widget-secret/rotate` | admin → `{secret}` (shown once): the default brand's first widget. Any widget: `POST /v1/widgets/{id}/secret/rotate` (contracts/teams.md) |
| GET | `/v1/members` · PATCH/DELETE `/v1/members/{user_id}` | `{role}` |
| PATCH | `/v1/members/{user_id}/profile` | `{title?, phone?, pronouns?, signature_html?}` → `Member`. `user_id` may be `me`; members edit their own; the owner edits anyone's, an admin those of agents and viewers (`403` otherwise). One line each (80, 40, 40 characters, no markup); `signature_html` is the personal signature (design allowlist, template variables except itself, contracts/templates.md). Publishes `member.updated` |
| PATCH | `/v1/members/{user_id}/availability` | `{availability?: Availability, max_open_tickets?: number \| null /* 1..1000, null clears */}` → `Member`. `user_id` may be `me`. Members set their own `availability`; the owner sets anyone's, an admin those of agents and viewers (`403` otherwise), with `max_open_tickets` (viewers cannot be assigned, so theirs is refused). Publishes `member.updated` |
| PATCH | `/v1/members/{user_id}/permissions` | admin; `{message_delete: "default"\|"off"\|"own"\|"all"}` → `Member`. Sets which messages this agent may delete, over `settings.message_delete`: `own` their own internal notes, `all` any agent's notes and replies (the way an admin lets an agent delete customer-facing replies), `off` none; `default` follows the workspace again. Only agents take it (`422` otherwise: owners and admins delete any message, viewers none). Audit-logged as `member.permissions_changed`; publishes `member.updated` |
| PUT/DELETE | `/v1/workspace/images/{slot}` | admin. `slot`: `logo` (the workspace `avatar_url`), `help_center_logo`, `help_center_favicon`, `widget_avatar`, `widget_launcher` (a bot's picture is `PUT /v1/bots/{id}/image`, contracts/bots.md). PUT multipart `file` (image, 5 MB) → `Workspace`; DELETE → `Workspace`. Audit `workspace.image_updated` / `workspace.image_removed`; publishes `workspace.updated` |
| GET | `/v1/images/{id}?s=256\|64` | public. The processed PNG behind every `avatar_url` (`s` defaults to 256). Ids are unguessable and immutable: `Cache-Control: public, max-age=31536000, immutable`, `X-Content-Type-Options: nosniff`, `Content-Security-Policy: default-src 'none'; sandbox` |
| GET | `/v1/onboarding` | members → `Onboarding`, computed from the workspace's real state (below) |
| POST | `/v1/onboarding/dismiss` | → 204. Hides the checklist for this member; `GET` still answers, with `dismissed: true` |
| GET/POST | `/v1/invitations` · DELETE `/v1/invitations/{id}` | `{email, role}` |
| POST | `/v1/invitations/{id}/link` | → `{url, expires_at}`: the invitation's link, to send another way (admin) |
| POST | `/v1/invitations/{id}/resend` | → `Invitation`: emails the same link again (admin; 3 / hour per invitation) |
| POST | `/v1/invitations/accept` | `{token}` → `Membership` (no workspace header) |
| POST | `/v1/invitations/lookup` | `{token}` → `{workspace_name, email, role}` public (the token never rides in a URL) |
| CRUD | `/v1/teams`, `/v1/teams/{id}` | `{name, member_ids}`; the full team API, brands, widgets and routing rules are in contracts/teams.md |
| CRUD | `/v1/api-keys`, `/v1/api-keys/{id}` | admin users only (not API keys). create `{name, scopes, expires_at?, private_team_ids?}` → `ApiKey & {secret}`, needing the second factor (when on) and a sign-in within 12 hours (`403 mfa_required` / `reauth_required`) (`secret` shown once; `prefix` is `od_live_` + 8 chars); PATCH `{name?, private_team_ids?}` (the list is replaced; contracts/teams.md "Visibility": `422` for an id that is not a private team of the workspace, `403 forbidden` for adding a team the caller is not on; removing is open to admins; a change closes the key's sockets). `private_teams` lists the same teams with `granted_by`: a team lasts while its granter is on it, and setting the list makes the caller the granter of the listed teams they are on; DELETE revokes |
| GET | `/v1/ai-clients/server` | members → `{url, resource_metadata, authorization_server, scopes, tools[]}`: the MCP server for AI client setup (`contracts/agent.md` "opendesk MCP server") |
| GET · DELETE | `/v1/ai-clients/connections` · `/v1/ai-clients/connections/{id}` | members (users only) → `{data: AIClientConnection[]}`: AI clients connected with OAuth, your own (admins: everyone's); DELETE disconnects one (your own; admins any) → 204, audit `oauth.grant_revoked` |
| GET · POST | `/v1/oauth/requests/{id}` · `/v1/oauth/requests/{id}/approve` · `/v1/oauth/requests/{id}/deny` | signed-in users (no workspace header, no API keys): the MCP OAuth consent page. GET → `OAuthRequest`; approve `{workspace_id}` → `{redirect_to}` (audit `oauth.grant_created`); deny → `{redirect_to}`. `410 request_expired` once decided or after 10 minutes |
| CRUD | `/v1/webhooks`, `/v1/webhooks/{id}` | `{url, description, events, enabled, private_team_ids}` create → `Webhook & {secret}`. `events: ["*"]` (or `[]`) is every event, including types added later; a list may use `resource.*`. Events of a private team's tickets are delivered only with the team in `private_team_ids` (same rules as API keys, `private_teams` with `granted_by` too) |
| GET | `/v1/webhooks/{id}/deliveries` | list |
| POST | `/v1/webhooks/{id}/test` | sends `webhook.test` → 202 `WebhookDelivery`; `409 webhook_unverified` before the webhook is verified |
| POST | `/v1/webhooks/{id}/verify` | `{code?}` → `Webhook`. Endpoint ownership (CASA 7.1.2): a webhook created, or whose `url` changes, is `verified: false` and receives no events until its URL answers a signed `webhook.verification` event (`data: {webhook_id, challenge}`) with `2xx` and the challenge as the body (or JSON `{challenge}`), or an admin sends that challenge here as `code` (within 24 h; `422 invalid_code` otherwise). Without `code`, a new challenge is sent. Webhooks made before verification existed count as verified |
| CRUD | `/v1/bots`, `/v1/bots/{id}` | members read, admin write. The AI agent's customer-facing bots: name, look and personality, with duplicate, default, reorder, picture and public avatar routes; see `contracts/bots.md` |
| CRUD | `/v1/integrations`, `/v1/integrations/{id}` | admin users only. Connected products for Ollie (MCP servers); see `contracts/integrations.md`, which also lists `/v1/tickets/{id}/links` |
| GET | `/v1/audit-logs` | admin, list, optional `?action=&entity_type=` |
| GET | `/v1/events?since=<seq>&limit=` | `limit` default 500, max 1000 → `{data: Envelope[], latest_seq, resync: bool}`. Members, and API keys with `realtime` or `tickets:read` |
| POST | `/v1/realtime/token` | → `{token, url, expires_at}` (users and keys with `realtime`) |
| GET | `/v1/me/notification-preferences` | workspace-scoped → `NotificationPreferences` for the caller in this workspace (members, any role) |
| PATCH | `/v1/me/notification-preferences` | `{email_cadence?, digest_hour?, timezone?, paused_until?: string \| "" /* "" resumes */, types?: [{type, email?, push?, desktop?}]}` → `NotificationPreferences`. `types` change this workspace only; the rest is account-wide. A cadence change reschedules the emails already waiting. `paused_until` at most 30 days ahead. Unknown types or timezones are `422` |
| POST | `/v1/me/notification-preferences/resubscribe` | workspace-scoped → `NotificationPreferences`. Restores each type's email choice from before the unsubscribe link was used here and clears `unsubscribed_at`; without one nothing changes. Turning any type's email on with PATCH also clears it |
| POST | `/v1/me/notification-preferences/test` | workspace-scoped → 204. Sends a sample notification email to the caller now, whatever the preferences. 5 per hour. `422 email_unverified` |
| POST | `/v1/notifications/unsubscribe` | public, `{token}` → `{workspace: {id, name}, email}`. Turns off every notification email from that workspace for the token's user (the `email` channel of every type). Idempotent. `400 invalid_token`. 30 per IP per hour |
| POST | `/v1/notifications/unsubscribe/one-click?token=` | public, RFC 8058 one-click (`List-Unsubscribe-Post: List-Unsubscribe=One-Click`, any form body) → 200 `text/plain`. Same effect as above |
| GET | `/v1/notifications?unread=` · POST `/v1/notifications/read` | `{ids?: string[], all?: bool}` → 204. Rows are created by helpdesk (assigned, new_ticket, mentioned, customer_replied, thread_replied, sla_breached, ai_handoff, team_joined) the AI area (ai_cap_reached, ai_key_failed) billing (ai_balance_low, ai_balance_out, auto_recharge_failed) and imports (import_finished) with a `notification.created` event to `user:<uid>`, and pushed to the user's phones (`contracts/native.md` "Push"). Marking read sends the phones a badge update |
| GET | `/v1/push/instance-key` | public, every instance → `{public_key}` (Ed25519, base64url, `Cache-Control: public, max-age=3600`): verifies this instance's relay requests |
| POST | `/v1/push/relay/register` | relay only (`PUSH_RELAY_SERVE`), called by the app → `{token, environment, instance}` → `{relay_token}`. `instance` is the instance's https API URL. 30 per IP per hour. `404 relay_off` elsewhere |
| POST | `/v1/push/relay/send` | relay only, signed by the instance (`OD-Push-Instance`, `OD-Push-Timestamp`, `OD-Push-Signature`) → `{relay_token, push: {kind: "alert"\|"badge", type?, notification_id?, ticket_id?, ticket_number?, workspace_id?, badge}}` → 204. Unknown members are refused (`422`), so no ticket text can be sent. `401 invalid_signature` (bad, stale or foreign signature, or a relay token of another instance), `410 gone` (APNs dropped the token), `429` (per instance), `502 push_failed` |

Core notes:
- App links in system email: `/verify?token=`, `/reset?token=`, `/invite?token=`, `/unsubscribe?token=` on `APP_URL`.
  Notification email links carry `?ws=<workspace id>`: the app switches to that workspace when the user is a member, then drops the parameter.
- Several accounts: a browser holds up to 5 signed-in accounts, one `od_rt_<user id hex>` refresh cookie each
  (`contracts/tokens.md` "Several accounts in one browser"). Every session-issuing route (register, login, `mfa`,
  `oauth/exchange`, `oauth/link`, `passkey/login/finish`) sets the signed-in account's cookie and leaves the others;
  a sixth account is `409 too_many_accounts`; signing in again to a held account replaces its session. The legacy
  `od_refresh` cookie is still accepted and moved to the account's own cookie on first use. Access tokens are per
  account; requests name the workspace with `OD-Workspace` as before.
- `401` with `code: "unauthenticated"` means the access token is missing or expired (refresh and retry); other
  401 codes (`invalid_credentials`, `invalid_code`) answer the request itself and must not trigger a refresh.
- Native requests (`OD-Client: ios/<version>` and no `Origin`, `contracts/native.md`) receive `refresh_token` in the
  body of every session-issuing answer and never a cookie. Sessions record their `client`: `ios` for native
  requests, `desktop` for `OD-Client: desktop/<version>` or a user agent with `opendesk-desktop/`, `web` otherwise.
- `app_attest` (native requests, when App Attest is on) replaces `turnstile_token` on register, login and password
  forgot: `{key_id, assertion, challenge}`, the assertion over
  `"opendesk-app-attest/v1\n" + action + "\n" + challenge` (action `register`, `login`, `password_forgot`,
  `password_reset`). A refused proof is `400 captcha_failed`; a key over its 30 protected requests per hour is `429`.
  A proof sent by a request with `Origin` is ignored (Turnstile applies).
- OAuth: `start` sets an `od_oauth` binding cookie (Path=`/v1/auth/oauth`) and an `oauth_state` JWT (`data.flow = "login"`).
  Callback errors: `access_denied`, `invalid_state`, `invalid_request`, `provider_error`, `provider_unavailable`,
  `email_unverified`, `signup_disabled`, `server_error`. A first sign-in needs a provider-verified email (GitHub: verified
  primary; Microsoft: personal account or the `xms_edov` claim).
- Owner-level operations (`DELETE /v1/workspace`, `/v1/workspace/transfer`, `/v1/workspace/widget-secret/rotate`, `/v1/widgets/{id}/secret/rotate`,
  `/v1/auth/totp/disable`) answer `403 mfa_required` when 2FA is on but this session did not pass it, and
  `403 reauth_required` when the session is older than 12 hours (sign in again).
- Passkeys: the relying party ID is the host of `APP_URL` and the accepted origin is `APP_URL`'s scheme and host. Options and
  responses use the WebAuthn JSON forms (base64url without padding), as `@simplewebauthn/browser` sends and expects. The
  user handle is the user id's 16 bytes. Passkey ids from other accounts answer `404`. Adding and removing a passkey is
  audit-logged (`passkey.added`, `passkey.removed`); a passkey sign-in logs `login` with `method: "passkey"`, a refused one
  `login.failed`. Deleting the account deletes its passkeys.
- Member rules: owners change and remove anyone but themselves; admins manage agents and viewers; only the owner grants
  `admin`; `owner` moves only by transfer. `DELETE /v1/members/{own id}` leaves (owner: `409 owner_cannot_leave`).
- `POST /v1/invitations/accept` needs the signed-in user's email to equal the invited one (`403 invitation_email_mismatch`).
- An invitation's token is stored hashed and sealed (`d.Box`), so `link` and `resend` give the same link the email
  carried; both extend the invitation to 7 days from now. Signing up with an invitation link opens sign-up when it is
  closed, and marks the address confirmed only when the link was never copied: after `link`, the new account confirms
  its address by email like any other.
- Role and list endpoints for API keys: workspace/members/teams read `settings:read`; writes `settings:write`.
- MCP OAuth (outside `/v1`, `contracts/agent.md` "OAuth"): `/.well-known/oauth-protected-resource[/mcp]`,
  `/.well-known/oauth-authorization-server`, `POST /oauth/register`, `GET /oauth/authorize`, `POST /oauth/token`,
  `POST /oauth/revoke`. A user acting through an OAuth grant is bound by the granted scopes like an API key, on top of
  their role; account routes and routes API keys may not call refuse it.

#### Notification preferences and emails

Every notification lands in the app (the bell, always on). Each type also has three channels, chosen per workspace
(`notification_preferences`, deleted with the membership): `email`, `push` (the user's phones) and `desktop` (OS
notifications from the desktop app, or from a browser that allowed them). A channel the user never set follows its default:

| type | group | email | push | desktop |
| --- | --- | --- | --- | --- |
| `assigned`, `mentioned`, `sla_breached` | tickets | on | on | on |
| `new_ticket` (a new ticket nobody is assigned to; who gets it: "Assignment") | tickets | on | on | on |
| `customer_replied` (tickets assigned to you; on a ticket nobody has, the people `new_ticket` tells) | tickets | on | on | on |
| `thread_replied` (an answer in a side conversation on a ticket assigned to you) | tickets | on | on | on |
| `ai_handoff` | tickets | on | on | on |
| `team_joined` | team | off | off | on |
| `ai_cap_reached`, `ai_key_failed`, `ai_balance_low`, `ai_balance_out`, `auto_recharge_failed` (owners and admins) | ai_billing | on | off | on |
| `import_finished` (owners and admins; only who started the import) | workspace | on | on | on |
| `collab_assigned`, `collab_mention` (Collab conversations) | mail | on | on | on |

Account-wide (`notification_settings`): `email_cadence` (`instant`, the default; `hourly`; `daily` at `digest_hour` in
`timezone`, default 9; `off`) and `paused_until` (no email, push or desktop notification while it is in the future;
notifications made meanwhile stay in the app and are never emailed or pushed later).

Delivery:
- `hd.Notify` stores `email_due_at` on the notification when its type's `email` channel is on and the cadence is not
  `off`: `created_at` + 5 minutes for `instant`, the next full hour for `hourly`, the next `digest_hour` for `daily`.
- A worker job sweeps every minute. When pacing allows (below), everything still waiting for that user in that workspace
  goes into **one** email: one notification gets its own email ("Maya assigned you #1095: SAML with Okta"); several get
  one grouped email. Its subject names the most pressing notification and counts the rest ("Maya Chen mentioned you
  #1095 and 4 more"); a summary line counts them by kind ("Since 14:05: 2 assigned, 1 mention, 3 replies", times in the recipient's `timezone` on their `User.time_format` clock); urgent ones
  lead under "Needs you now", then tickets, then the rest; a ticket's notifications are one line ("Dana Lee replied
  twice, Maya Chen mentioned you", with the newest excerpt); one person's assignments on 3 or more tickets are one line
  ("Maya Chen assigned you 37 tickets"). At most 20 lines, then "and N more". Digests use the same layout.

**Pacing** (`notification_email_state` per user and workspace, `notification_email_log`; `config.NotifyEmail`). A
notification email "conversation" with one person in one workspace:
- The first email after a quiet spell goes `delay` after the notification (5 minutes) if it is still unread.
- Each email starts a cooldown; what arrives meanwhile waits for one email at its end. While activity continues the
  cooldown grows (`cooldowns`, the last step repeating) and starts over after `quiet_reset` (2 hours) with nothing new.
- Urgent types (`mentioned`, `collab_mention`, `sla_breached`, `ai_handoff`, `ai_balance_out`, `auto_recharge_failed`, `ai_key_failed`)
  may cut a cooldown short, never sooner than `urgent_gap` after the last email and at most `urgent_per_day` times.
- `per_hour` and `per_day` cap a person's notification emails (all workspaces, digests included); past them everything
  rolls into the next allowed email.
- Nothing is emailed while the member was active in the app in that workspace within `active_window` (the web
  heartbeat, `POST /v1/me/heartbeat` with `OD-Workspace`, sets `members.active_at`).
- `instance_per_minute` caps notification emails across the instance; past it emails wait a minute.
- Nothing is dropped by pacing: a job that may not send records `next_check_at` and the sweep returns then. A new urgent
  notification brings `next_check_at` forward to its due time.

| `NOTIFY_EMAIL_PACING` | delay | cooldowns | quiet reset | urgent gap, per day | per hour, per day | instance per minute |
| --- | --- | --- | --- | --- | --- | --- |
| `strict` (default with billing on) | 5m | 15m, 30m, 1h | 2h | 10m, 6 | 4, 24 | 120 |
| `relaxed` (default otherwise) | 5m | 5m, 15m, 30m, 1h | 2h | 5m, 12 | 12, 100 | 60 |

Each value is overridable: `NOTIFY_EMAIL_DELAY`, `NOTIFY_EMAIL_COOLDOWNS` (e.g. `15m,30m,1h`), `NOTIFY_EMAIL_QUIET_RESET`,
`NOTIFY_EMAIL_URGENT_GAP`, `NOTIFY_EMAIL_URGENT_PER_DAY`, `NOTIFY_EMAIL_PER_HOUR`, `NOTIFY_EMAIL_PER_DAY`,
`NOTIFY_EMAIL_INSTANCE_PER_MINUTE`, `NOTIFY_EMAIL_ACTIVE_WINDOW` (5m).
- At send time each notification is checked again and dropped when it was read, the type's email channel is off, the
  pause is on, the user left the workspace, or its ticket was deleted or is no longer visible to the user (private
  teams). The address must be confirmed. A notification is emailed at most once (`emailed_at`, set in the transaction
  that queues the email).
- Notification emails carry `List-Unsubscribe: <{API_URL}/v1/notifications/unsubscribe/one-click?token=…>` and
  `List-Unsubscribe-Post: List-Unsubscribe=One-Click` (RFC 8058), and a footer link to `{APP_URL}/unsubscribe?token=…`
  and to `/settings/notifications`. The token is a `notification_unsubscribe` JWT (`contracts/tokens.md`). Emails about one
  ticket carry `References: <ticket-<ticket id>@<mail domain>>` so mail clients thread them per ticket.
- Contents are short and plain: who acted, the ticket number and subject, and at most a 140-character plain-text excerpt
  (mentions, customer replies). Never a full message, an attachment or anything secret.
- The unsubscribe link turns off email for every type in that workspace and records the choices from before
  (`notification_unsubscribes`); the settings page offers to restore them (`resubscribe`).
- Every platform email's footer names the sending company when `EMAIL_LEGAL_ENTITY` is set (with
  `EMAIL_LEGAL_COMPANY_NUMBER`, `EMAIL_LEGAL_PLACE_OF_REGISTRATION`, `EMAIL_LEGAL_ADDRESS`); unset, it names none.

### Helpdesk (owner: backend-helpdesk)
| method | path | notes |
| --- | --- | --- |
| GET | `/v1/tickets` | filters: `status` (csv), `priority` (csv), `severity` (csv of `sev1`..`sev4` and `none`), `assignee` (`me`/`none`/uuid), `team_id` (uuid, `mine` for the caller's teams, `none`), `brand_id`, `widget_id`, `mailbox_id`, `received_at` (the address a ticket was first written to: a mailbox's address or an alias, case-insensitive), `tag`, `channel`, `contact_id`, `view_id`, `q`, `spam`, `field.<key>` / `field.<key>[<op>]` (custom fields, below), `sort` (`last_message_at`/`created_at`/`priority`/`severity` (sev1 first, none last)/`due`/`field.<key>`), `order` |
| GET | `/v1/tickets/counts` | → `{ unassigned, mine, open, pending, on_hold, solved, spam, views: {<view_id>: n}, teams: {<team_id>: {open, unassigned}}, my_teams: {open, unassigned}, stats: {<row id>: {awaiting, urgent, overdue}} }`. `teams` lists every team the caller can see (open and pending, spam excluded); `my_teams` sums the caller's teams; Every count, the folders and `views` included, covers only tickets the caller can see (contracts/teams.md "Visibility"): a private team's tickets count for its members and for their assignee. `stats` breaks a sidebar row's tickets down, over the same tickets as its count: `ticket_folder:unassigned`, `ticket_folder:mine`, `ticket_folder:open`, `ticket_folder:pending` (pending and on hold), `team:<id>` for each team in `teams`, `team:mine` (as `my_teams`) and `view:<id>` for each view in `views`. `awaiting`: open or pending, and the customer's last message is the last message (within a second); `urgent`: urgent priority; `overdue`: not solved or closed, and the due time that applies has passed (first response while unanswered, else resolution) or an SLA breach is recorded |
| POST | `/v1/tickets` | `{subject, contact_id? , contact?: {email,name}, channel?: "email"\|"api", mailbox_id?, brand_id?, body_html, kind?: "reply"\|"note", priority?, severity?, assignee_id?, team_id?, tags?: string[], fields?: FieldValues, status?}`; an email ticket with a reply is sent to the contact (`422 mailbox_cannot_send` when its mailbox has `sending: "system_off"`; nothing is created). Without `team_id` the team comes from routing (contracts/teams.md "Routing"); `brand_id` defaults to the mailbox's brand, else the default brand (`mailbox_id` of another brand: `422`; an address the caller may not send from: `403 forbidden`). An assignee who is not on a private team is `422 not_on_team` |
| GET | `/v1/tickets/{id}` | id is uuid, or `#<number>` / numeric number |
| PATCH | `/v1/tickets/{id}` | `{subject?, status?, priority?, severity? /* null clears */, contact_id? /* a contact in this workspace */, assignee_id?, team_id?, mailbox_id? /* the reply address */, brand_id?, snoozed_until?, spam?, tags?: string[] /* replaces */, add_tags?: string[], remove_tags?: string[], custom?, fields?: Record<string, FieldValue \| null> /* merged; null clears */}`. Changing `contact_id` changes the recipient of future replies; existing messages stay as sent. It logs `contact_changed` and publishes `ticket.updated` with `contact_id` in `changes`. A live chat stays with the visitor who started it (the widget lists their chats by contact): changing its contact is `422`, and so is `contact_id` in a bulk patch. A status of `solved` or `closed` with an empty `required_on_solve` field is `422 field_required`. Setting `snoozed_until` without a `status` moves the ticket to `on_hold`; a status change that is not `on_hold` clears the snooze. Changing `priority`, `severity`, `team_id`, `spam` or labels removes Ollie's pending suggestion for that field (`custom.ai_suggestions`, contracts/ai.md). `team_id`, `mailbox_id` and `brand_id` follow contracts/teams.md "Reply address": an address of another brand is `422`, one the caller may not send from `403 forbidden`, an assignee not on the ticket's private team `422 not_on_team` (a team change alone unassigns them) |
| DELETE | `/v1/tickets/{id}` | soft delete |
| POST | `/v1/tickets/bulk` | `{ids, patch: {...PATCH body except contact_id}}` or `{ids, delete: true}` |
| POST | `/v1/tickets/{id}/merge` | `{into_id}` |
| GET | `/v1/tickets/{id}/messages` | ascending, all |
| GET | `/v1/tickets/{id}/variables` | agent+, `?mailbox_id=` → `{values, signature_html}`: the ticket's template values with the caller as the agent, and the signature their email reply would carry (contracts/templates.md) |
| POST | `/v1/tickets/{id}/render` | agent+. `{body_html}` → `{body_html}`: filled for this ticket and sanitized like a reply (inserting saved replies) |
| POST | `/v1/tickets/{id}/messages` | `{kind: "reply"\|"note", body_html, cc?, bcc?, attachment_ids?, status?: TicketStatus, mailbox_id?, from_note_id?}` → `Message`. `from_note_id` (replies only, a signed-in member) is the caller's own plain internal note without files the reply is written from: in the same step it becomes a marker where it was, `sent_as_reply_id` pointing at the reply and `deleted_at`/`deleted_by` saying when and by whom (`message.updated` with the marker, activity `note_sent_as_reply`), whatever `settings.message_delete` says. The note's own words are kept and hidden like a deleted message's (the reply may have been edited before it went): only admins read them, its mention notifications go and other notifications lose their preview. The marker cannot be edited, deleted, restored or sent again (`422`), an admin may purge it, and it is left out of what the AI, MCP and channels read; another member's note is `403`. A reply's template variables are filled when it is created, with the caller as the agent (contracts/templates.md); notes are not. `mailbox_id` (replies only) sends from that address and makes it the ticket's reply address (same rules as PATCH). `from_account: {account_id, email?, reply_to?}` (email replies only, a signed-in member, not with `mailbox_id`) sends the reply from one of the caller's own Collab addresses instead, leaving the ticket's reply address as it is (contracts/collab.md "Answering tickets"): `email` is one of the account's addresses that can send (left out: the one the caller last answered this ticket from in that account, else its default), `reply_to` is `"address"`, `"mailbox"` or an email address (left out: the ticket's reply address's `personal_replies.reply_to`, else `settings.collab_ticket_reply_to`). The ticket's reply address may override the workspace's rules and limit the addresses (`Mailbox.personal_replies`). `402 collab_required` without Collab, `409 collab_ticket_replies_off` while they are off for the ticket, `404` for an account the caller does not see, `403` for an API key, an AI client or Ollie, and `422 validation_failed` for an address that cannot send, a chat ticket or both `mailbox_id` and `from_account`. An email reply from a mailbox with `sending: "system_off"` is `422 mailbox_cannot_send` ("This mailbox has no way to send email yet. Connect Google or Microsoft, or add SMTP, in its settings.") and stores nothing; automations skip their email replies there, and the AI agent's answers wait as a suggested reply |
| PATCH | `/v1/tickets/{id}/messages/{messageId}` | agent, author only; `{body_html}` edits a plain internal note and returns `Message`. Replies and side-conversation messages return `422`; successful edits publish `message.updated` and `activity.created` (`note_edited`, message id only). Members it now mentions for the first time are notified; earlier mention notifications show the edited text |
| POST | `/v1/tickets/{id}/messages/{messageId}/retract` | agent; the author may take back their own reply on a chat ticket, admins any agent's. It becomes an internal note in place (`Message`, `kind: "note"`, `message.updated`): it leaves the visitor's widget (`message.withdrawn` on the contact topic; the team keeps the note), drops out of `message_count` and `last_message_preview`, no longer counts as the ticket's first response (`first_responded_at` moves to the next agent reply, or `null`), and is never sent as a chat follow-up. A reply already emailed to the visitor as a chat follow-up (`delivery_status` other than `none`) is `422`, as are a customer's message, an AI reply and a reply on any other channel: email is delivered when it is sent. Records `reply_retracted` with the message id only. The web app holds a reply for `settings.undo_send_seconds` (chat: `undo_send_chat_seconds`) before posting it, which is the way back before anything is sent |
| DELETE | `/v1/tickets/{id}/messages/{messageId}` | agent; deletes a message and leaves a marker: `deleted_at` and `deleted_by` are set, and for everyone but admins `body_html`, `body_text`, `subject`, `attachments` and `ai_action` come back empty. Admins may delete any message (a customer's included); an agent as their rule allows (`Member.message_delete`, else `settings.message_delete`): `own` (default) their own internal notes, `all` any agent's notes and replies, `off` none; never a customer's, a bot's or an automation's message. A signed-in member only: an API key gets `403`. A side conversation's message is `422`, as is a note sent as a reply (delete the reply). Returns 204, also when already deleted. Publishes `message.updated` with the marker to the team only (never the text, never public), removes its mention notifications, takes the preview out of other notifications about it, expires an action card still waiting on the visitor, and records `note_deleted` or `message_deleted` with the message id only. A deleted reply leaves `message_count`, `last_message_preview`, the visitor's widget (`message.withdrawn` on the contact topic), search, AI and MCP context, the quoted history of later emails and chat follow-ups, and the kept copies of emails sent after it (`GET /v1/messages/{id}/original`). A reply not sent yet (`pending`) is not sent: it is marked `failed` with "Deleted before it was sent." (one already being handed to the mailbox goes out and is recorded as `sent`); an email already delivered stays in the customer's inbox. Its stored files stay until it is purged, and only admins open them: the message's attachment links are signed apart while it is deleted, so a link handed out before is refused, as are other members and API keys. `GET /v1/events` replays earlier events about it as its marker. A copy already posted to a support channel follows edits, deletions and restores through its recorded provider message id (contracts/support-channels.md) |
| POST | `/v1/tickets/{id}/messages/{messageId}/restore` | admin, signed-in member; brings a deleted message back (`Message`), `422` when it is not deleted, was purged, or is a note sent as a reply. A reply that was deleted before it went out comes back `failed` ("Deleted before it was sent."), to send again by hand. Publishes `message.updated` and records `message_restored` |
| POST | `/v1/tickets/{id}/messages/{messageId}/purge` | admin, signed-in member; removes a deleted message's text, subject, files and the original email it came in for good, keeping the marker (`purged_at`). What was made from it goes too: its notifications, the text of Ollie's suggested reply to it or sent as it, an action card's summary and arguments, the ticket's AI summary (`custom.ai_summary`, written again on the next summary) and its search index. The ticket's own subject stays as it is, even when it came from the purged message (an email's subject): rename the ticket to change it. Events already delivered to webhooks and copies posted to support channels are not recalled. `422` when it is not deleted first; repeating it returns the marker. Records `message_purged` and an audit entry `message.purged` |
| GET | `/v1/tickets/{id}/activities` | ascending |
| GET | `/v1/tickets/{id}/draft` | viewer → `{draft: Draft \| null}` ("Drafts") |
| PUT | `/v1/tickets/{id}/draft` | agent (`tickets:write`). `{body_html}` → `Draft`; replaces the ticket's draft. Sanitized like a reply, `422` when empty |
| DELETE | `/v1/tickets/{id}/draft` | agent (`tickets:write`) → 204; idempotent |
| POST | `/v1/tickets/{id}/typing` | agent (`tickets:write`), signed-in member (an API key gets `403`) → 204. A teammate is writing a reply: on a chat ticket with a contact, the visitor's widget gets `"typing"` `{ticket_id, user: {id, name}}` (contracts/realtime.md "Live events"); on other tickets nothing happens. A ticket the caller cannot see is `404`. Clients send it at most every 2.5 s while typing |
| GET | `/v1/tickets/{id}/threads` | viewer → `{data: Thread[]}`, oldest first ("Threads") |
| POST | `/v1/tickets/{id}/threads` | `{to: string[] /* 1..20 */, cc?: string[], subject, body_html, attachment_ids?, mailbox_id?, include_history?: boolean, forward_attachment_ids?: string[]}` → 201 `{thread: Thread, message: Message}` |
| POST | `/v1/tickets/{id}/threads/{threadId}/messages` | `{body_html, attachment_ids?}` → 201 `Message` |
| PATCH | `/v1/tickets/{id}/threads/{threadId}` | `{status: "open"\|"closed"}` → `Thread` |
| GET | `/v1/messages/{id}/original` | → `{full_html}`: an inbound email in full (with its quoted history); for an email we sent (a reply, a side conversation's message), the copy kept when it went out: the layout, the reply, the signature and the quoted conversation, sanitized like every stored email, its inline images pointing at their attachments. A copy too large to keep (over 1 MB) is not kept. The kept copies of emails sent after a reply are dropped when that reply is deleted or purged (`message.updated`), since their quoted conversation may hold it; the body when there is none |
| CRUD | `/v1/contacts`, `/v1/contacts/{id}` | list supports `q`, `company_id`, `tag`, `field.<key>`. Write bodies also take `tags?: string[]` (replaces), `add_tags?`, `remove_tags?` (names, as for tickets) and `fields?` (merged; null clears) |
| GET | `/v1/contacts/{id}/tickets` | list |
| CRUD | `/v1/companies`, `/v1/companies/{id}` | list supports `q`, `tag`, `field.<key>`; write bodies take `tags?`, `add_tags?`, `remove_tags?`, `fields?` as contacts |
| PUT/DELETE | `/v1/contacts/{id}/avatar`, `/v1/companies/{id}/avatar` | agent (`contacts:write`). PUT multipart `file` (image, 5 MB) → `Contact` / `Company` with `avatar_source: "upload"`, which automatic sources never replace. DELETE removes it and looks up the automatic sources again. Audit `contact.avatar_updated` / `company.avatar_updated` (and `_removed`); publishes `contact.updated` / `company.updated` |
| POST | `/v1/contacts/{id}/avatar/refresh`, `/v1/companies/{id}/avatar/refresh` | agent → 202. Looks the automatic sources up again now (no effect on an upload) |
| GET | `/v1/tags` | `?q=&group_id=<uuid\|none>&archived=true\|false\|any` (default `any`) `&applies_to=tickets\|contacts\|companies` → `{data: Tag[], next_cursor: null}` (all of them, ordered by group position, then position, then name) |
| POST | `/v1/tags` | admin. `{name, color?, description?, group_id?, applies_to?, position?}` → 201 `Tag`. `color` defaults to a palette colour picked from the name, `applies_to` to all three, `position` to the end. `409 conflict` on a name taken (case-insensitive) |
| GET | `/v1/tags/{id}` | → `Tag` |
| PATCH | `/v1/tags/{id}` | admin. `{name?, color?, description?, group_id? /* null ungroups */, position?, archived?, applies_to?}` → `Tag`. A rename also rewrites automations and views that name the label |
| DELETE | `/v1/tags/{id}` | admin → 204; removes the label from every ticket, contact and company |
| POST | `/v1/tags/{id}/merge` | admin. `{into: string /* tag id */}` → the `into` `Tag`. Moves every use onto `into` (respecting `into`'s exclusive group), rewrites automations and views, deletes this label |
| POST | `/v1/tags/reorder` | admin. `{ids: string[]}` → `{data: Tag[]}`; sets `position` to each id's index (max 1000 ids, all in the workspace) |
| CRUD | `/v1/tag-groups`, `/v1/tag-groups/{id}` | admin writes. `{name, color?: LabelColor \| null, exclusive?: boolean, position?}` → `TagGroup`; list is ordered by position. Delete ungroups its labels. At most 50 groups |
| POST | `/v1/tag-groups/reorder` | admin. `{ids: string[]}` → `{data: TagGroup[]}` |
| GET | `/v1/custom-fields` | `?entity=ticket\|contact\|company&archived=true\|false\|any` (default `any`) → `{data: CustomField[], next_cursor: null}` ordered by entity, position |
| POST | `/v1/custom-fields` | admin. `{entity, key?, name, type, options?, required_on_solve?, description?, position?}` → 201 `CustomField`. `key` defaults to a slug of `name`; `409 conflict` if the key is taken for that entity |
| GET | `/v1/custom-fields/{id}` | → `CustomField` |
| PATCH | `/v1/custom-fields/{id}` | admin. `{name?, options?, required_on_solve?, description?, position?, archived?}` → `CustomField`. `entity`, `key` and `type` never change (`422`). Removing an option clears it from stored values |
| DELETE | `/v1/custom-fields/{id}` | admin → 204; removes the field's values from every record |
| POST | `/v1/custom-fields/reorder` | admin. `{entity, ids: string[]}` → `{data: CustomField[]}` (that entity's fields) |
| CRUD | `/v1/saved-replies` | `body_html` takes template variables (contracts/templates.md); an unknown variable or a tag that does not parse is `422`. Body takes `team_id?` (null for everyone). List `?ticket_id=` (replies of no team and of the ticket's team) or `?team_id=<id\|none>`. A team's replies are written by admins and the team's leads and members (`403 forbidden` otherwise); a private team's are listed only to its members (others get `404`) |
| CRUD | `/v1/views` | `{name, filters, shared, team_id?}`. `team_id` shares the view with that team's members (admins and the team's leads write it; with `shared: true` it is `422`). List `?team_id=` |
| CRUD | `/v1/sla-policies` | body takes `business_hours?: boolean`, `team_id?` (null for every team). List `?team_id=` |
| GET/PUT | `/v1/business-hours` | GET members → `BusinessHours`; PUT admin, full `BusinessHours` (`timezone` sets `settings.timezone`) → `BusinessHours`. Audit `business_hours.updated` |
| GET/PATCH | `/v1/assignment` | GET members → `Assignment`; PATCH admin `{mode?, rules? /* replaces */, auto_away_minutes?, new_ticket_notify?}` → `Assignment` (another `new_ticket_notify` is `422`). Audit `assignment.updated {mode, rules, auto_away_minutes, new_ticket_notify}` |
| GET/PATCH | `/v1/triage` | GET members → `TriageSettings`; PATCH admin `{auto_triage?, fields?, assign_after_triage?, threshold?}` → `TriageSettings` (stored in `settings.ai`, contracts/ai.md). Audit `triage.updated` |
| CRUD | `/v1/automations` | |
| CRUD | `/v1/imports`, `/v1/imports/{id}` | admin users only (reads also API keys with `settings:read`). History imports from Zendesk, Help Scout, Freshdesk, Intercom, Front and CSV files: check, CSV upload, start, pause, resume, cancel, sync, undo and credential removal; see `contracts/imports.md` |
| CRUD | `/v1/kb/collections`, `/v1/kb/articles` | articles list supports `collection_id`, `status`, `q` and `for_brand_id` (what that brand shows: its own articles and those of every brand; the composer's and assistant's scope for a ticket's brand); both lists `brand_id` (a brand id, or `none` for what every brand shows; an article's brand is its collection's when that has one). Create and PATCH take `brand_id` (a brand of the workspace, or null) |
| GET | `/v1/search?q=&brand_id=` | → `{tickets: Ticket[], contacts: Contact[], articles: KbArticle[]}` (5 each). `brand_id` (optional) narrows the articles to what that brand shows: its own and those of every brand |
| GET | `/v1/reports/overview?from=&to=&team_id=&brand_id=` | → `{created, solved, open_now, first_response_p50_minutes, resolution_p50_minutes, csat_avg, csat_count, sla_breached, by_day: [{date, created, solved}], by_channel: {...}, by_severity: Record<Severity \| "none", number> /* tickets created in the range */, by_agent: [{user, solved, first_response_p50_minutes}], by_team: [{team_id: string \| null, created, solved, open_now, first_response_p50_minutes}], by_brand: [{brand_id, created, solved, open_now, first_response_p50_minutes}]}`. `team_id` (a team, or `none`) and `brand_id` narrow every figure. Private teams' tickets are counted, with no subjects or contacts |
| POST | `/v1/attachments` | multipart `file` → `Attachment` (unattached, linked when sent). Only whoever uploaded it (that user, or that API key) may send it in `attachment_ids` (`422` otherwise) |
| GET | `/v1/attachments/{id}` | → 302 to signed URL or streamed. For img tags, the links messages carry are signed (`?exp=&sig=`) and work for one to two hours. A file on a ticket the caller may not see is `404` (contracts/teams.md "Visibility"); an upload not sent yet is its uploader's alone (`404` for anyone else) |

#### Threads

A third-party thread is an email conversation on a ticket with someone outside the team, such as
a supplier, a logistics partner or a payment provider, to ask what the team needs before it can
answer the customer. The app calls it a side conversation. It stays internal to the ticket:

```ts
type Thread = { id: string; ticket_id: string; subject: string; to: string[]; cc: string[];
  mailbox_id: string | null /* the address it is sent from; null once that mailbox is removed */;
  status: "open" | "closed"; include_history: boolean; created_by: UserRef | null; message_count: number;
  last_message_at: string; created_at: string; updated_at: string };
```

- Its messages are notes (`kind: "note"`) with `thread_id` set: outbound ones are the team's, sent by
  email to the thread's `to` and `cc`; inbound ones are the answers. They are never shown or sent to
  the customer, and they are not the conversation with them: no `last_message_at`, `message_count`,
  first response, SLA, CSAT, waiting time or report counts them, and no automation, auto-reply or AI
  answer runs on them.
- The team's messages go out from the thread's `mailbox_id`, an address of the ticket's brand the
  caller may send from (`403 reply_address_forbidden` otherwise; default the ticket's reply address,
  then its brand's address for its team). They thread with the thread's own messages and carry the
  thread's subject (`Re:` once answered), the address's signature and design, filled without the
  customer's or the ticket's details (their `contact.*`, `company.*` and `ticket.*` variables stay
  empty). The message's own tags (a saved reply's) are filled the same way when it is written.
  Delivery is reported on the message as for replies.
- Nothing of the customer's conversation goes along unless asked for when the thread starts:
  `include_history` quotes the ticket's last 10 public replies (as the `templates` history, whatever
  the design's history setting) under the first message only; `forward_attachment_ids` sends
  attachments of the ticket's public replies along with it, as copies on the thread's message that
  share the stored file. Notes, other threads and inbound mail that failed sender checks never go.
  An id that is not an attachment of the ticket's public replies, or is one of mail that failed
  sender checks, is a `422`; attachments of one message are limited to 25 MB in all.
- An inbound email that answers one of a thread's messages (`In-Reply-To` / `References`, or our
  Message-ID format) lands in that thread, whoever sends it, as an inbound note with the email's
  sender, recipients and attachments. The sender does not become a contact. A ticket that is
  `pending` or `on_hold` opens again (`status_changed` with reason `thread_replied`) unless the email
  failed sender checks; a solved or closed one keeps its status. The assignee gets a `thread_replied` notification.
- A new message in a closed thread opens it again. Closing a thread only marks it done.
- Merging a ticket moves its threads to the target with their messages.
- Events: `thread.created` and `thread.updated` (contracts/events.md); the messages themselves are
  `message.created` / `message.updated` like any note.

#### Drafts

A ticket has at most one shared draft: a pending reply body staged for whoever opens the ticket
next, by a teammate or by an API client such as an AI agent over MCP (`draft_reply`,
contracts/agent.md). It is the ticket's, not the staging credential's: every teammate who can see
the ticket sees the same text in their composer, and the app says who staged it.

```ts
type Draft = { ticket_id: string; body_html: string; body_text: string;
  by: { type: "user" | "api"; name: string; user?: UserRef /* type user */ }; updated_at: string };
```

- Whoever sends it edits it in the composer and sends it as their own reply through
  `POST /v1/tickets/{id}/messages`, like any reply. Nothing is attributed to who staged it.
- A reply going out on the ticket clears the draft, whoever or whatever sends it: the composer,
  a support channel thread, Ollie. A customer's message or a note leaves it. `PUT` replaces it;
  `DELETE` discards it without sending.
- This is not Ollie's suggested reply (contracts/agent.md "Email"), which answers one customer
  message and is sent as written, attributed to the agent.
- Event: `ticket_draft.updated` (contracts/events.md) with the draft, or `null` once it is gone.

#### Labels and custom fields

Labels are the API's `tags` (routes, body members and event names keep that word).
- Names: 1..50 characters, letters, digits, spaces and `- _ . / : & + #` (no `<`, `>`), unique per workspace ignoring case.
  Descriptions 0..300 characters, no `<` `>` or control characters. Group names 1..50, same rules. At most 1000 labels.
- Applying labels by name (`tags`, `add_tags`, `remove_tags`, the `add_tag` action, AI triage) creates missing labels unless
  `settings.label_creation` is `"admins"`. A label whose `applies_to` lacks the record's kind is `422` (`tags`/`add_tags`); automations
  and triage skip it. Archived labels are hidden from pickers (`?archived=false`) but stay on records and still apply by name.
- **Exclusive groups**: a record holds at most one label of an exclusive group. Adding one removes the others of that group
  (activity `untagged` for each); when one write names several labels of the same exclusive group, the last one named wins. This holds
  for ticket, contact and company writes, bulk, automations and AI triage (enforced in `internal/domain`). Making a group exclusive does
  not rewrite existing records; the rule applies on their next label change.
- `usage` counts the records (not deleted tickets) carrying the label; `tickets` leaves out private teams' tickets (contracts/teams.md).
- New workspaces get coloured labels plus an exclusive **Type** group: Question (blue), Problem (orange), Bug (red),
  Feature request (violet), Billing (green), and ungrouped VIP (amber) and Feedback (teal).

Custom fields (`/v1/custom-fields`):
- `key`: `^[a-z][a-z0-9_]{0,39}$`, unique per entity, immutable (as are `entity` and `type`). `name` 1..60 and option labels 1..60
  without `<` `>` or control characters; `description` 0..300. At most 100 fields per entity and 200 options per field. Option
  `id` is `^[a-z0-9_-]{1,40}$`, defaulting to a slug of its label; `color` defaults to `gray`.
- Values (`fields` on create and PATCH, merged; `null` clears; unknown keys are `422`): `text` string 0..1000 (single line),
  `long_text` 0..10000, `number` finite JSON number, `select` an option id, `multi_select` option ids (unique), `date` `YYYY-MM-DD`,
  `checkbox` boolean, `url` an http(s) URL up to 2000, `email` an address (lowercased), `user` a workspace member's user id.
  An empty string or empty list clears too. Archived fields keep their values but refuse changes (`422`).
- `required_on_solve` (tickets only): moving a ticket to `solved` or `closed` (PATCH, bulk, a reply with `status`) while the field
  is empty (absent; for `checkbox`, not `true`) answers `422 field_required` with `detail` naming the field and
  `errors: [{location: "body.fields.<key>", message}]` (one per missing field). Archived fields, automations, merges and the
  auto-close job are not held back.
- Filters: `field.<key>=<v>` uses the type's default op (`eq` for text-like, number, date, checkbox; `any` for select, multi_select
  and user), or `field.<key>[<op>]=<v>`. Ops by type: text, long_text, url, email: `eq ne contains not_contains set not_set`
  (case-insensitive); number, date: `eq ne gt gte lt lte set not_set`; checkbox: `eq ne set not_set` (`eq false` also matches unset);
  select, user: `eq ne any none set not_set`; multi_select: `any all none eq ne set not_set` (`eq` = has that option). `any`, `all`
  and `none` take a comma separated list; `user` accepts `me`. Views store the same as `filters.fields`. Unknown keys or ops are `422`.
- Sort: `sort=field.<key>` for text, number, date, checkbox and select (option order) fields; records without a value sort lowest.
- Search: `q` on tickets, contacts and `/v1/search` also matches text-like field values and select option ids.
- Changes log a `field_changed` activity per key on tickets and add `"fields"` to `ticket.updated` `changes`; contact and company
  events carry the new `fields`. Webhooks receive the same objects.
- Roles: definitions (labels, groups, fields) are managed by admins (API keys `settings:write`); everyone reads them (viewers; keys with
  `settings:read`, `tickets:read` or `contacts:read`). Agents apply labels and set values (`tickets:write` for tickets,
  `contacts:write` for contacts and companies). Definition changes are audit-logged (`tag.*`, `tag_group.*`, `custom_field.*`).

Automations: conditions take `field.<key>` (ticket field) and `contact.field.<key>` (the ticket contact's field) with the ops above
(`equals`/`not_equals` are `eq`/`ne`); `set`/`not_set` ignore `value`. The `set_field` action sets ticket field `field` to `value`
written as a string (`"12.5"`, `"true"`, `"2026-10-11"`, comma separated option ids; `""` clears); it is validated when the
automation is saved and skipped at run time if the field was archived or deleted since.

#### Profile pictures and logos

- Every `avatar_url` points at `GET /v1/images/{id}` on this API: pictures from any source are fetched and processed
  server-side and never hot-linked. Processing decodes PNG, JPEG, GIF (first frame) or WebP only (by content, not name or
  header), refuses anything else (SVG, HTML, ICO with bitmap entries), caps input at 5 MB and 4096 px a side (16 Mpx), applies
  EXIF orientation, crops people to a square (logos are fitted, not cropped), resizes to 256 and 64 px and re-encodes PNG
  without metadata. Uploads answer `413 too_large`, `422 unsupported_image` or `422 image_too_large`.
- Sources, highest first. Agents: `upload` > `oauth` (the Google or GitHub picture at sign-in; Microsoft and OIDC sign-ins
  carry none). Contacts: `upload` > `widget` (verified widget sessions) > `gravatar` (only with `settings.gravatar`, looked up
  as `sha256(lowercased email)` with `d=404`). Companies: `upload` > `domain` (with `settings.domain_logos`: the domain's
  `apple-touch-icon`, manifest icons, `<link rel=icon>` and `/favicon.ico` PNG entries; or the logo provider the instance
  configures). A workspace logo is `upload` only.
- Fetches use https only, refuse private, loopback and link-local targets (re-checked on each redirect, at most 3), take at
  most 10 seconds, read at most 5 MB and accept only image content types (HTML for the domain's home page, at most 512 KB).
- Automatic pictures are refreshed every 30 days. A miss (404, nothing usable, an error) is remembered and retried after
  1, 2, 4, 8, 16, then every 30 days. New contacts and companies are looked up within minutes; turning `gravatar` on looks up
  every contact without a picture, turning it off removes Gravatar pictures.

#### Assignment

- Mode per ticket: the rule for its team, else the rule for its mailbox, else `settings.auto_assign`. A team's rule is also its
  `assign_mode` (contracts/teams.md). Modes: `none`,
  `round_robin` (the next eligible agent after the last one picked in that pool), `load_balanced` (the eligible agent with the
  fewest open and pending assigned tickets; ties go round robin).
- Pool: the `lead`s and `member`s of the ticket's team, else of its mailbox's team, else every owner, admin and agent.
  Observers and viewers are never assigned. Eligible: `presence` is `online` and `open_tickets < max_open_tickets` (when set). With nobody eligible the ticket
  stays unassigned.
- It runs in the ticket trigger after automations, on `ticket_created`, on `customer_replied` and when the team changes
  (by an agent, an automation or a deleted team's `move_to`), for tickets that are unassigned, not spam and not solved or closed. The `assigned` activity has actor `system` and `via` the mode.
- `presence` is `away` when `availability` is `online` but `auto_away_minutes` > 0 and the member was not seen (sign-in,
  token refresh, `POST /v1/me/heartbeat`) for that long. `auto_away_minutes`: 0 or 10..1440, default 0.
- Rules: at most 200, one per target, targets must be the workspace's mailboxes and teams (deleting one deletes its rule).
- New tickets nobody is assigned to are announced with a `new_ticket` notification, in the `ticket_created` trigger
  after auto-assignment: only a ticket still unassigned, not spam, not solved or closed and not handed off by the bot
  (`custom.ai_handoff`). The person who created it is never told. Who hears follows `settings.new_ticket_notify`, for
  the ticket's team, else its mailbox's team (the assignment pool):
  - `auto` (the default): the team's leads and members (not observers or viewers), or every owner, admin and agent
    when there is no team, when that is at most 5 people; when more, the team's leads, or the owners and admins when
    there is no team or it has no lead (at most 5).
  - `everyone`: the team's leads and members, or every owner, admin and agent (at most 50).
  - `leads`: the team's leads, or the owners and admins (at most 50).
  - `off`: nobody.
  A team with nobody to work it falls back to its leads, else the owners and admins. `hd.Notify` still skips anyone who
  may not see a private team's ticket.
- A customer's reply on a ticket nobody is assigned to (`customer_replied` trigger, after auto-assignment) goes, as
  `customer_replied`, to the same people under the same rules (not spam, solved, closed or with the answer bot); on an
  assigned ticket it goes to the assignee.
- While the answer bot takes a new ticket on, nobody hears until it hands over (the `ai_handoff` notification): a chat
  when the instance has AI and the workspace's `ai.enabled` and `ai.widget_answers` are on; an email to an address with
  `ai_answers` on and `ai_answers_mode` `send` (Ollie replies itself; with `draft` people hear at once), within the
  hours it answers (`ai_answers_outside_hours`: only while business hours are closed), unless the sender is one of the
  workspace's members or addresses.

#### Triage

`/v1/triage` is the one place to set up Ollie's triage: `auto_triage` (`settings.ai.auto_triage`), `fields`
(`settings.ai.triage_fields`, default `priority`, `severity` and `labels`: `priority` lets triage raise priority, `severity`
lets it set or raise severity, `labels` lets it add labels, `team` lets it set the team of a ticket that has none; sentiment,
language and category are always recorded), `threshold` (`settings.ai.triage_threshold`, 0.5..0.99, default 0.7) and
`assign_after_triage` (`settings.ai.assign_after_triage`, default false): after Ollie triages a new ticket, it is assigned by
the resolved mode (of the team triage picked, when it picked one), or `load_balanced` when that mode is `none`, with actor
`ai` and `via: "triage"`. A decision whose confidence is below `threshold` is not applied; it is kept as a suggestion an
agent accepts or dismisses (contracts/ai.md "Decisions"). `engine` reports the decision engine: `typesafe` (Jev by
TypeSafe, calibrated confidence), `llm` (the configured AI model's structured output, applied without a confidence gate) or
`none`.

#### Business hours

`BusinessHours` lives in `settings.business_hours`. Up to 3 ranges per day, `start < end`, on the quarter hour (`24:00` ends a
day); up to 100 holidays (closed all day). An SLA policy with `business_hours: true` counts its minutes only while the
workspace is open, in its timezone; with business hours off or an empty week it counts every minute. A ticket of a team with
its own `business_hours` counts those (in the team's timezone, else the workspace's). Due dates are computed when the policy
is applied (ticket created, priority or team changed).

#### Onboarding

Steps, in order, and when each is done: `connect_mailbox` a mailbox exists; `invite_teammate` another member or a pending
invitation; `upload_photo` the caller has a picture; `install_widget` a widget session was ever opened; `publish_article` a
published article; `setup_triage` auto-assignment is not `none` anywhere or auto-triage is on; `create_label` a label other
than the seeded ones; `try_ollie` any Ollie feature was used in the workspace; `setup_ai_agent` (admin) the AI agent's setup
checklist is complete (`GET /v1/ai/agent/setup`, `contracts/agent.md` "Setup"). Titles are English copy the app may override.

### Public, no auth (owner: backend-helpdesk)
| method | path | notes |
| --- | --- | --- |
| GET | `/v1/public/help/{workspace_slug}` | `{title, collections: KbCollection[], featured: KbArticle[], locale, logo_url: string \| null, favicon_url: string \| null}` published only. `{workspace_slug}` is a brand slug (the default brand's is the workspace slug); 404 unless that brand's help center is on. Lists only the brand's collections and articles and those of every brand. `title` is the brand's help center title, else its name; `locale` the brand's, else the workspace's; `logo_url` its help center logo, else its logo, else the workspace logo; `favicon_url` its help center favicon |
| GET | `/v1/public/help/{workspace_slug}/collections/{slug}` | `{collection, articles}`, the brand's and shared only |
| GET | `/v1/public/help/{workspace_slug}/articles/{slug}` | `KbArticle`, increments views; the brand's and shared only |
| GET | `/v1/public/help/{workspace_slug}/search?q=` | `KbArticle[]`, the brand's and shared only |
| POST | `/v1/public/csat/survey` | `{token}` → `{workspace_name, brand_color, ticket: {number, subject}, score: number \| null, comment, locale, locale_fixed}`; `locale_fixed` true when the workspace fixes a set `locale`: the page uses it and offers no other |
| POST | `/v1/public/csat/rating` | `{token, score: 1..5, comment?}` records the score and answers the same survey object. The token rides in bodies only, never an API URL |

### Widget (owner: backend-helpdesk; `Authorization: Visitor <token>` except session)
| method | path | notes |
| --- | --- | --- |
| GET | `/v1/widget/config?key=<widget_key>` | `WidgetConfig` below; any widget's key (contracts/teams.md) |
| POST | `/v1/widget/session` | `{key, visitor_token?, email?, name?, user_id?, user_hash?, avatar_url?}` → `{visitor_token, contact: ContactRef, ws_token, gateway_url}`; `user_hash` = hex HMAC-SHA256(widget_secret, user_id) marks the session verified, with the secret of the widget whose `key` it is. A session belongs to that widget; a `visitor_token` of another widget of the workspace gives a new session of this widget for the same contact. `avatar_url` (https) is used only on a verified session: it is fetched server-side as the contact's picture (`avatar_source: "widget"`) unless an agent uploaded one. A session works while used within 90 days, and a verified one for 30 days from its start (the host page verifies the visitor again, which starts a fresh one). A verified session is never resumed without `user_hash`: that request starts an anonymous session |
| DELETE | `/v1/widget/session` | → 204: ends the visitor's session at once (the widget's `shutdown`, when the host page signs its user out) |
| GET | `/v1/widget/conversations` | visitor's chat tickets of the session's widget's brand `{id, subject, status, last_message_preview, last_message_at, ai_handoff}` (`ai_handoff`: the visitor asked for a person, contracts/ai.md). Conversations of another brand answer 404 |
| POST | `/v1/widget/conversations` | `{body, attachment_ids?, page_url?, topic_id?}` → conversation + first message. `body` may be empty when `attachment_ids` has a file; the conversation is then named after the first file. `page_url`: the host page (https, up to 500 chars, no fragment), stored as the ticket's `page_url` and given to routing rules and the AI agent. `topic_id`: one of `WidgetConfig.topics` (422 otherwise). The ticket gets the widget's brand and `widget_id`, and its team by routing (contracts/teams.md "Routing") |
| GET | `/v1/widget/conversations/{id}/messages` | public messages only (no notes). Counts as the visitor being on the page (contracts/teams.md "Chat follow-up") |
| POST | `/v1/widget/conversations/{id}/seen` | → 204. The widget sends it while the conversation is on screen and an agent's reply arrives, so the reply is not also emailed (contracts/teams.md "Chat follow-up"). 30 a minute per session |
| POST | `/v1/widget/conversations/{id}/typing` | → 204. The visitor is writing: agents on the ticket get `"customer_typing"` `{ticket_id, contact_id}` (contracts/realtime.md "Live events"), only those who may see the ticket. The widget sends it at most every 3 s while typing; 30 a minute per session |
| POST | `/v1/widget/conversations/{id}/messages` | `{body, attachment_ids?, page_url?}` (`body` may be empty with a file) (`page_url`: the host page, https, up to 500 chars, given to the AI agent) |
| POST | `/v1/widget/conversations/{id}/handoff` | → 204; ask for a person while the AI agent handles the conversation (contracts/agent.md) |
| POST | `/v1/widget/conversations/{id}/actions/{action_id}` | `{decision: "confirm" \| "decline"}` → 204 (contracts/agent.md "Visitor actions") |
| POST | `/v1/widget/conversations/{id}/messages/{message_id}/rating` | `{rating: "up" \| "down" \| null}` → 204 (contracts/agent.md "Ratings") |
| POST | `/v1/widget/attachments` | multipart. `403 attachments_off` while the session's widget has both `attachments` and `voice_messages` off, and so is a message or new conversation with `attachment_ids`. With `voice_messages` on, a recording is taken besides the widget's files: a `webm`, `ogg` or `m4a` (the types a browser records in) whose header declares sound and no picture, else `422 file_type_not_allowed`, of at most 32 KB per second of `voice_max_seconds` plus 64 KB, else `413 voice_too_long`. It is stored as `audio/webm`, `audio/ogg` or `audio/mp4`. A message or new conversation checks its `attachment_ids` again by the same rules |
| GET/PATCH | `/v1/widget/settings` | admin, agent-side: `WidgetSettings` below of the default brand's first widget (`widgets.settings`, mirrored to workspace `settings.widget`); PATCH merges the given keys, audit `widget.settings_updated` with the changed keys, publishes `widget.updated`. Every widget: `/v1/widgets` (contracts/teams.md) |

```ts
interface WidgetSettings {
  enabled: boolean
  greeting: string                      // up to 300 chars, plain text with newlines
  intro: string                         // under the greeting at the top of a new chat; up to 200 chars, plain text with newlines; "" = the translated default
  chat_welcome: boolean                 // default true: a new chat opens with the greeting and intro; false opens it empty
  require_email: boolean
  allowed_origins: string[]             // at most 20
  // Appearance. Every key has a default, so a stored settings.widget without it reads the same.
  color: string                         // "#rrggbb" launcher and accent colour; "" = the workspace brand_color
  theme: "auto" | "light" | "dark"      // default "auto": follows the visitor's system
  position: "right" | "left"            // default "right"
  offset_x: number                      // 0..200 px from the side, default 20
  offset_y: number                      // 0..200 px from the bottom, default 20
  launcher: "icon" | "label"            // default "icon"; "label" shows the icon and launcher_label
  launcher_label: string                // up to 24 chars; "" = the translated "Chat with us"
  radius: "square" | "soft" | "round"   // corner style of the panel, bubbles and launcher, default "soft"
  title: string                         // header title, up to 40 chars; "" = the workspace name
  subtitle: string                      // under the title, up to 80 chars; "" = the translated reply-time line
  show_team: boolean                    // up to 3 team avatars in the header, default true
  suggestions: string[]                 // at most 4 prompts of 2..60 chars on the home screen; a tap starts a conversation with it
  sound: boolean                        // a soft sound for a new reply while the page is hidden, default false
  topics: { id: string /* kept when sent, else generated; 1..40 of A-Z a-z 0-9 _ - */; label: string /* 2..40 */; team_id: string | null /* a team of the workspace */ }[]  // at most 8
  offline_message: string               // up to 300 chars, plain text with newlines; "" = the translated default
  email_follow_up: boolean              // default true: agents' chat replies also go by email to a visitor with an email who is not on the page (contracts/teams.md "Chat follow-up")
  attachments: boolean                  // default true: visitors may send files
  attachment_types: string[]            // ids of the file type catalog below visitors may send; default [] = any file but programs and scripts
  voice_messages: boolean               // visitors may record voice messages; default false
  voice_max_seconds: number             // 5..300 (422 otherwise); default 60
}

// GET /v1/widget/config. Public; Origin checked against the widget's allowed_origins.
interface WidgetConfig {
  workspace_name: string                // the widget's brand name (the workspace name for the default brand)
  brand_color: string | null            // the widget's color, else the brand's, else the workspace brand_color
  greeting: string; require_email: boolean
  intro: string                         // under the greeting in a new chat; "" = the translated default
  chat_welcome: boolean                 // false: a new chat opens empty, without the greeting and intro
  language: string                     // the brand's, else the workspace's locale when the workspace fixes it (locale_fixed); used before the visitor's browser languages, after the page's data-lang or opendesk("locale"); "" = none
  help_center_url: string | null        // the brand's help center url (contracts/teams.md) when its help center is on
  locale: string | null                 // the brand's, else the workspace's
  avatar_url: string | null             // the widget's avatar, else the brand logo, else the workspace logo
  launcher_icon_url: string | null
  theme: "auto" | "light" | "dark"; position: "right" | "left"; offset_x: number; offset_y: number
  launcher: "icon" | "label"; launcher_label: string; radius: "square" | "soft" | "round"
  title: string                         // the widget's title, else the brand name
  subtitle: string; suggestions: string[]; sound: boolean
  attachments: boolean                  // visitors may send files; the widget hides attaching while false
  attachment_extensions: string[] | null // extensions of the widget's attachment_types, for the file picker; null = any file but programs and scripts; [] while attachments is false
  voice_messages: boolean               // visitors may record voice messages, whether or not attachments is on
  voice_max_seconds: number             // the longest voice message; the widget stops recording there, and the server bounds a recording's size by it
  team: { name: string /* first name */; avatar_url: string | null }[]  // up to 3 non-viewer members of the widget's team (else of the workspace), available first; [] when show_team is off
  ai_answers: boolean                   // the AI agent handles new conversations (contracts/agent.md)
  bot: { id: string | null; name: string; role: string; avatar_url: string /* a PNG of the look, or the picture */; look: BotLook; greeting: string /* "" = greeting */ }
                                        // the widget's bot, else the default bot (contracts/bots.md)
  topics: { id: string; label: string }[]  // ask before the first message; send the pick as topic_id
  open: boolean                         // within the business hours of the widget's team (when it has its own), else the workspace's; true without hours
  offline_message: string               // shown while closed; "" = the translated default
  brand: { name: string; color: string /* the brand's colour, else the workspace brand_color */ }
}
```

File types a widget may take (`attachment_types`; `internal/helpdesk/hd/filetypes.go`, mirrored in
`web/src/components/widget/fileTypes.ts`). A visitor's upload is taken only when its extension is one of a
picked type's and its first bytes are that type's; anything else is `422 file_type_not_allowed`, at upload
and again, by the stored bytes, when a message or new conversation attaches it. With no types picked any
file is taken. Programs and scripts are refused whatever they are named (`422 file_type_refused`), and
agents' uploads are not limited by a widget. Stored ids this version does not know are dropped; a list of
only those takes no file.

| group | id: extensions, leading bytes |
| --- | --- |
| image | `png`: .png, PNG signature; `jpg`: .jpg .jpeg, `FF D8 FF`; `gif`: .gif, `GIF87a`/`GIF89a`; `webp`: .webp, RIFF `WEBP`; `heic`: .heic .heif, `ftyp` with a HEIF brand |
| document | `pdf`: .pdf, `%PDF-`; `txt`: .txt and `csv`: .csv, text without control characters (any 8-bit encoding, or UTF-16 with a byte order mark); `rtf`: .rtf, `{\rtf`; `doc`: .doc, OLE; `docx`: .docx and `odt`: .odt, ZIP |
| spreadsheet | `xls`: .xls, OLE; `xlsx`: .xlsx and `ods`: .ods, ZIP |
| presentation | `ppt`: .ppt, OLE; `pptx`: .pptx and `odp`: .odp, ZIP |
| media | `mp4`: .mp4 .m4v, `mov`: .mov and `m4a`: .m4a, `ftyp`; `mp3`: .mp3, `ID3` or an MPEG frame; `wav`: .wav, RIFF `WAVE`; `webm`: .webm, EBML `1A 45 DF A3`; `ogg`: .ogg .oga .opus, `OggS` |
| archive | `zip`: .zip, ZIP |

Text settings (`launcher_label`, `title`, `subtitle`, `suggestions`) are trimmed, have
runs of whitespace collapsed, and refuse `<`, `>` and control characters (`422 validation_failed`).
`color` must be `#rrggbb` or "". Names in `team` are first names only; no email or id is exposed.

### Email (owner: backend-email)
| method | path | notes |
| --- | --- | --- |
| CRUD | `/v1/mailboxes`, `/v1/mailboxes/{id}` | create `{name, email, display_name?, inbound_kind, outbound_kind, config, password?, smtp_password?, team_id?, brand_id?, aliases?, alias_teams?, alias_settings?, from_name_format?}`. PATCH also takes `ai_answers`, `ai_answers_mode`, `ai_answers_outside_hours` (admin; turning `ai_answers` on answers `422 ai_off` while AI is off for the instance or the workspace; audit `mailbox.updated` with the new values) and `personal_replies` (admin; replaces them: `enabled` and `reply_to` left out or `null` follow the workspace, `accounts` defaults to `any`; another value is `422 validation_failed`; audit `mailbox.updated` with the new rules) and `trusted_form_senders` (replaces the list; at most 50 entries, each an address or `@domain`, lowercased and deduplicated; a domain of a public email provider, the mailbox's own address or one of its aliases is `422 validation_failed`; audit `mailbox.updated` with the new list). GET answers `trusted_form_senders` to admins and API keys with `settings:read`, `[]` to others. `brand_id` is a brand of the workspace (default: the default brand; PATCH moves the address); OAuth connects create the mailbox in the default brand. `aliases`: at most 20 distinct valid addresses, not the mailbox's own, not on the inbound forwarding domain, and not the address or an alias of another mailbox of the workspace (a mailbox's `email` may not be another's alias either); `422 validation_failed`. `alias_teams` (PATCH replaces it) maps aliases to teams of the workspace; each key is one of `aliases` (lowercased), and a mailbox whose team is private takes none (a private team's address delivers every alias to that team), else `422 validation_failed`. Removing an alias drops its entry; deleting a team moves its entries to `move_to`, or drops them. Audit `mailbox.updated` with the new `alias_teams` when it changes. `alias_settings` (PATCH replaces it) maps aliases to `{display_name?, signature_html?, brand_id?, send_as?}`: each key is one of `aliases` (lowercased); `display_name` is checked like any name people choose, `signature_html` is sanitized and checked like the address's own signature (problems in `errors` at `alias_settings.<alias>.signature_html`), `brand_id` is a brand of the workspace (the mailbox's own brand is stored as `null`), else `422 validation_failed`. An alias left out of the map keeps no name, signature or brand of its own; an alias's `signature_html` left out keeps the stored one (`""` clears it), and `send_as` left out keeps its value, and given it is the admin's word (`send_as_by: "admin"`), refused with `422 validation_failed` while the connection reads Gmail's send-as list (`provider_send_as.readable`), which decides instead. Removing an alias drops its entry; an alias added while Gmail's list is known takes its `send_as` from the list. Audit `mailbox.updated` with `alias_settings: {<alias>: {display_name, brand_id, send_as, signature: boolean}}` when it changes (whether it has a signature, never the signature). `from_name_format`: up to 80 characters, only the five variables, no `<`, `>` or control characters; `""` resets it to the default. `inbound_kind` `"imap"` receives over generic IMAP and is accepted while the instance has `MAILBOX_IMAP` on, or the workspace has Collab (contracts/collab.md "Who has it") and the instance offers Collab IMAP (`COLLAB_IMAP`, default on); `"google"` and `"microsoft"` over IMAP (the provider's OAuth IMAP) only while `MAILBOX_IMAP` is on: otherwise `422 imap_off` ("Receiving over IMAP is off on this instance. Forward the address to opendesk instead."). The network guard and TLS rules are the same either way. Create takes `"forwarding"` or `"imap"`; Google and Microsoft connect through the OAuth start route. `outbound_kind` `"system"` (create, or a PATCH onto it) is accepted only while the instance has `MAILBOX_SYSTEM_SEND` on: otherwise `422 system_send_off` ("Sending through opendesk is off on this instance. Connect Google or Microsoft, or add SMTP."); a mailbox already on it keeps it, with `sending: "system_off"`, and other fields stay editable. PATCH changes how mail is received with `inbound_kind`: `"forwarding"` drops the IMAP settings and the IMAP password (kept as the SMTP password when it signed in to SMTP too) and keeps how replies are sent; a Google or Microsoft connection is kept while replies go through it and dropped otherwise; an `error` status from the IMAP sync becomes `active`. `"imap"` (from forwarding, while IMAP is on for the workspace) needs the IMAP settings and password as on create; `"google"`/`"microsoft"` are `422 validation_failed` (use the OAuth start route). PATCH also changes how replies are sent: `outbound_kind` (`"google"` and `"microsoft"` only for a mailbox that holds that provider's connection, whatever its `inbound_kind`), `config.smtp_*` and `smtp_password` (write-only; left out, the stored one is kept), validated as on create (public host, TLS, a password to sign in with). Moving to `"system"` drops the SMTP settings and the SMTP password; moving a forwarding mailbox off `"google"`/`"microsoft"` drops that connection (sending through it again needs a reconnect). `verify: true` signs in first, as on create (below). Create and PATCH take `config.sync_start` (the `Mailbox` shape). Audit `mailbox.updated` with `connection: {inbound_kind, outbound_kind, verified, imap?: {host, port, tls}, smtp?: {host, port, tls}}` when any of them change. PATCH `config.provider_mark_read`, `config.provider_archive_on_solve` and `config.provider_label` (booleans, admin) turn the Gmail or Outlook tidy-up on or off ("Reading the inbox directly"); turning one on for a mailbox that does not read its inbox through the provider's API is `422 validation_failed`, and a mailbox that stops reading through it drops them. Audit `mailbox.updated` with `provider_options: {mark_read, archive_on_solve, label}` when they change |
| GET | `/v1/mailboxes/{id}/usage` | admin, `settings:read` → `{tickets, open}`: the tickets the caller can see whose reply address is this mailbox (not deleted), and how many of them are `open`, `pending` or `on_hold`. Deleting the mailbox keeps those tickets without that reply address (replies on them then go out from the team's, the brand's or the workspace's first mailbox); the settings page says so before removing |
| POST | `/v1/mailboxes/{id}/send-as/check` | admin, `settings:write` → `Mailbox`: reads Gmail's send-as list now (contracts/teams.md "Aliases"), stores it in `provider_send_as` and sets each alias's `send_as` from it. Only for a Google connection that may read it (a sync or api grant): otherwise `422 send_as_unreadable` ("Google does not share the send-as list with this connection. Add the addresses as aliases by hand."). 6 a minute per mailbox (`429`); a refusal from Gmail is `502 provider_error` with its words. The list is also read on connecting and reconnecting, and about once a day while the mailbox syncs. Audit `mailbox.updated {changes: ["alias_settings"], alias_settings, send_as_checked: true}` |
| POST | `/v1/mailboxes/{id}/test` | → `MailboxCheck` (below). Checks only what the mailbox uses: forwarding is enabled on the instance, the IMAP sign-in (while IMAP is off for the workspace, the inbound leg fails without dialling: "Receiving over IMAP is off on this instance."), or for a mailbox that reads through the provider's API, reading its inbox label or folder (no mail is read); the SMTP sign-in, or the provider token (a Google send-only connection cannot read the profile, so the check refreshes its token). When SMTP on 465 is silent and STARTTLS on 587 of the same server works, the mailbox keeps 587 (`smtp_port`, `mailbox.updated`) |
| POST | `/v1/mailboxes/oauth/{provider}/start` | `{mailbox_id?, name, email?, receive?: "forwarding" \| "sync" \| "api", sync_start?: "recent" \| "new"}` → `{url}` (google, microsoft). `sync_start` is stored as `config.sync_start`; a reconnect without it keeps the mailbox's own. `receive` (default `"forwarding"`) is how a new mailbox receives: `"forwarding"` connects send-only (Google `openid email https://www.googleapis.com/auth/gmail.send`; Microsoft `openid email offline_access https://graph.microsoft.com/Mail.Send https://graph.microsoft.com/User.Read`, the code redeemed for the Graph token alone); `"sync"` also reads the inbox over IMAP (Google `https://mail.google.com/`; Microsoft adds `https://outlook.office.com/IMAP.AccessAsUser.All`) and is `422 imap_off` while `MAILBOX_IMAP` is off; `"api"` reads the inbox through the provider's API with the Collab OAuth client and scopes (Google `openid email https://www.googleapis.com/auth/gmail.modify`, offline, consent; Microsoft `openid email offline_access https://graph.microsoft.com/Mail.ReadWrite https://graph.microsoft.com/Mail.Send https://graph.microsoft.com/User.Read`, the code redeemed for the Graph token alone) and is `422 api_sync_unavailable` unless the workspace has Collab and that provider's Collab client is configured ("Reading the inbox directly needs Collab on this workspace's plan. Forward its mail to opendesk instead.", or that it is not set up on this instance). A reconnect (`mailbox_id`) keeps how the mailbox receives: a forwarding mailbox reconnects send-only, one that receives over IMAP reconnects with sync scopes while IMAP is on and, while it is off, becomes a forwarding mailbox with a send-only connection; one that reads through the API reconnects with the api scopes while the workspace may and, otherwise, becomes a forwarding mailbox with a send-only connection. `receive: "sync"` on a reconnect moves a forwarding mailbox (send-only or not) to reading its inbox over IMAP (the same `422 imap_off` while IMAP is off), keeping the mailbox, its address and its tickets; `receive: "api"` on a reconnect moves the mailbox to reading through the API (the same `422 api_sync_unavailable` otherwise); `"forwarding"` is ignored. A new connection while that provider's mailbox sign-in is off (`GOOGLE_MAILBOX_OAUTH`, `MICROSOFT_MAILBOX_OAUTH`) is `403 mailbox_oauth_disabled`; reconnecting a mailbox that already uses it is allowed. That switch governs an api connection only while the Collab client is the mailbox client itself |
| GET | `/v1/mailboxes/oauth/{provider}/callback` | → 302 `APP_URL/settings/mailboxes?connected=<id>`. A send-only connection creates (or turns the reconnected or same-address mailbox into) `inbound_kind: "forwarding"`, `outbound_kind: <provider>`, `config.oauth_grant: "send"`, with a forwarding address like any forwarding mailbox; its IMAP settings and passwords are dropped. A sync connection sets `inbound_kind` and `outbound_kind` to the provider and `config.oauth_grant: "sync"` (should IMAP be off by the callback, it receives by forwarding and keeps the sync connection for sending). An api connection sets them to the provider with `config.oauth_grant: "api"`, `config.oauth_client: "collab"` and no IMAP settings (should the workspace no longer have Collab by the callback, it receives by forwarding and keeps the connection for sending). The callback is the same for every grant, so `<API_URL>/v1/mailboxes/oauth/{google,microsoft}/callback` must be registered on the Collab client too when it is a different client. Audit `mailbox.connected` with `{provider, email, reconnect, grant, inbound_kind}` |
| GET | `/v1/mailboxes/connect-options` | admin, `settings:read` → `{google: {configured, oauth, api_sync}, microsoft: {configured, oauth, api_sync}, forwarding: {enabled, domain}, mailbox_imap, instance_imap, system_sender}`. `configured`: the OAuth client is set; `oauth`: new connections with that sign-in are allowed; `api_sync`: this workspace may connect the provider to read the inbox directly (`receive: "api"`: it has Collab and the provider's Collab client is configured); `mailbox_imap`: this workspace's mailboxes may receive over generic IMAP (`MAILBOX_IMAP`, or Collab, as on create); `instance_imap`: the instance has `MAILBOX_IMAP` on, so Google and Microsoft may also sync over IMAP (the web app's `mailboxImap`); `system_send`: mailboxes may send through the platform mailer from `system_sender` (`MAILBOX_SYSTEM_SEND`) |
| POST | `/v1/mailboxes/detect` `{email}` | admin, users only (not API keys), 30 a minute per user → `MailDetection` (below); `422 validation_failed` for an invalid address |
| GET | `/v1/templates/variables` | → `TemplateCatalog` (contracts/templates.md) |
| GET | `/v1/email-templates/{brand_id}` | → `{layout: EmailTemplate, csat: EmailTemplate}` |
| PUT | `/v1/email-templates/{brand_id}/{kind}` | admin. `{subject?, body_html?, options?}` → `EmailTemplate`; problems are `422` with each in `errors`. Publishes `email_template.updated` |
| DELETE | `/v1/email-templates/{brand_id}/{kind}` | admin → 204, back to the default |
| POST | `/v1/templates/preview` | agent+. `{kind, body_html?, subject?, options?, brand_id?, team_id?, mailbox_id?, alias?, ticket_id?}` (`alias`: one of `mailbox_id`'s aliases, previewed as mail written to it: its name, signature and, without `brand_id`, its brand) → `{subject, html, text, problems}` |
| POST | `/v1/templates/test` | agent+, users only. Same body; sends the preview to the caller's login address, `[Test]` subject. 10 an hour (`429`) |
| POST | `/v1/inbound/email` | header `OD-Inbound-Signature: t=<unix seconds>,v1=<hex>`, an HMAC-SHA256 keyed with `INBOUND_SECRET` over `<t>.<recipient>.<envelope sender>.` followed by the raw message, refused when more than five minutes old, query `to=<rcpt>`, header `OD-Envelope-From`, body raw RFC 822 → 202 (Cloudflare Email Worker / SES) |

Reading the inbox directly (`oauth_grant: "api"`): the per-mailbox sync job (`email.sync_mailbox`, queued
every minute by `email.sync_due` for each due mailbox) reads it over HTTPS, whatever `MAILBOX_IMAP` says,
and never dials IMAP. Gmail: the first pass lists the inbox of the last 7 days (at most 200 messages,
newest first) and keeps the profile's `historyId`; later passes read `users.history.list` from it
(`historyTypes=messageAdded`, `labelId=INBOX`; a history id Gmail no longer keeps starts over like a first
pass). Microsoft: the inbox's delta (`/me/mailFolders/inbox/messages/delta`, the first round filtered to
the last 7 days and at most 200 messages), keeping the delta link; an expired link starts over. Each new
message's raw source (`format=raw`, `/$value`) is received like forwarded mail (deduplicated, then
processed into a ticket); drafts, spam and what the mailbox sent itself are skipped, and so are messages
over 30 MB. The cursor is kept per account (`mailboxes.api_sync_state`), so another account starts over.
Failures are stored on the mailbox (`status: "error"`, a safe `last_error` with the provider's status and
error code, never its message) and retried with backoff (1 minute doubling to 1 hour); a refused grant
asks for a reconnect. A suspended workspace's mailbox waits; one whose workspace lost Collab is kept,
not read, with `last_error` "Reading this inbox directly needs Collab. Forward its mail or connect it
again.", and resumes when Collab is back.

Such a mailbox stays the same mailbox in Gmail or Outlook:
- **Provider ids.** Each email it receives records where it lives at the provider
  (`inbound_emails.provider_ref`, copied to its message's `messages.provider_ref`, never in the API):
  `{mailbox_id, account, provider, id, thread}`, Gmail's message id and `threadId`, or Graph's immutable
  message id (`Prefer: IdType="ImmutableId"`) and `conversationId`. A reply sent through Gmail records the
  sent message's too. Ids of another connected account are never used.
- **Replies in the conversation.** A reply, auto-reply or CSAT email on a ticket, sent through the
  provider's API by such a mailbox, joins the conversation of the ticket's latest email that account
  received (else sent): Gmail's `users.messages.send` carries its `threadId` (a multipart upload with the
  thread in the metadata), so Gmail shows the reply under the customer's email and in Sent; Graph's
  `sendMail` files it in Sent Items, and Outlook keeps it in the conversation by `In-Reply-To` and
  `References`, which name the customer's message as on every reply. A reply whose subject has no `[#n]`
  tag takes the conversation's subject, `Re: <subject>` without a tag added (Gmail and Outlook keep only
  matching subjects together); threading back into the ticket goes by `References` and the Message-ID,
  which these mailboxes read intact. Every other mailbox sends as before.
- **From name.** Connecting or reconnecting it fills an empty `display_name` with the name the account
  sends as: Gmail's send-as `displayName` for the mailbox's address (else the primary one's), or
  Graph's `/me` `displayName`. A name already set is kept.
- **Tidy-up** (mailbox settings, admin, each off by default; a reconnect keeps them):
  `provider_mark_read` marks an email read there once it became a ticket message; `provider_label` labels
  it `opendesk` then (Gmail: the label, made once if the account has none; Outlook: the category, added
  to the message's); `provider_archive_on_solve` archives the ticket's emails there when the ticket
  becomes solved or closed from another status (Gmail: INBOX removed; Outlook: moved to the Archive
  folder). Each is a River job `email.provider_tidy` (`{workspace_id, ticket_id, message_id?, action:
  "imported" | "archive"}`, queue `email`), queued in the transaction that adds the message or changes
  the status, and only for a mailbox with that option on; it does what the mailbox's options say when it
  runs, on that account's messages only, through `messages.batchModify` (Gmail) or the message's
  `PATCH` and `move` (Graph). A provider that is busy or unreachable is retried with backoff (8
  attempts); a refusal or a revoked grant ends it; an email gone since is skipped. It never changes the
  ticket. A suspended workspace's, a disabled mailbox's, or one whose workspace lost Collab does nothing.

Connecting a mailbox:

```ts
type MailHost = "google_workspace" | "gmail" | "microsoft365" | "outlook" | "zoho" | "yahoo" | "aol"
  | "icloud" | "fastmail" | "godaddy" | "namecheap" | "ionos" | "hostinger" | "ovh" | "migadu"
  | "purelymail" | "rackspace" | "yandex" | "gmx" | "proton" | "other";
type MailEndpoint = { host: string; port: number; security: "tls" | "starttls" };
type MailDetection = {
  domain: string;
  host: MailHost | "";              // "" when nothing was found
  label: string;                    // "Gmail", "Microsoft 365", ...
  source: "known" | "mx" | "autoconfig" | "ispdb" | "srv" | "none";
  settings: { imap: MailEndpoint; smtp: MailEndpoint } | null;  // secure servers only
  password_auth: "app_password" | "password" | "oauth_only" | "unsupported";
  app_password_url: string;         // where the provider makes app passwords, or ""
  oauth: "google" | "microsoft" | null;  // the sign-in that fits this host
};
type MailboxTestLeg = { ok: boolean; error: string; detail?: string };  // detail: the server's reply, scrubbed
type MailboxCause = { code: string; title: string; fix: string; retryable: boolean };
type MailboxCheck = { inbound: MailboxTestLeg; outbound: MailboxTestLeg; cause?: MailboxCause; smtp_port?: number };
```

- **Detection** tries, in order: the known-provider table, MX records, the domain's own
  autoconfig (`autoconfig.<domain>`, `<domain>/.well-known/autoconfig`), Thunderbird's ISPDB
  (only with `MAILHOST_ISPDB`), and SRV records. Every lookup goes through the outbound address
  guard (public addresses only, HTTPS, no redirect off 443), with timeouts, a body cap and a cache.
  Only TLS or STARTTLS servers with password authentication are returned.
- **Create with `verify: true`** signs in to the IMAP and SMTP servers the body names before
  anything is stored. A refusal is `422 mailbox_connection_failed` with the `MailboxCheck` members
  on the problem (`inbound`, `outbound`, `cause`) and nothing saved; a working fallback SMTP port is
  stored. Passwords are never echoed, logged or put in `detail`. **PATCH with `verify: true`** does
  the same for the servers it changes: IMAP (while IMAP is on for the workspace) when the mailbox moves to IMAP or its
  settings or password change, SMTP when its settings or password change or replies move to it; a
  PATCH that changes neither checks nothing.
- **Passwords** are trimmed; for Gmail and Google Workspace the spaces Google prints inside an app
  password are removed.
- `config.mail_host` (a `MailHost`) is set by the API from the servers, the OAuth provider or the
  address; clients may send it, and an unknown value is replaced.

Inbound mail is ignored, without a ticket, when it is machine-generated (`Auto-Submitted`, bulk,
mail from `mailer-daemon` or `postmaster`, a delivery report with the null envelope sender), comes
from the receiving mailbox's own address, connected account or aliases, comes from a forwarding
address, or is mail we sent coming back: every message we send carries `X-Loop: <an opaque token of
the sending mailbox>`, and mail carrying one of the workspace's tokens (or addresses) there is
dropped. Mail from **another** of the workspace's addresses (a teammate forwarding or writing to this
one) opens a ticket here unless its `Message-ID` is in our own format; it threads only into a
conversation whose customer is that address, and gets no auto-reply and no Ollie answer. A new email
ticket is routed (contracts/teams.md "Routing"); every inbound message records the mailbox it arrived
at (`mailbox_id`).

- **Identity.** The customer is the `From` address: the contact, the message author and every
  threading check use it. `Reply-To` is kept on the message (`reply_to`); replies to the customer go
  to it only when it is at the same domain as `From`.
- **Contact forms.** A website form sends `From` the site with the visitor in `Reply-To`. When `From`
  matches the receiving mailbox's `trusted_form_senders` (an exact address, or `@domain` for that
  domain only) **and** the message passed DMARC at a trusted receiving server (below), the
  `Reply-To` address is the customer instead: the contact (named from its display name), the
  message's `from_email` and `from_name`, and where replies go. The message records the form
  address in `form_sender` and `reply_to` is `""`. Without a DMARC pass, from an untrusted
  address, with no usable `Reply-To` (missing, invalid, one of the workspace's addresses or on the
  forwarding domain), or from another of the workspace's addresses, the message is handled as above.
  Form plugins mark their mail as generated or bulk (`Auto-Submitted`, `Precedence: bulk`): mail
  from a trusted form address is still received, and its visitor still gets automatic answers.
  Only an autoresponder's markers (`Auto-Submitted: auto-replied`, `Precedence: auto_reply`,
  `X-Autoreply`, `X-Autorespond`) or an `X-Loop` stop them.
- **Threading**, always within the receiving workspace: a stored `Message-ID` in `References` /
  `In-Reply-To`; then our own format `<m_<message id>.t<n>@…>` when that message exists on ticket n,
  or `<a_<id>.t<n>@…>` (auto-replies and CSAT, not stored) when its id carries the MAC we made for
  this workspace and ticket n; then the `+t<n>` address tag and the `[#n]` subject tag. A ticket
  number that is not proven so threads only when the sender is the ticket's contact. Mail we send
  never names a forwarding address: it answers to the mailbox's own address, or the alias the
  customer wrote to (`Reply-To` only when `From` is not that address), so the customer's reply comes
  back the way their first message did and threads by `In-Reply-To` and `References`.
  A sender new to the conversation, who is neither the ticket's contact nor in the `To` or `Cc` of a
  message the team sent or the contact wrote (what a newcomer's own message names never counts),
  still joins it: the message is stored, and no `customer_replied` automation or Ollie answer runs.
- **Sender checks.** `Authentication-Results` (or `ARC-Authentication-Results`) count only from a
  server the message passed through, by the route it arrived (`inbound_emails.via`): mail read from
  the mailbox believes an authserv-id in `INBOUND_AUTHSERV_ID`, plus `mx.google.com` for Google
  mailboxes and Microsoft's for Microsoft 365 ones; mail to a forwarding address, through
  `POST /v1/inbound/email` or from a relay in `INBOUND_SMTP_RELAYS` to the built-in SMTP receiver,
  believes `INBOUND_AUTHSERV_ID` alone; mail the built-in receiver takes straight from the sending
  server, and an answer read from a member's own Collab account, believe none. The first such header from the top is the verdict. A DMARC `fail` opens a
  new ticket as spam; on an existing conversation the message is stored with `auth_failed`, the
  ticket's status does not change (it is never reopened; a `status_changed` activity with
  `from` = `to`, `reason: "sender_unverified"` and the `message_id` records that it was kept), and no
  `customer_replied` automation, auto-reply or Ollie answer runs.
- **Bounces.** A delivery report counts as a bounce only with the null envelope sender (the relay's
  `OD-Envelope-From`, the SMTP receiver's `MAIL FROM`, else the delivering server's `Return-Path`),
  and marks a message `bounced` only when it names a `Message-ID` that mailbox sent. Any other
  report is ordinary mail.
- **Duplicates.** A message is the same one again only when its `Message-ID` and a hash of its
  content (`From`, `Date`, `Subject`, body) match; a different message reusing a `Message-ID` is
  still received (without that id for threading).
- **Automatic replies.** Mail that is automatic, bulk or list mail (not the mailbox's own group),
  asks for no automatic answers (`X-Auto-Response-Suppress`), carries someone's `X-Loop`, has a null
  sender, comes from a no-reply address or a workspace address, or failed sender checks gets no
  auto-reply and no automation `send_reply`. Automation replies go out with
  `Auto-Submitted: auto-generated` and `X-Auto-Response-Suppress: All`, at most 3 per ticket and 10
  per contact per hour.

### Support channels (owner: backend-supportchannels)
Support-channel connections, destinations, account linking and signed inbound hooks are
defined in `contracts/support-channels.md`.

### System
`GET /healthz` (liveness), `GET /readyz` (db + nats), `GET /v1/openapi.json`, `GET /docs` (API reference UI).
