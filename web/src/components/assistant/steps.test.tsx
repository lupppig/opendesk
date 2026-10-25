import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeAll, expect, it } from "vitest";
import { i18n, loadAgentCatalogs } from "@/i18n";
import type { AssistantStep } from "./api";
import { StepRow } from "./Steps";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

beforeAll(() => loadAgentCatalogs("fr"));
afterEach(async () => {
  await i18n.changeLanguage("en");
  document.body.innerHTML = "";
});

async function render(step: AssistantStep) {
  const host = document.createElement("div");
  document.body.appendChild(host);
  const root = createRoot(host);
  await act(async () => root.render(<StepRow step={step} />));
  return { host, unmount: () => act(async () => root.unmount()) };
}

const ran: AssistantStep = {
  id: "c1",
  tool: "update_ticket",
  label: "Updated the ticket",
  status: "done",
  summary: "Updated #42",
  action_id: "a1",
  auto: true,
  preview: { title: "Update #42", changes: [{ field: "Priority", from: "normal", to: "urgent" }], arguments: '{\n  "priority": "urgent"\n}' },
};

it("marks a change that ran without asking and opens what it ran", async () => {
  const { host, unmount } = await render(ran);
  expect(host.querySelector("[data-step]")?.getAttribute("data-step")).toBe("done");
  expect(host.querySelector("[data-auto]")?.textContent).toBe("Ran without asking");
  const toggle = [...host.querySelectorAll("button")].find((b) => b.textContent === "Show what it ran")!;
  expect(toggle.getAttribute("aria-expanded")).toBe("false");
  expect(host.textContent).not.toContain("urgent");
  await act(async () => toggle.click());
  expect(toggle.getAttribute("aria-expanded")).toBe("true");
  expect(host.textContent).toContain("Update #42");
  expect(host.textContent).toContain("urgent");
  expect(host.querySelector("pre")?.textContent).toBe(ran.preview!.arguments);
  await unmount();
});

it("shows no mark on an approved change, and speaks French", async () => {
  let r = await render({ ...ran, auto: undefined, preview: undefined });
  expect(r.host.querySelector("[data-auto]")).toBeNull();
  expect(r.host.textContent).not.toContain("Show what it ran");
  await r.unmount();
  await i18n.changeLanguage("fr");
  r = await render(ran);
  expect(r.host.querySelector("[data-auto]")?.textContent).toBe("Fait sans demander");
  await r.unmount();
});
