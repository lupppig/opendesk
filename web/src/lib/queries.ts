// Query keys and queryOptions factories. Hooks, route loaders and the realtime
// cache patcher all go through these, so a prefetched page hits the same key
// its component reads.

import { infiniteQueryOptions, queryOptions, type QueryClient } from "@tanstack/react-query";
import { scopeWorkspace } from "./api/client";
import { api, rows, type CollabThreadQuery, type TicketListQuery } from "./api/endpoints";
import type { Page, ReportsQuery, ReportsRange, SpacesResponse } from "./api/types";
import { normalizeSpace, normalizeSpaces } from "./spaces/model";

// Workspace-owned keys start with `ws:<workspace id>`: the cache holds several
// workspaces at once (contracts/api.md "Several workspaces") and a key of
// one is never read for another. A key is made for the workspace in scope when it is
// built: the focus workspace, or the one `inWorkspace` names. So a key is built where
// it is used, never kept in a module constant. User-level keys (the account, its
// sessions, public lookups) carry no workspace.

const SCOPE = "ws:";

/** The first element of every key of workspace `wid` (default: the one in scope). */
export function scopeKey(wid: string | null = scopeWorkspace()): string {
  return `${SCOPE}${wid ?? "none"}`;
}

const w = <T extends readonly unknown[]>(k: T) => [scopeKey(), ...k] as const;

/** The workspace a key belongs to, or null for a user-level key. */
export function keyWorkspace(key: readonly unknown[]): string | null {
  const head = key[0];
  return typeof head === "string" && head.startsWith(SCOPE) ? head.slice(SCOPE.length) : null;
}

/** The key without its workspace: what it names inside the workspace. */
export function keyBody(key: readonly unknown[]): readonly unknown[] {
  return keyWorkspace(key) === null ? key : key.slice(1);
}

/** The roots a user-level key may start with (src/lib/queryKeys.test.ts holds every literal key to it). */
export const USER_ROOTS = new Set(["auth-config", "me", "public", "invitation-lookup", "oauth", "totp-setup"]);

export const qk = {
  /** Every key of one workspace (default: the one in scope). */
  scope: (wid?: string | null) => [scopeKey(wid === undefined ? scopeWorkspace() : wid)] as const,
  /** An ad hoc workspace key: [scope, ...parts]. Prefer a named key below. */
  of: <T extends readonly unknown[]>(...parts: T) => w(parts),
  /** How the person arranges their workspaces (contracts/api.md "Several workspaces"). */
  workspacePrefs: ["me", "workspace-prefs"] as const,
  /** Every workspace's sidebar numbers in one answer (GET /v1/me/overview). */
  overview: ["me", "overview"] as const,
  /** The person's Spaces (contracts/spaces.md): one list, the same in every workspace. */
  spaces: ["me", "spaces"] as const,
  /** The workspace's starter Space. */
  get starter() {
    return w(["sidebar-starter"] as const);
  },
  authConfig: ["auth-config"] as const,
  get workspace() {
    return w(["workspace"] as const);
  },
  sessions: ["me", "sessions"] as const,
  get counts() {
    return w(["tickets", "counts"] as const);
  },
  get ticketLists() {
    return w(["tickets", "list"] as const);
  },
  tickets: (q: TicketListQuery) => w(["tickets", "list", q] as const),
  ticket: (id: string) => w(["tickets", "detail", id] as const),
  messages: (id: string) => w(["tickets", "messages", id] as const),
  activities: (id: string) => w(["tickets", "activities", id] as const),
  /** Third-party threads (contracts/api.md "Threads"). */
  threads: (id: string) => w(["tickets", "threads", id] as const),
  /** Ollie's pending suggested reply on an email ticket. */
  suggestedReply: (id: string) => w(["tickets", "suggested-reply", id] as const),
  /** The ticket's shared draft (contracts/api.md "Drafts"). */
  ticketDraft: (id: string) => w(["tickets", "draft", id] as const),
  get contactLists() {
    return w(["contacts", "list"] as const);
  },
  contacts: (q: { q?: string; company_id?: string }) => w(["contacts", "list", q] as const),
  contact: (id: string) => w(["contacts", "detail", id] as const),
  contactTickets: (id: string) => w(["contacts", "tickets", id] as const),
  contactPicker: (q: string) => w(["contacts", "picker", q] as const),
  get companyLists() {
    return w(["companies", "list"] as const);
  },
  companies: (q: { q?: string }) => w(["companies", "list", q] as const),
  company: (id: string) => w(["companies", "detail", id] as const),
  get tags() {
    return w(["tags"] as const);
  },
  get tagGroups() {
    return w(["tag-groups"] as const);
  },
  /** Every entity's custom fields, archived included; filter by `entity` when reading. */
  get customFields() {
    return w(["custom-fields"] as const);
  },
  get savedReplies() {
    return w(["saved-replies"] as const);
  },
  get templateVariables() {
    return w(["templates", "variables"] as const);
  },
  emailTemplates: (brandId: string) => w(["email-templates", brandId] as const),
  ticketVariables: (id: string, mailboxId: string | null) => w(["tickets", "variables", id, mailboxId] as const),
  get views() {
    return w(["views"] as const);
  },
  get slaPolicies() {
    return w(["sla-policies"] as const);
  },
  get automations() {
    return w(["automations"] as const);
  },
  get mailboxes() {
    return w(["mailboxes"] as const);
  },
  get members() {
    return w(["members"] as const);
  },
  get invitations() {
    return w(["invitations"] as const);
  },
  get teams() {
    return w(["teams"] as const);
  },
  get brands() {
    return w(["brands"] as const);
  },
  get widgets() {
    return w(["widgets"] as const);
  },
  get bots() {
    return w(["bots"] as const);
  },
  get routingRules() {
    return w(["routing-rules"] as const);
  },
  get apiKeys() {
    return w(["api-keys"] as const);
  },
  get webhooks() {
    return w(["webhooks"] as const);
  },
  webhookDeliveries: (id: string) => w(["webhooks", "deliveries", id] as const),
  get integrations() {
    return w(["integrations"] as const);
  },
  get integrationCatalog() {
    return w(["integrations", "catalog"] as const);
  },
  get supportChannelProviders() {
    return w(["support-channels", "providers"] as const);
  },
  get supportChannelConnections() {
    return w(["support-channels", "connections"] as const);
  },
  get supportChannelDestinations() {
    return w(["support-channels", "destinations"] as const);
  },
  supportChannelAvailableDestinations: (id: string) => w(["support-channels", "connections", id, "destinations"] as const),
  get imports() {
    return w(["imports"] as const);
  },
  ticketLinks: (id: string) => w(["tickets", "links", id] as const),
  get auditLogs() {
    return w(["audit-logs"] as const);
  },
  get notifications() {
    return w(["notifications"] as const);
  },
  /** The caller's notification preferences in the current workspace (the cache is cleared on a workspace switch). */
  get notificationPreferences() {
    return w(["me", "notification-preferences"] as const);
  },
  /** What teammates' Ollie may ask the caller's, and the asks made (contracts/ai.md "Asking a teammate"). */
  get assistantPeer() {
    return w(["me", "assistant-peer"] as const);
  },
  get assistantPeerAsks() {
    return w(["me", "assistant-peer", "asks"] as const);
  },
  get kbCollections() {
    return w(["kb", "collections"] as const);
  },
  get kbArticleLists() {
    return w(["kb", "articles"] as const);
  },
  kbArticles: (q: { collection_id?: string; status?: string; q?: string; for_brand_id?: string }) => w(["kb", "articles", q] as const),
  kbArticle: (id: string) => w(["kb", "article", id] as const),
  search: (q: string) => w(["search", q] as const),
  reports: (r: ReportsQuery) => w(["reports", r] as const),
  get widgetSettings() {
    return w(["widget-settings"] as const);
  },
  csat: (token: string) => ["public", "csat", token] as const,
  invitationLookup: (token: string) => ["invitation-lookup", token] as const,
  get billing() {
    return w(["billing"] as const);
  },
  billingUsage: (r: { from: string; to: string }) => w(["billing", "usage", r] as const),
  get billingInvoices() {
    return w(["billing", "invoices"] as const);
  },
  get aiConfig() {
    return w(["ai", "config"] as const);
  },
  aiSearch: (q: string) => w(["ai", "search", q] as const),
  get aiModels() {
    return w(["ai", "models"] as const);
  },
  get aiProvider() {
    return w(["ai", "provider"] as const);
  },
  agentReport: (r: ReportsRange) => w(["ai", "agent", "report", r] as const),
  get agentSetup() {
    return w(["ai", "agent", "setup"] as const);
  },
  get aiClientServer() {
    return w(["ai-clients", "server"] as const);
  },
  get aiClientConnections() {
    return w(["ai-clients", "connections"] as const);
  },
  oauthRequest: (id: string) => ["oauth", "request", id] as const,
  get mcpServers() {
    return w(["ai", "mcp-servers"] as const);
  },
  get knowledge() {
    return w(["ai", "knowledge"] as const);
  },
  knowledgeDocs: (id: string) => w(["ai", "knowledge", "docs", id] as const),
  get agentTests() {
    return w(["ai", "agent", "tests"] as const);
  },
  get agentTestRuns() {
    return w(["ai", "agent", "test-runs"] as const);
  },
  agentTestRun: (id: string) => w(["ai", "agent", "test-runs", id] as const),
  get agentReview() {
    return w(["ai", "agent", "review"] as const);
  },
  get assignment() {
    return w(["assignment"] as const);
  },
  get triage() {
    return w(["triage"] as const);
  },
  get businessHours() {
    return w(["business-hours"] as const);
  },
  get onboarding() {
    return w(["onboarding"] as const);
  },
  // Collab (contracts/collab.md): everything under "collab", so a workspace switch or a refetch reaches it all.
  get collab() {
    return w(["collab"] as const);
  },
  get collabStatus() {
    return w(["collab", "status"] as const);
  },
  get mailAccounts() {
    return w(["collab", "accounts"] as const);
  },
  /** With the shared accounts of people who left, for admins (settings). */
  get mailAccountsManage() {
    return w(["collab", "accounts", "manage"] as const);
  },
  mailFolders: (accountId: string) => w(["collab", "folders", accountId] as const),
  get mailCounts() {
    return w(["collab", "counts"] as const);
  },
  get mailSidebar() {
    return w(["collab", "sidebar"] as const);
  },
  get mailThreadLists() {
    return w(["collab", "threads"] as const);
  },
  mailThreads: (q: CollabThreadQuery) => w(["collab", "threads", q] as const),
  mailThread: (id: string) => w(["collab", "thread", id] as const),
  get mailPeople() {
    return w(["collab", "person"] as const);
  },
  mailPerson: (email: string, exclude: string) => w(["collab", "person", email, exclude] as const),
  // Outside "collab": a message's source never changes, and reading it is a call to the provider.
  mailMessageDetails: (id: string) => w(["collabMessageDetails", id] as const),
};

const list = <R>(key: readonly unknown[], fn: () => Promise<R[] | Page<R> | { data: R[] }>, opts: { staleTime?: number } = {}) =>
  queryOptions({ queryKey: key, queryFn: async () => rows(await fn()), ...opts });

async function allPages<R>(fn: (q: { limit: number; cursor: string | null }) => Promise<Page<R>>) {
  const all: R[] = [];
  let cursor: string | null = null;
  do {
    const page = await fn({ limit: 100, cursor });
    all.push(...page.data);
    cursor = page.next_cursor;
  } while (cursor);
  return all;
}

export const q = {
  authConfig: () => queryOptions({ queryKey: qk.authConfig, queryFn: api.auth.config, staleTime: 5 * 60_000 }),
  workspacePrefs: () => queryOptions({ queryKey: qk.workspacePrefs, queryFn: api.me.workspacePrefs, staleTime: 5 * 60_000 }),
  overview: () => queryOptions({ queryKey: qk.overview, queryFn: async () => (await api.me.overview()).workspaces, staleTime: 30_000 }),
  /** The user's Spaces, every field present. No events announce changes, so focus refetches them. */
  spaces: () =>
    queryOptions({
      queryKey: qk.spaces,
      queryFn: async (): Promise<SpacesResponse> => {
        const r = await api.me.spaces.get();
        return { spaces: normalizeSpaces(r?.spaces), saved: r?.saved !== false };
      },
      staleTime: 60_000,
      refetchOnWindowFocus: true,
    }),
  /** The workspace's starter Space, or null (another workspace's with inWorkspace). */
  starter: () => queryOptions({ queryKey: qk.starter, queryFn: async () => normalizeSpace((await api.starterSpace.get())?.space), staleTime: 60_000 }),
  workspace: () => queryOptions({ queryKey: qk.workspace, queryFn: api.workspaces.current }),
  sessions: () => list(qk.sessions, api.me.sessions),
  counts: () => queryOptions({ queryKey: qk.counts, queryFn: api.tickets.counts, staleTime: 30_000 }),
  tickets: (query: TicketListQuery) =>
    infiniteQueryOptions({
      queryKey: qk.tickets(query),
      queryFn: ({ pageParam }) => api.tickets.list({ ...query, limit: 50, cursor: pageParam }),
      initialPageParam: null as string | null,
      getNextPageParam: (last) => last.next_cursor,
      staleTime: 15_000,
    }),
  ticket: (id: string) => queryOptions({ queryKey: qk.ticket(id), queryFn: () => api.tickets.get(id) }),
  messages: (id: string) => list(qk.messages(id), () => api.tickets.messages(id)),
  activities: (id: string) => list(qk.activities(id), () => api.tickets.activities(id)),
  threads: (id: string) => list(qk.threads(id), () => api.tickets.threads(id)),
  suggestedReply: (id: string) =>
    queryOptions({ queryKey: qk.suggestedReply(id), queryFn: async () => (await api.ai.suggestedReply.get(id)).suggested_reply }),
  ticketDraft: (id: string) => queryOptions({ queryKey: qk.ticketDraft(id), queryFn: async () => (await api.tickets.draft.get(id)).draft }),
  contacts: (query: { q?: string; company_id?: string }) =>
    infiniteQueryOptions({
      queryKey: qk.contacts(query),
      queryFn: ({ pageParam }) => api.contacts.search({ ...query, limit: 50, cursor: pageParam }),
      initialPageParam: null as string | null,
      getNextPageParam: (last) => last.next_cursor,
    }),
  contact: (id: string) => queryOptions({ queryKey: qk.contact(id), queryFn: () => api.contacts.get(id) }),
  contactTickets: (id: string) => list(qk.contactTickets(id), () => api.contacts.tickets(id, { limit: 20 })),
  companies: (query: { q?: string }) =>
    infiniteQueryOptions({
      queryKey: qk.companies(query),
      queryFn: ({ pageParam }) => api.companies.search({ ...query, limit: 50, cursor: pageParam }),
      initialPageParam: null as string | null,
      getNextPageParam: (last) => last.next_cursor,
    }),
  company: (id: string) => queryOptions({ queryKey: qk.company(id), queryFn: () => api.companies.get(id) }),
  tags: () => list(qk.tags, () => api.tags.list()),
  tagGroups: () => list(qk.tagGroups, () => api.tagGroups.list()),
  customFields: () => list(qk.customFields, () => api.customFields.list()),
  savedReplies: () => list(qk.savedReplies, () => api.savedReplies.list({ limit: 100 })),
  templateVariables: () => queryOptions({ queryKey: qk.templateVariables, queryFn: api.templates.variables, staleTime: 5 * 60_000 }),
  emailTemplates: (brandId: string) => queryOptions({ queryKey: qk.emailTemplates(brandId), queryFn: () => api.emailTemplates.get(brandId) }),
  ticketVariables: (id: string, mailboxId: string | null) =>
    queryOptions({ queryKey: qk.ticketVariables(id, mailboxId), queryFn: () => api.tickets.variables(id, mailboxId), staleTime: 30_000 }),
  views: () => list(qk.views, () => api.views.list({ limit: 100 })),
  slaPolicies: () => list(qk.slaPolicies, () => api.slaPolicies.list({ limit: 100 })),
  automations: () => list(qk.automations, () => api.automations.list({ limit: 100 })),
  mailboxes: () => list(qk.mailboxes, () => api.mailboxes.list({ limit: 100 })),
  members: () => queryOptions({ queryKey: qk.members, queryFn: () => allPages(api.members.list) }),
  invitations: () => list(qk.invitations, () => api.invitations.list()),
  teams: () => list(qk.teams, () => api.teams.list({ limit: 100 })),
  brands: () => list(qk.brands, () => api.brands.list({ limit: 100 })),
  widgets: () => list(qk.widgets, () => api.widgets.list({ limit: 100 })),
  bots: () => list(qk.bots, () => api.bots.list()),
  routingRules: () => list(qk.routingRules, () => api.routingRules.list({ limit: 200 })),
  apiKeys: () => list(qk.apiKeys, () => api.apiKeys.list({ limit: 100 })),
  webhooks: () => list(qk.webhooks, () => api.webhooks.list({ limit: 100 })),
  webhookDeliveries: (id: string) => list(qk.webhookDeliveries(id), () => api.webhooks.deliveries(id, { limit: 50 })),
  integrations: () => list(qk.integrations, () => api.integrations.list()),
  integrationCatalog: () => list(qk.integrationCatalog, () => api.integrations.catalog(), { staleTime: 60 * 60_000 }),
  supportChannelProviders: () => list(qk.supportChannelProviders, api.supportChannels.providers, { staleTime: 60 * 60_000 }),
  supportChannelConnections: () => list(qk.supportChannelConnections, api.supportChannels.connections),
  supportChannelDestinations: () => list(qk.supportChannelDestinations, api.supportChannels.destinations),
  supportChannelAvailableDestinations: (id: string) =>
    list(qk.supportChannelAvailableDestinations(id), () => api.supportChannels.availableDestinations(id), { staleTime: 30_000 }),
  imports: () => list(qk.imports, () => api.imports.list()),
  ticketLinks: (id: string) => list(qk.ticketLinks(id), () => api.tickets.links(id)),
  auditLogs: () =>
    infiniteQueryOptions({
      queryKey: qk.auditLogs,
      queryFn: ({ pageParam }) => api.auditLogs({ limit: 50, cursor: pageParam }),
      initialPageParam: null as string | null,
      getNextPageParam: (last) => last.next_cursor,
    }),
  notifications: () => list(qk.notifications, () => api.notifications.list({ limit: 50 })),
  notificationPreferences: () => queryOptions({ queryKey: qk.notificationPreferences, queryFn: api.notifications.preferences }),
  assistantPeer: () => queryOptions({ queryKey: qk.assistantPeer, queryFn: api.assistantPeer.get, retry: false }),
  assistantPeerAsks: () =>
    infiniteQueryOptions({
      queryKey: qk.assistantPeerAsks,
      queryFn: ({ pageParam }) => api.assistantPeer.asks({ limit: 20, cursor: pageParam }),
      initialPageParam: null as string | null,
      getNextPageParam: (last) => last.next_cursor,
    }),
  kbCollections: () => list(qk.kbCollections, () => api.kb.collections.list({ limit: 100 })),
  kbArticles: (query: { collection_id?: string; status?: "draft" | "published"; q?: string; for_brand_id?: string }) =>
    list(qk.kbArticles(query), () => api.kb.articles.search({ ...query, limit: 100 })),
  kbArticle: (id: string) => queryOptions({ queryKey: qk.kbArticle(id), queryFn: () => api.kb.articles.get(id) }),
  search: (term: string) =>
    queryOptions({ queryKey: qk.search(term), queryFn: () => api.search(term), enabled: term.trim().length > 1, staleTime: 10_000 }),
  reports: (r: ReportsQuery) => queryOptions({ queryKey: qk.reports(r), queryFn: () => api.reports.overview(r) }),
  billing: () => queryOptions({ queryKey: qk.billing, queryFn: api.billing.get, staleTime: 60_000 }),
  billingUsage: (r: { from: string; to: string }) => queryOptions({ queryKey: qk.billingUsage(r), queryFn: () => api.billing.usage(r) }),
  billingInvoices: () => queryOptions({ queryKey: qk.billingInvoices, queryFn: async () => (await api.billing.invoices()).data, staleTime: 30_000 }),
  /** AI availability. A failure (older server, ai_disabled) reads as "off". */
  aiConfig: () =>
    queryOptions({
      queryKey: qk.aiConfig,
      queryFn: () => api.ai.config().catch(() => null),
      staleTime: 5 * 60_000,
    }),
  aiSearch: (query: string) => queryOptions({ queryKey: qk.aiSearch(query), queryFn: () => api.ai.search(query), enabled: query.trim().length > 1 }),
  aiModels: () => queryOptions({ queryKey: qk.aiModels, queryFn: api.ai.models, staleTime: 10 * 60_000 }),
  aiProvider: () => queryOptions({ queryKey: qk.aiProvider, queryFn: api.ai.provider.get, staleTime: 60_000 }),
  agentReport: (r: ReportsRange) => queryOptions({ queryKey: qk.agentReport(r), queryFn: () => api.ai.agent.report(r) }),
  agentSetup: () => queryOptions({ queryKey: qk.agentSetup, queryFn: api.ai.agent.setup }),
  aiClientServer: () => queryOptions({ queryKey: qk.aiClientServer, queryFn: api.aiClients.server, staleTime: 5 * 60_000 }),
  aiClientConnections: () => list(qk.aiClientConnections, api.aiClients.connections),
  oauthRequest: (id: string) => queryOptions({ queryKey: qk.oauthRequest(id), queryFn: () => api.oauth.request(id), retry: false, staleTime: Infinity }),
  mcpServers: () => list(qk.mcpServers, () => api.ai.mcp.list()),
  knowledge: () => list(qk.knowledge, () => api.ai.knowledge.list()),
  agentTests: () => list(qk.agentTests, () => api.ai.agent.tests.list()),
  agentTestRuns: () => list(qk.agentTestRuns, () => api.ai.agent.runs.list()),
  agentTestRun: (id: string) => queryOptions({ queryKey: qk.agentTestRun(id), queryFn: () => api.ai.agent.runs.get(id) }),
  agentReview: () => list(qk.agentReview, () => api.ai.agent.review.list()),
  knowledgeDocs: (id: string) =>
    infiniteQueryOptions({
      queryKey: qk.knowledgeDocs(id),
      queryFn: ({ pageParam }) => api.ai.knowledge.docs(id, { limit: 50, cursor: pageParam }),
      initialPageParam: null as string | null,
      getNextPageParam: (last) => last.next_cursor,
    }),
  widgetSettings: () => queryOptions({ queryKey: qk.widgetSettings, queryFn: api.widgetSettings.get }),
  assignment: () => queryOptions({ queryKey: qk.assignment, queryFn: api.assignment.get }),
  triage: () => queryOptions({ queryKey: qk.triage, queryFn: api.triage.get }),
  businessHours: () => queryOptions({ queryKey: qk.businessHours, queryFn: api.businessHours.get }),
  onboarding: () => queryOptions({ queryKey: qk.onboarding, queryFn: api.onboarding.get, staleTime: 30_000 }),
  /** Collab availability. A failure (an older server without the route) reads as "off". */
  collabStatus: () =>
    queryOptions({
      queryKey: qk.collabStatus,
      queryFn: () => api.collab.status().catch(() => null),
      staleTime: 5 * 60_000,
    }),
  mailAccounts: () => list(qk.mailAccounts, () => api.collab.accounts.list(), { staleTime: 30_000 }),
  mailAccountsManage: () => list(qk.mailAccountsManage, () => api.collab.accounts.list({ manage: true }), { staleTime: 30_000 }),
  mailFolders: (accountId: string) => list(qk.mailFolders(accountId), () => api.collab.accounts.folders(accountId), { staleTime: 60_000 }),
  mailCounts: () => queryOptions({ queryKey: qk.mailCounts, queryFn: api.collab.counts, staleTime: 30_000 }),
  mailSidebar: () => queryOptions({ queryKey: qk.mailSidebar, queryFn: api.collab.sidebar.get, staleTime: 5 * 60_000 }),
  mailThreads: (query: CollabThreadQuery) =>
    infiniteQueryOptions({
      queryKey: qk.mailThreads(query),
      queryFn: ({ pageParam }) => api.collab.threads.list({ ...query, limit: 50, cursor: pageParam }),
      initialPageParam: null as string | null,
      getNextPageParam: (last) => last.next_cursor,
      staleTime: 15_000,
    }),
  mailThread: (id: string) => queryOptions({ queryKey: qk.mailThread(id), queryFn: () => api.collab.threads.get(id) }),
  mailPerson: (email: string, exclude: string) =>
    queryOptions({ queryKey: qk.mailPerson(email, exclude), queryFn: () => api.collab.person({ email, exclude }), staleTime: 60_000 }),
  mailMessageDetails: (id: string) =>
    queryOptions({ queryKey: qk.mailMessageDetails(id), queryFn: () => api.collab.messageDetails(id), staleTime: Infinity, gcTime: 30 * 60_000, retry: 1 }),
  csat: (token: string) => queryOptions({ queryKey: qk.csat(token), queryFn: () => api.public.csat(token), retry: false }),
  invitationLookup: (token: string) =>
    queryOptions({ queryKey: qk.invitationLookup(token), queryFn: () => api.invitations.lookup(token), retry: false }),
};

/** Upsert or remove one row in a plain list cache keyed by id. */
export function upsertInList<R extends { id: string }>(qc: QueryClient, key: readonly unknown[], row: R, opts: { prepend?: boolean } = {}) {
  qc.setQueryData<R[]>(key, (old) => {
    if (!old) return old;
    const i = old.findIndex((x) => x.id === row.id);
    if (i >= 0) {
      const next = old.slice();
      next[i] = row;
      return next;
    }
    return opts.prepend ? [row, ...old] : [...old, row];
  });
}

export function removeFromList<R extends { id: string }>(qc: QueryClient, key: readonly unknown[], id: string) {
  qc.setQueryData<R[]>(key, (old) => old?.filter((x) => x.id !== id));
}
