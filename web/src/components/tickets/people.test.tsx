import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeAll, beforeEach, describe, expect, it } from "vitest";
import { i18n, loadAgentCatalogs } from "@/i18n";
import { TooltipProvider } from "@/components/ui/tooltip";
import type { Viewer } from "@/lib/realtime/presence";
import { AssigneeMark, LiveViewers } from "./people";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

// The agent app loads its French catalogs before rendering (the root route's loader).
beforeAll(() => loadAgentCatalogs("fr"));

let container: HTMLDivElement;
let root: Root;

beforeEach(async () => {
  await i18n.changeLanguage("en");
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
});

const render = (node: React.ReactNode) => act(async () => root.render(<TooltipProvider>{node}</TooltipProvider>));
const text = () => container.textContent ?? "";
const visible = () => [...container.querySelectorAll("span")].filter((s) => !s.closest(".sr-only") && !s.children.length).map((s) => s.textContent).join(" ");

const chimene: Viewer = { user_id: "u1", name: "Chimène Dupont", state: "viewing" };
const sam: Viewer = { user_id: "u2", name: "Sam Okafor", state: "viewing" };
const assignee = { id: "u9", name: "Lea Martin", email: "lea@margin.test", avatar_url: null };

describe("who is on the ticket right now", () => {
  it("names one teammate viewing, in a green pill with a ringed avatar", async () => {
    await render(<LiveViewers viewers={[chimene]} />);
    const pill = container.querySelector("[data-live-presence]")!;
    expect(pill.className).toContain("bg-tone-green-soft");
    expect(pill.querySelector(".animate-live-ring")).not.toBeNull();
    expect(pill.querySelector(".motion-reduce\\:animate-none")).not.toBeNull();
    expect(text()).toContain("Chimène viewing");
  });

  it("says when they are replying", async () => {
    await render(<LiveViewers viewers={[{ ...chimene, state: "replying" }]} />);
    expect(text()).toContain("Chimène replying…");
  });

  it("counts several teammates, and names the one replying", async () => {
    await render(<LiveViewers viewers={[chimene, sam]} />);
    expect(text()).toContain("2 viewing");
    expect(container.querySelectorAll(".animate-live-ring")).toHaveLength(2);
    await render(<LiveViewers viewers={[chimene, { ...sam, state: "replying" }]} />);
    expect(text()).toContain("Sam replying… +1");
  });

  it("keeps to the ringed avatars when bare, and still says it in words", async () => {
    await render(<LiveViewers viewers={[chimene]} bare />);
    expect(container.querySelector(".animate-live-ring")).not.toBeNull();
    expect(container.querySelector(".sr-only")?.textContent).toBe("Chimène viewing");
    expect(visible()).not.toContain("viewing");
  });

  it("shows nothing when nobody is on it", async () => {
    await render(<LiveViewers viewers={[]} />);
    expect(container.querySelector("[data-live-presence]")).toBeNull();
  });

  it("reads in French", async () => {
    await i18n.changeLanguage("fr");
    await render(<LiveViewers viewers={[chimene, { ...sam, state: "replying" }]} />);
    expect(text()).toContain("Sam répond… +1");
  });
});

describe("who the ticket is assigned to", () => {
  it("shows their photo by default, without a live ring", async () => {
    await render(<AssigneeMark assignee={assignee} mode="photo" />);
    const mark = container.querySelector("[data-assignee]")!;
    expect(mark.querySelector('[title="Lea Martin"]')).not.toBeNull();
    expect(mark.querySelector(".animate-live-ring")).toBeNull();
    expect(mark.querySelector(".sr-only")?.textContent).toBe("Assigned to Lea Martin");
  });

  it("shows their first name in a neutral pill", async () => {
    await render(<AssigneeMark assignee={assignee} mode="name" />);
    const mark = container.querySelector("[data-assignee]")!;
    expect(mark.className).toContain("bg-tone-gray-soft");
    expect(visible()).toBe("Lea");
    expect(mark.querySelector(".sr-only")?.textContent).toBe("Assigned to Lea Martin");
  });

  it("shows nothing when the browser chose nothing", async () => {
    await render(<AssigneeMark assignee={assignee} mode="none" />);
    expect(container.querySelector("[data-assignee]")).toBeNull();
    expect(text()).toBe("");
  });
});
