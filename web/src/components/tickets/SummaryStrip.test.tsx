import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeAll, expect, it, vi } from "vitest";
import { i18n, loadAgentCatalogs } from "@/i18n";
import type { Ticket, TicketListSummary } from "@/lib/api/types";
import type { InboxSearch } from "@/lib/folders";
import { TooltipProvider } from "@/components/ui/tooltip";
import { SummaryStrip, loadedCounts, stripCounts, type StripCounts } from "./SummaryStrip";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

beforeAll(() => loadAgentCatalogs("en"));
afterEach(async () => {
  await i18n.changeLanguage("en");
  document.body.innerHTML = "";
});

const now = Date.parse("2026-10-27T12:00:00Z");
const at = (min: number) => new Date(now + min * 60_000).toISOString();

function ticket(over: Partial<Ticket>): Ticket {
  return {
    id: crypto.randomUUID(),
    status: "open",
    priority: "normal",
    last_message_at: at(-10),
    last_customer_message_at: null,
    first_responded_at: null,
    sla: null,
    ...over,
  } as Ticket;
}

const sla = (first: string | null, resolution: string | null, breached = false) => ({ policy_id: "p", first_response_due_at: first, resolution_due_at: resolution, breached });

async function render(counts: StripCounts, search: InboxSearch = {}) {
  const onSearch = vi.fn();
  const host = document.createElement("div");
  document.body.appendChild(host);
  const root = createRoot(host);
  await act(async () =>
    root.render(
      <TooltipProvider>
        <SummaryStrip {...counts} search={search} onSearch={onSearch} />
      </TooltipProvider>,
    ),
  );
  const button = (label: string) => [...host.querySelectorAll("button")].find((b) => b.textContent?.includes(label));
  return { host, onSearch, button, unmount: () => act(async () => root.unmount()) };
}

const whole: TicketListSummary = { complete: true, total: 115, awaiting: 36, urgent: 2, overdue: 9, due_soon: 4 };

it("counts the loaded rows like the inbox shows them", () => {
  const rows = [
    ticket({ priority: "urgent", last_customer_message_at: at(-10) }), // awaiting, urgent
    ticket({ status: "pending", last_customer_message_at: new Date(now - 10 * 60_000 - 500).toISOString() }), // awaiting, within a second
    ticket({ last_customer_message_at: at(-11) }), // answered since
    ticket({ status: "on_hold", last_customer_message_at: at(-10), sla: sla(at(30), null) }), // due soon, not awaiting
    ticket({ sla: sla(at(-5), at(60)) }), // overdue
    ticket({ first_responded_at: at(-20), sla: sla(at(-60), at(20)) }), // due soon: its resolution
    ticket({ status: "solved", priority: "urgent", sla: sla(at(-60), null, true) }), // urgent only
  ];
  expect(loadedCounts(rows, now)).toEqual({ total: 7, awaiting: 2, urgent: 2, overdue: 1, dueSoon: 2 });
});

it("takes the whole list's numbers once the server counted it, else the loaded rows'", () => {
  const loaded = { total: 50, awaiting: 20, urgent: 1, overdue: 3, dueSoon: 0 };
  expect(stripCounts(whole, loaded, true)).toEqual({ total: 115, awaiting: 36, urgent: 2, overdue: 9, dueSoon: 4, partial: false });
  // Still loading, or not counted (too many tickets, a search): the loaded rows, partial while more remain.
  expect(stripCounts(undefined, loaded, true)).toEqual({ ...loaded, partial: true });
  expect(stripCounts({ ...whole, complete: false, total: 0 }, loaded, true)).toEqual({ ...loaded, partial: true });
  expect(stripCounts(undefined, loaded, false)).toEqual({ ...loaded, partial: false });
});

it("shows the whole list's total", async () => {
  const { host, unmount } = await render(stripCounts(whole, { total: 50, awaiting: 20, urgent: 1, overdue: 3, dueSoon: 0 }, true));
  expect(host.textContent).toContain("115 tickets");
  expect(host.textContent).toContain("36awaiting reply");
  expect(host.textContent).toContain("4due soon");
  expect(host.textContent).not.toContain("First");
  await unmount();
});

it("says the counts are the loaded rows' while more remain", async () => {
  const { host, unmount } = await render({ total: 50, awaiting: 20, urgent: 1, overdue: 3, dueSoon: 0, partial: true });
  expect(host.textContent).toContain("First 50 shown");
  expect(host.textContent).not.toContain("50 tickets");
  await unmount();
});

it("keeps the urgent and SLA toggles", async () => {
  const counts = stripCounts(whole, { total: 0, awaiting: 0, urgent: 0, overdue: 0, dueSoon: 0 }, false);
  const a = await render(counts, { q: "refund" });
  await act(async () => a.button("urgent")!.click());
  expect(a.onSearch).toHaveBeenLastCalledWith({ q: "refund", priority: "urgent" });
  await act(async () => a.button("overdue")!.click());
  expect(a.onSearch).toHaveBeenLastCalledWith({ q: "refund", sort: "due", order: undefined });
  await a.unmount();
  const b = await render(counts, { priority: "urgent", sort: "due" });
  expect(b.button("urgent")!.getAttribute("aria-pressed")).toBe("true");
  await act(async () => b.button("urgent")!.click());
  expect(b.onSearch).toHaveBeenLastCalledWith({ priority: undefined, sort: "due" });
  await act(async () => b.button("due soon")!.click());
  expect(b.onSearch).toHaveBeenLastCalledWith({ priority: "urgent", sort: undefined, order: undefined });
  await b.unmount();
});

it("hides when nothing needs attention", async () => {
  const { host, unmount } = await render({ total: 115, awaiting: 0, urgent: 0, overdue: 0, dueSoon: 0, partial: false });
  expect(host.textContent).toBe("");
  await unmount();
});
