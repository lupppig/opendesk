import { describe, expect, it } from "vitest";
import type { Space, SpaceItem } from "../api/types";
import { defaultSidebarPrefs } from "../collab/sidebarPrefs";
import {
  addItems,
  addSection,
  blankSpace,
  copySpace,
  defaultSections,
  findItem,
  forWire,
  hasItem,
  isCollabItem,
  itemKey,
  MAIN_SECTION_ID,
  moveItemTo,
  moveSpace,
  nextColor,
  normalizeSpace,
  otherWorkspaces,
  pickedSpace,
  removeItem,
  reorderItems,
  reorderSections,
  showSpace,
  sidebarCopy,
  spaceProblems,
  spacesProblems,
  uniqueName,
  updateItem,
} from "./model";

const A = "aaaaaaaa-0000-4000-8000-000000000001";
const B = "bbbbbbbb-0000-4000-8000-000000000002";
const C = "cccccccc-0000-4000-8000-000000000003";

const item = (workspace_id: string, kind: SpaceItem["kind"], id: string, extra: Partial<SpaceItem> = {}): SpaceItem => ({ workspace_id, kind, id, ...extra });

function space(sections: { id: string; title?: string; items: SpaceItem[] }[], p: Partial<Space> = {}): Space {
  return { id: "11111111-1111-4111-8111-111111111111", name: "Morning", icon: "", color: "blue", marks: true, sections: sections.map((s) => ({ title: "", collapsed: false, ...s })), ...p };
}

const ctx = (opts: { members?: string[]; collab?: Record<string, boolean | undefined> } = {}) => ({
  members: new Set(opts.members ?? [A, B]),
  collab: (w: string) => (opts.collab ? opts.collab[w] : true),
});

describe("starting a Space", () => {
  it("copies a workspace's own sidebar as whole sections in its order", () => {
    const prefs = { ...defaultSidebarPrefs(), order: ["inbox", "mail", "favorites", "teams", "views", "workspace"], hidden: ["section:views"] };
    const s = sidebarCopy({ id: A, name: "Margin" }, defaultSections(prefs, true), "Margin", "teal");
    expect(s).toMatchObject({ name: "Margin", icon: "", color: "teal", marks: true });
    expect(s.sections).toHaveLength(1);
    expect(s.sections[0].id).toBe(MAIN_SECTION_ID);
    expect(s.sections[0].items).toEqual(["inbox", "mail", "favorites", "teams", "workspace"].map((id) => item(A, "section", id)));
    expect(spaceProblems(s, new Set([A]))).toEqual([]);
  });

  it("lists the sections a workspace's sidebar shows: without hidden ones, and without Collab's without Collab", () => {
    expect(defaultSections(null, false)).toEqual(["inbox", "teams", "views", "workspace"]);
    expect(defaultSections(null, true)).toEqual(["favorites", "inbox", "mail", "teams", "views", "workspace"]);
    expect(defaultSections({ ...defaultSidebarPrefs(), hidden: ["section:favorites"] }, true)).toEqual(["inbox", "mail", "teams", "views", "workspace"]);
  });

  it("gives a new Space a colour not in use yet", () => {
    expect(nextColor([])).toBe("blue");
    expect(nextColor([{ color: "blue" }, { color: "violet" }])).toBe("teal");
  });
});

describe("reading Spaces", () => {
  it("fills left-out fields with their defaults and drops broken items", () => {
    const s = normalizeSpace({ id: "x", name: "Mail", sections: [{ id: "s", items: [{ workspace_id: A, kind: "box", id: "inbox", name: "" }, { workspace_id: A, kind: "nope", id: "x" }] }] });
    expect(s).toEqual({ id: "x", name: "Mail", icon: "", color: "grey", marks: true, sections: [{ id: "s", title: "", collapsed: false, items: [{ workspace_id: A, kind: "box", id: "inbox" }] }] });
    expect(normalizeSpace({ id: "x" })).toBeNull();
  });

  it("sends names cleaned and empty optional fields left out", () => {
    const s = space([{ id: "s", title: "  Team  stuff ", items: [item(A, "team", "t", { name: "   " }), item(A, "view", "v", { name: " Hot  ones ", badge: "urgent" })] }], { name: " Morning  shift " });
    const w = forWire(s);
    expect(w.name).toBe("Morning shift");
    expect(w.sections[0].title).toBe("Team stuff");
    expect(w.sections[0].items[0]).toEqual({ workspace_id: A, kind: "team", id: "t" });
    expect(w.sections[0].items[1]).toEqual({ workspace_id: A, kind: "view", id: "v", name: "Hot ones", badge: "urgent" });
  });
});

describe("checking Spaces", () => {
  it("accepts a Space within the limits", () => {
    expect(spaceProblems(space([{ id: "main", items: [item(A, "section", "inbox")] }]), new Set([A]))).toEqual([]);
  });

  it("names every limit the API holds to", () => {
    const codes = (s: Space, m?: Set<string>) => spaceProblems(s, m).map((p) => p.code);
    expect(codes({ ...blankSpace(""), name: "  " })).toContain("name_required");
    expect(codes(blankSpace("x".repeat(41)))).toContain("name_too_long");
    expect(codes(blankSpace("x".repeat(40)))).toEqual([]);
    expect(codes(space(Array.from({ length: 21 }, (_, i) => ({ id: `s${i}`, items: [] }))))).toContain("too_many_sections");
    expect(codes(space([{ id: "s", title: "t".repeat(41), items: [] }]))).toContain("title_too_long");
    expect(codes(space([{ id: "s", items: Array.from({ length: 61 }, (_, i) => item(A, "view", `v${i}`)) }]))).toContain("section_too_many_items");
    expect(codes(space(Array.from({ length: 6 }, (_, j) => ({ id: `s${j}`, items: Array.from({ length: 51 }, (_, i) => item(A, "view", `v${j}-${i}`)) }))))).toContain("too_many_items");
    expect(codes(space([{ id: "s", items: [item(A, "view", "v", { name: "n".repeat(65) })] }]))).toContain("row_name_too_long");
    expect(codes(space([{ id: "s", items: [item(C, "view", "v")] }]), new Set([A]))).toContain("not_member");
    expect(codes(space([{ id: "bad id!", items: [] }]))).toContain("bad_section_id");
    expect(codes(space([{ id: "s", items: [] }, { id: "s", items: [] }]))).toContain("bad_section_id");
    expect(codes(space([{ id: "s", items: [item(A, "view", "")] }]))).toContain("bad_item");
  });

  it("checks the list: at most 20, ids once, at most 64 KB", () => {
    const one = blankSpace("A");
    expect(spacesProblems([one, { ...one }]).map((p) => p.code)).toContain("duplicate_id");
    expect(spacesProblems(Array.from({ length: 21 }, (_, i) => blankSpace(`S${i}`))).map((p) => p.code)).toContain("too_many_spaces");
    const big = Array.from({ length: 8 }, (_, j) => space([{ id: "s", items: Array.from({ length: 60 }, (_, i) => item(A, "view", `${"v".repeat(90)}${j}${i}`, { name: "n".repeat(60) })) }], { id: `id-${j}` }));
    expect(spacesProblems(big).map((p) => p.code)).toContain("too_large");
  });
});

describe("changing a Space", () => {
  const base = space([
    { id: "s1", title: "Tickets", items: [item(A, "team", "t1"), item(A, "view", "v1")] },
    { id: "s2", title: "Mail", items: [item(B, "box", "inbox")] },
  ]);

  it("adds rows once, to the chosen section or the last, and a section when there is none", () => {
    const added = addItems(base, "s1", [item(A, "view", "v2"), item(A, "team", "t1")]);
    expect(added.sections[0].items.map((i) => i.id)).toEqual(["t1", "v1", "v2"]);
    expect(addItems(base, null, [item(A, "page", "contacts")]).sections[1].items.map((i) => i.id)).toEqual(["inbox", "contacts"]);
    const empty = addItems({ ...base, sections: [] }, null, [item(A, "page", "reports")]);
    expect(empty.sections).toHaveLength(1);
    expect(empty.sections[0].items[0].id).toBe("reports");
  });

  it("removes, renames, numbers and moves rows", () => {
    const k = itemKey(item(A, "view", "v1"));
    expect(hasItem(removeItem(base, k), item(A, "view", "v1"))).toBe(false);
    const named = updateItem(base, k, { name: "  Hot ", badge: "urgent" });
    expect(findItem(named, k)?.section.items[1]).toEqual({ workspace_id: A, kind: "view", id: "v1", name: "Hot", badge: "urgent" });
    const back = updateItem(named, k, { name: null, badge: null });
    expect(findItem(back, k)?.section.items[1]).toEqual({ workspace_id: A, kind: "view", id: "v1" });
    const moved = moveItemTo(base, k, "s2");
    expect(moved.sections[0].items.map((i) => i.id)).toEqual(["t1"]);
    expect(moved.sections[1].items.map((i) => i.id)).toEqual(["inbox", "v1"]);
  });

  it("reorders rows and sections, keeping anything the order leaves out", () => {
    const keys = base.sections[0].items.map(itemKey).reverse();
    expect(reorderItems(base, "s1", keys).sections[0].items.map((i) => i.id)).toEqual(["v1", "t1"]);
    expect(reorderItems(base, "s1", [keys[0]]).sections[0].items.map((i) => i.id)).toEqual(["v1", "t1"]);
    expect(reorderSections(base, ["s2", "s1"]).sections.map((s) => s.id)).toEqual(["s2", "s1"]);
    expect(reorderSections(base, ["s2"]).sections.map((s) => s.id)).toEqual(["s2", "s1"]);
  });

  it("adds a section with fresh rows only and an id of its own", () => {
    const { space: s, id } = addSection(base, "More", [item(A, "team", "t1"), item(A, "page", "kb")]);
    expect(s.sections.map((x) => x.id)).toEqual(["s1", "s2", id]);
    expect(s.sections[2].items.map((i) => i.id)).toEqual(["kb"]);
    expect(/^[A-Za-z0-9_-]{1,40}$/.test(id)).toBe(true);
  });

  it("copies with a new id, moves Spaces and finds a free name", () => {
    const c = copySpace(base, "Morning copy");
    expect(c.id).not.toBe(base.id);
    expect(c.sections).toEqual(base.sections);
    const other = blankSpace("Evening");
    expect(moveSpace([base, other], other.id, -1).map((s) => s.name)).toEqual(["Evening", "Morning"]);
    expect(moveSpace([base, other], base.id, -1)).toEqual([base, other]);
    expect(uniqueName([base, blankSpace("Morning 2")], "Morning")).toBe("Morning 3");
    expect(uniqueName([base], "Support")).toBe("Support");
  });
});

describe("showing a Space", () => {
  const mixed = space([
    { id: "s1", title: "Tickets", items: [item(A, "section", "inbox"), item(B, "team", "t1")] },
    { id: "s2", title: "Mail", items: [item(B, "box", "inbox"), item(B, "account", "acc")] },
    { id: "s3", title: "Gone", items: [item(C, "view", "v1")] },
  ]);

  it("shows every row of every workspace, with marks across workspaces, and drops empty sections", () => {
    const shown = showSpace(mixed, ctx());
    expect(shown.sections.map((s) => s.section.id)).toEqual(["s1", "s2"]);
    expect(shown.sections[0].items.map((i) => i.id)).toEqual(["inbox", "t1"]);
    expect(shown.workspaces).toEqual([A, B]);
    expect(shown.marks).toBe(true);
    expect(otherWorkspaces(shown, A)).toEqual([B]);
    expect(showSpace({ ...mixed, marks: false }, ctx()).marks).toBe(false);
    // Badges follow the Space's switch; which rows carry one (another workspace's) is the sidebar's to say.
    expect(showSpace(space([{ id: "s", items: [item(A, "view", "v")] }]), ctx()).marks).toBe(true);
  });

  it("shows everywhere rows whatever workspaces the person is in, and counts no workspace for them", () => {
    const s = space([{ id: "s", items: [item("", "everywhere", "mine"), item(A, "view", "v")] }]);
    const shown = showSpace(s, ctx({ members: [] }));
    expect(shown.sections[0].items.map((i) => i.id)).toEqual(["mine"]);
    expect(shown.workspaces).toEqual([]);
    expect(spaceProblems(s, new Set([A]))).toEqual([]);
    expect(spaceProblems(space([{ id: "s", items: [item(A, "everywhere", "mine")] }])).map((p) => p.code)).toContain("bad_item");
    expect(spaceProblems(space([{ id: "s", items: [item("", "everywhere", "views")] }])).map((p) => p.code)).toContain("bad_item");
    expect(spaceProblems(space([{ id: "s", items: [item("", "view", "v")] }])).map((p) => p.code)).toContain("bad_item");
  });

  it("skips workspaces the user left, and Collab rows where there is no Collab (or not yet known)", () => {
    const shown = showSpace(mixed, ctx({ collab: { [A]: true, [B]: false } }));
    expect(shown.sections.map((s) => s.section.id)).toEqual(["s1"]);
    expect(showSpace(mixed, ctx({ collab: {} })).sections.map((s) => s.section.id)).toEqual(["s1"]);
    expect(isCollabItem(item(A, "section", "mail"))).toBe(true);
    expect(isCollabItem(item(A, "section", "favorites"))).toBe(true);
    expect(isCollabItem(item(A, "section", "inbox"))).toBe(false);
  });

  it("finds the picked Space, and nothing when the remembered id is gone", () => {
    expect(pickedSpace([mixed], mixed.id)?.name).toBe("Morning");
    expect(pickedSpace([mixed], "gone")).toBeNull();
    expect(pickedSpace([mixed], null)).toBeNull();
    expect(pickedSpace(undefined, mixed.id)).toBeNull();
  });
});
