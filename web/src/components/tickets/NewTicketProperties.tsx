// The new ticket page's details column: the ticket page's properties, set on the
// draft, then who it goes to, with their earlier tickets.

import { useQuery } from "@tanstack/react-query";
import { Lock, Users } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { FieldValues, Priority, Severity } from "@/lib/api/types";
import { api } from "@/lib/api/endpoints";
import { q, qk } from "@/lib/queries";
import { Avatar } from "@/components/ui/avatar";
import { Picker } from "@/components/ui/picker";
import { ContactCard } from "@/components/contacts/ContactCard";
import { PriorityIcon, priorityOrder, SeverityChip, severityLabel } from "./bits";
import { useDisplay } from "./fields";
import { BrandChip, sortTeams, TeamDot } from "./teams";
import { Prop, PropButton, TagsProp, unassignedIcon, useAssigneeItems, useSeverityItems } from "./detail/TicketProperties";
import { FieldsSection } from "./detail/FieldsSection";
import { newTicketDrafts } from "./newTicket";
import type { NewTicketState } from "./useNewTicket";

export function NewTicketProperties({ s }: { s: NewTicketState }) {
  const { t: tr } = useTranslation("inbox");
  const display = useDisplay();
  const { draft, set, teams, teamRow, brandRow } = s;
  const { data: members } = useQuery(q.members());
  const { data: tags } = useQuery(q.tags());
  const assigneeItems = useAssigneeItems(teamRow);
  const severityItems = useSeverityItems();
  const assignee = members?.find((m) => m.user.id === s.assignee)?.user;
  const setField = (key: string, v: FieldValues[string] | null) => {
    const fields = { ...newTicketDrafts.get(s.key).fields };
    if (v === null) delete fields[key];
    else fields[key] = v;
    set({ fields });
  };

  return (
    <div className="flex flex-col">
      <section className="border-b border-line-soft px-2.5 py-2.5">
        <Prop label={tr("props.priority")}>
          <Picker
            value={draft.priority}
            placeholder={tr("props.setPriority")}
            items={priorityOrder.map((p) => ({ value: p, label: display.priority(p), icon: <PriorityIcon priority={p} /> }))}
            onSelect={(v) => set({ priority: v as Priority })}
            trigger={
              <PropButton>
                <PriorityIcon priority={draft.priority} />
                {display.priority(draft.priority)}
              </PropButton>
            }
          />
        </Prop>
        <Prop label={tr("props.severity")}>
          <Picker
            value={draft.severity ?? "__none"}
            placeholder={tr("props.setSeverity")}
            items={severityItems}
            onSelect={(v) => set({ severity: v === "__none" ? null : (v as Severity) })}
            trigger={
              <PropButton placeholder={!draft.severity}>
                {draft.severity ? <SeverityChip severity={draft.severity} /> : <span className="h-[16px] w-[22px] shrink-0 rounded-[4px] border border-dashed border-line-strong" />}
                {draft.severity ? severityLabel(draft.severity) : tr("props.noSeverity")}
              </PropButton>
            }
          />
        </Prop>
        <Prop label={tr("props.assignee")}>
          <Picker
            value={s.assignee ?? "__none"}
            placeholder={tr("props.assignTo")}
            items={assigneeItems}
            onSelect={(v) => set({ assignee: v === "__none" ? null : v })}
            trigger={
              <PropButton placeholder={!assignee}>
                {assignee ? <Avatar name={assignee.name} src={assignee.avatar_url} size="xs" /> : unassignedIcon}
                <span className="truncate">{assignee ? (assignee.id === s.me ? tr("newTicket.you") : assignee.name) : tr("props.unassigned")}</span>
              </PropButton>
            }
          />
        </Prop>
        {teams.hasTeams && (
          <Prop label={tr("props.team")}>
            <Picker
              value={s.teamId ?? "__none"}
              placeholder={tr("props.chooseTeam")}
              items={[
                ...sortTeams(teams.teams, teams.myTeams).map((x) => ({
                  value: x.id,
                  label: x.name,
                  icon: <TeamDot team={x} />,
                  hint: x.private ? <Lock className="size-3" /> : undefined,
                })),
                { value: "__none", label: tr("props.noTeam"), icon: <span className="size-2 rounded-full border border-dashed border-line-strong" /> },
              ]}
              onSelect={(v) => set({ team: v === "__none" ? null : v })}
              trigger={
                <PropButton placeholder={!teamRow}>
                  {teamRow ? <TeamDot team={teamRow} /> : <Users className="size-3.5 text-fg-3" />}
                  <span className="truncate">{teamRow?.name ?? tr("props.noTeam")}</span>
                  {teamRow?.private && <Lock className="size-3 text-fg-4" aria-label={tr("teams.private")} />}
                </PropButton>
              }
            />
          </Prop>
        )}
        {teams.multiBrand && (
          <Prop label={tr("props.brand")}>
            <Picker
              value={s.brand ?? null}
              placeholder={tr("props.chooseBrand")}
              items={teams.brands.map((b) => ({
                value: b.id,
                label: b.name,
                icon: <span className="size-2 rounded-[2px] bg-fg-4" style={b.color ? { backgroundColor: b.color } : undefined} />,
              }))}
              onSelect={(brandId) => set({ brandId })}
              trigger={
                <PropButton>{brandRow ? <BrandChip brand={brandRow} className="max-w-full" /> : <span className="text-fg-4">{tr("props.chooseBrand")}</span>}</PropButton>
              }
            />
          </Prop>
        )}
        <TagsProp
          tags={draft.tags.map((name) => ({ name, color: tags?.find((x) => x.name === name)?.color }))}
          canEdit
          onChange={(v) => set({ tags: v })}
        />
      </section>

      <FieldsSection collapseEmpty values={draft.fields} disabled={false} onChange={setField} />

      <section className="border-b border-line-soft px-4 py-3.5">
        <h3 className="label-caps mb-3">{tr("props.contact")}</h3>
        <Recipient s={s} />
      </section>
    </div>
  );
}

/** Who it goes to: their card and earlier tickets, an address typed for a contact we know, or a new one. */
function Recipient({ s }: { s: NewTicketState }) {
  const { t: tr } = useTranslation("inbox");
  const to = s.draft.to;
  const email = !to?.contact ? to?.email : undefined;
  const known = useQuery({
    queryKey: qk.of("contacts", "pick", email ?? ""),
    queryFn: () => api.contacts.search({ q: email, limit: 8 }),
    enabled: !!email,
    select: (r) => r.data.find((c) => c.email?.toLowerCase() === email?.toLowerCase()),
  });
  const contactId = to?.contact?.id ?? known.data?.id;
  if (contactId) return <ContactCard contactId={contactId} compact />;
  if (email)
    return (
      <div className="flex items-center gap-2.5">
        <Avatar name={email} size="lg" />
        <div className="min-w-0">
          <div className="truncate text-[13px] font-medium" data-ph-mask>
            {email}
          </div>
          {!known.isPending && <div className="text-xs text-fg-3">{tr("newTicket.newContact")}</div>}
        </div>
      </div>
    );
  return <p className="text-xs text-fg-4">{tr("newTicket.recipientHint")}</p>;
}
