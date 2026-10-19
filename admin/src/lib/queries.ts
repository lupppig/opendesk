// Query keys and options, shared by the route loaders (prefetch on intent) and
// the pages that read them.

import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import { api, type AuditParams, type JobParams, type ListParams } from "./api/endpoints";
import type { Page } from "./api/types";

export const PAGE_SIZE = 50;

const pager = <T,>(fetchPage: (cursor: string | null) => Promise<Page<T>>) => ({
  queryFn: ({ pageParam }: { pageParam: string | null }) => fetchPage(pageParam),
  initialPageParam: null as string | null,
  getNextPageParam: (last: Page<T>) => last.next_cursor || null,
});

export const qk = {
  summary: ["summary"] as const,
  health: ["health"] as const,
  workspaces: ["workspaces"] as const,
  workspace: (id: string) => ["workspace", id] as const,
  testWorkspaces: ["test-workspaces"] as const,
  users: ["users"] as const,
  user: (id: string) => ["user", id] as const,
  jobs: ["jobs"] as const,
  jobStats: ["jobs", "stats"] as const,
  admins: ["admins"] as const,
  audit: ["audit"] as const,
};

export type WorkspaceFilter = "all" | "suspended" | "paid" | "free" | "collab" | "test";
export type UserFilter = "all" | "suspended" | "admins" | "unverified";

const filterParam = (f: string | undefined) => (f && f !== "all" ? f : undefined);

export const q = {
  summary: () => queryOptions({ queryKey: qk.summary, queryFn: api.summary }),
  health: () => queryOptions({ queryKey: qk.health, queryFn: api.health }),

  workspaces: (p: { q?: string; filter?: WorkspaceFilter }) =>
    infiniteQueryOptions({
      queryKey: [...qk.workspaces, "list", p.q ?? "", p.filter ?? "all"],
      ...pager((cursor) => api.workspaces.list({ q: p.q, filter: filterParam(p.filter), cursor, limit: PAGE_SIZE } satisfies ListParams)),
    }),
  workspace: (id: string) => queryOptions({ queryKey: qk.workspace(id), queryFn: () => api.workspaces.get(id) }),
  testWorkspaces: () =>
    infiniteQueryOptions({
      queryKey: [...qk.testWorkspaces, "list"],
      ...pager((cursor) => api.testWorkspaces.list({ cursor, limit: PAGE_SIZE })),
    }),

  users: (p: { q?: string; filter?: UserFilter }) =>
    infiniteQueryOptions({
      queryKey: [...qk.users, "list", p.q ?? "", p.filter ?? "all"],
      ...pager((cursor) => api.users.list({ q: p.q, filter: filterParam(p.filter), cursor, limit: PAGE_SIZE })),
    }),
  user: (id: string) => queryOptions({ queryKey: qk.user(id), queryFn: () => api.users.get(id) }),

  jobStats: () => queryOptions({ queryKey: qk.jobStats, queryFn: api.jobs.stats }),
  jobs: (p: { state: string; kind?: string }) =>
    infiniteQueryOptions({
      queryKey: [...qk.jobs, "list", p.state, p.kind ?? ""],
      ...pager((cursor) => api.jobs.list({ state: p.state, kind: p.kind, cursor, limit: PAGE_SIZE } satisfies JobParams)),
    }),

  admins: () => queryOptions({ queryKey: qk.admins, queryFn: api.admins.list }),

  audit: (p: { action?: string; actor_id?: string; target_id?: string }) =>
    infiniteQueryOptions({
      queryKey: [...qk.audit, p.action ?? "", p.actor_id ?? "", p.target_id ?? ""],
      ...pager((cursor) => api.audit({ ...p, cursor, limit: PAGE_SIZE } satisfies AuditParams)),
    }),
};
