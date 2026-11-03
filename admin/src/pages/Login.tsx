// Admin sign-in (contracts/admin.md "Sign-in and sessions"): email and password,
// behind Turnstile when the instance has it, then a TOTP or recovery code, every
// time. Passkeys and federated sign-in do not open the panel.

import { useNavigate, useSearch } from "@tanstack/react-router";
import { ArrowLeft } from "lucide-react";
import { useRef, useState, type ReactNode } from "react";
import { Trans, useTranslation } from "react-i18next";
import { AdminPill, EnvPill, Lockup } from "@/components/brand";
import { CodeField, codeReady, type CodeMode } from "@/components/CodeField";
import { Notice } from "@/components/layout";
import { Turnstile, turnstileEnabled, type TurnstileHandle } from "@/components/Turnstile";
import { Button } from "@/components/ui/button";
import { Field, PasswordInput, TextInput } from "@/components/ui/input";
import { errorMessage, isApiError } from "@/lib/api/client";
import { api } from "@/lib/api/endpoints";
import { i18n } from "@/i18n";
import { config } from "@/lib/config";
import { nextTarget } from "@/lib/next";

const appLink = (path: string) => `${config.appUrl}${path}`;

function A({ href, children }: { href: string; children?: ReactNode }) {
  return (
    <a href={href} target="_blank" rel="noopener noreferrer" className="font-medium text-fg underline decoration-line-strong underline-offset-2 hover:decoration-fg">
      {children}
    </a>
  );
}

/** What a refused sign-in says. */
export function loginError(e: unknown): ReactNode {
  if (!isApiError(e)) return errorMessage(e);
  switch (e.code) {
    case "invalid_credentials":
      return i18n.t("auth:loginError.invalidCredentials");
    case "not_admin":
      return i18n.t("auth:loginError.notAdmin");
    case "admin_mfa_setup_required":
      return <Trans ns="auth" i18nKey="loginError.mfaSetupRequired" components={{ a: <A href={appLink("/settings/security")} /> }} />;
    case "account_suspended":
      return i18n.t("auth:loginError.suspended");
    case "rate_limited":
      return i18n.t("auth:loginError.rateLimited");
    case "captcha_failed":
      return i18n.t("auth:loginError.captchaFailed");
    case "origin_refused":
      return i18n.t("auth:loginError.originRefused");
    default:
      return errorMessage(e);
  }
}

/** Why the last session ended, when it ended on its own. */
function endedMessage(code: string | undefined): string | null {
  if (!code) return null;
  if (code === "account_suspended") return i18n.t("auth:loginError.suspended");
  if (code === "not_admin" || code === "forbidden") return i18n.t("auth:ended.removed");
  return i18n.t("auth:ended.expired");
}

export default function Login() {
  const { t } = useTranslation("auth");
  const search = useSearch({ from: "/login" });
  const navigate = useNavigate();
  const [step, setStep] = useState<"password" | "code">("password");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [captcha, setCaptcha] = useState<string | null>(null);
  const [mfaToken, setMfaToken] = useState<string | null>(null);
  const [mode, setMode] = useState<CodeMode>("totp");
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ReactNode>(null);
  const [codeInvalid, setCodeInvalid] = useState(false);
  const turnstile = useRef<TurnstileHandle>(null);
  const ended = endedMessage(search.ended);
  const needsCaptcha = turnstileEnabled();

  const signIn = async () => {
    if (busy || !email.trim() || !password || (needsCaptcha && !captcha)) return;
    setBusy(true);
    setError(null);
    try {
      const r = await api.auth.login({ email: email.trim(), password, turnstile_token: captcha ?? undefined });
      setMfaToken(r.mfa_token);
      setStep("code");
      setPassword("");
      setCode("");
      setMode("totp");
    } catch (e) {
      setError(loginError(e));
    } finally {
      // A Turnstile token is good for one try.
      turnstile.current?.reset();
      setBusy(false);
    }
  };

  const verify = async (value = code) => {
    if (busy || !mfaToken || !codeReady(mode, value)) return;
    setBusy(true);
    setError(null);
    setCodeInvalid(false);
    try {
      await api.auth.mfa({ mfa_token: mfaToken, code: value.trim() });
      const t = nextTarget(search.next);
      await navigate({ to: t.to, search: t.search, replace: true } as never);
    } catch (e) {
      setBusy(false);
      setCode("");
      if (isApiError(e, "invalid_code")) {
        setCodeInvalid(true);
        setError(mode === "totp" ? t("code.invalidTotp") : t("code.invalidRecovery"));
      } else if (isApiError(e, "invalid_mfa_token") || (isApiError(e) && e.status === 401)) {
        restart(t("signIn.expired"));
      } else setError(loginError(e));
    }
  };

  const restart = (message: ReactNode = null) => {
    setStep("password");
    setMfaToken(null);
    setCode("");
    setCodeInvalid(false);
    setError(message);
  };

  return (
    <div className="relative flex min-h-dvh flex-col bg-frame">
      <div aria-hidden className="h-[3px] shrink-0 bg-admin-accent" />
      <main className="flex flex-1 items-start justify-center px-4 pb-12 pt-[10vh]">
        <div className="w-full max-w-[400px] animate-rise-in overflow-hidden rounded-xl border border-line bg-surface shadow-float motion-reduce:animate-none">
          <div className="flex items-center gap-2 border-b border-line-soft px-6 py-4 sm:px-8">
            <Lockup className="h-[16px] text-fg" />
            <AdminPill />
            <EnvPill label={config.envLabel} className="ml-auto" />
          </div>
          <div className="px-6 py-7 sm:px-8">
            {step === "password" ? (
              <>
                <h1 className="text-[20px] font-semibold leading-7 tracking-[-0.03em]">{t("signIn.title")}</h1>
                <p className="mt-1 text-[12.5px] leading-5 text-fg-3">{t("signIn.subtitle")}</p>
                <form
                  className="mt-6 flex flex-col gap-4"
                  onSubmit={(e) => {
                    e.preventDefault();
                    void signIn();
                  }}
                >
                  {ended && !error && <Notice kind="info">{ended}</Notice>}
                  <Field label={t("signIn.email")}>
                    {(p) => (
                      <TextInput
                        {...p}
                        type="email"
                        inputSize="lg"
                        autoComplete="username"
                        autoFocus
                        required
                        value={email}
                        onChange={(e) => setEmail(e.target.value)}
                        disabled={busy}
                      />
                    )}
                  </Field>
                  <Field label={t("signIn.password")}>
                    {(p) => (
                      <PasswordInput
                        {...p}
                        inputSize="lg"
                        autoComplete="current-password"
                        required
                        value={password}
                        onChange={(e) => setPassword(e.target.value)}
                        disabled={busy}
                      />
                    )}
                  </Field>
                  <Turnstile ref={turnstile} onToken={setCaptcha} action="admin_login" />
                  {error && <Notice>{error}</Notice>}
                  <Button type="submit" variant="primary" size="lg" className="w-full" loading={busy} disabled={!email.trim() || !password || (needsCaptcha && !captcha)}>
                    {t("signIn.continue")}
                  </Button>
                </form>
                <p className="mt-5 text-[12px] leading-[18px] text-fg-4">
                  <Trans t={t} i18nKey="signIn.noPassword" components={{ a: <A href={appLink("/forgot")} /> }} />
                </p>
              </>
            ) : (
              <>
                <button type="button" onClick={() => restart()} className="mb-4 inline-flex items-center gap-1 text-[12px] text-fg-3 hover:text-fg">
                  <ArrowLeft className="size-3.5" /> {email}
                </button>
                <h1 className="text-[20px] font-semibold leading-7 tracking-[-0.03em]">{t("mfa.title")}</h1>
                <p className="mt-1 text-[12.5px] leading-5 text-fg-3">
                  {mode === "totp" ? t("mfa.totpHint") : t("mfa.recoveryHint")}
                </p>
                <form
                  className="mt-6 flex flex-col gap-4"
                  onSubmit={(e) => {
                    e.preventDefault();
                    void verify();
                  }}
                >
                  <CodeField
                    mode={mode}
                    onModeChange={(m) => {
                      setMode(m);
                      setError(null);
                      setCodeInvalid(false);
                    }}
                    value={code}
                    onChange={(v) => {
                      setCode(v);
                      setCodeInvalid(false);
                    }}
                    onComplete={(v) => void verify(v)}
                    disabled={busy}
                    invalid={codeInvalid}
                  />
                  {error && <Notice>{error}</Notice>}
                  <Button type="submit" variant="primary" size="lg" className="w-full" loading={busy} disabled={!codeReady(mode, code)}>
                    {t("mfa.signIn")}
                  </Button>
                </form>
              </>
            )}
          </div>
        </div>
      </main>
    </div>
  );
}
