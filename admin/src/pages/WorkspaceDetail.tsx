import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { Ban, ChevronDown, RotateCcw, ScrollText, Trash2 } from "lucide-react";
import { useState } from "react";
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
  const { id } = useParams({ from: "/shell/workspaces/$id" });
  const ws = useQuery(q.workspace(id));
  useDocumentTitle(ws.data?.name);

  return (
    <>
      <PageHeader title={<BackLink to="/workspaces">Workspaces</BackLink>} />
      <PageBody>
        {ws.isPending ? (
          <DetailSkeleton />
        ) : ws.isError ? (
          <ErrorState
            error={ws.error}
            onRetry={() => void ws.refetch()}
            action={
              <Button variant="ghost" asChild>
                <Link to="/workspaces">All workspaces</Link>
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
        <span className="font-medium text-fg">{w.name}</span> works again for its members, API keys, widget and help center. Mail ignored while it was suspended is not replayed.
      </>,
      async () => {
        apply(await api.workspaces.unsuspend(w.id));
        toast.success("Workspace restored");
      },
      { title: "Restore this workspace?", confirmLabel: "Restore" },
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
            <IdChip label="slug" value={w.slug} />
            <IdChip label="id" value={w.id} />
            <span className="text-[11.5px] text-fg-3">
              Created <Time iso={w.created_at} />, last active <Time iso={w.last_activity_at} />
            </span>
          </div>
        </div>
        <div className="flex items-center gap-1.5">
          <Button variant="ghost" asChild icon={<ScrollText />}>
            <Link to="/audit" search={{ target: w.id }}>
              Audit log
            </Link>
          </Button>
          <Menu>
            <MenuTrigger asChild>
              <Button variant="secondary" iconRight={<ChevronDown />}>
                Actions
              </Button>
            </MenuTrigger>
            <MenuContent className="w-[260px]">
              {w.suspended_at ? (
                <ActionMenuItem perm="workspaces.suspend" icon={<RotateCcw />} onSelect={restore}>
                  Restore workspace
                </ActionMenuItem>
              ) : (
                <ActionMenuItem perm="workspaces.suspend" icon={<Ban />} onSelect={() => setDialog("suspend")}>
                  Suspend workspace
                </ActionMenuItem>
              )}
              <MenuSeparator />
              <ActionMenuItem perm={w.test && !canDelete ? "workspaces.test" : "workspaces.delete"} icon={<Trash2 />} danger onSelect={() => setDialog("delete")}>
                Delete workspace
              </ActionMenuItem>
            </MenuContent>
          </Menu>
        </div>
      </div>

      {w.test && (
        <Banner
          tone="blue"
          title={w.test_expires_at ? <>Test workspace, deleted <Time iso={w.test_expires_at} /></> : "Test workspace, kept until deleted"}
          action={
            <Button variant="secondary" size="xs" asChild>
              <Link to="/test-workspaces">Test workspaces</Link>
            </Button>
          }
        >
          {w.sample_data.status === "running"
            ? "Sample data is being added."
            : w.sample_data.counts
              ? `Sample data: ${countsLine(w.sample_data.counts, 5)}.`
              : "No sample data."}{" "}
          It stays out of the overview's counts.
        </Banner>
      )}

      {w.suspended_at && (
        <Banner title={<>Suspended <Time iso={w.suspended_at} /></>}>
          {w.suspended_reason ? <span className="whitespace-pre-wrap">{w.suspended_reason}</span> : "No reason was given."} Members can sign in and see it, but nothing runs for it until it is restored.
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
          <Section title="Counts">
            <KV
              items={[
                ["Tickets", <span className="mono tabular-nums">{number(w.counts.tickets)}</span>],
                ["Open tickets", <span className="mono tabular-nums">{number(w.counts.open_tickets)}</span>],
                ["Contacts", <span className="mono tabular-nums">{number(w.counts.contacts)}</span>],
                ["Articles", <span className="mono tabular-nums">{number(w.counts.kb_articles)}</span>],
                ["Brands", <span className="mono tabular-nums">{number(w.counts.brands)}</span>],
                ["API keys", <span className="mono tabular-nums">{number(w.counts.api_keys)}</span>],
                ["Webhooks", <span className="mono tabular-nums">{number(w.counts.webhooks)}</span>],
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
        title={`Suspend ${w.name}?`}
        description="Members, API keys, the widget and the help center stop working, inbound mail is stored and ignored, and nothing is sent. Nothing is deleted."
        confirmLabel="Suspend workspace"
        onConfirm={async (reason) => {
          apply(await api.workspaces.suspend(w.id, reason));
          toast.success("Workspace suspended");
        }}
      />

      <StepUpDialog
        open={dialog === "delete"}
        onOpenChange={(o) => {
          setDialog(o ? "delete" : null);
          if (!o) setConfirmName("");
        }}
        title={`Delete ${w.name}?`}
        description="Every ticket, contact, article, mailbox and setting of this workspace is removed for good, and its Stripe subscription is canceled. This can't be undone."
        confirmLabel="Delete workspace"
        danger
        ready={confirmName === w.name}
        onConfirm={async (code) => {
          await api.workspaces.remove(w.id, code, confirmName);
          qc.removeQueries({ queryKey: qk.workspace(w.id) });
          void qc.invalidateQueries({ queryKey: qk.workspaces });
          void qc.invalidateQueries({ queryKey: qk.summary });
          toast.success(`${w.name} was deleted`);
          void navigate({ to: "/workspaces" });
        }}
      >
        <Field label={<>Type <span className="mono font-medium">{w.name}</span> to confirm</>}>
          {(p) => <TextInput {...p} autoFocus autoComplete="off" spellCheck={false} value={confirmName} onChange={(e) => setConfirmName(e.target.value)} />}
        </Field>
      </StepUpDialog>

      {dialog === "limits" && <LimitsDialog w={w} billing={billing} onClose={() => setDialog(null)} onSaved={apply} />}
      {dialog === "grant" && <GrantDialog w={w} onClose={() => setDialog(null)} onSaved={apply} />}
    </PageColumn>
  );
}

function Members({ w }: { w: AdminWorkspace }) {
  return (
    <Section
      title="Members"
      count={number(w.members_list.length)}
      flush
      actions={w.pending_invitations ? <span className="text-[11.5px] text-fg-3">{number(w.pending_invitations)} pending invitations</span> : undefined}
    >
      {w.members_list.length ? (
        <Table>
          <THead>
            <TH>Member</TH>
            <TH>Role</TH>
            <TH>Joined</TH>
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
                  <Badge tone={m.role === "owner" ? "indigo" : m.role === "admin" ? "blue" : "neutral"}>{humanize(m.role)}</Badge>
                </TD>
                <TD className="text-fg-3">
                  <Time iso={m.created_at} />
                </TD>
              </TR>
            ))}
          </tbody>
        </Table>
      ) : (
        <EmptyState compact art="users" title="No members" description="Everyone has left this workspace." />
      )}
    </Section>
  );
}

function Mailboxes({ w }: { w: AdminWorkspace }) {
  return (
    <Section title="Mailboxes" count={number(w.mailboxes.length)} flush>
      {w.mailboxes.length ? (
        <Table>
          <THead>
            <TH>Mailbox</TH>
            <TH>Receives, sends</TH>
            <TH>Status</TH>
            <TH>Last sync</TH>
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
        <EmptyState compact art="workspaces" title="No mailboxes" description="This workspace hasn't connected a mailbox." />
      )}
    </Section>
  );
}

const limit = (n: number) => (n === 0 ? "Unlimited" : number(n));

function PlanLimits({ w, billing, onEdit }: { w: AdminWorkspace; billing: boolean; onEdit: () => void }) {
  const e = w.entitlements;
  const over = (v: number | null) => (v === null ? <None>Plan default</None> : <Badge tone="amber">Override</Badge>);
  return (
    <Section
      title="Plan and limits"
      actions={
        <ActionButton perm="workspaces.limits" size="xs" onClick={onEdit}>
          Edit overrides
        </ActionButton>
      }
    >
      <KV
        items={[
          ["Plan", <PlanPill plan={w.plan} />],
          [
            "Mailboxes",
            <span className="flex items-center gap-2">
              <span className="mono tabular-nums">{limit(e.max_mailboxes)}</span>
              {over(w.limits.max_mailboxes)}
            </span>,
          ],
          [
            "Members",
            <span className="flex items-center gap-2">
              <span className="mono tabular-nums">{limit(e.max_members)}</span>
              {over(w.limits.max_members)}
            </span>,
          ],
          [
            "Monthly AI",
            <span className="flex items-center gap-2">
              <span className="mono tabular-nums">{e.unlimited_ai ? "Unlimited" : usd(e.monthly_ai_usd)}</span>
              {over(w.limits.monthly_ai_usd)}
            </span>,
          ],
        ]}
      />
      {!billing && <p className="mt-3 text-[11.5px] leading-4 text-fg-3">Billing is off on this instance, so every limit is unlimited. Overrides are kept but have no effect.</p>}
    </Section>
  );
}

function Subscription({ w, billing }: { w: AdminWorkspace; billing: boolean }) {
  const s = w.subscription;
  return (
    <Section title="Subscription">
      {!s ? (
        <p className="text-[12.5px] text-fg-3">{billing ? "No subscription." : "Billing is off on this instance."}</p>
      ) : (
        <KV
          items={[
            ["Plan", <PlanPill plan={s.plan} />],
            ["Status", <SubscriptionPill status={s.status} />],
            ["Billed", s.interval === "year" ? "Yearly" : "Monthly"],
            ["Seats", <span className="mono tabular-nums">{number(s.billed_seats)} billed of {number(s.seats)}</span>],
            [
              s.cancel_at_period_end ? "Ends" : "Renews",
              s.current_period_end ? (
                <span>
                  {shortDate(s.current_period_end)} {s.cancel_at_period_end && <Badge tone="amber">Canceling</Badge>}
                </span>
              ) : (
                <None />
              ),
            ],
            [
              "Stripe customer",
              s.stripe_customer_id ? (
                <ExtLink href={`https://dashboard.stripe.com/customers/${encodeURIComponent(s.stripe_customer_id)}`} className="mono text-[11.5px]">
                  {s.stripe_customer_id}
                </ExtLink>
              ) : (
                <None />
              ),
            ],
            [
              "Subscription",
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
  const a = w.ai;
  const m = a.month;
  return (
    <Section
      title="AI usage"
      actions={
        <ActionButton perm="workspaces.limits" size="xs" onClick={onGrant}>
          Grant AI usage
        </ActionButton>
      }
      flush
    >
      <div className="grid grid-cols-2 gap-px border-b border-line-soft bg-line-soft sm:grid-cols-4">
        {[
          ["Remaining", a.unlimited ? "Unlimited" : usd(a.remaining_usd)],
          ["Monthly allowance", a.unlimited ? "Unlimited" : usd(a.monthly_usd)],
          ["Allowance used", usd(a.included_used_usd)],
          ["Purchased left", usd(a.purchased_usd)],
        ].map(([k, v]) => (
          <div key={k} className="bg-surface px-4 py-3">
            <div className="label-caps">{k}</div>
            <div className="mono mt-1.5 text-[15px] font-medium tabular-nums">{v}</div>
          </div>
        ))}
      </div>
      <div className="flex items-center gap-2 px-4 pt-3 text-[11.5px] text-fg-3">
        This period, {shortDate(a.period_start)} to {shortDate(a.period_end)}
      </div>
      {m.by_feature.length ? (
        <Table>
          <THead>
            <TH>Feature</TH>
            <TH align="right">Calls</TH>
            <TH align="right">Charged</TH>
            <TH align="right">Provider cost</TH>
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
              <TD>Total</TD>
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
        <p className="px-4 pb-4 pt-2 text-[12.5px] text-fg-3">No AI calls this period.</p>
      )}
    </Section>
  );
}

function actorOf(a: AuditLog) {
  if (a.data?.actor === "instance_admin") return <Badge tone="indigo">Instance admin</Badge>;
  if (a.actor.type === "user" && a.actor.user)
    return (
      <Link to="/users/$id" params={{ id: a.actor.user.id }} className="truncate hover:underline">
        {a.actor.user.name || a.actor.user.email}
      </Link>
    );
  if (a.actor.type === "api_key") return <span className="text-fg-2">API key</span>;
  return <span className="text-fg-2">System</span>;
}

function RecentAudit({ items }: { items: AuditLog[] }) {
  return (
    <Section title="Workspace audit log" count={items.length ? `last ${items.length}` : undefined} flush>
      {items.length ? (
        <Table>
          <THead>
            <TH>Who</TH>
            <TH>Action</TH>
            <TH>Details</TH>
            <TH>When</TH>
          </THead>
          <tbody>
            {items.map((a) => (
              <TR key={a.id}>
                <TD className="max-w-[180px]">{actorOf(a)}</TD>
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
        <EmptyState compact art="audit" title="Nothing logged yet" description="Changes to settings, members and keys appear here." />
      )}
    </Section>
  );
}

type LimitKey = keyof AdminLimits;
const LIMIT_FIELDS: { key: LimitKey; label: string; hint: string; decimal?: boolean; suffix?: string; max: number }[] = [
  { key: "max_mailboxes", label: "Mailboxes", hint: "0 is unlimited.", max: 100_000 },
  { key: "max_members", label: "Members", hint: "0 is unlimited.", max: 100_000 },
  { key: "monthly_ai_usd", label: "Monthly AI allowance", hint: "US dollars of AI usage included each month.", decimal: true, suffix: "USD", max: 100_000 },
];

function LimitsDialog({ w, billing, onClose, onSaved }: { w: AdminWorkspace; billing: boolean; onClose: () => void; onSaved: (w: AdminWorkspace) => void }) {
  const [draft, setDraft] = useState<AdminLimits>({ ...w.limits });
  const [valid, setValid] = useState<Record<LimitKey, boolean>>({ max_mailboxes: true, max_members: true, monthly_ai_usd: true });
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
      toast.success("Limits saved");
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
      title="Limit overrides"
      description={`Overrides replace the plan's limits for ${w.name} on every plan, and survive plan changes.`}
      footer={
        <>
          <Button variant="ghost" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" loading={busy} disabled={!changed || !ok} onClick={() => void save()}>
            Save overrides
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        {!billing && <Notice kind="info">Billing is off on this instance, so every limit is already unlimited. Overrides are kept but have no effect.</Notice>}
        {LIMIT_FIELDS.map((f) => {
          const v = draft[f.key];
          const useDefault = v === null;
          return (
            <div key={f.key} className="flex flex-col gap-2 rounded-lg border border-line-soft p-3">
              <div className="flex items-center justify-between gap-3">
                <span className="text-[12.5px] font-medium">{f.label}</span>
                <label className="flex items-center gap-2 text-[12px] text-fg-2">
                  Use the plan's
                  <Switch
                    checked={useDefault}
                    aria-label={`Use the plan's ${f.label.toLowerCase()}`}
                    onCheckedChange={(on) => {
                      setDraft((d) => ({ ...d, [f.key]: on ? null : (w.limits[f.key] ?? planValue(f.key)) }));
                      setValid((s) => ({ ...s, [f.key]: true }));
                    }}
                  />
                </label>
              </div>
              {useDefault ? (
                <p className="text-[11.5px] text-fg-3">The plan's limit applies.</p>
              ) : (
                <Field hint={f.hint}>
                  {(p) => (
                    <NumberInput
                      {...p}
                      aria-label={f.label}
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
      toast.success(`Granted ${usd(amount)} of AI usage`);
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
      title="Grant AI usage"
      description={`Adds purchased AI usage to ${w.name}. It is spent after the monthly allowance, like a top-up, and never expires.`}
      size="sm"
      footer={
        <>
          <Button variant="ghost" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" loading={busy} disabled={!ok} onClick={() => void save()}>
            Grant {amountOk ? usd(amount) : ""}
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
        <Field label="Amount" hint="From $0.01 to $10,000.">
          {(p) => <NumberInput {...p} value={amount} onChange={setAmount} onValidChange={setAmountOk} min={0.01} max={10_000} decimal suffix="USD" />}
        </Field>
        <Field label="Note" hint={`${note.length} / 200. Kept in the audit log.`} error={note.length > 200 ? "Keep it under 200 characters." : undefined}>
          {(p) => <TextInput {...p} autoFocus value={note} onChange={(e) => setNote(e.target.value)} placeholder="Why, and any ticket it relates to" />}
        </Field>
        {error && <Notice>{error}</Notice>}
        <button type="submit" hidden aria-hidden tabIndex={-1} />
      </form>
    </Dialog>
  );
}
