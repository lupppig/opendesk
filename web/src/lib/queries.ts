// Query keys and queryOptions factories. Hooks, route loaders and the realtime
// cache patcher all go through these, so a prefetched page hits the same key
// its component reads.

import { infiniteQueryOptions, queryOptions, type QueryClient } from "@tanstack/react-query";
import { api, rows, type CollabThreadQuery, type TicketListQuery } from "./api/endpoints";
import type { Page, ReportsQuery, ReportsRange } from "./api/types";

export const qk = {
  authConfig: ["auth-config"] as const,
  workspace: ["workspace"] as const,
  sessions: ["me", "sessions"] as const,
  counts: ["tickets", "counts"] as const,
  ticketLists: ["tickets", "list"] as const,
  tickets: (q: TicketListQuery) => ["tickets", "list", q] as const,
  ticket: (id: string) => ["tickets", "detail", id] as const,
  messages: (id: string) => ["tickets", "messages", id] as const,
  activities: (id: string) => ["tickets", "activities", id] as const,
  /** Third-party threads (contracts/api.md "Threads"). */
  threads: (id: string) => ["tickets", "threads", id] as const,
  /** Ollie's pending suggested reply on an email ticket. */
  suggestedReply: (id: string) => ["tickets", "suggested-reply", id] as const,
  contactLists: ["contacts", "list"] as const,
  contacts: (q: { q?: string; company_id?: string }) => ["contacts", "list", q] as const,
  contact: (id: string) => ["contacts", "detail", id] as const,
  contactTickets: (id: string) => ["contacts", "tickets", id] as const,
  companyLists: ["companies", "list"] as const,
  companies: (q: { q?: string }) => ["companies", "list", q] as const,
  company: (id: string) => ["companies", "detail", id] as const,
  tags: ["tags"] as const,
  tagGroups: ["tag-groups"] as const,
  /** Every entity's custom fields, archived included; filter by `entity` when reading. */
  customFields: ["custom-fields"] as const,
  savedReplies: ["saved-replies"] as const,
  templateVariables: ["templates", "variables"] as const,
  emailTemplates: (brandId: string) => ["email-templates", brandId] as const,
  ticketVariables: (id: string, mailboxId: string | null) => ["tickets", "variables", id, mailboxId] as const,
  views: ["views"] as const,
  slaPolicies: ["sla-policies"] as const,
  automations: ["automations"] as const,
  mailboxes: ["mailboxes"] as const,
  members: ["members"] as const,
  invitations: ["invitations"] as const,
  teams: ["teams"] as const,
  brands: ["brands"] as const,
  widgets: ["widgets"] as const,
  bots: ["bots"] as const,
  routingRules: ["routing-rules"] as const,
  apiKeys: ["api-keys"] as const,
  webhooks: ["webhooks"] as const,
  webhookDeliveries: (id: string) => ["webhooks", "deliveries", id] as const,
  integrations: ["integrations"] as const,
  integrationCatalog: ["integrations", "catalog"] as const,
  supportChannelProviders: ["support-channels", "providers"] as const,
  supportChannelConnections: ["support-channels", "connections"] as const,
  supportChannelDestinations: ["support-channels", "destinations"] as const,
  imports: ["imports"] as const,
  ticketLinks: (id: string) => ["tickets", "links", id] as const,
  auditLogs: ["audit-logs"] as const,
  notifications: ["notifications"] as const,
  /** The caller's notification preferences in the current workspace (the cache is cleared on a workspace switch). */
  notificationPreferences: ["me", "notification-preferences"] as const,
  kbCollections: ["kb", "collections"] as const,
  kbArticleLists: ["kb", "articles"] as const,
  kbArticles: (q: { collection_id?: string; status?: string; q?: string; for_brand_id?: string }) => ["kb", "articles", q] as const,
  kbArticle: (id: string) => ["kb", "article", id] as const,
  search: (q: string) => ["search", q] as const,
  reports: (r: ReportsQuery) => ["reports", r] as const,
  widgetSettings: ["widget-settings"] as const,
  csat: (token: string) => ["public", "csat", token] as const,
  invitationLookup: (token: string) => ["invitation-lookup", token] as const,
  billing: ["billing"] as const,
  billingUsage: (r: { from: string; to: string }) => ["billing", "usage", r] as const,
  billingInvoices: ["billing", "invoices"] as const,
  aiConfig: ["ai", "config"] as const,
  aiSearch: (q: string) => ["ai", "search", q] as const,
  aiModels: ["ai", "models"] as const,
  aiProvider: ["ai", "provider"] as const,
  agentReport: (r: ReportsRange) => ["ai", "agent", "report", r] as const,
  agentSetup: ["ai", "agent", "setup"] as const,
  aiClientServer: ["ai-clients", "server"] as const,
  aiClientConnections: ["ai-clients", "connections"] as const,
  oauthRequest: (id: string) => ["oauth", "request", id] as const,
  mcpServers: ["ai", "mcp-servers"] as const,
  knowledge: ["ai", "knowledge"] as const,
  knowledgeDocs: (id: string) => ["ai", "knowledge", "docs", id] as const,
  agentTests: ["ai", "agent", "tests"] as const,
  agentTestRuns: ["ai", "agent", "test-runs"] as const,
  agentTestRun: (id: string) => ["ai", "agent", "test-runs", id] as const,
  agentReview: ["ai", "agent", "review"] as const,
  assignment: ["assignment"] as const,
  triage: ["triage"] as const,
  businessHours: ["business-hours"] as const,
  onboarding: ["onboarding"] as const,
  // Collab (contracts/collab.md): everything under "collab", so a workspace switch or a refetch reaches it all.
  collab: ["collab"] as const,
  collabStatus: ["collab", "status"] as const,
  mailAccounts: ["collab", "accounts"] as const,
  /** With the shared accounts of people who left, for admins (settings). */
  mailAccountsManage: ["collab", "accounts", "manage"] as const,
  mailFolders: (accountId: string) => ["collab", "folders", accountId] as const,
  mailCounts: ["collab", "counts"] as const,
  mailSidebar: ["collab", "sidebar"] as const,
  mailThreadLists: ["collab", "threads"] as const,
  mailThreads: (q: CollabThreadQuery) => ["collab", "threads", q] as const,
  mailThread: (id: string) => ["collab", "thread", id] as const,
};

const list = <R>(key: readonly unknown[], fn: () => Promise<R[] | Page<R> | { data: R[] }>, opts: { staleTime?: number } = {}) =>
  queryOptions({ queryKey: key, queryFn: async () => rows(await fn()), ...opts });

export const q = {
  authConfig: () => queryOptions({ queryKey: qk.authConfig, queryFn: api.auth.config, staleTime: 5 * 60_000 }),
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
  members: () => list(qk.members, () => api.members.list()),
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
