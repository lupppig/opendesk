// Pieces of connecting a mailbox: what the instance offers, provider detection,
// the app password walkthroughs and the cause-and-fix callout for a refused sign-in.

import { useQuery } from "@tanstack/react-query";
import { AlertCircle, ExternalLink, KeyRound, Settings2 } from "lucide-react";
import { Fragment, type ReactNode } from "react";
import { Trans, useTranslation } from "react-i18next";
import { Skeleton } from "@/components/ui/skeleton";
import { api } from "@/lib/api/endpoints";
import { isApiError } from "@/lib/api/client";
import type { MailDetection, MailboxCause, MailboxConnectOptions, MailboxTestLeg, MailEndpoint } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { config } from "@/lib/config";
import { tone } from "@/lib/tones";
import { ProviderLogo } from "./ProviderLogo";

const DOCS_OAUTH = "https://docs.opendesk.sh/self-hosting/configuration/#mailbox-sign-in";

export function useConnectOptions() {
  return useQuery({ queryKey: ["mailboxes", "connect-options"], queryFn: () => api.mailboxes.connectOptions(), staleTime: 5 * 60_000 });
}

/** Detection for an address, keyed by its domain. */
export function useMailDetection(email: string | null) {
  const domain = email?.split("@")[1]?.toLowerCase() ?? "";
  return useQuery({
    queryKey: ["mailboxes", "detect", domain],
    queryFn: () => api.mailboxes.detect(email!),
    enabled: !!domain,
    staleTime: 10 * 60_000,
    retry: false,
  });
}

export type Provider = "google" | "microsoft";

/**
 * Receiving over IMAP and SMTP is offered when the runtime config and the API both have
 * it on, or the workspace has it through Collab on an instance that otherwise doesn't.
 */
export function imapOn(opts?: MailboxConnectOptions): boolean {
  if (opts?.mailbox_imap && opts.instance_imap === false) return true;
  return config.mailboxImap && opts?.mailbox_imap !== false;
}

/** Syncing Google or Microsoft over IMAP is the instance's alone (MAILBOX_IMAP). */
export function syncOn(opts?: MailboxConnectOptions): boolean {
  return config.mailboxImap && opts?.mailbox_imap !== false && opts?.instance_imap !== false;
}

/** Reading a Google or Microsoft inbox directly, through the provider's API: workspaces with Collab. */
export function apiOn(opts: MailboxConnectOptions | undefined, p: Provider): boolean {
  return !!opts?.[p]?.api_sync;
}

/** Sending through opendesk is offered unless the API has it off. */
export function systemSendOn(opts?: MailboxConnectOptions): boolean {
  return opts?.system_send !== false;
}

/** Sign-in is offered when the API allows it and the deployment's runtime config does not hide it. */
export function oauthOffered(opts: MailboxConnectOptions | undefined, p: Provider): boolean {
  if (!opts?.[p].oauth) return false;
  return config.mailboxOAuth === null || config.mailboxOAuth.includes(p);
}

/**
 * The providers a forwarding mailbox may connect to send its replies: its host's own,
 * or either for a host nobody could identify, when the instance offers that sign-in.
 */
export function sendProviders(det: MailDetection | undefined, opts: MailboxConnectOptions | undefined): Provider[] {
  const candidates: Provider[] = det?.oauth ? [det.oauth] : det?.host ? [] : ["google", "microsoft"];
  return candidates.filter((p) => oauthOffered(opts, p));
}

/** A self-host without the provider's OAuth client says how to set it up. */
export function oauthUnconfigured(opts: MailboxConnectOptions | undefined, p: Provider): boolean {
  return !!opts && !opts[p].configured && config.selfHosted;
}

/** The problem body a create with verify answers when the check fails. */
export type ConnectFailure = { cause: MailboxCause | null; inbound: MailboxTestLeg; outbound: MailboxTestLeg; detail: string };
export function connectFailure(e: unknown): ConnectFailure | null {
  if (!isApiError(e, "mailbox_connection_failed")) return null;
  const b = e.body as Partial<ConnectFailure>;
  return {
    cause: b.cause ?? null,
    inbound: b.inbound ?? { ok: true, error: "" },
    outbound: b.outbound ?? { ok: true, error: "" },
    detail: e.detail,
  };
}

/** Turns bare https links in a fix into links. */
function linkify(text: string): ReactNode {
  const parts = text.split(/(https:\/\/[^\s,)]+[^\s,).])/g);
  return parts.map((p, i) =>
    p.startsWith("https://") ? (
      <a key={i} href={p} target="_blank" rel="noreferrer" className="text-fg underline decoration-line-strong underline-offset-2 hover:decoration-fg-3">
        {p.replace(/^https:\/\//, "")}
      </a>
    ) : (
      <Fragment key={i}>{p}</Fragment>
    ),
  );
}

/** Why a sign-in failed and what fixes it, with the server's own words underneath. */
export function CauseCallout({ failure }: { failure: ConnectFailure }) {
  const { t } = useTranslation("settingsChannels");
  const legs = [
    { label: t("connect.legInbound"), leg: failure.inbound },
    { label: t("connect.legOutbound"), leg: failure.outbound },
  ].filter((l) => !l.leg.ok);
  return (
    <div role="alert" className="animate-rise-in overflow-hidden rounded-lg border border-line bg-surface motion-reduce:animate-none">
      <div className="flex items-start gap-2.5 px-3.5 py-3">
        <span className={cn("mt-px flex size-5 shrink-0 items-center justify-center rounded-sm", tone.red.chip)}>
          <AlertCircle className="size-3.5" aria-hidden />
        </span>
        <div className="min-w-0 flex-1">
          <div className="text-[13px] font-medium">{failure.cause?.title ?? t("connect.failedTitle")}</div>
          <p className="mt-1 text-[12.5px] leading-5 text-fg-2">{failure.cause ? linkify(failure.cause.fix) : failure.detail}</p>
        </div>
      </div>
      {legs.length > 0 && (
        <div className="flex flex-col gap-1.5 border-t border-line-soft bg-sunken px-3.5 py-2.5">
          {legs.map((l) => (
            <div key={l.label} className="flex min-w-0 gap-2 text-[11.5px] leading-[17px]">
              <span className="w-9 shrink-0 font-medium text-fg-3">{l.label}</span>
              <span className="mono min-w-0 break-words text-fg-3">{l.leg.detail || l.leg.error}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

/** The detected provider: logo, name and where the answer came from. */
export function DetectionCard({ domain, detection, loading, failed }: { domain: string; detection?: MailDetection; loading: boolean; failed: boolean }) {
  const { t } = useTranslation("settingsChannels");
  const { data: opts } = useConnectOptions();
  if (loading)
    return (
      <div className="flex items-center gap-3 rounded-lg border border-line bg-sunken/60 px-3.5 py-3" aria-busy="true" role="status">
        <Skeleton className="size-8 rounded-md" />
        <div className="flex flex-1 flex-col gap-1.5">
          <span className="text-[12.5px] text-fg-3">{t("connect.detecting", { domain })}</span>
          <Skeleton className="h-2.5 w-40" />
        </div>
      </div>
    );
  if (failed || !detection)
    return <p className="rounded-lg border border-line bg-sunken/60 px-3.5 py-3 text-[12.5px] text-fg-3">{t("connect.detectFailed", { domain })}</p>;
  const known = detection.host !== "" && detection.host !== "other";
  return (
    <div className="flex animate-rise-in items-center gap-3 rounded-lg border border-line bg-sunken/60 px-3.5 py-3 motion-reduce:animate-none">
      <ProviderLogo host={detection.host} label={detection.label} />
      <div className="min-w-0 flex-1">
        <div className="text-[13px] font-medium">{known ? detection.label : detection.settings ? detection.domain : t("connect.unknownHost")}</div>
        <div className="text-xs text-fg-3">
          {/* Without IMAP receiving there are no server settings to enter: forwarding is the way in. */}
          {t(detection.source === "none" && !imapOn(opts) ? "connect.source.noneForward" : `connect.source.${detection.source}`, { domain: detection.domain })}
        </div>
      </div>
    </div>
  );
}

/** The fixed servers a provider uses, read-only. */
export function ServerSummary({ imap, smtp, onEdit, note }: { imap: MailEndpoint; smtp: MailEndpoint; onEdit?: () => void; note?: ReactNode }) {
  const { t } = useTranslation("settingsChannels");
  return (
    <div className="overflow-hidden rounded-lg border border-line">
      <div className="flex h-8 items-center gap-1.5 border-b border-line-soft bg-sunken px-3">
        <KeyRound className="size-3 text-fg-3" aria-hidden />
        <span className="text-[11.5px] font-medium text-fg-3">{t("connect.servers.title")}</span>
        {onEdit && (
          <button type="button" onClick={onEdit} className="ml-auto rounded-sm px-1 text-[11.5px] text-fg-3 outline-none hover:text-fg focus-visible:shadow-[0_0_0_2px_var(--focus)]">
            {t("connect.servers.edit")}
          </button>
        )}
      </div>
      {[
        { label: t("connect.servers.imap"), ep: imap },
        { label: t("connect.servers.smtp"), ep: smtp },
      ].map((r) => (
        <div key={r.label} className="flex items-center gap-3 border-b border-line-soft px-3 py-2 text-[12.5px] last:border-b-0">
          <span className="w-10 shrink-0 font-medium text-fg-3">{r.label}</span>
          <span className="mono min-w-0 truncate">{r.ep.host}</span>
          <span className="mono tabular-nums text-fg-3">:{r.ep.port}</span>
          <span className="ml-auto shrink-0 text-xs text-fg-3">{t(`connect.servers.${r.ep.security}`)}</span>
        </div>
      ))}
      {note && <div className="border-t border-line-soft px-3 py-2 text-xs leading-[18px] text-fg-3">{note}</div>}
    </div>
  );
}

export function ExternalButton({ href, children }: { href: string; children: ReactNode }) {
  return (
    <a
      href={href}
      target="_blank"
      rel="noreferrer"
      className="inline-flex h-7 items-center gap-1.5 self-start rounded-md border border-line bg-surface px-2.5 text-[12.5px] font-medium text-fg shadow-xs outline-none transition-colors hover:border-line-strong hover:bg-hover focus-visible:shadow-[0_0_0_3px_var(--focus)]"
    >
      <ExternalLink className="size-3.5" aria-hidden />
      {children}
    </a>
  );
}

export function Steps({ items }: { items: ReactNode[] }) {
  return (
    <ol className="flex flex-col gap-2">
      {items.map((it, i) => (
        <li key={i} className="flex gap-2.5 text-[12.5px] leading-5 text-fg-2 [&_b]:font-medium [&_b]:text-fg">
          <span className="mono flex size-5 shrink-0 items-center justify-center rounded-sm border border-line bg-sunken text-[10.5px] text-fg-3">{i + 1}</span>
          <span>{it}</span>
        </li>
      ))}
    </ol>
  );
}

/** A provider's app password, in three short steps with a link to where it is made. The button names the page it opens. */
export function AppPasswordGuide({ host, url }: { host: GuideHost; url: string }) {
  const { t } = useTranslation("settingsChannels");
  const b = <b />;
  return (
    <div className="flex flex-col gap-3 rounded-lg border border-line bg-sunken/50 p-3.5">
      <div className="text-[12.5px] font-medium">{t("connect.guide.title")}</div>
      <Steps items={(["s1", "s2", "s3"] as const).map((k) => <Trans key={k} t={t} i18nKey={`connect.guide.${host}.${k}`} components={{ b }} />)} />
      {url && <ExternalButton href={url}>{t(`connect.guide.${host}.open`)}</ExternalButton>}
    </div>
  );
}

export const GUIDE_HOSTS = ["yahoo", "aol", "icloud", "fastmail", "zoho", "yandex"] as const;
export type GuideHost = (typeof GUIDE_HOSTS)[number];
export const isGuideHost = (h: string): h is GuideHost => (GUIDE_HOSTS as readonly string[]).includes(h);

/** A self-host without a provider's OAuth client: the variables to set. */
export function NotConfiguredPanel({ provider }: { provider: Provider }) {
  const { t } = useTranslation("settingsChannels");
  const name = provider === "google" ? "Google" : "Microsoft";
  const vars = provider === "google" ? ["GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET"] : ["MICROSOFT_CLIENT_ID", "MICROSOFT_CLIENT_SECRET"];
  return (
    <div className="flex gap-2.5 rounded-lg border border-line bg-sunken/60 px-3.5 py-3">
      <span className={cn("mt-px flex size-5 shrink-0 items-center justify-center rounded-sm", tone.amber.chip)}>
        <Settings2 className="size-3.5" aria-hidden />
      </span>
      <div className="min-w-0 flex-1">
        <div className="text-[12.5px] font-medium">{t("connect.notConfigured.title", { provider: name })}</div>
        <p className="mt-1 text-xs leading-[18px] text-fg-3">{t("connect.notConfigured.body", { provider: name })}</p>
        <div className="mt-2 flex flex-wrap gap-1.5">
          {vars.map((v) => (
            <code key={v} className="mono rounded-sm border border-line bg-surface px-1.5 py-0.5 text-[11.5px] text-fg-2">
              {v}=
            </code>
          ))}
        </div>
        <div className="mt-2.5 flex flex-wrap items-center gap-x-3 gap-y-1">
          <ExternalButton href={DOCS_OAUTH}>{t("connect.notConfigured.docs")}</ExternalButton>
          <span className="text-xs text-fg-3">{t("connect.notConfigured.alternatives")}</span>
        </div>
      </div>
    </div>
  );
}
