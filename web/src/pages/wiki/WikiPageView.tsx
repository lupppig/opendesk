// One wiki page (/wiki/$pageId): its icon and title edited in place, the body in the
// knowledge base's editor, saved as people type (lib/wiki/saver.ts). Breadcrumbs follow
// the live tree; "Last edited" ticks in its own small component. When someone else saves
// first, the page keeps what was typed and asks whose version stays. A version loaded
// from the server (theirs, a restored one) is shown silently: loading is not an edit, and
// only someone who may edit ever saves.

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { getRouteApi, Link, useNavigate } from "@tanstack/react-router";
import type { Editor } from "@tiptap/react";
import { AlertTriangle, Check, CloudOff, FolderInput, History, Link2, MoreHorizontal, Plus, SmilePlus } from "lucide-react";
import { useCallback, useEffect, useMemo, useReducer, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useDocumentTitle } from "@/app/RootLayout";
import { usePageCrumb } from "@/app/useBreadcrumbs";
import { RichEditor, Toolbar, type RichEditorHandle } from "@/components/editor/RichEditor";
import { Morph } from "@/components/motion";
import { Avatar } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { copyText } from "@/components/ui/copy-button";
import { EmptyState } from "@/components/ui/empty";
import { Menu, MenuContent, MenuItem, MenuTrigger } from "@/components/ui/menu";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { toast } from "@/components/ui/toast";
import { Tooltip } from "@/components/ui/tooltip";
import { HistoryDrawer } from "@/components/wiki/HistoryDrawer";
import { IconPicker } from "@/components/wiki/IconPicker";
import { DeleteItem, MoveDialog } from "@/components/wiki/PageMenu";
import { UnsentBanner } from "@/components/wiki/Unsent";
import { PageIcon, pageUrl } from "@/components/wiki/WikiTree";
import { freshPages, nodeOf, patchTree, useCreatePage, useTitle, useWikiTree } from "@/components/wiki/hooks";
import { api } from "@/lib/api/endpoints";
import { errorMessage, isApiError } from "@/lib/api/client";
import type { WikiPage } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { fullDate, relLong } from "@/lib/format";
import { q, qk } from "@/lib/queries";
import { atLeast, useRole } from "@/lib/session";
import { useNow } from "@/lib/useNow";
import { forgetSaver, openSaver, saverFor, type PageSaver } from "@/lib/wiki/saver";
import { ancestorsOf, childrenOf, MAX_TITLE } from "@/lib/wiki/tree";
import { PagesButton } from "./WikiLayout";

const route = getRouteApi("/app/wiki/$pageId");

export default function WikiPageView() {
  const { t } = useTranslation("wiki");
  const { pageId } = route.useParams();
  const page = useQuery(q.wikiPage(pageId));
  if (page.isLoading)
    return (
      <div className="flex min-h-0 flex-1 flex-col">
        <div className="h-12 shrink-0 border-b border-line" />
        <div className="mx-auto w-full max-w-[760px] px-6 pt-16">
          <Skeleton className="h-8 w-8 rounded-md" />
          <Skeleton className="mt-4 h-7 w-2/3" />
          <Skeleton className="mt-3 h-3 w-40" />
          <Skeleton className="mt-10 h-3 w-full" />
          <Skeleton className="mt-3 h-3 w-5/6" />
          <Skeleton className="mt-3 h-3 w-4/6" />
        </div>
      </div>
    );
  if (!page.data)
    return (
      <div className="flex min-h-0 flex-1 flex-col">
        <header className="flex h-12 shrink-0 items-center border-b border-line px-4">
          <PagesButton />
        </header>
        {isApiError(page.error, "not_found") && <Orphan id={pageId} />}
        <EmptyState
          art="wiki"
          title={isApiError(page.error, "not_found") ? t("page.notFound") : t("page.loadFailed")}
          description={isApiError(page.error, "not_found") ? t("page.notFoundHint") : errorMessage(page.error)}
          action={
            <Button asChild>
              <Link to="/wiki">{t("page.backHome")}</Link>
            </Button>
          }
        />
      </div>
    );
  return <PageScreen key={pageId} page={page.data} />;
}

/** Typing left on a page that is gone: it can still be copied. */
function Orphan({ id }: { id: string }) {
  const saver = openSaver(id);
  if (!saver?.pending) return null;
  return <OrphanBanner saver={saver} />;
}

function OrphanBanner({ saver }: { saver: PageSaver }) {
  useSaver(saver);
  if (!saver.pending) return null;
  return <UnsentBanner reason="deleted" draft={saver.draft} onDiscard={() => forgetSaver(saver.id)} />;
}

function useSaver(saver: PageSaver) {
  const [, bump] = useReducer((n: number) => n + 1, 0);
  useEffect(() => {
    const off = saver.subscribe(bump);
    return () => {
      off();
    };
  }, [saver]);
}

export function PageScreen({ page }: { page: WikiPage }) {
  const { t } = useTranslation("wiki");
  const { t: ui } = useTranslation("ui");
  const qc = useQueryClient();
  const title = useTitle();
  const canEdit = atLeast(useRole(), "agent");
  const create = useCreatePage();
  const navigate = useNavigate();
  const { tree } = useWikiTree();

  const saver = useMemo(
    () =>
      saverFor(page, {
        onSaved: (p) => {
          qc.setQueryData<WikiPage>(qk.wikiPage(p.id), (old) => ({ ...(old ?? p), ...p }));
          patchTree(qc, (nodes) => nodes.map((n) => (n.id === p.id ? nodeOf(p) : n)));
        },
      }),
    // One saver per page: later loads of the page reach it through rebase below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [page.id],
  );
  useSaver(saver);
  const draft = saver.draft;
  const shown = title(draft.title);
  useDocumentTitle(shown);
  usePageCrumb(shown);

  const editorRef = useRef<RichEditorHandle>(null);
  const titleRef = useRef<HTMLTextAreaElement>(null);
  const [editor, setEditor] = useState<Editor | null>(null);
  const [, rerender] = useReducer((n: number) => n + 1, 0);
  useEffect(() => {
    const ed = editorRef.current?.editor ?? null;
    setEditor(ed);
    if (!ed) return;
    ed.on("transaction", rerender);
    return () => {
      ed.off("transaction", rerender);
    };
  }, []);

  // A page made a moment ago starts with its title focused.
  useEffect(() => {
    if (canEdit && freshPages.delete(page.id)) titleRef.current?.focus();
  }, [canEdit, page.id]);

  // What is typed goes out when the page closes (navigation keeps the saver alive).
  useEffect(() => () => void saver.flush(), [saver]);

  // A newer version from someone else (realtime refetches the page): taken in place when
  // nothing here is unsaved; with unsaved typing, the banner asks whose version stays.
  const { data: latest = page } = useQuery(q.wikiPage(page.id));
  const take = useCallback(
    (p: WikiPage) => {
      saver.rebase(p);
      editorRef.current?.setHtml(p.body_html, { silent: true });
    },
    [saver],
  );
  const newer = saver.isNewer(latest);
  useEffect(() => {
    if (!newer || saver.busy || saver.status === "conflict") return;
    take(latest);
  }, [newer, latest, saver, saver.status, take]);
  const theirs = saver.status === "conflict" || (newer && saver.status === "dirty");

  // Cmd/Ctrl+S saves now.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "s") {
        e.preventDefault();
        void saver.flush();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [saver]);

  const loadTheirs = async () => {
    try {
      const fresh = await api.wiki.get(page.id);
      qc.setQueryData(qk.wikiPage(page.id), fresh);
      take(fresh);
    } catch (e) {
      toast.error(e);
    }
  };
  const keepMine = async () => {
    try {
      const fresh = await api.wiki.get(page.id);
      qc.setQueryData(qk.wikiPage(page.id), fresh);
      saver.overwrite(fresh.version);
    } catch (e) {
      toast.error(e);
    }
  };

  const fileRef = useRef<HTMLInputElement>(null);
  const [uploading, setUploading] = useState(false);
  const upload = async (file: File) => {
    setUploading(true);
    try {
      const a = await api.attachments.upload(file);
      const src = a.url.replace(/&/g, "&amp;").replace(/"/g, "&quot;");
      const alt = file.name.replace(/\.[^.]+$/, "").replace(/[<>"]/g, "");
      editorRef.current?.insertHtml(`<img src="${src}" alt="${alt}">`);
    } catch (e) {
      toast.error(e, t("page.uploadFailed"));
    } finally {
      setUploading(false);
    }
  };

  const [iconOpen, setIconOpen] = useState(false);
  const [history, setHistory] = useState(false);
  const [moving, setMoving] = useState(false);
  const crumbs = tree.byId.has(page.id) ? ancestorsOf(tree, page.id) : page.breadcrumbs;
  const kids = tree.byId.has(page.id) ? childrenOf(tree, page.id) : page.children;
  const current = latest;
  const node = tree.byId.get(page.id) ?? nodeOf(current);

  const copyLink = () => {
    void copyText(pageUrl(page.id));
    toast.success(t("toast.linkCopied"));
  };

  const iconTrigger = (
    <button
      type="button"
      disabled={!canEdit}
      aria-label={draft.icon ? t("icon.change") : t("icon.add")}
      className={cn(
        "flex items-center justify-center rounded-lg outline-none transition-colors focus-visible:shadow-[0_0_0_2px_var(--focus)] disabled:pointer-events-none",
        draft.icon ? "size-14 text-[40px] leading-none hover:bg-hover" : "h-7 gap-1.5 px-2 text-[12.5px] text-fg-3 opacity-0 hover:bg-hover hover:text-fg group-hover/head:opacity-100 focus-visible:opacity-100 [@media(hover:none)]:opacity-100",
        !draft.icon && iconOpen && "opacity-100",
      )}
    >
      {draft.icon ? (
        <span aria-hidden>{draft.icon}</span>
      ) : (
        <>
          <SmilePlus className="size-3.5" />
          {t("icon.add")}
        </>
      )}
    </button>
  );

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <header className="flex h-12 shrink-0 items-center gap-1.5 border-b border-line px-3 sm:px-4">
        <PagesButton />
        <nav aria-label={t("page.breadcrumbs")} className="flex min-w-0 flex-1 items-center gap-1 text-[12.5px]">
          <Link to="/wiki" className="hidden shrink-0 rounded-md px-1.5 py-0.5 text-fg-3 transition-colors hover:bg-hover hover:text-fg sm:inline">
            {t("title")}
          </Link>
          {crumbs.map((c) => (
            <span key={c.id} className="hidden min-w-0 items-center gap-1 sm:flex">
              <span className="text-fg-4">/</span>
              <Link to="/wiki/$pageId" params={{ pageId: c.id }} className="flex min-w-0 max-w-[180px] items-center gap-1 rounded-md px-1.5 py-0.5 text-fg-3 transition-colors hover:bg-hover hover:text-fg">
                {c.icon && <span aria-hidden>{c.icon}</span>}
                <span className="truncate">{title(c.title)}</span>
              </Link>
            </span>
          ))}
          <span className="hidden text-fg-4 sm:inline">/</span>
          <span className="flex min-w-0 items-center gap-1 px-1.5 font-medium text-fg" aria-current="page">
            <PageIcon icon={draft.icon} />
            <span className="truncate">{shown}</span>
          </span>
          {canEdit && <SaveState saver={saver} />}
        </nav>
        <div className="flex shrink-0 items-center gap-0.5">
          <Tooltip content={t("page.copyLink")}>
            <Button square variant="ghost" size="sm" aria-label={t("page.copyLink")} onClick={copyLink}>
              <Link2 className="size-4" />
            </Button>
          </Tooltip>
          <Tooltip content={t("history.open")}>
            <Button square variant="ghost" size="sm" aria-label={t("history.open")} onClick={() => setHistory(true)}>
              <History className="size-4" />
            </Button>
          </Tooltip>
          {canEdit && (
            <Menu>
              <MenuTrigger asChild>
                <Button square variant="ghost" size="sm" aria-label={t("page.more")}>
                  <MoreHorizontal className="size-4" />
                </Button>
              </MenuTrigger>
              <MenuContent align="end">
                <MenuItem icon={<Plus />} onSelect={() => create.mutate({ parent_id: page.id })}>
                  {t("tree.addSubpage")}
                </MenuItem>
                <MenuItem icon={<FolderInput />} onSelect={() => setMoving(true)}>
                  {t("tree.moveTo")}
                </MenuItem>
                <MenuItem icon={<Link2 />} onSelect={copyLink}>
                  {t("page.copyLink")}
                </MenuItem>
                <MenuItem icon={<History />} onSelect={() => setHistory(true)}>
                  {t("history.open")}
                </MenuItem>
                <DeleteItem node={node} />
              </MenuContent>
            </Menu>
          )}
        </div>
      </header>

      {saver.final && saver.pending && (
        <UnsentBanner
          reason={saver.status === "forbidden" ? "forbidden" : "deleted"}
          draft={draft}
          onDiscard={() => {
            if (saver.status === "forbidden") return take(latest);
            forgetSaver(page.id);
            void navigate({ to: "/wiki" });
          }}
        />
      )}

      {theirs && (
        <div role="alert" className="flex flex-wrap items-center gap-x-3 gap-y-2 border-b border-[color-mix(in_srgb,var(--tone-amber-solid)_35%,var(--line))] bg-tone-amber-soft px-4 py-2.5 text-[12.5px] text-fg animate-rise-in motion-reduce:animate-none">
          <AlertTriangle className="size-4 shrink-0 text-tone-amber" />
          <span className="min-w-0 flex-1">
            <span className="font-medium">{latest.updated_by ? t("conflict.titleBy", { name: latest.updated_by.name }) : t("conflict.title")}</span>{" "}
            <span className="text-fg-2">{t("conflict.body")}</span>
          </span>
          <span className="flex shrink-0 gap-1.5">
            <Button size="sm" onClick={() => void loadTheirs()}>
              {t("conflict.loadTheirs")}
            </Button>
            <Button size="sm" variant="primary" onClick={() => void keepMine()}>
              {t("conflict.keepMine")}
            </Button>
          </span>
        </div>
      )}

      <div className="min-h-0 flex-1 overflow-y-auto">
        <article className="mx-auto w-full max-w-[760px] px-6 pb-32 pt-10 md:px-10 md:pt-14">
          <div className="group/head">
            <div className={cn("-ml-2", draft.icon ? "mb-2" : "mb-1 min-h-7")}>
              {canEdit ? (
                <IconPicker value={draft.icon} onChange={(icon) => saver.edit({ icon })} trigger={iconTrigger} open={iconOpen} onOpenChange={setIconOpen} />
              ) : draft.icon ? (
                <span className="flex size-14 items-center justify-center text-[40px] leading-none" aria-hidden>
                  {draft.icon}
                </span>
              ) : null}
            </div>
            <textarea
              ref={titleRef}
              value={draft.title}
              onChange={(e) => saver.edit({ title: e.target.value.replace(/\n/g, " ") })}
              onKeyDown={(e) => {
                if (e.key === "Enter" || (e.key === "ArrowDown" && e.currentTarget.selectionStart === e.currentTarget.value.length)) {
                  e.preventDefault();
                  editorRef.current?.editor?.commands.focus("start");
                }
              }}
              readOnly={!canEdit}
              rows={1}
              maxLength={MAX_TITLE}
              placeholder={t("untitled")}
              aria-label={ui("title")}
              className="field-sizing-content w-full resize-none bg-transparent text-[30px] font-semibold leading-[36px] tracking-[-0.03em] text-fg outline-none placeholder:text-fg-4 focus-visible:shadow-none"
            />
            <EditedLine page={current} />
          </div>

          {canEdit && editor && (
            <div className="sticky top-3 z-10 -mx-3 mt-6 rounded-lg border border-line bg-surface/95 pt-1.5 shadow-xs backdrop-blur">
              <Toolbar editor={editor} full onImage={() => fileRef.current?.click()} end={uploading ? <span className="pr-2 text-xs text-fg-4">{t("page.uploading")}</span> : undefined} />
            </div>
          )}
          <RichEditor
            ref={editorRef}
            value={draft.body_html}
            onChange={canEdit ? (html) => saver.edit({ body_html: html }) : undefined}
            toolbar="none"
            editable={canEdit}
            placeholder={canEdit ? t("page.bodyPlaceholder") : t("page.bodyEmpty")}
            ariaLabel={t("page.body")}
            className="mt-5"
            contentClassName="prose-lg min-h-[280px] px-0"
          />
          <input
            ref={fileRef}
            type="file"
            accept="image/*"
            hidden
            onChange={(e) => {
              const f = e.target.files?.[0];
              if (f) void upload(f);
              e.target.value = "";
            }}
          />

          {(kids.length > 0 || canEdit) && (
            <section className="mt-14 border-t border-line-soft pt-6" aria-labelledby="wiki-subpages">
              <h2 id="wiki-subpages" className="label-caps mb-2">
                {t("page.subpages")}
              </h2>
              <div className="flex flex-col gap-px">
                {kids.map((k) => (
                  <Link
                    key={k.id}
                    to="/wiki/$pageId"
                    params={{ pageId: k.id }}
                    className="-mx-2 flex h-8 items-center gap-2 rounded-md px-2 text-[13px] text-fg-2 outline-none transition-colors hover:bg-hover hover:text-fg focus-visible:bg-hover"
                  >
                    <PageIcon icon={k.icon} />
                    <span className={cn("truncate", !k.title.trim() && "text-fg-3")}>{title(k.title)}</span>
                  </Link>
                ))}
                {canEdit && (
                  <button
                    type="button"
                    disabled={create.isPending}
                    onClick={() => create.mutate({ parent_id: page.id })}
                    className="-mx-2 flex h-8 items-center gap-2 rounded-md px-2 text-[13px] text-fg-3 outline-none transition-colors hover:bg-hover hover:text-fg focus-visible:bg-hover disabled:opacity-60"
                  >
                    <Plus className="size-[14px]" />
                    {t("page.addSubpage")}
                  </button>
                )}
              </div>
            </section>
          )}
        </article>
      </div>

      <HistoryDrawer
        page={current}
        open={history}
        onOpenChange={setHistory}
        canRestore={canEdit}
        onRestored={take}
      />
      <MoveDialog node={node} open={moving} onOpenChange={setMoving} />
    </div>
  );
}

/** Saving…, Saved, or what went wrong, beside the breadcrumbs. */
function SaveState({ saver }: { saver: PageSaver }) {
  const { t } = useTranslation("wiki");
  const s = saver.status;
  let body: ReactNode = null;
  if (s === "saving" || s === "dirty")
    body = (
      <>
        <Spinner className="size-3" />
        {t("save.saving")}
      </>
    );
  else if (s === "error")
    body = (
      <>
        <CloudOff className="size-3 text-tone-amber" />
        {t("save.retrying")}
      </>
    );
  else if (s === "conflict" || s === "forbidden" || s === "deleted")
    body = (
      <>
        <AlertTriangle className="size-3 text-tone-amber" />
        {t("save.notSaved")}
      </>
    );
  else if (saver.lastSavedAt)
    body = (
      <>
        <Check className="size-3 text-tone-green" strokeWidth={2.5} />
        {t("save.saved")}
      </>
    );
  return (
    <span className="ml-1 hidden shrink-0 items-center text-xs text-fg-4 sm:inline-flex" aria-live="polite">
      <Morph k={s === "dirty" ? "saving" : s === "saved" && !saver.lastSavedAt ? "idle" : s} className="items-center gap-1.5">
        {body}
      </Morph>
    </span>
  );
}

/** "Last edited by Maya, 3 min ago": its own clock, so only this line redraws. */
function EditedLine({ page }: { page: WikiPage }) {
  const { t } = useTranslation("wiki");
  const now = useNow();
  const who = page.updated_by;
  const when = relLong(page.updated_at, now);
  return (
    <p className="mt-2 flex items-center gap-1.5 text-[12.5px] text-fg-3" title={fullDate(page.updated_at)}>
      {who && <Avatar name={who.name} src={who.avatar_url} size="xs" />}
      {who ? t("page.editedBy", { name: who.name, when }) : t("page.edited", { when })}
    </p>
  );
}
