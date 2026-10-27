import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { ArrowUpRight, AtSign, Building2, Globe, Inbox, Layers, Lock, MoreHorizontal, Pencil, Plus, Search, Tag, Trash2, UserRound, Users, UsersRound, X } from "lucide-react";
import { useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { SettingsPage, Section, ListRow } from "@/components/layout";
import { Button } from "@/components/ui/button";
import { Drawer } from "@/components/ui/dialog";
import { Field, TextInput } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { SettingsEmpty } from "@/components/settings2/SettingsEmpty";
import { Menu, MenuContent, MenuItem, MenuSeparator, MenuTrigger } from "@/components/ui/menu";
import { useConfirm } from "@/components/ui/confirm";
import { toast } from "@/components/ui/toast";
import { IconTile, ListSkeleton } from "@/components/settings2/bits";
import { Avatar } from "@/components/ui/avatar";
import { TagChip } from "@/components/ui/badge";
import { Tooltip } from "@/components/ui/tooltip";
import { orderBy, useListMutation } from "@/components/settings2/optimistic";
import { SortableList } from "@/components/settings2/Sortable";
import { TeamChip, TeamPick, useTeams } from "@/components/settings2/teams";
import { Segmented } from "@/components/ui/tabs";
import { viewAudience, viewAudienceHint, viewDeleteBody } from "@/components/settings2/viewAudience";
import { cleanFilters, clearField, fieldOps, filterOp, hasFilter, joinList, splitList, withOp, type FilterOp, type OpField } from "@/components/settings2/viewFilters";
import {
  CompaniesPick,
  ContactsPick,
  MultiPick,
  SinglePick,
  channelItems,
  priorityItems,
  severityFilterItems,
  statusItems,
  useCompanyItems,
  useContactItems,
  useMailboxItems,
  useMemberItems,
  useTagItems,
  useTeamItems,
} from "@/components/settings2/pickers";
import { ChannelIcon, PriorityIcon, SeverityChip, StatusIcon, channelLabel, priorityLabel, severityLabel, statusLabel } from "@/components/tickets/bits";
import { api } from "@/lib/api/endpoints";
import { errorMessage } from "@/lib/api/client";
import type { Channel, Priority, Severity, TicketFilters, TicketStatus, View } from "@/lib/api/types";
import { q, qk, upsertInList } from "@/lib/queries";
import { atLeast, useRole } from "@/lib/session";
import { cn } from "@/lib/cn";

const chip =
  "inline-flex h-5 max-w-[220px] shrink-0 items-center gap-1 rounded-sm bg-sunken px-1.5 text-[11px] text-fg-2 ring-1 ring-inset ring-line-soft [&_svg]:size-3 [&_svg]:shrink-0";

/** A view's filters as a row of small chips, each with its indicator. */
function FilterChips({ f }: { f: TicketFilters }) {
  const { t } = useTranslation("settingsInbox");
  useTranslation("indicators");
  const members = useQuery(q.members()).data;
  const teams = useQuery(q.teams()).data;
  const mailboxes = useQuery(q.mailboxes()).data;
  const tags = useQuery(q.tags()).data;
  const contacts = useContactItems(splitList(f.contact_not));
  const companies = useCompanyItems(splitList(f.company_not));
  const or = (xs: string[]) => xs.join(" / ");
  const out: ReactNode[] = [];
  if (f.status?.length)
    out.push(
      <span key="s" className={chip}>
        {f.status.map((s) => (
          <StatusIcon key={s} status={s} />
        ))}
        <span className="truncate">{or(f.status.map((s) => statusLabel(s)))}</span>
      </span>,
    );
  if (f.priority?.length)
    out.push(
      <span key="p" className={chip}>
        {f.priority.map((p) => (
          <PriorityIcon key={p} priority={p} />
        ))}
        <span className="truncate">{or(f.priority.map((p) => priorityLabel(p)))}</span>
      </span>,
    );
  if (f.severity?.length)
    out.push(
      <span key="sev" className={chip}>
        {f.severity.map((s) => (s === "none" ? null : <SeverityChip key={s} severity={s} />))}
        <span className="truncate">{or(f.severity.map((s) => (s === "none" ? t("views.noSeverity") : severityLabel(s))))}</span>
      </span>,
    );
  if (f.assignee) {
    const m = members?.find((x) => x.user.id === f.assignee);
    out.push(
      <span key="a" className={chip}>
        {m ? <Avatar name={m.user.name || m.user.email} src={m.user.avatar_url} size="xs" className="!size-3.5" /> : <UserRound />}
        <span className="truncate">
          {f.assignee === "me"
            ? t("views.assignedToViewer")
            : f.assignee === "none"
              ? t("views.unassigned")
              : t("views.assignedTo", {
                  name: m?.user.name ?? t("views.aTeammate"),
                })}
        </span>
      </span>,
    );
  }
  const teamNames = (list: string) =>
    or(
      splitList(list).map((id) =>
        id === "mine" ? t("views.myTeams") : id === "none" ? t("views.noTeam") : (teams?.find((x) => x.id === id)?.name ?? t("views.unknown")),
      ),
    );
  const mailboxNames = (list: string) => or(splitList(list).map((id) => mailboxes?.find((x) => x.id === id)?.email ?? t("views.aMailbox")));
  if (f.team_id)
    out.push(
      <span key="t" className={chip}>
        <Users />
        <span className="truncate">{t("views.teamNamed", { name: teamNames(f.team_id) })}</span>
      </span>,
    );
  if (f.team_not)
    out.push(
      <span key="tn" className={chip}>
        <Users />
        <span className="truncate">{t("views.notTeamNamed", { name: teamNames(f.team_not) })}</span>
      </span>,
    );
  if (f.mailbox_id)
    out.push(
      <span key="m" className={chip}>
        <Inbox />
        <span className="truncate">{t("views.inMailbox", { name: mailboxNames(f.mailbox_id) })}</span>
      </span>,
    );
  if (f.mailbox_not)
    out.push(
      <span key="mn" className={chip}>
        <Inbox />
        <span className="truncate">{t("views.notInMailbox", { name: mailboxNames(f.mailbox_not) })}</span>
      </span>,
    );
  if (f.received_at)
    out.push(
      <span key="r" className={chip}>
        <AtSign />
        <span className="truncate">{t("views.writtenTo", { address: f.received_at })}</span>
      </span>,
    );
  if (contacts.length)
    out.push(
      <span key="ctn" className={chip}>
        <UserRound />
        <span className="truncate">{t("views.notContact", { name: or(contacts.map((c) => c.label)) })}</span>
      </span>,
    );
  if (companies.length)
    out.push(
      <span key="cpn" className={chip}>
        <Building2 />
        <span className="truncate">{t("views.notCompany", { name: or(companies.map((c) => c.label)) })}</span>
      </span>,
    );
  const tagColor = (name: string) => tags?.find((x) => x.name.toLowerCase() === name.toLowerCase())?.color;
  for (const name of splitList(f.tag)) out.push(<TagChip key={`g:${name}`} name={name} color={tagColor(name)} className="h-5" />);
  if (f.tag_not)
    out.push(
      <span key="gn" className={chip}>
        <Tag />
        <span className="truncate">{t("views.withoutLabels", { labels: splitList(f.tag_not).join(", ") })}</span>
      </span>,
    );
  if (f.untagged)
    out.push(
      <span key="gu" className={chip}>
        <Tag />
        {t("views.noLabel")}
      </span>,
    );
  if (f.channel)
    out.push(
      <span key="c" className={chip}>
        <ChannelIcon channel={f.channel} />
        {channelLabel(f.channel)}
      </span>,
    );
  if (f.channel_not?.length)
    out.push(
      <span key="cn" className={chip}>
        {f.channel_not.map((c) => (
          <ChannelIcon key={c} channel={c} />
        ))}
        <span className="truncate">{t("views.notChannel", { channel: or(f.channel_not.map((c) => channelLabel(c))) })}</span>
      </span>,
    );
  if (f.q)
    out.push(
      <span key="q" className={chip}>
        <Search />
        <span className="truncate">{t("views.matching", { q: f.q })}</span>
      </span>,
    );
  if (!out.length)
    out.push(
      <span key="all" className={chip}>
        {t("views.allTickets")}
      </span>,
    );
  return <div className="mt-1.5 flex flex-wrap items-center gap-1">{out}</div>;
}

export default function Views() {
  const { data: views, isLoading } = useQuery(q.views());
  const role = useRole();
  const canEdit = atLeast(role, "agent");
  const { t } = useTranslation("settingsInbox");
  const confirm = useConfirm();
  const [editing, setEditing] = useState<View | "new" | null>(null);
  const navigate = useNavigate();

  const sorted = useMemo(() => [...(views ?? [])].sort((a, b) => a.position - b.position), [views]);
  const { teams } = useTeams();
  const teamById = new Map(teams.map((x) => [x.id, x]));

  const toggleShared = useListMutation<View, { id: string; shared: boolean }>(
    qk.views,
    (v) => api.views.update(v.id, { shared: v.shared }),
    (rows, v) => rows.map((r) => (r.id === v.id ? { ...r, shared: v.shared } : r)),
    { errorText: t("views.updateFailed") },
  );
  const reorder = useListMutation<View, string[]>(
    qk.views,
    async (ids) => {
      const next = orderBy(sorted, ids);
      const changed = next.filter((n) => sorted.find((o) => o.id === n.id)?.position !== n.position);
      await Promise.all(changed.map((n) => api.views.update(n.id, { position: n.position })));
    },
    (rows, ids) => orderBy(rows, ids),
    { errorText: t("views.reorderFailed") },
  );
  const remove = useListMutation<View, string>(
    qk.views,
    (id) => api.views.remove(id),
    (rows, id) => rows.filter((r) => r.id !== id),
    {
      errorText: t("views.deleteFailed"),
    },
  );

  const newButton = (size?: "sm") =>
    canEdit && (
      <Button variant={size ? "secondary" : "primary"} size={size} icon={<Plus className="size-3.5" />} onClick={() => setEditing("new")}>
        {t("views.new")}
      </Button>
    );

  return (
    <SettingsPage title={t("views.title")} description={t("views.description")} actions={newButton()}>
      <Section flush title={views ? t("views.count", { count: sorted.length }) : undefined}>
        {isLoading ? (
          <ListSkeleton rows={3} />
        ) : !sorted.length ? (
          <SettingsEmpty
            art="views"
            title={t("views.emptyTitle")}
            description={t("views.emptyHint")}
            action={newButton()}
            tips={[
              { label: t("views.emptyTips.sidebar"), icon: <Globe /> },
              { label: t("views.emptyTips.personal"), icon: <Lock /> },
            ]}
          />
        ) : (
          <SortableList
            items={sorted}
            disabled={!canEdit}
            onReorder={(ids) => reorder.mutate(ids)}
            handleLabel={(v) => t("views.reorderAria", { name: v.name })}
            renderRow={(v, handle) => (
                <ListRow onClick={canEdit ? () => setEditing(v) : undefined} className="items-start py-3">
                  {handle}
                  <IconTile tone={v.shared ? "blue" : "gray"} className="mt-0.5">
                    <Layers />
                  </IconTile>
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <span className="truncate text-[13px] font-medium">{v.name}</span>
                      {v.team_id && teamById.get(v.team_id) ? (
                        <TeamChip team={teamById.get(v.team_id)!} size="xs" />
                      ) : (
                        <Tooltip content={viewAudienceHint(t, viewAudience(v))}>
                          <span className="inline-flex items-center gap-1 text-[11px] text-fg-4">
                            {v.shared ? <Globe className="size-3" /> : <Lock className="size-3" />}
                            {v.shared ? t("views.shared") : t("views.personal")}
                          </span>
                        </Tooltip>
                      )}
                    </div>
                    <FilterChips f={v.filters} />
                  </div>
                  {canEdit && (
                    <div className="flex items-center gap-1 self-center">
                      <span className={cn("mr-1 hidden", !v.team_id && "sm:flex")} onClick={(e) => e.stopPropagation()}>
                        <Switch
                          checked={v.shared}
                          onCheckedChange={(shared) => toggleShared.mutate({ id: v.id, shared })}
                          aria-label={t("views.showToEveryone", {
                            name: v.name,
                          })}
                        />
                      </span>
                      <Menu>
                        <MenuTrigger asChild>
                          <button
                            type="button"
                            aria-label={t("common.actionsFor", {
                              name: v.name,
                            })}
                            onClick={(e) => e.stopPropagation()}
                            className="flex size-7 items-center justify-center rounded-md text-fg-3 hover:bg-hover hover:text-fg"
                          >
                            <MoreHorizontal className="size-4" />
                          </button>
                        </MenuTrigger>
                        <MenuContent>
                          <MenuItem icon={<Pencil />} onSelect={() => setEditing(v)}>
                            {t("common.edit")}
                          </MenuItem>
                          <MenuItem
                            icon={<ArrowUpRight />}
                            onSelect={() =>
                              void navigate({
                                to: "/views/$viewId/{-$ticketId}",
                                params: { viewId: v.id, ticketId: undefined },
                              })
                            }
                          >
                            {t("views.open")}
                          </MenuItem>
                          <MenuSeparator />
                          <MenuItem
                            danger
                            icon={<Trash2 />}
                            onSelect={() =>
                              confirm.show(
                                <>
                                  <span className="font-medium text-fg">{v.name}</span> {viewDeleteBody(t, viewAudience(v), v.team_id ? teamById.get(v.team_id)?.name : null)}
                                </>,
                                () => remove.mutateAsync(v.id),
                                {
                                  title: t("views.deleteTitle"),
                                  confirmLabel: t("views.deleteConfirm"),
                                  danger: true,
                                },
                              )
                            }
                          >
                            {t("common.delete")}
                          </MenuItem>
                        </MenuContent>
                      </Menu>
                    </div>
                  )}
                </ListRow>
            )}
          />
        )}
      </Section>
      {editing && <ViewDrawer view={editing === "new" ? null : editing} nextPosition={sorted.length} onClose={() => setEditing(null)} />}
    </SettingsPage>
  );
}

function ViewDrawer({ view, nextPosition, onClose }: { view: View | null; nextPosition: number; onClose: () => void }) {
  const { t } = useTranslation("settingsInbox");
  useTranslation("indicators");
  const qc = useQueryClient();
  const confirm = useConfirm();
  const [name, setName] = useState(view?.name ?? "");
  const [shared, setShared] = useState(view?.shared ?? true);
  const [teamId, setTeamId] = useState<string | null>(view?.team_id ?? null);
  const { teams: allTeams } = useTeams();
  const { t: tt } = useTranslation("settingsTeams");
  const [f, setF] = useState<TicketFilters>(view?.filters ?? { status: ["open"] });
  // The operator each row shows; it stays put while the row has no value yet.
  const [ops, setOps] = useState<Record<OpField, FilterOp>>(() => {
    const init = view?.filters ?? {};
    return { tag: filterOp(init, "tag"), team: filterOp(init, "team"), mailbox: filterOp(init, "mailbox"), channel: filterOp(init, "channel") };
  });
  const [error, setError] = useState<string | null>(null);
  const members = useMemberItems();
  const teams = useTeamItems();
  const mailboxes = useMailboxItems();
  const tagItems = useTagItems();
  // Stored names match labels in any case; show them as the label is spelled.
  const tagValues = (list: string | undefined) => splitList(list).map((n) => tagItems.find((i) => i.value.toLowerCase() === n.toLowerCase())?.value ?? n);
  // Every address tickets can be written to: each mailbox's own and its aliases.
  const mailboxRows = useQuery(q.mailboxes()).data;
  const addresses = useMemo(
    () => (mailboxRows ?? []).flatMap((m) => [m.email, ...(m.aliases ?? [])].map((a) => ({ value: a.toLowerCase(), label: a.toLowerCase(), group: m.display_name || m.name }))),
    [mailboxRows],
  );

  const initial = JSON.stringify({
    name: view?.name ?? "",
    shared: view?.shared ?? true,
    teamId: view?.team_id ?? null,
    f: view?.filters ?? { status: ["open"] },
  });
  const dirty = JSON.stringify({ name, shared, teamId, f }) !== initial;
  const audience = viewAudience({ shared, team_id: teamId });

  const requestClose = () => {
    if (!dirty) return onClose();
    confirm.show(t("views.discardBody"), onClose, {
      title: t("common.discardTitle"),
      confirmLabel: t("common.discard"),
      danger: true,
    });
  };

  const save = useMutation({
    mutationFn: () => {
      const filters = cleanFilters(f);
      return view
        ? api.views.update(view.id, { name: name.trim(), shared: teamId ? false : shared, team_id: teamId, filters })
        : api.views.create({
            name: name.trim(),
            shared: teamId ? false : shared,
            team_id: teamId,
            filters,
            position: nextPosition,
          });
    },
    onSuccess: (v) => {
      upsertInList(qc, qk.views, v);
      void qc.invalidateQueries({ queryKey: qk.counts });
      toast.success(view ? t("views.saved") : t("views.created"));
      onClose();
    },
    onError: (e) => setError(errorMessage(e)),
  });

  const set = <K extends keyof TicketFilters>(k: K, v: TicketFilters[K] | undefined) => setF((cur) => ({ ...cur, [k]: v }));
  const setOp = (field: OpField, op: FilterOp) => {
    setOps((cur) => ({ ...cur, [field]: op }));
    setF((cur) => withOp(cur, field, op));
  };
  const clearRow = (field: OpField) => () => {
    setOps((cur) => ({ ...cur, [field]: "is" }));
    setF((cur) => clearField(cur, field));
  };
  const opPick = (field: OpField) => <OpSelect field={field} value={ops[field]} onChange={(op) => setOp(field, op)} />;

  const assigneeItems = [
    { value: "me", label: t("views.assigneeViewer"), icon: <UserRound /> },
    {
      value: "none",
      label: t("views.unassigned"),
      icon: <span className="size-3.5 rounded-full border border-dashed border-fg-4" />,
    },
    ...members,
  ];

  return (
    <Drawer
      open
      onOpenChange={(o) => !o && requestClose()}
      title={view ? t("views.editTitle") : t("views.new")}
      width={480}
      onEscapeKeyDown={(e) => {
        if (dirty) {
          e.preventDefault();
          requestClose();
        }
      }}
      onInteractOutside={(e) => {
        e.preventDefault();
        requestClose();
      }}
    >
      <form
        className="flex min-h-full flex-col"
        onSubmit={(e) => {
          e.preventDefault();
          setError(null);
          if (!name.trim()) return setError(t("views.needName"));
          save.mutate();
        }}
      >
        <div className="flex flex-1 flex-col gap-5 p-5">
          <Field label={t("views.nameLabel")}>
            {(p) => (
              <TextInput {...p} autoFocus value={name} maxLength={60} placeholder={t("views.namePlaceholder")} onChange={(e) => setName(e.target.value)} />
            )}
          </Field>
          {allTeams.length > 0 || teamId ? (
            <div className="flex flex-col gap-2.5 rounded-lg border border-line px-3 py-2.5">
              <div className="flex items-center justify-between gap-3">
                <div className="text-[13px] font-medium">{tt("scoped.viewShare")}</div>
                <Segmented
                  size="xs"
                  value={audience}
                  onChange={(v) => {
                    if (v === "team") {
                      setTeamId(teamId ?? allTeams[0]?.id ?? null);
                      setShared(false);
                    } else {
                      setTeamId(null);
                      setShared(v === "everyone");
                    }
                  }}
                  options={[
                    { value: "me", label: tt("scoped.viewMe"), icon: <Lock /> },
                    { value: "team", label: tt("scoped.viewTeam"), icon: <UsersRound /> },
                    { value: "everyone", label: tt("scoped.viewEveryone"), icon: <Globe /> },
                  ]}
                />
              </div>
              {audience === "team" && <TeamPick className="w-full" value={teamId} onChange={(v) => setTeamId(v)} />}
              <div className="text-[12px] text-fg-3">{viewAudienceHint(t, audience, allTeams.find((x) => x.id === teamId)?.name)}</div>
            </div>
          ) : (
            <div className="flex items-center gap-3 rounded-lg border border-line px-3 py-2.5">
              <IconTile tone={shared ? "blue" : "gray"} size="sm">
                {shared ? <Globe /> : <Lock />}
              </IconTile>
              <div className="min-w-0 flex-1">
                <div className="text-[13px] font-medium">{t("views.visibleTitle")}</div>
                <div className="text-[12px] text-fg-3">{viewAudienceHint(t, audience)}</div>
              </div>
              <Switch checked={shared} onCheckedChange={setShared} aria-label={t("views.visibleTitle")} />
            </div>
          )}

          <div>
            <div className="label-caps mb-2">{t("views.showWhere")}</div>
            <div className="flex flex-col rounded-lg border border-line bg-surface">
              <FilterRow label={t("views.filters.status")} onClear={f.status?.length ? () => set("status", undefined) : undefined}>
                <MultiPick
                  items={statusItems()}
                  values={f.status ?? []}
                  onChange={(v) => set("status", v as TicketStatus[])}
                  placeholder={t("views.any.status")}
                />
              </FilterRow>
              <FilterRow label={t("views.filters.priority")} onClear={f.priority?.length ? () => set("priority", undefined) : undefined}>
                <MultiPick
                  items={priorityItems()}
                  values={f.priority ?? []}
                  onChange={(v) => set("priority", v as Priority[])}
                  placeholder={t("views.any.priority")}
                />
              </FilterRow>
              <FilterRow label={t("views.filters.severity")} onClear={f.severity?.length ? () => set("severity", undefined) : undefined}>
                <MultiPick
                  items={severityFilterItems()}
                  values={f.severity ?? []}
                  onChange={(v) => set("severity", v as (Severity | "none")[])}
                  placeholder={t("views.any.severity")}
                />
              </FilterRow>
              <FilterRow label={t("views.filters.assignee")} onClear={f.assignee ? () => set("assignee", undefined) : undefined}>
                <SinglePick items={assigneeItems} value={f.assignee} onChange={(v) => set("assignee", v)} placeholder={t("views.any.assignee")} width={280} />
              </FilterRow>
              <FilterRow label={t("views.filters.team")} onClear={hasFilter(f, "team") ? clearRow("team") : undefined}>
                {opPick("team")}
                {ops.team === "not" ? (
                  <MultiPick items={teams} values={splitList(f.team_not)} onChange={(v) => set("team_not", joinList(v))} placeholder={t("views.choose.team")} />
                ) : (
                  <MultiPick items={teams} values={splitList(f.team_id)} onChange={(v) => set("team_id", joinList(v))} placeholder={t("views.any.team")} />
                )}
              </FilterRow>
              <FilterRow label={t("views.filters.mailbox")} onClear={hasFilter(f, "mailbox") ? clearRow("mailbox") : undefined}>
                {opPick("mailbox")}
                {ops.mailbox === "not" ? (
                  <MultiPick items={mailboxes} values={splitList(f.mailbox_not)} onChange={(v) => set("mailbox_not", joinList(v))} placeholder={t("views.choose.mailbox")} />
                ) : (
                  <SinglePick items={mailboxes} value={f.mailbox_id} onChange={(v) => set("mailbox_id", v)} placeholder={t("views.any.mailbox")} width={300} />
                )}
              </FilterRow>
              <FilterRow label={t("views.filters.writtenTo")} onClear={f.received_at ? () => set("received_at", undefined) : undefined}>
                <SinglePick items={addresses} value={f.received_at} onChange={(v) => set("received_at", v)} placeholder={t("views.any.writtenTo")} width={300} />
              </FilterRow>
              <FilterRow label={t("views.filters.contact")} onClear={f.contact_not ? () => set("contact_not", undefined) : undefined}>
                <NotOp />
                <ContactsPick values={splitList(f.contact_not)} onChange={(v) => set("contact_not", joinList(v))} placeholder={t("views.choose.contact")} />
              </FilterRow>
              <FilterRow label={t("views.filters.company")} onClear={f.company_not ? () => set("company_not", undefined) : undefined}>
                <NotOp />
                <CompaniesPick values={splitList(f.company_not)} onChange={(v) => set("company_not", joinList(v))} placeholder={t("views.choose.company")} />
              </FilterRow>
              <FilterRow label={t("views.filters.tag")} onClear={hasFilter(f, "tag") ? clearRow("tag") : undefined}>
                {opPick("tag")}
                {ops.tag === "not" ? (
                  <MultiPick items={tagItems} values={tagValues(f.tag_not)} onChange={(v) => set("tag_not", joinList(v))} placeholder={t("views.choose.tag")} />
                ) : ops.tag === "is" ? (
                  <MultiPick items={tagItems} values={tagValues(f.tag)} onChange={(v) => set("tag", joinList(v))} placeholder={t("views.any.tag")} />
                ) : null}
              </FilterRow>
              <FilterRow label={t("views.filters.channel")} onClear={hasFilter(f, "channel") ? clearRow("channel") : undefined}>
                {opPick("channel")}
                {ops.channel === "not" ? (
                  <MultiPick
                    items={channelItems()}
                    values={f.channel_not ?? []}
                    onChange={(v) => set("channel_not", v.length ? (v as Channel[]) : undefined)}
                    placeholder={t("views.choose.channel")}
                  />
                ) : (
                  <SinglePick items={channelItems()} value={f.channel} onChange={(v) => set("channel", v as Channel)} placeholder={t("views.any.channel")} />
                )}
              </FilterRow>
              <FilterRow label={t("views.filters.keyword")} onClear={f.q ? () => set("q", undefined) : undefined}>
                <TextInput
                  inputSize="sm"
                  value={f.q ?? ""}
                  placeholder={t("views.keywordPlaceholder")}
                  aria-label={t("views.filters.keyword")}
                  onChange={(e) => set("q", e.target.value || undefined)}
                  className="w-full"
                />
              </FilterRow>
            </div>
          </div>
          {view && (
            <Link
              to="/views/$viewId/{-$ticketId}"
              params={{ viewId: view.id, ticketId: undefined }}
              className="inline-flex w-fit items-center gap-1 text-[12.5px] text-fg-3 underline-offset-2 hover:text-fg hover:underline"
            >
              {t("views.openThis")} <ArrowUpRight className="size-3.5" />
            </Link>
          )}
        </div>
        <div className="sticky bottom-0 flex items-center gap-2 border-t border-line bg-surface px-5 py-3">
          <span className="flex-1 text-xs text-danger-fg">{error}</span>
          <Button variant="ghost" onClick={requestClose}>
            {t("common.cancel")}
          </Button>
          <Button variant="primary" type="submit" loading={save.isPending}>
            {view ? t("views.save") : t("views.create")}
          </Button>
        </div>
      </form>
    </Drawer>
  );
}

function FilterRow({ label, children, onClear }: { label: string; children: React.ReactNode; onClear?: () => void }) {
  const { t } = useTranslation("settingsInbox");
  return (
    <div className={cn("flex min-h-11 items-center gap-3 border-b border-line-soft px-3 transition-colors last:border-0", onClear && "bg-sunken/60")}>
      <span className={cn("w-[72px] shrink-0 text-[12.5px]", onClear ? "font-medium text-fg-2" : "text-fg-3")}>{label}</span>
      <div className="flex min-w-0 flex-1 items-center gap-2">{children}</div>
      <button
        type="button"
        aria-label={t("views.clear", { field: label.toLowerCase() })}
        onClick={onClear}
        disabled={!onClear}
        className="flex size-6 shrink-0 items-center justify-center rounded-sm text-fg-4 hover:bg-hover hover:text-fg disabled:invisible"
      >
        <X className="size-3.5" />
      </button>
    </div>
  );
}

/** The operator of a row that only leaves tickets out, in the place of OpSelect. */
function NotOp() {
  const { t } = useTranslation("settingsInbox");
  return <span className="w-[104px] shrink-0 px-2 text-[12.5px] text-fg-2">{t("views.ops.not")}</span>;
}

/** A row's operator: is / is not, and for labels any of / none of / empty. */
function OpSelect({ field, value, onChange }: { field: OpField; value: FilterOp; onChange: (op: FilterOp) => void }) {
  const { t } = useTranslation("settingsInbox");
  const label: Record<FilterOp, string> =
    field === "tag"
      ? { is: t("views.labelOps.is"), not: t("views.labelOps.not"), empty: t("views.labelOps.empty") }
      : { is: t("views.ops.is"), not: t("views.ops.not"), empty: t("views.labelOps.empty") };
  return (
    <Select<FilterOp>
      size="sm"
      value={value}
      onChange={onChange}
      options={fieldOps[field].map((o) => ({ value: o, label: label[o] }))}
      className="w-[104px] shrink-0"
      aria-label={t("edit.operator")}
    />
  );
}
