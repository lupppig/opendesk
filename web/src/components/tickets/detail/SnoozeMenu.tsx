import { addDays, addHours, nextMonday, setHours, setMinutes, startOfHour, format } from "date-fns";
import { AlarmClock } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Popover } from "@/components/ui/popover";
import { Button } from "@/components/ui/button";
import { DateField, TimeField, parseDay, parseTime } from "@/components/ui/date-picker";
import { Tooltip } from "@/components/ui/tooltip";
import { weekdayClock } from "@/lib/format";

function at9(d: Date) {
  return setMinutes(setHours(d, 9), 0);
}

export function snoozeOptions(now = new Date()) {
  return [
    { key: "hour", label: "In 1 hour", at: startOfHour(addHours(now, 2)) },
    { key: "hours", label: "In 4 hours", at: startOfHour(addHours(now, 5)) },
    { key: "tomorrow", label: "Tomorrow", at: at9(addDays(now, 1)) },
    { key: "week", label: "Next week", at: at9(nextMonday(now)) },
  ] as const;
}

export function SnoozeMenu({ onSnooze, disabled, open: openProp, onOpenChange }: { onSnooze: (at: Date) => void; disabled?: boolean; open?: boolean; onOpenChange?: (o: boolean) => void }) {
  const { t } = useTranslation("inbox");
  const [innerOpen, setInnerOpen] = useState(false);
  const open = openProp ?? innerOpen;
  const setOpen = (o: boolean) => {
    setInnerOpen(o);
    onOpenChange?.(o);
  };
  const [day, setDay] = useState("");
  const [time, setTime] = useState("09:00");
  const custom = (() => {
    const d = parseDay(day);
    const t = parseTime(time);
    return d && t ? setMinutes(setHours(d, t[0]), t[1]) : null;
  })();
  const valid = !!custom && custom.getTime() > Date.now();
  const opts = snoozeOptions();
  return (
    <Popover
      open={open}
      onOpenChange={setOpen}
      align="end"
      className="w-[264px] p-1"
      trigger={
        <Tooltip content={t("snooze.title")} shortcut="h">
          <Button variant="ghost" size="sm" square aria-label={t("snooze.title")} disabled={disabled} icon={<AlarmClock className="size-3.5" />} />
        </Tooltip>
      }
    >
      <div className="label-caps px-2 pb-1 pt-1.5">{t("snooze.until")}</div>
      {opts.map((o) => (
        <button
          key={o.label}
          type="button"
          onClick={() => {
            onSnooze(o.at);
            setOpen(false);
          }}
          className="flex h-8 w-full items-center justify-between rounded-md px-2 text-[12.5px] outline-none transition-colors hover:bg-hover focus-visible:bg-hover"
        >
          {t(`snooze.${o.key}`)}
          <span className="mono text-[11px] text-fg-4">{weekdayClock(o.at)}</span>
        </button>
      ))}
      <div className="mt-1 border-t border-line p-1.5 pt-2">
        <form
          className="flex items-center gap-1.5"
          onSubmit={(e) => {
            e.preventDefault();
            if (valid) {
              onSnooze(custom);
              setOpen(false);
            }
          }}
        >
          <DateField size="sm" value={day} onChange={setDay} min={format(new Date(), "yyyy-MM-dd")} placeholder={t("snooze.pickDay")} aria-label={t("snooze.until")} className="min-w-0 flex-1" />
          <TimeField size="sm" value={time} onChange={setTime} aria-label={t("snooze.time")} />
          <Button type="submit" size="sm" disabled={!valid}>
            {t("snooze.set")}
          </Button>
        </form>
      </div>
    </Popover>
  );
}
