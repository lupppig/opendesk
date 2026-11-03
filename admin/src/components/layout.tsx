// Page scaffolding shared by every screen, so headers, sections and rows line up.

import { Link } from "@tanstack/react-router";
import { ChevronLeft } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/cn";

/** The 48px bar at the top of the work panel: title, an optional subtitle, actions. */
export function PageHeader({ title, subtitle, actions, children }: { title: ReactNode; subtitle?: ReactNode; actions?: ReactNode; children?: ReactNode }) {
  return (
    <header className="flex h-12 shrink-0 items-center gap-3 border-b border-line bg-surface px-4 md:px-6">
      <div className="flex min-w-0 items-center gap-2">
        <h1 className="truncate text-[13.5px] font-semibold tracking-[-0.015em]">{title}</h1>
        {subtitle && (
          <>
            <span aria-hidden className="mx-1 h-4 w-px shrink-0 bg-line" />
            <span className="truncate text-[12.5px] text-fg-3">{subtitle}</span>
          </>
        )}
      </div>
      {children && <div className="flex min-w-0 flex-1 items-center gap-2">{children}</div>}
      {actions && <div className={cn("flex shrink-0 items-center gap-1.5", !children && "ml-auto")}>{actions}</div>}
    </header>
  );
}

/** The scrolling body of a page. */
export function PageBody({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn("min-h-0 flex-1 overflow-y-auto", className)}>{children}</div>;
}

/** A padded column for detail pages. */
export function PageColumn({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn("mx-auto flex w-full max-w-[1120px] flex-col gap-5 px-4 py-5 md:px-6", className)}>{children}</div>;
}

/** A white card with a quiet header: title, a count, actions. */
export function Section({
  title,
  count,
  actions,
  children,
  className,
  flush,
  id,
}: {
  title: ReactNode;
  count?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  className?: string;
  /** No padding inside: tables and lists run edge to edge. */
  flush?: boolean;
  id?: string;
}) {
  return (
    <section id={id} className={cn("overflow-hidden rounded-lg border border-line bg-surface", className)}>
      <div className="flex min-h-10 items-center gap-2 border-b border-line-soft px-4 py-1.5">
        <h2 className="text-[12.5px] font-semibold tracking-[-0.01em] text-fg">{title}</h2>
        {count !== undefined && <span className="mono text-[11px] tabular-nums text-fg-4">{count}</span>}
        {actions && <div className="ml-auto flex items-center gap-1.5">{actions}</div>}
      </div>
      <div className={flush ? undefined : "px-4 py-3"}>{children}</div>
    </section>
  );
}

/** Label and value rows in a two-column grid. */
export function KV({ items, className }: { items: [ReactNode, ReactNode][]; className?: string }) {
  return (
    <dl className={cn("grid grid-cols-[minmax(110px,max-content)_1fr] gap-x-6 gap-y-2 text-[12.5px]", className)}>
      {items.map(([k, v], i) => (
        <div key={i} className="contents">
          <dt className="text-fg-3">{k}</dt>
          <dd className="min-w-0 text-fg">{v}</dd>
        </div>
      ))}
    </dl>
  );
}

/** A figure tile: label, a big number, a quiet line under it. */
export function StatTile({ label, value, sub, className }: { label: ReactNode; value: ReactNode; sub?: ReactNode; className?: string }) {
  return (
    <div className={cn("min-w-0 rounded-lg border border-line bg-surface px-4 py-3.5", className)}>
      <div className="label-caps truncate">{label}</div>
      <div className="mono mt-2 text-[22px] font-medium leading-none tracking-[-0.03em] tabular-nums text-fg">{value}</div>
      {sub && <div className="mt-2 truncate text-[11.5px] text-fg-3">{sub}</div>}
    </div>
  );
}

/** "← Workspaces" above a detail page. */
export function BackLink({ children, ...link }: { children: ReactNode } & React.ComponentProps<typeof Link>) {
  return (
    <Link
      {...link}
      className="inline-flex items-center gap-1 text-[12px] text-fg-3 transition-colors hover:text-fg [&_svg]:size-3.5"
    >
      <ChevronLeft />
      {children}
    </Link>
  );
}

/** A tinted banner across the top of a page body. */
export function Banner({ tone = "red", title, children, action }: { tone?: "red" | "amber" | "blue"; title: ReactNode; children?: ReactNode; action?: ReactNode }) {
  const cls = {
    red: "bg-tone-red-soft shadow-[inset_0_0_0_1px_var(--tone-red-line)] [&_[data-dot]]:bg-tone-red-solid",
    amber: "bg-tone-amber-soft shadow-[inset_0_0_0_1px_var(--tone-amber-line)] [&_[data-dot]]:bg-tone-amber-solid",
    blue: "bg-tone-blue-soft shadow-[inset_0_0_0_1px_var(--tone-blue-line)] [&_[data-dot]]:bg-tone-blue-solid",
  }[tone];
  return (
    <div role="status" className={cn("flex items-start gap-3 rounded-lg px-4 py-3", cls)}>
      <span data-dot aria-hidden className="mt-[7px] size-1.5 shrink-0 rounded-full" />
      <div className="min-w-0 flex-1">
        <p className="text-[12.5px] font-medium text-fg">{title}</p>
        {children && <div className="mt-0.5 text-[12.5px] leading-[18px] text-fg-2">{children}</div>}
      </div>
      {action && <div className="shrink-0">{action}</div>}
    </div>
  );
}

/** An inline error or notice under a form. */
export function Notice({ children, kind = "danger", className }: { children: ReactNode; kind?: "danger" | "info" | "ok"; className?: string }) {
  return (
    <div
      role={kind === "danger" ? "alert" : "status"}
      aria-live="polite"
      className={cn(
        "flex animate-rise-in items-start gap-2.5 rounded-md border px-3 py-2.5 text-[12.5px] leading-[18px] motion-reduce:animate-none",
        kind === "danger" ? "border-tone-red-line bg-tone-red-soft text-fg" : kind === "ok" ? "border-tone-green-line bg-tone-green-soft text-fg" : "border-line bg-sunken text-fg-2",
        className,
      )}
    >
      <span aria-hidden className={cn("mt-[6px] size-1.5 shrink-0 rounded-full", kind === "danger" ? "bg-tone-red-solid" : kind === "ok" ? "bg-tone-green-solid" : "bg-fg-3")} />
      <div className="min-w-0">{children}</div>
    </div>
  );
}

/** A detail page while it loads: a header and two columns. */
export function DetailSkeleton() {
  const { t } = useTranslation();
  return (
    <PageColumn>
      <div className="flex items-center gap-3" aria-busy="true" aria-label={t("loading")}>
        <Skeleton className="size-10 rounded-lg" />
        <div className="flex flex-col gap-2">
          <Skeleton className="h-4 w-48" />
          <Skeleton className="h-2.5 w-72" />
        </div>
      </div>
      <div className="grid gap-5 lg:grid-cols-[minmax(0,1fr)_340px]">
        <Skeleton className="h-64 rounded-lg" />
        <Skeleton className="h-64 rounded-lg" />
      </div>
    </PageColumn>
  );
}
