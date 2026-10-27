// The fields a new ticket starts from, the same in the dialog and on the page:
// email or log internally, who it goes to, and the address it goes from.

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Segmented } from "@/components/ui/tabs";
import { Picker } from "@/components/ui/picker";
import { Avatar } from "@/components/ui/avatar";
import { api } from "@/lib/api/endpoints";
import { qk } from "@/lib/queries";
import { addressLabel, TeamDot } from "./teams";
import type { NewTicketState } from "./useNewTicket";

const emailRe = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

export function NewTicketKind({ s }: { s: NewTicketState }) {
  const { t: tr } = useTranslation("inbox");
  return (
    <Segmented
      value={s.draft.kind}
      onChange={(kind) => s.set({ kind })}
      options={[
        { value: "reply", label: tr("newTicket.email") },
        { value: "note", label: tr("newTicket.internal") },
      ]}
    />
  );
}

export function NewTicketTo({ s }: { s: NewTicketState }) {
  const { t: tr } = useTranslation("inbox");
  const qc = useQueryClient();
  const [toQuery, setToQuery] = useState("");
  const contacts = useQuery({
    queryKey: qk.of("contacts", "pick", toQuery),
    queryFn: () => api.contacts.search({ q: toQuery, limit: 8 }),
    enabled: toQuery.trim().length > 0,
  });
  const to = s.draft.to;
  const toItems = (contacts.data?.data ?? []).map((c) => ({
    value: c.id,
    label: c.name || c.email || tr("newTicket.unnamed"),
    hint: c.email ?? undefined,
    icon: <Avatar name={c.name || c.email || "?"} src={c.avatar_url} size="xs" />,
  }));
  return (
    <Field label={tr("newTicket.to")}>
      {({ id }) =>
        to ? (
          <div className="flex h-8 items-center gap-2 rounded-md border border-line bg-surface pl-2 pr-1 text-sm">
            <Avatar name={to.contact?.name || to.email || "?"} size="xs" />
            <span className="min-w-0 flex-1 truncate" data-ph-mask>
              {to.contact ? `${to.contact.name} ` : ""}
              <span className="text-fg-3">{to.contact?.email ?? to.email}</span>
            </span>
            <Button size="xs" variant="ghost" onClick={() => s.set({ to: null })}>
              {tr("newTicket.change")}
            </Button>
          </div>
        ) : (
          <Picker
            items={toItems}
            value={null}
            width={380}
            placeholder={tr("newTicket.toPlaceholder")}
            emptyText={toQuery ? tr("newTicket.noContacts") : tr("newTicket.startTyping")}
            onSelect={(v) => {
              const contact = contacts.data?.data.find((c) => c.id === v);
              // The page's contact card shows them at once, before its own fetch answers.
              if (contact && !qc.getQueryData(qk.contact(v))) qc.setQueryData(qk.contact(v), contact);
              s.set({ to: { contact } });
            }}
            onCreate={(v) => emailRe.test(v) && s.set({ to: { email: v } })}
            createLabel={tr("newTicket.use")}
            trigger={
              <button
                id={id}
                type="button"
                className="flex h-8 w-full items-center rounded-md border border-line bg-surface px-2.5 text-left text-sm text-fg-4 outline-none transition-colors hover:border-line-strong focus-visible:border-fg-4 focus-visible:shadow-[0_0_0_3px_var(--focus)]"
              >
                {tr("newTicket.toPlaceholder")}
              </button>
            }
            onQueryChange={setToQuery}
          />
        )
      }
    </Field>
  );
}

export function NewTicketFrom({ s }: { s: NewTicketState }) {
  const { t: tr } = useTranslation("inbox");
  return (
    <Field label={tr("newTicket.from")} error={s.noSender ? tr("newTicket.cannotSend") : undefined}>
      {({ id }) => (
        <Select
          id={id}
          value={s.draft.mailbox}
          onChange={(mailbox) => s.set({ mailbox })}
          disabled={s.draft.kind === "note" || !s.fromChoices.length}
          placeholder={tr("newTicket.noMailbox")}
          options={s.fromChoices.map((m) => ({ value: m.id, label: addressLabel(m), icon: <TeamDot team={s.teams.team(m.team_id)} /> }))}
        />
      )}
    </Field>
  );
}
