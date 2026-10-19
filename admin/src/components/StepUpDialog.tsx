// Step-up (contracts/admin.md "Step-up"): turning off a user's 2FA, deleting a
// user or a workspace, and granting, changing or revoking an admin carry a
// current authenticator code (or a recovery code) of the acting admin. A wrong
// code keeps the dialog open; any other refusal shows in it too.

import { ShieldCheck } from "lucide-react";
import { useState, type ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { errorMessage, isApiError } from "@/lib/api/client";
import { CodeField, codeReady, type CodeMode } from "./CodeField";
import { Notice } from "./layout";

export function StepUpDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel,
  danger,
  children,
  ready = true,
  onConfirm,
  explain,
  size = "sm",
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  title: string;
  description?: ReactNode;
  confirmLabel: string;
  danger?: boolean;
  /** Fields above the code (a role, a typed confirmation). */
  children?: ReactNode;
  /** Whether those fields are filled in. */
  ready?: boolean;
  /** Runs the action with the code. Resolve to close; throw to keep the dialog open with the error. */
  onConfirm: (code: string) => Promise<unknown>;
  /** A message for a refusal other than the code (409 sole_owner, ...), or undefined for the API's. */
  explain?: (e: unknown) => string | undefined;
  /** Wider for a form with many fields. */
  size?: "sm" | "md" | "lg";
}) {
  const [mode, setMode] = useState<CodeMode>("totp");
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [codeError, setCodeError] = useState(false);

  const reset = () => {
    setMode("totp");
    setCode("");
    setBusy(false);
    setError(null);
    setCodeError(false);
  };

  const submit = async () => {
    if (busy || !ready || !codeReady(mode, code)) return;
    setBusy(true);
    setError(null);
    setCodeError(false);
    try {
      await onConfirm(code.trim());
      reset();
      onOpenChange(false);
    } catch (e) {
      setBusy(false);
      if (isApiError(e, "invalid_code")) {
        setCode("");
        setCodeError(true);
        setError(mode === "totp" ? "That code didn't work. Check your authenticator app and try again." : "That recovery code didn't work.");
      } else if (isApiError(e, "mfa_required")) {
        setCodeError(true);
        setError("Enter a code from your authenticator app.");
      } else {
        setError(explain?.(e) ?? errorMessage(e));
      }
    }
  };

  const hasFields = !!children;
  const close = (o: boolean) => {
    if (busy) return;
    if (!o) reset();
    onOpenChange(o);
  };

  return (
    <Dialog
      open={open}
      onOpenChange={close}
      title={title}
      description={description}
      size={size}
      footer={
        <>
          <Button variant="ghost" disabled={busy} onClick={() => close(false)}>
            Cancel
          </Button>
          <Button variant={danger ? "danger-solid" : "primary"} loading={busy} disabled={!ready || !codeReady(mode, code)} onClick={() => void submit()}>
            {confirmLabel}
          </Button>
        </>
      }
    >
      <form
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          void submit();
        }}
      >
        {children}
        <div className="flex flex-col gap-2">
          <div className="flex items-center gap-2 text-[12.5px] font-medium text-fg">
            <ShieldCheck className="size-3.5 text-tone-indigo" aria-hidden />
            {mode === "totp" ? "Your authenticator code" : "One of your recovery codes"}
          </div>
          <CodeField
            mode={mode}
            onModeChange={setMode}
            value={code}
            onChange={(v) => {
              setCode(v);
              setCodeError(false);
            }}
            disabled={busy}
            invalid={codeError}
            autoFocus={!hasFields}
            size="md"
          />
        </div>
        {error && <Notice>{error}</Notice>}
        <button type="submit" hidden aria-hidden tabIndex={-1} />
      </form>
    </Dialog>
  );
}
