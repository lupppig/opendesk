// The one editor of every signature: a mail account's (synced with Gmail), an address's, a
// team's and a member's own. A signature is never degraded by opening it: one the visual
// editor can hold opens there, a designed one (tables, inline styles, sized images) opens
// as HTML and says why, and onChange runs only for the person's own edits, so a page sends
// a signature only when it was changed. Its preview shows it as recipients get it: the
// server's, filled, for signatures that take variables (a Collab signature brings its own,
// filled for the person writing); drawn live otherwise.

import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { SignatureView } from "@/components/editor/signature";
import type { TemplatePreviewBody, TemplateProblem } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { EmailPreview } from "./EmailPreview";
import { needsHtml, TemplateEditor } from "./TemplateEditor";

export type SignatureEditorProps = {
  value: string;
  /** The person's edits only; opening, following the provider or switching modes never call it. */
  onChange: (html: string) => void;
  ariaLabel: string;
  placeholder?: string;
  disabled?: boolean;
  /** A signature that takes template variables; without it, HTML sent as written. */
  kind?: "signature" | "personal_signature" | "collab_signature";
  /** The server's preview of a templated signature. */
  previewBody?: TemplatePreviewBody;
  /** A preview drawn by the caller in place of the email preview (a Collab signature's). */
  preview?: ReactNode;
  previewTitle?: string;
  previewHeight?: number;
  onProblems?: (p: TemplateProblem[]) => void;
  problems?: TemplateProblem[];
  minHeight?: number;
  /** Under the preview. */
  footer?: ReactNode;
  className?: string;
};

export function SignatureEditor({
  value,
  onChange,
  ariaLabel,
  placeholder,
  disabled,
  kind,
  previewBody,
  preview,
  previewTitle,
  previewHeight = 420,
  onProblems,
  problems,
  minHeight,
  footer,
  className,
}: SignatureEditorProps) {
  const { t } = useTranslation("settings");
  const [mode, setMode] = useState<"visual" | "html">("visual");
  // A designed signature opens with its preview showing.
  const [startsHtml] = useState(() => needsHtml(value));
  const plain = !kind;

  // Someone who cannot change it sees it as it is sent.
  if (plain && disabled)
    return (
      <div className={cn("flex flex-col gap-2", className)}>
        {value.trim() ? (
          <div className="rounded-md border border-line bg-surface p-3 text-[13px]">
            <SignatureView html={value} />
          </div>
        ) : (
          <p className="text-[12.5px] text-fg-4">{t("signatureEditor.none")}</p>
        )}
      </div>
    );

  return (
    <div className={cn("signature-editor flex flex-col gap-3", className)}>
      <TemplateEditor
        kind={kind ?? "signature"}
        html
        variables={!plain}
        value={value}
        onChange={onChange}
        onModeChange={setMode}
        placeholder={placeholder}
        ariaLabel={ariaLabel}
        disabled={disabled}
        minHeight={minHeight}
        problems={problems}
      />
      {plain
        ? mode === "html" && (
            <div role="region" aria-label={t("signatureEditor.preview")} className="flex flex-col overflow-hidden rounded-lg border border-line bg-sunken">
              <div className="border-b border-line bg-surface px-2.5 py-1.5 text-[12.5px] font-medium">{t("signatureEditor.preview")}</div>
              <div className="p-3">
                <div className="rounded-md border border-line bg-white p-3 text-[13px]">
                  {value.trim() ? <SignatureView html={value} className="dark:p-0" /> : <p className="text-[12.5px] text-[#6b7280]">{t("signatureEditor.none")}</p>}
                </div>
              </div>
            </div>
          )
        : (preview ??
          (previewBody && (
            <EmailPreview collapsible defaultOpen={startsHtml} height={previewHeight} title={previewTitle ?? t("signatureEditor.preview")} body={previewBody} onProblems={onProblems} />
          )))}
      {footer}
    </div>
  );
}
