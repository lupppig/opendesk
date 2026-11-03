// The signed-in shell: an accent stripe across the top (this is the privileged
// surface), the sidebar on the grey chrome, a top bar with the instance's label,
// and the white work panel.

import { Outlet, useNavigate, useRouterState } from "@tanstack/react-router";
import { Menu as MenuIcon } from "lucide-react";
import { Dialog as D } from "radix-ui";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { AdminPill, EnvPill } from "@/components/brand";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Kbd } from "@/components/ui/kbd";
import { toast } from "@/components/ui/toast";
import { api } from "@/lib/api/endpoints";
import { config } from "@/lib/config";
import { useHotkeys } from "@/lib/hotkeys";
import { queryClient } from "@/lib/queryClient";
import { getSession, subscribeSession, useSession } from "@/lib/session";
import { NAV, SHORTCUTS } from "./nav";
import { Sidebar } from "./Sidebar";

/** Where the router goes when the session ends: sign-in, back here afterwards unless they signed out. */
function useSessionEnd() {
  const navigate = useNavigate();
  useEffect(
    () =>
      subscribeSession(() => {
        const s = getSession();
        if (s.status !== "anon") return;
        queryClient.clear();
        const here = location.pathname + location.search;
        const base = config.base.replace(/\/$/, "");
        const next = here.startsWith(base) ? here.slice(base.length) || "/" : "/";
        void navigate({
          to: "/login",
          search: s.ended === "logout" ? {} : { next: next === "/" ? undefined : next, ended: s.ended ?? "unauthenticated" },
          replace: true,
        });
      }),
    [navigate],
  );
}

function focusSearch() {
  const el = document.querySelector<HTMLInputElement>("[data-page-search]");
  if (!el) return false;
  el.focus();
  el.select();
  return true;
}

export function Shell() {
  const { t } = useTranslation("shell");
  const authed = useSession((s) => s.status === "authed");
  const navigate = useNavigate();
  const [drawer, setDrawer] = useState(false);
  const [shortcuts, setShortcuts] = useState(false);
  const section = useRouterState({ select: (s) => s.location.pathname.split("/").filter(Boolean)[0] ?? "" });
  useSessionEnd();

  const signOut = () => {
    // The panel forgets the session either way; the API ends it when it answers.
    api.auth.logout().catch(() => toast.info(t("signedOutHere"), { description: t("signedOutHereHint") }));
  };

  useHotkeys({
    "/": () => void focusSearch(),
    "?": () => setShortcuts(true),
    "shift+?": () => setShortcuts(true),
    ...Object.fromEntries(NAV.map((n) => [n.keys, () => void navigate({ to: n.to })])),
  });

  if (!authed) return null;

  return (
    <div className="fixed inset-0 flex flex-col bg-chrome text-fg">
      <div aria-hidden className="h-[3px] shrink-0 bg-admin-accent" />
      <div className="flex min-h-0 flex-1">
        <aside className="hidden w-56 shrink-0 md:flex">
          <Sidebar onSignOut={signOut} onShortcuts={() => setShortcuts(true)} />
        </aside>

        <D.Root open={drawer} onOpenChange={setDrawer}>
          <D.Portal>
            <D.Overlay className="fixed inset-0 z-40 bg-scrim data-[state=open]:animate-fade-in md:hidden" />
            <D.Content
              aria-describedby={undefined}
              className="fixed inset-y-0 left-0 z-40 flex w-[264px] shadow-float outline-none data-[state=open]:animate-fade-in md:hidden"
              onClick={(e) => (e.target as HTMLElement).closest("a") && setDrawer(false)}
            >
              <D.Title className="sr-only">{t("navigation")}</D.Title>
              <Sidebar onSignOut={signOut} onShortcuts={() => setShortcuts(true)} />
            </D.Content>
          </D.Portal>
        </D.Root>

        <div className="flex min-w-0 flex-1 flex-col">
          <div className="flex h-10 shrink-0 items-center gap-2 px-3 md:pl-1 md:pr-4">
            <Button variant="ghost" square aria-label={t("openNavigation")} className="md:hidden" icon={<MenuIcon />} onClick={() => setDrawer(true)} />
            <span className="flex items-center gap-1.5 md:hidden">
              <AdminPill />
            </span>
            <span className="hidden text-[12px] text-fg-3 md:inline">{t("instanceAdministration")}</span>
            <div className="ml-auto flex items-center gap-2">
              <EnvPill label={config.envLabel} />
            </div>
          </div>
          <main className="relative flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden border-t border-line bg-surface md:rounded-tl-2xl md:border-l">
            <div key={section} className="flex min-h-0 flex-1 flex-col animate-route-in motion-reduce:animate-none">
              <Outlet />
            </div>
          </main>
        </div>
      </div>

      <Dialog open={shortcuts} onOpenChange={setShortcuts} title={t("shortcuts.dialog")} size="sm">
        <ul className="flex flex-col divide-y divide-line-soft pb-1">
          {SHORTCUTS.map((s) => (
            <li key={s.keys} className="flex h-9 items-center justify-between text-[12.5px]">
              <span className="text-fg-2">{t(s.label)}</span>
              <Kbd>{s.keys}</Kbd>
            </li>
          ))}
        </ul>
      </Dialog>
    </div>
  );
}
