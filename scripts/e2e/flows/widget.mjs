// Chat widget on another origin (widget-host.mjs on 127.0.0.1:8899): allowed
// origins, the AI answer bot and handoff, live agent replies and typing both
// ways over /socket/widget, and user_hash identity.

import { createHmac } from "node:crypto";
import { api, assert, cfg, newPage, openTicket, shot, uid, until } from "../lib.mjs";

const HOST = process.env.E2E_WIDGET_HOST || "http://127.0.0.1:8899";

/** Opens the widget on the host page and starts a new conversation. */
async function startChat(page, url) {
  await page.goto(url);
  await page.getByRole("button", { name: "Open chat" }).click();
  await page.getByRole("button", { name: "Send us a message" }).click();
  return page.getByRole("textbox", { name: "Message" });
}

export async function run(t) {
  const owner = t.owner.page;
  const opts = () => ({ token: t.ownerToken, workspace: t.workspaceId });
  const ws = (await api("GET", "/v1/workspace", opts())).json;
  const key = ws.widget_key;
  const hostUrl = (extra = {}) => `${HOST}/?${new URLSearchParams({ key, api: cfg.api, ...extra })}`;

  await t.step("widget settings: allow the customer site", async () => {
    // Every workspace starts with a default brand and its first widget: the old
    // single widget page redirects to that widget's page.
    await owner.goto(cfg.app + "/settings/widget");
    await owner.waitForURL(/\/settings\/brands\/[^/]+\/widgets\/[^/?#]+/);
    const origins = owner.getByRole("textbox", { name: "Allowed sites" });
    await origins.fill(HOST);
    await origins.press("Enter");
    await owner.getByRole("button", { name: "Save changes" }).click();
    await until(async () => (await api("GET", "/v1/widget/settings", opts())).json?.allowed_origins?.includes(HOST), "origin saved");
    const snippet = await owner.locator("textarea, pre, code").filter({ hasText: "widget/v1.js" }).first().innerText().catch(() => "");
    const value = snippet || (await owner.locator("textarea").evaluateAll((els) => els.map((e) => e.value).find((v) => v.includes("widget/v1.js"))));
    const split = new URL(cfg.api).origin !== new URL(cfg.app).origin;
    assert(value?.includes("widget/v1.js") && value.includes(`data-api="${new URL(cfg.api).origin}"`) === split, `snippet names the API origin only when it differs: ${value}`);
  });

  await t.step("a site not in the allowed list is refused", async () => {
    const r = await api("POST", "/v1/widget/session", { body: { key }, headers: { Origin: "https://evil.example" } });
    assert(r.status === 403, `session from another origin -> ${r.status}`);
  });

  await t.step("Settings > AI: turn on chat answers", async () => {
    await owner.goto(cfg.app + "/settings/ai");
    const sw = owner.getByRole("switch", { name: "Let Ollie answer chats" });
    if ((await sw.getAttribute("aria-checked")) !== "true") await sw.click();
    await owner.getByRole("button", { name: "Save changes" }).click();
    await owner.getByText("AI settings saved").first().waitFor();
    const c = (await api("GET", "/v1/ai/config", opts())).json;
    assert(c.features.widget_answers === true, "widget_answers on");
  });

  const { page: visitor, problems } = await newPage("visitor");
  t.problems.push(problems);
  t.visitor = visitor;
  if (process.env.E2E_WS_DEBUG === "1") {
    for (const [who, p] of [["visitor", visitor], ["owner", owner]]) {
      p.on("websocket", (ws) => {
        ws.on("framesent", (f) => /typing|ticket:.*phx_(join|leave)/.test(String(f.payload)) && console.log(`    ${Date.now() % 100000} [${who} ->] ${String(f.payload).slice(0, 200)}`));
        ws.on("framereceived", (f) => String(f.payload).includes("typing") && console.log(`    ${Date.now() % 100000} [${who} <-] ${String(f.payload).slice(0, 200)}`));
      });
    }
  }
  let ticket;

  await t.step("visitor starts a chat: ticket appears live", async () => {
    await owner.goto(cfg.app + "/inbox/unassigned");
    await owner.getByRole("navigation", { name: "Main" }).waitFor();
    const box = await startChat(visitor, hostUrl());
    await box.fill("How do I reset my password?");
    await box.press("Enter");
    ticket = await until(async () => {
      const r = await api("GET", "/v1/tickets?channel=chat", opts());
      return r.json?.data?.[0];
    }, "chat ticket created");
    await owner.getByRole("option").filter({ hasText: ticket.subject }).first().waitFor({ timeout: 15_000 });
  });

  await t.step("AI answer bot replies in the widget", async () => {
    await visitor.locator(".thread").getByText("You can reset your password from the sign-in page").waitFor({ timeout: 20_000 });
    await visitor.locator(".thread .ai-pill").first().waitFor();
    // The widget shows the answer while it streams; the message is stored when it ends.
    const bot = await until(async () => {
      const r = await api("GET", `/v1/tickets/${ticket.id}/messages`, opts());
      return r.json?.data?.find((m) => m.ai);
    }, "bot message stored with ai=true");
    assert(bot.author.type === "system", "bot message is from the system");
    assert(bot.body_html.includes(`/help/`), "answer links the cited article");
    await shot(visitor, "widget-ai-answer");
  });

  await t.step("visitor asks for a person: handoff notifies the team", async () => {
    await visitor.getByRole("button", { name: "Talk to a person" }).click();
    await visitor.locator(".handed").filter({ hasText: "A person will reply here" }).waitFor();
    const n = await until(async () => {
      const r = await api("GET", "/v1/notifications", opts());
      return r.json?.data?.find((x) => x.type === "ai_handoff" && x.ticket?.id === ticket.id);
    }, "ai_handoff notification");
    assert(n.data?.via, "handoff notification says how");
    const tk = (await api("GET", `/v1/tickets/${ticket.id}`, opts())).json;
    assert(tk.custom?.ai_handoff === true || tk.custom?.ai_handoff === "true", "ticket marked for a human");
  });

  await t.step("agent typing shows in the widget", async () => {
    await openTicket(owner, ticket.subject);
    const editor = owner.getByRole("textbox", { name: "Reply" });
    await editor.click();
    await editor.pressSequentially("Hi there", { delay: 50 });
    await visitor.locator(".typing").first().waitFor();
    await editor.press("ControlOrMeta+a");
    await editor.press("Backspace");
  });

  await t.step("visitor typing shows for the agent", async () => {
    if (process.env.E2E_WS_DEBUG === "1") console.log(`    ${Date.now() % 100000} visitor typing step`);
    const box = visitor.getByRole("textbox", { name: "Message" });
    await box.click();
    await box.pressSequentially("One more thing", { delay: 50 });
    await owner.getByText(/is typing/).first().waitFor();
    await box.fill("");
  });

  await t.step("agent reply reaches the visitor live", async () => {
    const editor = owner.getByRole("textbox", { name: "Reply" });
    await editor.click();
    await editor.fill("Happy to help. Did the reset link arrive?");
    await owner.getByRole("button", { name: "Send", exact: true }).click();
    await visitor.locator(".thread").getByText("Did the reset link arrive?").waitFor();
    await shot(visitor, "widget-agent-reply");
  });

  await t.step("visitor reply reaches the agent live", async () => {
    const box = visitor.getByRole("textbox", { name: "Message" });
    await box.fill("Yes, it worked now. Thanks!");
    await box.press("Enter");
    await owner.getByRole("article").filter({ hasText: "it worked now" }).waitFor();
  });

  await t.step("user_hash identifies a signed-in user", async () => {
    const r = await api("POST", "/v1/workspace/widget-secret/rotate", opts());
    assert(r.status === 200, `rotate secret -> ${r.status} ${r.text}`);
    const secret = r.json.secret;
    const userId = `u-${uid()}`;
    const email = `signed-${uid()}@customer.test`;
    const hash = createHmac("sha256", secret).update(userId).digest("hex");
    const bad = await api("POST", "/v1/widget/session", { body: { key, user_id: userId, user_hash: "00".repeat(32) }, headers: { Origin: HOST } });
    assert(bad.status === 401 && bad.json?.code === "invalid_user_hash", `bad hash refused (${bad.status})`);
    const { page, problems: p2 } = await newPage("signed-in visitor");
    t.problems.push(p2);
    const box = await startChat(page, hostUrl({ uid: userId, hash, email, name: "Sam Signed" }));
    await box.fill("Hello from a signed-in user");
    await box.press("Enter");
    const contact = await until(async () => {
      const x = await api("GET", `/v1/contacts?q=${encodeURIComponent(email)}`, opts());
      return x.json?.data?.find((c) => c.email === email);
    }, "identified contact");
    assert(contact.external_id === userId, `contact linked to user_id (${contact.external_id})`);
    assert(contact.name === "Sam Signed", "name from identify");
    await page.context().close();
  });
}
