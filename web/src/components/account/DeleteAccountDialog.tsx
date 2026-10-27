import { Trans, useTranslation } from "react-i18next";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { PasswordInput } from "@/components/auth/parts";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { ReauthNotice } from "@/components/settings/ReauthNotice";
import { Field, TextInput } from "@/components/ui/input";
import { api } from "@/lib/api/endpoints";
import { errorMessage, isApiError } from "@/lib/api/client";
import { leaveActiveAccount, runBeforeSignOut, useSession } from "@/lib/session";

/** Deletes the signed-in account after the password (and 2FA code when on). Used in Security and onboarding. */
export function DeleteAccountDialog({ onClose }: { onClose: () => void }) {
  const { t: a } = useTranslation("auth");
  // Null for a moment after deletion clears the session, before the page leaves.
  const totp = useSession((s) => s.user?.totp_enabled ?? false);
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [typed, setTyped] = useState("");
  const del = useMutation({
    mutationFn: async () => {
      // A reply on hold goes out while the account can still send it.
      await runBeforeSignOut();
      return api.me.delete({ password: password || undefined, code: totp ? code.trim() : undefined });
    },
    onSuccess: async () => {
      // The deleted account's cookie is gone; another account signed in here takes over.
      const next = await leaveActiveAccount();
      qc.clear();
      if (next.status === "authed") void navigate({ to: next.workspaceId ? "/" : "/onboarding" });
      else void navigate({ to: "/login" });
    },
  });
  // Without a password, a recent sign-in stands in for it: the notice says so.
  const reauth = isApiError(del.error, "reauth_required");
  const message = del.error && !reauth
    ? isApiError(del.error, "sole_owner") || isApiError(del.error, "conflict")
      ? a("transferOwnershipOrDeleteTheWorkspacesYouOwn")
      : isApiError(del.error, "invalid_credentials") || isApiError(del.error, "invalid_code")
        ? a("thatPasswordOrCodeIsNotRight")
        : errorMessage(del.error)
    : null;
  return (
    <Dialog
      open
      onOpenChange={(o) => !o && onClose()}
      size="sm"
      title={a("deleteYourAccount")}
      description={a("yourAccountYourSessionsAndEveryWorkspaceYou")}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            {a("cancel")}
          </Button>
          <Button
            variant="danger-solid"
            loading={del.isPending}
            disabled={typed !== "delete" || (totp && code.trim().length < 6)}
            onClick={() => del.mutate()}
          >
            {a("deleteAccount")}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-3 pb-1">
        <Field label={a("password")} hint={a("leaveEmptyIfYouOnlySignInWith")}>
          {({ id, ...a }) => (
            <PasswordInput id={id} {...a} inputSize="md" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
          )}
        </Field>
        {totp && (
          <Field label={a("twofactorCode")}>
            {({ id }) => <TextInput id={id} className="mono" autoComplete="one-time-code" value={code} onChange={(e) => setCode(e.target.value)} placeholder="123456" />}
          </Field>
        )}
        <Field label={<Trans ns="auth" i18nKey="deleteConfirmation" components={{ token: <span className="mono" /> }} />}>
          {({ id }) => <TextInput id={id} value={typed} onChange={(e) => setTyped(e.target.value)} />}
        </Field>
        {message && <p className="text-xs text-danger-fg">{message}</p>}
        {reauth && <ReauthNotice error={del.error} />}
      </div>
    </Dialog>
  );
}
