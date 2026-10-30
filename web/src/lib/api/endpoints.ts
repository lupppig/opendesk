// Typed wrappers for every endpoint in contracts/api.md.

import { http, stream, streamEvents, type StreamHandlers } from "./client";
import type * as T from "./types";
import { creatingTicket } from "../ownTickets";
import { uiLanguage } from "@/i18n";

type ListQuery = { limit?: number; cursor?: string | null };

export type TicketListQuery = ListQuery & {
  status?: T.TicketStatus[];
  priority?: T.Priority[];
  /** "none": tickets without a severity. */
  severity?: (T.Severity | "none")[];
  assignee?: string;
  /** Team ids, "mine" and "none", comma separated: tickets of any of them. */
  team_id?: string;
  /** The same values as team_id: tickets of none of them. */
  team_not?: string;
  mailbox_id?: string;
  /** Mailbox ids, comma separated: tickets whose reply address is none of them. */
  mailbox_not?: string;
  /** The address a ticket was first written to: a mailbox's address or an alias. */
  received_at?: string;
  brand_id?: string;
  widget_id?: string;
  /** Label names, comma separated: tickets carrying any of them. */
  tag?: string;
  /** Label names, comma separated: tickets carrying none of them. */
  tag_not?: string;
  /** Tickets carrying no label. */
  untagged?: boolean;
  channel?: T.Channel;
  channel_not?: T.Channel[];
  contact_id?: string;
  /** Contact ids, comma separated: tickets whose contact is none of them. */
  contact_not?: string;
  /** Company ids, comma separated: tickets whose contact belongs to none of them. */
  company_not?: string;
  view_id?: string;
  q?: string;
  spam?: boolean | "any";
  /** `field.<key>` sorts by a text, number, date, checkbox or select custom field. */
  sort?: "last_message_at" | "created_at" | "priority" | "severity" | "due" | `field.${string}`;
  order?: "asc" | "desc";
  /** Custom field filters, sent as `field.<key>[<op>]=<value>`. */
  fields?: T.FieldFilter[];
};

/** The changelog list's filters (contracts/api.md "Changelog"). */
export type ChangelogListQuery = ListQuery & {
  /** published: live now; scheduled: published with a future date. */
  status?: "draft" | "published" | "scheduled";
  category?: string;
  q?: string;
  /** A brand's own entries, or "none" for those of every brand. */
  brand_id?: string;
  /** What that brand's help center shows: its own entries and those of every brand. */
  for_brand_id?: string;
};

/** A changelog write. On PATCH, `published_at: null` dates it when it is next published and `brand_id: null` is every brand. */
export type ChangelogEntryBody = {
  title: string;
  slug?: string;
  body_html?: string;
  excerpt?: string;
  categories?: string[];
  status?: T.ChangelogEntry["status"];
  published_at?: string | null;
  brand_id?: string | null;
};

export type TicketBulkQuery = Omit<TicketListQuery, "limit" | "cursor" | "sort" | "order">;
/** A list's filters, as GET /v1/tickets/summary counts them. */
export type TicketSummaryQuery = TicketBulkQuery;

/** Flattens custom field filters into `field.<key>[<op>]` query parameters. */
export function fieldQuery(fields?: T.FieldFilter[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const f of fields ?? []) {
    // set/not_set ignore the value, but an empty value would be dropped from the URL.
    out[`field.${f.key}[${f.op}]`] = f.op === "set" || f.op === "not_set" ? "1" : f.value;
  }
  return out;
}

function listQuery<Q extends { fields?: T.FieldFilter[] }>(q: Q) {
  const { fields, ...rest } = q;
  return { ...(rest as Omit<Q, "fields">), ...fieldQuery(fields) } as Record<string, string | number | boolean | string[] | null | undefined>;
}

function bulkQuery(q: TicketBulkQuery) {
	return { ...q, spam: q.spam === true ? "true" : q.spam === false ? "false" : q.spam };
}

export type ContactListQuery = ListQuery & { q?: string; company_id?: string; tag?: string; fields?: T.FieldFilter[] };
export type CompanyListQuery = ListQuery & { q?: string; tag?: string; fields?: T.FieldFilter[] };

/** Labels and custom fields on contact and company writes. */
type LabelWrites = { tags?: string[]; add_tags?: string[]; remove_tags?: string[]; fields?: T.FieldValuesPatch };

export type CreateTicketBody = {
  subject: string;
  contact_id?: string;
  contact?: { email: string; name: string };
  channel?: "email" | "api";
  mailbox_id?: string;
  body_html: string;
  kind?: "reply" | "note";
  priority?: T.Priority;
  severity?: T.Severity;
  assignee_id?: string;
  team_id?: string;
  tags?: string[];
  fields?: T.FieldValues;
  status?: T.TicketStatus;
  /** An email reply's copies. */
  cc?: string[];
  bcc?: string[];
  /** Files on the first message (POST /v1/attachments). */
  attachment_ids?: string[];
};

export type SendMessageBody = {
  kind: "reply" | "note";
  body_html: string;
  cc?: string[];
  bcc?: string[];
  attachment_ids?: string[];
  status?: T.TicketStatus;
  /** Send from another address; it becomes the ticket's reply address. */
  mailbox_id?: string;
  /** Replies: the agent's own note this reply is written from; it stays as a marker pointing at the reply. */
  from_note_id?: string;
  /** Email replies: send from one of your own Collab addresses instead; the ticket's reply address stays. */
  from_account?: { account_id: string; email?: string; reply_to?: string };
};

/** Starts a third-party thread (contracts/api.md "Threads"). */
export type StartThreadBody = {
  to: string[];
  cc?: string[];
  subject: string;
  body_html: string;
  attachment_ids?: string[];
  mailbox_id?: string;
  /** Quote the customer's conversation (its last 10 public replies) under the first message. */
  include_history?: boolean;
  /** Attachments of the customer's conversation to send along. */
  forward_attachment_ids?: string[];
};

/** How a Google or Microsoft connection receives mail. */
export type MailboxReceive = "forwarding" | "sync" | "api";

export type CreateMailboxBody = {
  name: string;
  email: string;
  display_name?: string;
  inbound_kind: T.Mailbox["inbound_kind"];
  outbound_kind: T.Mailbox["outbound_kind"];
  config: T.MailboxConfig;
  password?: string;
  smtp_password?: string;
  team_id?: string | null;
  brand_id?: string;
  aliases?: string[];
  /** An alias to the team its new tickets go to. */
  alias_teams?: Record<string, string>;
  from_name_format?: string;
  /** Sign in to the servers first; a refusal is 422 mailbox_connection_failed and nothing is saved. */
  verify?: boolean;
};

export type TeamBody = Partial<
  Pick<T.Team, "name" | "description" | "color" | "private" | "members" | "member_ids" | "signature_html" | "business_hours" | "assign_mode">
>;
export type BrandBody = Partial<Pick<T.Brand, "name" | "slug" | "color" | "locale">> & {
  help_center?: Partial<Pick<T.Brand["help_center"], "enabled" | "title">>;
};
export type WidgetBody = { brand_id?: string; name?: string; team_id?: string | null; bot_id?: string | null; settings?: Partial<T.WidgetSettings> };
export type RoutingRuleBody = Partial<Pick<T.RoutingRule, "name" | "enabled" | "match" | "conditions" | "team_id" | "skill_ids">>;
export type RoutingTestBody = {
  channel: "email" | "chat";
  to?: string;
  from?: string;
  subject?: string;
  body?: string;
  widget_id?: string;
  topic?: string;
  page_url?: string;
};

const pub = { auth: false, workspace: false } as const;

/** One `file` field, as every picture upload expects. */
function imageForm(file: File | Blob): FormData {
  const fd = new FormData();
  fd.append("file", file);
  return fd;
}

/** Upload, remove and refresh routes for a contact or company picture. */
function pictureRoutes<R>(base: string) {
  return {
    uploadAvatar: (id: string, file: File | Blob) => http.put<R>(`${base}/${id}/avatar`, imageForm(file)),
    removeAvatar: (id: string) => http.del<R>(`${base}/${id}/avatar`),
    /** Looks the automatic sources up again now (202). */
    refreshAvatar: (id: string) => http.post<void>(`${base}/${id}/avatar/refresh`),
  };
}

export const api = {
  auth: {
    config: () => http.get<T.AuthConfig>("/v1/auth/config", pub),
    register: (b: { email: string; password: string; name: string; turnstile_token?: string; invite_token?: string }) =>
      http.post<T.TokenResponse>("/v1/auth/register", b, pub),
    login: (b: { email: string; password: string; turnstile_token?: string }) =>
      http.post<T.LoginResponse>("/v1/auth/login", b, pub),
    mfa: (b: { mfa_token: string; code: string }) => http.post<T.TokenResponse>("/v1/auth/mfa", b, pub),
    /** Sign out of one account in this browser (the active one by default), or of every account. */
    logout: (b: { user_id?: string; all?: boolean } = {}) => http.post<void>("/v1/auth/logout", b, { workspace: false }),
    /** The accounts this browser is signed in to. Rotates nothing. */
    accounts: () => http.post<{ accounts: T.Account[] }>("/v1/auth/accounts", undefined, pub),
    forgot: (email: string, turnstile_token?: string) =>
      http.post<void>("/v1/auth/password/forgot", { email, turnstile_token }, pub),
    reset: (token: string, password: string) => http.post<void>("/v1/auth/password/reset", { token, password }, pub),
    verifyEmail: (token: string) => http.post<void>("/v1/auth/email/verify", { token }, { workspace: false }),
    resendVerification: () => http.post<void>("/v1/auth/email/resend", undefined, { workspace: false }),
    /** `code_verifier`: the PKCE verifier of a native (desktop app) flow. */
    oauthExchange: (code: string, code_verifier?: string) =>
      http.post<T.ExchangeResponse>("/v1/auth/oauth/exchange", code_verifier ? { code, code_verifier } : { code }, pub),
    oauthLink: (link_token: string, password: string) =>
      http.post<T.LoginResponse>("/v1/auth/oauth/link", { link_token, password }, pub),
    totpSetup: () => http.post<{ secret: string; otpauth_url: string }>("/v1/auth/totp/setup", undefined, { workspace: false }),
    totpEnable: (code: string) =>
      http.post<{ recovery_codes: string[] }>("/v1/auth/totp/enable", { code }, { workspace: false }),
    totpDisable: (code: string) => http.post<void>("/v1/auth/totp/disable", { code }, { workspace: false }),
  },
  me: {
    get: () => http.get<T.Me>("/v1/me", { workspace: false }),
    /** `avatar_url` is an https picture to copy (processed in the background), or "" to remove it. */
    update: (b: { name?: string; avatar_url?: string; time_format?: T.TimeFormat }) => http.patch<T.User>("/v1/me", b, { workspace: false }),
    /** PNG, JPEG, GIF or WebP, at most 5 MB. */
    uploadAvatar: (file: File | Blob) => http.put<T.User>("/v1/me/avatar", imageForm(file), { workspace: false }),
    removeAvatar: () => http.del<T.User>("/v1/me/avatar", undefined, { workspace: false }),
    /** Subscribes this browser to web pushes for the current session (contracts/native.md "Web Push"). */
    webPush: (s: PushSubscriptionJSON) => http.put<void>("/v1/me/web-push", s, { workspace: false }),
    /** Marks you as seen, for auto-away, in the active workspace (notification emails wait while you are active there). */
    heartbeat: () => http.post<void>("/v1/me/heartbeat"),
    password: (current_password: string, new_password: string) =>
      http.post<void>("/v1/me/password", { current_password, new_password }, { workspace: false }),
    sessions: () => http.get<T.Session[] | T.Page<T.Session>>("/v1/me/sessions", { workspace: false }),
    revokeSession: (id: string) => http.del(`/v1/me/sessions/${id}`, undefined, { workspace: false }),
    delete: (b: { password?: string; code?: string }) => http.del("/v1/me", b, { workspace: false }),
    /** How this person arranges their workspaces (contracts/api.md "Several workspaces"). */
    workspacePrefs: () => http.get<T.WorkspacePrefs>("/v1/me/workspace-prefs", { workspace: false }),
    putWorkspacePrefs: (b: T.WorkspacePrefsInput) => http.put<T.WorkspacePrefs>("/v1/me/workspace-prefs", b, { workspace: false }),
    /** Every workspace's sidebar numbers, each as that workspace answers this person. */
    overview: () => http.get<{ workspaces: T.WorkspaceOverview[] }>("/v1/me/overview", { workspace: false }),
    /** The caller's Spaces (contracts/spaces.md): one list for every workspace and device. */
    spaces: {
      get: () => http.get<T.SpacesResponse>("/v1/me/spaces", { workspace: false }),
      /** Replaces the whole list. */
      put: (spaces: T.Space[]) => http.put<T.SpacesResponse>("/v1/me/spaces", { spaces }, { workspace: false }),
    },
  },
  /** The workspace's starter Space: members read it, admins publish and remove it. */
  starterSpace: {
    get: () => http.get<{ space: T.Space | null }>("/v1/sidebar/starter"),
    put: (space: T.Space) => http.put<{ space: T.Space }>("/v1/sidebar/starter", space),
    remove: () => http.del("/v1/sidebar/starter"),
  },
  workspaces: {
    create: (name: string) => http.create<T.Workspace>("/v1/workspaces", { name }, { workspace: false }),
    current: () => http.get<T.Workspace>("/v1/workspace"),
    update: (b: { name?: string; settings?: T.WorkspaceSettings }) => http.patch<T.Workspace>("/v1/workspace", b),
    delete: (confirm_name: string) => http.del("/v1/workspace", { confirm_name }),
    transfer: (user_id: string) => http.post<void>("/v1/workspace/transfer", { user_id }),
    rotateWidgetSecret: () => http.post<{ secret: string }>("/v1/workspace/widget-secret/rotate"),
    /** `logo` is the workspace avatar_url; the others fill `images`. Admin. */
    uploadImage: (slot: "logo" | T.WorkspaceImageSlot, file: File | Blob) =>
      http.put<T.Workspace>(`/v1/workspace/images/${slot}`, imageForm(file)),
    removeImage: (slot: "logo" | T.WorkspaceImageSlot) => http.del<T.Workspace>(`/v1/workspace/images/${slot}`),
  },
  members: {
    list: (q?: ListQuery) => http.get<T.Page<T.Member>>("/v1/members", { query: q }),
    update: (userId: string, role: T.Role) => http.patch<T.Member>(`/v1/members/${userId}`, { role }),
    remove: (userId: string) => http.del(`/v1/members/${userId}`),
    /** `userId` may be "me". Members set their own availability; admins anyone's, and the cap. */
    setAvailability: (userId: string, b: { availability?: T.Availability; max_open_tickets?: number | null }) =>
      http.patch<T.Member>(`/v1/members/${userId}/availability`, b),
    /** Admin: which messages an agent may delete; "default" follows the workspace's rule. */
    setMessageDelete: (userId: string, message_delete: "default" | "off" | "own" | "all") =>
      http.patch<T.Member>(`/v1/members/${userId}/permissions`, { message_delete }),
    /** `userId` may be "me". Members edit their own profile; admins anyone's. */
    updateProfile: (userId: string, b: Partial<T.MemberProfile>) => http.patch<T.Member>(`/v1/members/${userId}/profile`, b),
  },
  /** Templates and email design (contracts/templates.md). */
  templates: {
    variables: () => http.get<T.TemplateCatalog>("/v1/templates/variables"),
    preview: (b: T.TemplatePreviewBody) => http.post<T.TemplatePreview>("/v1/templates/preview", b),
    test: (b: T.TemplatePreviewBody) => http.post<T.TemplatePreview>("/v1/templates/test", b),
  },
  emailTemplates: {
    get: (brandId: string) => http.get<T.EmailTemplates>(`/v1/email-templates/${brandId}`),
    save: (brandId: string, kind: "layout" | "csat", b: { subject?: string; body_html?: string; options?: T.EmailLayoutOptions }) =>
      http.put<T.EmailTemplate>(`/v1/email-templates/${brandId}/${kind}`, b),
    reset: (brandId: string, kind: "layout" | "csat") => http.del(`/v1/email-templates/${brandId}/${kind}`),
  },
  assignment: {
    get: () => http.get<T.Assignment>("/v1/assignment"),
    /** `rules` replaces the whole list. Admin. */
    update: (b: Partial<T.Assignment>) => http.patch<T.Assignment>("/v1/assignment", b),
  },
  triage: {
    get: () => http.get<T.TriageSettings>("/v1/triage"),
    update: (b: Partial<Omit<T.TriageSettings, "available">>) => http.patch<T.TriageSettings>("/v1/triage", b),
  },
  businessHours: {
    get: () => http.get<T.BusinessHours>("/v1/business-hours"),
    /** The full object; `timezone` sets the workspace timezone. Admin. */
    put: (b: T.BusinessHours) => http.put<T.BusinessHours>("/v1/business-hours", b),
  },
  onboarding: {
    get: () => http.get<T.Onboarding>("/v1/onboarding"),
    dismiss: () => http.post<void>("/v1/onboarding/dismiss"),
  },
  invitations: {
    list: () => http.get<T.Page<T.Invitation>>("/v1/invitations"),
    create: (b: { email: string; role: T.Role }) => http.create<T.Invitation>("/v1/invitations", b),
    revoke: (id: string) => http.del(`/v1/invitations/${id}`),
    link: (id: string) => http.post<{ url: string; expires_at: string }>(`/v1/invitations/${id}/link`),
    resend: (id: string) => http.post<T.Invitation>(`/v1/invitations/${id}/resend`),
    accept: (token: string) => http.post<T.Membership>("/v1/invitations/accept", { token }, { workspace: false }),
    lookup: (token: string) =>
      http.post<{ workspace_name: string; email: string; role: T.Role }>("/v1/invitations/lookup", { token }, pub),
  },
  teams: {
    ...crud<T.Team, TeamBody>("/v1/teams"),
    /** Moves its open tickets, addresses, widgets and routing rules to another team. */
    removeTo: (id: string, moveTo?: string) => http.del(`/v1/teams/${id}`, undefined, { query: { move_to: moveTo } }),
    /** An admin joins a private team as an observer (audit-logged, the leads are told). */
    join: (id: string) => http.post<T.Team>(`/v1/teams/${id}/join`),
    leave: (id: string) => http.post<void>(`/v1/teams/${id}/leave`),
  },
  brands: {
    ...crud<T.Brand, BrandBody>("/v1/brands"),
    makeDefault: (id: string) => http.post<T.Brand>(`/v1/brands/${id}/default`),
    uploadImage: (id: string, slot: "logo" | "help_center_logo" | "help_center_favicon", file: File | Blob) =>
      http.put<T.Brand>(`/v1/brands/${id}/images/${slot}`, imageForm(file)),
    removeImage: (id: string, slot: "logo" | "help_center_logo" | "help_center_favicon") =>
      http.del<T.Brand>(`/v1/brands/${id}/images/${slot}`),
    /** Sets the help center's custom domain and checks it right away (it can take a few seconds). */
    setHelpDomain: (id: string, hostname: string) => http.put<T.Brand>(`/v1/brands/${id}/help-domain`, { hostname }),
    /** Checks the custom domain now; 6 a minute per brand. */
    checkHelpDomain: (id: string) => http.post<T.Brand>(`/v1/brands/${id}/help-domain/check`),
    removeHelpDomain: (id: string) => http.del(`/v1/brands/${id}/help-domain`),
  },
  widgets: {
    ...crud<T.Widget, WidgetBody>("/v1/widgets"),
    /** The first secret comes back once, with the new widget, when the caller passes the owner-level gate (secret_available). */
    create: (b: WidgetBody & { brand_id: string; name: string }) => http.create<T.Widget & { secret?: string; secret_available: boolean }>("/v1/widgets", b),
    rotateSecret: (id: string) => http.post<{ secret: string }>(`/v1/widgets/${id}/secret/rotate`),
    uploadImage: (id: string, slot: "avatar" | "launcher", file: File | Blob) =>
      http.put<T.Widget>(`/v1/widgets/${id}/images/${slot}`, imageForm(file)),
    removeImage: (id: string, slot: "avatar" | "launcher") => http.del<T.Widget>(`/v1/widgets/${id}/images/${slot}`),
  },
  /** The AI agent's customer-facing bots (contracts/bots.md). */
  bots: {
    list: () => http.get<T.Page<T.Bot>>("/v1/bots"),
    get: (id: string) => http.get<T.Bot>(`/v1/bots/${id}`),
    create: (b: T.BotBody) => http.create<T.Bot>("/v1/bots", b),
    update: (id: string, b: Partial<T.BotBody>) => http.patch<T.Bot>(`/v1/bots/${id}`, b),
    remove: (id: string) => http.del(`/v1/bots/${id}`),
    duplicate: (id: string) => http.post<T.Bot>(`/v1/bots/${id}/duplicate`),
    makeDefault: (id: string) => http.post<T.Bot>(`/v1/bots/${id}/default`),
    reorder: (ids: string[]) => http.post<T.Page<T.Bot>>("/v1/bots/reorder", { ids }),
    uploadImage: (id: string, file: File | Blob) => http.put<T.Bot>(`/v1/bots/${id}/image`, imageForm(file)),
    removeImage: (id: string) => http.del<T.Bot>(`/v1/bots/${id}/image`),
  },
  skills: {
    list: () => http.get<T.Page<T.Skill>>("/v1/skills"),
    create: (b: { name: string }) => http.create<T.Skill>("/v1/skills", b),
    update: (id: string, b: Partial<Pick<T.Skill, "name" | "archived">>) => http.patch<T.Skill>(`/v1/skills/${id}`, b),
    replaceMembers: (id: string, member_ids: string[]) => http.put<T.Skill>(`/v1/skills/${id}/members`, { member_ids }),
  },
  routingRules: {
    ...crud<T.RoutingRule, RoutingRuleBody>("/v1/routing-rules"),
    reorder: (ids: string[]) => http.post<void>("/v1/routing-rules/reorder", { ids }),
    /** Where a message like this would go, without creating anything. */
    test: (b: RoutingTestBody) => http.post<T.RoutingTest>("/v1/routing/test", b),
  },
  apiKeys: {
    ...crud<T.ApiKey, { name: string; scopes: string[]; expires_at?: string | null; private_team_ids?: string[] }>("/v1/api-keys"),
    create: (b: { name: string; scopes: string[]; expires_at?: string | null; private_team_ids?: string[] }) =>
      http.create<T.ApiKey & { secret: string }>("/v1/api-keys", b),
  },
  /** Settings > AI clients: opendesk's MCP server and the clients connected with OAuth. */
  aiClients: {
    server: () => http.get<T.AIClientServer>("/v1/ai-clients/server"),
    connections: () => http.get<{ data: T.AIClientConnection[] }>("/v1/ai-clients/connections"),
    revoke: (id: string) => http.del(`/v1/ai-clients/connections/${id}`),
  },
  /** The MCP OAuth consent page. Signed-in user, no workspace header. */
  oauth: {
    request: (id: string) => http.get<T.OAuthRequest>(`/v1/oauth/requests/${id}`, { workspace: false }),
    approve: (id: string, workspaceId: string) =>
      http.post<T.OAuthDecision>(`/v1/oauth/requests/${id}/approve`, { workspace_id: workspaceId }, { workspace: false }),
    deny: (id: string) => http.post<T.OAuthDecision>(`/v1/oauth/requests/${id}/deny`, undefined, { workspace: false }),
  },
  webhooks: {
    ...crud<T.Webhook, { url: string; description: string; events: string[]; headers?: Record<string, string>; enabled: boolean; private_team_ids?: string[] }>("/v1/webhooks"),
    create: (b: { url: string; description: string; events: string[]; headers?: Record<string, string>; enabled: boolean; private_team_ids?: string[] }) =>
      http.create<T.Webhook & { secret: string }>("/v1/webhooks", b),
    deliveries: (id: string, q?: ListQuery) =>
      http.get<T.Page<T.WebhookDelivery>>(`/v1/webhooks/${id}/deliveries`, { query: q }),
    test: (id: string) => http.post<T.WebhookDelivery>(`/v1/webhooks/${id}/test`),
    /** With a code: verifies the address with the code it received. Without: sends a new challenge. */
    verify: (id: string, code?: string) => http.post<T.Webhook>(`/v1/webhooks/${id}/verify`, code ? { code } : {}),
  },
  integrations: {
    catalog: () => http.get<{ data: T.IntegrationProvider[] }>("/v1/integrations/catalog"),
    list: () => http.get<{ data: T.Integration[] }>("/v1/integrations"),
    /** OAuth answers `authorize_url`, to open in this browser (the call sets its cookie). */
    create: (b: { provider: string; name?: string; url?: string; auth?: "oauth" | "token" | "none"; token?: string }) =>
      http.create<{ integration: T.Integration; authorize_url?: string }>("/v1/integrations", b),
    authorize: (id: string) => http.post<{ integration: T.Integration; authorize_url: string }>(`/v1/integrations/${id}/authorize`),
    sync: (id: string) => http.post<T.Integration>(`/v1/integrations/${id}/sync`),
    update: (id: string, b: { name?: string; enabled?: boolean; disabled_tools?: string[]; ask_tools?: string[]; auto_tools?: string[]; token?: string }) =>
      http.patch<T.Integration>(`/v1/integrations/${id}`, b),
    remove: (id: string) => http.del(`/v1/integrations/${id}`),
  },
  phone: {
    providers: () => http.get<{ data: T.PhoneProvider[] }>("/v1/phone/providers"),
    connections: () => http.get<{ data: T.PhoneConnection[] }>("/v1/phone/connections"),
    connect: (b: { provider: string; name?: string; credentials: Record<string, string> }) => http.create<T.PhoneConnection>("/v1/phone/connections", b),
    update: (id: string, b: { name?: string; reuse_days?: number; assign_answered?: boolean; insight_cards?: boolean; credentials?: Record<string, string> }) =>
      http.patch<T.PhoneConnection>(`/v1/phone/connections/${id}`, b),
    test: (id: string) => http.post<T.PhoneConnection>(`/v1/phone/connections/${id}/test`),
    disconnect: (id: string) => http.del(`/v1/phone/connections/${id}`),
    lines: () => http.get<{ data: T.PhoneLine[] }>("/v1/phone/lines"),
  },
  ticketPanels: {
    list: () => http.get<{ data: T.TicketPanel[] }>("/v1/ticket-panels"),
    /** An http panel answers its signing `secret`, shown once. */
    create: (b: {
      kind: "shopify" | "http";
      name?: string;
      shop?: string;
      client_id?: string;
      client_secret?: string;
      token?: string;
      url?: string;
      fields?: T.ShopifyField[];
      orders_shown?: number;
      reference_pattern?: string;
    }) =>
      http.create<{ panel: T.TicketPanel; secret?: string }>("/v1/ticket-panels", b),
    update: (
      id: string,
      b: {
        name?: string;
        enabled?: boolean;
        position?: number;
        client_id?: string;
        client_secret?: string;
        token?: string;
        url?: string;
        fields?: T.ShopifyField[];
        orders_shown?: number;
        reference_pattern?: string;
      },
    ) =>
      http.patch<T.TicketPanel>(`/v1/ticket-panels/${id}`, b),
    rotateSecret: (id: string) => http.post<{ panel: T.TicketPanel; secret: string }>(`/v1/ticket-panels/${id}/rotate-secret`),
    remove: (id: string) => http.del(`/v1/ticket-panels/${id}`),
  },
  supportChannels: {
    providers: () => http.get<{ data: T.SupportChannelProvider[] }>("/v1/support-channels/providers"),
    accounts: (provider: string, credentials: Record<string, string>) =>
      http.post<{ data: T.SupportChannelAccount[] }>(`/v1/support-channels/providers/${provider}/accounts`, { credentials }),
    connections: () => http.get<{ data: T.SupportChannelConnection[] }>("/v1/support-channels/connections"),
    connect: (b: { provider: string; name?: string; credentials: Record<string, string> }) =>
      http.create<T.SupportChannelConnection>("/v1/support-channels/connections", b),
    rotate: (id: string, b: { name?: string; credentials: Record<string, string> }) =>
      http.patch<T.SupportChannelConnection>(`/v1/support-channels/connections/${id}`, b),
    test: (id: string) => http.post<T.SupportChannelConnection>(`/v1/support-channels/connections/${id}/test`),
    disconnect: (id: string) => http.del(`/v1/support-channels/connections/${id}`),
    availableDestinations: (id: string) =>
      http.get<{ data: T.SupportChannelAvailableDestination[] }>(`/v1/support-channels/connections/${id}/destinations`),
    destinations: () => http.get<{ data: T.SupportChannelDestination[] }>("/v1/support-channels/destinations"),
    deliveries: (q?: ListQuery) => http.get<T.Page<T.SupportChannelDelivery>>("/v1/support-channels/deliveries", { query: q }),
    operations: () => http.get<T.SupportChannelOperations>("/v1/support-channels/operations"),
    retryDelivery: (eventSeq: number) => http.post<{ event_seq: number; status: "queued" }>(`/v1/support-channels/deliveries/${eventSeq}/retry`),
    addDestination: (
      b: { connection_id: string; team_id?: string; priority?: "low" | "normal" | "high" | "urgent"; external_channel_id: string; external_channel_name?: string; message_limit?: number } & T.SupportChannelRouting,
    ) => http.create<T.SupportChannelDestination>("/v1/support-channels/destinations", b),
    updateDestination: (id: string, b: T.SupportChannelOllieUpdate & T.SupportChannelRouting) =>
      http.patch<T.SupportChannelDestination>(`/v1/support-channels/destinations/${id}`, b),
    removeDestination: (id: string) => http.del(`/v1/support-channels/destinations/${id}`),
    ollie: () => http.get<T.SupportChannelOllie>("/v1/support-channels/ollie"),
    setOllie: (b: T.SupportChannelOllie) => http.put<T.SupportChannelOllie>("/v1/support-channels/ollie", b),
    linkCode: () => http.create<{ code: string; expires_at: string }>("/v1/me/support-channel-link-code", {}),
  },
  imports: {
    list: () => http.get<{ data: T.Import[] }>("/v1/imports"),
    get: (id: string) => http.get<T.Import>(`/v1/imports/${id}`),
    /** Signs in to the source and answers what it has; nothing is stored. */
    check: (b: T.ImportCheckBody) => http.post<T.ImportCheck>("/v1/imports/check", b),
    uploadCsv: (file: File) => {
      const fd = new FormData();
      fd.append("file", file);
      return http.create<T.CsvUpload>("/v1/imports/csv", fd);
    },
    create: (b: T.ImportCreateBody) => http.create<T.Import>("/v1/imports", b),
    pause: (id: string) => http.post<T.Import>(`/v1/imports/${id}/pause`),
    resume: (id: string, credentials?: Record<string, string>) => http.post<T.Import>(`/v1/imports/${id}/resume`, credentials ? { credentials } : {}),
    cancel: (id: string) => http.post<T.Import>(`/v1/imports/${id}/cancel`),
    sync: (id: string) => http.post<T.Import>(`/v1/imports/${id}/sync`),
    undo: (id: string) => http.post<T.Import>(`/v1/imports/${id}/undo`),
    removeCredentials: (id: string) => http.del<T.Import>(`/v1/imports/${id}/credentials`),
  },
  auditLogs: (q?: ListQuery & { action?: string; entity_type?: string }) => http.get<T.Page<T.AuditLog>>("/v1/audit-logs", { query: q }),
  events: (since: number, limit = 500) => http.get<T.EventsPage>("/v1/events", { query: { since, limit } }),
  /** With allWorkspaces, the token also covers the person's other workspaces (contracts/tokens.md). */
  realtimeToken: (allWorkspaces = false) =>
    http.post<{ token: string; url: string; expires_at: string }>("/v1/realtime/token", undefined, { query: { all_workspaces: allWorkspaces || undefined } }),
  notifications: {
    list: (q?: ListQuery & { unread?: boolean }) => http.get<T.Page<T.Notification>>("/v1/notifications", { query: q }),
    read: (b: { ids?: string[]; all?: boolean }) => http.post<void>("/v1/notifications/read", b),
    /** The caller's preferences in the current workspace (contracts/api.md "Notification preferences and emails"). */
    preferences: () => http.get<T.NotificationPreferences>("/v1/me/notification-preferences"),
    updatePreferences: (b: T.NotificationPreferencesPatch) => http.patch<T.NotificationPreferences>("/v1/me/notification-preferences", b),
    /** Restores each type's email choice from before the unsubscribe link was used here. */
    resubscribe: () => http.post<T.NotificationPreferences>("/v1/me/notification-preferences/resubscribe"),
    /** Sends a sample notification email to the caller now. */
    testEmail: () => http.post<void>("/v1/me/notification-preferences/test"),
    /** Public: the token from a notification email turns off every notification email from its workspace. */
    unsubscribe: (token: string) =>
      http.post<{ workspace: { id: string; name: string }; email: string }>("/v1/notifications/unsubscribe", { token }, pub),
  },
  /** What teammates' Ollie may ask the caller's (contracts/ai.md "Asking a teammate"): the caller's own settings and log. */
  assistantPeer: {
    get: () => http.get<T.PeerSettings>("/v1/me/assistant-peer"),
    put: (b: T.PeerSettingsInput) => http.put<T.PeerSettings>("/v1/me/assistant-peer", b),
    asks: (q?: ListQuery) => http.get<T.Page<T.PeerAsk>>("/v1/me/assistant-peer/asks", { query: q }),
  },

  tickets: {
    list: (q: TicketListQuery) => http.get<T.Page<T.Ticket>>("/v1/tickets", { query: listQuery(q) }),
    counts: () => http.get<T.TicketCounts>("/v1/tickets/counts"),
    /** Counts the whole list for these filters, all pages together. */
    summary: (q: TicketSummaryQuery) => http.get<T.TicketListSummary>("/v1/tickets/summary", { query: listQuery(q) }),
    create: (b: CreateTicketBody) => creatingTicket(http.create<T.Ticket>("/v1/tickets", b)),
    get: (id: string) => http.get<T.Ticket>(`/v1/tickets/${encodeURIComponent(id)}`),
    update: (id: string, patch: T.TicketPatch) => http.patch<T.Ticket>(`/v1/tickets/${id}`, patch),
    delete: (id: string) => http.del(`/v1/tickets/${id}`),
    bulk: (b: ({ ids: string[] } | { query: TicketBulkQuery }) & { patch?: T.TicketPatch; delete?: true }) =>
      http.post<{ data: T.Ticket[]; updated: number; deleted: number } | undefined>("/v1/tickets/bulk", "query" in b ? { ...b, query: bulkQuery(b.query) } : b),
    merge: (id: string, into_id: string) => http.post<T.Ticket>(`/v1/tickets/${id}/merge`, { into_id }),
    messages: (id: string) => http.get<T.Page<T.Message>>(`/v1/tickets/${id}/messages`),
    /** `opts` pins the account and workspace a held reply started in, and lets it outlive the page. */
    send: (id: string, b: SendMessageBody, opts?: { keepalive?: boolean; as?: string; workspace?: string }) =>
      http.create<T.Message>(`/v1/tickets/${id}/messages`, b, opts),
    editMessage: (id: string, messageId: string, b: { body_html: string }) => http.patch<T.Message>(`/v1/tickets/${id}/messages/${messageId}`, b),
    deleteMessage: (id: string, messageId: string) => http.del(`/v1/tickets/${id}/messages/${messageId}`),
    /** Takes a chat reply back from the customer: it becomes an internal note. */
    retractMessage: (id: string, messageId: string) => http.post<T.Message>(`/v1/tickets/${id}/messages/${messageId}/retract`),
    /** Admin: brings a deleted message back. */
    restoreMessage: (id: string, messageId: string) => http.post<T.Message>(`/v1/tickets/${id}/messages/${messageId}/restore`),
    /** Admin: removes a deleted message's text, files and original email for good. */
    purgeMessage: (id: string, messageId: string) => http.post<T.Message>(`/v1/tickets/${id}/messages/${messageId}/purge`),
    activities: (id: string) => http.get<T.Page<T.Activity>>(`/v1/tickets/${id}/activities`),
    /** A teammate is writing a reply: the chat's visitor sees them typing. */
    typing: (id: string, opts?: { workspace?: string }) => http.post<void>(`/v1/tickets/${id}/typing`, undefined, opts),
    /** The ticket's shared draft (contracts/api.md "Drafts"): staged for the team, sent by whoever picks it up. */
    draft: {
      get: (id: string) => http.get<{ draft: T.TicketDraft | null }>(`/v1/tickets/${id}/draft`),
      discard: (id: string) => http.del(`/v1/tickets/${id}/draft`),
    },
    threads: (id: string) => http.get<{ data: T.Thread[] }>(`/v1/tickets/${id}/threads`),
    startThread: (id: string, b: StartThreadBody) => http.create<{ thread: T.Thread; message: T.Message }>(`/v1/tickets/${id}/threads`, b),
    sendInThread: (id: string, threadId: string, b: { body_html: string; attachment_ids?: string[] }) =>
      http.create<T.Message>(`/v1/tickets/${id}/threads/${threadId}/messages`, b),
    updateThread: (id: string, threadId: string, b: { status: T.Thread["status"] }) => http.patch<T.Thread>(`/v1/tickets/${id}/threads/${threadId}`, b),
    /** The ticket's template values with me as the agent, and the signature my reply from `mailbox_id` carries. */
    variables: (id: string, mailboxId?: string | null) =>
      http.get<T.TicketVariables>(`/v1/tickets/${id}/variables`, { query: mailboxId ? { mailbox_id: mailboxId } : undefined }),
    /** Fills a saved reply for this ticket; `thread` fills it for a side conversation, without the customer's or the ticket's details. */
    render: (id: string, body_html: string, thread?: boolean) =>
      http.post<{ body_html: string }>(`/v1/tickets/${id}/render`, { body_html, thread: thread || undefined }),
    links: (id: string) => http.get<{ data: T.TicketLink[] }>(`/v1/tickets/${id}/links`),
    /**
     * What a ticket panel's product knows about the ticket's contact; `refresh` asks it again. An
     * http panel's URL is told the app's language, so it can answer in it.
     */
    panel: (id: string, panelId: string, refresh = false) =>
      http.get<T.TicketPanelData>(`/v1/tickets/${id}/panels/${panelId}`, { query: { refresh: refresh || undefined, locale: uiLanguage() } }),
    addLink: (id: string, b: { url: string; title?: string; integration_id?: string }) => http.create<T.TicketLink>(`/v1/tickets/${id}/links`, b),
    removeLink: (id: string, linkId: string) => http.del(`/v1/tickets/${id}/links/${linkId}`),
  },
  messages: {
    original: (id: string) => http.get<{ full_html: string }>(`/v1/messages/${id}/original`),
  },
  contacts: {
    ...crud<T.Contact, Partial<Pick<T.Contact, "name" | "email" | "phone" | "notes" | "external_id" | "blocked" | "custom">> & { company_id?: string | null } & LabelWrites>("/v1/contacts"),
    search: (q: ContactListQuery) => http.get<T.Page<T.Contact>>("/v1/contacts", { query: listQuery(q) }),
    tickets: (id: string, q?: ListQuery) => http.get<T.Page<T.Ticket>>(`/v1/contacts/${id}/tickets`, { query: q }),
    ...pictureRoutes<T.Contact>("/v1/contacts"),
  },
  companies: {
    ...crud<T.Company, Partial<Pick<T.Company, "name" | "domain" | "notes" | "custom">> & LabelWrites>("/v1/companies"),
    search: (q: CompanyListQuery) => http.get<T.Page<T.Company>>("/v1/companies", { query: listQuery(q) }),
    ...pictureRoutes<T.Company>("/v1/companies"),
  },
  /** Labels (contracts/api.md "Labels and custom fields"). Writes need admin. */
  tags: {
    ...crud<T.Tag, T.TagInput>("/v1/tags"),
    list: (q?: { q?: string; group_id?: string | "none"; archived?: boolean | "any"; applies_to?: T.LabelScope }) =>
      http.get<T.Page<T.Tag>>("/v1/tags", { query: q }),
    merge: (id: string, into: string) => http.post<T.Tag>(`/v1/tags/${id}/merge`, { into }),
    reorder: (ids: string[]) => http.post<T.Page<T.Tag>>("/v1/tags/reorder", { ids }),
  },
  tagGroups: {
    ...crud<T.TagGroup, T.TagGroupInput>("/v1/tag-groups"),
    reorder: (ids: string[]) => http.post<T.Page<T.TagGroup>>("/v1/tag-groups/reorder", { ids }),
  },
  customFields: {
    list: (q?: { entity?: T.FieldEntity; archived?: boolean | "any" }) => http.get<T.Page<T.CustomField>>("/v1/custom-fields", { query: q }),
    get: (id: string) => http.get<T.CustomField>(`/v1/custom-fields/${id}`),
    create: (b: T.CustomFieldInput) => http.create<T.CustomField>("/v1/custom-fields", b),
    update: (id: string, b: T.CustomFieldPatch) => http.patch<T.CustomField>(`/v1/custom-fields/${id}`, b),
    remove: (id: string) => http.del(`/v1/custom-fields/${id}`),
    reorder: (entity: T.FieldEntity, ids: string[]) => http.post<T.Page<T.CustomField>>("/v1/custom-fields/reorder", { entity, ids }),
  },
  savedReplies: crud<T.SavedReply, { title: string; shortcut: string; body_html: string; team_id?: string | null }>("/v1/saved-replies"),
  views: crud<T.View, { name: string; filters: T.TicketFilters; shared: boolean; position?: number; team_id?: string | null; section?: string | null; icon?: T.ViewIcon | null }>("/v1/views"),
  slaPolicies: crud<T.SlaPolicy, Omit<T.SlaPolicy, "id" | "business_hours"> & { business_hours?: boolean }>("/v1/sla-policies"),
  automations: crud<T.Automation, Omit<T.Automation, "id" | "run_count">>("/v1/automations"),
  kb: {
    collections: crud<T.KbCollection, { name: string; slug?: string; description: string; position?: number; brand_id?: string | null }>("/v1/kb/collections"),
    articles: {
      ...crud<T.KbArticle, { title: string; slug?: string; collection_id: string | null; body_html: string; excerpt?: string; status: T.KbArticle["status"]; brand_id?: string | null }>("/v1/kb/articles"),
      /** `for_brand_id`: what that brand's help center shows, its own articles and those of every brand. */
      search: (q: ListQuery & { collection_id?: string; status?: T.KbArticle["status"]; q?: string; for_brand_id?: string }) =>
        http.get<T.Page<T.KbArticle>>("/v1/kb/articles", { query: q }),
    },
  },
  changelog: {
    entries: {
      ...crud<T.ChangelogEntry, ChangelogEntryBody>("/v1/changelog/entries"),
      search: (q: ChangelogListQuery) => http.get<T.Page<T.ChangelogEntry>>("/v1/changelog/entries", { query: q }),
    },
    categories: () => http.get<T.ChangelogCategory[]>("/v1/changelog/categories"),
  },
  wiki: {
    tree: () => http.get<T.Page<T.WikiNode>>("/v1/wiki/pages"),
    get: (id: string) => http.get<T.WikiPage>(`/v1/wiki/pages/${id}`),
    create: (b: { parent_id?: string | null; title?: string; icon?: string; body_html?: string; position?: number }) => http.create<T.WikiPage>("/v1/wiki/pages", b),
    /** `keepalive` lets a save started as the page closes finish (contracts/wiki.md "Editing together"). */
    update: (id: string, b: T.WikiPagePatch, opts?: { keepalive?: boolean }) => http.patch<T.WikiPage>(`/v1/wiki/pages/${id}`, b, opts),
    move: (id: string, b: { parent_id: string | null; position: number }) => http.post<T.WikiPage>(`/v1/wiki/pages/${id}/move`, b),
    remove: (id: string) => http.del(`/v1/wiki/pages/${id}`),
    revisions: (id: string) => http.get<T.Page<T.WikiRevision>>(`/v1/wiki/pages/${id}/revisions`),
    revision: (id: string, rid: string) => http.get<T.WikiRevision>(`/v1/wiki/pages/${id}/revisions/${rid}`),
    restore: (id: string, rid: string) => http.post<T.WikiPage>(`/v1/wiki/pages/${id}/revisions/${rid}/restore`, {}),
    search: (q: string, limit = 20) => http.get<T.Page<T.WikiSearchHit>>("/v1/wiki/search", { query: { q, limit } }),
  },
  search: (q: string) => http.get<T.SearchResults>("/v1/search", { query: { q } }),
  reports: {
    overview: (r: T.ReportsQuery) => http.get<T.ReportsOverview>("/v1/reports/overview", { query: r }),
    agents: (r: T.ReportsQuery) => http.get<T.ReportsAgents>("/v1/reports/agents", { query: r }),
  },
  attachments: {
    upload: (file: File) => {
      const fd = new FormData();
      fd.append("file", file);
      return http.create<T.Attachment>("/v1/attachments", fd);
    },
  },
  widgetSettings: {
    get: () => http.get<T.WidgetSettings>("/v1/widget/settings"),
    update: (b: Partial<T.WidgetSettings>) => http.patch<T.WidgetSettings>("/v1/widget/settings", b),
  },
  mailboxes: {
    ...crud<T.Mailbox, Partial<CreateMailboxBody> & Partial<Pick<T.Mailbox, "signature_html" | "auto_reply_enabled" | "auto_reply_subject" | "auto_reply_html" | "status" | "ai_answers" | "ai_answers_mode" | "ai_answers_outside_hours" | "trusted_form_senders" | "personal_replies">> & { bot_id?: string | null; alias_settings?: Record<string, T.AliasSettingsInput> }>("/v1/mailboxes"),
    create: (b: CreateMailboxBody) => http.create<T.Mailbox>("/v1/mailboxes", b),
    test: (id: string) => http.post<T.MailboxTest>(`/v1/mailboxes/${id}/test`),
    usage: (id: string) => http.get<T.MailboxUsage>(`/v1/mailboxes/${id}/usage`),
    /** Reads Gmail's send-as list again (422 send_as_unreadable for a send-only connection). */
    checkSendAs: (id: string) => http.post<T.Mailbox>(`/v1/mailboxes/${id}/send-as/check`),
    /**
     * receive: forwarding (the default) connects to send only; sync also receives over
     * IMAP, which is 422 imap_off on an instance with IMAP receiving off; api reads the
     * inbox directly, which is 422 api_sync_unavailable without Collab.
     */
    oauthStart: (provider: "google" | "microsoft", b: { mailbox_id?: string; name: string; email?: string; receive?: MailboxReceive; sync_start?: T.SyncStart }) =>
      http.post<{ url: string }>(`/v1/mailboxes/oauth/${provider}/start`, b),
    connectOptions: () => http.get<T.MailboxConnectOptions>("/v1/mailboxes/connect-options"),
    detect: (email: string) => http.post<T.MailDetection>("/v1/mailboxes/detect", { email }),
  },
  billing: {
    get: () => http.get<T.Billing>("/v1/billing"),
    checkout: (interval: "month" | "year", plan?: T.PaidPlan) => http.post<{ url: string }>("/v1/billing/checkout", plan ? { interval, plan } : { interval }),
    changePlan: (plan: T.PaidPlan) => http.post<T.Billing>("/v1/billing/change-plan", { plan }),
    portal: () => http.post<{ url: string }>("/v1/billing/portal"),
    /** Stripe Checkout for `usd` of AI usage. */
    topUpCheckout: (usd: number) => http.post<{ url: string }>("/v1/billing/top-up/checkout", { amount_usd: usd }),
    /** Buys `usd` of AI usage with the saved payment method, right away. */
    topUp: (usd: number) => http.post<T.TopUpResult>("/v1/billing/top-up", { amount_usd: usd }),
    invoices: () => http.get<{ data: T.BillingInvoice[] }>("/v1/billing/invoices"),
    autoRecharge: (patch: T.AutoRechargePatch) => http.patch<T.Billing>("/v1/billing/auto-recharge", patch),
    paymentMethod: () => http.post<{ url: string }>("/v1/billing/payment-method"),
    usage: (r: { from: string; to: string }) => http.get<T.BillingUsage>("/v1/billing/usage", { query: r }),
  },
  ai: {
    config: () => http.get<T.AIConfig>("/v1/ai/config"),
    updateSettings: (b: Partial<T.AISettings>) => http.patch<T.AISettings>("/v1/ai/settings", b),
    /** Streams `delta` text; resolves with the full draft at `done`. */
    draft: (ticketId: string, b: { instructions?: string; tone?: string }, h?: StreamHandlers) =>
      stream<T.AIDraft>(`/v1/tickets/${ticketId}/ai/draft`, b, h),
    summary: (ticketId: string) => http.post<T.AISummary>(`/v1/tickets/${ticketId}/ai/summary`, {}),
    ask: (ticketId: string, question: string, h?: StreamHandlers) => stream<T.AIAnswer>(`/v1/tickets/${ticketId}/ai/ask`, { question }, h),
    triage: (ticketId: string, apply: boolean) => http.post<T.Triage>(`/v1/tickets/${ticketId}/ai/triage`, { apply }),
    /** Ollie's pending suggested reply on an email ticket, sent or discarded by a teammate. */
    suggestedReply: {
      get: (ticketId: string) => http.get<{ suggested_reply: T.SuggestedReply | null }>(`/v1/tickets/${ticketId}/ai/suggested-reply`),
      send: (ticketId: string, id: string) => http.post<T.Message>(`/v1/tickets/${ticketId}/ai/suggested-reply/${id}/send`),
      discard: (ticketId: string, id: string) => http.del(`/v1/tickets/${ticketId}/ai/suggested-reply/${id}`),
    },
    /** Dismisses Ollie's suggestion for one field; accepting is setting the field. */
    dismissSuggestion: (ticketId: string, field: T.SuggestionField) => http.del(`/v1/tickets/${ticketId}/ai/suggestions/${field}`),
    rewrite: (b: { html: string; mode: T.RewriteMode; instructions?: string }, h?: StreamHandlers) => stream<{ html: string }>("/v1/ai/rewrite", b, h),
    translate: (b: { html?: string; text?: string; target: string }) => http.post<T.TranslateResult>("/v1/ai/translate", b),
    kbDraft: (b: { ticket_ids?: string[]; topic?: string }) => http.post<T.KBArticleDraft>("/v1/kb/articles/ai/draft", b),
    search: (q: string) => http.get<T.SearchResults>("/v1/ai/search", { query: { q } }),
    /** The models a workspace may choose from, with the instance defaults. */
    models: () => http.get<T.AIModels>("/v1/ai/models"),
    /** The workspace's provider: the instance's (charged to the AI balance) or its own key. */
    provider: {
      get: () => http.get<T.AIProvider>("/v1/ai/provider"),
      test: (b: T.AIProviderKeyInput) => http.post<T.AIProviderTest>("/v1/ai/provider/test", b),
      set: (b: T.AIProviderKeyInput) => http.put<T.AIProvider>("/v1/ai/provider", b),
      remove: () => http.del("/v1/ai/provider"),
      /** The workspace's own TypeSafe key, for decisions only. */
      decisions: {
        test: (b: { api_key: string }) => http.post<T.AIProviderTest>("/v1/ai/provider/decisions/test", b),
        set: (b: { api_key: string }) => http.put<T.AIProvider>("/v1/ai/provider/decisions", b),
        remove: () => http.del("/v1/ai/provider/decisions"),
      },
    },
    agent: {
      report: (r: T.ReportsRange) => http.get<T.AgentReport>("/v1/ai/agent/report", { query: r }),
      /** The setup checklist, computed from the workspace's data. Admin. */
      setup: () => http.get<T.AgentSetup>("/v1/ai/agent/setup"),
      /** Streams the agent's reply: `delta`, `tool`, `sources`, then `done`. An `error` event throws. */
      playground: (b: T.PlaygroundBody, onEvent: (e: T.PlaygroundEvent) => void, signal?: AbortSignal) =>
        streamEvents("/v1/ai/agent/playground", b, (event, data) => onEvent({ event, data } as T.PlaygroundEvent), signal),
      /** Saved test conversations (contracts/agent.md "Tests"). */
      tests: {
        list: () => http.get<{ data: T.AgentTest[] }>("/v1/ai/agent/tests"),
        create: (b: T.AgentTestBody) => http.create<T.AgentTest>("/v1/ai/agent/tests", b),
        update: (id: string, b: Partial<T.AgentTestBody>) => http.patch<T.AgentTest>(`/v1/ai/agent/tests/${id}`, b),
        remove: (id: string) => http.del(`/v1/ai/agent/tests/${id}`),
      },
      runs: {
        list: () => http.get<{ data: T.AgentTestRun[] }>("/v1/ai/agent/test-runs"),
        get: (id: string) => http.get<T.AgentTestRun>(`/v1/ai/agent/test-runs/${id}`),
        start: (b: { test_ids?: string[]; settings?: T.PlaygroundSettings }) => http.post<T.AgentTestRun>("/v1/ai/agent/test-runs", b),
        cancel: (id: string) => http.post<T.AgentTestRun>(`/v1/ai/agent/test-runs/${id}/cancel`),
      },
      improve: (b: T.ImproveAnswerBody) => http.create<T.ImproveAnswerResult>("/v1/ai/agent/improve", b),
      review: {
        list: () => http.get<{ data: T.AgentReviewItem[] }>("/v1/ai/agent/review"),
        dismiss: (id: string) => http.post<void>(`/v1/ai/agent/review/${id}/dismiss`),
      },
    },
    mcp: {
      list: () => http.get<T.Page<T.McpServer>>("/v1/ai/mcp-servers"),
      create: (b: { name: string; url: string; auth: T.McpAuthInput }) => http.create<T.McpServer>("/v1/ai/mcp-servers", b),
      update: (id: string, b: T.McpServerPatch) => http.patch<T.McpServer>(`/v1/ai/mcp-servers/${id}`, b),
      remove: (id: string) => http.del(`/v1/ai/mcp-servers/${id}`),
      sync: (id: string) => http.post<T.McpServer>(`/v1/ai/mcp-servers/${id}/sync`),
      /** The authorization URL to send the browser to. */
      oauthStart: (id: string) => http.post<{ url: string }>(`/v1/ai/mcp-servers/${id}/oauth/start`),
      testTool: (id: string, tool: string, b: { arguments: Record<string, unknown>; visitor?: T.PlaygroundVisitor }) =>
        http.post<T.McpToolResult>(`/v1/ai/mcp-servers/${id}/tools/${encodeURIComponent(tool)}/test`, b),
    },
    knowledge: {
      list: (q?: { kind?: T.KnowledgeKind }) => http.get<T.Page<T.KnowledgeSource>>("/v1/ai/knowledge", { query: q }),
      get: (id: string) => http.get<T.KnowledgeSource>(`/v1/ai/knowledge/${id}`),
      create: (b: T.KnowledgeCreate) => http.create<T.KnowledgeSource>("/v1/ai/knowledge", b),
      /** PDF, DOCX, TXT, Markdown, HTML or CSV, at most 20 MB. */
      upload: (file: File, name?: string) => {
        const fd = new FormData();
        fd.append("file", file);
        if (name) fd.append("name", name);
        return http.create<T.KnowledgeSource>("/v1/ai/knowledge/files", fd);
      },
      update: (id: string, b: T.KnowledgePatch) => http.patch<T.KnowledgeSource>(`/v1/ai/knowledge/${id}`, b),
      remove: (id: string) => http.del(`/v1/ai/knowledge/${id}`),
      sync: (id: string) => http.post<T.KnowledgeSource>(`/v1/ai/knowledge/${id}/sync`),
      docs: (id: string, q?: ListQuery) => http.get<T.Page<T.KnowledgeDoc>>(`/v1/ai/knowledge/${id}/docs`, { query: q }),
      removeDoc: (id: string, docId: string) => http.del(`/v1/ai/knowledge/${id}/docs/${docId}`),
    },
  },
  // Collab: mail accounts members connect (contracts/collab.md). Every route needs a user session.
  collab: {
    status: () => http.get<T.CollabStatus>("/v1/collab"),
    accounts: {
      /** manage: an admin also gets the shared accounts of people who left (owner null). */
      list: (q?: { manage?: boolean }) => http.get<T.MailAccount[] | T.Page<T.MailAccount>>("/v1/collab/accounts", { query: q }),
      /** Sets the flow cookie; the response's url is the provider's sign-in. */
      oauthStart: (provider: "google" | "microsoft", b: CollabOAuthStartBody) => http.post<{ url: string }>(`/v1/collab/accounts/oauth/${provider}/start`, b),
      /** Both servers are tried first: 422 connection_failed with detail. */
      createImap: (b: CollabImapBody) => http.create<T.MailAccount>("/v1/collab/accounts/imap", b),
      update: (id: string, b: CollabAccountPatch) => http.patch<T.MailAccount>(`/v1/collab/accounts/${id}`, b),
      /** Removes the account and its mail from opendesk; nothing is deleted at the provider. */
      remove: (id: string) => http.del(`/v1/collab/accounts/${id}`),
      sync: (id: string) => http.post<void>(`/v1/collab/accounts/${id}/sync`),
      /** Starts a history import, or starts it again with this scope (contracts/collab.md "History"). */
      startHistory: (id: string, b: { since: string | null; include_spam_trash?: boolean }) =>
        http.put<T.MailAccount>(`/v1/collab/accounts/${id}/history`, b),
      pauseHistory: (id: string, paused: boolean) => http.patch<T.MailAccount>(`/v1/collab/accounts/${id}/history`, { paused }),
      /** Stops it (canceled); what it imported stays. */
      stopHistory: (id: string) => http.del<T.MailAccount>(`/v1/collab/accounts/${id}/history`),
      /** Gmail only: replaces the signature with Gmail's now. */
      pullSignature: (id: string) => http.post<T.MailAccount>(`/v1/collab/accounts/${id}/signature/pull`),
      pullDisplayName: (id: string) => http.post<T.MailAccount>(`/v1/collab/accounts/${id}/display-name/pull`),
      /** Written to the provider first: 502 provider_error, nothing saved, when it refuses. */
      setOutOfOffice: (id: string, b: T.MailOutOfOffice) => http.put<T.MailAccount>(`/v1/collab/accounts/${id}/out-of-office`, b),
      /** Addresses it sends from (contracts/collab.md "Addresses"): hand-added ones for Microsoft and IMAP. */
      addIdentity: (id: string, b: CollabIdentityBody & { email: string }) => http.post<T.MailAccount>(`/v1/collab/accounts/${id}/identities`, b),
      updateIdentity: (id: string, identityId: string, b: CollabIdentityBody) => http.patch<T.MailAccount>(`/v1/collab/accounts/${id}/identities/${identityId}`, b),
      pullIdentitySignature: (id: string, identityId: string) => http.post<T.MailAccount>(`/v1/collab/accounts/${id}/identities/${identityId}/signature/pull`),
      pullIdentityDisplayName: (id: string, identityId: string) => http.post<T.MailAccount>(`/v1/collab/accounts/${id}/identities/${identityId}/display-name/pull`),
      previewIdentity: (id: string, b: T.MailIdentityPreviewBody) => http.post<T.MailIdentityPreview>(`/v1/collab/accounts/${id}/identities/preview`, b),
      removeIdentity: (id: string, identityId: string) => http.del<T.MailAccount>(`/v1/collab/accounts/${id}/identities/${identityId}`),
      /** Microsoft: the account's other addresses, when Microsoft shares them. */
      refreshIdentities: (id: string) => http.post<T.MailAccount>(`/v1/collab/accounts/${id}/identities/refresh`),
      identitySuggestions: (id: string) => http.get<{ addresses: string[] }>(`/v1/collab/accounts/${id}/identities/suggestions`),
      folders: (id: string) => http.get<T.MailFolder[] | T.Page<T.MailFolder>>(`/v1/collab/accounts/${id}/folders`),
      /** kind label on Microsoft makes an Outlook category; color is a tone, labels only. */
      createFolder: (id: string, b: { name: string; parent_id?: string; kind?: "folder" | "label"; color?: string }) => http.create<T.MailFolder>(`/v1/collab/accounts/${id}/folders`, b),
    },
    folders: {
      rename: (id: string, name: string) => http.patch<T.MailFolder>(`/v1/collab/folders/${id}`, { name }),
      /** A Gmail label's or Outlook category's colour, as a tone; set at the provider first. */
      color: (id: string, color: string) => http.patch<T.MailFolder>(`/v1/collab/folders/${id}`, { color }),
      remove: (id: string) => http.del(`/v1/collab/folders/${id}`),
    },
    threads: {
      list: (q: CollabThreadQuery) => http.get<T.Page<T.MailThread>>("/v1/collab/threads", { query: q }),
      get: (id: string) => http.get<T.MailThreadDetail>(`/v1/collab/threads/${id}`),
      /** All or nothing: a thread the caller cannot act on is 404. */
      action: (b: { thread_ids: string[]; action: T.MailThreadAction; folder_id?: string; category?: string }) =>
        http.post<{ threads: T.MailThread[] }>("/v1/collab/threads/actions", b),
      share: (id: string, shares: T.MailAudience) => http.put<T.MailThreadDetail>(`/v1/collab/threads/${id}/shares`, { shares }),
      assign: (id: string, user_ids: string[]) => http.put<T.MailThreadDetail>(`/v1/collab/threads/${id}/assignees`, { user_ids }),
      comment: (id: string, b: { body_html: string; mentions?: string[] }) => http.create<T.MailComment>(`/v1/collab/threads/${id}/comments`, b),
    },
    comments: {
      update: (id: string, body_html: string) => http.patch<T.MailComment>(`/v1/collab/comments/${id}`, { body_html }),
      remove: (id: string) => http.del(`/v1/collab/comments/${id}`),
    },
    /** One address across the conversations you see; exclude: the open one, left out of threads. */
    person: (q: { email: string; exclude?: string; limit?: number }) => http.get<T.MailPerson>("/v1/collab/person", { query: q }),
    counts: () => http.get<T.MailCounts>("/v1/collab/counts"),
    /** Inbox categories (contracts/collab.md "Categories"). */
    categories: {
      get: () => http.get<T.MailCategorySettings>("/v1/collab/categories"),
      setWorkspace: (b: { enabled: boolean; categories?: T.MailCategory[]; locked?: boolean }) => http.put<T.MailCategorySettings>("/v1/collab/categories/workspace", b),
      removeWorkspace: () => http.del<T.MailCategorySettings>("/v1/collab/categories/workspace"),
      setTeam: (teamId: string, b: { enabled: boolean; categories?: T.MailCategory[] }) => http.put<T.MailCategorySettings>(`/v1/collab/categories/teams/${teamId}`, b),
      removeTeam: (teamId: string) => http.del<T.MailCategorySettings>(`/v1/collab/categories/teams/${teamId}`),
      orderTeams: (team_ids: string[]) => http.put<T.MailCategorySettings>("/v1/collab/categories/team-order", { team_ids }),
      /** Ollie's proposal, not saved: from an account's inbox and what about says. */
      suggest: (b: { account_id?: string; about?: string }) => http.post<{ categories: T.MailCategory[] }>("/v1/collab/categories/suggest", b),
      setAccount: (id: string, b: { mode: T.MailAccountCategories["mode"]; categories?: T.MailCategory[]; ai?: boolean }) =>
        http.put<T.MailAccount>(`/v1/collab/accounts/${id}/categories`, b),
      rules: (id: string) => http.get<{ rules: T.MailCategoryRule[] }>(`/v1/collab/accounts/${id}/category-rules`),
      addRule: (id: string, b: { sender: string; category: string }) => http.post<T.MailCategoryRule>(`/v1/collab/accounts/${id}/category-rules`, b),
      removeRule: (id: string, ruleId: string) => http.del(`/v1/collab/accounts/${id}/category-rules/${ruleId}`),
    },
    /** Sent through the provider within the request (502 send_failed, nothing stored). */
    send: (b: CollabSendBody) => http.create<T.MailMessage>("/v1/collab/messages", b),
    /** Read from the message's source at the provider (502 provider_error). */
    messageDetails: (id: string) => http.get<T.MailMessageDetails>(`/v1/collab/messages/${id}/details`),
    /** Drafts, kept at the provider (contracts/collab.md "Drafts"). */
    drafts: {
      create: (b: CollabSendBody) => http.create<T.MailMessage>("/v1/collab/drafts", b),
      /** Saves again under the same id; 404 when it is no longer a draft. */
      update: (id: string, b: CollabDraftBody) => http.put<T.MailMessage>(`/v1/collab/drafts/${id}`, b),
      remove: (id: string) => http.del(`/v1/collab/drafts/${id}`),
      /** With a body the draft is saved with it first. */
      send: (id: string, b?: CollabDraftBody) => http.post<T.MailMessage>(`/v1/collab/drafts/${id}/send`, b),
    },
    sidebar: {
      get: () => http.get<T.SidebarPrefs>("/v1/collab/sidebar"),
      put: (b: T.SidebarPrefs) => http.put<T.SidebarPrefs>("/v1/collab/sidebar", b),
    },
  },
  public: {
    csat: (token: string) =>
      http.post<T.CsatSurvey>("/v1/public/csat/survey", { token }, pub),
    submitCsat: (token: string, b: { score: number; comment?: string }) => http.post<void>("/v1/public/csat/rating", { token, ...b }, pub),
  },
};

function crud<R, B>(base: string) {
  return {
    list: (q?: ListQuery & Record<string, string | number | boolean | undefined | null>) =>
      http.get<T.Page<R>>(base, { query: q }),
    get: (id: string) => http.get<R>(`${base}/${id}`),
    create: (b: B) => http.create<R>(base, b),
    update: (id: string, b: Partial<B>) => http.patch<R>(`${base}/${id}`, b),
    remove: (id: string) => http.del(`${base}/${id}`),
  };
}

/** Lists may come back bare or wrapped; read either. */
export function rows<R>(r: R[] | T.Page<R> | { data: R[] } | undefined | null): R[] {
  if (!r) return [];
  return Array.isArray(r) ? r : r.data;
}

// ─── Collab bodies (contracts/collab.md) ────────────────────────────────────

export type CollabThreadQuery = {
  box?: T.MailBox;
  account_id?: string;
  /** Replaces box. */
  folder_id?: string;
  q?: string;
  /** Only conversations with this address on them (one of the person's addresses). */
  to?: string;
  /** Only conversations with this person on them; without account_id, every conversation you see. */
  with?: string;
  /** One tab of the inbox (box inbox): a category key. */
  category?: string;
  limit?: number;
  cursor?: string | null;
};

export type CollabOAuthStartBody = {
  visibility: "private" | "shared";
  shared_with?: T.MailAudience;
  name?: string;
  /** A login hint. */
  email?: string;
  /** Reconnect this account (manage). */
  account_id?: string;
  /** Older mail to import once connected (since null: all of it); left out, none. */
  history?: T.ConnectHistory;
};

export type CollabImapBody = {
  email: string;
  name?: string;
  display_name?: string;
  imap_host: string;
  imap_port: number;
  imap_tls: T.MailTls;
  imap_username?: string;
  password: string;
  smtp_host: string;
  smtp_port: number;
  smtp_tls: T.MailTls;
  smtp_username?: string;
  smtp_password?: string;
  visibility: "private" | "shared";
  shared_with?: T.MailAudience;
  /** Older mail to import once connected (since null: all of it); left out, none. */
  history?: T.ConnectHistory;
};

export type CollabAccountPatch = {
  name?: string;
  display_name?: string;
  color?: string;
  signature_html?: string;
  visibility?: "private" | "shared";
  handling_enabled?: boolean;
  reply_all?: boolean;
  shared_with?: T.MailAudience;
  imap?: Partial<T.MailImapSettings> & { password?: string; smtp_password?: string };
  /** The address new mail goes out from. */
  default_from?: string;
};

export type CollabIdentityBody = {
  /** Empty uses the account's From name. */
  display_name?: string;
  signature_html?: string;
  /** Hand-added addresses only. */
  reply_to?: string;
};

export type CollabDraftBody = {
  to: string[];
  cc?: string[];
  bcc?: string[];
  subject: string;
  body_html: string;
  attachment_ids?: string[];
  /** The draft's own files to keep (MailAttachment ids); left out keeps them all. */
  keep_attachments?: string[];
  /** One of the account's usable addresses; left out keeps the draft's. */
  from_email?: string;
};

export type CollabSendBody = {
  account_id: string;
  mode: "new" | "reply" | "reply_all" | "forward";
  /** The message replied to or forwarded; required unless new. */
  message_id?: string;
  to: string[];
  cc?: string[];
  bcc?: string[];
  subject: string;
  body_html: string;
  /** POST /v1/attachments uploads of the caller. */
  attachment_ids?: string[];
  forward_attachments?: boolean;
  /** One of the account's usable addresses; left out: a reply goes from the address it answers, new mail from the default. */
  from_email?: string;
};
