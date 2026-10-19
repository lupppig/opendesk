import { keepPreviousData, useInfiniteQuery } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { ErrorState } from "@/components/ErrorState";
import { FilterChips } from "@/components/FilterChips";
import { PageBody, PageHeader } from "@/components/layout";
import { ListSearch } from "@/components/ListSearch";
import { PlanPill, SubscriptionPill, SuspendedPill, TestPill } from "@/components/pills";
import { LoadMore, RowLink, Table, TableSkeleton, TD, TH, THead } from "@/components/table";
import { Time } from "@/components/Time";
import { Avatar } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { EmptyState, GhostList } from "@/components/ui/empty";
import { number } from "@/lib/format";
import { q, type WorkspaceFilter } from "@/lib/queries";
import { useSession } from "@/lib/session";

const FILTERS: { value: WorkspaceFilter; label: string }[] = [
  { value: "all", label: "All" },
  { value: "suspended", label: "Suspended" },
  { value: "paid", label: "Paid" },
  { value: "free", label: "Free" },
  { value: "collab", label: "Collab" },
  { value: "test", label: "Test" },
];

export default function Workspaces() {
  const search = useSearch({ from: "/shell/workspaces" });
  const navigate = useNavigate({ from: "/workspaces" });
  const billing = useSession((s) => s.me?.instance.billing_enabled ?? false);
  const filter = search.filter ?? "all";
  const list = useInfiniteQuery({ ...q.workspaces({ q: search.q, filter }), placeholderData: keepPreviousData });
  const rows = list.data?.pages.flatMap((p) => p.data) ?? [];
  const filtered = !!search.q || filter !== "all";
  const filters = billing ? FILTERS : FILTERS.filter((f) => f.value === "all" || f.value === "suspended" || f.value === "test");

  return (
    <>
      <PageHeader title="Workspaces">
        <ListSearch
          value={search.q}
          placeholder="Search by name, slug, id or a member's email"
          onCommit={(v) => void navigate({ search: (s) => ({ ...s, q: v }), replace: true })}
        />
      </PageHeader>
      <div className="flex h-11 shrink-0 items-center gap-3 border-b border-line-soft px-4 md:px-6">
        <FilterChips
          label="Filter workspaces"
          value={filter}
          onChange={(f) => void navigate({ search: (s) => ({ ...s, filter: f === "all" ? undefined : f }), replace: true })}
          options={filters}
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
              title="No workspaces match"
              description="Try another name, slug, id or member email, or clear the filters."
              action={
                <Button variant="secondary" onClick={() => void navigate({ search: {}, replace: true })}>
                  Clear search and filters
                </Button>
              }
            />
          ) : (
            <EmptyState art="workspaces" ghost={<GhostList />} title="No workspaces yet" description="Workspaces show up here as people sign up and create them." />
          )
        ) : (
          <>
            <Table className={list.isPlaceholderData ? "opacity-60 transition-opacity" : undefined}>
              <THead>
                <TH>Workspace</TH>
                <TH>Plan</TH>
                {billing && <TH>Subscription</TH>}
                <TH align="right">Members</TH>
                <TH>Last activity</TH>
                <TH>Created</TH>
              </THead>
              <tbody>
                {rows.map((w) => (
                  <RowLink key={w.id} to="/workspaces/$id" params={{ id: w.id }}>
                    <TD>
                      <div className="flex min-w-0 items-center gap-2.5">
                        <Avatar name={w.name} src={w.avatar_url} square size="md" />
                        <div className="min-w-0">
                          <Link to="/workspaces/$id" params={{ id: w.id }} className="block truncate font-medium text-fg hover:underline">
                            {w.name}
                          </Link>
                          <span className="mono block truncate text-[11px] text-fg-4">{w.slug}</span>
                        </div>
                        {w.test && <TestPill />}
                        {w.suspended_at && <SuspendedPill />}
                      </div>
                    </TD>
                    <TD>
                      <PlanPill plan={w.plan} />
                    </TD>
                    {billing && (
                      <TD>
                        <SubscriptionPill status={w.status} />
                      </TD>
                    )}
                    <TD align="right" mono>
                      {number(w.members)}
                    </TD>
                    <TD className="text-fg-2">
                      <Time iso={w.last_activity_at} />
                    </TD>
                    <TD className="text-fg-3">
                      <Time iso={w.created_at} />
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
