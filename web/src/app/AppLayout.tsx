import { useTranslation } from "react-i18next";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useWaitingAlerts } from "@/components/tickets/useWaitingAlerts";
import { Outlet, useNavigate, useRouter, useRouterState } from "@tanstack/react-router";
import { lazy, Suspense, useEffect, useRef, type CSSProperties } from "react";
import { Dialog as D } from "radix-ui";
import { identify } from "@/lib/analytics";
import { formatUsd } from "@/lib/format";
import { useHotkeys } from "@/lib/hotkeys";
import { useHeartbeat } from "@/lib/pictures";
import { playAlertOnce, type AlertSound } from "@/lib/alertSound";
import { realtime, startRealtime, stopRealtime } from "@/lib/realtime/manager";
import { inWorkspace } from "@/lib/api/client";
import { useWorkspaces } from "@/lib/workspaces";
import { useRealtime } from "@/lib/realtime/store";
import { currentMembership, reloadSession, selectWorkspace, useSession } from "@/lib/session";
import { useUpgrade } from "@/lib/upgrade";
import { toast } from "@/components/ui/toast";
import { AssistantDock, AssistantRoot } from "@/components/assistant/AssistantRoot";
import type { Notification, Ticket } from "@/lib/api/types";
import { q, qk } from "@/lib/queries";
import { subscribeWebPush } from "@/lib/webPush";
import { afterOwnCreations, wantsTicketSound } from "@/lib/ownTickets";
import { i18n } from "@/i18n";
import { collabThread, describe, joinedTeam, ticketFolder } from "./Notifications";
import { Sidebar } from "./Sidebar";
import { SuspendedWorkspace } from "./SuspendedWorkspace";
import { AppHeader } from "./AppHeader";
import { cn } from "@/lib/cn";
import { paneCss, setLiveWidth, usePaneWidth } from "@/lib/panes";
import { ResizeHandle } from "@/components/ui/resize-handle";
import { CommandPalette } from "./CommandPalette";
import { ShortcutsSheet } from "./ShortcutsSheet";
import { ConnectionBanner } from "./ConnectionBanner";
import { notifyDesktop, useDesktopBadge, useDesktopPreferences } from "./useDesktopBridge";
import { AiBalanceBanner } from "@/components/billing/AiBalanceBanner";
import { DesktopUpdateBanner } from "@/components/desktop/UpdateBanner";
import { AllowNotificationsBanner } from "@/components/notifications/AllowBanner";
import { NEW_TICKET_PATH, shell, toggleNav, useNav, useShell } from "./shellStore";
import { useCollab } from "@/lib/collab/hooks";
import { useSpaceUi } from "@/lib/spaces/store";
import { compose } from "@/lib/collab/compose";

const NewTicketDialog = lazy(() => import("@/components/tickets/NewTicketDialog"));
// Collab's new mail composer loads only for workspaces with Collab.
const CollabRoot = lazy(() => import("@/components/collab/CollabRoot"));
// Customize sidebar is every member's, with or without Collab.
const CustomizeSidebar = lazy(() => import("@/components/collab/CustomizeSidebar").then((m) => ({ default: m.CustomizeSidebar })));
const SpaceEditorRoot = lazy(() => import("@/components/spaces/SpaceEditor"));
const UpgradeDialog = lazy(() => import("@/components/billing/UpgradeDialog"));

function notificationText(n: Notification) {
  const subject = n.ticket ? `#${n.ticket.number}` : "";
  switch (n.type) {
    case "assigned":
      return `Assigned to you ${subject}`;
    case "new_ticket":
      return describe(n, i18n.getFixedT(null, "shell"));
    case "mentioned":
      return `You were mentioned ${subject}`;
    case "customer_replied":
      return `New reply ${subject}`;
    case "thread_replied":
      return `New answer in a side conversation ${subject}`;
    case "sla_breached":
      return `SLA breached ${subject}`;
    case "ai_handoff":
      return `A customer asked for a person ${subject}`;
    case "ai_cap_reached":
      return typeof n.data.cap_usd === "number" ? `Ollie reached this month's ${formatUsd(n.data.cap_usd)} agent cap` : "Ollie reached this month's agent cap";
    case "ai_balance_low":
      return typeof n.data.remaining_usd === "number" ? `The AI balance is running low: ${formatUsd(n.data.remaining_usd)} left` : "The AI balance is running low";
    case "ai_balance_out":
      return "The AI balance is used up";
    case "auto_recharge_failed":
      return "Auto-recharge failed and is now off";
    case "ai_key_failed":
      return n.data.reason === "no_funds" ? "Your AI provider account is out of funds" : "Your AI provider rejected the workspace's key";
    case "team_joined":
    case "import_finished":
    case "support_channel_unhealthy":
    case "collab_assigned":
    case "collab_mention":
      return describe(n, i18n.getFixedT(null, "shell"));
    default:
      return `Notification ${subject}`;
  }
}

const billingNoticeBody: Partial<Record<Notification["type"], string>> = {
  ai_balance_low: "Add AI usage or turn on auto-recharge to keep Ollie working.",
  ai_balance_out: "Ollie is paused and drafts are unavailable until you add a top-up.",
  auto_recharge_failed: "The payment didn't go through. Update the payment method, then turn it back on.",
  ai_key_failed: "AI features are paused until the key works again. Fix it in AI settings.",
};

// Everyday sounds (lib/alertSound.ts); live chat has its own alarm in useWaitingAlerts.
const notificationSound: Partial<Record<Notification["type"], AlertSound>> = {
  customer_replied: "reply",
  thread_replied: "reply",
  assigned: "assigned",
  mentioned: "assigned",
  sla_breached: "assigned",
  collab_assigned: "assigned",
  collab_mention: "assigned",
};

/** What a stopped import says went wrong, for its toast. */
function importProblem(n: Notification): string | undefined {
  if (n.type !== "import_finished" || n.data.status === "completed") return undefined;
  return typeof n.data.error === "string" && n.data.error ? n.data.error : undefined;
}

/** Whether this person is looking at the ticket right now, so a sound would only interrupt. */
function lookingAt(ticketId: string | undefined) {
  return !!ticketId && document.hasFocus() && useRealtime.getState().active === ticketId;
}

function soundForNotification(n: Notification) {
  const kind = notificationSound[n.type];
  if (!kind || (n.type === "customer_replied" && lookingAt(n.ticket?.id))) return;
  playAlertOnce(kind, n.id);
}

/** A new ticket for the team or for you that someone else created (lib/ownTickets.ts). */
function soundForTicket(t: Ticket, me: string) {
  afterOwnCreations(() => {
    if (wantsTicketSound(t, me)) playAlertOnce("ticket", t.id);
  });
}

function useRealtimeConnection() {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const router = useRouter();
  const wid = useSession((s) => s.workspaceId);
  const uid = useSession((s) => s.user?.id);
  const suspended = useSession((s) => !!currentMembership(s)?.suspended);
  // Someone in several workspaces keeps one socket that follows them all while the focus
  // moves: a Space's rows, the bell and the Everywhere lists stay live across workspaces.
  const several = useWorkspaces().several;
  const memberKey = useSession((s) => s.memberships.map((m) => m.workspace.id).join(","));
  const socketKey = several ? `all:${memberKey}` : `one:${wid}`;
  const focusRef = useRef(wid);
  focusRef.current = wid;
  useEffect(() => {
    if (wid && uid) identify(uid, wid);
    if (wid) realtime()?.setFocus(wid);
  }, [wid, uid]);
  useEffect(() => {
    const focus = focusRef.current;
    if (!focus || !uid || suspended) return;
    startRealtime({
      qc,
      workspaceId: focus,
      userId: uid,
      allWorkspaces: several,
      onNotification: (n, from) => {
        // A notification of another workspace says which, and opening it moves the focus there.
        const other = from !== useSession.getState().workspaceId;
        const name = other ? useSession.getState().memberships.find((m) => m.workspace.id === from)?.workspace.name : undefined;
        const go = (fn: () => void) => () => {
          if (other) selectWorkspace(from);
          fn();
        };
        notifyDesktop(n, (path) => router.history.push(path), from);
        soundForNotification(n);
        if (n.type === "customer_replied") return;
        if (n.type === "ai_key_failed") inWorkspace(from, () => void qc.invalidateQueries({ queryKey: qk.aiProvider }));
        if (n.type === "support_channel_unhealthy")
          inWorkspace(from, () => {
            void qc.invalidateQueries({ queryKey: qk.supportChannelConnections });
            void qc.invalidateQueries({ queryKey: qk.supportChannelOperations });
          });
        const body =
          n.type === "ai_cap_reached"
            ? "Chats go to your team until the cap resets or you raise it."
            : (billingNoticeBody[n.type] ?? importProblem(n) ?? n.ticket?.subject ?? (typeof n.data.subject === "string" ? n.data.subject : undefined));
        toast.info(notificationText(n), {
          description: name ? [name, body].filter(Boolean).join(" · ") : body,
          action: n.ticket
            ? {
                label: "Open",
                onClick: go(() => void navigate({ to: "/inbox/$folder/{-$ticketId}", params: { folder: ticketFolder(n), ticketId: n.ticket!.id } })),
              }
            : n.type === "team_joined"
              ? {
                  label: "View team",
                  onClick: go(() => {
                    const id = joinedTeam(n).id;
                    if (id) void navigate({ to: "/settings/teams/$teamId", params: { teamId: id } });
                    else void navigate({ to: "/settings/teams" });
                  }),
                }
              : n.type === "import_finished"
                ? { label: "View import", onClick: go(() => void navigate({ to: "/settings/import" })) }
              : n.type === "support_channel_unhealthy"
                ? { label: "View connection", onClick: go(() => void navigate({ to: "/settings/support-channels" })) }
              : collabThread(n)
                ? {
                    label: "Open",
                    onClick: go(
                      () =>
                        void navigate({ to: "/mail/$box/{-$threadId}", params: { box: n.type === "collab_assigned" ? "assigned" : "all", threadId: collabThread(n)! }, search: {} }),
                    ),
                  }
              : n.type === "ai_cap_reached" || n.type === "ai_key_failed"
              ? { label: "Settings", onClick: go(() => void navigate({ to: n.type === "ai_key_failed" ? "/settings/ai" : "/settings/ai/behaviour" })) }
              : billingNoticeBody[n.type]
                ? { label: "Billing", onClick: go(() => void navigate({ to: "/settings/billing" })) }
                : undefined,
        });
      },
      // New-ticket sounds are the focus workspace's: the others ring through notifications.
      onTicketCreated: (t, from) => {
        if (from === useSession.getState().workspaceId) soundForTicket(t, uid);
      },
      onSelfMembershipChanged: () => {
        void reloadSession().then((s) => {
          if (!s.workspaceId) void navigate({ to: "/onboarding" });
        });
      },
    });
    return () => stopRealtime();
  }, [socketKey, uid, suspended, several, qc, navigate, router]);
}

/** Subscribes this browser to web pushes for the session once notifications are allowed. */
function useWebPushSubscription() {
  const uid = useSession((s) => s.user?.id);
  const { data } = useQuery(q.authConfig());
  const key = data?.web_push_key;
  useEffect(() => {
    if (uid && key) void subscribeWebPush(key).catch(() => undefined);
  }, [uid, key]);
}

export function AppLayout() {
  const { t: tNav } = useTranslation("navigation");
  // The top-level area (inbox, views, contacts, settings/...) keys the route transition.
  const section = useRouterState({ select: (s) => { const p = s.location.pathname.split("/").filter(Boolean); return p[0] ?? ""; } });
  const focus = useSession((s) => s.workspaceId);
  useRealtimeConnection();
  useHeartbeat();
  useDesktopBadge();
  useDesktopPreferences();
  useWebPushSubscription();
  const navigate = useNavigate();
  const newTicketOpen = useShell((s) => s.newTicketOpen);
  const sidebarOpen = useShell((s) => s.sidebarOpen);
  const upgradeReason = useUpgrade((s) => s.reason);
  const collapsed = useNav((s) => s.collapsed);
  // The expanded sidebar's width (`--pane-nav`, `--pane-nav-live` while dragged), shared with the header's logo above it.
  const [navWidth, setNavWidth] = usePaneWidth("nav");
  const shellRef = useRef<HTMLDivElement>(null);
  const navRef = useRef<HTMLElement>(null);
  // Logging out, or deleting or leaving the last workspace, clears the session
  // before the router leaves the app; render nothing in between rather than
  // pages that read the signed-in user and the current workspace.
  const authed = useSession((s) => s.status === "authed" && !!s.user && !!s.workspaceId);
  const suspended = useSession((s) => !!currentMembership(s)?.suspended);
  useWaitingAlerts(authed && !suspended);

  const collab = useCollab();
  const spaceEditor = useSpaceUi((s) => !!s.editor);
  // On the mail pages, c writes new mail and g i goes to the mail inbox.
  const onMail = collab.enabled && section === "mail";
  // The new ticket page is the dialog's draft on a full page: New ticket there opens nothing more.
  const onNewTicket = useRouterState({ select: (s) => s.location.pathname === NEW_TICKET_PATH });
  useEffect(() => {
    if (onNewTicket && newTicketOpen) shell.newTicket(false);
  }, [onNewTicket, newTicketOpen]);

  useHotkeys({
    "mod+k": () => shell.palette(!useShell.getState().paletteOpen),
    "?": () => shell.shortcuts(),
    "shift+?": () => shell.shortcuts(),
    c: () => {
      if (onMail) compose.open();
      else if (!onNewTicket) shell.newTicket();
    },
    b: () => toggleNav(),
    "g i": () =>
      onMail
        ? void navigate({ to: "/mail/$box/{-$threadId}", params: { box: "inbox", threadId: undefined }, search: (prev: { account?: string }) => ({ account: prev.account }) })
        : void navigate({ to: "/inbox/$folder/{-$ticketId}", params: { folder: "unassigned", ticketId: undefined } }),
    "g m": () => void navigate({ to: "/inbox/$folder/{-$ticketId}", params: { folder: "mine", ticketId: undefined } }),
    "g c": () => void navigate({ to: "/contacts/{-$contactId}", params: { contactId: undefined } }),
    "g k": () => void navigate({ to: "/kb" }),
    "g w": () => void navigate({ to: "/wiki" }),
    "g r": () => void navigate({ to: "/reports" }),
    "g s": () => void navigate({ to: "/settings" }),
  });

  if (!authed) return null;
  if (suspended) return <SuspendedWorkspace />;

  return (
    <div ref={shellRef} className="fixed inset-0 flex flex-col bg-frame text-fg" style={navWidth === null ? undefined : ({ "--pane-nav": paneCss("nav", navWidth, "vw") } as CSSProperties)}>
      <AppHeader />
      {/* In the desktop app the work panel floats inset in the window frame. */}
      <div className="flex min-h-0 flex-1 desktop:pb-2 desktop:pr-2">
        <aside ref={navRef} className={cn("hidden shrink-0 transition-[width] duration-200 ease-out motion-reduce:transition-none md:flex", collapsed ? "w-14" : "w-[var(--pane-nav-live,var(--pane-nav,15rem))]")}>
          <Sidebar />
        </aside>
        {!collapsed && (
          <ResizeHandle
            pane="nav"
            side="before"
            target={navRef}
            room={() => window.innerWidth}
            width={navWidth}
            onLive={(px) => setLiveWidth(shellRef.current, "nav", px === null ? null : paneCss("nav", px, "vw"))}
            onWidth={setNavWidth}
            className="hidden md:block"
          />
        )}

        {/* Phone: the sidebar is a sheet. */}
        <D.Root open={sidebarOpen} onOpenChange={(o) => shell.sidebar(o)}>
          <D.Portal>
            <D.Overlay className="fixed inset-0 z-40 bg-scrim data-[state=open]:animate-fade-in md:hidden" />
            <D.Content
              aria-describedby={undefined}
              className="fixed inset-y-0 left-0 z-40 flex w-[272px] flex-col bg-chrome pt-2 shadow-float outline-none data-[state=open]:animate-fade-in md:hidden"
              onClick={(e) => (e.target as HTMLElement).closest("a") && shell.sidebar(false)}
            >
              <D.Title className="sr-only">{tNav("navigation")}</D.Title>
              <Sidebar drawer />
            </D.Content>
          </D.Portal>
        </D.Root>

        {/* The work panel: pure white, its inner corner softened where it meets the chrome. */}
        <main className="relative flex min-w-0 flex-1 flex-col overflow-hidden border-t border-line bg-surface md:rounded-tl-2xl md:border-l desktop:rounded-2xl desktop:border desktop:border-panel-line desktop:shadow-[0_1px_3px_rgb(0_0_0/0.04)]">
          <ConnectionBanner />
          <AiBalanceBanner />
          <DesktopUpdateBanner />
          <AllowNotificationsBanner />
          {/* Moving between areas cross-fades the panel; moving within one (ticket to ticket) does not. A page belongs
              to the focus workspace: moving the focus remounts it, so nothing keeps another workspace's keys. */}
          <div key={`${focus}:${section}`} className="flex min-h-0 flex-1 flex-col animate-route-in motion-reduce:animate-none">
            <Outlet />
          </div>
        </main>
        <AssistantDock />
      </div>

      <CommandPalette />
      <ShortcutsSheet />
      <AssistantRoot />
      {upgradeReason && (
        <Suspense fallback={null}>
          <UpgradeDialog />
        </Suspense>
      )}
      {newTicketOpen && !onNewTicket && (
        <Suspense fallback={null}>
          <NewTicketDialog />
        </Suspense>
      )}
      {collab.enabled && (
        <Suspense fallback={null}>
          <CollabRoot />
        </Suspense>
      )}
      <Suspense fallback={null}>
        <CustomizeSidebar />
      </Suspense>
      {spaceEditor && (
        <Suspense fallback={null}>
          <SpaceEditorRoot />
        </Suspense>
      )}
    </div>
  );
}
