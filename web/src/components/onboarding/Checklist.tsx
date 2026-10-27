// The onboarding checklist (GET /v1/onboarding): a dismissible card with a
// progress bar and one row per step, plus a compact sidebar entry that opens
// it in a popover. Prop-driven: pass the API's Onboarding as `data`.

import { useRouter } from "@tanstack/react-router";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { BookOpen, Bot, Camera, ChevronRight, Mail, MessageSquare, Shuffle, Sparkles, Tag, UserPlus, X, type LucideIcon } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { AnimatedNumber } from "@/components/motion";
import { Button } from "@/components/ui/button";
import { Popover } from "@/components/ui/popover";
import { Tooltip } from "@/components/ui/tooltip";
import type { Onboarding, OnboardingStep, OnboardingStepId } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { ease, springs } from "@/lib/motion";
import { AI_TONE, tone } from "@/lib/tones";

export type { Onboarding, OnboardingStep, OnboardingStepId };

export const STEP_ICONS: Record<OnboardingStepId, LucideIcon> = {
  connect_mailbox: Mail,
  invite_teammate: UserPlus,
  upload_photo: Camera,
  install_widget: MessageSquare,
  publish_article: BookOpen,
  setup_triage: Shuffle,
  create_label: Tag,
  try_ollie: Sparkles,
  setup_ai_agent: Bot,
};

/** Done and total from the steps themselves, so an optimistic flip moves the bar at once. */
export function progressOf(data: Pick<Onboarding, "steps">) {
  const total = data.steps.length;
  const done = data.steps.filter((s) => s.done).length;
  return { done, total, pct: total ? done / total : 0, all: total > 0 && done === total };
}

/**
 * The steps still open that this viewer can do: an admin step is left out for a member,
 * and so is one waiting on AI when only an admin can turn AI on. None left, the checklist
 * has nothing to offer them and leaves the sidebar.
 */
export function stepsLeft(data: Pick<Onboarding, "steps">, viewer: { isAdmin: boolean; unavailable?: Partial<Record<OnboardingStepId, string>> }): number {
  return data.steps.filter((s) => !s.done && (viewer.isAdmin || (s.requires !== "admin" && !viewer.unavailable?.[s.id]))).length;
}

type Props = {
  data: Onboarding;
  onDismiss: () => void | Promise<void>;
  /** The viewer can do admin steps. Steps that need an admin are shown disabled otherwise. */
  isAdmin?: boolean;
  /** Steps that can't be done yet, with a quiet reason (e.g. AI is not configured). */
  unavailable?: Partial<Record<OnboardingStepId, string>>;
  /** Run instead of navigating to the step's href (open a dialog, start a tour). */
  onStep?: (step: OnboardingStep) => void;
  className?: string;
  /** Without the card frame, for use inside a popover. */
  bare?: boolean;
};

export function Checklist({ data, onDismiss, isAdmin = true, unavailable, onStep, className, bare }: Props) {
  const { t } = useTranslation("setup");
  const reduce = useReducedMotion();
  const { done, total, pct, all } = progressOf(data);

  // When the last step flips while we watch, let its check draw, then celebrate once.
  const [finished, setFinished] = useState(all);
  const [celebrate, setCelebrate] = useState(false);
  useEffect(() => {
    if (!all) return setFinished(false);
    if (finished) return;
    const id = setTimeout(() => {
      setFinished(true);
      setCelebrate(!reduce);
    }, reduce ? 0 : 650);
    return () => clearTimeout(id);
  }, [all, finished, reduce]);

  if (data.dismissed) return null;
  const firstOpen = data.steps.find((s) => !s.done && !blockedReason(s, isAdmin, unavailable, t));
  const [pre, post] = t("checklist.progress", { done: "\u0001", total }).split("\u0001");

  return (
    <section
      aria-label={t("checklist.title")}
      className={cn(!bare && "overflow-hidden rounded-lg border border-line bg-surface shadow-xs", className)}
    >
      <header className="flex items-center gap-3 px-4 pb-3 pt-3.5">
        <h2 className="text-[13px] font-semibold tracking-[-0.01em] text-fg">{t("checklist.title")}</h2>
        <span className="text-[12px] tabular-nums text-fg-3" aria-label={t("checklist.progress_label", { done, total })}>
          <span aria-hidden>
            {pre}
            <AnimatedNumber value={done} />
            {post}
          </span>
        </span>
        {/* In a popover the button takes focus on open; a tooltip there would pop up unasked. */}
        <Tooltip content={bare ? null : t("checklist.dismiss")}>
          <Button variant="ghost" size="xs" square aria-label={t("checklist.dismiss")} onClick={() => void onDismiss()} className="-me-1.5 ms-auto text-fg-4">
            <X className="size-3.5" />
          </Button>
        </Tooltip>
      </header>
      <div className="px-4 pb-1">
        <div className="h-1 overflow-hidden rounded-xs bg-hover" role="progressbar" aria-valuemin={0} aria-valuemax={total} aria-valuenow={done} aria-label={t("checklist.title")}>
          <motion.div
            className="h-full rounded-xs bg-tone-green-solid"
            initial={false}
            animate={{ width: `${pct * 100}%` }}
            transition={reduce ? { duration: 0 } : springs.glide}
          />
        </div>
      </div>
      <AnimatePresence mode="wait" initial={false}>
        {finished ? (
          <motion.div
            key="done"
            initial={reduce ? { opacity: 0 } : { opacity: 0, y: 6 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ duration: 0.22, ease: ease.out }}
          >
            <AllSet celebrate={celebrate} onDismiss={onDismiss} />
          </motion.div>
        ) : (
          <motion.ul key="steps" className="px-2 pb-2 pt-2" exit={{ opacity: 0, transition: { duration: 0.12 } }}>
            {data.steps.map((s) => (
              <StepRow
                key={s.id}
                step={s}
                focus={s.id === firstOpen?.id}
                blocked={blockedReason(s, isAdmin, unavailable, t)}
                onStep={onStep}
              />
            ))}
          </motion.ul>
        )}
      </AnimatePresence>
    </section>
  );
}

function blockedReason(s: OnboardingStep, isAdmin: boolean, unavailable: Props["unavailable"], t: (k: "checklist.needs_admin") => string): string | null {
  if (s.done) return null;
  if (unavailable?.[s.id]) return unavailable[s.id]!;
  if (s.requires === "admin" && !isAdmin) return t("checklist.needs_admin");
  return null;
}

function StepRow({ step, focus, blocked, onStep }: { step: OnboardingStep; focus: boolean; blocked: string | null; onStep?: (s: OnboardingStep) => void }) {
  const router = useRouter();
  const { t } = useTranslation("setup");
  const reduce = useReducedMotion();
  const Icon = STEP_ICONS[step.id] ?? Sparkles;
  const ai = step.id === "try_ollie" || step.id === "setup_ai_agent";
  const interactive = !step.done && !blocked;
  const go = () => (onStep ? onStep(step) : router.history.push(step.href));

  const content = (
    <>
      <Check done={step.done} />
      <span className="min-w-0 flex-1">
        <span className="inline-flex max-w-full items-center gap-1.5">
          <Icon className={cn("size-3.5 shrink-0 transition-colors duration-200", step.done ? "text-fg-4" : ai ? tone[AI_TONE].text : "text-fg-3")} />
          <span className="relative min-w-0">
          <span className={cn("block truncate text-[13px] transition-colors duration-200", step.done ? "text-fg-3" : blocked ? "text-fg-3" : "font-medium text-fg")}>{step.title}</span>
          {/* A soft strike that draws across when the step is done. */}
          <motion.span
            aria-hidden
            className="pointer-events-none absolute inset-x-0 top-1/2 h-px origin-left bg-fg-4 rtl:origin-right"
            initial={false}
            animate={{ scaleX: step.done ? 1 : 0, opacity: step.done ? 0.7 : 0 }}
            transition={reduce ? { duration: 0 } : { duration: 0.25, ease: ease.out, delay: step.done ? 0.12 : 0 }}
          />
          </span>
        </span>
        {blocked ? (
          <span className="mt-0.5 block text-[12px] leading-4 text-fg-4">{blocked}</span>
        ) : (
          focus &&
          step.description && <span className="mt-0.5 block text-[12px] leading-4 text-fg-3">{step.description}</span>
        )}
      </span>
      <span className="sr-only">{step.done ? t("checklist.step_done") : t("checklist.step_todo")}</span>
      {interactive && <ChevronRight className="size-3.5 shrink-0 text-fg-4 opacity-0 transition-opacity duration-150 group-hover:opacity-100 group-focus-visible:opacity-100 rtl:-scale-x-100" />}
    </>
  );

  const row = "group flex w-full items-start gap-2.5 rounded-md px-2 py-2 text-start";
  return (
    <li>
      {interactive ? (
        <a
          href={step.href}
          onClick={(e) => {
            if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
            e.preventDefault();
            go();
          }}
          className={cn(row, "outline-none transition-colors duration-150 hover:bg-hover focus-visible:bg-hover focus-visible:shadow-[0_0_0_2px_var(--focus)]")}
        >
          {content}
        </a>
      ) : (
        <div className={cn(row, blocked && "opacity-80")} aria-disabled={blocked ? true : undefined}>
          {content}
        </div>
      )}
    </li>
  );
}

/** The step's box: a hairline square that fills green and draws its check when done. */
function Check({ done }: { done: boolean }) {
  const reduce = useReducedMotion();
  return (
    <motion.span
      aria-hidden
      className={cn(
        "mt-px flex size-4 shrink-0 items-center justify-center rounded-sm border transition-colors duration-200",
        done ? "border-transparent bg-tone-green-solid text-white" : "border-line-strong bg-surface",
      )}
      initial={false}
      animate={reduce ? undefined : { scale: done ? [1, 1.15, 1] : 1 }}
      transition={{ duration: 0.25, ease: ease.out }}
    >
      <svg viewBox="0 0 16 16" className="size-3" fill="none" stroke="currentColor" strokeWidth={2.2} strokeLinecap="round" strokeLinejoin="round">
        <motion.path
          d="M3.5 8.4l2.9 2.9L12.5 5"
          initial={false}
          animate={{ pathLength: done ? 1 : 0, opacity: done ? 1 : 0 }}
          transition={reduce ? { duration: 0 } : { pathLength: { duration: 0.25, ease: ease.out, delay: 0.05 }, opacity: { duration: 0.05 } }}
        />
      </svg>
    </motion.span>
  );
}

// The mark's four modules, lit one after another: the one-shot celebration.
const TILES = [
  { x: 0, y: 0, circle: false, hue: "green" },
  { x: 18, y: 0, circle: true, hue: "blue" },
  { x: 0, y: 18, circle: false, hue: "violet" },
  { x: 18, y: 18, circle: false, hue: "amber" },
] as const;

function AllSet({ celebrate, onDismiss }: { celebrate: boolean; onDismiss: () => void | Promise<void> }) {
  const { t } = useTranslation("setup");
  return (
    <div className="flex flex-col items-center px-4 pb-5 pt-5 text-center">
      <svg viewBox="0 0 34 34" className="size-10" aria-hidden>
        {TILES.map((c, i) => {
          const cls = tone[c.hue].fill;
          const anim = celebrate
            ? { initial: { scale: 0.4, opacity: 0 }, animate: { scale: 1, opacity: 1 }, transition: { ...springs.snap, delay: 0.05 + i * 0.07 } }
            : {};
          return c.circle ? (
            <motion.circle key={i} cx={c.x + 8} cy={c.y + 8} r={8} className={cls} style={{ transformBox: "fill-box", transformOrigin: "center" }} {...anim} />
          ) : (
            <motion.rect key={i} x={c.x} y={c.y} width={16} height={16} rx={3} className={cls} style={{ transformBox: "fill-box", transformOrigin: "center" }} {...anim} />
          );
        })}
      </svg>
      <Line celebrate={celebrate} delay={0.3}>
        <p className="mt-3 text-[13.5px] font-semibold tracking-[-0.01em] text-fg">{t("checklist.done_title")}</p>
      </Line>
      <Line celebrate={celebrate} delay={0.36}>
        <p className="mt-0.5 text-[12.5px] leading-5 text-fg-3">{t("checklist.done_body")}</p>
      </Line>
      <Button size="sm" className="mt-3" onClick={() => void onDismiss()}>
        {t("checklist.dismiss")}
      </Button>
    </div>
  );
}

function Line({ celebrate, delay, children }: { celebrate: boolean; delay: number; children: ReactNode }) {
  if (!celebrate) return <>{children}</>;
  return (
    <motion.div initial={{ opacity: 0, y: 4 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.22, delay, ease: ease.out }}>
      {children}
    </motion.div>
  );
}

/** A small ring that fills with progress; turns into a full green ring when done. */
export function ProgressRing({ value, className }: { value: number; className?: string }) {
  const reduce = useReducedMotion();
  return (
    <svg viewBox="0 0 16 16" className={cn("size-4 shrink-0 -rotate-90", className)} fill="none" aria-hidden>
      <circle cx="8" cy="8" r="6" className="stroke-line-strong" strokeWidth={2} />
      <motion.circle
        cx="8"
        cy="8"
        r="6"
        className="stroke-tone-green-solid"
        strokeWidth={2}
        strokeLinecap="round"
        pathLength={1}
        strokeDasharray="1 1"
        initial={false}
        animate={{ strokeDashoffset: 1 - Math.min(1, Math.max(0, value)) }}
        transition={reduce ? { duration: 0 } : springs.glide}
      />
    </svg>
  );
}

/** The sidebar entry: ring + count; opens the checklist in a popover. `collapsed` for the icon rail. */
export function ChecklistMini({ collapsed, triggerClassName, ...props }: Props & { collapsed?: boolean; triggerClassName?: string }) {
  const { t } = useTranslation("setup");
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const { done, total, pct } = progressOf(props.data);
  if (props.data.dismissed) return null;

  const trigger = (
    <button
      type="button"
      aria-label={collapsed ? `${t("checklist.open")}, ${t("checklist.progress_label", { done, total })}` : t("checklist.open")}
      className={cn(
        "flex h-8 items-center gap-2 rounded-md text-[12.5px] text-fg-2 outline-none transition-colors duration-150 hover:bg-hover hover:text-fg focus-visible:shadow-[0_0_0_2px_var(--focus)] data-[state=open]:bg-hover data-[state=open]:text-fg",
        collapsed ? "w-8 justify-center" : "w-full px-2",
        triggerClassName,
      )}
    >
      <ProgressRing value={pct} />
      {!collapsed && (
        <>
          <span className="flex-1 truncate text-start font-medium">{t("checklist.title")}</span>
          <span className="mono text-[11px] tabular-nums text-fg-4">
            <AnimatedNumber value={done} />/{total}
          </span>
        </>
      )}
    </button>
  );

  return (
    <Popover
      open={open}
      onOpenChange={setOpen}
      side="right"
      align="end"
      sideOffset={8}
      className="w-[340px] p-0"
      trigger={collapsed ? <Tooltip content={t("checklist.title")} side="right">{trigger}</Tooltip> : trigger}
    >
      <Checklist
        {...props}
        bare
        onStep={(s) => {
          setOpen(false);
          if (props.onStep) props.onStep(s);
          else router.history.push(s.href);
        }}
        onDismiss={async () => {
          setOpen(false);
          await props.onDismiss();
        }}
      />
    </Popover>
  );
}
