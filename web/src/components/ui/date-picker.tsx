import { useTranslation } from "react-i18next";
import {
  addDays,
  addMonths,
  endOfMonth,
  endOfWeek,
  format,
  isAfter,
  isBefore,
  isSameDay,
  isSameMonth,
  isValid,
  parse,
  startOfDay,
  startOfMonth,
  startOfWeek,
} from "date-fns";
import { CalendarDays, ChevronLeft, ChevronRight, X } from "lucide-react";
import { fr } from "date-fns/locale";
import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { cn } from "@/lib/cn";
import { timeOfDay, twelveHour } from "@/lib/format";
import { inputBase, TextInput } from "./input";
import { Popover } from "./popover";

const ISO = "yyyy-MM-dd";

/** Parses a "yyyy-MM-dd" value; empty or malformed is null. */
export function parseDay(v: string | undefined | null): Date | null {
  if (!v) return null;
  const d = parse(v, ISO, new Date());
  return isValid(d) ? d : null;
}

export const formatDay = (d: Date) => format(d, ISO);

/** A month grid. Arrow keys move the day, Page Up and Down the month, Enter picks. */
export function Calendar({
  value,
  onChange,
  min,
  max,
  className,
}: {
  value: Date | null;
  onChange: (d: Date) => void;
  min?: Date | null;
  max?: Date | null;
  className?: string;
}) {
  const { t: ui, i18n } = useTranslation("ui");
  const locale = i18n.resolvedLanguage === "fr" ? fr : undefined;
  const [focus, setFocus] = useState(() => startOfDay(value ?? new Date()));
  const [month, setMonth] = useState(() => startOfMonth(focus));
  const grid = useRef<HTMLDivElement>(null);
  const keyed = useRef(false);

  useEffect(() => {
    if (!keyed.current) return;
    keyed.current = false;
    grid.current?.querySelector<HTMLButtonElement>('[tabindex="0"]')?.focus();
  }, [focus]);

  const days = useMemo(() => {
    const out: Date[] = [];
    const end = endOfWeek(endOfMonth(month), { weekStartsOn: 1 });
    for (let d = startOfWeek(month, { weekStartsOn: 1 }); !isAfter(d, end); d = addDays(d, 1)) out.push(d);
    return out;
  }, [month]);

  const out = (d: Date) => (!!min && isBefore(d, startOfDay(min))) || (!!max && isAfter(d, startOfDay(max)));

  const move = (d: Date) => {
    keyed.current = true;
    setFocus(d);
    if (!isSameMonth(d, month)) setMonth(startOfMonth(d));
  };

  const onKey = (e: KeyboardEvent) => {
    const step: Record<string, () => Date> = {
      ArrowLeft: () => addDays(focus, -1),
      ArrowRight: () => addDays(focus, 1),
      ArrowUp: () => addDays(focus, -7),
      ArrowDown: () => addDays(focus, 7),
      PageUp: () => addMonths(focus, -1),
      PageDown: () => addMonths(focus, 1),
      Home: () => startOfWeek(focus, { weekStartsOn: 1 }),
      End: () => endOfWeek(focus, { weekStartsOn: 1 }),
    };
    const next = step[e.key];
    if (!next) return;
    e.preventDefault();
    move(startOfDay(next()));
  };

  const today = new Date();
  return (
    <div className={cn("w-[224px] select-none", className)}>
      <div className="mb-1.5 flex items-center justify-between">
        <span className="pl-1 text-[12.5px] font-medium">{format(month, "MMMM yyyy", { locale })}</span>
        <div className="flex">
          {[-1, 1].map((n) => (
            <button
              key={n}
              type="button"
              aria-label={n < 0 ? ui("previousMonth") : ui("nextMonth")}
              onClick={() => setMonth((m) => addMonths(m, n))}
              className="grid size-7 place-items-center rounded-md text-fg-3 outline-none transition-colors hover:bg-hover hover:text-fg focus-visible:bg-hover"
            >
              {n < 0 ? <ChevronLeft className="size-3.5" /> : <ChevronRight className="size-3.5" />}
            </button>
          ))}
        </div>
      </div>
      <div className="grid grid-cols-7 text-center">
        {days.slice(0, 7).map((d) => (
          <span key={d.toISOString()} className="pb-1 text-[10.5px] font-medium uppercase text-fg-4">
            {format(d, "EEEEEE", { locale })}
          </span>
        ))}
      </div>
      <div ref={grid} role="grid" className="grid grid-cols-7 gap-y-0.5" onKeyDown={onKey}>
        {days.map((d) => {
          const selected = !!value && isSameDay(d, value);
          const disabled = out(d);
          return (
            <button
              key={d.toISOString()}
              type="button"
              role="gridcell"
              aria-selected={selected}
              aria-label={format(d, "PPPP", { locale })}
              disabled={disabled}
              tabIndex={isSameDay(d, focus) ? 0 : -1}
              onClick={() => {
                setFocus(d);
                onChange(d);
              }}
              className={cn(
                "mono mx-auto grid size-7 place-items-center rounded-md text-[11.5px] tabular-nums outline-none transition-colors",
                "hover:bg-hover focus-visible:shadow-[0_0_0_2px_var(--focus)] disabled:pointer-events-none disabled:text-fg-4/50",
                !isSameMonth(d, month) && "text-fg-4",
                isSameDay(d, today) && !selected && "font-semibold text-fg underline decoration-fg-4 underline-offset-[3px]",
                selected && "bg-fg text-bg hover:bg-fg",
              )}
            >
              {format(d, "d")}
            </button>
          );
        })}
      </div>
    </div>
  );
}

/** A date field: a button that shows the day and opens a calendar. Value is "yyyy-MM-dd" or "". */
export function DateField({
  value,
  onChange,
  min,
  max,
  placeholder,
  invalid,
  clearable,
  size = "md",
  className,
  autoFocus,
  "aria-label": ariaLabel,
}: {
  value: string;
  onChange: (v: string) => void;
  min?: string;
  max?: string;
  placeholder?: string;
  invalid?: boolean;
  clearable?: boolean;
  size?: "sm" | "md";
  className?: string;
  autoFocus?: boolean;
  "aria-label"?: string;
}) {
  const { t: ui, i18n } = useTranslation("ui");
  const locale = i18n.resolvedLanguage === "fr" ? fr : undefined;
  const [open, setOpen] = useState(false);
  const day = parseDay(value);
  return (
    <div className={cn("relative", className)}>
      <Popover
        open={open}
        onOpenChange={setOpen}
        className="p-2"
        trigger={
          <button
            type="button"
            aria-label={ariaLabel}
            aria-invalid={invalid || undefined}
            autoFocus={autoFocus}
            className={cn(
              inputBase,
              "flex items-center gap-2 text-left",
              size === "sm" ? "h-7 text-[12.5px]" : "h-8",
              clearable && day && "pr-7",
            )}
          >
            <CalendarDays className="size-3.5 shrink-0 text-fg-3" />
            <span className={cn("mono truncate", !day && "font-sans text-fg-4")}>{day ? format(day, "d MMM yyyy", { locale }) : (placeholder ?? ui("pickADate"))}</span>
          </button>
        }
      >
        <Calendar
          value={day}
          min={parseDay(min)}
          max={parseDay(max)}
          onChange={(d) => {
            onChange(formatDay(d));
            setOpen(false);
          }}
        />
      </Popover>
      {clearable && day && (
        <button
          type="button"
          aria-label={ui("clearDate")}
          onClick={() => onChange("")}
          className="absolute right-1 top-1/2 grid size-5 -translate-y-1/2 place-items-center rounded text-fg-4 hover:bg-hover hover:text-fg"
        >
          <X className="size-3" />
        </button>
      )}
    </div>
  );
}

/** Parses "H:mm", "HH:mm", "HHmm", or a 12-hour "9:30 PM", "9pm", "12 am" into [hours, minutes]; anything else is null. */
export function parseTime(v: string): [number, number] | null {
  const twelve = /^(\d{1,2})(?::?(\d{2}))?\s*([ap])\.?\s*m?\.?$/i.exec(v.trim());
  if (twelve) {
    const h = Number(twelve[1]);
    const min = Number(twelve[2] ?? 0);
    if (h < 1 || h > 12 || min > 59) return null;
    return [(h % 12) + (twelve[3].toLowerCase() === "p" ? 12 : 0), min];
  }
  const m = /^(\d{1,2}):?(\d{2})$/.exec(v.trim());
  if (!m) return null;
  const h = Number(m[1]);
  const min = Number(m[2]);
  return h < 24 && min < 60 ? [h, min] : null;
}

/** "9:00" -> "09:00"; null when it isn't a time. */
export function normalTime(v: string): string | null {
  const t = parseTime(v);
  return t ? `${String(t[0]).padStart(2, "0")}:${String(t[1]).padStart(2, "0")}` : null;
}

/** A time of day typed as text on the reader's clock ("9:00 AM" or "09:00"): no native time control. The value is always "HH:mm". */
export function TimeField({
  value,
  onChange,
  size = "md",
  className,
  "aria-label": ariaLabel,
}: {
  value: string;
  onChange: (v: string) => void;
  size?: "sm" | "md";
  className?: string;
  "aria-label"?: string;
}) {
  const twelve = twelveHour();
  const shown = (v: string) => (normalTime(v) ? timeOfDay(normalTime(v)!) : v);
  const [text, setText] = useState(() => shown(value));
  // Follow outside changes, but not the echo of what is being typed.
  useEffect(() => setText((t) => (normalTime(t) === value ? t : shown(value))), [value]);
  const invalid = !!text && !parseTime(text);
  return (
    <TextInput
      inputSize={size}
      inputMode={twelve ? "text" : "numeric"}
      placeholder={timeOfDay("09:00")}
      maxLength={twelve ? 8 : 5}
      aria-label={ariaLabel}
      invalid={invalid}
      value={text}
      onChange={(e) => {
        const t = twelve ? e.target.value.replace(/[^\d:apm.\s]/gi, "") : e.target.value.replace(/[^\d:]/g, "");
        setText(t);
        const v = normalTime(t);
        if (v) onChange(v);
      }}
      onBlur={() => {
        const v = normalTime(text);
        if (v) setText(timeOfDay(v));
      }}
      className={cn("mono text-center", twelve ? "w-[84px]" : "w-[64px]", className)}
    />
  );
}
