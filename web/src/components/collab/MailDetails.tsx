// How a message came: the details card under "to ..." (people, date, who sent and signed
// it, how it travelled), its headers, and the warning a message that failed its sender's
// checks carries. Everything about the source is the receiving server's own verdict,
// read by the API (contracts/collab.md "Message details").

import { useQuery } from "@tanstack/react-query";
import { ChevronDown, ShieldAlert, ShieldQuestion } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { MailAddress, MailMessage, MailMessageDetails } from "@/lib/api/types";
import { formatAddress } from "@/lib/collab/recipients";
import { fullDate } from "@/lib/format";
import { q } from "@/lib/queries";
import { cn } from "@/lib/cn";
import { tone } from "@/lib/tones";
import { Popover } from "@/components/ui/popover";
import { Dialog } from "@/components/ui/dialog";
import { CopyButton } from "@/components/ui/copy-button";
import { Skeleton } from "@/components/ui/skeleton";

/** The details of a message from someone else, once it is open; nothing for one's own mail or a draft. */
export function useMessageDetails(m: MailMessage, external: boolean, open: boolean) {
  return useQuery({ ...q.mailMessageDetails(m.id), enabled: open && external && !m.draft });
}

const list = (xs: MailAddress[]) => xs.map(formatAddress).join(", ");
const domainOf = (email: string) => email.split("@")[1]?.toLowerCase() ?? email;

/** "to Maya Chen, cc Tom ▾": opens the details card (and reads one's own mail's source when it opens). */
export function DetailsTrigger({ m, details: given, loading: givenLoading }: { m: MailMessage; details: MailMessageDetails | undefined; loading: boolean }) {
  const { t } = useTranslation("collab");
  const [open, setOpen] = useState(false);
  const [headers, setHeaders] = useState(false);
  const own = useQuery({ ...q.mailMessageDetails(m.id), enabled: open && !m.draft && !given });
  const details = given ?? own.data;
  const loading = givenLoading || own.isLoading;
  const summary = [m.to.length > 0 && t("thread.to", { list: list(m.to) }), m.cc.length > 0 && t("thread.cc", { list: list(m.cc) }), m.bcc.length > 0 && t("thread.bcc", { list: list(m.bcc) })]
    .filter(Boolean)
    .join(", ");
  return (
    <>
      <Popover
        open={open}
        onOpenChange={setOpen}
        className="w-[min(480px,calc(100vw-32px))] p-3"
        trigger={
          <button
            type="button"
            aria-label={t("details.show")}
            aria-expanded={open}
            className="group/details -mx-1 flex min-w-0 max-w-full items-center gap-0.5 rounded-sm px-1 text-left text-fg-3 outline-none hover:bg-hover hover:text-fg focus-visible:shadow-[0_0_0_2px_var(--focus)] data-[state=open]:bg-hover data-[state=open]:text-fg"
          >
            <span className="min-w-0 truncate" data-ph-mask>
              {summary || t("details.noRecipients")}
            </span>
            <ChevronDown className="size-3 shrink-0 text-fg-4 group-hover/details:text-fg" />
          </button>
        }
      >
        <DetailsCard m={m} details={details} loading={loading} onHeaders={() => (setOpen(false), setHeaders(true))} />
      </Popover>
      <HeadersDialog open={headers} onOpenChange={setHeaders} details={details} subject={m.subject} />
    </>
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <>
      <dt className="whitespace-nowrap text-right text-fg-4">{label}</dt>
      <dd className="min-w-0 break-words text-fg-2" data-ph-mask>
        {children}
      </dd>
    </>
  );
}

const resultTone = (r: string) => (r === "pass" ? tone.green.chip : r === "fail" ? tone.red.chip : r === "softfail" ? tone.amber.chip : tone.gray.chip);

function DetailsCard({ m, details, loading, onHeaders }: { m: MailMessage; details: MailMessageDetails | undefined; loading: boolean; onHeaders: () => void }) {
  const { t } = useTranslation("collab");
  const checks = details ? (["spf", "dkim", "dmarc"] as const).filter((k) => details[k]) : [];
  return (
    <div className="text-[12px] leading-[18px]">
      <dl className="grid grid-cols-[max-content_minmax(0,1fr)] gap-x-3 gap-y-1">
        <Row label={t("details.from")}>{formatAddress(m.from)}</Row>
        {m.reply_to.length > 0 && <Row label={t("details.replyTo")}>{list(m.reply_to)}</Row>}
        {m.to.length > 0 && <Row label={t("details.to")}>{list(m.to)}</Row>}
        {m.cc.length > 0 && <Row label={t("details.cc")}>{list(m.cc)}</Row>}
        {m.bcc.length > 0 && <Row label={t("details.bcc")}>{list(m.bcc)}</Row>}
        <Row label={t("details.date")}>{fullDate(m.sent_at)}</Row>
        <Row label={t("details.subject")}>{m.subject || t("list.noSubject")}</Row>
        {loading && (
          <Row label={t("details.mailedBy")}>
            <Skeleton className="mt-1 h-3 w-32" />
          </Row>
        )}
        {details?.mailed_by && <Row label={t("details.mailedBy")}>{details.mailed_by}</Row>}
        {details && details.signed_by.length > 0 && <Row label={t("details.signedBy")}>{details.signed_by.join(", ")}</Row>}
        {details?.encryption && <Row label={t("details.security")}>{details.encryption === "tls" ? t("details.tls") : t("details.noTls")}</Row>}
        {checks.length > 0 && (
          <Row label={t("details.checks")}>
            <span className="flex flex-wrap gap-1">
              {checks.map((k) => (
                <span key={k} className={cn("rounded-sm px-1.5 text-[11px] leading-[17px]", resultTone(details![k]))}>
                  {k.toUpperCase()} {details![k]}
                </span>
              ))}
            </span>
          </Row>
        )}
      </dl>
      {details && details.headers.length > 0 && (
        <div className="mt-2.5 border-t border-line pt-2">
          <button type="button" onClick={onHeaders} className="rounded-sm text-[12px] text-fg-3 underline-offset-2 outline-none hover:text-fg hover:underline focus-visible:shadow-[0_0_0_2px_var(--focus)]">
            {t("details.showHeaders")}
          </button>
        </div>
      )}
    </div>
  );
}

function HeadersDialog({ open, onOpenChange, details, subject }: { open: boolean; onOpenChange: (o: boolean) => void; details: MailMessageDetails | undefined; subject: string }) {
  const { t } = useTranslation("collab");
  const text = (details?.headers ?? []).map((h) => `${h.name}: ${h.value}`).join("\n");
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title={t("details.headersTitle")} description={subject} size="xl">
      <div className="relative">
        <CopyButton value={text} label={t("details.copyHeaders")} className="absolute right-1.5 top-1.5 bg-surface" />
        <dl className="ph-no-capture max-h-[60vh] overflow-auto rounded-md border border-line bg-sunken p-3 pr-10 font-mono text-[11.5px] leading-[18px]" data-ph-mask>
          {details?.headers.map((h, i) => (
            <div key={i} className="break-all">
              <dt className="inline font-medium text-fg">{h.name}:</dt> <dd className="inline text-fg-2">{h.value}</dd>
            </div>
          ))}
        </dl>
      </div>
    </Dialog>
  );
}

/** Gmail's "via": the domain the mail came through, beside the sender. */
export function Via({ details }: { details: MailMessageDetails | undefined }) {
  const { t } = useTranslation("collab");
  if (!details?.via) return null;
  return (
    <span data-text className="shrink-0 cursor-text select-text text-[11.5px] text-fg-4">
      {t("details.via", { domain: details.via })}
    </span>
  );
}

/** The warning a message carries when the receiving server could not confirm its sender. */
export function SenderWarning({ m, details }: { m: MailMessage; details: MailMessageDetails | undefined }) {
  const { t } = useTranslation("collab");
  if (!details?.warning) return null;
  const spoofed = details.warning === "spoofed";
  const Icon = spoofed ? ShieldAlert : ShieldQuestion;
  const domain = domainOf(m.from.email);
  return (
    <div role="alert" className={cn("mb-3 flex items-start gap-2.5 rounded-md px-3 py-2.5", spoofed ? tone.red.soft : tone.amber.soft)}>
      <Icon className={cn("mt-px size-4 shrink-0", spoofed ? tone.red.text : tone.amber.text)} />
      <div className="min-w-0 text-[12.5px] leading-[18px]">
        <p className="font-medium text-fg">{spoofed ? t("details.spoofedTitle") : t("details.unverifiedTitle")}</p>
        <p className="mt-0.5 text-fg-2">{spoofed ? t("details.spoofedBody", { domain }) : t("details.unverifiedBody", { domain })}</p>
      </div>
    </div>
  );
}
