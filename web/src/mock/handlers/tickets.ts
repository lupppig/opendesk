// Tickets, messages, activities, attachments, search and reports.

import type { Attachment, Channel, Message, Priority, ReportsOverview, Severity, SuggestionField, Thread, Ticket, TicketPatch, TicketStatus } from "@/lib/api/types";
import { contactRef, db, me, userRef, type WsData } from "../db";
import { publish } from "../gateway";
import { HttpError, del, forbidden, get, invalid, notFound, paginate, patch, post, required, respond, s, strArr, type Ctx } from "../router";
import { canCreateLabels, checkRequiredOnSolve, mergeFields } from "../labels";
import { canSeeTicket, defaultBrand, teamRole } from "../teams";
import { addActivity, addMessage, applyPatch, buildMessage, canSendFrom, counts, fieldFiltersFromQuery, findTicket, matches, newTicket, notify, publishTicketUpdated, sortTickets, type ListFilters } from "../tickets";
import { DAY, isRecord, nowIso, uuid } from "../util";
import { clearDraft } from "./drafts";
import { cannotSend, sendingOf } from "./mailhost";

const STATUSES: TicketStatus[] = ["open", "pending", "on_hold", "solved", "closed"];
const PRIORITIES: Priority[] = ["low", "normal", "high", "urgent"];
const SEVERITIES: Severity[] = ["sev1", "sev2", "sev3", "sev4"];
const SUGGESTION_FIELDS: SuggestionField[] = ["priority", "severity", "labels", "team", "spam"];

function csv<T extends string>(v: string | null, allowed: T[]): T[] | undefined {
  if (!v) return undefined;
  const out = v.split(",").map((x) => x.trim()).filter((x): x is T => (allowed as string[]).includes(x));
  return out.length ? out : undefined;
}

function filtersFrom(ctx: Ctx): ListFilters {
  const q = ctx.query;
  return {
    status: csv(q.get("status"), STATUSES),
    priority: csv(q.get("priority"), PRIORITIES),
    severity: csv(q.get("severity"), [...SEVERITIES, "none" as const]),
    assignee: q.get("assignee") ?? undefined,
    team_id: q.get("team_id") ?? undefined,
    mailbox_id: q.get("mailbox_id") ?? undefined,
    received_at: q.get("received_at")?.trim().toLowerCase() || undefined,
    brand_id: q.get("brand_id") ?? undefined,
    widget_id: q.get("widget_id") ?? undefined,
    tag: q.get("tag") ?? undefined,
    channel: (q.get("channel") as Channel | null) ?? undefined,
    contact_id: q.get("contact_id") ?? undefined,
    view_id: q.get("view_id") ?? undefined,
    q: q.get("q") ?? undefined,
    spam: q.get("spam") === "true",
    fieldFilters: fieldFiltersFromQuery(ctx.ws, "ticket", q),
  };
}

/** How a person's write treats labels and required fields. */
function personOpts(ws: WsData) {
  return { requireFields: true, createLabels: canCreateLabels(ws) };
}

function ticket(ctx: Ctx): Ticket {
  const t = findTicket(ctx.ws, ctx.params.id);
  // A private team's ticket doesn't exist for anyone outside it (contracts/teams.md "Visibility").
  if (!t || !canSeeTicket(ctx.ws, t)) throw notFound("Ticket");
  return t;
}

/** Team, brand and reply address checks for a patch (contracts/teams.md). */
function checkPatch(ws: WsData, t: Ticket, p: TicketPatch) {
  if (p.contact_id && !ws.contacts.some((x) => x.id === p.contact_id)) throw invalid("Unknown contact");
  if (p.contact_id && p.contact_id !== t.contact?.id && t.channel === "chat") throw invalid("A live chat stays with the visitor who started it");
  if (p.team_id && !ws.teams.some((x) => x.id === p.team_id)) throw invalid("Unknown team");
  if (p.brand_id !== undefined && !ws.brands.some((b) => b.id === p.brand_id)) throw invalid("Unknown brand");
  if (p.mailbox_id) {
    const m = ws.mailboxes.find((x) => x.id === p.mailbox_id);
    if (!m) throw invalid("Unknown address");
    if (m.brand_id !== (p.brand_id ?? t.brand_id)) throw invalid("That address belongs to another brand");
    if (m.status === "disabled" || !canSendFrom(ws, m, db.meId)) throw invalid("You can't send from that address");
  }
  const teamId = p.team_id !== undefined ? p.team_id : t.team_id;
  const team = teamId ? ws.teams.find((x) => x.id === teamId) : undefined;
  if (team?.private && p.assignee_id && !teamRole(team, p.assignee_id)) throw new HttpError(422, "not_on_team", "Only members of this team can be assigned its tickets");
}

/** Moving a ticket into a private team tells everyone else to drop it. */
function hideIfPrivate(ws: WsData, t: Ticket, fromTeam: string | null) {
  if (t.team_id === fromTeam || !t.team_id) return;
  if (ws.teams.find((x) => x.id === t.team_id)?.private) publish(ws, "ticket.hidden", { ticket_id: t.id, team_id: t.team_id });
}

function readPatch(body: Record<string, unknown>): TicketPatch {
  const p: TicketPatch = {};
  if ("contact_id" in body) {
    if (typeof body.contact_id !== "string" || !body.contact_id.trim()) throw invalid("contact_id must be a contact id");
    p.contact_id = body.contact_id;
  }
  if (typeof body.subject === "string") {
    if (!body.subject.trim()) throw invalid("Subject can't be empty");
    p.subject = body.subject.trim().slice(0, 250);
  }
  if (typeof body.status === "string") {
    if (!STATUSES.includes(body.status as TicketStatus)) throw invalid("Unknown status");
    p.status = body.status as TicketStatus;
  }
  if (typeof body.priority === "string") {
    if (!PRIORITIES.includes(body.priority as Priority)) throw invalid("Unknown priority");
    p.priority = body.priority as Priority;
  }
  if ("severity" in body) {
    const v = body.severity;
    if (v !== null && !SEVERITIES.includes(v as Severity)) throw invalid("severity must be one of sev1, sev2, sev3, sev4, or null");
    p.severity = (v as Severity | null) ?? null;
  }
  if ("assignee_id" in body) {
    const v = body.assignee_id;
    if (v !== null && typeof v !== "string") throw invalid("assignee_id must be a user id or null");
    if (v && !db.users.some((u) => u.id === v)) throw invalid("Unknown assignee");
    p.assignee_id = v;
  }
  if ("team_id" in body) p.team_id = typeof body.team_id === "string" ? body.team_id : null;
  if (typeof body.mailbox_id === "string") p.mailbox_id = body.mailbox_id;
  if (typeof body.brand_id === "string") p.brand_id = body.brand_id;
  if ("snoozed_until" in body) p.snoozed_until = typeof body.snoozed_until === "string" ? body.snoozed_until : null;
  if (typeof body.spam === "boolean") p.spam = body.spam;
  const tags = strArr(body.tags);
  if (tags) p.tags = tags;
  const add = strArr(body.add_tags);
  if (add) p.add_tags = add;
  const remove = strArr(body.remove_tags);
  if (remove) p.remove_tags = remove;
  if (isRecord(body.custom)) p.custom = body.custom;
  if (isRecord(body.fields)) p.fields = body.fields as TicketPatch["fields"];
  return p;
}

function attachmentsFor(ids: string[] | undefined): Attachment[] {
  return (ids ?? []).map((id) => db.attachments.find((a) => a.id === id)).filter((a): a is Attachment => !!a);
}

/** Outbound email: pending, then sent (or failed for addresses containing "bounce"). */
function simulateDelivery(ws: WsData, t: Ticket, msgId: string) {
  setTimeout(() => {
    const m = ws.messages.find((x) => x.id === msgId);
    if (!m || m.delivery_status !== "pending") return;
    const fail = (t.contact?.email ?? "").includes("bounce");
    m.delivery_status = fail ? "failed" : "sent";
    m.delivery_error = fail ? "550 5.1.1 Recipient address rejected" : "";
    publish(ws, "message.updated", { ticket_id: t.id, message: m });
  }, 1500);
}

/** A message of a third-party thread: a note emailed to the thread's recipients (contracts/api.md "Threads"). */
function threadMessage(ws: WsData, t: Ticket, th: Thread, html: string, attachmentIds: string[]): Message {
  const me = userRef(db.meId);
  const mb = ws.mailboxes.find((m) => m.id === th.mailbox_id);
  const msg: Message = {
    ...buildMessage(ws, t, { kind: "note", direction: "outbound", html, user: me, attachments: attachmentsFor(attachmentIds), delivery: "pending" }),
    thread_id: th.id,
    delivery_status: "pending",
    subject: th.subject,
    to: th.to,
    cc: th.cc,
    mailbox_id: mb?.id ?? null,
    from_email: mb?.email ?? "",
  };
  ws.messages.push(msg);
  th.status = "open";
  th.message_count += 1;
  th.last_message_at = th.updated_at = msg.created_at;
  publish(ws, "message.created", { ticket_id: t.id, message: msg });
  publish(ws, "thread.updated", { ticket_id: t.id, thread: th });
  simulateDelivery(ws, t, msg.id);
  return msg;
}

function threadOf(ctx: Ctx, t: Ticket): Thread {
  const th = ctx.ws.threads.find((x) => x.id === ctx.params.threadId && x.ticket_id === t.id);
  if (!th) throw notFound("Thread");
  return th;
}

export function registerTickets() {
  get("/v1/tickets/:id/threads", (ctx) => {
    const t = ticket(ctx);
    return { data: ctx.ws.threads.filter((x) => x.ticket_id === t.id) };
  });
  post("/v1/tickets/:id/threads", (ctx) => {
    const t = ticket(ctx);
    const to = strArr(ctx.body.to) ?? [];
    if (!to.length) throw invalid("a thread needs at least one recipient");
    const subject = required(ctx, "subject").trim();
    const html = required(ctx, "body_html");
    const mailbox = ctx.ws.mailboxes.find((m) => m.id === (s(ctx, "mailbox_id") ?? t.mailbox_id)) ?? ctx.ws.mailboxes.find((m) => m.brand_id === t.brand_id);
    const now = nowIso();
    const th: Thread = {
      id: uuid(),
      ticket_id: t.id,
      subject,
      to,
      cc: strArr(ctx.body.cc) ?? [],
      mailbox_id: mailbox?.id ?? null,
      status: "open",
      include_history: ctx.body.include_history === true,
      created_by: userRef(db.meId),
      message_count: 0,
      last_message_at: now,
      created_at: now,
      updated_at: now,
    };
    ctx.ws.threads.push(th);
    publish(ctx.ws, "thread.created", { ticket_id: t.id, thread: th });
    addActivity(ctx.ws, t, "thread_started", { thread_id: th.id, subject, to }, userRef(db.meId));
    // Files of the customer's conversation go along as copies (contracts/api.md "Threads").
    const forward = strArr(ctx.body.forward_attachment_ids) ?? [];
    const copies = forward.map((id) => {
      // Not the files of mail that failed its sender checks.
      const src = ctx.ws.messages
        .find((m) => m.ticket_id === t.id && m.kind === "reply" && !(m.direction === "inbound" && m.auth_failed) && m.attachments.some((a) => a.id === id))
        ?.attachments.find((a) => a.id === id);
      if (!src) throw invalid("forward_attachment_ids must be attachments of this ticket's conversation with the customer");
      const copy = { ...src, id: uuid(), inline: false };
      db.attachments.push(copy);
      return copy.id;
    });
    const message = threadMessage(ctx.ws, t, th, html, [...(strArr(ctx.body.attachment_ids) ?? []), ...copies]);
    return respond(201, { thread: th, message });
  });
  post("/v1/tickets/:id/threads/:threadId/messages", (ctx) => {
    const t = ticket(ctx);
    const th = threadOf(ctx, t);
    return respond(201, threadMessage(ctx.ws, t, th, required(ctx, "body_html"), strArr(ctx.body.attachment_ids) ?? []));
  });
  patch("/v1/tickets/:id/threads/:threadId", (ctx) => {
    const t = ticket(ctx);
    const th = threadOf(ctx, t);
    const status = s(ctx, "status");
    if (status !== "open" && status !== "closed") throw invalid("status is open or closed");
    if (th.status !== status) {
      th.status = status;
      th.updated_at = nowIso();
      addActivity(ctx.ws, t, status === "closed" ? "thread_closed" : "thread_reopened", { thread_id: th.id, subject: th.subject }, userRef(db.meId));
      publish(ctx.ws, "thread.updated", { ticket_id: t.id, thread: th });
    }
    return th;
  });

  get("/v1/tickets", (ctx) => {
    const f = filtersFrom(ctx);
    const list = ctx.ws.tickets.filter((t) => matches(ctx.ws, t, f, db.meId));
    return paginate(ctx, sortTickets(list, ctx.query.get("sort") ?? "last_message_at", ctx.query.get("order"), ctx.ws));
  });
  get("/v1/tickets/counts", (ctx) => counts(ctx.ws, db.meId));

  post("/v1/tickets", (ctx) => {
    const subject = required(ctx, "subject").slice(0, 250);
    const ws = ctx.ws;
    let contact = s(ctx, "contact_id") ? ws.contacts.find((c) => c.id === s(ctx, "contact_id")) : undefined;
    if (s(ctx, "contact_id") && !contact) throw invalid("Unknown contact");
    if (!contact && isRecord(ctx.body.contact)) {
      const email = typeof ctx.body.contact.email === "string" ? ctx.body.contact.email.trim().toLowerCase() : "";
      const name = typeof ctx.body.contact.name === "string" ? ctx.body.contact.name.trim() : "";
      if (!email) throw invalid("Contact email is required");
      contact = ws.contacts.find((c) => c.email === email);
      if (!contact) {
        contact = { id: uuid(), email, name: name || email.split("@")[0], phone: "", avatar_url: null, avatar_source: null, company: null, external_id: null, notes: "", custom: {}, tags: [], fields: {}, blocked: false, ticket_count: 0, created_at: nowIso(), updated_at: nowIso() };
        ws.contacts.unshift(contact);
        publish(ws, "contact.created", { contact });
      }
    }
    const p = readPatch(ctx.body);
    const fields = p.fields ? mergeFields(ws, "ticket", {}, p.fields).next : {};
    if (p.status === "solved" || p.status === "closed") checkRequiredOnSolve(ws, fields);
    const channel = (s(ctx, "channel") as Channel | undefined) ?? "email";
    const teamId = s(ctx, "team_id") ?? null;
    if (teamId && !ws.teams.some((x) => x.id === teamId)) throw invalid("Unknown team");
    // From: the given address, else the default brand's address for the team, else its default.
    const brand = defaultBrand(ws);
    const fallback =
      ws.mailboxes.find((m) => m.brand_id === brand?.id && !!teamId && m.team_id === teamId && m.status !== "disabled" && canSendFrom(ws, m, db.meId)) ??
      ws.mailboxes.find((m) => m.brand_id === brand?.id && m.status !== "disabled" && canSendFrom(ws, m, db.meId));
    const mailboxId = s(ctx, "mailbox_id") ?? (channel === "chat" ? null : (fallback?.id ?? null));
    const box = mailboxId ? ws.mailboxes.find((m) => m.id === mailboxId) : undefined;
    if (mailboxId && (!box || !canSendFrom(ws, box, db.meId))) throw invalid("You can't send from that address");
    if (box && (s(ctx, "kind") ?? "reply") === "reply" && sendingOf(box) === "system_off") throw cannotSend();
    const t = newTicket(ws, {
      subject,
      contact: contact ? contactRef(contact) : null,
      channel,
      priority: p.priority,
      severity: p.severity ?? null,
      mailbox_id: mailboxId,
      assignee: userRef(s(ctx, "assignee_id")),
      team_id: teamId,
      brand_id: box?.brand_id,
      // Started by an agent: it wasn't received anywhere.
      received_at: "",
      fields,
    });
    t.origin_mailbox_id = null;
    if (p.tags || p.add_tags) applyPatch(ws, t, { tags: p.tags, add_tags: p.add_tags }, userRef(db.meId), false, personOpts(ws));
    if (contact) contact.ticket_count++;
    publish(ws, "ticket.created", { ticket: t });
    const body = s(ctx, "body_html");
    if (body) {
      const kind = s(ctx, "kind") === "note" ? "note" : "reply";
      const msg = addMessage(ws, t, { kind, direction: "outbound", html: body, user: userRef(db.meId), delivery: "pending" }, { status: p.status, actor: userRef(db.meId) });
      if (msg.delivery_status === "pending") simulateDelivery(ws, t, msg.id);
    } else if (p.status && p.status !== t.status) {
      applyPatch(ws, t, { status: p.status }, userRef(db.meId));
    }
    return respond(201, t);
  });

  get("/v1/tickets/:id", (ctx) => ticket(ctx));
  patch("/v1/tickets/:id", (ctx) => {
    const t = ticket(ctx);
    const p = readPatch(ctx.body);
    checkPatch(ctx.ws, t, p);
    const from = t.team_id;
    applyPatch(ctx.ws, t, p, userRef(db.meId), true, personOpts(ctx.ws));
    hideIfPrivate(ctx.ws, t, from);
    return t;
  });
  del("/v1/tickets/:id/ai/suggestions/:field", (ctx) => {
    const t = ticket(ctx);
    const field = ctx.params.field as SuggestionField;
    if (!SUGGESTION_FIELDS.includes(field)) throw notFound("Suggestion");
    const sugg = t.custom.ai_suggestions as Record<string, unknown> | undefined;
    if (sugg && field in sugg) {
      t.custom = { ...t.custom, ai_suggestions: Object.fromEntries(Object.entries(sugg).filter(([k]) => k !== field)) };
      publishTicketUpdated(ctx.ws, t, ["custom"]);
    }
    return respond(204);
  });
  del("/v1/tickets/:id", (ctx) => {
    const t = ticket(ctx);
    ctx.ws.deletedTickets.add(t.id);
    publish(ctx.ws, "ticket.deleted", { ticket_id: t.id });
    return respond(204);
  });
  post("/v1/tickets/bulk", (ctx) => {
    const ids = strArr(ctx.body.ids) ?? [];
    if (!ids.length) throw invalid("ids is required");
    if (ids.length > 100) throw invalid("At most 100 tickets at a time");
    const doDelete = ctx.body.delete === true;
    const p = isRecord(ctx.body.patch) ? readPatch(ctx.body.patch) : null;
    if (!doDelete && !p) throw invalid("patch or delete is required");
    for (const id of ids) {
      const t = findTicket(ctx.ws, id);
      if (!t || !canSeeTicket(ctx.ws, t)) continue;
      if (doDelete) {
        ctx.ws.deletedTickets.add(t.id);
        publish(ctx.ws, "ticket.deleted", { ticket_id: t.id });
      } else if (p) {
        checkPatch(ctx.ws, t, p);
        const from = t.team_id;
        applyPatch(ctx.ws, t, p, userRef(db.meId), true, personOpts(ctx.ws));
        hideIfPrivate(ctx.ws, t, from);
      }
    }
    return respond(204);
  });
  post("/v1/tickets/:id/merge", (ctx) => {
    const src = ticket(ctx);
    const into = findTicket(ctx.ws, required(ctx, "into_id"));
    if (!into || !canSeeTicket(ctx.ws, into)) throw notFound("Target ticket");
    if (into.id === src.id) throw invalid("A ticket can't be merged into itself");
    for (const m of ctx.ws.messages.filter((x) => x.ticket_id === src.id)) {
      ctx.ws.messages.push({ ...m, id: uuid(), ticket_id: into.id });
      into.message_count++;
    }
    if (Date.parse(src.last_message_at) > Date.parse(into.last_message_at)) {
      into.last_message_at = src.last_message_at;
      into.last_message_preview = src.last_message_preview;
    }
    addActivity(ctx.ws, src, "merged", { into: { id: into.id, number: into.number, subject: into.subject } }, userRef(db.meId));
    addActivity(ctx.ws, into, "merged", { from: { id: src.id, number: src.number, subject: src.subject } }, userRef(db.meId));
    applyPatch(ctx.ws, src, { status: "closed" }, userRef(db.meId));
    publishTicketUpdated(ctx.ws, into, ["message_count"]);
    return into;
  });

  // The visitor's widget is not simulated: the mock only answers like the API does.
  post("/v1/tickets/:id/typing", (ctx) => {
    ticket(ctx);
    if ((ctx.ws.members.find((m) => m.user_id === db.meId)?.role ?? "viewer") === "viewer") throw forbidden("Viewers can't change tickets");
    return respond(204);
  });

  get("/v1/tickets/:id/messages", (ctx) => {
    const t = ticket(ctx);
    const role = ctx.ws.members.find((m) => m.user_id === db.meId)?.role;
    const admin = role === "admin" || role === "owner";
    const list = ctx.ws.messages.filter((m) => m.ticket_id === t.id).sort((a, b) => Date.parse(a.created_at) - Date.parse(b.created_at));
    return { data: admin ? list : list.map(tombstone), next_cursor: null };
  });
  post("/v1/tickets/:id/messages", (ctx) => {
    const t = ticket(ctx);
    const kind = s(ctx, "kind") === "note" ? "note" : "reply";
    const html = required(ctx, "body_html");
    const status = s(ctx, "status") as TicketStatus | undefined;
    if (status && !STATUSES.includes(status)) throw invalid("Unknown status");
    if ((status === "solved" || status === "closed") && t.status !== "solved" && t.status !== "closed") checkRequiredOnSolve(ctx.ws, t.fields);
    const myRef = userRef(db.meId);
    // A reply written from the agent's own note: the note stays as a marker pointing at it.
    const fromNote = kind === "reply" ? s(ctx, "from_note_id") : undefined;
    const noteIndex = fromNote ? ctx.ws.messages.findIndex((m) => m.id === fromNote && m.ticket_id === t.id && m.kind === "note" && !m.deleted_at && !m.sent_as_reply_id) : -1;
    if (fromNote && noteIndex < 0) throw notFound("Message");
    if (fromNote && ctx.ws.messages[noteIndex].author.user?.id !== db.meId) throw forbidden();
    // A reply from another address makes it the ticket's reply address.
    const from = kind === "reply" ? s(ctx, "mailbox_id") : undefined;
    const sendsFrom = kind === "reply" && t.channel === "email" ? ctx.ws.mailboxes.find((m) => m.id === (from ?? t.mailbox_id)) : undefined;
    if (sendsFrom && sendingOf(sendsFrom) === "system_off") throw cannotSend();
    if (from && from !== t.mailbox_id) {
      checkPatch(ctx.ws, t, { mailbox_id: from });
      applyPatch(ctx.ws, t, { mailbox_id: from }, myRef);
    }
    const msg = addMessage(
      ctx.ws,
      t,
      { kind, direction: "outbound", html, user: myRef, cc: strArr(ctx.body.cc), bcc: strArr(ctx.body.bcc), attachments: attachmentsFor(strArr(ctx.body.attachment_ids)), delivery: "pending" },
      { status, actor: myRef },
    );
    if (msg.delivery_status === "pending") simulateDelivery(ctx.ws, t, msg.id);
    if (fromNote) {
      const note = ctx.ws.messages[noteIndex];
      // Its words are kept, hidden like a deleted message's: only admins read them.
      Object.assign(note, { sent_as_reply_id: msg.id, deleted_at: nowIso(), deleted_by: myRef });
      addActivity(ctx.ws, t, "note_sent_as_reply", { message_id: fromNote, reply_id: msg.id }, myRef);
      publish(ctx.ws, "message.updated", { ticket_id: t.id, message: tombstone(note) });
    }
    // A reply going out ends the ticket's shared draft, whoever sends it.
    if (kind === "reply") clearDraft(ctx.ws, t);
    return respond(201, msg);
  });
  patch("/v1/tickets/:id/messages/:messageId", (ctx) => {
    const t = ticket(ctx);
    const role = ctx.ws.members.find((m) => m.user_id === db.meId)?.role;
    if (!role || role === "viewer") throw forbidden();
    const message = ctx.ws.messages.find((m) => m.id === ctx.params.messageId && m.ticket_id === t.id);
    if (!message) throw notFound("Message");
    if (message.kind !== "note" || message.thread_id) throw invalid("Only internal notes can be edited");
    if (message.author.user?.id !== db.meId) throw forbidden();
    const html = typeof ctx.body.body_html === "string" ? ctx.body.body_html : null;
    if (html === null) throw invalid("body_html is required");
    message.body_html = html;
    message.body_text = html.replace(/<[^>]*>/g, " ").replace(/\s+/g, " ").trim();
    message.edited_at = nowIso();
    addActivity(ctx.ws, t, "note_edited", { message_id: message.id }, userRef(db.meId));
    publish(ctx.ws, "message.updated", { ticket_id: t.id, message });
    return message;
  });
  // A deleted message stays as a marker; only admins read it (contracts/api.md).
  del("/v1/tickets/:id/messages/:messageId", (ctx) => {
    const t = ticket(ctx);
    const message = ctx.ws.messages.find((m) => m.id === ctx.params.messageId && m.ticket_id === t.id);
    if (!message) throw notFound("Message");
    if (message.sent_as_reply_id) throw invalid("this note was sent as a reply; delete the reply instead");
    if (message.deleted_at) return undefined;
    if (message.thread_id) throw invalid("a side conversation's messages can't be deleted");
    const role = ctx.ws.members.find((m) => m.user_id === db.meId)?.role;
    const admin = role === "admin" || role === "owner";
    const rule = ctx.ws.members.find((m) => m.user_id === db.meId)?.message_delete ?? ctx.ws.workspace.settings.message_delete ?? "own";
    const agentMsg = message.author.type === "user";
    // "own": an agent's own internal notes; "all": any agent's notes and replies.
    const allowed = admin || (agentMsg && (rule === "all" || (rule === "own" && message.kind === "note" && message.author.user?.id === db.meId)));
    if (!allowed) throw forbidden();
    message.deleted_at = nowIso();
    message.deleted_by = userRef(db.meId);
    // Not gone out yet: it is not sent.
    if (message.delivery_status === "pending") Object.assign(message, { delivery_status: "failed", delivery_error: "Deleted before it was sent." });
    addActivity(ctx.ws, t, message.kind === "note" ? "note_deleted" : "message_deleted", { message_id: message.id }, userRef(db.meId));
    publish(ctx.ws, "message.updated", { ticket_id: t.id, message: tombstone(message) });
    if (message.kind === "reply") {
      refreshPreview(ctx.ws.messages, t);
      publish(ctx.ws, "ticket.updated", { ticket: t, changes: ["last_message_preview"] });
    }
    return undefined;
  });
  post("/v1/tickets/:id/messages/:messageId/purge", (ctx) => {
    const t = ticket(ctx);
    const role = ctx.ws.members.find((m) => m.user_id === db.meId)?.role;
    if (role !== "admin" && role !== "owner") throw forbidden();
    const message = ctx.ws.messages.find((m) => m.id === ctx.params.messageId && m.ticket_id === t.id);
    if (!message) throw notFound("Message");
    if (!message.deleted_at) throw invalid("delete the message before removing it for good");
    if (!message.purged_at) {
      Object.assign(message, { body_html: "", body_text: "", has_quoted: false, subject: "", attachments: [], ai_action: null, purged_at: nowIso() });
      delete ctx.ws.originals[message.id];
      addActivity(ctx.ws, t, "message_purged", { message_id: message.id }, userRef(db.meId));
      publish(ctx.ws, "message.updated", { ticket_id: t.id, message: tombstone(message) });
    }
    return message;
  });
  post("/v1/tickets/:id/messages/:messageId/restore", (ctx) => {
    const t = ticket(ctx);
    const role = ctx.ws.members.find((m) => m.user_id === db.meId)?.role;
    if (role !== "admin" && role !== "owner") throw forbidden();
    const message = ctx.ws.messages.find((m) => m.id === ctx.params.messageId && m.ticket_id === t.id);
    if (!message) throw notFound("Message");
    if (message.sent_as_reply_id) throw invalid("this note was sent as a reply; it can't be restored");
    if (!message.deleted_at) throw invalid("this message is not deleted");
    if (message.purged_at) throw invalid("this message's content was removed for good");
    message.deleted_at = null;
    message.deleted_by = null;
    if (message.kind === "reply") {
      refreshPreview(ctx.ws.messages, t);
      publish(ctx.ws, "ticket.updated", { ticket: t, changes: ["last_message_preview"] });
    }
    addActivity(ctx.ws, t, "message_restored", { message_id: message.id }, userRef(db.meId));
    publish(ctx.ws, "message.updated", { ticket_id: t.id, message });
    return message;
  });
  post("/v1/tickets/:id/messages/:messageId/retract", (ctx) => {
    const t = ticket(ctx);
    const message = ctx.ws.messages.find((m) => m.id === ctx.params.messageId && m.ticket_id === t.id);
    if (!message) throw notFound("Message");
    if (message.kind !== "reply" || message.direction !== "outbound" || message.author.type !== "user") throw invalid("only an agent's reply can be taken back");
    if (t.channel !== "chat") throw invalid("only a chat reply can be taken back; email is delivered when it is sent");
    if (message.delivery_status !== "none") throw invalid("this reply was also sent by email; it can't be taken back");
    const role = ctx.ws.members.find((m) => m.user_id === db.meId)?.role;
    if (message.author.user?.id !== db.meId && role !== "admin" && role !== "owner") throw forbidden();
    message.kind = "note";
    refreshPreview(ctx.ws.messages, t);
    addActivity(ctx.ws, t, "reply_retracted", { message_id: message.id }, userRef(db.meId));
    publish(ctx.ws, "ticket.updated", { ticket: t, changes: ["last_message_preview"] });
    publish(ctx.ws, "message.updated", { ticket_id: t.id, message });
    return message;
  });
  get("/v1/tickets/:id/activities", (ctx) => {
    const t = ticket(ctx);
    const list = ctx.ws.activities.filter((a) => a.ticket_id === t.id).sort((a, b) => Date.parse(a.created_at) - Date.parse(b.created_at));
    return { data: list, next_cursor: null };
  });
  get("/v1/messages/:id/original", (ctx) => {
    const m = ctx.ws.messages.find((x) => x.id === ctx.params.id);
    const t = m && findTicket(ctx.ws, m.ticket_id);
    if (!m || !t || !canSeeTicket(ctx.ws, t)) throw notFound("Message");
    return { full_html: ctx.ws.originals[m.id] ?? m.body_html };
  });

  post("/v1/attachments", (ctx) => {
    const file = ctx.form?.get("file");
    if (!(file instanceof File)) throw invalid("file is required");
    if (file.size > 25 * 1024 * 1024) throw new HttpError(413, "too_large", "Files can be up to 25 MB");
    const a: Attachment = { id: uuid(), filename: file.name, content_type: file.type || "application/octet-stream", size_bytes: file.size, inline: false, url: URL.createObjectURL(file) };
    db.attachments.push(a);
    return respond(201, a);
  });
  get("/v1/attachments/:id", (ctx) => {
    const a = db.attachments.find((x) => x.id === ctx.params.id);
    if (!a) throw notFound("Attachment");
    return respond(302, undefined, { location: a.url });
  });

  get("/v1/search", (ctx) => {
    const q = (ctx.query.get("q") ?? "").trim().toLowerCase();
    if (!q) return { tickets: [], contacts: [], articles: [] };
    const ws = ctx.ws;
    const tickets = sortTickets(ws.tickets.filter((t) => matches(ws, t, { q }, db.meId)), "last_message_at", "desc").slice(0, 5);
    const contacts = ws.contacts.filter((c) => [c.name, c.email ?? "", c.company?.name ?? ""].join(" ").toLowerCase().includes(q)).slice(0, 5);
    const articles = ws.articles.filter((a) => (a.title + " " + a.excerpt).toLowerCase().includes(q)).slice(0, 5);
    return { tickets, contacts, articles };
  });

  get("/v1/reports/overview", (ctx) => reports(ctx));
}

function hash(s: string): number {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) h = Math.imul(h ^ s.charCodeAt(i), 16777619);
  return (h >>> 0) / 4294967296;
}

/** A group's stable share of the workspace's volume: weights normalised to 1. */
function shares<K extends string | null>(keys: K[], weight: (k: K) => number): Map<K, number> {
  const w = keys.map((k) => Math.max(0.05, weight(k)));
  const sum = w.reduce((a, b) => a + b, 0) || 1;
  return new Map(keys.map((k, i) => [k, w[i] / sum]));
}

function reports(ctx: Ctx): ReportsOverview {
  const ws = ctx.ws;
  const toRaw = ctx.query.get("to");
  const fromRaw = ctx.query.get("from");
  const to = toRaw ? new Date(toRaw) : new Date();
  const from = fromRaw ? new Date(fromRaw) : new Date(to.getTime() - 29 * DAY);
  if (Number.isNaN(to.getTime()) || Number.isNaN(from.getTime()) || from > to) throw invalid("from must be before to");
  // team_id (a team, or "none") and brand_id narrow every figure. Private teams count too.
  const teamF = (ctx.query.get("team_id") ?? "").trim();
  const brandF = (ctx.query.get("brand_id") ?? "").trim();
  if (teamF && teamF !== "none" && !ws.teams.some((t) => t.id === teamF)) throw invalid("team_id must be a team of the workspace or none");
  if (brandF && !ws.brands.some((b) => b.id === brandF)) throw invalid("brand_id must be a brand of the workspace");
  const teamKeys: (string | null)[] = [...ws.teams.map((t) => t.id), null];
  const teamShare = shares(teamKeys, (k) => (k === null ? 0.55 : 0.35 + hash(k)));
  const brandShare = shares(
    ws.brands.map((b) => b.id),
    (id) => (ws.brands.find((b) => b.id === id)?.is_default ? 3 : 0.4 + hash(id) * 0.4),
  );
  const factor = (teamF ? (teamShare.get(teamF === "none" ? null : teamF) ?? 0) : 1) * (brandF ? (brandShare.get(brandF) ?? 0) : 1);
  const live = ws.tickets.filter((t) => !t.spam && !ws.deletedTickets.has(t.id) && OPENISH_REPORT.includes(t.status));
  const openOf = (team: string | null | undefined, brand: string | undefined) =>
    live.filter((t) => (team === undefined || (t.team_id ?? null) === team) && (brand === undefined || t.brand_id === brand)).length;
  const teamArg = teamF ? (teamF === "none" ? null : teamF) : undefined;
  const brandArg = brandF || undefined;

  const by_day: ReportsOverview["by_day"] = [];
  const start = Date.UTC(from.getUTCFullYear(), from.getUTCMonth(), from.getUTCDate());
  const end = Date.UTC(to.getUTCFullYear(), to.getUTCMonth(), to.getUTCDate());
  for (let d = start; d <= end && by_day.length < 400; d += DAY) {
    const date = new Date(d).toISOString().slice(0, 10);
    const dow = new Date(d).getUTCDay();
    const weekend = dow === 0 || dow === 6;
    const trend = 1 + ((d - start) / DAY) * 0.004;
    const base = (weekend ? 9 : 34) * trend;
    const created = Math.round(base * (0.75 + hash(date) * 0.5) * factor);
    const solved = Math.max(0, Math.round(created * (0.8 + hash(date + "s") * 0.3)));
    by_day.push({ date, created, solved });
  }
  const created = by_day.reduce((a, b) => a + b.created, 0);
  const solved = by_day.reduce((a, b) => a + b.solved, 0);
  const key = `${by_day[0]?.date}-${by_day.length}-${teamF}-${brandF}`;
  const agentShares = [0.27, 0.22, 0.21, 0.18, 0.12];
  const frp = (k: string) => Math.round(22 + hash(key + k) * 50);
  const group = (share: number, k: string, open: number) => ({
    created: Math.round(created * share),
    solved: Math.round(solved * share),
    open_now: open,
    first_response_p50_minutes: created * share >= 1 ? frp(k) : null,
  });
  const teamRows = teamKeys
    .filter((k) => teamArg === undefined || k === teamArg)
    .map((k) => {
      const share = teamArg === undefined ? (teamShare.get(k) ?? 0) : 1;
      return { team_id: k, ...group(share, `team${k}`, openOf(k, brandArg)) };
    })
    .filter((r) => r.created || r.solved || r.open_now)
    .sort((a, b) => b.created - a.created || b.open_now - a.open_now);
  const brandRows = ws.brands
    .filter((b) => brandArg === undefined || b.id === brandArg)
    .map((b) => {
      const share = brandArg === undefined ? (brandShare.get(b.id) ?? 0) : 1;
      return { brand_id: b.id, ...group(share, `brand${b.id}`, openOf(teamArg, b.id)) };
    })
    .filter((r) => r.created || r.solved || r.open_now)
    .sort((a, b) => b.created - a.created || b.open_now - a.open_now);
  return {
    created,
    solved,
    open_now: openOf(teamArg, brandArg),
    first_response_p50_minutes: created ? Math.round(38 + hash(key) * 30) : null,
    resolution_p50_minutes: solved ? Math.round(6 * 60 + hash(key + "r") * 240) : null,
    csat_avg: solved ? Math.round((4.3 + hash(key + "c") * 0.5) * 100) / 100 : null,
    csat_count: Math.round(solved * 0.31),
    sla_breached: Math.round(created * (0.03 + hash(key + "b") * 0.03)),
    by_day,
    by_channel: {
      email: Math.round(created * 0.68),
      chat: Math.round(created * 0.22),
      api: Math.round(created * 0.06),
      portal: created - Math.round(created * 0.68) - Math.round(created * 0.22) - Math.round(created * 0.06),
    },
    by_severity: {
      sev1: Math.round(created * 0.02),
      sev2: Math.round(created * 0.07),
      sev3: Math.round(created * 0.16),
      sev4: Math.round(created * 0.21),
      none: created - Math.round(created * 0.02) - Math.round(created * 0.07) - Math.round(created * 0.16) - Math.round(created * 0.21),
    },
    by_agent: solved
      ? agentShares.map((share, i) => {
          const u = db.users[i];
          return {
            user: { id: u.id, name: u.name, email: u.email, avatar_url: u.avatar_url },
            solved: Math.round(solved * share),
            first_response_p50_minutes: frp(u.id),
          };
        })
      : [],
    by_team: teamRows,
    by_brand: brandRows,
  };
}

const OPENISH_REPORT: TicketStatus[] = ["open", "pending", "on_hold"];

/** Used by the live simulator. */
export function customerReply(ws: WsData, t: Ticket, html: string) {
  addMessage(ws, t, { kind: "reply", direction: "inbound", html });
  if (t.assignee?.id === db.meId) notify(ws, "customer_replied", t, { contact: { name: t.contact?.name ?? "" } });
}

export function meRef() {
  return userRef(me().id);
}

/** A deleted message (or a note sent as a reply) as agents see it: who deleted it, not what it said. */
function tombstone(m: Message): Message {
  return m.deleted_at ? { ...m, body_html: "", body_text: "", has_quoted: false, attachments: [], ai_action: null } : m;
}

/** The ticket's preview, from the latest reply it still shows. */
function refreshPreview(messages: Message[], t: Ticket) {
  const last = messages
    .filter((m) => m.ticket_id === t.id && m.kind === "reply" && !m.deleted_at)
    .sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at))[0];
  t.last_message_preview = last ? last.body_text.slice(0, 200) : "";
}
