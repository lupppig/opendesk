// Starter pages a new wiki offers: a real outline to fill in rather than a blank page.
// Their words live in the wiki catalog, so they come in the reader's language.

import type { TFunction } from "i18next";

export const TEMPLATES = [
  { id: "escalation", icon: "🚨" },
  { id: "refunds", icon: "💸" },
  { id: "onboarding", icon: "👋" },
] as const;

export type TemplateId = (typeof TEMPLATES)[number]["id"];

export function templatePage(t: TFunction<"wiki">, id: TemplateId) {
  const tpl = TEMPLATES.find((x) => x.id === id)!;
  return { title: t(`templates.${id}.title`), icon: tpl.icon, body_html: t(`templates.${id}.body`) };
}
