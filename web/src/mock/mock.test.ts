import { describe, expect, it } from "vitest";
import { installMock } from "./index";
import { dispatch } from "./router";
import { db } from "./db";
import { socketFactory, teammatePresence } from "./gateway";

async function call(method: string, path: string, body?: unknown) {
  const ws = db.workspaces[0]?.workspace.id;
  const r = await dispatch(`http://mock${path}`, {
    method,
    body: body ? JSON.stringify(body) : undefined,
    headers: ws ? { "OD-Workspace": ws } : {},
  });
  return { status: r.status, body: r.status === 204 ? null : await r.json() };
}

describe("mock backend", () => {
  installMock();

  it("breaks reports down by team and brand, and narrows them", async () => {
    const range = "from=2026-09-12&to=2026-10-11";
    const all = (await call("GET", `/v1/reports/overview?${range}`)).body;
    expect(all.by_team.length).toBeGreaterThan(1);
    expect(all.by_team.some((r: { team_id: string | null }) => r.team_id === null)).toBe(true);
    expect(all.by_brand.length).toBe(2);
    const team = all.by_team.find((r: { team_id: string | null }) => r.team_id)!.team_id;
    const one = (await call("GET", `/v1/reports/overview?${range}&team_id=${team}`)).body;
    expect(one.by_team.map((r: { team_id: string }) => r.team_id)).toEqual([team]);
    expect(one.created).toBeLessThan(all.created);
    const brand = all.by_brand[0].brand_id;
    const b = (await call("GET", `/v1/reports/overview?${range}&brand_id=${brand}&team_id=none`)).body;
    expect(b.by_brand.map((r: { brand_id: string }) => r.brand_id)).toEqual([brand]);
    expect((await call("GET", `/v1/reports/overview?${range}&team_id=nope`)).status).toBe(422);
  });

  it("gives collections and articles a brand, and scopes help centers by it", async () => {
    const brands = (await call("GET", "/v1/brands")).body.data as { id: string; slug: string; is_default: boolean }[];
    const press = brands.find((b) => b.slug === "pressroom")!;
    expect((await call("POST", "/v1/kb/collections", { name: "X", brand_id: "nope" })).status).toBe(422);
    const col = (await call("POST", "/v1/kb/collections", { name: "Press only", description: "", brand_id: press.id })).body;
    expect(col.brand_id).toBe(press.id);
    const art = (await call("POST", "/v1/kb/articles", { title: "Press article", collection_id: col.id, body_html: "<p>Hi</p>", status: "published" })).body;
    const forDefault = (await call("GET", `/v1/kb/articles?for_brand_id=${brands.find((b) => b.is_default)!.id}`)).body.data as { id: string }[];
    expect(forDefault.some((a) => a.id === art.id)).toBe(false);
    const own = (await call("GET", `/v1/kb/articles?brand_id=${press.id}`)).body.data as { id: string }[];
    expect(own.some((a) => a.id === art.id)).toBe(true);
    const home = (await call("GET", "/v1/public/help/margin")).body;
    expect(home.collections.some((c: { id: string }) => c.id === col.id)).toBe(false);
    expect(home.logo_url).toBeTruthy();
    const shared = (await call("PATCH", `/v1/kb/collections/${col.id}`, { brand_id: null })).body;
    expect(shared.brand_id).toBeNull();
    expect((await call("PATCH", `/v1/kb/articles/${art.id}`, { brand_id: "nope" })).status).toBe(422);
  });

  it("breaks each sidebar row's tickets down into awaiting, urgent and overdue", async () => {
    const c = (await call("GET", "/v1/tickets/counts")).body;
    const keys = ["ticket_folder:unassigned", "ticket_folder:mine", "ticket_folder:open", "ticket_folder:pending", "team:mine",
      ...Object.keys(c.teams).map((id) => `team:${id}`), ...Object.keys(c.views).map((id) => `view:${id}`)];
    expect(Object.keys(c.stats).sort()).toEqual(keys.sort());
    const open = c.stats["ticket_folder:open"];
    expect(open.urgent).toBeLessThanOrEqual(c.open);
    expect(open.awaiting).toBeGreaterThan(0);
    expect(open.urgent).toBeGreaterThan(0);
    // A view's stats cover its own tickets.
    for (const id of Object.keys(c.views)) {
      const s = c.stats[`view:${id}`];
      expect(Math.max(s.awaiting, s.urgent, s.overdue)).toBeLessThanOrEqual(c.views[id]);
    }
  });

  it("serves every list endpoint the app reads", async () => {
    for (const path of [
      "/v1/me", "/v1/tickets", "/v1/tickets/counts", "/v1/contacts", "/v1/companies", "/v1/tags", "/v1/views",
      "/v1/sla-policies", "/v1/automations", "/v1/mailboxes", "/v1/members", "/v1/invitations", "/v1/teams",
      "/v1/api-keys", "/v1/webhooks", "/v1/audit-logs", "/v1/notifications", "/v1/kb/collections", "/v1/kb/articles",
      "/v1/saved-replies", "/v1/me/sessions", "/v1/widget/settings", "/v1/workspace", "/v1/search?q=sso",
      "/v1/reports/overview?from=2026-09-12&to=2026-10-11", "/v1/public/help/margin", "/v1/public/csat/demo", "/v1/auth/config",
      "/v1/integrations", "/v1/integrations/catalog",
      "/v1/support-channels/providers", "/v1/support-channels/connections", "/v1/support-channels/destinations",
    ]) {
      expect((await call("GET", path)).status, path).toBe(200);
    }
  });

  it("publishes contiguous events for mutations", async () => {
    const before = db.workspaces[0].seq;
    const t = (await call("GET", "/v1/tickets/%231072")).body;
    await call("PATCH", `/v1/tickets/${t.id}`, { status: "pending", tags: ["sso", "fresh"] });
    const ev = (await call("GET", `/v1/events?since=${before}`)).body;
    expect(ev.resync).toBe(false);
    const seqs = ev.data.map((e: { seq: number }) => e.seq);
    expect(seqs[0]).toBe(before + 1);
    expect(seqs).toEqual(seqs.map((_: number, i: number) => before + 1 + i));
    expect(ev.data.some((e: { type: string }) => e.type === "ticket.updated")).toBe(true);
  });

  it("connects integrations and links tickets", async () => {
    const created = await call("POST", "/v1/integrations", { provider: "github", token: "github_pat_example" });
    expect(created.status).toBe(201);
    expect(created.body.integration.status).toBe("connected");
    expect(created.body.integration.tools.some((x: { read_only: boolean }) => !x.read_only)).toBe(true);
    const oauth = await call("POST", "/v1/integrations", { provider: "sentry" });
    expect(oauth.body.authorize_url).toContain("/settings/integrations?connected=");
    expect((await call("POST", "/v1/integrations", { provider: "custom", url: "https://10.0.0.4/mcp" })).status).toBe(422);

    const t = (await call("GET", "/v1/tickets/%231072")).body;
    const before = db.workspaces[0].seq;
    const link = await call("POST", `/v1/tickets/${t.id}/links`, { url: "https://github.com/margin/app/issues/7", title: "SSO loop" });
    expect(link.status).toBe(201);
    expect((await call("POST", `/v1/tickets/${t.id}/links`, { url: "javascript:alert(1)" })).status).toBe(422);
    const ev = (await call("GET", `/v1/events?since=${before}`)).body.data.map((e: { type: string }) => e.type);
    expect(ev).toContain("ticket_link.created");
    expect(ev).toContain("activity.created");
    expect((await call("DELETE", `/v1/tickets/${t.id}/links/${link.body.id}`)).status).toBe(204);
    expect((await call("DELETE", `/v1/integrations/${created.body.integration.id}`)).status).toBe(204);
  });

  it("connects a support channel and routes a team to it", async () => {
    const created = await call("POST", "/v1/support-channels/connections", { provider: "slack", credentials: { bot_token: "xoxb-example", signing_secret: "secret" } });
    expect(created.status).toBe(201);
    const plain = await call("POST", "/v1/support-channels/destinations", { connection_id: created.body.id, external_channel_id: "C1" });
    expect(plain.status).toBe(201);
    expect(plain.body.include_notes).toBe(false);
    const team = (await call("GET", "/v1/teams")).body.data.find((x: { private: boolean }) => !x.private);
    const notes = await call("POST", "/v1/support-channels/destinations", { connection_id: created.body.id, team_id: team.id, external_channel_id: "C2", include_notes: true });
    expect(notes.body.include_notes).toBe(true);
    expect((await call("DELETE", `/v1/support-channels/destinations/${plain.body.id}`)).status).toBe(204);
    expect((await call("DELETE", `/v1/support-channels/connections/${created.body.id}`)).status).toBe(204);
    expect((await call("GET", "/v1/support-channels/destinations")).body.data).toEqual([]);
  });

  it("sends presence_state when a ticket topic is joined", async () => {
    const [tid] = [...teammatePresence.keys()];
    const wid = db.workspaces[0].workspace.id;
    const sock = socketFactory("ws://mock", () => ({ token: "t" }));
    sock.connect();
    await new Promise((r) => setTimeout(r, 150));
    const ch = sock.channel(`ticket:${wid}:${tid}`, { watch: true });
    const got = new Promise((resolve) => ch.on("presence_state", resolve));
    ch.join();
    const state = (await got) as Record<string, unknown>;
    expect(Object.keys(state).length).toBeGreaterThan(0);
  });
});
