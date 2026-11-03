// Small helpers shared by the bot pages (contracts/bots.md).

import { useTranslation } from "react-i18next";
import type { Bot } from "@/lib/api/types";

/** "Answers on Marketing site, Docs and 1 more", or that it answers nowhere yet. */
export function useUsageLine() {
  const { t } = useTranslation("bots");
  return (b: Bot): string => {
    const names = [
      ...b.usage.widgets.map((w) => w.name),
      ...b.usage.mailboxes.map((m) => m.email),
      ...(b.usage.api_keys ?? []).map((k) => t("usage.apiKey", { name: k.name })),
    ];
    if (!names.length) return b.is_default ? t("usage.defaultNone") : t("usage.none");
    const shown = names.slice(0, 2);
    const more = names.length - shown.length;
    const list = more > 0 ? t("usage.listMore", { list: shown.join(", "), count: more }) : new Intl.ListFormat("en", { type: "conjunction" }).format(shown);
    return t("usage.on", { list });
  };
}

/** Where a bot answers, as one count: widgets, addresses and the API keys that speak as it. */
export const usageCount = (b: Bot) => b.usage.widgets.length + b.usage.mailboxes.length + (b.usage.api_keys?.length ?? 0);
