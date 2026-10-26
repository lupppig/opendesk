import { useTranslation } from "react-i18next";
import { i18n } from "@/i18n";
import { dateLocale, datePattern } from "@/i18n/dates";
import { format, parseISO } from "date-fns";
import { motion, useReducedMotion } from "motion/react";
import { useState } from "react";
import { ease } from "@/lib/motion";
import type { ReportsOverview } from "@/lib/api/types";
import { SERIES, toneVar } from "@/lib/tones";
import { monotonePath, niceTicks, useWidth } from "./useSize";

type Day = ReportsOverview["by_day"][number];

/** One line: a numeric key of the day rows, its label and colour. */
export type LineSeries<D> = { key: keyof D & string; label: string; color: string };
type Row = { date: string };

const H = 240; // plot height
const AXIS = 28; // x-axis band, inside the container height
const ML = 36;
const MR = 40;
const MT = 12;

export const series = () => [
  { key: "created", label: i18n.t("indicators:reports.created"), color: toneVar(SERIES[0]) },
  { key: "solved", label: i18n.t("indicators:reports.solved"), color: toneVar(SERIES[2]) },
] as const;

/**
 * Lines per day on one axis (created vs solved by default), crosshair and
 * tooltip on hover and arrow keys. The first series gets a faint area.
 */
export function VolumeChart<D extends Row = Day>({
  days,
  series: customLines,
  label,
}: {
  days: D[];
  series?: LineSeries<D>[];
  label?: string;
}) {
  const { t } = useTranslation("indicators");
  const lines = customLines ?? series() as unknown as LineSeries<D>[];
  const val = (d: D, k: keyof D) => Number(d[k]) || 0;
  const [ref, width] = useWidth<HTMLDivElement>();
  const [hover, setHover] = useState<number | null>(null);
  const reduce = useReducedMotion();

  const plotW = Math.max(0, width - ML - MR);
  const max = Math.max(1, ...days.map((d) => Math.max(...lines.map((s) => val(d, s.key)))));
  const ticks = niceTicks(max);
  const top = ticks[ticks.length - 1];
  const x = (i: number) => ML + (days.length <= 1 ? plotW / 2 : (i / (days.length - 1)) * plotW);
  const y = (v: number) => MT + H - (v / top) * H;

  const paths = lines.map((s) => {
    const pts = days.map((d, i) => [x(i), y(val(d, s.key))] as [number, number]);
    return { ...s, d: monotonePath(pts), pts };
  });
  const area = paths[0].pts.length
    ? `${paths[0].d}L${x(days.length - 1)},${y(0)}L${x(0)},${y(0)}Z`
    : "";

  const step = Math.max(1, Math.ceil(days.length / Math.max(2, Math.floor(plotW / 84))));
  const xTicks = days.map((d, i) => ({ d, i })).filter(({ i }) => i % step === 0);

  const last = days.length - 1;
  const ends = lines.map((s) => ({ ...s, v: days[last] ? val(days[last], s.key) : 0 }));
  const endsCollide = ends.some((a, i) => ends.some((b, j) => j > i && Math.abs(y(a.v) - y(b.v)) < 14));

  const pick = (clientX: number, rect: DOMRect) => {
    if (!days.length) return;
    const px = clientX - rect.left - ML;
    const i = Math.round((px / (plotW || 1)) * (days.length - 1));
    setHover(Math.min(last, Math.max(0, i)));
  };

  const h = hover !== null ? days[hover] : null;
  const tipLeft = hover !== null ? x(hover) : 0;
  const flip = tipLeft > width - 180;

  return (
    <div ref={ref} className="relative w-full select-none" style={{ height: H + MT + AXIS }}>
      {width > 0 && (
        <svg width={width} height={H + MT + AXIS} className="block overflow-visible" aria-hidden>
          {ticks.map((t) => (
            <g key={t}>
              <line x1={ML} x2={ML + plotW} y1={y(t)} y2={y(t)} stroke="var(--line-soft)" strokeWidth={1} shapeRendering="crispEdges" />
              <text x={ML - 8} y={y(t)} dy="0.32em" textAnchor="end" className="mono" fontSize={10.5} fill="var(--fg-4)">
                {t.toLocaleString()}
              </text>
            </g>
          ))}
          <line x1={ML} x2={ML + plotW} y1={y(0)} y2={y(0)} stroke="var(--line)" strokeWidth={1} shapeRendering="crispEdges" />
          {xTicks.map(({ d, i }) => (
            <text key={d.date} x={x(i)} y={MT + H + 18} textAnchor="middle" className="mono" fontSize={10.5} fill="var(--fg-4)">
              {format(parseISO(d.date), datePattern("dayMonth"), { locale: dateLocale() })}
            </text>
          ))}

          <motion.path d={area} fill={lines[0].color} initial={reduce ? false : { fillOpacity: 0 }} animate={{ fillOpacity: 0.07 }} transition={{ duration: 0.25, delay: 0.2, ease: ease.out }} />
          {[...paths].reverse().map((p) => (
            <motion.path
              key={p.key}
              d={p.d}
              fill="none"
              stroke={p.color}
              strokeWidth={2}
              strokeLinejoin="round"
              strokeLinecap="round"
              initial={reduce ? false : { pathLength: 0 }}
              animate={{ pathLength: 1 }}
              transition={{ duration: 0.25, ease: ease.out }}
            />
          ))}

          {hover === null &&
            last >= 0 &&
            ends.map((e) => (
              <g key={e.key}>
                <circle cx={x(last)} cy={y(e.v)} r={4} fill={e.color} stroke="var(--surface)" strokeWidth={2} />
                {!endsCollide && (
                  <text x={x(last) + 9} y={y(e.v)} dy="0.32em" className="mono" fontSize={11} fill="var(--fg-2)">
                    {e.v}
                  </text>
                )}
              </g>
            ))}

          {hover !== null && h && (
            <g>
              <line x1={x(hover)} x2={x(hover)} y1={MT} y2={MT + H} stroke="var(--line-strong)" strokeWidth={1} shapeRendering="crispEdges" />
              {lines.map((s) => (
                <circle key={s.key} cx={x(hover)} cy={y(val(h, s.key))} r={4} fill={s.color} stroke="var(--surface)" strokeWidth={2} />
              ))}
            </g>
          )}
        </svg>
      )}

      {/* Hit layer: the whole plot, not the 2px lines. Arrow keys move the crosshair. */}
      <div
        role="img"
        tabIndex={0}
        aria-label={label ?? t("reports.chartLabel", { count: days.length })}
        className="absolute rounded-md outline-none focus-visible:shadow-[0_0_0_2px_var(--focus)]"
        style={{ left: ML, top: MT, width: plotW, height: H }}
        onPointerMove={(e) => pick(e.clientX, e.currentTarget.parentElement!.getBoundingClientRect())}
        onPointerLeave={() => setHover(null)}
        onBlur={() => setHover(null)}
        onFocus={() => setHover((v) => v ?? last)}
        onKeyDown={(e) => {
          if (e.key === "ArrowRight") setHover((v) => Math.min(last, (v ?? -1) + 1));
          else if (e.key === "ArrowLeft") setHover((v) => Math.max(0, (v ?? last + 1) - 1));
          else if (e.key === "Escape") setHover(null);
          else return;
          e.preventDefault();
        }}
      />

      {h && (
        <div
          role="status"
          className="pointer-events-none absolute z-10 min-w-[148px] rounded-lg border border-line bg-raised px-3 py-2 shadow-pop transition-[left] duration-100 ease-out motion-reduce:transition-none"
          style={{ left: flip ? tipLeft - 12 : tipLeft + 12, top: MT + 4, transform: flip ? "translateX(-100%)" : undefined }}
        >
          <div className="mono mb-1.5 text-[10.5px] text-fg-3">{format(parseISO(h.date), datePattern("weekdayDayMonth"), { locale: dateLocale() })}</div>
          {lines.map((s) => (
            <div key={s.key} className="flex items-center gap-2 py-px">
              <span className="size-2 rounded-xs" style={{ background: s.color }} />
              <span className="mono min-w-6 text-[12.5px] font-semibold text-fg">{val(h, s.key)}</span>
              <span className="text-xs text-fg-3">{s.label}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

export function VolumeLegend<D extends Row = Day>({ series: customLines }: { series?: LineSeries<D>[] } = {}) {
  useTranslation("indicators");
  const lines = customLines ?? series() as unknown as LineSeries<D>[];
  return (
    <div className="flex items-center gap-3">
      {lines.map((s) => (
        <span key={s.key} className="flex items-center gap-1.5 text-xs text-fg-2">
          <span className="size-2 rounded-xs" style={{ background: s.color }} />
          {s.label}
        </span>
      ))}
    </div>
  );
}

export function VolumeTable({ days }: { days: Day[] }) {
  const { t } = useTranslation("indicators");
  return (
    <div className="max-h-[268px] overflow-y-auto">
      <table className="w-full text-[12.5px]">
        <caption className="sr-only">{t("reports.tableCaption")}</caption>
        <thead className="sticky top-0 bg-surface">
          <tr className="border-b border-line text-left">
            <th className="label-caps py-2 font-normal">{t("reports.day")}</th>
            <th className="label-caps py-2 text-right font-normal">{t("reports.created")}</th>
            <th className="label-caps py-2 text-right font-normal">{t("reports.solved")}</th>
          </tr>
        </thead>
        <tbody>
          {[...days].reverse().map((d) => (
            <tr key={d.date} className="border-b border-line-soft">
              <td className="py-1.5 text-fg-2">{format(parseISO(d.date), datePattern("weekdayDayMonth"), { locale: dateLocale() })}</td>
              <td className="mono py-1.5 text-right tabular-nums">{d.created}</td>
              <td className="mono py-1.5 text-right tabular-nums text-fg-2">{d.solved}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** A 12-point de-emphasised trend for a stat tile. */
export function Sparkline({ values, color = "var(--fg-4)", width = 72 }: { values: number[]; color?: string; width?: number }) {
  const reduce = useReducedMotion();
  const v = values.slice(-12);
  if (v.length < 2) return null;
  const w = width;
  const hh = 20;
  const max = Math.max(1, ...v);
  const pts = v.map((n, i) => [(i / (v.length - 1)) * (w - 4) + 2, hh - 2 - (n / max) * (hh - 4)] as [number, number]);
  const lastPt = pts[pts.length - 1];
  return (
    <svg width={w} height={hh} aria-hidden className="shrink-0 overflow-visible">
      <motion.path
        d={monotonePath(pts)}
        fill="none"
        stroke={color}
        strokeWidth={1.5}
        strokeLinecap="round"
        strokeLinejoin="round"
        initial={reduce ? false : { pathLength: 0 }}
        animate={{ pathLength: 1 }}
        transition={{ duration: 0.25, ease: ease.out }}
      />
      <circle cx={lastPt[0]} cy={lastPt[1]} r={2.5} fill={color} stroke="var(--surface)" strokeWidth={1.5} />
    </svg>
  );
}
