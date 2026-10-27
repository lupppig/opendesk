import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { CircleCheck, CircleDashed, Clock, Inbox, Layers, ShieldAlert, User, Users } from "lucide-react";
import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore, type CSSProperties, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { Ticket } from "@/lib/api/types";
import { folderById, listQuery, teamQuery, type FolderId, type InboxSearch } from "@/lib/folders";
import { q } from "@/lib/queries";
import { useHotkeys } from "@/lib/hotkeys";
import { realtime } from "@/lib/realtime/manager";
import { cn } from "@/lib/cn";
import { EmptyState } from "@/components/ui/empty";
import { springs } from "@/lib/motion";
import { snoozeOptions } from "@/components/tickets/detail/SnoozeMenu";
import { InboxEmpty } from "./InboxEmpty";
import { Skeleton, SkeletonRows } from "@/components/ui/skeleton";
import { Button } from "@/components/ui/button";
import { ResizeHandle } from "@/components/ui/resize-handle";
import { toast } from "@/components/ui/toast";
import { TicketRow, type QuickAction } from "@/components/tickets/TicketRow";
import { BulkBar } from "@/components/tickets/BulkBar";
import { ListToolbar, type InboxLayout } from "@/components/tickets/ListToolbar";
import { TicketPreview } from "@/components/tickets/TicketPreview";
import { SummaryStrip, loadedCounts, stripCounts } from "@/components/tickets/SummaryStrip";
import { WaitingBanner, chatWait, useAgentOn } from "@/components/tickets/waiting";
import { useUpdateTicket } from "@/components/tickets/useTicketActions";
import { intent } from "@/components/tickets/motion";
import { focusField, missingForSolve, useTicketFields } from "@/components/tickets/fields";
import { atLeast, useRole, useSession } from "@/lib/session";
import { paneCss, setLiveWidth, usePaneWidth } from "@/lib/panes";
import { useDocumentTitle } from "@/app/RootLayout";
import { TeamDot, useTeamsCtx } from "@/components/tickets/teams";
import { TeamTabs } from "./TeamTabs";

const TicketDetail = lazy(() => import("@/components/tickets/detail/TicketDetail"));

const folderIcon: Record<FolderId, ReactNode> = {
  unassigned: <CircleDashed />,
  mine: <User />,
  open: <Inbox />,
  pending: <Clock />,
  solved: <CircleCheck />,
  spam: <ShieldAlert />,
};


// Wide screens show the list beside a preview of the focused ticket.
// Phones always get the stacked rows.
const LAYOUT_KEY = "od-inbox-layout";
function useMedia(query: string) {
  return useSyncExternalStore(
    (cb) => {
      const m = window.matchMedia(query);
      m.addEventListener("change", cb);
      return () => m.removeEventListener("change", cb);
    },
    () => window.matchMedia(query).matches,
    () => false,
  );
}
function readLayout(): InboxLayout {
  try {
    return localStorage.getItem(LAYOUT_KEY) === "list" ? "list" : "split";
  } catch {
    return "split";
  }
}

/** One soft highlight that glides between rows as the focus moves (j/k). */
function FocusGlide({ listRef, id, active, rows }: { listRef: React.RefObject<HTMLDivElement | null>; id: string | undefined; active: boolean; rows: unknown }) {
  const reduce = useReducedMotion();
  const [box, setBox] = useState<{ y: number; h: number } | null>(null);
  useEffect(() => {
    const list = listRef.current;
    if (!list || !id) return setBox(null);
    let raf = 0;
    const measure = () => {
      const el = list.querySelector<HTMLElement>(`[data-ticket-id="${id}"]`);
      setBox(el ? { y: el.offsetTop, h: el.offsetHeight } : null);
    };
    measure();
    // Rows animate their height in and out; follow them until they settle.
    const ro = new ResizeObserver(() => {
      cancelAnimationFrame(raf);
      raf = requestAnimationFrame(measure);
    });
    for (const c of Array.from(list.children)) ro.observe(c);
    const t = setTimeout(measure, 260);
    return () => {
      ro.disconnect();
      cancelAnimationFrame(raf);
      clearTimeout(t);
    };
  }, [listRef, id, rows]);
  if (!box) return null;
  return (
    <motion.div
      aria-hidden
      className="pointer-events-none absolute inset-x-0 top-0 z-0 bg-sunken"
      initial={false}
      animate={{ y: box.y, height: box.h }}
      transition={reduce ? { duration: 0 } : springs.glide}
    >
      <span className={cn("absolute inset-y-2 left-0 w-0.5 rounded-r-xs transition-colors", active ? "bg-fg" : "bg-fg-4")} />
    </motion.div>
  );
}

function DetailFallback() {
  return (
    <div className="flex h-full flex-col">
      <div className="flex h-12 items-center gap-3 border-b border-line px-4">
        <Skeleton className="h-3 w-10" />
        <Skeleton className="h-3 w-64" />
      </div>
      <div className="mx-auto flex w-full max-w-[820px] flex-col gap-3 px-5 py-5">
        <Skeleton className="h-24 rounded-lg" />
        <Skeleton className="h-16 rounded-lg opacity-60" />
      </div>
    </div>
  );
}

export default function InboxPage() {
  const { t: tNav } = useTranslation("navigation");
  const { t: tr } = useTranslation("inbox");
  const params = useParams({ strict: false }) as { folder?: string; viewId?: string; teamId?: string; ticketId?: string };
  const search = useSearch({ strict: false }) as InboxSearch;
  const navigate = useNavigate();
  const qc = useQueryClient();
  const canEdit = atLeast(useRole(), "agent");
  const update = useUpdateTicket();
  const { data: ticketFields } = useTicketFields();
  const { data: views } = useQuery(q.views());
  const { data: counts } = useQuery(q.counts());
  const me = useSession((s) => s.user);

  const teamsCtx = useTeamsCtx();
  const folder = params.folder ? folderById(params.folder) : undefined;
  const view = params.viewId ? views?.find((v) => v.id === params.viewId) : undefined;
  // A team inbox: one team, or "mine" for every team the caller is on.
  const teamId = params.teamId;
  const team = teamId && teamId !== "mine" ? teamsCtx.team(teamId) : undefined;
  const tab = search.tab ?? "unassigned";
  const base = folder ? folder.query : teamId ? teamQuery(teamId, tab) : { view_id: params.viewId };
  const query = useMemo(() => listQuery(base, search), [JSON.stringify(base), JSON.stringify(search)]); // eslint-disable-line react-hooks/exhaustive-deps
  const listKey = JSON.stringify(query);
  const teamCounts = teamId ? (teamId === "mine" ? counts?.my_teams : counts?.teams?.[teamId]) : undefined;
  const title = (folder ? tNav(folder.labelKey) : undefined) ?? view?.name ?? (teamId ? (teamId === "mine" ? tr("teams.myTeams") : (team?.name ?? tr("teams.team"))) : tr("list.view"));
  const count = folder
    ? counts
      ? folder.count(counts)
      : null
    : params.viewId
      ? counts?.views?.[params.viewId]
      : teamCounts && (tab === "unassigned" || tab === "open")
        ? teamCounts[tab]
        : null;
  useDocumentTitle(params.ticketId ? null : title);

  const list = useInfiniteQuery(q.tickets(query));
  const agentOn = useAgentOn();
  // Unassigned in its default order puts visitors waiting in live chat first, longest
  // wait first: they are on the page now, and email can wait a minute.
  const pinChats = params.folder === "unassigned" && !search.sort;
  const tickets = useMemo(() => {
    const all = list.data?.pages.flatMap((p) => p.data) ?? [];
    if (!pinChats) return all;
    const now = Date.now();
    const waits = new Map(all.map((t) => [t.id, chatWait(t, agentOn, now)]));
    const waiting = all.filter((t) => waits.get(t.id)).sort((a, b) => waits.get(a.id)!.since - waits.get(b.id)!.since);
    return waiting.length ? [...waiting, ...all.filter((t) => !waits.get(t.id))] : all;
  }, [list.data, pinChats, agentOn]);
  const openId = params.ticketId;
  // Rows newer than this arrived live; they get a short tint.
  const shownAt = useMemo(() => Date.now(), [listKey]); // eslint-disable-line react-hooks/exhaustive-deps

  const wide = useMedia("(min-width: 1280px)");
  const phone = useMedia("(max-width: 767px)");
  const [layout, setLayoutState] = useState<InboxLayout>(readLayout);
  const setLayout = (l: InboxLayout) => {
    setLayoutState(l);
    try {
      localStorage.setItem(LAYOUT_KEY, l);
    } catch {
      /* storage unavailable */
    }
  };
  const previewing = !openId && wide && layout === "split" && tickets.length > 0;
  const split = !!openId || previewing;
  const compact = split || phone;
  // Beside a ticket the list keeps the width its edge was dragged to, at most half the inbox.
  const [listWidth, setListWidth] = usePaneWidth("list");
  const pageRef = useRef<HTMLDivElement>(null);
  const listPaneRef = useRef<HTMLElement>(null);

  // ── Selection and keyboard focus ────────────────────────────────────────
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [allMatching, setAllMatching] = useState(false);
  const [focusIdx, setFocusIdx] = useState(0);
  const lastToggled = useRef<string | null>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const rowsRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    setSelected(new Set());
    setAllMatching(false);
    setFocusIdx(0);
  }, [params.folder, params.viewId, params.teamId, tab]);

  useEffect(() => {
    setSelected(new Set());
    setAllMatching(false);
  }, [listKey]);

  // Drop selections that left the list (solved elsewhere, deleted), and keep
  // the focus in range when rows leave.
  useEffect(() => {
    setSelected((s) => {
      if (allMatching) return s;
      if (!s.size) return s;
      const ids = new Set(tickets.map((t) => t.id));
      const next = new Set([...s].filter((id) => ids.has(id)));
      return next.size === s.size ? s : next;
    });
    setFocusIdx((i) => Math.max(0, Math.min(i, tickets.length - 1)));
  }, [tickets, allMatching]);

  // Keep the focus marker on the open ticket.
  useEffect(() => {
    if (!openId) return;
    const i = tickets.findIndex((t) => t.id === openId || String(t.number) === openId);
    if (i >= 0) setFocusIdx(i);
  }, [openId, tickets]);

  useEffect(() => {
    const el = listRef.current?.querySelector<HTMLElement>(`[data-ticket-id="${tickets[focusIdx]?.id}"]`);
    el?.scrollIntoView({ block: "nearest" });
  }, [focusIdx, tickets]);

  // Teammates' presence on the visible rows.
  useEffect(() => {
    realtime()?.setWatched(tickets.slice(0, 30).map((t) => t.id));
  }, [tickets]);
  useEffect(() => () => realtime()?.setWatched([]), []);

  // The same list page serves folders, views and team inboxes.
  const go = useCallback(
    (ticketId: string | undefined, search: InboxSearch | true, replace?: boolean) => {
      if (params.viewId) void navigate({ to: "/views/$viewId/{-$ticketId}", params: { viewId: params.viewId, ticketId }, search, replace });
      else if (params.teamId) void navigate({ to: "/teams/$teamId/{-$ticketId}", params: { teamId: params.teamId, ticketId }, search, replace });
      else void navigate({ to: "/inbox/$folder/{-$ticketId}", params: { folder: params.folder!, ticketId }, search, replace });
    },
    [navigate, params.folder, params.viewId, params.teamId],
  );
  const open = useCallback((t: Ticket | undefined) => t && go(t.id, true), [go]);
  const close = useCallback(() => go(undefined, true), [go]);

  const setSearch = (s: InboxSearch) => {
    const clean = Object.fromEntries(Object.entries(s).filter(([, v]) => v !== undefined && v !== "")) as InboxSearch;
    // Filters keep the team inbox's tab.
    if (params.teamId && search.tab && !("tab" in s)) clean.tab = search.tab;
    go(params.ticketId, clean, true);
  };

  const toggle = useCallback(
    (t: Ticket, shift: boolean) => {
      if (allMatching) {
        setAllMatching(false);
        setSelected(new Set([t.id]));
        lastToggled.current = t.id;
        return;
      }
      setSelected((s) => {
        const next = new Set(s);
        if (shift && lastToggled.current) {
          const a = tickets.findIndex((x) => x.id === lastToggled.current);
          const b = tickets.findIndex((x) => x.id === t.id);
          if (a >= 0 && b >= 0) {
            for (let i = Math.min(a, b); i <= Math.max(a, b); i++) next.add(tickets[i].id);
            return next;
          }
        }
        if (next.has(t.id)) next.delete(t.id);
        else next.add(t.id);
        lastToggled.current = t.id;
        return next;
      });
    },
    [tickets, allMatching],
  );

  const move = (d: 1 | -1) => {
    const i = Math.max(0, Math.min(tickets.length - 1, focusIdx + d));
    setFocusIdx(i);
    if (openId) open(tickets[i]);
    if (d === 1 && i >= tickets.length - 5 && list.hasNextPage && !list.isFetchingNextPage) void list.fetchNextPage();
  };

  // Solve the focused ticket from the list; the row leaves and the focus stays put.
  const solveFocused = () => tickets[focusIdx] && solveTicket(tickets[focusIdx]);
  function solveTicket(t: Ticket) {
    if (!canEdit) return;
    const done = t.status === "solved" || t.status === "closed";
    const missing = done ? [] : missingForSolve(ticketFields, t);
    if (missing.length) {
      // Open it so the field can be filled in right away.
      toast.error(tr("fields.fill", { name: missing.map((f) => f.name).join(", ") }));
      open(t);
      setTimeout(() => focusField(missing[0].key), 400);
      return;
    }
    update.mutate({ id: t.id, patch: { status: done ? "open" : "solved" } });
    toast.success(
      done ? tr("toast.reopened", { number: t.number }) : tr("toast.solved", { number: t.number }),
      done ? undefined : { action: { label: tr("toast.undo"), onClick: () => update.mutate({ id: t.id, patch: { status: t.status } }) } },
    );
  }
  const quick = useCallback(
    (t: Ticket, a: QuickAction) => {
      if (!canEdit) return;
      if (a === "solve") {
        const i = tickets.findIndex((x) => x.id === t.id);
        if (i >= 0) setFocusIdx(i);
        solveTicket(t);
      } else if (a === "assign" && me) {
        update.mutate({ id: t.id, patch: { assignee_id: me.id } });
        toast.success(tr("quick.assigned", { number: t.number }), {
          action: { label: tr("toast.undo"), onClick: () => update.mutate({ id: t.id, patch: { assignee_id: t.assignee?.id ?? null } }) },
        });
      } else if (a === "snooze") {
        const at = snoozeOptions().find((o) => o.key === "tomorrow")!.at;
        update.mutate({ id: t.id, patch: { snoozed_until: at.toISOString() } });
        toast.success(tr("quick.snoozed", { number: t.number }), {
          action: { label: tr("toast.undo"), onClick: () => update.mutate({ id: t.id, patch: { snoozed_until: t.snoozed_until } }) },
        });
      }
    },
    [canEdit, me, tickets, update, tr], // eslint-disable-line react-hooks/exhaustive-deps
  );
  const spamFocused = () => {
    const t = tickets[focusIdx];
    if (!t || !canEdit) return;
    const to = !t.spam;
    update.mutate({ id: t.id, patch: { spam: to } });
    toast.success(to ? tr("toast.spammed") : tr("toast.unspammed"), {
      action: { label: tr("toast.undo"), onClick: () => update.mutate({ id: t.id, patch: { spam: !to } }) },
    });
  };
  const replyFocused = () => {
    const t = tickets[focusIdx];
    if (!t || !canEdit) return;
    intent.reply(t.id);
    open(t);
  };

  useHotkeys({
    j: () => move(1),
    arrowdown: () => !openId && move(1),
    k: () => move(-1),
    arrowup: () => !openId && move(-1),
    enter: () => !openId && open(tickets[focusIdx]),
    o: () => open(tickets[focusIdx]),
    x: () => tickets[focusIdx] && toggle(tickets[focusIdx], false),
    ...(openId
      ? {}
      : {
          e: solveFocused,
          r: replyFocused,
          i: () => tickets[focusIdx] && !tickets[focusIdx].assignee && quick(tickets[focusIdx], "assign"),
          h: () => tickets[focusIdx] && quick(tickets[focusIdx], "snooze"),
          s: (e: KeyboardEvent) => e.shiftKey && spamFocused(),
        }),
    "/": () => {
      searchRef.current?.focus();
      if (!searchRef.current) document.querySelector<HTMLButtonElement>("[data-inbox-search]")?.click();
    },
    escape: () => {
      if (selected.size || allMatching) {
        setSelected(new Set());
        setAllMatching(false);
      }
      else if (openId) close();
    },
    "mod+a": () => {
      if (!openId) {
        setAllMatching(false);
        setSelected(new Set(tickets.map((t) => t.id)));
      }
    },
  });

  // Infinite scroll.
  const sentinel = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = sentinel.current;
    if (!el) return;
    const io = new IntersectionObserver((e) => {
      if (e[0]?.isIntersecting && list.hasNextPage && !list.isFetchingNextPage) void list.fetchNextPage();
    });
    io.observe(el);
    return () => io.disconnect();
  }, [list]);

  const prefetch = useCallback(
    (t: Ticket) => {
      void qc.prefetchQuery(q.messages(t.id));
      void qc.prefetchQuery(q.activities(t.id));
    },
    [qc],
  );

  // Prefetch around the focus so the preview and j/k feel instant.
  useEffect(() => {
    if (!previewing) return;
    [focusIdx, focusIdx + 1].forEach((i) => tickets[i] && prefetch(tickets[i]));
  }, [previewing, focusIdx, tickets, prefetch]);

  const fid = folder?.id ?? (teamId ? "team" : "view");
  const selecting = selected.size > 0 || allMatching;
  const idx = tickets.findIndex((t) => t.id === openId || String(t.number) === openId);
  const focusedTicket = tickets[focusIdx];
  // The strip counts the whole list (GET /v1/tickets/summary), whatever its order. A search
  // is not counted by the server, so it is not asked.
  const summaryQuery = useMemo(() => ({ ...query, sort: undefined, order: undefined }), [query]);
  const { data: listSummary } = useQuery({ ...q.ticketSummary(summaryQuery), enabled: !query.q });
  const loaded = useMemo(() => loadedCounts(tickets), [tickets]);
  const summary = stripCounts(listSummary, loaded, !!list.hasNextPage);

  return (
    <div ref={pageRef} className="relative flex h-full min-h-0">
      <section
        ref={listPaneRef}
        aria-label={title}
        style={listWidth === null ? undefined : ({ "--pane-list": paneCss("list", listWidth, "%") } as CSSProperties)}
        className={cn(
          "relative flex min-h-0 flex-col",
          split
            ? "hidden w-[var(--pane-list-live,var(--pane-list,340px))] shrink-0 border-r border-line lg:flex xl:w-[var(--pane-list-live,var(--pane-list,380px))] 2xl:w-[var(--pane-list-live,var(--pane-list,420px))]"
            : "min-w-0 flex-1",
        )}
      >
        <ListToolbar
          title={title}
          icon={folder ? folderIcon[folder.id] : team ? <TeamDot team={team} className="size-2.5" /> : teamId ? <Users /> : <Layers />}
          count={count}
          inTeam={!!teamId}
          below={
            teamId ? (
              <TeamTabs
                tab={tab}
                counts={teamCounts}
                onTab={(next) => go(params.ticketId, { ...search, tab: next === "unassigned" ? undefined : next }, true)}
              />
            ) : undefined
          }
          search={search}
          onSearch={setSearch}
          searchRef={searchRef}
          compact={compact}
          layout={wide && !openId ? layout : undefined}
          onLayout={setLayout}
        />
        {tickets.length > 0 && <WaitingBanner tickets={tickets} onOpen={open} />}
        {!openId && tickets.length > 0 && <SummaryStrip {...summary} search={search} onSearch={setSearch} />}
        <div ref={listRef} role="listbox" aria-label={tr("list.label")} aria-multiselectable className="flex min-h-0 flex-1 flex-col overflow-y-auto">
          {list.isPending ? (
            <SkeletonRows rows={compact ? 7 : 12} />
          ) : list.isError ? (
            <EmptyState
              lit={3}
              title={tr("error.title")}
              description={tr("error.description")}
              action={<Button onClick={() => void list.refetch()}>{tr("error.retry")}</Button>}
            />
          ) : !tickets.length ? (
            <InboxEmpty folder={fid} counts={counts} search={search} onSearch={setSearch} viewId={params.viewId} teamName={teamId ? title : undefined} tab={tab} />
          ) : (
            <div ref={rowsRef} className="relative shrink-0">
              <FocusGlide listRef={rowsRef} rows={tickets} id={focusedTicket?.id} active={!!focusedTicket && (focusedTicket.id === openId || String(focusedTicket.number) === openId)} />
              <AnimatePresence initial={false} key={listKey}>
                {tickets.map((t, i) => (
                  <TicketRow
                    key={t.id}
                    ticket={t}
                    active={t.id === openId || String(t.number) === openId}
                    focused={i === focusIdx}
                    selected={allMatching || selected.has(t.id)}
                    selecting={selecting}
                    compact={compact}
                    hideAssignee={fid === "mine"}
                    fresh={Date.parse(t.last_message_at) > shownAt}
                    onOpen={open}
                    onToggle={toggle}
                    onHover={prefetch}
                    onQuick={canEdit ? quick : undefined}
                  />
                ))}
              </AnimatePresence>
            </div>
          )}
          {tickets.length > 0 && (
              <div ref={sentinel} className="shrink-0">
                {list.isFetchingNextPage ? <SkeletonRows rows={2} /> : <div className="h-14" />}
              </div>
          )}
        </div>
        <BulkBar
          ids={[...selected]}
          query={query}
          allMatching={allMatching}
          onSelectAll={selected.size === tickets.length && list.hasNextPage ? () => setAllMatching(true) : undefined}
          onClear={() => {
            setSelected(new Set());
            setAllMatching(false);
          }}
          spamView={fid === "spam"}
          compact={compact}
        />
      </section>
      {split && (
        <ResizeHandle
          pane="list"
          side="before"
          target={listPaneRef}
          room={() => pageRef.current?.clientWidth}
          width={listWidth}
          onLive={(px) => setLiveWidth(listPaneRef.current, "list", px === null ? null : paneCss("list", px, "%"))}
          onWidth={setListWidth}
          className="hidden lg:block"
        />
      )}

      {openId ? (
        <section aria-label={tr("detail.label")} className="flex min-w-0 flex-1 flex-col">
          <Suspense fallback={<DetailFallback />}>
            <TicketDetail
              key={openId}
              ticketId={openId}
              onClose={close}
              onPrev={idx > 0 ? () => open(tickets[idx - 1]) : undefined}
              onNext={idx >= 0 && idx < tickets.length - 1 ? () => open(tickets[idx + 1]) : undefined}
              position={idx >= 0 ? { index: idx + 1, total: count ?? tickets.length } : undefined}
            />
          </Suspense>
        </section>
      ) : previewing && focusedTicket ? (
        <section aria-label={tr("preview.label")} className="flex min-w-0 flex-1 flex-col" data-ollie-ticket={focusedTicket.id}>
          <TicketPreview key={focusedTicket.id} ticket={focusedTicket} onOpen={() => open(focusedTicket)} onReply={canEdit ? replyFocused : undefined} />
        </section>
      ) : null}
    </div>
  );
}
