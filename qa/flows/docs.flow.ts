import { expect, test } from "../lib/proof.ts";

test.use({ target: "docs" });

test("the docs home page", async ({ page, proof }) => {
  await proof.open("/");
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  await proof.shot("home", { caption: "Docs home" });
});

test("the mailboxes guide: replies and aliases", async ({ page, proof }) => {
  await proof.open("/guides/mailboxes/");
  const replies = page.getByRole("heading", { name: "Choose how replies look" });
  await replies.scrollIntoViewIfNeeded();
  await expect(page.getByRole("img", { name: /Name and addresses settings/ })).toBeVisible();
  await proof.shot("mailboxes-sending", { caption: "Choose how replies look: the team and From name, without the aliases" });
  const aliases = page.getByRole("heading", { name: "Aliases", exact: true });
  await aliases.scrollIntoViewIfNeeded();
  await expect(page.getByRole("img", { name: /Aliases section/ })).toBeVisible();
  await proof.shot("mailboxes-aliases", { caption: "Aliases has its own section: each alias with its sender name, team, brand and signature" });
});
