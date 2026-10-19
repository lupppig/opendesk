// Sharing one conversation on its own (PUT /v1/collab/threads/{id}/shares): with
// everyone, or with teams and people. The rest of its account stays as it is.

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { api } from "@/lib/api/endpoints";
import type { MailAudience, MailThreadDetail } from "@/lib/api/types";
import { patchThreads } from "@/lib/collab/cache";
import { qk } from "@/lib/queries";
import { useSession } from "@/lib/session";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast";
import { AudienceLists, audienceIsEmpty, emptyAudience } from "./pickers";

export function ShareDialog({ detail, open, onOpenChange }: { detail: MailThreadDetail; open: boolean; onOpenChange: (o: boolean) => void }) {
  const { t } = useTranslation("collab");
  const qc = useQueryClient();
  const me = useSession((s) => s.user?.id);
  const [value, setValue] = useState<MailAudience>(detail.shares);
  useEffect(() => {
    if (open) setValue(detail.shares);
  }, [open, detail.shares]);

  const save = useMutation({
    mutationFn: (shares: MailAudience) => api.collab.threads.share(detail.thread.id, shares),
    onSuccess: (d) => {
      qc.setQueryData(qk.mailThread(d.thread.id), d);
      patchThreads(qc, [d.thread]);
      toast.success(t("toast.shared"));
      onOpenChange(false);
    },
    onError: (e) => toast.error(e),
  });

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={t("share.title")}
      description={t("share.description")}
      footer={
        <>
          {!audienceIsEmpty(detail.shares) && (
            <Button variant="ghost" className="mr-auto" onClick={() => save.mutate(emptyAudience())} disabled={save.isPending}>
              {t("share.unshare")}
            </Button>
          )}
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            {t("comments.cancel")}
          </Button>
          <Button variant="primary" loading={save.isPending} onClick={() => save.mutate(value.everyone ? { everyone: true, team_ids: [], user_ids: [] } : value)}>
            {t("share.save")}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4 pb-2">
        <label className="flex items-start justify-between gap-4 rounded-lg border border-line px-3.5 py-3">
          <span>
            <span className="block text-[13px] font-medium">{t("share.everyone")}</span>
            <span className="mt-0.5 block text-[12px] text-fg-3">{t("share.everyoneHint")}</span>
          </span>
          <Switch checked={value.everyone} onCheckedChange={(everyone) => setValue({ ...value, everyone })} aria-label={t("share.everyone")} />
        </label>
        {!value.everyone && <AudienceLists value={value} onChange={setValue} exclude={me} />}
        <p className="text-[11.5px] text-fg-4">{t("share.accountAudience")}</p>
      </div>
    </Dialog>
  );
}
