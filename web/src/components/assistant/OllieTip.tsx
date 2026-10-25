// A gentle nudge under the header blob when something in the queue needs a
// look ("3 tickets breach SLA within the hour"). Tips come from a cheap count
// query, never the model. At most one every 20 minutes, each one once a day,
// never while the panel is open, and they can be paused for a week.

import { useQuery } from "@tanstack/react-query";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { X } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/cn";
import { tone } from "@/lib/tones";
import { assistantApi, type Tip } from "./api";
import { useAssistantName } from "./name";
import { ollie, useOllie } from "./store";
import { qk } from "@/lib/queries";

const KEY = "od-ollie-tips";
const GAP = 20 * 60_000;
const AGAIN = 24 * 3_600_000;
const PAUSE = 7 * 24 * 3_600_000;
const SHOW_FOR = 16_000;

type Memory = { last?: number; paused?: number; seen?: Record<string, number> };

function read(): Memory {
  try {
    return (JSON.parse(localStorage.getItem(KEY) || "{}") as Memory) ?? {};
  } catch {
    return {};
  }
}

function write(m: Memory) {
  try {
    localStorage.setItem(KEY, JSON.stringify(m));
  } catch {
    /* storage unavailable: tips just come back later */
  }
}

export function pickTip(tips: Tip[], m: Memory, now = Date.now()): Tip | null {
  if (m.paused && now < m.paused) return null;
  if (m.last && now - m.last < GAP) return null;
  return tips.find((t) => !(m.seen?.[t.key] && now - m.seen[t.key] < AGAIN)) ?? null;
}

export function OllieTip({ enabled }: { enabled: boolean }) {
  const { t } = useTranslation("assistant");
  const name = useAssistantName();
  const reduce = useReducedMotion();
  const open = useOllie((s) => s.open);
  const [tip, setTip] = useState<Tip | null>(null);
  const { data } = useQuery({
    queryKey: qk.of("assistant", "tips"),
    queryFn: assistantApi.tips,
    enabled,
    staleTime: 5 * 60_000,
    refetchInterval: 10 * 60_000,
    refetchOnWindowFocus: false,
    retry: false,
  });

  // Offer one a few seconds after the data arrives, if the rules allow it.
  useEffect(() => {
    if (!enabled || open || tip || !data?.tips.length) return;
    const id = setTimeout(() => {
      if (useOllie.getState().open || document.hidden) return;
      const m = read();
      const next = pickTip(data.tips, m);
      if (!next) return;
      write({ ...m, last: Date.now(), seen: { ...m.seen, [next.key]: Date.now() } });
      setTip(next);
    }, 3500);
    return () => clearTimeout(id);
  }, [data, enabled, open, tip]);

  // Leaves on its own, and the moment the panel opens.
  useEffect(() => {
    if (!tip) return;
    if (open) {
      setTip(null);
      return;
    }
    const id = setTimeout(() => setTip(null), SHOW_FOR);
    return () => clearTimeout(id);
  }, [tip, open]);

  const pause = () => {
    write({ ...read(), paused: Date.now() + PAUSE });
    setTip(null);
  };

  return (
    <AnimatePresence>
      {tip && !open && (
        <motion.div
          key={tip.key}
          role="status"
          initial={reduce ? { opacity: 0 } : { opacity: 0, y: -6, scale: 0.96 }}
          animate={{ opacity: 1, y: 0, scale: 1 }}
          exit={reduce ? { opacity: 0 } : { opacity: 0, y: -4, scale: 0.98 }}
          transition={reduce ? { duration: 0.12 } : { type: "spring", stiffness: 460, damping: 32 }}
          style={{ transformOrigin: "top right", ["--ollie-tip-x" as string]: "34px" }}
          className="ollie-tip absolute right-0 top-[calc(100%+10px)] z-40 w-[296px] rounded-xl border border-line bg-raised p-3 shadow-pop"
          data-ollie-tip={tip.key}
        >
          <div className="flex items-start gap-2">
            <span className={cn("mt-[5px] size-1.5 shrink-0 rounded-full", tip.severity === "high" ? tone.red.solid : tip.severity === "medium" ? tone.amber.solid : tone.violet.solid)} />
            <p className="flex-1 text-[12.5px] leading-[18px] text-fg">{tip.text}</p>
            <button
              type="button"
              aria-label={t("tip.dismiss")}
              onClick={() => setTip(null)}
              className="-mr-1 -mt-1 flex size-5 shrink-0 items-center justify-center rounded-md text-fg-4 hover:bg-hover hover:text-fg"
            >
              <X className="size-3" />
            </button>
          </div>
          <div className="mt-2.5 flex items-center gap-1.5 pl-3.5">
            <button
              type="button"
              onClick={() => {
                setTip(null);
                ollie.ask(tip.prompt);
              }}
              className="h-6 whitespace-nowrap rounded-md bg-inverse px-2 text-[12px] font-medium text-inverse-fg transition-[background-color,transform] hover:bg-inverse-hover active:scale-[0.97]"
            >
              {t("tip.act", { name })}
            </button>
            <button type="button" onClick={() => setTip(null)} className="h-6 whitespace-nowrap rounded-md px-2 text-[12px] text-fg-3 hover:bg-hover hover:text-fg">
              {t("tip.later")}
            </button>
            <button type="button" onClick={pause} className="ml-auto whitespace-nowrap text-[11px] text-fg-4 underline-offset-2 hover:text-fg-2 hover:underline">
              {t("tip.pause")}
            </button>
          </div>
        </motion.div>
      )}
    </AnimatePresence>
  );
}
