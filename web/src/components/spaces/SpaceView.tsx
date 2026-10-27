// The picked Space in the sidebar (contracts/spaces.md "Web app"): every row of it from every
// workspace, in its sections, and nothing the header already says. Rows and whole sections of
// the open workspace are the sidebar's own; a row of another workspace carries that
// workspace's small badge ("In Lumen Labs") and opens there, the focus moving without a
// reload, the Space still picked. A whole section of another workspace carries the badge on
// its title. Section titles the person wrote read like the built-in ones and fold.

import { Pencil } from "lucide-react";
import { Fragment, useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Section, SectionMarkCtx } from "@/app/sidebarParts";
import { useWaitingFor } from "@/components/tickets/useUrgentChats";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty";
import type { SidebarSectionId, Space, SpaceItem, SpaceSection } from "@/lib/api/types";
import { hid, livePins } from "@/lib/collab/sidebarPrefs";
import { cn } from "@/lib/cn";
import { folders as ticketFolders } from "@/lib/folders";
import { useSession } from "@/lib/session";
import { canSeeTeam, useOverview, useWorkspacesData, type WorkspaceData } from "@/lib/spaces/hooks";
import { isEverywhere, isSectionId, itemKey, replaceSpace, showSpace, SPACE_PAGES, updateItem, updateSection } from "@/lib/spaces/model";
import { spaceUi } from "@/lib/spaces/store";
import { buildEverywhereRow, buildRow, InWorkspace, NavItemRow, useMailLocation, useRowLabels, type RowContext, type RowModel } from "./rows";
import { RenameRow, SpaceRowMenu } from "./SpaceRowMenu";
import { useSpaceActions } from "./actions";
import { defaultSidebarPrefs } from "@/lib/collab/sidebarPrefs";

/** The open workspace's sections, in its order: the sidebar without Spaces. */
export function SectionList({ ids, render }: { ids: SidebarSectionId[]; render: (id: SidebarSectionId, first: boolean) => ReactNode }) {
  return (
    <div className="[&>*:first-child]:!mt-0">
      {ids.map((id, i) => (
        <Fragment key={id}>{render(id, i === 0)}</Fragment>
      ))}
    </div>
  );
}

const NO_PREFS = defaultSidebarPrefs();

/** One of the Space's sections: under the person's title, read and folded like a built-in section; untitled, its rows alone. */
function SpaceSectionBlock({ section, first, rail, onFold, children }: { section: SpaceSection; first: boolean; rail: boolean; onFold: (folded: boolean) => void; children: ReactNode }) {
  const title = section.title.trim();
  if (!title)
    return (
      <div className={cn(!first && "mt-5")}>
        {rail && !first && <div aria-hidden className="mx-4 mb-2 h-px bg-chrome-line" />}
        <div className="[&>*:first-child]:!mt-0">{children}</div>
      </div>
    );
  return (
    <SectionMarkCtx.Provider value={null}>
      <Section id={`space-${section.id}`} label={title} rail={rail} first={first} folded={section.collapsed} onFold={onFold}>
        <div className="[&>*:first-child]:!mt-0">{children}</div>
      </Section>
    </SectionMarkCtx.Provider>
  );
}

/** A whole built-in section of another workspace: that workspace's rows, by its own preferences. */
function RemoteSection({ d, id, first, rail, mark, ctx }: { d: WorkspaceData; id: SidebarSectionId; first: boolean; rail: boolean; mark: ReactNode; ctx: RowContext }) {
  const p = d.prefs;
  const hidden = new Set(p.hidden);
  const row = (kind: string, rid: string, via?: SidebarSectionId[]) => buildRow({ kind, id: rid }, d, { ...ctx, via });
  let list: (RowModel | null)[] = [];
  switch (id) {
    case "favorites":
      // Without Collab its mail favorites build no row (buildRow), the ticket ones do.
      list = livePins(p, (pin) => row(pin.kind, pin.id, ["favorites"])).map((x) => x.target);
      break;
    case "inbox":
      list = ticketFolders.filter((f) => !hidden.has(hid.ticketFolder(f.id))).map((f) => row("ticket_folder", f.id));
      break;
    case "mail": {
      if (!d.collab) break;
      const accounts = (d.accounts ?? []).filter((a) => !hidden.has(hid.account(a.id)));
      if (p.unified_inbox && (d.accounts?.length ?? 0) >= 2 && !hidden.has(hid.box("inbox"))) list.push(row("box", "inbox"));
      if (!hidden.has(hid.box("shared")) && d.mailCounts?.boxes.shared) list.push(row("box", "shared"));
      if (!hidden.has(hid.box("assigned")) && d.mailCounts?.boxes.assigned) list.push(row("box", "assigned"));
      list.push(...accounts.map((a) => row("account", a.id)));
      break;
    }
    case "teams": {
      const byName = (a: { name: string }, b: { name: string }) => a.name.localeCompare(b.name);
      const visible = (d.teams ?? []).filter((x) => canSeeTeam(d, x) && !hidden.has(hid.team(x.id)));
      const mine = visible.filter((x) => d.myTeams.has(x.id)).sort(byName);
      // The person's teams; someone on none of them gets the teams they can see.
      list = [...(mine.length > 1 ? [row("team", "mine")] : []), ...(mine.length ? mine : visible.sort(byName)).map((x) => row("team", x.id))];
      break;
    }
    case "views":
      list = [...(d.views ?? [])]
        .filter((v) => !hidden.has(hid.view(v.id)))
        .sort((a, b) => a.position - b.position)
        .map((v) => row("view", v.id));
      break;
    case "workspace":
      list = SPACE_PAGES.map((pg) => row("page", pg));
      break;
  }
  const models = list.filter((m): m is RowModel => !!m);
  if (!models.length) return null;
  return (
    <SectionMarkCtx.Provider value={mark}>
      <Section id={`ws-${d.id}-${id}`} label={ctx.labels.section(id)} rail={rail} first={first}>
        {models.map((m) => (
          <NavItemRow key={m.key} model={m} rail={rail} prefs={p} ws={d.id} />
        ))}
      </Section>
    </SectionMarkCtx.Provider>
  );
}

export function SpaceView({
  spaces,
  space,
  rail,
  renderSection,
}: {
  spaces: Space[];
  /** The picked Space. */
  space: Space;
  rail: boolean;
  /** Draws a built-in section of the open workspace. */
  renderSection: (id: SidebarSectionId, first: boolean) => ReactNode;
}) {
  const { t } = useTranslation("spaces");
  const memberships = useSession((s) => s.memberships);
  const members = useMemo(() => new Set(memberships.map((m) => m.workspace.id)), [memberships]);
  const actions = useSpaceActions(spaces);
  const wids = useMemo(() => {
    const out: string[] = [];
    for (const sec of space.sections) for (const i of sec.items) if (members.has(i.workspace_id) && !out.includes(i.workspace_id)) out.push(i.workspace_id);
    return out;
  }, [space, members]);
  const data = useWorkspacesData(wids, { folders: (w) => space.sections.some((sec) => sec.items.some((i) => i.workspace_id === w && i.kind === "folder")) });
  const everywhere = space.sections.some((sec) => sec.items.some(isEverywhere));
  const overview = useOverview(everywhere);
  const shown = showSpace(space, { members, collab: (w) => data.get(w)?.collab });
  const labels = useRowLabels();
  const here = useMailLocation();
  const waitingFor = useWaitingFor();
  const [renaming, setRenaming] = useState<string | null>(null);

  // Only another workspace's rows say which workspace they are in: the open one's are the sidebar's own.
  const badgeFor = (d: WorkspaceData): ReactNode => (shown.marks && !d.open ? <InWorkspace name={d.name} logo={d.logo} /> : null);
  const change = (next: Space) => actions.put(replaceSpace(spaces, next));

  const singleRow = (section: SpaceSection, item: SpaceItem) => {
    const d = isEverywhere(item) ? null : data.get(item.workspace_id);
    if (!isEverywhere(item) && !d) return null;
    const model = d ? buildRow(item, d, { labels, here: d.open ? here : undefined, waiting: d.open ? waitingFor : undefined }) : buildEverywhereRow(item.id, labels, [...overview.values()]);
    if (!model) return null;
    const key = itemKey(item);
    const rk = `${section.id}|${key}`;
    if (renaming === rk && !rail)
      return (
        <RenameRow
          key={key}
          initial={item.name ?? model.label}
          placeholder={model.label}
          label={t("row.renameLabel", { name: model.label })}
          onDone={(v) => {
            setRenaming(null);
            if (v === null) return;
            const name = v.trim();
            change(updateItem(space, key, { name: !name || name === model.label ? null : name }));
          }}
        />
      );
    return (
      <NavItemRow
        key={key}
        model={model}
        rail={rail}
        prefs={d?.prefs ?? NO_PREFS}
        own={item.badge}
        label={item.name}
        ws={d && !d.open ? d.id : undefined}
        mark={d ? badgeFor(d) : null}
        menu={(row) => (
          <SpaceRowMenu spaces={spaces} space={space} item={item} model={model} put={actions.put} onRename={() => setRenaming(rk)} rail={rail} label={item.name || model.label}>
            {row}
          </SpaceRowMenu>
        )}
      />
    );
  };

  const renderItems = (section: SpaceSection, items: SpaceItem[]) => {
    // Runs of single rows sit together; a whole section brings its own title.
    const blocks: ({ kind: "section"; item: SpaceItem } | { kind: "rows"; items: SpaceItem[] })[] = [];
    for (const item of items) {
      const last = blocks[blocks.length - 1];
      if (item.kind === "section") blocks.push({ kind: "section", item });
      else if (last?.kind === "rows") last.items.push(item);
      else blocks.push({ kind: "rows", items: [item] });
    }
    return blocks.map((b, bi) => {
      if (b.kind === "section") {
        const it = b.item;
        if (!isSectionId(it.id)) return null;
        const d = data.get(it.workspace_id);
        if (!d) return null;
        // Keyed by item, not by which workspace is open: opening a row elsewhere re-marks rows in place.
        if (d.open)
          return (
            <SectionMarkCtx.Provider key={itemKey(it)} value={null}>
              {renderSection(it.id, bi === 0)}
            </SectionMarkCtx.Provider>
          );
        return <RemoteSection key={itemKey(it)} d={d} id={it.id} first={bi === 0} rail={rail} mark={badgeFor(d)} ctx={{ labels }} />;
      }
      return (
        <div key={`rows-${bi}`} className={cn("flex flex-col gap-px", bi > 0 && "mt-3")}>
          {b.items.map((item) => singleRow(section, item))}
        </div>
      );
    });
  };

  if (!shown.sections.length)
    return rail ? null : (
      <EmptyState
        compact
        art="generic"
        title={t("view.empty")}
        description={t("view.emptyHint")}
        action={
          <Button size="xs" icon={<Pencil />} onClick={() => spaceUi.edit({ kind: "space", id: space.id })}>
            {t("view.emptyEdit")}
          </Button>
        }
      />
    );

  return (
    <>
      {shown.sections.map(({ section, items }, si) => (
        <SpaceSectionBlock key={section.id} section={section} first={si === 0} rail={rail} onFold={(collapsed) => change(updateSection(space, section.id, { collapsed }))}>
          {renderItems(section, items)}
        </SpaceSectionBlock>
      ))}
    </>
  );
}
