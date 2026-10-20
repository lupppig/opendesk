// Last: a member deletes their account, the owner deletes the workspace
// (owner + MFA) and then their own account with a fresh 2FA code. Needs the
// TOTP steps of the account flow (no E2E_SKIP_MFA).

import { PASSWORD, api, assert, cfg, totp, until } from "../lib.mjs";

/** Waits for a TOTP step after the last one used, since a step is accepted once. */
async function freshCode(page, t) {
  while (Math.floor(Date.now() / 30_000) <= t.totpStep) await page.waitForTimeout(500);
  t.totpStep = Math.floor(Date.now() / 30_000);
  return totp(t.totpSecret);
}

export async function run(t) {
  const owner = t.owner.page;
  const agent = t.agent.page;
  if (!t.totpSecret) throw new Error("danger flow needs the TOTP steps (unset E2E_SKIP_MFA)");
  const opts = () => ({ token: t.ownerToken, workspace: t.workspaceId });

  await t.step("member deletes their account", async () => {
    await agent.goto(cfg.app + "/settings/security");
    await agent.getByRole("button", { name: "Delete account" }).click();
    const dialog = agent.getByRole("dialog", { name: "Delete your account?" });
    await dialog.getByLabel("Password", { exact: true }).fill(PASSWORD);
    await dialog.getByLabel(/to confirm/).fill("delete");
    await dialog.getByRole("button", { name: "Delete account" }).click();
    await agent.waitForURL(/\/login/);
    await until(async () => !(await api("GET", "/v1/members", opts())).json?.data?.some((m) => m.user.email === t.agent.email), "member gone");
    const r = await api("POST", "/v1/auth/login", { body: { email: t.agent.email, password: PASSWORD } });
    assert(r.status === 401, `login refused after deletion (${r.status})`);
  });

  await t.step("owner deletes the workspace (MFA session)", async () => {
    await owner.goto(cfg.app + "/settings/workspace");
    const name = (await api("GET", "/v1/workspace", opts())).json.name;
    await owner.getByRole("button", { name: "Delete workspace" }).click();
    const dialog = owner.getByRole("alertdialog").or(owner.getByRole("dialog")).filter({ hasText: "Delete workspace?" });
    await dialog.getByLabel("Confirmation text").fill(name);
    await dialog.getByRole("button", { name: "Delete workspace" }).click();
    await owner.waitForURL(/\/onboarding/);
    const r = await api("GET", "/v1/workspace", opts());
    assert(r.status === 400 && r.json?.code === "workspace_required", `workspace gone (${r.status})`);
  });

  await t.step("owner deletes their account from onboarding with a 2FA code", async () => {
    // With no workspace left, onboarding is the only page: it offers log out and delete.
    await owner.getByRole("button", { name: "Delete account" }).click();
    const dialog = owner.getByRole("dialog", { name: "Delete your account?" });
    await dialog.getByLabel("Password", { exact: true }).fill(PASSWORD);
    await dialog.getByLabel(/to confirm/).fill("delete");
    await dialog.getByLabel("Two-factor code").fill(await freshCode(owner, t));
    await dialog.getByRole("button", { name: "Delete account" }).click();
    await owner.waitForURL(/\/login/);
    const r = await api("POST", "/v1/auth/login", { body: { email: t.owner.email, password: PASSWORD } });
    assert(r.status === 401, `login refused after deletion (${r.status})`);
  });
}
