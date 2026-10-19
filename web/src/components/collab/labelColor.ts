// Setting a label's colour (contracts/collab.md "Labels and colours"): a Gmail label or an
// Outlook category takes one of the opendesk tones (lib/collab/labelTones.ts), set at the
// provider first.

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api } from "@/lib/api/endpoints";
import type { MailFolder } from "@/lib/api/types";
import { qk, upsertInList } from "@/lib/queries";
import { toast } from "@/components/ui/toast";
import { LABEL_TONES, toneHex, toneOf, type LabelTone } from "@/lib/collab/labelTones";

export { LABEL_TONES, toneHex, toneOf, type LabelTone };

/** Whether a folder takes a colour: a label (Gmail label, Outlook category), never IMAP. */
export function canColor(folder: Pick<MailFolder, "kind" | "role"> | null | undefined, provider: string | undefined): boolean {
  return !!folder && folder.kind === "label" && !folder.role && !!provider && provider !== "imap";
}

/** Sets a label's colour: shown at once, put back if the provider refuses. */
export function useSetLabelColor() {
  const { t } = useTranslation("collab");
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (v: { folder: MailFolder; tone: LabelTone; provider: string }) => api.collab.folders.color(v.folder.id, v.tone),
    onMutate: (v) => {
      const key = qk.mailFolders(v.folder.account_id);
      const prev = qc.getQueryData<MailFolder[]>(key);
      if (prev) qc.setQueryData<MailFolder[]>(key, prev.map((f) => (f.id === v.folder.id ? { ...f, color: toneHex(v.provider, v.tone) } : f)));
      return { prev, key };
    },
    onSuccess: (f) => {
      upsertInList(qc, qk.mailFolders(f.account_id), f);
      void qc.invalidateQueries({ queryKey: qk.mailThreadLists });
    },
    onError: (e, _v, ctx) => {
      if (ctx?.prev) qc.setQueryData(ctx.key, ctx.prev);
      toast.error(e, t("labelColor.failed"));
    },
  });
}
