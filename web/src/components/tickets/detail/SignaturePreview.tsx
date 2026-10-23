import { HoverCard as HC } from "radix-ui";
import { useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { SignatureView } from "@/components/editor/signature";
import { floatingPanel } from "@/components/ui/popover";
import { cn } from "@/lib/cn";
import { signatureText } from "./composerTemplates";

/**
 * The signature a reply carries, as one line in the "Replying as" bar. Hover or
 * focus shows it in full, as the customer gets it (its layout, styles and sized
 * images kept); a click keeps it open until Escape or a click away.
 */
export function SignaturePreview({ html }: { html: string }) {
  const { t } = useTranslation("inbox");
  const line = useMemo(() => signatureText(html), [html]);
  const [open, setOpen] = useState(false);
  const pinned = useRef(false);
  const trigger = useRef<HTMLButtonElement>(null);
  if (!line) return null;
  const close = () => {
    pinned.current = false;
    setOpen(false);
  };
  return (
    <HC.Root
      open={open}
      onOpenChange={(o) => {
        // Pinned by a click: hover and focus leaving keep it open.
        if (o || !pinned.current) setOpen(o);
      }}
      openDelay={300}
      closeDelay={150}
    >
      <HC.Trigger asChild>
        <button
          ref={trigger}
          type="button"
          aria-label={t("composer.showSignature")}
          aria-expanded={open}
          onClick={() => {
            if (pinned.current) return close();
            pinned.current = true;
            setOpen(true);
          }}
          className="hidden max-w-[34%] shrink truncate rounded-sm px-1 text-left text-[11.5px] text-fg-4 outline-none transition-colors hover:bg-hover hover:text-fg-2 focus-visible:shadow-[0_0_0_2px_var(--focus)] data-[state=open]:text-fg-2 @[600px]:block"
          data-ph-mask
        >
          {line}
        </button>
      </HC.Trigger>
      <HC.Portal>
        <HC.Content
          side="bottom"
          align="end"
          sideOffset={6}
          collisionPadding={8}
          onEscapeKeyDown={close}
          onPointerDownOutside={(e) => {
            // The trigger's own click toggles it.
            if (trigger.current?.contains(e.target as Node)) return e.preventDefault();
            close();
          }}
          className={cn(floatingPanel, "w-[400px] max-w-[calc(100vw-16px)] origin-[var(--radix-hover-card-content-transform-origin)] motion-reduce:animate-none")}
        >
          <div className="label-caps px-3 pb-1 pt-2.5">{t("composer.signature")}</div>
          <div className="max-h-[260px] overflow-y-auto px-3 pb-3">
            <SignatureView html={html} className="ph-no-capture text-[12.5px]" />
          </div>
        </HC.Content>
      </HC.Portal>
    </HC.Root>
  );
}
