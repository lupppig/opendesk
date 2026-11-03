// The times a ticket's snooze menu offers (Settings → Workspace, settings.snooze_presets):
// each "in N hours", "in N days at a time" or "next week at a time", in the menu's order.

import { Plus, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { TimeField } from "@/components/ui/date-picker";
import { NumberInput } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { DEFAULT_SNOOZE_PRESETS, MAX_SNOOZE_PRESETS, SNOOZE_MAX_DAYS, SNOOZE_MAX_HOURS, sameSnoozePresets, snoozeAt, snoozeLabel } from "@/components/tickets/detail/snooze";
import type { SnoozePreset } from "@/lib/api/types";
import { weekdayClock } from "@/lib/format";

/** A time switched to another kind keeps what it can: its time of day, else 09:00. */
function asKind(p: SnoozePreset, kind: SnoozePreset["kind"]): SnoozePreset {
  const time = p.kind === "hours" ? "09:00" : p.time;
  if (kind === "hours") return { kind, hours: 1 };
  if (kind === "days") return { kind, days: 1, time };
  return { kind, time };
}

export function SnoozePresetsEditor({ value, onChange, disabled }: { value: SnoozePreset[]; onChange: (v: SnoozePreset[]) => void; disabled?: boolean }) {
  const { t } = useTranslation("settings");
  const { t: ti } = useTranslation("inbox");
  const set = (i: number, p: SnoozePreset) => onChange(value.map((x, j) => (j === i ? p : x)));
  const kinds = [
    { value: "hours" as const, label: t("workspace.snoozeKindHours") },
    { value: "days" as const, label: t("workspace.snoozeKindDays") },
    { value: "week" as const, label: t("workspace.snoozeKindWeek") },
  ];
  const now = new Date();
  return (
    <div className="flex flex-col gap-2">
      {value.map((p, i) => (
        <div key={i} className="flex flex-wrap items-center gap-2">
          <Select className="w-[180px]" value={p.kind} onChange={(k) => set(i, asKind(p, k))} options={kinds} disabled={disabled} aria-label={t("workspace.snoozeKind")} />
          {p.kind === "hours" && (
            <NumberInput className="w-[72px]" value={p.hours} min={1} max={SNOOZE_MAX_HOURS} onChange={(n) => set(i, { ...p, hours: n })} aria-label={t("workspace.snoozeHours")} />
          )}
          {p.kind === "days" && (
            <NumberInput className="w-[72px]" value={p.days} min={1} max={SNOOZE_MAX_DAYS} onChange={(n) => set(i, { ...p, days: n })} aria-label={t("workspace.snoozeDays")} />
          )}
          {/* Next week has no count: keep its "at" and time under the others'. */}
          {p.kind === "week" && <span aria-hidden className="w-[72px]" />}
          {p.kind !== "hours" && (
            <>
              <span className="text-[12.5px] text-fg-3">{t("workspace.snoozeAt")}</span>
              <TimeField value={p.time} onChange={(v) => set(i, { ...p, time: v })} aria-label={t("workspace.snoozeTime")} />
            </>
          )}
          <span className="min-w-0 flex-1 truncate text-[12px] text-fg-3">
            {snoozeLabel(p, ti)} · <span className="mono text-[11px] text-fg-4">{weekdayClock(snoozeAt(p, now))}</span>
          </span>
          <Button
            variant="ghost"
            size="sm"
            square
            aria-label={t("workspace.snoozeRemove")}
            disabled={disabled || value.length <= 1}
            onClick={() => onChange(value.filter((_, j) => j !== i))}
            icon={<X className="size-3.5" />}
          />
        </div>
      ))}
      <div className="flex flex-wrap items-center gap-1">
        <Button
          variant="ghost"
          size="sm"
          icon={<Plus className="size-3.5" />}
          disabled={disabled || value.length >= MAX_SNOOZE_PRESETS}
          onClick={() => onChange([...value, { kind: "days", days: 1, time: "09:00" }])}
        >
          {t("workspace.snoozeAdd")}
        </Button>
        {!sameSnoozePresets(value, DEFAULT_SNOOZE_PRESETS) && (
          <Button variant="ghost" size="sm" disabled={disabled} onClick={() => onChange([...DEFAULT_SNOOZE_PRESETS])}>
            {t("workspace.snoozeReset")}
          </Button>
        )}
      </div>
    </div>
  );
}
