// Ollie in the ticket side panel: one light section where the assistant sums up
// the conversation, proposes properties (its own below-threshold decisions,
// contracts/ai.md "Decisions", and on-demand triage) and answers questions about
// the ticket. Ollie's blob is the only mark and its status line does the
// talking: what it is doing, what it has for you, what just happened.

import { useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { AIAnswer, AISummary, IntegrationStarter, Priority, Sentiment, Severity, Suggestion, SuggestionField, Ticket, TicketPatch, Triage } from "@/lib/api/types";
import { api } from "@/lib/api/endpoints";
import { useAI } from "@/lib/ai";
import { aiSuggestions, pct } from "@/lib/decisions";
import { relLong, fullDate } from "@/lib/format";
import { ease, springs } from "@/lib/motion";
import { q } from "@/lib/queries";
import { sanitizeHtml } from "@/lib/sanitize";
import { atLeast, useRole, useSession } from "@/lib/session";
import { patchTicketEverywhere } from "@/lib/ticketCache";
import { tone, type Tone } from "@/lib/tones";
import { cn } from "@/lib/cn";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { TagChip } from "@/components/ui/badge";
import { Tooltip } from "@/components/ui/tooltip";
import { OllieMark } from "@/components/assistant/OllieMark";
import { useAssistantName } from "@/components/assistant/name";
import { ollie } from "@/components/assistant/store";
import { PriorityPill, SeverityChip, severityLabel } from "@/components/tickets/bits";
import { useDisplay } from "@/components/tickets/fields";
import { useDismissSuggestion, useUpdateTicket } from "@/components/tickets/useTicketActions";
import { AiTooltip, useCostLabel } from "../bits";
import { StreamingHtml } from "../ui/StreamingText";
import { formatElapsed, useElapsed } from "../ui/Thinking";
import { balanceSpent, isAbort, reportAiError } from "./errors";
import { streamTextToHtml } from "./text";

const OPEN_KEY = "od-ollie-panel";

/** custom.ai_summary is stored as {text, key_points, sentiment, updated_at, message_count} (contracts/ai.md). */
function readSummary(t: Ticket): AISummary | null {
  const s = t.custom?.ai_summary as (Partial<AISummary> & { text?: string }) | undefined;
  const text = typeof s?.text === "string" ? s.text : typeof s?.summary === "string" ? s.summary : null;
  if (!s || text === null) return null;
  return { summary: text, key_points: Array.isArray(s.key_points) ? s.key_points.filter((x) => typeof x === "string") : [], sentiment: (s.sentiment ?? "neutral") as Sentiment, updated_at: s.updated_at ?? t.updated_at };
}

/** Write a change into every cached copy of the ticket (lists and detail, by id or by number). */
function patchCachedTicket(qc: QueryClient, t: Ticket, me: string) {
  patchTicketEverywhere(qc, t, me);
  qc.setQueriesData<Ticket>({ queryKey: ["tickets", "detail"] }, (old) => (old && old.id === t.id ? t : old));
}

/** The PATCH that accepts one of Ollie's stored suggestions. */
export function acceptPatch(field: SuggestionField, s: Suggestion): TicketPatch {
  switch (field) {
    case "priority":
      return { priority: s.value as Priority };
    case "severity":
      return { severity: s.value as Severity };
    case "team":
      return { team_id: s.value as string };
    case "labels":
      return { add_tags: s.value as string[] };
    case "spam":
      return { spam: true };
  }
}

/** Whether a triage result would change anything on the ticket. */
function proposes(r: Triage, t: Ticket) {
  const current = new Set(t.tags.map((x) => x.name.toLowerCase()));
  return (
    r.priority !== t.priority ||
    (!!r.severity && r.severity !== t.severity) ||
    (!!r.team_id && r.team_id !== t.team_id) ||
    [...r.tags, ...(r.suggested_tags ?? [])].some((x) => !current.has(x.toLowerCase()))
  );
}

/**
 * Ollie's voice: work in flight (the blob thinks and the status line says what),
 * and a short note after (the blob cheers when it went well).
 */
type Run = <T>(label: string, fn: () => Promise<T>) => Promise<T>;

function useVoice(ticketId: string) {
  const [items, setItems] = useState<{ id: number; label: string }[]>([]);
  const [ok, setOk] = useState(true);
  const [note, setNote] = useState<string | null>(null);
  const seq = useRef(0);
  const run: Run = useCallback(async (label, fn) => {
    const id = ++seq.current;
    setNote(null);
    setItems((x) => [...x, { id, label }]);
    try {
      const r = await fn();
      setOk(true);
      return r;
    } catch (e) {
      setOk(false);
      throw e;
    } finally {
      setItems((x) => x.filter((y) => y.id !== id));
    }
  }, []);
  useEffect(() => setNote(null), [ticketId]);
  useEffect(() => {
    if (!note) return;
    const id = setTimeout(() => setNote(null), 2600);
    return () => clearTimeout(id);
  }, [note]);
  return { working: items.at(-1)?.label ?? null, ok, note, say: setNote, run };
}

function readOpen() {
  try {
    return localStorage.getItem(OPEN_KEY) !== "0";
  } catch {
    return true;
  }
}

const sentimentTone: Record<Sentiment, Tone> = { positive: "green", neutral: "gray", negative: "amber", frustrated: "red" };

/** Small text actions; Ollie's section has no icon buttons. */
const textBtn =
  "inline-flex h-6 shrink-0 items-center rounded-md px-1.5 text-[11.5px] font-medium outline-none transition-[background-color,color,transform] duration-150 hover:bg-hover focus-visible:shadow-[0_0_0_2px_var(--focus)] active:scale-[0.97] disabled:pointer-events-none disabled:opacity-50";

/** A piece of the section that eases its height in and out as it appears. */
function Piece({ children }: { children: ReactNode }) {
  const reduce = useReducedMotion();
  return (
    <motion.div
      initial={reduce ? { opacity: 0 } : { opacity: 0, height: 0 }}
      animate={reduce ? { opacity: 1 } : { opacity: 1, height: "auto" }}
      exit={reduce ? { opacity: 0 } : { opacity: 0, height: 0, transition: { duration: 0.16 } }}
      transition={{ duration: 0.24, ease: ease.out }}
      className="overflow-hidden"
    >
      {children}
    </motion.div>
  );
}

function Lines({ widths }: { widths: string[] }) {
  return (
    <div className="flex flex-col gap-2 py-1">
      {widths.map((w) => (
        <Skeleton key={w} className="h-2.5" style={{ width: w }} />
      ))}
    </div>
  );
}

// Summary

function useSummary(ticket: Ticket, run: Run) {
  const { t } = useTranslation("aiTicket");
  const qc = useQueryClient();
  const me = useSession((s) => s.user?.id ?? "");
  const [busy, setBusy] = useState(false);
  const summarize = async () => {
    setBusy(true);
    try {
      const res = await run(t("summary.working"), () => api.ai.summary(ticket.id));
      const cur = qc.getQueryData<Ticket>(["tickets", "detail", ticket.id]) ?? ticket;
      patchCachedTicket(qc, { ...cur, custom: { ...cur.custom, ai_summary: { text: res.summary, key_points: res.key_points, sentiment: res.sentiment, updated_at: res.updated_at }, ai_sentiment: res.sentiment } }, me);
      balanceSpent(qc);
    } catch (e) {
      reportAiError(e, t("summary.failed"));
    } finally {
      setBusy(false);
    }
  };
  return { stored: readSummary(ticket), busy, summarize };
}

function SummaryView({ s, busy, onRefresh }: { s: AISummary; busy: boolean; onRefresh?: () => void }) {
  const { t } = useTranslation("aiTicket");
  const reduce = useReducedMotion();
  return (
    <div className={cn("flex flex-col gap-2 transition-opacity duration-200", busy && "opacity-40")} aria-busy={busy || undefined} data-ph-mask>
      <p className="text-[12.5px] leading-[19px] text-fg">{s.summary}</p>
      {s.key_points.length > 0 && (
        <ul className="flex flex-col gap-1">
          {s.key_points.map((p, i) => (
            <motion.li
              key={`${i}-${p}`}
              initial={reduce ? false : { opacity: 0, x: -3 }}
              animate={{ opacity: 1, x: 0 }}
              transition={{ duration: 0.2, ease: ease.out, delay: reduce ? 0 : 0.08 + i * 0.05 }}
              className="flex gap-2 text-[12px] leading-[18px] text-fg-2"
            >
              <span aria-hidden className={cn("mt-[7px] size-1 shrink-0 rounded-full", tone.violet.solid)} />
              <span className="min-w-0">{p}</span>
            </motion.li>
          ))}
        </ul>
      )}
      <div className="flex items-center gap-2 text-[11px] text-fg-4">
        <Tooltip content={t("summary.mood")}>
          <span className={cn("inline-flex h-[18px] items-center rounded-sm px-1.5 text-[10.5px] font-medium", tone[sentimentTone[s.sentiment] ?? "gray"].chip)}>{t(`sentiment.${s.sentiment}`)}</span>
        </Tooltip>
        <Tooltip content={fullDate(s.updated_at)}>
          <span className="min-w-0 flex-1 truncate">{t("summary.updated", { when: relLong(s.updated_at) })}</span>
        </Tooltip>
        {onRefresh && (
          <AiTooltip feature="summarize" label={t("summary.refreshHint")}>
            <button type="button" onClick={onRefresh} disabled={busy} className={cn(textBtn, "-mr-1.5 font-normal text-fg-4 hover:text-fg")}>
              {t("summary.refresh")}
            </button>
          </AiTooltip>
        )}
      </div>
    </div>
  );
}

// Suggestions

type Row = {
  key: string;
  label: string;
  value: ReactNode;
  /** Plain words for the value, for screen readers. */
  text: string;
  sure: number | null;
  patch: TicketPatch;
  apply: () => void;
  skip: () => void;
};

type AskedKey = "priority" | "severity" | "team" | "tags" | "new_tags";

function SeverityValue({ s }: { s: Severity }) {
  return (
    <span className="flex items-center gap-1.5">
      <SeverityChip severity={s} />
      {severityLabel[s]}
    </span>
  );
}

function useSuggestions(ticket: Ticket, run: Run, say: (s: string) => void) {
  const { t } = useTranslation("aiTicket");
  const qc = useQueryClient();
  const me = useSession((s) => s.user?.id ?? "");
  const name = useAssistantName();
  const display = useDisplay();
  const update = useUpdateTicket();
  const dismiss = useDismissSuggestion();
  const { data: teams } = useQuery(q.teams());
  const { data: tags } = useQuery(q.tags());
  const [busy, setBusy] = useState(false);
  const [applying, setApplying] = useState(false);
  const [result, setResult] = useState<Triage | null>(null);
  const [handled, setHandled] = useState<Set<AskedKey>>(new Set());

  // A new ticket starts clean.
  useEffect(() => {
    setResult(null);
    setHandled(new Set());
  }, [ticket.id]);

  const tagColor = (n: string) => tags?.find((x) => x.name.toLowerCase() === n.toLowerCase())?.color ?? null;
  const teamName = (id: string) => teams?.find((x) => x.id === id)?.name ?? t("suggest.fields.team");
  const tagList = (names: string[]) => names.map((n) => <TagChip key={n} name={n} color={tagColor(n)} />);
  const patchAsync = (patch: TicketPatch) => run(t("ollie.applying"), () => update.mutateAsync({ id: ticket.id, patch }));
  const done = () => say(t("suggest.done"));

  // What the on-demand triage proposes that the ticket doesn't have yet.
  const current = new Set(ticket.tags.map((x) => x.name.toLowerCase()));
  const asked: Row[] = [];
  if (result) {
    const addTags = result.tags.filter((x) => !current.has(x.toLowerCase()));
    const newTags = (result.suggested_tags ?? []).filter((x) => !current.has(x.toLowerCase()));
    const askedRow = (key: AskedKey, label: string, value: ReactNode, text: string, patch: TicketPatch): Row => ({
      key,
      label,
      value,
      text,
      sure: null,
      patch,
      apply: () => {
        setHandled((s) => new Set(s).add(key));
        patchAsync(patch)
          .then(() => last && done())
          .catch(() =>
            setHandled((s) => {
              const n = new Set(s);
              n.delete(key);
              return n;
            }),
          );
      },
      skip: () => setHandled((s) => new Set(s).add(key)),
    });
    if (result.priority !== ticket.priority)
      asked.push(askedRow("priority", t("suggest.fields.priority"), <PriorityPill priority={result.priority} label={display.priority(result.priority)} hue={display.priorityTone(result.priority)} />, display.priority(result.priority), { priority: result.priority }));
    if (result.severity && result.severity !== ticket.severity)
      asked.push(askedRow("severity", t("suggest.fields.severity"), <SeverityValue s={result.severity} />, severityLabel[result.severity], { severity: result.severity }));
    if (result.team_id && result.team_id !== ticket.team_id)
      asked.push(askedRow("team", t("suggest.fields.team"), <span className="truncate">{teamName(result.team_id)}</span>, teamName(result.team_id), { team_id: result.team_id }));
    if (addTags.length) asked.push(askedRow("tags", t("suggest.fields.labels"), tagList(addTags), addTags.join(", "), { add_tags: addTags }));
    if (newTags.length)
      asked.push(
        askedRow(
          "new_tags",
          t("suggest.fields.newLabels"),
          newTags.map((x) => (
            <Tooltip key={x} content={t("suggest.newTag")}>
              <span className="inline-flex h-[19px] items-center rounded-sm border border-dashed border-line-strong px-1.5 text-[10.5px] text-fg-2">{x}</span>
            </Tooltip>
          )),
          newTags.join(", "),
          { add_tags: newTags },
        ),
      );
  }
  const askedLeft = asked.filter((r) => !handled.has(r.key as AskedKey));
  const askedFields = new Set<SuggestionField>(result ? ["priority", "severity", "team", "labels"] : []);

  // Ollie's own decisions that fell below the workspace threshold.
  const auto: Row[] = aiSuggestions(ticket)
    .filter(([f]) => !askedFields.has(f))
    .map(([field, s]) => {
      let value: ReactNode;
      let text: string;
      switch (field) {
        case "priority":
          text = display.priority(s.value as Priority);
          value = <PriorityPill priority={s.value as Priority} label={text} hue={display.priorityTone(s.value as Priority)} />;
          break;
        case "severity":
          text = severityLabel[s.value as Severity];
          value = <SeverityValue s={s.value as Severity} />;
          break;
        case "team":
          text = teamName(s.value as string);
          value = <span className="truncate">{text}</span>;
          break;
        case "labels":
          text = (s.value as string[]).join(", ");
          value = tagList(s.value as string[]);
          break;
        case "spam":
          text = t("suggest.spam");
          value = <span className={tone.red.text}>{text}</span>;
          break;
      }
      const patch = acceptPatch(field, s);
      return {
        key: `auto-${field}`,
        label: t(`suggest.fields.${field}`),
        value,
        text,
        sure: pct(s.confidence),
        patch,
        apply: () =>
          void patchAsync(patch)
            .then(() => last && done())
            .catch(() => {}),
        skip: () => dismiss.mutate({ id: ticket.id, field }),
      };
    });

  const rows = [...askedLeft, ...auto];
  const last = rows.length === 1;

  // Every proposal applied or skipped: back to the starting point.
  useEffect(() => {
    if (result && askedLeft.length === 0) setResult(null);
  }, [result, askedLeft.length]);

  const suggest = async () => {
    setBusy(true);
    try {
      const r = await run(t("suggest.working"), () => api.ai.triage(ticket.id, false));
      balanceSpent(qc);
      setHandled(new Set());
      if (proposes(r, ticket)) setResult(r);
      else say(t("suggest.nothing", { name }));
    } catch (e) {
      reportAiError(e, t("suggest.failed"));
    } finally {
      setBusy(false);
    }
  };

  const applyAll = async () => {
    setApplying(true);
    try {
      // Untouched triage: let the server apply it whole (it creates new labels too).
      const whole = !!result && handled.size === 0 && askedLeft.length > 0;
      if (whole)
        await run(t("ollie.applying"), async () => {
          await api.ai.triage(ticket.id, true);
          balanceSpent(qc);
          patchCachedTicket(qc, await api.tickets.get(ticket.id), me);
        });
      const patch: TicketPatch = {};
      for (const r of whole ? auto : rows) {
        const { add_tags, ...rest } = r.patch;
        Object.assign(patch, rest);
        if (add_tags) patch.add_tags = [...(patch.add_tags ?? []), ...add_tags];
      }
      if (Object.keys(patch).length) await patchAsync(patch);
      setResult(null);
      setHandled(new Set());
      done();
    } catch (e) {
      reportAiError(e, t("suggest.failed"));
    } finally {
      setApplying(false);
    }
  };

  const skipAll = () => {
    setResult(null);
    setHandled(new Set());
    for (const [field] of aiSuggestions(ticket).filter(([f]) => !askedFields.has(f))) dismiss.mutate({ id: ticket.id, field });
  };

  return { rows, busy, applying, suggest, applyAll, skipAll };
}

function SuggestionRow({ row, disabled }: { row: Row; disabled: boolean }) {
  const { t } = useTranslation("aiTicket");
  const name = useAssistantName();
  const reduce = useReducedMotion();
  const what = `${row.label}: ${row.text}`;
  const value = <span className="flex min-w-0 flex-1 flex-wrap items-center gap-1 text-[12px] text-fg">{row.value}</span>;
  return (
    <motion.li
      layout={reduce ? false : "position"}
      initial={reduce ? false : { opacity: 0, y: -3 }}
      animate={{ opacity: 1, y: 0 }}
      exit={reduce ? { opacity: 0, transition: { duration: 0 } } : { opacity: 0, x: 8, transition: { duration: 0.14 } }}
      transition={springs.row}
      className="flex min-h-8 items-center gap-2 py-0.5"
    >
      <span className="w-[54px] shrink-0 text-[11.5px] text-fg-3">{row.label}</span>
      {row.sure != null ? (
        <Tooltip content={t("suggest.sure", { name, pct: row.sure })}>
          <span className="flex min-w-0 flex-1" tabIndex={-1}>
            {value}
          </span>
        </Tooltip>
      ) : (
        value
      )}
      <button type="button" onClick={row.apply} disabled={disabled} aria-label={t("suggest.applyAria", { what })} className={cn(textBtn, "text-fg")}>
        {t("suggest.apply")}
      </button>
      <button type="button" onClick={row.skip} disabled={disabled} aria-label={t("suggest.skipAria", { what })} className={cn(textBtn, "-ml-1.5 text-fg-4 hover:text-fg-2")}>
        {t("suggest.skip")}
      </button>
    </motion.li>
  );
}

function SuggestionList({ rows, applying, onApplyAll, onSkipAll }: { rows: Row[]; applying: boolean; onApplyAll: () => void; onSkipAll: () => void }) {
  const { t } = useTranslation("aiTicket");
  return (
    <div className={cn("rounded-lg px-2.5 py-1", tone.violet.soft)}>
      <ul className="flex flex-col">
        <AnimatePresence initial={false}>
          {rows.map((r) => (
            <SuggestionRow key={r.key} row={r} disabled={applying} />
          ))}
        </AnimatePresence>
      </ul>
      {rows.length > 1 && (
        <div className="flex items-center justify-end gap-1 pb-1 pt-0.5">
          <button type="button" onClick={onSkipAll} disabled={applying} className={cn(textBtn, "text-fg-4 hover:text-fg-2")}>
            {t("suggest.skipAll")}
          </button>
          <Button size="xs" variant="primary" loading={applying} onClick={onApplyAll}>
            {t("suggest.applyAll")}
          </Button>
        </div>
      )}
    </div>
  );
}

// Ask

type Turn = { id: number; question: string; text: string; answer: AIAnswer | null; error?: boolean; took?: number };

function TurnView({ x }: { x: Turn }) {
  const { t } = useTranslation("aiTicket");
  const name = useAssistantName();
  const reduce = useReducedMotion();
  const working = !x.answer && !x.error;
  const elapsed = useElapsed(working);
  return (
    <motion.div
      layout={reduce ? false : "position"}
      initial={reduce ? false : { opacity: 0, y: 6 }}
      animate={{ opacity: 1, y: 0 }}
      exit={{ opacity: 0, transition: { duration: 0.12 } }}
      transition={springs.row}
      className="flex flex-col gap-2"
    >
      <p className="max-w-[88%] self-end rounded-lg rounded-br-sm bg-hover px-2.5 py-1.5 text-[12.5px] leading-[18px] text-fg" data-ph-mask>
        {x.question}
      </p>
      <div className="flex flex-col gap-1.5">
        <div className="flex items-center gap-1.5 text-[11.5px]">
          <OllieMark size={14} state={working ? "thinking" : "idle"} celebrate={!x.error} />
          <span className="font-medium text-fg-2">{name}</span>
          <span role="status" aria-live="polite" className="min-w-0 truncate text-fg-4">
            {x.error ? (
              <span className="text-danger-fg">{t("ask.failed")}</span>
            ) : working ? (
              <>
                <span className="ollie-shimmer">{x.text ? t("ask.writing") : t("ask.thinking")}</span>
                <span className="mono ml-1.5 text-[10.5px] tabular-nums">{formatElapsed(elapsed)}</span>
              </>
            ) : (
              t("ask.answered", { time: formatElapsed(x.took ?? 0) })
            )}
          </span>
        </div>
        {x.answer ? <StreamingHtml html={sanitizeHtml(x.answer.answer_html)} /> : x.text ? <StreamingHtml html={streamTextToHtml(x.text)} streaming /> : null}
        {x.answer && x.answer.sources.length > 0 && (
          <p className="text-[11px] leading-4 text-fg-4">
            {t("ask.sources")}{" "}
            {x.answer.sources.map((s, i) => (
              <span key={s.article_id}>
                {i > 0 && ", "}
                <a href={s.url} target="_blank" rel="noopener noreferrer" className="text-fg-2 underline decoration-line-strong underline-offset-2 transition-colors hover:text-fg hover:decoration-fg-4">
                  {s.title}
                </a>
              </span>
            ))}
          </p>
        )}
      </div>
    </motion.div>
  );
}

const STARTERS = ["want", "tried", "next"] as const;

function Ask({ t: ticket, run }: { t: Ticket; run: Run }) {
  const { t } = useTranslation("aiTicket");
  const qc = useQueryClient();
  const name = useAssistantName();
  const reduce = useReducedMotion();
  const cost = useCostLabel()("copilot");
  const [question, setQuestion] = useState("");
  const [focused, setFocused] = useState(false);
  const [turns, setTurns] = useState<Turn[]>([]);
  const abort = useRef<AbortController | null>(null);
  const busy = turns.some((x) => !x.answer && !x.error);
  const canSend = !!question.trim() && !busy;
  const starters = focused && !question && !busy;

  useEffect(() => {
    setTurns([]);
    return () => abort.current?.abort();
  }, [ticket.id]);

  const ask = async (text: string) => {
    const qn = text.trim();
    if (!qn || busy) return;
    const id = Date.now();
    setQuestion("");
    setTurns((x) => [...x.slice(-2), { id, question: qn, text: "", answer: null }]);
    const ac = new AbortController();
    abort.current = ac;
    try {
      const answer = await run(t("ask.thinking"), () =>
        api.ai.ask(ticket.id, qn, {
          signal: ac.signal,
          onDelta: (_d, soFar) => setTurns((x) => x.map((y) => (y.id === id ? { ...y, text: soFar } : y))),
        }),
      );
      const took = (Date.now() - id) / 1000;
      setTurns((x) => x.map((y) => (y.id === id ? { ...y, answer, took } : y)));
      balanceSpent(qc);
    } catch (e) {
      setTurns((x) => (isAbort(e) ? x.filter((y) => y.id !== id) : x.map((y) => (y.id === id ? { ...y, error: true } : y))));
      reportAiError(e, t("ask.failed"));
    } finally {
      abort.current = null;
    }
  };

  return (
    <div
      className="flex flex-col gap-2"
      onFocus={() => setFocused(true)}
      onBlur={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setFocused(false);
      }}
    >
      {turns.length > 0 && (
        <div className="flex flex-col gap-4 pb-1" aria-live="polite">
          <AnimatePresence initial={false}>
            {turns.map((x) => (
              <TurnView key={x.id} x={x} />
            ))}
          </AnimatePresence>
          {!busy && (
            <button type="button" onClick={() => setTurns([])} className={cn(textBtn, "-ml-1.5 -mt-2 self-start font-normal text-fg-4 hover:text-fg")}>
              {t("ask.clear")}
            </button>
          )}
        </div>
      )}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (canSend) void ask(question);
        }}
        className="flex h-8 items-center gap-1 rounded-lg border border-line bg-surface pl-2.5 pr-1 transition-[border-color,box-shadow] duration-150 focus-within:border-line-strong focus-within:shadow-[0_0_0_3px_var(--focus)]"
      >
        <input
          value={question}
          onChange={(e) => setQuestion(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Escape") e.currentTarget.blur();
          }}
          placeholder={t("ask.placeholder", { name })}
          aria-label={t("ask.title", { name })}
          maxLength={500}
          className="min-w-0 flex-1 bg-transparent text-[12.5px] text-fg outline-none placeholder:text-fg-4 focus-visible:shadow-none"
        />
        <AnimatePresence initial={false}>
          {canSend && (
            <motion.span
              initial={reduce ? { opacity: 0 } : { opacity: 0, scale: 0.9 }}
              animate={{ opacity: 1, scale: 1 }}
              exit={reduce ? { opacity: 0 } : { opacity: 0, scale: 0.9, transition: { duration: 0.1 } }}
              transition={springs.snap}
              className="flex"
            >
              <AiTooltip feature="copilot" label={t("ask.send")}>
                <Button type="submit" size="xs" variant="primary" aria-label={`${t("ask.send")}${cost ? `, ${cost}` : ""}`}>
                  {t("ask.send")}
                </Button>
              </AiTooltip>
            </motion.span>
          )}
        </AnimatePresence>
      </form>
      <AnimatePresence initial={false}>
        {starters && (
          <Piece>
            <div className="flex flex-col items-start">
              {STARTERS.map((k, i) => (
                <motion.button
                  key={k}
                  type="button"
                  initial={reduce ? false : { opacity: 0, x: -3 }}
                  animate={{ opacity: 1, x: 0 }}
                  transition={{ duration: 0.18, ease: ease.out, delay: reduce ? 0 : i * 0.04 }}
                  onClick={() => void ask(t(`ask.starters.${k}`))}
                  className="h-6 rounded-md px-1.5 text-[11.5px] text-fg-3 outline-none transition-colors hover:bg-hover hover:text-fg focus-visible:bg-hover focus-visible:text-fg"
                >
                  {t(`ask.starters.${k}`)}
                </motion.button>
              ))}
            </div>
          </Piece>
        )}
      </AnimatePresence>
    </div>
  );
}

/** Ready-made asks for the products the workspace connected, as Ollie shortcuts. */
function useStarters(on: boolean): IntegrationStarter[] {
  const { data } = useQuery({ ...q.integrations(), enabled: on });
  if (!on || !data) return [];
  return data.filter((x) => x.enabled && x.status === "connected").flatMap((x) => x.starters);
}

function Starters({ starters }: { starters: IntegrationStarter[] }) {
  const { t } = useTranslation("aiTicket");
  const [all, setAll] = useState(false);
  const shown = all ? starters : starters.slice(0, 3);
  return (
    <div className="flex flex-col gap-1.5">
      <span className="text-[11px] text-fg-4">{t("tools.title")}</span>
      <div className="flex flex-wrap gap-1.5">
        {shown.map((s) => (
          <button
            key={s.label}
            type="button"
            onClick={() => ollie.ask(s.prompt)}
            className="inline-flex h-6 max-w-full items-center rounded-md border border-line bg-surface px-2 text-[11.5px] text-fg-2 transition-colors hover:border-[var(--tone-violet-line)] hover:bg-tone-violet-soft hover:text-tone-violet"
          >
            <span className="truncate">{s.label}</span>
          </button>
        ))}
        {starters.length > 3 && (
          <button type="button" onClick={() => setAll((v) => !v)} className="h-6 px-1 text-[11.5px] text-fg-4 transition-colors hover:text-fg">
            {all ? t("tools.fewer") : t("tools.more", { count: starters.length - 3 })}
          </button>
        )}
      </div>
    </div>
  );
}

/** Ollie's section of the ticket side panel. Shows only what is on: AI features, or stored suggestions. */
export function OlliePanel({ t: ticket }: { t: Ticket }) {
  const ai = useAI();
  const role = useRole();
  const agent = atLeast(role, "agent");
  const waiting = agent ? aiSuggestions(ticket).length : 0;
  if (!ai.enabled && !waiting) return null;
  return <Ollie t={ticket} aiOn={ai.enabled} agent={agent} />;
}

function Ollie({ t: ticket, aiOn, agent }: { t: Ticket; aiOn: boolean; agent: boolean }) {
  const { t } = useTranslation("aiTicket");
  const name = useAssistantName();
  const reduce = useReducedMotion();
  const cost = useCostLabel();
  const voice = useVoice(ticket.id);
  const summary = useSummary(ticket, voice.run);
  const sugg = useSuggestions(ticket, voice.run, voice.say);
  const starters = useStarters(aiOn && agent);
  const [open, setOpen] = useState(readOpen);

  const toggle = () =>
    setOpen((o) => {
      try {
        localStorage.setItem(OPEN_KEY, o ? "0" : "1");
      } catch {
        /* storage unavailable */
      }
      return !o;
    });

  const rows = agent ? sugg.rows : [];
  const status = voice.working ?? voice.note ?? (rows.length ? t("ollie.waiting", { count: rows.length }) : t("ollie.idle"));
  // A summary is stored on the ticket, so making one is an agent's write.
  const canSummarize = aiOn && agent && !summary.stored && !summary.busy;
  const canSuggest = aiOn && agent && !rows.length && !sugg.busy;

  return (
    <section aria-label={name} className="border-b border-line-soft">
      <button
        type="button"
        onClick={toggle}
        aria-expanded={open}
        className="ollie-host group flex w-full items-center gap-2.5 px-4 py-3 text-left outline-none transition-colors hover:bg-sunken/60 focus-visible:shadow-[inset_0_0_0_2px_var(--focus)]"
      >
        <OllieMark size={22} state={voice.working ? "thinking" : "idle"} celebrate={voice.ok} />
        <span className="flex min-w-0 flex-1 flex-col">
          <span className="text-[12.5px] font-medium leading-4 text-fg">{name}</span>
          <span className="relative h-4 overflow-hidden text-[11.5px] leading-4">
            <AnimatePresence mode="popLayout" initial={false}>
              <motion.span
                key={status}
                role="status"
                initial={reduce ? { opacity: 0 } : { opacity: 0, y: 6 }}
                animate={{ opacity: 1, y: 0 }}
                exit={reduce ? { opacity: 0 } : { opacity: 0, y: -6 }}
                transition={{ duration: 0.18, ease: ease.out }}
                className={cn("block truncate", voice.working ? "ollie-shimmer" : voice.note ? tone.green.text : rows.length ? "text-fg-2" : "text-fg-4")}
              >
                {status}
              </motion.span>
            </AnimatePresence>
          </span>
        </span>
        <span className="text-[11px] text-fg-4 opacity-0 transition-opacity group-hover:opacity-100 group-focus-visible:opacity-100">{open ? t("ollie.hide") : t("ollie.show")}</span>
      </button>
      <AnimatePresence initial={false}>
        {open && (
          <motion.div
            key="body"
            initial={reduce ? { opacity: 0 } : { height: 0, opacity: 0 }}
            animate={reduce ? { opacity: 1 } : { height: "auto", opacity: 1 }}
            exit={reduce ? { opacity: 0 } : { height: 0, opacity: 0 }}
            transition={{ duration: 0.22, ease: ease.out }}
            className="overflow-hidden"
          >
            <div className="flex flex-col gap-3 px-4 pb-4">
              <AnimatePresence initial={false}>
                {rows.length > 0 && (
                  <Piece key="rows">
                    <SuggestionList rows={rows} applying={sugg.applying} onApplyAll={() => void sugg.applyAll()} onSkipAll={sugg.skipAll} />
                  </Piece>
                )}
                {sugg.busy && (
                  <Piece key="rows-busy">
                    <Lines widths={["70%", "52%"]} />
                  </Piece>
                )}
                {aiOn && summary.stored && (
                  <Piece key="summary">
                    <SummaryView s={summary.stored} busy={summary.busy} onRefresh={agent ? () => void summary.summarize() : undefined} />
                  </Piece>
                )}
                {aiOn && !summary.stored && summary.busy && (
                  <Piece key="summary-busy">
                    <Lines widths={["100%", "88%", "62%"]} />
                  </Piece>
                )}
                {(canSummarize || canSuggest) && (
                  <Piece key="actions">
                    <div className="flex flex-wrap gap-1.5">
                      {canSummarize && (
                        <AiTooltip feature="summarize" label={t("summary.hint", { name })}>
                          <Button size="xs" variant="secondary" onClick={() => void summary.summarize()} aria-label={[t("summary.generate"), cost("summarize")].filter(Boolean).join(", ")}>
                            {t("summary.generate")}
                          </Button>
                        </AiTooltip>
                      )}
                      {canSuggest && (
                        <AiTooltip feature="triage" label={t("suggest.hint", { name })}>
                          <Button size="xs" variant="secondary" onClick={() => void sugg.suggest()} aria-label={[t("suggest.run"), cost("triage")].filter(Boolean).join(", ")}>
                            {t("suggest.run")}
                          </Button>
                        </AiTooltip>
                      )}
                    </div>
                  </Piece>
                )}
                {starters.length > 0 && (
                  <Piece key="tools">
                    <Starters starters={starters} />
                  </Piece>
                )}
              </AnimatePresence>
              {aiOn && <Ask t={ticket} run={voice.run} />}
            </div>
          </motion.div>
        )}
      </AnimatePresence>
    </section>
  );
}
