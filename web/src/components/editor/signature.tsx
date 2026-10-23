// A signature in a composer is one block: the stored signature (already sanitized by the
// server) kept as written, drawn as its author designed it, selectable and removable as a
// whole but never edited inline. It is written out as <div class="od-signature">…</div>,
// a class the stored email policy keeps, so a saved draft opens with it intact. A Gmail
// draft's own signature (class gmail_signature) opens as one too.

import { Node } from "@tiptap/core";
import { NodeViewWrapper, ReactNodeViewRenderer, type NodeViewProps } from "@tiptap/react";
import { Settings2, X } from "lucide-react";
import { useLayoutEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { Tooltip } from "@/components/ui/tooltip";
import { cn } from "@/lib/cn";
import { cleanSignatureHtml, sanitizeEmailHtml } from "@/lib/sanitize";

export const SIGNATURE_CLASS = "od-signature";
const GMAIL_CLASS = "gmail_signature";

// Inside the view's own root: the page's styles stay out (no prose frame on a logo), the
// signature's stay in, and it renders on the light pane a mail client shows it on.
const VIEW_CSS = `
:host { display: block; contain: layout paint; overflow: hidden; color: #1f2328; overflow-wrap: break-word; }
.sig { display: flow-root; }
.sig.still { pointer-events: none; }
img { max-width: 100%; }
a { color: #1155cc; }
`;

/**
 * A signature drawn as it is sent: its tables, inline styles and sized images kept, in a
 * root of its own. `still` makes it one target (a composer selects it on click).
 */
export function SignatureView({ html, still, className }: { html: string; still?: boolean; className?: string }) {
  const host = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const el = host.current;
    if (!el) return;
    const root = el.shadowRoot ?? el.attachShadow({ mode: "open" });
    root.innerHTML = `<style>${VIEW_CSS}</style><div class="sig${still ? " still" : ""}">${sanitizeEmailHtml(html).html}</div>`;
  }, [html, still]);
  // On a dark surface the light pane gets a margin of its own, as a designed email does.
  return <div ref={host} className={cn("rounded-md dark:bg-white dark:p-3", className)} data-ph-mask />;
}

/** The signature's block, as the composer writes it in HTML. */
export function signatureBlockHtml(html: string): string {
  return `<div class="${SIGNATURE_CLASS}">${html}</div>`;
}

const inert = () => document.implementation.createHTMLDocument("");

function body(html: string): HTMLElement {
  return new DOMParser().parseFromString(`<!doctype html><body>${html}`, "text/html").body;
}

/** The composer's own signature block in html: the first one outside a quoted message. */
function ownBlock(root: HTMLElement): HTMLElement | null {
  return [...root.querySelectorAll<HTMLElement>(`div.${SIGNATURE_CLASS}`)].find((el) => !el.closest("blockquote")) ?? null;
}

/**
 * html with its signature block changed to `next` (removed when `next` is blank), or null
 * when it has none (the person took it out, or wrote it before signatures were blocks).
 */
export function replaceSignature(html: string, next: string): string | null {
  const root = body(html);
  const block = ownBlock(root);
  if (!block) return null;
  if (next.trim()) block.innerHTML = next;
  else block.remove();
  return root.innerHTML;
}

/** html without its signature block: what the person wrote. */
export function withoutSignature(html: string): string {
  const root = body(html);
  ownBlock(root)?.remove();
  return root.innerHTML;
}

export type SignatureBlockOptions = {
  /** Opens where the signature is edited; no settings button without it. */
  onSettings: (() => void) | null;
};

export const SignatureBlock = Node.create<SignatureBlockOptions>({
  name: "signatureBlock",
  group: "block",
  atom: true,
  selectable: true,
  draggable: false,

  addOptions() {
    return { onSettings: null };
  },

  addAttributes() {
    return {
      html: {
        default: "",
        rendered: false,
        // A Gmail signature keeps its own wrapper; ours is the block itself.
        parseHTML: (el) => cleanSignatureHtml(el.classList.contains(SIGNATURE_CLASS) ? el.innerHTML : el.outerHTML),
      },
    };
  },

  parseHTML() {
    return [
      { tag: `div.${SIGNATURE_CLASS}`, priority: 1000 },
      { tag: `div.${GMAIL_CLASS}`, priority: 1000 },
    ];
  },

  renderHTML({ node }) {
    // Built in a document of its own: nothing in it loads or runs while it is written out.
    const el = inert().createElement("div");
    el.className = SIGNATURE_CLASS;
    el.innerHTML = node.attrs.html as string;
    return el;
  },

  addNodeView() {
    return ReactNodeViewRenderer(SignatureNodeView);
  },
});

function SignatureNodeView({ node, selected, deleteNode, extension, editor }: NodeViewProps) {
  const { t } = useTranslation("settings");
  const onSettings = (extension.options as SignatureBlockOptions).onSettings;
  const editable = editor.isEditable;
  const button = "flex size-6 items-center justify-center rounded-sm text-fg-3 transition-colors hover:bg-hover hover:text-fg [&_svg]:size-3.5";
  return (
    <NodeViewWrapper
      contentEditable={false}
      role="group"
      aria-label={t("signatureEditor.block")}
      data-signature-block=""
      className={cn(
        "group/sig relative my-2 rounded-md transition-shadow motion-reduce:transition-none",
        selected ? "shadow-[0_0_0_2px_var(--focus)]" : "hover:shadow-[0_0_0_1px_var(--line)]",
      )}
    >
      <SignatureView html={node.attrs.html as string} still />
      {editable && (
        <div
          className={cn(
            "absolute right-1 top-1 flex items-center gap-0.5 rounded-md border border-line bg-surface p-0.5 opacity-0 shadow-xs transition-opacity group-focus-within/sig:opacity-100 group-hover/sig:opacity-100 motion-reduce:transition-none",
            selected && "opacity-100",
          )}
          onMouseDown={(e) => e.preventDefault()}
        >
          {onSettings && (
            <Tooltip content={t("signatureEditor.edit")}>
              <button type="button" aria-label={t("signatureEditor.edit")} onClick={onSettings} className={button}>
                <Settings2 />
              </button>
            </Tooltip>
          )}
          <Tooltip content={t("signatureEditor.remove")}>
            <button type="button" aria-label={t("signatureEditor.remove")} onClick={() => deleteNode()} className={button}>
              <X />
            </button>
          </Tooltip>
        </div>
      )}
    </NodeViewWrapper>
  );
}
