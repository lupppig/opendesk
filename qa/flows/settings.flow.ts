import type { Page } from "@playwright/test";
import { expect, test } from "../lib/proof.ts";

test("let visitors send voice messages from a chat widget", async ({ page, proof }) => {
  await proof.open("/settings/brands");
  await page.getByRole("link", { name: /^Margin Default/ }).click();
  await page.getByRole("tab", { name: /Chat widgets/ }).click();
  await page.getByRole("tabpanel").getByRole("link").first().click();
  const voice = page.getByRole("switch", { name: "Let visitors send voice messages" });
  await expect(voice).toBeVisible();
  await voice.scrollIntoViewIfNeeded();
  await proof.chapter("Voice messages", "Turn them on for a widget and set the longest recording");
  await proof.shot("voice-off", { caption: "Voice messages, off by default" });

  await voice.click();
  const length = page.getByLabel("Longest voice message");
  await expect(length).toBeVisible();
  await proof.shot("voice-on", { caption: "On, with the longest voice message in seconds" });

  await length.fill("90");
  await length.blur();
  await expect(length).toHaveValue("90");
  await proof.dwell();
  await proof.shot("voice-length", { caption: "The longest voice message set to 90 seconds" });
});

// Ollie's nudge about unowned tickets would sit over the page header.
async function openSupportChannels(page: Page, proof: { open: (path: string) => Promise<unknown> }) {
  await proof.open("/settings/support-channels");
  await page
    .getByRole("button", { name: "Not now" })
    .click({ timeout: 3_000 })
    .catch(() => {});
}

test("connect Discord, pick its server and route a channel in one go", async ({ page, proof }) => {
  await openSupportChannels(page, proof);
  await expect(page.getByRole("heading", { name: "Support channels", exact: true })).toBeVisible();
  await expect(page.getByText("No support channels yet")).toBeVisible();
  await proof.chapter("Support channels", "Connect a bot and choose where tickets appear");
  await proof.shot("empty", { caption: "Support channels before a provider is connected, with the new tip about creating tickets" });

  await page.getByRole("button", { name: "Discord" }).click();
  const connect = page.getByRole("dialog", { name: "Connect Discord" });
  await expect(connect).toBeVisible();
  await connect.getByLabel("Bot token").pressSequentially("demo-discord-token", { delay: 20 });
  await connect.getByLabel("Interactions public key").fill("demo-public-key");
  await connect.getByRole("button", { name: "Find servers" }).click();
  await expect(connect.getByText("Margin community")).toBeVisible();
  await proof.shot("connect-discord", { caption: "The bot's only server is chosen for you", target: connect });
  await connect.getByRole("button", { name: "Connect", exact: true }).click();

  const destination = page.getByRole("dialog", { name: "Add destination" });
  await expect(destination).toBeVisible();
  await destination.getByLabel("Provider channel").click();
  await page.getByRole("option", { name: "#support" }).click();
  await proof.shot("destination", { caption: "Right after connecting, the channel picker opens with the bot's text channels", target: destination });
  await destination.getByRole("button", { name: "Add destination" }).click();

  await expect(page.getByRole("button", { name: "Remove support" })).toBeVisible();
  await expect(page.getByText("Connected", { exact: true })).toBeVisible();
  await proof.dwell();
  await proof.shot("connected", { caption: "One status for the connection, and no webhook address to copy: opendesk set Discord up" });
});

test("finish Slack's setup and route Telegram by channel ID", async ({ page, proof }) => {
  await openSupportChannels(page, proof);
  await expect(page.getByText("No support channels yet")).toBeVisible();
  await proof.chapter("Slack", "The steps opendesk can't take for you, with the address to paste");
  await page.getByRole("button", { name: "Slack" }).click();
  const connect = page.getByRole("dialog", { name: "Connect Slack" });
  await connect.getByLabel("Bot token").fill("xoxb-demo");
  await connect.getByLabel("Signing secret").fill("demo-signing-secret");
  await connect.getByRole("button", { name: "Connect", exact: true }).click();
  const destination = page.getByRole("dialog", { name: "Add destination" });
  await destination.getByLabel("Provider channel").click();
  await page.getByRole("option", { name: "#support" }).click();
  await destination.getByRole("button", { name: "Add destination" }).click();
  await expect(page.getByText("Finish setting up the app")).toBeVisible();
  await expect(page.getByText("Finish setup", { exact: true })).toBeVisible();
  await proof.shot("slack-setup", { caption: "Slack waits on two settings in Slack itself: the steps and the address, until Slack calls in" });

  await proof.chapter("Telegram", "A new bot lists no groups yet, so enter the group's ID");
  await page.getByRole("button", { name: "Telegram" }).click();
  const telegram = page.getByRole("dialog", { name: "Connect Telegram" });
  await telegram.getByLabel("Bot token").fill("demo-telegram-token");
  await telegram.getByRole("button", { name: "Connect", exact: true }).click();
  await expect(destination).toBeVisible();
  await expect(destination.getByText("No forum groups yet.", { exact: false })).toBeVisible();
  await proof.shot("telegram-none", { caption: "What to do in Telegram when the bot hasn't seen a group yet", target: destination });
  await destination.getByRole("button", { name: "Enter a channel ID instead" }).click();
  await destination.getByLabel("Channel ID").pressSequentially("-1001234567890", { delay: 40 });
  await proof.shot("telegram-id", { caption: "Or route the group by its ID", target: destination });
  await destination.getByRole("button", { name: "Add destination" }).click();

  await expect(page.getByRole("button", { name: "Remove -1001234567890" })).toBeVisible();
  await expect(page.getByText("@margin_support_bot", { exact: false })).toBeVisible();
  await proof.dwell();
  await proof.shot("connections", { caption: "Slack still needs its steps; Telegram shows the bot's handle and its routed group" });
});
