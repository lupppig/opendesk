import { Toaster as Sonner, toast as sonner } from "sonner";
import { errorMessage } from "@/lib/api/client";
import { useTheme } from "@/lib/theme";
import { Check, Clock, Info, Undo2, X } from "lucide-react";
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
          content: "min-w-0 flex-1",
          title: "font-medium",
          description: "text-fg-3 text-xs",
          actionButton: "ml-auto h-6 shrink-0 whitespace-nowrap rounded-md bg-inverse px-2 text-xs font-medium text-inverse-fg",
          cancelButton: "h-6 shrink-0 whitespace-nowrap rounded-md px-2 text-xs text-fg-3 hover:bg-hover",
          icon: "flex",
        },
      }}
    />
  );
}

/** cancel: a second, quieter button beside the action (Undo next to Always). */
type Opts = { description?: string; action?: { label: string; onClick: () => void }; cancel?: { label: string; onClick: () => void } };

// A toast that offers an action (Undo, Open) stays a little longer and shows
// how long is left; the bar pauses while the stack is hovered, as the timer does.
const ACTION_MS = 6000;

function Countdown({ ms, steady }: { ms: number; steady?: boolean }) {
  return (
    <span
      aria-hidden
      className={cn(
        "pointer-events-none absolute inset-x-0 bottom-0 h-0.5 origin-left animate-toast-progress bg-fg-4/40 motion-reduce:hidden",
        !steady && "[[data-expanded=true]_&]:[animation-play-state:paused]",
      )}
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
  undo: (msg: string, onUndo: () => void, label = "Undo") =>
    sonner(msg, { ...withAction({ action: { label, onClick: onUndo } }), icon: <ToastIcon hue="gray"><Undo2 /></ToastIcon> }),
  /** Something about to happen on its own clock (a held reply): the bar runs whatever the
   * pointer does, and the caller dismisses the toast when the moment comes. */
  held: (msg: string, ms: number, action: { label: string; onClick: () => void }, cancel: { label: string; onClick: () => void }) =>
    sonner(msg, { duration: Infinity, action, cancel, description: <Countdown ms={ms} steady />, icon: <ToastIcon hue="blue"><Clock /></ToastIcon> }),
  dismiss: (id?: string | number) => sonner.dismiss(id),
  error: (e: unknown, fallback?: string) =>
    sonner.error(typeof e === "string" ? e : errorMessage(e, fallback), { icon: <ToastIcon hue="red"><X /></ToastIcon> }),
};
