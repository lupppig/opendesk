import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { PasswordInput } from "@/components/auth/parts";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Field, TextInput } from "@/components/ui/input";
import { api } from "@/lib/api/endpoints";
import { errorMessage, isApiError } from "@/lib/api/client";
import { leaveActiveAccount, useSession } from "@/lib/session";

/** Deletes the signed-in account after the password (and 2FA code when on). Used in Security and onboarding. */
export function DeleteAccountDialog({ onClose }: { onClose: () => void }) {
  // Null for a moment after deletion clears the session, before the page leaves.
  const totp = useSession((s) => s.user?.totp_enabled ?? false);
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [typed, setTyped] = useState("");
  const del = useMutation({
    mutationFn: () => api.me.delete({ password: password || undefined, code: totp ? code.trim() : undefined }),
    onSuccess: async () => {
      // The deleted account's cookie is gone; another account signed in here takes over.
      const next = await leaveActiveAccount();
      qc.clear();
      if (next.status === "authed") void navigate({ to: next.workspaceId ? "/" : "/onboarding" });
      else void navigate({ to: "/login" });
    },
  });
  const message = del.error
    ? isApiError(del.error, "sole_owner") || isApiError(del.error, "conflict")
      ? "Transfer ownership or delete the workspaces you own with other members first."
      : isApiError(del.error, "invalid_credentials") || isApiError(del.error, "invalid_code")
        ? "That password or code is not right."
        : errorMessage(del.error)
    : null;
  return (
    <Dialog
      open
      onOpenChange={(o) => !o && onClose()}
      size="sm"
      title="Delete your account?"
      description="Your account, your sessions and every workspace you solely own are removed for good."
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="danger-solid"
            loading={del.isPending}
            disabled={typed !== "delete" || (totp && code.trim().length < 6)}
            onClick={() => del.mutate()}
          >
            Delete account
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-3 pb-1">
        <Field label="Password" hint="Leave empty if you only sign in with Google, Microsoft, GitHub or SSO.">
          {({ id, ...a }) => (
            <PasswordInput id={id} {...a} inputSize="md" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
          )}
        </Field>
        {totp && (
          <Field label="Two-factor code">
            {({ id }) => <TextInput id={id} className="mono" autoComplete="one-time-code" value={code} onChange={(e) => setCode(e.target.value)} placeholder="123456" />}
          </Field>
        )}
        <Field label={<>Type <span className="mono">delete</span> to confirm</>}>
          {({ id }) => <TextInput id={id} value={typed} onChange={(e) => setTyped(e.target.value)} />}
        </Field>
        {message && <p className="text-xs text-danger-fg">{message}</p>}
      </div>
    </Dialog>
  );
}
