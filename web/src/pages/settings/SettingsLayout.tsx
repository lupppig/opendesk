import { atLeast, useRole } from "@/lib/session";
import { useQuery } from "@tanstack/react-query";
import { q } from "@/lib/queries";
import { Link, Outlet, useRouterState } from "@tanstack/react-router";
import { useNavigate } from "@tanstack/react-router";
import { Select } from "@/components/ui/select";
import { cn } from "@/lib/cn";
import { motion } from "motion/react";
import {
  AtSign,
  Bell,
  BookOpen,
  Bot,
  ClipboardCheck,
  FlaskConical,
  LayoutDashboard,
  Building2,
  Cable,
  CreditCard,
  Gauge,
  Import as ImportIcon,
  Inbox,
  KeyRound,
  Layers,
  ListChecks,
  MessageCircle,
  MessageSquareShare,
  MessageSquareText,
  Palette,
  Plug,
  Route,
  Shield,
  SlidersHorizontal,
  Shuffle,
  Sparkles,
  Clock,
  Tag,
  Wrench,
  User,
  Users,
  UsersRound,
  Webhook,
  Workflow,
  Smile,
} from "lucide-react";
import { Fragment, type ReactNode } from "react";
import { BrandMark } from "@/components/settings2/teams";
import { useCollab } from "@/lib/collab/hooks";

type NavPath =
  | "/settings/profile"
  | "/settings/security"
  | "/settings/notifications"
  | "/settings/workspace"
  | "/settings/members"
  | "/settings/teams"
  | "/settings/mailboxes"
  | "/settings/support-channels"
  | "/settings/mail-accounts"
  | "/settings/brands"
  | "/settings/email-design"
  | "/settings/routing"
  | "/settings/tags"
  | "/settings/custom-fields"
  | "/settings/assignment"
  | "/settings/triage"
  | "/settings/business-hours"
  | "/settings/saved-replies"
  | "/settings/views"
  | "/settings/sla"
  | "/settings/automations"
  | "/settings/api-keys"
  | "/settings/ai-clients"
  | "/settings/webhooks"
  | "/settings/audit-log"
  | "/settings/billing"
  | "/settings/ai"
  | "/settings/ai/agent"
  | "/settings/ai/behaviour"
  | "/settings/ai/bots"
  | "/settings/ai/knowledge"
  | "/settings/ai/tools"
  | "/settings/ai/playground"
  | "/settings/ai/tests"
  | "/settings/integrations"
  | "/settings/import";

const AGENT_GROUP = "AI agent";

const groups: { title: string; items: { to: NavPath; label: string; icon: ReactNode }[] }[] = [
  {
    title: "Account",
    items: [
      { to: "/settings/profile", label: "Profile", icon: <User /> },
      { to: "/settings/security", label: "Security", icon: <Shield /> },
      { to: "/settings/notifications", label: "Notifications", icon: <Bell /> },
    ],
  },
  {
    title: "Workspace",
    items: [
      { to: "/settings/workspace", label: "General", icon: <Building2 /> },
      { to: "/settings/members", label: "Members", icon: <Users /> },
      { to: "/settings/teams", label: "Teams", icon: <UsersRound /> },
      { to: "/settings/billing", label: "Plan and billing", icon: <CreditCard /> },
      { to: "/settings/ai", label: "AI", icon: <Bot /> },
      { to: "/settings/integrations", label: "Integrations", icon: <Plug /> },
      { to: "/settings/import", label: "Import", icon: <ImportIcon /> },
    ],
  },
  {
    title: "Inbox",
    items: [
      { to: "/settings/tags", label: "Labels", icon: <Tag /> },
      { to: "/settings/custom-fields", label: "Custom fields", icon: <SlidersHorizontal /> },
      { to: "/settings/assignment", label: "Assignment", icon: <Shuffle /> },
      { to: "/settings/triage", label: "Triage", icon: <Sparkles /> },
      { to: "/settings/business-hours", label: "Business hours", icon: <Clock /> },
      { to: "/settings/saved-replies", label: "Saved replies", icon: <MessageSquareText /> },
      { to: "/settings/views", label: "Views", icon: <Layers /> },
      { to: "/settings/sla", label: "SLA policies", icon: <Gauge /> },
      { to: "/settings/automations", label: "Automations", icon: <Workflow /> },
    ],
  },
  {
    title: "Channels",
    items: [
      { to: "/settings/mailboxes", label: "Mailboxes", icon: <Inbox /> },
      { to: "/settings/support-channels", label: "Support channels", icon: <MessageSquareShare /> },
      { to: "/settings/mail-accounts", label: "Mail accounts", icon: <AtSign /> },
      { to: "/settings/routing", label: "Routing", icon: <Route /> },
      { to: "/settings/brands", label: "Brands and widgets", icon: <MessageCircle /> },
      { to: "/settings/email-design", label: "Email design", icon: <Palette /> },
    ],
  },
  {
    title: AGENT_GROUP,
    items: [
      { to: "/settings/ai/agent", label: "Overview", icon: <LayoutDashboard /> },
      { to: "/settings/ai/bots", label: "Bots", icon: <Smile /> },
      { to: "/settings/ai/behaviour", label: "Behaviour", icon: <SlidersHorizontal /> },
      { to: "/settings/ai/knowledge", label: "Knowledge", icon: <BookOpen /> },
      { to: "/settings/ai/tools", label: "Tools", icon: <Wrench /> },
      { to: "/settings/ai/playground", label: "Playground", icon: <FlaskConical /> },
      { to: "/settings/ai/tests", label: "Tests", icon: <ClipboardCheck /> },
    ],
  },
  {
    title: "Developers",
    items: [
      { to: "/settings/api-keys", label: "API keys", icon: <KeyRound /> },
      { to: "/settings/ai-clients", label: "AI clients", icon: <Cable /> },
      { to: "/settings/webhooks", label: "Webhooks", icon: <Webhook /> },
    ],
  },
  { title: "Admin", items: [{ to: "/settings/audit-log", label: "Audit log", icon: <ListChecks /> }] },
];

/**
 * The AI page is listed when the instance has an AI provider. Admins always see
 * it, because without a provider it explains how to connect one. The AI agent
 * pages are for admins on an instance with AI.
 */
function useVisibleGroups() {
  const { data: ai, isFetched } = useQuery(q.aiConfig());
  const admin = atLeast(useRole(), "admin");
  const hideAi = isFetched && !ai?.enabled && !admin;
  const hideAgent = !admin || (isFetched && !ai?.enabled);
  // Mail accounts are Collab's, listed only while the workspace has it.
  const collab = useCollab();
  return groups
    .filter((g) => !(hideAgent && g.title === AGENT_GROUP))
    .map((g) => (hideAi ? { ...g, items: g.items.filter((i) => i.to !== "/settings/ai") } : g))
    .map((g) => (collab.enabled ? g : { ...g, items: g.items.filter((i) => i.to !== "/settings/mail-accounts") }));
}

/** With a second brand, each brand is listed under Brands (contracts/teams.md). */
function useBrandItems() {
  const admin = atLeast(useRole(), "admin");
  const { data } = useQuery({ ...q.brands(), enabled: admin, retry: false });
  if (!data || data.length < 2) return [];
  return [...data].sort((a, b) => Number(b.is_default) - Number(a.is_default) || a.position - b.position);
}

export default function SettingsLayout() {
  const groups = useVisibleGroups();
  const brands = useBrandItems();
  const all = groups.flatMap((g) => g.items);
  const path = useRouterState({ select: (s) => s.location.pathname });
  const navigate = useNavigate();
  const activeBrand = brands.find((b) => path === `/settings/brands/${b.id}` || path.startsWith(`/settings/brands/${b.id}/`))?.id;
  // The longest matching entry wins, so /settings/ai/tools is Tools, not AI.
  const current = all
    .filter((i) => path === i.to || path.startsWith(`${i.to}/`))
    .sort((a, b) => b.to.length - a.to.length)[0]?.to;

  return (
    <div className="flex min-h-0 flex-1 flex-col md:flex-row">
      <nav aria-label="Settings" className="hidden w-[232px] shrink-0 overflow-y-auto border-r border-line px-2.5 pb-6 pt-4 md:block">
        {groups.map((g, gi) => (
          <div key={g.title} className={gi ? "mt-4" : undefined}>
            <div className="label-caps px-2 pb-1">{g.title}</div>
            <div className="flex flex-col gap-px">
              {g.items.map((i) => {
                const active = current === i.to && !(i.to === "/settings/brands" && activeBrand);
                return (
                  <Fragment key={i.to}>
                  <Link
                    key={i.to}
                    to={i.to}
                    aria-current={active ? "page" : undefined}
                    className={cn(
                      "group relative flex h-7 items-center gap-2.5 rounded-md px-2 text-[12.5px] outline-none transition-colors focus-visible:shadow-[0_0_0_2px_var(--focus)] [&_svg]:relative [&_svg]:z-10 [&_svg]:size-[14px] [&_svg]:shrink-0",
                      active ? "font-medium text-fg [&_svg]:text-fg-2" : "text-fg-2 hover:bg-hover hover:text-fg [&_svg]:text-fg-4 hover:[&_svg]:text-fg-3",
                    )}
                  >
                    {active && (
                      <motion.span
                        layoutId="settings-active"
                        className="absolute inset-0 rounded-md bg-active"
                        transition={{ type: "spring", stiffness: 520, damping: 42 }}
                      />
                    )}
                    {i.icon}
                    <span className="relative z-10 truncate">{i.label}</span>
                  </Link>
                  {i.to === "/settings/brands" &&
                    brands.map((b) => {
                      const on = activeBrand === b.id;
                      return (
                        <Link
                          key={b.id}
                          to="/settings/brands/$brandId"
                          params={{ brandId: b.id }}
                          search={{}}
                          aria-current={on ? "page" : undefined}
                          className={cn(
                            "relative ml-[22px] flex h-7 items-center gap-2 rounded-md px-2 text-[12.5px] outline-none transition-colors focus-visible:shadow-[0_0_0_2px_var(--focus)]",
                            on ? "font-medium text-fg" : "text-fg-3 hover:bg-hover hover:text-fg",
                          )}
                        >
                          {on && (
                            <motion.span
                              layoutId="settings-active"
                              className="absolute inset-0 rounded-md bg-active"
                              transition={{ type: "spring", stiffness: 520, damping: 42 }}
                            />
                          )}
                          <BrandMark brand={b} size="xs" className="relative z-10" />
                          <span className="relative z-10 truncate">{b.name}</span>
                        </Link>
                      );
                    })}
                  </Fragment>
                );
              })}
            </div>
          </div>
        ))}
      </nav>

      <div className="border-b border-line px-4 py-2 md:hidden">
        <Select
          aria-label="Settings page"
          value={current}
          onChange={(v) => void navigate({ to: v })}
          options={all.map((i) => ({ value: i.to, label: i.label }))}
        />
      </div>

      <div className="min-h-0 min-w-0 flex-1 overflow-y-auto">
        <div key={current ?? path} className="flex min-h-full flex-col animate-rise-in">
          <Outlet />
        </div>
      </div>
    </div>
  );
}
