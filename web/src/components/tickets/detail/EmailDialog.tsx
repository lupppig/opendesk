// An email as its recipient gets it, in a dialog: who it is from and to, its subject and
// files, then the email itself in a frame of its own (no scripts, links open in a new tab)
// at a desktop or phone width. The reply preview and a sent reply's view share it.

import { Monitor, Paperclip, Smartphone, TriangleAlert } from "lucide-react";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Dialog } from "@/components/ui/dialog";
import { Spinner } from "@/components/ui/spinner";
import { Segmented } from "@/components/ui/tabs";
import type { TemplateProblem } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { sanitizeEmailHtml } from "@/lib/sanitize";
import { tone } from "@/lib/tones";

const DOC_CSS = `:root{color-scheme:light}body{margin:16px;color:#1f2328;font:14px/1.5 -apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,sans-serif;overflow-wrap:break-word}img{max-width:100%}a{color:#1155cc}p{margin:0 0 .75em}`;

/** Stored email HTML as a document for the frame, sanitized again on the way in. */
export function emailDocument(html: string): string {
  return `<!doctype html><html><head><meta charset="utf-8"><meta name="referrer" content="no-referrer"><base target="_blank"><style>${DOC_CSS}</style></head><body>${sanitizeEmailHtml(html).html}</body></html>`;
}

export type EmailHeader = {
  from: { name: string; email: string; replyTo?: string | null } | null;
  to: string[];
  cc: string[];
  bcc: string[];
  subject: string | undefined;
  /** The names of the files that go with it. */
  files: string[];
};

export function EmailDialog({
  open,
  onOpenChange,
  title,
  description,
  header,
  doc,
  error,
  problems = [],
  actions,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  title: ReactNode;
  description?: ReactNode;
  header: EmailHeader;
  /** The whole email as a document; undefined while it loads. */
  doc: string | undefined;
  /** Shown in place of the email when it could not be had. */
  error?: string;
  /** What is wrong with its variables. */
  problems?: TemplateProblem[];
  /** The footer's buttons, at its end. */
  actions: ReactNode;
}) {
  const { t } = useTranslation("inbox");
  const { t: ui } = useTranslation("ui");
  const [device, setDevice] = useState<"desktop" | "phone">("desktop");
  const { from } = header;
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      size="xl"
      title={title}
      description={description}
      footer={
        <>
          <Segmented
            size="xs"
            value={device}
            onChange={setDevice}
            className="mr-auto"
            options={[
              { value: "desktop", label: <span className="sr-only">{ui("desktop")}</span>, icon: <Monitor /> },
              { value: "phone", label: <span className="sr-only">{ui("phone")}</span>, icon: <Smartphone /> },
            ]}
          />
          {actions}
        </>
      }
    >
      <div className="flex flex-col gap-3">
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 rounded-lg border border-line bg-surface px-3 py-2 text-[12.5px]">
          {from && (
            <Row label={ui("from")}>
              {from.name && <span className="text-fg">{from.name} </span>}
              <span className="mono text-[11.5px] text-fg-3" data-ph-mask>
                &lt;{from.email}&gt;
              </span>
              {from.replyTo && <span className="text-[11.5px] text-fg-4"> {t("composer.repliesTo", { address: from.replyTo })}</span>}
            </Row>
          )}
          <Addresses label={t("composer.to")} values={header.to} />
          <Addresses label={t("composer.cc")} values={header.cc} />
          <Addresses label={t("composer.bcc")} values={header.bcc} />
          <Row label={t("composer.subject")}>
            <span className="text-fg-2" data-ph-mask>
              {header.subject || " "}
            </span>
          </Row>
          {header.files.length > 0 && (
            <Row label={t("composer.previewFiles")}>
              <span className="flex flex-wrap gap-1">
                {header.files.map((f, i) => (
                  <span key={`${f}-${i}`} className="inline-flex h-5 max-w-[220px] items-center gap-1 rounded-sm bg-hover px-1.5 text-[11.5px] text-fg-2">
                    <Paperclip className="size-3 shrink-0 text-fg-4" />
                    <span className="truncate">{f}</span>
                  </span>
                ))}
              </span>
            </Row>
          )}
        </dl>
        {problems.length > 0 && (
          <ul role="alert" className={cn("flex flex-col gap-1 rounded-md px-3 py-2 text-[12.5px]", tone.amber.soft)}>
            {problems.map((p, i) => (
              <li key={`${p.token}-${i}`} className="flex items-start gap-2 text-fg-2">
                <TriangleAlert className={cn("mt-0.5 size-3.5 shrink-0", tone.amber.text)} />
                <span className="min-w-0">
                  {p.message}
                  {p.token && <span className="mono ml-1.5 text-[11.5px] text-fg-3">{p.token}</span>}
                </span>
              </li>
            ))}
          </ul>
        )}
        <div className="flex h-[46vh] min-h-[280px] justify-center rounded-lg border border-line bg-sunken p-3">
          {error && !doc ? (
            <div className="flex items-center text-center text-[12.5px] text-fg-3">{error}</div>
          ) : doc ? (
            <iframe
              title={ui("emailPreview")}
              // No scripts and no access to the app; links open in a new tab.
              sandbox="allow-popups allow-popups-to-escape-sandbox"
              referrerPolicy="no-referrer"
              srcDoc={doc}
              className={cn(
                "ph-no-capture h-full rounded-md border border-line bg-white shadow-xs transition-[width] duration-200 motion-reduce:transition-none",
                device === "phone" ? "w-[380px] max-w-full" : "w-full",
              )}
              data-ph-mask
            />
          ) : (
            <div className="flex items-center">
              <Spinner className="size-4 text-fg-4" />
            </div>
          )}
        </div>
      </div>
    </Dialog>
  );
}

function Addresses({ label, values }: { label: string; values: string[] }) {
  if (!values.length) return null;
  return (
    <Row label={label}>
      <span className="mono text-[11.5px] text-fg-2" data-ph-mask>
        {values.join(", ")}
      </span>
    </Row>
  );
}

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="py-0.5 text-fg-4">{label}</dt>
      <dd className="min-w-0 break-words py-0.5">{children}</dd>
    </>
  );
}
