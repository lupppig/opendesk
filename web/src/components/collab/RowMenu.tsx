// The sidebar row's own menu: which number it shows (with what each would show), pin it to
// Favorites, hide it, and an account's settings. It opens on right-click, long-press or the Menu key, and from the row's
// ⋯ button, which takes the number's place on hover and keyboard focus (not in the rail).
// Every member has it, with or without Collab; `enabled` false leaves the row as it is.
// Saved at once with PUT /v1/collab/sidebar.

import { Ellipsis, EyeOff, Pin, PinOff, Settings, SlidersHorizontal } from "lucide-react";
import { forwardRef, useRef, type ReactElement, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Slot } from "radix-ui";
import {
  ContextMenu,
  ContextMenuChoice,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuLabel,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import { Menu, MenuChoice, MenuContent, MenuItem, MenuLabel, MenuSeparator, MenuTrigger } from "@/components/ui/menu";
import { toast } from "@/components/ui/toast";
import type { MailFolder, SidebarBadge, SidebarPin, SidebarPrefs, SidebarSectionId } from "@/lib/api/types";
import { useSaveSidebarPrefs } from "@/lib/collab/hooks";
import {
  badgeChoices,
  badgeFor,
  badgeWord,
  isPinned,
  MAX_PINS,
  pickCount,
  setBadge,
  setHidden,
  togglePin,
  type BadgeKind,
  type BadgeNumbers,
} from "@/lib/collab/sidebarPrefs";
import { collabUi } from "@/lib/collab/ui";
import { canColor, LABEL_TONES, toneHex, toneOf, useSetLabelColor } from "./labelColor";

export type RowMenuProps = {
  /** Collab is on: without it the row has no menu. */
  enabled: boolean;
  prefs: SidebarPrefs;
  /** The row's number: its key, kind, the sections it follows, and what each choice would show. */
  badge?: { key: string; kind: BadgeKind; sections: SidebarSectionId[]; numbers?: BadgeNumbers };
  pin?: SidebarPin;
  /** Its hidden-id; left out where hiding makes no sense (a favorite is unpinned instead). */
  hideId?: string;
  /** The collapsed rail: no ⋯ button there (right-click still works). */
  rail?: boolean;
  /** The row's name, for the ⋯ button's label. */
  label?: string;
  /** A Gmail label or Outlook category: its colour is chosen here too. */
  labelColor?: { folder: MailFolder; provider: string };
  /** What the row needs done first (an account to reconnect), on top of the menu. */
  action?: { label: string; icon: ReactNode; onSelect: () => void };
  /** Opens what the row is set up in (an account's settings), beside Customize sidebar. */
  settings?: { label: string; onSelect: () => void };
  children: ReactElement;
} & Omit<React.HTMLAttributes<HTMLElement>, "children">;

type ItemProps = { children: ReactNode; icon?: ReactNode; onSelect?: () => void; disabled?: boolean };
type ChoiceProps = { children: ReactNode; checked: boolean; onSelect: () => void; hint?: ReactNode };

/** The menu's parts, so the right-click menu and the ⋯ dropdown list the same things. */
type Parts = {
  Label: (p: { children: ReactNode }) => ReactNode;
  Choice: (p: ChoiceProps) => ReactNode;
  Item: (p: ItemProps) => ReactNode;
  Separator: () => ReactNode;
};
const contextParts: Parts = { Label: ContextMenuLabel, Choice: ContextMenuChoice, Item: ContextMenuItem, Separator: ContextMenuSeparator };
const dropdownParts: Parts = { Label: MenuLabel, Choice: MenuChoice, Item: MenuItem, Separator: MenuSeparator };

/** Forwards props and ref to its one child, so it can sit inside the rail's tooltip. */
export const RowMenu = forwardRef<HTMLElement, RowMenuProps>(function RowMenu({ enabled, prefs, badge, pin, hideId, rail, label, labelColor, action, settings, children, ...rest }, ref) {
  const { t } = useTranslation("collab");
  const save = useSaveSidebarPrefs();
  const setColor = useSetLabelColor();
  // A choice made with the pointer leaves focus off the ⋯ button, so the row shows its new number at once.
  const byPointer = useRef(false);
  if (!enabled)
    return (
      <Slot.Root ref={ref} {...rest}>
        {children}
      </Slot.Root>
    );

  const change = (p: SidebarPrefs) => save.mutate(p, { onError: (e) => toast.error(e, t("customize.saveFailed")) });
  const current = badge ? badgeFor(prefs, badge.key, badge.sections, badge.kind) : null;
  const own = badge ? prefs.badges[badge.key] : undefined;
  const pinned = pin ? isPinned(prefs, pin) : false;

  const body = ({ Label, Choice, Item, Separator }: Parts) => (
    <>
      {action && (
        <>
          <Item icon={action.icon} onSelect={action.onSelect}>
            {action.label}
          </Item>
          <Separator />
        </>
      )}
      {badge && (
        <>
          <Label>{t("rowMenu.number")}</Label>
          {badgeChoices(badge.kind).map((v: SidebarBadge) => (
            <Choice
              key={v}
              checked={current === v}
              onSelect={() => change(setBadge(prefs, badge.key, v))}
              hint={v === "none" || !badge.numbers ? undefined : pickCount(v, badge.numbers)}
            >
              {v === "none" ? t("rowMenu.hideNumber") : t(`customize.badge.${badgeWord(v, badge.kind)}`)}
            </Choice>
          ))}
          {own && (
            <Choice checked={false} onSelect={() => change(setBadge(prefs, badge.key, null))}>
              {t("rowMenu.sameAsSection")}
            </Choice>
          )}
          <Separator />
        </>
      )}
      {labelColor && canColor(labelColor.folder, labelColor.provider) && (
        <>
          <Label>{t("labelColor.title")}</Label>
          {LABEL_TONES.map((x) => (
            <Choice key={x} checked={toneOf(labelColor.provider, labelColor.folder.color) === x}
              onSelect={() => setColor.mutate({ folder: labelColor.folder, tone: x, provider: labelColor.provider })}>
              <span className="flex items-center gap-2">
                <span aria-hidden className="size-2.5 rotate-45 rounded-[2px]" style={{ backgroundColor: toneHex(labelColor.provider, x) }} />
                {t(`labelColor.tone.${x}`)}
              </span>
            </Choice>
          ))}
          <Separator />
        </>
      )}
      {pin && (
        <Item icon={pinned ? <PinOff /> : <Pin />} disabled={!pinned && prefs.pinned.length >= MAX_PINS} onSelect={() => change(togglePin(prefs, pin))}>
          {pinned ? t("rowMenu.unpin") : t("rowMenu.pin")}
        </Item>
      )}
      {hideId && (
        <Item icon={<EyeOff />} onSelect={() => change(setHidden(prefs, hideId, true))}>
          {t("rowMenu.hide")}
        </Item>
      )}
      {(pin || hideId) && <Separator />}
      {settings && (
        <Item icon={<Settings />} onSelect={settings.onSelect}>
          {settings.label}
        </Item>
      )}
      <Item icon={<SlidersHorizontal />} onSelect={() => collabUi.customize(true)}>
        {t("rowMenu.customize")}
      </Item>
    </>
  );

  const context = (
    <ContextMenu>
      <ContextMenuTrigger ref={ref} {...rest}>
        {children}
      </ContextMenuTrigger>
      <ContextMenuContent>{body(contextParts)}</ContextMenuContent>
    </ContextMenu>
  );
  if (rail) return context;

  // The ⋯ button sits over the row's right edge, beside the link rather than in it.
  return (
    <div className="nav-row-menu">
      {context}
      <Menu onOpenChange={(open) => open && (byPointer.current = false)}>
        <MenuTrigger asChild>
          <button
            type="button"
            data-row-more=""
            aria-label={t("rowMenu.more", { name: label ?? "" })}
            className="absolute right-3.5 top-1/2 flex size-5 -translate-y-1/2 items-center justify-center rounded text-fg-3 outline-none transition-[opacity,color,background-color] duration-100 hover:bg-chrome-active hover:text-fg focus-visible:shadow-[0_0_0_2px_var(--focus)] data-[state=open]:bg-chrome-active data-[state=open]:text-fg motion-reduce:transition-none"
          >
            <Ellipsis className="size-3.5" />
          </button>
        </MenuTrigger>
        <MenuContent
          align="start"
          className="w-[210px]"
          onCloseAutoFocus={(e) => {
            if (byPointer.current) e.preventDefault();
          }}
        >
          <div onPointerUp={() => (byPointer.current = true)}>{body(dropdownParts)}</div>
        </MenuContent>
      </Menu>
    </div>
  );
});
