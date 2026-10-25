import { useLayoutEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { SignatureView } from "@/components/editor/signature";
import { cn } from "@/lib/cn";

/** How tall a signature rests before it folds. */
const FOLDED = 72;

/**
 * The signature a reply carries, under what is written: drawn as the customer gets it
 * (its layout, styles and sized images kept) and quiet, since it is added when the reply
 * goes, not typed. A tall one rests folded and opens in place.
 */
export function ReplySignature({ html }: { html: string }) {
  const { t } = useTranslation("inbox");
  const box = useRef<HTMLDivElement>(null);
  const [tall, setTall] = useState(false);
  const [open, setOpen] = useState(false);
  // Its images load after it is drawn: measured again as it grows.
  useLayoutEffect(() => {
    const el = box.current;
    const view = el?.firstElementChild;
    if (!el || !view) return;
    const measure = () => setTall(el.scrollHeight > FOLDED + 1);
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(view);
    return () => ro.disconnect();
  }, [html]);
  const folded = tall && !open;
  return (
    <div role="group" aria-label={t("composer.signature")} className="px-3 pb-2 animate-fade-in motion-reduce:animate-none">
      <div className="mb-1 flex h-5 items-center gap-2 text-[11px] text-fg-4">
        <span className="h-px w-3 bg-line-strong" aria-hidden />
        <span className="truncate">{t("composer.signatureAdded")}</span>
        {tall && (
          <button
            type="button"
            aria-expanded={open}
            onClick={() => setOpen((o) => !o)}
            className="ml-auto shrink-0 rounded-sm px-1 text-fg-3 outline-none transition-colors hover:bg-hover hover:text-fg focus-visible:shadow-[0_0_0_2px_var(--focus)]"
          >
            {open ? t("composer.signatureLess") : t("composer.signatureMore")}
          </button>
        )}
      </div>
      <div
        ref={box}
        onClick={folded ? () => setOpen(true) : undefined}
        style={folded ? { maxHeight: FOLDED } : undefined}
        className={cn(
          "overflow-hidden opacity-70 transition-opacity duration-150 hover:opacity-100",
          folded && "cursor-pointer [mask-image:linear-gradient(to_bottom,black_50%,transparent)]",
        )}
      >
        <SignatureView html={html} className="ph-no-capture text-[13px]" />
      </div>
    </div>
  );
}
