import { keepPreviousData, useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { DataPairs } from "@/components/bits";
import { ErrorState } from "@/components/ErrorState";
import { PageBody, PageHeader } from "@/components/layout";
import { LoadMore, Table, TableSkeleton, TD, TH, THead, TR } from "@/components/table";
import { Time } from "@/components/Time";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { EmptyState, GhostList } from "@/components/ui/empty";
import { Select } from "@/components/ui/select";
import { AUDIT_ACTIONS, type AdminAuditLog } from "@/lib/api/types";
import { humanize } from "@/lib/format";
import { q } from "@/lib/queries";
import type { Tone } from "@/lib/tones";

const ALL = "__all";

export function actionTone(action: string): Tone {
  if (/failed|deleted|revoked|mfa_disabled/.test(action)) return "red";
  if (/suspended|cancelled/.test(action)) return "amber";
  if (/granted|role_changed/.test(action)) return "indigo";
  if (/unsuspended|retried|limits_changed|ai_granted|collab_changed/.test(action)) return "blue";
  return "gray";
}

function Target({ e }: { e: AdminAuditLog }) {
  const { t } = useTranslation("audit");
  const type = t(`target.${e.target_type}`, { defaultValue: humanize(e.target_type) });
  const id = e.target_id;
  const short = id ? (id.length > 14 ? `${id.slice(0, 12)}…` : id) : "";
  if (e.target_type === "instance" || !id) return <span className="text-fg-3">{type}</span>;
  const label = <span className="text-fg-3">{type}</span>;
  const idEl = (
    <span className="mono text-[11.5px]" title={id}>
      {short}
    </span>
  );
  if (e.target_type === "user" || e.target_type === "admin")
    return (
      <Link to="/users/$id" params={{ id }} className="inline-flex items-center gap-1.5 hover:underline">
        {label}
        {idEl}
      </Link>
    );
  if (e.target_type === "workspace")
    return (
      <Link to="/workspaces/$id" params={{ id }} className="inline-flex items-center gap-1.5 hover:underline">
        {label}
        {idEl}
      </Link>
    );
  return (
    <span className="inline-flex items-center gap-1.5">
      {label}
      <span className="mono text-[11.5px]">#{id}</span>
    </span>
  );
}

function Actor({ e }: { e: AdminAuditLog }) {
  const { t } = useTranslation("audit");
  if (e.actor.type === "cli")
    return (
      <span className="flex items-center gap-1.5">
        <Badge tone="outline">{t("actor.cli")}</Badge>
      </span>
    );
  if (e.actor.type === "system")
    return (
      <span className="flex items-center gap-1.5">
        <Badge tone="outline">{t("actor.system")}</Badge>
      </span>
    );
  if (e.actor.id)
    return (
      <Link to="/users/$id" params={{ id: e.actor.id }} className="block truncate hover:underline">
        {e.actor.email}
      </Link>
    );
  return <span className="block truncate text-fg-2">{e.actor.email || t("actor.unknown")}</span>;
}

export default function Audit() {
  const { t } = useTranslation("audit");
  const search = useSearch({ from: "/shell/audit" });
  const navigate = useNavigate({ from: "/audit" });
  const admins = useQuery(q.admins());
  const list = useInfiniteQuery({ ...q.audit({ action: search.action, actor_id: search.actor, target_id: search.target }), placeholderData: keepPreviousData });
  const rows = list.data?.pages.flatMap((p) => p.data) ?? [];
  const filtered = !!(search.action || search.actor || search.target);
  const actorOptions = [...(admins.data ?? [])].sort((a, b) => a.user.email.localeCompare(b.user.email)).map((a) => ({ value: a.user.id, label: a.user.email }));
  // An actor filter from a link may be someone no longer an admin.
  if (search.actor && !actorOptions.some((o) => o.value === search.actor)) actorOptions.push({ value: search.actor, label: search.actor });

  return (
    <>
      <PageHeader title={t("title")} subtitle={t("subtitle")} />
      <div className="flex min-h-11 shrink-0 flex-wrap items-center gap-2 border-b border-line-soft px-4 py-2 md:px-6">
        <div className="w-[230px]">
          <Select
            size="sm"
            aria-label={t("col.action")}
            value={search.action ?? ALL}
            onChange={(v) => void navigate({ search: (p) => ({ ...p, action: v === ALL ? undefined : v }), replace: true })}
            options={[{ value: ALL, label: t("allActions") }, ...AUDIT_ACTIONS.map((a) => ({ value: a, label: a }))]}
          />
        </div>
        <div className="w-[230px]">
          <Select
            size="sm"
            aria-label={t("col.admin")}
            value={search.actor ?? ALL}
            onChange={(v) => void navigate({ search: (p) => ({ ...p, actor: v === ALL ? undefined : v }), replace: true })}
            options={[{ value: ALL, label: t("allAdmins") }, ...actorOptions]}
          />
        </div>
        {search.target && (
          <span className="inline-flex h-7 items-center gap-1 rounded-md bg-hover pl-2.5 pr-1 text-[12px] text-fg-2">
            {t("col.target")} <span className="mono text-fg">{search.target}</span>
            <button
              type="button"
              aria-label={t("clearTarget")}
              onClick={() => void navigate({ search: (p) => ({ ...p, target: undefined }), replace: true })}
              className="flex size-5 items-center justify-center rounded-sm text-fg-3 hover:bg-active hover:text-fg"
            >
              <X className="size-3" />
            </button>
          </span>
        )}
        {filtered && (
          <Button variant="ghost" size="xs" className="ml-auto" onClick={() => void navigate({ search: {}, replace: true })}>
            {t("clearFilters")}
          </Button>
        )}
      </div>
      <PageBody>
        {list.isPending ? (
          <TableSkeleton cols={6} />
        ) : list.isError && !rows.length ? (
          <ErrorState error={list.error} onRetry={() => void list.refetch()} />
        ) : !rows.length ? (
          <EmptyState
            art={filtered ? "search" : "audit"}
            ghost={filtered ? undefined : <GhostList />}
            title={filtered ? t("empty.noMatch") : t("empty.title")}
            description={filtered ? t("empty.noMatchHint") : t("empty.hint")}
            action={
              filtered ? (
                <Button variant="secondary" onClick={() => void navigate({ search: {}, replace: true })}>
                  {t("clearFilters")}
                </Button>
              ) : undefined
            }
          />
        ) : (
          <>
            <Table className={list.isPlaceholderData ? "opacity-60 transition-opacity" : undefined}>
              <THead>
                <TH>{t("col.admin")}</TH>
                <TH>{t("col.action")}</TH>
                <TH>{t("col.target")}</TH>
                <TH>{t("col.details")}</TH>
                <TH>{t("col.ip")}</TH>
                <TH>{t("col.when")}</TH>
              </THead>
              <tbody>
                {rows.map((e) => (
                  <TR key={e.id}>
                    <TD className="max-w-[220px]">
                      <Actor e={e} />
                    </TD>
                    <TD>
                      <Badge tone={actionTone(e.action)} className="mono">
                        {e.action}
                      </Badge>
                    </TD>
                    <TD>
                      <Target e={e} />
                    </TD>
                    <TD className="max-w-[360px]">
                      <DataPairs data={e.data} max={4} />
                    </TD>
                    <TD mono className="text-[11.5px] text-fg-3">
                      {e.ip || "–"}
                    </TD>
                    <TD className="text-fg-3">
                      <Time iso={e.created_at} />
                    </TD>
                  </TR>
                ))}
              </tbody>
            </Table>
            <LoadMore shown={rows.length} hasMore={!!list.hasNextPage} loading={list.isFetchingNextPage} onLoad={() => void list.fetchNextPage()} />
          </>
        )}
      </PageBody>
    </>
  );
}
