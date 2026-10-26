// Customize sidebar: the member orders sections, hides and shows sections and what
// is in them (accounts, folders, boxes, views, teams), pins favorites, chooses the number
// beside each section's rows (and any one row's), and turns All inboxes and mark-as-read
// on and off. Each change shows at once and is saved with
// PUT /v1/collab/sidebar.

import { useQuery } from "@tanstack/react-query";
import { ChevronDown, ChevronRight, Eye, EyeOff, Hash, Layers, Pin, PinOff } from "lucide-react";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { SidebarBadge, SidebarPin, SidebarPrefs, SidebarSectionId } from "@/lib/api/types";
import { ACCOUNT_BOXES, accountName, buildFolderTree, flattenTree, folderWithRole, isMailBox } from "@/lib/collab/boxes";
import { useAllFolders, useMailAccounts, useSaveSidebarPrefs, useSidebarPrefs } from "@/lib/collab/hooks";
import {
  badgeChoices,
  badgeFor,
  badgeWord,
  hid,
  isHidden,
  isPinned,
  MAX_PINS,
  movePin,
  moveSection,
  sectionOrder,
  setBadge,
  setHidden,
  togglePin,
  type BadgeKind,
  type MailBadge,
  type TicketBadge,
} from "@/lib/collab/sidebarPrefs";
import { useCollabUi, collabUi } from "@/lib/collab/ui";
import { folderById, folders as ticketFolders } from "@/lib/folders";
import { cn } from "@/lib/cn";
import { q } from "@/lib/queries";
import { ReorderButtons } from "@/components/settings2/bits";
import { TeamDot, useTeamsCtx } from "@/components/tickets/teams";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Menu, MenuChoice, MenuContent, MenuSeparator, MenuTrigger } from "@/components/ui/menu";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast";
import { Tooltip } from "@/components/ui/tooltip";
import { AccountDot, boxIcon, FolderGlyph } from "./bits";
import { SpaceGlyph } from "@/components/spaces/icons";
import { useSession } from "@/lib/session";
import { useSpaces } from "@/lib/spaces/hooks";
import { pickedSpace } from "@/lib/spaces/model";
import { spaceUi, usePickedSpaceId } from "@/lib/spaces/store";

function IconToggle({ on, onClick, label, iconOn, iconOff, disabled }: { on: boolean; onClick: () => void; label: string; iconOn: ReactNode; iconOff: ReactNode; disabled?: boolean }) {
  return (
    <Tooltip content={label}>
      <button
        type="button"
        aria-label={label}
        aria-pressed={on}
        disabled={disabled}
        onClick={onClick}
        className={cn(
          "flex size-6 items-center justify-center rounded-md outline-none transition-colors hover:bg-hover focus-visible:shadow-[0_0_0_2px_var(--focus)] disabled:pointer-events-none disabled:opacity-30 [&_svg]:size-3.5",
          on ? "text-fg-2 hover:text-fg" : "text-fg-4 hover:text-fg-2",
        )}
      >
        {on ? iconOn : iconOff}
      </button>
    </Tooltip>
  );
}

type ItemBadge = { key: string; kind: BadgeKind; sections: SidebarSectionId[]; fallback?: MailBadge | TicketBadge };
type Item = { key: string; label: string; icon: ReactNode; hideId?: string; pin?: SidebarPin; badge?: ItemBadge; depth?: number; note?: string };

/** The sections whose rows carry numbers, and what kind. */
const SECTION_BADGE: Partial<Record<SidebarSectionId, BadgeKind | "both">> = { mail: "mail", favorites: "both", inbox: "ticket", teams: "team", views: "ticket" };

/** Favorites mixes mail and ticket rows: its own choice is a mail number, and a ticket row reads unread or total as its count. */
const choicesFor = (kind: BadgeKind | "both"): readonly SidebarBadge[] => badgeChoices(kind === "both" ? "mail" : kind);

/** A section's number for its rows: picking what rows show anyway clears the choice. */
function SectionNumbers({ id, prefs, onChange }: { id: SidebarSectionId; prefs: SidebarPrefs; onChange: (p: SidebarPrefs) => void }) {
  const { t } = useTranslation("collab");
  const kind = SECTION_BADGE[id];
  if (!kind) return null;
  const key = `section:${id}`;
  const rowKind: BadgeKind = kind === "both" ? "mail" : kind;
  const own = prefs.badges[key];
  const choices = choicesFor(kind);
  // Favorites without a choice of its own: each favorite reads the section it comes from.
  const value: SidebarBadge | "inherit" = own ? badgeFor(prefs, key, [], rowKind) : kind === "both" ? "inherit" : choices[0];
  const word = (v: SidebarBadge | "inherit") => (v === "inherit" ? t("customize.badge.section") : t(`customize.badge.${badgeWord(v, rowKind)}`));
  const set = (v: SidebarBadge | "inherit") => onChange(setBadge(prefs, key, v === "inherit" || (kind !== "both" && v === choices[0]) ? null : v));
  const label = t("customize.numberFor", { name: t(`customize.section.${id}`) });
  return (
    <Menu>
      <Tooltip content={t("customize.numbersHint")}>
        <MenuTrigger asChild>
          <button
            type="button"
            aria-label={`${label}: ${word(value)}`}
            className="flex h-6 shrink-0 items-center gap-1.5 rounded-md px-2 text-[11.5px] outline-none transition-colors hover:bg-hover focus-visible:shadow-[0_0_0_2px_var(--focus)] data-[state=open]:bg-hover"
          >
            <span className="hidden text-fg-4 sm:inline">{t("customize.numbers")}</span>
            <span className="font-medium text-fg-2">{word(value)}</span>
            <ChevronDown className="size-3 text-fg-4" />
          </button>
        </MenuTrigger>
      </Tooltip>
      <MenuContent align="end" className="w-[190px]">
        {kind === "both" && (
          <>
            <MenuChoice checked={value === "inherit"} onSelect={() => set("inherit")}>
              {word("inherit")}
            </MenuChoice>
            <MenuSeparator />
          </>
        )}
        {choices.map((v) => (
          <MenuChoice key={v} checked={value === v} onSelect={() => set(v)}>
            {word(v)}
          </MenuChoice>
        ))}
      </MenuContent>
    </Menu>
  );
}

/** One row's number: a quiet # while it follows its section, its own choice as a word once set. */
function RowNumbers({ item, badge, prefs, onChange }: { item: Item; badge: ItemBadge; prefs: SidebarPrefs; onChange: (p: SidebarPrefs) => void }) {
  const { t } = useTranslation("collab");
  const own = prefs.badges[badge.key] ? badgeFor(prefs, badge.key, [], badge.kind) : null;
  const effective = badgeFor(prefs, badge.key, badge.sections, badge.kind, badge.fallback);
  const word = (v: SidebarBadge) => t(`customize.badge.${badgeWord(v, badge.kind)}`);
  const label = t("customize.numberFor", { name: item.label });
  const pick = (v: SidebarBadge | null) => onChange(setBadge(prefs, badge.key, v));
  return (
    <Menu>
      <Tooltip content={`${label}: ${own ? word(own) : `${t("customize.badge.section")} (${word(effective)})`}`}>
        <MenuTrigger asChild>
          <button
            type="button"
            aria-label={label}
            className={cn(
              "flex h-6 shrink-0 items-center justify-center rounded-md outline-none transition-colors hover:bg-hover focus-visible:shadow-[0_0_0_2px_var(--focus)] data-[state=open]:bg-hover",
              own ? "px-1.5 text-[11px] font-medium text-fg-2" : "w-6 text-fg-4 hover:text-fg-2",
            )}
          >
            {own ? word(own) : <Hash className="size-3.5" />}
          </button>
        </MenuTrigger>
      </Tooltip>
      <MenuContent align="end" className="w-[190px]">
        <MenuChoice checked={own === null} onSelect={() => pick(null)}>
          {t("customize.badge.section")}
        </MenuChoice>
        <MenuSeparator />
        {badgeChoices(badge.kind).map((v) => (
          <MenuChoice key={v} checked={own === v} onSelect={() => pick(v)}>
            {word(v)}
          </MenuChoice>
        ))}
      </MenuContent>
    </Menu>
  );
}

function ItemRow({ item, prefs, onChange }: { item: Item; prefs: SidebarPrefs; onChange: (p: SidebarPrefs) => void }) {
  const { t } = useTranslation("collab");
  const hidden = item.hideId ? isHidden(prefs, item.hideId) : false;
  const pinned = item.pin ? isPinned(prefs, item.pin) : false;
  return (
    <li className="flex h-8 items-center gap-2 rounded-md pr-1 hover:bg-hover/60" style={{ paddingLeft: 8 + (item.depth ?? 0) * 16 }}>
      <span className={cn("flex size-4 shrink-0 items-center justify-center text-fg-3 [&_svg]:size-3.5", hidden && "opacity-50")}>{item.icon}</span>
      <span className={cn("min-w-0 flex-1 truncate text-[12.5px]", hidden ? "text-fg-4 line-through decoration-fg-4/50" : "text-fg")}>{item.label}</span>
      {item.note && <span className="shrink-0 text-[11px] text-fg-4">{item.note}</span>}
      {item.badge && <RowNumbers item={item} badge={item.badge} prefs={prefs} onChange={onChange} />}
      {item.pin && (
        <IconToggle
          on={pinned}
          disabled={!pinned && prefs.pinned.length >= MAX_PINS}
          label={pinned ? t("customize.unpin", { name: item.label }) : t("customize.pin", { name: item.label })}
          iconOn={<Pin className="fill-current" />}
          iconOff={<Pin />}
          onClick={() => onChange(togglePin(prefs, item.pin!))}
        />
      )}
      {item.hideId && (
        <IconToggle
          on={!hidden}
          label={hidden ? t("customize.show", { name: item.label }) : t("customize.hide", { name: item.label })}
          iconOn={<Eye />}
          iconOff={<EyeOff />}
          onClick={() => onChange(setHidden(prefs, item.hideId!, !hidden))}
        />
      )}
    </li>
  );
}

export function CustomizeSidebar() {
  const { t: tNav } = useTranslation("navigation");
  const { t } = useTranslation("collab");
  const open = useCollabUi((s) => s.customizeOpen);
  const prefs = useSidebarPrefs(true);
  const save = useSaveSidebarPrefs();
  const { data: accounts } = useMailAccounts();
  const folderMap = useAllFolders(accounts);
  const { data: views } = useQuery(q.views());
  const teams = useTeamsCtx();
  const [expanded, setExpanded] = useState<Set<SidebarSectionId>>(new Set(["mail"]));
  const { t: ts } = useTranslation("spaces");
  const { spaces } = useSpaces();
  const space = pickedSpace(spaces, usePickedSpaceId());
  const wsName = useSession((s) => s.memberships.find((m) => m.workspace.id === s.workspaceId)?.workspace.name ?? "");

  const change = (p: SidebarPrefs) => save.mutate(p, { onError: (e) => toast.error(e, t("customize.saveFailed")) });
  const order = sectionOrder(prefs);
  const name = (id: SidebarSectionId) => t(`customize.section.${id}`);

  const mailBadge = (key: string, fallback?: MailBadge): ItemBadge => ({ key, kind: "mail", sections: ["mail"], fallback });
  const ticketBadge = (key: string, section: SidebarSectionId): ItemBadge => ({ key, kind: section === "teams" ? "team" : "ticket", sections: [section] });

  const itemsOf = (id: SidebarSectionId): Item[] => {
    switch (id) {
      case "favorites":
        return [];
      case "mail": {
        const out: Item[] = [
          { key: "box:inbox", label: t("nav.allInboxes"), icon: <Layers />, hideId: hid.box("inbox"), pin: { kind: "box", id: "inbox" }, badge: mailBadge("box:inbox") },
          { key: "box:shared", label: t("box.shared"), icon: boxIcon.shared, hideId: hid.box("shared"), pin: { kind: "box", id: "shared" }, badge: mailBadge("box:shared") },
          { key: "box:assigned", label: t("box.assigned"), icon: boxIcon.assigned, hideId: hid.box("assigned"), pin: { kind: "box", id: "assigned" }, badge: mailBadge("box:assigned") },
          ...ACCOUNT_BOXES.map((b) => ({ key: `every:${b}`, label: t(`box.${b}`), icon: boxIcon[b], hideId: hid.folder(b), note: t("customize.everyAccount") })),
        ];
        for (const a of accounts ?? []) {
          const fs = folderMap[a.id];
          out.push({ key: `account:${a.id}`, label: accountName(a), icon: <AccountDot color={a.color} className="size-2.5" />, hideId: hid.account(a.id), pin: { kind: "account", id: a.id }, badge: mailBadge(`account:${a.id}`) });
          for (const b of ACCOUNT_BOXES) {
            const f = folderWithRole(fs, b);
            // Only Starred of the special folders showed a number before anyone chose.
            if (f) out.push({ key: `f:${f.id}`, label: t(`box.${b}`), icon: boxIcon[b], hideId: hid.folder(f.id), pin: { kind: "folder", id: f.id }, badge: mailBadge(`folder:${f.id}`, b === "starred" ? undefined : "none"), depth: 1 });
          }
          for (const { node, depth } of flattenTree(buildFolderTree(fs ?? []))) {
            if (!node.folder) continue;
            out.push({ key: `f:${node.folder.id}`, label: node.name, icon: <FolderGlyph folder={node.folder} />, hideId: hid.folder(node.folder.id), pin: { kind: "folder", id: node.folder.id }, badge: mailBadge(`folder:${node.folder.id}`), depth: 1 + depth });
          }
        }
        return out;
      }
      case "inbox":
        return ticketFolders.map((f) => ({
          key: f.id,
          label: tNav(f.labelKey),
          icon: <Layers />,
          hideId: hid.ticketFolder(f.id),
          pin: { kind: "ticket_folder", id: f.id },
          badge: f.id === "solved" || f.id === "spam" ? undefined : ticketBadge(hid.ticketFolder(f.id), "inbox"),
        }));
      case "teams":
        return teams.teams
          .filter(teams.canSeeTeam)
          .map((x) => ({ key: x.id, label: x.name, icon: <TeamDot team={x} />, hideId: hid.team(x.id), pin: { kind: "team", id: x.id }, badge: ticketBadge(hid.team(x.id), "teams") }));
      case "views":
        return [...(views ?? [])]
          .sort((a, b) => a.position - b.position)
          .map((v) => ({ key: v.id, label: v.name, icon: <Layers />, hideId: hid.view(v.id), pin: { kind: "view", id: v.id }, badge: ticketBadge(hid.view(v.id), "views") }));
      case "workspace":
        return [];
    }
  };

  // Favorites list: what each pin points at, by name.
  const pinLabel = (p: SidebarPin): string | null => {
    switch (p.kind) {
      case "box":
        return isMailBox(p.id) ? (p.id === "inbox" ? t("nav.allInboxes") : t(`box.${p.id}`)) : null;
      case "account": {
        const a = accounts?.find((x) => x.id === p.id);
        return a ? accountName(a) : null;
      }
      case "folder": {
        const f = Object.values(folderMap).flatMap((x) => x ?? []).find((x) => x.id === p.id);
        return f ? f.name : null;
      }
      case "view":
        return views?.find((v) => v.id === p.id)?.name ?? null;
      case "team":
        return teams.team(p.id)?.name ?? null;
      case "ticket_folder": {
        const folder = folderById(p.id);
        return folder ? tNav(folder.labelKey) : null;
      }
      default:
        return null;
    }
  };
  const livePinned = prefs.pinned.filter((p) => pinLabel(p));

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => collabUi.customize(o)}
      size="lg"
      title={t("customize.title")}
      description={t("customize.description")}
      footer={
        <Button variant="primary" onClick={() => collabUi.customize(false)}>
          {t("customize.done")}
        </Button>
      }
    >
      <div className="flex flex-col gap-5 pb-2">
        {space && (
          // These prefs are the workspace's own sidebar (and what is inside its sections in a Space); the picked Space has its editor.
          <div className="flex items-center justify-between gap-3 rounded-lg bg-sunken px-3.5 py-2.5 shadow-[inset_0_0_0_1px_var(--line-soft)]">
            <span className="flex min-w-0 items-center gap-2 text-[12px] text-fg-3">
              <SpaceGlyph icon={space.icon} name={space.name} color={space.color} />
              <span>{ts("customize.spaces", { space: space.name, workspace: wsName })}</span>
            </span>
            <Button
              size="xs"
              onClick={() => {
                collabUi.customize(false);
                spaceUi.edit({ kind: "space", id: space.id });
              }}
            >
              {ts("customize.editSpace")}
            </Button>
          </div>
        )}
        <section>
          <h3 className="label-caps mb-1.5">{t("customize.options")}</h3>
          <div className="rounded-lg border border-line">
            <label className="flex items-start justify-between gap-4 border-b border-line-soft px-3.5 py-3">
              <span>
                <span className="block text-[12.5px] font-medium">{t("customize.unified")}</span>
                <span className="mt-0.5 block text-[12px] text-fg-3">{t("customize.unifiedHint")}</span>
              </span>
              <Switch checked={prefs.unified_inbox} onCheckedChange={(v) => change({ ...prefs, unified_inbox: v })} aria-label={t("customize.unified")} />
            </label>
            <label className="flex items-start justify-between gap-4 px-3.5 py-3">
              <span>
                <span className="block text-[12.5px] font-medium">{t("customize.markRead")}</span>
                <span className="mt-0.5 block text-[12px] text-fg-3">{t("customize.markReadHint")}</span>
              </span>
              <Switch checked={prefs.mark_read_on_open} onCheckedChange={(v) => change({ ...prefs, mark_read_on_open: v })} aria-label={t("customize.markRead")} />
            </label>
          </div>
        </section>

        <section>
          <h3 className="label-caps mb-1.5">{t("customize.sections")}</h3>
          <ol className="flex flex-col rounded-lg border border-line">
            {order.map((id, i) => {
              const items = itemsOf(id);
              const sectionHidden = isHidden(prefs, hid.section(id));
              const isOpen = expanded.has(id);
              const hasBody = items.length > 0 || id === "favorites";
              return (
                <li key={id} className="border-b border-line-soft last:border-0">
                  <div className="flex h-10 items-center gap-2 px-2">
                    <ReorderButtons label={name(id)} first={i === 0} last={i === order.length - 1} onUp={() => change(moveSection(prefs, id, -1))} onDown={() => change(moveSection(prefs, id, 1))} />
                    {hasBody ? (
                      <button
                        type="button"
                        aria-expanded={isOpen}
                        aria-label={isOpen ? t("customize.collapse", { name: name(id) }) : t("customize.expand", { name: name(id) })}
                        onClick={() =>
                          setExpanded((s) => {
                            const n = new Set(s);
                            if (n.has(id)) n.delete(id);
                            else n.add(id);
                            return n;
                          })
                        }
                        className="flex min-w-0 flex-1 items-center gap-1.5 rounded-md py-1 text-left outline-none focus-visible:shadow-[0_0_0_2px_var(--focus)]"
                      >
                        <ChevronRight className={cn("size-3.5 shrink-0 text-fg-4 transition-transform duration-150 motion-reduce:transition-none", isOpen && "rotate-90")} />
                        <span className={cn("truncate text-[13px] font-medium", sectionHidden && "text-fg-4")}>{name(id)}</span>
                        <span className="mono text-[10.5px] text-fg-4">{id === "favorites" ? livePinned.length : items.length}</span>
                      </button>
                    ) : (
                      <span className={cn("flex-1 truncate pl-5 text-[13px] font-medium", sectionHidden && "text-fg-4")}>{name(id)}</span>
                    )}
                    <SectionNumbers id={id} prefs={prefs} onChange={change} />
                    <IconToggle
                      on={!sectionHidden}
                      label={sectionHidden ? t("customize.show", { name: name(id) }) : t("customize.hide", { name: name(id) })}
                      iconOn={<Eye />}
                      iconOff={<EyeOff />}
                      onClick={() => change(setHidden(prefs, hid.section(id), !sectionHidden))}
                    />
                  </div>
                  {isOpen && hasBody && (
                    <div className="px-2 pb-2 animate-rise-in motion-reduce:animate-none">
                      {id === "favorites" ? (
                        livePinned.length ? (
                          <ol className="flex flex-col">
                            {livePinned.map((p, j) => (
                              <li key={`${p.kind}:${p.id}`} className="flex h-8 items-center gap-2 rounded-md pl-2 pr-1 hover:bg-hover/60">
                                <Pin className="size-3.5 shrink-0 fill-current text-fg-3" />
                                <span className="min-w-0 flex-1 truncate text-[12.5px]">{pinLabel(p)}</span>
                                <ReorderButtons label={pinLabel(p) ?? ""} first={j === 0} last={j === livePinned.length - 1} onUp={() => change(movePin(prefs, p, -1))} onDown={() => change(movePin(prefs, p, 1))} />
                                <IconToggle on label={t("customize.unpin", { name: pinLabel(p) ?? "" })} iconOn={<PinOff />} iconOff={<PinOff />} onClick={() => change(togglePin(prefs, p))} />
                              </li>
                            ))}
                          </ol>
                        ) : (
                          <p className="px-2 py-2 text-[12px] text-fg-3">{t("customize.favoritesEmpty")}</p>
                        )
                      ) : (
                        <ul className="flex flex-col">
                          {items.map((item) => (
                            <ItemRow key={item.key} item={item} prefs={prefs} onChange={change} />
                          ))}
                        </ul>
                      )}
                    </div>
                  )}
                </li>
              );
            })}
          </ol>
          {prefs.pinned.length >= MAX_PINS && <p className="mt-1.5 text-[11.5px] text-fg-4">{t("customize.pinLimit")}</p>}
        </section>
      </div>
    </Dialog>
  );
}
