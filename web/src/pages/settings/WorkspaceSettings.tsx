import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { ArrowRight, ChevronDown, ExternalLink } from "lucide-react";
import { Link } from "@tanstack/react-router";
import { ImageUpload } from "@/components/ui/image-upload";
import { useWorkspaceImage } from "@/lib/pictures";
import type { WorkspaceImageSlot } from "@/lib/api/types";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { DisplayOverridesEditor, type Overrides } from "@/components/settings2/DisplayOverrides";
import { SettingsPage, Section, Row } from "@/components/layout";
import { Avatar } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/ui/confirm";
import { CopyButton } from "@/components/ui/copy-button";
import { Dialog } from "@/components/ui/dialog";
import { TextInput } from "@/components/ui/input";
import { Picker } from "@/components/ui/picker";
import { Switch } from "@/components/ui/switch";
import { Segmented } from "@/components/ui/tabs";
import { UNDO_SEND_CHAT_DEFAULT, UNDO_SEND_CHOICES, UNDO_SEND_DEFAULT } from "@/components/tickets/detail/outbox";
import { Skeleton } from "@/components/ui/skeleton";
import { toast } from "@/components/ui/toast";
import { ReauthNotice } from "@/components/settings/ReauthNotice";
import { SaveBar } from "@/components/settings/SaveBar";
import { StarterSpaceSection } from "@/components/spaces/StarterSpaceSection";
import { useBrands } from "@/components/settings2/teams";
import { bareUrl, defaultHelpUrl, helpCenterHref } from "@/lib/helpCenter";
import { api } from "@/lib/api/endpoints";
import type { Priority, TicketStatus, Workspace, WorkspaceSettings as WS } from "@/lib/api/types";
import { q, qk } from "@/lib/queries";
import { atLeast, reloadSession, updateWorkspace, useRole, useSession } from "@/lib/session";
import { LOCALE_NAMES, matchLocale, SUPPORTED } from "@/i18n";

type DisplaySettings = { status_labels?: Overrides<TicketStatus>; priority_labels?: Overrides<Priority> };
type Draft = {
  name: string;
  settings: Required<Pick<WS, "timezone" | "help_center_enabled" | "help_center_title" | "csat_enabled" | "gravatar" | "domain_logos" | "undo_send_seconds" | "undo_send_chat_seconds" | "message_delete" | "collab_ticket_replies" | "collab_ticket_reply_to">> & {
    brand_color: string;
    locale: string;
    locale_fixed: boolean;
  };
  display: Required<DisplaySettings>;
};

function toDraft(w: Workspace): Draft {
  const extra: DisplaySettings = w.settings;
  return {
    display: { status_labels: extra.status_labels ?? {}, priority_labels: extra.priority_labels ?? {} },
    name: w.name,
    settings: {
      timezone: w.settings.timezone ?? "UTC",
      help_center_enabled: w.settings.help_center_enabled ?? false,
      help_center_title: w.settings.help_center_title ?? "",
      csat_enabled: w.settings.csat_enabled ?? false,
      gravatar: w.settings.gravatar ?? false,
      domain_logos: w.settings.domain_logos ?? true,
      brand_color: w.settings.brand_color ?? "",
      locale: matchLocale(w.settings.locale) ?? "en",
      locale_fixed: w.settings.locale_fixed ?? false,
      undo_send_seconds: w.settings.undo_send_seconds ?? UNDO_SEND_DEFAULT,
      undo_send_chat_seconds: w.settings.undo_send_chat_seconds ?? UNDO_SEND_CHAT_DEFAULT,
      message_delete: w.settings.message_delete ?? "own",
      collab_ticket_replies: w.settings.collab_ticket_replies ?? true,
      collab_ticket_reply_to: w.settings.collab_ticket_reply_to ?? "address",
    },
  };
}

const hexRe = /^#[0-9a-fA-F]{6}$/;

function timezones(): string[] {
  try {
    return Intl.supportedValuesOf("timeZone");
  } catch {
    return ["UTC", "Europe/London", "Europe/Berlin", "America/New_York", "America/Los_Angeles", "Asia/Tokyo"];
  }
}

export default function WorkspaceSettings() {
  const { t } = useTranslation("settings");
  const { data: ws } = useQuery(q.workspace());
  const role = useRole();
  const canEdit = atLeast(role, "admin");
  const qc = useQueryClient();
  const { defaultBrand } = useBrands();
  const [draft, setDraft] = useState<Draft | null>(null);

  useEffect(() => {
    if (ws) setDraft(toDraft(ws));
  }, [ws]);

  const images = useWorkspaceImage();
  const image = async (slot: "logo" | WorkspaceImageSlot, file: Blob) => {
    await images.upload.mutateAsync({ slot, file });
  };
  const unimage = async (slot: "logo" | WorkspaceImageSlot) => {
    try {
      await images.remove.mutateAsync(slot);
    } catch (e) {
      toast.error(e);
    }
  };
  const assignModeLabel = t(`workspace.assignModes.${ws?.settings.auto_assign ?? "none"}`);
  // Replies from members' own mail accounts are Collab's (contracts/collab.md "Answering tickets").
  const { data: collab } = useQuery(q.collabStatus());

  const tzItems = useMemo(() => timezones().map((t) => ({ value: t, label: t.replace(/_/g, " ") })), []);

  const save = useMutation({
    mutationFn: (d: Draft) => {
      const settings: WS = {
        ...d.settings,
        help_center_title: d.settings.help_center_title.trim(),
        brand_color: d.settings.brand_color || undefined,
        ...d.display,
      };
      return api.workspaces.update({ name: d.name.trim(), settings });
    },
    onMutate: async (d) => {
      await qc.cancelQueries({ queryKey: qk.workspace });
      const prev = qc.getQueryData<Workspace>(qk.workspace);
      if (prev) {
        const next = { ...prev, name: d.name.trim(), settings: { ...prev.settings, ...d.settings, ...d.display } };
        qc.setQueryData(qk.workspace, next);
        updateWorkspace(next);
      }
      return { prev };
    },
    onError: (e, _d, ctx) => {
      if (ctx?.prev) {
        qc.setQueryData(qk.workspace, ctx.prev);
        updateWorkspace(ctx.prev);
      }
      toast.error(e);
    },
    onSuccess: (w) => {
      qc.setQueryData(qk.workspace, w);
      updateWorkspace(w);
      toast.success(t("workspace.saved"));
    },
  });

  if (!ws || !draft) {
    return (
      <SettingsPage title={t("workspace.title")}>
        <Skeleton className="h-52 w-full rounded-lg" />
        <Skeleton className="h-36 w-full rounded-lg" />
      </SettingsPage>
    );
  }

  const base = toDraft(ws);
  const dirty = JSON.stringify(base) !== JSON.stringify(draft);
  const set = <K extends keyof Draft["settings"]>(k: K, v: Draft["settings"][K]) => setDraft({ ...draft, settings: { ...draft.settings, [k]: v } });
  const colorOk = !draft.settings.brand_color || hexRe.test(draft.settings.brand_color);
  // The workspace's help center is the default brand's.
  const helpUrl = defaultBrand ? helpCenterHref(defaultBrand) : defaultHelpUrl(ws.slug);

  return (
    <SettingsPage title={t("workspace.title")} description={t("workspace.description")}>
      <fieldset disabled={!canEdit} className="flex flex-col gap-8">
        <Section>
          <Row label={t("workspace.logo")} description={t("workspace.logoHint")}>
            <ImageUpload
              name={ws.name}
              shape="square"
              size="lg"
              aspect={1}
              value={ws.avatar_url}
              disabled={!canEdit}
              onChange={(file) => image("logo", file)}
              onRemove={ws.avatar_url ? () => unimage("logo") : undefined}
            />
          </Row>
          <Row label={t("workspace.name")} htmlFor="ws-name">
            <TextInput id="ws-name" className="w-[280px]" value={draft.name} maxLength={60} onChange={(e) => setDraft({ ...draft, name: e.target.value })} />
          </Row>
          <Row label={t("workspace.url")} description={t("workspace.urlHint")}>
            <span className="mono text-[12.5px] text-fg-2">{ws.slug}</span>
          </Row>
          <Row label={t("workspace.timezone")} description={t("workspace.timezoneHint")}>
            <Picker
              items={tzItems}
              value={draft.settings.timezone}
              onSelect={(v) => set("timezone", v)}
              placeholder={t("workspace.searchTimezones")}
              width={280}
              align="end"
              trigger={
                <Button disabled={!canEdit} className="w-[280px] justify-between font-normal" iconRight={<ChevronDown className="size-3.5 text-fg-3" />}>
                  <span className="truncate">{draft.settings.timezone.replace(/_/g, " ")}</span>
                </Button>
              }
            />
          </Row>
          <Row
            label={t("workspace.language")}
            description={t("workspace.languageHint")}
          >
            <Picker
              items={SUPPORTED.map((l) => ({ value: l, label: LOCALE_NAMES[l], keywords: [l] }))}
              value={draft.settings.locale}
              onSelect={(v) => set("locale", v)}
              placeholder={t("workspace.searchLanguages")}
              width={220}
              align="end"
              trigger={
                <Button disabled={!canEdit} className="w-[220px] justify-between font-normal" iconRight={<ChevronDown className="size-3.5 text-fg-3" />}>
                  <span className="truncate">{LOCALE_NAMES[matchLocale(draft.settings.locale) ?? "en"]}</span>
                </Button>
              }
            />
          </Row>
          <Row label={t("workspace.languageFixed")} description={t("workspace.languageFixedHint", { language: LOCALE_NAMES[matchLocale(draft.settings.locale) ?? "en"] })}>
            <Switch aria-label={t("workspace.languageFixed")} checked={draft.settings.locale_fixed} onCheckedChange={(v) => set("locale_fixed", v)} disabled={!canEdit} />
          </Row>
        </Section>

        <Section title={t("workspace.helpCenter")} description={t("workspace.helpCenterHint")}>
          <Row label={t("workspace.publish")} description={draft.settings.help_center_enabled ? t("workspace.publishOn") : t("workspace.publishOff")}>
            <Switch aria-label={t("workspace.publish")} checked={draft.settings.help_center_enabled} onCheckedChange={(v) => set("help_center_enabled", v)} disabled={!canEdit} />
          </Row>
          <Row label={t("workspace.hcLogo")} description={t("workspace.hcLogoHint")}>
            <ImageUpload
              name={draft.settings.help_center_title || ws.name}
              shape="square"
              size="md"
              aspect={4}
              value={ws.images?.help_center_logo}
              disabled={!canEdit}
              onChange={(file) => image("help_center_logo", file)}
              onRemove={ws.images?.help_center_logo ? () => unimage("help_center_logo") : undefined}
            />
          </Row>
          <Row label={t("workspace.hcFavicon")} description={t("workspace.hcFaviconHint")}>
            <ImageUpload
              name={ws.name}
              shape="square"
              size="sm"
              value={ws.images?.help_center_favicon}
              disabled={!canEdit}
              onChange={(file) => image("help_center_favicon", file)}
              onRemove={ws.images?.help_center_favicon ? () => unimage("help_center_favicon") : undefined}
            />
          </Row>
          <Row label={t("workspace.hcTitle")} htmlFor="hc-title">
            <TextInput
              id="hc-title"
              className="w-[280px]"
              placeholder={`${ws.name} Help`}
              value={draft.settings.help_center_title}
              maxLength={80}
              onChange={(e) => set("help_center_title", e.target.value)}
            />
          </Row>
          <Row label={t("workspace.publicAddress")}>
            <a href={helpUrl} target="_blank" rel="noreferrer" className="mono inline-flex items-center gap-1 text-[12.5px] text-fg-2 hover:text-fg">
              {bareUrl(helpUrl)}
              <ExternalLink className="size-3" />
            </a>
            <CopyButton value={helpUrl} label={t("workspace.copyLink")} />
          </Row>
        </Section>

        <Section title={t("workspace.conversations")}>
          <Row label={t("workspace.csat")} description={t("workspace.csatHint")}>
            <Switch aria-label={t("workspace.csat")} checked={draft.settings.csat_enabled} onCheckedChange={(v) => set("csat_enabled", v)} disabled={!canEdit} />
          </Row>
          <Row label={t("workspace.undoSend")} description={t("workspace.undoSendHint")}>
            <Segmented
              value={String(draft.settings.undo_send_seconds)}
              onChange={(v) => set("undo_send_seconds", Number(v))}
              options={UNDO_SEND_CHOICES.map((n) => ({ value: String(n), label: n ? t("workspace.undoSeconds", { n }) : t("workspace.undoOff") }))}
            />
          </Row>
          <Row label={t("workspace.undoSendChat")} description={t("workspace.undoSendChatHint")}>
            <Segmented
              value={String(draft.settings.undo_send_chat_seconds)}
              onChange={(v) => set("undo_send_chat_seconds", Number(v))}
              options={UNDO_SEND_CHOICES.map((n) => ({ value: String(n), label: n ? t("workspace.undoSeconds", { n }) : t("workspace.undoOff") }))}
            />
          </Row>
          <Row label={t("workspace.messageDelete")} description={t("workspace.messageDeleteHint")}>
            <Segmented
              value={draft.settings.message_delete}
              onChange={(v) => set("message_delete", v)}
              options={[
                { value: "off", label: t("workspace.messageDeleteOff") },
                { value: "own", label: t("workspace.messageDeleteOwn") },
                { value: "all", label: t("workspace.messageDeleteAll") },
              ]}
            />
          </Row>
          {collab?.enabled && (
            <Row label={t("workspace.collabReplies")} description={t("workspace.collabRepliesHint")}>
              <Switch
                aria-label={t("workspace.collabReplies")}
                checked={draft.settings.collab_ticket_replies}
                onCheckedChange={(v) => set("collab_ticket_replies", v)}
                disabled={!canEdit}
              />
            </Row>
          )}
          {collab?.enabled && draft.settings.collab_ticket_replies && (
            <Row label={t("workspace.collabReplyTo")} description={t("workspace.collabReplyToHint")}>
              <Segmented
                value={draft.settings.collab_ticket_reply_to}
                onChange={(v) => set("collab_ticket_reply_to", v)}
                options={[
                  { value: "address", label: t("workspace.collabReplyToAddress") },
                  { value: "mailbox", label: t("workspace.collabReplyToMailbox") },
                ]}
              />
            </Row>
          )}
          <Row label={t("workspace.autoAssign")} description={assignModeLabel}>
            <Button asChild size="sm" variant="ghost" iconRight={<ArrowRight className="size-3.5" />}>
              <Link to="/settings/assignment">{t("workspace.autoAssignLink")}</Link>
            </Button>
          </Row>
          <Row label={t("workspace.brand")} description={t("workspace.brandHint")}>
            <span
              aria-hidden
              className="size-6 rounded-md border border-line"
              style={{ background: colorOk && draft.settings.brand_color ? draft.settings.brand_color : "#0a0a0a" }}
            />
            <TextInput
              aria-label={t("workspace.brandHex")}
              className="mono w-[120px]"
              placeholder="#0A0A0A"
              value={draft.settings.brand_color}
              invalid={!colorOk}
              maxLength={7}
              onChange={(e) => {
                const v = e.target.value.trim();
                set("brand_color", v && !v.startsWith("#") ? `#${v}` : v);
              }}
            />
          </Row>
        </Section>

        <Section title={t("workspace.pictures")} description={t("workspace.picturesHint")}>
          <Row label={t("workspace.gravatar")} description={t("workspace.gravatarHint")}>
            <Switch aria-label={t("workspace.gravatar")} checked={draft.settings.gravatar} onCheckedChange={(v) => set("gravatar", v)} disabled={!canEdit} />
          </Row>
          <Row label={t("workspace.domainLogos")} description={t("workspace.domainLogosHint")}>
            <Switch aria-label={t("workspace.domainLogos")} checked={draft.settings.domain_logos} onCheckedChange={(v) => set("domain_logos", v)} disabled={!canEdit} />
          </Row>
        </Section>

        <Section title={t("display.title")} description={t("display.description")}>
          <DisplayOverridesEditor
            statuses={draft.display.status_labels}
            priorities={draft.display.priority_labels}
            onStatuses={(v) => setDraft({ ...draft, display: { ...draft.display, status_labels: v } })}
            onPriorities={(v) => setDraft({ ...draft, display: { ...draft.display, priority_labels: v } })}
            disabled={!canEdit}
          />
        </Section>
      </fieldset>

      <StarterSpaceSection />

      {role === "owner" && <DangerZone ws={ws} />}

      <SaveBar
        dirty={dirty && canEdit}
        saving={save.isPending}
        disabled={!draft.name.trim() || !colorOk}
        onSave={() => save.mutate(draft)}
        onDiscard={() => setDraft(base)}
      />
    </SettingsPage>
  );
}

function DangerZone({ ws }: { ws: Workspace }) {
  const { t } = useTranslation("settings");
  const confirm = useConfirm();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const me = useSession((s) => s.user!.id);
  const { data: members } = useQuery(q.members());
  const [transferTo, setTransferTo] = useState<string | null>(null);
  const [error, setError] = useState<unknown>(null);

  const transfer = useMutation({
    mutationFn: (uid: string) => api.workspaces.transfer(uid),
    onSuccess: async () => {
      setTransferTo(null);
      await reloadSession(ws.id);
      void qc.invalidateQueries({ queryKey: qk.members });
      toast.success(t("danger.transferred"));
    },
  });

  const target = members?.find((m) => m.user.id === transferTo);

  return (
    <Section title={t("danger.title")} danger>
      <Row label={t("danger.transfer")} description={t("danger.transferHint")}>
        <Picker
          items={(members ?? [])
            .filter((m) => m.user.id !== me && m.role !== "viewer")
            .map((m) => ({ value: m.user.id, label: m.user.name, hint: m.role, icon: <Avatar name={m.user.name} src={m.user.avatar_url} size="xs" /> }))}
          value={null}
          onSelect={(v) => {
            transfer.reset();
            setTransferTo(v);
          }}
          placeholder={t("danger.chooseMember")}
          align="end"
          trigger={<Button variant="danger">{t("danger.transferButton")}</Button>}
        />
      </Row>
      <Row label={t("danger.delete")} description={t("danger.deleteHint")}>
        <Button
          variant="danger"
          onClick={() => {
            setError(null);
            confirm.show(
              t("danger.deleteBody", { name: ws.name }),
              async () => {
                try {
                  await api.workspaces.delete(ws.name);
                } catch (e) {
                  setError(e);
                  return;
                }
                qc.clear();
                const s = await reloadSession();
                void navigate(s.workspaceId ? { to: "/" } : { to: "/onboarding" });
              },
              { title: t("danger.deleteTitle"), confirmLabel: t("danger.delete"), danger: true, typeToConfirm: ws.name },
            );
          }}
        >
          {t("danger.delete")}
        </Button>
      </Row>
      {!!error && (
        <div className="pb-3.5">
          <ReauthNotice error={error} />
        </div>
      )}
      {transferTo && target && (
        <Dialog
          open
          onOpenChange={(o) => !o && setTransferTo(null)}
          size="sm"
          title={t("danger.transferTitle", { name: target.user.name })}
          description={t("danger.transferBody")}
          footer={
            <>
              <Button variant="ghost" onClick={() => setTransferTo(null)}>
                {t("cancel")}
              </Button>
              <Button variant="danger-solid" loading={transfer.isPending} onClick={() => transfer.mutate(target.user.id)}>
                {t("danger.transfer")}
              </Button>
            </>
          }
        >
          {transfer.isError ? <ReauthNotice error={transfer.error} /> : <div />}
        </Dialog>
      )}
    </Section>
  );
}
