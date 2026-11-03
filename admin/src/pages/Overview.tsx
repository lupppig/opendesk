import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ArrowRight } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { BarChart } from "@/components/BarChart";
import { ErrorState } from "@/components/ErrorState";
import { PageBody, PageColumn, PageHeader, Section, StatTile } from "@/components/layout";
import { CheckPill } from "@/components/pills";
import { Skeleton } from "@/components/ui/skeleton";
import { Segmented } from "@/components/ui/tabs";
import type { AdminHealth, AdminSummary } from "@/lib/api/types";
import { dayLabel, number, percent, usd } from "@/lib/format";
import { q } from "@/lib/queries";
import type { Tone } from "@/lib/tones";

type Series = "signups" | "workspaces" | "ai";
/** Each series' label is `series.<key>` in the overview catalog. */
const SERIES: Record<Series, { hue: Tone; value: (d: AdminSummary["daily"][number]) => number; format: (v: number) => string }> = {
  signups: { hue: "blue", value: (d) => d.signups, format: (v) => number(v) },
  workspaces: { hue: "teal", value: (d) => d.workspaces, format: (v) => number(v) },
  ai: { hue: "violet", value: (d) => d.ai_charged_usd, format: (v) => usd(v) },
};

export default function Overview() {
  const { t } = useTranslation("overview");
  const summary = useQuery(q.summary());
  const health = useQuery(q.health());
  const [series, setSeries] = useState<Series>("signups");

  return (
    <>
      <PageHeader title={t("title")} subtitle={t("subtitle")} />
      <PageBody>
        <PageColumn>
          <HealthStrip health={health.data} loading={health.isPending} error={health.error} />
          {summary.isPending ? (
            <TilesSkeleton />
          ) : summary.isError ? (
            <ErrorState error={summary.error} onRetry={() => void summary.refetch()} />
          ) : (
            <>
              <Tiles s={summary.data} />
              <Section
                title={t("last30")}
                actions={
                  <Segmented
                    size="xs"
                    value={series}
                    onChange={setSeries}
                    options={(Object.keys(SERIES) as Series[]).map((k) => ({ value: k, label: t(`series.${k}`) }))}
                  />
                }
              >
                <BarChart
                  label={t(`series.${series}`)}
                  hue={SERIES[series].hue}
                  format={SERIES[series].format}
                  data={summary.data.daily.map((d) => ({ key: d.date, label: dayLabel(d.date), value: SERIES[series].value(d) }))}
                />
              </Section>
            </>
          )}
        </PageColumn>
      </PageBody>
    </>
  );
}

function Tiles({ s }: { s: AdminSummary }) {
  const { t } = useTranslation("overview");
  const margin = s.ai.month_charged_usd - s.ai.month_provider_cost_usd;
  return (
    <div className="flex flex-col gap-3">
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <StatTile label={t("tiles.users")} value={number(s.users.total)} sub={t("tiles.growth", { week: number(s.users.new_7d), month: number(s.users.new_30d) })} />
        <StatTile label={t("tiles.activeUsers")} value={number(s.users.active_7d)} sub={s.users.total ? t("tiles.ofAllUsers", { percent: percent(s.users.active_7d / s.users.total) }) : t("tiles.seenInApp")} />
        <StatTile label={t("tiles.workspaces")} value={number(s.workspaces.total)} sub={t("tiles.growth", { week: number(s.workspaces.new_7d), month: number(s.workspaces.new_30d) })} />
        <StatTile
          label={t("tiles.activeWorkspaces")}
          value={number(s.workspaces.active_7d)}
          sub={s.workspaces.suspended ? t("tiles.suspended", { count: s.workspaces.suspended, n: number(s.workspaces.suspended) }) : t("tiles.noneSuspended")}
        />
      </div>
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        {s.billing.enabled && (
          <>
            <StatTile label={t("tiles.paidWorkspaces")} value={number(s.billing.paid_workspaces)} sub={s.billing.past_due ? t("tiles.pastDue", { count: s.billing.past_due, n: number(s.billing.past_due) }) : t("tiles.nonePastDue")} />
            <StatTile label={t("tiles.billedSeats")} value={number(s.billing.billed_seats)} />
          </>
        )}
        <StatTile label={t("tiles.aiCharged")} value={usd(s.ai.month_charged_usd)} sub={t("tiles.calls", { count: s.ai.month_calls, n: number(s.ai.month_calls) })} />
        <StatTile
          label={t("tiles.aiCost")}
          value={usd(s.ai.month_provider_cost_usd)}
          sub={s.billing.enabled ? (margin >= 0 ? t("tiles.aboveCost", { amount: usd(margin) }) : t("tiles.belowCost", { amount: usd(margin) })) : t("tiles.providerBilled")}
        />
      </div>
    </div>
  );
}

function TilesSkeleton() {
  const { t } = useTranslation();
  return (
    <div className="grid grid-cols-2 gap-3 lg:grid-cols-4" aria-busy="true" aria-label={t("loading")}>
      {Array.from({ length: 8 }).map((_, i) => (
        <div key={i} className="rounded-lg border border-line px-4 py-3.5">
          <Skeleton className="h-2 w-20" />
          <Skeleton className="mt-3 h-5 w-16" />
          <Skeleton className="mt-2.5 h-2 w-28" />
        </div>
      ))}
    </div>
  );
}

function HealthStrip({ health, loading, error }: { health: AdminHealth | undefined; loading: boolean; error: unknown }) {
  const { t } = useTranslation("overview");
  const checks = health?.checks ?? [];
  const failing = checks.filter((c) => c.status !== "ok");
  return (
    <Link
      to="/health"
      className="group flex min-h-11 flex-wrap items-center gap-x-3 gap-y-1.5 rounded-lg border border-line bg-surface px-4 py-2 transition-colors hover:border-line-strong hover:bg-sunken"
    >
      <span className="text-[12.5px] font-medium text-fg">{t("health.title")}</span>
      {loading ? (
        <Skeleton className="h-3 w-48" />
      ) : error ? (
        <span className="text-[12px] text-danger-fg">{t("health.error")}</span>
      ) : failing.length === 0 ? (
        <span className="flex items-center gap-2 text-[12px] text-fg-3">
          <CheckPill status="ok" /> {t("health.allPass", { count: checks.length })}
        </span>
      ) : (
        failing.slice(0, 5).map((c) => (
          <span key={c.name} className="flex items-center gap-1.5 text-[12px] text-fg-2">
            <CheckPill status={c.status} />
            {c.name}
          </span>
        ))
      )}
      <ArrowRight className="ml-auto size-3.5 text-fg-4 transition-transform group-hover:translate-x-0.5 motion-reduce:transition-none" />
    </Link>
  );
}
