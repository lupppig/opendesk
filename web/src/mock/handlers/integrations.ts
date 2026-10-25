// Mock integrations (contracts/integrations.md): the catalog, connections with fake
// tool lists, the service's sign-in (a redirect straight back to settings), and
// ticket links. State lives in sessionStorage so the sign-in reload keeps it.

import type { Integration, IntegrationProvider, IntegrationTool, Ticket, TicketLink } from "@/lib/api/types";
import { db, userRef, type WsData } from "../db";
import { publish } from "../gateway";
import { HttpError, del, forbidden, get, invalid, notFound, patch, post, respond, type Ctx } from "../router";
import { addActivity, findTicket } from "../tickets";
import { nowIso, uuid } from "../util";

const linkBack = " Link it to this ticket when it is created.";

const catalog: (IntegrationProvider & { hosts: string[] })[] = [
  { key: "linear", name: "Linear", category: "issues", description: "Create and update issues, and look up projects and cycles.", url: "https://mcp.linear.app/mcp", auth: "oauth", hosts: ["linear.app"],
    starters: [
      { label: "Create a Linear issue", prompt: "Create a Linear issue from this ticket: a short title, what the customer reported, steps to reproduce if known, and the impact." + linkBack },
      { label: "Find related Linear issues", prompt: "Search Linear for issues related to this ticket and tell me if one already covers it." },
    ] },
  { key: "github", name: "GitHub", category: "issues", description: "Open issues, read pull requests and search code.", url: "https://api.githubcopilot.com/mcp/", auth: "token", hosts: ["github.com"],
    token_help: "A fine-grained personal access token with access to the repositories Ollie may use.", token_url: "https://github.com/settings/personal-access-tokens/new",
    starters: [
      { label: "Open a GitHub issue", prompt: "Open a GitHub issue for this ticket in the right repository: a clear title, what happened, what was expected, and steps to reproduce." + linkBack },
      { label: "Find related GitHub issues", prompt: "Search GitHub issues and pull requests related to this ticket." },
    ] },
  { key: "atlassian", name: "Jira and Confluence", category: "issues", description: "Create Jira issues and search Confluence pages.", url: "https://mcp.atlassian.com/v1/mcp", auth: "oauth", hosts: ["atlassian.net"],
    starters: [
      { label: "Create a Jira issue", prompt: "Create a Jira issue from this ticket with a clear summary, description and the customer's impact." + linkBack },
      { label: "Search Confluence", prompt: "Search Confluence for pages that help answer this ticket." },
    ] },
  { key: "sentry", name: "Sentry", category: "issues", description: "Look up errors, their frequency and the releases they appeared in.", url: "https://mcp.sentry.dev/mcp", auth: "oauth", hosts: ["sentry.io"],
    starters: [{ label: "Find the error in Sentry", prompt: "Look in Sentry for errors that match what this customer describes and summarize what you find." }] },
  { key: "notion", name: "Notion", category: "docs", description: "Search pages and add notes to your workspace.", url: "https://mcp.notion.com/mcp", auth: "oauth", hosts: ["notion.so"],
    starters: [{ label: "Search Notion", prompt: "Search Notion for internal docs that help answer this ticket." }] },
  { key: "asana", name: "Asana", category: "issues", description: "Create tasks and follow up on projects.", url: "https://mcp.asana.com/sse", auth: "oauth", hosts: ["app.asana.com"],
    starters: [{ label: "Create an Asana task", prompt: "Create an Asana task for the follow-up this ticket needs." + linkBack }] },
  { key: "monday", name: "monday.com", category: "issues", description: "Create items on boards and update their status.", url: "https://mcp.monday.com/sse", auth: "oauth", hosts: ["monday.com"],
    starters: [{ label: "Create a monday.com item", prompt: "Create a monday.com item for this ticket on the right board." + linkBack }] },
  { key: "slack", name: "Slack", category: "chat", description: "Post to channels and search conversations.", url: "https://mcp.slack.com/mcp", auth: "oauth", hosts: ["slack.com"],
    starters: [{ label: "Ask the team in Slack", prompt: "Post a short summary of this ticket to the right Slack channel and ask the team for help." }] },
  { key: "intercom", name: "Intercom", category: "chat", description: "Look up conversations and contacts in Intercom.", url: "https://mcp.intercom.com/mcp", auth: "oauth", hosts: [],
    starters: [{ label: "Check Intercom history", prompt: "Look up this customer in Intercom and summarize their past conversations." }] },
  { key: "close", name: "Close", category: "crm", description: "Look up leads, deals and activity in your CRM.", url: "https://mcp.close.com/mcp", auth: "oauth", hosts: [],
    starters: [{ label: "Check the customer in Close", prompt: "Look up this customer in Close and tell me about their account and open deals." }] },
  { key: "stripe", name: "Stripe", category: "payments", description: "Look up customers, subscriptions, invoices and refunds.", url: "https://mcp.stripe.com", auth: "oauth", hosts: ["dashboard.stripe.com"],
    starters: [{ label: "Check billing in Stripe", prompt: "Look up this customer in Stripe and summarize their subscription, recent invoices and any failed payments." }] },
  { key: "paypal", name: "PayPal", category: "payments", description: "Look up transactions and invoices.", url: "https://mcp.paypal.com/mcp", auth: "oauth", hosts: [],
    starters: [{ label: "Check PayPal transactions", prompt: "Look up this customer's recent PayPal transactions." }] },
  { key: "square", name: "Square", category: "payments", description: "Look up orders, payments and customers.", url: "https://mcp.squareup.com/sse", auth: "oauth", hosts: [],
    starters: [{ label: "Check Square orders", prompt: "Look up this customer's recent Square orders and payments." }] },
  { key: "box", name: "Box", category: "files", description: "Search and read files.", url: "https://mcp.box.com/", auth: "oauth", hosts: ["app.box.com"],
    starters: [{ label: "Search Box", prompt: "Search Box for files that help answer this ticket." }] },
  { key: "zapier", name: "Zapier", category: "automation", description: "Reach thousands of apps through the actions you enable in Zapier.", url: "https://mcp.zapier.com/api/mcp/mcp", auth: "oauth", hosts: [], starters: [] },
];

const tool = (name: string, title: string, description: string, read_only: boolean): IntegrationTool => ({ name, title, description, read_only, read_only_hint: read_only, ask_first: false, auto_run: false, enabled: true });

const toolsFor: Record<string, IntegrationTool[]> = {
  linear: [
    tool("list_issues", "List issues", "Search issues by text, team, state or assignee.", true),
    tool("get_issue", "Get issue", "Read one issue with its comments.", true),
    tool("list_teams", "List teams", "The workspace's teams.", true),
    tool("create_issue", "Create issue", "Create an issue in a team.", false),
    tool("update_issue", "Update issue", "Change an issue's state, priority, assignee or labels.", false),
    tool("create_comment", "Comment on issue", "Add a comment to an issue.", false),
  ],
  github: [
    tool("search_issues", "Search issues", "Search issues and pull requests.", true),
    tool("get_issue", "Get issue", "Read one issue.", true),
    tool("list_pull_requests", "List pull requests", "Pull requests in a repository.", true),
    tool("create_issue", "Create issue", "Open an issue in a repository.", false),
    tool("add_issue_comment", "Comment on issue", "Add a comment to an issue.", false),
  ],
  atlassian: [
    tool("searchJiraIssuesUsingJql", "Search Jira", "Search issues with JQL.", true),
    tool("searchConfluenceUsingCql", "Search Confluence", "Search pages with CQL.", true),
    tool("createJiraIssue", "Create Jira issue", "Create an issue in a project.", false),
    tool("addCommentToJiraIssue", "Comment on Jira issue", "Add a comment to an issue.", false),
  ],
  sentry: [
    tool("search_issues", "Search issues", "Find error groups by text.", true),
    tool("get_issue_details", "Get issue details", "Frequency, releases and the latest event.", true),
    tool("update_issue", "Update issue", "Resolve, ignore or assign an issue.", false),
  ],
  slack: [tool("search_messages", "Search messages", "Search the workspace's messages.", true), tool("post_message", "Post message", "Post to a channel.", false)],
  stripe: [
    tool("search_customers", "Search customers", "Find customers by email or name.", true),
    tool("list_invoices", "List invoices", "A customer's invoices.", true),
    tool("list_subscriptions", "List subscriptions", "A customer's subscriptions.", true),
    tool("create_refund", "Create refund", "Refund a payment.", false),
  ],
  notion: [tool("search", "Search", "Search pages and databases.", true), tool("fetch", "Fetch page", "Read a page.", true), tool("create_pages", "Create pages", "Create pages.", false)],
};
const defaultTools = [tool("search", "Search", "Search the service.", true), tool("create_item", "Create item", "Create something new.", false)];

// ─── State ─────────────────────────────────────────────────────────────────

type State = { integrations: Integration[]; links: TicketLink[] };
const KEY = "od-mock-integrations";
const state = new Map<string, State>();

function load(): Record<string, State> {
  try {
    return JSON.parse(sessionStorage.getItem(KEY) ?? "{}") as Record<string, State>;
  } catch {
    return {};
  }
}
function save() {
  try {
    sessionStorage.setItem(KEY, JSON.stringify(Object.fromEntries(state)));
  } catch {
    /* private mode: lasts until reload */
  }
}

function seedState(ws: WsData): State {
  const at = new Date(Date.now() - 6 * 86_400_000).toISOString();
  const linear = connection("linear", "oauth", at);
  const st: State = { integrations: [linear], links: [] };
  const t = ws.tickets.find((x) => !ws.deletedTickets.has(x.id) && x.status === "open" && /bug|error|crash|broken|fail/i.test(x.subject));
  if (t) st.links.push({ id: uuid(), ticket_id: t.id, integration_id: linear.id, provider: "linear", url: `https://linear.app/margin/issue/FN-${t.number - 940}`, title: "Crash when opening shared notebooks on iOS", actor_type: "ai", created_by: db.meId, created_at: at });
  return st;
}

export function stateOf(ws: WsData): State {
  const id = ws.workspace.id;
  let st = state.get(id);
  if (!st) {
    st = load()[id] ?? seedState(ws);
    state.set(id, st);
  }
  return st;
}

function connection(key: string, auth: Integration["auth_type"], at = nowIso(), custom?: { url: string; name: string }): Integration {
  const p = catalog.find((x) => x.key === key);
  const url = custom?.url ?? p?.url ?? "";
  return {
    id: uuid(),
    provider: p ? key : "custom",
    name: custom?.name || p?.name || new URL(url).host,
    host: new URL(url).host,
    url: p ? url : undefined,
    icon_url: null,
    auth_type: auth,
    status: "connected",
    enabled: true,
    last_error: "",
    tools: (toolsFor[key] ?? defaultTools).map((x) => ({ ...x })),
    starters: p?.starters ?? [],
    synced_at: at,
    created_at: at,
  };
}

function admin(ws: WsData) {
  const r = ws.members.find((m) => m.user_id === db.meId)?.role;
  if (r !== "owner" && r !== "admin") throw forbidden("Only admins can manage integrations.");
}

function find(ctx: Ctx): Integration {
  const x = stateOf(ctx.ws).integrations.find((i) => i.id === ctx.params.id);
  if (!x) throw notFound("Integration");
  return x;
}

function audit(ws: WsData, action: string, x: Integration) {
  ws.audit.unshift({ id: uuid(), actor: { type: "user", user: userRef(db.meId), api_key_id: null }, action, entity_type: "integration", entity_id: x.id,
    data: { provider: x.provider, host: x.host }, ip: "84.152.33.17", created_at: nowIso() });
}

/** Connected products Ollie may use, with the hosts their links live on. */
export function connected(ws: WsData) {
  return stateOf(ws).integrations.filter((x) => x.enabled && x.status === "connected").map((x) => ({ x, hosts: catalog.find((p) => p.key === x.provider)?.hosts ?? [] }));
}

/** Links a ticket to something in another tool (as the server does after an approved create). */
export function linkTicket(ws: WsData, t: Ticket, x: Integration | null, url: string, title: string, actor: "user" | "ai"): TicketLink {
  const st = stateOf(ws);
  const existing = st.links.find((l) => l.ticket_id === t.id && l.url === url);
  if (existing) return existing;
  const l: TicketLink = { id: uuid(), ticket_id: t.id, integration_id: x?.id ?? null, provider: x?.provider ?? "link", url, title: title || new URL(url).host, actor_type: actor, created_by: db.meId, created_at: nowIso() };
  st.links.push(l);
  save();
  publish(ws, "ticket_link.created", { ticket_id: t.id, link: l });
  const a = addActivity(ws, t, "linked", { link_id: l.id, url: l.url, title: l.title, provider: l.provider }, userRef(db.meId), nowIso(), false);
  if (actor === "ai") a.actor = { type: "ai", user: userRef(db.meId) };
  publish(ws, "activity.created", { ticket_id: t.id, activity: a });
  return l;
}

export function registerIntegrations() {
  get("/v1/integrations/catalog", () => ({ data: catalog.map(({ hosts: _h, ...p }) => p) }));
  get("/v1/integrations", (ctx) => ({ data: stateOf(ctx.ws).integrations }));

  post("/v1/integrations", (ctx) => {
    admin(ctx.ws);
    const st = stateOf(ctx.ws);
    if (st.integrations.length >= 25) throw new HttpError(409, "conflict", "A workspace can have at most 25 integrations.");
    const key = String(ctx.body.provider ?? "");
    const p = catalog.find((x) => x.key === key);
    let x: Integration;
    if (p) {
      if (p.auth === "token" && !String(ctx.body.token ?? "").trim()) throw invalid("Paste the access token.");
      if (p.auth === "token" && String(ctx.body.token).trim().length < 8) throw new HttpError(422, "integration_failed", "Couldn't connect: the server refused access (401). Check the token.");
      x = connection(key, p.auth);
    } else if (key === "custom") {
      const raw = String(ctx.body.url ?? "").trim();
      let u: URL;
      try {
        u = new URL(raw);
      } catch {
        throw new HttpError(422, "invalid_url", "Enter the server's full URL.");
      }
      if (u.protocol !== "https:") throw new HttpError(422, "invalid_url", "Use an https:// URL.");
      if (/^(localhost|127\.|10\.|192\.168\.|169\.254\.)/.test(u.hostname)) throw new HttpError(422, "invalid_url", "destination is not a public internet address");
      const auth = (ctx.body.auth as Integration["auth_type"]) || "oauth";
      x = connection("custom", auth, nowIso(), { url: u.href, name: String(ctx.body.name ?? "").trim().slice(0, 64) });
    } else throw invalid(`Unknown provider: ${key.slice(0, 40)}`);
    if (typeof ctx.body.name === "string" && ctx.body.name.trim() && p) x.name = ctx.body.name.trim().slice(0, 64);
    const oauth = x.auth_type === "oauth";
    if (oauth) {
      x.status = "pending";
      x.tools = [];
      x.synced_at = null;
    }
    st.integrations.unshift(x);
    save();
    publish(ctx.ws, "integration.created", { integration: x });
    if (!oauth) audit(ctx.ws, "integration.connected", x);
    // The service's sign-in page is simulated: approve right away, then come back.
    if (oauth) {
      finishSignIn(ctx.ws, x);
      return respond(201, { integration: x, authorize_url: `/settings/integrations?connected=${x.id}` });
    }
    return respond(201, { integration: x });
  });

  post("/v1/integrations/:id/authorize", (ctx) => {
    admin(ctx.ws);
    const x = find(ctx);
    if (x.auth_type !== "oauth") throw invalid("This integration signs in with a token. Update the token instead.");
    finishSignIn(ctx.ws, x);
    return { integration: x, authorize_url: `/settings/integrations?connected=${x.id}` };
  });

  post("/v1/integrations/:id/sync", (ctx) => {
    admin(ctx.ws);
    const x = find(ctx);
    x.synced_at = nowIso();
    save();
    publish(ctx.ws, "integration.updated", { integration: x });
    return x;
  });

  patch("/v1/integrations/:id", (ctx) => {
    admin(ctx.ws);
    const x = find(ctx);
    if (typeof ctx.body.name === "string") {
      const n = ctx.body.name.trim();
      if (!n) throw invalid("Name is required");
      x.name = n.slice(0, 64);
    }
    if (typeof ctx.body.enabled === "boolean") x.enabled = ctx.body.enabled;
    if (Array.isArray(ctx.body.disabled_tools)) {
      const off = new Set(ctx.body.disabled_tools as string[]);
      x.tools = x.tools.map((tl) => ({ ...tl, enabled: !off.has(tl.name) }));
    }
    if (Array.isArray(ctx.body.ask_tools)) {
      // A tool its server marks read-only waits for approval while it is on the list.
      const ask = new Set(ctx.body.ask_tools as string[]);
      x.tools = x.tools.map((tl) => {
        const hint = tl.read_only_hint ?? tl.read_only;
        return { ...tl, read_only_hint: hint, ask_first: ask.has(tl.name), read_only: hint && !ask.has(tl.name), auto_run: !!tl.auto_run && !ask.has(tl.name) };
      });
    }
    if (Array.isArray(ctx.body.auto_tools)) {
      // Only a write runs without asking, and asking first wins.
      const auto = new Set(ctx.body.auto_tools as string[]);
      x.tools = x.tools.map((tl) => ({ ...tl, auto_run: auto.has(tl.name) && !tl.read_only && !tl.ask_first }));
    }
    save();
    publish(ctx.ws, "integration.updated", { integration: x });
    audit(ctx.ws, "integration.updated", x);
    return x;
  });

  del("/v1/integrations/:id", (ctx) => {
    admin(ctx.ws);
    const x = find(ctx);
    const st = stateOf(ctx.ws);
    st.integrations = st.integrations.filter((i) => i !== x);
    for (const l of st.links) if (l.integration_id === x.id) l.integration_id = null;
    save();
    publish(ctx.ws, "integration.deleted", { integration_id: x.id });
    audit(ctx.ws, "integration.disconnected", x);
    return respond(204);
  });

  get("/v1/tickets/:id/links", (ctx) => {
    const t = findTicket(ctx.ws, ctx.params.id);
    if (!t) throw notFound("Ticket");
    return { data: stateOf(ctx.ws).links.filter((l) => l.ticket_id === t.id) };
  });
  post("/v1/tickets/:id/links", (ctx) => {
    const t = findTicket(ctx.ws, ctx.params.id);
    if (!t) throw notFound("Ticket");
    const url = String(ctx.body.url ?? "").trim();
    if (!/^https:\/\/[^/\s]+/.test(url)) throw invalid("Use a full https:// link.");
    const x = stateOf(ctx.ws).integrations.find((i) => i.id === ctx.body.integration_id) ?? null;
    return respond(201, linkTicket(ctx.ws, t, x, url, String(ctx.body.title ?? "").trim().slice(0, 300), "user"));
  });
  del("/v1/tickets/:id/links/:linkId", (ctx) => {
    const st = stateOf(ctx.ws);
    const l = st.links.find((x) => x.id === ctx.params.linkId && x.ticket_id === ctx.params.id);
    const t = findTicket(ctx.ws, ctx.params.id);
    if (!l || !t) throw notFound("Link");
    st.links = st.links.filter((x) => x !== l);
    save();
    publish(ctx.ws, "ticket_link.deleted", { ticket_id: t.id, link_id: l.id });
    addActivity(ctx.ws, t, "unlinked", { url: l.url, title: l.title, provider: l.provider }, userRef(db.meId));
    return respond(204);
  });
}

function finishSignIn(ws: WsData, x: Integration) {
  x.status = "connected";
  x.last_error = "";
  x.tools = (toolsFor[x.provider] ?? defaultTools).map((t) => ({ ...t }));
  x.synced_at = nowIso();
  save();
  publish(ws, "integration.updated", { integration: x });
  audit(ws, "integration.connected", x);
}
