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

test("connect and route a support channel", async ({ page, proof }) => {
  await proof.open("/settings/support-channels");
  await expect(page.getByRole("heading", { name: "Support channels", exact: true })).toBeVisible();
  await expect(page.getByText("No support channels yet")).toBeVisible();
  await proof.chapter("Support channels", "Connect a provider, check its health and choose where tickets appear");
  await proof.shot("empty", { caption: "Support channels before a provider is connected" });

  await page.getByRole("button", { name: "Telegram" }).click();
  const connect = page.getByRole("dialog", { name: "Connect Telegram" });
  await expect(connect).toBeVisible();
  await connect.getByLabel("Connection name").fill("Support Telegram");
  await connect.getByLabel("Bot token").fill("demo-telegram-token");
  await proof.shot("connect", { caption: "Telegram onboarding with its generated webhook secret kept out of the form", target: connect });
  await connect.getByRole("button", { name: "Connect", exact: true }).click();

  await expect(page.getByText("Support Telegram")).toBeVisible();
  await expect(page.getByText("Healthy", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Test connection" })).toBeVisible();
  await proof.shot("connected", { caption: "A healthy connection with testing, rotation and destination controls" });

  await page.getByRole("button", { name: "Add destination" }).click();
  const destination = page.getByRole("dialog", { name: "Add destination" });
  await expect(destination).toBeVisible();
  await destination.getByLabel("Provider channel").click();
  await page.getByRole("option", { name: "#support" }).click();
  await destination.getByRole("switch", { name: "Include internal notes" }).click();
  await proof.shot("destination", { caption: "A discovered provider channel routed to public teams", target: destination });
  await destination.getByRole("button", { name: "Add destination" }).click();

  await expect(page.getByRole("button", { name: "Remove support" })).toBeVisible();
  await expect(page.getByText(/All teams/)).toBeVisible();
  await expect(page.getByText(/with notes/)).toBeVisible();
  await proof.shot("routed", { caption: "The connected destination, including internal notes" });

  await page.getByRole("button", { name: "Rotate credentials for Support Telegram" }).click();
  const rotate = page.getByRole("dialog", { name: "Rotate credentials for Support Telegram" });
  await expect(rotate).toBeVisible();
  await proof.shot("rotate", { caption: "Credential rotation keeps destinations and ticket threads connected", target: rotate });
});
