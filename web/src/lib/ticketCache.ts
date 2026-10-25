// Keeps every cached ticket list honest when a ticket changes elsewhere
// (realtime events, optimistic edits): a ticket that now matches a list's
// filters is inserted in sort order, one that no longer does is removed.

import type { InfiniteData, QueryClient } from "@tanstack/react-query";
import type { TicketListQuery } from "./api/endpoints";
import type { Page, Priority, Severity, Team, Ticket, TicketFilters, View } from "./api/types";
import { keyBody, qk } from "./queries";

export type TicketPages = InfiniteData<Page<Ticket>, string | null>;

const priorityRank: Record<Priority, number> = { low: 0, normal: 1, high: 2, urgent: 3 };
/** sev1 sorts first in descending order; no severity sorts last. */
const severityRank = (s: Severity | null | undefined) => (s ? { sev4: 1, sev3: 2, sev2: 3, sev1: 4 }[s] : 0);

/** Resolve a list query (which may name a view) to plain filters. */
export function effectiveFilters(query: TicketListQuery, views: View[] | undefined): (TicketListQuery & TicketFilters) | null {
  if (query.view_id) {
    const v = views?.find((x) => x.id === query.view_id);
    if (!v) return null;
    return { ...v.filters, ...query, view_id: undefined };
  }
  return query;
}

/** The teams someone is on (any role). */
export function teamsOf(teams: Team[] | undefined, me: string): string[] {
  return (teams ?? []).filter((x) => x.member_ids.includes(me) || x.members?.some((m) => m.user_id === me)).map((x) => x.id);
}

/** A team filter: ids, "mine" (the caller's teams) and "none", comma separated. */
export function teamFilterMatches(filter: string, teamId: string | null, myTeams: string[] | null): boolean | null {
  const parts = filter.split(",").filter(Boolean);
  if (!parts.length) return true;
  for (const p of parts) {
    if (p === "none" && !teamId) return true;
    if (p === "mine") {
      if (myTeams === null) return null;
      if (teamId && myTeams.includes(teamId)) return true;
    }
    if (p === teamId) return true;
  }
  return false;
}

/** Whether a ticket belongs in a list. `null` means "can't tell", so refetch. */
export function ticketMatches(query: TicketListQuery, t: Ticket, me: string, views?: View[], myTeams: string[] | null = null): boolean | null {
  const f = effectiveFilters(query, views);
  if (!f) return null;
  if (f.q || f.contact_id) {
    if (f.contact_id && t.contact?.id !== f.contact_id) return false;
    // Full-text matching is the server's job.
    if (f.q) return null;
  }
  const wantSpam = f.spam ?? false;
  if (t.spam !== wantSpam) return false;
  if (f.status?.length && !f.status.includes(t.status)) return false;
  if (f.priority?.length && !f.priority.includes(t.priority)) return false;
  if (f.severity?.length && !f.severity.includes(t.severity ?? "none")) return false;
  if (f.assignee) {
    if (f.assignee === "me" && t.assignee?.id !== me) return false;
    if (f.assignee === "none" && t.assignee) return false;
    if (f.assignee !== "me" && f.assignee !== "none" && t.assignee?.id !== f.assignee) return false;
  }
  if (f.team_id) {
    const m = teamFilterMatches(f.team_id, t.team_id, myTeams);
    if (m === null) return null;
    if (!m) return false;
  }
  if (f.mailbox_id && t.mailbox_id !== f.mailbox_id) return false;
  if (f.received_at && (t.received_at ?? "").toLowerCase() !== f.received_at.toLowerCase()) return false;
  if (f.brand_id && t.brand_id !== f.brand_id) return false;
  if (f.widget_id && t.widget_id !== f.widget_id) return false;
  if (f.channel && t.channel !== f.channel) return false;
  if (f.tag && !t.tags.some((x) => x.name.toLowerCase() === f.tag!.toLowerCase())) return false;
  return true;
}

function dueOf(t: Ticket): number {
  const d = t.sla?.first_response_due_at && !t.first_responded_at ? t.sla.first_response_due_at : t.sla?.resolution_due_at;
  return d ? Date.parse(d) : Number.POSITIVE_INFINITY;
}

export function compareTickets(query: TicketListQuery) {
  const sort = query.sort ?? "last_message_at";
  const dir = (query.order ?? (sort === "due" ? "asc" : "desc")) === "asc" ? 1 : -1;
  return (a: Ticket, b: Ticket) => {
    let d: number;
    if (sort === "priority") d = priorityRank[a.priority] - priorityRank[b.priority];
    else if (sort === "severity") d = severityRank(a.severity) - severityRank(b.severity);
    else if (sort === "due") d = dueOf(a) - dueOf(b);
    else if (sort === "created_at") d = Date.parse(a.created_at) - Date.parse(b.created_at);
    else d = Date.parse(a.last_message_at) - Date.parse(b.last_message_at);
    if (d === 0) d = a.number - b.number;
    return d * dir;
  };
}

/** Insert or replace a ticket in one list's pages, keeping sort order. */
export function placeTicket(data: TicketPages, t: Ticket, query: TicketListQuery): TicketPages {
  const cmp = compareTickets(query);
  const without = data.pages.map((p) => ({ ...p, data: p.data.filter((x) => x.id !== t.id) }));
  const all = without.flatMap((p) => p.data);
  const hasMore = data.pages[data.pages.length - 1]?.next_cursor != null;
  // Past the loaded window, a ticket that sorts last belongs to a page we don't have.
  const last = all[all.length - 1];
  if (hasMore && last && cmp(t, last) > 0) return { ...data, pages: without };
  let placed = false;
  const pages = without.map((p) => {
    if (placed) return p;
    const idx = p.data.findIndex((x) => cmp(t, x) < 0);
    if (idx >= 0) {
      placed = true;
      const next = p.data.slice();
      next.splice(idx, 0, t);
      return { ...p, data: next };
    }
    return p;
  });
  if (!placed) {
    const lastPage = pages[pages.length - 1];
    if (lastPage) pages[pages.length - 1] = { ...lastPage, data: [...lastPage.data, t] };
    else pages.push({ data: [t], next_cursor: null });
  }
  return { ...data, pages };
}

export function dropTicket(data: TicketPages, id: string): TicketPages {
  return { ...data, pages: data.pages.map((p) => ({ ...p, data: p.data.filter((x) => x.id !== id) })) };
}

export function findTicket(data: TicketPages | undefined, id: string): Ticket | undefined {
  for (const p of data?.pages ?? []) {
    const t = p.data.find((x) => x.id === id);
    if (t) return t;
  }
  return undefined;
}

/** Apply a changed ticket to its detail cache and every cached list. */
export function patchTicketEverywhere(qc: QueryClient, t: Ticket, me: string) {
  qc.setQueryData(qk.ticket(t.id), t);
  // Detail views opened by number (/inbox/mine/1072) cache under the number.
  for (const alias of [String(t.number), `#${t.number}`]) {
    qc.setQueryData(qk.ticket(alias), (old: Ticket | undefined) => (old ? t : old));
  }
  const views = qc.getQueryData<View[]>(qk.views);
  const teams = qc.getQueryData<Team[]>(qk.teams);
  const myTeams = teams ? teamsOf(teams, me) : null;
  for (const [key, data] of qc.getQueriesData<TicketPages>({ queryKey: qk.ticketLists })) {
    if (!data) continue;
    const query = (keyBody(key)[2] ?? {}) as TicketListQuery;
    const match = ticketMatches(query, t, me, views, myTeams);
    if (match === null) {
      if (findTicket(data, t.id)) qc.setQueryData<TicketPages>(key, placeOrReplace(data, t));
      else qc.invalidateQueries({ queryKey: key, exact: true });
      continue;
    }
    qc.setQueryData<TicketPages>(key, match ? placeTicket(data, t, query) : dropTicket(data, t.id));
  }
}

function placeOrReplace(data: TicketPages, t: Ticket): TicketPages {
  return { ...data, pages: data.pages.map((p) => ({ ...p, data: p.data.map((x) => (x.id === t.id ? t : x)) })) };
}

export function removeTicketEverywhere(qc: QueryClient, id: string) {
  for (const [key, data] of qc.getQueriesData<TicketPages>({ queryKey: qk.ticketLists })) {
    if (data) qc.setQueryData<TicketPages>(key, dropTicket(data, id));
  }
}

/**
 * A ticket this person may no longer see (it moved to a private team they are not on):
 * out of every list, and its detail, messages and activities dropped. Detail caches
 * opened by number are found through the cached ticket.
 */
export function forgetTicket(qc: QueryClient, id: string) {
  const known = cachedTicket(qc, id);
  removeTicketEverywhere(qc, id);
  const keys = [id, ...(known ? [String(known.number), `#${known.number}`] : [])];
  for (const k of keys) qc.removeQueries({ queryKey: qk.ticket(k), exact: true });
  qc.removeQueries({ queryKey: qk.messages(id), exact: true });
  qc.removeQueries({ queryKey: qk.activities(id), exact: true });
  qc.removeQueries({ queryKey: qk.suggestedReply(id), exact: true });
  qc.removeQueries({ queryKey: qk.ticketDraft(id), exact: true });
  qc.removeQueries({ queryKey: qk.ticketLinks(id), exact: true });
  qc.removeQueries({ queryKey: qk.threads(id), exact: true });
}

/** Every loaded ticket across lists, for optimistic bulk edits. */
export function cachedTicket(qc: QueryClient, id: string): Ticket | undefined {
  const d = qc.getQueryData<Ticket>(qk.ticket(id));
  if (d) return d;
  for (const [, data] of qc.getQueriesData<TicketPages>({ queryKey: qk.ticketLists })) {
    const t = findTicket(data, id);
    if (t) return t;
  }
  return undefined;
}
