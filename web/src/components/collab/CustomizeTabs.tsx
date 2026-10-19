// Customize tabs: which boxes and folders show as tabs in the mail page's folder bar, and
// in what order (SidebarPrefs.mail_tabs). Tabs first, in order, with move buttons; below,
// what stays in More. Each change shows at once and is saved with PUT /v1/collab/sidebar.

import { Fragment, useId, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { MailAccount, MailFolder, SidebarPrefs } from "@/lib/api/types";
import { accountName, buildFolderTree, flattenWithout, hasBox, isTabBox, TAB_BOXES } from "@/lib/collab/boxes";
import { isTab, MAX_TABS, moveTab, resetTabs, tabId, toggleTab } from "@/lib/collab/sidebarPrefs";
import { cn } from "@/lib/cn";
import { ReorderButtons } from "@/components/settings2/bits";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog } from "@/components/ui/dialog";
import { AccountDot, boxIcon, FolderGlyph } from "./bits";

type Entry = { id: string; label: string; icon: ReactNode; note?: string; depth?: number; title?: string };

function Row({
  entry,
  checked,
  disabled,
  onToggle,
  reorder,
}: {
  entry: Entry;
  checked: boolean;
  disabled?: boolean;
  onToggle: () => void;
  reorder?: ReactNode;
}) {
  const { t } = useTranslation("collab");
  const id = useId();
  return (
    <li className="flex h-8 items-center gap-2.5 rounded-md pr-1 hover:bg-hover/60" style={{ paddingLeft: 8 + (entry.depth ?? 0) * 16 }} title={entry.title}>
      <Checkbox id={id} checked={checked} disabled={disabled} onCheckedChange={onToggle} aria-label={t("tabs.show", { name: entry.label })} />
      <label htmlFor={id} className={cn("flex min-w-0 flex-1 cursor-default items-center gap-2", disabled && "opacity-50")}>
        <span className="flex size-4 shrink-0 items-center justify-center text-fg-3 [&_svg]:size-3.5">{entry.icon}</span>
        <span className="min-w-0 truncate text-[12.5px] text-fg">{entry.label}</span>
        {entry.note && <span className="shrink-0 truncate text-[11px] text-fg-4">{entry.note}</span>}
      </label>
      {reorder}
    </li>
  );
}

export function CustomizeTabs({
  open,
  onOpenChange,
  prefs,
  onChange,
  scope,
  accounts,
  folderMap,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  prefs: SidebarPrefs;
  onChange: (p: SidebarPrefs) => void;
  /** The account the bar is showing, when it is one: its folders are offered; otherwise every account's. */
  scope: MailAccount | undefined;
  accounts: MailAccount[];
  folderMap: Record<string, MailFolder[] | undefined>;
}) {
  const { t } = useTranslation("collab");
  const scopeFolders = scope ? folderMap[scope.id] : undefined;
  const offered = scope ? [scope] : accounts;
  const accountOf = (id: string) => accounts.find((a) => a.id === id);
  const folderById = (id: string) => offered.flatMap((a) => folderMap[a.id] ?? []).find((f) => f.id === id);

  const boxEntry = (b: (typeof TAB_BOXES)[number]): Entry => ({
    id: b,
    label: t(`box.${b}`),
    icon: boxIcon[b],
    note: scope && !hasBox(scope, scopeFolders, b) ? t("tabs.missing") : undefined,
  });
  const folderEntry = (f: MailFolder, depth = 0): Entry => {
    const a = accountOf(f.account_id);
    return {
      id: tabId.folder(f.id),
      label: f.name,
      icon: <FolderGlyph folder={f} />,
      title: f.path,
      depth,
      // Without one account in view, a folder tab shows only in its own account.
      note: !scope && a && accounts.length > 1 ? t("tabs.otherAccount", { name: accountName(a) }) : undefined,
    };
  };

  // The tabs, in order, that this dialog can name: every box, and the offered accounts' folders.
  const shown: Entry[] = [];
  for (const id of prefs.mail_tabs) {
    if (isTabBox(id)) shown.push(boxEntry(id));
    else if (id.startsWith("folder:")) {
      const f = folderById(id.slice(7));
      if (f) shown.push(folderEntry(f));
    }
  }
  const visible = shown.map((e) => e.id);
  const full = prefs.mail_tabs.length >= MAX_TABS;
  const moreBoxes = TAB_BOXES.filter((b) => !isTab(prefs, b)).map(boxEntry);
  const moreFolders = (a: MailAccount) =>
    flattenWithout(buildFolderTree(folderMap[a.id] ?? []), (f) => isTab(prefs, tabId.folder(f.id))).map(({ folder, depth, label }) => ({
      ...folderEntry(folder, depth),
      label,
      note: undefined,
    }));
  const groups = offered.map((a) => ({ account: a, items: moreFolders(a) })).filter((g) => g.items.length > 0);

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      size="md"
      title={t("tabs.title")}
      description={t("tabs.description")}
      footer={
        <>
          <Button variant="ghost" className="mr-auto" onClick={() => onChange(resetTabs(prefs))}>
            {t("tabs.reset")}
          </Button>
          <Button variant="primary" onClick={() => onOpenChange(false)}>
            {t("tabs.done")}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-5 pb-2">
        <section>
          <h3 className="label-caps mb-1.5">{t("tabs.shown")}</h3>
          <div className="rounded-lg border border-line p-1">
            {shown.length ? (
              <ol className="flex flex-col">
                {shown.map((e, i) => (
                  <Row
                    key={e.id}
                    entry={e}
                    checked
                    onToggle={() => onChange(toggleTab(prefs, e.id))}
                    reorder={
                      <ReorderButtons
                        label={e.label}
                        first={i === 0}
                        last={i === shown.length - 1}
                        onUp={() => onChange(moveTab(prefs, e.id, -1, visible))}
                        onDown={() => onChange(moveTab(prefs, e.id, 1, visible))}
                      />
                    }
                  />
                ))}
              </ol>
            ) : (
              <p className="px-2 py-2 text-[12px] text-fg-3">{t("tabs.noTabs")}</p>
            )}
          </div>
          {full && <p className="mt-1.5 text-[11.5px] text-fg-4">{t("tabs.limit")}</p>}
        </section>

        {(moreBoxes.length > 0 || groups.length > 0) && (
          <section>
            <h3 className="label-caps mb-1.5">{t("tabs.more")}</h3>
            <div className="flex flex-col rounded-lg border border-line p-1">
              {moreBoxes.length > 0 && (
                <ul className="flex flex-col">
                  {moreBoxes.map((e) => (
                    <Row key={e.id} entry={e} checked={false} disabled={full} onToggle={() => onChange(toggleTab(prefs, e.id))} />
                  ))}
                </ul>
              )}
              {groups.map(({ account, items }, gi) => (
                <Fragment key={account.id}>
                  <div className={cn("mx-1 flex h-7 items-center gap-2 px-1 text-[11.5px] font-medium text-fg-3", (gi > 0 || moreBoxes.length > 0) && "mt-1 border-t border-line-soft pt-1")}>
                    {scope ? (
                      t("tabs.folders")
                    ) : (
                      <>
                        <AccountDot color={account.color} className="size-2" />
                        <span className="truncate">{accountName(account)}</span>
                      </>
                    )}
                  </div>
                  <ul className="flex flex-col">
                    {items.map((e) => (
                      <Row key={e.id} entry={e} checked={false} disabled={full} onToggle={() => onChange(toggleTab(prefs, e.id))} />
                    ))}
                  </ul>
                </Fragment>
              ))}
            </div>
          </section>
        )}
      </div>
    </Dialog>
  );
}
