// Settings > Inbox categories (contracts/collab.md "Categories"): the tabs an inbox is split into.
// Admins set the workspace's default, whether members may choose their own, and a team's own set
// (the first team in order wins for someone in several). Others see what applies to them and where
// to change their own accounts. Admins choose categories; they never see anyone's mail.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { ChevronDown, ChevronRight, Lock, Plus, ShieldCheck, Users } from "lucide-react";
import { useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { ListRow, Row, Section, SettingsPage } from "@/components/layout";
import { AccountDot } from "@/components/collab/bits";
import { CategoryEditor, CategoryPills, likeGmail, useCategoriesSaved, useCategoryProblem } from "@/components/collab/CategoryEditor";
import { sourceText } from "@/components/collab/CategoriesSection";
import { ListSkeleton } from "@/components/settings2/bits";
import { SettingsEmpty } from "@/components/settings2/SettingsEmpty";
import { SortableList } from "@/components/settings2/Sortable";
import { Note, TeamChip, teamItems, useTeams } from "@/components/settings2/teams";
import { UnsavedGuard } from "@/components/settings2/UnsavedGuard";
import { OllieAvatar } from "@/components/tickets/ollie";
import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/ui/confirm";
import { EmptyState } from "@/components/ui/empty";
import { Picker } from "@/components/ui/picker";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast";
import { api } from "@/lib/api/endpoints";
import type { MailCategory, MailCategorySettings, Team, UserRef } from "@/lib/api/types";
import { withKeys } from "@/lib/collab/categories";
import { useMailAccounts } from "@/lib/collab/hooks";
import { cn } from "@/lib/cn";
import { relLong } from "@/lib/format";
import { dur, springs } from "@/lib/motion";
import { q, qk } from "@/lib/queries";
import { atLeast, useRole } from "@/lib/session";
import { AI_TONE, tone } from "@/lib/tones";

type TeamSet = MailCategorySettings["teams"][number];
type Draft = { enabled: boolean; categories: MailCategory[] };

const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);

export default function MailCategories() {
  const { t } = useTranslation("mailCategories");
  const admin = atLeast(useRole(), "admin");
  const { data: settings, isLoading } = useQuery(q.mailCategories());
  return (
    <SettingsPage title={t("page.title")} description={t("page.description")}>
      {isLoading || !settings ? (
        <Section>
          <ListSkeleton rows={4} />
        </Section>
      ) : admin ? (
        <>
          <WorkspaceCard settings={settings} />
          <TeamsCard settings={settings} />
          <Note icon={<ShieldCheck className="mt-px size-3.5 shrink-0 text-fg-4" />}>{t("privacy")}</Note>
        </>
      ) : (
        <MemberView settings={settings} />
      )}
    </SettingsPage>
  );
}

/** "Updated 3 days ago by Priya", under a saved set. */
function Updated({ at, by }: { at: string; by: UserRef | null }) {
  const { t } = useTranslation("mailCategories");
  return <span className="text-[11.5px] text-fg-4">{by ? t("common.updated", { when: relLong(at), name: by.name }) : t("common.updatedNoName", { when: relLong(at) })}</span>;
}

/** A card's footer: what is wrong or when it was saved on the left, its actions on the right. */
function Footer({ left, children }: { left?: ReactNode; children: ReactNode }) {
  return (
    <div className="flex flex-wrap items-center justify-end gap-2 border-t border-line-soft py-3">
      <div className="mr-auto min-w-0">{left}</div>
      {children}
    </div>
  );
}

/** How new mail is sorted, from the workspace's AI. */
function SortingRow({ sorting }: { sorting: MailCategorySettings["sorting"] }) {
  const { t } = useTranslation("mailCategories");
  return (
    <Row
      label={t("workspace.sorting")}
      description={
        sorting === "ai" ? (
          t("workspace.sortingAi")
        ) : (
          <>
            {t("workspace.sortingRules")}{" "}
            <Link to="/settings/ai" className="font-medium text-fg-2 underline-offset-2 hover:text-fg hover:underline">
              {t("workspace.sortingRulesHint")}
            </Link>
          </>
        )
      }
    >
      {sorting === "ai" ? (
        <span className={cn("inline-flex h-6 items-center gap-1.5 rounded-md px-2 text-[12px] font-medium", tone[AI_TONE].chip)}>
          <OllieAvatar size={14} />
          {t("workspace.byOllie")}
        </span>
      ) : (
        <span className={cn("inline-flex h-6 items-center rounded-md px-2 text-[12px] font-medium", tone.gray.chip)}>{t("workspace.byRules")}</span>
      )}
    </Row>
  );
}

function WorkspaceCard({ settings }: { settings: MailCategorySettings }) {
  const { t } = useTranslation("mailCategories");
  const confirm = useConfirm();
  const saved = useCategoriesSaved();
  const ws = settings.workspace;
  const base = useMemo(() => (ws ? { enabled: ws.enabled, categories: ws.enabled ? ws.categories : likeGmail(settings.presets), locked: ws.locked } : null), [ws, settings.presets]);
  const [draft, setDraft] = useState(base);
  // The saved set changed (here or by another admin): it replaces the form while it is not being edited.
  const [prevBase, setPrevBase] = useState(base);
  if (prevBase !== base) {
    setPrevBase(base);
    if (same(draft, prevBase)) setDraft(base);
  }
  const dirty = !same(draft, base);
  const problem = useCategoryProblem(draft?.enabled ? draft.categories : likeGmail(settings.presets));

  const save = useMutation({
    mutationFn: (d: NonNullable<typeof draft>) => api.collab.categories.setWorkspace({ enabled: d.enabled, categories: d.enabled ? withKeys(d.categories) : undefined, locked: d.locked }),
    onSuccess: (s) => {
      saved(s);
      const w = s.workspace;
      setDraft(w ? { enabled: w.enabled, categories: w.enabled ? w.categories : likeGmail(s.presets), locked: w.locked } : null);
      toast.success(t("workspace.saved"));
    },
    onError: (e) => toast.error(e, t("workspace.failed")),
  });

  const remove = () =>
    confirm.show(
      t("workspace.removeText"),
      async () => {
        const s = await api.collab.categories.removeWorkspace();
        saved(s);
        setDraft(null);
        toast.success(t("workspace.removed"));
      },
      { title: t("workspace.removeTitle"), confirmLabel: t("workspace.remove"), danger: true },
    );

  if (!draft)
    return (
      <Section title={t("workspace.title")} description={t("workspace.description")} flush>
        <SettingsEmpty
          art="inbox"
          ghost="tickets"
          title={t("empty.title")}
          description={t("empty.description")}
          action={
            <Button variant="primary" onClick={() => setDraft({ enabled: true, categories: likeGmail(settings.presets), locked: false })}>
              {t("empty.action")}
            </Button>
          }
          tips={[
            { label: t("empty.tipTeams"), icon: <Users /> },
            { label: t("empty.tipPrivate"), icon: <Lock /> },
          ]}
          className="min-h-[320px]"
        >
          <CategoryPills categories={likeGmail(settings.presets)} className="justify-center" />
        </SettingsEmpty>
      </Section>
    );

  const set = (p: Partial<NonNullable<typeof draft>>) => setDraft((d) => (d ? { ...d, ...p } : d));
  return (
    <Section title={t("workspace.title")} description={t("workspace.description")}>
      <UnsavedGuard when={dirty} />
      <Row label={t("workspace.enabled")} description={draft.enabled ? t("workspace.enabledOn") : t("workspace.enabledOff")} htmlFor="cat-ws-on">
        <Switch id="cat-ws-on" checked={draft.enabled} onCheckedChange={(enabled) => set({ enabled })} aria-label={t("workspace.enabled")} />
      </Row>
      <Reveal show={draft.enabled}>
        <Row label={t("workspace.categories")} description={t("workspace.categoriesHint")} stack>
          <CategoryEditor value={draft.categories} onChange={(categories) => set({ categories })} />
        </Row>
      </Reveal>
      <Row label={t("workspace.choose")} description={draft.locked ? t("workspace.chooseOff") : t("workspace.chooseOn")} htmlFor="cat-ws-choose">
        <Switch id="cat-ws-choose" checked={!draft.locked} onCheckedChange={(on) => set({ locked: !on })} aria-label={t("workspace.choose")} />
      </Row>
      <SortingRow sorting={settings.sorting} />
      <Footer left={dirty && draft.enabled && problem ? <p className="text-[12px] text-danger-fg">{problem}</p> : ws && !dirty ? <Updated at={ws.updated_at} by={ws.updated_by} /> : null}>
        {ws && !dirty && (
          <Button size="sm" variant="ghost" onClick={remove}>
            {t("workspace.remove")}
          </Button>
        )}
        {dirty && (
          <Button size="sm" variant="ghost" onClick={() => setDraft(base)}>
            {t("common.discard")}
          </Button>
        )}
        <Button size="sm" variant="primary" disabled={!dirty || (draft.enabled && !!problem)} loading={save.isPending} onClick={() => save.mutate(draft)}>
          {t("common.save")}
        </Button>
      </Footer>
    </Section>
  );
}

/** Content that opens and closes in place, quietly; still under reduced motion. */
function Reveal({ show, children }: { show: boolean; children: ReactNode }) {
  const reduce = useReducedMotion();
  return (
    <AnimatePresence initial={false}>
      {show && (
        <motion.div
          initial={reduce ? false : { opacity: 0, height: 0 }}
          animate={{ opacity: 1, height: "auto" }}
          exit={reduce ? { opacity: 0, transition: { duration: 0 } } : { opacity: 0, height: 0, transition: { duration: dur.fast } }}
          transition={reduce ? { duration: 0 } : springs.row}
          className="overflow-hidden"
        >
          {children}
        </motion.div>
      )}
    </AnimatePresence>
  );
}

function TeamsCard({ settings }: { settings: MailCategorySettings }) {
  const { t } = useTranslation("mailCategories");
  const qc = useQueryClient();
  const { teams } = useTeams();
  const [open, setOpen] = useState<string | null>(null);
  // A team picked but not saved yet: last in the list, open.
  const [adding, setAdding] = useState<TeamSet | null>(null);
  const [picking, setPicking] = useState(false);
  const byId = new Map(teams.map((x) => [x.id, x]));
  const sets = settings.teams.filter((s) => s.team_id !== adding?.team_id);
  const free = teams.filter((x) => !settings.teams.some((s) => s.team_id === x.id) && x.id !== adding?.team_id);
  const start = (settings.workspace?.enabled && settings.workspace.categories.length ? settings.workspace.categories : likeGmail(settings.presets)).map((c) => ({ ...c }));

  const order = useMutation({
    mutationFn: (ids: string[]) => api.collab.categories.orderTeams(ids),
    onMutate: (ids) => {
      const prev = qc.getQueryData<MailCategorySettings>(qk.mailCategories);
      if (prev) qc.setQueryData<MailCategorySettings>(qk.mailCategories, { ...prev, teams: ids.map((id) => prev.teams.find((s) => s.team_id === id)!).filter(Boolean) });
      return { prev };
    },
    onSuccess: (s) => qc.setQueryData(qk.mailCategories, s),
    onError: (e, _ids, ctx) => {
      if (ctx?.prev) qc.setQueryData(qk.mailCategories, ctx.prev);
      toast.error(e, t("teams.orderFailed"));
    },
  });

  const pick = (id: string) => {
    const team = byId.get(id);
    if (!team) return;
    const now = new Date().toISOString();
    setAdding({ team_id: id, team_name: team.name, position: settings.teams.length, enabled: true, categories: start, updated_at: now, updated_by: null });
    setOpen(id);
    setPicking(false);
  };

  const rows = (s: TeamSet, handle: ReactNode) => (
    <TeamRow
      key={s.team_id}
      set={s}
      team={byId.get(s.team_id)}
      isNew={s === adding}
      handle={handle}
      open={open === s.team_id}
      onToggle={() => setOpen((o) => (o === s.team_id ? null : s.team_id))}
      onDone={() => {
        if (s === adding) setAdding(null);
        setOpen(null);
      }}
    />
  );

  const addButton = (
    <Picker
      open={picking}
      onOpenChange={setPicking}
      align="end"
      items={teamItems(free)}
      value={null}
      onSelect={pick}
      placeholder={t("teams.search")}
      emptyText={t("teams.allHave")}
      trigger={
        <Button size="sm" icon={<Plus />} disabled={!free.length}>
          {t("teams.add")}
        </Button>
      }
    />
  );

  const empty = !sets.length && !adding;
  return (
    <Section title={t("teams.title")} description={t("teams.description")} actions={!empty && addButton} flush>
      {empty ? (
        <EmptyState compact art="settings" title={t("teams.empty")} description={t("teams.emptyHint")} action={addButton} />
      ) : (
        <>
          <SortableList
            items={sets.map((s) => ({ ...s, id: s.team_id }))}
            onReorder={(ids) => !same(ids, sets.map((s) => s.team_id)) && order.mutate(ids)}
            handleLabel={(s) => t("teams.reorder", { team: s.team_name })}
            renderRow={(s, handle) => rows(settings.teams.find((x) => x.team_id === s.team_id)!, handle)}
          />
          {adding && <div className="border-t border-line-soft">{rows(adding, null)}</div>}
          {sets.length > 1 && <p className="border-t border-line-soft bg-sunken px-4 py-2 text-[11.5px] text-fg-3">{t("teams.order")}</p>}
        </>
      )}
    </Section>
  );
}

function TeamRow({ set, team, isNew, handle, open, onToggle, onDone }: { set: TeamSet; team?: Team; isNew: boolean; handle: ReactNode; open: boolean; onToggle: () => void; onDone: () => void }) {
  const { t } = useTranslation("mailCategories");
  const confirm = useConfirm();
  const saved = useCategoriesSaved();
  const name = team?.name ?? set.team_name;
  const base: Draft = useMemo(() => ({ enabled: set.enabled, categories: set.categories }), [set]);
  const [draft, setDraft] = useState<Draft>(base);
  const [prevBase, setPrevBase] = useState(base);
  if (prevBase !== base) {
    setPrevBase(base);
    if (same(draft, prevBase)) setDraft(base);
  }
  const dirty = isNew || !same(draft, base);
  const problem = useCategoryProblem(draft.categories);

  const save = useMutation({
    mutationFn: () => api.collab.categories.setTeam(set.team_id, { enabled: draft.enabled, categories: draft.enabled ? withKeys(draft.categories) : undefined }),
    onSuccess: (s) => {
      saved(s);
      const next = s.teams.find((x) => x.team_id === set.team_id);
      if (next) setDraft({ enabled: next.enabled, categories: next.categories });
      toast.success(t("teams.saved", { team: name }));
      onDone();
    },
    onError: (e) => toast.error(e, t("workspace.failed")),
  });
  const remove = () => {
    if (isNew) return onDone();
    confirm.show(
      t("teams.removeText"),
      async () => {
        saved(await api.collab.categories.removeTeam(set.team_id));
        toast.success(t("teams.removed", { team: name }));
        onDone();
      },
      { title: t("teams.removeTitle", { team: name }), confirmLabel: t("teams.remove") },
    );
  };

  return (
    <div>
      <ListRow onClick={onToggle} className="relative min-h-11 pl-5">
        {handle}
        <span className="flex size-4 shrink-0 items-center justify-center text-fg-4 [&_svg]:size-3.5">{open ? <ChevronDown /> : <ChevronRight />}</span>
        {team ? <TeamChip team={team} /> : <span className="text-[12.5px] font-medium">{name}</span>}
        {isNew && <span className={cn("rounded-sm px-1.5 text-[10.5px] font-medium leading-[18px]", tone.blue.chip)}>{t("common.new")}</span>}
        <span className="ml-auto flex min-w-0 justify-end">
          {set.enabled ? <CategoryPills categories={set.categories} className="justify-end" /> : <span className="text-[12px] text-fg-3">{t("teams.summaryOff")}</span>}
        </span>
      </ListRow>
      <Reveal show={open}>
        <div className="border-b border-line-soft bg-sunken/40 px-4 last:border-0">
          <Row label={t("teams.enabled", { team: name })} description={draft.enabled ? t("workspace.enabledOn") : t("teams.enabledOff")} htmlFor={`cat-team-${set.team_id}`}>
            <Switch id={`cat-team-${set.team_id}`} checked={draft.enabled} onCheckedChange={(enabled) => setDraft((d) => ({ ...d, enabled }))} aria-label={t("teams.enabled", { team: name })} />
          </Row>
          <Reveal show={draft.enabled}>
            <div className="pb-3.5">
              <CategoryEditor value={draft.categories} onChange={(categories) => setDraft((d) => ({ ...d, categories }))} />
            </div>
          </Reveal>
          <Footer left={!isNew && !dirty ? <Updated at={set.updated_at} by={set.updated_by} /> : null}>
            <Button size="sm" variant="ghost" onClick={remove}>
              {isNew ? t("common.cancel") : t("teams.remove")}
            </Button>
            {dirty && !isNew && (
              <Button size="sm" variant="ghost" onClick={() => setDraft(base)}>
                {t("common.discard")}
              </Button>
            )}
            <Button size="sm" variant="primary" disabled={!dirty || (draft.enabled && !!problem)} loading={save.isPending} onClick={() => save.mutate()}>
              {t("common.save")}
            </Button>
          </Footer>
        </div>
      </Reveal>
    </div>
  );
}

/** For members: what the workspace and their teams set, and their accounts as they are split now. */
function MemberView({ settings }: { settings: MailCategorySettings }) {
  const { t } = useTranslation("mailCategories");
  const { data: accounts } = useMailAccounts();
  const mine = (accounts ?? []).filter((a) => a.can_manage);
  const ws = settings.workspace;
  const nothing = !ws && !settings.teams.length;
  const open = (
    <Button asChild variant={nothing ? "primary" : "secondary"}>
      <Link to="/settings/mail-accounts">{t("member.open")}</Link>
    </Button>
  );
  return (
    <>
      {nothing ? (
        <Section flush>
          <SettingsEmpty art="inbox" ghost="tickets" title={t("member.emptyTitle")} description={t("member.emptyDescription")} action={open} />
        </Section>
      ) : (
        <Section title={t("member.applies")} description={ws?.locked ? t("member.locked") : t("member.free")}>
          <Row label={t("member.workspace")}>{ws ? ws.enabled ? <CategoryPills categories={ws.categories} className="justify-end" /> : <span className="text-[12px] text-fg-3">{t("common.off")}</span> : <span className="text-[12px] text-fg-3">{t("member.none")}</span>}</Row>
          {settings.teams.map((s) => (
            <Row key={s.team_id} label={s.team_name}>
              {s.enabled ? <CategoryPills categories={s.categories} className="justify-end" /> : <span className="text-[12px] text-fg-3">{t("common.off")}</span>}
            </Row>
          ))}
        </Section>
      )}
      {mine.length > 0 && (
        <Section title={t("member.accounts")} description={t("member.accountsHint")} flush>
          {mine.map((a) => (
            <Link key={a.id} to="/settings/mail-accounts/$accountId" params={{ accountId: a.id }} className="block outline-none focus-visible:bg-sunken">
              <ListRow className="cursor-pointer transition-colors hover:bg-sunken">
                <AccountDot color={a.color} />
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-[12.5px] font-medium">{a.name || a.email}</span>
                  <span className="block truncate text-[11.5px] text-fg-3">{sourceText(t, a.categories)}</span>
                </span>
                {a.categories.enabled ? <CategoryPills categories={a.categories.categories} className="hidden justify-end sm:flex" /> : <span className="text-[12px] text-fg-4">{t("member.notSplit")}</span>}
                <ChevronRight className="size-3.5 shrink-0 text-fg-4" />
              </ListRow>
            </Link>
          ))}
        </Section>
      )}
      <Note icon={<ShieldCheck className="mt-px size-3.5 shrink-0 text-fg-4" />}>{t("privacy")}</Note>
    </>
  );
}
