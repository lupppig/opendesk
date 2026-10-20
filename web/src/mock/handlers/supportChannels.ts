import type { SupportChannelConnection, SupportChannelDestination, SupportChannelProvider } from "@/lib/api/types";
import { del, get, invalid, notFound, post, respond } from "../router";
import { nowIso, uuid } from "../util";

const providers: SupportChannelProvider[] = [
  {
    key: "slack",
    name: "Slack",
    description: "Mirror email tickets into Slack threads and work them by talking to Ollie.",
    fields: [
      { name: "bot_token", label: "Bot token", secret: true },
      { name: "signing_secret", label: "Signing secret", secret: true },
    ],
  },
  {
    key: "discord",
    name: "Discord",
    description: "Mirror email tickets into Discord threads and work them by talking to Ollie.",
    fields: [
      { name: "bot_token", label: "Bot token", secret: true },
      { name: "public_key", label: "Interactions public key", secret: true },
      { name: "guild_id", label: "Server ID", secret: false },
    ],
  },
  {
    key: "telegram",
    name: "Telegram",
    description: "Mirror email tickets into Telegram forum topics and work them by talking to Ollie.",
    fields: [
      { name: "bot_token", label: "Bot token", secret: true },
      { name: "webhook_secret", label: "Webhook secret", secret: true },
    ],
  },
];

const connections: SupportChannelConnection[] = [];
const destinations: SupportChannelDestination[] = [];

export function registerSupportChannels() {
  get("/v1/support-channels/providers", () => ({ data: providers }));
  get("/v1/support-channels/connections", () => ({ data: connections }));
  get("/v1/support-channels/destinations", () => ({ data: destinations }));
  post("/v1/support-channels/connections", (ctx) => {
    const provider = providers.find((p) => p.key === ctx.body.provider);
    if (!provider) throw invalid("Unknown support channel provider.");
    const credentials = ctx.body.credentials as Record<string, string> | undefined;
    if (!credentials || provider.fields.some((field) => !String(credentials[field.name] ?? "").trim())) throw invalid("Provide every required credential.");
    const id = uuid();
    const row: SupportChannelConnection = {
      id,
      provider: provider.key,
      name: String(ctx.body.name ?? "").trim() || `Demo ${provider.name}`,
      external_account_id: `demo-${provider.key}`,
      external_account_name: `Demo ${provider.name}`,
      status: "connected",
      last_error: "",
      webhook_url: `${location.origin}/v1/support-channels/hooks/${provider.key}/${id}`,
      created_at: nowIso(),
    };
    connections.unshift(row);
    return row;
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
    const row: SupportChannelDestination = {
      id: uuid(),
      connection_id: connection.id,
      team_id: typeof ctx.body.team_id === "string" ? ctx.body.team_id : null,
      external_channel_id: external,
      external_channel_name: String(ctx.body.external_channel_name ?? "").trim(),
      enabled: true,
      include_notes: ctx.body.include_notes === true,
      created_at: nowIso(),
    };
    destinations.push(row);
    return row;
  });
  del("/v1/support-channels/destinations/:id", (ctx) => {
    const i = destinations.findIndex((x) => x.id === ctx.params.id);
    if (i < 0) throw notFound("Support channel destination");
    destinations.splice(i, 1);
    return respond(204);
  });
  post("/v1/me/support-channel-link-code", () => ({ code: "LINK-DEMO", expires_at: new Date(Date.now() + 10 * 60_000).toISOString() }));
}
