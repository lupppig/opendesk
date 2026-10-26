import { describe, expect, it } from "vitest";
import { tokenWorkspaces } from "./realtime/manager";
import { mergeByActivity, moveWorkspace, orderWorkspaces, shownWorkspaces, toggleIn, workspaceInitials } from "./workspaces";
import { withWorkspaceParam } from "./workspaceParam";

const ws = (id: string) => ({ workspace: { id } });
const ids = (list: { workspace: { id: string } }[]) => list.map((m) => m.workspace.id);

describe("arranging workspaces", () => {
  it("orders workspaces: the placed ones first, the rest in joining order, unknown ids ignored", () => {
    const list = [ws("a"), ws("b"), ws("c"), ws("d")];
    expect(ids(orderWorkspaces(list, ["c", "gone", "a"]))).toEqual(["c", "a", "b", "d"]);
    expect(ids(orderWorkspaces(list, []))).toEqual(["a", "b", "c", "d"]);
  });

  it("leaves hidden workspaces out, but never the one open now", () => {
    const list = [ws("a"), ws("b"), ws("c")];
    expect(ids(shownWorkspaces(list, { order: [], hidden: ["b", "c"] }, "c"))).toEqual(["a", "c"]);
  });

  it("moves a workspace by keyboard steps, within the ends", () => {
    expect(moveWorkspace(["a", "b", "c"], "c", -1)).toEqual(["a", "c", "b"]);
    expect(moveWorkspace(["a", "b", "c"], "a", -1)).toEqual(["a", "b", "c"]);
    expect(moveWorkspace(["a", "b", "c"], "a", 5)).toEqual(["b", "c", "a"]);
    expect(moveWorkspace(["a"], "x", 1)).toEqual(["a"]);
  });

  it("toggles ids in a list", () => {
    expect(toggleIn(["a"], "b")).toEqual(["a", "b"]);
    expect(toggleIn(["a", "b"], "a")).toEqual(["b"]);
    expect(toggleIn(["a"], "a", true)).toEqual(["a"]);
  });

  it("makes initials", () => {
    expect(workspaceInitials("Lumen Labs")).toBe("LL");
    expect(workspaceInitials("margin")).toBe("M");
    expect(workspaceInitials("  ")).toBe("?");
  });
});

describe("mergeByActivity", () => {
  it("merges newest first and tags each row with its workspace", () => {
    const merged = mergeByActivity(
      [
        { workspaceId: "A", rows: [{ id: "a1", at: "2026-10-20T10:00:00Z" }, { id: "a2", at: "2026-10-18T10:00:00Z" }] },
        { workspaceId: "B", rows: [{ id: "b1", at: "2026-10-19T10:00:00Z" }, { id: "b2", at: "2026-10-21T10:00:00Z" }] },
      ],
      (r) => r.at,
    );
    expect(merged.map((x) => `${x.workspaceId}:${x.row.id}`)).toEqual(["B:b2", "A:a1", "B:b1", "A:a2"]);
  });

  it("keeps the same id from two workspaces apart, breaks ties by workspace order, and puts undated rows last", () => {
    const merged = mergeByActivity(
      [
        { workspaceId: "A", rows: [{ id: "x", at: "2026-10-20T10:00:00Z" }, { id: "y", at: null }] },
        { workspaceId: "B", rows: [{ id: "x", at: "2026-10-20T10:00:00Z" }] },
      ],
      (r) => r.at,
      2,
    );
    expect(merged.map((x) => `${x.workspaceId}:${x.row.id}`)).toEqual(["A:x", "B:x"]);
  });
});

describe("tokenWorkspaces", () => {
  const b64 = (o: unknown) => btoa(JSON.stringify(o)).replace(/=+$/, "").replace(/\+/g, "-").replace(/\//g, "_");
  const token = (claims: unknown) => `${b64({ alg: "HS256" })}.${b64(claims)}.sig`;

  it("reads the token's own workspace and its others, with their private teams", () => {
    const got = tokenWorkspaces(token({ wid: "A", teams: ["t1"], workspaces: { B: { role: "viewer", teams: ["t2"] }, C: { role: "agent" } } }));
    expect([...got.entries()]).toEqual([
      ["B", ["t2"]],
      ["C", []],
      ["A", ["t1"]],
    ]);
  });

  it("reads a one-workspace token (iOS, older servers) and nothing from garbage", () => {
    expect([...tokenWorkspaces(token({ wid: "A" })).entries()]).toEqual([["A", []]]);
    expect(tokenWorkspaces("not-a-token").size).toBe(0);
  });
});

describe("withWorkspaceParam", () => {
  it("names the workspace on an app href and keeps the rest", () => {
    expect(withWorkspaceParam("/inbox/mine/t1", "w-1")).toBe("/inbox/mine/t1?ws=w-1");
    expect(withWorkspaceParam("/settings/import?id=9#x", "w-1")).toBe("/settings/import?id=9&ws=w-1#x");
    expect(withWorkspaceParam("/inbox/mine", undefined)).toBe("/inbox/mine");
    expect(withWorkspaceParam("/inbox/mine", "bad id!")).toBe("/inbox/mine");
  });
});
