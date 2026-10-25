// The desktop app's side of the page (contracts/native.md, "Desktop shell"):
// deep links and notification clicks navigate, the dock or taskbar badge follows
// the unread count, and notifications reach the OS while the window is in the
// background, when the person's preferences let them (the type's desktop channel
// is on and notifications are not paused). In a browser, only the notifications
// remain: the browser shows them once the person has allowed it.

import { useQuery } from "@tanstack/react-query";
import { useRouter } from "@tanstack/react-router";
import { useEffect } from "react";
import { safeNext } from "@/components/auth/parts";
import { i18n } from "@/i18n";
import type { Notification, NotificationPreferences } from "@/lib/api/types";
import { desktop, isDesktop } from "@/lib/desktop";
import { wantsDesktop } from "@/lib/notificationPrefs";
import { q, qk } from "@/lib/queries";
import { queryClient } from "@/lib/queryClient";
import { describe, notificationPath } from "./Notifications";
import { useBellNotifications } from "./notificationRead";
import { inWorkspace } from "@/lib/api/client";
import { withWorkspaceParam } from "@/lib/workspaceParam";

/** `opendesk://open?path=…` and notification clicks: always on, signed in or not. */
export function useDesktopNavigation() {
  const router = useRouter();
  useEffect(() => {
    if (!desktop) return;
    return desktop.onNavigate((path) => {
      const p = safeNext(path);
      if (p) router.history.push(p);
    });
  }, [router]);
}

/** Keeps the badge equal to the unread notification count: every shown workspace's with Together. */
export function useDesktopBadge() {
  const bell = useBellNotifications();
  const unread = bell.isLoading && !bell.hasAny ? undefined : bell.unread;
  useEffect(() => {
    if (desktop && unread !== undefined) desktop.setBadge(unread);
  }, [unread]);
  useEffect(() => () => desktop?.setBadge(0), []);
}

/** True when this browser has allowed the page to show notifications. */
export function browserNotificationsAllowed(): boolean {
  return typeof window !== "undefined" && "Notification" in window && window.Notification.permission === "granted";
}

/** Keeps the notification preferences loaded wherever notifyDesktop can show something, so it can follow them. */
export function useDesktopPreferences() {
  useQuery({ ...q.notificationPreferences(), enabled: isDesktop || browserNotificationsAllowed(), staleTime: 5 * 60_000 });
}

/**
 * Mirrors a new notification to the OS when the window isn't focused (the toasts
 * cover the focused window), its type's desktop channel is on and nothing is
 * paused: through the desktop app, or through the browser once it is allowed.
 * Preferences not loaded yet follow the defaults: on for every type. `open`
 * navigates to a browser notification's path when it is clicked.
 */
export function notifyDesktop(n: Notification, open: (path: string) => void, workspaceId?: string) {
  if (document.hasFocus()) return;
  if (!desktop && !browserNotificationsAllowed()) return;
  if (!wantsDesktop(inWorkspace(workspaceId, () => queryClient.getQueryData<NotificationPreferences>(qk.notificationPreferences)), n.type)) return;
  const t = i18n.getFixedT(null, "shell");
  const title = describe(n, t);
  const body = n.ticket ? `#${n.ticket.number} ${n.ticket.subject ?? ""}`.trim() : undefined;
  // The path names its workspace (`?ws=`), so a click opens it there whichever has the focus.
  const at = notificationPath(n);
  const path = at ? withWorkspaceParam(at, workspaceId) : undefined;
  if (desktop) {
    desktop.notify({ id: n.id, title, body, path });
    return;
  }
  const note = new window.Notification(title, { body, tag: n.id, icon: "/icon-192.png" });
  note.onclick = () => {
    window.focus();
    if (path) open(path);
    note.close();
  };
}
