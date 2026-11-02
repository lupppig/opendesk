// A call in the ticket's timeline (contracts/phone.md "Timeline"): one entry per call,
// kept current as the call rings, is answered and ends, then gets its recording, the
// team's comments and the provider's AI summary.

import { ChevronRight, ExternalLink, PhoneIncoming, PhoneMissed, PhoneOutgoing, Play, Voicemail } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { PROVIDER_NAMES } from "@/components/contacts/PhoneNumber";
import { Badge, TagChip } from "@/components/ui/badge";
import { Tooltip } from "@/components/ui/tooltip";
import type { Activity, CallData } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { fullDate, relShort } from "@/lib/format";
import { tone, type Tone } from "@/lib/tones";

/** Missed reasons with their own words; another provider's reason reads as it came. */
const REASONS = [
  "out_of_opening_hours",
  "short_abandoned",
  "abandoned_in_ivr",
  "abandoned_in_classic",
  "no_available_agent",
  "agents_did_not_answer",
  "no_answer",
  "busy",
  "cancelled",
  "abandon",
  "failed",
] as const;
type Reason = (typeof REASONS)[number];
const knownReason = (r: string): r is Reason => (REASONS as readonly string[]).includes(r);

/** Only https links from the provider are opened. */
function https(v: unknown): string | null {
  if (typeof v !== "string" || !v) return null;
  try {
    const u = new URL(v);
    return u.protocol === "https:" ? u.href : null;
  } catch {
    return null;
  }
}

/** Seconds as 3:05, or 1:02:05 past an hour. */
export function clock(seconds: number): string {
  const s = Math.max(0, Math.round(seconds));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const pad = (n: number) => String(n).padStart(2, "0");
  return h > 0 ? `${h}:${pad(m)}:${pad(s % 60)}` : `${m}:${pad(s % 60)}`;
}

/** How long the customer and the team spoke: from the answer to the end, else the provider's duration. */
export function talkSeconds(d: Pick<CallData, "answered_at" | "ended_at" | "duration">): number {
  if (d.answered_at && d.ended_at) return Math.max(0, (Date.parse(d.ended_at) - Date.parse(d.answered_at)) / 1000);
  return d.duration ?? 0;
}

/** The link that plays a recording or voicemail now: the direct one while it lasts, else the provider's page. */
export function playLink(direct: string, asset: string, expiresAt: string | null, now = Date.now()): string | null {
  const d = https(direct);
  if (d && expiresAt && Date.parse(expiresAt) > now + 60_000) return d;
  return https(asset);
}

export function CallEntry({ a }: { a: Activity }) {
  const { t } = useTranslation("inbox");
  const d = a.data as Partial<CallData>;
  const [transcript, setTranscript] = useState(false);
  const outbound = d.direction === "outbound";
  const who = d.agent?.name || d.agent_name || "";
  const caller = d.caller_name || d.number || t("call.withheld");
  const live = d.stage !== "ended";

  let hue: Tone = "green";
  let Icon = outbound ? PhoneOutgoing : PhoneIncoming;
  let status: string;
  if (live) {
    hue = "blue";
    status = d.stage === "answered" ? t("call.inProgress") : t("call.ringing");
  } else if (d.outcome === "voicemail") {
    hue = "amber";
    Icon = Voicemail;
    status = t("call.voicemail");
  } else if (d.outcome === "missed") {
    hue = "red";
    Icon = PhoneMissed;
    status = outbound ? t("call.noAnswer") : t("call.missed");
  } else {
    status = t("call.answered");
  }

  const title = outbound
    ? who
      ? t("call.outboundBy", { who, caller })
      : t("call.outbound", { caller })
    : d.outcome === "answered" && who
      ? t("call.inboundAnsweredBy", { who, caller })
      : t("call.inbound", { caller });
  const reason = d.missed_reason ? (knownReason(d.missed_reason) ? t(`call.reason.${d.missed_reason}`) : d.missed_reason.replace(/_/g, " ")) : "";
  const talk = d.outcome === "answered" ? talkSeconds({ answered_at: d.answered_at ?? null, ended_at: d.ended_at ?? null, duration: d.duration ?? 0 }) : 0;
  const recording = d.recording ? playLink(d.recording_url ?? "", d.asset_url ?? "", d.links_expire_at ?? null) : null;
  const voicemail = d.voicemail ? playLink(d.voicemail_url ?? "", d.asset_url ?? "", d.links_expire_at ?? null) : null;
  // The provider's lasting page for the call's media is also where it opens there.
  const open = https(d.asset_url);
  const comments = Array.isArray(d.comments) ? d.comments : [];
  const tags = Array.isArray(d.tags) ? d.tags : [];

  return (
    <div className="py-1 pl-[18px]" data-testid="call-entry">
      <div className="rounded-lg border border-line bg-surface px-3 py-2.5">
        <div className="flex items-start gap-2.5">
          <span className={cn("mt-px flex size-6 shrink-0 items-center justify-center rounded-md [&_svg]:size-3.5", tone[hue].chip)} aria-hidden>
            <Icon />
          </span>
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
              <span className="min-w-0 truncate text-[12.5px] font-medium text-fg" data-ph-mask>
                {title}
              </span>
              <Badge tone={hue} dot={live}>
                {status}
              </Badge>
              {talk > 0 && <span className="mono text-[11px] tabular-nums text-fg-3">{clock(talk)}</span>}
              {d.outside_hours && <Badge tone="amber">{t("call.outsideHours")}</Badge>}
            </div>
            <div className="mt-0.5 flex flex-wrap items-center gap-x-1.5 text-[11.5px] text-fg-3">
              {reason && <span>{reason}</span>}
              {reason && (d.line || d.number) && <span aria-hidden>·</span>}
              {d.line && <span>{t("call.line", { line: d.line })}</span>}
              {d.line && d.number && d.caller_name && <span aria-hidden>·</span>}
              {d.number && d.caller_name && (
                <span className="mono" data-ph-mask>
                  {d.number}
                </span>
              )}
            </div>
            {(recording || voicemail || open) && (
              <div className="mt-2 flex flex-wrap gap-1.5">
                {recording && (
                  <a href={recording} target="_blank" rel="noopener noreferrer" className={linkCls}>
                    <Play className="size-3" aria-hidden />
                    {t("call.playRecording")}
                  </a>
                )}
                {voicemail && (
                  <a href={voicemail} target="_blank" rel="noopener noreferrer" className={linkCls}>
                    <Voicemail className="size-3" aria-hidden />
                    {t("call.playVoicemail")}
                  </a>
                )}
                {open && (
                  <a href={open} target="_blank" rel="noopener noreferrer" className={linkCls}>
                    <ExternalLink className="size-3" aria-hidden />
                    {t("call.openIn", { provider: PROVIDER_NAMES[d.provider ?? ""] ?? d.provider })}
                  </a>
                )}
              </div>
            )}
            {d.summary && (
              <p className="mt-2 rounded-md bg-sunken px-2.5 py-2 text-[12px] leading-[18px] text-fg-2" data-ph-mask>
                <span className="label-caps mr-1.5">{t("call.summary")}</span>
                {d.summary}
              </p>
            )}
            {comments.length > 0 && (
              <ul className="mt-2 flex flex-col gap-1">
                {comments.map((c) => (
                  <li key={c.id || c.text} className="text-[12px] text-fg-2" data-ph-mask>
                    {c.author && <span className="font-medium text-fg">{c.author}: </span>}
                    {c.text}
                  </li>
                ))}
              </ul>
            )}
            {tags.length > 0 && (
              <div className="mt-2 flex flex-wrap gap-1">
                {tags.map((name) => (
                  <TagChip key={name} name={name} />
                ))}
              </div>
            )}
            {d.transcript && (
              <div className="mt-2">
                <button
                  type="button"
                  aria-expanded={transcript}
                  onClick={() => setTranscript((v) => !v)}
                  className="inline-flex items-center gap-1 rounded text-[11.5px] font-medium text-fg-3 outline-none hover:text-fg focus-visible:shadow-[0_0_0_2px_var(--focus)]"
                >
                  <ChevronRight className={cn("size-3 transition-transform motion-reduce:transition-none", transcript && "rotate-90")} aria-hidden />
                  {transcript ? t("call.hideTranscript") : t("call.showTranscript")}
                </button>
                {transcript && (
                  <p className="mt-1.5 max-h-[280px] overflow-y-auto whitespace-pre-wrap rounded-md bg-sunken px-2.5 py-2 text-[12px] leading-[18px] text-fg-2" data-ph-mask>
                    {d.transcript}
                  </p>
                )}
              </div>
            )}
          </div>
          <Tooltip content={fullDate(d.started_at || a.created_at)}>
            <time dateTime={d.started_at || a.created_at} className="mono shrink-0 text-[10.5px] text-fg-4">
              {relShort(d.started_at || a.created_at)}
            </time>
          </Tooltip>
        </div>
      </div>
    </div>
  );
}

const linkCls =
  "inline-flex h-6 items-center gap-1 rounded-md border border-line bg-surface px-2 text-[11.5px] font-medium text-fg-2 outline-none transition-colors hover:border-line-strong hover:text-fg focus-visible:shadow-[0_0_0_2px_var(--focus)]";
