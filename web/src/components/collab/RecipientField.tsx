// A To / Cc / Bcc row: validated address chips, paste a whole list, and suggestions
// from the conversation's people and the workspace's contacts. The suggestion list
// closes on Escape and when the field loses focus.

import { useQuery } from "@tanstack/react-query";
import { X } from "lucide-react";
import { useId, useMemo, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { api } from "@/lib/api/endpoints";
import type { MailAddress } from "@/lib/api/types";
import { normEmail, parseAddressList } from "@/lib/collab/recipients";
import { cn } from "@/lib/cn";
import { useDebounced } from "@/components/contacts/hooks";
import { Avatar } from "@/components/ui/avatar";

export function RecipientField({
  label,
  values,
  onChange,
  suggestions = [],
  autoFocus,
  end,
  onSubmit,
}: {
  label: string;
  values: string[];
  onChange: (v: string[]) => void;
  /** People to offer first (the conversation's participants). */
  suggestions?: MailAddress[];
  autoFocus?: boolean;
  end?: ReactNode;
  onSubmit?: () => void;
}) {
  const { t } = useTranslation("collab");
  const [text, setText] = useState("");
  const [open, setOpen] = useState(false);
  const [index, setIndex] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const listId = useId();
  const term = useDebounced(text.trim(), 200);
  const contacts = useQuery({
    queryKey: ["collab", "contact-suggest", term],
    queryFn: () => api.contacts.search({ q: term, limit: 6 }),
    enabled: open && term.length > 1,
    staleTime: 30_000,
  });

  const taken = useMemo(() => new Set(values.map(normEmail)), [values]);
  const options = useMemo(() => {
    const q = text.trim().toLowerCase();
    const seen = new Set<string>();
    const out: MailAddress[] = [];
    const add = (a: MailAddress) => {
      const k = normEmail(a.email);
      if (!k || seen.has(k) || taken.has(k)) return;
      if (q && !a.email.toLowerCase().includes(q) && !a.name.toLowerCase().includes(q)) return;
      seen.add(k);
      out.push(a);
    };
    suggestions.forEach(add);
    for (const c of contacts.data?.data ?? []) if (c.email) add({ name: c.name, email: c.email });
    return q || suggestions.length ? out.slice(0, 8) : [];
  }, [text, suggestions, contacts.data, taken]);

  const commit = (raw = text) => {
    const { valid, invalid } = parseAddressList(raw);
    const fresh = valid.filter((e) => !taken.has(normEmail(e)));
    if (fresh.length) onChange([...values, ...fresh]);
    setText(invalid.join(" "));
    setError(invalid.length ? t("composer.invalid", { value: invalid[0] }) : null);
  };
  const pick = (a: MailAddress) => {
    onChange([...values, a.email]);
    setText("");
    setError(null);
    setIndex(0);
    inputRef.current?.focus();
  };
  const showList = open && options.length > 0;

  return (
    <div className="relative border-b border-line-soft">
      <div className="flex min-h-8 items-center gap-2 px-3" onClick={() => inputRef.current?.focus()}>
        <span className="w-9 shrink-0 text-xs text-fg-3">{label}</span>
        <div className="flex min-w-0 flex-1 flex-wrap items-center gap-1 py-1">
          {values.map((v) => (
            <span key={v} className="mono inline-flex h-5 items-center gap-1 rounded-sm bg-hover pl-1.5 pr-0.5 text-[11.5px] animate-pop-in motion-reduce:animate-none" data-ph-mask>
              {v}
              <button
                type="button"
                aria-label={t("composer.removeRecipient", { email: v })}
                onClick={(e) => {
                  e.stopPropagation();
                  onChange(values.filter((x) => x !== v));
                }}
                className="flex size-4 items-center justify-center rounded-xs text-fg-4 hover:text-fg"
              >
                <X className="size-3" />
              </button>
            </span>
          ))}
          <input
            ref={inputRef}
            value={text}
            autoFocus={autoFocus}
            role="combobox"
            aria-expanded={showList}
            aria-controls={listId}
            aria-autocomplete="list"
            aria-label={label}
            aria-invalid={!!error}
            onChange={(e) => {
              setText(e.target.value);
              setOpen(true);
              setIndex(0);
              setError(null);
            }}
            onFocus={() => setOpen(true)}
            onBlur={() => {
              setOpen(false);
              if (text.trim()) commit();
            }}
            onPaste={(e) => {
              const pasted = e.clipboardData.getData("text");
              if (/[,;\n]/.test(pasted) || parseAddressList(pasted).valid.length > 1) {
                e.preventDefault();
                commit(`${text} ${pasted}`);
              }
            }}
            onKeyDown={(e) => {
              if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
                e.preventDefault();
                if (text.trim()) commit();
                onSubmit?.();
                return;
              }
              if (showList && e.key === "ArrowDown") {
                e.preventDefault();
                setIndex((i) => (i + 1) % options.length);
                return;
              }
              if (showList && e.key === "ArrowUp") {
                e.preventDefault();
                setIndex((i) => (i - 1 + options.length) % options.length);
                return;
              }
              if (e.key === "Escape" && showList) {
                e.preventDefault();
                e.stopPropagation();
                setOpen(false);
                return;
              }
              if (e.key === "Enter" || e.key === "," || e.key === ";" || e.key === "Tab") {
                if (showList && (e.key === "Enter" || e.key === "Tab") && options[index] && !parseAddressList(text).valid.length) {
                  e.preventDefault();
                  pick(options[index]);
                  return;
                }
                if (text.trim()) {
                  e.preventDefault();
                  commit();
                }
              }
              if (e.key === "Backspace" && !text && values.length) onChange(values.slice(0, -1));
            }}
            placeholder={values.length ? "" : "name@example.com"}
            className="h-6 min-w-[140px] flex-1 bg-transparent text-[12.5px] outline-none placeholder:text-fg-4"
          />
        </div>
        {end}
      </div>
      {error && <p role="alert" className="px-3 pb-1.5 pl-[56px] text-[11.5px] text-danger-fg">{error}</p>}
      {showList && (
        <div
          id={listId}
          role="listbox"
          aria-label={t("composer.suggestions")}
          className="absolute left-[52px] top-full z-30 mt-1 w-[300px] overflow-hidden rounded-lg border border-line bg-raised p-1 shadow-pop animate-pop-in motion-reduce:animate-none"
        >
          {options.map((a, i) => (
            <button
              key={a.email}
              type="button"
              role="option"
              aria-selected={i === index}
              onMouseDown={(e) => {
                e.preventDefault();
                pick(a);
              }}
              onMouseEnter={() => setIndex(i)}
              className={cn("flex h-8 w-full items-center gap-2 rounded-md px-2 text-left text-[12.5px]", i === index && "bg-hover")}
            >
              <Avatar name={a.name || a.email} size="xs" />
              <span className="min-w-0 flex-1 truncate">{a.name || a.email}</span>
              {a.name && <span className="mono shrink-0 truncate text-[11px] text-fg-4">{a.email}</span>}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
