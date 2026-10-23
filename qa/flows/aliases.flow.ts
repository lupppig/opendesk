import type { Page } from "@playwright/test";
import { expect, test } from "../lib/proof.ts";

const quietTips = (page: Page) => page.addInitScript(() => localStorage.setItem("od-ollie-tips", JSON.stringify({ paused: Number.MAX_SAFE_INTEGER })));

async function dismissNudge(page: Page) {
  const nudge = page.getByRole("button", { name: "Not now" });
  if (await nudge.isVisible()) await nudge.click();
}

test("give an alias its own name, signature and brand, and add one Gmail can send as", async ({ page, proof }) => {
  await quietTips(page);
  await proof.open("/settings/mailboxes");
  await page.getByText("support@margin.app").first().click();
  await dismissNudge(page);
  const aliases = page.getByRole("list", { name: "Each alias" });
  await expect(aliases.getByRole("button", { name: "Edit hello@lumen.studio" })).toBeVisible({ timeout: 30000 });
  const gmail = page.getByRole("list", { name: "From Gmail" });
  await gmail.scrollIntoViewIfNeeded();
  await proof.chapter("Aliases", "Every alias of the Support mailbox, how its replies go out, and Gmail's other send-as addresses");
  await proof.shot("aliases", { caption: "Support's aliases: Lumen and help@ send as themselves, hello@ answers from support@; Gmail offers sales@ and jobs@" });

  await gmail.getByRole("button", { name: "Add jobs@lumen.studio as an alias" }).click();
  await expect(aliases.getByRole("button", { name: "Edit jobs@lumen.studio" })).toBeVisible();
  await expect(gmail.getByRole("button", { name: "Add jobs@lumen.studio as an alias" })).toHaveCount(0);
  await proof.dwell();
  await proof.shot("gmail-added", { caption: "jobs@lumen.studio added from Gmail's list, with its Gmail name" });

  await aliases.getByRole("button", { name: "Edit hello@lumen.studio" }).click();
  const signature = page.getByRole("textbox", { name: "Signature for hello@lumen.studio" });
  await expect(aliases.getByLabel("Sender name")).toHaveValue("Lumen");
  await expect(signature).toBeVisible();
  await signature.scrollIntoViewIfNeeded();
  await proof.chapter("Lumen, a side project", "Its own sender name, brand, team and signature, in the same inbox");
  await proof.shot("lumen-identity", { caption: "hello@lumen.studio: sender name Lumen, the Lumen brand, Support's team, and its own signature" });

  await signature.click();
  await page.keyboard.press("End");
  await page.keyboard.press("Enter");
  await page.keyboard.type("Reply here any time, we read every email.", { delay: 40 });
  await expect(signature).toContainText("we read every email");
  // The preview fills the signature as a reply to the alias carries it.
  const previewToggle = aliases.getByRole("button", { name: "Preview" });
  await previewToggle.click();
  await expect(previewToggle).toHaveAttribute("aria-expanded", "true");
  const filled = page.frameLocator('iframe[title="Email preview"]');
  await expect(filled.getByText("we read every email", { exact: false })).toBeVisible({ timeout: 30000 });
  await previewToggle.scrollIntoViewIfNeeded();
  await proof.dwell();
  await proof.shot("lumen-preview", { caption: "Lumen's signature in the preview, filled as a reply to hello@lumen.studio" });
  await page.getByRole("region", { name: "Unsaved changes" }).getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByText("Mailbox saved")).toBeVisible({ timeout: 30000 });
  await proof.dwell();
  await proof.shot("lumen-saved", { caption: "Lumen's signature saved; mail to the alias carries it before the team's and the mailbox's" });
});

test("a ticket written to an alias, answered from it", async ({ page, proof }) => {
  await quietTips(page);
  await proof.open("/inbox/open", { to: "hello@lumen.studio" });
  await dismissNudge(page);
  const tickets = page.getByRole("listbox", { name: "Tickets" });
  await expect(tickets.getByRole("option").first()).toContainText("Shared journal stopped syncing photos", { timeout: 30000 });
  await expect(page.getByText("Written to")).toBeVisible();
  await proof.chapter("Lumen's tickets", "The inbox filtered to the address customers wrote to");
  await proof.shot("alias-inbox", { caption: "Open tickets written to hello@lumen.studio, in the Lumen brand" });

  await tickets.getByRole("option").first().click();
  await expect(page.getByText("Replying as")).toBeVisible({ timeout: 30000 });
  await expect(page.getByText("Maya Chen at Lumen")).toBeVisible();
  await dismissNudge(page);
  await proof.chapter("Reply as Lumen", "The reply goes out From hello@lumen.studio, under Lumen's name");
  await proof.shot("alias-composer", { caption: "Replying as Maya Chen at Lumen <hello@lumen.studio>" });

  const reply = page.getByRole("textbox", { name: "Reply" });
  await reply.click();
  await page.keyboard.type("Thanks Carlos, a fix for photo sync is rolling out now. Pull to refresh the journal in a few minutes.", { delay: 30 });
  await page.getByRole("button", { name: "Send", exact: true }).click();
  const sent = page.getByRole("article", { name: "Message from Maya Chen" }).last();
  await expect(sent.getByText("a fix for photo sync", { exact: false })).toBeVisible();
  await proof.dwell();
  await proof.shot("alias-sent", { caption: "Sent from hello@lumen.studio" });
});
