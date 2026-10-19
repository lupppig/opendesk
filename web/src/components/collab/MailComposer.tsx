// Writing mail from an account: reply, reply all and forward inline under a
// conversation, and new mail in the floating composer. Recipients are chips, the
// account's signature is put in, files go up through POST /v1/attachments, and
// mod+enter sends (POST /v1/collab/messages, through the provider within the request).
// What is written is kept as a draft at the provider (POST/PUT /v1/collab/drafts) a
// couple of seconds after typing stops and when the composer closes; Send sends that
// draft, and Discard deletes it. A draft made anywhere (Gmail, Outlook) opens here too.

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Loader2, Paperclip, Send, Trash2, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { create } from "zustand";
import { api, type CollabDraftBody, type CollabSendBody } from "@/lib/api/endpoints";
import { isApiError } from "@/lib/api/client";
import type { Attachment, MailAccount, MailAddress, MailAttachment, MailMessage, MailThreadDetail } from "@/lib/api/types";
import { escapeHtml, isBlankHtml } from "@/lib/collab/html";
import { sanitizeHtml } from "@/lib/sanitize";
import { forwardHeader, replyRecipients, replySubject, type ComposeMode } from "@/lib/collab/recipients";
import { bytes, fullDate } from "@/lib/format";
import { cn } from "@/lib/cn";
import { qk } from "@/lib/queries";
import { RichEditor, type RichEditorHandle } from "@/components/editor/RichEditor";
import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/ui/confirm";
import { Select } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast";
import { Tooltip } from "@/components/ui/tooltip";
import { AccountDot } from "./bits";
import { RecipientField } from "./RecipientField";

type Upload = { key: string; file: File; status: "uploading" | "done" | "error"; attachment?: Attachment; preview?: string };

export type ComposerDraft = {
  mode: ComposeMode;
  to: string[];
  cc: string[];
  bcc: string[];
  subject: string;
  html: string;
  showCc: boolean;
  showBcc: boolean;
  /** The draft it is saved as (the /v1/collab/drafts id), once saved. */
  draftId?: string | null;
  /** The draft's own files (not inline images), kept on it while listed. */
  kept?: MailAttachment[];
};

// Inline drafts survive moving between conversations (memory only).
const useDrafts = create<Record<string, ComposerDraft>>(() => ({}));

/** What the account adds under what you write. */
function withSignature(signature: string | undefined) {
  return signature && !isBlankHtml(signature) ? `<p></p><p></p>${signature}` : "<p></p>";
}

/** A fresh draft for a mode: who it goes to, its subject, and the signature (and the original, forwarding). */
export function startDraft(mode: ComposeMode, opts: { message?: MailMessage; ownEmail: string; signature?: string; subject?: string; forwardBlock?: string }): ComposerDraft {
  const r = opts.message ? replyRecipients(opts.message, opts.ownEmail, mode) : { to: [], cc: [] };
  const subject = opts.message ? replySubject(opts.message.subject, mode) : (opts.subject ?? "");
  const html = withSignature(opts.signature) + (mode === "forward" && opts.forwardBlock ? opts.forwardBlock : "");
  return { mode, to: r.to, cc: r.cc, bcc: [], subject, html, showCc: r.cc.length > 0, showBcc: false };
}

/** A stored draft, as the composer opens it: a reply when it answers a message, else new mail. */
export function draftFromMessage(m: MailMessage): ComposerDraft {
  const emails = (list: MailAddress[]) => list.map((a) => a.email).filter(Boolean);
  return {
    mode: m.in_reply_to_id ? "reply" : "new",
    to: emails(m.to),
    cc: emails(m.cc),
    bcc: emails(m.bcc),
    subject: m.subject,
    html: m.body_html || (m.body_text ? `<p>${escapeHtml(m.body_text)}</p>` : "<p></p>"),
    showCc: m.cc.length > 0,
    showBcc: m.bcc.length > 0,
    draftId: m.draft_id ?? m.id,
    kept: m.attachments.filter((a) => !a.inline),
  };
}

/** Saving a draft: how the composer says it, quietly. */
type SaveState = "idle" | "saving" | "saved" | "error";

/** Typing stops this long before the draft is saved. */
const SAVE_AFTER_MS = 2000;

export type MailComposerProps = {
  variant: "inline" | "floating";
  /** The conversation replied in (inline); its id keys the kept draft. */
  threadId?: string;
  accounts: Pick<MailAccount, "id" | "email" | "name" | "color" | "signature_html">[];
  /** The account it sends from; floating lets you change it. */
  accountId: string;
  onAccountChange?: (id: string) => void;
  message?: MailMessage;
  initial: ComposerDraft;
  /** People to suggest first: the conversation's participants. */
  suggestions?: MailAddress[];
  autoFocus?: boolean;
  onSent?: (m: MailMessage) => void;
  onDiscard?: () => void;
  /** The draft it is saved as, each time that changes (the conversation hides it while it is open here). */
  onDraftId?: (id: string | null) => void;
};

export function MailComposer({ variant, threadId, accounts, accountId, onAccountChange, message, initial, suggestions, autoFocus, onSent, onDiscard, onDraftId }: MailComposerProps) {
  const { t } = useTranslation("collab");
  const qc = useQueryClient();
  const confirm = useConfirm();
  const remembered = threadId && !initial.draftId ? useDrafts.getState()[threadId] : undefined;
  const [draft, setDraftState] = useState<ComposerDraft>(() => (remembered && remembered.mode === initial.mode ? remembered : initial));
  const [uploads, setUploads] = useState<Upload[]>([]);
  const [forwardFiles, setForwardFiles] = useState(true);
  const [keptFiles, setKeptFiles] = useState<MailAttachment[]>(() => draft.kept ?? []);
  const [saveState, setSaveState] = useState<SaveState>(draft.draftId ? "saved" : "idle");
  const editor = useRef<RichEditorHandle>(null);
  const fileInput = useRef<HTMLInputElement>(null);
  const account = accounts.find((a) => a.id === accountId);

  const setDraft = (patch: Partial<ComposerDraft>) =>
    setDraftState((d) => {
      const next = { ...d, ...patch };
      if (threadId) useDrafts.setState({ [threadId]: next });
      return next;
    });

  // ── The draft at the provider ───────────────────────────────────────────
  // Saves run one at a time from refs (a save can finish after the composer closed);
  // a change made while one runs saves again after it.
  const draftIdRef = useRef<string | null>(draft.draftId ?? null);
  const touched = useRef(false);
  const gone = useRef(false); // sent or discarded: nothing more is saved
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const inflight = useRef<Promise<void> | null>(null);
  const again = useRef(false);
  const latest = useRef({ draft, uploads, keptFiles, forwardFiles, accountId, message });
  latest.current = { draft, uploads, keptFiles, forwardFiles, accountId, message };
  const onDraftIdRef = useRef(onDraftId);
  onDraftIdRef.current = onDraftId;

  const contentOf = (s: typeof latest.current) => {
    const html = editor.current?.editor?.getHTML() ?? s.draft.html;
    return {
      to: s.draft.to,
      cc: s.draft.cc.length ? s.draft.cc : undefined,
      bcc: s.draft.bcc.length ? s.draft.bcc : undefined,
      subject: s.draft.subject.trim() || (s.message && s.draft.mode !== "new" ? replySubject(s.message.subject, s.draft.mode) : ""),
      body_html: isBlankHtml(html) ? "<p></p>" : html,
      attachment_ids: s.uploads.filter((u) => u.attachment).map((u) => u.attachment!.id),
    };
  };

  const saveNow = useCallback((): Promise<void> => {
    if (timer.current) clearTimeout(timer.current);
    timer.current = null;
    if (gone.current) return Promise.resolve();
    if (inflight.current) {
      again.current = true;
      return inflight.current;
    }
    const s = latest.current;
    if (s.uploads.some((u) => u.status === "uploading")) {
      timer.current = setTimeout(() => void saveNow(), SAVE_AFTER_MS);
      return Promise.resolve();
    }
    const content = contentOf(s);
    const savedUploads = s.uploads.filter((u) => u.attachment).map((u) => u.key);
    const create = () =>
      api.collab.drafts.create({
        account_id: s.accountId,
        mode: s.draft.mode,
        message_id: s.draft.mode === "new" ? undefined : s.message?.id,
        ...content,
        forward_attachments: s.draft.mode === "forward" ? s.forwardFiles : undefined,
      });
    setSaveState("saving");
    const run = async () => {
      const before = draftIdRef.current;
      try {
        let m: MailMessage;
        if (before) {
          const body: CollabDraftBody = { ...content, keep_attachments: s.keptFiles.map((a) => a.id) };
          try {
            m = await api.collab.drafts.update(before, body);
          } catch (e) {
            // Sent or discarded elsewhere meanwhile: what is written becomes a new draft.
            if (!isApiError(e, "not_found")) throw e;
            m = await create();
          }
        } else m = await create();
        if (gone.current) {
          // Discarded while it saved: the draft just made goes too.
          if (m.id !== before) void api.collab.drafts.remove(m.id).catch(() => undefined);
          return;
        }
        draftIdRef.current = m.id;
        setKeptFiles(m.attachments.filter((a) => !a.inline));
        setUploads((u) => u.filter((x) => !savedUploads.includes(x.key)));
        setDraftState((d) => {
          const next = { ...d, draftId: m.id };
          if (threadId) useDrafts.setState({ [threadId]: next });
          return next;
        });
        setSaveState("saved");
        if (m.id !== before) onDraftIdRef.current?.(m.id);
      } catch {
        setSaveState("error");
      }
    };
    inflight.current = run().finally(() => {
      inflight.current = null;
      if (again.current) {
        again.current = false;
        void saveNow();
      }
    });
    return inflight.current;
  }, [threadId]);

  /** A change by the member: the draft is saved once typing stops. */
  const changed = useCallback(() => {
    touched.current = true;
    if (gone.current) return;
    // Unsaved edits: no stale "Saved" until the next save.
    setSaveState((v) => (v === "saved" || v === "error" ? "idle" : v));
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => void saveNow(), SAVE_AFTER_MS);
  }, [saveNow]);
  const edit = (patch: Partial<ComposerDraft>) => {
    setDraft(patch);
    changed();
  };

  // Closing (or moving to another conversation) saves what is not saved yet.
  useEffect(
    () => () => {
      if (timer.current && touched.current && !gone.current) void saveNow();
      else if (timer.current) clearTimeout(timer.current);
    },
    [saveNow],
  );

  // Writing starts above the signature (and the forwarded message).
  useEffect(() => {
    if (autoFocus && draft.to.length) requestAnimationFrame(() => editor.current?.editor?.commands.focus("start"));
    // Once, when it opens.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Switching the From account swaps the signature, when the text still ends with the old one.
  const prevAccount = useRef(accountId);
  useEffect(() => {
    if (prevAccount.current === accountId) return;
    const before = accounts.find((a) => a.id === prevAccount.current)?.signature_html ?? "";
    prevAccount.current = accountId;
    // A draft belongs to its account: the one made in the old account goes, the next save makes it in this one.
    const old = draftIdRef.current;
    if (old) {
      draftIdRef.current = null;
      setKeptFiles([]);
      setDraftState((d) => ({ ...d, draftId: null }));
      onDraftIdRef.current?.(null);
      void api.collab.drafts.remove(old).catch(() => undefined);
      if (touched.current) changed();
    }
    const after = account?.signature_html ?? "";
    setDraftState((d) => {
      const html = before && d.html.includes(before) ? d.html.replace(before, after) : isBlankHtml(d.html) ? withSignature(after) : d.html;
      return { ...d, html };
    });
  }, [accountId, account, accounts, changed]);

  const upload = (files: FileList | File[]) => {
    for (const file of Array.from(files)) {
      const key = `${file.name}-${file.size}-${Math.random().toString(36).slice(2)}`;
      const preview = file.type.startsWith("image/") ? URL.createObjectURL(file) : undefined;
      setUploads((u) => [...u, { key, file, status: "uploading", preview }]);
      api.attachments
        .upload(file)
        .then((attachment) => {
          setUploads((u) => u.map((x) => (x.key === key ? { ...x, status: "done", attachment } : x)));
          changed();
        })
        .catch((e) => {
          setUploads((u) => u.map((x) => (x.key === key ? { ...x, status: "error" } : x)));
          toast.error(e, t("composer.uploadFailed", { name: file.name }));
        });
    }
  };
  useEffect(() => () => uploads.forEach((u) => u.preview && URL.revokeObjectURL(u.preview)), []); // eslint-disable-line react-hooks/exhaustive-deps

  const recipients = draft.to.length + draft.cc.length + draft.bcc.length;
  const busy = uploads.some((u) => u.status === "uploading");

  const send = useMutation({
    // With a draft, the draft is sent (saved with what the composer shows first).
    mutationFn: (v: { draftId: string | null; body: CollabSendBody; draftBody: CollabDraftBody }) =>
      v.draftId ? api.collab.drafts.send(v.draftId, v.draftBody) : api.collab.send(v.body),
    onSuccess: (m, v) => {
      // The conversation shows it at once; collab.changed brings the rest.
      qc.setQueryData<MailThreadDetail>(qk.mailThread(m.thread_id), (old) => {
        if (!old) return old;
        const messages = old.messages.filter((x) => x.id !== v.draftId);
        return messages.some((x) => x.id === m.id) ? { ...old, messages } : { ...old, messages: [...messages, m], thread: { ...old.thread, message_count: messages.length + 1, last_message_at: m.sent_at } };
      });
      void qc.invalidateQueries({ queryKey: qk.mailThreadLists });
      if (threadId) useDrafts.setState((s) => {
        const next = { ...s };
        delete next[threadId];
        return next;
      }, true);
      uploads.forEach((u) => u.preview && URL.revokeObjectURL(u.preview));
      setUploads([]);
      toast.success(t("toast.sent", { email: account?.email ?? "" }));
      onSent?.(m);
    },
    onError: (e) => {
      gone.current = false;
      toast.error(e, t("composer.sendFailed"));
    },
  });

  const submit = async () => {
    if (send.isPending) return;
    if (busy) return toast.error(t("composer.waitUploads"));
    if (!recipients) return toast.error(t("composer.needRecipient"));
    if (recipients > 100) return toast.error(t("composer.tooMany"));
    // A save on its way finishes first, so the draft it makes is the one sent.
    if (timer.current) clearTimeout(timer.current);
    timer.current = null;
    if (inflight.current) await inflight.current;
    gone.current = true;
    const content = contentOf(latest.current);
    send.mutate({
      draftId: draftIdRef.current,
      body: {
        account_id: accountId,
        mode: draft.mode,
        message_id: draft.mode === "new" ? undefined : message?.id,
        ...content,
        forward_attachments: draft.mode === "forward" ? forwardFiles : undefined,
      },
      draftBody: { ...content, keep_attachments: keptFiles.map((a) => a.id) },
    });
  };

  const discard = () => {
    const clear = () => {
      gone.current = true;
      if (timer.current) clearTimeout(timer.current);
      timer.current = null;
      if (threadId)
        useDrafts.setState((s) => {
          const next = { ...s };
          delete next[threadId];
          return next;
        }, true);
      const id = draftIdRef.current;
      if (id) {
        qc.setQueriesData<MailThreadDetail>({ queryKey: ["collab", "thread"] }, (old) => (old ? { ...old, messages: old.messages.filter((x) => x.id !== id) } : old));
        api.collab.drafts.remove(id).then(
          () => void qc.invalidateQueries({ queryKey: qk.mailThreadLists }),
          (e) => toast.error(e, t("composer.discardFailed")),
        );
      }
      onDiscard?.();
    };
    const dirty = !!draftIdRef.current || !isBlankHtml(draft.html.replace(account?.signature_html ?? "\u0000", "")) || uploads.length > 0;
    if (!dirty) return clear();
    confirm.show(draftIdRef.current ? t("composer.discardDraftConfirm") : t("composer.discardConfirm"), clear, {
      title: draftIdRef.current ? t("composer.discardDraftTitle") : t("composer.discardTitle"),
      confirmLabel: t("composer.discard"),
      danger: true,
    });
  };

  const showSubject = variant === "floating" || draft.mode === "forward" || draft.mode === "new";
  const ccToggles = (
    <span className="flex shrink-0 items-center gap-0.5">
      {!draft.showCc && (
        <button type="button" onClick={() => setDraft({ showCc: true })} className="rounded-sm px-1.5 py-0.5 text-[11.5px] text-fg-3 hover:bg-hover hover:text-fg">
          {t("composer.addCc")}
        </button>
      )}
      {!draft.showBcc && (
        <button type="button" onClick={() => setDraft({ showBcc: true })} className="rounded-sm px-1.5 py-0.5 text-[11.5px] text-fg-3 hover:bg-hover hover:text-fg">
          {t("composer.addBcc")}
        </button>
      )}
    </span>
  );

  const accountOptions = useMemo(
    () => accounts.map((a) => ({ value: a.id, label: a.name && a.name !== a.email ? `${a.name} (${a.email})` : a.email, icon: <AccountDot color={a.color} /> })),
    [accounts],
  );

  return (
    <div
      className={cn("flex min-h-0 flex-col", variant === "inline" && "rounded-lg border border-line bg-surface shadow-xs focus-within:border-line-strong")}
      onDragOver={(e) => e.preventDefault()}
      onDrop={(e) => {
        if (e.dataTransfer.files.length) {
          e.preventDefault();
          upload(e.dataTransfer.files);
        }
      }}
      onPaste={(e) => {
        const files = Array.from(e.clipboardData.files);
        if (files.length) {
          e.preventDefault();
          upload(files);
        }
      }}
    >
      {variant === "floating" ? (
        <div className="flex min-h-9 items-center gap-2 border-b border-line-soft px-3">
          <span className="w-9 shrink-0 text-xs text-fg-3">{t("composer.from")}</span>
          <Select size="sm" value={accountId} onChange={(v) => onAccountChange?.(v)} options={accountOptions} aria-label={t("composer.from")} className="h-7 border-transparent px-1.5 shadow-none hover:border-line" />
        </div>
      ) : (
        <div className="flex h-8 items-center gap-2 border-b border-line-soft px-3 text-[11.5px] text-fg-3">
          {account && <AccountDot color={account.color} />}
          <span className="min-w-0 truncate">
            {draft.mode === "forward" ? t("composer.forwarding") : message ? t("composer.replyingTo", { name: message.from.name || message.from.email }) : ""}
          </span>
          <span className="mono ml-auto truncate text-fg-4">{account?.email}</span>
        </div>
      )}
      <RecipientField label={t("composer.to")} values={draft.to} onChange={(to) => edit({ to })} suggestions={suggestions} autoFocus={autoFocus && !draft.to.length} end={ccToggles} onSubmit={() => void submit()} />
      {draft.showCc && <RecipientField label={t("composer.cc")} values={draft.cc} onChange={(cc) => edit({ cc })} suggestions={suggestions} onSubmit={() => void submit()} />}
      {draft.showBcc && <RecipientField label={t("composer.bcc")} values={draft.bcc} onChange={(bcc) => edit({ bcc })} suggestions={suggestions} onSubmit={() => void submit()} />}
      {showSubject && (
        <div className="flex min-h-8 items-center gap-2 border-b border-line-soft px-3">
          <label htmlFor={`subject-${threadId ?? "new"}`} className="min-w-9 shrink-0 text-xs text-fg-3">
            {t("composer.subject")}
          </label>
          <input
            id={`subject-${threadId ?? "new"}`}
            value={draft.subject}
            onChange={(e) => edit({ subject: e.target.value })}
            onKeyDown={(e) => {
              if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
                e.preventDefault();
                void submit();
              }
            }}
            className="h-7 min-w-0 flex-1 bg-transparent text-[12.5px] outline-none placeholder:text-fg-4"
            data-ph-mask
          />
        </div>
      )}
      <RichEditor
        ref={editor}
        value={draft.html}
        onChange={(html) => {
          setDraft({ html });
          // Typing, not the editor settling what it was given.
          if (editor.current?.editor?.isFocused) changed();
        }}
        placeholder={draft.mode === "new" ? t("composer.newPlaceholder") : t("composer.placeholder")}
        ariaLabel={draft.mode === "new" ? t("composer.newPlaceholder") : t("composer.placeholder")}
        onSubmit={() => void submit()}
        className={variant === "floating" ? "min-h-[200px] flex-1" : undefined}
        contentClassName={cn("text-[13px]", variant === "floating" ? "min-h-[180px]" : "min-h-[96px] max-h-[360px]")}
        toolbar="compact"
        toolbarEnd={
          <>
            <input ref={fileInput} type="file" multiple hidden onChange={(e) => e.target.files && upload(e.target.files)} />
            <Tooltip content={t("composer.attach")}>
              <button
                type="button"
                aria-label={t("composer.attach")}
                onClick={() => fileInput.current?.click()}
                className="flex size-7 items-center justify-center rounded-md text-fg-3 transition-colors hover:bg-hover hover:text-fg"
              >
                <Paperclip className="size-[15px]" />
              </button>
            </Tooltip>
            {onDiscard && (
              <Tooltip content={t("composer.discard")}>
                <button
                  type="button"
                  aria-label={t("composer.discard")}
                  onClick={discard}
                  className="flex size-7 items-center justify-center rounded-md text-fg-3 transition-colors hover:bg-hover hover:text-fg"
                >
                  <Trash2 className="size-[15px]" />
                </button>
              </Tooltip>
            )}
            {saveState !== "idle" && (
              <span aria-live="polite" className={cn("mr-1 whitespace-nowrap text-[11px]", saveState === "error" ? "text-tone-red" : "text-fg-4")}>
                {saveState === "saving" ? t("composer.saving") : saveState === "saved" ? t("composer.saved") : t("composer.saveFailed")}
              </span>
            )}
            <Tooltip content={busy ? t("composer.waitUploads") : t("composer.send")} shortcut="mod enter">
              <Button
                variant="primary"
                size="sm"
                onClick={() => void submit()}
                disabled={busy || !recipients}
                icon={send.isPending ? <Loader2 className="size-3.5 animate-spin motion-reduce:animate-none" /> : <Send className="size-3.5" />}
              >
                {send.isPending ? t("composer.sending") : t("composer.send")}
              </Button>
            </Tooltip>
          </>
        }
      />
      {(uploads.length > 0 || keptFiles.length > 0 || draft.mode === "forward") && (
        <div className="flex flex-wrap items-center gap-1.5 border-t border-line-soft px-3 py-2">
          {draft.mode === "forward" && !draft.draftId && message && message.attachments.some((a) => !a.inline) && (
            <label className="mr-2 flex items-center gap-2 text-[11.5px] text-fg-3">
              <Switch
                checked={forwardFiles}
                onCheckedChange={(v) => {
                  setForwardFiles(v);
                  changed();
                }}
                aria-label={t("composer.forwardAttachments")}
              />
              {t("composer.forwardAttachments")}
            </label>
          )}
          {keptFiles.map((a) => (
            <span key={`kept-${a.id}`} className="flex h-8 max-w-[220px] items-center gap-2 rounded-md border border-line bg-surface pl-1 pr-1 text-[11.5px]">
              <span className="flex size-6 items-center justify-center rounded-sm bg-hover">
                <Paperclip className="size-3 text-fg-3" />
              </span>
              <span className="min-w-0">
                <span className="block truncate">{a.filename}</span>
                <span className="mono block text-[10px] text-fg-4">{bytes(a.size)}</span>
              </span>
              <button
                type="button"
                aria-label={t("composer.remove", { name: a.filename })}
                onClick={() => {
                  setKeptFiles((x) => x.filter((y) => y.id !== a.id));
                  changed();
                }}
                className="flex size-5 shrink-0 items-center justify-center rounded-sm text-fg-4 hover:bg-hover hover:text-fg"
              >
                <X className="size-3" />
              </button>
            </span>
          ))}
          {uploads.map((u) => (
            <span key={u.key} className="flex h-8 max-w-[220px] items-center gap-2 rounded-md border border-line bg-surface pl-1 pr-1 text-[11.5px]">
              {u.preview ? (
                <img src={u.preview} alt="" className="size-6 rounded-sm object-cover" />
              ) : (
                <span className="flex size-6 items-center justify-center rounded-sm bg-hover">
                  <Paperclip className="size-3 text-fg-3" />
                </span>
              )}
              <span className="min-w-0">
                <span className="block truncate">{u.file.name}</span>
                <span className="mono block text-[10px] text-fg-4">{u.status === "uploading" ? t("composer.uploading") : u.status === "error" ? t("composer.failed") : bytes(u.file.size)}</span>
              </span>
              {u.status === "uploading" ? (
                <Loader2 className="size-3.5 shrink-0 animate-spin text-fg-4 motion-reduce:animate-none" />
              ) : (
                <button
                  type="button"
                  aria-label={t("composer.remove", { name: u.file.name })}
                  onClick={() => {
                    setUploads((x) => x.filter((y) => y.key !== u.key));
                    changed();
                  }}
                  className="flex size-5 shrink-0 items-center justify-center rounded-sm text-fg-4 hover:bg-hover hover:text-fg"
                >
                  <X className="size-3" />
                </button>
              )}
            </span>
          ))}
        </div>
      )}
    </div>
  );
}

/** The forward header and the original message, for the editor. */
export function useForwardBlock(message: MailMessage | undefined): string {
  const { t } = useTranslation("collab");
  return useMemo(() => {
    if (!message) return "";
    const head = forwardHeader(message, { title: t("composer.fwdTitle"), from: t("composer.fwdFrom"), date: t("composer.fwdDate"), subject: t("composer.fwdSubject"), to: t("composer.fwdTo") }, fullDate(message.sent_at));
    return `${head}<blockquote>${message.body_html ? sanitizeHtml(message.body_html) : `<p>${escapeHtml(message.body_text)}</p>`}</blockquote>`;
  }, [message, t]);
}
