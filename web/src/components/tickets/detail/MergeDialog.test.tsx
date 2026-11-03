import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Ticket } from "@/lib/api/types";
import { MergeDialog, mergedTicketDest } from "./MergeDialog";

const list = vi.fn();
const merge = vi.fn();
vi.mock("@/lib/api/endpoints", () => ({ api: { tickets: { list: (q: unknown) => list(q), merge: (id: string, into: string) => merge(id, into) } } }));
const navigate = vi.fn();
let params: Record<string, string> = {};
vi.mock("@tanstack/react-router", async (orig) => ({ ...(await orig<object>()), useNavigate: () => navigate, useParams: () => params }));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const ticket = (id: string, number: number) => ({ id, number, subject: `Ticket ${number}`, status: "open", contact: null }) as unknown as Ticket;
const merged = ticket("t-1", 1);
const target = ticket("t-42", 42);

describe("mergedTicketDest", () => {
  it("opens the ticket in the view the agent was working", () => {
    expect(mergedTicketDest({ viewId: "v-1", ticketId: "t-1" } as never, "t-42")).toEqual({
      to: "/views/$viewId/{-$ticketId}",
      params: { viewId: "v-1", ticketId: "t-42" },
      search: true,
    });
  });

  it("opens it in the team inbox the agent was working", () => {
    expect(mergedTicketDest({ teamId: "team-1" }, "t-42")).toEqual({
      to: "/teams/$teamId/{-$ticketId}",
      params: { teamId: "team-1", ticketId: "t-42" },
      search: true,
    });
  });

  it("opens it in the inbox folder the agent was working", () => {
    expect(mergedTicketDest({ folder: "mine" }, "t-42")).toEqual({
      to: "/inbox/$folder/{-$ticketId}",
      params: { folder: "mine", ticketId: "t-42" },
      search: true,
    });
  });

  it("falls back to the open folder outside a list", () => {
    expect(mergedTicketDest({}, "t-42")).toMatchObject({ to: "/inbox/$folder/{-$ticketId}", params: { folder: "open", ticketId: "t-42" } });
  });
});

describe("MergeDialog", () => {
  let root: Root;

  beforeEach(() => {
    list.mockReset().mockResolvedValue({ data: [merged, target] });
    merge.mockReset().mockResolvedValue(target);
    navigate.mockReset();
  });
  afterEach(async () => {
    await act(async () => root.unmount());
    document.body.innerHTML = "";
  });

  async function mergeFrom(where: Record<string, string>) {
    params = where;
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const host = document.createElement("div");
    document.body.appendChild(host);
    root = createRoot(host);
    await act(async () => root.render(<QueryClientProvider client={qc}><MergeDialog ticket={merged} open onOpenChange={() => {}} /></QueryClientProvider>));
    await act(() => new Promise((r) => setTimeout(r, 20)));
    const buttons = () => [...document.querySelectorAll("button")];
    await act(async () => buttons().find((b) => b.hasAttribute("aria-pressed") && b.textContent?.includes("#42"))!.click());
    await act(async () => buttons().find((b) => !b.hasAttribute("aria-pressed") && b.textContent?.includes("#42"))!.click());
    // TanStack Query hands a mutation's result to React on a timer: let it land.
    await act(() => new Promise((r) => setTimeout(r, 20)));
  }

  it("stays in the view after a merge, on the ticket it went into", async () => {
    await mergeFrom({ viewId: "v-1", ticketId: "t-1" });
    expect(merge).toHaveBeenCalledWith("t-1", "t-42");
    expect(navigate).toHaveBeenCalledWith({ to: "/views/$viewId/{-$ticketId}", params: { viewId: "v-1", ticketId: "t-42" }, search: true });
  });

  it("stays in the inbox folder after a merge", async () => {
    await mergeFrom({ folder: "mine", ticketId: "t-1" });
    expect(navigate).toHaveBeenCalledWith({ to: "/inbox/$folder/{-$ticketId}", params: { folder: "mine", ticketId: "t-42" }, search: true });
  });
});
