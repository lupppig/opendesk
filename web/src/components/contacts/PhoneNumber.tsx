// A contact's phone number. With a phone system connected (contracts/phone.md) it is a
// tel: link: the operating system hands it to the default phone app, such as Aircall
// Workspace or the Voys app once it is set as the handler for phone links.

import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Tooltip } from "@/components/ui/tooltip";
import { cn } from "@/lib/cn";
import { q } from "@/lib/queries";

/** A dialable tel: link for a number as people write it, or null. A 00 prefix becomes +. */
export function telHref(raw: string): string | null {
  const s = raw.trim().replace(/\(0\)/g, "");
  if (!s || /[^\d\s+().\-/]/.test(s) || s.indexOf("+") > 0) return null;
  let digits = s.replace(/\D/g, "");
  let plus = s.startsWith("+");
  if (!plus && digits.startsWith("00")) {
    digits = digits.slice(2);
    plus = true;
  }
  if (digits.length < 3 || digits.length > 15) return null;
  return `tel:${plus ? "+" : ""}${digits}`;
}

/** Phone providers by their key (contracts/phone.md). */
export const PROVIDER_NAMES: Record<string, string> = { aircall: "Aircall", voys: "Voys" };

export function PhoneNumber({ phone, className }: { phone: string; className?: string }) {
  const { t } = useTranslation("contacts");
  const { data: phones } = useQuery({ ...q.phoneConnections(), staleTime: 5 * 60_000 });
  const href = telHref(phone);
  const provider = phones?.[0]?.provider;
  if (!href || !provider) {
    return (
      <span className={className} data-ph-mask>
        {phone}
      </span>
    );
  }
  return (
    <Tooltip content={t("callWith", { provider: PROVIDER_NAMES[provider] ?? provider })}>
      <a href={href} className={cn("underline-offset-2 hover:text-fg hover:underline", className)} data-ph-mask>
        {phone}
      </a>
    </Tooltip>
  );
}
