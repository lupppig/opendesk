// The small pills beside an account's name in the sidebar: Away while its automatic reply is on,
// and the blue +N of its addresses besides its own. The mail section and Space rows share them.

import { useTranslation } from "react-i18next";
import { Badge } from "@/components/ui/badge";
import { Tooltip } from "@/components/ui/tooltip";
import type { MailAccount } from "@/lib/api/types";
import { aliasesOf } from "@/lib/collab/identities";
import { isAway } from "@/lib/collab/outOfOffice";

export function AwayPill() {
  const { t } = useTranslation("collab");
  return (
    <Tooltip content={t("nav.awayTip")}>
      <span className="ml-auto flex shrink-0">
        <Badge tone="amber" className="h-4 rounded-[4px] px-1 text-[10px]">
          {t("nav.away")}
        </Badge>
      </span>
    </Tooltip>
  );
}

/** The blue +N beside an account with aliases: how many addresses it has besides its own, and which. The mail page picks one. */
function AliasPill({ account }: { account: MailAccount }) {
  const { t } = useTranslation("collab");
  const aliases = aliasesOf(account);
  return (
    <Tooltip
      content={
        <span className="flex flex-col gap-0.5">
          <span>{t("address.pillTip", { count: aliases.length })}</span>
          {aliases.slice(0, 8).map((x) => (
            <span key={x.email} className="mono text-[11px] opacity-80">
              {x.email}
            </span>
          ))}
          {aliases.length > 8 && <span className="opacity-80">{t("address.more", { count: aliases.length - 8 })}</span>}
        </span>
      }
    >
      <span className="flex shrink-0">
        <Badge tone="blue" className="h-4 rounded-[4px] px-1 text-[10px] tabular-nums">
          +{aliases.length}
        </Badge>
      </span>
    </Tooltip>
  );
}

/** Away and the addresses pill for an account's row, or nothing. */
export function AccountTags({ account }: { account: MailAccount }) {
  const away = isAway(account);
  const aliases = aliasesOf(account).length;
  if (!away && !aliases) return null;
  return (
    <span className="ml-auto flex shrink-0 items-center gap-1">
      {away && <AwayPill />}
      {aliases > 0 && <AliasPill account={account} />}
    </span>
  );
}
