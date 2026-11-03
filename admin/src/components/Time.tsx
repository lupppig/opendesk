import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { fullDate, relative } from "@/lib/format";
import { Tooltip } from "@/components/ui/tooltip";
import { cn } from "@/lib/cn";

const subs = new Set<() => void>();
let timer: ReturnType<typeof setInterval> | undefined;

/** One shared 30-second clock, so relative times stay fresh without a timer each. */
function useNow() {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const tick = () => setNow(Date.now());
    subs.add(tick);
    timer ??= setInterval(() => subs.forEach((s) => s()), 30_000);
    return () => {
      subs.delete(tick);
      if (!subs.size) {
        clearInterval(timer);
        timer = undefined;
      }
    };
  }, []);
  return now;
}

/** A relative time ("5 minutes ago") with the absolute one on hover. */
export function Time({ iso, className, empty }: { iso: string | null | undefined; className?: string; empty?: string }) {
  const { t } = useTranslation();
  const now = useNow();
  if (!iso) return <span className={cn("text-fg-4", className)}>{empty ?? t("time.never")}</span>;
  return (
    <Tooltip content={fullDate(iso)}>
      <time dateTime={iso} className={cn("whitespace-nowrap", className)} tabIndex={-1}>
        {relative(iso, now)}
      </time>
    </Tooltip>
  );
}
