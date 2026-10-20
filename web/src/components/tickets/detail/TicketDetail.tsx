import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronDown, ChevronUp, CircleCheck, CircleDot, Signal, Copy, GitMerge, MoreHorizontal, PanelRight, RotateCcw, ShieldAlert, ShieldCheck, Trash2, UserRound, Users, X, MessageSquare, MessagesSquare, StickyNote } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { tone } from "@/lib/tones";
import { Badge } from "@/components/ui/badge";
import { q, qk, upsertInList } from "@/lib/queries";
import { api } from "@/lib/api/endpoints";
import { isApiError } from "@/lib/api/client";
import { realtime } from "@/lib/realtime/manager";
import { useRealtime } from "@/lib/realtime/store";
import { useHotkeys } from "@/lib/hotkeys";
import { useAI } from "@/lib/ai";
import { useTranslation } from "react-i18next";
import { AiIcon } from "@/components/ai/bits";
import { atLeast, useRole, useSession } from "@/lib/session";
import { cn } from "@/lib/cn";
import { useDocumentTitle } from "@/app/RootLayout";
import { usePageCommands } from "@/app/shellStore";
import { Button } from "@/components/ui/button";
import { Tooltip } from "@/components/ui/tooltip";
import { AvatarStack } from "@/components/ui/avatar";
import { Menu, MenuContent, MenuItem, MenuSeparator, MenuTrigger } from "@/components/ui/menu";
import { EmptyState } from "@/components/ui/empty";
import { Skeleton } from "@/components/ui/skeleton";
import { useConfirm } from "@/components/ui/confirm";
import { toast } from "@/components/ui/toast";
import { copyText } from "@/components/ui/copy-button";
import { ChannelIcon, channelLabel, PriorityPill, SeverityChip, severityLabel, severityOrder, SlaPill, slaState, StatusPill, TicketNumber } from "../bits";
import { useOllieNote } from "../ollie";
import { aiDecisions } from "@/lib/decisions";
import type { Thread, Ticket } from "@/lib/api/types";
import { Morph } from "@/components/motion";
import { CheckBurst } from "../CheckBurst";
import { intent } from "../motion";
import { focusField, missingForSolve, useDisplay, useTicketFields } from "../fields";
import { usePageCrumb } from "@/app/useBreadcrumbs";
import { useMarkTicketNotificationsRead } from "@/app/notificationRead";
import { useTicketViewers } from "../presence";
import { useDeleteTicket, useUpdateTicket } from "../useTicketActions";
import { Timeline } from "./Timeline";
import { Composer, type ComposerHandle } from "./Composer";
import { TicketProperties, type PropPicker } from "./TicketProperties";
import { MergeDialog } from "./MergeDialog";
import { TicketTeamChips, useTeamsCtx } from "../teams";
import { SnoozeMenu } from "./SnoozeMenu";

const PANEL_KEY = "od-ticket-panel";
const isUuid = (s: string) => /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(s);

function readPanel() {
  try {
    return localStorage.getItem(PANEL_KEY) !== "0";
  } catch {
    return true;
  }
}

function SubjectEditor({ value, onSave, disabled }: { value: string; onSave: (v: string) => void; disabled?: boolean }) {
  const { t } = useTranslation("inbox");
  const [editing, setEditing] = useState(false);
  const [text, setText] = useState(value);
  useEffect(() => setText(value), [value]);
  if (!editing || disabled)
    return (
      <h1
        className={cn("min-w-0 truncate text-[14px] font-semibold tracking-[-0.01em]", !disabled && "-mx-1 cursor-text rounded-sm px-1 transition-colors hover:bg-hover")}
        onClick={() => !disabled && setEditing(true)}
        title={disabled ? value : t("detail.editSubject")}
        data-ph-mask
      >
        {value || t("row.noSubject")}
      </h1>
    );
  const commit = () => {
    setEditing(false);
    if (text.trim() && text.trim() !== value) onSave(text.trim());
    else setText(value);
  };
  return (
    <input
      autoFocus
      value={text}
      onChange={(e) => setText(e.target.value)}
      onBlur={commit}
      onKeyDown={(e) => {
        if (e.key === "Enter") commit();
        if (e.key === "Escape") {
          e.stopPropagation();
          setText(value);
          setEditing(false);
        }
      }}
      aria-label={t("detail.subject")}
      className="-mx-1 h-7 min-w-0 flex-1 rounded-sm border border-line bg-surface px-1 text-[14px] font-semibold tracking-[-0.01em] outline-none focus:border-fg-4 focus:shadow-[0_0_0_3px_var(--focus)]"
    />
  );
}

export default function TicketDetail({
  ticketId,
  onClose,
  onPrev,
  onNext,
  position,
}: {
  ticketId: string;
  onClose: () => void;
  onPrev?: () => void;
  onNext?: () => void;
  position?: { index: number; total: number };
}) {
  // The URL may carry the uuid or the ticket number; messages need the uuid.
  const ticket = useQuery(q.ticket(ticketId));
  const realId = ticket.data?.id ?? (isUuid(ticketId) ? ticketId : "");
  const messages = useQuery({ ...q.messages(realId), enabled: !!realId });
  const activities = useQuery({ ...q.activities(realId), enabled: !!realId });
  const threads = useQuery({ ...q.threads(realId), enabled: !!realId });
  const role = useRole();
  const canEdit = atLeast(role, "agent");
  const qc = useQueryClient();
  const threadActions = useMemo(
    () =>
      canEdit
        ? {
            reply: (th: Thread) => composer.current?.inThread(th),
            setStatus: (th: Thread, status: Thread["status"]) => {
              upsertInList(qc, qk.threads(th.ticket_id), { ...th, status });
              api.tickets.updateThread(th.ticket_id, th.id, { status }).then(
                (next) => upsertInList(qc, qk.threads(th.ticket_id), next),
                (e) => {
                  upsertInList(qc, qk.threads(th.ticket_id), th);
                  toast.error(e);
                },
              );
            },
          }
        : undefined,
    [canEdit, qc],
  );
  const me = useSession((s) => s.user!.id);
  const viewers = useTicketViewers(realId || undefined);
  const update = useUpdateTicket();
  const del = useDeleteTicket();
  const confirm = useConfirm();
  const composer = useRef<ComposerHandle>(null);
  const aiOn = useAI().enabled;
  const { t: tAi } = useTranslation("aiTicket");
  const { t: tr } = useTranslation("inbox");
  const [picker, setPicker] = useState<PropPicker>(null);
  const [snoozeOpen, setSnoozeOpen] = useState(false);
  const [burst, setBurst] = useState(0);
  const [mergeOpen, setMergeOpen] = useState(false);
  const [panel, setPanel] = useState(readPanel);
  const display = useDisplay();
  const { data: ticketFields } = useTicketFields();
  const reduced = useReducedMotion();
  const t = ticket.data;
  const teamsCtx = useTeamsCtx();
  useDocumentTitle(t ? `#${t.number} ${t.subject}` : null);
  usePageCrumb(t ? `#${t.number}` : null);

  // Opened with r from the list: start the reply right away.
  useEffect(() => {
    if (t && canEdit && intent.take(t.id, t.number)) composer.current?.focus("reply");
  }, [t?.id]); // eslint-disable-line react-hooks/exhaustive-deps

  // Seeing the ticket reads its notifications, so they are never emailed.
  useMarkTicketNotificationsRead(realId);

  useEffect(() => {
    if (!realId) return;
    realtime()?.setActiveTicket(realId);
    return () => realtime()?.setActiveTicket(null);
  }, [realId]);

  // Moved to a private team this person is not on: it is gone from their view.
  const hiddenAt = useRealtime((s) => (realId ? s.hidden[realId] : undefined));
  useEffect(() => {
    if (!hiddenAt) return;
    toast.info(tr("detail.movedPrivate"));
    onClose();
  }, [hiddenAt]); // eslint-disable-line react-hooks/exhaustive-deps

  const togglePanel = () => {
    setPanel((p) => {
      try {
        localStorage.setItem(PANEL_KEY, p ? "0" : "1");
      } catch {
        /* storage unavailable */
      }
      return !p;
    });
  };

  const solved = t?.status === "solved" || t?.status === "closed";
  const solve = () => {
    if (!t || !canEdit) return;
    // Required fields: point at the first empty one instead of a refused solve.
    const missing = solved ? [] : missingForSolve(ticketFields, t);
    if (missing.length) {
      if (!panel) togglePanel();
      focusField(missing[0].key);
      toast.error(tr("fields.fill", { name: missing.map((f) => f.name).join(", ") }));
      return;
    }
    update.mutate({ id: t.id, patch: { status: solved ? "open" : "solved" } });
    toast.success(
      solved ? tr("toast.reopened", { number: t.number }) : tr("toast.solved", { number: t.number }),
      solved ? undefined : { action: { label: tr("toast.undo"), onClick: () => update.mutate({ id: t.id, patch: { status: t.status } }) } },
    );
    if (solved) return;
    // A short check burst on the button, then on to the next ticket.
    setBurst((n) => n + 1);
    if (onNext) setTimeout(onNext, reduced ? 0 : 520);
  };
  const spam = () => {
    if (!t || !canEdit) return;
    const was = t.spam;
    update.mutate({ id: t.id, patch: { spam: !was } });
    toast.success(was ? tr("toast.unspammed") : tr("toast.spammed"), {
      action: { label: tr("toast.undo"), onClick: () => update.mutate({ id: t.id, patch: { spam: was } }) },
    });
    if (!t.spam) onClose();
  };
  const remove = () =>
    t &&
    confirm.show(
      tr("detail.deleteBody"),
      async () => {
        await del.mutateAsync(t.id);
        toast.success(tr("toast.deleted", { number: t.number }));
        onClose();
      },
      { title: tr("detail.deleteTitle", { number: t.number }), confirmLabel: tr("detail.delete"), danger: true },
    );
  const assignMe = () => t && update.mutate({ id: t.id, patch: { assignee_id: me } });
  // Property pickers live in the details panel; show it first when hidden.
  const openPicker = (p: Exclude<PropPicker, null>) => {
    if (!panel) togglePanel();
    setTimeout(() => setPicker(p), panel ? 0 : 60);
  };

  useHotkeys(
    {
      r: () => composer.current?.focus("reply"),
      n: () => composer.current?.focus("note"),
      a: () => openPicker("assignee"),
      s: (e: KeyboardEvent) => (e.shiftKey ? spam() : openPicker("status")),
      h: () => setSnoozeOpen(true),
      p: () => openPicker("priority"),
      t: () => teamsCtx.hasTeams && openPicker("team"),
      e: solve,
      i: () => assignMe(),
      "]": togglePanel,
      // mod+J drafts a reply from inside the composer (Composer.tsx); elsewhere it opens Ollie.
    },
    !!t && canEdit,
  );

  // Offer the ticket's actions in the command menu while it's open.
  useEffect(() => {
    if (!t || !canEdit) return;
    usePageCommands.setState({
      commands: [
        { id: "reply", label: tr("commands.reply"), shortcut: "r", icon: <MessageSquare />, run: () => composer.current?.focus("reply") },
        { id: "note", label: tr("commands.note"), shortcut: "n", icon: <StickyNote />, run: () => composer.current?.focus("note") },
        { id: "side", label: tr("commands.side"), keywords: ["thread", "third party", "partner"], icon: <MessagesSquare />, run: () => composer.current?.focus("thread") },
        ...(aiOn ? [{ id: "ai-draft", label: tAi("composer.command"), icon: <AiIcon />, run: () => composer.current?.aiDraft() }] : []),
        { id: "assign-me", label: tr("commands.assignMe"), shortcut: "i", icon: <UserRound />, run: assignMe },
        { id: "assign", label: tr("commands.assign"), shortcut: "a", icon: <UserRound />, run: () => openPicker("assignee") },
        { id: "status", label: tr("commands.status"), shortcut: "s", icon: <CircleDot />, run: () => openPicker("status") },
        { id: "priority", label: tr("commands.priority"), shortcut: "p", icon: <Signal />, run: () => openPicker("priority") },
        ...(teamsCtx.hasTeams ? [{ id: "team", label: tr("commands.team"), shortcut: "t", keywords: ["team", "move", "transfer"], icon: <Users />, run: () => openPicker("team") }] : []),
        { id: "severity", label: tr("commands.severity"), icon: <SeverityChip severity={t.severity ?? "sev3"} />, run: () => openPicker("severity") },
        ...severityOrder
          .filter((s) => s !== t.severity)
          .map((s) => ({
            id: `severity-${s}`,
            label: tr("commands.setSeverity", { severity: severityLabel[s] }),
            keywords: ["severity", "sev", s, `s${s.slice(3)}`],
            icon: <SeverityChip severity={s} />,
            run: () => update.mutate({ id: t.id, patch: { severity: s } }),
          })),
        ...(t.severity ? [{ id: "severity-clear", label: tr("commands.clearSeverity"), keywords: ["severity"], icon: <SeverityChip severity={null} />, run: () => update.mutate({ id: t.id, patch: { severity: null } }) }] : []),
        { id: "solve", label: solved ? tr("commands.reopen") : tr("commands.solve"), shortcut: "e", icon: <CircleCheck />, run: solve },
        { id: "merge", label: tr("detail.merge"), icon: <GitMerge />, run: () => setMergeOpen(true) },
        { id: "spam", label: t.spam ? tr("detail.notSpam") : tr("detail.markSpam"), icon: <ShieldAlert />, run: spam },
      ],
    });
    return () => usePageCommands.setState({ commands: [] });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [t?.id, t?.status, t?.spam, t?.severity, canEdit, aiOn, teamsCtx.hasTeams]);

  if (ticket.isError) {
    return (
      <div className="flex h-full flex-col">
        <EmptyState
          lit={3}
          title={isApiError(ticket.error, "not_found") ? tr("detail.notFound") : tr("detail.loadError")}
          description={isApiError(ticket.error, "not_found") ? tr("detail.notFoundHint") : tr("error.description")}
          action={
            <>
              {!isApiError(ticket.error, "not_found") && <Button onClick={() => void ticket.refetch()}>{tr("error.retry")}</Button>}
              <Button variant="ghost" onClick={onClose}>
                {tr("detail.back")}
              </Button>
            </>
          }
        />
      </div>
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex h-12 shrink-0 items-center gap-2 border-b border-line px-3">
        <Tooltip content={tr("detail.close")} shortcut="esc">
          <Button variant="ghost" size="sm" square aria-label="Close ticket" icon={<X className="size-4" />} onClick={onClose} />
        </Tooltip>
        <div className="hidden items-center sm:flex">
          <Tooltip content={tr("detail.previous")} shortcut="k">
            <Button variant="ghost" size="sm" square aria-label="Previous ticket" disabled={!onPrev} onClick={onPrev} icon={<ChevronUp className="size-4" />} />
          </Tooltip>
          <Tooltip content={tr("detail.next")} shortcut="j">
            <Button variant="ghost" size="sm" square aria-label="Next ticket" disabled={!onNext} onClick={onNext} icon={<ChevronDown className="size-4" />} />
          </Tooltip>
          {position && (
            <span className="mono ml-1 hidden text-[11px] text-fg-4 tabular-nums sm:inline">
              {position.index} / {position.total}
            </span>
          )}
        </div>
        <span className="mx-1 hidden h-5 w-px bg-line sm:block" />
        <div className="flex min-w-0 flex-1 items-center gap-2">
          {t ? (
            <>
              {t.channel !== "email" && (
                <Tooltip content={channelLabel[t.channel]}>
                  <span className="hidden shrink-0 sm:flex">
                    <ChannelIcon channel={t.channel} plain />
                  </span>
                </Tooltip>
              )}
              <TicketNumber n={t.number} className="shrink-0" />
              <SubjectEditor value={t.subject} disabled={!canEdit} onSave={(subject) => update.mutate({ id: t.id, patch: { subject } })} />
              <span className="hidden shrink-0 items-center gap-1.5 md:flex">
                <StatusPill status={t.status} label={display.status(t.status)} hue={display.statusTone(t.status)} />
                <PriorityPill priority={t.priority} label={display.priority(t.priority)} hue={display.priorityTone(t.priority)} />
                {t.severity && <HeaderSeverity t={t} />}
                {slaState(t) && slaState(t)!.state !== "on_track" && <SlaPill ticket={t} />}
                {t.spam && <Badge tone="red">{tr("detail.spam")}</Badge>}
                <TicketTeamChips t={t} ctx={teamsCtx} className="ml-0.5" />
              </span>
            </>
          ) : (
            <Skeleton className="h-3 w-64" />
          )}
        </div>
        {viewers.length > 0 && (
          <Tooltip content={viewers.map((v) => `${v.name} ${v.state === "replying" ? "is replying" : "is here"}`).join(", ")}>
            <span className="flex items-center">
              <AvatarStack people={viewers.map((v) => ({ id: v.user_id, name: v.name, avatar: v.avatar, state: v.state }))} />
            </span>
          </Tooltip>
        )}
        {t && canEdit && (
          <div className="flex items-center gap-1">
            <Tooltip content={solved ? tr("detail.reopen") : tr("detail.solve")} shortcut="e">
              <Button
                size="sm"
                variant="secondary"
                icon={
                  <Morph k={burst ? `b${burst}` : solved ? "reopen" : "solve"}>
                    {burst && solved ? (
                      <CheckBurst size={14} />
                    ) : solved ? (
                      <RotateCcw className="size-3.5" />
                    ) : (
                      <CircleCheck className={cn("size-3.5", tone.green.text)} />
                    )}
                  </Morph>
                }
                onClick={solve}
              >
                <span className="max-sm:sr-only">{solved ? tr("detail.reopen") : tr("detail.solve")}</span>
              </Button>
            </Tooltip>
            <SnoozeMenu
              open={snoozeOpen}
              onOpenChange={setSnoozeOpen}
              onSnooze={(at) => {
                update.mutate({ id: t.id, patch: { snoozed_until: at.toISOString(), status: "on_hold" } });
                toast.success(tr("toast.snoozed"));
                if (onNext) onNext();
              }}
            />
            <Menu>
              <Tooltip content={tr("detail.more")}>
                <MenuTrigger asChild>
                  <Button variant="ghost" size="sm" square aria-label="More actions" icon={<MoreHorizontal className="size-4" />} />
                </MenuTrigger>
              </Tooltip>
              <MenuContent align="end" className="w-[210px]">
                <MenuItem icon={<UserRound />} shortcut="i" onSelect={assignMe}>
                  {tr("commands.assignMe")}
                </MenuItem>
                <MenuItem icon={<GitMerge />} onSelect={() => setMergeOpen(true)}>
                  {tr("detail.merge")}
                </MenuItem>
                <MenuItem
                  icon={<Copy />}
                  onSelect={async () => {
                    if (await copyText(`${location.origin}/inbox/open/${t.id}`)) toast.success(tr("toast.linkCopied"));
                  }}
                >
                  {tr("props.copyLink")}
                </MenuItem>
                <MenuSeparator />
                <MenuItem icon={t.spam ? <ShieldCheck /> : <ShieldAlert />} onSelect={spam}>
                  {t.spam ? tr("detail.notSpam") : tr("detail.markSpam")}
                </MenuItem>
                <MenuItem icon={<Trash2 />} danger onSelect={remove}>
                  {tr("detail.deleteTicket")}
                </MenuItem>
              </MenuContent>
            </Menu>
          </div>
        )}
        <Tooltip content={panel ? tr("detail.hideDetails") : tr("detail.showDetails")} shortcut="]">
          <Button variant="ghost" size="sm" square aria-label="Toggle details" aria-pressed={panel} onClick={togglePanel} icon={<PanelRight className="size-4" />} className={cn("hidden lg:inline-flex", panel && "text-fg")} />
        </Tooltip>
      </header>

      <div className="flex min-h-0 flex-1">
        <div className="flex min-w-0 flex-1 flex-col">
          <Timeline
            ticketId={realId}
            messages={messages.data}
            activities={activities.data}
            loading={!messages.data}
            onRetry={(m) => composer.current?.retry(m)}
            contactName={t?.contact?.name}
            receivedAt={t && t.channel !== "email" ? null : t?.received_at}
            threads={threads.data}
            threadActions={threadActions}
          />
          {t && canEdit && <Composer ref={composer} ticket={t} />}
        </div>
        <AnimatePresence initial={false}>
          {panel && (
            <motion.aside
              key="panel"
              aria-label="Ticket details"
              initial={reduced ? { opacity: 0 } : { width: 0, opacity: 0 }}
              animate={reduced ? { opacity: 1 } : { width: 300, opacity: 1 }}
              exit={reduced ? { opacity: 0 } : { width: 0, opacity: 0 }}
              transition={{ duration: 0.2, ease: [0.16, 1, 0.3, 1] }}
              className="hidden shrink-0 overflow-hidden border-l border-line lg:block"
            >
              <div className="h-full w-[300px] overflow-y-auto">
                {t ? (
                  <TicketProperties t={t} picker={picker} onPicker={setPicker} onSideConversation={() => composer.current?.focus("thread")} />
                ) : (
                  <div className="flex flex-col gap-3.5 p-4">
                    {[0, 1, 2, 3, 4].map((i) => (
                      <div key={i} className="flex items-center gap-3">
                        <Skeleton className="h-2.5 w-14" />
                        <Skeleton className="h-2.5" style={{ width: `${40 + i * 7}%` }} />
                      </div>
                    ))}
                  </div>
                )}
              </div>
            </motion.aside>
          )}
        </AnimatePresence>
      </div>
      {t && <MergeDialog ticket={t} open={mergeOpen} onOpenChange={setMergeOpen} />}
    </div>
  );
}

/** The header's severity chip; its tooltip says when Ollie set it and how sure Jev was. */
function HeaderSeverity({ t }: { t: Ticket }) {
  const note = useOllieNote();
  if (!t.severity) return null;
  const d = aiDecisions(t)?.fields.severity;
  const byAi = !!d?.applied && d.value === t.severity;
  return (
    <Tooltip content={byAi ? `${severityLabel[t.severity]} · ${note(t, "severity")}` : severityLabel[t.severity]}>
      <span className="flex">
        <SeverityChip severity={t.severity} className="h-[19px] min-w-[26px] text-[10.5px]" />
      </span>
    </Tooltip>
  );
}
