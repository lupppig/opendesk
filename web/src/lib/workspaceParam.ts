// `?ws=<workspace id>` on any app URL (contracts/api.md "Core notes"): links in
// notification emails name the workspace they are about. The app opens that
// workspace when the person is in it, then drops the parameter.

type Held = { user: { id: string }; memberships: { workspace: { id: string } }[] };

const ID = /^[\w-]{1,64}$/;

/**
 * The `ws` parameter of a parsed search: the id, `null` when it is there but not
 * an id (it is still dropped), `undefined` when there is none.
 */
export function workspaceParam(search: Record<string, unknown>): string | null | undefined {
  if (!("ws" in search)) return undefined;
  const v = search.ws;
  const s = typeof v === "number" ? String(v) : v;
  return typeof s === "string" && ID.test(s) ? s : null;
}

/**
 * An app href that names its workspace (`?ws=<id>`), so a reload, a shared link or a
 * notification opens it there whichever workspace has the focus. Without an id, as is.
 */
export function withWorkspaceParam(href: string, wid: string | null | undefined): string {
  if (!wid || !ID.test(wid)) return href;
  const u = new URL(href, "http://app.invalid");
  u.searchParams.set("ws", wid);
  return `${u.pathname}${u.search}${u.hash}`;
}

/** The same app href without `ws`: path, the other parameters and the hash. */
export function stripWorkspaceParam(href: string): string {
  const u = new URL(href, "http://app.invalid");
  u.searchParams.delete("ws");
  return `${u.pathname}${u.search}${u.hash}`;
}

/**
 * What opening the workspace takes: nothing (it is already open), selecting it
 * in the active account, switching to another account signed in to in this
 * browser that is in it, or nothing at all (the parameter is just dropped).
 */
export function workspaceTarget(
  ws: string,
  s: { user: { id: string } | null; workspaceId: string | null; memberships: { workspace: { id: string } }[]; accounts: Held[] },
): { kind: "current" } | { kind: "select" } | { kind: "account"; userId: string } | { kind: "none" } {
  if (s.workspaceId === ws) return { kind: "current" };
  if (s.memberships.some((m) => m.workspace.id === ws)) return { kind: "select" };
  const other = s.accounts.find((a) => a.user.id !== s.user?.id && a.memberships.some((m) => m.workspace.id === ws));
  return other ? { kind: "account", userId: other.user.id } : { kind: "none" };
}
