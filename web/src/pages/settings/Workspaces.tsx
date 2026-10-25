// Settings › Account › Workspaces: how the workspaces a person belongs to share the
// sidebar (contracts/api.md "Workspaces together"). Together or Separate, grouped or
// mixed, the Everywhere lists, the order (drag or Alt+Arrow) and what each workspace
// shows. Every change applies at once and is put back if the server refuses it.

import { useQuery } from "@tanstack/react-query";
import { Eye, EyeOff, LayoutList, Rows3 } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Row, Section, SettingsPage } from "@/components/layout";
import { SortableList } from "@/components/settings2/Sortable";
import { EmptyState } from "@/components/ui/empty";
import { Segmented } from "@/components/ui/tabs";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast";
import { Tooltip } from "@/components/ui/tooltip";
import { WorkspaceBadge } from "@/components/workspaces/WorkspaceBadge";
import { inWorkspace } from "@/lib/api/client";
import type { Membership, TogetherLayout, TogetherMode, WorkspacePrefsInput } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { folders } from "@/lib/folders";
import { q } from "@/lib/queries";
import { useSession } from "@/lib/session";
import { teamsOf } from "@/lib/ticketCache";
import { itemId, orderWorkspaces, toggleIn, useTogether, useUpdateWorkspacePrefs } from "@/lib/together";

export default function Workspaces() {
  const { t } = useTranslation("together");
  const { prefs, ordered, focus } = useTogether();
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
  const together = prefs.mode === "together";
  const hidden = new Set(prefs.hidden);
  const items = ordered.map((m) => ({ id: m.workspace.id, m }));

  return (
    <SettingsPage title={t("customize.title")} description={t("customize.description")}>
      <Section>
        <Row label={t("customize.mode")} description={together ? t("customize.togetherHint") : t("customize.separateHint")}>
          <Segmented<TogetherMode>
            value={prefs.mode}
            onChange={(mode) => save({ mode })}
            options={[
              { value: "together", label: t("customize.together") },
              { value: "separate", label: t("customize.separate") },
            ]}
          />
        </Row>
        {together && (
          <>
            <Row label={t("customize.layout")} description={prefs.layout === "grouped" ? t("customize.groupedHint") : t("customize.mixedHint")}>
              <Segmented<TogetherLayout>
                value={prefs.layout}
                onChange={(layout) => save({ layout })}
                options={[
                  { value: "grouped", label: t("customize.grouped"), icon: <Rows3 /> },
                  { value: "mixed", label: t("customize.mixed"), icon: <LayoutList /> },
                ]}
              />
            </Row>
            <Row label={t("customize.everywhere")} description={t("customize.everywhereHint")}>
              <Switch checked={prefs.everywhere} onCheckedChange={(everywhere) => save({ everywhere })} aria-label={t("customize.everywhere")} />
            </Row>
          </>
        )}
      </Section>

      {together && (
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
      )}

      {together && (
        <Section title={t("customize.items")} description={t("customize.itemsHint")}>
          <div className="flex flex-col gap-5">
            {orderWorkspaces(memberships, prefs.order)
              .filter((m) => !hidden.has(m.workspace.id) && !m.suspended)
              .map((m) => (
                <WorkspaceItems key={m.workspace.id} m={m} hiddenItems={prefs.items.hidden} onChange={(h) => save({ items: { ...prefs.items, hidden: h } })} />
              ))}
          </div>
        </Section>
      )}
    </SettingsPage>
  );
}

/** One workspace's rows: inbox folders, teams and views, each with a switch. */
function WorkspaceItems({ m, hiddenItems, onChange }: { m: Membership; hiddenItems: string[]; onChange: (hidden: string[]) => void }) {
  const { t } = useTranslation("together");
  const { t: tNav } = useTranslation("navigation");
  const me = useSession((s) => s.user?.id ?? "");
  const wid = m.workspace.id;
  const teams = useQuery(inWorkspace(wid, () => q.teams())).data ?? [];
  const views = useQuery(inWorkspace(wid, () => q.views())).data ?? [];
  const mine = new Set(teamsOf(teams, me));
  const seen = teams.filter((x) => mine.has(x.id) || !x.private);
  const toggle = (id: string, on: boolean) => onChange(toggleIn(hiddenItems, id, !on));
  const line = (id: string, label: string) => {
    const on = !hiddenItems.includes(id);
    return (
      <label key={id} className="flex h-8 cursor-pointer items-center gap-2.5 rounded-md px-2 text-[12.5px] hover:bg-hover">
        <span className={cn("min-w-0 flex-1 truncate", on ? "text-fg" : "text-fg-3")}>{label}</span>
        <Switch checked={on} onCheckedChange={(v) => toggle(id, v)} aria-label={`${label}, ${m.workspace.name}`} />
      </label>
    );
  };
  return (
    <div>
      <div className="mb-1.5 flex items-center gap-2">
        <WorkspaceBadge name={m.workspace.name} logo={m.workspace.avatar_url} size="md" decorative />
        <span className="text-[13px] font-medium">{m.workspace.name}</span>
      </div>
      <div className="grid gap-x-6 sm:grid-cols-3">
        <div>
          <div className="label-caps px-2 py-1">{t("customize.folders")}</div>
          {folders.filter((f) => f.id !== "solved" && f.id !== "spam").map((f) => line(itemId(wid, "ticket_folder", f.id), tNav(f.labelKey)))}
        </div>
        <div>
          <div className="label-caps px-2 py-1">{t("customize.teams")}</div>
          {seen.length ? seen.map((x) => line(itemId(wid, "team", x.id), x.name)) : <p className="px-2 py-1 text-[12px] text-fg-4">{t("customize.none")}</p>}
        </div>
        <div>
          <div className="label-caps px-2 py-1">{t("customize.views")}</div>
          {views.length ? [...views].sort((a, b) => a.position - b.position).map((v) => line(itemId(wid, "view", v.id), v.name)) : <p className="px-2 py-1 text-[12px] text-fg-4">{t("customize.none")}</p>}
        </div>
      </div>
    </div>
  );
}
