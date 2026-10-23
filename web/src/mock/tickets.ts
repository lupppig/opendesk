// Ticket rules shared by the seed and the handlers: SLA, filtering, counts,
// adding messages and applying patches (with activities and events).

import type {
  Activity,
  Attachment,
  Channel,
  Mailbox,
  Message,
  Priority,
  SlaPolicy,
  TagRef,
  Ticket,
  TicketCounts,
  TicketStats,
  TicketFilters,
  TicketPatch,
  TicketRouted,
  TicketStatus,
  UserRef,
} from "@/lib/api/types";
import { contactRef, db, userRef, type WsData } from "./db";
import { publish } from "./gateway";
import { canSeeTicket, defaultBrand, teamRole } from "./teams";
import { applyLabels, checkRequiredOnSolve, fieldDefs, fieldFiltersFromQuery, fieldsText, fieldSortValue, matchFields, mergeFields, type FieldFilterIn } from "./labels";
import { MIN, nowIso, preview, stripHtml, uuid } from "./util";
import { botForTicket, botRefFor } from "./bots";

export const OPENISH: TicketStatus[] = ["open", "pending", "on_hold"];

// ─── Teams, visibility and addresses (contracts/teams.md) ──────────────────

export function myTeamIds(ws: WsData, userId: string): string[] {
  return ws.teams.filter((x) => teamRole(x, userId)).map((x) => x.id);
}

/** Only a private team's members send from its addresses. */
export function canSendFrom(ws: WsData, m: Mailbox, userId: string): boolean {
  const team = m.team_id ? ws.teams.find((x) => x.id === m.team_id) : undefined;
  return !m.private || (!!team && !!teamRole(team, userId));
}

/** The brand's address whose default team is this team (the oldest active one). */
export function teamAddress(ws: WsData, brandId: string, teamId: string | null): Mailbox | undefined {
  if (!teamId) return undefined;
  return ws.mailboxes
    .filter((m) => m.brand_id === brandId && m.team_id === teamId && m.status !== "disabled")
    .sort((a, b) => Date.parse(a.created_at) - Date.parse(b.created_at))[0];
}

/**
 * Moving to another team switches the reply address when the current one's default
 * team is the old team (or none) and the brand has an address for the new team.
 */
export function addressAfterMove(ws: WsData, t: Ticket, toTeam: string | null): Mailbox | null {
  const cur = ws.mailboxes.find((m) => m.id === t.mailbox_id);
  if (!cur || !toTeam || toTeam === t.team_id) return null;
  if (cur.team_id && cur.team_id !== t.team_id) return null;
  const next = teamAddress(ws, t.brand_id, toTeam);
  return next && next.id !== cur.id ? next : null;
}

/** The From name customers see: the address's format filled in (contracts/teams.md "Reply address"). */
export function fromName(ws: WsData, t: Ticket, m: Mailbox, agent: string | null): string {
  const team = ws.teams.find((x) => x.id === t.team_id)?.name;
  const brand = ws.brands.find((b) => b.id === (t.brand_id || m.brand_id))?.name ?? "";
  const alias = t.received_at.toLowerCase();
  const mailbox = (m.aliases.includes(alias) && m.alias_settings?.[alias]?.display_name) || m.display_name || m.name;
  let f = (m.from_name_format || "{agent} at {mailbox}").trim();
  if (!/\{(team|brand|mailbox|agent|agent_first)\}/.test(f)) return f || mailbox;
  if (!agent) {
    f = f.replace(/\{agent(_first)?\}[^{]*/g, "");
    if (!/\{(team|brand|mailbox)\}/.test(f)) return mailbox;
  }
  const vars: Record<string, string> = { agent: agent ?? "", agent_first: agent?.split(" ")[0] ?? "", team: team || brand, brand, mailbox };
  return f.replace(/\{(agent_first|agent|team|brand|mailbox)\}/g, (_, k: string) => vars[k] ?? "").replace(/\s+/g, " ").trim() || mailbox;
}

/**
 * The address a reply goes out From: the alias the customer wrote to, when it belongs to that
 * mailbox and the provider sends as it (contracts/teams.md "Aliases"), else the mailbox's own.
 */
function sendAs(t: Ticket, m: Mailbox): string {
  const r = t.received_at.toLowerCase();
  if (!r || !m.aliases.some((a) => a.toLowerCase() === r)) return m.email;
  const st = m.alias_settings?.[r]?.send_as ?? "unknown";
  const self = m.outbound_kind === "smtp" || (m.outbound_kind === "google" && st === "verified") || (m.outbound_kind === "microsoft" && st !== "unverified");
  return self ? t.received_at : m.email;
}

/** A team filter: ids, "mine" and "none", comma separated. */
function teamMatches(ws: WsData, filter: string, t: Ticket, meId: string): boolean {
  const mine = filter.includes("mine") ? myTeamIds(ws, meId) : [];
  return filter
    .split(",")
    .filter(Boolean)
    .some((p) => (p === "none" ? !t.team_id : p === "mine" ? !!t.team_id && mine.includes(t.team_id) : t.team_id === p));
}

/**
 * As the server: the first enabled policy in list order whose priority (or any) and team (or any)
 * match. A priority or team of its own only wins between policies at the same position.
 */
export function policyFor(ws: WsData, priority: Priority, teamId: string | null = null) {
  const rank = (p: SlaPolicy) => [p.position, p.priority === null ? 1 : 0, p.team_id ? 0 : 1];
  const matching = ws.sla.filter((p) => p.enabled && (p.priority === null || p.priority === priority) && (!p.team_id || p.team_id === teamId));
  return matching.sort((a, b) => {
    const [x, y] = [rank(a), rank(b)];
    return x[0] - y[0] || x[1] - y[1] || x[2] - y[2];
  })[0] ?? null;
}

/** Recompute due dates and breach from the ticket's policy. */
export function computeSla(ws: WsData, t: Ticket, keepPolicy = true): Ticket["sla"] {
  const policy = keepPolicy && t.sla ? ws.sla.find((p) => p.id === t.sla!.policy_id) ?? policyFor(ws, t.priority, t.team_id) : policyFor(ws, t.priority, t.team_id);
  if (!policy) return null;
  const created = Date.parse(t.created_at);
  const fr = created + policy.first_response_minutes * MIN;
  const res = created + policy.resolution_minutes * MIN;
  const now = Date.now();
  const open = OPENISH.includes(t.status);
  const frBreached = t.first_responded_at ? Date.parse(t.first_responded_at) > fr : open && now > fr;
  const resBreached = t.solved_at ? Date.parse(t.solved_at) > res : open && now > res;
  return {
    policy_id: policy.id,
    first_response_due_at: new Date(fr).toISOString(),
    resolution_due_at: new Date(res).toISOString(),
    breached: frBreached || resBreached,
  };
}

export type ListFilters = TicketFilters & { contact_id?: string; spam?: boolean; view_id?: string; fieldFilters?: FieldFilterIn[] };

export function resolveFilters(ws: WsData, f: ListFilters): ListFilters {
  if (!f.view_id) return f;
  const v = ws.views.find((x) => x.id === f.view_id);
  if (!v) return f;
  // Only filters the request actually sets narrow the view's own.
  const set = Object.fromEntries(Object.entries(f).filter(([, x]) => x !== undefined)) as ListFilters;
  return { ...v.filters, ...set, fields: [...(v.filters.fields ?? []), ...(f.fields ?? [])], view_id: undefined };
}

export function matches(ws: WsData, t: Ticket, raw: ListFilters, meId: string): boolean {
  const f = resolveFilters(ws, raw);
  if (ws.deletedTickets.has(t.id)) return false;
  if (!canSeeTicket(ws, t, meId)) return false;
  if ((f.spam ?? false) !== t.spam) return false;
  if (f.status?.length && !f.status.includes(t.status)) return false;
  if (f.priority?.length && !f.priority.includes(t.priority)) return false;
  if (f.severity?.length && !f.severity.includes(t.severity ?? "none")) return false;
  if (f.assignee === "me" && t.assignee?.id !== meId) return false;
  if (f.assignee === "none" && t.assignee) return false;
  if (f.assignee && f.assignee !== "me" && f.assignee !== "none" && t.assignee?.id !== f.assignee) return false;
  if (f.team_id && !teamMatches(ws, f.team_id, t, meId)) return false;
  if (f.mailbox_id && t.mailbox_id !== f.mailbox_id) return false;
  if (f.received_at && t.received_at.toLowerCase() !== f.received_at.toLowerCase()) return false;
  if (f.brand_id && t.brand_id !== f.brand_id) return false;
  if (f.widget_id && t.widget_id !== f.widget_id) return false;
  if (f.channel && t.channel !== f.channel) return false;
  if (f.contact_id && t.contact?.id !== f.contact_id) return false;
  if (f.tag && !t.tags.some((x) => x.name.toLowerCase() === f.tag!.toLowerCase())) return false;
  if (!matchFields(ws, "ticket", t.fields, [...(f.fields ?? []), ...(f.fieldFilters ?? [])])) return false;
  if (f.q) {
    const q = f.q.toLowerCase().trim().replace(/^#/, "");
    const hay = [t.subject, t.last_message_preview, t.contact?.name ?? "", t.contact?.email ?? "", String(t.number), fieldsText(t.fields)].join(" ").toLowerCase();
    if (!hay.includes(q)) return false;
  }
  return true;
}

/** Awaiting a reply (the customer wrote last), urgent, and overdue (past the due time that applies, or breached). */
export function ticketStats(list: Ticket[], now = Date.now()): TicketStats {
  let awaiting = 0,
    urgent = 0,
    overdue = 0;
  for (const t of list) {
    if ((t.status === "open" || t.status === "pending") && t.last_customer_message_at && Date.parse(t.last_customer_message_at) >= Date.parse(t.last_message_at) - 1000) awaiting++;
    if (t.priority === "urgent") urgent++;
    if (t.status !== "solved" && t.status !== "closed" && t.sla) {
      const due = t.first_responded_at ? t.sla.resolution_due_at : t.sla.first_response_due_at;
      if (t.sla.breached || (due && Date.parse(due) < now)) overdue++;
    }
  }
  return { awaiting, urgent, overdue };
}

export function counts(ws: WsData, meId: string): TicketCounts {
  const live = ws.tickets.filter((t) => !ws.deletedTickets.has(t.id));
  const ns = live.filter((t) => !t.spam);
  const views: Record<string, number> = {};
  for (const v of ws.views) views[v.id] = live.filter((t) => matches(ws, t, { view_id: v.id }, meId)).length;
  // Every team the caller can see; private teams' tickets still count in the totals. Like the
  // server, a team counts its open and pending tickets, and the unassigned ones among them.
  const teamCount = (list: Ticket[]) => {
    const active = list.filter((t) => t.status === "open" || t.status === "pending");
    return { open: active.length, unassigned: active.filter((t) => !t.assignee).length };
  };
  const teams: Record<string, { open: number; unassigned: number }> = {};
  for (const team of ws.teams) {
    if (team.private && !teamRole(team, meId)) continue;
    teams[team.id] = teamCount(ns.filter((t) => t.team_id === team.id));
  }
  const mine = new Set(myTeamIds(ws, meId));
  const myTeams = ns.filter((t) => !!t.team_id && mine.has(t.team_id) && (t.status === "open" || t.status === "pending"));
  // Each sidebar row's stats, over the same tickets as its count (contracts/api.md).
  const stats: Record<string, TicketStats> = {
    "ticket_folder:unassigned": ticketStats(ns.filter((t) => OPENISH.includes(t.status) && !t.assignee)),
    "ticket_folder:mine": ticketStats(ns.filter((t) => OPENISH.includes(t.status) && t.assignee?.id === meId)),
    "ticket_folder:open": ticketStats(ns.filter((t) => t.status === "open")),
    "ticket_folder:pending": ticketStats(ns.filter((t) => t.status === "pending" || t.status === "on_hold")),
    "team:mine": ticketStats(myTeams),
  };
  for (const id of Object.keys(teams)) stats[`team:${id}`] = ticketStats(ns.filter((t) => t.team_id === id && (t.status === "open" || t.status === "pending")));
  for (const v of ws.views) stats[`view:${v.id}`] = ticketStats(live.filter((t) => matches(ws, t, { view_id: v.id }, meId)));
  return {
    teams,
    stats,
    my_teams: teamCount(ns.filter((t) => !!t.team_id && mine.has(t.team_id))),
    unassigned: ns.filter((t) => OPENISH.includes(t.status) && !t.assignee).length,
    mine: ns.filter((t) => OPENISH.includes(t.status) && t.assignee?.id === meId).length,
    open: ns.filter((t) => t.status === "open").length,
    pending: ns.filter((t) => t.status === "pending").length,
    on_hold: ns.filter((t) => t.status === "on_hold").length,
    solved: ns.filter((t) => t.status === "solved").length,
    spam: live.filter((t) => t.spam).length,
    views,
  };
}

const priorityRank: Record<Priority, number> = { low: 0, normal: 1, high: 2, urgent: 3 };
const severityRank = (s: Ticket["severity"]) => (s ? { sev4: 1, sev3: 2, sev2: 3, sev1: 4 }[s] : 0);

function dueOf(t: Ticket): number {
  const d = t.sla?.first_response_due_at && !t.first_responded_at ? t.sla.first_response_due_at : t.sla?.resolution_due_at;
  return d ? Date.parse(d) : Number.POSITIVE_INFINITY;
}

export { fieldFiltersFromQuery };

export function sortTickets(list: Ticket[], sort: string, order: string | null, ws?: WsData): Ticket[] {
  const dir = (order ?? (sort === "due" ? "asc" : "desc")) === "asc" ? 1 : -1;
  const field = sort.startsWith("field.") && ws ? fieldDefs(ws, "ticket").find((f) => `field.${f.key}` === sort) : undefined;
  return list.slice().sort((a, b) => {
    let d: number;
    if (field) {
      const x = fieldSortValue(field, a.fields[field.key]);
      const y = fieldSortValue(field, b.fields[field.key]);
      // Records without a value sort lowest.
      if (x === null || y === null) d = x === y ? 0 : x === null ? -1 : 1;
      else d = typeof x === "number" && typeof y === "number" ? x - y : String(x).localeCompare(String(y));
    } else if (sort === "priority") d = priorityRank[a.priority] - priorityRank[b.priority];
    else if (sort === "severity") d = severityRank(a.severity) - severityRank(b.severity);
    else if (sort === "due") d = dueOf(a) - dueOf(b);
    else if (sort === "created_at") d = Date.parse(a.created_at) - Date.parse(b.created_at);
    else d = Date.parse(a.last_message_at) - Date.parse(b.last_message_at);
    if (d === 0) d = a.number - b.number;
    return d * dir;
  });
}

export function findTicket(ws: WsData, idOrNumber: string): Ticket | undefined {
  const raw = decodeURIComponent(idOrNumber).replace(/^#/, "");
  const t = /^\d+$/.test(raw) ? ws.tickets.find((x) => x.number === Number(raw)) : ws.tickets.find((x) => x.id === raw);
  return t && !ws.deletedTickets.has(t.id) ? t : undefined;
}

/** Labels by name, created when missing (seed and lenient writes). */
export function ensureTags(ws: WsData, names: string[], publishEvents = true): TagRef[] {
  return applyLabels(ws, "tickets", [], { replace: names }, { create: true, lenient: true, publishEvents }).next;
}

export function addActivity(
  ws: WsData,
  t: Ticket,
  type: string,
  data: Record<string, unknown>,
  actor: UserRef | null,
  at = nowIso(),
  publishEvent = true,
): Activity {
  const a: Activity = { id: uuid(), ticket_id: t.id, actor: { type: actor ? "user" : "system", user: actor }, type, data, created_at: at };
  ws.activities.push(a);
  if (publishEvent) publish(ws, "activity.created", { ticket_id: t.id, activity: a });
  return a;
}

/** Topics for a ticket.updated event: the assignee hears about assignment changes. */
function ticketTopics(t: Ticket, changes: string[]): string[] {
  return changes.includes("assignee_id") && t.assignee ? [`user:${t.assignee.id}`] : [];
}

export function publishTicketUpdated(ws: WsData, t: Ticket, changes: string[]) {
  t.updated_at = nowIso();
  publish(ws, "ticket.updated", { ticket: t, changes }, { topics: ticketTopics(t, changes) });
}

export type PatchResult = { changes: string[] };

export type PatchOpts = {
  /** How a team change came about (team_changed `via`); a person's edit by default. */
  via?: TicketRouted["via"];
  /** People and API keys: solving with an empty required field is refused. */
  requireFields?: boolean;
  /** May name labels that don't exist yet (they're created). */
  createLabels?: boolean;
  /** Automations and AI: skip unknown labels and bad values instead of failing. */
  lenient?: boolean;
};

export function applyPatch(ws: WsData, t: Ticket, patch: TicketPatch, actor: UserRef | null, publishEvents = true, opts: PatchOpts = {}): PatchResult {
  const changes: string[] = [];
  const act = (type: string, data: Record<string, unknown>) => addActivity(ws, t, type, data, actor, nowIso(), publishEvents);
  // Validate before writing anything, so a refused patch changes nothing.
  const merged = patch.fields ? mergeFields(ws, "ticket", t.fields, patch.fields) : null;
  const labels =
    patch.tags || patch.add_tags?.length || patch.remove_tags?.length
      ? applyLabels(ws, "tickets", t.tags, { replace: patch.tags, add: patch.add_tags, remove: patch.remove_tags }, { create: opts.createLabels ?? true, lenient: opts.lenient, publishEvents })
      : null;
  const closing = (patch.status === "solved" || patch.status === "closed") && t.status !== "solved" && t.status !== "closed";
  if (closing && opts.requireFields) checkRequiredOnSolve(ws, merged?.next ?? t.fields);

  if (patch.subject !== undefined && patch.subject !== t.subject) {
    t.subject = patch.subject;
    changes.push("subject");
  }
  if (patch.status && patch.status !== t.status) {
    const from = t.status;
    t.status = patch.status;
    if (patch.status === "solved" || patch.status === "closed") t.solved_at = t.solved_at ?? nowIso();
    else t.solved_at = null;
    if (patch.status !== "pending" && patch.status !== "on_hold") t.snoozed_until = null;
    changes.push("status");
    act("status_changed", { from, to: patch.status });
  }
  if (patch.priority && patch.priority !== t.priority) {
    const from = t.priority;
    t.priority = patch.priority;
    changes.push("priority");
    act("priority_changed", { from, to: patch.priority });
  }
  if (patch.severity !== undefined && (patch.severity ?? null) !== t.severity) {
    const from = t.severity;
    t.severity = patch.severity ?? null;
    changes.push("severity");
    act("severity_changed", { from, to: t.severity });
  }
  if (patch.contact_id !== undefined && patch.contact_id !== t.contact?.id) {
    const contact = ws.contacts.find((c) => c.id === patch.contact_id)!;
    const from = t.contact;
    t.contact = contactRef(contact);
    changes.push("contact_id");
    act("contact_changed", { from, to: t.contact });
  }
  if (patch.assignee_id !== undefined && (patch.assignee_id ?? null) !== (t.assignee?.id ?? null)) {
    const from = t.assignee;
    t.assignee = userRef(patch.assignee_id);
    changes.push("assignee_id");
    act("assigned", { from, to: t.assignee });
    if (t.assignee && actor?.id !== t.assignee.id && t.assignee.id === db.meId) {
      notify(ws, "assigned", t, { actor });
    }
  }
  if (patch.brand_id !== undefined && patch.brand_id !== t.brand_id && ws.brands.some((b) => b.id === patch.brand_id)) {
    t.brand_id = patch.brand_id;
    changes.push("brand_id");
    // Replies go from an address of the new brand: its address for the team, else its default.
    const cur = ws.mailboxes.find((m) => m.id === t.mailbox_id);
    if (cur && cur.brand_id !== t.brand_id) {
      const brand = ws.brands.find((b) => b.id === t.brand_id);
      const next = teamAddress(ws, t.brand_id, t.team_id) ?? ws.mailboxes.find((m) => m.id === brand?.default_mailbox_id) ?? ws.mailboxes.find((m) => m.brand_id === t.brand_id);
      t.mailbox_id = next?.id ?? null;
      changes.push("mailbox_id");
    }
  }
  if (patch.team_id !== undefined && (patch.team_id ?? null) !== t.team_id) {
    const from = t.team_id;
    const before = ws.mailboxes.find((m) => m.id === t.mailbox_id);
    const box = patch.mailbox_id === undefined ? addressAfterMove(ws, t, patch.team_id ?? null) : null;
    t.team_id = patch.team_id ?? null;
    changes.push("team_id");
    const via = opts.via ?? (actor ? "agent" : "automation");
    if (box) {
      t.mailbox_id = box.id;
      changes.push("mailbox_id");
    }
    t.routed = via === "agent" && actor ? { via, by: actor } : { via };
    act("team_changed", { from, to: t.team_id, via, mailbox_from: box ? (before?.id ?? null) : null, mailbox_to: box?.id ?? null });
  }
  if (patch.mailbox_id !== undefined && patch.mailbox_id !== t.mailbox_id) {
    t.mailbox_id = patch.mailbox_id;
    changes.push("mailbox_id");
  }
  if (patch.snoozed_until !== undefined && patch.snoozed_until !== t.snoozed_until) {
    t.snoozed_until = patch.snoozed_until;
    changes.push("snoozed_until");
    if (t.snoozed_until && t.status === "open") {
      t.status = "pending";
      changes.push("status");
      act("status_changed", { from: "open", to: "pending" });
    }
  }
  if (patch.spam !== undefined && patch.spam !== t.spam) {
    t.spam = patch.spam;
    changes.push("spam");
  }
  if (labels && (labels.added.length || labels.removed.length)) {
    t.tags = labels.next;
    changes.push("tags");
    for (const a of labels.added) act("tagged", { tag: a });
    for (const r of labels.removed) act("untagged", { tag: r });
  }
  if (merged?.changes.length) {
    t.fields = merged.next;
    changes.push("fields");
    for (const c of merged.changes) act("field_changed", { key: c.key, name: c.name, from: c.from, to: c.to });
  }
  if (patch.custom) {
    t.custom = { ...t.custom, ...patch.custom };
    changes.push("custom");
  }
  // Setting a field drops Ollie's pending suggestion for it (contracts/api.md).
  const sugg = t.custom.ai_suggestions as Record<string, unknown> | undefined;
  if (sugg && typeof sugg === "object") {
    const map: Record<string, string> = { priority: "priority", severity: "severity", team_id: "team", spam: "spam", tags: "labels" };
    const drop = changes.map((c) => map[c]).filter(Boolean);
    if (drop.some((k) => k in sugg)) {
      t.custom = { ...t.custom, ai_suggestions: Object.fromEntries(Object.entries(sugg).filter(([k]) => !drop.includes(k))) };
      if (!changes.includes("custom")) changes.push("custom");
    }
  }
  if (changes.some((c) => c === "priority" || c === "status" || c === "team_id")) t.sla = computeSla(ws, t, false);
  if (changes.length && publishEvents) publishTicketUpdated(ws, t, changes);
  return { changes };
}

export function notify(
  ws: WsData,
  type: "assigned" | "new_ticket" | "mentioned" | "customer_replied" | "sla_breached",
  t: Ticket,
  data: Record<string, unknown> = {},
) {
  const n = {
    id: uuid(),
    type,
    ticket: { id: t.id, number: t.number, subject: t.subject },
    data,
    read_at: null,
    created_at: nowIso(),
  };
  ws.notifications.unshift(n);
  publish(ws, "notification.created", { notification: n }, { topics: [`user:${db.meId}`] });
}

export type NewMessage = {
  kind: "reply" | "note";
  direction: "inbound" | "outbound";
  html: string;
  user?: UserRef | null;
  attachments?: Attachment[];
  cc?: string[];
  bcc?: string[];
  at?: string;
  has_quoted?: boolean;
  delivery?: Message["delivery_status"];
  delivery_error?: string;
  /** A widget agent reply under this bot name (contracts/agent.md "Output and posting"). */
  bot?: string;
  /** The bot an AI reply is sent as; else the one named `bot`, else the default. */
  bot_id?: string;
  ai_action?: Message["ai_action"];
  ai_rating?: Message["ai_rating"];
  /** Inbound email: the Reply-To it named when that differs from From. */
  reply_to?: string;
  /** Inbound email: its From failed DMARC; it does not reopen the ticket. */
  auth_failed?: boolean;
  /** Inbound email: the trusted contact form it came through (the contact is the visitor). */
  form_sender?: string;
};

export function buildMessage(ws: WsData, t: Ticket, m: NewMessage): Message {
  const contact = t.contact;
  const mailbox = ws.mailboxes.find((x) => x.id === t.mailbox_id);
  const brand = ws.brands.find((b) => b.id === t.brand_id)?.name ?? ws.workspace.name;
  const inbound = m.direction === "inbound";
  const at = m.at ?? nowIso();
  const isEmail = t.channel === "email";
  // Only an email reply goes out from an address; a note or a chat message has none.
  const sentFrom = m.kind === "reply" && isEmail ? mailbox : undefined;
  // An AI reply is sent as the ticket's bot (its widget's or mailbox's, contracts/bots.md).
  const aiBot = m.bot ? botRefFor(ws, m.bot_id ?? botForTicket(ws, t).id, "") : null;
  const botLabel = aiBot?.name ?? m.bot;
  const msg: Message = {
    id: uuid(),
    ticket_id: t.id,
    ai: !!m.bot,
    ai_action: m.ai_action ?? null,
    ai_rating: m.ai_rating ?? null,
    kind: m.kind,
    direction: m.direction,
    author: inbound
      ? { type: "contact", user: null, contact }
      : m.bot
        ? { type: "system", user: null, contact: null, bot: aiBot }
        : { type: "user", user: m.user ?? null, contact: null },
    body_html: m.html,
    body_text: stripHtml(m.html),
    has_quoted: m.has_quoted ?? false,
    subject: inbound ? t.subject : `Re: ${t.subject}`,
    from_email: inbound ? (contact?.email ?? "") : sentFrom ? sendAs(t, sentFrom) : "",
    from_name: inbound ? (contact?.name ?? "") : sentFrom ? fromName(ws, t, sentFrom, botLabel ?? m.user?.name ?? null) : (botLabel ?? m.user?.name ?? brand),
    // Inbound: the address it was written to (an alias or the mailbox's own).
    to: inbound ? (isEmail && (t.received_at || mailbox) ? [t.received_at || mailbox!.email] : []) : contact?.email ? [contact.email] : [],
    // Inbound: where it arrived (email only); outbound: the address it went from.
    mailbox_id: inbound ? (isEmail ? (t.origin_mailbox_id ?? t.mailbox_id) : null) : (sentFrom?.id ?? null),
    cc: m.cc ?? [],
    bcc: m.bcc ?? [],
    attachments: m.attachments ?? [],
    delivery_status: m.kind === "note" || inbound || !isEmail ? "none" : (m.delivery ?? "sent"),
    delivery_error: m.delivery_error ?? "",
    reply_to: inbound && isEmail ? (m.reply_to ?? "") : "",
    received_at: inbound && isEmail ? (t.received_at || mailbox?.email || "").toLowerCase() : "",
    auth_failed: inbound && isEmail && !!m.auth_failed,
    form_sender: inbound && isEmail ? (m.form_sender ?? "") : "",
    created_at: at,
    edited_at: null,
  };
  if (msg.delivery_status === "failed" && !msg.delivery_error) msg.delivery_error = "550 5.1.1 Recipient address rejected";
  return msg;
}

/** Store a message, update the ticket's derived fields, publish events. */
export function addMessage(ws: WsData, t: Ticket, m: NewMessage, opts: { publish?: boolean; status?: TicketStatus; actor?: UserRef | null } = {}): Message {
  const doPublish = opts.publish ?? true;
  const msg = buildMessage(ws, t, m);
  ws.messages.push(msg);
  if (m.has_quoted) {
    ws.originals[msg.id] =
      `${msg.body_html}<blockquote><p>On ${new Date(Date.parse(msg.created_at) - 3 * 3600_000).toUTCString()}, ${ws.brands.find((b) => b.id === t.brand_id)?.name ?? ws.workspace.name} Support wrote:</p><p>Thanks for the detailed report. Could you check whether anything changed on your side recently?</p><blockquote><p>Earlier messages in this thread…</p></blockquote></blockquote>`;
  }
  const changes: string[] = [];
  if (m.kind === "reply") {
    t.last_message_at = msg.created_at;
    t.last_message_preview = preview(m.html);
    changes.push("last_message_at");
    if (m.direction === "inbound") {
      t.last_customer_message_at = msg.created_at;
      t.waiting_since ??= msg.created_at;
      t.unanswered_count = (t.unanswered_count ?? 0) + 1;
      if (t.status !== "open" && !t.spam && msg.auth_failed) {
        // A reply that failed sender checks leaves the status where it was (contracts/api.md "Inbound email").
        const data = { from: t.status, to: t.status, reason: "sender_unverified", message_id: msg.id };
        if (doPublish) addActivity(ws, t, "status_changed", data, null);
        else addActivity(ws, t, "status_changed", data, null, msg.created_at, false);
      } else if (t.status !== "open" && !t.spam) {
        const from = t.status;
        t.status = "open";
        t.solved_at = null;
        changes.push("status");
        if (doPublish) addActivity(ws, t, "status_changed", { from, to: "open" }, null);
        else addActivity(ws, t, "status_changed", { from, to: "open" }, null, msg.created_at, false);
      }
    } else {
      t.waiting_since = null;
      t.unanswered_count = 0;
      if (!t.first_responded_at && !m.bot) {
        t.first_responded_at = msg.created_at;
        changes.push("first_responded_at");
      }
    }
  }
  t.message_count += 1;
  if (opts.status && opts.status !== t.status) {
    const r = applyPatch(ws, t, { status: opts.status }, opts.actor ?? null, doPublish);
    changes.push(...r.changes.filter((c) => !changes.includes(c)));
  }
  t.sla = computeSla(ws, t);
  if (doPublish) {
    const visibility = t.channel === "chat" && m.kind === "reply" ? "public" : "agents";
    const topics = visibility === "public" && t.contact ? [`contact:${ws.workspace.id}:${t.contact.id}`] : [];
    publish(ws, "message.created", { ticket_id: t.id, message: msg }, { visibility, topics });
    publishTicketUpdated(ws, t, changes);
  }
  return msg;
}

export function newTicket(
  ws: WsData,
  init: {
    subject: string;
    contact: Ticket["contact"];
    channel: Channel;
    priority?: Priority;
    severity?: Ticket["severity"];
    status?: TicketStatus;
    mailbox_id?: string | null;
    assignee?: UserRef | null;
    team_id?: string | null;
    tags?: TagRef[];
    fields?: Ticket["fields"];
    created_at?: string;
    brand_id?: string;
    widget_id?: string | null;
    /** The exact address written to; the mailbox's own address by default (email). */
    received_at?: string;
    page_url?: string;
    routed?: TicketRouted | null;
  },
): Ticket {
  const number = Math.max(1000, ...ws.tickets.map((x) => x.number)) + 1;
  const at = init.created_at ?? nowIso();
  // A chat gets no address until it needs one (contracts/teams.md "Reply address").
  const mailboxId = init.mailbox_id !== undefined ? init.mailbox_id : init.channel === "chat" ? null : (ws.mailboxes[0]?.id ?? null);
  const mailbox = ws.mailboxes.find((m) => m.id === mailboxId);
  const email = init.channel === "email";
  const t: Ticket = {
    id: uuid(),
    number,
    subject: init.subject,
    status: init.status ?? "open",
    priority: init.priority ?? "normal",
    severity: init.severity ?? null,
    channel: init.channel,
    mailbox_id: mailboxId,
    contact: init.contact,
    assignee: init.assignee ?? null,
    team_id: init.team_id ?? null,
    brand_id: init.brand_id ?? mailbox?.brand_id ?? defaultBrand(ws)?.id ?? "",
    widget_id: init.widget_id ?? null,
    origin_mailbox_id: email && mailbox ? mailbox.id : null,
    received_at: init.received_at ?? (email && mailbox ? mailbox.email : ""),
    page_url: init.page_url ?? "",
    routed: init.routed ?? null,
    tags: init.tags ?? [],
    sla: null,
    first_responded_at: null,
    solved_at: null,
    snoozed_until: null,
    last_message_at: at,
    last_customer_message_at: null,
    waiting_since: null,
    unanswered_count: 0,
    last_message_preview: "",
    message_count: 0,
    spam: false,
    csat: null,
    custom: {},
    fields: init.fields ?? {},
    created_at: at,
    updated_at: at,
  };
  t.sla = computeSla(ws, t, false);
  ws.tickets.push(t);
  return t;
}
