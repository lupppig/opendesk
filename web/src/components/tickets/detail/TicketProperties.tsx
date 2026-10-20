import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowUpRight, ChevronDown, Clock, CornerDownRight, Hash, Import as ImportIcon, Inbox, Lock, Mail, Route, Star, Users } from "lucide-react";
import { forwardRef, useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { Priority, Severity, Ticket, TicketStatus } from "@/lib/api/types";
import { q, qk } from "@/lib/queries";
import { fullDate, minutes, relLong, shortDate } from "@/lib/format";
import { cn } from "@/lib/cn";
import { tone } from "@/lib/tones";
import { useRole, atLeast } from "@/lib/session";
import { Picker } from "@/components/ui/picker";
import { Avatar } from "@/components/ui/avatar";
import { TagChip } from "@/components/ui/badge";
import { CopyButton } from "@/components/ui/copy-button";
import { Tooltip } from "@/components/ui/tooltip";
import { ContactCard } from "@/components/contacts/ContactCard";
import { api } from "@/lib/api/endpoints";
import { OlliePanel } from "@/components/ai/ticket/OlliePanel";
import { ChannelIcon, channelLabel, PriorityIcon, priorityOrder, SeverityChip, severityHint, severityLabel, severityOrder, slaState, StatusIcon, statusOrder } from "../bits";
import { aiDecisions } from "@/lib/decisions";
import { LabelPicker } from "./LabelPicker";
import { SetByOllie, useAISet, useOllieNote } from "../ollie";
import { FieldsSection } from "./FieldsSection";
import { TicketLinks } from "./TicketLinks";
import { SideConversations } from "./SideConversations";
import { importedFrom } from "@/components/imports/sources";
import { useDisplay, type FieldPatch, type TicketWithFields } from "../fields";
import { useUpdateTicket } from "../useTicketActions";
import { addressLabel, BrandChip, replyAddresses, sendingAddress, TeamDot, TeamMove, useRoutedText, useTeamsCtx } from "../teams";

const PropButton = forwardRef<HTMLButtonElement, { children: ReactNode; disabled?: boolean; placeholder?: boolean } & React.ButtonHTMLAttributes<HTMLButtonElement>>(
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

function Prop({ label, children, shortcut }: { label: string; children: ReactNode; shortcut?: string }) {
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

const unassignedIcon = <span className="size-4 rounded-xs border border-dashed border-line-strong" />;

export function TicketProperties({
  t,
  picker,
  onPicker,
  onSideConversation,
}: {
  t: Ticket;
  picker: PropPicker;
  onPicker: (p: PropPicker) => void;
  onSideConversation?: () => void;
}) {
  const { t: tr } = useTranslation("inbox");
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
  const { data: members } = useQuery(q.members());
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
  const routed = useRoutedText(t, ctx);
  const origin = ctx.mailbox(t.origin_mailbox_id);
  const receivedAt = t.received_at || origin?.email || "";
  const addresses = replyAddresses(ctx, t);
  // The ticket's team first; a private team's tickets go to its members only.
  const teamMembers = new Set(team ? (team.members?.length ? team.members.map((m) => m.user_id) : team.member_ids) : []);
  const agents = (members ?? []).filter((m) => m.role !== "viewer");
  const assigneeItems = [
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
  const tagNames = t.tags.map((x) => x.name);
  const imported = importedFrom(t.custom);
  const openFor = (k: Exclude<PropPicker, null>) => ({ open: picker === k, onOpenChange: (o: boolean) => onPicker(o ? k : null) });

  return (
    <div className="flex flex-col">
      <section className="border-b border-line-soft px-2.5 py-2.5">
        <Prop label={tr("props.status")} shortcut="s">
          <Picker
            {...openFor("status")}
            value={t.status}
            placeholder={tr("props.setStatus")}
            items={statusOrder.map((s) => ({ value: s, label: display.status(s), icon: <StatusIcon status={s} /> }))}
            onSelect={(v) => patch({ status: v as TicketStatus })}
            trigger={
              <PropButton disabled={!canEdit}>
                <StatusIcon status={t.status} />
                {display.status(t.status)}
                {aiSet.status && <SetByOllie />}
              </PropButton>
            }
          />
        </Prop>
        <Prop label={tr("props.priority")} shortcut="p">
          <Picker
            {...openFor("priority")}
            value={t.priority}
            placeholder={tr("props.setPriority")}
            items={priorityOrder.map((p) => ({ value: p, label: display.priority(p), icon: <PriorityIcon priority={p} /> }))}
            onSelect={(v) => patch({ priority: v as Priority })}
            trigger={
              <PropButton disabled={!canEdit}>
                <PriorityIcon priority={t.priority} />
                {display.priority(t.priority)}
                {aiSet.priority && <SetByOllie note={decided?.priority?.applied ? note(t, "priority") : undefined} />}
              </PropButton>
            }
          />
        </Prop>
        <Prop label={tr("props.severity")}>
          <Picker
            {...openFor("severity")}
            value={t.severity ?? "__none"}
            placeholder={tr("props.setSeverity")}
            items={[
              ...severityOrder.map((s) => ({ value: s, label: severityLabel[s], hint: severityHint[s], icon: <SeverityChip severity={s} /> })),
              { value: "__none", label: tr("props.noSeverity"), icon: <span className="h-[16px] w-[22px] rounded-[4px] border border-dashed border-line-strong" /> },
            ]}
            onSelect={(v) => patch({ severity: v === "__none" ? null : (v as Severity) })}
            trigger={
              <PropButton disabled={!canEdit} placeholder={!t.severity}>
                {t.severity ? <SeverityChip severity={t.severity} /> : <span className="h-[16px] w-[22px] shrink-0 rounded-[4px] border border-dashed border-line-strong" />}
                {t.severity ? severityLabel[t.severity] : tr("props.noSeverity")}
                {aiSet.severity && <SetByOllie note={decided?.severity?.applied ? note(t, "severity") : undefined} />}
              </PropButton>
            }
          />
        </Prop>
        <Prop label={tr("props.assignee")} shortcut="a">
          <Picker
            {...openFor("assignee")}
            value={t.assignee?.id ?? "__none"}
            placeholder={tr("props.assignTo")}
            items={assigneeItems}
            onSelect={(v) => patch({ assignee_id: v === "__none" ? null : v })}
            trigger={
              <PropButton disabled={!canEdit} placeholder={!t.assignee}>
                {t.assignee ? <Avatar name={t.assignee.name} src={t.assignee.avatar_url} size="xs" /> : unassignedIcon}
                <span className="truncate">{t.assignee?.name ?? tr("props.unassigned")}</span>
              </PropButton>
            }
          />
        </Prop>
        {ctx.hasTeams && (
          <Prop label={tr("props.team")} shortcut="t">
            <TeamMove
              tickets={[t]}
              {...openFor("team")}
              onMove={(id) => patch({ team_id: id })}
              trigger={
                <PropButton disabled={!canEdit} placeholder={!team}>
                  {team ? <TeamDot team={team} /> : <Users className="size-3.5 text-fg-3" />}
                  <span className="truncate">{team?.name ?? tr("props.noTeam")}</span>
                  {team?.private && <Lock className="size-3 text-fg-4" aria-label={tr("teams.private")} />}
                  {aiSet.team && team && <SetByOllie note={decided?.team?.applied ? note(t, "team") : undefined} />}
                </PropButton>
              }
            />
          </Prop>
        )}
        {ctx.multiBrand && (
          <Prop label={tr("props.brand")}>
            <Picker
              value={t.brand_id}
              placeholder={tr("props.chooseBrand")}
              items={ctx.brands.map((b) => ({
                value: b.id,
                label: b.name,
                icon: <span className="size-2 rounded-[2px] bg-fg-4" style={b.color ? { backgroundColor: b.color } : undefined} />,
              }))}
              onSelect={(v) => v !== t.brand_id && patch({ brand_id: v })}
              trigger={
                <PropButton disabled={!canEdit}>
                  {brand ? <BrandChip brand={brand} className="max-w-full" /> : <span className="text-fg-4">{tr("props.chooseBrand")}</span>}
                </PropButton>
              }
            />
          </Prop>
        )}
        {(t.channel === "email" || t.mailbox_id) && addresses.length > 0 && (
          <Prop label={tr("props.replyFrom")}>
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
              trigger={
                <PropButton disabled={!canEdit} placeholder={!mailbox}>
                  <Mail className="size-3.5 text-fg-3" />
                  <span className="mono truncate text-[11.5px]">{mailbox ? sendingAddress(mailbox, t) : tr("props.noAddress")}</span>
                </PropButton>
              }
            />
          </Prop>
        )}
        <div className="flex items-start gap-1 py-1">
          <span className="w-[76px] shrink-0 pt-1.5 text-[12px] text-fg-3">{tr("props.tags")}</span>
          <div className="flex min-w-0 flex-1 flex-wrap items-center gap-1 px-1 py-1">
            {t.tags.map((tag) => (
              <span key={tag.id} className="relative animate-pop-in motion-reduce:animate-none">
                <TagChip name={tag.name} color={tag.color} onRemove={canEdit ? () => patch({ tags: tagNames.filter((n) => n !== tag.name) }) : undefined} />
                {aiSet.tags.has(tag.name) && <SetByOllie note={note(t, decided?.type_label?.value === tag.id ? "type_label" : "label")} size={10} className="absolute -right-1 -top-1 ring-1 ring-surface" />}
              </span>
            ))}
            {canEdit && (
              <LabelPicker
                values={tagNames}
                onChange={(v) => patch({ tags: v })}
                trigger={
                  <button
                    type="button"
                    aria-label={tr("props.addTag")}
                    className="inline-flex h-[18px] items-center rounded-sm border border-dashed border-line-strong px-1.5 text-[11px] text-fg-3 outline-none transition-colors hover:border-fg-4 hover:text-fg focus-visible:shadow-[0_0_0_2px_var(--focus)]"
                  >
                    {t.tags.length ? tr("props.addTagShort") : tr("props.addTag")}
                  </button>
                }
              />
            )}
          </div>
        </div>
      </section>

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

      <section className="border-b border-line-soft">
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

      <section className="flex flex-col gap-1.5 px-4 py-3.5 text-[12px]">
        <h3 className="label-caps mb-1">{tr("props.details")}</h3>
        <div className="flex items-center gap-2 text-fg-3">
          <Hash className="size-3.5" />
          <span className="flex-1">{tr("props.ticket")}</span>
          <span className="mono text-fg-2">#{t.number}</span>
          <CopyButton value={`${location.origin}/inbox/open/${t.id}`} label={tr("props.copyLink")} className="size-5" />
        </div>
        <div className="flex items-center gap-2 text-fg-3">
          <ChannelIcon channel={t.channel} />
          <span className="flex-1">{tr("props.channel")}</span>
          <span className="text-fg-2">{channelLabel[t.channel]}</span>
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
