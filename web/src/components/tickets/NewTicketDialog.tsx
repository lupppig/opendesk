import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { Dialog } from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Field, TextInput } from "@/components/ui/input";
import { Picker } from "@/components/ui/picker";
import { Avatar } from "@/components/ui/avatar";
import { Tooltip } from "@/components/ui/tooltip";
import { RichEditor } from "@/components/editor/RichEditor";
import type { Priority } from "@/lib/api/types";
import { q } from "@/lib/queries";
import { useConfirm } from "@/components/ui/confirm";
import { shell } from "@/app/shellStore";
import { PriorityIcon, priorityLabel, priorityOrder } from "./bits";
import { ChevronDown, Lock, Maximize2, Users } from "lucide-react";
import { sortTeams, TeamDot } from "./teams";
import { useNewTicket } from "./useNewTicket";
import { pageOnlyCount } from "./newTicket";
import { NewTicketFrom, NewTicketKind, NewTicketTo } from "./NewTicketFields";

export default function NewTicketDialog() {
  const { t: tr } = useTranslation("inbox");
  useTranslation("indicators");
  const navigate = useNavigate();
  const confirm = useConfirm();
  const { data: members } = useQuery(q.members());
  const close = () => shell.newTicket(false);
  const s = useNewTicket({ onCreated: close });
  const { draft, set, teams, me, assignee, brand, brandRow, teamId, teamRow } = s;

  const discard = () => {
    s.discard();
    close();
  };
  const requestClose = () => {
    if (!s.dirty) return discard();
    confirm.show(tr("newTicket.discardBody"), discard, { title: tr("newTicket.discardTitle"), confirmLabel: tr("newTicket.discard"), danger: true });
  };
  // The page picks the draft up where the dialog leaves it.
  const expand = () => {
    close();
    void navigate({ to: "/tickets/new" });
  };

  const assigneeName = members?.find((m) => m.user.id === assignee)?.user.name;
  // Set on the full page and not shown here: it goes with the ticket, so say so and offer the page.
  const more = pageOnlyCount(draft);

  return (
    <Dialog
      open
      onOpenChange={(o) => !o && requestClose()}
      title={tr("newTicket.title")}
      size="lg"
      onEscapeKeyDown={(e) => {
        if (s.dirty) {
          e.preventDefault();
          requestClose();
        }
      }}
      onInteractOutside={(e) => {
        e.preventDefault();
        requestClose();
      }}
      actions={
        <Tooltip content={tr("newTicket.openPage")}>
          <button
            type="button"
            aria-label={tr("newTicket.openPage")}
            onClick={expand}
            className="flex size-7 shrink-0 items-center justify-center rounded-md text-fg-4 transition-colors hover:bg-hover hover:text-fg"
          >
            <Maximize2 className="size-3.5" />
          </button>
        </Tooltip>
      }
      footer={
        <>
          {s.error && (
            <p role="alert" className="mr-auto text-xs text-danger-fg animate-fade-in">
              {s.error}
            </p>
          )}
          <Button variant="ghost" onClick={requestClose}>
            {tr("common.cancel")}
          </Button>
          <Button variant="primary" loading={s.create.isPending} onClick={() => s.submit()}>
            {draft.kind === "reply" ? tr("newTicket.createSend") : tr("newTicket.create")}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <NewTicketKind s={s} />
        <div className="grid grid-cols-[1fr_240px] gap-3">
          <NewTicketTo s={s} />
          <NewTicketFrom s={s} />
        </div>
        <Field label={tr("newTicket.subject")}>
          {({ id }) => <TextInput id={id} value={draft.subject} onChange={(e) => set({ subject: e.target.value })} placeholder={tr("newTicket.subjectPlaceholder")} />}
        </Field>
        <div className="rounded-md border border-line transition-[border-color,box-shadow] duration-150 focus-within:border-fg-4 focus-within:shadow-[0_0_0_3px_var(--focus)]">
          <RichEditor
            value={draft.body}
            onChange={(body) => set({ body })}
            placeholder={draft.kind === "reply" ? tr("newTicket.bodyReply") : tr("newTicket.bodyNote")}
            contentClassName="min-h-[160px]"
            onSubmit={() => s.submit()}
          />
        </div>
        <div className="flex items-center gap-2">
          <Picker
            items={priorityOrder.map((p) => ({ value: p, label: priorityLabel(p), icon: <PriorityIcon priority={p} /> }))}
            value={draft.priority}
            onSelect={(v) => set({ priority: v as Priority })}
            placeholder={tr("props.setPriority")}
            trigger={
              <Button size="sm" icon={<PriorityIcon priority={draft.priority} />} iconRight={<ChevronDown className="size-3 text-fg-4" />}>
                {priorityLabel(draft.priority)}
              </Button>
            }
          />
          {teams.multiBrand && (
            <Picker
              items={teams.brands.map((b) => ({
                value: b.id,
                label: b.name,
                icon: <span className="size-2 rounded-[2px] bg-fg-4" style={b.color ? { backgroundColor: b.color } : undefined} />,
              }))}
              value={brand ?? null}
              onSelect={(brandId) => set({ brandId })}
              placeholder={tr("props.chooseBrand")}
              trigger={
                <Button
                  size="sm"
                  icon={<span className="size-2 rounded-[2px] bg-fg-4" style={brandRow?.color ? { backgroundColor: brandRow.color } : undefined} />}
                  iconRight={<ChevronDown className="size-3 text-fg-4" />}
                >
                  {brandRow?.name ?? tr("props.chooseBrand")}
                </Button>
              }
            />
          )}
          {teams.hasTeams && (
            <Picker
              items={[
                ...sortTeams(teams.teams, teams.myTeams).map((x) => ({
                  value: x.id,
                  label: x.name,
                  icon: <TeamDot team={x} />,
                  hint: x.private ? <Lock className="size-3" /> : undefined,
                })),
                { value: "__none", label: tr("props.noTeam"), icon: <span className="size-2 rounded-full border border-dashed border-line-strong" /> },
              ]}
              value={teamId ?? "__none"}
              onSelect={(v) => set({ team: v === "__none" ? null : v })}
              placeholder={tr("props.chooseTeam")}
              trigger={
                <Button size="sm" icon={teamRow ? <TeamDot team={teamRow} /> : <Users className="text-fg-3" />} iconRight={<ChevronDown className="size-3 text-fg-4" />}>
                  {teamRow?.name ?? tr("props.noTeam")}
                </Button>
              }
            />
          )}
          <Picker
            items={[
              { value: "__none", label: tr("props.unassigned"), icon: <span className="size-4 rounded-xs border border-dashed border-line-strong" /> },
              ...(members ?? []).filter((m) => m.role !== "viewer").map((m) => ({ value: m.user.id, label: m.user.name, icon: <Avatar name={m.user.name} src={m.user.avatar_url} size="xs" presence={m.presence} /> })),
            ]}
            value={assignee ?? "__none"}
            onSelect={(v) => set({ assignee: v === "__none" ? null : v })}
            placeholder={tr("props.assignTo")}
            trigger={
              <Button
                size="sm"
                icon={assignee ? <Avatar name={assigneeName ?? "?"} size="xs" /> : <span className="size-4 rounded-xs border border-dashed border-line-strong" />}
                iconRight={<ChevronDown className="size-3 text-fg-4" />}
              >
                {assignee ? (assignee === me ? tr("newTicket.you") : assigneeName) : tr("props.unassigned")}
              </Button>
            }
          />
          {more > 0 && (
            <Button size="sm" variant="ghost" icon={<Maximize2 className="size-3 text-fg-3" />} onClick={expand} className="ml-auto">
              {tr("newTicket.moreOnPage", { count: more })}
            </Button>
          )}
        </div>
      </div>
    </Dialog>
  );
}
