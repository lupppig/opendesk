import { Activity, Building2, FlaskConical, LayoutDashboard, ListChecks, ScrollText, ShieldCheck, Users, type LucideIcon } from "lucide-react";
import type { AdminPermission } from "@/lib/api/types";

export type NavId = "overview" | "workspaces" | "testWorkspaces" | "users" | "jobs" | "health" | "audit" | "admins";

export type NavItem = {
  to: "/" | "/workspaces" | "/test-workspaces" | "/users" | "/jobs" | "/health" | "/audit" | "/admins";
  /** Its label is `nav.<id>` in the shell catalog, its shortcut's `shortcuts.go.<id>`. */
  id: NavId;
  icon: LucideIcon;
  keys: string;
  /** Shown only to admins who hold it (the page itself still reads for everyone). */
  perm?: AdminPermission;
};

export const NAV: NavItem[] = [
  { to: "/", id: "overview", icon: LayoutDashboard, keys: "g o" },
  { to: "/workspaces", id: "workspaces", icon: Building2, keys: "g w" },
  { to: "/test-workspaces", id: "testWorkspaces", icon: FlaskConical, keys: "g t", perm: "workspaces.test" },
  { to: "/users", id: "users", icon: Users, keys: "g u" },
  { to: "/jobs", id: "jobs", icon: ListChecks, keys: "g j" },
  { to: "/health", id: "health", icon: Activity, keys: "g h" },
  { to: "/audit", id: "audit", icon: ScrollText, keys: "g l" },
  { to: "/admins", id: "admins", icon: ShieldCheck, keys: "g a" },
];

/** The shortcuts dialog: each label is a key of the shell catalog. */
export const SHORTCUTS = [
  { keys: "/", label: "shortcuts.search" },
  ...NAV.map((n) => ({ keys: n.keys, label: `shortcuts.go.${n.id}` as const })),
  { keys: "?", label: "shortcuts.dialog" },
] as const;
