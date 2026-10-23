// Mock Ollie (contracts/ai.md, Assistant): conversations, a scripted agent that
// streams text and tool steps over SSE, approval cards that change the ticket
// only once approved (attributed to Ollie), and tips from simple counts.

import type { AssistantSession, AssistantStep, Approval, Preview, Tip, TranscriptItem } from "@/components/assistant/api";
import type { Ticket, UserRef } from "@/lib/api/types";
import { db, userRef, type WsData } from "../db";
import { reserve, spend } from "../aiBalance";
import { HttpError, del, get, invalid, patch, post, respond, type Ctx } from "../router";
import { aiDecisions } from "@/lib/decisions";
import { applyPatch, findTicket } from "../tickets";
import { nowIso, uuid } from "../util";
import { assertAi } from "./ai";
import { connected, linkTicket } from "./integrations";

type Pending = {
  id: string;
  callId: string;
  ticketIds: string[];
  patch: Record<string, unknown>;
  summary: string;
  token: string;
  step: AssistantStep;
  /** A change outside the tickets (a connected tool, an API write), run on approval instead of a ticket patch. */
  external?: () => Partial<AssistantStep> & { text: string };
};
type Sess = AssistantSession & { wid: string; items: TranscriptItem[]; pending: Pending | null };

const sessions: Sess[] = [];
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

function mine(ctx: Ctx): Sess[] {
  return sessions.filter((s) => s.wid === ctx.ws.workspace.id).sort((a, b) => b.updated_at.localeCompare(a.updated_at));
}

function session(ctx: Ctx): Sess {
  const s = mine(ctx).find((x) => x.id === ctx.params.id);
  if (!s) throw new HttpError(404, "not_found", "Conversation not found");
  return s;
}

function out(s: Sess): AssistantSession {
  return { id: s.id, title: s.title, created_at: s.created_at, updated_at: s.updated_at };
}

function check(ws: WsData) {
  assertAi(ws);
  if (ws.ai.assistant === false) throw new HttpError(403, "assistant_off", "The assistant is turned off for this workspace.");
}

function approvalOf(p: Pending, preview: Preview): Approval {
  return { action_id: p.id, call_id: p.callId, tool: p.step.tool, preview, token: p.token, expires_at: new Date(Date.now() + 15 * 60_000).toISOString() };
}

// ─── The scripted agent ────────────────────────────────────────────────────

type Emit = (event: string, data: unknown) => Promise<void>;

function open(ws: WsData): Ticket[] {
  return ws.tickets.filter((t) => !ws.deletedTickets.has(t.id) && !t.spam && (t.status === "open" || t.status === "pending"));
}

function ref(t: Ticket) {
  return { kind: "ticket" as const, id: t.id, label: t.subject, number: t.number };
}

function line(t: Ticket) {
  return `#${t.number} ${t.subject} (${t.priority}${t.assignee ? `, ${t.assignee.name}` : ", unassigned"})`;
}

function teammates(ws: WsData): { user: UserRef; open: number }[] {
  return ws.members
    .filter((m) => m.role !== "viewer")
    .map((m) => userRef(m.user_id))
    .filter((u): u is UserRef => !!u)
    .map((u) => ({ user: u, open: open(ws).filter((t) => t.assignee?.id === u.id).length }))
    .sort((a, b) => a.open - b.open);
}

async function step(emit: Emit, s: AssistantStep, run: () => Partial<AssistantStep>, item: TranscriptItem): Promise<AssistantStep> {
  await emit("tool", s);
  await sleep(550);
  const done = { ...s, status: "done" as const, ...run() };
  await emit("tool", done);
  item.steps.push(done);
  return done;
}

async function say(emit: Emit, item: TranscriptItem, text: string) {
  const words = text.split(/(\s+)/);
  for (let i = 0; i < words.length; i += 4) {
    const part = words.slice(i, i + 4).join("");
    item.text += part;
    await emit("text", { text: part });
    await sleep(28);
  }
}

async function agent(ctx: Ctx, s: Sess, text: string, ticketId: string | undefined, emit: Emit) {
  const ws = ctx.ws;
  const item: TranscriptItem = { role: "assistant", text: "", steps: [] };
  s.items.push(item);
  const lower = text.toLowerCase();
  const id = () => `call_${uuid().slice(0, 8)}`;
  const current = (ticketId && findTicket(ws, ticketId)) || open(ws).find((t) => !t.assignee) || open(ws)[0];

  const tools = await connectedTools(ctx, s, lower, current, emit, item, id);
  if (tools) return tools;

  if (/\bsignature\b/.test(lower)) return signature(ctx, s, text, emit, item, id);

  if (/\b(who should|assign|take this|take it)\b/.test(lower) && current) {
    await step(emit, { id: id(), tool: "get_ticket", label: "Reading the ticket", status: "running" }, () => ({ label: "Read the ticket", summary: `#${current.number}`, refs: [ref(current)] }), item);
    const team = teammates(ws);
    await step(emit, { id: id(), tool: "list_team", label: "Checking the team's workload", status: "running" }, () => ({ label: "Checked the team's workload", summary: `${team.length} teammates` }), item);
    const pick = team[0];
    await say(emit, item, `**${pick.user.name}** has the lightest load right now, with ${pick.open} open tickets. I can assign #${current.number} to them and raise it to high, since the customer is blocked.`);
    const pid = uuid();
    const st: AssistantStep = { id: id(), tool: "update_ticket", label: "Preparing a ticket change", status: "awaiting_approval", action_id: pid };
    await emit("tool", st);
    item.steps.push(st);
    const preview: Preview = {
      title: `Update #${current.number}`,
      summary: current.subject,
      changes: [
        { field: "Assignee", from: current.assignee?.name ?? "Unassigned", to: pick.user.name },
        ...(current.priority === "high" || current.priority === "urgent" ? [] : [{ field: "Priority", from: current.priority, to: "high" }]),
      ],
      targets: [ref(current)],
    };
    s.pending = { id: pid, callId: st.id, ticketIds: [current.id], patch: { assignee_id: pick.user.id, priority: current.priority === "urgent" ? "urgent" : "high" }, summary: `#${current.number} is now with **${pick.user.name}** at high priority.`, token: uuid(), step: st };
    await emit("approval", approvalOf(s.pending, preview));
    return "awaiting_approval";
  }

  if (/\blabel|tag\b/.test(lower)) {
    const hits = open(ws).filter((t) => /bill|invoice|refund|charge|payment|subscription/i.test(t.subject)).slice(0, 8);
    await step(emit, { id: id(), tool: "search_tickets", label: "Searching tickets", status: "running" }, () => ({ label: "Searched tickets", summary: `Found ${hits.length} tickets`, refs: hits.map(ref) }), item);
    if (!hits.length) {
      await say(emit, item, "I couldn't find open billing tickets to label.");
      return "complete";
    }
    await say(emit, item, `${hits.length} open tickets look like billing questions. Here's the change:`);
    const pid = uuid();
    const st: AssistantStep = { id: id(), tool: "bulk_update_tickets", label: "Preparing a bulk change", status: "awaiting_approval", action_id: pid };
    await emit("tool", st);
    item.steps.push(st);
    const preview: Preview = { title: `Label ${hits.length} tickets`, summary: "Billing questions from this week", changes: [{ field: "Labels", from: "", to: "+ Billing" }], targets: hits.map(ref), total: hits.length };
    s.pending = { id: pid, callId: st.id, ticketIds: hits.map((t) => t.id), patch: { add_tags: ["Billing"] }, summary: `Labeled ${hits.length} tickets **Billing**.`, token: uuid(), step: st };
    await emit("approval", approvalOf(s.pending, preview));
    return "awaiting_approval";
  }

  if (/\bsummar/.test(lower) && current) {
    await step(emit, { id: id(), tool: "get_ticket", label: "Reading the ticket", status: "running" }, () => ({ label: "Read the ticket", summary: `#${current.number}, ${current.message_count} messages`, refs: [ref(current)] }), item);
    const who = current.contact?.name ?? "The customer";
    await say(
      emit,
      item,
      `**${who}** wrote in about "${current.subject}".\n\n- It is ${current.status}, ${current.priority} priority, ${current.assignee ? `with ${current.assignee.name}` : "and nobody has it yet"}.\n- Latest: ${current.last_message_preview || "no messages yet"}\n\nNext step: ${current.assignee ? "a reply from the assignee." : "assign it, then reply."}`,
    );
    return "complete";
  }

  if (/\breport|how are we|this week\b/.test(lower)) {
    await step(emit, { id: id(), tool: "get_report", label: "Pulling the numbers", status: "running" }, () => ({ label: "Pulled the numbers", summary: "Last 7 days" }), item);
    const all = open(ws);
    await say(emit, item, `This week: **${all.length} open** tickets, ${all.filter((t) => !t.assignee).length} unassigned and ${all.filter((t) => t.priority === "urgent" || t.priority === "high").length} high or urgent. Median first reply is about 42 minutes, and CSAT is holding at 94%.`);
    return "complete";
  }

  // Triage: urgent first, then old unassigned.
  const urgent = open(ws)
    .filter((t) => t.priority === "urgent" || t.priority === "high" || !t.assignee)
    .slice(0, 5);
  await step(emit, { id: id(), tool: "search_tickets", label: "Searching tickets", status: "running" }, () => ({ label: "Searched tickets", summary: `Found ${urgent.length} tickets`, refs: urgent.map(ref) }), item);
  await say(emit, item, urgent.length ? `These need a look first:\n\n${urgent.map((t) => `- ${line(t)}`).join("\n")}\n\nWant me to find owners for the unassigned ones?` : "Nothing urgent right now. The queue is in good shape.");
  return "complete";
}

/** "Set this as our signature": found through the API, proposed on the addresses, applied on approval. */
async function signature(ctx: Ctx, s: Sess, text: string, emit: Emit, item: TranscriptItem, id: () => string) {
  const ws = ctx.ws;
  const start = text.indexOf("<");
  const end = text.lastIndexOf(">");
  const html = start >= 0 && end > start ? text.slice(start, end + 1) : text;
  await step(emit, { id: id(), tool: "find_api", label: "Looking up how to do it", status: "running" }, () => ({ label: "Looked up how to do it", summary: "3 operations" }), item);
  const box = ws.mailboxes.find((m) => m.status !== "error") ?? ws.mailboxes[0];
  await step(emit, { id: id(), tool: "api_get", label: "Reading", status: "running" }, () => ({ label: "Read", summary: "List mailboxes" }), item);
  if (!box) {
    await say(emit, item, "There is no address to put a signature on yet. Connect one in **Settings, Mailboxes** first.");
    return "complete";
  }
  await say(
    emit,
    item,
    `A signature for everyone goes on the address, so every reply from **${box.email}** carries it. The placeholders like \`{%user.fullName%}\` are another tool's, so I switched them to opendesk's, such as \`{{ agent.name }}\`.`,
  );
  const value = html.replace(/\{%\s*user\.fullName\s*%\}/g, "{{ agent.name }}").replace(/\{%\s*user\.jobTitle[^%]*%\}/g, "{{ agent.title }}").replace(/\{%\s*mailbox\.email\s*%\}/g, "{{ mailbox.email }}");
  const pid = uuid();
  const st: AssistantStep = { id: id(), tool: "api_change", label: "Update a mailbox", status: "awaiting_approval", action_id: pid };
  await emit("tool", st);
  item.steps.push(st);
  const preview: Preview = { title: "Update a mailbox", summary: box.name || box.email, changes: [{ field: "signature_html", from: box.signature_html || "", to: value }] };
  s.pending = {
    id: pid,
    callId: st.id,
    ticketIds: [],
    patch: {},
    summary: "",
    token: uuid(),
    step: st,
    external: () => {
      box.signature_html = value;
      return { label: "Made the change", summary: "Update a mailbox", text: `Done. Replies from **${box.email}** now end with the new signature.` };
    },
  };
  await emit("approval", approvalOf(s.pending, preview));
  return "awaiting_approval";
}

const productWords: Record<string, RegExp> = {
  linear: /\blinear\b/,
  github: /\bgithub\b/,
  atlassian: /\b(jira|confluence|atlassian)\b/,
  sentry: /\bsentry\b/,
  notion: /\bnotion\b/,
  slack: /\bslack\b/,
  stripe: /\bstripe\b/,
  asana: /\basana\b/,
};
const productName: Record<string, string> = { linear: "Linear", github: "GitHub", atlassian: "Jira", sentry: "Sentry", notion: "Notion", slack: "Slack", stripe: "Stripe", asana: "Asana" };

/** Requests for a connected product: lookups run, anything else waits for approval and links back. */
async function connectedTools(ctx: Ctx, s: Sess, lower: string, current: Ticket | undefined, emit: Emit, item: TranscriptItem, id: () => string) {
  const ws = ctx.ws;
  const named = Object.keys(productWords).find((k) => productWords[k].test(lower));
  const wantsIssue = /\b(open|create|file|make|log)\b.*\b(issue|bug|task)\b/.test(lower);
  if (!named && !wantsIssue) return null;
  const all = connected(ws);
  const hit = all.find((c) => c.x.provider === named) ?? (!named ? all.find((c) => ["linear", "github", "atlassian", "asana"].includes(c.x.provider)) : undefined);
  if (!hit) {
    if (!named) return null;
    await say(emit, item, `${productName[named]} isn't connected yet. An admin can connect it in **Settings, Integrations**, and then I can work there for you.`);
    return "complete";
  }
  const { x, hosts } = hit;
  const slug = x.provider === "custom" ? x.name.toLowerCase().replace(/[^a-z0-9]+/g, "_") : x.provider;
  const enabled = (n: string) => x.tools.find((tl) => tl.name === n && tl.enabled);
  const lookup = x.tools.find((tl) => tl.read_only && tl.enabled);
  const create = x.tools.find((tl) => !tl.read_only && tl.enabled && /create|open|post|new/i.test(tl.name)) ?? enabled("create_issue");
  const host = hosts[0] ?? x.host;

  if (/\b(find|search|related|already|look up|check)\b/.test(lower) || !create) {
    if (!lookup) {
      await say(emit, item, `Every lookup tool for ${x.name} is turned off, so I can't search it right now.`);
      return "complete";
    }
    await step(emit, { id: id(), tool: `${slug}__${lookup.name}`, label: `Checking ${x.name}`, status: "running" }, () => ({ label: `Checked ${x.name}`, summary: `Read from ${x.name}` }), item);
    await say(emit, item, current ? `I searched ${x.name} for "${current.subject}" and found **no open item** that covers it yet. Want me to create one and link it here?` : `I searched ${x.name} and found nothing that matches.`);
    return "complete";
  }
  if (!current) {
    await say(emit, item, `Open the ticket you want this for, and I'll prepare it in ${x.name}.`);
    return "complete";
  }
  await step(emit, { id: id(), tool: "get_ticket", label: "Reading the ticket", status: "running" }, () => ({ label: "Read the ticket", summary: `#${current.number}`, refs: [ref(current)] }), item);
  const who = current.contact?.name ?? "A customer";
  const title = current.subject.length > 80 ? `${current.subject.slice(0, 77)}…` : current.subject;
  const body = `**Reported by** ${who} in opendesk #${current.number}.\n\n${current.last_message_preview || "No details yet."}\n\n**Impact:** the customer is ${current.priority === "urgent" || current.priority === "high" ? "blocked" : "affected"}.`;
  await say(emit, item, `Here's the ${x.name} ${x.provider === "slack" ? "message" : "item"} I'd create from #${current.number}:`);
  const pid = uuid();
  const st: AssistantStep = { id: id(), tool: `${slug}__${create.name}`, label: `Preparing ${create.title.toLowerCase()} in ${x.name}`, status: "awaiting_approval", action_id: pid };
  await emit("tool", st);
  item.steps.push(st);
  const preview: Preview = {
    title: `${x.name}: ${create.title}`,
    system: x.name,
    changes: [{ field: "Title", from: "", to: title }, ...(x.provider === "linear" ? [{ field: "Team", from: "", to: "Support" }, { field: "Priority", from: "", to: current.priority === "urgent" ? "Urgent" : current.priority === "high" ? "High" : "Medium" }] : [])],
    body,
  };
  const n = 180 + current.number;
  const url = x.provider === "github" ? `https://github.com/margin/app/issues/${n}` : x.provider === "atlassian" ? `https://margin.atlassian.net/browse/SUP-${n}` : `https://${host}/margin/issue/FN-${n}`;
  s.pending = {
    id: pid,
    callId: st.id,
    ticketIds: [],
    patch: {},
    summary: "",
    token: uuid(),
    step: st,
    external: () => {
      linkTicket(ws, current, x, url, title, "ai");
      return {
        label: `Done in ${x.name}`,
        summary: `Created in ${x.name} and linked to the ticket`,
        refs: [{ kind: "external", id: url, label: title }],
        text: `Created it in ${x.name} and linked it to #${current.number}. Anyone on the ticket can open it from **Linked**.`,
      };
    },
  };
  await emit("approval", approvalOf(s.pending, preview));
  return "awaiting_approval";
}

function stream(run: (emit: Emit) => Promise<void>): Response {
  const enc = new TextEncoder();
  const body = new ReadableStream<Uint8Array>({
    async start(controller) {
      const emit: Emit = async (event, data) => {
        controller.enqueue(enc.encode(`event: ${event}\ndata: ${JSON.stringify(data)}\n\n`));
      };
      try {
        await run(emit);
      } catch (e) {
        const err = e instanceof HttpError ? e : new HttpError(500, "server_error", "Internal error in mock assistant");
        await emit("error", { status: err.status, code: err.code, detail: err.detail });
      }
      controller.close();
    },
  });
  return new Response(body, { status: 200, headers: { "content-type": "text/event-stream" } });
}

function tips(ws: WsData): Tip[] {
  const all = open(ws);
  const day = Date.now() - 86_400_000;
  const out: Tip[] = [];
  const urgent = all.filter((t) => t.priority === "urgent" && !t.assignee).length;
  if (urgent) out.push({ key: "urgent_unassigned", text: `${urgent} urgent ${urgent === 1 ? "ticket has" : "tickets have"} no owner yet.`, prompt: "Which urgent tickets have no owner, and who should take each?", severity: "high", count: urgent });
  const soon = Date.now() + 4 * 3_600_000;
  const risky = all.filter((t) => {
    if (!t.sla || t.sla.breached) return false;
    const due = Date.parse(t.sla.first_response_due_at ?? t.sla.resolution_due_at ?? "");
    const risk = aiDecisions(t)?.fields.sla_risk?.value;
    return due > Date.now() && due <= soon && typeof risk === "number" && risk >= 0.6;
  }).length;
  if (risky) out.push({ key: "sla_at_risk", text: `${risky} ${risky === 1 ? "ticket looks" : "tickets look"} likely to miss ${risky === 1 ? "its" : "their"} SLA in the next 4 hours.`, prompt: "Which open tickets are likely to miss their SLA soon, and what should we do first on each?", severity: "high", count: risky });
  const old = all.filter((t) => !t.assignee && Date.parse(t.created_at) < day).length;
  if (old) out.push({ key: "unassigned_old", text: `${old} unassigned ${old === 1 ? "ticket is" : "tickets are"} older than a day.`, prompt: "Show the open unassigned tickets older than a day and suggest an assignee for each, based on the team's workload.", severity: "medium", count: old });
  return out;
}

export function registerAssistant() {
  get("/v1/assistant/sessions", (ctx) => {
    check(ctx.ws);
    return { data: mine(ctx).map(out) };
  });
  post("/v1/assistant/sessions", (ctx) => {
    check(ctx.ws);
    const now = nowIso();
    const s: Sess = { id: uuid(), title: typeof ctx.body.title === "string" ? ctx.body.title.slice(0, 120) : "", created_at: now, updated_at: now, wid: ctx.ws.workspace.id, items: [], pending: null };
    sessions.push(s);
    return out(s);
  });
  get("/v1/assistant/sessions/:id", (ctx) => {
    check(ctx.ws);
    const s = session(ctx);
    return { session: out(s), items: s.items, approval: null };
  });
  patch("/v1/assistant/sessions/:id", (ctx) => {
    const s = session(ctx);
    const title = typeof ctx.body.title === "string" ? ctx.body.title.trim().slice(0, 120) : "";
    if (!title) throw invalid("title is required");
    s.title = title;
    return out(s);
  });
  del("/v1/assistant/sessions/:id", (ctx) => {
    const s = session(ctx);
    sessions.splice(sessions.indexOf(s), 1);
    return respond(204);
  });
  get("/v1/assistant/tips", (ctx) => {
    check(ctx.ws);
    return { tips: tips(ctx.ws) };
  });

  post("/v1/assistant/sessions/:id/messages", (ctx) => {
    check(ctx.ws);
    const s = session(ctx);
    const text = typeof ctx.body.text === "string" ? ctx.body.text.trim() : "";
    if (!text) throw invalid("text must be 1 to 8,000 characters");
    reserve(ctx.ws, "assistant");
    const context = (ctx.body.context ?? {}) as { ticket_id?: string };
    // A new message moves on from a change still waiting.
    if (s.pending) {
      s.pending.step.status = "expired";
      s.pending = null;
    }
    s.items.push({ role: "user", text, steps: [] });
    if (!s.title) s.title = text.length > 60 ? `${text.slice(0, 57)}…` : text;
    s.updated_at = nowIso();
    return stream(async (emit) => {
      await emit("session", out(s));
      await sleep(350);
      const status = await agent(ctx, s, text, context.ticket_id, emit);
      spend(ctx.ws, "assistant");
      await emit("done", { status });
    });
  });

  post("/v1/assistant/actions/:id/decision", (ctx) => {
    check(ctx.ws);
    const s = sessions.find((x) => x.pending?.id === ctx.params.id && x.wid === ctx.ws.workspace.id);
    const p = s?.pending;
    if (!s || !p || ctx.body.token !== p.token) throw new HttpError(403, "approval_invalid", "This approval is no longer valid.");
    const approve = ctx.body.decision === "approve";
    s.pending = null;
    const item: TranscriptItem = { role: "assistant", text: "", steps: [] };
    s.items.push(item);
    return stream(async (emit) => {
      if (!approve) {
        p.step.status = "denied";
        await emit("tool", p.step);
        await say(emit, item, "Okay, I left it as it is. What would you like instead?");
        await emit("done", { status: "complete" });
        return;
      }
      p.step.status = "approved";
      await emit("tool", p.step);
      await sleep(450);
      if (p.external) {
        const { text, ...done } = p.external();
        Object.assign(p.step, done, { status: "done" });
        await emit("tool", p.step);
        await say(emit, item, text);
        spend(ctx.ws, "assistant");
        await emit("done", { status: "complete" });
        return;
      }
      const me = userRef(db.meId);
      for (const tid of p.ticketIds) {
        const t = findTicket(ctx.ws, tid);
        if (!t) continue;
        const before = ctx.ws.activities.length;
        applyPatch(ctx.ws, t, p.patch, me, true, { createLabels: true, lenient: true });
        // Ollie made the change on the user's behalf.
        for (const a of ctx.ws.activities.slice(before)) a.actor = { type: "ai", user: me };
      }
      p.step.status = "done";
      p.step.label = p.step.tool === "bulk_update_tickets" ? "Updated the tickets" : "Updated the ticket";
      p.step.summary = p.ticketIds.length === 1 ? "Done" : `${p.ticketIds.length} tickets`;
      await emit("tool", p.step);
      await say(emit, item, `Done. ${p.summary}`);
      spend(ctx.ws, "assistant");
      await emit("done", { status: "complete" });
    });
  });
}
