import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowRightLeft, Check, ChevronDown, Eye, Loader2, Lock, MessagesSquare, Paperclip, TriangleAlert, X } from "lucide-react";
import { Morph } from "@/components/motion";
import { forwardRef, useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState } from "react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { useTranslation } from "react-i18next";
import { tone } from "@/lib/tones";
import { create } from "zustand";
import { DropdownMenu as M } from "radix-ui";
import type { Activity, Mailbox, Member, Message, SavedReply, Thread, Ticket, TicketStatus } from "@/lib/api/types";
import { api, type SendMessageBody } from "@/lib/api/endpoints";
import { q, qk, upsertInList } from "@/lib/queries";
import { useSession } from "@/lib/session";
import { realtime } from "@/lib/realtime/manager";
import { SideIncludeItems, SideIncludedChips, useIncludeSummary, useSideInclude, type SideInclude } from "./SideIncludes";
import { cn } from "@/lib/cn";
import { keyName } from "@/lib/hotkeys";
import { Button } from "@/components/ui/button";
import { Segmented } from "@/components/ui/tabs";
import { Picker } from "@/components/ui/picker";
import { Tooltip } from "@/components/ui/tooltip";
import { Avatar } from "@/components/ui/avatar";
import { Kbd } from "@/components/ui/kbd";
import { floatingPanel } from "@/components/ui/popover";
import { toast } from "@/components/ui/toast";
import { RichEditor, type RichEditorHandle } from "@/components/editor/RichEditor";
import { mentionExtension, savedReplyExtension, SuggestionPopup } from "./suggestions";
import { templateGroupLabel, useCatalog } from "@/components/templates/catalog";
import { matchVars, refreshTags, templateExtension, VarSuggestPopup, type VarItem } from "@/components/templates/extension";
import { variableText } from "./composerTemplates";
import { ReplySignature } from "./ReplySignature";
import { ReplyPreview } from "./ReplyPreview";
import { StatusIcon, statusLabel } from "../bits";
import { useTicketViewers } from "../presence";
import { useUpdateTicket } from "../useTicketActions";
import { ComposerAiMenu, ComposerAiStatus, ComposerSources, useComposerAi } from "@/components/ai/ticket/ComposerAi";
import { SuggestedReplyCard } from "./SuggestedReply";
import { SharedDraftBar } from "./SharedDraft";
import { backToNote, deliver, hold, undoSeconds as undoSecondsFor, useUnsent, type Upload } from "./outbox";
import { EmailChips, SavedReplyButton, UploadChip, uploadFiles } from "./composerParts";
import { addressLabel, fromLine, replyAddresses, TeamDot, useTeamsCtx, type TeamsCtx } from "../teams";
import { AccountDot } from "@/components/collab/bits";
import { identitySignature } from "@/lib/collab/identities";
import { parsePersonalValue, personalFromLine, personalValue, ReplyToLine, usePersonalSenders, type PersonalFrom, type PersonalSender } from "./personalFrom";

type Mode = "reply" | "note" | "thread";
/**
 * mailboxId: this reply goes out from another address (it becomes the ticket's reply address).
 * fromAccount: it goes out from one of my own Collab addresses instead (the reply address stays);
 * null: the ticket's address, chosen over the one I last answered this ticket from.
 * thread*: a third-party thread (contracts/api.md "Threads"): threadId writes in an existing one,
 * else threadTo, threadCc and threadSubject start a new one, with the customer's conversation
 * (threadHistory) and the files picked from it (threadFiles) if asked for.
 */
type Draft = {
  mode: Mode;
  html: string;
  cc: string[];
  bcc: string[];
  showCc: boolean;
  mailboxId?: string;
  fromAccount?: PersonalFrom | null;
  threadId?: string;
  threadTo: string[];
  threadCc: string[];
  threadSubject: string;
  threadHistory: boolean;
  threadFiles: string[];
  /** The note this reply is written from: it is removed once the reply is sent. */
  fromNote?: string;
};

// Drafts survive switching tickets (memory only).
const useDrafts = create<Record<string, Draft>>(() => ({}));
const emptyDraft: Draft = {
  mode: "reply",
  html: "",
  cc: [],
  bcc: [],
  showCc: false,
  threadTo: [],
  threadCc: [],
  threadSubject: "",
  threadHistory: false,
  threadFiles: [],
};

export type ComposerHandle = {
  focus: (mode?: Mode) => void;
  retry: (m: Message) => void;
  aiDraft: () => void;
  /** Writes in an existing third-party thread. */
  inThread: (thread: Thread) => void;
  /** Puts a note in the reply box, to go to the customer instead. */
  fromNote: (m: Message) => void;
};

/** The optimistic copy of a reply or note this agent writes, shown until the server's arrives. */
function useTempMessage(ticket: Ticket, ctx: TeamsCtx, personal?: PersonalSender) {
  const user = useSession((s) => s.user!);
  return (body: SendMessageBody, tempId: string): Message => {
    const own = body.kind === "reply" && body.from_account ? personal : undefined;
    const mb = body.kind === "reply" && !own ? ctx.mailbox(body.mailbox_id ?? ticket.mailbox_id) : undefined;
    const from = own ? personalFromLine(own) : mb ? fromLine(ctx, ticket, mb, user.name) : null;
    return {
      id: tempId,
      ticket_id: ticket.id,
      ai: false,
      kind: body.kind,
      direction: "outbound",
      author: { type: "user", user: { id: user.id, name: user.name, email: user.email, avatar_url: user.avatar_url }, contact: null },
      body_html: body.body_html,
      body_text: "",
      has_quoted: false,
      subject: ticket.subject,
      from_email: from?.email ?? "",
      from_name: from?.name ?? user.name,
      mailbox_id: mb?.id ?? null,
      from_account:
        own && body.from_account ? { account_id: own.account.id, email: own.identity.email, reply_to: body.from_account.reply_to ?? "" } : null,
      to: body.kind === "reply" && ticket.contact?.email ? [ticket.contact.email] : [],
      cc: body.cc ?? [],
      bcc: body.bcc ?? [],
      attachments: [],
      delivery_status: body.kind === "reply" ? "pending" : "none",
      delivery_error: "",
      edited_at: null,
      created_at: new Date().toISOString(),
    };
  };
}

/** Starts a third-party thread or writes in one; the draft is cleared only once it is sent. */
/** Writes in a side conversation. What follows a send (the draft back to replying, the status,
 * the toast) is the mutation's own, so it happens even if the ticket was left meanwhile. */
function useSendThread(ticket: Ticket) {
  const qc = useQueryClient();
  const { t } = useTranslation("inbox");
  useTranslation("indicators");
  const updateTicket = useUpdateTicket();
  const keep = (m: Message, th?: Thread) => {
    qc.setQueryData<Message[]>(qk.messages(ticket.id), (old) => {
      const rest = (old ?? []).filter((x) => x.id !== m.id);
      const live = (old ?? []).find((x) => x.id === m.id);
      return [...rest, live ?? m].sort((a, b) => Date.parse(a.created_at) - Date.parse(b.created_at));
    });
    if (th) upsertInList(qc, qk.threads(ticket.id), th);
  };
  return useMutation({
    mutationFn: async (v: {
      threadId?: string;
      to: string[];
      cc: string[];
      subject: string;
      body_html: string;
      attachment_ids: string[];
      include_history: boolean;
      forward_attachment_ids: string[];
      /** Set on the ticket once sent ("on hold" while waiting on the third party). */
      status?: TicketStatus;
    }): Promise<{ message: Message; thread?: Thread }> => {
      if (v.threadId) return { message: await api.tickets.sendInThread(ticket.id, v.threadId, { body_html: v.body_html, attachment_ids: v.attachment_ids }) };
      return api.tickets.startThread(ticket.id, {
        to: v.to,
        cc: v.cc.length ? v.cc : undefined,
        subject: v.subject,
        body_html: v.body_html,
        attachment_ids: v.attachment_ids,
        include_history: v.include_history || undefined,
        forward_attachment_ids: v.forward_attachment_ids.length ? v.forward_attachment_ids : undefined,
      });
    },
    onSuccess: (r, v) => {
      keep(r.message, r.thread);
      // Back to replying: the next thing typed here must not go to the third party by
      // mistake. Writing in the side conversation again starts from its message.
      useDrafts.setState((s) => ({
        [ticket.id]: { ...(s[ticket.id] ?? emptyDraft), mode: "reply", html: "", threadId: undefined, threadTo: [], threadCc: [], threadSubject: "", threadHistory: false, threadFiles: [] },
      }));
      if (v.status) updateTicket.mutate({ id: ticket.id, patch: { status: v.status } });
      const to = r.message.to.join(", ");
      toast.success(v.status ? t("composer.sideSentAndMarked", { to, status: statusLabel(v.status).toLowerCase() }) : t("composer.sideSent", { to }));
    },
    onError: (e) => toast.error(e, t("composer.threadFailed")),
  });
}

export const Composer = forwardRef<ComposerHandle, { ticket: Ticket; disabled?: boolean }>(function Composer({ ticket, disabled }, ref) {
  const { t } = useTranslation("inbox");
  useTranslation("indicators");
  useTranslation("ui");
  const reduced = useReducedMotion();
  // Writing: focus went into the editor and has not left the composer since.
  // Focus on the controls above the editor (mode, Cc, the reply address) does not
  // open it up: growing moves those controls under the pointer mid-click.
  const [focused, setFocused] = useState(false);
  const writingArea = useRef<HTMLDivElement>(null);
  const leaving = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(leaving.current), []);
  const draft = useDrafts((s) => s[ticket.id]) ?? emptyDraft;
  const setDraft = useCallback((p: Partial<Draft>) => useDrafts.setState((s) => ({ [ticket.id]: { ...(s[ticket.id] ?? emptyDraft), ...p } })), [ticket.id]);
  const { mode } = draft;

  const { data: members } = useQuery(q.members());
  const { data: threads } = useQuery(q.threads(ticket.id));
  const thread = draft.threadId ? threads?.find((x) => x.id === draft.threadId) : undefined;
  // Starting a side conversation: what of the customer's conversation goes along.
  const starting = draft.mode === "thread" && !thread;
  const include = useSideInclude(ticket.id, draft.threadHistory, draft.threadFiles, setDraft);
  const { data: allReplies } = useQuery(q.savedReplies());
  // Replies for everyone, plus the ticket team's own.
  const replies = useMemo(() => (allReplies ?? []).filter((r) => !r.team_id || r.team_id === ticket.team_id), [allReplies, ticket.team_id]);
  const teams = useTeamsCtx();
  const qc = useQueryClient();
  const viewers = useTicketViewers(ticket.id);
  const others = viewers.filter((v) => v.state === "replying");

  // Variables in replies: "{{" offers them with this ticket's values, from the address the reply goes out from.
  const catalog = useCatalog("reply");
  const { data: vars } = useQuery({ ...q.ticketVariables(ticket.id, draft.mailboxId ?? ticket.mailbox_id), enabled: mode === "reply" });
  const varItems: VarItem[] = catalog.variables.map((v) => {
    const value = vars?.values[v.key];
    return { key: v.key, label: v.label, group: templateGroupLabel(v.group), value: vars ? (v.html && value ? t("composer.valueSet") : (value ?? "")) : undefined };
  });

  const editor = useRef<RichEditorHandle>(null);
  const membersRef = useRef<Member[]>([]);
  const repliesRef = useRef<SavedReply[]>([]);
  const modeRef = useRef<Mode>(mode);
  const ticketRef = useRef(ticket.id);
  const knownRef = useRef<Set<string> | null>(null);
  const varItemsRef = useRef<VarItem[]>([]);
  const varsRef = useRef(vars);
  const catalogRef = useRef(catalog);
  const tRef = useRef(t);
  membersRef.current = members ?? [];
  repliesRef.current = replies;
  modeRef.current = mode;
  ticketRef.current = ticket.id;
  knownRef.current = catalog.loading ? null : catalog.known;
  varItemsRef.current = varItems;
  varsRef.current = vars;
  catalogRef.current = catalog;
  tRef.current = t;

  // Saved replies go in filled for this ticket, so the agent reads real words; notes are never filled.
  // In a side conversation the server fills them without the customer's or the ticket's details.
  // A reply that can't be filled goes in as written (the server fills it on send).
  const [filling, setFilling] = useState(0);
  const insertSavedReply = useCallback((html: string, clear?: () => void) => {
    clear?.();
    if (modeRef.current === "note") return editor.current?.insertHtml(html);
    const id = ticketRef.current;
    setFilling((n) => n + 1);
    api.tickets
      .render(id, html, modeRef.current === "thread")
      .then((r) => r.body_html, () => html)
      .then((out) => {
        // Another ticket opened meanwhile: this one's draft is not the place.
        if (ticketRef.current === id) editor.current?.insertHtml(out);
      })
      .finally(() => setFilling((n) => n - 1));
  }, []);

  const extensions = useMemo(
    () => [
      mentionExtension(() => membersRef.current, () => modeRef.current === "note"),
      savedReplyExtension(() => repliesRef.current, insertSavedReply),
      templateExtension({
        known: () => knownRef.current,
        items: (query) => matchVars(varItemsRef.current, query, (g) => g),
        insert: (item) => variableText(item.key, varsRef.current?.values[item.key], catalogRef.current.byKey.get(item.key)?.html),
        filters: () => catalogRef.current.filters,
        enabled: () => modeRef.current === "reply",
        // Read when the list opens, so it follows the language.
        get valueLabel() {
          return tRef.current("composer.thisTicket");
        },
      }),
    ],
    [insertSavedReply],
  );
  // Unknown tags are marked once the list is in.
  useEffect(() => {
    if (!catalog.loading) refreshTags(editor.current?.editor);
  }, [catalog.loading, catalog.known]);

  const [uploads, setUploads] = useState<Upload[]>([]);
  const [dragging, setDragging] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);
  // My own Collab addresses: a reply goes from one when I pick it, or when I last answered this ticket from it.
  const personal = usePersonalSenders(ticket, teams.mailbox(ticket.mailbox_id));
  const personalFrom = draft.fromAccount === undefined ? personal.sticky : (draft.fromAccount ?? undefined);
  const personalSender = personal.find(personalFrom);
  const tempMessage = useTempMessage(ticket, teams, personalSender);
  const me = useSession((s) => s.user!.id);
  const userName = useSession((s) => s.user!.name);
  const { data: workspace } = useQuery(q.workspace());
  // Email replies wait a few seconds by default, chat replies go at once (settings).
  const undoSeconds = undoSecondsFor(workspace?.settings, ticket.channel);
  const sendThread = useSendThread(ticket);
  // Switching to a side conversation fills in a subject to edit, so what goes out is visible.
  const toMode = useCallback(
    (m: Mode) => {
      const cur = useDrafts.getState()[ticket.id] ?? emptyDraft;
      setDraft(m === "thread" && !cur.threadSubject ? { mode: m, threadSubject: ticket.subject } : { mode: m });
    },
    [setDraft, ticket.id, ticket.subject],
  );
  const retries = useRef(new Map<string, SendMessageBody>());
  const ai = useComposerAi(editor, ticket, mode === "reply" ? "reply" : "note");
  /** Ollie drafts a reply; false when it can't here (AI off, the composer is disabled, or a side
   * conversation is being written, where a draft could carry the customer's details). */
  const draftReply = () => {
    if (!ai.available || disabled || mode === "thread") return false;
    if (mode !== "reply") setDraft({ mode: "reply" });
    ai.draft();
    return true;
  };

  useImperativeHandle(ref, () => ({
    focus: (m) => {
      if (m) toMode(m);
      setTimeout(() => editor.current?.focus(), 0);
    },
    // A failed send: a local one is replaced, a bounced one is sent again as a new message.
    retry: (m) => {
      // A side conversation's message goes to its recipients again, in that conversation.
      if (m.thread_id) {
        sendThread.mutate({ threadId: m.thread_id, to: [], cc: [], subject: "", body_html: m.body_html, attachment_ids: [], include_history: false, forward_attachment_ids: [] });
        return;
      }
      const body = retries.current.get(m.id) ?? { kind: m.kind, body_html: m.body_html, cc: m.cc.length ? m.cc : undefined, bcc: m.bcc.length ? m.bcc : undefined };
      if (m.id.startsWith("tmp-")) qc.setQueryData<Message[]>(qk.messages(ticket.id), (old) => old?.filter((x) => x.id !== m.id));
      const tempId = `tmp-${Date.now().toString(36)}`;
      retries.current.set(tempId, body);
      void deliver(tempMessage(body, tempId), body, me);
    },
    aiDraft: () => void draftReply(),
    inThread: (th) => {
      setDraft({ mode: "thread", threadId: th.id });
      setTimeout(() => editor.current?.focus(), 0);
    },
    fromNote: (m) => {
      const cur = useDrafts.getState()[ticket.id] ?? emptyDraft;
      const html = cur.html && cur.mode === "reply" ? cur.html + m.body_html : m.body_html;
      setDraft({ mode: "reply", html, fromNote: m.id });
      editor.current?.setHtml(html);
      setTimeout(() => editor.current?.focus(), 0);
    },
  }));

  // The ticket's shared draft goes into the reply box; discarding it takes it back out.
  const takeShared = useCallback(
    (html: string) => {
      setDraft({ mode: "reply", html });
      editor.current?.setHtml(html);
    },
    [setDraft],
  );
  const dropShared = useCallback(() => {
    setDraft({ html: "" });
    editor.current?.clear();
  }, [setDraft]);

  // Presence: "replying" while a draft exists, "viewing" otherwise.
  const hasDraft = !!draft.html;
  useEffect(() => {
    realtime()?.setPresenceState(hasDraft && mode === "reply" ? "replying" : "viewing");
  }, [hasDraft, mode]);

  const upload = (files: FileList | File[]) => uploadFiles(files, setUploads);

  const busyUploads = uploads.some((u) => u.status === "uploading");
  // A reply from an address that has no way to send is refused (mailbox_cannot_send): say so up front.
  const noSender =
    (mode === "reply" && ticket.channel === "email" && !personalSender && teams.mailbox(draft.mailboxId ?? ticket.mailbox_id)?.sending === "system_off") ||
    (mode === "thread" && teams.mailbox(thread?.mailbox_id ?? ticket.mailbox_id)?.sending === "system_off");
  const threadReady = mode !== "thread" || !!thread || (draft.threadTo.length > 0 && !!draft.threadSubject.trim());
  const canSend = !!draft.html && !busyUploads && !disabled && !ai.busy && !noSender && !filling && threadReady && !sendThread.isPending;

  // The send button's moment: a check once it is out, or once a reply is on hold (its toast
  // and the timeline count down from there).
  const [sent, setSent] = useState(false);
  useEffect(() => {
    if (!sent) return;
    const id = setTimeout(() => setSent(false), 1200);
    return () => clearTimeout(id);
  }, [sent]);
  const phase = sendThread.isPending ? "sending" : sent ? "sent" : "idle";

  // A reply taken back during its undo window comes back here, after anything typed since.
  const unsent = useUnsent((s) => s[ticket.id]);
  useEffect(() => {
    if (!unsent) return;
    useUnsent.setState((s) => Object.fromEntries(Object.entries(s).filter(([k]) => k !== ticket.id)), true);
    const cur = useDrafts.getState()[ticket.id] ?? emptyDraft;
    const html = cur.html ? cur.html + unsent.html : unsent.html;
    setDraft({
      mode: "reply",
      html,
      cc: unsent.cc,
      bcc: unsent.bcc,
      showCc: unsent.cc.length + unsent.bcc.length > 0,
      mailboxId: unsent.mailboxId,
      fromAccount: unsent.fromAccount,
      fromNote: unsent.fromNote,
    });
    editor.current?.setHtml(html);
    setUploads((u) => [...u, ...unsent.uploads]);
    setTimeout(() => editor.current?.focus(), 0);
  }, [unsent, ticket.id, setDraft]);

  const submitThread = (status?: TicketStatus) => {
    const attachments = uploads.filter((u) => u.attachment).map((u) => u.attachment!.id);
    sendThread.mutate(
      {
        threadId: thread?.id,
        to: draft.threadTo,
        cc: draft.threadCc,
        subject: draft.threadSubject.trim(),
        body_html: draft.html,
        attachment_ids: attachments,
        include_history: !thread && include.history,
        forward_attachment_ids: thread ? [] : include.picked,
        status,
      },
      {
        onSuccess: () => {
          setSent(true);
          setTimeout(() => setSent(false), 1200);
          uploads.forEach((u) => u.preview && URL.revokeObjectURL(u.preview));
          setUploads([]);
          editor.current?.clear();
        },
      },
    );
  };

  // A reply that @mentions a teammate was most likely meant as a note: ask before it goes out.
  const [mentionWarn, setMentionWarn] = useState<{ names: string[]; status?: TicketStatus } | null>(null);
  useEffect(() => setMentionWarn(null), [draft.html, mode]);

  /** Sends what is written; `as` sends it as a note instead, `checked` skips the mention check. */
  const submit = (status?: TicketStatus, opts: { as?: "note"; checked?: boolean } = {}) => {
    if (!canSend) return;
    if (mode === "thread") return submitThread(status);
    const kind = opts.as ?? mode;
    const reply = kind === "reply";
    if (reply && !opts.checked) {
      const names = mentionedTeammates(draft.html, members ?? [], me);
      if (names.length) return setMentionWarn({ names, status });
    }
    setMentionWarn(null);
    if (opts.as === "note" && draft.fromNote) {
      // A reply written from a note, kept as a note: it goes back into that note (edited if
      // the words changed) rather than adding the same words twice. Its status went with
      // answering the customer.
      const attachments = uploads.filter((u) => u.attachment).map((u) => u.attachment!.id);
      void backToNote(tempMessage({ kind: "note", body_html: draft.html }, `tmp-${Date.now().toString(36)}`), draft.fromNote, draft.html, attachments, me);
      uploads.forEach((u) => u.preview && URL.revokeObjectURL(u.preview));
      setUploads([]);
      setDraft({ html: "", cc: [], bcc: [], showCc: false, mailboxId: undefined, fromAccount: undefined, fromNote: undefined, mode: "reply" });
      editor.current?.clear();
      ai.dismissSources();
      return;
    }
    const body: SendMessageBody = {
      kind,
      body_html: draft.html,
      cc: reply && draft.cc.length ? draft.cc : undefined,
      bcc: reply && draft.bcc.length ? draft.bcc : undefined,
      attachment_ids: uploads.filter((u) => u.attachment).map((u) => u.attachment!.id),
      status,
      mailbox_id: reply && !personalSender && draft.mailboxId && draft.mailboxId !== ticket.mailbox_id ? draft.mailboxId : undefined,
      from_account:
        reply && personalSender && personalFrom
          ? { account_id: personalSender.account.id, email: personalSender.identity.email, reply_to: personalFrom.replyTo || undefined }
          : undefined,
      from_note_id: reply ? draft.fromNote : undefined,
    };
    const tempId = `tmp-${Date.now().toString(36)}`;
    retries.current.set(tempId, body);
    const temp = tempMessage(body, tempId);
    if (reply)
      hold(
        temp,
        body,
        { html: draft.html, cc: draft.cc, bcc: draft.bcc, mailboxId: draft.mailboxId, fromAccount: draft.fromAccount, uploads, fromNote: draft.fromNote },
        me,
        undoSeconds,
      );
    else {
      uploads.forEach((u) => u.preview && URL.revokeObjectURL(u.preview));
      void deliver(temp, body, me);
    }
    setSent(true);
    setUploads([]);
    setDraft({ html: "", cc: [], bcc: [], showCc: false, mailboxId: undefined, fromAccount: undefined, fromNote: undefined, ...(opts.as ? { mode: "reply" } : {}) });
    editor.current?.clear();
    ai.dismissSources();
    // A held reply's toast says what it will do; it sets the status only once it is sent.
    if (status && (!reply || undoSeconds <= 0))
      toast.success(t(reply ? "composer.sentAndMarked" : "composer.noteAndMarked", { status: statusLabel(status).toLowerCase() }));
  };

  const onKeyDown = (e: KeyboardEvent) => {
    if (ticket.channel === "chat" && mode === "reply") realtime()?.typing(ticket.contact?.id);
    else realtime()?.typing();
    if (e.key === "Enter" && (e.metaKey || e.ctrlKey) && e.shiftKey) {
      e.preventDefault();
      // Solving is not on offer while asking a third party.
      if (mode !== "thread") submit("solved");
      return true;
    }
    return false;
  };

  const note = mode === "note";
  const threadMode = mode === "thread";
  const recipient = ticket.contact?.email ?? ticket.contact?.name ?? t("composer.theCustomer");
  // An email reply: from one of my own addresses, or from a team address.
  const replyMailboxId = draft.mailboxId ?? ticket.mailbox_id;
  const replyMailbox = teams.mailbox(replyMailboxId);
  const emailReply = mode === "reply" && (!!personalSender || (!!replyMailbox && (ticket.channel === "email" || !!ticket.mailbox_id)));
  // The signature it carries, filled by the server: the team's, else the address's, else mine; from my own address, that address's.
  const signature = !emailReply ? undefined : personalSender ? identitySignature(personalSender.identity) : vars?.signature_html;
  const replyFrom = personalSender
    ? { ...personalFromLine(personalSender), replyTo: null as string | null }
    : replyMailbox
      ? fromLine(teams, ticket, replyMailbox, userName)
      : null;
  const [previewing, setPreviewing] = useState(false);
  // The editor opens up while in use or holding a draft, and rests small otherwise.
  const open = focused || !!draft.html || uploads.length > 0;

  // mod+J in the reply box asks Ollie for a draft; everywhere else it opens Ollie
  // (AssistantRoot). Handled here first, so the app-wide binding sees it as taken.
  const onComposerKey = (e: React.KeyboardEvent) => {
    if (e.defaultPrevented || e.nativeEvent.isComposing || keyName(e.nativeEvent) !== "mod+j") return;
    if (draftReply()) e.preventDefault();
  };

  return (
    <div className="shrink-0 px-5 pb-4" onKeyDown={onComposerKey}>
      <div className="mx-auto w-full max-w-[820px]">
        {!disabled && (
          <SuggestedReplyCard
            ticket={ticket}
            hasDraft={!!draft.html}
            onEdit={(html) => {
              setDraft({ mode: "reply", html });
              editor.current?.setHtml(html);
              setTimeout(() => editor.current?.focus(), 0);
            }}
          />
        )}
        {!disabled && <SharedDraftBar ticket={ticket} html={draft.html} onUse={takeShared} onClear={dropShared} />}
        <AnimatePresence initial={false}>
          {others.length > 0 && mode === "reply" && (
            <motion.div
              role="status"
              initial={reduced ? { opacity: 0 } : { opacity: 0, y: 6 }}
              animate={{ opacity: 1, y: 0 }}
              exit={reduced ? { opacity: 0 } : { opacity: 0, y: 4 }}
              transition={{ duration: 0.18 }}
              className={cn("mb-2 flex items-center gap-2 rounded-md px-3 py-2 text-[12.5px]", tone.amber.soft)}
            >
              <span className={cn("size-1.5 shrink-0 rounded-full animate-pulse-soft motion-reduce:animate-none", tone.amber.solid)} />
              <Avatar name={others[0].name} src={others[0].avatar} size="xs" />
              <span className="min-w-0 flex-1 truncate text-fg-2">
                {t("composer.othersReplying", { names: others.map((o) => o.name.split(" ")[0]).join(", "), count: others.length })}
              </span>
              <button type="button" onClick={() => setDraft({ mode: "note" })} className={cn("shrink-0 text-xs font-medium underline-offset-2 hover:underline", tone.amber.text)}>
                {t("composer.noteInstead")}
              </button>
            </motion.div>
          )}
        </AnimatePresence>
        {draft.fromNote && mode === "reply" && (
          <div role="status" className={cn("mb-2 flex items-center gap-2 rounded-md px-3 py-2 text-[12.5px] animate-fade-in motion-reduce:animate-none", tone.amber.soft)}>
            <Lock className={cn("size-3.5 shrink-0", tone.amber.text)} />
            <span className="min-w-0 flex-1 text-fg-2">{t("composer.fromNote")}</span>
            <Button
              size="xs"
              variant="ghost"
              onClick={() => {
                setDraft({ html: "", fromNote: undefined });
                editor.current?.clear();
              }}
            >
              {t("composer.fromNoteCancel")}
            </Button>
          </div>
        )}
        <AnimatePresence initial={false}>
          {mentionWarn && (
            <motion.div
              role="alert"
              initial={reduced ? { opacity: 0 } : { opacity: 0, y: 6 }}
              animate={{ opacity: 1, y: 0 }}
              exit={reduced ? { opacity: 0 } : { opacity: 0, y: 4 }}
              transition={{ duration: 0.18 }}
              className={cn("mb-2 flex items-center gap-2 rounded-md px-3 py-2 text-[12.5px]", tone.amber.soft)}
            >
              <TriangleAlert className={cn("size-3.5 shrink-0", tone.amber.text)} />
              <span className="min-w-0 flex-1 text-fg-2">{t("composer.mentionWarn", { names: mentionWarn.names.join(", ") })}</span>
              <Button size="xs" variant="secondary" onClick={() => submit(mentionWarn.status, { as: "note" })}>
                {t("composer.mentionAsNote")}
              </Button>
              <Button size="xs" variant="ghost" onClick={() => submit(mentionWarn.status, { checked: true })}>
                {t("composer.mentionSend")}
              </Button>
            </motion.div>
          )}
        </AnimatePresence>
        <div
          onDragOver={(e) => {
            if (e.dataTransfer.types.includes("Files")) {
              e.preventDefault();
              setDragging(true);
            }
          }}
          onDragLeave={(e) => {
            if (!e.currentTarget.contains(e.relatedTarget as Node)) setDragging(false);
          }}
          onDrop={(e) => {
            e.preventDefault();
            setDragging(false);
            if (e.dataTransfer.files.length) upload(e.dataTransfer.files);
          }}
          onFocus={(e) => {
            // Focus events from this composer's popovers (portalled) arrive here too, and keep it open.
            clearTimeout(leaving.current);
            if (writingArea.current?.contains(e.target)) setFocused(true);
          }}
          onBlur={(e) => {
            if (e.currentTarget.contains(e.relatedTarget as Node)) return;
            // Focus may be moving into one of its popovers: that focus event cancels this.
            clearTimeout(leaving.current);
            leaving.current = setTimeout(() => setFocused(false), 0);
          }}
          onPaste={(e) => {
            const files = Array.from(e.clipboardData.files);
            if (files.length) {
              e.preventDefault();
              upload(files);
            }
          }}
          className={cn(
            "relative rounded-lg border shadow-xs transition-[border-color,box-shadow,background-color] duration-200 focus-within:border-fg-4 focus-within:shadow-[0_0_0_3px_var(--focus)]",
            note ? "border-dashed border-note-line bg-note" : threadMode ? cn(tone.cyan.line, "bg-surface") : "border-line bg-surface",
          )}
        >
          <div className="flex h-10 items-center gap-2 border-b border-line-soft px-2">
            <Segmented
              size="xs"
              value={mode}
              onChange={toMode}
              options={[
                { value: "reply", label: t("composer.reply") },
                { value: "note", label: t("composer.note") },
                { value: "thread", label: t("composer.thread") },
              ]}
            />
            {note ? (
              <span className="truncate text-xs text-fg-3">{t("composer.noteHint")}</span>
            ) : threadMode ? (
              thread ? (
                <span className="flex min-w-0 flex-1 items-center gap-1.5 text-xs text-fg-3">
                  <MessagesSquare className={cn("size-3.5 shrink-0", tone.cyan.text)} />
                  <span className="min-w-0 truncate">
                    {t("composer.inThread", { subject: thread.subject })} <span className="mono text-fg-2" data-ph-mask>{[...thread.to, ...thread.cc].join(", ")}</span>
                  </span>
                  <Tooltip content={t("composer.newThread")}>
                    <button
                      type="button"
                      aria-label={t("composer.newThread")}
                      onClick={() => setDraft({ threadId: undefined })}
                      className="flex size-5 shrink-0 items-center justify-center rounded-sm text-fg-4 hover:bg-hover hover:text-fg"
                    >
                      <X className="size-3" />
                    </button>
                  </Tooltip>
                </span>
              ) : (
                <span className="min-w-0 flex-1 truncate text-xs text-fg-3">{t("composer.threadHint")}</span>
              )
            ) : (
              <span className="min-w-0 flex-1 truncate text-xs text-fg-3">
                {t("composer.to")} <span className="mono text-fg-2" data-ph-mask>{recipient}</span>
              </span>
            )}
            {!note && !threadMode && (
              <button
                type="button"
                onClick={() => setDraft({ showCc: !draft.showCc })}
                aria-expanded={draft.showCc}
                className={cn("ml-auto shrink-0 rounded-sm px-1.5 py-0.5 text-xs text-fg-3 transition-colors hover:bg-hover hover:text-fg", draft.showCc && "bg-hover text-fg")}
              >
                {t("composer.ccBcc")}
              </button>
            )}
          </div>
          <AnimatePresence initial={false}>
            {mode === "reply" && draft.showCc && (
              <motion.div
                key="cc"
                initial={reduced ? { opacity: 0 } : { height: 0, opacity: 0 }}
                animate={reduced ? { opacity: 1 } : { height: "auto", opacity: 1 }}
                exit={reduced ? { opacity: 0 } : { height: 0, opacity: 0 }}
                transition={{ duration: 0.18, ease: [0.16, 1, 0.3, 1] }}
                className="overflow-hidden"
              >
                <EmailChips label={t("composer.cc")} values={draft.cc} onChange={(cc) => setDraft({ cc })} />
                <EmailChips label={t("composer.bcc")} values={draft.bcc} onChange={(bcc) => setDraft({ bcc })} />
              </motion.div>
            )}
          </AnimatePresence>
          {threadMode && !thread && (
            <div>
              <EmailChips wide label={t("composer.to")} values={draft.threadTo} onChange={(threadTo) => setDraft({ threadTo })} />
              <EmailChips wide label={t("composer.cc")} values={draft.threadCc} onChange={(threadCc) => setDraft({ threadCc })} />
              <div className="flex h-8 items-center gap-2 border-b border-line-soft px-3">
                <span className="w-12 shrink-0 text-xs text-fg-3">{t("composer.subject")}</span>
                <input
                  value={draft.threadSubject}
                  onChange={(e) => setDraft({ threadSubject: e.target.value })}
                  aria-label={t("composer.subject")}
                  placeholder={t("composer.subject")}
                  maxLength={500}
                  className="h-6 min-w-0 flex-1 bg-transparent text-[12.5px] outline-none placeholder:text-fg-4"
                />
              </div>
            </div>
          )}
          {mode === "reply" && (
            <ReplyingAs
              ticket={ticket}
              ctx={teams}
              mailboxId={draft.mailboxId ?? ticket.mailbox_id}
              chosen={!personalSender && !!draft.mailboxId && draft.mailboxId !== ticket.mailbox_id}
              onChange={(id) => setDraft({ mailboxId: id === ticket.mailbox_id ? undefined : id, fromAccount: personal.sticky ? null : undefined })}
              senders={personal.senders}
              personal={personalSender && personalFrom ? { sender: personalSender, replyTo: personalFrom.replyTo } : undefined}
              defaultReplyTo={personal.defaultReplyTo}
              onPersonal={(f) => setDraft({ fromAccount: f, mailboxId: undefined })}
            />
          )}
          <div ref={writingArea}>
            <RichEditor
              ref={editor}
              value={draft.html}
              onChange={(html) => setDraft({ html })}
              placeholder={
                note
                  ? t("composer.notePlaceholder")
                  : threadMode
                    ? t("composer.threadPlaceholder")
                    : t("composer.replyPlaceholder", { braces: "{{", interpolation: { escapeValue: false } })
              }
              extensions={extensions}
              onSubmit={() => submit()}
              onKeyDown={onKeyDown}
              editable={!disabled && !ai.busy}
              contentClassName={cn("max-h-[40vh] transition-[min-height] duration-200 ease-out motion-reduce:transition-none", open ? "min-h-[96px]" : "min-h-[44px]")}
              afterContent={open && signature?.trim() ? <ReplySignature html={signature} /> : null}
              ariaLabel={note ? t("composer.note") : threadMode ? t("composer.thread") : t("composer.reply")}
              toolbarEnd={
                <>
                  {/* Ollie writes from the ticket, so it stays out of what goes to a third party. */}
                  {!threadMode && <ComposerAiMenu ai={ai} hasDraft={!!draft.html} disabled={disabled} />}
                  <input ref={fileInput} type="file" multiple hidden onChange={(e) => e.target.files && upload(e.target.files)} />
                  <Tooltip content={t("composer.attach")}>
                    <button
                      type="button"
                      aria-label={t("composer.attach")}
                      onClick={() => fileInput.current?.click()}
                      className="flex size-7 items-center justify-center rounded-md text-fg-3 transition-colors hover:bg-hover hover:text-fg active:scale-95"
                    >
                      <Paperclip className="size-[15px]" />
                    </button>
                  </Tooltip>
                  <SavedReplyButton replies={replies} filling={filling > 0} onPick={(r) => insertSavedReply(r.body_html)} />
                  {emailReply && (
                    <Tooltip content={draft.html ? t("composer.previewEmail") : t("composer.previewEmpty")}>
                      <button
                        type="button"
                        aria-label={t("composer.previewEmail")}
                        aria-disabled={!draft.html}
                        onClick={() => draft.html && setPreviewing(true)}
                        className="flex size-7 items-center justify-center rounded-md text-fg-3 transition-colors hover:bg-hover hover:text-fg active:scale-95 aria-disabled:opacity-50 aria-disabled:hover:bg-transparent aria-disabled:hover:text-fg-3"
                      >
                        <Eye className="size-[15px]" />
                      </button>
                    </Tooltip>
                  )}
                  <span className="mx-1 h-4 w-px bg-line" />
                  <SendButton
                    note={note}
                    sideTo={threadMode ? (thread ? [...thread.to, ...thread.cc] : [...draft.threadTo, ...draft.threadCc]) : undefined}
                    include={starting ? include : undefined}
                    disabled={!canSend}
                    busy={busyUploads}
                    onSend={submit}
                    current={ticket.status}
                    phase={phase}
                  />
                </>
              }
            />
          </div>
          {!threadMode && <ComposerSources ai={ai} />}
          {!threadMode && <ComposerAiStatus ai={ai} />}
          {(uploads.length > 0 || (starting && (include.history || include.picked.length > 0))) && (
            <div className="flex flex-wrap gap-1.5 px-3 pb-2.5">
              {starting && <SideIncludedChips inc={include} />}
              {uploads.map((u) => (
                <UploadChip key={u.key} u={u} onRemove={() => setUploads((x) => x.filter((y) => y.key !== u.key))} />
              ))}
            </div>
          )}
          {dragging && (
            <div className="pointer-events-none absolute inset-0 flex items-center justify-center rounded-lg border-2 border-dashed border-fg-3 bg-surface/90 text-[13px] font-medium text-fg-2 animate-fade-in">
              {t("composer.drop")}
            </div>
          )}
        </div>
        <SuggestionPopup />
        <VarSuggestPopup />
        {emailReply && (
          <ReplyPreview
            open={previewing && !!draft.html}
            onOpenChange={setPreviewing}
            ticket={ticket}
            html={draft.html}
            from={replyFrom}
            to={recipient}
            cc={draft.cc}
            bcc={draft.bcc}
            files={uploads.filter((u) => u.status !== "error").map((u) => u.file.name)}
            mailboxId={replyMailboxId}
            personalSignature={personalSender ? (signature ?? "") : undefined}
            canSend={canSend}
            onSend={() => {
              setPreviewing(false);
              submit();
            }}
          />
        )}
      </div>
    </div>
  );
});

/**
 * Who the customer sees the reply from, and from which address: the address's From
 * name format filled in. Click to send this reply from another of the brand's
 * addresses (it becomes the ticket's reply address), or from one of my own Collab
 * addresses (the reply address stays), with where the customer's answers go.
 */
function ReplyingAs({
  ticket,
  ctx,
  mailboxId,
  chosen,
  onChange,
  senders,
  personal,
  defaultReplyTo,
  onPersonal,
}: {
  ticket: Ticket;
  ctx: TeamsCtx;
  mailboxId: string | null;
  chosen: boolean;
  onChange: (id: string) => void;
  /** My own Collab addresses this reply may go from. */
  senders: PersonalSender[];
  /** The one it goes from, with the Reply-To asked for ("" = the workspace's). */
  personal?: { sender: PersonalSender; replyTo: string };
  defaultReplyTo: "address" | "mailbox";
  onPersonal: (f: PersonalFrom) => void;
}) {
  const { t } = useTranslation("inbox");
  const user = useSession((s) => s.user!);
  const { data: activities } = useQuery(q.activities(ticket.id));
  const { data: messages } = useQuery(q.messages(ticket.id));
  const mailbox = ctx.mailbox(mailboxId);
  const options = replyAddresses(ctx, ticket);
  // Without an address of its own, an email ticket still offers mine.
  if (!mailbox && !personal && senders.length === 0) return null;
  if (!personal && ticket.channel !== "email" && !ticket.mailbox_id) return null;
  const team = mailbox ? fromLine(ctx, ticket, mailbox, user.name) : null;
  const from = personal ? { ...personalFromLine(personal.sender), replyTo: null as string | null } : team;
  const moved = chosen || personal || !mailbox ? null : movedTo(activities, messages, mailbox, ctx);
  const canPick = senders.length > 0 || options.length > 1 || (options.length === 1 && options[0].id !== mailbox?.id);
  const grouped = senders.length > 0;
  const chip = (
    <button
      type="button"
      disabled={!canPick}
      aria-label={t("composer.changeAddress")}
      className="group flex h-6 min-w-0 max-w-full items-center gap-1.5 rounded-sm px-1.5 text-left text-[12px] outline-none transition-colors hover:bg-hover focus-visible:shadow-[0_0_0_2px_var(--focus)] disabled:pointer-events-none data-[state=open]:bg-hover"
    >
      {personal && <AccountDot color={personal.sender.account.color} className="size-2 shrink-0" />}
      {from ? (
        <>
          <span className="truncate text-fg">{from.name}</span>
          <span className="mono min-w-0 truncate text-[11.5px] text-fg-3" data-ph-mask>
            &lt;{from.email}&gt;
          </span>
        </>
      ) : (
        <span className="truncate text-fg-3">{t("composer.ticketAddress")}</span>
      )}
      {from?.replyTo && (
        <span className="min-w-0 shrink truncate text-[11.5px] text-fg-4" data-ph-mask>
          {t("composer.repliesTo", { address: from.replyTo })}
        </span>
      )}
      {!personal && mailbox?.private && <Lock className="size-3 shrink-0 text-fg-4" />}
      {canPick && <ChevronDown className="size-3 shrink-0 text-fg-4 transition-colors group-hover:text-fg-2" />}
    </button>
  );
  return (
    <div className="border-b border-line-soft">
      <div className="@container flex min-h-8 items-center gap-2 px-3">
        <span className="shrink-0 text-xs text-fg-3">{t("composer.replyingAs")}</span>
        <div className="flex min-w-0 flex-1 items-center">
          {canPick ? (
            <Picker
              width={340}
              value={personal ? personalValue(personal.sender.account.id, personal.sender.identity.email) : (mailbox?.id ?? null)}
              placeholder={t("props.searchAddresses")}
              items={[
                ...options.map((m) => ({
                  value: m.id,
                  label: addressLabel(m),
                  keywords: [m.email, ...(m.aliases ?? [])],
                  icon: <TeamDot team={ctx.team(m.team_id)} />,
                  hint: m.private ? <Lock className="size-3" /> : undefined,
                  group: grouped ? t("composer.teamAddresses") : undefined,
                })),
                ...senders.map((x) => {
                  const line = personalFromLine(x);
                  return {
                    value: personalValue(x.account.id, x.identity.email),
                    label: `${line.name} <${line.email}>`,
                    keywords: [line.email, x.account.name],
                    icon: <AccountDot color={x.account.color} className="size-2" />,
                    hint: x.account.visibility === "shared" ? t("composer.sharedAccount") : undefined,
                    group: t("composer.yourAddresses"),
                  };
                }),
              ]}
              onSelect={(v) => {
                const own = parsePersonalValue(v);
                if (own) onPersonal({ ...own, replyTo: personal?.replyTo ?? "" });
                else onChange(v);
              }}
              trigger={chip}
            />
          ) : (
            chip
          )}
        </div>
      </div>
      {personal && (
        <ReplyToLine
          sender={personal.sender}
          value={personal.replyTo}
          fallback={defaultReplyTo}
          mailboxAddress={team ? (team.replyTo ?? team.email) : null}
          onChange={(replyTo) =>
            onPersonal({ accountId: personal.sender.account.id, email: personal.sender.identity.email, replyTo })
          }
        />
      )}
      {!personal && mailbox?.sending === "system_off" && (
        <p className="flex items-start gap-1.5 px-3 pb-1.5 text-[11.5px] leading-4 text-fg-2 animate-fade-in motion-reduce:animate-none" role="status">
          <TriangleAlert className="mt-px size-3 shrink-0 text-tone-amber" aria-hidden />
          {t("composer.cannotSend")}
        </p>
      )}
      {personal && (
        <p className="flex items-center gap-1.5 px-3 pb-1.5 text-[11.5px] text-fg-3 animate-fade-in motion-reduce:animate-none">
          <ArrowRightLeft className="size-3 shrink-0 text-fg-4" />
          {t("composer.personalChosen")}
        </p>
      )}
      {!personal && (moved || chosen) && (
        <p className="flex items-center gap-1.5 px-3 pb-1.5 text-[11.5px] text-fg-3 animate-fade-in motion-reduce:animate-none">
          <ArrowRightLeft className="size-3 shrink-0 text-fg-4" />
          {chosen ? t("composer.addressChosen") : t("composer.addressMoved", { team: moved })}
        </p>
      )}
    </div>
  );
}

/** Teammates a reply names with "@" (full name, first name or email), but not the writer. */
function mentionedTeammates(html: string, members: Member[], me: string): string[] {
  const text = html.replace(/<[^>]+>/g, " ").replace(/&nbsp;/g, " ").toLowerCase();
  if (!text.includes("@")) return [];
  const named = (handle: string) => {
    const h = handle.trim().toLowerCase();
    if (h.length < 2) return false;
    return new RegExp(`(^|[^\\p{L}\\p{N}])@${h.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}(?![\\p{L}\\p{N}@])`, "u").test(text);
  };
  return members
    .filter((m) => m.user.id !== me && [m.user.name, m.user.name.split(" ")[0], m.user.email].some((h) => h && named(h)))
    .map((m) => m.user.name);
}

/** The team a transfer switched the reply address for, until the next reply goes out. */
function movedTo(activities: Activity[] | undefined, messages: Message[] | undefined, mailbox: Mailbox, ctx: TeamsCtx): string | null {
  const a = [...(activities ?? [])]
    .reverse()
    .find((x) => x.type === "team_changed" && (x.data.mailbox_to === mailbox.id || x.data.mailbox_to === mailbox.email));
  if (!a) return null;
  const since = Date.parse(a.created_at);
  if ((messages ?? []).some((m) => m.direction === "outbound" && m.kind === "reply" && Date.parse(m.created_at) > since)) return null;
  const to = a.data.to;
  return (typeof to === "string" ? (ctx.team(to)?.name ?? to) : null) ?? ctx.team(mailbox.team_id)?.name ?? null;
}

function SendButton({
  note,
  sideTo,
  include,
  disabled,
  busy,
  onSend,
  current,
  phase,
}: {
  note: boolean;
  /** A side conversation's recipients: it goes to them only, and only "on hold" (waiting on a third party) rides along. */
  sideTo?: string[];
  /** Starting a side conversation: what of the customer's conversation can go along. */
  include?: SideInclude;
  disabled: boolean;
  busy: boolean;
  onSend: (s?: TicketStatus) => void;
  current: TicketStatus;
  phase: "idle" | "sending" | "sent";
}) {
  const { t } = useTranslation("inbox");
  useTranslation("indicators");
  const side = !!sideTo;
  const label = note ? t("composer.addNote") : t("composer.send");
  const options: TicketStatus[] = (side ? (["on_hold"] as TicketStatus[]) : (["pending", "on_hold", "solved"] as TicketStatus[])).filter((s) => s !== current);
  const includes = !!include && (include.quoted > 0 || include.files.length > 0);
  const menu = options.length > 0 || includes;
  const along = useIncludeSummary(include);
  const hint = busy
    ? t("composer.waitUploads")
    : note
      ? t("composer.addNote")
      : side
        ? sideTo.length
          ? t("composer.sendTo", { to: sideTo.join(", ") }) + (along ? ` ${along}` : "")
          : t("composer.addRecipient")
        : t("composer.sendReply");
  return (
    <div className="flex items-center">
      <Tooltip content={hint} shortcut="mod enter">
        <Button variant="primary" size="sm" disabled={disabled && phase === "idle"} onClick={() => onSend()} className={cn(menu && "rounded-r-none", "active:scale-[0.97]")}>
          {/* The label keeps the width while the spinner and check swap in. */}
          <span className="relative inline-grid place-items-center">
            <span className={cn("col-start-1 row-start-1 transition-opacity", phase !== "idle" && "opacity-0")}>{label}</span>
            <span className="col-start-1 row-start-1 flex">
              <Morph k={phase}>
                {phase === "sending" ? (
                  <Loader2 className="size-3.5 animate-spin" aria-label={t("composer.sending")} />
                ) : phase === "sent" ? (
                  <Check className="size-3.5" strokeWidth={2.5} aria-label={t("composer.sent")} />
                ) : null}
              </Morph>
            </span>
          </span>
        </Button>
      </Tooltip>
      {menu && (
        <M.Root>
          {/* Choosing what goes along works before the draft can be sent. */}
          <Tooltip content={includes ? t("side.optionsHint") : undefined}>
            <M.Trigger asChild disabled={disabled && !includes}>
              <Button
                variant="primary"
                size="sm"
                square
                aria-label={includes ? t("side.options") : t("composer.andStatus", { label })}
                className="rounded-l-none border-l border-white/15 dark:border-black/15"
                icon={<ChevronDown className="size-3.5" />}
              />
            </M.Trigger>
          </Tooltip>
          <M.Portal>
            <M.Content side="top" align="end" sideOffset={6} className={cn(floatingPanel, includes ? "w-[300px]" : "min-w-[230px]", "p-1")}>
              {includes && <SideIncludeItems inc={include} />}
              {includes && options.length > 0 && <M.Separator className="-mx-1 my-1 h-px bg-line" />}
              {options.length > 0 && <div className="label-caps px-2 pb-1 pt-1.5">{t("composer.andStatus", { label })}</div>}
              {options.map((s) => (
                <M.Item key={s} disabled={disabled} onSelect={() => onSend(s)} className="flex h-8 cursor-default items-center gap-2 rounded-md px-2 text-[12.5px] outline-none data-[disabled]:opacity-50 data-[highlighted]:bg-hover">
                  <StatusIcon status={s} />
                  <span className="flex-1 whitespace-nowrap">{t("composer.andMark", { label, status: statusLabel(s).toLowerCase() })}</span>
                  {s === "solved" && <Kbd className="ml-3 shrink-0">mod shift enter</Kbd>}
                </M.Item>
              ))}
            </M.Content>
          </M.Portal>
        </M.Root>
      )}
    </div>
  );
}
