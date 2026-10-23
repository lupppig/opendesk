// A From name that may name its sender (lib/fromname): the field, a chip per variable that
// puts it in at the cursor, what is wrong with it, and under it the live preview the caller
// draws. The helpdesk mailbox's From name and Collab's addresses share it.

import { useRef, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { TextInput } from "@/components/ui/input";
import { FROM_MAX, fromNameProblem, type FromVar } from "@/lib/fromname";

export function FromFormatField({
  value,
  onChange,
  vars,
  varTitle,
  ariaLabel,
  placeholder,
  disabled,
  id,
  autoFocus,
  children,
}: {
  value: string;
  onChange: (v: string) => void;
  vars: readonly FromVar[];
  /** What a variable reads, as its chip's tooltip. */
  varTitle: (v: FromVar) => string;
  ariaLabel: string;
  placeholder?: string;
  disabled?: boolean;
  id?: string;
  autoFocus?: boolean;
  /** The preview, shown while the format is valid. */
  children?: ReactNode;
}) {
  const { t } = useTranslation("settingsChannels");
  const ref = useRef<HTMLInputElement>(null);
  const problem = fromNameProblem(value, vars);
  const insert = (v: string) => {
    const el = ref.current;
    const at = el?.selectionStart ?? value.length;
    const end = el?.selectionEnd ?? at;
    onChange(value.slice(0, at) + v + value.slice(end));
    requestAnimationFrame(() => {
      el?.focus();
      el?.setSelectionRange(at + v.length, at + v.length);
    });
  };
  return (
    <div className="flex flex-col gap-2">
      <TextInput
        ref={ref}
        id={id}
        aria-label={ariaLabel}
        value={value}
        maxLength={FROM_MAX + 10}
        invalid={!!problem}
        placeholder={placeholder}
        disabled={disabled}
        data-autofocus={autoFocus || undefined}
        onChange={(e) => onChange(e.target.value)}
      />
      {!disabled && (
        <div className="flex flex-wrap items-center gap-1.5">
          {vars.map((v) => (
            <button
              key={v}
              type="button"
              onClick={() => insert(`{${v}}`)}
              className="mono inline-flex h-[22px] items-center rounded-sm border border-line bg-sunken px-1.5 text-[11px] text-fg-2 transition-colors hover:border-line-strong hover:text-fg"
              title={varTitle(v)}
            >
              {`{${v}}`}
            </button>
          ))}
        </div>
      )}
      {problem ? (
        <p role="alert" className="text-[12px] text-danger-fg">
          {t(`addressing.fromErrors.${problem}`, { max: FROM_MAX })}
        </p>
      ) : (
        children
      )}
    </div>
  );
}

/** One line of a From preview: what it is, then `Name <address>`. */
export function FromPreviewLine({ label, name, email }: { label?: string; name: string; email: string }) {
  return (
    <div className="flex min-w-0 items-baseline gap-2 text-[12px]">
      {label && <span className="w-[92px] shrink-0 text-fg-4">{label}</span>}
      <span className="min-w-0 truncate">
        {name && <span className="font-medium text-fg">{name}</span>} <span className="mono text-[11.5px] text-fg-3">&lt;{email}&gt;</span>
      </span>
    </div>
  );
}
