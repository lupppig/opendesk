// Teams, brands and addresses on tickets (contracts/teams.md): the chips, the
// reply address rules, the "routed" line and the move-to-team preview. Team UI
// shows once the workspace has a team; brand UI once it has a second brand.

import { useQuery } from "@tanstack/react-query";
import { Command } from "cmdk";
import { Popover as P } from "radix-ui";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { ArrowLeft, Check, Clock, CornerDownLeft, EyeOff, Lock, Mail, Timer, UserRound, Users } from "lucide-react";
import { useMemo, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import type { AssignMode, Brand, Mailbox, SlaPolicy, Team, Ticket, Widget } from "@/lib/api/types";
import { q } from "@/lib/queries";
import { teamsOf } from "@/lib/ticketCache";
import { atLeast, useRole, useSession } from "@/lib/session";
import { cn } from "@/lib/cn";
import { dur, ease } from "@/lib/motion";
import { isTone, tone } from "@/lib/tones";
import { floatingPanel } from "@/components/ui/popover";
import { pickerSearch } from "@/components/ui/picker";
import { Button } from "@/components/ui/button";
import { fillFromName as fillFormat, HELPDESK_FROM_VARS } from "@/lib/fromname";
import { useAssistantName } from "./ollie";

// ─── Context ───────────────────────────────────────────────────────────────

export type TeamsCtx = {
  me: string;
  teams: Team[];
  brands: Brand[];
  mailboxes: Mailbox[];
  widgets: Widget[];
  /** The teams the signed-in person is on, any role. */
  myTeams: Set<string>;
  /** Team pickers, chips and folders: once the workspace has a team. */
  hasTeams: boolean;
  /** Brand pickers and chips: once the workspace has a second brand. */
  multiBrand: boolean;
  /** More than one address (mailboxes and their aliases) customers can write to. */
  multiAddress: boolean;
  team: (id: string | null | undefined) => Team | undefined;
  brand: (id: string | null | undefined) => Brand | undefined;
  mailbox: (id: string | null | undefined) => Mailbox | undefined;
  widget: (id: string | null | undefined) => Widget | undefined;
  /** A private team's addresses are only for its members. */
  canSendFrom: (m: Mailbox) => boolean;
  /** Private teams hide their tickets from everyone else. */
  canSeeTeam: (t: Team) => boolean;
};

const none: never[] = [];

export function useTeamsCtx(): TeamsCtx {
  const me = useSession((s) => s.user?.id ?? "");
  const admin = atLeast(useRole(), "admin");
  const teams = useQuery(q.teams()).data ?? none;
  const brands = useQuery({ ...q.brands(), retry: false }).data ?? none;
  const mailboxes = useQuery(q.mailboxes()).data ?? none;
  // Widgets are an admin resource; agents see "a chat widget" instead of its name.
  const widgets = useQuery({ ...q.widgets(), enabled: admin, retry: false }).data ?? none;
  return useMemo(() => {
    const myTeams = new Set(teamsOf(teams, me));
    const byId = <R extends { id: string }>(rows: R[]) => {
      const m = new Map(rows.map((r) => [r.id, r]));
      return (id: string | null | undefined) => (id ? m.get(id) : undefined);
    };
    const addresses = mailboxes.reduce((n, m) => n + 1 + (m.aliases?.length ?? 0), 0);
    return {
      me,
      teams,
      brands,
      mailboxes,
      widgets,
      myTeams,
      hasTeams: teams.length > 0,
      multiBrand: brands.length > 1,
      multiAddress: addresses > 1,
      team: byId(teams),
      brand: byId(brands),
      mailbox: byId(mailboxes),
      widget: byId(widgets),
      canSendFrom: (m) => !m.private || (!!m.team_id && myTeams.has(m.team_id)),
      canSeeTeam: (t) => !t.private || myTeams.has(t.id),
    };
  }, [me, teams, brands, mailboxes, widgets]);
}

export const teamTone = (t: Pick<Team, "color"> | undefined) => (t && isTone(t.color) ? t.color : "gray");

/** Teams the caller is on first, then the rest, each by name. */
export function sortTeams(teams: Team[], mine: Set<string>): Team[] {
  return [...teams].sort((a, b) => Number(mine.has(b.id)) - Number(mine.has(a.id)) || a.name.localeCompare(b.name));
}

// ─── Chips ─────────────────────────────────────────────────────────────────

export function TeamDot({ team, className }: { team: Pick<Team, "color"> | undefined; className?: string }) {
  return <span aria-hidden className={cn("size-2 shrink-0 rounded-full", tone[teamTone(team)].solid, className)} />;
}

/** A team: its colour dot and name, with a lock when private. */
export function TeamChip({ team, className, plain }: { team: Team; className?: string; plain?: boolean }) {
  const { t } = useTranslation("inbox");
  return (
    <span
      title={team.private ? t("teams.privateTeam", { team: team.name }) : team.name}
      className={cn(
        "inline-flex h-[19px] min-w-0 max-w-[140px] shrink-0 items-center gap-1 text-[10.5px] font-medium text-fg-2",
        !plain && "rounded-sm bg-hover px-1.5",
        className,
      )}
    >
      <TeamDot team={team} className="size-1.5" />
      <span className="truncate">{team.name}</span>
      {team.private && <Lock aria-label={t("teams.private")} className="size-2.5 shrink-0 text-fg-3" />}
    </span>
  );
}

/** A brand: a hairline pill with its colour. Only shown with two or more brands. */
export function BrandChip({ brand, className, plain }: { brand: Brand; className?: string; plain?: boolean }) {
  return (
    <span
      title={brand.name}
      className={cn(
        "inline-flex h-[19px] min-w-0 max-w-[120px] shrink-0 items-center gap-1 text-[10.5px] font-medium text-fg-3",
        !plain && "rounded-sm px-1.5 shadow-[inset_0_0_0_1px_var(--line)]",
        className,
      )}
    >
      <span aria-hidden className="size-1.5 shrink-0 rounded-[2px] bg-fg-4" style={brand.color ? { backgroundColor: brand.color } : undefined} />
      <span className="truncate">{brand.name}</span>
    </span>
  );
}

/** The team and brand chips of a ticket, each only when the workspace uses them. */
export function TicketTeamChips({ t, ctx, className, plain }: { t: Ticket; ctx: TeamsCtx; className?: string; plain?: boolean }) {
  const team = ctx.hasTeams ? ctx.team(t.team_id) : undefined;
  const brand = ctx.multiBrand ? ctx.brand(t.brand_id) : undefined;
  if (!team && !brand) return null;
  return (
    // Plain chips sit in a tight line: one that doesn't fit wraps out of sight instead of being cut.
    <span className={cn("flex min-w-0 shrink-0 items-center", plain ? "h-[19px] flex-wrap gap-x-2 overflow-hidden" : "gap-1", className)}>
      {team && <TeamChip team={team} plain={plain} className={plain ? "text-fg-3" : undefined} />}
      {brand && <BrandChip brand={brand} plain={plain} className={plain ? "text-fg-4" : undefined} />}
    </span>
  );
}

// ─── Addresses ─────────────────────────────────────────────────────────────

/** "Name <email>", the label of an address everywhere. */
export function addressLabel(m: Pick<Mailbox, "name" | "display_name" | "email">) {
  const name = m.display_name || m.name;
  return name && name !== m.email ? `${name} <${m.email}>` : m.email;
}

export function firstName(name: string) {
  return name.trim().split(/\s+/)[0] ?? name;
}

/**
 * The From name customers see (contracts/teams.md "Reply address"): the address's
 * format with {agent} {agent_first} {team} {brand} {mailbox} filled in (lib/fromname).
 * Without an author the agent variable and the words up to the next variable are dropped.
 */
export function fillFromName(format: string, v: { agent?: string | null; team?: string | null; brand?: string | null; mailbox: string }) {
  return fillFormat(format.trim() || "{agent} at {mailbox}", { agent: v.agent ?? "", team: v.team ?? "", brand: v.brand ?? "", mailbox: v.mailbox }, HELPDESK_FROM_VARS);
}

/** The alias of the reply mailbox the customer wrote to, or null. */
export function ticketAlias(m: Mailbox, t: Pick<Ticket, "received_at">): string | null {
  const r = t.received_at?.toLowerCase();
  return r && m.aliases?.some((a) => a.toLowerCase() === r) ? r : null;
}

/**
 * Whether mail as an alias goes out From it (contracts/teams.md "Aliases"): always over SMTP,
 * through Gmail for a verified send-as address, through Microsoft unless it refused the alias.
 */
export function sendsAsAlias(m: Mailbox, alias: string): boolean {
  const st = m.alias_settings?.[alias]?.send_as ?? "unknown";
  if (m.outbound_kind === "smtp") return true;
  if (m.outbound_kind === "google") return st === "verified";
  if (m.outbound_kind === "microsoft") return st !== "unverified";
  return false;
}

/** The address a ticket's replies answer as: the alias the customer wrote to when it belongs to the reply mailbox. */
export function sendingAddress(m: Mailbox, t: Pick<Ticket, "received_at" | "origin_mailbox_id">) {
  return ticketAlias(m, t) ?? m.email;
}

/**
 * The From line of a reply: `{from name} <address>`, with the alias's own name. When the
 * provider does not send as the alias, the reply goes From the mailbox and answers to the alias.
 */
export function fromLine(ctx: TeamsCtx, t: Ticket, m: Mailbox, agent: string | null) {
  const alias = ticketAlias(m, t);
  const name = fillFromName(m.from_name_format, {
    agent,
    team: ctx.team(t.team_id)?.name ?? null,
    brand: ctx.brand(t.brand_id ?? m.brand_id)?.name ?? null,
    mailbox: (alias && m.alias_settings?.[alias]?.display_name) || m.display_name || m.name,
  });
  if (alias && !sendsAsAlias(m, alias)) return { name, email: m.email, replyTo: alias };
  return { name, email: alias ?? m.email, replyTo: null as string | null };
}

/** Whether the address belongs to the brand: its own, or one of its aliases has it. */
export function addressOfBrand(m: Mailbox, brandId: string) {
  return m.brand_id === brandId || Object.values(m.alias_settings ?? {}).some((s) => s.brand_id === brandId);
}

/** Addresses a reply on this ticket may go out from: its brand's, minus private teams' the caller isn't on. */
export function replyAddresses(ctx: TeamsCtx, t: Pick<Ticket, "brand_id">): Mailbox[] {
  const all = ctx.mailboxes.filter((m) => m.status !== "disabled" && ctx.canSendFrom(m));
  const own = t.brand_id ? all.filter((m) => addressOfBrand(m, t.brand_id!)) : all;
  return own.length ? own : all;
}

/**
 * Moving a ticket to another team switches its reply address when the current
 * address's default team is the old team (or none) and the brand has an address
 * whose default team is the new team. Otherwise it stays.
 */
export function replyAddressAfterMove(t: Ticket, toTeam: string | null, mailboxes: Mailbox[]): { from: Mailbox; to: Mailbox } | null {
  if (!toTeam || toTeam === t.team_id) return null;
  const cur = mailboxes.find((m) => m.id === t.mailbox_id);
  if (!cur) return null;
  if (cur.team_id && cur.team_id !== t.team_id) return null;
  const target = mailboxes
    .filter((m) => m.brand_id === t.brand_id && m.team_id === toTeam && m.status !== "disabled")
    .sort((a, b) => Date.parse(a.created_at) - Date.parse(b.created_at))[0];
  return target && target.id !== cur.id ? { from: cur, to: target } : null;
}

// ─── Routing ───────────────────────────────────────────────────────────────

/** One plain line on how a ticket reached its team (or null when it says nothing new). */
export function routedText(t: Ticket, ctx: TeamsCtx, tr: TFunction<"inbox">, ollie: string): string | null {
  const r = t.routed;
  const team = ctx.team(t.team_id)?.name;
  const widget = ctx.widget(t.widget_id);
  if (!r) return t.received_at && t.channel === "email" ? tr("routed.arrived", { address: t.received_at }) : null;
  switch (r.via) {
    case "rule":
      return team ? tr("routed.rule", { team, rule: r.rule_name || tr("routed.aRule") }) : tr("routed.ruleNoTeam", { rule: r.rule_name || tr("routed.aRule") });
    case "address":
      return tr("routed.arrived", { address: t.received_at || ctx.mailbox(t.origin_mailbox_id)?.email || tr("routed.anAddress") });
    case "topic": {
      const topic = widget?.settings.topics?.find((x) => x.team_id === t.team_id)?.label;
      const w = widget?.name;
      if (w && topic) return tr("routed.topic", { widget: w, topic });
      return topic ? tr("routed.topicOnly", { topic }) : w ? tr("routed.widget", { widget: w }) : tr("routed.aWidget");
    }
    case "widget":
      return widget ? tr("routed.widget", { widget: widget.name }) : tr("routed.aWidget");
    case "triage":
      return team ? tr("routed.triage", { team, name: ollie }) : tr("routed.triageNoTeam", { name: ollie });
    case "automation":
      return r.rule_name ? tr("routed.automationNamed", { name: r.rule_name }) : tr("routed.automation");
    case "agent":
      return r.by ? tr("routed.agent", { name: r.by.name }) : tr("routed.agentSomeone");
    case "api":
      return tr("routed.api");
    default:
      return null;
  }
}

export function useRoutedText(t: Ticket, ctx: TeamsCtx) {
  const { t: tr } = useTranslation("inbox");
  const ollie = useAssistantName();
  return routedText(t, ctx, tr, ollie);
}

// ─── Moving tickets between teams ──────────────────────────────────────────

/**
 * The SLA policy a ticket of this team and priority falls under: first enabled match in order. A
 * priority or team of its own only wins between policies at the same position (as the server).
 */
function policyFor(policies: SlaPolicy[] | undefined, teamId: string | null, priority: Ticket["priority"]) {
  return (policies ?? [])
    .filter((p) => p.enabled && (p.team_id == null || p.team_id === teamId) && (p.priority === null || p.priority === priority))
    .sort((a, b) => a.position - b.position || Number(a.priority === null) - Number(b.priority === null) || Number(a.team_id == null) - Number(b.team_id == null))[0];
}

export type TransferPlan = {
  to: Team | null;
  count: number;
  /** Reply address switches, grouped by from and to. */
  switches: { from: Mailbox; to: Mailbox; n: number }[];
  /** Tickets that become private (were public or in no team). */
  becomePrivate: number;
  /** Tickets leaving a private team for a public one (or none). */
  becomePublic: number;
  /** The caller is not on the private target team: those tickets leave their view. */
  loseSight: number;
  memberCount: number;
  mode: AssignMode | null;
  /** Unassigned open tickets the team's assignment rule will hand out. */
  autoAssign: number;
  /** Assigned tickets whose assignee is not on the private target team. */
  offTeam: { name: string; n: number }[];
  hours: "team" | "workspace" | null;
  sla: { from: string | null; to: string | null; n: number }[];
};

export function planTransfer(tickets: Ticket[], toId: string | null, ctx: TeamsCtx, opts: { policies?: SlaPolicy[]; workspaceMode?: AssignMode | null }): TransferPlan {
  const to = ctx.team(toId) ?? null;
  const moving = tickets.filter((t) => (t.team_id ?? null) !== toId);
  const sw = new Map<string, { from: Mailbox; to: Mailbox; n: number }>();
  const sla = new Map<string, { from: string | null; to: string | null; n: number }>();
  const off = new Map<string, { name: string; n: number }>();
  let becomePrivate = 0,
    becomePublic = 0,
    autoAssign = 0,
    hoursChange = 0;
  const mode = to ? (to.assign_mode ?? opts.workspaceMode ?? null) : null;
  const members = new Set(to ? (to.members?.length ? to.members.map((m) => m.user_id) : to.member_ids) : []);
  for (const t of moving) {
    const s = replyAddressAfterMove(t, toId, ctx.mailboxes);
    if (s) {
      const k = `${s.from.id}>${s.to.id}`;
      sw.set(k, { ...s, n: (sw.get(k)?.n ?? 0) + 1 });
    }
    const was = ctx.team(t.team_id);
    if (to?.private && !(was?.private && was.id === to.id)) becomePrivate++;
    if (was?.private && !to?.private) becomePublic++;
    if (!t.assignee && mode && mode !== "none" && (t.status === "open" || t.status === "pending")) autoAssign++;
    if (to?.private && t.assignee && !members.has(t.assignee.id)) {
      off.set(t.assignee.id, { name: t.assignee.name, n: (off.get(t.assignee.id)?.n ?? 0) + 1 });
    }
    if (!!was?.business_hours !== !!to?.business_hours || (was?.business_hours && to?.business_hours && was.id !== to.id)) hoursChange++;
    if (opts.policies) {
      const cur = t.sla ? opts.policies.find((p) => p.id === t.sla!.policy_id) : undefined;
      const next = policyFor(opts.policies, toId, t.priority);
      if ((cur?.id ?? null) !== (next?.id ?? null)) {
        const k = `${cur?.id}>${next?.id}`;
        sla.set(k, { from: cur?.name ?? null, to: next?.name ?? null, n: (sla.get(k)?.n ?? 0) + 1 });
      }
    }
  }
  return {
    to,
    count: moving.length,
    switches: [...sw.values()],
    becomePrivate,
    becomePublic,
    loseSight: to?.private && !ctx.myTeams.has(to.id) ? moving.filter((t) => t.assignee?.id !== ctx.me).length : 0,
    memberCount: members.size,
    mode,
    autoAssign,
    offTeam: [...off.values()],
    hours: hoursChange ? (to?.business_hours ? "team" : "workspace") : null,
    sla: [...sla.values()],
  };
}

function PlanLine({ icon, children, hue }: { icon: ReactNode; children: ReactNode; hue?: "amber" }) {
  return (
    <li className="flex items-start gap-2 text-[12px] leading-[17px] text-fg-2">
      <span className={cn("mt-px flex size-4 shrink-0 items-center justify-center [&_svg]:size-3", hue ? tone[hue].text : "text-fg-3")}>{icon}</span>
      <span className="min-w-0">{children}</span>
    </li>
  );
}

const B = ({ children }: { children: ReactNode }) => <span className="font-medium text-fg">{children}</span>;
const Mono = ({ children }: { children: ReactNode }) => <span className="mono text-[11.5px] text-fg">{children}</span>;

/** What moving changes, in a few lines: reply address, privacy, assignment, hours and SLA. */
export function TransferSummary({ plan, single }: { plan: TransferPlan; single: boolean }) {
  const { t } = useTranslation("inbox");
  const n = plan.count;
  const of = (k: number) => (single ? "" : t("transfer.of", { count: k, total: n }));
  const lines: ReactNode[] = [];
  for (const s of plan.switches) {
    lines.push(
      <PlanLine key={`sw-${s.from.id}-${s.to.id}`} icon={<Mail />}>
        {of(s.n)}
        {t("transfer.replyFrom")} <Mono>{s.to.email}</Mono> <span className="text-fg-3">{t("transfer.insteadOf", { address: s.from.email })}</span>
      </PlanLine>,
    );
  }
  if (plan.to?.private && plan.becomePrivate) {
    lines.push(
      <PlanLine key="private" icon={<Lock />} hue={plan.loseSight ? "amber" : undefined}>
        {of(plan.becomePrivate)}
        {t("transfer.private", { team: plan.to.name, count: plan.memberCount })}
        {plan.loseSight > 0 && <span className="block text-fg-3">{t("transfer.loseSight", { count: single ? 1 : plan.loseSight })}</span>}
      </PlanLine>,
    );
  }
  if (plan.becomePublic) {
    lines.push(
      <PlanLine key="public" icon={<Users />}>
        {of(plan.becomePublic)}
        {t("transfer.public")}
      </PlanLine>,
    );
  }
  for (const o of plan.offTeam) {
    lines.push(
      <PlanLine key={`off-${o.name}`} icon={<EyeOff />} hue="amber">
        {t("transfer.offTeam", { name: o.name, team: plan.to?.name ?? "" })}
      </PlanLine>,
    );
  }
  if (plan.to && plan.mode) {
    lines.push(
      <PlanLine key="assign" icon={<UserRound />}>
        {plan.autoAssign > 0 ? (
          <>
            {of(plan.autoAssign)}
            {t(plan.mode === "load_balanced" ? "transfer.autoLoad" : "transfer.autoRound", { team: plan.to.name })}
          </>
        ) : plan.mode === "none" ? (
          t("transfer.manual", { team: plan.to.name })
        ) : (
          t("transfer.keepsAssignee")
        )}
      </PlanLine>,
    );
  }
  if (plan.hours) {
    lines.push(
      <PlanLine key="hours" icon={<Clock />}>
        {plan.hours === "team" ? t("transfer.hoursTeam", { team: plan.to?.name ?? "" }) : t("transfer.hoursWorkspace")}
      </PlanLine>,
    );
  }
  for (const s of plan.sla) {
    lines.push(
      <PlanLine key={`sla-${s.from}-${s.to}`} icon={<Timer />}>
        {of(s.n)}
        {s.to ? (
          <>
            {t("transfer.slaTo")} <B>{s.to}</B>
          </>
        ) : (
          t("transfer.slaNone")
        )}
      </PlanLine>,
    );
  }
  if (!lines.length) return <p className="text-[12px] text-fg-3">{t("transfer.nothingElse")}</p>;
  return <ul className="flex flex-col gap-1.5">{lines}</ul>;
}

/**
 * The team picker with its move preview: pick a team (type to filter, Enter),
 * see what changes, Enter again moves. Escape closes, Backspace goes back.
 */
export function TeamMove({
  tickets,
  trigger,
  open: openProp,
  onOpenChange,
  onMove,
  side = "bottom",
  align = "start",
}: {
  tickets: Ticket[];
  trigger: ReactNode;
  open?: boolean;
  onOpenChange?: (o: boolean) => void;
  onMove: (teamId: string | null) => void;
  side?: "top" | "bottom" | "left" | "right";
  align?: "start" | "center" | "end";
}) {
  const { t } = useTranslation("inbox");
  const ctx = useTeamsCtx();
  const reduce = useReducedMotion();
  const [inner, setInner] = useState(false);
  const open = openProp ?? inner;
  const [pending, setPending] = useState<string | null | undefined>(undefined);
  const [query, setQuery] = useState("");
  const confirmRef = useRef<HTMLButtonElement>(null);
  const { data: policies } = useQuery({ ...q.slaPolicies(), enabled: open });
  const { data: assignment } = useQuery({ ...q.assignment(), enabled: open, retry: false });
  const setOpen = (o: boolean) => {
    onOpenChange?.(o);
    if (openProp === undefined) setInner(o);
    if (!o) {
      setPending(undefined);
      setQuery("");
    }
  };
  const single = tickets.length === 1;
  const current = single ? (tickets[0].team_id ?? "__none") : null;
  const teams = sortTeams(ctx.teams, ctx.myTeams);
  const plan = pending !== undefined ? planTransfer(tickets, pending, ctx, { policies, workspaceMode: assignment?.mode ?? null }) : null;
  const confirm = () => {
    if (pending === undefined) return;
    onMove(pending);
    setOpen(false);
  };
  const choose = (v: string) => {
    const id = v === "__none" ? null : v;
    if (single && (tickets[0].team_id ?? null) === id) return setOpen(false);
    setPending(id);
  };

  return (
    <P.Root open={open} onOpenChange={setOpen}>
      <P.Trigger asChild>{trigger}</P.Trigger>
      <P.Portal>
        <P.Content
          align={align}
          side={side}
          sideOffset={6}
          collisionPadding={8}
          style={{ width: plan ? 300 : 240 }}
          className={cn(floatingPanel, "overflow-hidden p-0 transition-[width] duration-150 ease-out motion-reduce:transition-none")}
          onClick={(e) => e.stopPropagation()}
        >
          <AnimatePresence mode="wait" initial={false}>
            {plan ? (
              <motion.div
                key="plan"
                initial={reduce ? { opacity: 0 } : { opacity: 0, x: 8 }}
                animate={{ opacity: 1, x: 0 }}
                exit={reduce ? { opacity: 0 } : { opacity: 0, x: -6 }}
                transition={{ duration: dur.fast, ease: ease.out }}
                onKeyDown={(e) => {
                  if (e.key === "Backspace" || e.key === "ArrowLeft") {
                    e.preventDefault();
                    setPending(undefined);
                  }
                }}
              >
                <div className="flex items-center gap-2 border-b border-line px-3 py-2.5">
                  <button
                    type="button"
                    aria-label={t("transfer.back")}
                    onClick={() => setPending(undefined)}
                    className="-ml-1 flex size-6 shrink-0 items-center justify-center rounded-md text-fg-3 outline-none transition-colors hover:bg-hover hover:text-fg focus-visible:shadow-[0_0_0_2px_var(--focus)]"
                  >
                    <ArrowLeft className="size-3.5" />
                  </button>
                  {plan.to && <TeamDot team={plan.to} />}
                  <span className="min-w-0 flex-1 truncate text-[12.5px] font-medium text-fg">
                    {plan.to
                      ? single
                        ? t("transfer.title", { team: plan.to.name })
                        : t("transfer.titleMany", { team: plan.to.name, count: plan.count })
                      : single
                        ? t("transfer.titleNone")
                        : t("transfer.titleNoneMany", { count: plan.count })}
                  </span>
                </div>
                <div className="px-3 py-2.5">
                  <TransferSummary plan={plan} single={single} />
                </div>
                <div className="flex items-center justify-end gap-1.5 border-t border-line bg-sunken px-2.5 py-2">
                  <Button size="xs" variant="ghost" onClick={() => setOpen(false)}>
                    {t("common.cancel")}
                  </Button>
                  <Button ref={confirmRef} autoFocus size="xs" variant="primary" onClick={confirm} iconRight={<CornerDownLeft className="!size-3 opacity-60" />}>
                    {t("transfer.move")}
                  </Button>
                </div>
              </motion.div>
            ) : (
              <motion.div key="pick" initial={false} animate={{ opacity: 1 }} exit={{ opacity: 0 }} transition={{ duration: dur.fast }}>
                <Command loop className="flex flex-col">
                  <div className={pickerSearch.row}>
                    <Command.Input autoFocus value={query} onValueChange={setQuery} placeholder={t("teams.moveTo")} className={pickerSearch.input} />
                  </div>
                  <Command.List className="max-h-[280px] overflow-y-auto p-1">
                    <Command.Empty className="px-2 py-5 text-center text-xs text-fg-3">{t("teams.noMatch")}</Command.Empty>
                    {teams.map((x) => (
                      <Command.Item
                        key={x.id}
                        value={`${x.name} ${x.id}`}
                        onSelect={() => choose(x.id)}
                        className="flex h-7 cursor-default items-center gap-2 rounded-md px-2 text-[12.5px] text-fg data-[selected=true]:bg-hover [&_svg]:shrink-0"
                      >
                        <TeamDot team={x} />
                        <span className="min-w-0 flex-1 truncate">{x.name}</span>
                        {x.private && <Lock className="size-3 text-fg-4" />}
                        {!ctx.myTeams.has(x.id) && x.private && <span className="text-[10.5px] text-fg-4">{t("teams.notMember")}</span>}
                        {current === x.id && <Check className="size-3.5 text-fg" />}
                      </Command.Item>
                    ))}
                    <Command.Item
                      value={`${t("props.noTeam")} __none`}
                      onSelect={() => choose("__none")}
                      className="flex h-7 cursor-default items-center gap-2 rounded-md px-2 text-[12.5px] text-fg-2 data-[selected=true]:bg-hover"
                    >
                      <span className="size-2 shrink-0 rounded-full border border-dashed border-line-strong" />
                      <span className="flex-1">{t("props.noTeam")}</span>
                      {current === "__none" && <Check className="size-3.5 text-fg" />}
                    </Command.Item>
                  </Command.List>
                </Command>
              </motion.div>
            )}
          </AnimatePresence>
        </P.Content>
      </P.Portal>
    </P.Root>
  );
}

