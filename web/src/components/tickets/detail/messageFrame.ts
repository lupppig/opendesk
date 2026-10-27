import type { Message } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { tone } from "@/lib/tones";

/**
 * Who a timeline message is from, as its frame tells it at a glance: the customer, the
 * team (agents, the AI agent, automations, the API), an internal note the customer never
 * sees, or a side conversation with someone outside the team.
 */
export type MessageAuthor = "customer" | "team" | "note" | "thread";

export function messageAuthor(m: Pick<Message, "kind" | "direction" | "author" | "thread_id">): MessageAuthor {
  // A thread message is stored as a note, but it is email to or from a third party.
  if (m.thread_id) return "thread";
  if (m.kind === "note") return "note";
  if (m.direction === "outbound" || m.author.type === "user") return "team";
  return "customer";
}

/** The border and surface of a message: a neutral card for the customer, the reply tint for the team, the note tint for notes. */
export function messageFrame(author: MessageAuthor, failed: boolean): string {
  if (author === "note") return "border-dashed border-note-line bg-note";
  const surface = author === "team" ? "bg-reply" : "bg-surface";
  if (failed) return cn(tone.red.line, surface);
  if (author === "thread") return cn(tone.cyan.line, surface);
  return author === "team" ? "border-reply-line bg-reply" : "border-line bg-surface";
}

/** The side of the timeline a message sits on in the conversation layout. */
export type MessageSide = "customer" | "team";

/**
 * Whose side a message is on, by one rule: is it the customer's or ours. Ours is everything
 * the team sends or keeps: members' replies and notes, and the replies and notes of Ollie and
 * bots, automations and the API (outbound), auto-replies and CSAT emails included. Anything
 * that came in from outside is the customer's side: the customer's messages, mail a system
 * sends us such as a bounce, and a third party's answers in a side conversation.
 */
export function messageSide(m: Pick<Message, "kind" | "direction" | "author" | "thread_id">): MessageSide {
  const author = messageAuthor(m);
  if (author === "thread") return m.direction === "outbound" || m.author.type === "user" ? "team" : "customer";
  return author === "customer" ? "customer" : "team";
}

/**
 * How a message's card sits in the timeline. Off, it takes the full width as it always has.
 * On, each card takes --conversation-card of the width (styles/app.css), the customer's at
 * the start and the team's at the end (logical, so a right-to-left language mirrors it);
 * below 28rem of timeline (a phone, a narrow pane) it is full width again. Only the margin
 * moves: the reading order stays.
 */
export function sideClass(side: MessageSide, on: boolean): string | undefined {
  if (!on) return undefined;
  return side === "team" ? "@md:ms-auto @md:w-(--conversation-card)" : "@md:me-auto @md:w-(--conversation-card)";
}
