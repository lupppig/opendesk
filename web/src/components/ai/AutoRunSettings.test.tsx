import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeAll, beforeEach, expect, it, vi } from "vitest";
import { i18n, loadAgentCatalogs } from "@/i18n";
import type { AIConfig, AISettings } from "@/lib/api/types";
import { qk } from "@/lib/queries";
import { AutoRunSettings } from "./AutoRunSettings";

const update = vi.fn<(b: Partial<AISettings>) => Promise<AISettings>>();
vi.mock("@/lib/api/endpoints", () => ({ api: { ai: { updateSettings: (b: Partial<AISettings>) => update(b) } } }));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

beforeAll(() => loadAgentCatalogs("fr"));
beforeEach(() => {
  update.mockReset();
});
afterEach(async () => {
  await i18n.changeLanguage("en");
  document.body.innerHTML = "";
});

const features = { enabled: true, assistant: true, assistant_auto: ["add_note"] } as AISettings;

async function render(disabled = false) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  qc.setQueryData<AIConfig>(qk.aiConfig, { enabled: true, features } as AIConfig);
  const host = document.createElement("div");
  document.body.appendChild(host);
  const root = createRoot(host);
  await act(async () =>
    root.render(
      <QueryClientProvider client={qc}>
        <AutoRunSettings name="Ollie" disabled={disabled} />
      </QueryClientProvider>,
    ),
  );
  const sw = (label: string) => host.querySelector<HTMLButtonElement>(`button[role="switch"][aria-label="${label}"]`)!;
  return { host, qc, sw, unmount: () => act(async () => root.unmount()) };
}

it("shows one labelled switch per change, with the ones that always ask", async () => {
  const { host, sw, unmount } = await render();
  expect(host.querySelectorAll('button[role="switch"]')).toHaveLength(6);
  expect(sw("Add an internal note").getAttribute("aria-checked")).toBe("true");
  expect(sw("Update a ticket").getAttribute("aria-checked")).toBe("false");
  expect(host.textContent).toContain("Replies, messages to people outside the team and changes to other settings always ask first");
  expect(host.querySelector('input[type="checkbox"]')).toBeNull();
  await unmount();
});

it("saves a switch at once and shows it on before the answer", async () => {
  let resolve!: (v: AISettings) => void;
  update.mockImplementation(() => new Promise((r) => (resolve = r)));
  const { qc, sw, unmount } = await render();
  await act(async () => sw("Update a ticket").click());
  // In the order of the switches.
  expect(update).toHaveBeenCalledWith({ assistant_auto: ["update_ticket", "add_note"] });
  expect(sw("Update a ticket").getAttribute("aria-checked")).toBe("true");
  await act(async () => resolve({ ...features, assistant_auto: ["update_ticket", "add_note"] }));
  expect(qc.getQueryData<AIConfig>(qk.aiConfig)?.features.assistant_auto).toEqual(["update_ticket", "add_note"]);
  await unmount();
});

it("puts a switch back when the save fails", async () => {
  update.mockRejectedValue(new Error("nope"));
  const { sw, unmount } = await render();
  await act(async () => sw("Add an internal note").click());
  expect(update).toHaveBeenCalledWith({ assistant_auto: [] });
  expect(sw("Add an internal note").getAttribute("aria-checked")).toBe("true");
  await unmount();
});

it("is read only when disabled, and speaks French", async () => {
  await i18n.changeLanguage("fr");
  const { host, sw, unmount } = await render(true);
  expect(sw("Ajouter une note interne").disabled).toBe(true);
  expect(host.textContent).toContain("Modifications faites par Ollie sans demander");
  await unmount();
});
