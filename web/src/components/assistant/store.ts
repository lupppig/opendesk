// Ollie's panel state and runs. One conversation is live at a time; its blocks
// (text and tool steps, in the order they happened) render the transcript, and
// runs stream into the last assistant entry.

import { create } from "zustand";
import { queryClient } from "@/lib/queryClient";
import { errorMessage } from "@/lib/api/client";
import { assistantApi, type Approval, type AssistantStep, type PageContext, type RunEvent, type Transcript } from "./api";
import { pageContext } from "./context";
import { qk } from "@/lib/queries";

export type Block = { kind: "text"; text: string } | { kind: "step"; step: AssistantStep };
export type Entry = { id: string; role: "user" | "assistant"; text: string; blocks: Block[]; error?: string };

type Mode = "docked" | "floating";

type State = {
  open: boolean;
  mode: Mode;
  /** Floating panel position, from the viewport's bottom right. */
  offset: { right: number; bottom: number };
  showSessions: boolean;
  sessionId: string | null;
  title: string;
  entries: Entry[];
  loading: boolean;
  running: boolean;
  /** The change waiting for a decision. */
  approval: Approval | null;
  /** Set when a run ended well, for the blob's cheer. */
  lastOk: boolean;
  /** A prompt to send once the panel is ready (askOllie). */
  queued: string | null;
};

const PREFS = "od-ollie-prefs";

function readPrefs(): Partial<Pick<State, "mode" | "offset" | "sessionId">> {
  try {
    const raw = localStorage.getItem(PREFS);
    return raw ? (JSON.parse(raw) as Partial<State>) : {};
  } catch {
    return {};
  }
}

function writePrefs(s: State) {
  try {
    localStorage.setItem(PREFS, JSON.stringify({ mode: s.mode, offset: s.offset, sessionId: s.sessionId }));
  } catch {
    /* storage unavailable */
  }
}

const prefs = readPrefs();

export const useOllie = create<State>(() => ({
  open: false,
  mode: prefs.mode === "floating" ? "floating" : "docked",
  offset: prefs.offset ?? { right: 24, bottom: 24 },
  showSessions: false,
  sessionId: prefs.sessionId ?? null,
  title: "",
  entries: [],
  loading: false,
  running: false,
  approval: null,
  lastOk: false,
  queued: null,
}));

useOllie.subscribe((s, prev) => {
  if (s.mode !== prev.mode || s.offset !== prev.offset || s.sessionId !== prev.sessionId) writePrefs(s);
});

let seq = 0;
const uid = () => `e${Date.now().toString(36)}${(seq++).toString(36)}`;
let controller: AbortController | null = null;

/** The assistant's conversations in the workspace in scope (built when used, never kept). */
export const sessionsKey = () => qk.of("assistant", "sessions");

/** Transcript items to entries: consecutive assistant items merge into one. */
export function toEntries(tr: Transcript): Entry[] {
  const out: Entry[] = [];
  for (const it of tr.items) {
    if (it.role === "user") {
      out.push({ id: uid(), role: "user", text: it.text, blocks: [] });
      continue;
    }
    let last = out[out.length - 1];
    if (!last || last.role !== "assistant") {
      last = { id: uid(), role: "assistant", text: "", blocks: [] };
      out.push(last);
    }
    if (it.text) last.blocks.push({ kind: "text", text: it.text });
    for (const st of it.steps) last.blocks.push({ kind: "step", step: st });
  }
  return out;
}

function patchLast(fn: (e: Entry) => Entry) {
  useOllie.setState((s) => {
    const entries = s.entries.slice();
    const i = entries.length - 1;
    if (i < 0 || entries[i].role !== "assistant") return s;
    entries[i] = fn(entries[i]);
    return { entries };
  });
}

function apply(e: RunEvent) {
  switch (e.type) {
    case "session":
      useOllie.setState({ title: e.session.title });
      void queryClient.invalidateQueries({ queryKey: sessionsKey() });
      break;
    case "text":
      patchLast((en) => {
        const blocks = en.blocks.slice();
        const last = blocks[blocks.length - 1];
        if (last && last.kind === "text") blocks[blocks.length - 1] = { kind: "text", text: last.text + e.text };
        else blocks.push({ kind: "text", text: e.text });
        return { ...en, blocks };
      });
      break;
    case "tool":
      patchLast((en) => {
        const blocks = en.blocks.slice();
        const i = blocks.findIndex((b) => b.kind === "step" && b.step.id === e.step.id);
        if (i >= 0) blocks[i] = { kind: "step", step: { ...(blocks[i] as { step: AssistantStep }).step, ...e.step } };
        else blocks.push({ kind: "step", step: e.step });
        return { ...en, blocks };
      });
      break;
    case "approval":
      useOllie.setState({ approval: e.approval });
      break;
    case "done":
      useOllie.setState({ lastOk: e.status === "complete" });
      break;
  }
}

async function run(go: (signal: AbortSignal) => Promise<void>) {
  controller?.abort();
  const c = new AbortController();
  controller = c;
  useOllie.setState({ running: true, lastOk: false });
  try {
    await go(c.signal);
  } catch (err) {
    if ((err as Error)?.name !== "AbortError") patchLast((en) => ({ ...en, error: errorMessage(err, "Ollie could not answer. Try again.") }));
  } finally {
    if (controller === c) controller = null;
    useOllie.setState({ running: false });
    // Tool writes went through the API: refresh what they may have changed.
    void queryClient.invalidateQueries({ queryKey: sessionsKey() });
  }
}

async function ensureSession(): Promise<string> {
  const s = useOllie.getState();
  if (s.sessionId) return s.sessionId;
  const created = await assistantApi.create();
  useOllie.setState({ sessionId: created.id, title: created.title });
  return created.id;
}

/** Reopening after a reload picks the last conversation back up. */
function restore() {
  const s = useOllie.getState();
  if (s.sessionId && !s.entries.length && !s.loading && !s.running && !s.queued) void ollie.load(s.sessionId);
}

export const ollie = {
  open: (open = true) => {
    useOllie.setState({ open });
    if (open) restore();
  },
  toggle: () => ollie.open(!useOllie.getState().open),
  setMode: (mode: Mode) => useOllie.setState({ mode }),
  setOffset: (offset: State["offset"]) => useOllie.setState({ offset }),
  sessions: (showSessions = !useOllie.getState().showSessions) => useOllie.setState({ showSessions }),

  /** A fresh, empty conversation (created on the first message). */
  newChat: () => {
    controller?.abort();
    useOllie.setState({ sessionId: null, title: "", entries: [], approval: null, running: false, showSessions: false });
  },

  load: async (id: string) => {
    controller?.abort();
    useOllie.setState({ sessionId: id, loading: true, entries: [], approval: null, showSessions: false, running: false });
    try {
      const tr = await assistantApi.get(id);
      if (useOllie.getState().sessionId !== id) return;
      useOllie.setState({ title: tr.session.title, entries: toEntries(tr), approval: tr.approval, loading: false });
    } catch {
      if (useOllie.getState().sessionId === id) useOllie.setState({ sessionId: null, title: "", entries: [], loading: false });
    }
  },

  send: async (text: string, context: PageContext | undefined = pageContext()) => {
    const t = text.trim();
    if (!t || useOllie.getState().running) return;
    useOllie.setState((s) => ({
      approval: null,
      entries: [...s.entries, { id: uid(), role: "user", text: t, blocks: [] }, { id: uid(), role: "assistant", text: "", blocks: [] }],
    }));
    await run(async (signal) => {
      const id = await ensureSession();
      await assistantApi.send(id, t, context, apply, signal);
    });
  },

  decide: async (decision: "approve" | "deny") => {
    const a = useOllie.getState().approval;
    if (!a || useOllie.getState().running) return;
    useOllie.setState((s) => {
      const entries = s.entries.slice();
      if (entries[entries.length - 1]?.role !== "assistant") entries.push({ id: uid(), role: "assistant", text: "", blocks: [] });
      return { approval: null, entries };
    });
    await run((signal) => assistantApi.decide(a.action_id, decision, a.token, apply, signal));
    // The approved change went through the API: show it everywhere at once.
    if (decision === "approve") void queryClient.invalidateQueries({ queryKey: qk.of("tickets") });
  },

  stop: () => controller?.abort(),

  /** Open on a fresh conversation and ask `prompt` there. */
  ask: (prompt: string) => {
    const s = useOllie.getState();
    if (s.sessionId || s.entries.length) ollie.newChat();
    useOllie.setState({ open: true, queued: prompt });
  },
  takeQueued: () => {
    const q = useOllie.getState().queued;
    if (q) useOllie.setState({ queued: null });
    return q;
  },
};
