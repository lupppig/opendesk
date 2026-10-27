import { useMutation, useQuery } from "@tanstack/react-query";
import { Check, Volume2 } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { SaveBar } from "@/components/settings/SaveBar";
import { RolePill } from "@/components/settings2/bits";
import { ClockSetting } from "@/components/settings2/ClockSetting";
import { MemberProfileFields, profileChanges, profileDirty, profileOf, useSaveProfile } from "@/components/settings2/MemberProfileFields";
import { UnsavedGuard } from "@/components/settings2/UnsavedGuard";
import { SettingsPage, Section, Row } from "@/components/layout";
import { Avatar } from "@/components/ui/avatar";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ImageUpload } from "@/components/ui/image-upload";
import { Select } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { TextInput } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast";
import { playAlert, setAlertSound, setSoundPref, useAlertSound, useSoundPrefs, type AlertSound, type SoundPref } from "@/lib/alertSound";
import { api } from "@/lib/api/endpoints";
import type { MemberProfile, User } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { setAssigneeDisplay, useAssigneeDisplay, type AssigneeDisplay } from "@/lib/listDisplay";
import { useMyAvatar } from "@/lib/pictures";
import { q } from "@/lib/queries";
import { updateUser, useRole, useSession } from "@/lib/session";
import { useTheme, type ThemePref } from "@/lib/theme";
import { setConversationLayout, useConversationLayout } from "@/lib/conversationLayout";

export default function Profile() {
  const { t } = useTranslation("settings");
  const user = useSession((s) => s.user)!;
  const role = useRole();
  const workspace = useSession((s) => s.memberships.find((m) => m.workspace.id === s.workspaceId)?.workspace.name);
  const [name, setName] = useState(user.name);
  const picture = useMyAvatar();
  useEffect(() => {
    setName(user.name);
  }, [user.name]);

  // The profile customers see, per workspace, from the members list.
  const { data: members, isLoading: membersLoading } = useQuery(q.members());
  const me = members?.find((m) => m.user.id === user.id);
  const base = useMemo(() => profileOf(me), [me]);
  const [profile, setProfile] = useState<MemberProfile>(base);
  const [prevBase, setPrevBase] = useState(base);
  if (base !== prevBase) {
    // Saved elsewhere (or the list just came in): take it unless we are mid-edit.
    // Set while rendering, so the signature editor starts with the saved signature.
    setPrevBase(base);
    if (!profileDirty(profile, prevBase)) setProfile(base);
  }

  const nameDirty = name.trim() !== user.name;
  const profileChanged = profileDirty(profile, base);
  const dirty = nameDirty || profileChanged;

  const save = useMutation({
    mutationFn: () => api.me.update({ name: name.trim() }),
    onMutate: () => {
      const prev: User = user;
      updateUser({ ...user, name: name.trim() });
      return { prev };
    },
    onError: (e, _v, ctx) => {
      if (ctx) updateUser(ctx.prev);
      toast.error(e);
    },
    onSuccess: (u) => {
      updateUser(u);
      toast.success(t("profile.saved"));
    },
  });
  const saveProfile = useSaveProfile(user.id, true);

  const submit = () => {
    if (nameDirty && name.trim()) save.mutate();
    if (profileChanged) {
      // A refused save rolls the list back; the edits stay to fix.
      const edits = profile;
      saveProfile.mutate(profileChanges(profile, base), { onError: () => setProfile(edits) });
    }
  };
  const discard = () => {
    setName(user.name);
    setProfile(base);
    saveProfile.clearProblems();
  };

  const resend = useMutation({
    mutationFn: api.auth.resendVerification,
    onSuccess: () => toast.success(t("profile.verifySent"), { description: t("profile.verifySentHint") }),
    onError: (e) => toast.error(e),
  });

  return (
    <SettingsPage title={t("profile.title")} description={t("profile.description")}>
      <UnsavedGuard when={dirty && !save.isPending && !saveProfile.isPending} />
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (dirty && name.trim()) submit();
        }}
      >
        <Section>
          <div className="flex items-center gap-4 border-b border-line-soft py-4">
            <Avatar name={name || user.email} src={user.avatar_url} size="xl" />
            <div className="min-w-0 flex-1">
              <div className="truncate text-[14px] font-semibold tracking-[-0.01em]">{name.trim() || user.email}</div>
              <div className="mono mt-0.5 truncate text-xs text-fg-3">{user.email}</div>
            </div>
            {role && (
              <span className="flex shrink-0 items-center gap-2 text-xs text-fg-3">
                {workspace}
                <RolePill role={role} />
              </span>
            )}
          </div>
          <Row label={t("profile.name")} htmlFor="profile-name">
            <TextInput id="profile-name" className="w-[280px]" value={name} onChange={(e) => setName(e.target.value)} maxLength={80} />
          </Row>
          <Row label={t("profile.photo")} description={t("profile.photoHint")}>
            <ImageUpload
              name={name.trim() || user.email}
              shape="round"
              size="md"
              value={user.avatar_url}
              source={user.avatar_source}
              onChange={async (blob) => {
                await picture.upload.mutateAsync(blob);
              }}
              onRemove={async () => {
                await picture.remove.mutateAsync();
              }}
            />
          </Row>
          <Row
            label={t("profile.email")}
            description={user.email_verified ? t("profile.emailHint") : t("profile.emailUnverified")}
          >
            <span className="mono text-[12.5px] text-fg-2">{user.email}</span>
            {user.email_verified ? (
              <Badge tone="ok">{t("profile.verified")}</Badge>
            ) : (
              <Button size="xs" loading={resend.isPending} onClick={() => resend.mutate()}>
                {t("profile.resend")}
              </Button>
            )}
          </Row>
        </Section>
      </form>

      <Section title={t("memberProfile.title")} description={t("memberProfile.description", { workspace })}>
        {membersLoading ? (
          // The editor picks visual or HTML from the signature it starts with, so it waits for it.
          <div className="flex flex-col gap-3 py-3.5">
            {[36, 36, 36, 140].map((h, i) => (
              <Skeleton key={i} className="w-full rounded-md" style={{ height: h }} />
            ))}
          </div>
        ) : (
          <MemberProfileFields
            value={profile}
            onChange={(p) => {
              setProfile(p);
              saveProfile.clearProblems();
            }}
            problems={saveProfile.problems}
          />
        )}
      </Section>

      <Section title={t("profile.appearance")} description={t("profile.appearanceHint")}>
        <div className="border-b border-line-soft py-3">
          <ThemeCards />
        </div>
        <AssigneeSetting />
        <ConversationLayout />
      </Section>

      <Section title={t("profile.clock.title")} description={t("profile.clock.hint")}>
        <ClockSetting />
      </Section>

      <Section title={t("profile.sounds.title")} description={t("profile.sounds.hint")}>
        <Sounds />
      </Section>

      <SaveBar dirty={dirty} saving={save.isPending || saveProfile.isPending} disabled={!name.trim()} onSave={submit} onDiscard={discard} />
    </SettingsPage>
  );
}

/** Each sound on its own switch, with a way to hear it first. Stored per browser. */
function Sounds() {
  const { t } = useTranslation("settings");
  const chat = useAlertSound();
  const prefs = useSoundPrefs();
  const rows: { key: "chat" | SoundPref; sound: AlertSound; on: boolean; set: (v: boolean) => void }[] = [
    { key: "chat", sound: "waiting", on: chat, set: setAlertSound },
    { key: "ticket", sound: "ticket", on: prefs.ticket, set: (v) => setSoundPref("ticket", v) },
    { key: "reply", sound: "reply", on: prefs.reply, set: (v) => setSoundPref("reply", v) },
    { key: "assigned", sound: "assigned", on: prefs.assigned, set: (v) => setSoundPref("assigned", v) },
  ];
  return (
    <div>
      {rows.map((r) => (
        <Row key={r.key} label={t(`profile.sounds.${r.key}`)} description={t(`profile.sounds.${r.key}Hint`)}>
          <div className="flex items-center gap-2">
            <Button variant="ghost" size="sm" square aria-label={t("profile.sounds.preview")} title={t("profile.sounds.preview")} onClick={() => playAlert(r.sound)} icon={<Volume2 className="size-3.5" />} />
            <Switch checked={r.on} onCheckedChange={r.set} aria-label={t(`profile.sounds.${r.key}`)} />
          </div>
        </Row>
      ))}
    </div>
  );
}

/** How the ticket list shows the assignee. Stored per browser, like the theme. */
function AssigneeSetting() {
  const { t } = useTranslation("settings");
  const mode = useAssigneeDisplay();
  return (
    <Row label={t("profile.assignee.label")} description={t("profile.assignee.hint")} htmlFor="list-assignee">
      <Select<AssigneeDisplay>
        id="list-assignee"
        aria-label={t("profile.assignee.label")}
        value={mode}
        onChange={setAssigneeDisplay}
        className="w-48"
        options={[
          { value: "photo", label: t("profile.assignee.photo") },
          { value: "name", label: t("profile.assignee.name") },
          { value: "none", label: t("profile.assignee.none") },
        ]}
      />
    </Row>
  );
}

/** The ticket timeline as a conversation: the customer's messages on one side, the team's on the other. */
function ConversationLayout() {
  const { t } = useTranslation("settings");
  const on = useConversationLayout();
  return (
    <Row label={t("profile.conversation.label")} description={t("profile.conversation.hint")}>
      <Switch checked={on} onCheckedChange={setConversationLayout} aria-label={t("profile.conversation.label")} />
    </Row>
  );
}

function ThemeCards() {
  const { t } = useTranslation("settings");
  const { pref, setTheme } = useTheme();
  const opts: { value: ThemePref; label: string }[] = [
    { value: "light", label: t("profile.theme.light") },
    { value: "dark", label: t("profile.theme.dark") },
    { value: "system", label: t("profile.theme.system") },
  ];
  return (
    <div role="radiogroup" aria-label={t("profile.theme.label")} className="grid grid-cols-3 gap-3">
      {opts.map((o) => (
        <button
          key={o.value}
          type="button"
          role="radio"
          aria-checked={pref === o.value}
          onClick={() => setTheme(o.value)}
          className={cn(
            "group flex flex-col gap-2 rounded-lg border p-2 text-left transition-colors",
            pref === o.value ? "border-fg-2 shadow-[0_0_0_1px_var(--fg-2)]" : "border-line hover:border-line-strong",
          )}
        >
          <ThemePreview kind={o.value} />
          <span className="flex items-center justify-between px-0.5 text-[12.5px] font-medium">
            {o.label}
            {pref === o.value && <Check className="size-3.5" />}
          </span>
        </button>
      ))}
    </div>
  );
}

function Mini({ dark }: { dark: boolean }) {
  const bg = dark ? "#0a0a0a" : "#fafafa";
  const panel = dark ? "#0f0f0f" : "#ffffff";
  const line = dark ? "#262626" : "#e4e4e4";
  const ink = dark ? "#fafafa" : "#0a0a0a";
  const muted = dark ? "#333333" : "#e4e4e4";
  return (
    <svg viewBox="0 0 120 72" className="h-full w-full" aria-hidden>
      <rect width="120" height="72" fill={bg} />
      <rect x="6" y="8" width="22" height="3" rx="1.5" fill={ink} opacity="0.8" />
      <rect x="6" y="16" width="18" height="2.5" rx="1.25" fill={muted} />
      <rect x="6" y="22" width="20" height="2.5" rx="1.25" fill={muted} />
      <rect x="6" y="28" width="15" height="2.5" rx="1.25" fill={muted} />
      <rect x="34" y="4" width="82" height="64" rx="4" fill={panel} stroke={line} />
      <rect x="40" y="12" width="40" height="3" rx="1.5" fill={ink} opacity="0.85" />
      <rect x="40" y="22" width="68" height="2.5" rx="1.25" fill={muted} />
      <rect x="40" y="28" width="60" height="2.5" rx="1.25" fill={muted} />
      <rect x="40" y="34" width="64" height="2.5" rx="1.25" fill={muted} />
      <rect x="86" y="54" width="24" height="8" rx="2" fill={ink} />
    </svg>
  );
}

function ThemePreview({ kind }: { kind: ThemePref }) {
  return (
    <span className="relative block aspect-[5/3] overflow-hidden rounded-md border border-line">
      {kind === "system" ? (
        <>
          <span className="absolute inset-0">
            <Mini dark={false} />
          </span>
          <span className="absolute inset-0 [clip-path:polygon(55%_0,100%_0,100%_100%,45%_100%)]">
            <Mini dark />
          </span>
        </>
      ) : (
        <Mini dark={kind === "dark"} />
      )}
    </span>
  );
}
