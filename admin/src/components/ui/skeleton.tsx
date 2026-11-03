import { i18n } from "@/i18n";
import { cn } from "@/lib/cn";

export function Skeleton({ className, style }: { className?: string; style?: React.CSSProperties }) {
  return <div className={cn("skeleton h-3", className)} style={style} aria-hidden />;
}

export function SkeletonRows({ rows = 8, className }: { rows?: number; className?: string }) {
  return (
    <div className={cn("divide-y divide-line-soft", className)} aria-busy="true" aria-label={i18n.t("loading")}>
      {Array.from({ length: rows }).map((_, i) => (
        <div key={i} className="flex items-center gap-3 px-4 py-3" style={{ opacity: 1 - i * (0.8 / rows) }}>
          <Skeleton className="size-5 rounded-sm" />
          <div className="flex flex-1 flex-col gap-1.5">
            <Skeleton className="h-2.5" style={{ width: `${32 + ((i * 17) % 30)}%` }} />
            <Skeleton className="h-2 opacity-60" style={{ width: `${48 + ((i * 23) % 35)}%` }} />
          </div>
          <Skeleton className="h-2 w-10" />
        </div>
      ))}
    </div>
  );
}
