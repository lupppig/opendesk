import { Check, Copy } from "lucide-react";
import { useState } from "react";
import { i18n } from "@/i18n";
import { cn } from "@/lib/cn";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { springs } from "@/lib/motion";
import { Tooltip } from "./tooltip";

function Morph({ k, children }: { k: string; children: React.ReactNode }) {
  const reduce = useReducedMotion();
  return (
    <AnimatePresence mode="popLayout" initial={false}>
      <motion.span
        key={k}
        className="inline-flex"
        initial={reduce ? { opacity: 0 } : { opacity: 0, scale: 0.6 }}
        animate={{ opacity: 1, scale: 1 }}
        exit={reduce ? { opacity: 0 } : { opacity: 0, scale: 0.6 }}
        transition={springs.snap}
      >
        {children}
      </motion.span>
    </AnimatePresence>
  );
}

export async function copyText(text: string) {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}

export function CopyButton({ value, label = i18n.t("actions.copy"), className }: { value: string; label?: string; className?: string }) {
  const [done, setDone] = useState(false);
  return (
    <Tooltip content={done ? i18n.t("actions.copied") : label}>
      <button
        type="button"
        aria-label={label}
        onClick={async (e) => {
          e.stopPropagation();
          if (await copyText(value)) {
            setDone(true);
            setTimeout(() => setDone(false), 1400);
          }
        }}
        className={cn("flex size-6 shrink-0 items-center justify-center rounded-md text-fg-3 transition-colors hover:bg-hover hover:text-fg", className)}
      >
        <Morph k={done ? "done" : "copy"}>{done ? <Check className="size-3.5 text-tone-green" /> : <Copy className="size-3.5" />}</Morph>
      </button>
    </Tooltip>
  );
}

/** A read-only value in a field with a copy button: forwarding addresses, secrets, snippets. */
export function CopyField({ value, mono = true, className, multiline }: { value: string; mono?: boolean; className?: string; multiline?: boolean }) {
  return (
    <div className={cn("flex min-w-0 items-start gap-1 rounded-md border border-line bg-sunken py-1 pl-2.5 pr-1", className)}>
      {multiline ? (
        <pre className={cn("min-w-0 flex-1 overflow-x-auto whitespace-pre py-1 text-xs leading-5 text-fg", mono && "mono")}>{value}</pre>
      ) : (
        <span className={cn("min-w-0 flex-1 truncate py-0.5 text-[12.5px] text-fg", mono && "mono")}>{value}</span>
      )}
      <CopyButton value={value} />
    </div>
  );
}
