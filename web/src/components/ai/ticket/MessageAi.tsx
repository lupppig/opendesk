// Per-message AI: translate a customer message written in another language,
// and label answers the widget bot wrote.

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { Message, Ticket } from "@/lib/api/types";
import { api } from "@/lib/api/endpoints";
import { useAI } from "@/lib/ai";
import { q, qk } from "@/lib/queries";
import { useRole, atLeast } from "@/lib/session";
import { cn } from "@/lib/cn";
import { AI_TONE, tone } from "@/lib/tones";
import { Spinner } from "@/components/ui/spinner";
import { Tooltip } from "@/components/ui/tooltip";
import { AiIcon, AiTooltip } from "../bits";
import { balanceSpent, reportAiError } from "./errors";
import { isAiMessage, isForeign, languageName, messageLanguage, streamTextToHtml } from "./text";

const OURS = "en";

export type MessageTranslation = {
  /** Language to offer translation from, or null when none is needed. */
  from: string | null;
  showing: boolean;
  pending: boolean;
  html: string | null;
  toggle: () => void;
};

export function useMessageTranslation(m: Message): MessageTranslation {
  const { t } = useTranslation("aiTicket");
  const ai = useAI();
  const role = useRole();
  const qc = useQueryClient();
  // The message list doesn't carry the ticket; read the cached one (subscribing, never fetching).
  const { data: byId } = useQuery({ ...q.ticket(m.ticket_id), enabled: false });
  const ticket = byId ?? qc.getQueriesData<Ticket>({ queryKey: qk.of("tickets", "detail") }).find(([, d]) => d?.id === m.ticket_id)?.[1];
  const [requested, setRequested] = useState(false);
  const [showing, setShowing] = useState(false);
  const lang = messageLanguage(m, ticket);
  const eligible = ai.enabled && atLeast(role, "agent") && m.direction === "inbound" && m.kind === "reply" && isForeign(lang, OURS);

  const res = useQuery({
    queryKey: qk.of("ai", "translate", m.id, OURS),
    queryFn: async () => {
      try {
        const r = await api.ai.translate({ html: m.body_html || undefined, text: m.body_html ? undefined : m.body_text, target: OURS });
        balanceSpent(qc);
        return r.html ?? streamTextToHtml(r.text ?? "");
      } catch (e) {
        reportAiError(e, t("message.failed"));
        setShowing(false);
        setRequested(false);
        throw e;
      }
    },
    enabled: eligible && requested,
    staleTime: Infinity,
    gcTime: 30 * 60_000,
    retry: false,
  });

  return {
    from: eligible ? lang : null,
    showing: showing && !!res.data,
    pending: requested && res.isFetching,
    html: res.data ?? null,
    toggle: () => {
      if (!requested) setRequested(true);
      setShowing((v) => !v || !res.data);
    },
  };
}

/** The small "Translate" control in a message header. */
export function TranslateButton({ tr }: { tr: MessageTranslation }) {
  const { t } = useTranslation("aiTicket");
  if (!tr.from || tr.showing) return null;
  const label = tr.pending ? t("message.translating") : tr.showing ? t("message.showOriginal") : tr.html ? t("message.showTranslation") : t("message.translate");
  const button = (
    <button
      type="button"
      onClick={tr.toggle}
      disabled={tr.pending}
      aria-pressed={tr.showing}
      className="flex h-5 shrink-0 items-center gap-1 rounded px-1.5 text-[11px] text-fg-3 opacity-80 transition-opacity hover:bg-hover hover:text-fg hover:opacity-100 group-hover/msg:opacity-100 disabled:opacity-60"
    >
      {tr.pending ? <Spinner className="size-3" /> : <AiIcon className="size-3" />}
      {label}
    </button>
  );
  if (tr.html) return button;
  return (
    <AiTooltip feature="translate" label={t("message.translateHint", { language: languageName(tr.from) })}>
      {button}
    </AiTooltip>
  );
}

/** "Translated from Spanish" line shown above a translated body. */
export function TranslatedNote({ tr }: { tr: MessageTranslation }) {
  const { t } = useTranslation("aiTicket");
  if (!tr.showing || !tr.from) return null;
  return (
    <p className="mb-1.5 flex items-center gap-1.5 text-[11px] text-fg-4">
      <AiIcon className="size-3" />
      {t("message.translatedFrom", { language: languageName(tr.from) })}
      <span aria-hidden>·</span>
      <button type="button" onClick={tr.toggle} className="underline-offset-2 hover:text-fg hover:underline">
        {t("message.showOriginal")}
      </button>
    </p>
  );
}

/** A quiet "AI" label next to the author of a widget bot answer. */
export function AiAuthorLabel({ m }: { m: Message }) {
  const { t } = useTranslation("aiTicket");
  if (!isAiMessage(m)) return null;
  return (
    <Tooltip content={t("message.aiHint")}>
      <span className={cn("inline-flex h-4 shrink-0 items-center gap-1 rounded-sm px-1 text-[10px] font-medium", tone[AI_TONE].chip)}>
        <AiIcon className="size-2.5 text-current" />
        {t("message.aiLabel")}
      </span>
    </Tooltip>
  );
}
