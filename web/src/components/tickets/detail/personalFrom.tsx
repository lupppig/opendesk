import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { ChevronDown, CornerDownLeft } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { Mailbox, MailAccount, MailIdentity, Message, Ticket } from "@/lib/api/types";
import { q } from "@/lib/queries";
import { atLeast, useRole, useSession } from "@/lib/session";
import { identityName, sendableIdentities } from "@/lib/collab/identities";
import { normEmail } from "@/lib/collab/recipients";
import { Picker } from "@/components/ui/picker";

// Answering a ticket from one of my own Collab addresses (contracts/collab.md "Answering
// tickets"): the composer offers them next to the team's addresses, and the server checks
// the choice again when the reply is sent.

/** The address a reply goes out from; replyTo is "address", "mailbox", an address, or "" for the workspace's choice. */
export type PersonalFrom = { accountId: string; email: string; replyTo: string };

export type PersonalSender = { account: MailAccount; identity: MailIdentity };

/** The value a Collab address takes in the From picker, apart from a mailbox id. */
export const personalValue = (accountId: string, email: string) => `collab:${accountId}:${normEmail(email)}`;

export function parsePersonalValue(v: string): { accountId: string; email: string } | null {
  const m = /^collab:([^:]+):(.+)$/.exec(v);
  return m ? { accountId: m[1], email: m[2] } : null;
}

/**
 * The Collab addresses I may answer this ticket from, its default Reply-To, and the address
 * I last answered it from (where my next reply starts). The rules are the workspace's, each
 * overridden by the ticket's current reply address (mailbox.personal_replies), which may
 * also limit the addresses to shared accounts or its own domains. Nothing without Collab,
 * with replies like this off, on a chat ticket or for a viewer.
 */
export function usePersonalSenders(ticket: Ticket, mailbox?: Mailbox) {
  const me = useSession((s) => s.user!.id);
  const role = useRole();
  const { data: status } = useQuery(q.collabStatus());
  const { data: workspace } = useQuery(q.workspace());
  const rules = mailbox?.personal_replies;
  const domains = mailbox ? [mailbox.email, ...(mailbox.aliases ?? [])].map((a) => a.split("@")[1]?.toLowerCase()) : [];
  const allowed = (s: PersonalSender) =>
    rules?.accounts === "shared"
      ? s.account.visibility === "shared"
      : rules?.accounts === "domain"
        ? domains.includes(s.identity.email.split("@")[1]?.toLowerCase())
        : true;
  const on =
    !!status?.enabled &&
    (rules?.enabled ?? workspace?.settings.collab_ticket_replies !== false) &&
    ticket.channel === "email" &&
    !!ticket.contact?.email &&
    atLeast(role, "agent");
  const { data: accounts } = useQuery({ ...q.mailAccounts(), enabled: on });
  const { data: messages } = useQuery({ ...q.messages(ticket.id), enabled: on });
  const senders: PersonalSender[] = on
    ? (accounts ?? [])
        .filter((a) => !a.reconnect)
        .flatMap((a) => sendableIdentities(a).map((identity) => ({ account: a, identity })))
        .filter(allowed)
    : [];
  const find = (f: { accountId: string; email: string } | null | undefined) =>
    f ? senders.find((s) => s.account.id === f.accountId && normEmail(s.identity.email) === normEmail(f.email)) : undefined;
  const last = lastMine(messages, me);
  const sticky: PersonalFrom | undefined =
    last?.from_account && find({ accountId: last.from_account.account_id, email: last.from_account.email })
      ? { accountId: last.from_account.account_id, email: last.from_account.email, replyTo: last.from_account.reply_to }
      : undefined;
  return { senders, find, sticky, defaultReplyTo: rules?.reply_to ?? workspace?.settings.collab_ticket_reply_to ?? "address" };
}

/** My latest reply on the ticket that went (or is going) out. */
function lastMine(messages: Message[] | undefined, me: string): Message | undefined {
  return [...(messages ?? [])].reverse().find((m) => m.direction === "outbound" && m.kind === "reply" && m.author.user?.id === me && !m.deleted_at);
}

const emailRe = /^[^\s@,;]+@[^\s@,;]+\.[^\s@,;]+$/;

/**
 * Where the customer's answers to a reply from my own address go: the address's own
 * reply-to (else the address), the ticket's reply address, or one typed here.
 */
export function ReplyToLine({
  sender,
  value,
  fallback,
  mailboxAddress,
  onChange,
}: {
  sender: PersonalSender;
  /** "" follows the workspace's choice (fallback). */
  value: string;
  fallback: "address" | "mailbox";
  /** The ticket's reply address; null when it has none. */
  mailboxAddress: string | null;
  onChange: (v: string) => void;
}) {
  const { t } = useTranslation("inbox");
  const [open, setOpen] = useState(false);
  const mode = value || fallback;
  const own = sender.identity.reply_to || sender.identity.email;
  const shown = mode === "address" ? own : mode === "mailbox" ? (mailboxAddress ?? own) : mode;
  const items = [
    { value: "address", label: t("composer.replyToAddress", { address: own }), keywords: [own] },
    ...(mailboxAddress ? [{ value: "mailbox", label: t("composer.replyToMailbox", { address: mailboxAddress }), keywords: [mailboxAddress] }] : []),
    ...(mode !== "address" && mode !== "mailbox" ? [{ value: mode, label: mode }] : []),
  ];
  return (
    <div className="flex min-h-7 items-center gap-2 px-3 pb-1">
      <span className="shrink-0 text-xs text-fg-3">{t("composer.answersGoTo")}</span>
      <Picker
        open={open}
        onOpenChange={setOpen}
        width={320}
        value={mode}
        items={items}
        placeholder={t("composer.replyToSearch")}
        emptyText={t("composer.replyToEmpty")}
        onCreate={(v) => {
          const a = v.trim().toLowerCase();
          if (!emailRe.test(a)) return;
          onChange(a);
          setOpen(false);
        }}
        createLabel={t("composer.replyToUse")}
        onSelect={(v) => onChange(v === fallback ? "" : v)}
        trigger={
          <button
            type="button"
            aria-label={t("composer.changeReplyTo")}
            className="group flex h-6 min-w-0 max-w-full items-center gap-1.5 rounded-sm px-1.5 text-left text-[12px] outline-none transition-colors hover:bg-hover focus-visible:shadow-[0_0_0_2px_var(--focus)] data-[state=open]:bg-hover"
          >
            <CornerDownLeft className="size-3 shrink-0 text-fg-4" aria-hidden />
            <span className="mono min-w-0 truncate text-[11.5px] text-fg-2" data-ph-mask>
              {shown}
            </span>
            <ChevronDown className="size-3 shrink-0 text-fg-4 transition-colors group-hover:text-fg-2" />
          </button>
        }
      />
    </div>
  );
}

/** "Name <email>" of a Collab address, as the customer sees it. */
export function personalFromLine(s: PersonalSender) {
  return { name: identityName(s.identity, s.account), email: s.identity.email };
}
