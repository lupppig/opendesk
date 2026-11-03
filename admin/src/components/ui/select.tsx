import { Select as S } from "radix-ui";
import { Check, ChevronDown } from "lucide-react";
import type { ReactNode } from "react";
import { i18n } from "@/i18n";
import { cn } from "@/lib/cn";
import { floatingPanel } from "./popover";

export type SelectOption<V extends string> = { value: V; label: ReactNode; icon?: ReactNode; hint?: ReactNode };

export function Select<V extends string>({
  value,
  onChange,
  options,
  placeholder = i18n.t("ui.select"),
  className,
  size = "md",
  id,
  disabled,
  "aria-label": ariaLabel,
}: {
  value: V | undefined;
  onChange: (v: V) => void;
  options: SelectOption<V>[];
  placeholder?: string;
  className?: string;
  size?: "sm" | "md";
  id?: string;
  disabled?: boolean;
  "aria-label"?: string;
}) {
  return (
    <S.Root value={value} onValueChange={(v) => onChange(v as V)} disabled={disabled}>
      <S.Trigger
        id={id}
        aria-label={ariaLabel}
        className={cn(
          "group inline-flex w-full min-w-0 items-center justify-between gap-2 rounded-md border border-line bg-surface px-2.5 text-left text-fg shadow-[0_1px_1px_rgb(0_0_0/0.02)] outline-none transition-[border-color,box-shadow] duration-150 hover:border-line-strong focus-visible:border-fg-3 focus-visible:shadow-[0_0_0_3px_var(--focus)] disabled:opacity-60 data-[placeholder]:text-fg-4 [&_svg]:size-3.5",
          size === "sm" ? "h-7 text-[12.5px]" : "h-8 text-sm",
          className,
        )}
      >
        <span className="flex min-w-0 items-center gap-2 truncate">
          <S.Value placeholder={placeholder} />
        </span>
        <S.Icon className="text-fg-3 transition-transform duration-150 group-data-[state=open]:rotate-180 motion-reduce:transition-none">
          <ChevronDown />
        </S.Icon>
      </S.Trigger>
      <S.Portal>
        <S.Content position="popper" sideOffset={6} collisionPadding={8} className={cn(floatingPanel, "max-h-[320px] min-w-[var(--radix-select-trigger-width)] origin-[var(--radix-select-content-transform-origin)] p-1")}>
          <S.Viewport>
            {options.map((o) => (
              <S.Item
                key={o.value}
                value={o.value}
                className="relative flex h-7 cursor-default select-none items-center gap-2 rounded-md pl-2 pr-7 text-[12.5px] text-fg outline-none data-[highlighted]:bg-hover [&_svg]:size-3.5 [&_svg]:shrink-0"
              >
                {o.icon}
                <S.ItemText>{o.label}</S.ItemText>
                {o.hint && <span className="ml-auto text-[11px] text-fg-4">{o.hint}</span>}
                <S.ItemIndicator className="absolute right-2">
                  <Check />
                </S.ItemIndicator>
              </S.Item>
            ))}
          </S.Viewport>
        </S.Content>
      </S.Portal>
    </S.Root>
  );
}
