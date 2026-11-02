import { i18n } from "@/i18n";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { AnimatePresence, motion } from "motion/react";
import { AnimatedNumber } from "@/components/motion";
import { springs } from "@/lib/motion";
import { ShieldAlert, Signal, Siren, Tag as TagIcon, Trash2, UserRound, Users, X, CircleDot } from "lucide-react";
import { forwardRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { Picker } from "@/components/ui/picker";
import { Avatar } from "@/components/ui/avatar";
import { Tooltip } from "@/components/ui/tooltip";
import { useConfirm } from "@/components/ui/confirm";
import { toast } from "@/components/ui/toast";
import type { Priority, Severity, Ticket, TicketStatus } from "@/lib/api/types";
import { cachedTicket } from "@/lib/ticketCache";
import { q } from "@/lib/queries";
import { PriorityIcon, priorityLabel, priorityOrder, SeverityChip, severityHint, severityLabel, severityOrder, StatusIcon, statusLabel } from "./bits";
import { useLabelChoices } from "./detail/LabelPicker";
import { useDisplay } from "./fields";
import { useBulkUpdate } from "./useTicketActions";
import { TeamMove, useTeamsCtx } from "./teams";
import type { TicketBulkQuery } from "@/lib/api/endpoints";

/** A bar action: labelled, or icon-only with a tooltip when the list is narrow. */
const Act = forwardRef<HTMLButtonElement, { compact?: boolean; icon: ReactNode; label: string } & React.ButtonHTMLAttributes<HTMLButtonElement>>(function Act(
  { compact, icon, label, ...rest },
  ref,
) {
  if (!compact)
    return (
      <Button ref={ref} variant="ghost" size="sm" icon={icon} {...rest}>
        {label}
      </Button>
    );
  return (
    <Tooltip content={label}>
      <Button ref={ref} variant="ghost" size="sm" square aria-label={label} icon={icon} {...rest} />
    </Tooltip>
  );
});

export function BulkBar({
  ids,
  query,
  allMatching,
  onSelectAll,
  onClear,
  spamView,
  compact,
}: {
  ids: string[];
  query: TicketBulkQuery;
  allMatching: boolean;
  onSelectAll?: () => void;
  onClear: () => void;
  spamView?: boolean;
  compact?: boolean;
}) {
  const { t } = useTranslation("inbox");
  useTranslation("indicators");
  const bulk = useBulkUpdate();
  const confirm = useConfirm();
  const { data: members } = useQuery(q.members());
  const { data: tags } = useQuery(q.tags());
  const [tagSel, setTagSel] = useState<string[]>([]);
  const labelChoices = useLabelChoices();
  const display = useDisplay();
  const teams = useTeamsCtx();
  const qc = useQueryClient();
  const n = ids.length;
  const target = allMatching ? { query } : { ids };

  const run = (action: { patch: Parameters<typeof bulk.mutate>[0]["patch"] }, done: (count: number) => string) =>
    bulk.mutate(
      { ...target, ...action },
      {
        onSuccess: (result) => {
          toast.success(done(allMatching ? result?.updated ?? 0 : n));
          if (allMatching) onClear();
        },
      },
    );

  return (
    <AnimatePresence>
      {(n > 0 || allMatching) && (
        <motion.div
          initial={{ opacity: 0, y: 12, scale: 0.98 }}
          animate={{ opacity: 1, y: 0, scale: 1 }}
          exit={{ opacity: 0, y: 8, scale: 0.98 }}
          transition={springs.pop}
          role="toolbar"
          aria-label={i18n.t("indicators:tickets.bulkActions")}
          className="absolute bottom-5 left-1/2 z-20 flex max-w-[calc(100%-24px)] -translate-x-1/2 items-center gap-0.5 overflow-x-auto rounded-lg border border-line bg-raised p-1 pl-3 shadow-float"
        >
          <span className="mr-2 whitespace-nowrap text-[12px] text-fg-2">
            {allMatching ? t("bulk.allMatching") : <AnimatedNumber value={n} className="font-medium text-fg" />}
            {!allMatching && !compact && <> {t("bulk.selectedWord", { count: n })}</>}
          </span>
          {!allMatching && onSelectAll && (
            <Button variant="ghost" size="xs" onClick={onSelectAll}>
              {t("bulk.selectAll")}
            </Button>
          )}
          <span className="mx-1 h-5 w-px bg-line" />
          <Picker
            side="top"
            placeholder={t("bulk.assignTo")}
            value={null}
            items={[
              { value: "__none", label: t("bulk.unassigned"), icon: <span className="size-4 rounded-xs border border-dashed border-line-strong" /> },
              ...(members ?? [])
                .filter((m) => m.role !== "viewer")
                .map((m) => ({ value: m.user.id, label: m.user.name, icon: <Avatar name={m.user.name} src={m.user.avatar_url} size="xs" presence={m.presence} /> })),
            ]}
            onSelect={(v) => run({ patch: { assignee_id: v === "__none" ? null : v } }, (count) => t("bulk.assigned", { count }))}
            trigger={
              <Act compact={compact} icon={<UserRound className="size-3.5" />} label={t("bulk.assign")} />
            }
          />
          {!allMatching && teams.hasTeams && (
            <TeamMove
              side="top"
              tickets={n ? ids.map((id) => cachedTicket(qc, id)).filter((x): x is Ticket => !!x) : []}
              onMove={(teamId) =>
                run(
                  { patch: { team_id: teamId } },
                  (count) => (teamId ? t("bulk.moved", { count, team: teams.team(teamId)?.name ?? "" }) : t("bulk.teamCleared", { count })),
                )
              }
              trigger={<Act compact={compact} icon={<Users className="size-3.5" />} label={t("bulk.team")} />}
            />
          )}
          <Picker
            side="top"
            placeholder={t("bulk.setStatus")}
            value={null}
            items={display.statuses().map((s) => ({ value: s, label: display.status(s), icon: <StatusIcon status={s} /> }))}
            onSelect={(v) => run({ patch: { status: v as TicketStatus } }, (count) => t("bulk.marked", { count, status: statusLabel(v as TicketStatus).toLowerCase() }))}
            trigger={
              <Act compact={compact} icon={<CircleDot className="size-3.5" />} label={t("bulk.status")} />
            }
          />
          <Picker
            side="top"
            placeholder={t("bulk.setPriority")}
            value={null}
            items={priorityOrder.map((p) => ({ value: p, label: display.priority(p), icon: <PriorityIcon priority={p} /> }))}
            onSelect={(v) => run({ patch: { priority: v as Priority } }, (count) => t("bulk.prioritised", { count, priority: priorityLabel(v as Priority).toLowerCase() }))}
            trigger={
              <Act compact={compact} icon={<Signal className="size-3.5" />} label={t("bulk.priority")} />
            }
          />
          {display.severity && (
            <Picker
              side="top"
              placeholder={t("bulk.setSeverity")}
              value={null}
              items={[
                ...severityOrder.map((v) => ({ value: v, label: severityLabel(v), hint: severityHint(v), icon: <SeverityChip severity={v} /> })),
                { value: "__none", label: t("bulk.noSeverity"), icon: <span className="h-[16px] w-[22px] rounded-[4px] border border-dashed border-line-strong" /> },
              ]}
              onSelect={(v) =>
                v === "__none"
                  ? run({ patch: { severity: null } }, (count) => t("bulk.severityCleared", { count }))
                  : run({ patch: { severity: v as Severity } }, (count) => t("bulk.severitySet", { count, severity: severityLabel(v as Severity) }))
              }
              trigger={
                <Act compact={compact} icon={<Siren className="size-3.5" />} label={t("bulk.severity")} />
              }
            />
          )}
          <Picker
            side="top"
            multi
            placeholder={i18n.t("indicators:tickets.addLabels")}
            values={tagSel}
            onChange={(v) => setTagSel(labelChoices.resolve(tagSel, v))}
            onCreate={(name) => setTagSel((s) => [...s, name])}
            createLabel="Add label"
            items={[...labelChoices.items, ...tagSel.filter((x) => !tags?.some((g) => g.name === x)).map((x) => ({ value: x, label: x }))]}
            onOpenChange={(o) => {
              if (!o && tagSel.length) {
                run({ patch: { add_tags: tagSel } }, (count) => t("bulk.tagged", { count }));
                setTagSel([]);
              }
            }}
            trigger={
              <Act compact={compact} icon={<TagIcon className="size-3.5" />} label={t("bulk.tag")} />
            }
          />
          <Act
            compact={compact}
            icon={<ShieldAlert className="size-3.5" />}
            label={spamView ? t("bulk.notSpam") : t("bulk.spam")}
            onClick={() => {
              bulk.mutate(
                { ...target, patch: { spam: !spamView } },
                {
                  onSuccess: (result) => {
                    const done = spamView ? t("bulk.unspammed", { count: allMatching ? result?.updated ?? 0 : n }) : t("bulk.spammed", { count: allMatching ? result?.updated ?? 0 : n });
                    if (allMatching) {
                      onClear();
                      toast.success(done);
                      return;
                    }
                    toast.success(done, {
                      action: { label: t("toast.undo"), onClick: () => bulk.mutate({ ids, patch: { spam: spamView } }) },
                    });
                  },
                },
              );
            }}
          />
          <Tooltip content={t("bulk.delete")}>
            <Button
              variant="ghost"
              size="sm"
              square
              aria-label={t("bulk.delete")}
              icon={<Trash2 className="size-3.5" />}
              onClick={() =>
                confirm.show(
                  allMatching ? t("bulk.deleteAllBody") : t("bulk.deleteBody", { count: n }),
                  async () => {
                    const result = await bulk.mutateAsync({ ...target, delete: true });
                    onClear();
                    toast.success(t("bulk.deleted", { count: allMatching ? result?.deleted ?? 0 : n }));
                  },
                  { title: allMatching ? t("bulk.deleteAllTitle") : t("bulk.deleteTitle", { count: n }), confirmLabel: t("bulk.delete"), danger: true },
                )
              }
            />
          </Tooltip>
          <span className="mx-1 h-5 w-px bg-line" />
          <Tooltip content={t("bulk.clear")} shortcut="esc">
            <Button variant="ghost" size="sm" square aria-label={t("bulk.clear")} icon={<X className="size-3.5" />} onClick={onClear} />
          </Tooltip>
        </motion.div>
      )}
    </AnimatePresence>
  );
}
