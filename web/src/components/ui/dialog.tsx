import { useTranslation } from "react-i18next";
import { Dialog as D } from "radix-ui";
import { X } from "lucide-react";
import type { ReactNode } from "react";
import { cn } from "@/lib/cn";

export function Dialog({
  open,
  onOpenChange,
  title,
  description,
  children,
  footer,
  className,
  size = "md",
  onEscapeKeyDown,
  onInteractOutside,
  actions,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  title: ReactNode;
  description?: ReactNode;
  children?: ReactNode;
  footer?: ReactNode;
  className?: string;
  size?: "sm" | "md" | "lg" | "xl";
  onEscapeKeyDown?: (e: KeyboardEvent) => void;
  onInteractOutside?: (e: Event) => void;
  /** Buttons beside the close button (open on a full page). */
  actions?: ReactNode;
}) {
  const { t: ui } = useTranslation("ui");
  const w = { sm: "max-w-[400px]", md: "max-w-[520px]", lg: "max-w-[680px]", xl: "max-w-[880px]" }[size];
  return (
    <D.Root open={open} onOpenChange={onOpenChange}>
      <D.Portal>
        <D.Overlay className="fixed inset-0 z-50 bg-scrim backdrop-blur-[1.5px] data-[state=open]:animate-fade-in data-[state=closed]:animate-fade-out" />
        <D.Content
          onEscapeKeyDown={onEscapeKeyDown}
          onInteractOutside={onInteractOutside}
          className={cn(
            "fixed left-1/2 top-[12vh] z-50 flex max-h-[76vh] w-[calc(100vw-32px)] -translate-x-1/2 flex-col overflow-hidden rounded-xl border border-line bg-raised shadow-float outline-none data-[state=open]:animate-dialog-in data-[state=closed]:animate-dialog-out",
            w,
            className,
          )}
        >
          <div className="flex items-start justify-between gap-4 px-5 pb-2 pt-[18px]">
            <div className="min-w-0">
              <D.Title className="text-[15px] font-semibold leading-5 tracking-[-0.015em] text-fg">{title}</D.Title>
              {description ? (
                <D.Description className="mt-1 text-[12.5px] leading-[18px] text-fg-3">{description}</D.Description>
              ) : (
                <D.Description className="sr-only">{typeof title === "string" ? title : "Dialog"}</D.Description>
              )}
            </div>
            <div className="-mr-2 -mt-1 flex shrink-0 items-center gap-0.5">
              {actions}
              <D.Close
                aria-label={ui("close")}
                className="flex size-7 shrink-0 items-center justify-center rounded-md text-fg-4 transition-colors hover:bg-hover hover:text-fg"
              >
                <X className="size-4" />
              </D.Close>
            </div>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto px-5 py-3">{children}</div>
          {footer && <div className="flex items-center justify-end gap-2 border-t border-line bg-sunken px-5 py-3">{footer}</div>}
        </D.Content>
      </D.Portal>
    </D.Root>
  );
}

/** Side sheet for record details: slides in from the right, Escape and backdrop close it. */
export function Drawer({
  open,
  onOpenChange,
  title,
  children,
  header,
  width = 520,
  onEscapeKeyDown,
  onInteractOutside,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  title: string;
  children: ReactNode;
  header?: ReactNode;
  width?: number;
  onEscapeKeyDown?: (e: KeyboardEvent) => void;
  onInteractOutside?: (e: Event) => void;
}) {
  const { t: ui } = useTranslation("ui");
  return (
    <D.Root open={open} onOpenChange={onOpenChange}>
      <D.Portal>
        <D.Overlay className="fixed inset-0 z-40 bg-scrim/50 data-[state=open]:animate-fade-in data-[state=closed]:animate-fade-out" />
        <D.Content
          style={{ width: `min(${width}px, calc(100vw - 16px))` }}
          onEscapeKeyDown={onEscapeKeyDown}
          onInteractOutside={onInteractOutside}
          onOpenAutoFocus={(e) => {
            // Focus the panel (or a field marked data-autofocus), not the close button.
            e.preventDefault();
            const el = e.currentTarget as HTMLElement | null;
            (el?.querySelector<HTMLElement>("[data-autofocus]") ?? el)?.focus({ preventScroll: true });
          }}
          tabIndex={-1}
          className="fixed inset-y-2 right-2 z-40 flex flex-col overflow-hidden rounded-xl border border-line bg-surface shadow-float outline-none data-[state=open]:animate-slide-in-right data-[state=closed]:animate-slide-out-right max-sm:inset-0 max-sm:rounded-none"
        >
          <D.Title className="sr-only">{title}</D.Title>
          <D.Description className="sr-only">{title}</D.Description>
          <div className="flex h-12 shrink-0 items-center gap-2 border-b border-line px-4">
            <div className="min-w-0 flex-1">{header ?? <span className="text-sm font-medium">{title}</span>}</div>
            <D.Close
              aria-label={ui("close")}
              className="flex size-7 items-center justify-center rounded-md text-fg-3 hover:bg-hover hover:text-fg"
            >
              <X className="size-4" />
            </D.Close>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto">{children}</div>
        </D.Content>
      </D.Portal>
    </D.Root>
  );
}
