// Settings > AI agent > Procedures (contracts/agent.md "Procedures"): the
// agent's instructions for one kind of conversation each, switched on and off
// alone, with the editor in a drawer.

import { useQuery } from "@tanstack/react-query";
import { ScrollText, MoreHorizontal, NotebookText, Pencil, Plus, Trash2, Workflow } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { NoAccess, Section, SettingsPage } from "@/components/layout";
import { Button } from "@/components/ui/button";
import { Menu, MenuContent, MenuItem, MenuSeparator, MenuTrigger } from "@/components/ui/menu";
import { Switch } from "@/components/ui/switch";
import { Tooltip } from "@/components/ui/tooltip";
import { useConfirm } from "@/components/ui/confirm";
import { IconTile, ListSkeleton } from "@/components/settings2/bits";
import { MotionList, MotionRow } from "@/components/settings2/motion";
import { SettingsEmpty } from "@/components/settings2/SettingsEmpty";
import { useListMutation } from "@/components/settings2/optimistic";
import { AgentOffNotice } from "@/components/agent/bits";
import { agentSettings } from "@/components/agent/settings";
import { channelsText, ProcedureEditor } from "@/components/agent/procedures";
import { api } from "@/lib/api/endpoints";
import type { Procedure } from "@/lib/api/types";
import { fullDate, relShort } from "@/lib/format";
import { useAI } from "@/lib/ai";
import { MAX_PROCEDURES } from "@/lib/procedures";
import { q, qk } from "@/lib/queries";
import { atLeast, useRole } from "@/lib/session";
import { AI_TONE } from "@/lib/tones";
import { cn } from "@/lib/cn";

type Editing = { procedure: Procedure | null } | null;

export default function AgentProcedures() {
  const { t } = useTranslation("agent");
  const admin = atLeast(useRole(), "admin");
  const ai = useAI();
  const confirm = useConfirm();
  const { data, isLoading } = useQuery({ ...q.procedures(), enabled: admin });
  const { data: bots } = useQuery({ ...q.bots(), enabled: admin });
  const [editing, setEditing] = useState<Editing>(null);

  const toggle = useListMutation<Procedure, { id: string; enabled: boolean }>(
    qk.procedures,
    (v) => api.ai.procedures.update(v.id, { enabled: v.enabled }),
    (rows, v) => rows.map((r) => (r.id === v.id ? { ...r, enabled: v.enabled } : r)),
    { errorText: t("procedures.updateError") },
  );
  const remove = useListMutation<Procedure, string>(qk.procedures, (id) => api.ai.procedures.remove(id), (rows, id) => rows.filter((r) => r.id !== id), {
    errorText: t("procedures.deleteError"),
  });

  const list = data ?? [];
  const full = list.length >= MAX_PROCEDURES;
  const botNames = (ids: string[]) => (ids.length ? ids.map((id) => bots?.find((b) => b.id === id)?.name ?? "").filter(Boolean).join(", ") : t("procedures.everyBot"));

  const askDelete = (p: Procedure) =>
    confirm.show(<>{t("procedures.deleteBody", { name: p.name })}</>, () => remove.mutateAsync(p.id), {
      title: t("procedures.deleteTitle"),
      confirmLabel: t("procedures.delete"),
      danger: true,
    });

  const newButton = (
    <Tooltip content={full ? t("procedures.limit") : null}>
      <span>
        <Button variant="primary" icon={<Plus className="size-3.5" />} disabled={full} onClick={() => setEditing({ procedure: null })}>
          {t("procedures.new")}
        </Button>
      </span>
    </Tooltip>
  );

  if (!admin)
    return (
      <SettingsPage title={t("procedures.title")}>
        <NoAccess what={t("procedures.what")} />
      </SettingsPage>
    );

  return (
    <SettingsPage wide title={t("procedures.title")} description={t("procedures.description")} actions={list.length > 0 ? newButton : undefined}>
      {ai.config && !agentSettings(ai.features).widget_answers && <AgentOffNotice className="-mb-3" />}
      <Section flush title={data && list.length > 0 ? t("procedures.listCount", { n: list.length, max: MAX_PROCEDURES }) : undefined}>
        {isLoading ? (
          <ListSkeleton rows={3} />
        ) : !list.length ? (
          <SettingsEmpty
            art="ai"
            title={t("procedures.empty.title")}
            description={t("procedures.empty.description")}
            action={
              <Button variant="primary" icon={<Plus className="size-3.5" />} onClick={() => setEditing({ procedure: null })}>
                {t("procedures.new")}
              </Button>
            }
            tips={[
              { label: t("procedures.empty.tipWhen"), icon: <ScrollText /> },
              { label: t("procedures.empty.tipWiki"), icon: <NotebookText /> },
            ]}
          />
        ) : (
          <MotionList>
            {list.map((p) => (
              <MotionRow key={p.id}>
                <ProcedureRow
                  p={p}
                  bots={botNames(p.bot_ids)}
                  onOpen={() => setEditing({ procedure: p })}
                  onToggle={(enabled) => toggle.mutate({ id: p.id, enabled })}
                  onDelete={() => askDelete(p)}
                />
              </MotionRow>
            ))}
          </MotionList>
        )}
      </Section>
      <ProcedureEditor open={!!editing} procedure={editing?.procedure ? (list.find((x) => x.id === editing.procedure?.id) ?? editing.procedure) : null} onClose={() => setEditing(null)} />
    </SettingsPage>
  );
}

function ProcedureRow({ p, bots, onOpen, onToggle, onDelete }: { p: Procedure; bots: string; onOpen: () => void; onToggle: (v: boolean) => void; onDelete: () => void }) {
  const { t } = useTranslation("agent");
  return (
    <div
      role="button"
      tabIndex={0}
      onClick={onOpen}
      onKeyDown={(e) => {
        if ((e.key === "Enter" || e.key === " ") && e.target === e.currentTarget) {
          e.preventDefault();
          onOpen();
        }
      }}
      className="group flex cursor-pointer items-center gap-3 px-4 py-2.5 outline-none transition-colors hover:bg-sunken focus-visible:bg-sunken md:grid md:grid-cols-[minmax(0,1fr)_160px_84px_64px] md:gap-4"
    >
      <div className={cn("flex min-w-0 flex-1 items-center gap-3", !p.enabled && "opacity-60")}>
        <IconTile tone={AI_TONE}>{p.channels.length ? <ScrollText /> : <Workflow />}</IconTile>
        <div className="min-w-0">
          <div className="truncate text-[12.5px] font-medium text-fg">{p.name}</div>
          <div className="truncate text-[12px] text-fg-3">{p.when}</div>
        </div>
      </div>
      <div className="hidden min-w-0 text-[11.5px] text-fg-3 md:block">
        <div className="truncate">{channelsText(p.channels, t)}</div>
        <div className="truncate text-fg-4">{bots}</div>
      </div>
      <span className="mono hidden text-[11.5px] text-fg-3 md:block">
        <Tooltip content={p.updated_by ? t("procedures.updatedBy", { when: fullDate(p.updated_at), name: p.updated_by.name }) : fullDate(p.updated_at)}>
          <span>{relShort(p.updated_at)}</span>
        </Tooltip>
      </span>
      <div className="flex items-center justify-end gap-1" onClick={(e) => e.stopPropagation()}>
        <Switch checked={p.enabled} onCheckedChange={onToggle} aria-label={t("procedures.toggle", { name: p.name })} />
        <Menu>
          <MenuTrigger asChild>
            <button
              type="button"
              aria-label={t("procedures.actions", { name: p.name })}
              className="flex size-7 items-center justify-center rounded-md text-fg-3 transition-colors hover:bg-hover hover:text-fg"
            >
              <MoreHorizontal className="size-4" />
            </button>
          </MenuTrigger>
          <MenuContent>
            <MenuItem icon={<Pencil />} onSelect={onOpen}>
              {t("procedures.edit")}
            </MenuItem>
            <MenuSeparator />
            <MenuItem danger icon={<Trash2 />} onSelect={onDelete}>
              {t("procedures.delete")}
            </MenuItem>
          </MenuContent>
        </Menu>
      </div>
    </div>
  );
}
