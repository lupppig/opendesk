// Quick asks that fit the page: a ticket gets "summarize" and "who should take
// this", the inbox gets triage, reports get the week in numbers.

import { useRouterState } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { pageContext } from "./context";

type Key = "summarize" | "assign" | "reply" | "note" | "triage" | "unassigned" | "report" | "kb" | "wiki" | "wikiProcess" | "label" | "breach" | "mine";

export function suggestionKeys(page: string | undefined): Key[] {
  switch (page) {
    case "ticket":
      return ["summarize", "assign", "reply", "note"];
    case "reports":
      return ["report", "breach", "unassigned"];
    case "kb":
      return ["kb", "report"];
    case "wiki":
      return ["wiki", "wikiProcess", "triage"];
    case "inbox":
    case "view":
      return ["triage", "unassigned", "label", "mine"];
    default:
      return ["triage", "breach", "mine", "report"];
  }
}

export function Suggestions({ onPick, compact }: { onPick: (prompt: string) => void; compact?: boolean }) {
  const { t } = useTranslation("assistant");
  // Re-read on navigation; the preview pane also counts as an open ticket.
  useRouterState({ select: (s) => s.location.href });
  const all = suggestionKeys(pageContext().page);
  const keys = compact ? all.slice(0, 3) : all;
  if (compact) {
    return (
      <div className="-mx-3 flex gap-1.5 overflow-x-auto px-3 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden" role="list" aria-label={t("suggested")}>
        {keys.map((k, i) => (
          <button
            key={k}
            type="button"
            role="listitem"
            title={t(`prompts.${k}`)}
            onClick={() => onPick(t(`prompts.${k}`))}
            style={{ animationDelay: `${60 + i * 40}ms` }}
            className="animate-item-in shrink-0 whitespace-nowrap rounded-full border border-line bg-surface px-2.5 py-0.5 text-[11.5px] text-fg-2 shadow-xs transition-[border-color,color,background-color,transform] hover:border-[color-mix(in_srgb,var(--tone-violet-solid)_40%,var(--line))] hover:bg-tone-violet-soft hover:text-fg active:scale-[0.98] motion-reduce:animate-none"
          >
            {t(`chips.${k}`)}
          </button>
        ))}
      </div>
    );
  }
  // Empty panel: one tidy row per suggestion, a short label that sends the full prompt.
  return (
    <div className="flex flex-col gap-1" role="list" aria-label={t("suggested")}>
      {keys.map((k, i) => (
        <button
          key={k}
          type="button"
          role="listitem"
          title={t(`prompts.${k}`)}
          onClick={() => onPick(t(`prompts.${k}`))}
          style={{ animationDelay: `${60 + i * 40}ms` }}
          className="group animate-item-in flex w-full items-center gap-2.5 rounded-[6px] border border-line bg-surface px-3 py-2 text-left text-[13px] text-fg-2 shadow-xs transition-[border-color,color,background-color,transform] hover:border-[color-mix(in_srgb,var(--tone-violet-solid)_40%,var(--line))] hover:bg-tone-violet-soft hover:text-fg active:scale-[0.99] motion-reduce:animate-none"
        >
          <span aria-hidden className="size-1.5 shrink-0 rounded-full bg-tone-violet-solid opacity-60 transition-opacity group-hover:opacity-100" />
          <span className="min-w-0 flex-1 truncate">{t(`chips.${k}`)}</span>
          <span aria-hidden className="shrink-0 text-fg-3 opacity-0 transition-[opacity,transform] group-hover:translate-x-0.5 group-hover:opacity-100">→</span>
        </button>
      ))}
    </div>
  );
}
