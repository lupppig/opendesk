// An account's mail history (contracts/collab.md "History"): how far back its mail is in
// opendesk, the import of older mail (scope, changed while it runs, progress, pause, resume, stop) and what the
// workspace's Collab storage allowance means for it ("Storage"). Progress arrives with
// collab.accounts, which refreshes the account; nothing here polls.

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { History, Pause, Play, Square } from "lucide-react";
import { motion, useReducedMotion } from "motion/react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Row, Section } from "@/components/layout";
import { StatePill, type ItemState } from "@/components/settings2/bits";
import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/ui/confirm";
import { Select } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast";
import { api } from "@/lib/api/endpoints";
import { errorMessage } from "@/lib/api/client";
import type { CollabStorage, MailAccount, MailHistory, MailHistoryStatus } from "@/lib/api/types";
import { storeAccount } from "@/lib/collab/hooks";
import { bytes, longDate, monthYear, number, percent, relLong } from "@/lib/format";
import { springs } from "@/lib/motion";
import { atLeast, useRole } from "@/lib/session";
import { cn } from "@/lib/cn";

export type HistoryScope = "1y" | "2y" | "5y" | "all";
export const HISTORY_SCOPES: HistoryScope[] = ["1y", "2y", "5y", "all"];

/** The RFC 3339 start of a scope, null for all mail. */
export function sinceOf(scope: HistoryScope, now = new Date()): string | null {
  if (scope === "all") return null;
  const d = new Date(now);
  d.setUTCFullYear(d.getUTCFullYear() - Number(scope.slice(0, -1)));
  return d.toISOString();
}

/** The scope an import was started with, null for a date none of them gives. */
export function scopeOf(h: MailHistory): HistoryScope | null {
  if (h.since === null) return "all";
  const since = new Date(h.since).getTime();
  const day = 24 * 60 * 60 * 1000;
  return HISTORY_SCOPES.find((s) => s !== "all" && Math.abs(new Date(sinceOf(s, new Date(h.started_at))!).getTime() - since) < 2 * day) ?? null;
}

/** An import that no longer runs: a new one can start. */
export const historyEnded = (h: MailHistory | null) => !h || h.status === "done" || h.status === "canceled" || h.status === "error";

/** How much of the scope an import has read, 0..1; null when the provider did not say. */
export function historyProgress(h: MailHistory): number | null {
  if (!h.total || h.total <= 0) return null;
  return Math.min(1, (h.imported + h.present + h.too_large + h.unreadable) / h.total);
}

const pillState: Record<MailHistoryStatus, ItemState> = {
  running: "pending",
  paused: "paused",
  limited: "paused",
  done: "active",
  canceled: "disabled",
  error: "failing",
};

/** A small pill for an account row while its import is under way, paused or waiting for storage. */
export function HistoryPill({ history }: { history: MailHistory | null }) {
  const { t } = useTranslation("collab");
  if (!history || (history.status !== "running" && history.status !== "paused" && history.status !== "limited")) return null;
  const p = historyProgress(history);
  const label =
    history.status === "running"
      ? p === null
        ? t("history.pill.running")
        : t("history.pill.runningPercent", { percent: percent(p) })
      : t(`history.pill.${history.status}`);
  return (
    <StatePill state={pillState[history.status]} pulse={history.status === "running"}>
      {label}
    </StatePill>
  );
}

function ProgressBar({ value, label }: { value: number | null; label?: string }) {
  const reduce = useReducedMotion();
  return (
    <div
      className="h-1 w-full overflow-hidden rounded-xs bg-hover"
      role="progressbar"
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={value === null ? undefined : Math.round(value * 100)}
      aria-label={label}
    >
      {value === null ? (
        <div className="h-full w-1/3 animate-[pending-bar_1.4s_ease-in-out_infinite] rounded-xs bg-fg-4 motion-reduce:hidden" />
      ) : (
        <motion.div
          className="h-full rounded-xs bg-inverse dark:bg-inverse-fg"
          initial={false}
          animate={{ width: `${value * 100}%` }}
          transition={reduce ? { duration: 0 } : springs.glide}
        />
      )}
    </div>
  );
}

/** "12,340 imported, 210 already here", with the skipped ones when there are any. */
function useCounts() {
  const { t } = useTranslation("collab");
  return (h: MailHistory) =>
    [
      t("history.counts.imported", { n: number(h.imported) }),
      h.present > 0 && t("history.counts.present", { n: number(h.present) }),
      h.too_large > 0 && t("history.counts.tooLarge", { n: number(h.too_large) }),
      h.unreadable > 0 && t("history.counts.unreadable", { n: number(h.unreadable) }),
    ]
      .filter(Boolean)
      .join(", ");
}

export function HistorySection({ account, storage }: { account: MailAccount; storage: CollabStorage | null }) {
  const { t } = useTranslation("collab");
  const qc = useQueryClient();
  const confirm = useConfirm();
  const admin = atLeast(useRole(), "admin");
  const counts = useCounts();
  const [scope, setScope] = useState<HistoryScope>("1y");
  const [spamTrash, setSpamTrash] = useState(false);
  const h = account.history;
  const manage = account.can_manage;
  const provider = t(`history.where.${account.provider}`);
  const name = account.name || account.email;

  const done = (a: MailAccount) => storeAccount(qc, a);
  const start = useMutation({
    mutationFn: (b: { since: string | null; include_spam_trash: boolean; change?: boolean }) =>
      api.collab.accounts.startHistory(account.id, { since: b.since, include_spam_trash: b.include_spam_trash }),
    onSuccess: (a, b) => {
      done(a);
      toast.success(b.change ? t("history.changed") : t("history.started", { name }));
    },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const pause = useMutation({
    mutationFn: (paused: boolean) => api.collab.accounts.pauseHistory(account.id, paused),
    onSuccess: (a, paused) => {
      done(a);
      toast.success(paused ? t("history.paused") : t("history.resumed"));
    },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const stop = () =>
    confirm.show(
      t("history.stopText"),
      async () => {
        done(await api.collab.accounts.stopHistory(account.id));
        toast.success(t("history.stopped"));
      },
      { title: t("history.stopTitle"), confirmLabel: t("history.stop") },
    );

  const allDone = h?.status === "done" && h.since === null;
  const coverage = !account.oldest_message_at
    ? t("history.coverageNone")
    : allDone
      ? t("history.coverageAll", { date: longDate(account.oldest_message_at) })
      : t("history.coverageSince", { date: longDate(account.oldest_message_at), provider });
  const live = h && !historyEnded(h);
  const progress = h ? historyProgress(h) : null;
  const scopeLabel = h ? (h.since ? t("history.scopeOf.since", { date: longDate(h.since) }) : t("history.scopeOf.all")) : "";
  const scopeOptions = HISTORY_SCOPES.map((s) => ({ value: s, label: t(`history.scopes.${s}`) }));
  const liveScope = h ? scopeOf(h) : null;

  return (
    <Section title={t("history.title")} description={t("history.description")}>
      <Row label={t("history.coverage")} description={coverage} />

      {h && (
        <Row
          label={t(`history.status.${h.status}`)}
          description={
            <>
              {scopeLabel}
              {h.requested_by && <> · {t("history.requestedBy", { name: h.requested_by.name, when: relLong(h.started_at) })}</>}
            </>
          }
          stack={!!live}
        >
          {live ? (
            <div className="flex flex-col gap-2">
              {h.status === "running" && <ProgressBar value={progress} label={progress === null ? undefined : t("history.progressLabel", { percent: percent(progress) })} />}
              <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
                <p className="text-[12px] text-fg-2 tabular-nums">
                  {counts(h)}
                  {progress !== null && h.status === "running" && <span className="text-fg-4"> · {percent(progress)}</span>}
                  {h.reached && <span className="text-fg-4"> · {t("history.reached", { date: monthYear(h.reached) })}</span>}
                </p>
                {manage && (
                  <div className="flex items-center gap-1.5">
                    {h.status === "paused" ? (
                      <Button size="sm" icon={<Play />} loading={pause.isPending} onClick={() => pause.mutate(false)}>
                        {t("history.resume")}
                      </Button>
                    ) : h.status === "running" ? (
                      <Button size="sm" icon={<Pause />} loading={pause.isPending} onClick={() => pause.mutate(true)}>
                        {t("history.pause")}
                      </Button>
                    ) : null}
                    <Button size="sm" icon={<Square />} onClick={stop}>
                      {t("history.stop")}
                    </Button>
                  </div>
                )}
              </div>
              {h.status === "limited" && (
                <p className="rounded-md bg-sunken px-3 py-2 text-[12px] leading-[18px] text-fg-2">
                  {t("history.limited", { used: bytes(storage?.used_bytes ?? 0), limit: bytes(storage?.limit_bytes ?? 0) })}
                  {admin && (
                    <>
                      {" "}
                      {t("history.limitedAdmin")}{" "}
                      <Link to="/settings/billing" search={{}} className="font-medium text-fg underline decoration-line-strong underline-offset-2 hover:decoration-fg-3">
                        {t("history.billing")}
                      </Link>
                    </>
                  )}
                </p>
              )}
              {h.last_error && h.status === "running" && <p className="mono break-words text-[11.5px] text-fg-3">{t("history.retrying", { error: h.last_error })}</p>}
            </div>
          ) : h.status === "error" ? (
            manage ? (
              <Button size="sm" loading={start.isPending} onClick={() => start.mutate({ since: h.since, include_spam_trash: h.include_spam_trash })}>
                {t("history.retry")}
              </Button>
            ) : null
          ) : (
            <span className="text-[12px] text-fg-3 tabular-nums">{counts(h)}</span>
          )}
        </Row>
      )}

      {/* A running import's scope can change: it starts again with it, and what it imported stays. */}
      {h && live && manage && (
        <Row label={t("history.scope")} description={t("history.changeHint")} htmlFor="history-scope-live">
          <Select<HistoryScope>
            id="history-scope-live"
            className="w-[160px]"
            size="sm"
            value={liveScope ?? undefined}
            placeholder={scopeLabel}
            disabled={start.isPending}
            onChange={(s) => s !== liveScope && start.mutate({ since: sinceOf(s), include_spam_trash: h.include_spam_trash, change: true })}
            options={scopeOptions}
          />
        </Row>
      )}

      {h && h.status === "error" && h.last_error && (
        <Row label={t("detail.lastError")} stack>
          <span className="mono break-words text-[11.5px] text-fg-2">{h.last_error}</span>
        </Row>
      )}

      {h && h.status === "done" && (h.too_large > 0 || h.unreadable > 0 || h.finished_at) && (
        <Row
          label={t("history.doneSummary", { when: relLong(h.finished_at ?? h.started_at) })}
          description={
            [
              h.too_large > 0 && t("history.tooLarge", { count: h.too_large, n: number(h.too_large), provider }),
              h.unreadable > 0 && t("history.unreadable", { count: h.unreadable, n: number(h.unreadable) }),
            ]
              .filter(Boolean)
              .join(" ") || undefined
          }
        />
      )}

      {manage && historyEnded(h) && !allDone && (
        <>
          <Row label={h ? t("history.startAgain") : t("history.scope")} description={t("history.scopeHint")} htmlFor="history-scope">
            <Select<HistoryScope>
              id="history-scope"
              className="w-[160px]"
              size="sm"
              value={scope}
              onChange={setScope}
              options={scopeOptions}
            />
            <Button size="sm" variant="primary" icon={<History />} loading={start.isPending} onClick={() => start.mutate({ since: sinceOf(scope), include_spam_trash: spamTrash })}>
              {t("history.start")}
            </Button>
          </Row>
          <Row label={t("history.spamTrash")} description={t("history.spamTrashHint")} htmlFor="history-spam">
            <Switch id="history-spam" checked={spamTrash} onCheckedChange={setSpamTrash} />
          </Row>
        </>
      )}

      {(account.folders_left_out > 0 || (storage?.limit_bytes ?? 0) > 0) && (
        <div className={cn("flex flex-col gap-1 border-t border-line-soft py-3 text-[12px] leading-[18px] text-fg-3", "first:border-0")}>
          {account.folders_left_out > 0 && <p>{t("history.foldersLeftOut", { count: account.folders_left_out, n: number(account.folders_left_out) })}</p>}
          {(storage?.limit_bytes ?? 0) > 0 && <p>{t("history.filesHint", { provider })}</p>}
        </div>
      )}
    </Section>
  );
}
