// Account: register, verify, onboarding (workspace + forwarding mailbox),
// log out, log in, refresh rotation on reload, TOTP setup and MFA login.

import { PASSWORD, assert, cfg, mailLink, newPage, shot, totp, uid, until } from "../lib.mjs";

export async function register(page, { name, email }) {
  await page.goto(cfg.app + "/register");
  await page.getByLabel("Name").fill(name);
  await page.getByLabel("Work email").fill(email);
  await page.getByRole("textbox", { name: "Password" }).fill(PASSWORD);
  await page.getByRole("button", { name: /create account/i }).click();
  await page.waitForURL(/\/onboarding/);
  await page.goto(await mailLink("/verify", email));
  await page.getByText("Email verified").waitFor();
}

export async function login(page, email, password = PASSWORD) {
  await page.goto(cfg.app + "/login");
  await page.getByLabel("Email").fill(email);
  await page.getByRole("textbox", { name: "Password" }).fill(password);
  await page.getByRole("button", { name: /^log in$/i }).click();
}

export async function logout(page) {
  await page.getByRole("button", { name: "Account menu" }).click();
  await page.getByRole("menuitem", { name: "Log out", exact: true }).click();
  await page.waitForURL(/\/login/);
}

/** The access token the app holds in memory, read through a fresh refresh. */
export async function accessToken(page) {
  return page.evaluate(async (api) => {
    const r = await fetch(api + "/v1/auth/refresh", { method: "POST", credentials: "include" });
    return r.ok ? (await r.json()).access_token : null;
  }, cfg.api);
}

export async function run(t) {
  const { page, problems, ctx } = await newPage("owner");
  t.problems.push(problems);
  const email = `owner-${uid()}@example.com`;
  t.owner = { email, name: "Ada Owner", page, ctx };

  await t.step("register and verify email", async () => {
    await register(page, { name: "Ada Owner", email });
  });

  await t.step("onboarding: create workspace", async () => {
    await page.goto(cfg.app + "/onboarding");
    await page.getByLabel("Workspace name").fill(`Acme ${uid()}`);
    await page.getByRole("button", { name: /create workspace|continue/i }).click();
    await page.waitForURL(/\/onboarding\/mailbox/);
  });

  await t.step("onboarding: forwarding mailbox", async () => {
    const support = `support-${uid()}@acme.test`;
    await page.getByLabel("Name", { exact: true }).fill("Support");
    await page.getByLabel("Email address").fill(support);
    await page.keyboard.press("Tab"); // commit the address so host detection runs
    await page.getByRole("radio", { name: /forward mail to opendesk/i }).click();
    await page.getByRole("button", { name: "Continue", exact: true }).click();
    await page.getByRole("button", { name: "Create mailbox" }).click();
    const addr = await until(async () => {
      const v = await page.locator("input[readonly], .mono").filter({ hasText: /@/ }).allInnerTexts();
      const inputs = await page.locator("input[readonly]").evaluateAll((els) => els.map((e) => e.value));
      return [...v, ...inputs].find((x) => x.includes("@in.") || x.includes("in.localhost"));
    }, "forwarding address shown");
    t.forwarding = addr.trim();
    t.supportEmail = support;
    await shot(page, "account-mailbox");
  });

  await t.step("finish onboarding to the inbox", async () => {
    await page.getByRole("button", { name: "Done", exact: true }).click();
    await page.waitForURL(/\/inbox\//);
    t.workspaceId = await page.evaluate(() => {
      for (let i = 0; i < localStorage.length; i++) {
        const k = localStorage.key(i);
        if (k?.startsWith("od-ws:")) return localStorage.getItem(k);
      }
      return null;
    });
    assert(t.workspaceId, "workspace remembered");
  });

  await t.step("log out and log in", async () => {
    await logout(page);
    await login(page, email);
    await page.waitForURL(/\/inbox\//);
  });

  await t.step("reload keeps the session (refresh rotation)", async () => {
    // One refresh cookie per signed-in account: od_rt_<user id hex>.
    const before = (await ctx.cookies()).find((c) => c.name.startsWith("od_rt_"))?.value;
    await page.reload();
    await page.waitForURL(/\/inbox\//);
    await page.getByRole("button", { name: "Account menu" }).waitFor();
    const after = (await ctx.cookies()).find((c) => c.name.startsWith("od_rt_"))?.value;
    assert(before && after && before !== after, "refresh cookie rotated");
    t.ownerToken = await accessToken(page);
  });

  if (process.env.E2E_SKIP_MFA === "1") return;

  await t.step("TOTP setup", async () => {
    await page.goto(cfg.app + "/settings/security");
    await page.getByRole("button", { name: "Set up" }).click();
    const dialog = page.getByRole("dialog");
    const secret = await until(async () => {
      const txt = await dialog.innerText();
      const m = txt.replace(/\s/g, "").match(/[A-Z2-7]{16,}/);
      return m?.[0];
    }, "totp secret shown");
    t.totpSecret = secret;
    await dialog.getByRole("button", { name: "Continue" }).click();
    await dialog.getByLabel("6-digit code").fill(await totp(secret));
    t.totpStep = Math.floor(Date.now() / 30_000);
    await dialog.getByRole("button", { name: "Verify" }).click();
    await dialog.getByText(/recovery/i).first().waitFor();
    // Mark the codes saved (copy or download), then finish.
    await dialog.getByRole("button", { name: /copy/i }).first().click().catch(() => {});
    const saved = dialog.getByRole("checkbox");
    if (await saved.count()) await saved.first().click();
    await dialog.getByRole("button", { name: "Done" }).click();
    await page.getByText("On", { exact: true }).first().waitFor();
  });

  await t.step("MFA login", async () => {
    await logout(page);
    await login(page, email);
    await page.getByLabel("6-digit code").waitFor();
    // The step used at setup is consumed (replay refused): wait for the next one.
    while (Math.floor(Date.now() / 30_000) <= t.totpStep) await page.waitForTimeout(500);
    t.totpStep = Math.floor(Date.now() / 30_000);
    await page.getByLabel("6-digit code").fill(await totp(t.totpSecret));
    const submit = page.getByRole("button", { name: /verify|continue|log in/i });
    if (await submit.isEnabled()) await submit.click().catch(() => {});
    await page.waitForURL(/\/inbox\//);
    t.ownerToken = await accessToken(page);
  });
}
