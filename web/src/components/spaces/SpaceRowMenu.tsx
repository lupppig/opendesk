// A Space row's own menu (contracts/spaces.md "Row menu"): its number in this Space, its
// name in this Space, the section it sits in, another Space to add it to, and taking it out.
// Like the sidebar's row menu it opens on right-click, long-press or the Menu key, and from
// the ⋯ button that takes the number's place on hover and focus (not in the rail).
// Rows inside a whole built-in section keep that section's own menu.

import { Check, ChevronRight, Ellipsis, FolderInput, Pencil, Plus, RotateCcw, SlidersHorizontal, SquareStack, X } from "lucide-react";
import { ContextMenu as CM, DropdownMenu as M } from "radix-ui";
import { forwardRef, useRef, type ReactElement, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { menuItemCls } from "@/components/ui/menu";
import { floatingPanel } from "@/components/ui/popover";
import type { Space, SpaceItem } from "@/lib/api/types";
import { badgeChoices, badgeWord, pickCount } from "@/lib/collab/sidebarPrefs";
import { cn } from "@/lib/cn";
import { addItems, addSection, hasItem, itemKey, moveItemTo, removeItem, replaceSpace, updateItem } from "@/lib/spaces/model";
import { spaceUi } from "@/lib/spaces/store";
import type { RowModel } from "./rows";

type Kit = {
  Item: typeof M.Item;
  Label: typeof M.Label;
  Separator: typeof M.Separator;
  CheckboxItem: typeof M.CheckboxItem;
  Indicator: typeof M.ItemIndicator;
  Sub: typeof M.Sub;
  SubTrigger: typeof M.SubTrigger;
  SubContent: typeof M.SubContent;
  Portal: typeof M.Portal;
};
const dropdown: Kit = { Item: M.Item, Label: M.Label, Separator: M.Separator, CheckboxItem: M.CheckboxItem, Indicator: M.ItemIndicator, Sub: M.Sub, SubTrigger: M.SubTrigger, SubContent: M.SubContent, Portal: M.Portal };
const context = { Item: CM.Item, Label: CM.Label, Separator: CM.Separator, CheckboxItem: CM.CheckboxItem, Indicator: CM.ItemIndicator, Sub: CM.Sub, SubTrigger: CM.SubTrigger, SubContent: CM.SubContent, Portal: CM.Portal } as unknown as Kit;

export type SpaceRowMenuProps = {
  spaces: Space[];
  space: Space;
  item: SpaceItem;
  model: RowModel;
  /** Saves the whole list. */
  put: (list: Space[]) => void;
  onRename: () => void;
  rail?: boolean;
  label: string;
  children: ReactElement;
} & Omit<React.HTMLAttributes<HTMLElement>, "children">;

/** Forwards props and ref to its one child, so it can sit inside the rail's tooltip. */
export const SpaceRowMenu = forwardRef<HTMLElement, SpaceRowMenuProps>(function SpaceRowMenu({ spaces, space, item, model, put, onRename, rail, label, children, ...rest }, ref) {
  const { t } = useTranslation("spaces");
  const { t: tc } = useTranslation("collab");
  const byPointer = useRef(false);
  const key = itemKey(item);
  const change = (next: Space) => put(replaceSpace(spaces, next));
  const others = spaces.filter((x) => x.id !== space.id);
  const sectionName = (title: string, i: number) => title.trim() || t("view.untitledN", { n: i + 1 });

  const body = (K: Kit) => {
    const itemEl = (icon: ReactNode, text: ReactNode, onSelect: () => void, disabled?: boolean) => (
      <K.Item disabled={disabled} onSelect={onSelect} className={menuItemCls}>
        {icon}
        <span className="flex-1 truncate">{text}</span>
      </K.Item>
    );
    const choice = (key: string, checked: boolean, text: ReactNode, onSelect: () => void, hint?: ReactNode) => (
      <K.CheckboxItem key={key} checked={checked} onSelect={onSelect} className={cn(menuItemCls, "pl-[30px]")}>
        <K.Indicator className="absolute left-2 flex">
          <Check className="!text-fg" />
        </K.Indicator>
        <span className="flex-1 truncate">{text}</span>
        {hint !== undefined && hint !== null && <span className="mono shrink-0 text-[11px] tabular-nums text-fg-4">{hint}</span>}
      </K.CheckboxItem>
    );
    const sub = (icon: ReactNode, text: ReactNode, children: ReactNode) => (
      <K.Sub>
        <K.SubTrigger className={cn(menuItemCls, "data-[state=open]:bg-hover")}>
          {icon}
          <span className="flex-1 truncate">{text}</span>
          <ChevronRight />
        </K.SubTrigger>
        <K.Portal>
          <K.SubContent sideOffset={6} collisionPadding={8} className={cn(floatingPanel, "max-h-[min(360px,var(--radix-popper-available-height))] min-w-[180px] overflow-y-auto p-1")}>
            {children}
          </K.SubContent>
        </K.Portal>
      </K.Sub>
    );
    const sep = <K.Separator className="-mx-1 my-1 h-px bg-line" />;
    const b = model.badge;
    return (
      <>
        {b && (
          <>
            <K.Label className="label-caps px-2 pb-1 pt-1.5">{t("row.number")}</K.Label>
            {choice("default", !item.badge, t("row.default"), () => change(updateItem(space, key, { badge: null })))}
            {badgeChoices(b.kind).map((v) =>
              choice(
                v,
                item.badge === v,
                v === "none" ? tc("rowMenu.hideNumber") : tc(`customize.badge.${badgeWord(v, b.kind)}`),
                () => change(updateItem(space, key, { badge: v })),
                v === "none" ? undefined : pickCount(v, b.nums),
              ),
            )}
            {sep}
          </>
        )}
        {itemEl(<Pencil />, t("row.rename"), onRename)}
        {item.name && itemEl(<RotateCcw />, t("row.resetName", { name: model.label }), () => change(updateItem(space, key, { name: null })))}
        {sub(
          <FolderInput />,
          t("row.moveTo"),
          <>
            {space.sections.map((sec, i) => choice(sec.id, sec.items.some((x) => itemKey(x) === key), sectionName(sec.title, i), () => change(moveItemTo(space, key, sec.id))))}
            {sep}
            {itemEl(<Plus />, t("row.newSection"), () => {
              const made = addSection(space, "");
              change(moveItemTo(made.space, key, made.id));
            })}
          </>,
        )}
        {others.length > 0 &&
          sub(
            <SquareStack />,
            t("row.alsoAdd"),
            others.map((s) => {
              const there = hasItem(s, item);
              return (
                <K.Item key={s.id} disabled={there} onSelect={() => put(replaceSpace(spaces, addItems(s, null, [{ workspace_id: item.workspace_id, kind: item.kind, id: item.id }])))} className={menuItemCls}>
                  <span className="flex-1 truncate">{s.name}</span>
                  {there && <span className="shrink-0 text-[11px] text-fg-4">{t("row.already")}</span>}
                </K.Item>
              );
            }),
          )}
        {itemEl(<X />, t("row.remove"), () => change(removeItem(space, key)))}
        {sep}
        {itemEl(<SlidersHorizontal />, t("row.edit"), () => spaceUi.edit({ kind: "space", id: space.id }))}
      </>
    );
  };

  const ctx = (
    <CM.Root>
      <CM.Trigger asChild ref={ref as React.Ref<HTMLSpanElement>} {...rest}>
        {children}
      </CM.Trigger>
      <CM.Portal>
        <CM.Content collisionPadding={8} className={cn(floatingPanel, "min-w-[200px] origin-[var(--radix-context-menu-content-transform-origin)] p-1 motion-reduce:animate-none")}>
          {body(context)}
        </CM.Content>
      </CM.Portal>
    </CM.Root>
  );
  if (rail) return ctx;
  return (
    <div className="nav-row-menu">
      {ctx}
      <M.Root onOpenChange={(open) => open && (byPointer.current = false)}>
        <M.Trigger asChild>
          <button
            type="button"
            data-row-more=""
            aria-label={t("row.more", { name: label })}
            className="absolute right-3.5 top-1/2 flex size-5 -translate-y-1/2 items-center justify-center rounded text-fg-3 outline-none transition-[opacity,color,background-color] duration-100 hover:bg-chrome-active hover:text-fg focus-visible:shadow-[0_0_0_2px_var(--focus)] data-[state=open]:bg-chrome-active data-[state=open]:text-fg motion-reduce:transition-none"
          >
            <Ellipsis className="size-3.5" />
          </button>
        </M.Trigger>
        <M.Portal>
          <M.Content
            align="start"
            sideOffset={6}
            collisionPadding={8}
            className={cn(floatingPanel, "w-[220px] origin-[var(--radix-dropdown-menu-content-transform-origin)] p-1")}
            onClick={(e) => e.stopPropagation()}
            onCloseAutoFocus={(e) => {
              if (byPointer.current) e.preventDefault();
            }}
          >
            <div onPointerUp={() => (byPointer.current = true)}>{body(dropdown)}</div>
          </M.Content>
        </M.Portal>
      </M.Root>
    </div>
  );
});

/** A Space row while it is renamed: its name in place, Enter saves, Escape leaves it. */
export function RenameRow({ initial, placeholder, label, onDone }: { initial: string; placeholder: string; label: string; onDone: (name: string | null) => void }) {
  const done = useRef(false);
  const finish = (v: string | null) => {
    if (done.current) return;
    done.current = true;
    onDone(v);
  };
  return (
    <div className="mx-2.5 flex h-7 w-[calc(100%-1.25rem)] items-center rounded-md bg-surface px-1 shadow-[0_0_0_1.5px_var(--focus)]">
      <input
        autoFocus
        defaultValue={initial}
        placeholder={placeholder}
        aria-label={label}
        maxLength={64}
        onFocus={(e) => e.currentTarget.select()}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            finish(e.currentTarget.value);
          } else if (e.key === "Escape") {
            e.preventDefault();
            e.stopPropagation();
            finish(null);
          }
        }}
        onBlur={(e) => finish(e.currentTarget.value)}
        className="h-6 min-w-0 flex-1 bg-transparent px-1 text-[12.5px] text-fg outline-none placeholder:text-fg-4"
      />
    </div>
  );
}
