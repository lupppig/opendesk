import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import "@/i18n";
import { i18n } from "@/i18n";
import type { AIDraftRef, MailMessage } from "@/lib/api/types";
import { AIDraftFeedback } from "./AIDraftFeedback";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let container: HTMLDivElement;
let root: Root;

beforeEach(async () => {
  await i18n.changeLanguage("en");
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
});

function message(ai_draft: AIDraftRef | null, draft = false): MailMessage {
  return {
    id: "m-1",
    thread_id: "thread-1",
    account_id: "account-1",
    message_id: "m-1@example.test",
    from: { name: "Support", email: "support@example.test" },
    to: [{ name: "Frank", email: "frank@example.test" }],
    cc: [],
    bcc: [],
    reply_to: [],
    subject: "Re: Quote",
    snippet: "About $2 per case",
    body_html: "<p>About $2 per case</p>",
    body_text: "About $2 per case",
    sent_at: new Date().toISOString(),
    unread: false,
    starred: false,
    draft,
    folder_ids: [],
    attachments: [],
    sent_by: null,
    ai_draft,
    outgoing: true,
  };
}

const render = (m: MailMessage) =>
  act(async () =>
    root.render(
      <QueryClientProvider client={new QueryClient()}>
        <AIDraftFeedback m={m} />
      </QueryClientProvider>,
    ),
  );

describe("Collab AI draft feedback", () => {
  it("names the AI draft a sent message came from and asks for a reason before saving", async () => {
    await render(message({ id: "ai-1", client: "Local drafter", outcome: "sent_edited", has_feedback: false }));
    expect(container.textContent).toContain("From AI draft by Local drafter · edited before sending");

    await act(async () => container.querySelector("button")?.click());
    const dialog = document.querySelector("[role=dialog]");
    expect(dialog?.textContent).toContain("Feedback on this AI draft");
    const save = [...(dialog?.querySelectorAll("button") ?? [])].find((b) => b.textContent === "Save feedback");
    expect(save?.disabled).toBe(true);
  });

  it("offers to edit feedback already given, and shows nothing on drafts or a person's mail", async () => {
    await render(message({ id: "ai-1", client: "Local drafter", outcome: "sent_unchanged", has_feedback: true }));
    expect(container.textContent).toContain("sent as written");
    expect(container.textContent).toContain("Edit feedback");

    await render(message({ id: "ai-1", client: "Local drafter", outcome: null, has_feedback: false }, true));
    expect(container.textContent).toBe("");
    await render(message(null));
    expect(container.textContent).toBe("");
  });
});
