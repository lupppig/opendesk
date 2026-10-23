import type { Page } from "@playwright/test";
import { expect, test } from "../lib/proof.ts";

async function openNewsletter(page: Page) {
  await page.getByRole("option", { name: /Wednesday Web Drop/ }).click();
  await expect(page.getByRole("heading", { name: "Wednesday Web Drop" })).toBeVisible();
  await page.getByRole("button", { name: "Not now" }).click();
}

async function openNewsletterAgain(page: Page) {
  await page.getByRole("option", { name: /Wednesday Web Drop/ }).click();
  await expect(page.getByRole("heading", { name: "Wednesday Web Drop" })).toBeVisible();
}

test("comment on a long email without scrolling to the end", async ({ page, proof }) => {
  await proof.open("/mail");
  await openNewsletter(page);
  const field = page.getByRole("textbox", { name: "Comment, @ to mention a teammate" });
  await expect(field).toBeInViewport();
  await proof.chapter("Pinned comments", "Writing a comment and replying stay under the conversation, however long the mail is");
  await proof.shot("pinned", { caption: "A long newsletter: the comment field, the 2 comments and the replies are pinned at the bottom" });

  await field.click();
  await field.pressSequentially("Plane is in Leads now, I'll reach out on Friday.", { delay: 40 });
  await expect(page.getByRole("button", { name: "Comment", exact: true })).toBeEnabled();
  await proof.shot("writing", { caption: "Writing opens the editor in place, with who reads it" });

  await page.keyboard.press("Control+Enter");
  const posted = page.getByRole("region", { name: "Team comments" }).getByText("Plane is in Leads now");
  await expect(posted).toBeInViewport();
  await proof.dwell();
  await proof.shot("posted", { caption: "Posted: the conversation scrolls to the new comment" });
});

test("jump to the comments and reply from the pinned bar", async ({ page, proof }) => {
  await proof.open("/mail");
  await openNewsletter(page);
  await proof.chapter("Shortcuts", "Shift C comments, the count jumps to the discussion, Reply stays a click away");

  await page.keyboard.press("Shift+C");
  await expect(page.getByRole("textbox", { name: "Comment, @ to mention a teammate" })).toBeFocused();
  await proof.shot("shortcut", { caption: "Shift C starts a comment from anywhere in the conversation" });
  await page.keyboard.press("Escape");

  await page.getByRole("button", { name: "2 comments" }).click();
  await expect(page.getByRole("region", { name: "Team comments" })).toBeInViewport();
  await proof.dwell();
  await proof.shot("comments", { caption: "The count jumps to the team's discussion" });

  await page.getByRole("button", { name: "Reply", exact: true }).click();
  await expect(page.getByRole("button", { name: "Send" }).first()).toBeVisible();
  await proof.dwell();
  await proof.shot("replying", { caption: "Replying: the reply buttons step aside, the comment field stays" });
});

test("an unsent comment waits for its conversation", async ({ page, proof }) => {
  await proof.open("/mail");
  await openNewsletter(page);
  await proof.chapter("Unsent comments", "A comment you started stays with its conversation while you read another");
  const field = page.getByRole("textbox", { name: "Comment, @ to mention a teammate" });
  await field.click();
  await field.pressSequentially("Check whether Quo has an API", { delay: 40 });

  await page.getByRole("option", { name: /Q4 renewal and seat count/ }).first().click();
  await expect(page.getByRole("heading", { name: /Q4 renewal and seat count/ })).toBeVisible();
  await expect(field).toHaveText("");
  await proof.dwell();
  await proof.shot("other-conversation", { caption: "Another conversation: its own empty comment field" });

  await openNewsletterAgain(page);
  await expect(field).toHaveText("Check whether Quo has an API");
  await proof.dwell();
  await proof.shot("unsent-kept", { caption: "Back on the newsletter: the comment is still there, ready to post" });
});

test("Cc in a forward opens its own field", async ({ page, proof }) => {
  await proof.open("/mail");
  await openNewsletter(page);
  await page.getByRole("button", { name: "Forward", exact: true }).click();
  const to = page.getByRole("combobox", { name: "To" });
  await expect(to).toBeVisible();
  await proof.chapter("Cc", "Adding Cc puts the cursor in Cc, and the To suggestions stay closed");
  await page.getByRole("button", { name: "Cc", exact: true }).click();
  await expect(page.getByRole("combobox", { name: "Cc" })).toBeFocused();
  await expect(to).toHaveAttribute("aria-expanded", "false");
  await proof.dwell();
  await proof.shot("forward-cc", { caption: "Forwarding: Cc added with the cursor in it and its own suggestions, To untouched" });
});

test("a toast's button stays on one line", async ({ page, proof }) => {
  await proof.open("/mail");
  await expect(page.getByRole("option", { name: /Wednesday Web Drop/ })).toBeVisible();
  // The toast a mention raises, with a long description beside its button.
  // The app's own toast module, loaded from the dev server inside the page.
  await page.evaluate(async (path) => {
    const { toast } = (await import(path)) as { toast: { info: (msg: string, opts: object) => void } };
    toast.info("You were mentioned #1034", { description: "Can guests comment without an account?", action: { label: "Open", onClick: () => undefined } });
  }, "/src/components/ui/toast.tsx");
  const toastEl = page.locator("[data-sonner-toast]").first();
  await expect(toastEl.getByRole("button", { name: "Open", exact: true })).toBeVisible();
  await proof.dwell();
  await proof.shot("toast-open", { caption: "A mention toast: the description wraps, Open stays whole", target: toastEl });
});

test("scroll the folder tabs beside an open conversation", async ({ page, proof }) => {
  await proof.open("/mail");
  await openNewsletter(page);
  const tabs = page.getByRole("navigation", { name: "Folders" });
  const right = tabs.getByRole("button", { name: "Scroll tabs right" });
  await expect(right).toBeVisible();
  await proof.chapter("Folder tabs", "The arrows scroll the tabs and stay where they went");
  await proof.shot("tabs-start", { caption: "A narrow list: more tabs to the right", target: tabs });

  await right.click();
  await expect(tabs.getByRole("link", { name: /Archive/ })).toBeInViewport();
  await proof.dwell();
  await proof.shot("tabs-right", { caption: "Scrolled right: Drafts and Archive in view, the open Inbox tab does not pull it back", target: tabs });

  await tabs.getByRole("button", { name: "Scroll tabs left" }).click();
  await expect(tabs.getByRole("link", { name: /Inbox/ })).toBeInViewport();
  await proof.dwell();
  await proof.shot("tabs-back", { caption: "And back to the start", target: tabs });
});

test.describe("dark", () => {
  test.use({ colorScheme: "dark" });

  test("the pinned comment bar in the dark theme", async ({ page, proof }) => {
    // The app is white first: dark is the member's own choice.
    await page.addInitScript(() => localStorage.setItem("od-theme", "dark"));
    await proof.open("/mail");
    await openNewsletter(page);
    await expect(page.getByRole("textbox", { name: "Comment, @ to mention a teammate" })).toBeInViewport();
    await proof.shot("pinned-dark", { caption: "The pinned bar in the dark theme" });
  });
});

test("a reply shows what is new and keeps the quoted mail a click away", async ({ page, proof }) => {
  await proof.open("/mail");
  await page.getByRole("option", { name: /Your application to Fieldway/ }).first().click();
  await expect(page.getByRole("heading", { name: "Your application to Fieldway" })).toBeVisible();
  await page.getByRole("button", { name: "Not now" }).click();
  const latest = page.getByRole("article", { name: "Show message from Jade Morrow" });
  await expect(latest.getByText("what's holding you back")).toBeVisible();
  await expect(latest.getByText("Growth @ Fieldway")).toBeVisible();
  await expect(latest.getByText(/wrote:/)).toHaveCount(0);
  await proof.chapter("Quoted text", "A reply shows what the sender wrote; the earlier mail their client copied in folds away");
  await proof.shot("quoted-folded", { caption: "The newest reply: the message and its signature, the earlier mail folded into ..." });

  await latest.getByRole("button", { name: "Show quoted text" }).click();
  await expect(latest.getByText(/Jade Morrow <jade@fieldway.io> wrote:/)).toBeVisible();
  await proof.dwell();
  await proof.shot("quoted-open", { caption: "Asked for: the quoted mail opens under the reply, set off by a rule" });

  await latest.getByRole("button", { name: "Hide quoted text" }).click();
  await expect(latest.getByText(/wrote:/)).toHaveCount(0);
  await proof.dwell();
});

test("fold a message from its header and read how it came", async ({ page, proof }) => {
  await proof.open("/mail");
  await page.getByRole("option", { name: /Your application to Fieldway/ }).first().click();
  await expect(page.getByRole("heading", { name: "Your application to Fieldway" })).toBeVisible();
  await page.getByRole("button", { name: "Not now" }).click();
  const latest = page.getByRole("article", { name: "Show message from Jade Morrow" });
  await expect(latest.getByText("what's holding you back")).toBeVisible();
  await proof.chapter("Message details", "Click the header to fold a message; the arrow beside its recipients shows how it came");

  await latest.locator("header time").click();
  await expect(latest).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Show message from Jade Morrow" }).last()).toBeFocused();
  await proof.dwell();
  await proof.shot("header-folded", { caption: "A click on the header folds the message: the row keeps the avatar, name and time where they were" });

  await page.getByRole("button", { name: "Show message from Jade Morrow" }).last().click();
  await expect(latest.getByText("what's holding you back")).toBeVisible();
  await latest.getByRole("button", { name: "Show details" }).click();
  const card = page.getByRole("dialog").filter({ hasText: "mailed by" });
  await expect(card.getByText("fieldway.io", { exact: true }).first()).toBeVisible();
  await expect(card.getByText("Standard encryption (TLS)")).toBeVisible();
  await proof.dwell();
  await proof.shot("details", { caption: "Details: who it is from and to, mailed by, signed by, encryption and the sender checks" });

  await card.getByRole("button", { name: "Show original headers" }).click();
  const headers = page.getByRole("dialog", { name: "Original headers" });
  await expect(headers.getByText("Authentication-Results:")).toBeVisible();
  await proof.dwell();
  await proof.shot("headers", { caption: "The original headers, in order, ready to copy" });
});

test("mail that fails its sender's checks carries a warning", async ({ page, proof }) => {
  await proof.open("/mail");
  await page.getByRole("option", { name: /Your account access has been limited/ }).first().click();
  await expect(page.getByRole("heading", { name: "Your account access has been limited" })).toBeVisible();
  await page.getByRole("button", { name: "Not now" }).click();
  const msg = page.getByRole("article", { name: "Show message from PayPal" });
  await expect(msg.getByRole("alert")).toContainText("Be careful with this message");
  await expect(msg.getByText("via mail-relay.top")).toBeVisible();
  await proof.chapter("Sender warning", "A message that only claims to be from PayPal fails DMARC");
  await proof.shot("spoofed", { caption: "The warning above a message that failed its sender's checks, and the domain it came through" });

  await msg.getByRole("button", { name: "Show details" }).click();
  const card = page.getByRole("dialog").filter({ hasText: "mailed by" });
  await expect(card.getByText("DMARC fail")).toBeVisible();
  await proof.dwell();
  await proof.shot("spoofed-details", { caption: "Its details: mailed by another domain, no signature, DMARC failed, not encrypted" });
});

test("fold a long email from its header while reading it", async ({ page, proof }) => {
  await proof.open("/mail");
  await openNewsletter(page);
  const mail = page.getByRole("article", { name: "Show message from Webdrop" });
  const header = mail.locator("header");
  await header.hover();
  await page.mouse.wheel(0, 1400);
  await expect(header.getByText("newsletter@webdrop.dev")).toBeInViewport();
  await proof.chapter("Always in reach", "Deep in a long email, its header stays on top, one click from folding");
  await proof.dwell();
  await proof.shot("sticky-header", { caption: "Scrolled through a long newsletter: its header stays at the top" });

  await header.locator("time").click();
  const row = page.getByRole("button", { name: "Show message from Webdrop" });
  await expect(row).toBeInViewport();
  await expect(row).toBeFocused();
  await proof.dwell();
  await proof.shot("long-folded", { caption: "Folded: the row stays in view, ready to open again" });
});
