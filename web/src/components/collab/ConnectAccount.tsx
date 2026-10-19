// Connecting a mail account: where it is (Google, Microsoft or any IMAP server the
// instance offers), then who sees it (only you, or shared with everyone or the
// teams and people you pick), then the sign-in or the IMAP form.

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Lock, Server, Users } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { api, type CollabImapBody } from "@/lib/api/endpoints";
import { ApiError, errorMessage } from "@/lib/api/client";
import type { CollabStatus, MailAudience, MailProvider, MailTls } from "@/lib/api/types";
import { defaultPort, guessServers, presetForEmail } from "@/lib/collab/imapPresets";
import { isEmail } from "@/lib/collab/recipients";
import { isDesktop, openInBrowser } from "@/lib/desktop";
import { cn } from "@/lib/cn";
import { qk } from "@/lib/queries";
import { storeAccount } from "@/lib/collab/hooks";
import { useSession } from "@/lib/session";
import { GoogleGlyph, MicrosoftGlyph } from "@/components/mailboxes/kinds";
import { PortInput, validPort } from "@/components/mailboxes/PortInput";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Field, TextInput } from "@/components/ui/input";
import { Segmented } from "@/components/ui/tabs";
import { toast } from "@/components/ui/toast";
import { audienceIsEmpty, ChoiceCard, SharedAudience } from "./pickers";

type Step = "provider" | "audience" | "imap";

export type ImapForm = {
  email: string;
  name: string;
  password: string;
  imap_host: string;
  imap_port: number;
  imap_tls: MailTls;
  imap_username: string;
  smtp_host: string;
  smtp_port: number;
  smtp_tls: MailTls;
  smtp_username: string;
  smtp_password: string;
};

const emptyImap: ImapForm = { email: "", name: "", password: "", imap_host: "", imap_port: 993, imap_tls: "tls", imap_username: "", smtp_host: "", smtp_port: 465, smtp_tls: "tls", smtp_username: "", smtp_password: "" };

/** Starts the provider's sign-in; the browser leaves for it (the desktop app opens the browser). */
export async function startOAuth(provider: "google" | "microsoft", body: Parameters<typeof api.collab.accounts.oauthStart>[1]): Promise<boolean> {
  if (isDesktop) {
    openInBrowser("/settings/mail-accounts");
    return false;
  }
  const { url } = await api.collab.accounts.oauthStart(provider, body);
  window.location.assign(url);
  return true;
}

function ProviderCard({ provider, onPick }: { provider: MailProvider; onPick: () => void }) {
  const { t } = useTranslation("collab");
  const icon = provider === "google" ? <GoogleGlyph className="size-4" /> : provider === "microsoft" ? <MicrosoftGlyph className="size-4" /> : <Server className="size-4 text-fg-3" />;
  return (
    <button
      type="button"
      onClick={onPick}
      className="flex w-full items-center gap-3 rounded-lg border border-line bg-surface px-3.5 py-3 text-left outline-none transition-[border-color,transform] duration-150 hover:border-line-strong focus-visible:shadow-[0_0_0_3px_var(--focus)] active:scale-[0.995] motion-reduce:active:scale-100"
    >
      <span className="flex size-8 shrink-0 items-center justify-center rounded-md border border-line bg-surface">{icon}</span>
      <span className="min-w-0 flex-1">
        <span className="block text-[13px] font-medium">{t(`connect.${provider}`)}</span>
        <span className="block text-[12px] text-fg-3">{t(`connect.${provider}Hint`)}</span>
      </span>
    </button>
  );
}

/** The IMAP and SMTP fields, shared with the account page. */
export function ImapFields({ form, onChange, passwordOptional, showIdentity = true }: { form: ImapForm; onChange: (patch: Partial<ImapForm>) => void; passwordOptional?: boolean; showIdentity?: boolean }) {
  const { t } = useTranslation("collab");
  const [advanced, setAdvanced] = useState(!!form.imap_username && form.imap_username !== form.email);
  const preset = presetForEmail(form.email);
  const server = (kind: "imap" | "smtp") => {
    const host = kind === "imap" ? form.imap_host : form.smtp_host;
    const port = kind === "imap" ? form.imap_port : form.smtp_port;
    const tls = kind === "imap" ? form.imap_tls : form.smtp_tls;
    return (
      <fieldset className="flex flex-col gap-2">
        <legend className="mb-1.5 text-xs font-medium text-fg-2">{kind === "imap" ? t("connect.imapServer") : t("connect.smtpServer")}</legend>
        <div className="grid grid-cols-[1fr_88px] gap-2">
          <TextInput aria-label={`${kind === "imap" ? t("connect.imapServer") : t("connect.smtpServer")} ${t("connect.host")}`} placeholder={kind === "imap" ? "imap.example.com" : "smtp.example.com"} value={host} onChange={(e) => onChange(kind === "imap" ? { imap_host: e.target.value.trim() } : { smtp_host: e.target.value.trim() })} />
          <PortInput value={port} onChange={(n) => onChange(kind === "imap" ? { imap_port: n } : { smtp_port: n })} aria-describedby={undefined} />
        </div>
        <Segmented
          size="xs"
          value={tls}
          onChange={(v) => onChange(kind === "imap" ? { imap_tls: v, imap_port: defaultPort("imap", v) } : { smtp_tls: v, smtp_port: defaultPort("smtp", v) })}
          options={[
            { value: "tls", label: t("connect.tls") },
            { value: "starttls", label: t("connect.starttls") },
          ]}
        />
      </fieldset>
    );
  };
  return (
    <div className="flex flex-col gap-4">
      {showIdentity && (
        <>
          <Field label={t("connect.address")}>
            {(p) => (
              <TextInput
                {...p}
                type="email"
                autoComplete="email"
                autoFocus
                value={form.email}
                onChange={(e) => {
                  const email = e.target.value.trim();
                  const pr = presetForEmail(email);
                  const guess = pr ?? (form.imap_host ? null : guessServers(email));
                  onChange({ email, ...(guess ? { imap_host: guess.imap_host, imap_port: guess.imap_port, imap_tls: guess.imap_tls, smtp_host: guess.smtp_host, smtp_port: guess.smtp_port, smtp_tls: guess.smtp_tls } : {}) });
                }}
                placeholder="you@company.com"
              />
            )}
          </Field>
          <Field label={t("connect.name")} optional>
            {(p) => <TextInput {...p} value={form.name} onChange={(e) => onChange({ name: e.target.value })} placeholder={t("connect.namePlaceholder")} />}
          </Field>
        </>
      )}
      <Field
        label={preset?.appPassword ? t("connect.appPassword") : t("connect.password")}
        optional={passwordOptional}
        hint={passwordOptional ? t("detail.passwordUnchanged") : preset?.appPassword ? t("connect.appPasswordHint", { host: preset.name }) : undefined}
      >
        {(p) => <TextInput {...p} type="password" autoComplete="new-password" value={form.password} onChange={(e) => onChange({ password: e.target.value })} />}
      </Field>
      {preset && showIdentity && <p className="-mt-2 text-[11.5px] text-fg-3">{t("connect.preset", { name: preset.name })}</p>}
      <div className="grid gap-4 sm:grid-cols-2">
        {server("imap")}
        {server("smtp")}
      </div>
      <div>
        <button type="button" aria-expanded={advanced} onClick={() => setAdvanced((a) => !a)} className="text-[12px] text-fg-3 underline-offset-2 hover:text-fg hover:underline">
          {t("connect.advanced")}: {t("connect.usernames")}
        </button>
        {advanced && (
          <div className="mt-3 grid gap-3 sm:grid-cols-2 animate-rise-in motion-reduce:animate-none">
            <Field label={t("connect.imapUsername")} optional>
              {(p) => <TextInput {...p} value={form.imap_username} onChange={(e) => onChange({ imap_username: e.target.value })} placeholder={form.email} />}
            </Field>
            <Field label={t("connect.smtpUsername")} optional>
              {(p) => <TextInput {...p} value={form.smtp_username} onChange={(e) => onChange({ smtp_username: e.target.value })} placeholder={form.imap_username || form.email} />}
            </Field>
            <Field label={t("connect.smtpPassword")} optional hint={t("connect.smtpPasswordHint")} className="sm:col-span-2">
              {(p) => <TextInput {...p} type="password" autoComplete="new-password" value={form.smtp_password} onChange={(e) => onChange({ smtp_password: e.target.value })} />}
            </Field>
          </div>
        )}
      </div>
    </div>
  );
}

export function imapValid(f: ImapForm, needPassword: boolean): boolean {
  return isEmail(f.email) && (!needPassword || !!f.password) && !!f.imap_host && !!f.smtp_host && validPort(f.imap_port) && validPort(f.smtp_port);
}

export function ConnectAccountDialog({ open, onOpenChange, status }: { open: boolean; onOpenChange: (o: boolean) => void; status: CollabStatus }) {
  const { t } = useTranslation("collab");
  const qc = useQueryClient();
  const me = useSession((s) => s.user?.id);
  const [step, setStep] = useState<Step>("provider");
  const [provider, setProvider] = useState<MailProvider | null>(null);
  const [visibility, setVisibility] = useState<"private" | "shared">("private");
  const [audience, setAudience] = useState<MailAudience>({ everyone: true, team_ids: [], user_ids: [] });
  const [hint, setHint] = useState("");
  const [imap, setImap] = useState<ImapForm>(emptyImap);
  const [failure, setFailure] = useState<string | null>(null);
  const [openedInBrowser, setOpenedInBrowser] = useState(false);

  const reset = () => {
    setStep("provider");
    setProvider(null);
    setVisibility("private");
    setAudience({ everyone: true, team_ids: [], user_ids: [] });
    setHint("");
    setImap(emptyImap);
    setFailure(null);
    setOpenedInBrowser(false);
  };
  const close = (o: boolean) => {
    onOpenChange(o);
    if (!o) setTimeout(reset, 200);
  };

  const shared = visibility === "shared" ? (audience.everyone ? { everyone: true, team_ids: [], user_ids: [] } : audience) : undefined;
  const audienceOk = visibility === "private" || !audienceIsEmpty(audience);

  const oauth = useMutation({
    mutationFn: (p: "google" | "microsoft") => startOAuth(p, { visibility, shared_with: shared, email: hint.trim() || undefined }),
    onSuccess: (left) => !left && setOpenedInBrowser(true),
    onError: (e) => setFailure(errorMessage(e)),
  });

  const create = useMutation({
    mutationFn: () => {
      const body: CollabImapBody = {
        email: imap.email,
        name: imap.name.trim() || undefined,
        imap_host: imap.imap_host,
        imap_port: imap.imap_port,
        imap_tls: imap.imap_tls,
        imap_username: imap.imap_username.trim() || undefined,
        password: imap.password,
        smtp_host: imap.smtp_host,
        smtp_port: imap.smtp_port,
        smtp_tls: imap.smtp_tls,
        smtp_username: imap.smtp_username.trim() || undefined,
        smtp_password: imap.smtp_password || undefined,
        visibility,
        shared_with: shared,
      };
      return api.collab.accounts.createImap(body);
    },
    onSuccess: (a) => {
      storeAccount(qc, a);
      void qc.invalidateQueries({ queryKey: qk.collabStatus });
      toast.success(t("accounts.connected", { email: a.email }));
      close(false);
    },
    onError: (e) => {
      if (e instanceof ApiError && e.code === "account_conflict") setFailure(t("accounts.error.account_conflict"));
      else setFailure(errorMessage(e));
    },
  });

  const offered = (["google", "microsoft", "imap"] as const).filter((p) => status.providers[p]);
  const providerName = provider ? t(`connect.${provider}`) : "";

  const footer =
    step === "provider" ? undefined : (
      <>
        <Button variant="ghost" className="mr-auto" icon={<ArrowLeft />} onClick={() => (setFailure(null), setStep(step === "imap" ? "audience" : "provider"))}>
          {t("connect.back")}
        </Button>
        {step === "audience" && provider !== "imap" && provider && (
          <Button variant="primary" loading={oauth.isPending} disabled={!audienceOk} onClick={() => oauth.mutate(provider as "google" | "microsoft")}>
            {t("connect.continueWith", { provider: providerName })}
          </Button>
        )}
        {step === "audience" && provider === "imap" && (
          <Button variant="primary" disabled={!audienceOk} onClick={() => setStep("imap")}>
            {t("connect.continue")}
          </Button>
        )}
        {step === "imap" && (
          <Button variant="primary" loading={create.isPending} disabled={!imapValid(imap, true)} onClick={() => (setFailure(null), create.mutate())}>
            {create.isPending ? t("connect.connecting") : t("connect.connectButton")}
          </Button>
        )}
      </>
    );

  return (
    <Dialog
      open={open}
      onOpenChange={close}
      size={step === "imap" ? "lg" : "md"}
      title={t("connect.title")}
      description={step === "provider" ? t("connect.providerStep") : step === "audience" ? t("connect.audienceStep") : providerName}
      footer={footer}
    >
      <div className="flex flex-col gap-3 pb-2">
        {step === "provider" &&
          (offered.length ? (
            offered.map((p) => (
              <ProviderCard
                key={p}
                provider={p}
                onPick={() => {
                  setProvider(p);
                  setStep("audience");
                }}
              />
            ))
          ) : (
            <p className="rounded-md bg-sunken px-3 py-2.5 text-[12.5px] text-fg-3">{t("connect.none")}</p>
          ))}

        {step === "audience" && (
          <>
            <div role="radiogroup" aria-label={t("connect.audienceStep")} className="flex flex-col gap-2">
              <ChoiceCard checked={visibility === "private"} onSelect={() => setVisibility("private")} icon={<Lock />} title={t("audience.private")} hint={t("audience.privateHint")} />
              <ChoiceCard checked={visibility === "shared"} onSelect={() => setVisibility("shared")} icon={<Users />} title={t("audience.shared")} hint={t("audience.sharedHint")}>
                <SharedAudience value={audience} onChange={setAudience} exclude={me} />
              </ChoiceCard>
            </div>
            {visibility === "shared" && !audienceOk && <p className="text-[11.5px] text-danger-fg">{t("audience.needSomeone")}</p>}
            {provider !== "imap" && (
              <Field label={t("connect.loginHint")} optional className="mt-1">
                {(p) => <TextInput {...p} type="email" value={hint} onChange={(e) => setHint(e.target.value)} placeholder="you@company.com" />}
              </Field>
            )}
            {openedInBrowser && <p className="rounded-md bg-sunken px-3 py-2 text-[12px] text-fg-2">{t("connect.openedInBrowser")}</p>}
          </>
        )}

        {step === "imap" && <ImapFields form={imap} onChange={(p) => setImap((f) => ({ ...f, ...p }))} />}

        {failure && (
          <div role="alert" className={cn("rounded-md border px-3 py-2 text-[12px]", "border-[color-mix(in_srgb,var(--danger)_28%,var(--line))] bg-[color-mix(in_srgb,var(--danger)_6%,transparent)] text-fg-2")}>
            <div className="font-medium text-danger-fg">{t("connect.failed")}</div>
            <div className="mono mt-0.5 break-words text-[11.5px]">{failure}</div>
          </div>
        )}
        {step !== "provider" && <p className="text-[11.5px] text-fg-4">{t("connect.nothingDeleted")}</p>}
      </div>
    </Dialog>
  );
}
