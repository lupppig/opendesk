// One import as it runs and after: progress from its import.updated events, the time
// left, a rate limit it is waiting out, what it could not bring, and what can be done next
// (pause, resume with new credentials, bring over what changed, undo).

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { AlertTriangle, ArrowUpRight, CheckCircle2, ChevronDown, Hourglass, KeyRound, MoreHorizontal, Pause, Play, Lock, RefreshCw, Undo2, X } from "lucide-react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/ui/confirm";
import { Menu, MenuContent, MenuItem, MenuSeparator, MenuTrigger } from "@/components/ui/menu";
import { toast } from "@/components/ui/toast";
import { StatePill } from "@/components/settings2/bits";
import { api } from "@/lib/api/endpoints";
import { errorMessage } from "@/lib/api/client";
import type { Import } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { number, relLong, shortDate } from "@/lib/format";
import { dur, ease } from "@/lib/motion";
import { qk, upsertInList } from "@/lib/queries";
import { CredentialsForm, credentialErrors, credentialsReady } from "./Connect";
import { SourceMark, defaultCredentials, sourceName, statusState } from "./sources";

/** Ticks every second while `on`, for countdowns and the time left. */
function useNow(on: boolean) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!on) return;
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [on]);
  return now;
}

function doneOf(x: Import) {
  if (x.phase === "threading") return x.counts.threaded;
  return x.counts.tickets + x.counts.updated + x.counts.articles + x.counts.skipped + x.counts.failed;
}

/** Share done, or null when the source gave no total (or a date or an update narrows it). */
function shareOf(x: Import): number | null {
  if (x.status === "completed") return 1;
  if (x.phase === "threading") return x.counts.thread_total > 0 ? Math.min(0.99, x.counts.threaded / x.counts.thread_total) : null;
  if (x.run > 1 || x.options.since) return null;
  const total = (x.totals.tickets ?? NaN) * (x.options.tickets ? 1 : 0) + (x.options.articles ? (x.totals.articles ?? NaN) : 0);
  if (!Number.isFinite(total) || total <= 0) return null;
  return Math.min(0.99, doneOf(x) / total);
}

/** The time left from the pace of the last few minutes. */
function useTimeLeft(x: Import, share: number | null, now: number): number | null {
  const samples = useRef<{ at: number; done: number }[]>([]);
  const done = doneOf(x);
  useEffect(() => {
    if (x.status !== "running") {
      samples.current = [];
      return;
    }
    const s = samples.current;
    if (!s.length || s[s.length - 1]!.done !== done) s.push({ at: Date.now(), done });
    while (s.length > 2 && Date.now() - s[0]!.at > 180_000) s.shift();
  }, [done, x.status]);
  const s = samples.current;
  if (share === null || s.length < 3) return null;
  const first = s[0]!;
  const last = s[s.length - 1]!;
  const rate = (last.done - first.done) / Math.max(1, last.at - first.at);
  if (rate <= 0) return null;
  const left = done / share - done;
  return Math.max(0, left / rate - (now - last.at));
}

function duration(ms: number, t: (k: "progress.lessThanMinute" | "progress.minutes" | "progress.hours", o?: Record<string, unknown>) => string) {
  const m = Math.round(ms / 60_000);
  if (m < 1) return t("progress.lessThanMinute");
  if (m < 90) return t("progress.minutes", { count: m });
  return t("progress.hours", { count: Math.round(m / 60) });
}

function Bar({ share, tone }: { share: number | null; tone: "run" | "done" | "paused" | "fail" }) {
  const reduce = useReducedMotion();
  const fill = { run: "bg-inverse", done: "bg-tone-green-solid", paused: "bg-tone-amber-solid", fail: "bg-tone-red-solid" }[tone];
  return (
    <div className="relative h-1.5 overflow-hidden rounded-full bg-hover" role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={share === null ? undefined : Math.round(share * 100)}>
      {share === null ? (
        tone === "run" && !reduce ? (
          <motion.div
            className={cn("absolute inset-y-0 w-1/3 rounded-full", fill)}
            initial={{ left: "-33%" }}
            animate={{ left: "100%" }}
            transition={{ duration: 1.4, ease: "easeInOut", repeat: Infinity }}
          />
        ) : (
          <div className={cn("absolute inset-0 opacity-40", fill)} />
        )
      ) : (
        <motion.div className={cn("absolute inset-y-0 left-0 rounded-full", fill)} initial={false} animate={{ width: `${share * 100}%` }} transition={{ duration: reduce ? 0 : dur.slow, ease: ease.out }} />
      )}
    </div>
  );
}

function Figure({ label, value }: { label: string; value: number }) {
  return (
    <div className="min-w-0">
      <div className="label-caps truncate">{label}</div>
      <div className="mt-1 text-[17px] font-light leading-none tracking-[-0.02em] tabular-nums text-fg">{number(value)}</div>
    </div>
  );
}

export function ImportCard({ x, admin }: { x: Import; admin: boolean }) {
  const { t } = useTranslation("imports");
  const qc = useQueryClient();
  const confirm = useConfirm();
  const reduce = useReducedMotion();
  const running = x.status === "running";
  const waiting = running && !!x.waiting_until && Date.parse(x.waiting_until) > Date.now();
  const now = useNow(running);
  const share = shareOf(x);
  const left = useTimeLeft(x, share, now);
  const [problemsOpen, setProblemsOpen] = useState(false);
  const [creds, setCreds] = useState<Record<string, string>>(() => defaultCredentials(x.source));
  const [credErr, setCredErr] = useState<Record<string, string>>({});
  const name = sourceName[x.source];

  const act = (fn: () => Promise<Import>, done?: (r: Import) => void) => ({
    mutationFn: fn,
    onSuccess: (r: Import) => {
      upsertInList(qc, qk.imports, r, { prepend: true });
      done?.(r);
    },
    onError: (e: unknown) => toast.error(e),
  });
  const pause = useMutation(act(() => api.imports.pause(x.id)));
  const resume = useMutation(act(() => api.imports.resume(x.id)));
  const cancel = useMutation(act(() => api.imports.cancel(x.id), () => toast.success(t("progress.canceledToast"))));
  const sync = useMutation(act(() => api.imports.sync(x.id)));
  const undo = useMutation(act(() => api.imports.undo(x.id)));
  const forget = useMutation(act(() => api.imports.removeCredentials(x.id), () => toast.success(t("progress.credentialsRemoved"))));
  const reconnect = useMutation({
    mutationFn: () => api.imports.resume(x.id, creds),
    onSuccess: (r) => {
      upsertInList(qc, qk.imports, r, { prepend: true });
      setCreds(defaultCredentials(x.source));
      setCredErr({});
    },
    onError: (e) => {
      const fe = credentialErrors(e);
      setCredErr(Object.keys(fe).length ? fe : { _: errorMessage(e) });
    },
  });

  const askUndo = () =>
    confirm.show(
      t("progress.undoBody", {
        tickets: number(x.counts.tickets),
        articles: number(x.counts.articles),
        count: x.counts.tickets,
      }),
      () => undo.mutateAsync(),
      { title: t("progress.undoTitle", { name }), confirmLabel: t("progress.undo"), danger: true },
    );
  const askCancel = () =>
    confirm.show(t("progress.cancelBody"), () => cancel.mutateAsync(), { title: t("progress.cancelTitle"), confirmLabel: t("progress.cancel"), danger: true });
  const askForget = () =>
    confirm.show(t("progress.forgetBody", { name }), () => forget.mutateAsync(), { title: t("progress.forgetTitle"), confirmLabel: t("progress.forget"), danger: true });

  const pill =
    x.status === "paused" && x.needs_credentials
      ? t("state.needsCredentials")
      : waiting
        ? t("state.waiting")
        : t(`state.${x.status}`);
  const barTone = x.status === "completed" ? "done" : x.status === "failed" ? "fail" : x.status === "paused" ? "paused" : "run";
  const phase =
    x.status === "undoing"
      ? t("progress.undoing")
      : waiting
        ? t("progress.waiting", { name, seconds: Math.max(1, Math.ceil((Date.parse(x.waiting_until!) - now) / 1000)) })
        : x.phase === "articles"
          ? t("progress.articles")
          : x.phase === "threading"
            ? t("progress.threading", { done: number(x.counts.threaded), total: number(x.counts.thread_total) })
            : x.run > 1
              ? t("progress.changes")
              : t("progress.tickets");
  const tag = x.options.tag;
  const canUndo = admin && x.status !== "running" && x.status !== "undoing" && x.status !== "undone";

  return (
    <div className="overflow-hidden rounded-lg border border-line bg-surface shadow-xs">
      <div className="flex items-center gap-3 px-4 py-3">
        <SourceMark source={x.source} />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className="truncate text-[13.5px] font-semibold tracking-[-0.01em] text-fg">{x.account.name || name}</span>
            {x.run > 1 && <span className="text-[11.5px] text-fg-4">{t("progress.run", { n: x.run })}</span>}
          </div>
          <div className="truncate text-[12px] text-fg-3">
            {x.started_by ? t("progress.startedBy", { name: x.started_by.name, when: relLong(x.started_at) }) : t("progress.started", { when: relLong(x.started_at) })}
            {x.finished_at && x.status !== "running" && <span className="text-fg-4"> · {t("progress.finished", { when: shortDate(x.finished_at) })}</span>}
          </div>
        </div>
        <StatePill state={waiting ? "pending" : x.needs_credentials && x.status === "paused" ? "failing" : statusState[x.status]} pulse={running || x.status === "undoing"}>
          {pill}
        </StatePill>
        {admin && (
          <div className="flex items-center gap-1">
            {running && (
              <Button size="xs" icon={<Pause />} loading={pause.isPending} onClick={() => pause.mutate()}>
                {t("progress.pause")}
              </Button>
            )}
            {(x.status === "paused" || x.status === "failed") && !x.needs_credentials && (
              <Button size="xs" variant="primary" icon={<Play />} loading={resume.isPending} onClick={() => resume.mutate()}>
                {x.status === "failed" ? t("progress.retry") : t("progress.resume")}
              </Button>
            )}
            {x.status === "completed" && x.can_sync && (
              <Button size="xs" icon={<RefreshCw />} loading={sync.isPending} onClick={() => sync.mutate()}>
                {t("progress.sync")}
              </Button>
            )}
            <Menu>
              <MenuTrigger asChild>
                <button
                  type="button"
                  aria-label={t("progress.actions")}
                  className="flex size-7 items-center justify-center rounded-md text-fg-3 transition-colors hover:bg-hover hover:text-fg"
                >
                  <MoreHorizontal className="size-4" />
                </button>
              </MenuTrigger>
              <MenuContent>
                {x.account.url && (
                  <MenuItem icon={<ArrowUpRight />} onSelect={() => window.open(x.account.url, "_blank", "noopener,noreferrer")}>
                    {t("progress.openSource", { name })}
                  </MenuItem>
                )}
                {x.can_sync && x.status !== "running" && (
                  <MenuItem icon={<KeyRound />} onSelect={askForget}>
                    {t("progress.forget")}
                  </MenuItem>
                )}
                {(running || x.status === "paused") && (
                  <>
                    <MenuSeparator />
                    <MenuItem danger icon={<X />} onSelect={askCancel}>
                      {t("progress.cancel")}
                    </MenuItem>
                  </>
                )}
                {canUndo && (
                  <>
                    <MenuSeparator />
                    <MenuItem danger icon={<Undo2 />} onSelect={askUndo}>
                      {t("progress.undo")}
                    </MenuItem>
                  </>
                )}
                {running && (
                  <MenuItem disabled icon={<Undo2 />}>
                    {t("progress.undoPauseFirst")}
                  </MenuItem>
                )}
              </MenuContent>
            </Menu>
          </div>
        )}
      </div>

      {(running || x.status === "paused" || x.status === "undoing" || x.status === "failed") && (
        <div className="border-t border-line-soft px-4 py-3">
          <div className="mb-2 flex items-center justify-between gap-3 text-[12px]">
            <span className={cn("flex min-w-0 items-center gap-1.5 truncate", waiting ? "text-tone-amber" : "text-fg-2")}>
              {waiting && <Hourglass className="size-3.5 shrink-0" aria-hidden />}
              {x.status === "paused" ? t("progress.paused") : phase}
            </span>
            <span className="shrink-0 tabular-nums text-fg-3">
              {share !== null && x.status !== "undoing" && <span className="font-medium text-fg">{Math.floor(share * 100)}%</span>}
              {running && left !== null && !waiting && <span> · {t("progress.left", { time: duration(left, t) })}</span>}
            </span>
          </div>
          <Bar share={x.status === "undoing" ? null : share} tone={barTone} />
        </div>
      )}

      {x.status === "paused" && x.needs_credentials && admin && x.source !== "csv" && (
        <form
          className="border-t border-line-soft bg-[color-mix(in_srgb,var(--tone-amber-soft)_45%,var(--surface))] px-4 py-3"
          onSubmit={(e) => {
            e.preventDefault();
            if (x.source !== "csv" && credentialsReady(x.source, creds, true)) reconnect.mutate();
          }}
        >
          <p className="mb-3 flex items-start gap-2 text-[12.5px] leading-[18px] text-fg-2">
            <KeyRound className="mt-0.5 size-3.5 shrink-0 text-tone-amber" aria-hidden />
            <span>
              <span className="font-medium text-fg">{t("progress.needsCredentials", { name })}</span> {x.last_error && <span className="text-fg-3">{x.last_error}</span>}
            </span>
          </p>
          <CredentialsForm source={x.source} value={creds} onChange={setCreds} errors={credErr} autoFocus={false} replacing />
          <div className="mt-3 flex items-center justify-end gap-2">
            {credErr._ && <span className="mr-auto text-xs text-danger-fg">{credErr._}</span>}
            <Button type="submit" size="sm" variant="primary" loading={reconnect.isPending} disabled={!credentialsReady(x.source, creds, true)}>
              {t("progress.resumeWith")}
            </Button>
          </div>
        </form>
      )}

      {x.status === "failed" && x.last_error && (
        <p className="flex items-start gap-2 border-t border-line-soft bg-[color-mix(in_srgb,var(--tone-red-soft)_45%,var(--surface))] px-4 py-2.5 text-[12.5px] leading-[18px] text-fg-2">
          <AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-tone-red" aria-hidden />
          <span>
            {x.last_error} <span className="text-fg-3">{t("progress.failedHint")}</span>
          </span>
        </p>
      )}

      {x.status === "completed" && (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2 border-t border-line-soft bg-[color-mix(in_srgb,var(--tone-green-soft)_40%,var(--surface))] px-4 py-2.5 text-[12.5px] text-fg-2">
          <span className="flex min-w-0 items-center gap-2">
            <CheckCircle2 className="size-3.5 shrink-0 text-tone-green" aria-hidden />
            {x.run > 1 ? t("progress.syncDone", { count: x.counts.updated }) : t("progress.done", { name })}
          </span>
          {x.options.tickets && x.counts.tickets + x.counts.updated > 0 && (
            <Link
              to="/inbox/$folder/{-$ticketId}"
              params={{ folder: "solved", ticketId: undefined }}
              search={tag ? { tag } : {}}
              className="ml-auto inline-flex items-center gap-0.5 font-medium text-fg underline decoration-line-strong underline-offset-2 hover:decoration-fg"
            >
              {t("progress.seeTickets")}
              <ArrowUpRight className="size-3" aria-hidden />
            </Link>
          )}
        </div>
      )}

      {x.status !== "undone" && (
        <div className="grid grid-cols-3 gap-4 border-t border-line-soft px-4 py-3 sm:grid-cols-6">
          <Figure label={t("figures.tickets")} value={x.counts.tickets} />
          <Figure label={t("figures.messages")} value={x.counts.messages} />
          <Figure label={t("figures.contacts")} value={x.counts.contacts} />
          <Figure label={t("figures.articles")} value={x.counts.articles} />
          <Figure label={t("figures.attachments")} value={x.counts.attachments} />
          <Figure label={t("figures.updated")} value={x.counts.updated} />
        </div>
      )}
      {x.status === "undone" && <p className="border-t border-line-soft px-4 py-2.5 text-[12.5px] text-fg-3">{t("progress.undoneHint")}</p>}

      {x.problems.length > 0 && x.status !== "undone" && (
        <div className="border-t border-line-soft">
          <button
            type="button"
            onClick={() => setProblemsOpen((o) => !o)}
            aria-expanded={problemsOpen}
            className="flex w-full items-center gap-2 px-4 py-2 text-left text-[12px] text-fg-2 transition-colors hover:bg-hover"
          >
            <AlertTriangle className="size-3.5 text-tone-amber" aria-hidden />
            {t("progress.problems", { count: x.counts.skipped + x.counts.failed || x.problems.length })}
            <ChevronDown className={cn("ml-auto size-3.5 text-fg-4 transition-transform duration-150", problemsOpen && "rotate-180")} aria-hidden />
          </button>
          <AnimatePresence initial={false}>
            {problemsOpen && (
              <motion.ul
                initial={reduce ? false : { height: 0, opacity: 0 }}
                animate={{ height: "auto", opacity: 1 }}
                exit={reduce ? { opacity: 0 } : { height: 0, opacity: 0 }}
                transition={{ duration: dur.base, ease: ease.out }}
                className="max-h-[260px] overflow-y-auto border-t border-line-soft bg-canvas"
              >
                {x.problems.map((p, i) => (
                  <li key={i} className="flex gap-3 border-b border-line-soft px-4 py-2 text-[12px] last:border-0">
                    <span className="mono w-[140px] shrink-0 truncate text-fg-2">{p.item}</span>
                    <span className="min-w-0 flex-1 text-fg-3">{p.message}</span>
                  </li>
                ))}
                {x.counts.skipped + x.counts.failed > x.problems.length && (
                  <li className="px-4 py-2 text-[11.5px] text-fg-4">{t("progress.moreProblems", { count: x.counts.skipped + x.counts.failed - x.problems.length })}</li>
                )}
              </motion.ul>
            )}
          </AnimatePresence>
        </div>
      )}

      {x.status === "completed" && x.can_sync && (
        <p className="flex items-center gap-1.5 border-t border-line-soft px-4 py-2 text-[11.5px] text-fg-4">
          <Lock className="size-3" aria-hidden />
          {t("progress.kept")}
        </p>
      )}
    </div>
  );
}
