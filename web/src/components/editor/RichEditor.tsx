import { EditorContent, useEditor, type Editor, type Extensions } from "@tiptap/react";
import StarterKit from "@tiptap/starter-kit";
import Placeholder from "@tiptap/extension-placeholder";
import Image from "@tiptap/extension-image";
import { Bold, Code, Heading2, Heading3, ImageIcon, Italic, Link2, List, ListOrdered, Quote, Strikethrough } from "lucide-react";
import { forwardRef, useEffect, useImperativeHandle, useRef, useState, type ReactNode } from "react";
import { Popover } from "@/components/ui/popover";
import { Button } from "@/components/ui/button";
import { TextInput } from "@/components/ui/input";
import { cn } from "@/lib/cn";
import { Tooltip } from "@/components/ui/tooltip";

export type RichEditorHandle = {
  editor: Editor | null;
  focus: () => void;
  clear: () => void;
  insertHtml: (html: string) => void;
  setHtml: (html: string) => void;
  isEmpty: () => boolean;
};

export type RichEditorProps = {
  value?: string;
  onChange?: (html: string) => void;
  placeholder?: string;
  className?: string;
  contentClassName?: string;
  /** "compact" shows inline formatting only; "full" adds headings, quotes, code and images. */
  toolbar?: "none" | "compact" | "full";
  toolbarEnd?: ReactNode;
  extensions?: Extensions;
  onSubmit?: () => void;
  autofocus?: boolean;
  onImage?: () => void;
  editable?: boolean;
  onFocus?: () => void;
  onKeyDown?: (e: KeyboardEvent) => boolean | void;
  ariaLabel?: string;
};

export const RichEditor = forwardRef<RichEditorHandle, RichEditorProps>(function RichEditor(
  {
    value = "",
    onChange,
    placeholder,
    className,
    contentClassName,
    toolbar = "compact",
    toolbarEnd,
    extensions = [],
    onSubmit,
    autofocus,
    onImage,
    editable = true,
    onFocus,
    onKeyDown,
    ariaLabel,
  },
  ref,
) {
  // Read when the editor draws it, so it follows the composer's mode.
  const placeholderRef = useRef(placeholder);
  placeholderRef.current = placeholder;
  const editor = useEditor({
    extensions: [
      StarterKit.configure({
        heading: { levels: [2, 3] },
        link: { openOnClick: false, autolink: true, HTMLAttributes: { rel: "noopener noreferrer nofollow", target: "_blank" } },
      }),
      Placeholder.configure({ placeholder: () => placeholderRef.current ?? "" }),
      Image.configure({ inline: false }),
      ...extensions,
    ],
    content: value,
    editable,
    autofocus: autofocus ? "end" : false,
    immediatelyRender: true,
    editorProps: {
      attributes: {
        class: cn("tiptap prose-od min-h-[64px] px-3 py-2.5", contentClassName),
        "aria-label": ariaLabel ?? placeholder ?? "Editor",
        "data-ph-mask": "true",
      },
      handleKeyDown: (_view, e) => {
        if (onKeyDown?.(e)) return true;
        if (e.key === "Enter" && (e.metaKey || e.ctrlKey) && onSubmit) {
          e.preventDefault();
          onSubmit();
          return true;
        }
        return false;
      },
    },
    onUpdate: ({ editor }) => onChange?.(editor.isEmpty ? "" : editor.getHTML()),
    onFocus: () => onFocus?.(),
  });

  // Follow external value changes (switching saved reply, loading an article).
  useEffect(() => {
    if (!editor) return;
    const cur = editor.isEmpty ? "" : editor.getHTML();
    if (value !== cur && !editor.isFocused) editor.commands.setContent(value || "", { emitUpdate: false });
  }, [value, editor]);

  useEffect(() => {
    editor?.setEditable(editable);
  }, [editable, editor]);

  // A new placeholder shows at once on an empty editor.
  useEffect(() => {
    if (editor && !editor.isDestroyed && editor.isEmpty) editor.view.dispatch(editor.state.tr);
  }, [placeholder, editor]);

  useImperativeHandle(
    ref,
    () => ({
      editor,
      focus: () => editor?.commands.focus("end"),
      clear: () => editor?.commands.clearContent(true),
      insertHtml: (html) => editor?.chain().focus().insertContent(html).run(),
      setHtml: (html) => editor?.commands.setContent(html, { emitUpdate: true }),
      isEmpty: () => editor?.isEmpty ?? true,
    }),
    [editor],
  );

  return (
    <div className={cn("flex min-h-0 flex-col", className)}>
      <EditorContent editor={editor} className="min-h-0 flex-1 overflow-y-auto" />
      {toolbar !== "none" && editor && (
        <Toolbar editor={editor} full={toolbar === "full"} onImage={onImage} end={toolbarEnd} />
      )}
    </div>
  );
});

function TB({ label, active, onClick, children, shortcut }: { label: string; active?: boolean; onClick: () => void; children: ReactNode; shortcut?: string }) {
  return (
    <Tooltip content={label} shortcut={shortcut}>
      <button
        type="button"
        aria-label={label}
        aria-pressed={active}
        onMouseDown={(e) => e.preventDefault()}
        onClick={onClick}
        className={cn(
          "flex size-7 items-center justify-center rounded-md text-fg-3 transition-colors hover:bg-hover hover:text-fg [&_svg]:size-[15px]",
          active && "bg-hover text-fg",
        )}
      >
        {children}
      </button>
    </Tooltip>
  );
}

export function Toolbar({ editor, full, onImage, end }: { editor: Editor; full?: boolean; onImage?: () => void; end?: ReactNode }) {
  return (
    <div className="flex items-center gap-0.5 px-1.5 pb-1.5">
      {full && (
        <>
          <TB label="Heading" active={editor.isActive("heading", { level: 2 })} onClick={() => editor.chain().focus().toggleHeading({ level: 2 }).run()}>
            <Heading2 />
          </TB>
          <TB label="Subheading" active={editor.isActive("heading", { level: 3 })} onClick={() => editor.chain().focus().toggleHeading({ level: 3 }).run()}>
            <Heading3 />
          </TB>
          <span className="mx-1 h-4 w-px bg-line" />
        </>
      )}
      <TB label="Bold" shortcut="mod b" active={editor.isActive("bold")} onClick={() => editor.chain().focus().toggleBold().run()}>
        <Bold />
      </TB>
      <TB label="Italic" shortcut="mod i" active={editor.isActive("italic")} onClick={() => editor.chain().focus().toggleItalic().run()}>
        <Italic />
      </TB>
      {full && (
        <TB label="Strikethrough" active={editor.isActive("strike")} onClick={() => editor.chain().focus().toggleStrike().run()}>
          <Strikethrough />
        </TB>
      )}
      <LinkButton editor={editor} />
      <TB label="Bulleted list" active={editor.isActive("bulletList")} onClick={() => editor.chain().focus().toggleBulletList().run()}>
        <List />
      </TB>
      <TB label="Numbered list" active={editor.isActive("orderedList")} onClick={() => editor.chain().focus().toggleOrderedList().run()}>
        <ListOrdered />
      </TB>
      {full && (
        <>
          <TB label="Quote" active={editor.isActive("blockquote")} onClick={() => editor.chain().focus().toggleBlockquote().run()}>
            <Quote />
          </TB>
          <TB label="Code block" active={editor.isActive("codeBlock")} onClick={() => editor.chain().focus().toggleCodeBlock().run()}>
            <Code />
          </TB>
          {onImage && (
            <TB label="Image" onClick={onImage}>
              <ImageIcon />
            </TB>
          )}
        </>
      )}
      <div className="ml-auto flex items-center gap-1">{end}</div>
    </div>
  );
}

function LinkButton({ editor }: { editor: Editor }) {
  const [open, setOpen] = useState(false);
  const [href, setHref] = useState("");
  const active = editor.isActive("link");
  const apply = () => {
    const v = href.trim();
    if (!v) editor.chain().focus().extendMarkRange("link").unsetLink().run();
    else {
      // A template tag (contracts/templates.md) is kept as written.
      const url = /^(https?:|mailto:|tel:|\{\{)/i.test(v) ? v : `https://${v}`;
      editor.chain().focus().extendMarkRange("link").setLink({ href: url }).run();
    }
    setOpen(false);
  };
  return (
    <Popover
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        if (o) setHref((editor.getAttributes("link").href as string | undefined) ?? "");
      }}
      side="top"
      className="w-[300px] p-2"
      trigger={
        <Tooltip content={active ? "Edit link" : "Link"} shortcut="mod k">
          <button
            type="button"
            aria-label="Link"
            aria-pressed={active}
            onMouseDown={(e) => e.preventDefault()}
            className={cn(
              "flex size-7 items-center justify-center rounded-md text-fg-3 transition-colors hover:bg-hover hover:text-fg [&_svg]:size-[15px]",
              active && "bg-hover text-fg",
            )}
          >
            <Link2 />
          </button>
        </Tooltip>
      }
    >
      <form
        className="flex items-center gap-1.5"
        onSubmit={(e) => {
          // The popover renders in a portal, but React still bubbles the submit to a form around the editor.
          e.preventDefault();
          e.stopPropagation();
          apply();
        }}
      >
        <TextInput autoFocus inputSize="sm" value={href} onChange={(e) => setHref(e.target.value)} placeholder="https://" aria-label="Link address" />
        <Button type="submit" size="sm" variant="primary">
          {href.trim() ? "Apply" : "Remove"}
        </Button>
      </form>
    </Popover>
  );
}
