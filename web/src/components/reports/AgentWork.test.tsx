import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { i18n } from "@/i18n";
import { TooltipProvider } from "@/components/ui/tooltip";
import type { ReportsAgentRow, ReportsAgents } from "@/lib/api/types";
import { AgentWorkTable, sortAgents } from "./AgentWork";

const row = (name: string, o: Partial<ReportsAgentRow>): ReportsAgentRow => ({
  user: { id: name.toLowerCase(), name, email: `${name.toLowerCase()}@example.com`, avatar_url: null },
  assigned: 0,
  solved: 0,
  replies: 0,
  notes: 0,
  replies_per_solved: null,
  first_response_p50_minutes: null,
  handle_p50_minutes: null,
  online_minutes: null,
  ...o,
});

const rows = [
  row("Bea", { solved: 4, replies: 9, handle_p50_minutes: 30, online_minutes: 120 }),
  row("Anna", { solved: 7, replies: 9, handle_p50_minutes: 200, online_minutes: 60 }),
  row("Carl", { solved: 4, replies: 2, online_minutes: null }),
];

describe("sortAgents", () => {
  it("sorts by a figure either way, with empty figures last and ties by name", () => {
    expect(sortAgents(rows, { key: "solved", dir: "desc" }).map((r) => r.user.name)).toEqual(["Anna", "Bea", "Carl"]);
    expect(sortAgents(rows, { key: "solved", dir: "asc" }).map((r) => r.user.name)).toEqual(["Bea", "Carl", "Anna"]);
    expect(sortAgents(rows, { key: "handle_p50_minutes", dir: "asc" }).map((r) => r.user.name)).toEqual(["Bea", "Anna", "Carl"]);
    expect(sortAgents(rows, { key: "handle_p50_minutes", dir: "desc" }).map((r) => r.user.name)).toEqual(["Anna", "Bea", "Carl"]);
    expect(sortAgents(rows, { key: "name", dir: "asc" }).map((r) => r.user.name)).toEqual(["Anna", "Bea", "Carl"]);
  });
});

let container: HTMLDivElement;
let root: Root;

beforeEach(async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  await i18n.changeLanguage("en");
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  vi.unstubAllGlobals();
});

async function render(report: ReportsAgents) {
  await act(async () => {
    root.render(
      <TooltipProvider>
        <AgentWorkTable report={report} />
      </TooltipProvider>,
    );
  });
}

const names = () => [...container.querySelectorAll("tbody tr")].map((tr) => tr.querySelector("td span.truncate")?.textContent);
const header = (label: string) => [...container.querySelectorAll("th")].find((th) => th.textContent === label)!;

describe("AgentWorkTable", () => {
  it("starts with the most solved and sorts by the header clicked", async () => {
    await render({ data: rows, online_time: "all" });
    expect(names()).toEqual(["Anna", "Bea", "Carl"]);
    expect(header("Solved").getAttribute("aria-sort")).toBe("descending");
    await act(async () => header("Online").querySelector("button")!.click());
    expect(names()).toEqual(["Bea", "Anna", "Carl"]);
    expect(header("Online").getAttribute("aria-sort")).toBe("descending");
    expect(header("Solved").getAttribute("aria-sort")).toBe("none");
    await act(async () => header("Online").querySelector("button")!.click());
    expect(names()).toEqual(["Anna", "Bea", "Carl"]);
    expect(container.querySelector("tbody tr:last-child")?.textContent).toContain("–");
  });

  it("leaves online time out when the caller sees nobody's", async () => {
    await render({ data: rows, online_time: "none" });
    expect([...container.querySelectorAll("th")].map((th) => th.textContent)).not.toContain("Online");
    expect(header("Handle time")).toBeTruthy();
  });
});
