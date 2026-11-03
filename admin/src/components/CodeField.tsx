// A two-factor code: six boxes over one real input (input-otp, so paste,
// autofill and screen readers just work), or a recovery code.

import { OTPInput, REGEXP_ONLY_DIGITS } from "input-otp";
import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { TextInput } from "@/components/ui/input";
import { cn } from "@/lib/cn";

export type CodeMode = "totp" | "recovery";

/** Whether a code is complete enough to send. */
export function codeReady(mode: CodeMode, code: string) {
  const v = code.trim();
  return mode === "totp" ? /^\d{6}$/.test(v) : v.length >= 8;
}

export function OtpInput({
  value,
  onChange,
  onComplete,
  disabled,
  invalid,
  autoFocus = true,
  size = "lg",
}: {
  value: string;
  onChange: (v: string) => void;
  onComplete?: (v: string) => void;
  disabled?: boolean;
  invalid?: boolean;
  autoFocus?: boolean;
  size?: "md" | "lg";
}) {
  const { t } = useTranslation("auth");
  return (
    <OTPInput
      autoFocus={autoFocus}
      maxLength={6}
      value={value}
      onChange={onChange}
      onComplete={(v: string) => onComplete?.(v)}
      disabled={disabled}
      pattern={REGEXP_ONLY_DIGITS}
      inputMode="numeric"
      autoComplete="one-time-code"
      aria-label={t("code.totpLabel")}
      aria-invalid={invalid || undefined}
      containerClassName={cn("flex items-center", invalid && "animate-shake motion-reduce:animate-none")}
      render={({ slots }) => (
        <div className="flex w-full items-center gap-2">
          {slots.map((s, i) => (
            <div
              key={i}
              className={cn(
                "mono relative flex flex-1 items-center justify-center rounded-md border bg-surface font-medium tabular-nums transition-[border-color,box-shadow] duration-150",
                size === "lg" ? "h-12 text-[20px]" : "h-10 text-[17px]",
                invalid ? "border-tone-red-solid" : s.isActive ? "border-focus-line shadow-[0_0_0_3px_var(--focus)]" : s.char ? "border-line-strong" : "border-line",
                i === 2 && "mr-2",
              )}
            >
              {s.char && <span className="animate-indicator-in motion-reduce:animate-none">{s.char}</span>}
              {s.hasFakeCaret && <span className="h-5 w-px animate-pulse-soft bg-fg" />}
            </div>
          ))}
        </div>
      )}
    />
  );
}

export function CodeField({
  mode,
  onModeChange,
  value,
  onChange,
  onComplete,
  disabled,
  invalid,
  autoFocus = true,
  size = "lg",
}: {
  mode: CodeMode;
  onModeChange: (m: CodeMode) => void;
  value: string;
  onChange: (v: string) => void;
  onComplete?: (v: string) => void;
  disabled?: boolean;
  invalid?: boolean;
  autoFocus?: boolean;
  size?: "md" | "lg";
}) {
  const { t } = useTranslation("auth");
  const recoveryRef = useRef<HTMLInputElement>(null);
  const switched = useRef(false);
  useEffect(() => {
    if (mode === "recovery" && switched.current) recoveryRef.current?.focus();
  }, [mode]);
  return (
    <div className="flex flex-col gap-2">
      {mode === "totp" ? (
        <OtpInput value={value} onChange={onChange} onComplete={onComplete} disabled={disabled} invalid={invalid} autoFocus={autoFocus} size={size} />
      ) : (
        <TextInput
          ref={recoveryRef}
          inputSize={size === "lg" ? "lg" : "md"}
          className="mono tracking-[0.04em]"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder="xxxx-xxxx-xxxx-xxxx"
          aria-label={t("code.recoveryLabel")}
          autoComplete="off"
          spellCheck={false}
          invalid={invalid}
          disabled={disabled}
          autoFocus={autoFocus}
        />
      )}
      <button
        type="button"
        onClick={() => {
          switched.current = true;
          onModeChange(mode === "totp" ? "recovery" : "totp");
          onChange("");
        }}
        className="self-start text-[12px] text-fg-3 underline decoration-line-strong underline-offset-2 hover:text-fg"
      >
        {mode === "totp" ? t("code.useRecovery") : t("code.useApp")}
      </button>
    </div>
  );
}
