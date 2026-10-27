// The wiki's page tree, beside every wiki page: search on top, then the pages, nested.
// Rows expand and collapse, "+" adds a subpage, the menu copies a link, moves (Move to…,
// also on touch screens) or deletes, and pages drag onto, above or below each other to
// nest and reorder. Keyboard: arrows move
// through the rows (Right opens, Left closes or goes up), Enter opens a page, and
// Alt+arrows move the focused page up, down, in under the one above, or out.

import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { ChevronRight, FileText, FolderInput, Link2, MoreHorizontal, Plus } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type DragEvent, type KeyboardEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { SearchInput } from "@/components/ui/input";
import { Menu, MenuContent, MenuItem, MenuTrigger } from "@/components/ui/menu";
import { Skeleton } from "@/components/ui/skeleton";
import { Tooltip } from "@/components/ui/tooltip";
import { copyText } from "@/components/ui/copy-button";
import { toast } from "@/components/ui/toast";
import { useDebounced } from "@/components/contacts/hooks";
import type { WikiNode, WikiSearchHit } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { q } from "@/lib/queries";
import { atLeast, useRole } from "@/lib/session";
import { ancestorsOf, childrenOf, dropFor, keyMove, visibleRows, type Tree, type Zone } from "@/lib/wiki/tree";
import { useCreatePage, useExpanded, useMovePage, useOpenPageId, useTitle, useWikiTree } from "./hooks";
import { DeleteItem, MoveDialog } from "./PageMenu";

export function pageUrl(id: string) {
  return `${location.origin}/wiki/${id}`;
}

export function PageIcon({ icon, className }: { icon: string; className?: string }) {
  return icon ? (
    <span className={cn("flex size-4 shrink-0 items-center justify-center text-[13px] leading-none", className)} aria-hidden>
      {icon}
    </span>
  ) : (
    <FileText className={cn("size-[14px] shrink-0 text-fg-4", className)} aria-hidden />
  );
}

export function WikiTree({ onNavigate, autoFocusSearch }: { onNavigate?: () => void; autoFocusSearch?: boolean }) {
  const { t } = useTranslation("wiki");
  const { tree, isLoading, data } = useWikiTree();
  const canEdit = atLeast(useRole(), "agent");
  const create = useCreatePage();
  const [term, setTerm] = useState("");
  const debounced = useDebounced(term.trim(), 180);
  const searchRef = useRef<HTMLInputElement>(null);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex items-center gap-1.5 px-2.5 pb-2 pt-3">
        <SearchInput
          inputRef={searchRef}
          autoFocus={autoFocusSearch}
          value={term}
          onChange={setTerm}
          placeholder={t("tree.search")}
          className="min-w-0 flex-1"
          onKeyDown={(e) => {
            if (e.key === "Escape" && term) {
              e.preventDefault();
              e.stopPropagation();
              setTerm("");
            }
            if (e.key === "ArrowDown") {
              e.preventDefault();
              (e.currentTarget.closest("[data-wiki-panel]")?.querySelector<HTMLElement>("[data-wiki-row]") ?? null)?.focus();
            }
          }}
        />
        {canEdit && (
          <Tooltip content={t("tree.newPage")}>
            <Button square variant="ghost" size="sm" aria-label={t("tree.newPage")} loading={create.isPending && !create.variables?.parent_id} onClick={() => create.mutate({})}>
              <Plus className="size-4" />
            </Button>
          </Tooltip>
        )}
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto px-1.5 pb-6" data-wiki-panel>
        {debounced ? (
          <SearchResults term={debounced} onNavigate={onNavigate} />
        ) : isLoading ? (
          <div className="flex flex-col gap-2 px-2 pt-1">
            {[70, 52, 64, 40, 58].map((w, i) => (
              <Skeleton key={i} className="h-3.5" style={{ width: `${w}%`, marginLeft: i % 3 === 1 ? 16 : 0 }} />
            ))}
          </div>
        ) : data?.length ? (
          <TreeView tree={tree} canEdit={canEdit} onNavigate={onNavigate} onSearch={() => searchRef.current?.focus()} />
        ) : (
          <p className="px-2.5 pt-2 text-[12.5px] leading-[18px] text-fg-3">{t("tree.empty")}</p>
        )}
      </div>
    </div>
  );
}

// ─── Search ────────────────────────────────────────────────────────────────

function SearchResults({ term, onNavigate }: { term: string; onNavigate?: () => void }) {
  const { t } = useTranslation("wiki");
  const { data, isFetching, isError } = useQuery(q.wikiSearch(term));
  const title = useTitle();
  const openId = useOpenPageId();
  if (!data) return <p className="px-2.5 pt-2 text-[12.5px] text-fg-3">{isError ? t("tree.searchFailed") : t("tree.searching")}</p>;
  if (!data.length) return <p className="px-2.5 pt-2 text-[12.5px] text-fg-3">{t("tree.noMatches", { term })}</p>;
  return (
    <ul className={cn("flex flex-col gap-px transition-opacity", isFetching && "opacity-70")} aria-label={t("tree.results")}>
      {data.map((h) => (
        <li key={h.id}>
          <Link
            to="/wiki/$pageId"
            params={{ pageId: h.id }}
            onClick={onNavigate}
            data-wiki-row
            className={cn(
              "block rounded-md px-2 py-1.5 outline-none transition-colors hover:bg-hover focus-visible:bg-hover focus-visible:shadow-[0_0_0_2px_var(--focus)]",
              h.id === openId && "bg-active",
            )}
            onKeyDown={(e) => moveFocus(e)}
          >
            <span className="flex items-center gap-2 text-[12.5px] font-medium text-fg">
              <PageIcon icon={h.icon} />
              <span className="truncate">
                <Highlight text={title(h.title)} term={term} />
              </span>
            </span>
            {h.path.length > 0 && <span className="mt-0.5 block truncate pl-6 text-[11.5px] text-fg-4">{h.path.map((p) => title(p.title)).join(" / ")}</span>}
            {h.snippet && <Snippet hit={h} term={term} />}
          </Link>
        </li>
      ))}
    </ul>
  );
}

function Snippet({ hit, term }: { hit: WikiSearchHit; term: string }) {
  return (
    <span className="mt-0.5 line-clamp-2 pl-6 text-[11.5px] leading-[16px] text-fg-3">
      <Highlight text={hit.snippet} term={term} />
    </span>
  );
}

/** Marks the words of term (as prefixes) in text. */
export function Highlight({ text, term }: { text: string; term: string }) {
  const words = term.toLowerCase().match(/[\p{L}\p{N}]+/gu) ?? [];
  if (!words.length) return <>{text}</>;
  const re = new RegExp(`(${words.map((w) => w.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")).join("|")})`, "giu");
  const parts = text.split(re);
  return (
    <>
      {parts.map((p, i) =>
        i % 2 ? (
          <mark key={i} className="rounded-[3px] bg-tone-amber-soft px-px text-fg">
            {p}
          </mark>
        ) : (
          p
        ),
      )}
    </>
  );
}

function moveFocus(e: KeyboardEvent<HTMLElement>) {
  if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
  e.preventDefault();
  const rows = [...(e.currentTarget.closest("[data-wiki-panel]")?.querySelectorAll<HTMLElement>("[data-wiki-row]") ?? [])];
  const i = rows.indexOf(e.currentTarget);
  rows[e.key === "ArrowDown" ? i + 1 : i - 1]?.focus();
}

// ─── Tree ──────────────────────────────────────────────────────────────────

type Over = { id: string; zone: Zone } | { id: "__end"; zone: "after" };

function TreeView({ tree, canEdit, onNavigate, onSearch }: { tree: Tree; canEdit: boolean; onNavigate?: () => void; onSearch: () => void }) {
  const { t } = useTranslation("wiki");
  const { expanded, toggle, open } = useExpanded();
  const openId = useOpenPageId();
  const navigate = useNavigate();
  const move = useMovePage();
  const [focusId, setFocusId] = useState<string | null>(null);
  const [drag, setDrag] = useState<string | null>(null);
  const [over, setOver] = useState<Over | null>(null);
  const [moving, setMoving] = useState<WikiNode | null>(null);
  const hoverTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const root = useRef<HTMLDivElement>(null);

  // The open page's ancestors stay open, so it is always in view.
  const ancestorIds = useMemo(() => (openId ? ancestorsOf(tree, openId).map((a) => a.id) : []), [tree, openId]);
  useEffect(() => {
    if (ancestorIds.length) open(ancestorIds);
  }, [ancestorIds, open]);
  useEffect(() => {
    if (!openId) return;
    root.current?.querySelector<HTMLElement>(`[data-id="${openId}"]`)?.scrollIntoView({ block: "nearest" });
  }, [openId]);

  const rows = visibleRows(tree, expanded);
  const current = focusId && rows.some((r) => r.node.id === focusId) ? focusId : (openId && rows.some((r) => r.node.id === openId) ? openId : rows[0]?.node.id);

  const focusRow = (id: string | undefined) => {
    if (!id) return;
    setFocusId(id);
    requestAnimationFrame(() => root.current?.querySelector<HTMLElement>(`[data-id="${id}"]`)?.focus());
  };

  const doMove = (id: string, drop: { parent: string | null; position: number } | null) => {
    if (!drop) return;
    if (drop.parent) open([drop.parent]);
    move.mutate({ id, drop });
  };

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    const id = (e.target as HTMLElement).closest<HTMLElement>("[data-id]")?.dataset.id;
    if (!id || (e.target as HTMLElement).closest("button")) return;
    const i = rows.findIndex((r) => r.node.id === id);
    const n = tree.byId.get(id);
    if (!n || i < 0) return;
    const hasKids = childrenOf(tree, id).length > 0;
    if (e.altKey && canEdit && ["ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight"].includes(e.key)) {
      e.preventDefault();
      const dir = ({ ArrowUp: "up", ArrowDown: "down", ArrowRight: "in", ArrowLeft: "out" } as const)[e.key as "ArrowUp"];
      doMove(id, keyMove(tree, id, dir));
      focusRow(id);
      return;
    }
    switch (e.key) {
      case "ArrowDown":
        e.preventDefault();
        focusRow(rows[i + 1]?.node.id);
        break;
      case "ArrowUp":
        e.preventDefault();
        if (i === 0) onSearch();
        else focusRow(rows[i - 1]?.node.id);
        break;
      case "ArrowRight":
        e.preventDefault();
        if (hasKids && !expanded.has(id)) toggle(id, true);
        else if (hasKids) focusRow(childrenOf(tree, id)[0]?.id);
        break;
      case "ArrowLeft":
        e.preventDefault();
        if (hasKids && expanded.has(id)) toggle(id, false);
        else if (n.parent_id) focusRow(n.parent_id);
        break;
      case "Home":
        e.preventDefault();
        focusRow(rows[0]?.node.id);
        break;
      case "End":
        e.preventDefault();
        focusRow(rows[rows.length - 1]?.node.id);
        break;
      case "Enter":
        e.preventDefault();
        void navigate({ to: "/wiki/$pageId", params: { pageId: id } });
        onNavigate?.();
        break;
    }
  };

  const zoneOf = (e: DragEvent<HTMLElement>, id: string): Zone => {
    const r = e.currentTarget.getBoundingClientRect();
    const y = (e.clientY - r.top) / r.height;
    if (y < 0.28) return "before";
    if (y > 0.72) return childrenOf(tree, id).length && expanded.has(id) ? "inside" : "after";
    return "inside";
  };

  const dnd = (n: WikiNode) =>
    canEdit
      ? {
          draggable: true,
          onDragStart: (e: DragEvent<HTMLElement>) => {
            e.dataTransfer.effectAllowed = "move";
            e.dataTransfer.setData("text/plain", pageUrl(n.id));
            setDrag(n.id);
          },
          onDragEnd: () => {
            clearTimeout(hoverTimer.current);
            setDrag(null);
            setOver(null);
          },
          onDragOver: (e: DragEvent<HTMLElement>) => {
            if (!drag) return;
            let zone = zoneOf(e, n.id);
            if (!dropFor(tree, drag, n.id, zone)) zone = zone === "inside" ? "after" : zone;
            if (!dropFor(tree, drag, n.id, zone)) return setOver(null);
            e.preventDefault();
            e.dataTransfer.dropEffect = "move";
            if (over?.id !== n.id || over.zone !== zone) {
              setOver({ id: n.id, zone });
              clearTimeout(hoverTimer.current);
              // Hovering in the middle of a closed page opens it, to drop deeper.
              if (zone === "inside" && childrenOf(tree, n.id).length && !expanded.has(n.id)) hoverTimer.current = setTimeout(() => toggle(n.id, true), 650);
            }
          },
          onDrop: (e: DragEvent<HTMLElement>) => {
            e.preventDefault();
            clearTimeout(hoverTimer.current);
            if (drag && over && over.id === n.id) doMove(drag, dropFor(tree, drag, n.id, over.zone));
            setDrag(null);
            setOver(null);
          },
        }
      : {};

  const top = childrenOf(tree, null);
  return (
    <>
      <div ref={root} role="tree" aria-label={t("tree.label")} onKeyDown={onKeyDown} className="flex flex-col">
        {rows.map(({ node: n, depth }) => (
          <TreeRow
            key={n.id}
            node={n}
            depth={depth}
            hasKids={childrenOf(tree, n.id).length > 0}
            open={expanded.has(n.id)}
            active={n.id === openId}
            focusable={n.id === current}
            dragging={drag === n.id || (!!drag && ancestorsOf(tree, n.id).some((a) => a.id === drag))}
            over={over && over.id === n.id ? over.zone : null}
            canEdit={canEdit}
            onToggle={() => toggle(n.id)}
            onFocus={() => setFocusId(n.id)}
            onNavigate={onNavigate}
            onMoveTo={() => setMoving(n)}
            dnd={dnd(n)}
          />
        ))}
        {canEdit && drag && (
          // Below the last page: the end of the top level.
          <div
            className={cn("relative h-8 rounded-md", over?.id === "__end" && "before:absolute before:inset-x-2 before:top-1 before:h-0.5 before:rounded-full before:bg-tone-blue-solid")}
            onDragOver={(e) => {
              e.preventDefault();
              setOver({ id: "__end", zone: "after" });
            }}
            onDragLeave={() => setOver(null)}
            onDrop={(e) => {
              e.preventDefault();
              if (drag) doMove(drag, { parent: null, position: top.filter((x) => x.id !== drag).length });
              setDrag(null);
              setOver(null);
            }}
          />
        )}
      </div>
      <MoveDialog node={moving} open={!!moving} onOpenChange={(o) => !o && setMoving(null)} />
    </>
  );
}

function TreeRow({
  node: n,
  depth,
  hasKids,
  open,
  active,
  focusable,
  dragging,
  over,
  canEdit,
  onToggle,
  onFocus,
  onNavigate,
  onMoveTo,
  dnd,
}: {
  node: WikiNode;
  depth: number;
  hasKids: boolean;
  open: boolean;
  active: boolean;
  focusable: boolean;
  dragging: boolean;
  over: Zone | null;
  canEdit: boolean;
  onToggle: () => void;
  onFocus: () => void;
  onNavigate?: () => void;
  onMoveTo: () => void;
  dnd: Record<string, unknown>;
}) {
  const { t } = useTranslation("wiki");
  const title = useTitle();
  const create = useCreatePage();
  const [menu, setMenu] = useState(false);
  const indent = 6 + depth * 14;
  const line = (where: "top" | "bottom"): ReactNode => (
    <span
      aria-hidden
      className={cn("pointer-events-none absolute right-1 z-10 h-0.5 rounded-full bg-tone-blue-solid", where === "top" ? "-top-px" : "-bottom-px")}
      style={{ left: indent + 18 }}
    />
  );
  return (
    <div
      role="treeitem"
      aria-level={depth + 1}
      aria-expanded={hasKids ? open : undefined}
      aria-selected={active}
      aria-label={title(n.title)}
      data-id={n.id}
      data-wiki-row
      tabIndex={focusable ? 0 : -1}
      onFocus={onFocus}
      {...dnd}
      className={cn(
        "group relative flex h-7 shrink-0 select-none items-center gap-1 rounded-md pr-1 text-[12.5px] outline-none transition-colors focus-visible:shadow-[0_0_0_2px_var(--focus)]",
        active ? "bg-active font-medium text-fg" : "text-fg-2 hover:bg-hover hover:text-fg",
        dragging && "opacity-40",
        over === "inside" && "bg-tone-blue-soft shadow-[inset_0_0_0_1px_var(--tone-blue-solid)]",
      )}
      style={{ paddingLeft: indent }}
    >
      {over === "before" && line("top")}
      {over === "after" && line("bottom")}
      <button
        type="button"
        tabIndex={-1}
        aria-label={open ? t("tree.collapse") : t("tree.expand")}
        onClick={(e) => {
          e.stopPropagation();
          onToggle();
        }}
        className={cn("flex size-4 shrink-0 items-center justify-center rounded-sm text-fg-4 transition-colors hover:bg-active hover:text-fg-2", !hasKids && "invisible")}
      >
        <ChevronRight className={cn("size-3 transition-transform duration-150 motion-reduce:transition-none", open && "rotate-90")} />
      </button>
      <Link
        to="/wiki/$pageId"
        params={{ pageId: n.id }}
        tabIndex={-1}
        draggable={false}
        onClick={onNavigate}
        className="flex min-w-0 flex-1 items-center gap-1.5 self-stretch outline-none"
      >
        <PageIcon icon={n.icon} />
        <span className={cn("truncate", !n.title.trim() && "text-fg-3")}>{title(n.title)}</span>
      </Link>
      <span className={cn("flex shrink-0 items-center gap-px transition-opacity", menu ? "opacity-100" : "opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 [@media(hover:none)]:opacity-100")}>
        <Menu open={menu} onOpenChange={setMenu}>
          <MenuTrigger asChild>
            <button type="button" tabIndex={-1} aria-label={t("tree.actions", { title: title(n.title) })} className="flex size-5 items-center justify-center rounded-sm text-fg-3 hover:bg-active hover:text-fg">
              <MoreHorizontal className="size-3.5" />
            </button>
          </MenuTrigger>
          <MenuContent align="start">
            {canEdit && (
              <>
                <MenuItem icon={<Plus />} onSelect={() => create.mutate({ parent_id: n.id })}>
                  {t("tree.addSubpage")}
                </MenuItem>
                <MenuItem icon={<FolderInput />} onSelect={onMoveTo}>
                  {t("tree.moveTo")}
                </MenuItem>
              </>
            )}
            <MenuItem
              icon={<Link2 />}
              onSelect={() => {
                void copyText(pageUrl(n.id));
                toast.success(t("toast.linkCopied"));
              }}
            >
              {t("page.copyLink")}
            </MenuItem>
            <DeleteItem node={n} />
          </MenuContent>
        </Menu>
        {canEdit && (
          <Tooltip content={t("tree.addSubpage")}>
            <button
              type="button"
              tabIndex={-1}
              aria-label={t("tree.addSubpageTo", { title: title(n.title) })}
              onClick={(e) => {
                e.stopPropagation();
                create.mutate({ parent_id: n.id });
              }}
              className="flex size-5 items-center justify-center rounded-sm text-fg-3 hover:bg-active hover:text-fg"
            >
              <Plus className="size-3.5" />
            </button>
          </Tooltip>
        )}
      </span>
    </div>
  );
}
