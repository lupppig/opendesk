// Small pieces shared by the sign-in flows.

import { useQuery } from "@tanstack/react-query";
import { useRouter } from "@tanstack/react-router";
import { Eye, EyeOff } from "lucide-react";
import { forwardRef, useEffect, useId, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { AuthProvider, TokenResponse } from "@/lib/api/types";
import { config } from "@/lib/config";
import { desktop, setOAuthHandoff } from "@/lib/desktop";
import { newPopupId, openSignInPopup, waitForPopup } from "@/lib/oauthPopup";
import { cn } from "@/lib/cn";
import { q } from "@/lib/queries";
import { signIn } from "@/lib/session";
import { Button } from "@/components/ui/button";
import { TextInput } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { usePasswordStrength } from "@/lib/password-strength";
import { tone } from "@/lib/tones";

/** A same-origin relative path, or null. Guards every post-login redirect. */
export function safeNext(v: string | null | undefined): string | null {
  if (!v || typeof v !== "string") return null;
  if (!v.startsWith("/") || v.startsWith("//") || v.startsWith("/\\")) return null;
  try {
    const u = new URL(v, location.origin);
    if (u.origin !== location.origin) return null;
    return u.pathname + u.search + u.hash;
  } catch {
    return null;
  }
}

/** Sign in, then return the path to land on: redirect_to, then next, then "/". */
export async function completeSignIn(tok: TokenResponse, next?: string | null): Promise<string> {
  const s = await signIn(tok);
  const target = safeNext(tok.redirect_to) ?? safeNext(next);
  if (target) return target;
  return s.workspaceId ? "/" : "/onboarding";
}

/** Full-page navigation to a path the router may not know statically (invite links with tokens). */
export function go(path: string) {
  location.assign(path);
}

export function Notice({ children, tone: kind = "danger", className }: { children: ReactNode; tone?: "danger" | "ok" | "info"; className?: string }) {
  return (
    <div
      role={kind === "danger" ? "alert" : "status"}
      aria-live="polite"
      className={cn(
        "flex animate-rise-in items-start gap-2.5 rounded-md border px-3 py-2.5 text-[12.5px] leading-[18px]",
        kind === "danger" ? cn(tone.red.soft, tone.red.line, "text-fg") : kind === "ok" ? cn(tone.green.soft, tone.green.line, "text-fg") : "border-line bg-sunken text-fg-2",
        className,
      )}
    >
      <span
        aria-hidden
        className={cn("mt-[6px] size-1.5 shrink-0 rounded-full", kind === "danger" ? tone.red.solid : kind === "ok" ? tone.green.solid : "bg-fg-3")}
      />
      <div className="min-w-0">{children}</div>
    </div>
  );
}

export function OrDivider({ label = "or" }: { label?: string }) {
  return (
    <div className="my-5 flex items-center gap-3" role="separator">
      <span className="h-px flex-1 bg-line" />
      <span className="text-[11.5px] text-fg-4">{label}</span>
      <span className="h-px flex-1 bg-line" />
    </div>
  );
}

type PasswordInputProps = React.InputHTMLAttributes<HTMLInputElement> & { invalid?: boolean; inputSize?: "sm" | "md" | "lg" };

export const PasswordInput = forwardRef<HTMLInputElement, PasswordInputProps>(
  function PasswordInput({ className, inputSize = "lg", ...rest }, ref) {
    const [show, setShow] = useState(false);
    return (
      <TextInput
        ref={ref}
        type={show ? "text" : "password"}
        inputSize={inputSize}
        className={className}
        trailing={
          <button
            type="button"
            onClick={() => setShow((s) => !s)}
            aria-label={show ? "Hide password" : "Show password"}
            className="flex size-7 items-center justify-center rounded-sm text-fg-4 transition-colors hover:bg-hover hover:text-fg"
          >
            {show ? <EyeOff className="size-3.5" /> : <Eye className="size-3.5" />}
          </button>
        }
        {...rest}
      />
    );
  },
);

const strengthWords = ["Too weak", "Weak", "Fair", "Good", "Strong"] as const;
const strengthBar = [tone.red.solid, tone.red.solid, tone.amber.solid, tone.green.solid, tone.green.solid] as const;

/** Four segments that warm up as the password gets harder to guess (zxcvbn), plus its one warning. */
export function StrengthMeter({ password, userInputs }: { password: string; userInputs?: string[] }) {
  const { score, warning } = usePasswordStrength(password, userInputs);
  const length = [...password].length;
  const short = length > 0 && length < 12;
  const label = !password ? "12 characters minimum" : short ? `${12 - length} more characters` : strengthWords[score];
  const filled = !password ? 0 : Math.max(1, score);
  return (
    <div className="flex flex-col gap-1.5" aria-live="polite">
      <div className="flex items-center gap-2.5">
        <div className="flex flex-1 gap-1" aria-hidden>
          {[1, 2, 3, 4].map((i) => (
            <span key={i} className="relative h-[3px] flex-1 overflow-hidden rounded-full bg-line">
              <span
                className={cn(
                  "absolute inset-0 origin-left rounded-full transition-transform duration-300 ease-out motion-reduce:transition-none",
                  short ? tone.gray.solid : strengthBar[score],
                  i <= filled ? "scale-x-100" : "scale-x-0",
                )}
              />
            </span>
          ))}
        </div>
        <span className="mono shrink-0 whitespace-nowrap text-right text-[11px] text-fg-3">{label}</span>
      </div>
      {warning && !short && score < 3 && <p className="text-[11.5px] leading-4 text-fg-3">{warning}</p>}
    </div>
  );
}

/** Inline field error, announced. */
export function FieldError({ message }: { message?: string }) {
  if (!message) return null;
  return (
    <p role="alert" className="animate-rise-in text-[12px] leading-4 text-danger-fg">
      {message}
    </p>
  );
}

// ─── Providers ─────────────────────────────────────────────────────────────

function ProviderGlyph({ id }: { id: AuthProvider["id"] }) {
  const cls = "size-4 shrink-0";
  switch (id) {
    case "google":
      return (
        <svg viewBox="0 0 24 24" className={cls} aria-hidden fill="currentColor">
          <path d="M21.6 12.23c0-.71-.06-1.4-.18-2.06H12v3.9h5.39a4.6 4.6 0 0 1-2 3.02v2.5h3.24c1.9-1.75 2.97-4.32 2.97-7.36Z" />
          <path d="M12 22c2.7 0 4.97-.9 6.63-2.42l-3.24-2.5c-.9.6-2.04.95-3.39.95-2.6 0-4.81-1.76-5.6-4.12H3.06v2.58A10 10 0 0 0 12 22Z" opacity=".75" />
          <path d="M6.4 13.9a6 6 0 0 1 0-3.8V7.52H3.06a10 10 0 0 0 0 8.96L6.4 13.9Z" opacity=".55" />
          <path d="M12 5.98c1.47 0 2.79.5 3.83 1.5l2.87-2.87A9.62 9.62 0 0 0 12 2a10 10 0 0 0-8.94 5.52L6.4 10.1C7.19 7.74 9.4 5.98 12 5.98Z" opacity=".9" />
        </svg>
      );
    case "microsoft":
      return (
        <svg viewBox="0 0 24 24" className={cls} aria-hidden fill="currentColor">
          <rect x="3" y="3" width="8.5" height="8.5" rx="1" />
          <rect x="12.5" y="3" width="8.5" height="8.5" rx="1" opacity=".7" />
          <rect x="3" y="12.5" width="8.5" height="8.5" rx="1" opacity=".7" />
          <rect x="12.5" y="12.5" width="8.5" height="8.5" rx="1" opacity=".45" />
        </svg>
      );
    case "github":
      return (
        <svg viewBox="0 0 24 24" className={cls} aria-hidden fill="currentColor">
          <path d="M12 2a10 10 0 0 0-3.16 19.49c.5.09.68-.22.68-.48v-1.7c-2.78.6-3.37-1.34-3.37-1.34-.45-1.16-1.11-1.47-1.11-1.47-.91-.62.07-.6.07-.6 1 .07 1.53 1.03 1.53 1.03.9 1.52 2.34 1.08 2.91.83.09-.65.35-1.09.63-1.34-2.22-.25-4.55-1.11-4.55-4.94 0-1.09.39-1.98 1.03-2.68-.1-.25-.45-1.27.1-2.64 0 0 .84-.27 2.75 1.02a9.58 9.58 0 0 1 5 0c1.91-1.3 2.75-1.02 2.75-1.02.55 1.37.2 2.39.1 2.64.64.7 1.03 1.59 1.03 2.68 0 3.84-2.34 4.68-4.57 4.93.36.31.68.92.68 1.85v2.74c0 .27.18.58.69.48A10 10 0 0 0 12 2Z" />
        </svg>
      );
    default:
      return (
        <svg viewBox="0 0 24 24" className={cls} aria-hidden fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
          <rect x="4" y="10.5" width="16" height="10" rx="2.5" />
          <path d="M8 10.5V7.5a4 4 0 0 1 8 0v3" />
          <circle cx="12" cy="15.5" r="1.25" fill="currentColor" stroke="none" />
        </svg>
      );
  }
}

function oauthStartUrl(provider: AuthProvider["id"], next?: string | null, popup?: string) {
  // redirect_to is where to land after sign-in; the API always returns to /auth/callback
  // first, or to /auth/popup for a sign-in running in a popup window.
  const n = safeNext(next);
  const p = new URLSearchParams();
  if (n) p.set("redirect_to", n);
  if (popup) p.set("popup", popup);
  const q = p.size ? `?${p}` : "";
  return new URL(`${config.apiUrl}/v1/auth/oauth/${provider}/start${q}`, location.href).toString();
}

export function startOAuth(provider: AuthProvider["id"], next?: string | null) {
  if (import.meta.env.VITE_MOCK === "1") {
    location.assign(`/auth/callback?code=mock-${provider}`);
    return;
  }
  location.assign(oauthStartUrl(provider, next));
}

/**
 * The desktop app runs OAuth in the default browser (contracts/native.md): the
 * shell returns a one-time code and its PKCE verifier, and /auth/callback
 * finishes exactly as it does for the web flow.
 */
async function desktopOAuth(provider: AuthProvider["id"], next: string | null | undefined): Promise<{ code: string } | { error: string }> {
  const r = await desktop!.oauth(oauthStartUrl(provider, next));
  if ("error" in r) return r;
  setOAuthHandoff(r);
  return { code: r.code };
}

export function ProviderButtons({ next, verb = "Continue" }: { next?: string | null; verb?: string }) {
  const { data, isLoading } = useQuery(q.authConfig());
  const { t } = useTranslation("shell");
  const router = useRouter();
  const [pending, setPending] = useState<string | null>(null);
  const [waiting, setWaiting] = useState(false);
  // A sign-in running in a popup window: which provider, and whether the browser blocked it.
  const [popup, setPopup] = useState<{ provider: AuthProvider["id"]; blocked: boolean } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const flow = useRef(0);
  const stopWaiting = useRef<(() => void) | null>(null);

  useEffect(() => () => stopWaiting.current?.(), []);

  const toCallback = (code: string) => {
    const n = safeNext(next);
    router.history.push(`/auth/callback?code=${encodeURIComponent(code)}${n ? `&next=${encodeURIComponent(n)}` : ""}`);
  };

  // In a browser the provider opens in a popup window, so this page stays put. If the
  // browser blocks it, the person can allow it from the address bar and still finish,
  // or carry on in this tab.
  const startPopup = (id: AuthProvider["id"]) => {
    stopWaiting.current?.();
    const popupId = newPopupId();
    stopWaiting.current = waitForPopup(popupId, (r) => {
      stopWaiting.current = null;
      setPopup(null);
      if (r.code) {
        setPending(id);
        toCallback(r.code);
      } else setError(oauthErrorMessage(r.error));
    });
    const win = openSignInPopup(oauthStartUrl(id, next, popupId));
    setPending(null);
    setPopup({ provider: id, blocked: !win });
  };
  const cancelPopup = () => {
    stopWaiting.current?.();
    stopWaiting.current = null;
    setPopup(null);
  };
  const inThisTab = (id: AuthProvider["id"]) => {
    cancelPopup();
    setPending(id);
    startOAuth(id, next);
  };

  const start = async (id: AuthProvider["id"]) => {
    setError(null);
    if (!desktop && import.meta.env.VITE_MOCK !== "1") return startPopup(id);
    setPending(id);
    if (!desktop || import.meta.env.VITE_MOCK === "1") return startOAuth(id, next);
    const mine = ++flow.current;
    setWaiting(true);
    const r = await desktopOAuth(id, next);
    if (mine !== flow.current) return;
    setWaiting(false);
    setPending(null);
    if ("code" in r) toCallback(r.code);
    else if (r.error !== "cancelled") {
      setError(r.error === "timeout" ? t("desktop.oauthTimeout") : r.error === "browser_unavailable" ? t("desktop.oauthBrowser") : oauthErrorMessage(r.error));
    }
  };
  const cancel = () => {
    flow.current++;
    setWaiting(false);
    setPending(null);
  };

  if (isLoading)
    return (
      <div className="flex flex-col gap-2">
        <Skeleton className="h-10 rounded-md" />
        <Skeleton className="h-10 rounded-md" />
      </div>
    );
  const providers = data?.providers ?? [];
  if (!providers.length) return null;
  // The desktop app keeps sign-in compact: providers side by side, named only.
  const grid = !!desktop && providers.length > 1;
  return (
    <div className="flex flex-col gap-2">
      {error && <Notice>{error}</Notice>}
      {grid ? (
        <div className="grid grid-cols-2 gap-2">
          {providers.map((p, i) => (
            <Button
              key={p.id}
              variant="secondary"
              size="md"
              className={cn("w-full", providers.length % 2 === 1 && i === providers.length - 1 && "col-span-2")}
              loading={pending === p.id}
              icon={<ProviderGlyph id={p.id} />}
              onClick={() => void start(p.id)}
            >
              {p.id === "oidc" ? p.name || "SSO" : p.name}
            </Button>
          ))}
        </div>
      ) : providers.map((p) => (
        <Button
          key={p.id}
          variant="secondary"
          size="lg"
          className="w-full"
          loading={pending === p.id}
          icon={<ProviderGlyph id={p.id} />}
          onClick={() => void start(p.id)}
        >
          {verb} with {p.id === "oidc" ? p.name || "SSO" : p.name}
        </Button>
      ))}
      {popup && (
        <Notice tone="info">
          {popup.blocked ? t("signIn.popupBlocked", { name: providerName(popup.provider) }) : t("signIn.popupWaiting", { name: providerName(popup.provider) })}{" "}
          <button type="button" onClick={() => inThisTab(popup.provider)} className="font-medium text-fg underline decoration-line-strong underline-offset-2 hover:decoration-fg">
            {t("signIn.popupThisTab")}
          </button>
          {!popup.blocked && (
            <>
              {" · "}
              <button type="button" onClick={cancelPopup} className="font-medium text-fg underline decoration-line-strong underline-offset-2 hover:decoration-fg">
                {t("desktop.oauthCancel")}
              </button>
            </>
          )}
        </Notice>
      )}
      {waiting && (
        <Notice tone="info">
          {t("desktop.oauthWaiting")}{" "}
          <button type="button" onClick={cancel} className="font-medium text-fg underline decoration-line-strong underline-offset-2 hover:decoration-fg">
            {t("desktop.oauthCancel")}
          </button>
        </Notice>
      )}
    </div>
  );
}

export function providerName(id: string | undefined): string {
  return { google: "Google", microsoft: "Microsoft", github: "GitHub", oidc: "SSO" }[id ?? ""] ?? "your provider";
}

export const oauthErrors: Record<string, string> = {
  access_denied: "Sign-in was cancelled.",
  invalid_state: "That sign-in link expired. Start again.",
  invalid_request: "The sign-in request was not valid. Start again.",
  provider_error: "The provider returned an error. Try again.",
  provider_unavailable: "The provider is not reachable right now. Try again in a moment.",
  email_unverified: "Your email is not verified with that provider. Verify it there, then try again.",
  signup_disabled: "New sign-ups are turned off on this instance. Ask an admin for an invitation.",
  server_error: "Something went wrong on our side. Try again.",
  invalid_code: "That sign-in link expired. Start again.",
};

export function oauthErrorMessage(code: string | undefined): string | null {
  if (!code) return null;
  return oauthErrors[code] ?? "Sign-in failed. Try again.";
}

/** A labelled field used across the auth screens. */
export function AuthField({ label, children, aside }: { label: string; children: (id: string) => ReactNode; aside?: ReactNode }) {
  const id = useId();
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-center justify-between">
        <label htmlFor={id} className="text-[12.5px] font-medium text-fg">
          {label}
        </label>
        {aside}
      </div>
      {children(id)}
    </div>
  );
}
