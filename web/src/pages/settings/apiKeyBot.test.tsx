import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { usageCount } from "@/components/bots/bits";
import { loadAgentCatalogs } from "@/i18n";
import type { ApiKey, Bot } from "@/lib/api/types";
import { qk } from "@/lib/queries";
import { useSession } from "@/lib/session";
import { CreateKeyDialog } from "./ApiKeys";

const look = { kind: "built", shape: "blob", fill: { type: "gradient", color: "#c9bcff", to: "#4a2bc4" }, eyes: "pills", mouth: "none", accessory: "none", accent: "#f59e0b", blush: false, background: "none", emoji: "" };
const julia = { id: "bot-julia", name: "Julia", role: "", look, image_url: null, is_default: false, usage: { widgets: [], mailboxes: [], api_keys: [] } } as unknown as Bot;

const create = vi.fn<(b: unknown) => Promise<ApiKey & { secret: string }>>();
vi.mock("@/lib/api/endpoints", async (orig) => ({
  ...(await orig<object>()),
  api: { apiKeys: { create: (b: unknown) => create(b) }, bots: { list: async () => ({ data: [julia] }) } },
}));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
};
// The bot picker scrolls its list to the chosen bot; jsdom has no layout.
Element.prototype.scrollIntoView ??= () => {};
Element.prototype.hasPointerCapture ??= () => false;
Element.prototype.releasePointerCapture ??= () => {};

let root: Root;

beforeAll(() => loadAgentCatalogs("en"));
beforeEach(() => {
  create.mockReset();
  create.mockResolvedValue({ id: "k1", name: "Replai", secret: "od_live_x" } as ApiKey & { secret: string });
  useSession.setState({ user: { id: "me", email: "owner@example.com" } as never, workspaceId: "w1" });
});
afterEach(async () => {
  await act(async () => root.unmount());
  document.body.innerHTML = "";
});

async function render() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  qc.setQueryData(qk.teams, []);
  const host = document.createElement("div");
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => root.render(<QueryClientProvider client={qc}><CreateKeyDialog onClose={() => {}} onCreated={() => {}} /></QueryClientProvider>));
}

const settle = () => act(() => new Promise((r) => setTimeout(r, 20)));
const button = (label: string) => [...document.querySelectorAll("button")].find((b) => b.textContent === label)!;
const label = (text: string) => [...document.querySelectorAll("label")].find((l) => l.textContent === text);

async function name(value: string) {
  const input = document.querySelector<HTMLInputElement>("input[placeholder]")!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

describe("A key that speaks as a bot", () => {
  it("is offered only to a key that writes tickets", async () => {
    await render();
    expect(label("Speaks as")).toBeUndefined();
    await act(async () => button("Full access").click());
    await settle();
    expect(label("Speaks as")).toBeDefined();
    expect(document.body.textContent).toContain("No bot: it writes as the API");
  });

  it("sends no bot until one is picked, then the one picked", async () => {
    await render();
    await name("Replai");
    await act(async () => button("Full access").click());
    await act(async () => button("Create key").click());
    await settle();
    expect(create).toHaveBeenLastCalledWith(expect.not.objectContaining({ bot_id: expect.anything() }));

    const picker = document.getElementById(label("Speaks as")!.htmlFor)!;
    await act(async () => picker.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true })));
    await settle();
    const option = [...document.querySelectorAll('[role="option"]')].find((o) => o.textContent?.includes("Julia")) as HTMLElement;
    await act(async () => option.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
    await settle();
    await act(async () => button("Create key").click());
    await settle();
    expect(create).toHaveBeenLastCalledWith(expect.objectContaining({ name: "Replai", bot_id: "bot-julia" }));
  });
});

describe("Where a bot answers", () => {
  it("counts the API keys that speak as it", () => {
    expect(usageCount({ ...julia, usage: { widgets: [], mailboxes: [], api_keys: [{ id: "k1", name: "Replai" }] } })).toBe(1);
    expect(usageCount({ ...julia, usage: { widgets: [], mailboxes: [] } })).toBe(0);
  });
});
