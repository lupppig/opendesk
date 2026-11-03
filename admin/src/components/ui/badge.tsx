import type { ReactNode } from "react";
import { i18n } from "@/i18n";
import { cn } from "@/lib/cn";
import { isTone, tone as tones, toneFor, type Tone } from "@/lib/tones";

export function Badge({
  children,
  className,
  tone = "neutral",
  dot,
}: {
  children: ReactNode;
  className?: string;
  /** A meaning (ok, warn, danger), a style (neutral, solid, outline) or any palette tone. */
  tone?: "neutral" | "solid" | "outline" | "ok" | "warn" | "danger" | Tone;
  dot?: boolean;
}) {
  const semantic: Record<string, Tone> = { ok: "green", warn: "amber", danger: "red" };
  const hue: Tone | null = semantic[tone] ?? (isTone(tone) ? tone : null);
  const toneCls = hue
    ? tones[hue].chip
    : { neutral: "bg-hover text-fg-2", solid: "bg-inverse text-inverse-fg", outline: "border border-line text-fg-2" }[tone as "neutral" | "solid" | "outline"];
  const dotCls = hue ? tones[hue].solid : { neutral: "bg-fg-4", solid: "bg-inverse-fg", outline: "bg-fg-4" }[tone as "neutral" | "solid" | "outline"];
  return (
    <span className={cn("inline-flex h-[19px] shrink-0 items-center gap-1 whitespace-nowrap rounded-sm px-1.5 text-[10.5px] font-medium", toneCls, className)}>
      {(dot || tone === "ok" || tone === "warn" || tone === "danger") && <span className={cn("size-1.5 rounded-full", dotCls)} />}
      {children}
    </span>
  );
}

export function Dot({ tone, className, pulse }: { tone: "ok" | "warn" | "danger" | "muted" | "fg"; className?: string; pulse?: boolean }) {
  const c = { ok: tones.green.solid, warn: tones.amber.solid, danger: tones.red.solid, muted: "bg-fg-4", fg: "bg-fg" }[tone];
  return <span className={cn("inline-block size-1.5 shrink-0 rounded-full", c, pulse && "animate-pulse-soft", className)} aria-hidden />;
}

/** A tag in its colour; a tag without one gets a stable hue from its name. */
export function TagChip({ name, color, onRemove, className }: { name: string; color?: string | null; onRemove?: () => void; className?: string }) {
  const hue = isTone(color) ? color : toneFor(name);
  return (
    <span className={cn("inline-flex h-[19px] max-w-[140px] shrink-0 items-center gap-1 rounded-sm pl-1.5 text-[10.5px] font-medium", tones[hue].chip, onRemove ? "pr-0.5" : "pr-1.5", className)}>
      <span className={cn("size-1.5 shrink-0 rounded-[2px]", tones[hue].solid)} />
      <span className="truncate">{name}</span>
      {onRemove && (
        <button
          type="button"
          aria-label={i18n.t("ui.remove", { name })}
          onClick={(e) => {
            e.stopPropagation();
            onRemove();
          }}
          className="flex size-3.5 items-center justify-center rounded-xs opacity-60 hover:bg-black/5 hover:opacity-100 dark:hover:bg-white/10"
        >
          <svg viewBox="0 0 8 8" className="size-2" aria-hidden>
            <path d="M1.5 1.5l5 5M6.5 1.5l-5 5" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" />
          </svg>
        </button>
      )}
    </span>
  );
}
