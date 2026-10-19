import { describe, expect, it } from "vitest";
import type { AdminPermission } from "./api/types";
import { can, deniedReason, MIN_ROLE, roleRank } from "./permissions";

const viewer = { permissions: ["read"] as AdminPermission[] };
const support = { permissions: ["read", "users.sessions", "users.suspend", "workspaces.suspend", "workspaces.limits", "jobs.manage"] as AdminPermission[] };

describe("permissions", () => {
  it("follow the server's list", () => {
    expect(can(viewer, "read")).toBe(true);
    expect(can(viewer, "jobs.manage")).toBe(false);
    expect(can(support, "jobs.manage")).toBe(true);
    expect(can(support, "users.delete")).toBe(false);
    expect(can(null, "read")).toBe(false);
  });

  it("name the role an action needs", () => {
    expect(deniedReason(support, "jobs.manage")).toBeNull();
    expect(deniedReason(viewer, "jobs.manage")).toBe("Needs the Support or Super admin role");
    expect(deniedReason(support, "admins.manage")).toBe("Needs the Super admin role");
    expect(deniedReason(undefined, "users.mfa")).toBe("Needs the Super admin role");
  });

  it("cover every permission in the contract", () => {
    expect(Object.keys(MIN_ROLE).sort()).toEqual(
      ["admins.manage", "jobs.manage", "read", "users.delete", "users.mfa", "users.sessions", "users.suspend", "workspaces.delete", "workspaces.limits", "workspaces.suspend", "workspaces.test"].sort(),
    );
  });

  it("rank roles", () => {
    expect(roleRank("super")).toBeGreaterThan(roleRank("support"));
    expect(roleRank("support")).toBeGreaterThan(roleRank("viewer"));
    expect(roleRank(null)).toBe(-1);
  });
});
