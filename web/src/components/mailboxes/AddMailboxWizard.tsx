import { useMutation, useQueryClient } from "@tanstack/react-query";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { InBrowserNote } from "@/components/desktop/InBrowser";
import { isDesktop, openInBrowser } from "@/lib/desktop";
import { ArrowRight, Check, ChevronDown, ChevronLeft, Forward, KeyRound, LockKeyhole, Send, Server, ShieldCheck } from "lucide-react";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Trans, useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { useConfirm } from "@/components/ui/confirm";
import { Field, TextInput } from "@/components/ui/input";
import { Segmented } from "@/components/ui/tabs";
import { api, type CreateMailboxBody, type MailboxReceive } from "@/lib/api/endpoints";
import { errorMessage } from "@/lib/api/client";
import type { MailDetection, MailHost, Mailbox, MailboxConnectOptions, MailboxTest, SyncStart, TlsMode } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { isGoogleHost, isMicrosoftHost, looksLikeGoogleAppPassword, mailHostLabel, offersNoTls, stripAppPassword } from "@/lib/mailHost";
import { qk, upsertInList } from "@/lib/queries";
import { tone } from "@/lib/tones";
import { useBrands, useTeams } from "@/components/settings2/teams";
import { GoogleGlyph, MicrosoftGlyph, TestResults } from "./kinds";
import { AliasesField, aliasTeamsFor, AliasTeamsField, DEFAULT_FROM, FromNameField, fromNameProblem, MailboxBrandField, MailboxTeamField } from "./addressing";
import {
  apiOn,
  AppPasswordGuide,
  CauseCallout,
  connectFailure,
  DetectionCard,
  ExternalButton,
  imapOn,
  isGuideHost,
  NotConfiguredPanel,
  oauthOffered,
  oauthUnconfigured,
  sendProviders,
  ServerSummary,
  Steps,
  SyncStartField,
  syncOn,
  systemSendOn,
  useConnectOptions,
  useMailDetection,
  type ConnectFailure,
} from "./connect";
import { ForwardingInstructions } from "./forwarding";
import { PortInput, validPort } from "./PortInput";
import { SmtpPresets } from "./smtpPresets";

/** google and microsoft sync the mailbox over IMAP; google_api and microsoft_api read the inbox directly through the provider's API. */
export type Method = "google" | "microsoft" | "google_api" | "microsoft_api" | "app_password" | "password" | "forwarding" | "imap";

type Provider = "google" | "microsoft";
const apiMethod = (p: Provider): Method => (p === "google" ? "google_api" : "microsoft_api");
const isApiMethod = (m: Method | null): m is "google_api" | "microsoft_api" => m === "google_api" || m === "microsoft_api";

const emailRe = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
const hostRe = /^[a-z0-9.-]+\.[a-z]{2,}$/i;

type Form = {
  name: string;
  email: string;
  display_name: string;
  imap_host: string;
  imap_port: number;
  imap_username: string;
  imap_password: string;
  imap_tls: TlsMode;
  smtp_same: boolean;
  smtp_host: string;
  smtp_port: number;
  smtp_username: string;
  smtp_password: string;
  smtp_tls: TlsMode;
  /** Forwarding mailboxes: replies through opendesk, the mailbox's own SMTP server, or a send-only Google or Microsoft connection. */
  send_via: SendVia;
  mail_host: MailHost | "";
  team_id: string;
  brand_id: string;
  aliases: string[];
  /** An alias to its own team; cleared when the mailbox's team is private. */
  alias_teams: Record<string, string>;
  from_name_format: string;
  /** Ways that read the inbox: where the first sync starts. */
  sync_start: SyncStart;
};

const empty: Form = {
  name: "",
  email: "",
  display_name: "",
  imap_host: "",
  imap_port: 993,
  imap_username: "",
  imap_password: "",
  imap_tls: "tls",
  smtp_same: true,
  smtp_host: "",
  smtp_port: 587,
  smtp_username: "",
  smtp_password: "",
  smtp_tls: "starttls",
  send_via: "system",
  mail_host: "",
  team_id: "",
  brand_id: "",
  aliases: [],
  alias_teams: {},
  from_name_format: DEFAULT_FROM,
  sync_start: "recent",
};

type SendVia = "system" | "smtp" | "google" | "microsoft";
const isProvider = (v: SendVia): v is "google" | "microsoft" => v === "google" || v === "microsoft";
const providerName = (p: "google" | "microsoft") => (p === "google" ? "Google" : "Microsoft");

type Errors = Partial<Record<keyof Form, string>>;

type T = TFunction<"settingsChannels">;

/** Brand, aliases, From name and team, as the create body takes them. */
function addressing(f: Form): Pick<CreateMailboxBody, "team_id" | "brand_id" | "aliases" | "alias_teams" | "from_name_format"> {
  const aliasTeams = aliasTeamsFor(f.alias_teams, f.aliases, false);
  return {
    team_id: f.team_id || null,
    ...(f.brand_id ? { brand_id: f.brand_id } : {}),
    ...(f.aliases.length ? { aliases: f.aliases } : {}),
    ...(Object.keys(aliasTeams).length ? { alias_teams: aliasTeams } : {}),
    from_name_format: f.from_name_format.trim() || DEFAULT_FROM,
  };
}

function validateAddress(f: Form, t: T): Errors {
  const e: Errors = {};
  if (!f.name.trim()) e.name = t("wizard.err.name");
  if (!emailRe.test(f.email.trim())) e.email = f.email.trim() ? t("wizard.err.emailInvalid") : t("wizard.err.email");
  return e;
}

/** The provider the next step signs in to, if any: reading the inbox, or a forwarding mailbox's send-only connection. */
function signInFor(method: Method | null, f: Form): "google" | "microsoft" | null {
  if (method === "google" || method === "microsoft") return method;
  if (method === "google_api") return "google";
  if (method === "microsoft_api") return "microsoft";
  if (method === "forwarding" && isProvider(f.send_via)) return f.send_via;
  return null;
}

function validateConnect(method: Method, f: Form, t: T): Errors {
  const e: Errors = {};
  if (!signInFor(method, f) && fromNameProblem(f.from_name_format)) e.from_name_format = t("addressing.fromInvalid");
  if (method === "app_password" || method === "password") {
    if (!f.imap_password.trim()) e.imap_password = t("wizard.err.imapPassword");
  }
  const smtp = method === "imap" || (method === "forwarding" && f.send_via === "smtp");
  if (method === "imap") {
    if (!hostRe.test(f.imap_host.trim())) e.imap_host = t("wizard.err.imapHost");
    if (!validPort(f.imap_port)) e.imap_port = t("wizard.err.port");
    if (!f.imap_username.trim()) e.imap_username = t("wizard.err.imapUser");
    if (!f.imap_password) e.imap_password = t("wizard.err.imapPassword");
  }
  if (smtp) {
    const own = method === "forwarding" || !f.smtp_same;
    if (!hostRe.test(f.smtp_host.trim())) e.smtp_host = t("wizard.err.smtpHost");
    if (!validPort(f.smtp_port)) e.smtp_port = t("wizard.err.port");
    if (own && !f.smtp_username.trim()) e.smtp_username = t("wizard.err.smtpUser");
    if (own && !f.smtp_password) e.smtp_password = t("wizard.err.smtpPassword");
  }
  return e;
}

type Way = { id: Method; recommended?: boolean; advanced?: boolean };

/** Whether there is more than forwarding to choose from. */
function choices(opts: MailboxConnectOptions | undefined): boolean {
  return imapOn(opts) || apiOn(opts, "google") || apiOn(opts, "microsoft");
}

/**
 * The ways mail can reach opendesk for a detected host, best first: reading a Google or
 * Microsoft inbox directly where the workspace can, forwarding, then, where the mailbox
 * may receive over IMAP, signing in to it.
 */
function suggestedWays(det: MailDetection | undefined, opts: MailboxConnectOptions | undefined): Way[] {
  const direct = det?.oauth && apiOn(opts, det.oauth) ? det.oauth : null;
  const out: Way[] = direct ? [{ id: apiMethod(direct), recommended: true }, { id: "forwarding" }] : [{ id: "forwarding", recommended: true }];
  if (!imapOn(opts)) return out;
  if (!direct && det?.oauth && syncOn(opts) && oauthOffered(opts, det.oauth)) out.push({ id: det.oauth });
  if (det?.settings && det.password_auth === "app_password") out.push({ id: "app_password" });
  if (det?.settings && det.password_auth === "password") out.push({ id: "password" });
  out.push({ id: "imap", advanced: true });
  return out;
}

/** Every way in, when the suggested ones don't fit. */
function allWays(det: MailDetection | undefined, opts: MailboxConnectOptions | undefined): Way[] {
  const out: Way[] = [{ id: "forwarding" }];
  for (const p of ["google", "microsoft"] as const) if (apiOn(opts, p)) out.push({ id: apiMethod(p) });
  if (!imapOn(opts)) return out;
  for (const p of ["google", "microsoft"] as const) if (!apiOn(opts, p) && syncOn(opts) && oauthOffered(opts, p)) out.push({ id: p });
  if (det?.settings && det.password_auth === "app_password") out.push({ id: "app_password" });
  if (det?.settings && det.password_auth === "password") out.push({ id: "password" });
  out.push({ id: "imap" });
  return out;
}

/** The ways replies can leave a forwarding mailbox: a send-only sign-in where the instance offers one, SMTP, or opendesk where it sends for mailboxes. */
function sendWays(det: MailDetection | undefined, opts: MailboxConnectOptions | undefined): SendVia[] {
  return [...sendProviders(det, opts), "smtp", ...(systemSendOn(opts) ? (["system"] as const) : [])];
}

/** Replies go out from the address itself when its provider's sign-in is offered, else through opendesk, else SMTP. */
function defaultSendVia(det: MailDetection | undefined, opts: MailboxConnectOptions | undefined): SendVia {
  if (det?.oauth && oauthOffered(opts, det.oauth)) return det.oauth;
  return systemSendOn(opts) ? "system" : "smtp";
}

/** Arrow keys move the choice within a radio group, as a native one does. */
function radioKeys(e: React.KeyboardEvent<HTMLElement>) {
  const step = e.key === "ArrowDown" || e.key === "ArrowRight" ? 1 : e.key === "ArrowUp" || e.key === "ArrowLeft" ? -1 : 0;
  if (!step) return;
  const radios = Array.from(e.currentTarget.querySelectorAll<HTMLElement>('[role="radio"]'));
  const i = radios.indexOf(document.activeElement as HTMLElement);
  if (i < 0) return;
  e.preventDefault();
  const next = radios[(i + step + radios.length) % radios.length];
  next.focus();
  next.click();
}

function wayIcon(id: Method): ReactNode {
  switch (id) {
    case "google":
    case "google_api":
      return <GoogleGlyph className="size-[18px]" />;
    case "microsoft":
    case "microsoft_api":
      return <MicrosoftGlyph className="size-[18px]" />;
    case "app_password":
      return <KeyRound className="size-[18px]" />;
    case "password":
      return <LockKeyhole className="size-[18px]" />;
    case "forwarding":
      return <Forward className="size-[18px]" />;
  }
  return <Server className="size-[18px]" />;
}

const wayTone: Record<Method, keyof typeof tone> = {
  google: "blue",
  microsoft: "cyan",
  google_api: "blue",
  microsoft_api: "cyan",
  app_password: "amber",
  password: "indigo",
  forwarding: "teal",
  imap: "gray",
};

function sendIcon(v: SendVia): ReactNode {
  if (v === "google") return <GoogleGlyph className="size-[18px]" />;
  if (v === "microsoft") return <MicrosoftGlyph className="size-[18px]" />;
  if (v === "smtp") return <Server className="size-[18px]" />;
  return <Send className="size-[18px]" />;
}
const sendTone: Record<SendVia, keyof typeof tone> = { google: "blue", microsoft: "cyan", smtp: "indigo", system: "gray" };

const slide = {
  enter: (dir: number) => ({ x: dir * 16, opacity: 0 }),
  center: { x: 0, opacity: 1 },
  exit: (dir: number) => ({ x: dir * -16, opacity: 0 }),
};
const still = { enter: { opacity: 0 }, center: { opacity: 1 }, exit: { opacity: 0 } };

export function AddMailboxWizard({
  onDone,
  onCancel,
  compact,
  extraFooter,
  brandId,
}: {
  onDone: (m: Mailbox | null) => void;
  onCancel?: () => void;
  compact?: boolean;
  extraFooter?: ReactNode;
  /** Prefill the brand (from a brand's Addresses tab). */
  brandId?: string;
}) {
  const { t } = useTranslation("settingsChannels");
  const { t: tShell } = useTranslation("shell");
  const reduce = useReducedMotion();
  const qc = useQueryClient();
  const confirm = useConfirm();
  const [step, setStep] = useState<0 | 1 | 2>(0);
  const [dir, setDir] = useState(1);
  const [method, setMethod] = useState<Method | null>(null);
  const [showAll, setShowAll] = useState(false);
  const [sub, setSub] = useState(0); // Gmail app password: 2-Step Verification, app password, connect
  const initial = useMemo(() => ({ ...empty, brand_id: brandId ?? "" }), [brandId]);
  const [form, setForm] = useState<Form>(initial);
  const [errors, setErrors] = useState<Errors>({});
  const [blocked, setBlocked] = useState<string | null>(null);
  const [failure, setFailure] = useState<ConnectFailure | null>(null);
  const [created, setCreated] = useState<Mailbox | null>(null);
  const [test, setTest] = useState<MailboxTest | null>(null);
  const [lookup, setLookup] = useState<string | null>(null);
  // Once someone picks how replies are sent, a new lookup no longer changes it.
  const [sendPicked, setSendPicked] = useState(false);

  const opts = useConnectOptions();
  const detect = useMailDetection(lookup);
  const det = detect.data;
  const domain = lookup?.split("@")[1]?.toLowerCase() ?? "";
  const gmailPath = method === "app_password" && isGoogleHost(det?.host);
  const ways = useMemo(() => (showAll ? allWays(det, opts.data) : suggestedWays(det, opts.data)), [showAll, det, opts.data]);

  // Pick the best way once the host is known.
  useEffect(() => {
    if (!lookup || detect.isLoading) return;
    setMethod((m) => (m && ways.some((w) => w.id === m) ? m : ways[0]?.id ?? null));
  }, [lookup, detect.isLoading, ways]);

  const set = <K extends keyof Form>(k: K, v: Form[K]) => {
    if (k === "send_via") setSendPicked(true);
    setForm((f) => ({ ...f, [k]: v }));
    if (errors[k]) setErrors((e) => ({ ...e, [k]: undefined }));
    if (failure && (k === "imap_password" || k === "smtp_password" || k.endsWith("_host") || k.endsWith("_port"))) setFailure(null);
  };
  const go = (to: 0 | 1 | 2) => {
    setDir(to > step ? 1 : -1);
    setBlocked(null);
    setFailure(null);
    setStep(to);
  };
  const dirty = step > 0 && step < 2 && JSON.stringify(form) !== JSON.stringify(initial);

  /** Commits the typed address for detection. */
  const commitEmail = () => {
    const e = form.email.trim().toLowerCase();
    if (emailRe.test(e) && e !== lookup) {
      setLookup(e);
      setShowAll(false);
    }
  };

  /** Fills the servers from detection, keeping what the person typed for this address. */
  const prefill = (m: Method) => {
    const email = form.email.trim();
    setForm((f) => {
      const s = det?.settings;
      const next = { ...f, mail_host: det?.host ?? "" };
      // A provider that is no longer on offer (another address) falls back to the default.
      if (m === "forwarding" && (!sendPicked || !sendWays(det, opts.data).includes(f.send_via))) next.send_via = defaultSendVia(det, opts.data);
      if (s && (m !== "imap" || !f.imap_host)) {
        Object.assign(next, {
          imap_host: s.imap.host,
          imap_port: s.imap.port,
          imap_tls: s.imap.security,
          smtp_host: s.smtp.host,
          smtp_port: s.smtp.port,
          smtp_tls: s.smtp.security,
        });
      }
      if (!f.imap_username || m !== "imap") next.imap_username = email;
      if (!f.smtp_username || m !== "imap") next.smtp_username = email;
      return next;
    });
  };

  // The desktop app connects in the browser, where the provider's sign-in works and the session lives.
  const [openedInBrowser, setOpenedInBrowser] = useState(false);
  const oauth = useMutation({
    mutationFn: async ({ provider, receive }: { provider: "google" | "microsoft"; receive: MailboxReceive }) => {
      if (isDesktop) {
        openInBrowser(location.pathname);
        return null;
      }
      return api.mailboxes.oauthStart(provider, {
        name: form.name.trim(),
        email: form.email.trim() || undefined,
        receive,
        sync_start: receive === "forwarding" ? undefined : form.sync_start,
      });
    },
    onSuccess: (r) => (r ? window.location.assign(r.url) : setOpenedInBrowser(true)),
    onError: (e) => setBlocked(errorMessage(e)),
  });

  const create = useMutation({
    mutationFn: async () => {
      const f = form;
      const google = isGoogleHost(f.mail_host);
      const base = {
        name: f.name.trim(),
        email: f.email.trim(),
        display_name: f.display_name.trim() || undefined,
        ...addressing(f),
      };
      const imapCfg = { imap_host: f.imap_host.trim(), imap_port: f.imap_port, imap_username: f.imap_username.trim(), imap_tls: f.imap_tls };
      const smtpCfg = { smtp_host: f.smtp_host.trim(), smtp_port: f.smtp_port, smtp_tls: f.smtp_tls };
      let body: CreateMailboxBody;
      if (method === "forwarding") {
        body =
          f.send_via === "smtp"
            ? { ...base, inbound_kind: "forwarding", outbound_kind: "smtp", config: { ...smtpCfg, smtp_username: f.smtp_username.trim(), mail_host: f.mail_host }, smtp_password: f.smtp_password, verify: true }
            : { ...base, inbound_kind: "forwarding", outbound_kind: "system", config: { mail_host: f.mail_host } };
      } else if (method === "imap") {
        body = {
          ...base,
          inbound_kind: "imap",
          outbound_kind: "smtp",
          config: { ...imapCfg, ...smtpCfg, smtp_username: (f.smtp_same ? f.imap_username : f.smtp_username).trim(), mail_host: f.mail_host, sync_start: f.sync_start },
          password: f.imap_password,
          smtp_password: f.smtp_same ? f.imap_password : f.smtp_password,
          verify: true,
        };
      } else {
        const pw = google ? stripAppPassword(f.imap_password) : f.imap_password.trim();
        body = {
          ...base,
          inbound_kind: "imap",
          outbound_kind: "smtp",
          config: { ...imapCfg, ...smtpCfg, imap_username: f.email.trim(), smtp_username: f.email.trim(), mail_host: f.mail_host, sync_start: f.sync_start },
          password: pw,
          smtp_password: pw,
          verify: true,
        };
      }
      const m = await api.mailboxes.create(body);
      upsertInList(qc, qk.mailboxes, m);
      setCreated(m);
      if (body.verify) setTest({ inbound: { ok: true, error: "" }, outbound: { ok: true, error: "" } });
      return m;
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.mailboxes });
      void qc.invalidateQueries({ queryKey: qk.billing, exact: true });
      go(2);
    },
    onError: (e) => {
      const f = connectFailure(e);
      if (f) {
        setFailure(f);
        setBlocked(null);
      } else setBlocked(errorMessage(e));
    },
  });

  const next = () => {
    if (step === 0) {
      const e = validateAddress(form, t);
      setErrors(e);
      if (Object.keys(e).length) return setBlocked(t("wizard.fixFields"));
      const email = form.email.trim().toLowerCase();
      if (email !== lookup) return commitEmail();
      if (detect.isLoading) return;
      if (!method) return setBlocked(t("wizard.chooseMethod"));
      prefill(method);
      setSub(0);
      return go(1);
    }
    if (step === 1 && method) {
      if (gmailPath && sub < 2) {
        setDir(1);
        return setSub(sub + 1);
      }
      const e = validateConnect(method, form, t);
      setErrors(e);
      if (Object.keys(e).length) return setBlocked(t("wizard.fixFields"));
      setBlocked(null);
      setFailure(null);
      const provider = signInFor(method, form);
      if (provider) oauth.mutate({ provider, receive: method === "forwarding" ? "forwarding" : isApiMethod(method) ? "api" : "sync" });
      else create.mutate();
    }
  };

  const back = () => {
    if (gmailPath && sub > 0) {
      setDir(-1);
      return setSub(sub - 1);
    }
    go(0);
  };

  const cancel = () => {
    if (!onCancel) return;
    if (!dirty) return onCancel();
    confirm.show(t("wizard.discardBody"), onCancel, { title: t("wizard.discardTitle"), confirmLabel: t("wizard.discard"), danger: true });
  };

  const busy = oauth.isPending || create.isPending;
  const hostName = det?.label && det.host && det.host !== "other" ? det.label : "";
  const signIn = signInFor(method, form);
  const primaryLabel =
    step === 0
      ? t("wizard.continue")
      : isDesktop && signIn
        ? tShell("desktop.continueInBrowser")
        : signIn === "google"
          ? t("wizard.continueGoogle")
          : signIn === "microsoft"
            ? t("wizard.continueMicrosoft")
            : gmailPath && sub === 0
              ? t("connect.gmail.s1Next")
              : gmailPath && sub === 1
                ? t("connect.gmail.s2Next")
                : method === "imap"
                  ? t("wizard.connectTest")
                  : method === "forwarding" && form.send_via === "system"
                    ? t("wizard.create")
                    : t("wizard.connect");

  const body = (
    <div className="relative overflow-hidden">
      <AnimatePresence mode="wait" initial={false} custom={dir}>
        <motion.div
          key={`${step}-${gmailPath ? sub : 0}`}
          custom={dir}
          variants={reduce ? still : slide}
          initial="enter"
          animate="center"
          exit="exit"
          transition={{ duration: reduce ? 0.1 : 0.18, ease: [0.22, 1, 0.36, 1] }}
        >
          {step === 0 && (
            <div className="flex flex-col gap-4">
              <div className={cn("grid gap-3", !compact && "sm:grid-cols-2")}>
                <Field label={t("wizard.name")} hint={t("wizard.nameHint")} error={errors.name}>
                  {({ id, invalid }) => (
                    <TextInput id={id} autoFocus invalid={invalid} value={form.name} placeholder={t("wizard.namePlaceholder")} onChange={(e) => set("name", e.target.value)} />
                  )}
                </Field>
                <Field label={t("wizard.email")} hint={t("wizard.emailHint")} error={errors.email}>
                  {({ id, invalid }) => (
                    <TextInput
                      id={id}
                      type="email"
                      className="mono"
                      invalid={invalid}
                      value={form.email}
                      placeholder="support@company.com"
                      onChange={(e) => set("email", e.target.value)}
                      onBlur={commitEmail}
                      onKeyDown={(e) => {
                        if (e.key === "Enter") {
                          e.preventDefault();
                          next();
                        }
                      }}
                    />
                  )}
                </Field>
              </div>
              {lookup && (
                <div className="flex flex-col gap-3">
                  <DetectionCard domain={domain} detection={det} loading={detect.isLoading} failed={detect.isError} />
                  {!detect.isLoading && !choices(opts.data) && <ForwardingWay email={form.email.trim()} />}
                  {!detect.isLoading && choices(opts.data) && (
                    <WayPicker
                      ways={ways}
                      method={method}
                      det={det}
                      opts={opts.data}
                      showAll={showAll}
                      compact={compact}
                      onPick={(m) => {
                        setMethod(m);
                        setBlocked(null);
                      }}
                      onOpen={(m) => {
                        setMethod(m);
                        prefill(m);
                        setSub(0);
                        go(1);
                      }}
                      onToggleAll={() => setShowAll((v) => !v)}
                    />
                  )}
                </div>
              )}
            </div>
          )}

          {step === 1 && method && (
            <ConnectStep
              method={method}
              form={form}
              set={set}
              errors={errors}
              compact={compact}
              det={det}
              opts={opts.data}
              sub={sub}
              onEditServers={() => {
                setMethod("imap");
                setForm((f) => ({ ...f, smtp_same: true }));
              }}
            />
          )}
          {step === 1 && signIn && <InBrowserNote name={providerName(signIn)} opened={openedInBrowser} className="mt-4" />}

          {step === 2 && created && <DoneStep mailbox={created} method={method!} test={test} />}
        </motion.div>
      </AnimatePresence>
    </div>
  );

  const footer = (
    <>
      {step === 1 && (
        <Button variant="ghost" icon={<ChevronLeft className="size-3.5" />} onClick={back} disabled={busy}>
          {t("wizard.back")}
        </Button>
      )}
      {step < 2 && onCancel && (
        <Button variant="ghost" onClick={cancel} disabled={busy}>
          {t("wizard.cancel")}
        </Button>
      )}
      {extraFooter}
      <div className="ml-auto flex min-w-0 items-center gap-3">
        {create.isPending && (
          <span className="hidden truncate text-xs text-fg-3 sm:inline" role="status">
            {hostName ? t("connect.checking", { host: hostName }) : t("connect.checkingGeneric")}
          </span>
        )}
        {step < 2 ? (
          <Button variant="primary" onClick={next} loading={busy} iconRight={!busy && <ArrowRight className="size-3.5" />}>
            {primaryLabel}
          </Button>
        ) : (
          <Button variant="primary" onClick={() => onDone(created)}>
            {t("wizard.done")}
          </Button>
        )}
      </div>
    </>
  );

  const error = (failure || blocked) && (
    <div className="mt-4">
      {failure ? (
        <CauseCallout failure={failure} />
      ) : (
        <p className="text-xs text-danger-fg" role="alert">
          {blocked}
        </p>
      )}
    </div>
  );

  if (!compact)
    return (
      <div className="animate-rise-in overflow-hidden rounded-lg border border-line bg-surface shadow-xs">
        <Stepper step={step} />
        <div className="px-5 py-5">
          {body}
          {error}
        </div>
        <div className="flex items-center gap-2 border-t border-line-soft bg-sunken px-5 py-3">{footer}</div>
      </div>
    );

  return (
    <div className="flex flex-col">
      {body}
      {error}
      <div className="mt-5 flex items-center gap-2">{footer}</div>
    </div>
  );
}

/** Address, connect, done: where you are in adding a mailbox. */
function Stepper({ step }: { step: 0 | 1 | 2 }) {
  const { t } = useTranslation("settingsChannels");
  const labels = [t("wizard.stepAddress"), t("wizard.stepConnect"), t("wizard.stepDone")];
  return (
    <ol className="flex items-center gap-2 border-b border-line-soft px-5 py-3" aria-label={t("new.title")}>
      {labels.map((l, i) => {
        const done = i < step;
        const cur = i === step;
        return (
          <li key={l} className="flex items-center gap-2" aria-current={cur ? "step" : undefined}>
            {i > 0 && <span aria-hidden className={cn("h-px w-6 transition-colors duration-200", done || cur ? "bg-fg-3" : "bg-line")} />}
            <span
              className={cn(
                "mono flex size-[18px] items-center justify-center rounded-sm text-[10.5px] transition-colors duration-200",
                done ? "bg-inverse text-inverse-fg" : cur ? "border border-fg-2 text-fg" : "border border-line text-fg-4",
              )}
            >
              {done ? <Check className="size-2.5" strokeWidth={3} /> : i + 1}
            </span>
            <span className={cn("text-[12.5px]", cur ? "font-medium text-fg" : done ? "text-fg-2" : "text-fg-4")}>{l}</span>
          </li>
        );
      })}
    </ol>
  );
}

function WayPicker({
  ways,
  method,
  det,
  opts,
  showAll,
  compact,
  onPick,
  onOpen,
  onToggleAll,
}: {
  ways: Way[];
  method: Method | null;
  det: MailDetection | undefined;
  opts: MailboxConnectOptions | undefined;
  showAll: boolean;
  compact?: boolean;
  onPick: (m: Method) => void;
  onOpen: (m: Method) => void;
  onToggleAll: () => void;
}) {
  const { t } = useTranslation("settingsChannels");
  const hostLabel = det?.label || mailHostLabel(det?.host);
  const unconfigured = det?.oauth && oauthUnconfigured(opts, det.oauth) ? det.oauth : null;
  const msNoPassword = isMicrosoftHost(det?.host) && !(det?.oauth && (oauthOffered(opts, det.oauth) || apiOn(opts, det.oauth)));
  return (
    <div className="flex flex-col gap-2.5">
      <div className="text-[12.5px] font-medium">{t("connect.howTitle")}</div>
      {unconfigured && <NotConfiguredPanel provider={unconfigured} />}
      {!showAll && msNoPassword && <Note>{t("connect.microsoftNoPassword")}</Note>}
      {!showAll && det?.host === "proton" && <Note>{t("connect.protonNoImap")}</Note>}
      <div role="radiogroup" aria-label={t("wizard.methodsLabel")} onKeyDown={radioKeys} className={cn("grid gap-2", !compact && "sm:grid-cols-2")}>
        {ways.map((w, i) => (
          <Choice
            key={w.id}
            on={method === w.id}
            tabbable={method === w.id || (i === 0 && !ways.some((x) => x.id === method))}
            index={i}
            icon={wayIcon(w.id)}
            tint={wayTone[w.id]}
            title={t(`connect.method.${w.id}`)}
            badge={
              !showAll && w.recommended ? (
                <span className={cn("rounded-sm px-1.5 py-px text-[10.5px] font-medium", tone.green.chip)}>{t("connect.recommended")}</span>
              ) : !showAll && w.advanced ? (
                <span className={cn("rounded-sm px-1.5 py-px text-[10.5px] font-medium", tone.gray.chip)}>{t("connect.advanced")}</span>
              ) : null
            }
            body={t(`connect.method.${w.id}Body`, { host: hostLabel || "your provider" })}
            onPick={() => onPick(w.id)}
            onOpen={() => onOpen(w.id)}
          />
        ))}
      </div>
      <button type="button" onClick={onToggleAll} className="self-start rounded-sm text-xs text-fg-3 underline decoration-line-strong underline-offset-2 outline-none hover:text-fg focus-visible:shadow-[0_0_0_2px_var(--focus)]">
        {showAll ? t("connect.suggestedWays") : t("connect.anotherWay")}
      </button>
    </div>
  );
}

/** Where receiving over IMAP is off, mail always arrives by forwarding: said once, not offered as a choice. */
function ForwardingWay({ email }: { email: string }) {
  const { t } = useTranslation("settingsChannels");
  return (
    <div className="flex animate-rise-in items-start gap-3 rounded-lg border border-line bg-surface p-3.5 motion-reduce:animate-none">
      <span className={cn("flex size-9 shrink-0 items-center justify-center rounded-md", tone.teal.chip)}>
        <Forward className="size-[18px]" />
      </span>
      <span className="min-w-0 flex-1">
        <span className="block text-[13px] font-medium">{t("connect.forwardingOnly")}</span>
        <span className="mt-0.5 block text-xs leading-[18px] text-fg-3">{t("connect.forwardingOnlyBody", { email })}</span>
      </span>
    </div>
  );
}

/** One option in a radio group of cards: an icon tile, a title, a line of help and the radio dot. */
function Choice({
  on,
  tabbable = on,
  index,
  icon,
  tint,
  title,
  badge,
  body,
  onPick,
  onOpen,
}: {
  on: boolean;
  /** The one option Tab lands on: the chosen one, else the first. */
  tabbable?: boolean;
  index: number;
  icon: ReactNode;
  tint: keyof typeof tone;
  title: ReactNode;
  badge?: ReactNode;
  body: ReactNode;
  onPick: () => void;
  onOpen?: () => void;
}) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={on}
      tabIndex={tabbable ? 0 : -1}
      onClick={onPick}
      onDoubleClick={onOpen}
      style={{ animationDelay: `${index * 30}ms` }}
      className={cn(
        "group relative flex animate-rise-in items-start gap-3 rounded-lg border bg-surface p-3.5 text-left outline-none transition-[border-color,box-shadow,background-color,transform] duration-150 active:scale-[0.99] focus-visible:shadow-[0_0_0_3px_var(--focus)] motion-reduce:animate-none motion-reduce:active:scale-100",
        on ? "border-fg-2 shadow-[0_0_0_1px_var(--fg-2)]" : "border-line hover:border-line-strong hover:bg-sunken/60",
      )}
    >
      <span className={cn("flex size-9 shrink-0 items-center justify-center rounded-md", tone[tint].chip)}>{icon}</span>
      <span className="min-w-0 flex-1">
        <span className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[13px] font-medium">
          {title}
          {badge}
        </span>
        <span className="mt-0.5 block text-xs leading-[18px] text-fg-3">{body}</span>
      </span>
      <span
        className={cn(
          "mt-0.5 flex size-4 shrink-0 items-center justify-center rounded-full border transition-colors",
          on ? "border-inverse bg-inverse text-inverse-fg" : "border-line-strong",
        )}
      >
        {on && <Check className="size-2.5 animate-indicator-in motion-reduce:animate-none" strokeWidth={3.5} />}
      </span>
    </button>
  );
}

/** How replies leave a forwarding mailbox. */
function SendPicker({
  ways,
  value,
  email,
  sender,
  onChange,
}: {
  ways: SendVia[];
  value: SendVia;
  email: string;
  sender: string;
  onChange: (v: SendVia) => void;
}) {
  const { t } = useTranslation("settingsChannels");
  return (
    <div role="radiogroup" aria-label={t("connect.sending")} onKeyDown={radioKeys} className="flex flex-col gap-2">
      {ways.map((w, i) => (
        <Choice
          key={w}
          on={value === w}
          tabbable={value === w || (i === 0 && !ways.includes(value))}
          index={i}
          icon={sendIcon(w)}
          tint={sendTone[w]}
          title={t(`connect.send.${w}`)}
          body={t(`connect.send.${w}Body`, { email, sender })}
          onPick={() => onChange(w)}
        />
      ))}
    </div>
  );
}

function Note({ children }: { children: ReactNode }) {
  return <p className="rounded-md border border-line bg-sunken px-3 py-2.5 text-xs leading-[18px] text-fg-3">{children}</p>;
}

function ConnectStep({
  method,
  form,
  set,
  errors,
  compact,
  det,
  opts,
  sub,
  onEditServers,
}: {
  method: Method;
  form: Form;
  set: <K extends keyof Form>(k: K, v: Form[K]) => void;
  errors: Errors;
  compact?: boolean;
  det: MailDetection | undefined;
  opts: MailboxConnectOptions | undefined;
  sub: number;
  onEditServers: () => void;
}) {
  const { t } = useTranslation("settingsChannels");
  const servers = (
    <ServerSummary
      imap={{ host: form.imap_host, port: form.imap_port, security: form.imap_tls === "starttls" ? "starttls" : "tls" }}
      smtp={{ host: form.smtp_host, port: form.smtp_port, security: form.smtp_tls === "starttls" ? "starttls" : "tls" }}
      onEdit={onEditServers}
      note={isGoogleHost(form.mail_host) ? t("connect.gmail.s3Note") : t("connect.verifiedNote")}
    />
  );

  if (method === "google" || method === "microsoft")
    return (
      <div className="flex flex-col gap-4">
        <Summary form={form} det={det} />
        {!isDesktop && <Note>{t("wizard.oauthNote", { provider: method === "google" ? "Google" : "Microsoft" })}</Note>}
        <SyncStartField value={form.sync_start} onChange={(v) => set("sync_start", v)} />
      </div>
    );

  if (isApiMethod(method)) {
    const provider = method === "google_api" ? "Google" : "Microsoft";
    return (
      <div className="flex flex-col gap-4">
        <Summary form={form} det={det} />
        <Note>{t("connect.directNote", { provider })}</Note>
        {!isDesktop && <Note>{t("wizard.oauthNote", { provider })}</Note>}
        <SyncStartField value={form.sync_start} onChange={(v) => set("sync_start", v)} />
      </div>
    );
  }

  if (method === "app_password" && isGoogleHost(form.mail_host))
    return (
      <GmailAppPassword
        sub={sub}
        form={form}
        set={set}
        errors={errors}
        servers={
          <>
            {servers}
            <SyncStartField value={form.sync_start} onChange={(v) => set("sync_start", v)} />
          </>
        }
      />
    );

  if (method === "app_password" || method === "password") {
    const guideHost = form.mail_host;
    const guide = det?.app_password_url && isGuideHost(guideHost) ? <AppPasswordGuide host={guideHost} url={det.app_password_url} /> : null;
    const app = method === "app_password";
    return (
      <div className="flex flex-col gap-4">
        <Summary form={form} det={det} />
        {!app && guide && guideHost === "zoho" && <Note>{t("connect.zohoTwoFactor")}</Note>}
        {guide}
        <Field label={app ? t("connect.appPassword") : t("connect.password")} error={errors.imap_password}>
          {({ id, invalid }) => (
            <TextInput
              id={id}
              type="password"
              autoComplete="new-password"
              autoFocus={!guide}
              invalid={invalid}
              value={form.imap_password}
              placeholder={app ? t("connect.appPasswordPlaceholder") : t("connect.passwordPlaceholder")}
              onChange={(e) => set("imap_password", e.target.value)}
            />
          )}
        </Field>
        {servers}
        <SyncStartField value={form.sync_start} onChange={(v) => set("sync_start", v)} />
        <AddressingFields form={form} set={set} errors={errors} compact={compact} />
      </div>
    );
  }

  if (method === "forwarding") {
    const ways = sendWays(det, opts);
    const via = ways.includes(form.send_via) ? form.send_via : ways[ways.length - 1];
    const unconfigured = det?.oauth && oauthUnconfigured(opts, det.oauth) ? det.oauth : null;
    return (
      <div className="flex flex-col gap-4">
        <Summary form={form} det={det} />
        <Note>{t("connect.forwardingIntro")}</Note>
        <div className="flex flex-col gap-2.5">
          <span className="text-[12.5px] font-medium">{t("connect.sending")}</span>
          {unconfigured && !imapOn(opts) && <NotConfiguredPanel provider={unconfigured} />}
          <SendPicker
            ways={ways}
            value={via}
            email={form.email.trim()}
            sender={opts?.system_sender || "opendesk"}
            onChange={(v) => set("send_via", v)}
          />
          {isProvider(via) && !isDesktop && <Note>{t("connect.sendOnlyNote", { provider: providerName(via) })}</Note>}
          {via === "smtp" && isMicrosoftHost(form.mail_host) && <p className="text-xs leading-[18px] text-tone-amber">{t("connect.sendSmtpMicrosoft")}</p>}
        </div>
        {via === "smtp" && (
          <ServerFields
            presets={
              <SmtpPresets
                host={form.smtp_host}
                onPick={(p) => {
                  set("smtp_host", p.host);
                  set("smtp_port", p.port);
                  set("smtp_tls", p.tls);
                  set("smtp_username", p.username ?? "");
                }}
              />
            }
            title={t("wizard.outgoing")}
            host={form.smtp_host}
            port={form.smtp_port}
            tls={form.smtp_tls}
            username={form.smtp_username}
            password={form.smtp_password}
            onHost={(v) => set("smtp_host", v)}
            onPort={(v) => set("smtp_port", v)}
            onTls={(v) => {
              set("smtp_tls", v);
              set("smtp_port", v === "tls" ? 465 : 587);
            }}
            onUsername={(v) => set("smtp_username", v)}
            onPassword={(v) => set("smtp_password", v)}
            errors={{ host: errors.smtp_host, port: errors.smtp_port, username: errors.smtp_username, password: errors.smtp_password }}
            hostPlaceholder="smtp.company.com"
            compact={compact}
          />
        )}
        {!isProvider(via) && <AddressingFields form={form} set={set} errors={errors} compact={compact} />}
      </div>
    );
  }

  // Other IMAP and SMTP.
  return (
    <div className="flex flex-col gap-4">
      <Summary form={form} det={det} />
      <ServerFields
        title={t("wizard.incoming")}
        host={form.imap_host}
        port={form.imap_port}
        tls={form.imap_tls}
        username={form.imap_username}
        password={form.imap_password}
        onHost={(v) => set("imap_host", v)}
        onPort={(v) => set("imap_port", v)}
        onTls={(v) => {
          set("imap_tls", v);
          set("imap_port", v === "tls" ? 993 : 143);
        }}
        onUsername={(v) => set("imap_username", v)}
        onPassword={(v) => set("imap_password", v)}
        errors={{ host: errors.imap_host, port: errors.imap_port, username: errors.imap_username, password: errors.imap_password }}
        hostPlaceholder="imap.company.com"
        compact={compact}
      />
      <ServerFields
        title={t("wizard.outgoing")}
        host={form.smtp_host}
        port={form.smtp_port}
        tls={form.smtp_tls}
        username={form.smtp_username}
        password={form.smtp_password}
        onHost={(v) => set("smtp_host", v)}
        onPort={(v) => set("smtp_port", v)}
        onTls={(v) => {
          set("smtp_tls", v);
          set("smtp_port", v === "tls" ? 465 : 587);
        }}
        onUsername={(v) => set("smtp_username", v)}
        onPassword={(v) => set("smtp_password", v)}
        errors={{ host: errors.smtp_host, port: errors.smtp_port, username: errors.smtp_username, password: errors.smtp_password }}
        hostPlaceholder="smtp.company.com"
        hideCredentials={form.smtp_same}
        compact={compact}
        extra={
          <label className="flex items-center gap-2 text-[12.5px] text-fg-2">
            <Checkbox checked={form.smtp_same} onCheckedChange={(v) => set("smtp_same", v)} />
            {t("wizard.sameCredentials")}
          </label>
        }
      />
      <SyncStartField value={form.sync_start} onChange={(v) => set("sync_start", v)} />
      <AddressingFields form={form} set={set} errors={errors} compact={compact} />
    </div>
  );
}

/** Who is being connected: the address and its host, carried over from the first step. */
function Summary({ form, det }: { form: Form; det: MailDetection | undefined }) {
  const label = det?.host && det.host !== "other" ? det.label : "";
  return (
    <div className="flex items-center gap-2.5 text-[12.5px]">
      <span className="font-medium">{form.name.trim()}</span>
      <span className="mono truncate text-fg-3">{form.email.trim()}</span>
      {label && <span className={cn("ml-auto shrink-0 rounded-sm px-1.5 py-px text-[11px] font-medium", tone.gray.chip)}>{label}</span>}
    </div>
  );
}

/** The Gmail and Google Workspace app password, in three steps. */
function GmailAppPassword({
  sub,
  form,
  set,
  errors,
  servers,
}: {
  sub: number;
  form: Form;
  set: <K extends keyof Form>(k: K, v: Form[K]) => void;
  errors: Errors;
  servers: ReactNode;
}) {
  const { t } = useTranslation("settingsChannels");
  const b = <b />;
  const cleaned = stripAppPassword(form.imap_password);
  const lengthOff = cleaned.length > 0 && !looksLikeGoogleAppPassword(cleaned);
  const labels = [t("connect.gmail.p1"), t("connect.gmail.p2"), t("connect.gmail.p3")];
  return (
    <div className="flex flex-col gap-4">
      <p className={cn("rounded-md px-3 py-2 text-xs leading-[18px]", tone.blue.chip)}>{t("connect.gmail.banner")}</p>
      <div className="flex items-center gap-1.5" aria-hidden>
        {labels.map((l, i) => (
          <div key={l} className="min-w-0 flex-1">
            <div className={cn("h-1 rounded-full transition-colors duration-200", i < sub ? "bg-fg-2" : i === sub ? "bg-fg-3" : "bg-line")} />
            <div className={cn("mt-1.5 truncate text-[11px] font-medium", i === sub ? "text-fg" : i < sub ? "text-fg-2" : "text-fg-4")}>{l}</div>
          </div>
        ))}
      </div>
      <div className="flex items-start gap-3">
        <span className="flex size-9 shrink-0 items-center justify-center rounded-md border border-line bg-surface">
          {sub === 2 ? <KeyRound className="size-4 text-fg-2" aria-hidden /> : <ShieldCheck className="size-4 text-fg-2" aria-hidden />}
        </span>
        <div className="min-w-0">
          <div className="text-[13.5px] font-medium">{t(`connect.gmail.s${(sub + 1) as 1 | 2 | 3}Title`)}</div>
          <div className="text-xs text-fg-3">{t(`connect.gmail.s${(sub + 1) as 1 | 2 | 3}Sub`)}</div>
        </div>
      </div>
      {sub === 0 && (
        <>
          <Steps items={[<Trans key="a" t={t} i18nKey="connect.gmail.s1a" components={{ b }} />, t("connect.gmail.s1b")]} />
          <ExternalButton href="https://myaccount.google.com/security">{t("connect.gmail.s1Open")}</ExternalButton>
          <p className="text-xs leading-[18px] text-fg-3">{t("connect.gmail.s1Note")}</p>
          <p className="text-xs text-fg-4">{t("connect.gmail.s1Already")}</p>
        </>
      )}
      {sub === 1 && (
        <>
          <Steps
            items={[
              t("connect.gmail.s2a"),
              <Trans key="b" t={t} i18nKey="connect.gmail.s2b" components={{ b }} />,
              t("connect.gmail.s2c"),
            ]}
          />
          <ExternalButton href="https://myaccount.google.com/apppasswords">{t("connect.gmail.s2Open")}</ExternalButton>
          <p className="text-xs leading-[18px] text-fg-3">{t("connect.gmail.s2Note")}</p>
        </>
      )}
      {sub === 2 && (
        <>
          <Field label={t("connect.appPassword")} error={errors.imap_password}>
            {({ id, invalid }) => (
              <TextInput
                id={id}
                type="password"
                autoComplete="new-password"
                autoFocus
                className="mono"
                invalid={invalid}
                value={form.imap_password}
                placeholder="xxxx xxxx xxxx xxxx"
                onChange={(e) => set("imap_password", e.target.value)}
              />
            )}
          </Field>
          {lengthOff && <p className="-mt-2 text-xs leading-[18px] text-tone-amber">{t("connect.gmail.lengthHint")}</p>}
          {servers}
        </>
      )}
    </div>
  );
}

/** Sender name, team, brand, From name and aliases, folded away until wanted. */
function AddressingFields({
  form,
  set,
  errors,
  compact,
  defaultOpen,
}: {
  form: Form;
  set: <K extends keyof Form>(k: K, v: Form[K]) => void;
  errors: Errors;
  compact?: boolean;
  defaultOpen?: boolean;
}) {
  const { multi: multiBrand } = useBrands();
  const { teams } = useTeams();
  const { t } = useTranslation("settingsChannels");
  const [open, setOpen] = useState(!!defaultOpen || !!errors.from_name_format);
  const setTeam = (id: string | null) => {
    set("team_id", id ?? "");
    // A private team's address delivers every alias to that team.
    if (teams.find((x) => x.id === id)?.private) set("alias_teams", {});
  };
  const reduce = useReducedMotion();
  useEffect(() => {
    if (errors.from_name_format) setOpen(true);
  }, [errors.from_name_format]);
  return (
    <div className="rounded-lg border border-line">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-2 rounded-lg px-3.5 py-2.5 text-left outline-none hover:bg-sunken/60 focus-visible:shadow-[0_0_0_3px_var(--focus)]"
      >
        <span className="text-[12.5px] font-medium">{t("connect.addressing")}</span>
        <span className="text-xs text-fg-4">{t("connect.addressingHint")}</span>
        <ChevronDown className={cn("ml-auto size-3.5 text-fg-3 transition-transform duration-150", open && "rotate-180")} aria-hidden />
      </button>
      <AnimatePresence initial={false}>
        {open && (
          <motion.div
            initial={reduce ? { opacity: 0 } : { height: 0, opacity: 0 }}
            animate={reduce ? { opacity: 1 } : { height: "auto", opacity: 1 }}
            exit={reduce ? { opacity: 0 } : { height: 0, opacity: 0 }}
            transition={{ duration: 0.18, ease: [0.22, 1, 0.36, 1] }}
            className="overflow-hidden"
          >
            <div className="flex flex-col gap-3 border-t border-line-soft px-3.5 py-3.5">
              <div className={cn("grid gap-3", !compact && "sm:grid-cols-2")}>
                <Field label={t("wizard.senderName")} optional hint={t("wizard.senderNameHint")}>
                  {({ id }) => <TextInput id={id} value={form.display_name} placeholder={t("wizard.senderPlaceholder")} onChange={(e) => set("display_name", e.target.value)} />}
                </Field>
                <Field label={t("addressing.team")} optional hint={t("addressing.teamHint")}>
                  {() => <MailboxTeamField className="w-full" value={form.team_id || null} onChange={setTeam} />}
                </Field>
                {multiBrand && (
                  <Field label={t("addressing.brand")} hint={t("addressing.brandHint")}>
                    {() => <MailboxBrandField className="w-full" value={form.brand_id || null} onChange={(v) => set("brand_id", v)} />}
                  </Field>
                )}
              </div>
              <Field label={t("addressing.fromName")} hint={t("addressing.fromNameHint")} error={errors.from_name_format}>
                {() => (
                  <FromNameField
                    value={form.from_name_format}
                    onChange={(v) => set("from_name_format", v)}
                    email={form.email.trim()}
                    mailboxName={form.display_name.trim() || form.name.trim()}
                    teamId={form.team_id || null}
                    brandId={form.brand_id || null}
                  />
                )}
              </Field>
              <Field label={t("addressing.aliases")} optional hint={t("addressing.aliasesHint")}>
                {() => (
                  <div className="flex flex-col gap-3">
                    <AliasesField value={form.aliases} onChange={(v) => set("aliases", v)} email={form.email} />
                    <AliasTeamsField aliases={form.aliases} value={form.alias_teams} onChange={(v) => set("alias_teams", v)} teamId={form.team_id || null} />
                  </div>
                )}
              </Field>
            </div>
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
}

function ServerFields(p: {
  title: string;
  /** Above the server: sending services that fill it in. */
  presets?: ReactNode;
  host: string;
  port: number;
  tls: TlsMode;
  username: string;
  password: string;
  onHost: (v: string) => void;
  onPort: (v: number) => void;
  onTls: (v: TlsMode) => void;
  onUsername: (v: string) => void;
  onPassword: (v: string) => void;
  errors: { host?: string; port?: string; username?: string; password?: string };
  hostPlaceholder: string;
  hideCredentials?: boolean;
  extra?: ReactNode;
  compact?: boolean;
}) {
  const { t } = useTranslation("settingsChannels");
  return (
    <fieldset className="flex min-w-0 flex-col gap-3 rounded-lg border border-line bg-sunken/50 p-3.5">
      <legend className="px-1 text-[12.5px] font-medium">{p.title}</legend>
      {p.presets}
      <div className={cn("grid gap-3", p.compact ? "grid-cols-[1fr_96px]" : "grid-cols-[1fr_110px]")}>
        <Field label={t("wizard.server")} error={p.errors.host}>
          {({ id, invalid }) => <TextInput id={id} className="mono" invalid={invalid} value={p.host} placeholder={p.hostPlaceholder} onChange={(e) => p.onHost(e.target.value)} />}
        </Field>
        <Field label={t("wizard.port")} error={p.errors.port}>
          {({ id, invalid }) => <PortInput id={id} value={p.port} invalid={invalid} onChange={p.onPort} />}
        </Field>
      </div>
      <div className="flex flex-col gap-1.5">
        <span className="text-[12.5px] font-medium">{t("wizard.security")}</span>
        <Segmented
          value={p.tls}
          onChange={p.onTls}
          options={[
            { value: "tls", label: "TLS" },
            { value: "starttls", label: "STARTTLS" },
            ...(p.tls === "none" || offersNoTls(p.host) ? [{ value: "none" as const, label: t("wizard.tlsNone") }] : []),
          ]}
        />
        {p.tls === "none" && <p className="text-xs text-tone-amber">{t("wizard.noTls")}</p>}
      </div>
      {p.extra}
      {!p.hideCredentials && (
        <div className={cn("grid gap-3", !p.compact && "sm:grid-cols-2")}>
          <Field label={t("wizard.username")} error={p.errors.username}>
            {({ id, invalid }) => <TextInput id={id} className="mono" autoComplete="off" invalid={invalid} value={p.username} onChange={(e) => p.onUsername(e.target.value)} />}
          </Field>
          <Field label={t("wizard.password")} hint={t("wizard.passwordHint")} error={p.errors.password}>
            {({ id, invalid }) => <TextInput id={id} type="password" autoComplete="new-password" invalid={invalid} value={p.password} onChange={(e) => p.onPassword(e.target.value)} />}
          </Field>
        </div>
      )}
    </fieldset>
  );
}

function DoneStep({ mailbox, method, test }: { mailbox: Mailbox; method: Method; test: MailboxTest | null }) {
  const { t } = useTranslation("settingsChannels");
  if (method === "forwarding")
    return (
      <div className="flex flex-col gap-4">
        <div className="flex items-center gap-3">
          <span className={cn("flex size-8 items-center justify-center rounded-md animate-indicator-in motion-reduce:animate-none", tone.teal.chip)}>
            <Forward className="size-4" />
          </span>
          <div>
            <div className="text-[13.5px] font-medium">{t("wizard.created", { name: mailbox.name })}</div>
            <div className="text-xs text-fg-3">{t("wizard.createdForward")}</div>
          </div>
        </div>
        <ForwardingInstructions mailbox={mailbox} />
      </div>
    );
  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center gap-3">
        <span className={cn("flex size-8 items-center justify-center rounded-md animate-indicator-in motion-reduce:animate-none", tone.green.chip)}>
          <Check className="size-4" strokeWidth={2.5} />
        </span>
        <div>
          <div className="text-[13.5px] font-medium">{t("wizard.connected", { name: mailbox.name })}</div>
          <div className="mono text-xs text-fg-3">{mailbox.email}</div>
        </div>
      </div>
      {test ? <TestResults result={test} /> : <p className="text-xs text-fg-3">{t("wizard.testRunning")}</p>}
      {test && (!test.inbound.ok || !test.outbound.ok) && <p className="text-xs text-fg-3">{t("wizard.fixLater")}</p>}
    </div>
  );
}

