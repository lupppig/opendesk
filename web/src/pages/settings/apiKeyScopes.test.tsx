import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { i18n, loadAgentCatalogs } from "@/i18n";
import { ApiError } from "@/lib/api/client";
import type { ApiKey } from "@/lib/api/types";
import { qk } from "@/lib/queries";
import { useSession } from "@/lib/session";
import { ScopesDialog } from "./ApiKeys";

const update = vi.fn<(id: string, b: { scopes?: string[] }) => Promise<ApiKey>>();
vi.mock("@/lib/api/endpoints", () => ({ api: { apiKeys: { update: (id: string, b: { scopes?: string[] }) => update(id, b) } } }));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
// The scope checkboxes measure themselves; jsdom has no ResizeObserver.
globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
};

const key = (over: Partial<ApiKey> = {}): ApiKey =>
  ({
    id: "k1",
    name: "Claimy",
    prefix: "od_live_abcd1234",
    scopes: ["tickets:read"],
    last_used_at: null,
    expires_at: null,
    created_at: "2026-11-02T10:00:00Z",
    created_by: null,
    private_team_ids: [],
    private_teams: [],
    ...over,
  }) as ApiKey;

let root: Root;
let qc: QueryClient;
const onClose = vi.fn();

beforeAll(() => loadAgentCatalogs("fr"));
beforeEach(() => {
  update.mockReset();
  onClose.mockReset();
  useSession.setState({ user: { id: "me", email: "owner@example.com" } as never, workspaceId: "w1" });
});
afterEach(async () => {
  await act(async () => root.unmount());
  await i18n.changeLanguage("en");
  document.body.innerHTML = "";
});

async function render(k: ApiKey) {
  qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  qc.setQueryData(qk.apiKeys, [k]);
  const host = document.createElement("div");
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => root.render(<QueryClientProvider client={qc}>{<ScopesDialog apiKey={k} onClose={onClose} />}</QueryClientProvider>));
}

const settle = () => act(() => new Promise((r) => setTimeout(r, 20)));
const button = (label: string) => [...document.querySelectorAll("button")].find((b) => b.textContent === label)!;
const box = (label: string) => document.querySelector<HTMLElement>(`[aria-label="${label}"]`)!;
const click = (el: HTMLElement) => act(async () => el.click());
const text = () => document.body.textContent ?? "";

describe("Edit permissions", () => {
  it("adds a scope, says it needs a recent sign-in, and saves the new list", async () => {
    update.mockImplementation(async (_id, b) => key({ scopes: b.scopes }));
    await render(key());
    const save = button("Save permissions");
    expect(save.disabled).toBe(true);
    await click(box("Tickets write"));
    expect(text()).toContain("+ tickets:write");
    expect(text()).toContain("Adding a permission asks for a recent sign-in");
    expect(save.disabled).toBe(false);
    await click(save);
    await settle();
    expect(update).toHaveBeenCalledWith("k1", { scopes: ["tickets:read", "tickets:write"] });
    expect(onClose).toHaveBeenCalled();
    expect(qc.getQueryData<ApiKey[]>(qk.apiKeys)?.[0].scopes).toEqual(["tickets:read", "tickets:write"]);
  });

  it("removes a scope without the sign-in notice", async () => {
    update.mockImplementation(async (_id, b) => key({ scopes: b.scopes }));
    await render(key({ scopes: ["contacts:read", "tickets:read"] }));
    await click(box("Contacts read"));
    expect(text()).toContain("− contacts:read");
    expect(text()).not.toContain("Adding a permission");
    await click(button("Save permissions"));
    await settle();
    expect(update).toHaveBeenCalledWith("k1", { scopes: ["tickets:read"] });
  });

  it("explains a refused addition and offers to sign in again, keeping the dialog open", async () => {
    update.mockRejectedValue(
      new ApiError({ status: 403, code: "reauth_required", title: "Forbidden", detail: "For your security, sign in again to do this. Your last sign-in is more than 12 hours old." }),
    );
    await render(key());
    await click(box("Tickets write"));
    await click(button("Save permissions"));
    await settle();
    expect(document.querySelector('[role="alert"]')?.textContent).toContain("12");
    expect(onClose).not.toHaveBeenCalled();
  });

  it("is in French", async () => {
    await i18n.changeLanguage("fr");
    await render(key());
    expect(text()).toContain("Autorisations de Claimy");
    expect(button("Enregistrer les autorisations")).toBeTruthy();
  });
});
