import { Check, Forward, Server, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import type { Mailbox, MailboxTest, MailboxTestLeg } from "@/lib/api/types";
import { connectionMethod, mailboxHost, mailHostLabel, readsDirectly, sendOnly } from "@/lib/mailHost";
import { cn } from "@/lib/cn";
import { tone, type Tone } from "@/lib/tones";
import { Tooltip } from "@/components/ui/tooltip";
import { StatePill } from "@/components/settings2/bits";

export function GoogleGlyph({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" className={cn("size-4", className)} aria-hidden fill="none">
      <path
        d="M20.6 12.2c0-.6-.1-1.2-.2-1.8H12v3.4h4.8a4.1 4.1 0 0 1-1.8 2.7v2.2h2.9c1.7-1.6 2.7-3.9 2.7-6.5Z"
        fill="currentColor"
      />
      <path
        d="M12 21c2.4 0 4.5-.8 5.9-2.2L15 16.6c-.8.5-1.8.9-3 .9-2.3 0-4.3-1.6-5-3.7H4v2.3A9 9 0 0 0 12 21Z"
        fill="currentColor"
        opacity=".75"
      />
      <path d="M7 13.8a5.4 5.4 0 0 1 0-3.5V8H4a9 9 0 0 0 0 8.1l3-2.3Z" fill="currentColor" opacity=".5" />
      <path d="M12 6.6c1.3 0 2.5.5 3.4 1.3L18 5.4A9 9 0 0 0 4 8l3 2.3c.7-2.1 2.7-3.7 5-3.7Z" fill="currentColor" opacity=".9" />
    </svg>
  );
}

export function MicrosoftGlyph({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" className={cn("size-4", className)} aria-hidden>
      <rect x="3" y="3" width="8.5" height="8.5" fill="currentColor" />
      <rect x="12.5" y="3" width="8.5" height="8.5" fill="currentColor" opacity=".7" />
      <rect x="3" y="12.5" width="8.5" height="8.5" fill="currentColor" opacity=".55" />
      <rect x="12.5" y="12.5" width="8.5" height="8.5" fill="currentColor" opacity=".85" />
    </svg>
  );
}

export const kindLabel: Record<Mailbox["inbound_kind"], string> = {
  google: "Google",
  microsoft: "Microsoft 365",
  imap: "IMAP and SMTP",
  forwarding: "Forwarding",
};

/** Each way in gets its own quiet hue, so a list of mailboxes scans by kind. */
export const kindTone: Record<Mailbox["inbound_kind"], Tone> = { google: "blue", microsoft: "cyan", imap: "indigo", forwarding: "teal" };

export function MailboxGlyph({ kind, className }: { kind: Mailbox["inbound_kind"]; className?: string }) {
  if (kind === "google") return <GoogleGlyph className={className} />;
  if (kind === "microsoft") return <MicrosoftGlyph className={className} />;
  if (kind === "imap") return <Server className={cn("size-4", className)} aria-hidden />;
  return <Forward className={cn("size-4", className)} aria-hidden />;
}

export function MailboxIcon({ kind, size = "md" }: { kind: Mailbox["inbound_kind"]; size?: "sm" | "md" | "lg" }) {
  return (
    <span
      className={cn(
        "flex shrink-0 items-center justify-center rounded-md",
        size === "sm" ? "size-6 [&_svg]:size-3.5" : size === "lg" ? "size-10 rounded-lg [&_svg]:size-5" : "size-8 [&_svg]:size-4",
        tone[kindTone[kind]].chip,
      )}
      aria-hidden
    >
      <MailboxGlyph kind={kind} />
    </span>
  );
}

const statusState = { active: "active", error: "failing", disabled: "disabled" } as const;

/** An enabled mailbox that receives over IMAP where that is now off for its workspace: no mail arrives. */
export function notReceiving(m: Pick<Mailbox, "status" | "receiving">): boolean {
  return m.status !== "disabled" && m.receiving === "imap_off";
}

/** An enabled mailbox that sends through opendesk on an instance where that is off: no reply goes out. */
export function notSending(m: Pick<Mailbox, "status" | "sending">): boolean {
  return m.status !== "disabled" && m.sending === "system_off";
}

export function MailboxStatus({ mailbox }: { mailbox: Pick<Mailbox, "status" | "last_error" | "receiving" | "sending"> }) {
  const { t } = useTranslation("settingsChannels");
  if (notReceiving(mailbox))
    return (
      <Tooltip content={t("receiving.pillHint")}>
        <span className="inline-flex">
          <StatePill state="paused">{t("receiving.pill")}</StatePill>
        </span>
      </Tooltip>
    );
  if (notSending(mailbox))
    return (
      <Tooltip content={t("sendingOff.pillHint")}>
        <span className="inline-flex">
          <StatePill state="paused">{t("sendingOff.pill")}</StatePill>
        </span>
      </Tooltip>
    );
  const el = (
    <StatePill state={statusState[mailbox.status]} pulse={mailbox.status === "error"}>
      {t(`status.${mailbox.status}`)}
    </StatePill>
  );
  return mailbox.status === "error" && mailbox.last_error ? (
    <Tooltip content={mailbox.last_error}>
      <span className="inline-flex">{el}</span>
    </Tooltip>
  ) : (
    el
  );
}

/** A connection check: each side, then the cause and its fix. `only` shows one side. */
export function TestResults({ result, only }: { result: MailboxTest; only?: "inbound" | "outbound" }) {
  const { t } = useTranslation("settingsChannels");
  const line = (label: string, r: MailboxTestLeg) => (
    <div className="flex items-start gap-2.5 py-2">
      <span
        className={cn(
          "mt-px flex size-[18px] shrink-0 items-center justify-center rounded-sm animate-indicator-in motion-reduce:animate-none",
          r.ok ? tone.green.chip : tone.red.chip,
        )}
        aria-hidden
      >
        {r.ok ? <Check className="size-3" strokeWidth={2.5} /> : <X className="size-3" strokeWidth={2.5} />}
      </span>
      <div className="min-w-0 flex-1">
        <div className="text-[12.5px] font-medium">{r.ok ? t("test.works", { what: label }) : t("test.failed", { what: label })}</div>
        {!r.ok && r.error && <div className="mt-0.5 break-words text-xs text-fg-3">{r.error}</div>}
        {!r.ok && r.detail && r.detail !== r.error && (
          <div className="mono mt-0.5 break-words text-[11.5px] text-fg-4">
            {t("test.detail")}: {r.detail}
          </div>
        )}
      </div>
    </div>
  );
  const cause = result.cause;
  const failed = only ? !result[only].ok : !result.inbound.ok || !result.outbound.ok;
  return (
    <div role="status" className="overflow-hidden rounded-lg border border-line bg-sunken">
      <div className="divide-y divide-line-soft px-3">
        {only !== "outbound" && line(t("test.receiving"), result.inbound)}
        {only !== "inbound" && line(t("test.sending"), result.outbound)}
      </div>
      {cause && failed && (
        <div className="border-t border-line-soft bg-surface px-3 py-2.5">
          <div className="text-[12.5px] font-medium">{cause.title}</div>
          <p className="mt-0.5 break-words text-xs leading-[18px] text-fg-2">{cause.fix}</p>
        </div>
      )}
    </div>
  );
}

/** "Gmail · App password": the host, when known, and how the mailbox signs in. Send-only: "Forwarding · Sends with Google"; read directly: "Gmail · Reads the inbox directly through Google". */
export function connectionLabel(m: Mailbox, t: TFunction<"settingsChannels">): string {
  if (sendOnly(m)) return `${t("connect.method_label.forwarding")} · ${t("connect.method_label.sendsWith", { provider: m.outbound_kind === "google" ? "Google" : "Microsoft" })}`;
  const host = mailboxHost(m);
  const how = readsDirectly(m)
    ? t("connect.method_label.direct", { provider: m.inbound_kind === "google" ? "Google" : "Microsoft" })
    : t(`connect.method_label.${connectionMethod(m)}`);
  if (host && host !== "other") return `${mailHostLabel(host)} · ${how}`;
  return m.inbound_kind === "imap" ? `${t("kind.imap")} · ${how}` : how;
}
