// Settings > Brands > a brand > a chat widget (contracts/teams.md "Widget"):
// an editor on the left (appearance, header, home screen, topics, routing,
// behaviour, install, identity) and the real widget on the right, live,
// following every unsaved change. Pictures upload straight away; everything
// else saves together from the save bar.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useLocation, useNavigate, useParams } from "@tanstack/react-router";
import { ArrowRight, ChevronDown, KeyRound, LayoutPanelLeft, MessageCircle, MessagesSquare, Monitor, Moon, PanelRight, Sun, Trash2 } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { StatePill } from "@/components/settings2/bits";
import { BackLink, NoAccess, Row, Section, SettingsPage } from "@/components/layout";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { EmptyState } from "@/components/ui/empty";
import { Textarea, TextInput } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { Picker } from "@/components/ui/picker";
import { Segmented } from "@/components/ui/tabs";
import { CopyField } from "@/components/ui/copy-button";
import { useConfirm } from "@/components/ui/confirm";
import { toast } from "@/components/ui/toast";
import { ImageUpload } from "@/components/ui/image-upload";
import { Stepper } from "@/components/settings2/Stepper";
import { SaveBar } from "@/components/settings/SaveBar";
import { ReauthNotice, isReauthError } from "@/components/settings/ReauthNotice";
import { UnsavedGuard } from "@/components/settings2/UnsavedGuard";
import { BrandMark, Note, TeamPick, useBrands, useTeams } from "@/components/settings2/teams";
import { OllieMark } from "@/components/assistant/OllieMark";
import { ChipEditor } from "@/components/agent/ChipEditor";
import { Counter } from "@/components/agent/bits";
import { agentSettings } from "@/components/agent/settings";
import { LivePreview, type PreviewScene } from "@/components/widget/LivePreview";
import { ColorField, hasMarkup, HEX, LIMITS, suggestionProblem, SuggestionsEditor, tidy, toWidgetConfig, widgetBotOf } from "@/components/widget/fields";
import { BotSelect, resolveBot } from "@/components/bots/BotSelect";
import { topicProblem, topicsForSave, TopicsEditor } from "@/components/widget/TopicsEditor";
import { ALL_FILE_TYPES, FILE_TYPES } from "@/components/widget/fileTypes";
import { api } from "@/lib/api/endpoints";
import { errorMessage } from "@/lib/api/client";
import type { Brand, Widget, WidgetSettings as WS } from "@/lib/api/types";
import { useAI } from "@/lib/ai";
import { appOrigin, config } from "@/lib/config";
import { q, qk, removeFromList, upsertInList } from "@/lib/queries";
import { atLeast, useRole, useWorkspace } from "@/lib/session";
import { AI_TONE, tone } from "@/lib/tones";
import { cn } from "@/lib/cn";

const HOSTED = "https://app.opendesk.sh";
const OFFLINE_MAX = 300;

/** The widget is served by the app itself: the hosted app, or this self-hosted instance. */
export const widgetOrigin = appOrigin;

/** The API origin for `data-api`, when the widget can't infer it from where it loads. */
function widgetApi(origin: string): string | null {
  if (!config.apiUrl) return null;
  const inferred = origin === HOSTED ? "https://api.opendesk.sh" : origin;
  try {
    const a = new URL(config.apiUrl, window.location.href).origin;
    return a === inferred ? null : a;
  } catch {
    return null;
  }
}

/** The script tag for a widget key, on this instance. */
export function widgetSnippet(key: string) {
  const origin = widgetOrigin();
  const apiAttr = widgetApi(origin);
  return `<script src="${origin}/widget/v1.js" data-key="${key}"${apiAttr ? ` data-api="${apiAttr}"` : ""} async></script>`;
}

function normalizeOrigin(v: string): string | null {
  try {
    const u = new URL(v.trim().includes("://") ? v.trim() : `https://${v.trim()}`);
    if (u.protocol !== "https:" && u.protocol !== "http:") return null;
    return u.origin;
  } catch {
    return null;
  }
}

/** Defaults for keys an older server leaves out (contracts/api.md, contracts/teams.md). */
function withDefaults(s: WS): WS {
  return {
    ...s,
    color: s.color ?? "",
    theme: s.theme ?? "auto",
    position: s.position ?? "right",
    offset_x: s.offset_x ?? 20,
    offset_y: s.offset_y ?? 20,
    launcher: s.launcher ?? "icon",
    launcher_label: s.launcher_label ?? "",
    radius: s.radius ?? "soft",
    title: s.title ?? "",
    subtitle: s.subtitle ?? "",
    show_team: s.show_team ?? true,
    suggestions: s.suggestions ?? [],
    sound: s.sound ?? false,
    topics: (s.topics ?? []).map((x) => ({ ...x })),
    offline_message: s.offline_message ?? "",
    intro: s.intro ?? "",
    chat_welcome: s.chat_welcome ?? true,
    email_follow_up: s.email_follow_up ?? true,
    attachments: s.attachments ?? true,
    attachment_types: s.attachment_types ?? [],
    voice_messages: s.voice_messages ?? false,
    voice_max_seconds: s.voice_max_seconds ?? 60,
  };
}

// limitTypes: files are held to settings.attachment_types; off, any file but a program is taken.
type Draft = { name: string; team_id: string | null; bot_id: string | null; limitTypes: boolean; settings: WS };
const toDraft = (w: Widget): Draft => {
  const settings = withDefaults(w.settings);
  return { name: w.name, team_id: w.team_id, bot_id: w.bot_id ?? null, limitTypes: settings.attachment_types.length > 0, settings };
};

type TextKey = "greeting" | "intro" | "launcher_label" | "title" | "subtitle";

export default function WidgetDetail() {
  const { t } = useTranslation("settingsTeams");
  const { brandId, widgetId } = useParams({ from: "/app/settings/brands/$brandId/widgets/$widgetId" });
  const admin = atLeast(useRole(), "admin");
  const { data: widgets, isLoading } = useQuery({ ...q.widgets(), enabled: admin });
  const { brands } = useBrands();
  const widget = widgets?.find((w) => w.id === widgetId);
  const brand = brands.find((b) => b.id === (widget?.brand_id ?? brandId));
  const back = (
    <BackLink {...({ to: "/settings/brands/$brandId", params: { brandId }, search: { tab: "widgets" } } as unknown as React.ComponentProps<typeof Link>)}>
      {brand ? brand.name : t("brands.title")}
    </BackLink>
  );

  if (!admin)
    return (
      <SettingsPage title={t("widget.fallbackTitle")} back={back}>
        <NoAccess what="chat widgets" />
      </SettingsPage>
    );
  if (isLoading)
    return (
      <SettingsPage wide title={<Skeleton className="h-5 w-48" />} back={back}>
        <Skeleton className="h-40 w-full rounded-lg" />
        <Skeleton className="h-28 w-full rounded-lg" />
      </SettingsPage>
    );
  if (!widget)
    return (
      <SettingsPage title={t("widget.fallbackTitle")} back={back}>
        <Section flush>
          <EmptyState
            lit={3}
            title={t("widget.notFound")}
            description={t("widget.notFoundBody")}
            action={
              <Button size="sm" asChild>
                <Link to="/settings/brands">{t("brands.backToList")}</Link>
              </Button>
            }
          />
        </Section>
      </SettingsPage>
    );
  return <WidgetEditor key={widget.id} widget={widget} brand={brand} back={back} />;
}

function WidgetEditor({ widget, brand, back }: { widget: Widget; brand: Brand | undefined; back: React.ReactNode }) {
  const { t } = useTranslation("settingsDev");
  const { t: tt } = useTranslation("settingsTeams");
  const ws = useWorkspace();
  const ai = useAI();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const confirm = useConfirm();
  const { data: members } = useQuery(q.members());
  const { data: mailboxes } = useQuery(q.mailboxes());
  const { teams } = useTeams();
  const base = useMemo(() => toDraft(widget), [widget]);
  const [draft, setDraft] = useState<Draft>(base);
  const [prevBase, setPrevBase] = useState(base);
  const [scene, setScene] = useState<PreviewScene>("home");
  const [dark, setDark] = useState(false);
  // While the offline message is being edited, the preview shows the widget closed.
  const [closed, setClosed] = useState(false);

  useEffect(() => {
    // Someone else saved: take their version unless we are mid-edit.
    if (JSON.stringify(draft) === JSON.stringify(prevBase)) setDraft(base);
    setPrevBase(base);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base]);
  // A fixed theme decides the preview's; "auto" leaves the toggle to you.
  useEffect(() => {
    if (draft.settings.theme === "light") setDark(false);
    else if (draft.settings.theme === "dark") setDark(true);
  }, [draft.settings.theme]);

  const s = draft.settings;
  const dirty = JSON.stringify(draft) !== JSON.stringify(base);
  const agentOn = ai.enabled && agentSettings(ai.features).widget_answers;
  const hasFollowUpMailbox = mailboxes?.some(
    (m) =>
      (m.brand_id === widget.brand_id || Object.values(m.alias_settings ?? {}).some((alias) => alias.brand_id === widget.brand_id)) &&
      m.status === "active" &&
      m.sending !== "system_off",
  );
  const team = teams.find((x) => x.id === draft.team_id);
  const { t: tb } = useTranslation("bots");
  const { data: bots } = useQuery(q.bots());
  const bot = resolveBot(bots, draft.bot_id);
  const preview = useMemo(() => {
    // The preview shows the widget's team when it has one, as visitors will see it.
    const people = team ? (members ?? []).filter((m) => team.members.some((x) => x.user_id === m.user.id && x.role !== "observer")) : members;
    const c = toWidgetConfig(s, ws, people, agentOn, bot ? widgetBotOf(bot) : null);
    return {
      ...c,
      workspace_name: brand?.name ?? c.workspace_name,
      brand_color: s.color || brand?.color || c.brand_color,
      locale: brand?.locale || c.locale,
      avatar_url: widget.images.avatar ?? brand?.images.logo ?? c.avatar_url,
      launcher_icon_url: widget.images.launcher ?? c.launcher_icon_url,
      help_center_url: brand?.help_center.enabled ? brand.help_center.url : null,
      topics: s.topics.filter((x) => tidy(x.label).length >= 2).map((x) => ({ id: x.id, label: tidy(x.label) })),
      offline_message: tidy(s.offline_message),
      open: !closed,
      brand: brand ? { name: brand.name, color: brand.color || null } : undefined,
    };
  }, [s, ws, members, agentOn, team, brand, widget.images, closed, bot]);

  const textError = (k: TextKey): string | null => {
    const v = s[k];
    if (tidy(v).length > LIMITS[k]) return t("widget.tooLong", { max: LIMITS[k] });
    if (hasMarkup(v)) return t("widget.noMarkup");
    return null;
  };
  const offlineError = tidy(s.offline_message).length > OFFLINE_MAX ? t("widget.tooLong", { max: OFFLINE_MAX }) : hasMarkup(s.offline_message) ? t("widget.noMarkup") : null;
  const invalid =
    !draft.name.trim() ||
    (["greeting", "intro", "launcher_label", "title", "subtitle"] as TextKey[]).some((k) => textError(k)) ||
    s.suggestions.some((x) => suggestionProblem(x)) ||
    s.topics.some((x) => topicProblem(x, s.topics)) ||
    !!offlineError ||
    (s.attachments && draft.limitTypes && s.attachment_types.length === 0) ||
    (s.color !== "" && !HEX.test(s.color));

  const save = useMutation({
    mutationFn: (d: Draft) => {
      // PATCH merges, so only what changed goes up.
      const patch: Partial<WS> = {};
      for (const k of Object.keys(d.settings) as (keyof WS)[]) {
        if (JSON.stringify(d.settings[k]) !== JSON.stringify(base.settings[k])) (patch as Record<string, unknown>)[k] = d.settings[k];
      }
      if (patch.suggestions) patch.suggestions = patch.suggestions.map(tidy);
      if (patch.topics) patch.topics = topicsForSave(patch.topics);
      for (const k of ["launcher_label", "title", "subtitle", "offline_message"] as const) if (patch[k] !== undefined) patch[k] = tidy(patch[k]!);
      return api.widgets.update(widget.id, {
        ...(d.name !== base.name ? { name: d.name.trim() } : {}),
        ...(d.team_id !== base.team_id ? { team_id: d.team_id } : {}),
        ...(d.bot_id !== base.bot_id ? { bot_id: d.bot_id } : {}),
        ...(Object.keys(patch).length ? { settings: patch } : {}),
      });
    },
    onSuccess: (w) => {
      upsertInList(qc, qk.widgets, w);
      setDraft(toDraft(w));
      void qc.invalidateQueries({ queryKey: qk.widgetSettings });
      toast.success(t("widget.saved"));
    },
    onError: (e) => toast.error(e, t("widget.saveError")),
  });

  const remove = () =>
    confirm.show(
      tt("widget.deleteBody", { name: widget.name }),
      async () => {
        await api.widgets.remove(widget.id);
        removeFromList(qc, qk.widgets, widget.id);
        void qc.invalidateQueries({ queryKey: qk.brands });
        toast.success(tt("widget.deleted", { name: widget.name }));
        void navigate({ to: "/settings/brands/$brandId", params: { brandId: widget.brand_id }, search: { tab: "widgets" } });
      },
      { title: tt("widget.deleteTitle", { name: widget.name }), confirmLabel: tt("widget.delete"), danger: true, typeToConfirm: widget.name },
    );

  const set = (p: Partial<WS>) => setDraft((d) => ({ ...d, settings: { ...d.settings, ...p } }));
  const snippet = widgetSnippet(widget.key);
  const identify = `window.opendesk('identify', {
  user_id: "42",
  user_hash: "<hmac from your server>",
  email: "ada@example.com",
  name: "Ada Lovelace",
});`;

  const text = (k: TextKey, placeholder: string, opts: { area?: boolean; className?: string } = {}) => {
    const err = textError(k);
    const common = { id: `widget-${k}`, value: s[k], invalid: !!err, placeholder, "aria-describedby": err ? `widget-${k}-err` : undefined };
    return (
      <div className={cn("flex flex-col gap-1", opts.className)}>
        {opts.area ? (
          <Textarea {...common} rows={3} maxLength={LIMITS[k] + 20} onChange={(e) => set({ [k]: e.target.value })} />
        ) : (
          <TextInput {...common} maxLength={LIMITS[k] + 10} onChange={(e) => set({ [k]: e.target.value })} />
        )}
        <div className="flex items-start justify-between gap-3">
          {err ? (
            <span id={`widget-${k}-err`} role="alert" className="text-xs text-danger-fg">
              {err}
            </span>
          ) : (
            <span />
          )}
          <Counter n={tidy(s[k]).length} max={LIMITS[k]} />
        </div>
      </div>
    );
  };

  return (
    <SettingsPage
      wide
      back={back}
      title={
        <span className="flex items-center gap-2.5">
          {brand && <BrandMark brand={brand} size="sm" />}
          <span className="truncate">{widget.name}</span>
        </span>
      }
      description={tt("widget.description", { brand: brand?.name ?? "" })}
      actions={
        <>
          {widget.seen_at ? (
            <StatePill state={s.enabled ? "active" : "disabled"}>{s.enabled ? t("widget.live") : t("widget.hidden")}</StatePill>
          ) : (
            <StatePill state="pending">{tt("widget.notInstalled")}</StatePill>
          )}
        </>
      }
    >
      <UnsavedGuard when={dirty && !save.isPending} />
      <div className="grid grid-cols-1 gap-8 xl:grid-cols-[minmax(0,1fr)_420px]">
        <div className="flex min-w-0 flex-col gap-8">
          <Section title={tt("widget.general")} description={tt("widget.generalHint")}>
            <Row label={tt("widget.name")} description={tt("widget.nameHint")} htmlFor="widget-name">
              <TextInput id="widget-name" className="w-[260px]" maxLength={60} value={draft.name} invalid={!draft.name.trim()} onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))} />
            </Row>
            <Row label={tt("widget.team")} description={team?.private ? tt("widget.teamPrivate", { name: team.name }) : tt("widget.teamHint")}>
              <TeamPick className="w-[260px]" value={draft.team_id} noneLabel={tt("widget.noTeam")} onChange={(team_id) => setDraft((d) => ({ ...d, team_id }))} />
            </Row>
            <Row
              label={tb("picker.label")}
              description={
                <>
                  {agentOn ? tb("picker.widgetHint") : tb("picker.widgetHintOff")}{" "}
                  <Link to="/settings/ai/bots" className="text-fg-2 underline decoration-line-strong underline-offset-2 hover:text-fg">
                    {tb("picker.manage")}
                  </Link>
                </>
              }
              htmlFor="widget-bot"
            >
              <BotSelect
                id="widget-bot"
                className="w-[260px]"
                value={draft.bot_id}
                onChange={(bot_id) => {
                  setDraft((d) => ({ ...d, bot_id }));
                  setScene("chat");
                }}
              />
            </Row>
          </Section>

          <div onFocusCapture={() => setScene((x) => (x === "chat" ? x : "launcher"))}>
            <Section title={t("widget.appearance")} description={t("widget.appearanceHint")}>
              <Row label={t("widget.color")} description={tt("widget.colorHint")} stack>
                <ColorField value={s.color} onChange={(color) => set({ color })} brand={brand?.color || ws?.settings.brand_color} />
              </Row>
              <Row label={t("widget.theme")} description={t("widget.themeHint")}>
                <Segmented
                  size="xs"
                  value={s.theme}
                  onChange={(theme) => set({ theme })}
                  options={[
                    { value: "auto", label: t("widget.themeAuto"), icon: <Monitor /> },
                    { value: "light", label: t("widget.light"), icon: <Sun /> },
                    { value: "dark", label: t("widget.dark"), icon: <Moon /> },
                  ]}
                />
              </Row>
              <Row label={t("widget.position")} description={t("widget.positionHint")}>
                <Segmented
                  size="xs"
                  value={s.position}
                  onChange={(position) => set({ position })}
                  options={[
                    { value: "left", label: t("widget.left"), icon: <LayoutPanelLeft /> },
                    { value: "right", label: t("widget.right"), icon: <PanelRight /> },
                  ]}
                />
              </Row>
              <Row label={t("widget.offset")} description={t("widget.offsetHint")} stack>
                <div className="flex flex-wrap items-center gap-2">
                  <Stepper value={s.offset_x} onChange={(offset_x) => set({ offset_x })} min={0} max={200} step={4} suffix="px" aria-label={t("widget.offsetX")} className="w-[124px]" />
                  <span className="text-xs text-fg-3">{t("widget.offsetXShort")}</span>
                  <Stepper value={s.offset_y} onChange={(offset_y) => set({ offset_y })} min={0} max={200} step={4} suffix="px" aria-label={t("widget.offsetY")} className="ml-2 w-[124px]" />
                  <span className="text-xs text-fg-3">{t("widget.offsetYShort")}</span>
                </div>
              </Row>
              <Row label={t("widget.radius")} description={t("widget.radiusHint")}>
                <Segmented
                  size="xs"
                  value={s.radius}
                  onChange={(radius) => set({ radius })}
                  options={(["square", "soft", "round"] as const).map((r) => ({
                    value: r,
                    label: t(`widget.radius_${r}`),
                    icon: <span className={cn("block size-3 border-[1.5px] border-current", r === "square" ? "rounded-[1px]" : r === "soft" ? "rounded-[4px]" : "rounded-full")} />,
                  }))}
                />
              </Row>
              <Row label={t("widget.launcherStyle")} description={t("widget.launcherStyleHint")} stack={s.launcher === "label"}>
                <div className="flex flex-col gap-2">
                  <Segmented
                    size="xs"
                    className="self-start"
                    value={s.launcher}
                    onChange={(launcher) => set({ launcher })}
                    options={[
                      { value: "icon", label: t("widget.launcherIcon") },
                      { value: "label", label: t("widget.launcherLabel") },
                    ]}
                  />
                  {s.launcher === "label" && text("launcher_label", t("widget.launcherLabelPlaceholder"), { className: "max-w-[320px]" })}
                </div>
              </Row>
              <ImageRow widget={widget} slot="launcher" label={t("widget.launcher")} hint={t("widget.launcherHint")} />
            </Section>
          </div>

          <div onFocusCapture={() => setScene("home")}>
            <Section title={t("widget.header")} description={t("widget.headerHint")}>
              <Row label={t("widget.titleField")} description={tt("widget.titleHint")} htmlFor="widget-title" stack>
                {text("title", brand?.name ?? ws?.name ?? "", { className: "max-w-[420px]" })}
              </Row>
              <Row label={t("widget.subtitle")} description={t("widget.subtitleHint")} htmlFor="widget-subtitle" stack>
                {text("subtitle", t("widget.subtitlePlaceholder"), { className: "max-w-[420px]" })}
              </Row>
              <Row label={t("widget.showTeam")} description={team ? tt("widget.showTeamOf", { name: team.name }) : t("widget.showTeamHint")}>
                <Switch checked={s.show_team} onCheckedChange={(show_team) => set({ show_team })} aria-label={t("widget.showTeam")} />
              </Row>
              <ImageRow widget={widget} slot="avatar" label={t("widget.avatar")} hint={t("widget.avatarHint")} />
            </Section>
          </div>

          <div onFocusCapture={() => setScene("home")}>
            <Section title={t("widget.home")} description={t("widget.homeHint")}>
              <Row label={t("widget.greeting")} description={t("widget.greetingHint")} stack htmlFor="widget-greeting">
                {text("greeting", t("widget.greetingPlaceholder"), { area: true })}
              </Row>
              <div onFocusCapture={() => setScene("new")}>
                <Row label={t("widget.intro")} description={t("widget.introHint")} stack htmlFor="widget-intro">
                  {text("intro", t("widget.introPlaceholder"), { area: true })}
                </Row>
                <Row label={t("widget.chatWelcome")} description={t("widget.chatWelcomeHint")}>
                  <Switch checked={s.chat_welcome} onCheckedChange={(chat_welcome) => set({ chat_welcome })} aria-label={t("widget.chatWelcome")} />
                </Row>
              </div>
              <Row label={t("widget.suggestions")} description={t("widget.suggestionsHint")} stack>
                <SuggestionsEditor value={s.suggestions} onChange={(suggestions) => set({ suggestions })} />
              </Row>
            </Section>
          </div>

          <div onFocusCapture={() => setScene("home")}>
            <Section title={tt("topics.title")} description={tt("topics.hint")}>
              <div className="py-3.5">
                <TopicsEditor value={s.topics} onChange={(topics) => set({ topics })} />
              </div>
            </Section>
          </div>

          <div
            onFocusCapture={() => {
              setClosed(true);
              setScene((x) => (x === "launcher" ? "home" : x));
            }}
            onBlurCapture={(e) => {
              if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setClosed(false);
            }}
          >
            <Section title={tt("widget.offlineTitle")} description={tt("widget.offlineHint")}>
              <Row label={tt("widget.offlineMessage")} description={team?.business_hours ? tt("widget.offlineTeamHours", { name: team.name }) : tt("widget.offlineWorkspaceHours")} stack htmlFor="widget-offline">
                <div className="flex flex-col gap-1">
                  <Textarea
                    id="widget-offline"
                    rows={2}
                    value={s.offline_message}
                    invalid={!!offlineError}
                    maxLength={OFFLINE_MAX + 20}
                    placeholder={tt("widget.offlinePlaceholder")}
                    onChange={(e) => set({ offline_message: e.target.value })}
                  />
                  <div className="flex items-start justify-between gap-3">
                    {offlineError ? (
                      <span role="alert" className="text-xs text-danger-fg">
                        {offlineError}
                      </span>
                    ) : (
                      <span />
                    )}
                    <Counter n={tidy(s.offline_message).length} max={OFFLINE_MAX} />
                  </div>
                </div>
              </Row>
              <Row
                label={tt("widget.followUp")}
                description={
                  <>
                    {tt("widget.followUpHint")}
                    {s.email_follow_up && mailboxes && !hasFollowUpMailbox && (
                      <span className="mt-1 block text-fg-2">
                        {tt("widget.followUpMailboxMissing")} <Link className="underline underline-offset-2" to="/settings/mailboxes/new" search={{ brand: widget.brand_id }}>{tt("widget.followUpAddMailbox")}</Link>
                      </span>
                    )}
                  </>
                }
              >
                <Switch checked={s.email_follow_up} onCheckedChange={(email_follow_up) => set({ email_follow_up })} aria-label={tt("widget.followUp")} />
              </Row>
            </Section>
          </div>

          <Section title={t("widget.settings")} description={t("widget.settingsHint")}>
            <Row label={t("widget.show")} description={t("widget.showHint")}>
              <Switch checked={s.enabled} onCheckedChange={(enabled) => set({ enabled })} aria-label={t("widget.show")} />
            </Row>
            <Row label={t("widget.requireEmail")} description={t("widget.requireEmailHint")}>
              <Switch checked={s.require_email} onCheckedChange={(require_email) => set({ require_email })} aria-label={t("widget.requireEmail")} />
            </Row>
            <Row label={t("widget.attachments")} description={t("widget.attachmentsHint")}>
              <Switch checked={s.attachments} onCheckedChange={(attachments) => set({ attachments })} aria-label={t("widget.attachments")} />
            </Row>
            {s.attachments && (
              <Row label={t("widget.fileTypes")} description={t("widget.fileTypesHint")} stack>
                <div className="flex flex-col items-start gap-2">
                  <Segmented
                    size="xs"
                    value={draft.limitTypes ? "some" : "any"}
                    onChange={(v) =>
                      setDraft((d) => ({ ...d, limitTypes: v === "some", settings: { ...d.settings, attachment_types: v === "some" ? d.settings.attachment_types : [] } }))
                    }
                    options={[
                      { value: "any", label: t("widget.anyFileType") },
                      { value: "some", label: t("widget.someFileTypes") },
                    ]}
                  />
                  {draft.limitTypes && (
                    <Picker
                      multi
                      values={s.attachment_types}
                      onChange={(ids) => set({ attachment_types: ALL_FILE_TYPES.filter((id) => ids.includes(id)) })}
                      items={FILE_TYPES.map((f) => ({ value: f.id, label: f.extensions.join(" "), group: t(`widget.fileGroups.${f.group}`), keywords: [f.id] }))}
                      placeholder={t("widget.searchFileTypes")}
                      width={280}
                      trigger={
                        <button
                          type="button"
                          aria-label={t("widget.pickFileTypes")}
                          className="group inline-flex h-8 w-full min-w-0 items-center justify-between gap-2 rounded-md border border-line bg-surface px-2.5 text-left text-sm text-fg shadow-[0_1px_1px_rgb(0_0_0/0.02)] outline-none transition-[border-color,box-shadow] duration-150 hover:border-line-strong focus-visible:border-fg-3 focus-visible:shadow-[0_0_0_3px_var(--focus)] [&_svg]:size-3.5"
                        >
                          <span className={cn("min-w-0 truncate", s.attachment_types.length === 0 && "text-fg-4")}>
                            {s.attachment_types.length === ALL_FILE_TYPES.length
                              ? t("widget.allFileTypes")
                              : s.attachment_types.length === 0
                                ? t("widget.pickFileTypes")
                                : FILE_TYPES.filter((f) => s.attachment_types.includes(f.id))
                                    .map((f) => f.extensions[0])
                                    .join(" ")}
                          </span>
                          <ChevronDown className="shrink-0 text-fg-3" />
                        </button>
                      }
                    />
                  )}
                  {draft.limitTypes && s.attachment_types.length === 0 && (
                    <span role="alert" className="text-xs text-danger-fg">
                      {t("widget.noFileTypes")}
                    </span>
                  )}
                </div>
              </Row>
            )}
            <Row label={t("widget.voice")} description={t("widget.voiceHint")}>
              <Switch checked={s.voice_messages} onCheckedChange={(voice_messages) => set({ voice_messages })} aria-label={t("widget.voice")} />
            </Row>
            {s.voice_messages && (
              <Row label={t("widget.voiceLength")} description={t("widget.voiceLengthHint")}>
                <Stepper
                  value={s.voice_max_seconds}
                  onChange={(voice_max_seconds) => set({ voice_max_seconds })}
                  min={5}
                  max={300}
                  step={5}
                  suffix={t("widget.secondsSuffix")}
                  aria-label={t("widget.voiceLength")}
                  className="w-[124px]"
                />
              </Row>
            )}
            <Row label={t("widget.sound")} description={t("widget.soundHint")}>
              <Switch checked={s.sound} onCheckedChange={(sound) => set({ sound })} aria-label={t("widget.sound")} />
            </Row>
            <Row label={t("widget.origins")} description={t("widget.originsHint")} stack>
              <ChipEditor
                mono
                values={s.allowed_origins}
                onChange={(allowed_origins) => set({ allowed_origins })}
                max={LIMITS.origins}
                normalize={normalizeOrigin}
                placeholder="https://www.example.com"
                aria-label={t("widget.origins")}
                dot="bg-tone-green-solid"
                empty={t("widget.anySite")}
              />
            </Row>
            {ai.enabled && <AgentLink on={agentOn} />}
          </Section>

          <Section title={t("widget.install")} description={tt("widget.installHint")}>
            <div className="flex flex-col gap-3 py-3">
              <CopyField value={snippet} multiline />
              <div className="flex items-center gap-2 text-[12px] text-fg-3">
                {t("widget.key")} <span className="mono text-fg-2">{widget.key}</span>
              </div>
            </div>
          </Section>

          <Section title={t("widget.identify")} description={t("widget.identifyHint")}>
            <div className="flex flex-col gap-3 py-3">
              <CopyField value={identify} multiline />
              <p className="text-[12.5px] leading-5 text-fg-3">{t("widget.identifyBody")}</p>
              <RotateSecret widget={widget} />
            </div>
          </Section>

          <Section title={tt("widget.danger")} danger>
            <Row label={tt("widget.deleteTitle", { name: widget.name })} description={tt("widget.deleteHint")}>
              <Button variant="danger" icon={<Trash2 className="size-3.5" />} onClick={remove}>
                {tt("widget.delete")}
              </Button>
            </Row>
          </Section>
        </div>

        <div className="xl:sticky xl:top-6 xl:self-start">
          <div className="mb-2.5 flex flex-wrap items-center justify-between gap-2">
            <h2 className="text-[13px] font-semibold tracking-[-0.01em]">{t("widget.preview")}</h2>
            <div className="flex items-center gap-1.5">
              <Segmented<PreviewScene>
                size="xs"
                value={scene}
                onChange={setScene}
                options={[
                  { value: "launcher", label: t("widget.sceneLauncher"), icon: <MessageCircle /> },
                  { value: "home", label: t("widget.sceneHome") },
                  { value: "chat", label: t("widget.sceneChat"), icon: <MessagesSquare /> },
                ]}
              />
              <Segmented
                size="xs"
                value={dark ? "dark" : "light"}
                onChange={(v) => setDark(v === "dark")}
                options={[
                  { value: "light", label: <span className="sr-only">{t("widget.light")}</span>, icon: <Sun /> },
                  { value: "dark", label: <span className="sr-only">{t("widget.dark")}</span>, icon: <Moon /> },
                ]}
              />
            </div>
          </div>
          <LivePreview config={preview} scene={scene} dark={dark} className="h-[min(680px,calc(100vh-140px))] min-h-[560px]" />
          <p className="mt-2 text-[12px] leading-[18px] text-fg-3">{!s.enabled ? t("widget.previewHidden") : t("widget.previewHint")}</p>
          {team?.private && (
            <Note className="mt-3" tone="violet">
              {tt("widget.privateTeamNote", { name: team.name })}
            </Note>
          )}
        </div>
      </div>
      <SaveBar dirty={dirty} saving={save.isPending} disabled={invalid} onSave={() => save.mutate(draft)} onDiscard={() => setDraft(base)} />
    </SettingsPage>
  );
}

/** Where the AI agent is set up, and whether it answers in this widget. */
function AgentLink({ on }: { on: boolean }) {
  const { t } = useTranslation("settingsDev");
  return (
    <div className="py-3.5">
      <Link
        to="/settings/ai/behaviour"
        className={cn("group flex items-center gap-3 rounded-lg border px-3.5 py-3 transition-colors", on ? cn(tone[AI_TONE].soft, tone[AI_TONE].line) : "border-line bg-sunken hover:bg-hover")}
      >
        <OllieMark size={22} tone={on ? "color" : "muted"} />
        <span className="min-w-0 flex-1">
          <span className="block text-[12.5px] font-medium text-fg">{on ? t("widget.agentOn") : t("widget.agentOff")}</span>
          <span className="block text-[12px] text-fg-3">{t("widget.agentHint")}</span>
        </span>
        <ArrowRight className="size-3.5 text-fg-3 transition-transform duration-150 group-hover:translate-x-0.5 motion-reduce:transition-none" />
      </Link>
    </div>
  );
}

/** The identity verification secret, shown once: on create (brand page) and after a rotate. */
export function WidgetSecretDialog({ secret, onClose, title, description }: { secret: string | null; onClose: () => void; title?: string; description?: string }) {
  const { t } = useTranslation("settingsDev");
  return (
    <Dialog
      open={!!secret}
      onOpenChange={(o) => !o && onClose()}
      title={title ?? t("widget.newSecret")}
      description={description ?? t("widget.newSecretHint")}
      footer={
        <Button variant="primary" onClick={onClose}>
          {t("widget.savedIt")}
        </Button>
      }
    >
      <div className="flex flex-col gap-3 pb-1">
        <CopyField value={secret ?? ""} />
        <p className="flex items-start gap-2 text-[12px] text-fg-3">
          <span className="mt-1.5 size-1.5 shrink-0 rounded-full bg-warn" />
          {t("widget.secretWarning")}
        </p>
      </div>
    </Dialog>
  );
}

/** The anchor of the secret row: a new widget whose first secret was not answered links here. */
export const WIDGET_SECRET_ANCHOR = "widget-secret";

/** A new widget created without passing the owner-level gate: no secret was answered, so rotate to see one. */
export function WidgetSecretLaterDialog({ title, onClose, onReveal }: { title: string; onClose: () => void; onReveal: () => void }) {
  const { t } = useTranslation("settingsDev");
  return (
    <Dialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={title}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            {t("widget.later")}
          </Button>
          <Button variant="primary" icon={<KeyRound className="size-3.5" />} onClick={onReveal}>
            {t("widget.revealSecret")}
          </Button>
        </>
      }
    >
      <div className="pb-1">
        <Note tone="amber" icon={<KeyRound className="mt-0.5 size-3.5 shrink-0" aria-hidden />}>
          <span className="font-medium">{t("widget.secretLater")}</span> {t("widget.secretLaterHint")}
        </Note>
      </div>
    </Dialog>
  );
}

function RotateSecret({ widget }: { widget: Widget }) {
  const { t } = useTranslation("settingsDev");
  const { t: tt } = useTranslation("settingsTeams");
  const confirm = useConfirm();
  const [secret, setSecret] = useState<string | null>(null);
  const [blocked, setBlocked] = useState<unknown>(null);
  // Arrived from creating the widget without its secret: say why, and point at the rotate button.
  const pending = useLocation({ select: (l) => l.hash === WIDGET_SECRET_ANCHOR });
  const [showPending, setShowPending] = useState(pending);
  const row = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!pending) return;
    setShowPending(true);
    const reduce = window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
    row.current?.scrollIntoView({ block: "center", behavior: reduce ? "auto" : "smooth" });
  }, [pending]);

  const rotate = async () => {
    setBlocked(null);
    try {
      const r = await api.widgets.rotateSecret(widget.id);
      setSecret(r.secret);
      setShowPending(false);
    } catch (e) {
      if (isReauthError(e)) return setBlocked(e);
      throw new Error(errorMessage(e, t("widget.rotateError")), { cause: e });
    }
  };

  return (
    <>
      {showPending && (
        <Note tone="amber" icon={<KeyRound className="mt-0.5 size-3.5 shrink-0" aria-hidden />}>
          <span className="font-medium">{t("widget.secretLater")}</span> {t("widget.secretLaterHint")}
        </Note>
      )}
      <div
        ref={row}
        id={WIDGET_SECRET_ANCHOR}
        className={cn("flex scroll-mt-24 items-center justify-between gap-4 rounded-md border px-3 py-2.5", showPending ? tone.amber.line : "border-line")}
      >
        <div className="min-w-0">
          <div className="text-[13px] font-medium">{t("widget.secret")}</div>
          <div className="text-[12px] text-fg-3">{tt("widget.secretHint", { name: widget.name })}</div>
        </div>
        <Button
          size="sm"
          icon={<KeyRound className="size-3.5" />}
          onClick={() => confirm.show(tt("widget.rotateBody", { name: widget.name }), rotate, { title: t("widget.rotateTitle"), confirmLabel: t("widget.rotate"), danger: true })}
        >
          {t("widget.rotate")}
        </Button>
      </div>
      {!!blocked && <ReauthNotice error={blocked} />}
      <WidgetSecretDialog secret={secret} onClose={() => setSecret(null)} />
    </>
  );
}

/** A widget picture, uploaded straight away (not part of the draft). */
function ImageRow({ widget, slot, label, hint }: { widget: Widget; slot: "avatar" | "launcher"; label: string; hint: string }) {
  const qc = useQueryClient();
  const src = widget.images[slot];
  return (
    <Row label={label} description={hint}>
      <ImageUpload
        name={widget.name}
        shape="round"
        size="md"
        value={src}
        onChange={async (f) => {
          const w = await api.widgets.uploadImage(widget.id, slot, f);
          upsertInList(qc, qk.widgets, w);
        }}
        onRemove={
          src
            ? async () => {
                try {
                  upsertInList(qc, qk.widgets, await api.widgets.removeImage(widget.id, slot));
                } catch (e) {
                  toast.error(e);
                }
              }
            : undefined
        }
      />
    </Row>
  );
}
