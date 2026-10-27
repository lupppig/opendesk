// The sidebar: sits on the chrome beside the white work panel. Small
// tracked-uppercase section labels, 28px rows, hairlines between groups, and an
// icon-only rail (`b` or the Collapse row) that keeps every row reachable.

import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { BarChart3, BookOpen, Check, ChevronRight, ChevronsUpDown, CircleCheck, CircleDashed, Clock, EyeOff, Inbox, Keyboard, Layers, Lock, LogOut, Monitor, Moon, NotebookText, PanelLeftClose, PanelLeftOpen, Plus, Server, Settings, ShieldAlert, SlidersHorizontal, Sun, User, UserPlus, Users } from "lucide-react";
import { DropdownMenu as M } from "radix-ui";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { lazy, Suspense, useEffect, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { ChecklistMini, ProgressRing, stepsLeft } from "@/components/onboarding/Checklist";
import { useUrgentChats, useWaitingFor } from "@/components/tickets/useUrgentChats";
import { useNow } from "@/lib/useNow";
import { formatWait, urgentWash } from "@/components/tickets/waiting";
import { AvailabilityMenuItems } from "@/components/presence/availability";
import { Avatar } from "@/components/ui/avatar";
import { toast } from "@/components/ui/toast";
import { Kbd } from "@/components/ui/kbd";
import { Menu, MenuContent, MenuItem, MenuLabel, MenuSeparator, MenuSub, MenuTrigger } from "@/components/ui/menu";
import { Picker } from "@/components/ui/picker";
import { sortTeams, TeamDot, useTeamsCtx } from "@/components/tickets/teams";
import { useHotkeys } from "@/lib/hotkeys";
import { dur, ease } from "@/lib/motion";
import type { Team, TicketCounts } from "@/lib/api/types";
import { Tooltip } from "@/components/ui/tooltip";
import { cn } from "@/lib/cn";
import { folders, type FolderId } from "@/lib/folders";
import { useAI } from "@/lib/ai";
import { useDismissOnboarding, useSetAvailability } from "@/lib/pictures";
import { q } from "@/lib/queries";
import { atLeast, loadAccounts, useRole, useSession, useWorkspace } from "@/lib/session";
import { useTheme, type ThemePref } from "@/lib/theme";
import { desktop } from "@/lib/desktop";
import { shell, toggleNav, useNav } from "./shellStore";
import { quickEntries, quickSwitchKeys, useAccountActions, useQuickSwitch, useSwitcherEntries } from "./useAccounts";
import { Count, Label, NavTip, PillCtx, rowActive, rowBase, rowIdle, rowShape, Section, WaitDot, WaitingBadge, withPill } from "./sidebarParts";
import { useSpaceKeys } from "@/components/spaces/actions";
import { SpaceGlyph } from "@/components/spaces/icons";
import { SwitcherSpaces, useSwitcherStarters } from "@/components/spaces/SwitcherSpaces";
import { SectionList, SpaceView } from "@/components/spaces/SpaceView";
import { useSpaces } from "@/lib/spaces/hooks";
import { pickedSpace } from "@/lib/spaces/model";
import { spaceUi, usePickedSpaceId } from "@/lib/spaces/store";

export { WorkspaceGlyph } from "./sidebarParts";
import type { SidebarPrefs, SidebarSectionId } from "@/lib/api/types";
import { RowMenu } from "@/components/collab/RowMenu";
import { useCollab, useSidebarPrefs } from "@/lib/collab/hooks";
import { ticketRowNumbers } from "@/lib/collab/badges";
import { badgeFor, hid, pickCount, visibleSections, type BadgeNumbers } from "@/lib/collab/sidebarPrefs";
import { collabUi } from "@/lib/collab/ui";
import { WorkspaceBadge } from "@/components/workspaces/WorkspaceBadge";

// Collab's sections load with Collab: a workspace without it never downloads them.
const LazyMail = lazy(() => import("@/components/collab/MailNav").then((m) => ({ default: m.MailSection })));
const LazyFavorites = lazy(() => import("@/components/collab/MailNav").then((m) => ({ default: m.FavoritesSection })));

const folderIcons: Record<FolderId, ReactNode> = {
  unassigned: <CircleDashed />,
  mine: <User />,
  open: <Inbox />,
  pending: <Clock />,
  solved: <CircleCheck />,
  spam: <ShieldAlert />,
};

/**
 * Visitors waiting in live chat with nobody on it: a red row on top of the inbox, on
 * every page, that opens the longest wait. Gone when nobody waits.
 */
function WaitingChats({ rail }: { rail: boolean }) {
  const { t } = useTranslation("shell");
  const navigate = useNavigate();
  const { urgent } = useUrgentChats();
  // The duration ticks here alone, not in everything that reads the waiting chats.
  const now = useNow(urgent.length > 0);
  if (!urgent.length) return null;
  const longest = urgent[0];
  const time = formatWait(now - longest.wait.since);
  const label = t("nav.waiting", { count: urgent.length, time });
  return (
    <NavTip rail={rail} label={label}>
      <button
        type="button"
        onClick={() => void navigate({ to: "/inbox/$folder/{-$ticketId}", params: { folder: "unassigned", ticketId: longest.ticket.id } })}
        aria-label={label}
        className={cn(
          rowBase,
          rowShape(rail),
          urgentWash,
          "mb-1 overflow-hidden font-medium text-tone-red shadow-[inset_0_0_0_1px_var(--tone-red-line)] hover:brightness-[0.98] active:scale-[0.99]",
        )}
      >
        {rail ? (
          <span className="mono text-[11px] font-semibold tabular-nums">{urgent.length > 9 ? "9+" : urgent.length}</span>
        ) : (
          <>
            <span className="flex size-[15px] shrink-0 items-center justify-center">
              <WaitDot size="size-2" />
            </span>
            <span className="min-w-0 flex-1 truncate">{t("nav.waitingShort", { count: urgent.length })}</span>
            <span className="mono ml-auto shrink-0 text-[11px] font-semibold tabular-nums">{time}</span>
          </>
        )}
      </button>
    </NavTip>
  );
}

const OTHER_TEAMS_KEY = "od-nav-other-teams";

/** The member's sidebar prefs, for a row's own menu and its number by their choice (with or without Collab). */
type RowPrefs = { prefs: SidebarPrefs };

/** One team inbox row: its colour dot, a lock when private, and what waits unassigned. */
function TeamRow({
  teamId,
  label,
  icon,
  locked,
  nums,
  title,
  rail,
  muted,
  rp,
  waiting = 0,
}: {
  teamId: string;
  label: string;
  icon: ReactNode;
  locked?: boolean;
  /** What the row can show (ticketRowNumbers). */
  nums: BadgeNumbers;
  title?: string;
  rail: boolean;
  muted?: boolean;
  rp: RowPrefs;
  /** Visitors waiting in chat on this team's tickets with nobody on them. */
  waiting?: number;
}) {
  const { t } = useTranslation("shell");
  const key = hid.team(teamId);
  const n = pickCount(badgeFor(rp.prefs, key, "teams", "team"), nums);
  const own = teamId !== "mine";
  return (
    <NavTip rail={rail} label={label}>
      <RowMenu
        enabled
        prefs={rp.prefs}
        badge={{ key, kind: "team", sections: ["teams"], numbers: nums }}
        pin={own ? { kind: "team", id: teamId } : undefined}
        hideId={own ? key : undefined}
        rail={rail}
        label={label}
      >
      <Link
        to="/teams/$teamId/{-$ticketId}"
        params={{ teamId, ticketId: undefined }}
        className={cn(rowBase, rowShape(rail), rowIdle, muted && "text-fg-3")}
        activeProps={{ className: rowActive, "data-status": "active" } as Record<string, string>}
        aria-label={rail ? label : undefined}
        title={rail ? undefined : title}
      >{withPill(<>
        <span className="flex size-[15px] shrink-0 items-center justify-center">{icon}</span>
        <Label rail={rail}>
          <span className="flex min-w-0 items-center gap-1">
            <span className="truncate">{label}</span>
            {locked && <Lock aria-label={t("nav.privateTeam")} className="size-[11px] shrink-0 text-fg-4" />}
          </span>
        </Label>
        <WaitingBadge n={waiting} rail={rail} />
        {n !== undefined && n > 0 && <Count n={n} rail={rail} />}
        {rail && n !== undefined && n > 0 && <span className="absolute right-1 top-1 size-1.5 rounded-full bg-fg ring-2 ring-chrome" />}
      </>)}</Link>
      </RowMenu>
    </NavTip>
  );
}

/**
 * The caller's teams (once the workspace has one), "My teams" across them, and the
 * other public teams under a fold. g then t opens a team switcher.
 */
function TeamsSection({ rail, drawer, first, counts, hidden = NONE, rp }: { rail: boolean; drawer?: boolean; first?: boolean; counts: TicketCounts | undefined; hidden?: ReadonlySet<string>; rp: RowPrefs }) {
  const { t } = useTranslation("shell");
  const ctx = useTeamsCtx();
  const role = useRole();
  const navigate = useNavigate();
  const [switcher, setSwitcher] = useState(false);
  const [othersOpen, setOthersOpen] = useState(() => {
    try {
      return localStorage.getItem(OTHER_TEAMS_KEY) === "1";
    } catch {
      return false;
    }
  });
  useHotkeys({ "g t": () => setSwitcher(true) }, !drawer && ctx.hasTeams);
  const waitingFor = useWaitingFor();
  if (!ctx.hasTeams) return null;
  const byName = (a: Team, b: Team) => a.name.localeCompare(b.name);
  const shown = (x: Team) => !hidden.has(hid.team(x.id));
  const mine = ctx.teams.filter((x) => ctx.myTeams.has(x.id) && shown(x)).sort(byName);
  const others = ctx.teams.filter((x) => !ctx.myTeams.has(x.id) && ctx.canSeeTeam(x) && shown(x)).sort(byName);
  const countsOf = (id: string) => (id === "mine" ? counts?.my_teams : counts?.teams?.[id]);
  const titleOf = (id: string) => {
    const c = countsOf(id);
    return c ? t("nav.teamCounts", { unassigned: c.unassigned, open: c.open }) : undefined;
  };
  const toggleOthers = () => {
    setOthersOpen(!othersOpen);
    try {
      localStorage.setItem(OTHER_TEAMS_KEY, othersOpen ? "0" : "1");
    } catch {
      /* storage unavailable */
    }
  };
  const go = (teamId: string) => void navigate({ to: "/teams/$teamId/{-$ticketId}", params: { teamId, ticketId: undefined } });
  const row = (x: Team, muted?: boolean) => (
    <TeamRow
      key={x.id}
      teamId={x.id}
      label={x.name}
      icon={<TeamDot team={x} />}
      locked={x.private}
      nums={ticketRowNumbers(counts, hid.team(x.id))}
      title={titleOf(x.id)}
      rail={rail}
      muted={muted}
      rp={rp}
      waiting={waitingFor({ kind: "team", id: x.id })}
    />
  );
  return (
    <Section
      id="teams"
      label={t("nav.teams")}
      rail={rail}
      first={first}
      action={
        atLeast(role, "admin") ? (
          <Tooltip content={t("nav.manageTeams")}>
            <Link
              to="/settings/teams"
              aria-label={t("nav.manageTeams")}
              className="flex size-5 items-center justify-center rounded text-fg-4 opacity-0 transition-opacity hover:bg-chrome-hover hover:text-fg group-hover/section:opacity-100 focus-visible:opacity-100"
            >
              <Plus className="size-3.5" />
            </Link>
          </Tooltip>
        ) : undefined
      }
    >
      {!drawer && (
        <Picker
          open={switcher}
          onOpenChange={setSwitcher}
          side="right"
          width={260}
          placeholder={t("nav.switchTeam")}
          value={null}
          items={[
            ...(mine.length > 1 ? [{ value: "mine", label: t("nav.myTeams"), icon: <Users />, group: t("nav.yourTeams") }] : []),
            ...sortTeams([...mine, ...others], ctx.myTeams).map((x) => ({
              value: x.id,
              label: x.name,
              icon: <TeamDot team={x} />,
              hint: countsOf(x.id)?.unassigned ? String(countsOf(x.id)!.unassigned) : undefined,
              group: ctx.myTeams.has(x.id) ? t("nav.yourTeams") : t("nav.otherTeams"),
            })),
          ]}
          onSelect={go}
          trigger={<span aria-hidden className="pointer-events-none mx-2.5 block h-0" />}
        />
      )}
      {mine.length > 1 && (
        <TeamRow teamId="mine" label={t("nav.myTeams")} icon={<Users className="size-[15px] text-fg-3" />} nums={ticketRowNumbers(counts, hid.team("mine"))} title={titleOf("mine")} rail={rail} rp={rp} waiting={waitingFor({ kind: "team", id: "mine" })} />
      )}
      {mine.map((x) => row(x))}
      {others.length > 0 && !rail && (
        <button
          type="button"
          aria-expanded={othersOpen}
          onClick={toggleOthers}
          className={cn(rowBase, rowShape(rail), rowIdle, "text-fg-3")}
        >
          <ChevronRight className={cn("transition-transform duration-150 motion-reduce:transition-none", othersOpen && "rotate-90")} />
          <Label rail={rail}>{t("nav.otherTeams")}</Label>
          {/* Folded away: their waiting chats still show here. */}
          {!othersOpen && <WaitingBadge n={others.reduce((sum, x) => sum + waitingFor({ kind: "team", id: x.id }), 0)} rail={rail} />}
          <span className="mono ml-auto text-[11px] text-fg-4">{others.length}</span>
        </button>
      )}
      {(othersOpen || (rail && !mine.length)) && others.map((x) => row(x, true))}
    </Section>
  );
}

const NONE: ReadonlySet<string> = new Set();

/** One built-in section of the open workspace: the sidebar's own rows, live counts and menus. */
function BuiltinSection({ id, first, rail, drawer }: { id: SidebarSectionId; first: boolean; rail: boolean; drawer?: boolean }) {
  const { t: tNav } = useTranslation("navigation");
  const { data: counts } = useQuery(q.counts());
  const { data: views } = useQuery(q.views());
  const role = useRole();
  const { t } = useTranslation("shell");
  // The member's hidden rows, numbers, favorites and menus apply with or without Collab;
  // Collab adds the Mail section (contracts/collab.md SidebarPrefs).
  const collab = useCollab();
  const prefs = useSidebarPrefs();
  const hidden: ReadonlySet<string> = new Set(prefs.hidden);
  const rp: RowPrefs = { prefs };
  const shown = (key: string, section: SidebarSectionId, nums: BadgeNumbers) => pickCount(badgeFor(prefs, key, section, "ticket"), nums);
  const waitingFor = useWaitingFor();
  switch (id) {
    case "inbox":
      return (
        <Section id="inbox" label={t("nav.inbox")} rail={rail} first={first}>
          <WaitingChats rail={rail} />
          {folders.filter((f) => !hidden.has(hid.ticketFolder(f.id))).map((f) => {
            const key = hid.ticketFolder(f.id);
            const counted = f.id !== "solved" && f.id !== "spam";
            const nums = ticketRowNumbers(counts, key);
            const n = counted ? shown(key, "inbox", nums) : undefined;
            return (
            <NavTip key={f.id} rail={rail} label={tNav(f.labelKey)}>
              <RowMenu
                enabled
                prefs={prefs}
                badge={counted ? { key, kind: "ticket", sections: ["inbox"], numbers: nums } : undefined}
                pin={{ kind: "ticket_folder", id: f.id }}
                hideId={key}
                rail={rail}
                label={tNav(f.labelKey)}
              >
              <Link
                to="/inbox/$folder/{-$ticketId}"
                params={{ folder: f.id, ticketId: undefined }}
                className={cn(rowBase, rowShape(rail), rowIdle)}
                activeProps={{ className: rowActive, "data-status": "active" } as Record<string, string>}
                aria-label={rail ? tNav(f.labelKey) : undefined}
              >{withPill(<>
                {folderIcons[f.id]}
                <Label rail={rail}>{tNav(f.labelKey)}</Label>
                <WaitingBadge n={waitingFor({ kind: "ticket_folder", id: f.id })} rail={rail} />
                <Count n={n} rail={rail} />
                {rail && f.id === "unassigned" && (n ?? 0) > 0 && (
                  <span className="absolute right-1 top-1 size-1.5 rounded-full bg-fg ring-2 ring-chrome" />
                )}
              </>)}</Link>
              </RowMenu>
            </NavTip>
            );
          })}
        </Section>
      );
    case "teams":
      return <TeamsSection rail={rail} drawer={drawer} first={first} counts={counts} hidden={hidden} rp={rp} />;
    case "views":
      return (
        <Section
          id="views"
          label={t("nav.views")}
          rail={rail}
          first={first}
          action={
            atLeast(role, "agent") ? (
              <Tooltip content={t("nav.manageViews")}>
                <Link
                  to="/settings/views"
                  aria-label={t("nav.manageViews")}
                  className="flex size-5 items-center justify-center rounded text-fg-4 opacity-0 transition-opacity hover:bg-chrome-hover hover:text-fg group-hover/section:opacity-100 focus-visible:opacity-100"
                >
                  <Plus className="size-3.5" />
                </Link>
              </Tooltip>
            ) : undefined
          }
        >
          {views?.length ? (
            views
              .filter((v) => !hidden.has(hid.view(v.id)))
              .sort((a, b) => a.position - b.position)
              .map((v) => {
                const nums = ticketRowNumbers(counts, hid.view(v.id));
                return (
                <NavTip key={v.id} rail={rail} label={v.name}>
                  <RowMenu
                    enabled
                    prefs={prefs}
                    badge={{ key: hid.view(v.id), kind: "ticket", sections: ["views"], numbers: nums }}
                    pin={{ kind: "view", id: v.id }}
                    hideId={hid.view(v.id)}
                    rail={rail}
                    label={v.name}
                  >
                  <Link
                    to="/views/$viewId/{-$ticketId}"
                    params={{ viewId: v.id, ticketId: undefined }}
                    className={cn(rowBase, rowShape(rail), rowIdle)}
                    activeProps={{ className: rowActive, "data-status": "active" } as Record<string, string>}
                    aria-label={rail ? v.name : undefined}
                  >{withPill(<>
                    <Layers />
                    <Label rail={rail}>{v.name}</Label>
                    <WaitingBadge n={waitingFor({ kind: "view", id: v.id })} rail={rail} />
                    <Count n={shown(hid.view(v.id), "views", nums)} rail={rail} />
                  </>)}</Link>
                  </RowMenu>
                </NavTip>
                );
              })
          ) : (
            <NavTip rail={rail} label={t("nav.createView")}>
              <Link to="/settings/views" className={cn(rowBase, rowShape(rail), rowIdle, "text-fg-3")}>
                <Plus />
                <Label rail={rail}>{t("nav.createView")}</Label>
              </Link>
            </NavTip>
          )}
        </Section>
      );
    case "workspace":
      return (
        <Section id="workspace" label={t("nav.workspace")} rail={rail} first={first}>
          <NavTip rail={rail} label={t("nav.contacts")}>
            <Link
              to="/contacts/{-$contactId}"
              params={{ contactId: undefined }}
              className={cn(rowBase, rowShape(rail), rowIdle)}
              activeProps={{ className: rowActive }}
              aria-label={rail ? t("nav.contacts") : undefined}
            >{withPill(<>
              <Users />
              <Label rail={rail}>{t("nav.contacts")}</Label>
            </>)}</Link>
          </NavTip>
          <NavTip rail={rail} label={t("nav.kb")}>
            <Link to="/kb" className={cn(rowBase, rowShape(rail), rowIdle)} activeProps={{ className: rowActive }} aria-label={rail ? t("nav.kb") : undefined}>{withPill(<>
              <BookOpen />
              <Label rail={rail}>{t("nav.kb")}</Label>
            </>)}</Link>
          </NavTip>
          <NavTip rail={rail} label={t("nav.wiki")}>
            <Link to="/wiki" className={cn(rowBase, rowShape(rail), rowIdle)} activeProps={{ className: rowActive }} aria-label={rail ? t("nav.wiki") : undefined}>{withPill(<>
              <NotebookText />
              <Label rail={rail}>{t("nav.wiki")}</Label>
            </>)}</Link>
          </NavTip>
          <NavTip rail={rail} label={t("nav.reports")}>
            <Link to="/reports" className={cn(rowBase, rowShape(rail), rowIdle)} activeProps={{ className: rowActive }} aria-label={rail ? t("nav.reports") : undefined}>{withPill(<>
              <BarChart3 />
              <Label rail={rail}>{t("nav.reports")}</Label>
            </>)}</Link>
          </NavTip>
        </Section>
      );
    case "favorites":
      // Without Collab, favorites are the ticket rows: inbox folders, teams and views.
      return (
        <Suspense fallback={null}>
          <LazyFavorites prefs={prefs} rail={rail} first={first} />
        </Suspense>
      );
    case "mail":
      return collab.enabled ? (
        <Suspense fallback={null}>
          <LazyMail prefs={prefs} rail={rail} first={first} />
        </Suspense>
      ) : null;
  }
}

export function Sidebar({ drawer }: { drawer?: boolean }) {
  const collapsed = useNav((s) => s.collapsed);
  const rail = collapsed && !drawer;
  const { t } = useTranslation("shell");
  // The member's own order and hidden sections, with or without Collab; Mail only with it.
  const collab = useCollab();
  const prefs = useSidebarPrefs();
  const order = visibleSections(prefs, collab.enabled);
  // Spaces (contracts/spaces.md): a picked Space is the sidebar; otherwise the workspace's own.
  const uid = useSession((s) => s.user?.id ?? "");
  const wid = useSession((s) => s.workspaceId);
  const { spaces, fresh, isError } = useSpaces();
  const pickedId = usePickedSpaceId();
  const space = pickedSpace(spaces, pickedId);
  // A remembered Space that is gone (deleted elsewhere) quietly gives way to the workspace.
  useEffect(() => {
    if (fresh && pickedId && !space && uid) spaceUi.pick(uid, null);
  }, [fresh, pickedId, space, uid]);
  // Until the Spaces load, a remembered Space shows nothing rather than the workspace for a moment.
  const waiting = !!pickedId && !spaces && !isError;
  const render = (id: SidebarSectionId, first: boolean) => <BuiltinSection id={id} first={first} rail={rail} drawer={drawer} />;

  return (
    <PillCtx.Provider value={drawer ? "nav-pill-drawer" : "nav-pill"}>
    <nav aria-label={t("nav.main")} className="flex h-full w-full flex-col">
      <div className="flex-1 overflow-y-auto overflow-x-hidden pb-3 pt-1">
        {waiting ? null : space && spaces ? (
          <SpaceView key={space.id} spaces={spaces} space={space} rail={rail} renderSection={render} />
        ) : (
          <SectionList ids={order} render={render} />
        )}
      </div>
      <div className="flex shrink-0 flex-col gap-px py-1.5">
        {/* Per workspace: one finished elsewhere never celebrates on switching to it. */}
        <SetupChecklist key={wid} rail={rail} />
        <NavTip rail={rail} label={t("nav.settings")}>
          <Link to="/settings" className={cn(rowBase, rowShape(rail), rowIdle)} activeProps={{ className: rowActive }} aria-label={rail ? t("nav.settings") : undefined}>{withPill(<>
            <Settings />
            <Label rail={rail}>{t("nav.settings")}</Label>
          </>)}</Link>
        </NavTip>
        {!drawer && (
          <Tooltip content={rail ? t("nav.expand") : t("nav.collapse")} shortcut="b" side="right">
            <button
              type="button"
              aria-label={rail ? t("nav.expand") : t("nav.collapse")}
              onClick={() => toggleNav()}
              className={cn(rowBase, rowShape(rail), rowIdle, "hidden md:flex")}
            >
              {rail ? <PanelLeftOpen /> : <PanelLeftClose />}
              <Label rail={rail}>{t("nav.collapse")}</Label>
            </button>
          </Tooltip>
        )}
      </div>

      <div className="shrink-0 border-t border-chrome-line">
        <UserMenu rail={rail} />
      </div>
    </nav>
    </PillCtx.Provider>
  );
}

/**
 * The workspace switcher. With more than one account signed in here, workspaces
 * are grouped under each account's email; any of them opens in one step, and
 * Mod+Alt+1..9 opens them in this order.
 */
export function WorkspaceSwitcher() {
  const ws = useWorkspace();
  const user = useSession((s) => s.user);
  const entries = useSwitcherEntries();
  const accounts = useSession((s) => s.accounts);
  const navigate = useNavigate();
  const { switchTo, addAccount } = useAccountActions();
  const { t } = useTranslation("shell");
  const { t: tw } = useTranslation("workspaces");
  const { spaces } = useSpaces();
  const pickedId = usePickedSpaceId();
  const space = pickedSpace(spaces, pickedId);
  const starters = useSwitcherStarters();
  useQuickSwitch();
  useSpaceKeys(spaces);
  if (!ws || !user) return null;
  // A remembered Space waits for the list before the trigger names anything.
  const waiting = !!pickedId && !spaces;
  const grouped = accounts.length > 1;
  // Accounts in switcher order, each with its workspaces and their quick switch numbers.
  // Quick switch numbers skip the workspaces the person hid; those fold away under their account.
  const numbered = new Map(quickEntries(entries).map((e, i) => [`${e.account.user.id}:${e.membership.workspace.id}`, i + 1]));
  const withN = entries.map((e) => ({ ...e, n: numbered.get(`${e.account.user.id}:${e.membership.workspace.id}`) ?? Infinity }));
  const groups = (grouped ? accounts : accounts.filter((a) => a.user.id === user.id)).map((a) => ({
    account: a,
    items: withN.filter((e) => e.account.user.id === a.user.id),
  }));
  if (!groups.length) groups.push({ account: { user, memberships: [] }, items: withN });
  return (
    <Menu
      onOpenChange={(open) => {
        starters.onOpen(open);
        if (open) void loadAccounts();
      }}
    >
      <MenuTrigger asChild>
        <button
          type="button"
          className="group flex h-8 min-w-0 max-w-[220px] items-center gap-2 rounded-md pl-1 pr-1.5 text-left outline-none transition-colors hover:bg-chrome-hover focus-visible:shadow-[0_0_0_2px_var(--focus)] data-[state=open]:bg-chrome-hover"
        >
          {waiting ? (
            <span aria-hidden className="skeleton size-6 shrink-0 rounded-md" />
          ) : space ? (
            <SpaceGlyph icon={space.icon} name={space.name} color={space.color} size="lg" className="size-6" />
          ) : (
            <WorkspaceBadge name={ws.name} logo={ws.avatar_url} size="lg" decorative />
          )}
          <span className="min-w-0 flex-1 truncate text-[12.5px] font-medium text-fg">{waiting ? <span className="skeleton block h-2.5 w-20" /> : space ? space.name : ws.name}</span>
          <ChevronsUpDown className="size-3.5 shrink-0 text-fg-4 transition-colors group-hover:text-fg-2" />
        </button>
      </MenuTrigger>
      <MenuContent align="start" className="max-h-[min(560px,var(--radix-dropdown-menu-content-available-height))] w-[264px] overflow-y-auto">
        {spaces && (
          <>
            <SwitcherSpaces spaces={spaces} picked={space?.id ?? null} starters={starters.list} />
            <MenuSeparator />
          </>
        )}
        {!grouped && <MenuLabel>{t("workspaces.title")}</MenuLabel>}
        {groups.map(({ account, items }, gi) => (
          <M.Group key={account.user.id}>
            {grouped && (
              <M.Label className={cn("flex min-w-0 items-center gap-1.5 px-2 pb-1 text-[11.5px] text-fg-3", gi === 0 ? "pt-1.5" : "mt-1 border-t border-line pt-2")}>
                <Avatar name={account.user.name || account.user.email} src={account.user.avatar_url} size="xs" />
                <span className="truncate">{account.user.email}</span>
              </M.Label>
            )}
            {(() => {
              const entry = ({ membership: m, n }: (typeof items)[number]) => {
                const current = !space && account.user.id === user.id && m.workspace.id === ws.id;
                return (
                  <MenuItem
                    key={`${account.user.id}:${m.workspace.id}`}
                    icon={<WorkspaceBadge name={m.workspace.name} logo={m.workspace.avatar_url} size="sm" decorative />}
                    onSelect={() => void switchTo(account.user.id, m.workspace.id)}
                  >
                    <span className="flex items-center gap-2">
                      <span className={cn("min-w-0 flex-1 truncate", current && "font-medium")}>{m.workspace.name}</span>
                      {current && <Check aria-label={t("workspaces.current")} className="!size-3 !text-fg" />}
                      {n <= 9 && (
                        <Kbd className="opacity-0 transition-opacity duration-100 group-data-[highlighted]/item:opacity-100 motion-reduce:transition-none">
                          {quickSwitchKeys(n)}
                        </Kbd>
                      )}
                    </span>
                  </MenuItem>
                );
              };
              const hidden = items.filter((e) => e.hidden);
              return (
                <>
                  {items.filter((e) => !e.hidden).map(entry)}
                  {hidden.length > 0 && (
                    <MenuSub label={<span className="text-fg-3">{tw("switcher.hidden", { count: hidden.length })}</span>} icon={<EyeOff />}>
                      {hidden.map(entry)}
                    </MenuSub>
                  )}
                </>
              );
            })()}
            {!items.length && (
              <MenuItem icon={<Plus />} onSelect={() => void switchTo(account.user.id, null)}>
                <span className="text-fg-3">{t("workspaces.noneYet")}</span>
              </MenuItem>
            )}
          </M.Group>
        ))}
        <MenuSeparator />
        <MenuItem icon={<Plus />} onSelect={() => void navigate({ to: "/onboarding" })}>
          {t("workspaces.create")}
        </MenuItem>
        <MenuItem icon={<Settings />} onSelect={() => void navigate({ to: "/settings/workspace" })}>
          {t("workspaces.settings")}
        </MenuItem>
        <MenuSeparator />
        <MenuItem icon={<UserPlus />} onSelect={addAccount}>
          {t("workspaces.addAccount")}
        </MenuItem>
      </MenuContent>
    </Menu>
  );
}

/** Light, dark, system: a three-way segmented switch, used in the account menu. */
export function ThemeSwitch() {
  const { pref, setTheme } = useTheme();
  const { t } = useTranslation("shell");
  const opts: { v: ThemePref; icon: ReactNode; label: string }[] = [
    { v: "light", icon: <Sun />, label: t("theme.light") },
    { v: "dark", icon: <Moon />, label: t("theme.dark") },
    { v: "system", icon: <Monitor />, label: t("theme.system") },
  ];
  return (
    <div role="radiogroup" aria-label={t("theme.title")} className="inline-flex items-center rounded-md bg-hover p-0.5">
      {opts.map((o) => (
        <Tooltip key={o.v} content={o.label}>
          <button
            type="button"
            role="radio"
            aria-checked={pref === o.v}
            aria-label={o.label}
            onClick={(e) => {
              e.preventDefault();
              setTheme(o.v);
            }}
            className={cn(
              "flex h-[22px] w-7 items-center justify-center rounded-sm text-fg-3 transition-[color,background-color,box-shadow] hover:text-fg [&_svg]:size-3.5",
              pref === o.v && "bg-surface text-fg shadow-[0_0_0_1px_var(--line),0_1px_2px_rgb(0_0_0/0.06)]",
            )}
          >
            {o.icon}
          </button>
        </Tooltip>
      ))}
    </div>
  );
}

/** How long "You're all set" stays after the last step before the entry folds away. */
const ALL_SET_MS = 3500;

/**
 * The onboarding checklist, above Settings while something is left that the viewer can do
 * and until it is dismissed. Finishing the last step shows "You're all set" for a moment,
 * then the entry folds away; once done, it is not shown again.
 */
function SetupChecklist({ rail }: { rail: boolean }) {
  const { t } = useTranslation("setup");
  const { data } = useQuery(q.onboarding());
  const dismiss = useDismissOnboarding();
  const ai = useAI();
  const isAdmin = atLeast(useRole(), "admin");
  const reduce = useReducedMotion();
  const unavailable = ai.enabled ? undefined : { try_ollie: t("checklist.needs_ai"), setup_ai_agent: t("checklist.needs_ai") };
  const left = data && !data.dismissed && data.steps.length ? stepsLeft(data, { isAdmin, unavailable }) : 0;
  // Seen open in this session: finishing it now earns the moment; a checklist done before is simply gone.
  const seenOpen = useRef(false);
  const [allSet, setAllSet] = useState(false);
  useEffect(() => {
    if (left > 0) {
      seenOpen.current = true;
      return;
    }
    if (!seenOpen.current || !data || data.dismissed) return;
    seenOpen.current = false;
    setAllSet(true);
    const id = window.setTimeout(() => setAllSet(false), ALL_SET_MS);
    return () => window.clearTimeout(id);
  }, [left, data]);

  const show = !!data && (left > 0 || allSet);
  return (
    <AnimatePresence initial={false}>
      {show && (
        <motion.div
          key="setup"
          initial={{ opacity: 0, height: 0 }}
          animate={{ opacity: 1, height: "auto" }}
          exit={{ opacity: 0, height: 0 }}
          transition={reduce ? { duration: 0 } : { duration: dur.slow, ease: ease.out }}
          className={cn("overflow-hidden", rail ? "mx-3" : "mx-2.5")}
        >
          {left > 0 ? (
            <ChecklistMini
              collapsed={rail}
              data={data}
              isAdmin={isAdmin}
              unavailable={unavailable}
              onDismiss={() => dismiss.mutateAsync()}
              triggerClassName={cn("h-7 hover:bg-chrome-hover data-[state=open]:bg-chrome-hover", rail && "h-8")}
            />
          ) : (
            <div role="status" aria-label={t("checklist.done_title")} className={cn("flex items-center gap-2 text-[12.5px] font-medium text-fg-2", rail ? "h-8 w-8 justify-center" : "h-7 px-2")}>
              <ProgressRing value={1} />
              {!rail && <span className="truncate">{t("checklist.done_title")}</span>}
            </div>
          )}
        </motion.div>
      )}
    </AnimatePresence>
  );
}

function UserMenu({ rail }: { rail: boolean }) {
  const user = useSession((s) => s.user);
  const accountCount = useSession((s) => s.accounts.length);
  const navigate = useNavigate();
  const { logOut } = useAccountActions();
  const { t } = useTranslation("shell");
  const { data: members } = useQuery(q.members());
  const setAvailability = useSetAvailability();
  const { t: tc } = useTranslation("collab");
  if (!user) return null;
  const name = user.name || user.email;
  const me = members?.find((m) => m.user.id === user.id);
  return (
    <Menu>
      <MenuTrigger asChild>
        <button
          type="button"
          aria-label={t("account.menu")}
          className={cn(
            "my-2 flex items-center rounded-md text-left outline-none transition-[width,background-color] duration-200 hover:bg-chrome-hover focus-visible:shadow-[0_0_0_2px_var(--focus)] data-[state=open]:bg-chrome-hover",
            rail ? "mx-3 w-8 justify-center py-0.5" : "mx-2.5 w-[calc(100%-1.25rem)] gap-2.5 px-1.5 py-1",
          )}
        >
          <Avatar name={name} src={user.avatar_url} size="lg" presence={me?.presence} />
          {!rail && (
            <span className="min-w-0 flex-1">
              <span className="block truncate text-[12.5px] font-medium text-fg">{name}</span>
              <span className="block truncate text-[11px] text-fg-3">{user.email}</span>
            </span>
          )}
        </button>
      </MenuTrigger>
      <MenuContent align="start" side="top" className="w-[244px]">
        <div className="px-2 pb-2 pt-1.5">
          <div className="truncate text-[12.5px] font-medium">{user.name}</div>
          <div className="mono truncate text-[11px] text-fg-3">{user.email}</div>
        </div>
        <MenuSeparator />
        <div className="flex h-8 items-center justify-between gap-3 pl-2 pr-0.5">
          <span className="text-[12.5px] text-fg">{t("theme.title")}</span>
          <ThemeSwitch />
        </div>
        <MenuSeparator />
        {me && (
          <>
            <AvailabilityMenuItems
              value={me.availability}
              onChange={(v) => setAvailability.mutate({ userId: "me", availability: v }, { onError: (e) => toast.error(e) })}
              openTickets={me.role === "viewer" ? undefined : me.open_tickets}
              maxOpenTickets={me.max_open_tickets}
            />
            <MenuSeparator />
          </>
        )}
        <MenuItem icon={<User />} onSelect={() => void navigate({ to: "/settings/profile" })}>
          {t("account.profile")}
        </MenuItem>
        <MenuItem icon={<Settings />} onSelect={() => void navigate({ to: "/settings" })}>
          {t("nav.settings")}
        </MenuItem>
        <MenuItem icon={<SlidersHorizontal />} onSelect={() => collabUi.customize(true)}>
          {tc("nav.customize")}
        </MenuItem>
        <MenuItem icon={<Keyboard />} onSelect={() => shell.shortcuts()}>
          <span className="flex items-center justify-between">
            {t("account.shortcuts")} <Kbd>?</Kbd>
          </span>
        </MenuItem>
        {desktop && (
          <MenuItem icon={<Server />} onSelect={() => desktop?.switchServer()}>
            {t("desktop.switchServer")}
          </MenuItem>
        )}
        <MenuSeparator />
        <MenuItem danger icon={<LogOut />} onSelect={() => void logOut()}>
          {t("account.logout")}
        </MenuItem>
        {accountCount > 1 && (
          <MenuItem danger icon={<LogOut />} onSelect={() => void logOut(true)}>
            {t("account.logoutAll")}
          </MenuItem>
        )}
      </MenuContent>
    </Menu>
  );
}
