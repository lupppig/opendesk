// Picking and changing Spaces from the switcher, the sidebar's rows and the shortcuts: one place
// that saves the list and toasts a refusal.

import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "@/components/ui/toast";
import { inWorkspace } from "@/lib/api/client";
import type { SidebarSectionId, Space } from "@/lib/api/types";
import { normalizePrefs } from "@/lib/collab/sidebarPrefs";
import { isTypingTarget } from "@/lib/hotkeys";
import { q } from "@/lib/queries";
import { useSession } from "@/lib/session";
import { useSaveSpaces } from "@/lib/spaces/hooks";
import { copySpace, defaultSections, LIMITS, moveSpace, nextColor, removeSpace, sidebarCopy, uniqueName } from "@/lib/spaces/model";
import { pickedSpaceOf, spaceUi } from "@/lib/spaces/store";

/** Alt and a digit, as Kbd text. */
export const spaceKeys = (n: number | string) => `alt ${n}`;

/** A workspace's own sidebar: its built-in sections in its SidebarPrefs order (read with OD-Workspace set to it). */
export async function workspaceSections(qc: QueryClient, wid: string): Promise<SidebarSectionId[]> {
  const status = await qc.fetchQuery(inWorkspace(wid, () => q.collabStatus()));
  const collab = status?.enabled === true;
  const prefs = collab ? await qc.fetchQuery(inWorkspace(wid, () => q.mailSidebar())).catch(() => null) : null;
  return defaultSections(prefs ? normalizePrefs(prefs) : null, collab);
}

export function useSpaceActions(spaces: Space[] | undefined) {
  const { t } = useTranslation("spaces");
  const qc = useQueryClient();
  const uid = useSession((s) => s.user?.id ?? "");
  const save = useSaveSpaces();
  const list = spaces ?? [];
  const full = list.length >= LIMITS.spaces;
  const put = (next: Space[]) => save.mutate(next, { onError: (e) => toast.error(e, t("menu.saveFailed")) });
  return {
    full,
    put,
    /** Picks a Space, or the open workspace (null). Never switches the workspace. */
    pick: (id: string | null) => spaceUi.pick(uid, id),
    /** The editor, blank or started from a Space (a starter, a workspace's sidebar). */
    create: (from?: Space) => spaceUi.edit({ kind: "new", from }),
    /** The editor started from a workspace's own sidebar. */
    createFromWorkspace: async (w: { id: string; name: string }) => {
      try {
        const sections = await workspaceSections(qc, w.id);
        spaceUi.edit({ kind: "new", from: sidebarCopy(w, sections, uniqueName(list, w.name), nextColor(list)) });
      } catch (e) {
        toast.error(e);
      }
    },
    /** The editor started from a starter Space: a copy that is the person's own. */
    createFromStarter: (s: Space) => spaceUi.edit({ kind: "new", from: copySpace(s, uniqueName(list, s.name)) }),
    edit: (id: string) => spaceUi.edit({ kind: "space", id }),
    duplicate: (s: Space) => {
      if (full) return;
      const copy = copySpace(s, uniqueName(list, t("menu.copyName", { name: s.name })));
      const i = list.findIndex((x) => x.id === s.id);
      put([...list.slice(0, i + 1), copy, ...list.slice(i + 1)]);
      spaceUi.pick(uid, copy.id);
    },
    move: (s: Space, dir: -1 | 1) => put(moveSpace(list, s.id, dir)),
    remove: (s: Space) => {
      // The workspace's own sidebar shows again.
      if (pickedSpaceOf(uid) === s.id) spaceUi.pick(uid, null);
      put(removeSpace(list, s.id));
    },
  };
}

/** Alt+1..9 picks a Space in switcher order. Read from the digit keys (Alt changes the character on macOS), never while typing. */
export function useSpaceKeys(spaces: Space[] | undefined) {
  const ref = useRef(spaces);
  ref.current = spaces;
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.isComposing || !e.altKey || e.metaKey || e.ctrlKey || e.shiftKey) return;
      if (e.getModifierState?.("AltGraph") || isTypingTarget(e.target)) return;
      const m = /^Digit([1-9])$/.exec(e.code);
      const uid = useSession.getState().user?.id;
      const s = m && ref.current?.[Number(m[1]) - 1];
      if (!s || !uid) return;
      e.preventDefault();
      spaceUi.pick(uid, s.id);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
}
