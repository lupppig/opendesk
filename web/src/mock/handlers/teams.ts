// Teams, brands, chat widgets and routing rules (contracts/teams.md).

import {
  LABEL_COLORS,
  type AssignMode,
  type Brand,
  type LabelColor,
  type RoutingCondition,
  type RoutingRule,
  type Team,
  type TeamRole,
  type Widget,
  type WidgetTopic,
} from "@/lib/api/types";
import { db, userRef, type WsData } from "../db";
import { publish } from "../gateway";
import { HttpError, del, forbidden, get, invalid, notFound, paginate, patch, post, put, required, respond, s, strArr, type Ctx } from "../router";
import { defaultWidget } from "../seed";
import {
  brandOut,
  defaultBrand,
  dropLapsedGrants,
  evaluateRouting,
  isAdmin,
  roleIn,
  setTeamAssignMode,
  syncLegacyWidget,
  teamOut,
  teamRole,
  widgetOut,
  type RoutingInput,
} from "../teams";
import { addActivity, publishTicketUpdated } from "../tickets";
import { isRecord, nowIso, randomToken, slugify, uuid } from "../util";
import { parseHours, readUpload } from "./pictures";
import { widgetPatch } from "./widget";
import { checkBotId } from "./bots";
import { forgetHelpDomain, registerHelpDomainRoutes } from "./helpDomains";
import { removeBrandChangelog } from "./changelog";

const TEAM_ROLES: TeamRole[] = ["lead", "member", "observer"];
const MODES: AssignMode[] = ["none", "round_robin", "load_balanced"];
const FIELDS = ["received_at", "mailbox_id", "brand_id", "widget_id", "channel", "from_email", "from_domain", "subject", "body", "page_url", "topic", "ai_decide"];
const OPS: RoutingCondition["op"][] = ["equals", "not_equals", "contains", "not_contains", "starts_with", "gt", "lt"];
const SLUG = /^[a-z0-9-]{3,40}$/;
const HEX = /^#[0-9a-f]{6}$/i;

function requireAdmin(ws: WsData, what = "Only admins can change this") {
  if (!isAdmin(ws, db.meId)) throw forbidden(what);
}

function audit(ws: WsData, action: string, entity_type: string, entity_id: string | null, data: Record<string, unknown> = {}) {
  ws.audit.unshift({ id: uuid(), actor: { type: "user", user: userRef(db.meId), api_key_id: null }, action, entity_type, entity_id, data, ip: "84.152.33.17", created_at: nowIso() });
}

const limit = (n: number, max: number, what: string) => {
  if (n >= max) throw new HttpError(422, "limit_reached", `A workspace can have at most ${max} ${what}.`);
};

// ─── Teams ─────────────────────────────────────────────────────────────────

/** Keys and webhooks lose the private teams whose granter left them (contracts/teams.md "Visibility"). */
export function lapseGrants(ws: WsData) {
  for (const o of dropLapsedGrants(ws)) {
    const hook = "url" in o;
    if (hook) publish(ws, "webhook.updated", { webhook: o });
    audit(ws, hook ? "webhook.updated" : "api_key.updated", hook ? "webhook" : "api_key", o.id, {
      private_team_ids: o.private_team_ids,
      reason: "granter_left_team",
    });
  }
}

function findTeam(ctx: Ctx): Team {
  const t = ctx.ws.teams.find((x) => x.id === ctx.params.id);
  if (!t) throw notFound("Team");
  return t;
}

const publishTeam = (ws: WsData, type: "team.created" | "team.updated", t: Team) => publish(ws, type, { team: teamOut(ws, t) });

/** Validates and writes a team body; members replace the list. */
function writeTeam(ctx: Ctx, t: Team, creating: boolean) {
  const b = ctx.body;
  if (creating || "name" in b) {
    const name = required(ctx, "name").replace(/\s+/g, " ").slice(0, 60);
    if (ctx.ws.teams.some((x) => x !== t && x.name.toLowerCase() === name.toLowerCase())) throw new HttpError(409, "conflict", "A team with this name already exists");
    t.name = name;
  }
  if ("description" in b) {
    const d = (s(ctx, "description") ?? "").trim();
    if (d.length > 280) throw invalid("Keep the description under 280 characters");
    t.description = d;
  }
  if ("color" in b) {
    if (!LABEL_COLORS.includes(b.color as LabelColor)) throw invalid("Unknown colour");
    t.color = b.color as LabelColor;
  }
  if ("private" in b) {
    if (typeof b.private !== "boolean") throw invalid("private must be true or false");
    if (!creating && b.private !== t.private && !isAdmin(ctx.ws, db.meId)) throw forbidden("Only admins make a team private or public");
    t.private = b.private;
  }
  const list = Array.isArray(b.members) ? b.members : null;
  const ids = strArr(b.member_ids);
  if (list || ids) {
    const next = list
      ? list.map((m) => {
          if (!isRecord(m) || typeof m.user_id !== "string") throw invalid("Each member needs user_id and role");
          const role = (m.role ?? "member") as TeamRole;
          if (!TEAM_ROLES.includes(role)) throw invalid("role must be lead, member or observer");
          return { user_id: m.user_id, role };
        })
      : ids!.map((user_id) => ({ user_id, role: t.members.find((x) => x.user_id === user_id)?.role ?? ("member" as TeamRole) }));
    const seen = new Set<string>();
    for (const m of next) {
      const wsRole = ctx.ws.members.find((x) => x.user_id === m.user_id)?.role;
      if (!wsRole) throw invalid("Members must belong to the workspace");
      if (wsRole === "viewer" && m.role !== "observer") throw invalid("Viewers can only be observers");
      if (seen.has(m.user_id)) throw invalid("Each person can be on a team once");
      seen.add(m.user_id);
    }
    t.members = next;
    t.member_ids = next.map((m) => m.user_id);
  }
  if ("signature_html" in b) t.signature_html = typeof b.signature_html === "string" ? b.signature_html.replace(/<script[\s\S]*?<\/script>/gi, "") : "";
  if ("business_hours" in b) {
    if (b.business_hours === null) t.business_hours = null;
    else if (isRecord(b.business_hours)) {
      const tz = typeof b.business_hours.timezone === "string" ? b.business_hours.timezone : (ctx.ws.workspace.settings.timezone ?? "UTC");
      t.business_hours = { ...parseHours(b.business_hours), timezone: tz };
    } else throw invalid("business_hours must be an object or null");
  }
  if ("assign_mode" in b) {
    if (b.assign_mode !== null && !MODES.includes(b.assign_mode as AssignMode)) throw invalid("assign_mode must be none, round_robin, load_balanced or null");
    setTeamAssignMode(ctx.ws, t.id, (b.assign_mode as AssignMode | null) ?? null);
  }
  t.updated_at = nowIso();
}

/** A team turned private or public: other clients drop or regain its tickets. */
function publishPrivacy(ws: WsData, t: Team) {
  for (const x of ws.tickets) {
    if (x.team_id !== t.id || ws.deletedTickets.has(x.id)) continue;
    if (t.private) publish(ws, "ticket.hidden", { ticket_id: x.id, team_id: t.id });
    else publish(ws, "ticket.updated", { ticket: x, changes: ["team_id"] }, { teamId: null });
  }
}

function registerTeamRoutes() {
  get("/v1/teams", (ctx) => paginate(ctx, ctx.ws.teams.map((t) => teamOut(ctx.ws, t)), 100));
  get("/v1/teams/:id", (ctx) => teamOut(ctx.ws, findTeam(ctx)));

  post("/v1/teams", (ctx) => {
    requireAdmin(ctx.ws);
    limit(ctx.ws.teams.length, 200, "teams");
    const now = nowIso();
    const t: Team = {
      id: uuid(),
      name: "",
      description: "",
      color: "gray",
      private: false,
      members: [],
      member_ids: [],
      signature_html: "",
      business_hours: null,
      assign_mode: null,
      mailbox_ids: [],
      open_count: 0,
      unassigned_count: 0,
      created_at: now,
      updated_at: now,
    };
    writeTeam(ctx, t, true);
    ctx.ws.teams.push(t);
    publishTeam(ctx.ws, "team.created", t);
    audit(ctx.ws, "team.created", "team", t.id, { name: t.name, private: t.private });
    return respond(201, teamOut(ctx.ws, t));
  });

  patch("/v1/teams/:id", (ctx) => {
    const t = findTeam(ctx);
    if (!isAdmin(ctx.ws, db.meId) && teamRole(t, db.meId) !== "lead") throw forbidden("Only admins and the team's leads can change it");
    const wasPrivate = t.private;
    writeTeam(ctx, t, false);
    lapseGrants(ctx.ws);
    publishTeam(ctx.ws, "team.updated", t);
    if (wasPrivate !== t.private) {
      audit(ctx.ws, "team.privacy_changed", "team", t.id, { private: t.private });
      publishPrivacy(ctx.ws, t);
    } else audit(ctx.ws, "team.updated", "team", t.id, { fields: Object.keys(ctx.body) });
    return teamOut(ctx.ws, t);
  });

  del("/v1/teams/:id", (ctx) => {
    requireAdmin(ctx.ws);
    const t = findTeam(ctx);
    const moveTo = ctx.query.get("move_to") || null;
    if (moveTo && (moveTo === t.id || !ctx.ws.teams.some((x) => x.id === moveTo))) throw invalid("Choose another team to move its work to");
    const ws = ctx.ws;
    for (const x of ws.tickets) {
      if (x.team_id !== t.id) continue;
      const open = x.status === "open" || x.status === "pending" || x.status === "on_hold";
      x.team_id = open ? moveTo : null;
      if (open) {
        addActivity(ws, x, "team_changed", { from: t.name, to: moveTo ? ws.teams.find((y) => y.id === moveTo)?.name ?? null : null, via: "agent" }, userRef(db.meId));
        publishTicketUpdated(ws, x, ["team_id"]);
      }
    }
    for (const m of ws.mailboxes) if (m.team_id === t.id) {
      m.team_id = moveTo;
      m.private = !!moveTo && !!ws.teams.find((y) => y.id === moveTo)?.private;
      publish(ws, "mailbox.updated", { mailbox: m });
    }
    for (const w of ws.widgets) {
      let changed = false;
      if (w.team_id === t.id) {
        w.team_id = moveTo;
        changed = true;
      }
      for (const topic of w.settings.topics) if (topic.team_id === t.id) {
        topic.team_id = moveTo;
        changed = true;
      }
      if (changed) publish(ws, "widget.updated", { widget: widgetOut(ws, w) });
    }
    for (const r of [...ws.routingRules]) if (r.team_id === t.id) {
      if (moveTo) {
        r.team_id = moveTo;
        publish(ws, "routing_rule.updated", { routing_rule: r });
      } else {
        ws.routingRules = ws.routingRules.filter((x) => x !== r);
        publish(ws, "routing_rule.deleted", { routing_rule_id: r.id });
      }
    }
    // Aliases routed to the team follow it, else go back to their mailbox's team.
    for (const m of ws.mailboxes) {
      for (const [a, id] of Object.entries(m.alias_teams ?? {})) {
        if (id !== t.id) continue;
        if (moveTo) m.alias_teams![a] = moveTo;
        else delete m.alias_teams![a];
      }
    }
    for (const v of ws.views) if (v.team_id === t.id) v.team_id = null;
    for (const r of ws.savedReplies) if (r.team_id === t.id) r.team_id = null;
    for (const p of ws.sla) if (p.team_id === t.id) p.team_id = null;
    for (const k of [...ws.apiKeys, ...ws.webhooks]) {
      if (k.private_team_ids) k.private_team_ids = k.private_team_ids.filter((x) => x !== t.id);
      if (k.private_teams) k.private_teams = k.private_teams.filter((x) => x.id !== t.id);
    }
    setTeamAssignMode(ws, t.id, null);
    ws.teams = ws.teams.filter((x) => x !== t);
    publish(ws, "team.deleted", { team_id: t.id });
    audit(ws, "team.deleted", "team", t.id, { name: t.name, move_to: moveTo });
    return respond(204);
  });

  post("/v1/teams/:id/join", (ctx) => {
    requireAdmin(ctx.ws, "Only admins can join a team this way");
    const t = findTeam(ctx);
    if (!teamRole(t, db.meId)) {
      t.members = [...t.members, { user_id: db.meId, role: "observer" }];
      t.member_ids = t.members.map((m) => m.user_id);
      t.updated_at = nowIso();
      audit(ctx.ws, "team.joined", "team", t.id, { role: "observer" });
      // The server also disconnects the caller so the socket comes back with the team in its token.
      publishTeam(ctx.ws, "team.updated", t);
    }
    return respond(200, teamOut(ctx.ws, t));
  });

  post("/v1/teams/:id/leave", (ctx) => {
    const t = findTeam(ctx);
    const mine = teamRole(t, db.meId);
    if (!mine) return respond(204);
    const leads = t.members.filter((m) => m.role === "lead");
    if (mine === "lead" && leads.length === 1 && t.members.length > 1) throw new HttpError(409, "last_lead", "Make someone else a lead before you leave this team");
    t.members = t.members.filter((m) => m.user_id !== db.meId);
    t.member_ids = t.members.map((m) => m.user_id);
    t.updated_at = nowIso();
    audit(ctx.ws, "team.left", "team", t.id);
    lapseGrants(ctx.ws);
    publishTeam(ctx.ws, "team.updated", t);
    if (t.private) for (const x of ctx.ws.tickets) if (x.team_id === t.id && x.assignee?.id !== db.meId) publish(ctx.ws, "ticket.hidden", { ticket_id: x.id, team_id: t.id });
    return respond(204);
  });
}

// ─── Brands ────────────────────────────────────────────────────────────────

function findBrand(ctx: Ctx): Brand {
  const b = ctx.ws.brands.find((x) => x.id === ctx.params.id);
  if (!b) throw notFound("Brand");
  return b;
}

function slugTaken(slug: string, self: Brand | null) {
  for (const w of db.workspaces) {
    for (const b of w.brands) if (b !== self && b.slug === slug) return true;
    if (w.workspace.slug === slug && !(self?.is_default && w.brands.includes(self))) return true;
  }
  return false;
}

function writeBrand(ctx: Ctx, b: Brand, creating: boolean) {
  const body = ctx.body;
  if (creating || "name" in body) {
    const name = required(ctx, "name").replace(/\s+/g, " ");
    if (name.length > 60) throw invalid("Keep the name under 60 characters");
    b.name = name;
  }
  if (creating || "slug" in body) {
    const raw = s(ctx, "slug");
    const slug = raw !== undefined && raw !== "" ? raw.toLowerCase() : slugify(b.name).slice(0, 40);
    if (!SLUG.test(slug)) throw invalid("Use 3 to 40 lowercase letters, numbers and dashes");
    if (slugTaken(slug, creating ? null : b)) throw new HttpError(409, "slug_taken", "Another help center already uses this address");
    b.slug = slug;
  }
  if ("color" in body) {
    const c = s(ctx, "color") ?? "";
    if (c && !HEX.test(c)) throw invalid("color must be a hex colour like #4f46e5, or empty for the workspace colour");
    b.color = c.toLowerCase();
  }
  if ("locale" in body) b.locale = (s(ctx, "locale") ?? "").slice(0, 20);
  if (isRecord(body.help_center)) {
    const h = body.help_center;
    if ("enabled" in h) b.help_center.enabled = h.enabled === true;
    if (typeof h.title === "string") b.help_center.title = h.title.trim().slice(0, 80);
  }
  b.updated_at = nowIso();
}

const BRAND_SLOTS = ["logo", "help_center_logo", "help_center_favicon"] as const;

function registerBrandRoutes() {
  registerHelpDomainRoutes();
  get("/v1/brands", (ctx) => paginate(ctx, ctx.ws.brands.slice().sort((a, b) => a.position - b.position).map((b) => brandOut(ctx.ws, b)), 100));
  get("/v1/brands/:id", (ctx) => brandOut(ctx.ws, findBrand(ctx)));

  post("/v1/brands", (ctx) => {
    requireAdmin(ctx.ws);
    limit(ctx.ws.brands.length, 50, "brands");
    const now = nowIso();
    const b: Brand = {
      id: uuid(),
      name: "",
      slug: "",
      is_default: false,
      color: "",
      locale: "",
      help_center: { enabled: false, title: "", url: "", default_url: "", domain: null },
      images: { logo: null, help_center_logo: null, help_center_favicon: null },
      default_mailbox_id: null,
      mailbox_count: 0,
      widget_count: 0,
      position: ctx.ws.brands.length,
      created_at: now,
      updated_at: now,
    };
    writeBrand(ctx, b, true);
    if (!b.help_center.title) b.help_center.title = `${b.name} Help`;
    ctx.ws.brands.push(b);
    publish(ctx.ws, "brand.created", { brand: brandOut(ctx.ws, b) });
    audit(ctx.ws, "brand.created", "brand", b.id, { name: b.name });
    return respond(201, brandOut(ctx.ws, b));
  });

  patch("/v1/brands/:id", (ctx) => {
    requireAdmin(ctx.ws);
    const b = findBrand(ctx);
    writeBrand(ctx, b, false);
    publish(ctx.ws, "brand.updated", { brand: brandOut(ctx.ws, b) });
    audit(ctx.ws, "brand.updated", "brand", b.id, { fields: Object.keys(ctx.body) });
    return brandOut(ctx.ws, b);
  });

  del("/v1/brands/:id", (ctx) => {
    requireAdmin(ctx.ws);
    const b = findBrand(ctx);
    if (b.is_default) throw new HttpError(409, "default_brand", "The default brand can't be deleted. Make another brand the default first.");
    if (ctx.ws.mailboxes.some((m) => m.brand_id === b.id || Object.values(m.alias_settings ?? {}).some((x) => x.brand_id === b.id)))
      throw new HttpError(409, "brand_in_use", "Move or remove this brand's addresses and aliases first.");
    for (const w of ctx.ws.widgets.filter((x) => x.brand_id === b.id)) {
      delete ctx.ws.widgetSecrets[w.id];
      publish(ctx.ws, "widget.deleted", { widget_id: w.id });
    }
    ctx.ws.widgets = ctx.ws.widgets.filter((x) => x.brand_id !== b.id);
    removeBrandChangelog(ctx.ws, b.id);
    ctx.ws.brands = ctx.ws.brands.filter((x) => x !== b);
    forgetHelpDomain(b.id);
    ctx.ws.brands.sort((x, y) => x.position - y.position).forEach((x, i) => (x.position = i));
    publish(ctx.ws, "brand.deleted", { brand_id: b.id });
    audit(ctx.ws, "brand.deleted", "brand", b.id, { name: b.name });
    return respond(204);
  });

  post("/v1/brands/:id/default", (ctx) => {
    requireAdmin(ctx.ws);
    const b = findBrand(ctx);
    for (const x of ctx.ws.brands) {
      const was = x.is_default;
      x.is_default = x === b;
      if (was !== x.is_default) publish(ctx.ws, "brand.updated", { brand: brandOut(ctx.ws, x) });
    }
    syncLegacyWidget(ctx.ws);
    audit(ctx.ws, "brand.made_default", "brand", b.id, { name: b.name });
    return respond(200, brandOut(ctx.ws, b));
  });

  put("/v1/brands/:id/images/:slot", async (ctx) => {
    requireAdmin(ctx.ws);
    const b = findBrand(ctx);
    const slot = ctx.params.slot as (typeof BRAND_SLOTS)[number];
    if (!BRAND_SLOTS.includes(slot)) throw notFound("Image slot");
    b.images = { ...b.images, [slot]: await readUpload(ctx) };
    publish(ctx.ws, "brand.updated", { brand: brandOut(ctx.ws, b) });
    return respond(200, brandOut(ctx.ws, b));
  });
  del("/v1/brands/:id/images/:slot", (ctx) => {
    requireAdmin(ctx.ws);
    const b = findBrand(ctx);
    const slot = ctx.params.slot as (typeof BRAND_SLOTS)[number];
    if (!BRAND_SLOTS.includes(slot)) throw notFound("Image slot");
    b.images = { ...b.images, [slot]: null };
    publish(ctx.ws, "brand.updated", { brand: brandOut(ctx.ws, b) });
    return respond(200, brandOut(ctx.ws, b));
  });
}

// ─── Widgets ───────────────────────────────────────────────────────────────

function findWidget(ctx: Ctx): Widget {
  const w = ctx.ws.widgets.find((x) => x.id === ctx.params.id);
  if (!w) throw notFound("Widget");
  return w;
}

const checkTeam = (ws: WsData, id: unknown): string | null => {
  if (id === null || id === "" || id === undefined) return null;
  if (typeof id !== "string" || !ws.teams.some((t) => t.id === id)) throw invalid("Unknown team");
  return id;
};

/** settings.topics: at most 8, each a 2..40 character label and a team or none. */
function parseTopics(ws: WsData, v: unknown): WidgetTopic[] {
  if (!Array.isArray(v)) throw invalid("topics must be a list");
  if (v.length > 8) throw invalid("Add at most 8 topics");
  const seen = new Set<string>();
  return v.map((x) => {
    if (!isRecord(x)) throw invalid("Each topic needs a label");
    const label = typeof x.label === "string" ? x.label.replace(/\s+/g, " ").trim() : "";
    if (label.length < 2 || label.length > 40 || /[<>]/.test(label)) throw invalid("Each topic is 2 to 40 characters, without < or >");
    if (seen.has(label.toLowerCase())) throw invalid("Topics must be different");
    seen.add(label.toLowerCase());
    return { id: typeof x.id === "string" && x.id ? x.id : uuid(), label, team_id: checkTeam(ws, x.team_id) };
  });
}

function writeWidgetSettings(ws: WsData, w: Widget, raw: unknown) {
  if (!isRecord(raw)) return;
  const { topics, offline_message, email_follow_up, ...rest } = raw;
  const next = { ...w.settings, ...widgetPatch(rest) };
  if (topics !== undefined) next.topics = parseTopics(ws, topics);
  if (offline_message !== undefined) {
    if (typeof offline_message !== "string" || offline_message.trim().length > 300 || /[<>]/.test(offline_message)) throw invalid("Keep the offline message under 300 characters, as plain text");
    next.offline_message = offline_message.trim();
  }
  if (email_follow_up !== undefined) {
    if (typeof email_follow_up !== "boolean") throw invalid("email_follow_up must be true or false");
    next.email_follow_up = email_follow_up;
  }
  w.settings = next;
}

function registerWidgetRoutes() {
  get("/v1/widgets", (ctx) => {
    requireAdmin(ctx.ws);
    const brand = ctx.query.get("brand_id");
    const pos = (w: Widget) => (ctx.ws.brands.find((b) => b.id === w.brand_id)?.position ?? 0) * 1000 + w.position;
    const rows = ctx.ws.widgets.filter((w) => !brand || w.brand_id === brand).sort((a, b) => pos(a) - pos(b));
    return paginate(ctx, rows.map((w) => widgetOut(ctx.ws, w)), 100);
  });
  get("/v1/widgets/:id", (ctx) => {
    requireAdmin(ctx.ws);
    return widgetOut(ctx.ws, findWidget(ctx));
  });

  post("/v1/widgets", (ctx) => {
    requireAdmin(ctx.ws);
    const brandId = required(ctx, "brand_id");
    const brand = ctx.ws.brands.find((b) => b.id === brandId);
    if (!brand) throw invalid("Unknown brand");
    limit(ctx.ws.widgets.filter((w) => w.brand_id === brandId).length, 50, "widgets per brand");
    const name = required(ctx, "name").replace(/\s+/g, " ");
    if (name.length > 60) throw invalid("Keep the name under 60 characters");
    const now = nowIso();
    const w: Widget = {
      id: uuid(),
      brand_id: brandId,
      name,
      key: `wk_${brand.slug.replace(/-/g, "")}_${randomToken(10).replace(/[-_]/g, "x")}`,
      team_id: checkTeam(ctx.ws, ctx.body.team_id),
      bot_id: checkBotId(ctx.ws, ctx.body.bot_id ?? null),
      settings: { ...defaultWidget(), color: brand.color, title: brand.name },
      images: { avatar: null, launcher: null },
      install: { snippet: "" },
      seen_at: null,
      position: ctx.ws.widgets.filter((x) => x.brand_id === brandId).length,
      created_at: now,
      updated_at: now,
    };
    writeWidgetSettings(ctx.ws, w, ctx.body.settings);
    const secret = `wsec_${randomToken(40)}`;
    ctx.ws.widgets.push(w);
    ctx.ws.widgetSecrets[w.id] = secret;
    syncLegacyWidget(ctx.ws);
    publish(ctx.ws, "widget.created", { widget: widgetOut(ctx.ws, w) });
    audit(ctx.ws, "widget.created", "widget", w.id, { name });
    // The first secret is answered only past the owner-level gate; otherwise rotate to see one.
    if (db.stepUp !== "ok") return respond(201, { ...widgetOut(ctx.ws, w), secret_available: false });
    return respond(201, { ...widgetOut(ctx.ws, w), secret, secret_available: true });
  });

  patch("/v1/widgets/:id", (ctx) => {
    requireAdmin(ctx.ws);
    const w = findWidget(ctx);
    if ("name" in ctx.body) {
      const name = required(ctx, "name").replace(/\s+/g, " ");
      if (name.length > 60) throw invalid("Keep the name under 60 characters");
      w.name = name;
    }
    if ("team_id" in ctx.body) w.team_id = checkTeam(ctx.ws, ctx.body.team_id);
    if ("bot_id" in ctx.body) w.bot_id = checkBotId(ctx.ws, ctx.body.bot_id);
    writeWidgetSettings(ctx.ws, w, ctx.body.settings);
    w.updated_at = nowIso();
    syncLegacyWidget(ctx.ws);
    publish(ctx.ws, "widget.updated", { widget: widgetOut(ctx.ws, w) });
    audit(ctx.ws, "widget.updated", "widget", w.id, { fields: Object.keys(ctx.body), settings: isRecord(ctx.body.settings) ? Object.keys(ctx.body.settings) : [], ...("bot_id" in ctx.body ? { bot_id: w.bot_id } : {}) });
    return widgetOut(ctx.ws, w);
  });

  del("/v1/widgets/:id", (ctx) => {
    requireAdmin(ctx.ws);
    const w = findWidget(ctx);
    ctx.ws.widgets = ctx.ws.widgets.filter((x) => x !== w);
    delete ctx.ws.widgetSecrets[w.id];
    ctx.ws.widgets.filter((x) => x.brand_id === w.brand_id).sort((a, b) => a.position - b.position).forEach((x, i) => (x.position = i));
    syncLegacyWidget(ctx.ws);
    publish(ctx.ws, "widget.deleted", { widget_id: w.id });
    audit(ctx.ws, "widget.deleted", "widget", w.id, { name: w.name });
    return respond(204);
  });

  post("/v1/widgets/:id/secret/rotate", (ctx) => {
    if (roleIn(ctx.ws, db.meId) !== "owner" && roleIn(ctx.ws, db.meId) !== "admin") throw forbidden("Only admins can rotate the secret");
    if (db.stepUp === "mfa_required") throw new HttpError(403, "mfa_required", "this action requires two-factor authentication");
    if (db.stepUp === "reauth_required") throw new HttpError(403, "reauth_required", "For your security, sign in again to do this. Your last sign-in is more than 12 hours old.");
    const w = findWidget(ctx);
    const secret = `wsec_${randomToken(40)}`;
    ctx.ws.widgetSecrets[w.id] = secret;
    audit(ctx.ws, "widget.secret_rotated", "widget", w.id);
    return respond(200, { secret });
  });

  put("/v1/widgets/:id/images/:slot", async (ctx) => {
    requireAdmin(ctx.ws);
    const w = findWidget(ctx);
    const slot = ctx.params.slot;
    if (slot !== "avatar" && slot !== "launcher") throw notFound("Image slot");
    w.images = { ...w.images, [slot]: await readUpload(ctx) };
    publish(ctx.ws, "widget.updated", { widget: widgetOut(ctx.ws, w) });
    return respond(200, widgetOut(ctx.ws, w));
  });
  del("/v1/widgets/:id/images/:slot", (ctx) => {
    requireAdmin(ctx.ws);
    const w = findWidget(ctx);
    const slot = ctx.params.slot;
    if (slot !== "avatar" && slot !== "launcher") throw notFound("Image slot");
    w.images = { ...w.images, [slot]: null };
    publish(ctx.ws, "widget.updated", { widget: widgetOut(ctx.ws, w) });
    return respond(200, widgetOut(ctx.ws, w));
  });
}

// ─── Routing rules ─────────────────────────────────────────────────────────

function findRule(ctx: Ctx): RoutingRule {
  const r = ctx.ws.routingRules.find((x) => x.id === ctx.params.id);
  if (!r) throw notFound("Routing rule");
  return r;
}

function parseConditions(v: unknown): RoutingCondition[] {
  if (!Array.isArray(v) || v.length < 1 || v.length > 20) throw invalid("A rule needs 1 to 20 conditions");
  return v.map((c) => {
    if (!isRecord(c)) throw invalid("Each condition needs field, op and value");
    const field = typeof c.field === "string" ? c.field : "";
    if (!FIELDS.includes(field) && !/^contact\.field\.[a-z0-9_]{1,40}$/.test(field)) throw invalid(`Unknown field ${field}`);
    const op = c.op as RoutingCondition["op"];
    if (!OPS.includes(op)) throw invalid("Unknown operator");
    const value = typeof c.value === "string" ? c.value.trim().slice(0, 500) : "";
    if (field === "ai_decide") {
      const p = Number(value);
      if (op !== "gt" && op !== "lt") throw invalid("Ollie decides takes more than or less than");
      if (!(p >= 0.01 && p <= 0.99)) throw invalid("The confidence is between 1% and 99%");
      const question = typeof c.question === "string" ? c.question.trim() : "";
      if (question.length < 3 || question.length > 300) throw invalid("Ask Ollie a yes or no question of 3 to 300 characters");
      return { field, op, value, question } as RoutingCondition;
    }
    if (!value) throw invalid("Each condition needs a value");
    return { field, op, value } as RoutingCondition;
  });
}

function writeRule(ctx: Ctx, r: RoutingRule, creating: boolean) {
  const b = ctx.body;
  if (creating || "name" in b) {
    const name = required(ctx, "name").replace(/\s+/g, " ");
    if (name.length > 80) throw invalid("Keep the name under 80 characters");
    r.name = name;
  }
  if ("enabled" in b) r.enabled = b.enabled !== false;
  if ("match" in b) {
    if (b.match !== "all" && b.match !== "any") throw invalid("match must be all or any");
    r.match = b.match;
  }
  if (creating || "conditions" in b) r.conditions = parseConditions(b.conditions);
  if (creating || "team_id" in b) {
    const t = checkTeam(ctx.ws, b.team_id);
    if (!t) throw invalid("Choose the team this rule sends tickets to");
    r.team_id = t;
  }
  r.updated_at = nowIso();
}

function registerRoutingRoutes() {
  get("/v1/routing-rules", (ctx) => {
    requireAdmin(ctx.ws);
    return paginate(ctx, ctx.ws.routingRules.slice().sort((a, b) => a.position - b.position), 100);
  });
  get("/v1/routing-rules/:id", (ctx) => {
    requireAdmin(ctx.ws);
    return findRule(ctx);
  });
  post("/v1/routing-rules", (ctx) => {
    requireAdmin(ctx.ws);
    limit(ctx.ws.routingRules.length, 200, "routing rules");
    const now = nowIso();
    const r: RoutingRule = { id: uuid(), name: "", enabled: true, position: ctx.ws.routingRules.length, run_count: 0, match: "all", conditions: [], team_id: "", created_at: now, updated_at: now };
    writeRule(ctx, r, true);
    ctx.ws.routingRules.push(r);
    publish(ctx.ws, "routing_rule.created", { routing_rule: r });
    audit(ctx.ws, "routing_rule.created", "routing_rule", r.id, { name: r.name });
    return respond(201, r);
  });
  post("/v1/routing-rules/reorder", (ctx) => {
    requireAdmin(ctx.ws);
    const ids = strArr(ctx.body.ids) ?? [];
    const rules = ctx.ws.routingRules;
    if (ids.length !== rules.length || ids.some((id) => !rules.some((r) => r.id === id))) throw invalid("ids must list every rule once");
    ids.forEach((id, i) => {
      const r = rules.find((x) => x.id === id)!;
      if (r.position !== i) {
        r.position = i;
        publish(ctx.ws, "routing_rule.updated", { routing_rule: r });
      }
    });
    audit(ctx.ws, "routing_rule.reordered", "workspace", ctx.ws.workspace.id);
    return respond(204);
  });
  patch("/v1/routing-rules/:id", (ctx) => {
    requireAdmin(ctx.ws);
    const r = findRule(ctx);
    writeRule(ctx, r, false);
    publish(ctx.ws, "routing_rule.updated", { routing_rule: r });
    audit(ctx.ws, "routing_rule.updated", "routing_rule", r.id, { fields: Object.keys(ctx.body) });
    return r;
  });
  del("/v1/routing-rules/:id", (ctx) => {
    requireAdmin(ctx.ws);
    const r = findRule(ctx);
    ctx.ws.routingRules = ctx.ws.routingRules.filter((x) => x !== r);
    ctx.ws.routingRules.sort((a, b) => a.position - b.position).forEach((x, i) => (x.position = i));
    publish(ctx.ws, "routing_rule.deleted", { routing_rule_id: r.id });
    audit(ctx.ws, "routing_rule.deleted", "routing_rule", r.id, { name: r.name });
    return respond(204);
  });

  post("/v1/routing/test", (ctx) => {
    requireAdmin(ctx.ws);
    const channel = s(ctx, "channel");
    if (channel !== "email" && channel !== "chat") throw invalid("channel must be email or chat");
    const input: RoutingInput = {
      channel,
      to: s(ctx, "to"),
      from: s(ctx, "from"),
      subject: s(ctx, "subject"),
      body: s(ctx, "body"),
      widget_id: s(ctx, "widget_id"),
      topic: s(ctx, "topic"),
      page_url: s(ctx, "page_url"),
    };
    if (channel === "email" && !input.to) throw invalid("Enter the address the email is sent to");
    if (channel === "chat" && !input.widget_id) throw invalid("Choose a widget");
    const from = input.from?.toLowerCase();
    const contact = from ? ctx.ws.contacts.find((c) => (c.email ?? "").toLowerCase() === from) : undefined;
    const r = evaluateRouting(ctx.ws, { ...input, contact_fields: contact ? { ...contact.custom, ...contact.fields } : {} });
    return respond(200, { team_id: r.team_id, brand_id: r.brand_id, mailbox_id: r.mailbox_id, routed: r.routed, steps: r.steps, assign_mode: r.assign_mode });
  });
}

export function registerTeams() {
  registerTeamRoutes();
  registerBrandRoutes();
  registerWidgetRoutes();
  registerRoutingRoutes();
}

/** The default brand for a new workspace, named after it. */
export function seedDefaultBrand(ws: WsData) {
  if (ws.brands.length) return;
  const now = ws.workspace.created_at;
  ws.brands.push({
    id: uuid(),
    name: ws.workspace.name,
    slug: ws.workspace.slug,
    is_default: true,
    color: ws.workspace.settings.brand_color ?? "",
    locale: ws.workspace.settings.locale ?? "",
    help_center: { enabled: !!ws.workspace.settings.help_center_enabled, title: ws.workspace.settings.help_center_title || `${ws.workspace.name} Help`, url: "", default_url: "", domain: null },
    images: { logo: ws.workspace.avatar_url, help_center_logo: ws.workspace.images.help_center_logo, help_center_favicon: ws.workspace.images.help_center_favicon },
    default_mailbox_id: null,
    mailbox_count: 0,
    widget_count: 0,
    position: 0,
    created_at: now,
    updated_at: now,
  });
  const b = defaultBrand(ws)!;
  for (const m of ws.mailboxes) if (!m.brand_id) m.brand_id = b.id;
  if (!ws.widgets.length) {
    ws.widgets.push({
      id: uuid(),
      brand_id: b.id,
      name: "Website",
      key: ws.workspace.widget_key,
      team_id: null,
      bot_id: null,
      settings: ws.widget,
      images: { avatar: ws.workspace.images.widget_avatar, launcher: ws.workspace.images.widget_launcher },
      install: { snippet: "" },
      seen_at: ws.routing.widgetSeen ? now : null,
      position: 0,
      created_at: now,
      updated_at: now,
    });
    ws.widgetSecrets[ws.widgets[0].id] = `wsec_${randomToken(40)}`;
  }
  syncLegacyWidget(ws);
}
