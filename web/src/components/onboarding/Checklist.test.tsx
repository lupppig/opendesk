import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import type { ReactNode } from "react";
import "@/i18n";
import { TooltipProvider } from "@/components/ui/tooltip";
import { Checklist, progressOf, stepsLeft, type Onboarding, type OnboardingStepId } from "./Checklist";

const ids: OnboardingStepId[] = ["connect_mailbox", "invite_teammate", "upload_photo", "install_widget", "publish_article", "setup_triage"];
const data = (doneCount: number, extra: Partial<Onboarding> = {}): Onboarding => ({
  steps: ids.map((id, i) => ({ id, title: `Step ${id}`, description: "", done: i < doneCount, href: `/x/${id}`, requires: id === "setup_triage" ? "admin" : "agent" })),
  done_count: doneCount,
  total: ids.length,
  dismissed: false,
  ...extra,
});

const render = (n: ReactNode) => renderToStaticMarkup(<TooltipProvider>{n}</TooltipProvider>);

describe("onboarding checklist", () => {
  it("counts progress from the steps", () => {
    expect(progressOf(data(3))).toEqual({ done: 3, total: 6, pct: 0.5, all: false });
    expect(progressOf(data(6)).all).toBe(true);
    expect(progressOf({ steps: [] })).toEqual({ done: 0, total: 0, pct: 0, all: false });
  });

  it("shows 3 of 6 and a progressbar", () => {
    const html = render(<Checklist data={data(3)} onDismiss={() => {}} />);
    expect(html).toContain("3 of 6 steps done");
    expect(html).toContain('aria-valuenow="3"');
    expect(html).toContain('aria-valuemax="6"');
    expect(html).toContain("Get set up");
  });

  it("disables admin steps for non-admins with a reason", () => {
    const html = render(<Checklist data={data(0)} onDismiss={() => {}} isAdmin={false} />);
    expect(html).toContain("An admin sets this up");
    expect(html).not.toContain('href="/x/setup_triage"');
    expect(html).toContain('href="/x/connect_mailbox"');
  });

  it("renders the all-set state when everything is done, and nothing when dismissed", () => {
    expect(render(<Checklist data={data(6)} onDismiss={() => {}} />)).toContain("You&#x27;re all set");
    expect(render(<Checklist data={data(2, { dismissed: true })} onDismiss={() => {}} />)).toBe("");
  });
});

describe("stepsLeft", () => {
  it("counts every open step for an admin", () => {
    expect(stepsLeft(data(4), { isAdmin: true })).toBe(2);
    expect(stepsLeft(data(6), { isAdmin: true })).toBe(0);
  });
  it("leaves out what a member cannot do: admin steps and steps waiting on AI", () => {
    // Open: publish_article (agent) and setup_triage (admin).
    expect(stepsLeft(data(4), { isAdmin: false })).toBe(1);
    expect(stepsLeft(data(4), { isAdmin: false, unavailable: { publish_article: "needs AI" } })).toBe(0);
    expect(stepsLeft(data(4), { isAdmin: true, unavailable: { publish_article: "needs AI" } })).toBe(2);
  });
});
