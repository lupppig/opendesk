import { expect, test } from "../lib/proof.ts";

test.use({ target: "site" });

test("the marketing site's home page", async ({ page, proof }) => {
  await proof.open("/");
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  await proof.shot("home", { caption: "Home, above the fold" });
});

test("the security page and its controls", async ({ page, proof }) => {
  await proof.open("/security/");
  await expect(page.getByRole("heading", { level: 1, name: "Security you can read." })).toBeVisible();
  await proof.shot("security", { caption: "Security, above the fold" });

  const controls = page.getByRole("heading", { level: 2, name: "Every control, written down." });
  await controls.scrollIntoViewIfNeeded();
  await expect(controls).toBeVisible();
  await proof.shot("security-controls", { caption: "The controls, written down" });
});

test("the privacy policy's Google user data terms", async ({ page, proof }) => {
  await proof.open("/privacy/");
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  const google = page.getByRole("heading", { level: 2, name: "Google user data" });
  await google.scrollIntoViewIfNeeded();
  await expect(google).toBeVisible();
  await proof.shot("privacy-google", { caption: "What opendesk does with Google user data, for each kind of connection" });
});
