import { describe, expect, it } from "vitest";
import type { Space, SpacesResponse } from "@/lib/api/types";
import { installMock } from "../index";
import { dispatch } from "../router";
import { db } from "../db";
import { memberships } from "./auth";

async function call(method: string, path: string, body?: unknown, wid?: string) {
  const ws = wid ?? db.workspaces[0]?.workspace.id;
  const r = await dispatch(`http://mock${path}`, { method, body: body ? JSON.stringify(body) : undefined, headers: ws ? { "OD-Workspace": ws } : {} });
  return { status: r.status, body: r.status === 204 ? null : await r.json() };
}

const uuid = (n: number) => `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`;

describe("mock Spaces", () => {
  installMock();
  const mine = () => memberships(db.users[0].id);

  it("seeds several workspaces for the signed-in person, one without Collab", async () => {
    const names = mine().map((m) => m.workspace.name);
    expect(names).toEqual(["Margin", "Lumen Labs", "Pressroom"]);
    const pressroom = mine()[2].workspace.id;
    expect((await call("GET", "/v1/collab", undefined, pressroom)).body.enabled).toBe(false);
    const lumen = mine()[1].workspace.id;
    expect(((await call("GET", "/v1/teams", undefined, lumen)).body.data as unknown[]).length).toBeGreaterThan(0);
  });

  it("has no Spaces until some are saved", async () => {
    expect((await call("GET", "/v1/me/spaces")).body).toEqual({ spaces: [], saved: false });
  });

  it("keeps a workspace's starter Space for admins to change, without handing it out as a Space", async () => {
    const lumen = mine()[1].workspace.id;
    const margin = mine()[0].workspace.id;
    const starter = { id: uuid(9), name: "Support desk", icon: "headphones", color: "teal", marks: true, sections: [{ id: "main", title: "", collapsed: false, items: [{ workspace_id: lumen, kind: "section", id: "inbox" }] }] };
    // Maya is an agent in Lumen Labs.
    expect((await call("PUT", "/v1/sidebar/starter", starter, lumen)).status).toBe(403);
    const put = await call("PUT", "/v1/sidebar/starter", starter, margin);
    expect(put.status).toBe(200);
    // Every item becomes the workspace's own.
    expect((put.body.space as Space).sections[0].items[0].workspace_id).toBe(margin);
    expect((await call("GET", "/v1/sidebar/starter", undefined, margin)).body.space.name).toBe("Support desk");
    expect((await call("GET", "/v1/sidebar/starter", undefined, lumen)).body.space).toBeNull();
    // Offered to start from, never swapped in.
    expect((await call("GET", "/v1/me/spaces")).body).toEqual({ spaces: [], saved: false });
    const audit = db.workspaces[0].audit[0];
    expect(audit.action).toBe("sidebar.starter_updated");
    expect(audit.entity_type).toBe("workspace");
    expect((await call("DELETE", "/v1/sidebar/starter", undefined, margin)).status).toBe(204);
    expect(db.workspaces[0].audit[0].action).toBe("sidebar.starter_removed");
    expect((await call("DELETE", "/v1/sidebar/starter", undefined, margin)).status).toBe(204);
    expect((await call("GET", "/v1/sidebar/starter", undefined, margin)).body.space).toBeNull();
  });

  it("saves the whole list with defaults for left-out fields, and keeps an empty list saved", async () => {
    const [margin, lumen] = mine().map((m) => m.workspace.id);
    const r = await call("PUT", "/v1/me/spaces", {
      spaces: [{ id: uuid(1), name: "  Morning ", sections: [{ id: "s1", items: [{ workspace_id: margin, kind: "team", id: "x", name: "Mine" }, { workspace_id: lumen, kind: "view", id: "y", badge: "urgent" }, { workspace_id: "", kind: "everywhere", id: "mail" }] }] }],
    });
    expect(r.status).toBe(200);
    const s = (r.body as SpacesResponse).spaces[0];
    expect((r.body as SpacesResponse).saved).toBe(true);
    expect(s).toEqual(expect.objectContaining({ name: "Morning", icon: "", color: "grey", marks: true }));
    expect("narrow" in s).toBe(false);
    expect(s.sections[0]).toMatchObject({ title: "", collapsed: false });
    expect(s.sections[0].items[1]).toEqual({ workspace_id: lumen, kind: "view", id: "y", badge: "urgent" });
    expect(s.sections[0].items[2]).toEqual({ workspace_id: "", kind: "everywhere", id: "mail" });
    const empty = await call("PUT", "/v1/me/spaces", { spaces: [] });
    expect(empty.body).toEqual({ spaces: [], saved: true });
    expect((await call("GET", "/v1/me/spaces")).body).toEqual({ spaces: [], saved: true });
  });

  it("refuses what the API refuses", async () => {
    const margin = mine()[0].workspace.id;
    const ok = { id: uuid(2), name: "Ok", sections: [] as unknown[] };
    const code = async (body: unknown) => (await call("PUT", "/v1/me/spaces", body)).body.code;
    expect(await code({ spaces: [{ ...ok, name: "" }] })).toBe("invalid_name");
    expect(await code({ spaces: [{ ...ok, name: "x".repeat(41) }] })).toBe("invalid_name");
    expect(await code({ spaces: [{ ...ok, extra: true }] })).toBe("validation_failed");
    expect(await code({ spaces: [{ ...ok, narrow: true }] })).toBe("validation_failed");
    expect(await code({ spaces: [{ ...ok, sections: [{ id: "s", items: [{ workspace_id: margin, kind: "everywhere", id: "mine" }] }] }] })).toBe("validation_failed");
    expect(await code({ spaces: [{ ...ok, sections: [{ id: "s", items: [{ workspace_id: "", kind: "everywhere", id: "views" }] }] }] })).toBe("validation_failed");
    expect(await code({ spaces: [{ ...ok, icon: "dragon" }] })).toBe("validation_failed");
    expect(await code({ spaces: [{ ...ok, color: "orange" }] })).toBe("validation_failed");
    expect(await code({ spaces: [ok, ok] })).toBe("validation_failed");
    expect(await code({ spaces: Array.from({ length: 21 }, (_, i) => ({ ...ok, id: uuid(100 + i) })) })).toBe("validation_failed");
    expect(await code({ spaces: [{ ...ok, sections: [{ id: "s", items: [{ workspace_id: db.workspaces[2].workspace.id, kind: "view", id: "v" }] }] }] })).toBe("validation_failed");
    expect(await code({ spaces: [{ ...ok, sections: [{ id: "s", items: Array.from({ length: 61 }, (_, i) => ({ workspace_id: margin, kind: "view", id: `v${i}` })) }] }] })).toBe("validation_failed");
    expect(await code({ spaces: [{ ...ok, sections: [{ id: "s", title: "t".repeat(41) }] }] })).toBe("invalid_name");
  });
});
