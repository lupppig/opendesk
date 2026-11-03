import { Toaster as Sonner, toast as sonner } from "sonner";
import { errorMessage } from "@/lib/api/client";
import { i18n } from "@/i18n";
import { useTheme } from "@/lib/theme";
import { Check, Info, Undo2, X } from "lucide-react";
import { cn } from "@/lib/cn";
import { tone, type Tone } from "@/lib/tones";

function ToastIcon({ hue, children }: { hue: Tone; children: React.ReactNode }) {
  return (
    <span className={cn("flex size-[18px] shrink-0 items-center justify-center rounded-[5px] [&_svg]:size-3 [&_svg]:stroke-[2.25]", tone[hue].chip)}>{children}</span>
  );
}

export function Toaster() {
  const { dark } = useTheme();
  return (
    <Sonner
      theme={dark ? "dark" : "light"}
      position="bottom-right"
      gap={6}
      visibleToasts={4}
      offset={16}
      swipeDirections={["right", "bottom"]}
      toastOptions={{
        unstyled: true,
        classNames: {
          toast:
            "relative flex w-[340px] items-center gap-2.5 overflow-hidden rounded-lg border border-line bg-raised px-3 py-2.5 text-[12.5px] text-fg shadow-float",
          title: "font-medium",
          description: "text-fg-3 text-xs",
          actionButton: "ml-auto h-6 rounded-md bg-inverse px-2 text-xs font-medium text-inverse-fg",
          cancelButton: "h-6 rounded-md px-2 text-xs text-fg-3 hover:bg-hover",
          icon: "flex",
        },
      }}
    />
  );
}

type Opts = { description?: string; action?: { label: string; onClick: () => void } };

// A toast that offers an action (Undo, Open) stays a little longer and shows
// how long is left; the bar pauses while the stack is hovered, as the timer does.
const ACTION_MS = 6000;

function Countdown({ ms }: { ms: number }) {
  return (
    <span
      aria-hidden
      className="pointer-events-none absolute inset-x-0 bottom-0 h-0.5 origin-left animate-toast-progress bg-fg-4/40 motion-reduce:hidden [[data-expanded=true]_&]:[animation-play-state:paused]"
      style={{ animationDuration: `${ms}ms` }}
    />
  );
}

function withAction(opts?: Opts) {
  if (!opts?.action) return opts;
  return {
    ...opts,
    duration: ACTION_MS,
    description: (
      <>
        {opts.description}
        <Countdown ms={ACTION_MS} />
      </>
    ),
  };
}

export const toast = {
  success: (msg: string, opts?: Opts) => sonner.success(msg, { ...withAction(opts), icon: <ToastIcon hue="green"><Check /></ToastIcon> }),
  info: (msg: string, opts?: Opts) => sonner(msg, { ...withAction(opts), icon: <ToastIcon hue="blue"><Info /></ToastIcon> }),
  /** A done destructive action with a way back. */
  undo: (msg: string, onUndo: () => void, label = i18n.t("actions.undo")) =>
    sonner(msg, { ...withAction({ action: { label, onClick: onUndo } }), icon: <ToastIcon hue="gray"><Undo2 /></ToastIcon> }),
  error: (e: unknown, fallback?: string) =>
    sonner.error(typeof e === "string" ? e : errorMessage(e, fallback), { icon: <ToastIcon hue="red"><X /></ToastIcon> }),
};
