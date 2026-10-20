import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams } from "@tanstack/react-router";
import { Activity, Pencil, RefreshCw, Send, Trash2 } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { BackLink, NoAccess, Section, SettingsPage } from "@/components/layout";
import { Button } from "@/components/ui/button";
import { TextInput } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { EmptyState } from "@/components/ui/empty";
import { SettingsEmpty } from "@/components/settings2/SettingsEmpty";
import { Tooltip } from "@/components/ui/tooltip";
import { useConfirm } from "@/components/ui/confirm";
import { toast } from "@/components/ui/toast";
import { ListSkeleton, MonoChip, StatePill, SummaryStrip } from "@/components/settings2/bits";
import { MotionList, MotionRow } from "@/components/settings2/motion";
import { Segmented } from "@/components/ui/tabs";
import { useListMutation } from "@/components/settings2/optimistic";
import { WebhookDialog, isAllEvents } from "@/components/settings2/webhooks";
import { api } from "@/lib/api/endpoints";
import type { Webhook, WebhookDelivery } from "@/lib/api/types";
import { fullDate, relShort } from "@/lib/format";
import { q, qk, removeFromList } from "@/lib/queries";
import { atLeast, useRole } from "@/lib/session";
import { cn } from "@/lib/cn";
import { tone } from "@/lib/tones";

const deliveryState: Record<WebhookDelivery["status"], "active" | "failing" | "pending"> = { succeeded: "active", failed: "failing", pending: "pending" };

/** The HTTP response in its tone: 2xx green, 3xx amber, 4xx and 5xx red. */
function CodePill({ code }: { code: number | null }) {
  if (code == null) return <span className="mono text-[12px] text-fg-4">–</span>;
  const t = code < 300 ? tone.green : code < 400 ? tone.amber : tone.red;
  return <span className={cn("mono inline-flex h-5 items-center rounded-sm px-1.5 text-[11px] font-medium tabular-nums", t.chip)}>{code}</span>;
}

const grid = "grid grid-cols-[96px_minmax(0,1fr)_64px_56px] items-center gap-3 px-4 sm:grid-cols-[104px_minmax(0,1fr)_80px_80px_72px_72px]";

export default function WebhookDetail() {
  const { t } = useTranslation("settingsDev");
  const { webhookId } = useParams({ from: "/app/settings/webhooks/$webhookId" });
  const role = useRole();
  const admin = atLeast(role, "admin");
  const { data: hooks, isLoading } = useQuery({ ...q.webhooks(), enabled: admin });
  const deliveries = useQuery({ ...q.webhookDeliveries(webhookId), enabled: admin });
  const hook = hooks?.find((h) => h.id === webhookId);
  const qc = useQueryClient();
  const navigate = useNavigate();
  const confirm = useConfirm();
  const [editing, setEditing] = useState(false);
  const [filter, setFilter] = useState<"all" | "failed">("all");

  const toggle = useListMutation<Webhook, boolean>(
    qk.webhooks,
    (enabled) => api.webhooks.update(webhookId, { enabled }),
    (rows, enabled) => rows.map((r) => (r.id === webhookId ? { ...r, enabled } : r)),
    { errorText: t("webhooks.updateError") },
  );
  const test = useMutation({
    mutationFn: () => api.webhooks.test(webhookId),
    onSuccess: (d) => {
      if (d?.id) qc.setQueryData<WebhookDelivery[]>(qk.webhookDeliveries(webhookId), (old) => [d, ...(old ?? []).filter((x) => x.id !== d.id)]);
      else void qc.invalidateQueries({ queryKey: qk.webhookDeliveries(webhookId) });
      toast.success(t("webhook.testSent"), { description: t("webhook.testSentHint") });
    },
    onError: (e) => toast.error(e, t("webhook.testError")),
  });

  const back = <BackLink to="/settings/webhooks">{t("webhooks.title")}</BackLink>;
  if (!admin)
    return (
      <SettingsPage title={t("webhook.title")}>
        <NoAccess what="webhooks" />
      </SettingsPage>
    );
  if (isLoading)
    return (
      <SettingsPage wide title={t("webhook.title")} back={back}>
        <div className="skeleton h-[74px] rounded-lg" />
        <div className="skeleton h-20 rounded-lg" />
        <div className="skeleton h-64 rounded-lg" />
      </SettingsPage>
    );
  if (!hook)
    return (
      <SettingsPage wide title={t("webhook.title")} back={back}>
        <EmptyState
          lit={3}
          title={t("webhook.notFound")}
          description={t("webhook.notFoundHint")}
          action={
            <Button onClick={() => void navigate({ to: "/settings/webhooks" })}>{t("webhook.backToList")}</Button>
          }
        />
      </SettingsPage>
    );

  const rows = (deliveries.data ?? []).filter((d) => filter === "all" || d.status === "failed");
  const all = deliveries.data ?? [];
  const failed = all.filter((d) => d.status === "failed").length;
  const done = all.filter((d) => d.status !== "pending");
  const rate = done.length ? Math.round(((done.length - failed) / done.length) * 100) : null;
  const timed = all.filter((d) => d.duration_ms != null);
  const avg = timed.length ? Math.round(timed.reduce((n, d) => n + (d.duration_ms ?? 0), 0) / timed.length) : null;
  const retrying = all[0]?.status === "pending" && (all[0].response_code ?? 0) >= 400;
  const state = !hook.verified ? "unverified" : !hook.enabled ? "paused" : retrying || done[0]?.status === "failed" ? "failing" : "active";
  const everything = isAllEvents(hook.events);

  return (
    <SettingsPage
      wide
      back={back}
      title={<span className="mono text-[17px] tracking-[-0.01em]">{hook.url}</span>}
      description={hook.description || undefined}
      actions={
        <>
          <label className="mr-1 flex items-center gap-2 text-[12.5px] text-fg-2">
            <StatePill state={state === "unverified" ? "pending" : state}>{t(`webhooks.state.${state}`)}</StatePill>
            <Switch checked={hook.enabled} onCheckedChange={(v) => toggle.mutate(v)} aria-label="Webhook active" />
          </label>
          <Button icon={<Send className="size-3.5" />} loading={test.isPending} onClick={() => test.mutate()} disabled={!hook.enabled || !hook.verified}>
            {t("webhook.sendTest")}
          </Button>
          <Button square aria-label="Edit webhook" icon={<Pencil className="size-3.5" />} onClick={() => setEditing(true)} />
          <Button
            square
            variant="danger"
            aria-label="Delete webhook"
            icon={<Trash2 className="size-3.5" />}
            onClick={() =>
              confirm.show(
                <>
                  {t("webhooks.deleteBodyPrefix")} <span className="mono text-fg">{hook.url}</span> {t("webhooks.deleteBodySuffix")}
                </>,
                async () => {
                  await api.webhooks.remove(hook.id);
                  removeFromList(qc, qk.webhooks, hook.id);
                  toast.success(t("webhook.deleted"));
                  void navigate({ to: "/settings/webhooks" });
                },
                { title: t("webhooks.deleteTitle"), confirmLabel: t("webhooks.deleteConfirm"), danger: true },
              )
            }
          />
        </>
      }
    >
      {!hook.verified && <VerifyWebhook hook={hook} />}

      <SummaryStrip
        items={[
          { label: t("webhook.summary.delivered"), value: rate === null ? "–" : `${rate}%`, tone: rate === null ? undefined : rate >= 95 ? "green" : rate >= 80 ? "amber" : "red" },
          { label: t("webhook.summary.failed"), value: failed, tone: failed ? "red" : undefined },
          { label: t("webhook.summary.avg"), value: avg === null ? "–" : `${avg} ms` },
          { label: t("webhook.summary.last"), value: all[0] ? relShort(all[0].created_at) : "–", hint: all[0]?.event_type },
        ]}
      />

      <Section title={t("webhook.events")} description={everything ? t("webhook.eventsAll") : t("webhook.eventsCount", { count: hook.events.length })}>
        <div className="flex flex-wrap gap-1 py-3">
          {everything ? <MonoChip>{t("webhooks.allEvents")}</MonoChip> : hook.events.map((e) => <MonoChip key={e}>{e}</MonoChip>)}
        </div>
      </Section>

      <Section
        flush
        title={t("webhook.deliveries")}
        description={t("webhook.deliveriesHint")}
        actions={
          <div className="flex items-center gap-1">
            <Segmented
              size="xs"
              value={filter}
              onChange={setFilter}
              options={[
                { value: "all", label: t("webhook.filterAll") },
                { value: "failed", label: failed ? t("webhook.filterFailedCount", { count: failed }) : t("webhook.filterFailed") },
              ]}
            />
            <Tooltip content={t("webhook.refresh")}>
              <button
                type="button"
                aria-label="Refresh deliveries"
                onClick={() => void deliveries.refetch()}
                className="flex size-7 items-center justify-center rounded-md text-fg-3 transition-colors hover:bg-hover hover:text-fg"
              >
                <RefreshCw className={cn("size-3.5", deliveries.isFetching && "animate-spin")} />
              </button>
            </Tooltip>
          </div>
        }
      >
        {deliveries.isLoading ? (
          <ListSkeleton rows={5} />
        ) : !rows.length ? (
          filter === "failed" ? (
            <EmptyState compact lit={0} className="py-10" title={t("webhook.emptyFailed")} description={t("webhook.emptyFailedHint")} />
          ) : (
            // The header already offers Send test event; one button keeps the label unique.
            <SettingsEmpty
              art="settings"
              title={t("webhook.empty")}
              description={t("webhook.emptyHint")}
              tips={[
                { label: t("webhook.emptyTips.status"), icon: <Activity /> },
                { label: t("webhook.emptyTips.retry"), icon: <RefreshCw /> },
              ]}
            />
          )
        ) : (
          <div role="table" aria-label={t("webhook.deliveries")} className="text-[12.5px]">
            <div role="row" className={cn(grid, "h-8 border-b border-line-soft bg-sunken")}>
              <span role="columnheader" className="label-caps">{t("webhook.col.status")}</span>
              <span role="columnheader" className="label-caps">{t("webhook.col.event")}</span>
              <span role="columnheader" className="label-caps text-right">{t("webhook.col.response")}</span>
              <span role="columnheader" className="label-caps hidden text-right sm:block">{t("webhook.col.duration")}</span>
              <span role="columnheader" className="label-caps hidden text-right sm:block">{t("webhook.col.attempts")}</span>
              <span role="columnheader" className="label-caps text-right">{t("webhook.col.when")}</span>
            </div>
            <MotionList>
              {rows.map((d) => (
                <MotionRow key={d.id}>
                  <div role="row" className={cn(grid, "h-10 transition-colors hover:bg-sunken")}>
                    <span role="cell">
                      <StatePill state={deliveryState[d.status]} pulse={d.status === "pending"}>
                        {t(`webhook.status.${d.status}`)}
                      </StatePill>
                    </span>
                    <span role="cell" className="mono truncate text-[12px] text-fg-2">
                      {d.event_type}
                    </span>
                    <span role="cell" className="text-right">
                      <CodePill code={d.response_code} />
                    </span>
                    <span role="cell" className="mono hidden text-right text-[12px] tabular-nums text-fg-3 sm:block">
                      {d.duration_ms != null ? `${d.duration_ms} ms` : "–"}
                    </span>
                    <span role="cell" className={cn("mono hidden text-right text-[12px] tabular-nums sm:block", d.attempts > 1 ? "text-tone-amber" : "text-fg-3")}>
                      {d.attempts}
                    </span>
                    <span role="cell" className="text-right">
                      <Tooltip content={fullDate(d.created_at)}>
                        <span className="mono text-[11.5px] text-fg-3">{relShort(d.created_at)}</span>
                      </Tooltip>
                    </span>
                  </div>
                </MotionRow>
              ))}
            </MotionList>
          </div>
        )}
      </Section>
      {editing && <WebhookDialog hook={hook} onClose={() => setEditing(false)} />}
    </SettingsPage>
  );
}

/** An address that has not answered its verification challenge: enter the challenge it got, or send a new one. */
function VerifyWebhook({ hook }: { hook: Webhook }) {
  const { t } = useTranslation("settingsDev");
  const qc = useQueryClient();
  const [code, setCode] = useState("");
  const done = (w: Webhook) => {
    qc.setQueryData<Webhook[]>(qk.webhooks, (rows) => rows?.map((r) => (r.id === w.id ? w : r)));
    return w.verified;
  };
  const verify = useMutation({
    mutationFn: () => api.webhooks.verify(hook.id, code.trim()),
    onSuccess: (w) => {
      if (done(w)) toast.success(t("webhook.verify.verified"));
    },
    onError: (e) => toast.error(e, t("webhook.verify.error")),
  });
  const resend = useMutation({
    mutationFn: () => api.webhooks.verify(hook.id),
    onSuccess: (w) => (done(w) ? toast.success(t("webhook.verify.verified")) : toast.info(t("webhook.verify.resent"), { description: t("webhook.verify.stillWaiting") })),
    onError: (e) => toast.error(e, t("webhook.verify.error")),
  });
  return (
    <Section title={t("webhook.verify.title")} description={t("webhook.verify.description")}>
      <form
        className="flex flex-wrap items-center gap-2 py-3"
        onSubmit={(e) => {
          e.preventDefault();
          if (code.trim()) verify.mutate();
        }}
      >
        <TextInput
          aria-label={t("webhook.verify.codeLabel")}
          placeholder={t("webhook.verify.codePlaceholder")}
          value={code}
          onChange={(e) => setCode(e.target.value)}
          className="mono w-72"
          autoComplete="off"
          spellCheck={false}
        />
        <Button type="submit" variant="primary" loading={verify.isPending} disabled={!code.trim()}>
          {t("webhook.verify.submit")}
        </Button>
        <Button type="button" loading={resend.isPending} onClick={() => resend.mutate()}>
          {t("webhook.verify.resend")}
        </Button>
      </form>
    </Section>
  );
}
