// One message of a conversation: collapsed to a line (from, snippet, time) or open
// with its people, body and files. The body goes through the app's one safe email
// renderer: sanitized again here, a designed email in its sandboxed frame without
// scripts, and images from other sites (CSS url() too) held back until asked for.

import { ChevronDown, Download, FileText, ImageOff, Paperclip } from "lucide-react";
import { memo, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import type { MailAddress, MailAttachment, MailMessage } from "@/lib/api/types";
import { formatAddress, normEmail } from "@/lib/collab/recipients";
import { bytes, clock, fullDate, relShort } from "@/lib/format";
import { cn } from "@/lib/cn";
import { isDesignedEmail, sanitizeEmailHtml, sanitizeProse } from "@/lib/sanitize";
import { escapeHtml } from "@/lib/collab/html";
import { Avatar } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Tooltip } from "@/components/ui/tooltip";
import { EmailFrame } from "@/components/tickets/detail/EmailFrame";

type Body = { designed: boolean; html: string; held: number };

/** A message body, sanitized once; mail from others holds back remote images until loaded. */
function useMailBody(m: MailMessage, external: boolean, remote: boolean): Body {
  return useMemo(() => {
    const html = m.body_html || `<p>${escapeHtml(m.body_text).replace(/\n/g, "<br>")}</p>`;
    const opts = { blockRemoteImages: external && !remote };
    if (isDesignedEmail(html)) return { designed: true, ...sanitizeEmailHtml(html, opts) };
    return { designed: false, ...sanitizeProse(html, opts) };
  }, [m.body_html, m.body_text, external, remote]);
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
  ownEmail: string;
  open: boolean;
  onToggle: () => void;
}) {
  const { t } = useTranslation("collab");
  const [remote, setRemote] = useState(false);
  const external = normEmail(m.from.email) !== normEmail(ownEmail);
  const name = m.from.name || m.from.email;
  const body = useMailBody(m, external, remote);
  const files = m.attachments.filter((a) => !a.inline).length;

  if (!open)
    return (
      <button
        type="button"
        onClick={onToggle}
        aria-expanded={false}
        aria-label={t("thread.expand", { name })}
        className="group flex w-full items-center gap-2.5 rounded-lg border border-line bg-surface px-4 py-2.5 text-left transition-colors hover:bg-sunken/60 focus-visible:shadow-[0_0_0_2px_var(--focus)] outline-none"
      >
        <Avatar name={name} size="sm" />
        <span className={cn("w-[160px] shrink-0 truncate text-[12.5px]", m.unread ? "font-semibold text-fg" : "text-fg-2")} data-ph-mask>
          {name}
        </span>
        {m.draft && <span className="shrink-0 text-[11px] font-medium text-tone-red">{t("thread.draft")}</span>}
        <span className="min-w-0 flex-1 truncate text-[12.5px] text-fg-4" data-ph-mask>
          {m.snippet}
        </span>
        {files > 0 && <Paperclip className="size-3 shrink-0 text-fg-4" />}
        <time dateTime={m.sent_at} title={fullDate(m.sent_at)} className="mono shrink-0 text-[11px] text-fg-4">
          {relShort(m.sent_at)}
        </time>
      </button>
    );

  return (
    <article aria-label={t("thread.expand", { name })} className="rounded-lg border border-line bg-surface">
      <header className="flex items-start gap-2.5 px-4 pt-3">
        <Avatar name={name} size="md" />
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-baseline gap-2">
            <span className="max-w-[50%] shrink-0 truncate text-[13px] font-medium" data-ph-mask>
              {name}
            </span>
            <span className="mono min-w-0 truncate text-[11.5px] text-fg-3" data-ph-mask>
              {m.from.email}
            </span>
            {m.draft && <span className="shrink-0 text-[11px] font-medium text-tone-red">{t("thread.draft")}</span>}
          </div>
          <div className="mt-0.5 flex flex-col text-[11.5px] leading-[17px] text-fg-3" data-ph-mask>
            {m.to.length > 0 && <span className="truncate">{t("thread.to", { list: list(m.to) })}</span>}
            {m.cc.length > 0 && <span className="truncate">{t("thread.cc", { list: list(m.cc) })}</span>}
            {m.bcc.length > 0 && <span className="truncate">{t("thread.bcc", { list: list(m.bcc) })}</span>}
            {m.reply_to.length > 0 && normEmail(m.reply_to[0].email) !== normEmail(m.from.email) && <span className="truncate">{t("thread.replyTo", { list: list(m.reply_to) })}</span>}
            {m.sent_by && <span className="truncate text-fg-4">{t("thread.sentBy", { name: m.sent_by.name })}</span>}
          </div>
        </div>
        <Tooltip content={fullDate(m.sent_at)}>
          <time dateTime={m.sent_at} className="mono shrink-0 pt-0.5 text-[11px] text-fg-4">
            {relShort(m.sent_at)} · {clock(m.sent_at)}
          </time>
        </Tooltip>
        <Tooltip content={t("thread.collapse")}>
          <button type="button" onClick={onToggle} aria-label={t("thread.collapse")} aria-expanded className="-mr-1.5 flex size-6 shrink-0 items-center justify-center rounded-md text-fg-4 hover:bg-hover hover:text-fg">
            <ChevronDown className="size-3.5" />
          </button>
        </Tooltip>
      </header>
      <div className="px-4 pb-3.5 pl-[50px] pt-2">
        {body.held > 0 && <HeldImages onLoad={() => setRemote(true)} />}
        {body.designed ? (
          <EmailFrame html={body.html} title={t("thread.emailContent")} />
        ) : (
          <div className="prose-od ph-no-capture text-[13px]" data-ph-mask dangerouslySetInnerHTML={{ __html: body.html }} />
        )}
        <MailAttachments items={m.attachments} />
      </div>
    </article>
  );
});
