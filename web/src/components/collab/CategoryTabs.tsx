// Inbox categories above the mail list (contracts/collab.md "Categories"): the tabs of
// an inbox that is split, and, while none in view is, a quiet offer to split one.

import { Link } from "@tanstack/react-router";
import { SlidersHorizontal, X } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { MailAccount } from "@/lib/api/types";
import type { MailSearch } from "@/lib/collab/boxes";
import { categoryTone, PRIMARY, type CategoryTab } from "@/lib/collab/categories";
import { cn } from "@/lib/cn";
import { atLeast, useRole, useSession } from "@/lib/session";
import { AI_TONE, tone } from "@/lib/tones";
import { OllieMark } from "@/components/assistant/OllieMark";
import { Button } from "@/components/ui/button";
import { Tooltip } from "@/components/ui/tooltip";
import { CategorySetupDialog } from "./CategorySetup";

/** The tabs of the inbox in view; Primary carries no search key, so it is the inbox's plain link. */
export function CategoryTabs({ tabs, active, search, settings }: { tabs: CategoryTab[]; active: string; search: MailSearch; settings: MailAccount | "workspace" | null }) {
  const { t } = useTranslation("collab");
  return (
    <nav aria-label={t("categories.tabs")} className="flex h-10 shrink-0 items-center gap-0.5 overflow-x-auto border-b border-line-soft px-3 [scrollbar-width:none]">
      {tabs.map((c) => {
        const on = c.key === active;
        return (
          <Link
            key={c.key}
            to="/mail/$box/{-$threadId}"
            params={{ box: "inbox", threadId: undefined }}
            search={{ ...search, category: c.key === PRIMARY ? undefined : c.key }}
            aria-current={on ? "page" : undefined}
            title={c.description || undefined}
            className={cn(
              "flex h-7 shrink-0 items-center gap-1.5 rounded-md px-2.5 text-[12.5px] outline-none transition-colors duration-100 focus-visible:shadow-[0_0_0_2px_var(--focus)]",
              on ? "bg-active font-medium text-fg" : "text-fg-3 hover:bg-hover hover:text-fg-2",
            )}
          >
            <span aria-hidden className={cn("size-2 shrink-0 rounded-full", tone[categoryTone(c)].solid)} />
            <span className="max-w-[160px] truncate">{c.name}</span>
            {c.unread > 0 && (
              <span className={cn("mono rounded-sm px-1 text-[10.5px] tabular-nums", on ? "text-fg-3" : tone[categoryTone(c)].chip)}>{c.unread > 999 ? "999+" : c.unread}</span>
            )}
          </Link>
        );
      })}
      {settings && (
        <Tooltip content={t("categories.settings")}>
          {settings === "workspace" ? (
            <Link to="/settings/mail-categories" aria-label={t("categories.settings")} className={gearCls}>
              <SlidersHorizontal />
            </Link>
          ) : (
            <Link to="/settings/mail-accounts/$accountId" params={{ accountId: settings.id }} hash="categories" aria-label={t("categories.settings")} className={gearCls}>
              <SlidersHorizontal />
            </Link>
          )}
        </Tooltip>
      )}
    </nav>
  );
}

const gearCls =
  "ml-auto flex size-7 shrink-0 items-center justify-center rounded-md text-fg-4 outline-none transition-colors hover:bg-hover hover:text-fg-2 focus-visible:shadow-[0_0_0_2px_var(--focus)] [&_svg]:size-3.5";

/** Where the tabs' settings live for the caller: the one account in view they manage, else the workspace's for an admin. */
export function useCategorySettingsTarget(accounts: MailAccount[]): MailAccount | "workspace" | null {
  const admin = atLeast(useRole(), "admin");
  const own = accounts.filter((a) => a.can_manage);
  if (accounts.length === 1 && own.length === 1) return own[0];
  if (admin) return "workspace";
  return own.length === 1 ? own[0] : null;
}

/** An account that could be split and never was: no default reaches it and its owner has not chosen. */
export const canOfferCategories = (a: MailAccount) => a.can_manage && !a.categories.enabled && !a.categories.locked && a.categories.mode === "follow" && a.categories.source === "none";

function dismissedKey(wid: string | null) {
  return `od-mail-categories-offer:${wid ?? ""}`;
}

function wasDismissed(key: string) {
  try {
    return localStorage.getItem(key) === "1";
  } catch {
    return false;
  }
}

/** The offer to split an inbox: Ollie suggests categories from the account's own mail. */
export function CategoryOffer({ account }: { account: MailAccount }) {
  const { t } = useTranslation("collab");
  const wid = useSession((s) => s.workspaceId);
  const key = dismissedKey(wid);
  const [hidden, setHidden] = useState(() => wasDismissed(key));
  const [open, setOpen] = useState(false);
  if (hidden && !open) return null;
  const dismiss = () => {
    setHidden(true);
    try {
      localStorage.setItem(key, "1");
    } catch {
      /* private mode: hidden until reload */
    }
  };
  const hue = tone[AI_TONE];
  return (
    <>
      {!hidden && (
        <div role="status" className={cn("flex shrink-0 animate-rise-in items-center gap-3 border-b px-4 py-2 motion-reduce:animate-none", hue.soft, hue.line)}>
          <OllieMark size={18} />
          <p className="min-w-0 flex-1 text-[12.5px] leading-[18px]">
            <span className="font-medium">{t("categories.offer.title")}</span> <span className="text-fg-3">{t("categories.offer.body")}</span>
          </p>
          <Button size="xs" variant="primary" onClick={() => setOpen(true)}>
            {t("categories.offer.action")}
          </Button>
          <Tooltip content={t("categories.offer.dismiss")}>
            <Button size="xs" variant="ghost" square icon={<X />} aria-label={t("categories.offer.dismiss")} onClick={dismiss} />
          </Tooltip>
        </div>
      )}
      {/* Once saved, the account is split and the tabs take this place. */}
      <CategorySetupDialog account={account} open={open} onOpenChange={setOpen} />
    </>
  );
}
