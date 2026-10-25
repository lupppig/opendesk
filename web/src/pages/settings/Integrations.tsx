import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { continueInBrowser } from "@/components/desktop/InBrowser";
import { isDesktop } from "@/lib/desktop";
import { ChevronDown, MoreHorizontal, Plus, RefreshCw, Trash2, LogIn } from "lucide-react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Section, SettingsPage } from "@/components/layout";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { Segmented } from "@/components/ui/tabs";
import { Menu, MenuContent, MenuItem, MenuSeparator, MenuTrigger } from "@/components/ui/menu";
import { useConfirm } from "@/components/ui/confirm";
import { toast } from "@/components/ui/toast";
import { ListSkeleton, StatePill } from "@/components/settings2/bits";
import { MotionList, MotionRow } from "@/components/settings2/motion";
import { ConnectDialog, ProviderMark, ToolList } from "@/components/settings2/integrations";
import { api } from "@/lib/api/endpoints";
import type { Integration, IntegrationCategory, IntegrationProvider } from "@/lib/api/types";
import { relShort } from "@/lib/format";
import { qk, q, removeFromList, upsertInList } from "@/lib/queries";
import { atLeast, useRole } from "@/lib/session";
import { cn } from "@/lib/cn";

const oauthErrors = ["oauth_state", "access_denied", "forbidden", "not_found", "provider_error", "sync_failed"] as const;
type OAuthError = (typeof oauthErrors)[number];
type Filter = "all" | IntegrationCategory;
const filters: Filter[] = ["all", "issues", "docs", "chat", "crm", "payments", "files", "automation"];

function stateOf(x: Integration) {
  if (!x.enabled) return { state: "paused" as const, key: "off" as const };
  if (x.status === "connected") return { state: "active" as const, key: "connected" as const };
  if (x.status === "pending") return { state: "pending" as const, key: "pending" as const };
  return { state: "failing" as const, key: "error" as const };
}

function ConnectedRow({ x, admin }: { x: Integration; admin: boolean }) {
  const { t } = useTranslation("settingsDev");
  const qc = useQueryClient();
  const confirm = useConfirm();
  const reduce = useReducedMotion();
  const [open, setOpen] = useState(false);
  const st = stateOf(x);
  const asks = x.tools.filter((tl) => !tl.read_only && !tl.auto_run && tl.enabled).length;
  const on = x.tools.filter((tl) => tl.enabled).length;

  const update = useMutation({
    mutationFn: (b: { enabled?: boolean; disabled_tools?: string[]; ask_tools?: string[]; auto_tools?: string[] }) => api.integrations.update(x.id, b),
    onMutate: (b) => {
      const prev = qc.getQueryData<Integration[]>(qk.integrations);
      const tools = x.tools.map((tl) => {
        let next = b.disabled_tools ? { ...tl, enabled: !b.disabled_tools.includes(tl.name) } : tl;
        if (b.ask_tools) {
          const ask = b.ask_tools.includes(tl.name);
          next = { ...next, ask_first: ask, read_only: tl.read_only_hint && !ask, auto_run: next.auto_run && !ask };
        }
        // Only a write runs without asking, and asking first wins.
        if (b.auto_tools) next = { ...next, auto_run: b.auto_tools.includes(tl.name) && !next.read_only && !next.ask_first };
        return next;
      });
      upsertInList(qc, qk.integrations, { ...x, enabled: b.enabled ?? x.enabled, tools });
      return { prev };
    },
    onError: (e, _b, c) => {
      if (c?.prev) qc.setQueryData(qk.integrations, c.prev);
      toast.error(e, t("integrations.updateError"));
    },
    onSuccess: (row) => upsertInList(qc, qk.integrations, row),
  });
  const sync = useMutation({
    mutationFn: () => api.integrations.sync(x.id),
    onSuccess: (row) => {
      upsertInList(qc, qk.integrations, row);
      toast.success(t("integrations.synced", { count: row.tools.length }));
    },
    onError: (e) => toast.error(e, t("integrations.syncError")),
  });
  const signIn = useMutation({
    mutationFn: async () => (isDesktop ? null : api.integrations.authorize(x.id)),
    // The desktop app signs in to the service from the browser (contracts/native.md).
    onSuccess: (r) => (r ? window.location.assign(r.authorize_url) : continueInBrowser("/settings/integrations")),
    onError: (e) => toast.error(e),
  });
  const remove = useMutation({
    mutationFn: () => api.integrations.remove(x.id),
    onSuccess: () => {
      removeFromList(qc, qk.integrations, x.id);
      toast.success(t("integrations.disconnected", { name: x.name }));
    },
    onError: (e) => toast.error(e),
  });
  const toggleTool = (name: string, enabled: boolean) => {
    const off = x.tools.filter((tl) => (tl.name === name ? !enabled : !tl.enabled)).map((tl) => tl.name);
    update.mutate({ disabled_tools: off });
  };
  const toggleAsk = (name: string, ask: boolean) => {
    const list = x.tools.filter((tl) => (tl.name === name ? ask : tl.ask_first)).map((tl) => tl.name);
    update.mutate({ ask_tools: list });
  };
  const toggleAuto = (name: string, on: boolean) => {
    const list = x.tools.filter((tl) => (tl.name === name ? on : tl.auto_run)).map((tl) => tl.name);
    update.mutate({ auto_tools: list });
  };

  return (
    <MotionRow>
      <div className="group flex items-center gap-3 px-4 py-2.5">
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          aria-expanded={open}
          className="flex min-w-0 flex-1 items-center gap-3 text-left outline-none"
        >
          <ProviderMark provider={x.provider} name={x.name} icon={x.icon_url} />
          <div className={cn("min-w-0 flex-1", !x.enabled && "opacity-60")}>
            <div className="flex items-center gap-2">
              <span className="truncate text-[13px] font-medium text-fg">{x.name}</span>
              <span className="mono hidden truncate text-[11.5px] text-fg-4 sm:inline">{x.host}</span>
            </div>
            <div className="truncate text-[12px] text-fg-3">
              {x.status === "error" && x.last_error
                ? x.last_error
                : x.status === "pending"
                  ? t("integrations.row.pending")
                  : t("integrations.row.tools", { count: on, asks })}
              {x.synced_at && x.status === "connected" && <span className="text-fg-4"> · {t("integrations.row.synced", { when: relShort(x.synced_at) })}</span>}
            </div>
          </div>
          <ChevronDown className={cn("size-3.5 shrink-0 text-fg-4 transition-transform duration-150", open && "rotate-180")} aria-hidden />
        </button>
        <StatePill state={st.state}>{t(`integrations.state.${st.key}`)}</StatePill>
        {admin && (
          <div className="flex items-center gap-1">
            {x.status !== "connected" && x.auth_type === "oauth" ? (
              <Button size="xs" variant="primary" loading={signIn.isPending} onClick={() => signIn.mutate()}>
                {t("integrations.signIn")}
              </Button>
            ) : (
              <Switch checked={x.enabled} onCheckedChange={(enabled) => update.mutate({ enabled })} aria-label={t("integrations.toggle", { name: x.name })} />
            )}
            <Menu>
              <MenuTrigger asChild>
                <button
                  type="button"
                  aria-label={t("integrations.actions", { name: x.name })}
                  className="flex size-7 items-center justify-center rounded-md text-fg-3 transition-colors hover:bg-hover hover:text-fg"
                >
                  <MoreHorizontal className="size-4" />
                </button>
              </MenuTrigger>
              <MenuContent>
                <MenuItem icon={<RefreshCw />} disabled={x.status === "pending"} onSelect={() => sync.mutate()}>
                  {t("integrations.sync")}
                </MenuItem>
                {x.auth_type === "oauth" && (
                  <MenuItem icon={<LogIn />} onSelect={() => signIn.mutate()}>
                    {t("integrations.signInAgain")}
                  </MenuItem>
                )}
                <MenuSeparator />
                <MenuItem
                  danger
                  icon={<Trash2 />}
                  onSelect={() =>
                    confirm.show(t("integrations.disconnectBody", { name: x.name }), () => remove.mutateAsync(), {
                      title: t("integrations.disconnectTitle", { name: x.name }),
                      confirmLabel: t("integrations.disconnect"),
                      danger: true,
                    })
                  }
                >
                  {t("integrations.disconnect")}
                </MenuItem>
              </MenuContent>
            </Menu>
          </div>
        )}
      </div>
      <AnimatePresence initial={false}>
        {open && (
          <motion.div
            initial={reduce ? false : { height: 0, opacity: 0 }}
            animate={{ height: "auto", opacity: 1 }}
            exit={reduce ? { opacity: 0 } : { height: 0, opacity: 0 }}
            transition={{ duration: 0.2, ease: [0.23, 1, 0.32, 1] }}
            className="overflow-hidden border-t border-line-soft bg-canvas"
          >
            <ToolList x={x} disabled={!admin || update.isPending} onToggle={toggleTool} onAsk={toggleAsk} onAuto={toggleAuto} />
          </motion.div>
        )}
      </AnimatePresence>
    </MotionRow>
  );
}

function CatalogCard({ p, connected, admin, onConnect }: { p: IntegrationProvider; connected: boolean; admin: boolean; onConnect: () => void }) {
  const { t } = useTranslation("settingsDev");
  return (
    <motion.div
      layout="position"
      initial={{ opacity: 0, y: 4 }}
      animate={{ opacity: 1, y: 0 }}
      exit={{ opacity: 0 }}
      transition={{ duration: 0.18 }}
      className="flex flex-col gap-3 rounded-lg border border-line bg-surface p-3.5 shadow-xs transition-colors hover:border-line-strong"
    >
      <div className="flex items-center gap-2.5">
        <ProviderMark provider={p.key} name={p.name} />
        <div className="min-w-0 flex-1">
          <div className="truncate text-[13px] font-medium text-fg">{p.name}</div>
          <div className="text-[11.5px] text-fg-4">{t(`integrations.category.${p.category}`)}</div>
        </div>
      </div>
      <p className="line-clamp-2 min-h-[36px] text-[12px] leading-[18px] text-fg-3">{p.description}</p>
      <div className="flex items-center justify-between">
        <span className="text-[11.5px] text-fg-4">{t(`integrations.via.${p.auth}`)}</span>
        {connected ? (
          <span className="text-[12px] font-medium text-tone-green">{t("integrations.state.connected")}</span>
        ) : admin ? (
          <Button size="xs" onClick={onConnect}>
            {t("integrations.connect.connect")}
          </Button>
        ) : null}
      </div>
    </motion.div>
  );
}

export default function Integrations() {
  const { t } = useTranslation("settingsDev");
  const admin = atLeast(useRole(), "admin");
  const navigate = useNavigate();
  const search = useSearch({ from: "/app/settings/integrations" });
  const { data: list, isLoading } = useQuery(q.integrations());
  const { data: catalog } = useQuery(q.integrationCatalog());
  const [filter, setFilter] = useState<Filter>("all");
  const [connecting, setConnecting] = useState<IntegrationProvider | "custom" | null>(null);
  const handled = useRef(false);

  useEffect(() => {
    if (handled.current || (!search.connected && !search.error)) return;
    handled.current = true;
    if (search.connected) toast.success(t("integrations.connectedToast"));
    else if (search.error)
      toast.error((oauthErrors as readonly string[]).includes(search.error) ? t(`integrations.oauthErrors.${search.error as OAuthError}`) : t("integrations.connectFailed"));
    void navigate({ to: "/settings/integrations", search: {}, replace: true });
  }, [search.connected, search.error, navigate, t]);

  const rows = useMemo(() => {
    const rank = { error: 0, pending: 1, connected: 2 } as const;
    return [...(list ?? [])].sort((a, b) => rank[a.status] - rank[b.status] || a.name.localeCompare(b.name));
  }, [list]);
  const have = new Set(rows.map((x) => x.provider));
  const cards = (catalog ?? []).filter((p) => filter === "all" || p.category === filter);
  const present = new Set((catalog ?? []).map((p) => p.category));

  return (
    <SettingsPage
      wide
      title={t("integrations.title")}
      description={t("integrations.description")}
      actions={
        admin && (
          <Button icon={<Plus className="size-3.5" />} onClick={() => setConnecting("custom")}>
            {t("integrations.custom.add")}
          </Button>
        )
      }
    >
      {(isLoading || rows.length > 0) && (
        <Section flush title={t("integrations.connectedTitle")} description={t("integrations.connectedHint")}>
          {isLoading ? (
            <ListSkeleton rows={2} />
          ) : (
            <MotionList>
              {rows.map((x) => (
                <ConnectedRow key={x.id} x={x} admin={admin} />
              ))}
            </MotionList>
          )}
        </Section>
      )}

      <section className="animate-rise-in">
        <div className="mb-3 flex flex-wrap items-end justify-between gap-3">
          <div className="min-w-0">
            <h2 className="text-[13px] font-semibold tracking-[-0.01em]">{t("integrations.catalogTitle")}</h2>
            <p className="mt-0.5 text-[12px] leading-[18px] text-fg-3">{admin ? t("integrations.catalogHint") : t("integrations.adminsOnly")}</p>
          </div>
          <Segmented<Filter>
            size="xs"
            value={filter}
            onChange={setFilter}
            options={filters.filter((f) => f === "all" || present.has(f)).map((f) => ({ value: f, label: t(`integrations.category.${f}`) }))}
          />
        </div>
        <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-2 lg:grid-cols-3">
          <AnimatePresence initial={false} mode="popLayout">
            {cards.map((p) => (
              <CatalogCard key={p.key} p={p} admin={admin} connected={have.has(p.key)} onConnect={() => setConnecting(p)} />
            ))}
          </AnimatePresence>
          {admin && (
            <button
              type="button"
              onClick={() => setConnecting("custom")}
              className="flex flex-col items-start gap-1.5 rounded-lg border border-dashed border-line p-3.5 text-left transition-colors hover:border-line-strong hover:bg-surface"
            >
              <span className="text-[13px] font-medium text-fg">{t("integrations.custom.cardTitle")}</span>
              <span className="text-[12px] leading-[18px] text-fg-3">{t("integrations.custom.cardBody")}</span>
            </button>
          )}
        </div>
      </section>

      {connecting && (
        <ConnectDialog
          provider={connecting === "custom" ? null : connecting}
          onClose={() => setConnecting(null)}
          onConnected={(x) => {
            setConnecting(null);
            toast.success(t("integrations.connectedNamed", { name: x.name, count: x.tools.length }));
          }}
        />
      )}
    </SettingsPage>
  );
}
