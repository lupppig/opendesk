// A read-only look at the focused ticket, beside the list on wide screens.
// It follows j/k without opening the ticket (no presence, nothing marked),
// and Enter or a click opens it for real.

import { useQuery } from "@tanstack/react-query";
import { AnimatePresence, motion } from "motion/react";
import { ChevronDown, CornerDownLeft, MessageSquare } from "lucide-react";
import { useState } from "react";
import { cn } from "@/lib/cn";
import { dur, ease } from "@/lib/motion";
import { useTranslation } from "react-i18next";
import type { Ticket } from "@/lib/api/types";
import { q } from "@/lib/queries";
import { relLong } from "@/lib/format";
import { Avatar } from "@/components/ui/avatar";
import { TagChip } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Tooltip } from "@/components/ui/tooltip";
import { ChannelIcon, channelLabel, PriorityPill, SlaPill, slaState, StatusPill, TicketNumber } from "./bits";
import { Timeline } from "./detail/Timeline";
import { useDisplay } from "./fields";
import { TicketTeamChips, useTeamsCtx } from "./teams";
import { requesterLabel } from "./requester";

const noop = () => {};

export function TicketPreview({ ticket: t, onOpen, onReply }: { ticket: Ticket; onOpen: () => void; onReply?: () => void }) {
  const { t: tr } = useTranslation("inbox");
  useTranslation("indicators");
  const messages = useQuery(q.messages(t.id));
  const activities = useQuery(q.activities(t.id));
  const display = useDisplay();
  const contact = requesterLabel(t.contact) || tr("row.unknown");
  const sla = slaState(t);
  const [more, setMore] = useState(false);
  const teams = useTeamsCtx();

  return (
    <div className="flex h-full min-h-0 flex-col" aria-label={tr("preview.label")}>
      <header className="flex h-12 shrink-0 items-center gap-2 border-b border-line px-4">
        <TicketNumber n={t.number} className="shrink-0" />
        <h2 className="min-w-0 flex-1 truncate text-[14px] font-semibold tracking-[-0.01em]" data-ph-mask>
          {t.subject || tr("row.noSubject")}
        </h2>
        {onReply && (
          <Tooltip content={tr("preview.reply")} shortcut="r">
            <Button size="sm" variant="ghost" icon={<MessageSquare className="size-3.5" />} onClick={onReply}>
              {tr("preview.replyShort")}
            </Button>
          </Tooltip>
        )}
        <Tooltip content={tr("preview.openHint")} shortcut="enter">
          <Button size="sm" variant="secondary" iconRight={<CornerDownLeft className="size-3 text-fg-4" />} onClick={onOpen}>
            {tr("preview.open")}
          </Button>
        </Tooltip>
      </header>
      <div className="shrink-0 border-b border-line-soft px-4 py-2">
        <div className="flex flex-wrap items-center gap-1.5">
          <StatusPill status={t.status} label={display.status(t.status)} hue={display.statusTone(t.status)} />
          <PriorityPill priority={t.priority} label={display.priority(t.priority)} hue={display.priorityTone(t.priority)} />
          {sla && sla.state !== "on_track" && <SlaPill ticket={t} />}
          <TicketTeamChips t={t} ctx={teams} />
          <span className="ml-1 flex shrink-0 items-center gap-1.5 text-xs">
            {t.assignee ? (
              <>
                <Avatar name={t.assignee.name} src={t.assignee.avatar_url} size="xs" />
                <span className="text-fg-2">{t.assignee.name}</span>
              </>
            ) : (
              <span className="text-fg-4">{tr("preview.unassigned")}</span>
            )}
          </span>
          <button
            type="button"
            aria-expanded={more}
            onClick={() => setMore((m) => !m)}
            className="ml-auto flex h-6 items-center gap-1 rounded-sm px-1.5 text-xs text-fg-3 outline-none transition-colors hover:bg-hover hover:text-fg focus-visible:ring-1 focus-visible:ring-line-strong"
          >
            {more ? tr("preview.less") : tr("preview.more")}
            <ChevronDown className={cn("size-3 transition-transform duration-150", more && "rotate-180")} />
          </button>
        </div>
        <AnimatePresence initial={false}>
          {more && (
            <motion.div
              initial={{ height: 0, opacity: 0 }}
              animate={{ height: "auto", opacity: 1 }}
              exit={{ height: 0, opacity: 0 }}
              transition={{ duration: dur.base, ease: ease.out }}
              className="overflow-hidden"
            >
              <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 pt-2 text-xs text-fg-3">
                <span className="flex min-w-0 items-center gap-1.5">
                  <ChannelIcon channel={t.channel} plain />
                  <span className="truncate text-fg-2" data-ph-mask>
                    {contact}
                  </span>
                  <span className="text-fg-4">· {channelLabel(t.channel)}</span>
                </span>
                {sla && sla.state === "on_track" && <SlaPill ticket={t} />}
                {t.tags.map((tag) => (
                  <TagChip key={tag.id} name={tag.name} color={tag.color} />
                ))}
                {teams.multiAddress && t.received_at && (
                  <span className="mono text-[11px] text-fg-3" data-ph-mask>
                    {tr("row.receivedAt", { address: t.received_at })}
                  </span>
                )}
                <span className="text-fg-4">{tr("preview.created", { when: relLong(t.created_at) })}</span>
              </div>
            </motion.div>
          )}
        </AnimatePresence>
      </div>
      <div className="flex min-h-0 flex-1 flex-col">
        <Timeline
          ticketId={t.id}
          messages={messages.data}
          activities={activities.data}
          loading={!messages.data}
          onRetry={noop}
          contactName={t.contact?.name}
          receivedAt={t.channel === "email" ? t.received_at : null}
        />
      </div>
    </div>
  );
}
