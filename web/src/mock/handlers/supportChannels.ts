import type { Condition, SupportChannelConnection, SupportChannelDelivery, SupportChannelDestination, SupportChannelMirror, SupportChannelOllie, SupportChannelOllieAction, SupportChannelOllieLength, SupportChannelOperations, SupportChannelProvider } from "@/lib/api/types";
import { del, get, HttpError, invalid, notFound, paginate, patch, post, put, respond } from "../router";
import { nowIso, uuid } from "../util";

const providers: SupportChannelProvider[] = [
  {
    key: "slack",
    name: "Slack",
    description: "Mirror email tickets into Slack threads and work them by talking to Ollie.",
    configures: false,
    fields: [
      { name: "bot_token", label: "Bot token", secret: true },
      { name: "signing_secret", label: "Signing secret", secret: true },
    ],
  },
  {
    key: "discord",
    name: "Discord",
    description: "Mirror email tickets into Discord threads and work them by talking to Ollie.",
    configures: true,
    fields: [
      { name: "bot_token", label: "Bot token", secret: true },
      { name: "public_key", label: "Interactions public key", secret: true },
      { name: "guild_id", label: "Server", secret: false, discovered: true },
    ],
  },
  {
    key: "telegram",
    name: "Telegram",
    description: "Mirror email tickets into Telegram forum topics and work them by talking to Ollie.",
    configures: true,
    fields: [
      { name: "bot_token", label: "Bot token", secret: true },
      { name: "webhook_secret", label: "Webhook secret", secret: true, generated: true },
    ],
  },
  {
    key: "google_chat",
    name: "Google Chat",
    description: "Mirror email tickets into Google Chat spaces and work them by talking to Ollie.",
    configures: false,
    fields: [{ name: "service_account_json", label: "Service account JSON", secret: true }],
  },
];

const connections: SupportChannelConnection[] = [];

// What each demo bot can see: Slack and Discord list channels, while a new
// Telegram bot hears of its groups only once it is added to one.
const discovered: Record<string, { id: string; name: string; kind: string; available: boolean }[]> = {
  slack: [
    { id: "C07SUPPORT", name: "support", kind: "channel", available: true },
    { id: "C07BILLING", name: "billing", kind: "channel", available: true },
  ],
  discord: [
    { id: "1290000000000000001", name: "support", kind: "text", available: true },
    { id: "1290000000000000002", name: "escalations", kind: "text", available: true },
  ],
  telegram: [],
  google_chat: [{ id: "spaces/AAAAsupport", name: "Support", kind: "space", available: true }],
};

const health = (provider: string) =>
  provider === "slack" || provider === "google_chat"
    ? { health_status: "action_required" as const, health_detail: provider === "slack" ? "Credentials work. Verify the Events API, interactivity and /opendesk request URLs in Slack." : "Set the Chat app's connection URL and authentication audience to the webhook address." }
    : { health_status: "healthy" as const, health_detail: provider === "discord" ? "Discord's interactions endpoint and commands are configured." : "Telegram's webhook is configured and accepting updates." };
const destinations: SupportChannelDestination[] = [];
const ollieActions: SupportChannelOllieAction[] = ["reply", "note", "assign_self", "status", "priority", "create_ticket"];
const ollieLengths: SupportChannelOllieLength[] = ["brief", "standard", "detailed"];
const ollie: SupportChannelOllie = { actions: [...ollieActions] };

// Applies a destination's Ollie settings as the API does.
function applyOllie(row: SupportChannelDestination, body: Record<string, unknown>) {
  if (typeof body.ollie_summary === "boolean") row.ollie_summary = body.ollie_summary;
  if (typeof body.ollie_warnings === "boolean") row.ollie_warnings = body.ollie_warnings;
  if (body.ollie_personality !== undefined) {
    const personality = String(body.ollie_personality).trim();
    if (/[<>]/.test(personality)) throw invalid("The personality can't contain < or >.");
    if (personality.length > 500) throw invalid("The personality must be 500 characters or less.");
    row.ollie_personality = personality;
  }
  if (body.ollie_length !== undefined) {
    const length = ollieLengths.find((value) => value === body.ollie_length);
    if (!length) throw invalid("Response length must be brief, standard or detailed.");
    row.ollie_length = length;
  }
}
const mirrors: SupportChannelMirror[] = ["customer_replies", "agent_replies", "status_changes", "assignee_changes", "priority_changes"];
const destinationFields = ["subject", "from_email", "received_at", "mailbox_id", "brand_id", "severity", "sentiment", "tag", "time"];

// Applies a destination's routing as the API does.
function applyRouting(row: SupportChannelDestination, body: Record<string, unknown>) {
  if (body.mode !== undefined) {
    if (body.mode !== "route" && body.mode !== "copy") throw invalid("Mode must be route or copy.");
    row.mode = body.mode;
  }
  if (body.conditions !== undefined) {
    const conditions = Array.isArray(body.conditions) ? (body.conditions as Condition[]) : [];
    if (conditions.length > 10) throw invalid("A destination can have at most 10 conditions.");
    for (const c of conditions) {
      if (!destinationFields.includes(c.field) && !c.field.startsWith("field.") && !c.field.startsWith("contact.field.")) {
        throw invalid(`Condition field must be one of ${destinationFields.join(", ")}, field.<key> or contact.field.<key>.`);
      }
    }
    row.conditions = conditions.map((c) => ({ field: c.field, op: c.op, value: c.value }));
  }
  if (body.mirror !== undefined) {
    const list = Array.isArray(body.mirror) ? (body.mirror as string[]) : [];
    if (list.some((m) => !mirrors.includes(m as SupportChannelMirror))) throw invalid(`mirror takes only ${mirrors.join(", ")}.`);
    row.mirror = mirrors.filter((m) => list.includes(m));
  }
  if (typeof body.include_notes === "boolean") row.include_notes = body.include_notes;
}

// Only one of two routes with the same team, priority and conditions could ever post.
function sameRoute(a: SupportChannelDestination, b: SupportChannelDestination) {
  return a.id !== b.id && a.mode === "route" && b.mode === "route" && a.connection_id === b.connection_id && a.team_id === b.team_id && a.priority === b.priority && JSON.stringify(a.conditions) === JSON.stringify(b.conditions);
}
function checkRoute(row: SupportChannelDestination) {
  if (destinations.some((other) => sameRoute(row, other))) throw new HttpError(409, "conflict", "Another route on this connection already has that team, priority and conditions.");
}
const deliveries: SupportChannelDelivery[] = [
  { event_seq: 1842, event_type: "ticket.created", ticket_number: 284, status: "queued", attempts: 0, last_error: "", created_at: new Date(Date.now() - 2 * 60_000).toISOString(), updated_at: new Date(Date.now() - 2 * 60_000).toISOString() },
  { event_seq: 1841, event_type: "message.created", ticket_number: 283, status: "retrying", attempts: 2, last_error: "", created_at: new Date(Date.now() - 8 * 60_000).toISOString(), updated_at: new Date(Date.now() - 45_000).toISOString() },
  { event_seq: 1840, event_type: "ticket.updated", ticket_number: 282, status: "failed", attempts: 8, last_error: "Couldn't deliver to the provider.", created_at: new Date(Date.now() - 23 * 60_000).toISOString(), updated_at: new Date(Date.now() - 10 * 60_000).toISOString() },
  { event_seq: 1839, event_type: "ticket.created", ticket_number: 281, status: "delivered", attempts: 1, last_error: "", created_at: new Date(Date.now() - 36 * 60_000).toISOString(), updated_at: new Date(Date.now() - 35 * 60_000).toISOString(), delivered_at: new Date(Date.now() - 35 * 60_000).toISOString() },
];

export function registerSupportChannels() {
  get("/v1/support-channels/providers", () => ({ data: providers }));
  get("/v1/support-channels/connections", () => ({ data: connections }));
  get("/v1/support-channels/destinations", () => ({ data: destinations }));
  get("/v1/support-channels/deliveries", (ctx) => paginate(ctx, deliveries));
  get("/v1/support-channels/operations", () => {
    const delivered = deliveries.filter((row) => row.status === "delivered");
    const latencies = delivered.map((row) => Math.max(0, Date.parse(row.delivered_at ?? row.updated_at) - Date.parse(row.created_at)));
    return {
      window_started_at: new Date(Date.now() - 24 * 60 * 60_000).toISOString(),
      generated_at: nowIso(),
      connections: {
        total: connections.length,
        healthy: connections.filter((row) => row.health_status === "healthy").length,
        unhealthy: connections.filter((row) => row.health_status === "action_required" || row.health_status === "error").length,
        alerted: connections.filter((row) => !!row.health_alerted_at).length,
      },
      deliveries: {
        queued: deliveries.filter((row) => row.status === "queued").length,
        retrying: deliveries.filter((row) => row.status === "retrying").length,
        delivered: delivered.length,
        failed: deliveries.filter((row) => row.status === "failed").length,
        average_latency_ms: latencies.length ? Math.round(latencies.reduce((sum, value) => sum + value, 0) / latencies.length) : 0,
        maximum_latency_ms: latencies.length ? Math.max(...latencies) : 0,
        oldest_pending_at: deliveries.find((row) => row.status === "queued" || row.status === "retrying")?.created_at,
      },
      webhooks: { rejected: 2, duplicates_suppressed: 7 },
    } satisfies SupportChannelOperations;
  });
  post("/v1/support-channels/deliveries/:event_seq/retry", (ctx) => {
    const row = deliveries.find((delivery) => delivery.event_seq === Number(ctx.params.event_seq));
    if (!row) throw notFound("Support channel delivery");
    if (row.status !== "failed") throw invalid("Only failed deliveries can be retried.");
    Object.assign(row, { status: "queued" as const, updated_at: nowIso() });
    return respond(202, { event_seq: row.event_seq, status: row.status });
  });
  post("/v1/support-channels/providers/:provider/accounts", (ctx) => {
    if (ctx.params.provider !== "discord") throw invalid("This provider does not have selectable accounts.");
    if (!String((ctx.body.credentials as Record<string, string> | undefined)?.bot_token ?? "").trim()) throw invalid("Couldn't load accounts from the provider.");
    return { data: [{ id: "1289000000000000000", name: "Margin community" }] };
  });
  post("/v1/support-channels/connections", (ctx) => {
    const provider = providers.find((p) => p.key === ctx.body.provider);
    if (!provider) throw invalid("Unknown support channel provider.");
    const credentials = ctx.body.credentials as Record<string, string> | undefined;
    if (!credentials || provider.fields.some((field) => !field.generated && !String(credentials[field.name] ?? "").trim())) throw invalid("Provide every required credential.");
    const id = uuid();
    const row: SupportChannelConnection = {
      id,
      provider: provider.key,
      name: "",
      external_account_id: provider.key === "discord" ? String(credentials.guild_id) : `demo-${provider.key}`,
      external_account_name: provider.key === "telegram" ? "Margin support" : provider.key === "discord" ? "Margin community" : provider.key === "google_chat" ? "Margin Chat app" : "Margin",
      external_account_handle: provider.key === "telegram" ? "@margin_support_bot" : "",
      status: "connected",
      last_error: "",
      ...health(provider.key),
      health_checked_at: nowIso(),
      webhook_url: `${location.origin}/v1/support-channels/hooks/${provider.key}/${id}`,
      created_at: nowIso(),
    };
    // Like the API, an unnamed connection takes its account's name.
    row.name = String(ctx.body.name ?? "").trim() || row.external_account_name;
    connections.unshift(row);
    return row;
  });
  patch("/v1/support-channels/connections/:id", (ctx) => {
    const row = connections.find((x) => x.id === ctx.params.id);
    if (!row) throw notFound("Support channel");
    row.name = String(ctx.body.name ?? "").trim() || row.name;
    Object.assign(row, health(row.provider), { health_checked_at: nowIso() });
    return row;
  });
  post("/v1/support-channels/connections/:id/test", (ctx) => {
    const row = connections.find((x) => x.id === ctx.params.id);
    if (!row) throw notFound("Support channel");
    Object.assign(row, health(row.provider), { health_checked_at: nowIso() });
    return row;
  });
  get("/v1/support-channels/connections/:id/destinations", (ctx) => {
    const row = connections.find((x) => x.id === ctx.params.id);
    if (!row) throw notFound("Support channel");
    return { data: discovered[row.provider] ?? [] };
  });
  del("/v1/support-channels/connections/:id", (ctx) => {
    const i = connections.findIndex((x) => x.id === ctx.params.id);
    if (i < 0) throw notFound("Support channel");
    connections.splice(i, 1);
    for (let n = destinations.length - 1; n >= 0; n--) if (destinations[n].connection_id === ctx.params.id) destinations.splice(n, 1);
    return respond(204);
  });
  post("/v1/support-channels/destinations", (ctx) => {
    const connection = connections.find((x) => x.id === ctx.body.connection_id);
    if (!connection) throw notFound("Support channel");
    const external = String(ctx.body.external_channel_id ?? "").trim();
    if (!external) throw invalid("Enter the provider channel ID.");
    const priorities = ["low", "normal", "high", "urgent"] as const;
    const priority = priorities.find((value) => value === ctx.body.priority) ?? null;
    if (ctx.body.priority !== undefined && priority === null) {
      throw invalid("Priority must be low, normal, high, or urgent.");
    }
    if (destinations.some((destination) => destination.connection_id === connection.id && destination.external_channel_id === external)) {
      throw new HttpError(409, "conflict", "That provider channel already has a destination.");
    }
    const row: SupportChannelDestination = {
      id: uuid(),
      connection_id: connection.id,
      team_id: typeof ctx.body.team_id === "string" ? ctx.body.team_id : null,
      priority,
      external_channel_id: external,
      external_channel_name: String(ctx.body.external_channel_name ?? "").trim(),
      enabled: true,
      include_notes: ctx.body.include_notes === true,
      message_limit: typeof ctx.body.message_limit === "number" ? ctx.body.message_limit : 3500,
      mode: "route",
      conditions: [],
      mirror: ["customer_replies", "agent_replies"],
      ollie_summary: true,
      ollie_warnings: true,
      ollie_personality: "",
      ollie_length: "standard",
      created_at: nowIso(),
    };
    applyOllie(row, ctx.body);
    applyRouting(row, ctx.body);
    checkRoute(row);
    destinations.push(row);
    return row;
  });
  patch("/v1/support-channels/destinations/:id", (ctx) => {
    const row = destinations.find((x) => x.id === ctx.params.id);
    if (!row) throw notFound("Support channel destination");
    const next = { ...row };
    applyOllie(next, ctx.body);
    applyRouting(next, ctx.body);
    checkRoute(next);
    Object.assign(row, next);
    return row;
  });
  get("/v1/support-channels/ollie", () => ollie);
  put("/v1/support-channels/ollie", (ctx) => {
    const actions = Array.isArray(ctx.body.actions) ? (ctx.body.actions as string[]) : [];
    if (actions.some((a) => !ollieActions.includes(a as SupportChannelOllieAction))) throw invalid(`actions takes only ${ollieActions.join(", ")}`);
    ollie.actions = ollieActions.filter((a) => actions.includes(a));
    return ollie;
  });
  del("/v1/support-channels/destinations/:id", (ctx) => {
    const i = destinations.findIndex((x) => x.id === ctx.params.id);
    if (i < 0) throw notFound("Support channel destination");
    destinations.splice(i, 1);
    return respond(204);
  });
  post("/v1/me/support-channel-link-code", () => ({ code: "LINK-DEMO", expires_at: new Date(Date.now() + 10 * 60_000).toISOString() }));
}
