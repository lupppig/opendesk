import { zodResolver } from "@hookform/resolvers/zod";
import { useQuery } from "@tanstack/react-query";
import { getRouteApi, Link, useRouter } from "@tanstack/react-router";
import { ArrowLeft, KeyRound } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useForm } from "react-hook-form";
import { useTranslation } from "react-i18next";
import { z } from "zod";
import { AuthHeading } from "@/app/AuthLayout";
import { Button } from "@/components/ui/button";
import { TextInput } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { toast } from "@/components/ui/toast";
import { MfaStep } from "@/components/auth/MfaStep";
import { AuthField, completeSignIn, FieldError, Notice, oauthErrorMessage, OrDivider, PasswordInput, ProviderButtons, safeNext } from "@/components/auth/parts";
import { Turnstile, type TurnstileHandle } from "@/components/auth/Turnstile";
import { api } from "@/lib/api/endpoints";
import { errorMessage, isApiError } from "@/lib/api/client";
import { cancelPasskeyCeremony, isPasskeyCancel, passkeyAutofillSupported, passkeysSupported, signInWithPasskey } from "@/lib/api/passkeys";
import type { AuthConfig, LoginResponse, TokenResponse } from "@/lib/api/types";
import { q } from "@/lib/queries";
import { track } from "@/lib/analytics";
import { MAX_ACCOUNTS, switchAccount, useSession, useWorkspace } from "@/lib/session";

const route = getRouteApi("/auth/login");

const schema = z.object({
  email: z.string().trim().min(1, "Enter your email.").email("Enter a valid email address."),
  password: z.string().min(1, "Enter your password."),
});
type Values = z.infer<typeof schema>;

export default function Login() {
  const search = route.useSearch();
  const router = useRouter();
  const { t } = useTranslation("shell");
  // Add-account mode: the accounts signed in here stay signed in.
  const authed = useSession((s) => s.status === "authed");
  const accounts = useSession((s) => s.accounts);
  const active = useSession((s) => s.user);
  const workspace = useWorkspace();
  const adding = search.add === "1" && authed;
  const full = adding && accounts.length >= MAX_ACCOUNTS;
  const { data: cfg, isLoading } = useQuery(q.authConfig());
  const [token, setToken] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(oauthErrorMessage(search.error));
  const [mfa, setMfa] = useState<string | null>(null);
  const [passkeyBusy, setPasskeyBusy] = useState(false);
  const turnstile = useRef<TurnstileHandle>(null);
  const {
    register,
    handleSubmit,
    setValue,
    formState: { errors, isSubmitting },
  } = useForm<Values>({ resolver: zodResolver(schema), defaultValues: { email: search.email ?? "", password: "" } });

  const passkeysOn = !!(cfg as (AuthConfig & { passkeys?: boolean }) | undefined)?.passkeys && passkeysSupported();

  const finish = async (tok: TokenResponse, method: "password" | "passkey" = "password") => {
    track("login_succeeded", { method });
    const next = await completeSignIn(tok, search.next);
    // A breached or too short password still signs in, straight to choosing a new one.
    if (tok.change_password) {
      toast.info(t("signIn.changePassword"));
      router.history.push("/settings/security");
      return;
    }
    router.history.push(next);
  };

  const answer = async (r: LoginResponse, method: "password" | "passkey") => {
    if ("mfa_required" in r) setMfa(r.mfa_token);
    else await finish(r, method);
  };

  // Conditional UI: the browser offers saved passkeys right in the email field.
  useEffect(() => {
    if (!passkeysOn || mfa) return;
    let live = true;
    void passkeyAutofillSupported().then((ok) => {
      if (!ok || !live) return;
      signInWithPasskey({ autofill: true })
        .then((r) => (live ? answer(r, "passkey") : undefined))
        // Autofill waits quietly; only a refused sign-in after a pick is worth saying.
        .catch((e) => {
          if (live && isApiError(e)) setError(e.status === 401 ? "That passkey didn't work. Try another way to sign in." : errorMessage(e));
        });
    });
    return () => {
      live = false;
      cancelPasskeyCeremony();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [passkeysOn, mfa]);

  const passkey = async () => {
    setError(null);
    setPasskeyBusy(true);
    try {
      await answer(await signInWithPasskey(), "passkey");
    } catch (e) {
      if (!isPasskeyCancel(e)) setError(isApiError(e) && e.status === 401 ? "That passkey didn't work. Try another way to sign in." : errorMessage(e));
    } finally {
      setPasskeyBusy(false);
    }
  };

  const submit = handleSubmit(async ({ email, password }) => {
    setError(null);
    // An account already signed in here: switch to it rather than signing in twice.
    const held = adding ? accounts.find((a) => a.user.email.toLowerCase() === email.trim().toLowerCase()) : undefined;
    if (held) {
      const s = await switchAccount(held.user.id);
      if (s) return router.history.push(safeNext(search.next) ?? (s.workspaceId ? "/" : "/onboarding"));
    }
    try {
      await answer(await api.auth.login({ email: email.trim(), password, turnstile_token: token ?? undefined }), "password");
    } catch (err) {
      turnstile.current?.reset();
      if (isApiError(err, "invalid_credentials") || (isApiError(err) && err.status === 401)) setError("Wrong email or password.");
      else if (isApiError(err, "too_many_accounts")) setError(t("accounts.limit", { count: MAX_ACCOUNTS }));
      else if (isApiError(err, "captcha_failed")) setError("We couldn't verify you're human. Try the check again.");
      else setError(errorMessage(err));
    }
  });

  if (mfa)
    return (
      <MfaStep
        mfaToken={mfa}
        onDone={(t) => finish(t)}
        onBack={() => {
          setMfa(null);
          setValue("password", "");
        }}
      />
    );

  const passwordLogin = cfg?.password_login ?? true;
  const hasAlternatives = !!cfg?.providers.length || passkeysOn;

  const back = adding && (
    <p className="mt-7 text-center text-[12.5px] text-fg-3">
      <Link to="/" className="inline-flex items-center gap-1.5 font-medium text-fg-2 transition-colors hover:text-fg">
        <ArrowLeft className="size-3.5" />
        {workspace ? t("accounts.back", { name: workspace.name }) : t("accounts.backPlain")}
      </Link>
    </p>
  );

  if (full)
    return (
      <div>
        <AuthHeading title={t("accounts.addTitle")} />
        <Notice tone="info">{t("accounts.limit", { count: MAX_ACCOUNTS })}</Notice>
        {back}
      </div>
    );

  return (
    <div>
      {adding ? (
        <AuthHeading
          title={t("accounts.addTitle")}
          subtitle={
            accounts.length > 1
              ? t("accounts.addSubtitleMany", { count: accounts.length })
              : t("accounts.addSubtitle", { email: active?.email ?? "" })
          }
        />
      ) : (
        <AuthHeading title="Log in to opendesk" subtitle="Welcome back. Pick up where your team left off." />
      )}
      {isLoading ? (
        <div className="flex flex-col gap-3">
          <Skeleton className="h-10 rounded-md" />
          <Skeleton className="h-10 rounded-md" />
          <Skeleton className="h-10 rounded-md" />
        </div>
      ) : (
        <>
          <div className="flex flex-col gap-2">
            {passkeysOn && (
              <Button variant="secondary" size="lg" className="w-full" icon={<KeyRound className="size-4" />} loading={passkeyBusy} onClick={() => void passkey()}>
                Sign in with a passkey
              </Button>
            )}
            <ProviderButtons next={search.next} />
          </div>
          {passwordLogin && hasAlternatives && <OrDivider />}
          {error && <Notice className="mb-4">{error}</Notice>}
          {passwordLogin && (
            <form className="flex flex-col gap-4" onSubmit={submit} noValidate>
              <AuthField label="Email">
                {(id) => (
                  <div className="flex flex-col gap-1.5">
                    <TextInput
                      id={id}
                      inputSize="lg"
                      type="email"
                      autoComplete={passkeysOn ? "username webauthn" : "email"}
                      autoFocus
                      placeholder="you@company.com"
                      invalid={!!errors.email}
                      {...register("email")}
                    />
                    <FieldError message={errors.email?.message} />
                  </div>
                )}
              </AuthField>
              <AuthField
                label="Password"
                aside={
                  <Link to="/forgot" className="text-xs text-fg-3 transition-colors hover:text-fg">
                    Forgot password?
                  </Link>
                }
              >
                {(id) => (
                  <div className="flex flex-col gap-1.5">
                    <PasswordInput id={id} autoComplete="current-password" invalid={!!errors.password} {...register("password")} />
                    <FieldError message={errors.password?.message} />
                  </div>
                )}
              </AuthField>
              <Turnstile ref={turnstile} onToken={setToken} action="login" />
              <Button type="submit" variant="primary" size="lg" className="mt-1 w-full" loading={isSubmitting}>
                Log in
              </Button>
            </form>
          )}
          {cfg?.signup_enabled !== false && (
            <p className="mt-7 text-center text-[12.5px] text-fg-3">
              New to opendesk?{" "}
              <Link to="/register" search={{ next: search.next, add: adding ? "1" : undefined }} className="font-medium text-fg hover:underline">
                Create an account
              </Link>
            </p>
          )}
          {back}
        </>
      )}
    </div>
  );
}
