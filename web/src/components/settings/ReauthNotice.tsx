import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "@tanstack/react-router";
import { Button } from "@/components/ui/button";
import { errorMessage, isApiError } from "@/lib/api/client";
import { signOut, useSession } from "@/lib/session";

/**
 * Owner-level operations need a recent sign-in (12 hours) that passed the second factor when
 * the account has one: `403 mfa_required` (2FA on, this session did not pass it) or
 * `403 reauth_required` (the session is older). Signing in again answers both.
 */
export function isReauthError(e: unknown) {
  return isApiError(e, "mfa_required") || isApiError(e, "reauth_required");
}

export function ReauthNotice({ error }: { error: unknown }) {
  const { t: ui } = useTranslation("ui");
  const navigate = useNavigate();
  const box = useRef<HTMLDivElement>(null);
  const reauth = isReauthError(error);
  // Shown after a click at the foot of a long form or dialog: bring it into view.
  useEffect(() => {
    if (!reauth) return;
    const reduce = window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
    box.current?.scrollIntoView?.({ block: "nearest", behavior: reduce ? "auto" : "smooth" });
  }, [reauth, error]);
  if (!error) return null;
  if (!reauth) return <p className="text-xs text-danger-fg">{errorMessage(error)}</p>;
  const mfa = isApiError(error, "mfa_required");
  return (
    <div ref={box} role="alert" className="flex items-center justify-between gap-3 rounded-md border border-line bg-sunken px-3 py-2.5">
      <p className="text-[12.5px] text-fg-2">{mfa ? ui("reauthMfa") : ui("reauthRecent")}</p>
      <Button
        size="xs"
        variant="secondary"
        onClick={async () => {
          const next = location.pathname + location.search;
          const email = useSession.getState().user?.email;
          // Only this account signs out; others in this browser stay signed in.
          await signOut();
          void navigate({ to: "/login", search: { next, email, add: "1" } });
        }}
      >
        {ui("signInAgain")}
      </Button>
    </div>
  );
}
