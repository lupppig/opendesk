// The folder bar under the mail list's title: the member's own tabs (SidebarPrefs.mail_tabs:
// boxes, and folders or labels of the account in scope), the rest in More. Each tab's number
// follows the same choices as the sidebar (box:<box>, folder:<id>). It keeps every folder one
// click away, so the sidebar can stay a row per account.

import { Link, useNavigate } from "@tanstack/react-router";
import { ChevronDown, ChevronLeft, ChevronRight, Pin, SlidersHorizontal, X } from "lucide-react";
import { Fragment, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import {
  ContextMenu,
  ContextMenuChoice,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuLabel,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import { Menu, MenuContent, MenuItem, MenuLabel, MenuSeparator, MenuSub, MenuTrigger } from "@/components/ui/menu";
import { toast } from "@/components/ui/toast";
import { Tooltip } from "@/components/ui/tooltip";
import type { MailAccount, MailBox, MailCounts, MailFolder, SidebarBadge, SidebarPrefs } from "@/lib/api/types";
import { boxTabNumbers, folderNumbers, type RowNumbers } from "@/lib/collab/badges";
import { accountName, buildFolderTree, flattenWithout, hasBox, isTabBox, TAB_BOXES } from "@/lib/collab/boxes";
import { useSaveSidebarPrefs } from "@/lib/collab/hooks";
import { isTab, MAX_TABS, pickCount, setBadge, tabBadge, tabId, tabsFor, toggleTab, type MailTab } from "@/lib/collab/sidebarPrefs";
import { cn } from "@/lib/cn";
import { AccountDot, boxIcon, FolderGlyph } from "./bits";
import { CustomizeTabs } from "./CustomizeTabs";
import { canColor, LABEL_TONES, toneHex, toneOf, useSetLabelColor } from "./labelColor";

const tabCls =
  "relative flex h-9 shrink-0 items-center gap-1.5 px-2 text-[12.5px] outline-none transition-colors focus-visible:text-fg [&_svg]:size-3.5 [&_svg]:shrink-0";

function Underline({ active, children }: { active: boolean; children: ReactNode }) {
  return (
    <>
      {children}
      {active && <span aria-hidden className="absolute inset-x-2 -bottom-px h-[2px] rounded-full bg-fg" />}
    </>
  );
}

function TabCount({ n }: { n: number | undefined }) {
  if (!n) return null;
  return <span className="mono text-[11px] tabular-nums text-fg-4">{n > 999 ? "999+" : n}</span>;
}

/** One place on the bar: a tab, or the open box or folder that is not a tab. */
type BarItem = {
  id: string;
  label: string;
  title?: string;
  icon: ReactNode;
  count: number | undefined;
  active: boolean;
  link: { box: MailBox | "folder"; search: { account?: string; folder?: string } } | null;
  /** A label the tab is (it takes a colour from its menu). */
  folder?: MailFolder;
};

/** The tab's own menu: its number, removing it, and the dialog. */
function TabMenu({
  id,
  label,
  prefs,
  onChange,
  onCustomize,
  removable,
  colorFolder,
  provider,
  children,
}: {
  id: string;
  label: string;
  prefs: SidebarPrefs;
  onChange: (p: SidebarPrefs) => void;
  onCustomize: () => void;
  removable: boolean;
  /** A Gmail label or Outlook category: its colour is chosen here too. */
  colorFolder?: MailFolder;
  provider?: string;
  children: React.ReactElement;
}) {
  const { t } = useTranslation("collab");
  const setColor = useSetLabelColor();
  const colorable = canColor(colorFolder, provider);
  const tone = colorable ? toneOf(provider!, colorFolder!.color) : null;
  const key = id.startsWith("folder:") ? id : `box:${id}`;
  const current = tabBadge(prefs, id);
  const own = prefs.badges[key];
  const pick = (v: SidebarBadge | null) => onChange(setBadge(prefs, key, v));
  return (
    <ContextMenu>
      <ContextMenuTrigger>{children}</ContextMenuTrigger>
      <ContextMenuContent>
        <ContextMenuLabel>{label}</ContextMenuLabel>
        <ContextMenuChoice checked={current === "unread"} onSelect={() => pick("unread")}>
          {t("rowMenu.showUnread")}
        </ContextMenuChoice>
        <ContextMenuChoice checked={current === "total"} onSelect={() => pick("total")}>
          {t("rowMenu.showTotal")}
        </ContextMenuChoice>
        <ContextMenuChoice checked={current === "none"} onSelect={() => pick("none")}>
          {t("rowMenu.hideNumber")}
        </ContextMenuChoice>
        {own && (
          <ContextMenuChoice checked={false} onSelect={() => pick(null)}>
            {t("rowMenu.sameAsDefault")}
          </ContextMenuChoice>
        )}
        {colorable && (
          <>
            <ContextMenuSeparator />
            <ContextMenuLabel>{t("labelColor.title")}</ContextMenuLabel>
            {LABEL_TONES.map((x) => (
              <ContextMenuChoice key={x} checked={tone === x} onSelect={() => setColor.mutate({ folder: colorFolder!, tone: x, provider: provider! })}>
                <span className="flex items-center gap-2">
                  <span aria-hidden className="size-2.5 rotate-45 rounded-[2px]" style={{ backgroundColor: toneHex(provider!, x) }} />
                  {t(`labelColor.tone.${x}`)}
                </span>
              </ContextMenuChoice>
            ))}
          </>
        )}
        <ContextMenuSeparator />
        {removable && (
          <ContextMenuItem icon={<X />} onSelect={() => onChange(toggleTab(prefs, id))}>
            {t("bar.removeTab")}
          </ContextMenuItem>
        )}
        <ContextMenuItem icon={<SlidersHorizontal />} onSelect={onCustomize}>
          {t("bar.customize")}
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  );
}

/** Inside a More item: shows it as a tab in one click (the dialog does the same from the keyboard). */
function PinAsTab({ label, onPin, disabled }: { label: string; onPin: () => void; disabled?: boolean }) {
  const { t } = useTranslation("collab");
  if (disabled) return null;
  return (
    <Tooltip content={t("bar.pin", { name: label })} side="right">
      <button
        type="button"
        tabIndex={-1}
        aria-label={t("bar.pin", { name: label })}
        onClick={(e) => {
          // Not a pick: the item stays, and the menu stays open.
          e.preventDefault();
          e.stopPropagation();
          onPin();
        }}
        className="-mr-1 flex size-5 shrink-0 items-center justify-center rounded text-fg-4 opacity-0 transition-opacity hover:bg-active hover:text-fg group-data-[highlighted]/item:opacity-100 motion-reduce:transition-none [&_svg]:!size-3"
      >
        <Pin />
      </button>
    </Tooltip>
  );
}

export function FolderBar({
  box,
  account,
  folder,
  accounts,
  folderMap,
  counts,
  prefs,
}: {
  /** The route's box ("folder" when a folder is open). */
  box: string;
  /** The account the page is in, when it is one account. */
  account: MailAccount | undefined;
  /** The open folder or label. */
  folder: MailFolder | undefined;
  accounts: MailAccount[];
  folderMap: Record<string, MailFolder[] | undefined>;
  counts: MailCounts | undefined;
  prefs: SidebarPrefs;
}) {
  const { t } = useTranslation("collab");
  const navigate = useNavigate();
  const save = useSaveSidebarPrefs();
  const [customizing, setCustomizingState] = useState(false);
  // Opening the dialog from More: the menu must not take focus back from it.
  const opening = useRef(false);
  const setCustomizing = (o: boolean) => {
    opening.current = o;
    setCustomizingState(o);
  };
  const scope = account ?? (folder ? accounts.find((a) => a.id === folder.account_id) : undefined) ?? (accounts.length === 1 ? accounts[0] : undefined);
  const scopeId = account?.id ?? folder?.account_id;
  const folders = scope ? folderMap[scope.id] : undefined;
  const tabs = tabsFor(prefs, scope ? { account: scope, folders } : null);
  const shownIds = new Set(tabs.map((x) => x.id));
  const current = folder ? tabId.folder(folder.id) : box;
  // The open box or folder when it is not a tab: shown after the tabs while open.
  const extra: { id: string; label: string; icon: ReactNode; nums: RowNumbers } | null = shownIds.has(current)
    ? null
    : folder
      ? { id: tabId.folder(folder.id), label: folder.name, icon: <FolderGlyph folder={folder} className="!size-3.5 [&>span]:!size-[7px]" />, nums: folderNumbers(folder, counts) }
      : isTabBox(box)
        ? { id: box, label: t(`box.${box}`), icon: boxIcon[box], nums: boxTabNumbers(box, scope ? [scope] : accounts, folderMap, counts, !scope) }
        : null;
  const full = prefs.mail_tabs.length >= MAX_TABS;

  const change = (p: SidebarPrefs) => save.mutate(p, { onError: (e) => toast.error(e, t("tabs.saveFailed")) });
  const pin = (id: string) => change(toggleTab(prefs, id));
  const go = (b: MailBox | "folder", folderId?: string, accountId = scopeId) =>
    void navigate({ to: "/mail/$box/{-$threadId}", params: { box: b, threadId: undefined }, search: { account: accountId, folder: folderId } });

  const scopeAccounts = scope ? [scope] : accounts;
  const numbersOf = (x: MailTab): RowNumbers => (x.kind === "box" ? boxTabNumbers(x.box, scopeAccounts, folderMap, counts, !scope) : folderNumbers(x.folder, counts));
  const countOf = (x: MailTab) => pickCount(tabBadge(prefs, x.id), numbersOf(x));

  const moreBoxes = TAB_BOXES.filter((b) => !isTab(prefs, b) && (!scope || hasBox(scope, folders, b)));
  const folderItems = (list: MailFolder[] | undefined, accountId: string, pinnable: boolean) =>
    flattenWithout(buildFolderTree(list ?? []), (f) => pinnable && isTab(prefs, tabId.folder(f.id))).map(({ folder: f, depth, label, key }) => (
      <MenuItem key={key} icon={<FolderGlyph folder={f} />} onSelect={() => go("folder", f.id, accountId)}>
        <span className="flex items-center gap-2" style={depth ? { paddingLeft: depth * 12 } : undefined}>
          <span className="min-w-0 flex-1 truncate">{label}</span>
          {pinnable && <PinAsTab label={f.name} disabled={full} onPin={() => pin(tabId.folder(f.id))} />}
        </span>
      </MenuItem>
    ));
  const own = (scope ? [scope] : accounts).filter((a) => buildFolderTree(folderMap[a.id] ?? []).length > 0);
  const scopeFolderItems = scope ? folderItems(folders, scope.id, true) : [];

  const tabLabel = (x: MailTab) => (x.kind === "box" ? t(`box.${x.box}`) : x.folder.name);

  // Every tab, then the open box or folder when it is not one: what the bar would show with room.
  const items: BarItem[] = tabs.map((x) => ({
    id: x.id,
    label: tabLabel(x),
    title: x.kind === "folder" ? x.folder.path : undefined,
    icon: x.kind === "box" ? boxIcon[x.box] : <FolderGlyph folder={x.folder} className="!size-3.5 [&>span]:!size-[7px]" />,
    count: countOf(x),
    active: current === x.id,
    link: x.kind === "box" ? { box: x.box, search: { account: scopeId } } : { box: "folder" as const, search: { account: x.folder.account_id, folder: x.folder.id } },
    folder: x.kind === "folder" ? x.folder : undefined,
  }));
  if (extra)
    items.push({ id: extra.id, label: extra.label, icon: extra.icon, count: pickCount(tabBadge(prefs, extra.id), extra.nums), active: true, link: null, folder });
  const providerOf = (f: MailFolder | undefined) => (f ? accounts.find((a) => a.id === f.account_id)?.provider : undefined);

  // The tabs scroll sideways when they don't fit (a narrow list beside an open conversation,
  // or many tabs): arrows show while there is more on that side, the wheel scrolls too, and
  // the open tab is kept in view. More and the customize button stay put at the end.
  const scrollRef = useRef<HTMLDivElement>(null);
  const [edges, setEdges] = useState({ left: false, right: false });
  const itemsKey = items.map((x) => `${x.id}:${x.label}:${x.count ?? ""}`).join("|");
  const activeId = items.find((x) => x.active)?.id;
  useLayoutEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const update = () => setEdges({ left: el.scrollLeft > 1, right: el.scrollLeft + el.clientWidth < el.scrollWidth - 1 });
    update();
    el.addEventListener("scroll", update, { passive: true });
    const ro = new ResizeObserver(update);
    ro.observe(el);
    return () => {
      el.removeEventListener("scroll", update);
      ro.disconnect();
    };
  }, [itemsKey]);
  // Keep the open tab fully in view, clear of the faded edges; again once the arrows show,
  // since they narrow the strip.
  useLayoutEffect(() => {
    const el = scrollRef.current;
    const tab = el?.querySelector<HTMLElement>('[aria-current="page"]');
    if (!el || !tab) return;
    const pad = 20;
    const left = tab.offsetLeft - el.offsetLeft;
    if (left - pad < el.scrollLeft) el.scrollLeft = Math.max(0, left - pad);
    else if (left + tab.offsetWidth + pad > el.scrollLeft + el.clientWidth) el.scrollLeft = left + tab.offsetWidth + pad - el.clientWidth;
  }, [activeId, itemsKey, edges.left, edges.right]);
  const reduce = typeof window !== "undefined" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  const scrollBy = (dir: -1 | 1) => {
    const el = scrollRef.current;
    if (el) el.scrollBy({ left: dir * Math.max(120, el.clientWidth * 0.7), behavior: reduce ? "auto" : "smooth" });
  };
  const arrow = (dir: -1 | 1) => (
    <Tooltip content={dir < 0 ? t("bar.scrollLeft") : t("bar.scrollRight")}>
      <button
        type="button"
        aria-label={dir < 0 ? t("bar.scrollLeft") : t("bar.scrollRight")}
        onClick={() => scrollBy(dir)}
        className="flex size-6 shrink-0 items-center justify-center rounded-md text-fg-3 outline-none transition-colors hover:bg-hover hover:text-fg focus-visible:shadow-[0_0_0_2px_var(--focus)]"
      >
        {dir < 0 ? <ChevronLeft className="size-3.5" /> : <ChevronRight className="size-3.5" />}
      </button>
    </Tooltip>
  );

  return (
    <>
      <nav aria-label={t("bar.label")} className="group/bar flex h-9 shrink-0 items-center gap-0.5 border-b border-line px-2">
        {edges.left && arrow(-1)}
        <div
          ref={scrollRef}
          onWheel={(e) => {
            const el = scrollRef.current;
            if (el && Math.abs(e.deltaY) > Math.abs(e.deltaX) && el.scrollWidth > el.clientWidth) el.scrollLeft += e.deltaY;
          }}
          className={cn(
            "flex min-w-0 shrink items-center gap-0.5 overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden",
            edges.left && edges.right
              ? "[mask-image:linear-gradient(to_right,transparent,black_16px,black_calc(100%-16px),transparent)]"
              : edges.left
                ? "[mask-image:linear-gradient(to_right,transparent,black_16px)]"
                : edges.right && "[mask-image:linear-gradient(to_left,transparent,black_16px)]",
          )}
        >
          {items.map((x) =>
            x.link ? (
              <TabMenu key={x.id} id={x.id} label={x.label} prefs={prefs} onChange={change} onCustomize={() => setCustomizing(true)} removable colorFolder={x.folder} provider={providerOf(x.folder)}>
                <Link
                  to="/mail/$box/{-$threadId}"
                  params={{ box: x.link.box, threadId: undefined }}
                  search={x.link.search}
                  aria-current={x.active ? "page" : undefined}
                  title={x.title}
                  className={cn(tabCls, x.active ? "font-medium text-fg" : "text-fg-3 hover:text-fg-2")}
                >
                  <Underline active={x.active}>
                    {x.icon}
                    <span className="max-w-[160px] truncate">{x.label}</span>
                    <TabCount n={x.count} />
                  </Underline>
                </Link>
              </TabMenu>
            ) : (
              <TabMenu
                key={x.id}
                id={x.id}
                label={x.label}
                prefs={prefs}
                onChange={change}
                onCustomize={() => setCustomizing(true)}
                removable={false}
                colorFolder={x.folder}
                provider={providerOf(x.folder)}
              >
                <span aria-current="page" tabIndex={-1} className={cn(tabCls, "font-medium text-fg")}>
                  <Underline active>
                    {x.icon}
                    <span className="max-w-[160px] truncate">{x.label}</span>
                    <TabCount n={x.count} />
                  </Underline>
                </span>
              </TabMenu>
            ),
          )}
        </div>
        {edges.right && arrow(1)}
        <Menu>
          <MenuTrigger asChild>
            <button type="button" className={cn(tabCls, "text-fg-3 hover:text-fg-2 data-[state=open]:text-fg")}>
              {t("bar.more")}
              <ChevronDown className="!size-3" />
            </button>
          </MenuTrigger>
          <MenuContent
            align="start"
            className="max-h-[min(480px,var(--radix-dropdown-menu-content-available-height))] w-[232px] overflow-y-auto"
            onCloseAutoFocus={(e) => {
              if (opening.current) e.preventDefault();
            }}
          >
            {moreBoxes.map((b) => (
              <MenuItem key={b} icon={boxIcon[b]} onSelect={() => go(b)}>
                <span className="flex items-center gap-2">
                  <span className="min-w-0 flex-1 truncate">{t(`box.${b}`)}</span>
                  <PinAsTab label={t(`box.${b}`)} disabled={full} onPin={() => pin(b)} />
                </span>
              </MenuItem>
            ))}
            {(scope ? scopeFolderItems.length > 0 : own.length > 0) && <MenuSeparator />}
            {scope
              ? scopeFolderItems.length > 0 && (
                  <>
                    <MenuLabel>{t("bar.folders")}</MenuLabel>
                    {scopeFolderItems}
                  </>
                )
              : own.map((a) => (
                  <Fragment key={a.id}>
                    <MenuSub label={accountName(a)} icon={<AccountDot color={a.color} className="!size-2" />}>
                      {folderItems(folderMap[a.id], a.id, false)}
                    </MenuSub>
                  </Fragment>
                ))}
            <MenuSeparator />
            <MenuItem icon={<SlidersHorizontal />} onSelect={() => setCustomizing(true)}>
              {t("bar.customize")}
            </MenuItem>
          </MenuContent>
        </Menu>
        <Tooltip content={t("bar.customize")}>
          <button
            type="button"
            aria-label={t("bar.customize")}
            onClick={() => setCustomizing(true)}
            className="ml-auto flex size-6 shrink-0 items-center justify-center rounded-md text-fg-4 opacity-0 outline-none transition-[opacity,color,background-color] hover:bg-hover hover:text-fg-2 focus-visible:opacity-100 focus-visible:shadow-[0_0_0_2px_var(--focus)] group-hover/bar:opacity-100 motion-reduce:transition-none"
          >
            <SlidersHorizontal className="size-3.5" />
          </button>
        </Tooltip>
      </nav>
      <CustomizeTabs open={customizing} onOpenChange={setCustomizing} prefs={prefs} onChange={change} scope={scope} accounts={accounts} folderMap={folderMap} />
    </>
  );
}
