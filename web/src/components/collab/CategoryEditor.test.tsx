import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, useState } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import "@/i18n";
import { TooltipProvider } from "@/components/ui/tooltip";
import type { MailCategory, MailCategorySettings } from "@/lib/api/types";
import { qk } from "@/lib/queries";
import { CategoryEditor } from "./CategoryEditor";

vi.mock("@/lib/api/endpoints", () => ({ api: { collab: { categories: { suggest: vi.fn() } } } }));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

afterEach(() => {
  document.body.innerHTML = "";
});

const preset = (key: string, name: string): MailCategory => ({ key, name, description: `${name} mail`, color: "blue" });
const PRESETS = [preset("primary", "Primary"), preset("updates", "Updates"), preset("newsletters", "Newsletters"), preset("promotions", "Promotions")];

async function render(initial: MailCategory[], suggest = false) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  qc.setQueryData<MailCategorySettings>(qk.mailCategories, { workspace: null, teams: [], presets: PRESETS, sorting: "rules", suggest });
  const seen: MailCategory[][] = [];
  function Harness() {
    const [value, setValue] = useState(initial);
    return (
      <CategoryEditor
        value={value}
        onChange={(v) => {
          seen.push(v);
          setValue(v);
        }}
      />
    );
  }
  const host = document.createElement("div");
  document.body.appendChild(host);
  const root = createRoot(host);
  await act(async () =>
    root.render(
      <QueryClientProvider client={qc}>
        <TooltipProvider>
          <Harness />
        </TooltipProvider>
      </QueryClientProvider>,
    ),
  );
  const names = () => [...host.querySelectorAll<HTMLInputElement>('input[aria-label="Name"]')].map((i) => i.value);
  const button = (text: string) => [...host.querySelectorAll("button")].find((b) => b.textContent?.trim() === text);
  return { host, names, button, seen, unmount: () => act(async () => root.unmount()) };
}

it("keeps Primary first without a remove button, and offers the presets not in the list", async () => {
  const { host, names, button, unmount } = await render([PRESETS[0], PRESETS[1]]);
  expect(names()).toEqual(["Primary", "Updates"]);
  expect(host.querySelector('button[aria-label="Remove Primary"]')).toBeNull();
  expect(host.querySelector('button[aria-label="Remove Updates"]')).not.toBeNull();
  expect(button("Updates")).toBeUndefined();
  expect(button("Newsletters")).toBeDefined();
  expect(host.textContent).toContain("2 of 8");
  await unmount();
});

it("adds a preset with its description, and it leaves the chips", async () => {
  const { names, button, seen, unmount } = await render([PRESETS[0], PRESETS[1]]);
  await act(async () => button("Promotions")!.click());
  expect(names()).toEqual(["Primary", "Updates", "Promotions"]);
  expect(seen.at(-1)?.[2]).toEqual(PRESETS[3]);
  expect(button("Promotions")).toBeUndefined();
  await unmount();
});

it("says when a set has too few categories, and offers Ollie only when it can suggest", async () => {
  const { host, button, unmount } = await render([PRESETS[0], PRESETS[1]]);
  expect(button("Suggest with Ollie")).toBeUndefined();
  await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="Remove Updates"]')!.click());
  expect(host.querySelector('[role="alert"]')?.textContent).toBe("Use between 2 and 8 categories.");
  await unmount();
  const withOllie = await render([PRESETS[0], PRESETS[1]], true);
  expect(withOllie.button("Suggest with Ollie")).toBeDefined();
  await withOllie.unmount();
});
