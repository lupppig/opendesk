// Value pickers shared by the view filter builder and the automation builder.

import { useQueries, useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Building2, ChevronDown, Inbox, Lock, MessageSquareReply, PencilLine, Reply, SquarePlus, UserRound } from "lucide-react";
import { forwardRef, useState, type ReactNode } from "react";
import { CompanyGlyph } from "@/components/contacts/bits";
import { useDebounced } from "@/components/contacts/hooks";
import { Avatar } from "@/components/ui/avatar";
import { Picker, type PickerItem } from "@/components/ui/picker";
import { TagChip } from "@/components/ui/badge";
import {
  ChannelIcon,
  PriorityIcon,
  SentimentIcon,
  SeverityChip,
  StatusIcon,
  channelLabel,
  priorityLabel,
  priorityOrder,
  severityHint,
  severityLabel,
  severityOrder,
  statusLabel,
  statusOrder,
} from "@/components/tickets/bits";
import { i18n } from "@/i18n";
import { api } from "@/lib/api/endpoints";
import type { AutomationTrigger, Channel, Company, Contact } from "@/lib/api/types";
import { isTone, tone, toneFor } from "@/lib/tones";
import { sentimentLabel, sentiments, triggerLabel, triggerTone } from "./automation";
import { q, qk } from "@/lib/queries";
import { cn } from "@/lib/cn";

export const statusItems = (): PickerItem[] => statusOrder.map((s) => ({
  value: s,
  label: statusLabel(s),
  icon: <StatusIcon status={s} />,
}));
export const priorityItems = (): PickerItem[] => priorityOrder.map((p) => ({
  value: p,
  label: priorityLabel(p),
  icon: <PriorityIcon priority={p} />,
}));
/** Severity levels, sev1 first, each with what it means. */
export const severityItems = (): PickerItem[] => severityOrder.map((s) => ({
  value: s,
  label: severityLabel(s),
  hint: severityHint(s),
  icon: <SeverityChip severity={s} />,
}));
/** Severity levels plus "no severity" ("none"), for conditions and filters. */
export const severityFilterItems = (): PickerItem[] => [
  ...severityItems(),
  { value: "none", label: i18n.t("settingsInbox:edit.noSeverity"), icon: <span className="h-[16px] w-[22px] rounded-[4px] border border-dashed border-line-strong" /> },
];
export const channelItems = (): PickerItem[] => (["email", "chat", "api", "portal"] as Channel[]).map((c) => ({
  value: c,
  label: channelLabel(c),
  icon: <ChannelIcon channel={c} />,
}));

export const sentimentItems = (): PickerItem[] =>
  sentiments.map((v) => ({
    value: v,
    label: sentimentLabel(v),
    icon: <SentimentIcon sentiment={v} />,
  }));

const triggerGlyph: Record<AutomationTrigger, ReactNode> = {
  ticket_created: <SquarePlus />,
  customer_replied: <MessageSquareReply />,
  agent_replied: <Reply />,
  ticket_updated: <PencilLine />,
};

/** The trigger's glyph in its hue. */
export function TriggerIcon({ trigger, className }: { trigger: AutomationTrigger; className?: string }) {
  return <span className={cn("flex shrink-0 [&_svg]:size-3.5", tone[triggerTone[trigger]].text, className)}>{triggerGlyph[trigger]}</span>;
}

export const triggerItems = () =>
  (Object.keys(triggerLabel) as AutomationTrigger[]).map((t) => ({
    value: t,
    label: triggerLabel[t],
    icon: <TriggerIcon trigger={t} />,
  }));

/** A tag's colour square, the same hue its chip uses. */
export function TagSwatch({ name, color }: { name: string; color?: string | null }) {
  const hue = isTone(color) ? color : toneFor(name);
  return <span aria-hidden className={cn("size-2 shrink-0 rounded-xs", tone[hue].solid)} />;
}

export function useMemberItems(): PickerItem[] {
  const { data } = useQuery(q.members());
  return (data ?? [])
    .filter((m) => m.role !== "viewer")
    .map((m) => ({
      value: m.user.id,
      label: m.user.name || m.user.email,
      hint: m.user.email,
      keywords: [m.user.email],
      icon: <Avatar name={m.user.name || m.user.email} src={m.user.avatar_url} size="xs" presence={m.presence} />,
    }));
}

export function useTeamItems(): PickerItem[] {
  const { t: tr } = useTranslation("inbox");
  const { data } = useQuery(q.teams());
  return (data ?? []).map((t) => ({
    value: t.id,
    label: t.name,
    icon: <span aria-hidden className={cn("size-2 shrink-0 rounded-[3px]", tone[isTone(t.color) ? t.color : toneFor(t.name)].solid)} />,
    hint: t.private ? <Lock className="size-3 text-fg-4" aria-label={tr("teams.private")} /> : undefined,
  }));
}

export function useMailboxItems(): PickerItem[] {
  const { data } = useQuery(q.mailboxes());
  return (data ?? []).map((m) => ({
    value: m.id,
    label: m.email,
    hint: m.name !== m.email ? m.name : undefined,
    icon: <Inbox />,
  }));
}

export function useTagItems(): PickerItem[] {
  const { data } = useQuery(q.tags());
  return (data ?? []).map((t) => ({
    value: t.name,
    label: t.name,
    icon: <TagSwatch name={t.name} color={t.color} />,
  }));
}

/**
 * Trigger heights: `sm` (28 px) for builder rows and dense lists, `md` (32 px) for a
 * form field beside text inputs and selects, which are 32 px.
 */
export type PickSize = "sm" | "md";
const pickHeights: Record<PickSize, string> = { sm: "h-7 text-[12.5px]", md: "h-8 px-2.5 text-[13px]" };

/** The trigger every builder picker uses: a quiet bordered button showing the current value. */
type ValueButtonProps = {
  children?: ReactNode;
  placeholder: string;
  icon?: ReactNode;
  className?: string;
  size?: PickSize;
  invalid?: boolean;
  disabled?: boolean;
} & Omit<React.ButtonHTMLAttributes<HTMLButtonElement>, "children">;

export const ValueButton = forwardRef<HTMLButtonElement, ValueButtonProps>(function ValueButton(
  { children, placeholder, icon, className, invalid, disabled, size = "sm", ...rest },
  ref,
) {
  const empty = children === undefined || children === null || children === "";
  return (
    <button
      ref={ref}
      type="button"
      disabled={disabled}
      aria-invalid={invalid || undefined}
      {...rest}
      className={cn(
        "inline-flex min-w-0 max-w-full items-center gap-1.5 rounded-md border border-line bg-surface px-2 text-fg outline-none transition-colors hover:border-line-strong focus-visible:shadow-[0_0_0_3px_var(--focus)] disabled:opacity-60 data-[state=open]:border-fg-3 aria-[invalid=true]:border-danger [&_svg]:size-3.5 [&_svg]:shrink-0",
        pickHeights[size],
        className,
      )}
    >
      {icon}
      <span className={cn("min-w-0 truncate", empty && "text-fg-4")}>{empty ? placeholder : children}</span>
      <ChevronDown className={cn("ml-auto", size === "md" ? "text-fg-3" : "!size-3 text-fg-4")} />
    </button>
  );
});

/** Single-value picker with a ValueButton trigger. */
export function SinglePick({
  items,
  value,
  onChange,
  placeholder,
  searchPlaceholder,
  onCreate,
  invalid,
  disabled,
  width,
  className,
  size,
  renderValue,
}: {
  items: PickerItem[];
  value: string | null | undefined;
  onChange: (v: string) => void;
  placeholder: string;
  searchPlaceholder?: string;
  onCreate?: (q: string) => void;
  invalid?: boolean;
  disabled?: boolean;
  width?: number;
  className?: string;
  size?: PickSize;
  /** Draw the chosen value in the trigger yourself (a tag chip, say); the icon is then left out. */
  renderValue?: (value: string) => ReactNode;
}) {
  useTranslation("settingsInbox");
  const cur = items.find((i) => i.value === value);
  const custom = value && renderValue ? renderValue(value) : null;
  const trigger = (
    <ValueButton placeholder={placeholder} icon={custom ? undefined : cur?.icon} invalid={invalid} disabled={disabled} className={className} size={size}>
      {custom ?? cur?.label ?? (value || undefined)}
    </ValueButton>
  );
  if (disabled) return trigger;
  return (
    <Picker
      items={items}
      value={value ?? null}
      onSelect={onChange}
      placeholder={searchPlaceholder ?? i18n.t("settingsInbox:pickers.search")}
      onCreate={onCreate}
      width={width}
      trigger={trigger}
    />
  );
}

/** Multi-value picker showing a compact summary of the selection. */
export function MultiPick({
  items,
  values,
  onChange,
  placeholder,
  searchPlaceholder,
  onQueryChange,
  disabled,
  className,
  size,
}: {
  items: PickerItem[];
  values: string[];
  onChange: (v: string[]) => void;
  placeholder: string;
  searchPlaceholder?: string;
  /** Server-side search: called as the query changes, and local filtering is off. */
  onQueryChange?: (q: string) => void;
  disabled?: boolean;
  className?: string;
  size?: PickSize;
}) {
  useTranslation("settingsInbox");
  const chosen = items.filter((i) => values.includes(i.value));
  const summary =
    chosen.length === 0
      ? undefined
      : chosen.length <= 2
        ? chosen.map((c) => c.label).join(", ")
        : i18n.t("settingsInbox:pickers.andMore", {
            first: chosen[0].label,
            count: chosen.length - 1,
          });
  const icons = (
    <span className="flex items-center gap-0.5">
      {chosen.slice(0, 3).map((c) => (
        <span key={c.value} className="flex">
          {c.icon}
        </span>
      ))}
    </span>
  );
  const trigger = (
    <ValueButton placeholder={placeholder} icon={chosen.length ? icons : undefined} disabled={disabled} className={className} size={size}>
      {summary}
    </ValueButton>
  );
  if (disabled) return trigger;
  return (
    <Picker
      multi
      items={items}
      values={values}
      onChange={onChange}
      placeholder={searchPlaceholder ?? i18n.t("settingsInbox:pickers.search")}
      onQueryChange={onQueryChange}
      trigger={trigger}
    />
  );
}

const contactItem = (c: Contact): PickerItem => ({
  value: c.id,
  label: c.name || c.email || i18n.t("settingsInbox:views.aContact"),
  hint: c.name ? (c.email ?? undefined) : undefined,
  keywords: c.email ? [c.email] : undefined,
  icon: <Avatar name={c.name || c.email || "?"} src={c.avatar_url} size="xs" />,
});

const companyItem = (c: Company): PickerItem => ({
  value: c.id,
  label: c.name,
  hint: c.domain || undefined,
  icon: <CompanyGlyph name={c.name} src={c.avatar_url} size="xs" />,
});

/** The contacts with these ids, as picker items; one not found (deleted, say) reads "a contact". */
export function useContactItems(ids: string[]): PickerItem[] {
  const { t } = useTranslation("settingsInbox");
  const rows = useQueries({ queries: ids.map((id) => q.contact(id)) });
  return ids.map((id, i) => {
    const c = rows[i]?.data;
    return c ? contactItem(c) : { value: id, label: t("views.aContact"), icon: <UserRound /> };
  });
}

/** The companies with these ids, as picker items; one not found reads "a company". */
export function useCompanyItems(ids: string[]): PickerItem[] {
  const { t } = useTranslation("settingsInbox");
  const rows = useQueries({ queries: ids.map((id) => q.company(id)) });
  return ids.map((id, i) => {
    const c = rows[i]?.data;
    return c ? companyItem(c) : { value: id, label: t("views.aCompany"), icon: <Building2 /> };
  });
}

/** The chosen rows first (those the query names, while searching), then the search results. */
function withChosen(chosen: PickerItem[], found: PickerItem[], query: string): PickerItem[] {
  const term = query.trim().toLowerCase();
  const shown = term ? chosen.filter((i) => [i.label, ...(i.keywords ?? [])].some((x) => x.toLowerCase().includes(term))) : chosen;
  return [...shown, ...found.filter((i) => !chosen.some((c) => c.value === i.value))];
}

type RecordsPickProps = { values: string[]; onChange: (v: string[]) => void; placeholder: string };

/** Several contacts, searched on the server by name or email. */
export function ContactsPick({ values, onChange, placeholder }: RecordsPickProps) {
  const { t } = useTranslation("settingsInbox");
  const [query, setQuery] = useState("");
  const term = useDebounced(query.trim(), 200);
  const found = useQuery({ queryKey: qk.contactPicker(term), queryFn: () => api.contacts.search({ q: term || undefined, limit: 12 }) });
  const items = withChosen(useContactItems(values), (found.data?.data ?? []).map(contactItem), query);
  return (
    <MultiPick items={items} values={values} onChange={onChange} placeholder={placeholder} searchPlaceholder={t("pickers.searchContacts")} onQueryChange={setQuery} />
  );
}

/** Several companies, searched on the server by name or domain. */
export function CompaniesPick({ values, onChange, placeholder }: RecordsPickProps) {
  const { t } = useTranslation("settingsInbox");
  const [query, setQuery] = useState("");
  const term = useDebounced(query.trim(), 200);
  const found = useQuery({ queryKey: qk.of("companies", "pick", term), queryFn: () => api.companies.search({ q: term || undefined, limit: 12 }) });
  const items = withChosen(useCompanyItems(values), (found.data?.data ?? []).map(companyItem), query);
  return (
    <MultiPick items={items} values={values} onChange={onChange} placeholder={placeholder} searchPlaceholder={t("pickers.searchCompanies")} onQueryChange={setQuery} />
  );
}

/** A tag picker whose trigger shows the tag as its coloured chip. */
export function TagPick(props: Omit<React.ComponentProps<typeof SinglePick>, "items" | "renderValue">) {
  const items = useTagItems();
  const { data } = useQuery(q.tags());
  return <SinglePick {...props} items={items} renderValue={(v) => <TagChip name={v} color={data?.find((t) => t.name === v)?.color} />} />;
}
