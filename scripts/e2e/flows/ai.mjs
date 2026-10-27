// AI with the fake provider (fake-ai.mjs): triage and apply, summary, message
// translation, draft (SSE), rewrite and translate in the composer, ask about a
// ticket, a KB article drafted from tickets and AI search in the palette.

import { api, assert, cfg, inboundEmail, openTicket, rawEmail, shot, uid, until } from "../lib.mjs";

export async function run(t) {
  const owner = t.owner.page;
  const opts = () => ({ token: t.ownerToken, workspace: t.workspaceId });
  const subject = `No puedo entrar ${uid()}`;
  let ticketId;

  await t.step("Spanish email arrives", async () => {
    await inboundEmail(t.forwarding, rawEmail({ from: `maria-${uid()}@cliente.test`, fromName: "María López", to: t.supportEmail, subject, text: "Hola, cambié mi contraseña y todavía no puedo iniciar sesión." }));
    await owner.goto(cfg.app + "/inbox/unassigned");
    await openTicket(owner, subject);
    ticketId = owner.url().split("/").pop();
  });

  await t.step("triage: suggest changes and apply all", async () => {
    // Jev (fake-typesafe.mjs) proposes high priority and sev2 for "no puedo" and reads
    // the language; applying the untouched proposal writes all of it.
    const panel = owner.getByRole("region", { name: "Ollie", exact: true });
    await panel.getByRole("button", { name: /^Suggest changes/ }).click();
    await panel.getByRole("button", { name: /^Apply Priority: / }).waitFor();
    await panel.getByRole("button", { name: /^Apply Severity: / }).waitFor();
    await panel.getByRole("button", { name: "Apply all" }).click();
    await panel.getByText("Done. The ticket is updated.").waitFor();
    await until(async () => {
      const x = (await api("GET", `/v1/tickets/${ticketId}`, opts())).json;
      return x.priority === "high" && x.severity === "sev2" && x.custom?.language === "es" ? x : null;
    }, "triage applied");
  });

  await t.step("summary", async () => {
    await owner.getByRole("button", { name: /^Summarize/ }).click();
    await owner.getByText("cannot sign in after resetting their password").first().waitFor({ timeout: 15_000 });
    const tk = (await api("GET", `/v1/tickets/${ticketId}`, opts())).json;
    assert(JSON.stringify(tk.custom?.ai_summary ?? "").includes("cannot sign in"), "summary stored on the ticket");
  });

  await t.step("translate an inbound message", async () => {
    const msg = owner.getByRole("article").filter({ hasText: "Hola, cambié" });
    await msg.hover();
    await msg.getByRole("button", { name: /Translate/ }).click();
    await owner.getByText(/Translated from/).first().waitFor({ timeout: 15_000 });
    await owner.getByText("[es]").first().waitFor();
  });

  const editor = owner.getByRole("textbox", { name: "Reply" });
  const aiMenu = async (item) => {
    await owner.getByRole("button", { name: "Write with AI" }).click();
    await owner.getByRole("menuitem", { name: item }).click();
  };

  await t.step("draft a reply (streamed)", async () => {
    await aiMenu(/^Draft a reply/);
    await until(async () => (await editor.innerText()).includes("Please try resetting your password"), "draft streamed into the composer", 20_000);
    await until(async () => !(await owner.getByText("Drafting a reply").count()), "draft finished");
    await shot(owner, "ai-draft");
  });

  await t.step("rewrite the draft", async () => {
    await aiMenu("Friendlier");
    await until(async () => (await editor.innerText()).includes("friendlier version"), "rewritten", 15_000);
  });

  await t.step("translate the draft to the customer's language", async () => {
    await aiMenu(/^Translate to/);
    await until(async () => (await editor.innerText()).includes("[es]"), "translated draft", 15_000);
    await editor.press("ControlOrMeta+a");
    await editor.press("Backspace");
  });

  await t.step("ask about the ticket", async () => {
    const ask = owner.getByRole("textbox", { name: "Ask Ollie" });
    await ask.fill("What has the customer tried?");
    await ask.press("Enter");
    await owner.getByText("Suggest clearing cookies").first().waitFor({ timeout: 15_000 });
  });

  await t.step("draft a KB article from tickets", async () => {
    await owner.goto(cfg.app + "/kb");
    await owner.getByRole("link", { name: "Draft from tickets" }).first().click();
    const dialog = owner.getByRole("dialog");
    await dialog.getByLabel("Topic").fill("Signing in after a password reset");
    await dialog.getByRole("button", { name: "Draft article" }).click();
    await owner.getByText("Draft ready. Review it before publishing.").first().waitFor({ timeout: 20_000 });
    assert((await owner.getByLabel("Title").inputValue()) === "How to sign in after a password reset", "title filled");
    await owner.getByRole("button", { name: "Save draft" }).click();
    await until(async () => {
      const r = await api("GET", `/v1/kb/articles?q=${encodeURIComponent("password reset")}&status=draft`, opts());
      return r.json?.data?.some((a) => a.title === "How to sign in after a password reset");
    }, "drafted article saved");
  });

  await t.step("AI search in the command palette", async () => {
    await owner.goto(cfg.app + "/inbox/unassigned");
    await owner.getByRole("navigation", { name: "Main" }).waitFor();
    await owner.keyboard.press("ControlOrMeta+k");
    const input = owner.getByPlaceholder("Search tickets, contacts, articles, the wiki, or type a command");
    await input.fill("customer cannot sign in after password reset");
    await input.press("Tab");
    await owner.getByPlaceholder("Ask anything, like customers who were charged twice").waitFor();
    await owner.getByText(/Related (tickets|articles)/).first().waitFor({ timeout: 15_000 });
    await shot(owner, "ai-search");
    await owner.keyboard.press("Escape");
  });
}
