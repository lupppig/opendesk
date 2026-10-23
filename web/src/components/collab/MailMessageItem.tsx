// One message of a conversation: collapsed to a line (from, snippet, time) or open
// with its people, body and files. The body goes through the app's one safe email
// renderer: sanitized again here, a designed email in its sandboxed frame without
// scripts, and images from other sites (CSS url() too) held back until asked for.

import { ChevronDown, Download, FileText, ImageOff, MoreHorizontal, Paperclip } from "lucide-react";
import { memo, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { MailAddress, MailAttachment, MailMessage } from "@/lib/api/types";
import { formatAddress, normEmail } from "@/lib/collab/recipients";
import { bytes, clock, fullDate, relShort } from "@/lib/format";
import { cn } from "@/lib/cn";
import { isDesignedEmail, sanitizeEmailHtml, sanitizeProse } from "@/lib/sanitize";
import { escapeHtml } from "@/lib/collab/html";
import { isForward, splitQuotedHtml, splitQuotedText } from "@/lib/collab/quote";
import { Avatar } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Tooltip } from "@/components/ui/tooltip";
import { EmailFrame } from "@/components/tickets/detail/EmailFrame";
import { DetailsTrigger, SenderWarning, useMessageDetails, Via } from "./MailDetails";

type Body = { designed: boolean; html: string; held: number };

const textHtml = (text: string) => `<p>${escapeHtml(text).replace(/\n/g, "<br>")}</p>`;

function sanitizeBody(html: string, external: boolean, remote: boolean): Body {
  const opts = { blockRemoteImages: external && !remote };
  if (isDesignedEmail(html)) return { designed: true, ...sanitizeEmailHtml(html, opts) };
  return { designed: false, ...sanitizeProse(html, opts) };
}

/**
 * A message body, sanitized once, and the quoted history under it (the earlier mail the
 * sender's client copied in), shown when asked. Mail from others holds back remote images
 * until loaded. A forward keeps what it carries in the body.
 */
function useMailBody(m: MailMessage, external: boolean, remote: boolean): { body: Body; quoted: Body | null } {
  return useMemo(() => {
    const split = isForward(m.subject) ? null : m.body_html ? splitQuotedHtml(m.body_html) : splitQuotedText(m.body_text);
    const raw = (s: string) => (m.body_html ? s : textHtml(s));
    if (!split) return { body: sanitizeBody(m.body_html || textHtml(m.body_text), external, remote), quoted: null };
    return { body: sanitizeBody(raw(split.body), external, remote), quoted: sanitizeBody(raw(split.quoted), external, remote) };
  }, [m.body_html, m.body_text, m.subject, external, remote]);
}

function MailBody({ body, title }: { body: Body; title: string }) {
  return body.designed ? (
    <EmailFrame html={body.html} title={title} />
  ) : (
    <div className="prose-od ph-no-capture text-[13px]" data-ph-mask dangerouslySetInnerHTML={{ __html: body.html }} />
  );
}

function HeldImages({ onLoad }: { onLoad: () => void }) {
  const { t } = useTranslation("collab");
  return (
    <div className="mb-2.5 flex items-center gap-2 rounded-md bg-sunken py-1 pl-2.5 pr-1 text-[11.5px] text-fg-3">
      <ImageOff className="size-3.5 shrink-0 text-fg-4" />
      <span className="min-w-0 flex-1">{t("thread.imagesHeld")}</span>
      <Button size="xs" variant="secondary" className="shrink-0" onClick={onLoad}>
        {t("thread.loadImages")}
      </Button>
    </div>
  );
}

const isImage = (a: MailAttachment) => a.content_type.startsWith("image/");
/** An image attachment shown in place (`?inline=1` on the signed URL). */
const inlineUrl = (url: string) => (/^https?:|^\//i.test(url) ? `${url}${url.includes("?") ? "&" : "?"}inline=1` : url);

export function MailAttachments({ items }: { items: MailAttachment[] }) {
  const { t } = useTranslation("collab");
  const [preview, setPreview] = useState<MailAttachment | null>(null);
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
              className="group relative h-[88px] w-[124px] overflow-hidden rounded-md border border-line bg-sunken outline-none focus-visible:shadow-[0_0_0_2px_var(--focus)]"
              aria-label={t("thread.preview", { name: a.filename })}
            >
              <img src={inlineUrl(a.url)} alt="" loading="lazy" referrerPolicy="no-referrer" className="ph-no-capture size-full object-cover transition-transform duration-200 group-hover:scale-[1.03] motion-reduce:transition-none" />
              <span className="absolute inset-x-0 bottom-0 truncate bg-gradient-to-t from-black/55 to-transparent px-1.5 pb-1 pt-4 text-left text-[10.5px] text-white">{a.filename}</span>
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
              download={a.filename}
              rel="noopener noreferrer"
              aria-label={t("thread.download", { name: a.filename })}
              className="group flex h-10 max-w-[260px] items-center gap-2.5 rounded-md border border-line bg-surface pl-2.5 pr-3 hover:border-line-strong"
            >
              <FileText className="size-4 shrink-0 text-fg-3" />
              <span className="min-w-0">
                <span className="block truncate text-[12.5px] text-fg">{a.filename}</span>
                <span className="mono block text-[10.5px] text-fg-4">{bytes(a.size)}</span>
              </span>
              <Download className="ml-1 size-3.5 shrink-0 text-fg-4 group-hover:text-fg" />
            </a>
          ))}
        </div>
      )}
      <Dialog open={!!preview} onOpenChange={(o) => !o && setPreview(null)} title={preview?.filename ?? ""} size="xl">
        {preview && (
          <div className="flex flex-col items-center gap-3 pb-2">
            <img src={inlineUrl(preview.url)} alt={preview.filename} referrerPolicy="no-referrer" className="ph-no-capture max-h-[60vh] max-w-full rounded-md border border-line" />
            <a href={preview.url} download={preview.filename} className="text-xs text-fg-3 underline underline-offset-2 hover:text-fg">
              {t("thread.download", { name: preview.filename })}
            </a>
          </div>
        )}
      </Dialog>
    </div>
  );
}

const list = (xs: MailAddress[]) => xs.map(formatAddress).join(", ");

export const MailMessageItem = memo(function MailMessageItem({
  m,
  ownEmail,
  open,
  onToggle,
}: {
  m: MailMessage;
  /** The account's addresses: its own and its aliases. */
  ownEmail: string | readonly string[];
  open: boolean;
  onToggle: () => void;
}) {
  const { t } = useTranslation("collab");
  const [remote, setRemote] = useState(false);
  const [showQuoted, setShowQuoted] = useState(false);
  const external = !(typeof ownEmail === "string" ? [ownEmail] : ownEmail).some((e) => normEmail(e) === normEmail(m.from.email));
  const name = m.from.name || m.from.email;
  const { body, quoted } = useMailBody(m, external, remote);
  const held = body.held + (showQuoted && quoted ? quoted.held : 0);
  const files = m.attachments.filter((a) => !a.inline).length;
  const details = useMessageDetails(m, external, open);

  // Folding keeps your place: the folded row stays in view and takes focus, so the next
  // message is a keypress away. Open and folded share one first line (avatar, name, time
  // in the same spots), so nothing moves but what opens below it.
  const row = useRef<HTMLButtonElement>(null);
  const folding = useRef(false);
  const fold = () => {
    folding.current = true;
    onToggle();
  };
  useLayoutEffect(() => {
    if (open || !folding.current) return;
    folding.current = false;
    row.current?.scrollIntoView({ block: "nearest" });
    row.current?.focus({ preventScroll: true });
  }, [open]);

  if (!open)
    return (
      <button
        ref={row}
        type="button"
        onClick={onToggle}
        aria-expanded={false}
        aria-label={t("thread.expand", { name })}
        className="group flex w-full items-start gap-2.5 rounded-lg border border-line bg-surface px-4 py-3 text-left outline-none hover:bg-sunken/60 focus-visible:shadow-[0_0_0_2px_var(--focus)]"
      >
        <Avatar name={name} size="md" />
        <span className={cn("w-[160px] shrink-0 truncate text-[13px] leading-6", m.unread ? "font-semibold text-fg" : "font-medium text-fg-2")} data-ph-mask>
          {name}
        </span>
        {m.draft && <span className="shrink-0 text-[11px] font-medium leading-6 text-tone-red">{t("thread.draft")}</span>}
        <span className="min-w-0 flex-1 truncate text-[12.5px] leading-6 text-fg-4" data-ph-mask>
          {m.snippet}
        </span>
        {files > 0 && <Paperclip className="mt-1.5 size-3 shrink-0 text-fg-4" />}
        <time dateTime={m.sent_at} title={fullDate(m.sent_at)} className="mono shrink-0 text-[11px] leading-6 text-fg-4">
          {relShort(m.sent_at)} · {clock(m.sent_at)}
        </time>
        {/* The open header's fold button sits here; the time stays put. */}
        <span aria-hidden className="-mr-1.5 flex size-6 shrink-0 items-center justify-center text-fg-4 opacity-0 group-hover:opacity-100">
          <ChevronDown className="size-3.5 -rotate-90" />
        </span>
      </button>
    );

  return (
    <article aria-label={t("thread.expand", { name })} className="rounded-lg border border-line bg-surface">
      {/* A click on the header folds the message, as in a mail app. Only its text (data-text, the text cursor)
          selects, and a click there never folds; buttons and links keep their own clicks. A double click is one
          fold, never a selection. It stays at the top while a long message scrolls under it, so folding is
          always one click away. */}
      <header
        className="sticky top-0 z-[2] flex cursor-pointer select-none items-start gap-2.5 rounded-t-lg bg-surface px-4 pb-1 pt-3"
        onMouseDown={(e) => {
          if (e.detail > 1 && !(e.target as HTMLElement).closest("[data-text]")) e.preventDefault();
        }}
        onClick={(e) => {
          const el = e.target as HTMLElement;
          if (e.detail > 1 || !e.currentTarget.contains(el) || el.closest("button, a, input, [role=dialog], [data-text]")) return;
          fold();
        }}
      >
        <Avatar name={name} size="md" />
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-baseline gap-2 leading-6">
            <span data-text className="max-w-[50%] shrink-0 cursor-text select-text truncate text-[13px] font-medium" data-ph-mask>
              {name}
            </span>
            <span data-text className="mono min-w-0 cursor-text select-text truncate text-[11.5px] text-fg-3" data-ph-mask>
              {m.from.email}
            </span>
            <Via details={details.data} />
            {m.draft && <span className="shrink-0 text-[11px] font-medium text-tone-red">{t("thread.draft")}</span>}
          </div>
          <div className="flex flex-col items-start text-[11.5px] leading-[17px] text-fg-3" data-ph-mask>
            <DetailsTrigger m={m} details={details.data} loading={details.isLoading} />
            {m.reply_to.length > 0 && normEmail(m.reply_to[0].email) !== normEmail(m.from.email) && <span data-text className="max-w-full cursor-text select-text truncate">{t("thread.replyTo", { list: list(m.reply_to) })}</span>}
            {m.sent_by && <span data-text className="max-w-full cursor-text select-text truncate text-fg-4">{t("thread.sentBy", { name: m.sent_by.name })}</span>}
          </div>
        </div>
        <Tooltip content={fullDate(m.sent_at)}>
          <time dateTime={m.sent_at} className="mono shrink-0 text-[11px] leading-6 text-fg-4">
            {relShort(m.sent_at)} · {clock(m.sent_at)}
          </time>
        </Tooltip>
        <Tooltip content={t("thread.collapse")}>
          <button type="button" onClick={fold} aria-label={t("thread.collapse")} aria-expanded className="-mr-1.5 flex size-6 shrink-0 items-center justify-center rounded-md text-fg-4 hover:bg-hover hover:text-fg">
            <ChevronDown className="size-3.5" />
          </button>
        </Tooltip>
      </header>
      <div className="px-4 pb-3.5 pl-[50px] pt-1">
        <SenderWarning m={m} details={details.data} />
        {held > 0 && <HeldImages onLoad={() => setRemote(true)} />}
        <MailBody body={body} title={t("thread.emailContent")} />
        {quoted && (
          <div>
            <Tooltip content={showQuoted ? t("thread.hideQuoted") : t("thread.showQuoted")}>
              <button
                type="button"
                aria-expanded={showQuoted}
                aria-label={showQuoted ? t("thread.hideQuoted") : t("thread.showQuoted")}
                onClick={() => setShowQuoted((v) => !v)}
                className="mt-2 inline-flex h-4 items-center rounded-sm bg-hover px-1.5 text-fg-3 outline-none transition-colors hover:bg-active hover:text-fg focus-visible:shadow-[0_0_0_2px_var(--focus)]"
              >
                <MoreHorizontal className="size-3.5" />
              </button>
            </Tooltip>
            {showQuoted && (
              <div className="mt-2 border-l-2 border-line pl-3 text-fg-3">
                <MailBody body={quoted} title={t("thread.quotedContent")} />
              </div>
            )}
          </div>
        )}
        <MailAttachments items={m.attachments} />
      </div>
    </article>
  );
});
