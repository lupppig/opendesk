// The wiki's home (/wiki). A new workspace gets a first-run state with starter pages;
// otherwise the pages edited lately and the top-level pages to start from.

import { Link } from "@tanstack/react-router";
import { ChevronRight, Plus } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useDocumentTitle } from "@/app/RootLayout";
import { Button } from "@/components/ui/button";
import { EmptyState, GhostList } from "@/components/ui/empty";
import { Kbd } from "@/components/ui/kbd";
import { Skeleton } from "@/components/ui/skeleton";
import { PageIcon } from "@/components/wiki/WikiTree";
import { useCreatePage, useTitle, useWikiTree } from "@/components/wiki/hooks";
import { TEMPLATES, templatePage, type TemplateId } from "@/components/wiki/templates";
import { relLong, fullDate } from "@/lib/format";
import { cn } from "@/lib/cn";
import { atLeast, useRole } from "@/lib/session";
import { ancestorsOf, childrenOf, descendantsOf } from "@/lib/wiki/tree";
import { PagesButton } from "./WikiLayout";

export default function WikiHome() {
  const { t } = useTranslation("wiki");
  const { tree, data, isLoading } = useWikiTree();
  const canEdit = atLeast(useRole(), "agent");
  const create = useCreatePage();
  const title = useTitle();
  useDocumentTitle(t("title"));

  const header = (
    <header className="flex h-12 shrink-0 items-center gap-2 border-b border-line px-4">
      <PagesButton />
      <h1 className="text-[13.5px] font-semibold tracking-[-0.01em] text-fg">{t("title")}</h1>
      {canEdit && !!data?.length && (
        <Button variant="primary" size="sm" className="ml-auto" icon={<Plus />} loading={create.isPending} onClick={() => create.mutate({})}>
          {t("tree.newPage")}
        </Button>
      )}
    </header>
  );

  if (isLoading)
    return (
      <div className="flex min-h-0 flex-1 flex-col">
        {header}
        <div className="mx-auto w-full max-w-[760px] px-6 pt-12">
          <Skeleton className="h-6 w-1/3" />
          <Skeleton className="mt-6 h-12 w-full" />
          <Skeleton className="mt-2 h-12 w-full" />
        </div>
      </div>
    );

  if (!data?.length)
    return (
      <div className="relative flex min-h-0 flex-1 flex-col">
        {header}
        <div className="relative flex min-h-0 flex-1 flex-col items-center overflow-y-auto px-6 pb-16 pt-[4vh]">
          <EmptyState
            className="min-h-0 w-full flex-none pb-2 pt-6"
            art="wiki"
            ghost={<GhostList rows={7} variant="table" />}
            title={t("empty.title")}
            description={canEdit ? t("empty.body") : t("empty.viewer")}
            action={
              canEdit ? (
                <Button variant="primary" icon={<Plus />} loading={create.isPending && !create.variables?.title} onClick={() => create.mutate({})}>
                  {t("empty.create")}
                </Button>
              ) : undefined
            }
            tips={canEdit ? [{ label: t("empty.tipNest") }, { label: t("empty.tipOllie") }] : undefined}
          />
          {canEdit && <Templates onPick={(id) => create.mutate(templatePage(t, id))} busy={create.isPending} />}
        </div>
      </div>
    );

  const recent = [...data].sort((a, b) => Date.parse(b.updated_at) - Date.parse(a.updated_at)).slice(0, 6);
  const top = childrenOf(tree, null);
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {header}
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto w-full max-w-[760px] px-6 pb-20 pt-10 animate-rise-in motion-reduce:animate-none">
          <p className="max-w-[560px] text-[13px] leading-5 text-fg-3">{t("home.intro")}</p>

          <h2 className="label-caps mb-2 mt-8">{t("home.recent")}</h2>
          <ul className="divide-y divide-line-soft overflow-hidden rounded-lg border border-line bg-surface">
            {recent.map((n) => {
              const path = ancestorsOf(tree, n.id);
              return (
                <li key={n.id}>
                  <Link to="/wiki/$pageId" params={{ pageId: n.id }} className="flex h-11 items-center gap-2.5 px-3.5 outline-none transition-colors hover:bg-hover focus-visible:bg-hover">
                    <PageIcon icon={n.icon} />
                    <span className={cn("min-w-0 truncate text-[13px] font-medium text-fg", !n.title.trim() && "text-fg-3")}>{title(n.title)}</span>
                    {path.length > 0 && <span className="hidden min-w-0 truncate text-xs text-fg-4 sm:inline">{path.map((p) => title(p.title)).join(" / ")}</span>}
                    <span className="ml-auto shrink-0 text-xs text-fg-4" title={fullDate(n.updated_at)}>
                      {relLong(n.updated_at)}
                    </span>
                  </Link>
                </li>
              );
            })}
          </ul>

          <h2 className="label-caps mb-2 mt-8">{t("home.all")}</h2>
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
            {top.map((n) => {
              const count = descendantsOf(tree, n.id).length;
              return (
                <Link
                  key={n.id}
                  to="/wiki/$pageId"
                  params={{ pageId: n.id }}
                  className="group flex items-center gap-3 rounded-lg border border-line bg-surface px-3.5 py-3 shadow-xs outline-none transition-[border-color,box-shadow] hover:border-line-strong focus-visible:shadow-[0_0_0_2px_var(--focus)]"
                >
                  <span className="flex size-8 shrink-0 items-center justify-center rounded-md bg-sunken text-[16px]">
                    <PageIcon icon={n.icon} className="size-4 text-[16px]" />
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className={cn("block truncate text-[13px] font-medium text-fg", !n.title.trim() && "text-fg-3")}>{title(n.title)}</span>
                    <span className="block text-xs text-fg-3">{count ? t("home.subpages", { count }) : t("home.noSubpages")}</span>
                  </span>
                  <ChevronRight className="size-4 shrink-0 text-fg-4 transition-transform group-hover:translate-x-0.5 motion-reduce:transition-none" />
                </Link>
              );
            })}
          </div>

          {canEdit && <Templates onPick={(id) => create.mutate(templatePage(t, id))} busy={create.isPending} compact />}
          <p className="mt-10 text-xs text-fg-4">
            {t("home.paletteHint")} <Kbd>mod k</Kbd>
          </p>
        </div>
      </div>
    </div>
  );
}

function Templates({ onPick, busy, compact }: { onPick: (id: TemplateId) => void; busy: boolean; compact?: boolean }) {
  const { t } = useTranslation("wiki");
  return (
    <section className={cn("w-full", compact ? "mt-10" : "relative z-10 mt-6 max-w-[640px]")} aria-labelledby="wiki-templates">
      <h2 id="wiki-templates" className={cn("label-caps mb-2", !compact && "text-center")}>
        {t("templates.heading")}
      </h2>
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
        {TEMPLATES.map((tpl) => (
          <button
            key={tpl.id}
            type="button"
            disabled={busy}
            onClick={() => onPick(tpl.id)}
            className="group flex flex-col items-start gap-1.5 rounded-lg border border-line bg-surface p-3 text-left shadow-xs outline-none transition-[border-color,box-shadow,transform] hover:border-line-strong active:scale-[0.99] disabled:opacity-60 focus-visible:shadow-[0_0_0_2px_var(--focus)] motion-reduce:transition-none"
          >
            <span className="text-[18px] leading-none" aria-hidden>
              {tpl.icon}
            </span>
            <span className="text-[13px] font-medium text-fg">{t(`templates.${tpl.id}.title`)}</span>
            <span className="text-xs leading-[17px] text-fg-3">{t(`templates.${tpl.id}.hint`)}</span>
          </button>
        ))}
      </div>
    </section>
  );
}
