// The route table. Every page is a lazy chunk preloaded on link intent, carries
// its tab title in staticData.title, and prefetches its first queries in its
// loader through the same queryOptions its components read.

import type navigation from "./i18n/locales/en/navigation.json";
import {
  createRootRouteWithContext,
  createRoute,
  createRouter,
  lazyRouteComponent,
  redirect,
} from "@tanstack/react-router";
import type { QueryClient } from "@tanstack/react-query";
import { isPublicPath, loadAgentCatalogs } from "./i18n";
import { RootLayout } from "./app/RootLayout";
import { AppLayout } from "./app/AppLayout";
import { AuthLayout } from "./app/AuthLayout";
import { NotFound, RouteError, RoutePending } from "./app/RouteStates";
import { bootSession, openLinkedWorkspace } from "./lib/session";
import { stripWorkspaceParam, workspaceParam } from "./lib/workspaceParam";
import { queryClient } from "./lib/queryClient";
import { q } from "./lib/queries";
import { folderById, listQuery, parseInboxSearch, teamQuery } from "./lib/folders";
import { isMailRouteBox, mailListQuery, parseMailSearch } from "./lib/collab/boxes";

export type RouterContext = { queryClient: QueryClient };

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
  interface StaticDataRouteOption {
    /** Navigation title key, without the brand. */
    title?: keyof typeof navigation.titles;
  }
}

const page = (load: () => Promise<{ default: React.ComponentType }>) => lazyRouteComponent(load);

const str = (v: unknown) => (typeof v === "string" ? v : undefined);

// ─── Guards ────────────────────────────────────────────────────────────────

async function requireAuth(href: string) {
  const s = await bootSession();
  if (s.status !== "authed") throw redirect({ to: "/login", search: { next: href } });
  return s;
}

async function requireWorkspace(href: string) {
  const s = await requireAuth(href);
  if (!s.workspaceId) throw redirect({ to: "/onboarding" });
  return s;
}

/** Sign-in pages, except in add-account mode, where the signed-in accounts stay. */
async function guestOnly({ search }: { search: { add?: string } }) {
  const s = await bootSession();
  if (s.status === "authed" && search.add !== "1") throw redirect({ to: "/" });
}

const addMode = (v: unknown) => (v === "1" || v === 1 || v === true ? "1" : undefined);

// ─── Root ──────────────────────────────────────────────────────────────────

const rootRoute = createRootRouteWithContext<RouterContext>()({
  // The agent app's catalogs in a language other than English load before its
  // first page renders; public pages load their own (src/i18n/index.ts).
  loader: ({ location }) => (isPublicPath(location.pathname) ? undefined : loadAgentCatalogs()),
  component: RootLayout,
  notFoundComponent: NotFound,
  errorComponent: RouteError,
});

// ─── Public (no app chrome) ────────────────────────────────────────────────

// Help centers are rendered by the API (contracts/teams.md "Help center addresses").
// An old /help/<slug>/... link of the app goes on to the help center's address.
const helpRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/help/$slug/$",
  component: page(() => import("./pages/public/HelpRedirect")),
  staticData: { title: "helpCenter" },
});

// The survey token rides in the fragment (/csat#t=<token>&score=); /csat/$token serves
// links sent before, and moves them to the fragment form.
const csatRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/csat",
  component: page(() => import("./pages/public/Csat")),
  staticData: { title: "rateYourConversation" },
});

const csatLegacyRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/csat/$token",
  component: page(() => import("./pages/public/Csat")),
  staticData: { title: "rateYourConversation" },
});

// ─── Auth ──────────────────────────────────────────────────────────────────

const authLayout = createRoute({ getParentRoute: () => rootRoute, id: "auth", component: AuthLayout });

const loginRoute = createRoute({
  getParentRoute: () => authLayout,
  path: "/login",
  // add=1: add another account, keeping the ones signed in; email prefills the address.
  validateSearch: (s: Record<string, unknown>): { next?: string; error?: string; add?: string; email?: string } => ({
    next: str(s.next),
    error: str(s.error),
    add: addMode(s.add),
    email: str(s.email),
  }),
  beforeLoad: guestOnly,
  loader: ({ context }) => context.queryClient.prefetchQuery(q.authConfig()),
  component: page(() => import("./pages/auth/Login")),
  staticData: { title: "logIn" },
});
const registerRoute = createRoute({
  getParentRoute: () => authLayout,
  path: "/register",
  validateSearch: (s: Record<string, unknown>): { next?: string; email?: string; add?: string } => ({ next: str(s.next), email: str(s.email), add: addMode(s.add) }),
  beforeLoad: guestOnly,
  loader: ({ context }) => context.queryClient.prefetchQuery(q.authConfig()),
  component: page(() => import("./pages/auth/Register")),
  staticData: { title: "createYourAccount" },
});
const forgotRoute = createRoute({
  getParentRoute: () => authLayout,
  path: "/forgot",
  component: page(() => import("./pages/auth/Forgot")),
  loader: ({ context }) => context.queryClient.prefetchQuery(q.authConfig()),
  staticData: { title: "resetYourPassword" },
});
const resetRoute = createRoute({
  getParentRoute: () => authLayout,
  path: "/reset",
  validateSearch: (s: Record<string, unknown>): { token?: string } => ({ token: str(s.token) }),
  component: page(() => import("./pages/auth/Reset")),
  staticData: { title: "chooseANewPassword" },
});
const verifyRoute = createRoute({
  getParentRoute: () => authLayout,
  path: "/verify",
  validateSearch: (s: Record<string, unknown>): { token?: string } => ({ token: str(s.token) }),
  component: page(() => import("./pages/auth/Verify")),
  staticData: { title: "verifyYourEmail" },
});
const callbackRoute = createRoute({
  getParentRoute: () => authLayout,
  path: "/auth/callback",
  validateSearch: (s: Record<string, unknown>): { code?: string; error?: string; next?: string } => ({ code: str(s.code), error: str(s.error), next: str(s.next) }),
  component: page(() => import("./pages/auth/Callback")),
  staticData: { title: "signingIn" },
});
// Where a sign-in that ran in a popup window returns; it hands the code to the tab that opened it.
const popupDoneRoute = createRoute({
  getParentRoute: () => authLayout,
  path: "/auth/popup",
  validateSearch: (s: Record<string, unknown>): { code?: string; error?: string; popup?: string } => ({ code: str(s.code), error: str(s.error), popup: str(s.popup) }),
  component: page(() => import("./pages/auth/PopupDone")),
  staticData: { title: "signingIn" },
});
// The footer link of notification emails. It never acts on load (link scanners open it), only on the click.
const unsubscribeRoute = createRoute({
  getParentRoute: () => authLayout,
  path: "/unsubscribe",
  validateSearch: (s: Record<string, unknown>): { token?: string } => ({ token: str(s.token) }),
  component: page(() => import("./pages/auth/Unsubscribe")),
  staticData: { title: "notificationEmails" },
});
const inviteRoute = createRoute({
  getParentRoute: () => authLayout,
  path: "/invite",
  validateSearch: (s: Record<string, unknown>): { token?: string } => ({ token: str(s.token) }),
  component: page(() => import("./pages/auth/Invite")),
  staticData: { title: "joinWorkspace" },
});

// The MCP OAuth consent page: an AI client asks to connect; signing in is required.
const oauthConsentRoute = createRoute({
  getParentRoute: () => authLayout,
  path: "/oauth/consent",
  validateSearch: (s: Record<string, unknown>): { request?: string; error?: string } => ({ request: str(s.request), error: str(s.error) }),
  beforeLoad: ({ location }) => requireAuth(location.href),
  loaderDeps: ({ search }) => ({ request: search.request }),
  loader: ({ context, deps }) => {
    if (deps.request && /^[0-9a-f-]{36}$/i.test(deps.request)) void context.queryClient.prefetchQuery(q.oauthRequest(deps.request));
  },
  component: page(() => import("./pages/auth/OAuthConsent")),
  staticData: { title: "connectAnApp" },
});

const onboardingRoute = createRoute({
  getParentRoute: () => authLayout,
  path: "/onboarding",
  beforeLoad: ({ location }) => requireAuth(location.href),
  component: page(() => import("./pages/onboarding/Workspace")),
  staticData: { title: "createAWorkspace" },
});
const onboardingMailboxRoute = createRoute({
  getParentRoute: () => authLayout,
  path: "/onboarding/mailbox",
  beforeLoad: ({ location }) => requireWorkspace(location.href),
  component: page(() => import("./pages/onboarding/Mailbox")),
  staticData: { title: "connectAMailbox" },
});

// ─── App ───────────────────────────────────────────────────────────────────

const appLayout = createRoute({
  getParentRoute: () => rootRoute,
  id: "app",
  beforeLoad: async ({ location }) => {
    await requireWorkspace(location.href);
    // ?ws=<workspace id> (notification email links): open that workspace when this
    // person is in it, then drop the parameter so it never reaches a page's search.
    const ws = workspaceParam(location.search as Record<string, unknown>);
    if (ws === undefined) return;
    if (ws) await openLinkedWorkspace(ws);
    throw redirect({ href: stripWorkspaceParam(location.href), replace: true });
  },
  loader: ({ context }) => {
    // The shell's own data: counts and views for the sidebar, members/tags/teams
    // for pickers everywhere. Started together, awaited for the sidebar only.
    const qc = context.queryClient;
    void qc.prefetchQuery(q.members());
    void qc.prefetchQuery(q.tags());
    void qc.prefetchQuery(q.teams());
    void qc.prefetchQuery(q.notifications());
    // Collab's mail sections join the sidebar when the workspace has it.
    void qc.prefetchQuery(q.collabStatus());
    return Promise.all([qc.ensureQueryData(q.counts()), qc.ensureQueryData(q.views())]);
  },
  component: AppLayout,
});

const indexRoute = createRoute({
  getParentRoute: () => appLayout,
  path: "/",
  beforeLoad: () => {
    throw redirect({ to: "/inbox/$folder/{-$ticketId}", params: { folder: "unassigned", ticketId: undefined } });
  },
});

const inboxRoute = createRoute({
  getParentRoute: () => appLayout,
  path: "/inbox/$folder/{-$ticketId}",
  validateSearch: parseInboxSearch,
  loaderDeps: ({ search }) => search,
  loader: ({ context, params, deps }) => {
    const f = folderById(params.folder);
    if (!f) throw redirect({ to: "/inbox/$folder/{-$ticketId}", params: { folder: "unassigned", ticketId: undefined } });
    const qc = context.queryClient;
    if (params.ticketId) prefetchTicket(qc, params.ticketId);
    prefetchInboxShared(qc);
    return qc.ensureInfiniteQueryData(q.tickets(listQuery(f.query, deps)));
  },
  component: page(() => import("./pages/inbox/InboxPage")),
  staticData: { title: "inbox" },
});

// The Everywhere lists (contracts/api.md "Workspaces together"): every workspace's, one request each.
const everywhereRoute = createRoute({
  getParentRoute: () => appLayout,
  path: "/everywhere/$list",
  beforeLoad: ({ params }) => {
    if (params.list !== "mine" && params.list !== "mail") throw redirect({ to: "/everywhere/$list", params: { list: "mine" } });
  },
  loader: ({ context }) => {
    void context.queryClient.prefetchQuery(q.overview());
  },
  component: page(() => import("./pages/everywhere/EverywherePage")),
  staticData: { title: "everywhere" },
});

const viewRoute = createRoute({
  getParentRoute: () => appLayout,
  path: "/views/$viewId/{-$ticketId}",
  validateSearch: parseInboxSearch,
  loaderDeps: ({ search }) => search,
  loader: ({ context, params, deps }) => {
    const qc = context.queryClient;
    if (params.ticketId) prefetchTicket(qc, params.ticketId);
    prefetchInboxShared(qc);
    return qc.ensureInfiniteQueryData(q.tickets(listQuery({ view_id: params.viewId }, deps)));
  },
  component: page(() => import("./pages/inbox/InboxPage")),
  staticData: { title: "view" },
});

// A team's inbox; teamId "mine" is every team the caller is on.
const teamRoute = createRoute({
  getParentRoute: () => appLayout,
  path: "/teams/$teamId/{-$ticketId}",
  validateSearch: parseInboxSearch,
  loaderDeps: ({ search }) => search,
  loader: ({ context, params, deps }) => {
    const qc = context.queryClient;
    if (params.ticketId) prefetchTicket(qc, params.ticketId);
    prefetchInboxShared(qc);
    return qc.ensureInfiniteQueryData(q.tickets(listQuery(teamQuery(params.teamId, deps.tab), deps)));
  },
  component: page(() => import("./pages/inbox/InboxPage")),
  staticData: { title: "team" },
});

// Collab mail (contracts/collab.md): a box, or a folder (`folder` search), across accounts or
// one (`account`). Without Collab on the workspace the mail pages are not there.
async function requireCollab(qc: QueryClient) {
  const status = await qc.ensureQueryData(q.collabStatus());
  if (!status?.enabled) throw redirect({ to: "/inbox/$folder/{-$ticketId}", params: { folder: "unassigned", ticketId: undefined } });
}

const mailIndexRoute = createRoute({
  getParentRoute: () => appLayout,
  path: "/mail",
  beforeLoad: () => {
    throw redirect({ to: "/mail/$box/{-$threadId}", params: { box: "inbox", threadId: undefined }, search: {} });
  },
});

const mailRoute = createRoute({
  getParentRoute: () => appLayout,
  path: "/mail/$box/{-$threadId}",
  validateSearch: parseMailSearch,
  loaderDeps: ({ search }) => search,
  loader: async ({ context, params, deps }) => {
    const qc = context.queryClient;
    await requireCollab(qc);
    if (!isMailRouteBox(params.box) || (params.box === "folder" && !deps.folder))
      throw redirect({ to: "/mail/$box/{-$threadId}", params: { box: "inbox", threadId: undefined }, search: {} });
    void qc.prefetchQuery(q.mailAccounts());
    void qc.prefetchQuery(q.mailSidebar());
    void qc.prefetchQuery(q.members());
    if (params.threadId) void qc.prefetchQuery(q.mailThread(params.threadId));
    return qc.ensureInfiniteQueryData(q.mailThreads(mailListQuery(params.box, deps)));
  },
  component: page(() => import("./pages/mail/MailPage")),
  staticData: { title: "mail" },
});

/** Addresses and brands name the chips on every row and the reply address. */
function prefetchInboxShared(qc: QueryClient) {
  void qc.prefetchQuery(q.mailboxes());
  void qc.prefetchQuery({ ...q.brands(), retry: false });
}

function prefetchTicket(qc: QueryClient, id: string) {
  void qc.prefetchQuery(q.ticket(id));
  // A ticket number in the URL resolves through the ticket; messages need the uuid.
  if (/^[0-9a-f-]{36}$/i.test(id)) {
    void qc.prefetchQuery(q.messages(id));
    void qc.prefetchQuery(q.activities(id));
    void qc.prefetchQuery(q.threads(id));
  }
  void qc.prefetchQuery(q.savedReplies());
  void qc.prefetchQuery(q.slaPolicies());
}

const contactsRoute = createRoute({
  getParentRoute: () => appLayout,
  path: "/contacts/{-$contactId}",
  validateSearch: (s: Record<string, unknown>): { q?: string; company?: string } => ({ q: str(s.q), company: str(s.company) }),
  loaderDeps: ({ search }) => search,
  loader: ({ context, params, deps }) => {
    if (params.contactId) {
      void context.queryClient.prefetchQuery(q.contact(params.contactId));
      void context.queryClient.prefetchQuery(q.contactTickets(params.contactId));
    }
    return context.queryClient.ensureInfiniteQueryData(q.contacts({ q: deps.q, company_id: deps.company }));
  },
  component: page(() => import("./pages/contacts/ContactsPage")),
  staticData: { title: "contacts" },
});

const companiesRoute = createRoute({
  getParentRoute: () => appLayout,
  path: "/companies/{-$companyId}",
  validateSearch: (s: Record<string, unknown>): { q?: string } => ({ q: str(s.q) }),
  loaderDeps: ({ search }) => search,
  loader: ({ context, params, deps }) => {
    if (params.companyId) {
      void context.queryClient.prefetchQuery(q.company(params.companyId));
      void context.queryClient.prefetchInfiniteQuery(q.contacts({ company_id: params.companyId }));
    }
    return context.queryClient.ensureInfiniteQueryData(q.companies({ q: deps.q }));
  },
  component: page(() => import("./pages/contacts/CompaniesPage")),
  staticData: { title: "companies" },
});

const kbRoute = createRoute({
  getParentRoute: () => appLayout,
  path: "/kb",
  // brand: what that brand's help center shows, its own articles and the shared ones.
  validateSearch: (s: Record<string, unknown>): { collection?: string; status?: "draft" | "published"; q?: string; brand?: string } => ({
    collection: str(s.collection),
    status: s.status === "draft" || s.status === "published" ? (s.status as "draft" | "published") : undefined,
    q: str(s.q),
    brand: str(s.brand),
  }),
  loaderDeps: ({ search }) => search,
  loader: ({ context, deps }) => {
    void context.queryClient.prefetchQuery({ ...q.brands(), retry: false });
    return Promise.all([
      context.queryClient.ensureQueryData(q.kbCollections()),
      context.queryClient.ensureQueryData(q.kbArticles({ collection_id: deps.collection, status: deps.status, q: deps.q, for_brand_id: deps.brand })),
    ]);
  },
  component: page(() => import("./pages/kb/KbPage")),
  staticData: { title: "knowledgeBase" },
});

const kbArticleRoute = createRoute({
  getParentRoute: () => appLayout,
  path: "/kb/articles/$articleId",
  // `title` prefills a new article (the agent report's "Write an article").
  validateSearch: (s: Record<string, unknown>): { collection?: string; title?: string } => ({ collection: str(s.collection), title: str(s.title)?.slice(0, 200) }),
  loader: ({ context, params }) => {
    void context.queryClient.prefetchQuery(q.kbCollections());
    void context.queryClient.prefetchQuery({ ...q.brands(), retry: false });
    if (params.articleId !== "new") return context.queryClient.ensureQueryData(q.kbArticle(params.articleId));
  },
  component: page(() => import("./pages/kb/ArticleEditor")),
  staticData: { title: "article" },
});

export function defaultRange() {
  const to = new Date();
  const from = new Date(to.getTime() - 29 * 86400_000);
  return { from: from.toISOString().slice(0, 10), to: to.toISOString().slice(0, 10) };
}

const isDay = (v: unknown): v is string => typeof v === "string" && /^\d{4}-\d{2}-\d{2}$/.test(v);

/** The reports range from the URL, or the last 30 days. */
export function reportsRange(s: { from?: string; to?: string }) {
  const d = defaultRange();
  return { from: s.from ?? d.from, to: s.to ?? d.to };
}

/** The overview query from the URL: the range, and the team (`none`: no team) and brand filters. */
export function reportsQuery(s: { from?: string; to?: string; team?: string; brand?: string }) {
  return { ...reportsRange(s), team_id: s.team, brand_id: s.brand };
}

const isRef = (v: unknown): v is string => typeof v === "string" && /^[\w-]{1,64}$/.test(v);

const reportsRoute = createRoute({
  getParentRoute: () => appLayout,
  path: "/reports",
  validateSearch: (s: Record<string, unknown>): { from?: string; to?: string; team?: string; brand?: string } => ({
    from: isDay(s.from) ? s.from : undefined,
    to: isDay(s.to) ? s.to : undefined,
    team: isRef(s.team) ? s.team : undefined,
    brand: isRef(s.brand) ? s.brand : undefined,
  }),
  loaderDeps: ({ search }) => search,
  loader: ({ context, deps }) => {
    // The filters' names: teams and brands.
    void context.queryClient.prefetchQuery(q.teams());
    void context.queryClient.prefetchQuery({ ...q.brands(), retry: false });
    return context.queryClient.ensureQueryData(q.reports(reportsQuery(deps)));
  },
  component: page(() => import("./pages/reports/ReportsPage")),
  staticData: { title: "reports" },
});

// ─── Settings ──────────────────────────────────────────────────────────────

const settingsLayout = createRoute({
  getParentRoute: () => appLayout,
  path: "/settings",
  component: page(() => import("./pages/settings/SettingsLayout")),
  staticData: { title: "settings" },
});

const settingsIndex = createRoute({
  getParentRoute: () => settingsLayout,
  path: "/",
  beforeLoad: () => {
    throw redirect({ to: "/settings/profile" });
  },
});

type Prefetch = (qc: QueryClient) => unknown;
function settingsPage<P extends string>(path: P, title: keyof typeof navigation.titles, load: () => Promise<{ default: React.ComponentType }>, prefetch?: Prefetch) {
  return createRoute({
    getParentRoute: () => settingsLayout,
    path,
    loader: prefetch ? ({ context }) => prefetch(context.queryClient) : undefined,
    component: page(load),
    staticData: { title },
  });
}

const sProfile = settingsPage("/profile", "profile", () => import("./pages/settings/Profile"));
const sSecurity = settingsPage("/security", "security", () => import("./pages/settings/Security"), (qc) => qc.ensureQueryData(q.sessions()));
const sNotifications = settingsPage("/notifications", "notifications", () => import("./pages/settings/Notifications"), (qc) =>
  qc.ensureQueryData(q.notificationPreferences()),
);
// What teammates' Ollie may ask the member's (contracts/ai.md "Asking a teammate"); Collab's mail only.
const sAgentToAgent = settingsPage("/agent-to-agent", "agentToAgent", () => import("./pages/settings/AgentToAgent"), async (qc) => {
  const status = await qc.ensureQueryData(q.collabStatus());
  if (!status?.enabled) throw redirect({ to: "/settings/profile" });
  void qc.prefetchQuery(q.members());
  void qc.prefetchQuery(q.teams());
  void qc.prefetchInfiniteQuery(q.assistantPeerAsks());
  return qc.ensureQueryData(q.assistantPeer()).catch(() => null);
});
const sWorkspaces = settingsPage("/workspaces", "workspaces", () => import("./pages/settings/Workspaces"), (qc) =>
  Promise.all([qc.ensureQueryData(q.workspacePrefs()), qc.prefetchQuery(q.overview())]),
);
const sWorkspace = settingsPage("/workspace", "general", () => import("./pages/settings/WorkspaceSettings"), (qc) =>
  Promise.all([qc.ensureQueryData(q.workspace()), qc.ensureQueryData(q.members())]),
);
const sMembers = settingsPage("/members", "members", () => import("./pages/settings/Members"), (qc) =>
  Promise.all([qc.ensureQueryData(q.members()), qc.ensureQueryData(q.invitations())]),
);
const sTeams = settingsPage("/teams", "teams", () => import("./pages/settings/Teams"), (qc) => {
  void qc.prefetchQuery(q.mailboxes());
  return Promise.all([qc.ensureQueryData(q.teams()), qc.ensureQueryData(q.members())]);
});
// Teams, brands, widgets and routing (contracts/teams.md).
const sTeamDetail = createRoute({
  getParentRoute: () => settingsLayout,
  path: "/teams/$teamId",
  loader: ({ context }) => {
    const qc = context.queryClient;
    void qc.prefetchQuery(q.mailboxes());
    void qc.prefetchQuery(q.businessHours());
    void qc.prefetchQuery(q.assignment());
    return Promise.all([qc.ensureQueryData(q.teams()), qc.ensureQueryData(q.members())]);
  },
  component: page(() => import("./pages/settings/TeamDetail")),
  staticData: { title: "team" },
});
// Each brand's email layout and satisfaction email (contracts/templates.md).
const sEmailDesign = settingsPage("/email-design", "emailDesign", () => import("./pages/settings/EmailDesign"), (qc) => {
  void qc.prefetchQuery(q.templateVariables());
  return qc.ensureQueryData(q.brands());
});
const sBrands = settingsPage("/brands", "brands", () => import("./pages/settings/Brands"), (qc) => qc.ensureQueryData(q.brands()));
const sBrandDetail = createRoute({
  getParentRoute: () => settingsLayout,
  path: "/brands/$brandId",
  validateSearch: (s: Record<string, unknown>): { tab?: "overview" | "addresses" | "widgets" | "help" } => ({
    tab: s.tab === "addresses" || s.tab === "widgets" || s.tab === "help" ? s.tab : undefined,
  }),
  loader: ({ context }) => {
    const qc = context.queryClient;
    void qc.prefetchQuery(q.mailboxes());
    void qc.prefetchQuery(q.teams());
    return Promise.all([qc.ensureQueryData(q.brands()), qc.ensureQueryData(q.widgets())]);
  },
  component: page(() => import("./pages/settings/BrandDetail")),
  staticData: { title: "brand" },
});
const sWidgetDetail = createRoute({
  getParentRoute: () => settingsLayout,
  path: "/brands/$brandId/widgets/$widgetId",
  loader: ({ context }) => {
    const qc = context.queryClient;
    void qc.prefetchQuery(q.members());
    void qc.prefetchQuery(q.teams());
    void qc.prefetchQuery(q.aiConfig());
    void qc.prefetchQuery(q.bots());
    return Promise.all([qc.ensureQueryData(q.widgets()), qc.ensureQueryData(q.brands()), qc.ensureQueryData(q.workspace())]);
  },
  component: page(() => import("./pages/settings/WidgetDetail")),
  staticData: { title: "chatWidget" },
});
const sRouting = settingsPage("/routing", "routing", () => import("./pages/settings/Routing"), (qc) => {
  void qc.prefetchQuery(q.teams());
  void qc.prefetchQuery(q.mailboxes());
  void qc.prefetchQuery(q.widgets());
  void qc.prefetchQuery(q.brands());
  return qc.ensureQueryData(q.routingRules());
});
const sRoutingRule = createRoute({
  getParentRoute: () => settingsLayout,
  path: "/routing/$ruleId",
  loader: ({ context }) => {
    const qc = context.queryClient;
    void qc.prefetchQuery(q.teams());
    void qc.prefetchQuery(q.mailboxes());
    void qc.prefetchQuery(q.widgets());
    void qc.prefetchQuery(q.brands());
    void qc.prefetchQuery(q.customFields());
    return qc.ensureQueryData(q.routingRules());
  },
  component: page(() => import("./pages/settings/RoutingRuleEdit")),
  staticData: { title: "routingRule" },
});
const sMailboxes = createRoute({
  getParentRoute: () => settingsLayout,
  path: "/mailboxes",
  validateSearch: (s: Record<string, unknown>): { connected?: string; error?: string } => ({ connected: str(s.connected), error: str(s.error) }),
  loader: ({ context }) => context.queryClient.ensureQueryData(q.mailboxes()),
  component: page(() => import("./pages/settings/Mailboxes")),
  staticData: { title: "mailboxes" },
});
const sSupportChannels = settingsPage("/support-channels", "supportChannels", () => import("./pages/settings/SupportChannels"), (qc) =>
  Promise.all([
    qc.ensureQueryData(q.supportChannelProviders()),
    qc.ensureQueryData(q.supportChannelConnections()),
    qc.ensureQueryData(q.supportChannelDestinations()),
    qc.ensureQueryData(q.teams()),
  ]),
);
const sMailboxNew = createRoute({
  getParentRoute: () => settingsLayout,
  path: "/mailboxes/new",
  validateSearch: (s: Record<string, unknown>): { brand?: string } => ({ brand: str(s.brand) }),
  loader: ({ context }) => {
    void context.queryClient.prefetchQuery(q.brands());
    return context.queryClient.prefetchQuery(q.teams());
  },
  component: page(() => import("./pages/settings/MailboxNew")),
  staticData: { title: "addAMailbox" },
});
const sMailboxDetail = createRoute({
  getParentRoute: () => settingsLayout,
  path: "/mailboxes/$mailboxId",
  loader: ({ context }) => {
    void context.queryClient.prefetchQuery(q.bots());
    return context.queryClient.ensureQueryData(q.mailboxes());
  },
  component: page(() => import("./pages/settings/MailboxDetail")),
  staticData: { title: "mailbox" },
});
// Collab mail accounts (contracts/collab.md), listed in settings only while Collab is on.
const sMailAccounts = createRoute({
  getParentRoute: () => settingsLayout,
  path: "/mail-accounts",
  validateSearch: (s: Record<string, unknown>): { connected?: string; error?: string } => ({ connected: str(s.connected), error: str(s.error) }),
  loader: async ({ context }) => {
    const qc = context.queryClient;
    const status = await qc.ensureQueryData(q.collabStatus());
    void qc.prefetchQuery(q.teams());
    void qc.prefetchQuery(q.members());
    if (status?.enabled) return qc.ensureQueryData(q.mailAccounts());
  },
  component: page(() => import("./pages/settings/MailAccounts")),
  staticData: { title: "mailAccounts" },
});
const sMailAccountDetail = createRoute({
  getParentRoute: () => settingsLayout,
  path: "/mail-accounts/$accountId",
  loader: async ({ context }) => {
    const qc = context.queryClient;
    await requireCollab(qc);
    void qc.prefetchQuery(q.teams());
    void qc.prefetchQuery(q.members());
    return qc.ensureQueryData(q.mailAccounts());
  },
  component: page(() => import("./pages/settings/MailAccountDetail")),
  staticData: { title: "mailAccount" },
});
const sTags = settingsPage("/tags", "labels", () => import("./pages/settings/Tags"), (qc) => qc.ensureQueryData(q.tags()));
const sCustomFields = settingsPage("/custom-fields", "customFields", () => import("./pages/settings/CustomFields"));
const sAssignment = settingsPage("/assignment", "assignment", () => import("./pages/settings/Assignment"), (qc) =>
  Promise.all([qc.ensureQueryData(q.assignment()), qc.ensureQueryData(q.members())]),
);
const sTriage = settingsPage("/triage", "triage", () => import("./pages/settings/Triage"), (qc) => qc.ensureQueryData(q.triage()));
const sBusinessHours = settingsPage("/business-hours", "businessHours", () => import("./pages/settings/BusinessHours"), (qc) =>
  qc.ensureQueryData(q.businessHours()),
);
const sSavedReplies = settingsPage("/saved-replies", "savedReplies", () => import("./pages/settings/SavedReplies"), (qc) =>
  qc.ensureQueryData(q.savedReplies()),
);
const sViews = settingsPage("/views", "views", () => import("./pages/settings/Views"), (qc) => qc.ensureQueryData(q.views()));
const sSla = settingsPage("/sla", "slaPolicies", () => import("./pages/settings/Sla"), (qc) => qc.ensureQueryData(q.slaPolicies()));
const sAutomations = settingsPage("/automations", "automations", () => import("./pages/settings/Automations"), (qc) =>
  qc.ensureQueryData(q.automations()),
);
const sAutomationEdit = createRoute({
  getParentRoute: () => settingsLayout,
  path: "/automations/$automationId",
  loader: ({ context }) => {
    const qc = context.queryClient;
    void qc.prefetchQuery(q.mailboxes());
    void qc.prefetchQuery(q.teams());
    return qc.ensureQueryData(q.automations());
  },
  component: page(() => import("./pages/settings/AutomationEdit")),
  staticData: { title: "automation" },
});
// The old single widget page: the default brand's first widget, else the brand's widgets.
const sWidget = createRoute({
  getParentRoute: () => settingsLayout,
  path: "/widget",
  beforeLoad: async ({ context }) => {
    const [brands, widgets] = await Promise.all([context.queryClient.ensureQueryData(q.brands()), context.queryClient.ensureQueryData(q.widgets())]);
    const brand = brands.find((b) => b.is_default) ?? brands[0];
    const widget = widgets.filter((w) => w.brand_id === brand?.id).sort((a, b) => a.position - b.position)[0];
    if (brand && widget) throw redirect({ to: "/settings/brands/$brandId/widgets/$widgetId", params: { brandId: brand.id, widgetId: widget.id } });
    if (brand) throw redirect({ to: "/settings/brands/$brandId", params: { brandId: brand.id }, search: { tab: "widgets" } });
    throw redirect({ to: "/settings/brands" });
  },
});
const sApiKeys = settingsPage("/api-keys", "apiKeys", () => import("./pages/settings/ApiKeys"), (qc) => qc.ensureQueryData(q.apiKeys()));
// Connecting AI clients to opendesk's MCP server (contracts/agent.md "opendesk MCP server").
const sAiClients = settingsPage("/ai-clients", "aiClients", () => import("./pages/settings/AiClients"), (qc) =>
  Promise.all([qc.ensureQueryData(q.aiClientServer()), qc.ensureQueryData(q.aiClientConnections())]),
);
const sWebhooks = settingsPage("/webhooks", "webhooks", () => import("./pages/settings/Webhooks"), (qc) => qc.ensureQueryData(q.webhooks()));
const sWebhookDetail = createRoute({
  getParentRoute: () => settingsLayout,
  path: "/webhooks/$webhookId",
  loader: ({ context, params }) =>
    Promise.all([
      context.queryClient.ensureQueryData(q.webhooks()),
      context.queryClient.ensureQueryData(q.webhookDeliveries(params.webhookId)),
    ]),
  component: page(() => import("./pages/settings/WebhookDetail")),
  staticData: { title: "webhook" },
});
const sAudit = settingsPage("/audit-log", "auditLog", () => import("./pages/settings/AuditLog"), (qc) =>
  qc.ensureInfiniteQueryData(q.auditLogs()),
);

const sBilling = createRoute({
  getParentRoute: () => settingsLayout,
  path: "/billing",
  validateSearch: (s: Record<string, unknown>): { checkout?: string; top_up?: string; payment_method?: string } => ({
    checkout: str(s.checkout),
    top_up: str(s.top_up),
    payment_method: str(s.payment_method),
  }),
  loader: ({ context }) => context.queryClient.ensureQueryData(q.billing()),
  component: page(() => import("./pages/settings/Billing")),
  staticData: { title: "planAndBilling" },
});
const sAi = settingsPage("/ai", "ai", () => import("./pages/settings/AiSettings"), (qc) => {
  void qc.prefetchQuery(q.aiModels());
  return qc.ensureQueryData(q.aiConfig());
});

// The widget AI agent (contracts/agent.md).
const sAgent = settingsPage("/ai/agent", "aiAgent", () => import("./pages/settings/agent/AgentOverview"), (qc) => {
  void qc.prefetchQuery(q.aiConfig());
  void qc.prefetchQuery(q.agentSetup());
  void qc.prefetchQuery(q.agentReview());
  return qc.ensureQueryData(q.agentReport(defaultRange()));
});
const sAgentBehaviour = settingsPage("/ai/behaviour", "agentBehaviour", () => import("./pages/settings/agent/AgentBehaviour"), (qc) => {
  void qc.prefetchQuery(q.aiModels());
  void qc.prefetchQuery(q.bots());
  void qc.prefetchQuery(q.widgetSettings());
  return Promise.all([qc.ensureQueryData(q.aiConfig()), qc.ensureQueryData(q.workspace())]);
});
// The bots customers talk to (contracts/bots.md).
const sAgentBots = settingsPage("/ai/bots", "bots", () => import("./pages/settings/agent/Bots"), (qc) => {
  void qc.prefetchQuery(q.widgets());
  void qc.prefetchQuery(q.mailboxes());
  return qc.ensureQueryData(q.bots());
});
const sAgentBot = createRoute({
  getParentRoute: () => settingsLayout,
  path: "/ai/bots/$botId",
  validateSearch: (s: Record<string, unknown>): { tab?: "personality" | "test" } => ({
    tab: s.tab === "personality" || s.tab === "test" ? s.tab : undefined,
  }),
  loader: ({ context }) => {
    const qc = context.queryClient;
    void qc.prefetchQuery(q.widgetSettings());
    void qc.prefetchQuery(q.members());
    void qc.prefetchQuery(q.aiConfig());
    return Promise.all([qc.ensureQueryData(q.bots()), qc.ensureQueryData(q.workspace())]);
  },
  component: page(() => import("./pages/settings/agent/BotDetail")),
  staticData: { title: "bot" },
});
const sAgentKnowledge = settingsPage("/ai/knowledge", "knowledge", () => import("./pages/settings/agent/AgentKnowledge"), (qc) =>
  qc.ensureQueryData(q.knowledge()),
);
const sAgentTools = createRoute({
  getParentRoute: () => settingsLayout,
  path: "/ai/tools",
  validateSearch: (s: Record<string, unknown>): { connected?: string; error?: string } => ({ connected: str(s.connected), error: str(s.error) }),
  loader: ({ context }) => context.queryClient.ensureQueryData(q.mcpServers()),
  component: page(() => import("./pages/settings/agent/AgentTools")),
  staticData: { title: "agentTools" },
});
const sAgentPlayground = settingsPage("/ai/playground", "agentPlayground", () => import("./pages/settings/agent/AgentPlayground"), (qc) => {
  void qc.prefetchQuery(q.aiModels());
  void qc.prefetchQuery(q.bots());
  void qc.prefetchQuery(q.widgetSettings());
  return Promise.all([qc.ensureQueryData(q.aiConfig()), qc.ensureQueryData(q.workspace())]);
});
const sAgentTests = settingsPage("/ai/tests", "agentTests", () => import("./pages/settings/agent/AgentTests"), (qc) => {
  void qc.prefetchQuery(q.agentTestRuns());
  void qc.prefetchQuery(q.mcpServers());
  return Promise.all([qc.ensureQueryData(q.agentTests()), qc.ensureQueryData(q.aiConfig())]);
});
const sIntegrations = createRoute({
  getParentRoute: () => settingsLayout,
  path: "/integrations",
  validateSearch: (s: Record<string, unknown>): { connected?: string; error?: string } => ({ connected: str(s.connected), error: str(s.error) }),
  loader: ({ context }) => {
    void context.queryClient.prefetchQuery(q.integrationCatalog());
    return context.queryClient.ensureQueryData(q.integrations());
  },
  component: page(() => import("./pages/settings/Integrations")),
  staticData: { title: "integrations" },
});

const sImport = createRoute({
  getParentRoute: () => settingsLayout,
  path: "/import",
  validateSearch: (s: Record<string, unknown>): { id?: string } => ({ id: str(s.id) }),
  loader: ({ context }) => {
    const qc = context.queryClient;
    void qc.prefetchQuery(q.members());
    void qc.prefetchQuery(q.teams());
    void qc.prefetchQuery(q.mailboxes());
    return qc.ensureQueryData(q.imports());
  },
  component: page(() => import("./pages/settings/Import")),
  staticData: { title: "import" },
});

const routeTree = rootRoute.addChildren([
  helpRoute,
  csatRoute,
  csatLegacyRoute,
  authLayout.addChildren([
    loginRoute,
    registerRoute,
    forgotRoute,
    resetRoute,
    verifyRoute,
    callbackRoute,
    popupDoneRoute,
    inviteRoute,
    unsubscribeRoute,
    oauthConsentRoute,
    onboardingRoute,
    onboardingMailboxRoute,
  ]),
  appLayout.addChildren([
    indexRoute,
    inboxRoute,
    everywhereRoute,
    viewRoute,
    teamRoute,
    mailIndexRoute,
    mailRoute,
    contactsRoute,
    companiesRoute,
    kbRoute,
    kbArticleRoute,
    reportsRoute,
    settingsLayout.addChildren([
      settingsIndex,
      sProfile,
      sSecurity,
      sNotifications,
      sAgentToAgent,
      sWorkspaces,
      sWorkspace,
      sMembers,
      sTeams,
      sTeamDetail,
      sBrands,
      sBrandDetail,
      sWidgetDetail,
      sEmailDesign,
      sRouting,
      sRoutingRule,
      sMailboxes,
      sSupportChannels,
      sMailboxNew,
      sMailboxDetail,
      sMailAccounts,
      sMailAccountDetail,
      sTags,
      sCustomFields,
      sAssignment,
      sTriage,
      sBusinessHours,
      sSavedReplies,
      sViews,
      sSla,
      sAutomations,
      sAutomationEdit,
      sWidget,
      sApiKeys,
      sAiClients,
      sWebhooks,
      sWebhookDetail,
      sAudit,
      sBilling,
      sAi,
      sAgent,
      sAgentBehaviour,
      sAgentBots,
      sAgentBot,
      sAgentKnowledge,
      sAgentTools,
      sAgentPlayground,
      sAgentTests,
      sIntegrations,
      sImport,
    ]),
  ]),
]);

export const router = createRouter({
  routeTree,
  context: { queryClient },
  defaultPreload: "intent",
  defaultPreloadStaleTime: 0,
  defaultPendingComponent: RoutePending,
  defaultPendingMs: 200,
  defaultPendingMinMs: 0,
  scrollRestoration: true,
});
