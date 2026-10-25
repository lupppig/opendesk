// Past conversations: open one, rename it in place or delete it.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { MessageSquare, Pencil, Trash2 } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useConfirm } from "@/components/ui/confirm";
import { toast } from "@/components/ui/toast";
import { cn } from "@/lib/cn";
import { assistantApi, type AssistantSession } from "./api";
import { ollie, sessionsKey, useOllie } from "./store";

function when(iso: string) {
  const d = new Date(iso);
  const diff = Date.now() - d.getTime();
  if (diff < 60_000) return "now";
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m`;
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h`;
  if (diff < 7 * 86_400_000) return `${Math.floor(diff / 86_400_000)}d`;
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

function Row({ s, active }: { s: AssistantSession; active: boolean }) {
  const { t } = useTranslation("assistant");
  const qc = useQueryClient();
  const confirm = useConfirm();
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState(s.title);
  const rename = useMutation({
    mutationFn: (v: string) => assistantApi.rename(s.id, v),
    onSuccess: (r) => {
      if (useOllie.getState().sessionId === s.id) useOllie.setState({ title: r.title });
      void qc.invalidateQueries({ queryKey: sessionsKey() });
    },
    onError: (e) => toast.error(e),
  });
  const remove = () =>
    confirm.show(
      t("deleteConfirm"),
      async () => {
        await assistantApi.remove(s.id);
        if (useOllie.getState().sessionId === s.id) ollie.newChat();
        void qc.invalidateQueries({ queryKey: sessionsKey() });
      },
      { title: t("deleteTitle"), confirmLabel: t("delete"), danger: true },
    );
  const commit = () => {
    setEditing(false);
    const v = title.trim();
    if (v && v !== s.title) rename.mutate(v);
    else setTitle(s.title);
  };

  return (
    <li className={cn("group relative flex h-9 items-center gap-2 rounded-lg px-2 transition-colors", active ? "bg-active" : "hover:bg-hover")}>
      <MessageSquare className="size-3.5 shrink-0 text-fg-4" />
      {editing ? (
        <input
          autoFocus
          value={title}
          maxLength={120}
          aria-label={t("rename")}
          onChange={(e) => setTitle(e.target.value)}
          onBlur={commit}
          onKeyDown={(e) => {
            if (e.key === "Enter") commit();
            if (e.key === "Escape") {
              e.stopPropagation();
              setTitle(s.title);
              setEditing(false);
            }
          }}
          className="h-6 min-w-0 flex-1 rounded-md border border-line-strong bg-surface px-1.5 text-[12.5px] text-fg outline-none"
        />
      ) : (
        <button type="button" onClick={() => void ollie.load(s.id)} className="min-w-0 flex-1 truncate text-left text-[12.5px] text-fg-2 after:absolute after:inset-0 group-hover:text-fg" data-ph-mask>
          {s.title || t("untitled")}
        </button>
      )}
      {!editing && (
        <>
          <span className="shrink-0 text-[11px] tabular-nums text-fg-4 group-focus-within:hidden group-hover:hidden">{when(s.updated_at)}</span>
          <span className="relative z-10 hidden shrink-0 items-center group-focus-within:flex group-hover:flex">
            <button type="button" aria-label={t("rename")} onClick={() => setEditing(true)} className="flex size-6 items-center justify-center rounded-md text-fg-3 hover:bg-active hover:text-fg">
              <Pencil className="size-3" />
            </button>
            <button type="button" aria-label={t("delete")} onClick={remove} className="flex size-6 items-center justify-center rounded-md text-fg-3 hover:bg-active hover:text-danger-fg">
              <Trash2 className="size-3" />
            </button>
          </span>
        </>
      )}
    </li>
  );
}

export function SessionList() {
  const { t } = useTranslation("assistant");
  const active = useOllie((s) => s.sessionId);
  const { data, isLoading } = useQuery({ queryKey: sessionsKey(), queryFn: assistantApi.sessions, staleTime: 30_000 });
  const rows = data?.data ?? [];
  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-y-auto px-2 py-2">
      <p className="px-2 pb-1.5 pt-1 text-[11px] font-medium uppercase tracking-[0.04em] text-fg-4">{t("history")}</p>
      {isLoading ? (
        <div className="flex flex-col gap-1 px-2">
          {[0, 1, 2].map((i) => (
            <div key={i} className="h-7 animate-pulse-soft rounded-md bg-sunken" />
          ))}
        </div>
      ) : rows.length === 0 ? (
        <p className="px-2 py-6 text-center text-[12.5px] text-fg-4">{t("historyEmpty")}</p>
      ) : (
        <ul className="flex flex-col gap-px">
          {rows.map((s) => (
            <Row key={s.id} s={s} active={s.id === active} />
          ))}
        </ul>
      )}
    </div>
  );
}
