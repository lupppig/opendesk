// The in-memory database behind mock mode.

import type {
  Activity,
  AIFeature,
  AISettings,
  Billing,
  ApiKey,
  AuditLog,
  Automation,
  Company,
  Contact,
  CustomField,
  Envelope,
  Invitation,
  KbArticle,
  KnowledgeDoc,
  KnowledgeSource,
  McpServer,
  KbCollection,
  Mailbox,
  Message,
  Notification,
  Role,
  SavedReply,
  Session,
  SlaPolicy,
  Tag,
  TagGroup,
  Team,
  Thread,
  Ticket,
  User,
  UserRef,
  View,
  Webhook,
  WebhookDelivery,
  WidgetSettings,
  Workspace,
  Attachment,
  AssignmentRule,
  Brand,
  RoutingRule,
  Widget,
  Availability,
  BusinessHours,
  TriageField,
  Bot,
  EmailCadence,
  NotificationChannels,
  NotificationType,
} from "@/lib/api/types";

export type MemberRow = {
  user_id: string;
  role: Role;
  created_at: string;
  availability?: Availability;
  max_open_tickets?: number | null;
  message_delete?: "off" | "own" | "all" | null;
  last_seen_at?: string | null;
  onboarding_dismissed?: boolean;
};

/** Triage and assignment state (handlers/pictures.ts). */
export type RoutingState = {
  rules: AssignmentRule[];
  autoAwayMinutes: number;
  hours: Omit<BusinessHours, "timezone">;
  triageFields: TriageField[];
  triageThreshold: number;
  assignAfterTriage: boolean;
  widgetSeen: boolean;
};

/** One metered AI call (contracts/billing.md): counts only, never content. */
export type AiUsageRow = {
  feature: AIFeature;
  user_id: string | null;
  /** What the call was charged, in integer micro-dollars (millionths of a US dollar). */
  micro_usd: number;
  /** An OpenRouter model id, or "jev" for decisions. */
  model: string;
  input_tokens: number;
  output_tokens: number;
  at: string;
  /** "own_key": the workspace's own provider key paid ($0). Unset: the instance's provider. */
  source?: "instance" | "own_key";
};

export type WsData = {
  workspace: Workspace;
  members: MemberRow[];
  invitations: (Invitation & { token: string })[];
  teams: Team[];
  contacts: Contact[];
  companies: Company[];
  tickets: Ticket[];
  deletedTickets: Set<string>;
  messages: Message[];
  /** Third-party threads (contracts/api.md "Threads"). */
  threads: Thread[];
  originals: Record<string, string>;
  activities: Activity[];
  tags: Tag[];
  tagGroups: TagGroup[];
  customFields: CustomField[];
  savedReplies: SavedReply[];
  views: View[];
  sla: SlaPolicy[];
  automations: Automation[];
  mailboxes: Mailbox[];
  collections: KbCollection[];
  articles: KbArticle[];
  apiKeys: ApiKey[];
  webhooks: Webhook[];
  deliveries: Record<string, WebhookDelivery[]>;
  audit: AuditLog[];
  notifications: Notification[];
  widget: WidgetSettings;
  events: Envelope[];
  seq: number;
  csat: Record<string, { ticket_id: string; score: number | null; comment: string }>;
  /** Billing state (handlers/billing.ts). */
  billing: Billing;
  /** settings.ai (handlers/ai.ts). */
  ai: AISettings;
  /** AI usage rows, counts only (contracts/ai.md). */
  aiUsage: AiUsageRow[];
  /** The widget agent's MCP connections (handlers/mcp.ts). */
  mcpServers: McpServer[];
  /** Knowledge sources and their documents (handlers/knowledge.ts). */
  knowledge: KnowledgeSource[];
  knowledgeDocs: Record<string, KnowledgeDoc[]>;
  /** Assignment rules, auto-away, business hours and triage options. */
  routing: RoutingState;
  /** Brands, chat widgets and routing rules (contracts/teams.md; handlers/teams.ts). */
  brands: Brand[];
  /** `settings` of the default brand's first widget is also `widget` (the legacy /v1/widget/settings). */
  widgets: Widget[];
  /** Each widget's identity verification secret, never answered after create or rotate. */
  widgetSecrets: Record<string, string>;
  routingRules: RoutingRule[];
  /** The AI agent's bots (contracts/bots.md; mock/bots.ts makes the default on first use). */
  bots?: Bot[];
  /** Notification channels each member set here, by user id then type; unset ones follow the defaults (handlers/notificationPrefs.ts). */
  notificationPrefs?: Record<string, Partial<Record<NotificationType, Partial<NotificationChannels>>>>;
  /** Collab is off here whatever the URL says (a workspace seeded without it). */
  collabOff?: "plan" | "disabled";
  /** Teams a member turned notifications off for here, by user id (handlers/notificationPrefs.ts). */
  mutedTeams?: Record<string, string[]>;
  /** Unsubscribe links used here, by user id: when, and each type's email choice before (handlers/notificationPrefs.ts). */
  notificationUnsubscribes?: Record<string, { at: string; previous: Partial<Record<NotificationType, boolean | undefined>> }>;
};

/** A user's account-wide notification settings: the email schedule and the pause. */
export type NotificationSettingsRow = { email_cadence: EmailCadence; digest_hour: number; timezone: string; paused_until: string | null };

export type Db = {
  users: User[];
  meId: string;
  sessions: Session[];
  workspaces: WsData[];
  attachments: Attachment[];
  totpPending: string | null;
  /** The owner-level gate for this session (contracts/api.md): "ok", or why it refuses. `?mock_gate=mfa|reauth` sets it. */
  stepUp: "ok" | "mfa_required" | "reauth_required";
  /** Account-wide notification settings by user id; unset users have the defaults. */
  notificationSettings: Record<string, NotificationSettingsRow>;
};

export const db: Db = {
  users: [],
  meId: "",
  sessions: [],
  workspaces: [],
  attachments: [],
  totpPending: null,
  stepUp: "ok",
  notificationSettings: {},
};

export function me(): User {
  const u = db.users.find((x) => x.id === db.meId);
  if (!u) throw new Error("mock: no current user");
  return u;
}

/** The account a mock access token was issued to (tokens name their account). */
export function userFromToken(header: string | null): string | null {
  const m = /^Bearer mock_access_([0-9a-f-]{36})_/.exec(header ?? "");
  return m && db.users.some((u) => u.id === m[1]) ? m[1] : null;
}

export function userRef(id: string | null | undefined): UserRef | null {
  if (!id) return null;
  const u = db.users.find((x) => x.id === id);
  return u ? { id: u.id, name: u.name, email: u.email, avatar_url: u.avatar_url } : null;
}

export function wsById(id: string | null | undefined): WsData | undefined {
  return db.workspaces.find((w) => w.workspace.id === id);
}

export function defaultRouting(): RoutingState {
  const day = [{ start: "09:00", end: "17:00" }];
  return {
    rules: [],
    autoAwayMinutes: 0,
    hours: { enabled: false, schedule: { mon: day, tue: day, wed: day, thu: day, fri: day, sat: [], sun: [] }, holidays: [] },
    triageFields: ["priority", "severity", "labels"],
    triageThreshold: 0.7,
    assignAfterTriage: false,
    widgetSeen: false,
  };
}

export function contactRef(c: Contact) {
  return { id: c.id, email: c.email, name: c.name, avatar_url: c.avatar_url };
}
