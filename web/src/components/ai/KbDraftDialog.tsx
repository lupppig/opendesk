// "Draft article from tickets" (contracts/ai.md, POST /v1/kb/articles/ai/draft,
// about $0.05 on the default model). Pick conversations and/or describe a topic; the draft is handed
// back to the editor, never saved here. Progress shows as task rows adapted
// from Beautiful UI (MIT, see web/THIRD_PARTY_NOTICES.md and ./ui/TaskRows).

import { useMutation, useQuery } from "@tanstack/react-query";
import { Plus, X } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Label, TextInput } from "@/components/ui/input";
import { Picker } from "@/components/ui/picker";
import { StatusIcon } from "@/components/tickets/bits";
import { api } from "@/lib/api/endpoints";
import { errorMessage, isPlanLimit } from "@/lib/api/client";
import type { KBArticleDraft, Ticket } from "@/lib/api/types";
import { AiIcon, useCostLabel } from "./bits";
import { TaskRows, type TaskRow, type TaskStatus } from "./ui/TaskRows";
import { qk } from "@/lib/queries";

/** The server drafts from at most 10 tickets. */
const MAX_TICKETS = 10;

export function KbDraftDialog({
  open,
  onOpenChange,
  onDraft,
  initialTickets = [],
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  onDraft: (d: KBArticleDraft) => void;
  initialTickets?: Ticket[];
}) {
  const { t } = useTranslation("ai");
  const { t: tc } = useTranslation();
  const cost = useCostLabel()("kb_draft");
  const [picked, setPicked] = useState<Ticket[]>(initialTickets);
  const [topic, setTopic] = useState("");
  const [query, setQuery] = useState("");
  const [tried, setTried] = useState(false);
  // Which step the progress rows show as running; -1 before the first run.
  const [stage, setStage] = useState(-1);

  const search = useQuery({
    queryKey: qk.of("ai", "kb-draft-tickets", query),
    queryFn: () => api.tickets.list({ q: query.trim() || undefined, limit: 8, spam: false }),
    enabled: open,
    staleTime: 30_000,
  });
  const results = search.data?.data ?? [];
  const known = new Map([...picked, ...results].map((x) => [x.id, x]));

  const draft = useMutation({
    mutationFn: () => api.ai.kbDraft({ ticket_ids: picked.length ? picked.map((x) => x.id) : undefined, topic: topic.trim() || undefined }),
    onSuccess: (d) => {
      onDraft(d);
      onOpenChange(false);
      setPicked([]);
      setTopic("");
      setTried(false);
      setStage(-1);
    },
  });

  const pending = draft.isPending;
  useEffect(() => {
    if (!pending) return;
    // The server answers once; the first two steps settle on a gentle clock and the
    // last one stays running until the draft arrives.
    const a = setTimeout(() => setStage(1), 900);
    const b = setTimeout(() => setStage(2), 2200);
    return () => {
      clearTimeout(a);
      clearTimeout(b);
    };
  }, [pending]);

  const empty = !picked.length && !topic.trim();
  const submit = () => {
    setTried(true);
    if (empty || draft.isPending) return;
    setStage(0);
    draft.mutate();
  };

  const failed = !!draft.error && !pending;
  const statusAt = (i: number): TaskStatus => (i < stage ? "done" : i > stage ? "pending" : failed ? "failed" : "running");
  const steps: TaskRow[] = [
    picked.length
      ? { key: "read", label: t("kb_draft.step_read", { count: picked.length }), meta: picked.length, status: statusAt(0) }
      : { key: "read", label: t("kb_draft.step_topic"), status: statusAt(0) },
    { key: "find", label: t("kb_draft.step_find"), status: statusAt(1) },
    { key: "write", label: t("kb_draft.step_write"), status: statusAt(2) },
  ];
  const showSteps = stage >= 0 && (pending || failed);

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => !draft.isPending && onOpenChange(o)}
      title={
        <span className="flex items-center gap-2">
          <AiIcon />
          {t("kb_draft.title")}
        </span>
      }
      description={t("kb_draft.description")}
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={draft.isPending}>
            {tc("actions.cancel")}
          </Button>
          <Button variant="primary" onClick={submit} loading={draft.isPending} icon={<AiIcon />}>
            {draft.isPending ? t("kb_draft.generating") : t("kb_draft.generate")}
            {cost && !draft.isPending && <span className="mono ml-1 text-[10.5px] opacity-60">{cost}</span>}
          </Button>
        </>
      }
    >
      <form
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          submit();
        }}
      >
        <div className="flex flex-col gap-1.5">
          <Label>{t("kb_draft.tickets")}</Label>
          <div className="flex min-h-9 flex-wrap items-center gap-1.5 rounded-md border border-line px-1.5 py-1.5">
            {picked.map((x) => (
              <span key={x.id} className="inline-flex h-6 max-w-[260px] items-center gap-1.5 rounded-sm border border-line bg-sunken pl-1.5 pr-0.5 text-[12px]">
                <span className="mono text-[11px] text-fg-3">#{x.number}</span>
                <span className="truncate text-fg-2">{x.subject}</span>
                <button
                  type="button"
                  aria-label={t("kb_draft.remove_ticket", { subject: x.subject })}
                  onClick={() => setPicked((p) => p.filter((y) => y.id !== x.id))}
                  className="flex size-5 items-center justify-center rounded text-fg-4 hover:bg-hover hover:text-fg"
                >
                  <X className="size-3" />
                </button>
              </span>
            ))}
            <Picker
              multi
              values={picked.map((x) => x.id)}
              onChange={(ids) => setPicked(ids.map((id) => known.get(id)).filter((x): x is Ticket => !!x).slice(0, MAX_TICKETS))}
              onQueryChange={setQuery}
              placeholder={t("kb_draft.tickets_placeholder")}
              emptyText={search.isFetching ? "…" : t("kb_draft.tickets_empty")}
              width={380}
              items={results.map((x) => ({
                value: x.id,
                label: x.subject,
                icon: <StatusIcon status={x.status} />,
                hint: <span className="mono">#{x.number}</span>,
              }))}
              trigger={
                <button type="button" className="inline-flex h-6 items-center gap-1 rounded-sm px-1.5 text-[12px] text-fg-3 hover:bg-hover hover:text-fg">
                  <Plus className="size-3" />
                  {t("kb_draft.add_ticket")}
                </button>
              }
            />
          </div>
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="kb-draft-topic">{t("kb_draft.topic")}</Label>
          <TextInput id="kb-draft-topic" value={topic} maxLength={200} placeholder={t("kb_draft.topic_placeholder")} onChange={(e) => setTopic(e.target.value)} />
        </div>
        {showSteps && (
          <TaskRows
            rows={steps}
            labels={{ pending: t("kb_draft.status_pending"), running: t("kb_draft.status_running"), done: t("kb_draft.status_done"), failed: t("kb_draft.status_failed") }}
          />
        )}
        {tried && empty && <p className="text-[12.5px] text-danger-fg">{t("kb_draft.need_input")}</p>}
        {draft.error && !isPlanLimit(draft.error) && <p className="text-[12.5px] text-danger-fg">{errorMessage(draft.error)}</p>}
        <button type="submit" hidden />
      </form>
    </Dialog>
  );
}
