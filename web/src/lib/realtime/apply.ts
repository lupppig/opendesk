// Applies one event envelope (contracts/events.md) to the TanStack Query cache.
// Payloads carry the full REST object, so most events patch in place; only
// aggregates (counts, reports) are invalidated.

import type { InfiniteData, QueryClient } from "@tanstack/react-query";
import type {
  Activity,
  Contact,
  Envelope,
  Import,
  KnowledgeSource,
  McpServer,
  Member,
  Message,
  Notification,
  Page,
  SuggestedReply,
  Team,
  Thread,
  Ticket,
  TicketDraft,
  TicketLink,
  Workspace,
} from "../api/types";
import { inWorkspace, scopeWorkspace } from "../api/client";
import { qk, removeFromList, upsertInList } from "../queries";
import { applyAgentQualityEvent } from "../agentTests";
import { updateWorkspace, useSession } from "../session";
import { cachedTicket, forgetTicket, patchTicketEverywhere, removeTicketEverywhere, teamsOf } from "../ticketCache";
import { markHidden, unmarkHidden } from "./store";
import { applyCollabEvent } from "../collab/cache";
import { applyWikiEvent } from "../wiki/events";

// Events that can tick off a setup checklist step (contracts/api.md "Onboarding").
const onboardingResources = new Set(["mailbox", "member", "tag", "kb_article", "workspace"]);
// Events that can tick off an AI agent setup step (contracts/agent.md "Setup").
const agentSetupResources = new Set(["mailbox", "kb_article", "workspace", "ai_knowledge", "ai_mcp_server", "bot"]);
// Bots list where they answer, so widget and address changes move their usage too.
const botResources = new Set(["bot", "widget", "mailbox"]);
// Events that change what a reply's template values or signature read.
const templateResources = new Set(["member", "team", "mailbox", "brand", "contact", "company", "workspace"]);
// Events that move a plan limit's count (billing usage).
const limitEvents = new Set(["mailbox.created", "mailbox.deleted", "member.added", "member.removed"]);

export type ApplyContext = {
  me: string;
  /** The envelope came from the user:<me> channel (the gateway pushes envelopes without `topics`). */
  viaUser?: boolean;
  /** The signed-in user was removed from this workspace or changed role. */
  onSelfMembershipChanged?: () => void;
  onNotification?: (n: Notification) => void;
  /** A ticket arrived (once per ticket, from the workspace channel). */
  onTicketCreated?: (t: Ticket) => void;
  /** The private teams this connection may see (the ws token's `teams` claim). */
  privateTeams?: () => string[];
};

// Settings-style resources: event prefix -> [list key, payload field].
const simpleLists: Record<string, { key: () => readonly unknown[]; field: string }> = {
  tag: { key: () => qk.tags, field: "tag" },
  tag_group: { key: () => qk.tagGroups, field: "tag_group" },
  custom_field: { key: () => qk.customFields, field: "custom_field" },
  mailbox: { key: () => qk.mailboxes, field: "mailbox" },
  saved_reply: { key: () => qk.savedReplies, field: "saved_reply" },
  view: { key: () => qk.views, field: "view" },
  automation: { key: () => qk.automations, field: "automation" },
  sla_policy: { key: () => qk.slaPolicies, field: "sla_policy" },
  kb_collection: { key: () => qk.kbCollections, field: "kb_collection" },
  team: { key: () => qk.teams, field: "team" },
  brand: { key: () => qk.brands, field: "brand" },
  widget: { key: () => qk.widgets, field: "widget" },
  routing_rule: { key: () => qk.routingRules, field: "routing_rule" },
  webhook: { key: () => qk.webhooks, field: "webhook" },
  integration: { key: () => qk.integrations, field: "integration" },
  support_channel_connection: { key: () => qk.supportChannelConnections, field: "support_channel_connection" },
  support_channel_destination: { key: () => qk.supportChannelDestinations, field: "support_channel_destination" },
};

/** What an import has written so far, to tell whether lists have news. */
const landed = (x: Import) => x.counts.tickets + x.counts.updated + x.counts.articles + x.counts.contacts;

// Coalescing timers, one per workspace and kind: events of several workspaces arrive
// at once (several workspaces), and a timer runs later, outside the event's scope.
const timers = new Map<string, ReturnType<typeof setTimeout>>();
function later(kind: string, ms: number, fn: () => void) {
  const wid = scopeWorkspace() ?? "none";
  const id = `${kind}:${wid}`;
  if (timers.has(id)) return;
  timers.set(
    id,
    setTimeout(() => {
      timers.delete(id);
      inWorkspace(wid, fn);
    }, ms),
  );
}

/** Refetch what imports and undos write: tickets, people, labels and the knowledge base. */
function refreshImported(qc: QueryClient) {
  later("import", 6000, () => {
    for (const key of [qk.of("tickets"), qk.of("contacts"), qk.of("companies"), qk.tags, qk.of("kb")]) void qc.invalidateQueries({ queryKey: key });
  });
}

/**
 * Coalesce bursts (bulk edits, imports, auto-close) into one counts refetch every two seconds at
 * most, the sidebar's numbers of every workspace with it. A refetch still running is left to
 * finish and the next one starts after it, so the numbers settle on a read after the last event.
 */
function refreshCounts(qc: QueryClient) {
  later("counts", 2000, function refetch() {
    const keys = [qk.counts, qk.of("tickets", "everywhere"), qk.overview];
    if (keys.some((queryKey) => qc.isFetching({ queryKey }) > 0)) return later("counts", 2000, refetch);
    for (const queryKey of keys) void qc.invalidateQueries({ queryKey }, { cancelRefetch: false });
  });
}

function appendUnique<R extends { id: string }>(qc: QueryClient, key: readonly unknown[], row: R) {
  qc.setQueryData<R[]>(key, (old) => {
    if (!old) return old;
    const i = old.findIndex((x) => x.id === row.id);
    if (i >= 0) {
      const next = old.slice();
      next[i] = row;
      return next;
    }
    return [...old, row];
  });
}

/**
 * Merge by id. Chat replies arrive customer-safe (no author email, no
 * to/cc/bcc), so an existing copy keeps its fields, and a new partial one
 * triggers a refetch of the full list.
 */
function mergeMessage(qc: QueryClient, ticketId: string, m: Message) {
  const key = qk.messages(ticketId);
  const old = qc.getQueryData<Message[]>(key);
  if (!old) return;
  const defined = Object.fromEntries(Object.entries(m).filter(([, v]) => v !== undefined && v !== null)) as Partial<Message>;
  // A marker (deleted, purged, or a note sent as a reply) and a restore set these, null included.
  if ("deleted_at" in m)
    Object.assign(defined, {
      deleted_at: m.deleted_at ?? null,
      deleted_by: m.deleted_by ?? null,
      purged_at: m.purged_at ?? null,
      sent_as_reply_id: m.sent_as_reply_id ?? null,
    });
  // A marker carries no action card: the one cached goes with it.
  if (m.deleted_at) defined.ai_action = null;
  const i = old.findIndex((x) => x.id === m.id);
  if (i >= 0) {
    qc.setQueryData<Message[]>(key, old.map((x, j) => (j === i ? { ...x, ...defined, author: { ...x.author, ...(m.author ?? {}) } } : x)));
    return;
  }
  const partial = !Array.isArray(m.to) || !Array.isArray(m.attachments) || !m.author;
  const full = { ...m, to: m.to ?? [], cc: m.cc ?? [], bcc: m.bcc ?? [], attachments: m.attachments ?? [] };
  // Our own send can arrive here before its POST answers: take the optimistic copy's place.
  const temp = old.findIndex(
    (x) =>
      x.id.startsWith("tmp-") &&
      x.kind === m.kind &&
      (x.thread_id ?? null) === (m.thread_id ?? null) &&
      x.author?.user?.id === m.author?.user?.id &&
      x.delivery_status !== "failed" &&
      // A reply still held for its undo window has not been sent: nothing can be its copy.
      x.delivery_status !== "scheduled",
  );
  if (temp >= 0 && m.author?.type === "user") {
    qc.setQueryData<Message[]>(key, old.map((x, j) => (j === temp ? full : x)));
    if (partial) void qc.invalidateQueries({ queryKey: key, exact: true });
    return;
  }
  qc.setQueryData<Message[]>(key, [...old, full]);
  if (partial) void qc.invalidateQueries({ queryKey: key, exact: true });
}

type ContactPages = InfiniteData<Page<Contact>, string | null>;

function patchContactLists(qc: QueryClient, c: Contact | null, removeId?: string) {
  for (const [key, data] of qc.getQueriesData<ContactPages>({ queryKey: qk.contactLists })) {
    if (!data) continue;
    const pages = data.pages.map((p) => ({
      ...p,
      data: removeId ? p.data.filter((x) => x.id !== removeId) : p.data.map((x) => (c && x.id === c.id ? c : x)),
    }));
    qc.setQueryData<ContactPages>(key, { ...data, pages });
  }
}

/**
 * Applies an envelope to the cache of its own workspace: every key built and request
 * made while it applies is that workspace's, whichever one has the focus.
 */
export function applyEnvelope(qc: QueryClient, env: Envelope, ctx: ApplyContext): void {
  inWorkspace(env.workspace_id || null, () => applyInScope(qc, env, ctx));
}

function applyInScope(qc: QueryClient, env: Envelope, ctx: ApplyContext): void {
  const d = env.data as Record<string, unknown>;
  const [resource, verb] = splitType(env.type);
  if (onboardingResources.has(resource) && verb !== "deleted") void qc.invalidateQueries({ queryKey: qk.onboarding });
  if (agentSetupResources.has(resource)) void qc.invalidateQueries({ queryKey: qk.agentSetup });
  // A new default or an order touches every row, so the short list is refetched whole.
  if (botResources.has(resource)) void qc.invalidateQueries({ queryKey: qk.bots });
  if (limitEvents.has(env.type)) void qc.invalidateQueries({ queryKey: qk.billing, exact: true });
  if (resource === "bot") return;
  // What templates fill in (contracts/templates.md): signatures, profiles, fields and designs.
  if (templateResources.has(resource)) void qc.invalidateQueries({ queryKey: qk.of("tickets", "variables") });
  if (resource === "custom_field") void qc.invalidateQueries({ queryKey: qk.templateVariables });
  if (env.type === "email_template.updated") {
    void qc.invalidateQueries({ queryKey: qk.emailTemplates(d.brand_id as string) });
    return;
  }

  // Collab mail (contracts/collab.md "Events"): ids only, for the people who see them; lists refetch.
  if (resource === "collab") {
    applyCollabEvent(qc, env.type, d);
    return;
  }

  switch (env.type) {
    // A stand-in for an event of a private team's ticket, or of one scoped to people
    // (Collab mail): it only keeps seq contiguous.
    case "event.hidden":
      return;
    case "ticket.hidden": {
      const id = d.ticket_id as string | undefined;
      const team = d.team_id as string | undefined;
      if (!id) return;
      // Its members (and its assignee) keep seeing it; the team channel brings its updates.
      if (team && (ctx.privateTeams?.().includes(team) || teamsOf(qc.getQueryData<Team[]>(qk.teams), ctx.me).includes(team))) return;
      if (cachedTicket(qc, id)?.assignee?.id === ctx.me) return;
      forgetTicket(qc, id);
      markHidden(id);
      refreshCounts(qc);
      return;
    }
    case "ticket.created":
    case "ticket.updated": {
      const t = d.ticket as Ticket;
      if (!t) return;
      unmarkHidden(t.id);
      patchTicketEverywhere(qc, t, ctx.me);
      refreshCounts(qc);
      if (env.type === "ticket.created" && !ctx.viaUser) ctx.onTicketCreated?.(t);
      return;
    }
    case "ticket.deleted": {
      const id = d.ticket_id as string;
      removeTicketEverywhere(qc, id);
      qc.removeQueries({ queryKey: qk.ticket(id) });
      refreshCounts(qc);
      return;
    }
    case "message.created":
    case "message.updated": {
      const m = d.message as Message;
      const tid = (d.ticket_id as string) ?? m?.ticket_id;
      if (!m || !tid) return;
      mergeMessage(qc, tid, m);
      // A newer message means Ollie's suggestion no longer answers the latest one.
      if (env.type === "message.created" && m.kind === "reply") {
        const s = qc.getQueryData<SuggestedReply | null>(qk.suggestedReply(tid));
        if (s && s.message_id !== m.id) qc.setQueryData(qk.suggestedReply(tid), null);
      }
      return;
    }
    // message.withdrawn: a chat reply left the visitor's widget. The team keeps it (a marker
    // or a note, which message.updated brings), so there is nothing to drop here.
    case "message.deleted": {
      const tid = d.ticket_id as string | undefined;
      const id = d.message_id as string | undefined;
      if (!tid || !id) return;
      qc.setQueryData<Message[]>(qk.messages(tid), (old) => old?.filter((m) => m.id !== id));
      return;
    }
    case "ai_suggested_reply.created":
    case "ai_suggested_reply.updated": {
      const s = d.suggested_reply as SuggestedReply | undefined;
      const tid = (d.ticket_id as string) ?? s?.ticket_id;
      if (!s || !tid) return;
      qc.setQueryData<SuggestedReply | null>(qk.suggestedReply(tid), (old) => {
        if (s.status === "pending") return s;
        return old && old.id !== s.id ? old : null;
      });
      return;
    }
    case "ticket_draft.updated": {
      const tid = d.ticket_id as string | undefined;
      if (!tid) return;
      qc.setQueryData<TicketDraft | null>(qk.ticketDraft(tid), (d.draft as TicketDraft | null) ?? null);
      return;
    }
    case "thread.created":
    case "thread.updated": {
      const th = d.thread as Thread | undefined;
      const tid = (d.ticket_id as string) ?? th?.ticket_id;
      if (th && tid) upsertInList(qc, qk.threads(tid), th);
      return;
    }
    case "activity.created": {
      const a = d.activity as Activity;
      const tid = (d.ticket_id as string) ?? a?.ticket_id;
      if (a && tid) appendUnique(qc, qk.activities(tid), a);
      return;
    }
    case "tag.merged": {
      removeFromList(qc, qk.tags, d.tag_id as string);
      if (d.into) upsertInList(qc, qk.tags, d.into as { id: string });
      void qc.invalidateQueries({ queryKey: qk.ticketLists });
      void qc.invalidateQueries({ queryKey: qk.contactLists });
      void qc.invalidateQueries({ queryKey: qk.companyLists });
      return;
    }
    case "billing.updated": {
      if (d && typeof d === "object" && "plan" in d) qc.setQueryData(qk.billing, d);
      else void qc.invalidateQueries({ queryKey: qk.billing });
      // A purchase or recharge brings a new invoice.
      void qc.invalidateQueries({ queryKey: qk.billingInvoices });
      return;
    }
    case "notification.created": {
      // Notifications arrive on user:<me> only; a replay carries the recipient in topics.
      if (!ctx.viaUser && !env.topics?.includes(`user:${ctx.me}`)) return;
      const n = d.notification as Notification;
      if (!n) return;
      qc.setQueryData<Notification[]>(qk.notifications, (old) =>
        old ? [n, ...old.filter((x) => x.id !== n.id)] : old,
      );
      ctx.onNotification?.(n);
      return;
    }
  }

  if (resource.startsWith("ai_agent_")) return void applyAgentQualityEvent(qc, env.type, d);

  if (resource === "ai_knowledge") {
    if (verb === "deleted") {
      const id = (d.source_id ?? d.knowledge_source_id ?? d.id) as string | undefined;
      if (id) {
        removeFromList(qc, qk.knowledge, id);
        qc.removeQueries({ queryKey: qk.knowledgeDocs(id) });
      }
    } else {
      const src = d.source as KnowledgeSource | undefined;
      if (!src) return void qc.invalidateQueries({ queryKey: qk.knowledge, exact: true });
      const before = qc.getQueryData<KnowledgeSource[]>(qk.knowledge)?.find((x) => x.id === src.id);
      upsertInList(qc, qk.knowledge, src, { prepend: true });
      // A finished sync changes the documents.
      if (before && before.status !== src.status && src.status === "ready") void qc.invalidateQueries({ queryKey: qk.knowledgeDocs(src.id) });
    }
    return;
  }

  if (resource === "ai_mcp_server") {
    if (verb === "deleted") {
      const id = (d.mcp_server_id ?? d.id) as string | undefined;
      if (id) removeFromList(qc, qk.mcpServers, id);
    } else {
      const srv = d.mcp_server as McpServer | undefined;
      if (srv) upsertInList(qc, qk.mcpServers, srv);
      else void qc.invalidateQueries({ queryKey: qk.mcpServers });
    }
    return;
  }

  if (resource === "ticket_link") {
    const tid = d.ticket_id as string;
    if (verb === "deleted") removeFromList(qc, qk.ticketLinks(tid), d.link_id as string);
    else if (d.link) appendUnique(qc, qk.ticketLinks(tid), d.link as TicketLink);
    return;
  }

  if (resource === "contact") {
    if (verb === "deleted") {
      const id = d.contact_id as string;
      qc.removeQueries({ queryKey: qk.contact(id) });
      patchContactLists(qc, null, id);
    } else {
      const c = d.contact as Contact;
      qc.setQueryData(qk.contact(c.id), c);
      if (verb === "created") void qc.invalidateQueries({ queryKey: qk.contactLists });
      else patchContactLists(qc, c);
    }
    return;
  }

  if (resource === "company") {
    void qc.invalidateQueries({ queryKey: qk.companyLists });
    if (verb !== "deleted") qc.setQueryData(qk.company((d.company as { id: string }).id), d.company);
    return;
  }

  if (resource === "workspace") {
    const w = d.workspace as Workspace | undefined;
    if (w) {
      qc.setQueryData(qk.workspace, w);
      updateWorkspace(w);
    }
    void qc.invalidateQueries({ queryKey: qk.assignment });
    void qc.invalidateQueries({ queryKey: qk.businessHours });
    return;
  }

  if (resource === "member") {
    if (verb === "removed") {
      const uid = d.user_id as string;
      qc.setQueryData<Member[]>(qk.members, (old) => old?.filter((m) => m.user.id !== uid));
      if (uid === ctx.me) ctx.onSelfMembershipChanged?.();
    } else {
      const m = d.member as Member;
      const before = qc.getQueryData<Member[]>(qk.members)?.find((x) => x.user.id === m.user.id);
      qc.setQueryData<Member[]>(qk.members, (old) => {
        if (!old) return old;
        const i = old.findIndex((x) => x.user.id === m.user.id);
        return i >= 0 ? old.map((x, j) => (j === i ? m : x)) : [...old, m];
      });
      // Assignees embed the member's picture.
      if (before && before.user.avatar_url !== m.user.avatar_url) void qc.invalidateQueries({ queryKey: qk.of("tickets") });
      if (m.user.id === ctx.me) {
        const roleChanged = verb !== "updated" || !before || before.role !== m.role;
        if (roleChanged) ctx.onSelfMembershipChanged?.();
        else {
          const u = useSession.getState().user;
          if (u && u.avatar_url !== m.user.avatar_url) useSession.setState({ user: { ...u, avatar_url: m.user.avatar_url, avatar_source: m.user.avatar_source } });
        }
      }
    }
    return;
  }

  if (resource === "wiki_page") {
    applyWikiEvent(qc, verb, d);
    return;
  }

  if (resource === "kb_article") {
    void qc.invalidateQueries({ queryKey: qk.kbArticleLists });
    if (verb === "deleted") qc.removeQueries({ queryKey: qk.kbArticle(d.kb_article_id as string) });
    else {
      const a = d.kb_article as { id: string };
      qc.setQueryData(qk.kbArticle(a.id), a);
    }
    void qc.invalidateQueries({ queryKey: qk.kbCollections });
    return;
  }

  if (resource === "changelog_entry") {
    if (verb === "deleted") qc.removeQueries({ queryKey: qk.changelogEntry(d.changelog_entry_id as string) });
    else {
      const e = d.changelog_entry as { id: string } | undefined;
      if (e) qc.setQueryData(qk.changelogEntry(e.id), e);
    }
    void qc.invalidateQueries({ queryKey: qk.changelogEntryLists });
    void qc.invalidateQueries({ queryKey: qk.changelogCategories });
    return;
  }

  if (resource === "team") {
    // Joining or leaving a team changes what "My teams" and private tickets hold.
    const before = teamsOf(qc.getQueryData<Team[]>(qk.teams), ctx.me).join(",");
    if (verb === "deleted") removeFromList(qc, qk.teams, d.team_id as string);
    else if (d.team) upsertInList(qc, qk.teams, d.team as Team);
    const after = teamsOf(qc.getQueryData<Team[]>(qk.teams), ctx.me).join(",");
    if (before !== after || verb === "deleted") void qc.invalidateQueries({ queryKey: qk.of("tickets") });
    // Addresses, widgets and aliases of a deleted team moved with it.
    if (verb === "deleted") {
      void qc.invalidateQueries({ queryKey: qk.mailboxes });
      void qc.invalidateQueries({ queryKey: qk.widgets });
    }
    refreshCounts(qc);
    return;
  }

  if (resource === "import") {
    const x = d.import as Import | undefined;
    if (!x) return;
    const before = qc.getQueryData<Import[]>(qk.imports)?.find((i) => i.id === x.id);
    upsertInList(qc, qk.imports, x, { prepend: true });
    // Imported history is quiet, one event per import rather than per ticket: lists catch
    // up when what it wrote moves, at most every few seconds (the last refetch comes after the last event).
    if (!before || landed(before) !== landed(x) || before.status !== x.status) refreshImported(qc);
    return;
  }

  // A mailbox event carries the members' view, without the config admins read: the list
  // refetches so an admin's copy keeps it.
  if (resource === "mailbox") {
    void qc.invalidateQueries({ queryKey: qk.mailboxes });
    return;
  }

  const simple = simpleLists[resource];
  if (simple) {
    if (verb === "deleted") removeFromList(qc, simple.key(), d[`${simple.field}_id`] as string);
    else {
      const row = d[simple.field] as { id: string } | undefined;
      if (row) upsertInList(qc, simple.key(), row);
    }
    if (resource === "support_channel_connection" && verb === "deleted") {
      const id = d.support_channel_connection_id as string;
      qc.setQueryData<{ connection_id: string }[]>(qk.supportChannelDestinations, (old) => old?.filter((x) => x.connection_id !== id));
    }
    if (resource === "view") refreshCounts(qc);
    // Tickets name their brand and widget; a removed one leaves lists to refetch.
    if ((resource === "brand" || resource === "widget") && verb === "deleted") void qc.invalidateQueries({ queryKey: qk.ticketLists });
    // Records embed label names and colours, and field changes can clear stored values.
    if ((resource === "tag" || resource === "tag_group") && verb !== "created") void qc.invalidateQueries({ queryKey: qk.ticketLists });
    if (resource === "tag_group") void qc.invalidateQueries({ queryKey: qk.tags });
    if (resource === "custom_field" && verb !== "created") {
      void qc.invalidateQueries({ queryKey: qk.ticketLists });
      void qc.invalidateQueries({ queryKey: qk.of("tickets", "detail") });
    }
    return;
  }
}

function splitType(type: string): [string, string] {
  const i = type.lastIndexOf(".");
  return i < 0 ? [type, ""] : [type.slice(0, i), type.slice(i + 1)];
}
