// Feedback on a sent message that began as an AI draft (contracts/collab.md "AI draft
// outcomes"): what was wrong with the draft and, optionally, a better answer. The key that
// wrote the draft reads it back from its outcome feed; opendesk itself trains nothing.

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { MessageSquareText } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { api } from "@/lib/api/endpoints";
import type { AIDraftFeedbackReason, MailMessage } from "@/lib/api/types";
import { escapeHtml } from "@/lib/collab/html";
import { qk } from "@/lib/queries";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Textarea } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { toast } from "@/components/ui/toast";

const reasons: AIDraftFeedbackReason[] = ["wrong_facts", "wrong_tone", "missing_info", "should_not_answer", "other"];
const noteMax = 1000;

const textHtml = (text: string) =>
  text
    .trim()
    .split(/\n{2,}/)
    .map((p) => `<p>${escapeHtml(p).replace(/\n/g, "<br>")}</p>`)
    .join("");

/** The line under a sent message's header: which AI drafted it, and a way to say how it did. */
export function AIDraftFeedback({ m }: { m: MailMessage }) {
  const { t } = useTranslation("collab");
  const [open, setOpen] = useState(false);
  const d = m.ai_draft;
  if (!d || m.draft) return null;
  const how = d.outcome === "sent_unchanged" ? t("aiFeedback.asWritten") : d.outcome === "sent_edited" ? t("aiFeedback.edited") : "";
  return (
    <span className="inline-flex max-w-full items-center gap-1.5 text-fg-4">
      <span data-text className="cursor-text select-text truncate">
        {t("aiFeedback.from", { name: d.client })}
        {how && ` · ${how}`}
      </span>
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="inline-flex shrink-0 items-center gap-1 rounded-sm px-1 text-fg-3 outline-none hover:bg-hover hover:text-fg focus-visible:shadow-[0_0_0_2px_var(--focus)]"
      >
        <MessageSquareText className="size-3" />
        {d.has_feedback ? t("aiFeedback.edit") : t("aiFeedback.give")}
      </button>
      <FeedbackDialog m={m} open={open} onOpenChange={setOpen} />
    </span>
  );
}

function FeedbackDialog({ m, open, onOpenChange }: { m: MailMessage; open: boolean; onOpenChange: (o: boolean) => void }) {
  const { t } = useTranslation("collab");
  const qc = useQueryClient();
  const [reason, setReason] = useState<AIDraftFeedbackReason | undefined>();
  const [note, setNote] = useState("");
  const [better, setBetter] = useState("");
  useEffect(() => {
    if (!open) return;
    setReason(undefined);
    setNote("");
    setBetter("");
  }, [open]);

  const save = useMutation({
    mutationFn: (r: AIDraftFeedbackReason) =>
      api.collab.aiDraftFeedback(m.ai_draft!.id, {
        reason: r,
        note: note.trim() || undefined,
        better_answer_html: better.trim() ? textHtml(better) : undefined,
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.mailThread(m.thread_id) });
      toast.success(t("toast.aiFeedbackSaved"));
      onOpenChange(false);
    },
    onError: (e) => toast.error(e),
  });

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={t("aiFeedback.title")}
      description={t("aiFeedback.description", { name: m.ai_draft?.client ?? "" })}
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            {t("comments.cancel")}
          </Button>
          <Button variant="primary" disabled={!reason} loading={save.isPending} onClick={() => reason && save.mutate(reason)}>
            {t("aiFeedback.save")}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-3 pb-2">
        <label className="flex flex-col gap-1.5 text-[12.5px] font-medium">
          {t("aiFeedback.reason")}
          <Select
            value={reason}
            onChange={setReason}
            placeholder={t("aiFeedback.reasonPlaceholder")}
            aria-label={t("aiFeedback.reason")}
            options={reasons.map((r) => ({ value: r, label: t(`aiFeedback.reasons.${r}`) }))}
          />
        </label>
        <label className="flex flex-col gap-1.5 text-[12.5px] font-medium">
          {t("aiFeedback.note")}
          <Textarea value={note} maxLength={noteMax} rows={3} onChange={(e) => setNote(e.target.value)} placeholder={t("aiFeedback.notePlaceholder")} />
        </label>
        <label className="flex flex-col gap-1.5 text-[12.5px] font-medium">
          {t("aiFeedback.better")}
          <Textarea value={better} rows={5} onChange={(e) => setBetter(e.target.value)} placeholder={t("aiFeedback.betterPlaceholder")} />
        </label>
        <p className="text-[11.5px] text-fg-4">{t("aiFeedback.privacy")}</p>
      </div>
    </Dialog>
  );
}
