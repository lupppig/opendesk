// The team's discussion of a conversation: comments only teammates who can see it
// read, never the sender. @ mentions a teammate (the ticket notes' mention list);
// a mention of someone who cannot see it shares the conversation with them.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Lock, MoreHorizontal, Pencil, Trash2 } from "lucide-react";
import { useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { api } from "@/lib/api/endpoints";
import type { MailComment, MailThreadDetail, Member } from "@/lib/api/types";
import { isBlankHtml, mentionIds } from "@/lib/collab/html";
import { fullDate, relShort } from "@/lib/format";
import { cn } from "@/lib/cn";
import { qk, q } from "@/lib/queries";
import { sanitizeHtml } from "@/lib/sanitize";
import { atLeast, useRole, useSession } from "@/lib/session";
import { RichEditor, type RichEditorHandle } from "@/components/editor/RichEditor";
import { mentionExtension, SuggestionPopup } from "@/components/tickets/detail/suggestions";
import { Avatar } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/ui/confirm";
import { Menu, MenuContent, MenuItem, MenuTrigger } from "@/components/ui/menu";
import { toast } from "@/components/ui/toast";
import { Tooltip } from "@/components/ui/tooltip";

function useMentionExtension() {
  const { data: members } = useQuery(q.members());
  const ref = useRef<Member[]>([]);
  ref.current = members ?? [];
  // One extension per editor; it reads the members as they are when @ is typed.
  return useMemo(() => [mentionExtension(() => ref.current)], []);
}

function patchComments(qc: ReturnType<typeof useQueryClient>, threadId: string, fn: (c: MailComment[]) => MailComment[]) {
  qc.setQueryData<MailThreadDetail>(qk.mailThread(threadId), (old) => {
    if (!old) return old;
    const comments = fn(old.comments);
    return { ...old, comments, thread: { ...old.thread, comment_count: comments.length } };
  });
}

function CommentItem({ c, threadId, canAct }: { c: MailComment; threadId: string; canAct: boolean }) {
  const { t } = useTranslation("collab");
  const qc = useQueryClient();
  const confirm = useConfirm();
  const me = useSession((s) => s.user?.id);
  const admin = atLeast(useRole(), "admin");
  const mine = !!c.author && c.author.id === me;
  const [editing, setEditing] = useState(false);
  const [html, setHtml] = useState(c.body_html);
  const extensions = useMentionExtension();
  const editor = useRef<RichEditorHandle>(null);
  const body = useMemo(() => sanitizeHtml(c.body_html), [c.body_html]);
  const name = c.author?.name ?? t("comments.someone");

  const save = useMutation({
    mutationFn: (body_html: string) => api.collab.comments.update(c.id, body_html),
    onSuccess: (n) => {
      patchComments(qc, threadId, (list) => list.map((x) => (x.id === n.id ? n : x)));
      setEditing(false);
    },
    onError: (e) => toast.error(e),
  });
  const remove = () =>
    confirm.show(
      t("comments.deleteConfirm"),
      async () => {
        await api.collab.comments.remove(c.id);
        patchComments(qc, threadId, (list) => list.filter((x) => x.id !== c.id));
      },
      { title: t("comments.deleteTitle"), confirmLabel: t("comments.delete"), danger: true },
    );

  return (
    <li className="group/comment flex gap-2.5">
      <Avatar name={name} src={c.author?.avatar_url} size="sm" className="mt-0.5" />
      <div className="min-w-0 flex-1">
        <div className="flex items-baseline gap-2">
          <span className="truncate text-[12.5px] font-medium">{name}</span>
          <Tooltip content={fullDate(c.created_at)}>
            <time dateTime={c.created_at} className="mono shrink-0 text-[10.5px] text-fg-4">
              {relShort(c.created_at)}
            </time>
          </Tooltip>
          {c.updated_at !== c.created_at && <span className="text-[10.5px] text-fg-4">{t("comments.edited")}</span>}
          {canAct && (mine || admin) && !editing && (
            <Menu>
              <MenuTrigger asChild>
                <button
                  type="button"
                  aria-label={t("comments.menu")}
                  className="ml-auto flex size-6 items-center justify-center rounded-md text-fg-4 opacity-0 transition-opacity hover:bg-hover hover:text-fg focus-visible:opacity-100 group-hover/comment:opacity-100 data-[state=open]:opacity-100"
                >
                  <MoreHorizontal className="size-3.5" />
                </button>
              </MenuTrigger>
              <MenuContent>
                {mine && (
                  <MenuItem icon={<Pencil />} onSelect={() => setEditing(true)}>
                    {t("comments.edit")}
                  </MenuItem>
                )}
                <MenuItem icon={<Trash2 />} danger onSelect={remove}>
                  {t("comments.delete")}
                </MenuItem>
              </MenuContent>
            </Menu>
          )}
        </div>
        {editing ? (
          <div className="mt-1 rounded-md border border-line bg-surface">
            <RichEditor
              ref={editor}
              value={html}
              onChange={setHtml}
              extensions={extensions}
              autofocus
              toolbar="none"
              contentClassName="min-h-[40px] text-[12.5px]"
              ariaLabel={t("comments.edit")}
              onSubmit={() => !isBlankHtml(html) && save.mutate(html)}
            />
            <div className="flex justify-end gap-1.5 px-2 pb-2">
              <Button size="xs" variant="ghost" onClick={() => setEditing(false)}>
                {t("comments.cancel")}
              </Button>
              <Button size="xs" variant="primary" loading={save.isPending} disabled={isBlankHtml(html)} onClick={() => save.mutate(html)}>
                {t("comments.save")}
              </Button>
            </div>
          </div>
        ) : (
          <div className="prose-od mt-0.5 text-[12.5px] text-fg" data-ph-mask dangerouslySetInnerHTML={{ __html: body }} />
        )}
      </div>
    </li>
  );
}

export function Comments({ detail }: { detail: MailThreadDetail }) {
  const { t } = useTranslation("collab");
  const qc = useQueryClient();
  const threadId = detail.thread.id;
  const [html, setHtml] = useState("");
  const editor = useRef<RichEditorHandle>(null);
  const extensions = useMentionExtension();
  const add = useMutation({
    mutationFn: (body_html: string) => api.collab.threads.comment(threadId, { body_html, mentions: mentionIds(body_html) }),
    onSuccess: (c) => {
      patchComments(qc, threadId, (list) => (list.some((x) => x.id === c.id) ? list : [...list, c]));
      editor.current?.clear();
      setHtml("");
    },
    onError: (e) => toast.error(e),
  });
  const submit = () => {
    const body = editor.current?.editor?.getHTML() ?? html;
    if (!isBlankHtml(body) && !add.isPending) add.mutate(body);
  };

  return (
    <section aria-label={t("comments.title")} className="rounded-lg border border-dashed border-note-line bg-note px-4 py-3">
      <div className="flex items-center gap-1.5">
        <Lock className="size-3 text-fg-3" />
        <h3 className="text-[12px] font-semibold tracking-[-0.01em]">{t("comments.title")}</h3>
        <span className="mono text-[10.5px] text-fg-4">{detail.comments.length || ""}</span>
      </div>
      <p className="mt-0.5 text-[11.5px] text-fg-3">{t("comments.hint")}</p>
      {detail.comments.length > 0 ? (
        <ul className="mt-3 flex flex-col gap-3">
          {detail.comments.map((c) => (
            <CommentItem key={c.id} c={c} threadId={threadId} canAct={detail.can_act} />
          ))}
        </ul>
      ) : null}
      {detail.can_act && (
        <div className={cn("mt-3 rounded-md border border-line bg-surface focus-within:border-line-strong")}>
          <RichEditor
            ref={editor}
            value={html}
            onChange={setHtml}
            extensions={extensions}
            toolbar="none"
            placeholder={t("comments.placeholder")}
            ariaLabel={t("comments.placeholder")}
            contentClassName="min-h-[40px] text-[12.5px]"
            onSubmit={submit}
          />
          <div className="flex justify-end px-2 pb-2">
            <Tooltip content={t("comments.add")} shortcut="mod enter">
              <Button size="xs" variant="primary" loading={add.isPending} disabled={isBlankHtml(html)} onClick={submit}>
                {t("comments.add")}
              </Button>
            </Tooltip>
          </div>
        </div>
      )}
      <SuggestionPopup />
    </section>
  );
}
