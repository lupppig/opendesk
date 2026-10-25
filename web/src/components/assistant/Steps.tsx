// Tool activity rows ("Searching tickets…" turning into a check) and the
// approval card that shows exactly what a change will do before it runs.

import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { ArrowRight, ArrowUpRight, Ban, Check, ChevronRight, Clock, Send, X } from "lucide-react";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { Kbd } from "@/components/ui/kbd";
import { cn } from "@/lib/cn";
import { tone } from "@/lib/tones";
import { Avatar } from "@/components/ui/avatar";
import { peerOutcome } from "@/lib/assistantPeer";
import type { Approval, AssistantRef, AssistantStep, Preview, StepPeer } from "./api";
import { OllieMark } from "./OllieMark";
import { Markdown } from "./Markdown";
import { openRef } from "./refs";
import { useAssistantName } from "./name";

const EASE = [0.23, 1, 0.32, 1] as const;

function StepIcon({ status }: { status: AssistantStep["status"] }) {
  const reduce = useReducedMotion();
  return (
    <span className="relative flex size-4 shrink-0 items-center justify-center">
      <AnimatePresence initial={false} mode="popLayout">
        <motion.span
          key={status}
          initial={reduce ? false : { opacity: 0, scale: 0.6 }}
          animate={{ opacity: 1, scale: 1 }}
          exit={reduce ? { opacity: 0 } : { opacity: 0, scale: 0.6 }}
          transition={{ type: "spring", stiffness: 520, damping: 30 }}
          className="flex"
        >
          {status === "running" || status === "approved" ? (
            <OllieMark variant="spark" size={13} state="thinking" />
          ) : status === "done" ? (
            <span className={cn("flex size-4 items-center justify-center rounded-[5px] text-white", tone.green.solid)}>
              <Check className="size-2.5" strokeWidth={3.5} />
            </span>
          ) : status === "error" ? (
            <span className={cn("flex size-4 items-center justify-center rounded-[5px] text-white", tone.red.solid)}>
              <X className="size-2.5" strokeWidth={3.5} />
            </span>
          ) : status === "awaiting_approval" ? (
            <OllieMark variant="spark" size={13} state="attention" />
          ) : status === "denied" ? (
            <Ban className="size-3.5 text-fg-4" />
          ) : (
            <Clock className="size-3.5 text-fg-4" />
          )}
        </motion.span>
      </AnimatePresence>
    </span>
  );
}

const chip =
  "inline-flex h-5 max-w-[200px] items-center gap-1 rounded-[5px] border border-line bg-surface px-1.5 text-[11px] text-fg-2 transition-colors enabled:hover:border-line-strong enabled:hover:text-fg disabled:cursor-default";

/** Only https links from a connected product open, in a new tab. */
export function externalHref(r: AssistantRef): string | null {
  if (r.kind !== "external") return null;
  try {
    const u = new URL(r.id);
    return u.protocol === "https:" ? u.href : null;
  } catch {
    return null;
  }
}

export function RefChip({ r }: { r: AssistantRef }) {
  const href = externalHref(r);
  if (r.kind === "external") {
    if (!href) return null;
    return (
      <a href={href} target="_blank" rel="noopener noreferrer" title={r.id} className={cn(chip, "hover:border-line-strong hover:text-fg")}>
        <span className="truncate">{r.label || new URL(href).host}</span>
        <ArrowUpRight className="size-3 shrink-0 text-fg-4" aria-hidden />
      </a>
    );
  }
  const clickable = r.kind !== "member";
  return (
    <button
      type="button"
      disabled={!clickable}
      onClick={() => clickable && void openRef(r)}
      title={r.label}
      className={chip}
    >
      {r.kind === "ticket" && r.number ? <span className="mono font-medium text-tone-violet">#{r.number}</span> : null}
      <span className="truncate">{r.label || r.kind}</span>
    </button>
  );
}

/** ask_teammate: the teammate whose Ollie answered, and how ("Asked Priya's Ollie · answered from 3 conversations"). */
function PeerStepRow({ step, peer }: { step: AssistantStep; peer: StepPeer }) {
  const { t } = useTranslation("assistant");
  const assistant = useAssistantName();
  const reduce = useReducedMotion();
  const working = step.status === "running";
  const name = peer.user.name || step.label;
  // The first name keeps the row on one line in the panel; the full name is its title.
  const first = name.split(/\s+/)[0] || name;
  const outcome = peerOutcome(peer.result, peer.sources);
  const summary = step.status === "error" ? step.summary : t(`peer.${outcome.key}`, { count: outcome.count });
  const shared = peer.result === "answered";
  return (
    <motion.div
      initial={reduce ? false : { opacity: 0, y: 3 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.22, ease: EASE }}
      className="flex min-h-6 flex-wrap items-center gap-x-2 gap-y-0.5 text-[12px]"
      data-step={step.status}
      data-peer={peer.result}
      title={name}
    >
      <StepIcon status={step.status} />
      <Avatar name={name} src={peer.user.avatar_url} size="xs" />
      <span className={cn("min-w-0", working ? "text-fg-2" : "text-fg-3")}>
        {t(working ? "peer.asking" : "peer.asked", { name: first, assistant })}
        {working && "…"}
      </span>
      {!working && summary && (
        <span className={cn("min-w-0", step.status === "error" ? "text-danger-fg" : shared ? "text-fg-4" : tone.amber.text)}>· {summary}</span>
      )}
    </motion.div>
  );
}

export function StepRow({ step }: { step: AssistantStep }) {
  if (step.peer) return <PeerStepRow step={step} peer={step.peer} />;
  return <ToolStepRow step={step} />;
}

function ToolStepRow({ step }: { step: AssistantStep }) {
  const { t } = useTranslation("assistant");
  const reduce = useReducedMotion();
  const working = step.status === "running";
  const refs = (step.refs ?? []).filter((r) => r.kind !== "member").slice(0, 4);
  return (
    <motion.div
      initial={reduce ? false : { opacity: 0, y: 3 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.22, ease: EASE }}
      className="flex flex-col gap-1"
      data-step={step.status}
    >
      <div className="flex min-h-6 items-center gap-2 text-[12px]">
        <StepIcon status={step.status} />
        <span className={cn("min-w-0 truncate", working ? "text-fg-2" : "text-fg-3")}>
          {step.label}
          {working && "…"}
        </span>
        {step.summary && !working && (
          <span className={cn("min-w-0 truncate text-fg-4", step.status === "error" && "text-danger-fg")}>· {step.summary}</span>
        )}
        {(step.status === "denied" || step.status === "expired") && <span className="shrink-0 text-fg-4">· {t(`status.${step.status}`)}</span>}
        {step.auto && !working && (
          <span className={cn("inline-flex h-[18px] shrink-0 items-center rounded-sm px-1.5 text-[10.5px] font-medium", tone.violet.chip)} data-auto>
            {t("step.auto")}
          </span>
        )}
      </div>
      {step.auto && step.preview && !working && <RanDetails preview={step.preview} />}
      {refs.length > 0 && step.status === "done" && (
        <div className="flex flex-wrap gap-1 pl-6">
          {refs.map((r) => (
            <RefChip key={`${r.kind}-${r.id}`} r={r} />
          ))}
        </div>
      )}
    </motion.div>
  );
}

/** What a change that ran without asking ran: its fields, text and arguments, behind a disclosure. */
function RanDetails({ preview: p }: { preview: Preview }) {
  const { t } = useTranslation("assistant");
  const [open, setOpen] = useState(false);
  const id = useId();
  return (
    <div className="pl-6">
      <button
        type="button"
        aria-expanded={open}
        aria-controls={id}
        onClick={() => setOpen((v) => !v)}
        className={cn("inline-flex h-5 items-center gap-1 rounded-[5px] px-1 text-[11px] text-fg-3 transition-colors hover:text-fg", focusRing)}
      >
        <ChevronRight className={cn("size-3 transition-transform duration-150 motion-reduce:transition-none", open && "rotate-90")} aria-hidden />
        {open ? t("step.hideRan") : t("step.showRan")}
      </button>
      {open && (
        <div id={id} className="mt-1 flex flex-col gap-1.5">
          <p className="text-[12px] font-medium text-fg-2 wrap-anywhere">{p.title}</p>
          {p.changes && p.changes.length > 0 && (
            <div className="overflow-hidden rounded-lg border border-line bg-canvas">
              {p.changes.map((c, i) => (
                <ChangeRow key={i} field={c.field} from={c.from} to={c.to} />
              ))}
            </div>
          )}
          {p.body && (
            <div className="max-h-40 overflow-y-auto rounded-lg border border-line bg-canvas px-3 py-2">
              <Markdown text={p.body} className="text-fg" />
            </div>
          )}
          {p.arguments && (
            <pre
              tabIndex={0}
              aria-label={t("approval.arguments")}
              className={cn("max-h-48 overflow-auto whitespace-pre-wrap rounded-lg border border-line bg-canvas px-3 py-2 font-mono text-[11.5px] leading-[17px] text-fg-2 wrap-anywhere", focusRing)}
              data-ph-mask
            >
              {p.arguments}
            </pre>
          )}
        </div>
      )}
    </div>
  );
}

/** A value this long (a signature, a template) is shown as a block, not a pill. */
const LONG_VALUE = 80;

function ChangeRow({ field, from, to }: { field: string; from: string; to: string }) {
  if (from.length > LONG_VALUE || to.length > LONG_VALUE || from.includes("\n") || to.includes("\n")) {
    const box = "mono max-h-32 overflow-y-auto whitespace-pre-wrap rounded-[5px] px-1.5 py-1 text-[11px] leading-4 wrap-anywhere";
    return (
      <div className="flex flex-col gap-1 border-t border-line-soft px-3 py-1.5 first:border-t-0">
        <span className="truncate text-[11.5px] text-fg-3">{field}</span>
        {from && <div className={cn(box, "line-through decoration-1", tone.red.soft, tone.red.text)}>{from}</div>}
        <div className={cn(box, tone.green.soft, tone.green.text)}>{to}</div>
      </div>
    );
  }
  return (
    <div className="grid grid-cols-[88px_minmax(0,1fr)] items-baseline gap-2 border-t border-line-soft px-3 py-1.5 first:border-t-0">
      <span className="truncate text-[11.5px] text-fg-3">{field}</span>
      <span className="flex min-w-0 flex-wrap items-center gap-1.5 text-[12px] wrap-anywhere">
        {from && (
          <>
            <span className={cn("min-w-0 rounded-[4px] px-1 line-through decoration-1", tone.red.soft, tone.red.text)}>{from}</span>
            <ArrowRight className="size-3 shrink-0 text-fg-4" aria-hidden />
          </>
        )}
        <span className={cn("min-w-0 rounded-[4px] px-1 font-medium", tone.green.soft, tone.green.text)}>{to}</span>
      </span>
    </div>
  );
}

const focusRing = "outline-none focus-visible:shadow-[0_0_0_1px_var(--surface),0_0_0_3px_var(--focus)]";

/**
 * What the summary above leaves out, and every argument the change runs with, exactly
 * as it will be sent, behind a disclosure.
 */
function Arguments({ preview: p }: { preview: Preview }) {
  const { t } = useTranslation("assistant");
  const [open, setOpen] = useState(false);
  const id = useId();
  const omitted = p.omitted ?? 0;
  if (!p.arguments && !omitted && !p.clipped) return null;
  return (
    <div className="mx-3 mb-2">
      <div className="flex flex-wrap items-center gap-1.5 text-[11px]">
        {omitted > 0 && <span className={cn("inline-flex h-5 items-center rounded-[5px] px-1.5 font-medium", tone.amber.chip)}>{t("approval.moreFields", { count: omitted })}</span>}
        {p.clipped && <span className={cn("inline-flex h-5 items-center rounded-[5px] px-1.5 font-medium", tone.amber.chip)}>{t("approval.clipped")}</span>}
        {p.arguments && (
          <button
            type="button"
            aria-expanded={open}
            aria-controls={id}
            onClick={() => setOpen((v) => !v)}
            className={cn("ml-auto inline-flex h-5 items-center gap-1 rounded-[5px] px-1 text-fg-3 transition-colors hover:text-fg", focusRing)}
          >
            <ChevronRight className={cn("size-3 transition-transform duration-150 motion-reduce:transition-none", open && "rotate-90")} aria-hidden />
            {open ? t("approval.hideArguments") : t("approval.showArguments")}
          </button>
        )}
      </div>
      {open && p.arguments && (
        <pre
          id={id}
          tabIndex={0}
          aria-label={t("approval.arguments")}
          className={cn("mt-1.5 max-h-64 overflow-auto whitespace-pre-wrap rounded-lg border border-line bg-canvas px-3 py-2 font-mono text-[11.5px] leading-[17px] text-fg-2 wrap-anywhere", focusRing)}
          data-ph-mask
        >
          {p.arguments}
        </pre>
      )}
    </div>
  );
}

export function ApprovalCard({ approval, busy, onDecide }: { approval: Approval; busy: boolean; onDecide: (d: "approve" | "deny") => void }) {
  const { t } = useTranslation("assistant");
  const name = useAssistantName();
  const reduce = useReducedMotion();
  const p = approval.preview;
  const targets = p.targets ?? [];
  const shown = targets.slice(0, 6);
  return (
    <motion.div
      role="group"
      aria-label={t("approval.title")}
      initial={reduce ? false : { opacity: 0, y: 6, scale: 0.98 }}
      animate={{ opacity: 1, y: 0, scale: 1 }}
      transition={{ type: "spring", stiffness: 420, damping: 32 }}
      className="shrink-0 overflow-hidden rounded-xl border border-[color-mix(in_srgb,var(--tone-amber-solid)_40%,var(--line))] bg-surface shadow-[0_10px_30px_-18px_rgb(0_0_0/0.35)]"
      data-approval={approval.action_id}
    >
      <div className="flex items-start gap-2.5 px-3 pb-2 pt-3">
        <OllieMark size={20} state="attention" />
        <div className="min-w-0 flex-1">
          <p className="text-[13px] font-semibold text-fg wrap-anywhere">{p.title}</p>
          {p.summary && <p className="mt-0.5 line-clamp-2 text-[12px] text-fg-3 wrap-anywhere">{p.summary}</p>}
        </div>
        {p.external && (
          <span className={cn("inline-flex h-5 shrink-0 items-center gap-1 rounded-[5px] px-1.5 text-[11px] font-medium", tone.amber.chip)}>
            <Send className="size-3" />
            {t("approval.external")}
          </span>
        )}
        {p.system && (
          <span className={cn("inline-flex h-5 max-w-[140px] shrink-0 items-center rounded-[5px] px-1.5 text-[11px] font-medium", tone.violet.chip)}>
            <span className="truncate">{t("approval.system", { system: p.system })}</span>
          </span>
        )}
      </div>
      {p.changes && p.changes.length > 0 && (
        <div className="mx-3 mb-2 overflow-hidden rounded-lg border border-line bg-canvas" aria-label={t("approval.field")}>
          {p.changes.map((c, i) => (
            <ChangeRow key={i} field={c.field} from={c.from} to={c.to} />
          ))}
        </div>
      )}
      {p.body && (
        <div className={cn("mx-3 mb-2 max-h-56 overflow-y-auto rounded-lg border px-3 py-2", tone.green.line, tone.green.soft)}>
          <Markdown text={p.body} className="text-fg" />
        </div>
      )}
      <Arguments key={approval.action_id} preview={p} />
      {targets.length > 1 && (
        <div className="mx-3 mb-2 flex flex-wrap items-center gap-1">
          {shown.map((r) => (
            <RefChip key={r.id} r={r} />
          ))}
          {targets.length > shown.length && <span className="text-[11px] text-fg-4">{t("approval.more", { count: targets.length - shown.length })}</span>}
          {p.total && p.total > targets.length ? (
            <span className="w-full text-[11px] text-fg-4">{t("approval.of", { shown: targets.length, total: p.total })}</span>
          ) : null}
        </div>
      )}
      <div className="flex items-center gap-1.5 border-t border-line-soft bg-canvas px-3 py-2">
        <span className="mr-auto text-[11px] text-fg-4">{t("approval.expires")}</span>
        <Button size="sm" variant="ghost" disabled={busy} onClick={() => onDecide("deny")} icon={<X />}>
          {t("approval.deny")}
        </Button>
        <Button size="sm" variant="primary" disabled={busy} onClick={() => onDecide("approve")} icon={<Check />} aria-label={`${t("approval.approve")} (${name})`}>
          {t("approval.approve")}
          <Kbd className="ml-1 hidden sm:inline-flex [&_kbd]:border-transparent [&_kbd]:bg-white/15 [&_kbd]:text-inverse-fg dark:[&_kbd]:bg-black/10">mod enter</Kbd>
        </Button>
      </div>
    </motion.div>
  );
}
