import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useWaitingAlerts } from "@/components/tickets/useWaitingAlerts";
import { Outlet, useNavigate, useRouter, useRouterState } from "@tanstack/react-router";
import { lazy, Suspense, useEffect } from "react";
import { Dialog as D } from "radix-ui";
import { identify } from "@/lib/analytics";
import { formatUsd } from "@/lib/format";
import { useHotkeys } from "@/lib/hotkeys";
import { useHeartbeat } from "@/lib/pictures";
import { playAlertOnce, type AlertSound } from "@/lib/alertSound";
import { startRealtime, stopRealtime } from "@/lib/realtime/manager";
import { useRealtime } from "@/lib/realtime/store";
import { currentMembership, reloadSession, useSession } from "@/lib/session";
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
import { CommandPalette } from "./CommandPalette";
import { ShortcutsSheet } from "./ShortcutsSheet";
import { ConnectionBanner } from "./ConnectionBanner";
import { notifyDesktop, useDesktopBadge, useDesktopPreferences } from "./useDesktopBridge";
import { AiBalanceBanner } from "@/components/billing/AiBalanceBanner";
import { DesktopUpdateBanner } from "@/components/desktop/UpdateBanner";
import { AllowNotificationsBanner } from "@/components/notifications/AllowBanner";
import { shell, toggleNav, useNav, useShell } from "./shellStore";
import { useCollab } from "@/lib/collab/hooks";
import { compose } from "@/lib/collab/compose";

const NewTicketDialog = lazy(() => import("@/components/tickets/NewTicketDialog"));
// Collab's dialogs (customize sidebar, new mail) load only for workspaces with Collab.
const CollabRoot = lazy(() => import("@/components/collab/CollabRoot"));
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
  useEffect(() => {
    if (!wid || !uid || suspended) return;
    identify(uid, wid);
    startRealtime({
      qc,
      workspaceId: wid,
      userId: uid,
      onNotification: (n) => {
        notifyDesktop(n, (path) => router.history.push(path));
        soundForNotification(n);
        if (n.type === "customer_replied") return;
        if (n.type === "ai_key_failed") void qc.invalidateQueries({ queryKey: qk.aiProvider });
        toast.info(notificationText(n), {
          description:
            n.type === "ai_cap_reached"
              ? "Chats go to your team until the cap resets or you raise it."
              : (billingNoticeBody[n.type] ?? importProblem(n) ?? n.ticket?.subject ?? (typeof n.data.subject === "string" ? n.data.subject : undefined)),
          action: n.ticket
            ? {
                label: "Open",
                onClick: () =>
                  void navigate({ to: "/inbox/$folder/{-$ticketId}", params: { folder: ticketFolder(n), ticketId: n.ticket!.id } }),
              }
            : n.type === "team_joined"
              ? {
                  label: "View team",
                  onClick: () => {
                    const id = joinedTeam(n).id;
                    if (id) void navigate({ to: "/settings/teams/$teamId", params: { teamId: id } });
                    else void navigate({ to: "/settings/teams" });
                  },
                }
              : n.type === "import_finished"
                ? { label: "View import", onClick: () => void navigate({ to: "/settings/import" }) }
              : collabThread(n)
                ? {
                    label: "Open",
                    onClick: () =>
                      void navigate({ to: "/mail/$box/{-$threadId}", params: { box: n.type === "collab_assigned" ? "assigned" : "all", threadId: collabThread(n)! }, search: {} }),
                  }
              : n.type === "ai_cap_reached" || n.type === "ai_key_failed"
              ? { label: "Settings", onClick: () => void navigate({ to: n.type === "ai_key_failed" ? "/settings/ai" : "/settings/ai/behaviour" }) }
              : billingNoticeBody[n.type]
                ? { label: "Billing", onClick: () => void navigate({ to: "/settings/billing" }) }
                : undefined,
        });
      },
      onTicketCreated: (t) => soundForTicket(t, uid),
      onSelfMembershipChanged: () => {
        void reloadSession().then((s) => {
          if (!s.workspaceId) void navigate({ to: "/onboarding" });
        });
      },
    });
    return () => stopRealtime();
  }, [wid, uid, suspended, qc, navigate, router]);
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
  // The top-level area (inbox, views, contacts, settings/...) keys the route transition.
  const section = useRouterState({ select: (s) => { const p = s.location.pathname.split("/").filter(Boolean); return p[0] ?? ""; } });
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
  // Logging out, or deleting or leaving the last workspace, clears the session
  // before the router leaves the app; render nothing in between rather than
  // pages that read the signed-in user and the current workspace.
  const authed = useSession((s) => s.status === "authed" && !!s.user && !!s.workspaceId);
  const suspended = useSession((s) => !!currentMembership(s)?.suspended);
  useWaitingAlerts(authed && !suspended);

  const collab = useCollab();
  // On the mail pages, c writes new mail and g i goes to the mail inbox.
  const onMail = collab.enabled && section === "mail";

  useHotkeys({
    "mod+k": () => shell.palette(!useShell.getState().paletteOpen),
    "?": () => shell.shortcuts(),
    "shift+?": () => shell.shortcuts(),
    c: () => (onMail ? compose.open() : shell.newTicket()),
    b: () => toggleNav(),
    "g i": () =>
      onMail
        ? void navigate({ to: "/mail/$box/{-$threadId}", params: { box: "inbox", threadId: undefined }, search: (prev: { account?: string }) => ({ account: prev.account }) })
        : void navigate({ to: "/inbox/$folder/{-$ticketId}", params: { folder: "unassigned", ticketId: undefined } }),
    "g m": () => void navigate({ to: "/inbox/$folder/{-$ticketId}", params: { folder: "mine", ticketId: undefined } }),
    "g c": () => void navigate({ to: "/contacts/{-$contactId}", params: { contactId: undefined } }),
    "g k": () => void navigate({ to: "/kb" }),
    "g r": () => void navigate({ to: "/reports" }),
    "g s": () => void navigate({ to: "/settings" }),
  });

  if (!authed) return null;
  if (suspended) return <SuspendedWorkspace />;

  return (
    <div className="fixed inset-0 flex flex-col bg-frame text-fg">
      <AppHeader />
      {/* In the desktop app the work panel floats inset in the window frame. */}
      <div className="flex min-h-0 flex-1 desktop:pb-2 desktop:pr-2">
        <aside className={cn("hidden shrink-0 transition-[width] duration-200 ease-out motion-reduce:transition-none md:flex", collapsed ? "w-14" : "w-60")}>
          <Sidebar />
        </aside>

        {/* Phone: the sidebar is a sheet. */}
        <D.Root open={sidebarOpen} onOpenChange={(o) => shell.sidebar(o)}>
          <D.Portal>
            <D.Overlay className="fixed inset-0 z-40 bg-scrim data-[state=open]:animate-fade-in md:hidden" />
            <D.Content
              aria-describedby={undefined}
              className="fixed inset-y-0 left-0 z-40 flex w-[272px] flex-col bg-chrome pt-2 shadow-float outline-none data-[state=open]:animate-fade-in md:hidden"
              onClick={(e) => (e.target as HTMLElement).closest("a") && shell.sidebar(false)}
            >
              <D.Title className="sr-only">Navigation</D.Title>
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
          {/* Moving between areas cross-fades the panel; moving within one (ticket to ticket) does not. */}
          <div key={section} className="flex min-h-0 flex-1 flex-col animate-route-in motion-reduce:animate-none">
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
      {newTicketOpen && (
        <Suspense fallback={null}>
          <NewTicketDialog />
        </Suspense>
      )}
      {collab.enabled && (
        <Suspense fallback={null}>
          <CollabRoot />
        </Suspense>
      )}
    </div>
  );
}
