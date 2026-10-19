import { DropdownMenu as M } from "radix-ui";
import { Check, ChevronRight } from "lucide-react";
import type { ReactNode } from "react";
import { cn } from "@/lib/cn";
import { floatingPanel } from "./popover";
import { Kbd } from "./kbd";

export const Menu = M.Root;
export const MenuTrigger = M.Trigger;

export function MenuContent({
  children,
  align = "end",
  side = "bottom",
  className,
  sideOffset = 6,
  onCloseAutoFocus,
}: {
  children: ReactNode;
  align?: "start" | "center" | "end";
  side?: "top" | "bottom" | "left" | "right";
  className?: string;
  sideOffset?: number;
  /** preventDefault keeps focus where it went (an item that opens another popover). */
  onCloseAutoFocus?: (e: Event) => void;
}) {
  return (
    <M.Portal>
      <M.Content
        align={align}
        side={side}
        sideOffset={sideOffset}
        collisionPadding={8}
        className={cn(floatingPanel, "min-w-[180px] origin-[var(--radix-dropdown-menu-content-transform-origin)] p-1", className)}
        onClick={(e) => e.stopPropagation()}
        onCloseAutoFocus={onCloseAutoFocus}
      >
        {children}
      </M.Content>
    </M.Portal>
  );
}

export const menuItemCls =
  "group/item relative flex h-7 cursor-default select-none items-center gap-2 rounded-md px-2 transition-colors duration-100 text-[12.5px] text-fg outline-none data-[disabled]:pointer-events-none data-[disabled]:opacity-40 data-[highlighted]:bg-hover [&_svg]:size-3.5 [&_svg]:shrink-0 [&_svg]:text-fg-3";

export function MenuItem({
  children,
  icon,
  onSelect,
  danger,
  shortcut,
  disabled,
}: {
  children: ReactNode;
  icon?: ReactNode;
  onSelect?: () => void;
  danger?: boolean;
  shortcut?: string;
  disabled?: boolean;
}) {
  return (
    <M.Item
      disabled={disabled}
      onSelect={onSelect}
      className={cn(menuItemCls, danger && "text-danger-fg data-[highlighted]:bg-[color-mix(in_srgb,var(--danger)_8%,transparent)] [&_svg]:text-danger-fg")}
    >
      {icon}
      <span className="flex-1 truncate">{children}</span>
      {shortcut && <Kbd>{shortcut}</Kbd>}
    </M.Item>
  );
}

export function MenuCheckItem({ children, checked, onSelect, icon }: { children: ReactNode; checked: boolean; onSelect: () => void; icon?: ReactNode }) {
  return (
    <M.Item
      onSelect={(e) => {
        e.preventDefault();
        onSelect();
      }}
      className={menuItemCls}
    >
      {icon}
      <span className="flex-1 truncate">{children}</span>
      {checked && <Check className="!text-fg" />}
    </M.Item>
  );
}

/** One of several choices: the current one shows its check, and `hint` sits at the right (a count, a key). */
export function MenuChoice({ children, checked, onSelect, hint }: { children: ReactNode; checked: boolean; onSelect: () => void; hint?: ReactNode }) {
  return (
    <M.CheckboxItem checked={checked} onSelect={onSelect} className={cn(menuItemCls, "pl-[30px]")}>
      <M.ItemIndicator className="absolute left-2 flex">
        <Check className="!text-fg" />
      </M.ItemIndicator>
      <span className="flex-1 truncate">{children}</span>
      {hint !== undefined && hint !== null && <span className="mono shrink-0 text-[11px] tabular-nums text-fg-4">{hint}</span>}
    </M.CheckboxItem>
  );
}

export function MenuLabel({ children }: { children: ReactNode }) {
  return <M.Label className="px-2 pb-1 pt-1.5 label-caps">{children}</M.Label>;
}

export function MenuSeparator() {
  return <M.Separator className="-mx-1 my-1 h-px bg-line" />;
}

export function MenuSub({ label, icon, children }: { label: ReactNode; icon?: ReactNode; children: ReactNode }) {
  return (
    <M.Sub>
      <M.SubTrigger className={cn(menuItemCls, "data-[state=open]:bg-hover")}>
        {icon}
        <span className="flex-1">{label}</span>
        <ChevronRight />
      </M.SubTrigger>
      <M.Portal>
        <M.SubContent sideOffset={6} collisionPadding={8} className={cn(floatingPanel, "min-w-[180px] origin-[var(--radix-dropdown-menu-content-transform-origin)] p-1")}>
          {children}
        </M.SubContent>
      </M.Portal>
    </M.Sub>
  );
}
