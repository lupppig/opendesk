// Logos of the products opendesk connects to (integrations, contracts/integrations.md;
// support channels, contracts/support-channels.md) and imports from: each vendor's own mark, shipped in public/logos/apps and drawn for
// a light background. Keys are the catalog's provider keys and the import sources.

const APP_LOGOS = new Set([
  "linear",
  "github",
  "atlassian",
  "sentry",
  "notion",
  "asana",
  "monday",
  "slack",
  "discord",
  "telegram",
  "intercom",
  "close",
  "stripe",
  "paypal",
  "square",
  "box",
  "zapier",
  "zendesk",
  "helpscout",
  "freshdesk",
]);

/** Where each product's pages live, so a plain link to one shows its logo. */
const APP_HOSTS: [string, string][] = [
  ["linear.app", "linear"],
  ["github.com", "github"],
  ["atlassian.net", "atlassian"],
  ["atlassian.com", "atlassian"],
  ["sentry.io", "sentry"],
  ["notion.so", "notion"],
  ["notion.site", "notion"],
  ["asana.com", "asana"],
  ["monday.com", "monday"],
  ["slack.com", "slack"],
  ["discord.com", "discord"],
  ["telegram.org", "telegram"],
  ["intercom.com", "intercom"],
  ["close.com", "close"],
  ["stripe.com", "stripe"],
  ["paypal.com", "paypal"],
  ["squareup.com", "square"],
  ["box.com", "box"],
  ["zapier.com", "zapier"],
  ["zendesk.com", "zendesk"],
  ["helpscout.net", "helpscout"],
  ["helpscout.com", "helpscout"],
  ["freshdesk.com", "freshdesk"],
];

/** The shipped logo for a product key, if any. */
export function appLogo(key: string | null | undefined): string | null {
  return key && APP_LOGOS.has(key) ? `/logos/apps/${key}.svg` : null;
}

/** The product a URL belongs to, by its host (the host or any subdomain of it). */
export function appForUrl(raw: string): string | null {
  let host: string;
  try {
    host = new URL(raw).hostname.toLowerCase();
  } catch {
    return null;
  }
  for (const [h, key] of APP_HOSTS) {
    if (host === h || host.endsWith("." + h)) return key;
  }
  return null;
}
