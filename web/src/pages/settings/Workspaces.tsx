// Settings › Account › Workspaces: how the workspaces a person belongs to are arranged
// (contracts/api.md "Several workspaces"): their order in the switcher (drag or
// Alt+Arrow), and which ones are folded away there and left out of the bell and the
// Everywhere lists. Every change applies at once and is put back if the server refuses it.

import { Eye, EyeOff } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Section, SettingsPage } from "@/components/layout";
import { SortableList } from "@/components/settings2/Sortable";
import { EmptyState } from "@/components/ui/empty";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast";
import { Tooltip } from "@/components/ui/tooltip";
import { WorkspaceBadge } from "@/components/workspaces/WorkspaceBadge";
import type { WorkspacePrefsInput } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { useSession } from "@/lib/session";
import { toggleIn, useUpdateWorkspacePrefs, useWorkspaces } from "@/lib/workspaces";

export default function Workspaces() {
  const { t } = useTranslation("workspaces");
  const { prefs, ordered, focus } = useWorkspaces();
  const memberships = useSession((s) => s.memberships);
  const update = useUpdateWorkspacePrefs();
  const save = (b: WorkspacePrefsInput) => update.mutate(b, { onError: () => toast.error(t("customize.failed")) });
  const [announce, setAnnounce] = useState("");

  if (memberships.length < 2) {
    return (
      <SettingsPage title={t("customize.title")} description={t("customize.description")}>
        <EmptyState art="inbox" title={t("customize.single")} description={t("customize.singleHint")} />
      </SettingsPage>
    );
  }
  const hidden = new Set(prefs.hidden);
  const items = ordered.map((m) => ({ id: m.workspace.id, m }));

  return (
    <SettingsPage title={t("customize.title")} description={t("customize.description")}>
      <Section title={t("customize.order")} description={t("customize.orderHint")} flush>
        <SortableList
          items={items}
          handleLabel={(x) => t("customize.drag", { name: x.m.workspace.name })}
          onReorder={(ids) => {
            save({ order: ids });
            const moved = ids.findIndex((id, i) => id !== items[i]?.id);
            if (moved >= 0) setAnnounce(t("customize.moved", { name: items.find((x) => x.id === ids[moved])?.m.workspace.name ?? "", n: moved + 1 }));
          }}
          renderRow={({ m }, handle) => {
            const off = hidden.has(m.workspace.id);
            const current = m.workspace.id === focus;
            return (
              <div className="flex h-11 items-center gap-2.5 px-3">
                {handle}
                <WorkspaceBadge name={m.workspace.name} logo={m.workspace.avatar_url} size="lg" decorative />
                <span className={cn("min-w-0 flex-1 truncate text-[13px]", off ? "text-fg-3" : "text-fg")}>{m.workspace.name}</span>
                {current && <span className="text-[11.5px] text-fg-3">{t("customize.current")}</span>}
                {off && <span className="text-[11.5px] text-fg-4">{t("customize.hiddenNow")}</span>}
                <Tooltip content={t("customize.show", { name: m.workspace.name })}>
                  <span className="flex items-center gap-1.5 text-fg-4 [&_svg]:size-3.5">
                    {off ? <EyeOff aria-hidden /> : <Eye aria-hidden />}
                    <Switch
                      checked={!off}
                      onCheckedChange={(on) => save({ hidden: toggleIn(prefs.hidden, m.workspace.id, !on) })}
                      aria-label={t("customize.show", { name: m.workspace.name })}
                    />
                  </span>
                </Tooltip>
              </div>
            );
          }}
        />
        <span aria-live="polite" className="sr-only">
          {announce}
        </span>
      </Section>
    </SettingsPage>
  );
}
