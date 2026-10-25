// The assistant API (contracts/ai.md, Assistant): conversations, streamed runs,
// approvals and tips.

import { http, streamEvents } from "@/lib/api/client";

export type AssistantSession = { id: string; title: string; created_at: string; updated_at: string };

export type PageContext = {
  path?: string;
  page?: string;
  ticket_id?: string;
  view_id?: string;
  folder?: string;
  filters?: Record<string, string>;
};

/** `external`: something in a connected product; `id` is its https link. */
export type RefKind = "ticket" | "contact" | "article" | "view" | "automation" | "member" | "external";
export type AssistantRef = { kind: RefKind; id: string; label: string; number?: number };

export type StepStatus = "running" | "done" | "error" | "awaiting_approval" | "approved" | "denied" | "expired";
export type AssistantStep = {
  id: string;
  tool: string;
  label: string;
  status: StepStatus;
  summary?: string;
  refs?: AssistantRef[];
  action_id?: string;
  /** ask_teammate: whose Ollie answered, and how (contracts/ai.md "Asking a teammate"). */
  peer?: StepPeer;
  /** A change that ran without asking, because the workspace allows it (contracts/ai.md "Approvals"). */
  auto?: boolean;
  /** What a change that ran without asking ran. */
  preview?: Preview;
};
export type PeerResult = "answered" | "declined" | "not_shared" | "failed";
export type StepPeer = { user: { id: string; name: string; avatar_url?: string | null }; result: PeerResult; sources: number };

export type Change = { field: string; from: string; to: string };
export type Preview = {
  title: string;
  summary?: string;
  changes?: Change[];
  body?: string;
  targets?: AssistantRef[];
  total?: number;
  external?: boolean;
  /** The connected product the change runs in ("Linear"). */
  system?: string;
  /** Everything the change runs with, as indented JSON; changes and body may summarize it. */
  arguments?: string;
  /** Fields changes and body leave out. */
  omitted?: number;
  /** A value shown in changes or body is cut short. */
  clipped?: boolean;
};
export type Approval = { action_id: string; call_id: string; tool: string; preview: Preview; token: string; expires_at: string };

export type TranscriptItem = { role: "user" | "assistant"; text: string; steps: AssistantStep[]; context?: PageContext };
export type Transcript = { session: AssistantSession; items: TranscriptItem[]; approval: Approval | null };

export type Tip = { key: string; text: string; prompt: string; severity: "high" | "medium" | "low"; count: number };

export type RunEvent =
  | { type: "session"; session: AssistantSession }
  | { type: "text"; text: string }
  | { type: "tool"; step: AssistantStep }
  | { type: "approval"; approval: Approval }
  | { type: "done"; status: "complete" | "awaiting_approval" };

function relay(onEvent: (e: RunEvent) => void) {
  return (event: string, data: unknown) => {
    switch (event) {
      case "session":
        onEvent({ type: "session", session: data as AssistantSession });
        break;
      case "text":
        onEvent({ type: "text", text: (data as { text: string }).text });
        break;
      case "tool":
        onEvent({ type: "tool", step: data as AssistantStep });
        break;
      case "approval":
        onEvent({ type: "approval", approval: data as Approval });
        break;
      case "done":
        onEvent({ type: "done", status: (data as { status: "complete" | "awaiting_approval" }).status });
        break;
    }
  };
}

export const assistantApi = {
  sessions: () => http.get<{ data: AssistantSession[] }>("/v1/assistant/sessions", { query: { limit: 50 } }),
  create: (title?: string) => http.post<AssistantSession>("/v1/assistant/sessions", title ? { title } : {}),
  get: (id: string) => http.get<Transcript>(`/v1/assistant/sessions/${id}`),
  rename: (id: string, title: string) => http.patch<AssistantSession>(`/v1/assistant/sessions/${id}`, { title }),
  remove: (id: string) => http.del(`/v1/assistant/sessions/${id}`),
  send: (id: string, text: string, context: PageContext | undefined, onEvent: (e: RunEvent) => void, signal?: AbortSignal) =>
    streamEvents(`/v1/assistant/sessions/${id}/messages`, { text, context }, relay(onEvent), signal),
  decide: (actionId: string, decision: "approve" | "deny", token: string, onEvent: (e: RunEvent) => void, signal?: AbortSignal) =>
    streamEvents(`/v1/assistant/actions/${actionId}/decision`, { decision, token }, relay(onEvent), signal),
  tips: () => http.get<{ tips: Tip[] }>("/v1/assistant/tips"),
};
