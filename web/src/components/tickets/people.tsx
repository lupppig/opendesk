import { useTranslation } from "react-i18next";
import type { Ticket } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import type { AssigneeDisplay } from "@/lib/listDisplay";
import type { Viewer } from "@/lib/realtime/presence";
import { tone } from "@/lib/tones";
import { Avatar, AvatarStack } from "@/components/ui/avatar";
import { Tooltip } from "@/components/ui/tooltip";
import { useTicketViewers } from "./presence";

// The two kinds of people on a ticket row, kept apart: who is on it right now (live,
// green, said in words) and who it is assigned to (still, neutral, at the row's end).

const first = (name: string) => name.trim().split(/\s+/)[0] || name;

/**
 * Teammates on the ticket right now: ringed avatars and a short label in a green pill.
 * `bare` keeps only the avatars (compact rows, or a chat whose wait pill already names
 * them); the label is then read out but not shown.
 */
export function LiveViewers({ viewers, bare }: { viewers: Viewer[]; bare?: boolean }) {
  const { t } = useTranslation("inbox");
  if (!viewers.length) return null;
  const full = viewers.map((x) => t(x.state === "replying" ? "presence.replying" : "presence.viewing", { name: x.name })).join(", ");
  // "Chimène viewing", "Chimène replying…", "2 viewing" or "Chimène replying… +1".
  const replying = viewers.find((x) => x.state === "replying");
  const label =
    viewers.length === 1 ? t(replying ? "presence.oneReplying" : "presence.oneViewing", { name: first(viewers[0].name) })
    : replying ? t("presence.manyReplying", { name: first(replying.name), others: viewers.length - 1 })
    : t("presence.manyViewing", { n: viewers.length });
  const stack = <AvatarStack size="xs" max={3} live people={viewers.map((x) => ({ id: x.user_id, name: x.name, avatar: x.avatar, state: x.state }))} />;
  return (
    <Tooltip content={full}>
      {bare ? (
        <span className="flex shrink-0 animate-pop-in px-0.5 motion-reduce:animate-none" data-live-presence>
          {stack}
          <span className="sr-only">{label}</span>
        </span>
      ) : (
        <span
          className="inline-flex h-5 min-w-0 shrink-0 animate-pop-in items-center gap-1.5 rounded-full bg-tone-green-soft pl-[3px] pr-2 text-[11px] font-medium text-tone-green motion-reduce:animate-none"
          data-live-presence
        >
          {stack}
          <span className="max-w-[128px] truncate">{label}</span>
        </span>
      )}
    </Tooltip>
  );
}

/** The teammates on a ticket right now, live from presence. */
export function Viewers({ ticketId, bare }: { ticketId: string; bare?: boolean }) {
  return <LiveViewers viewers={useTicketViewers(ticketId)} bare={bare} />;
}

/** Who the ticket is assigned to, as this browser chose: their photo, their first name, or nothing. */
export function AssigneeMark({ assignee, mode, compact }: { assignee: NonNullable<Ticket["assignee"]>; mode: AssigneeDisplay; compact?: boolean }) {
  const { t } = useTranslation("inbox");
  if (mode === "none") return null;
  const said = <span className="sr-only">{t("row.assignedTo", { name: assignee.name })}</span>;
  return (
    <Tooltip content={assignee.name}>
      {mode === "name" ? (
        <span
          className={cn(
            "inline-flex min-w-0 shrink items-center rounded-full font-medium",
            tone.gray.chip,
            compact ? "h-4 max-w-[64px] px-1.5 text-[10.5px]" : "h-5 max-w-[84px] px-2 text-[11px]",
          )}
          data-assignee
        >
          <span className="truncate" aria-hidden>
            {first(assignee.name)}
          </span>
          {said}
        </span>
      ) : (
        <span className="flex" data-assignee>
          <Avatar name={assignee.name} src={assignee.avatar_url} size={compact ? "xs" : "sm"} />
          {said}
        </span>
      )}
    </Tooltip>
  );
}
