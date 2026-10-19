// Settings: members and roles, teams, tags, SLA, an automation that fires,
// webhooks to a local receiver (signature and deliveries), API keys used with
// curl, the audit log, workspace language, CSAT end to end, and the
// self-hosted billing page. CSAT needs the SMTP sink (E2E_MAIL_SINK).

import { execFile } from "node:child_process";
import { createHmac, timingSafeEqual } from "node:crypto";
import http from "node:http";
import { promisify } from "node:util";
import { api, assert, cfg, inboundEmail, mailLink, newPage, openTicket, rawEmail, uid, until } from "../lib.mjs";

const run$ = promisify(execFile);

/** A local webhook receiver that records each request with its raw body. */
function receiver() {
  const got = [];
  const server = http.createServer((req, res) => {
    let body = "";
    req.on("data", (c) => (body += c));
    req.on("end", () => {
      got.push({ headers: req.headers, body });
      res.writeHead(200, { "Content-Type": "text/plain" }).end("ok");
    });
  });
  return new Promise((resolve) => server.listen(0, "127.0.0.1", () => resolve({ got, url: `http://127.0.0.1:${server.address().port}/hook`, close: () => server.close() })));
}

export async function run(t) {
  const owner = t.owner.page;
  const opts = () => ({ token: t.ownerToken, workspace: t.workspaceId });

  await t.step("members: change a role", async () => {
    await owner.goto(cfg.app + "/settings/members");
    await owner.getByRole("combobox", { name: "Role for Ben Agent" }).click();
    await owner.getByRole("option", { name: "Admin" }).click();
    await until(async () => (await api("GET", "/v1/members", opts())).json?.data?.find((m) => m.user.name === "Ben Agent")?.role === "admin", "role saved");
    await owner.getByRole("combobox", { name: "Role for Ben Agent" }).click();
    await owner.getByRole("option", { name: "Agent" }).click();
    await until(async () => (await api("GET", "/v1/members", opts())).json?.data?.find((m) => m.user.name === "Ben Agent")?.role === "agent", "role back to agent");
  });

  const team = `Billing ${uid()}`;
  await t.step("teams: create a team with a member", async () => {
    await owner.goto(cfg.app + "/settings/teams");
    await owner.getByRole("button", { name: "New team" }).first().click();
    const drawer = owner.getByRole("dialog", { name: "New team" });
    await drawer.getByLabel("Name").fill(team);
    await drawer.getByRole("button", { name: "Create team" }).click();
    await until(async () => (await api("GET", "/v1/teams", opts())).json?.data?.find((g) => g.name === team), "team saved");

    const ben = (await api("GET", "/v1/members", opts())).json?.data?.find((m) => m.user.name === "Ben Agent");
    assert(ben, "Ben Agent is a workspace member");
    await owner.getByRole("button", { name: "Add people" }).click();
    await owner.getByRole("option", { name: /Ben Agent/ }).click();
    await owner.getByRole("button", { name: "Save changes" }).click();

    const x = await until(async () => {
      const saved = (await api("GET", "/v1/teams", opts())).json?.data?.find((g) => g.name === team);
      return saved?.member_ids?.includes(ben.user.id) ? saved : null;
    }, "team member saved");
    assert(x.members?.some((m) => m.user_id === ben.user.id), "Ben Agent is on the team");
  });

  await t.step("labels: create a label", async () => {
    await owner.goto(cfg.app + "/settings/tags");
    await owner.getByRole("button", { name: "New label" }).first().click();
    await owner.getByLabel("New label name").fill("enterprise");
    await owner.getByRole("button", { name: "Create label" }).click();
    await until(async () => (await api("GET", "/v1/tags", opts())).json?.data?.some((g) => g.name === "enterprise"), "tag saved");
  });

  await t.step("SLA: create a policy", async () => {
    await owner.goto(cfg.app + "/settings/sla");
    await owner.getByRole("button", { name: "New policy" }).first().click();
    const dialog = owner.getByRole("dialog", { name: "New SLA policy" });
    await dialog.getByLabel("Name").fill("Urgent tickets");
    await dialog.getByRole("combobox", { name: "Applies to" }).click();
    await owner.getByRole("option", { name: "Urgent priority" }).click();
    await dialog.getByRole("button", { name: "Create policy" }).click();
    await until(async () => (await api("GET", "/v1/sla-policies", opts())).json?.data?.some((p) => p.name === "Urgent tickets" && p.priority === "urgent"), "policy saved");
  });

  await t.step("automations: create one and see it fire", async () => {
    await owner.goto(cfg.app + "/settings/automations");
    await owner.getByRole("link", { name: "New automation" }).first().click();
    await owner.getByLabel("Name").fill("Tag refund requests");
    // A new automation starts with one "subject contains" condition and one "add tag" action.
    await owner.getByLabel("Subject value").fill("refund");
    await owner.getByRole("button", { name: "Choose a label" }).click();
    await owner.keyboard.type("refund");
    await owner.getByRole("option", { name: /refund/i }).first().click();
    await owner.getByRole("button", { name: "Create automation" }).click();
    await until(async () => (await api("GET", "/v1/automations", opts())).json?.data?.some((a) => a.name === "Tag refund requests"), "automation saved");
    const subject = `Refund for order ${uid()}`;
    await inboundEmail(t.forwarding, rawEmail({ from: `buyer-${uid()}@customer.test`, to: t.supportEmail, subject, text: "Please refund my order." }));
    await until(async () => {
      const r = await api("GET", `/v1/tickets?q=${encodeURIComponent(subject)}`, opts());
      return r.json?.data?.find((x) => x.subject === subject)?.tags.some((g) => g.name === "refund");
    }, "automation tagged the ticket", 15_000);
  });

  await t.step("webhooks: signed deliveries to a local receiver", async () => {
    const rx = await receiver();
    try {
      await owner.goto(cfg.app + "/settings/webhooks");
      await owner.getByRole("button", { name: "New webhook" }).first().click();
      const dialog = owner.getByRole("dialog", { name: "New webhook" });
      await dialog.getByLabel("Endpoint URL").fill(rx.url);
      await dialog.getByRole("checkbox", { name: "All events" }).click();
      await dialog.getByRole("button", { name: "Create webhook" }).click();
      const secretDialog = owner.getByRole("dialog", { name: "Signing secret" });
      const secret = (await secretDialog.locator("span.mono").first().innerText()).trim();
      assert(secret.length > 16, "secret shown once");
      await secretDialog.getByRole("button", { name: "I've saved it" }).click();
      const hook = await until(async () => (await api("GET", "/v1/webhooks", opts())).json?.data?.find((w) => w.url === rx.url), "webhook saved");
      assert(JSON.stringify(hook.events) === '["*"]', `all events saved as * (${JSON.stringify(hook.events)})`);
      await inboundEmail(t.forwarding, rawEmail({ from: `hook-${uid()}@customer.test`, to: t.supportEmail, subject: `Webhook test ${uid()}`, text: "Hello" }));
      const d = await until(() => rx.got.find((x) => x.headers["od-event-type"] === "ticket.created"), "ticket.created delivered", 20_000);
      const [ts, v1] = String(d.headers["od-signature"]).split(",").map((p) => p.split("=")[1]);
      const want = createHmac("sha256", secret).update(`${ts}.${d.body}`).digest("hex");
      assert(timingSafeEqual(Buffer.from(want), Buffer.from(v1)), "signature verifies");
      assert(Math.abs(Date.now() / 1000 - Number(ts)) < 300, "timestamp fresh");
      await owner.goto(cfg.app + `/settings/webhooks/${hook.id}`);
      await owner.getByRole("button", { name: "Send test event" }).click();
      await until(() => rx.got.some((x) => x.headers["od-event-type"] === "webhook.test"), "test event delivered");
      await owner.getByRole("button", { name: "Refresh deliveries" }).click();
      await owner.getByText("ticket.created").first().waitFor();
      await owner.getByText("webhook.test").first().waitFor();
    } finally {
      rx.close();
    }
  });

  await t.step("API keys: create one and use it with curl", async () => {
    await owner.goto(cfg.app + "/settings/api-keys");
    await owner.getByRole("button", { name: "New key" }).first().click();
    const dialog = owner.getByRole("dialog", { name: "New API key" });
    await dialog.getByLabel("Name").fill("CI");
    await dialog.getByRole("radio", { name: "Read only" }).click();
    await dialog.getByRole("button", { name: "Create key" }).click();
    const shown = owner.getByRole("dialog", { name: "Copy your new key" });
    const key = (await shown.locator("span.mono").first().innerText()).trim();
    assert(key.startsWith("od_live_"), "key shown once");
    await shown.getByRole("button", { name: "Done" }).click();
    const get = await run$("curl", ["-s", "-o", "/dev/null", "-w", "%{http_code}", "-H", `Authorization: Bearer ${key}`, `${cfg.api}/v1/tickets?limit=1`]);
    assert(get.stdout === "200", `curl GET /v1/tickets -> ${get.stdout}`);
    const post = await run$("curl", ["-s", "-o", "/dev/null", "-w", "%{http_code}", "-X", "POST", "-H", `Authorization: Bearer ${key}`, "-H", "Content-Type: application/json", "-d", '{"name":"x"}', `${cfg.api}/v1/tags`]);
    assert(post.stdout === "403", `read-only key cannot write (${post.stdout})`);
  });

  await t.step("audit log lists the security events", async () => {
    const r = await api("GET", "/v1/audit-logs", opts());
    const actions = new Set((r.json?.data ?? []).map((x) => x.action));
    for (const a of ["api_key.created", "webhook.created"]) assert([...actions].some((x) => x === a), `audit has ${a} (${[...actions].join(", ")})`);
    await owner.goto(cfg.app + "/settings/audit-log");
    await owner.getByText(/API key/i).first().waitFor();
  });

  await t.step("workspace: change the customer language", async () => {
    await owner.goto(cfg.app + "/settings/workspace");
    await owner.getByRole("button", { name: "English" }).click();
    await owner.getByPlaceholder("Search languages").fill("Deutsch");
    await owner.getByRole("option", { name: /Deutsch/ }).click();
    await owner.getByRole("button", { name: "Save changes" }).click();
    await until(async () => (await api("GET", "/v1/workspace", opts())).json?.settings?.locale === "de", "locale saved");
  });

  await t.step("CSAT: solve a ticket, open the survey link and rate it", async () => {
    if (!cfg.mailSink) throw new Error("needs E2E_MAIL_SINK (smtp-sink.mjs) to read the survey email");
    await owner.goto(cfg.app + "/settings/workspace");
    const sw = owner.getByRole("switch", { name: "Satisfaction survey" });
    if ((await sw.getAttribute("aria-checked")) !== "true") await sw.click();
    await owner.getByRole("button", { name: "Save changes" }).click();
    await until(async () => (await api("GET", "/v1/workspace", opts())).json?.settings?.csat_enabled === true, "csat on");
    const email = `rate-${uid()}@customer.test`;
    const subject = `Please rate ${uid()}`;
    await inboundEmail(t.forwarding, rawEmail({ from: email, to: t.supportEmail, subject, text: "Can you help me?" }));
    await owner.goto(cfg.app + "/inbox/unassigned");
    await openTicket(owner, subject);
    const tid = owner.url().split("/").pop();
    await owner.getByRole("button", { name: "Solve", exact: true }).click();
    const link = await mailLink("/csat/", email, 20_000);
    const { page, problems } = await newPage("customer");
    t.problems.push(problems);
    await page.goto(link.replace(/\?score=\d/, "?score=4"));
    await page.getByRole("radio", { name: /^5,/ }).click();
    await page.getByLabel(/Anything you'd like to add/).fill("Quick and kind.");
    await page.getByRole("button", { name: /Send (feedback|comment)/ }).click();
    await page.getByText("Thanks for your feedback").waitFor();
    const tk = await until(async () => {
      const x = (await api("GET", `/v1/tickets/${tid}`, opts())).json;
      return x.csat?.score === 5 ? x : null;
    }, "score stored");
    assert(tk.csat.comment === "Quick and kind.", "comment stored");
    await page.context().close();
  });

  await t.step("billing: self-hosted view", async () => {
    await owner.goto(cfg.app + "/settings/billing");
    await owner.getByText("Self-hosted: unlimited").waitFor();
  });
}
