// Collab mail: a box (or folder) across accounts or one, the conversation list
// beside the open conversation. Every action shows at once and is undone if the
// API refuses; archive, trash, spam and moves offer Undo. Keyboard first.

import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { AnimatePresence } from "motion/react";
import { Archive, AtSign, Folder, Mail, MailOpen, PenSquare, RefreshCw, ShieldAlert, Star, Tag, Trash2, UserRound, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { MailFolder, MailThread, MailThreadAction } from "@/lib/api/types";
import { api } from "@/lib/api/endpoints";
import { inverseAction, leavesList, listPlace, UNDOABLE } from "@/lib/collab/actions";
import { accountName, isMailBox, mailListQuery, type MailSearch } from "@/lib/collab/boxes";
import { cachedFolders } from "@/lib/collab/cache";
import { compose } from "@/lib/collab/compose";
import { addressNumbers } from "@/lib/collab/badges";
import { accountCategories, categoryTabs, PRIMARY, ruleSender } from "@/lib/collab/categories";
import { useAllFolders, useCanAct, useMailAccounts, useSidebarPrefs, useThreadAction } from "@/lib/collab/hooks";
import { type ComposeMode } from "@/lib/collab/recipients";
import { identitiesOf, ownEmails as ownEmailsOf } from "@/lib/collab/identities";
import { useHotkeys } from "@/lib/hotkeys";
import { cn } from "@/lib/cn";
import { qk, q } from "@/lib/queries";
import { useDocumentTitle } from "@/app/RootLayout";
import { usePageCrumb } from "@/app/useBreadcrumbs";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty";
import { SearchInput } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { SkeletonRows } from "@/components/ui/skeleton";
import { toast } from "@/components/ui/toast";
import { Tooltip } from "@/components/ui/tooltip";
import { AccountDot, boxIcon, FolderGlyph } from "@/components/collab/bits";
import { canOfferCategories, CategoryOffer, CategoryTabs, useCategorySettingsTarget } from "@/components/collab/CategoryTabs";
import { FolderBar } from "@/components/collab/FolderBar";
import { SearchCoverage, StorageFullBanner } from "@/components/collab/MailNotices";
import { LabelsPicker, MovePicker, type MoveTarget } from "@/components/collab/pickers";
import { ThreadRow } from "@/components/collab/ThreadRow";
import { ThreadView, type ThreadViewHandle } from "@/components/collab/ThreadView";

type ActOpts = { folderId?: string; category?: string; label?: string; silent?: boolean };

const toastKey: Partial<Record<MailThreadAction, "archived" | "trashed" | "spammed" | "moved" | "inboxed" | "notSpam">> = {
  archive: "archived",
  trash: "trashed",
  spam: "spammed",
  move: "moved",
  inbox: "inboxed",
  not_spam: "notSpam",
};

function BulkButton({ label, shortcut, onClick, children }: { label: string; shortcut?: string; onClick?: () => void; children: ReactNode }) {
  return (
    <Tooltip content={label} shortcut={shortcut}>
      <button
        type="button"
        aria-label={label}
        onClick={onClick}
        className="flex size-7 items-center justify-center rounded-md text-fg-2 outline-none transition-colors hover:bg-hover hover:text-fg focus-visible:shadow-[0_0_0_2px_var(--focus)] data-[state=open]:bg-hover [&_svg]:size-[15px]"
      >
        {children}
      </button>
    </Tooltip>
  );
}

function usePhone() {
  return useSyncExternalStore(
    (cb) => {
      const m = window.matchMedia("(max-width: 767px)");
      m.addEventListener("change", cb);
      return () => m.removeEventListener("change", cb);
    },
    () => window.matchMedia("(max-width: 767px)").matches,
    () => false,
  );
}

export default function MailPage() {
  const { t } = useTranslation("collab");
  const params = useParams({ strict: false }) as { box?: string; threadId?: string };
  const search = useSearch({ strict: false }) as MailSearch;
  const navigate = useNavigate();
  const qc = useQueryClient();
  const canAct = useCanAct();
  const box = params.box ?? "inbox";
  const openId = params.threadId;
  const { data: accounts } = useMailAccounts();
  const { data: counts } = useQuery(q.mailCounts());
  const prefs = useSidebarPrefs();
  const folderMap = useAllFolders(accounts);
  const action = useThreadAction();

  const account = search.account ? accounts?.find((a) => a.id === search.account) : undefined;
  const folder: MailFolder | undefined = search.folder ? Object.values(folderMap).flatMap((f) => f ?? []).find((f) => f.id === search.folder) : undefined;
  const folderAccount = folder ? accounts?.find((a) => a.id === folder.account_id) : undefined;
  const spans = !account && !folder && (accounts?.length ?? 0) > 1;
  const ownEmails = useMemo(() => new Set((accounts ?? []).flatMap((a) => ownEmailsOf(a))), [accounts]);
  const accountsById = useMemo(() => new Map((accounts ?? []).map((a) => [a.id, a])), [accounts]);

  // An inbox (no folder, no search) whose accounts are split shows its tabs; Primary when none is chosen.
  const tabbed = box === "inbox" && !search.folder && !search.q;
  const tabAccounts = useMemo(() => (!tabbed || !accounts ? [] : account ? [account] : accounts), [tabbed, accounts, account]);
  const tabs = useMemo(() => categoryTabs(tabAccounts, counts), [tabAccounts, counts]);
  const category = tabs.length ? (tabs.some((c) => c.key === search.category) ? search.category : PRIMARY) : undefined;
  const settingsTarget = useCategorySettingsTarget(tabAccounts);
  const offer = !tabs.length && canAct ? tabAccounts.find(canOfferCategories) : undefined;
  const query = useMemo(() => mailListQuery(box, { ...search, category }), [box, search.account, search.folder, search.q, search.to, search.with, category]); // eslint-disable-line react-hooks/exhaustive-deps
  const listKey = JSON.stringify(query);
  // The inbox waits for the accounts, which say whether it opens on a tab.
  const list = useInfiniteQuery({ ...q.mailThreads(query), enabled: !tabbed || !!accounts });
  const threads = useMemo(() => list.data?.pages.flatMap((p) => p.data) ?? [], [list.data]);

  const boxLabel = isMailBox(box) ? (box === "inbox" && !account ? t("nav.allInboxes") : t(`box.${box}`)) : t("box.folder");
  const title = folder ? folder.name : account ? `${boxLabel} · ${accountName(account)}` : boxLabel;
  useDocumentTitle(openId ? null : title);
  usePageCrumb(openId ? null : title);
  const count = folder
    ? (counts?.folders[folder.id] ?? folder.unread)
    : account && box === "inbox" && search.to
      ? addressNumbers(account.id, search.to, counts).unread
      : account && box === "inbox"
        ? (counts?.accounts[account.id] ?? account.unread)
        : !account && isMailBox(box) && box in (counts?.boxes ?? {})
          ? counts?.boxes[box as "inbox"]
          : undefined;

  // ── Selection and keyboard focus ────────────────────────────────────────
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [focusIdx, setFocusIdx] = useState(0);
  const lastToggled = useRef<string | null>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const threadRef = useRef<ThreadViewHandle>(null);
  const [intent, setIntent] = useState<{ id: string; mode: ComposeMode } | null>(null);
  const [pickMove, setPickMove] = useState(false);
  const [pickLabels, setPickLabels] = useState(false);
  const [q2, setQ2] = useState(search.q ?? "");

  useEffect(() => {
    setSelected(new Set());
    setFocusIdx(0);
  }, [listKey]);
  useEffect(() => setQ2(search.q ?? ""), [search.q]);

  useEffect(() => {
    setSelected((s) => {
      if (!s.size) return s;
      const ids = new Set(threads.map((x) => x.id));
      const next = new Set([...s].filter((id) => ids.has(id)));
      return next.size === s.size ? s : next;
    });
    setFocusIdx((i) => Math.max(0, Math.min(i, threads.length - 1)));
  }, [threads]);

  useEffect(() => {
    if (!openId) return;
    const i = threads.findIndex((x) => x.id === openId);
    if (i >= 0) setFocusIdx(i);
  }, [openId, threads]);

  useEffect(() => {
    listRef.current?.querySelector<HTMLElement>(`[data-thread-id="${threads[focusIdx]?.id}"]`)?.scrollIntoView({ block: "nearest" });
  }, [focusIdx, threads]);

  const go = useCallback(
    (threadId: string | undefined, s: MailSearch | true = true, replace?: boolean) =>
      void navigate({ to: "/mail/$box/{-$threadId}", params: { box, threadId }, search: s === true ? (prev: MailSearch) => prev : s, replace }),
    [navigate, box],
  );
  const open = useCallback((x: MailThread | undefined) => x && go(x.id), [go]);
  const close = useCallback(() => go(undefined), [go]);

  const setQuery = (v: string) => {
    const next: MailSearch = { ...search, q: v.trim() || undefined };
    go(undefined, next, true);
  };
  // One account's mail written to one of its addresses (an alias): GET /v1/collab/threads?to=.
  const scopeAccount = account ?? folderAccount;
  const addresses = scopeAccount ? identitiesOf(scopeAccount) : [];
  const setTo = (v: string) => go(undefined, { ...search, to: v || undefined }, true);

  const toggle = useCallback(
    (x: MailThread, shift: boolean) => {
      setSelected((s) => {
        const next = new Set(s);
        if (shift && lastToggled.current) {
          const a = threads.findIndex((y) => y.id === lastToggled.current);
          const b = threads.findIndex((y) => y.id === x.id);
          if (a >= 0 && b >= 0) {
            for (let i = Math.min(a, b); i <= Math.max(a, b); i++) next.add(threads[i].id);
            return next;
          }
        }
        if (next.has(x.id)) next.delete(x.id);
        else next.add(x.id);
        lastToggled.current = x.id;
        return next;
      });
    },
    [threads],
  );

  const move = (d: 1 | -1) => {
    const i = Math.max(0, Math.min(threads.length - 1, focusIdx + d));
    setFocusIdx(i);
    if (openId) open(threads[i]);
    if (d === 1 && i >= threads.length - 5 && list.hasNextPage && !list.isFetchingNextPage) void list.fetchNextPage();
  };

  /** What a key acts on: the selection, else the open conversation, else the focused one. */
  const targets = (): MailThread[] => {
    if (selected.size) return threads.filter((x) => selected.has(x.id));
    if (openId) {
      const open = threads.find((x) => x.id === openId) ?? qc.getQueryData<{ thread: MailThread }>(qk.mailThread(openId))?.thread;
      return open ? [open] : [];
    }
    return threads[focusIdx] ? [threads[focusIdx]] : [];
  };

  // ── Actions ─────────────────────────────────────────────────────────────
  /**
   * Moved to a category: undo puts them back where they were, and when they are all from one
   * sender of an account the caller manages, Always makes it a rule for that sender's mail.
   */
  const categorized = useCallback(
    (items: MailThread[], key: string, name: string) => {
      const ids = items.map((x) => x.id);
      const before = new Set(items.map((x) => x.category ?? PRIMARY));
      const back = before.size === 1 ? [...before][0] : null;
      const undo = back
        ? () =>
            action.mutate(
              { ids, action: "categorize", category: back },
              { onSuccess: () => void qc.invalidateQueries({ queryKey: qk.mailThreadLists }), onError: (e) => toast.error(e, t("toast.failed")) },
            )
        : null;
      const acc = accountsById.get(items[0].account_id);
      const senders = new Set(items.map((x) => ruleSender(x.participants, ownEmails)));
      const sender = senders.size === 1 ? [...senders][0] : null;
      const msg = t("categories.moved", { count: ids.length, name });
      if (!acc?.can_manage || !sender || items.some((x) => x.account_id !== acc.id)) {
        if (undo) toast.undo(msg, undo, t("toast.undo"));
        else toast.success(msg);
        return;
      }
      const always = () =>
        void api.collab.categories.addRule(acc.id, { sender, category: key }).then(
          () => {
            void qc.invalidateQueries({ queryKey: qk.mailCategoryRules(acc.id) });
            toast.success(t("categories.ruleSaved", { sender, name }));
          },
          (e) => toast.error(e, t("toast.failed")),
        );
      toast.success(msg, {
        description: t("categories.alwaysHint", { sender }),
        action: { label: t("categories.always"), onClick: always },
        cancel: undo ? { label: t("toast.undo"), onClick: undo } : undefined,
      });
    },
    [accountsById, ownEmails, action, qc, t],
  );

  const act = useCallback(
    (items: MailThread[], a: MailThreadAction, opts: ActOpts = {}) => {
      if (!canAct || !items.length) return;
      const ids = items.map((x) => x.id);
      const place = listPlace(query, cachedFolders(qc));
      // The open conversation leaving the list: the next one opens (or the list comes back).
      if (openId && ids.includes(openId) && leavesList(a, place, opts.folderId, opts.category)) {
        const i = threads.findIndex((x) => x.id === openId);
        const rest = threads.filter((x) => !ids.includes(x.id));
        const next = rest[Math.min(Math.max(i, 0), rest.length - 1)];
        if (next) go(next.id, true, true);
        else close();
      }
      setSelected((s) => (s.size ? new Set() : s));
      action.mutate(
        { ids, action: a, folderId: opts.folderId, category: opts.category },
        {
          onError: (e) => toast.error(e, t("toast.failed")),
        },
      );
      if (a === "categorize" && opts.category && !opts.silent) return categorized(items, opts.category, opts.label ?? "");
      const key = toastKey[a];
      if (opts.silent || !key) return;
      const msg = t(`toast.${key}`, { count: ids.length, folder: opts.label ?? "" });
      const back = UNDOABLE.has(a) ? inverseAction(a, place) : null;
      if (back)
        toast.undo(
          msg,
          () =>
            action.mutate(
              { ids, action: back.action, folderId: back.folderId },
              { onSuccess: () => void qc.invalidateQueries({ queryKey: qk.mailThreadLists }), onError: (e) => toast.error(e, t("toast.failed")) },
            ),
          t("toast.undo"),
        );
      else toast.success(msg);
    },
    [canAct, query, qc, openId, threads, go, close, action, t, categorized],
  );

  const onRowAction = useCallback((x: MailThread, a: MailThreadAction) => act([x], a, { silent: a !== "archive" && a !== "trash" }), [act]);
  const onThreadAction = useCallback(
    (a: MailThreadAction, opts?: ActOpts) => {
      if (!openId) return;
      const x = threads.find((y) => y.id === openId) ?? qc.getQueryData<{ thread: MailThread }>(qk.mailThread(openId))?.thread;
      if (x) act([x], a, opts);
    },
    [openId, threads, qc, act],
  );

  const star = () => {
    const ts = targets();
    if (ts.length) act(ts, ts.every((x) => x.starred) ? "unstar" : "star", { silent: true });
  };
  const answer = (mode: ComposeMode) => {
    if (!canAct) return;
    if (openId) return threadRef.current?.reply(mode);
    const x = threads[focusIdx];
    if (!x) return;
    setIntent({ id: x.id, mode });
    open(x);
  };
  // Move and labels act within one account.
  const targetAccount = (() => {
    const ts = selected.size ? threads.filter((x) => selected.has(x.id)) : threads[focusIdx] ? [threads[focusIdx]] : [];
    const ids = new Set(ts.map((x) => x.account_id));
    return ids.size === 1 ? [...ids][0] : null;
  })();
  const targetFolders = targetAccount ? (folderMap[targetAccount] ?? []) : [];
  const targetProvider = targetAccount ? accountsById.get(targetAccount)?.provider : undefined;
  // Labels: Gmail labels and Outlook categories (IMAP has folders only); moving to a label is Gmail's.
  const targetLabels = !!targetProvider && targetProvider !== "imap";
  const moveFolders = targetProvider === "google" ? targetFolders : targetFolders.filter((f) => f.kind !== "label");
  const pickTargets = () => (selected.size ? threads.filter((x) => selected.has(x.id)) : threads[focusIdx] ? [threads[focusIdx]] : []);
  const appliedLabels = (() => {
    const ts = pickTargets();
    if (!ts.length) return [];
    return targetFolders.filter((f) => f.kind === "label" && !f.role && ts.every((x) => x.folder_ids.includes(f.id))).map((f) => f.id);
  })();

  useHotkeys({
    j: () => move(1),
    arrowdown: () => !openId && move(1),
    k: () => move(-1),
    arrowup: () => !openId && move(-1),
    enter: () => !openId && open(threads[focusIdx]),
    o: () => open(threads[focusIdx]),
    x: () => threads[focusIdx] && toggle(threads[focusIdx], false),
    e: () => act(targets(), "archive"),
    "#": () => act(targets(), "trash"),
    "!": () => act(targets(), "spam"),
    s: (e) => !e.shiftKey && star(),
    u: (e) => e.shiftKey && act(targets(), "unread", { silent: true }),
    i: (e) => e.shiftKey && act(targets(), "read", { silent: true }),
    m: () => (openId && !selected.size ? threadRef.current?.move() : canAct && targetAccount && setPickMove(true)),
    l: () => (openId && !selected.size ? threadRef.current?.labels() : canAct && targetLabels && setPickLabels(true)),
    r: () => answer("reply"),
    a: () => answer("reply_all"),
    f: () => answer("forward"),
    c: (e) => (e.shiftKey ? openId && threadRef.current?.comment() : compose.open({ accountId: account?.id ?? folderAccount?.id })),
    "]": () => openId && threadRef.current?.people(),
    "/": () => searchRef.current?.focus(),
    escape: () => {
      if (selected.size) setSelected(new Set());
      else if (openId) close();
    },
    "mod+a": () => !openId && setSelected(new Set(threads.map((x) => x.id))),
  });

  // Infinite scroll.
  const sentinel = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = sentinel.current;
    if (!el) return;
    const io = new IntersectionObserver((e) => {
      if (e[0]?.isIntersecting && list.hasNextPage && !list.isFetchingNextPage) void list.fetchNextPage();
    });
    io.observe(el);
    return () => io.disconnect();
  }, [list]);

  const prefetch = useCallback((x: MailThread) => void qc.prefetchQuery(q.mailThread(x.id)), [qc]);
  useEffect(() => {
    [focusIdx, focusIdx + 1].forEach((i) => threads[i] && prefetch(threads[i]));
  }, [focusIdx, threads, prefetch]);

  const selecting = selected.size > 0;
  const idx = threads.findIndex((x) => x.id === openId);
  const phone = usePhone();
  const split = !!openId;
  const compact = split || phone;
  const noAccounts = accounts && accounts.length === 0;

  // Off the inbox's tabs (a search, another box), a conversation sorted out of Primary says where to.
  const categoryOf = (x: MailThread) => (x.category && x.category !== PRIMARY ? accountCategories(accountsById.get(x.account_id)).find((c) => c.key === x.category) : undefined);
  const labelsFor = (x: MailThread): MailFolder[] => {
    const fs = folderMap[x.account_id];
    if (!fs) return [];
    return fs.filter((f) => f.kind === "label" && !f.role && f.id !== search.folder && x.folder_ids.includes(f.id));
  };

  const empty = (() => {
    if (search.q)
      return (
        <EmptyState
          art="search"
          title={t("list.empty.search")}
          description={t("list.empty.searchHint")}
          action={
            <Button variant="secondary" onClick={() => setQuery("")}>
              {t("list.empty.clearSearch")}
            </Button>
          }
        />
      );
    if (box === "inbox" && !folder) return <EmptyState art="solved" title={t("list.empty.inbox")} description={t("list.empty.inboxHint")} />;
    if (box === "shared") return <EmptyState art="inbox" title={t("list.empty.shared")} description={t("list.empty.sharedHint")} />;
    if (box === "assigned") return <EmptyState art="mine" title={t("list.empty.assigned")} description={t("list.empty.assignedHint")} />;
    if (box === "starred") return <EmptyState art="generic" title={t("list.empty.starred")} description={t("list.empty.starredHint")} tips={[{ label: t("shortcuts.star"), kbd: "s" }]} />;
    return <EmptyState art="generic" title={t("list.empty.generic")} description={t("list.empty.genericHint")} />;
  })();

  const titleIcon = folder ? <FolderGlyph folder={folder} /> : account ? <AccountDot color={account.color} className="size-2.5" /> : isMailBox(box) ? boxIcon[box] : <Mail />;

  return (
    <div className="relative flex h-full min-h-0">
      <section
        aria-label={title}
        className={cn("relative flex min-h-0 flex-col", split ? "hidden w-[340px] shrink-0 border-r border-line lg:flex xl:w-[380px] 2xl:w-[420px]" : "min-w-0 flex-1")}
      >
        <div className="flex h-12 shrink-0 items-center gap-2 border-b border-line px-4">
          <span className="flex size-4 shrink-0 items-center justify-center text-fg-3 [&_svg]:size-4">{titleIcon}</span>
          <h1 className="min-w-0 truncate text-[13.5px] font-semibold tracking-[-0.015em]">{title}</h1>
          {!!count && <span className="mono shrink-0 text-[11px] tabular-nums text-fg-4">{count}</span>}
          <div className="ml-auto flex shrink-0 items-center gap-1">
            {!compact && (
              <SearchInput
                inputRef={searchRef}
                value={q2}
                onChange={(v) => {
                  setQ2(v);
                  if (!v) setQuery("");
                }}
                onKeyDown={(e) => {
                  if (e.key === "Enter") setQuery(q2);
                  if (e.key === "Escape") searchRef.current?.blur();
                }}
                placeholder={t("list.search")}
                className="w-[220px]"
              />
            )}
            {account && (
              <Tooltip content={t("list.refresh")}>
                <Button
                  size="sm"
                  variant="ghost"
                  square
                  aria-label={t("list.refresh")}
                  icon={<RefreshCw />}
                  onClick={() =>
                    void api.collab.accounts.sync(account.id).then(
                      () => toast.info(t("toast.syncing", { name: accountName(account) })),
                      (e) => toast.error(e),
                    )
                  }
                />
              </Tooltip>
            )}
            {canAct && !noAccounts && (
              <Tooltip content={t("list.compose")} shortcut="c">
                <Button size="sm" variant={compact ? "ghost" : "secondary"} square={compact} icon={<PenSquare />} onClick={() => compose.open({ accountId: account?.id ?? folderAccount?.id })} aria-label={t("list.compose")}>
                  {compact ? null : t("list.compose")}
                </Button>
              </Tooltip>
            )}
          </div>
        </div>
        {!noAccounts && accounts && box !== "shared" && box !== "assigned" && (
          <FolderBar
            box={box}
            account={account}
            folder={folder}
            accounts={accounts}
            folderMap={folderMap}
            counts={counts}
            prefs={prefs}
          />
        )}
        {tabs.length > 0 && category && <CategoryTabs tabs={tabs} active={category} search={search} settings={settingsTarget} />}
        {offer && <CategoryOffer account={offer} />}
        {!noAccounts && <StorageFullBanner />}
        {search.q && !noAccounts && <SearchCoverage accounts={scopeAccount ? [scopeAccount] : (accounts ?? [])} />}
        {(addresses.length > 1 || search.to) && (
          <div className="flex h-9 shrink-0 items-center gap-2 border-b border-line-soft px-4 text-[11.5px] text-fg-3">
            <AtSign className="size-3.5 shrink-0 text-fg-4" />
            <span className="shrink-0">{t("list.writtenTo")}</span>
            {addresses.length > 1 ? (
              <Select
                size="sm"
                value={search.to ?? "*"}
                onChange={(v) => setTo(v === "*" ? "" : v)}
                aria-label={t("list.writtenTo")}
                className="h-7 max-w-[280px] border-transparent px-1.5 shadow-none hover:border-line"
                options={[
                  { value: "*", label: t("list.anyAddress") },
                  ...addresses.map((i) => {
                    const n = scopeAccount ? addressNumbers(scopeAccount.id, i.email, counts).unread : undefined;
                    return { value: i.email, label: i.email, hint: n || undefined };
                  }),
                ]}
              />
            ) : (
              <span className="mono min-w-0 truncate text-fg-2">{search.to}</span>
            )}
            {search.to && (
              <Button size="sm" variant="ghost" icon={<X />} onClick={() => setTo("")} className="ml-auto">
                {t("list.anyAddress")}
              </Button>
            )}
          </div>
        )}
        {search.with && (
          <div className="flex h-9 shrink-0 items-center gap-2 border-b border-line-soft px-4 text-[11.5px] text-fg-3">
            <UserRound className="size-3.5 shrink-0 text-fg-4" />
            <span className="shrink-0">{t("list.with")}</span>
            <span className="mono min-w-0 truncate text-fg-2" data-ph-mask>
              {search.with}
            </span>
            <Button size="sm" variant="ghost" icon={<X />} onClick={() => go(undefined, { ...search, with: undefined }, true)} className="ml-auto">
              {t("list.anyone")}
            </Button>
          </div>
        )}
        {compact && (
          <div className="border-b border-line-soft px-3 py-2">
            <SearchInput
              inputRef={searchRef}
              value={q2}
              onChange={(v) => {
                setQ2(v);
                if (!v) setQuery("");
              }}
              onKeyDown={(e) => {
                if (e.key === "Enter") setQuery(q2);
                if (e.key === "Escape") searchRef.current?.blur();
              }}
              placeholder={t("list.search")}
            />
          </div>
        )}

        {canAct && targetAccount && (
          <div className="pointer-events-none absolute left-4 top-12 h-0 w-0">
            <MovePicker
              accountId={targetAccount}
              folders={moveFolders}
              categories={accountCategories(accountsById.get(targetAccount))}
              current={category}
              open={pickMove}
              onOpenChange={setPickMove}
              onMove={(m: MoveTarget) => act(pickTargets(), m.action, { folderId: m.folderId, category: m.category, label: m.label })}
              trigger={<span aria-hidden className="block h-0 w-0" />}
            />
            {targetLabels && (
              <LabelsPicker
                accountId={targetAccount}
                folders={targetFolders}
                applied={appliedLabels}
                open={pickLabels}
                onOpenChange={setPickLabels}
                onToggle={(id, on) => act(pickTargets(), on ? "label" : "unlabel", { folderId: id, silent: true })}
                trigger={<span aria-hidden className="block h-0 w-0" />}
              />
            )}
          </div>
        )}

        <div ref={listRef} role="listbox" aria-label={t("list.label")} aria-multiselectable className="flex min-h-0 flex-1 flex-col overflow-y-auto">
          {noAccounts ? (
            <EmptyState
              art="inbox"
              title={t("list.noAccounts.title")}
              description={t("list.noAccounts.description")}
              action={
                <Button variant="primary" onClick={() => void navigate({ to: "/settings/mail-accounts", search: {} })}>
                  {t("list.noAccounts.action")}
                </Button>
              }
              tips={[{ label: t("list.noAccounts.tipPrivate") }, { label: t("list.noAccounts.tipMirror") }]}
            />
          ) : list.isPending ? (
            <SkeletonRows rows={compact ? 7 : 12} />
          ) : list.isError ? (
            <EmptyState lit={3} title={t("list.error.title")} description={t("list.error.description")} action={<Button onClick={() => void list.refetch()}>{t("list.error.retry")}</Button>} />
          ) : !threads.length ? (
            empty
          ) : (
            <div className="relative shrink-0">
              <AnimatePresence initial={false} key={listKey}>
                {threads.map((x, i) => {
                  const acc = accountsById.get(x.account_id);
                  return (
                    <ThreadRow
                      key={x.id}
                      thread={x}
                      active={x.id === openId}
                      focused={i === focusIdx}
                      selected={selected.has(x.id)}
                      selecting={selecting}
                      compact={compact}
                      accountColor={spans ? acc?.color : undefined}
                      accountName={acc ? accountName(acc) : undefined}
                      labels={labelsFor(x)}
                      category={category ? undefined : categoryOf(x)}
                      ownEmails={ownEmails}
                      onOpen={open}
                      onToggle={toggle}
                      onHover={prefetch}
                      onAction={canAct ? onRowAction : undefined}
                    />
                  );
                })}
              </AnimatePresence>
            </div>
          )}
          {threads.length > 0 && (
            <div ref={sentinel} className="shrink-0">
              {list.isFetchingNextPage ? <SkeletonRows rows={2} /> : <div className="h-14" />}
            </div>
          )}
        </div>

        {selecting && canAct && (
          <div className="absolute inset-x-3 bottom-3 z-10 flex items-center gap-0.5 rounded-lg border border-line bg-raised px-2 py-1 shadow-float animate-rise-in motion-reduce:animate-none">
            <span className="mono px-1.5 text-[11.5px] tabular-nums text-fg-2">{t("list.selected", { count: selected.size })}</span>
            <span aria-hidden className="mx-1 h-4 w-px bg-line" />
            <BulkButton label={t("action.archive")} shortcut="e" onClick={() => act(targets(), "archive")}>
              <Archive />
            </BulkButton>
            <BulkButton label={t("action.trash")} shortcut="#" onClick={() => act(targets(), "trash")}>
              <Trash2 />
            </BulkButton>
            <BulkButton label={t("action.spam")} shortcut="!" onClick={() => act(targets(), "spam")}>
              <ShieldAlert />
            </BulkButton>
            <BulkButton label={t("action.read")} shortcut="shift i" onClick={() => act(targets(), "read", { silent: true })}>
              <MailOpen />
            </BulkButton>
            <BulkButton label={t("action.unread")} shortcut="shift u" onClick={() => act(targets(), "unread", { silent: true })}>
              <Mail />
            </BulkButton>
            <BulkButton label={t("action.star")} shortcut="s" onClick={star}>
              <Star />
            </BulkButton>
            {targetAccount && (
              <BulkButton label={t("action.move")} shortcut="m" onClick={() => setPickMove(true)}>
                <Folder />
              </BulkButton>
            )}
            {targetLabels && (
              <BulkButton label={t("action.label")} shortcut="l" onClick={() => setPickLabels(true)}>
                <Tag />
              </BulkButton>
            )}
            <span className="flex-1" />
            <BulkButton label={t("list.clear")} shortcut="escape" onClick={() => setSelected(new Set())}>
              <X />
            </BulkButton>
          </div>
        )}
      </section>

      {openId && (
        <section aria-label={t("thread.label")} className="flex min-w-0 flex-1 flex-col">
          <ThreadView
            key={openId}
            ref={threadRef}
            threadId={openId}
            onClose={close}
            onPrev={idx > 0 ? () => open(threads[idx - 1]) : undefined}
            onNext={idx >= 0 && idx < threads.length - 1 ? () => open(threads[idx + 1]) : undefined}
            position={idx >= 0 ? { index: idx + 1, total: threads.length } : undefined}
            onAction={onThreadAction}
            markReadOnOpen={prefs.mark_read_on_open}
            intent={intent?.id === openId ? intent.mode : null}
            openDraft={box === "drafts"}
            onOpenThread={(id) => go(id)}
            onShowPerson={(email) =>
              void navigate({ to: "/mail/$box/{-$threadId}", params: { box: "all", threadId: openId }, search: { with: email } })
            }
          />
        </section>
      )}
    </div>
  );
}
