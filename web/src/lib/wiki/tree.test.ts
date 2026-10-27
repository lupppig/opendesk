import { describe, expect, it } from "vitest";
import type { WikiNode } from "../api/types";
import { applyMove, buildTree, canPlace, childrenOf, descendantsOf, dropFor, keyMove, moveTargets, moveToEnd, ownership, removeSubtree, upsertNode, visibleRows } from "./tree";

const node = (id: string, parent: string | null, position: number, by: string | null = "me"): WikiNode => ({
  id,
  parent_id: parent,
  title: id.toUpperCase(),
  icon: "",
  position,
  version: 1,
  updated_at: "2026-10-01T00:00:00Z",
  created_by_id: by,
});

// a ─ b ─ c
//   └ d
// e
const nodes = [node("a", null, 0), node("b", "a", 0), node("c", "b", 0), node("d", "a", 1), node("e", null, 1)];
const titles = (list: WikiNode[]) => list.map((n) => n.id).join("");

describe("wiki tree", () => {
  it("orders siblings and finds descendants", () => {
    const t = buildTree(nodes);
    expect(titles(childrenOf(t, null))).toBe("ae");
    expect(titles(childrenOf(t, "a"))).toBe("bd");
    expect(titles(descendantsOf(t, "a"))).toBe("bcd");
  });

  it("never places a page under itself, a subpage, or past ten levels", () => {
    const t = buildTree(nodes);
    expect(canPlace(t, "a", "a")).toBe(false);
    expect(canPlace(t, "a", "c")).toBe(false);
    expect(canPlace(t, "e", "c")).toBe(true);
    const chain = Array.from({ length: 10 }, (_, i) => node(`l${i}`, i ? `l${i - 1}` : null, 0));
    const deep = buildTree([...chain, node("x", null, 1), node("y", "x", 0)]);
    expect(canPlace(deep, "y", "l8")).toBe(true);
    expect(canPlace(deep, "x", "l8")).toBe(false);
  });

  it("turns a drop on a row into the API's parent and position", () => {
    const t = buildTree(nodes);
    expect(dropFor(t, "e", "b", "before")).toEqual({ parent: "a", position: 0 });
    expect(dropFor(t, "e", "b", "after")).toEqual({ parent: "a", position: 1 });
    expect(dropFor(t, "e", "a", "inside")).toEqual({ parent: "a", position: 2 });
    expect(dropFor(t, "d", "b", "after")).toEqual({ parent: "a", position: 1 });
    expect(dropFor(t, "a", "c", "inside")).toBeNull();
    expect(dropFor(t, "a", "a", "before")).toBeNull();
  });

  it("moves like the API: renumbers the new and the old siblings", () => {
    const moved = applyMove(nodes, "b", { parent: null, position: 1 });
    const t = buildTree(moved);
    expect(titles(childrenOf(t, null))).toBe("abe");
    expect(childrenOf(t, null).map((n) => n.position)).toEqual([0, 1, 2]);
    expect(childrenOf(t, "a").map((n) => [n.id, n.position])).toEqual([["d", 0]]);
    expect(titles(childrenOf(t, "b"))).toBe("c");
    // Reordering within one parent.
    const re = buildTree(applyMove(nodes, "e", { parent: null, position: 0 }));
    expect(titles(childrenOf(re, null))).toBe("ea");
  });

  it("removes a page with its subpages and closes the gap", () => {
    const left = removeSubtree(nodes, ["a", "b", "c", "d"]);
    expect(left).toEqual([{ ...node("e", null, 0) }]);
  });

  it("inserts a new page at its position", () => {
    const t = buildTree(upsertNode(nodes, node("n", "a", 1)));
    expect(titles(childrenOf(t, "a"))).toBe("bnd");
    const again = buildTree(upsertNode(nodes, { ...node("a", null, 0), title: "Renamed" }));
    expect(again.byId.get("a")?.title).toBe("Renamed");
  });

  it("lists the rows a person sees and moves them with the keyboard", () => {
    const t = buildTree(nodes);
    expect(visibleRows(t, new Set(["a"])).map((r) => `${r.node.id}${r.depth}`)).toEqual(["a0", "b1", "d1", "e0"]);
    expect(keyMove(t, "d", "up")).toEqual({ parent: "a", position: 0 });
    expect(keyMove(t, "b", "up")).toBeNull();
    expect(keyMove(t, "d", "in")).toEqual({ parent: "b", position: 1 });
    expect(keyMove(t, "b", "out")).toEqual({ parent: null, position: 1 });
    expect(keyMove(t, "a", "out")).toBeNull();
  });

  it("offers every page a move can go under, never the page, its subpages or past ten levels", () => {
    const t = buildTree(nodes);
    expect(moveTargets(t, "b").map((r) => `${r.node.id}${r.depth}`)).toEqual(["a0", "d1", "e0"]);
    expect(moveToEnd(t, "b", "e")).toEqual({ parent: "e", position: 0 });
    expect(moveToEnd(t, "b", "a")).toEqual({ parent: "a", position: 1 });
    expect(moveToEnd(t, "e", null)).toEqual({ parent: null, position: 1 });
    // A chain ten deep: nothing more fits under its last page, and a two-level page fits only up top.
    const chain = Array.from({ length: 10 }, (_, i) => node(`p${i}`, i ? `p${i - 1}` : null, 0));
    const deep = buildTree([...chain, node("x", null, 1), node("y", "x", 0)]);
    expect(moveTargets(deep, "x").map((r) => r.node.id)).toEqual(["p0", "p1", "p2", "p3", "p4", "p5", "p6", "p7"]);
  });

  it("knows who may delete: the creator of the page and of every page under it", () => {
    const t = buildTree([node("a", null, 0), node("b", "a", 0, "other"), node("c", null, 1), node("d", null, 2, null)]);
    expect(ownership(t, "a", "me")).toBe("subpages");
    expect(ownership(t, "b", "me")).toBe("page");
    expect(ownership(t, "c", "me")).toBe("yes");
    expect(ownership(t, "d", "me")).toBe("page");
    expect(ownership(t, "unknown", "me")).toBe("yes");
  });
});
