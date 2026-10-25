// Switching between the accounts signed in to in this browser and their
// workspaces: the workspace switcher, the account menu, the command menu and
// the quick switch keys all go through here.

import { useNavigate } from "@tanstack/react-router";
import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "@/components/ui/toast";
import { modKey } from "@/components/ui/kbd";
import { resetAnalytics } from "@/lib/analytics";
import { isTypingTarget } from "@/lib/hotkeys";
import { selectWorkspace, signOut, switchAccount, switcherEntries, useSession } from "@/lib/session";

/** The quick switch keys, as Kbd text: mod + alt + n. */
export function quickSwitchKeys(n: number | string) {
  return `mod ${modKey() === "⌘" ? "⌥" : "Alt"} ${n}`;
}

/** Workspaces in switcher order (grouped by account), each with its account. */
export function useSwitcherEntries() {
  const accounts = useSession((s) => s.accounts);
  const user = useSession((s) => s.user);
  const memberships = useSession((s) => s.memberships);
  return switcherEntries({ accounts, user, memberships });
}

export function useAccountActions() {
  const navigate = useNavigate();
  const { t } = useTranslation("shell");

  const land = (workspaceId: string | null) => {
    if (workspaceId) void navigate({ to: "/inbox/$folder/{-$ticketId}", params: { folder: "unassigned", ticketId: undefined } });
    else void navigate({ to: "/onboarding" });
  };

  /** Open a workspace, in whichever signed-in account it belongs to. */
  const switchTo = async (userId: string, workspaceId: string | null) => {
    const s = useSession.getState();
    if (s.user?.id === userId) {
      if (!workspaceId || workspaceId === s.workspaceId) return;
      selectWorkspace(workspaceId);
      land(workspaceId);
      return;
    }
    const email = s.accounts.find((a) => a.user.id === userId)?.user.email ?? "";
    const next = await switchAccount(userId, workspaceId);
    if (!next) {
      toast.error(t("accounts.ended", { email }));
      return;
    }
    land(next.workspaceId);
  };

  /** Sign in to another account; the current ones stay signed in. */
  const addAccount = () => void navigate({ to: "/login", search: { add: "1", next: undefined, error: undefined, email: undefined } });

  /** Log out of the active account (another signed-in account takes over), or of all of them. */
  const logOut = async (all = false) => {
    const next = await signOut({ all });
    if (next.status === "authed") land(next.workspaceId);
    else {
      resetAnalytics();
      void navigate({ to: "/login", search: { next: undefined, error: undefined, add: undefined, email: undefined } });
    }
  };

  return { switchTo, addAccount, logOut };
}

/**
 * Mod+Alt+1..9 opens the workspaces in switcher order. Read from the physical
 * digit keys (Alt changes the character on macOS), and never while typing, where
 * Ctrl+Alt is AltGr on many keyboards.
 */
export function useQuickSwitch() {
  const entries = useSwitcherEntries();
  const { switchTo } = useAccountActions();
  const ref = useRef({ entries, switchTo });
  ref.current = { entries, switchTo };
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.isComposing || !(e.metaKey || e.ctrlKey) || !e.altKey || e.shiftKey) return;
      if (e.getModifierState?.("AltGraph") || isTypingTarget(e.target)) return;
      const m = /^Digit([1-9])$/.exec(e.code);
      if (!m) return;
      const entry = ref.current.entries[Number(m[1]) - 1];
      if (!entry) return;
      e.preventDefault();
      void ref.current.switchTo(entry.account.user.id, entry.membership.workspace.id);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
}
