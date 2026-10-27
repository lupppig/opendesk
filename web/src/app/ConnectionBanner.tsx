// A slim banner at the top of the work panel while the live connection is down
// after having been up. It goes away when the connection returns: the header's
// connection dot stays the steady indicator, so the return needs no line of its own.

import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useRealtime } from "@/lib/realtime/store";
import { springs } from "@/lib/motion";
import { cn } from "@/lib/cn";
import { tone } from "@/lib/tones";

export function ConnectionBanner() {
  const { t } = useTranslation("shell");
  const status = useRealtime((s) => s.status);
  const reduce = useReducedMotion();
  const wasLive = useRef(false);
  const [lost, setLost] = useState(false);

  useEffect(() => {
    if (status === "live") {
      wasLive.current = true;
      setLost(false);
      return;
    }
    if (wasLive.current && (status === "reconnecting" || status === "offline")) {
      // A blip under a second and a half is not worth a banner.
      const id = setTimeout(() => setLost(true), 1500);
      return () => clearTimeout(id);
    }
  }, [status]);

  return (
    <AnimatePresence initial={false}>
      {lost && (
        <motion.div
          role="status"
          initial={reduce ? { opacity: 0 } : { opacity: 0, y: -8 }}
          animate={{ opacity: 1, y: 0 }}
          exit={reduce ? { opacity: 0 } : { opacity: 0, y: -8 }}
          transition={springs.row}
          className="pointer-events-none absolute inset-x-0 top-2 z-30 flex justify-center"
        >
          <span
            className={cn(
              "inline-flex h-7 items-center gap-2 rounded-md px-2.5 text-[12px] font-medium shadow-float",
              tone.amber.chip,
              "bg-raised",
            )}
          >
            <span className={cn("size-1.5 rounded-full", tone.amber.solid, "animate-pulse-soft")} />
            {t("connection.lost")}
          </span>
        </motion.div>
      )}
    </AnimatePresence>
  );
}
