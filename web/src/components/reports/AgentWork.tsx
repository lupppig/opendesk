import { ArrowDown, ArrowUp } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { ReportsAgentRow, ReportsAgents } from "@/lib/api/types";
import { Avatar } from "@/components/ui/avatar";
import { Tooltip } from "@/components/ui/tooltip";
import { cn } from "@/lib/cn";
import { minutes } from "@/lib/format";

export type AgentSortKey = "name" | Exclude<keyof ReportsAgentRow, "user">;
export type AgentSort = { key: AgentSortKey; dir: "asc" | "desc" };

/** Each column's figure; null sorts last whichever way the column runs. */
const value = (r: ReportsAgentRow, key: AgentSortKey): string | number | null => (key === "name" ? r.user.name : r[key]);

/** The rows in `sort`'s order: empty figures last, then by name. */
export function sortAgents(rows: ReportsAgentRow[], sort: AgentSort): ReportsAgentRow[] {
  const sign = sort.dir === "asc" ? 1 : -1;
  return [...rows].sort((a, b) => {
    const x = value(a, sort.key);
    const y = value(b, sort.key);
    if (x === null || y === null) {
      if (x !== y) return x === null ? 1 : -1;
    } else if (x !== y) {
      return sign * (typeof x === "string" ? x.localeCompare(String(y)) : x - (y as number));
    }
    return a.user.name.localeCompare(b.user.name) || a.user.id.localeCompare(b.user.id);
  });
}

const int = (n: number) => n.toLocaleString();
const ratio = (n: number | null) => (n === null ? "–" : n.toLocaleString(undefined, { minimumFractionDigits: 1, maximumFractionDigits: 1 }));

type Column = { key: Exclude<AgentSortKey, "name">; label: string; hint: string; show: (r: ReportsAgentRow) => string };

/** Each member's work in the period, sortable by any column. */
export function AgentWorkTable({ report }: { report: ReportsAgents }) {
  const { t } = useTranslation("reports");
  const [sort, setSort] = useState<AgentSort>({ key: "solved", dir: "desc" });
  const columns: Column[] = [
    { key: "assigned", label: t("work.assigned"), hint: t("work.assignedHint"), show: (r) => int(r.assigned) },
    { key: "solved", label: t("work.solved"), hint: t("work.solvedHint"), show: (r) => int(r.solved) },
    { key: "replies", label: t("work.replies"), hint: t("work.repliesHint"), show: (r) => int(r.replies) },
    { key: "notes", label: t("work.notes"), hint: t("work.notesHint"), show: (r) => int(r.notes) },
    { key: "replies_per_solved", label: t("work.perSolved"), hint: t("work.perSolvedHint"), show: (r) => ratio(r.replies_per_solved) },
    { key: "first_response_p50_minutes", label: t("work.firstResponse"), hint: t("work.firstResponseHint"), show: (r) => minutes(r.first_response_p50_minutes) },
    { key: "handle_p50_minutes", label: t("work.handle"), hint: t("work.handleHint"), show: (r) => minutes(r.handle_p50_minutes) },
  ];
  if (report.online_time !== "none") {
    columns.push({ key: "online_minutes", label: t("work.online"), hint: t("work.onlineHint"), show: (r) => minutes(r.online_minutes) });
  }
  const rows = sortAgents(report.data, sort);
  const toggle = (key: AgentSortKey) =>
    setSort((s) => (s.key === key ? { key, dir: s.dir === "asc" ? "desc" : "asc" } : { key, dir: key === "name" ? "asc" : "desc" }));
  const head = (key: AgentSortKey, label: string, hint?: string, right = true) => {
    const active = sort.key === key;
    const Arrow = sort.dir === "asc" ? ArrowUp : ArrowDown;
    const button = (
      <button
        type="button"
        onClick={() => toggle(key)}
        className={cn(
          "label-caps inline-flex items-center gap-1 rounded-sm font-normal outline-none transition-colors hover:text-fg focus-visible:shadow-[0_0_0_2px_var(--focus)]",
          active && "text-fg",
          right && "flex-row-reverse",
        )}
      >
        {label}
        <Arrow aria-hidden className={cn("size-3 shrink-0", !active && "invisible")} />
      </button>
    );
    return (
      <th
        key={key}
        scope="col"
        aria-sort={active ? (sort.dir === "asc" ? "ascending" : "descending") : "none"}
        className={cn("whitespace-nowrap py-2 font-normal", right ? "pl-3 text-right last:pr-4" : "pl-4")}
      >
        {hint ? <Tooltip content={hint}>{button}</Tooltip> : button}
      </th>
    );
  };
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[760px] text-[13px]">
        <caption className="sr-only">{t("work.caption")}</caption>
        <thead>
          <tr className="border-b border-line text-left">
            {head("name", t("work.agent"), undefined, false)}
            {columns.map((c) => head(c.key, c.label, c.hint))}
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.user.id} className="border-b border-line-soft transition-colors last:border-0 hover:bg-sunken">
              <td className="max-w-0 py-2 pl-4 pr-3">
                <span className="flex min-w-0 items-center gap-2.5">
                  <Avatar name={r.user.name} src={r.user.avatar_url} size="sm" />
                  <span className="truncate font-medium">{r.user.name}</span>
                </span>
              </td>
              {columns.map((c) => (
                <td key={c.key} className={cn("mono py-2 pl-3 text-right text-xs tabular-nums last:pr-4", sort.key === c.key ? "text-fg" : "text-fg-2")}>
                  {c.show(r)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
