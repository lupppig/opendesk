import { Checkbox as C } from "radix-ui";
import { Check, Minus } from "lucide-react";
import { cn } from "@/lib/cn";

export function Checkbox({
  checked,
  onCheckedChange,
  className,
  disabled,
  id,
  "aria-label": ariaLabel,
  onClick,
}: {
  checked: boolean | "indeterminate";
  onCheckedChange: (v: boolean) => void;
  className?: string;
  disabled?: boolean;
  id?: string;
  "aria-label"?: string;
  onClick?: React.MouseEventHandler<HTMLButtonElement>;
}) {
  return (
    <C.Root
      id={id}
      checked={checked}
      disabled={disabled}
      aria-label={ariaLabel}
      onClick={onClick}
      onCheckedChange={(v) => onCheckedChange(v === true)}
      className={cn(
        "peer flex size-[15px] shrink-0 items-center justify-center rounded-sm border border-line-strong bg-surface text-inverse-fg transition-[background-color,border-color,transform] duration-150 hover:border-fg-3 active:scale-90 data-[state=checked]:border-inverse data-[state=checked]:bg-inverse data-[state=indeterminate]:border-inverse data-[state=indeterminate]:bg-inverse disabled:opacity-50",
        className,
      )}
    >
      <C.Indicator className="flex items-center justify-center data-[state=checked]:animate-check-pop data-[state=indeterminate]:animate-check-pop motion-reduce:animate-none">
        {checked === "indeterminate" ? <Minus className="size-2.5" strokeWidth={3} /> : <Check className="size-2.5" strokeWidth={3.5} />}
      </C.Indicator>
    </C.Root>
  );
}
