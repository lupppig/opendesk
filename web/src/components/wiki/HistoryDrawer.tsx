// A page's history: its revisions, newest first, each a snapshot to read and put back.
// One person's edits a few minutes apart are one revision (contracts/wiki.md "History").

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RotateCcw } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Avatar } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { Drawer } from "@/components/ui/dialog";
import { EmptyState } from "@/components/ui/empty";
import { Skeleton } from "@/components/ui/skeleton";
import { useConfirm } from "@/components/ui/confirm";
import { toast } from "@/components/ui/toast";
import { api } from "@/lib/api/endpoints";
import type { WikiPage } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { fullDate, relLong } from "@/lib/format";
import { q, qk } from "@/lib/queries";
import { sanitizeHtml } from "@/lib/sanitize";
import { nodeOf, patchTree, useTitle } from "./hooks";

export function HistoryDrawer({ page, open, onOpenChange, canRestore, onRestored }: { page: WikiPage; open: boolean; onOpenChange: (o: boolean) => void; canRestore: boolean; onRestored: (p: WikiPage) => void }) {
  const { t } = useTranslation("wiki");
  const title = useTitle();
  const qc = useQueryClient();
  const confirm = useConfirm();
  const list = useQuery({ ...q.wikiRevisions(page.id), enabled: open });
  const [picked, setPicked] = useState<string | null>(null);
  const revs = list.data ?? [];
  const sel = revs.find((r) => r.id === picked) ?? revs[0];
  const rev = useQuery({ ...q.wikiRevision(page.id, sel?.id ?? ""), enabled: open && !!sel });
  const current = sel && revs[0]?.id === sel.id;

  const restore = useMutation({
    mutationFn: (rid: string) => api.wiki.restore(page.id, rid),
    onSuccess: (p) => {
      qc.setQueryData(qk.wikiPage(p.id), p);
      patchTree(qc, (nodes) => nodes.map((n) => (n.id === p.id ? nodeOf(p) : n)));
      void qc.invalidateQueries({ queryKey: qk.wikiRevisions(p.id) });
      onRestored(p);
      toast.success(t("history.restored"));
      onOpenChange(false);
    },
    onError: (e) => toast.error(e),
  });

  return (
    <Drawer open={open} onOpenChange={onOpenChange} title={t("history.title")} width={860}>
      {list.isLoading ? (
        <div className="flex flex-col gap-3 p-4">
          {[0, 1, 2, 3].map((i) => (
            <Skeleton key={i} className="h-9 w-full" />
          ))}
        </div>
      ) : !revs.length ? (
        <EmptyState compact art="wiki" title={t("history.empty")} description={t("history.emptyHint")} />
      ) : (
        <div className="flex h-full min-h-0 flex-col md:flex-row">
          <ol className="max-h-[38vh] shrink-0 overflow-y-auto border-b border-line p-2 md:max-h-none md:w-[260px] md:border-b-0 md:border-r" aria-label={t("history.list")}>
            {revs.map((r, i) => (
              <li key={r.id}>
                <button
                  type="button"
                  onClick={() => setPicked(r.id)}
                  aria-current={sel?.id === r.id ? "true" : undefined}
                  className={cn(
                    "flex w-full items-start gap-2.5 rounded-md px-2.5 py-2 text-left outline-none transition-colors focus-visible:shadow-[0_0_0_2px_var(--focus)]",
                    sel?.id === r.id ? "bg-active" : "hover:bg-hover",
                  )}
                >
                  <Avatar name={r.author?.name ?? "?"} src={r.author?.avatar_url} size="xs" className="mt-0.5" />
                  <span className="min-w-0 flex-1">
                    <span className="flex items-center gap-1.5 text-[12.5px] font-medium text-fg">
                      <span title={fullDate(r.updated_at)}>{relLong(r.updated_at)}</span>
                      {i === 0 && <span className="rounded-sm bg-tone-green-soft px-1 text-[10.5px] font-medium text-tone-green">{t("history.current")}</span>}
                    </span>
                    <span className="block truncate text-[11.5px] text-fg-3">{r.author?.name ?? t("history.api")}</span>
                  </span>
                </button>
              </li>
            ))}
          </ol>
          <div className="flex min-h-0 min-w-0 flex-1 flex-col">
            <div className="flex h-11 shrink-0 items-center gap-2 border-b border-line px-4">
              <span className="min-w-0 flex-1 truncate text-[12.5px] text-fg-3">
                {sel && t("history.by", { name: sel.author?.name ?? t("history.api"), when: fullDate(sel.updated_at) })}
              </span>
              {canRestore && sel && !current && (
                <Button
                  size="sm"
                  icon={<RotateCcw />}
                  loading={restore.isPending}
                  onClick={() =>
                    confirm.show(t("history.restoreBody"), () => restore.mutateAsync(sel.id), { title: t("history.restoreTitle"), confirmLabel: t("history.restore") })
                  }
                >
                  {t("history.restore")}
                </Button>
              )}
            </div>
            <div className="min-h-0 flex-1 overflow-y-auto px-6 pb-12 pt-6">
              {rev.data ? (
                <article>
                  <h1 className="flex items-center gap-2 text-[22px] font-semibold leading-[28px] tracking-[-0.025em] text-fg">
                    {rev.data.icon && <span aria-hidden>{rev.data.icon}</span>}
                    <span className={cn(!rev.data.title.trim() && "text-fg-3")}>{title(rev.data.title)}</span>
                  </h1>
                  {rev.data.body_html ? (
                    <div className="tiptap prose-od mt-4" dangerouslySetInnerHTML={{ __html: sanitizeHtml(rev.data.body_html) }} />
                  ) : (
                    <p className="mt-4 text-[13px] text-fg-3">{t("history.blank")}</p>
                  )}
                </article>
              ) : (
                <div className="flex flex-col gap-3">
                  <Skeleton className="h-6 w-1/2" />
                  <Skeleton className="h-3 w-full" />
                  <Skeleton className="h-3 w-5/6" />
                </div>
              )}
            </div>
          </div>
        </div>
      )}
    </Drawer>
  );
}
