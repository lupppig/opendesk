// Several workspaces (contracts/api.md "Several workspaces"): how the person arranges
// them in the switcher (order, hidden) and the overview of every workspace's sidebar
// numbers, each as that workspace answers them.

import type { WorkspaceOverview, WorkspacePrefs } from "@/lib/api/types";
import { db } from "../db";
import { counts as ticketCounts } from "../tickets";
import { teamRole } from "../teams";
import { get, invalid, put, type Ctx } from "../router";
import { nowIso, randomToken } from "../util";
import { memberships } from "./auth";
import { collabCountsFor } from "./collab";

const prefs = new Map<string, WorkspacePrefs>();

function prefsOf(uid: string): WorkspacePrefs {
  let p = prefs.get(uid);
  if (!p) prefs.set(uid, (p = { order: [], hidden: [] }));
  return p;
}

const UUIDISH = /^[0-9a-f-]{36}$/;

function ids(v: unknown, what: string): string[] {
  if (!Array.isArray(v) || v.length > 100 || !v.every((x) => typeof x === "string" && UUIDISH.test(x))) throw invalid(`${what} takes at most 100 workspace ids.`);
  return [...new Set(v as string[])];
}

function update(ctx: Ctx): WorkspacePrefs {
  const b = ctx.body;
  for (const k of Object.keys(b)) if (k !== "order" && k !== "hidden") throw invalid(`Unknown field: ${k}`);
  const next: WorkspacePrefs = { ...prefsOf(db.meId) };
  if ("order" in b) next.order = ids(b.order, "order");
  if ("hidden" in b) next.hidden = ids(b.hidden, "hidden");
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

export function registerWorkspaces() {
  get("/v1/me/workspace-prefs", () => prefsOf(db.meId));
  put("/v1/me/workspace-prefs", (ctx) => update(ctx));
  get("/v1/me/overview", () => ({ workspaces: overview() }));
}
