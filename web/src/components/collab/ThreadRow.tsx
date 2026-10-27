// One conversation in a mail list: unread weight, participants, subject, snippet,
// time, star, attachment, the account's dot when the list spans accounts, labels,
// assignees, shared and comment marks. Hover offers archive, trash, read and star.

import { motion, useReducedMotion } from "motion/react";
import { Archive, Mail, MailOpen, MessageSquare, Paperclip, Star, Trash2, Users } from "lucide-react";
import { forwardRef, memo, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { MailCategory, MailFolder, MailThread, MailThreadAction } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { fullDate, relShort } from "@/lib/format";
import { categoryTone } from "@/lib/collab/categories";
import { participantsLine } from "@/lib/collab/recipients";
import { tone } from "@/lib/tones";
import { AvatarStack } from "@/components/ui/avatar";
import { Checkbox } from "@/components/ui/checkbox";
import { Tooltip } from "@/components/ui/tooltip";
import { rowMotion } from "@/components/tickets/motion";
import { AccountDot, LabelPill } from "./bits";

export type ThreadRowProps = {
  thread: MailThread;
  active: boolean;
  focused: boolean;
  selected: boolean;
  selecting: boolean;
  compact: boolean;
  /** The list spans accounts: show which one each conversation is in. */
  accountColor?: string;
  accountName?: string;
  /** Labels to show as pills (Gmail labels on the conversation, not the folder being viewed). */
  labels: MailFolder[];
  /** The inbox category it is sorted into, when the list is not that category's tab. */
  category?: MailCategory;
  ownEmails: Set<string>;
  onOpen: (t: MailThread) => void;
  onToggle: (t: MailThread, shift: boolean) => void;
  onHover?: (t: MailThread) => void;
  /** Hover actions; left out for viewers. */
  onAction?: (t: MailThread, a: MailThreadAction) => void;
};

function Quick({ label, shortcut, onClick, children }: { label: string; shortcut?: string; onClick: () => void; children: ReactNode }) {
  return (
    <Tooltip content={label} shortcut={shortcut}>
      <button
        type="button"
        aria-label={label}
        onClick={(e) => {
          e.stopPropagation();
          onClick();
        }}
        className="flex size-6 items-center justify-center rounded-sm text-fg-3 outline-none transition-colors duration-100 hover:bg-hover hover:text-fg focus-visible:bg-hover focus-visible:text-fg active:scale-95 [&_svg]:size-3.5"
      >
        {children}
      </button>
    </Tooltip>
  );
}

const Row = forwardRef<HTMLDivElement, ThreadRowProps>(function ThreadRow(
  { thread: t, active, focused, selected, selecting, compact, accountColor, accountName, labels, category, ownEmails, onOpen, onToggle, onHover, onAction },
  ref,
) {
  const { t: tr } = useTranslation("collab");
  const reduced = useReducedMotion();
  const motionProps = rowMotion(reduced);
  const who = participantsLine(t.participants, ownEmails, tr("list.me")) || tr("list.me");
  const subject = t.subject || tr("list.noSubject");
  const showCheck = selecting || selected;

  const check = (
    <Checkbox
      checked={selected}
      aria-label={tr("list.select", { name: who })}
      onClick={(e) => {
        e.stopPropagation();
        if (e.shiftKey) {
          e.preventDefault();
          onToggle(t, true);
        }
      }}
      onCheckedChange={() => onToggle(t, false)}
    />
  );

  // The unread dot (or the account's), swapped for a checkbox on hover or while selecting.
  const lead = (
    <span className="relative flex size-4 shrink-0 items-center justify-center">
      <span className={cn("flex transition-opacity duration-100", showCheck ? "opacity-0" : "md:group-hover:opacity-0")}>
        {t.unread ? <span className="size-2 rounded-full bg-tone-blue-solid" aria-hidden /> : <span className="size-2" />}
      </span>
      <span
        className={cn(
          "absolute inset-0 flex items-center justify-center transition-opacity duration-100",
          showCheck ? "opacity-100" : "pointer-events-none opacity-0 md:group-hover:pointer-events-auto md:group-hover:opacity-100",
        )}
      >
        {check}
      </span>
    </span>
  );

  const star = (
    <button
      type="button"
      aria-label={t.starred ? tr("list.unstar") : tr("list.star")}
      aria-pressed={t.starred}
      disabled={!onAction}
      onClick={(e) => {
        e.stopPropagation();
        onAction?.(t, t.starred ? "unstar" : "star");
      }}
      className={cn(
        "flex size-5 shrink-0 items-center justify-center rounded-sm outline-none transition-[color,transform] duration-150 focus-visible:shadow-[0_0_0_2px_var(--focus)] active:scale-90 disabled:pointer-events-none",
        t.starred ? "text-tone-amber" : "text-fg-4 opacity-0 hover:text-fg-2 group-hover:opacity-100 focus-visible:opacity-100",
      )}
    >
      <Star className={cn("size-3.5", t.starred && "fill-current")} />
    </button>
  );

  const marks = (
    <>
      {t.has_attachments && (
        <Tooltip content={tr("list.attachments")}>
          <Paperclip className="size-3 shrink-0 text-fg-4" aria-label={tr("list.attachments")} />
        </Tooltip>
      )}
      {t.shared && (
        <Tooltip content={tr("list.sharedOn")}>
          <Users className="size-3 shrink-0 text-fg-4" aria-label={tr("list.sharedOn")} />
        </Tooltip>
      )}
      {t.comment_count > 0 && (
        <Tooltip content={tr("list.comments", { count: t.comment_count })}>
          <span className="mono flex shrink-0 items-center gap-0.5 text-[10.5px] text-fg-4">
            <MessageSquare className="size-3" />
            {t.comment_count}
          </span>
        </Tooltip>
      )}
    </>
  );

  const assignees = t.assignees.length > 0 && (
    <Tooltip content={tr("list.assignedTo", { names: t.assignees.map((a) => a.name).join(", ") })}>
      <span className="flex shrink-0">
        <AvatarStack size="xs" max={2} people={t.assignees.map((a) => ({ id: a.id, name: a.name, avatar: a.avatar_url }))} />
      </span>
    </Tooltip>
  );

  // Written to one of the account's aliases: which one.
  const alias = t.to_address ? (
    <Tooltip key="alias" content={tr("list.toAlias", { email: t.to_address })}>
      <span className="mono inline-flex h-[18px] max-w-[180px] shrink-0 items-center truncate rounded-sm bg-tone-blue-soft px-1.5 text-[10.5px] text-tone-blue" data-ph-mask>
        {t.to_address}
      </span>
    </Tooltip>
  ) : null;
  const tab = category ? (
    <span key="category" className={cn("inline-flex h-[18px] max-w-[120px] shrink-0 items-center rounded-sm px-1.5 text-[10.5px] font-medium", tone[categoryTone(category)].chip)}>
      <span className="truncate">{category.name}</span>
    </span>
  ) : null;
  const pills = [tab, alias, ...labels.slice(0, compact ? 2 : 3).map((l) => <LabelPill key={l.id} name={l.name} color={l.color} />)];

  const end = (
    <span className={cn("relative flex shrink-0 items-center justify-end", compact ? "h-4 min-w-[52px]" : "h-6 min-w-[72px]")}>
      <span className={cn("flex items-center gap-2 transition-opacity duration-100", onAction && "md:group-hover:opacity-0 md:group-focus-within:opacity-0")}>
        {!compact && assignees}
        <time
          dateTime={t.last_message_at}
          title={fullDate(t.last_message_at)}
          className={cn("mono whitespace-nowrap text-right tabular-nums", compact ? "text-[10.5px]" : "text-[11px]", t.unread ? "font-medium text-fg-2" : "text-fg-4")}
        >
          {relShort(t.last_message_at)}
        </time>
      </span>
      {onAction && (
        <span className="pointer-events-none absolute inset-y-0 right-0 hidden items-center gap-0.5 opacity-0 transition-opacity duration-100 group-hover:pointer-events-auto group-hover:opacity-100 group-focus-within:pointer-events-auto group-focus-within:opacity-100 md:flex">
          <Quick label={tr("action.archive")} shortcut="e" onClick={() => onAction(t, "archive")}>
            <Archive />
          </Quick>
          <Quick label={tr("action.trash")} shortcut="#" onClick={() => onAction(t, "trash")}>
            <Trash2 />
          </Quick>
          <Quick label={t.unread ? tr("action.read") : tr("action.unread")} shortcut={t.unread ? "shift i" : "shift u"} onClick={() => onAction(t, t.unread ? "read" : "unread")}>
            {t.unread ? <MailOpen /> : <Mail />}
          </Quick>
        </span>
      )}
    </span>
  );

  return (
    <motion.div
      ref={ref}
      role="option"
      aria-selected={active}
      data-thread-id={t.id}
      data-focused={focused || undefined}
      tabIndex={-1}
      onClick={() => onOpen(t)}
      onMouseEnter={() => onHover?.(t)}
      {...motionProps}
      className="relative z-[1] shrink-0 overflow-hidden outline-none"
    >
      {compact ? (
        <div
          className={cn(
            "group relative flex cursor-pointer gap-2.5 border-b border-line-soft py-2.5 pl-3.5 pr-3 transition-colors duration-100",
            active ? "bg-active" : selected ? "bg-hover" : focused ? "bg-sunken" : "hover:bg-sunken/70",
          )}
        >
          {focused && <span aria-hidden className={cn("absolute inset-y-2 left-0 w-0.5 rounded-r-xs", active ? "bg-fg" : "bg-fg-4")} />}
          <div className="flex w-4 shrink-0 flex-col items-center gap-[7px] pt-0.5">
            {lead}
            {star}
          </div>
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-1.5">
              {accountColor && <AccountDot color={accountColor} className="size-1.5" title={accountName} />}
              <span className={cn("min-w-0 truncate text-[12.5px]", t.unread ? "font-semibold text-fg" : "text-fg-2")} data-ph-mask>
                {who}
              </span>
              {t.message_count > 1 && <span className="mono shrink-0 text-[10.5px] text-fg-4">{t.message_count}</span>}
              <span className="flex-1" />
              {end}
            </div>
            <div className="mt-0.5 flex min-w-0 items-center gap-1.5">
              <span className={cn("min-w-0 truncate text-[12.5px]", t.unread ? "font-semibold text-fg" : "text-fg")} data-ph-mask>
                {subject}
              </span>
            </div>
            <div className="mt-1 flex min-w-0 items-center gap-1.5">
              <span className="min-w-0 flex-1 truncate text-xs text-fg-3" data-ph-mask>
                {t.snippet}
              </span>
              {pills}
              {marks}
              {assignees}
            </div>
          </div>
        </div>
      ) : (
        <div
          className={cn(
            "group relative flex h-11 cursor-pointer items-center gap-3 border-b border-line-soft pl-3.5 pr-4 transition-colors duration-100",
            active ? "bg-active" : selected ? "bg-hover" : focused ? "bg-sunken" : "hover:bg-sunken/70",
          )}
        >
          {focused && <span aria-hidden className={cn("absolute inset-y-2 left-0 w-0.5 rounded-r-xs", active ? "bg-fg" : "bg-fg-4")} />}
          {lead}
          {star}
          <span className="flex w-[200px] shrink-0 items-center gap-1.5">
            {accountColor && <AccountDot color={accountColor} className="size-1.5" title={accountName} />}
            <span className={cn("min-w-0 truncate text-[13px]", t.unread ? "font-semibold text-fg" : "text-fg-2")} data-ph-mask>
              {who}
            </span>
            {t.message_count > 1 && <span className="mono shrink-0 text-[10.5px] text-fg-4">{t.message_count}</span>}
          </span>
          <span className="flex min-w-0 flex-1 items-center gap-2">
            {pills}
            <span className={cn("max-w-[55%] shrink-0 truncate text-[13px]", t.unread ? "font-semibold text-fg" : "text-fg-2")} data-ph-mask>
              {subject}
            </span>
            <span className="min-w-0 flex-1 truncate text-[12.5px] text-fg-4" data-ph-mask>
              {t.snippet}
            </span>
            {marks}
          </span>
          {end}
        </div>
      )}
    </motion.div>
  );
});

export const ThreadRow = memo(Row);
