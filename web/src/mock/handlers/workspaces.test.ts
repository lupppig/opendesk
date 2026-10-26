import { describe, expect, it } from "vitest";
import type { WorkspaceOverview, WorkspacePrefs } from "@/lib/api/types";
import { tokenWorkspaces } from "@/lib/realtime/manager";
import { installMock } from "../index";
import { dispatch } from "../router";
import { db } from "../db";

async function call(method: string, path: string, body?: unknown, ws: string | undefined = db.workspaces[0]?.workspace.id) {
  const r = await dispatch(`http://mock${path}`, { method, body: body ? JSON.stringify(body) : undefined, headers: ws ? { "OD-Workspace": ws } : {} });
  return { status: r.status, body: r.status === 204 ? null : await r.json() };
}

describe("mock workspaces", () => {
  installMock();

  it("keeps the person's order and hidden workspaces, and nothing else", async () => {
    const start = (await call("GET", "/v1/me/workspace-prefs")).body as WorkspacePrefs;
    expect(start).toEqual({ order: [], hidden: [] });
    const lumen = db.workspaces[1].workspace.id;
    const put = await call("PUT", "/v1/me/workspace-prefs", { order: [lumen] });
    expect(put.status).toBe(200);
    expect((put.body as WorkspacePrefs).order).toEqual([lumen]);
    expect((await call("PUT", "/v1/me/workspace-prefs", { hidden: ["not-a-workspace"] })).status).toBe(422);
    expect((await call("PUT", "/v1/me/workspace-prefs", { mode: "separate" })).status).toBe(422);
    expect(((await call("PUT", "/v1/me/workspace-prefs", { hidden: [lumen] })).body as WorkspacePrefs).order).toEqual([lumen]);
  });

  it("answers every workspace of the person with its own numbers, never another's", async () => {
    const list = ((await call("GET", "/v1/me/overview")).body as { workspaces: WorkspaceOverview[] }).workspaces;
    const names = list.map((w) => w.name);
    expect(names).toContain("Margin");
    expect(names).toContain("Lumen Labs");
    // Harbor is the second account's, not this person's.
    expect(names).not.toContain("Harbor Studio");
    const lumen = list.find((w) => w.name === "Lumen Labs")!;
    expect(lumen.role).toBe("agent");
    expect(lumen.ticket_counts?.mine).toBeGreaterThanOrEqual(2);
    // The private Founders team is not Maya's: it is not one of her rows there.
    const founders = db.workspaces[1].teams.find((t) => t.name === "Founders")!;
    expect(lumen.ticket_counts?.teams?.[founders.id]).toBeUndefined();
    expect(lumen.unread_notifications).toBe(2);
  });

  it("issues a ws token that covers the other workspaces only when asked", async () => {
    const one = tokenWorkspaces((await call("POST", "/v1/realtime/token")).body.token);
    expect([...one.keys()]).toEqual([db.workspaces[0].workspace.id]);
    const all = tokenWorkspaces((await call("POST", "/v1/realtime/token?all_workspaces=true")).body.token);
    expect(all.has(db.workspaces[1].workspace.id)).toBe(true);
    expect(all.has(db.workspaces[2].workspace.id)).toBe(false);
  });
});
