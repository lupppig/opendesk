// The sidebar with Collab on: the member's sections in their order, with Favorites
// (pinned boxes, folders, accounts, views and teams) and Mail, kept short: All inboxes,
// Shared with me and Assigned to me while they hold something, then one row per account
// (its folders unfold on request). Unread counts come from GET /v1/collab/counts, kept
// fresh by collab.changed events.

import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useRouterState } from "@tanstack/react-router";
import { AlertCircle, ChevronRight, CircleCheck, CircleDashed, Clock, Inbox, Layers, Plus, ShieldAlert, SlidersHorizontal, User } from "lucide-react";
import { Fragment, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { ActivePill, Count, Label, WaitingBadge, NavTip, rowActive, rowBase, rowIdle, rowShape, Section, withPill } from "@/app/sidebarParts";
import { TeamDot, useTeamsCtx } from "@/components/tickets/teams";
import { useWaitingFor } from "@/components/tickets/useUrgentChats";
import { Tooltip } from "@/components/ui/tooltip";
import type { MailAccount, MailBox, MailCounts, MailFolder, SidebarPin, SidebarPrefs, SidebarSectionId } from "@/lib/api/types";
import { accountNumbers, allInboxNumbers, boxTabNumbers, folderNumbers, listBoxNumbers, ticketRowNumbers, type RowNumbers } from "@/lib/collab/badges";
import { ACCOUNT_BOXES, accountName, buildFolderTree, flattenTree, folderWithRole, isMailBox, isTabBox, type MailSearch } from "@/lib/collab/boxes";
import { useAllFolders, useMailAccounts, useSaveSidebarPrefs } from "@/lib/collab/hooks";
import { badgeFor, hid, livePins, pickCount, toggleExpanded, type BadgeNumbers, type MailBadge, type TicketBadge } from "@/lib/collab/sidebarPrefs";
import { collabUi } from "@/lib/collab/ui";
import { folderById, type FolderId } from "@/lib/folders";
import { cn } from "@/lib/cn";
import { q } from "@/lib/queries";
import { isAway } from "@/lib/collab/outOfOffice";
import { AwayPill } from "./AccountSettings";
import { AccountDot, boxIcon, FolderGlyph } from "./bits";
import { RowMenu, type RowMenuProps } from "./RowMenu";

const ticketFolderIcons: Record<FolderId, ReactNode> = {
  unassigned: <CircleDashed />,
  mine: <User />,
  open: <Inbox />,
  pending: <Clock />,
  solved: <CircleCheck />,
  spam: <ShieldAlert />,
};

const sectionAction =
  "flex size-5 items-center justify-center rounded text-fg-4 opacity-0 transition-opacity hover:bg-chrome-hover hover:text-fg group-hover/section:opacity-100 focus-visible:opacity-100";

/** Where the mail page is: its box, account and folder, from the URL. */
function useMailLocation(): { box: string | null; account?: string; folder?: string } {
  const href = useRouterState({ select: (s) => (s.location.pathname.startsWith("/mail/") ? `${s.location.pathname}|${(s.location.search as MailSearch).account ?? ""}|${(s.location.search as MailSearch).folder ?? ""}` : "") });
  if (!href) return { box: null };
  const [path, account, folder] = href.split("|");
  return { box: path.split("/")[2] ?? null, account: account || undefined, folder: folder || undefined };
}

type MailLink = { box: MailBox | "folder"; account?: string; folder?: string };

/** What a mail row shows for its chosen number: the count beside it, the rail's dot (unread only, never for none), and every number it could show. */
type Shown = { count?: number; dot: boolean; nums?: RowNumbers };

/**
 * A mail row's number by the member's choice (contracts/collab.md SidebarPrefs.badges).
 * `fallback` is what the row showed before anyone chose: unread, or nothing for rows that never had a number.
 */
function mailShown(prefs: SidebarPrefs, key: string, sections: SidebarSectionId[], nums: RowNumbers, fallback?: MailBadge): Shown {
  const b = badgeFor(prefs, key, sections, "mail", fallback);
  return { count: pickCount(b, nums), dot: b !== "none" && !!nums.unread, nums };
}

/** A ticket or team row's number by the member's choice: its usual count, one of its stats, or none. */
function ticketShown(prefs: SidebarPrefs, key: string, sections: SidebarSectionId[], kind: "ticket" | "team", nums: BadgeNumbers, fallback?: TicketBadge): number | undefined {
  return pickCount(badgeFor(prefs, key, sections, kind, fallback), nums);
}

type Menu = Omit<RowMenuProps, "children" | "enabled" | "prefs" | "rail" | "label">;

function MailRow({
  link,
  active,
  icon,
  label,
  shown,
  rail,
  depth = 0,
  title,
  end,
  tag,
  prefs,
  menu,
}: {
  link: MailLink;
  active: boolean;
  icon: ReactNode;
  label: string;
  shown?: Shown;
  rail: boolean;
  depth?: number;
  title?: string;
  end?: ReactNode;
  /** Beside the label, before the count (the Away pill). */
  tag?: ReactNode;
  prefs: SidebarPrefs;
  menu?: Menu;
}) {
  const count = shown?.count;
  // The menu lists what each choice would show.
  const withNums: Menu | undefined = menu?.badge && shown?.nums ? { ...menu, badge: { ...menu.badge, numbers: shown.nums } } : menu;
  return (
    <NavTip rail={rail} label={label}>
      <RowMenu enabled={!!menu} prefs={prefs} rail={rail} label={label} {...withNums}>
      <Link
        to="/mail/$box/{-$threadId}"
        params={{ box: link.box, threadId: undefined }}
        search={{ account: link.account, folder: link.folder }}
        className={cn(rowBase, rowShape(rail), active ? rowActive : rowIdle)}
        data-status={active ? "active" : undefined}
        aria-current={active ? "page" : undefined}
        aria-label={rail ? label : undefined}
        title={rail ? undefined : title}
        style={!rail && depth ? { paddingLeft: `${8 + depth * 14}px` } : undefined}
      >
        {active && <ActivePill />}
        {icon}
        <Label rail={rail}>{label}</Label>
        {!rail && tag}
        {end}
        {!end && count !== undefined && count > 0 && <Count n={count} rail={rail} />}
        {rail && shown?.dot && <span className="absolute right-1 top-1 size-1.5 rounded-full bg-fg ring-2 ring-chrome" />}
      </Link>
      </RowMenu>
    </NavTip>
  );
}

/** An account's trouble, in place of its count: it needs a reconnect, or its sync failed. */
function AccountAlert({ account }: { account: MailAccount }) {
  const { t } = useTranslation("collab");
  const navigate = useNavigate();
  if (!account.reconnect && account.status !== "error") return null;
  const problem = account.reconnect ? t("nav.reconnect") : account.last_error || t("nav.syncError");
  // Inside the row's link: the click opens the account's settings instead of its inbox. The
  // row's ⋯ menu has the same step for the keyboard.
  return (
    <Tooltip content={`${problem}. ${t("nav.fixHint")}`}>
      <span
        className="ml-auto flex cursor-pointer rounded-full text-tone-red transition-transform hover:scale-110 motion-reduce:transition-none"
        onClick={(e) => {
          e.preventDefault();
          e.stopPropagation();
          void navigate({ to: "/settings/mail-accounts/$accountId", params: { accountId: account.id } });
        }}
      >
        <AlertCircle className="size-3.5" />
      </span>
    </Tooltip>
  );
}

/**
 * One account: a single row that opens its inbox, with its unread count. Its other
 * folders stay folded until asked for (the mail page's folder bar has every one), so
 * Mail never pushes the ticket inbox out of sight.
 */
function AccountGroup({
  account,
  folders,
  prefs,
  hidden,
  counts,
  here,
  rail,
}: {
  account: MailAccount;
  folders: MailFolder[] | undefined;
  prefs: SidebarPrefs;
  hidden: ReadonlySet<string>;
  counts: MailCounts | undefined;
  here: ReturnType<typeof useMailLocation>;
  rail: boolean;
}) {
  const { t } = useTranslation("collab");
  const navigate = useNavigate();
  const save = useSaveSidebarPrefs();
  const name = accountName(account);
  const key = `account:${account.id}`;
  const shown = mailShown(prefs, key, ["mail"], accountNumbers(account, folders, counts));
  const menu: Menu = { badge: { key, kind: "mail", sections: ["mail"] }, pin: { kind: "account", id: account.id }, hideId: hid.account(account.id) };
  const expanded = !rail && prefs.expanded_accounts.includes(account.id);
  const atInbox = here.box === "inbox" && here.account === account.id && !here.folder;
  const trouble = account.reconnect || account.status === "error";
  if (trouble)
    menu.action = {
      label: account.reconnect ? t("nav.fixReconnect") : t("nav.fixSync"),
      icon: <AlertCircle className="!text-tone-red" />,
      onSelect: () => void navigate({ to: "/settings/mail-accounts/$accountId", params: { accountId: account.id } }),
    };
  if (rail)
    return (
      <MailRow
        link={{ box: "inbox", account: account.id }}
        active={atInbox}
        icon={<span className="flex size-[15px] shrink-0 items-center justify-center"><AccountDot color={account.color} className="size-2.5" /></span>}
        label={name}
        shown={shown}
        rail
        prefs={prefs}
        menu={menu}
      />
    );

  // Expanded: the everyday folders the provider has (the row above is the inbox; Spam and
  // Trash wait in the folder bar's More), then the account's own.
  const specialShown = ACCOUNT_BOXES.filter((b) => {
    if (b === "inbox" || b === "spam" || b === "trash" || hidden.has(hid.folder(b))) return false;
    const f = folderWithRole(folders, b);
    if (f && hidden.has(hid.folder(f.id))) return false;
    // Archive shows on Gmail, which archives by removing the inbox label and has no such folder.
    return b === "starred" || (b === "archive" && account.provider === "google") || !folders || !!f;
  });
  const tree = expanded ? flattenTree(buildFolderTree(folders ?? [])).filter(({ node }) => !node.folder || !hidden.has(hid.folder(node.folder.id))) : [];
  const toggleLabel = expanded ? t("nav.hideFolders", { name }) : t("nav.showFolders", { name });

  return (
    <div className="flex flex-col gap-px">
      <div className="group/account relative">
        <MailRow
          link={{ box: "inbox", account: account.id }}
          active={atInbox}
          icon={
            <span className="flex size-[15px] shrink-0 items-center justify-center">
              <AccountDot color={account.color} className={cn("size-2.5 transition-opacity", expanded ? "opacity-0" : "group-hover/account:opacity-0")} />
            </span>
          }
          label={name}
          title={account.email}
          rail={false}
          end={trouble ? <AccountAlert account={account} /> : undefined}
          tag={isAway(account) ? <AwayPill /> : undefined}
          shown={shown}
          prefs={prefs}
          menu={menu}
        />
        {/* Over the dot: folds and unfolds the account's folders. */}
        <Tooltip content={toggleLabel}>
          <button
            type="button"
            aria-expanded={expanded}
            aria-label={toggleLabel}
            onClick={() => save.mutate(toggleExpanded(prefs, account.id))}
            className={cn(
              "absolute left-[15px] top-1/2 flex size-5 -translate-y-1/2 items-center justify-center rounded text-fg-3 outline-none transition-[opacity,color,background-color] hover:bg-chrome-hover hover:text-fg focus-visible:opacity-100 focus-visible:shadow-[0_0_0_2px_var(--focus)]",
              expanded ? "opacity-100" : "opacity-0 group-hover/account:opacity-100",
            )}
          >
            <ChevronRight className={cn("size-3 transition-transform duration-150 motion-reduce:transition-none", expanded && "rotate-90")} />
          </button>
        </Tooltip>
      </div>
      {expanded && (
        <>
          {specialShown.map((b) => {
            const f = folderWithRole(folders, b);
            // Starred showed its unread conversations before anyone chose; the others nothing.
            const fkey = f ? `folder:${f.id}` : "";
            return (
              <MailRow
                key={b}
                link={{ box: b, account: account.id }}
                active={here.box === b && here.account === account.id && !here.folder}
                icon={boxIcon[b]}
                label={t(`box.${b}`)}
                shown={f ? mailShown(prefs, fkey, ["mail"], folderNumbers(f, counts), b === "starred" ? undefined : "none") : undefined}
                rail={false}
                depth={1}
                prefs={prefs}
                menu={f ? { badge: { key: fkey, kind: "mail", sections: ["mail"] }, pin: { kind: "folder", id: f.id }, hideId: hid.folder(f.id) } : undefined}
              />
            );
          })}
          {tree.map(({ node, depth }) =>
            node.folder ? (
              <MailRow
                key={node.key}
                link={{ box: "folder", folder: node.folder.id, account: account.id }}
                active={here.folder === node.folder.id}
                icon={<FolderGlyph folder={node.folder} />}
                label={node.name}
                title={node.path}
                shown={mailShown(prefs, `folder:${node.folder.id}`, ["mail"], folderNumbers(node.folder, counts))}
                rail={false}
                depth={1 + depth}
                prefs={prefs}
                menu={{
                  badge: { key: `folder:${node.folder.id}`, kind: "mail", sections: ["mail"] },
                  pin: { kind: "folder", id: node.folder.id },
                  hideId: hid.folder(node.folder.id),
                  labelColor: { folder: node.folder, provider: account.provider },
                }}
              />
            ) : (
              <div key={node.key} className={cn(rowBase, rowShape(false), "text-fg-3")} style={{ paddingLeft: `${8 + (1 + depth) * 14}px` }}>
                <FolderGlyph folder={null} className="text-fg-4" />
                <span className="min-w-0 flex-1 truncate">{node.name}</span>
              </div>
            ),
          )}
        </>
      )}
    </div>
  );
}

function MailSection({ prefs, rail, first }: { prefs: SidebarPrefs; rail: boolean; first: boolean }) {
  const { t } = useTranslation("collab");
  const { data: accounts } = useMailAccounts();
  const { data: counts } = useQuery(q.mailCounts());
  const folders = useAllFolders(accounts);
  const here = useMailLocation();
  const hidden = new Set(prefs.hidden);
  const shown = (accounts ?? []).filter((a) => !hidden.has(hid.account(a.id)));
  const unified = prefs.unified_inbox && (accounts?.length ?? 0) >= 2 && !hidden.has(hid.box("inbox"));

  return (
    <Section
      id="mail"
      label={t("nav.mail")}
      rail={rail}
      first={first}
      action={
        <span className="flex items-center gap-0.5">
          <Tooltip content={t("nav.customize")}>
            <button type="button" aria-label={t("nav.customize")} onClick={() => collabUi.customize(true)} className={sectionAction}>
              <SlidersHorizontal className="size-3.5" />
            </button>
          </Tooltip>
          <Tooltip content={t("nav.connect")}>
            <Link to="/settings/mail-accounts" search={{}} aria-label={t("nav.connect")} className={sectionAction}>
              <Plus className="size-3.5" />
            </Link>
          </Tooltip>
        </span>
      }
    >
      {unified && (
        <MailRow
          link={{ box: "inbox" }}
          active={here.box === "inbox" && !here.account && !here.folder}
          icon={<Layers />}
          label={t("nav.allInboxes")}
          shown={mailShown(prefs, "box:inbox", ["mail"], allInboxNumbers(shown, folders, counts))}
          rail={rail}
          prefs={prefs}
          menu={{ badge: { key: "box:inbox", kind: "mail", sections: ["mail"] }, pin: { kind: "box", id: "inbox" }, hideId: hid.box("inbox") }}
        />
      )}
      {/* Shared and assigned conversations show only while there are some (or while open);
          the mail page reaches them always. */}
      {!hidden.has(hid.box("shared")) && (here.box === "shared" || !!counts?.boxes.shared) && (
        <MailRow
          link={{ box: "shared" }}
          active={here.box === "shared"}
          icon={boxIcon.shared}
          label={t("box.shared")}
          shown={mailShown(prefs, "box:shared", ["mail"], listBoxNumbers("shared", counts))}
          rail={rail}
          prefs={prefs}
          menu={{ badge: { key: "box:shared", kind: "mail", sections: ["mail"] }, pin: { kind: "box", id: "shared" }, hideId: hid.box("shared") }}
        />
      )}
      {!hidden.has(hid.box("assigned")) && (here.box === "assigned" || !!counts?.boxes.assigned) && (
        <MailRow
          link={{ box: "assigned" }}
          active={here.box === "assigned"}
          icon={boxIcon.assigned}
          label={t("box.assigned")}
          shown={mailShown(prefs, "box:assigned", ["mail"], listBoxNumbers("assigned", counts))}
          rail={rail}
          prefs={prefs}
          menu={{ badge: { key: "box:assigned", kind: "mail", sections: ["mail"] }, pin: { kind: "box", id: "assigned" }, hideId: hid.box("assigned") }}
        />
      )}
      {shown.map((a) => (
        <AccountGroup key={a.id} account={a} folders={folders[a.id]} prefs={prefs} hidden={hidden} counts={counts} here={here} rail={rail} />
      ))}
      {accounts && accounts.length === 0 && (
        <NavTip rail={rail} label={t("nav.noAccounts")}>
          <Link to="/settings/mail-accounts" search={{}} className={cn(rowBase, rowShape(rail), rowIdle, "text-fg-3")}>
            <Plus />
            <Label rail={rail}>{t("nav.noAccounts")}</Label>
          </Link>
        </NavTip>
      )}
    </Section>
  );
}

type PinTarget = { label: string; icon: ReactNode; row: (rail: boolean) => ReactNode };

function FavoritesSection({ prefs, rail, first }: { prefs: SidebarPrefs; rail: boolean; first: boolean }) {
  const { t } = useTranslation("collab");
  const waitingFor = useWaitingFor();
  const { data: accounts } = useMailAccounts();
  const { data: counts } = useQuery(q.mailCounts());
  const { data: ticketCounts } = useQuery(q.counts());
  const { data: views } = useQuery(q.views());
  const teams = useTeamsCtx();
  const folders = useAllFolders(accounts);
  const here = useMailLocation();
  const allFolders = Object.values(folders).flatMap((f) => f ?? []);
  const hidden = new Set(prefs.hidden);
  const shownAccounts = (accounts ?? []).filter((a) => !hidden.has(hid.account(a.id)));
  // A favorite is unpinned rather than hidden, and reads Favorites' number first, then its own section's.
  const mailMenu = (key: string, pin: SidebarPin): Menu => ({ badge: { key, kind: "mail", sections: ["favorites", "mail"] }, pin });
  const ticketMenu = (key: string, pin: SidebarPin, home: SidebarSectionId, numbers: BadgeNumbers): Menu => ({
    badge: { key, kind: home === "teams" ? "team" : "ticket", sections: ["favorites", home], numbers },
    pin,
  });
  const ticketRow = (r: boolean, link: ReactNode, menu: Menu, label: string) => (
    <NavTip rail={r} label={label}>
      <RowMenu enabled prefs={prefs} rail={r} label={label} {...menu}>
        {link as React.ReactElement}
      </RowMenu>
    </NavTip>
  );

  const resolve = (pin: SidebarPin): PinTarget | null => {
    switch (pin.kind) {
      case "box": {
        if (!isMailBox(pin.id)) return null;
        const box = pin.id;
        const key = `box:${box}`;
        // Inbox, Unread, Shared and Assigned showed their count before anyone chose; the other boxes nothing.
        const nums: RowNumbers =
          box === "inbox"
            ? allInboxNumbers(shownAccounts, folders, counts)
            : box === "shared" || box === "assigned"
              ? listBoxNumbers(box, counts)
              : isTabBox(box)
                ? boxTabNumbers(box, accounts ?? [], folders, counts, true)
                : {};
        const fallback = box === "inbox" || box === "shared" || box === "assigned" || box === "unread" ? undefined : "none";
        const label = box === "inbox" ? t("nav.allInboxes") : t(`box.${box}`);
        return {
          label,
          icon: boxIcon[box],
          row: (r) => (
            <MailRow
              link={{ box }}
              active={here.box === box && !here.account && !here.folder}
              icon={box === "inbox" ? <Layers /> : boxIcon[box]}
              label={label}
              shown={mailShown(prefs, key, ["favorites", "mail"], nums, fallback)}
              rail={r}
              prefs={prefs}
              menu={mailMenu(key, pin)}
            />
          ),
        };
      }
      case "account": {
        const a = accounts?.find((x) => x.id === pin.id);
        if (!a) return null;
        const label = accountName(a);
        const key = `account:${a.id}`;
        const icon = <span className="flex size-[15px] items-center justify-center"><AccountDot color={a.color} className="size-2.5" /></span>;
        return {
          label,
          icon,
          row: (r) => (
            <MailRow
              link={{ box: "inbox", account: a.id }}
              active={here.box === "inbox" && here.account === a.id && !here.folder}
              icon={icon}
              label={label}
              shown={mailShown(prefs, key, ["favorites", "mail"], accountNumbers(a, folders[a.id], counts))}
              rail={r}
              prefs={prefs}
              menu={mailMenu(key, pin)}
            />
          ),
        };
      }
      case "folder": {
        const f = allFolders.find((x) => x.id === pin.id);
        if (!f) return null;
        const label = f.name;
        const key = `folder:${f.id}`;
        const icon = <FolderGlyph folder={f} />;
        return {
          label,
          icon,
          row: (r) => (
            <MailRow
              link={{ box: "folder", folder: f.id, account: f.account_id }}
              active={here.folder === f.id}
              icon={icon}
              label={label}
              title={f.path}
              shown={mailShown(prefs, key, ["favorites", "mail"], folderNumbers(f, counts))}
              rail={r}
              prefs={prefs}
              menu={mailMenu(key, pin)}
            />
          ),
        };
      }
      case "view": {
        const v = views?.find((x) => x.id === pin.id);
        if (!v) return null;
        const key = `view:${v.id}`;
        const nums = ticketRowNumbers(ticketCounts, key);
        const n = ticketShown(prefs, key, ["favorites", "views"], "ticket", nums);
        return {
          label: v.name,
          icon: <Layers />,
          row: (r) =>
            ticketRow(
              r,
              <Link
                to="/views/$viewId/{-$ticketId}"
                params={{ viewId: v.id, ticketId: undefined }}
                className={cn(rowBase, rowShape(r), rowIdle)}
                activeProps={{ className: rowActive, "data-status": "active" } as Record<string, string>}
                aria-label={r ? v.name : undefined}
              >
                {withPill(
                  <>
                    <Layers />
                    <Label rail={r}>{v.name}</Label>
                    <WaitingBadge n={waitingFor({ kind: "view", id: v.id })} rail={r} />
                    <Count n={n} rail={r} />
                  </>,
                )}
              </Link>,
              ticketMenu(key, pin, "views", nums),
              v.name,
            ),
        };
      }
      case "team": {
        const team = teams.team(pin.id);
        if (!team || !teams.canSeeTeam(team)) return null;
        const key = `team:${team.id}`;
        const nums = ticketRowNumbers(ticketCounts, key);
        const n = ticketShown(prefs, key, ["favorites", "teams"], "team", nums);
        return {
          label: team.name,
          icon: <TeamDot team={team} />,
          row: (r) =>
            ticketRow(
              r,
              <Link
                to="/teams/$teamId/{-$ticketId}"
                params={{ teamId: team.id, ticketId: undefined }}
                className={cn(rowBase, rowShape(r), rowIdle)}
                activeProps={{ className: rowActive, "data-status": "active" } as Record<string, string>}
                aria-label={r ? team.name : undefined}
              >
                {withPill(
                  <>
                    <span className="flex size-[15px] shrink-0 items-center justify-center">
                      <TeamDot team={team} />
                    </span>
                    <Label rail={r}>{team.name}</Label>
                    <WaitingBadge n={waitingFor({ kind: "team", id: team.id })} rail={r} />
                    {!!n && <Count n={n} rail={r} />}
                  </>,
                )}
              </Link>,
              ticketMenu(key, pin, "teams", nums),
              team.name,
            ),
        };
      }
      case "ticket_folder": {
        const f = folderById(pin.id);
        if (!f) return null;
        const key = `ticket_folder:${f.id}`;
        const counted = f.id !== "solved" && f.id !== "spam";
        const nums = ticketRowNumbers(ticketCounts, key);
        const n = counted ? ticketShown(prefs, key, ["favorites", "inbox"], "ticket", nums) : undefined;
        return {
          label: f.label,
          icon: ticketFolderIcons[f.id],
          row: (r) =>
            ticketRow(
              r,
              <Link
                to="/inbox/$folder/{-$ticketId}"
                params={{ folder: f.id, ticketId: undefined }}
                className={cn(rowBase, rowShape(r), rowIdle)}
                activeProps={{ className: rowActive, "data-status": "active" } as Record<string, string>}
                aria-label={r ? f.label : undefined}
              >
                {withPill(
                  <>
                    {ticketFolderIcons[f.id]}
                    <Label rail={r}>{f.label}</Label>
                    <WaitingBadge n={waitingFor({ kind: "ticket_folder", id: f.id })} rail={r} />
                    {n !== undefined && <Count n={n} rail={r} />}
                  </>,
                )}
              </Link>,
              counted ? ticketMenu(key, pin, "inbox", nums) : { pin },
              f.label,
            ),
        };
      }
      default:
        return null;
    }
  };

  const pins = livePins(prefs, resolve);
  if (!pins.length) return null;
  return (
    <Section id="favorites" label={t("nav.favorites")} rail={rail} first={first}>
      {pins.map(({ pin, target }) => (
        <Fragment key={`${pin.kind}:${pin.id}`}>{target.row(rail)}</Fragment>
      ))}
    </Section>
  );
}

export default function MailSections({
  order,
  prefs,
  rail,
  renderSection,
}: {
  order: SidebarSectionId[];
  prefs: SidebarPrefs;
  rail: boolean;
  drawer?: boolean;
  renderSection: (id: SidebarSectionId) => ReactNode;
}) {
  return (
    <>
      {order.map((id, i) => (
        <Fragment key={id}>
          {id === "mail" ? (
            <MailSection prefs={prefs} rail={rail} first={i === 0} />
          ) : id === "favorites" ? (
            <FavoritesSection prefs={prefs} rail={rail} first={i === 0} />
          ) : (
            renderSection(id)
          )}
        </Fragment>
      ))}
    </>
  );
}
