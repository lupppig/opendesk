// Fake activity so the app feels alive in mock mode: customer replies, new
// tickets, notifications, teammates moving between tickets and typing, and
// Ollie answering chat visitors on a ticket you're viewing (ollie.ts).

import { contactRef, db, type WsData } from "./db";
import { activeTicketIds, emitOnTicket, publish, setTeammatePresence, teammatePresence } from "./gateway";
import { customerReply } from "./handlers/tickets";
import { FOLLOW_UPS, GENERATED, TEAM } from "./content";
import { countRuleRun, evaluateRouting } from "./teams";
import { addMessage, newTicket, notify, OPENISH } from "./tickets";
import { ollieOnActive } from "./ollie";
import { textToHtml } from "./util";

const timers: ReturnType<typeof setTimeout>[] = [];
let running = false;

const rnd = (min: number, max: number) => min + Math.random() * (max - min);
const any = <T>(a: T[]): T | undefined => a[Math.floor(Math.random() * a.length)];

function ws(): WsData {
  return db.workspaces[0];
}

function schedule(fn: () => void, min: number, max: number) {
  const t = setTimeout(() => {
    if (running) {
      try {
        fn();
      } catch (e) {
        console.warn("[mock] live event failed", e);
      }
    }
    schedule(fn, min, max);
  }, rnd(min, max));
  timers.push(t);
}

function randomCustomerReply() {
  const w = ws();
  const candidates = w.tickets.filter((t) => !t.spam && OPENISH.includes(t.status) && t.contact && !w.deletedTickets.has(t.id));
  const t = any(candidates);
  if (!t) return;
  customerReply(w, t, textToHtml(any(FOLLOW_UPS) ?? "Any update?"));
}

const NEW_SUBJECTS = [
  "Can't log in after changing my email",
  "Shared link asks for a password",
  "Question about the Business plan",
  "Images not loading in notes",
  "Exported CSV has broken characters",
  "Two workspaces after signing up with Google",
];

const CHAT_PAGES = ["/pricing", "/docs/getting-started", "/blog/launch", "/", "/account/billing"];

/**
 * A new conversation, routed like the server would (contracts/teams.md "Routing"):
 * email arrives at an active address or one of its aliases, chat starts on a widget
 * (sometimes with a topic) and has no reply address until it needs one.
 */
function randomNewTicket() {
  const w = ws();
  const contact = any(w.contacts);
  if (!contact) return;
  const subject = any(NEW_SUBJECTS) ?? GENERATED[0].subject;
  const body = `Hi there,\n\n${subject}. Could you take a look?\n\n${contact.name.split(" ")[0]}`;
  const widget = Math.random() < 0.25 ? any(w.widgets) : undefined;
  let t;
  if (widget) {
    const topic = Math.random() < 0.6 ? any(widget.settings.topics) : undefined;
    const slug = w.brands.find((b) => b.id === widget.brand_id)?.slug ?? "margin";
    const page_url = `https://${slug}.app${any(CHAT_PAGES) ?? "/"}`;
    const route = evaluateRouting(w, { channel: "chat", from: contact.email ?? "", subject, body, widget_id: widget.id, topic: topic?.id, page_url });
    t = newTicket(w, { subject, contact: contactRef(contact), channel: "chat", priority: "normal", mailbox_id: null, team_id: route.team_id, brand_id: route.brand_id, widget_id: widget.id, page_url, routed: route.routed });
    countRuleRun(w, route.routed);
  } else {
    const box = any(w.mailboxes.filter((m) => m.status === "active"));
    if (!box) return;
    const to = Math.random() < 0.3 && box.aliases.length ? (any(box.aliases) ?? box.email) : box.email;
    const route = evaluateRouting(w, { channel: "email", to, from: contact.email ?? "", subject, body });
    t = newTicket(w, { subject, contact: contactRef(contact), channel: "email", priority: Math.random() < 0.2 ? "high" : "normal", mailbox_id: route.mailbox_id ?? box.id, team_id: route.team_id, brand_id: route.brand_id, received_at: to, routed: route.routed });
    t.origin_mailbox_id = box.id;
    countRuleRun(w, route.routed);
  }
  contact.ticket_count++;
  publish(w, "ticket.created", { ticket: t });
  addMessage(w, t, { kind: "reply", direction: "inbound", html: textToHtml(body) });
  // Nobody is assigned: the team hears (contracts/api.md "Assignment"). A chat waits for Ollie's handoff.
  if (!t.assignee && t.channel === "email" && w.workspace.settings.new_ticket_notify !== "off") {
    notify(w, "new_ticket", t, { channel: t.channel, contact: { id: contact.id, name: contact.name } });
  }
}

function randomNotification() {
  const w = ws();
  const t = any(w.tickets.filter((x) => x.assignee?.id === db.meId && OPENISH.includes(x.status)));
  if (!t) return;
  const jonas = db.users[1];
  notify(w, Math.random() < 0.5 ? "mentioned" : "customer_replied", t, { actor: { id: jonas.id, name: jonas.name, email: jonas.email, avatar_url: "" }, contact: { name: t.contact?.name ?? "" } });
}

function teammateWander() {
  const w = ws();
  const mates = TEAM.slice(1, 5).map((p, i) => ({ ...p, id: db.users[i + 1].id }));
  const mate = any(mates);
  const t = any(w.tickets.filter((x) => !x.spam && OPENISH.includes(x.status)));
  if (!mate || !t) return;
  // Leave wherever they were, then show up on the new ticket.
  for (const other of w.tickets) setTeammatePresenceIfPresent(other.id, mate.id);
  setTeammatePresence(t.id, { user_id: mate.id, name: mate.name, avatar: "", state: Math.random() < 0.3 ? "replying" : "viewing" });
}

function setTeammatePresenceIfPresent(tid: string, uid: string) {
  if (teammatePresence.get(tid)?.some((m) => m.user_id === uid)) setTeammatePresence(tid, null, uid);
}

function typingOnActive() {
  const w = ws();
  const tid = any(activeTicketIds());
  if (!tid) return;
  const t = w.tickets.find((x) => x.id === tid);
  if (!t) return;
  if (t.channel === "chat" && Math.random() < 0.6) {
    emitOnTicket(tid, "customer_typing", { ticket_id: tid, contact_id: t.contact?.id });
    return;
  }
  const present = teammatePresence.get(tid)?.[0];
  const mate = present ?? { user_id: db.users[2].id, name: db.users[2].name };
  emitOnTicket(tid, "typing", { user_id: mate.user_id, name: mate.name });
}

/**
 * A new ticket in one of the signed-in person's other workspaces (Lumen Labs, Pressroom), so a
 * Space mixing workspaces shows another workspace's numbers moving (contracts/spaces.md "Rows").
 */
function randomSideTicket() {
  const side = db.workspaces.filter((w, i) => i > 0 && w.members.some((m) => m.user_id === db.users[0]?.id) && w.contacts.length);
  const w = any(side);
  const contact = w && any(w.contacts);
  if (!w || !contact) return;
  const subject = any(NEW_SUBJECTS) ?? GENERATED[0].subject;
  const t = newTicket(w, { subject, contact: contactRef(contact), channel: "email", team_id: any(w.teams)?.id ?? null });
  contact.ticket_count++;
  publish(w, "ticket.created", { ticket: t });
  addMessage(w, t, { kind: "reply", direction: "inbound", html: textToHtml(`Hi,\n\n${subject}.\n\n${contact.name.split(" ")[0]}`) });
}

export function startLive() {
  if (running) return;
  running = true;
  if (timers.length) return;
  schedule(randomCustomerReply, 25_000, 45_000);
  schedule(randomNewTicket, 80_000, 110_000);
  schedule(randomSideTicket, 70_000, 120_000);
  schedule(randomNotification, 60_000, 120_000);
  schedule(teammateWander, 30_000, 60_000);
  schedule(typingOnActive, 12_000, 25_000);
  schedule(ollieOnActive, 35_000, 60_000);
}

export function pauseLive() {
  running = false;
}

export function resumeLive() {
  running = true;
  if (!timers.length) startLive();
}
