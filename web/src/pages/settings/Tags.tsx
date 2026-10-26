// Settings > Labels (the API's tags): coloured labels in groups, with where
// they apply, how often they're used, archive and merge.

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Archive, ArchiveRestore, ChevronRight, FolderPlus, GitMerge, MoreHorizontal, Pencil, Plus, ShieldCheck, Trash2 } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { SettingsPage, Section, Row } from "@/components/layout";
import { Badge, TagChip } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/ui/confirm";
import { EmptyState } from "@/components/ui/empty";
import { SettingsEmpty } from "@/components/settings2/SettingsEmpty";
import { SearchInput } from "@/components/ui/input";
import { Menu, MenuContent, MenuItem, MenuSeparator, MenuSub, MenuTrigger } from "@/components/ui/menu";
import { Segmented } from "@/components/ui/tabs";
import { Tooltip } from "@/components/ui/tooltip";
import { toast } from "@/components/ui/toast";
import { ListSkeleton, SummaryStrip } from "@/components/settings2/bits";
import { SortableList } from "@/components/settings2/Sortable";
import { ColorSwatchPicker } from "@/components/settings2/ColorSwatchPicker";
import { MotionList, MotionRow } from "@/components/settings2/motion";
import { GroupDialog, MergeDialog } from "@/components/settings2/labels/GroupDialog";
import { LabelDialog, scopeIcon } from "@/components/settings2/labels/LabelDialog";
import {
  SCOPES,
  totalUsage,
  useLabels,
  useLabelStructureWrites,
  useLabelWrites,
  type Label,
  type LabelCaps,
  type LabelGroup,
} from "@/components/settings2/labels/model";
import { api } from "@/lib/api/endpoints";
import type { Workspace, WorkspaceSettings } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { q, qk } from "@/lib/queries";
import { atLeast, updateWorkspace, useRole } from "@/lib/session";
import { tone as tones } from "@/lib/tones";
import { inSentence } from "@/i18n";

type Editing = { label: Label | null; group?: string | null } | null;

export default function Tags() {
  const { t } = useTranslation("settings");
  const { labels, groups, caps, isLoading } = useLabels();
  const writes = useLabelWrites(caps);
  const structure = useLabelStructureWrites();
  const role = useRole();
  const canEdit = atLeast(role, "admin");
  const confirm = useConfirm();
  const [query, setQuery] = useState("");
  const [editing, setEditing] = useState<Editing>(null);
  const [groupEditing, setGroupEditing] = useState<LabelGroup | "new" | null>(null);
  const [merging, setMerging] = useState<Label | null>(null);
  const [showArchived, setShowArchived] = useState(false);

  const term = query.trim().toLowerCase();
  const match = (l: Label) => !term || l.name.toLowerCase().includes(term) || l.description.toLowerCase().includes(term);
  const active = labels.filter((l) => !l.archived);
  const archived = labels.filter((l) => l.archived);
  const shown = active.filter(match);

  const sections = useMemo(() => {
    const out: { group: LabelGroup | null; rows: Label[] }[] = [...groups]
      .sort((a, b) => a.position - b.position)
      .map((g) => ({ group: g, rows: shown.filter((l) => l.group_id === g.id) }));
    out.push({ group: null, rows: shown.filter((l) => !l.group_id || !groups.some((g) => g.id === l.group_id)) });
    return out.filter((s) => s.group || s.rows.length || !groups.length);
  }, [groups, shown]);

  const askDelete = (l: Label) =>
    confirm.show(
      <>
        {t("labels.delete.body.before")} <TagChip name={l.name} color={l.color} className="mx-0.5 align-[-3px]" /> {t("labels.delete.body.after")}
      </>,
      () => writes.remove.mutateAsync(l.id),
      { title: t("labels.delete.title"), confirmLabel: t("labels.delete.confirm"), danger: true },
    );

  const setArchived = (l: Label, v: boolean) =>
    writes.update.mutate(
      { id: l.id, archived: v },
      { onSuccess: () => toast.success(v ? t("labels.archivedToast", { name: l.name }) : t("labels.restoredToast", { name: l.name })) },
    );

  const rowProps = {
    caps,
    canEdit,
    groups,
    onEdit: (l: Label) => setEditing({ label: l }),
    onDelete: askDelete,
    onArchive: setArchived,
    onMerge: setMerging,
    onColor: (l: Label, c: Label["color"]) => writes.update.mutate({ id: l.id, color: c }),
    onMove: (l: Label, g: string | null) => writes.update.mutate({ id: l.id, group_id: g }),
  };

  const summary: { label: string; value: number | string }[] = [
    { label: t("labels.summary.labels"), value: active.length },
    caps.groups ? { label: t("labels.summary.groups"), value: groups.length } : null,
    caps.usage ? { label: t("labels.summary.onTickets"), value: active.reduce((n, l) => n + (l.usage?.tickets ?? 0), 0).toLocaleString() } : null,
    caps.archive ? { label: t("labels.summary.archived"), value: archived.length } : null,
  ].flatMap((x) => (x ? [x] : []));

  const search = <SearchInput value={query} onChange={setQuery} placeholder={t("labels.filter")} className="w-[220px]" />;

  return (
    <SettingsPage
      title={t("labels.title")}
      description={t("labels.description")}
      actions={
        canEdit && (
          <>
            {caps.groups && (
              <Button icon={<FolderPlus className="size-3.5" />} onClick={() => setGroupEditing("new")}>
                {t("labels.newGroup")}
              </Button>
            )}
            <Button variant="primary" icon={<Plus className="size-3.5" />} onClick={() => setEditing({ label: null })}>
              {t("labels.newLabel")}
            </Button>
          </>
        )
      }
    >
      {summary.length > 1 && <SummaryStrip items={summary} className="-mb-3" />}

      {isLoading ? (
        <Section flush title={t("labels.title")}>
          <ListSkeleton rows={6} />
        </Section>
      ) : !active.length ? (
        <Section flush>
          <SettingsEmpty
            art="settings"
            title={t("labels.empty.title")}
            description={t("labels.empty.description")}
            action={
              canEdit && (
                <Button variant="primary" icon={<Plus className="size-3.5" />} onClick={() => setEditing({ label: null })}>
                  {t("labels.newLabel")}
                </Button>
              )
            }
            tips={[
              { label: t("labels.empty.tips.groups"), icon: <FolderPlus /> },
              { label: t("labels.empty.tips.who"), icon: <ShieldCheck /> },
            ]}
          />
        </Section>
      ) : (
        <>
          {sections.map(({ group, rows }, i) => (
            <Section
              key={group?.id ?? "none"}
              flush
              title={
                group ? (
                  <span className="flex items-center gap-2">
                    {group.color && <span className={cn("size-2 rounded-xs", tones[group.color].solid)} aria-hidden />}
                    {group.name}
                    {group.exclusive && (
                      <Tooltip content={t("labels.group.exclusiveHint")}>
                        <span tabIndex={0}>
                          <Badge tone="outline" className="h-[18px] font-normal">
                            {t("labels.group.exclusiveBadge")}
                          </Badge>
                        </span>
                      </Tooltip>
                    )}
                    <span className="mono text-[11px] font-normal tabular-nums text-fg-4">{rows.length}</span>
                  </span>
                ) : (
                  <span className="flex items-center gap-2">
                    {groups.length ? t("labels.ungrouped") : t("labels.count", { count: shown.length })}
                  </span>
                )
              }
              actions={
                <>
                  {i === 0 && search}
                  {group && canEdit && <GroupMenu group={group} onEdit={() => setGroupEditing(group)} onDelete={() => structure.removeGroup(group.id)} onAdd={() => setEditing({ label: null, group: group.id })} />}
                </>
              }
            >
              {caps.usage && rows.length > 0 && <ColumnHead caps={caps} />}
              {rows.length ? (
                caps.reorder && canEdit && !term ? (
                  <SortableList
                    items={rows}
                    onReorder={(ids) => void structure.reorder(ids)}
                    handleLabel={(l) => t("labels.reorderAria", { name: l.name })}
                    renderRow={(l, handle) => <LabelRow label={l} {...rowProps} handle={handle} />}
                  />
                ) : (
                  <MotionList>
                    {rows.map((l) => (
                      <MotionRow key={l.id}>
                        <LabelRow label={l} {...rowProps} />
                      </MotionRow>
                    ))}
                  </MotionList>
                )
              ) : term ? (
                <EmptyState compact lit={2} title={t("labels.noMatch.title")} description={t("labels.noMatch.description")} />
              ) : (
                <EmptyState compact lit={2} title={t("labels.emptyGroup")} />
              )}
            </Section>
          ))}
        </>
      )}

      {caps.archive && archived.length > 0 && (
        <section className="animate-rise-in">
          <button
            type="button"
            aria-expanded={showArchived}
            onClick={() => setShowArchived((v) => !v)}
            className="flex items-center gap-1.5 rounded-md text-[13px] font-semibold text-fg-2 hover:text-fg"
          >
            <ChevronRight className={cn("size-3.5 text-fg-4 transition-transform duration-150", showArchived && "rotate-90")} />
            {t("labels.archivedSection", { count: archived.length })}
          </button>
          {showArchived && (
            <div className="mt-3 overflow-hidden rounded-lg border border-line bg-surface shadow-xs">
              <MotionList>
                {archived.filter(match).map((l) => (
                  <MotionRow key={l.id}>
                    <LabelRow label={l} {...rowProps} />
                  </MotionRow>
                ))}
              </MotionList>
            </div>
          )}
        </section>
      )}

      {caps.colors && canEdit && <CreationRule />}

      {editing && (
        <LabelDialog
          label={editing.label}
          caps={caps}
          groups={groups}
          existing={labels}
          defaultGroup={editing.group}
          onClose={() => setEditing(null)}
          onSave={async (v) => {
            if (editing.label) {
              await writes.update.mutateAsync({ id: editing.label.id, ...v });
              toast.success(t("labels.savedToast"));
            } else {
              const made = await writes.create.mutateAsync(v);
              toast.success(t("labels.createdToast", { name: made.name }));
            }
          }}
        />
      )}
      {groupEditing && (
        <GroupDialog group={groupEditing === "new" ? null : groupEditing} onClose={() => setGroupEditing(null)} onSave={(v) => structure.saveGroup(groupEditing === "new" ? null : groupEditing.id, v)} />
      )}
      {merging && <MergeDialog label={merging} labels={labels} onClose={() => setMerging(null)} onMerge={(into) => structure.merge(merging.id, into)} />}
    </SettingsPage>
  );
}

function ColumnHead({ caps }: { caps: LabelCaps }) {
  const { t } = useTranslation("settings");
  return (
    <div className="flex h-8 items-center gap-3 border-b border-line-soft bg-sunken px-4 text-fg-4">
      <span className="label-caps flex-1">{t("labels.columns.label")}</span>
      {caps.scope && <span className="label-caps hidden w-[76px] md:block">{t("labels.columns.appliesTo")}</span>}
      <span className="label-caps w-14 text-right">{t("labels.columns.uses")}</span>
      <span className="w-7" />
    </div>
  );
}

type RowProps = {
  caps: LabelCaps;
  canEdit: boolean;
  groups: LabelGroup[];
  onEdit: (l: Label) => void;
  onDelete: (l: Label) => void;
  onArchive: (l: Label, v: boolean) => void;
  onMerge: (l: Label) => void;
  onColor: (l: Label, c: Label["color"]) => void;
  onMove: (l: Label, group: string | null) => void;
};

function LabelRow({ label: l, caps, canEdit, groups, onEdit, onDelete, onArchive, onMerge, onColor, onMove, handle }: RowProps & { label: Label; handle?: React.ReactNode }) {
  const { t } = useTranslation("settings");
  return (
    <div
      role={canEdit ? "button" : undefined}
      tabIndex={canEdit ? 0 : undefined}
      aria-label={canEdit ? t("labels.editAria", { name: l.name }) : undefined}
      onClick={canEdit ? () => onEdit(l) : undefined}
      onKeyDown={(e) => {
        if (canEdit && e.key === "Enter" && e.target === e.currentTarget) onEdit(l);
      }}
      className={cn(
        "group flex min-h-11 items-center gap-3 bg-surface px-4 py-2 outline-none focus-visible:bg-hover",
        canEdit && "cursor-pointer transition-colors hover:bg-sunken",
        l.archived && "opacity-70",
      )}
    >
      {handle}
      <div className="flex min-w-0 flex-1 items-center gap-3">
        <TagChip name={l.name} color={l.color} className="max-w-[220px] text-[11.5px]" />
        {l.description && <span className="min-w-0 truncate text-[12px] text-fg-3">{l.description}</span>}
      </div>
      {caps.scope && (
        <span className="hidden w-[76px] items-center gap-1.5 text-fg-4 md:flex [&_svg]:size-3.5">
          {SCOPES.map((s) => (
            <Tooltip key={s} content={l.applies_to.includes(s) ? t(`labels.scope.${s}`) : t("labels.scope.not", { what: inSentence(t(`labels.scope.${s}`)) })}>
              <span tabIndex={-1} className={cn("flex", !l.applies_to.includes(s) && "opacity-25")}>
                {scopeIcon[s]}
              </span>
            </Tooltip>
          ))}
        </span>
      )}
      {caps.usage && (
        <Tooltip content={l.usage ? t("labels.usageHint", { tickets: l.usage.tickets, contacts: l.usage.contacts, companies: l.usage.companies }) : ""}>
          <span className="mono w-14 text-right text-[11.5px] tabular-nums text-fg-3">{totalUsage(l).toLocaleString()}</span>
        </Tooltip>
      )}
      <div className="w-7">
        {canEdit && (
          <Menu>
            <MenuTrigger asChild>
              <Button
                size="sm"
                variant="ghost"
                square
                aria-label={t("labels.actionsAria", { name: l.name })}
                onClick={(e) => e.stopPropagation()}
                className="opacity-100 data-[state=open]:opacity-100 md:opacity-0 md:group-hover:opacity-100 md:focus-visible:opacity-100"
              >
                <MoreHorizontal className="size-4" />
              </Button>
            </MenuTrigger>
            <MenuContent>
              <MenuItem icon={<Pencil />} onSelect={() => onEdit(l)}>
                {t("labels.menu.edit")}
              </MenuItem>
              {caps.colors && (
                <MenuSub label={t("labels.menu.colour")} icon={<span className={cn("!size-3 rounded-xs", tones[l.color].solid)} />}>
                  <div className="p-1.5" onKeyDown={(e) => e.stopPropagation()}>
                    <ColorSwatchPicker value={l.color} onChange={(c) => onColor(l, c)} size="sm" className="w-[124px]" />
                  </div>
                </MenuSub>
              )}
              {caps.groups && groups.length > 0 && (
                <MenuSub label={t("labels.menu.moveTo")}>
                  <MenuItem onSelect={() => onMove(l, null)}>{t("labels.dialog.noGroup")}</MenuItem>
                  {groups.map((g) => (
                    <MenuItem key={g.id} icon={<span className={cn("!size-2 rounded-xs", tones[g.color ?? "gray"].solid)} />} onSelect={() => onMove(l, g.id)}>
                      {g.name}
                    </MenuItem>
                  ))}
                </MenuSub>
              )}
              {caps.merge && (
                <MenuItem icon={<GitMerge />} onSelect={() => onMerge(l)}>
                  {t("labels.menu.merge")}
                </MenuItem>
              )}
              {caps.archive && (
                <MenuItem icon={l.archived ? <ArchiveRestore /> : <Archive />} onSelect={() => onArchive(l, !l.archived)}>
                  {l.archived ? t("labels.menu.restore") : t("labels.menu.archive")}
                </MenuItem>
              )}
              <MenuSeparator />
              <MenuItem danger icon={<Trash2 />} onSelect={() => onDelete(l)}>
                {t("labels.menu.delete")}
              </MenuItem>
            </MenuContent>
          </Menu>
        )}
      </div>
    </div>
  );
}

function GroupMenu({ group, onEdit, onDelete, onAdd }: { group: LabelGroup; onEdit: () => void; onDelete: () => Promise<unknown>; onAdd: () => void }) {
  const { t } = useTranslation("settings");
  const confirm = useConfirm();
  return (
    <Menu>
      <MenuTrigger asChild>
        <Button size="sm" variant="ghost" square aria-label={t("labels.group.actionsAria", { name: group.name })}>
          <MoreHorizontal className="size-4" />
        </Button>
      </MenuTrigger>
      <MenuContent>
        <MenuItem icon={<Plus />} onSelect={onAdd}>
          {t("labels.group.addLabel")}
        </MenuItem>
        <MenuItem icon={<Pencil />} onSelect={onEdit}>
          {t("labels.group.edit")}
        </MenuItem>
        <MenuSeparator />
        <MenuItem
          danger
          icon={<Trash2 />}
          onSelect={() =>
            confirm.show(t("labels.group.deleteBody", { name: group.name }), () => onDelete(), {
              title: t("labels.group.deleteTitle"),
              confirmLabel: t("labels.group.deleteConfirm"),
              danger: true,
            })
          }
        >
          {t("labels.group.delete")}
        </MenuItem>
      </MenuContent>
    </Menu>
  );
}

/** Who may create a label by typing a new name: everyone, or admins only. */
function CreationRule() {
  const { t } = useTranslation("settings");
  const qc = useQueryClient();
  const { data: ws } = useQuery(q.workspace());
  type Rule = "anyone" | "admins";
  const current = ((ws?.settings as WorkspaceSettings & { label_creation?: Rule }) ?? {}).label_creation ?? "anyone";
  const save = async (v: Rule) => {
    if (!ws) return;
    const prev = qc.getQueryData<Workspace>(qk.workspace);
    const next = { ...ws, settings: { ...ws.settings, label_creation: v } };
    qc.setQueryData(qk.workspace, next);
    updateWorkspace(next);
    try {
      const settings: WorkspaceSettings = next.settings;
      const w = await api.workspaces.update({ settings });
      qc.setQueryData(qk.workspace, w);
      toast.success(t("labels.creation.saved"));
    } catch (e) {
      if (prev) {
        qc.setQueryData(qk.workspace, prev);
        updateWorkspace(prev);
      }
      toast.error(e);
    }
  };
  return (
    <Section title={t("labels.creation.title")}>
      <Row label={t("labels.creation.label")} description={current === "admins" ? t("labels.creation.adminsHint") : t("labels.creation.anyoneHint")}>
        <Segmented
          value={current}
          onChange={(v) => void save(v)}
          options={[
            { value: "anyone", label: t("labels.creation.anyone") },
            { value: "admins", label: t("labels.creation.admins") },
          ]}
        />
      </Row>
    </Section>
  );
}
