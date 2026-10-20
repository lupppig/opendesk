import { expect, test } from "../lib/proof.ts";

test.use({ target: "admin" });

test("find a past-due workspace from the admin overview", async ({ page, proof }) => {
  await proof.open("/");
  await expect(page.getByRole("heading", { name: "Overview" })).toBeVisible();
  await expect(page.getByText("$799.57")).toBeVisible();
  await proof.chapter("Admin panel", "The instance overview, then one workspace");
  await proof.shot("overview", { caption: "The instance overview" });

  await page.getByRole("navigation", { name: "Admin" }).getByRole("link", { name: /^Workspaces/ }).click();
  await page.getByRole("searchbox", { name: /Search by name/ }).pressSequentially("halcyon", { delay: 60 });
  await page.getByRole("link", { name: "Halcyon Studio" }).click();
  await expect(page.getByRole("heading", { name: /Halcyon Studio/ })).toBeVisible();
  await proof.dwell();
  await proof.shot("workspace", { caption: "Halcyon Studio, past due, with its mailbox in error" });
});

test("sign in to the admin panel", async ({ page, proof }) => {
  await proof.open("/", { mock: "anon" });
  await expect(page.getByRole("heading", { name: "Sign in to the admin panel" })).toBeVisible();
  await proof.chapter("Admin sign-in", "Password, then an authenticator code, every time");
  await page.getByRole("textbox", { name: "Email" }).pressSequentially("ava@opendesk.example", { delay: 40 });
  await page.getByRole("textbox", { name: "Password" }).pressSequentially("plum orbit canvas", { delay: 40 });
  await page.getByRole("button", { name: "Show password" }).click();
  await expect(page.getByRole("textbox", { name: "Password" })).toHaveValue("plum orbit canvas");
  await proof.dwell();
  await proof.shot("sign-in", { caption: "The sign-in form, its password shown on request" });
});

test("create a test workspace whose password lasts a day", async ({ page, proof }) => {
  await proof.open("/test-workspaces");
  await expect(page.getByRole("heading", { name: "Test workspaces" })).toBeVisible();
  await expect(page.getByText("Password expires in 20 hours")).toBeVisible();
  await proof.chapter("Test workspaces", "A test account's password works for 1 or 2 days, then is renewed from here");
  await proof.shot("test-workspaces", { caption: "Test workspaces with their passwords' expiry" });

  await page.getByRole("button", { name: "Create test workspace" }).click();
  const dialog = page.getByRole("dialog", { name: "Create test workspace" });
  await expect(dialog.getByText("It works for 1 or 2 days.")).toBeVisible();
  await proof.dwell();
  await proof.shot("create-test-workspace", { caption: "A new test account's password lasts 1 day by default" });
});
