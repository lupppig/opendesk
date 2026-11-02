import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowUpRight, ChevronDown, Clock, CornerDownRight, Hash, Import as ImportIcon, Inbox, Lock, Mail, Pencil, Route, Star, Users } from "lucide-react";
import { forwardRef, useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { Priority, Severity, Team, Ticket, TicketStatus } from "@/lib/api/types";
import { q, qk } from "@/lib/queries";
import { fullDate, minutes, relLong, shortDate } from "@/lib/format";
import { cn } from "@/lib/cn";
import { tone } from "@/lib/tones";
import { useRole, atLeast } from "@/lib/session";
import { Picker, type PickerItem } from "@/components/ui/picker";
import { Avatar } from "@/components/ui/avatar";
import { TagChip } from "@/components/ui/badge";
import { CopyButton } from "@/components/ui/copy-button";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { TextInput } from "@/components/ui/input";
import { Tooltip } from "@/components/ui/tooltip";
import { ContactCard } from "@/components/contacts/ContactCard";
import { api } from "@/lib/api/endpoints";
import { OlliePanel } from "@/components/ai/ticket/OlliePanel";
import { ChannelIcon, channelLabel, PriorityIcon, priorityOrder, SeverityChip, severityHint, severityLabel, severityOrder, slaState, StatusIcon } from "../bits";
import { aiDecisions } from "@/lib/decisions";
import { LabelPicker } from "./LabelPicker";
import { SetByOllie, useAISet, useOllieNote } from "../ollie";
import { FieldsSection } from "./FieldsSection";
import { TicketLinks } from "./TicketLinks";
import { TicketPanels } from "./TicketPanels";
import { SideConversations } from "./SideConversations";
import { importedFrom } from "@/components/imports/sources";
import { useDisplay, type FieldPatch, type TicketWithFields } from "../fields";
import { useUpdateTicket } from "../useTicketActions";
import { addressLabel, BrandChip, replyAddresses, sendingAddress, TeamDot, TeamMove, useRoutedText, useTeamsCtx } from "../teams";

export const PropButton = forwardRef<HTMLButtonElement, { children: ReactNode; disabled?: boolean; placeholder?: boolean } & React.ButtonHTMLAttributes<HTMLButtonElement>>(
  function PropButton({ children, disabled, placeholder, className, ...rest }, ref) {
    return (
      <button
        ref={ref}
        type="button"
        disabled={disabled}
        className={cn(
          "group flex h-7 min-w-0 flex-1 items-center gap-2 rounded-md px-2 text-left text-[12.5px] outline-none transition-[background-color,transform] duration-150 hover:bg-hover focus-visible:shadow-[0_0_0_2px_var(--focus)] active:scale-[0.99] disabled:pointer-events-none data-[state=open]:bg-hover [&_svg]:shrink-0",
          placeholder ? "text-fg-4" : "text-fg",
          className,
        )}
        {...rest}
      >
        {children}
        <ChevronDown className="ml-auto size-3 text-fg-4 opacity-0 transition-opacity group-hover:opacity-100" />
      </button>
    );
  },
);

/**
 * A property in the bar above the conversation (Settings → Profile → Appearance): its value
 * with a hint that it opens, named for screen readers and in its tooltip, since the bar has
 * no visible labels.
 */
export const BarButton = forwardRef<
  HTMLButtonElement,
  { label: string; shortcut?: string; children: ReactNode; disabled?: boolean; placeholder?: boolean } & React.ButtonHTMLAttributes<HTMLButtonElement>
>(function BarButton({ label, shortcut, children, disabled, placeholder, className, ...rest }, ref) {
  return (
    <Tooltip content={label} shortcut={shortcut}>
      <button
        ref={ref}
        type="button"
        disabled={disabled}
        data-bar-property
        className={cn(
          "inline-flex h-7 min-w-0 max-w-[240px] items-center gap-1.5 rounded-md border border-line px-2 text-[12.5px] outline-none transition-[background-color,border-color] duration-150 hover:border-line-strong hover:bg-hover focus-visible:shadow-[0_0_0_2px_var(--focus)] disabled:pointer-events-none data-[state=open]:border-line-strong data-[state=open]:bg-hover [&_svg]:shrink-0",
          placeholder ? "text-fg-4" : "text-fg",
          className,
        )}
        {...rest}
      >
        <span className="sr-only">{label}: </span>
        {children}
        {!disabled && <ChevronDown className="size-3 text-fg-4" aria-hidden />}
      </button>
    </Tooltip>
  );
});

export function Prop({ label, children, shortcut }: { label: string; children: ReactNode; shortcut?: string }) {
  return (
    <div className="flex min-h-8 items-center gap-1">
      <span className="flex w-[76px] shrink-0 items-center text-[12px] text-fg-3" title={shortcut ? `${label} (${shortcut.toUpperCase()})` : undefined}>
        {label}
      </span>
      <div className="flex min-w-0 flex-1">{children}</div>
    </div>
  );
}

function useNow(ms = 30_000) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), ms);
    return () => clearInterval(t);
  }, [ms]);
  return now;
}

function SlaTimers({ t }: { t: Ticket }) {
  const { t: tr } = useTranslation("inbox");
  const now = useNow();
  const { data: policies } = useQuery(q.slaPolicies());
  if (!t.sla) return null;
  const policy = policies?.find((p) => p.id === t.sla!.policy_id);
  const rows: { kind: string; label: string; due: string | null; done: string | null }[] = [
    { kind: "First response", label: tr("sla.firstResponse"), due: t.sla.first_response_due_at, done: t.first_responded_at },
    { kind: "Resolution", label: tr("sla.resolution"), due: t.sla.resolution_due_at, done: t.solved_at },
  ];
  const active = slaState(t, now);
  return (
    <section className="border-b border-line-soft px-4 py-3.5">
      <div className="mb-2 flex items-center justify-between">
        <h3 className="label-caps">{tr("sla.title")}</h3>
        {policy && <span className="truncate text-[11px] text-fg-4">{policy.name}</span>}
      </div>
      <div className="flex flex-col gap-1.5">
        {rows.map((r) => {
          if (!r.due) return null;
          const left = Math.round((Date.parse(r.due) - now) / 60000);
          const met = r.done && Date.parse(r.done) <= Date.parse(r.due);
          const missed = (r.done && !met) || (!r.done && left < 0);
          const isActive = active?.kind === r.kind;
          const soon = !r.done && !missed && left < 60;
          return (
            <div key={r.kind} className="flex items-center gap-2 text-[12.5px]">
              <span
                className={cn(
                  "size-1.5 shrink-0 rounded-full",
                  missed ? tone.red.solid : met ? tone.green.solid : soon ? tone.amber.solid : isActive ? tone.green.solid : "bg-fg-4",
                  missed && !r.done && "animate-pulse-soft motion-reduce:animate-none",
                )}
              />
              <span className={cn("flex-1", isActive ? "text-fg" : "text-fg-3")}>{r.label}</span>
              <Tooltip content={tr("sla.due", { when: fullDate(r.due) })}>
                <span
                  className={cn(
                    "mono inline-flex h-[18px] items-center rounded-sm px-1.5 text-[11px] tabular-nums",
                    missed ? tone.red.chip : soon ? tone.amber.chip : met ? tone.green.text : isActive ? "text-fg" : "text-fg-3",
                  )}
                >
                  {r.done ? (met ? tr("sla.met") : tr("sla.missed")) : left < 0 ? tr("sla.over", { time: minutes(-left) }) : tr("sla.left", { time: minutes(left) })}
                </span>
              </Tooltip>
            </div>
          );
        })}
      </div>
    </section>
  );
}

/** A page address without its scheme, for a narrow column. */
function pageLabel(url: string) {
  try {
    const u = new URL(url);
    return `${u.host}${u.pathname === "/" ? "" : u.pathname}`;
  } catch {
    return url;
  }
}

export type PropPicker = "status" | "priority" | "severity" | "assignee" | "team" | "contact" | null;

export const unassignedIcon = <span className="size-4 rounded-xs border border-dashed border-line-strong" />;

function TicketAliasEditor({ ticket, canEdit }: { ticket: Ticket; canEdit: boolean }) {
  const { t } = useTranslation("inbox");
  const update = useUpdateTicket();
  const [open, setOpen] = useState(false);
  const [alias, setAlias] = useState(ticket.alias ?? "");
  const save = (event: React.FormEvent) => {
    event.preventDefault();
    update.mutate({ id: ticket.id, patch: { alias: alias.trim() || null } });
    setOpen(false);
  };

  return (
    <>
      <div className="flex min-h-8 items-center gap-2 text-fg-3">
        <Hash className="size-3.5" />
        <span className="flex-1">{t("props.reference")}</span>
        {ticket.alias ? (
          <>
            <span className="mono text-fg-2">{ticket.alias}</span>
            <CopyButton value={ticket.alias} label={t("props.copyReference")} className="size-5" />
          </>
        ) : (
          <span className="text-fg-4">{t("props.noReference")}</span>
        )}
        {canEdit && (
          <Button
            variant="ghost"
            size="xs"
            square
            aria-label={ticket.alias ? t("props.editReference") : t("props.addReference")}
            icon={<Pencil />}
            onClick={() => {
              setAlias(ticket.alias ?? "");
              setOpen(true);
            }}
          />
        )}
      </div>
      <Dialog
        open={open}
        onOpenChange={setOpen}
        title={t("props.reference")}
        description={t("props.referenceHint")}
        footer={
          <>
            <Button variant="ghost" onClick={() => setOpen(false)}>{t("common.cancel")}</Button>
            <Button variant="primary" type="submit" form={`ticket-reference-form-${ticket.id}`} loading={update.isPending}>{t("props.saveReference")}</Button>
          </>
        }
      >
        <form id={`ticket-reference-form-${ticket.id}`} onSubmit={save}>
          <label htmlFor={`ticket-reference-${ticket.id}`} className="sr-only">{t("props.reference")}</label>
          <TextInput
            id={`ticket-reference-${ticket.id}`}
            autoFocus
            maxLength={40}
            value={alias}
            placeholder={t("props.referencePlaceholder")}
            onChange={(event) => setAlias(event.target.value)}
          />
        </form>
      </Dialog>
    </>
  );
}

/** Who a ticket of this team can go to: its members first, and a private team's to its members only. */
export function useAssigneeItems(team: Team | undefined): PickerItem[] {
  const { t: tr } = useTranslation("inbox");
  const { data: members } = useQuery(q.members());
  const teamMembers = new Set(team ? (team.members?.length ? team.members.map((m) => m.user_id) : team.member_ids) : []);
  const agents = (members ?? []).filter((m) => m.role !== "viewer");
  return [
    { value: "__none", label: tr("props.unassigned"), icon: unassignedIcon },
    ...agents
      .filter((m) => !team || teamMembers.has(m.user.id) || !team.private)
      .sort((a, b) => Number(teamMembers.has(b.user.id)) - Number(teamMembers.has(a.user.id)))
      .map((m) => ({
        value: m.user.id,
        label: m.user.name,
        keywords: [m.user.email],
        group: team ? (teamMembers.has(m.user.id) ? tr("teams.membersOf", { team: team.name }) : tr("teams.others")) : undefined,
        icon: <Avatar name={m.user.name} src={m.user.avatar_url} size="xs" presence={m.presence} />,
      })),
  ];
}

export function useSeverityItems(): PickerItem[] {
  const { t: tr } = useTranslation("inbox");
  return [
    ...severityOrder.map((s) => ({ value: s, label: severityLabel(s), hint: severityHint(s), icon: <SeverityChip severity={s} /> })),
    { value: "__none", label: tr("props.noSeverity"), icon: <span className="h-[16px] w-[22px] rounded-[4px] border border-dashed border-line-strong" /> },
  ];
}

/** The labels row: chips that come off with a click, and the picker to add more. */
export function TagsProp({
  tags,
  canEdit,
  onChange,
  badge,
}: {
  tags: { id?: string; name: string; color?: string | null }[];
  canEdit: boolean;
  onChange: (names: string[]) => void;
  /** Drawn on a chip's corner (set by Ollie). */
  badge?: (name: string) => ReactNode;
}) {
  const { t: tr } = useTranslation("inbox");
  const names = tags.map((x) => x.name);
  return (
    <div className="flex items-start gap-1 py-1">
      <span className="w-[76px] shrink-0 pt-1.5 text-[12px] text-fg-3">{tr("props.tags")}</span>
      <div className="flex min-w-0 flex-1 flex-wrap items-center gap-1 px-1 py-1">
        {tags.map((tag) => (
          <span key={tag.id ?? tag.name} className="relative animate-pop-in motion-reduce:animate-none">
            <TagChip name={tag.name} color={tag.color} onRemove={canEdit ? () => onChange(names.filter((n) => n !== tag.name)) : undefined} />
            {badge?.(tag.name)}
          </span>
        ))}
        {canEdit && (
          <LabelPicker
            values={names}
            onChange={onChange}
            trigger={
              <button
                type="button"
                aria-label={tr("props.addTag")}
                className="inline-flex h-[18px] items-center rounded-sm border border-dashed border-line-strong px-1.5 text-[11px] text-fg-3 outline-none transition-colors hover:border-fg-4 hover:text-fg focus-visible:shadow-[0_0_0_2px_var(--focus)]"
              >
                {tags.length ? tr("props.addTagShort") : tr("props.addTag")}
              </button>
            }
          />
        )}
      </div>
    </div>
  );
}

/**
 * The ticket's properties, each with its own picker: rows with their labels in the details
 * column, or compact buttons in the bar above the conversation. Both share the pickers, so
 * the shortcuts (s, p, a, t) open the same ones wherever they are.
 */
function PropertyControls({ t, picker, onPicker, bar }: { t: Ticket; picker: PropPicker; onPicker: (p: PropPicker) => void; bar?: boolean }) {
  const { t: tr } = useTranslation("inbox");
  const update = useUpdateTicket();
  const canEdit = atLeast(useRole(), "agent");
  const severityItems = useSeverityItems();
  const ctx = useTeamsCtx();
  const display = useDisplay();
  const aiSet = useAISet(t.id);
  const note = useOllieNote();
  const decided = aiDecisions(t)?.fields;
  const { data: mailboxes } = useQuery(q.mailboxes());
  const patch = (p: Parameters<typeof update.mutate>[0]["patch"]) => update.mutate({ id: t.id, patch: p });
  const team = ctx.team(t.team_id);
  const mailbox = mailboxes?.find((m) => m.id === t.mailbox_id);
  const brand = ctx.brand(t.brand_id);
  const addresses = replyAddresses(ctx, t);
  // The ticket's team first; a private team's tickets go to its members only.
  const assigneeItems = useAssigneeItems(team);
  const openFor = (k: Exclude<PropPicker, null>) => ({ open: picker === k, onOpenChange: (o: boolean) => onPicker(o ? k : null) });
  // The same trigger content, as a labelled row in the column or a named button in the bar.
  const trigger = (label: string, shortcut: string | undefined, opts: { placeholder?: boolean }, children: ReactNode) =>
    bar ? (
      <BarButton label={label} shortcut={shortcut} disabled={!canEdit} placeholder={opts.placeholder}>
        {children}
      </BarButton>
    ) : (
      <PropButton disabled={!canEdit} placeholder={opts.placeholder}>
        {children}
      </PropButton>
    );
  const row = (label: string, shortcut: string | undefined, control: ReactNode) =>
    bar ? (
      control
    ) : (
      <Prop label={label} shortcut={shortcut}>
        {control}
      </Prop>
    );
  const tagBadge = (name: string) =>
    aiSet.tags.has(name) && (
      <SetByOllie note={note(t, decided?.type_label?.value === t.tags.find((x) => x.name === name)?.id ? "type_label" : "label")} size={10} className="absolute -right-1 -top-1 ring-1 ring-surface" />
    );

  return (
    <>
      {row(
        tr("props.status"),
        "s",
        <Picker
          {...openFor("status")}
          value={t.status}
          placeholder={tr("props.setStatus")}
          items={display.statuses(t.status).map((s) => ({ value: s, label: display.status(s), icon: <StatusIcon status={s} /> }))}
          onSelect={(v) => patch({ status: v as TicketStatus })}
          trigger={trigger(
            tr("props.status"),
            "s",
            {},
            <>
              <StatusIcon status={t.status} />
              <span className="truncate">{display.status(t.status)}</span>
              {aiSet.status && <SetByOllie />}
            </>,
          )}
        />,
      )}
      {row(
        tr("props.priority"),
        "p",
        <Picker
          {...openFor("priority")}
          value={t.priority}
          placeholder={tr("props.setPriority")}
          items={priorityOrder.map((p) => ({ value: p, label: display.priority(p), icon: <PriorityIcon priority={p} /> }))}
          onSelect={(v) => patch({ priority: v as Priority })}
          trigger={trigger(
            tr("props.priority"),
            "p",
            {},
            <>
              <PriorityIcon priority={t.priority} />
              <span className="truncate">{display.priority(t.priority)}</span>
              {aiSet.priority && <SetByOllie note={decided?.priority?.applied ? note(t, "priority") : undefined} />}
            </>,
          )}
        />,
      )}
      {row(
        tr("props.severity"),
        undefined,
        <Picker
          {...openFor("severity")}
          value={t.severity ?? "__none"}
          placeholder={tr("props.setSeverity")}
          items={severityItems}
          onSelect={(v) => patch({ severity: v === "__none" ? null : (v as Severity) })}
          trigger={trigger(
            tr("props.severity"),
            undefined,
            { placeholder: !t.severity },
            <>
              {t.severity ? <SeverityChip severity={t.severity} /> : <span className="h-[16px] w-[22px] shrink-0 rounded-[4px] border border-dashed border-line-strong" />}
              <span className="truncate">{t.severity ? severityLabel(t.severity) : tr("props.noSeverity")}</span>
              {aiSet.severity && <SetByOllie note={decided?.severity?.applied ? note(t, "severity") : undefined} />}
            </>,
          )}
        />,
      )}
      {row(
        tr("props.assignee"),
        "a",
        <Picker
          {...openFor("assignee")}
          value={t.assignee?.id ?? "__none"}
          placeholder={tr("props.assignTo")}
          items={assigneeItems}
          onSelect={(v) => patch({ assignee_id: v === "__none" ? null : v })}
          trigger={trigger(
            tr("props.assignee"),
            "a",
            { placeholder: !t.assignee },
            <>
              {t.assignee ? <Avatar name={t.assignee.name} src={t.assignee.avatar_url} size="xs" /> : unassignedIcon}
              <span className="truncate">{t.assignee?.name ?? tr("props.unassigned")}</span>
            </>,
          )}
        />,
      )}
      {ctx.hasTeams &&
        row(
          tr("props.team"),
          "t",
          <TeamMove
            tickets={[t]}
            {...openFor("team")}
            onMove={(id) => patch({ team_id: id })}
            trigger={trigger(
              tr("props.team"),
              "t",
              { placeholder: !team },
              <>
                {team ? <TeamDot team={team} /> : <Users className="size-3.5 text-fg-3" />}
                <span className="truncate">{team?.name ?? tr("props.noTeam")}</span>
                {team?.private && <Lock className="size-3 text-fg-4" aria-label={tr("teams.private")} />}
                {aiSet.team && team && <SetByOllie note={decided?.team?.applied ? note(t, "team") : undefined} />}
              </>,
            )}
          />,
        )}
      {ctx.multiBrand &&
        row(
          tr("props.brand"),
          undefined,
          <Picker
            value={t.brand_id}
            placeholder={tr("props.chooseBrand")}
            items={ctx.brands.map((b) => ({
              value: b.id,
              label: b.name,
              icon: <span className="size-2 rounded-[2px] bg-fg-4" style={b.color ? { backgroundColor: b.color } : undefined} />,
            }))}
            onSelect={(v) => v !== t.brand_id && patch({ brand_id: v })}
            trigger={trigger(
              tr("props.brand"),
              undefined,
              {},
              brand ? <BrandChip brand={brand} plain={bar} className={cn("max-w-full", bar && "text-[12.5px] text-fg")} /> : <span className="text-fg-4">{tr("props.chooseBrand")}</span>,
            )}
          />,
        )}
      {(t.channel === "email" || t.mailbox_id) &&
        addresses.length > 0 &&
        row(
          tr("props.replyFrom"),
          undefined,
          <Picker
            width={300}
            value={t.mailbox_id}
            placeholder={tr("props.searchAddresses")}
            items={addresses.map((m) => ({
              value: m.id,
              label: addressLabel(m),
              keywords: [m.email, ...(m.aliases ?? [])],
              icon: m.team_id ? <TeamDot team={ctx.team(m.team_id)} /> : <Mail className="text-fg-3" />,
              hint: m.private ? <Lock className="size-3" /> : undefined,
            }))}
            onSelect={(v) => v !== t.mailbox_id && patch({ mailbox_id: v })}
            trigger={trigger(
              tr("props.replyFrom"),
              undefined,
              { placeholder: !mailbox },
              <>
                <Mail className="size-3.5 text-fg-3" />
                <span className="mono truncate text-[11.5px]">{mailbox ? sendingAddress(mailbox, t) : tr("props.noAddress")}</span>
              </>,
            )}
          />,
        )}
      {bar ? (
        <BarTags tags={t.tags} canEdit={canEdit} onChange={(v) => patch({ tags: v })} badge={tagBadge} />
      ) : (
        <TagsProp tags={t.tags} canEdit={canEdit} onChange={(v) => patch({ tags: v })} badge={tagBadge} />
      )}
    </>
  );
}

/** The labels in the bar: a named group of chips that come off with a click, and the picker to add more. */
function BarTags({
  tags,
  canEdit,
  onChange,
  badge,
}: {
  tags: { id?: string; name: string; color?: string | null }[];
  canEdit: boolean;
  onChange: (names: string[]) => void;
  badge?: (name: string) => ReactNode;
}) {
  const { t: tr } = useTranslation("inbox");
  const names = tags.map((x) => x.name);
  if (!tags.length && !canEdit) return null;
  return (
    <div role="group" aria-label={tr("props.tags")} className="flex min-w-0 flex-wrap items-center gap-1 px-1">
      {tags.map((tag) => (
        <span key={tag.id ?? tag.name} className="relative animate-pop-in motion-reduce:animate-none">
          <TagChip name={tag.name} color={tag.color} onRemove={canEdit ? () => onChange(names.filter((n) => n !== tag.name)) : undefined} />
          {badge?.(tag.name)}
        </span>
      ))}
      {canEdit && (
        <LabelPicker
          values={names}
          onChange={onChange}
          trigger={
            <button
              type="button"
              aria-label={tr("props.addTag")}
              className="inline-flex h-[18px] items-center rounded-sm border border-dashed border-line-strong px-1.5 text-[11px] text-fg-3 outline-none transition-colors hover:border-fg-4 hover:text-fg focus-visible:shadow-[0_0_0_2px_var(--focus)]"
            >
              {tags.length ? tr("props.addTagShort") : tr("props.addTag")}
            </button>
          }
        />
      )}
    </div>
  );
}

/**
 * The bar above the conversation that holds the ticket's properties when a member puts them
 * there (Settings → Profile → Appearance). It wraps on a narrow pane, and its controls come
 * in the column's order, between the header and the timeline in the tab order.
 */
export function TicketPropertiesBar({ t, picker, onPicker }: { t: Ticket; picker: PropPicker; onPicker: (p: PropPicker) => void }) {
  const { t: tr } = useTranslation("inbox");
  return (
    <div role="group" aria-label={tr("props.bar")} data-properties-bar className="flex shrink-0 flex-wrap items-center gap-1 border-b border-line px-3 py-1.5">
      <PropertyControls t={t} picker={picker} onPicker={onPicker} bar />
    </div>
  );
}

export function TicketProperties({
  t,
  picker,
  onPicker,
  onSideConversation,
  bar,
}: {
  t: Ticket;
  picker: PropPicker;
  onPicker: (p: PropPicker) => void;
  onSideConversation?: () => void;
  /** The properties are in the bar above the conversation: the column starts with the customer. */
  bar?: boolean;
}) {
  const { t: tr } = useTranslation("inbox");
  useTranslation("indicators");
  const update = useUpdateTicket();
  const role = useRole();
  const canEdit = atLeast(role, "agent");
  const qc = useQueryClient();
  const [contactQuery, setContactQuery] = useState("");
  const contactResults = useQuery({
    queryKey: qk.contactPicker(contactQuery),
    queryFn: () => api.contacts.search({ q: contactQuery.trim() || undefined, limit: 12 }),
    enabled: picker === "contact",
  });
  const contactItems = (contactResults.data?.data ?? []).map((c) => ({
    value: c.id,
    label: c.name || c.email || tr("contact.unnamed"),
    keywords: c.email ? [c.email] : undefined,
    icon: <Avatar name={c.name || c.email || "?"} src={c.avatar_url} size="xs" />,
  }));
  const ctx = useTeamsCtx();
  const patch = (p: Parameters<typeof update.mutate>[0]["patch"]) => update.mutate({ id: t.id, patch: p });
  const routed = useRoutedText(t, ctx);
  const origin = ctx.mailbox(t.origin_mailbox_id);
  const receivedAt = t.received_at || origin?.email || "";
  const imported = importedFrom(t.custom);

  const customer = (
    <>
      <section className="border-b border-line-soft" data-contact-section>
        <div className="px-4 py-3.5">
          <div className="mb-3 flex items-center justify-between gap-2">
            <h3 className="label-caps">{tr("props.contact")}</h3>
            {/* A live chat stays with the visitor who started it. */}
            {t.channel !== "chat" && <Picker
              open={picker === "contact"}
              onOpenChange={(open) => {
                onPicker(open ? "contact" : null);
                if (!open) setContactQuery("");
              }}
              value={t.contact?.id ?? null}
              items={contactItems}
              width={280}
              placeholder={tr("props.searchContacts")}
              emptyText={contactResults.isFetching ? tr("props.searchingContacts") : contactQuery.trim() ? tr("props.noContactsFound") : tr("props.searchContacts")}
              onQueryChange={setContactQuery}
              onSelect={(id) => {
                // The card shows the picked contact at once, before the server answers.
                const picked = contactResults.data?.data.find((c) => c.id === id);
                if (picked && !qc.getQueryData(qk.contact(id))) qc.setQueryData(qk.contact(id), picked);
                patch({ contact_id: id });
              }}
              trigger={
                <button
                  type="button"
                  disabled={!canEdit}
                  className="inline-flex h-7 items-center gap-1.5 rounded-md px-2 text-[12px] text-fg-3 outline-none transition-colors hover:bg-hover hover:text-fg focus-visible:shadow-[0_0_0_2px_var(--focus)] disabled:pointer-events-none disabled:opacity-50"
                >
                  <Users className="size-3.5" />
                  {tr("props.changeContact")}
                  <ChevronDown className="size-3 text-fg-4" />
                </button>
              }
            />}
          </div>
          {t.contact ? <ContactCard contactId={t.contact.id} compact excludeTicketId={t.id} /> : <p className="text-xs text-fg-4">{tr("props.noContact")}</p>}
        </div>
      </section>

      {/* What Shopify or the workspace's own systems know about the contact. */}
      <TicketPanels ticketId={t.id} />
    </>
  );

  return (
    <div className="flex flex-col">
      {/* With the properties in the bar, the customer comes first, without scrolling. */}
      {bar ? (
        <>
          {customer}
          {t.required_skills.length > 0 && (
            <section className="border-b border-line-soft px-2.5 py-2.5">
              <Prop label={tr("props.requiredSkills")}>
                <div className="flex min-w-0 flex-wrap gap-1 py-1">
                  {t.required_skills.map((skill) => <TagChip key={skill.id} name={skill.name} color="violet" />)}
                </div>
              </Prop>
            </section>
          )}
        </>
      ) : (
        <section className="border-b border-line-soft px-2.5 py-2.5">
          <PropertyControls t={t} picker={picker} onPicker={onPicker} />
          {t.required_skills.length > 0 && (
            <Prop label={tr("props.requiredSkills")}>
              <div className="flex min-w-0 flex-wrap gap-1 py-1">
                {t.required_skills.map((skill) => <TagChip key={skill.id} name={skill.name} color="violet" />)}
              </div>
            </Prop>
          )}
        </section>
      )}

      <FieldsSection
        key={t.id}
        collapseEmpty
        values={(t as TicketWithFields).fields ?? {}}
        disabled={!canEdit}
        onChange={(key, v) => update.mutate({ id: t.id, patch: { fields: { [key]: v } } as FieldPatch })}
      />

      <OlliePanel t={t} />

      <TicketLinks ticketId={t.id} canEdit={canEdit} />

      <SideConversations ticketId={t.id} canEdit={canEdit} onStart={onSideConversation} />

      <SlaTimers t={t} />

      {t.snoozed_until && Date.parse(t.snoozed_until) > Date.now() && (
        <section className="flex items-center gap-2 border-b border-line-soft px-4 py-3 text-[12.5px]">
          <Clock className={cn("size-3.5", tone.violet.text)} />
          <span className="flex-1 text-fg-2">{tr("props.snoozedUntil", { when: shortDate(t.snoozed_until) })}</span>
          {canEdit && (
            <button type="button" onClick={() => patch({ snoozed_until: null, status: "open" })} className="rounded-sm px-1 text-xs text-fg-3 hover:bg-hover hover:text-fg">
              {tr("props.unsnooze")}
            </button>
          )}
        </section>
      )}

      {t.csat && (
        <section className="border-b border-line-soft px-4 py-3.5">
          <h3 className="label-caps mb-2">{tr("props.satisfaction")}</h3>
          <div className="flex items-center gap-1">
            {[1, 2, 3, 4, 5].map((i) => (
              <Star key={i} className={cn("size-3.5", i <= t.csat!.score ? "fill-tone-amber-solid stroke-tone-amber-solid" : "text-line-strong")} />
            ))}
            <span className="mono ml-1.5 text-[11.5px] text-fg-3 tabular-nums">{t.csat.score}/5</span>
          </div>
          {t.csat.comment && (
            <p className="mt-2 text-[12.5px] leading-5 text-fg-2" data-ph-mask>
              “{t.csat.comment}”
            </p>
          )}
        </section>
      )}

      {!bar && customer}

      <section className="flex flex-col gap-1.5 px-4 py-3.5 text-[12px]">
        <h3 className="label-caps mb-1">{tr("props.details")}</h3>
        <div className="flex items-center gap-2 text-fg-3">
          <Hash className="size-3.5" />
          <span className="flex-1">{tr("props.ticket")}</span>
          <span className="mono text-fg-2">#{t.number}</span>
          <CopyButton value={`${location.origin}/inbox/open/${t.id}`} label={tr("props.copyLink")} className="size-5" />
        </div>
        <TicketAliasEditor ticket={t} canEdit={canEdit} />
        <div className="flex items-center gap-2 text-fg-3">
          <ChannelIcon channel={t.channel} />
          <span className="flex-1">{tr("props.channel")}</span>
          <span className="text-fg-2">{channelLabel(t.channel)}</span>
        </div>
        {receivedAt ? (
          <div className="flex items-center gap-2 text-fg-3">
            <Inbox className="size-3.5 shrink-0" />
            <span className="flex-1">{tr("props.receivedAt")}</span>
            <Tooltip content={origin && origin.email !== receivedAt ? tr("props.aliasOf", { address: origin.email }) : receivedAt}>
              <span className="mono min-w-0 truncate text-fg-2">{receivedAt}</span>
            </Tooltip>
          </div>
        ) : null}
        {t.page_url && (
          <div className="flex items-center gap-2 text-fg-3">
            <CornerDownRight className="size-3.5 shrink-0" />
            <span className="flex-1">{tr("props.startedOn")}</span>
            <Tooltip content={t.page_url}>
              <span className="mono min-w-0 max-w-[150px] truncate text-fg-2">{pageLabel(t.page_url)}</span>
            </Tooltip>
          </div>
        )}
        {ctx.hasTeams && routed && (
          <div className="flex items-start gap-2 text-fg-3">
            <Route className="mt-px size-3.5 shrink-0" />
            <span className="min-w-0 flex-1 text-fg-2">{routed}</span>
          </div>
        )}
        <div className="flex items-center gap-2 text-fg-3">
          <Clock className="size-3.5" />
          <span className="flex-1">{tr("props.created")}</span>
          <Tooltip content={fullDate(t.created_at)}>
            <span className="text-fg-2">{relLong(t.created_at)}</span>
          </Tooltip>
        </div>
        {imported && (
          <div className="flex items-center gap-2 text-fg-3">
            <ImportIcon className="size-3.5 shrink-0" />
            <span className="flex-1">{tr("props.importedFrom")}</span>
            {imported.url ? (
              <a href={imported.url} target="_blank" rel="noopener noreferrer" className="inline-flex min-w-0 items-center gap-0.5 truncate text-fg-2 hover:text-fg hover:underline">
                {imported.label}
                <ArrowUpRight className="size-3 shrink-0" aria-hidden />
              </a>
            ) : (
              <span className="min-w-0 truncate text-fg-2">{imported.label}</span>
            )}
          </div>
        )}
      </section>
    </div>
  );
}
