// The Everywhere lists (contracts/api.md "Workspaces together"): tickets assigned to me
// and all mail, across every workspace the person shows, newest activity first. Each
// workspace's rows come from its own request (its role, its private teams), keep its
// badge, and open in that workspace: the focus moves there first.

import { useQueries, useQuery } from "@tanstack/react-query";
import { useParams } from "@tanstack/react-router";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { Mail, Paperclip, RotateCw, User } from "lucide-react";
import { useTranslation } from "react-i18next";
import { PageHeader } from "@/components/layout";
import { PriorityIcon, StatusPill } from "@/components/indicators";
import { EmptyState } from "@/components/ui/empty";
import { Skeleton } from "@/components/ui/skeleton";
import { WorkspaceBadge } from "@/components/workspaces/WorkspaceBadge";
import { inWorkspace } from "@/lib/api/client";
import type { MailThread, Membership, Ticket } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { relShort } from "@/lib/format";
import { springs } from "@/lib/motion";
import { api } from "@/lib/api/endpoints";
import { q, qk } from "@/lib/queries";
import { mergeByActivity, useOpenInWorkspace, useTogether, type Tagged } from "@/lib/together";

/** Rows each workspace contributes: its first page, newest first. */
const PER_WORKSPACE = 50;

type List = "mine" | "mail";

export default function EverywherePage() {
  const { list } = useParams({ strict: false }) as { list?: List };
  const { t } = useTranslation("together");
  const { shown, active } = useTogether();
  const workspaces = (active ? shown : shown.slice(0, 1)).filter((m) => !m.suspended);
  const mine = list === "mine";
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PageHeader
        icon={mine ? <User /> : <Mail />}
        title={mine ? t("everywhere.mineTitle") : t("everywhere.mailTitle")}
        subtitle={<WorkspaceStack workspaces={workspaces} />}
      />
      {mine ? <MineList workspaces={workspaces} /> : <MailList workspaces={workspaces} />}
    </div>
  );
}

/** The badges of the workspaces the list gathers. */
function WorkspaceStack({ workspaces }: { workspaces: Membership[] }) {
  const { t } = useTranslation("together");
  return (
    <span className="flex items-center gap-1" aria-label={t("everywhere.workspaces")}>
      {workspaces.map((m) => (
        <WorkspaceBadge key={m.workspace.id} name={m.workspace.name} logo={m.workspace.avatar_url} size="sm" />
      ))}
    </span>
  );
}

function useWorkspaceMap(workspaces: Membership[]) {
  return new Map(workspaces.map((m) => [m.workspace.id, m.workspace]));
}

function MineList({ workspaces }: { workspaces: Membership[] }) {
  const { t } = useTranslation("together");
  const results = useQueries({
    queries: workspaces.map((m) =>
      inWorkspace(m.workspace.id, () => ({
        queryKey: qk.of("tickets", "everywhere"),
        queryFn: () => api.tickets.list({ status: ["open", "pending", "on_hold"], assignee: "me", sort: "last_message_at", order: "desc", limit: PER_WORKSPACE }),
        staleTime: 15_000,
      })),
    ),
  });
  const rows = mergeByActivity(
    workspaces.map((m, i) => ({ workspaceId: m.workspace.id, rows: results[i]?.data?.data ?? [] })),
    (x: Ticket) => x.last_message_at,
  );
  return (
    <Merged
      rows={rows}
      loading={results.some((r) => r.isPending)}
      failed={workspaces.filter((_, i) => results[i]?.isError)}
      retry={() => results.forEach((r) => r.isError && void r.refetch())}
      workspaces={workspaces}
      empty={<EmptyState art="mine" title={t("everywhere.emptyMine")} description={t("everywhere.emptyMineHint")} />}
      render={(x, ws) => <TicketLine t={x} ws={ws} />}
    />
  );
}

function MailList({ workspaces }: { workspaces: Membership[] }) {
  const { t } = useTranslation("together");
  // Collab is a plan's: only the workspaces whose overview has mail numbers have it.
  const { data: overview } = useQuery(q.overview());
  const withMail = workspaces.filter((m) => overview?.find((o) => o.id === m.workspace.id)?.mail_counts);
  const results = useQueries({
    queries: withMail.map((m) =>
      inWorkspace(m.workspace.id, () => ({
        queryKey: qk.of("collab", "everywhere"),
        queryFn: () => api.collab.threads.list({ box: "inbox", limit: PER_WORKSPACE }),
        staleTime: 15_000,
      })),
    ),
  });
  if (overview && !withMail.length) return <EmptyState art="inbox" title={t("everywhere.noMail")} description={t("everywhere.noMailHint")} />;
  const rows = mergeByActivity(
    withMail.map((m, i) => ({ workspaceId: m.workspace.id, rows: results[i]?.data?.data ?? [] })),
    (x: MailThread) => x.last_message_at,
  );
  return (
    <Merged
      rows={rows}
      loading={!overview || results.some((r) => r.isPending)}
      failed={withMail.filter((_, i) => results[i]?.isError)}
      retry={() => results.forEach((r) => r.isError && void r.refetch())}
      workspaces={withMail}
      empty={<EmptyState art="inbox" title={t("everywhere.emptyMail")} description={t("everywhere.emptyMailHint")} />}
      render={(x, ws) => <ThreadLine th={x} ws={ws} />}
    />
  );
}

function Merged<T extends { id: string }>({
  rows,
  loading,
  failed,
  retry,
  workspaces,
  empty,
  render,
}: {
  rows: Tagged<T>[];
  loading: boolean;
  failed: Membership[];
  retry: () => void;
  workspaces: Membership[];
  empty: React.ReactNode;
  render: (row: T, ws: Membership["workspace"]) => React.ReactNode;
}) {
  const { t } = useTranslation("together");
  const reduce = useReducedMotion();
  const byId = useWorkspaceMap(workspaces);
  if (loading && !rows.length)
    return (
      <div className="flex flex-col" aria-busy="true">
        {[0, 1, 2, 3, 4].map((i) => (
          <div key={i} className="flex h-14 items-center gap-3 border-b border-line-soft px-5">
            <Skeleton className="size-5 rounded-sm" />
            <div className="flex flex-1 flex-col gap-2">
              <Skeleton className="h-2.5 w-1/2" />
              <Skeleton className="h-2 w-1/3 opacity-60" />
            </div>
          </div>
        ))}
      </div>
    );
  return (
    <div className="min-h-0 flex-1 overflow-y-auto">
      {failed.map((m) => (
        <div key={m.workspace.id} className="flex items-center gap-2 border-b border-line-soft bg-tone-amber-soft px-5 py-2 text-[12px] text-tone-amber">
          <WorkspaceBadge name={m.workspace.name} logo={m.workspace.avatar_url} size="sm" decorative />
          <span className="flex-1">{t("everywhere.failed", { name: m.workspace.name })}</span>
          <button type="button" onClick={retry} className="inline-flex items-center gap-1 rounded-sm px-1.5 py-0.5 hover:bg-hover [&_svg]:size-3.5">
            <RotateCw aria-hidden />
            {t("everywhere.retry")}
          </button>
        </div>
      ))}
      {!rows.length ? (
        empty
      ) : (
        <ul>
          <AnimatePresence initial={false}>
            {rows.map(({ workspaceId, row }) => {
              const ws = byId.get(workspaceId);
              if (!ws) return null;
              return (
                <motion.li
                  key={`${workspaceId}:${row.id}`}
                  layout={reduce ? false : "position"}
                  initial={reduce ? false : { opacity: 0, y: -4 }}
                  animate={{ opacity: 1, y: 0 }}
                  exit={reduce ? undefined : { opacity: 0 }}
                  transition={reduce ? { duration: 0 } : springs.row}
                  className="border-b border-line-soft"
                >
                  {render(row, ws)}
                </motion.li>
              );
            })}
          </AnimatePresence>
        </ul>
      )}
      {rows.length > 0 && <p className="px-5 py-3 text-center text-[11.5px] text-fg-4">{t("everywhere.more")}</p>}
    </div>
  );
}

const lineCls =
  "group flex w-full items-center gap-3 px-5 py-2.5 text-left outline-none transition-colors duration-100 hover:bg-hover focus-visible:bg-hover focus-visible:shadow-[inset_2px_0_0_var(--focus)]";

function TicketLine({ t: tk, ws }: { t: Ticket; ws: Membership["workspace"] }) {
  const { t } = useTranslation("together");
  const openIn = useOpenInWorkspace();
  return (
    <button
      type="button"
      className={lineCls}
      onClick={() => openIn(ws.id, { to: "/inbox/$folder/{-$ticketId}", params: { folder: "mine", ticketId: tk.id } })}
      title={t("everywhere.openIn", { name: ws.name })}
    >
      <WorkspaceBadge name={ws.name} logo={ws.avatar_url} size="md" label={t("badge.in", { name: ws.name })} />
      <span className="min-w-0 flex-1">
        <span className="flex min-w-0 items-center gap-1.5">
          <PriorityIcon priority={tk.priority} muted />
          <span className="truncate text-[13px] font-medium text-fg" data-ph-mask>
            {tk.subject}
          </span>
        </span>
        <span className="mt-0.5 flex min-w-0 items-center gap-1.5 text-[12px] text-fg-3">
          <span className="mono shrink-0 text-fg-4">#{tk.number}</span>
          <span className="truncate">{ws.name}</span>
          {tk.contact && (
            <>
              <span aria-hidden className="text-fg-4">·</span>
              <span className="truncate" data-ph-mask>
                {tk.contact.name || tk.contact.email}
              </span>
            </>
          )}
        </span>
      </span>
      <StatusPill status={tk.status} />
      <span className="mono w-10 shrink-0 text-right text-[11px] text-fg-4">{relShort(tk.last_message_at)}</span>
    </button>
  );
}

function ThreadLine({ th, ws }: { th: MailThread; ws: Membership["workspace"] }) {
  const { t } = useTranslation("together");
  const openIn = useOpenInWorkspace();
  const from = th.participants[0];
  return (
    <button
      type="button"
      className={lineCls}
      onClick={() => openIn(ws.id, { to: "/mail/$box/{-$threadId}", params: { box: "inbox", threadId: th.id }, search: {} })}
      title={t("everywhere.openIn", { name: ws.name })}
    >
      <WorkspaceBadge name={ws.name} logo={ws.avatar_url} size="md" label={t("badge.in", { name: ws.name })} />
      <span className="min-w-0 flex-1">
        <span className="flex min-w-0 items-center gap-1.5">
          {th.unread && <span className="size-1.5 shrink-0 rounded-full bg-tone-blue-solid" aria-hidden />}
          <span className={cn("truncate text-[13px]", th.unread ? "font-semibold text-fg" : "text-fg-2")} data-ph-mask>
            {from?.name || from?.email || th.subject}
          </span>
          {th.has_attachments && <Paperclip aria-hidden className="size-3 shrink-0 text-fg-4" />}
        </span>
        <span className="mt-0.5 block truncate text-[12px] text-fg-3" data-ph-mask>
          <span className="text-fg-2">{th.subject}</span>
          {th.snippet ? ` · ${th.snippet}` : ""}
        </span>
      </span>
      <span className="mono w-10 shrink-0 text-right text-[11px] text-fg-4">{relShort(th.last_message_at)}</span>
    </button>
  );
}
