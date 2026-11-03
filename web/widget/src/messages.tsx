/** @jsxImportSource preact */
// Message rows: visitor and team bubbles, the agent's replies (posted and
// live), action cards, sources and ratings.

import type { JSX } from "preact";
import { useEffect, useMemo, useRef, useState } from "preact/hooks";
import { actionStatus, keyBot } from "./ai";
import { botShown, readBotRef } from "./config";
import type { Controller } from "./controller";
import { hostOf, initials, safeHtml, size, splitSources } from "./html";
import { clock, t } from "./i18n";
import { Bolt, BotFace, Check, Clock, Cross, FileIc, LinkIc, Person, Spinner, ThumbDown, ThumbUp } from "./icons";
import type { LiveReply } from "./live";
import { renderMarkdown } from "./markdown";
import type { ActionUi } from "./store";
import { VoicePlayer } from "./voice";
import type { AIAction, AIActionStatus, Attachment, BotRef, Rating, TeamMember, UiMessage } from "./types";

export const reducedMotion = () => typeof matchMedia === "function" && matchMedia("(prefers-reduced-motion: reduce)").matches;

export const Dots = () => (
  <span class="dots" aria-hidden="true">
    <span />
    <span />
    <span />
  </span>
);

/**
 * A bot's avatar: its uploaded picture (an image look), round, else its look
 * drawn by the shared renderer. A picture that fails to load falls back to the look.
 */
export function BotAvatar({ bot, size: s = 18, anim }: { bot: BotRef; size?: number; anim?: boolean }) {
  const [broken, setBroken] = useState<string | null>(null);
  const pic = bot.look.kind === "image" && bot.avatar_url && broken !== bot.avatar_url ? bot.avatar_url : null;
  return (
    <span class={`av bot-av${pic ? " is-pic" : ""}`} style={{ width: `${s}px`, height: `${s}px` }} aria-hidden="true">
      {pic ? <img src={pic} alt="" referrerpolicy="no-referrer" onError={() => setBroken(pic)} /> : <BotFace look={bot.look} anim={anim} />}
    </span>
  );
}

export function PersonAvatar({ p, size: s = 18, one }: { p: TeamMember; size?: number; one?: boolean }) {
  return (
    <span class="av" style={{ width: `${s}px`, height: `${s}px`, fontSize: `${Math.round(s * 0.42)}px` }} aria-hidden="true">
      {p.avatar_url ? <img src={p.avatar_url} alt="" referrerpolicy="no-referrer" /> : one ? p.name.trim().charAt(0).toUpperCase() || "?" : initials(p.name)}
    </span>
  );
}

/** Overlapping avatars: the agent first (when it answers), then up to 3 team members. */
export function AvatarStack({ team, bot, size: s = 24 }: { team: TeamMember[]; bot?: BotRef | null; size?: number }) {
  if (!team.length && !bot) return null;
  return (
    <span class="stack" aria-hidden="true">
      {bot && <BotAvatar bot={bot} size={s} anim />}
      {team.slice(0, 3).map((p, i) => (
        <PersonAvatar key={`${p.name}-${i}`} p={p} size={s} one />
      ))}
    </span>
  );
}

function BotHead({ bot, role }: { bot: BotRef; role?: string }) {
  return (
    <div class="who">
      <BotAvatar bot={bot} />
      <span class="who-name">{bot.name}</span>
      {role && (
        <span class="who-role" dir="auto">
          {role}
        </span>
      )}
      <span class="ai-pill" title={t("aiAnswer")}>
        <span class="sr">{t("aiAnswer")}</span>
        <span aria-hidden="true">{t("ai")}</span>
      </span>
    </div>
  );
}

// ── Visitor and team ─────────────────────────────────────────────────────────

export function Bubble({ m, me, grouped, onRetry }: { m: UiMessage; me: boolean; grouped: boolean; onRetry: () => void }) {
  const who = m.author.user?.name ?? (m.author.type === "system" ? t("automated") : t("support"));
  const hasBody = me ? !!m.body_text : !!(m.body_html || m.body_text);
  // A reply an API key wrote as a bot shows the bot, with the AI pill.
  const bot = me ? null : keyBot(m);
  return (
    <div class={`row ${me ? "me" : "them"}${grouped ? "" : " gap"}${m.local === "pending" ? " is-pending" : ""}`}>
      {!me && !grouped && bot && <BotHead bot={bot} />}
      {!me && !grouped && !bot && (
        <div class="who">
          <PersonAvatar p={{ name: who, avatar_url: m.author.user?.avatar_url || null }} />
          <span class="who-name">{who}</span>
        </div>
      )}
      {hasBody &&
        (me || !m.body_html ? (
          <div class="msg" dir="auto">
            {m.body_text}
          </div>
        ) : (
          <div class="msg" dangerouslySetInnerHTML={{ __html: safeHtml(m.body_html) }} />
        ))}
      {m.attachments.length > 0 && (
        <div class="files">
          {m.attachments.map((a) => (
            <FileView key={a.id} a={a} />
          ))}
        </div>
      )}
      {m.local === "failed" ? (
        <div class="meta failed">
          {t("notSent")} <button onClick={onRetry}>{t("retry")}</button>
        </div>
      ) : m.local === "pending" ? (
        <div class="meta">{t("sending")}</div>
      ) : !grouped || me ? (
        <div class="meta">{clock(m.created_at)}</div>
      ) : null}
    </div>
  );
}

export function FileView({ a }: { a: Attachment }) {
  if (a.content_type.startsWith("audio/") && a.url) return <VoicePlayer src={a.url} />;
  if (a.content_type.startsWith("image/") && a.url)
    return (
      <a href={a.url} target="_blank" rel="noopener noreferrer">
        <img class="thumb" src={a.url} alt={a.filename} loading="lazy" />
      </a>
    );
  return (
    <a class="chip" href={a.url || undefined} target="_blank" rel="noopener noreferrer">
      <FileIc />
      <span>{a.filename}</span>
      <small>{size(a.size_bytes)}</small>
    </a>
  );
}

// ── The agent's posted replies ───────────────────────────────────────────────

export type Offer = "idle" | "pending" | "failed" | null;

export function AiMessage({
  c,
  m,
  convId,
  bot,
  offer,
  swapped,
  rating,
  action,
  landed,
}: {
  c: Controller;
  m: UiMessage;
  convId: string | null;
  bot: BotRef;
  offer: Offer;
  swapped: boolean;
  rating: Rating | null;
  action: ActionUi | undefined;
  /** It just landed: the bot hops once. */
  landed?: boolean;
}) {
  const body = useMemo(() => (m.body_html ? splitSources(safeHtml(m.body_html)) : null), [m.body_html]);
  const seen = useMemo(() => readBotRef(m.author.bot), [m.author.bot]);
  return (
    <div class={`row them gap ai${swapped ? " no-enter" : ""}`} data-bot-state={landed ? "happy" : undefined}>
      <BotHead bot={botShown(seen, bot)} />
      {body ? <div class="msg" dangerouslySetInnerHTML={{ __html: body.html }} /> : <div class="msg" dir="auto">{m.body_text}</div>}
      {body && body.sources.length > 0 && (
        <div class="sources" aria-label={t("sources")}>
          {body.sources.map((s, i) => (
            <a key={s.href} class="src" href={s.href} target="_blank" rel="noopener noreferrer" style={{ "--i": i }} title={hostOf(s.href)}>
              <LinkIc />
              <span>{s.title}</span>
            </a>
          ))}
        </div>
      )}
      {m.ai_action && convId && <ActionCard a={m.ai_action} ui={action} onDecide={(d) => void c.decide(convId, m.ai_action!, d)} />}
      {offer && (
        <div class="offer">
          <button class="handoff" disabled={offer === "pending"} aria-busy={offer === "pending"} onClick={() => convId && void c.handoff(convId)}>
            {offer === "pending" ? <Spinner /> : Person()}
            {t("talkToPerson")}
          </button>
          {offer === "failed" && <div class="meta failed">{t("handoffFailed")}</div>}
        </div>
      )}
      <div class="meta-row">
        <span class="meta">{clock(m.created_at)}</span>
        {convId && !m.local && (
          <span class={`rate${rating ? " rated" : ""}`}>
            <button class={`rate-btn${rating === "up" ? " on" : ""}`} aria-pressed={rating === "up"} aria-label={t("rateUp")} title={t("rateUp")} onClick={() => void c.rate(convId, m, "up")}>
              <ThumbUp />
            </button>
            <button class={`rate-btn${rating === "down" ? " on" : ""}`} aria-pressed={rating === "down"} aria-label={t("rateDown")} title={t("rateDown")} onClick={() => void c.rate(convId, m, "down")}>
              <ThumbDown />
            </button>
          </span>
        )}
      </div>
    </div>
  );
}

const STATUS: Record<Exclude<AIActionStatus, "pending">, { label: () => string; icon: () => JSX.Element }> = {
  confirmed: { label: () => t("actionConfirmed"), icon: () => Check(11) },
  declined: { label: () => t("actionDeclined"), icon: () => Cross(10) },
  expired: { label: () => t("actionExpired"), icon: () => Clock(11) },
  failed: { label: () => t("actionFailed"), icon: () => Cross(10) },
};

export function ActionCard({ a, ui, onDecide }: { a: AIAction; ui: ActionUi | undefined; onDecide: (d: "confirm" | "decline") => void }) {
  const status = actionStatus(a, ui);
  const busy = status === "pending" ? ui?.busy : undefined;
  return (
    <div class={`action s-${status}`} role="group" aria-label={a.title}>
      <div class="action-top">
        <span class="action-tool">
          {Bolt(11)}
          {a.tool}
        </span>
        {status === "pending" ? (
          <span class="action-need">{t("actionPending")}</span>
        ) : (
          <span class={`pill p-${status}`} role="status">
            {STATUS[status].icon()}
            {STATUS[status].label()}
          </span>
        )}
      </div>
      <div class="action-title">{a.title}</div>
      {a.summary && <div class="action-sum">{a.summary}</div>}
      <div class="action-btns" data-open={status === "pending" ? "" : undefined}>
        <div>
          <button class="btn ghost" disabled={!!busy || status !== "pending"} aria-busy={busy === "decline"} onClick={() => onDecide("decline")}>
            {busy === "decline" && <Spinner />}
            {t("cancel")}
          </button>
          <button class="btn solid" disabled={!!busy || status !== "pending"} aria-busy={busy === "confirm"} onClick={() => onDecide("confirm")}>
            {busy === "confirm" && <Spinner />}
            {t("confirm")}
          </button>
        </div>
      </div>
      {ui?.error && status === "pending" && <div class="err">{t("actionError")}</div>}
    </div>
  );
}

// ── Live preview of a reply being written ────────────────────────────────────

/** Reveals streamed text at a steady pace that catches up with bursts. */
function useSmoothText(target: string, live: boolean): string {
  const [shown, setShown] = useState(() => (live ? 0 : target.length));
  const want = useRef(target.length);
  want.current = target.length;
  useEffect(() => {
    if (reducedMotion()) {
      setShown(target.length);
      return;
    }
    if (shown >= target.length) return;
    const id = requestAnimationFrame(() => setShown((n) => Math.min(want.current, n + Math.max(2, Math.ceil((want.current - n) * 0.18)))));
    return () => cancelAnimationFrame(id);
  }, [shown, target]);
  return target.slice(0, Math.min(shown, target.length));
}

function StreamText({ text, writing }: { text: string; writing: boolean }) {
  const shown = useSmoothText(text, true);
  const nodes = useMemo(() => renderMarkdown(shown, true), [shown]);
  return <div class={`md${writing || shown.length < text.length ? " writing" : ""}`}>{nodes}</div>;
}

function ToolLine({ name, status }: { name: string; status: "running" | "done" | "error" }) {
  return (
    <div class={`tool t-${status}`}>
      <span class="tool-ic">{status === "running" ? <Spinner /> : status === "done" ? Check(11) : Cross(10)}</span>
      <span>{status === "running" ? t("checking", { tool: name }) : status === "done" ? t("checked", { tool: name }) : t("toolFailed", { tool: name })}</span>
    </div>
  );
}

export function LiveReplyView({ r, bot }: { r: LiveReply; bot: BotRef }) {
  const showText = !r.partial && r.text.trim() !== "";
  const who = botShown(r.bot, bot);
  const name = who.name;
  const writing = r.phase === "typing" || r.phase === "streaming";
  return (
    <div class={`row them gap ai live${r.phase === "leaving" ? " leaving" : ""}`} aria-busy="true" data-bot-state={writing ? "thinking" : undefined}>
      <BotHead bot={who} />
      <div class="msg">
        {r.tools.length > 0 && (
          <div class="tools">
            {r.tools.map((tl, i) => (
              <ToolLine key={i} name={tl.name} status={tl.status} />
            ))}
          </div>
        )}
        {showText ? (
          <StreamText text={r.text} writing={r.phase === "streaming"} />
        ) : (
          !r.tools.some((x) => x.status === "running") && (
            <span class="typing-dots" role="status" aria-label={t("botTyping", { name })}>
              <Dots />
            </span>
          )
        )}
      </div>
    </div>
  );
}

/** The bot's opening message in a new chat (its greeting), attributed to it. */
export function BotGreeting({ bot, text }: { bot: BotRef & { role?: string }; text: string }) {
  return (
    <div class="row them gap ai greeting">
      <BotHead bot={bot} role={bot.role} />
      <div class="msg" dir="auto">
        {text}
      </div>
    </div>
  );
}

/** Calm note at the end of the thread once a person takes over. */
export function HandedOff() {
  return (
    <div class="handed" role="status">
      <span class="handed-ic">{Person(15)}</span>
      <span class="handed-text">
        <b>{t("handedOff")}</b>
        <span>{t("handedOffHint")}</span>
      </span>
    </div>
  );
}
