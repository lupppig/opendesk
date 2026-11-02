// Phone connections (contracts/phone.md). The demo Aircall account takes the API ID
// "demo" with any token, the demo Voys account the token "demo"; anything else is refused
// like a wrong key. A Voys connection waits for its first call, as its webhook is added
// in Voys by hand.

import type { PhoneProvider } from "@/lib/api/types";
import { phoneConnection } from "../phone";
import { del, get, HttpError, invalid, notFound, patch, post, respond } from "../router";
import { nowIso, uuid } from "../util";

const providers: PhoneProvider[] = [
  {
    key: "aircall",
    name: "Aircall",
    fields: [
      { name: "api_id", label: "API ID", secret: false },
      { name: "api_token", label: "API token", secret: true },
    ],
    manual_webhook: false,
    insight_cards: true,
  },
  { key: "voys", name: "Voys", fields: [{ name: "api_token", label: "API token", secret: true }], manual_webhook: true, insight_cards: false },
];

const lines = [
  { id: "501", name: "DE - Support", provider: "aircall" },
  { id: "502", name: "EN - Support", provider: "aircall" },
  { id: "503", name: "EN - Sales", provider: "aircall" },
  { id: "+31332022074", name: "Support desk", provider: "voys" },
];

/** A Voys webhook URL: the connection's id, then its secret. */
const voysHook = (id: string) => `https://api.opendesk.sh/v1/phone/hooks/voys/${id}/${uuid().replace(/-/g, "")}${uuid().replace(/-/g, "").slice(0, 11)}`;

const waiting = {
  health_status: "action_required",
  health_code: "webhook_missing",
  health_detail: "No call has come in yet. Add the webhook URL in the provider, then make a test call.",
  last_event_at: null,
} as const;

export function registerPhone() {
  get("/v1/phone/providers", () => ({ data: providers }));
  get("/v1/phone/connections", (ctx) => ({ data: ctx.ws.phone }));
  get("/v1/phone/lines", (ctx) => ({ data: lines.filter((l) => ctx.ws.phone.some((c) => c.provider === l.provider)) }));
  post("/v1/phone/connections", (ctx) => {
    const b = ctx.body as { provider?: string; credentials?: Record<string, string> };
    const p = providers.find((x) => x.key === b.provider);
    if (!p) throw invalid("Unknown phone provider.");
    if (ctx.ws.phone.some((c) => c.provider === b.provider)) throw new HttpError(409, "conflict", "This workspace is already connected to that provider.");
    if (!p.fields.every((f) => b.credentials?.[f.name]?.trim())) throw invalid("Provide every required credential.");
    const demo = p.key === "voys" ? b.credentials?.api_token : b.credentials?.api_id;
    if (demo !== "demo") throw new HttpError(422, "phone_credentials_refused", "The provider refused these credentials.");
    const row = phoneConnection({ name: "Margin", created_at: nowIso(), last_event_at: null, health_checked_at: nowIso() });
    if (p.manual_webhook) Object.assign(row, { provider: p.key, account_name: "Margin", insight_cards: false, webhook_url: voysHook(row.id), ...waiting });
    ctx.ws.phone.push(row);
    return respond(201, row);
  });
  patch("/v1/phone/connections/:id", (ctx) => {
    const row = ctx.ws.phone.find((c) => c.id === ctx.params.id);
    if (!row) throw notFound("Phone connection");
    const b = ctx.body as { reuse_days?: number; assign_answered?: boolean; insight_cards?: boolean; name?: string };
    if (b.reuse_days !== undefined) row.reuse_days = b.reuse_days;
    if (b.assign_answered !== undefined) row.assign_answered = b.assign_answered;
    if (b.insight_cards !== undefined) row.insight_cards = b.insight_cards;
    if (b.name) row.name = b.name;
    return row;
  });
  post("/v1/phone/connections/:id/test", (ctx) => {
    const row = ctx.ws.phone.find((c) => c.id === ctx.params.id);
    if (!row) throw notFound("Phone connection");
    const manual = providers.find((p) => p.key === row.provider)?.manual_webhook;
    if (manual && !row.last_event_at) Object.assign(row, { ...waiting, status: "connected", health_checked_at: nowIso() });
    else Object.assign(row, { health_status: "healthy", health_code: "receiving", health_detail: "Calls are sent to opendesk.", status: "connected", health_checked_at: nowIso() });
    return row;
  });
  post("/v1/phone/connections/:id/rotate-webhook", (ctx) => {
    const row = ctx.ws.phone.find((c) => c.id === ctx.params.id);
    if (!row) throw notFound("Phone connection");
    if (!providers.find((p) => p.key === row.provider)?.manual_webhook) throw invalid("opendesk registers this provider's webhook itself. Test the connection to repair it.");
    Object.assign(row, { webhook_url: voysHook(row.id), ...waiting, health_checked_at: nowIso() });
    return respond(200, row);
  });
  del("/v1/phone/connections/:id", (ctx) => {
    const before = ctx.ws.phone.length;
    ctx.ws.phone = ctx.ws.phone.filter((c) => c.id !== ctx.params.id);
    if (ctx.ws.phone.length === before) throw notFound("Phone connection");
    return respond(204, null);
  });
}
