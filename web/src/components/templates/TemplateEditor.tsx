// The editor of every customer-facing template (contracts/templates.md):
// signatures, auto-replies, saved replies, footers, layouts and the CSAT email.
// Visual mode is the rich editor with tags as pills and "{{" to insert one;
// HTML mode edits the markup for designed signatures and layouts.

import { AlertCircle, Code2, Type } from "lucide-react";
import { forwardRef, useEffect, useImperativeHandle, useMemo, useRef, useState } from "react";
import { RichEditor, type RichEditorHandle } from "@/components/editor/RichEditor";
import { useConfirm } from "@/components/ui/confirm";
import { Textarea } from "@/components/ui/input";
import { Segmented } from "@/components/ui/tabs";
import type { TemplateKind, TemplateProblem } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { GROUP_LABELS, tagFor, useCatalog } from "./catalog";
import { insertCode, matchVars, refreshTags, templateExtension, VarSuggestPopup, type FilterInfo, type VarItem } from "./extension";
import { VariablePicker } from "./VariablePicker";

/** Markup the visual editor cannot keep: layout tables, styles, sized images. */
export function needsHtml(html: string): boolean {
  return /<(table|td|tr|div|font|center|span)\b|\sstyle=|<img\b[^>]*\s(width|height)=/i.test(html);
}

/** What in html the visual editor would lose, in plain words (needsHtml's reasons). */
export function htmlReasons(html: string): string[] {
  const out: string[] = [];
  if (/<(table|td|tr)\b/i.test(html)) out.push("a table layout");
  if (/\sstyle=/i.test(html)) out.push("inline styles");
  if (/<img\b[^>]*\s(width|height)=/i.test(html)) out.push("sized images");
  if (!out.length && /<(div|font|center|span)\b/i.test(html)) out.push("lines and fonts set in HTML");
  return out;
}

function list(parts: string[]): string {
  return parts.length < 2 ? (parts[0] ?? "") : `${parts.slice(0, -1).join(", ")} and ${parts[parts.length - 1]}`;
}

export type TemplateEditorHandle = { insert: (text: string) => void; focus: () => void };

export type TemplateEditorProps = {
  value: string;
  onChange: (html: string) => void;
  kind: TemplateKind;
  ariaLabel: string;
  placeholder?: string;
  /** Offer an HTML mode (signatures, footers, layouts, CSAT). */
  html?: boolean;
  /** Start in HTML mode (custom layouts). */
  htmlOnly?: boolean;
  toolbar?: "compact" | "full";
  minHeight?: number;
  disabled?: boolean;
  /** Draw a border and focus ring (inside forms); off when the parent frames it. */
  framed?: boolean;
  className?: string;
  problems?: TemplateProblem[];
  /** Template tags: the picker, "{{" suggestions and tags as pills. Off for HTML that is not filled (a mail account's signature). */
  variables?: boolean;
  onModeChange?: (mode: "visual" | "html") => void;
};

export const TemplateEditor = forwardRef<TemplateEditorHandle, TemplateEditorProps>(function TemplateEditor(
  { value, onChange, kind, ariaLabel, placeholder, html: allowHtml, htmlOnly, toolbar = "compact", minHeight = 96, disabled, framed = true, className, problems, variables = true, onModeChange },
  ref,
) {
  const confirm = useConfirm();
  const catalog = useCatalog(kind);
  const [mode, setMode] = useState<"visual" | "html">(() => (htmlOnly || (allowHtml && needsHtml(value)) ? "html" : "visual"));
  const onModeChangeRef = useRef(onModeChange);
  onModeChangeRef.current = onModeChange;
  useEffect(() => onModeChangeRef.current?.(mode), [mode]);
  const rich = useRef<RichEditorHandle>(null);
  const area = useRef<HTMLTextAreaElement>(null);
  const knownRef = useRef<Set<string> | null>(null);
  const itemsRef = useRef<VarItem[]>([]);
  const filtersRef = useRef<FilterInfo[]>([]);
  knownRef.current = catalog.loading ? null : catalog.known;
  filtersRef.current = catalog.filters;
  itemsRef.current = useMemo(
    () => [
      ...catalog.slots.map((s) => ({ key: s.key, label: s.label, group: "Place" })),
      ...catalog.variables.map((v) => ({ key: v.key, label: v.label, group: GROUP_LABELS[v.group] ?? v.group, value: v.example })),
    ],
    [catalog.slots, catalog.variables],
  );
  const extensions = useMemo(
    () => [
      templateExtension({
        known: () => knownRef.current,
        items: (query) => matchVars(itemsRef.current, query, (g) => g),
        filters: () => filtersRef.current,
        valueLabel: "Example",
      }),
    ],
    [],
  );
  // Unknown tags are marked once the list is in.
  useEffect(() => {
    if (!catalog.loading) refreshTags(rich.current?.editor);
  }, [catalog.loading, catalog.known]);

  const insert = (text: string) => {
    if (mode === "visual") {
      rich.current?.insertHtml(text.replace(/&/g, "&amp;").replace(/</g, "&lt;"));
      return;
    }
    const el = area.current;
    if (!el) return;
    const start = el.selectionStart ?? value.length;
    const end = el.selectionEnd ?? start;
    const next = value.slice(0, start) + text + value.slice(end);
    onChange(next);
    requestAnimationFrame(() => {
      el.focus();
      el.setSelectionRange(start + text.length, start + text.length);
    });
  };
  // Logic and helpers: written at the caret with the part to change selected.
  const insertWithSelection = (code: string, select?: string) => {
    if (mode === "visual") {
      const editor = rich.current?.editor;
      if (editor) insertCode(editor, code, select);
      return;
    }
    const el = area.current;
    if (!el) return;
    const start = el.selectionStart ?? value.length;
    const end = el.selectionEnd ?? start;
    onChange(value.slice(0, start) + code + value.slice(end));
    const i = select ? code.indexOf(select) : -1;
    requestAnimationFrame(() => {
      el.focus();
      if (select && i >= 0) el.setSelectionRange(start + i, start + i + select.length);
      else el.setSelectionRange(start + code.length, start + code.length);
    });
  };
  useImperativeHandle(ref, () => ({ insert, focus: () => (mode === "visual" ? rich.current?.focus() : area.current?.focus()) }));

  // A value that arrives later (still loading, another admin's save) and needs HTML opens
  // in HTML mode, so the visual editor never saves a stripped copy. A value the person
  // chose to simplify stays visual.
  const simplified = useRef<string | null>(null);
  useEffect(() => {
    if (allowHtml && mode === "visual" && needsHtml(value) && value !== simplified.current && !rich.current?.editor?.isFocused) setMode("html");
  }, [allowHtml, mode, value]);

  const switchMode = (next: "visual" | "html") => {
    if (next === mode) return;
    if (next === "visual" && needsHtml(value)) {
      confirm.show(
        "The visual editor keeps text, links, lists and images. Tables, inline styles and layout in this HTML will be removed.",
        () => {
          simplified.current = value;
          setMode("visual");
        },
        { title: "Switch to the visual editor?", confirmLabel: "Switch and simplify" },
      );
      return;
    }
    setMode(next);
  };

  const picker = variables && <VariablePicker kind={kind} onPick={(k) => insert(tagFor(k))} onInsert={insertWithSelection} />;
  // Why it is HTML: what the visual editor would remove.
  const reasons = mode === "html" && !htmlOnly && allowHtml ? htmlReasons(value) : [];
  const modeToggle = allowHtml && !htmlOnly && (
    <Segmented
      size="xs"
      value={mode}
      onChange={switchMode}
      options={[
        { value: "visual", label: "Visual", icon: <Type /> },
        { value: "html", label: "HTML", icon: <Code2 /> },
      ]}
    />
  );

  return (
    <div className={cn("flex flex-col", className)}>
      <div
        className={cn(
          "flex min-h-0 flex-col",
          framed && "rounded-md border border-line bg-surface transition-shadow focus-within:border-fg-3 focus-within:shadow-[0_0_0_3px_var(--focus)]",
          disabled && "opacity-70",
        )}
        style={{ "--tpl-min": `${minHeight}px` } as React.CSSProperties}
      >
        {mode === "visual" ? (
          <RichEditor
            ref={rich}
            value={value}
            onChange={onChange}
            placeholder={placeholder}
            ariaLabel={ariaLabel}
            editable={!disabled}
            toolbar={toolbar}
            extensions={variables ? extensions : undefined}
            contentClassName="min-h-[var(--tpl-min)]"
            toolbarEnd={
              <>
                {modeToggle}
                {picker}
              </>
            }
          />
        ) : (
          <>
            {reasons.length > 0 && (
              <p className="border-b border-line-soft px-3 py-2 text-[12px] leading-[17px] text-fg-3">
                {/signature/.test(kind) ? "This signature" : "This template"} uses {list(reasons)}, which the visual editor would remove, so it opens as HTML.
              </p>
            )}
            <Textarea
              ref={area}
              value={value}
              onChange={(e) => onChange(e.target.value)}
              aria-label={`${ariaLabel} (HTML)`}
              placeholder={placeholder}
              disabled={disabled}
              spellCheck={false}
              style={{ minHeight }}
              className="mono rounded-none border-0 bg-transparent px-3 py-2.5 text-[12px] leading-[18px] shadow-none focus:shadow-none"
            />
            <div className="flex items-center gap-1 border-t border-line-soft px-1.5 py-1">
              <span className="px-1.5 text-[11.5px] text-fg-4">Scripts, forms and remote styles are removed when you save.</span>
              <div className="ml-auto flex items-center gap-1">
                {modeToggle}
                {picker}
              </div>
            </div>
          </>
        )}
      </div>
      {problems && problems.length > 0 && <Problems problems={problems} />}
      {!disabled && variables && <TemplateHint />}
      {mode === "visual" && variables && <VarSuggestPopup />}
    </div>
  );
});

/** What the server found wrong with a template. */
export function Problems({ problems, className }: { problems: TemplateProblem[]; className?: string }) {
  return (
    <ul className={cn("mt-2 flex flex-col gap-1", className)} aria-live="polite">
      {problems.map((p, i) => (
        <li key={i} className="flex items-start gap-1.5 text-[12px] leading-[17px] text-tone-red">
          <AlertCircle className="mt-px size-3.5 shrink-0" />
          <span>
            {p.message}
            {p.token && <span className="mono ml-1 rounded-sm bg-tone-red-soft px-1 text-[11px]">{p.token}</span>}
          </span>
        </li>
      ))}
    </ul>
  );
}

/** Tells everyone writing a template what it can do. */
export function TemplateHint({ className }: { className?: string }) {
  return (
    <p className={cn("mt-1.5 flex flex-wrap items-center gap-x-1 text-[11.5px] leading-[17px] text-fg-4", className)}>
      <span>Type</span>
      <kbd className="mono rounded-sm bg-hover px-1 text-[10.5px] text-fg-3">{"{{"}</kbd>
      <span>for a variable or</span>
      <kbd className="mono rounded-sm bg-hover px-1 text-[10.5px] text-fg-3">{"{%"}</kbd>
      <span>for logic like if and loops.</span>
      <a href="https://docs.opendesk.sh/guides/email-templates/variables/" target="_blank" rel="noreferrer" className="text-fg-3 underline-offset-2 hover:text-fg hover:underline">
        Template guide
      </a>
    </p>
  );
}
