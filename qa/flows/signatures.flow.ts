import { expect, test } from "../lib/proof.ts";

// The Work account's signature in the mock is one a Gmail signature generator made: a
// layout table, inline styles, a sized logo, the title on its own line under the name.

test("a Gmail signature opens in settings as designed, and saving other settings leaves it alone", async ({ page, proof }) => {
  await proof.open("/settings/mail-accounts");
  await page.getByRole("button", { name: /^Work Private/ }).click();
  await expect(page.getByRole("heading", { name: "Work" })).toBeVisible();
  const preview = page.getByRole("region", { name: "Preview" });
  await expect(preview.getByText("Customer success & onboarding")).toBeVisible();
  await expect(page.getByText(/This signature uses a table layout, inline styles and sized images/)).toBeVisible();
  await preview.scrollIntoViewIfNeeded();
  await proof.chapter("Signature settings", "A designed signature opens as HTML, says why, and shows a live preview as recipients get it");
  await proof.shot("settings-signature", { caption: "The Gmail signature: name, the title on its own line, the logo at its own size, no frame" });

  await expect(page.getByRole("button", { name: "Save", exact: true })).toBeDisabled();
  const source = page.getByRole("textbox", { name: "Signature (HTML)" });
  await source.click();
  await source.press("ControlOrMeta+End");
  await source.pressSequentially('<div style="color:#4f46e5">Book a call: margin.app/demo</div>', { delay: 15, timeout: 60_000 });
  await expect(preview.getByText("Book a call: margin.app/demo")).toBeVisible();
  await expect(page.getByRole("button", { name: "Save", exact: true })).toBeEnabled();
  await proof.dwell();
  await proof.shot("settings-signature-edited", { caption: "An edit in the HTML shows in the preview at once; only now is there a signature to save" });
});

test("new mail carries the account's signature intact, and changing From swaps it", async ({ page, proof }) => {
  await proof.open("/mail");
  await page.getByRole("button", { name: "Compose", exact: true }).click();
  const composer = page.getByRole("dialog", { name: "New message" });
  const signature = composer.getByRole("group", { name: "Signature" });
  await expect(signature.getByText("Customer success & onboarding")).toBeVisible();
  await expect(signature.getByRole("img", { name: "Margin" })).toBeVisible();
  await proof.chapter("Composer", "The signature is one block, drawn and sent as the account has it");

  const body = composer.getByRole("textbox", { name: "Write your message" });
  // The first line, above the signature.
  await body.click({ position: { x: 24, y: 14 } });
  await body.pressSequentially("Hi Nora, the seat count is confirmed for Friday.", { delay: 30 });
  await proof.shot("composer-signature", { caption: "New mail from Work: the name, its title on the next line, the logo unframed" });

  await composer.getByRole("combobox", { name: "From" }).click();
  await page.getByRole("option", { name: /sales@margin\.app/ }).click();
  await expect(signature.getByText(/Head of support, Margin Sales/)).toBeVisible();
  await expect(composer.getByText("Hi Nora, the seat count is confirmed for Friday.")).toBeVisible();
  await proof.dwell();
  await proof.shot("composer-from-sales", { caption: "From Sales: its signature, with your name and title, takes the block's place; the text stays" });

  await composer.getByRole("combobox", { name: "From" }).click();
  await page.getByRole("option", { name: /maya@margin\.app/ }).click();
  await expect(signature.getByText("Customer success & onboarding")).toBeVisible();
  await signature.hover();
  await expect(signature.getByRole("button", { name: "Remove signature" })).toBeVisible();
  await proof.dwell();
  await proof.shot("composer-signature-tools", { caption: "Back on Work; the block can be removed whole or opened in settings" });
});
