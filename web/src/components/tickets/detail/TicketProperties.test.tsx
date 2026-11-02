import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { i18n, loadAgentCatalogs } from "@/i18n";
import { TooltipProvider } from "@/components/ui/tooltip";
import type { Mailbox, Team, Ticket, Workspace } from "@/lib/api/types";
import { qk } from "@/lib/queries";
import { useSession } from "@/lib/session";
import { TicketProperties, TicketPropertiesBar, type PropPicker } from "./TicketProperties";
import { HeaderPills } from "./TicketDetail";

beforeAll(() => loadAgentCatalogs("fr"));

// The pickers' lists measure themselves; jsdom has no ResizeObserver.
globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
} as unknown as typeof ResizeObserver;
// cmdk keeps the selected row in view.
Element.prototype.scrollIntoView ??= () => {};

const team = { id: "team1", name: "Support", private: false, member_ids: ["u9"], members: [] } as unknown as Team;
const mailbox = { id: "mb1", name: "Support", email: "support@margin.test", aliases: [], team_id: null, private: false } as unknown as Mailbox;

const ticket = (patch: Partial<Ticket> = {}): Ticket =>
  ({
    id: "t1",
    number: 12679,
    subject: "Bestelling #194799",
    status: "open",
    priority: "high",
    severity: null,
    channel: "email",
    mailbox_id: "mb1",
    contact: { id: "c1", name: "Ada Buyer", email: "ada@example.com" },
    assignee: { id: "u9", name: "Lea Martin", email: "lea@margin.test", avatar_url: null },
    team_id: "team1",
    brand_id: "b1",
    widget_id: null,
    origin_mailbox_id: "mb1",
    received_at: "support@margin.test",
    page_url: "",
    routed: null,
    required_skills: [],
    tags: [{ id: "tag1", name: "Refund", color: null }],
    fields: {},
    sla: null,
    first_responded_at: null,
    solved_at: null,
    snoozed_until: null,
    last_message_at: "2026-10-28T09:00:00Z",
    last_customer_message_at: null,
    waiting_since: null,
    unanswered_count: 0,
    last_message_preview: "",
    message_count: 1,
    spam: false,
    csat: null,
    custom: {},
    created_at: "2026-10-28T09:00:00Z",
    updated_at: "2026-10-28T09:00:00Z",
    ...patch,
  }) as Ticket;

let host: HTMLDivElement;
let root: Root;

beforeEach(async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  await i18n.changeLanguage("en");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(async () => {
  await act(async () => root.unmount());
  document.body.innerHTML = "";
  useSession.setState({ memberships: [], workspaceId: null, user: null });
  vi.unstubAllGlobals();
});

/** Renders with the workspace's teams and addresses known, and nothing fetched. */
async function render(node: ReactNode, role: "agent" | "viewer" = "agent") {
  useSession.setState({
    user: { id: "u9", name: "Lea Martin", email: "lea@margin.test" } as never,
    memberships: [{ workspace: { id: "w" }, role }] as never,
    workspaceId: "w",
  });
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity, enabled: false } } });
  qc.setQueryData<Workspace>(qk.workspace, { id: "w", name: "Acme", settings: {} } as Workspace);
  qc.setQueryData(qk.teams, [team]);
  qc.setQueryData(qk.brands, []);
  qc.setQueryData(qk.mailboxes, [mailbox]);
  qc.setQueryData(qk.members, []);
  await act(async () =>
    root.render(
      <QueryClientProvider client={qc}>
        <TooltipProvider>{node}</TooltipProvider>
      </QueryClientProvider>,
    ),
  );
}

/** Each bar button's name as a screen reader hears it: its text without what is hidden from it. */
const names = (el: ParentNode) =>
  [...el.querySelectorAll<HTMLButtonElement>("[data-bar-property]")].map((b) => {
    const c = b.cloneNode(true) as HTMLElement;
    c.querySelectorAll("[aria-hidden]").forEach((n) => n.remove());
    return c.textContent?.replace(/\s+/g, " ").trim();
  });

describe("ticket properties in a bar above the conversation", () => {
  it("holds every property control in the column's order, each named for a screen reader", async () => {
    await render(<TicketPropertiesBar t={ticket()} picker={null} onPicker={() => {}} />);
    const bar = host.querySelector("[data-properties-bar]")!;
    expect(bar.getAttribute("role")).toBe("group");
    expect(bar.getAttribute("aria-label")).toBe("Ticket properties");
    expect(names(bar)).toEqual(["Status: Open", "Priority: High", "Severity: No severity", "Assignee: Lea Martin", "Team: Support", "Reply from: support@margin.test"]);
    // The labels: a named group of removable chips and the picker to add more.
    const labels = bar.querySelector("[role='group'][aria-label='Labels']")!;
    expect(labels.textContent).toContain("Refund");
    expect(labels.querySelector("button[aria-label='Add label']")).not.toBeNull();
    // Wraps on a narrow pane rather than overflowing it.
    expect(bar.className).toContain("flex-wrap");
  });

  it("opens the same picker as the column, from a click or from the ticket's shortcut", async () => {
    const onPicker = vi.fn<(p: PropPicker) => void>();
    await render(<TicketPropertiesBar t={ticket()} picker={null} onPicker={onPicker} />);
    const status = [...host.querySelectorAll<HTMLButtonElement>("[data-bar-property]")][0];
    await act(async () => {
      status.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, button: 0 }));
      status.click();
    });
    expect(onPicker).toHaveBeenCalledWith("status");
    // What the a shortcut does in TicketDetail: the bar's assignee picker opens in place.
    await render(<TicketPropertiesBar t={ticket()} picker="assignee" onPicker={onPicker} />);
    const assignee = [...host.querySelectorAll<HTMLButtonElement>("[data-bar-property]")][3];
    expect(assignee.getAttribute("aria-expanded")).toBe("true");
    expect(document.querySelector("[cmdk-input]")?.getAttribute("aria-label")).toBe("Assign to");
  });

  it("shows a viewer the values without letting them change", async () => {
    await render(<TicketPropertiesBar t={ticket()} picker={null} onPicker={() => {}} />, "viewer");
    const buttons = [...host.querySelectorAll<HTMLButtonElement>("[data-bar-property]")];
    expect(buttons.length).toBeGreaterThan(0);
    expect(buttons.every((b) => b.disabled)).toBe(true);
    expect(host.querySelector("button[aria-label='Add label']")).toBeNull();
  });
});

describe("the details column", () => {
  it("keeps required skill labels in the details column when the bar is on", async () => {
    await render(<TicketProperties t={ticket({ required_skills: [{ id: "skill-billing", name: "Billing" }] })} picker={null} onPicker={() => {}} bar />);
    expect(host.textContent).toContain("Required skills");
    expect(host.textContent).toContain("Billing");
  });

  it("starts with the properties, the customer further down, by default", async () => {
    await render(<TicketProperties t={ticket()} picker={null} onPicker={() => {}} />);
    const column = host.firstElementChild!;
    expect(column.firstElementChild!.textContent).toContain("Status");
    expect(column.firstElementChild!.hasAttribute("data-contact-section")).toBe(false);
    expect(column.querySelector("[data-contact-section]")).not.toBeNull();
  });

  it("starts with the customer when the properties are in the bar, and holds them nowhere else", async () => {
    await render(<TicketProperties t={ticket()} picker={null} onPicker={() => {}} bar />);
    const column = host.firstElementChild!;
    expect(column.firstElementChild!.hasAttribute("data-contact-section")).toBe(true);
    expect(column.querySelectorAll("[data-contact-section]")).toHaveLength(1);
    expect(column.textContent).not.toContain("Assignee");
    expect(column.textContent).not.toContain("Lea Martin");
    expect(column.textContent).not.toContain("Reply from");
  });
});

describe("the ticket header", () => {
  it("shows status, priority and team next to the subject by default", async () => {
    await render(<HeaderPills t={ticket({ severity: "sev2" })} bar={false} />);
    const text = host.textContent ?? "";
    expect(text).toContain("Open");
    expect(text).toContain("High");
    expect(text).toContain("Support");
  });

  it("leaves them to the bar when it is on, keeping spam, which the bar has not", async () => {
    await render(<HeaderPills t={ticket({ severity: "sev2" })} bar />);
    expect(host.querySelector("[data-header-pills]")).toBeNull();
    await render(<HeaderPills t={ticket({ spam: true })} bar />);
    const pills = host.querySelector("[data-header-pills]")!;
    expect(pills.textContent).toBe("Spam");
  });
});
