// Spaces (contracts/spaces.md): named sidebars of sections and rows, from any of the
// workspaces a person belongs to. Pure, so the sidebar, its menus, the editor, the
// optimistic save and the mock read and change Spaces the same way. Items naming
// something gone (a deleted team, a workspace left) are kept and skipped where shown.

import type { SidebarBadge, SidebarPrefs, SidebarSectionId, Space, SpaceColor, SpaceItem, SpaceItemKind, SpaceSection } from "../api/types";
import { SECTION_IDS, sectionsOffered, visibleSections } from "../collab/sidebarPrefs";

/** The icons a Space can take (lucide names; components/spaces/icons.tsx maps them). */
export const SPACE_ICONS = [
  "sun",
  "moon",
  "star",
  "inbox",
  "mail",
  "headphones",
  "users",
  "briefcase",
  "rocket",
  "flame",
  "zap",
  "heart",
  "coffee",
  "flag",
  "bookmark",
  "layers",
  "target",
  "globe",
  "bell",
  "leaf",
  "home",
  "chart",
  "shield",
  "sparkles",
] as const;
export type SpaceIcon = (typeof SPACE_ICONS)[number];

export const SPACE_COLORS: readonly SpaceColor[] = ["grey", "blue", "violet", "teal", "green", "amber", "red", "pink"];
/** The colours new Spaces take, in turn. */
export const NEW_COLORS: readonly SpaceColor[] = ["blue", "violet", "teal", "green", "amber", "red", "pink", "grey"];

export const SPACE_KINDS: readonly SpaceItemKind[] = ["section", "ticket_folder", "team", "view", "box", "account", "folder", "page", "everywhere"];
/** The pages a Space row can open. */
export const SPACE_PAGES = ["contacts", "knowledge", "wiki", "reports"] as const;
export type SpacePage = (typeof SPACE_PAGES)[number];
/** The Everywhere lists a Space row can open: tickets assigned to me, and all mail, across workspaces. */
export const EVERYWHERE_LISTS = ["mine", "mail"] as const;
export type EverywhereList = (typeof EVERYWHERE_LISTS)[number];
export const isEverywhereList = (v: unknown): v is EverywhereList => typeof v === "string" && (EVERYWHERE_LISTS as readonly string[]).includes(v);
/** An everywhere row belongs to no workspace: its workspace_id is "". */
export const isEverywhere = (i: Pick<SpaceItem, "kind">) => i.kind === "everywhere";

/** The Collab boxes a Space row can open (across a workspace's accounts). */
export const SPACE_BOXES = ["inbox", "shared", "assigned"] as const;
export type SpaceBox = (typeof SPACE_BOXES)[number];

export const LIMITS = {
  spaces: 20,
  sections: 20,
  sectionItems: 60,
  items: 300,
  name: 40,
  title: 40,
  rowName: 64,
  itemId: 100,
  bytes: 64 * 1024,
} as const;

/** The one section of a Space started from a workspace's sidebar. */
export const MAIN_SECTION_ID = "main";

const SECTION_ID = /^[A-Za-z0-9_-]{1,40}$/;
const BADGES: readonly SidebarBadge[] = ["unread", "total", "count", "awaiting", "urgent", "overdue", "open", "none"];

export const isSpaceIcon = (v: unknown): v is SpaceIcon => typeof v === "string" && (SPACE_ICONS as readonly string[]).includes(v);
export const isSpaceColor = (v: unknown): v is SpaceColor => typeof v === "string" && (SPACE_COLORS as readonly string[]).includes(v);
export const isSpacePage = (v: unknown): v is SpacePage => typeof v === "string" && (SPACE_PAGES as readonly string[]).includes(v);
export const isSpaceBox = (v: unknown): v is SpaceBox => typeof v === "string" && (SPACE_BOXES as readonly string[]).includes(v);
const isKind = (v: unknown): v is SpaceItemKind => typeof v === "string" && (SPACE_KINDS as readonly string[]).includes(v);
const isBadge = (v: unknown): v is SidebarBadge => typeof v === "string" && (BADGES as readonly string[]).includes(v);
export const isSectionId = (v: unknown): v is SidebarSectionId => typeof v === "string" && (SECTION_IDS as readonly string[]).includes(v);

/** Rows that need Collab: its mail and its member prefs (Favorites). Skipped for a workspace without it. */
export function isCollabItem(i: Pick<SpaceItem, "kind" | "id">): boolean {
  return i.kind === "box" || i.kind === "account" || i.kind === "folder" || (i.kind === "section" && (i.id === "mail" || i.id === "favorites"));
}

// ─── Ids and names ─────────────────────────────────────────────────────────

export function newSpaceId(): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) return crypto.randomUUID();
  const hex = (n: number) => Array.from({ length: n }, () => Math.floor(Math.random() * 16).toString(16)).join("");
  return `${hex(8)}-${hex(4)}-4${hex(3)}-${"89ab"[Math.floor(Math.random() * 4)]}${hex(3)}-${hex(12)}`;
}

/** A section id unique within the Space. */
export function newSectionId(space?: Pick<Space, "sections">): string {
  const taken = new Set(space?.sections.map((s) => s.id) ?? []);
  for (;;) {
    const id = `s${Math.random().toString(36).slice(2, 10)}`;
    if (!taken.has(id)) return id;
  }
}

/** A name as people type it: trimmed, inner runs of space made one. */
export function cleanName(s: string): string {
  return s.replace(/\s+/g, " ").trim();
}

/** The one key an item has in a Space: its workspace, kind and id. */
export function itemKey(i: Pick<SpaceItem, "workspace_id" | "kind" | "id">): string {
  return `${i.workspace_id}:${i.kind}:${i.id}`;
}

// ─── Reading answers ───────────────────────────────────────────────────────

function readItem(v: unknown): SpaceItem | null {
  if (!v || typeof v !== "object") return null;
  const x = v as Record<string, unknown>;
  if (typeof x.workspace_id !== "string" || typeof x.id !== "string" || !isKind(x.kind)) return null;
  const out: SpaceItem = { workspace_id: x.workspace_id, kind: x.kind, id: x.id };
  if (typeof x.name === "string" && x.name.trim()) out.name = x.name;
  if (isBadge(x.badge)) out.badge = x.badge;
  return out;
}

/** A Space with every field present: left-out fields read as their defaults (contracts/spaces.md "Shapes"). */
export function normalizeSpace(v: unknown): Space | null {
  if (!v || typeof v !== "object") return null;
  const x = v as Record<string, unknown>;
  if (typeof x.id !== "string" || typeof x.name !== "string") return null;
  const sections: SpaceSection[] = Array.isArray(x.sections)
    ? x.sections.flatMap((s): SpaceSection[] => {
        if (!s || typeof s !== "object") return [];
        const r = s as Record<string, unknown>;
        if (typeof r.id !== "string") return [];
        return [
          {
            id: r.id,
            title: typeof r.title === "string" ? r.title : "",
            collapsed: r.collapsed === true,
            items: Array.isArray(r.items) ? r.items.map(readItem).filter((i): i is SpaceItem => !!i) : [],
          },
        ];
      })
    : [];
  return {
    id: x.id,
    name: x.name,
    icon: typeof x.icon === "string" ? x.icon : "",
    color: isSpaceColor(x.color) ? x.color : "grey",
    marks: x.marks !== false,
    sections,
  };
}

export function normalizeSpaces(v: unknown): Space[] {
  return Array.isArray(v) ? v.map(normalizeSpace).filter((s): s is Space => !!s) : [];
}

// ─── Starting points ───────────────────────────────────────────────────────

/**
 * A workspace's built-in sections as its own sidebar shows them: its SidebarPrefs order without
 * the sections hidden there; without saved prefs the default order. Mail only with Collab.
 */
export function defaultSections(prefs: SidebarPrefs | null | undefined, collab: boolean): SidebarSectionId[] {
  return prefs ? visibleSections(prefs, collab) : [...sectionsOffered(collab)];
}

/** A colour no Space uses yet, else the next in turn. */
export function nextColor(list: Pick<Space, "color">[]): SpaceColor {
  const used = new Set(list.map((s) => s.color));
  return NEW_COLORS.find((c) => !used.has(c)) ?? NEW_COLORS[list.length % NEW_COLORS.length];
}

/** A new, empty Space with one untitled section ready for rows. */
export function blankSpace(name: string, color: SpaceColor = "blue"): Space {
  return { id: newSpaceId(), name, icon: "", color, marks: true, sections: [{ id: newSectionId(), title: "", collapsed: false, items: [] }] };
}

/** A new Space holding a workspace's own sidebar: its built-in sections, kept up to date, in its order. */
export function sidebarCopy(workspace: { id: string; name: string }, sections: SidebarSectionId[], name: string, color: SpaceColor): Space {
  return {
    id: newSpaceId(),
    name: name.slice(0, LIMITS.name),
    icon: "",
    color,
    marks: true,
    sections: [{ id: MAIN_SECTION_ID, title: "", collapsed: false, items: sections.map((id) => ({ workspace_id: workspace.id, kind: "section" as const, id })) }],
  };
}

/** A copy that is the person's own: a new id, the same content. */
export function copySpace(s: Space, name = s.name): Space {
  return { ...structuredClone(s), id: newSpaceId(), name: name.slice(0, LIMITS.name) };
}

// ─── Checking ──────────────────────────────────────────────────────────────

export type SpaceProblem =
  | { code: "name_required" }
  | { code: "name_too_long" }
  | { code: "too_many_sections" }
  | { code: "title_too_long"; section: string }
  | { code: "section_too_many_items"; section: string }
  | { code: "too_many_items" }
  | { code: "row_name_too_long"; section: string; item: string }
  | { code: "not_member"; section: string; item: string }
  | { code: "bad_section_id"; section: string }
  | { code: "bad_item"; section: string; item: string };

/** What the API would refuse in one Space (contracts/spaces.md limits). `members`: the workspaces the user is in. */
export function spaceProblems(s: Space, members?: ReadonlySet<string>): SpaceProblem[] {
  const out: SpaceProblem[] = [];
  const name = cleanName(s.name);
  if (!name) out.push({ code: "name_required" });
  else if ([...name].length > LIMITS.name) out.push({ code: "name_too_long" });
  if (s.sections.length > LIMITS.sections) out.push({ code: "too_many_sections" });
  const ids = new Set<string>();
  let total = 0;
  for (const sec of s.sections) {
    if (!SECTION_ID.test(sec.id) || ids.has(sec.id)) out.push({ code: "bad_section_id", section: sec.id });
    ids.add(sec.id);
    if ([...cleanName(sec.title)].length > LIMITS.title) out.push({ code: "title_too_long", section: sec.id });
    if (sec.items.length > LIMITS.sectionItems) out.push({ code: "section_too_many_items", section: sec.id });
    total += sec.items.length;
    for (const it of sec.items) {
      const key = itemKey(it);
      const wrongWorkspace = isEverywhere(it) ? it.workspace_id !== "" || !isEverywhereList(it.id) : !it.workspace_id;
      if (!it.id || it.id.length > LIMITS.itemId || !isKind(it.kind) || wrongWorkspace || (it.badge !== undefined && !isBadge(it.badge))) out.push({ code: "bad_item", section: sec.id, item: key });
      if (it.name !== undefined && [...cleanName(it.name)].length > LIMITS.rowName) out.push({ code: "row_name_too_long", section: sec.id, item: key });
      if (members && !isEverywhere(it) && !members.has(it.workspace_id)) out.push({ code: "not_member", section: sec.id, item: key });
    }
  }
  if (total > LIMITS.items) out.push({ code: "too_many_items" });
  return out;
}

export type SpacesProblem = { code: "too_many_spaces" } | { code: "duplicate_id"; space: string } | { code: "too_large" } | { code: "space"; space: string; problem: SpaceProblem };

/** What the API would refuse in the whole list. */
export function spacesProblems(list: Space[], members?: ReadonlySet<string>): SpacesProblem[] {
  const out: SpacesProblem[] = [];
  if (list.length > LIMITS.spaces) out.push({ code: "too_many_spaces" });
  const seen = new Set<string>();
  for (const s of list) {
    if (seen.has(s.id)) out.push({ code: "duplicate_id", space: s.id });
    seen.add(s.id);
    for (const p of spaceProblems(s, members)) out.push({ code: "space", space: s.id, problem: p });
  }
  if (spacesBytes(list) > LIMITS.bytes) out.push({ code: "too_large" });
  return out;
}

export function spacesBytes(list: Space[]): number {
  return new TextEncoder().encode(JSON.stringify({ spaces: list.map(forWire) })).length;
}

/** A Space as it is sent: names cleaned, empty optional fields left out. */
export function forWire(s: Space): Space {
  return {
    ...s,
    name: cleanName(s.name),
    sections: s.sections.map((sec) => ({
      ...sec,
      title: cleanName(sec.title),
      items: sec.items.map((it) => {
        const out: SpaceItem = { workspace_id: it.workspace_id, kind: it.kind, id: it.id };
        const name = it.name ? cleanName(it.name) : "";
        if (name) out.name = name;
        if (it.badge) out.badge = it.badge;
        return out;
      }),
    })),
  };
}

// ─── Changing one Space ────────────────────────────────────────────────────

export function hasItem(s: Space, i: Pick<SpaceItem, "workspace_id" | "kind" | "id">): boolean {
  const k = itemKey(i);
  return s.sections.some((sec) => sec.items.some((x) => itemKey(x) === k));
}

/** Where an item sits: its section and place. */
export function findItem(s: Space, key: string): { section: SpaceSection; index: number } | null {
  for (const section of s.sections) {
    const index = section.items.findIndex((x) => itemKey(x) === key);
    if (index >= 0) return { section, index };
  }
  return null;
}

const mapSection = (s: Space, id: string, f: (sec: SpaceSection) => SpaceSection): Space => ({ ...s, sections: s.sections.map((sec) => (sec.id === id ? f(sec) : sec)) });

/** Adds rows at the end of a section (a new untitled one when the Space has none); rows already in the Space stay where they are. */
export function addItems(s: Space, sectionId: string | null, items: SpaceItem[]): Space {
  let next = s;
  let target = sectionId && s.sections.some((x) => x.id === sectionId) ? sectionId : (s.sections[s.sections.length - 1]?.id ?? null);
  if (!target) {
    target = newSectionId(s);
    next = { ...s, sections: [{ id: target, title: "", collapsed: false, items: [] }] };
  }
  const fresh: SpaceItem[] = [];
  const keys = new Set<string>();
  for (const it of items) {
    const k = itemKey(it);
    if (keys.has(k) || hasItem(next, it)) continue;
    keys.add(k);
    fresh.push(it);
  }
  if (!fresh.length) return next;
  return mapSection(next, target, (sec) => ({ ...sec, items: [...sec.items, ...fresh] }));
}

export function removeItem(s: Space, key: string): Space {
  return { ...s, sections: s.sections.map((sec) => ({ ...sec, items: sec.items.filter((x) => itemKey(x) !== key) })) };
}

/** Sets a row's own name or number; null goes back to the row's default. */
export function updateItem(s: Space, key: string, patch: { name?: string | null; badge?: SidebarBadge | null }): Space {
  return {
    ...s,
    sections: s.sections.map((sec) => ({
      ...sec,
      items: sec.items.map((x) => {
        if (itemKey(x) !== key) return x;
        const out = { ...x };
        if (patch.name !== undefined) {
          const name = patch.name === null ? "" : cleanName(patch.name);
          if (name) out.name = name;
          else delete out.name;
        }
        if (patch.badge !== undefined) {
          if (patch.badge === null) delete out.badge;
          else out.badge = patch.badge;
        }
        return out;
      }),
    })),
  };
}

/** Moves a row to the end of another section. */
export function moveItemTo(s: Space, key: string, sectionId: string): Space {
  const at = findItem(s, key);
  if (!at || at.section.id === sectionId || !s.sections.some((x) => x.id === sectionId)) return s;
  const item = at.section.items[at.index];
  return mapSection(removeItem(s, key), sectionId, (sec) => ({ ...sec, items: [...sec.items, item] }));
}

/** Puts a section's rows in the order of `keys`; rows the list lacks keep their place at the end. */
export function reorderItems(s: Space, sectionId: string, keys: string[]): Space {
  return mapSection(s, sectionId, (sec) => {
    const byKey = new Map(sec.items.map((x) => [itemKey(x), x]));
    const out = keys.map((k) => byKey.get(k)).filter((x): x is SpaceItem => !!x);
    for (const x of sec.items) if (!keys.includes(itemKey(x))) out.push(x);
    return { ...sec, items: out };
  });
}

export function addSection(s: Space, title = "", items: SpaceItem[] = []): { space: Space; id: string } {
  const id = newSectionId(s);
  const keys = new Set(s.sections.flatMap((sec) => sec.items.map(itemKey)));
  const fresh = items.filter((it) => !keys.has(itemKey(it)));
  return { space: { ...s, sections: [...s.sections, { id, title, collapsed: false, items: fresh }] }, id };
}

export function updateSection(s: Space, id: string, patch: Partial<Pick<SpaceSection, "title" | "collapsed">>): Space {
  return mapSection(s, id, (sec) => ({ ...sec, ...patch }));
}

export function removeSection(s: Space, id: string): Space {
  return { ...s, sections: s.sections.filter((x) => x.id !== id) };
}

export function reorderSections(s: Space, ids: string[]): Space {
  const byId = new Map(s.sections.map((x) => [x.id, x]));
  const out = ids.map((id) => byId.get(id)).filter((x): x is SpaceSection => !!x);
  for (const x of s.sections) if (!ids.includes(x.id)) out.push(x);
  return { ...s, sections: out };
}

// ─── Changing the list ─────────────────────────────────────────────────────

export function replaceSpace(list: Space[], s: Space): Space[] {
  return list.some((x) => x.id === s.id) ? list.map((x) => (x.id === s.id ? s : x)) : [...list, s];
}

export function removeSpace(list: Space[], id: string): Space[] {
  return list.filter((x) => x.id !== id);
}

/** Moves a Space one place left (-1) or right (1). */
export function moveSpace(list: Space[], id: string, dir: -1 | 1): Space[] {
  const i = list.findIndex((x) => x.id === id);
  const j = i + dir;
  if (i < 0 || j < 0 || j >= list.length) return list;
  const next = list.slice();
  [next[i], next[j]] = [next[j], next[i]];
  return next;
}

/** A name not yet used in the list: "Support", then "Support 2". */
export function uniqueName(list: Space[], base: string): string {
  const names = new Set(list.map((x) => cleanName(x.name).toLowerCase()));
  const b = cleanName(base).slice(0, LIMITS.name - 3) || "Space";
  if (!names.has(b.toLowerCase())) return b;
  for (let n = 2; ; n++) if (!names.has(`${b} ${n}`.toLowerCase())) return `${b} ${n}`;
}

// ─── Showing a Space ───────────────────────────────────────────────────────

/** The picked Space, or null: none picked, or the remembered id names a Space that is gone (the workspace shows). */
export function pickedSpace(list: Space[] | undefined, id: string | null | undefined): Space | null {
  return (id && list?.find((x) => x.id === id)) || null;
}

export type ShowContext = {
  /** The workspaces the user is a member of. */
  members: ReadonlySet<string>;
  /** Whether a workspace has Collab; undefined while unknown (its rows wait). */
  collab: (workspaceId: string) => boolean | undefined;
};

export type Shown = {
  sections: { section: SpaceSection; items: SpaceItem[] }[];
  /** The workspaces whose rows show, in order of first appearance. */
  workspaces: string[];
  /** Rows of a workspace other than the open one carry its badge. */
  marks: boolean;
};

/**
 * What of a Space shows (contracts/spaces.md "Rows"): every row of a workspace the user is in,
 * Collab rows only where there is Collab; sections left empty do not show.
 */
export function showSpace(s: Space, ctx: ShowContext): Shown {
  const usable = (i: SpaceItem) => isEverywhere(i) || (ctx.members.has(i.workspace_id) && (!isCollabItem(i) || ctx.collab(i.workspace_id) === true));
  const sections: Shown["sections"] = [];
  const workspaces: string[] = [];
  for (const section of s.sections) {
    const items = section.items.filter(usable);
    for (const i of items) if (!isEverywhere(i) && !workspaces.includes(i.workspace_id)) workspaces.push(i.workspace_id);
    if (items.length) sections.push({ section, items });
  }
  return { sections, workspaces, marks: s.marks };
}

/** The workspaces other than the open one with rows showing: the ones to keep live. */
export function otherWorkspaces(shown: Shown, workspaceId: string | null): string[] {
  return shown.workspaces.filter((w) => w !== workspaceId);
}
