// Workspaces together (contracts/api.md "Workspaces together"): the person's settings
// and the overview of every workspace's sidebar numbers, each as that workspace
// answers them. `?mock_together=separate|mixed|grouped|noeverywhere` starts the
// settings in that state (for recordings); a change then sticks for the session.

import type { WorkspaceOverview, WorkspacePrefs } from "@/lib/api/types";
import { db } from "../db";
import { counts as ticketCounts } from "../tickets";
import { teamRole } from "../teams";
import { get, invalid, put, type Ctx } from "../router";
import { nowIso, randomToken } from "../util";
import { memberships } from "./auth";
import { collabCountsFor } from "./collab";

const prefs = new Map<string, WorkspacePrefs>();

function initial(): WorkspacePrefs {
  const p: WorkspacePrefs = { mode: "together", mode_set: false, layout: "grouped", order: [], hidden: [], collapsed: [], items: { hidden: [], order: [] }, everywhere: true };
  const v = typeof location !== "undefined" ? new URLSearchParams(location.search).get("mock_together") : null;
  if (v === "separate") return { ...p, mode: "separate", mode_set: true };
  if (v === "mixed") return { ...p, layout: "mixed" };
  if (v === "noeverywhere") return { ...p, everywhere: false };
  return p;
}

function prefsOf(uid: string): WorkspacePrefs {
  let p = prefs.get(uid);
  if (!p) prefs.set(uid, (p = initial()));
  return p;
}

const UUIDISH = /^[0-9a-f-]{36}$/;
const ITEM = /^(?:[0-9a-f-]{36}|everywhere)\/[a-z_]{1,32}(?::[A-Za-z0-9_-]{1,64})?$/;

function ids(v: unknown, what: string, max = 100, ok: (x: string) => boolean = (x) => UUIDISH.test(x)): string[] {
  if (!Array.isArray(v) || v.length > max || !v.every((x) => typeof x === "string" && ok(x))) throw invalid(`${what} takes at most ${max} ids.`);
  return [...new Set(v as string[])];
}

function update(ctx: Ctx): WorkspacePrefs {
  const cur = prefsOf(db.meId);
  const b = ctx.body;
  const next: WorkspacePrefs = { ...cur, items: { ...cur.items } };
  if ("mode" in b) {
    if (b.mode === "") Object.assign(next, { mode: "together", mode_set: false });
    else if (b.mode === "together" || b.mode === "separate") Object.assign(next, { mode: b.mode, mode_set: true });
    else throw invalid("mode must be together or separate.");
  }
  if ("layout" in b) {
    if (b.layout !== "grouped" && b.layout !== "mixed") throw invalid("layout must be grouped or mixed.");
    next.layout = b.layout;
  }
  if ("order" in b) next.order = ids(b.order, "order");
  if ("hidden" in b) next.hidden = ids(b.hidden, "hidden");
  if ("collapsed" in b) next.collapsed = ids(b.collapsed, "collapsed", 101, (x) => x === "everywhere" || UUIDISH.test(x));
  if ("items" in b) {
    const it = (b.items ?? {}) as Record<string, unknown>;
    next.items = {
      hidden: "hidden" in it ? ids(it.hidden, "items", 500, (x) => ITEM.test(x)) : [],
      order: "order" in it ? ids(it.order, "items", 500, (x) => ITEM.test(x)) : [],
    };
  }
  if ("everywhere" in b) {
    if (typeof b.everywhere !== "boolean") throw invalid("everywhere is true or false.");
    next.everywhere = b.everywhere;
  }
  next.updated_at = nowIso();
  prefs.set(db.meId, next);
  return next;
}

function overview(): WorkspaceOverview[] {
  const me = db.meId;
  return memberships(me).map((m) => {
    const ws = db.workspaces.find((w) => w.workspace.id === m.workspace.id)!;
    const suspended = !!m.suspended;
    return {
      id: ws.workspace.id,
      name: ws.workspace.name,
      slug: ws.workspace.slug,
      avatar_url: ws.workspace.avatar_url,
      role: m.role,
      suspended,
      unread_notifications: suspended ? 0 : ws.notifications.filter((n) => !n.read_at).length,
      ticket_counts: suspended ? null : ticketCounts(ws, me),
      mail_counts: suspended ? null : collabCountsFor(ws),
    };
  });
}

/** A ws token the app can read: unsigned, with the claims a real one carries (contracts/tokens.md). */
export function mockWsToken(ctx: Ctx): string {
  const me = db.meId;
  const teamsIn = (wid: string) =>
    (db.workspaces.find((w) => w.workspace.id === wid)?.teams ?? []).filter((t) => t.private && teamRole(t, me)).map((t) => t.id);
  const claims: Record<string, unknown> = {
    purpose: "ws",
    sub: me,
    kind: "user",
    wid: ctx.ws.workspace.id,
    role: memberships(me).find((m) => m.workspace.id === ctx.ws.workspace.id)?.role ?? "viewer",
    teams: teamsIn(ctx.ws.workspace.id),
    jti: randomToken(12),
  };
  if (ctx.query.get("all_workspaces") === "true") {
    claims.workspaces = Object.fromEntries(
      memberships(me)
        .filter((m) => m.workspace.id !== ctx.ws.workspace.id && !m.suspended)
        .map((m) => [m.workspace.id, { role: m.role, teams: teamsIn(m.workspace.id) }]),
    );
  }
  const b64 = (o: unknown) => btoa(JSON.stringify(o)).replace(/=+$/, "").replace(/\+/g, "-").replace(/\//g, "_");
  return `${b64({ alg: "HS256", typ: "JWT" })}.${b64(claims)}.mock`;
}

export function registerTogether() {
  get("/v1/me/workspace-prefs", () => prefsOf(db.meId));
  put("/v1/me/workspace-prefs", (ctx) => update(ctx));
  get("/v1/me/overview", () => ({ workspaces: overview() }));
}
