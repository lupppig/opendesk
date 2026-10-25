// The sidebar with Together (contracts/api.md "Workspaces together"): every workspace
// the person shows, grouped (a foldable group per workspace) or mixed (sections
// combined, every row marked with its workspace), and the Everywhere lists on top.
// The focus workspace's group is the full sidebar of today; the others' rows come
// from their own data, fetched in their own workspace, and opening one moves the focus
// there without a reload.

import { useQuery } from "@tanstack/react-query";
import { Link, useRouterState } from "@tanstack/react-router";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { ChevronRight, CircleDashed, Clock, Inbox, Layers, Mail, User, Users } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { inWorkspace } from "@/lib/api/client";
import type { MailAccount, Membership, Team, View, WorkspaceOverview } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { AccountDot } from "@/components/collab/bits";
import { TeamDot } from "@/components/tickets/teams";
import { WorkspaceBadge } from "@/components/workspaces/WorkspaceBadge";
import { folders, type FolderId } from "@/lib/folders";
import { q } from "@/lib/queries";
import { useSession } from "@/lib/session";
import { teamsOf } from "@/lib/ticketCache";
import { itemId, toggleIn, useOpenInWorkspace, useTogether, useUpdateWorkspacePrefs } from "@/lib/together";
import { Count, Label, NavTip, rowActive, rowBase, rowIdle, rowShape, Section, withPill } from "./sidebarParts";

const folderIcons: Partial<Record<FolderId, ReactNode>> = {
  unassigned: <CircleDashed />,
  mine: <User />,
  open: <Inbox />,
  pending: <Clock />,
};
/** The folders another workspace's group shows: the ones with a number. */
const COMPACT: FolderId[] = ["unassigned", "mine", "open", "pending"];

const sum = (xs: (number | undefined | null)[]) => xs.reduce<number>((n, x) => n + (x ?? 0), 0);

/** What waits in a workspace: unassigned tickets and unread mail in its inboxes. */
export function waitingIn(o: WorkspaceOverview | undefined): number {
  if (!o || o.suspended) return 0;
  return (o.ticket_counts?.unassigned ?? 0) + (o.mail_counts?.boxes.inbox ?? 0);
}

/** A workspace's teams, views and mail accounts, each read in that workspace. */
function useWorkspaceRows(wid: string, mail: boolean, enabled: boolean) {
  const teams = useQuery({ ...inWorkspace(wid, () => q.teams()), enabled });
  const views = useQuery({ ...inWorkspace(wid, () => q.views()), enabled });
  const accounts = useQuery({ ...inWorkspace(wid, () => q.mailAccounts()), enabled: enabled && mail });
  return { teams: teams.data ?? [], views: views.data ?? [], accounts: mail ? (accounts.data ?? []) : [] };
}

/** The teams a person sees as rows: theirs, then the other public ones. */
function visibleTeams(teams: Team[], me: string): Team[] {
  const mine = new Set(teamsOf(teams, me));
  const byName = (a: Team, b: Team) => a.name.localeCompare(b.name);
  return [...teams.filter((x) => mine.has(x.id)).sort(byName), ...teams.filter((x) => !mine.has(x.id) && !x.private).sort(byName)];
}

// Rows take their destination as data; the Link of the focus workspace's rows spreads it.
const AnyLink = Link as unknown as (p: Record<string, unknown>) => ReactNode;

/** One row that opens a page of another workspace (or, for the focus, a Link). */
function Row({
  wid,
  focus,
  rail,
  icon,
  label,
  count,
  to,
  badge,
}: {
  wid: string;
  focus: boolean;
  rail: boolean;
  icon: ReactNode;
  label: string;
  count?: number;
  to: Parameters<ReturnType<typeof useOpenInWorkspace>>[1];
  /** The workspace mark, in mixed rows. */
  badge?: Membership["workspace"];
}) {
  const openIn = useOpenInWorkspace();
  const { t } = useTranslation("together");
  const content = (
    <>
      {/* Mixed rows lead with their workspace's badge, so rows of two workspaces never read alike. */}
      {badge ? (
        <span className="flex size-[15px] shrink-0 items-center justify-center">
          <WorkspaceBadge name={badge.name} logo={badge.avatar_url} size="sm" decorative />
        </span>
      ) : (
        icon
      )}
      <Label rail={rail}>{label}</Label>
      <Count n={count || undefined} rail={rail} />
    </>
  );
  const name = badge ? `${label}, ${t("badge.in", { name: badge.name })}` : label;
  if (focus) {
    return (
      <NavTip rail={rail} label={name}>
        <AnyLink
          {...(to as object)}
          className={cn(rowBase, rowShape(rail), rowIdle)}
          activeProps={{ className: rowActive, "data-status": "active" } as Record<string, string>}
          aria-label={badge || rail ? name : undefined}
        >
          {withPill(content)}
        </AnyLink>
      </NavTip>
    );
  }
  return (
    <NavTip rail={rail} label={name}>
      <button type="button" onClick={() => openIn(wid, to)} className={cn(rowBase, rowShape(rail), rowIdle)} aria-label={badge || rail ? name : undefined}>
        {content}
      </button>
    </NavTip>
  );
}

/** A group's header: the workspace's badge and name, what waits there, and the fold. */
function GroupHeader({
  ws,
  current,
  waiting,
  collapsed,
  onToggle,
  rail,
}: {
  ws: Membership;
  current: boolean;
  waiting: number;
  collapsed: boolean;
  onToggle: () => void;
  rail: boolean;
}) {
  const { t } = useTranslation("together");
  const openIn = useOpenInWorkspace();
  const name = ws.workspace.name;
  if (rail) {
    return (
      <NavTip rail label={name}>
        <button
          type="button"
          onClick={() => openIn(ws.workspace.id, { to: "/inbox/$folder/{-$ticketId}", params: { folder: "unassigned", ticketId: undefined } })}
          aria-label={t("group.open", { name })}
          className={cn("relative mx-3 flex size-8 items-center justify-center rounded-md hover:bg-chrome-hover", current && "bg-chrome-active")}
        >
          <WorkspaceBadge name={name} logo={ws.workspace.avatar_url} size="md" decorative />
          {waiting > 0 && <span className="absolute right-1 top-1 size-1.5 rounded-full bg-fg ring-2 ring-chrome" />}
        </button>
      </NavTip>
    );
  }
  return (
    <div className="group/ws mx-2.5 flex h-7 items-center gap-2 rounded-md px-2">
      <button
        type="button"
        aria-expanded={!collapsed}
        aria-label={collapsed ? t("group.expand", { name }) : t("group.collapse", { name })}
        onClick={onToggle}
        className="flex min-w-0 flex-1 items-center gap-2 text-left outline-none focus-visible:shadow-[0_0_0_2px_var(--focus)]"
      >
        <WorkspaceBadge name={name} logo={ws.workspace.avatar_url} size="md" decorative />
        <span className={cn("min-w-0 flex-1 truncate text-[12.5px]", current ? "font-semibold text-fg" : "font-medium text-fg-2")}>{name}</span>
        {current && <span className="sr-only">{t("group.current")}</span>}
        {ws.suspended && <span className="shrink-0 text-[10.5px] text-fg-4">{t("group.suspended")}</span>}
        {collapsed && waiting > 0 && (
          <span className="mono shrink-0 text-[11px] text-fg-3" aria-label={t("group.waiting", { count: waiting })}>
            {waiting > 999 ? "999+" : waiting}
          </span>
        )}
        <ChevronRight
          aria-hidden
          className={cn(
            "size-3.5 shrink-0 text-fg-4 transition-transform duration-150 motion-reduce:transition-none",
            !collapsed && "rotate-90",
            !collapsed && "opacity-0 group-hover/ws:opacity-100",
          )}
        />
      </button>
    </div>
  );
}

/** The rows of a workspace that is not the focus: inbox folders, teams, views, mail. */
function CompactRows({ ws, overview, rail, hidden }: { ws: Membership; overview: WorkspaceOverview | undefined; rail: boolean; hidden: Set<string> }) {
  const { t } = useTranslation("together");
  const { t: tNav } = useTranslation("navigation");
  const me = useSession((s) => s.user?.id ?? "");
  const wid = ws.workspace.id;
  const counts = overview?.ticket_counts ?? undefined;
  const mail = overview?.mail_counts ?? null;
  const rows = useWorkspaceRows(wid, !!mail, !ws.suspended);
  if (ws.suspended) return null;
  const shown = (kind: string, id: string) => !hidden.has(itemId(wid, kind, id));
  const teams = visibleTeams(rows.teams, me).filter((x) => shown("team", x.id));
  const views = [...rows.views].sort((a, b) => a.position - b.position).filter((v) => shown("view", v.id));
  return (
    <div className="flex flex-col gap-px pb-1">
      {COMPACT.filter((f) => shown("ticket_folder", f)).map((id) => {
        const f = folders.find((x) => x.id === id)!;
        return (
          <Row
            key={id}
            wid={wid}
            focus={false}
            rail={rail}
            icon={folderIcons[id]}
            label={tNav(f.labelKey)}
            count={counts ? (f.count(counts) ?? undefined) : undefined}
            to={{ to: "/inbox/$folder/{-$ticketId}", params: { folder: id, ticketId: undefined } }}
          />
        );
      })}
      {teams.map((x) => (
        <Row
          key={x.id}
          wid={wid}
          focus={false}
          rail={rail}
          icon={<span className="flex size-[15px] shrink-0 items-center justify-center"><TeamDot team={x} /></span>}
          label={x.name}
          count={counts?.teams?.[x.id]?.unassigned}
          to={{ to: "/teams/$teamId/{-$ticketId}", params: { teamId: x.id, ticketId: undefined } }}
        />
      ))}
      {views.map((v) => (
        <Row key={v.id} wid={wid} focus={false} rail={rail} icon={<Layers />} label={v.name} count={counts?.views?.[v.id]} to={{ to: "/views/$viewId/{-$ticketId}", params: { viewId: v.id, ticketId: undefined } }} />
      ))}
      {mail &&
        rows.accounts
          .filter((a) => shown("account", a.id))
          .map((a) => (
            <Row
              key={a.id}
              wid={wid}
              focus={false}
              rail={rail}
              icon={<span className="flex size-[15px] shrink-0 items-center justify-center"><AccountDot color={a.color} className="size-2.5" /></span>}
              label={a.name || a.email}
              count={mail.accounts[a.id]}
              to={{ to: "/mail/$box/{-$threadId}", params: { box: "inbox", threadId: undefined }, search: { account: a.id } }}
            />
          ))}
      {!teams.length && !views.length && !rows.accounts.length && !COMPACT.some((f) => shown("ticket_folder", f)) && (
        <span className="mx-2.5 px-2 py-1 text-[11.5px] text-fg-4">{t("customize.none")}</span>
      )}
    </div>
  );
}

/** Assigned to me and All mail, across every shown workspace. */
function EverywhereRows({ overview, rail, hasMail }: { overview: WorkspaceOverview[]; rail: boolean; hasMail: boolean }) {
  const { t } = useTranslation("together");
  const mine = sum(overview.map((o) => (o.suspended ? 0 : o.ticket_counts?.mine)));
  const mail = sum(overview.map((o) => (o.suspended ? 0 : o.mail_counts?.boxes.inbox)));
  const row = (list: "mine" | "mail", icon: ReactNode, label: string, n: number) => (
    <NavTip rail={rail} label={label}>
      <Link
        to="/everywhere/$list"
        params={{ list }}
        className={cn(rowBase, rowShape(rail), rowIdle)}
        activeProps={{ className: rowActive, "data-status": "active" } as Record<string, string>}
        aria-label={rail ? label : undefined}
      >
        {withPill(
          <>
            {icon}
            <Label rail={rail}>{label}</Label>
            <Count n={n || undefined} rail={rail} />
          </>,
        )}
      </Link>
    </NavTip>
  );
  return (
    <>
      {row("mine", <User />, t("everywhere.mine"), mine)}
      {hasMail && row("mail", <Mail />, t("everywhere.mail"), mail)}
    </>
  );
}

/** A foldable block, folding with the shared motion and never animating for reduced motion. */
function Fold({ open, children }: { open: boolean; children: ReactNode }) {
  const reduce = useReducedMotion();
  return (
    <AnimatePresence initial={false}>
      {open && (
        <motion.div
          initial={reduce ? false : { height: 0, opacity: 0 }}
          animate={{ height: "auto", opacity: 1 }}
          exit={reduce ? undefined : { height: 0, opacity: 0 }}
          transition={{ duration: 0.16, ease: [0.2, 0, 0, 1] }}
          className="overflow-hidden"
        >
          {children}
        </motion.div>
      )}
    </AnimatePresence>
  );
}

export function TogetherNav({ rail, focusContent }: { rail: boolean; focusContent: ReactNode }) {
  const { t } = useTranslation("together");
  const { prefs, shown, focus } = useTogether();
  const update = useUpdateWorkspacePrefs();
  const { data: overview = [] } = useQuery(q.overview());
  const byId = new Map(overview.map((o) => [o.id, o]));
  const visible = new Set(shown.map((m) => m.workspace.id));
  const hidden = new Set(prefs.items.hidden);
  const collapsed = new Set(prefs.collapsed);
  const toggle = (id: string) => update.mutate({ collapsed: toggleIn(prefs.collapsed, id) });
  const listed = overview.filter((o) => visible.has(o.id));
  const hasMail = listed.some((o) => o.mail_counts);

  const everywhere = prefs.everywhere && (
    <Section id="together-everywhere" label={t("everywhere.title")} rail={rail} first>
      <EverywhereRows overview={listed} rail={rail} hasMail={hasMail} />
    </Section>
  );

  if (prefs.layout === "mixed") return <MixedNav rail={rail} everywhere={everywhere} focusContent={focusContent} overview={byId} hidden={hidden} />;

  return (
    <>
      {everywhere}
      {shown.map((m, i) => {
        const wid = m.workspace.id;
        const current = wid === focus;
        const open = !collapsed.has(wid) || rail;
        return (
          <div key={wid} className={cn(i > 0 || everywhere ? "mt-4" : "mt-1", !rail && "border-t border-chrome-line pt-3 first:border-t-0 first:pt-0")} data-workspace={wid}>
            <GroupHeader ws={m} current={current} waiting={waitingIn(byId.get(wid))} collapsed={!open} onToggle={() => toggle(wid)} rail={rail} />
            <Fold open={open && (!rail || current)}>
              {current ? <div className="[&>*:first-child]:!mt-1">{focusContent}</div> : <CompactRows ws={m} overview={byId.get(wid)} rail={rail} hidden={hidden} />}
            </Fold>
          </div>
        );
      })}
    </>
  );
}

/** Mixed: one Inbox, Teams, Views and Mail across workspaces, then the focus workspace's own pages. */
function MixedNav({
  rail,
  everywhere,
  focusContent,
  overview,
  hidden,
}: {
  rail: boolean;
  everywhere: ReactNode;
  focusContent: ReactNode;
  overview: Map<string, WorkspaceOverview>;
  hidden: Set<string>;
}) {
  const { t } = useTranslation("together");
  const { t: tNav } = useTranslation("navigation");
  const { shown, focus } = useTogether();
  const live = shown.filter((m) => !m.suspended);
  const focusWs = shown.find((m) => m.workspace.id === focus);
  return (
    <>
      {everywhere}
      <Section id="together-inbox" label={t("group.inbox")} rail={rail} first={!everywhere}>
        {COMPACT.flatMap((id) =>
          live
            .filter((m) => !hidden.has(itemId(m.workspace.id, "ticket_folder", id)))
            .map((m) => {
              const f = folders.find((x) => x.id === id)!;
              const counts = overview.get(m.workspace.id)?.ticket_counts;
              return (
                <Row
                  key={`${m.workspace.id}:${id}`}
                  wid={m.workspace.id}
                  focus={m.workspace.id === focus}
                  rail={rail}
                  icon={folderIcons[id]}
                  label={tNav(f.labelKey)}
                  count={counts ? (f.count(counts) ?? undefined) : undefined}
                  badge={m.workspace}
                  to={{ to: "/inbox/$folder/{-$ticketId}", params: { folder: id, ticketId: undefined } }}
                />
              );
            }),
        )}
      </Section>
      <MixedLists rail={rail} live={live} focus={focus} overview={overview} hidden={hidden} />
      {focusWs && (
        <div className="mt-5">
          {!rail && (
            <div className="mx-2.5 mb-1 flex h-6 items-center gap-2 px-2">
              <WorkspaceBadge name={focusWs.workspace.name} logo={focusWs.workspace.avatar_url} size="sm" decorative />
              <span className="label-caps truncate">{focusWs.workspace.name}</span>
            </div>
          )}
          {focusContent}
        </div>
      )}
    </>
  );
}

/** Teams, views and mail accounts of every workspace, each row with its workspace's badge. */
function MixedLists({ rail, live, focus, overview, hidden }: { rail: boolean; live: Membership[]; focus: string | null; overview: Map<string, WorkspaceOverview>; hidden: Set<string> }) {
  const { t } = useTranslation("together");
  const per = live.map((m) => ({ m, o: overview.get(m.workspace.id) }));
  return (
    <>
      <Section id="together-teams" label={t("group.teams")} rail={rail}>
        {per.map(({ m, o }) => (
          <MixedRowsOf key={m.workspace.id} kind="teams" ws={m} focus={focus} rail={rail} overview={o} hidden={hidden} />
        ))}
      </Section>
      <Section id="together-views" label={t("group.views")} rail={rail}>
        {per.map(({ m, o }) => (
          <MixedRowsOf key={m.workspace.id} kind="views" ws={m} focus={focus} rail={rail} overview={o} hidden={hidden} />
        ))}
      </Section>
      {per.some(({ o }) => o?.mail_counts) && (
        <Section id="together-mail" label={t("group.mail")} rail={rail}>
          {per.map(({ m, o }) => (
            <MixedRowsOf key={m.workspace.id} kind="mail" ws={m} focus={focus} rail={rail} overview={o} hidden={hidden} />
          ))}
        </Section>
      )}
    </>
  );
}

function MixedRowsOf({
  kind,
  ws,
  focus,
  rail,
  overview,
  hidden,
}: {
  kind: "teams" | "views" | "mail";
  ws: Membership;
  focus: string | null;
  rail: boolean;
  overview: WorkspaceOverview | undefined;
  hidden: Set<string>;
}) {
  const me = useSession((s) => s.user?.id ?? "");
  const wid = ws.workspace.id;
  const mail = overview?.mail_counts ?? null;
  const rows = useWorkspaceRows(wid, !!mail, true);
  const counts = overview?.ticket_counts ?? undefined;
  const isFocus = wid === focus;
  const shown = (k: string, id: string) => !hidden.has(itemId(wid, k, id));
  if (kind === "teams")
    return (
      <>
        {visibleTeams(rows.teams, me)
          .filter((x) => shown("team", x.id))
          .map((x: Team) => (
            <Row
              key={x.id}
              wid={wid}
              focus={isFocus}
              rail={rail}
              badge={ws.workspace}
              icon={<Users />}
              label={x.name}
              count={counts?.teams?.[x.id]?.unassigned}
              to={{ to: "/teams/$teamId/{-$ticketId}", params: { teamId: x.id, ticketId: undefined } }}
            />
          ))}
      </>
    );
  if (kind === "views")
    return (
      <>
        {[...rows.views]
          .sort((a, b) => a.position - b.position)
          .filter((v) => shown("view", v.id))
          .map((v: View) => (
            <Row
              key={v.id}
              wid={wid}
              focus={isFocus}
              rail={rail}
              badge={ws.workspace}
              icon={<Layers />}
              label={v.name}
              count={counts?.views?.[v.id]}
              to={{ to: "/views/$viewId/{-$ticketId}", params: { viewId: v.id, ticketId: undefined } }}
            />
          ))}
      </>
    );
  if (!mail) return null;
  return (
    <>
      {rows.accounts
        .filter((a) => shown("account", a.id))
        .map((a: MailAccount) => (
          <Row
            key={a.id}
            wid={wid}
            focus={isFocus}
            rail={rail}
            badge={ws.workspace}
            icon={<Mail />}
            label={a.name || a.email}
            count={mail.accounts[a.id]}
            to={{ to: "/mail/$box/{-$threadId}", params: { box: "inbox", threadId: undefined }, search: { account: a.id } }}
          />
        ))}
    </>
  );
}

/** Whether the current page is one of the Everywhere lists (for the header). */
export function useOnEverywhere(): boolean {
  return useRouterState({ select: (s) => s.location.pathname.startsWith("/everywhere") });
}
