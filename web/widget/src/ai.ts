// The AI agent's replies (contracts/agent.md): posted as `author.type =
// "system"` with `ai: true`. The cited sources are links at the end of the
// body. While the agent handles a conversation the visitor can ask for a
// person; after a handoff or a team member's reply it is the team's.

import type { ActionUi } from "./store";
import { readBotRef } from "./config";
import type { AIAction, AIActionStatus, BotRef, Conversation, Message } from "./types";

export function isAiAnswer(m: Pick<Message, "author" | "ai">): boolean {
  return m.author?.type === "system" && m.ai === true;
}

/**
 * The bot a team reply was written as by an API key that speaks as one (contracts/api.md
 * "API keys"): an AI of the team's own, shown with the bot's name and face. Null otherwise.
 */
export function keyBot(m: Pick<Message, "author" | "ai">): BotRef | null {
  if (m.author?.type !== "api" || m.ai) return null;
  const seen = readBotRef(m.author.bot);
  return seen?.look ? { id: seen.id, name: seen.name, avatar_url: seen.avatar_url, look: seen.look } : null;
}

/** A person from the team has replied in the thread. */
export function teamReplied(msgs: readonly Pick<Message, "author" | "direction">[]): boolean {
  return msgs.some((m) => m.direction === "outbound" && m.author?.type === "user");
}

export function handedOff(c: Pick<Conversation, "ai_handoff"> | undefined): boolean {
  return c?.ai_handoff === true;
}

/**
 * Whether the agent is handling the conversation: it is on for new
 * conversations (or has replied here), the conversation is still open, no one
 * from the team has replied and nobody handed it off.
 */
export function aiHandling(o: {
  aiAnswers: boolean;
  conversation?: Pick<Conversation, "ai_handoff" | "status">;
  handedOff?: boolean;
  msgs: readonly Pick<Message, "author" | "ai" | "direction">[];
}): boolean {
  if (o.handedOff || handedOff(o.conversation)) return false;
  if (o.conversation && (o.conversation.status === "solved" || o.conversation.status === "closed")) return false;
  if (teamReplied(o.msgs)) return false;
  return o.aiAnswers || o.msgs.some(isAiAnswer);
}

/** The latest agent reply in the thread (where "Talk to a person" is offered). */
export function latestAiAnswer<T extends Pick<Message, "author" | "ai">>(msgs: readonly T[]): T | null {
  for (let i = msgs.length - 1; i >= 0; i--) if (isAiAnswer(msgs[i])) return msgs[i];
  return null;
}

/** What an action card shows: the server's status once it moved on, else the visitor's decision in flight. */
export function actionStatus(a: Pick<AIAction, "status">, ui: ActionUi | undefined): AIActionStatus {
  if (a.status !== "pending") return a.status;
  return ui?.status ?? "pending";
}
