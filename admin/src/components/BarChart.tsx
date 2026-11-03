// A small daily bar chart, hand-rolled in SVG: one series, a hairline baseline,
// the hovered (or arrow-key focused) day called out above the bars.

import { useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { cn } from "@/lib/cn";
import { tone as tones, toneVar, type Tone } from "@/lib/tones";

export type Bar = { key: string; label: string; value: number };

export function BarChart({
  data,
  format,
  hue = "blue",
  height = 140,
  label,
}: {
  data: Bar[];
  format: (v: number) => string;
  hue?: Tone;
  height?: number;
  /** What the chart shows, for screen readers. */
  label: string;
}) {
  const { t } = useTranslation();
  const [active, setActive] = useState<number | null>(null);
  const max = Math.max(0, ...data.map((d) => d.value));
  const n = Math.max(1, data.length);
  const gap = 0.22;
  const w = 100 / n;
  const total = data.reduce((s, d) => s + d.value, 0);
  const shown = active ?? data.length - 1;
  const cur = data[shown];

  return (
    <div className="flex flex-col gap-2">
      <div className="flex h-9 items-end justify-between gap-3">
        <div className="min-w-0">
          <div className="mono text-[18px] font-medium leading-none tracking-[-0.02em] tabular-nums text-fg">{cur ? format(cur.value) : format(0)}</div>
          <div className="mt-1 text-[11px] text-fg-3">{cur ? cur.label : t("noData")}</div>
        </div>
        <div className="text-right text-[11px] text-fg-3">
          <Trans t={t} i18nKey="chart.total" count={data.length} values={{ total: format(total) }} components={{ n: <span className="mono text-fg-2 tabular-nums" /> }} />
        </div>
      </div>
      <div
        tabIndex={0}
        role="img"
        aria-label={t("chart.summary", { label, total: format(total), count: data.length })}
        className="relative rounded-md outline-none focus-visible:shadow-[0_0_0_3px_var(--focus)]"
        style={{ height }}
        onMouseLeave={() => setActive(null)}
        onBlur={() => setActive(null)}
        onKeyDown={(e) => {
          if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
          e.preventDefault();
          const from = active ?? data.length - 1;
          setActive(Math.min(data.length - 1, Math.max(0, from + (e.key === "ArrowRight" ? 1 : -1))));
        }}
      >
        <svg viewBox={`0 0 100 ${height}`} preserveAspectRatio="none" className="absolute inset-0 size-full overflow-visible" aria-hidden>
          <line x1="0" x2="100" y1={height - 0.5} y2={height - 0.5} stroke="var(--line)" strokeWidth="1" vectorEffect="non-scaling-stroke" />
          {data.map((d, i) => {
            const h = max > 0 ? Math.max(d.value > 0 ? 2 : 0, (d.value / max) * (height - 6)) : 0;
            const on = i === shown;
            return (
              <g key={d.key} onMouseEnter={() => setActive(i)}>
                <rect x={i * w} y={0} width={w} height={height} fill="transparent" />
                <rect
                  x={i * w + (w * gap) / 2}
                  y={height - h - 1}
                  width={w * (1 - gap)}
                  height={h}
                  rx={0.6}
                  fill={toneVar(hue)}
                  opacity={active === null ? (on ? 1 : 0.55) : on ? 1 : 0.3}
                  className="transition-opacity duration-150 motion-reduce:transition-none"
                />
              </g>
            );
          })}
        </svg>
      </div>
      <div className="flex justify-between text-[10.5px] text-fg-4">
        <span>{data[0]?.label}</span>
        <span>{data[data.length - 1]?.label}</span>
      </div>
    </div>
  );
}

/** A legend dot in a series' hue. */
export function SeriesDot({ hue, className }: { hue: Tone; className?: string }) {
  return <span aria-hidden className={cn("inline-block size-2 rounded-[2px]", tones[hue].solid, className)} />;
}
