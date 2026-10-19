import { useTranslation } from "react-i18next";
import { Dialog } from "@/components/ui/dialog";
import { Kbd } from "@/components/ui/kbd";
import { shortcutGroups } from "@/lib/hotkeys";
import { shell, useShell } from "./shellStore";
import { quickSwitchKeys } from "./useAccounts";
import { useCollab } from "@/lib/collab/hooks";

export function ShortcutsSheet() {
  const open = useShell((s) => s.shortcutsOpen);
  const { t } = useTranslation("shell");
  // Shortcuts bound in AppLayout that the shared list may not name yet.
  const extra: Record<string, { keys: string; label: string }[]> = {
    General: [
      { keys: "b", label: t("shortcuts.toggleSidebar") },
      { keys: "g m", label: t("shortcuts.goMine") },
      { keys: "g r", label: t("shortcuts.goReports") },
      { keys: quickSwitchKeys("1-9"), label: t("shortcuts.switchWorkspace") },
    ],
  };
  const collab = useCollab();
  const { t: tc } = useTranslation("collab");
  const groups = shortcutGroups.map((g) => {
    const have = new Set(g.items.map((i) => i.keys));
    return { ...g, items: [...g.items, ...(extra[g.title] ?? []).filter((i) => !have.has(i.keys))] };
  });
  // Collab's mail list and conversation, while the workspace has it.
  if (collab.enabled)
    groups.push({
      title: tc("shortcuts.title"),
      items: [
        { keys: "j k", label: tc("shortcuts.move") },
        { keys: "enter", label: tc("shortcuts.open") },
        { keys: "e", label: tc("shortcuts.archive") },
        { keys: "#", label: tc("shortcuts.trash") },
        { keys: "!", label: tc("shortcuts.spam") },
        { keys: "s", label: tc("shortcuts.star") },
        { keys: "shift u", label: tc("shortcuts.unread") },
        { keys: "shift i", label: tc("shortcuts.read") },
        { keys: "m", label: tc("shortcuts.movePicker") },
        { keys: "l", label: tc("shortcuts.labelPicker") },
        { keys: "r", label: tc("shortcuts.reply") },
        { keys: "a", label: tc("shortcuts.replyAll") },
        { keys: "f", label: tc("shortcuts.forward") },
        { keys: "c", label: tc("shortcuts.compose") },
        { keys: "x", label: tc("shortcuts.select") },
        { keys: "/", label: tc("shortcuts.search") },
        { keys: "g i", label: tc("shortcuts.inbox") },
      ],
    });
  return (
    <Dialog open={open} onOpenChange={(o) => shell.shortcuts(o)} title="Keyboard shortcuts" description="Most things in opendesk work without a mouse." size="lg">
      <div className="grid grid-cols-1 gap-x-8 gap-y-6 pb-2 sm:grid-cols-2">
        {groups.map((g) => (
          <section key={g.title} className={g.title === "General" ? "sm:row-span-2" : undefined}>
            <h3 className="label-caps mb-1.5">{g.title}</h3>
            <ul className="flex flex-col">
              {g.items.map((i) => (
                <li key={i.label} className="flex h-8 items-center justify-between gap-4 border-b border-line-soft text-[12.5px] text-fg-2 last:border-0">
                  <span className="truncate">{i.label}</span>
                  <Kbd>{i.keys}</Kbd>
                </li>
              ))}
            </ul>
          </section>
        ))}
      </div>
    </Dialog>
  );
}
