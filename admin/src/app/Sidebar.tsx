// The sidebar sits on the grey chrome beside the white work panel: the logo with
// its Admin pill, the pages, and the signed-in admin at the foot.

import { Link } from "@tanstack/react-router";
import { ExternalLink, Keyboard, LogOut, Monitor, Moon, Sun } from "lucide-react";
import { motion, useReducedMotion } from "motion/react";
import { AdminPill, Lockup } from "@/components/brand";
import { AdminRolePill } from "@/components/pills";
import { Avatar } from "@/components/ui/avatar";
import { Kbd } from "@/components/ui/kbd";
import { Menu, MenuCheckItem, MenuContent, MenuItem, MenuLabel, MenuSeparator, MenuTrigger } from "@/components/ui/menu";
import { cn } from "@/lib/cn";
import { config } from "@/lib/config";
import { can } from "@/lib/permissions";
import { springs } from "@/lib/motion";
import { useMe } from "@/lib/session";
import { useTheme, type ThemePref } from "@/lib/theme";
import { NAV } from "./nav";

function ActivePill() {
  const reduce = useReducedMotion();
  return (
    <motion.span
      aria-hidden
      layoutId={reduce ? undefined : "admin-nav-pill"}
      transition={springs.glide}
      className="absolute inset-0 -z-10 rounded-md bg-chrome-active"
    />
  );
}

export function Sidebar({ onSignOut, onShortcuts }: { onSignOut: () => void; onShortcuts: () => void }) {
  const me = useMe();
  const { pref, setTheme } = useTheme();
  const appUrl = me.instance.app_url || config.appUrl;
  const themes: { value: ThemePref; label: string; icon: React.ReactNode }[] = [
    { value: "system", label: "System", icon: <Monitor /> },
    { value: "light", label: "Light", icon: <Sun /> },
    { value: "dark", label: "Dark", icon: <Moon /> },
  ];

  return (
    <nav aria-label="Admin" className="flex h-full w-full flex-col bg-chrome">
      <div className="flex h-12 shrink-0 items-center gap-2 px-4">
        <Link to="/" aria-label="opendesk admin overview" className="flex items-center text-fg">
          <Lockup className="h-[15px]" />
        </Link>
        <AdminPill />
      </div>

      <ul className="flex flex-col gap-px px-2.5 pt-1">
        {NAV.filter((n) => !n.perm || can(me, n.perm)).map((n) => (
          <li key={n.to}>
            <Link
              to={n.to}
              activeOptions={{ exact: n.to === "/" }}
              className="group relative isolate flex h-7 items-center gap-2.5 rounded-md px-2 text-[12.5px] text-fg-2 outline-none transition-colors hover:bg-chrome-hover hover:text-fg focus-visible:shadow-[0_0_0_2px_var(--focus)] data-[status=active]:font-medium data-[status=active]:text-fg data-[status=active]:hover:bg-transparent [&>svg]:size-[15px] [&>svg]:text-fg-3 data-[status=active]:[&>svg]:text-fg"
            >
              {({ isActive }) => (
                <>
                  {isActive && <ActivePill />}
                  <n.icon />
                  <span className="flex-1 truncate">{n.label}</span>
                  <Kbd className="opacity-0 transition-opacity group-hover:opacity-100 [&_kbd]:h-4 [&_kbd]:min-w-4 [&_kbd]:text-[9.5px]">{n.keys}</Kbd>
                </>
              )}
            </Link>
          </li>
        ))}
      </ul>

      <div className="mt-auto p-2.5">
        <Menu>
          <MenuTrigger asChild>
            <button
              type="button"
              className="flex w-full items-center gap-2.5 rounded-md px-2 py-1.5 text-left outline-none transition-colors hover:bg-chrome-hover focus-visible:shadow-[0_0_0_2px_var(--focus)] data-[state=open]:bg-chrome-active"
            >
              <Avatar name={me.name || me.email} size="lg" />
              <span className="min-w-0 flex-1">
                <span className="block truncate text-[12.5px] font-medium text-fg">{me.name || me.email}</span>
                <span className="block truncate text-[11px] text-fg-3">{me.email}</span>
              </span>
              <AdminRolePill role={me.role} />
            </button>
          </MenuTrigger>
          <MenuContent side="top" align="start" className="w-[232px]">
            <MenuLabel>Theme</MenuLabel>
            {themes.map((t) => (
              <MenuCheckItem key={t.value} checked={pref === t.value} onSelect={() => setTheme(t.value)} icon={t.icon}>
                {t.label}
              </MenuCheckItem>
            ))}
            <MenuSeparator />
            <MenuItem icon={<Keyboard />} shortcut="?" onSelect={onShortcuts}>
              Keyboard shortcuts
            </MenuItem>
            <MenuItem icon={<ExternalLink />} onSelect={() => window.open(appUrl, "_blank", "noopener,noreferrer")}>
              Open the app
            </MenuItem>
            <MenuSeparator />
            <MenuItem icon={<LogOut />} onSelect={onSignOut}>
              Sign out
            </MenuItem>
          </MenuContent>
        </Menu>
        <p className={cn("mono mt-2 truncate px-2 text-[10.5px] text-fg-4")} title={me.instance.commit}>
          {me.instance.version || "dev"}
          {me.instance.commit ? ` · ${me.instance.commit.slice(0, 7)}` : ""}
        </p>
      </div>
    </nav>
  );
}
