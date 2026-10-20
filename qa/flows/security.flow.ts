import { expect, test } from "../lib/proof.ts";

test("change your password with the rules shown as you type", async ({ page, proof }) => {
  await proof.open("/settings/security");
  await expect(page.getByRole("heading", { name: "Password", level: 2 })).toBeVisible();
  await page.getByRole("button", { name: "Not now" }).click();
  await proof.chapter("Password", "A new password is 12 to 128 characters and not a known breached one");
  await proof.shot("password", { caption: "The password section, with its rules" });

  await page.getByRole("textbox", { name: "Current password" }).fill("correct horse battery");
  await page.getByRole("textbox", { name: "New password", exact: true }).pressSequentially("short", { delay: 60 });
  await proof.dwell();
  await proof.shot("password-short", { caption: "A new password under 12 characters" });

  await page.getByRole("textbox", { name: "New password", exact: true }).pressSequentially(" lantern orchard tide", { delay: 40 });
  await page.getByRole("textbox", { name: "Confirm new password" }).fill("short lantern orchard tide");
  await expect(page.getByRole("button", { name: "Change password" })).toBeEnabled();
  await proof.dwell();
  await proof.shot("password-ready", { caption: "A long passphrase, ready to save" });
});

test("verify a webhook address before it gets events", async ({ page, proof }) => {
  await proof.open("/settings/webhooks");
  const zapier = page.getByRole("button", { name: /^https:\/\/hooks\.zapier\.com\/hooks\/catch\/48213\/b7kq2x\/ .*Not verified/ });
  await expect(zapier).toBeVisible();
  await page.getByRole("button", { name: "Not now" }).click();
  await proof.chapter("Webhooks", "A new address gets no events until it answers its challenge or its code is entered");
  await proof.shot("webhooks", { caption: "The Zapier address is not verified yet" });

  await zapier.click();
  await expect(page.getByRole("heading", { name: "Verify this address" })).toBeVisible();
  await proof.shot("webhook-unverified", { caption: "Its page asks for the challenge it received" });

  await page.getByRole("textbox", { name: "Challenge" }).pressSequentially("whv_demo", { delay: 60 });
  await page.getByRole("button", { name: "Verify", exact: true }).click();
  await expect(page.getByText("Webhook verified")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Verify this address" })).toBeHidden();
  await proof.dwell();
  await proof.shot("webhook-verified", { caption: "Verified: events go out from now on" });
});

test("rate a conversation from the survey link", async ({ page, proof }) => {
  await proof.open("/csat#t=demo&score=4");
  await expect(page.getByRole("heading", { name: "How was your conversation?" })).toBeVisible();
  await expect(page.getByRole("radio", { name: "4, Good" })).toBeChecked();
  await proof.chapter("Satisfaction survey", "The survey token rides in the link's fragment, so it never reaches a server log");
  await proof.dwell();
  await proof.shot("csat", { caption: "The rating from the email, saved" });
});
