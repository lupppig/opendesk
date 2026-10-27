import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import "@/i18n";
import { TooltipProvider } from "@/components/ui/tooltip";
import type { MailAccount, MailCategory, MailCategorySettings } from "@/lib/api/types";
import { qk } from "@/lib/queries";
import { CategorySetupDialog } from "./CategorySetup";

const setAccount = vi.fn();
vi.mock("@/lib/api/endpoints", () => ({ api: { collab: { categories: { suggest: vi.fn(), setAccount: (...a: unknown[]) => setAccount(...a) } } } }));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

afterEach(() => {
  document.body.innerHTML = "";
  setAccount.mockReset();
});

const preset = (key: string, name: string): MailCategory => ({ key, name, description: "", color: "blue" });
const PRESETS = ["primary", "updates", "newsletters", "promotions", "social"].map((k) => preset(k, k[0].toUpperCase() + k.slice(1)));
const account = { id: "a1", name: "Work", email: "work@example.com", categories: { enabled: false, categories: [], source: "none", team: null, mode: "follow", own: [], ai: true, locked: false } } as unknown as MailAccount;

it("starts like Gmail, then splits the inbox with that set as the account's own", async () => {
  setAccount.mockResolvedValue({ ...account, categories: { ...account.categories, enabled: true, mode: "custom" } });
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  qc.setQueryData<MailCategorySettings>(qk.mailCategories, { workspace: null, teams: [], presets: PRESETS, sorting: "rules", suggest: false });
  const onOpenChange = vi.fn();
  const host = document.createElement("div");
  document.body.appendChild(host);
  const root = createRoot(host);
  await act(async () =>
    root.render(
      <QueryClientProvider client={qc}>
        <TooltipProvider>
          <CategorySetupDialog account={account} open onOpenChange={onOpenChange} />
        </TooltipProvider>
      </QueryClientProvider>,
    ),
  );
  const button = (text: string) => [...document.querySelectorAll("button")].find((b) => b.textContent?.trim() === text);
  // Without an AI provider Ollie is not offered.
  expect(document.body.textContent).not.toContain("Ask Ollie");
  expect(document.body.textContent).toContain("Like Gmail");
  await act(async () => button("Continue")!.click());
  // The first step leaves before the editor comes in.
  const inputs = () => [...document.querySelectorAll<HTMLInputElement>('input[aria-label="Name"]')];
  for (let i = 0; i < 40 && !inputs().length; i++) await act(() => new Promise((r) => setTimeout(r, 25)));
  const names = inputs().map((i) => i.value);
  expect(names).toEqual(["Primary", "Updates", "Newsletters", "Promotions"]);
  await act(async () => button("Split my inbox")!.click());
  expect(setAccount).toHaveBeenCalledWith("a1", { mode: "custom", categories: PRESETS.slice(0, 4), ai: true });
  expect(onOpenChange).toHaveBeenCalledWith(false);
  await act(async () => root.unmount());
});
