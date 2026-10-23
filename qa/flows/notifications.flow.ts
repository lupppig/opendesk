import { expect, test } from "../lib/proof.ts";

test("a new unassigned ticket in the notifications panel", async ({ page, proof }) => {
  await proof.open("/inbox/unassigned");
  await expect(page.getByRole("listbox", { name: "Tickets" }).getByRole("option").first()).toBeVisible();
  await proof.chapter("New tickets", "Nobody is assigned, so the team hears about it");

  await page.getByRole("button", { name: /^Notifications/ }).click();
  const note = page.getByRole("button", { name: /New ticket from/ }).first();
  await expect(note).toBeVisible();
  await proof.dwell();
  await proof.shot("panel", { caption: "A new ticket nobody is assigned to, in the notifications panel" });

  await note.click();
  await expect(page).toHaveURL(/\/inbox\/unassigned\//);
  await proof.dwell();
  await proof.shot("opened", { caption: "It opens in Unassigned" });
});

test("choose who hears about new unassigned tickets", async ({ page, proof }) => {
  await proof.open("/settings/assignment");
  const section = page.getByRole("heading", { name: "New unassigned tickets" });
  await expect(section).toBeVisible();
  await section.scrollIntoViewIfNeeded();
  await proof.chapter("Assignment", "Who is notified about a new ticket nobody is assigned to");
  await proof.shot("setting", { caption: "Automatic: everyone on small teams, only leads when more than 5 people would be notified" });

  const picker = page.getByRole("combobox", { name: "Notify about new unassigned tickets" });
  await picker.click();
  const leads = page.getByRole("option", { name: /Leads/ });
  await expect(leads).toBeVisible();
  await proof.shot("options", { caption: "Automatic, everyone on the team, leads and admins, or off" });

  await leads.click();
  await expect(picker).toContainText(/Leads/);
  await proof.dwell();
  await proof.shot("leads", { caption: "Only leads and admins hear about new tickets" });

  await proof.open("/settings/notifications");
  const row = page.getByRole("row", { name: /^New tickets/ });
  await expect(row).toBeVisible();
  await row.scrollIntoViewIfNeeded();
  await expect(row.getByRole("switch", { name: "New tickets: Phone" })).toBeChecked();
  await proof.chapter("Notifications", "Each person still decides where it reaches them");
  await proof.shot("preferences", { caption: "New tickets: phone and desktop on, email off by default" });
});

test("the app asks to allow notifications, so new tickets reach you in the background", async ({ page, proof }) => {
  // The browser's own prompt: not asked yet, then allowed when the person agrees.
  await page.addInitScript(() => {
    let state: NotificationPermission = "default";
    Object.defineProperty(Notification, "permission", { get: () => state });
    Notification.requestPermission = async () => (state = "granted");
  });
  await proof.open("/inbox/unassigned");
  const strip = page.getByRole("status").filter({ hasText: "Get told about new tickets and replies." });
  await expect(strip).toBeVisible();
  await expect(page.getByRole("listbox", { name: "Tickets" }).getByRole("option").first()).toBeVisible();
  await proof.chapter("Allow notifications", "Until then, new tickets and replies only show under the bell");
  await proof.dwell();
  await proof.shot("ask", { caption: "A strip above the inbox asks to allow notifications" });

  await strip.getByRole("button", { name: "Allow notifications" }).click();
  await expect(strip).toBeHidden();
  await proof.dwell();
  await proof.shot("allowed", { caption: "Allowed: the strip goes away and notifications reach this browser" });
});

test("new tickets are emailed by default, and replies on unassigned tickets reach the team", async ({ page, proof }) => {
  await page.addInitScript(() => localStorage.setItem("od-ollie-tips", JSON.stringify({ paused: Number.MAX_SAFE_INTEGER })));
  await proof.open("/settings/notifications");
  const row = page.getByText("New tickets nobody is assigned to. Who gets them is set in Assignment");
  await expect(row).toBeVisible();
  await expect(page.getByText("On tickets assigned to you, and on tickets nobody has yet")).toBeVisible();
  await row.scrollIntoViewIfNeeded();
  await proof.chapter("Notification settings", "New tickets now reach you by email, phone and desktop unless you turn them off");
  await proof.dwell();
  await proof.shot("defaults", { caption: "New tickets and customer replies, with email on by default" });
});

test("each address shows when mail last arrived, and warns when it goes quiet", async ({ page, proof }) => {
  await page.addInitScript(() => localStorage.setItem("od-ollie-tips", JSON.stringify({ paused: Number.MAX_SAFE_INTEGER })));
  await proof.open("/settings/mailboxes");
  await expect(page.getByText("Last mail", { exact: true })).toBeVisible();
  await expect(page.getByText("billing@margin.app")).toBeVisible();
  await proof.chapter("Mailboxes", "When mail last arrived at each address");
  await proof.dwell();
  await proof.shot("list", { caption: "The Last mail column; Billing, quiet for 11 days, in amber" });

  await page.getByText("billing@margin.app").click();
  const warning = page.getByRole("status").filter({ hasText: "No mail for 11 days" });
  await expect(warning).toBeVisible();
  await expect(page.getByText("Last mail received")).toBeVisible();
  await proof.chapter("A quiet address", "What to check when forwarded mail stops arriving");
  await proof.dwell();
  await proof.shot("quiet", { caption: "Billing's page: no mail for 11 days, and what to check" });
});
