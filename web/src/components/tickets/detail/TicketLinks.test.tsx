import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import "@/i18n";
import { TooltipProvider } from "@/components/ui/tooltip";
import { api } from "@/lib/api/endpoints";
import type { TicketLink } from "@/lib/api/types";
import { qk } from "@/lib/queries";
import { TicketLinks } from "./TicketLinks";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: ReturnType<typeof createRoot>;
let client: QueryClient;

const addedLink: TicketLink = {
  id: "link-1",
  ticket_id: "ticket-1",
  integration_id: null,
  provider: "linear",
  url: "https://linear.app/acme/issue/ENG-123",
  title: "ENG-123",
  actor_type: "user",
  created_by: "user-1",
  created_at: "2026-11-02T12:00:00Z",
};

beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  client.setQueryData(qk.ticketLinks("ticket-1"), []);
});

afterEach(async () => {
  await act(async () => root.unmount());
  client.clear();
  document.body.innerHTML = "";
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

async function render(canEdit = true) {
  await act(async () => {
    root.render(
      <QueryClientProvider client={client}>
        <TooltipProvider>
          <TicketLinks ticketId="ticket-1" canEdit={canEdit} />
        </TooltipProvider>
      </QueryClientProvider>,
    );
  });
}

function input(index: number) {
  return document.querySelectorAll<HTMLInputElement>("[role='dialog'] input")[index];
}

async function type(target: HTMLInputElement, value: string) {
  const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!;
  await act(async () => {
    set.call(target, value);
    target.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

it("lets an agent add a provider link and shows it on the ticket", async () => {
  const addLink = vi.spyOn(api.tickets, "addLink").mockResolvedValue(addedLink);
  await render();

  const addButton = [...host.querySelectorAll("button")].find((button) => button.textContent?.trim() === "Add link")!;
  await act(async () => addButton.click());
  expect(document.querySelector("[role='dialog']")?.textContent).toContain("Add a link");

  await type(input(0), addedLink.url);
  await type(input(1), " ENG-123 ");
  await act(async () => document.querySelector<HTMLFormElement>("#add-ticket-link")!.requestSubmit());

  expect(addLink).toHaveBeenCalledWith("ticket-1", { url: addedLink.url, title: "ENG-123" });
  expect(client.getQueryData<TicketLink[]>(qk.ticketLinks("ticket-1"))).toEqual([addedLink]);
  expect(host.textContent).toContain("ENG-123");
  expect(document.querySelector("[role='dialog']")).toBeNull();
});

it("requires an HTTPS URL before submitting", async () => {
  const addLink = vi.spyOn(api.tickets, "addLink");
  await render();
  await act(async () => [...host.querySelectorAll("button")].find((button) => button.textContent?.trim() === "Add link")!.click());
  await type(input(0), "http://linear.app/acme/issue/ENG-123");
  await act(async () => document.querySelector<HTMLFormElement>("#add-ticket-link")!.requestSubmit());

  expect(addLink).not.toHaveBeenCalled();
  expect(document.querySelector("[role='alert']")?.textContent).toBe("Enter a full HTTPS URL.");
});

it("does not show an empty Linked section to a viewer", async () => {
  await render(false);
  expect(host.textContent).toBe("");
});
