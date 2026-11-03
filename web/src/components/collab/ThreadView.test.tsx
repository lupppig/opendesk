import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { ConfirmProvider } from "@/components/ui/confirm";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { TooltipProvider } from "@/components/ui/tooltip";
import { i18n } from "@/i18n";
import { setWorkspaceId } from "@/lib/api/client";
import { api, rows } from "@/lib/api/endpoints";
import type { MailAccount, MailThreadDetail } from "@/lib/api/types";
import { qk } from "@/lib/queries";
import { installMock } from "@/mock";
import { db } from "@/mock/db";
import { ThreadView } from "./ThreadView";

// Navigation belongs to the surrounding page; the conversation renders normally.
vi.mock("@tanstack/react-router", async (original) => ({ ...await original<typeof import("@tanstack/react-router")>(), useNavigate: () => vi.fn() }));

let detail: MailThreadDetail;
let account: MailAccount;
let qc: QueryClient;
let host: HTMLDivElement | null = null;
let root: ReturnType<typeof createRoot> | null = null;
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
beforeEach(async () => {
  installMock();
  setWorkspaceId(db.workspaces[0].workspace.id);
  await i18n.changeLanguage("en");
  const accounts = rows(await api.collab.accounts.list());
  account = accounts[0];
  const threads = await api.collab.threads.list({ account_id: account.id });
  detail = await api.collab.threads.get(threads.data[0].id);
  const message = detail.messages.find((m) => !m.draft);
  if (!message) throw new Error("Missing received message");
  detail = {
    ...detail, comments: [], can_act: true,
    account: { ...detail.account, reply_all: true },
    messages: [{ ...message, draft: false, outgoing: false, from: { name: "Sender", email: "sender@example.com" }, to: [{ name: "", email: account.email }], cc: [{ name: "Other", email: "other@example.com" }], reply_to: [] }],
  };
  qc = new QueryClient({ defaultOptions: { queries: { staleTime: Infinity, retry: false } } });
  qc.setQueryData(qk.collabStatus, { enabled: true });
  qc.setQueryData(qk.mailAccounts, []);
});
afterEach(async () => {
  if (root) await act(async () => root?.unmount());
  host?.remove();
  root = null;
  host = null;
  qc.clear();
  setWorkspaceId(null);
  vi.unstubAllGlobals();
});

function renderMarkup() {
  qc.setQueryData(qk.mailThread(detail.thread.id), detail);
  return renderToStaticMarkup(
    <QueryClientProvider client={qc}><TooltipProvider>
      <ThreadView threadId={detail.thread.id} onClose={() => undefined} onAction={() => undefined} markReadOnOpen={false} />
    </TooltipProvider></QueryClientProvider>,
  );
}

function alternateReplyButtons() {
  const html = renderMarkup();
  const doc = new DOMParser().parseFromString(html, "text/html");
  return [...doc.querySelectorAll("footer button[aria-label]")].map((b) => b.getAttribute("aria-label")).filter((name) => name === "Reply" || name === "Reply all");
}

it("keeps Reply selected by default without opening a composer", () => {
  const doc = new DOMParser().parseFromString(renderMarkup(), "text/html");
  const group = doc.querySelector('footer [role="group"]');
  expect(group?.getAttribute("aria-label")).toBe("Choose whether to reply to the sender or comment for your team");
  expect(group?.querySelector('button[aria-pressed="true"]')?.textContent).toBe("Reply");
  expect(doc.querySelector('footer [contenteditable="true"]')).toBeNull();
});

it("switches to an explicitly internal comment mode", async () => {
  vi.stubGlobal("matchMedia", () => ({ matches: true, addEventListener: vi.fn(), removeEventListener: vi.fn() }));
  vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} });
  vi.stubGlobal("requestAnimationFrame", vi.fn());
  qc.setQueryData(qk.mailThread(detail.thread.id), detail);
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  await act(async () => {
    root?.render(<QueryClientProvider client={qc}><TooltipProvider><ConfirmProvider>
      <ThreadView threadId={detail.thread.id} onClose={() => undefined} onAction={() => undefined} markReadOnOpen={false} />
    </ConfirmProvider></TooltipProvider></QueryClientProvider>);
  });
  const comment = [...host.querySelectorAll<HTMLButtonElement>("footer [role='group'] button")].find((button) => button.textContent === "Comment");
  expect(comment).toBeTruthy();
  await act(async () => comment?.click());
  expect(comment?.getAttribute("aria-pressed")).toBe("true");
  expect(host.textContent).toContain("Only your team sees this. It will not be sent to the sender.");
  expect(host.querySelector('footer [contenteditable="true"]')).not.toBeNull();
  const reply = host.querySelector<HTMLButtonElement>('footer [role="group"] button[aria-pressed="false"]');
  await act(async () => reply?.click());
  expect(reply?.getAttribute("aria-pressed")).toBe("true");
  expect(host.textContent).not.toContain("Only your team sees this. It will not be sent to the sender.");
});

it("uses the account's Reply all default and keeps Reply as the alternative", async () => {
  await api.collab.accounts.update(account.id, { reply_all: true });
  detail.account = (await api.collab.threads.get(detail.thread.id)).account;
  expect(alternateReplyButtons()).toEqual(["Reply"]);
});
it("uses the live account preference ahead of a stale conversation default", () => {
  qc.setQueryData(qk.mailAccounts, [{ ...account, reply_all: false }]);
  expect(alternateReplyButtons()).toEqual(["Reply all"]);
  detail.account.reply_all = false;
  qc.setQueryData(qk.mailAccounts, [{ ...account, reply_all: true }]);
  expect(alternateReplyButtons()).toEqual(["Reply"]);
});
it("keeps Reply first for disabled preferences and older servers without the field", () => {
  detail.account.reply_all = false;
  expect(alternateReplyButtons()).toEqual(["Reply all"]);
  delete detail.account.reply_all;
  expect(alternateReplyButtons()).toEqual(["Reply all"]);
});
it("offers only Reply when Reply all adds no recipient", () => {
  detail.messages[0].cc = [];
  expect(alternateReplyButtons()).toEqual([]);
});
