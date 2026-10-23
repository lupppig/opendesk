// A mail account's own settings as they sync with its provider (contracts/collab.md
// "Account settings"): the line that says how its signature and From name sync, the Out of
// office section (Gmail's vacation responder, Outlook's automatic replies), and the Away
// pill the sidebar shows while the automatic reply is on.

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArrowDownToLine, ArrowLeftRight, Info } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Row, Section } from "@/components/layout";
import { RichEditor } from "@/components/editor/RichEditor";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { DateField, TimeField } from "@/components/ui/date-picker";
import { TextInput } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast";
import { Tooltip } from "@/components/ui/tooltip";
import { api } from "@/lib/api/endpoints";
import { errorMessage } from "@/lib/api/client";
import type { MailAccount, MailIdentitySync } from "@/lib/api/types";
import { storeAccount } from "@/lib/collab/hooks";
import { outOfOfficeBody, outOfOfficeForm, outOfOfficeProblem, type OutOfOfficeForm } from "@/lib/collab/outOfOffice";
import { cn } from "@/lib/cn";
import { relLong } from "@/lib/format";

const syncIcon: Record<MailIdentitySync, ReactNode> = {
  two_way: <ArrowLeftRight />,
  read_only: <ArrowDownToLine />,
  pull: <ArrowDownToLine />,
  none: <Info />,
};

/** One plain line on how a setting syncs, with what can be done about it. */
export function SyncLine({ sync, children, actions, className }: { sync: MailIdentitySync; children: ReactNode; actions?: ReactNode; className?: string }) {
  return (
    <div className={cn("flex flex-wrap items-center gap-x-3 gap-y-1.5 text-[12px] leading-[18px] text-fg-3", className)}>
      <span className="flex min-w-0 flex-1 items-start gap-1.5">
        <span className={cn("mt-px flex shrink-0 [&_svg]:size-3.5", sync === "two_way" ? "text-tone-green" : "text-fg-4")}>{syncIcon[sync]}</span>
        <span className="min-w-0">{children}</span>
      </span>
      {actions && <span className="flex shrink-0 items-center gap-2">{actions}</span>}
    </div>
  );
}

/** How the signature syncs, as the line above its editor. */
export function SignatureSync({ account, manage, onReconnect }: { account: MailAccount; manage: boolean; onReconnect: () => void }) {
  const { t } = useTranslation("collab");
  const qc = useQueryClient();
  const pull = useMutation({
    mutationFn: () => api.collab.accounts.pullSignature(account.id),
    onSuccess: (a) => {
      storeAccount(qc, a);
      toast.success(t("detail.signaturePulled"));
    },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const sync = account.signature_sync;
  const text =
    account.provider === "google" ? t(`detail.signatureSync.${sync === "two_way" ? "two_way" : "read_only"}`) : account.provider === "microsoft" ? t("detail.signatureSync.microsoft") : t("detail.signatureSync.imap");
  return (
    <SyncLine
      sync={sync}
      className="border-b border-line-soft px-3 py-2"
      actions={
        account.provider === "google" && (
          <>
            {account.signature_synced_at && <span className="text-fg-4">{t("detail.syncedAt", { when: relLong(account.signature_synced_at) })}</span>}
            {manage && sync === "read_only" && (
              <Button size="sm" variant="ghost" onClick={onReconnect}>
                {t("detail.reconnect")}
              </Button>
            )}
            {manage && (
              <Button size="sm" loading={pull.isPending} onClick={() => pull.mutate()}>
                {t("detail.getFromGmail")}
              </Button>
            )}
          </>
        )
      }
    >
      {text}
    </SyncLine>
  );
}

/** The short hint under the From name: where the name comes from. */
export function FromNameHint({ account }: { account: MailAccount }) {
  const { t } = useTranslation("collab");
  const text =
    account.provider === "google"
      ? t(`detail.fromNameSync.${account.display_name_sync === "two_way" ? "two_way" : "read_only"}`)
      : account.provider === "microsoft" && account.display_name_sync === "read_only"
        ? t("detail.fromNameSync.microsoft")
        : t("detail.fromNameSync.none");
  return <span className="mt-0.5 block text-[11.5px] text-fg-4">{text}</span>;
}

/** The sidebar's pill while an account's automatic reply is on. */
export function AwayPill() {
  const { t } = useTranslation("collab");
  return (
    <Tooltip content={t("nav.awayTip")}>
      <span className="ml-auto flex shrink-0">
        <Badge tone="amber" className="h-4 rounded-[4px] px-1 text-[10px]">
          {t("nav.away")}
        </Badge>
      </span>
    </Tooltip>
  );
}

const sameForm = (a: OutOfOfficeForm, b: OutOfOfficeForm) => JSON.stringify(a) === JSON.stringify(b);

/**
 * Whether a rich editor has the focus: an editor's change without it is the editor writing
 * the saved HTML its own way (links, empty paragraphs), not a person editing.
 */
export const editorFocused = () => !!document.activeElement?.closest("[contenteditable='true']");

/** The Out of office section: the automatic reply, saved on its own through the provider. */
/** A consumer Gmail address (not Google Workspace): Gmail refuses the domain-only reply for it. */
function personalGmail(email: string): boolean {
  const domain = email.split("@").pop()?.toLowerCase() ?? "";
  return domain === "gmail.com" || domain === "googlemail.com";
}

export function OutOfOfficeSection({ account, manage, onReconnect }: { account: MailAccount; manage: boolean; onReconnect: () => void }) {
  const { t } = useTranslation("collab");
  const qc = useQueryClient();
  const sync = account.out_of_office_sync;
  const provider = account.provider;
  // The message as the editor writes it, once it has rewritten the saved one (not a change).
  const [shownMessage, setShownMessage] = useState<string | null>(null);
  const base = useMemo(() => {
    const b = outOfOfficeForm(account.out_of_office);
    return shownMessage === null ? b : { ...b, message_html: shownMessage };
  }, [account.out_of_office, shownMessage]);
  const lastSaved = useRef(account.out_of_office);
  useEffect(() => {
    if (lastSaved.current === account.out_of_office) return;
    lastSaved.current = account.out_of_office;
    setShownMessage(null);
  }, [account.out_of_office]);
  const [form, setForm] = useState<OutOfOfficeForm>(base);
  const [failure, setFailure] = useState<string | null>(null);
  // A change from the provider replaces the form while nobody is editing it.
  const last = useRef(base);
  useEffect(() => {
    setForm((f) => (sameForm(f, last.current) ? base : f));
    last.current = base;
  }, [base]);

  const save = useMutation({
    mutationFn: () => api.collab.accounts.setOutOfOffice(account.id, outOfOfficeBody(form)),
    onSuccess: (a) => {
      storeAccount(qc, a);
      const next = outOfOfficeForm(a.out_of_office);
      last.current = next;
      setForm(next);
      setFailure(null);
      toast.success(t("detail.ooo.saved"));
    },
    onError: (e) => setFailure(errorMessage(e)),
  });

  if (sync === "none" || provider === "imap") {
    return (
      <Section title={t("detail.ooo.title")} description={t("detail.ooo.hint")}>
        <div className="py-3.5">
          <SyncLine
            sync="none"
            actions={
              provider === "microsoft" && manage ? (
                <Button size="sm" onClick={onReconnect}>
                  {t("detail.reconnect")}
                </Button>
              ) : undefined
            }
          >
            {provider === "microsoft" ? t("detail.ooo.none.microsoft") : t("detail.ooo.none.imap")}
          </SyncLine>
        </div>
      </Section>
    );
  }

  const editable = manage && sync === "two_way";
  const set = (p: Partial<OutOfOfficeForm>) => setForm((f) => ({ ...f, ...p }));
  const dirty = !sameForm(form, base);
  const problem = outOfOfficeProblem(form, provider);
  const name = provider === "google" ? "google" : "microsoft";

  return (
    <Section title={t("detail.ooo.title")} description={t("detail.ooo.hint")}>
      <Row label={t("detail.ooo.enabled")} description={t(`detail.ooo.sync.${sync}.${name}`)} htmlFor="ooo-on">
        {sync === "read_only" && manage && (
          <Button size="sm" variant="ghost" onClick={onReconnect}>
            {t("detail.reconnect")}
          </Button>
        )}
        <Switch id="ooo-on" checked={form.enabled} disabled={!editable} onCheckedChange={(enabled) => set({ enabled })} aria-label={t("detail.ooo.enabled")} />
      </Row>
      <Row label={t("detail.ooo.start")} description={provider === "microsoft" ? t("detail.ooo.needBothHint") : t("detail.ooo.startHint")}>
        <DayTime day={form.startDay} time={form.startTime} disabled={!editable} label={t("detail.ooo.start")} max={form.endDay || undefined} onChange={(startDay, startTime) => set({ startDay, startTime })} />
      </Row>
      <Row label={t("detail.ooo.end")} description={provider === "microsoft" ? undefined : t("detail.ooo.endHint")}>
        <DayTime day={form.endDay} time={form.endTime} disabled={!editable} label={t("detail.ooo.end")} min={form.startDay || undefined} onChange={(endDay, endTime) => set({ endDay, endTime })} />
      </Row>
      {provider === "google" && (
        <Row label={t("detail.ooo.subject")} htmlFor="ooo-subject">
          <TextInput id="ooo-subject" className="w-[300px]" maxLength={200} value={form.subject} disabled={!editable} placeholder={t("detail.ooo.subjectPlaceholder")} onChange={(e) => set({ subject: e.target.value })} />
        </Row>
      )}
      <Row label={t("detail.ooo.message")} stack>
        <div className="overflow-hidden rounded-md border border-line">
          <RichEditor
            value={form.message_html}
            onChange={(message_html) => {
              if (!editorFocused()) setShownMessage(message_html);
              set({ message_html });
            }}
            editable={editable}
            toolbar={editable ? "full" : "none"}
            placeholder={t("detail.ooo.messagePlaceholder")}
            ariaLabel={t("detail.ooo.message")}
            contentClassName="min-h-[96px] text-[13px]"
          />
        </div>
      </Row>
      <Row label={t("detail.ooo.contactsOnly")} description={provider === "microsoft" ? t("detail.ooo.contactsOnlyHintMicrosoft") : undefined} htmlFor="ooo-contacts">
        <Switch id="ooo-contacts" checked={form.contacts_only} disabled={!editable || (provider === "microsoft" && form.internal_only)} onCheckedChange={(contacts_only) => set({ contacts_only })} aria-label={t("detail.ooo.contactsOnly")} />
      </Row>
      {/* A personal Gmail address has no organization to keep the reply inside. */}
      {!(provider === "google" && personalGmail(account.email)) && (
      <Row label={t("detail.ooo.internalOnly")} description={provider === "google" ? t("detail.ooo.internalOnlyHintGoogle") : t("detail.ooo.internalOnlyHintMicrosoft")} htmlFor="ooo-internal">
        <Switch
          id="ooo-internal"
          checked={form.internal_only}
          disabled={!editable}
          onCheckedChange={(internal_only) => set(provider === "microsoft" && internal_only ? { internal_only, contacts_only: false } : { internal_only })}
          aria-label={t("detail.ooo.internalOnly")}
        />
      </Row>
      )}
      {editable && (
        <div className="flex flex-wrap items-center justify-end gap-3 border-t border-line-soft py-3">
          {(failure || (dirty && problem)) && (
            <p role="alert" className="mr-auto min-w-0 break-words text-[12px] text-danger-fg">
              {failure ?? (problem ? t(problem) : "")}
            </p>
          )}
          <Button variant="primary" size="sm" disabled={!dirty || !!problem} loading={save.isPending} onClick={() => save.mutate()}>
            {t("detail.ooo.save")}
          </Button>
        </div>
      )}
    </Section>
  );
}

/** A day (the app's date picker) and a time, either cleared together. */
function DayTime({
  day,
  time,
  onChange,
  disabled,
  label,
  min,
  max,
}: {
  day: string;
  time: string;
  onChange: (day: string, time: string) => void;
  disabled: boolean;
  label: string;
  min?: string;
  max?: string;
}) {
  const { t } = useTranslation("collab");
  return (
    <div className={cn("flex items-center gap-1.5", disabled && "pointer-events-none opacity-60")} aria-disabled={disabled || undefined}>
      <DateField size="sm" className="w-[150px]" value={day} min={min} max={max} clearable placeholder={t("detail.ooo.noDate")} aria-label={label} onChange={(d) => onChange(d, time)} />
      {day && <TimeField size="sm" value={time} aria-label={t("detail.ooo.timeOf", { label })} onChange={(v) => onChange(day, v)} />}
    </div>
  );
}
