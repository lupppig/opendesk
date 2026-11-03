// Dense data tables: sticky header, hairline rows, a whole row that opens its
// record (by click, or Enter when focused), and "Load more" for cursor pages.

import { useNavigate, type LinkProps } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/cn";

export function Table({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <div className={cn("w-full overflow-x-auto", className)}>
      <table className="w-full border-collapse text-left text-[12.5px]">{children}</table>
    </div>
  );
}

export function THead({ children }: { children: ReactNode }) {
  return (
    <thead className="sticky top-0 z-[1] bg-surface">
      <tr className="shadow-[inset_0_-1px_0_var(--line)]">{children}</tr>
    </thead>
  );
}

export function TH({ children, className, align }: { children?: ReactNode; className?: string; align?: "right" }) {
  return (
    <th scope="col" className={cn("h-8 whitespace-nowrap px-3 text-[11px] font-medium text-fg-3 first:pl-4 last:pr-4 md:first:pl-6 md:last:pr-6", align === "right" && "text-right", className)}>
      {children}
    </th>
  );
}

export function TD({ children, className, align, mono }: { children?: ReactNode; className?: string; align?: "right"; mono?: boolean }) {
  return (
    <td className={cn("h-11 px-3 align-middle first:pl-4 last:pr-4 md:first:pl-6 md:last:pr-6", align === "right" && "text-right", mono && "mono tabular-nums", className)}>{children}</td>
  );
}

/** A row that opens a record. Links inside it keep working on their own. */
export function RowLink({ children, to, params, search, className }: { children: ReactNode; className?: string } & Pick<LinkProps, "to" | "params" | "search">) {
  const navigate = useNavigate();
  const open = () => void navigate({ to, params, search } as Parameters<typeof navigate>[0]);
  return (
    <tr
      tabIndex={0}
      onClick={(e) => {
        if ((e.target as HTMLElement).closest("a,button,[role=menuitem]")) return;
        if (window.getSelection()?.toString()) return;
        open();
      }}
      onKeyDown={(e) => {
        if (e.key === "Enter" && e.target === e.currentTarget) open();
      }}
      className={cn(
        "cursor-pointer border-b border-line-soft outline-none transition-colors duration-100 last:border-b-0 hover:bg-sunken focus-visible:bg-hover",
        className,
      )}
    >
      {children}
    </tr>
  );
}

export function TR({ children, className }: { children: ReactNode; className?: string }) {
  return <tr className={cn("border-b border-line-soft last:border-b-0", className)}>{children}</tr>;
}

export function TableSkeleton({ rows = 8, cols = 5 }: { rows?: number; cols?: number }) {
  const { t } = useTranslation();
  return (
    <div aria-busy="true" aria-label={t("loading")} className="divide-y divide-line-soft">
      {Array.from({ length: rows }).map((_, i) => (
        <div key={i} className="flex h-11 items-center gap-6 px-4 md:px-6" style={{ opacity: 1 - i * (0.75 / rows) }}>
          {Array.from({ length: cols }).map((__, j) => (
            <Skeleton key={j} className={cn("h-2.5", j === 0 ? "w-48" : "w-16")} style={j === 0 ? { width: `${140 + ((i * 37) % 90)}px` } : undefined} />
          ))}
        </div>
      ))}
    </div>
  );
}

export function LoadMore({ hasMore, loading, onLoad, shown }: { hasMore: boolean; loading: boolean; onLoad: () => void; shown: number }) {
  const { t } = useTranslation();
  if (!hasMore && shown === 0) return null;
  return (
    <div className="flex items-center justify-center gap-3 border-t border-line-soft py-3 text-[11.5px] text-fg-4">
      <span className="mono tabular-nums">{t("table.shown", { count: shown })}</span>
      {hasMore ? (
        <Button size="xs" variant="secondary" loading={loading} onClick={onLoad}>
          {t("actions.loadMore")}
        </Button>
      ) : (
        <span>{t("table.end")}</span>
      )}
    </div>
  );
}
