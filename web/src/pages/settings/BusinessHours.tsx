// Settings > Business hours: the workspace's opening hours and holidays.
// SLA policies with "business hours" count only while the workspace is open
// (contracts/api.md "Business hours"). Edits are a draft saved with PUT.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { CalendarDays, CalendarOff, ChevronDown, Copy, Gauge, Globe, MoreHorizontal, Plus, X } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Row, Section, SettingsPage } from "@/components/layout";
import { MotionList, MotionRow } from "@/components/settings2/motion";
import { SettingsEmpty } from "@/components/settings2/SettingsEmpty";
import { UnsavedGuard } from "@/components/settings2/UnsavedGuard";
import { SaveBar } from "@/components/settings/SaveBar";
import { Button } from "@/components/ui/button";
import { TextInput } from "@/components/ui/input";
import { Menu, MenuContent, MenuItem, MenuTrigger } from "@/components/ui/menu";
import { Picker } from "@/components/ui/picker";
import { Select } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast";
import { Tooltip } from "@/components/ui/tooltip";
import { api } from "@/lib/api/endpoints";
import type { BusinessHours as BH, Holiday, TimeRange, Weekday } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { timeOfDay, twelveHour } from "@/lib/format";
import { dur, springs } from "@/lib/motion";
import { q, qk } from "@/lib/queries";
import { atLeast, useRole } from "@/lib/session";
import { tone } from "@/lib/tones";
import { DateField } from "@/components/ui/date-picker";

export const WEEK: Weekday[] = ["mon", "tue", "wed", "thu", "fri", "sat", "sun"];
const WEEKDAYS: Weekday[] = ["mon", "tue", "wed", "thu", "fri"];
const MAX_RANGES = 3;
const MAX_HOLIDAYS = 100;

/** "00:00" .. "24:00" on the quarter hour. */
const TIMES = Array.from({ length: 97 }, (_, i) => `${String(Math.floor(i / 4)).padStart(2, "0")}:${String((i % 4) * 15).padStart(2, "0")}`);
const mins = (hhmm: string) => {
  const [h, m] = hhmm.split(":").map(Number);
  return h * 60 + m;
};
const hhmm = (n: number) => TIMES[Math.max(0, Math.min(96, Math.round(n / 15)))];

export function timezones(): string[] {
  try {
    return Intl.supportedValuesOf("timeZone");
  } catch {
    return ["UTC", "Europe/London", "Europe/Berlin", "America/New_York", "America/Los_Angeles", "Asia/Tokyo"];
  }
}

/** The first problem in a day's ranges, if any: an empty or reversed range, or an overlap. */
export function dayError(ranges: TimeRange[]): "order" | "overlap" | null {
  const sorted = [...ranges].sort((a, b) => mins(a.start) - mins(b.start));
  for (const r of sorted) if (mins(r.start) >= mins(r.end)) return "order";
  for (let i = 1; i < sorted.length; i++) if (mins(sorted[i].start) < mins(sorted[i - 1].end)) return "overlap";
  return null;
}

export function weeklyMinutes(schedule: BH["schedule"]): number {
  return WEEK.reduce((sum, d) => sum + (schedule[d] ?? []).reduce((s, r) => s + Math.max(0, mins(r.end) - mins(r.start)), 0), 0);
}

/** Whether the workspace is open right now in its timezone. */
export function openNow(bh: BH, now = new Date()): boolean | null {
  try {
    const parts = new Intl.DateTimeFormat("en-GB", { timeZone: bh.timezone, weekday: "short", hour: "2-digit", minute: "2-digit", year: "numeric", month: "2-digit", day: "2-digit", hourCycle: "h23" }).formatToParts(now);
    const get = (k: string) => parts.find((p) => p.type === k)?.value ?? "";
    const date = `${get("year")}-${get("month")}-${get("day")}`;
    if (bh.holidays.some((h) => h.date === date)) return false;
    const day = get("weekday").slice(0, 3).toLowerCase() as Weekday;
    const at = Number(get("hour")) * 60 + Number(get("minute"));
    return (bh.schedule[day] ?? []).some((r) => at >= mins(r.start) && at < mins(r.end));
  } catch {
    return null;
  }
}

export const norm = (b: BH): BH => ({
  ...b,
  schedule: Object.fromEntries(WEEK.map((d) => [d, b.schedule[d] ?? []])) as BH["schedule"],
  holidays: [...b.holidays].sort((x, y) => x.date.localeCompare(y.date)),
});

export default function BusinessHours() {
  const { t } = useTranslation("settingsInbox");
  const canEdit = atLeast(useRole(), "admin");
  const qc = useQueryClient();
  const { data, isLoading } = useQuery(q.businessHours());
  const [draft, setDraft] = useState<BH | null>(() => (data ? norm(data) : null));
  const [prevData, setPrevData] = useState(data);
  if (prevData !== data) {
    setPrevData(data);
    setDraft(data ? norm(data) : null);
  }
  const tzItems = useMemo(() => timezones().map((z) => ({ value: z, label: z.replace(/_/g, " "), keywords: z.split("/") })), []);

  const save = useMutation({
    mutationFn: (b: BH) => api.businessHours.put(b),
    onSuccess: (b) => {
      qc.setQueryData(qk.businessHours, b);
      void qc.invalidateQueries({ queryKey: qk.workspace });
      toast.success(t("hours.saved"));
    },
    onError: (e) => toast.error(e, t("hours.saveFailed")),
  });

  if (isLoading || !data || !draft) {
    return (
      <SettingsPage title={t("hours.title")} description={t("hours.description")}>
        <Skeleton className="h-28 w-full rounded-lg" />
        <Skeleton className="h-72 w-full rounded-lg" />
      </SettingsPage>
    );
  }

  const base = norm(data);
  const dirty = JSON.stringify(base) !== JSON.stringify(draft);
  const errors = WEEK.map((d) => dayError(draft.schedule[d]));
  const invalid = errors.some(Boolean);
  const total = weeklyMinutes(draft.schedule);
  const open = draft.enabled ? openNow(draft) : null;
  const setDay = (d: Weekday, ranges: TimeRange[]) => setDraft({ ...draft, schedule: { ...draft.schedule, [d]: ranges } });

  return (
    <SettingsPage
      title={t("hours.title")}
      description={t("hours.description")}
      actions={
        open !== null && (
          <span className={cn("inline-flex h-6 items-center gap-1.5 rounded-sm px-2 text-[12px] font-medium", open ? tone.green.chip : tone.gray.chip)}>
            <span className={cn("size-1.5 rounded-full", open ? tone.green.solid : tone.gray.solid)} />
            {open ? t("hours.openNow") : t("hours.closedNow")}
          </span>
        )
      }
    >
      <UnsavedGuard when={dirty && !save.isPending} />
      <fieldset disabled={!canEdit} className="flex flex-col gap-8">
        <Section>
          <Row label={t("hours.enabled")} description={draft.enabled ? t("hours.enabledOn") : t("hours.enabledOff")}>
            <Switch aria-label={t("hours.enabled")} checked={draft.enabled} disabled={!canEdit} onCheckedChange={(enabled) => setDraft({ ...draft, enabled })} />
          </Row>
          <Row label={t("hours.timezone")} description={t("hours.timezoneHint")}>
            <Picker
              items={tzItems}
              value={draft.timezone}
              onSelect={(timezone) => setDraft({ ...draft, timezone })}
              placeholder={t("hours.searchTimezones")}
              width={300}
              align="end"
              trigger={
                <Button disabled={!canEdit} className="w-[280px] justify-between font-normal" icon={<Globe className="size-3.5 text-fg-3" />} iconRight={<ChevronDown className="size-3.5 text-fg-3" />}>
                  <span className="flex-1 truncate text-left">{draft.timezone.replace(/_/g, " ")}</span>
                </Button>
              }
            />
          </Row>
          <Row label={t("hours.sla")} description={t("hours.slaHint")}>
            <Button asChild size="sm" variant="ghost" icon={<Gauge className="size-3.5" />}>
              <Link to="/settings/sla">{t("hours.slaLink")}</Link>
            </Button>
          </Row>
        </Section>

        <Section
          flush
          title={t("hours.weekTitle")}
          description={t("hours.weekHint")}
          actions={<span className="text-[12px] tabular-nums text-fg-3">{t("hours.perWeek", { hours: Math.round((total / 60) * 10) / 10 })}</span>}
        >
          <div className={cn("transition-opacity duration-200", !draft.enabled && "opacity-55")}>
            {WEEK.map((d, i) => (
              <DayRow
                key={d}
                day={d}
                ranges={draft.schedule[d]}
                error={errors[i]}
                disabled={!canEdit}
                onChange={(r) => setDay(d, r)}
                onCopy={(days) => {
                  const next = { ...draft.schedule };
                  for (const x of days) next[x] = draft.schedule[d].map((r) => ({ ...r }));
                  setDraft({ ...draft, schedule: next });
                }}
              />
            ))}
          </div>
        </Section>

        <Holidays holidays={draft.holidays} disabled={!canEdit} onChange={(holidays) => setDraft({ ...draft, holidays })} />
      </fieldset>

      <SaveBar dirty={dirty && canEdit} saving={save.isPending} disabled={invalid} onSave={() => save.mutate(draft)} onDiscard={() => setDraft(base)} />
    </SettingsPage>
  );
}

export function DayRow({
  day,
  ranges,
  error,
  disabled,
  onChange,
  onCopy,
}: {
  day: Weekday;
  ranges: TimeRange[];
  error: "order" | "overlap" | null;
  disabled: boolean;
  onChange: (r: TimeRange[]) => void;
  onCopy: (days: Weekday[]) => void;
}) {
  const { t } = useTranslation("settingsInbox");
  const reduce = useReducedMotion();
  const open = ranges.length > 0;
  const last = ranges[ranges.length - 1];
  const canAdd = open && ranges.length < MAX_RANGES && !!last && mins(last.end) < 24 * 60;
  const addRange = () => {
    const start = mins(last.end) + 60 <= 24 * 60 ? mins(last.end) + 60 : mins(last.end);
    onChange([...ranges, { start: hhmm(start), end: hhmm(Math.min(24 * 60, start + 120)) }]);
  };
  const timeOptions = (filter: (m: number) => boolean) => TIMES.filter((x) => filter(mins(x))).map((x) => ({ value: x, label: timeOfDay(x) }));
  const dayName = t(`hours.days.${day}`);

  return (
    <div className="flex flex-col gap-2 border-b border-line-soft px-4 py-3 last:border-0 sm:flex-row sm:items-start">
      <div className="flex h-7 w-[150px] shrink-0 items-center gap-3">
        <Switch
          aria-label={t("hours.openOn", { day: dayName })}
          checked={open}
          disabled={disabled}
          onCheckedChange={(v) => onChange(v ? [{ start: "09:00", end: "17:00" }] : [])}
        />
        <span className={cn("text-[13px] font-medium", !open && "text-fg-3")}>{dayName}</span>
      </div>
      <div className="flex min-w-0 flex-1 flex-col gap-1.5">
        {!open ? (
          <span className="flex h-7 items-center text-[12.5px] text-fg-4">{t("hours.closed")}</span>
        ) : (
          <AnimatePresence initial={false}>
            {ranges.map((r, i) => (
              <motion.div
                key={i}
                layout={reduce ? false : "position"}
                initial={reduce ? false : { opacity: 0, y: -4 }}
                animate={{ opacity: 1, y: 0 }}
                exit={reduce ? { opacity: 0, transition: { duration: 0 } } : { opacity: 0, transition: { duration: dur.fast } }}
                transition={springs.row}
                className="flex items-center gap-2"
              >
                <Select
                  size="sm"
                  className={cn("mono", twelveHour() ? "w-[112px]" : "w-[92px]")}
                  aria-label={t("hours.startOf", { day: dayName, n: i + 1 })}
                  disabled={disabled}
                  value={r.start}
                  onChange={(start) => onChange(ranges.map((x, j) => (j === i ? { ...x, start } : x)))}
                  options={timeOptions((m) => m < 24 * 60)}
                />
                <span className="text-[12px] text-fg-4">{t("hours.to")}</span>
                <Select
                  size="sm"
                  className={cn("mono", twelveHour() ? "w-[112px]" : "w-[92px]")}
                  aria-label={t("hours.endOf", { day: dayName, n: i + 1 })}
                  disabled={disabled}
                  value={r.end}
                  onChange={(end) => onChange(ranges.map((x, j) => (j === i ? { ...x, end } : x)))}
                  options={timeOptions((m) => m > 0)}
                />
                {ranges.length > 1 && (
                  <Tooltip content={t("hours.removeRange")}>
                    <Button
                      size="sm"
                      variant="ghost"
                      square
                      disabled={disabled}
                      aria-label={t("hours.removeRangeOf", { day: dayName, n: i + 1 })}
                      onClick={() => onChange(ranges.filter((_, j) => j !== i))}
                    >
                      <X className="size-3.5" />
                    </Button>
                  </Tooltip>
                )}
              </motion.div>
            ))}
          </AnimatePresence>
        )}
        {error && (
          <p role="alert" className="text-[12px] text-danger-fg">
            {error === "order" ? t("hours.errors.order") : t("hours.errors.overlap")}
          </p>
        )}
      </div>
      {open && !disabled && (
        <div className="flex shrink-0 items-center gap-1 self-start">
          <Tooltip content={canAdd ? t("hours.addRange") : t("hours.maxRanges")}>
            <span>
              <Button size="sm" variant="ghost" square disabled={!canAdd} aria-label={t("hours.addRangeOn", { day: dayName })} onClick={addRange}>
                <Plus className="size-3.5" />
              </Button>
            </span>
          </Tooltip>
          <Menu>
            <MenuTrigger asChild>
              <Button size="sm" variant="ghost" square aria-label={t("hours.copyMenu", { day: dayName })}>
                <MoreHorizontal className="size-4" />
              </Button>
            </MenuTrigger>
            <MenuContent>
              <MenuItem icon={<Copy />} onSelect={() => onCopy(WEEKDAYS.filter((x) => x !== day))}>
                {t("hours.copyWeekdays")}
              </MenuItem>
              <MenuItem icon={<CalendarDays />} onSelect={() => onCopy(WEEK.filter((x) => x !== day))}>
                {t("hours.copyAll")}
              </MenuItem>
            </MenuContent>
          </Menu>
        </div>
      )}
    </div>
  );
}

function todayIso() {
  const d = new Date();
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

export function Holidays({ holidays, disabled, onChange }: { holidays: Holiday[]; disabled: boolean; onChange: (h: Holiday[]) => void }) {
  const { t, i18n } = useTranslation("settingsInbox");
  const reduce = useReducedMotion();
  const [adding, setAdding] = useState(false);
  const [date, setDate] = useState("");
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);
  const today = todayIso();
  const fmt = (iso: string, o: Intl.DateTimeFormatOptions) => new Intl.DateTimeFormat(i18n.language, { ...o, timeZone: "UTC" }).format(new Date(`${iso}T00:00:00Z`));
  const dayDiff = (iso: string) => Math.round((Date.parse(`${iso}T00:00:00Z`) - Date.parse(`${today}T00:00:00Z`)) / 86_400_000);

  const add = () => {
    setError(null);
    if (!date) return setError(t("hours.holidays.needDate"));
    if (!name.trim()) return setError(t("hours.holidays.needName"));
    if (holidays.some((h) => h.date === date)) return setError(t("hours.holidays.duplicate"));
    if (holidays.length >= MAX_HOLIDAYS) return setError(t("hours.holidays.tooMany", { count: MAX_HOLIDAYS }));
    onChange([...holidays, { date, name: name.trim() }].sort((x, y) => x.date.localeCompare(y.date)));
    setDate("");
    setName("");
  };
  const remove = (h: Holiday) => onChange(holidays.filter((x) => x.date !== h.date));
  const startAdding = () => {
    setAdding(true);
    setError(null);
  };
  const upcoming = holidays.filter((h) => h.date >= today);
  const past = holidays.length - upcoming.length;

  return (
    <Section
      flush
      title={t("hours.holidays.title")}
      description={t("hours.holidays.hint")}
      actions={
        !disabled &&
        holidays.length > 0 &&
        !adding && (
          <Button size="sm" icon={<Plus className="size-3.5" />} onClick={startAdding}>
            {t("hours.holidays.add")}
          </Button>
        )
      }
    >
      <AnimatePresence initial={false}>
        {adding && (
          <motion.form
            key="add"
            initial={reduce ? false : { opacity: 0, height: 0 }}
            animate={{ opacity: 1, height: "auto" }}
            exit={reduce ? { opacity: 0, transition: { duration: 0 } } : { opacity: 0, height: 0, transition: { duration: dur.fast } }}
            transition={springs.glide}
            className="overflow-hidden border-b border-line-soft bg-sunken/60"
            onSubmit={(e) => {
              e.preventDefault();
              add();
            }}
          >
            <div className="flex flex-wrap items-start gap-2 px-4 py-3">
              <DateField
                size="sm"
                className="w-[150px]"
                aria-label={t("hours.holidays.date")}
                value={date}
                invalid={!!error && !date}
                onChange={setDate}
                autoFocus
              />
              <TextInput
                inputSize="sm"
                className="min-w-[180px] flex-1"
                aria-label={t("hours.holidays.name")}
                placeholder={t("hours.holidays.namePlaceholder")}
                value={name}
                maxLength={80}
                invalid={!!error && !!date && !name.trim()}
                onChange={(e) => setName(e.target.value)}
              />
              <Button type="submit" size="sm" variant="primary">
                {t("hours.holidays.addConfirm")}
              </Button>
              <Button type="button" size="sm" variant="ghost" onClick={() => setAdding(false)}>
                {t("hours.holidays.done")}
              </Button>
              {error && (
                <p role="alert" className="basis-full text-[12px] text-danger-fg">
                  {error}
                </p>
              )}
            </div>
          </motion.form>
        )}
      </AnimatePresence>
      {!holidays.length ? (
        !adding && (
          <SettingsEmpty
            className="min-h-[300px]"
            art="pending"
            title={t("hours.holidays.emptyTitle")}
            description={t("hours.holidays.emptyHint")}
            action={
              !disabled && (
                <Button variant="primary" icon={<Plus className="size-3.5" />} onClick={startAdding}>
                  {t("hours.holidays.add")}
                </Button>
              )
            }
            tips={[
              { label: t("hours.holidays.tips.closed"), icon: <CalendarOff /> },
              { label: t("hours.holidays.tips.sla"), icon: <Gauge /> },
            ]}
          />
        )
      ) : (
        <MotionList>
          {holidays.map((h) => {
            const diff = dayDiff(h.date);
            const isPast = diff < 0;
            return (
              <MotionRow key={h.date}>
                <div className={cn("group flex min-h-12 items-center gap-3 px-4 py-2", isPast && "opacity-60")}>
                  <span
                    aria-hidden
                    className={cn("flex size-9 shrink-0 flex-col items-center justify-center rounded-md leading-none", isPast ? tone.gray.chip : tone.amber.chip)}
                  >
                    <span className="text-[9px] font-semibold uppercase tracking-wide">{fmt(h.date, { month: "short" })}</span>
                    <span className="mt-0.5 text-[13px] font-semibold tabular-nums">{fmt(h.date, { day: "numeric" })}</span>
                  </span>
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-[13px] font-medium">{h.name}</div>
                    <div className="mt-0.5 text-[12px] text-fg-3">
                      {fmt(h.date, { weekday: "long", year: "numeric", month: "long", day: "numeric" })}
                      <span className="text-fg-4">
                        {" · "}
                        {diff === 0 ? t("hours.holidays.today") : isPast ? t("hours.holidays.past") : t("hours.holidays.inDays", { count: diff })}
                      </span>
                    </div>
                  </div>
                  {!disabled && (
                    <Tooltip content={t("hours.holidays.remove")}>
                      <Button
                        size="sm"
                        variant="ghost"
                        square
                        aria-label={t("hours.holidays.removeNamed", { name: h.name })}
                        onClick={() => remove(h)}
                        className="md:opacity-0 md:group-hover:opacity-100 md:focus-visible:opacity-100"
                      >
                        <X className="size-3.5" />
                      </Button>
                    </Tooltip>
                  )}
                </div>
              </MotionRow>
            );
          })}
        </MotionList>
      )}
      {past > 0 && (
        <div className="border-t border-line-soft px-4 py-2 text-[12px] text-fg-4">
          {t("hours.holidays.pastCount", { count: past })}{" "}
          {!disabled && (
            <button type="button" className="text-fg-3 underline decoration-line-strong underline-offset-2 hover:text-fg" onClick={() => onChange(holidays.filter((h) => h.date >= today))}>
              {t("hours.holidays.clearPast")}
            </button>
          )}
        </div>
      )}
    </Section>
  );
}
