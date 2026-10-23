// Ollie's panel. Docked, it is a column beside the work panel that slides open;
// floating, it is a card you can drag anywhere. On phones it is a sheet.
// Esc closes it, ⌘Enter approves the change waiting in it.

import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { History, PanelRight, Plus, X, Maximize2 } from "lucide-react";
import { useEffect, useLayoutEffect, useRef, useState, type PointerEvent as RPointerEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Tooltip } from "@/components/ui/tooltip";
import { cn } from "@/lib/cn";
import { Composer, type ComposerHandle } from "./Composer";
import { OllieMark } from "./OllieMark";
import { Markdown } from "./Markdown";
import { useAssistantName } from "./name";
import { SessionList } from "./SessionList";
import { ApprovalCard, StepRow } from "./Steps";
import { ollie, useOllie, type Entry } from "./store";
import { Suggestions } from "./Suggestions";
import type { AssistantRef } from "./api";

export const PANEL_WIDTH = 400;
const EASE = [0.23, 1, 0.32, 1] as const;

function HeadButton({ label, onClick, children, active, shortcut }: { label: string; onClick: () => void; children: ReactNode; active?: boolean; shortcut?: string }) {
  return (
    <Tooltip content={label} shortcut={shortcut} side="bottom">
      <button
        type="button"
        aria-label={label}
        aria-pressed={active}
        onClick={onClick}
        onPointerDown={(e) => e.stopPropagation()}
        className={cn("flex size-7 items-center justify-center rounded-md text-fg-3 transition-colors hover:bg-hover hover:text-fg", active && "bg-active text-fg")}
      >
        {children}
      </button>
    </Tooltip>
  );
}

function Header({ onDragStart }: { onDragStart?: (e: RPointerEvent) => void }) {
  const { t } = useTranslation("assistant");
  const name = useAssistantName();
  const { title, mode, showSessions, running, approval, lastOk } = useOllie();
  const state = approval ? "attention" : running ? "thinking" : "idle";
  return (
    <div
      onPointerDown={onDragStart}
      className={cn("flex h-11 shrink-0 items-center gap-2 border-b border-line-soft pl-3 pr-1.5", onDragStart && "cursor-grab touch-none select-none active:cursor-grabbing")}
    >
      <OllieMark size={22} state={state} celebrate={lastOk} listen />
      <div className="flex min-w-0 flex-1 flex-col leading-tight">
        <span className="text-[13px] font-semibold text-fg">{name}</span>
        <span className="truncate text-[11px] text-fg-4" data-ph-mask>
          {showSessions ? t("history") : title || t("untitled")}
        </span>
      </div>
      <HeadButton label={t("history")} active={showSessions} onClick={() => ollie.sessions()}>
        <History className="size-3.5" />
      </HeadButton>
      <HeadButton label={t("newChat")} onClick={() => ollie.newChat()}>
        <Plus className="size-3.5" />
      </HeadButton>
      <span className="hidden md:contents">
        <HeadButton label={mode === "docked" ? t("float") : t("dock")} onClick={() => ollie.setMode(mode === "docked" ? "floating" : "docked")}>
          {mode === "docked" ? <Maximize2 className="size-3.5" /> : <PanelRight className="size-3.5" />}
        </HeadButton>
      </span>
      <HeadButton label={t("close", { name })} shortcut="mod j" onClick={() => ollie.open(false)}>
        <X className="size-3.5" />
      </HeadButton>
    </div>
  );
}

function refsOf(e: Entry): AssistantRef[] {
  const out: AssistantRef[] = [];
  for (const b of e.blocks) if (b.kind === "step") out.push(...(b.step.refs ?? []));
  return out;
}

function Thinking() {
  const { t } = useTranslation("assistant");
  const name = useAssistantName();
  return (
    <div className="flex h-6 items-center gap-2 text-[12px] text-fg-3" role="status">
      <OllieMark variant="spark" size={13} state="thinking" />
      <span className="ollie-shimmer">{t("thinking", { name })}</span>
    </div>
  );
}

function AssistantEntry({ e, live }: { e: Entry; live: boolean }) {
  const refs = refsOf(e);
  const lastText = e.blocks.map((b) => b.kind).lastIndexOf("text");
  const tail = e.blocks[e.blocks.length - 1];
  return (
    <div className="flex flex-col gap-2" data-entry="assistant">
      {e.blocks.map((b, i) =>
        b.kind === "text" ? (
          <Markdown key={i} text={b.text} refs={refs} caret={live && i === lastText && tail?.kind === "text"} />
        ) : (
          <StepRow key={b.step.id} step={b.step} />
        ),
      )}
      {live && (e.blocks.length === 0 || tail?.kind === "step") && !e.blocks.some((b) => b.kind === "step" && b.step.status === "awaiting_approval") && <Thinking />}
      {e.error && <p className="rounded-lg border border-line bg-sunken px-3 py-2 text-[12.5px] text-danger-fg">{e.error}</p>}
    </div>
  );
}

function Empty({ onPick }: { onPick: (p: string) => void }) {
  const { t } = useTranslation("assistant");
  const name = useAssistantName();
  return (
    <div className="flex flex-1 flex-col justify-end gap-4 px-4 pb-4">
      <div className="flex flex-col items-start gap-3">
        <OllieMark size={44} listen />
        <div>
          <p className="text-[15px] font-semibold tracking-[-0.01em] text-fg">{t("hello", { name })}</p>
          <p className="mt-1 text-[12.5px] leading-[19px] text-fg-3">{t("helloSub")}</p>
        </div>
      </div>
      <Suggestions onPick={onPick} />
    </div>
  );
}

/** Pasted text this long folds to its first lines until it is opened. */
const FOLD_LINES = 12;
const FOLD_CHARS = 700;

/** A message the user sent. Long unbroken runs (HTML, links) wrap inside the bubble. */
function UserText({ text }: { text: string }) {
  const { t } = useTranslation("assistant");
  const long = text.length > FOLD_CHARS || text.split("\n").length > FOLD_LINES;
  const [open, setOpen] = useState(false);
  return (
    <>
      <div className={cn("whitespace-pre-wrap wrap-anywhere", long && !open && "line-clamp-12")}>{text}</div>
      {long && (
        <button
          type="button"
          aria-expanded={open}
          onClick={() => setOpen((o) => !o)}
          className="mt-1 text-[12px] font-medium text-fg-3 transition-colors hover:text-fg"
        >
          {open ? t("showLess") : t("showMore")}
        </button>
      )}
    </>
  );
}

function Conversation({ composer }: { composer: React.RefObject<ComposerHandle | null> }) {
  const { entries, running, approval, loading } = useOllie();
  const scroller = useRef<HTMLDivElement>(null);
  const stick = useRef(true);
  const reduce = useReducedMotion();

  // Follow the stream while the reader is at the bottom.
  useLayoutEffect(() => {
    const el = scroller.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
  });

  const send = (text: string) => {
    stick.current = true;
    void ollie.send(text);
  };

  if (loading) {
    return (
      <div className="flex flex-1 flex-col gap-3 p-4">
        {[60, 85, 40].map((w, i) => (
          <div key={i} className="h-4 animate-pulse-soft rounded bg-sunken" style={{ width: `${w}%` }} />
        ))}
      </div>
    );
  }
  if (entries.length === 0) return <Empty onPick={send} />;
  return (
    <div
      ref={scroller}
      onScroll={(e) => {
        const el = e.currentTarget;
        stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
      }}
      className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto overscroll-contain px-4 py-4"
      aria-live="polite"
    >
      {entries.map((e, i) =>
        e.role === "user" ? (
          <motion.div
            key={e.id}
            initial={reduce ? false : { opacity: 0, y: 6 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ duration: 0.22, ease: EASE }}
            className="ml-8 min-w-0 max-w-full shrink-0 self-end rounded-2xl rounded-br-md bg-sunken px-3 py-2 text-[13px] leading-5 text-fg"
            data-ph-mask
            data-entry="user"
          >
            <UserText text={e.text} />
          </motion.div>
        ) : (
          <AssistantEntry key={e.id} e={e} live={running && i === entries.length - 1} />
        ),
      )}
      {approval && (
        <ApprovalCard
          approval={approval}
          busy={running}
          onDecide={(d) => {
            stick.current = true;
            void ollie.decide(d).then(() => composer.current?.focus());
          }}
        />
      )}
    </div>
  );
}

/** Title, body and composer; shared by every presentation. */
export function PanelBody({ onDragStart }: { onDragStart?: (e: RPointerEvent) => void }) {
  const showSessions = useOllie((s) => s.showSessions);
  const running = useOllie((s) => s.running);
  const entries = useOllie((s) => s.entries.length);
  const queued = useOllie((s) => s.queued);
  const composer = useRef<ComposerHandle>(null);

  useEffect(() => {
    composer.current?.focus();
  }, [showSessions]);
  useEffect(() => {
    const q = queued && ollie.takeQueued();
    if (q) void ollie.send(q);
  }, [queued]);

  return (
    <div
      className="flex h-full min-h-0 flex-col"
      onKeyDown={(e) => {
        if (e.key === "Escape" && !e.defaultPrevented) {
          e.preventDefault();
          if (useOllie.getState().showSessions) ollie.sessions(false);
          else ollie.open(false);
        }
        if (e.key === "Enter" && (e.metaKey || e.ctrlKey) && useOllie.getState().approval) {
          e.preventDefault();
          void ollie.decide("approve");
        }
      }}
    >
      <Header onDragStart={onDragStart} />
      {showSessions ? <SessionList /> : <Conversation composer={composer} />}
      {!showSessions && (
        <div className="shrink-0 px-3 pb-3 pt-1">
          {entries > 0 && !running && (
            <div className="mb-2">
              <Suggestions compact onPick={(p) => void ollie.send(p)} />
            </div>
          )}
          <Composer ref={composer} running={running} onSend={(t) => void ollie.send(t)} onStop={() => ollie.stop()} />
        </div>
      )}
    </div>
  );
}

function useMedia(query: string) {
  const [hit, setHit] = useState(() => typeof matchMedia === "undefined" || matchMedia(query).matches);
  useEffect(() => {
    const m = matchMedia(query);
    const on = () => setHit(m.matches);
    m.addEventListener("change", on);
    return () => m.removeEventListener("change", on);
  }, [query]);
  return hit;
}
const useIsDesktop = () => useMedia("(min-width: 768px)");
/** Wide enough to give the panel its own column without squeezing the ticket. */
const useRoomToDock = () => useMedia("(min-width: 1600px)");

/** The docked column: mount it as the last child of the app's main row. */
export function AssistantDock() {
  const { t } = useTranslation("assistant");
  const name = useAssistantName();
  const open = useOllie((s) => s.open);
  const mode = useOllie((s) => s.mode);
  const room = useRoomToDock();
  const reduce = useReducedMotion();
  const show = open && mode === "docked" && room;
  return (
    <AnimatePresence initial={false}>
      {show && (
        <motion.aside
          key="ollie-dock"
          aria-label={t("open", { name })}
          initial={{ width: 0, opacity: reduce ? 1 : 0 }}
          animate={{ width: PANEL_WIDTH, opacity: 1 }}
          exit={{ width: 0, opacity: reduce ? 1 : 0 }}
          transition={reduce ? { duration: 0 } : { type: "spring", stiffness: 380, damping: 38 }}
          className="relative hidden shrink-0 overflow-hidden md:block"
          data-ollie-panel="docked"
        >
          <div className="absolute inset-y-0 left-0 flex flex-col border-l border-t border-line bg-surface md:ml-2 md:rounded-tl-2xl desktop:overflow-hidden desktop:rounded-2xl desktop:border desktop:border-panel-line" style={{ width: PANEL_WIDTH - 8 }}>
            <PanelBody />
          </div>
        </motion.aside>
      )}
    </AnimatePresence>
  );
}

/** The floating card, and the sheet on phones. */
export function AssistantFloat() {
  const { t } = useTranslation("assistant");
  const name = useAssistantName();
  const open = useOllie((s) => s.open);
  const mode = useOllie((s) => s.mode);
  const offset = useOllie((s) => s.offset);
  const desk = useIsDesktop();
  const room = useRoomToDock();
  const reduce = useReducedMotion();
  // Docked without room for a column: a sheet over the right edge instead.
  const sheet = desk && mode === "docked" && !room;
  const show = open && (mode === "floating" || !room);
  const drag = useRef<{ x: number; y: number; right: number; bottom: number } | null>(null);
  const [live, setLive] = useState<{ right: number; bottom: number } | null>(null);

  const clamp = (right: number, bottom: number) => ({
    right: Math.max(8, Math.min(right, window.innerWidth - PANEL_WIDTH - 8)),
    bottom: Math.max(8, Math.min(bottom, window.innerHeight - 160)),
  });

  const onDragStart = (e: RPointerEvent) => {
    if (!desk || e.button !== 0) return;
    drag.current = { x: e.clientX, y: e.clientY, right: offset.right, bottom: offset.bottom };
    const move = (ev: PointerEvent) => {
      const d = drag.current;
      if (!d) return;
      setLive(clamp(d.right - (ev.clientX - d.x), d.bottom - (ev.clientY - d.y)));
    };
    const up = (ev: PointerEvent) => {
      const d = drag.current;
      drag.current = null;
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      setLive(null);
      if (d) ollie.setOffset(clamp(d.right - (ev.clientX - d.x), d.bottom - (ev.clientY - d.y)));
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
  };

  const pos = live ?? offset;
  if (sheet) {
    return (
      <AnimatePresence>
        {show && (
          <motion.div
            key="ollie-sheet"
            role="dialog"
            aria-label={t("open", { name })}
            initial={reduce ? { opacity: 0 } : { x: PANEL_WIDTH + 24 }}
            animate={reduce ? { opacity: 1 } : { x: 0 }}
            exit={reduce ? { opacity: 0 } : { x: PANEL_WIDTH + 24 }}
            transition={reduce ? { duration: 0.12 } : { type: "spring", stiffness: 380, damping: 40 }}
            style={{ width: PANEL_WIDTH }}
            className="fixed bottom-0 right-0 top-12 z-40 flex flex-col overflow-hidden rounded-tl-2xl border-l border-t border-line bg-surface shadow-float desktop:bottom-2 desktop:right-2 desktop:rounded-2xl desktop:border desktop:border-panel-line"
            data-ollie-panel="docked"
          >
            <PanelBody />
          </motion.div>
        )}
      </AnimatePresence>
    );
  }
  return (
    <AnimatePresence>
      {show && (
        <motion.div
          key="ollie-float"
          role="dialog"
          aria-label={t("open", { name })}
          initial={reduce ? { opacity: 0 } : { opacity: 0, y: 14, scale: 0.97 }}
          animate={{ opacity: 1, y: 0, scale: 1 }}
          exit={reduce ? { opacity: 0 } : { opacity: 0, y: 10, scale: 0.98 }}
          transition={reduce ? { duration: 0.12 } : { type: "spring", stiffness: 420, damping: 34 }}
          style={desk ? { right: pos.right, bottom: pos.bottom, width: PANEL_WIDTH, height: "min(640px, calc(100vh - 96px))", transformOrigin: "bottom right" } : undefined}
          className={cn(
            "fixed z-50 flex flex-col overflow-hidden border border-line bg-surface shadow-float",
            desk ? "rounded-2xl" : "inset-x-0 bottom-0 top-12 rounded-t-2xl",
          )}
          data-ollie-panel="floating"
        >
          <PanelBody onDragStart={desk ? onDragStart : undefined} />
        </motion.div>
      )}
    </AnimatePresence>
  );
}
