import { useTranslation } from "react-i18next";
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
import { ReauthNotice, isReauthError } from "@/components/settings/ReauthNotice";
import { describeUserAgent } from "@/components/settings/userAgent";
import { api } from "@/lib/api/endpoints";
import { errorMessage, isApiError } from "@/lib/api/client";
import type { Session } from "@/lib/api/types";
import { relLong } from "@/lib/format";
import { q, qk } from "@/lib/queries";
import { updateUser, useSession } from "@/lib/session";

export default function Security() {
  const { t: a } = useTranslation("auth");
  return (
    <SettingsPage title={a("security")} description={a("yourPasswordPasskeysTwofactorAuthenticationAndSignedinDevices")}>
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
  const { t: a } = useTranslation("auth");
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
      toast.success(a("passwordChanged"), { description: a("otherDevicesWereSignedOut") });
    },
    onError: (e) =>
      setError(isApiError(e, "invalid_credentials") ? a("yourCurrentPasswordIsNotRight") : errorMessage(e)),
  });
  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    // Counted in characters (code points), as the server counts them.
    const length = [...next].length;
    if (length < 12) return setError(a("useAtLeast12Characters"));
    if (length > 128) return setError(a("useAtMost128Characters"));
    if (next !== confirmPw) return setError(a("theNewPasswordsDontMatch"));
    change.mutate();
  };
  return (
    <Section title={a("password")} description={a("changingItSignsOutEveryOtherDevice")}>
      <form onSubmit={submit} className="grid gap-3 py-3.5 sm:grid-cols-3">
        <Field label={a("currentPassword")}>
          {({ id }) => (
            <PasswordInput id={id} inputSize="md" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} />
          )}
        </Field>
        <Field label={a("newPassword")}>
          {({ id }) => (
            <div className="flex flex-col gap-1.5">
              <PasswordInput id={id} inputSize="md" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} />
              <StrengthMeter password={next} userInputs={[user.name, user.email]} />
            </div>
          )}
        </Field>
        <Field label={a("confirmNewPassword")}>
          {({ id }) => (
            <PasswordInput id={id} inputSize="md" autoComplete="new-password" value={confirmPw} onChange={(e) => setConfirmPw(e.target.value)} />
          )}
        </Field>
        <div className="flex items-center justify-between gap-3 sm:col-span-3">
          <p className={error ? "text-xs text-danger-fg" : "text-xs text-fg-3"}>{error ?? a("12To128CharactersLongPassphrasesWorkWell")}</p>
          <Button type="submit" variant="primary" loading={change.isPending} disabled={!current || !next || !confirmPw}>
            {a("changePassword")}
          </Button>
        </div>
      </form>
    </Section>
  );
}

// ─── Two-factor ────────────────────────────────────────────────────────────

function TwoFactorSection() {
  const { t: a } = useTranslation("auth");
  const user = useSession((s) => s.user)!;
  const [setupOpen, setSetupOpen] = useState(false);
  const [disableOpen, setDisableOpen] = useState(false);
  return (
    <Section title={a("twofactorAuthentication")} description={a("aCodeFromAnAuthenticatorAppAskedFor")}>
      <Row
        label={
          <span className="flex items-center gap-2">
             {a("authenticatorApp")} {user.totp_enabled ? <Badge tone="ok">{a("on")}</Badge> : <Badge tone="outline">{a("off")}</Badge>}
          </span>
        }
        description={
          user.totp_enabled
            ? a("requiredForOwnerActionsLikeDeletingOrTransferring")
            : a("worksWith1PasswordGoogleAuthenticatorAuthyAndAny")
        }
      >
        {user.totp_enabled ? (
          <Button onClick={() => setDisableOpen(true)}>{a("turnOff")}</Button>
        ) : (
          <Button variant="primary" icon={<ShieldCheck className="size-3.5" />} onClick={() => setSetupOpen(true)}>
            {a("setUp")}
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
  const { t: a } = useTranslation("auth");
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
    toast.success(a("twofactorAuthenticationIsOn"));
    onClose();
  };
  const requestClose = () => {
    if (step === 2) {
      if (saved) return finish();
      confirm.show(a("recoveryCodesAreShownOnlyOnceWithoutThem"), finish, {
        title: a("closeWithoutSavingCodes"),
        confirmLabel: a("closeAnyway"),
      });
      return;
    }
    onClose();
  };

  const download = () => {
    const text = a("recoveryDownload", { email: user.email, codes: codes.join("\n") });
    const url = URL.createObjectURL(new Blob([text], { type: "text/plain" }));
    const link = document.createElement("a");
    link.href = url;
    link.download = "opendesk-recovery-codes.txt";
    link.click();
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
      title={[a("scanTheCode"), a("enterACode"), a("saveYourRecoveryCodes")][step]}
      description={
        [
          a("openYourAuthenticatorAppAndScanThisQR"),
          a("typeThe6digitCodeYourAppShowsFor"),
          a("useOneOfTheseIfYouLoseAccess"),
        ][step]
      }
      footer={
        <>
          <span className="mr-auto mono text-[11px] text-fg-4">{a("stepOfThree", { step: step + 1 })}</span>
          {step === 0 && (
            <Button variant="primary" disabled={!setup.data} onClick={() => setStep(1)}>
              {a("continue")}
            </Button>
          )}
          {step === 1 && (
            <>
              <Button variant="ghost" onClick={() => setStep(0)}>
                {a("back")}
              </Button>
              <Button
                variant="primary"
                loading={enable.isPending}
                disabled={code.replace(/\s/g, "").length !== 6}
                onClick={() => enable.mutate()}
              >
                {a("verify")}
              </Button>
            </>
          )}
          {step === 2 && (
            <Button variant="primary" disabled={!saved} onClick={finish}>
              {a("done")}
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
                {isReauthError(setup.error) ? (
                  <div className="w-full py-12">
                    <ReauthNotice error={setup.error} />
                  </div>
                ) : setup.isError ? (
                  <p className="py-16 text-xs text-danger-fg">{errorMessage(setup.error)}</p>
                ) : setup.data ? (
                  <QrCode value={setup.data.otpauth_url} label={a("authenticatorSetupQRCode")} />
                ) : (
                  <div className="skeleton size-44 rounded-md" />
                )}
                <div className="w-full">
                  <p className="mb-1.5 text-xs text-fg-3">{a("cantScanEnterThisKeyInstead")}</p>
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
                  aria-label={a("6digitCode")}
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
                  {enable.isError && (isApiError(enable.error, "validation_failed") || isApiError(enable.error, "invalid_code") ? a("thatCodeDidntWorkTryTheNextOne") : errorMessage(enable.error))}
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
                      if (await copyText(codes.join("\n"))) toast.success(a("recoveryCodesCopied"));
                    }}
                  >
                    {a("copyAll")}
                  </Button>
                  <Button size="sm" icon={<Download className="size-3.5" />} onClick={download}>
                    {a("downloadTxt")}
                  </Button>
                </div>
                <label className="flex items-center gap-2 text-[12.5px] text-fg-2">
                  <Checkbox checked={saved} onCheckedChange={setSaved} />{a("iSavedTheseCodesSomewhereSafe")} </label>
              </div>
            )}
          </motion.div>
        </AnimatePresence>
      </div>
    </Dialog>
  );
}

function DisableDialog({ onClose }: { onClose: () => void }) {
  const { t: a } = useTranslation("auth");
  const user = useSession((s) => s.user)!;
  const [code, setCode] = useState("");
  const disable = useMutation({
    mutationFn: () => api.auth.totpDisable(code.trim()),
    onSuccess: () => {
      updateUser({ ...user, totp_enabled: false });
      toast.success(a("twofactorAuthenticationIsOff"));
      onClose();
    },
  });
  return (
    <Dialog
      open
      onOpenChange={(o) => !o && onClose()}
      size="sm"
      title={a("turnOffTwofactor")}
      description={a("enterACodeFromYourAuthenticatorAppOr")}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            {a("cancel")}
          </Button>
          <Button variant="danger-solid" loading={disable.isPending} disabled={code.trim().length < 6} onClick={() => disable.mutate()}>
            {a("turnOff")}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-3 pb-1">
        <TextInput
          autoFocus
          aria-label={a("code")}
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
  const { t: a } = useTranslation("auth");
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
      title={a("sessions")}
      description={a("devicesSignedInToYourAccount")}
      flush
      actions={
        others.length > 0 ? (
          <Button
            size="sm"
            onClick={() =>
              confirm.show(a("signOutOthers", { count: others.length }), () => revoke.mutateAsync(others.map((s) => s.id)), {
                title: a("signOutOtherDevices"),
                confirmLabel: a("signOut"),
              })
            }
          >
            {a("signOutOtherDevices")}
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
                  {s.current && <Badge tone="solid">{a("thisDevice")}</Badge>}
                </div>
                <div className="mt-0.5 flex items-center gap-2 text-xs text-fg-3">
                  <span className="mono">{s.ip}</span>
                  <span aria-hidden>·</span>
                  <span>{s.current ? a("activeNow") : a("lastUsed", { date: relLong(s.last_used_at) })}</span>
                </div>
              </div>
              {!s.current && (
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() =>
                    confirm.show(a("deviceSignOutBody", { name: ua.label }), () => revoke.mutateAsync([s.id]), {
                      title: a("signOutThisDevice"),
                      confirmLabel: a("signOut"),
                    })
                  }
                >
                  {a("signOut")}
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
  const { t: a } = useTranslation("auth");
  const [open, setOpen] = useState(false);
  return (
    <Section title={a("deleteAccount")} danger>
      <Row
        label={a("deleteYourAccount2")}
        description={a("removesYourAccountAndEveryWorkspaceYouAre")}
      >
        <Button variant="danger" onClick={() => setOpen(true)}>
          {a("deleteAccount")}
        </Button>
      </Row>
      {open && <DeleteAccountDialog onClose={() => setOpen(false)} />}
    </Section>
  );
}
