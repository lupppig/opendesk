// A ticket's side conversations with people outside the team, in the sidebar: who was asked,
// whether they answered, and a way into the timeline where the messages are.

import { useQuery } from "@tanstack/react-query";
import { Plus } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Tooltip } from "@/components/ui/tooltip";
import type { Message, Thread } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { relShort } from "@/lib/format";
import { q } from "@/lib/queries";
import { tone } from "@/lib/tones";
import { showInTimeline } from "./Timeline";

type State = "waiting" | "answered" | "closed";

const stateTone = { waiting: tone.amber.chip, answered: tone.green.chip, closed: tone.gray.chip } as const;

function latestIn(messages: Message[] | undefined, id: string): Message | undefined {
  let last: Message | undefined;
  for (const m of messages ?? []) if (m.thread_id === id && (!last || m.created_at >= last.created_at)) last = m;
  return last;
}

export function SideConversations({ ticketId, canEdit, onStart }: { ticketId: string; canEdit: boolean; onStart?: () => void }) {
  const { t } = useTranslation("inbox");
  const { data: threads } = useQuery(q.threads(ticketId));
  const { data: messages } = useQuery(q.messages(ticketId));
  const canStart = canEdit && !!onStart;
  // With none yet, the heading alone stays for whoever can start the first one.
  if (!threads || (!threads.length && !canStart)) return null;

  // The timeline scrolls to the message and rings it for a moment.
  const open = (m: Message | undefined) => m && showInTimeline(m.id);
  // Open ones first, the most recent on top.
  const sorted = [...threads].sort((a, b) => (a.status === b.status ? b.last_message_at.localeCompare(a.last_message_at) : a.status === "open" ? -1 : 1));

  return (
    <section className="border-b border-line-soft px-4 py-3.5" aria-label={t("side.title")}>
      <div className={cn("flex items-center justify-between", sorted.length > 0 && "mb-2")}>
        <h3 className="label-caps">{t("side.title")}</h3>
        {canStart && (
          <Tooltip content={t("side.newHint")}>
            <button
              type="button"
              onClick={onStart}
              className="-mr-1 inline-flex h-5 items-center gap-0.5 rounded-sm px-1 text-[11.5px] text-fg-3 outline-none transition-colors hover:bg-hover hover:text-fg focus-visible:shadow-[0_0_0_2px_var(--focus)]"
            >
              <Plus className="size-3" />
              {t("side.new")}
            </button>
          </Tooltip>
        )}
      </div>
      {sorted.length > 0 && (
        <ul className="flex flex-col gap-0.5">
          {sorted.map((th: Thread) => {
            const last = latestIn(messages, th.id);
            const state: State = th.status === "closed" ? "closed" : last?.direction === "inbound" ? "answered" : "waiting";
            return (
              <li key={th.id}>
                <button
                  type="button"
                  onClick={() => open(last)}
                  aria-label={t("side.open", { subject: th.subject })}
                  className="group -mx-1.5 flex w-[calc(100%+12px)] items-start gap-2 rounded-md px-1.5 py-1 text-left outline-none transition-colors hover:bg-sunken focus-visible:shadow-[0_0_0_2px_var(--focus)]"
                >
                  <span className="min-w-0 flex-1">
                    <span className={cn("block truncate text-[12.5px] group-hover:text-fg", state === "closed" ? "text-fg-3" : "text-fg-2")}>{th.subject}</span>
                    <span className="mono block truncate text-[11px] text-fg-4" data-ph-mask>
                      {[...th.to, ...th.cc].join(", ")}
                    </span>
                  </span>
                  <span className="flex shrink-0 flex-col items-end gap-0.5">
                    <Tooltip content={state === "closed" ? undefined : t(state === "waiting" ? "side.waitingHint" : "side.answeredHint")}>
                      <span className={cn("rounded-sm px-1.5 py-px text-[10.5px] font-medium", stateTone[state])}>{t(`side.${state}`)}</span>
                    </Tooltip>
                    <span className="mono text-[10.5px] text-fg-4">{relShort(th.last_message_at)}</span>
                  </span>
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}
