import { useInfiniteQuery } from "@tanstack/react-query";
import {
  Building2,
  ChevronRight,
  CreditCard,
  Inbox,
  KeyRound,
  Lock,
  LogIn,
  LogOut,
  MessageCircle,
  MonitorSmartphone,
  Settings2,
  ShieldCheck,
  ShieldAlert,
  Sparkles,
  UserPlus,
  Users,
  UsersRound,
  Webhook,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { NoAccess, SettingsPage } from "@/components/layout";
import { SearchInput } from "@/components/ui/input";
import { IconTile } from "@/components/settings2/bits";
import type { Tone } from "@/lib/tones";
import { EmptyState } from "@/components/ui/empty";
import { SettingsEmpty } from "@/components/settings2/SettingsEmpty";
import { Segmented } from "@/components/ui/tabs";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { Tooltip } from "@/components/ui/tooltip";
import type { AuditLog as Entry } from "@/lib/api/types";
import { clock, dayLabel, fullDate } from "@/lib/format";
import { q } from "@/lib/queries";
import { atLeast, useRole } from "@/lib/session";
import { cn } from "@/lib/cn";

type Category = "all" | "access" | "security" | "people" | "integrations" | "workspace";

/** "auth.login_failed" and "login.failed" read the same. */
function norm(action: string) {
  return action.startsWith("auth.") ? action.slice(5).replace("_", ".") : action;
}

const categoryOf = (action: string): Exclude<Category, "all"> => {
  const head = norm(action).split(".")[0];
  if (head === "login" || head === "logout" || head === "session" || head === "oauth") return "access";
  if (head === "totp" || head === "password" || head === "mfa" || head === "recovery_codes" || head === "account") return "security";
  if (head === "member" || head === "invitation" || head === "team") return "people";
  if (head === "api_key" || head === "webhook" || head === "mailbox" || head === "widget") return "integrations";
  return "workspace";
};

/** An icon and tone for an entry, by what it touched: one glance tells sign-ins from key changes. */
function glyph(action: string): { icon: ReactNode; tone: Tone } {
  const a = norm(action);
  const head = a.split(".")[0];
  if (a === "login.failed" || a === "session.refresh_reused") return { icon: <ShieldAlert />, tone: "red" };
  if (head === "login" || head === "oauth" || head === "identity") return { icon: <LogIn />, tone: "blue" };
  if (head === "logout") return { icon: <LogOut />, tone: "gray" };
  if (head === "session") return { icon: <MonitorSmartphone />, tone: "blue" };
  if (head === "password" || head === "account") return { icon: <Lock />, tone: "amber" };
  if (head === "totp" || head === "2fa" || head === "mfa" || head === "recovery_codes" || head === "passkey") return { icon: <ShieldCheck />, tone: "amber" };
  if (head === "invitation" || a === "member.invited") return { icon: <UserPlus />, tone: "teal" };
  if (head === "member") return { icon: <Users />, tone: "teal" };
  if (head === "team") return { icon: <UsersRound />, tone: "teal" };
  if (head === "api_key") return { icon: <KeyRound />, tone: "violet" };
  if (head === "webhook") return { icon: <Webhook />, tone: "violet" };
  if (head === "mailbox" || head === "collab") return { icon: <Inbox />, tone: "indigo" };
  if (head === "widget" || head === "widget_secret" || a === "workspace.widget_secret_rotated") return { icon: <MessageCircle />, tone: "cyan" };
  if (head === "billing") return { icon: <CreditCard />, tone: "green" };
  if (head === "ai") return { icon: <Sparkles />, tone: "violet" };
  if (head === "workspace") return { icon: <Building2 />, tone: "gray" };
  return { icon: <Settings2 />, tone: "gray" };
}

const verbs: Record<string, string> = {
  "login.succeeded": "signed in",
  login: "signed in",
  "login.failed": "failed to sign in",
  "member.invited": "invited",
  logout: "signed out",
  "session.revoked": "revoked a session",
  "password.changed": "changed their password",
  "password.reset": "reset their password",
  "totp.enabled": "turned on two-factor authentication",
  "totp.disabled": "turned off two-factor authentication",
  "recovery_codes.used": "used a recovery code",
  "member.added": "added a member",
  "member.removed": "removed a member",
  "member.role_changed": "changed a member's role",
  "invitation.created": "invited someone",
  "invitation.revoked": "revoked an invitation",
  "invitation.accepted": "accepted an invitation",
  "api_key.created": "created an API key",
  "api_key.revoked": "revoked an API key",
  "api_key.deleted": "revoked an API key",
  "webhook.created": "created a webhook",
  "webhook.updated": "updated a webhook",
  "webhook.deleted": "deleted a webhook",
  "mailbox.connected": "connected a mailbox",
  "mailbox.created": "added a mailbox",
  "mailbox.disconnected": "disconnected a mailbox",
  "mailbox.deleted": "removed a mailbox",
  "workspace.updated": "updated workspace settings",
  "workspace.transferred": "transferred ownership",
  "widget.secret_rotated": "rotated the widget secret",
  "widget_secret.rotated": "rotated the widget secret",
  "workspace.widget_secret_rotated": "rotated the widget secret",
  "workspace.created": "created the workspace",
  "2fa.enabled": "turned on two-factor authentication",
  "2fa.disabled": "turned off two-factor authentication",
  "member.joined": "joined the workspace",
  "member.left": "left the workspace",
  "member.account_deleted": "deleted their account",
  "identity.linked": "linked a sign-in provider",
  "session.refresh_reused": "had a reused session token refused",
  "api_key.updated": "renamed an API key",
  "mailbox.updated": "updated a mailbox",
  "team.created": "created a team",
  "team.updated": "updated a team",
  "team.deleted": "deleted a team",
  "view.created": "created a view",
  "view.updated": "updated a view",
  "view.deleted": "deleted a view",
  "widget.settings_updated": "updated the chat widget",
  "ai.settings_updated": "updated AI settings",
  "billing.updated": "changed the subscription",
  "billing.ai_top_up_purchased": "added AI usage",
  "billing.ai_recharged": "auto-recharged the AI balance",
  "billing.auto_recharge_updated": "changed auto-recharge",
  "billing.auto_recharge_failed": "had an auto-recharge fail",
  // Older entries, from before AI usage was counted in US dollars.
  "billing.credits_purchased": "added AI usage",
  "billing.credits_recharged": "auto-recharged the AI balance",
  "billing.payment_failed": "had a payment fail",
  "billing.seats_synced": "synced billed seats",
  "billing.plan_changed": "changed the plan",
  "workspace.collab_changed": "changed Collab access",
  "sidebar.starter_updated": "published the starter Space",
  "sidebar.starter_removed": "removed the starter Space",
  "collab.account_connected": "connected a mail account",
  "collab.account_updated": "updated a mail account",
  "collab.account_disconnected": "disconnected a mail account",
  "collab.thread_shared": "shared a conversation",
  "collab.thread_assigned": "assigned a conversation",
  "support_channel.connected": "connected a support channel",
  "support_channel.disconnected": "disconnected a support channel",
  "support_channel.destination_created": "added a support channel destination",
  "support_channel.destination_deleted": "removed a support channel destination",
  "support_channel.identity_linked": "linked a chat account",
  "support_channel.identity_unlinked": "unlinked a chat account",
};

function str(v: unknown): string | null {
  return typeof v === "string" && v ? v : null;
}

const genericVerbs = new Set(["created", "updated", "deleted", "removed", "added", "revoked", "connected", "disconnected", "enabled", "disabled", "rotated", "changed", "published", "invited"]);

function generic(e: Entry): string | null {
  const a = norm(e.action);
  const i = a.lastIndexOf(".");
  const verb = i < 0 ? "" : a.slice(i + 1);
  if (!genericVerbs.has(verb)) return null;
  const thing = (i < 0 ? a : a.slice(0, i)).replace(/_/g, " ");
  const article = /^[aeiou]/.test(thing) || thing.startsWith("sla") ? "an" : "a";
  return `${verb} ${article} ${thing === "sla policy" ? "SLA policy" : thing === "api key" ? "API key" : thing}`;
}

/** A plain sentence for an entry, with the object named when the data has it. */
function describe(e: Entry): ReactNode {
  const verb = verbs[norm(e.action)] ?? verbs[e.action] ?? generic(e);
  const d = e.data;
  const object = str(d.email) ?? str(d.name) ?? str(d.url) ?? str(d.title);
  const role = norm(e.action) === "member.role_changed" && str(d.to) ? ` to ${str(d.to)}` : "";
  if (!verb)
    return (
      <>
        <span className="mono text-[11.5px] text-fg-3">{e.action}</span>
        {object && <span className="font-medium text-fg"> {object}</span>}
      </>
    );
  return (
    <>
      {verb}
      {object && <span className="font-medium text-fg"> {object}</span>}
      {role}
    </>
  );
}

/** Who did it. The row's tile already says what kind of change it was, so the actor stays text. */
function Actor({ e }: { e: Entry }) {
  const { t: ui } = useTranslation("ui");
  if (e.actor.type === "user" && e.actor.user) return <span className="truncate font-medium text-fg">{e.actor.user.name || e.actor.user.email}</span>;
  if (e.actor.type === "api_key")
    return (
      <span className="flex items-center gap-1.5">
        <span className="font-medium text-fg">{ui("apiKey")}</span>
        {e.actor.api_key_id && <span className="mono text-[11px] text-fg-4">{e.actor.api_key_id.slice(0, 8)}</span>}
      </span>
    );
  return <span className="font-medium text-fg">{e.actor.type === "user" ? ui("someone") : ui("system")}</span>;
}

function formatValue(v: unknown): string {
  if (v === null || v === undefined) return "–";
  if (typeof v === "string") return v;
  if (typeof v === "number" || typeof v === "boolean") return String(v);
  return JSON.stringify(v);
}

function Row({ e }: { e: Entry }) {
  const { t } = useTranslation("settingsDev");
  const [open, setOpen] = useState(false);
  const failed = norm(e.action) === "login.failed";
  const keys = Object.keys(e.data ?? {});
  const g = glyph(e.action);
  return (
    <div className="border-b border-line-soft last:border-0">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        className={cn("group flex min-h-11 w-full items-center gap-3 px-4 py-2 text-left text-[12.5px] text-fg-2 outline-none transition-colors hover:bg-sunken focus-visible:bg-sunken", open && "bg-sunken")}
      >
        <IconTile size="sm" tone={g.tone}>
          {g.icon}
        </IconTile>
        <span className="flex min-w-0 flex-1 flex-wrap items-center gap-x-1.5 gap-y-0.5">
          <Actor e={e} />
          <span className="min-w-0">{describe(e)}</span>
          {failed && <span className="ml-1 inline-flex h-[18px] items-center rounded-sm bg-tone-red-soft px-1.5 text-[10.5px] font-medium text-tone-red">{t("audit.failed")}</span>}
        </span>
        <span className="mono hidden w-32 shrink-0 truncate text-right text-[11px] text-fg-4 md:block">{e.ip}</span>
        <Tooltip content={fullDate(e.created_at)}>
          <span className="mono w-[68px] shrink-0 whitespace-nowrap text-right text-[11.5px] tabular-nums text-fg-3">{clock(e.created_at)}</span>
        </Tooltip>
        <ChevronRight className={cn("size-3.5 shrink-0 text-fg-4 opacity-0 transition-[transform,opacity] duration-150 group-hover:opacity-100 motion-reduce:transition-none", open && "rotate-90 opacity-100")} />
      </button>
      {open && (
        <div className="mx-4 mb-3 ml-[52px] grid animate-fade-in grid-cols-[140px_1fr] gap-x-4 gap-y-1.5 rounded-md border border-line-soft bg-sunken px-3 py-2.5 text-[11.5px] motion-reduce:animate-none">
          <span className="text-fg-3">{t("audit.detail.action")}</span>
          <span className="mono text-fg">{e.action}</span>
          <span className="text-fg-3">{t("audit.detail.entity")}</span>
          <span className="mono text-fg">
            {e.entity_type}
            {e.entity_id ? ` ${e.entity_id}` : ""}
          </span>
          <span className="text-fg-3">{t("audit.detail.ip")}</span>
          <span className="mono text-fg">{e.ip || "–"}</span>
          {keys.map((k) => (
            <div key={k} className="contents">
              <span className="mono text-fg-3">{k}</span>
              <span className="mono break-all text-fg">{formatValue(e.data[k])}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

/** Words to match a filter against: who, what, and on what. */
function haystack(e: Entry): string {
  const d = e.data ?? {};
  return [e.action, e.actor.user?.name, e.actor.user?.email, e.ip, str(d.email), str(d.name), str(d.url), str(d.title), verbs[norm(e.action)]]
    .filter(Boolean)
    .join(" ")
    .toLowerCase();
}

export default function AuditLog() {
  const { t } = useTranslation("settingsDev");
  const role = useRole();
  const admin = atLeast(role, "admin");
  const list = useInfiniteQuery({ ...q.auditLogs(), enabled: admin });
  const [cat, setCat] = useState<Category>("all");
  const [term, setTerm] = useState("");
  const sentinel = useRef<HTMLDivElement>(null);

  const entries = useMemo(() => (list.data?.pages ?? []).flatMap((p) => p.data), [list.data]);
  const shown = useMemo(() => {
    const needle = term.trim().toLowerCase();
    return entries.filter((e) => (cat === "all" || categoryOf(e.action) === cat) && (!needle || haystack(e).includes(needle)));
  }, [entries, cat, term]);
  const days = useMemo(() => {
    const groups: { day: string; label: string; rows: Entry[] }[] = [];
    for (const e of shown) {
      const day = new Date(e.created_at).toDateString();
      const last = groups[groups.length - 1];
      if (last?.day === day) last.rows.push(e);
      else groups.push({ day, label: dayLabel(e.created_at), rows: [e] });
    }
    return groups;
  }, [shown]);

  const { hasNextPage, isFetchingNextPage, fetchNextPage } = list;
  useEffect(() => {
    const el = sentinel.current;
    if (!el || !hasNextPage) return;
    const io = new IntersectionObserver((es) => {
      if (es.some((x) => x.isIntersecting) && !isFetchingNextPage) void fetchNextPage();
    }, { rootMargin: "300px" });
    io.observe(el);
    return () => io.disconnect();
  }, [hasNextPage, isFetchingNextPage, fetchNextPage]);

  if (!admin)
    return (
      <SettingsPage title={t("audit.title")}>
        <NoAccess what="the audit log" />
      </SettingsPage>
    );

  const filtered = cat !== "all" || !!term.trim();
  return (
    <SettingsPage wide title={t("audit.title")} description={t("audit.description")}>
      <div className="-mb-3 flex flex-wrap items-center gap-2">
        <div className="min-w-0 overflow-x-auto">
          <Segmented
            value={cat}
            onChange={setCat}
            options={[
              { value: "all", label: t("audit.cat.all") },
              { value: "access", label: t("audit.cat.access") },
              { value: "security", label: t("audit.cat.security") },
              { value: "people", label: t("audit.cat.people") },
              { value: "integrations", label: t("audit.cat.integrations") },
              { value: "workspace", label: t("audit.cat.workspace") },
            ]}
          />
        </div>
        <SearchInput value={term} onChange={setTerm} placeholder={t("audit.search")} className="ml-auto w-full sm:w-[240px]" />
      </div>
      {list.isLoading ? (
        <div className="overflow-hidden rounded-lg border border-line bg-surface shadow-xs">
          {Array.from({ length: 8 }).map((_, i) => (
            <div key={i} className="flex h-11 items-center gap-3 border-b border-line-soft px-4 last:border-0">
              <div className="skeleton size-6 rounded-md" />
              <div className="skeleton h-2.5" style={{ width: `${30 + ((i * 17) % 35)}%` }} />
              <div className="skeleton ml-auto h-2.5 w-12" />
            </div>
          ))}
        </div>
      ) : !shown.length ? (
        <div className="overflow-hidden rounded-lg border border-line bg-surface shadow-xs">
          {filtered ? (
            <EmptyState
              compact
              lit={2}
              className="py-12"
              title={t("audit.emptyFiltered")}
              description={hasNextPage ? t("audit.emptyFilteredMore") : t("audit.emptyFilteredHint")}
              action={
                <Button
                  size="sm"
                  onClick={() => {
                    setCat("all");
                    setTerm("");
                  }}
                >
                  {t("audit.clear")}
                </Button>
              }
            />
          ) : (
            <SettingsEmpty
              art="search"
              title={t("audit.empty")}
              description={t("audit.emptyHint")}
              tips={[
                { label: t("audit.emptyTips.security"), icon: <ShieldCheck /> },
                { label: t("audit.emptyTips.ip"), icon: <MonitorSmartphone /> },
              ]}
            />
          )}
        </div>
      ) : (
        <div className="flex flex-col gap-6">
          {days.map((g) => (
            <section key={g.day} className="animate-rise-in motion-reduce:animate-none">
              <div className="mb-2 flex items-baseline gap-2 px-1">
                <h2 className="label-caps">{g.label}</h2>
                <span className="mono text-[10.5px] tabular-nums text-fg-4">{g.rows.length}</span>
              </div>
              <div className="overflow-hidden rounded-lg border border-line bg-surface shadow-xs">
                {g.rows.map((e) => (
                  <Row key={e.id} e={e} />
                ))}
              </div>
            </section>
          ))}
        </div>
      )}
      <div ref={sentinel} className="flex h-10 items-center justify-center text-[12px] text-fg-4">
        {isFetchingNextPage ? <Spinner className="size-3.5" /> : !hasNextPage && entries.length > 0 ? t("audit.end") : null}
      </div>
    </SettingsPage>
  );
}
