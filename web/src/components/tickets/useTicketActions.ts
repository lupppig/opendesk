// Ticket mutations. Property edits (status, priority, assignee, team, tags,
// spam, snooze) are predictable, so they patch every cache at once and roll
// back on failure. Merge and delete wait for the server.

import { useMutation, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api/endpoints";
import type { Mailbox, Member, SuggestionField, Tag, TagGroup, TagRef, Team, Ticket, TicketPatch } from "@/lib/api/types";
import { withoutSuggestion } from "@/lib/decisions";
import { qk } from "@/lib/queries";
import { cachedTicket, forgetTicket, patchTicketEverywhere, removeTicketEverywhere, teamsOf } from "@/lib/ticketCache";
import { markHidden } from "@/lib/realtime/store";
import { useSession } from "@/lib/session";
import { toast } from "@/components/ui/toast";
import { isApiError } from "@/lib/api/client";
import { focusField, isUnknownLabel, requiredFieldKeys } from "./fields";
import { replyAddressAfterMove } from "./teams";

/** In a private team this person is not on (and not assigned to them): out of their view. */
export function hiddenFrom(qc: QueryClient, t: Ticket, me: string) {
  const teams = qc.getQueryData<Team[]>(qk.teams);
  const team = teams?.find((x) => x.id === t.team_id);
  return !!team?.private && !teamsOf(teams, me).includes(team.id) && t.assignee?.id !== me;
}

/** Apply a ticket the server answered, or let it go when it left this person's view. */
function settle(qc: QueryClient, t: Ticket, me: string) {
  if (hiddenFrom(qc, t, me)) {
    forgetTicket(qc, t.id);
    markHidden(t.id);
  } else patchTicketEverywhere(qc, t, me);
}

/** Apply a PATCH body to a ticket locally, resolving ids to the refs the server would return. */
export function applyPatch(qc: QueryClient, t: Ticket, p: TicketPatch): Ticket {
  const next: Ticket = { ...t, updated_at: new Date().toISOString() };
  if (p.subject !== undefined) next.subject = p.subject;
  if (p.status !== undefined) {
    next.status = p.status;
    if (p.status === "solved" && !t.solved_at) next.solved_at = new Date().toISOString();
  }
  if (p.priority !== undefined) next.priority = p.priority;
  if (p.severity !== undefined) next.severity = p.severity;
  if (p.spam !== undefined) next.spam = p.spam;
  if (p.snoozed_until !== undefined) next.snoozed_until = p.snoozed_until;
  if (p.team_id !== undefined && p.team_id !== t.team_id) {
    // The reply address follows the team when the old one was the old team's (contracts/teams.md).
    const sw = replyAddressAfterMove(t, p.team_id, qc.getQueryData<Mailbox[]>(qk.mailboxes) ?? []);
    if (sw && p.mailbox_id === undefined) next.mailbox_id = sw.to.id;
    next.team_id = p.team_id;
  }
  if (p.mailbox_id !== undefined) next.mailbox_id = p.mailbox_id;
  if (p.brand_id !== undefined) next.brand_id = p.brand_id;
  if (p.assignee_id !== undefined) {
    if (p.assignee_id === null) next.assignee = null;
    else {
      const m = qc.getQueryData<Member[]>(qk.members)?.find((x) => x.user.id === p.assignee_id);
      next.assignee = m ? m.user : t.assignee;
    }
  }
  const fp = p.fields;
  if (fp) {
    const cur = { ...(t.fields ?? {}) };
    for (const [k, v] of Object.entries(fp)) {
      if (v === null || v === "" || (Array.isArray(v) && !v.length)) delete cur[k];
      else cur[k] = v;
    }
    next.fields = cur;
  }
  if (p.tags !== undefined || p.add_tags || p.remove_tags) {
    const known = qc.getQueryData<Tag[]>(qk.tags) ?? [];
    const groups = qc.getQueryData<TagGroup[]>(qk.tagGroups) ?? [];
    const same = (a: string, b: string) => a.toLowerCase() === b.toLowerCase();
    const toRef = (name: string): TagRef => {
      const k = known.find((x) => same(x.name, name));
      return k ? { id: k.id, name: k.name, color: k.color, group_id: k.group_id } : { id: `tmp-${name}`, name, color: "gray", group_id: null };
    };
    let names = p.tags ?? t.tags.map((x) => x.name);
    if (p.add_tags) names = [...names, ...p.add_tags.filter((n) => !names.some((x) => same(x, n)))];
    if (p.remove_tags) names = names.filter((n) => !p.remove_tags!.some((x) => same(x, n)));
    // Exclusive groups keep the label named last (as the server does).
    const refs = names.map(toRef);
    next.tags = refs.filter((r, i) => {
      const g = r.group_id && groups.find((x) => x.id === r.group_id);
      return !g || !g.exclusive || !refs.slice(i + 1).some((o) => o.group_id === r.group_id);
    });
  }
  // Setting a field drops Ollie's pending suggestion for it (as the server does).
  const sugg = t.custom?.ai_suggestions;
  if (sugg && typeof sugg === "object") {
    const drop = new Set<string>();
    if (p.priority !== undefined) drop.add("priority");
    if (p.severity !== undefined) drop.add("severity");
    if (p.team_id !== undefined) drop.add("team");
    if (p.spam !== undefined) drop.add("spam");
    if (p.tags !== undefined || p.add_tags || p.remove_tags) drop.add("labels");
    const rest = Object.fromEntries(Object.entries(sugg as Record<string, unknown>).filter(([k]) => !drop.has(k)));
    if (Object.keys(rest).length !== Object.keys(sugg).length) next.custom = { ...next.custom, ai_suggestions: rest };
  }
  return next;
}

export function useUpdateTicket() {
  const qc = useQueryClient();
  const me = useSession((s) => s.user?.id ?? "");
  return useMutation({
    mutationFn: ({ id, patch }: { id: string; patch: TicketPatch; quiet?: boolean }) => api.tickets.update(id, patch),
    onMutate: async ({ id, patch }) => {
      await qc.cancelQueries({ queryKey: qk.ticket(id) });
      const prev = cachedTicket(qc, id);
      if (prev) patchTicketEverywhere(qc, applyPatch(qc, prev, patch), me);
      return { prev };
    },
    onError: (e, _v, ctx) => {
      if (ctx?.prev) patchTicketEverywhere(qc, ctx.prev, me);
      // Solving with an empty required field: open that field instead of a bare error.
      const missing = requiredFieldKeys(e);
      if (missing.length) {
        focusField(missing[0]);
        toast.error(isApiError(e) && e.detail ? e.detail : "Fill in the required fields before solving");
        return;
      }
      if (isUnknownLabel(e)) {
        toast.error(isApiError(e) && e.detail ? e.detail : "Only admins can create labels. Pick an existing one.");
        return;
      }
      toast.error(e, "Couldn't update the ticket");
    },
    onSuccess: (t) => {
      settle(qc, t, me);
      if (t.tags.some((x) => !qc.getQueryData<Tag[]>(qk.tags)?.some((k) => k.id === x.id))) void qc.invalidateQueries({ queryKey: qk.tags });
    },
    onSettled: () => void qc.invalidateQueries({ queryKey: qk.counts }),
  });
}

export function useBulkUpdate() {
  const qc = useQueryClient();
  const me = useSession((s) => s.user?.id ?? "");
  return useMutation({
    mutationFn: (b: { ids: string[]; patch?: TicketPatch; delete?: true }) => api.tickets.bulk(b),
    onMutate: ({ ids, patch, delete: del }) => {
      const prev = ids.map((id) => cachedTicket(qc, id)).filter((t): t is Ticket => !!t);
      for (const t of prev) {
        if (del) removeTicketEverywhere(qc, t.id);
        else if (patch) {
          // Bulk tags add to the existing set (see contracts); single edits replace.
          const p = patch.tags ? { ...patch, add_tags: [...(patch.add_tags ?? []), ...patch.tags], tags: undefined } : patch;
          patchTicketEverywhere(qc, applyPatch(qc, t, p), me);
        }
      }
      return { prev };
    },
    onSuccess: (r) => {
      for (const t of r?.data ?? []) settle(qc, t, me);
    },
    onError: (e, _v, ctx) => {
      ctx?.prev.forEach((t) => patchTicketEverywhere(qc, t, me));
      if (isUnknownLabel(e)) return void toast.error(isApiError(e) && e.detail ? e.detail : "Only admins can create labels. Pick an existing one.");
      const missing = requiredFieldKeys(e);
      if (missing.length) return void toast.error(isApiError(e) && e.detail ? e.detail : "Some tickets have required fields to fill in before solving");
      toast.error(e, "Couldn't update those tickets");
    },
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: qk.counts });
      void qc.invalidateQueries({ queryKey: qk.ticketLists });
    },
  });
}

export function useDeleteTicket() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.tickets.delete(id),
    onSuccess: (_r, id) => {
      removeTicketEverywhere(qc, id);
      qc.removeQueries({ queryKey: qk.ticket(id) });
      void qc.invalidateQueries({ queryKey: qk.counts });
    },
  });
}

export function useMergeTicket() {
  const qc = useQueryClient();
  const me = useSession((s) => s.user?.id ?? "");
  return useMutation({
    mutationFn: ({ id, into }: { id: string; into: string }) => api.tickets.merge(id, into),
    onSuccess: (target, { id }) => {
      removeTicketEverywhere(qc, id);
      if (target?.id) {
        patchTicketEverywhere(qc, target, me);
        void qc.invalidateQueries({ queryKey: qk.messages(target.id) });
        void qc.invalidateQueries({ queryKey: qk.activities(target.id) });
        void qc.invalidateQueries({ queryKey: qk.threads(target.id) });
      }
      void qc.invalidateQueries({ queryKey: qk.counts });
    },
  });
}

export function teamName(teams: Team[] | undefined, id: string | null) {
  return id ? teams?.find((t) => t.id === id)?.name ?? "Team" : null;
}

/** Dismiss Ollie's suggestion for a field, optimistically. */
export function useDismissSuggestion() {
  const qc = useQueryClient();
  const me = useSession((s) => s.user?.id ?? "");
  return useMutation({
    mutationFn: ({ id, field }: { id: string; field: SuggestionField }) => api.ai.dismissSuggestion(id, field),
    onMutate: ({ id, field }) => {
      const prev = cachedTicket(qc, id);
      if (prev) patchTicketEverywhere(qc, withoutSuggestion(prev, field), me);
      return { prev };
    },
    onError: (e, _v, ctx) => {
      if (ctx?.prev) patchTicketEverywhere(qc, ctx.prev, me);
      toast.error(e, "Couldn't dismiss the suggestion");
    },
  });
}
