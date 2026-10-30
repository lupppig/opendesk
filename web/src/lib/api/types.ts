// Resource shapes, exactly as contracts/api.md defines them.

import type { BotLook } from "@/lib/botlook";
export type { BotLook, BotFill } from "@/lib/botlook";

export type Role = "owner" | "admin" | "agent" | "viewer";

/** Where a picture came from (contracts/api.md "Profile pictures and logos"). */
export type AvatarSource = "upload" | "oauth" | "gravatar" | "domain" | "widget";

export type User = {
  id: string;
  email: string;
  name: string;
  /** null when there is no picture: show initials. */
  avatar_url: string | null;
  avatar_source: AvatarSource | null;
  email_verified: boolean;
  totp_enabled: boolean;
  /** The clock for times in the app and notification emails; "auto" follows the browser's locale in the app and 24-hour in emails. */
  time_format: TimeFormat;
  created_at: string;
};
export type TimeFormat = "auto" | "12h" | "24h";
export type UserRef = { id: string; name: string; email: string; avatar_url: string | null };

export type WorkspaceSettings = {
  brand_color?: string;
  timezone?: string;
  help_center_enabled?: boolean;
  help_center_title?: string;
  csat_enabled?: boolean;
  /** The workspace default assignment mode; see Assignment. */
  auto_assign?: AssignMode;
  /** Who hears about a new ticket nobody is assigned to (read only here, set through Assignment). Default "auto". */
  new_ticket_notify?: NewTicketNotify;
  /** Look up contact pictures on Gravatar. Default false. */
  gravatar?: boolean;
  /** Fetch company logos from the company's website. Default true. */
  domain_logos?: boolean;
  /** Default language (BCP 47) for customer-facing pages when the visitor's browser has no match. */
  locale?: string;
  /** Customer-facing pages always use locale (a brand's own, else this one), whatever the visitor's browser. Default false. */
  locale_fixed?: boolean;
  ai?: AISettings;
  /** "admins": only admins may create labels by applying a new name. Default "anyone". */
  label_creation?: "anyone" | "admins";
  /** Seconds an agent's email (or other non-chat) reply waits in the app, with a way to take it back, before it is sent. Default 5; 0 sends at once. */
  undo_send_seconds?: number;
  /** The same for chat replies. Default 0: a chat reply goes at once. */
  undo_send_chat_seconds?: number;
  /** With Collab, members may answer email tickets from their own mail accounts. Default true. */
  collab_ticket_replies?: boolean;
  /** Where answers to such a reply go when it names no Reply-To: the address's own reply-to (else the address), or the ticket's reply address. Default "address". */
  collab_ticket_reply_to?: "address" | "mailbox";
  /** Which messages agents may delete: none, their own internal notes, or any agent's notes and replies. Admins may delete any. Default "own". */
  message_delete?: "off" | "own" | "all";
  /** The composer shows "Send and solve" (or close, when solved is labelled Closed) as a button next to Send, not only in its menu. Default false. */
  send_and_finish_button?: boolean;
  /** Heartbeats count toward each member's online time, shown in the agents report. Default true; false records and shows none. */
  agent_online_time?: boolean;
  /** Display-only names and colours for the fixed statuses and priorities. */
  status_labels?: Partial<Record<TicketStatus, DisplayOverride>>;
  priority_labels?: Partial<Record<Priority, DisplayOverride>>;
};
/** `hidden` (statuses only): left out of the menus agents pick a status from. */
export type DisplayOverride = { label?: string; color?: LabelColor; hidden?: boolean };
export type Workspace = {
  id: string;
  name: string;
  slug: string;
  widget_key: string;
  settings: WorkspaceSettings;
  /** The workspace logo. */
  avatar_url: string | null;
  avatar_source: "upload" | null;
  images: WorkspaceImages;
  created_at: string;
};
/** The AI agent's picture is its bot's (contracts/bots.md). */
export type WorkspaceImageSlot = "help_center_logo" | "help_center_favicon" | "widget_avatar" | "widget_launcher";
export type WorkspaceImages = Record<WorkspaceImageSlot, string | null>;
export type Membership = { workspace: Workspace; role: Role; /** An instance admin suspended the workspace: its routes answer 403 workspace_suspended (contracts/admin.md). */ suspended?: boolean };
export type Availability = "online" | "away" | "offline";
export type Member = {
  user: UserRef & { avatar_source: AvatarSource | null };
  role: Role;
  /** What the member set. */
  availability: Availability;
  /** availability, or "away" when auto-away applies. */
  presence: Availability;
  /** Auto-assignment cap on open and pending tickets; null is no cap. */
  max_open_tickets: number | null;
  /** An agent's own rule for which messages they may delete; null follows the workspace's settings.message_delete. */
  message_delete?: "off" | "own" | "all" | null;
  open_tickets: number;
  last_seen_at: string | null;
  created_at: string;
  /** Feeds the agent template variables (contracts/templates.md). Older servers leave it out. */
  profile?: MemberProfile;
};

/** What customers can see of a member in signatures, per workspace. */
export type MemberProfile = {
  title: string;
  phone: string;
  pronouns: string;
  /** The personal signature; takes template variables. */
  signature_html: string;
};
export type Invitation = {
  id: string;
  email: string;
  role: Role;
  expires_at: string;
  created_at: string;
  invited_by: UserRef | null;
};
// Teams, brands, widgets and routing (contracts/teams.md).
export type TeamRole = "lead" | "member" | "observer";
export type Team = {
  id: string;
  name: string;
  /** Up to 280 chars. */
  description: string;
  color: LabelColor;
  /** Only its members, observers and the assignee see its tickets. */
  private: boolean;
  members: { user_id: string; role: TeamRole }[];
  /** Every member, any role. */
  member_ids: string[];
  /** "" uses the address's signature. */
  signature_html: string;
  /** null follows the workspace's hours. */
  business_hours: BusinessHours | null;
  /** null follows the workspace default. */
  assign_mode: AssignMode | null;
  /** Addresses whose default team this is. */
  mailbox_ids: string[];
  open_count: number;
  unassigned_count: number;
  created_at: string;
  updated_at: string;
};
/** A help center's custom domain (contracts/teams.md "Help center addresses"). */
export type HelpDomain = {
  /** Lowercase ASCII, e.g. help.acme.com. */
  hostname: string;
  /** pending: waiting for its DNS records or certificate; failed: checks gave up after 72 hours. */
  status: "pending" | "active" | "failed";
  problem: HelpDomainProblem;
  /** What the customer adds at their DNS provider; ok is the last check's answer. */
  records: HelpDomainRecord[];
  checked_at: string | null;
  /** Both records were found. */
  verified_at: string | null;
  created_at: string;
};
export type HelpDomainProblem = "" | "txt_missing" | "cname_missing" | "domain_taken" | "certificate_pending" | "certificate_failed" | "provider_error";
export type HelpDomainRecord = { type: "TXT" | "CNAME"; name: string; value: string; ok: boolean };
export type Brand = {
  id: string;
  name: string;
  /** Names the help center's default address: <slug>.<HELP_DOMAIN>, or <base>/help/<slug> (APP_URL or API_URL). */
  slug: string;
  is_default: boolean;
  /** "#rrggbb", or "" for the workspace brand colour. */
  color: string;
  locale: string;
  help_center: {
    enabled: boolean;
    title: string;
    /** Where the help center is: the active custom domain, else default_url. */
    url: string;
    /** https://<slug>.<HELP_DOMAIN>, or <base>/help/<slug> (APP_URL or API_URL) without HELP_DOMAIN. */
    default_url: string;
    domain: HelpDomain | null;
  };
  images: { logo: string | null; help_center_logo: string | null; help_center_favicon: string | null };
  default_mailbox_id: string | null;
  mailbox_count: number;
  widget_count: number;
  position: number;
  created_at: string;
  updated_at: string;
};
export type Widget = {
  id: string;
  brand_id: string;
  /** Usually the website, e.g. "Marketing site". */
  name: string;
  key: string;
  team_id: string | null;
  /** The bot the AI agent answers as here (contracts/bots.md); null is the default bot. */
  bot_id: string | null;
  settings: WidgetSettings;
  images: { avatar: string | null; launcher: string | null };
  install: { snippet: string };
  seen_at: string | null;
  position: number;
  created_at: string;
  updated_at: string;
};
export type WidgetTopic = { id: string; label: string; team_id: string | null };
export type TicketRouted = {
  via: "address" | "widget" | "topic" | "rule" | "triage" | "automation" | "agent" | "api";
  rule_id?: string;
  rule_name?: string;
  by?: UserRef;
};
export type RoutingField =
  | "received_at"
  | "mailbox_id"
  | "brand_id"
  | "widget_id"
  | "channel"
  | "from_email"
  | "from_domain"
  | "subject"
  | "body"
  | "page_url"
  | "topic"
  | "ai_decide"
  | `contact.field.${string}`;
export type RoutingOp = "equals" | "not_equals" | "contains" | "not_contains" | "starts_with" | "gt" | "lt";
export type RoutingCondition = { field: RoutingField; op: RoutingOp; value: string; question?: string };
/** A workspace expertise label. Archived skills remain on existing tickets. */
export type Skill = { id: string; name: string; archived: boolean; member_ids: string[] };
/** The resolved label a route fixed on a ticket at creation time. */
export type TicketSkill = Pick<Skill, "id" | "name">;
export type RoutingRule = {
  id: string;
  name: string;
  enabled: boolean;
  position: number;
  run_count: number;
  match: "all" | "any";
  conditions: RoutingCondition[];
  team_id: string;
  skill_ids: string[];
  created_at: string;
  updated_at: string;
};
export type RoutingTest = {
  team_id: string | null;
  brand_id: string;
  mailbox_id: string | null;
  routed: TicketRouted | null;
  steps: { rule_id: string; name: string; matched: boolean | "unknown" }[];
  required_skills: TicketSkill[];
  assign_mode: AssignMode;
};

export type CompanyRef = { id: string; name: string; domain: string; avatar_url?: string | null };
export type Contact = {
  id: string;
  email: string | null;
  name: string;
  phone: string;
  avatar_url: string | null;
  avatar_source: AvatarSource | null;
  company: CompanyRef | null;
  external_id: string | null;
  notes: string;
  custom: Record<string, unknown>;
  tags: TagRef[];
  fields: FieldValues;
  blocked: boolean;
  ticket_count: number;
  created_at: string;
  updated_at: string;
};
export type ContactRef = { id: string; email: string | null; name: string; avatar_url: string | null };
export type Company = {
  id: string;
  name: string;
  domain: string;
  /** The company logo. */
  avatar_url: string | null;
  avatar_source: "upload" | "domain" | null;
  notes: string;
  custom: Record<string, unknown>;
  tags: TagRef[];
  fields: FieldValues;
  contact_count: number;
  created_at: string;
  updated_at: string;
};

export type TicketStatus = "open" | "pending" | "on_hold" | "solved" | "closed";
export type Priority = "low" | "normal" | "high" | "urgent";
/** Business impact; sev1 is the most severe. A ticket may have none. */
export type Severity = "sev1" | "sev2" | "sev3" | "sev4";
/** phone: a ticket a call opened (contracts/phone.md). */
export type Channel = "email" | "chat" | "api" | "portal" | "phone";
// Labels ("tags" in the API) and custom fields (contracts/api.md "Labels and custom fields").
export type LabelColor =
  | "gray" | "red" | "orange" | "amber" | "yellow" | "lime" | "green" | "teal" | "cyan" | "blue" | "indigo" | "violet" | "purple" | "pink" | "rose";
export const LABEL_COLORS: LabelColor[] = ["gray", "red", "orange", "amber", "yellow", "lime", "green", "teal", "cyan", "blue", "indigo", "violet", "purple", "pink", "rose"];
export type LabelScope = "tickets" | "contacts" | "companies";
export type TagUsage = { tickets: number; contacts: number; companies: number };
export type Tag = {
  id: string;
  name: string;
  color: LabelColor;
  description: string;
  group_id: string | null;
  position: number;
  archived: boolean;
  applies_to: LabelScope[];
  usage: TagUsage;
  created_at: string;
  updated_at: string;
};
/** A label as embedded in tickets, contacts and companies. */
export type TagRef = { id: string; name: string; color: LabelColor; group_id: string | null };
export type TagGroup = {
  id: string;
  name: string;
  color: LabelColor | null;
  /** At most one label of the group per record; adding one replaces the other. */
  exclusive: boolean;
  position: number;
  tag_count: number;
  created_at: string;
  updated_at: string;
};
export type TagInput = {
  name: string;
  color?: LabelColor;
  description?: string;
  group_id?: string | null;
  position?: number;
  archived?: boolean;
  applies_to?: LabelScope[];
};
export type TagGroupInput = { name: string; color?: LabelColor | null; exclusive?: boolean; position?: number };

export type FieldEntity = "ticket" | "contact" | "company";
export type FieldType = "text" | "long_text" | "number" | "select" | "multi_select" | "date" | "checkbox" | "url" | "email" | "user";
export type FieldOption = { id: string; label: string; color: LabelColor };
export type CustomField = {
  id: string;
  entity: FieldEntity;
  /** Immutable slug; the key under `fields`. */
  key: string;
  name: string;
  type: FieldType;
  options: FieldOption[];
  required_on_solve: boolean;
  description: string;
  position: number;
  archived: boolean;
  created_at: string;
  updated_at: string;
};
export type CustomFieldInput = {
  entity: FieldEntity;
  key?: string;
  name: string;
  type: FieldType;
  /** `id` and `color` are optional on write (slug of label, gray). */
  options?: { id?: string; label: string; color?: LabelColor }[];
  required_on_solve?: boolean;
  description?: string;
  position?: number;
};
export type CustomFieldPatch = Partial<Pick<CustomFieldInput, "name" | "options" | "required_on_solve" | "description" | "position">> & {
  archived?: boolean;
};
/** text, long_text, url, email, date (YYYY-MM-DD), select (option id), user (user id): string; number; checkbox; multi_select: option ids. */
export type FieldValue = string | number | boolean | string[];
/** Only keys with a value; a cleared field is absent. */
export type FieldValues = Record<string, FieldValue>;
/** A write: null clears a field. */
export type FieldValuesPatch = Record<string, FieldValue | null>;
export type FieldOp = "eq" | "ne" | "contains" | "not_contains" | "gt" | "gte" | "lt" | "lte" | "any" | "all" | "none" | "set" | "not_set";
/** `any`, `all` and `none` take a comma separated list; `set`/`not_set` ignore value. */
export type FieldFilter = { key: string; op: FieldOp; value: string };
export type TicketSla = {
  policy_id: string;
  first_response_due_at: string | null;
  resolution_due_at: string | null;
  breached: boolean;
};
export type Ticket = {
  id: string;
  number: number;
  alias?: string | null;
  subject: string;
  status: TicketStatus;
  priority: Priority;
  severity: Severity | null;
  channel: Channel;
  mailbox_id: string | null;
  contact: ContactRef | null;
  assignee: UserRef | null;
  team_id: string | null;
  brand_id: string;
  widget_id: string | null;
  /** Where the first message arrived. mailbox_id is the address replies go out from. */
  origin_mailbox_id: string | null;
  /** The exact address the customer wrote to (an alias or the mailbox address). */
  received_at: string;
  /** Chat: the page the conversation started on. */
  page_url: string;
  routed: TicketRouted | null;
  tags: TagRef[];
  required_skills: TicketSkill[];
  fields: FieldValues;
  sla: TicketSla | null;
  first_responded_at: string | null;
  solved_at: string | null;
  snoozed_until: string | null;
  last_message_at: string;
  last_customer_message_at: string | null;
  /** The customer's first unanswered message; null once anyone (or the AI agent) replied. */
  waiting_since: string | null;
  /** The customer's public messages since the last reply; 0 once anyone (or the AI agent) replied. */
  unanswered_count: number;
  last_message_preview: string;
  message_count: number;
  spam: boolean;
  csat: { score: number; comment: string } | null;
  custom: Record<string, unknown>;
  created_at: string;
  updated_at: string;
};

export type Attachment = {
  id: string;
  filename: string;
  content_type: string;
  size_bytes: number;
  inline: boolean;
  url: string;
};
/** "scheduled" is the app's own: a reply held for the undo window, not yet sent (outbox.ts). */
export type DeliveryStatus = "none" | "scheduled" | "pending" | "sent" | "failed" | "bounced";
export type Message = {
  id: string;
  ticket_id: string;
  kind: "reply" | "note";
  direction: "inbound" | "outbound";
  author: {
    type: "contact" | "user" | "system" | "api";
    user: UserRef | null;
    contact: ContactRef | null;
    /** The bot an AI reply was sent as (contracts/bots.md). */
    bot?: BotRef | null;
  };
  body_html: string;
  body_text: string;
  has_quoted: boolean;
  subject: string;
  from_email: string;
  from_name: string;
  to: string[];
  cc: string[];
  bcc: string[];
  attachments: Attachment[];
  delivery_status: DeliveryStatus;
  delivery_error: string;
  /** A reply from the widget AI agent (contracts/agent.md). */
  ai: boolean;
  /** An action the agent asks the visitor to confirm; set only on agent messages that carry one. */
  ai_action?: AIAction | null;
  /** The visitor's rating of an agent reply (agents only). */
  ai_rating?: "up" | "down" | null;
  /** Sent from (outbound) or received at (inbound). */
  mailbox_id?: string | null;
  /** Inbound email: the Reply-To it named when that differs from from_email, else "". Agents only. */
  reply_to?: string;
  /** Inbound email: its From failed DMARC at a trusted receiving server. Shown as unverified. Agents only. */
  auth_failed?: boolean;
  /** Inbound email: the trusted contact form address it came through; from_email is then the visitor. Agents only. */
  form_sender?: string;
  /** Inbound email: the address of ours it was written to (its mailbox's address or an alias), else "". Agents only. */
  received_at?: string;
  /** The third-party thread a note belongs to (contracts/api.md "Threads"), else null. */
  thread_id?: string | null;
  /** An email reply sent from a member's own Collab address (contracts/collab.md "Answering tickets"), else null. */
  from_account?: FromAccount | null;
  created_at: string;
  edited_at: string | null;
  /** A deleted message stays as a marker: who deleted it and when. Only admins get what it said. */
  deleted_at?: string | null;
  deleted_by?: UserRef | null;
  /** Set once an admin removed a deleted message's content for good. */
  purged_at?: string | null;
  /** A note sent as a reply: a marker pointing at the reply (deleted_at says when). Only admins get what it said. */
  sent_as_reply_id?: string | null;
};
/** The Collab address a reply goes or went out from, and the Reply-To it asks for: "address" (the address's own), "mailbox" (the ticket's reply address) or an address. */
export type FromAccount = { account_id: string; email: string; reply_to: string };
/** A third-party thread: an email conversation on a ticket with someone outside the team (contracts/api.md "Threads"). */
export type Thread = {
  id: string;
  ticket_id: string;
  subject: string;
  to: string[];
  cc: string[];
  /** The address it is sent from; null once that mailbox is removed. */
  mailbox_id: string | null;
  status: "open" | "closed";
  /** The first message quoted the ticket's conversation with the customer. */
  include_history: boolean;
  created_by: UserRef | null;
  message_count: number;
  last_message_at: string;
  created_at: string;
  updated_at: string;
};
/** contracts/agent.md "Visitor actions". */
export type AIActionStatus = "pending" | "confirmed" | "declined" | "expired" | "failed";
export type AIAction = { id: string; title: string; summary: string; status: AIActionStatus; tool: string };
export type ActivityType =
  | "status_changed"
  | "priority_changed"
  /** data: {from, to} with Severity | null. */
  | "severity_changed"
  | "assigned"
  | "team_changed"
  | "tagged"
  | "untagged"
  | "sla_breached"
  | "merged"
  | "csat_received"
  /** data: {key, name, from, to} with FieldValue | null values. */
  | "field_changed"
  /** data: {from, to}, each {id, name} (name, else email) or null. */
  | "contact_changed"
  /** data: CallData. Updated in place (activity.updated) as the call moves on. */
  | "call"
  | (string & {});

/** A call entry's data (contracts/phone.md "Timeline"). */
export type CallData = {
  call_id: string;
  provider: string;
  direction: "inbound" | "outbound";
  stage: "ringing" | "answered" | "ended";
  /** Set once the call ended. */
  outcome: "" | "answered" | "missed" | "voicemail";
  /** The provider's own reason (out_of_opening_hours, no_available_agent, ...). */
  missed_reason: string;
  /** E.164, "" when withheld. */
  number: string;
  line: string;
  line_id: string;
  caller_name: string;
  /** The member who took or made the call, when the provider's user has their email. */
  agent: UserRef | null;
  agent_name: string;
  started_at: string | null;
  answered_at: string | null;
  ended_at: string | null;
  duration: number;
  recording: boolean;
  voicemail: boolean;
  /** The provider's lasting page for the recording or voicemail, also "Open in" the provider. */
  asset_url: string;
  /** Direct links, working until links_expire_at. */
  recording_url: string;
  voicemail_url: string;
  links_expire_at: string | null;
  tags: string[];
  comments: { id: string; text: string; author?: string; at?: string }[];
  summary: string;
  transcript: string;
  outside_hours: boolean;
};
export type Activity = {
  id: string;
  ticket_id: string;
  actor: { type: string; user: UserRef | null };
  type: ActivityType;
  data: Record<string, unknown>;
  created_at: string;
};

export type SavedReply = {
  id: string;
  title: string;
  shortcut: string;
  body_html: string;
  /** Offered only on this team's tickets; null is everyone. */
  team_id?: string | null;
  created_at: string;
  updated_at: string;
};
export type TicketFilters = {
  status?: TicketStatus[];
  priority?: Priority[];
  /** "none" matches tickets without a severity. */
  severity?: (Severity | "none")[];
  assignee?: "me" | "none" | string;
  /** Team ids, "mine" (the caller's teams) and "none", comma separated: tickets of any of them. */
  team_id?: string;
  /** The same values as team_id: tickets of none of them. */
  team_not?: string;
  mailbox_id?: string;
  /** Mailbox ids, comma separated: tickets whose reply address is none of them. */
  mailbox_not?: string;
  /** Contact ids, comma separated: tickets whose contact is none of them. */
  contact_not?: string;
  /** Company ids, comma separated: tickets whose contact belongs to none of them. */
  company_not?: string;
  /** The address a ticket was first written to: a mailbox's address or an alias. */
  received_at?: string;
  brand_id?: string;
  widget_id?: string;
  /** Label names, comma separated (a name never contains a comma): tickets carrying any of them. */
  tag?: string;
  /** Label names, comma separated: tickets carrying none of them. */
  tag_not?: string;
  /** Tickets carrying no label. */
  untagged?: boolean;
  channel?: Channel;
  /** Tickets on none of these channels. */
  channel_not?: Channel[];
  q?: string;
  /** Custom field filters, ANDed (max 20). */
  fields?: FieldFilter[];
};
export type View = { id: string; name: string; shared: boolean; filters: TicketFilters; position: number; /** Shared with this team. */ team_id?: string | null; /** The sidebar heading it shows under (lib/viewSections). */ section?: string | null; /** What the sidebar shows beside the name; none shows the layers icon. */ icon?: ViewIcon | null };
/** emoji: one emoji; icon: a name of the app's list (viewIcons.ts); text: one or two letters or digits. `color` tints an icon or text (a Tone), never an emoji. */
export type ViewIcon = { kind: "emoji" | "icon" | "text"; value: string; color: string | null };
export type SlaPolicy = {
  id: string;
  name: string;
  priority: Priority | null;
  first_response_minutes: number;
  resolution_minutes: number;
  /** Count only the workspace's open business hours. */
  business_hours: boolean;
  enabled: boolean;
  position: number;
  /** Only this team's tickets; null is any. */
  team_id?: string | null;
};

// Triage, assignment and setup (contracts/api.md "Assignment", "Triage", "Business hours", "Onboarding").
export type AssignMode = "none" | "round_robin" | "load_balanced";
/** Who hears about a new ticket nobody is assigned to (settings.new_ticket_notify). */
export type NewTicketNotify = "auto" | "everyone" | "leads" | "off";
export type AssignmentRule = { target_type: "mailbox" | "team"; target_id: string; mode: AssignMode };
export type Assignment = {
  /** settings.auto_assign */
  mode: AssignMode;
  rules: AssignmentRule[];
  /** 0 is off, else 10..1440. */
  auto_away_minutes: number;
  new_ticket_notify: NewTicketNotify;
};
export type TriageField = "priority" | "severity" | "labels" | "team";
/** The decision engine behind triage (contracts/api.md). Jev is TypeSafe's decision model. */
export type DecisionEngine = { provider: "typesafe" | "llm" | "none"; model: string; connected: boolean };
export type TriageSettings = {
  auto_triage: boolean;
  fields: TriageField[];
  assign_after_triage: boolean;
  /** 0.5..0.99: decisions at or above it apply, below it they become suggestions. */
  threshold: number;
  engine: DecisionEngine;
  /** Triage can run: AI or a decision engine is configured and AI is on. */
  available: boolean;
};
export type Weekday = "mon" | "tue" | "wed" | "thu" | "fri" | "sat" | "sun";
export type TimeRange = { start: string; end: string };
export type Holiday = { date: string; name: string };
export type BusinessHours = {
  enabled: boolean;
  timezone: string;
  /** "HH:MM", 24h, up to 3 ranges a day. */
  schedule: Record<Weekday, TimeRange[]>;
  holidays: Holiday[];
};
export type OnboardingStepId =
  | "connect_mailbox" | "invite_teammate" | "upload_photo" | "install_widget" | "publish_article" | "setup_triage" | "create_label" | "try_ollie"
  | "setup_ai_agent";
export type OnboardingStep = {
  id: OnboardingStepId;
  title: string;
  description: string;
  done: boolean;
  /** App path that completes the step. */
  href: string;
  requires: "admin" | "agent";
};
export type Onboarding = { steps: OnboardingStep[]; done_count: number; total: number; dismissed: boolean };

// The AI agent's setup checklist (contracts/agent.md "Setup").
export type AgentSetupStepId = "ai_on" | "identity" | "knowledge" | "tools" | "playground" | "widget" | "answers_on" | "email_answers";
export type AgentSetupStep = { id: AgentSetupStepId; title: string; description: string; done: boolean; optional: boolean; href: string };
/** Counts are of required steps; optional steps never block `complete`. */
export type AgentSetup = { steps: AgentSetupStep[]; done_count: number; total: number; complete: boolean };

// AI clients and MCP OAuth (contracts/agent.md "opendesk MCP server").
export type OAuthClientInfo = { name: string; uri: string; redirect_host: string; /** The redirect goes to an app on this computer. */ local: boolean };
export type OAuthRequest = {
  id: string;
  client: OAuthClientInfo;
  scopes: string[];
  /** Each workspace with the requested scopes the user's role there allows. */
  workspaces: { id: string; name: string; role: Role; scopes: string[] }[];
  expires_at: string;
};
export type OAuthDecision = { redirect_to: string };
export type AIClientTool = { name: string; title: string; description: string; scopes: string[]; read_only: boolean; destructive: boolean };
export type AIClientServer = { url: string; resource_metadata: string; authorization_server: string; scopes: string[]; tools: AIClientTool[] };
export type AIClientConnection = {
  id: string;
  client: OAuthClientInfo;
  user: UserRef;
  /** The caller connected it. */
  mine: boolean;
  scopes: string[];
  created_at: string;
  last_used_at: string | null;
};

/** call_ended: a phone call reached its final state (contracts/phone.md). */
export type AutomationTrigger = "ticket_created" | "customer_replied" | "agent_replied" | "ticket_updated" | "call_ended";
export type ConditionField =
  | "subject" | "body" | "from_email" | "channel" | "mailbox_id" | "priority" | "severity" | "status" | "tag" | "sentiment" | "ai_decide"
  /** contracts/teams.md: equals/not_equals with an id (team and widget also "none"); received_at also contains. */
  | "assignee_id" | "team_id" | "brand_id" | "widget_id" | "received_at"
  /** The call of a call_ended automation (contracts/phone.md "Automations"). */
  | "call_outcome" | "call_direction" | "call_missed_reason" | "call_time" | "call_line" | "call_tag"
  /** A support channel destination's: business_hours or outside_business_hours when the event happens. */
  | "time"
  /** A ticket custom field, or the ticket contact's. */
  | `field.${string}` | `contact.field.${string}`;
/** `eq` is the AI sentiment condition's operator (contracts/ai.md). `any` and `none` on subject, body, from_email,
 *  received_at and tag hold phrases, one per line: the text contains any, or none, of them. */
export type ConditionOp = "contains" | "not_contains" | "equals" | "not_equals" | "eq" | "gt" | "lt" | "any" | "none";
/** Custom field conditions (`field.<key>`, `contact.field.<key>`) take the FieldOp set of their type; `ne` aliases `not_equals`. */
/** ai_decide: op gt/lt, value a probability 0.01..0.99, question a yes/no question (3..300 chars). */
export type Condition = { field: ConditionField; op: ConditionOp; value: string; question?: string };
/** A condition on a custom field, with the operators of its type. */
export type FieldCondition = { field: `field.${string}` | `contact.field.${string}`; op: FieldOp; value: string };
export type ActionType =
  | "set_status"
  | "set_priority"
  /** value: a Severity, or "" to clear. */
  | "set_severity"
  | "assign_user"
  /** The agent who wrote the run's reply; skipped when there is none. */
  | "assign_replier"
  | "assign_team"
  | "add_tag"
  | "remove_tag"
  | "send_reply"
  | "add_note"
  | "mark_spam"
  /** Soft-deletes the ticket; later steps and automations skip it. */
  | "delete_ticket"
  /** ticket_created only. value: the window in days, "1".."30"; same_subject (default true). */
  | "merge_into_open_ticket"
  | "ai_triage"
  /** value: a mailbox id of the ticket's brand (contracts/api.md). */
  | "set_reply_address"
  | "set_field";
/**
 * set_field: `field` is the ticket field key, `value` its string form ("12.5", "true", "2026-10-11", "a,b"; "" clears).
 * merge_into_open_ticket: `same_subject` merges only into a ticket with the same subject (contracts/api.md).
 */
export type Action = { type: ActionType; value: string; field?: string; same_subject?: boolean };
export type Automation = {
  id: string;
  name: string;
  trigger: AutomationTrigger;
  conditions: Condition[];
  actions: Action[];
  enabled: boolean;
  position: number;
  run_count: number;
};

export type TlsMode = "tls" | "starttls" | "none";
export type MailboxConfig = {
  imap_host?: string;
  imap_port?: number;
  imap_username?: string;
  imap_tls?: TlsMode;
  smtp_host?: string;
  smtp_port?: number;
  smtp_username?: string;
  smtp_tls?: TlsMode;
  oauth_email?: string;
  /**
   * Read-only. What the Google or Microsoft connection may do: send only (mail arrives by
   * forwarding), sync (reads the inbox over IMAP), or api (reads the inbox directly through
   * the Gmail API or Microsoft Graph).
   */
  oauth_grant?: "send" | "sync" | "api";
  /** Read-only. collab: the connection uses the Collab sign-in (an api grant). */
  oauth_client?: "collab";
  /** Who hosts the mailbox's mail (MailHost), for labels and connection fixes. */
  mail_host?: MailHost | "";
  /** Where the first sync of an inbox that reads its inbox starts: the last 7 days (default) or only new mail. */
  sync_start?: SyncStart;
  /**
   * Tidy-up in Gmail or Outlook, only for a mailbox that reads its inbox directly
   * (oauth_grant api); off (absent) by default. Mark emails read once they become ticket
   * messages, archive a ticket's emails when it is solved or closed, label them "opendesk".
   */
  provider_mark_read?: boolean;
  provider_archive_on_solve?: boolean;
  provider_label?: boolean;
};
export type SyncStart = "recent" | "new";
/** Mail hosts the API names (config.mail_host, detect). */
export type MailHost =
  | "google_workspace"
  | "gmail"
  | "microsoft365"
  | "outlook"
  | "zoho"
  | "yahoo"
  | "aol"
  | "icloud"
  | "fastmail"
  | "godaddy"
  | "namecheap"
  | "ionos"
  | "hostinger"
  | "ovh"
  | "migadu"
  | "purelymail"
  | "rackspace"
  | "yandex"
  | "gmx"
  | "proton"
  | "other";
export type PasswordAuth = "app_password" | "password" | "oauth_only" | "unsupported";
export type MailEndpoint = { host: string; port: number; security: "tls" | "starttls" };
/** POST /v1/mailboxes/detect {email}. */
export type MailDetection = {
  domain: string;
  host: MailHost | "";
  label: string;
  source: "known" | "mx" | "autoconfig" | "ispdb" | "srv" | "none";
  settings: { imap: MailEndpoint; smtp: MailEndpoint } | null;
  password_auth: PasswordAuth;
  app_password_url: string;
  oauth: "google" | "microsoft" | null;
};
/** GET /v1/mailboxes/connect-options. */
export type MailboxConnectOptions = {
  /** api_sync: this workspace may connect the provider to read the inbox directly (it has Collab). */
  google: { configured: boolean; oauth: boolean; api_sync?: boolean };
  microsoft: { configured: boolean; oauth: boolean; api_sync?: boolean };
  forwarding: { enabled: boolean; domain: string };
  system_sender: string;
  /** Mailboxes may send through opendesk (MAILBOX_SYSTEM_SEND). Off, each sends through Google, Microsoft or SMTP. */
  system_send?: boolean;
  /** This workspace's mailboxes may receive over generic IMAP (MAILBOX_IMAP, or the workspace has Collab). */
  mailbox_imap?: boolean;
  /** The instance receives over IMAP (MAILBOX_IMAP): Google and Microsoft may also sync over IMAP. */
  instance_imap?: boolean;
};
/** Why a connection check failed and what fixes it. */
export type MailboxCause = { code: string; title: string; fix: string; retryable: boolean };
/**
 * Whether replies to an alias go out From the alias itself: verified when Gmail lists it as a
 * send-as address or an admin said the provider sends as it; unverified when the provider does
 * not list it or refused it; unknown until then.
 */
export type AliasSendAs = "verified" | "unverified" | "unknown";
export type AliasSettings = {
  /** The From name's {mailbox} for mail to this alias; "" = the mailbox's. */
  display_name: string;
  /** Comes before the team's and the address's signature; "" = none of its own. */
  signature_html: string;
  /** New tickets written to this alias take this brand; null = the mailbox's. */
  brand_id: string | null;
  send_as: AliasSendAs;
  send_as_by: "provider" | "admin" | "";
};
export type ProviderSendAs = {
  /** The connection may read Gmail's send-as list (a sync or api grant, not send-only). */
  readable: boolean;
  checked_at: string | null;
  /** Gmail's send-as addresses other than the account's own. */
  addresses: { email: string; display_name: string; verified: boolean }[];
};
export type AliasSettingsInput = { display_name?: string; signature_html?: string; brand_id?: string | null; send_as?: AliasSendAs };
/** accounts: any of the member's addresses, shared accounts only, or addresses at the mailbox's own domains. */
export type MailboxPersonalReplies = { enabled: boolean | null; reply_to: "address" | "mailbox" | null; accounts: "any" | "shared" | "domain" };
export type Mailbox = {
  id: string;
  name: string;
  email: string;
  display_name: string;
  /** How mail arrives: forwarded to forwarding_address, or fetched over IMAP (imap, or a Google or Microsoft sync). */
  inbound_kind: "forwarding" | "imap" | "google" | "microsoft";
  /** How replies leave. google or microsoft on a forwarding mailbox is a send-only connection. */
  outbound_kind: "smtp" | "google" | "microsoft" | "system";
  forwarding_address: string;
  /**
   * Read-only. imap_off: the mailbox receives over IMAP where receiving over IMAP is now
   * off for its workspace, so no mail arrives until it is switched to forwarding.
   */
  receiving?: "ok" | "imap_off";
  /**
   * Read-only. system_off: the mailbox sends through opendesk on an instance where that
   * is off, so no reply goes out until it connects Google, Microsoft or SMTP.
   */
  sending?: "ok" | "system_off";
  config: MailboxConfig;
  team_id: string | null;
  brand_id: string;
  /** Other addresses that deliver into this mailbox, at most 20. */
  aliases: string[];
  /** An alias to the team its new tickets go to instead of team_id. None on a private team's address. */
  alias_teams?: Record<string, string>;
  /** Every alias's own identity, keyed by the alias (contracts/teams.md "Aliases"). */
  alias_settings?: Record<string, AliasSettings>;
  /** Gmail's send-as list for a Google mailbox; admins only, null otherwise. */
  provider_send_as?: ProviderSendAs | null;
  /** {agent} {agent_first} {team} {brand} {mailbox}. */
  from_name_format: string;
  /** Its team is private: only that team's members send from it. */
  private: boolean;
  /** Replies on its tickets from members' own Collab addresses (contracts/collab.md "Answering tickets"); null follows the workspace. */
  personal_replies?: MailboxPersonalReplies;
  /** Website contact forms (exact addresses or "@domain"); their authenticated mail is from the visitor in Reply-To. Admins only. */
  trusted_form_senders?: string[];
  signature_html: string;
  auto_reply_enabled: boolean;
  auto_reply_subject: string;
  auto_reply_html: string;
  /** Ollie answers emails to this mailbox (contracts/agent.md "Email"), off by default. */
  ai_answers: boolean;
  ai_answers_mode: MailboxAIMode;
  /** Only while business hours are closed (ignored while business hours are off). */
  ai_answers_outside_hours: boolean;
  /** The bot the AI agent answers as (contracts/bots.md); null is the default bot. */
  bot_id?: string | null;
  status: "active" | "error" | "disabled";
  last_error: string;
  last_synced_at: string | null;
  /** When mail last arrived, by any route; null before the first. */
  last_received_at?: string | null;
  created_at: string;
};
/** draft: a suggested reply a teammate sends; send: Ollie replies by email. */
export type MailboxAIMode = "draft" | "send";

export type KbCollection = {
  id: string;
  name: string;
  slug: string;
  description: string;
  position: number;
  article_count: number;
  /** The brand whose help center shows it; null = every brand (contracts/teams.md). Older servers omit it. */
  brand_id?: string | null;
};
export type KbArticle = {
  id: string;
  collection_id: string | null;
  /** null = every brand. An article in a collection with a brand belongs to the collection's brand. Older servers omit it. */
  brand_id?: string | null;
  title: string;
  slug: string;
  excerpt: string;
  body_html: string;
  status: "draft" | "published";
  author: UserRef | null;
  views: number;
  published_at: string | null;
  updated_at: string;
};

/** A product update the help center publishes at /changelog (contracts/api.md "Changelog"). */
export type ChangelogEntry = {
  id: string;
  /** 1..200 characters. */
  title: string;
  /** Unique in the workspace; the entry's address is <help center>/changelog/<slug>. */
  slug: string;
  /** Plain text, up to 300; derived from the body unless written. */
  excerpt: string;
  body_html: string;
  /** Up to 5 names, each up to 32 characters, compared without case. */
  categories: string[];
  status: "draft" | "published";
  /** The date it carries: set when first published unless given. Published and in the future = scheduled. */
  published_at: string | null;
  author: UserRef | null;
  /** The brand whose help center shows it; null = every brand. */
  brand_id: string | null;
  created_at: string;
  updated_at: string;
};

/** A category the workspace's changelog entries use, with how many use it. */
export type ChangelogCategory = { name: string; count: number };

export type ApiKey = {
  id: string;
  name: string;
  prefix: string;
  scopes: string[];
  last_used_at: string | null;
  expires_at: string | null;
  created_at: string;
  created_by: UserRef | null;
  private_team_ids?: string[];
  private_teams?: PrivateTeamGrant[];
};
export type Webhook = {
  id: string;
  url: string;
  description: string;
  events: string[];
  /** Configured custom request header names. Header values are never returned. */
  header_names: string[];
  enabled: boolean;
  /** The address answered its verification challenge; until then it receives no events. */
  verified: boolean;
  created_at: string;
  private_team_ids?: string[];
  private_teams?: PrivateTeamGrant[];
};
/** A private team of an API key or webhook, kept while its granter is on the team (contracts/teams.md "Visibility"). */
export type PrivateTeamGrant = { id: string; name: string; granted_by: UserRef | null };
/** A product connected for Ollie over MCP (contracts/integrations.md). */
export type IntegrationStarter = { label: string; prompt: string };
export type IntegrationCategory = "issues" | "docs" | "chat" | "crm" | "payments" | "automation" | "files";
export type IntegrationProvider = {
  key: string;
  name: string;
  category: IntegrationCategory;
  description: string;
  url: string;
  auth: "oauth" | "token";
  token_help?: string;
  token_url?: string;
  starters: IntegrationStarter[];
};
export type IntegrationTool = {
  name: string;
  title: string;
  description: string;
  /** Runs without approval: the server marks it read-only and no admin set it to ask first. */
  read_only: boolean;
  /** The server marks it read-only (its own hint). */
  read_only_hint: boolean;
  /** An admin set it to wait for approval anyway. */
  ask_first: boolean;
  /** A write an admin lets run without asking (auto_tools); ask_first wins over it. */
  auto_run: boolean;
  enabled: boolean;
};
export type Integration = {
  id: string;
  /** A catalog key, or "custom". */
  provider: string;
  name: string;
  host: string;
  url?: string;
  /** A custom server's own icon (its advertised icon, else its site's), or null. */
  icon_url: string | null;
  auth_type: "oauth" | "token" | "none";
  status: "pending" | "connected" | "error";
  enabled: boolean;
  last_error: string;
  tools: IntegrationTool[];
  starters: IntegrationStarter[];
  synced_at: string | null;
  created_at: string;
};
/** An agent-facing provider that mirrors email tickets into its own threads. */
export type SupportChannelProvider = {
  key: string;
  name: string;
  description: string;
  /** opendesk sets the provider up itself; otherwise an admin enters webhook_url there. */
  configures?: boolean;
  fields: { name: string; label: string; secret: boolean; generated?: boolean; discovered?: boolean; help?: string }[];
};
export type SupportChannelAccount = { id: string; name: string };

/** contracts/phone.md. */
export type PhoneProvider = { key: string; name: string; fields: { name: string; label: string; secret: boolean }[] };
export type PhoneHealthCode = "" | "receiving" | "webhook_missing" | "credentials_refused" | "provider_unreachable";
export type PhoneConnection = {
  id: string;
  provider: string;
  name: string;
  account_name: string;
  status: "connected" | "error";
  last_error: string;
  health_status: "unknown" | "healthy" | "action_required" | "error";
  health_code: PhoneHealthCode;
  health_detail: string;
  health_checked_at: string | null;
  /** A call goes on the caller's latest ticket solved within this many days; 0 opens a new one. */
  reuse_days: number;
  assign_answered: boolean;
  insight_cards: boolean;
  last_event_at: string | null;
  /** Where the provider sends events: admins only. */
  webhook_url?: string;
  created_at: string;
};
export type PhoneLine = { id: string; name: string; provider: string };
export type SupportChannelConnection = {
  id: string;
  provider: string;
  name: string;
  external_account_id: string;
  external_account_name: string;
  /** The bot's @username where people address it by one (Telegram). */
  external_account_handle?: string;
  status: "connected" | "error";
  last_error: string;
  health_status: "unknown" | "healthy" | "action_required" | "error";
  health_detail: string;
  health_checked_at?: string;
  health_unhealthy_since?: string;
  health_alerted_at?: string;
  webhook_url: string;
  created_at: string;
};
export type SupportChannelAvailableDestination = {
  id: string;
  name: string;
  kind?: string;
  available: boolean;
};
export type SupportChannelDestination = {
  id: string;
  connection_id: string;
  team_id: string | null;
  priority: "low" | "normal" | "high" | "urgent" | null;
  external_channel_id: string;
  external_channel_name: string;
  enabled: boolean;
  /** Internal notes are posted too; off by default. */
  include_notes: boolean;
  /** Maximum characters before the provider message links back to opendesk. */
  message_limit: number;
  /** route: the most specific matching route on the connection posts the ticket. copy: posts every matching ticket as well. */
  mode: SupportChannelDestinationMode;
  /** Every condition must hold for a ticket's thread to start here (contracts/support-channels.md "Routing"). */
  conditions: Condition[];
  /** What the threads show besides the ticket card. */
  mirror: SupportChannelMirror[];
  /** Ollie posts a summary when a ticket's thread opens. */
  ollie_summary: boolean;
  /** Ollie posts SLA and sentiment warnings in ticket threads. */
  ollie_warnings: boolean;
  /** How Ollie sounds in this channel's threads; empty for the default. */
  ollie_personality: string;
  ollie_length: SupportChannelOllieLength;
  created_at: string;
};
export type SupportChannelDestinationMode = "route" | "copy";
export type SupportChannelMirror = "customer_replies" | "agent_replies" | "status_changes" | "assignee_changes" | "priority_changes";
/** Where a destination's threads start and what they show; the create and update bodies share it. */
export type SupportChannelRouting = Partial<Pick<SupportChannelDestination, "mode" | "conditions" | "mirror" | "include_notes">>;
export type SupportChannelOllieLength = "brief" | "standard" | "detailed";
/** Ticket changes Ollie may choose in support channel threads. */
export type SupportChannelOllieAction = "reply" | "note" | "assign_self" | "status" | "priority" | "create_ticket";
export type SupportChannelOllie = { actions: SupportChannelOllieAction[] };
export type SupportChannelOllieUpdate = Partial<Pick<SupportChannelDestination, "ollie_summary" | "ollie_warnings" | "ollie_personality" | "ollie_length">>;
export type SupportChannelDelivery = {
  event_seq: number;
  event_type: string;
  ticket_number?: number;
  status: "queued" | "retrying" | "delivered" | "failed";
  attempts: number;
  last_error: string;
  created_at: string;
  updated_at: string;
  delivered_at?: string;
};
/** A field of Shopify's answer a ticket panel can show (contracts/integrations.md "Ticket panels"). */
export type ShopifyField =
  | "tags"
  | "email"
  | "orders_count"
  | "total_spent"
  | "financial_status"
  | "fulfillment_status"
  | "line_items"
  | "line_prices"
  | "shipping"
  | "discounts"
  | "refunds"
  | "tracking";
/** A ticket panel (contracts/integrations.md "Ticket panels"): read-only data about a ticket's contact. */
export type TicketPanel = {
  id: string;
  kind: "shopify" | "http";
  name: string;
  /** shopify: the shop's myshopify.com domain; http: the URL's host. */
  host: string;
  /** shopify: client_credentials when OpenDesk asks Shopify for its token with the app's client ID and secret; access_token when a token was pasted. */
  auth?: "client_credentials" | "access_token";
  /** shopify client_credentials: the app's client ID (not a secret). */
  client_id?: string;
  /** shopify: the fields tickets show, in catalogue order (the defaults until an admin chooses); http: empty. */
  fields: ShopifyField[];
  /** shopify: how many of the last orders show, 1 to 5. */
  orders_shown?: number;
  /** http: the regular expression (RE2) that finds order and invoice numbers in the ticket for the request's `ticket.references`; empty finds none. */
  reference_pattern?: string;
  enabled: boolean;
  position: number;
  /** error: the last lookup found the credentials refused. */
  status: "connected" | "error";
  last_error: string;
  created_at: string;
  updated_at: string;
};
/** A field the panel does not show is left out, and `tags` is empty. */
export type PanelCustomer = { name: string; url: string; email?: string; orders_count?: number; total_spent?: string; currency?: string; tags: string[] };
/** A field the panel does not show is left out, and a list is empty. */
export type PanelOrder = {
  id: string;
  number: string;
  url: string;
  created_at: string;
  /** Decimal, after refunds. */
  total: string;
  currency: string;
  financial_status?: string;
  fulfillment_status?: string;
  cancelled: boolean;
  cancelled_at?: string;
  /** Decimal, what was refunded; absent when nothing was. */
  refunded?: string;
  discount_codes: string[];
  /** Where it goes (city and ISO country, never the street) and the shipping method; absent when there is neither. */
  shipping?: { city?: string; country?: string; method?: string };
  /** `price`: the unit price before discounts, decimal, in the order's currency. */
  items: { title: string; variant?: string; quantity: number; price?: string }[];
  tracking: { company?: string; number?: string; url?: string }[];
};
export type PanelSection = { title: string; items: { label: string; value: string; url?: string }[] };
/** A tone of an http panel's badges and notes. */
export type PanelTone = "neutral" | "info" | "success" | "warning" | "danger";
export type PanelBadge = { text: string; tone: PanelTone };
/**
 * A value of an http panel's blocks, as the server cleaned it: always an object. `date` is
 * YYYY-MM-DD for a day, else an RFC 3339 time in UTC; `money` a decimal and an ISO 4217 code.
 */
export type PanelValue =
  | { type: "text"; text: string }
  | { type: "date"; value: string; relative?: boolean }
  | { type: "money"; amount: string; currency: string }
  | { type: "badge"; text: string; tone: PanelTone };
export type PanelField = { label?: string; value: PanelValue; url?: string };
export type PanelLine = { quantity?: number; title: string; subtitle?: string; value?: PanelValue; url?: string };
export type PanelListItem = { title: string; subtitle?: string; value?: PanelValue; badge?: PanelBadge; url?: string; links?: PanelLink[] };
/** The record in another tool: a small button by the title. `icon` is one the app draws (TicketPanels' LINK_ICONS or a shipped logo), else the label's first letter shows. */
export type PanelLink = { label: string; url: string; icon?: string };
/** A block of an http panel's version 2 answer (contracts/integrations.md "The http answer"). */
export type PanelBlock =
  | { type: "card"; title: string; url?: string; subtitle?: string; badges?: PanelBadge[]; links?: PanelLink[]; collapsed?: boolean; fields?: PanelField[]; lines?: PanelLine[]; footer?: PanelField[] }
  | { type: "fields"; title?: string; items: PanelField[] }
  | { type: "list"; title?: string; items: PanelListItem[] }
  | { type: "note"; text: string; tone: PanelTone };
export type TicketPanelData = {
  panel_id: string;
  kind: "shopify" | "http";
  state: "ok" | "empty" | "no_contact" | "error";
  message?: string;
  customer?: PanelCustomer;
  orders?: PanelOrder[];
  /** http: a version 1 answer's sections. */
  sections?: PanelSection[];
  /** http: a version 2 answer's blocks. */
  blocks?: PanelBlock[];
  fetched_at: string;
};
export type SupportChannelOperations = {
  window_started_at: string;
  generated_at: string;
  connections: { total: number; healthy: number; unhealthy: number; alerted: number };
  deliveries: {
    queued: number;
    retrying: number;
    delivered: number;
    failed: number;
    average_latency_ms: number;
    maximum_latency_ms: number;
    oldest_pending_at?: string;
  };
  webhooks: { rejected: number; duplicates_suppressed: number };
};
export type TicketLink = {
  id: string;
  ticket_id: string;
  integration_id: string | null;
  /** A catalog key, "custom" or "link". */
  provider: string;
  url: string;
  title: string;
  actor_type: "user" | "ai" | "api";
  created_by: string | null;
  created_at: string;
};
export type WebhookDelivery = {
  id: string;
  event_id: string;
  event_type: string;
  status: "pending" | "succeeded" | "failed";
  attempts: number;
  response_code: number | null;
  duration_ms: number | null;
  created_at: string;
};
export type AuditLog = {
  id: string;
  actor: { type: "user" | "api_key" | "system"; user: UserRef | null; api_key_id: string | null };
  action: string;
  entity_type: string;
  entity_id: string | null;
  data: Record<string, unknown>;
  ip: string;
  created_at: string;
};
export type NotificationType = "assigned" | "new_ticket" | "mentioned" | "customer_replied" | "thread_replied" | "sla_breached" | "ai_handoff" | "ai_cap_reached" | "ai_balance_low" | "ai_balance_out" | "auto_recharge_failed" | "ai_key_failed" | "team_joined" | "import_finished" | "support_channel_unhealthy" | "collab_assigned" | "collab_mention";
/** `ai_cap_reached`: data `{cap_usd}`, ticket null; the widget agent reached `widget_monthly_cap_usd` (once a month, owners and admins).
 * `ai_balance_low` / `ai_balance_out`: data `{remaining_usd}` (US dollars); `auto_recharge_failed`: data `{reason: AutoRechargeFailure}` (billing, owners and admins, ticket null).
 * `ai_key_failed`: data `{provider: AIKeyKind, reason: "rejected" | "no_funds"}`; the workspace's own AI key failed (at most hourly, owners and admins, ticket null).
 * `thread_replied`: data `{message_id, thread_id, subject, preview, from: {name, email}}`; someone outside the team answered a third-party thread on a ticket assigned to you.
 * `team_joined`: data `{team_id, team: {id, name, private}, user: {id, name}, by}`; an admin joined a team you lead (ticket null).
 * `support_channel_unhealthy`: data `{connection_id, name, provider, status, detail}`; a provider connection stayed unhealthy for 15 minutes (owners and admins, ticket null).
 * `import_finished`: data `{import_id, source, source_name, account, status: "completed" | "failed" | "needs_credentials", tickets, updated, articles, failed, error}`;
 * a history import you started ended or paused for new credentials (contracts/imports.md, ticket null). */
export type Notification = {
  id: string;
  type: NotificationType;
  ticket: { id: string; number: number; subject: string } | null;
  data: Record<string, unknown>;
  read_at: string | null;
  created_at: string;
};

/** Where a notification can reach someone besides the bell: email, their phones (`push`) and the desktop app or browser (`desktop`). */
export type NotificationChannels = { email: boolean; push: boolean; desktop: boolean };
export type NotificationChannel = keyof NotificationChannels;
export type NotificationGroup = "tickets" | "team" | "mail" | "ai_billing" | "workspace";
/** `urgent`: may be emailed sooner than the cooldown (contracts/api.md "Notification preferences and emails"). */
export type NotificationTypePrefs = NotificationChannels & { type: NotificationType; group: NotificationGroup; defaults: NotificationChannels; urgent: boolean };
/** How the server paces notification emails for this person. */
export type NotificationPacing = {
  /** The first email after a quiet spell waits this long. */
  delay_minutes: number;
  /** The gap after each email while activity continues, growing. */
  cooldown_minutes: number[];
  /** After this long with nothing new, the next email is "first" again. */
  quiet_reset_minutes: number;
  /** Urgent ones may cut a cooldown short, but never sooner than this after the last email. */
  urgent_gap_minutes: number;
  /** At most this many early emails a day. */
  urgent_per_day: number;
  /** Hard caps on notification emails per person; past them everything rolls into the next allowed email. */
  per_hour: number;
  per_day: number;
  /** Emails wait while you are active in this workspace in the app (a heartbeat within this many minutes). */
  active_minutes: number;
};
export type EmailCadence = "instant" | "hourly" | "daily" | "off";
/**
 * The caller's notification preferences in the current workspace. Channels are per
 * workspace; the email schedule and the pause are the account's, across workspaces.
 */
export type NotificationPreferences = {
  email_cadence: EmailCadence;
  /** 0..23, the daily digest's hour in `timezone`. */
  digest_hour: number;
  /** IANA, e.g. "Europe/Budapest"; "UTC" until the app sends one. */
  timezone: string;
  /** No email, push or desktop notification while this is in the future. */
  paused_until: string | null;
  /** Where notification emails go. */
  email: string;
  /** Notification emails go to a confirmed address only. */
  email_verified: boolean;
  /** How long an unread notification waits before "instant" emails it (the same as `pacing.delay_minutes`). */
  delay_minutes: number;
  pacing: NotificationPacing;
  /** An email's unsubscribe link turned off email from this workspace; `resubscribe` restores the choices from before. */
  unsubscribed_at: string | null;
  /** The types the caller can receive here: owners and admins also get the ai_billing group. */
  types: NotificationTypePrefs[];
  /** The teams the caller leads or works in, by name. notify false: nothing of its tickets nobody has (new tickets, their replies). */
  teams: NotificationTeam[];
};
export type NotificationTeam = { id: string; name: string; notify: boolean };
/** PATCH /v1/me/notification-preferences: only what changed. `paused_until: ""` resumes. */
export type NotificationPreferencesPatch = {
  email_cadence?: EmailCadence;
  digest_hour?: number;
  timezone?: string;
  paused_until?: string;
  types?: ({ type: NotificationType } & Partial<NotificationChannels>)[];
  teams?: { id: string; notify: boolean }[];
};

// Lists and envelopes.
export type Page<T> = { data: T[]; next_cursor: string | null };

export type Envelope<D = Record<string, unknown>> = {
  id: string;
  seq: number;
  workspace_id: string;
  type: string;
  visibility: "agents" | "public";
  /** Not sent over the gateway (it pushes the envelope minus topics); present in GET /v1/events. */
  topics?: string[];
  data: D;
  occurred_at: string;
};
export type EventsPage = { data: Envelope[]; latest_seq: number; resync: boolean };

// Auth.
export type AuthProvider = { id: "google" | "microsoft" | "github" | "oidc"; name: string };
export type AuthConfig = {
  providers: AuthProvider[];
  password_login: boolean;
  signup_enabled: boolean;
  turnstile_site_key: string | null;
  /** The VAPID public key browsers subscribe to web pushes with; null when the server sends none. */
  web_push_key: string | null;
};
export type TokenResponse = {
  access_token: string;
  expires_in: number;
  user: User;
  redirect_to?: string;
  /** The password signed in with is breached or shorter than the rules now ask. */
  change_password?: boolean;
};
/** One account this browser is signed in to (POST /v1/auth/accounts). */
export type Account = {
  user: Pick<User, "id" | "name" | "email" | "avatar_url">;
  memberships: { workspace: Pick<Workspace, "id" | "name" | "slug" | "avatar_url">; role: Role }[];
};
export type MfaChallenge = { mfa_required: true; mfa_token: string; redirect_to?: string };
/** OAuth exchange: the provider's email belongs to an existing password account. */
export type LinkRequired = { link_required: true; link_token: string; email: string; redirect_to?: string };
export type LoginResponse = TokenResponse | MfaChallenge;
export type ExchangeResponse = TokenResponse | MfaChallenge | LinkRequired;
export type Me = { user: User; memberships: Membership[] };
export type Session = {
  id: string;
  user_agent: string;
  ip: string;
  created_at: string;
  last_used_at: string;
  current: boolean;
};

export type TicketCounts = {
  unassigned: number;
  mine: number;
  open: number;
  pending: number;
  on_hold: number;
  solved: number;
  spam: number;
  views: Record<string, number>;
  /** Every team the caller can see. */
  teams?: Record<string, { open: number; unassigned: number }>;
  my_teams?: { open: number; unassigned: number };
  /** Per sidebar row id (ticket_folder:<id>, team:<id>, team:mine, view:<id>), over the row's own tickets. */
  stats?: Record<string, TicketStats>;
};

/** A sidebar row's tickets awaiting a reply (the customer wrote last), urgent, and overdue. */
export type TicketStats = { awaiting: number; urgent: number; overdue: number };

/**
 * A whole ticket list, every page of it (GET /v1/tickets/summary). `complete` is false, with
 * every count 0, when the server did not count it: too many tickets match, or it is a search.
 */
export type TicketListSummary = TicketStats & { complete: boolean; total: number; due_soon: number };

/** `wiki_pages` is left out by servers without the wiki and for callers without `wiki:read`. */
export type SearchResults = { tickets: Ticket[]; contacts: Contact[]; articles: KbArticle[]; wiki_pages?: WikiSearchHit[] };

// ─── Wiki (contracts/wiki.md) ───────────────────────────────────────────────

/** A page in the tree, without its body. */
export type WikiNode = {
  id: string;
  parent_id: string | null;
  /** "" until named: shown as "Untitled". */
  title: string;
  /** One emoji, or "". */
  icon: string;
  /** Order among its siblings, from 0. */
  position: number;
  /** Moves on every change of title, icon or body; PATCH sends it back. */
  version: number;
  updated_at: string;
  /** Who created it; null for an API key or a deleted account. */
  created_by_id: string | null;
};

export type WikiRef = { id: string; title: string; icon: string };

export type WikiPage = WikiNode & {
  body_html: string;
  created_by: UserRef | null;
  updated_by: UserRef | null;
  created_at: string;
  /** Its ancestors, the top-level page first. */
  breadcrumbs: WikiRef[];
  children: WikiRef[];
};

export type WikiRevision = {
  id: string;
  title: string;
  icon: string;
  /** Only when one revision is read. */
  body_html?: string;
  author: UserRef | null;
  created_at: string;
  updated_at: string;
};

export type WikiSearchHit = {
  id: string;
  parent_id: string | null;
  title: string;
  icon: string;
  /** Plain text around the match. */
  snippet: string;
  path: WikiRef[];
  updated_at: string;
};

export type WikiPagePatch = { title?: string; icon?: string; body_html?: string; version?: number };

export type ReportsOverview = {
  created: number;
  solved: number;
  open_now: number;
  first_response_p50_minutes: number | null;
  resolution_p50_minutes: number | null;
  csat_avg: number | null;
  csat_count: number;
  sla_breached: number;
  by_day: { date: string; created: number; solved: number }[];
  by_channel: Partial<Record<Channel, number>>;
  /** Tickets created in the range, by severity. */
  by_severity?: Partial<Record<Severity | "none", number>>;
  by_agent: { user: UserRef; solved: number; first_response_p50_minutes: number | null }[];
  /** Every team with tickets in the range or open now; `team_id` null counts tickets without a team. Older servers omit it. */
  by_team?: (ReportsGroup & { team_id: string | null })[];
  /** Every brand with tickets in the range or open now. Older servers omit it. */
  by_brand?: (ReportsGroup & { brand_id: string })[];
};
/** One team's or brand's row of the overview. */
export type ReportsGroup = { created: number; solved: number; open_now: number; first_response_p50_minutes: number | null };

/** One member's row of GET /v1/reports/agents (contracts/api.md "Agents report"). */
export type ReportsAgentRow = {
  user: UserRef;
  /** Tickets assigned to them in the range, or created in it assigned to them. */
  assigned: number;
  /** Tickets solved in the range that are assigned to them. */
  solved: number;
  /** Public replies they sent in the range. */
  replies: number;
  /** Internal notes they wrote in the range. */
  notes: number;
  /** Their public replies on the tickets they solved, per ticket. */
  replies_per_solved: number | null;
  first_response_p50_minutes: number | null;
  /** Median time from the last assignment to them until solved. */
  handle_p50_minutes: number | null;
  /** Time in the app with availability online; null when `online_time` is none or off. */
  online_minutes: number | null;
};
/**
 * Whose rows and online time the caller sees: everyone's (admins), their own row only, or every row without online time (API keys).
 * off: the workspace turned online time off (settings.agent_online_time), so no row has any; the rows are still the caller's.
 */
export type ReportsOnlineTime = "all" | "self" | "none" | "off";
export type ReportsAgents = { data: ReportsAgentRow[]; online_time: ReportsOnlineTime };

export type MailboxTestLeg = { ok: boolean; error: string; /** The server's reply, scrubbed. */ detail?: string };
/** GET /v1/mailboxes/{id}/usage: the tickets that reply from a mailbox, before removing it. */
export type MailboxUsage = { tickets: number; open: number };

export type MailboxTest = {
  inbound: MailboxTestLeg;
  outbound: MailboxTestLeg;
  cause?: MailboxCause | null;
  /** The SMTP port that answered in place of the configured one; the mailbox uses it now. */
  smtp_port?: number;
};

// The chat widget (contracts/api.md "Widget").
export type WidgetTheme = "auto" | "light" | "dark";
export type WidgetRadius = "square" | "soft" | "round";
export type WidgetSettings = {
  enabled: boolean;
  /** Up to 300 chars, plain text with newlines. */
  greeting: string;
  /** Under the greeting in a new chat; up to 200 chars, plain text with newlines; "" is the translated default. */
  intro: string;
  /** A new chat opens with the greeting and intro; off, it opens empty. */
  chat_welcome: boolean;
  require_email: boolean;
  /** At most 20. */
  allowed_origins: string[];
  /** "#rrggbb", or "" for the workspace brand colour. */
  color: string;
  theme: WidgetTheme;
  position: "right" | "left";
  /** 0..200 px from the side. */
  offset_x: number;
  /** 0..200 px from the bottom. */
  offset_y: number;
  launcher: "icon" | "label";
  /** Up to 24 chars; "" is the translated "Chat with us". */
  launcher_label: string;
  radius: WidgetRadius;
  /** Up to 40 chars; "" is the workspace name. */
  title: string;
  /** Up to 80 chars; "" is the translated reply-time line. */
  subtitle: string;
  show_team: boolean;
  /** At most 4 prompts of 2..60 chars. */
  suggestions: string[];
  sound: boolean;
  /** At most 8. When set, the widget asks what the visitor needs before the first message. */
  topics: WidgetTopic[];
  /** Shown while the routed team is closed; "" is the translated default. */
  offline_message: string;
  /** Email agent replies to a visitor who left an email and is not connected. */
  email_follow_up: boolean;
  /** Visitors may send files. */
  attachments: boolean;
  /** Ids of the file type catalog (components/widget/fileTypes.ts) visitors may send; [] takes any file but programs and scripts. */
  attachment_types: string[];
  /** Visitors may record voice messages, whether or not they may send files. */
  voice_messages: boolean;
  /** The longest voice message, 5 to 300 seconds. */
  voice_max_seconds: number;
};
/** GET /v1/widget/config: what the widget renders. */
export type WidgetConfig = {
  workspace_name: string;
  brand_color: string | null;
  greeting: string;
  help_center_url: string | null;
  require_email: boolean;
  locale: string | null;
  avatar_url: string | null;
  launcher_icon_url: string | null;
  theme: WidgetTheme;
  position: "right" | "left";
  offset_x: number;
  offset_y: number;
  launcher: "icon" | "label";
  launcher_label: string;
  radius: WidgetRadius;
  title: string;
  subtitle: string;
  suggestions: string[];
  sound: boolean;
  /** Visitors may send files; older servers omit it. */
  attachments?: boolean;
  /** Extensions of the types visitors may send; null for any file but programs and scripts; [] while attachments is off. */
  attachment_extensions?: string[] | null;
  /** Visitors may record voice messages; older servers omit it. */
  voice_messages?: boolean;
  voice_max_seconds?: number;
  team: { name: string; avatar_url: string | null }[];
  ai_answers: boolean;
  /** The widget's bot, else the default bot (contracts/bots.md). */
  bot: WidgetBot;
  topics?: { id: string; label: string }[];
  /** Business hours of the routed team, else the workspace's. */
  open?: boolean;
  offline_message?: string;
  /** Under the greeting in a new chat; older servers omit it. */
  intro?: string;
  /** false: a new chat opens empty; older servers omit it. */
  chat_welcome?: boolean;
  /** The widget's fixed language; "" or missing: the visitor's browser. */
  language?: string;
  brand?: { name: string; color: string | null };
};

/** POST /v1/public/csat/survey {token}. `locale` is the workspace default ("" when not set). */
export type CsatSurvey = {
  workspace_name: string;
  brand_color: string;
  ticket: { number: number; subject: string };
  score: number | null;
  comment: string;
  locale: string;
  /** The survey always uses locale; older servers omit it. */
  locale_fixed?: boolean;
};

export type TicketPatch = {
  subject?: string;
  /** Optional workspace-unique reference; null clears it. */
  alias?: string | null;
  status?: TicketStatus;
  priority?: Priority;
  /** null clears. */
  severity?: Severity | null;
  /** Changes the contact who receives future replies. */
  contact_id?: string;
  assignee_id?: string | null;
  team_id?: string | null;
  /** The address replies go out from (an address of the ticket's brand). */
  mailbox_id?: string;
  brand_id?: string;
  snoozed_until?: string | null;
  spam?: boolean;
  /** Replaces the tags, by name. */
  tags?: string[];
  /** Adds these tags, keeping the others. */
  add_tags?: string[];
  remove_tags?: string[];
  custom?: Record<string, unknown>;
  /** Merged into fields; null clears. Solving with an empty required field is 422 field_required. */
  fields?: FieldValuesPatch;
};

export type ReportsRange = { from: string; to: string };
/** The overview's query: a range, narrowed to a team (`none`: no team) and a brand. */
export type ReportsQuery = ReportsRange & { team_id?: string; brand_id?: string };

// Billing (contracts/billing.md).
export type Plan = "free" | "pro" | "collab" | "self_hosted";
export type BillingStatus = "none" | "trialing" | "active" | "past_due" | "unpaid" | "canceled" | "incomplete" | "incomplete_expired" | "paused";
export type Price = { id: string; unit_amount: number; currency: string; interval: "month" | "year" | null; /** ai_top_up: the AI usage `unit_amount` buys, in US dollars. */ usd?: number };
export type Billing = {
  plan: Plan;
  billing_enabled: boolean;
  status: BillingStatus;
  interval: "month" | "year";
  seats: number;
  billed_seats: number;
  current_period_end: string | null;
  cancel_at_period_end: boolean;
  /** 0 = unlimited. */
  limits: { max_mailboxes: number; max_members: number };
  usage: { mailboxes: number; members: number };
  ai: AIBalance;
  /** collab_*: only when the collab plan is offered to the workspace (contracts/billing.md "Collab"). */
  prices: { pro_month: Price | null; pro_year: Price | null; ai_top_up: Price | null; collab_month?: Price | null; collab_year?: Price | null };
  /** The workspace's Collab access and whether it is on; older servers omit it. */
  collab?: BillingCollab;
  /** null when billing is disabled; older servers omit it. */
  auto_recharge?: AutoRecharge | null;
  /** What top-ups and auto-recharge charge; null when none is saved. */
  payment_method?: PaymentMethod | null;
};
/** Billing.collab (contracts/billing.md "Collab"). */
export type BillingCollab = {
  access: "none" | "offered" | "complimentary";
  /** Complimentary access ends then; null = until changed. */
  until: string | null;
  /** The workspace has Collab now. */
  enabled: boolean;
  /** The workspace may subscribe or switch to the collab plan. */
  available: boolean;
};
export type PaidPlan = "pro" | "collab";
/** Billing.payment_method: the saved card as people recognise it. */
export type PaymentMethod = {
  /** visa, mastercard, amex, ... or the payment method type (link, sepa_debit, ...). */
  brand: string;
  last4: string;
  exp_month?: number;
  exp_year?: number;
  /** apple_pay, google_pay, link, ... when the card is in a wallet. */
  wallet?: string;
};
/** GET /v1/billing/invoices: the workspace's recent invoices, newest first. `total` in minor units. */
export type BillingInvoice = {
  id: string;
  number: string;
  created: string;
  total: number;
  currency: string;
  status: "paid" | "open" | "uncollectible";
  kind: "subscription" | "top_up" | "auto_recharge" | "other";
  description: string;
  /** Stripe's page for the invoice: view, download or pay it. */
  url: string;
  pdf_url: string;
};
/** POST /v1/billing/top-up: AI usage bought with the saved payment method. */
export type TopUpResult = { status: "paid" | "processing"; usd: number; billing: Billing };
/** Billing.ai: the workspace's AI balance. Every `*_usd` value is US dollars, up to 6 decimals. */
export type AIBalance = {
  /** Self-hosted: no allowance, nothing refused. */
  unlimited: boolean;
  /** The monthly AI usage allowance ($0.50 free; $10 per paid seat on Pro). */
  monthly_usd: number;
  /** Used of the allowance this period (with unlimited AI: all usage this period). */
  used_usd: number;
  /** Top-ups left; they never expire and are spent after the allowance. */
  purchased_usd: number;
  /** What is left of the allowance plus purchased_usd. */
  remaining_usd: number;
  period_end: string;
};
export type AutoRechargeFailure = "card_declined" | "authentication_required" | "no_payment_method" | "payment_failed" | "unavailable";
/** Billing.auto_recharge (contracts/billing.md "Auto-recharge"). `*_usd` in US dollars; `amount` in minor units. */
export type AutoRecharge = {
  enabled: boolean;
  /** Recharge when the AI balance falls below it (default $5). */
  threshold_usd: number;
  /** AI usage each recharge adds, $5..$5,000, charged dollar for dollar (default $25). */
  amount_usd: number;
  /** At most this much is recharged per calendar month, UTC (default $250). */
  monthly_limit_usd: number;
  /** Recharged this month, paid or still being paid. */
  charged_this_month_usd: number;
  recharges_this_month: number;
  /** An AI balance below this reads as running low. */
  low_at_usd: number;
  /** `usd` is the AI usage added; `amount` what was charged. */
  last_recharge: { at: string; usd: number; amount: number; currency: string } | null;
  /** The bank declined the last recharge for now; it is tried again at `at`. */
  retry: { at: string; code: "card_declined" } | null;
  failure: { code: AutoRechargeFailure; at: string } | null;
};
export type AutoRechargePatch = Partial<Pick<AutoRecharge, "enabled" | "threshold_usd" | "amount_usd" | "monthly_limit_usd">>;
export type AIFeature =
  | "summarize" | "rewrite" | "translate" | "triage" | "decide" | "answer_check" | "copilot" | "draft_reply" | "answer" | "answer_email" | "playground" | "kb_draft" | "assistant" | "assistant_peer";
/**
 * GET /v1/billing/usage (contracts/billing.md). `from`/`to` are RFC 3339; `to` is exclusive.
 * Every usd value is US dollars, a number with up to 6 decimals.
 */
export type BillingUsage = {
  from: string;
  to: string;
  total_usd: number;
  total_calls: number;
  by_feature: { feature: AIFeature; usd: number; calls: number }[];
  by_day: { date: string; usd: number; calls: number; by_feature?: Partial<Record<AIFeature, number>> }[];
  by_member: { user: UserRef | null; usd: number; calls: number }[];
  /** One row per model and source. */
  by_model?: { model: string; usd: number; calls: number; input_tokens: number; output_tokens: number; source?: "instance" | "own_key" }[];
  /** Calls made on the workspace's own AI provider key ($0 each). */
  own_key_calls?: number;
};

// AI (contracts/ai.md).
export type Sentiment = "positive" | "neutral" | "negative" | "frustrated";
/** The built-in changes a workspace may let Ollie make without asking. Replies and the API's changes always ask. */
export const ASSISTANT_AUTO_TOOLS = ["update_ticket", "bulk_update_tickets", "add_note", "create_kb_draft", "create_view", "create_automation_draft"] as const;
export type AssistantAutoTool = (typeof ASSISTANT_AUTO_TOOLS)[number];
export type AISettings = {
  enabled: boolean;
  auto_triage: boolean;
  auto_summarize: boolean;
  widget_answers: boolean;
  /** The assistant panel (Ollie). Default true. */
  assistant: boolean;
  /** Members may let teammates' Ollie ask theirs about their own mail (contracts/ai.md "Asking a teammate"). Default false. */
  assistant_peer?: boolean;
  /** Built-in changes Ollie makes without asking (contracts/ai.md "Approvals"). Default empty. */
  assistant_auto?: AssistantAutoTool[];
  tone: string;
  reply_language: "customer" | "workspace";
  instructions: string;
  /** Drafts, summaries, copilot, KB drafts, Ollie; "" is the instance default. */
  model?: string;
  /** Rewrite, translate, LLM triage and decisions; "" is the instance fast model. */
  fast_model?: string;
  /** The widget agent; "" follows `model`. */
  widget_model?: string;
  /** 1..100, default 20: agent replies in one conversation before it hands off. */
  widget_max_replies?: number;
  /** At most 20 topics of 2..100 chars the agent declines. */
  widget_blocked_topics?: string[];
  /** US dollars of AI usage per calendar month the agent may use; 0 is no cap. */
  widget_monthly_cap_usd?: number;
  /** Ask for an email before handing off when the visitor has none. Default true. */
  widget_collect_email?: boolean;
};
// Asking a teammate's Ollie (contracts/ai.md "Asking a teammate"): the caller's own sharing.
export type PeerMode = "verbatim" | "summary" | "none";
export type PeerResult = "answered" | "declined" | "not_shared" | "failed";
export type PeerPerson = { id: string; name: string; email: string; avatar_url?: string | null };
export type PeerAccount = { id: string; email: string; name: string; provider: "google" | "microsoft" | "imap"; mode: PeerMode; chosen: boolean };
/** GET /v1/me/assistant-peer: the caller's own settings in this workspace. */
export type PeerSettings = {
  /** The workspace allows it (an admin's switch, AI, Ollie and Collab on). */
  available: boolean;
  enabled: boolean;
  share_with: "everyone" | "chosen";
  audience: { users: PeerPerson[]; teams: { id: string; name: string }[] };
  /** Every own mail account, new ones at default_mode. */
  all_accounts: boolean;
  default_mode: PeerMode;
  /** The caller's own Collab accounts; mode is the effective one, chosen marks an explicit choice. */
  accounts: PeerAccount[];
  /** Up to 2,000 characters, added to the instructions of the caller's answering Ollie. */
  rules: string;
  updated_at: string | null;
};
/** PUT /v1/me/assistant-peer (a full replace). */
export type PeerSettingsInput = {
  enabled: boolean;
  share_with: "everyone" | "chosen";
  audience: { user_ids: string[]; team_ids: string[] };
  all_accounts: boolean;
  default_mode: PeerMode;
  accounts: { id: string; mode: PeerMode }[];
  rules: string;
};
/** One ask made to the caller (GET /v1/me/assistant-peer/asks), kept 90 days. */
export type PeerAsk = { id: string; asker: PeerPerson; question: string; answer: string; result: PeerResult; sources: number; created_at: string };

/** A model the instance offers (GET /v1/ai/models). */
export type AIModel = {
  id: string;
  name: string;
  vendor: string;
  context_length: number | null;
  /** US dollars per million tokens as the workspace pays them; null when unknown (or on its own key). */
  usd_per_mtok: { input: number; output: number } | null;
  /** What a typical draft reply (4,000 input, 500 output tokens) costs, in US dollars; null when unknown. */
  typical_reply_usd: number | null;
};
export type AIModels = { data: AIModel[]; default: string; fast: string };
export type AIConfig = {
  enabled: boolean;
  provider: string;
  models: { default: string; fast: string };
  embeddings: boolean;
  features: AISettings;
  /** What a typical call of each feature costs on the workspace's chosen models, in US dollars. */
  costs: Partial<Record<AIFeature, number>>;
  /** Each call is charged on its real cost (model, thread and answer length). */
  metered?: boolean;
  /** The name AI acts under everywhere ("Ollie"). */
  assistant_name?: string;
  /** The decision engine (Jev by TypeSafe, the LLM fallback, or none). */
  decisions?: DecisionEngine;
  /** LLM calls run on the workspace's own provider key and cost $0 (GET /v1/ai/provider). */
  own_key?: boolean;
  /** Decisions run on the workspace's own TypeSafe or provider key and cost $0. */
  decisions_own_key?: boolean;
};

// The workspace's AI provider (contracts/ai.md "Provider per workspace").
export type AIKeyKind = "openrouter" | "openai" | "anthropic";
export type AIKeyStatus = "ok" | "rejected" | "no_funds";
/** A saved key, never the key itself. */
export type AIProviderKey = {
  kind: AIKeyKind;
  /** Host of an OpenAI-compatible endpoint, else "". */
  base_url_host: string;
  last4: string;
  model: string;
  fast_model: string;
  /** What the provider last said about the key. */
  status: AIKeyStatus;
  status_at: string | null;
  created_by: UserRef | null;
  created_at: string;
  updated_at: string;
};
export type AIProvider = {
  mode: "instance" | "own_key";
  instance_provider: string;
  /** Calls on the instance's provider are charged to the AI balance, in US dollars. */
  billing: boolean;
  key: AIProviderKey | null;
  /** Where decisions run; missing on older servers. */
  decisions?: AIDecisionsRoute;
};
/** Where the workspace's decisions (Jev) run, and its own TypeSafe key. */
export type AIDecisionsRoute = {
  runs_on: "instance" | "provider_key" | "typesafe_key" | "none";
  engine: "jev" | "llm" | "none";
  key: AIDecisionsKey | null;
};
/** A saved TypeSafe key, never the key itself. */
export type AIDecisionsKey = {
  last4: string;
  status: AIKeyStatus;
  status_at: string | null;
  created_by: UserRef | null;
  created_at: string;
  updated_at: string;
};
export type AIProviderKeyInput = { kind: AIKeyKind; api_key: string; base_url?: string; model?: string; fast_model?: string };
export type AIProviderTestReason = "invalid_key" | "no_funds" | "unreachable" | "busy" | "model_unavailable";
export type AIProviderTest = { ok: boolean; reason?: AIProviderTestReason; message?: string; models: string[] };

// Decisions (contracts/ai.md "Decisions"), stored on tickets as custom.ai_decisions and custom.ai_suggestions.
export type DecisionField =
  | "priority" | "severity" | "category" | "type_label" | "label" | "sentiment" | "language" | "spam" | "needs_human" | "team" | "sla_risk";
export type Decision = {
  value: string | boolean | number | null;
  probability: number | null;
  confidence: number | null;
  applied: boolean;
};
export type AIDecisions = {
  engine: "jev" | "llm";
  model: string;
  model_version?: string;
  threshold: number;
  decided_at: string;
  fields: Partial<Record<DecisionField, Decision>>;
};
export type SuggestionField = "priority" | "severity" | "labels" | "team" | "spam";
export type Suggestion = {
  /** labels: names; team: a team id. */
  value: string | string[] | boolean;
  confidence: number | null;
  probability: number | null;
  model: string;
  decided_at: string;
};
export type AISuggestions = Partial<Record<SuggestionField, Suggestion>>;
export type KBSource = { article_id: string; title: string; url: string };
export type AIDraft = { body_html: string; body_text: string; sources: KBSource[] };
export type AISummary = { summary: string; key_points: string[]; sentiment: Sentiment; updated_at: string };
export type AIAnswer = { answer_html: string; sources: KBSource[] };
export type Triage = {
  tags: string[];
  /** At most 2 new tag names; only the explicit apply creates them. */
  suggested_tags: string[];
  priority: Priority;
  severity: Severity | null;
  team_id: string | null;
  sentiment: Sentiment;
  language: string;
  category: string;
  summary: string;
  applied: boolean;
  decisions?: AIDecisions;
  suggestions?: AISuggestions;
};
export type RewriteMode = "friendlier" | "more_formal" | "shorter" | "longer" | "fix_grammar" | "simplify";
export type TranslateResult = { html?: string; text?: string; source_language: string };
export type KBArticleDraft = { title: string; body_html: string; excerpt: string };

// The widget AI agent (contracts/agent.md).

/** A passage the agent answered from. */
export type AgentSource = { kind: "article" | "page" | "file" | "snippet"; id: string; title: string; url: string | null };

export type AgentReport = {
  from: string;
  to: string;
  conversations: number;
  replies: number;
  resolved: number;
  handoffs: number;
  ratings: { up: number; down: number };
  /** US dollars. */
  usd: number;
  by_day: { date: string; conversations: number; replies: number; resolved: number; handoffs: number }[];
  tools: { tool: string; calls: number; errors: number }[];
  gaps: { question: string; count: number }[];
  /** The totals split by the ticket's channel. */
  channels?: { chat: AgentChannelReport; email: AgentChannelReport };
};
export type AgentChannelReport = {
  conversations: number;
  replies: number;
  resolved: number;
  handoffs: number;
  /** Email only: suggested replies written (draft mode), and how many were sent as written. */
  suggested: number;
  suggested_sent: number;
};

/** A ticket's shared draft (contracts/api.md "Drafts"): one pending reply body, staged for whoever opens the ticket next. */
export type TicketDraft = {
  ticket_id: string;
  body_html: string;
  body_text: string;
  /** Who staged it: a teammate, or an API client (an AI agent over MCP) by its key's name. */
  by: { type: "user" | "api"; name: string; user?: UserRef };
  updated_at: string;
};

/** Ollie's suggested reply on an email ticket (contracts/agent.md "Email"). */
export type SuggestedReply = {
  id: string;
  ticket_id: string;
  /** The customer message it answers. */
  message_id: string;
  body_html: string;
  sources: AgentSource[];
  tools: string[];
  status: "pending" | "sent" | "discarded" | "superseded";
  reply_id: string | null;
  created_at: string;
};

export type PlaygroundMessage = { role: "visitor" | "agent"; text: string };
export type PlaygroundVisitor = { name?: string; email?: string; verified?: boolean; user_id?: string };
export type PlaygroundSettings = Partial<Pick<AISettings, "widget_model" | "widget_blocked_topics" | "tone" | "reply_language" | "instructions">> & {
  /** Answer as this bot; default the default bot. */
  bot_id?: string;
  /** Unsaved edits of that bot. */
  bot?: BotDraft;
};
export type PlaygroundBody = { messages: PlaygroundMessage[]; settings?: PlaygroundSettings; visitor?: PlaygroundVisitor };
export type PlaygroundToolEvent = { name: string; status: "running" | "done" | "error"; summary: string };
export type PlaygroundDone = {
  text_html: string;
  sources: AgentSource[];
  handoff: boolean;
  action: { tool: string; title: string; summary: string } | null;
  /** US dollars. */
  usd: number;
};
export type PlaygroundEvent =
  | { event: "delta"; data: { text: string } }
  | { event: "tool"; data: PlaygroundToolEvent }
  | { event: "sources"; data: { sources: AgentSource[] } }
  | { event: "done"; data: PlaygroundDone };

/** contracts/agent.md "Tests". */
export type AgentExpectationKind = "mentions" | "not_mentions" | "handoff" | "no_handoff" | "uses_tool" | "cites_knowledge";
export type AgentExpectation = { kind: AgentExpectationKind; value?: string };
export type AgentTestStatus = "pending" | "running" | "passed" | "failed" | "error";
export type AgentTestCheck = { kind: AgentExpectationKind; value: string; passed: boolean; reason: string };
export type AgentTestResult = {
  run_id: string;
  test_id: string;
  status: AgentTestStatus;
  answer_html: string;
  handoff: boolean;
  tools: string[];
  sources: AgentSource[];
  action: PlaygroundDone["action"];
  checks: AgentTestCheck[];
  error: string | null;
  /** US dollars. */
  usd: number;
  draft: boolean;
  previous_status: "passed" | "failed" | "error" | null;
  finished_at: string | null;
};
export type AgentTest = {
  id: string;
  name: string;
  messages: PlaygroundMessage[];
  expectations: AgentExpectation[];
  visitor: PlaygroundVisitor | null;
  last_result: AgentTestResult | null;
  created_at: string;
  updated_at: string;
};
export type AgentTestBody = { name?: string; messages: PlaygroundMessage[]; expectations: AgentExpectation[]; visitor?: PlaygroundVisitor };
export type AgentTestRunStatus = "queued" | "running" | "done" | "failed" | "cancelled";
export type AgentTestRun = {
  id: string;
  status: AgentTestRunStatus;
  draft: boolean;
  test_ids: string[];
  total: number;
  passed: number;
  failed: number;
  errored: number;
  /** US dollars. */
  usd: number;
  error: string | null;
  results?: AgentTestResult[];
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
};
/** contracts/agent.md "Improve answer". */
export type ImproveAnswerBody = { question: string; answer: string; message_id?: string; save_test?: boolean };
export type ImproveAnswerResult = { source: { id: string; kind: "snippet"; name: string }; test: AgentTest | null };
/** contracts/agent.md "Review queue". */
export type AgentReviewItem = {
  id: string;
  ticket_id: string;
  ticket_number: number;
  reason: "rated_down" | "handoff";
  question: string;
  answer: string;
  contact_name: string;
  created_at: string;
};

export type McpAuthKind = "none" | "bearer" | "header" | "oauth";
export type McpTool = {
  name: string;
  title: string | null;
  description: string;
  input_schema: Record<string, unknown>;
  read_only: boolean;
  destructive: boolean;
  enabled: boolean;
  policy: "auto" | "confirm";
  verified_only: boolean;
};
export type McpServerStatus = "connected" | "needs_auth" | "error" | "connecting";
export type McpServer = {
  id: string;
  name: string;
  slug: string;
  url: string;
  enabled: boolean;
  auth: {
    kind: McpAuthKind;
    header_name?: string;
    has_secret: boolean;
    oauth?: { connected: boolean; scopes: string[]; expires_at: string | null };
  };
  status: McpServerStatus;
  error: string | null;
  server_info: { name: string; version: string } | null;
  tools: McpTool[];
  last_synced_at: string | null;
  created_at: string;
  updated_at: string;
};
/** Secrets are write-only: omit them to keep the stored one. */
export type McpAuthInput = { kind: McpAuthKind; token?: string; header_name?: string; header_value?: string };
export type McpToolPatch = Partial<Pick<McpTool, "enabled" | "policy" | "verified_only">>;
export type McpServerPatch = { name?: string; url?: string; enabled?: boolean; auth?: McpAuthInput; tools?: Record<string, McpToolPatch> };
export type McpToolResult = { content: string; is_error: boolean; ms: number };

export type KnowledgeKind = "website" | "file" | "snippet";
export type KnowledgeStatus = "pending" | "syncing" | "ready" | "error";
export type KnowledgeWebsite = { url: string; max_pages: number; include: string[]; exclude: string[]; recrawl: "off" | "weekly" };
export type KnowledgeSource = {
  id: string;
  kind: KnowledgeKind;
  name: string;
  status: KnowledgeStatus;
  error: string | null;
  enabled: boolean;
  website?: KnowledgeWebsite;
  file?: { filename: string; content_type: string; size_bytes: number };
  snippet?: { question: string; answer: string };
  doc_count: number;
  char_count: number;
  last_synced_at: string | null;
  created_at: string;
  updated_at: string;
};
export type KnowledgeDoc = { id: string; title: string; url: string | null; chars: number; updated_at: string };
export type KnowledgeCreate =
  | { kind: "website"; name?: string; website: Partial<KnowledgeWebsite> & { url: string } }
  | { kind: "snippet"; name?: string; snippet: { question: string; answer: string } };
export type KnowledgePatch = { name?: string; enabled?: boolean; website?: Partial<KnowledgeWebsite>; snippet?: { question: string; answer: string } };

/** "live" pushes on the ticket channel while the widget agent writes (contracts/realtime.md). */
export type LiveEvent =
  | { type: "ai.reply.started"; data: { reply_id: string; ticket_id: string; bot: { id?: string | null; name: string; look?: BotLook; avatar_url?: string } } }
  | { type: "ai.reply.delta"; data: { reply_id: string; ticket_id: string; text: string } }
  | { type: "ai.reply.tool"; data: { reply_id: string; ticket_id: string; tool: string; status: "running" | "done" | "error" } }
  | { type: "ai.reply.ended"; data: { reply_id: string; ticket_id: string; message_id: string | null; handoff: boolean; action_id: string | null } };

// ─── Bots (contracts/bots.md) ──────────────────────────────────────────────

export type BotTone = "friendly" | "professional" | "playful" | "concise" | "warm" | "custom";
export type BotReplyLength = "short" | "medium" | "detailed";
export type BotEmoji = "none" | "some" | "lots";
export type BotPersonality = {
  tone: BotTone;
  /** Up to 200 chars; used when tone is custom. */
  custom_tone: string;
  /** Up to 4,000 chars: what this bot should know and do. */
  instructions: string;
  reply_length: BotReplyLength;
  emoji: BotEmoji;
  /** Up to 300 chars: its opening message in a new chat; "" is the widget's greeting. */
  greeting: string;
  /** Up to 300 chars: what it says when it passes the conversation to a person. */
  handoff_message: string;
  /** At most 20 topics of 2..100 chars it declines. */
  blocked_topics: string[];
  /** Up to 200 chars: how its emails end; "" is its name and the company's. */
  sign_off: string;
};
export type BotUsage = {
  widgets: { id: string; name: string; brand_id: string }[];
  mailboxes: { id: string; name: string; email: string }[];
};
export type Bot = {
  id: string;
  name: string;
  role: string;
  /** BCP 47 tag it always writes in; "" is the customer's language. */
  language: string;
  look: BotLook;
  /** The uploaded picture (look.kind "image"), else null. */
  image_url: string | null;
  /** A PNG of the look for clients that cannot render it. */
  avatar_url: string;
  personality: BotPersonality;
  is_default: boolean;
  position: number;
  usage: BotUsage;
  created_at: string;
  updated_at: string;
};
/** What a message or a live reply carries of its bot. */
export type BotRef = { id: string; name: string; avatar_url: string; look: BotLook };
/** WidgetConfig.bot. */
export type WidgetBot = { id: string | null; name: string; role: string; avatar_url: string; look: BotLook; greeting: string };
export type BotBody = { name: string; role?: string; language?: string; look?: BotLook; personality?: Partial<BotPersonality> };
export type BotDraft = { name?: string; role?: string; language?: string; personality?: Partial<BotPersonality> };

// History imports (contracts/imports.md).
export type ImportSource = "zendesk" | "helpscout" | "freshdesk" | "intercom" | "front" | "csv";
export type ImportStatus = "running" | "paused" | "completed" | "failed" | "canceled" | "undoing" | "undone";
export type ImportCounts = {
  tickets: number;
  updated: number;
  messages: number;
  contacts: number;
  companies: number;
  articles: number;
  attachments: number;
  skipped: number;
  failed: number;
  threaded: number;
  thread_total: number;
};
export type ImportTotals = { tickets: number | null; articles: number | null };
export type ImportProblem = { at: string; item: string; message: string };
export type ImportOptions = {
  tickets: boolean;
  articles: boolean;
  attachments: boolean;
  /** Only tickets created on or after this day (YYYY-MM-DD). */
  since: string | null;
  mailbox_id: string | null;
  /** Label on every imported ticket; "" for none. */
  tag: string;
  statuses: "keep" | "closed";
  articles_as_drafts: boolean;
};
export type CsvField =
  | "ticket_id"
  | "subject"
  | "body"
  | "from_email"
  | "from_name"
  | "author"
  | "status"
  | "priority"
  | "tags"
  | "created_at"
  | "assignee_email"
  | "team"
  | "company"
  | "message_id"
  | "in_reply_to"
  | "references";
/** Each field's column name; a missing field is not imported. */
export type CsvMapping = Partial<Record<CsvField, string>>;
export type ImportMapping = { agents: Record<string, string | null>; groups: Record<string, string | null>; csv?: CsvMapping };
export type Import = {
  id: string;
  source: ImportSource;
  account: { name: string; url: string };
  status: ImportStatus;
  phase: "articles" | "tickets" | "threading" | "done";
  run: number;
  options: ImportOptions;
  mapping: ImportMapping;
  totals: ImportTotals;
  counts: ImportCounts;
  problems: ImportProblem[];
  last_error: string;
  needs_credentials: boolean;
  waiting_until: string | null;
  can_sync: boolean;
  started_by: UserRef | null;
  started_at: string;
  finished_at: string | null;
  created_at: string;
  updated_at: string;
};
export type ImportSampleMessage = { author: string; agent: boolean; note: boolean; text: string; created_at: string; attachments: number };
export type ImportSample = {
  ref: string;
  subject: string;
  status: TicketStatus;
  created_at: string;
  requester: { name: string; email: string } | null;
  messages: ImportSampleMessage[];
  message_count: number;
  tags: string[];
};
export type ImportCheck = {
  account: { name: string; url: string };
  totals: ImportTotals;
  agents: { id: string; name: string; email: string; user_id: string | null }[];
  groups: { id: string; name: string; team_id: string | null }[];
  samples: ImportSample[];
  has_articles: boolean;
};
export type CsvUpload = {
  upload_id: string;
  filename: string;
  rows: number;
  columns: { name: string; samples: string[] }[];
  suggested: CsvMapping;
  preview: ImportSample[];
};
export type ImportCheckBody = { source: ImportSource; credentials?: Record<string, string>; upload_id?: string; mapping?: CsvMapping };
export type ImportCreateBody = {
  source: ImportSource;
  credentials?: Record<string, string>;
  upload_id?: string;
  options?: ImportOptions;
  mapping?: ImportMapping;
};

// Templates and email design (contracts/templates.md).

export type TemplateGroup = "contact" | "company" | "ticket" | "agent" | "team" | "brand" | "mailbox" | "workspace" | "date";
export type TemplateVariable = { key: string; group: TemplateGroup; label: string; description: string; example: string; html: boolean; list?: boolean };
export type TemplateFilter = { name: string; group: "text" | "list" | "number" | "date"; args: string; description: string; example: string };
export type TemplateTag = { name: string; syntax: string; description: string };
export type TemplateSlotKey = "content" | "signature" | "history" | "survey";
export type TemplateSlot = { key: TemplateSlotKey; label: string; description: string; required_in: TemplateKind[] | null };
export type TemplateKind = "reply" | "signature" | "personal_signature" | "auto_reply" | "layout" | "csat" | "subject" | "collab_signature";
/** kind_groups: the variable groups of the kinds that take only some (a Collab signature). */
export type TemplateCatalog = { variables: TemplateVariable[]; slots: TemplateSlot[]; filters: TemplateFilter[]; tags?: TemplateTag[]; kind_groups?: Partial<Record<TemplateKind, TemplateGroup[]>> };
export type TemplateProblem = { message: string; token: string };

export type EmailLayoutOptions = {
  mode: "simple" | "custom";
  style: "plain" | "branded";
  logo: boolean;
  /** "#rrggbb", or "" for the brand colour. */
  accent: string;
  font: "system" | "sans" | "serif";
  footer_html: string;
  history: "none" | "last" | "all";
  /** "" is the default "On {{ message.date }}, {{ message.author }} wrote:". */
  history_header: string;
};
export type EmailTemplate = {
  brand_id: string;
  kind: "layout" | "csat";
  subject: string;
  body_html: string;
  options: EmailLayoutOptions;
  /** The brand has not changed it; the fields carry the default. */
  is_default: boolean;
  updated_at: string | null;
};
export type EmailTemplates = { layout: EmailTemplate; csat: EmailTemplate };

export type PreviewKind = "reply" | "signature" | "personal_signature" | "auto_reply" | "layout" | "csat";
export type TemplatePreviewBody = {
  kind: PreviewKind;
  body_html?: string;
  subject?: string;
  options?: EmailLayoutOptions;
  brand_id?: string;
  team_id?: string;
  mailbox_id?: string;
  /** One of mailbox_id's aliases: preview mail written to it. */
  alias?: string;
  ticket_id?: string;
};
export type TemplatePreview = { subject: string; html: string; text: string; problems: TemplateProblem[] };
export type TicketVariables = { values: Record<string, string>; signature_html: string };

// ─── Collab: shared and private mail accounts (contracts/collab.md) ─────────

export type MailAudience = { everyone: boolean; team_ids: string[]; user_ids: string[] };
export type MailAddress = { name: string; email: string };
export type MailProvider = "google" | "microsoft" | "imap";
export type MailTls = "tls" | "starttls";
export type MailAccountStatus = "syncing" | "active" | "error" | "paused";
/** A tone key: gray, red, orange, amber, green, teal, blue, indigo, violet, pink. */
export type MailColor = "gray" | "red" | "orange" | "amber" | "green" | "teal" | "blue" | "indigo" | "violet" | "pink";

export type MailImapSettings = {
  imap_host: string;
  imap_port: number;
  imap_tls: MailTls;
  imap_username: string;
  smtp_host: string;
  smtp_port: number;
  smtp_tls: MailTls;
  smtp_username: string;
};

export type MailAccount = {
  id: string;
  provider: MailProvider;
  email: string;
  /** How the sidebar names it; the address by default. */
  name: string;
  /** The From name of its mail. */
  display_name: string;
  color: string;
  /** Who connected it; null after they left (shared accounts only). */
  owner: UserRef | null;
  visibility: "private" | "shared";
  /** Empty when private. */
  shared_with: MailAudience;
  /** Sanitized; added to new mail and replies by the composer. */
  signature_html: string;
  /** paused: Collab is off for the workspace. */
  status: MailAccountStatus;
  /** The grant or password was refused: connect it again. */
  reconnect: boolean;
  last_error: string;
  last_synced_at: string | null;
  can_manage: boolean;
  /** Unread conversations in its inbox. */
  unread: number;
  /** Shared accounts can track work separately from provider read state. */
  handling_enabled: boolean;
  /** Reply answers everyone on the message by default, for everyone answering from this account. */
  reply_all: boolean;
  /** IMAP accounts, can_manage only. */
  imap?: MailImapSettings;
  created_at: string;
  /** How its own settings sync with the provider (contracts/collab.md "Account settings"). */
  signature_sync: MailSettingSync;
  signature_synced_at: string | null;
  display_name_sync: MailSettingSync;
  /** Null for IMAP, and while it was never read. */
  out_of_office: MailOutOfOffice | null;
  out_of_office_sync: MailSettingSync;
  /** The addresses it sends from, its own first (contracts/collab.md "Addresses"). */
  identities: MailIdentity[];
  /** The address new mail goes out from. */
  default_from: string;
  /** The latest history import (contracts/collab.md "History"); null when none was started. */
  history: MailHistory | null;
  /** The oldest mail stored from it: its mail is here from then on. */
  oldest_message_at: string | null;
  /** IMAP: folders past the 30 the sync reads; 0 otherwise. */
  folders_left_out: number;
  /** How its inbox is split into tabs (contracts/collab.md "Categories"). */
  categories: MailAccountCategories;
};

/** One tab of an inbox (contracts/collab.md "Categories"). "primary" is always the first. */
export type MailCategory = { key: string; name: string; description: string; color: MailColor };

export type MailCategorySet = { enabled: boolean; categories: MailCategory[]; updated_at: string; updated_by: UserRef | null };

/** GET /v1/collab/categories. */
export type MailCategorySettings = {
  /** The workspace's default; null when there is none. locked: members who are not admins cannot choose their own. */
  workspace: (MailCategorySet & { locked: boolean }) | null;
  /** Admins: every team with its own; others: their own teams'. The first in position order the owner is in decides. */
  teams: (MailCategorySet & { team_id: string; team_name: string; position: number })[];
  presets: MailCategory[];
  /** The fixed categories available when syncing a Google account. */
  gmail_categories: MailCategory[];
  /** ai: Ollie sorts new mail; rules: sender rules and headers only. */
  sorting: "ai" | "rules";
  /** Ollie can suggest categories. */
  suggest: boolean;
};

export type MailAccountCategories = {
  enabled: boolean;
  /** What its inbox shows; [] when not split. */
  categories: MailCategory[];
  source: "account" | "team" | "workspace" | "none";
  team: { id: string; name: string } | null;
  /** The account's own choice. */
  mode: "follow" | "custom" | "off" | "gmail";
  /** The effective category authority after workspace policy. */
  engine: "off" | "local" | "gmail";
  sync_status?: "refreshing" | "pending" | "failed" | "ready";
  /** Its own categories, kept while it follows. */
  own: MailCategory[];
  /** Ollie may sort its new mail. */
  ai: boolean;
  /** The caller cannot change mode or categories: the workspace locks them. */
  locked: boolean;
};

/** A sender rule: mail from an address, or from "@domain", goes to a category. */
export type MailCategoryRule = { id: string; sender: string; category: string; created_at: string };

/** limited: at the workspace's Collab storage allowance (contracts/collab.md "Storage"). */
export type MailHistoryStatus = "running" | "paused" | "limited" | "done" | "canceled" | "error";

/** A history import: older mail brought into opendesk (contracts/collab.md "History"). */
/** The older mail a new account imports once connected: from since on, or all of it (null). */
export type ConnectHistory = { since: string | null };

export type MailHistory = {
  status: MailHistoryStatus;
  /** Mail received from then on; null = all mail. */
  since: string | null;
  include_spam_trash: boolean;
  requested_by: UserRef | null;
  started_at: string;
  /** Done, canceled or error. */
  finished_at: string | null;
  /** The oldest mail it has read. */
  reached: string | null;
  /** Messages it stored. */
  imported: number;
  /** Already in opendesk. */
  present: number;
  /** Over 30 MB, left at the provider. */
  too_large: number;
  /** Could not be parsed. */
  unreadable: number;
  /** Messages in scope, when the provider says. */
  total: number | null;
  /** Safe to show; "" when the last pass went well. */
  last_error: string;
};

/** The workspace's Collab storage (contracts/collab.md "Storage"). */
export type CollabStorage = {
  used_bytes: number;
  /** 0 = unlimited. */
  limit_bytes: number;
  /** At the allowance: history imports wait. */
  history_limited: boolean;
  /** Past twice the allowance: new mail waits at the provider. */
  full: boolean;
};

/**
 * An address an account sends from: its own (primary; its From name and signature are the
 * account's) or another one: Gmail's send-as addresses, aliases added by hand for Microsoft and IMAP.
 */
export type MailIdentity = {
  /** Null for the account's own address. */
  id: string | null;
  email: string;
  /** The From name; empty uses the account's. */
  display_name: string;
  signature_html: string;
  /** Replies go here when set. */
  reply_to: string;
  primary: boolean;
  /** New mail goes out from it. */
  default: boolean;
  source: "account" | "provider" | "manual";
  /** pending: Gmail is waiting for the address's owner to confirm it. */
  verification: "accepted" | "pending";
  can_send: boolean;
  signature_sync: MailIdentitySync;
  display_name_sync: MailIdentitySync;
  signature_synced_at: string | null;
  /** The From name has variables, so it is kept in opendesk and Gmail keeps its own. */
  display_name_local?: boolean;
  /** The signature has variables, so it is kept in opendesk and Gmail keeps its own. */
  signature_local?: boolean;
  /** The From name (its own, else the account's) filled for you. */
  from_name?: string;
  /** The signature filled for you; null when it has no variables. */
  filled_signature_html?: string | null;
};

/** An address's From name and signature as typed, filled for you (POST .../identities/preview). */
export type MailIdentityPreview = { from_name: string; from: string; signature_html: string; from_name_problem: string; problems: TemplateProblem[] };
export type MailIdentityPreviewBody = { email?: string; display_name: string; signature_html?: string };

/** two_way: read from the provider and changed there too; read_only: read only; none: opendesk only (or not available). */
export type MailSettingSync = "two_way" | "read_only" | "none";

/**
 * How an address's From name and signature sync: as an account's setting, or pull (Gmail's
 * other send-as addresses): read when it changes at the provider, a change here kept in opendesk.
 */
export type MailIdentitySync = MailSettingSync | "pull";

/** An account's automatic reply as its provider keeps it. */
export type MailOutOfOffice = {
  enabled: boolean;
  /** Replies only to mail received from then on. */
  start: string | null;
  end: string | null;
  /** Always "" for Microsoft: Outlook's replies have no subject of their own. */
  subject: string;
  message_html: string;
  /** Only people in the account's contacts. */
  contacts_only: boolean;
  /** Only people in the account's own organization. */
  internal_only: boolean;
};

export type MailFolderRole = "inbox" | "sent" | "drafts" | "archive" | "trash" | "spam" | "starred" | "important" | "all";

export type MailFolder = {
  id: string;
  account_id: string;
  /** The last part of the path. */
  name: string;
  /** "Clients/Acme". */
  path: string;
  role: MailFolderRole | null;
  /** Gmail's own labels are labels; everything else is a folder. */
  kind: "folder" | "label";
  /** Unread conversations. */
  unread: number;
  /** Conversations. */
  total: number;
  /** Gmail label colour, a hex background. */
  color: string | null;
  /** User folders and labels; never the special ones. */
  can_rename: boolean;
};

export type MailThread = {
  id: string;
  account_id: string;
  subject: string;
  snippet: string;
  /** Senders and recipients, newest first, at most 10, the account's own address last. */
  participants: MailAddress[];
  last_message_at: string;
  message_count: number;
  unread: boolean;
  starred: boolean;
  has_attachments: boolean;
  /** A shared inbox conversation that still needs a person to handle it. */
  needs_action: boolean;
  folder_ids: string[];
  /** Shared on its own beyond its account's audience. */
  shared: boolean;
  assignees: UserRef[];
  comment_count: number;
  /** The account's address other than its own the conversation was written to or sent from (an alias); null when none. */
  to_address?: string | null;
  /** Its category key (contracts/collab.md "Categories"); null when its account's inbox is not split. */
  category?: string | null;
};

export type MailAttachment = {
  /** Its index in the message, "0", "1", ... */
  id: string;
  filename: string;
  content_type: string;
  size: number;
  inline: boolean;
  content_id: string;
  /** Signed, valid for an hour, works in <img> and as a download link. */
  url: string;
};

export type MailMessage = {
  id: string;
  thread_id: string;
  account_id: string;
  /** The RFC 5322 Message-ID, without angle brackets. */
  message_id: string;
  from: MailAddress;
  to: MailAddress[];
  cc: MailAddress[];
  /** Only on mail sent from the account. */
  bcc: MailAddress[];
  reply_to: MailAddress[];
  subject: string;
  snippet: string;
  /** Sanitized; inline images point at their attachment URL. */
  body_html: string;
  body_text: string;
  sent_at: string;
  unread: boolean;
  starred: boolean;
  draft: boolean;
  folder_ids: string[];
  attachments: MailAttachment[];
  /** The teammate who sent it from opendesk (for a draft, who saved it). */
  sent_by: UserRef | null;
  /** Drafts: the id the /v1/collab/drafts routes take. */
  draft_id?: string | null;
  /** Drafts an AI key saved and no person has saved since: the key's name, shown "AI draft by" (contracts/collab.md "AI drafts"). */
  drafted_by_ai?: string | null;
  /** The message of the conversation it answers (its In-Reply-To), when stored. */
  in_reply_to_id?: string | null;
  /** The account's address (its own or an alias) it was sent from or written to; null when none. */
  own_address?: string | null;
  /** Mail the account wrote: sent from opendesk, a draft, or in its Sent or Drafts at the provider. A From naming the account does not make mail outgoing. */
  outgoing: boolean;
};

/** How a message came, read from its source (GET /v1/collab/messages/{id}/details). */
export type MailMessageDetails = {
  /** The envelope sender's domain. */
  mailed_by: string;
  /** Domains whose DKIM signature the receiving server verified. */
  signed_by: string[];
  /** The domain it came through, when that is not the sender's own. */
  via: string;
  /** The receiving server's results ("pass", "fail", "softfail", "none", ...); empty when it reported none. */
  spf: string;
  dkim: string;
  dmarc: string;
  /** How it reached the receiving server; empty when unknown. */
  encryption: "tls" | "none" | "";
  /** spoofed: DMARC failed; unverified: neither SPF nor DKIM passed. */
  warning: "spoofed" | "unverified" | "";
  headers: { name: string; value: string }[];
};

export type MailComment = {
  id: string;
  thread_id: string;
  author: UserRef | null;
  /** Sanitized like a note. */
  body_html: string;
  /** User ids. */
  mentions: string[];
  created_at: string;
  updated_at: string;
};

export type MailThreadAccount = {
  id: string;
  email: string;
  name: string;
  display_name: string;
  color: string;
  provider: string;
  /** The account's reply default; older servers omit it. */
  reply_all?: boolean;
  signature_html: string;
  /** The addresses it sends from, its own first. */
  identities?: MailIdentity[];
  default_from?: string;
};

export type MailThreadDetail = {
  thread: MailThread;
  account: MailThreadAccount;
  /** Oldest first. */
  messages: MailMessage[];
  /** Oldest first. */
  comments: MailComment[];
  /** The conversation's own shares. */
  shares: MailAudience;
  can_act: boolean;
  /** The ticket it answered or was answered from, when you see it (contracts/collab.md "Answering tickets"). */
  ticket?: { id: string; number: number; subject: string } | null;
};

/** One address across the conversations you see (contracts/collab.md "People"). */
export type MailPerson = {
  email: string;
  /** The newest name the address has on them; "" none. */
  name: string;
  /** Conversations with them, trash and spam left out, the open one included. */
  conversations: number;
  /** Them without `exclude`. */
  others: number;
  /** Their oldest message in the mail opendesk has synced, not the first ever. */
  first_at: string | null;
  last_at: string | null;
  /** The newest of them, without `exclude`. */
  threads: MailThread[];
  /** Files from messages they sent, newest first. */
  files: MailPersonFile[];
};

export type MailPersonFile = { thread_id: string; message_id: string; sent_at: string; attachment: MailAttachment };

export type CollabStatus = {
  enabled: boolean;
  /** Why not: not on the plan, or off on this instance. */
  reason: "plan" | "disabled" | null;
  providers: { google: boolean; microsoft: boolean; imap: boolean };
  /** Accounts the caller sees. */
  accounts: number;
  storage: CollabStorage;
};

export type SidebarSectionId = "favorites" | "mail" | "inbox" | "teams" | "views" | "workspace";
export type SidebarPinKind = "box" | "folder" | "account" | "view" | "team" | "ticket_folder" | "view_section";
export type SidebarPin = { kind: SidebarPinKind; id: string };

export type SidebarPrefs = {
  /** Section ids, top to bottom. Unknown ids are kept by the server. */
  order: string[];
  /** "section:<id>", "box:<box>", "account:<id>", "folder:<id>", "folder:<inbox|starred|...>", "view:<id>", "team:<id>". */
  hidden: string[];
  /** Shown under Favorites, in order, at most 30. */
  pinned: SidebarPin[];
  /** Account ids whose folders are folded. */
  expanded_accounts: string[];
  /** Default true. */
  mark_read_on_open: boolean;
  /** Default true: "All inboxes" on top of the Mail section. */
  unified_inbox: boolean;
  /**
   * The number beside a row ("box:inbox", "account:<id>", "folder:<id>", "ticket_folder:<id>",
   * "view:<id>", "team:<id>") or a section's default ("section:mail"). Default {}.
   */
  badges: Record<string, SidebarBadge>;
  /** The mail page's folder bar, in order: box names and "folder:<id>". */
  mail_tabs: string[];
  /** Pins the sidebar added once by itself ("box:inbox": All inboxes, at a second account), so one unpinned stays so. */
  auto_pins: string[];
};

/** unread / total / none for mail rows; count / awaiting / urgent / overdue / none for ticket rows, and open for team rows. */
export type SidebarBadge = "unread" | "total" | "count" | "awaiting" | "urgent" | "overdue" | "open" | "none";

export type MailBox = "inbox" | "unread" | "starred" | "sent" | "drafts" | "archive" | "trash" | "spam" | "all" | "shared" | "assigned" | "needs_action";

export type MailThreadAction =
  | "read"
  | "unread"
  | "star"
  | "unstar"
  | "archive"
  | "inbox"
  | "trash"
  | "spam"
  | "not_spam"
  | "move"
  | "categorize"
  | "label"
  | "unlabel"
  | "done"
  | "needs_action";

export type MailCounts = {
  /** Unread conversations; assigned and shared: open ones. */
  boxes: { inbox: number; unread: number; shared: number; assigned: number; drafts: number; needs_action: number };
  accounts: Record<string, number>;
  folders: Record<string, number>;
  /** By account with aliases, then address (its own included): unread conversations in its inbox with it on a message; missing = 0. */
  addresses?: Record<string, Record<string, number>>;
  /** boxes.assigned by account; only accounts with some. */
  assigned: Record<string, number>;
  /** By split account, then category key (primary included): unread conversations in its inbox; missing = 0. */
  categories?: Record<string, Record<string, number>>;
};

/** `collab.changed` event data. */
export type CollabChangedEvent = { account_id: string | null; thread_ids: string[]; folders: boolean };

// Several workspaces (contracts/api.md "Several workspaces").
export type WorkspacePrefs = {
  /** Workspace ids in switcher order; the rest follow in joining order. */
  order: string[];
  /** Workspaces folded away in the switcher and left out of the bell and the Everywhere lists. */
  hidden: string[];
  updated_at?: string;
};
export type WorkspacePrefsInput = Partial<Pick<WorkspacePrefs, "order" | "hidden">>;
export type WorkspaceOverview = {
  id: string;
  name: string;
  slug: string;
  avatar_url: string | null;
  role: Role;
  suspended: boolean;
  unread_notifications: number;
  /** GET /v1/tickets/counts there; null while suspended. */
  ticket_counts: TicketCounts | null;
  /** GET /v1/collab/counts there; null without Collab. */
  mail_counts: MailCounts | null;
};
// ─── Spaces (contracts/spaces.md) ───────────────────────────────────────────

/** The colours a Space takes. */
export type SpaceColor = "grey" | "blue" | "violet" | "teal" | "green" | "amber" | "red" | "pink";

/** What a Space row names: a whole built-in section, or one row of a workspace. */
export type SpaceItemKind = "section" | "ticket_folder" | "team" | "view" | "box" | "account" | "folder" | "page" | "everywhere";

export type SpaceItem = {
  /** A workspace the user is a member of; "" for an everywhere row (it spans them all). */
  workspace_id: string;
  kind: SpaceItemKind;
  /** section: favorites | inbox | mail | teams | views | workspace; ticket_folder: a folder id; box: inbox | shared | assigned; page: contacts | knowledge | reports; everywhere: mine | mail; else a uuid. */
  id: string;
  /** The person's own label for the row, 1..64 characters. */
  name?: string;
  /** The number beside the row; left out: the row's default. */
  badge?: SidebarBadge;
};

export type SpaceSection = {
  /** 1..40 of [A-Za-z0-9_-], once per Space. */
  id: string;
  /** 0..40 characters; "" shows no header. */
  title: string;
  collapsed: boolean;
  /** At most 60; at most 300 across the Space. */
  items: SpaceItem[];
};

export type Space = {
  /** A uuid made by the client. */
  id: string;
  /** 1..40 characters. */
  name: string;
  /** "" (the name's first letter) or one of SPACE_ICONS. */
  icon: string;
  color: SpaceColor;
  /** The workspace's mark beside each row while the Space shows several workspaces. */
  marks: boolean;
  /** At most 20. */
  sections: SpaceSection[];
};

/** GET and PUT /v1/me/spaces. `saved` false: nothing saved yet (no Spaces). */
export type SpacesResponse = { spaces: Space[]; saved: boolean };
