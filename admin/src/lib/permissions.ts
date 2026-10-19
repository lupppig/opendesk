// What the signed-in admin may do (contracts/admin.md "Admins and roles"). The
// panel hides or disables what the role does not allow; the API enforces it
// either way, reading the role from the database on every request.

import type { AdminMe, AdminPermission, AdminRole } from "./api/types";

export const ROLES: AdminRole[] = ["viewer", "support", "super"];

export const ROLE_LABEL: Record<AdminRole, string> = { viewer: "Viewer", support: "Support", super: "Super" };

export const ROLE_HINT: Record<AdminRole, string> = {
  viewer: "Reads every page, changes nothing",
  support: "Sessions, suspensions, limits, AI grants, test workspaces and jobs",
  super: "Everything, including 2FA, deletion and admins",
};

/** The table in the contract: the least role that holds each permission. */
export const MIN_ROLE: Record<AdminPermission, AdminRole> = {
  read: "viewer",
  "users.sessions": "support",
  "users.suspend": "support",
  "workspaces.suspend": "support",
  "workspaces.limits": "support",
  "workspaces.test": "support",
  "jobs.manage": "support",
  "users.mfa": "super",
  "users.delete": "super",
  "workspaces.delete": "super",
  "admins.manage": "super",
};

/** Whether the admin holds a permission. The server's list is the truth; the role is not consulted. */
export function can(me: Pick<AdminMe, "permissions"> | null | undefined, perm: AdminPermission): boolean {
  return !!me && me.permissions.includes(perm);
}

/** Why an action is not available, for its tooltip; null when it is. */
export function deniedReason(me: Pick<AdminMe, "permissions"> | null | undefined, perm: AdminPermission): string | null {
  if (can(me, perm)) return null;
  const role = MIN_ROLE[perm];
  return role === "super" ? "Needs the Super admin role" : `Needs the ${ROLE_LABEL[role]} or Super admin role`;
}

/** The roles an admin may hold, highest first, for sorting. */
export function roleRank(role: AdminRole | null | undefined): number {
  return role ? ROLES.indexOf(role) : -1;
}
