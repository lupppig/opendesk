// Mock routes for the AI agent setup checklist (contracts/agent.md "Setup") and for
// connecting AI clients to opendesk's MCP server: the server info, connected OAuth
// clients and the consent page's request (contracts/agent.md "OAuth").
//
// Any well-formed request id the mock hasn't seen opens a demo request from Claude, so
// /oauth/consent?request=<uuid> works without an AI client. Answered requests are gone.

import type { AgentSetup, AgentSetupStep, AIClientConnection, AIClientServer, AIClientTool, OAuthRequest, Role } from "@/lib/api/types";
import { aiKeyOf } from "../aiKey";
import { db, userRef, type WsData } from "../db";
import { HttpError, del, forbidden, get, invalid, notFound, post, respond } from "../router";
import { isRecord, nowIso, uuid } from "../util";
import { aiOff, assertAiInstance, requireAiAdmin } from "./ai";
import { botsOf } from "../bots";

// ─── Setup ──────────────────────────────────────────────────────────────────

/** The checklist, computed from the mock workspace the way agentsetup.Load reads tables. */
export function agentSetup(ws: WsData): AgentSetup {
  const available = !aiOff() || !!aiKeyOf(ws);
  const steps: AgentSetupStep[] = [
    { id: "ai_on", title: "Turn on AI", description: "Ollie needs AI switched on for the workspace before it can answer anyone.", done: available && ws.ai.enabled, optional: false, href: "/settings/ai" },
    {
      id: "identity",
      title: "Build your bot and tell it how to help",
      description: "A name, a look and instructions make answers sound like your team.",
      done: botsOf(ws).some((b) => !b.is_default || b.name !== "Ollie" || !!b.image_url || !!b.personality.instructions.trim() || b.updated_at > b.created_at),
      optional: false,
      href: "/settings/ai/bots",
    },
    {
      id: "knowledge",
      title: "Give it something to know",
      description: "Ollie answers only from published help articles and ready knowledge sources.",
      done: ws.articles.some((a) => a.status === "published") || ws.knowledge.some((k) => k.status === "ready" && k.enabled),
      optional: false,
      href: "/settings/ai/knowledge",
    },
    {
      id: "tools",
      title: "Connect tools",
      description: "Let Ollie look up orders or accounts in your own systems.",
      done: ws.mcpServers.some((m) => m.enabled && m.status === "connected"),
      optional: true,
      href: "/settings/ai/tools",
    },
    {
      id: "playground",
      title: "Test it in the playground",
      description: "Ask a few real questions and check the answers before visitors do.",
      done: ws.aiUsage.some((u) => u.feature === "playground"),
      optional: false,
      href: "/settings/ai/playground",
    },
    { id: "widget", title: "Install the chat widget", description: "Visitors reach Ollie through the widget on your site.", done: ws.routing.widgetSeen, optional: false, href: "/settings/widget" },
    {
      id: "answers_on",
      title: "Let Ollie answer chats",
      description: "Turn on answers so Ollie replies to visitors and hands off when it should.",
      done: ws.ai.widget_answers,
      optional: false,
      href: "/settings/ai/behaviour",
    },
    {
      id: "email_answers",
      title: "Answer email too",
      description: "Turn on Ollie for a mailbox to draft or send email replies.",
      done: ws.mailboxes.some((m) => m.ai_answers),
      optional: true,
      href: "/settings/mailboxes",
    },
  ];
  const required = steps.filter((s) => !s.optional);
  const done_count = required.filter((s) => s.done).length;
  return { steps, done_count, total: required.length, complete: done_count === required.length };
}

// ─── Scopes and tools ───────────────────────────────────────────────────────

export const OAUTH_SCOPES = ["tickets:read", "tickets:write", "contacts:read", "contacts:write", "kb:read", "kb:write", "wiki:read", "wiki:write", "settings:read"];

/** oauthgrant.ForRole: viewers read, agents and up also write. */
export function scopesForRole(role: Role): string[] {
  return role === "viewer" ? ["tickets:read", "contacts:read", "kb:read", "wiki:read", "settings:read"] : [...OAUTH_SCOPES];
}

const TOOLS: AIClientTool[] = [
  { name: "search_tickets", title: "Search tickets", description: "Find tickets by text, status, priority, severity, assignee, team, label, channel, contact or creation date.", scopes: ["tickets:read"], read_only: true, destructive: false },
  { name: "get_ticket", title: "Get a ticket", description: "Read one ticket: its fields, SLA, labels and its thread as plain text.", scopes: ["tickets:read"], read_only: true, destructive: false },
  { name: "reply_to_ticket", title: "Reply to a ticket", description: "Send a public reply on a ticket. The customer receives it (by email for email tickets).", scopes: ["tickets:write"], read_only: false, destructive: false },
  { name: "draft_reply", title: "Draft a reply for the team", description: "Stage a reply on a ticket without sending it. A teammate reviews, edits and sends it from the ticket.", scopes: ["tickets:write"], read_only: false, destructive: false },
  { name: "add_note", title: "Add an internal note", description: "Add an internal note to a ticket. Notes are visible to the team only, never to the customer.", scopes: ["tickets:write"], read_only: false, destructive: false },
  { name: "update_ticket", title: "Update a ticket", description: "Change a ticket's status, priority, severity, assignee, team or labels. Only the fields you pass change.", scopes: ["tickets:write"], read_only: false, destructive: true },
  { name: "create_ticket", title: "Create a ticket", description: "Create a ticket for a contact by email (the contact is created when new).", scopes: ["tickets:write"], read_only: false, destructive: false },
  { name: "search_contacts", title: "Search contacts", description: "Find customers (contacts) by name, email, phone or custom field value.", scopes: ["contacts:read"], read_only: true, destructive: false },
  { name: "get_contact", title: "Get a contact", description: "Read one contact by id or email: details, company, labels, custom fields and notes.", scopes: ["contacts:read"], read_only: true, destructive: false },
  { name: "search_knowledge", title: "Search the knowledge base", description: "Search the help center's knowledge base articles and the workspace's other knowledge sources by text.", scopes: ["kb:read"], read_only: true, destructive: false },
  { name: "create_kb_draft", title: "Create a draft article", description: "Create a knowledge base article as an unpublished draft for the team to review and publish.", scopes: ["kb:write"], read_only: false, destructive: false },
  { name: "search_wiki", title: "Search the wiki", description: "Search the team's internal wiki (runbooks, policies, escalation steps, how-tos) by text.", scopes: ["wiki:read"], read_only: true, destructive: false },
  { name: "get_wiki_page", title: "Read a wiki page", description: "Read one page of the team's internal wiki: its text, where it sits, its subpages and its version.", scopes: ["wiki:read"], read_only: true, destructive: false },
  { name: "create_wiki_page", title: "Create a wiki page", description: "Add a page to the team's internal wiki, at the top level or under another page.", scopes: ["wiki:write"], read_only: false, destructive: false },
  { name: "update_wiki_page", title: "Update a wiki page", description: "Rename a wiki page or replace its text, without overwriting a teammate's newer edit.", scopes: ["wiki:write"], read_only: false, destructive: true },
  { name: "get_report", title: "Get the overview report", description: "Helpdesk numbers for a date range: tickets created and solved, open now, response and resolution times.", scopes: ["settings:read", "tickets:read"], read_only: true, destructive: false },
];

// ─── State ──────────────────────────────────────────────────────────────────

type MockRequest = Omit<OAuthRequest, "workspaces"> & { status: "pending" | "answered"; redirect: string };

const requests = new Map<string, MockRequest>();
/** Connected clients per workspace id; ws[0] starts with one so the list isn't empty. */
const connections = new Map<string, (AIClientConnection & { user_id: string })[]>();

function connectionsOf(ws: WsData) {
  let list = connections.get(ws.workspace.id);
  if (!list) {
    list = [];
    if (ws === db.workspaces[0]) {
      const at = new Date(Date.now() - 2 * 3600_000).toISOString();
      list.push({
        id: uuid(),
        client: { name: "Claude", uri: "https://claude.ai", redirect_host: "claude.ai", local: false },
        user: userRef(db.meId)!,
        user_id: db.meId,
        mine: true,
        scopes: ["tickets:read", "tickets:write", "contacts:read", "kb:read"],
        created_at: new Date(Date.now() - 9 * 86400_000).toISOString(),
        last_used_at: at,
      });
    }
    connections.set(ws.workspace.id, list);
  }
  return list;
}

function roleIn(ws: WsData): Role | null {
  return ws.members.find((m) => m.user_id === db.meId)?.role ?? null;
}

function audit(ws: WsData, action: string, id: string, data: Record<string, unknown>) {
  ws.audit.unshift({ id: uuid(), actor: { type: "user", user: userRef(db.meId), api_key_id: null }, action, entity_type: "oauth_grant", entity_id: id, data, ip: "84.152.33.17", created_at: nowIso() });
}

function requestFor(id: string): MockRequest {
  if (!/^[0-9a-f-]{36}$/i.test(id)) throw notFound("Request");
  let r = requests.get(id);
  if (!r) {
    r = {
      id,
      client: { name: "Claude", uri: "https://claude.ai", redirect_host: "claude.ai", local: false },
      scopes: [...OAUTH_SCOPES],
      expires_at: new Date(Date.now() + 10 * 60_000).toISOString(),
      status: "pending",
      // The mock stays in the app: the "client" lands on Settings > AI clients.
      redirect: `${location.origin}/settings/ai-clients`,
    };
    requests.set(id, r);
  }
  if (r.status !== "pending" || Date.parse(r.expires_at) < Date.now()) throw new HttpError(410, "request_expired", "This request expired or was already answered.");
  return r;
}

function withWorkspaces(r: MockRequest): OAuthRequest {
  const { status: _s, redirect: _r, ...rest } = r;
  const workspaces = db.workspaces
    .map((ws) => ({ ws, role: roleIn(ws) }))
    .filter((x): x is { ws: WsData; role: Role } => x.role !== null)
    .map(({ ws, role }) => ({ id: ws.workspace.id, name: ws.workspace.name, role, scopes: r.scopes.filter((s) => scopesForRole(role).includes(s)) }));
  return { ...rest, workspaces };
}

/** For tests: forget requests and connections. */
export function resetAiClients() {
  requests.clear();
  connections.clear();
}

// ─── Routes ─────────────────────────────────────────────────────────────────

export function registerAiClients() {
  get("/v1/ai/agent/setup", (ctx) => {
    assertAiInstance();
    requireAiAdmin(ctx.ws, "Only admins can see the agent setup");
    return agentSetup(ctx.ws);
  });

  get("/v1/ai-clients/server", (): AIClientServer => {
    const api = `${location.origin}`;
    return { url: `${api}/mcp`, resource_metadata: `${api}/.well-known/oauth-protected-resource/mcp`, authorization_server: api, scopes: [...OAUTH_SCOPES], tools: TOOLS };
  });

  get("/v1/ai-clients/connections", (ctx) => {
    const role = roleIn(ctx.ws);
    if (!role) throw forbidden();
    const all = role === "owner" || role === "admin";
    const data = connectionsOf(ctx.ws)
      .filter((c) => all || c.user_id === db.meId)
      .map(({ user_id, ...c }) => ({ ...c, mine: user_id === db.meId }));
    return { data };
  });

  del("/v1/ai-clients/connections/:id", (ctx) => {
    const role = roleIn(ctx.ws);
    const list = connectionsOf(ctx.ws);
    const c = list.find((x) => x.id === ctx.params.id);
    if (!c || (c.user_id !== db.meId && role !== "owner" && role !== "admin")) throw notFound("Connection");
    connections.set(ctx.ws.workspace.id, list.filter((x) => x !== c));
    audit(ctx.ws, "oauth.grant_revoked", c.id, { client: c.client.name, user_id: c.user_id, reason: "revoked" });
    return respond(204);
  });

  get("/v1/oauth/requests/:id", (ctx) => withWorkspaces(requestFor(ctx.params.id)));

  post("/v1/oauth/requests/:id/approve", (ctx) => {
    const r = requestFor(ctx.params.id);
    const wid = isRecord(ctx.body) && typeof ctx.body.workspace_id === "string" ? ctx.body.workspace_id : "";
    const ws = db.workspaces.find((w) => w.workspace.id === wid);
    const role = ws ? roleIn(ws) : null;
    if (!ws || !role) throw invalid("Choose a workspace you're a member of.");
    const scopes = r.scopes.filter((s) => scopesForRole(role).includes(s));
    if (!scopes.length) throw forbidden("Your role in this workspace can't use any of the scopes this app asked for.");
    r.status = "answered";
    const list = connectionsOf(ws).filter((c) => !(c.user_id === db.meId && c.client.name === r.client.name));
    const now = nowIso();
    const conn = { id: uuid(), client: r.client, user: userRef(db.meId)!, user_id: db.meId, mine: true, scopes, created_at: now, last_used_at: now };
    connections.set(ws.workspace.id, [conn, ...list]);
    audit(ws, "oauth.grant_created", conn.id, { client: r.client.name, scopes });
    return respond(200, { redirect_to: `${r.redirect}?code=mock_${uuid().slice(0, 8)}` });
  });

  post("/v1/oauth/requests/:id/deny", (ctx) => {
    const r = requestFor(ctx.params.id);
    r.status = "answered";
    return respond(200, { redirect_to: `${r.redirect}?error=access_denied` });
  });
}
