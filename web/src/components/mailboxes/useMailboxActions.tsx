import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/ui/confirm";
import { Dialog } from "@/components/ui/dialog";
import { toast } from "@/components/ui/toast";
import { api } from "@/lib/api/endpoints";
import type { Mailbox, MailboxTest } from "@/lib/api/types";
import { qk, removeFromList, upsertInList } from "@/lib/queries";
import { TestResults } from "./kinds";

/** Test, enable/disable and remove, shared by the list and the detail page. */
export function useMailboxActions() {
  const { t } = useTranslation("settingsChannels");
  const qc = useQueryClient();
  const confirm = useConfirm();
  const [testing, setTesting] = useState<{ mailbox: Mailbox; result?: MailboxTest; error?: unknown } | null>(null);

  const setStatus = useMutation({
    mutationFn: ({ m, status }: { m: Mailbox; status: Mailbox["status"] }) => api.mailboxes.update(m.id, { status }),
    onMutate: ({ m, status }) => {
      const prev = qc.getQueryData<Mailbox[]>(qk.mailboxes);
      upsertInList(qc, qk.mailboxes, { ...m, status });
      return { prev };
    },
    onError: (e, _v, ctx) => {
      if (ctx?.prev) qc.setQueryData(qk.mailboxes, ctx.prev);
      toast.error(e);
    },
    onSuccess: (m) => upsertInList(qc, qk.mailboxes, m),
  });

  const test = async (m: Mailbox) => {
    setTesting({ mailbox: m });
    try {
      const result = await api.mailboxes.test(m.id);
      setTesting({ mailbox: m, result });
    } catch (error) {
      setTesting({ mailbox: m, error });
    }
  };

  // The confirmation says how many tickets reply from the mailbox: they stay, but lose it as
  // their reply address. Without the count (an older API), it says what it always did.
  const remove = async (m: Mailbox, after?: () => void) => {
    const usage = await api.mailboxes.usage(m.id).catch(() => null);
    const body =
      usage && usage.tickets > 0
        ? t("actions.removeBodyTickets", { email: m.email, count: usage.tickets, open: usage.open })
        : t("actions.removeBody", { email: m.email });
    confirm.show(body, async () => {
      const prev = qc.getQueryData<Mailbox[]>(qk.mailboxes);
      removeFromList(qc, qk.mailboxes, m.id);
      try {
        await api.mailboxes.remove(m.id);
        // Frees a seat under the plan's mailbox limit.
        void qc.invalidateQueries({ queryKey: qk.billing, exact: true });
        toast.success(t("actions.removed"));
        after?.();
      } catch (e) {
        if (prev) qc.setQueryData(qk.mailboxes, prev);
        throw e;
      }
    }, { title: t("actions.removeTitle"), confirmLabel: t("actions.removeConfirm"), danger: true });
  };

  const testDialog = testing && (
    <Dialog
      open
      onOpenChange={(o) => !o && setTesting(null)}
      size="sm"
      title={t("actions.testTitle")}
      description={<span className="mono">{testing.mailbox.email}</span>}
      footer={
        <Button variant="primary" onClick={() => setTesting(null)}>
          {t("actions.done")}
        </Button>
      }
    >
      {testing.result ? (
        <TestResults result={testing.result} />
      ) : testing.error ? (
        <p role="alert" className="text-xs text-danger-fg">{t("actions.testFailed")}</p>
      ) : (
        <div className="flex flex-col gap-3 rounded-lg border border-line bg-sunken px-3 py-3" aria-busy="true">
          {[0, 1].map((i) => (
            <div key={i} className="flex items-center gap-2.5">
              <div className="skeleton size-[18px] rounded-sm" />
              <div className="skeleton h-2.5" style={{ width: i ? "40%" : "52%" }} />
            </div>
          ))}
        </div>
      )}
    </Dialog>
  );

  return { setStatus, test, remove, testDialog };
}
