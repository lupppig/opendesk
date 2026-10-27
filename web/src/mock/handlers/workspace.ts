// Workspaces, members, invitations, teams, API keys, webhooks, audit log,
// events, realtime tokens and notifications. Widget settings: handlers/widget.ts.

import type { ApiKey, Member, Role, Webhook, WebhookDelivery, WorkspaceSettings } from "@/lib/api/types";
import { profileOf } from "../profiles";
import { db, me, userRef, type WsData } from "../db";
import { publish } from "../gateway";
import { HttpError, del, forbidden, get, invalid, notFound, paginate, patch, post, required, respond, s, strArr, type Ctx } from "../router";
import { baseWorkspace } from "../seed";
import { seedDefaultLabels } from "../labels";
import { isRecord, nowIso, randomToken, slugify, uuid, DAY } from "../util";
import { checkLimit } from "./billing";
import { assign } from "./crud";
import { registerPictures } from "./pictures";
import { registerWidget } from "./widget";
import { mockWsToken } from "./workspaces";
import { lapseGrants, registerTeams, seedDefaultBrand } from "./teams";
import { envelopeTeam, standIn } from "../gateway";
import { setGrants, teamRole } from "../teams";

const ROLES: Role[] = ["owner", "admin", "agent", "viewer"];

function myRole(ws: WsData): Role {
  return ws.members.find((m) => m.user_id === db.meId)?.role ?? "viewer";
}

function requireAdmin(ws: WsData) {
  const r = myRole(ws);
  if (r !== "owner" && r !== "admin") throw forbidden("Only admins can change this");
}

export function memberOut(ws: WsData, user_id: string): Member {
  const m = ws.members.find((x) => x.user_id === user_id)!;
  const u = db.users.find((x) => x.id === m.user_id);
  const availability = m.availability ?? "online";
  const lastSeen = m.last_seen_at ?? null;
  const idle = ws.routing.autoAwayMinutes > 0 && (!lastSeen || Date.now() - Date.parse(lastSeen) > ws.routing.autoAwayMinutes * 60_000);
  const open = ws.tickets.filter((t) => t.assignee?.id === m.user_id && (t.status === "open" || t.status === "pending") && !ws.deletedTickets.has(t.id)).length;
  return {
    user: { ...userRef(m.user_id)!, avatar_source: u?.avatar_source ?? null },
    role: m.role,
    availability,
    presence: availability === "online" && idle ? "away" : availability,
    max_open_tickets: m.max_open_tickets ?? null,
    message_delete: m.message_delete ?? null,
    open_tickets: open,
    last_seen_at: lastSeen,
    created_at: m.created_at,
    profile: { ...profileOf(ws, m.user_id) },
  };
}

function audit(ws: WsData, action: string, entity_type: string, entity_id: string | null, data: Record<string, unknown> = {}) {
  ws.audit.unshift({
    id: uuid(),
    actor: { type: "user", user: userRef(db.meId), api_key_id: null },
    action,
    entity_type,
    entity_id,
    data,
    ip: "84.152.33.17",
    created_at: nowIso(),
  });
}

export function seedNewWorkspace(name: string): WsData {
  const slugBase = slugify(name) || "workspace";
  let slug = slugBase;
  for (let i = 2; db.workspaces.some((w) => w.workspace.slug === slug); i++) slug = `${slugBase}-${i}`;
  const ws = baseWorkspace(uuid(), name, slug);
  ws.workspace.created_at = nowIso();
  ws.workspace.settings = { timezone: Intl.DateTimeFormat().resolvedOptions().timeZone, help_center_enabled: false, csat_enabled: false, auto_assign: "none" };
  ws.members = [{ user_id: db.meId, role: "owner", created_at: nowIso() }];
  seedDefaultLabels(ws);
  ws.views = [{ id: uuid(), name: "Urgent", shared: true, filters: { status: ["open"], priority: ["urgent", "high"] }, position: 0 }];
  ws.sla = [{ id: uuid(), name: "Standard", priority: null, first_response_minutes: 480, resolution_minutes: 4320, business_hours: false, enabled: true, position: 0 }];
  seedDefaultBrand(ws);
  db.workspaces.push(ws);
  return ws;
}

export function registerWorkspace() {
  registerPictures();
  registerWidget();
  registerTeams();
  post("/v1/workspaces", (ctx) => {
    const name = required(ctx, "name").slice(0, 60);
    const ws = seedNewWorkspace(name);
    return respond(201, ws.workspace);
  });
  get("/v1/workspace", (ctx) => ctx.ws.workspace);
  patch("/v1/workspace", (ctx) => {
    requireAdmin(ctx.ws);
    const name = s(ctx, "name");
    if (name !== undefined) {
      if (!name.trim()) throw invalid("Name can't be empty");
      ctx.ws.workspace.name = name.trim().slice(0, 60);
    }
    if (isRecord(ctx.body.settings)) {
      const wasGravatar = !!ctx.ws.workspace.settings.gravatar;
      ctx.ws.workspace.settings = { ...ctx.ws.workspace.settings, ...(ctx.body.settings as WorkspaceSettings) };
      // Turning Gravatar pictures off removes them (contracts/api.md "Profile pictures and logos").
      if (wasGravatar && !ctx.ws.workspace.settings.gravatar)
        for (const c of ctx.ws.contacts)
          if (c.avatar_source === "gravatar") {
            c.avatar_url = null;
            c.avatar_source = null;
          }
    }
    audit(ctx.ws, "workspace.updated", "workspace", ctx.ws.workspace.id, { fields: Object.keys(ctx.body) });
    return ctx.ws.workspace;
  });
  del("/v1/workspace", (ctx) => {
    if (myRole(ctx.ws) !== "owner") throw forbidden("Only the owner can delete the workspace");
    if (s(ctx, "confirm_name") !== ctx.ws.workspace.name) throw invalid("Type the workspace name exactly to confirm");
    db.workspaces = db.workspaces.filter((w) => w !== ctx.ws);
    return respond(204);
  });
  post("/v1/workspace/transfer", (ctx) => {
    if (myRole(ctx.ws) !== "owner") throw forbidden("Only the owner can transfer ownership");
    const uid = required(ctx, "user_id");
    const target = ctx.ws.members.find((m) => m.user_id === uid);
    if (!target) throw notFound("Member");
    target.role = "owner";
    ctx.ws.members.find((m) => m.user_id === db.meId)!.role = "admin";
    publish(ctx.ws, "member.updated", { member: memberOut(ctx.ws, uid) }, { topics: [`user:${uid}`] });
    publish(ctx.ws, "member.updated", { member: memberOut(ctx.ws, db.meId) }, { topics: [`user:${db.meId}`] });
    audit(ctx.ws, "workspace.ownership_transferred", "workspace", ctx.ws.workspace.id, { to: uid });
    return respond(204);
  });
  post("/v1/workspace/widget-secret/rotate", (ctx) => {
    requireAdmin(ctx.ws);
    audit(ctx.ws, "workspace.widget_secret_rotated", "workspace", ctx.ws.workspace.id);
    return respond(200, { secret: `wsec_${randomToken(40)}` });
  });

  // Members.
  get("/v1/members", (ctx) => ({ data: ctx.ws.members.map((m) => memberOut(ctx.ws, m.user_id)), next_cursor: null }));
  patch("/v1/members/:user_id", (ctx) => {
    requireAdmin(ctx.ws);
    const m = ctx.ws.members.find((x) => x.user_id === ctx.params.user_id);
    if (!m) throw notFound("Member");
    const role = s(ctx, "role") as Role | undefined;
    if (!role || !ROLES.includes(role)) throw invalid("Unknown role");
    if (m.role === "owner" && role !== "owner") throw new HttpError(409, "conflict", "Transfer ownership before changing the owner's role");
    if (role === "owner") throw new HttpError(409, "conflict", "Use transfer ownership to make someone the owner");
    const from = m.role;
    m.role = role;
    publish(ctx.ws, "member.updated", { member: memberOut(ctx.ws, m.user_id) }, { topics: [`user:${m.user_id}`] });
    audit(ctx.ws, "member.role_changed", "member", m.user_id, { from, to: role });
    return memberOut(ctx.ws, m.user_id);
  });
  del("/v1/members/:user_id", (ctx) => {
    requireAdmin(ctx.ws);
    const m = ctx.ws.members.find((x) => x.user_id === ctx.params.user_id);
    if (!m) throw notFound("Member");
    if (m.role === "owner") throw new HttpError(409, "owner_cannot_leave", "The owner can't be removed. Transfer ownership first.");
    ctx.ws.members = ctx.ws.members.filter((x) => x !== m);
    for (const t of ctx.ws.teams) {
      t.members = t.members.filter((x) => x.user_id !== m.user_id);
      t.member_ids = t.members.map((x) => x.user_id);
    }
    lapseGrants(ctx.ws);
    publish(ctx.ws, "member.removed", { user_id: m.user_id }, { topics: [`user:${m.user_id}`] });
    audit(ctx.ws, "member.removed", "member", m.user_id);
    return respond(204);
  });

  // Invitations.
  get("/v1/invitations", (ctx) => ({ data: ctx.ws.invitations.map(({ token: _t, ...rest }) => rest), next_cursor: null }));
  post("/v1/invitations", (ctx) => {
    requireAdmin(ctx.ws);
    const email = required(ctx, "email").toLowerCase();
    if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(email)) throw invalid("Enter a valid email address");
    const role = (s(ctx, "role") ?? "agent") as Role;
    if (!ROLES.includes(role) || role === "owner") throw invalid("Choose admin, agent or viewer");
    const existing = ctx.ws.members.find((m) => userRef(m.user_id)?.email === email);
    if (existing) throw new HttpError(409, "conflict", "This person is already a member");
    if (ctx.ws.invitations.some((i) => i.email === email)) throw new HttpError(409, "conflict", "An invitation for this email is already pending");
    checkLimit(ctx.ws, "member");
    const inv = { id: uuid(), token: `inv_${randomToken(12)}`, email, role, expires_at: new Date(Date.now() + 7 * DAY).toISOString(), created_at: nowIso(), invited_by: userRef(db.meId) };
    ctx.ws.invitations.unshift(inv);
    audit(ctx.ws, "member.invited", "invitation", inv.id, { email, role });
    const { token: _t, ...out } = inv;
    return respond(201, out);
  });
  del("/v1/invitations/:id", (ctx) => {
    requireAdmin(ctx.ws);
    ctx.ws.invitations = ctx.ws.invitations.filter((i) => i.id !== ctx.params.id);
    return respond(204);
  });
  const findInvite = (token: string) => {
    for (const w of db.workspaces) {
      const inv = w.invitations.find((i) => i.token === token);
      if (inv) return { w, inv };
    }
    return null;
  };
  post("/v1/invitations/lookup", (ctx) => {
    const token = required(ctx, "token");
    if (!token || token === "expired") throw new HttpError(404, "not_found", "This invitation has expired or was revoked");
    const f = findInvite(token);
    if (f) return { workspace_name: f.w.workspace.name, email: f.inv.email, role: f.inv.role };
    return { workspace_name: "Northwind Support", email: me().email, role: "agent" };
  });
  post("/v1/invitations/accept", (ctx) => {
    const token = required(ctx, "token");
    if (token === "expired") throw new HttpError(404, "not_found", "This invitation has expired or was revoked");
    const f = findInvite(token);
    let ws: WsData;
    let role: Role = "agent";
    if (f) {
      ws = f.w;
      role = f.inv.role;
      f.w.invitations = f.w.invitations.filter((i) => i !== f.inv);
    } else {
      ws = db.workspaces.find((w) => w.workspace.slug === "northwind-support") ?? seedNewWorkspace("Northwind Support");
      ws.members = ws.members.filter((m) => m.user_id !== db.meId || m.role !== "owner");
    }
    if (!ws.members.some((m) => m.user_id === db.meId)) ws.members.push({ user_id: db.meId, role, created_at: nowIso() });
    else ws.members.find((m) => m.user_id === db.meId)!.role = role;
    publish(ws, "member.added", { member: memberOut(ws, db.meId) }, { topics: [`user:${db.meId}`] });
    return { workspace: ws.workspace, role };
  });

  // API keys: the secret is only returned on create.
  get("/v1/api-keys", (ctx) => paginate(ctx, ctx.ws.apiKeys, 100));
  get("/v1/api-keys/:id", (ctx) => ctx.ws.apiKeys.find((k) => k.id === ctx.params.id) ?? (() => { throw notFound("API key"); })());
  post("/v1/api-keys", (ctx) => {
    requireAdmin(ctx.ws);
    // Like the API: a key needs the second factor (when on) and a sign-in within 12 hours.
    if (db.stepUp === "mfa_required") throw new HttpError(403, "mfa_required", "this action requires two-factor authentication");
    if (db.stepUp === "reauth_required") throw new HttpError(403, "reauth_required", "For your security, sign in again to do this. Your last sign-in is more than 12 hours old.");
    const name = required(ctx, "name").slice(0, 60);
    const scopes = strArr(ctx.body.scopes) ?? [];
    if (!scopes.length) throw invalid("Choose at least one scope");
    const secret = `od_live_${randomToken(43)}`;
    const key: ApiKey = { id: uuid(), name, prefix: secret.slice(0, 16), scopes, last_used_at: null, expires_at: s(ctx, "expires_at") ?? null, created_at: nowIso(), created_by: userRef(db.meId) };
    setGrants(ctx.ws, key, privateTeams(ctx, []));
    ctx.ws.apiKeys.unshift(key);
    audit(ctx.ws, "api_key.created", "api_key", key.id, { name });
    return respond(201, { ...key, secret });
  });
  patch("/v1/api-keys/:id", (ctx) => {
    const key = ctx.ws.apiKeys.find((k) => k.id === ctx.params.id);
    if (!key) throw notFound("API key");
    assign(key, ctx.body, ["name", "scopes"]);
    if ("private_team_ids" in ctx.body) setGrants(ctx.ws, key, privateTeams(ctx, key.private_team_ids ?? []));
    audit(ctx.ws, "api_key.updated", "api_key", key.id, { fields: Object.keys(ctx.body) });
    return key;
  });
  del("/v1/api-keys/:id", (ctx) => {
    requireAdmin(ctx.ws);
    ctx.ws.apiKeys = ctx.ws.apiKeys.filter((k) => k.id !== ctx.params.id);
    audit(ctx.ws, "api_key.revoked", "api_key", ctx.params.id);
    return respond(204);
  });

  // Webhooks.
  const echoless = (url: string) => url.startsWith("https://hooks.zapier.com/");
  const checkUrl = (url: string) => {
    if (!/^https:\/\/[^/\s]+/.test(url)) throw invalid("Webhook URLs must use https");
    if (/^https:\/\/(localhost|127\.|10\.|192\.168\.|169\.254\.)/.test(url)) throw invalid("Webhook URLs can't point to private or local addresses");
  };
  get("/v1/webhooks", (ctx) => paginate(ctx, ctx.ws.webhooks, 100));
  get("/v1/webhooks/:id", (ctx) => findHook(ctx));
  post("/v1/webhooks", (ctx) => {
    requireAdmin(ctx.ws);
    const url = required(ctx, "url");
    checkUrl(url);
    const events = strArr(ctx.body.events) ?? [];
    if (!events.length) throw invalid("Choose at least one event");
    // The mock's addresses echo the verification challenge, except Zapier's catch hooks,
    // which (like the real ones) cannot choose their answer: their code is entered instead.
    const hook: Webhook = {
      id: uuid(),
      url,
      description: s(ctx, "description") ?? "",
      events,
      header_names: Object.keys((ctx.body.headers as Record<string, string> | undefined) ?? {}).sort(),
      enabled: ctx.body.enabled !== false,
      verified: !echoless(url),
      created_at: nowIso(),
    };
    setGrants(ctx.ws, hook, privateTeams(ctx, []));
    ctx.ws.webhooks.unshift(hook);
    ctx.ws.deliveries[hook.id] = [];
    publish(ctx.ws, "webhook.created", { webhook: hook });
    audit(ctx.ws, "webhook.created", "webhook", hook.id, { url });
    return respond(201, { ...hook, secret: `whsec_${randomToken(32)}` });
  });
  patch("/v1/webhooks/:id", (ctx) => {
    const hook = findHook(ctx);
    if (s(ctx, "url")) checkUrl(s(ctx, "url")!);
    assign(hook, ctx.body, ["url", "description", "events", "enabled"]);
    if ("headers" in ctx.body) hook.header_names = Object.keys((ctx.body.headers as Record<string, string>) ?? {}).sort();
    if ("private_team_ids" in ctx.body) setGrants(ctx.ws, hook, privateTeams(ctx, hook.private_team_ids ?? []));
    publish(ctx.ws, "webhook.updated", { webhook: hook });
    audit(ctx.ws, "webhook.updated", "webhook", hook.id);
    return hook;
  });
  del("/v1/webhooks/:id", (ctx) => {
    const hook = findHook(ctx);
    ctx.ws.webhooks = ctx.ws.webhooks.filter((h) => h !== hook);
    publish(ctx.ws, "webhook.deleted", { webhook_id: hook.id });
    audit(ctx.ws, "webhook.deleted", "webhook", hook.id);
    return respond(204);
  });
  // The code a demo address "received" in its webhook.verification event.
  post("/v1/webhooks/:id/verify", (ctx) => {
    const hook = findHook(ctx);
    const code = s(ctx, "code");
    if (code !== undefined && code !== "whv_demo") throw invalid("That code does not match the last challenge sent to this address.");
    if (code !== undefined || !echoless(hook.url)) hook.verified = true;
    publish(ctx.ws, "webhook.updated", { webhook: hook });
    return respond(200, hook);
  });
  get("/v1/webhooks/:id/deliveries", (ctx) => paginate(ctx, ctx.ws.deliveries[findHook(ctx).id] ?? []));
  post("/v1/webhooks/:id/test", (ctx) => {
    const hook = findHook(ctx);
    const d: WebhookDelivery = { id: uuid(), event_id: uuid(), event_type: "webhook.test", status: "pending", attempts: 1, response_code: null, duration_ms: null, created_at: nowIso() };
    (ctx.ws.deliveries[hook.id] ??= []).unshift(d);
    setTimeout(() => {
      d.status = hook.url.includes("fail") ? "failed" : "succeeded";
      d.response_code = d.status === "failed" ? 500 : 200;
      d.duration_ms = 180 + Math.floor(Math.random() * 300);
      publish(ctx.ws, "webhook.updated", { webhook: hook });
    }, 900);
    return respond(202, { ...d });
  });

  get("/v1/audit-logs", (ctx) => {
    requireAdmin(ctx.ws);
    const action = ctx.query.get("action");
    const entity = ctx.query.get("entity_type");
    return paginate(ctx, ctx.ws.audit.filter((a) => (!action || a.action.startsWith(action)) && (!entity || a.entity_type === entity)));
  });

  get("/v1/events", (ctx) => {
    const since = Number(ctx.query.get("since") ?? 0) || 0;
    const limit = Math.min(Number(ctx.query.get("limit") ?? 500) || 500, 1000);
    const log = ctx.ws.events;
    const oldest = log[0]?.seq;
    const resync = since < 0 || (oldest !== undefined && since < oldest - 1);
    // Events of private teams the caller is not on come back as stand-ins (contracts/teams.md "Realtime").
    const mine = (e: (typeof log)[number]) => {
      const team = envelopeTeam.get(e);
      const row = team ? ctx.ws.teams.find((t) => t.id === team) : null;
      return !row || !!teamRole(row, db.meId) ? e : standIn(e);
    };
    return { data: resync ? [] : log.filter((e) => e.seq > since).slice(0, limit).map(mine), latest_seq: ctx.ws.seq, resync };
  });

  post("/v1/realtime/token", (ctx) =>
    respond(200, { token: mockWsToken(ctx), url: "ws://mock.opendesk.local/socket", expires_at: new Date(Date.now() + 5 * 60_000).toISOString() }),
  );

  get("/v1/notifications", (ctx) => {
    const unread = ctx.query.get("unread") === "true";
    return paginate(ctx, unread ? ctx.ws.notifications.filter((n) => !n.read_at) : ctx.ws.notifications);
  });
  post("/v1/notifications/read", (ctx) => {
    const ids = strArr(ctx.body.ids);
    const all = ctx.body.all === true;
    const now = nowIso();
    for (const n of ctx.ws.notifications) if (!n.read_at && (all || ids?.includes(n.id))) n.read_at = now;
    return respond(204);
  });
}

/**
 * private_team_ids on a key or webhook: private teams only, and only ones the
 * caller belongs to may be added (contracts/teams.md "Visibility"). Anyone may
 * take a team out: narrowing access needs no membership.
 */
function privateTeams(ctx: Ctx, current: string[]): string[] {
  if (!("private_team_ids" in ctx.body)) return current;
  const ids = strArr(ctx.body.private_team_ids);
  if (!ids) throw invalid("private_team_ids must be a list of team ids");
  for (const id of ids) {
    const t = ctx.ws.teams.find((x) => x.id === id);
    if (!t || !t.private) throw invalid("Only private teams can be listed");
    if (!current.includes(id) && !teamRole(t, db.meId)) throw forbidden("Only members of a private team can give access to it");
  }
  return [...new Set(ids)];
}

function findHook(ctx: Ctx): Webhook {
  const h = ctx.ws.webhooks.find((x) => x.id === ctx.params.id);
  if (!h) throw notFound("Webhook");
  return h;
}
