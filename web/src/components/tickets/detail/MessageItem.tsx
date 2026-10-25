import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertCircle, ArrowDown, Check, CheckCheck, Download, FileText, Globe, ImageOff, Lock, Mail, MessagesSquare, MoreHorizontal, Pencil, Quote, Reply, RotateCcw, RotateCw, Send, ShieldAlert, Trash2, Undo2 } from "lucide-react";
import { memo, useMemo, useRef, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { tone } from "@/lib/tones";
import type { Attachment, Member, Message, Thread } from "@/lib/api/types";
import { api } from "@/lib/api/endpoints";
import { q, qk } from "@/lib/queries";
import { atLeast, useRole, useSession } from "@/lib/session";
import { isBlankHtml } from "@/lib/collab/html";
import { isDesignedEmail, type Sanitized, sanitizeEmailHtml, sanitizeProse } from "@/lib/sanitize";
import { bytes, clock, fullDate } from "@/lib/format";
import { cn } from "@/lib/cn";
import { Avatar } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Spinner } from "@/components/ui/spinner";
import { Tooltip } from "@/components/ui/tooltip";
import { OllieAvatar, useAssistantName } from "../ollie";
import { AiActionCard, AiRating } from "./AgentReply";
import { ImproveAnswerButton } from "@/components/agent/quality";
import { AiAuthorLabel, TranslateButton, TranslatedNote, useMessageTranslation } from "@/components/ai/ticket/MessageAi";
import { BotRefFace } from "@/components/bots/BotFace";
import { EmailFrame } from "./EmailFrame";
import { SentEmail } from "./SentEmail";
import { RichEditor } from "@/components/editor/RichEditor";
import { mentionExtension, useSuggest } from "./suggestions";
import { useConfirm } from "@/components/ui/confirm";
import { Menu, MenuContent, MenuItem, MenuTrigger } from "@/components/ui/menu";
import { toast } from "@/components/ui/toast";
import { useNow } from "@/lib/useNow";
import { asNote, release, undo, useHeld } from "./outbox";
import { showInTimeline } from "./Timeline";

function authorName(m: Message): string {
  if (m.author.user) return m.author.user.name;
  if (m.author.contact) return m.author.contact.name || m.author.contact.email || m.from_email;
  if (m.author.type === "system") return "opendesk";
  if (m.author.type === "api") return m.from_name || "API";
  return m.from_name || m.from_email || "Unknown";
}

type SanitizedBody = Sanitized & { designed: boolean };

/**
 * A message body, sanitized once. An inbound email holds back images from other sites
 * until the reader loads them; a designed one renders in its own frame.
 */
function useBody(html: string, inbound: boolean, designable: boolean, remote: boolean): SanitizedBody {
  return useMemo(() => {
    const opts = { blockRemoteImages: inbound && !remote };
    if (designable && isDesignedEmail(html)) return { designed: true, ...sanitizeEmailHtml(html, opts) };
    return { designed: false, ...sanitizeProse(html, opts) };
  }, [html, inbound, designable, remote]);
}

function Body({ body, className }: { body: SanitizedBody; className?: string }) {
  const { t } = useTranslation("inbox");
  if (body.designed) return <EmailFrame html={body.html} title={t("message.emailContent")} className={className} />;
  return <div className={cn("prose-od ph-no-capture", className)} data-ph-mask dangerouslySetInnerHTML={{ __html: body.html }} />;
}

/** Shown while an email's images from other sites are held back, so opening it tells the sender nothing. */
function HeldImages({ onLoad }: { onLoad: () => void }) {
  const { t } = useTranslation("inbox");
  return (
    <div className="mb-2.5 flex items-center gap-2 rounded-md bg-sunken py-1 pl-2.5 pr-1 text-[11.5px] text-fg-3">
      <ImageOff className="size-3.5 shrink-0 text-fg-4" />
      <span className="min-w-0 flex-1">{t("message.imagesHeld")}</span>
      <Button size="xs" variant="secondary" className="shrink-0" onClick={onLoad}>
        {t("message.loadImages")}
      </Button>
    </div>
  );
}

function Original({ id, inbound, remote, onLoadImages }: { id: string; inbound: boolean; remote: boolean; onLoadImages: () => void }) {
  const { t } = useTranslation("inbox");
  const { data, isPending, isError } = useQuery({ queryKey: qk.of("message-original", id), queryFn: () => api.messages.original(id), staleTime: Infinity });
  const body = useBody(data?.full_html ?? "", inbound, inbound, remote);
  if (isPending)
    return (
      <div className="mt-3 flex items-center gap-2 text-xs text-fg-3">
        <Spinner className="size-3" /> {t("message.loadingOriginal")}
      </div>
    );
  if (isError) return <p className="mt-3 text-xs text-fg-3">{t("message.originalMissing")}</p>;
  return (
    <div className="mt-3">
      {body.held > 0 && <HeldImages onLoad={onLoadImages} />}
      <Body body={body} className={body.designed ? undefined : "border-l-2 border-line pl-3 text-[13px] text-fg-2"} />
    </div>
  );
}

function isImage(a: Attachment) {
  return a.content_type.startsWith("image/");
}

function isAudio(a: Attachment) {
  return a.content_type.startsWith("audio/");
}

export function Attachments({ items }: { items: Attachment[] }) {
  const { t } = useTranslation("inbox");
  const [preview, setPreview] = useState<Attachment | null>(null);
  const visible = items.filter((a) => !a.inline);
  if (!visible.length) return null;
  const images = visible.filter(isImage);
  const audio = visible.filter(isAudio);
  const files = visible.filter((a) => !isImage(a) && !isAudio(a));
  return (
    <div className="mt-3 flex flex-col gap-2">
      {images.length > 0 && (
        <div className="flex flex-wrap gap-2">
          {images.map((a) => (
            <button
              key={a.id}
              type="button"
              onClick={() => setPreview(a)}
              className="group relative h-[88px] w-[124px] overflow-hidden rounded-md border border-line bg-sunken"
              aria-label={t("message.preview", { name: a.filename })}
            >
              <img src={a.url} alt="" loading="lazy" className="ph-no-capture size-full object-cover transition-transform duration-200 group-hover:scale-[1.03]" />
              <span className="absolute inset-x-0 bottom-0 truncate bg-gradient-to-t from-black/55 to-transparent px-1.5 pb-1 pt-4 text-left text-[10.5px] text-white">
                {a.filename}
              </span>
            </button>
          ))}
        </div>
      )}
      {audio.map((a) => (
        <div key={a.id} className="flex max-w-[360px] items-center gap-2">
          <audio controls preload="metadata" src={a.url} aria-label={a.filename} className="ph-no-capture h-9 min-w-0 flex-1" />
          <a href={a.url} target="_blank" rel="noopener noreferrer" aria-label={t("message.download", { name: a.filename })} className="shrink-0 text-fg-4 hover:text-fg">
            <Download className="size-3.5" />
          </a>
        </div>
      ))}
      {files.length > 0 && (
        <div className="flex flex-wrap gap-2">
          {files.map((a) => (
            <a
              key={a.id}
              href={a.url}
              target="_blank"
              rel="noopener noreferrer"
              className="group flex h-10 max-w-[260px] items-center gap-2.5 rounded-md border border-line bg-surface pl-2.5 pr-3 hover:border-line-strong"
            >
              <FileText className="size-4 shrink-0 text-fg-3" />
              <span className="min-w-0">
                <span className="block truncate text-[12.5px] text-fg">{a.filename}</span>
                <span className="mono block text-[10.5px] text-fg-4">{bytes(a.size_bytes)}</span>
              </span>
              <Download className="ml-1 size-3.5 shrink-0 text-fg-4 group-hover:text-fg" />
            </a>
          ))}
        </div>
      )}
      <Dialog open={!!preview} onOpenChange={(o) => !o && setPreview(null)} title={preview?.filename ?? ""} size="xl">
        {preview && (
          <div className="flex flex-col items-center gap-3 pb-2">
            <img src={preview.url} alt={preview.filename} className="ph-no-capture max-h-[60vh] max-w-full rounded-md border border-line" />
            <a href={preview.url} target="_blank" rel="noopener noreferrer" className="text-xs text-fg-3 underline underline-offset-2 hover:text-fg">
              {t("message.fullSize")}
            </a>
          </div>
        )}
      </Dialog>
    </div>
  );
}

/** A note sent as a reply: it stays where it was written, pointing at the reply below. What it
 * said is kept for admins, who can read it (the reply may have been edited before it went). */
function SentAsReply({ m, name, admin }: { m: Message; name: string; admin: boolean }) {
  const { t } = useTranslation("inbox");
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const show = () => {
    // A marker that arrived live carries no text; an admin's copy is a refetch away.
    if (!open && !m.body_text && !m.body_html) void qc.invalidateQueries({ queryKey: qk.messages(m.ticket_id), exact: true });
    setOpen(!open);
  };
  return (
    <article id={`m-${m.id}`} aria-label={t("message.sentAsReplyLabel", { name })} className="rounded-lg border border-dashed border-note-line bg-note px-4 py-2">
      <div className="flex items-center gap-2 text-[12px] text-fg-3">
        <Lock className="size-3.5 shrink-0 text-fg-4" />
        <span className="min-w-0 flex-1">{t("message.sentAsReply", { name })}</span>
        <Tooltip content={fullDate(m.created_at)}>
          <time dateTime={m.created_at} className="mono shrink-0 text-[11px] text-fg-4">
            {clock(m.created_at)}
          </time>
        </Tooltip>
        {admin && !m.purged_at && (
          <Button size="xs" variant="ghost" onClick={show} aria-expanded={open}>
            {open ? t("message.hideNote") : t("message.showNote")}
          </Button>
        )}
        <Button size="xs" variant="ghost" iconRight={<ArrowDown className="size-3" />} onClick={() => showInTimeline(m.sent_as_reply_id!)}>
          {t("message.showReply")}
        </Button>
      </div>
      {admin && open && !m.purged_at && (
        <p className="mt-2 whitespace-pre-wrap border-t border-note-line pt-2 text-[13px] text-fg-2 animate-fade-in motion-reduce:animate-none">
          {m.body_text || t("message.loadingDeleted")}
        </p>
      )}
    </article>
  );
}

/** A deleted message: a marker saying who deleted it. An admin can read it and bring it back. */
function DeletedMessage({ m, name, admin }: { m: Message; name: string; admin: boolean }) {
  const { t } = useTranslation("inbox");
  const qc = useQueryClient();
  const confirm = useConfirm();
  const [open, setOpen] = useState(false);
  const purged = !!m.purged_at;
  const purge = () =>
    confirm.show(
      t("message.purgeConfirm"),
      async () => {
        const gone = await api.tickets.purgeMessage(m.ticket_id, m.id);
        qc.setQueryData<Message[]>(qk.messages(m.ticket_id), (old) => old?.map((x) => (x.id === gone.id ? gone : x)));
        void qc.invalidateQueries({ queryKey: qk.activities(m.ticket_id) });
        setOpen(false);
      },
      { title: t("message.purgeTitle"), confirmLabel: t("message.purgeLabel"), danger: true },
    );
  const restore = useMutation({
    mutationFn: () => api.tickets.restoreMessage(m.ticket_id, m.id),
    onSuccess: (back) => {
      qc.setQueryData<Message[]>(qk.messages(m.ticket_id), (old) => old?.map((x) => (x.id === back.id ? back : x)));
      void qc.invalidateQueries({ queryKey: qk.activities(m.ticket_id) });
      toast.success(t("message.restored"));
    },
    onError: (e) => toast.error(e),
  });
  const who = m.deleted_by?.name ?? t("message.someone");
  const own = !!m.deleted_by && m.deleted_by.id === m.author.user?.id;
  const show = () => {
    // A marker that arrived live carries no text or files; an admin's copy is a refetch away.
    if (!open && !m.body_text && !m.body_html && !m.attachments.length) void qc.invalidateQueries({ queryKey: qk.messages(m.ticket_id), exact: true });
    setOpen(!open);
  };
  return (
    <article id={`m-${m.id}`} aria-label={t("message.deletedLabel", { name })} className="rounded-lg border border-dashed border-line px-4 py-2.5">
      <div className="flex items-center gap-2 text-[12px] text-fg-3">
        <Trash2 className="size-3.5 shrink-0 text-fg-4" />
        <span className="min-w-0 flex-1">
          {own
            ? t(m.kind === "note" ? "message.deletedOwnNote" : "message.deletedOwnMessage", { who })
            : t(m.kind === "note" ? "message.deletedNote" : "message.deletedMessage", { who, name })}
        </span>
        {m.deleted_at && (
          <Tooltip content={fullDate(m.deleted_at)}>
            <time dateTime={m.deleted_at} className="mono shrink-0 text-[11px] text-fg-4">
              {clock(m.deleted_at)}
            </time>
          </Tooltip>
        )}
        {purged && <span className="shrink-0 text-[11px] text-fg-4">{t("message.purged")}</span>}
        {admin && !purged && (
          <>
            <Button size="xs" variant="ghost" onClick={show} aria-expanded={open}>
              {open ? t("message.hideDeleted") : t("message.showDeleted")}
            </Button>
            <Tooltip content={t("message.purgeHint")}>
              <Button size="xs" variant="ghost" square aria-label={t("message.purgeLabel")} icon={<Trash2 className={cn("size-3", tone.red.text)} />} onClick={purge} />
            </Tooltip>
            <Button size="xs" variant="secondary" icon={<RotateCcw className="size-3" />} disabled={restore.isPending} onClick={() => restore.mutate()}>
              {t("message.restore")}
            </Button>
          </>
        )}
      </div>
      {admin && open && !purged && (
        <div className="mt-2 border-t border-line-soft pt-2 animate-fade-in motion-reduce:animate-none">
          {(m.body_text || !m.attachments.length) && (
            <p className="whitespace-pre-wrap text-[13px] text-fg-2">{m.body_text || t("message.loadingDeleted")}</p>
          )}
          {/* Its files open for admins alone, through links signed for the deleted message. */}
          {m.attachments.length > 0 && <Attachments items={m.attachments} />}
        </div>
      )}
    </article>
  );
}

/** A reply on hold for the undo window: when it goes out, and the ways out of sending it. */
function HeldDelivery({ id }: { id: string }) {
  const { t } = useTranslation("inbox");
  const at = useHeld((s) => s[id]);
  const now = useNow(!!at);
  if (!at) return null;
  const left = Math.max(1, Math.ceil((at - now) / 1000));
  return (
    <span className="flex items-center gap-0.5">
      <span className="mono mr-1 text-[11px] text-fg-4 tabular-nums">{t("outbox.sendingIn", { n: left })}</span>
      <Tooltip content={t("outbox.asNote")}>
        <Button size="xs" variant="ghost" square aria-label={t("outbox.asNote")} icon={<Lock className="size-3" />} onClick={() => asNote(id)} />
      </Tooltip>
      <Tooltip content={t("outbox.undo")}>
        <Button size="xs" variant="ghost" square aria-label={t("outbox.undo")} icon={<Undo2 className="size-3" />} onClick={() => undo(id)} />
      </Tooltip>
      <Tooltip content={t("outbox.sendNow")}>
        <Button size="xs" variant="ghost" square aria-label={t("outbox.sendNow")} icon={<Send className="size-3" />} onClick={() => release(id)} />
      </Tooltip>
    </span>
  );
}

function Delivery({ m, onRetry }: { m: Message; onRetry?: () => void }) {
  const { t } = useTranslation("inbox");
  // A thread message is a note that is emailed to the third party, so it reports delivery.
  if ((m.kind === "note" && !m.thread_id) || m.direction === "inbound" || m.delivery_status === "none") return null;
  if (m.delivery_status === "scheduled") return <HeldDelivery id={m.id} />;
  if (m.delivery_status === "pending")
    return (
      <span className="flex items-center gap-1 text-[11px] text-fg-4">
        <Spinner className="size-3" /> {t("message.sending")}
      </span>
    );
  if (m.delivery_status === "sent")
    return (
      <Tooltip content={t("message.deliveredTo", { to: m.to.join(", ") || t("composer.theCustomer") })}>
        <span className="flex items-center gap-1 text-[11px] text-fg-4 animate-fade-in">
          <Check className={`size-3 ${tone.green.text}`} /> {t("message.sent")}
        </span>
      </Tooltip>
    );
  return (
    <span className="flex items-center gap-2">
      <Tooltip content={m.delivery_error || t("message.refused")}>
        <span className={`inline-flex h-5 items-center gap-1.5 rounded-sm px-1.5 text-[11px] font-medium ${tone.red.chip}`}>
          <span className={`size-1.5 rounded-full ${tone.red.solid}`} />
          {m.delivery_status === "bounced" ? t("message.bounced") : t("message.notDelivered")}
        </span>
      </Tooltip>
      {onRetry && (
        <Button size="xs" variant="secondary" icon={<RotateCw className="size-3" />} onClick={onRetry}>
          {t("error.retry")}
        </Button>
      )}
    </span>
  );
}

/** An inbound email whose From failed its domain's authentication check. */
function UnverifiedSender() {
  const { t } = useTranslation("inbox");
  return (
    <Tooltip content={<span className="block max-w-[260px] font-normal leading-snug">{t("message.unverifiedHint")}</span>}>
      <span
        tabIndex={0}
        className={`inline-flex h-[18px] shrink-0 items-center gap-1 self-center rounded-sm px-1.5 text-[11px] font-medium outline-none focus-visible:shadow-[0_0_0_2px_var(--focus)] ${tone.amber.chip}`}
      >
        <ShieldAlert className="size-3" />
        {t("message.unverified")}
      </span>
    </Tooltip>
  );
}

/** A reply sent from its author's own mail address (contracts/collab.md "Answering tickets"). */
function OwnAddress() {
  const { t } = useTranslation("inbox");
  return (
    <Tooltip content={<span className="block max-w-[260px] font-normal leading-snug">{t("message.ownAddressHint")}</span>}>
      <span
        tabIndex={0}
        aria-label={t("message.ownAddress")}
        className="inline-flex h-[18px] min-w-[18px] shrink items-center gap-1 self-center overflow-hidden rounded-sm bg-hover px-1 text-[11px] font-medium text-fg-2 outline-none focus-visible:shadow-[0_0_0_2px_var(--focus)]"
      >
        <Mail className="size-3 shrink-0" />
        <span className="truncate">{t("message.ownAddress")}</span>
      </span>
    </Tooltip>
  );
}

/** Which of our addresses a message went through, when the workspace has more than one. */
function useMessageAddress(m: Message, receivedAt?: string | null) {
  const { data: mailboxes } = useQuery(q.mailboxes());
  const multi = (mailboxes ?? []).reduce((n, x) => n + 1 + (x.aliases?.length ?? 0), 0) > 1;
  // A reply from a member's own address always says which one it went from.
  if (m.from_account) return m.from_email || m.from_account.email;
  if (!multi || receivedAt === null) return null;
  const mb = m.mailbox_id ? mailboxes?.find((x) => x.id === m.mailbox_id) : undefined;
  if (m.direction === "inbound") {
    if (m.received_at) return m.received_at;
    const ours = (a: string) => !!mb && (a.toLowerCase() === mb.email.toLowerCase() || (mb.aliases ?? []).some((x) => x.toLowerCase() === a.toLowerCase()));
    return m.to.find(ours) ?? mb?.email ?? (receivedAt || null);
  }
  return m.from_email || mb?.email || null;
}

/** What can be done to a thread from its latest message. */
export type ThreadActions = { reply: (th: Thread) => void; setStatus: (th: Thread, status: Thread["status"]) => void };

/** The side conversation a message belongs to, above its body, and on its latest message its actions. */
function ThreadBar({ thread, actions, first }: { thread: Thread; actions?: ThreadActions; first?: boolean }) {
  const { t } = useTranslation("inbox");
  const closed = thread.status === "closed";
  return (
    <div className="mb-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11.5px] text-fg-3">
      <Tooltip content={<span className="block max-w-[260px] font-normal leading-snug">{t("message.hiddenFromCustomer")}</span>}>
        <span tabIndex={0} className="flex min-w-0 items-center gap-1.5 rounded-sm outline-none focus-visible:shadow-[0_0_0_2px_var(--focus)]">
          <MessagesSquare className={cn("size-3 shrink-0", tone.cyan.text)} />
          <span className="min-w-0 truncate">
            <Trans t={t} i18nKey="message.inThread" values={{ subject: thread.subject }} components={{ b: <span className="font-medium text-fg-2" /> }} />
          </span>
        </span>
      </Tooltip>
      {first && thread.include_history && (
        <span className="flex shrink-0 items-center gap-1 rounded-sm bg-hover px-1.5 text-[10.5px] text-fg-3">
          <Quote className="size-2.5" />
          {t("side.withHistory")}
        </span>
      )}
      {closed && <span className="shrink-0 rounded-sm bg-hover px-1.5 text-[10.5px] text-fg-3">{t("message.threadClosed")}</span>}
      {actions && (
        <span className="ml-auto flex items-center gap-1">
          <Button size="xs" variant="ghost" icon={<Reply className="size-3" />} onClick={() => actions.reply(thread)}>
            {t("message.replyInThread")}
          </Button>
          <Button
            size="xs"
            variant="ghost"
            icon={closed ? <RotateCcw className="size-3" /> : <CheckCheck className="size-3" />}
            onClick={() => actions.setStatus(thread, closed ? "open" : "closed")}
          >
            {closed ? t("message.reopenThread") : t("message.closeThread")}
          </Button>
        </span>
      )}
    </div>
  );
}

/** Edits a note in place, with the composer's @mentions; Escape or Cancel leaves it as it was. */
function NoteEditor({ m, onDone }: { m: Message; onDone: () => void }) {
  const { t } = useTranslation("inbox");
  const qc = useQueryClient();
  const [html, setHtml] = useState(m.body_html);
  const { data: members } = useQuery(q.members());
  const membersRef = useRef<Member[]>([]);
  membersRef.current = members ?? [];
  // The composer's SuggestionPopup shows the list; it is on the page whenever a note can be edited.
  const extensions = useMemo(() => [mentionExtension(() => membersRef.current)], []);
  const empty = isBlankHtml(html) && m.attachments.length === 0;
  const save = useMutation({
    mutationFn: (body_html: string) => api.tickets.editMessage(m.ticket_id, m.id, { body_html }),
    onSuccess: (updated) => {
      qc.setQueryData<Message[]>(qk.messages(m.ticket_id), (old) => old?.map((item) => (item.id === updated.id ? updated : item)));
      qc.invalidateQueries({ queryKey: qk.activities(m.ticket_id) });
      onDone();
    },
    onError: (error) => toast.error(error),
  });
  return (
    <div className="rounded-md border border-line bg-surface">
      <RichEditor
        value={html}
        onChange={setHtml}
        autofocus
        editable={!save.isPending}
        toolbar="none"
        extensions={extensions}
        contentClassName="min-h-[48px] text-[13px]"
        ariaLabel={t("message.edit")}
        onSubmit={() => !empty && save.mutate(html)}
        onKeyDown={(e) => {
          // Escape first closes an open @mention list.
          if (e.key !== "Escape" || useSuggest.getState().open) return false;
          onDone();
          return true;
        }}
      />
      <div className="flex justify-end gap-1.5 px-2 pb-2">
        <Button size="xs" variant="ghost" onClick={onDone}>
          {t("message.cancel")}
        </Button>
        <Button size="xs" variant="primary" loading={save.isPending} disabled={empty} onClick={() => save.mutate(html)}>
          {t("message.save")}
        </Button>
      </div>
    </div>
  );
}

export const MessageItem = memo(function MessageItem({
  m,
  onRetry,
  onToReply,
  onRetract,
  highlight,
  receivedAt,
  thread,
  threadActions,
  threadFirst,
}: {
  m: Message;
  onRetry?: (m: Message) => void;
  /** Puts a note in the reply box, to go to the customer instead (it is removed once sent). */
  onToReply?: (m: Message) => void;
  /** Takes a sent chat reply back from the customer, as an internal note. */
  onRetract?: (m: Message) => void;
  highlight?: boolean;
  /** The ticket's first address, for inbound messages that don't record one; null when the ticket isn't email. */
  receivedAt?: string | null;
  /** The third-party thread the message belongs to. */
  thread?: Thread;
  /** Set on a thread's latest message. */
  threadActions?: ThreadActions;
  /** The thread's first message. */
  threadFirst?: boolean;
}) {
  const [showOriginal, setShowOriginal] = useState(false);
  const [viewingSent, setViewingSent] = useState(false);
  const ollie = useAssistantName();
  // An AI reply shows the bot it was sent as (contracts/bots.md), else Ollie.
  const bot = m.ai ? (m.author.bot ?? null) : null;
  const name = m.ai ? (bot?.name ?? ollie) : authorName(m);
  // A thread message is stored as a note, but it is email to or from someone outside the
  // team: it reads like one, and an answer from them is untrusted inbound mail.
  const inThread = !!m.thread_id;
  const note = m.kind === "note" && !inThread;
  const outbound = m.direction === "outbound" && !note;
  const failed = m.delivery_status === "failed" || m.delivery_status === "bounced";
  const avatar = m.author.user?.avatar_url ?? m.author.contact?.avatar_url;
  const tr = useMessageTranslation(m);
  const { t } = useTranslation("inbox");
  const qc = useQueryClient();
  const confirm = useConfirm();
  const meUser = useSession((s) => s.user);
  const me = meUser?.id;
  const admin = atLeast(useRole(), "admin");
  const mine = !!me && m.author.user?.id === me;
  const editableNote = note && mine;
  // Who may delete what is the workspace's rule for agents (settings.message_delete); admins any message.
  const { data: ws } = useQuery(q.workspace());
  const { data: members } = useQuery(q.members());
  // An agent's own rule, set by an admin, wins over the workspace's.
  const rule = members?.find((x) => x.user.id === me)?.message_delete ?? ws?.settings.message_delete ?? "own";
  // "own" (the default) is an agent's own internal notes; "all" any agent's notes and replies.
  const deletable = !inThread && (admin || (m.author.type === "user" && (rule === "all" || (rule === "own" && mine && note))));
  // Only a chat reply that was not emailed as a follow-up (delivery "none") can be taken back.
  const retractable = !!onRetract && outbound && m.author.type === "user" && !m.ai && (mine || admin) && m.delivery_status === "none";
  const [editing, setEditing] = useState(false);
  const remove = () =>
    confirm.show(
      t(note ? "message.deleteConfirm" : m.direction === "inbound" ? "message.deleteInboundConfirm" : "message.deleteReplyConfirm"),
      async () => {
        await api.tickets.deleteMessage(m.ticket_id, m.id);
        const by = meUser ? { id: meUser.id, name: meUser.name, email: meUser.email, avatar_url: meUser.avatar_url } : null;
        qc.setQueryData<Message[]>(qk.messages(m.ticket_id), (old) =>
          old?.map((item) =>
            item.id === m.id
              ? { ...item, deleted_at: new Date().toISOString(), deleted_by: by, ...(admin ? {} : { body_html: "", body_text: "", attachments: [] }) }
              : item,
          ),
        );
        qc.invalidateQueries({ queryKey: qk.activities(m.ticket_id) });
        // An admin's copy comes back with its files' links signed for the deleted message.
        if (admin) void qc.invalidateQueries({ queryKey: qk.messages(m.ticket_id), exact: true });
      },
      { title: t(note ? "message.deleteTitle" : "message.deleteMessageTitle"), confirmLabel: t("message.deleteLabel"), danger: true },
    );
  const address = useMessageAddress(m, receivedAt);
  const inbound = m.direction === "inbound" && !note;
  const [remote, setRemote] = useState(false);
  const translated = !!(tr.showing && tr.html);
  const body = useBody(translated ? tr.html! : m.body_html || `<p>${m.body_text}</p>`, inbound, inbound && !translated, remote);
  const replyTo = inbound && m.reply_to && m.reply_to.toLowerCase() !== m.from_email.toLowerCase() ? m.reply_to : null;

  // A note sent as a reply is hidden like a deleted message, and says where its words went.
  if (m.sent_as_reply_id) return <SentAsReply m={m} name={name} admin={admin} />;
  if (m.deleted_at) return <DeletedMessage m={m} name={name} admin={admin} />;

  return (
    <article
      id={`m-${m.id}`}
      aria-label={t(note ? "message.noteFrom" : inThread ? "message.threadFrom" : "message.from", { name })}
      className={cn(
        "group/msg relative rounded-lg border transition-shadow duration-300",
        note
          ? "border-dashed border-note-line bg-note"
          : failed
            ? cn(tone.red.line, "bg-surface")
            : inThread
              ? cn(tone.cyan.line, "bg-surface")
              : "border-line bg-surface",
        highlight && "shadow-[0_0_0_3px_var(--focus)]",
      )}
    >
      <header className="flex items-center gap-2.5 px-4 pt-3">
        {bot ? <BotRefFace bot={bot} size={24} /> : m.ai ? <OllieAvatar size={24} /> : <Avatar name={name} src={avatar} size="md" />}
        <div className="flex min-w-0 flex-1 items-baseline gap-2">
          <span className="max-w-[50%] shrink-0 truncate text-[13px] font-medium" data-ph-mask={m.author.contact ? true : undefined}>
            {name}
          </span>
          <AiAuthorLabel m={m} />
          {inbound && m.auth_failed && <UnverifiedSender />}
          {outbound && m.from_account && <OwnAddress />}
          {note ? (
            <span className="flex shrink-0 items-center gap-1 text-[11.5px] text-fg-3">
              <Lock className="size-3" /> {t("message.internalNote")}
            </span>
          ) : outbound ? (
            <span className="min-w-0 truncate text-[11.5px] text-fg-3" data-ph-mask>
              {address && (
                <span className="text-fg-2">
                  {t("message.fromAddress", { name: m.from_name || name, address })}
                  <span className="text-fg-4"> · </span>
                </span>
              )}
              {t("message.to", { to: m.to.join(", ") || t("composer.theCustomer") })}
              {m.cc.length > 0 && `, cc ${m.cc.join(", ")}`}
            </span>
          ) : (
            <span className="min-w-0 truncate text-[11.5px] text-fg-3" data-ph-mask>
              {m.from_email}
              {address && <span className="text-fg-4"> {t("message.toAddress", { address })}</span>}
            </span>
          )}
        </div>
        {m.ai && <ImproveAnswerButton m={m} />}
        {m.ai && m.ai_rating && <AiRating rating={m.ai_rating} />}
        <TranslateButton tr={tr} />
        <Delivery m={m} onRetry={onRetry ? () => onRetry(m) : undefined} />
        <Tooltip content={fullDate(m.created_at)}>
          <time dateTime={m.created_at} className="mono shrink-0 text-[11px] text-fg-4">
            {clock(m.created_at)}
          </time>
        </Tooltip>
        {note && m.edited_at && <span className="shrink-0 text-[10.5px] text-fg-4">{t("message.edited")}</span>}
        {(deletable || retractable || (editableNote && onToReply)) && !editing && !m.id.startsWith("tmp-") && (
          <Menu>
            <MenuTrigger asChild>
              <button
                type="button"
                aria-label={t(note ? "message.menu" : "message.replyMenu")}
                className="ml-auto flex size-6 shrink-0 items-center justify-center rounded-md text-fg-4 opacity-0 transition-opacity hover:bg-hover hover:text-fg focus-visible:opacity-100 group-hover/msg:opacity-100 data-[state=open]:opacity-100"
              >
                <MoreHorizontal className="size-3.5" />
              </button>
            </MenuTrigger>
            <MenuContent>
              {/* Its files belong to the note, so only a note without any becomes a reply. */}
              {editableNote && onToReply && m.attachments.length === 0 && (
                <MenuItem icon={<Reply />} onSelect={() => onToReply(m)}>
                  {t("message.toReply")}
                </MenuItem>
              )}
              {editableNote && (
                <MenuItem
                  icon={<Pencil />}
                  onSelect={() => setEditing(true)}
                >
                  {t("message.edit")}
                </MenuItem>
              )}
              {retractable && (
                <MenuItem icon={<Lock />} onSelect={() => onRetract(m)}>
                  {t("message.retract")}
                </MenuItem>
              )}
              {deletable && (
                <MenuItem icon={<Trash2 />} danger onSelect={remove}>
                  {t(note ? "message.delete" : "message.deleteMessage")}
                </MenuItem>
              )}
            </MenuContent>
          </Menu>
        )}
      </header>
      <div className="px-4 pb-3.5 pl-[50px] pt-1">
        {inThread && thread && <ThreadBar thread={thread} actions={threadActions} first={threadFirst} />}
        {(replyTo || (inbound && m.form_sender)) && (
          <div className="mb-1.5 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[11.5px] text-fg-3" data-ph-mask>
            {inbound && m.form_sender && (
              <span className="flex min-w-0 items-center gap-1.5">
                <Globe className="size-3 shrink-0 text-fg-4" />
                <span className="min-w-0 truncate">
                  <Trans t={t} i18nKey="message.viaForm" values={{ address: m.form_sender }} components={{ m: <span className="mono text-fg-2" /> }} />
                </span>
              </span>
            )}
            {replyTo && (
              <Tooltip content={t("message.replyToHint")}>
                <span className="flex min-w-0 items-center gap-1.5">
                  <Reply className="size-3 shrink-0 text-fg-4" />
                  <span className="min-w-0 truncate">
                    <Trans t={t} i18nKey="message.replyTo" values={{ address: replyTo }} components={{ m: <span className="mono text-fg-2" /> }} />
                  </span>
                </span>
              </Tooltip>
            )}
          </div>
        )}
        <TranslatedNote tr={tr} />
        {body.held > 0 && <HeldImages onLoad={() => setRemote(true)} />}
        {editing ? <NoteEditor m={m} onDone={() => setEditing(false)} /> : <Body body={body} />}
        {/* An email we sent keeps the copy that went out: it opens as its recipients got it. */}
        {outbound && m.has_quoted && (
          <>
            <button
              type="button"
              onClick={() => setViewingSent(true)}
              className="mt-2 inline-flex h-6 items-center gap-1.5 rounded-md px-1.5 -ml-1.5 text-[11.5px] text-fg-3 outline-none transition-colors hover:bg-hover hover:text-fg focus-visible:shadow-[0_0_0_2px_var(--focus)]"
            >
              <Mail className="size-3.5 text-fg-4" />
              {t("message.viewSent")}
            </button>
            <SentEmail m={m} open={viewingSent} onOpenChange={setViewingSent} />
          </>
        )}
        {!outbound && m.has_quoted && (
          <div>
            <Tooltip content={showOriginal ? t("message.hideQuoted") : t("message.showOriginal")}>
              <button
                type="button"
                aria-expanded={showOriginal}
                onClick={() => setShowOriginal((v) => !v)}
                className="mt-2 inline-flex h-4 items-center rounded-sm bg-hover px-1.5 text-fg-3 transition-colors hover:bg-active hover:text-fg"
                aria-label={showOriginal ? t("message.hideQuoted") : t("message.showOriginal")}
              >
                <MoreHorizontal className="size-3.5" />
              </button>
            </Tooltip>
            {showOriginal && <Original id={m.id} inbound={inbound} remote={remote} onLoadImages={() => setRemote(true)} />}
          </div>
        )}
        <Attachments items={m.attachments} />
        {m.ai_action && <AiActionCard action={m.ai_action} />}
        {failed && m.delivery_error && (
          <p className="mt-3 flex items-start gap-1.5 rounded-md bg-sunken px-2.5 py-2 text-xs text-fg-2">
            <AlertCircle className={`mt-px size-3.5 shrink-0 ${tone.red.text}`} />
            <span className="mono">{m.delivery_error}</span>
          </p>
        )}
      </div>
    </article>
  );
});
