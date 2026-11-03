// Per-message AI: translate a customer message into the reader's language (the
// app's), offered whenever the message isn't known to be in it already, and label
// answers the widget bot wrote.

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
import { uiLanguage } from "@/i18n";
import { isAiMessage, isForeign, isLanguage, languageName, messageLanguage, streamTextToHtml } from "./text";

export type MessageTranslation = {
  /** Whether a translation is offered. */
  offered: boolean;
  /** The message's detected language, or null when none was detected. */
  from: string | null;
  /** The reader's language, the translation's target. */
  to: string;
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
  const ours = uiLanguage();
  // Without a detected language (no triage), offer it anyway: the reader knows best.
  const eligible = ai.enabled && atLeast(role, "agent") && m.direction === "inbound" && m.kind === "reply" &&
    (!isLanguage(lang) || isForeign(lang, ours));

  const res = useQuery({
    queryKey: qk.of("ai", "translate", m.id, ours),
    queryFn: async () => {
      try {
        const r = await api.ai.translate({ html: m.body_html || undefined, text: m.body_html ? undefined : m.body_text, target: ours });
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
    offered: eligible,
    from: eligible && isLanguage(lang) ? lang : null,
    to: ours,
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
  if (!tr.offered || tr.showing) return null;
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
    <AiTooltip feature="translate" label={tr.from ? t("message.translateHint", { language: languageName(tr.from, tr.to) })
      : t("message.translateInto", { language: languageName(tr.to, tr.to) })}>
      {button}
    </AiTooltip>
  );
}

/** "Translated from Spanish" line shown above a translated body. */
export function TranslatedNote({ tr }: { tr: MessageTranslation }) {
  const { t } = useTranslation("aiTicket");
  if (!tr.showing || !tr.offered) return null;
  return (
    <p className="mb-1.5 flex items-center gap-1.5 text-[11px] text-fg-4">
      <AiIcon className="size-3" />
      {tr.from ? t("message.translatedFrom", { language: languageName(tr.from, tr.to) })
        : t("message.translatedInto", { language: languageName(tr.to, tr.to) })}
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
  // An API key that speaks as a bot is an AI of the team's own (contracts/api.md "API keys").
  const viaKey = !m.ai && m.author.type === "api" && !!m.author.bot;
  if (!isAiMessage(m) && !viaKey) return null;
  return (
    <Tooltip content={viaKey ? t("message.apiBotHint", { key: m.author.api_key?.name ?? "API" }) : t("message.aiHint")}>
      <span className={cn("inline-flex h-4 shrink-0 items-center gap-1 rounded-sm px-1 text-[10px] font-medium", tone[AI_TONE].chip)}>
        <AiIcon className="size-2.5 text-current" />
        {t("message.aiLabel")}
      </span>
    </Tooltip>
  );
}
