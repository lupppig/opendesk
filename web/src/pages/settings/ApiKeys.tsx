import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { EyeOff, KeyRound, MoreHorizontal, Pencil, Plus, ShieldCheck, Trash2 } from "lucide-react";
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { NoAccess, Section, SettingsPage } from "@/components/layout";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Field, TextInput } from "@/components/ui/input";
import { Checkbox } from "@/components/ui/checkbox";
import { Select } from "@/components/ui/select";
import { Segmented } from "@/components/ui/tabs";
import { SettingsEmpty } from "@/components/settings2/SettingsEmpty";
import { CopyField } from "@/components/ui/copy-button";
import { Tooltip } from "@/components/ui/tooltip";
import { Avatar } from "@/components/ui/avatar";
import { Menu, MenuContent, MenuItem, MenuSeparator, MenuTrigger } from "@/components/ui/menu";
import { useConfirm } from "@/components/ui/confirm";
import { IconTile, ListSkeleton, MonoChip, StatePill, SummaryStrip } from "@/components/settings2/bits";
import { MotionList, MotionRow } from "@/components/settings2/motion";
import { useListMutation } from "@/components/settings2/optimistic";
import { GrantedTeams, PrivateTeamsField } from "@/components/settings2/teams";
import { ReauthNotice, isReauthError } from "@/components/settings/ReauthNotice";
import { api } from "@/lib/api/endpoints";
import { errorMessage } from "@/lib/api/client";
import type { ApiKey } from "@/lib/api/types";
import { fullDate, relLong, shortDate } from "@/lib/format";
import { q, qk, upsertInList } from "@/lib/queries";
import { atLeast, useRole } from "@/lib/session";
import { cn } from "@/lib/cn";

const scopeGroups: { resource: string; label: string; scopes: { id: string; label: string }[] }[] = [
  { resource: "tickets", label: "Tickets", scopes: [{ id: "tickets:read", label: "Read" }, { id: "tickets:write", label: "Write" }] },
  { resource: "contacts", label: "Contacts", scopes: [{ id: "contacts:read", label: "Read" }, { id: "contacts:write", label: "Write" }] },
  { resource: "kb", label: "Knowledge base", scopes: [{ id: "kb:read", label: "Read" }, { id: "kb:write", label: "Write" }] },
  { resource: "changelog", label: "Changelog", scopes: [{ id: "changelog:read", label: "Read" }, { id: "changelog:write", label: "Write" }] },
  { resource: "wiki", label: "Wiki", scopes: [{ id: "wiki:read", label: "Read" }, { id: "wiki:write", label: "Write" }] },
  { resource: "settings", label: "Settings", scopes: [{ id: "settings:read", label: "Read" }, { id: "settings:write", label: "Write" }] },
  { resource: "realtime", label: "Realtime", scopes: [{ id: "realtime", label: "Subscribe" }] },
  // Acts as the key's creator in Collab: reads the mail they see and saves reply drafts, never sends.
  { resource: "collab", label: "Collab (as you)", scopes: [{ id: "collab:draft", label: "Draft replies" }] },
];
// collab:draft reaches the creator's mail, so no preset includes it: it is chosen by hand.
const optInScopes = ["collab:draft"];
const allScopes = scopeGroups.flatMap((g) => g.scopes.map((s) => s.id)).filter((s) => !optInScopes.includes(s));
const readScopes = allScopes.filter((s) => s.endsWith(":read"));

type Expiry = "never" | "30" | "90" | "365";

export default function ApiKeys() {
  const { t } = useTranslation("settingsDev");
  const role = useRole();
  const admin = atLeast(role, "admin");
  const { data: keys, isLoading } = useQuery({ ...q.apiKeys(), enabled: admin });
  const confirm = useConfirm();
  const [creating, setCreating] = useState(false);
  const [created, setCreated] = useState<(ApiKey & { secret: string }) | null>(null);
  const [renaming, setRenaming] = useState<string | null>(null);

  const revoke = useListMutation<ApiKey, string>(qk.apiKeys, (id) => api.apiKeys.remove(id), (rows, id) => rows.filter((k) => k.id !== id), {
    errorText: t("apiKeys.revokeError"),
  });
  const rename = useListMutation<ApiKey, { id: string; name: string }>(
    qk.apiKeys,
    (v) => api.apiKeys.update(v.id, { name: v.name }),
    (rows, v) => rows.map((k) => (k.id === v.id ? { ...k, name: v.name } : k)),
    { errorText: t("apiKeys.renameError") },
  );

  if (!admin)
    return (
      <SettingsPage title={t("apiKeys.title")}>
        <NoAccess what="API keys" />
      </SettingsPage>
    );

  const sorted = [...(keys ?? [])].sort((a, b) => b.created_at.localeCompare(a.created_at));
  const now = Date.now();
  const counts = sorted.reduce(
    (c, k) => {
      const s = keyState(k, now);
      c[s] += 1;
      if (!k.last_used_at) c.unused += 1;
      return c;
    },
    { active: 0, expiring: 0, expired: 0, unused: 0 },
  );

  return (
    <SettingsPage
      wide
      title={t("apiKeys.title")}
      description={
        <>
          {t("apiKeys.description")} <span className="mono text-fg-2">Authorization: Bearer od_live_…</span>
        </>
      }
      actions={
        <Button variant="primary" icon={<Plus className="size-3.5" />} onClick={() => setCreating(true)}>
          {t("apiKeys.new")}
        </Button>
      }
    >
      {sorted.length > 0 && (
        <SummaryStrip
          items={[
            { label: t("apiKeys.summary.active"), value: counts.active + counts.expiring, tone: counts.active + counts.expiring ? "green" : undefined },
            { label: t("apiKeys.summary.expiring"), value: counts.expiring, tone: counts.expiring ? "amber" : undefined, hint: t("apiKeys.summary.expiringHint") },
            { label: t("apiKeys.summary.unused"), value: counts.unused },
            { label: t("apiKeys.summary.expired"), value: counts.expired, tone: counts.expired ? "gray" : undefined },
          ]}
        />
      )}
      <Section flush title={keys ? t("apiKeys.count", { count: sorted.length }) : undefined}>
        {isLoading ? (
          <ListSkeleton rows={3} />
        ) : !sorted.length ? (
          <SettingsEmpty
            art="settings"
            title={t("apiKeys.empty.title")}
            description={t("apiKeys.empty.description")}
            action={
              <Button variant="primary" icon={<Plus className="size-3.5" />} onClick={() => setCreating(true)}>
                {t("apiKeys.new")}
              </Button>
            }
            tips={[
              { label: t("apiKeys.empty.tips.scopes"), icon: <ShieldCheck /> },
              { label: t("apiKeys.empty.tips.once"), icon: <EyeOff /> },
            ]}
          />
        ) : (
          <>
            <div className="hidden h-8 items-center gap-4 border-b border-line-soft bg-sunken px-4 md:grid md:grid-cols-[minmax(0,1fr)_130px_130px_170px_28px]">
              <span className="label-caps">{t("apiKeys.col.key")}</span>
              <span className="label-caps">{t("apiKeys.col.lastUsed")}</span>
              <span className="label-caps">{t("apiKeys.col.expires")}</span>
              <span className="label-caps">{t("apiKeys.col.createdBy")}</span>
              <span />
            </div>
            <MotionList>
              {sorted.map((k) => {
                const state = keyState(k, now);
                return (
                  <MotionRow key={k.id}>
                    <div className="group relative flex flex-col gap-2 px-4 py-3 transition-colors hover:bg-sunken md:grid md:grid-cols-[minmax(0,1fr)_130px_130px_170px_28px] md:items-center md:gap-4">
                      <div className="flex min-w-0 items-center gap-3">
                        <IconTile tone={state === "expired" ? "gray" : "violet"}>
                          <KeyRound />
                        </IconTile>
                        <div className="min-w-0 flex-1">
                          <div className="flex min-w-0 items-center gap-2">
                            {renaming === k.id ? (
                              <InlineName
                                initial={k.name}
                                onDone={(name) => {
                                  setRenaming(null);
                                  if (name && name !== k.name) rename.mutate({ id: k.id, name });
                                }}
                              />
                            ) : (
                              <span className="truncate text-[13px] font-medium">{k.name}</span>
                            )}
                            {state === "expired" ? (
                              <StatePill state="expired">{t("apiKeys.state.expired")}</StatePill>
                            ) : state === "expiring" ? (
                              <StatePill state="pending">{t("apiKeys.state.expiring")}</StatePill>
                            ) : (
                              <StatePill state="active">{t("apiKeys.state.active")}</StatePill>
                            )}
                          </div>
                          <div className="mt-1 flex flex-wrap items-center gap-1">
                            <span className="mono mr-1 text-[11px] text-fg-4">{k.prefix.slice(0, 12)}…</span>
                            <ScopeChips scopes={k.scopes} />
                          </div>
                          <GrantedTeams grants={k.private_teams} className="mt-1.5" />
                        </div>
                      </div>
                      <Tooltip content={k.last_used_at ? fullDate(k.last_used_at) : t("apiKeys.neverUsed")}>
                        <span className={cn("text-[12px] max-md:pl-11", k.last_used_at ? "text-fg-2" : "text-fg-4")}>
                          {k.last_used_at ? relLong(k.last_used_at) : t("apiKeys.neverUsed")}
                        </span>
                      </Tooltip>
                      <span className={cn("text-[12px] max-md:pl-11", state === "expired" ? "text-fg-4" : state === "expiring" ? "text-tone-amber" : "text-fg-2")}>
                        {k.expires_at ? shortDate(k.expires_at) : t("apiKeys.noExpiry")}
                      </span>
                      <span className="flex min-w-0 items-center gap-1.5 text-[12px] text-fg-3 max-md:pl-11">
                        {k.created_by ? (
                          <>
                            <Avatar name={k.created_by.name} src={k.created_by.avatar_url} size="xs" />
                            <span className="truncate text-fg-2">{k.created_by.name}</span>
                            <span className="shrink-0 text-fg-4">{shortDate(k.created_at)}</span>
                          </>
                        ) : (
                          shortDate(k.created_at)
                        )}
                      </span>
                      <Menu>
                        <MenuTrigger asChild>
                          <button
                            type="button"
                            aria-label={`Actions for ${k.name}`}
                            className="flex size-7 items-center justify-center rounded-md text-fg-3 transition-colors hover:bg-hover hover:text-fg max-md:absolute max-md:right-3 max-md:top-3"
                          >
                            <MoreHorizontal className="size-4" />
                          </button>
                        </MenuTrigger>
                        <MenuContent>
                          <MenuItem icon={<Pencil />} onSelect={() => setRenaming(k.id)}>
                            {t("apiKeys.rename")}
                          </MenuItem>
                          <MenuSeparator />
                          <MenuItem
                            danger
                            icon={<Trash2 />}
                            onSelect={() =>
                              confirm.show(
                                <>
                                  {t("apiKeys.revokeBodyPrefix")} <span className="font-medium text-fg">{k.name}</span> {t("apiKeys.revokeBodySuffix")}
                                </>,
                                () => revoke.mutateAsync(k.id),
                                { title: t("apiKeys.revokeTitle"), confirmLabel: t("apiKeys.revokeConfirm"), danger: true },
                              )
                            }
                          >
                            {t("apiKeys.revoke")}
                          </MenuItem>
                        </MenuContent>
                      </Menu>
                    </div>
                  </MotionRow>
                );
              })}
            </MotionList>
          </>
        )}
      </Section>

      {creating && (
        <CreateKeyDialog
          onClose={() => setCreating(false)}
          onCreated={(k) => {
            setCreating(false);
            setCreated(k);
          }}
        />
      )}
      <Dialog
        open={!!created}
        onOpenChange={(o) => !o && setCreated(null)}
        title={t("apiKeys.created.title")}
        description={t("apiKeys.created.description")}
        footer={
          <Button variant="primary" onClick={() => setCreated(null)}>
            {t("apiKeys.created.done")}
          </Button>
        }
      >
        <div className="flex flex-col gap-3 pb-1">
          <CopyField value={created?.secret ?? ""} />
          <p className="flex items-start gap-2 rounded-md bg-tone-amber-soft px-3 py-2 text-[12px] text-tone-amber">
            <span className="mt-1.5 size-1.5 shrink-0 rounded-full bg-tone-amber-solid" />
            {t("apiKeys.created.warning")}
          </p>
        </div>
      </Dialog>
    </SettingsPage>
  );
}

/** Scopes, compact: a preset reads as one chip; write access is tinted so it stands out. */
function ScopeChips({ scopes }: { scopes: string[] }) {
  const { t } = useTranslation("settingsDev");
  const set = new Set(scopes);
  if (allScopes.every((s) => set.has(s))) return <MonoChip className="border-tone-amber-line text-tone-amber">{t("apiKeys.scopeFull")}</MonoChip>;
  if (scopes.length === readScopes.length && readScopes.every((s) => set.has(s))) return <MonoChip>{t("apiKeys.scopeRead")}</MonoChip>;
  return (
    <>
      {scopes.map((s) => (
        <MonoChip key={s} className={s.endsWith(":write") ? "border-tone-amber-line text-tone-amber" : undefined}>
          {s}
        </MonoChip>
      ))}
    </>
  );
}

const SOON = 14 * 86400_000;
function keyState(k: ApiKey, now: number): "active" | "expiring" | "expired" {
  if (!k.expires_at) return "active";
  const at = Date.parse(k.expires_at);
  if (at < now) return "expired";
  return at - now < SOON ? "expiring" : "active";
}

function InlineName({ initial, onDone }: { initial: string; onDone: (v: string | null) => void }) {
  const { t: ui } = useTranslation("ui");
  const [v, setV] = useState(initial);
  const cancelled = useRef(false);
  return (
    <TextInput
      autoFocus
      inputSize="sm"
      value={v}
      maxLength={80}
      aria-label={ui("keyName")}
      className="max-w-[260px]"
      onChange={(e) => setV(e.target.value)}
      onBlur={() => onDone(cancelled.current ? null : v.trim())}
      onKeyDown={(e) => {
        if (e.key === "Enter") (e.target as HTMLInputElement).blur();
        if (e.key === "Escape") {
          e.stopPropagation();
          cancelled.current = true;
          (e.target as HTMLInputElement).blur();
        }
      }}
    />
  );
}

export function CreateKeyDialog({ onClose, onCreated }: { onClose: () => void; onCreated: (k: ApiKey & { secret: string }) => void }) {
  const { t } = useTranslation("settingsDev");
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [scopes, setScopes] = useState<string[]>(readScopes);
  const [expiry, setExpiry] = useState<Expiry>("90");
  const [privateTeams, setPrivateTeams] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);

  const preset = scopes.length === allScopes.length ? "full" : scopes.length === readScopes.length && readScopes.every((s) => scopes.includes(s)) ? "read" : "custom";

  const create = useMutation({
    mutationFn: () =>
      api.apiKeys.create({
        name: name.trim(),
        scopes,
        expires_at: expiry === "never" ? null : new Date(Date.now() + Number(expiry) * 86400_000).toISOString(),
        ...(privateTeams.length ? { private_team_ids: privateTeams } : {}),
      }),
    onSuccess: (k) => {
      const { secret: _secret, ...row } = k;
      upsertInList(qc, qk.apiKeys, row as ApiKey, { prepend: true });
      onCreated(k);
    },
    // A key needs a recent sign-in (with 2FA when on): the notice below says why and signs in again.
    onError: (e) => {
      if (!isReauthError(e)) setError(errorMessage(e));
    },
  });

  const toggle = (id: string, on: boolean) => setScopes((s) => (on ? [...new Set([...s, id])] : s.filter((x) => x !== id)));

  const submit = () => {
    setError(null);
    if (!name.trim()) return setError(t("apiKeys.form.nameRequired"));
    if (!scopes.length) return setError(t("apiKeys.form.scopeRequired"));
    create.mutate();
  };

  return (
    <Dialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={t("apiKeys.form.title")}
      footer={
        <>
          <span className="mr-auto text-xs text-danger-fg">{error}</span>
          <Button variant="ghost" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button variant="primary" loading={create.isPending} onClick={submit}>
            {t("apiKeys.form.create")}
          </Button>
        </>
      }
    >
      <form
        className="flex flex-col gap-4 pb-1"
        onSubmit={(e) => {
          e.preventDefault();
          submit();
        }}
      >
        <Field label={t("apiKeys.form.name")}>
          {(p) => <TextInput {...p} autoFocus value={name} maxLength={80} placeholder={t("apiKeys.form.namePlaceholder")} onChange={(e) => setName(e.target.value)} />}
        </Field>
        <div className="flex flex-col gap-2">
          <div className="flex items-center justify-between">
            <span className="text-[12.5px] font-medium">{t("apiKeys.form.scopes")}</span>
            <Segmented
              size="xs"
              value={preset}
              onChange={(v) => {
                if (v === "read") setScopes(readScopes);
                if (v === "full") setScopes(allScopes);
              }}
              options={[
                { value: "read", label: t("apiKeys.form.readOnly") },
                { value: "full", label: t("apiKeys.form.full") },
                { value: "custom", label: t("apiKeys.form.custom") },
              ]}
            />
          </div>
          <div className="overflow-hidden rounded-md border border-line">
            {scopeGroups.map((g) => (
              <div key={g.resource} className="flex h-10 items-center gap-3 border-b border-line-soft px-3 last:border-0">
                <span className="flex-1 text-[12.5px] text-fg">{g.label}</span>
                {g.scopes.map((s) => (
                  <label key={s.id} className={cn("flex items-center gap-1.5 text-[12px]", scopes.includes(s.id) ? "text-fg" : "text-fg-3")}>
                    <Checkbox checked={scopes.includes(s.id)} onCheckedChange={(on) => toggle(s.id, on)} aria-label={`${g.label} ${s.label.toLowerCase()}`} />
                    {s.label}
                  </label>
                ))}
              </div>
            ))}
          </div>
        </div>
        <Field label={t("apiKeys.form.expires")}>
          {(p) => (
            <Select<Expiry>
              id={p.id}
              value={expiry}
              onChange={setExpiry}
              options={[
                { value: "30", label: t("apiKeys.form.in30") },
                { value: "90", label: t("apiKeys.form.in90") },
                { value: "365", label: t("apiKeys.form.in365") },
                { value: "never", label: t("apiKeys.form.never") },
              ]}
            />
          )}
        </Field>
        <PrivateTeamsField value={privateTeams} onChange={setPrivateTeams} />
        {isReauthError(create.error) && <ReauthNotice error={create.error} />}
        <button type="submit" className="hidden" />
      </form>
    </Dialog>
  );
}
