// The widget agent in the ticket thread (contracts/agent.md): its reply as it
// streams ("live" pushes on the ticket channel), the action card a visitor is
// asked to confirm, and the visitor's rating of a reply. All read-only here.

import { Check, Hand, Loader2, ShieldCheck, ThumbsDown, ThumbsUp, Wrench, X } from "lucide-react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { useTranslation } from "react-i18next";
import type { AIAction, AIActionStatus, Message } from "@/lib/api/types";
import { useRealtime } from "@/lib/realtime/store";
import { springs } from "@/lib/motion";
import { AI_TONE, tone, type Tone } from "@/lib/tones";
import { cn } from "@/lib/cn";
import { Tooltip } from "@/components/ui/tooltip";
import { OllieAvatar } from "../ollie";
import { BotFace } from "@/components/bots/BotFace";

const ACTION_TONE: Record<AIActionStatus, Tone> = { pending: "amber", confirmed: "green", declined: "gray", expired: "gray", failed: "red" };

/** Hides the `[n]` source markers of streamed text. */
const stripMarkers = (s: string) => s.replace(/\s?\[\d+\](?!\()/g, "");

/** What the agent asked the visitor to confirm, with where it stands. */
export function AiActionCard({ action }: { action: AIAction }) {
  const { t } = useTranslation("inbox");
  const hue = ACTION_TONE[action.status];
  return (
    <div className="mt-3 overflow-hidden rounded-lg border border-line bg-surface">
      <div className="flex items-center gap-2 border-b border-line-soft px-3 py-2">
        <ShieldCheck className={cn("size-3.5 shrink-0", tone[AI_TONE].text)} />
        <span className="min-w-0 flex-1 truncate text-[12.5px] font-medium text-fg">{action.title}</span>
        <span className={cn("inline-flex h-5 shrink-0 items-center gap-1.5 rounded-sm px-1.5 text-[11px] font-medium", tone[hue].chip)}>
          <span className={cn("size-1.5 rounded-full", tone[hue].solid, action.status === "pending" && "animate-pulse-soft motion-reduce:animate-none")} aria-hidden />
          {t(`agent.action.${action.status}`)}
        </span>
      </div>
      <p className="px-3 py-2 text-[12.5px] leading-[18px] text-fg-2" data-ph-mask>
        {action.summary}
      </p>
      <div className="flex items-center gap-2 border-t border-line-soft bg-sunken px-3 py-1.5 text-[11px] text-fg-3">
        <Wrench className="size-3 shrink-0" />
        <span className="mono truncate">{action.tool}</span>
        <span className="ml-auto shrink-0">{t(`agent.actionNote.${action.status}`)}</span>
      </div>
    </div>
  );
}

/** The visitor's thumbs up or down on an agent reply. */
export function AiRating({ rating }: { rating: "up" | "down" }) {
  const { t } = useTranslation("inbox");
  const up = rating === "up";
  return (
    <Tooltip content={up ? t("agent.ratedUp") : t("agent.ratedDown")}>
      <span
        className={cn("inline-flex size-5 shrink-0 items-center justify-center rounded-sm", up ? tone.green.chip : tone.red.chip)}
        aria-label={up ? t("agent.ratedUp") : t("agent.ratedDown")}
        role="img"
      >
        {up ? <ThumbsUp className="size-3" /> : <ThumbsDown className="size-3" />}
      </span>
    </Tooltip>
  );
}

/**
 * The agent's reply while it is being written. It gives way to the real
 * message once that is in the thread (message.created).
 */
export function LiveAgentReply({ ticketId, messages, className }: { ticketId: string; messages: Message[] | undefined; className?: string }) {
  const { t } = useTranslation("inbox");
  const reduce = useReducedMotion();
  const live = useRealtime((s) => s.live[ticketId]);
  const landed = !!live?.message_id && !!messages?.some((m) => m.id === live.message_id);
  const show = !!live && !landed && (!live.ended || !!live.message_id || !!live.handoff);
  const text = live ? stripMarkers(live.text) : "";

  return (
    <AnimatePresence initial={false}>
      {show && live && (
        <motion.article
          key={live.reply_id}
          initial={reduce ? false : { opacity: 0, y: 6 }}
          animate={{ opacity: 1, y: 0 }}
          exit={reduce ? { opacity: 0, transition: { duration: 0 } } : { opacity: 0, transition: { duration: 0.12 } }}
          transition={springs.snap}
          aria-live="polite"
          aria-label={t("agent.writing", { name: live.bot })}
          className={cn("rounded-lg border", tone[AI_TONE].soft, tone[AI_TONE].line, className)}
        >
          <header className="flex items-center gap-2.5 px-4 pt-3">
            {live.botLook ? <BotFace look={live.botLook} image={live.botAvatar} size={24} state={live.ended ? "idle" : "thinking"} /> : <OllieAvatar size={24} />}
            <span className="truncate text-[13px] font-medium">{live.bot}</span>
            <span className={cn("text-[11.5px]", tone[AI_TONE].text)}>{live.ended ? t("agent.posting") : t("agent.writingShort")}</span>
          </header>
          <div className="flex flex-col gap-2 px-4 pb-3.5 pl-[50px] pt-1">
            {live.tools.length > 0 && (
              <ul className="flex flex-col gap-1">
                {live.tools.map((x, i) => (
                  <li key={`${x.tool}-${i}`} className="flex items-center gap-2 text-[12px] text-fg-3">
                    <span className={cn("flex size-4 shrink-0 items-center justify-center rounded-full", x.status === "error" ? tone.red.chip : x.status === "done" ? tone.green.chip : tone[AI_TONE].chip)}>
                      {x.status === "running" ? <Loader2 className="size-2.5 animate-spin motion-reduce:animate-none" /> : x.status === "done" ? <Check className="size-2.5" /> : <X className="size-2.5" />}
                    </span>
                    <span className="mono truncate">{x.tool}</span>
                  </li>
                ))}
              </ul>
            )}
            {text ? (
              <p className="whitespace-pre-wrap break-words text-[13.5px] leading-[21px] text-fg" data-ph-mask>
                {text}
                {!live.ended && <span className={cn("ml-0.5 inline-block h-3.5 w-0.5 translate-y-0.5 rounded-xs animate-pulse-soft motion-reduce:animate-none", tone[AI_TONE].solid)} aria-hidden />}
              </p>
            ) : !live.ended ? (
              <span className="inline-flex items-center gap-1" aria-label={t("agent.thinking")}>
                {[0, 1, 2].map((i) => (
                  <span key={i} className={cn("size-1.5 rounded-full animate-pulse-soft motion-reduce:animate-none", tone[AI_TONE].solid)} style={{ animationDelay: `${i * 160}ms` }} />
                ))}
              </span>
            ) : null}
            {live.ended && live.handoff && (
              <span className="flex items-center gap-1.5 text-[12px] text-fg-3">
                <Hand className="size-3.5 text-tone-amber" />
                {t("agent.handingOff")}
              </span>
            )}
          </div>
        </motion.article>
      )}
    </AnimatePresence>
  );
}
