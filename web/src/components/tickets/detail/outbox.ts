import { create } from "zustand";
import { errorMessage, getActiveAccount, getWorkspaceId, inWorkspace, jsonBytes, KEEPALIVE_MAX } from "@/lib/api/client";
import { api, type SendMessageBody } from "@/lib/api/endpoints";
import type { Attachment, Channel, Message, Ticket, TicketDraft, WorkspaceSettings } from "@/lib/api/types";
import { qk } from "@/lib/queries";
import { queryClient as qc } from "@/lib/queryClient";
import { beforeSignOut, useSession } from "@/lib/session";
import { patchTicketEverywhere } from "@/lib/ticketCache";
import { i18n } from "@/i18n";
import { toast } from "@/components/ui/toast";
import { statusLabel } from "../bits";
import { applyPatch } from "../useTicketActions";
import type { PersonalFrom } from "./personalFrom";

// A reply waits out the workspace's undo window here, in the app and not on the server:
// nothing of it reaches the customer, an automation or a channel until it is sent. It lives
// outside the composer, so leaving the ticket keeps the countdown and the reply goes out, or
// comes back, wherever the agent is. It belongs to the account and workspace it was written
// in: switching either, or signing out, sends it at once as that account, in that workspace.

/** Seconds an email (or any other non-chat) reply waits by default (settings.undo_send_seconds). */
export const UNDO_SEND_DEFAULT = 5;
/** Seconds a chat reply waits by default (settings.undo_send_chat_seconds): none, chat is live. */
export const UNDO_SEND_CHAT_DEFAULT = 0;
export const UNDO_SEND_CHOICES = [0, 5, 10, 20, 30] as const;

/** How long a reply on a ticket of this channel waits before it is sent. */
export function undoSeconds(settings: WorkspaceSettings | undefined, channel: Channel | undefined): number {
  return channel === "chat"
    ? (settings?.undo_send_chat_seconds ?? UNDO_SEND_CHAT_DEFAULT)
    : (settings?.undo_send_seconds ?? UNDO_SEND_DEFAULT);
}

export type Upload = { key: string; file: File; status: "uploading" | "done" | "error"; attachment?: Attachment; preview?: string };

/** A reply taken back: what goes into its ticket's composer again. */
export type Unsent = {
  html: string;
  cc: string[];
  bcc: string[];
  mailboxId?: string;
  /** Sent from one of my own Collab addresses (Draft.fromAccount). */
  fromAccount?: PersonalFrom | null;
  uploads: Upload[];
  /** The note this reply was made from (body.from_note_id): it stays as a marker pointing at the reply. */
  fromNote?: string;
};

/** The account and workspace a message was written in; it is sent there whatever is selected by then. */
type Origin = { account: string | null; workspace: string | null };
const currentOrigin = (): Origin => ({ account: getActiveAccount(), workspace: getWorkspaceId() });

type Held = {
  temp: Message;
  body: SendMessageBody;
  unsent: Unsent;
  me: string;
  origin: Origin;
  /** The body's size: every held reply together must fit the browser's keepalive allowance. */
  bytes: number;
  timer: ReturnType<typeof setTimeout>;
  toast: string | number;
};

const held = new Map<string, Held>();

/** When each held reply goes out, by its temporary message id. */
export const useHeld = create<Record<string, number>>(() => ({}));
/** Replies taken back, by ticket, until that ticket's composer takes them in. */
export const useUnsent = create<Record<string, Unsent>>(() => ({}));

function revoke(uploads: Upload[]) {
  uploads.forEach((u) => u.preview && URL.revokeObjectURL(u.preview));
}

type DeliverOpts = {
  /** Let the request outlive the page. */
  keepalive?: boolean;
  /** The account and workspace to send as; the current ones by default. */
  origin?: Origin;
  /** Say so in a toast when it fails: sent while leaving, where the timeline no longer shows it. */
  warn?: boolean;
};

/** Sends a reply or note now. The optimistic copy (temp) shows until the server's takes its place. */
export async function deliver(temp: Message, body: SendMessageBody, me: string, opts: DeliverOpts = {}): Promise<boolean> {
  const tid = temp.ticket_id;
  const origin = opts.origin ?? currentOrigin();
  // The cache it touches is its workspace's, even when another is in focus by the time it goes.
  const here = <T>(fn: () => T) => inWorkspace(origin.workspace, fn);
  const { prevTicket, prevDraft } = here(() => {
    qc.setQueryData<Message[]>(qk.messages(tid), (old) => old && [...old.filter((x) => x.id !== temp.id), temp]);
    const prevTicket = qc.getQueryData<Ticket>(qk.ticket(tid));
    if ((body.status || body.mailbox_id) && prevTicket)
      patchTicketEverywhere(qc, applyPatch(qc, prevTicket, { status: body.status, mailbox_id: body.mailbox_id }), me);
    // A reply going out clears the ticket's shared draft (contracts/api.md "Drafts").
    const prevDraft = body.kind === "reply" ? qc.getQueryData<TicketDraft | null>(qk.ticketDraft(tid)) : undefined;
    if (prevDraft) qc.setQueryData(qk.ticketDraft(tid), null);
    return { prevTicket, prevDraft };
  });
  try {
    const m = await api.tickets.send(tid, body, {
      keepalive: !!opts.keepalive,
      as: origin.account ?? undefined,
      workspace: origin.workspace ?? undefined,
    });
    here(() => {
      qc.setQueryData<Message[]>(qk.messages(tid), (old) => {
        if (!old) return old;
        // The note it was written from stays as a marker pointing at it; only admins read what it said.
        if (body.from_note_id)
          old = old.map((x) => (x.id === body.from_note_id ? { ...x, sent_as_reply_id: m.id, deleted_at: x.deleted_at ?? m.created_at } : x));
        // The socket may have delivered this message (and its delivery status) before
        // the response arrived; that copy is newer than the response.
        const live = old.find((x) => x.id === m.id);
        const rest = old.filter((x) => x.id !== temp.id && x.id !== m.id);
        return [...rest, live ?? m].sort((a, b) => Date.parse(a.created_at) - Date.parse(b.created_at));
      });
      if (body.from_note_id) void qc.invalidateQueries({ queryKey: qk.activities(tid) });
    });
    return true;
  } catch (e) {
    here(() => {
      qc.setQueryData<Message[]>(qk.messages(tid), (old) =>
        old?.map((x) => (x.id === temp.id ? { ...x, delivery_status: "failed", delivery_error: errorMessage(e, "Not sent") } : x)),
      );
      if (prevTicket) patchTicketEverywhere(qc, prevTicket, me);
      if (prevDraft) qc.setQueryData(qk.ticketDraft(tid), prevDraft);
    });
    if (opts.warn) toast.error(e, i18n.getFixedT(null, "inbox")("outbox.notSent"));
    return false;
  } finally {
    void here(() => qc.invalidateQueries({ queryKey: qk.counts }));
  }
}

/** Holds a reply for `seconds` before sending it, with a way to take it back or keep it as a note. */
export function hold(temp: Message, body: SendMessageBody, unsent: Unsent, me: string, seconds: number) {
  const t = i18n.getFixedT(null, "inbox");
  const bytes = jsonBytes(body);
  // Closing the tab sends what is held, and the browser carries only so much out of a closing
  // page: a reply that would not fit goes now rather than be lost.
  const tooLong = seconds > 0 && bytes + [...held.values()].reduce((n, h) => n + h.bytes, 0) > KEEPALIVE_MAX;
  if (seconds <= 0 || tooLong) {
    revoke(unsent.uploads);
    if (tooLong) toast.info(t("outbox.tooLongToHold"));
    void deliver(temp, body, me);
    return;
  }
  const ms = seconds * 1000;
  const label = body.status
    ? t("outbox.sendingAndMark", { status: statusLabel(body.status).toLowerCase() })
    : t("outbox.sending");
  const id = toast.held(label, ms, { label: t("outbox.undo"), onClick: () => undo(temp.id) }, { label: t("outbox.asNote"), onClick: () => asNote(temp.id) });
  held.set(temp.id, { temp, body, unsent, me, origin: currentOrigin(), bytes, timer: setTimeout(() => void release(temp.id), ms), toast: id });
  useHeld.setState({ [temp.id]: Date.now() + ms });
  qc.setQueryData<Message[]>(qk.messages(temp.ticket_id), (old) => old && [...old, { ...temp, delivery_status: "scheduled" }]);
}

function take(tempId: string): Held | undefined {
  const h = held.get(tempId);
  if (!h) return undefined;
  held.delete(tempId);
  clearTimeout(h.timer);
  toast.dismiss(h.toast);
  useHeld.setState((s) => Object.fromEntries(Object.entries(s).filter(([k]) => k !== tempId)), true);
  return h;
}

/** Sends a held reply now, as the account and in the workspace it was written in. */
export function release(tempId: string, opts: { keepalive?: boolean; warn?: boolean } = {}): Promise<boolean> {
  const h = take(tempId);
  if (!h) return Promise.resolve(false);
  revoke(h.unsent.uploads);
  return deliver(h.temp, h.body, h.me, { ...opts, origin: h.origin });
}

/** Sends every held reply now (signing out, switching away, closing the tab), each where it was written. */
export function flushHeld(opts: { keepalive?: boolean; warn?: boolean } = {}): Promise<boolean[]> {
  return Promise.all([...held.keys()].map((id) => release(id, opts)));
}

/** Takes a held reply back into its ticket's composer. */
export function undo(tempId: string) {
  const h = take(tempId);
  if (!h) return;
  qc.setQueryData<Message[]>(qk.messages(h.temp.ticket_id), (old) => old?.filter((x) => x.id !== tempId));
  useUnsent.setState({ [h.temp.ticket_id]: h.unsent });
}

/**
 * A reply written from a note stays that note instead: the note takes the words as they read
 * now (edited, when they changed), and files added since become a note of their own. Nothing
 * is posted twice.
 */
export async function backToNote(temp: Message, noteId: string, html: string, attachmentIds: string[] | undefined, me: string) {
  const tid = temp.ticket_id;
  const note = qc.getQueryData<Message[]>(qk.messages(tid))?.find((x) => x.id === noteId);
  if (!note || note.body_html !== html) {
    try {
      const edited = await api.tickets.editMessage(tid, noteId, { body_html: html });
      qc.setQueryData<Message[]>(qk.messages(tid), (old) => old?.map((x) => (x.id === edited.id ? edited : x)));
    } catch (e) {
      toast.error(e, i18n.getFixedT(null, "inbox")("outbox.noteNotSaved"));
    }
  }
  if (attachmentIds?.length) {
    const files: Message = { ...temp, id: `${temp.id}-files`, kind: "note", body_html: "", body_text: "", delivery_status: "none", to: [], cc: [], bcc: [], from_email: "", mailbox_id: null };
    await deliver(files, { kind: "note", body_html: "", attachment_ids: attachmentIds }, me);
  }
}

/** Adds a held reply as an internal note instead: the customer never gets it. The status it
 * was to set went with answering the customer, so it stays as it is. A reply made from a
 * note goes back into that note. */
export function asNote(tempId: string) {
  const h = take(tempId);
  if (!h) return;
  revoke(h.unsent.uploads);
  if (h.unsent.fromNote) {
    qc.setQueryData<Message[]>(qk.messages(h.temp.ticket_id), (old) => old?.filter((x) => x.id !== tempId));
    void backToNote(h.temp, h.unsent.fromNote, h.body.body_html, h.body.attachment_ids, h.me);
    return;
  }
  const note: Message = { ...h.temp, kind: "note", delivery_status: "none", to: [], cc: [], bcc: [], from_email: "", mailbox_id: null };
  void deliver(note, { kind: "note", body_html: h.body.body_html, attachment_ids: h.body.attachment_ids }, h.me, { origin: h.origin });
}

if (typeof window !== "undefined") {
  // Leaving with a reply on hold asks first; leaving anyway sends it, as the agent meant.
  window.addEventListener("beforeunload", (e) => {
    if (held.size) e.preventDefault();
  });
  window.addEventListener("pagehide", () => {
    void flushHeld({ keepalive: true });
  });
  // Another workspace or account: what was written in the last one goes out there, now.
  useSession.subscribe(() => {
    const now = currentOrigin();
    for (const [id, h] of [...held]) {
      if (h.origin.account !== now.account || h.origin.workspace !== now.workspace) void release(id, { warn: true });
    }
  });
  // Signing out waits for held replies to go, while the account can still send them.
  beforeSignOut(() => flushHeld({ warn: true }));
}
