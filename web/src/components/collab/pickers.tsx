// Collab pickers: move to a folder, labels (Gmail labels, Outlook categories), teammates to assign, and the
// audience editor that shares an account or a single conversation.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Archive, Check, Globe, Inbox, Plus, ShieldAlert, Trash2, Users, X } from "lucide-react";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { api } from "@/lib/api/endpoints";
import type { MailAudience, MailFolder, MailThreadAction, Member } from "@/lib/api/types";
import { buildFolderTree, flattenTree } from "@/lib/collab/boxes";
import { cn } from "@/lib/cn";
import { qk, q, upsertInList } from "@/lib/queries";
import { Avatar } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { Picker, type PickerItem } from "@/components/ui/picker";
import { toast } from "@/components/ui/toast";
import { TeamDot } from "@/components/tickets/teams";
import { FolderGlyph } from "./bits";

export type MoveTarget = { action: MailThreadAction; folderId?: string; label: string };

/** Move to: the inbox, archive, spam, trash or any folder of the account; typing a new name creates it at the provider first. */
export function MovePicker({
  accountId,
  folders,
  open,
  onOpenChange,
  onMove,
  trigger,
  side = "bottom",
  align = "start",
}: {
  accountId: string | null;
  folders: MailFolder[];
  open?: boolean;
  onOpenChange?: (o: boolean) => void;
  onMove: (t: MoveTarget) => void;
  trigger: ReactNode;
  side?: "top" | "bottom" | "left" | "right";
  align?: "start" | "center" | "end";
}) {
  const { t } = useTranslation("collab");
  const qc = useQueryClient();
  const create = useMutation({
    mutationFn: (name: string) => api.collab.accounts.createFolder(accountId!, { name }),
    onSuccess: (f) => {
      upsertInList(qc, qk.mailFolders(f.account_id), f);
      toast.success(t("action.folderCreated"));
      onMove({ action: "move", folderId: f.id, label: f.name });
    },
    onError: (e) => toast.error(e),
  });
  const special: (PickerItem & { target: MoveTarget })[] = [
    { value: "a:inbox", label: t("box.inbox"), icon: <Inbox />, target: { action: "inbox", label: t("box.inbox") } },
    { value: "a:archive", label: t("box.archive"), icon: <Archive />, target: { action: "archive", label: t("box.archive") } },
    { value: "a:spam", label: t("box.spam"), icon: <ShieldAlert />, target: { action: "spam", label: t("box.spam") } },
    { value: "a:trash", label: t("box.trash"), icon: <Trash2 />, target: { action: "trash", label: t("box.trash") } },
  ];
  const user = flattenTree(buildFolderTree(folders))
    .filter(({ node }) => node.folder)
    .map(({ node, depth }) => ({
      value: `f:${node.folder!.id}`,
      label: node.folder!.path || node.name,
      icon: <span style={{ marginLeft: depth * 10 }} className="flex"><FolderGlyph folder={node.folder} /></span>,
      group: t("action.move"),
      target: { action: "move" as const, folderId: node.folder!.id, label: node.name },
    }));
  const all = [...special.map((s) => ({ ...s, group: t("nav.mail") })), ...user];
  return (
    <Picker
      open={open}
      onOpenChange={onOpenChange}
      trigger={trigger}
      side={side}
      align={align}
      width={260}
      placeholder={t("action.movePlaceholder")}
      emptyText={t("action.noFolders")}
      value={null}
      items={all}
      onSelect={(v) => {
        const hit = all.find((x) => x.value === v);
        if (hit) onMove(hit.target);
      }}
      onCreate={accountId ? (name) => create.mutate(name) : undefined}
      createLabel={t("action.createFolder")}
    />
  );
}

/** Labels (Gmail labels, Outlook categories): those every selected conversation has are checked; toggling adds or removes one; typing a new name creates it at the provider first. */
export function LabelsPicker({
  accountId,
  folders,
  applied,
  open,
  onOpenChange,
  onToggle,
  trigger,
  side = "bottom",
  align = "start",
}: {
  /** The account labels are created in. */
  accountId?: string | null;
  folders: MailFolder[];
  applied: string[];
  open?: boolean;
  onOpenChange?: (o: boolean) => void;
  onToggle: (folderId: string, on: boolean, name: string) => void;
  trigger: ReactNode;
  side?: "top" | "bottom" | "left" | "right";
  align?: "start" | "center" | "end";
}) {
  const { t } = useTranslation("collab");
  const qc = useQueryClient();
  const labels = folders.filter((f) => f.kind === "label" && !f.role);
  const create = useMutation({
    mutationFn: (name: string) => api.collab.accounts.createFolder(accountId!, { name, kind: "label" }),
    onSuccess: (f) => {
      upsertInList(qc, qk.mailFolders(f.account_id), f);
      toast.success(t("action.labelCreated"));
      onToggle(f.id, true, f.name);
    },
    onError: (e) => toast.error(e),
  });
  return (
    <Picker
      multi
      open={open}
      onOpenChange={onOpenChange}
      trigger={trigger}
      side={side}
      align={align}
      width={260}
      placeholder={t("action.labelPlaceholder")}
      emptyText={t("action.noFolders")}
      values={applied}
      items={labels.map((l) => ({ value: l.id, label: l.path || l.name, icon: <FolderGlyph folder={l} /> }))}
      onChange={(next) => {
        const added = next.find((x) => !applied.includes(x));
        const removed = applied.find((x) => !next.includes(x));
        const id = added ?? removed;
        if (!id) return;
        onToggle(id, !!added, labels.find((l) => l.id === id)?.name ?? "");
      }}
      onCreate={accountId ? (name) => create.mutate(name) : undefined}
      createLabel={t("action.createLabel")}
    />
  );
}

/** Teammates (agents and above) to assign; each change saves at once. */
export function AssignPicker({
  value,
  onChange,
  trigger,
  open,
  onOpenChange,
  align = "end",
}: {
  value: string[];
  onChange: (ids: string[]) => void;
  trigger: ReactNode;
  open?: boolean;
  onOpenChange?: (o: boolean) => void;
  align?: "start" | "center" | "end";
}) {
  const { t } = useTranslation("collab");
  const { data: members } = useQuery(q.members());
  return (
    <Picker
      multi
      open={open}
      onOpenChange={onOpenChange}
      trigger={trigger}
      align={align}
      width={260}
      placeholder={t("assign.placeholder")}
      values={value}
      items={(members ?? [])
        .filter((m) => m.role !== "viewer")
        .map((m) => ({ value: m.user.id, label: m.user.name || m.user.email, icon: <Avatar name={m.user.name || m.user.email} src={m.user.avatar_url} size="xs" />, keywords: [m.user.email] }))}
      onChange={onChange}
      footer={<p className="px-2 py-1 text-[11px] text-fg-4">{t("assign.hint")}</p>}
    />
  );
}

export const emptyAudience = (): MailAudience => ({ everyone: false, team_ids: [], user_ids: [] });
export const audienceIsEmpty = (a: MailAudience) => !a.everyone && !a.team_ids.length && !a.user_ids.length;

/** "Everyone", "2 teams, 3 people" or "Only you". */
export function useAudienceSummary() {
  const { t } = useTranslation("collab");
  return (a: MailAudience) => {
    if (a.everyone) return t("audience.summaryEveryone");
    const parts = [
      ...(a.team_ids.length ? [t("audience.summaryTeams", { count: a.team_ids.length })] : []),
      ...(a.user_ids.length ? [t("audience.summaryPeople", { count: a.user_ids.length })] : []),
    ];
    return parts.length ? parts.join(", ") : t("audience.onlyYou");
  };
}

function Chip({ children, onRemove, label }: { children: ReactNode; onRemove?: () => void; label: string }) {
  return (
    <span className="inline-flex h-6 items-center gap-1.5 rounded-md border border-line bg-surface pl-1.5 pr-0.5 text-[12px] animate-pop-in motion-reduce:animate-none">
      {children}
      {onRemove && (
        <button type="button" aria-label={label} onClick={onRemove} className="flex size-5 items-center justify-center rounded-sm text-fg-4 hover:bg-hover hover:text-fg">
          <X className="size-3" />
        </button>
      )}
    </span>
  );
}

/** Teams and people of an audience, as chips with pickers to add more. */
export function AudienceLists({ value, onChange, disabled, exclude }: { value: MailAudience; onChange: (a: MailAudience) => void; disabled?: boolean; exclude?: string }) {
  const { t } = useTranslation("collab");
  const { data: teams } = useQuery(q.teams());
  const { data: members } = useQuery(q.members());
  const memberOf = (id: string): Member | undefined => members?.find((m) => m.user.id === id);
  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-col gap-1.5">
        <span className="text-xs font-medium text-fg-2">{t("audience.teams")}</span>
        <div className="flex flex-wrap items-center gap-1.5">
          {value.team_ids.map((id) => {
            const team = teams?.find((x) => x.id === id);
            return (
              <Chip key={id} label={t("composer.remove", { name: team?.name ?? id })} onRemove={disabled ? undefined : () => onChange({ ...value, team_ids: value.team_ids.filter((x) => x !== id) })}>
                <TeamDot team={team} />
                {team?.name ?? "…"}
              </Chip>
            );
          })}
          {!disabled && (
            <Picker
              multi
              values={value.team_ids}
              onChange={(ids) => onChange({ ...value, team_ids: ids })}
              placeholder={t("audience.addTeams")}
              emptyText={t("share.noTeams")}
              items={(teams ?? []).map((x) => ({ value: x.id, label: x.name, icon: <TeamDot team={x} /> }))}
              trigger={
                <Button size="xs" variant="ghost" icon={<Plus />}>
                  {t("audience.addTeams")}
                </Button>
              }
            />
          )}
        </div>
      </div>
      <div className="flex flex-col gap-1.5">
        <span className="text-xs font-medium text-fg-2">{t("audience.people")}</span>
        <div className="flex flex-wrap items-center gap-1.5">
          {value.user_ids.map((id) => {
            const m = memberOf(id);
            const name = m?.user.name || m?.user.email || "…";
            return (
              <Chip key={id} label={t("composer.remove", { name })} onRemove={disabled ? undefined : () => onChange({ ...value, user_ids: value.user_ids.filter((x) => x !== id) })}>
                <Avatar name={name} src={m?.user.avatar_url} size="xs" />
                {name}
              </Chip>
            );
          })}
          {!disabled && (
            <Picker
              multi
              values={value.user_ids}
              onChange={(ids) => onChange({ ...value, user_ids: ids })}
              placeholder={t("audience.addPeople")}
              items={(members ?? [])
                .filter((m) => m.user.id !== exclude)
                .map((m) => ({ value: m.user.id, label: m.user.name || m.user.email, icon: <Avatar name={m.user.name || m.user.email} src={m.user.avatar_url} size="xs" />, keywords: [m.user.email] }))}
              trigger={
                <Button size="xs" variant="ghost" icon={<Plus />}>
                  {t("audience.addPeople")}
                </Button>
              }
            />
          )}
        </div>
      </div>
    </div>
  );
}

/** A choice card: a radio in a bordered tile, used for Private/Shared and Everyone/Pick. */
export function ChoiceCard({
  checked,
  onSelect,
  icon,
  title,
  hint,
  disabled,
  children,
}: {
  checked: boolean;
  onSelect: () => void;
  icon: ReactNode;
  title: ReactNode;
  hint: ReactNode;
  disabled?: boolean;
  children?: ReactNode;
}) {
  return (
    <div
      className={cn(
        "rounded-lg border bg-surface transition-[border-color,box-shadow] duration-150",
        checked ? "border-fg-3 shadow-[0_0_0_1px_var(--fg-3)]" : "border-line hover:border-line-strong",
        disabled && "pointer-events-none opacity-60",
      )}
    >
      <button
        type="button"
        role="radio"
        aria-checked={checked}
        disabled={disabled}
        onClick={onSelect}
        className="flex w-full items-start gap-3 rounded-lg px-3.5 py-3 text-left outline-none focus-visible:shadow-[0_0_0_3px_var(--focus)]"
      >
        <span className={cn("mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-md [&_svg]:size-3.5", checked ? "bg-inverse text-inverse-fg" : "bg-hover text-fg-3")}>{icon}</span>
        <span className="min-w-0 flex-1">
          <span className="block text-[13px] font-medium text-fg">{title}</span>
          <span className="mt-0.5 block text-[12px] leading-[17px] text-fg-3">{hint}</span>
        </span>
        <span className={cn("mt-1 flex size-4 shrink-0 items-center justify-center rounded-full border", checked ? "border-inverse bg-inverse text-inverse-fg" : "border-line-strong")}>
          {checked && <Check className="size-2.5" strokeWidth={3.5} />}
        </span>
      </button>
      {checked && children && <div className="border-t border-line-soft px-3.5 pb-3.5 pt-3 animate-rise-in motion-reduce:animate-none">{children}</div>}
    </div>
  );
}

/** Who sees a shared account: everyone, or the teams and people picked. */
export function SharedAudience({ value, onChange, disabled, exclude }: { value: MailAudience; onChange: (a: MailAudience) => void; disabled?: boolean; exclude?: string }) {
  const { t } = useTranslation("collab");
  const [mode, setMode] = useState<"everyone" | "pick">(value.everyone || audienceIsEmpty(value) ? "everyone" : "pick");
  const everyone = mode === "everyone";
  return (
    <div role="radiogroup" aria-label={t("audience.with")} className="flex flex-col gap-2">
      <ChoiceCard
        checked={everyone}
        disabled={disabled}
        onSelect={() => {
          setMode("everyone");
          onChange({ everyone: true, team_ids: [], user_ids: [] });
        }}
        icon={<Globe />}
        title={t("audience.everyone")}
        hint={t("audience.everyoneHint")}
      />
      <ChoiceCard
        checked={!everyone}
        disabled={disabled}
        onSelect={() => {
          setMode("pick");
          onChange({ ...value, everyone: false });
        }}
        icon={<Users />}
        title={t("audience.pick")}
        hint={t("audience.pickHint")}
      >
        <AudienceLists value={value} onChange={(a) => onChange({ ...a, everyone: false })} disabled={disabled} exclude={exclude} />
      </ChoiceCard>
    </div>
  );
}
