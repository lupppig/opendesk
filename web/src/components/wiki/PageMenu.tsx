// What a page's menus share, in the tree and in the page's header: "Move to…" (a page
// picked from a searchable list, or the top level; how pages move on touch screens,
// where the tree can't be dragged) and Delete, which says when only an admin can.

import { Command } from "cmdk";
import { ArrowUpToLine, Trash2 } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Dialog } from "@/components/ui/dialog";
import { MenuItem, MenuSeparator } from "@/components/ui/menu";
import { pickerSearch } from "@/components/ui/picker";
import type { WikiNode } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { ancestorsOf, moveTargets, moveToEnd } from "@/lib/wiki/tree";
import { useDeletePage, useDeleteRule, useExpanded, useMovePage, useTitle, useWikiTree } from "./hooks";
import { PageIcon } from "./WikiTree";

const row =
  "flex h-8 cursor-default items-center gap-2 rounded-md px-2 text-[12.5px] text-fg data-[selected=true]:bg-hover [@media(pointer:coarse)]:h-10 [&_svg]:size-3.5 [&_svg]:shrink-0";

/** Moves node under the page picked, last among its subpages; pages it can't go under are left out. */
export function MoveDialog({ node, open, onOpenChange }: { node: WikiNode | null; open: boolean; onOpenChange: (o: boolean) => void }) {
  const { t } = useTranslation("wiki");
  const title = useTitle();
  const { tree } = useWikiTree();
  const move = useMovePage();
  const { open: expand } = useExpanded();
  const [query, setQuery] = useState("");
  const targets = useMemo(() => (open && node ? moveTargets(tree, node.id) : []), [open, node, tree]);
  if (!node) return null;
  const here = tree.byId.get(node.id)?.parent_id ?? null;

  const close = () => {
    onOpenChange(false);
    setQuery("");
  };
  const choose = (parent: string | null) => {
    close();
    // Its new place opens in the tree, all the way down.
    if (parent) expand([...ancestorsOf(tree, parent).map((a) => a.id), parent]);
    move.mutate({ id: node.id, drop: moveToEnd(tree, node.id, parent) });
  };
  const hint = (id: string | null) => (id === here ? <span className="shrink-0 text-[11px] text-fg-4">{t("move.here")}</span> : null);

  return (
    <Dialog open={open} onOpenChange={(o) => (o ? onOpenChange(true) : close())} size="sm" title={t("move.title", { title: title(node.title) })} description={t("move.description")}>
      <Command loop className="-mx-2 flex flex-col">
        <div className="mx-2 rounded-md border border-line p-1">
          <Command.Input
            aria-label={t("move.search")}
            // A phone's keyboard would cover the list it opens over.
            autoFocus={open && !window.matchMedia?.("(pointer: coarse)").matches}
            value={query}
            onValueChange={setQuery}
            placeholder={t("move.search")}
            className={pickerSearch.input}
          />
        </div>
        <Command.List className="max-h-[min(360px,50vh)] overflow-y-auto px-1 pt-1.5">
          <Command.Empty className="px-2 py-5 text-center text-xs text-fg-3">{t("move.none")}</Command.Empty>
          <Command.Item value={`${t("move.top")} __top`} onSelect={() => choose(null)} className={cn(row, "text-fg-2")}>
            <ArrowUpToLine className="text-fg-4" />
            <span className="flex-1">{t("move.top")}</span>
            {hint(null)}
          </Command.Item>
          {targets.map(({ node: n, depth }) => {
            const path = ancestorsOf(tree, n.id).map((a) => title(a.title));
            return (
              <Command.Item key={n.id} value={`${title(n.title)} ${n.id}`} keywords={path} onSelect={() => choose(n.id)} className={row} style={query ? undefined : { paddingLeft: 8 + depth * 14 }}>
                <PageIcon icon={n.icon} />
                <span className="min-w-0 flex-1 truncate">
                  {title(n.title)}
                  {query && path.length > 0 && <span className="ml-1.5 text-[11.5px] text-fg-4">{path.join(" / ")}</span>}
                </span>
                {hint(n.id)}
              </Command.Item>
            );
          })}
        </Command.List>
      </Command>
    </Dialog>
  );
}

/** Delete in a page's menu: offered when it would pass, otherwise shown off with the reason. */
export function DeleteItem({ node }: { node: WikiNode }) {
  const { t } = useTranslation("wiki");
  const rule = useDeleteRule()(node.id);
  const del = useDeletePage();
  if (rule === "no") return null;
  return (
    <>
      <MenuSeparator />
      <MenuItem danger disabled={rule !== "yes"} icon={<Trash2 />} onSelect={() => del(node)}>
        {t("page.delete")}
      </MenuItem>
      {rule !== "yes" && <p className="max-w-[232px] px-2 pb-1.5 pt-0.5 text-[11.5px] leading-4 text-fg-3">{t(rule === "page" ? "delete.adminOnlyPage" : "delete.adminOnlySubpages")}</p>}
    </>
  );
}
