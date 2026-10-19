// The sidebar's row primitives: the row shapes, the gliding active pill, rail
// tooltips, counts and foldable sections. Shared by the ticket sections and the
// Collab mail sections, so every row lines up.

import { useTranslation } from "react-i18next";
import { motion, useReducedMotion } from "motion/react";
import { ChevronDown } from "lucide-react";
import { createContext, useContext, useState, type ReactNode } from "react";
import { AnimatedNumber } from "@/components/motion";
import { Tooltip } from "@/components/ui/tooltip";
import { cn } from "@/lib/cn";

// The two row shapes share one element so the icon never moves while the label
// fades and the column narrows.
export const rowBase =
  "group relative isolate flex items-center rounded-md text-left text-[12.5px] outline-none transition-[width,background-color,color] duration-200 ease-out focus-visible:shadow-[0_0_0_2px_var(--focus)] motion-reduce:transition-none [&>svg]:size-[15px] [&>svg]:shrink-0 [&>svg]:transition-colors";
export const rowShape = (rail: boolean) => (rail ? "mx-3 h-8 w-8 justify-center" : "mx-2.5 h-7 w-[calc(100%-1.25rem)] gap-2.5 px-2");
export const rowIdle = "text-fg-2 hover:bg-chrome-hover hover:text-fg [&>svg]:text-fg-3 hover:[&>svg]:text-fg-2";
// The current row: its grey pill glides between rows (ActivePill), so the row
// itself only changes its text.
export const rowActive = "text-fg font-medium [&>svg]:text-fg hover:bg-transparent";

export const PillCtx = createContext("nav-pill");

/** The active row's background, shared across rows so it glides between them. */
export function ActivePill() {
  const id = useContext(PillCtx);
  const reduce = useReducedMotion();
  return (
    <motion.span
      aria-hidden
      layoutId={reduce ? undefined : id}
      transition={{ type: "spring", stiffness: 550, damping: 42, mass: 0.7 }}
      className="absolute inset-0 -z-10 rounded-md bg-chrome-active"
    />
  );
}

/** Link children that draw the gliding pill when the row is current. */
export const withPill = (content: ReactNode) =>
  function NavRowContent({ isActive }: { isActive: boolean }) {
    return (
      <>
        {isActive && <ActivePill />}
        {content}
      </>
    );
  };

export function NavTip({ rail, label, children }: { rail: boolean; label: string; children: React.ReactElement }) {
  if (!rail) return children;
  return (
    <Tooltip content={label} side="right">
      {children}
    </Tooltip>
  );
}

export function Count({ n, rail }: { n: number | null | undefined; rail: boolean }) {
  if (n === null || n === undefined || rail) return null;
  // data-count: a row with a menu fades it for its ⋯ button (app.css .nav-row-menu).
  return (
    <span data-count="" className="ml-auto flex shrink-0">
      <AnimatedNumber
        value={Math.min(n, 1000)}
        format={(v) => (v >= 1000 ? "999+" : String(Math.round(v)))}
        flash
        className="mono text-[11px] text-fg-4 group-data-[status=active]:text-fg-2"
      />
    </span>
  );
}

export function Label({ children, rail }: { children: ReactNode; rail: boolean }) {
  return <span className={cn("min-w-0 flex-1 truncate", rail ? "sr-only" : "animate-label-in motion-reduce:animate-none")}>{children}</span>;
}

export function Section({
  id,
  label,
  rail,
  action,
  first,
  children,
}: {
  id: string;
  label: string;
  rail: boolean;
  action?: ReactNode;
  first?: boolean;
  children: ReactNode;
}) {
  const key = `od-nav-fold-${id}`;
  const [folded, setFolded] = useState(() => {
    try {
      return localStorage.getItem(key) === "1";
    } catch {
      return false;
    }
  });
  const toggle = () => {
    setFolded(!folded);
    try {
      localStorage.setItem(key, folded ? "0" : "1");
    } catch {
      /* storage unavailable */
    }
  };
  return (
    <div className={cn(!first && "mt-5")}>
      {!rail && (
        <div className="group/section mx-2.5 mb-0.5 flex h-6 items-center px-2">
          <button
            type="button"
            aria-expanded={!folded}
            onClick={toggle}
            className="label-caps flex min-w-0 flex-1 items-center gap-1 text-left outline-none transition-colors hover:text-fg-2 focus-visible:text-fg"
          >
            {label}
            <ChevronDown
              aria-hidden
              className={cn(
                "size-3 shrink-0 transition-[transform,opacity] duration-200",
                folded ? "-rotate-90 opacity-100" : "opacity-0 group-hover/section:opacity-100",
              )}
            />
          </button>
          {action}
        </div>
      )}
      {(!folded || rail) && <div className="flex flex-col gap-px">{children}</div>}
    </div>
  );
}


/** The pulsing red dot of a waiting visitor. */
export function WaitDot({ size = "size-1.5" }: { size?: string }) {
  return (
    <span className={cn("relative flex shrink-0", size)}>
      <span className="absolute inset-0 rounded-full bg-tone-red-solid animate-urgent-ping motion-reduce:hidden" />
      <span className={cn("relative rounded-full bg-tone-red-solid", size)} />
    </span>
  );
}

/**
 * On a row that lists tickets: how many visitors wait in chat there with nobody on it. A
 * soft tinted chip with a small live dot, so several rows can carry it without shouting;
 * the "waiting in chat" row on top of the inbox stays the loud one.
 */
export function WaitingBadge({ n, rail }: { n: number; rail: boolean }) {
  const { t } = useTranslation("shell");
  if (!n) return null;
  if (rail) return <span className="absolute left-1 top-1"><WaitDot /></span>;
  return (
    <span
      title={t("nav.waitingCount", { count: n })}
      aria-label={t("nav.waitingCount", { count: n })}
      className="ml-auto inline-flex h-[18px] shrink-0 items-center gap-1 rounded-[5px] bg-tone-red-soft pl-1.5 pr-[5px] text-[10.5px] font-semibold tabular-nums text-tone-red"
    >
      <WaitDot size="size-[5px]" />
      {n > 99 ? "99+" : n}
    </span>
  );
}
