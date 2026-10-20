// What of the customer's conversation goes along when a side conversation starts
// (contracts/api.md "Threads"): nothing unless picked in the send menu, and what is picked
// shows next to the uploads, where it can be taken out again.

import { useQuery } from "@tanstack/react-query";
import { FileText, Quote, X } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { MenuCheckItem, MenuLabel } from "@/components/ui/menu";
import type { Attachment, Message } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { bytes } from "@/lib/format";
import { q } from "@/lib/queries";

/** Messages the first email can quote: the server takes the last 10. */
const MAX_QUOTED = 10;

/** The customer's conversation: public replies that reached them or came from them. */
function conversationOf(messages: Message[] | undefined): Message[] {
  return (messages ?? []).filter(
    (m) =>
      m.kind === "reply" &&
      !m.thread_id &&
      !m.id.startsWith("tmp-") &&
      (m.direction === "inbound" ? !m.auth_failed : m.delivery_status === "sent" || m.delivery_status === "none"),
  );
}

export type SideInclude = {
  /** How many messages the conversation would quote; 0 when there is none. */
  quoted: number;
  /** The files of the conversation, oldest first. */
  files: Attachment[];
  history: boolean;
  /** The picked files, as attachment ids. */
  picked: string[];
  onChange: (p: { threadHistory?: boolean; threadFiles?: string[] }) => void;
};

/** What a new side conversation on the ticket could take along, and what is picked. */
export function useSideInclude(
  ticketId: string,
  history: boolean,
  picked: string[],
  onChange: SideInclude["onChange"],
): SideInclude {
  const { data: messages } = useQuery(q.messages(ticketId));
  const conversation = conversationOf(messages);
  const files: Attachment[] = [];
  for (const m of conversation) for (const a of m.attachments) if (!files.some((x) => x.id === a.id)) files.push(a);
  return {
    quoted: Math.min(conversation.length, MAX_QUOTED),
    files,
    history: history && conversation.length > 0,
    picked: picked.filter((id) => files.some((a) => a.id === id)),
    onChange,
  };
}

/** The tooltip's words for what goes along, or "" for nothing. */
export function useIncludeSummary(inc: SideInclude | undefined): string {
  const { t } = useTranslation("inbox");
  if (!inc) return "";
  const n = inc.picked.length;
  if (inc.history && n) return t("side.withBoth", { count: n });
  if (inc.history) return t("side.withConversation");
  if (n) return t("side.withFiles", { count: n });
  return "";
}

/** The send menu's "Include" section. Picking keeps the menu open. */
export function SideIncludeItems({ inc }: { inc: SideInclude }) {
  const { t } = useTranslation("inbox");
  const toggle = (id: string) => inc.onChange({ threadFiles: inc.picked.includes(id) ? inc.picked.filter((x) => x !== id) : [...inc.picked, id] });
  return (
    <>
      <MenuLabel>{t("side.include")}</MenuLabel>
      {inc.quoted > 0 && (
        <MenuCheckItem
          checked={inc.history}
          onSelect={() => inc.onChange({ threadHistory: !inc.history })}
          icon={<Quote />}
          hint={t("side.messages", { count: inc.quoted })}
        >
          {t("side.conversation")}
        </MenuCheckItem>
      )}
      {inc.files.length > 1 && (
        <MenuCheckItem
          checked={inc.picked.length === inc.files.length}
          onSelect={() => inc.onChange({ threadFiles: inc.picked.length === inc.files.length ? [] : inc.files.map((a) => a.id) })}
          icon={<FileText />}
          hint={inc.files.length}
        >
          {t("side.allFiles")}
        </MenuCheckItem>
      )}
      <div className={cn("max-h-[184px] overflow-y-auto", inc.files.length > 1 && "pl-4")}>
        {inc.files.map((a) => (
          <MenuCheckItem key={a.id} checked={inc.picked.includes(a.id)} onSelect={() => toggle(a.id)} icon={<FileText />} hint={bytes(a.size_bytes)}>
            <span data-ph-mask>{a.filename}</span>
          </MenuCheckItem>
        ))}
      </div>
    </>
  );
}

/** What is picked, next to the uploads: each can be taken out again. */
export function SideIncludedChips({ inc }: { inc: SideInclude }) {
  const { t } = useTranslation("inbox");
  const files = inc.files.filter((a) => inc.picked.includes(a.id));
  return (
    <>
      {inc.history && (
        <Chip
          icon={<Quote className="size-3 text-fg-3" />}
          title={t("side.conversation")}
          meta={t("side.messages", { count: inc.quoted })}
          remove={t("side.remove", { name: t("side.conversation") })}
          onRemove={() => inc.onChange({ threadHistory: false })}
        />
      )}
      {files.map((a) => (
        <Chip
          key={a.id}
          icon={<FileText className="size-3 text-fg-3" />}
          title={a.filename}
          meta={t("side.fromCustomer", { size: bytes(a.size_bytes) })}
          remove={t("side.remove", { name: a.filename })}
          onRemove={() => inc.onChange({ threadFiles: inc.picked.filter((x) => x !== a.id) })}
          mask
        />
      ))}
    </>
  );
}

function Chip({ icon, title, meta, remove, onRemove, mask }: { icon: ReactNode; title: string; meta: string; remove: string; onRemove: () => void; mask?: boolean }) {
  return (
    <span className="flex h-8 max-w-[240px] items-center gap-2 rounded-md border border-line bg-surface pl-1 pr-1 animate-pop-in motion-reduce:animate-none">
      <span className="flex size-6 shrink-0 items-center justify-center rounded-sm bg-hover">{icon}</span>
      <span className="min-w-0">
        <span className="block truncate text-[11.5px]" data-ph-mask={mask || undefined}>
          {title}
        </span>
        <span className="mono block truncate text-[10px] text-fg-4">{meta}</span>
      </span>
      <button type="button" aria-label={remove} onClick={onRemove} className="flex size-5 shrink-0 items-center justify-center rounded-sm text-fg-4 hover:bg-hover hover:text-fg">
        <X className="size-3" />
      </button>
    </span>
  );
}
