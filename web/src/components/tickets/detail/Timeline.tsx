import { useQuery } from "@tanstack/react-query";
import { AlarmClock, Hand, ShieldAlert, Sparkles, ArrowRightLeft, CircleDot, GitMerge, Link2, MessagesSquare, PencilLine, Star, Tag as TagIcon, TimerOff, Trash2, Undo2, Users, UserRound, Reply } from "lucide-react";
import { Fragment, useEffect, useLayoutEffect, useMemo, useRef, type ReactNode } from "react";
import { Trans, useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { useReducedMotion } from "motion/react";
import { create } from "zustand";
import type { Activity, Mailbox, Member, Message, Priority, Tag, Team, Thread, TicketStatus, UserRef } from "@/lib/api/types";
import { dayLabel, fullDate, relShort, shortDate } from "@/lib/format";
import { q } from "@/lib/queries";
import { useRealtime, isCustomerTyping } from "@/lib/realtime/store";
import { Skeleton } from "@/components/ui/skeleton";
import { TagChip } from "@/components/ui/badge";
import { cn } from "@/lib/cn";
import { AI_TONE, tone } from "@/lib/tones";
import { Tooltip } from "@/components/ui/tooltip";
import { PriorityIcon, priorityOrder, SeverityChip, severityLabel, StatusIcon, statusOrder } from "../bits";
import { isSeverity, pct } from "@/lib/decisions";
import { MessageItem, type ThreadActions } from "./MessageItem";
import { LiveAgentReply } from "./AgentReply";
import { BotFace } from "@/components/bots/BotFace";
import { isAIActor, OllieAvatar, useAssistantName } from "../ollie";
import { fieldText, useDisplay, useTicketFields, type CustomField } from "../fields";
import { messageSide, sideClass } from "./messageFrame";
import { useConversationLayout } from "@/lib/conversationLayout";

type Item = { kind: "message"; at: string; m: Message } | { kind: "activity"; at: string; a: Activity };

/** The message the timeline brings into view and rings for a moment; `at` tells two asks for the same one apart. */
const usePointedAt = create<{ id: string | null; at: number }>(() => ({ id: null, at: 0 }));

/** Shows a message in the timeline (a side conversation in the sidebar points at its latest). */
export function showInTimeline(messageId: string) {
  usePointedAt.setState({ id: messageId, at: Date.now() });
}

/** A user in activity data: a UserRef, or a user id (resolved through the members list). */
function ref(v: unknown, members: Member[] | undefined): UserRef | null {
  if (v && typeof v === "object" && "name" in (v as object)) return v as UserRef;
  if (typeof v === "string") return members?.find((m) => m.user.id === v)?.user ?? { id: v, name: "a former member", email: "", avatar_url: "" };
  return null;
}

function actorName(a: Activity, t: TFunction<"inbox">, ollie: string) {
  // The AI acts on someone's behalf (actor.user), but the change is Ollie's.
  if (isAIActor(a.actor)) return ollie;
  if (a.actor.user) return a.actor.user.name;
  if (typeof a.data.automation_name === "string") return t("activity.automationNamed", { name: a.data.automation_name });
  if (a.actor.type === "automation") return t("activity.automation");
  if (a.actor.type === "contact") return t("activity.customer");
  return "opendesk";
}

const B = <span className="font-medium text-fg-2" />;
const M = <span className="mono text-fg-2" />;

type Line = { icon: ReactNode; text: ReactNode; hue?: "red" | "amber" | "ai" };

/** Only https links from other tools are opened. */
function safeLink(v: unknown): string | null {
  if (typeof v !== "string") return null;
  try {
    const u = new URL(v);
    return u.protocol === "https:" ? u.href : null;
  } catch {
    return null;
  }
}

type Ctx = { teams?: Team[]; members?: Member[]; tags?: Tag[]; fields?: CustomField[]; mailboxes?: Mailbox[]; display: ReturnType<typeof useDisplay>; ollie: string };

/** A team in activity data: an id, a name (older activities) or an object with one. */
function teamLabel(v: unknown, teams: Team[] | undefined): string | null {
  if (!v) return null;
  if (typeof v === "object" && "name" in (v as object)) return String((v as { name: unknown }).name);
  if (typeof v === "string") return teams?.find((x) => x.id === v)?.name ?? (/^[0-9a-f-]{36}$/i.test(v) ? null : v);
  return null;
}

function contactLabel(v: unknown, t: TFunction<"inbox">): string {
  if (v && typeof v === "object") {
    const contact = v as { name?: unknown; email?: unknown };
    if (typeof contact.name === "string" && contact.name) return contact.name;
    if (typeof contact.email === "string" && contact.email) return contact.email;
  }
  return t("activity.noContact");
}

/** An address in activity data: a mailbox id, an email or an object with one. */
function addressOf(v: unknown, mailboxes: Mailbox[] | undefined): string | null {
  if (!v) return null;
  if (typeof v === "object" && "email" in (v as object)) return String((v as { email: unknown }).email);
  if (typeof v === "string") return v.includes("@") ? v : (mailboxes?.find((m) => m.id === v)?.email ?? null);
  return null;
}

function describe(a: Activity, t: TFunction<"inbox">, { teams, members, tags, fields, mailboxes, display, ollie }: Ctx): Line {
  const who = actorName(a, t, ollie);
  const d = a.data;
  const line = (key: Parameters<TFunction<"inbox">>[0], values: Record<string, unknown> = {}) => (
    <Trans t={t} i18nKey={key as never} values={{ who, ...values }} components={{ b: B, m: M }} />
  );
  switch (a.type) {
    case "note_edited":
      return { icon: <PencilLine />, text: line("activity.noteEdited") };
    case "note_deleted":
      return { icon: <Trash2 />, text: line("activity.noteDeleted") };
    case "reply_retracted":
      return { icon: <Undo2 />, text: line("activity.replyRetracted") };
    case "message_deleted":
      return { icon: <Trash2 />, text: line("activity.messageDeleted") };
    case "message_purged":
      return { icon: <Trash2 />, text: line("activity.messagePurged") };
    case "message_restored":
      return { icon: <Undo2 />, text: line("activity.messageRestored") };
    case "note_sent_as_reply":
      return { icon: <Reply />, text: line("activity.noteSentAsReply") };
    case "status_changed": {
      const to = d.to as TicketStatus;
      if (d.reason === "thread_replied") {
        // An answer in a third-party thread brought a waiting ticket back.
        return { icon: <MessagesSquare />, text: line("activity.statusThread") };
      }
      if (d.reason === "sender_unverified") {
        // A reply that failed sender checks did not reopen the ticket.
        return {
          icon: <ShieldAlert />,
          hue: "amber",
          text: line("activity.statusKept", { status: statusOrder.includes(to) ? display.status(to) : String(d.to) }),
        };
      }
      return {
        icon: statusOrder.includes(to) ? <StatusIcon status={to} className="size-3" /> : <CircleDot />,
        text: line("activity.status", { status: (statusOrder.includes(to) ? display.status(to) : String(d.to)).toLowerCase() }),
      };
    }
    case "priority_changed": {
      const to = d.to as Priority;
      return {
        icon: priorityOrder.includes(to) ? <PriorityIcon priority={to} className="size-3" /> : <CircleDot />,
        text: line("activity.priority", { priority: priorityOrder.includes(to) ? display.priority(to) : String(d.to) }),
      };
    }
    case "severity_changed": {
      const to = d.to;
      return isSeverity(to)
        ? { icon: <SeverityChip severity={to} className="h-[13px] min-w-[18px] px-0.5 text-[8.5px]" />, text: line("activity.severity", { severity: severityLabel(to) }) }
        : { icon: <CircleDot />, text: line("activity.severityCleared") };
    }
    case "assigned": {
      const to = ref(d.to, members);
      return {
        icon: <UserRound />,
        text: to ? (a.actor.user?.id === to.id && !isAIActor(a.actor) ? line("activity.took") : line("activity.assigned", { to: to.name })) : line("activity.unassigned"),
      };
    }
    case "team_changed": {
      const to = teamLabel(d.to, teams);
      const from = teamLabel(d.from, teams);
      const rule = typeof d.rule_name === "string" ? d.rule_name : null;
      const main = !to
        ? line("activity.teamRemoved")
        : d.via === "rule" && rule
          ? line("activity.teamRule", { team: to, rule })
          : from
            ? line("activity.teamFrom", { from, team: to })
            : line("activity.team", { team: to });
      const replyTo = addressOf(d.mailbox_to, mailboxes);
      return {
        icon: <Users />,
        text: replyTo ? (
          <>
            {main}
            <span className="text-fg-4"> · </span>
            {line("activity.replyAddress", { address: replyTo })}
          </>
        ) : (
          main
        ),
      };
    }
    case "contact_changed":
      return {
        icon: <Users />,
        text: line("activity.contactChanged", { from: contactLabel(d.from, t), to: contactLabel(d.to, t) }),
      };
    case "tagged":
    case "untagged": {
      const raw = d.tag as string | { name?: string; color?: string | null } | undefined;
      const name = typeof raw === "string" ? raw : (raw?.name ?? "");
      const color = (typeof raw === "object" ? raw?.color : null) ?? tags?.find((x) => x.name === name)?.color;
      return {
        icon: <TagIcon />,
        text: (
          <span className="inline-flex min-w-0 items-center gap-1.5">
            {line(a.type === "tagged" ? "activity.tagged" : "activity.untagged")}
            <TagChip name={name} color={color} className={cn(a.type === "untagged" && "opacity-60")} />
          </span>
        ),
      };
    }
    case "field_changed": {
      const f = fields?.find((x) => x.key === d.key);
      const name = f?.name ?? (typeof d.name === "string" ? d.name : typeof d.key === "string" ? d.key : "");
      const to = fieldText(f, d.to, (id) => members?.find((m) => m.user.id === id)?.user.name) || null;
      return { icon: <PencilLine />, text: to ? line("activity.fieldSet", { field: name, value: to }) : line("activity.fieldCleared", { field: name }) };
    }
    case "sla_breached":
      return { icon: <TimerOff />, hue: "red", text: line(d.kind === "first_response" ? "activity.slaFirst" : "activity.slaResolution") };
    case "subject_changed":
      return { icon: <ArrowRightLeft />, text: line("activity.subject") };
    case "snoozed":
      return { icon: <AlarmClock />, text: typeof d.until === "string" ? line("activity.snoozedUntil", { when: shortDate(d.until) }) : line("activity.snoozed") };
    case "unsnoozed":
      return { icon: <AlarmClock />, text: line("activity.unsnoozed") };
    case "marked_spam":
      return { icon: <ShieldAlert />, text: line("activity.spam") };
    case "unmarked_spam":
      return { icon: <ShieldAlert />, text: line("activity.unspam") };
    case "custom_updated":
      return { icon: <ArrowRightLeft />, text: Array.isArray(d.keys) ? line("activity.customKeys", { keys: (d.keys as string[]).join(", ") }) : line("activity.custom") };
    case "merged": {
      const into = typeof d.into_number === "number" ? { number: d.into_number } : typeof d.into === "object" ? (d.into as { number?: number }) : undefined;
      const from = typeof d.from_number === "number" ? { number: d.from_number } : (d.from as { number?: number } | undefined);
      if (into?.number) return { icon: <GitMerge />, text: line("activity.mergedInto", { number: into.number }) };
      if (from?.number) return { icon: <GitMerge />, text: line("activity.mergedFrom", { number: from.number }) };
      // The target is in a private team the viewer may not see: it is not named.
      return { icon: <GitMerge />, text: line("activity.mergedIntoHidden") };
    }
    case "linked":
    case "unlinked": {
      const href = safeLink(d.url);
      const title = (typeof d.title === "string" && d.title) || (href ? new URL(href).host : "");
      return {
        icon: <Link2 />,
        text: (
          <span className="inline-flex min-w-0 items-center gap-1.5">
            <span className="shrink-0">{line(a.type === "linked" ? "activity.linked" : "activity.unlinked")}</span>
            {href && a.type === "linked" ? (
              <a href={href} target="_blank" rel="noopener noreferrer" className="min-w-0 truncate font-medium text-fg-2 underline decoration-line-strong underline-offset-2 hover:text-fg">
                {title}
              </a>
            ) : (
              <span className="min-w-0 truncate font-medium text-fg-3">{title}</span>
            )}
          </span>
        ),
      };
    }
    case "csat_received":
      return {
        icon: <Star className={tone.amber.text} />,
        text: (
          <>
            {line("activity.csat", { score: String(d.score) })}
            {typeof d.comment === "string" && d.comment ? <span className="text-fg-3"> · “{d.comment}”</span> : null}
          </>
        ),
      };
    case "ai_triaged":
      return {
        icon: <Sparkles />,
        hue: "ai",
        text: typeof d.category === "string" ? line("activity.triagedAs", { category: d.category.replace(/_/g, " ") }) : line("activity.triaged"),
      };
    case "ai_answered":
      return { icon: <Sparkles />, hue: "ai", text: line(d.channel === "email" ? "activity.aiAnsweredEmail" : "activity.aiAnswered") };
    case "ai_suggested_reply":
      return { icon: <Sparkles />, hue: "ai", text: line("activity.aiSuggested") };
    case "ai_suggested_reply_sent":
      return { icon: <Sparkles />, text: line("activity.aiSuggestedSent", { ollie }) };
    case "thread_started":
      return {
        icon: <MessagesSquare />,
        text: line("activity.threadStarted", { subject: String(d.subject ?? ""), to: Array.isArray(d.to) ? d.to.join(", ") : "" }),
      };
    case "thread_closed":
      return { icon: <MessagesSquare />, text: line("activity.threadClosed", { subject: String(d.subject ?? "") }) };
    case "thread_reopened":
      return { icon: <MessagesSquare />, text: line("activity.threadReopened", { subject: String(d.subject ?? "") }) };
    case "ai_handoff":
      if (a.actor.type === "contact") return { icon: <Hand />, text: line("activity.handoffCustomer") };
      // The bot that handed over, by the name it had then.
      return { icon: <Hand />, text: line("activity.handoffAi", typeof d.bot === "string" && d.bot ? { who: d.bot } : {}) };
    default:
      return { icon: <ArrowRightLeft />, text: line("activity.other", { type: a.type }) };
  }
}

function ActivityLine({ a }: { a: Activity }) {
  const { t } = useTranslation("inbox");
  useTranslation("indicators");
  const { data: teams } = useQuery(q.teams());
  const { data: members } = useQuery(q.members());
  const { data: tags } = useQuery(q.tags());
  const { data: mailboxes } = useQuery(q.mailboxes());
  const { data: fields } = useTicketFields();
  const display = useDisplay();
  const ollie = useAssistantName();
  const line = describe(a, t, { teams, members, tags, fields, mailboxes, display, ollie });
  // A handoff shows the face of the bot that handed over.
  const botId = a.type === "ai_handoff" && typeof a.data.bot_id === "string" ? a.data.bot_id : null;
  const { data: bots } = useQuery({ ...q.bots(), enabled: !!botId });
  const bot = botId ? bots?.find((b) => b.id === botId) : undefined;
  // Anything the AI did carries its violet mark as the actor.
  const ai = isAIActor(a.actor) || line.hue === "ai";
  const { text, hue } = line;
  const icon = bot ? <BotFace look={bot.look} image={bot.image_url} size={20} /> : ai ? <OllieAvatar size={20} className="rounded-sm" /> : line.icon;
  return (
    <div className="flex items-center gap-2.5 py-1 pl-[18px] text-[12px] text-fg-3">
      <span
        className={cn(
          "flex size-5 shrink-0 items-center justify-center rounded-sm [&_svg]:size-3",
          ai ? "" : hue === "red" ? tone.red.chip : hue === "amber" ? tone.amber.chip : hue === "ai" ? tone[AI_TONE].chip : "bg-sunken text-fg-3",
        )}
      >
        {icon}
      </span>
      <span className="min-w-0 truncate">{text}</span>
      {ai && <DecisionMeta a={a} ollie={ollie} />}
      <span className="flex-1" />
      <Tooltip content={fullDate(a.created_at)}>
        <time dateTime={a.created_at} className="mono shrink-0 text-[10.5px] text-fg-4">
          {relShort(a.created_at)}
        </time>
      </Tooltip>
    </div>
  );
}

/** How sure Ollie was about an AI change ({confidence} in the activity data). */
function DecisionMeta({ a, ollie }: { a: Activity; ollie: string }) {
  const { t } = useTranslation("inbox");
  const sure = typeof a.data.confidence === "number" ? pct(a.data.confidence) : null;
  if (sure == null) return null;
  const note = `${t("ollie.setThis", { name: ollie })} · ${t("ollie.sure", { pct: sure })}`;
  return (
    <Tooltip content={note}>
      <span className={cn("mono shrink-0 rounded-sm px-1 text-[10px] tabular-nums", tone[AI_TONE].text, tone[AI_TONE].soft)}>{sure}%</span>
    </Tooltip>
  );
}

function TypingDots() {
  return (
    <span className="inline-flex items-center gap-[3px]" aria-hidden>
      {[0, 1, 2].map((i) => (
        <span key={i} className="size-1 animate-bounce rounded-full bg-fg-3" style={{ animationDelay: `${i * 120}ms`, animationDuration: "900ms" }} />
      ))}
    </span>
  );
}

export function TypingIndicator({ ticketId, contactName }: { ticketId: string; contactName?: string }) {
  const { t } = useTranslation("inbox");
  const typing = useRealtime((s) => s.typing[ticketId]);
  const customerAt = useRealtime((s) => s.customerTyping[ticketId]);
  const agents = (typing ?? []).filter((t) => Date.now() - t.at < 5000);
  const customer = isCustomerTyping(customerAt);
  if (!agents.length && !customer) return null;
  const who = customer ? (contactName ?? t("activity.customer")) : agents.map((a) => a.name.split(" ")[0]).join(", ");
  return (
    <div aria-live="polite" className="flex items-center gap-2.5 py-1 pl-[18px] text-[12px] text-fg-3 animate-rise-in motion-reduce:animate-none">
      <span className="flex size-5 items-center justify-center rounded-sm bg-sunken">
        <TypingDots />
      </span>
      {t("typing", { who, count: customer ? 1 : agents.length })}
    </div>
  );
}

export function Timeline({
  ticketId,
  messages,
  activities,
  loading,
  onRetry,
  onToReply,
  onRetract,
  contactName,
  receivedAt,
  threads,
  threadActions,
}: {
  ticketId: string;
  messages: Message[] | undefined;
  activities: Activity[] | undefined;
  loading: boolean;
  onRetry: (m: Message) => void;
  onToReply?: (m: Message) => void;
  /** Set on a chat ticket: an agent's sent reply can be taken back from the customer. */
  onRetract?: (m: Message) => void;
  contactName?: string;
  /** The address the customer first wrote to, for inbound messages that don't say. */
  receivedAt?: string | null;
  /** The ticket's third-party threads (contracts/api.md "Threads"). */
  threads?: Thread[];
  /** Offered on each thread's latest message; absent for viewers. */
  threadActions?: ThreadActions;
}) {
  const { t } = useTranslation("indicators");
  // Messages to the customer's side and the team's (Settings → Profile → Appearance); activity stays full width.
  const conversation = useConversationLayout();
  const threadById = useMemo(() => new Map((threads ?? []).map((th) => [th.id, th])), [threads]);
  // Each thread's latest message carries its actions; its first says what went along.
  const [firstInThread, lastInThread] = useMemo(() => {
    const first = new Map<string, string>();
    const last = new Map<string, string>();
    for (const m of messages ?? []) {
      if (!m.thread_id || m.id.startsWith("tmp-")) continue;
      if (!first.has(m.thread_id)) first.set(m.thread_id, m.id);
      last.set(m.thread_id, m.id);
    }
    return [new Set(first.values()), new Set(last.values())];
  }, [messages]);
  const items = useMemo<Item[]>(() => {
    const out: Item[] = [
      ...(messages ?? []).map((m) => ({ kind: "message" as const, at: m.created_at, m })),
      ...(activities ?? []).map((a) => ({ kind: "activity" as const, at: a.created_at, a })),
    ];
    return out.sort((x, y) => Date.parse(x.at) - Date.parse(y.at));
  }, [messages, activities]);

  const scroller = useRef<HTMLDivElement>(null);
  const atBottom = useRef(true);
  const reduced = useReducedMotion();
  const pointed = usePointedAt();
  useEffect(() => {
    if (!pointed.id) return;
    scroller.current?.querySelector(`[id="m-${pointed.id}"]`)?.scrollIntoView({ block: "center", behavior: reduced ? "auto" : "smooth" });
    const id = setTimeout(() => usePointedAt.setState({ id: null }), 1400);
    return () => clearTimeout(id);
  }, [pointed, reduced]);
  const count = items.length;
  // Items present when the conversation first showed stay still; later ones rise in.
  const seen = useRef<Set<string> | null>(null);
  const sentBodies = useRef(new Set<string>());
  const opening = useRef(false);
  if (!seen.current && !loading) {
    seen.current = new Set(items.map((it) => (it.kind === "message" ? it.m.id : it.a.id)));
    opening.current = true;
  }
  useEffect(() => {
    if (!opening.current) return;
    const id = setTimeout(() => (opening.current = false), 600);
    return () => clearTimeout(id);
  });

  // Open at the latest message; follow new ones only if already at the bottom.
  const liveLen = useRealtime((s) => {
    const l = s.live[ticketId];
    return l ? l.text.length + l.tools.length : 0;
  });
  useLayoutEffect(() => {
    const el = scroller.current;
    if (el && atBottom.current) el.scrollTop = el.scrollHeight;
  }, [count, loading, liveLen]);

  useEffect(() => {
    const el = scroller.current;
    if (!el) return;
    const on = () => (atBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80);
    el.addEventListener("scroll", on, { passive: true });
    return () => el.removeEventListener("scroll", on);
  }, []);

  let lastDay = "";
  return (
    <div ref={scroller} className="min-h-0 flex-1 overflow-y-auto" aria-label={t("tickets.conversation")} role="log">
      <div className={cn("mx-auto flex w-full max-w-[820px] flex-col gap-2 px-5 py-5", conversation && "@container")}>
        {loading ? (
          <div className="flex flex-col gap-3">
            {[0, 1].map((i) => (
              <div key={i} className="rounded-lg border border-line p-4">
                <div className="flex items-center gap-2.5">
                  <Skeleton className="size-6 rounded-sm" />
                  <Skeleton className="h-2.5 w-32" />
                </div>
                <div className="mt-4 flex flex-col gap-2 pl-[34px]">
                  <Skeleton className="h-2.5 w-[85%]" />
                  <Skeleton className="h-2.5 w-[70%]" />
                  <Skeleton className="h-2.5 w-[40%]" />
                </div>
              </div>
            ))}
          </div>
        ) : (
          items.map((it, idx) => {
            const id = it.kind === "message" ? it.m.id : it.a.id;
            // A sent message swaps its temporary id for the real one; it has already risen.
            const swapped = it.kind === "message" && !id.startsWith("tmp-") && sentBodies.current.has(it.m.body_html);
            const fresh = !!seen.current && !seen.current.has(id) && !swapped;
            if (it.kind === "message" && id.startsWith("tmp-")) sentBodies.current.add(it.m.body_html);
            // On open the latest few rise in one after another; older ones sit still.
            const intro = opening.current ? items.length - 1 - idx : 99;
            const day = dayLabel(it.at);
            const sep = day !== lastDay;
            lastDay = day;
            return (
              <Fragment key={id}>
                {sep && (
                  <div className="flex items-center gap-3 py-2" role="separator">
                    <span className="h-px flex-1 bg-line-soft" />
                    <span className="label-caps">{day}</span>
                    <span className="h-px flex-1 bg-line-soft" />
                  </div>
                )}
                <div
                  className={cn(
                    (fresh || intro < 6) && "animate-rise-in motion-reduce:animate-none [animation-fill-mode:both]",
                    it.kind === "message" && sideClass(messageSide(it.m), conversation),
                  )}
                  style={intro < 6 && !fresh ? { animationDelay: `${Math.max(0, Math.min(5, items.length - 1) - intro) * 35}ms` } : undefined}
                >
                  {it.kind === "message" ? (
                    <MessageItem
                      m={it.m}
                      onRetry={onRetry}
                      onToReply={onToReply}
                      onRetract={onRetract}
                      highlight={pointed.id === it.m.id}
                      receivedAt={receivedAt}
                      thread={it.m.thread_id ? threadById.get(it.m.thread_id) : undefined}
                      threadActions={lastInThread.has(it.m.id) ? threadActions : undefined}
                      threadFirst={firstInThread.has(it.m.id)}
                    />
                  ) : (
                    <ActivityLine a={it.a} />
                  )}
                </div>
              </Fragment>
            );
          })
        )}
        {!loading && <LiveAgentReply ticketId={ticketId} messages={messages} className={sideClass("team", conversation)} />}
        <TypingIndicator ticketId={ticketId} contactName={contactName} />
      </div>
    </div>
  );
}
