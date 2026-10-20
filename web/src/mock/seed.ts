// Builds the mock database: the Margin workspace (rich) and Lumen Labs (small).

import { applyDecisions, severityFor } from "./decisions";
import type {
  ApiKey,
  Brand,
  RoutingRule,
  Team,
  TeamRole,
  Widget,
  Attachment,
  AuditLog,
  Automation,
  Contact,
  KbArticle,
  KbCollection,
  Mailbox,
  Priority,
  SlaPolicy,
  Ticket,
  TicketStatus,
  AISettings,
  User,
  Webhook,
  WebhookDelivery,
  WidgetSettings,
} from "@/lib/api/types";
import {
  AGENT_REPLIES,
  ARTICLES,
  COLLECTIONS,
  COMPANIES,
  FIRST,
  FOLLOW_UPS,
  GENERATED,
  LAST,
  SAVED_REPLIES,
  SPAM,
  STORIES,
  TEAM,
  type TeamKey,
} from "./content";
import { contactRef, db, defaultRouting, userRef, type WsData } from "./db";
import { brandLogo, logoMark, portrait } from "./avatars";
import { teammatePresence, type Meta } from "./gateway";
import { seedDefaultLabels } from "./labels";
import { seedDefaultBrand } from "./handlers/teams";
import { labelName, seedMarginFields, seedMarginLabels, seedTicketTypes } from "./seedLabels";
import { addActivity, addMessage, computeSla, ensureTags, newTicket } from "./tickets";
import { DAY, HOUR, MIN, ago, chance, int, iso, NOW, pick, preview, reseed, shuffle, slugify, textToHtml, uuid } from "./util";
import { defaultPersonality, newBot } from "./bots";

const svgData = (svg: string) => `data:image/svg+xml;utf8,${encodeURIComponent(svg)}`;

function screenshotSvg(label: string, dark = false): string {
  const bg = dark ? "#171717" : "#fafafa";
  const card = dark ? "#262626" : "#ffffff";
  const line = dark ? "#3a3a3a" : "#e4e4e4";
  const ink = dark ? "#e4e4e4" : "#0a0a0a";
  const muted = dark ? "#525252" : "#d4d4d4";
  return svgData(
    `<svg xmlns="http://www.w3.org/2000/svg" width="960" height="600" viewBox="0 0 960 600"><rect width="960" height="600" fill="${bg}"/><rect x="0" y="0" width="200" height="600" fill="${card}" stroke="${line}"/><rect x="24" y="28" width="110" height="12" rx="6" fill="${ink}"/>${[0, 1, 2, 3, 4, 5].map((i) => `<rect x="24" y="${80 + i * 34}" width="${120 - i * 9}" height="9" rx="4.5" fill="${muted}"/>`).join("")}<rect x="232" y="28" width="696" height="544" rx="12" fill="${card}" stroke="${line}"/><rect x="264" y="64" width="300" height="16" rx="8" fill="${ink}"/>${[0, 1, 2, 3, 4, 5, 6, 7].map((i) => `<rect x="264" y="${116 + i * 26}" width="${600 - (i % 3) * 120}" height="9" rx="4.5" fill="${muted}"/>`).join("")}<rect x="264" y="350" width="620" height="160" rx="8" fill="${bg}" stroke="${line}"/><text x="574" y="438" font-family="Helvetica, Arial" font-size="18" text-anchor="middle" fill="${dark ? "#8a8a8a" : "#8a8a8a"}">${label}</text></svg>`,
  );
}

function makeAttachment(kind: "screenshot" | "screenshot2" | "pdf" | "log"): Attachment {
  const a: Attachment =
    kind === "screenshot"
      ? { id: uuid(), filename: "screenshot-2026-10-11.png", content_type: "image/png", size_bytes: 284_311, inline: false, url: screenshotSvg("Sign-in redirect loop") }
      : kind === "screenshot2"
        ? { id: uuid(), filename: "export-preview.png", content_type: "image/png", size_bytes: 512_904, inline: false, url: screenshotSvg("Images missing in export", true) }
        : kind === "pdf"
          ? { id: uuid(), filename: kind === "pdf" ? "document.pdf" : "file", content_type: "application/pdf", size_bytes: 1_204_118, inline: false, url: "data:text/plain;charset=utf-8,Mock%20PDF%20attachment" }
          : { id: uuid(), filename: "crash-report.log", content_type: "text/plain", size_bytes: 48_220, inline: false, url: "data:text/plain;charset=utf-8,Thread%200%20Crashed%3A%20EXC_BAD_ACCESS" };
  db.attachments.push(a);
  return a;
}

export function baseWorkspace(id: string, name: string, slug: string): WsData {
  return {
    workspace: {
      id,
      name,
      slug,
      widget_key: `wk_${slug}_${uuid().slice(0, 8)}`,
      settings: {},
      avatar_url: null,
      avatar_source: null,
      images: { help_center_logo: null, help_center_favicon: null, widget_avatar: null, widget_launcher: null },
      created_at: ago(400 * DAY),
    },
    members: [],
    invitations: [],
    teams: [],
    contacts: [],
    companies: [],
    tickets: [],
    deletedTickets: new Set(),
    messages: [],
    threads: [],
    originals: {},
    activities: [],
    tags: [],
    tagGroups: [],
    customFields: [],
    savedReplies: [],
    views: [],
    sla: [],
    automations: [],
    mailboxes: [],
    collections: [],
    articles: [],
    apiKeys: [],
    webhooks: [],
    deliveries: {},
    audit: [],
    notifications: [],
    widget: defaultWidget(),
    events: [],
    seq: 0,
    csat: {},
    billing: defaultBilling(),
    ai: defaultAi(),
    aiUsage: [],
    mcpServers: [],
    knowledge: [],
    knowledgeDocs: {},
    routing: defaultRouting(),
    brands: [],
    widgets: [],
    widgetSecrets: {},
    routingRules: [],
  };
}

/** settings.widget with every contract default (contracts/api.md "Widget"). */
export function defaultWidget(): WidgetSettings {
  return {
    enabled: true,
    greeting: "Hi there! How can we help?",
    require_email: false,
    allowed_origins: [],
    color: "",
    theme: "auto",
    position: "right",
    offset_x: 20,
    offset_y: 20,
    launcher: "icon",
    launcher_label: "",
    radius: "soft",
    title: "",
    subtitle: "",
    show_team: true,
    suggestions: [],
    sound: false,
    topics: [],
    offline_message: "",
    email_follow_up: true,
  };
}

/** settings.ai with every contract default (contracts/ai.md, contracts/agent.md). */
export function defaultAi(): AISettings {
  return {
    enabled: true,
    auto_triage: false,
    auto_summarize: false,
    widget_answers: false,
    assistant: true,
    tone: "friendly",
    reply_language: "customer",
    instructions: "",
    model: "",
    fast_model: "",
    widget_model: "",
    widget_max_replies: 20,
    widget_blocked_topics: [],
    widget_monthly_cap_usd: 0,
    widget_collect_email: true,
  };
}

const MARGIN_AGENT = [
  "Margin is a note-taking app for teams, on the web, Mac, Windows, iOS and Android.",
  "Plans: Free (1 workspace, 3 members), Team at $8 per member per month, Business at $15 per member per month with SSO and audit logs.",
  "Refunds: we refund annual plans in full within 30 days of payment. Monthly plans are not refunded but can be cancelled any time.",
  "Support hours: Monday to Friday, 8:00 to 18:00 CET. Outside those hours, say the team will reply the next working day.",
  "Links: pricing https://margin.app/pricing, status https://status.margin.app, help center https://margin.app/help.",
  "Never promise a feature date. For bugs, ask for the app version and device.",
].join("\n");

/** Self-hosted entitlements; handlers/billing.ts seeds real plans per workspace. */
export function defaultBilling(): WsData["billing"] {
  return {
    plan: "self_hosted",
    billing_enabled: false,
    status: "none",
    interval: "month",
    seats: 0,
    billed_seats: 0,
    current_period_end: null,
    cancel_at_period_end: false,
    limits: { max_mailboxes: 0, max_members: 0 },
    usage: { mailboxes: 0, members: 0 },
    ai: { unlimited: true, monthly_usd: 0, used_usd: 0, purchased_usd: 0, remaining_usd: 0, period_end: new Date(NOW + 30 * DAY).toISOString() },
    prices: { pro_month: null, pro_year: null, ai_top_up: null },
  };
}

function seedUsers() {
  for (const p of TEAM) {
    const u: User = {
      id: uuid(),
      email: p.email,
      name: p.name,
      // Maya uploaded a photo, Jonas and Lea signed in with Google; the others have none.
      avatar_url: p.key === "maya" || p.key === "jonas" || p.key === "lea" ? portrait(p.email) : null,
      avatar_source: p.key === "maya" ? "upload" : p.key === "jonas" || p.key === "lea" ? "oauth" : null,
      email_verified: true,
      totp_enabled: p.key === "jonas",
      created_at: ago(int(120, 380) * DAY),
    };
    db.users.push(u);
  }
  db.meId = db.users[0].id;
  db.sessions = [
    { id: uuid(), user_agent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 15_6) AppleWebKit/605.1.15 Chrome/141.0 Safari/537.36", ip: "84.152.33.17", created_at: ago(3 * DAY), last_used_at: ago(1 * MIN), current: true },
    { id: uuid(), user_agent: "Mozilla/5.0 (iPhone; CPU iPhone OS 26_0 like Mac OS X) AppleWebKit/605.1.15 Mobile Safari/604.1", ip: "84.152.33.90", created_at: ago(12 * DAY), last_used_at: ago(5 * HOUR), current: false },
    { id: uuid(), user_agent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Firefox/142.0", ip: "62.214.8.201", created_at: ago(40 * DAY), last_used_at: ago(9 * DAY), current: false },
  ];
}

const uid = (k: TeamKey) => db.users[TEAM.findIndex((t) => t.key === k)].id;

function seedMargin(): WsData {
  const ws = baseWorkspace(uuid(), "Margin", "margin");
  ws.workspace.settings = {
    timezone: "Europe/Berlin",
    help_center_enabled: true,
    help_center_title: "Margin Help",
    csat_enabled: true,
    auto_assign: "round_robin",
  };
  ws.widget = {
    ...defaultWidget(),
    greeting: "Hi! Ask us anything about Margin.",
    require_email: true,
    allowed_origins: ["https://margin.app", "https://www.margin.app"],
    color: "#4f46e5",
    title: "Margin support",
    subtitle: "We usually reply in a few minutes",
    suggestions: ["Where's my order?", "Change my plan", "Export my notes"],
  };
  ws.ai = {
    ...defaultAi(),
    widget_answers: true,
    widget_blocked_topics: ["legal advice", "competitor pricing", "medical advice"],
    widget_monthly_cap_usd: 25,
  };

  const availability = { maya: "online", jonas: "online", priya: "away", sam: "online", lea: "offline", theo: "online" } as const;
  TEAM.forEach((p, i) =>
    ws.members.push({
      user_id: db.users[i].id,
      role: p.role,
      created_at: ago((380 - i * 40) * DAY),
      availability: availability[p.key],
      max_open_tickets: p.key === "sam" ? 12 : null,
      last_seen_at: ago((i + 1) * 7 * MIN),
    }),
  );
  ws.workspace.avatar_url = brandLogo("margin", "#4f46e5");
  ws.workspace.avatar_source = "upload";
  ws.workspace.images.help_center_logo = brandLogo("margin", "#4f46e5", true);
  ws.workspace.images.help_center_favicon = brandLogo("margin", "#4f46e5");
  ws.routing.autoAwayMinutes = 30;
  ws.routing.widgetSeen = true;
  ws.routing.hours = {
    enabled: true,
    schedule: { mon: [{ start: "08:00", end: "18:00" }], tue: [{ start: "08:00", end: "18:00" }], wed: [{ start: "08:00", end: "18:00" }], thu: [{ start: "08:00", end: "18:00" }], fri: [{ start: "08:00", end: "16:00" }], sat: [], sun: [] },
    holidays: [{ date: "2026-12-24", name: "Christmas Eve" }, { date: "2026-12-25", name: "Christmas Day" }, { date: "2027-01-01", name: "New Year's Day" }],
  };
  // Maya (the signed-in owner) is on Support, Billing and Partnerships, not on the private Legal and Security teams.
  const team = (name: string, color: Team["color"], days: number, members: [TeamKey, TeamRole][], extra: Partial<Team> = {}): Team => ({
    id: uuid(),
    name,
    description: "",
    color,
    private: false,
    members: members.map(([k, role]) => ({ user_id: uid(k), role })),
    member_ids: members.map(([k]) => uid(k)),
    signature_html: "",
    business_hours: null,
    assign_mode: null,
    mailbox_ids: [],
    open_count: 0,
    unassigned_count: 0,
    created_at: ago(days * DAY),
    updated_at: ago(int(2, 40) * DAY),
    ...extra,
  });
  ws.teams = [
    team("Support", "blue", 300, [["maya", "lead"], ["jonas", "member"], ["priya", "member"], ["sam", "member"], ["theo", "observer"]], {
      description: "First line for product questions, bugs and how-tos.",
      signature_html: "<p>{{agent.first_name}} from {{brand.name}} support<br>Ticket #{{ticket.number}}</p>",
    }),
    team("Billing", "amber", 280, [["lea", "lead"], ["maya", "member"], ["sam", "member"]], {
      description: "Invoices, refunds, plan changes and payment problems.",
      signature_html: "<p>{{agent.name}}<br>{{team.name}}, {{brand.name}}</p>",
    }),
    team("Legal", "violet", 160, [["lea", "lead"], ["jonas", "member"]], {
      description: "Contracts, DPAs, GDPR requests and anything a lawyer should read first.",
      private: true,
      signature_html: "<p>{{agent.name}}<br>Legal, {{brand.name}}</p>",
      business_hours: {
        enabled: true,
        timezone: "Europe/Berlin",
        schedule: { mon: [{ start: "09:00", end: "17:00" }], tue: [{ start: "09:00", end: "17:00" }], wed: [{ start: "09:00", end: "17:00" }], thu: [{ start: "09:00", end: "17:00" }], fri: [{ start: "09:00", end: "13:00" }], sat: [], sun: [] },
        holidays: [],
      },
    }),
    team("Security", "red", 120, [["jonas", "lead"], ["priya", "member"]], {
      description: "Vulnerability reports and account security. Reports are confidential until fixed.",
      private: true,
    }),
    // Jonas (an admin) joined as an observer, which told the leads (the seeded team_joined notification).
    team("Partnerships", "green", 90, [["sam", "lead"], ["maya", "lead"], ["jonas", "observer"]], {
      description: "Resellers, integrations and co-marketing.",
    }),
  ];
  const teamId = (n: "Support" | "Billing" | "Legal" | "Security" | "Partnerships" | null) => (n ? ws.teams.find((t) => t.name === n)!.id : null);
  ws.routing.rules = [
    { target_type: "team", target_id: teamId("Billing")!, mode: "load_balanced" },
    { target_type: "team", target_id: teamId("Legal")!, mode: "none" },
    { target_type: "team", target_id: teamId("Support")!, mode: "round_robin" },
  ];

  ws.invitations = [
    { id: uuid(), token: "inv_demo", email: "rafael@margin.app", role: "agent", expires_at: iso(NOW + 5 * DAY), created_at: ago(2 * DAY), invited_by: userRef(uid("maya")) },
    { id: uuid(), token: "inv_demo2", email: "noor@margin.app", role: "admin", expires_at: iso(NOW + 6 * DAY), created_at: ago(1 * DAY), invited_by: userRef(uid("jonas")) },
  ];

  // Tags, SLA, mailboxes.
  seedMarginLabels(ws);
  const sla: SlaPolicy[] = [
    { id: uuid(), name: "Urgent", priority: "urgent", first_response_minutes: 60, resolution_minutes: 8 * 60, business_hours: false, enabled: true, position: 0 },
    { id: uuid(), name: "High priority", priority: "high", first_response_minutes: 2 * 60, resolution_minutes: 24 * 60, business_hours: true, enabled: true, position: 1 },
    { id: uuid(), name: "Legal requests", priority: null, first_response_minutes: 2 * 24 * 60, resolution_minutes: 30 * 24 * 60, business_hours: true, enabled: true, position: 2, team_id: teamId("Legal") },
    { id: uuid(), name: "Security reports", priority: null, first_response_minutes: 4 * 60, resolution_minutes: 7 * 24 * 60, business_hours: false, enabled: true, position: 3, team_id: teamId("Security") },
    { id: uuid(), name: "Standard", priority: null, first_response_minutes: 8 * 60, resolution_minutes: 3 * 24 * 60, business_hours: true, enabled: true, position: 4 },
  ];
  ws.sla = sla;

  // Every address belongs to a brand; the brand ids are filled in once the brands exist (below).
  const mbBase = { signature_html: "", auto_reply_enabled: false, auto_reply_subject: "", auto_reply_html: "", ai_answers: false, ai_answers_mode: "draft" as const, ai_answers_outside_hours: false, last_error: "", team_id: null as string | null, brand_id: "", aliases: [] as string[], alias_teams: {} as Record<string, string>, trusted_form_senders: [] as string[], from_name_format: "{agent} at {mailbox}", private: false };
  const support: Mailbox = {
    ...mbBase,
    id: uuid(),
    name: "Support",
    email: "support@margin.app",
    display_name: "Margin Support",
    inbound_kind: "google",
    outbound_kind: "google",
    forwarding_address: "margin-support-2m9q@in.opendesk.sh",
    config: { oauth_email: "support@margin.app" },
    team_id: teamId("Support"),
    aliases: ["help@margin.app", "hello@margin.app"],
    // hello@ is where partners write; it has a team of its own.
    alias_teams: { "hello@margin.app": teamId("Partnerships")! },
    // The contact form on margin.app sends from this address with the visitor as Reply-To.
    trusted_form_senders: ["website@margin.app"],
    signature_html: "<p>{{agent.first_name}} from Margin<br>margin.app</p>",
    auto_reply_enabled: true,
    auto_reply_subject: "We got your message",
    auto_reply_html: "<p>Thanks for writing in. We usually reply within a few hours on weekdays.</p>",
    ai_answers: true,
    status: "active",
    last_synced_at: ago(40 * 1000),
    created_at: ago(300 * DAY),
  };
  const billing: Mailbox = {
    ...mbBase,
    id: uuid(),
    name: "Billing",
    email: "billing@margin.app",
    display_name: "Margin Billing",
    inbound_kind: "forwarding",
    outbound_kind: "system",
    forwarding_address: "margin-billing-7f3k@in.opendesk.sh",
    config: {},
    team_id: teamId("Billing"),
    from_name_format: "{agent_first} from {team}",
    aliases: ["invoices@margin.app"],
    status: "active",
    last_synced_at: ago(12 * MIN),
    created_at: ago(260 * DAY),
  };
  const partners: Mailbox = {
    ...mbBase,
    id: uuid(),
    name: "Partners",
    email: "partners@margin.app",
    display_name: "Margin Partnerships",
    inbound_kind: "imap",
    outbound_kind: "smtp",
    forwarding_address: "margin-partners-q81z@in.opendesk.sh",
    config: { imap_host: "imap.fastmail.com", imap_port: 993, imap_username: "partners@margin.app", imap_tls: "tls", smtp_host: "smtp.fastmail.com", smtp_port: 465, smtp_username: "partners@margin.app", smtp_tls: "tls" },
    status: "error",
    last_error: "IMAP login failed: [AUTHENTICATIONFAILED] Invalid credentials",
    last_synced_at: ago(2 * DAY),
    created_at: ago(90 * DAY),
  };
  const legal: Mailbox = {
    ...mbBase,
    id: uuid(),
    name: "Legal",
    email: "legal@margin.app",
    display_name: "Margin Legal",
    // Forwarding in, its own SMTP server out.
    inbound_kind: "forwarding",
    outbound_kind: "smtp",
    forwarding_address: "margin-legal-x4c8@in.opendesk.sh",
    config: { mail_host: "google_workspace", smtp_host: "smtp.gmail.com", smtp_port: 465, smtp_tls: "tls", smtp_username: "legal@margin.app" },
    team_id: teamId("Legal"),
    private: true,
    aliases: ["privacy@margin.app", "dpo@margin.app"],
    from_name_format: "{team} at {brand}",
    auto_reply_enabled: true,
    auto_reply_subject: "We received your request",
    auto_reply_html: "<p>Thanks for writing to our legal team. We answer within two working days, and within 30 days for GDPR requests.</p>",
    status: "active",
    last_synced_at: ago(25 * MIN),
    created_at: ago(160 * DAY),
  };
  const security: Mailbox = {
    ...mbBase,
    id: uuid(),
    name: "Security",
    email: "security@margin.app",
    display_name: "Margin Security",
    // Forwarding in, connected to Google to send only.
    inbound_kind: "forwarding",
    outbound_kind: "google",
    forwarding_address: "margin-security-k2v1@in.opendesk.sh",
    config: { oauth_email: "security@margin.app", mail_host: "google_workspace" },
    team_id: teamId("Security"),
    private: true,
    from_name_format: "{agent} at {mailbox}",
    status: "active",
    last_synced_at: ago(2 * MIN),
    created_at: ago(120 * DAY),
  };
  const pressroom: Mailbox = {
    ...mbBase,
    id: uuid(),
    name: "Pressroom",
    email: "hello@pressroom.app",
    display_name: "Pressroom",
    // Forwarding in, connected to Microsoft 365 to send only.
    inbound_kind: "forwarding",
    outbound_kind: "microsoft",
    forwarding_address: "margin-pressroom-h7d2@in.opendesk.sh",
    config: { oauth_email: "hello@pressroom.app", mail_host: "microsoft365" },
    team_id: teamId("Support"),
    aliases: ["support@pressroom.app"],
    from_name_format: "{agent_first} at {brand}",
    signature_html: "<p>{{agent.first_name}}, Pressroom<br>pressroom.app</p>",
    status: "active",
    last_synced_at: ago(4 * MIN),
    created_at: ago(70 * DAY),
  };
  ws.mailboxes = [support, billing, legal, security, partners, pressroom];

  // Brands: Margin (the default, its help center at the workspace slug) and Pressroom, a second product.
  const brand = (name: string, slug: string, color: string, days: number, extra: Partial<Brand> = {}): Brand => ({
    id: uuid(),
    name,
    slug,
    is_default: false,
    color,
    locale: "en",
    help_center: { enabled: true, title: `${name} Help`, url: "", default_url: "", domain: null },
    // The logo is the mark on the brand colour; the help center has its own, the mark on white.
    images: { logo: brandLogo(slug, color), help_center_logo: brandLogo(slug, color, true), help_center_favicon: brandLogo(slug, color) },
    default_mailbox_id: null,
    mailbox_count: 0,
    widget_count: 0,
    position: 0,
    created_at: ago(days * DAY),
    updated_at: ago(int(1, 20) * DAY),
    ...extra,
  });
  const marginBrand = brand("Margin", "margin", "#4f46e5", 400, { is_default: true });
  const pressBrand = brand("Pressroom", "pressroom", "#e11d48", 70, { position: 1, help_center: { enabled: false, title: "Pressroom Help Desk", url: "", default_url: "", domain: null } });
  ws.brands = [marginBrand, pressBrand];
  for (const m of ws.mailboxes) m.brand_id = m === pressroom ? pressBrand.id : marginBrand.id;

  // Chat widgets, one per website. Topics route a conversation before its first message.
  const topic = (label: string, team: Parameters<typeof teamId>[0]) => ({ id: uuid(), label, team_id: teamId(team) });
  const marketingTopics = [topic("Billing and plans", "Billing"), topic("Technical help", "Support"), topic("Legal and privacy", "Legal")];
  const docsTopics = [topic("Technical help", "Support"), topic("Report a vulnerability", "Security")];
  const widget = (b: Brand, name: string, key: string, position: number, team: Parameters<typeof teamId>[0], settings: Partial<WidgetSettings>, days: number): Widget => ({
    id: uuid(),
    brand_id: b.id,
    name,
    key,
    team_id: teamId(team),
    bot_id: null,
    settings: { ...defaultWidget(), ...settings },
    images: { avatar: null, launcher: null },
    install: { snippet: "" },
    seen_at: ago((days - 1) * DAY),
    position,
    created_at: ago(days * DAY),
    updated_at: ago(int(1, 30) * DAY),
  });
  const marketingSite = widget(marginBrand, "Marketing site", ws.workspace.widget_key, 0, "Support", { ...ws.widget, topics: marketingTopics, offline_message: "We're offline right now. Leave your email and we'll reply first thing tomorrow." }, 380);
  const docsWidget = widget(marginBrand, "Docs", `wk_margin_docs${uuid().slice(0, 6)}`, 1, "Support", {
    greeting: "Stuck on something in the docs? Ask away.",
    allowed_origins: ["https://docs.margin.app"],
    color: "#4f46e5",
    title: "Margin docs",
    subtitle: "Engineers answer here on weekdays",
    suggestions: ["API rate limits", "Webhook signatures"],
    topics: docsTopics,
  }, 200);
  const pressWidget = widget(pressBrand, "Pressroom app", `wk_pressroom_${uuid().slice(0, 8)}`, 0, "Support", {
    greeting: "Hi! Questions about your publication? We're here.",
    allowed_origins: ["https://pressroom.app", "https://app.pressroom.app"],
    color: "#e11d48",
    title: "Pressroom",
    subtitle: "Usually replies within an hour",
    suggestions: ["Custom domains", "Import subscribers"],
    topics: [topic("Publishing help", "Support"), topic("Payments and payouts", "Billing")],
    require_email: true,
  }, 65);
  ws.widgets = [marketingSite, docsWidget, pressWidget];

  // Bots (contracts/bots.md): Ollie answers by default; Pip on the docs, the Concierge for Pressroom.
  const ollie = newBot("Ollie", {
    is_default: true,
    role: "Margin support",
    personality: { ...defaultPersonality(), instructions: MARGIN_AGENT, handoff_message: "I'll bring in someone from the team. They'll reply right here." },
    created_at: ago(380 * DAY),
    updated_at: ago(12 * DAY),
  });
  const pip = newBot("Pip", {
    role: "Docs sidekick",
    position: 1,
    look: { kind: "built", shape: "squircle", fill: { type: "gradient", color: "#9ff0e3", to: "#0b7f73" }, eyes: "happy", mouth: "grin", accessory: "sprout", accent: "#4cc27a", blush: true, background: "soft", emoji: "" },
    personality: {
      ...defaultPersonality(),
      tone: "playful",
      emoji: "lots",
      reply_length: "medium",
      greeting: "Hey! I'm Pip. Stuck on something in the docs? Ask me anything 🌱",
      handoff_message: "Ooh, that one needs a human. Fetching an engineer for you now!",
      instructions: "You help developers with the Margin API and SDKs. Link the docs page for every answer. Ask for the SDK version when something fails.",
      blocked_topics: ["unreleased features"],
    },
    created_at: ago(40 * DAY),
    updated_at: ago(3 * DAY),
  });
  const concierge = newBot("Margin Concierge", {
    role: "Pressroom publishing desk",
    position: 2,
    look: { kind: "built", shape: "circle", fill: { type: "solid", color: "#16151c", to: "#16151c" }, eyes: "glasses", mouth: "smile", accessory: "none", accent: "#f6b81a", blush: false, background: "ring", emoji: "" },
    personality: {
      ...defaultPersonality(),
      tone: "professional",
      reply_length: "detailed",
      greeting: "Good day. How may I help with your publication?",
      handoff_message: "I'm passing this to our publishing team, who will reply here shortly.",
      instructions: "Pressroom is a publishing platform for newsletters and magazines. Payouts run on the 1st and 15th. Custom domains need a CNAME to pressroom.app.",
      sign_off: "Kind regards,\nThe Pressroom desk",
    },
    created_at: ago(60 * DAY),
    updated_at: ago(9 * DAY),
  });
  ws.bots = [ollie, pip, concierge];
  docsWidget.bot_id = pip.id;
  pressWidget.bot_id = concierge.id;
  pressroom.bot_id = concierge.id;
  for (const w of ws.widgets) ws.widgetSecrets[w.id] = `wsec_${uuid().replace(/-/g, "")}`;
  ws.widget = marketingSite.settings;

  // Routing rules, first match wins.
  const rule = (name: string, position: number, match: RoutingRule["match"], conditions: RoutingRule["conditions"], team: Parameters<typeof teamId>[0], run_count: number, enabled = true): RoutingRule => ({
    id: uuid(),
    name,
    enabled,
    position,
    run_count,
    match,
    conditions,
    team_id: teamId(team)!,
    created_at: ago((150 - position * 20) * DAY),
    updated_at: ago((20 - position * 3) * DAY),
  });
  ws.routingRules = [
    rule("Invoices to Billing", 0, "any", [
      { field: "subject", op: "contains", value: "invoice" },
      { field: "subject", op: "contains", value: "refund" },
      { field: "body", op: "contains", value: "VAT" },
    ], "Billing", 214),
    rule("GDPR requests to Legal", 1, "any", [
      { field: "subject", op: "contains", value: "GDPR" },
      { field: "subject", op: "contains", value: "DPA" },
      { field: "body", op: "contains", value: "right to erasure" },
      { field: "body", op: "contains", value: "data processing agreement" },
    ], "Legal", 31),
    rule("Vulnerability reports to Security", 2, "any", [
      { field: "subject", op: "contains", value: "vulnerability" },
      { field: "subject", op: "contains", value: "disclosure" },
      { field: "body", op: "contains", value: "CVE-" },
      { field: "topic", op: "equals", value: docsTopics[1].id },
    ], "Security", 9),
    rule("Partner domains to Partnerships", 3, "any", [
      { field: "from_domain", op: "equals", value: "copperleaf.dev" },
      { field: "from_domain", op: "equals", value: "fjord.ai" },
    ], "Partnerships", 12),
    rule("Contract questions, Ollie decides", 4, "all", [
      { field: "channel", op: "equals", value: "email" },
      { field: "ai_decide", op: "gt", value: "0.8", question: "Is the customer asking about a contract, a privacy policy or another legal matter?" },
    ], "Legal", 0, false),
  ];

  // Companies and contacts.
  ws.companies = COMPANIES.map((c, i) => ({
    id: uuid(),
    name: c.name,
    domain: c.domain,
    // Most companies have a website icon, one uploaded its logo, a few have nothing usable.
    avatar_url: i % 4 === 3 ? null : logoMark(c.name),
    avatar_source: i % 4 === 3 ? null : i === 0 ? "upload" : "domain",
    notes: c.notes,
    custom: { plan: ["Enterprise", "Business", "Team", "Startup"][i % 4] },
    tags: [],
    fields: {},
    contact_count: 0,
    created_at: ago((200 - i * 9) * DAY),
    updated_at: ago(int(1, 30) * DAY),
  }));
  const fixed: [string, number | null][] = [
    ["Dana Whitfield", 3],
    ["Kofi Mensah", 0],
    ["Marcus Lindqvist", 0],
    ["Tomás García", 2],
    ["Elena Rossi", 1],
    ["Hugo Moreau", 5],
    ["Chloé Dubois", 4],
    ["Ben Hartmann", 7],
    ["Freya Nilsen", 8],
    ["Gabriel Laurent", 6],
    ["Aisha Bello", 10],
    ["Isaac Levy", 9],
    ["Julia Novak", 11],
  ];
  const people: [string, number | null][] = [...fixed];
  for (let i = 13; i < 45; i++) people.push([`${FIRST[i]} ${LAST[(i * 7) % LAST.length]}`, chance(0.72) ? int(0, COMPANIES.length - 1) : null]);
  const personal = ["gmail.com", "outlook.com", "proton.me", "icloud.com", "fastmail.com"];
  ws.contacts = people.map(([name, ci], i): Contact => {
    const company = ci === null ? null : ws.companies[ci];
    const local = slugify(name.normalize("NFD").replace(/[̀-ͯ]/g, "")).replace(/-/g, ".");
    const email = `${company ? local.split(".")[0] : local}@${company ? company.domain : pick(personal)}`;
    if (company) company.contact_count++;
    return {
      id: uuid(),
      email,
      name,
      phone: chance(0.4) ? `+49 30 ${int(1000000, 9999999)}` : "",
      // Roughly a third have a picture: the verified chat widget's for most, an agent's upload for
      // some. Gravatar pictures is off in this workspace, so none come from Gravatar.
      avatar_url: i % 3 === 0 ? portrait(email) : null,
      avatar_source: i % 3 === 0 ? (i % 2 === 0 ? "widget" : "upload") : null,
      company: company ? { id: company.id, name: company.name, domain: company.domain, avatar_url: company.avatar_url } : null,
      external_id: chance(0.5) ? `usr_${int(10000, 99999)}` : null,
      notes: i === 0 ? "IT lead. Prefers email, very responsive. Escalate SSO issues to Jonas." : "",
      custom: chance(0.5) ? { plan: pick(["Team", "Business", "Enterprise"]), seats: int(3, 240) } : {},
      tags: [],
      fields: {},
      blocked: false,
      ticket_count: 0,
      created_at: ago(int(20, 300) * DAY),
      updated_at: ago(int(1, 20) * DAY),
    };
  });

  const mention = (html: string) => html.replace(/@@(\w+)/g, (_m, k: string) => uid(k as TeamKey));

  // Story tickets.
  for (const s of STORIES) {
    const c = ws.contacts[s.contact];
    const first = s.thread[0];
    const t = newTicket(ws, {
      subject: s.subject,
      contact: contactRef(c),
      channel: s.channel,
      priority: s.priority,
      mailbox_id: s.mailbox === "billing" ? billing.id : support.id,
      assignee: s.assignee ? userRef(uid(s.assignee)) : null,
      team_id: teamId(s.team),
      tags: ensureTags(ws, s.tags.map(labelName), false),
      created_at: ago(first.min * MIN),
    });
    t.number = s.number;
    seedActivities(ws, t, s.assignee ? uid(s.assignee) : null, t.tags.map((x) => x.name));
    for (const m of s.thread) {
      addMessage(
        ws,
        t,
        {
          kind: m.note ? "note" : "reply",
          direction: m.from === "c" ? "inbound" : "outbound",
          html: mention(m.html),
          user: m.from === "c" ? null : userRef(uid(m.from)),
          attachments: (m.attach ?? []).map(makeAttachment),
          at: ago(m.min * MIN),
          has_quoted: m.quoted,
          delivery: m.delivery,
        },
        { publish: false },
      );
    }
    finalize(ws, t, s.status, s.csat);
  }

  // Generated tickets.
  const statuses: TicketStatus[] = shuffle([
    ...Array<TicketStatus>(23).fill("open"),
    ...Array<TicketStatus>(10).fill("pending"),
    ...Array<TicketStatus>(2).fill("on_hold"),
    ...Array<TicketStatus>(18).fill("solved"),
    ...Array<TicketStatus>(3).fill("closed"),
  ]);
  const agents: TeamKey[] = ["maya", "jonas", "priya", "sam", "lea"];
  const gen = GENERATED.slice(0, statuses.length);
  gen.forEach((g, i) => {
    const status = statuses[i];
    const c = ws.contacts[(i * 5 + 13) % ws.contacts.length];
    const openish = status === "open" || status === "pending" || status === "on_hold";
    const createdMin = openish ? int(60, 6 * 24 * 60) : int(2 * 24 * 60, 30 * 24 * 60);
    const priority: Priority = pick(["low", "normal", "normal", "normal", "high", "high", "urgent"]);
    const assignee: TeamKey | null = status === "open" && chance(0.45) ? null : pick(agents);
    const isBilling = g.tags.includes("billing");
    const t = newTicket(ws, {
      subject: g.subject,
      contact: contactRef(c),
      channel: chance(0.12) ? "chat" : chance(0.05) ? "portal" : "email",
      priority,
      mailbox_id: isBilling ? billing.id : support.id,
      assignee: assignee ? userRef(uid(assignee)) : null,
      team_id: isBilling ? teamId("Billing") : chance(0.7) ? teamId("Support") : null,
      tags: ensureTags(ws, g.tags.map(labelName), false),
      created_at: ago(createdMin * MIN),
    });
    seedActivities(ws, t, assignee ? uid(assignee) : null, t.tags.map((x) => x.name));
    const msgs: { dir: "inbound" | "outbound"; note?: boolean; html: string }[] = [{ dir: "inbound", html: textToHtml(`Hi,\n\n${g.body}\n\nThanks,\n${c.name.split(" ")[0]}`) }];
    const replier = assignee ?? "sam";
    const agentHtml = () => textToHtml(`Hi ${c.name.split(" ")[0]},\n\n${pick(AGENT_REPLIES)}\n\n${TEAM.find((x) => x.key === replier)!.name.split(" ")[0]}`);
    if (status === "open") {
      if (chance(0.5)) {
        msgs.push({ dir: "outbound", html: agentHtml() });
        msgs.push({ dir: "inbound", html: textToHtml(pick(FOLLOW_UPS)) });
      }
    } else {
      msgs.push({ dir: "outbound", html: agentHtml() });
      if (status === "solved" || status === "closed") {
        if (chance(0.6)) msgs.push({ dir: "inbound", html: textToHtml(pick(["Thanks, that worked!", "Great, appreciate the quick reply.", "All sorted now, thank you."])) });
      }
    }
    if (chance(0.25)) msgs.splice(1, 0, { dir: "outbound", note: true, html: `<p>${pick(["Looks related to the sync issue from last week.", "Customer is on the Business plan, prioritize.", "Checked the logs, nothing unusual on our side yet.", "Same as #1018, linking."])}</p>` });
    const span = Math.max(createdMin - (openish ? int(5, Math.min(createdMin - 1, 2 * 24 * 60)) : int(60, 24 * 60)), 1);
    msgs.forEach((m, j) => {
      const at = ago((createdMin - (span * j) / Math.max(msgs.length - 1, 1)) * MIN);
      addMessage(
        ws,
        t,
        { kind: m.note ? "note" : "reply", direction: m.dir, html: m.html, user: m.dir === "outbound" ? userRef(uid(replier)) : null, at },
        { publish: false },
      );
    });
    finalize(ws, t, status, status === "solved" && chance(0.5) ? { score: pick([5, 5, 4, 4, 3, 2]), comment: pick(["", "Fast and friendly.", "Took a while but solved.", ""]) } : undefined);
    if (chance(0.22) && t.priority !== "urgent") t.sla = null;
  });

  // Work for the private teams, Partnerships and the second brand.
  const extra: {
    subject: string;
    contact: number;
    to: string;
    mailbox: Mailbox;
    team: Parameters<typeof teamId>[0];
    status: TicketStatus;
    priority: Priority;
    assignee: TeamKey | null;
    hours: number;
    body: string;
    channel?: "email" | "chat";
    widget?: Widget;
    page?: string;
    topic?: string;
  }[] = [
    { subject: "GDPR data access request", contact: 17, to: "privacy@margin.app", mailbox: legal, team: "Legal", status: "open", priority: "normal", assignee: null, hours: 5, body: "Under Article 15 of the GDPR I'd like a copy of all personal data you hold about me, including notes your team wrote." },
    { subject: "Subprocessor list for our vendor review", contact: 1, to: "legal@margin.app", mailbox: legal, team: "Legal", status: "pending", priority: "normal", assignee: "lea", hours: 30, body: "Our procurement team needs your current list of subprocessors and where each one stores data." },
    { subject: "Please delete my data under GDPR", contact: 22, to: "support@margin.app", mailbox: support, team: "Legal", status: "open", priority: "high", assignee: null, hours: 2, body: "I closed my account last month. Please confirm you have deleted my data, under my right to erasure." },
    { subject: "Responsible disclosure: stored XSS in shared note titles", contact: 26, to: "security@margin.app", mailbox: security, team: "Security", status: "open", priority: "urgent", assignee: "jonas", hours: 3, body: "A note title with an SVG payload runs script when the shared link is opened. Proof of concept attached on request. I'll wait 90 days before publishing." },
    { subject: "Open redirect on the sign-in page", contact: 29, to: "support@margin.app", mailbox: support, team: "Security", status: "open", priority: "high", assignee: null, hours: 9, body: "The next= parameter on /login accepts any domain. Similar to CVE-2024-1234 in other apps. Happy to share details privately." },
    { subject: "Found an API key in a docs example", contact: 33, to: "", mailbox: support, team: "Security", status: "pending", priority: "high", assignee: "priya", hours: 20, body: "The webhook guide shows a key that looks real. You may want to rotate it.", channel: "chat", widget: docsWidget, page: "https://docs.margin.app/guides/webhooks", topic: docsTopics[1].id },
    { subject: "Reseller agreement for the Nordics", contact: 9, to: "partners@margin.app", mailbox: partners, team: "Partnerships", status: "open", priority: "normal", assignee: "sam", hours: 26, body: "We'd like to resell Margin to our customers in Norway and Sweden. Who should we talk to about terms?" },
    { subject: "Co-marketing webinar in November", contact: 4, to: "support@margin.app", mailbox: support, team: "Partnerships", status: "pending", priority: "low", assignee: "sam", hours: 50, body: "Paperplane would love to run a joint webinar about note-taking for startups. Interested?" },
    { subject: "Newsletter scheduled for 9:00 went out at 10:00", contact: 14, to: "hello@pressroom.app", mailbox: pressroom, team: "Support", status: "open", priority: "high", assignee: null, hours: 4, body: "My Tuesday issue was scheduled for 9:00 Berlin time but subscribers got it an hour late." },
    { subject: "Custom domain for my publication", contact: 18, to: "", mailbox: pressroom, team: "Support", status: "open", priority: "normal", assignee: "priya", hours: 7, body: "I added the CNAME record two days ago but the domain still shows as pending.", channel: "chat", widget: pressWidget, page: "https://app.pressroom.app/settings/domain" },
    { subject: "Import subscribers from Substack", contact: 21, to: "support@pressroom.app", mailbox: pressroom, team: "Support", status: "pending", priority: "normal", assignee: "sam", hours: 28, body: "I exported my list from Substack as CSV. The import says 0 subscribers found." },
    { subject: "Refund for a paid subscriber", contact: 24, to: "hello@pressroom.app", mailbox: pressroom, team: "Billing", status: "open", priority: "normal", assignee: null, hours: 11, body: "A reader paid twice for an annual subscription. How do I refund one of the charges?" },
    { subject: "Pricing question", contact: 27, to: "", mailbox: support, team: "Billing", status: "open", priority: "normal", assignee: null, hours: 1, body: "Is the Business plan billed per member or per workspace?", channel: "chat", widget: marketingSite, page: "https://margin.app/pricing", topic: marketingTopics[0].id },
  ];
  const extraMeta = new Map<string, (typeof extra)[number]>();
  for (const x of extra) {
    const c = ws.contacts[x.contact % ws.contacts.length];
    const t = newTicket(ws, {
      subject: x.subject,
      contact: contactRef(c),
      channel: x.channel ?? "email",
      priority: x.priority,
      mailbox_id: x.mailbox.id,
      assignee: x.assignee ? userRef(uid(x.assignee)) : null,
      team_id: teamId(x.team),
      created_at: ago(x.hours * HOUR),
    });
    extraMeta.set(t.id, x);
    if (x.assignee) addActivity(ws, t, "assigned", { from: null, to: userRef(uid(x.assignee)) }, null, iso(Date.parse(t.created_at) + 60_000), false);
    addMessage(ws, t, { kind: "reply", direction: "inbound", html: textToHtml(`Hi,\n\n${x.body}\n\n${c.name.split(" ")[0]}`), at: t.created_at }, { publish: false });
    if (x.status === "pending" && x.assignee) {
      const who = TEAM.find((p) => p.key === x.assignee)!.name.split(" ")[0];
      addMessage(ws, t, { kind: "reply", direction: "outbound", html: textToHtml(`Hi ${c.name.split(" ")[0]},\n\nThanks, I'm on it and will get back to you shortly.\n\n${who}`), user: userRef(uid(x.assignee)), at: iso(Date.parse(t.created_at) + 40 * MIN) }, { publish: false });
    }
    finalize(ws, t, x.status);
  }

  // Make one generated open, unassigned, high ticket close to its first-response deadline.
  const dueSoon = ws.tickets.find((t) => t.status === "open" && !t.assignee && t.number < 1061 && !t.first_responded_at);
  if (dueSoon) {
    dueSoon.priority = "high";
    const created = NOW - 100 * MIN;
    dueSoon.created_at = iso(created);
    for (const m of ws.messages.filter((x) => x.ticket_id === dueSoon.id)) m.created_at = iso(created);
    dueSoon.last_message_at = iso(created);
    dueSoon.last_customer_message_at = iso(created);
    dueSoon.waiting_since = iso(created);
    dueSoon.sla = computeSla(ws, dueSoon, false);
  }

  // Sender checks: a website contact form, and a reply that failed its domain's authentication.
  {
    const visitor = ws.contacts.find((c, i) => i > 12 && !c.company) ?? ws.contacts[13];
    const form = newTicket(ws, {
      subject: "Question about team pricing",
      contact: contactRef(visitor),
      channel: "email",
      priority: "normal",
      mailbox_id: support.id,
      assignee: null,
      team_id: teamId("Support"),
      tags: [],
      created_at: ago(35 * MIN),
    });
    addMessage(
      ws,
      form,
      {
        kind: "reply",
        direction: "inbound",
        form_sender: "website@margin.app",
        html: textToHtml(`Name: ${visitor.name}\nTopic: Pricing\n\nWe're a team of 14 writers. Do you offer a discount for nonprofits, and can we pay yearly by invoice?`),
        at: ago(35 * MIN),
      },
      { publish: false },
    );
    finalize(ws, form, "open");

    const payer = ws.contacts[2];
    const domain = payer.email?.split("@")[1] ?? "example.com";
    const bank = newTicket(ws, {
      subject: "Invoice copy for March",
      contact: contactRef(payer),
      channel: "email",
      priority: "normal",
      mailbox_id: billing.id,
      assignee: userRef(uid("lea")),
      team_id: teamId("Billing"),
      tags: [],
      created_at: ago(3 * DAY),
    });
    seedActivities(ws, bank, uid("lea"), []);
    const first = payer.name.split(" ")[0];
    addMessage(
      ws,
      bank,
      { kind: "reply", direction: "inbound", reply_to: `accounts@${domain}`, html: textToHtml(`Hi,\n\nCould you send us a copy of the March invoice? Our accounts team handles payments, so replies can go to them.\n\n${first}`), at: ago(3 * DAY) },
      { publish: false },
    );
    addMessage(
      ws,
      bank,
      { kind: "reply", direction: "outbound", user: userRef(uid("lea")), html: textToHtml(`Hi ${first},\n\nHere is the March invoice. Let me know if you need anything else.\n\nLea`), at: ago(3 * DAY - 2 * HOUR) },
      { publish: false },
    );
    finalize(ws, bank, "solved");
    addMessage(
      ws,
      bank,
      {
        kind: "reply",
        direction: "inbound",
        auth_failed: true,
        reply_to: "payments@ledger-transfers.net",
        html: textToHtml(`Hi Lea,\n\nOur bank details have changed. Please update them before the next invoice and confirm the change by replying to this email.\n\n${first}`),
        at: ago(50 * MIN),
      },
      { publish: false },
    );
    bank.updated_at = bank.last_message_at;
  }

  // Spam.
  for (const s of SPAM) {
    const t = newTicket(ws, {
      subject: s.subject,
      contact: { id: uuid(), email: s.from, name: s.name, avatar_url: "" },
      channel: "email",
      priority: "low",
      mailbox_id: support.id,
      created_at: ago(int(60, 3 * 24 * 60) * MIN),
    });
    addMessage(ws, t, { kind: "reply", direction: "inbound", html: textToHtml(s.body), at: t.created_at }, { publish: false });
    t.spam = true;
    t.sla = null;
  }

  // Number tickets by creation for everything that isn't a story.
  const storyNumbers = new Set(STORIES.map((s) => s.number));
  let n = 1001;
  for (const t of ws.tickets.filter((x) => !storyNumbers.has(x.number)).sort((a, b) => Date.parse(a.created_at) - Date.parse(b.created_at))) {
    while (storyNumbers.has(n)) n++;
    t.number = n++;
  }
  for (const c of ws.contacts) c.ticket_count = ws.tickets.filter((t) => t.contact?.id === c.id).length;

  // Keep breaches rare and meaningful: a few overdue open tickets, not half the inbox.
  let breachedKept = 0;
  for (const t of ws.tickets) {
    if (!t.sla?.breached || storyNumbers.has(t.number)) continue;
    const openish = t.status === "open" || t.status === "pending" || t.status === "on_hold";
    if (openish && breachedKept < 3) {
      breachedKept++;
      continue;
    }
    if (openish) t.sla = null;
    else t.sla.breached = false;
    ws.activities = ws.activities.filter((a) => !(a.ticket_id === t.id && a.type === "sla_breached"));
  }

  // Ollie triaged each new ticket with Jev: confident decisions applied, the rest left as suggestions.
  for (const t of ws.tickets) {
    if (t.spam) continue;
    const openish = t.status === "open" || t.status === "pending" || t.status === "on_hold";
    if (!openish && !chance(0.4)) continue;
    const text = ws.messages.find((m) => m.ticket_id === t.id && m.direction === "inbound")?.body_html ?? "";
    applyDecisions(
      ws,
      t,
      {
        priority: t.priority,
        severity: severityFor(t.priority, `${t.subject} ${text}`),
        tags: [],
        team_id: t.team_id ? null : teamId("Support") ?? null,
        sentiment: typeof t.custom.ai_sentiment === "string" ? t.custom.ai_sentiment : "neutral",
        category: typeof t.custom.ai_category === "string" ? t.custom.ai_category : "problem",
        language: typeof t.custom.language === "string" ? t.custom.language : "en",
      },
      { via: "auto_triage", at: iso(Date.parse(t.created_at) + 40_000) },
    );
  }

  // Where each ticket came in and how it found its team (contracts/teams.md "Routing").
  const dpa = byNumber(ws, 1063);
  if (dpa) {
    // Maya answered first, then moved it to Legal: the reply address followed the team.
    dpa.team_id = teamId("Legal");
    dpa.assignee = userRef(uid("lea"));
    dpa.mailbox_id = legal.id;
    addActivity(ws, dpa, "team_changed", { from: null, to: "Legal", via: "agent", mailbox_from: support.email, mailbox_to: legal.email }, userRef(uid("maya")), iso(Date.parse(dpa.created_at) + 2 * HOUR + 10 * MIN), false);
  }
  const rulesInOrder = ws.routingRules.filter((r) => r.enabled).sort((a, b) => a.position - b.position);
  const byRule = (t: Ticket, from: string) => {
    const body = (ws.messages.find((m) => m.ticket_id === t.id && m.direction === "inbound")?.body_html ?? "").toLowerCase();
    const domain = from.split("@")[1] ?? "";
    return rulesInOrder.find((r) =>
      r.conditions.some((c) =>
        c.field === "subject" ? t.subject.toLowerCase().includes(c.value.toLowerCase()) : c.field === "body" ? body.includes(c.value.toLowerCase()) : c.field === "from_domain" ? domain === c.value : false,
      ),
    );
  };
  for (const t of ws.tickets) {
    const x = extraMeta.get(t.id);
    const from = t.contact?.email ?? "";
    t.widget_id = null;
    t.page_url = "";
    t.routed = null;
    t.received_at = "";
    t.origin_mailbox_id = null;
    if (t.channel === "chat") {
      const w = x?.widget ?? (chance(0.7) ? marketingSite : docsWidget);
      t.widget_id = w.id;
      t.brand_id = w.brand_id;
      t.page_url = x?.page ?? (w === docsWidget ? pick(["https://docs.margin.app/api/rate-limits", "https://docs.margin.app/guides/sso", "https://docs.margin.app/guides/webhooks"]) : pick(["https://margin.app/pricing", "https://margin.app/", "https://margin.app/features/search"]));
      const tp = w.settings.topics.find((y) => y.id === x?.topic) ?? (t.team_id ? w.settings.topics.find((y) => y.team_id === t.team_id) : undefined);
      t.routed = t.team_id ? (tp ? { via: "topic" } : { via: "widget" }) : null;
      if (tp?.team_id === teamId("Security")) t.routed = { via: "rule", rule_id: ws.routingRules[2].id, rule_name: ws.routingRules[2].name };
      // A chat takes the brand's address for its team when it first needs one.
      t.mailbox_id = ws.mailboxes.find((m) => m.brand_id === t.brand_id && m.team_id === t.team_id)?.id ?? (w.brand_id === pressBrand.id ? pressroom.id : support.id);
      continue;
    }
    const arrived = ws.mailboxes.find((m) => m.id === t.mailbox_id) ?? support;
    t.origin_mailbox_id = arrived.id;
    t.brand_id = arrived.brand_id;
    t.received_at = x?.to || (arrived.aliases.length && chance(0.25) ? arrived.aliases[0] : arrived.email);
    if (t.channel === "api" || t.channel === "portal") {
      t.routed = t.team_id ? { via: "api" } : null;
      continue;
    }
    if (t === dpa) {
      t.routed = { via: "agent", by: userRef(uid("maya"))! };
      continue;
    }
    // Billing mail written to support@ was moved by "Invoices to Billing"; its reply address followed.
    const r = !x && t.team_id === teamId("Billing") && arrived === billing && /invoice|refund/i.test(t.subject) && chance(0.6) ? byRule(t, from) : undefined;
    if (r && r.team_id === t.team_id) {
      t.origin_mailbox_id = support.id;
      t.received_at = support.email;
      t.routed = { via: "rule", rule_id: r.id, rule_name: r.name };
      continue;
    }
    if (x?.team === "Partnerships" && x.assignee === "sam" && arrived === support) {
      t.routed = { via: "agent", by: userRef(uid("sam"))! };
      continue;
    }
    const rr = t.team_id && arrived.team_id !== t.team_id ? byRule(t, from) : undefined;
    if (rr && rr.team_id === t.team_id) {
      t.routed = { via: "rule", rule_id: rr.id, rule_name: rr.name };
      const box = ws.mailboxes.find((m) => m.brand_id === t.brand_id && m.team_id === t.team_id);
      if (box) t.mailbox_id = box.id;
    } else if (t.team_id) t.routed = arrived.team_id === t.team_id ? { via: "address" } : { via: "triage" };
  }

  // Saved replies, views, automations.
  ws.savedReplies = SAVED_REPLIES.map((r, i) => ({ id: uuid(), title: r.title, shortcut: r.shortcut, body_html: r.body, team_id: r.shortcut === "refund" ? teamId("Billing") : null, created_at: ago((100 - i) * DAY), updated_at: ago(int(1, 30) * DAY) }));
  ws.savedReplies.push(
    { id: uuid(), title: "DPA and subprocessors", shortcut: "dpa", body_html: "<p>Our data processing agreement and the current subprocessor list are attached. Sign the DPA and reply with the PDF, and we'll countersign within two working days.</p>", team_id: teamId("Legal"), created_at: ago(50 * DAY), updated_at: ago(6 * DAY) },
    { id: uuid(), title: "Disclosure received", shortcut: "vuln", body_html: "<p>Thanks for the report. We've reproduced it and are working on a fix. We'll let you know when it ships and credit you in our security page if you like.</p>", team_id: teamId("Security"), created_at: ago(40 * DAY), updated_at: ago(12 * DAY) },
    { id: uuid(), title: "Pressroom custom domains", shortcut: "domain", body_html: "<p>DNS changes can take up to 48 hours. Once the CNAME points at <strong>domains.pressroom.app</strong>, the domain switches to active on its own.</p>", team_id: teamId("Support"), created_at: ago(30 * DAY), updated_at: ago(3 * DAY) },
  );
  ws.views = [
    { id: uuid(), name: "Urgent", shared: true, filters: { status: ["open", "pending"], priority: ["urgent", "high"] }, position: 0 },
    { id: uuid(), name: "Billing queue", shared: false, team_id: teamId("Billing"), filters: { status: ["open", "pending", "on_hold"], team_id: teamId("Billing")! }, position: 1 },
    { id: uuid(), name: "Waiting on engineering", shared: false, filters: { status: ["on_hold", "pending"], tag: "Bug" }, position: 2 },
    { id: uuid(), name: "Legal requests due", shared: false, team_id: teamId("Legal"), filters: { status: ["open", "pending"], team_id: teamId("Legal")! }, position: 3 },
  ];
  const tagName = (n: string) => n;
  const autos: Automation[] = [
    {
      id: uuid(),
      name: "Route billing mail to the Billing team",
      trigger: "ticket_created",
      conditions: [{ field: "mailbox_id", op: "equals", value: billing.id }],
      actions: [{ type: "assign_team", value: teamId("Billing")! }, { type: "add_tag", value: tagName("Billing") }],
      enabled: true,
      position: 0,
      run_count: 412,
    },
    {
      id: uuid(),
      name: "Escalate SSO outages",
      trigger: "ticket_created",
      conditions: [{ field: "subject", op: "contains", value: "SSO" }, { field: "body", op: "contains", value: "can't sign in" }],
      actions: [{ type: "set_priority", value: "urgent" }, { type: "add_tag", value: "SSO" }, { type: "assign_user", value: uid("jonas") }],
      enabled: true,
      position: 1,
      run_count: 37,
    },
    {
      id: uuid(),
      name: "Reopen on customer reply",
      trigger: "customer_replied",
      conditions: [{ field: "status", op: "equals", value: "pending" }],
      actions: [{ type: "set_status", value: "open" }],
      enabled: true,
      position: 2,
      run_count: 1290,
    },
    {
      id: uuid(),
      name: "Mark gift card mail as spam",
      trigger: "ticket_created",
      conditions: [{ field: "subject", op: "contains", value: "gift card" }],
      actions: [{ type: "mark_spam", value: "true" }],
      enabled: false,
      position: 3,
      run_count: 8,
    },
  ];
  ws.automations = autos;

  // Knowledge base.
  // Most of it is shared by every brand; Pressroom has its own Publishing collection and one article of its own.
  const brandOf = (slug?: string) => (slug ? (ws.brands.find((b) => b.slug === slug)?.id ?? null) : null);
  ws.collections = COLLECTIONS.map((c, i): KbCollection => ({ id: uuid(), name: c.name, slug: c.slug, description: c.description, position: i, article_count: 0, brand_id: brandOf(c.brand) }));
  ws.articles = ARTICLES.map((a, i): KbArticle => {
    const col = ws.collections[a.collection];
    col.article_count++;
    const updated = ago(int(1, 60) * DAY);
    return {
      id: uuid(),
      brand_id: brandOf(a.brand),
      collection_id: col.id,
      title: a.title,
      slug: slugify(a.title),
      excerpt: a.excerpt,
      body_html: a.body,
      status: a.status,
      author: userRef(uid(pick(["maya", "jonas", "priya"] as const))),
      views: a.views,
      published_at: a.status === "published" ? ago((90 - i * 4) * DAY) : null,
      updated_at: updated,
    };
  });

  // API keys and webhooks.
  const keys: ApiKey[] = [
    { id: uuid(), name: "Zapier", prefix: "od_live_Zq4kP2mA", scopes: ["tickets:read", "tickets:write", "contacts:read"], last_used_at: ago(3 * MIN), expires_at: null, created_at: ago(120 * DAY), created_by: userRef(uid("maya")) },
    { id: uuid(), name: "Data warehouse export", prefix: "od_live_8sTnW7cE", scopes: ["tickets:read", "contacts:read", "kb:read"], last_used_at: ago(9 * HOUR), expires_at: iso(NOW + 60 * DAY), created_at: ago(45 * DAY), created_by: userRef(uid("jonas")), private_team_ids: [] },
    { id: uuid(), name: "Legal hold archive", prefix: "od_live_Lg2Hq9Xa", scopes: ["tickets:read"], last_used_at: ago(2 * DAY), expires_at: null, created_at: ago(20 * DAY), created_by: userRef(uid("jonas")), private_team_ids: [teamId("Legal")!], private_teams: [{ id: teamId("Legal")!, name: "Legal", granted_by: userRef(uid("jonas")) }] },
  ];
  keys[0].private_team_ids = [];
  ws.apiKeys = keys;
  const hooks: Webhook[] = [
    { id: uuid(), url: "https://hooks.margin.app/opendesk", description: "Internal event bus", events: ["ticket.created", "ticket.updated", "message.created"], enabled: true, created_at: ago(100 * DAY),
      private_team_ids: [teamId("Security")!], private_teams: [{ id: teamId("Security")!, name: "Security", granted_by: userRef(uid("priya")) }] },
    { id: uuid(), url: "https://hooks.slack.com/services/T0001/B0002/XXXX", description: "Urgent tickets to #support-alerts", events: ["ticket.created"], enabled: true, created_at: ago(30 * DAY) },
  ];
  ws.webhooks = hooks;
  for (const h of hooks) {
    const list: WebhookDelivery[] = [];
    for (let i = 0; i < (h === hooks[0] ? 14 : 8); i++) {
      const failed = chance(0.2);
      list.push({
        id: uuid(),
        event_id: uuid(),
        event_type: pick(h.events),
        status: i === 0 && h === hooks[1] ? "pending" : failed ? "failed" : "succeeded",
        attempts: failed ? int(2, 6) : 1,
        response_code: failed ? pick([500, 502, 504, 410]) : 200,
        duration_ms: int(40, 900),
        created_at: ago((i * 47 + int(1, 30)) * MIN),
      });
    }
    ws.deliveries[h.id] = list;
  }

  // Audit log.
  const actions: [string, string, () => Record<string, unknown>][] = [
    ["auth.login", "user", () => ({})],
    ["auth.login_failed", "user", () => ({ reason: "invalid_credentials" })],
    ["member.invited", "invitation", () => ({ email: "rafael@margin.app", role: "agent" })],
    ["member.role_changed", "member", () => ({ from: "agent", to: "admin" })],
    ["api_key.created", "api_key", () => ({ name: "Zapier" })],
    ["webhook.created", "webhook", () => ({ url: "https://hooks.margin.app/opendesk" })],
    ["mailbox.connected", "mailbox", () => ({ email: "support@margin.app", kind: "google" })],
    ["mailbox.disconnected", "mailbox", () => ({ email: "sales@margin.app" })],
    ["auth.totp_enabled", "user", () => ({})],
    ["auth.password_changed", "user", () => ({})],
    ["workspace.updated", "workspace", () => ({ fields: ["settings.csat_enabled"] })],
    ["automation.updated", "automation", () => ({ name: "Escalate SSO outages" })],
    ["sla_policy.created", "sla_policy", () => ({ name: "Urgent" })],
    ["member.removed", "member", () => ({ email: "alex@margin.app" })],
  ];
  const ips = ["84.152.33.17", "62.214.8.201", "91.64.120.5", "185.12.44.9", "2a02:8109:9a40::1c"];
  const audit: AuditLog[] = [];
  for (let i = 0; i < 42; i++) {
    const [action, entity, data] = pick(actions);
    const actorUser = pick(["maya", "jonas", "maya", "lea", "priya"] as const);
    const viaKey = action.startsWith("ticket") ? keys[0].id : null;
    audit.push({
      id: uuid(),
      actor: viaKey ? { type: "api_key", user: null, api_key_id: viaKey } : i % 13 === 7 ? { type: "system", user: null, api_key_id: null } : { type: "user", user: userRef(uid(actorUser)), api_key_id: null },
      action,
      entity_type: entity,
      entity_id: uuid(),
      data: data(),
      ip: pick(ips),
      created_at: ago((i * 173 + int(1, 120)) * MIN),
    });
  }
  ws.audit = audit;

  // Notifications for Maya.
  const byNum = (n: number) => ws.tickets.find((t) => t.number === n)!;
  const nref = (t: Ticket) => ({ id: t.id, number: t.number, subject: t.subject });
  const jonas = userRef(uid("jonas"));
  ws.notifications = [
    { id: uuid(), type: "customer_replied", ticket: nref(byNum(1072)), data: { contact: { name: "Dana Whitfield" } }, read_at: null, created_at: ago(6 * MIN) },
    { id: uuid(), type: "mentioned", ticket: nref(byNum(1072)), data: { actor: jonas }, read_at: null, created_at: ago(61 * MIN) },
    { id: uuid(), type: "customer_replied", ticket: nref(byNum(1068)), data: { contact: { name: "Tomás García" } }, read_at: null, created_at: ago(35 * MIN) },
    { id: uuid(), type: "sla_breached", ticket: nref(byNum(1067)), data: { kind: "first_response" }, read_at: null, created_at: ago(2 * HOUR) },
    // Jonas (an admin) joined Partnerships, which Maya co-leads.
    { id: uuid(), type: "team_joined", ticket: null, data: { team_id: teamId("Partnerships"), team: { id: teamId("Partnerships"), name: "Partnerships", private: false }, user: userRef(uid("jonas")), by: uid("jonas") }, read_at: null, created_at: ago(3 * HOUR) },
    { id: uuid(), type: "assigned", ticket: nref(byNum(1062)), data: { actor: jonas }, read_at: ago(8 * HOUR), created_at: ago(9 * HOUR) },
    { id: uuid(), type: "assigned", ticket: nref(byNum(1068)), data: { actor: null }, read_at: ago(4 * HOUR), created_at: ago(5 * HOUR) },
    { id: uuid(), type: "mentioned", ticket: nref(byNum(1070)), data: { actor: userRef(uid("lea")) }, read_at: ago(20 * HOUR), created_at: ago(25 * HOUR) },
    { id: uuid(), type: "customer_replied", ticket: nref(byNum(1065)), data: { contact: { name: "Hugo Moreau" } }, read_at: ago(60 * HOUR), created_at: ago(68 * HOUR) },
  ];

  ws.csat.demo = { ticket_id: byNum(1066).id, score: null, comment: "" };

  // Teammates present on tickets.
  const meta = (k: TeamKey, state: Meta["state"]): Omit<Meta, "phx_ref" | "online_at"> => ({ user_id: uid(k), name: TEAM.find((x) => x.key === k)!.name, avatar: "", state });
  const unassigned = ws.tickets
    .filter((t) => !t.spam && !t.assignee && ["open", "pending", "on_hold"].includes(t.status))
    .sort((a, b) => Date.parse(b.last_message_at) - Date.parse(a.last_message_at));
  const place = (t: Ticket | undefined, m: Omit<Meta, "phx_ref" | "online_at">) => {
    if (!t) return;
    const list = teammatePresence.get(t.id) ?? [];
    list.push({ ...m, online_at: ago(int(1, 20) * MIN), phx_ref: `seed${list.length}${m.user_id.slice(0, 4)}` });
    teammatePresence.set(t.id, list);
  };
  place(unassigned[1], meta("jonas", "viewing"));
  place(byNum(1072), meta("priya", "replying"));
  place(unassigned[3], meta("sam", "viewing"));
  place(byNum(1068), meta("lea", "viewing"));

  seedTicketTypes(ws);
  seedMarginFields(ws, { maya: uid("maya"), jonas: uid("jonas"), lea: uid("lea"), priya: uid("priya") });
  return ws;
}

const byNumber = (ws: WsData, n: number) => ws.tickets.find((t) => t.number === n);

/** A one-brand workspace without teams: every ticket arrived at its mailbox. */
function placeSimple(ws: WsData) {
  const brandId = ws.brands[0]?.id ?? "";
  for (const t of ws.tickets) {
    const box = ws.mailboxes.find((m) => m.id === t.mailbox_id);
    t.brand_id = brandId;
    t.widget_id = null;
    t.origin_mailbox_id = box?.id ?? null;
    t.received_at = box?.email ?? "";
    t.page_url = "";
    t.routed = null;
  }
}

function seedActivities(ws: WsData, t: Ticket, assigneeId: string | null, tags: string[]) {
  const created = Date.parse(t.created_at);
  for (const tag of tags) addActivity(ws, t, "tagged", { tag }, null, iso(created + 30_000), false);
  if (assigneeId) addActivity(ws, t, "assigned", { from: null, to: userRef(assigneeId) }, null, iso(created + 60_000), false);
}

function finalize(ws: WsData, t: Ticket, status: TicketStatus, csat?: { score: number; comment: string }) {
  const lastAt = Date.parse(t.last_message_at);
  if (status !== "open") {
    addActivity(ws, t, "status_changed", { from: "open", to: status }, t.assignee, iso(lastAt + 60_000), false);
  }
  t.status = status;
  if (status === "solved" || status === "closed") t.solved_at = iso(lastAt + 60_000);
  if (t.priority === "urgent" && t.number % 2 === 0) {
    addActivity(ws, t, "priority_changed", { from: "high", to: "urgent" }, t.assignee, iso(Date.parse(t.created_at) + 5 * MIN), false);
  }
  t.sla = computeSla(ws, t, false);
  if (t.sla?.breached && status === "open") {
    const due = t.first_responded_at ? t.sla.resolution_due_at : t.sla.first_response_due_at;
    addActivity(ws, t, "sla_breached", { kind: t.first_responded_at ? "resolution" : "first_response" }, null, due ?? t.created_at, false);
  }
  if (csat) {
    t.csat = csat;
    addActivity(ws, t, "csat_received", { score: csat.score, comment: csat.comment }, null, iso(lastAt + 3 * HOUR), false);
  }
  t.updated_at = t.last_message_at;
  t.last_message_preview = t.last_message_preview || preview(ws.messages.find((m) => m.ticket_id === t.id)?.body_html ?? "");
}

function seedLumen(): WsData {
  const ws = baseWorkspace(uuid(), "Lumen Labs", "lumen");
  ws.workspace.settings = { timezone: "America/New_York", help_center_enabled: false, csat_enabled: false, auto_assign: "none", locale: "de" };
  ws.members = [
    { user_id: db.users[0].id, role: "agent", created_at: ago(60 * DAY) },
    { user_id: db.users[3].id, role: "owner", created_at: ago(200 * DAY) },
  ];
  ws.sla = [{ id: uuid(), name: "Default", priority: null, first_response_minutes: 240, resolution_minutes: 2880, business_hours: false, enabled: true, position: 0 }];
  seedDefaultLabels(ws);
  ws.mailboxes = [
    {
      id: uuid(),
      name: "Hello",
      email: "hello@lumenlabs.io",
      display_name: "Lumen Labs",
      inbound_kind: "forwarding",
      outbound_kind: "system",
      forwarding_address: "lumen-hello-a1b2@in.opendesk.sh",
      config: {},
      team_id: null,
      brand_id: "",
      aliases: [],
      from_name_format: "{agent} at {mailbox}",
      private: false,
      signature_html: "",
      auto_reply_enabled: false,
      auto_reply_subject: "",
      auto_reply_html: "",
      ai_answers: false,
      ai_answers_mode: "draft",
      ai_answers_outside_hours: false,
      status: "active",
      last_error: "",
      last_synced_at: ago(3 * MIN),
      created_at: ago(200 * DAY),
    },
  ];
  const names = ["Avery Stone", "Jordan Blake", "Riley Chen", "Morgan Diaz"];
  ws.contacts = names.map((name) => ({
    id: uuid(),
    email: `${name.split(" ")[0].toLowerCase()}@example.com`,
    name,
    phone: "",
    avatar_url: null,
    avatar_source: null,
    company: null,
    external_id: null,
    notes: "",
    custom: {},
    tags: [],
    fields: {},
    blocked: false,
    ticket_count: 1,
    created_at: ago(30 * DAY),
    updated_at: ago(3 * DAY),
  }));
  const subjects = ["Lamp firmware update stuck at 80%", "Where can I buy replacement bulbs?", "App can't find my lamp", "Order #4410 hasn't shipped"];
  subjects.forEach((subject, i) => {
    const t = newTicket(ws, { subject, contact: contactRef(ws.contacts[i]), channel: "email", created_at: ago((i + 1) * 7 * HOUR), mailbox_id: ws.mailboxes[0].id });
    t.number = 201 + i;
    addMessage(ws, t, { kind: "reply", direction: "inbound", html: textToHtml(`Hello,\n\n${subject}. Can you help?`), at: t.created_at }, { publish: false });
    if (i === 3) t.status = "pending";
  });
  seedDefaultBrand(ws);
  placeSimple(ws);
  return ws;
}

/** The email of the second seeded account: the same person, signed in with another address (multi-account). */
export const SECOND_ACCOUNT_EMAIL = "maya@harbor.studio";

/** A second account for Maya, owner of a small side workspace, so the account switcher has something to group. */
function seedHarbor(): WsData {
  const u: User = {
    id: uuid(),
    email: SECOND_ACCOUNT_EMAIL,
    name: "Maya Chen",
    avatar_url: null,
    avatar_source: null,
    email_verified: true,
    totp_enabled: false,
    created_at: ago(90 * DAY),
  };
  db.users.push(u);
  const ws = baseWorkspace(uuid(), "Harbor Studio", "harbor");
  ws.workspace.settings = { timezone: "Europe/Lisbon", help_center_enabled: false, csat_enabled: false, auto_assign: "none" };
  ws.members = [{ user_id: u.id, role: "owner", created_at: ago(90 * DAY), availability: "online" }];
  ws.sla = [{ id: uuid(), name: "Default", priority: null, first_response_minutes: 480, resolution_minutes: 4320, business_hours: false, enabled: true, position: 0 }];
  seedDefaultLabels(ws);
  const names = ["Inês Duarte", "Felix Hartmann", "Grace Okoye"];
  ws.contacts = names.map((name) => ({
    id: uuid(),
    email: `${name.split(" ")[0].normalize("NFD").replace(/[\u0300-\u036f]/g, "").toLowerCase()}@example.com`,
    name,
    phone: "",
    avatar_url: null,
    avatar_source: null,
    company: null,
    external_id: null,
    notes: "",
    custom: {},
    tags: [],
    fields: {},
    blocked: false,
    ticket_count: 1,
    created_at: ago(20 * DAY),
    updated_at: ago(2 * DAY),
  }));
  const subjects = ["Quote for the spring catalogue", "Can we move Thursday's shoot?", "Invoice 0042 has the old address"];
  subjects.forEach((subject, i) => {
    const t = newTicket(ws, { subject, contact: contactRef(ws.contacts[i]), channel: "email", created_at: ago((i + 1) * 5 * HOUR) });
    t.number = 41 + i;
    addMessage(ws, t, { kind: "reply", direction: "inbound", html: textToHtml(`Hi Maya,\n\n${subject}. Let me know.`), at: t.created_at }, { publish: false });
  });
  seedDefaultBrand(ws);
  placeSimple(ws);
  return ws;
}

export function seed() {
  reseed(0x2f6e2b1);
  seedUsers();
  db.notificationSettings = {};
  db.workspaces = [seedMargin(), seedLumen()];
  db.workspaces.push(seedHarbor());
}
