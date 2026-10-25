// A sent email as its recipients got it: the copy kept when it went out (GET
// /v1/messages/{id}/original), with the layout, the signature and the quoted conversation.

import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { errorMessage } from "@/lib/api/client";
import { api } from "@/lib/api/endpoints";
import type { Message } from "@/lib/api/types";
import { fullDate } from "@/lib/format";
import { qk } from "@/lib/queries";
import { EmailDialog, emailDocument } from "./EmailDialog";

export function SentEmail({ m, open, onOpenChange }: { m: Message; open: boolean; onOpenChange: (o: boolean) => void }) {
  const { t } = useTranslation("inbox");
  const { t: ui } = useTranslation("ui");
  const { data, isError, error } = useQuery({
    queryKey: qk.of("message-original", m.id),
    queryFn: () => api.messages.original(m.id),
    enabled: open,
    staleTime: Infinity,
  });
  const to = m.to.join(", ") || t("composer.theCustomer");
  return (
    <EmailDialog
      open={open}
      onOpenChange={onOpenChange}
      title={t("message.sentTitle")}
      description={t("message.sentDescription", { to, time: fullDate(m.created_at) })}
      header={{
        from: m.from_email ? { name: m.from_name, email: m.from_email } : null,
        to: m.to,
        cc: m.cc,
        bcc: m.bcc,
        subject: m.subject,
        files: m.attachments.filter((a) => !a.inline).map((a) => a.filename),
      }}
      doc={data ? emailDocument(data.full_html) : undefined}
      error={isError ? errorMessage(error, t("message.originalMissing")) : undefined}
      actions={<Button onClick={() => onOpenChange(false)}>{ui("close")}</Button>}
    />
  );
}
