import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { Command } from "cmdk";
import { Dialog as D } from "radix-ui";
import {
  ArrowRight,
  BarChart3,
  BookOpen,
  Building2,
  CircleDashed,
  FileText,
  Inbox,
  Keyboard,
  Layers,
  Mail,
  Moon,
  Search,
  Server,
  Settings,
  SquarePen,
  Sun,
  User,
  UserPlus,
  Users,
} from "lucide-react";
import { useDeferredValue, useEffect, useState, type ReactNode } from "react";
import { Avatar } from "@/components/ui/avatar";
import { EmptyState } from "@/components/ui/empty";
import { Kbd } from "@/components/ui/kbd";
import { Spinner } from "@/components/ui/spinner";
import { StatusIcon } from "@/components/tickets/bits";
import { folders } from "@/lib/folders";
import { relShort } from "@/lib/format";
import { q } from "@/lib/queries";
import { useSession } from "@/lib/session";
import { useTheme } from "@/lib/theme";
import { desktop } from "@/lib/desktop";
import { shell, usePageCommands, useShell } from "./shellStore";
import { useAccountActions, useSwitcherEntries } from "./useAccounts";
import { useTranslation } from "react-i18next";
import { AiIcon } from "@/components/ai/bits";
import { WorkingBar } from "@/components/ai/ui/Thinking";
import { cn } from "@/lib/cn";
import { AI_TONE, tone } from "@/lib/tones";
import { useDebounced } from "@/components/contacts/hooks";
import { useAI } from "@/lib/ai";
import { sortTeams, TeamDot, useTeamsCtx } from "@/components/tickets/teams";
import type { SearchResults } from "@/lib/api/types";
import { useCollab, useMailAccounts } from "@/lib/collab/hooks";
import { compose } from "@/lib/collab/compose";
import { AccountDot } from "@/components/collab/bits";
import { WorkspaceBadge } from "@/components/workspaces/WorkspaceBadge";
import { useWorkspaces } from "@/lib/workspaces";

const itemCls =
  "flex h-9 cursor-default select-none items-center gap-2.5 rounded-md px-2.5 text-[13px] text-fg transition-colors duration-100 animate-item-in motion-reduce:animate-none data-[selected=true]:bg-hover [&>svg]:size-4 [&>svg]:shrink-0 [&>svg]:text-fg-3";
const groupCls =
  "[&_[cmdk-group-heading]]:px-2.5 [&_[cmdk-group-heading]]:pb-1.5 [&_[cmdk-group-heading]]:pt-3 [&_[cmdk-group-heading]]:text-[11.5px] [&_[cmdk-group-heading]]:font-medium [&_[cmdk-group-heading]]:text-fg-3";

function Item({ children, onSelect, icon, hint, value, keywords, forceMount }: { children: ReactNode; onSelect: () => void; icon?: ReactNode; hint?: ReactNode; value: string; keywords?: string[]; forceMount?: boolean }) {
  return (
    <Command.Item value={value} keywords={keywords} onSelect={onSelect} forceMount={forceMount} className={itemCls}>
      {icon}
      <span className="flex min-w-0 flex-1 items-center gap-2 truncate">{children}</span>
      {hint && <span className="shrink-0 text-xs text-fg-4">{hint}</span>}
    </Command.Item>
  );
}

export function CommandPalette() {
  const { t: tNav } = useTranslation("navigation");
  const open = useShell((s) => s.paletteOpen);
  const [query, setQuery] = useState("");
  const deferred = useDeferredValue(query.trim());
  const navigate = useNavigate();
  const { data: views } = useQuery(q.views());
  const teams = useTeamsCtx();
  const visibleTeams = sortTeams(teams.teams.filter(teams.canSeeTeam), teams.myTeams);
  const search = useQuery({ ...q.search(deferred), enabled: open && deferred.length > 1 });
  const pageCommands = usePageCommands((s) => s.commands);
  const workspaceId = useSession((s) => s.workspaceId);
  const meId = useSession((s) => s.user?.id);
  const entries = useSwitcherEntries();
  const { switchTo, addAccount } = useAccountActions();
  const { dark, setTheme } = useTheme();
  const ai = useAI();
  const { t } = useTranslation("ai");
  const { t: tKb } = useTranslation("kb");
  const { t: tShell } = useTranslation("shell");
  const [aiMode, setAiMode] = useState(false);
  const collab = useCollab();
  const { data: mailAccounts } = useMailAccounts();
  const { t: tCollab } = useTranslation("collab");
  const { t: tw } = useTranslation("workspaces");
  const several = useWorkspaces().several;
  const aiQuery = useDebounced(query.trim(), 300);
  const aiSearch = useQuery({ ...q.aiSearch(aiQuery), enabled: open && aiMode && ai.enabled && aiQuery.length > 1 });

  useEffect(() => {
    if (!open) {
      setQuery("");
      setAiMode(false);
    }
  }, [open]);

  const go = (fn: () => void) => {
    shell.palette(false);
    fn();
  };

  const results = search.data;
  const hasResults = !!results && (results.tickets.length + results.contacts.length + results.articles.length > 0);

  return (
    <D.Root open={open} onOpenChange={(o) => shell.palette(o)}>
      <D.Portal>
        <D.Overlay className="fixed inset-0 z-50 bg-scrim/70 data-[state=open]:animate-fade-in data-[state=closed]:animate-fade-out" />
        <D.Content
          aria-describedby={undefined}
          className="fixed left-1/2 top-[14vh] z-50 w-[calc(100vw-32px)] max-w-[620px] -translate-x-1/2 overflow-hidden rounded-xl border border-line bg-raised shadow-float origin-top outline-none data-[state=open]:animate-palette-in data-[state=closed]:animate-palette-out motion-reduce:animate-none"
        >
          <D.Title className="sr-only">{tNav("command.menu")}</D.Title>
          <Command loop shouldFilter={!aiMode} className="flex flex-col">
            <div className="relative flex h-12 items-center gap-2.5 border-b border-line px-4">
              {aiMode ? (
                <button
                  type="button"
                  onClick={() => setAiMode(false)}
                  aria-label={t("palette.exit")}
                  className={cn("inline-flex h-6 shrink-0 items-center gap-1.5 rounded-sm px-1.5 text-[12px] font-medium transition-opacity hover:opacity-80 animate-pop-in", tone[AI_TONE].chip)}
                >
                  <AiIcon className="size-3 text-current" />
                  {t("palette.mode")}
                </button>
              ) : (
                <Search className="size-4 shrink-0 text-fg-3" />
              )}
              <Command.Input
                autoFocus
                value={query}
                onValueChange={setQuery}
                onKeyDown={(e) => {
                  if (e.key === "Tab" && !e.shiftKey && ai.enabled) {
                    e.preventDefault();
                    setAiMode((m) => !m);
                  } else if (e.key === "Backspace" && aiMode && !query) {
                    e.preventDefault();
                    setAiMode(false);
                  }
                }}
                placeholder={aiMode ? t("palette.placeholder") : tNav("command.search")}
                className="h-full flex-1 bg-transparent text-[14px] text-fg outline-none placeholder:text-fg-4 focus-visible:shadow-none"
              />
              {!aiMode && search.isFetching && <Spinner className="size-3.5 text-fg-4" />}
              {aiMode && aiSearch.isFetching && <WorkingBar className="top-auto -bottom-px" />}
              <Kbd>esc</Kbd>
            </div>
            <Command.List className="max-h-[min(420px,60vh)] overflow-y-auto p-1.5">
              {aiMode ? (
                <AiResults
                  query={aiQuery}
                  pending={query.trim() !== aiQuery || aiSearch.isFetching}
                  data={aiSearch.data}
                  error={!!aiSearch.error}
                  onExample={setQuery}
                  onTicket={(id) => go(() => void navigate({ to: "/inbox/$folder/{-$ticketId}", params: { folder: "open", ticketId: id } }))}
                  onArticle={(id) => go(() => void navigate({ to: "/kb/articles/$articleId", params: { articleId: id }, search: { collection: undefined } }))}
                />
              ) : (
              <>
              {ai.enabled && query.trim().length > 2 && (
                <Command.Group forceMount className={groupCls}>
                  <Item forceMount value={`ask ai ${query}`} icon={<AiIcon className="text-fg-3" />} hint={<Kbd>tab</Kbd>} onSelect={() => setAiMode(true)}>
                    <span className="truncate">{t("palette.ask_item", { query: query.trim() })}</span>
                  </Item>
                </Command.Group>
              )}
              {!hasResults && !(ai.enabled && query.trim().length > 2) && (
                <Command.Empty>
                  {deferred.length > 1 && search.isFetching ? (
                    <p className="py-10 text-center text-[13px] text-fg-3">{tNav("command.searching")}</p>
                  ) : (
                    <EmptyState compact lit={2} title={tNav("command.noResults")} description={tNav("command.noResultsHelp")} />
                  )}
                </Command.Empty>
              )}

              {hasResults && results.tickets.length > 0 && (
                <Command.Group heading={tNav("command.tickets")} forceMount className={groupCls}>
                  {results.tickets.map((t) => (
                    <Item
                      forceMount
                      key={t.id}
                      value={`ticket ${t.number} ${t.subject} ${t.contact?.name ?? ""} ${t.id}`}
                      icon={<StatusIcon status={t.status} />}
                      hint={<span className="mono">{relShort(t.last_message_at)}</span>}
                      onSelect={() =>
                        go(() => void navigate({ to: "/inbox/$folder/{-$ticketId}", params: { folder: "open", ticketId: t.id } }))
                      }
                    >
                      <span className="mono shrink-0 text-xs text-fg-3">#{t.number}</span>
                      <span className="truncate" data-ph-mask>
                        {t.subject}
                      </span>
                      {t.contact && <span className="shrink-0 truncate text-xs text-fg-3">{t.contact.name}</span>}
                    </Item>
                  ))}
                </Command.Group>
              )}
              {hasResults && results.contacts.length > 0 && (
                <Command.Group heading={tNav("titles.contacts")} forceMount className={groupCls}>
                  {results.contacts.map((c) => (
                    <Item
                      forceMount
                      key={c.id}
                      value={`contact ${c.name} ${c.email ?? ""} ${c.id}`}
                      icon={<Avatar name={c.name || c.email || "?"} src={c.avatar_url} size="sm" />}
                      hint={c.company?.name}
                      onSelect={() => go(() => void navigate({ to: "/contacts/{-$contactId}", params: { contactId: c.id } }))}
                    >
                      <span className="truncate" data-ph-mask>
                        {c.name}
                      </span>
                      <span className="truncate text-xs text-fg-3" data-ph-mask>
                        {c.email}
                      </span>
                    </Item>
                  ))}
                </Command.Group>
              )}
              {hasResults && results.articles.length > 0 && (
                <Command.Group heading={tNav("command.articles")} forceMount className={groupCls}>
                  {results.articles.map((a) => (
                    <Item
                      forceMount
                      key={a.id}
                      value={`article ${a.title} ${a.id}`}
                      icon={<FileText />}
                      hint={a.status === "draft" ? tKb("status.draft") : undefined}
                      onSelect={() => go(() => void navigate({ to: "/kb/articles/$articleId", params: { articleId: a.id }, search: { collection: undefined } }))}
                    >
                      {a.title}
                    </Item>
                  ))}
                </Command.Group>
              )}

              {pageCommands.length > 0 && (
                <Command.Group heading={tNav("command.thisTicket")} className={groupCls}>
                  {pageCommands.map((c) => (
                    <Item key={c.id} value={`${c.label} ${c.id}`} keywords={c.keywords} icon={c.icon} hint={c.shortcut && <Kbd>{c.shortcut}</Kbd>} onSelect={() => go(c.run)}>
                      {c.label}
                    </Item>
                  ))}
                </Command.Group>
              )}

              <Command.Group heading={tNav("command.goTo")} className={groupCls}>
                {folders.map((f) => (
                  <Item
                    key={f.id}
                    value={`go inbox ${f.id}`}
                    keywords={[tNav(f.labelKey)]}
                    icon={f.id === "unassigned" ? <CircleDashed /> : f.id === "mine" ? <User /> : <Inbox />}
                    onSelect={() => go(() => void navigate({ to: "/inbox/$folder/{-$ticketId}", params: { folder: f.id, ticketId: undefined } }))}
                  >
                    {tNav(f.labelKey)}
                  </Item>
                ))}
                {collab.enabled && (
                  <Item
                    value="go mail inbox email" keywords={[tCollab("palette.goMail")]}
                    icon={<Mail />}
                    onSelect={() => go(() => void navigate({ to: "/mail/$box/{-$threadId}", params: { box: "inbox", threadId: undefined }, search: {} }))}
                  >
                    {tCollab("palette.goMail")}
                  </Item>
                )}
                {collab.enabled &&
                  mailAccounts?.map((a) => (
                    <Item
                      key={`mail-${a.id}`}
                      value={`go mail account ${a.name} ${a.email}`}
                      icon={<span className="flex size-4 shrink-0 items-center justify-center"><AccountDot color={a.color} /></span>}
                      hint={tCollab("palette.account")}
                      onSelect={() => go(() => void navigate({ to: "/mail/$box/{-$threadId}", params: { box: "inbox", threadId: undefined }, search: { account: a.id } }))}
                    >
                      {a.name || a.email}
                    </Item>
                  ))}
                {teams.myTeams.size > 1 && (
                  <Item
                    value="go team my teams" keywords={[tShell("nav.myTeams")]}
                    icon={<Users />}
                    hint={tShell("nav.teamHint")}
                    onSelect={() => go(() => void navigate({ to: "/teams/$teamId/{-$ticketId}", params: { teamId: "mine", ticketId: undefined } }))}
                  >
                    {tShell("nav.myTeams")}
                  </Item>
                )}
                {visibleTeams.map((x) => (
                  <Item
                    key={`team-${x.id}`}
                    value={`go team ${x.name} ${x.id}`}
                    icon={<span className="flex size-4 shrink-0 items-center justify-center"><TeamDot team={x} /></span>}
                    hint={tShell("nav.teamHint")}
                    onSelect={() => go(() => void navigate({ to: "/teams/$teamId/{-$ticketId}", params: { teamId: x.id, ticketId: undefined } }))}
                  >
                    {x.name}
                  </Item>
                ))}
                {views?.map((v) => (
                  <Item
                    key={v.id}
                    value={`go view ${v.name}`}
                    icon={<Layers />}
                    hint={tNav("titles.view")}
                    onSelect={() => go(() => void navigate({ to: "/views/$viewId/{-$ticketId}", params: { viewId: v.id, ticketId: undefined } }))}
                  >
                    {v.name}
                  </Item>
                ))}
                <Item value="go contacts" keywords={[tNav("titles.contacts")]} icon={<Users />} onSelect={() => go(() => void navigate({ to: "/contacts/{-$contactId}", params: { contactId: undefined } }))}>
                  {tNav("titles.contacts")}
                </Item>
                <Item value="go companies" keywords={[tNav("titles.companies")]} icon={<Building2 />} onSelect={() => go(() => void navigate({ to: "/companies/{-$companyId}", params: { companyId: undefined } }))}>
                  {tNav("titles.companies")}
                </Item>
                <Item value="go knowledge base kb articles" keywords={[tNav("titles.knowledgeBase")]} icon={<BookOpen />} onSelect={() => go(() => void navigate({ to: "/kb" }))}>
                  {tNav("titles.knowledgeBase")}
                </Item>
                <Item value="go reports analytics" keywords={[tNav("titles.reports")]} icon={<BarChart3 />} onSelect={() => go(() => void navigate({ to: "/reports" }))}>
                  {tNav("titles.reports")}
                </Item>
                {(
                  [
                    ["/settings/profile", "titles.profile"],
                    ["/settings/security", "titles.security"],
                    ["/settings/notifications", "titles.notifications"],
                    ["/settings/workspace", "command.workspaceSettings"],
                    ["/settings/members", "titles.members"],
                    ["/settings/teams", "titles.teams"],
                    ["/settings/mailboxes", "titles.mailboxes"],
                    ...(collab.enabled ? ([["/settings/mail-accounts", "titles.mailAccounts"]] as const) : []),
                    ["/settings/email-design", "titles.emailDesign"],
                    ["/settings/tags", "titles.labels"],
                    ["/settings/saved-replies", "titles.savedReplies"],
                    ["/settings/views", "titles.views"],
                    ["/settings/sla", "titles.slaPolicies"],
                    ["/settings/automations", "titles.automations"],
                    ["/settings/ai", "command.aiSettings"],
                    ["/settings/ai/agent", "command.aiOverview"],
                    ["/settings/ai/bots", "command.aiBots"],
                    ["/settings/ai/behaviour", "command.aiBehaviour"],
                    ["/settings/ai/knowledge", "command.aiKnowledge"],
                    ["/settings/ai/tools", "command.aiTools"],
                    ["/settings/ai/playground", "command.aiPlayground"],
                    ["/settings/widget", "titles.chatWidget"],
                    ["/settings/api-keys", "titles.apiKeys"],
                    ["/settings/webhooks", "titles.webhooks"],
                    ["/settings/audit-log", "titles.auditLog"],
                    ["/settings/import", "command.importHistory"],
                  ] as const
                ).map(([to, labelKey]) => (
                  <Item key={to} value={`settings ${to}`} keywords={[tNav(labelKey)]} icon={<Settings />} hint={tNav("titles.settings")} onSelect={() => go(() => void navigate({ to }))}>
                    {tNav(labelKey)}
                  </Item>
                ))}
              </Command.Group>

              <Command.Group heading={tNav("command.actions")} className={groupCls}>
                <Item value="new ticket compose create" keywords={[tNav("command.newTicket")]} icon={<SquarePen />} hint={<Kbd>c</Kbd>} onSelect={() => go(() => shell.newTicket())}>
                  {tNav("command.newTicket")}
                </Item>
                {collab.enabled && !!mailAccounts?.length && (
                  <Item value="compose email new mail message write" keywords={[tCollab("palette.compose")]} icon={<Mail />} onSelect={() => go(() => compose.open())}>
                    {tCollab("palette.compose")}
                  </Item>
                )}
                <Item value="toggle theme dark light mode" keywords={[tNav(dark ? "command.switchLight" : "command.switchDark")]} icon={dark ? <Sun /> : <Moon />} onSelect={() => go(() => setTheme(dark ? "light" : "dark"))}>
                  {tNav(dark ? "command.switchLight" : "command.switchDark")}
                </Item>
                <Item value="keyboard shortcuts help" keywords={[tNav("command.shortcuts")]} icon={<Keyboard />} hint={<Kbd>?</Kbd>} onSelect={() => go(() => shell.shortcuts())}>
                  {tNav("command.shortcuts")}
                </Item>
                {desktop && (
                  <Item value="switch server instance cloud self-hosted" keywords={[tShell("desktop.switchServer")]} icon={<Server />} onSelect={() => go(() => desktop?.switchServer())}>
                    {tShell("desktop.switchServer")}
                  </Item>
                )}
                {entries
                  .filter(({ account, membership: m }) => account.user.id !== meId || m.workspace.id !== workspaceId)
                  .map(({ account, membership: m }) => {
                    const other = account.user.id !== meId;
                    return (
                      <Item
                        key={`${account.user.id}:${m.workspace.id}`}
                        value={`switch workspace ${m.workspace.name}${other ? ` account ${account.user.email}` : ""}`}
                        icon={other ? <ArrowRight /> : <WorkspaceBadge name={m.workspace.name} logo={m.workspace.avatar_url} size="sm" decorative />}
                        onSelect={() => go(() => void switchTo(account.user.id, m.workspace.id))}
                      >
                        {other ? tShell("accounts.paletteSwitch", { workspace: m.workspace.name, email: account.user.email }) : tNav("command.switchWorkspace", { workspace: m.workspace.name })}
                      </Item>
                    );
                  })}
                {several && (
                  <>
                    <Item value="everywhere assigned to me all workspaces" icon={<User />} onSelect={() => go(() => void navigate({ to: "/everywhere/$list", params: { list: "mine" } }))}>
                      {tw("everywhere.mineTitle")}
                    </Item>
                    <Item value="everywhere all mail all workspaces" icon={<Mail />} onSelect={() => go(() => void navigate({ to: "/everywhere/$list", params: { list: "mail" } }))}>
                      {tw("everywhere.mailTitle")}
                    </Item>
                    <Item value="arrange workspaces order hide switcher" icon={<Layers />} onSelect={() => go(() => void navigate({ to: "/settings/workspaces" }))}>
                      {tw("customize.arrange")}
                    </Item>
                  </>
                )}
                                <Item value="add account sign in another switch account" keywords={[tShell("accounts.paletteAdd")]} icon={<UserPlus />} onSelect={() => go(addAccount)}>
                  {tShell("accounts.paletteAdd")}
                </Item>
              </Command.Group>
              {ai.enabled && (
                <Command.Group heading={t("palette.ask")} className={groupCls}>
                  <Item value="ask ai semantic search meaning" keywords={[t("palette.ask_hint")]} icon={<AiIcon />} hint={<Kbd>tab</Kbd>} onSelect={() => setAiMode(true)}>
                    {t("palette.ask_hint")}
                  </Item>
                </Command.Group>
              )}
              </>
              )}
            </Command.List>
            <div className="flex h-9 items-center gap-4 border-t border-line px-4 text-[11.5px] text-fg-4">
              <span className="flex items-center gap-1.5">
                <Kbd>↑</Kbd>
                <Kbd>↓</Kbd> {tNav("command.move")}
              </span>
              <span className="flex items-center gap-1.5">
                <Kbd>enter</Kbd> {tNav("command.open")}
              </span>
              {ai.enabled && (
                <span className="ml-auto flex items-center gap-1.5">
                  <Kbd>tab</Kbd> {aiMode ? t("palette.exit") : t("palette.ask")}
                </span>
              )}
            </div>
          </Command>
        </D.Content>
      </D.Portal>
    </D.Root>
  );
}

/** Semantic results (GET /v1/ai/search): tickets and articles related by meaning. */
function AiResults({
  query,
  pending,
  data,
  error,
  onTicket,
  onArticle,
  onExample,
}: {
  query: string;
  onExample: (q: string) => void;
  pending: boolean;
  data: SearchResults | undefined;
  error: boolean;
  onTicket: (id: string) => void;
  onArticle: (id: string) => void;
}) {
  const { t } = useTranslation("ai");
  const { t: tKb } = useTranslation("kb");
  if (query.length < 2)
    return (
      <div className="animate-fade-in">
        <div className="flex items-center gap-2.5 px-2.5 pb-2 pt-2.5">
          <span className={cn("flex size-6 shrink-0 items-center justify-center rounded-sm", tone[AI_TONE].soft)}>
            <AiIcon className="size-3.5" />
          </span>
          <p className="text-[12.5px] text-fg-3">{t("palette.start")}</p>
        </div>
        <Command.Group heading={t("palette.try")} className={groupCls}>
          {(["palette.example_1", "palette.example_2", "palette.example_3"] as const).map((k) => (
            <Item key={k} value={`ai example ${k}`} icon={<ArrowRight />} hint={<Kbd>enter</Kbd>} onSelect={() => onExample(t(k))}>
              <span className="truncate text-fg-2">{t(k)}</span>
            </Item>
          ))}
        </Command.Group>
      </div>
    );
  if (!data && pending)
    return (
      <div className="flex flex-col gap-1 p-1" aria-live="polite" aria-label={t("palette.searching")}>
        <div className="px-1.5 pb-1.5 pt-2 text-[11.5px] font-medium text-fg-3">{t("palette.searching")}</div>
        {[0, 1, 2].map((i) => (
          <div key={i} className="flex h-9 items-center gap-2.5 px-2.5">
            <span className="skeleton size-4 rounded-[3px]" />
            <span className="skeleton h-2.5" style={{ width: `${62 - i * 14}%` }} />
          </div>
        ))}
      </div>
    );
  const tickets = data?.tickets ?? [];
  const articles = data?.articles ?? [];
  if (error || (!tickets.length && !articles.length))
    return <p className="px-6 py-10 text-center text-[13px] text-fg-3">{error ? t("palette.error") : t("palette.empty")}</p>;
  const heading = (label: string) => (
    <span className="flex items-center gap-1.5">
      <AiIcon className="size-3" />
      {label}
    </span>
  );
  return (
    <div className={pending ? "opacity-60 transition-opacity" : "transition-opacity"}>
      {tickets.length > 0 && (
        <Command.Group heading={heading(t("palette.tickets"))} className={groupCls}>
          {tickets.map((x) => (
            <Item
              key={x.id}
              value={`ai ticket ${x.id}`}
              icon={<StatusIcon status={x.status} />}
              hint={<span className="mono">{relShort(x.last_message_at)}</span>}
              onSelect={() => onTicket(x.id)}
            >
              <span className="mono shrink-0 text-xs text-fg-3">#{x.number}</span>
              <span className="truncate" data-ph-mask>
                {x.subject}
              </span>
              {x.contact && <span className="shrink-0 truncate text-xs text-fg-3">{x.contact.name}</span>}
            </Item>
          ))}
        </Command.Group>
      )}
      {articles.length > 0 && (
        <Command.Group heading={heading(t("palette.articles"))} className={groupCls}>
          {articles.map((a) => (
            <Item key={a.id} value={`ai article ${a.id}`} icon={<FileText />} hint={a.status === "draft" ? tKb("status.draft") : undefined} onSelect={() => onArticle(a.id)}>
              <span className="truncate">{a.title}</span>
              {a.excerpt && <span className="hidden min-w-0 truncate text-xs text-fg-3 sm:inline">{a.excerpt}</span>}
            </Item>
          ))}
        </Command.Group>
      )}
    </div>
  );
}
