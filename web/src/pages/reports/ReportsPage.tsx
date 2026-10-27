import { dateLocale, datePattern } from "@/i18n/dates";
import { keepPreviousData, useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { getRouteApi } from "@tanstack/react-router";
import { differenceInCalendarDays, format, parseISO, subDays } from "date-fns";
import { BarChart3, CircleCheck, CircleDot, Inbox, Reply, ShieldAlert, Smile, Timer } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { PageHeader } from "@/components/layout";
import { EmptyState } from "@/components/ui/empty";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { Segmented } from "@/components/ui/tabs";
import { Button } from "@/components/ui/button";
import { TagChip } from "@/components/ui/badge";
import { Avatar } from "@/components/ui/avatar";
import { RangePicker } from "@/components/reports/RangePicker";
import { Sparkline, VolumeChart, VolumeLegend, VolumeTable } from "@/components/reports/VolumeChart";
import { ChannelBars } from "@/components/reports/ChannelBars";
import { SeverityBars } from "@/components/reports/SeverityBars";
import { AgentTable } from "@/components/reports/AgentTable";
import { AgentWorkTable } from "@/components/reports/AgentWork";
import { AnimatedNumber } from "@/components/motion";
import { Bar, BreakdownBars, ShareBar, type BarRow } from "@/components/reports/Bars";
import { BrandFilter, GroupTable, NO_TEAM, TeamFilter, brandRows, teamRows, useReportScope, type GroupRow } from "@/components/reports/TeamBrand";
import { PriorityIcon, StatusIcon, priorityLabel, priorityOrder, statusLabel } from "@/components/tickets/bits";
import type { ReportsOverview, ReportsQuery, ReportsRange, Ticket } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { minutes } from "@/lib/format";
import { q } from "@/lib/queries";
import { isTone, priorityTone, SERIES, statusTone, tone, toneFor, toneVar, type Tone } from "@/lib/tones";
import { reportsQuery } from "@/router";

const route = getRouteApi("/app/reports");

/** The same-length period just before `r`, with the same filters, for deltas. */
function previous<R extends ReportsRange>(r: R): R {
  const from = parseISO(r.from);
  const len = differenceInCalendarDays(parseISO(r.to), from) + 1;
  return { ...r, from: format(subDays(from, len), "yyyy-MM-dd"), to: format(subDays(from, 1), "yyyy-MM-dd") };
}

const OPEN_STATUSES = ["open", "pending", "on_hold"] as Ticket["status"][];

/** The open tickets behind the backlog and open work cards, narrowed like the report. */
function openWork(r: ReportsQuery) {
  return { status: OPEN_STATUSES, team_id: r.team_id, brand_id: r.brand_id };
}

export default function ReportsPage() {
  const { t } = useTranslation("reports");
  const search = route.useSearch();
  const navigate = route.useNavigate();
  const range = reportsQuery(search);
  const scope = useReportScope();
  const filtered = !!(range.team_id || range.brand_id);
  // A link to a team the caller may not see (a private team they are not on) drops the filter.
  const lostTeam = !!search.team && search.team !== NO_TEAM && scope.teamsLoaded && !scope.team(search.team);
  useEffect(() => {
    if (lostTeam) void navigate({ search: (s) => ({ ...s, team: undefined }), replace: true });
  }, [lostTeam, navigate]);
  const report = useQuery({ ...q.reports(range), placeholderData: keepPreviousData });
  const prev = useQuery({ ...q.reports(previous(range)), placeholderData: keepPreviousData });
  const d = report.data;
  const p = prev.data;
  const refetching = report.isFetching && report.isPlaceholderData;
  const empty = d && d.created === 0 && d.solved === 0 && d.open_now === 0;
  const teamTable = d && scope.hasTeams && !range.team_id ? teamRows(d, scope, t("filter.noTeam")) : null;
  const brandTable = d && scope.multiBrand && !range.brand_id ? brandRows(d, scope) : null;
  const days = differenceInCalendarDays(parseISO(range.to), parseISO(range.from)) + 1;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader
        title={t("title")}
        icon={<BarChart3 />}
        subtitle={<span className="hidden sm:inline">{t("compared", { count: days })}</span>}
        actions={
          <>
            {scope.hasTeams && <TeamFilter scope={scope} value={search.team} onChange={(team) => void navigate({ search: (s) => ({ ...s, team }) })} />}
            {(scope.multiBrand || search.brand) && <BrandFilter scope={scope} value={search.brand} onChange={(brand) => void navigate({ search: (s) => ({ ...s, brand }) })} />}
            <RangePicker value={range} onChange={(r) => void navigate({ search: (s) => ({ ...s, ...r }) })} />
          </>
        }
      />
      <div className="min-h-0 flex-1 overflow-y-auto">
        {report.isLoading ? (
          <ReportSkeleton />
        ) : report.error ? (
          <EmptyState
            lit={3}
            title={t("error.title")}
            description={t("error.description")}
            action={<Button onClick={() => void report.refetch()}>{t("error.retry")}</Button>}
          />
        ) : !d || (empty && !filtered) ? (
          <SamplePreview />
        ) : empty ? (
          <EmptyState
            lit={1}
            art="search"
            title={t("filtered.emptyTitle")}
            description={t("filtered.emptyDescription")}
            action={<Button onClick={() => void navigate({ search: (s) => ({ ...s, team: undefined, brand: undefined }) })}>{t("filtered.clear")}</Button>}
          />
        ) : (
          <div className={cn("flex flex-col gap-4 px-4 pb-12 pt-4 transition-opacity duration-150 md:px-6 md:pt-5", refetching && "opacity-60")}>
            <Kpis d={d} p={p} />
            <div className="grid grid-cols-1 gap-4 xl:grid-cols-3">
              <VolumeCard d={d} className="xl:col-span-2" />
              <BacklogCard filter={range} openNow={d.open_now} />
            </div>
            <div className={cn("grid grid-cols-1 gap-4", d.by_severity ? "md:grid-cols-2 xl:grid-cols-4" : "lg:grid-cols-3")}>
              <Card title={t("channel.title")} subtitle={t("channel.subtitle")}>
                <ChannelBars data={d.by_channel} />
              </Card>
              {d.by_severity && (
                <Card title={t("severity.title")} subtitle={t("severity.subtitle")}>
                  <SeverityBars data={d.by_severity} />
                </Card>
              )}
              <OpenWorkCards filter={range} />
            </div>
            {(teamTable || brandTable) && (
              <div className={cn("grid grid-cols-1 gap-4", teamTable && brandTable && "xl:grid-cols-2")}>
                {teamTable && <TeamCard rows={teamTable.rows} hidden={teamTable.hidden} />}
                {brandTable && <BrandCard rows={brandTable} />}
              </div>
            )}
            <div className="grid grid-cols-1 gap-4 xl:grid-cols-3">
              <Card title={t("agents.title")} subtitle={t("agents.subtitle")} flush className="xl:col-span-2">
                {d.by_agent.length ? (
                  <AgentTable rows={d.by_agent} />
                ) : (
                  <EmptyState compact lit={0} title={t("agents.emptyTitle")} description={t("agents.emptyDescription")} />
                )}
              </Card>
              <ResponseCard d={d} />
            </div>
            <AgentWorkCard filter={range} />
          </div>
        )}
      </div>
    </div>
  );
}

function Card({
  title,
  subtitle,
  actions,
  children,
  flush,
  className,
}: {
  title: string;
  subtitle?: string;
  actions?: ReactNode;
  children: ReactNode;
  flush?: boolean;
  className?: string;
}) {
  return (
    <section className={cn("flex min-w-0 flex-col rounded-lg border border-line bg-surface shadow-xs", className)}>
      <div className="flex min-h-12 items-start justify-between gap-4 px-4 pt-3.5">
        <div className="min-w-0">
          <h2 className="text-[13px] font-semibold tracking-[-0.01em]">{title}</h2>
          {subtitle && <p className="mt-0.5 text-xs text-fg-3">{subtitle}</p>}
        </div>
        {actions && <div className="flex shrink-0 items-center gap-3">{actions}</div>}
      </div>
      <div className={cn("flex-1", flush ? "pt-2" : "px-4 pb-4 pt-4")}>{children}</div>
    </section>
  );
}

type Tile = {
  label: string;
  value: number | null;
  format: (n: number) => string;
  unit?: string;
  icon: ReactNode;
  tone: Tone;
  delta?: { now: number | null; before: number | null; upIsGood: boolean | null };
  trend?: number[];
  note?: string;
};

function Kpis({ d, p }: { d: ReportsOverview; p?: ReportsOverview }) {
  const { t } = useTranslation("reports");
  const int = (n: number) => Math.round(n).toLocaleString();
  const dur = (n: number) => minutes(Math.round(n));
  const tiles: Tile[] = [
    { label: t("kpi.created"), value: d.created, format: int, icon: <Inbox />, tone: SERIES[0], delta: { now: d.created, before: p?.created ?? null, upIsGood: null }, trend: d.by_day.map((x) => x.created) },
    { label: t("kpi.solved"), value: d.solved, format: int, icon: <CircleCheck />, tone: SERIES[2], delta: { now: d.solved, before: p?.solved ?? null, upIsGood: true }, trend: d.by_day.map((x) => x.solved) },
    { label: t("kpi.openNow"), value: d.open_now, format: int, icon: <CircleDot />, tone: "amber", note: t("kpi.rightNow") },
    {
      label: t("kpi.firstResponse"),
      value: d.first_response_p50_minutes,
      format: dur,
      icon: <Reply />,
      tone: "blue",
      note: t("kpi.median"),
      delta: { now: d.first_response_p50_minutes, before: p?.first_response_p50_minutes ?? null, upIsGood: false },
    },
    {
      label: t("kpi.resolution"),
      value: d.resolution_p50_minutes,
      format: dur,
      icon: <Timer />,
      tone: "violet",
      note: t("kpi.median"),
      delta: { now: d.resolution_p50_minutes, before: p?.resolution_p50_minutes ?? null, upIsGood: false },
    },
    {
      label: t("kpi.csat"),
      value: d.csat_avg,
      format: (n) => n.toFixed(1),
      unit: d.csat_avg === null ? undefined : "/ 5",
      icon: <Smile />,
      tone: "pink",
      note: t("kpi.ratings", { count: d.csat_count }),
      delta: { now: d.csat_avg, before: p?.csat_avg ?? null, upIsGood: true },
    },
    {
      label: t("kpi.slaBreached"),
      value: d.sla_breached,
      format: int,
      icon: <ShieldAlert />,
      tone: d.sla_breached ? "red" : "gray",
      delta: { now: d.sla_breached, before: p?.sla_breached ?? null, upIsGood: false },
    },
  ];
  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-4 lg:grid-cols-7">
      {tiles.map((x) => (
        <div key={x.label} className="flex min-w-0 flex-col gap-3 rounded-lg border border-line bg-surface px-4 py-3.5 shadow-xs">
          <div className="flex items-center justify-between gap-2">
            <span className="label-caps truncate">{x.label}</span>
            <span className={cn("hidden size-6 shrink-0 items-center justify-center rounded-md 2xl:flex [&_svg]:size-3.5", tone[x.tone].chip)}>{x.icon}</span>
          </div>
          <div className="flex items-end justify-between gap-2">
            <span className="flex min-w-0 items-baseline gap-1">
              {x.value === null ? (
                <span className="text-[26px] font-semibold leading-8 tracking-[-0.03em] text-fg-4">–</span>
              ) : (
                <AnimatedNumber flash value={x.value} format={x.format} className="text-[26px] font-semibold leading-8 tracking-[-0.03em] tabular-nums text-fg" />
              )}
              {x.unit && <span className="mono text-xs text-fg-4">{x.unit}</span>}
            </span>
          </div>
          <span className="flex min-h-5 items-center gap-1.5 text-[11.5px] text-fg-3">
            {x.delta && <Delta {...x.delta} />}
            {x.note && <span className="truncate text-fg-4">{x.note}</span>}
            {x.trend && (
              <span className="ml-auto">
                <Sparkline values={x.trend} color={toneVar(x.tone)} width={56} />
              </span>
            )}
          </span>
        </div>
      ))}
    </div>
  );
}

function Delta({ now, before, upIsGood }: { now: number | null; before: number | null; upIsGood: boolean | null }) {
  const { t } = useTranslation("reports");
  if (now === null || before === null || before === 0) return null;
  const pct = Math.round(((now - before) / before) * 100);
  if (pct === 0) return <span className="mono rounded-sm bg-hover px-1.5 py-px text-[11px] text-fg-3">{t("delta.none")}</span>;
  const good = pct > 0 === upIsGood;
  const hue: Tone = upIsGood === null ? "gray" : good ? "green" : "red";
  return (
    <span className={cn("mono inline-flex items-center gap-0.5 rounded-sm px-1.5 py-px text-[11px] font-medium", tone[hue].chip)} title={t("delta.title")}>
      {pct > 0 ? "↑" : "↓"}
      {Math.abs(pct)}%
      {upIsGood !== null && <span className="sr-only">{good ? t("delta.better") : t("delta.worse")}</span>}
    </span>
  );
}

function Figure({ label, value, sub }: { label: string; value: ReactNode; sub?: ReactNode }) {
  return (
    <div className="min-w-0">
      <div className="label-caps">{label}</div>
      <div className="mt-1 flex items-baseline gap-1.5 text-[15px] font-semibold tabular-nums tracking-[-0.01em] text-fg">
        {value}
        {sub && <span className="truncate text-[11.5px] font-normal text-fg-4">{sub}</span>}
      </div>
    </div>
  );
}

function VolumeCard({ d, className }: { d: ReportsOverview; className?: string }) {
  const { t } = useTranslation("reports");
  const [view, setView] = useState<"chart" | "table">("chart");
  const avg = d.by_day.length ? d.created / d.by_day.length : 0;
  const busiest = d.by_day.reduce<ReportsOverview["by_day"][number] | null>((m, x) => (!m || x.created > m.created ? x : m), null);
  const rate = d.created ? Math.round((d.solved / d.created) * 100) : null;
  return (
    <Card
      title={t("volume.title")}
      subtitle={t("volume.subtitle")}
      className={className}
      actions={
        <>
          <VolumeLegend />
          <Segmented
            size="xs"
            value={view}
            onChange={setView}
            options={[
              { value: "chart", label: t("volume.chart") },
              { value: "table", label: t("volume.table") },
            ]}
          />
        </>
      }
    >
      <div className="mb-4 grid grid-cols-3 gap-4 border-b border-line-soft pb-4">
        <Figure label={t("volume.avg")} value={<AnimatedNumber value={avg} format={(n) => n.toFixed(1)} />} sub={t("volume.perDay")} />
        <Figure
          label={t("volume.busiest")}
          value={busiest && busiest.created ? <AnimatedNumber value={busiest.created} /> : "–"}
          sub={busiest && busiest.created ? format(parseISO(busiest.date), datePattern("weekdayDayMonth"), { locale: dateLocale() }) : undefined}
        />
        <Figure label={t("volume.solveRate")} value={rate === null ? "–" : <AnimatedNumber value={rate} format={(n) => `${Math.round(n)}%`} />} sub={t("volume.solvedOfCreated")} />
      </div>
      {view === "chart" ? <VolumeChart days={d.by_day} /> : <VolumeTable days={d.by_day} />}
    </Card>
  );
}

/**
 * Open work right now, by status: from the inbox counts, or, with a team or brand
 * filter, from the open tickets of that team or brand.
 */
function BacklogCard({ filter, openNow }: { filter: ReportsQuery; openNow: number }) {
  const { t } = useTranslation("reports");
  useTranslation("indicators");
  const narrowed = !!(filter.team_id || filter.brand_id);
  const counts = useQuery({ ...q.counts(), enabled: !narrowed });
  const list = useInfiniteQuery({ ...q.tickets(openWork(filter)), enabled: narrowed });
  const tickets = list.data?.pages.flatMap((pg) => pg.data) ?? [];
  const c = narrowed
    ? list.data && {
        open: tickets.filter((x) => x.status === "open").length,
        pending: tickets.filter((x) => x.status === "pending").length,
        on_hold: tickets.filter((x) => x.status === "on_hold").length,
        unassigned: tickets.filter((x) => !x.assignee).length,
      }
    : counts.data;
  const loading = narrowed ? list.isLoading : counts.isLoading;
  const rows: BarRow[] = c
    ? (["open", "pending", "on_hold"] as const).map((s) => ({
        id: s,
        label: statusLabel(s),
        text: statusLabel(s),
        value: c[s],
        tone: statusTone[s],
        icon: <StatusIcon status={s} />,
      }))
    : [];
  const listed = rows.reduce((a, r) => a + r.value, 0);
  // A partial list still knows the true total: the report's open now.
  const total = narrowed && list.hasNextPage ? openNow : listed;
  return (
    <Card title={t("backlog.title")} subtitle={narrowed && list.hasNextPage ? t("open.sample", { count: tickets.length }) : t("backlog.subtitle")}>
      {loading ? (
        <BarsSkeleton />
      ) : !c ? null : (
        <div className="flex flex-col gap-5">
          <div className="grid grid-cols-2 gap-4">
            <Figure label={t("backlog.open")} value={<AnimatedNumber value={total} />} sub={t("backlog.tickets")} />
            <Figure label={t("backlog.unassigned")} value={<AnimatedNumber value={c.unassigned} />} sub={listed ? `${Math.round((c.unassigned / listed) * 100)}%` : undefined} />
          </div>
          <ShareBar rows={rows} />
          <BreakdownBars
            rows={rows}
            unit={(n) => t("unit.tickets", { count: n })}
            empty={<EmptyState compact lit={1} title={t("backlog.emptyTitle")} description={t("backlog.emptyDescription")} />}
          />
        </div>
      )}
    </Card>
  );
}

/** Tickets per team the caller may see, and those without a team. */
function TeamCard({ rows, hidden }: { rows: GroupRow[]; hidden: boolean }) {
  const { t } = useTranslation("reports");
  return (
    <Card title={t("byTeam.title")} subtitle={t("byTeam.subtitle")} flush>
      {rows.length ? (
        <GroupTable rows={rows} caption={t("byTeam.caption")} head={t("byTeam.team")} />
      ) : (
        <EmptyState compact lit={0} title={t("byTeam.emptyTitle")} description={t("byTeam.emptyDescription")} />
      )}
      {hidden && <p className="border-t border-line-soft px-4 py-2.5 text-[11.5px] text-fg-4">{t("byTeam.hidden")}</p>}
    </Card>
  );
}

/** Tickets per brand, once the workspace has a second brand. */
function BrandCard({ rows }: { rows: GroupRow[] }) {
  const { t } = useTranslation("reports");
  return (
    <Card title={t("byBrand.title")} subtitle={t("byBrand.subtitle")} flush>
      {rows.length ? (
        <GroupTable rows={rows} caption={t("byBrand.caption")} head={t("byBrand.brand")} />
      ) : (
        <EmptyState compact lit={0} title={t("byBrand.emptyTitle")} description={t("byBrand.emptyDescription")} />
      )}
    </Card>
  );
}

/** Priority and label mix of the open work, from the loaded open tickets. */
function OpenWorkCards({ filter }: { filter: ReportsQuery }) {
  const { t } = useTranslation("reports");
  useTranslation("indicators");
  const list = useInfiniteQuery(q.tickets(openWork(filter)));
  const tickets = list.data?.pages.flatMap((pg) => pg.data) ?? [];
  const sample = list.hasNextPage ? t("open.sample", { count: tickets.length }) : t("open.subtitle");

  const priorities: BarRow[] = priorityOrder.map((pr) => ({
    id: pr,
    label: priorityLabel(pr),
    text: priorityLabel(pr),
    value: tickets.filter((x) => x.priority === pr).length,
    tone: priorityTone[pr],
    icon: <PriorityIcon priority={pr} />,
  }));

  const tagMap = new Map<string, { name: string; color?: string | null; n: number }>();
  for (const tk of tickets)
    for (const tag of tk.tags) {
      const e = tagMap.get(tag.id) ?? { name: tag.name, color: tag.color, n: 0 };
      e.n++;
      tagMap.set(tag.id, e);
    }
  const tags: BarRow[] = [...tagMap.entries()]
    .sort((a, b) => b[1].n - a[1].n)
    .slice(0, 6)
    .map(([id, e]) => ({ id, label: <TagChip name={e.name} color={e.color} />, text: e.name, value: e.n, tone: isTone(e.color) ? e.color : toneFor(e.name) }));

  return (
    <>
      <Card title={t("priority.title")} subtitle={sample}>
        {list.isLoading ? (
          <BarsSkeleton />
        ) : (
          <BreakdownBars
            rows={priorities}
            unit={(n) => t("unit.tickets", { count: n })}
            empty={<EmptyState compact lit={1} title={t("backlog.emptyTitle")} description={t("backlog.emptyDescription")} />}
          />
        )}
      </Card>
      <Card title={t("tags.title")} subtitle={sample}>
        {list.isLoading ? (
          <BarsSkeleton />
        ) : (
          <BreakdownBars
            rows={tags}
            unit={(n) => t("unit.tickets", { count: n })}
            empty={<EmptyState compact lit={2} title={t("tags.emptyTitle")} description={t("tags.emptyDescription")} />}
          />
        )}
      </Card>
    </>
  );
}

/** Each member's work in the period, from its own report: a member who is not an admin gets their own row. */
function AgentWorkCard({ filter }: { filter: ReportsQuery }) {
  const { t } = useTranslation("reports");
  const report = useQuery({ ...q.reportAgents(filter), placeholderData: keepPreviousData });
  const d = report.data;
  const self = d?.online_time === "self";
  const note = d?.online_time === "all" ? t("work.onlineNote") : self ? t("work.onlineSelf") : null;
  return (
    <Card title={t("work.title")} subtitle={self ? t("work.subtitleSelf") : t("work.subtitle")} flush className={cn("transition-opacity duration-150", report.isFetching && report.isPlaceholderData && "opacity-60")}>
      {report.isLoading ? (
        <TableSkeleton />
      ) : report.error ? (
        <EmptyState
          compact
          lit={3}
          title={t("work.errorTitle")}
          description={t("error.description")}
          action={<Button onClick={() => void report.refetch()}>{t("error.retry")}</Button>}
        />
      ) : !d?.data.length ? (
        <EmptyState compact lit={0} title={t("work.emptyTitle")} description={self ? t("work.emptySelf") : t("work.emptyDescription")} />
      ) : (
        <>
          <AgentWorkTable report={d} />
          {note && <p className="border-t border-line-soft px-4 py-2.5 text-[11.5px] text-fg-4">{note}</p>}
        </>
      )}
    </Card>
  );
}

function TableSkeleton() {
  return (
    <div className="flex flex-col gap-3 px-4 pb-4 pt-2" aria-hidden>
      {[60, 48, 54, 40].map((w) => (
        <div key={w} className="flex items-center gap-3">
          <Skeleton className="size-6 rounded-full" />
          <Skeleton className="h-2.5" style={{ width: `${w / 3}%` }} />
          <Skeleton className="ml-auto h-2.5" style={{ width: `${w}%` }} />
        </div>
      ))}
    </div>
  );
}

/** Medians for the period, then agents ranked by how fast they first reply. */
function ResponseCard({ d }: { d: ReportsOverview }) {
  const { t } = useTranslation("reports");
  const ranked = d.by_agent.filter((a) => a.first_response_p50_minutes !== null).sort((a, b) => a.first_response_p50_minutes! - b.first_response_p50_minutes!);
  const max = Math.max(1, ...ranked.map((a) => a.first_response_p50_minutes!));
  return (
    <Card title={t("response.title")} subtitle={t("response.subtitle")}>
      <div className="grid grid-cols-2 gap-4 border-b border-line-soft pb-4">
        <Figure label={t("kpi.firstResponse")} value={minutes(d.first_response_p50_minutes)} sub={t("kpi.median")} />
        <Figure label={t("kpi.resolution")} value={minutes(d.resolution_p50_minutes)} sub={t("kpi.median")} />
      </div>
      <div className="label-caps mb-3 mt-4">{t("response.fastest")}</div>
      {ranked.length ? (
        <ul className="flex flex-col gap-3">
          {ranked.slice(0, 6).map((a) => (
            <li key={a.user.id} className="flex items-center gap-2.5">
              <Avatar name={a.user.name} src={a.user.avatar_url} size="sm" />
              <span className="w-28 shrink-0 truncate text-[12.5px] text-fg-2">{a.user.name}</span>
              <Bar pct={(a.first_response_p50_minutes! / max) * 100} tone="blue" />
              <span className="mono w-14 shrink-0 text-right text-xs tabular-nums text-fg">{minutes(a.first_response_p50_minutes)}</span>
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-[12.5px] text-fg-4">{t("response.none")}</p>
      )}
    </Card>
  );
}

function BarsSkeleton() {
  return (
    <div className="flex flex-col gap-4" aria-hidden>
      {[70, 45, 30].map((w) => (
        <div key={w} className="flex flex-col gap-2">
          <Skeleton className="h-2.5 w-24" />
          <Skeleton className="h-1.5" style={{ width: `${w}%` }} />
        </div>
      ))}
    </div>
  );
}

function ReportSkeleton() {
  const { t } = useTranslation("reports");
  const card = "rounded-lg border border-line bg-surface p-4";
  return (
    <div className="flex flex-col gap-4 px-4 pt-4 md:px-6 md:pt-5" aria-busy="true" aria-label={t("loading")}>
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4 lg:grid-cols-7">
        {Array.from({ length: 7 }).map((_, i) => (
          <div key={i} className={cn(card, "flex flex-col gap-3")}>
            <Skeleton className="h-2 w-14" />
            <Skeleton className="h-6 w-16" />
            <Skeleton className="h-2 w-10 opacity-60" />
          </div>
        ))}
      </div>
      <div className="grid grid-cols-1 gap-4 xl:grid-cols-3">
        <div className={cn(card, "xl:col-span-2")}>
          <Skeleton className="h-3 w-28" />
          <Skeleton className="mt-6 h-[260px] w-full opacity-50" />
        </div>
        <div className={card}>
          <Skeleton className="h-3 w-24" />
          <div className="mt-6">
            <BarsSkeleton />
          </div>
        </div>
      </div>
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4">
        {[0, 1, 2, 3].map((i) => (
          <div key={i} className={card}>
            <Skeleton className="h-3 w-24" />
            <div className="mt-6">
              <BarsSkeleton />
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

/** A clearly labelled, made-up month so an empty workspace sees what reports will show. */
function sampleOverview(): ReportsOverview {
  const today = new Date();
  const by_day = Array.from({ length: 30 }, (_, i) => {
    const d = subDays(today, 29 - i);
    const weekend = d.getDay() === 0 || d.getDay() === 6;
    const created = Math.round((weekend ? 6 : 18) + 6 * Math.sin(i / 3) + (i % 5));
    return { date: format(d, "yyyy-MM-dd"), created, solved: Math.max(0, created - 2 + (i % 3)) };
  });
  const created = by_day.reduce((a, x) => a + x.created, 0);
  const solved = by_day.reduce((a, x) => a + x.solved, 0);
  return {
    created,
    solved,
    open_now: 24,
    first_response_p50_minutes: 38,
    resolution_p50_minutes: 260,
    csat_avg: 4.6,
    csat_count: 128,
    sla_breached: 6,
    by_day,
    by_channel: { email: Math.round(created * 0.62), chat: Math.round(created * 0.26), portal: Math.round(created * 0.08), api: Math.round(created * 0.04) },
    by_agent: [],
  };
}

function SamplePreview() {
  const { t } = useTranslation("reports");
  const d = sampleOverview();
  return (
    <div className="relative min-h-full overflow-hidden">
      <div className="flex items-center gap-2 px-4 pt-4 md:px-6 md:pt-5">
        <Badge tone="amber" dot>
          {t("empty.sample")}
        </Badge>
        <span className="text-xs text-fg-3">{t("empty.sampleHint")}</span>
      </div>
      <div aria-hidden className="pointer-events-none flex select-none flex-col gap-4 px-4 pt-3 opacity-45 [mask-image:linear-gradient(to_bottom,black_55%,transparent)] md:px-6">
        <Kpis d={d} />
        <div className="grid grid-cols-1 gap-4 xl:grid-cols-3">
          <VolumeCard d={d} className="xl:col-span-2" />
          <Card title={t("channel.title")} subtitle={t("channel.subtitle")}>
            <ChannelBars data={d.by_channel} />
          </Card>
        </div>
      </div>
      <div className="absolute inset-0 flex items-start justify-center pt-[18vh]">
        <div className="pointer-events-auto rounded-xl border border-line bg-surface shadow-float">
          <EmptyState
            compact
            art="reports"
            className="px-8 py-7"
            title={t("empty.title")}
            description={t("empty.description")}
          >
            <Badge tone="amber" dot>
              {t("empty.sample")}
            </Badge>
          </EmptyState>
        </div>
      </div>
    </div>
  );
}
