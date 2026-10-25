import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { TooltipProvider } from "@/components/ui/tooltip";
import type { MailMessage } from "@/lib/api/types";
import { MailMessageItem } from "./MailMessageItem";

vi.mock("./MailDetails", () => ({
  useMessageDetails: () => ({ data: undefined }),
  DetailsTrigger: () => null,
  SenderWarning: () => null,
  Via: () => null,
}));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
});

const own = { name: "Support", email: "support@ours.test" };

function message(outgoing: boolean): MailMessage {
  return {
    id: "m1",
    thread_id: "t1",
    account_id: "a1",
    message_id: "x@ours.test",
    // The account's own address, as anyone can write it into a From.
    from: own,
    to: [{ name: "", email: "someone@else.test" }],
    cc: [],
    bcc: [],
    reply_to: [],
    subject: "Hello",
    snippet: "Hello",
    body_html: '<p>Hello</p><img src="https://tracker.example/pixel.png" alt="">',
    body_text: "Hello",
    sent_at: new Date().toISOString(),
    unread: false,
    starred: false,
    draft: false,
    folder_ids: [],
    attachments: [],
    sent_by: null,
    outgoing,
  };
}

describe("MailMessageItem", () => {
  it("holds back remote images of mail the account did not write, whatever its From says", async () => {
    await act(async () => root.render(<TooltipProvider><MailMessageItem m={message(false)} open onToggle={() => undefined} /></TooltipProvider>));
    expect(container.innerHTML).not.toContain("tracker.example");
  });

  it("shows remote images of mail the account wrote", async () => {
    await act(async () => root.render(<TooltipProvider><MailMessageItem m={message(true)} open onToggle={() => undefined} /></TooltipProvider>));
    expect(container.innerHTML).toContain("tracker.example");
  });
});
