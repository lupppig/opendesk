import { useQuery } from "@tanstack/react-query";
import { AlertCircle, Check, CheckCheck, Download, FileText, Globe, ImageOff, Lock, MessagesSquare, MoreHorizontal, Quote, Reply, RotateCcw, RotateCw, ShieldAlert } from "lucide-react";
import { memo, useMemo, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { tone } from "@/lib/tones";
import type { Attachment, Message, Thread } from "@/lib/api/types";
import { api } from "@/lib/api/endpoints";
import { q } from "@/lib/queries";
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
  const { data, isPending, isError } = useQuery({ queryKey: ["message-original", id], queryFn: () => api.messages.original(id), staleTime: Infinity });
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

export function Attachments({ items }: { items: Attachment[] }) {
  const { t } = useTranslation("inbox");
  const [preview, setPreview] = useState<Attachment | null>(null);
  const visible = items.filter((a) => !a.inline);
  if (!visible.length) return null;
  const images = visible.filter(isImage);
  const files = visible.filter((a) => !isImage(a));
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

function Delivery({ m, onRetry }: { m: Message; onRetry?: () => void }) {
  const { t } = useTranslation("inbox");
  // A thread message is a note that is emailed to the third party, so it reports delivery.
  if ((m.kind === "note" && !m.thread_id) || m.direction === "inbound" || m.delivery_status === "none") return null;
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

/** Which of our addresses a message went through, when the workspace has more than one. */
function useMessageAddress(m: Message, receivedAt?: string | null) {
  const { data: mailboxes } = useQuery(q.mailboxes());
  const multi = (mailboxes ?? []).reduce((n, x) => n + 1 + (x.aliases?.length ?? 0), 0) > 1;
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

export const MessageItem = memo(function MessageItem({
  m,
  onRetry,
  highlight,
  receivedAt,
  thread,
  threadActions,
  threadFirst,
}: {
  m: Message;
  onRetry?: (m: Message) => void;
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
  const address = useMessageAddress(m, receivedAt);
  const inbound = m.direction === "inbound" && !note;
  const [remote, setRemote] = useState(false);
  const translated = !!(tr.showing && tr.html);
  const body = useBody(translated ? tr.html! : m.body_html || `<p>${m.body_text}</p>`, inbound, inbound && !translated, remote);
  const replyTo = inbound && m.reply_to && m.reply_to.toLowerCase() !== m.from_email.toLowerCase() ? m.reply_to : null;

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
        <Body body={body} />
        {m.has_quoted && (
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
