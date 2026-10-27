// The wiki in mock mode (contracts/wiki.md): the tree, pages with versions, history
// with coalesced revisions, moves that refuse cycles, deletes that take subpages along,
// and search. Checked as the API checks them. The first workspace starts with a small
// support handbook; the others start empty, to show the first-run state.

import { sanitizeHtml } from "@/lib/sanitize";
import type { Role, WikiNode, WikiPage, WikiRevision, WikiSearchHit } from "@/lib/api/types";
import { MAX_DEPTH, ancestorsOf, applyMove, buildTree, canPlace, childrenOf, descendantsOf, ownership, removeSubtree, upsertNode } from "@/lib/wiki/tree";
import { db, userRef, type WsData } from "../db";
import { publish } from "../gateway";
import { del, get, HttpError, invalid, notFound, patch, post, respond, s, type Ctx } from "../router";
import { roleIn } from "../teams";
import { ago, DAY, HOUR, MIN, nowIso, stripHtml, uuid } from "../util";

type Row = Omit<WikiNode, "created_by_id"> & { body_html: string; created_by: string | null; updated_by: string | null; created_at: string };
type Rev = { id: string; page_id: string; title: string; icon: string; body_html: string; author: string | null; created_at: string; updated_at: string };
type State = { pages: Row[]; revisions: Rev[] };

const MAX_PAGES = 5000;
const MAX_BODY = 512 * 1024;
const MAX_REVISIONS = 100;
const COALESCE_GAP = 10 * MIN;
const COALESCE_SPAN = HOUR;

const states = new WeakMap<WsData, State>();

function stateOf(ws: WsData): State {
  let st = states.get(ws);
  if (!st) {
    st = { pages: [], revisions: [] };
    states.set(ws, st);
    if (ws === db.workspaces[0]) seedHandbook(ws, st);
  }
  return st;
}

const rank: Record<Role, number> = { viewer: 1, agent: 2, admin: 3, owner: 4 };
const role = (ctx: Ctx) => roleIn(ctx.ws, db.meId);

function requireAgent(ctx: Ctx) {
  if (rank[role(ctx)] < rank.agent) throw new HttpError(403, "forbidden", "You don't have permission to do that");
}

const node = (r: Row): WikiNode => ({ id: r.id, parent_id: r.parent_id, title: r.title, icon: r.icon, position: r.position, version: r.version, updated_at: r.updated_at, created_by_id: r.created_by });
const nodes = (st: State) => st.pages.map(node);
const refOf = (n: Pick<WikiNode, "id" | "title" | "icon">) => ({ id: n.id, title: n.title, icon: n.icon });

function pageOut(st: State, r: Row): WikiPage {
  const tree = buildTree(nodes(st));
  return {
    ...node(r),
    body_html: r.body_html,
    created_at: r.created_at,
    created_by: userRef(r.created_by),
    updated_by: userRef(r.updated_by),
    breadcrumbs: ancestorsOf(tree, r.id).map(refOf),
    children: childrenOf(tree, r.id).map(refOf),
  };
}

function find(ctx: Ctx, st = stateOf(ctx.ws)): Row {
  const r = st.pages.find((p) => p.id === ctx.params.id);
  if (!r) throw notFound("Page");
  return r;
}

/** Writes positions and parents from the tree helpers back onto the rows. */
function sync(st: State, next: WikiNode[]) {
  const by = new Map(next.map((n) => [n.id, n]));
  st.pages = st.pages.filter((r) => by.has(r.id));
  for (const r of st.pages) {
    const n = by.get(r.id)!;
    r.parent_id = n.parent_id;
    r.position = n.position;
  }
}

function cleanTitle(v: unknown): string {
  if (typeof v !== "string") return "";
  if ([...v].length > 200) throw invalid("title must be at most 200 characters");
  return v.replace(/\s+/g, " ").trim();
}

// One emoji as the API reads it (internal/helpdesk/wiki/emoji.go): pictographs with a
// variation selector and skin tone, joined by ZWJ; a flag; a tag flag; or a keycap.
const pict = String.raw`\p{Extended_Pictographic}[\u{FE0E}\u{FE0F}]?[\u{1F3FB}-\u{1F3FF}]?`;
const oneEmoji = new RegExp(
  String.raw`^(?:${pict}(?:\u{200D}${pict})*|[\u{1F1E6}-\u{1F1FF}]{2}|[0-9#*]\u{FE0F}?\u{20E3}|\u{1F3F4}[\u{E0020}-\u{E007E}]+\u{E007F})$`,
  "u",
);

function cleanIcon(v: unknown): string {
  if (typeof v !== "string" || !v.trim()) return "";
  const t = v.trim();
  if (t.length > 64 || !oneEmoji.test(t)) throw invalid("icon must be one emoji");
  return t;
}

function cleanBody(v: unknown): string {
  const html = typeof v === "string" ? v : "";
  if (new TextEncoder().encode(html).length > MAX_BODY) throw new HttpError(422, "page_too_large", "A page can hold up to 512 KB of text and formatting. Split it into subpages.");
  // The server sanitizes with its rich-text allowlist; the mock uses the app's own allowlist.
  return sanitizeHtml(html);
}

function record(st: State, r: Row, merge: boolean) {
  const now = Date.now();
  const mine = st.revisions.filter((x) => x.page_id === r.id).sort((a, b) => Date.parse(b.updated_at) - Date.parse(a.updated_at));
  const last = mine[0];
  if (merge && last && r.updated_by && last.author === r.updated_by && now - Date.parse(last.updated_at) < COALESCE_GAP && now - Date.parse(last.created_at) < COALESCE_SPAN) {
    Object.assign(last, { title: r.title, icon: r.icon, body_html: r.body_html, updated_at: nowIso() });
    return;
  }
  const fresh: Rev = { id: uuid(), page_id: r.id, title: r.title, icon: r.icon, body_html: r.body_html, author: r.updated_by, created_at: nowIso(), updated_at: nowIso() };
  // The newest 100 stay: this one and the 99 before it.
  const keep = new Set([fresh.id, ...mine.slice(0, MAX_REVISIONS - 1).map((x) => x.id)]);
  st.revisions = [...st.revisions, fresh].filter((x) => x.page_id !== r.id || keep.has(x.id));
}

const revOut = (x: Rev, body: boolean): WikiRevision => ({
  id: x.id,
  title: x.title,
  icon: x.icon,
  ...(body ? { body_html: x.body_html } : {}),
  author: userRef(x.author),
  created_at: x.created_at,
  updated_at: x.updated_at,
});

function announce(ws: WsData, type: string, r: Row) {
  publish(ws, type, { wiki_page: node(r), updated_by: userRef(r.updated_by) });
}

/** The workspace's best matching pages, as GET /v1/wiki/search answers. */
export function searchWiki(ws: WsData, q: string, limit = 20): WikiSearchHit[] {
  const st = stateOf(ws);
  const words = q.toLowerCase().match(/[\p{L}\p{N}]+/gu) ?? [];
  if (!words.length) return [];
  const tree = buildTree(nodes(st));
  const scored = st.pages
    .map((r) => {
      const title = r.title.toLowerCase();
      const text = stripHtml(r.body_html);
      const lower = text.toLowerCase();
      const has = (w: string, s: string) => new RegExp(`(^|[^\\p{L}\\p{N}])${w.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}`, "u").test(s);
      if (!words.every((w) => has(w, title) || has(w, lower))) return null;
      const score = words.reduce((n, w) => n + (has(w, title) ? 3 : 0) + (has(w, lower) ? 1 : 0), 0);
      const at = Math.max(0, lower.indexOf(words[0] ?? "") - 40);
      const snippet = text.slice(at, at + 180).trim();
      return { r, score, snippet };
    })
    .filter((x): x is { r: Row; score: number; snippet: string } => !!x)
    .sort((a, b) => b.score - a.score || Date.parse(b.r.updated_at) - Date.parse(a.r.updated_at));
  return scored.slice(0, limit).map(({ r, snippet }) => ({ ...refOf(r), parent_id: r.parent_id, snippet, path: ancestorsOf(tree, r.id).map(refOf), updated_at: r.updated_at }));
}

export function registerWiki() {
  get("/v1/wiki/pages", (ctx) => {
    const st = stateOf(ctx.ws);
    const tree = buildTree(nodes(st));
    const out: WikiNode[] = [];
    const walk = (p: string | null) => childrenOf(tree, p).forEach((n) => (out.push(n), walk(n.id)));
    walk(null);
    return { data: out, next_cursor: null };
  });

  get("/v1/wiki/pages/:id", (ctx) => {
    const st = stateOf(ctx.ws);
    return pageOut(st, find(ctx, st));
  });

  post("/v1/wiki/pages", (ctx) => {
    requireAgent(ctx);
    const st = stateOf(ctx.ws);
    if (st.pages.length >= MAX_PAGES) throw new HttpError(422, "wiki_full", "A workspace's wiki holds up to 5,000 pages. Delete pages you no longer need first.");
    const parent = s(ctx, "parent_id")?.trim() || null;
    const tree = buildTree(nodes(st));
    if (parent) {
      if (!tree.byId.has(parent)) throw invalid("parent page not found in this workspace");
      if (ancestorsOf(tree, parent).length + 2 > MAX_DEPTH) throw new HttpError(422, "page_too_deep", "Pages nest up to 10 levels deep.");
    }
    const siblings = childrenOf(tree, parent).length;
    const pos = typeof ctx.body.position === "number" ? Math.max(0, Math.min(ctx.body.position, siblings)) : siblings;
    const now = nowIso();
    const r: Row = {
      id: uuid(),
      parent_id: parent,
      title: cleanTitle(ctx.body.title),
      icon: cleanIcon(ctx.body.icon),
      body_html: cleanBody(ctx.body.body_html),
      position: pos,
      version: 1,
      created_by: db.meId,
      updated_by: db.meId,
      created_at: now,
      updated_at: now,
    };
    const next = upsertNode(nodes(st), node(r));
    st.pages.push(r);
    sync(st, next);
    record(st, r, false);
    announce(ctx.ws, "wiki_page.created", r);
    return respond(201, pageOut(st, r));
  });

  patch("/v1/wiki/pages/:id", (ctx) => {
    requireAgent(ctx);
    const st = stateOf(ctx.ws);
    const r = find(ctx, st);
    if (typeof ctx.body.version === "number" && ctx.body.version !== r.version)
      throw new HttpError(409, "page_changed", "Someone else changed this page since you opened it. Reload it to see their changes.");
    const next = {
      title: "title" in ctx.body ? cleanTitle(ctx.body.title) : r.title,
      icon: "icon" in ctx.body ? cleanIcon(ctx.body.icon) : r.icon,
      body_html: "body_html" in ctx.body ? cleanBody(ctx.body.body_html) : r.body_html,
    };
    if (next.title !== r.title || next.icon !== r.icon || next.body_html !== r.body_html) {
      Object.assign(r, next, { version: r.version + 1, updated_by: db.meId, updated_at: nowIso() });
      record(st, r, true);
      announce(ctx.ws, "wiki_page.updated", r);
    }
    return pageOut(st, r);
  });

  post("/v1/wiki/pages/:id/move", (ctx) => {
    requireAgent(ctx);
    const st = stateOf(ctx.ws);
    const r = find(ctx, st);
    const parent = s(ctx, "parent_id")?.trim() || null;
    const tree = buildTree(nodes(st));
    if (parent && !tree.byId.has(parent)) throw invalid("parent page not found in this workspace");
    if (parent && (parent === r.id || ancestorsOf(tree, parent).some((a) => a.id === r.id)))
      throw new HttpError(422, "page_cycle", "A page can't move under itself or one of its subpages.");
    if (!canPlace(tree, r.id, parent)) throw new HttpError(422, "page_too_deep", "Pages nest up to 10 levels deep.");
    const position = typeof ctx.body.position === "number" ? ctx.body.position : 0;
    sync(st, applyMove(nodes(st), r.id, { parent, position }));
    announce(ctx.ws, "wiki_page.moved", r);
    return pageOut(st, r);
  });

  del("/v1/wiki/pages/:id", (ctx) => {
    requireAgent(ctx);
    const st = stateOf(ctx.ws);
    const r = find(ctx, st);
    const tree = buildTree(nodes(st));
    const owned = ownership(tree, r.id, db.meId);
    if (rank[role(ctx)] < rank.admin && owned !== "yes")
      throw new HttpError(
        403,
        "forbidden",
        owned === "page" ? "Someone else created this page, so only an admin can delete it." : "Someone else created pages under this one, so only an admin can delete it.",
      );
    const gone = [r.id, ...descendantsOf(tree, r.id).map((n) => n.id)];
    sync(st, removeSubtree(nodes(st), gone));
    st.revisions = st.revisions.filter((x) => !gone.includes(x.page_id));
    publish(ctx.ws, "wiki_page.deleted", { wiki_page_id: r.id, wiki_page_ids: gone });
    return respond(204);
  });

  get("/v1/wiki/pages/:id/revisions", (ctx) => {
    const st = stateOf(ctx.ws);
    const r = find(ctx, st);
    const list = st.revisions.filter((x) => x.page_id === r.id).sort((a, b) => Date.parse(b.updated_at) - Date.parse(a.updated_at));
    return { data: list.map((x) => revOut(x, false)), next_cursor: null };
  });

  get("/v1/wiki/pages/:id/revisions/:rid", (ctx) => {
    const st = stateOf(ctx.ws);
    const r = find(ctx, st);
    const x = st.revisions.find((v) => v.page_id === r.id && v.id === ctx.params.rid);
    if (!x) throw notFound("Revision");
    return revOut(x, true);
  });

  post("/v1/wiki/pages/:id/revisions/:rid/restore", (ctx) => {
    requireAgent(ctx);
    const st = stateOf(ctx.ws);
    const r = find(ctx, st);
    const x = st.revisions.find((v) => v.page_id === r.id && v.id === ctx.params.rid);
    if (!x) throw notFound("Revision");
    Object.assign(r, { title: x.title, icon: x.icon, body_html: x.body_html, version: r.version + 1, updated_by: db.meId, updated_at: nowIso() });
    record(st, r, false);
    announce(ctx.ws, "wiki_page.updated", r);
    return pageOut(st, r);
  });

  get("/v1/wiki/search", (ctx) => {
    const q = ctx.query.get("q") ?? "";
    const limit = Math.min(Number(ctx.query.get("limit") ?? 20) || 20, 50);
    return { data: searchWiki(ctx.ws, q, limit), next_cursor: null };
  });
}

// ─── The starter handbook ──────────────────────────────────────────────────

type Seed = { title: string; icon: string; body: string; days: number; by: number; children?: Seed[] };

const HANDBOOK: Seed[] = [
  {
    title: "Support handbook",
    icon: "📘",
    days: 40,
    by: 0,
    body: "<p>How our team works: who answers what, how fast, and what to do when it gets hard. Start with the escalation process if you are new.</p><h2>Our promises</h2><ul><li><p>A first answer within 4 business hours.</p></li><li><p>Every customer gets a name, never a queue.</p></li><li><p>We say when we don't know yet, and when we'll know.</p></li></ul>",
    children: [
      {
        title: "Escalation process",
        icon: "🚨",
        days: 3,
        by: 1,
        body: "<p>Escalate when a customer is blocked, money is at stake or you have tried twice without progress.</p><h2>Steps</h2><ol><li><p>Set the ticket to <strong>urgent</strong> and add the <code>escalated</code> label.</p></li><li><p>Leave a note with what you tried and what the customer needs.</p></li><li><p>Assign it to the on-call lead (see Business hours and on-call).</p></li><li><p>Tell the customer who has it now and when they will hear back.</p></li></ol><h2>Sev1 outages</h2><p>Page the on-call engineer from the incident channel. Post a status update every 30 minutes until it is resolved.</p>",
      },
      {
        title: "Refund policy",
        icon: "💸",
        days: 9,
        by: 2,
        body: "<p>Refund within 30 days of a charge, no questions asked, for any plan.</p><ul><li><p>Annual plans: refund the unused months after 30 days, if the customer asks.</p></li><li><p>Duplicate charges: refund at once and apologise.</p></li><li><p>Chargebacks: don't refund twice. Hand it to billing.</p></li></ul>",
        children: [
          {
            title: "Refunds over 100 euros",
            icon: "",
            days: 12,
            by: 2,
            body: "<p>A team lead approves refunds over 100 euros. Leave a note with the amount and the reason, and assign the ticket to a lead. They answer within one business day.</p>",
          },
        ],
      },
      {
        title: "Business hours and on-call",
        icon: "🕐",
        days: 20,
        by: 0,
        body: "<p>We answer Monday to Friday, 8:00 to 18:00 CET. Outside those hours, one lead is on call for sev1 and sev2 issues.</p><p>The on-call rota lives in the team calendar. Swap shifts in the team channel and update the calendar.</p>",
      },
    ],
  },
  {
    title: "Onboarding checklist",
    icon: "👋",
    days: 15,
    by: 1,
    body: "<p>Your first two weeks on the team.</p><h2>Week one</h2><ul><li><p>Read the support handbook.</p></li><li><p>Shadow a teammate for two shifts.</p></li><li><p>Answer ten tickets with a buddy reviewing your drafts.</p></li></ul><h2>Week two</h2><ul><li><p>Take your own queue for half a day.</p></li><li><p>Write or improve one help article.</p></li></ul>",
  },
  {
    title: "Tone of voice",
    icon: "💬",
    days: 60,
    by: 0,
    body: "<p>Warm, plain and specific. Use the customer's name, say what you did and what happens next. No jargon, no exclamation marks in apologies, no \"per my last email\".</p>",
  },
];

function seedHandbook(ws: WsData, st: State) {
  const people = ws.members.map((m) => m.user_id);
  const add = (list: Seed[], parent: string | null) =>
    list.forEach((x, i) => {
      const at = ago(x.days * DAY);
      const by = people[x.by % Math.max(people.length, 1)] ?? null;
      const r: Row = { id: uuid(), parent_id: parent, title: x.title, icon: x.icon, body_html: x.body, position: i, version: 3, created_by: by, updated_by: by, created_at: ago((x.days + 30) * DAY), updated_at: at };
      st.pages.push(r);
      st.revisions.push(
        { id: uuid(), page_id: r.id, title: r.title, icon: r.icon, body_html: x.body.replace(/<h2>[\s\S]*$/, ""), author: by, created_at: r.created_at, updated_at: ago((x.days + 29) * DAY) },
        { id: uuid(), page_id: r.id, title: r.title, icon: r.icon, body_html: r.body_html, author: by, created_at: at, updated_at: at },
      );
      if (x.children) add(x.children, r.id);
    });
  add(HANDBOOK, null);
}
