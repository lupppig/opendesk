// Settings > AI agent > Playground: chat with the widget agent without a
// visitor or a ticket (POST /v1/ai/agent/playground, streamed). It uses the
// Behaviour page's unsaved edits when there are any, or its own quick
// overrides, and an identity panel to try verified-only tools.

import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import {
  AlertCircle,
  ArrowUp,
  BookOpen,
  Check,
  ClipboardCheck,
  FileText,
  Globe,
  Hand,
  Loader2,
  MessageSquareQuote,
  RotateCcw,
  ShieldCheck,
  Square,
  UserRound,
  Wand2,
  Wrench,
  X,
} from "lucide-react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { NoAccess, SettingsPage } from "@/components/layout";
import { Button } from "@/components/ui/button";
import { Field, TextInput } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Tooltip } from "@/components/ui/tooltip";
import { ModelPicker } from "@/components/ai/ModelPicker";
import { AgentOffNotice, Eyebrow } from "@/components/agent/bits";
import { agentSettings, playgroundSettings, useAgentDraft } from "@/components/agent/settings";
import { BotFace } from "@/components/bots/BotFace";
import { BotSelect, resolveBot } from "@/components/bots/BotSelect";
import { ImproveAnswerDialog, TestEditor, toTestMessages } from "@/components/agent/quality";
import { api } from "@/lib/api/endpoints";
import { errorMessage } from "@/lib/api/client";
import type { AgentExpectation, AgentSource, AgentTestBody, Bot, PlaygroundBody, PlaygroundDone, PlaygroundMessage, PlaygroundToolEvent } from "@/lib/api/types";
import { useAI } from "@/lib/ai";
import { formatUsd } from "@/lib/format";
import { springs } from "@/lib/motion";
import { q } from "@/lib/queries";
import { atLeast, useRole } from "@/lib/session";
import { AI_TONE, tone } from "@/lib/tones";
import { cn } from "@/lib/cn";
import { sanitizeHtml } from "@/lib/sanitize";

type Turn =
  | { id: string; role: "visitor"; text: string }
  | {
      id: string;
      role: "agent";
      text: string;
      html: string | null;
      tools: PlaygroundToolEvent[];
      sources: AgentSource[];
      action: PlaygroundDone["action"];
      handoff: boolean;
      usd: number | null;
      streaming: boolean;
      error: string | null;
    };

const MAX_TURNS = 40;
const MARKERS = /\s?\[\d+\](?!\()/g;
let seq = 0;
const nextId = () => `t${++seq}`;

/** The streamed text without the `[n]` source markers. */
const stripMarkers = (s: string) => s.replace(MARKERS, "");

type AgentTurn = Extract<Turn, { role: "agent" }>;

/** The conversation as the agent sees it: visitor turns and finished agent replies. */
const toMessages = (ts: Turn[]): PlaygroundMessage[] =>
  ts.filter((x) => x.role === "visitor" || (x.role === "agent" && !x.error && x.text)).map((x) => ({ role: x.role, text: stripMarkers(x.text).slice(0, 4000) }));

export default function AgentPlayground() {
  const { t } = useTranslation("agent");
  const admin = atLeast(useRole(), "admin");
  const ai = useAI();
  const { data: models } = useQuery({ ...q.aiModels(), enabled: admin && ai.enabled });
  const { data: widget } = useQuery({ ...q.widgetSettings(), enabled: admin });
  const { data: bots } = useQuery({ ...q.bots(), enabled: admin });
  const draft = useAgentDraft((s) => s.draft);
  const saved = agentSettings(ai.features);

  // Quick overrides, used when there is no Behaviour draft.
  const [model, setModel] = useState<string | null>(null);
  const [botId, setBotId] = useState<string | null>(null);
  const [visitor, setVisitor] = useState({ name: "", email: "", verified: false });
  // Save as test and Improve answer, opened from an agent reply.
  const [saving, setSaving] = useState<Partial<AgentTestBody> | null>(null);
  const [improving, setImproving] = useState<{ question: string; answer: string } | null>(null);

  const bot = resolveBot(bots, botId);
  const defaultModel = ai.features?.model || ai.config?.models.default || models?.default || "";
  const base = draft ? playgroundSettings(draft) : model !== null ? { widget_model: model } : undefined;
  const settings: PlaygroundBody["settings"] = botId ? { ...base, bot_id: botId } : base;
  const chat = usePlaygroundChat(settings, visitor);
  const { turns } = chat;
  const suggestions = widget?.suggestions?.length ? widget.suggestions.slice(0, 4) : [t("playground.try1"), t("playground.try2"), t("playground.try3")];

  const before = (turn: AgentTurn) => turns.slice(0, turns.findIndex((x) => x.id === turn.id));
  const saveAsTest = (turn: AgentTurn) => {
    const expectations: AgentExpectation[] = [{ kind: turn.handoff ? "handoff" : "no_handoff" }];
    if (turn.sources.length) expectations.push({ kind: "cites_knowledge" });
    const email = visitor.email.trim();
    setSaving({
      messages: toTestMessages(toMessages(before(turn))),
      expectations,
      visitor: visitor.verified ? { verified: true, email: email || undefined } : undefined,
    });
  };
  const improve = (turn: AgentTurn) => {
    const asked = before(turn).filter((x) => x.role === "visitor");
    setImproving({ question: asked[asked.length - 1]?.text ?? "", answer: stripMarkers(turn.text).trim() });
  };

  if (!admin)
    return (
      <SettingsPage title={t("playground.title")}>
        <NoAccess what={t("playground.what")} />
      </SettingsPage>
    );

  return (
    <SettingsPage wide title={t("playground.title")} description={t("playground.description")}>
      {ai.config && !saved.widget_answers && !draft?.ai.widget_answers && <AgentOffNotice className="-mb-3" />}
      <div className="grid grid-cols-1 gap-5 lg:grid-cols-[minmax(0,1fr)_300px]">
        <Chat chat={chat} bot={chatBot(bot)} suggestions={suggestions} onSaveTest={saveAsTest} onImprove={improve} />
        <aside className="flex flex-col gap-4">
          <SettingsPanel
            hasDraft={!!draft}
            model={model ?? saved.widget_model}
            onModel={setModel}
            botId={botId}
            onBot={setBotId}
            overridden={model !== null || botId !== null}
            onClear={() => {
              setModel(null);
              setBotId(null);
            }}
            models={models?.data}
            defaultModel={defaultModel}
          />
          <VisitorPanel value={visitor} onChange={setVisitor} />
          <p className="px-1 text-[11.5px] leading-[17px] text-fg-3">{t("playground.note", { cost: formatUsd(ai.cost("playground") ?? 0.03, { precise: true }) })}</p>
        </aside>
      </div>
      {saving && <TestEditor initial={saving} onClose={() => setSaving(null)} />}
      {improving && <ImproveAnswerDialog question={improving.question} answer={improving.answer} onClose={() => setImproving(null)} />}
    </SettingsPage>
  );
}

/** A bot as the chat shows it. */
export type ChatBot = { name: string; look: unknown; image: string | null };
export const chatBot = (b: Pick<Bot, "name" | "look" | "image_url"> | undefined): ChatBot => (b ? { name: b.name, look: b.look, image: b.image_url } : { name: "Ollie", look: null, image: null });

/** A playground conversation: POST /v1/ai/agent/playground, streamed, with the given drafts. */
export function usePlaygroundChat(settings: PlaygroundBody["settings"], visitor: { name: string; email: string; verified: boolean }) {
  const { t } = useTranslation("agent");
  const [turns, setTurns] = useState<Turn[]>([]);
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const abort = useRef<AbortController | null>(null);

  useEffect(() => () => abort.current?.abort(), []);

  const patchLast = (fn: (a: Extract<Turn, { role: "agent" }>) => Partial<Extract<Turn, { role: "agent" }>>) =>
    setTurns((all) => {
      const i = all.length - 1;
      const last = all[i];
      if (!last || last.role !== "agent") return all;
      const next = all.slice();
      next[i] = { ...last, ...fn(last) };
      return next;
    });

  const send = async (raw: string) => {
    const msg = raw.trim().slice(0, 4000);
    if (!msg || busy) return;
    setText("");
    const history = [...turns, { id: nextId(), role: "visitor" as const, text: msg }];
    const agent: Turn = { id: nextId(), role: "agent", text: "", html: null, tools: [], sources: [], action: null, handoff: false, usd: null, streaming: true, error: null };
    setTurns([...history, agent]);
    setBusy(true);
    const ctl = new AbortController();
    abort.current = ctl;
    const messages = history
      .filter((x) => x.role === "visitor" || (x.role === "agent" && !x.error && x.text))
      .map((x) => ({ role: x.role, text: stripMarkers(x.text).slice(0, 4000) }))
      .slice(-MAX_TURNS);
    const v = { name: visitor.name.trim() || undefined, email: visitor.email.trim() || undefined, verified: visitor.verified };
    try {
      await api.ai.agent.playground(
        { messages, settings, visitor: v.name || v.email || v.verified ? v : undefined },
        (e) => {
          if (e.event === "delta") patchLast((a) => ({ text: a.text + e.data.text }));
          else if (e.event === "tool")
            patchLast((a) => {
              const i = a.tools.findIndex((x) => x.name === e.data.name && x.status === "running");
              const tools = a.tools.slice();
              if (i >= 0) tools[i] = e.data;
              else tools.push(e.data);
              return { tools };
            });
          else if (e.event === "sources") patchLast(() => ({ sources: e.data.sources }));
          else if (e.event === "done")
            patchLast(() => ({
              html: e.data.text_html ? e.data.text_html.replace(MARKERS, "") : null,
              sources: e.data.sources,
              action: e.data.action,
              handoff: e.data.handoff,
              usd: e.data.usd,
              streaming: false,
            }));
        },
        ctl.signal,
      );
      patchLast(() => ({ streaming: false }));
    } catch (err) {
      if (ctl.signal.aborted) patchLast((a) => ({ streaming: false, error: a.text ? null : t("playground.stopped") }));
      else patchLast(() => ({ streaming: false, error: errorMessage(err, t("playground.error")) }));
    } finally {
      if (abort.current === ctl) abort.current = null;
      setBusy(false);
    }
  };

  const reset = () => {
    abort.current?.abort();
    setTurns([]);
    setText("");
  };

  const spent = turns.reduce((n, x) => n + (x.role === "agent" ? (x.usd ?? 0) : 0), 0);
  return { turns, text, setText, busy, send, stop: () => abort.current?.abort(), reset, spent };
}

export type PlaygroundChat = ReturnType<typeof usePlaygroundChat>;

export function Chat({
  chat,
  bot,
  suggestions,
  onSaveTest,
  onImprove,
  className,
}: {
  chat: PlaygroundChat;
  bot: ChatBot;
  suggestions: string[];
  onSaveTest?: (turn: AgentTurn) => void;
  onImprove?: (turn: AgentTurn) => void;
  className?: string;
}) {
  const { turns, busy, text, setText, send: onSend, stop: onStop, reset: onReset, spent } = chat;
  const { t } = useTranslation("agent");
  const scroller = useRef<HTMLDivElement>(null);
  const input = useRef<HTMLTextAreaElement>(null);
  const last = turns[turns.length - 1];
  const tail = last?.role === "agent" ? last.text.length + last.tools.length : 0;

  useEffect(() => {
    const el = scroller.current;
    if (el) el.scrollTo({ top: el.scrollHeight, behavior: "smooth" });
  }, [turns.length, tail]);

  return (
    <section className={cn("flex h-[min(680px,calc(100vh-220px))] min-h-[460px] flex-col overflow-hidden rounded-xl border border-line bg-surface shadow-xs", className)} aria-label={t("playground.chat")}>
      <header className="flex h-12 shrink-0 items-center gap-2.5 border-b border-line px-4">
        <BotFace look={bot.look} image={bot.image} size={24} state={busy ? "thinking" : "idle"} blink />
        <div className="min-w-0 flex-1">
          <div className="truncate text-[13px] font-medium text-fg">{bot.name}</div>
        </div>
        {spent > 0 && (
          <Tooltip content={t("playground.spentHint")}>
            <span className="mono text-[11px] tabular-nums text-fg-3">{t("playground.spent", { cost: formatUsd(spent, { precise: true }) })}</span>
          </Tooltip>
        )}
        <Button size="xs" variant="ghost" icon={<RotateCcw className="size-3" />} onClick={onReset} disabled={!turns.length}>
          {t("playground.reset")}
        </Button>
      </header>
      <div ref={scroller} className="min-h-0 flex-1 overflow-y-auto bg-sunken/60 px-4 py-4" aria-live="polite">
        {!turns.length ? (
          <div className="flex h-full flex-col items-center justify-center gap-4 text-center">
            <BotFace look={bot.look} image={bot.image} size={44} blink />
            <div>
              <div className="text-[14px] font-medium text-fg">{t("playground.emptyTitle", { name: bot.name })}</div>
              <p className="mx-auto mt-1 max-w-[340px] text-[12.5px] leading-[18px] text-fg-3">{t("playground.emptyBody")}</p>
            </div>
            <div className="flex max-w-[460px] flex-wrap justify-center gap-1.5">
              {suggestions.map((s) => (
                <button
                  key={s}
                  type="button"
                  onClick={() => onSend(s)}
                  className="rounded-full border border-line bg-surface px-3 py-1 text-[12px] text-fg-2 shadow-xs transition-colors hover:border-line-strong hover:text-fg"
                >
                  {s}
                </button>
              ))}
            </div>
          </div>
        ) : (
          <ol className="flex flex-col gap-4">
            {turns.map((x) => (
              <li key={x.id}>{x.role === "visitor" ? <VisitorBubble text={x.text} /> : <AgentBubble turn={x} bot={bot} onSaveTest={busy || !onSaveTest ? undefined : () => onSaveTest(x)} onImprove={busy || !onImprove ? undefined : () => onImprove(x)} />}</li>
            ))}
          </ol>
        )}
      </div>
      <form
        className="flex shrink-0 items-end gap-2 border-t border-line bg-surface px-3 py-2.5"
        onSubmit={(e) => {
          e.preventDefault();
          onSend(text);
          input.current?.focus();
        }}
      >
        <textarea
          ref={input}
          rows={1}
          value={text}
          maxLength={4000}
          placeholder={t("playground.placeholder")}
          aria-label={t("playground.placeholder")}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
              e.preventDefault();
              onSend(text);
            }
          }}
          className="max-h-32 min-h-8 flex-1 resize-none bg-transparent py-1.5 text-[13px] leading-5 text-fg outline-none [field-sizing:content] placeholder:text-fg-4"
        />
        {busy ? (
          <Button square size="sm" variant="secondary" aria-label={t("playground.stop")} onClick={onStop}>
            <Square className="size-3 fill-current" />
          </Button>
        ) : (
          <Button square size="sm" variant="primary" type="submit" aria-label={t("playground.send")} disabled={!text.trim()}>
            <ArrowUp className="size-3.5" />
          </Button>
        )}
      </form>
    </section>
  );
}

function VisitorBubble({ text }: { text: string }) {
  return (
    <div className="flex justify-end">
      <div className="max-w-[80%] whitespace-pre-wrap break-words rounded-2xl rounded-br-md bg-inverse px-3.5 py-2 text-[13px] leading-5 text-inverse-fg" data-ph-mask>
        {text}
      </div>
    </div>
  );
}

const SOURCE_ICON: Record<AgentSource["kind"], typeof Globe> = { article: BookOpen, page: Globe, file: FileText, snippet: MessageSquareQuote };

function AgentBubble({
  turn,
  bot,
  onSaveTest,
  onImprove,
}: {
  turn: AgentTurn;
  bot: ChatBot;
  onSaveTest?: () => void;
  onImprove?: () => void;
}) {
  const { t } = useTranslation("agent");
  const { t: tq } = useTranslation("agentQuality");
  const finished = !turn.streaming && !turn.error && !!turn.text.trim();
  const reduce = useReducedMotion();
  const shown = stripMarkers(turn.text);
  const waiting = turn.streaming && !shown && !turn.tools.length;
  return (
    <motion.div initial={reduce ? false : { opacity: 0, y: 4 }} animate={{ opacity: 1, y: 0 }} transition={springs.snap} className="group/bubble flex items-start gap-2.5">
      <BotFace look={bot.look} image={bot.image} size={26} state={turn.streaming ? "thinking" : "idle"} className="mt-0.5" />
      <div className="flex min-w-0 max-w-[85%] flex-col gap-1.5">
        <span className="text-[11.5px] font-medium text-fg-3">{bot.name}</span>
        {turn.tools.length > 0 && (
          <ul className="flex flex-col gap-1">
            <AnimatePresence initial={false}>
              {turn.tools.map((x, i) => (
                <motion.li
                  key={`${x.name}-${i}`}
                  initial={reduce ? false : { opacity: 0, x: -4 }}
                  animate={{ opacity: 1, x: 0 }}
                  transition={springs.snap}
                  className="flex items-center gap-2 text-[12px] text-fg-3"
                >
                  <span className={cn("flex size-4 shrink-0 items-center justify-center rounded-full", x.status === "error" ? tone.red.chip : x.status === "done" ? tone.green.chip : tone[AI_TONE].chip)}>
                    {x.status === "running" ? (
                      <Loader2 className="size-2.5 animate-spin motion-reduce:animate-none" />
                    ) : x.status === "done" ? (
                      <Check className="size-2.5" />
                    ) : (
                      <X className="size-2.5" />
                    )}
                  </span>
                  <Wrench className="size-3 shrink-0 text-fg-4" />
                  <span className="min-w-0 truncate">{x.summary || x.name}</span>
                </motion.li>
              ))}
            </AnimatePresence>
          </ul>
        )}
        {waiting ? (
          <div className={cn("inline-flex h-9 w-14 items-center justify-center gap-1 rounded-2xl rounded-tl-md border", tone[AI_TONE].soft, tone[AI_TONE].line)} aria-label={t("playground.thinking")}>
            {[0, 1, 2].map((i) => (
              <span key={i} className={cn("size-1.5 rounded-full animate-pulse-soft motion-reduce:animate-none", tone[AI_TONE].solid)} style={{ animationDelay: `${i * 160}ms` }} />
            ))}
          </div>
        ) : shown || turn.html ? (
          <div className={cn("rounded-2xl rounded-tl-md border px-3.5 py-2 text-[13px] leading-5 text-fg", tone[AI_TONE].soft, tone[AI_TONE].line)} data-ph-mask>
            {turn.html && !turn.streaming ? (
              <div className="prose-od text-[13px] leading-5 [&_li]:my-0.5 [&_p:first-child]:mt-0 [&_p:last-child]:mb-0 [&_p]:my-1.5 [&_ul]:my-1 [&_ul]:pl-4" dangerouslySetInnerHTML={{ __html: sanitizeHtml(turn.html) }} />
            ) : (
              <span className="whitespace-pre-wrap break-words">
                {shown}
                {turn.streaming && <span className={cn("ml-0.5 inline-block h-3 w-0.5 translate-y-0.5 rounded-xs animate-pulse-soft motion-reduce:animate-none", tone[AI_TONE].solid)} aria-hidden />}
              </span>
            )}
          </div>
        ) : null}
        {turn.action && (
          <div className="animate-rise-in overflow-hidden rounded-lg border border-line bg-surface shadow-xs">
            <div className="flex items-center gap-2 border-b border-line-soft px-3 py-2">
              <ShieldCheck className={cn("size-3.5", tone[AI_TONE].text)} />
              <span className="flex-1 text-[12.5px] font-medium text-fg">{turn.action.title}</span>
              <span className={cn("inline-flex h-[18px] items-center rounded-xs px-1.5 text-[10.5px] font-medium", tone.amber.chip)}>{t("playground.actionPending")}</span>
            </div>
            <div className="px-3 py-2 text-[12px] leading-[18px] text-fg-2">{turn.action.summary}</div>
            <div className="flex items-center gap-2 border-t border-line-soft bg-sunken px-3 py-1.5 text-[11.5px] text-fg-3">
              <span className="mono truncate">{turn.action.tool}</span>
              <span className="ml-auto">{t("playground.actionNote")}</span>
            </div>
          </div>
        )}
        {turn.handoff && (
          <div className="flex items-center gap-2 text-[12px] text-fg-3">
            <Hand className="size-3.5 text-tone-amber" />
            {t("playground.handoff")}
          </div>
        )}
        {turn.error && (
          <div className="flex items-center gap-1.5 text-[12px] text-danger-fg" role="alert">
            <AlertCircle className="size-3.5" />
            {turn.error}
          </div>
        )}
        {(turn.sources.length > 0 || turn.usd !== null) && !turn.streaming && (
          <div className="flex flex-wrap items-center gap-1.5">
            {turn.sources.map((s) => {
              const Icon = SOURCE_ICON[s.kind] ?? BookOpen;
              const chip = (
                <>
                  <Icon className={cn("size-3 shrink-0", tone[AI_TONE].text)} />
                  <span className="truncate">{s.title}</span>
                </>
              );
              const cls = "inline-flex h-5 max-w-[220px] items-center gap-1 rounded-sm border border-line bg-surface px-1.5 text-[11px] text-fg-2";
              return s.url ? (
                <a key={`${s.kind}-${s.id}`} href={s.url} target="_blank" rel="noopener noreferrer" className={cn(cls, "transition-colors hover:border-line-strong hover:text-fg")}>
                  {chip}
                </a>
              ) : (
                <span key={`${s.kind}-${s.id}`} className={cls}>
                  {chip}
                </span>
              );
            })}
            {turn.usd !== null && (
              <span className="mono ml-auto text-[10.5px] tabular-nums text-fg-4">{t("playground.replyCost", { cost: formatUsd(turn.usd, { precise: true }) })}</span>
            )}
          </div>
        )}
        {finished && onSaveTest && onImprove && (
          <div className="flex items-center gap-1 opacity-0 transition-opacity duration-150 focus-within:opacity-100 group-hover/bubble:opacity-100 motion-reduce:transition-none">
            <Button size="xs" variant="ghost" icon={<ClipboardCheck className="size-3" />} onClick={onSaveTest}>
              {tq("playground.saveTest")}
            </Button>
            <Button size="xs" variant="ghost" icon={<Wand2 className="size-3" />} onClick={onImprove}>
              {tq("playground.improve")}
            </Button>
          </div>
        )}
      </div>
    </motion.div>
  );
}

function SettingsPanel({
  hasDraft,
  model,
  onModel,
  botId,
  onBot,
  overridden,
  onClear,
  models,
  defaultModel,
}: {
  hasDraft: boolean;
  model: string;
  onModel: (v: string) => void;
  botId: string | null;
  onBot: (v: string | null) => void;
  overridden: boolean;
  onClear: () => void;
  models: Parameters<typeof ModelPicker>[0]["models"];
  defaultModel: string;
}) {
  const { t } = useTranslation("agent");
  if (hasDraft)
    return (
      <div className={cn("flex flex-col gap-2 rounded-lg border px-4 py-3", tone[AI_TONE].soft, tone[AI_TONE].line)}>
        <div className="text-[12.5px] font-medium text-fg">{t("playground.draftTitle")}</div>
        <p className="text-[12px] leading-[18px] text-fg-3">{t("playground.draftBody")}</p>
        <Link to="/settings/ai/behaviour" className="text-[12.5px] font-medium text-fg-2 underline decoration-line-strong underline-offset-2 hover:text-fg">
          {t("playground.draftBack")}
        </Link>
      </div>
    );
  return (
    <div className="flex flex-col gap-3 rounded-lg border border-line bg-surface px-4 py-3 shadow-xs">
      <div className="flex items-center">
        <Eyebrow className="flex-1">{t("playground.overrides")}</Eyebrow>
        {overridden && (
          <Button size="xs" variant="ghost" onClick={onClear}>
            {t("playground.useSaved")}
          </Button>
        )}
      </div>
      <Field label={t("behaviour.widgetModel")}>
        {(p) => (
          <ModelPicker id={p.id} value={model} onChange={onModel} models={models} defaultId={defaultModel} defaultLabel={t("behaviour.sameAsMain")} aria-label={t("behaviour.widgetModel")} />
        )}
      </Field>
      <Field label={t("playground.answerAs")}>{(p) => <BotSelect id={p.id} size="sm" value={botId} onChange={onBot} />}</Field>
      <p className="text-[11.5px] leading-[17px] text-fg-3">
        {t("playground.overridesHint")}{" "}
        <Link to="/settings/ai/behaviour" className="text-fg-2 underline decoration-line-strong underline-offset-2 hover:text-fg">
          {t("playground.overridesLink")}
        </Link>
      </p>
    </div>
  );
}

function VisitorPanel({ value, onChange }: { value: { name: string; email: string; verified: boolean }; onChange: (v: { name: string; email: string; verified: boolean }) => void }) {
  const { t } = useTranslation("agent");
  const emailOk = useMemo(() => !value.email || /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value.email.trim()), [value.email]);
  return (
    <div className="flex flex-col gap-3 rounded-lg border border-line bg-surface px-4 py-3 shadow-xs">
      <Eyebrow>
        <span className="inline-flex items-center gap-1.5">
          <UserRound className="size-3" />
          {t("playground.visitor")}
        </span>
      </Eyebrow>
      <Field label={t("playground.visitorName")} optional>
        {(p) => <TextInput {...p} inputSize="sm" value={value.name} placeholder="Ada Lovelace" onChange={(e) => onChange({ ...value, name: e.target.value })} />}
      </Field>
      <Field label={t("playground.visitorEmail")} optional error={emailOk ? null : t("playground.emailInvalid")}>
        {(p) => <TextInput {...p} inputSize="sm" type="email" value={value.email} placeholder="ada@example.com" onChange={(e) => onChange({ ...value, email: e.target.value })} />}
      </Field>
      <label className="flex items-start justify-between gap-3">
        <span className="min-w-0">
          <span className="block text-[12.5px] font-medium text-fg">{t("playground.verified")}</span>
          <span className="block text-[11.5px] leading-[17px] text-fg-3">{t("playground.verifiedHint")}</span>
        </span>
        <Switch checked={value.verified} onCheckedChange={(verified) => onChange({ ...value, verified })} aria-label={t("playground.verified")} />
      </label>
    </div>
  );
}
