import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "@/components/ui/toast";
import { api } from "@/lib/api/endpoints";
import { errorMessage } from "@/lib/api/client";
import { q, qk } from "@/lib/queries";
import { patchTicketEverywhere } from "@/lib/ticketCache";
import { useSession } from "@/lib/session";
import { useTeamsCtx } from "./teams";
import { createTicketBody, draftKey, isDirty, newTicketDrafts, newTicketProblem, useNewTicketDraft, type NewTicketDraft } from "./newTicket";

/**
 * Writing a new ticket, in the dialog or on its page: the workspace's draft, the
 * addresses it can go from, its team, and creating it. `onCreated` runs before
 * the created ticket opens.
 */
export function useNewTicket({ onCreated }: { onCreated?: () => void } = {}) {
  const { t: tr } = useTranslation("inbox");
  const qc = useQueryClient();
  const navigate = useNavigate();
  const me = useSession((s) => s.user!.id);
  const wid = useSession((s) => s.workspaceId!);
  const key = draftKey(me, wid);
  const draft = useNewTicketDraft(key);
  const set = useCallback((p: Partial<NewTicketDraft>) => newTicketDrafts.patch(key, p), [key]);
  const { data: mailboxes } = useQuery(q.mailboxes());
  const teams = useTeamsCtx();
  const [error, setError] = useState<string | null>(null);

  // The brand's addresses the caller may send from; the default brand first.
  const defaultBrand = teams.brands.find((b) => b.is_default)?.id;
  const brand = draft.brandId ?? defaultBrand;
  const sendable = (mailboxes ?? []).filter((m) => m.status !== "disabled" && teams.canSendFrom(m));
  const brandRow = teams.brand(brand);
  const fromChoices = teams.multiBrand && brand ? sendable.filter((m) => m.brand_id === brand) : sendable;
  const mailbox = draft.mailbox;
  useEffect(() => {
    if (mailbox && fromChoices.some((m) => m.id === mailbox)) return;
    const next = fromChoices.find((m) => m.status === "active" && m.sending !== "system_off") ?? fromChoices.find((m) => m.status === "active") ?? fromChoices[0];
    if (next?.id !== mailbox) set({ mailbox: next?.id });
  }, [fromChoices.map((m) => m.id).join(), mailbox]); // eslint-disable-line react-hooks/exhaustive-deps
  // The team follows the address's default team until someone picks one.
  const teamId = draft.team !== undefined ? draft.team : (teams.mailbox(mailbox)?.team_id ?? null);
  const teamRow = teams.team(teamId);
  // An address with no way to send cannot email the first message (mailbox_cannot_send).
  const noSender = draft.kind === "reply" && teams.mailbox(mailbox)?.sending === "system_off";
  const assignee = draft.assignee === undefined ? me : draft.assignee;

  const create = useMutation({
    mutationFn: (attachmentIds?: string[]) => api.tickets.create(createTicketBody(draft, { me, teamId, hasTeams: teams.hasTeams, attachmentIds })),
    onSuccess: (t) => {
      patchTicketEverywhere(qc, t, me);
      void qc.invalidateQueries({ queryKey: qk.counts });
      toast.success(tr("newTicket.created", { number: t.number }));
      newTicketDrafts.clear(key);
      onCreated?.();
      void navigate({ to: "/inbox/$folder/{-$ticketId}", params: { folder: t.assignee?.id === me ? "mine" : "open", ticketId: t.id } });
    },
    onError: (e) => setError(errorMessage(e)),
  });

  const submit = (attachmentIds?: string[]) => {
    setError(null);
    const problem = newTicketProblem(draft, noSender);
    if (problem) return setError(tr(`newTicket.${problem}`));
    create.mutate(attachmentIds);
  };

  return {
    key,
    me,
    draft,
    set,
    discard: () => newTicketDrafts.clear(key),
    dirty: isDirty(draft),
    teams,
    brand,
    brandRow,
    fromChoices,
    teamId,
    teamRow,
    noSender,
    assignee,
    error,
    setError,
    create,
    submit,
  };
}

export type NewTicketState = ReturnType<typeof useNewTicket>;
