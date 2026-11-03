import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { Ban, ChevronDown, RotateCcw, ScrollText, Trash2 } from "lucide-react";
import { useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { ActionButton, ActionMenuItem } from "@/components/ActionButton";
import { DataPairs, ExtLink, IdChip, None } from "@/components/bits";
import { ErrorState } from "@/components/ErrorState";
import { BackLink, Banner, DetailSkeleton, KV, Notice, PageBody, PageColumn, PageHeader, Section } from "@/components/layout";
import { MailboxStatusPill, PlanPill, SubscriptionPill, SuspendedPill, TestPill } from "@/components/pills";
import { ReasonDialog } from "@/components/ReasonDialog";
import { StepUpDialog } from "@/components/StepUpDialog";
import { Table, TD, TH, THead, TR } from "@/components/table";
import { Time } from "@/components/Time";
import { Avatar } from "@/components/ui/avatar";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/ui/confirm";
import { Dialog } from "@/components/ui/dialog";
import { EmptyState } from "@/components/ui/empty";
import { Field, NumberInput, TextInput } from "@/components/ui/input";
import { Menu, MenuContent, MenuSeparator, MenuTrigger } from "@/components/ui/menu";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast";
import { useDocumentTitle } from "@/app/RootLayout";
import { errorMessage } from "@/lib/api/client";
import { api } from "@/lib/api/endpoints";
import type { AdminLimits, AdminWorkspace, AuditLog } from "@/lib/api/types";
import { humanize, number, shortDate, usd } from "@/lib/format";
import { q, qk } from "@/lib/queries";
import { can } from "@/lib/permissions";
import { useSession } from "@/lib/session";
import { countsLine } from "@/lib/testWorkspaces";
import { CollabSection } from "./WorkspaceCollab";

export default function WorkspaceDetail() {
  const { t } = useTranslation(["workspaces", "admins", "audit", "common", "shell", "users"]);
  const { id } = useParams({ from: "/shell/workspaces/$id" });
  const ws = useQuery(q.workspace(id));
  useDocumentTitle(ws.data?.name);

  return (
    <>
      <PageHeader title={<BackLink to="/workspaces">{t("list.title")}</BackLink>} />
      <PageBody>
        {ws.isPending ? (
          <DetailSkeleton />
        ) : ws.isError ? (
          <ErrorState
            error={ws.error}
            onRetry={() => void ws.refetch()}
            action={
              <Button variant="ghost" asChild>
                <Link to="/workspaces">{t("detail.all")}</Link>
              </Button>
            }
          />
        ) : (
          <WorkspaceView w={ws.data} />
        )}
      </PageBody>
    </>
  );
}

function WorkspaceView({ w }: { w: AdminWorkspace }) {
  const { t } = useTranslation(["workspaces", "admins", "audit", "common", "shell", "users"]);
  const qc = useQueryClient();
  const navigate = useNavigate();
  const confirm = useConfirm();
  const billing = useSession((s) => s.me?.instance.billing_enabled ?? false);
  const canDelete = useSession((s) => can(s.me, "workspaces.delete"));
  const [dialog, setDialog] = useState<null | "suspend" | "delete" | "limits" | "grant">(null);
  const [confirmName, setConfirmName] = useState("");

  const apply = (next: AdminWorkspace) => {
    qc.setQueryData(qk.workspace(w.id), next);
    void qc.invalidateQueries({ queryKey: qk.workspaces });
    void qc.invalidateQueries({ queryKey: qk.summary });
  };

  const restore = () =>
    confirm.show(
      <>
        <Trans t={t} i18nKey="detail.restore.text" values={{ name: w.name }} components={{ b: <span className="font-medium text-fg" /> }} />
      </>,
      async () => {
        apply(await api.workspaces.unsuspend(w.id));
        toast.success(t("detail.toast.restored"));
      },
      { title: t("detail.restore.title"), confirmLabel: t("common:actions.restore") },
    );

  return (
    <PageColumn>
      <div className="flex flex-wrap items-start gap-4">
        <Avatar name={w.name} src={w.avatar_url} square size="xl" />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h1 className="truncate text-[18px] font-semibold tracking-[-0.02em]">{w.name}</h1>
            <PlanPill plan={w.plan} />
            {billing && <SubscriptionPill status={w.status} />}
            {w.test && <TestPill />}
            {w.suspended_at && <SuspendedPill />}
          </div>
          <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
            <IdChip label={t("detail.slug")} value={w.slug} />
            <IdChip label={t("common:id")} value={w.id} />
            <span className="text-[11.5px] text-fg-3">
              <Trans t={t} i18nKey="detail.createdActive" components={{ created: <Time iso={w.created_at} />, active: <Time iso={w.last_activity_at} /> }} />
            </span>
          </div>
        </div>
        <div className="flex items-center gap-1.5">
          <Button variant="ghost" asChild icon={<ScrollText />}>
            <Link to="/audit" search={{ target: w.id }}>
              {t("shell:nav.audit")}
            </Link>
          </Button>
          <Menu>
            <MenuTrigger asChild>
              <Button variant="secondary" iconRight={<ChevronDown />}>
                {t("users:detail.actions")}
              </Button>
            </MenuTrigger>
            <MenuContent className="w-[260px]">
              {w.suspended_at ? (
                <ActionMenuItem perm="workspaces.suspend" icon={<RotateCcw />} onSelect={restore}>
                  {t("detail.restore.button")}
                </ActionMenuItem>
              ) : (
                <ActionMenuItem perm="workspaces.suspend" icon={<Ban />} onSelect={() => setDialog("suspend")}>
                  {t("detail.suspend.button")}
                </ActionMenuItem>
              )}
              <MenuSeparator />
              <ActionMenuItem perm={w.test && !canDelete ? "workspaces.test" : "workspaces.delete"} icon={<Trash2 />} danger onSelect={() => setDialog("delete")}>
                {t("detail.delete.button")}
              </ActionMenuItem>
            </MenuContent>
          </Menu>
        </div>
      </div>

      {w.test && (
        <Banner
          tone="blue"
          title={w.test_expires_at ? <Trans t={t} i18nKey="detail.test.deleted" components={{ time: <Time iso={w.test_expires_at} /> }} /> : t("detail.test.kept")}
          action={
            <Button variant="secondary" size="xs" asChild>
              <Link to="/test-workspaces">{t("shell:nav.testWorkspaces")}</Link>
            </Button>
          }
        >
          {w.sample_data.status === "running"
            ? t("detail.test.adding")
            : w.sample_data.counts
              ? t("detail.test.sample", { counts: countsLine(w.sample_data.counts, 5) })
              : t("detail.test.noSample")}{" "}
          {t("detail.test.outOfCounts")}
        </Banner>
      )}

      {w.suspended_at && (
        <Banner title={<Trans t={t} i18nKey="users:detail.suspendedSince" components={{ time: <Time iso={w.suspended_at} /> }} />}>
          {w.suspended_reason ? <span className="whitespace-pre-wrap">{w.suspended_reason}</span> : t("common:noReason")} {t("detail.suspendedHint")}
        </Banner>
      )}

      <div className="grid items-start gap-5 lg:grid-cols-[minmax(0,1fr)_340px]">
        <div className="flex min-w-0 flex-col gap-5">
          <Members w={w} />
          <Mailboxes w={w} />
          <Ai w={w} onGrant={() => setDialog("grant")} />
          <RecentAudit items={w.recent_audit} />
        </div>
        <div className="flex min-w-0 flex-col gap-5">
          <Section title={t("detail.counts.title")}>
            <KV
              items={[
                [t("detail.counts.tickets"), <span className="mono tabular-nums">{number(w.counts.tickets)}</span>],
                [t("detail.counts.openTickets"), <span className="mono tabular-nums">{number(w.counts.open_tickets)}</span>],
                [t("detail.counts.contacts"), <span className="mono tabular-nums">{number(w.counts.contacts)}</span>],
                [t("detail.counts.articles"), <span className="mono tabular-nums">{number(w.counts.kb_articles)}</span>],
                [t("detail.counts.brands"), <span className="mono tabular-nums">{number(w.counts.brands)}</span>],
                [t("detail.counts.apiKeys"), <span className="mono tabular-nums">{number(w.counts.api_keys)}</span>],
                [t("detail.counts.webhooks"), <span className="mono tabular-nums">{number(w.counts.webhooks)}</span>],
              ]}
            />
          </Section>
          <PlanLimits w={w} billing={billing} onEdit={() => setDialog("limits")} />
          <CollabSection w={w} billing={billing} onSaved={apply} />
          <Subscription w={w} billing={billing} />
        </div>
      </div>

      <ReasonDialog
        open={dialog === "suspend"}
        onOpenChange={(o) => setDialog(o ? "suspend" : null)}
        title={t("detail.suspend.title", { name: w.name })}
        description={t("detail.suspend.description")}
        confirmLabel={t("detail.suspend.button")}
        onConfirm={async (reason) => {
          apply(await api.workspaces.suspend(w.id, reason));
          toast.success(t("detail.toast.suspended"));
        }}
      />

      <StepUpDialog
        open={dialog === "delete"}
        onOpenChange={(o) => {
          setDialog(o ? "delete" : null);
          if (!o) setConfirmName("");
        }}
        title={t("detail.delete.title", { name: w.name })}
        description={t("detail.delete.description")}
        confirmLabel={t("detail.delete.button")}
        danger
        ready={confirmName === w.name}
        onConfirm={async (code) => {
          await api.workspaces.remove(w.id, code, confirmName);
          qc.removeQueries({ queryKey: qk.workspace(w.id) });
          void qc.invalidateQueries({ queryKey: qk.workspaces });
          void qc.invalidateQueries({ queryKey: qk.summary });
          toast.success(t("detail.toast.deleted", { name: w.name }));
          void navigate({ to: "/workspaces" });
        }}
      >
        <Field label={<Trans t={t} i18nKey="common:ui.typeToConfirm" values={{ text: w.name }} components={{ v: <span className="mono font-medium" /> }} />}>
          {(p) => <TextInput {...p} autoFocus autoComplete="off" spellCheck={false} value={confirmName} onChange={(e) => setConfirmName(e.target.value)} />}
        </Field>
      </StepUpDialog>

      {dialog === "limits" && <LimitsDialog w={w} billing={billing} onClose={() => setDialog(null)} onSaved={apply} />}
      {dialog === "grant" && <GrantDialog w={w} onClose={() => setDialog(null)} onSaved={apply} />}
    </PageColumn>
  );
}

function Members({ w }: { w: AdminWorkspace }) {
  const { t } = useTranslation(["workspaces", "admins", "audit", "common", "shell", "users"]);
  return (
    <Section
      title={t("detail.members.title")}
      count={number(w.members_list.length)}
      flush
      actions={w.pending_invitations ? <span className="text-[11.5px] text-fg-3">{t("detail.members.pending", { count: w.pending_invitations, n: number(w.pending_invitations) })}</span> : undefined}
    >
      {w.members_list.length ? (
        <Table>
          <THead>
            <TH>{t("detail.members.member")}</TH>
            <TH>{t("admins:col.role")}</TH>
            <TH>{t("users:list.col.joined")}</TH>
          </THead>
          <tbody>
            {w.members_list.map((m) => (
              <TR key={m.user.id}>
                <TD>
                  <Link to="/users/$id" params={{ id: m.user.id }} className="group flex min-w-0 items-center gap-2.5">
                    <Avatar name={m.user.name || m.user.email} size="sm" />
                    <span className="min-w-0">
                      <span className="block truncate font-medium text-fg group-hover:underline">{m.user.name || m.user.email}</span>
                      {m.user.name && <span className="block truncate text-[11.5px] text-fg-3">{m.user.email}</span>}
                    </span>
                  </Link>
                </TD>
                <TD>
                  <Badge tone={m.role === "owner" ? "indigo" : m.role === "admin" ? "blue" : "neutral"}>{t(`common:memberRole.${m.role}`, { defaultValue: humanize(m.role) })}</Badge>
                </TD>
                <TD className="text-fg-3">
                  <Time iso={m.created_at} />
                </TD>
              </TR>
            ))}
          </tbody>
        </Table>
      ) : (
        <EmptyState compact art="users" title={t("detail.members.empty")} description={t("detail.members.emptyHint")} />
      )}
    </Section>
  );
}

function Mailboxes({ w }: { w: AdminWorkspace }) {
  const { t } = useTranslation(["workspaces", "admins", "audit", "common", "shell", "users"]);
  return (
    <Section title={t("detail.mailboxes.title")} count={number(w.mailboxes.length)} flush>
      {w.mailboxes.length ? (
        <Table>
          <THead>
            <TH>{t("detail.mailboxes.mailbox")}</TH>
            <TH>{t("detail.mailboxes.kinds")}</TH>
            <TH>{t("detail.mailboxes.status")}</TH>
            <TH>{t("detail.mailboxes.lastSync")}</TH>
          </THead>
          <tbody>
            {w.mailboxes.map((m) => (
              <TR key={m.id}>
                <TD>
                  <span className="block truncate font-medium text-fg">{m.name || m.email}</span>
                  <span className="block truncate text-[11.5px] text-fg-3">{m.email}</span>
                  {m.last_error && (
                    <span title={m.last_error} className="mono mt-0.5 line-clamp-2 max-w-[420px] text-[11px] text-danger-fg">
                      {m.last_error}
                    </span>
                  )}
                </TD>
                <TD className="text-fg-2">
                  <span className="mono text-[11.5px]">
                    {m.inbound_kind || "–"}, {m.outbound_kind || "–"}
                  </span>
                </TD>
                <TD>
                  <MailboxStatusPill status={m.status} />
                </TD>
                <TD className="text-fg-3">
                  <Time iso={m.last_synced_at} />
                </TD>
              </TR>
            ))}
          </tbody>
        </Table>
      ) : (
        <EmptyState compact art="workspaces" title={t("detail.mailboxes.empty")} description={t("detail.mailboxes.emptyHint")} />
      )}
    </Section>
  );
}


function PlanLimits({ w, billing, onEdit }: { w: AdminWorkspace; billing: boolean; onEdit: () => void }) {
  const { t } = useTranslation(["workspaces", "admins", "audit", "common", "shell", "users"]);
  const e = w.entitlements;
  const limit = (n: number) => (n === 0 ? t("detail.unlimited") : number(n));
  const over = (v: number | null) => (v === null ? <None>{t("detail.limits.planDefault")}</None> : <Badge tone="amber">{t("detail.limits.override")}</Badge>);
  return (
    <Section
      title={t("detail.limits.title")}
      actions={
        <ActionButton perm="workspaces.limits" size="xs" onClick={onEdit}>
          {t("detail.limits.edit")}
        </ActionButton>
      }
    >
      <KV
        items={[
          [t("list.col.plan"), <PlanPill plan={w.plan} />],
          [
            t("detail.limits.mailboxes"),
            <span className="flex items-center gap-2">
              <span className="mono tabular-nums">{limit(e.max_mailboxes)}</span>
              {over(w.limits.max_mailboxes)}
            </span>,
          ],
          [
            t("detail.limits.members"),
            <span className="flex items-center gap-2">
              <span className="mono tabular-nums">{limit(e.max_members)}</span>
              {over(w.limits.max_members)}
            </span>,
          ],
          [
            t("detail.limits.monthlyAi"),
            <span className="flex items-center gap-2">
              <span className="mono tabular-nums">{e.unlimited_ai ? t("detail.unlimited") : usd(e.monthly_ai_usd)}</span>
              {over(w.limits.monthly_ai_usd)}
            </span>,
          ],
          [
            t("detail.limits.collabStorage"),
            <span className="flex items-center gap-2">
              <span className="mono tabular-nums">{e.collab_storage_gb === 0 ? t("detail.unlimited") : t("detail.gb", { gb: number(e.collab_storage_gb) })}</span>
              {over(w.limits.collab_storage_gb)}
            </span>,
          ],
        ]}
      />
      {!billing && <p className="mt-3 text-[11.5px] leading-4 text-fg-3">{t("detail.limits.noBilling")}</p>}
    </Section>
  );
}

function Subscription({ w, billing }: { w: AdminWorkspace; billing: boolean }) {
  const { t } = useTranslation(["workspaces", "admins", "audit", "common", "shell", "users"]);
  const s = w.subscription;
  return (
    <Section title={t("list.col.subscription")}>
      {!s ? (
        <p className="text-[12.5px] text-fg-3">{billing ? t("detail.sub.none") : t("detail.sub.billingOff")}</p>
      ) : (
        <KV
          items={[
            [t("list.col.plan"), <PlanPill plan={s.plan} />],
            [t("detail.mailboxes.status"), <SubscriptionPill status={s.status} />],
            [t("detail.sub.billed"), s.interval === "year" ? t("detail.sub.yearly") : t("detail.sub.monthly")],
            [t("detail.sub.seats"), <span className="mono tabular-nums">{t("detail.sub.seatsLine", { billed: number(s.billed_seats), total: number(s.seats) })}</span>],
            [
              s.cancel_at_period_end ? t("detail.sub.ends") : t("detail.sub.renews"),
              s.current_period_end ? (
                <span>
                  {shortDate(s.current_period_end)} {s.cancel_at_period_end && <Badge tone="amber">{t("detail.sub.canceling")}</Badge>}
                </span>
              ) : (
                <None />
              ),
            ],
            [
              t("detail.sub.stripeCustomer"),
              s.stripe_customer_id ? (
                <ExtLink href={`https://dashboard.stripe.com/customers/${encodeURIComponent(s.stripe_customer_id)}`} className="mono text-[11.5px]">
                  {s.stripe_customer_id}
                </ExtLink>
              ) : (
                <None />
              ),
            ],
            [
              t("list.col.subscription"),
              s.stripe_subscription_id ? (
                <ExtLink href={`https://dashboard.stripe.com/subscriptions/${encodeURIComponent(s.stripe_subscription_id)}`} className="mono text-[11.5px]">
                  {s.stripe_subscription_id}
                </ExtLink>
              ) : (
                <None />
              ),
            ],
          ]}
        />
      )}
    </Section>
  );
}

function Ai({ w, onGrant }: { w: AdminWorkspace; onGrant: () => void }) {
  const { t } = useTranslation(["workspaces", "admins", "audit", "common", "shell", "users"]);
  const a = w.ai;
  const m = a.month;
  return (
    <Section
      title={t("detail.ai.title")}
      actions={
        <ActionButton perm="workspaces.limits" size="xs" onClick={onGrant}>
          {t("detail.grant.title")}
        </ActionButton>
      }
      flush
    >
      <div className="grid grid-cols-2 gap-px border-b border-line-soft bg-line-soft sm:grid-cols-4">
        {[
          [t("detail.ai.remaining"), a.unlimited ? t("detail.unlimited") : usd(a.remaining_usd)],
          [t("detail.ai.allowance"), a.unlimited ? t("detail.unlimited") : usd(a.monthly_usd)],
          [t("detail.ai.allowanceUsed"), usd(a.included_used_usd)],
          [t("detail.ai.purchasedLeft"), usd(a.purchased_usd)],
        ].map(([k, v]) => (
          <div key={k} className="bg-surface px-4 py-3">
            <div className="label-caps">{k}</div>
            <div className="mono mt-1.5 text-[15px] font-medium tabular-nums">{v}</div>
          </div>
        ))}
      </div>
      <div className="flex items-center gap-2 px-4 pt-3 text-[11.5px] text-fg-3">
        {t("detail.ai.period", { start: shortDate(a.period_start), end: shortDate(a.period_end) })}
      </div>
      {m.by_feature.length ? (
        <Table>
          <THead>
            <TH>{t("detail.ai.feature")}</TH>
            <TH align="right">{t("detail.ai.calls")}</TH>
            <TH align="right">{t("detail.ai.charged")}</TH>
            <TH align="right">{t("detail.ai.providerCost")}</TH>
          </THead>
          <tbody>
            {m.by_feature.map((f) => (
              <TR key={f.feature}>
                <TD>{humanize(f.feature)}</TD>
                <TD align="right" mono>
                  {number(f.calls)}
                </TD>
                <TD align="right" mono>
                  {usd(f.charged_usd)}
                </TD>
                <TD align="right" mono className="text-fg-3">
                  {usd(f.provider_cost_usd)}
                </TD>
              </TR>
            ))}
            <TR className="bg-sunken font-medium">
              <TD>{t("detail.ai.total")}</TD>
              <TD align="right" mono>
                {number(m.calls)}
              </TD>
              <TD align="right" mono>
                {usd(m.charged_usd)}
              </TD>
              <TD align="right" mono className="text-fg-2">
                {usd(m.provider_cost_usd)}
              </TD>
            </TR>
          </tbody>
        </Table>
      ) : (
        <p className="px-4 pb-4 pt-2 text-[12.5px] text-fg-3">{t("detail.ai.none")}</p>
      )}
    </Section>
  );
}

function Actor({ a }: { a: AuditLog }) {
  const { t } = useTranslation(["workspaces", "admins", "audit", "common", "shell", "users"]);
  if (a.data?.actor === "instance_admin") return <Badge tone="indigo">{t("detail.audit.instanceAdmin")}</Badge>;
  if (a.actor.type === "user" && a.actor.user)
    return (
      <Link to="/users/$id" params={{ id: a.actor.user.id }} className="truncate hover:underline">
        {a.actor.user.name || a.actor.user.email}
      </Link>
    );
  if (a.actor.type === "api_key") return <span className="text-fg-2">{t("detail.audit.apiKey")}</span>;
  return <span className="text-fg-2">{t("detail.audit.system")}</span>;
}

function RecentAudit({ items }: { items: AuditLog[] }) {
  const { t } = useTranslation(["workspaces", "admins", "audit", "common", "shell", "users"]);
  return (
    <Section title={t("detail.audit.title")} count={items.length ? t("detail.audit.last", { count: items.length }) : undefined} flush>
      {items.length ? (
        <Table>
          <THead>
            <TH>{t("detail.audit.who")}</TH>
            <TH>{t("audit:col.action")}</TH>
            <TH>{t("audit:col.details")}</TH>
            <TH>{t("audit:col.when")}</TH>
          </THead>
          <tbody>
            {items.map((a) => (
              <TR key={a.id}>
                <TD className="max-w-[180px]">
                  <Actor a={a} />
                </TD>
                <TD>
                  <span className="mono text-[11.5px]">{a.action}</span>
                </TD>
                <TD>
                  <DataPairs data={a.data} max={3} />
                </TD>
                <TD className="text-fg-3">
                  <Time iso={a.created_at} />
                </TD>
              </TR>
            ))}
          </tbody>
        </Table>
      ) : (
        <EmptyState compact art="audit" title={t("audit:empty.title")} description={t("detail.audit.emptyHint")} />
      )}
    </Section>
  );
}

type LimitKey = keyof AdminLimits;
/** Each field's label and hint are `limitsDialog.<key>.label` and `.hint` in the workspaces catalog. */
const LIMIT_FIELDS: { key: LimitKey; decimal?: boolean; suffix?: string; max: number }[] = [
  { key: "max_mailboxes", max: 100_000 },
  { key: "max_members", max: 100_000 },
  { key: "monthly_ai_usd", decimal: true, suffix: "USD", max: 100_000 },
  { key: "collab_storage_gb", suffix: "GB", max: 1_000_000 },
];

function LimitsDialog({ w, billing, onClose, onSaved }: { w: AdminWorkspace; billing: boolean; onClose: () => void; onSaved: (w: AdminWorkspace) => void }) {
  const { t } = useTranslation(["workspaces", "admins", "audit", "common", "shell", "users"]);
  const [draft, setDraft] = useState<AdminLimits>({ ...w.limits });
  const [valid, setValid] = useState<Record<LimitKey, boolean>>({ max_mailboxes: true, max_members: true, monthly_ai_usd: true, collab_storage_gb: true });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const planValue = (k: LimitKey) => (k === "monthly_ai_usd" ? w.entitlements.monthly_ai_usd : w.entitlements[k]);
  const changed = LIMIT_FIELDS.some((f) => draft[f.key] !== w.limits[f.key]);
  const ok = Object.values(valid).every(Boolean);

  const save = async () => {
    setBusy(true);
    setError(null);
    try {
      onSaved(await api.workspaces.setLimits(w.id, draft));
      toast.success(t("limitsDialog.saved"));
      onClose();
    } catch (e) {
      setBusy(false);
      setError(errorMessage(e));
    }
  };

  return (
    <Dialog
      open
      onOpenChange={(o) => !o && !busy && onClose()}
      title={t("limitsDialog.title")}
      description={t("limitsDialog.description", { name: w.name })}
      footer={
        <>
          <Button variant="ghost" disabled={busy} onClick={onClose}>
            {t("common:actions.cancel")}
          </Button>
          <Button variant="primary" loading={busy} disabled={!changed || !ok} onClick={() => void save()}>
            {t("limitsDialog.save")}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        {!billing && <Notice kind="info">{t("limitsDialog.noBilling")}</Notice>}
        {LIMIT_FIELDS.map((f) => {
          const v = draft[f.key];
          const useDefault = v === null;
          const label = t(`limitsDialog.${f.key}.label`);
          return (
            <div key={f.key} className="flex flex-col gap-2 rounded-lg border border-line-soft p-3">
              <div className="flex items-center justify-between gap-3">
                <span className="text-[12.5px] font-medium">{label}</span>
                <label className="flex items-center gap-2 text-[12px] text-fg-2">
                  {t("limitsDialog.usePlan")}
                  <Switch
                    checked={useDefault}
                    aria-label={t("limitsDialog.usePlanFor", { label })}
                    onCheckedChange={(on) => {
                      setDraft((d) => ({ ...d, [f.key]: on ? null : (w.limits[f.key] ?? planValue(f.key)) }));
                      setValid((s) => ({ ...s, [f.key]: true }));
                    }}
                  />
                </label>
              </div>
              {useDefault ? (
                <p className="text-[11.5px] text-fg-3">{t("limitsDialog.planApplies")}</p>
              ) : (
                <Field hint={t(`limitsDialog.${f.key}.hint`)}>
                  {(p) => (
                    <NumberInput
                      {...p}
                      aria-label={label}
                      value={v as number}
                      min={0}
                      max={f.max}
                      decimal={f.decimal}
                      step={f.decimal ? 1 : 1}
                      suffix={f.suffix}
                      onChange={(n) => setDraft((d) => ({ ...d, [f.key]: n }))}
                      onValidChange={(okNow) => setValid((s) => ({ ...s, [f.key]: okNow }))}
                    />
                  )}
                </Field>
              )}
            </div>
          );
        })}
        {error && <Notice>{error}</Notice>}
      </div>
    </Dialog>
  );
}

function GrantDialog({ w, onClose, onSaved }: { w: AdminWorkspace; onClose: () => void; onSaved: (w: AdminWorkspace) => void }) {
  const { t } = useTranslation(["workspaces", "admins", "audit", "common", "shell", "users"]);
  const [amount, setAmount] = useState(10);
  const [amountOk, setAmountOk] = useState(true);
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const n = note.trim();
  const ok = amountOk && amount >= 0.01 && amount <= 10_000 && n.length >= 1 && n.length <= 200;

  const save = async () => {
    if (!ok || busy) return;
    setBusy(true);
    setError(null);
    try {
      onSaved(await api.workspaces.grantAi(w.id, Math.round(amount * 100) / 100, n));
      toast.success(t("detail.grant.done", { amount: usd(amount) }));
      onClose();
    } catch (e) {
      setBusy(false);
      setError(errorMessage(e));
    }
  };

  return (
    <Dialog
      open
      onOpenChange={(o) => !o && !busy && onClose()}
      title={t("detail.grant.title")}
      description={t("detail.grant.description", { name: w.name })}
      size="sm"
      footer={
        <>
          <Button variant="ghost" disabled={busy} onClick={onClose}>
            {t("common:actions.cancel")}
          </Button>
          <Button variant="primary" loading={busy} disabled={!ok} onClick={() => void save()}>
            {amountOk ? t("detail.grant.confirmAmount", { amount: usd(amount) }) : t("detail.grant.confirm")}
          </Button>
        </>
      }
    >
      <form
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <Field label={t("detail.grant.amount")} hint={t("detail.grant.amountHint", { min: usd(0.01), max: usd(10_000) })}>
          {(p) => <NumberInput {...p} value={amount} onChange={setAmount} onValidChange={setAmountOk} min={0.01} max={10_000} decimal suffix="USD" />}
        </Field>
        <Field label={t("collab.note")} hint={t("detail.grant.noteHint", { length: note.length, max: 200 })} error={note.length > 200 ? t("common:reason.tooLong", { max: 200 }) : undefined}>
          {(p) => <TextInput {...p} autoFocus value={note} onChange={(e) => setNote(e.target.value)} placeholder={t("detail.grant.notePlaceholder")} />}
        </Field>
        {error && <Notice>{error}</Notice>}
        <button type="submit" hidden aria-hidden tabIndex={-1} />
      </form>
    </Dialog>
  );
}
