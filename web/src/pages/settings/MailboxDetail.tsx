import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { InBrowserNote } from "@/components/desktop/InBrowser";
import { isDesktop, openInBrowser } from "@/lib/desktop";
import { Braces, Forward, MailWarning, RefreshCw, TriangleAlert, Zap } from "lucide-react";
import { useReducedMotion } from "motion/react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { BackLink, NoAccess, Row, Section, SettingsPage } from "@/components/layout";
import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/ui/confirm";
import { EmptyState } from "@/components/ui/empty";
import { Field, TextInput } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { Segmented } from "@/components/ui/tabs";
import { toast } from "@/components/ui/toast";
import { Tooltip } from "@/components/ui/tooltip";
import { SaveBar } from "@/components/settings/SaveBar";
import { templateProblems } from "@/components/settings2/MemberProfileFields";
import { tagFor } from "@/components/templates/catalog";
import { EmailPreview } from "@/components/templates/EmailPreview";
import { SignatureEditor } from "@/components/templates/SignatureEditor";
import { TemplateEditor } from "@/components/templates/TemplateEditor";
import { VariablePicker } from "@/components/templates/VariablePicker";
import { connectionLabel, GoogleGlyph, MailboxIcon, MailboxStatus, MicrosoftGlyph, notReceiving, TestResults } from "@/components/mailboxes/kinds";
import { ForwardingInstructions } from "@/components/mailboxes/forwarding";
import { PortInput, validPort } from "@/components/mailboxes/PortInput";
import { useMailboxActions } from "@/components/mailboxes/useMailboxActions";
import {
  apiOn,
  CauseCallout,
  connectFailure,
  imapOn,
  sendProviders,
  syncOn,
  systemSendOn,
  useConnectOptions,
  useMailDetection,
  type ConnectFailure,
  type Provider,
} from "@/components/mailboxes/connect";
import { SmtpPresets } from "@/components/mailboxes/smtpPresets";
import { MailboxOllieSection } from "@/components/mailboxes/MailboxOllie";
import { quietFor } from "@/components/mailboxes/quiet";
import { AliasIdentities, aliasDraftsFor, aliasSettingsPatch, GmailSuggestions, type AliasDraft } from "@/components/mailboxes/AliasIdentities";
import {
  AliasesField,
  aliasTeamsFor,
  FromNameField,
  fromNameProblem,
  MailboxBrandField,
  MailboxTeamField,
  sameAliasTeams,
} from "@/components/mailboxes/addressing";
import { useBrands, useTeams } from "@/components/settings2/teams";
import { ContactFormsField } from "@/components/settings/ContactFormsField";
import { api, type MailboxReceive } from "@/lib/api/endpoints";
import { errorMessage } from "@/lib/api/client";
import type { Mailbox, MailboxConfig, MailboxTest, TemplatePreviewBody, TemplateProblem, TlsMode } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { relLong } from "@/lib/format";
import { tone } from "@/lib/tones";
import { isMicrosoftHost, mailboxHost, offersNoTls, readsDirectly, receivesOverImap, sendOnly, signedInProvider } from "@/lib/mailHost";
import { q, qk, upsertInList } from "@/lib/queries";
import { atLeast, useRole } from "@/lib/session";

type Draft = {
  name: string;
  display_name: string;
  team_id: string | null;
  brand_id: string;
  aliases: string[];
  alias_teams: Record<string, string>;
  alias_settings: Record<string, AliasDraft>;
  trusted_form_senders: string[];
  from_name_format: string;
  signature_html: string;
  auto_reply_enabled: boolean;
  auto_reply_subject: string;
  auto_reply_html: string;
  ai_answers: boolean;
  ai_answers_mode: Mailbox["ai_answers_mode"];
  ai_answers_outside_hours: boolean;
  bot_id: string | null;
  outbound_kind: Mailbox["outbound_kind"];
  config: MailboxConfig;
  password: string;
  smtp_password: string;
};

const IMAP_KEYS = ["imap_host", "imap_port", "imap_username", "imap_tls"] as const;
const SMTP_KEYS = ["smtp_host", "smtp_port", "smtp_username", "smtp_tls"] as const;
type ServerKey = (typeof IMAP_KEYS)[number] | (typeof SMTP_KEYS)[number];

const sameKeys = (a: MailboxConfig, b: MailboxConfig, keys: readonly ServerKey[]) => keys.every((k) => (a[k] ?? "") === (b[k] ?? ""));
const pick = (c: MailboxConfig, keys: readonly ServerKey[]): MailboxConfig => Object.fromEntries(keys.map((k) => [k, c[k]])) as MailboxConfig;

/** What a mailbox that reads its inbox directly may tidy up in Gmail or Outlook. */
const TIDY_KEYS = ["provider_mark_read", "provider_archive_on_solve", "provider_label"] as const;
type TidyKey = (typeof TIDY_KEYS)[number];
const sameTidy = (a: MailboxConfig, b: MailboxConfig) => TIDY_KEYS.every((k) => !!a[k] === !!b[k]);

/** Replies go out through opendesk or the mailbox's own SMTP server (not a provider sign-in). */
const ownSending = (k: Mailbox["outbound_kind"]) => k === "system" || k === "smtp";

const providerName = (p: Provider) => (p === "google" ? "Google" : "Microsoft");

function toDraft(m: Mailbox): Draft {
  return {
    name: m.name,
    display_name: m.display_name,
    team_id: m.team_id,
    brand_id: m.brand_id ?? "",
    aliases: [...(m.aliases ?? [])],
    alias_teams: { ...(m.alias_teams ?? {}) },
    alias_settings: aliasDraftsFor(m),
    trusted_form_senders: [...(m.trusted_form_senders ?? [])],
    from_name_format: m.from_name_format ?? "",
    signature_html: m.signature_html,
    auto_reply_enabled: m.auto_reply_enabled,
    auto_reply_subject: m.auto_reply_subject,
    auto_reply_html: m.auto_reply_html,
    ai_answers: m.ai_answers ?? false,
    ai_answers_mode: m.ai_answers_mode ?? "draft",
    ai_answers_outside_hours: m.ai_answers_outside_hours ?? false,
    bot_id: m.bot_id ?? null,
    outbound_kind: m.outbound_kind,
    config: { ...m.config },
    password: "",
    smtp_password: "",
  };
}

export default function MailboxDetail() {
  const { t } = useTranslation("settingsChannels");
  const { mailboxId } = useParams({ from: "/app/settings/mailboxes/$mailboxId" });
  const back = <BackLink to="/settings/mailboxes">{t("detail.back")}</BackLink>;
  const role = useRole();
  const { data, isLoading } = useQuery({ ...q.mailboxes(), enabled: atLeast(role, "admin") });
  const mailbox = data?.find((m) => m.id === mailboxId);

  if (!atLeast(role, "admin"))
    return (
      <SettingsPage title={t("detail.fallbackTitle")} back={back}>
        <NoAccess what="mailboxes" />
      </SettingsPage>
    );
  if (isLoading)
    return (
      <SettingsPage title={<Skeleton className="h-5 w-48" />} back={back}>
        {[180, 220, 120].map((h, i) => (
          <div key={i} aria-hidden>
            <Skeleton className="mb-3 h-3 w-28" />
            <div className="rounded-lg border border-line px-4 py-4" style={{ height: h }}>
              <Skeleton className="h-2.5 w-32" />
              <Skeleton className="mt-2.5 h-2 w-[55%] opacity-60" />
            </div>
          </div>
        ))}
      </SettingsPage>
    );
  if (!mailbox)
    return (
      <SettingsPage title={t("detail.fallbackTitle")} back={back}>
        <Section flush>
          <EmptyState
            lit={3}
            title={t("detail.notFound")}
            description={t("detail.notFoundBody")}
            action={
              <Button size="sm" asChild>
                <Link to="/settings/mailboxes">{t("detail.backToList")}</Link>
              </Button>
            }
          />
        </Section>
      </SettingsPage>
    );
  return <MailboxEditor key={mailbox.id} mailbox={mailbox} />;
}

function MailboxEditor({ mailbox }: { mailbox: Mailbox }) {
  const { t } = useTranslation("settingsChannels");
  const qc = useQueryClient();
  const navigate = useNavigate();
  const { multi: multiBrand } = useBrands();
  const { teams } = useTeams();
  const { setStatus, remove, testDialog } = useMailboxActions();
  const confirm = useConfirm();
  const reduce = useReducedMotion();
  const base = useMemo(() => toDraft(mailbox), [mailbox]);
  const [draft, setDraft] = useState<Draft>(base);
  const [test, setTest] = useState<MailboxTest | null>(null);
  const [testing, setTesting] = useState(false);
  // The Sending section's own check, and a save whose sign-in was refused.
  const [sendTest, setSendTest] = useState<MailboxTest | null>(null);
  const [sendTesting, setSendTesting] = useState(false);
  const [refused, setRefused] = useState<ConnectFailure | null>(null);

  // Someone else saved: take their version unless we are mid-edit.
  const [prevBase, setPrevBase] = useState(base);
  useEffect(() => {
    if (JSON.stringify(draft) === JSON.stringify(prevBase)) setDraft(base);
    setPrevBase(base);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base]);

  // What the server found wrong with the signature and the auto-reply: in the previews' check, or a refused save.
  const [signatureProblems, setSignatureProblems] = useState<TemplateProblem[]>([]);
  const [autoReplyProblems, setAutoReplyProblems] = useState<TemplateProblem[]>([]);
  const [saveProblems, setSaveProblems] = useState<{ signature: TemplateProblem[]; autoReply: TemplateProblem[]; aliases: unknown }>({ signature: [], autoReply: [], aliases: null });
  const onSignatureProblems = useCallback((p: TemplateProblem[]) => setSignatureProblems(p), []);
  const onAutoReplyProblems = useCallback((p: TemplateProblem[]) => setAutoReplyProblems(p), []);
  const signatureHtml = draft.signature_html;
  const signatureBody = useMemo<TemplatePreviewBody>(() => ({ kind: "signature", mailbox_id: mailbox.id, body_html: signatureHtml }), [mailbox.id, signatureHtml]);
  const { auto_reply_subject: autoSubject, auto_reply_html: autoHtml } = draft;
  const autoReplyBody = useMemo<TemplatePreviewBody>(
    () => ({ kind: "auto_reply", mailbox_id: mailbox.id, subject: autoSubject, body_html: autoHtml }),
    [mailbox.id, autoSubject, autoHtml],
  );
  const subjectRef = useRef<HTMLInputElement>(null);
  const insertInSubject = (text: string, select?: string) => {
    const el = subjectRef.current;
    const cur = draft.auto_reply_subject;
    const start = el?.selectionStart ?? cur.length;
    const end = el?.selectionEnd ?? start;
    set("auto_reply_subject", cur.slice(0, start) + text + cur.slice(end));
    const i = select ? text.indexOf(select) : -1;
    requestAnimationFrame(() => {
      el?.focus();
      if (select && i >= 0) el?.setSelectionRange(start + i, start + i + select.length);
      else el?.setSelectionRange(start + text.length, start + text.length);
    });
  };

  const aliasTeams = aliasTeamsFor(draft.alias_teams, draft.aliases, !!teams.find((x) => x.id === draft.team_id)?.private);
  const aliasSettings = aliasSettingsPatch(draft.alias_settings, draft.aliases, base.alias_settings);
  const setAlias = (alias: string, d: AliasDraft) => setDraft((x) => ({ ...x, alias_settings: { ...x.alias_settings, [alias]: d } }));
  const dirty = JSON.stringify(draft) !== JSON.stringify(base);
  const set = <K extends keyof Draft>(k: K, v: Draft[K]) => setDraft((d) => ({ ...d, [k]: v }));
  const setCfg = <K extends keyof MailboxConfig>(k: K, v: MailboxConfig[K]) => setDraft((d) => ({ ...d, config: { ...d.config, [k]: v } }));

  // IMAP server settings are edited only where the instance still receives over IMAP.
  const { data: opts } = useConnectOptions();
  const imapReceiving = imapOn(opts);
  // Google and Microsoft sync over IMAP only where the instance receives over IMAP.
  const syncReceiving = syncOn(opts);
  const direct = readsDirectly(mailbox);
  const imap = mailbox.inbound_kind === "imap" && imapReceiving && mailbox.receiving !== "imap_off";
  const smtp = draft.outbound_kind === "smtp";
  const imapChanged = imap && (!sameKeys(draft.config, base.config, IMAP_KEYS) || !!draft.password);
  const sendChanged = draft.outbound_kind !== base.outbound_kind || (smtp && (!sameKeys(draft.config, base.config, SMTP_KEYS) || !!draft.smtp_password));
  // Moving to SMTP from opendesk: no password is stored yet.
  const smtpPasswordNeeded = smtp && base.outbound_kind !== "smtp";
  const sendingValid = !smtp || (!!draft.config.smtp_host?.trim() && validPort(draft.config.smtp_port ?? 0) && (!smtpPasswordNeeded || !!draft.smtp_password));
  const tidyChanged = direct && !sameTidy(draft.config, base.config);

  // Where replies would go through the mailbox's own server: the address's SMTP server, when detection knows it.
  const forwarding = mailbox.inbound_kind === "forwarding";
  const detection = useMailDetection(forwarding || mailbox.outbound_kind === "system" ? mailbox.email : null).data;
  const chooseSending = (kind: Mailbox["outbound_kind"]) =>
    setDraft((d) => {
      if (kind !== "smtp") return { ...d, outbound_kind: kind, smtp_password: "", config: { ...d.config, ...pick(base.config, SMTP_KEYS) } };
      if (base.outbound_kind === "smtp") return { ...d, outbound_kind: kind, config: { ...d.config, ...pick(base.config, SMTP_KEYS) } };
      const found = detection?.settings?.smtp;
      return {
        ...d,
        outbound_kind: kind,
        config: {
          ...d.config,
          smtp_host: d.config.smtp_host || found?.host || "",
          smtp_port: d.config.smtp_port || found?.port || 587,
          smtp_tls: d.config.smtp_tls || found?.security || "starttls",
          smtp_username: d.config.smtp_username || mailbox.email,
        },
      };
    });

  const save = useMutation({
    mutationFn: () =>
      api.mailboxes.update(mailbox.id, {
        name: draft.name.trim(),
        display_name: draft.display_name.trim(),
        team_id: draft.team_id ?? "", // "" clears the team; null would mean "unchanged"
        ...(draft.brand_id && draft.brand_id !== base.brand_id ? { brand_id: draft.brand_id } : {}),
        ...(JSON.stringify(draft.aliases) !== JSON.stringify(base.aliases) ? { aliases: draft.aliases } : {}),
        ...(sameAliasTeams(aliasTeams, base.alias_teams) ? {} : { alias_teams: aliasTeams }),
        ...(aliasSettings ? { alias_settings: aliasSettings } : {}),
        ...(JSON.stringify(draft.trusted_form_senders) !== JSON.stringify(base.trusted_form_senders) ? { trusted_form_senders: draft.trusted_form_senders } : {}),
        ...(draft.from_name_format !== base.from_name_format ? { from_name_format: draft.from_name_format.trim() } : {}),
        signature_html: draft.signature_html,
        auto_reply_enabled: draft.auto_reply_enabled,
        auto_reply_subject: draft.auto_reply_subject.trim(),
        auto_reply_html: draft.auto_reply_html,
        // Only what changed, so saving other fields never needs AI on.
        ...(draft.ai_answers !== base.ai_answers ? { ai_answers: draft.ai_answers } : {}),
        ...(draft.ai_answers_mode !== base.ai_answers_mode ? { ai_answers_mode: draft.ai_answers_mode } : {}),
        ...(draft.ai_answers_outside_hours !== base.ai_answers_outside_hours ? { ai_answers_outside_hours: draft.ai_answers_outside_hours } : {}),
        ...(draft.bot_id !== base.bot_id ? { bot_id: draft.bot_id } : {}),
        ...(draft.outbound_kind !== base.outbound_kind ? { outbound_kind: draft.outbound_kind } : {}),
        // Only the server settings and the Gmail or Outlook options: the rest of config is the API's.
        ...(imap || smtp || tidyChanged
          ? {
              config: {
                ...(imap ? pick(draft.config, IMAP_KEYS) : {}),
                ...(smtp ? pick(draft.config, SMTP_KEYS) : {}),
                ...(tidyChanged ? Object.fromEntries(TIDY_KEYS.map((k) => [k, !!draft.config[k]])) : {}),
              },
            }
          : {}),
        ...(draft.password ? { password: draft.password } : {}),
        ...(smtp && draft.smtp_password ? { smtp_password: draft.smtp_password } : {}),
        // A changed server or password is signed in to before it is saved.
        ...(imapChanged || sendChanged ? { verify: true } : {}),
      }),
    onMutate: () => {
      setRefused(null);
      setSaveProblems({ signature: [], autoReply: [], aliases: null });
    },
    onSuccess: (m) => {
      upsertInList(qc, qk.mailboxes, m);
      setDraft(toDraft(m));
      setSendTest(null);
      toast.success(t("detail.saved"));
    },
    onError: (e) => {
      const failed = connectFailure(e);
      if (failed) setRefused(failed);
      else {
        setSaveProblems({ signature: templateProblems(e, "body.signature_html"), autoReply: templateProblems(e, "auto_reply"), aliases: e });
        toast.error(e);
      }
    },
  });

  // Signing in to Google or Microsoft: reconnecting, or connecting a forwarding mailbox to send.
  // The desktop app does it in the browser (contracts/native.md).
  const provider = signedInProvider(mailbox);
  const [openedInBrowser, setOpenedInBrowser] = useState<Provider | null>(null);
  const signIn = useMutation({
    mutationFn: async (p: Provider) => {
      if (isDesktop) {
        openInBrowser(location.pathname);
        return null;
      }
      // Reading directly keeps reading directly while the workspace can (the API decides); a sync keeps
      // syncing where the instance still receives over IMAP; anything else connects to send only.
      const receive: MailboxReceive | undefined = direct ? undefined : mailbox.inbound_kind === p && syncReceiving ? "sync" : "forwarding";
      return api.mailboxes.oauthStart(p, { mailbox_id: mailbox.id, name: mailbox.name, email: mailbox.email, receive });
    },
    onSuccess: (r, p) => (r ? window.location.assign(r.url) : setOpenedInBrowser(p)),
    onError: (e) => toast.error(e),
  });

  // A forwarding mailbox of a workspace with Collab can read its Google or Microsoft inbox directly instead.
  const directProvider: Provider | null = forwarding ? (signedInProvider(mailbox) ?? detection?.oauth ?? null) : null;
  const directOffered = !!directProvider && apiOn(opts, directProvider);
  const readDirect = useMutation({
    mutationFn: async (p: Provider) => {
      if (isDesktop) {
        openInBrowser(location.pathname);
        return null;
      }
      return api.mailboxes.oauthStart(p, { mailbox_id: mailbox.id, name: mailbox.name, email: mailbox.email, receive: "api" });
    },
    onSuccess: (r, p) => (r ? window.location.assign(r.url) : setOpenedInBrowser(p)),
    onError: (e) => toast.error(e),
  });

  // Receiving by forwarding instead of over IMAP. Replies keep going out the way they do.
  const toForwarding = useMutation({
    mutationFn: () => api.mailboxes.update(mailbox.id, { inbound_kind: "forwarding" }),
    onSuccess: (m) => {
      upsertInList(qc, qk.mailboxes, m);
      toast.success(t("receiving.switched"));
      // The forwarding steps take the place of the warning: bring them into view.
      requestAnimationFrame(() => document.getElementById("mb-forwarding")?.scrollIntoView({ behavior: reduce ? "auto" : "smooth", block: "center" }));
    },
  });
  const switchToForwarding = () =>
    confirm.show(t("receiving.confirmBody", { email: mailbox.email }), () => toForwarding.mutateAsync(), {
      title: t("receiving.confirmTitle"),
      confirmLabel: t("receiving.switch"),
    });

  const runTest = async () => {
    setTesting(true);
    setTest(null);
    try {
      setTest(await api.mailboxes.test(mailbox.id));
    } catch (e) {
      toast.error(errorMessage(e, t("detail.testRunFailed")));
    } finally {
      setTesting(false);
    }
  };

  const runSendTest = async () => {
    setSendTesting(true);
    setSendTest(null);
    setRefused(null);
    try {
      setSendTest(await api.mailboxes.test(mailbox.id));
    } catch (e) {
      toast.error(errorMessage(e, t("detail.testRunFailed")));
    } finally {
      setSendTesting(false);
    }
  };

  const off = notReceiving(mailbox);
  const quiet = quietFor(mailbox);
  const sendingOnly = sendOnly(mailbox);
  // A forwarding mailbox sending through opendesk or SMTP can connect its provider to send as itself.
  const connectable = forwarding && !sendingOnly ? sendProviders(detection, opts) : [];
  const microsoft = isMicrosoftHost(mailboxHost(mailbox));
  const refusedOut = refused && !refused.outbound.ok;
  // Where the instance has sending through opendesk off, it is offered only to a mailbox still on it.
  const systemOffered = systemSendOn(opts) || base.outbound_kind === "system";
  const noSender = mailbox.sending === "system_off";

  return (
    <SettingsPage
      back={<BackLink to="/settings/mailboxes">{t("detail.back")}</BackLink>}
      title={
        <span className="flex items-center gap-3">
          <MailboxIcon kind={mailbox.inbound_kind} size="lg" />
          <span className="min-w-0">
            <span className="block truncate">{mailbox.name}</span>
            <span className="mono block truncate text-[12.5px] font-normal tracking-normal text-fg-3">{mailbox.email}</span>
          </span>
        </span>
      }
      actions={<MailboxStatus mailbox={mailbox} />}
    >
      <Section
        title={t("detail.connection")}
        description={connectionLabel(mailbox, t)}
        actions={
          <Button size="sm" icon={<Zap className="size-3.5" />} loading={testing} onClick={runTest}>
            {t("detail.testConnection")}
          </Button>
        }
      >
        {off && (
          <div className="-mx-4 flex animate-rise-in items-start gap-2.5 border-b border-tone-amber-line bg-tone-amber-soft px-4 py-3.5 motion-reduce:animate-none" role="status">
            <TriangleAlert className="mt-0.5 size-4 shrink-0 text-tone-amber" aria-hidden />
            <div className="min-w-0 flex-1">
              <div className="text-[13px] font-medium">{t("receiving.offTitle")}</div>
              <p className="mt-0.5 text-[12.5px] leading-5 text-fg-2">{t("receiving.offBody", { email: mailbox.email })}</p>
              <Button variant="primary" size="sm" className="mt-3" icon={<Forward className="size-3.5" />} loading={toForwarding.isPending} onClick={switchToForwarding}>
                {t("receiving.switch")}
              </Button>
            </div>
          </div>
        )}
        {!off && mailbox.status === "error" && mailbox.last_error && (
          <div className="-mx-4 border-b border-tone-red-line bg-tone-red-soft px-4 py-3" role="alert">
            <div className="flex items-start gap-2.5 text-[12.5px]">
              <span className="mt-1.5 size-1.5 shrink-0 animate-pulse-soft rounded-full bg-tone-red-solid motion-reduce:animate-none" />
              <div className="min-w-0">
                <div className="font-medium text-tone-red">{t("detail.lastSyncFailed")}</div>
                <div className="mono mt-0.5 break-words text-xs text-fg-2">{mailbox.last_error}</div>
              </div>
            </div>
          </div>
        )}
        {!off && quiet && (
          <div className={cn("-mx-4 border-b px-4 py-3", tone.amber.soft, tone.amber.line)} role="status">
            <div className="flex items-start gap-2.5 text-[12.5px]">
              <MailWarning className={cn("mt-0.5 size-3.5 shrink-0", tone.amber.text)} aria-hidden />
              <div className="min-w-0">
                <div className="font-medium">{quiet.never ? t("detail.quietNeverTitle") : t("detail.quietTitle", { count: quiet.days })}</div>
                <p className="mt-0.5 leading-5 text-fg-2">{t(forwarding ? "detail.quietForwarding" : "detail.quietConnected", { email: mailbox.email })}</p>
              </div>
            </div>
          </div>
        )}
        {test && (
          <div className="border-b border-line-soft py-3.5">
            <TestResults result={test} />
          </div>
        )}
        <Row label={t("detail.lastReceived")} description={t("detail.lastReceivedHint")}>
          <span className="mono text-xs text-fg-3">{mailbox.last_received_at ? relLong(mailbox.last_received_at) : t("detail.noMail")}</span>
        </Row>
        <Row label={t("detail.lastSynced")} description={forwarding ? t("detail.forwardingInstant") : undefined}>
          <span className="mono text-xs text-fg-3">{mailbox.last_synced_at ? relLong(mailbox.last_synced_at) : t("detail.never")}</span>
        </Row>
        {provider && !sendingOnly && (
          <Row
            label={t("detail.account")}
            description={
              direct
                ? apiOn(opts, provider)
                  ? t("detail.accountDirect", { provider: providerName(provider) })
                  : t("detail.accountHintNoImap", { provider: providerName(provider) })
                : syncReceiving
                  ? t("detail.accountHint", { provider: t(`kind.${provider}`) })
                  : t("detail.accountHintNoImap", { provider: providerName(provider) })
            }
          >
            <span className="mono text-xs text-fg-2">{mailbox.config.oauth_email ?? mailbox.email}</span>
            <Button size="sm" icon={<RefreshCw className="size-3.5" />} loading={signIn.isPending && signIn.variables === provider} onClick={() => signIn.mutate(provider)}>
              {t("detail.reconnect")}
            </Button>
          </Row>
        )}
        {provider && !sendingOnly && openedInBrowser && <InBrowserNote name={providerName(provider)} opened className="my-3" />}
        {directOffered && directProvider && (
          <Row label={t("detail.directTitle")} description={t("detail.directHint", { provider: providerName(directProvider) })}>
            <Button
              size="sm"
              icon={directProvider === "google" ? <GoogleGlyph className="size-3.5" /> : <MicrosoftGlyph className="size-3.5" />}
              loading={readDirect.isPending}
              onClick={() => readDirect.mutate(directProvider)}
            >
              {t("detail.directButton")}
            </Button>
          </Row>
        )}
        {directOffered && directProvider && readDirect.data === null && <InBrowserNote name={providerName(directProvider)} opened className="my-3" />}
        {receivesOverImap(mailbox) && !off && (
          <Row
            label={t("receiving.title")}
            description={direct ? t("receiving.apiHint", { provider: providerName(mailbox.inbound_kind === "google" ? "google" : "microsoft") }) : t("receiving.imapHint")}
          >
            <Button size="sm" icon={<Forward className="size-3.5" />} loading={toForwarding.isPending} onClick={switchToForwarding}>
              {t("receiving.switch")}
            </Button>
          </Row>
        )}
        {forwarding && (
          <div id="mb-forwarding" className="scroll-mt-24 py-3.5">
            <ForwardingInstructions mailbox={mailbox} />
          </div>
        )}
        {refused && !refusedOut && (
          <div className="py-3.5">
            <CauseCallout failure={refused} />
          </div>
        )}
        {imap && (
          <div className="py-4">
            <ServerEdit
              title={t("detail.incoming")}
              host={draft.config.imap_host ?? ""}
              port={draft.config.imap_port ?? 993}
              tls={draft.config.imap_tls ?? "tls"}
              username={draft.config.imap_username ?? ""}
              password={draft.password}
              onHost={(v) => setCfg("imap_host", v)}
              onPort={(v) => setCfg("imap_port", v)}
              onTls={(v) => setCfg("imap_tls", v)}
              onUsername={(v) => setCfg("imap_username", v)}
              onPassword={(v) => set("password", v)}
              defaultPorts={[993, 143]}
            />
          </div>
        )}
      </Section>

      {direct && <ProviderTidySection provider={mailbox.inbound_kind === "microsoft" ? "microsoft" : "google"} config={draft.config} onChange={setCfg} />}

      {(ownSending(mailbox.outbound_kind) || sendingOnly) && (
        <Section
          title={t("detail.send.title")}
          description={t("detail.send.hint")}
          actions={
            <Tooltip content={sendChanged ? t("detail.send.saveToTest") : t("detail.send.testHint")}>
              <span className="inline-flex">
                <Button size="sm" icon={<Zap className="size-3.5" />} loading={sendTesting} disabled={sendChanged} onClick={runSendTest}>
                  {t("detail.testConnection")}
                </Button>
              </span>
            </Tooltip>
          }
        >
          {noSender && (
            <div className="-mx-4 flex animate-rise-in items-start gap-2.5 border-b border-tone-amber-line bg-tone-amber-soft px-4 py-3.5 motion-reduce:animate-none" role="status">
              <TriangleAlert className="mt-0.5 size-4 shrink-0 text-tone-amber" aria-hidden />
              <div className="min-w-0 flex-1">
                <div className="text-[13px] font-medium">{t("sendingOff.title")}</div>
                <p className="mt-0.5 text-[12.5px] leading-5 text-fg-2">{t("sendingOff.body", { email: mailbox.email })}</p>
              </div>
            </div>
          )}
          {refusedOut && (
            <div className="border-b border-line-soft py-3.5">
              <CauseCallout failure={refused} />
            </div>
          )}
          {sendTest && !refusedOut && (
            <div className="border-b border-line-soft py-3.5">
              <TestResults result={sendTest} only="outbound" />
            </div>
          )}
          <Row
            label={t("connect.sending")}
            description={
              smtp
                ? t("connect.sendSmtpBody", { email: mailbox.email })
                : draft.outbound_kind === "google" || draft.outbound_kind === "microsoft"
                  ? t("detail.send.providerBody", { provider: providerName(draft.outbound_kind), email: mailbox.email })
                  : noSender && draft.outbound_kind === "system"
                    ? t("sendingOff.choose")
                    : t("connect.sendSystemBody", { sender: opts?.system_sender || "opendesk", email: mailbox.email })
            }
          >
            <Segmented
              value={draft.outbound_kind}
              onChange={chooseSending}
              options={[
                ...(sendingOnly && provider ? [{ value: provider, label: t("detail.send.with", { provider: providerName(provider) }) }] : []),
                ...(systemOffered ? [{ value: "system" as const, label: noSender ? t("sendingOff.none") : t("connect.sendSystem") }] : []),
                { value: "smtp" as const, label: t("connect.sendSmtp") },
              ]}
            />
          </Row>
          {sendingOnly && provider && draft.outbound_kind === provider && (
            <Row label={t("detail.account")} description={t("detail.accountSendOnly", { provider: providerName(provider) })}>
              <span className="mono text-xs text-fg-2">{mailbox.config.oauth_email ?? mailbox.email}</span>
              <Button size="sm" icon={<RefreshCw className="size-3.5" />} loading={signIn.isPending} onClick={() => signIn.mutate(provider)}>
                {t("detail.reconnect")}
              </Button>
            </Row>
          )}
          {!smtp &&
            connectable.map((p) => (
              <Row key={p} label={t("detail.send.connectTitle", { provider: providerName(p) })} description={t("detail.send.connectHint", { email: mailbox.email, provider: providerName(p) })}>
                <Button
                  size="sm"
                  icon={p === "google" ? <GoogleGlyph className="size-3.5" /> : <MicrosoftGlyph className="size-3.5" />}
                  loading={signIn.isPending && signIn.variables === p}
                  onClick={() => signIn.mutate(p)}
                >
                  {t("detail.send.connect", { provider: providerName(p) })}
                </Button>
              </Row>
            ))}
          {(sendingOnly || connectable.length > 0) && openedInBrowser && <InBrowserNote name={providerName(openedInBrowser)} opened className="my-3" />}
          {smtp && (
            <div className="flex flex-col gap-3 py-4">
              {microsoft && <p className="text-xs leading-[18px] text-tone-amber">{t("connect.sendSmtpMicrosoft")}</p>}
              <SmtpPresets
                host={draft.config.smtp_host}
                onPick={(p) => setDraft((d) => ({ ...d, config: { ...d.config, smtp_host: p.host, smtp_port: p.port, smtp_tls: p.tls, smtp_username: p.username ?? "" } }))}
              />
              <ServerEdit
                title={t("detail.send.server")}
                host={draft.config.smtp_host ?? ""}
                port={draft.config.smtp_port ?? 587}
                tls={draft.config.smtp_tls ?? "starttls"}
                username={draft.config.smtp_username ?? ""}
                password={draft.smtp_password}
                passwordHint={smtpPasswordNeeded ? t("detail.send.passwordNeeded") : undefined}
                hostPlaceholder="smtp.company.com"
                onHost={(v) => setCfg("smtp_host", v)}
                onPort={(v) => setCfg("smtp_port", v)}
                onTls={(v) => setCfg("smtp_tls", v)}
                onUsername={(v) => setCfg("smtp_username", v)}
                onPassword={(v) => set("smtp_password", v)}
                defaultPorts={[465, 587]}
              />
            </div>
          )}
        </Section>
      )}

      <Section title={t("detail.sending")} description={t("detail.sendingHint")}>
        <Row label={t("detail.name")} description={t("detail.nameHint")} htmlFor="mb-name">
          <TextInput id="mb-name" className="w-[260px]" value={draft.name} onChange={(e) => set("name", e.target.value)} />
        </Row>
        <Row label={t("detail.senderName")} description={t("detail.senderNameHint")} htmlFor="mb-display">
          <TextInput id="mb-display" className="w-[260px]" value={draft.display_name} placeholder={mailbox.name} onChange={(e) => set("display_name", e.target.value)} />
        </Row>
        {multiBrand && (
          <Row label={t("addressing.brand")} description={t("addressing.brandHint")}>
            <MailboxBrandField className="w-[260px]" value={draft.brand_id} onChange={(v) => set("brand_id", v)} />
          </Row>
        )}
        <Row label={t("addressing.team")} description={t("addressing.teamHint")}>
          <MailboxTeamField className="w-[260px]" value={draft.team_id} onChange={(v) => set("team_id", v)} />
        </Row>
        <Row label={t("addressing.fromName")} description={t("addressing.fromNameHint")} stack>
          <FromNameField
            value={draft.from_name_format}
            onChange={(v) => set("from_name_format", v)}
            email={mailbox.email}
            mailboxName={draft.display_name.trim() || draft.name.trim()}
            teamId={draft.team_id}
            brandId={draft.brand_id}
          />
        </Row>
      </Section>

      <Section title={t("aliases.title")} description={t("aliases.hint")}>
        <div className="flex flex-col gap-3 py-3.5">
          <AliasesField value={draft.aliases} onChange={(v) => set("aliases", v)} email={mailbox.email} />
          <GmailSuggestions
            mailbox={mailbox}
            aliases={draft.aliases}
            onAdd={(alias, name) =>
              setDraft((d) => ({
                ...d,
                aliases: [...d.aliases, alias],
                alias_settings: { ...d.alias_settings, [alias]: { display_name: name, signature_html: "", brand_id: null, send_as: "unknown" } },
              }))
            }
          />
          <AliasIdentities
            mailbox={mailbox}
            aliases={draft.aliases}
            drafts={draft.alias_settings}
            onChange={setAlias}
            aliasTeams={draft.alias_teams}
            onAliasTeams={(v) => set("alias_teams", v)}
            teamId={draft.team_id}
            systemSender={opts?.system_sender || "opendesk"}
            problems={(a) => templateProblems(saveProblems.aliases, `alias_settings.${a}.`)}
          />
        </div>
      </Section>

      <Section title={t("forms.title")} description={t("forms.hint")}>
        <div className="py-3.5">
          <ContactFormsField value={draft.trusted_form_senders} onChange={(v) => set("trusted_form_senders", v)} own={[mailbox.email, ...draft.aliases]} />
        </div>
      </Section>

      <Section title={t("detail.signature")} description={t("detail.signatureHint", { tag: "{{ agent.signature }}" })}>
        <SignatureEditor
          className="py-3.5"
          kind="signature"
          value={draft.signature_html}
          onChange={(v) => {
            set("signature_html", v);
            setSaveProblems((p) => ({ ...p, signature: [] }));
          }}
          placeholder={t("detail.signaturePlaceholder")}
          ariaLabel={t("detail.signature")}
          minHeight={88}
          problems={signatureProblems.length ? signatureProblems : saveProblems.signature}
          previewTitle={t("detail.preview")}
          previewBody={signatureBody}
          onProblems={onSignatureProblems}
        />
      </Section>

      <Section title={t("detail.autoReply")} description={t("detail.autoReplyHint")}>
        <Row label={t("detail.autoReplyToggle")}>
          <Switch aria-label={t("detail.autoReplyToggle")} checked={draft.auto_reply_enabled} onCheckedChange={(v) => set("auto_reply_enabled", v)} />
        </Row>
        {draft.auto_reply_enabled && (
          <div className="flex flex-col gap-3 py-3.5">
            <Field label={t("detail.subject")}>
              {({ id }) => (
                <TextInput
                  ref={subjectRef}
                  id={id}
                  value={draft.auto_reply_subject}
                  placeholder={t("detail.subjectPlaceholder")}
                  onChange={(e) => {
                    set("auto_reply_subject", e.target.value);
                    setSaveProblems((p) => ({ ...p, autoReply: [] }));
                  }}
                  trailing={
                    <VariablePicker
                      kind="subject"
                      side="bottom"
                      onPick={(k) => insertInSubject(tagFor(k))}
                      onInsert={insertInSubject}
                      trigger={
                        <Tooltip content={t("detail.subjectVariable")}>
                          <button
                            type="button"
                            aria-label={t("detail.subjectVariable")}
                            onMouseDown={(e) => e.preventDefault()}
                            className="flex size-6 items-center justify-center rounded text-fg-3 transition-colors hover:bg-hover hover:text-fg data-[state=open]:bg-hover"
                          >
                            <Braces className="size-3.5" />
                          </button>
                        </Tooltip>
                      }
                    />
                  }
                />
              )}
            </Field>
            <div className="flex flex-col gap-1.5">
              <span className="text-[12.5px] font-medium">{t("detail.message")}</span>
              <TemplateEditor
                kind="auto_reply"
                html
                value={draft.auto_reply_html}
                onChange={(v) => {
                  set("auto_reply_html", v);
                  setSaveProblems((p) => ({ ...p, autoReply: [] }));
                }}
                placeholder={t("detail.messagePlaceholder")}
                ariaLabel={t("detail.autoReplyLabel")}
                minHeight={100}
                problems={autoReplyProblems.length ? autoReplyProblems : saveProblems.autoReply}
              />
              <span className="text-[11.5px] leading-4 text-fg-4">{t("detail.autoReplyNoAgent")}</span>
            </div>
            <EmailPreview collapsible defaultOpen={false} height={420} title={t("detail.preview")} body={autoReplyBody} onProblems={onAutoReplyProblems} />
          </div>
        )}
      </Section>

      <MailboxOllieSection value={draft} onChange={(p) => setDraft((d) => ({ ...d, ...p }))} />

      <Section title={t("detail.danger")} danger>
        <Row
          label={mailbox.status === "disabled" ? t("detail.enableTitle") : t("detail.disableTitle")}
          description={mailbox.status === "disabled" ? t("detail.enableHint") : t("detail.disableHint")}
        >
          <Button
            variant="danger"
            loading={setStatus.isPending}
            onClick={() => setStatus.mutate({ m: mailbox, status: mailbox.status === "disabled" ? "active" : "disabled" })}
          >
            {mailbox.status === "disabled" ? t("detail.enable") : t("detail.disable")}
          </Button>
        </Row>
        <Row label={t("detail.removeTitle")} description={t("detail.removeHint")}>
          <Button variant="danger" onClick={() => remove(mailbox, () => void navigate({ to: "/settings/mailboxes" }))}>
            {t("detail.remove")}
          </Button>
        </Row>
      </Section>

      {testDialog}
      <SaveBar dirty={dirty} saving={save.isPending} disabled={!draft.name.trim() || !!fromNameProblem(draft.from_name_format) || (imap && !validPort(draft.config.imap_port ?? 993)) || !sendingValid} onSave={() => save.mutate()} onDiscard={() => setDraft(base)} />
    </SettingsPage>
  );
}

/** For a mailbox that reads Gmail or Outlook directly: what opendesk also does there. */
function ProviderTidySection({ provider, config, onChange }: { provider: Provider; config: MailboxConfig; onChange: (k: TidyKey, v: boolean) => void }) {
  const { t } = useTranslation("settingsChannels");
  const app = provider === "google" ? "Gmail" : "Outlook";
  const rows: { key: TidyKey; label: string; hint?: string }[] = [
    { key: "provider_mark_read", label: t("provider.markRead", { app }) },
    { key: "provider_archive_on_solve", label: t("provider.archive", { app }) },
    { key: "provider_label", label: t("provider.label", { app }), hint: provider === "microsoft" ? t("provider.labelOutlook") : undefined },
  ];
  return (
    <Section title={t("provider.title", { app })} description={t("provider.replies", { app })}>
      {rows.map((r) => (
        <Row key={r.key} label={r.label} description={r.hint}>
          <Switch aria-label={r.label} checked={!!config[r.key]} onCheckedChange={(v) => onChange(r.key, v)} />
        </Row>
      ))}
    </Section>
  );
}

function ServerEdit(p: {
  title: string;
  host: string;
  port: number;
  tls: TlsMode;
  username: string;
  password: string;
  /** Shown under the password; by default, that an empty one keeps the saved one. */
  passwordHint?: string;
  hostPlaceholder?: string;
  /** The usual ports for TLS and STARTTLS: picking a security mode on one of them moves to the other. */
  defaultPorts: [number, number];
  onHost: (v: string) => void;
  onPort: (v: number) => void;
  onTls: (v: TlsMode) => void;
  onUsername: (v: string) => void;
  onPassword: (v: string) => void;
}) {
  const { t } = useTranslation("settingsChannels");
  return (
    <fieldset className="flex min-w-0 flex-col gap-3 rounded-lg border border-line bg-sunken/50 p-3.5">
      <legend className="px-1 text-[12.5px] font-medium">{p.title}</legend>
      <div className="grid grid-cols-[1fr_110px] gap-3">
        <Field label={t("detail.server")}>
          {({ id }) => <TextInput id={id} className="mono" invalid={!p.host.trim()} value={p.host} placeholder={p.hostPlaceholder} onChange={(e) => p.onHost(e.target.value)} />}
        </Field>
        <Field label={t("detail.port")} error={validPort(p.port) ? undefined : t("wizard.err.port")}>
          {({ id, invalid }) => <PortInput id={id} value={p.port} invalid={invalid} onChange={p.onPort} />}
        </Field>
      </div>
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-1 xl:grid-cols-2">
        <Field label={t("detail.username")}>{({ id }) => <TextInput id={id} className="mono" autoComplete="off" value={p.username} onChange={(e) => p.onUsername(e.target.value)} />}</Field>
        <Field label={t("detail.password")} hint={p.passwordHint ?? t("detail.passwordKeep")}>
          {({ id }) => <TextInput id={id} type="password" autoComplete="new-password" value={p.password} placeholder={p.passwordHint ? undefined : "••••••••"} onChange={(e) => p.onPassword(e.target.value)} />}
        </Field>
      </div>
      <div className="flex items-center justify-between gap-3">
        <span className="text-[12.5px] font-medium">{t("detail.security")}</span>
        <Segmented
          size="xs"
          value={p.tls}
          onChange={(v) => {
            p.onTls(v);
            // A usual port follows the security mode; a custom one stays.
            const [tlsPort, startPort] = p.defaultPorts;
            if (v === "tls" && p.port === startPort) p.onPort(tlsPort);
            if (v === "starttls" && p.port === tlsPort) p.onPort(startPort);
          }}
          options={[
            { value: "tls", label: "TLS" },
            { value: "starttls", label: "STARTTLS" },
            ...(p.tls === "none" || offersNoTls(p.host) ? [{ value: "none" as const, label: t("detail.tlsNone") }] : []),
          ]}
        />
      </div>
    </fieldset>
  );
}
