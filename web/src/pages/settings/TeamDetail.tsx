// Settings > Teams > a team (contracts/teams.md): general details, members
// and their roles, privacy, hours, assignment, the signature and the
// addresses that route here. Admins and the team's leads edit; privacy and
// deleting are for admins.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { ArrowRight, AtSign, ChevronDown, Clock, Eye, Globe, Inbox, Lock, LogOut, MessageCircle, Plus, Route, Shuffle, Ticket, Trash2, UserPlus, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { BackLink, ListRow, Row, Section, SettingsPage } from "@/components/layout";
import { SaveBar } from "@/components/settings/SaveBar";
import { ColorSwatchPicker } from "@/components/settings2/ColorSwatchPicker";
import { templateProblems } from "@/components/settings2/MemberProfileFields";
import { MotionList, MotionRow } from "@/components/settings2/motion";
import { Note, PrivatePill, TeamDot, teamItems, useMyTeamRole } from "@/components/settings2/teams";
import { UnsavedGuard } from "@/components/settings2/UnsavedGuard";
import { SignatureEditor } from "@/components/templates/SignatureEditor";
import { Avatar } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/ui/confirm";
import { Dialog } from "@/components/ui/dialog";
import { EmptyState } from "@/components/ui/empty";
import { Field, Textarea, TextInput } from "@/components/ui/input";
import { Picker } from "@/components/ui/picker";
import { Select } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { Segmented } from "@/components/ui/tabs";
import { toast } from "@/components/ui/toast";
import { Tooltip } from "@/components/ui/tooltip";
import { api } from "@/lib/api/endpoints";
import type { AssignMode, BusinessHours as BH, LabelColor, Member, Team, TeamRole, TemplatePreviewBody, TemplateProblem, TimeRange, Weekday } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { q, qk, removeFromList, upsertInList } from "@/lib/queries";
import { atLeast, useRole, useUser } from "@/lib/session";
import { DayRow, Holidays, WEEK, dayError, norm, timezones } from "./BusinessHours";

const ROLES: TeamRole[] = ["lead", "member", "observer"];
const MODES: AssignMode[] = ["none", "round_robin", "load_balanced"];

type Draft = Pick<Team, "name" | "description" | "color" | "members" | "signature_html" | "business_hours" | "assign_mode">;

const toDraft = (t: Team): Draft => ({
  name: t.name,
  description: t.description,
  color: t.color,
  members: t.members.map((m) => ({ ...m })),
  signature_html: t.signature_html,
  business_hours: t.business_hours ? norm(t.business_hours) : null,
  assign_mode: t.assign_mode,
});

export default function TeamDetail() {
  const { t } = useTranslation("settingsTeams");
  const { teamId } = useParams({ from: "/app/settings/teams/$teamId" });
  const { data, isLoading } = useQuery(q.teams());
  const team = data?.find((x) => x.id === teamId);
  const back = <BackLink to="/settings/teams">{t("detail.back")}</BackLink>;

  if (isLoading)
    return (
      <SettingsPage title={<Skeleton className="h-5 w-40" />} back={back}>
        {[160, 220, 120].map((h, i) => (
          <Skeleton key={i} className="w-full rounded-lg" style={{ height: h }} />
        ))}
      </SettingsPage>
    );
  if (!team)
    return (
      <SettingsPage title={t("detail.fallbackTitle")} back={back}>
        <Section flush>
          <EmptyState
            lit={3}
            title={t("detail.notFound")}
            description={t("detail.notFoundBody")}
            action={
              <Button size="sm" asChild>
                <Link to="/settings/teams">{t("detail.backToList")}</Link>
              </Button>
            }
          />
        </Section>
      </SettingsPage>
    );
  return <TeamEditor key={team.id} team={team} />;
}

function TeamEditor({ team }: { team: Team }) {
  const { t } = useTranslation("settingsTeams");
  const qc = useQueryClient();
  const navigate = useNavigate();
  const confirm = useConfirm();
  const me = useUser();
  const admin = atLeast(useRole(), "admin");
  const myRole = useMyTeamRole(team);
  const canEdit = admin || myRole === "lead";
  const { data: members } = useQuery(q.members());
  const { data: mailboxes } = useQuery({ ...q.mailboxes(), enabled: canEdit, retry: false });
  const [deleting, setDeleting] = useState(false);
  // What the server found wrong with the signature: in the preview's check, or a refused save.
  const [previewProblems, setPreviewProblems] = useState<TemplateProblem[]>([]);
  const [saveProblems, setSaveProblems] = useState<TemplateProblem[]>([]);
  const onProblems = useCallback((p: TemplateProblem[]) => setPreviewProblems(p), []);

  const base = useMemo(() => toDraft(team), [team]);
  const [draft, setDraft] = useState<Draft>(base);
  const [prevBase, setPrevBase] = useState(base);
  useEffect(() => {
    // Someone else saved: take their version unless we are mid-edit.
    if (JSON.stringify(draft) === JSON.stringify(prevBase)) setDraft(base);
    setPrevBase(base);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base]);
  const dirty = JSON.stringify(draft) !== JSON.stringify(base);
  const set = <K extends keyof Draft>(k: K, v: Draft[K]) => setDraft((d) => ({ ...d, [k]: v }));
  const signatureHtml = draft.signature_html;
  const previewBody = useMemo<TemplatePreviewBody>(() => ({ kind: "signature", team_id: team.id, body_html: signatureHtml }), [team.id, signatureHtml]);
  const hoursErrors = draft.business_hours ? WEEK.map((d) => dayError(draft.business_hours!.schedule[d])) : [];
  const noLead = draft.members.length > 0 && !draft.members.some((m) => m.role === "lead");

  const save = useMutation({
    mutationFn: () => {
      const body: Partial<Draft> = {};
      for (const k of Object.keys(draft) as (keyof Draft)[])
        if (JSON.stringify(draft[k]) !== JSON.stringify(base[k])) (body as Record<string, unknown>)[k] = draft[k];
      if (body.name !== undefined) body.name = body.name.trim();
      if (body.description !== undefined) body.description = body.description.trim();
      return api.teams.update(team.id, body);
    },
    onMutate: () => setSaveProblems([]),
    onSuccess: (x) => {
      upsertInList(qc, qk.teams, x);
      setDraft(toDraft(x));
      toast.success(t("detail.saved"));
    },
    onError: (e) => {
      setSaveProblems(templateProblems(e, "signature"));
      toast.error(e, t("detail.saveFailed"));
    },
  });

  const write = useMutation({
    mutationFn: (b: Partial<Team>) => api.teams.update(team.id, b),
    onSuccess: (x) => upsertInList(qc, qk.teams, x),
    onError: (e) => toast.error(e),
  });

  const join = () =>
    confirm.show(t("join.body", { name: team.name }), async () => {
      const x = await api.teams.join(team.id);
      upsertInList(qc, qk.teams, x);
      void qc.invalidateQueries({ queryKey: qk.counts });
      toast.success(t("join.done", { name: team.name }));
    }, { title: t("join.title", { name: team.name }), confirmLabel: t("join.confirm") });

  const leave = () =>
    confirm.show(team.private ? t("leave.bodyPrivate", { name: team.name }) : t("leave.body", { name: team.name }), async () => {
      await api.teams.leave(team.id);
      void qc.invalidateQueries({ queryKey: qk.teams });
      void qc.invalidateQueries({ queryKey: qk.counts });
      toast.success(t("leave.done", { name: team.name }));
    }, { title: t("leave.title", { name: team.name }), confirmLabel: t("leave.confirm"), danger: team.private });

  const togglePrivate = (next: boolean) => {
    const people = (members ?? []).filter((m) => !team.member_ids.includes(m.user.id));
    const body = next
      ? t("privacy.makePrivateBody", { name: team.name, count: team.open_count, people: people.length, onTeam: team.member_ids.length })
      : t("privacy.makePublicBody", { name: team.name });
    confirm.show(body, () => write.mutateAsync({ private: next }).then(() => toast.success(next ? t("privacy.nowPrivate") : t("privacy.nowPublic"))), {
      title: next ? t("privacy.makePrivateTitle", { name: team.name }) : t("privacy.makePublicTitle", { name: team.name }),
      confirmLabel: next ? t("privacy.makePrivate") : t("privacy.makePublic"),
    });
  };

  const addresses = (mailboxes ?? []).filter((m) => team.mailbox_ids.includes(m.id) || m.team_id === team.id);
  // Aliases of other addresses whose new tickets come here.
  const aliasRows = (mailboxes ?? []).flatMap((m) =>
    m.private ? [] : Object.entries(m.alias_teams ?? {}).flatMap(([alias, id]) => (id === team.id ? [{ mailbox: m, alias }] : [])),
  );
  const membersHidden = team.private && !team.members.length && team.member_ids.length === 0 && !admin;

  return (
    <SettingsPage
      back={<BackLink to="/settings/teams">{t("detail.back")}</BackLink>}
      title={
        <span className="flex items-center gap-2.5">
          <TeamDot team={{ name: draft.name || team.name, color: draft.color }} className="size-3" />
          <span className="truncate">{team.name}</span>
          {team.private && <PrivatePill />}
        </span>
      }
      description={team.description || t("detail.subtitle", { open: team.open_count, unassigned: team.unassigned_count })}
      actions={
        <>
          {admin && team.private && !myRole && (
            <Button icon={<Eye className="size-3.5" />} onClick={join}>
              {t("join.button")}
            </Button>
          )}
          {myRole && (
            <Button variant="ghost" icon={<LogOut className="size-3.5" />} onClick={leave}>
              {t("leave.button")}
            </Button>
          )}
          <Button asChild variant="secondary" iconRight={<ArrowRight className="size-3.5" />}>
            <Link to="/teams/$teamId/{-$ticketId}" params={{ teamId: team.id, ticketId: undefined }}>
              {t("detail.openInbox")}
            </Link>
          </Button>
        </>
      }
    >
      <UnsavedGuard when={dirty && !save.isPending} />

      {team.private && !myRole && (
        <Note tone="violet" icon={<Lock className="mt-0.5 size-3.5 shrink-0" aria-hidden />}>
          {admin ? t("join.note", { name: team.name }) : t("join.noteMember", { name: team.name })}
        </Note>
      )}

      <fieldset disabled={!canEdit} className="flex min-w-0 flex-col gap-9">
        <Section title={t("general.title")}>
          <Row label={t("general.name")} htmlFor="team-name">
            <TextInput id="team-name" className="w-[280px]" maxLength={60} value={draft.name} invalid={!draft.name.trim()} onChange={(e) => set("name", e.target.value)} />
          </Row>
          <Row label={t("general.description")} description={t("general.descriptionHint")} stack>
            <Textarea rows={2} maxLength={280} value={draft.description} placeholder={t("general.descriptionPlaceholder")} onChange={(e) => set("description", e.target.value)} />
          </Row>
          <Row label={t("general.color")} description={t("general.colorHint")} stack>
            <ColorSwatchPicker value={draft.color} onChange={(c) => set("color", c as LabelColor)} label={t("general.color")} size="sm" />
          </Row>
        </Section>

        <MembersSection draft={draft.members} hidden={membersHidden} members={members ?? []} canEdit={canEdit} meId={me.id} onChange={(m) => set("members", m)} noLead={noLead} />

        <Section title={t("privacy.title")} description={t("privacy.hint")}>
          <Row
            label={
              <span className="flex items-center gap-1.5">
                <Lock className="size-3.5 text-fg-3" aria-hidden />
                {t("privacy.label")}
              </span>
            }
            description={team.private ? t("privacy.onHint") : t("privacy.offHint")}
          >
            <Tooltip content={admin ? undefined : t("privacy.adminOnly")}>
              <span>
                <Switch aria-label={t("privacy.label")} checked={team.private} disabled={!admin || write.isPending} onCheckedChange={togglePrivate} />
              </span>
            </Tooltip>
          </Row>
          {team.private && (
            <div className="py-3">
              <Note>{t("privacy.whoSees")}</Note>
            </div>
          )}
        </Section>

        <HoursSection value={draft.business_hours} errors={hoursErrors} disabled={!canEdit} onChange={(h) => set("business_hours", h)} />

        <AssignSection value={draft.assign_mode} disabled={!canEdit} onChange={(m) => set("assign_mode", m)} />

        <Section title={t("signature.title")} description={t("signature.hint", { tag: "{{ agent.signature }}" })}>
          <SignatureEditor
            className="py-3.5"
            kind="signature"
            value={draft.signature_html}
            onChange={(v) => {
              set("signature_html", v);
              setSaveProblems([]);
            }}
            placeholder={t("signature.placeholder")}
            ariaLabel={t("signature.title")}
            disabled={!canEdit}
            problems={previewProblems.length ? previewProblems : saveProblems}
            previewTitle={t("signature.preview")}
            previewBody={previewBody}
            onProblems={onProblems}
          />
        </Section>

        <Section
          flush
          title={t("addresses.title")}
          description={t("addresses.hint")}
          actions={
            admin && (
              <Button size="sm" asChild icon={<Plus className="size-3.5" />}>
                <Link to="/settings/mailboxes/new">{t("addresses.add")}</Link>
              </Button>
            )
          }
        >
          {!addresses.length && !aliasRows.length ? (
            <div className="flex items-center gap-3 px-4 py-4 text-[12.5px] text-fg-3">
              <Inbox className="size-4 text-fg-4" aria-hidden />
              {t("addresses.empty")}
            </div>
          ) : (
            <MotionList>
              {addresses.map((m) => (
                <MotionRow key={m.id}>
                  <Link
                    to="/settings/mailboxes/$mailboxId"
                    params={{ mailboxId: m.id }}
                    className="group flex min-h-11 items-center gap-3 border-b border-line-soft px-4 py-2 last:border-0 hover:bg-sunken"
                  >
                    <Inbox className="size-3.5 text-fg-4" aria-hidden />
                    <span className="mono min-w-0 flex-1 truncate text-[12.5px]">{m.email}</span>
                    {m.aliases.length > 0 && <span className="text-[11.5px] text-fg-4">{t("addresses.aliases", { count: m.aliases.length })}</span>}
                    <ArrowRight className="size-3.5 text-fg-4 opacity-0 transition-opacity group-hover:opacity-100" aria-hidden />
                  </Link>
                </MotionRow>
              ))}
              {aliasRows.map(({ mailbox: m, alias }) => (
                <MotionRow key={`${m.id}:${alias}`}>
                  <Link
                    to="/settings/mailboxes/$mailboxId"
                    params={{ mailboxId: m.id }}
                    className="group flex min-h-11 items-center gap-3 border-b border-line-soft px-4 py-2 last:border-0 hover:bg-sunken"
                  >
                    <AtSign className="size-3.5 text-fg-4" aria-hidden />
                    <span className="mono min-w-0 flex-1 truncate text-[12.5px]">{alias}</span>
                    <span className="min-w-0 truncate text-[11.5px] text-fg-4">{t("addresses.aliasOf", { email: m.email })}</span>
                    <ArrowRight className="size-3.5 text-fg-4 opacity-0 transition-opacity group-hover:opacity-100" aria-hidden />
                  </Link>
                </MotionRow>
              ))}
            </MotionList>
          )}
          {admin && <RoutedHere teamId={team.id} />}
        </Section>

        {admin && (
          <Section title={t("delete.section")} danger>
            <Row label={t("delete.title", { name: team.name })} description={t("delete.hint")}>
              <Button variant="danger" icon={<Trash2 className="size-3.5" />} onClick={() => setDeleting(true)}>
                {t("delete.button")}
              </Button>
            </Row>
          </Section>
        )}
      </fieldset>

      {deleting && (
        <DeleteTeamDialog
          team={team}
          onClose={() => setDeleting(false)}
          onDeleted={() => {
            void navigate({ to: "/settings/teams" });
          }}
        />
      )}
      <SaveBar
        dirty={dirty && canEdit}
        saving={save.isPending}
        disabled={!draft.name.trim() || hoursErrors.some(Boolean)}
        onSave={() => save.mutate()}
        onDiscard={() => setDraft(base)}
        message={noLead ? t("members.noLeadWarning") : undefined}
      />
    </SettingsPage>
  );
}

function MembersSection({
  draft,
  members,
  canEdit,
  hidden,
  meId,
  noLead,
  onChange,
}: {
  draft: Team["members"];
  members: Member[];
  canEdit: boolean;
  hidden: boolean;
  meId: string;
  noLead: boolean;
  onChange: (m: Team["members"]) => void;
}) {
  const { t } = useTranslation("settingsTeams");
  const byId = new Map(members.map((m) => [m.user.id, m]));
  const on = new Set(draft.map((m) => m.user_id));
  const items = members
    .filter((m) => !on.has(m.user.id))
    .map((m) => ({
      value: m.user.id,
      label: m.user.name || m.user.email,
      hint: m.role === "viewer" ? t("members.viewerHint") : m.user.email,
      keywords: [m.user.email],
      icon: <Avatar name={m.user.name} src={m.user.avatar_url} size="xs" />,
    }));
  const add = (id: string) => {
    const m = byId.get(id);
    onChange([...draft, { user_id: id, role: m?.role === "viewer" ? "observer" : draft.length ? "member" : "lead" }]);
  };
  const order = (r: TeamRole) => ROLES.indexOf(r);
  const rows = [...draft].sort((a, b) => order(a.role) - order(b.role) || (byId.get(a.user_id)?.user.name ?? "").localeCompare(byId.get(b.user_id)?.user.name ?? ""));

  return (
    <Section
      flush
      title={t("members.title")}
      description={t("members.hint")}
      actions={
        canEdit &&
        !hidden && (
          <Picker
            items={items}
            value={null}
            onSelect={add}
            placeholder={t("members.search")}
            emptyText={t("members.noneLeft")}
            width={300}
            align="end"
            trigger={
              <Button size="sm" icon={<UserPlus className="size-3.5" />} iconRight={<ChevronDown className="size-3 text-fg-4" />}>
                {t("members.add")}
              </Button>
            }
          />
        )
      }
    >
      <div className="grid gap-px border-b border-line-soft bg-line-soft sm:grid-cols-3">
        {ROLES.map((r) => (
          <div key={r} className="bg-sunken/60 px-4 py-2.5">
            <div className="flex items-center gap-1.5 text-[12px] font-medium">
              <RoleDot role={r} />
              {t(`roles.${r}`)}
            </div>
            <p className="mt-0.5 text-[11.5px] leading-[16px] text-fg-3">{t(`roles.${r}Hint`)}</p>
          </div>
        ))}
      </div>
      {hidden ? (
        <div className="flex items-center gap-3 px-4 py-5 text-[12.5px] text-fg-3">
          <Lock className="size-4 text-fg-4" aria-hidden />
          {t("members.hidden")}
        </div>
      ) : !rows.length ? (
        <div className="px-4 py-5 text-[12.5px] text-fg-3">{t("members.empty")}</div>
      ) : (
        <MotionList>
          {rows.map((r) => {
            const m = byId.get(r.user_id);
            const viewer = m?.role === "viewer";
            return (
              <MotionRow key={r.user_id}>
                <ListRow className="min-h-11 py-1.5">
                  <Avatar name={m?.user.name ?? "?"} src={m?.user.avatar_url} size="md" presence={m?.presence} />
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-[13px] font-medium">
                      {m?.user.name ?? t("members.unknown")}
                      {r.user_id === meId && <span className="ml-1.5 text-[11.5px] font-normal text-fg-4">{t("members.you")}</span>}
                    </div>
                    <div className="truncate text-[12px] text-fg-3">{m?.user.email}</div>
                  </div>
                  <Tooltip content={viewer ? t("members.viewerOnly") : undefined}>
                    <span>
                      <Select
                        size="sm"
                        className="w-[132px]"
                        aria-label={t("members.roleFor", { name: m?.user.name ?? "" })}
                        disabled={!canEdit || viewer}
                        value={r.role}
                        onChange={(role) => onChange(draft.map((x) => (x.user_id === r.user_id ? { ...x, role } : x)))}
                        options={ROLES.map((x) => ({ value: x, label: t(`roles.${x}`), icon: <RoleDot role={x} /> }))}
                      />
                    </span>
                  </Tooltip>
                  {canEdit && (
                    <Tooltip content={t("members.remove")}>
                      <Button
                        size="sm"
                        variant="ghost"
                        square
                        aria-label={t("members.removeNamed", { name: m?.user.name ?? "" })}
                        onClick={() => onChange(draft.filter((x) => x.user_id !== r.user_id))}
                      >
                        <X className="size-3.5" />
                      </Button>
                    </Tooltip>
                  )}
                </ListRow>
              </MotionRow>
            );
          })}
        </MotionList>
      )}
      {noLead && <div className="border-t border-line-soft px-4 py-2.5 text-[12px] text-tone-amber">{t("members.noLeadWarning")}</div>}
    </Section>
  );
}

function RoleDot({ role }: { role: TeamRole }) {
  return (
    <span
      aria-hidden
      className={cn(
        "size-1.5 shrink-0 rounded-full",
        role === "lead" ? "bg-tone-violet-solid" : role === "member" ? "bg-tone-blue-solid" : "border border-fg-4 bg-transparent",
      )}
    />
  );
}

function HoursSection({ value, errors, disabled, onChange }: { value: BH | null; errors: ("order" | "overlap" | null)[]; disabled: boolean; onChange: (h: BH | null) => void }) {
  const { t } = useTranslation("settingsTeams");
  const { data: ws } = useQuery(q.businessHours());
  const tzItems = useMemo(() => timezones().map((z) => ({ value: z, label: z.replace(/_/g, " "), keywords: z.split("/") })), []);
  const own = value !== null;
  const [showHolidays, setShowHolidays] = useState(false);
  const start = () => {
    const from = ws ? norm(ws) : null;
    onChange({
      enabled: true,
      timezone: from?.timezone ?? "UTC",
      schedule: from?.schedule ?? (Object.fromEntries(WEEK.map((d) => [d, d === "sat" || d === "sun" ? [] : [{ start: "09:00", end: "17:00" }]])) as BH["schedule"]),
      holidays: from?.holidays ?? [],
    });
  };
  const setDay = (d: Weekday, ranges: TimeRange[]) => value && onChange({ ...value, schedule: { ...value.schedule, [d]: ranges } });

  return (
    <>
      <Section
        flush
        title={t("hours.title")}
        description={t("hours.hint")}
        actions={
          <Segmented
            value={own ? "own" : "workspace"}
            onChange={(v) => (disabled ? undefined : v === "own" ? start() : onChange(null))}
            options={[
              { value: "workspace", label: t("hours.workspace"), icon: <Globe className="size-3" /> },
              { value: "own", label: t("hours.own"), icon: <Clock className="size-3" /> },
            ]}
          />
        }
      >
        {!own ? (
          <div className="flex items-center justify-between gap-3 px-4 py-3.5 text-[12.5px] text-fg-3">
            <span>
              {ws ? (ws.enabled ? t("hours.followsOn", { tz: ws.timezone.replace(/_/g, " ") }) : t("hours.followsOff")) : t("hours.follows")}
            </span>
            <Button size="sm" variant="ghost" asChild>
              <Link to="/settings/business-hours">{t("hours.editWorkspace")}</Link>
            </Button>
          </div>
        ) : (
          <>
            <div className="flex items-center justify-between gap-3 border-b border-line-soft px-4 py-3">
              <span className="text-[12.5px] font-medium">{t("hours.timezone")}</span>
              <Picker
                items={tzItems}
                value={value.timezone}
                onSelect={(timezone) => onChange({ ...value, timezone })}
                placeholder={t("hours.searchTimezones")}
                width={300}
                align="end"
                trigger={
                  <Button disabled={disabled} className="w-[260px] justify-between font-normal" icon={<Globe className="size-3.5 text-fg-3" />} iconRight={<ChevronDown className="size-3.5 text-fg-3" />}>
                    <span className="flex-1 truncate text-left">{value.timezone.replace(/_/g, " ")}</span>
                  </Button>
                }
              />
            </div>
            {WEEK.map((d, i) => (
              <DayRow
                key={d}
                day={d}
                ranges={value.schedule[d]}
                error={errors[i] ?? null}
                disabled={disabled}
                onChange={(r) => setDay(d, r)}
                onCopy={(days) => {
                  const next = { ...value.schedule };
                  for (const x of days) next[x] = value.schedule[d].map((r) => ({ ...r }));
                  onChange({ ...value, schedule: next });
                }}
              />
            ))}
            {!value.holidays.length && !showHolidays && (
              <div className="flex items-center justify-between gap-3 border-t border-line-soft px-4 py-3 text-[12.5px] text-fg-3">
                <span>{t("hours.noHolidays")}</span>
                {!disabled && (
                  <span className="flex items-center gap-1">
                    {(ws?.holidays.length ?? 0) > 0 && (
                      <Button size="sm" variant="ghost" onClick={() => onChange({ ...value, holidays: ws!.holidays.map((h) => ({ ...h })) })}>
                        {t("hours.copyHolidays", { count: ws!.holidays.length })}
                      </Button>
                    )}
                    <Button size="sm" variant="ghost" icon={<Plus className="size-3.5" />} onClick={() => setShowHolidays(true)}>
                      {t("hours.addHoliday")}
                    </Button>
                  </span>
                )}
              </div>
            )}
          </>
        )}
      </Section>
      {own && (value.holidays.length > 0 || showHolidays) && <Holidays holidays={value.holidays} disabled={disabled} onChange={(holidays) => onChange({ ...value, holidays })} />}
    </>
  );
}

function AssignSection({ value, disabled, onChange }: { value: AssignMode | null; disabled: boolean; onChange: (m: AssignMode | null) => void }) {
  const { t } = useTranslation("settingsTeams");
  const { t: ti } = useTranslation("settingsInbox");
  const { data: a } = useQuery(q.assignment());
  const wsMode = a?.mode ?? "none";
  return (
    <Section title={t("assign.title")} description={t("assign.hint")}>
      <Row label={t("assign.label")} description={value === null ? t("assign.followsHint", { mode: ti(`assignment.modes.${wsMode}.label`) }) : ti(`assignment.modes.${value}.hint`)}>
        <Select
          className="w-[240px]"
          aria-label={t("assign.label")}
          disabled={disabled}
          value={value ?? "__default"}
          onChange={(v) => onChange(v === "__default" ? null : (v as AssignMode))}
          options={[
            { value: "__default", label: t("assign.follow", { mode: ti(`assignment.modes.${wsMode}.label`) }), icon: <Shuffle className="text-fg-4" /> },
            ...MODES.map((m) => ({ value: m, label: ti(`assignment.modes.${m}.label`) })),
          ]}
        />
      </Row>
      <Row label={t("assign.precedence")} description={t("assign.precedenceHint")}>
        <Button size="sm" variant="ghost" asChild>
          <Link to="/settings/assignment">{t("assign.open")}</Link>
        </Button>
      </Row>
    </Section>
  );
}

/** Widgets, topics and routing rules that send work here, with links. */
function RoutedHere({ teamId }: { teamId: string }) {
  const { t } = useTranslation("settingsTeams");
  const { data: widgets } = useQuery({ ...q.widgets(), retry: false });
  const { data: rules } = useQuery({ ...q.routingRules(), retry: false });
  const ws = (widgets ?? []).filter((w) => w.team_id === teamId);
  const topics = (widgets ?? []).flatMap((w) => w.settings.topics.filter((x) => x.team_id === teamId).map((x) => ({ ...x, widget: w })));
  const rs = (rules ?? []).filter((r) => r.team_id === teamId);
  if (!ws.length && !topics.length && !rs.length) return null;
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-1.5 border-t border-line-soft bg-sunken/50 px-4 py-2.5 text-[12px] text-fg-3">
      <span className="font-medium text-fg-2">{t("addresses.alsoRouted")}</span>
      {ws.map((w) => (
        <Link key={w.id} to="/settings/brands/$brandId/widgets/$widgetId" params={{ brandId: w.brand_id, widgetId: w.id }} className="inline-flex items-center gap-1 hover:text-fg">
          <MessageCircle className="size-3" aria-hidden />
          {w.name}
        </Link>
      ))}
      {topics.map((x) => (
        <Link key={x.id} to="/settings/brands/$brandId/widgets/$widgetId" params={{ brandId: x.widget.brand_id, widgetId: x.widget.id }} className="inline-flex items-center gap-1 hover:text-fg">
          <MessageCircle className="size-3" aria-hidden />
          {t("addresses.topic", { label: x.label, widget: x.widget.name })}
        </Link>
      ))}
      {rs.map((r) => (
        <Link key={r.id} to="/settings/routing/$ruleId" params={{ ruleId: r.id }} className="inline-flex items-center gap-1 hover:text-fg">
          <Route className="size-3" aria-hidden />
          {r.name}
        </Link>
      ))}
    </div>
  );
}

function DeleteTeamDialog({ team, onClose, onDeleted }: { team: Team; onClose: () => void; onDeleted: () => void }) {
  const { t } = useTranslation("settingsTeams");
  const qc = useQueryClient();
  const { data: teams } = useQuery(q.teams());
  const { data: widgets } = useQuery({ ...q.widgets(), retry: false });
  const { data: rules } = useQuery({ ...q.routingRules(), retry: false });
  const others = (teams ?? []).filter((x) => x.id !== team.id);
  const [moveTo, setMoveTo] = useState<string>(others[0]?.id ?? "__none");
  const [typed, setTyped] = useState("");
  const widgetCount = (widgets ?? []).filter((w) => w.team_id === team.id || w.settings.topics.some((x) => x.team_id === team.id)).length;
  const ruleCount = (rules ?? []).filter((r) => r.team_id === team.id).length;
  const target = others.find((x) => x.id === moveTo);

  const del = useMutation({
    mutationFn: () => api.teams.removeTo(team.id, target?.id),
    onSuccess: () => {
      removeFromList(qc, qk.teams, team.id);
      void qc.invalidateQueries({ queryKey: qk.teams });
      void qc.invalidateQueries({ queryKey: qk.mailboxes });
      void qc.invalidateQueries({ queryKey: qk.widgets });
      void qc.invalidateQueries({ queryKey: qk.routingRules });
      toast.success(t("delete.done", { name: team.name }));
      onClose();
      onDeleted();
    },
    onError: (e) => toast.error(e),
  });

  const facts = [
    { n: team.open_count, label: t("delete.tickets", { count: team.open_count }), icon: <Ticket className="size-3.5" /> },
    { n: team.mailbox_ids.length, label: t("delete.addresses", { count: team.mailbox_ids.length }), icon: <Inbox className="size-3.5" /> },
    { n: widgetCount, label: t("delete.widgets", { count: widgetCount }), icon: <MessageCircle className="size-3.5" /> },
    { n: ruleCount, label: t("delete.rules", { count: ruleCount }), icon: <Route className="size-3.5" /> },
  ];

  return (
    <Dialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={t("delete.dialogTitle", { name: team.name })}
      description={t("delete.dialogHint")}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button variant="danger-solid" disabled={typed.trim() !== team.name} loading={del.isPending} onClick={() => del.mutate()}>
            {target ? t("delete.confirmMove", { name: target.name }) : t("delete.confirm")}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4 pb-2">
        <ul className="grid grid-cols-2 gap-2">
          {facts.map((f, i) => (
            <li key={i} className={cn("flex items-center gap-2 rounded-md border border-line px-3 py-2 text-[12.5px]", !f.n && "text-fg-4")}>
              <span className="text-fg-4">{f.icon}</span>
              {f.label}
            </li>
          ))}
        </ul>
        <Field label={t("delete.moveTo")} hint={target ? t("delete.moveHint", { name: target.name }) : t("delete.noMoveHint")}>
          {({ id }) => (
            <Picker
              items={[{ value: "__none", label: t("delete.noMove") }, ...teamItems(others)]}
              value={moveTo}
              onSelect={setMoveTo}
              placeholder={t("common.searchTeams")}
              width={320}
              trigger={
                <Button id={id} className="w-full justify-between font-normal" iconRight={<ChevronDown className="size-3.5 text-fg-3" />}>
                  <span className="flex min-w-0 items-center gap-2">
                    {target ? <TeamDot team={target} /> : null}
                    <span className="truncate">{target ? target.name : t("delete.noMove")}</span>
                  </span>
                </Button>
              }
            />
          )}
        </Field>
        <Field label={t("delete.typeName", { name: team.name })}>
          {({ id }) => <TextInput id={id} value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" placeholder={team.name} />}
        </Field>
      </div>
    </Dialog>
  );
}
