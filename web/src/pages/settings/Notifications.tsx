import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCollab } from "@/lib/collab/hooks";
import { Link } from "@tanstack/react-router";
import { motion, useReducedMotion } from "motion/react";
import { BellOff, BellRing, CalendarClock, Check, ChevronDown, Clock, Gauge, Laptop, MailCheck, MailX, Moon, Play, Send, Smartphone, Zap } from "lucide-react";
import { Fragment, useEffect, useId, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Row, Section, SettingsPage } from "@/components/layout";
import { IconTile } from "@/components/settings2/bits";
import { Badge, Dot } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Menu, MenuContent, MenuItem, MenuTrigger } from "@/components/ui/menu";
import { Select } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast";
import { Tooltip } from "@/components/ui/tooltip";
import { api } from "@/lib/api/endpoints";
import { isApiError } from "@/lib/api/client";
import type {
  EmailCadence,
  NotificationChannel,
  NotificationPreferences,
  NotificationPreferencesPatch,
  NotificationTypePrefs,
} from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { isDesktop } from "@/lib/desktop";
import { clock, dayLabel, fullDate } from "@/lib/format";
import { springs } from "@/lib/motion";
import {
  applyPreferencesPatch,
  browserTimeZone,
  type GapWords,
  groupTypes,
  isPaused,
  notificationHolds,
  type NotificationHold,
  pacingCopy,
  MAX_PAUSE_MS,
  pauseEnd,
  type PauseChoice,
} from "@/lib/notificationPrefs";
import { q, qk } from "@/lib/queries";
import { useWorkspace } from "@/lib/session";
import { tone, type Tone } from "@/lib/tones";
import { useNow } from "@/lib/useNow";

const MUTATION_KEY = ["notification-preferences", "update"] as const;

/** PATCH with only what changed, applied to the cache first; a failure puts it back. */
function useUpdatePreferences() {
  const qc = useQueryClient();
  const { t } = useTranslation("settings");
  return useMutation({
    mutationKey: MUTATION_KEY,
    mutationFn: (patch: NotificationPreferencesPatch) => api.notifications.updatePreferences(patch),
    onMutate: async (patch) => {
      await qc.cancelQueries({ queryKey: qk.notificationPreferences });
      const prev = qc.getQueryData<NotificationPreferences>(qk.notificationPreferences);
      if (prev) qc.setQueryData(qk.notificationPreferences, applyPreferencesPatch(prev, patch));
      return { prev };
    },
    onError: (e, _patch, ctx) => {
      if (ctx?.prev) qc.setQueryData(qk.notificationPreferences, ctx.prev);
      toast.error(e, t("notifications.saveFailed"));
      void qc.invalidateQueries({ queryKey: qk.notificationPreferences });
    },
    onSuccess: (res) => {
      // A later change still in flight wins; its own answer lands after it.
      if (qc.isMutating({ mutationKey: MUTATION_KEY }) <= 1) qc.setQueryData(qk.notificationPreferences, res);
    },
  });
}

export default function Notifications() {
  const { t } = useTranslation("settings");
  const { data: prefs } = useQuery(q.notificationPreferences());
  const update = useUpdatePreferences();
  const change = (patch: NotificationPreferencesPatch) => update.mutate(patch);

  return (
    <SettingsPage title={t("notifications.title")} description={t("notifications.description")}>
      {!prefs ? (
        <LoadingSections />
      ) : (
        <>
          <HoldNotices prefs={prefs} onChange={change} />
          <EmailSection prefs={prefs} onChange={change} />
          <PauseSection prefs={prefs} onChange={change} />
          <TypesSection prefs={prefs} onChange={change} />
        </>
      )}
    </SettingsPage>
  );
}

// ─── Turning things back on ────────────────────────────────────────────────

/**
 * What stops notifications here, each with its one-click undo: an email's unsubscribe link
 * (restores the choices from before), a pause, or the email schedule set to off.
 */
function HoldNotices({ prefs, onChange }: { prefs: NotificationPreferences; onChange: (p: NotificationPreferencesPatch) => void }) {
  const { t } = useTranslation("settings");
  const qc = useQueryClient();
  const workspace = useWorkspace();
  const now = useNow(!!prefs.paused_until);
  const resubscribe = useMutation({
    mutationFn: api.notifications.resubscribe,
    onSuccess: (res) => {
      qc.setQueryData(qk.notificationPreferences, res);
      toast.success(t("notifications.holds.resubscribed"));
    },
    onError: (e) => toast.error(e, t("notifications.saveFailed")),
  });
  const holds = notificationHolds(prefs, now || Date.now());
  if (!holds.length) return null;
  const name = workspace?.name ?? t("notifications.types.thisWorkspace");
  const row = (h: NotificationHold) => {
    switch (h.kind) {
      case "unsubscribed":
        return {
          icon: <MailX />,
          title: t("notifications.holds.unsubscribed", { workspace: name }),
          hint: t("notifications.holds.unsubscribedHint"),
          action: (
            <Button variant="primary" icon={<MailCheck />} loading={resubscribe.isPending} onClick={() => resubscribe.mutate()}>
              {t("notifications.holds.turnEmailOn")}
            </Button>
          ),
        };
      case "paused":
        return {
          icon: <Moon />,
          title: t("notifications.holds.paused", { when: fullDate(h.until) }),
          hint: t("notifications.holds.pausedHint"),
          action: (
            <Button variant="primary" icon={<Play />} onClick={() => onChange({ paused_until: "" })}>
              {t("notifications.holds.resume")}
            </Button>
          ),
        };
      case "emailOff":
        return {
          icon: <BellOff />,
          title: t("notifications.holds.emailOff"),
          hint: t("notifications.holds.emailOffHint"),
          action: (
            <Button variant="primary" icon={<MailCheck />} onClick={() => onChange({ email_cadence: "instant" })}>
              {t("notifications.holds.turnEmailOn")}
            </Button>
          ),
        };
    }
  };
  return (
    <div className="-mb-3 flex flex-col gap-2" role="status">
      {holds.map((h) => {
        const r = row(h);
        return (
          <div key={h.kind} className={cn("flex flex-wrap items-center gap-x-4 gap-y-3 rounded-lg border px-4 py-3.5", tone.amber.soft, tone.amber.line)}>
            <span className={cn("flex size-8 shrink-0 items-center justify-center rounded-md bg-surface [&_svg]:size-4", tone.amber.text)}>{r.icon}</span>
            <div className="min-w-[220px] flex-1">
              <div className="text-[13px] font-medium text-fg">{r.title}</div>
              <div className="mt-0.5 text-[12.5px] leading-[18px] text-fg-2">{r.hint}</div>
            </div>
            {r.action}
          </div>
        );
      })}
    </div>
  );
}

/**
 * OS notifications need permission, from the system in the desktop app and from the
 * browser elsewhere: say when it is blocked, and ask when it was never asked.
 */
function DesktopPermission() {
  const { t } = useTranslation("settings");
  const supported = typeof window !== "undefined" && "Notification" in window;
  const [perm, setPerm] = useState<NotificationPermission | null>(supported ? Notification.permission : null);
  useEffect(() => {
    if (!supported) return;
    const sync = () => setPerm(Notification.permission);
    window.addEventListener("focus", sync);
    return () => window.removeEventListener("focus", sync);
  }, [supported]);
  if (!supported || perm === "granted" || perm === null) return null;
  if (perm === "denied") {
    return (
      <Note icon={<BellOff />} strong>
        {t(isDesktop ? "notifications.desktop.blocked" : "notifications.desktop.blockedBrowser")}
      </Note>
    );
  }
  return (
    <Note icon={<BellRing />} strong>
      {t(isDesktop ? "notifications.desktop.ask" : "notifications.desktop.askBrowser")}{" "}
      <button
        type="button"
        className="font-medium text-fg underline decoration-line-strong underline-offset-2 hover:decoration-fg"
        onClick={() => void Notification.requestPermission().then(setPerm)}
      >
        {t("notifications.desktop.allow")}
      </button>
    </Note>
  );
}

function LoadingSections() {
  return (
    <div className="flex flex-col gap-9" aria-busy="true">
      {[120, 56, 320].map((h, i) => (
        <div key={i}>
          <Skeleton className="mb-3 h-3 w-40" />
          <Skeleton className="w-full rounded-lg" style={{ height: h }} />
        </div>
      ))}
    </div>
  );
}

// ─── Email schedule ────────────────────────────────────────────────────────

/** "hour", "2 hours", "10 minutes". */
function useGapWords() {
  const { t } = useTranslation("settings");
  return (g: GapWords) => (g.key === "hour" ? t("notifications.gap.hour") : t(`notifications.gap.${g.key}`, { count: g.count }));
}

const CADENCES: EmailCadence[] = ["instant", "hourly", "daily", "off"];
const cadenceIcon: Record<EmailCadence, ReactNode> = { instant: <Zap />, hourly: <Clock />, daily: <CalendarClock />, off: <BellOff /> };
const cadenceTone: Record<EmailCadence, Tone> = { instant: "blue", hourly: "teal", daily: "indigo", off: "gray" };

/** "9:00 AM" / "09:00" for an hour of the day, in the reader's locale. */
const hourLabel = (h: number) => clock(new Date(2000, 0, 1, h).toISOString());
const HOURS = Array.from({ length: 24 }, (_, h) => ({ value: String(h), label: hourLabel(h) }));

function EmailSection({ prefs, onChange }: { prefs: NotificationPreferences; onChange: (p: NotificationPreferencesPatch) => void }) {
  const { t } = useTranslation("settings");
  const test = useMutation({
    mutationFn: api.notifications.testEmail,
    onSuccess: () => toast.success(t("notifications.email.testSent"), { description: t("notifications.email.testSentHint", { email: prefs.email }) }),
    onError: (e) => toast.error(isApiError(e, "email_unverified") ? t("notifications.email.unverified") : e),
  });
  const copy = pacingCopy(prefs.pacing, prefs.delay_minutes);
  const gap = useGapWords();
  const instantHint =
    copy.instant.key === "instantPaced" && copy.instant.gap
      ? t("notifications.cadence.instantPaced", { count: copy.instant.delay, gap: gap(copy.instant.gap) })
      : t("notifications.cadence.instantHint", { count: copy.instant.delay });
  const pick = (c: EmailCadence) => {
    if (c === prefs.email_cadence) return;
    onChange(c === "daily" ? { email_cadence: c, timezone: browserTimeZone() } : { email_cadence: c });
  };

  return (
    <Section title={t("notifications.email.title")} description={t("notifications.email.description")} flush>
      <CadenceChoice value={prefs.email_cadence} instantHint={instantHint} onChange={pick} />
      {prefs.email_cadence === "daily" && (
        <div className="flex flex-wrap items-center justify-between gap-3 border-t border-line-soft px-4 py-3 animate-rise-in motion-reduce:animate-none">
          <div className="min-w-0">
            <div className="text-[12.5px] font-medium text-fg">{t("notifications.email.digestAt")}</div>
            <div className="mt-0.5 text-[12px] leading-[18px] text-fg-3">{t("notifications.email.digestAtHint")}</div>
          </div>
          <div className="flex items-center gap-2.5">
            <Select
              aria-label={t("notifications.email.digestAt")}
              size="sm"
              className="w-[124px]"
              value={String(prefs.digest_hour)}
              onChange={(v) => onChange({ digest_hour: Number(v), timezone: browserTimeZone() })}
              options={HOURS}
            />
            <span className="mono text-[11.5px] text-fg-3" title={t("notifications.email.timezone")}>
              {prefs.timezone.replace(/_/g, " ")}
            </span>
          </div>
        </div>
      )}
      {prefs.email_cadence !== "off" && (
        <p className="flex items-start gap-2 border-t border-line-soft px-4 py-2.5 text-[12px] leading-[18px] text-fg-3">
          <Gauge className="mt-[2px] size-3.5 shrink-0 text-fg-4" aria-hidden />
          {t("notifications.email.limits", { perHour: copy.limits.perHour, perDay: copy.limits.perDay })}
        </p>
      )}
      <div className="flex flex-wrap items-center justify-between gap-3 border-t border-line-soft bg-sunken/60 px-4 py-3">
        <div className="min-w-0">
          <div className="flex min-w-0 items-center gap-2 text-[12.5px] text-fg-2">
            <span className="shrink-0">{t("notifications.email.sentTo")}</span>
            <span className="mono truncate text-fg">{prefs.email}</span>
            {prefs.email_verified ? <Badge tone="ok">{t("notifications.email.confirmed")}</Badge> : <Badge tone="warn">{t("notifications.email.notConfirmed")}</Badge>}
          </div>
          {!prefs.email_verified && (
            <p className="mt-1 text-[12px] leading-[18px] text-fg-3">
              {t("notifications.email.unverifiedHint")}{" "}
              <Link to="/settings/profile" className="font-medium text-fg underline-offset-4 hover:underline">
                {t("notifications.email.confirmLink")}
              </Link>
            </p>
          )}
        </div>
        <Button
          icon={<Send />}
          loading={test.isPending}
          disabled={!prefs.email_verified}
          onClick={() => test.mutate()}
        >
          {t("notifications.email.test")}
        </Button>
      </div>
    </Section>
  );
}

/** Four radio cards; a sliding outline marks the chosen one. Arrow keys move between them. */
function CadenceChoice({ value, instantHint, onChange }: { value: EmailCadence; instantHint: string; onChange: (c: EmailCadence) => void }) {
  const { t } = useTranslation("settings");
  const id = useId();
  const reduce = useReducedMotion();
  const move = (dir: 1 | -1) => onChange(CADENCES[(CADENCES.indexOf(value) + dir + CADENCES.length) % CADENCES.length]);
  const hint = (c: EmailCadence) => (c === "instant" ? instantHint : t(`notifications.cadence.${c}Hint`));
  return (
    <div role="radiogroup" aria-label={t("notifications.email.title")} className="grid gap-1 p-1 sm:grid-cols-2">
      {CADENCES.map((c) => {
        const on = c === value;
        return (
          <button
            key={c}
            type="button"
            role="radio"
            aria-checked={on}
            tabIndex={on ? 0 : -1}
            onClick={() => onChange(c)}
            onKeyDown={(e) => {
              if (e.key === "ArrowRight" || e.key === "ArrowDown") {
                e.preventDefault();
                move(1);
              } else if (e.key === "ArrowLeft" || e.key === "ArrowUp") {
                e.preventDefault();
                move(-1);
              }
            }}
            className={cn(
              "group relative flex items-start gap-3 rounded-md p-3 text-left outline-none transition-colors duration-150",
              "focus-visible:ring-2 focus-visible:ring-focus/30",
              !on && "hover:bg-sunken",
            )}
          >
            {on && (
              <motion.span
                layoutId={`${id}-cadence`}
                aria-hidden
                transition={reduce ? { duration: 0 } : springs.glide}
                className="pointer-events-none absolute inset-0 rounded-md bg-sunken shadow-[inset_0_0_0_1px_var(--line)]"
              />
            )}
            <IconTile tone={on ? cadenceTone[c] : "gray"} className="relative">
              {cadenceIcon[c]}
            </IconTile>
            <span className="relative min-w-0 flex-1">
              <span className="block text-[13px] font-medium text-fg">{t(`notifications.cadence.${c}`)}</span>
              <span className="mt-0.5 block text-[12px] leading-[17px] text-fg-3">{hint(c)}</span>
            </span>
            <span
              aria-hidden
              className={cn(
                "relative mt-0.5 flex size-3.5 shrink-0 items-center justify-center rounded-full border transition-colors duration-150",
                on ? "border-fg bg-fg" : "border-line-strong bg-surface",
              )}
            >
              <span className={cn("size-1.5 rounded-full bg-surface transition-transform duration-150 motion-reduce:transition-none", on ? "scale-100" : "scale-0")} />
            </span>
          </button>
        );
      })}
    </div>
  );
}

// ─── Pause ─────────────────────────────────────────────────────────────────

function PauseSection({ prefs, onChange }: { prefs: NotificationPreferences; onChange: (p: NotificationPreferencesPatch) => void }) {
  const { t } = useTranslation("settings");
  // Ticks only while paused, so the page notices when the pause ends.
  const now = useNow(!!prefs.paused_until);
  const paused = isPaused(prefs.paused_until, now || Date.now());
  const pause = (c: PauseChoice) => {
    const end = Math.min(pauseEnd(c).getTime(), Date.now() + MAX_PAUSE_MS);
    onChange({ paused_until: new Date(end).toISOString() });
  };

  return (
    <Section title={t("notifications.pause.title")} description={t("notifications.pause.description")}>
      <Row
        label={
          paused ? (
            <span className="flex items-center gap-2">
              <Dot tone="warn" />
              {t("notifications.pause.until", { when: fullDate(prefs.paused_until) })}
            </span>
          ) : (
            t("notifications.pause.off")
          )
        }
        description={paused ? t("notifications.pause.onHint") : t("notifications.pause.offHint")}
      >
        <PauseMenu label={paused ? t("notifications.pause.change") : t("notifications.pause.pause")} onPick={pause} primary={!paused} />
        {paused && (
          <Button variant="primary" icon={<Play />} onClick={() => onChange({ paused_until: "" })}>
            {t("notifications.pause.resume")}
          </Button>
        )}
      </Row>
    </Section>
  );
}

function PauseMenu({ label, onPick, primary }: { label: string; onPick: (c: PauseChoice) => void; primary?: boolean }) {
  const { t } = useTranslation("settings");
  const items: { c: PauseChoice; label: string; hint: string }[] = [
    { c: "hour", label: t("notifications.pause.hour"), hint: clock(pauseEnd("hour").toISOString()) },
    { c: "tomorrow", label: t("notifications.pause.tomorrow"), hint: clock(pauseEnd("tomorrow").toISOString()) },
    { c: "week", label: t("notifications.pause.week"), hint: dayLabel(pauseEnd("week").toISOString()) },
  ];
  return (
    <Menu>
      <MenuTrigger asChild>
        <Button variant={primary ? "secondary" : "ghost"} icon={<Moon />} iconRight={<ChevronDown className="size-3 text-fg-3" />}>
          {label}
        </Button>
      </MenuTrigger>
      <MenuContent className="min-w-[240px]">
        {items.map((i) => (
          <MenuItem key={i.c} onSelect={() => onPick(i.c)}>
            <span className="flex items-center justify-between gap-6">
              <span>{i.label}</span>
              <span className="text-[11.5px] text-fg-4">{i.hint}</span>
            </span>
          </MenuItem>
        ))}
      </MenuContent>
    </Menu>
  );
}

// ─── Types by channel ──────────────────────────────────────────────────────

const CHANNELS: NotificationChannel[] = ["email", "push", "desktop"];
const GRID = "grid grid-cols-[minmax(0,1fr)_repeat(4,64px)] items-center gap-x-1 sm:grid-cols-[minmax(0,1fr)_repeat(4,84px)]";

function TypesSection({ prefs, onChange }: { prefs: NotificationPreferences; onChange: (p: NotificationPreferencesPatch) => void }) {
  const { t } = useTranslation("settings");
  const workspace = useWorkspace();
  const collabOn = useCollab().enabled;
  // Collab's mail notifications are listed only while the workspace has Collab.
  const groups = groupTypes(prefs.types).filter((g) => g.group !== "mail" || collabOn);
  const emailOff = prefs.email_cadence === "off";
  const column = (c: NotificationChannel) => t(`notifications.channels.${c}`);
  const gap = useGapWords();
  const urgentHint = t("notifications.types.urgentHint", { gap: gap(pacingCopy(prefs.pacing, prefs.delay_minutes).urgent) });
  const set = (row: NotificationTypePrefs, c: NotificationChannel, v: boolean) => onChange({ types: [{ type: row.type, [c]: v }] });

  return (
    <Section
      title={t("notifications.types.title")}
      description={t("notifications.types.description", { workspace: workspace?.name ?? t("notifications.types.thisWorkspace") })}
      flush
    >
      <div role="table" aria-label={t("notifications.types.title")}>
        <div role="row" className={cn(GRID, "h-9 border-b border-line px-4")}>
          <span role="columnheader" className="label-caps">
            {t("notifications.types.type")}
          </span>
          <span role="columnheader" className="text-center text-[11.5px] font-medium text-fg-3">
            {t("notifications.channels.app")}
          </span>
          {CHANNELS.map((c) => (
            <span key={c} role="columnheader" className="flex items-center justify-center gap-1 text-center text-[11.5px] font-medium text-fg-3">
              {column(c)}
              {c === "email" && emailOff && <span className="sr-only">({t("notifications.types.emailOffShort")})</span>}
            </span>
          ))}
        </div>
        {groups.map((g) => (
          <Fragment key={g.group}>
            <div role="row" className="flex h-8 items-center border-b border-line-soft bg-sunken/60 px-4">
              <span role="rowheader" className="label-caps">
                {t(`notifications.groups.${g.group}`)}
              </span>
            </div>
            {g.types.map((row) => {
              const label = t(`notifications.kinds.${row.type}.label`, { defaultValue: row.type });
              return (
                <div key={row.type} role="row" className={cn(GRID, "min-h-[52px] border-b border-line-soft px-4 py-2 last:border-0")}>
                  <div role="cell" className="min-w-0 pr-2">
                    <div className="flex min-w-0 items-center gap-1.5">
                      <span className="truncate text-[12.5px] font-medium text-fg">{label}</span>
                      {row.urgent && (
                        <Tooltip content={urgentHint}>
                          <span tabIndex={0} className="shrink-0 rounded-[5px] outline-none focus-visible:shadow-[0_0_0_2px_var(--focus)]">
                            <Badge tone="amber">{t("notifications.types.urgent")}</Badge>
                          </span>
                        </Tooltip>
                      )}
                    </div>
                    <div className="mt-0.5 text-[12px] leading-[17px] text-fg-3">{t(`notifications.kinds.${row.type}.hint`, { defaultValue: "" })}</div>
                  </div>
                  <div role="cell" className="flex justify-center">
                    <Tooltip content={t("notifications.types.alwaysOn")}>
                      <span className="flex size-[18px] items-center justify-center rounded-full text-fg-4" aria-label={t("notifications.types.alwaysOn")} role="img">
                        <Check className="size-3.5" strokeWidth={2.25} />
                      </span>
                    </Tooltip>
                  </div>
                  {CHANNELS.map((c) => (
                    <div key={c} role="cell" className="flex justify-center">
                      <Switch
                        checked={row[c]}
                        disabled={c === "email" && emailOff}
                        onCheckedChange={(v) => set(row, c, v)}
                        aria-label={`${label}: ${column(c)}`}
                      />
                    </div>
                  ))}
                </div>
              );
            })}
          </Fragment>
        ))}
      </div>
      <ul className="flex flex-col gap-1.5 border-t border-line bg-sunken/60 px-4 py-3 text-[12px] leading-[18px] text-fg-3">
        {emailOff && (
          <Note icon={<MailX />} strong>
            {t("notifications.types.emailOff")}
          </Note>
        )}
        <Note icon={<Smartphone />}>{t("notifications.types.phoneHint")}</Note>
        <Note icon={<Laptop />}>{t("notifications.types.desktopHint")}</Note>
        <DesktopPermission />
      </ul>
    </Section>
  );
}

function Note({ icon, children, strong }: { icon: ReactNode; children: ReactNode; strong?: boolean }) {
  return (
    <li className={cn("flex items-start gap-2", strong && "text-fg-2")}>
      <span className="mt-[2px] flex shrink-0 text-fg-4 [&_svg]:size-3.5">{icon}</span>
      <span>{children}</span>
    </li>
  );
}
