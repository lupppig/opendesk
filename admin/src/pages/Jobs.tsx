import { keepPreviousData, useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { ChevronRight, RefreshCw, RotateCcw, X } from "lucide-react";
import { Fragment, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { ActionButton } from "@/components/ActionButton";
import { ErrorState } from "@/components/ErrorState";
import { FilterChips } from "@/components/FilterChips";
import { KV, PageBody, PageHeader } from "@/components/layout";
import { JobStatePill } from "@/components/pills";
import { LoadMore, Table, TableSkeleton, TD, TH, THead } from "@/components/table";
import { Time } from "@/components/Time";
import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/ui/confirm";
import { CopyButton } from "@/components/ui/copy-button";
import { EmptyState, GhostList } from "@/components/ui/empty";
import { Select } from "@/components/ui/select";
import { toast } from "@/components/ui/toast";
import { api } from "@/lib/api/endpoints";
import { JOB_STATES, type AdminJob, type JobState } from "@/lib/api/types";
import { inSentence } from "@/i18n";
import { cn } from "@/lib/cn";
import { humanize, number } from "@/lib/format";
import { q, qk } from "@/lib/queries";

const SHOWN_STATES: JobState[] = ["retryable", "discarded", "available", "running", "scheduled", "cancelled", "completed"];
const RETRYABLE = new Set<JobState>(["retryable", "discarded", "cancelled"]);
const CANCELLABLE = new Set<JobState>(["available", "scheduled", "retryable", "running", "pending"]);
const ALL = "__all";

export default function Jobs() {
  const { t } = useTranslation(["jobs", "common", "shell"]);
  const search = useSearch({ from: "/shell/jobs" });
  const navigate = useNavigate({ from: "/jobs" });
  const qc = useQueryClient();
  const state = (JOB_STATES as readonly string[]).includes(search.state ?? "") ? (search.state as JobState) : "retryable";
  const stats = useQuery(q.jobStats());
  const list = useInfiniteQuery({ ...q.jobs({ state, kind: search.kind }), placeholderData: keepPreviousData });
  const rows = list.data?.pages.flatMap((p) => p.data) ?? [];
  const [open, setOpen] = useState<Set<number>>(new Set());

  const counts = stats.data?.states ?? {};
  const states = [...SHOWN_STATES, ...(counts.pending ? (["pending"] as JobState[]) : [])];
  const kinds = [...(stats.data?.kinds ?? [])].sort((a, b) => a.kind.localeCompare(b.kind));
  const stateLabel = (s: JobState) => t(`common:jobState.${s}`, { defaultValue: humanize(s) });
  const stateInSentence = inSentence(stateLabel(state));

  const refresh = () => {
    void qc.invalidateQueries({ queryKey: qk.jobs });
  };

  const toggle = (id: number) =>
    setOpen((s) => {
      const next = new Set(s);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  return (
    <>
      <PageHeader
        title={t("title")}
        subtitle={t("subtitle")}
        actions={
          <Button variant="ghost" icon={<RefreshCw className={cn(list.isFetching && !list.isFetchingNextPage && "animate-spin motion-reduce:animate-none")} />} onClick={refresh}>
            {t("refresh")}
          </Button>
        }
      />
      <div className="flex min-h-11 shrink-0 flex-wrap items-center gap-3 border-b border-line-soft px-4 py-2 md:px-6">
        <FilterChips
          label={t("stateLabel")}
          value={state}
          onChange={(s) => void navigate({ search: (p) => ({ ...p, state: s === "retryable" ? undefined : s }), replace: true })}
          options={states.map((s) => ({ value: s, label: stateLabel(s), count: counts[s] }))}
        />
        <div className="ml-auto w-[240px]">
          <Select
            size="sm"
            aria-label={t("kindLabel")}
            value={search.kind ?? ALL}
            onChange={(k) => void navigate({ search: (p) => ({ ...p, kind: k === ALL ? undefined : k }), replace: true })}
            options={[{ value: ALL, label: t("allKinds") }, ...kinds.map((k) => ({ value: k.kind, label: k.kind, hint: k.retryable ? t("retrying", { count: k.retryable, n: number(k.retryable) }) : undefined }))]}
          />
        </div>
      </div>
      <PageBody>
        {list.isPending ? (
          <TableSkeleton cols={6} />
        ) : list.isError && !rows.length ? (
          <ErrorState error={list.error} onRetry={() => void list.refetch()} />
        ) : !rows.length ? (
          <EmptyState
            art="jobs"
            ghost={<GhostList />}
            title={search.kind ? t("empty.titleKind", { state: stateInSentence, kind: search.kind }) : t("empty.title", { state: stateInSentence })}
            description={state === "retryable" ? t("empty.retryable") : state === "discarded" ? t("empty.discarded") : t("empty.other")}
            tips={[{ kbd: "g h", label: t("shell:nav.health") }]}
          />
        ) : (
          <>
            <Table className={list.isPlaceholderData ? "opacity-60 transition-opacity" : undefined}>
              <THead>
                <TH className="w-8" />
                <TH>{t("col.job")}</TH>
                <TH>{t("col.queue")}</TH>
                <TH>{t("col.state")}</TH>
                <TH align="right">{t("col.attempt")}</TH>
                <TH>{t("col.lastError")}</TH>
                <TH>{t("col.scheduled")}</TH>
                <TH />
              </THead>
              <tbody>
                {rows.map((j) => (
                  <Fragment key={j.id}>
                    <tr className={cn("border-b border-line-soft", open.has(j.id) && "bg-sunken")}>
                      <TD className="w-8 !pr-0">
                        <button
                          type="button"
                          aria-expanded={open.has(j.id)}
                          aria-label={open.has(j.id) ? t("hideDetails") : t("showDetails")}
                          onClick={() => toggle(j.id)}
                          className="flex size-6 items-center justify-center rounded-md text-fg-3 hover:bg-hover hover:text-fg"
                        >
                          <ChevronRight className={cn("size-3.5 transition-transform duration-150 motion-reduce:transition-none", open.has(j.id) && "rotate-90")} />
                        </button>
                      </TD>
                      <TD>
                        <span className="mono block text-[12px] font-medium text-fg">{j.kind}</span>
                        <span className="mono block text-[11px] text-fg-4">#{j.id}</span>
                      </TD>
                      <TD mono className="text-[11.5px] text-fg-2">
                        {j.queue}
                      </TD>
                      <TD>
                        <JobStatePill state={j.state} />
                      </TD>
                      <TD align="right" mono className="text-[11.5px]">
                        {j.attempt} / {j.max_attempts}
                      </TD>
                      <TD className="max-w-[360px]">
                        {j.last_error ? (
                          <span className="mono block truncate text-[11.5px] text-danger-fg" title={j.last_error}>
                            {j.last_error}
                          </span>
                        ) : (
                          <span className="text-fg-4">–</span>
                        )}
                      </TD>
                      <TD className="text-fg-3">
                        <Time iso={j.scheduled_at} />
                      </TD>
                      <TD align="right">
                        <JobActions job={j} />
                      </TD>
                    </tr>
                    {open.has(j.id) && (
                      <tr className="border-b border-line-soft bg-sunken">
                        <td colSpan={8} className="px-4 pb-4 pt-1 md:px-6">
                          <JobDetails job={j} />
                        </td>
                      </tr>
                    )}
                  </Fragment>
                ))}
              </tbody>
            </Table>
            <LoadMore shown={rows.length} hasMore={!!list.hasNextPage} loading={list.isFetchingNextPage} onLoad={() => void list.fetchNextPage()} />
          </>
        )}
      </PageBody>
    </>
  );
}

function JobActions({ job }: { job: AdminJob }) {
  const { t } = useTranslation(["jobs", "common", "shell"]);
  const qc = useQueryClient();
  const confirm = useConfirm();
  const [busy, setBusy] = useState(false);
  const done = (msg: string) => {
    toast.success(msg);
    void qc.invalidateQueries({ queryKey: qk.jobs });
  };
  const retry = async () => {
    setBusy(true);
    try {
      await api.jobs.retry(job.id);
      done(t("toast.retried", { id: job.id }));
    } catch (e) {
      toast.error(e);
    } finally {
      setBusy(false);
    }
  };
  const cancel = () =>
    confirm.show(
      <>
        <Trans t={t} i18nKey="cancel.text" values={{ kind: job.kind, id: job.id }} components={{ k: <span className="mono text-fg" /> }} />
      </>,
      async () => {
        await api.jobs.cancel(job.id);
        done(t("toast.cancelled", { id: job.id }));
      },
      { title: t("cancel.title"), confirmLabel: t("cancel.confirm"), danger: true },
    );
  return (
    <div className="flex items-center justify-end gap-1">
      {RETRYABLE.has(job.state) && (
        <ActionButton perm="jobs.manage" size="xs" variant="secondary" icon={<RotateCcw />} loading={busy} onClick={() => void retry()}>
          {t("retry")}
        </ActionButton>
      )}
      {CANCELLABLE.has(job.state) && (
        <ActionButton perm="jobs.manage" size="xs" variant="ghost" icon={<X />} onClick={cancel}>
          {t("cancel.button")}
        </ActionButton>
      )}
    </div>
  );
}

function JobDetails({ job }: { job: AdminJob }) {
  const { t } = useTranslation(["jobs", "common", "shell"]);
  const args = JSON.stringify(job.args ?? {}, null, 2);
  return (
    <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
      <div className="flex min-w-0 flex-col gap-1.5">
        <div className="flex items-center justify-between">
          <span className="label-caps">{t("details.args")}</span>
          <CopyButton value={args} label={t("details.copyArgs")} />
        </div>
        <pre className="mono max-h-[280px] overflow-auto rounded-md border border-line bg-surface px-3 py-2 text-[11.5px] leading-[18px] text-fg">{args}</pre>
      </div>
      <div className="flex min-w-0 flex-col gap-3">
        {job.last_error && (
          <div className="flex min-w-0 flex-col gap-1.5">
            <div className="flex items-center justify-between">
              <span className="label-caps">
                {job.errors > 1 ? t("details.lastErrorOf", { count: job.errors, n: number(job.errors) }) : t("col.lastError")}
              </span>
              <CopyButton value={job.last_error} label={t("details.copyError")} />
            </div>
            <pre className="mono max-h-[200px] overflow-auto whitespace-pre-wrap break-words rounded-md border border-tone-red-line bg-tone-red-soft px-3 py-2 text-[11.5px] leading-[18px] text-fg">
              {job.last_error}
            </pre>
          </div>
        )}
        <KV
          items={[
            [t("details.created"), <Time iso={job.created_at} />],
            [t("col.scheduled"), <Time iso={job.scheduled_at} />],
            [t("details.lastAttempt"), <Time iso={job.attempted_at} empty={t("details.notYet")} />],
            [t("details.finished"), <Time iso={job.finalized_at} empty={t("details.notYet")} />],
          ]}
        />
      </div>
    </div>
  );
}
