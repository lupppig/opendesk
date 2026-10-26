// The Spaces in the workspace switcher (contracts/spaces.md "Spaces in the switcher"): each
// Space picks with a click or Alt+n, and has its own menu (Edit, Duplicate, Move up, Move down,
// Delete). New Space starts the editor blank, from a workspace's own sidebar, or from a
// workspace's starter Space. With no Spaces yet the group is just New Space, to be found.

import { useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowUp, Check, Copy, Ellipsis, Pencil, Plus, Square, Trash2 } from "lucide-react";
import { DropdownMenu as M } from "radix-ui";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { WorkspaceBadge } from "@/components/workspaces/WorkspaceBadge";
import { useConfirm } from "@/components/ui/confirm";
import { Kbd } from "@/components/ui/kbd";
import { MenuItem, MenuLabel, MenuSeparator, MenuSub, menuItemCls } from "@/components/ui/menu";
import { floatingPanel } from "@/components/ui/popover";
import type { Space } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { useSession } from "@/lib/session";
import { useWorkspaces } from "@/lib/workspaces";
import { useStarterSpaces } from "@/lib/spaces/hooks";
import { LIMITS } from "@/lib/spaces/model";
import { spaceKeys, useSpaceActions } from "./actions";
import { SpaceGlyph } from "./icons";

/** The starter Spaces of the person's workspaces: read when the switcher first opens, and again on every later open. */
export function useSwitcherStarters() {
  const qc = useQueryClient();
  const memberships = useSession((s) => s.memberships);
  const [asked, setAsked] = useState(false);
  const starters = useStarterSpaces(
    memberships.map((m) => m.workspace.id),
    asked,
  );
  const list = memberships.flatMap((m) => {
    const s = starters.get(m.workspace.id);
    return s ? [{ space: s, workspace: m.workspace.name, logo: m.workspace.avatar_url }] : [];
  });
  const onOpen = (open: boolean) => {
    if (!open) return;
    if (!asked) setAsked(true);
    else void qc.invalidateQueries({ predicate: (q) => q.queryKey[0] === "ws" && q.queryKey[2] === "starter" });
  };
  return { list, onOpen };
}

function SpaceEntry({ space: s, n, picked, spaces, actions }: { space: Space; n: number; picked: boolean; spaces: Space[]; actions: ReturnType<typeof useSpaceActions> }) {
  const { t } = useTranslation("spaces");
  const confirm = useConfirm();
  const i = spaces.findIndex((x) => x.id === s.id);
  const sub = (icon: ReactNode, label: ReactNode, onSelect: () => void, opts: { disabled?: boolean; danger?: boolean } = {}) => (
    <MenuItem icon={icon} onSelect={onSelect} disabled={opts.disabled} danger={opts.danger}>
      {label}
    </MenuItem>
  );
  return (
    <div className="group/space relative">
      <M.Item onSelect={() => actions.pick(s.id)} className={cn(menuItemCls, "pr-9")}>
        <SpaceGlyph icon={s.icon} name={s.name} color={s.color} size="sm" />
        <span className={cn("min-w-0 flex-1 truncate", picked && "font-medium")}>{s.name}</span>
        {picked && <Check aria-label={t("switcher.picked")} className="!size-3 !text-fg" />}
        {n <= 9 && <Kbd className="opacity-0 transition-opacity duration-100 group-data-[highlighted]/item:opacity-100 motion-reduce:transition-none">{spaceKeys(n)}</Kbd>}
      </M.Item>
      <M.Sub>
        <M.SubTrigger
          aria-label={t("switcher.more", { name: s.name })}
          className="absolute right-1 top-1/2 flex size-6 -translate-y-1/2 cursor-default items-center justify-center rounded-md text-fg-3 opacity-0 outline-none transition-[opacity,background-color,color] duration-100 hover:text-fg group-hover/space:opacity-100 data-[highlighted]:bg-active data-[highlighted]:text-fg data-[highlighted]:opacity-100 data-[state=open]:bg-active data-[state=open]:opacity-100 motion-reduce:transition-none [@media(hover:none)]:opacity-100 [&_svg]:size-3.5"
        >
          <Ellipsis />
        </M.SubTrigger>
        <M.Portal>
          <M.SubContent sideOffset={6} collisionPadding={8} className={cn(floatingPanel, "min-w-[180px] origin-[var(--radix-dropdown-menu-content-transform-origin)] p-1")}>
            {sub(<Pencil />, t("menu.edit"), () => actions.edit(s.id))}
            {sub(<Copy />, t("menu.duplicate"), () => actions.duplicate(s), { disabled: actions.full })}
            {spaces.length > 1 && sub(<ArrowUp />, t("menu.moveUp"), () => actions.move(s, -1), { disabled: i <= 0 })}
            {spaces.length > 1 && sub(<ArrowDown />, t("menu.moveDown"), () => actions.move(s, 1), { disabled: i < 0 || i >= spaces.length - 1 })}
            <MenuSeparator />
            {sub(
              <Trash2 />,
              t("menu.delete"),
              () => confirm.show(t("menu.deleteBody"), () => actions.remove(s), { title: t("menu.deleteTitle", { name: s.name }), confirmLabel: t("menu.deleteConfirm"), danger: true }),
              { danger: true },
            )}
          </M.SubContent>
        </M.Portal>
      </M.Sub>
    </div>
  );
}

/** The Spaces group of the switcher menu, above the workspaces. */
export function SwitcherSpaces({ spaces, picked, starters }: { spaces: Space[]; picked: string | null; starters: { space: Space; workspace: string; logo: string | null }[] }) {
  const { t } = useTranslation("spaces");
  const actions = useSpaceActions(spaces);
  const { ordered } = useWorkspaces();
  return (
    <>
      <MenuLabel>{t("switcher.spaces")}</MenuLabel>
      {spaces.map((s, i) => (
        <SpaceEntry key={s.id} space={s} n={i + 1} picked={s.id === picked} spaces={spaces} actions={actions} />
      ))}
      {actions.full ? (
        <p className="px-2 pb-1 pt-1 text-[11.5px] text-fg-4">{t("menu.limit", { count: LIMITS.spaces })}</p>
      ) : (
        <MenuSub label={t("menu.new")} icon={<Plus />}>
          <MenuItem icon={<Square />} onSelect={() => actions.create()}>
            {t("newMenu.blank")}
          </MenuItem>
          <MenuSeparator />
          <MenuLabel>{t("newMenu.workspaces")}</MenuLabel>
          {ordered.map((m) => (
            <MenuItem key={m.workspace.id} icon={<WorkspaceBadge name={m.workspace.name} logo={m.workspace.avatar_url} size="sm" decorative />} onSelect={() => void actions.createFromWorkspace({ id: m.workspace.id, name: m.workspace.name })}>
              {t("newMenu.copyOf", { workspace: m.workspace.name })}
            </MenuItem>
          ))}
          {starters.length > 0 && (
            <>
              <MenuSeparator />
              <MenuLabel>{t("newMenu.starters")}</MenuLabel>
              {starters.map(({ space: s, workspace, logo }) => (
                <MenuItem key={`${workspace}:${s.id}`} icon={<SpaceGlyph icon={s.icon} name={s.name} color={s.color} size="sm" />} onSelect={() => actions.createFromStarter(s)}>
                  <span className="flex min-w-0 items-center gap-2">
                    <span className="min-w-0 flex-1 truncate">{s.name}</span>
                    <span className="flex shrink-0 items-center gap-1 text-[11px] text-fg-4">
                      <WorkspaceBadge name={workspace} logo={logo} size="xs" decorative />
                      <span className="max-w-[90px] truncate">{workspace}</span>
                    </span>
                  </span>
                </MenuItem>
              ))}
            </>
          )}
        </MenuSub>
      )}
    </>
  );
}
