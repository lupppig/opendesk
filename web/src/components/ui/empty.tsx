// Empty states: a designed moment, not a white void. The page keeps its shape
// with a faint ghost of its real layout, and a centre card carries an
// illustration built from the mark's modules, a title, a helpful line, up to
// two actions and a few quiet tips. The art snaps together on mount and one
// module breathes; reduced motion shows it still.

import { motion, useReducedMotion } from "motion/react";
import {
  BarChart3,
  BookOpen,
  CircleCheck,
  Clock,
  Inbox,
  Layers,
  NotebookText,
  Search,
  Settings2,
  ShieldCheck,
  Sparkles,
  UserRound,
  Users,
  type LucideIcon,
} from "lucide-react";
import type { ReactNode } from "react";
import { Kbd } from "@/components/ui/kbd";
import { cn } from "@/lib/cn";
import { springs } from "@/lib/motion";
import { tone, toneVar, type Tone } from "@/lib/tones";

// ─── The art ──────────────────────────────────────────────────────────────

export type ArtKind =
  | "inbox"
  | "mine"
  | "pending"
  | "solved"
  | "spam"
  | "views"
  | "contacts"
  | "kb"
  | "wiki"
  | "reports"
  | "search"
  | "settings"
  | "ai"
  | "generic";

const ART: Record<ArtKind, { icon: LucideIcon; hue: Tone; lit: 0 | 1 | 2 | 3 }> = {
  inbox: { icon: Inbox, hue: "blue", lit: 1 },
  mine: { icon: UserRound, hue: "indigo", lit: 0 },
  pending: { icon: Clock, hue: "amber", lit: 3 },
  solved: { icon: CircleCheck, hue: "green", lit: 1 },
  spam: { icon: ShieldCheck, hue: "red", lit: 2 },
  views: { icon: Layers, hue: "violet", lit: 0 },
  contacts: { icon: Users, hue: "teal", lit: 1 },
  kb: { icon: BookOpen, hue: "orange", lit: 2 },
  wiki: { icon: NotebookText, hue: "teal", lit: 0 },
  reports: { icon: BarChart3, hue: "cyan", lit: 3 },
  search: { icon: Search, hue: "gray", lit: 0 },
  settings: { icon: Settings2, hue: "gray", lit: 1 },
  ai: { icon: Sparkles, hue: "violet", lit: 1 },
  generic: { icon: Inbox, hue: "gray", lit: 1 },
};

/**
 * The 2x2 module mark as an illustration: three quiet tiles and one lit tile in
 * the page's hue carrying its icon. The second module is the mark's circle.
 */
export function EmptyArt({ kind = "generic", size = 72, className }: { kind?: ArtKind; size?: number; className?: string }) {
  const reduce = useReducedMotion();
  const a = ART[kind];
  const Icon = a.icon;
  const cell = (size - 6) / 2;
  const cells = [0, 1, 2, 3];
  return (
    <div className={cn("relative grid grid-cols-2 gap-1.5", className)} style={{ width: size, height: size }} aria-hidden>
      {cells.map((i) => {
        const lit = i === a.lit;
        const round = i === 1;
        return (
          <motion.span
            key={i}
            initial={reduce ? false : { opacity: 0, scale: 0.6, y: 4 }}
            animate={
              lit && !reduce
                ? { opacity: 1, scale: [1, 1.04, 1], y: 0, transition: { scale: { duration: 2.8, repeat: Infinity, ease: "easeInOut", delay: 0.6 }, default: { ...springs.snap, delay: 0.05 + i * 0.06 } } }
                : { opacity: 1, scale: 1, y: 0, transition: { ...springs.snap, delay: 0.05 + i * 0.06 } }
            }
            className={cn(
              "flex items-center justify-center",
              round ? "rounded-full" : "rounded-md",
              lit
                ? tone[a.hue].soft
                : "flex-col gap-[3px] border border-line-strong bg-surface shadow-[0_1px_2px_rgb(0_0_0/0.06)]",
            )}
            style={{
              width: cell,
              height: cell,
              boxShadow: lit ? `inset 0 0 0 1px color-mix(in oklab, ${toneVar(a.hue)} 34%, transparent), 0 1px 2px rgb(0 0 0 / 0.06)` : undefined,
            }}
          >
            {lit ? (
              <Icon style={{ width: cell * 0.46, height: cell * 0.46, color: toneVar(a.hue) }} strokeWidth={2} />
            ) : round ? (
              <span className="rounded-full bg-line-strong" style={{ width: cell * 0.3, height: cell * 0.3 }} />
            ) : (
              <>
                <span className="h-[3px] rounded-full bg-line-strong" style={{ width: cell * 0.5 }} />
                <span className="h-[3px] rounded-full bg-line" style={{ width: cell * 0.34 }} />
              </>
            )}
          </motion.span>
        );
      })}
    </div>
  );
}

/** Kept for older call sites: the module mark with one lit tile, no icon. */
export function ModuleArt({ lit = 1, className }: { lit?: 0 | 1 | 2 | 3; className?: string }) {
  const kinds: ArtKind[] = ["mine", "solved", "kb", "pending"];
  return <EmptyArt kind={kinds[lit]} size={56} className={className} />;
}

// ─── Ghost structure ──────────────────────────────────────────────────────

const widths = [62, 48, 74, 55, 68, 44, 58, 70, 50, 64];

/** Faint placeholder rows in the list's real shape, fading out downward. Static. */
export function GhostList({ rows = 9, variant = "tickets", className }: { rows?: number; variant?: "tickets" | "people" | "table"; className?: string }) {
  return (
    <div
      aria-hidden
      className={cn("pointer-events-none absolute inset-0 select-none overflow-hidden opacity-[0.55] [mask-image:linear-gradient(to_bottom,black_0%,transparent_85%)]", className)}
    >
      {Array.from({ length: rows }).map((_, i) => (
        <div key={i} className="flex h-11 items-center gap-3 border-b border-line-soft px-4">
          {variant === "tickets" && <span className="size-3 shrink-0 rounded-full border-[1.5px] border-line-strong" />}
          <span className={cn("shrink-0 bg-hover", variant === "people" ? "size-6 rounded-sm" : "size-5 rounded-sm")} />
          <span className="h-2 w-24 shrink-0 rounded-sm bg-active" />
          <span className="h-2 rounded-sm bg-hover" style={{ width: `${widths[i % widths.length] * 0.6}%` }} />
          <span className="ml-auto h-3.5 w-12 shrink-0 rounded-sm bg-hover" />
          <span className="h-2 w-8 shrink-0 rounded-sm bg-hover" />
        </div>
      ))}
    </div>
  );
}

/** Faint placeholder cards for grid pages (KB collections, reports). */
export function GhostGrid({ cards = 6, className }: { cards?: number; className?: string }) {
  return (
    <div
      aria-hidden
      className={cn(
        "pointer-events-none absolute inset-0 grid select-none auto-rows-[120px] grid-cols-2 gap-3 overflow-hidden p-6 opacity-[0.55] [mask-image:linear-gradient(to_bottom,black_0%,transparent_80%)] lg:grid-cols-3",
        className,
      )}
    >
      {Array.from({ length: cards }).map((_, i) => (
        <div key={i} className="flex flex-col gap-2.5 rounded-lg border border-line-soft p-4">
          <span className="size-6 rounded-md bg-hover" />
          <span className="h-2 w-2/3 rounded-sm bg-active" />
          <span className="h-2 rounded-sm bg-hover" style={{ width: `${widths[i] }%` }} />
        </div>
      ))}
    </div>
  );
}

// ─── The empty state ──────────────────────────────────────────────────────

export type EmptyTip = { label: ReactNode; kbd?: string; icon?: ReactNode };

export function EmptyState({
  title,
  description,
  action,
  secondary,
  tips,
  art,
  ghost,
  children,
  icon,
  lit,
  className,
  compact,
}: {
  title: ReactNode;
  description?: ReactNode;
  /** The primary action (a Button). */
  action?: ReactNode;
  /** A quieter second action. */
  secondary?: ReactNode;
  /** Two or three quiet hints under the actions. */
  tips?: EmptyTip[];
  /** Which illustration; defaults from `lit` for older call sites. */
  art?: ArtKind;
  /** The page's ghost layout, drawn behind (e.g. <GhostList />). Full states only. */
  ghost?: ReactNode;
  /** Extra context under the description (status lines, small stats). */
  children?: ReactNode;
  icon?: ReactNode;
  lit?: 0 | 1 | 2 | 3;
  className?: string;
  compact?: boolean;
}) {
  const reduce = useReducedMotion();
  const kinds: ArtKind[] = ["mine", "solved", "kb", "pending"];
  const kind: ArtKind = art ?? (lit !== undefined ? kinds[lit] : "generic");

  if (compact)
    return (
      <div className={cn("flex flex-col items-center justify-center gap-2.5 px-4 py-8 text-center", className)}>
        {icon ?? <EmptyArt kind={kind} size={40} />}
        <div className="flex max-w-[320px] flex-col gap-0.5">
          <p className="text-[13px] font-medium text-fg">{title}</p>
          {description && <p className="text-[12px] leading-[18px] text-fg-3">{description}</p>}
        </div>
        {children}
        {(action || secondary) && (
          <div className="mt-0.5 flex items-center gap-2">
            {action}
            {secondary}
          </div>
        )}
      </div>
    );

  const rise = (i: number) =>
    reduce ? {} : { initial: { opacity: 0, y: 6 }, animate: { opacity: 1, y: 0 }, transition: { ...springs.row, delay: 0.12 + i * 0.05 } };

  return (
    <div className={cn("relative flex min-h-full flex-1 flex-col items-center justify-center overflow-hidden px-6 py-16 text-center", className)}>
      {ghost}
      <div className="relative flex max-w-[420px] flex-col items-center">
        {/* A soft plate lifts the card off the ghost rows without drawing a box. */}
        <div aria-hidden className="absolute -inset-x-16 -inset-y-12 -z-10 bg-[radial-gradient(closest-side,var(--surface)_62%,transparent)]" />
        {icon ?? <EmptyArt kind={kind} />}
        <motion.h2 {...rise(0)} className="mt-5 text-[15px] font-semibold tracking-[-0.015em] text-fg">
          {title}
        </motion.h2>
        {description && (
          <motion.p {...rise(1)} className="mt-1.5 text-[12.5px] leading-5 text-fg-3">
            {description}
          </motion.p>
        )}
        {children && (
          <motion.div {...rise(2)} className="mt-4 w-full">
            {children}
          </motion.div>
        )}
        {(action || secondary) && (
          <motion.div {...rise(3)} className="mt-5 flex flex-wrap items-center justify-center gap-2">
            {action}
            {secondary}
          </motion.div>
        )}
        {tips?.length ? (
          <motion.ul {...rise(4)} className="mt-6 flex flex-wrap items-center justify-center gap-x-4 gap-y-1.5 text-[11.5px] text-fg-4">
            {tips.map((t, i) => (
              <li key={i} className="inline-flex items-center gap-1.5">
                {t.icon}
                {t.kbd && <Kbd>{t.kbd}</Kbd>}
                <span>{t.label}</span>
              </li>
            ))}
          </motion.ul>
        ) : null}
      </div>
    </div>
  );
}

/** A small status line for empty-state context: a tinted dot, a label and an optional link. */
export function EmptyFact({ hue = "green", children }: { hue?: Tone; children: ReactNode }) {
  return (
    <span className="inline-flex items-center gap-1.5 rounded-sm bg-sunken px-2 py-1 text-[11.5px] text-fg-2 shadow-[inset_0_0_0_1px_var(--line-soft)]">
      <span className={cn("size-1.5 rounded-full", tone[hue].solid)} />
      {children}
    </span>
  );
}
