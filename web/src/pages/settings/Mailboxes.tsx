import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { Forward, MoreHorizontal, Plus, Power, Trash2, Users, Zap } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { SettingsPage, Section, ListRow, NoAccess } from "@/components/layout";
import { Button } from "@/components/ui/button";
import { SettingsEmpty } from "@/components/settings2/SettingsEmpty";
import { Menu, MenuContent, MenuItem, MenuSeparator, MenuTrigger } from "@/components/ui/menu";
import { Skeleton } from "@/components/ui/skeleton";
import { toast } from "@/components/ui/toast";
import { SummaryStrip } from "@/components/settings2/bits";
import { MotionList, MotionRow } from "@/components/settings2/motion";
import { connectionLabel, MailboxIcon, MailboxStatus, notReceiving, notSending } from "@/components/mailboxes/kinds";
import { quietFor } from "@/components/mailboxes/quiet";
import { useMailboxActions } from "@/components/mailboxes/useMailboxActions";
import type { Mailbox } from "@/lib/api/types";
import { relLong } from "@/lib/format";
import { q } from "@/lib/queries";
import { atLeast, useRole } from "@/lib/session";
import { cn } from "@/lib/cn";
import { tone } from "@/lib/tones";
import { PlanLimitHint, showLimit } from "@/components/billing/PlanLimit";
import { useLimit } from "@/components/billing/hooks";

/** A mailbox that no longer receives or sends needs attention whatever its status says. */
const needsAttention = (m: Mailbox) => m.status === "error" || notReceiving(m) || notSending(m);

/** "Add mailbox", or the upgrade prompt when Free's one mailbox is taken. */
function AddMailboxButton({ children, size }: { children: React.ReactNode; size?: "sm" | "md" }) {
  const limit = useLimit("mailboxes");
  if (limit.atLimit)
    return (
      <Button variant="primary" size={size} onClick={() => showLimit("mailboxes", limit.max)}>
        <Plus className="size-3.5" />
        {children}
      </Button>
    );
  return (
    <Button variant="primary" size={size} asChild>
      <Link to="/settings/mailboxes/new">
        <Plus className="size-3.5" />
        {children}
      </Link>
    </Button>
  );
}

const oauthErrorKeys = ["access_denied", "invalid_state", "provider_error", "provider_unavailable"] as const;
type OauthError = (typeof oauthErrorKeys)[number];

// Fixed column widths so rows and the header line up.
const COLS = {
  kind: "hidden w-52 lg:block",
  team: "hidden w-24 xl:block",
  status: "hidden w-[120px] sm:block",
  synced: "hidden w-24 text-right md:block",
};

export default function Mailboxes() {
  const { t } = useTranslation("settingsChannels");
  const role = useRole();
  const navigate = useNavigate();
  const search = useSearch({ from: "/app/settings/mailboxes" });
  const { data, isLoading } = useQuery({ ...q.mailboxes(), enabled: atLeast(role, "admin") });
  const { data: teams } = useQuery({ ...q.teams(), enabled: atLeast(role, "admin") });
  const { setStatus, test, remove, testDialog } = useMailboxActions();
  const [highlight, setHighlight] = useState<string | null>(null);
  const handled = useRef(false);

  useEffect(() => {
    if (handled.current || (!search.connected && !search.error)) return;
    if (search.connected && isLoading) return;
    handled.current = true;
    const connected = search.connected ? data?.find((m) => m.id === search.connected) : undefined;
    if (connected?.inbound_kind === "forwarding") {
      // Connected to send only: its mail still has to be forwarded, which its page explains.
      toast.success(t("list.connectedForward"));
      void navigate({ to: "/settings/mailboxes/$mailboxId", params: { mailboxId: connected.id }, replace: true });
      return;
    }
    if (search.connected) {
      toast.success(t("list.connected"));
      setHighlight(search.connected);
      setTimeout(() => setHighlight(null), 2400);
    } else if (search.error)
      toast.error((oauthErrorKeys as readonly string[]).includes(search.error) ? t(`oauthErrors.${search.error as OauthError}`) : t("list.connectFailed"));
    void navigate({ to: "/settings/mailboxes", search: {}, replace: true });
  }, [search.connected, search.error, navigate, t, isLoading, data]);

  const sorted = useMemo(() => {
    const rank = (m: Mailbox) => (needsAttention(m) ? 0 : m.status === "active" ? 1 : 2);
    return [...(data ?? [])].sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name));
  }, [data]);
  const teamName = (id: string | null) => (id ? teams?.find((x) => x.id === id)?.name : undefined);

  if (!atLeast(role, "admin"))
    return (
      <SettingsPage title={t("list.title")}>
        <NoAccess what="mailboxes" />
      </SettingsPage>
    );

  const count = (s: Mailbox["status"]) =>
    sorted.filter((m) => (s === "error" ? needsAttention(m) : s === "active" ? m.status === "active" && !needsAttention(m) : m.status === s)).length;

  return (
    <SettingsPage title={t("list.title")} description={t("list.description")} actions={<AddMailboxButton>{t("list.add")}</AddMailboxButton>}>
      <PlanLimitHint kind="mailboxes" className="-mt-3" />
      {isLoading ? (
        <>
          <div className="grid grid-cols-2 gap-px overflow-hidden rounded-lg border border-line md:grid-cols-4" aria-hidden>
            {[0, 1, 2, 3].map((i) => (
              <div key={i} className="bg-surface px-4 py-3">
                <Skeleton className="h-2 w-16" />
                <Skeleton className="mt-2.5 h-4 w-8" />
              </div>
            ))}
          </div>
          <Section flush>
            <MailboxSkeleton />
          </Section>
        </>
      ) : !sorted.length ? (
        <Section flush>
          <SettingsEmpty
            art="inbox"
            title={t("list.emptyTitle")}
            description={t("list.emptyBody")}
            action={<AddMailboxButton>{t("list.addFirst")}</AddMailboxButton>}
            tips={[
              { label: t("list.emptyTips.forward"), icon: <Forward /> },
              { label: t("list.emptyTips.team"), icon: <Users /> },
            ]}
          />
        </Section>
      ) : (
        <>
          <SummaryStrip
            items={[
              { label: t("summary.total"), value: sorted.length },
              { label: t("summary.active"), value: count("active"), tone: "green", hint: count("error") === 0 && count("disabled") === 0 ? t("summary.allGood") : undefined },
              { label: t("summary.attention"), value: count("error"), tone: count("error") ? "red" : undefined, hint: count("error") ? t("summary.checkThese") : undefined },
              { label: t("summary.disabled"), value: count("disabled"), tone: count("disabled") ? "gray" : undefined, hint: count("disabled") ? t("summary.paused") : undefined },
            ]}
          />
          <Section flush title={t("list.count", { count: sorted.length })}>
            <div className="flex h-8 items-center gap-3 border-b border-line-soft bg-sunken px-4 text-[11px] text-fg-4" aria-hidden>
              <span className="w-8 shrink-0" />
              <span className="flex-1">{t("list.colAddress")}</span>
              <span className={COLS.kind}>{t("list.colConnection")}</span>
              <span className={COLS.team}>{t("list.colTeam")}</span>
              <span className={COLS.status}>{t("list.colStatus")}</span>
              <span className={COLS.synced}>{t("list.colSynced")}</span>
              <span className="w-7 shrink-0" />
            </div>
            <MotionList>
              {sorted.map((m) => (
                <MotionRow key={m.id}>
                  <ListRow
                    onClick={() => void navigate({ to: "/settings/mailboxes/$mailboxId", params: { mailboxId: m.id } })}
                    className={cn("transition-colors duration-700", highlight === m.id && "bg-hover")}
                  >
                    <MailboxIcon kind={m.inbound_kind} />
                    <div className="min-w-0 flex-1">
                      <div className="truncate text-[13px] font-medium">{m.name}</div>
                      <div className="mono truncate text-xs text-fg-3">{m.email}</div>
                    </div>
                    <span className={cn(COLS.kind, "truncate text-xs text-fg-2")}>{connectionLabel(m, t)}</span>
                    <span className={cn(COLS.team, "truncate text-xs", teamName(m.team_id) ? "text-fg-2" : "text-fg-4")}>{teamName(m.team_id) ?? t("list.noTeam")}</span>
                    <div className={COLS.status}>
                      <MailboxStatus mailbox={m} />
                    </div>
                    <LastMail mailbox={m} />
                    <Menu>
                      <MenuTrigger asChild>
                        <Button size="sm" variant="ghost" square aria-label={t("list.actionsFor", { email: m.email })} onClick={(e) => e.stopPropagation()}>
                          <MoreHorizontal className="size-4" />
                        </Button>
                      </MenuTrigger>
                      <MenuContent>
                        <MenuItem icon={<Zap />} onSelect={() => void test(m)}>
                          {t("list.test")}
                        </MenuItem>
                        <MenuItem icon={<Power />} onSelect={() => setStatus.mutate({ m, status: m.status === "disabled" ? "active" : "disabled" })}>
                          {m.status === "disabled" ? t("list.enable") : t("list.disable")}
                        </MenuItem>
                        <MenuSeparator />
                        <MenuItem danger icon={<Trash2 />} onSelect={() => remove(m)}>
                          {t("list.remove")}
                        </MenuItem>
                      </MenuContent>
                    </Menu>
                  </ListRow>
                </MotionRow>
              ))}
            </MotionList>
          </Section>
        </>
      )}
      {testDialog}
    </SettingsPage>
  );
}

function MailboxSkeleton() {
  return (
    <div aria-busy="true" aria-label="Loading">
      {[0, 1, 2].map((i) => (
        <div key={i} className="flex min-h-12 items-center gap-3 border-b border-line-soft px-4 py-2.5 last:border-0">
          <Skeleton className="size-8 rounded-md" />
          <div className="flex flex-1 flex-col gap-1.5">
            <Skeleton className="h-2.5" style={{ width: `${18 + i * 6}%` }} />
            <Skeleton className="h-2 opacity-60" style={{ width: `${26 + i * 5}%` }} />
          </div>
          <Skeleton className="hidden h-5 w-20 rounded-sm sm:block" />
          <Skeleton className="hidden h-2 w-14 md:block" />
        </div>
      ))}
    </div>
  );
}

/** When mail last came in, amber once the address has gone quiet. */
function LastMail({ mailbox: m }: { mailbox: Mailbox }) {
  const { t } = useTranslation("settingsChannels");
  const quiet = quietFor(m);
  return (
    <span className={cn(COLS.synced, "mono whitespace-nowrap text-[11px]", quiet ? tone.amber.text : "text-fg-4")} title={quiet && !quiet.never ? t("list.quiet", { count: quiet.days }) : undefined}>
      {m.last_received_at ? relLong(m.last_received_at) : t("list.noMail")}
    </span>
  );
}
