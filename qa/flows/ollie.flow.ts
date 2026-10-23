import { expect, test } from "../lib/proof.ts";

// The signature from the report: one long line of HTML another tool wrote.
const pasted =
  'Set this as the organization wide signature: <table cellpadding="0" cellspacing="0" border="0" style="font-family:Inter, \'-apple-system\', \'Segoe UI\', Roboto, Helvetica, Arial, sans-serif;font-size:14px;line-height:1.5;color:#0f172a;"><tbody><tr><td style="font-size:16px;font-weight:600;letter-spacing:-0.01em;">{%user.fullName%}</td></tr><tr><td style="padding:3px 0 0;color:#64748b;">{%user.jobTitle,fallback=%}</td></tr><tr><td style="padding:12px 0 0;"><img src="https://raw.githubusercontent.com/margin/margin/main/site/public/brand/margin-wordmark.png" height="16" alt="Margin" style="border:0;"></td></tr><tr><td style="padding:10px 0 0;font-size:13px;"><a href="mailto:{%mailbox.email%}" style="color:#0f172a;text-decoration:none;font-weight:500;">{%mailbox.email%}</a><span style="color:#cbd5e1;">&nbsp;&nbsp;|&nbsp;&nbsp;</span><a href="https://margin.app" style="color:#0f172a;text-decoration:none;font-weight:500;">margin.app</a></td></tr><tr><td style="padding:14px 0 0;border-top:1px solid #e2e8f3;font-size:10.5px;line-height:1.65;color:#a8b4c4;">Margin is operated by <span style="font-weight:600;color:#64748b;">Margin Labs Ltd</span>, registered in England and Wales, company no.&nbsp;16543299.<br>Registered office: 71-75 Shelton Street, London WC2H 9JQ, United Kingdom.</td></tr></tbody></table>';

test("Ollie sets a pasted signature on the address, and long text stays inside the panel", async ({ page, proof }) => {
  await page.addInitScript(() => localStorage.setItem("od-ollie-tips", JSON.stringify({ paused: Number.MAX_SAFE_INTEGER })));
  await proof.open("/inbox/unassigned");
  await page.getByRole("button", { name: "Ask Ollie" }).click();
  const box = page.getByRole("textbox", { name: "Ask Ollie about your queue…" });
  await expect(box).toBeVisible();
  await box.fill(pasted);
  await page.keyboard.press("Enter");

  const panel = page.locator("[data-ollie-panel]");
  const approval = panel.getByRole("group", { name: "Approve this change?" });
  await expect(approval).toBeVisible();
  await expect(approval.getByText("Update a mailbox")).toBeVisible();
  const mine = panel.locator('[data-entry="user"]');
  await expect(mine.getByRole("button", { name: "Show more" })).toBeVisible();
  // The message wraps inside the panel instead of running off its edge.
  const [bubble, frame] = await Promise.all([mine.boundingBox(), panel.boundingBox()]);
  expect(bubble && frame && bubble.x >= frame.x && bubble.x + bubble.width <= frame.x + frame.width).toBeTruthy();
  await mine.scrollIntoViewIfNeeded();
  await proof.chapter("Paste a signature", "A long line of HTML wraps in the panel and folds to its first lines");
  await proof.dwell();
  await proof.shot("pasted", { target: panel, caption: "The pasted HTML wraps inside its bubble, folded with Show more" });

  await mine.getByRole("button", { name: "Show more" }).click();
  await expect(mine.getByRole("button", { name: "Show less" })).toBeVisible();
  await proof.dwell();
  await mine.getByRole("button", { name: "Show less" }).click();

  await approval.evaluate((el) => el.scrollIntoView({ block: "end" }));
  await expect(panel.getByText("Looked up how to do it")).toBeVisible();
  await proof.chapter("Approve it", "Ollie found the address signature through the API and shows the exact value");
  await proof.dwell();
  await proof.shot("approval", { target: panel, caption: "Ollie looked up how, then proposes the signature on the address, wrapped in the card" });

  await approval.getByRole("button", { name: /^Approve/ }).click();
  await expect(panel.getByText(/now end with the new signature/)).toBeVisible();
  await proof.dwell();
  await proof.shot("done", { target: panel, caption: "Approved: replies from the address carry the new signature" });
});
