// Ticket work: status, assignee and tags (live for the other agent), snooze,
// merge, bulk actions, saved replies, views and sidebar counts.

import { api, assert, cfg, inboundEmail, openTicket, rawEmail, shot, uid, until } from "../lib.mjs";

/** The button of a property row in the ticket sidebar ("Status", "Assignee"...). */
function prop(page, label) {
  return page.locator("div.min-h-8").filter({ has: page.getByText(label, { exact: true }) }).getByRole("button").first();
}

async function pick(page, label, option) {
  await prop(page, label).click();
  await page.getByRole("option", { name: option }).first().click();
}

export async function run(t) {
  const owner = t.owner.page;
  const agent = t.agent.page;
  const tok = () => t.ownerToken;
  const getTicket = async (id) => (await api("GET", `/v1/tickets/${id}`, { token: tok(), workspace: t.workspaceId })).json;

  await t.step("change status, live in the other browser", async () => {
    await pick(owner, "Status", "Pending");
    await until(async () => (await getTicket(t.ticketId)).status === "pending", "status saved");
    await prop(agent, "Status").filter({ hasText: "Pending" }).waitFor();
  });

  await t.step("assign to the other agent: live notification", async () => {
    await pick(owner, "Assignee", "Ben Agent");
    await until(async () => (await getTicket(t.ticketId)).assignee?.name === "Ben Agent", "assignee saved");
    await agent.getByText(/assigned/i).first().waitFor();
    await prop(agent, "Assignee").filter({ hasText: "Ben Agent" }).waitFor();
  });

  await t.step("add a new tag", async () => {
    const tagBtn = owner.getByRole("button", { name: /add (tags?|labels?)/i }).or(owner.getByPlaceholder("Add tags")).first();
    await tagBtn.click();
    // New workspaces start with a few labels; create one that is not among them.
    const tagName = `e2e-${uid()}`;
    const input = owner.getByPlaceholder("Add or create a label");
    await input.fill(tagName);
    await owner.getByRole("option", { name: /create/i }).first().click();
    await owner.keyboard.press("Escape");
    await until(async () => (await getTicket(t.ticketId)).tags.some((x) => x.name === tagName), "tag saved");
    await agent.getByText(tagName, { exact: true }).first().waitFor();
  });

  await t.step("snooze puts the ticket on hold", async () => {
    await owner.getByRole("button", { name: "Snooze", exact: true }).click();
    await owner.getByRole("button", { name: /^Tomorrow/ }).click();
    const tk = await until(async () => {
      const x = await getTicket(t.ticketId);
      return x.snoozed_until && x.status === "on_hold" ? x : null;
    }, "snoozed and on hold");
    assert(tk.status === "on_hold", "status on_hold");
  });

  await t.step("solving clears the snooze", async () => {
    await owner.getByRole("button", { name: "Solve", exact: true }).click();
    const tk = await until(async () => {
      const x = await getTicket(t.ticketId);
      return x.status === "solved" ? x : null;
    }, "solved");
    assert(tk.snoozed_until === null, "snooze cleared on solve");
  });

  await t.step("merge a second ticket from the same customer", async () => {
    const subject = `Second issue ${uid()}`;
    await inboundEmail(t.forwarding, rawEmail({ from: t.customerEmail, to: t.supportEmail, subject, text: "Also, the export button is broken." }));
    await owner.goto(cfg.app + "/inbox/unassigned");
    await openTicket(owner, subject);
    await owner.getByRole("button", { name: "More actions" }).click();
    await owner.getByRole("menuitem", { name: "Merge into another ticket" }).click();
    const dialog = owner.getByRole("dialog");
    await dialog.getByPlaceholder("Search by subject, customer or #number").fill(t.subject);
    await dialog.getByRole("button").filter({ hasText: t.subject }).first().click();
    await dialog.getByRole("button", { name: /^Merge into #/ }).click();
    await dialog.waitFor({ state: "hidden" });
    const id = owner.url().split("/").pop();
    const merged = await until(async () => {
      const r = await api("GET", `/v1/tickets?status=closed&q=${encodeURIComponent(subject)}`, { token: tok(), workspace: t.workspaceId });
      return r.json?.data?.find((x) => x.subject === subject);
    }, "source closed");
    assert(merged, `merged source closed (${id})`);
    const msgs = await api("GET", `/v1/tickets/${t.ticketId}/messages`, { token: tok(), workspace: t.workspaceId });
    assert(msgs.json.data.some((m) => m.body_text.includes("export button")), "messages moved into the target");
  });

  await t.step("bulk: select two tickets and set priority", async () => {
    for (const s of ["Bulk one", "Bulk two"]) {
      await inboundEmail(t.forwarding, rawEmail({ from: `bulk-${uid()}@customer.test`, to: t.supportEmail, subject: `${s} ${t.subject}`, text: "Bulk test" }));
    }
    await owner.goto(cfg.app + "/inbox/unassigned");
    const rows = owner.getByRole("option").filter({ hasText: /Bulk (one|two)/ });
    await until(async () => (await rows.count()) === 2, "two bulk tickets listed");
    for (const n of [0, 1]) {
      await rows.nth(n).hover();
      await rows.nth(n).getByRole("checkbox").click();
      await shot(owner, `bulk-${n}`);
    }
    const bar = owner.getByRole("toolbar", { name: "Bulk actions" }).or(owner.locator('[aria-label="Bulk actions"]')).first();
    await bar.waitFor();
    await bar.getByRole("button", { name: "Label" }).click();
    await owner.getByPlaceholder("Add labels").fill("bulk");
    await shot(owner, "bulk-tag-typed");
    await owner.getByRole("option", { name: /Add label/ }).first().click();
    await shot(owner, "bulk-tag-picked");
    await owner.keyboard.press("Escape");
    const list = await until(async () => {
      const r = await api("GET", `/v1/tickets?tag=bulk`, { token: tok(), workspace: t.workspaceId });
      return r.json?.data?.length === 2 ? r.json.data : null;
    }, "bulk tags applied");
    assert(list.every((x) => x.tags.some((g) => g.name === "bulk")), "both tagged");
  });

  await t.step("saved reply: create in settings and insert with /", async () => {
    await owner.goto(cfg.app + "/settings/saved-replies");
    await owner.getByRole("button", { name: "New reply" }).first().click();
    await owner.getByLabel("Title").fill("Reset steps");
    await owner.getByLabel(/Shortcut/).fill("reset");
    await owner.getByRole("textbox").filter({ hasText: "" }).last().click();
    await owner.keyboard.type("Open the sign-in page and choose Forgot password.");
    await owner.getByRole("button", { name: "Save", exact: true }).click();
    await until(async () => (await api("GET", "/v1/saved-replies", { token: tok(), workspace: t.workspaceId })).json?.data?.some((x) => x.title === "Reset steps"), "saved reply stored");
    await owner.getByText("Reset steps").first().waitFor();
    await owner.goto(t.ticketUrl);
    const editor = owner.getByRole("textbox", { name: "Reply" });
    await editor.click();
    await editor.pressSequentially("/reset", { delay: 40 });
    await owner.getByRole("option", { name: /Reset steps/ }).first().click();
    await until(async () => (await editor.innerText()).includes("Forgot password"), "saved reply inserted");
    await editor.press("ControlOrMeta+a");
    await editor.press("Backspace");
  });

  await t.step("views: a new view shows in the sidebar with its count", async () => {
    const r = await api("POST", "/v1/views", { token: tok(), workspace: t.workspaceId, body: { name: "Bulk view", filters: { tag: "bulk" }, shared: true } });
    assert(r.status === 201 || r.status === 200, `create view ${r.status} ${r.text}`);
    const nav = owner.getByRole("navigation", { name: "Main" });
    const link = nav.getByRole("link", { name: /Bulk view/ });
    await link.waitFor();
    await until(async () => /2/.test(await link.innerText()), "view count 2 in the sidebar");
    await link.click();
    await owner.getByRole("option").filter({ hasText: /Bulk one/ }).waitFor();
    await shot(owner, "view");
  });
}
