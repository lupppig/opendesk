// A conversation: its subject, folders and labels, who it is shared with and
// assigned to, the actions (each mirrored at the provider), the messages oldest
// first with the older ones folded, the team's comments, and the reply composer.
// Writing a comment and the reply buttons are pinned under it, whatever its length.
// Beside it, who it is with: the people panel (] shows and hides it).

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Archive,
  ArrowLeft,
  AtSign,
  ChevronDown,
  ChevronUp,
  Folder,
  Forward,
  Inbox,
  Link2,
  Mail,
  MailOpen,
  MessageSquare,
  MoreHorizontal,
  PanelRight,
  PencilLine,
  RefreshCw,
  Reply,
  ReplyAll,
  Settings,
  ShieldAlert,
  ShieldCheck,
  Star,
  Tag,
  Ticket,
  Trash2,
  UserPlus,
  Users,
} from "lucide-react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { forwardRef, useEffect, useImperativeHandle, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link, useNavigate } from "@tanstack/react-router";
import { api } from "@/lib/api/endpoints";
import type { MailFolder, MailMessage, MailThreadAction, MailThreadDetail } from "@/lib/api/types";
import { isApiError } from "@/lib/api/client";
import { patchThreads } from "@/lib/collab/cache";
import { compose } from "@/lib/collab/compose";
import { useMailAccounts } from "@/lib/collab/hooks";
import { hasReplyAll, replyTarget, type ComposeMode } from "@/lib/collab/recipients";
import { identitySignature, ownEmails, replyIdentity } from "@/lib/collab/identities";
import { cn } from "@/lib/cn";
import { springs } from "@/lib/motion";
import { qk, q } from "@/lib/queries";
import { usePageCrumb } from "@/app/useBreadcrumbs";
import { useDocumentTitle } from "@/app/RootLayout";
import { AvatarStack } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty";
import { Menu, MenuContent, MenuItem, MenuSeparator, MenuTrigger } from "@/components/ui/menu";
import { Skeleton } from "@/components/ui/skeleton";
import { toast } from "@/components/ui/toast";
import { Tooltip } from "@/components/ui/tooltip";
import { AccountDot, LabelPill } from "./bits";
import { CommentBar, Comments, Count, type CommentBarHandle } from "./Comments";
import { draftFromMessage, MailComposer, startDraft, useForwardBlock } from "./MailComposer";
import { MailMessageItem } from "./MailMessageItem";
import { PersonPanel } from "./PersonPanel";
import { AssignPicker, LabelsPicker, MovePicker, type MoveTarget } from "./pickers";
import { ShareDialog } from "./ShareDialog";

export type ThreadViewHandle = { reply: (mode: ComposeMode) => void; comment: () => void; move: () => void; labels: () => void; people: () => void };

export type ThreadViewProps = {
  threadId: string;
  onClose: () => void;
  onPrev?: () => void;
  onNext?: () => void;
  position?: { index: number; total: number };
  /** Applies an action from the page, so its toast and Undo know the list. */
  onAction: (action: MailThreadAction, opts?: { folderId?: string; label?: string; silent?: boolean }) => void;
  markReadOnOpen: boolean;
  /** Open the composer in this mode once loaded. */
  intent?: ComposeMode | null;
  /** Opened from the Drafts box: its newest draft opens in the composer. */
  openDraft?: boolean;
  /** Opens another conversation (from the people panel). */
  onOpenThread?: (id: string) => void;
  /** Lists every conversation with someone (from the people panel). */
  onShowPerson?: (email: string) => void;
};

const PANEL_KEY = "od-mail-people";
/** Wide enough for the people panel beside the list and the conversation. */
const WIDE = "(min-width: 1280px)";

function readPanel() {
  try {
    return localStorage.getItem(PANEL_KEY) !== "0";
  } catch {
    return true;
  }
}

function useWide() {
  return useSyncExternalStore(
    (cb) => {
      const m = window.matchMedia(WIDE);
      m.addEventListener("change", cb);
      return () => m.removeEventListener("change", cb);
    },
    () => window.matchMedia(WIDE).matches,
    () => true,
  );
}

/** A draft in the conversation: who it is to and how it starts; opening it continues it in the composer. */
function DraftRow({ m, onOpen }: { m: MailMessage; onOpen: () => void }) {
  const { t } = useTranslation("collab");
  const to = [...m.to, ...m.cc].map((a) => a.name || a.email).join(", ");
  return (
    <button
      type="button"
      onClick={onOpen}
      className="group flex w-full items-center gap-3 rounded-lg border border-dashed border-line-strong bg-surface px-3.5 py-2.5 text-left outline-none transition-colors hover:bg-sunken focus-visible:shadow-[0_0_0_2px_var(--focus)]"
    >
      <span className="inline-flex h-[18px] shrink-0 items-center rounded-sm bg-tone-red-soft px-1.5 text-[10.5px] font-medium text-tone-red">{t("composer.draft")}</span>
      <span className="min-w-0 flex-1">
        <span className="block truncate text-[12.5px] text-fg-2" data-ph-mask>
          {to ? t("thread.draftTo", { who: to }) : t("thread.draftNoRecipients")}
        </span>
        <span className="block truncate text-xs text-fg-4" data-ph-mask>
          {m.snippet || m.subject}
        </span>
      </span>
      <span className="flex shrink-0 items-center gap-1 text-[11.5px] text-fg-3 group-hover:text-fg">
        <PencilLine className="size-3.5" />
        {t("composer.editDraft")}
      </span>
    </button>
  );
}

function HeaderButton({ label, shortcut, onClick, children, pressed, active, disabled }: { label: string; shortcut?: string; onClick?: () => void; children: ReactNode; pressed?: boolean; active?: boolean; disabled?: boolean }) {
  return (
    <Tooltip content={label} shortcut={shortcut}>
      <button
        type="button"
        aria-label={label}
        aria-pressed={pressed ?? active}
        disabled={disabled}
        onClick={onClick}
        className={cn(
          "flex size-7 items-center justify-center rounded-md text-fg-3 outline-none transition-colors hover:bg-hover hover:text-fg focus-visible:shadow-[0_0_0_2px_var(--focus)] disabled:pointer-events-none disabled:opacity-40 data-[state=open]:bg-hover data-[state=open]:text-fg [&_svg]:size-[15px]",
          pressed && "text-tone-amber hover:text-tone-amber",
          active && "bg-hover text-fg",
        )}
      >
        {children}
      </button>
    </Tooltip>
  );
}

function ThreadSkeleton() {
  return (
    <div className="flex h-full flex-col">
      <div className="flex h-12 items-center gap-3 border-b border-line px-4">
        <Skeleton className="h-3 w-10" />
        <Skeleton className="h-3 w-64" />
      </div>
      <div className="mx-auto flex w-full max-w-[860px] flex-col gap-3 px-5 py-5">
        <Skeleton className="h-5 w-1/2" />
        <Skeleton className="h-24 rounded-lg" />
        <Skeleton className="h-16 rounded-lg opacity-60" />
      </div>
    </div>
  );
}

/** Folder and label names a conversation is in, skipping what says nothing (All mail, Important). */
function folderPills(detail: MailThreadDetail, folders: MailFolder[] | undefined, box: (b: string) => string) {
  if (!folders) return [];
  const skip = new Set(["all", "important", "starred", "sent", "drafts"]);
  return detail.thread.folder_ids
    .map((id) => folders.find((f) => f.id === id))
    .filter((f): f is MailFolder => !!f && !skip.has(f.role ?? ""))
    .map((f) => ({ id: f.id, name: f.role ? box(f.role) : f.name, color: f.color }));
}

export const ThreadView = forwardRef<ThreadViewHandle, ThreadViewProps>(function ThreadView({ threadId, onClose, onPrev, onNext, position, onAction, markReadOnOpen, intent, openDraft, onOpenThread, onShowPerson }, ref) {
  const { t } = useTranslation("collab");
  const qc = useQueryClient();
  const navigate = useNavigate();
  const { data: detail, isPending, error } = useQuery(q.mailThread(threadId));
  const { data: accounts } = useMailAccounts();
  const accountId = detail?.account.id;
  const { data: folders } = useQuery({ ...q.mailFolders(accountId ?? ""), enabled: !!accountId });
  const { data: teams } = useQuery(q.teams());
  const { data: members } = useQuery(q.members());
  const [composer, setComposer] = useState<ComposeMode | null>(null);
  const [composerKey, setComposerKey] = useState(0);
  // The stored draft the composer continues, and the draft it saves to (hidden from the conversation while open).
  const [editing, setEditing] = useState<MailMessage | null>(null);
  const [composerDraftId, setComposerDraftId] = useState<string | null>(null);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  // The newest message is open unless folded by hand.
  const [foldedLast, setFoldedLast] = useState<string | null>(null);
  const [showOlder, setShowOlder] = useState(false);
  const [share, setShare] = useState(false);
  const [moveOpen, setMoveOpen] = useState(false);
  const [labelOpen, setLabelOpen] = useState(false);
  const [assignOpen, setAssignOpen] = useState(false);
  const composerRef = useRef<HTMLDivElement>(null);
  const commentsRef = useRef<HTMLDivElement>(null);
  const commentBar = useRef<CommentBarHandle>(null);
  // A comment just posted comes into view once it is in the list.
  const posted = useRef<string | null>(null);
  const reduce = useReducedMotion();
  const behavior: ScrollBehavior = reduce ? "auto" : "smooth";
  // The bar lifts off the conversation while there is more of it underneath.
  const scroller = useRef<HTMLDivElement>(null);
  const [below, setBelow] = useState(false);
  const [flash, setFlash] = useState(false);
  const marked = useRef<string | null>(null);
  // The people panel: docked when there is room (and kept as the member left it), else a sheet.
  const wide = useWide();
  const [docked, setDocked] = useState(readPanel);
  const [sheet, setSheet] = useState(false);
  const sheetRef = useRef<HTMLDivElement>(null);
  const panelOpen = wide ? docked : sheet;
  const togglePanel = () => {
    if (!wide) return setSheet((v) => !v);
    setDocked((v) => {
      try {
        localStorage.setItem(PANEL_KEY, v ? "0" : "1");
      } catch {
        /* storage unavailable */
      }
      return !v;
    });
  };
  // The sheet closes on Escape (before the page's Escape closes the conversation) and on a click away.
  useEffect(() => {
    if (!sheet || wide) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || e.defaultPrevented) return;
      e.preventDefault();
      e.stopPropagation();
      setSheet(false);
    };
    const onDown = (e: PointerEvent) => {
      const el = e.target as Element | null;
      if (sheetRef.current?.contains(el) || el?.closest?.("[data-people-toggle]")) return;
      setSheet(false);
    };
    window.addEventListener("keydown", onKey, true);
    document.addEventListener("pointerdown", onDown);
    return () => {
      window.removeEventListener("keydown", onKey, true);
      document.removeEventListener("pointerdown", onDown);
    };
  }, [sheet, wide]);

  const subject = detail?.thread.subject || t("list.noSubject");
  useDocumentTitle(detail ? subject : null);
  usePageCrumb(detail ? subject : null);

  // Unread messages open with the conversation, and stay open once it reads as read.
  const seeded = useRef<string | null>(null);
  useEffect(() => {
    if (!detail || seeded.current === detail.thread.id) return;
    seeded.current = detail.thread.id;
    setExpanded(new Set(detail.messages.filter((m) => m.unread).map((m) => m.id)));
    setFoldedLast(null);
  }, [detail]);

  // Opening shows it read, when the member wants that (and can act).
  useEffect(() => {
    if (!detail || !detail.can_act || !markReadOnOpen || marked.current === detail.thread.id) return;
    marked.current = detail.thread.id;
    if (detail.thread.unread) onAction("read", { silent: true });
  }, [detail, markReadOnOpen, onAction]);

  const all = useMemo(() => detail?.messages ?? [], [detail]);
  const messages = useMemo(() => all.filter((m) => !m.draft), [all]);
  const drafts = useMemo(() => all.filter((m) => m.draft && m.id !== composerDraftId && m.id !== editing?.id), [all, composerDraftId, editing]);
  const target = replyTarget(all);
  // Every address of the account (its own and its aliases): never a reply-all recipient.
  const ownEmail = useMemo(() => (detail ? ownEmails(detail.account) : []), [detail]);
  // A reply goes out from the address the answered message was written to.
  const replyFrom = detail ? replyIdentity(detail.account, composer === "new" ? undefined : target) : undefined;
  const forwardBlock = useForwardBlock(composer === "forward" ? target : undefined);
  const gmail = detail?.account.provider === "google";
  // Gmail labels and Outlook categories; IMAP has folders only.
  const hasLabels = !!detail && detail.account.provider !== "imap";
  const labelIds = (folders ?? []).filter((f) => f.kind === "label" && !f.role && detail?.thread.folder_ids.includes(f.id)).map((f) => f.id);
  // Moving to a label is Gmail's; an Outlook category is not a place to move to.
  const moveFolders = gmail ? (folders ?? []) : (folders ?? []).filter((f) => f.kind !== "label");

  // Once the composer has laid out, so all of it comes into view. Not smooth: the editor
  // focusing itself meanwhile would stop a smooth scroll partway.
  const showComposer = () => requestAnimationFrame(() => requestAnimationFrame(() => composerRef.current?.scrollIntoView({ block: "nearest" })));
  const openComposer = (mode: ComposeMode) => {
    if (!detail?.can_act) return;
    setEditing(null);
    setComposerDraftId(null);
    setComposer(mode);
    setComposerKey((k) => k + 1);
    showComposer();
  };
  /** Continues a draft: under its conversation, or new mail in the floating composer. */
  const editDraft = (m: MailMessage) => {
    if (!detail?.can_act) return;
    if (!messages.length) {
      compose.open({ fromDraft: m });
      onClose();
      return;
    }
    setComposer(null);
    setEditing(m);
    setComposerDraftId(m.id);
    setComposerKey((k) => k + 1);
    showComposer();
  };
  useImperativeHandle(ref, () => ({
    reply: openComposer,
    comment: () => detail?.can_act && commentBar.current?.focus(),
    move: () => detail?.can_act && setMoveOpen(true),
    labels: () => detail?.can_act && hasLabels && setLabelOpen(true),
    people: togglePanel,
  }));

  const commentCount = detail?.comments.length ?? 0;
  useEffect(() => {
    if (!posted.current || !document.getElementById(`comment-${posted.current}`)) return;
    posted.current = null;
    commentsRef.current?.scrollIntoView({ block: "end", behavior });
  }, [commentCount, behavior]);

  useEffect(() => {
    const el = scroller.current;
    if (!el) return;
    const measure = () => setBelow(el.scrollTop + el.clientHeight < el.scrollHeight - 2);
    measure();
    el.addEventListener("scroll", measure, { passive: true });
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    if (el.firstElementChild) ro.observe(el.firstElementChild);
    return () => {
      el.removeEventListener("scroll", measure);
      ro.disconnect();
    };
  }, [isPending]);

  // Jumping to the comments marks them for a moment, so the eye lands there.
  const showComments = () => {
    commentsRef.current?.scrollIntoView({ block: "start", behavior });
    setFlash(true);
  };
  useEffect(() => {
    if (!flash) return;
    const id = setTimeout(() => setFlash(false), 900);
    return () => clearTimeout(id);
  }, [flash]);

  // Opened from the Drafts box: its newest draft continues in the composer.
  const draftOpened = useRef(false);
  useEffect(() => {
    if (!detail || !openDraft || draftOpened.current || !detail.can_act) return;
    draftOpened.current = true;
    const newest = [...detail.messages].reverse().find((m) => m.draft);
    if (newest) editDraft(newest);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [detail, openDraft]);

  // Opened to answer (r, a or f in the list): the composer opens once the conversation is here.
  const intended = useRef(false);
  useEffect(() => {
    if (!detail || !intent || intended.current) return;
    intended.current = true;
    openComposer(intent);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [detail, intent]);

  const assign = useMutation({
    mutationFn: (ids: string[]) => api.collab.threads.assign(threadId, ids),
    onMutate: (ids) => {
      const prev = qc.getQueryData<MailThreadDetail>(qk.mailThread(threadId));
      if (prev) {
        const assignees = ids.map((id) => prev.thread.assignees.find((a) => a.id === id) ?? members?.find((m) => m.user.id === id)?.user).filter((x): x is NonNullable<typeof x> => !!x);
        qc.setQueryData<MailThreadDetail>(qk.mailThread(threadId), { ...prev, thread: { ...prev.thread, assignees: assignees.map((a) => ({ id: a.id, name: a.name, email: a.email, avatar_url: a.avatar_url })) } });
      }
      return { prev };
    },
    onError: (e, _v, ctx) => {
      if (ctx?.prev) qc.setQueryData(qk.mailThread(threadId), ctx.prev);
      toast.error(e);
    },
    onSuccess: (d) => {
      qc.setQueryData(qk.mailThread(d.thread.id), d);
      patchThreads(qc, [d.thread]);
    },
  });

  const move = (m: MoveTarget) => onAction(m.action, { folderId: m.folderId, label: m.label });


  if (isPending) return <ThreadSkeleton />;
  if (error || !detail)
    return (
      <div className="flex h-full flex-col">
        <div className="flex h-12 items-center border-b border-line px-3">
          <HeaderButton label={t("thread.back")} onClick={onClose}>
            <ArrowLeft />
          </HeaderButton>
        </div>
        <EmptyState
          art="generic"
          title={isApiError(error, "not_found") || !detail ? t("thread.notFound") : t("list.error.title")}
          description={t("thread.notFoundHint")}
          action={<Button onClick={onClose}>{t("thread.back")}</Button>}
        />
      </div>
    );

  const th = detail.thread;
  const canAct = detail.can_act;
  const inSpam = th.folder_ids.some((id) => folders?.find((f) => f.id === id)?.role === "spam");
  const inInbox = th.folder_ids.some((id) => folders?.find((f) => f.id === id)?.role === "inbox");
  const pills = folderPills(detail, folders, (b) => t(`box.${b as "inbox"}`, { defaultValue: b }));
  const sharedWith = (() => {
    const s = detail.shares;
    if (s.everyone) return t("thread.everyone");
    const names = [...s.team_ids.map((id) => teams?.find((x) => x.id === id)?.name), ...s.user_ids.map((id) => members?.find((m) => m.user.id === id)?.user.name)].filter((x): x is string => !!x);
    if (!names.length) return null;
    if (names.length === 1) return names[0];
    if (names.length === 2) return t("thread.and", { a: names[0], b: names[1] });
    return t("thread.andMore", { list: names.slice(0, 2).join(", "), count: names.length - 2 });
  })();

  // The newest message is open; older ones are a line each. Past five, the middle folds into one row.
  const last = messages.length - 1;
  const isOpen = (id: string, i: number) => (i === last ? foldedLast !== id : expanded.has(id));
  const toggle = (id: string) => setExpanded((s) => {
    const n = new Set(s);
    if (n.has(id)) n.delete(id);
    else n.add(id);
    return n;
  });
  const fold = messages.length > 5 && !showOlder;
  const hiddenCount = fold ? messages.slice(1, -2).filter((m, i) => !isOpen(m.id, i + 1)).length : 0;

  const account = accounts?.find((a) => a.id === detail.account.id);
  const composerAccounts = [detail.account];
  const participants = th.participants;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex h-12 shrink-0 items-center gap-0.5 border-b border-line px-3">
        <HeaderButton label={t("thread.back")} shortcut="escape" onClick={onClose}>
          <ArrowLeft />
        </HeaderButton>
        <span aria-hidden className="mx-1.5 h-4 w-px bg-line" />
        {canAct && (
          <>
            {inInbox || !inSpam ? (
              <HeaderButton label={t("action.archive")} shortcut="e" onClick={() => onAction("archive")}>
                <Archive />
              </HeaderButton>
            ) : null}
            {!inInbox && (
              <HeaderButton label={t("action.inbox")} onClick={() => onAction("inbox")}>
                <Inbox />
              </HeaderButton>
            )}
            <HeaderButton label={t("action.trash")} shortcut="#" onClick={() => onAction("trash")}>
              <Trash2 />
            </HeaderButton>
            {inSpam ? (
              <HeaderButton label={t("action.notSpam")} onClick={() => onAction("not_spam")}>
                <ShieldCheck />
              </HeaderButton>
            ) : (
              <HeaderButton label={t("action.spam")} shortcut="!" onClick={() => onAction("spam")}>
                <ShieldAlert />
              </HeaderButton>
            )}
            <MovePicker
              accountId={detail.account.id}
              folders={moveFolders}
              open={moveOpen}
              onOpenChange={setMoveOpen}
              onMove={move}
              trigger={
                <span className="flex">
                  <HeaderButton label={t("action.move")} shortcut="m">
                    <Folder />
                  </HeaderButton>
                </span>
              }
            />
            {hasLabels && (
              <LabelsPicker
                accountId={detail.account.id}
                folders={folders ?? []}
                applied={labelIds}
                open={labelOpen}
                onOpenChange={setLabelOpen}
                onToggle={(id, on, name) => onAction(on ? "label" : "unlabel", { folderId: id, label: name, silent: true })}
                trigger={
                  <span className="flex">
                    <HeaderButton label={t("action.label")} shortcut="l">
                      <Tag />
                    </HeaderButton>
                  </span>
                }
              />
            )}
            <span aria-hidden className="mx-1.5 h-4 w-px bg-line" />
            <HeaderButton label={th.starred ? t("action.unstar") : t("action.star")} shortcut="s" pressed={th.starred} onClick={() => onAction(th.starred ? "unstar" : "star", { silent: true })}>
              <Star className={cn(th.starred && "fill-current")} />
            </HeaderButton>
            <HeaderButton label={th.unread ? t("action.read") : t("action.unread")} shortcut={th.unread ? "shift i" : "shift u"} onClick={() => onAction(th.unread ? "read" : "unread", { silent: true })}>
              {th.unread ? <MailOpen /> : <Mail />}
            </HeaderButton>
          </>
        )}
        <div className="ml-auto flex items-center gap-0.5">
          {position && <span className="mono mr-1 hidden whitespace-nowrap text-[11px] text-fg-4 sm:inline">{position.index} / {position.total}</span>}
          <HeaderButton label={t("action.prev")} shortcut="k" onClick={onPrev} disabled={!onPrev}>
            <ChevronUp />
          </HeaderButton>
          <HeaderButton label={t("action.next")} shortcut="j" onClick={onNext} disabled={!onNext}>
            <ChevronDown />
          </HeaderButton>
          {canAct && (
            <>
              <span aria-hidden className="mx-1.5 h-4 w-px bg-line" />
              <Button size="sm" variant="ghost" icon={<Users />} onClick={() => setShare(true)}>
                {t("action.share")}
              </Button>
              <AssignPicker
                value={th.assignees.map((a) => a.id)}
                onChange={(ids) => assign.mutate(ids)}
                open={assignOpen}
                onOpenChange={setAssignOpen}
                trigger={
                  <Button size="sm" variant="ghost" icon={th.assignees.length ? undefined : <UserPlus />} aria-label={t("action.assign")}>
                    {th.assignees.length ? <AvatarStack size="xs" max={3} people={th.assignees.map((a) => ({ id: a.id, name: a.name, avatar: a.avatar_url }))} /> : t("action.assign")}
                  </Button>
                }
              />
            </>
          )}
          <Menu>
            <MenuTrigger asChild>
              <button
                type="button"
                aria-label={t("action.more")}
                className="flex size-7 items-center justify-center rounded-md text-fg-3 outline-none transition-colors hover:bg-hover hover:text-fg focus-visible:shadow-[0_0_0_2px_var(--focus)] data-[state=open]:bg-hover data-[state=open]:text-fg"
              >
                <MoreHorizontal className="size-[15px]" />
              </button>
            </MenuTrigger>
            <MenuContent>
              {canAct && (
                <>
                  <MenuItem icon={<Reply />} shortcut="r" onSelect={() => openComposer("reply")}>
                    {t("action.reply")}
                  </MenuItem>
                  {target && hasReplyAll(target, ownEmail) && (
                    <MenuItem icon={<ReplyAll />} shortcut="a" onSelect={() => openComposer("reply_all")}>
                      {t("action.replyAll")}
                    </MenuItem>
                  )}
                  <MenuItem icon={<Forward />} shortcut="f" onSelect={() => openComposer("forward")}>
                    {t("action.forward")}
                  </MenuItem>
                  <MenuSeparator />
                </>
              )}
              <MenuItem
                icon={<Link2 />}
                onSelect={() => {
                  void navigator.clipboard?.writeText(`${location.origin}/mail/all/${th.id}`).then(() => toast.success(t("action.linkCopied")));
                }}
              >
                {t("action.copyLink")}
              </MenuItem>
              <MenuItem
                icon={<RefreshCw />}
                onSelect={() => {
                  void api.collab.accounts.sync(detail.account.id).then(
                    () => toast.info(t("toast.syncing", { name: detail.account.name || detail.account.email })),
                    (e) => toast.error(e),
                  );
                }}
              >
                {t("action.syncAccount")}
              </MenuItem>
              {account?.can_manage && (
                <MenuItem icon={<Settings />} onSelect={() => void navigate({ to: "/settings/mail-accounts/$accountId", params: { accountId: detail.account.id } })}>
                  {t("action.openAccount")}
                </MenuItem>
              )}
            </MenuContent>
          </Menu>
          <span aria-hidden className="mx-1 h-4 w-px bg-line" />
          <span data-people-toggle className="flex">
            <HeaderButton label={panelOpen ? t("people.hide") : t("people.show")} shortcut="]" active={panelOpen} onClick={togglePanel}>
              <PanelRight />
            </HeaderButton>
          </span>
        </div>
      </header>

      <div className="relative flex min-h-0 flex-1">
      <div className="flex min-w-0 flex-1 flex-col">
      <div ref={scroller} className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex w-full max-w-[860px] flex-col gap-2.5 px-4 pb-10 pt-5 md:px-6">
          <div className="mb-1.5 flex flex-col gap-2">
            <h1 className="text-[17px] font-semibold leading-6 tracking-[-0.02em]" data-ph-mask>
              {subject}
            </h1>
            <div className="flex flex-wrap items-center gap-1.5 text-[11.5px] text-fg-3">
              <span className="inline-flex h-[18px] items-center gap-1.5 rounded-sm bg-hover px-1.5 font-medium text-fg-2">
                <AccountDot color={detail.account.color} />
                {detail.account.name || detail.account.email}
              </span>
              {th.to_address && (
                <Tooltip content={t("thread.toAliasHint")}>
                  <button
                    type="button"
                    onClick={() => void navigate({ to: "/mail/$box/{-$threadId}", params: { box: "inbox", threadId: undefined }, search: { account: detail.account.id, to: th.to_address! } })}
                    className="mono inline-flex h-[18px] items-center gap-1 rounded-sm bg-tone-blue-soft px-1.5 text-[11px] text-tone-blue outline-none hover:brightness-95 focus-visible:shadow-[0_0_0_2px_var(--focus)]"
                  >
                    <AtSign className="size-3" />
                    {t("thread.toAlias", { email: th.to_address })}
                  </button>
                </Tooltip>
              )}
              {detail.ticket && (
                <Tooltip content={t("thread.ticketHint", { subject: detail.ticket.subject })}>
                  <Link
                    to="/inbox/$folder/{-$ticketId}"
                    params={{ folder: "open", ticketId: detail.ticket.id }}
                    className="inline-flex h-[18px] items-center gap-1 rounded-sm bg-hover px-1.5 text-[11px] font-medium text-fg-2 outline-none hover:text-fg focus-visible:shadow-[0_0_0_2px_var(--focus)]"
                  >
                    <Ticket className="size-3 text-fg-4" />
                    {t("thread.onTicket", { number: detail.ticket.number })}
                  </Link>
                </Tooltip>
              )}
              {pills.map((p) => (
                <LabelPill key={p.id} name={p.name} color={p.color} />
              ))}
              {sharedWith && (
                <span className="inline-flex items-center gap-1 pl-1">
                  <Users className="size-3 text-fg-4" />
                  {t("thread.sharedWith", { who: sharedWith })}
                </span>
              )}
              {th.assignees.length > 0 && (
                <span className="inline-flex items-center gap-1 pl-1">
                  <UserPlus className="size-3 text-fg-4" />
                  {t("thread.assignedTo", { who: th.assignees.map((a) => a.name).join(", ") })}
                </span>
              )}
            </div>
          </div>

          {messages.map((m, i) => {
            if (fold && i > 0 && i < messages.length - 2 && !isOpen(m.id, i)) {
              // The first folded message stands for all of them.
              const firstFolded = messages.findIndex((x, j) => j > 0 && j < messages.length - 2 && !isOpen(x.id, j));
              if (i !== firstFolded) return null;
              return (
                <button
                  key="older"
                  type="button"
                  onClick={() => setShowOlder(true)}
                  className="relative flex h-7 items-center justify-center text-[11.5px] text-fg-3 outline-none hover:text-fg focus-visible:text-fg"
                >
                  <span aria-hidden className="absolute inset-x-0 top-1/2 h-px bg-line" />
                  <span className="relative rounded-full border border-line bg-surface px-2.5 py-0.5">{t("thread.older", { count: hiddenCount })}</span>
                </button>
              );
            }
            return <MailMessageItem key={m.id} m={m} open={isOpen(m.id, i)} onToggle={() => (i === last ? setFoldedLast((f) => (f === m.id ? null : m.id)) : toggle(m.id))} />;
          })}

          {drafts.map((m) => (canAct ? <DraftRow key={m.id} m={m} onOpen={() => editDraft(m)} /> : <MailMessageItem key={m.id} m={m} open={false} onToggle={() => undefined} />))}

          <AnimatePresence initial={false}>
            {detail.comments.length > 0 && (
              <motion.div
                key="comments"
                ref={commentsRef}
                initial={reduce ? { opacity: 0 } : { opacity: 0, y: 8 }}
                animate={{ opacity: 1, y: 0 }}
                exit={{ opacity: 0 }}
                transition={springs.row}
                className={cn("mt-3 scroll-my-4 rounded-lg transition-shadow duration-500 ease-out motion-reduce:transition-none", flash && "shadow-[0_0_0_3px_var(--focus)] duration-150")}
              >
                <Comments detail={detail} />
              </motion.div>
            )}
          </AnimatePresence>

          {canAct && (editing || composer) && (
            <div ref={composerRef} className="mt-1 scroll-mb-4">
              {editing ? (
                <MailComposer
                  key={`draft-${editing.id}-${composerKey}`}
                  variant="inline"
                  threadId={th.id}
                  accounts={composerAccounts}
                  accountId={detail.account.id}
                  message={all.find((m) => m.id === editing.in_reply_to_id) ?? target}
                  initial={draftFromMessage(editing)}
                  suggestions={participants}
                  autoFocus
                  onDraftId={setComposerDraftId}
                  onSent={() => {
                    setEditing(null);
                    setComposerDraftId(null);
                  }}
                  onDiscard={() => {
                    setEditing(null);
                    setComposerDraftId(null);
                  }}
                />
              ) : composer ? (
                <MailComposer
                  key={`${composer}-${composerKey}`}
                  variant="inline"
                  threadId={th.id}
                  accounts={composerAccounts}
                  accountId={detail.account.id}
                  message={target}
                  initial={startDraft(composer, { message: target, ownEmail, signature: replyFrom ? identitySignature(replyFrom) : detail.account.signature_html, from: replyFrom?.email, forwardBlock })}
                  suggestions={participants}
                  autoFocus
                  onDraftId={setComposerDraftId}
                  onSent={() => {
                    setComposer(null);
                    setComposerDraftId(null);
                  }}
                  onDiscard={() => {
                    setComposer(null);
                    setComposerDraftId(null);
                  }}
                />
              ) : null}
            </div>
          )}
        </div>
      </div>

      <footer className={cn("@container relative z-10 shrink-0 border-t border-line bg-surface transition-shadow duration-200 ease-out", below && "shadow-[0_-8px_16px_-12px_rgb(10_10_10/0.18)] dark:shadow-[0_-8px_16px_-12px_rgb(0_0_0/0.6)]")}>
        <div className="mx-auto w-full max-w-[860px] px-4 py-2.5 md:px-6">
          {canAct ? (
            <CommentBar
              ref={commentBar}
              detail={detail}
              onPosted={(id) => (posted.current = id)}
            >
              {detail.comments.length > 0 && (
                <>
                  <Tooltip content={t("thread.showComments")}>
                    <Button size="md" variant="ghost" icon={<MessageSquare />} aria-label={t("list.comments", { count: detail.comments.length })} onClick={showComments}>
                      <Count n={detail.comments.length} className="text-[11.5px]" />
                    </Button>
                  </Tooltip>
                  {!editing && !composer && <span aria-hidden className="mx-0.5 h-4 w-px bg-line" />}
                </>
              )}
              {!editing && !composer && (
                <>
                  <Tooltip content={t("action.reply")} shortcut="r">
                    <Button size="md" icon={<Reply />} aria-label={t("action.reply")} onClick={() => openComposer("reply")}>
                      <span className="hidden @[36rem]:inline">{t("action.reply")}</span>
                    </Button>
                  </Tooltip>
                  {target && hasReplyAll(target, ownEmail) && (
                    <Tooltip content={t("action.replyAll")} shortcut="a">
                      <Button size="md" icon={<ReplyAll />} aria-label={t("action.replyAll")} onClick={() => openComposer("reply_all")}>
                        <span className="hidden @[36rem]:inline">{t("action.replyAll")}</span>
                      </Button>
                    </Tooltip>
                  )}
                  <Tooltip content={t("action.forward")} shortcut="f">
                    <Button size="md" icon={<Forward />} aria-label={t("action.forward")} onClick={() => openComposer("forward")}>
                      <span className="hidden @[36rem]:inline">{t("action.forward")}</span>
                    </Button>
                  </Tooltip>
                </>
              )}
            </CommentBar>
          ) : (
            <p className="rounded-md bg-sunken px-3 py-2 text-[12px] text-fg-3">{t("thread.readOnly")}</p>
          )}
        </div>
      </footer>
      </div>
      <AnimatePresence initial={false}>
        {panelOpen &&
          (wide ? (
            <motion.aside
              key="docked"
              aria-label={t("people.title")}
              initial={reduce ? { opacity: 0 } : { width: 0, opacity: 0 }}
              animate={reduce ? { opacity: 1 } : { width: 300, opacity: 1 }}
              exit={reduce ? { opacity: 0 } : { width: 0, opacity: 0 }}
              transition={{ duration: 0.2, ease: [0.16, 1, 0.3, 1] }}
              className="shrink-0 overflow-hidden border-l border-line"
            >
              <div className="h-full w-[300px] overflow-y-auto">
                <PersonPanel detail={detail} onOpenThread={(id) => onOpenThread?.(id)} onShowAll={(email) => onShowPerson?.(email)} />
              </div>
            </motion.aside>
          ) : (
            <motion.aside
              key="sheet"
              ref={sheetRef}
              aria-label={t("people.title")}
              initial={reduce ? { opacity: 0 } : { x: 24, opacity: 0 }}
              animate={{ x: 0, opacity: 1 }}
              exit={reduce ? { opacity: 0 } : { x: 24, opacity: 0 }}
              transition={{ duration: 0.18, ease: [0.16, 1, 0.3, 1] }}
              className="absolute inset-y-0 right-0 z-20 w-[300px] max-w-full overflow-y-auto border-l border-line bg-surface shadow-[-12px_0_32px_-16px_rgb(10_10_10/0.25)] dark:shadow-[-12px_0_32px_-16px_rgb(0_0_0/0.7)]"
            >
              <PersonPanel
                detail={detail}
                onOpenThread={(id) => {
                  setSheet(false);
                  onOpenThread?.(id);
                }}
                onShowAll={(email) => {
                  setSheet(false);
                  onShowPerson?.(email);
                }}
              />
            </motion.aside>
          ))}
      </AnimatePresence>
      </div>
      <ShareDialog detail={detail} open={share} onOpenChange={setShare} />
    </div>
  );
});
