import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowUpRight, RefreshCw } from "lucide-react";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { ErrorState } from "@/components/ErrorState";
import { Banner, KV, PageBody, PageColumn, PageHeader, Section } from "@/components/layout";
import { CheckPill } from "@/components/pills";
import { Table, TD, TH, THead, TR } from "@/components/table";
import { Time } from "@/components/Time";
import { Badge, Dot } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/copy-button";
import { Skeleton } from "@/components/ui/skeleton";
import { toast } from "@/components/ui/toast";
import { i18n } from "@/i18n";
import { idleMs } from "@/lib/api/client";
import { api } from "@/lib/api/endpoints";
import type { AdminHealth, AdminUpdates, AutoUpdateResult } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { duration, number } from "@/lib/format";
import { q, qk } from "@/lib/queries";

/** Refresh every 15 s while the page is visible and the operator is around (an idle panel lets its session lapse). */
const EVERY = 15_000;
const IDLE = 15 * 60_000;

export default function Health() {
  const { t } = useTranslation(["health", "common", "jobs"]);
  const health = useQuery({ ...q.health(), refetchInterval: () => (idleMs() < IDLE ? EVERY : false), refetchIntervalInBackground: false });
  const h = health.data;

  return (
    <>
      <PageHeader
        title={t("title")}
        subtitle={
          h ? (
            <span className="inline-flex items-center gap-1.5">
              <Dot tone={health.isError ? "danger" : "ok"} pulse={!health.isError} /> {t("updated")} <Time iso={new Date(health.dataUpdatedAt).toISOString()} />
            </span>
          ) : undefined
        }
        actions={
          <Button variant="ghost" icon={<RefreshCw className={cn(health.isFetching && "animate-spin motion-reduce:animate-none")} />} onClick={() => void health.refetch()}>
            {t("jobs:refresh")}
          </Button>
        }
      />
      <PageBody>
        {health.isPending ? (
          <PageColumn>
            <Skeleton className="h-40 rounded-lg" />
            <div className="grid gap-5 md:grid-cols-2">
              <Skeleton className="h-44 rounded-lg" />
              <Skeleton className="h-44 rounded-lg" />
            </div>
          </PageColumn>
        ) : !h ? (
          <ErrorState error={health.error} onRetry={() => void health.refetch()} />
        ) : (
          <HealthView h={h} stale={health.isError} />
        )}
      </PageBody>
    </>
  );
}

const ok = (v: boolean) => (v ? <Badge tone="ok">{i18n.t("common:check.ok")}</Badge> : <Badge tone="danger">{i18n.t("health:down")}</Badge>);
const on = (v: boolean) => (v ? <Badge tone="green">{i18n.t("health:on")}</Badge> : <Badge tone="gray">{i18n.t("health:off")}</Badge>);
const n = (v: number) => <span className="mono tabular-nums">{number(v)}</span>;
const warnN = (v: number, warnAt = 1): ReactNode => <span className={cn("mono tabular-nums", v >= warnAt && "font-medium text-danger-fg")}>{number(v)}</span>;
const text = (v: string) => (v ? <span className="mono text-[12px]">{v}</span> : <span className="text-fg-4">{i18n.t("common:none")}</span>);

function HealthView({ h, stale }: { h: AdminHealth; stale: boolean }) {
  const { t } = useTranslation(["health", "common", "jobs"]);
  const order = { fail: 0, warn: 1, ok: 2 } as const;
  const checks = [...h.checks].sort((a, b) => order[a.status] - order[b.status] || a.name.localeCompare(b.name));
  const failing = checks.filter((c) => c.status === "fail").length;
  const warning = checks.filter((c) => c.status === "warn").length;
  return (
    <PageColumn>
      {stale && <p className="text-[12px] text-danger-fg">{t("stale")}</p>}
      {h.updates.available && h.updates.latest && <UpdateBanner u={h.updates} />}
      <Section
        title={t("checks.title")}
        count={checks.length}
        flush
        actions={
          <span className="text-[11.5px] text-fg-3">
            {failing ? t("checks.failing", { count: failing }) : warning ? t("checks.warning", { count: warning }) : t("checks.allPassing")}
          </span>
        }
      >
        <ul className="divide-y divide-line-soft">
          {checks.map((c) => (
            <li key={c.name} className="flex items-start gap-3 px-4 py-2.5">
              <span className="w-[72px] shrink-0 pt-px">
                <CheckPill status={c.status} />
              </span>
              <span className="min-w-0 flex-1">
                <span className="block text-[12.5px] font-medium text-fg">{c.name}</span>
                {c.detail && <span className="block text-[12px] leading-[18px] text-fg-3">{c.detail}</span>}
              </span>
            </li>
          ))}
        </ul>
      </Section>

      <div className="grid items-start gap-5 md:grid-cols-2">
        <Section title={t("instance.title")}>
          <KV
            items={[
              [t("instance.version"), text(h.version)],
              [t("instance.commit"), text(h.commit)],
              [t("instance.role"), text(h.role)],
              [t("instance.started"), <Time iso={h.started_at} />],
            ]}
          />
        </Section>
        <UpdatesSection u={h.updates} />
        <Section title={t("database.title")}>
          <KV
            items={[
              [t("database.status"), ok(h.database.ok)],
              [t("database.latency"), <span className="mono tabular-nums">{number(h.database.latency_ms, { maximumFractionDigits: 1 })} ms</span>],
              [t("database.schema"), <span className="flex items-center gap-2">{n(h.database.schema_version)}{h.database.dirty && <Badge tone="danger">{t("database.dirty")}</Badge>}</span>],
              [
                t("database.pool"),
                <span className="mono tabular-nums">
                  {t("database.poolLine", { inUse: number(h.database.pool.in_use), idle: number(h.database.pool.idle), total: number(h.database.pool.total), max: number(h.database.pool.max) })}
                </span>,
              ],
            ]}
          />
        </Section>
        <Section title={t("events.title")}>
          <KV
            items={[
              ["NATS", <span className="flex items-center gap-2">{ok(h.nats.ok)}<span className="text-[11.5px] text-fg-3">{h.nats.embedded ? t("events.embedded") : t("events.cluster")}</span></span>],
              [t("events.unpublished"), warnN(h.relay.unpublished, 100)],
              [t("events.oldestUnpublished"), <span className="mono tabular-nums">{h.relay.oldest_unpublished_s === null ? t("common:none") : duration(h.relay.oldest_unpublished_s)}</span>],
            ]}
          />
        </Section>
        <Section title={t("mail.title")}>
          <KV
            items={[
              [t("mail.driver"), text(h.mail.driver)],
              [t("mail.inboundFailed"), warnN(h.mail.inbound_failed_24h)],
              [t("mail.inboundPending"), n(h.mail.inbound_pending)],
              [t("mail.mailboxesInError"), warnN(h.mail.mailboxes_in_error)],
              [t("mail.webhooksFailing"), warnN(h.webhooks.failing_24h)],
            ]}
          />
        </Section>
      </div>

      <Section title={t("jobs.title")} flush>
        <div className="grid grid-cols-2 gap-px border-b border-line-soft bg-line-soft sm:grid-cols-3 lg:grid-cols-6">
          {(
            [
              [t("jobs.available"), h.jobs.available],
              [t("jobs.running"), h.jobs.running],
              [t("jobs.retrying"), h.jobs.retryable],
              [t("jobs.scheduled"), h.jobs.scheduled],
              [t("jobs.discarded"), h.jobs.discarded_24h],
            ] as const
          ).map(([k, v]) => (
            <div key={k} className="bg-surface px-4 py-3">
              <div className="label-caps">{k}</div>
              <div className="mono mt-1.5 text-[15px] font-medium tabular-nums">{number(v)}</div>
            </div>
          ))}
          <div className="bg-surface px-4 py-3">
            <div className="label-caps">{t("jobs.oldestWaiting")}</div>
            <div className="mono mt-1.5 text-[15px] font-medium tabular-nums">{h.jobs.oldest_available_s === null ? t("common:none") : duration(h.jobs.oldest_available_s)}</div>
          </div>
        </div>
        {h.jobs.by_queue.length > 0 && (
          <Table>
            <THead>
              <TH>{t("jobs:col.queue")}</TH>
              <TH align="right">{t("jobs.available")}</TH>
              <TH align="right">{t("jobs.running")}</TH>
              <TH align="right">{t("jobs.retrying")}</TH>
            </THead>
            <tbody>
              {h.jobs.by_queue.map((r) => (
                <TR key={r.queue}>
                  <TD mono>{r.queue}</TD>
                  <TD align="right" mono>
                    {number(r.available)}
                  </TD>
                  <TD align="right" mono>
                    {number(r.running)}
                  </TD>
                  <TD align="right" mono className={r.retryable ? "text-danger-fg" : undefined}>
                    {number(r.retryable)}
                  </TD>
                </TR>
              ))}
            </tbody>
          </Table>
        )}
      </Section>

      <Section title={t("config.title")}>
        <KV
          className="md:grid-cols-[minmax(110px,max-content)_1fr_minmax(110px,max-content)_1fr]"
          items={[
            [t("config.billing"), on(h.config.billing)],
            [t("config.signup"), on(h.config.signup_enabled)],
            [t("config.aiProvider"), text(h.config.ai_provider)],
            [t("config.decisions"), text(h.config.decisions_provider)],
            [t("config.embeddings"), text(h.config.embeddings)],
            [t("config.storage"), text(h.config.storage)],
            [t("config.imap"), on(h.config.mailbox_imap)],
            [t("config.systemSend"), on(h.config.mailbox_system_send)],
            [t("config.push"), text(h.config.push)],
            [t("config.helpDomain"), text(h.config.help_domain)],
          ]}
        />
      </Section>
    </PageColumn>
  );
}

const command = (version: string) => `opendesk-ctl upgrade ${version}`;

function UpdateBanner({ u }: { u: AdminUpdates }) {
  const { t } = useTranslation(["health", "common", "jobs"]);
  const latest = u.latest!;
  const auto = u.auto.enabled && !u.major;
  return (
    <Banner
      tone={auto ? "blue" : "amber"}
      title={t("update.available", { version: latest.version })}
      action={
        <a href={latest.url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-[12px] font-medium text-fg hover:underline">
          {t("update.releaseNotes")} <ArrowUpRight className="size-3.5" />
        </a>
      }
    >
      {auto ? (
        <>{t("update.auto", { current: u.current })}</>
      ) : (
        <>
          {u.major ? t("update.major") : t("update.runs", { current: u.current })} {t("update.how")}
          <span className="mt-1.5 flex w-fit items-center gap-1.5 rounded-md bg-surface px-2 py-1 shadow-[inset_0_0_0_1px_var(--line)]">
            <code className="mono text-[12px] text-fg">{command(latest.version)}</code>
            <CopyButton value={command(latest.version)} label={t("update.copyCommand")} />
          </span>
        </>
      )}
    </Banner>
  );
}


function UpdatesSection({ u }: { u: AdminUpdates }) {
  const { t } = useTranslation(["health", "common", "jobs"]);
  const result = (r: AutoUpdateResult) => t(`updates.result.${r}`);
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);
  const checkNow = async () => {
    setBusy(true);
    try {
      const r = await api.checkUpdates();
      toast.success(r.available && r.latest ? t("updates.versionAvailable", { version: r.latest.version }) : r.error ? t("updates.unreachable") : t("updates.newest"));
      void qc.invalidateQueries({ queryKey: qk.health });
    } catch (e) {
      toast.error(e);
    } finally {
      setBusy(false);
    }
  };
  const auto = u.auto;
  return (
    <Section
      title={t("updates.title")}
      actions={
        u.check ? (
          <Button size="xs" variant="ghost" icon={<RefreshCw className={cn(busy && "animate-spin motion-reduce:animate-none")} />} disabled={busy} onClick={() => void checkNow()}>
            {t("updates.checkNow")}
          </Button>
        ) : undefined
      }
    >
      <KV
        items={[
          [
            t("updates.running"),
            <span className="flex flex-wrap items-center gap-x-2">
              {text(u.current)}
              {u.previous_version && u.running_since && (
                <span className="text-[11.5px] text-fg-3">
                  {t("updates.from", { version: u.previous_version })} <Time iso={u.running_since} />
                </span>
              )}
            </span>,
          ],
          [
            t("updates.newestRelease"),
            !u.check ? (
              <span className="text-fg-4">{t("updates.notChecked")}</span>
            ) : u.latest ? (
              <span className="flex flex-wrap items-center gap-x-2">
                <a href={u.latest.url} target="_blank" rel="noreferrer" className="mono text-[12px] hover:underline">
                  {u.latest.version}
                </a>
                {u.available ? <Badge tone={u.major ? "warn" : "blue"}>{u.major ? t("updates.newMajor") : t("updates.availableBadge")}</Badge> : <Badge tone="ok">{t("updates.current")}</Badge>}
                {u.latest.published_at && (
                  <span className="text-[11.5px] text-fg-3">
                    <Time iso={u.latest.published_at} />
                  </span>
                )}
              </span>
            ) : (
              <span className="text-fg-4">{t("updates.noneFound")}</span>
            ),
          ],
          [
            t("updates.lastChecked"),
            !u.check ? (
              <span className="text-fg-3">{u.current && /^v\d/.test(u.current) ? t("updates.checkOff") : t("updates.notRelease")}</span>
            ) : (
              <span className="flex flex-col">
                {u.checked_at ? <Time iso={u.checked_at} /> : <span className="text-fg-4">{t("jobs:details.notYet")}</span>}
                {u.error && <span className="text-[11.5px] text-danger-fg">{u.error}</span>}
              </span>
            ),
          ],
          [
            t("updates.automatic"),
            !auto.reported ? (
              <span className="text-fg-3">{t("updates.notSetUp")}</span>
            ) : (
              <span className="flex flex-col gap-0.5">
                <span className="flex items-center gap-2">
                  {on(auto.enabled)}
                  {auto.last_run_at && (
                    <span className={cn("text-[11.5px]", auto.last_result === "failed" ? "text-danger-fg" : "text-fg-3")}>
                      {result(auto.last_result)}, <Time iso={auto.last_run_at} />
                    </span>
                  )}
                </span>
                {auto.last_detail && <span className="text-[11.5px] leading-4 text-fg-3">{auto.last_detail}</span>}
              </span>
            ),
          ],
        ]}
      />
    </Section>
  );
}
