import { useQueries, useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { MoreHorizontal, Pencil, Plus, RefreshCw, ShieldCheck, Trash2, Webhook as WebhookIcon } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { NoAccess, Section, SettingsPage } from "@/components/layout";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { SettingsEmpty } from "@/components/settings2/SettingsEmpty";
import { Tooltip } from "@/components/ui/tooltip";
import { Menu, MenuContent, MenuItem, MenuSeparator, MenuTrigger } from "@/components/ui/menu";
import { useConfirm } from "@/components/ui/confirm";
import { IconTile, ListSkeleton, MonoChip, StatePill, SummaryStrip } from "@/components/settings2/bits";
import { MotionList, MotionRow } from "@/components/settings2/motion";
import { useListMutation } from "@/components/settings2/optimistic";
import { SecretDialog, WebhookDialog, isAllEvents } from "@/components/settings2/webhooks";
import { GrantedTeams } from "@/components/settings2/teams";
import { api } from "@/lib/api/endpoints";
import type { Webhook, WebhookDelivery } from "@/lib/api/types";
import { fullDate, relShort } from "@/lib/format";
import { q, qk } from "@/lib/queries";
import { atLeast, useRole } from "@/lib/session";
import { cn } from "@/lib/cn";

type Health = { state: "active" | "failing" | "paused" | "unverified"; last: WebhookDelivery | null; total: number; failed: number };

/** A webhook is failing when its latest delivery failed or is retrying after an error. */
function health(h: Webhook, rows: WebhookDelivery[] | undefined): Health {
  const list = rows ?? [];
  const finished = list.find((d) => d.status !== "pending") ?? null;
  const failed = list.filter((d) => d.status === "failed").length;
  // A delivery still retrying after an error response counts as failing too.
  const retrying = list[0]?.status === "pending" && (list[0].response_code ?? 0) >= 400;
  const state = !h.verified ? "unverified" : !h.enabled ? "paused" : retrying || finished?.status === "failed" ? "failing" : "active";
  return { state, last: list[0] ?? null, total: list.filter((d) => d.status !== "pending").length, failed };
}

export default function Webhooks() {
  const { t } = useTranslation("settingsDev");
  const role = useRole();
  const admin = atLeast(role, "admin");
  const { data: hooks, isLoading } = useQuery({ ...q.webhooks(), enabled: admin });
  const deliveries = useQueries({
    queries: (hooks ?? []).slice(0, 25).map((h) => ({ ...q.webhookDeliveries(h.id), enabled: admin, staleTime: 30_000 })),
  });
  const navigate = useNavigate();
  const confirm = useConfirm();
  const [editing, setEditing] = useState<Webhook | "new" | null>(null);
  const [secret, setSecret] = useState<string | null>(null);

  const toggle = useListMutation<Webhook, { id: string; enabled: boolean }>(
    qk.webhooks,
    (v) => api.webhooks.update(v.id, { enabled: v.enabled }),
    (rows, v) => rows.map((r) => (r.id === v.id ? { ...r, enabled: v.enabled } : r)),
    { errorText: t("webhooks.updateError") },
  );
  const remove = useListMutation<Webhook, string>(qk.webhooks, (id) => api.webhooks.remove(id), (rows, id) => rows.filter((r) => r.id !== id), {
    errorText: t("webhooks.deleteError"),
  });

  if (!admin)
    return (
      <SettingsPage title={t("webhooks.title")}>
        <NoAccess what="webhooks" />
      </SettingsPage>
    );

  const open = (id: string) => void navigate({ to: "/settings/webhooks/$webhookId", params: { webhookId: id } });
  const list = hooks ?? [];
  const healths = list.map((h, i) => health(h, deliveries[i]?.data));
  const sum = healths.reduce(
    (c, x) => ({ ...c, [x.state]: c[x.state] + 1, total: c.total + x.total, failed: c.failed + x.failed }),
    { active: 0, failing: 0, paused: 0, unverified: 0, total: 0, failed: 0 },
  );
  const rate = sum.total ? Math.round(((sum.total - sum.failed) / sum.total) * 100) : null;

  return (
    <SettingsPage
      wide
      title={t("webhooks.title")}
      description={t("webhooks.description")}
      actions={
        <Button variant="primary" icon={<Plus className="size-3.5" />} onClick={() => setEditing("new")}>
          {t("webhooks.new")}
        </Button>
      }
    >
      {list.length > 0 && (
        <SummaryStrip
          items={[
            { label: t("webhooks.summary.active"), value: sum.active, tone: sum.active ? "green" : undefined },
            { label: t("webhooks.summary.failing"), value: sum.failing, tone: sum.failing ? "red" : undefined },
            { label: t("webhooks.summary.paused"), value: sum.paused, tone: sum.paused ? "amber" : undefined },
            {
              label: t("webhooks.summary.delivered"),
              value: rate === null ? "–" : `${rate}%`,
              hint: sum.total ? t("webhooks.summary.deliveredHint", { count: sum.total }) : t("webhooks.summary.noAttempts"),
            },
          ]}
        />
      )}
      <Section flush title={hooks ? t("webhooks.count", { count: list.length }) : undefined}>
        {isLoading ? (
          <ListSkeleton rows={3} />
        ) : !list.length ? (
          <SettingsEmpty
            art="settings"
            title={t("webhooks.empty.title")}
            description={t("webhooks.empty.description")}
            action={
              <Button variant="primary" icon={<Plus className="size-3.5" />} onClick={() => setEditing("new")}>
                {t("webhooks.new")}
              </Button>
            }
            tips={[
              { label: t("webhooks.empty.tips.signed"), icon: <ShieldCheck /> },
              { label: t("webhooks.empty.tips.retry"), icon: <RefreshCw /> },
            ]}
          />
        ) : (
          <>
            <div className="hidden h-8 items-center gap-4 border-b border-line-soft bg-sunken px-4 md:grid md:grid-cols-[minmax(0,1fr)_200px_110px_96px_64px]">
              <span className="label-caps">{t("webhooks.col.endpoint")}</span>
              <span className="label-caps">{t("webhooks.col.events")}</span>
              <span className="label-caps">{t("webhooks.col.lastDelivery")}</span>
              <span className="label-caps">{t("webhooks.col.status")}</span>
              <span />
            </div>
            <MotionList>
              {list.map((h, i) => {
                const hp = healths[i];
                const all = isAllEvents(h.events);
                return (
                  <MotionRow key={h.id}>
                    <div
                      role="button"
                      tabIndex={0}
                      onClick={() => open(h.id)}
                      onKeyDown={(e) => {
                        if ((e.key === "Enter" || e.key === " ") && e.target === e.currentTarget) {
                          e.preventDefault();
                          open(h.id);
                        }
                      }}
                      className="group flex cursor-pointer flex-wrap items-center gap-3 px-4 py-2.5 outline-none transition-colors hover:bg-sunken focus-visible:bg-sunken md:grid md:grid-cols-[minmax(0,1fr)_200px_110px_96px_64px] md:gap-4"
                    >
                      <div className="flex min-w-0 flex-1 items-center gap-3">
                        <IconTile tone={hp.state === "failing" ? "red" : hp.state === "paused" ? "gray" : hp.state === "unverified" ? "amber" : "teal"}>
                          <WebhookIcon />
                        </IconTile>
                        <div className={cn("min-w-0", !h.enabled && "opacity-60")}>
                          <div className="mono truncate text-[12.5px] text-fg">{h.url}</div>
                          <div className="truncate text-[12px] text-fg-3">{h.description || t("webhooks.noDescription")}</div>
                          <GrantedTeams grants={h.private_teams} className="mt-1" />
                        </div>
                      </div>
                      <div className="hidden min-w-0 items-center gap-1 md:flex">
                        {all ? (
                          <MonoChip>{t("webhooks.allEvents")}</MonoChip>
                        ) : (
                          <>
                            {h.events.slice(0, 1).map((e) => (
                              <MonoChip key={e} className="max-w-[150px] truncate">
                                {e}
                              </MonoChip>
                            ))}
                            {h.events.length > 1 && (
                              <Tooltip content={h.events.slice(1).join(", ")}>
                                <span>
                                  <MonoChip className="text-fg-3">+{h.events.length - 1}</MonoChip>
                                </span>
                              </Tooltip>
                            )}
                          </>
                        )}
                      </div>
                      <span className="mono hidden text-[11.5px] text-fg-3 md:block">
                        {hp.last ? (
                          <Tooltip content={fullDate(hp.last.created_at)}>
                            <span className="inline-flex items-center gap-1.5">
                              <span
                                className={cn(
                                  "size-1.5 rounded-full",
                                  hp.last.status === "succeeded" ? "bg-tone-green-solid" : hp.last.status === "failed" ? "bg-tone-red-solid" : "bg-tone-amber-solid",
                                )}
                              />
                              {relShort(hp.last.created_at)}
                            </span>
                          </Tooltip>
                        ) : (
                          <span className="text-fg-4">{t("webhooks.never")}</span>
                        )}
                      </span>
                      <span>
                        <StatePill state={hp.state === "unverified" ? "pending" : hp.state}>{t(`webhooks.state.${hp.state}`)}</StatePill>
                      </span>
                      <div className="flex items-center justify-end gap-1" onClick={(e) => e.stopPropagation()}>
                        <Switch checked={h.enabled} onCheckedChange={(enabled) => toggle.mutate({ id: h.id, enabled })} aria-label={`Webhook ${h.url} active`} />
                        <Menu>
                          <MenuTrigger asChild>
                            <button
                              type="button"
                              aria-label="Webhook actions"
                              className="flex size-7 items-center justify-center rounded-md text-fg-3 transition-colors hover:bg-hover hover:text-fg"
                            >
                              <MoreHorizontal className="size-4" />
                            </button>
                          </MenuTrigger>
                          <MenuContent>
                            <MenuItem icon={<Pencil />} onSelect={() => setEditing(h)}>
                              {t("webhooks.edit")}
                            </MenuItem>
                            <MenuSeparator />
                            <MenuItem
                              danger
                              icon={<Trash2 />}
                              onSelect={() =>
                                confirm.show(
                                  <>
                                    {t("webhooks.deleteBodyPrefix")} <span className="mono text-fg">{h.url}</span> {t("webhooks.deleteBodySuffix")}
                                  </>,
                                  () => remove.mutateAsync(h.id),
                                  { title: t("webhooks.deleteTitle"), confirmLabel: t("webhooks.deleteConfirm"), danger: true },
                                )
                              }
                            >
                              {t("webhooks.delete")}
                            </MenuItem>
                          </MenuContent>
                        </Menu>
                      </div>
                    </div>
                  </MotionRow>
                );
              })}
            </MotionList>
          </>
        )}
      </Section>
      {editing && (
        <WebhookDialog
          hook={editing === "new" ? null : editing}
          onClose={() => setEditing(null)}
          onCreated={(w) => {
            setEditing(null);
            setSecret(w.secret);
          }}
        />
      )}
      <SecretDialog secret={secret} onClose={() => setSecret(null)} />
    </SettingsPage>
  );
}
