// Spaces in mock mode (contracts/spaces.md): each user's list (none until they save one) and
// each workspace's starter Space, offered to start a new Space from. Checked as the API
// checks them: unknown fields refused, 422 invalid_name for a refused name, 422
// validation_failed for everything else.

import type { Space, SpaceItem, SpaceSection, SpacesResponse } from "@/lib/api/types";
import { isSpaceColor, isSpaceIcon, LIMITS, SPACE_KINDS, spaceProblems, type SpaceProblem } from "@/lib/spaces/model";
import { db, me, type WsData } from "../db";
import { del, forbidden, get, HttpError, put, respond } from "../router";
import { isRecord, nowIso, uuid } from "../util";
import { memberships } from "./auth";

/** Saved lists, by user id. */
const saved = new Map<string, Space[]>();
/** Starter Spaces, by workspace id. */
const starters = new Map<string, Space>();
export function resetSpaces() {
  saved.clear();
  starters.clear();
}

const invalid = (detail: string) => new HttpError(422, "validation_failed", detail);

function memberIds(uid: string): Set<string> {
  return new Set(memberships(uid).map((m) => m.workspace.id));
}

/** The saved list with items of workspaces the user left taken out (a section left empty stays). */
function visible(uid: string, list: Space[]): Space[] {
  const ids = memberIds(uid);
  return list.map((s) => ({ ...s, sections: s.sections.map((sec) => ({ ...sec, items: sec.items.filter((i) => i.kind === "everywhere" || ids.has(i.workspace_id)) })) }));
}

export function spacesOf(uid: string): SpacesResponse {
  const list = saved.get(uid);
  return list ? { spaces: structuredClone(visible(uid, list)), saved: true } : { spaces: [], saved: false };
}

function only(v: Record<string, unknown>, keys: string[], what: string) {
  for (const k of Object.keys(v)) if (!keys.includes(k)) throw invalid(`${what} has an unknown field: ${k}`);
}

const chars = (s: string) => [...s].length;

/** A name people choose: 1..40 characters, no control characters. */
function name(v: unknown, what: string, max: number, empty = false): string {
  if (typeof v !== "string") throw invalid(`${what} must be text`);
  const s = v.replace(/\s+/g, " ").trim();
  // eslint-disable-next-line no-control-regex
  if ((!s && !empty) || chars(s) > max || /[\u0000-\u001f\u007f]/.test(v)) throw new HttpError(422, "invalid_name", `${what} must be ${empty ? 0 : 1} to ${max} characters`);
  return s;
}

function readItem(v: unknown): SpaceItem {
  if (!isRecord(v)) throw invalid("An item must be an object");
  only(v, ["workspace_id", "kind", "id", "name", "badge"], "An item");
  if (typeof v.workspace_id !== "string" || typeof v.id !== "string" || typeof v.kind !== "string") throw invalid("An item needs workspace_id, kind and id");
  if (!(SPACE_KINDS as readonly string[]).includes(v.kind)) throw invalid(`Unknown item kind: ${v.kind}`);
  if (!v.id || v.id.length > LIMITS.itemId) throw invalid("An item id is 1 to 100 characters");
  const out: SpaceItem = { workspace_id: v.workspace_id, kind: v.kind as SpaceItem["kind"], id: v.id };
  if (v.name !== undefined && v.name !== "") out.name = name(v.name, "A row name", LIMITS.rowName);
  if (v.badge !== undefined) {
    if (!["unread", "total", "count", "awaiting", "urgent", "overdue", "open", "none"].includes(String(v.badge))) throw invalid("Unknown badge");
    out.badge = v.badge as SpaceItem["badge"];
  }
  return out;
}

function readSection(v: unknown): SpaceSection {
  if (!isRecord(v)) throw invalid("A section must be an object");
  only(v, ["id", "title", "collapsed", "items"], "A section");
  if (typeof v.id !== "string") throw invalid("A section needs an id");
  if (v.collapsed !== undefined && typeof v.collapsed !== "boolean") throw invalid("collapsed must be true or false");
  if (v.items !== undefined && !Array.isArray(v.items)) throw invalid("items must be a list");
  return {
    id: v.id,
    title: v.title === undefined ? "" : name(v.title, "A section title", LIMITS.title, true),
    collapsed: v.collapsed === true,
    items: ((v.items as unknown[] | undefined) ?? []).map(readItem),
  };
}

/** A Space from a request body, with the defaults for what it leaves out. */
export function readSpace(v: unknown): Space {
  if (!isRecord(v)) throw invalid("A Space must be an object");
  only(v, ["id", "name", "icon", "color", "marks", "sections"], "A Space");
  if (typeof v.id !== "string" || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(v.id)) throw invalid("A Space id must be a uuid");
  const icon = v.icon === undefined ? "" : v.icon;
  if (icon !== "" && !isSpaceIcon(icon)) throw invalid("Unknown icon");
  const color = v.color === undefined ? "grey" : v.color;
  if (!isSpaceColor(color)) throw invalid("Unknown colour");
  if (v.marks !== undefined && typeof v.marks !== "boolean") throw invalid("marks must be true or false");
  if (v.sections !== undefined && !Array.isArray(v.sections)) throw invalid("sections must be a list");
  return {
    id: v.id.toLowerCase(),
    name: name(v.name, "The name", LIMITS.name),
    icon: icon as string,
    color,
    marks: v.marks !== false,
    sections: ((v.sections as unknown[] | undefined) ?? []).map(readSection),
  };
}

const PROBLEM_TEXT: Record<SpaceProblem["code"], string> = {
  name_required: "The name is 1 to 40 characters",
  name_too_long: "The name is 1 to 40 characters",
  too_many_sections: "A Space holds at most 20 sections",
  title_too_long: "A section title is at most 40 characters",
  section_too_many_items: "A section holds at most 60 items",
  too_many_items: "A Space holds at most 300 items",
  row_name_too_long: "A row name is 1 to 64 characters",
  not_member: "An item names a workspace you are not a member of",
  bad_section_id: "Section ids are 1 to 40 of A-Z, a-z, 0-9, _ and -, once per Space",
  bad_item: "An item is not valid",
};

function check(s: Space, members: Set<string>) {
  const p = spaceProblems(s, members)[0];
  if (!p) return;
  if (p.code === "name_required" || p.code === "name_too_long" || p.code === "title_too_long" || p.code === "row_name_too_long") throw new HttpError(422, "invalid_name", PROBLEM_TEXT[p.code]);
  throw invalid(PROBLEM_TEXT[p.code]);
}

function admin(ws: WsData) {
  const r = ws.members.find((m) => m.user_id === db.meId)?.role;
  if (r !== "owner" && r !== "admin") throw forbidden("Only admins can change the starter Space.");
}

function audit(ws: WsData, action: string, data: Record<string, unknown>) {
  const u = me();
  ws.audit.unshift({
    id: uuid(),
    actor: { type: "user", user: { id: u.id, name: u.name, email: u.email, avatar_url: u.avatar_url }, api_key_id: null },
    action,
    entity_type: "workspace",
    entity_id: ws.workspace.id,
    data,
    ip: "[IP]",
    created_at: nowIso(),
  });
}

export function registerSpaces() {
  get("/v1/me/spaces", () => spacesOf(db.meId));

  put("/v1/me/spaces", (ctx) => {
    only(ctx.body, ["spaces"], "The body");
    if (!Array.isArray(ctx.body.spaces)) throw invalid("spaces must be a list");
    if (ctx.body.spaces.length > LIMITS.spaces) throw invalid("At most 20 Spaces");
    if (new TextEncoder().encode(JSON.stringify(ctx.body)).length > LIMITS.bytes) throw invalid("Spaces are limited to 64 KB");
    const list = ctx.body.spaces.map(readSpace);
    const ids = new Set<string>();
    const members = memberIds(db.meId);
    for (const s of list) {
      if (ids.has(s.id)) throw invalid("Each Space id once");
      ids.add(s.id);
      check(s, members);
    }
    saved.set(db.meId, list);
    return spacesOf(db.meId);
  });

  get("/v1/sidebar/starter", (ctx) => ({ space: structuredClone(starters.get(ctx.ws.workspace.id) ?? null) }));

  put("/v1/sidebar/starter", (ctx) => {
    admin(ctx.ws);
    if (new TextEncoder().encode(JSON.stringify(ctx.body)).length > LIMITS.bytes) throw invalid("A Space is limited to 64 KB");
    const wid = ctx.ws.workspace.id;
    const s = readSpace(ctx.body);
    // Every item is of this workspace, whatever the body says.
    if (s.sections.some((sec) => sec.items.some((i) => i.kind === "everywhere"))) throw invalid("A starter Space's rows are all of its workspace: an everywhere row can't be one.");
    s.sections = s.sections.map((sec) => ({ ...sec, items: sec.items.map((i) => ({ ...i, workspace_id: wid })) }));
    check(s, new Set([wid]));
    starters.set(wid, s);
    audit(ctx.ws, "sidebar.starter_updated", { name: s.name, sections: s.sections.length });
    return { space: structuredClone(s) };
  });

  del("/v1/sidebar/starter", (ctx) => {
    admin(ctx.ws);
    if (starters.delete(ctx.ws.workspace.id)) audit(ctx.ws, "sidebar.starter_removed", {});
    return respond(204);
  });
}
