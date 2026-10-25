import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { GitMerge } from "lucide-react";
import { useDeferredValue, useState } from "react";
import { useTranslation } from "react-i18next";
import type { Ticket } from "@/lib/api/types";
import { api } from "@/lib/api/endpoints";
import { errorMessage } from "@/lib/api/client";
import { relShort } from "@/lib/format";
import { cn } from "@/lib/cn";
import { Dialog } from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { SearchInput } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { toast } from "@/components/ui/toast";
import { StatusIcon } from "../bits";
import { useMergeTicket } from "../useTicketActions";
import { EmptyState } from "@/components/ui/empty";
import { qk } from "@/lib/queries";

export function MergeDialog({ ticket, open, onOpenChange }: { ticket: Ticket; open: boolean; onOpenChange: (o: boolean) => void }) {
  const { t: tr } = useTranslation("inbox");
  const { t } = useTranslation("indicators");
  const [term, setTerm] = useState("");
  const deferred = useDeferredValue(term.trim());
  const [target, setTarget] = useState<Ticket | null>(null);
  const [error, setError] = useState<string | null>(null);
  const merge = useMergeTicket();
  const navigate = useNavigate();

  // Default to the same customer's other tickets; search everything when typing.
  const results = useQuery({
    queryKey: qk.of("merge-search", ticket.id, deferred),
    queryFn: () =>
      deferred
        ? api.tickets.list({ q: deferred, limit: 10 })
        : api.tickets.list({ contact_id: ticket.contact?.id, limit: 10, status: ["open", "pending", "on_hold", "solved"] }),
    enabled: open,
  });
  const list = (results.data?.data ?? []).filter((t) => t.id !== ticket.id);

  const run = () => {
    if (!target) return;
    setError(null);
    merge.mutate(
      { id: ticket.id, into: target.id },
      {
        onSuccess: () => {
          toast.success(tr("merge.done", { number: target.number }));
          onOpenChange(false);
          void navigate({ to: "/inbox/$folder/{-$ticketId}", params: { folder: "open", ticketId: target.id } });
        },
        onError: (e) => setError(errorMessage(e)),
      },
    );
  };

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={tr("merge.title", { number: ticket.number })}
      description={tr("merge.description")}
      footer={
        <>
          {error && <p className="mr-auto text-xs text-danger-fg">{error}</p>}
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            {tr("common.cancel")}
          </Button>
          <Button variant="primary" icon={<GitMerge className="size-3.5" />} disabled={!target} loading={merge.isPending} onClick={run}>
            {target ? tr("merge.into", { number: target.number }) : tr("merge.merge")}
          </Button>
        </>
      }
    >
      <SearchInput value={term} onChange={setTerm} placeholder={t("tickets.mergeSearch")} inputSize="md" autoFocus />
      <div className="mt-3 flex min-h-[220px] flex-col gap-0.5">
        {!deferred && <p className="label-caps px-1 pb-1">{ticket.contact ? tr("merge.fromContact", { name: ticket.contact.name || ticket.contact.email }) : tr("merge.recent")}</p>}
        {results.isPending ? (
          <div className="flex flex-col gap-0.5" aria-busy="true">
            {[0, 1, 2, 3].map((i) => (
              <div key={i} className="flex h-10 items-center gap-2.5 px-2.5" style={{ opacity: 1 - i * 0.2 }}>
                <Skeleton className="size-3.5 rounded-full" />
                <Skeleton className="h-2.5 w-10" />
                <Skeleton className="h-2.5 flex-1" />
                <Skeleton className="h-2.5 w-8" />
              </div>
            ))}
          </div>
        ) : !list.length ? (
          <EmptyState compact lit={2} title={tr("merge.none")} description={tr("merge.noneHint")} />
        ) : (
          list.map((t) => (
            <button
              key={t.id}
              type="button"
              onClick={() => setTarget(t)}
              aria-pressed={target?.id === t.id}
              className={cn(
                "flex h-10 items-center gap-2.5 rounded-md px-2.5 text-left text-[12.5px] outline-none transition-[background-color,box-shadow] duration-150 hover:bg-hover focus-visible:shadow-[0_0_0_2px_var(--focus)] active:scale-[0.995]",
                target?.id === t.id && "bg-active shadow-[inset_0_0_0_1px_var(--line-strong)]",
              )}
            >
              <StatusIcon status={t.status} />
              <span className="mono w-12 shrink-0 text-fg-3">#{t.number}</span>
              <span className="min-w-0 flex-1 truncate" data-ph-mask>
                {t.subject}
              </span>
              <span className="shrink-0 truncate text-xs text-fg-3" data-ph-mask>
                {t.contact?.name}
              </span>
              <span className="mono w-8 shrink-0 text-right text-[11px] text-fg-4">{relShort(t.last_message_at)}</span>
            </button>
          ))
        )}
      </div>
    </Dialog>
  );
}
