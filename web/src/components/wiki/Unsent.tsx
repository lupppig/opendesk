// What was typed on a page whose saving stopped for good (lib/wiki/saver.ts "forbidden",
// "deleted"): the page says why, and the text can be copied (formatted, and as plain
// text) before it is let go.

import { AlertTriangle, Copy } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { copyText } from "@/components/ui/copy-button";
import { toast } from "@/components/ui/toast";
import { escapeHtml } from "@/lib/collab/html";
import type { Draft } from "@/lib/wiki/saver";

/** The title and the body's text, a line per paragraph. Parsed inert: nothing loads or runs. */
export function draftText(d: Draft): string {
  const marked = d.body_html.replace(/<\/(p|h[1-6]|li|blockquote|pre)>|<br\s*\/?>/gi, "$&\n");
  const text = new DOMParser().parseFromString(marked, "text/html").body.textContent ?? "";
  return [d.title.trim(), text.trim()].filter(Boolean).join("\n\n");
}

async function copyDraft(d: Draft): Promise<boolean> {
  const text = draftText(d);
  try {
    const html = `${d.title.trim() ? `<h1>${escapeHtml(d.title)}</h1>` : ""}${d.body_html}`;
    await navigator.clipboard.write([new ClipboardItem({ "text/plain": new Blob([text], { type: "text/plain" }), "text/html": new Blob([html], { type: "text/html" }) })]);
    return true;
  } catch {
    return copyText(text);
  }
}

export function UnsentBanner({ reason, draft, onDiscard }: { reason: "forbidden" | "deleted"; draft: Draft; onDiscard: () => void }) {
  const { t } = useTranslation("wiki");
  return (
    <div role="alert" className="flex flex-wrap items-center gap-x-3 gap-y-2 border-b border-[color-mix(in_srgb,var(--tone-amber-solid)_35%,var(--line))] bg-tone-amber-soft px-4 py-2.5 text-[12.5px] text-fg animate-rise-in motion-reduce:animate-none">
      <AlertTriangle className="size-4 shrink-0 text-tone-amber" />
      <span className="min-w-0 flex-1">
        <span className="font-medium">{t(reason === "forbidden" ? "unsent.forbidden" : "unsent.deleted")}</span> <span className="text-fg-2">{t("unsent.body")}</span>
      </span>
      <span className="flex shrink-0 gap-1.5">
        <Button size="sm" onClick={onDiscard}>
          {t("unsent.discard")}
        </Button>
        <Button
          size="sm"
          variant="primary"
          icon={<Copy />}
          onClick={async () => {
            if (await copyDraft(draft)) toast.success(t("unsent.copied"));
          }}
        >
          {t("unsent.copy")}
        </Button>
      </span>
    </div>
  );
}
