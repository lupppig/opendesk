import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AnimatePresence, motion } from "motion/react";
import { Download, Laptop, ShieldCheck, Smartphone } from "lucide-react";
import { useState } from "react";
import { DeleteAccountDialog } from "@/components/account/DeleteAccountDialog";
import { PasswordInput, StrengthMeter } from "@/components/auth/parts";
import { PasskeysSection } from "@/components/auth/PasskeysSection";
import { SettingsPage, Section, Row, ListRow } from "@/components/layout";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { useConfirm } from "@/components/ui/confirm";
import { CopyField, copyText } from "@/components/ui/copy-button";
import { Dialog } from "@/components/ui/dialog";
import { Field, TextInput } from "@/components/ui/input";
import { SkeletonRows } from "@/components/ui/skeleton";
import { toast } from "@/components/ui/toast";
import { QrCode } from "@/components/settings/QrCode";
import { ReauthNotice } from "@/components/settings/ReauthNotice";
import { describeUserAgent } from "@/components/settings/userAgent";
import { api } from "@/lib/api/endpoints";
import { errorMessage, isApiError } from "@/lib/api/client";
import type { Session } from "@/lib/api/types";
import { relLong } from "@/lib/format";
import { q, qk } from "@/lib/queries";
import { updateUser, useSession } from "@/lib/session";

export default function Security() {
  return (
    <SettingsPage title="Security" description="Your password, passkeys, two-factor authentication and signed-in devices.">
      <PasswordSection />
      <PasskeysSection />
      <TwoFactorSection />
      <SessionsSection />
      <DeleteAccountSection />
    </SettingsPage>
  );
}

// ─── Password ──────────────────────────────────────────────────────────────

function PasswordSection() {
  const user = useSession((s) => s.user)!;
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirmPw, setConfirmPw] = useState("");
  const [error, setError] = useState<string | null>(null);
  const change = useMutation({
    mutationFn: () => api.me.password(current, next),
    onSuccess: () => {
      setCurrent("");
      setNext("");
      setConfirmPw("");
      toast.success("Password changed", { description: "Other devices were signed out." });
    },
    onError: (e) =>
      setError(isApiError(e, "invalid_credentials") ? "Your current password is not right." : errorMessage(e)),
  });
  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    // Counted in characters (code points), as the server counts them.
    const length = [...next].length;
    if (length < 12) return setError("Use at least 12 characters.");
    if (length > 128) return setError("Use at most 128 characters.");
    if (next !== confirmPw) return setError("The new passwords don't match.");
    change.mutate();
  };
  return (
    <Section title="Password" description="Changing it signs out every other device.">
      <form onSubmit={submit} className="grid gap-3 py-3.5 sm:grid-cols-3">
        <Field label="Current password">
          {({ id }) => (
            <PasswordInput id={id} inputSize="md" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} />
          )}
        </Field>
        <Field label="New password">
          {({ id }) => (
            <div className="flex flex-col gap-1.5">
              <PasswordInput id={id} inputSize="md" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} />
              <StrengthMeter password={next} userInputs={[user.name, user.email]} />
            </div>
          )}
        </Field>
        <Field label="Confirm new password">
          {({ id }) => (
            <PasswordInput id={id} inputSize="md" autoComplete="new-password" value={confirmPw} onChange={(e) => setConfirmPw(e.target.value)} />
          )}
        </Field>
        <div className="flex items-center justify-between gap-3 sm:col-span-3">
          <p className={error ? "text-xs text-danger-fg" : "text-xs text-fg-3"}>{error ?? "12 to 128 characters. Long passphrases work well."}</p>
          <Button type="submit" variant="primary" loading={change.isPending} disabled={!current || !next || !confirmPw}>
            Change password
          </Button>
        </div>
      </form>
    </Section>
  );
}

// ─── Two-factor ────────────────────────────────────────────────────────────

function TwoFactorSection() {
  const user = useSession((s) => s.user)!;
  const [setupOpen, setSetupOpen] = useState(false);
  const [disableOpen, setDisableOpen] = useState(false);
  return (
    <Section title="Two-factor authentication" description="A code from an authenticator app, asked for at every sign-in.">
      <Row
        label={
          <span className="flex items-center gap-2">
            Authenticator app
            {user.totp_enabled ? <Badge tone="ok">On</Badge> : <Badge tone="outline">Off</Badge>}
          </span>
        }
        description={
          user.totp_enabled
            ? "Required for owner actions like deleting or transferring a workspace."
            : "Works with 1Password, Google Authenticator, Authy and any TOTP app."
        }
      >
        {user.totp_enabled ? (
          <Button onClick={() => setDisableOpen(true)}>Turn off</Button>
        ) : (
          <Button variant="primary" icon={<ShieldCheck className="size-3.5" />} onClick={() => setSetupOpen(true)}>
            Set up
          </Button>
        )}
      </Row>
      {setupOpen && <SetupDialog onClose={() => setSetupOpen(false)} />}
      {disableOpen && <DisableDialog onClose={() => setDisableOpen(false)} />}
    </Section>
  );
}

const slide = {
  enter: (dir: number) => ({ x: dir * 24, opacity: 0 }),
  center: { x: 0, opacity: 1 },
  exit: (dir: number) => ({ x: dir * -24, opacity: 0 }),
};

function SetupDialog({ onClose }: { onClose: () => void }) {
  const user = useSession((s) => s.user)!;
  const confirm = useConfirm();
  const [step, setStep] = useState<0 | 1 | 2>(0);
  const [code, setCode] = useState("");
  const [codes, setCodes] = useState<string[]>([]);
  const [saved, setSaved] = useState(false);

  const setup = useQuery({ queryKey: ["totp-setup"], queryFn: api.auth.totpSetup, staleTime: Infinity, gcTime: 0, retry: false });
  const enable = useMutation({
    mutationFn: () => api.auth.totpEnable(code.replace(/\s/g, "")),
    onSuccess: (r) => {
      setCodes(r.recovery_codes);
      setStep(2);
    },
  });

  const finish = () => {
    updateUser({ ...user, totp_enabled: true });
    toast.success("Two-factor authentication is on");
    onClose();
  };
  const requestClose = () => {
    if (step === 2) {
      if (saved) return finish();
      confirm.show("Recovery codes are shown only once. Without them, losing your phone locks you out.", finish, {
        title: "Close without saving codes?",
        confirmLabel: "Close anyway",
      });
      return;
    }
    onClose();
  };

  const download = () => {
    const text = `opendesk recovery codes for ${user.email}\nEach code works once.\n\n${codes.join("\n")}\n`;
    const url = URL.createObjectURL(new Blob([text], { type: "text/plain" }));
    const a = document.createElement("a");
    a.href = url;
    a.download = "opendesk-recovery-codes.txt";
    a.click();
    URL.revokeObjectURL(url);
  };

  const secretGroups = setup.data?.secret.replace(/(.{4})/g, "$1 ").trim();

  return (
    <Dialog
      open
      onOpenChange={(o) => !o && requestClose()}
      onEscapeKeyDown={(e) => {
        if (step === 2) {
          e.preventDefault();
          requestClose();
        }
      }}
      onInteractOutside={(e) => e.preventDefault()}
      title={["Scan the code", "Enter a code", "Save your recovery codes"][step]}
      description={
        [
          "Open your authenticator app and scan this QR code.",
          "Type the 6-digit code your app shows for opendesk.",
          "Use one of these if you lose access to your app. Each works once.",
        ][step]
      }
      footer={
        <>
          <span className="mr-auto mono text-[11px] text-fg-4">Step {step + 1} of 3</span>
          {step === 0 && (
            <Button variant="primary" disabled={!setup.data} onClick={() => setStep(1)}>
              Continue
            </Button>
          )}
          {step === 1 && (
            <>
              <Button variant="ghost" onClick={() => setStep(0)}>
                Back
              </Button>
              <Button
                variant="primary"
                loading={enable.isPending}
                disabled={code.replace(/\s/g, "").length !== 6}
                onClick={() => enable.mutate()}
              >
                Verify
              </Button>
            </>
          )}
          {step === 2 && (
            <Button variant="primary" disabled={!saved} onClick={finish}>
              Done
            </Button>
          )}
        </>
      }
    >
      <div className="relative min-h-[230px] overflow-hidden">
        <AnimatePresence mode="wait" initial={false} custom={1}>
          <motion.div
            key={step}
            custom={1}
            variants={slide}
            initial="enter"
            animate="center"
            exit="exit"
            transition={{ duration: 0.18, ease: [0.22, 1, 0.36, 1] }}
          >
            {step === 0 && (
              <div className="flex flex-col items-center gap-4 py-2">
                {setup.isError ? (
                  <p className="py-16 text-xs text-danger-fg">{errorMessage(setup.error)}</p>
                ) : setup.data ? (
                  <QrCode value={setup.data.otpauth_url} label="Authenticator setup QR code" />
                ) : (
                  <div className="skeleton size-44 rounded-md" />
                )}
                <div className="w-full">
                  <p className="mb-1.5 text-xs text-fg-3">Can't scan? Enter this key instead:</p>
                  <CopyField value={secretGroups ?? "…"} />
                </div>
              </div>
            )}
            {step === 1 && (
              <form
                className="flex flex-col items-center gap-3 py-10"
                onSubmit={(e) => {
                  e.preventDefault();
                  if (code.replace(/\s/g, "").length === 6) enable.mutate();
                }}
              >
                <TextInput
                  autoFocus
                  aria-label="6-digit code"
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  maxLength={7}
                  value={code}
                  onChange={(e) => setCode(e.target.value.replace(/[^\d ]/g, ""))}
                  placeholder="000000"
                  className="mono h-11 w-[200px] text-center text-[20px] tracking-[0.3em]"
                  invalid={enable.isError}
                />
                <p className="h-4 text-xs text-danger-fg">
                  {enable.isError && (isApiError(enable.error, "validation_failed") || isApiError(enable.error, "invalid_code") ? "That code didn't work. Try the next one." : errorMessage(enable.error))}
                </p>
              </form>
            )}
            {step === 2 && (
              <div className="flex flex-col gap-4">
                <div className="grid grid-cols-2 gap-x-6 gap-y-1.5 rounded-md border border-line bg-sunken px-4 py-3">
                  {codes.map((c) => (
                    <span key={c} className="mono text-[12.5px] tabular-nums text-fg">
                      {c}
                    </span>
                  ))}
                </div>
                <div className="flex gap-2">
                  <Button
                    size="sm"
                    onClick={async () => {
                      if (await copyText(codes.join("\n"))) toast.success("Recovery codes copied");
                    }}
                  >
                    Copy all
                  </Button>
                  <Button size="sm" icon={<Download className="size-3.5" />} onClick={download}>
                    Download .txt
                  </Button>
                </div>
                <label className="flex items-center gap-2 text-[12.5px] text-fg-2">
                  <Checkbox checked={saved} onCheckedChange={setSaved} />I saved these codes somewhere safe
                </label>
              </div>
            )}
          </motion.div>
        </AnimatePresence>
      </div>
    </Dialog>
  );
}

function DisableDialog({ onClose }: { onClose: () => void }) {
  const user = useSession((s) => s.user)!;
  const [code, setCode] = useState("");
  const disable = useMutation({
    mutationFn: () => api.auth.totpDisable(code.trim()),
    onSuccess: () => {
      updateUser({ ...user, totp_enabled: false });
      toast.success("Two-factor authentication is off");
      onClose();
    },
  });
  return (
    <Dialog
      open
      onOpenChange={(o) => !o && onClose()}
      size="sm"
      title="Turn off two-factor?"
      description="Enter a code from your authenticator app, or a recovery code."
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="danger-solid" loading={disable.isPending} disabled={code.trim().length < 6} onClick={() => disable.mutate()}>
            Turn off
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-3 pb-1">
        <TextInput
          autoFocus
          aria-label="Code"
          className="mono"
          autoComplete="one-time-code"
          value={code}
          onChange={(e) => setCode(e.target.value)}
          placeholder="123456 or xxxx-xxxx-xxxx-xxxx"
        />
        {disable.isError && <ReauthNotice error={disable.error} />}
      </div>
    </Dialog>
  );
}

// ─── Sessions ──────────────────────────────────────────────────────────────

function SessionsSection() {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const { data, isLoading } = useQuery(q.sessions());

  const revoke = useMutation({
    mutationFn: async (ids: string[]) => {
      for (const id of ids) await api.me.revokeSession(id);
    },
    onMutate: async (ids) => {
      await qc.cancelQueries({ queryKey: qk.sessions });
      const prev = qc.getQueryData<Session[]>(qk.sessions);
      qc.setQueryData<Session[]>(qk.sessions, (old) => old?.filter((s) => !ids.includes(s.id)));
      return { prev };
    },
    onError: (e, _ids, ctx) => {
      if (ctx?.prev) qc.setQueryData(qk.sessions, ctx.prev);
      toast.error(e);
    },
    onSettled: () => qc.invalidateQueries({ queryKey: qk.sessions }),
  });

  const sorted = [...(data ?? [])].sort((a, b) => Number(b.current) - Number(a.current) || Date.parse(b.last_used_at) - Date.parse(a.last_used_at));
  const others = sorted.filter((s) => !s.current);

  return (
    <Section
      title="Sessions"
      description="Devices signed in to your account."
      flush
      actions={
        others.length > 0 ? (
          <Button
            size="sm"
            onClick={() =>
              confirm.show(`Sign out ${others.length} other ${others.length === 1 ? "device" : "devices"}?`, () => revoke.mutateAsync(others.map((s) => s.id)), {
                title: "Sign out other devices",
                confirmLabel: "Sign out",
              })
            }
          >
            Sign out other devices
          </Button>
        ) : undefined
      }
    >
      {isLoading ? (
        <SkeletonRows rows={3} />
      ) : (
        sorted.map((s) => {
          const ua = describeUserAgent(s.user_agent);
          return (
            <ListRow key={s.id}>
              <span className="flex size-8 shrink-0 items-center justify-center rounded-md border border-line text-fg-3">
                {ua.mobile ? <Smartphone className="size-4" /> : <Laptop className="size-4" />}
              </span>
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2 text-[13px] font-medium">
                  {ua.label}
                  {s.current && <Badge tone="solid">This device</Badge>}
                </div>
                <div className="mt-0.5 flex items-center gap-2 text-xs text-fg-3">
                  <span className="mono">{s.ip}</span>
                  <span aria-hidden>·</span>
                  <span>{s.current ? "Active now" : `Last used ${relLong(s.last_used_at)}`}</span>
                </div>
              </div>
              {!s.current && (
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() =>
                    confirm.show(`${ua.label} will be signed out.`, () => revoke.mutateAsync([s.id]), {
                      title: "Sign out this device?",
                      confirmLabel: "Sign out",
                    })
                  }
                >
                  Sign out
                </Button>
              )}
            </ListRow>
          );
        })
      )}
    </Section>
  );
}

// ─── Delete account ────────────────────────────────────────────────────────

function DeleteAccountSection() {
  const [open, setOpen] = useState(false);
  return (
    <Section title="Delete account" danger>
      <Row
        label="Delete your account"
        description="Removes your account and every workspace you are the only member of. This can't be undone."
      >
        <Button variant="danger" onClick={() => setOpen(true)}>
          Delete account
        </Button>
      </Row>
      {open && <DeleteAccountDialog onClose={() => setOpen(false)} />}
    </Section>
  );
}
