// Contacts and companies, a knowledge base collection and a published article,
// and the public help center at /help/<slug>.

import { api, assert, cfg, newPage, shot, uid, until } from "../lib.mjs";

export async function run(t) {
  const owner = t.owner.page;
  const opts = () => ({ token: t.ownerToken, workspace: t.workspaceId });

  const company = `Northwind ${uid()}`;
  const domain = `northwind-${uid()}.test`;
  await t.step("create a company", async () => {
    await owner.goto(cfg.app + "/companies");
    await owner.getByRole("button", { name: "New company" }).first().click();
    const dialog = owner.getByRole("dialog", { name: "New company" });
    await dialog.getByLabel("Name").fill(company);
    await dialog.getByLabel(/Domain/).fill(domain);
    await dialog.getByRole("button", { name: "Create company" }).click();
    await dialog.waitFor({ state: "hidden" });
    await owner.getByText(company).first().waitFor();
  });

  const contactEmail = `lena-${uid()}@${domain}`;
  await t.step("create a contact in that company", async () => {
    await owner.goto(cfg.app + "/contacts");
    await owner.getByRole("button", { name: "New contact" }).first().click();
    const dialog = owner.getByRole("dialog", { name: "New contact" });
    await dialog.getByLabel("Name").fill("Lena Park");
    await dialog.getByLabel("Email").fill(contactEmail);
    await dialog.getByLabel(/^Company/).click();
    await owner.getByPlaceholder("Search companies").fill(company.split(" ")[1]);
    await owner.getByRole("option", { name: new RegExp(company) }).click();
    await dialog.getByRole("button", { name: "Create contact" }).click();
    await dialog.waitFor({ state: "hidden" });
    // The drawer opens on the new contact.
    await owner.getByText(contactEmail).first().waitFor();
    const r = await api("GET", `/v1/contacts?q=${encodeURIComponent(contactEmail)}`, opts());
    const c = r.json?.data?.[0];
    assert(c?.company?.name === company, `contact linked to the company (${r.text.slice(0, 200)})`);
    t.contactId = c.id;
  });

  await t.step("edit the contact", async () => {
    await owner.getByRole("button", { name: "Edit" }).first().click();
    const name = owner.getByRole("textbox", { name: "Name", exact: true });
    await name.fill("Lena Park-Ito");
    await owner.getByLabel("Notes").fill("Prefers email.");
    await owner.getByRole("button", { name: "Save changes" }).click();
    await until(async () => (await api("GET", `/v1/contacts/${t.contactId}`, opts())).json?.name === "Lena Park-Ito", "contact saved");
    await owner.keyboard.press("Escape");
  });

  await t.step("the email customer is a contact with their ticket", async () => {
    await owner.goto(cfg.app + `/contacts?q=${encodeURIComponent(t.customerEmail)}`);
    const row = owner.getByRole("link").filter({ hasText: t.customerEmail });
    await row.waitFor();
    assert(/[1-9]/.test(await row.innerText()), "ticket count shown");
  });

  await t.step("company lists its contact", async () => {
    await owner.goto(cfg.app + "/companies");
    await owner.getByRole("link").filter({ hasText: company }).click();
    await owner.getByRole("tab", { name: /People/ }).click();
    await owner.getByRole("tabpanel").getByText("Lena Park-Ito").first().waitFor();
    await owner.keyboard.press("Escape");
  });

  const collection = `Getting started ${uid()}`;
  await t.step("create a KB collection", async () => {
    await owner.goto(cfg.app + "/kb");
    await owner.getByRole("button", { name: "New collection" }).click();
    const dialog = owner.getByRole("dialog", { name: "New collection" });
    await dialog.getByLabel("Name").fill(collection);
    await dialog.getByLabel(/Description/).fill("First steps with Acme.");
    await dialog.getByRole("button", { name: "Create" }).click();
    await dialog.waitFor({ state: "hidden" });
    await owner.getByRole("navigation", { name: "Collections" }).getByText(collection).waitFor();
  });

  const title = `How to reset your password ${uid()}`;
  t.articleTitle = title;
  await t.step("write and publish an article", async () => {
    await owner.getByRole("navigation", { name: "Collections" }).getByText(collection).click();
    await owner.getByRole("link", { name: "New article" }).first().click();
    await owner.getByLabel("Title").fill(title);
    await owner.getByLabel("Excerpt").fill("Reset your password from the sign-in page.");
    await owner.locator(".ProseMirror").last().click();
    await owner.keyboard.type("Open the sign-in page and choose Forgot password. We email you a reset link that works for one hour.");
    await owner.getByRole("button", { name: collection }).waitFor();
    await owner.getByRole("button", { name: "Publish" }).click();
    await owner.getByText("Published", { exact: true }).first().waitFor();
    const r = await until(async () => {
      const x = await api("GET", `/v1/kb/articles?q=${encodeURIComponent(title)}`, opts());
      return x.json?.data?.find((a) => a.title === title && a.status === "published");
    }, "article published");
    t.articleSlug = r.slug;
    await shot(owner, "kb-article");
  });

  await t.step("publish the help center", async () => {
    await owner.goto(cfg.app + "/settings/workspace");
    const sw = owner.getByRole("switch", { name: "Publish the help center" });
    // On by default: turn it off and on again so the save path is exercised.
    for (const want of ["false", "true"]) {
      if ((await sw.getAttribute("aria-checked")) !== want) await sw.click();
      await owner.getByRole("button", { name: "Save changes" }).click();
      await owner.getByText("Workspace saved").first().waitFor();
      await until(async () => String((await api("GET", "/v1/workspace", opts())).json?.settings?.help_center_enabled) === want, `help center ${want}`);
      await owner.getByRole("button", { name: "Save changes" }).waitFor({ state: "hidden" });
    }
    const ws = await api("GET", "/v1/workspace", opts());
    t.slug = ws.json?.slug;
    assert(ws.json?.settings?.help_center_enabled === true, "help center enabled");
  });

  await t.step("public help center: home, collection, article and search", async () => {
    const { page, problems } = await newPage("visitor");
    t.problems.push(problems);
    await page.goto(`${cfg.api}/help/${t.slug}`);
    await page.getByText(collection).first().click();
    await page.getByRole("heading", { name: collection }).waitFor();
    await page.getByText(title).first().click();
    await page.getByRole("heading", { name: title }).waitFor();
    await page.getByText("works for one hour").waitFor();
    await shot(page, "help-article");
    await page.goto(`${cfg.api}/help/${t.slug}`);
    const search = page.getByPlaceholder("Search articles").first();
    await search.fill("password");
    await search.press("Enter");
    await page.getByText(title).first().waitFor();
    await page.context().close();
  });
}
