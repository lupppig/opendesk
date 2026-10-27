// A new ticket on a full page, laid out like the ticket page: what is written on
// the left, its properties and the customer it goes to on the right. It writes
// the same draft as the quick dialog, which opens it with what was written.

import { useQuery } from "@tanstack/react-query";
import { useBlocker, useCanGoBack, useNavigate, useRouter } from "@tanstack/react-router";
import { PanelRight, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { Member, SavedReply } from "@/lib/api/types";
import { q } from "@/lib/queries";
import { cn } from "@/lib/cn";
import { useHotkeys } from "@/lib/hotkeys";
import { useDocumentTitle } from "@/app/RootLayout";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Field, TextInput } from "@/components/ui/input";
import { Tooltip } from "@/components/ui/tooltip";
import { RichEditor, type RichEditorHandle } from "@/components/editor/RichEditor";
import { templateGroupLabel, useCatalog } from "@/components/templates/catalog";
import { matchVars, refreshTags, templateExtension, VarSuggestPopup, type VarItem } from "@/components/templates/extension";
import { useNewTicket } from "@/components/tickets/useNewTicket";
import { isDirty, newTicketDrafts } from "@/components/tickets/newTicket";
import { NewTicketFrom, NewTicketKind, NewTicketTo } from "@/components/tickets/NewTicketFields";
import { NewTicketProperties } from "@/components/tickets/NewTicketProperties";
import { AttachButton, EmailChips, SavedReplyButton, UploadChip, uploadFiles } from "@/components/tickets/detail/composerParts";
import { mentionExtension, savedReplyExtension, SuggestionPopup } from "@/components/tickets/detail/suggestions";
import { readPanel, savePanel } from "@/components/tickets/detail/panel";
import type { Upload } from "@/components/tickets/detail/outbox";

export default function NewTicketPage() {
  const { t: tr } = useTranslation("inbox");
  const { t: indicatorText } = useTranslation("indicators");
  const navigate = useNavigate();
  const router = useRouter();
  const canGoBack = useCanGoBack();
  const created = useRef(false);
  const s = useNewTicket({ onCreated: () => (created.current = true) });
  const { draft, set } = s;
  const [uploads, setUploads] = useState<Upload[]>([]);
  const [dragging, setDragging] = useState(false);
  const [panel, setPanel] = useState(readPanel);
  const editor = useRef<RichEditorHandle>(null);
  const note = draft.kind === "note";
  const emailed = draft.kind === "reply" && !!draft.mailbox;
  const recipient = draft.to?.contact?.email ?? draft.to?.email;
  useDocumentTitle(tr("newTicket.title"));

  // Leaving with nothing written drops the defaults picked here; a draft stays for its next opening.
  const uploadsRef = useRef(uploads);
  uploadsRef.current = uploads;
  useEffect(
    () => () => {
      uploadsRef.current.forEach((u) => u.preview && URL.revokeObjectURL(u.preview));
      if (!isDirty(newTicketDrafts.get(s.key))) newTicketDrafts.clear(s.key);
    },
    [s.key],
  );

  const dirty = s.dirty || uploads.length > 0;
  const blocker = useBlocker({ shouldBlockFn: () => dirty && !created.current, enableBeforeUnload: () => dirty && !created.current, withResolver: true });
  const discard = () => {
    s.discard();
    setUploads([]);
    blocker.proceed?.();
  };
  const close = () => (canGoBack ? router.history.back() : void navigate({ to: "/inbox/$folder/{-$ticketId}", params: { folder: "unassigned", ticketId: undefined } }));
  const togglePanel = () =>
    setPanel((p) => {
      savePanel(!p);
      return !p;
    });
  useHotkeys({ escape: close, "]": togglePanel });

  // The composer's suggestions: "/" saved replies (the ticket's team's and everyone's),
  // "@" teammates in a note, "{{" variables in an email, filled by the server when it is created.
  const { data: members } = useQuery(q.members());
  const { data: allReplies } = useQuery(q.savedReplies());
  const replies = useMemo(() => (allReplies ?? []).filter((r) => !r.team_id || r.team_id === s.teamId), [allReplies, s.teamId]);
  const catalog = useCatalog("reply");
  const varItems: VarItem[] = catalog.variables.map((v) => ({ key: v.key, label: v.label, group: templateGroupLabel(v.group) }));
  const membersRef = useRef<Member[]>([]);
  const repliesRef = useRef<SavedReply[]>([]);
  const kindRef = useRef(draft.kind);
  const knownRef = useRef<Set<string> | null>(null);
  const varItemsRef = useRef<VarItem[]>([]);
  const catalogRef = useRef(catalog);
  membersRef.current = members ?? [];
  repliesRef.current = replies;
  kindRef.current = draft.kind;
  knownRef.current = catalog.loading ? null : catalog.known;
  varItemsRef.current = varItems;
  catalogRef.current = catalog;
  const extensions = useMemo(
    () => [
      mentionExtension(() => membersRef.current, () => kindRef.current === "note"),
      savedReplyExtension(
        () => repliesRef.current,
        (html, clear) => {
          clear();
          editor.current?.insertHtml(html);
        },
      ),
      templateExtension({
        known: () => knownRef.current,
        items: (query) => matchVars(varItemsRef.current, query, (g) => g),
        filters: () => catalogRef.current.filters,
        enabled: () => kindRef.current === "reply",
      }),
    ],
    [],
  );
  useEffect(() => {
    if (!catalog.loading) refreshTags(editor.current?.editor);
  }, [catalog.loading, catalog.known]);

  const upload = (files: FileList | File[]) => uploadFiles(files, setUploads);
  const busyUploads = uploads.some((u) => u.status === "uploading");
  const submit = () => {
    if (busyUploads) return s.setError(tr("composer.waitUploads"));
    s.submit(uploads.flatMap((u) => (u.attachment ? [u.attachment.id] : [])));
  };

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex h-12 shrink-0 items-center gap-2 border-b border-line px-3">
        <Tooltip content={tr("detail.close")} shortcut="esc">
          <Button variant="ghost" size="sm" square aria-label={tr("detail.close")} icon={<X className="size-4" />} onClick={close} />
        </Tooltip>
        <span className="mx-1 hidden h-5 w-px bg-line sm:block" />
        <h1 className="min-w-0 flex-1 truncate text-[14px] font-semibold tracking-[-0.01em]" data-ph-mask>
          {draft.subject.trim() || tr("newTicket.title")}
        </h1>
        <Button variant="primary" size="sm" loading={s.create.isPending} onClick={submit}>
          {draft.kind === "reply" ? tr("newTicket.createSend") : tr("newTicket.create")}
        </Button>
        <Tooltip content={panel ? tr("detail.hideDetails") : tr("detail.showDetails")} shortcut="]">
          <Button
            variant="ghost"
            size="sm"
            square
            aria-label={indicatorText("tickets.toggleDetails")}
            aria-pressed={panel}
            onClick={togglePanel}
            icon={<PanelRight className="size-4" />}
            className={cn("hidden lg:inline-flex", panel && "text-fg")}
          />
        </Tooltip>
      </header>

      {/* Phone and tablet: one column, the details under what is written. */}
      <div className="flex min-h-0 flex-1 flex-col overflow-y-auto lg:flex-row lg:overflow-hidden">
        <div className="min-w-0 flex-1 lg:overflow-y-auto">
          <div className="mx-auto flex w-full max-w-[760px] flex-col gap-4 px-4 py-5 sm:px-6">
            <NewTicketKind s={s} />
            <div className="grid gap-3 sm:grid-cols-[1fr_240px]">
              <NewTicketTo s={s} />
              <NewTicketFrom s={s} />
            </div>
            <Field label={tr("newTicket.subject")}>
              {({ id }) => <TextInput id={id} value={draft.subject} maxLength={500} onChange={(e) => set({ subject: e.target.value })} placeholder={tr("newTicket.subjectPlaceholder")} />}
            </Field>
            <div
              onDragOver={(e) => {
                if (e.dataTransfer.types.includes("Files")) {
                  e.preventDefault();
                  setDragging(true);
                }
              }}
              onDragLeave={(e) => {
                if (!e.currentTarget.contains(e.relatedTarget as Node)) setDragging(false);
              }}
              onDrop={(e) => {
                e.preventDefault();
                setDragging(false);
                if (e.dataTransfer.files.length) upload(e.dataTransfer.files);
              }}
              onPaste={(e) => {
                const files = Array.from(e.clipboardData.files);
                if (files.length) {
                  e.preventDefault();
                  upload(files);
                }
              }}
              className={cn(
                "relative rounded-lg border shadow-xs transition-[border-color,box-shadow,background-color] duration-200 focus-within:border-fg-4 focus-within:shadow-[0_0_0_3px_var(--focus)]",
                note ? "border-dashed border-note-line bg-note" : "border-line bg-surface",
              )}
            >
              {emailed && (
                <>
                  <div className="flex h-9 items-center gap-2 border-b border-line-soft px-3">
                    <span className="min-w-0 flex-1 truncate text-xs text-fg-3">
                      {recipient && (
                        <>
                          {tr("composer.to")} <span className="mono text-fg-2" data-ph-mask>{recipient}</span>
                        </>
                      )}
                    </span>
                    <button
                      type="button"
                      onClick={() => set({ showCc: !draft.showCc })}
                      aria-expanded={draft.showCc}
                      className={cn("ml-auto shrink-0 rounded-sm px-1.5 py-0.5 text-xs text-fg-3 transition-colors hover:bg-hover hover:text-fg", draft.showCc && "bg-hover text-fg")}
                    >
                      {tr("composer.ccBcc")}
                    </button>
                  </div>
                  {draft.showCc && (
                    <div className="animate-fade-in motion-reduce:animate-none">
                      <EmailChips label={tr("composer.cc")} values={draft.cc} onChange={(cc) => set({ cc })} />
                      <EmailChips label={tr("composer.bcc")} values={draft.bcc} onChange={(bcc) => set({ bcc })} />
                    </div>
                  )}
                </>
              )}
              <RichEditor
                ref={editor}
                value={draft.body}
                onChange={(body) => set({ body })}
                placeholder={note ? tr("newTicket.bodyNote") : tr("newTicket.bodyReply")}
                extensions={extensions}
                onSubmit={submit}
                contentClassName="min-h-[240px]"
                ariaLabel={note ? tr("composer.note") : tr("composer.reply")}
                toolbarEnd={
                  <>
                    <AttachButton onFiles={upload} />
                    <SavedReplyButton replies={replies} onPick={(r) => editor.current?.insertHtml(r.body_html)} />
                  </>
                }
              />
              {uploads.length > 0 && (
                <div className="flex flex-wrap gap-1.5 px-3 pb-2.5">
                  {uploads.map((u) => (
                    <UploadChip key={u.key} u={u} onRemove={() => setUploads((x) => x.filter((y) => y.key !== u.key))} />
                  ))}
                </div>
              )}
              {dragging && (
                <div className="pointer-events-none absolute inset-0 flex items-center justify-center rounded-lg border-2 border-dashed border-fg-3 bg-surface/90 text-[13px] font-medium text-fg-2 animate-fade-in">
                  {tr("composer.drop")}
                </div>
              )}
            </div>
            {s.error && (
              <p role="alert" className="text-xs text-danger-fg animate-fade-in">
                {s.error}
              </p>
            )}
          </div>
        </div>
        <aside
          aria-label={indicatorText("tickets.ticketDetails")}
          className={cn("shrink-0 border-t border-line lg:w-[300px] lg:overflow-y-auto lg:border-l lg:border-t-0", !panel && "lg:hidden")}
        >
          <NewTicketProperties s={s} />
        </aside>
      </div>

      <SuggestionPopup />
      <VarSuggestPopup />
      <Dialog
        open={blocker.status === "blocked"}
        onOpenChange={(o) => !o && blocker.reset?.()}
        title={tr("newTicket.discardTitle")}
        description={tr("newTicket.discardBody")}
        size="sm"
        footer={
          <>
            <Button variant="ghost" onClick={() => blocker.reset?.()}>
              {tr("newTicket.keepWriting")}
            </Button>
            <Button variant="danger-solid" onClick={discard}>
              {tr("newTicket.discard")}
            </Button>
          </>
        }
      />
    </div>
  );
}
