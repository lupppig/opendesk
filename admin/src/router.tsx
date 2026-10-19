// The route table. Every page is a lazy chunk preloaded on link intent, carries
// its tab title in staticData.title, and prefetches its first queries in its
// loader through the same query options its components read. The basepath is
// the build's base ("/" on admin.opendesk.sh, "/admin" inside the binary).

import { createRootRouteWithContext, createRoute, createRouter, lazyRouteComponent, redirect } from "@tanstack/react-router";
import type { QueryClient } from "@tanstack/react-query";
import { RootLayout } from "./app/RootLayout";
import { NotFound, RouteError, RoutePending } from "./app/RouteStates";
import { Shell } from "./app/Shell";
import { config, routerBasepath } from "./lib/config";
import { queryClient } from "./lib/queryClient";
import { q, type UserFilter, type WorkspaceFilter } from "./lib/queries";
import { nextTarget, safeNext } from "./lib/next";
import { bootSession } from "./lib/session";

export type RouterContext = { queryClient: QueryClient };

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
  interface StaticDataRouteOption {
    /** Tab title, without the suffix. */
    title?: string;
  }
}

const page = (load: () => Promise<{ default: React.ComponentType }>) => lazyRouteComponent(load);
const str = (v: unknown) => (typeof v === "string" && v !== "" ? v : undefined);
const oneOf = <T extends string>(v: unknown, all: readonly T[]): T | undefined => (all.includes(v as T) ? (v as T) : undefined);

const rootRoute = createRootRouteWithContext<RouterContext>()({
  component: RootLayout,
  notFoundComponent: NotFound,
  errorComponent: RouteError,
});

// ─── Sign-in ───────────────────────────────────────────────────────────────

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/login",
  validateSearch: (s: Record<string, unknown>): { next?: string; ended?: string } => ({ next: safeNext(s.next), ended: str(s.ended) }),
  beforeLoad: async ({ search }) => {
    const s = await bootSession();
    if (s.status === "authed") throw redirect(nextTarget(search.next) as never);
  },
  component: page(() => import("./pages/Login")),
  staticData: { title: "Sign in" },
});

// ─── The panel ─────────────────────────────────────────────────────────────

const shellRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: "shell",
  beforeLoad: async ({ location }) => {
    const s = await bootSession();
    if (s.status !== "authed") throw redirect({ to: "/login", search: { next: safeNext(location.href) } });
  },
  component: Shell,
});

const overviewRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: "/",
  loader: ({ context }) => {
    void context.queryClient.prefetchQuery(q.summary());
    void context.queryClient.prefetchQuery(q.health());
  },
  component: page(() => import("./pages/Overview")),
  staticData: { title: "Overview" },
});

const WORKSPACE_FILTERS = ["all", "suspended", "paid", "free", "collab", "test"] as const satisfies readonly WorkspaceFilter[];
const workspacesRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: "/workspaces",
  validateSearch: (s: Record<string, unknown>): { q?: string; filter?: WorkspaceFilter } => ({ q: str(s.q), filter: oneOf(s.filter, WORKSPACE_FILTERS) }),
  loaderDeps: ({ search }) => ({ q: search.q, filter: search.filter }),
  loader: ({ context, deps }) => void context.queryClient.prefetchInfiniteQuery(q.workspaces(deps)),
  component: page(() => import("./pages/Workspaces")),
  staticData: { title: "Workspaces" },
});

const workspaceRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: "/workspaces/$id",
  loader: ({ context, params }) => void context.queryClient.prefetchQuery(q.workspace(params.id)),
  component: page(() => import("./pages/WorkspaceDetail")),
  staticData: { title: "Workspace" },
});

const testWorkspacesRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: "/test-workspaces",
  loader: ({ context }) => void context.queryClient.prefetchInfiniteQuery(q.testWorkspaces()),
  component: page(() => import("./pages/TestWorkspaces")),
  staticData: { title: "Test workspaces" },
});

const USER_FILTERS = ["all", "suspended", "admins", "unverified"] as const satisfies readonly UserFilter[];
const usersRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: "/users",
  validateSearch: (s: Record<string, unknown>): { q?: string; filter?: UserFilter } => ({ q: str(s.q), filter: oneOf(s.filter, USER_FILTERS) }),
  loaderDeps: ({ search }) => ({ q: search.q, filter: search.filter }),
  loader: ({ context, deps }) => void context.queryClient.prefetchInfiniteQuery(q.users(deps)),
  component: page(() => import("./pages/Users")),
  staticData: { title: "Users" },
});

const userRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: "/users/$id",
  loader: ({ context, params }) => void context.queryClient.prefetchQuery(q.user(params.id)),
  component: page(() => import("./pages/UserDetail")),
  staticData: { title: "User" },
});

const jobsRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: "/jobs",
  validateSearch: (s: Record<string, unknown>): { state?: string; kind?: string } => ({ state: str(s.state), kind: str(s.kind) }),
  loaderDeps: ({ search }) => ({ state: search.state ?? "retryable", kind: search.kind }),
  loader: ({ context, deps }) => {
    void context.queryClient.prefetchQuery(q.jobStats());
    void context.queryClient.prefetchInfiniteQuery(q.jobs(deps));
  },
  component: page(() => import("./pages/Jobs")),
  staticData: { title: "Jobs" },
});

const healthRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: "/health",
  loader: ({ context }) => void context.queryClient.prefetchQuery(q.health()),
  component: page(() => import("./pages/Health")),
  staticData: { title: "Health" },
});

const auditRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: "/audit",
  validateSearch: (s: Record<string, unknown>): { action?: string; actor?: string; target?: string } => ({ action: str(s.action), actor: str(s.actor), target: str(s.target) }),
  loaderDeps: ({ search }) => ({ action: search.action, actor_id: search.actor, target_id: search.target }),
  loader: ({ context, deps }) => {
    void context.queryClient.prefetchInfiniteQuery(q.audit(deps));
    void context.queryClient.prefetchQuery(q.admins());
  },
  component: page(() => import("./pages/Audit")),
  staticData: { title: "Audit log" },
});

const adminsRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: "/admins",
  loader: ({ context }) => void context.queryClient.prefetchQuery(q.admins()),
  component: page(() => import("./pages/Admins")),
  staticData: { title: "Admins" },
});

export const routeTree = rootRoute.addChildren([
  loginRoute,
  shellRoute.addChildren([overviewRoute, workspacesRoute, workspaceRoute, testWorkspacesRoute, usersRoute, userRoute, jobsRoute, healthRoute, auditRoute, adminsRoute]),
]);

export const router = createRouter({
  routeTree,
  basepath: routerBasepath(config.base),
  context: { queryClient },
  defaultPreload: "intent",
  defaultPreloadStaleTime: 0,
  defaultPendingComponent: RoutePending,
  defaultPendingMs: 200,
  defaultPendingMinMs: 0,
  scrollRestoration: true,
});
