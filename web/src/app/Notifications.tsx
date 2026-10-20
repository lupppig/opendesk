import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { AtSign, Bell, BellOff, Mail, ChevronRight, CornerDownLeft, CreditCard, Gauge, Hand, Import, KeyRound, MailX, MessagesSquare, Moon, Settings2, Sparkles, TimerOff, UserPlus, UsersRound } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import type { Notification } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { clock, formatUsd, relShort } from "@/lib/format";
import { notificationHolds } from "@/lib/notificationPrefs";
import { q } from "@/lib/queries";
import { AI_TONE, tone, type Tone } from "@/lib/tones";
import { Popover } from "@/components/ui/popover";
import { Tooltip } from "@/components/ui/tooltip";
import { EmptyState } from "@/components/ui/empty";
import { Skeleton } from "@/components/ui/skeleton";
import { Segmented } from "@/components/ui/tabs";
import { useMarkNotificationsRead } from "./notificationRead";
import { i18n } from "@/i18n";

// Each kind keeps one icon and one hue, so the list scans by what happened.
const kind: Record<Notification["type"], { icon: React.ReactNode; tone: Tone }> = {
  assigned: { icon: <UserPlus />, tone: "blue" },
  mentioned: { icon: <AtSign />, tone: "indigo" },
  customer_replied: { icon: <CornerDownLeft />, tone: "teal" },
  thread_replied: { icon: <MessagesSquare />, tone: "cyan" },
  sla_breached: { icon: <TimerOff />, tone: "red" },
  ai_handoff: { icon: <Hand />, tone: AI_TONE },
  ai_cap_reached: { icon: <Gauge />, tone: AI_TONE },
  ai_balance_low: { icon: <Sparkles />, tone: "amber" },
  ai_balance_out: { icon: <Sparkles />, tone: "red" },
  auto_recharge_failed: { icon: <CreditCard />, tone: "red" },
  ai_key_failed: { icon: <KeyRound />, tone: "red" },
  team_joined: { icon: <UsersRound />, tone: "cyan" },
  import_finished: { icon: <Import />, tone: "green" },
  collab_assigned: { icon: <Mail />, tone: "blue" },
  collab_mention: { icon: <AtSign />, tone: "indigo" },
};

/** A notification's icon and hue; an import that stopped or waits for credentials is not green. */
function kindOf(n: Notification): { icon: React.ReactNode; tone: Tone } {
  const k = kind[n.type] ?? { icon: <Bell />, tone: "gray" as Tone };
  if (n.type === "import_finished" && n.data.status !== "completed") return { ...k, tone: n.data.status === "needs_credentials" ? "amber" : "red" };
  return k;
}

/** The mail conversation a Collab notification is about. */
export function collabThread(n: Notification): string | null {
  if (n.type !== "collab_assigned" && n.type !== "collab_mention") return null;
  return typeof n.data.thread_id === "string" && n.data.thread_id ? n.data.thread_id : null;
}

/** Where a notification leads: its ticket, its mail conversation, or the settings page it is about. */
export function notificationPath(n: Notification): string | null {
  if (n.type === "collab_assigned" || n.type === "collab_mention") {
    const id = collabThread(n);
    return id ? `/mail/${n.type === "collab_assigned" ? "assigned" : "all"}/${encodeURIComponent(id)}` : "/mail/assigned";
  }
  if (n.type === "ai_cap_reached") return "/settings/ai/behaviour";
  if (n.type === "ai_key_failed") return "/settings/ai";
  if (n.type === "ai_balance_low" || n.type === "ai_balance_out" || n.type === "auto_recharge_failed") return "/settings/billing";
  if (n.type === "team_joined") {
    const id = joinedTeam(n).id;
    return id ? `/settings/teams/${encodeURIComponent(id)}` : "/settings/teams";
  }
  if (n.type === "import_finished") {
    const id = typeof n.data.import_id === "string" && n.data.import_id ? n.data.import_id : null;
    return id ? `/settings/import?id=${encodeURIComponent(id)}` : "/settings/import";
  }
  if (n.ticket) return `/inbox/mine/${encodeURIComponent(n.ticket.id)}`;
  return null;
}

/** The team of a `team_joined` notification and who joined it. */
export function joinedTeam(n: Notification): { id: string | null; name: string | null; private: boolean; who: string | null } {
  const team = (n.data.team ?? {}) as { id?: unknown; name?: unknown; private?: unknown };
  const user = (n.data.user ?? {}) as { name?: unknown };
  const id = typeof team.id === "string" && team.id ? team.id : typeof n.data.team_id === "string" && n.data.team_id ? n.data.team_id : null;
  return {
    id,
    name: typeof team.name === "string" && team.name ? team.name : null,
    private: team.private === true,
    who: typeof user.name === "string" && user.name ? user.name : null,
  };
}

export function describe(n: Notification, t: TFunction<"shell">): string {
  const actor = n.data.actor as { name?: string } | undefined;
  const who = typeof actor?.name === "string" ? actor.name : null;
  const c = n.data.contact as { name?: string } | undefined;
  const contact = typeof c?.name === "string" && c.name ? c.name : null;
  switch (n.type) {
    case "assigned":
      return who ? t("notifications.type.assigned", { who }) : t("notifications.type.assignedAnon");
    case "mentioned":
      return who ? t("notifications.type.mentioned", { who }) : t("notifications.type.mentionedAnon");
    case "customer_replied":
      return contact ? t("notifications.type.replied", { who: contact }) : t("notifications.type.repliedAnon");
    case "thread_replied": {
      const from = (n.data.from ?? {}) as { name?: unknown; email?: unknown };
      const sender = typeof from.name === "string" && from.name ? from.name : typeof from.email === "string" && from.email ? from.email : null;
      return sender ? t("notifications.type.threadReplied", { who: sender }) : t("notifications.type.threadRepliedAnon");
    }
    case "sla_breached":
      return t("notifications.type.sla");
    case "ai_handoff":
      return contact ? t("notifications.type.handoff", { who: contact }) : t("notifications.type.handoffAnon");
    case "ai_cap_reached":
      return typeof n.data.cap_usd === "number" ? t("notifications.type.capReachedUsd", { cap: formatUsd(n.data.cap_usd) }) : t("notifications.type.capReached");
    case "ai_balance_low":
      return typeof n.data.remaining_usd === "number" ? t("notifications.type.balanceLowUsd", { remaining: formatUsd(n.data.remaining_usd) }) : t("notifications.type.balanceLow");
    case "ai_balance_out":
      return t("notifications.type.balanceOut");
    case "auto_recharge_failed":
      return t("notifications.type.rechargeFailed");
    case "ai_key_failed":
      return n.data.reason === "no_funds" ? t("notifications.type.keyNoFunds") : t("notifications.type.keyRejected");
    case "team_joined": {
      const j = joinedTeam(n);
      const who = j.who ?? t("notifications.type.someone");
      if (!j.name) return t("notifications.type.teamJoinedAnon", { who });
      return j.private ? t("notifications.type.teamJoinedPrivate", { who, team: j.name }) : t("notifications.type.teamJoined", { who, team: j.name });
    }
    case "import_finished": {
      const source = typeof n.data.source_name === "string" && n.data.source_name ? n.data.source_name : t("notifications.type.importSource");
      if (n.data.status === "needs_credentials") return t("notifications.type.importCredentials", { source });
      if (n.data.status !== "completed") return t("notifications.type.importFailed", { source });
      const tickets = typeof n.data.tickets === "number" ? n.data.tickets : 0;
      const articles = typeof n.data.articles === "number" ? n.data.articles : 0;
      const parts = [
        ...(tickets > 0 ? [t("notifications.type.importTickets", { count: tickets })] : []),
        ...(articles > 0 ? [t("notifications.type.importArticles", { count: articles })] : []),
      ];
      if (!parts.length) return t("notifications.type.importDone", { source });
      const counts = parts.length === 2 ? t("notifications.type.importAnd", { a: parts[0], b: parts[1] }) : parts[0];
      return t("notifications.type.importDoneCounts", { source, counts });
    }
    case "collab_assigned":
    case "collab_mention": {
      const c = i18n.getFixedT(null, "collab");
      if (n.type === "collab_assigned") return who ? c("notifications.assigned", { who }) : c("notifications.assignedAnon");
      return who ? c("notifications.mention", { who }) : c("notifications.mentionAnon");
    }
    default:
      return t("notifications.type.other");
  }
}

/** A Collab notification's conversation subject, when the server sent one. */
function collabSubject(n: Notification): string | null {
  const s = n.data.subject;
  return collabThread(n) && typeof s === "string" && s ? s : null;
}

export function NotificationsButton() {
  const [open, setOpen] = useState(false);
  const [filter, setFilter] = useState<"all" | "unread">("all");
  const navigate = useNavigate();
  const reduce = useReducedMotion();
  const { data, isLoading } = useQuery(q.notifications());
  const unread = data?.filter((n) => !n.read_at).length ?? 0;
  const { t } = useTranslation("shell");
  const shown = (data ?? []).filter((n) => filter === "all" || !n.read_at);
  // The bell rings once when a new unread notification arrives (not on first load).
  const [rings, setRings] = useState(0);
  const seen = useRef<number | null>(null);
  useEffect(() => {
    if (!data) return;
    if (seen.current !== null && unread > seen.current) setRings((r) => r + 1);
    seen.current = unread;
  }, [data, unread]);

  const markRead = useMarkNotificationsRead();

  const openTicket = (n: Notification) => {
    if (!n.read_at) markRead.mutate({ ids: [n.id] });
    setOpen(false);
    if (n.type === "ai_cap_reached") void navigate({ to: "/settings/ai/behaviour" });
    else if (n.type === "ai_key_failed") void navigate({ to: "/settings/ai" });
    else if (n.type === "ai_balance_low" || n.type === "ai_balance_out" || n.type === "auto_recharge_failed") void navigate({ to: "/settings/billing" });
    else if (n.type === "team_joined") {
      const id = joinedTeam(n).id;
      if (id) void navigate({ to: "/settings/teams/$teamId", params: { teamId: id } });
      else void navigate({ to: "/settings/teams" });
    } else if (n.type === "import_finished") void navigate({ to: "/settings/import" });
    else if (n.type === "collab_assigned" || n.type === "collab_mention") {
      const id = collabThread(n);
      void navigate({ to: "/mail/$box/{-$threadId}", params: { box: n.type === "collab_assigned" ? "assigned" : "all", threadId: id ?? undefined }, search: {} });
    }
    else if (n.ticket)
      void navigate({
        to: "/inbox/$folder/{-$ticketId}",
        params: { folder: n.type === "assigned" || n.type === "mentioned" ? "mine" : "open", ticketId: n.ticket.id },
      });
  };

  return (
    <Popover
      open={open}
      onOpenChange={setOpen}
      side="bottom"
      align="end"
      className="w-[400px] overflow-hidden p-0"
      trigger={
        <Tooltip content={t("notifications.title")}>
          <button
            type="button"
            aria-label={unread ? t("notifications.unread", { count: unread }) : t("notifications.title")}
            className="relative flex size-7 items-center justify-center rounded-md text-fg-3 transition-colors hover:bg-chrome-hover hover:text-fg active:scale-[0.96] data-[state=open]:bg-chrome-active data-[state=open]:text-fg motion-reduce:active:scale-100"
          >
            <Bell key={rings} className={cn("size-[15px] origin-top", rings > 0 && "animate-bell-ring motion-reduce:animate-none")} />
            <AnimatePresence>
              {unread > 0 && (
                <motion.span
                  key="dot"
                  initial={reduce ? false : { scale: 0 }}
                  animate={{ scale: 1 }}
                  exit={reduce ? undefined : { scale: 0 }}
                  transition={{ type: "spring", stiffness: 600, damping: 30 }}
                  className="absolute right-[5px] top-[5px] size-[7px] rounded-full bg-tone-blue-solid ring-2 ring-chrome"
                />
              )}
            </AnimatePresence>
          </button>
        </Tooltip>
      }
    >
      <div className="flex h-12 items-center gap-2 border-b border-line px-3.5">
        <span className="text-[13px] font-semibold tracking-[-0.01em]">{t("notifications.title")}</span>
        {unread > 0 && <span className={cn("mono rounded-sm px-1.5 text-[10.5px] font-medium tabular-nums leading-[18px]", tone.blue.chip)}>{unread}</span>}
        <div className="ml-auto flex items-center gap-1.5">
          {unread > 0 && (
            <button
              type="button"
              onClick={() => markRead.mutate({ all: true })}
              className="rounded-sm px-1.5 py-0.5 text-xs text-fg-3 transition-colors hover:bg-hover hover:text-fg"
            >
              {t("notifications.markAll")}
            </button>
          )}
          <Tooltip content={t("notifications.settings")}>
            <Link
              to="/settings/notifications"
              aria-label={t("notifications.settings")}
              onClick={() => setOpen(false)}
              className="flex size-6 items-center justify-center rounded-md text-fg-3 transition-colors hover:bg-hover hover:text-fg [&_svg]:size-3.5"
            >
              <Settings2 />
            </Link>
          </Tooltip>
          <Segmented
            size="xs"
            value={filter}
            onChange={setFilter}
            options={[
              { value: "all", label: t("notifications.all") },
              { value: "unread", label: t("notifications.unreadTab") },
            ]}
          />
        </div>
      </div>
      <HoldStrip onOpen={() => setOpen(false)} />
      <div className="max-h-[440px] overflow-y-auto p-1">
        {isLoading ? (
          <div className="flex flex-col gap-1 p-1" aria-busy="true">
            {[0, 1, 2].map((i) => (
              <div key={i} className="flex items-start gap-2.5 px-2 py-2">
                <Skeleton className="size-7 rounded-md" />
                <div className="flex flex-1 flex-col gap-2 pt-1">
                  <Skeleton className="h-2.5 w-2/3" />
                  <Skeleton className="h-2 w-1/2 opacity-60" />
                </div>
              </div>
            ))}
          </div>
        ) : !shown.length ? (
          filter === "unread" && data?.length ? (
            <EmptyState compact art="solved" title={t("notifications.noneUnread")} description={t("notifications.noneUnreadHint")} />
          ) : (
            <EmptyState compact art="inbox" title={t("notifications.empty")} description={t("notifications.emptyHint")} />
          )
        ) : (
          <ul>
            <AnimatePresence initial={false}>
              {shown.map((n) => {
                const k = kindOf(n);
                return (
                  <motion.li
                    key={n.id}
                    layout={reduce ? false : "position"}
                    initial={reduce ? false : { opacity: 0, y: -4 }}
                    animate={{ opacity: 1, y: 0 }}
                    exit={reduce ? undefined : { opacity: 0, transition: { duration: 0.12 } }}
                    transition={{ type: "spring", stiffness: 500, damping: 40 }}
                  >
                    <button
                      type="button"
                      onClick={() => openTicket(n)}
                      className="group flex w-full items-start gap-2.5 rounded-md px-2 py-2 text-left transition-colors duration-100 hover:bg-hover active:scale-[0.995] motion-reduce:active:scale-100"
                    >
                      <span
                        className={cn(
                          "mt-px flex size-7 shrink-0 items-center justify-center rounded-md transition-opacity [&_svg]:size-3.5",
                          tone[k.tone].chip,
                          n.read_at && "opacity-60",
                        )}
                      >
                        {k.icon}
                      </span>
                      <span className="min-w-0 flex-1">
                        <span className={cn("block truncate text-[12.5px]", n.read_at ? "text-fg-2" : "font-medium text-fg")}>{describe(n, t)}</span>
                        {!n.ticket && collabSubject(n) && (
                          <span className="mt-0.5 block truncate text-xs text-fg-3" data-ph-mask>
                            {collabSubject(n)}
                          </span>
                        )}
                        {n.ticket && (
                          <span className="mt-0.5 flex min-w-0 items-center gap-1.5 text-xs text-fg-3">
                            <span className="mono shrink-0 text-fg-4">#{n.ticket.number}</span>
                            <span className="truncate" data-ph-mask>
                              {n.ticket.subject}
                            </span>
                          </span>
                        )}
                      </span>
                      <span className="flex shrink-0 flex-col items-end gap-1.5 pt-0.5">
                        <span className="mono text-[10.5px] text-fg-4">{relShort(n.created_at)}</span>
                        {!n.read_at && <span className="size-1.5 rounded-full bg-tone-blue-solid" role="img" aria-label={t("notifications.unreadDot")} />}
                      </span>
                    </button>
                  </motion.li>
                );
              })}
            </AnimatePresence>
          </ul>
        )}
      </div>
    </Popover>
  );
}

/** Why nothing reaches you outside the app right now, with the way back to turn it on. */
function HoldStrip({ onOpen }: { onOpen: () => void }) {
  const { t } = useTranslation("shell");
  const { data: prefs } = useQuery(q.notificationPreferences());
  const hold = prefs ? notificationHolds(prefs)[0] : undefined;
  if (!hold) return null;
  const { icon, text } =
    hold.kind === "paused"
      ? { icon: <Moon />, text: t("notifications.hold.paused", { when: clock(hold.until) }) }
      : hold.kind === "unsubscribed"
        ? { icon: <MailX />, text: t("notifications.hold.unsubscribed") }
        : { icon: <BellOff />, text: t("notifications.hold.emailOff") };
  return (
    <Link
      to="/settings/notifications"
      onClick={onOpen}
      className={cn("flex items-center gap-2 border-b px-3.5 py-2 text-[12px] transition-colors hover:brightness-[0.98] [&_svg]:size-3.5", tone.amber.soft, tone.amber.line)}
    >
      <span className={tone.amber.text}>{icon}</span>
      <span className="min-w-0 flex-1 truncate text-fg-2">{text}</span>
      <span className="flex items-center gap-0.5 font-medium text-fg">
        {t("notifications.hold.turnOn")}
        <ChevronRight />
      </span>
    </Link>
  );
}
