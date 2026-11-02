import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import "@/i18n";
import { TooltipProvider } from "@/components/ui/tooltip";
import { useSession } from "@/lib/session";
import { installMock } from "@/mock";
import { ticket } from "@/lib/test/fixtures";
import type { Ticket } from "@/lib/api/types";
import { TicketProperties } from "./TicketProperties";

vi.mock("../teams", () => ({
  useTeamsCtx: () => ({ hasTeams: false, multiBrand: false, brands: [], team: () => undefined, brand: () => undefined, mailbox: () => undefined }),
  useRoutedText: () => "",
  replyAddresses: () => [],
  addressLabel: () => "",
  sendingAddress: () => "",
  BrandChip: () => null,
  TeamDot: () => null,
  TeamMove: () => null,
}));
vi.mock("../fields", () => ({
  useDisplay: () => ({ statuses: () => ["open", "pending", "solved"], status: (value: string) => value, priority: (value: string) => value }),
  FieldsSection: () => null,
}));
vi.mock("../ollie", () => ({
  SetByOllie: () => null,
  useAISet: () => ({ status: false, priority: false, severity: false, team: false, tags: new Set<string>() }),
  useOllieNote: () => () => undefined,
}));
vi.mock("@/components/ai/ticket/OlliePanel", () => ({ OlliePanel: () => null }));
vi.mock("./FieldsSection", () => ({ FieldsSection: () => null }));
vi.mock("./TicketLinks", () => ({ TicketLinks: () => null }));
vi.mock("./TicketPanels", () => ({ TicketPanels: () => null }));
vi.mock("./SideConversations", () => ({ SideConversations: () => null }));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
const settle = () => act(async () => new Promise((resolve) => setTimeout(resolve, 30)));

describe("TicketProperties routing skill labels", () => {
  installMock();
  let root: Root;
  let el: HTMLDivElement;

  beforeEach(() => {
    el = document.createElement("div");
    document.body.appendChild(el);
    root = createRoot(el);
    const ws = useSession.getState().memberships[0]?.workspace;
    if (ws) useSession.setState({ status: "authed", workspaceId: ws.id, memberships: [{ workspace: ws, role: "agent" }] } as never);
  });
  afterEach(() => {
    act(() => root.unmount());
    el.remove();
  });

  async function render(value: Ticket) {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    await act(async () => root.render(<QueryClientProvider client={qc}><TooltipProvider><TicketProperties t={value} picker={null} onPicker={() => {}} /></TooltipProvider></QueryClientProvider>));
    await settle();
  }

  it("renders required skills whether the ticket is assigned or unassigned", async () => {
    const required_skills = [{ id: "skill-billing", name: "Billing" }];
    await render(ticket({ required_skills, assignee: { id: "agent-1", name: "Ada", email: "ada@example.com", avatar_url: null } }));
    expect(el.textContent).toContain("Required skills");
    expect(el.textContent).toContain("Billing");
    await render(ticket({ required_skills, assignee: null }));
    expect(el.textContent).toContain("Required skills");
    expect(el.textContent).toContain("Billing");
  });
});
