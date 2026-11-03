import { keepPreviousData, useInfiniteQuery } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { ErrorState } from "@/components/ErrorState";
import { FilterChips } from "@/components/FilterChips";
import { PageBody, PageHeader } from "@/components/layout";
import { ListSearch } from "@/components/ListSearch";
import { AdminRolePill, FlagPill, SuspendedPill } from "@/components/pills";
import { LoadMore, RowLink, Table, TableSkeleton, TD, TH, THead } from "@/components/table";
import { Time } from "@/components/Time";
import { Avatar } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { EmptyState, GhostList } from "@/components/ui/empty";
import { number } from "@/lib/format";
import { q, type UserFilter } from "@/lib/queries";

/** Each filter's label is `list.filter.<value>` in the users catalog. */
const FILTERS: UserFilter[] = ["all", "suspended", "admins", "unverified"];

export default function Users() {
  const { t } = useTranslation(["users", "common"]);
  const search = useSearch({ from: "/shell/users" });
  const navigate = useNavigate({ from: "/users" });
  const filter = search.filter ?? "all";
  const list = useInfiniteQuery({ ...q.users({ q: search.q, filter }), placeholderData: keepPreviousData });
  const rows = list.data?.pages.flatMap((p) => p.data) ?? [];
  const filtered = !!search.q || filter !== "all";

  return (
    <>
      <PageHeader title={t("list.title")}>
        <ListSearch value={search.q} placeholder={t("list.search")} onCommit={(v) => void navigate({ search: (s) => ({ ...s, q: v }), replace: true })} />
      </PageHeader>
      <div className="flex h-11 shrink-0 items-center gap-3 border-b border-line-soft px-4 md:px-6">
        <FilterChips
          label={t("list.filterLabel")}
          value={filter}
          onChange={(f) => void navigate({ search: (s) => ({ ...s, filter: f === "all" ? undefined : f }), replace: true })}
          options={FILTERS.map((f) => ({ value: f, label: t(`list.filter.${f}`) }))}
        />
      </div>
      <PageBody>
        {list.isPending ? (
          <TableSkeleton cols={6} />
        ) : list.isError && !rows.length ? (
          <ErrorState error={list.error} onRetry={() => void list.refetch()} />
        ) : !rows.length ? (
          filtered ? (
            <EmptyState
              art="search"
              title={t("list.noMatch")}
              description={t("list.noMatchHint")}
              action={
                <Button variant="secondary" onClick={() => void navigate({ search: {}, replace: true })}>
                  {t("common:clearFilters")}
                </Button>
              }
            />
          ) : (
            <EmptyState art="users" ghost={<GhostList variant="people" />} title={t("list.empty")} description={t("list.emptyHint")} />
          )
        ) : (
          <>
            <Table className={list.isPlaceholderData ? "opacity-60 transition-opacity" : undefined}>
              <THead>
                <TH>{t("list.col.user")}</TH>
                <TH>{t("list.col.security")}</TH>
                <TH align="right">{t("list.col.workspaces")}</TH>
                <TH>{t("list.col.lastSeen")}</TH>
                <TH>{t("list.col.joined")}</TH>
              </THead>
              <tbody>
                {rows.map((u) => (
                  <RowLink key={u.id} to="/users/$id" params={{ id: u.id }}>
                    <TD>
                      <div className="flex min-w-0 items-center gap-2.5">
                        <Avatar name={u.name || u.email} src={u.avatar_url} size="md" />
                        <div className="min-w-0">
                          <Link to="/users/$id" params={{ id: u.id }} className="block truncate font-medium text-fg hover:underline">
                            {u.name || u.email}
                          </Link>
                          {u.name && <span className="block truncate text-[11.5px] text-fg-3">{u.email}</span>}
                        </div>
                        {u.admin_role && <AdminRolePill role={u.admin_role} />}
                        {u.suspended_at && <SuspendedPill />}
                      </div>
                    </TD>
                    <TD>
                      <div className="flex flex-wrap items-center gap-1">
                        <FlagPill on={u.email_verified} yes={t("flag.verified")} no={t("flag.unverified")} warnOff />
                        <FlagPill on={u.totp_enabled} yes={t("flag.twoFactor")} no={t("flag.noTwoFactor")} />
                        {!u.has_password && <FlagPill on={false} yes="" no={t("flag.noPassword")} />}
                      </div>
                    </TD>
                    <TD align="right" mono>
                      {number(u.workspace_count)}
                    </TD>
                    <TD className="text-fg-2">
                      <Time iso={u.last_seen_at} />
                    </TD>
                    <TD className="text-fg-3">
                      <Time iso={u.created_at} />
                    </TD>
                  </RowLink>
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
