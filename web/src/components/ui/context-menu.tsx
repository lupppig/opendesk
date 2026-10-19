// A right-click (or long-press, or the Menu key) menu on one element, styled like the
// dropdown menus. Closes on click-away and Escape.

import { ContextMenu as CM } from "radix-ui";
import { Check } from "lucide-react";
import { forwardRef, type ReactNode } from "react";
import { cn } from "@/lib/cn";
import { menuItemCls } from "./menu";
import { floatingPanel } from "./popover";

export const ContextMenu = CM.Root;

/** Wraps one element; forwards props and ref to it, so it can sit inside a Tooltip. */
export const ContextMenuTrigger = forwardRef<HTMLElement, { children: ReactNode } & React.HTMLAttributes<HTMLElement>>(function ContextMenuTrigger({ children, ...rest }, ref) {
  return (
    <CM.Trigger asChild ref={ref as React.Ref<HTMLSpanElement>} {...rest}>
      {children}
    </CM.Trigger>
  );
});

export function ContextMenuContent({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <CM.Portal>
      <CM.Content
        collisionPadding={8}
        className={cn(floatingPanel, "min-w-[184px] origin-[var(--radix-context-menu-content-transform-origin)] p-1 motion-reduce:animate-none", className)}
      >
        {children}
      </CM.Content>
    </CM.Portal>
  );
}

export function ContextMenuItem({ children, icon, onSelect, disabled }: { children: ReactNode; icon?: ReactNode; onSelect?: () => void; disabled?: boolean }) {
  return (
    <CM.Item disabled={disabled} onSelect={onSelect} className={menuItemCls}>
      {icon}
      <span className="flex-1 truncate">{children}</span>
    </CM.Item>
  );
}

/** One of several choices: the current one shows its check, and `hint` sits at the right (a count, a key). */
export function ContextMenuChoice({ children, checked, onSelect, hint }: { children: ReactNode; checked: boolean; onSelect: () => void; hint?: ReactNode }) {
  return (
    <CM.CheckboxItem checked={checked} onSelect={onSelect} className={cn(menuItemCls, "pl-[30px]")}>
      <CM.ItemIndicator className="absolute left-2 flex">
        <Check className="!text-fg" />
      </CM.ItemIndicator>
      <span className="flex-1 truncate">{children}</span>
      {hint !== undefined && hint !== null && <span className="mono shrink-0 text-[11px] tabular-nums text-fg-4">{hint}</span>}
    </CM.CheckboxItem>
  );
}

export function ContextMenuLabel({ children }: { children: ReactNode }) {
  return <CM.Label className="label-caps px-2 pb-1 pt-1.5">{children}</CM.Label>;
}

export function ContextMenuSeparator() {
  return <CM.Separator className="-mx-1 my-1 h-px bg-line" />;
}
