import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo } from "react";
import type { TicketListQuery } from "@/lib/api/endpoints";
import type { Ticket } from "@/lib/api/types";
import { folderById, type FolderId } from "@/lib/folders";
import { ticketMatches } from "@/lib/ticketCache";
import { q } from "@/lib/queries";
import { realtime } from "@/lib/realtime/manager";
import { useRealtime } from "@/lib/realtime/store";
import { useSession } from "@/lib/session";
import { useNow } from "@/lib/useNow";
import { useTeamsCtx } from "./teams";
import { chatWait, handlerOf, useAgentOn, type ChatWait } from "./waiting";

// Live chat visitors waiting with nobody on it, for the whole app: the sidebar, the
// alerts and the tab title read the same list, longest wait first. Presence on every
// waiting chat is followed, so one a teammate (or you) picked up drops out at once.

const QUERY = { status: ["open", "pending"], assignee: "none", channel: "chat" } as const;

export type UrgentChat = { ticket: Ticket; wait: ChatWait };

/** `watch`: follow presence on the waiting chats; only the app-wide alerts hook sets it. */
export function useUrgentChats(enabled = true, watch = false) {
  const agentOn = useAgentOn();
  const list = useInfiniteQuery({ ...q.tickets({ ...QUERY, status: [...QUERY.status] }), enabled });
  const tickets = useMemo(() => list.data?.pages.flatMap((p) => p.data) ?? [], [list.data]);
  const now = useNow(tickets.length > 0);
  const presence = useRealtime((s) => s.presence);
  const active = useRealtime((s) => s.active);
  const me = useSession((s) => s.user?.id);

  const waitingIds = useMemo(() => tickets.filter((x) => !x.assignee && x.channel === "chat").map((x) => x.id).join(","), [tickets]);
  useEffect(() => {
    if (!enabled || !watch) return;
    realtime()?.setWatched(waitingIds ? waitingIds.split(",") : [], "waiting");
    return () => realtime()?.setWatched([], "waiting");
  }, [waitingIds, enabled, watch]);

  const urgent = useMemo(
    () =>
      tickets
        .filter((x) => !x.assignee)
        .map((x) => ({ ticket: x, wait: chatWait(x, agentOn, now, handlerOf(x, presence[x.id], me, active)) }))
        .filter((x): x is UrgentChat => x.wait?.state === "urgent")
        .sort((a, b) => a.wait.since - b.wait.since),
    [tickets, agentOn, now, presence, me, active],
  );
  return { urgent, now, ready: list.isSuccess };
}

/** A sidebar row that lists tickets: an inbox folder, a team ("mine": the caller's teams) or a view. */
export type WaitingScope = { kind: "ticket_folder"; id: FolderId } | { kind: "team"; id: string } | { kind: "view"; id: string };

/**
 * How many waiting chats each row would list, by the same rules the lists use, so a team,
 * a view or a favorite shows its own red count, not only Unassigned.
 */
export function useWaitingFor(): (scope: WaitingScope) => number {
  const { urgent } = useUrgentChats();
  const { data: views } = useQuery(q.views());
  const teams = useTeamsCtx();
  const me = useSession((s) => s.user?.id) ?? "";
  return useCallback(
    (scope: WaitingScope) => {
      if (!urgent.length) return 0;
      const mine = [...teams.myTeams];
      const query: TicketListQuery | null =
        scope.kind === "ticket_folder"
          ? (folderById(scope.id)?.query ?? null)
          : scope.kind === "team"
            ? { team_id: scope.id === "mine" ? "mine" : scope.id }
            : { view_id: scope.id };
      if (!query) return 0;
      return urgent.filter((x) => ticketMatches(query, x.ticket, me, views, mine) === true).length;
    },
    [urgent, views, teams.myTeams, me],
  );
}
