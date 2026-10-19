// Settings → Mail accounts (contracts/collab.md): the accounts you see, yours first,
// with who connected them, private or shared, and how their sync is doing. Connect
// one with Google, Microsoft or IMAP, privately or shared with your team.

import { useNavigate, useSearch } from "@tanstack/react-router";
import { ChevronRight, Lock, Plus, RefreshCw, Users } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { ListRow, Section, SettingsPage } from "@/components/layout";
import { SettingsEmpty } from "@/components/settings2/SettingsEmpty";
import { ListSkeleton, StatePill, type ItemState } from "@/components/settings2/bits";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty";
import { toast } from "@/components/ui/toast";
import { Tooltip } from "@/components/ui/tooltip";
import { AccountDot, ProviderMark } from "@/components/collab/bits";
import { ConnectAccountDialog, startOAuth } from "@/components/collab/ConnectAccount";
import { useAudienceSummary } from "@/components/collab/pickers";
import type { MailAccount } from "@/lib/api/types";
import { errorMessage } from "@/lib/api/client";
import { useCanAct, useCollab, useSettingsAccounts } from "@/lib/collab/hooks";
import { relLong } from "@/lib/format";
import { useSession } from "@/lib/session";

const stateOf: Record<MailAccount["status"], ItemState> = { active: "active", syncing: "pending", error: "failing", paused: "paused" };
const oauthErrors = ["oauth_state", "access_denied", "provider_error", "account_conflict", "collab_required", "forbidden", "internal"] as const;

export function AccountStatus({ account }: { account: MailAccount }) {
  const { t } = useTranslation("collab");
  const pill = (
    <StatePill state={account.reconnect ? "failing" : stateOf[account.status]} pulse={account.status === "syncing"}>
      {account.reconnect ? t("nav.reconnect") : t(`accounts.status.${account.status}`)}
    </StatePill>
  );
  const tip = account.status === "paused" ? t("accounts.pausedHint") : account.last_error || null;
  return tip ? (
    <Tooltip content={<span className="block max-w-[260px] font-normal leading-snug">{tip}</span>}>
      <span tabIndex={0} className="flex rounded-sm outline-none focus-visible:shadow-[0_0_0_2px_var(--focus)]">
        {pill}
      </span>
    </Tooltip>
  ) : (
    pill
  );
}

/** Connect the account again: the provider's sign-in for Google and Microsoft, the server settings for IMAP. */
export function useReconnect() {
  const navigate = useNavigate();
  return async (a: MailAccount) => {
    if (a.provider === "imap") return void navigate({ to: "/settings/mail-accounts/$accountId", params: { accountId: a.id } });
    try {
      await startOAuth(a.provider, { visibility: a.visibility, shared_with: a.visibility === "shared" ? a.shared_with : undefined, account_id: a.id, email: a.email });
    } catch (e) {
      toast.error(errorMessage(e));
    }
  };
}

export default function MailAccounts() {
  const { t } = useTranslation("collab");
  const navigate = useNavigate();
  const search = useSearch({ from: "/app/settings/mail-accounts" });
  const collab = useCollab();
  const canAct = useCanAct();
  const me = useSession((s) => s.user?.id);
  const { data, isLoading } = useSettingsAccounts();
  const [connect, setConnect] = useState(false);
  const summary = useAudienceSummary();
  const reconnect = useReconnect();
  const handled = useRef(false);

  // Back from the provider: ?connected=<id> or ?error=<code>.
  useEffect(() => {
    if (handled.current || (!search.connected && !search.error)) return;
    if (search.connected && isLoading) return;
    handled.current = true;
    if (search.connected) {
      const a = data?.find((x) => x.id === search.connected);
      toast.success(t("accounts.connected", { email: a?.email ?? "" }));
    } else if (search.error) {
      const code = (oauthErrors as readonly string[]).includes(search.error) ? (search.error as (typeof oauthErrors)[number]) : null;
      toast.error(code ? t(`accounts.error.${code}`) : t("accounts.error.fallback"));
    }
    void navigate({ to: "/settings/mail-accounts", search: {}, replace: true });
  }, [search.connected, search.error, isLoading, data, navigate, t]);

  if (collab.isFetched && !collab.enabled)
    return (
      <SettingsPage title={t("accounts.title")} description={t("accounts.description")}>
        <Section flush>
          <EmptyState
            art="inbox"
            title={t("accounts.upsell.title")}
            description={collab.status?.reason === "disabled" ? t("accounts.upsell.disabled") : t("accounts.upsell.description")}
            action={
              collab.status?.reason === "plan" ? (
                <Button variant="primary" onClick={() => void navigate({ to: "/settings/billing", search: {} })}>
                  {t("accounts.upsell.title")}
                </Button>
              ) : undefined
            }
          />
        </Section>
      </SettingsPage>
    );

  const mine = (data ?? []).filter((a) => a.owner?.id === me);
  const others = (data ?? []).filter((a) => a.owner?.id !== me);
  const connectButton = canAct && collab.status && (
    <Button variant="primary" icon={<Plus />} onClick={() => setConnect(true)}>
      {t("accounts.connect")}
    </Button>
  );

  const row = (a: MailAccount) => (
    <ListRow key={a.id} onClick={() => void navigate({ to: "/settings/mail-accounts/$accountId", params: { accountId: a.id } })}>
      <ProviderMark provider={a.provider} size="md" />
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-center gap-1.5">
          <AccountDot color={a.color} />
          <span className="truncate text-[13px] font-medium">{a.name || a.email}</span>
          <Badge tone={a.visibility === "private" ? "neutral" : "blue"}>
            {a.visibility === "private" ? <Lock className="size-2.5" /> : <Users className="size-2.5" />}
            {t(`accounts.badge.${a.visibility}`)}
          </Badge>
        </div>
        <div className="mt-0.5 flex min-w-0 items-center gap-1.5 text-[11.5px] text-fg-3">
          <span className="mono truncate" data-ph-mask>
            {a.email}
          </span>
          <span className="text-fg-4">·</span>
          <span className="truncate">{a.owner ? (a.owner.id === me ? t("accounts.owner", { name: t("accounts.you") }) : t("accounts.owner", { name: a.owner.name })) : t("accounts.ownerGone")}</span>
          {a.visibility === "shared" && (
            <>
              <span className="text-fg-4">·</span>
              <span className="truncate">{summary(a.shared_with)}</span>
            </>
          )}
        </div>
      </div>
      <span className="hidden w-28 text-right text-[11.5px] text-fg-4 md:block">{a.last_synced_at ? t("accounts.lastSync", { when: relLong(a.last_synced_at) }) : t("accounts.neverSynced")}</span>
      {a.reconnect && a.can_manage ? (
        <Button
          size="xs"
          variant="secondary"
          icon={<RefreshCw />}
          onClick={(e) => {
            e.stopPropagation();
            void reconnect(a);
          }}
        >
          {t("accounts.reconnect")}
        </Button>
      ) : (
        <AccountStatus account={a} />
      )}
      <ChevronRight className="size-3.5 shrink-0 text-fg-4" />
    </ListRow>
  );

  return (
    <SettingsPage title={t("accounts.title")} description={t("accounts.description")} actions={data?.length ? connectButton : undefined}>
      {isLoading || !collab.isFetched ? (
        <Section flush>
          <ListSkeleton rows={3} />
        </Section>
      ) : !data?.length ? (
        <Section flush>
          <SettingsEmpty
            art="inbox"
            title={t("accounts.empty.title")}
            description={t("accounts.empty.description")}
            action={connectButton || undefined}
            tips={[{ label: t("accounts.empty.tipPrivate"), icon: <Lock /> }, { label: t("accounts.empty.tipShared"), icon: <Users /> }]}
          />
        </Section>
      ) : (
        <>
          {mine.length > 0 && (
            <Section title={t("accounts.yours")} flush>
              {mine.map(row)}
            </Section>
          )}
          {others.length > 0 && (
            <Section title={t("accounts.sharedWithYou")} flush>
              {others.map(row)}
            </Section>
          )}
        </>
      )}
      {collab.status && <ConnectAccountDialog open={connect} onOpenChange={setConnect} status={collab.status} />}
    </SettingsPage>
  );
}
