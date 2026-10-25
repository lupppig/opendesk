import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeAll, expect, it } from "vitest";
import { i18n, loadAgentCatalogs } from "@/i18n";
import { q } from "@/lib/queries";
import type { Approval } from "./api";
import { ApprovalCard } from "./Steps";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

beforeAll(() => loadAgentCatalogs("fr"));
afterEach(async () => {
  await i18n.changeLanguage("en");
  document.body.innerHTML = "";
});

const args = '{\n  "title": "Login loop",\n  "team": "Support",\n  "labels": [\n    "bug"\n  ]\n}';

function approval(preview: Approval["preview"]): Approval {
  return { action_id: "a1", call_id: "c1", tool: "linear__create_issue", preview, token: "t", expires_at: new Date().toISOString() };
}

async function render(a: Approval) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  qc.setQueryData(q.aiConfig().queryKey, { assistant_name: "Ollie" } as never);
  const host = document.createElement("div");
  document.body.appendChild(host);
  const root = createRoot(host);
  await act(async () =>
    root.render(
      <QueryClientProvider client={qc}>
        <ApprovalCard approval={a} busy={false} onDecide={() => {}} />
      </QueryClientProvider>,
    ),
  );
  return { host, unmount: () => act(async () => root.unmount()) };
}

it("says what the summary leaves out and opens every argument as it is sent", async () => {
  const { host, unmount } = await render(
    approval({ title: "Linear: Create issue", system: "Linear", changes: [{ field: "Title", from: "", to: "Login loop" }], arguments: args, omitted: 2, clipped: true }),
  );
  expect(host.textContent).toContain("2 more fields");
  expect(host.textContent).toContain("Some values are cut short");
  const toggle = [...host.querySelectorAll("button")].find((b) => b.textContent === "Show all arguments")!;
  expect(toggle.getAttribute("aria-expanded")).toBe("false");
  expect(host.querySelector("pre")).toBeNull();
  await act(async () => toggle.click());
  const pre = host.querySelector("pre")!;
  expect(toggle.getAttribute("aria-expanded")).toBe("true");
  expect(toggle.getAttribute("aria-controls")).toBe(pre.id);
  expect(pre.textContent).toBe(args);
  expect(pre.tabIndex).toBe(0);
  expect(toggle.textContent).toBe("Hide arguments");
  await unmount();
});

it("shows only the disclosure when the summary is complete, and nothing without arguments", async () => {
  let r = await render(approval({ title: "Linear: Create issue", changes: [{ field: "Title", from: "", to: "Login loop" }], arguments: args }));
  expect(r.host.textContent).not.toContain("more field");
  expect(r.host.textContent).toContain("Show all arguments");
  await r.unmount();
  r = await render(approval({ title: "Update #4", changes: [{ field: "Status", from: "open", to: "solved" }] }));
  expect(r.host.textContent).not.toContain("Show all arguments");
  await r.unmount();
});

it("speaks French", async () => {
  await i18n.changeLanguage("fr");
  const { host, unmount } = await render(approval({ title: "Linear: Create issue", arguments: args, omitted: 3 }));
  expect(host.textContent).toContain("3 champs de plus");
  expect(host.textContent).toContain("Afficher tous les arguments");
  await unmount();
});
