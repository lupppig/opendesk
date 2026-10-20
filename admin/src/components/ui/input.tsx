import { forwardRef, useEffect, useId, useRef, useState, type InputHTMLAttributes, type ReactNode, type TextareaHTMLAttributes } from "react";
import { Eye, EyeOff, Search, X } from "lucide-react";
import { cn } from "@/lib/cn";

export const inputBase =
  "w-full min-w-0 rounded-md border border-line bg-surface px-2.5 text-[13px] text-fg shadow-xs outline-none transition-[border-color,box-shadow,background-color] duration-150 placeholder:text-fg-4 hover:border-line-strong focus:border-focus-line focus:shadow-[0_0_0_3px_var(--focus)] focus-visible:shadow-[0_0_0_3px_var(--focus)] disabled:cursor-not-allowed disabled:bg-sunken disabled:text-fg-3 aria-[invalid=true]:border-danger aria-[invalid=true]:focus:shadow-[0_0_0_3px_color-mix(in_srgb,var(--danger)_18%,transparent)]";

/**
 * Shakes a control once when it turns invalid (not on every render while it
 * stays invalid). Reduced motion keeps it still.
 */
function useShake(invalid: boolean | undefined) {
  const [shake, setShake] = useState(false);
  const was = useRef(!!invalid);
  useEffect(() => {
    if (invalid && !was.current) setShake(true);
    was.current = !!invalid;
  }, [invalid]);
  return {
    className: shake ? "animate-shake motion-reduce:animate-none" : undefined,
    onAnimationEnd: (e: React.AnimationEvent) => {
      if (e.target === e.currentTarget) setShake(false);
    },
  };
}

const heights = { sm: "h-7 text-[12.5px]", md: "h-8", lg: "h-10 rounded-lg px-3 text-[14px]" } as const;

type InputProps = InputHTMLAttributes<HTMLInputElement> & { invalid?: boolean; leading?: ReactNode; trailing?: ReactNode; inputSize?: keyof typeof heights };

export const TextInput = forwardRef<HTMLInputElement, InputProps>(function TextInput(
  { className, invalid, leading, trailing, inputSize = "md", ...rest },
  ref,
) {
  const h = heights[inputSize];
  const shake = useShake(invalid);
  if (!leading && !trailing)
    return (
      <input
        ref={ref}
        aria-invalid={invalid || undefined}
        className={cn(inputBase, h, className, shake.className)}
        {...rest}
        onAnimationEnd={(e) => {
          shake.onAnimationEnd(e);
          rest.onAnimationEnd?.(e);
        }}
      />
    );
  return (
    <div className={cn("relative flex items-center", className, shake.className)} onAnimationEnd={shake.onAnimationEnd}>
      {leading && <span className="pointer-events-none absolute left-2.5 flex text-fg-3 [&_svg]:size-3.5">{leading}</span>}
      <input
        ref={ref}
        aria-invalid={invalid || undefined}
        className={cn(inputBase, h, leading && (inputSize === "lg" ? "pl-9" : "pl-8"), trailing && (inputSize === "lg" ? "pr-10" : "pr-8"))}
        {...rest}
      />
      {trailing && <span className="absolute right-1.5 flex items-center text-fg-3">{trailing}</span>}
    </div>
  );
});

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaHTMLAttributes<HTMLTextAreaElement> & { invalid?: boolean }>(
  function Textarea({ className, invalid, ...rest }, ref) {
    const shake = useShake(invalid);
    return (
      <textarea
        ref={ref}
        aria-invalid={invalid || undefined}
        className={cn(inputBase, "min-h-[72px] resize-y py-1.5 leading-5", className, shake.className)}
        {...rest}
        onAnimationEnd={(e) => {
          shake.onAnimationEnd(e);
          rest.onAnimationEnd?.(e);
        }}
      />
    );
  },
);

/** A password field with a show/hide toggle. */
export const PasswordInput = forwardRef<HTMLInputElement, InputProps>(function PasswordInput(props, ref) {
  const [show, setShow] = useState(false);
  return (
    <TextInput
      ref={ref}
      {...props}
      type={show ? "text" : "password"}
      trailing={
        <button
          type="button"
          onClick={() => setShow((s) => !s)}
          aria-label={show ? "Hide password" : "Show password"}
          className="flex size-7 items-center justify-center rounded-sm text-fg-4 transition-colors hover:bg-hover hover:text-fg"
        >
          {show ? <EyeOff className="size-3.5" /> : <Eye className="size-3.5" />}
        </button>
      }
    />
  );
});

export function SearchInput({
  value,
  onChange,
  placeholder = "Search",
  className,
  autoFocus,
  inputSize = "sm",
  onKeyDown,
  inputRef,
  pageSearch,
}: {
  /** The page's main search: the "/" shortcut focuses it. */
  pageSearch?: boolean;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  className?: string;
  autoFocus?: boolean;
  inputSize?: "sm" | "md" | "lg";
  onKeyDown?: React.KeyboardEventHandler<HTMLInputElement>;
  inputRef?: React.Ref<HTMLInputElement>;
}) {
  return (
    <TextInput
      ref={inputRef}
      inputSize={inputSize}
      type="text"
      role="searchbox"
      data-page-search={pageSearch ? "" : undefined}
      value={value}
      autoFocus={autoFocus}
      onKeyDown={(e) => {
        if (e.key === "Escape" && value) {
          e.stopPropagation();
          onChange("");
        }
        onKeyDown?.(e);
      }}
      onChange={(e) => onChange(e.target.value)}
      placeholder={placeholder}
      aria-label={placeholder}
      className={className}
      leading={<Search />}
      trailing={
        value ? (
          <button
            type="button"
            aria-label="Clear search"
            onClick={() => onChange("")}
            className="flex size-5 items-center justify-center rounded text-fg-3 hover:bg-hover hover:text-fg"
          >
            <X className="size-3" />
          </button>
        ) : undefined
      }
    />
  );
}

export function Label({ children, htmlFor, className, optional }: { children: ReactNode; htmlFor?: string; className?: string; optional?: boolean }) {
  return (
    <label htmlFor={htmlFor} className={cn("flex items-center gap-1.5 text-[12.5px] font-medium text-fg", className)}>
      {children}
      {optional && <span className="font-normal text-fg-4">Optional</span>}
    </label>
  );
}

/** Label + control + hint/error, with ids wired for screen readers. */
export function Field({
  label,
  hint,
  error,
  optional,
  children,
  className,
  id: idProp,
}: {
  label?: ReactNode;
  hint?: ReactNode;
  error?: ReactNode;
  optional?: boolean;
  children: (p: { id: string; "aria-describedby"?: string; invalid: boolean }) => ReactNode;
  className?: string;
  id?: string;
}) {
  const auto = useId();
  const id = idProp ?? auto;
  const describedBy = error ? `${id}-err` : hint ? `${id}-hint` : undefined;
  return (
    <div className={cn("flex flex-col gap-1.5", className)}>
      {label && (
        <Label htmlFor={id} optional={optional}>
          {label}
        </Label>
      )}
      {children({ id, "aria-describedby": describedBy, invalid: !!error })}
      {error ? (
        <p id={`${id}-err`} role="alert" className="animate-rise-in text-xs text-danger-fg">
          {error}
        </p>
      ) : hint ? (
        <p id={`${id}-hint`} className="text-xs text-fg-3">
          {hint}
        </p>
      ) : null}
    </div>
  );
}

/** What a number field's text says: a number, nothing, or not a number. */
export function parseNumber(text: string, decimal = false): number | null | undefined {
  const v = text.trim().replace(",", ".");
  if (!v) return null;
  if (!(decimal ? /^-?(\d+\.?\d*|\.\d+)$/ : /^-?\d+$/).test(v)) return undefined;
  const n = Number(v);
  return Number.isFinite(n) ? n : undefined;
}

/**
 * A number field: a text input (no spinners) that takes digits, and a decimal
 * point when `decimal`. `onChange` only ever gets a number within min..max;
 * anything else marks the field invalid, and leaving it puts back a valid
 * number (clamped into range, or the last one). ArrowUp and ArrowDown step,
 * Shift steps ten times as far.
 */
export function NumberInput({
  value,
  onChange,
  onValidChange,
  min,
  max,
  step = 1,
  decimal = false,
  suffix,
  invalid,
  className,
  id,
  ...aria
}: {
  value: number;
  onChange: (n: number) => void;
  /** Whether the text is a number in range right now (false while it is not). */
  onValidChange?: (valid: boolean) => void;
  min?: number;
  max?: number;
  step?: number;
  decimal?: boolean;
  suffix?: string;
  invalid?: boolean;
  className?: string;
  id?: string;
  "aria-label"?: string;
  "aria-describedby"?: string;
}) {
  const show = (n: number) => (Number.isFinite(n) ? String(n) : "");
  const [text, setText] = useState(() => show(value));
  const [bad, setBad] = useState(false);
  const clamp = (n: number) => Math.min(max ?? Infinity, Math.max(min ?? -Infinity, n));
  const inRange = (n: number) => n === clamp(n);

  // A value changed from outside (a unit switch, a reset) replaces the text, unless the text already says it.
  const [seen, setSeen] = useState(value);
  if (!Object.is(seen, value)) {
    setSeen(value);
    if (parseNumber(text, decimal) !== value) setText(show(value));
  }

  const mark = (ok: boolean) => {
    setBad(!ok);
    onValidChange?.(ok);
  };
  const edit = (next: string) => {
    if (next && !(decimal ? /^-?[\d.,]*$/ : /^-?\d*$/).test(next)) return; // ignore keys that are not part of a number
    setText(next);
    const n = parseNumber(next, decimal);
    const ok = n != null && inRange(n);
    mark(ok);
    if (ok && n !== value) onChange(n);
  };
  const commit = () => {
    const n = parseNumber(text, decimal);
    const next = n == null ? value : clamp(n);
    setText(show(next));
    mark(true);
    if (next !== value) onChange(next);
  };
  const stepBy = (d: number) => {
    const base = parseNumber(text, decimal) ?? value;
    const next = clamp(Math.round(((Number.isFinite(base) ? base : (min ?? 0)) + d) * 1e6) / 1e6);
    setText(show(next));
    mark(true);
    if (next !== value) onChange(next);
  };

  return (
    <div className={cn("relative flex h-8 items-center", className)}>
      <input
        id={id}
        type="text"
        inputMode={decimal ? "decimal" : "numeric"}
        autoComplete="off"
        spellCheck={false}
        value={text}
        role="spinbutton"
        aria-valuenow={Number.isFinite(value) ? value : undefined}
        aria-valuemin={min}
        aria-valuemax={max}
        aria-invalid={bad || invalid || undefined}
        onChange={(e) => edit(e.target.value)}
        onBlur={commit}
        onKeyDown={(e) => {
          if (e.key !== "ArrowUp" && e.key !== "ArrowDown") return;
          e.preventDefault();
          stepBy((e.key === "ArrowUp" ? step : -step) * (e.shiftKey ? 10 : 1));
        }}
        className={cn(inputBase, "h-8 mono tabular-nums", suffix && "pr-10")}
        {...aria}
      />
      {suffix && <span className="pointer-events-none absolute right-2.5 text-xs text-fg-3">{suffix}</span>}
    </div>
  );
}
