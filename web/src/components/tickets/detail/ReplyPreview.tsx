// The email a reply sends, before it goes. From a team address the server renders it as
// sending does (POST /v1/templates/preview, kind reply, this ticket and address): the brand's
// layout, the reply filled, the signature and the quoted conversation, with what is wrong
// with its variables. From one of my own addresses it is personal mail (contracts/collab.md
// "Answering tickets"): the reply filled, then the signature, no layout.

import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { signatureBlockHtml } from "@/components/editor/signature";
import { Button } from "@/components/ui/button";
import { errorMessage } from "@/lib/api/client";
import { api } from "@/lib/api/endpoints";
import type { Ticket } from "@/lib/api/types";
import { qk } from "@/lib/queries";
import { EmailDialog, emailDocument, type EmailHeader } from "./EmailDialog";

export function ReplyPreview({
  open,
  onOpenChange,
  ticket,
  html,
  from,
  to,
  cc,
  bcc,
  files,
  mailboxId,
  personalSignature,
  canSend,
  onSend,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  ticket: Ticket;
  /** The draft as written. */
  html: string;
  from: EmailHeader["from"];
  to: string;
  cc: string[];
  bcc: string[];
  /** The names of the files going with it. */
  files: string[];
  /** The team address it goes from. */
  mailboxId: string | null;
  /** Set when it goes from one of my own addresses: that address's signature. */
  personalSignature?: string;
  canSend: boolean;
  onSend: () => void;
}) {
  const { t } = useTranslation("inbox");
  const { t: ui } = useTranslation("ui");
  const personal = personalSignature !== undefined;

  const team = useQuery({
    queryKey: qk.of("tickets", "reply-preview", ticket.id, mailboxId, html),
    queryFn: () => api.templates.preview({ kind: "reply", body_html: html, ticket_id: ticket.id, mailbox_id: mailboxId ?? undefined }),
    enabled: open && !personal,
    placeholderData: keepPreviousData,
    staleTime: 30_000,
    retry: false,
  });
  // Filled as the server fills it when the reply is created.
  const own = useQuery({
    queryKey: qk.of("tickets", "reply-preview-own", ticket.id, html),
    queryFn: () => api.tickets.render(ticket.id, html).then((r) => r.body_html),
    enabled: open && personal,
    placeholderData: keepPreviousData,
    staleTime: 30_000,
    retry: false,
  });
  const q = personal ? own : team;
  const signature = personalSignature?.trim() ? `<p></p>${signatureBlockHtml(personalSignature)}` : "";
  const doc = personal ? (own.data !== undefined ? emailDocument(own.data + signature) : undefined) : team.data?.html;

  return (
    <EmailDialog
      open={open}
      onOpenChange={onOpenChange}
      title={t("composer.previewTitle")}
      description={t("composer.previewDescription", { to })}
      header={{ from, to: [to], cc, bcc, subject: personal ? `Re: ${ticket.subject}` : team.data?.subject, files }}
      doc={doc}
      error={q.isError ? errorMessage(q.error, ui("previewFailed")) : undefined}
      problems={personal ? [] : team.data?.problems}
      actions={
        <>
          <Button onClick={() => onOpenChange(false)}>{t("composer.keepEditing")}</Button>
          <Button variant="primary" disabled={!canSend} onClick={onSend}>
            {t("composer.send")}
          </Button>
        </>
      }
    />
  );
}
