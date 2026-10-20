// Test workspaces (contracts/admin.md "Test workspaces"): workspaces made for
// demos and testing, oldest first, with their owner, plan, sample data and when
// the cleanup deletes them. The page creates them (with a test account whose
// generated password is shown once), adds sample data, extends them, renews the
// owner's password and deletes them. A batch that is still running refreshes
// the list every few seconds until it finishes.

import { keepPreviousData, useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { CalendarClock, Database, ExternalLink, FlaskConical, KeyRound, MoreHorizontal, Plus, Trash2, TriangleAlert, X } from "lucide-react";
import { useState, type ReactNode } from "react";
import { ActionButton, ActionMenuItem } from "@/components/ActionButton";
import { ErrorState } from "@/components/ErrorState";
import { Notice, PageBody, PageHeader } from "@/components/layout";
import { PlanPill, SuspendedPill, TestPill } from "@/components/pills";
import { StepUpDialog } from "@/components/StepUpDialog";
import { LoadMore, Table, TableSkeleton, TD, TH, THead, TR } from "@/components/table";
import { Time } from "@/components/Time";
import { Avatar } from "@/components/ui/avatar";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { CopyField } from "@/components/ui/copy-button";
import { Dialog } from "@/components/ui/dialog";
import { EmptyState, GhostList } from "@/components/ui/empty";
import { Field, NumberInput, SearchInput, Textarea, TextInput } from "@/components/ui/input";
import { Menu, MenuContent, MenuItem, MenuSeparator, MenuTrigger } from "@/components/ui/menu";
import { Select } from "@/components/ui/select";
import { Spinner } from "@/components/ui/spinner";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast";
import { Tooltip } from "@/components/ui/tooltip";
import { errorMessage, isApiError } from "@/lib/api/client";
import { api } from "@/lib/api/endpoints";
import type { AdminSampleData, AdminTestWorkspace, AdminUserRow, SampleKind, SampleSize, TestPlan, TestWorkspaceResult } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { fullDate, number, shortDate } from "@/lib/format";
import { q, qk } from "@/lib/queries";
import { useSession } from "@/lib/session";
import {
  ALL_KINDS,
  batchLine,
  countsLine,
  DELETE_CHOICES,
  deleteAfterDays,
  expiryLabel,
  isRunning,
  isTestEmail,
  kindAdds,
  KINDS,
  orderKinds,
  passwordLabel,
  PLANS,
  sampleLine,
  SIZES,
  suggestEmail,
  type DeleteChoice,
} from "@/lib/testWorkspaces";

type Dialogs = { kind: "create" } | { kind: "add" | "change" | "delete"; w: AdminTestWorkspace } | null;
type Result = TestWorkspaceResult & { renewed?: boolean };

export default function TestWorkspaces() {
  const qc = useQueryClient();
  const billing = useSession((s) => s.me?.instance.billing_enabled ?? false);
  const [dialog, setDialog] = useState<Dialogs>(null);
  const [result, setResult] = useState<Result | null>(null);
  const list = useInfiniteQuery({
    ...q.testWorkspaces(),
    placeholderData: keepPreviousData,
    // A batch of sample data runs in the background: refresh until none is.
    refetchInterval: (query) => (query.state.data?.pages.some((p) => p.data.some((w) => isRunning(w.sample_data))) ? 3000 : false),
  });
  const rows = list.data?.pages.flatMap((p) => p.data) ?? [];

  const changed = (wid?: string) => {
    void qc.invalidateQueries({ queryKey: qk.testWorkspaces });
    void qc.invalidateQueries({ queryKey: qk.workspaces });
    void qc.invalidateQueries({ queryKey: qk.summary });
    if (wid) void qc.invalidateQueries({ queryKey: qk.workspace(wid) });
  };

  const create = (
    <ActionButton perm="workspaces.test" variant="primary" icon={<Plus />} onClick={() => setDialog({ kind: "create" })}>
      Create test workspace
    </ActionButton>
  );

  return (
    <>
      <PageHeader title="Test workspaces" subtitle={rows.length ? number(rows.length) : undefined} actions={create} />
      <PageBody>
        {result && (
          <div className="border-b border-line-soft px-4 py-4 md:px-6">
            <ResultBanner r={result} running={!!rows.find((w) => w.workspace.id === result.workspace.workspace.id && isRunning(w.sample_data))} onDismiss={() => setResult(null)} />
          </div>
        )}
        {list.isPending ? (
          <TableSkeleton cols={6} />
        ) : list.isError && !rows.length ? (
          <ErrorState error={list.error} onRetry={() => void list.refetch()} />
        ) : !rows.length ? (
          <EmptyState
            art="workspaces"
            ghost={<GhostList />}
            title="No test workspaces"
            description="Make one to demo opendesk with realistic data, or to try something without touching a customer's workspace. It is deleted on the day you choose."
            action={create}
            tips={[{ label: "Owners get a test account with a password that expires", icon: <KeyRound /> }, { label: "Test workspaces stay out of the overview's counts", icon: <FlaskConical /> }]}
          />
        ) : (
          <>
            <Table className={list.isPlaceholderData ? "opacity-60 transition-opacity" : undefined}>
              <THead>
                <TH>Workspace</TH>
                <TH>Owner</TH>
                <TH>Plan</TH>
                <TH>Sample data</TH>
                <TH>Deleted</TH>
                <TH>Created</TH>
                <TH className="w-10" />
              </THead>
              <tbody>
                {rows.map((w) => (
                  <Row key={w.workspace.id} w={w} billing={billing} open={(kind) => setDialog({ kind, w })} />
                ))}
              </tbody>
            </Table>
            <LoadMore shown={rows.length} hasMore={!!list.hasNextPage} loading={list.isFetchingNextPage} onLoad={() => void list.fetchNextPage()} />
          </>
        )}
      </PageBody>

      {dialog?.kind === "create" && (
        <CreateDialog
          billing={billing}
          onClose={() => setDialog(null)}
          onCreated={(r) => {
            setResult(r);
            changed(r.workspace.workspace.id);
            toast.success(`${r.workspace.workspace.name} is ready`);
          }}
        />
      )}
      {dialog?.kind === "add" && (
        <AddDataDialog
          w={dialog.w}
          onClose={() => setDialog(null)}
          onAdded={() => changed(dialog.w.workspace.id)}
        />
      )}
      {dialog?.kind === "change" && (
        <ChangeDialog
          w={dialog.w}
          onClose={() => setDialog(null)}
          onChanged={(r, renewed) => {
            changed(dialog.w.workspace.id);
            if (renewed) setResult({ ...r, renewed: true });
          }}
        />
      )}
      {dialog?.kind === "delete" && (
        <DeleteDialog
          w={dialog.w}
          onClose={() => setDialog(null)}
          onDeleted={() => {
            qc.removeQueries({ queryKey: qk.workspace(dialog.w.workspace.id) });
            changed();
            if (result?.workspace.workspace.id === dialog.w.workspace.id) setResult(null);
          }}
        />
      )}
    </>
  );
}

function Row({ w, billing, open }: { w: AdminTestWorkspace; billing: boolean; open: (kind: "add" | "change" | "delete") => void }) {
  const navigate = useNavigate();
  const exp = expiryLabel(w.test_expires_at);
  const pw = w.owner?.test_account ? passwordLabel(w.owner.password_expires_at) : null;
  return (
    <TR>
      <TD>
        <div className="flex min-w-0 items-center gap-2.5">
          <Avatar name={w.workspace.name} square size="md" />
          <div className="min-w-0">
            <span className="flex min-w-0 items-center gap-1.5">
              <Link to="/workspaces/$id" params={{ id: w.workspace.id }} className="truncate font-medium text-fg hover:underline">
                {w.workspace.name}
              </Link>
              <TestPill />
              {w.suspended_at && <SuspendedPill />}
            </span>
            <span className="block max-w-[280px] truncate text-[11px] text-fg-4" title={w.reason}>
              {w.reason}
            </span>
          </div>
        </div>
      </TD>
      <TD>
        {w.owner ? (
          <div className="min-w-0">
            <Link to="/users/$id" params={{ id: w.owner.id }} className="mono block max-w-[240px] truncate text-[12px] text-fg hover:underline">
              {w.owner.email}
            </Link>
            <span className="mt-0.5 flex items-center gap-1.5">
              {w.owner.test_account ? <Badge tone="outline">Test account</Badge> : <span className="text-[11px] text-fg-3">Own account</span>}
              {pw && <span className={cn("text-[11px]", pw.tone === "red" ? "text-danger-fg" : pw.tone === "amber" ? "text-tone-amber" : "text-fg-3")}>{pw.label}</span>}
            </span>
          </div>
        ) : (
          <span className="text-fg-3">No owner</span>
        )}
      </TD>
      <TD>
        <span className="flex items-center gap-1.5">
          <PlanPill plan={w.plan} />
          {!billing && w.plan !== "free" && (
            <Tooltip content="Billing is off: every workspace is unlimited and the plan is only recorded">
              <span tabIndex={0} className="text-[11px] text-fg-4">
                recorded
              </span>
            </Tooltip>
          )}
        </span>
      </TD>
      <TD>
        <SampleStatus s={w.sample_data} />
      </TD>
      <TD>
        <Tooltip content={w.test_expires_at ? fullDate(w.test_expires_at) : "Kept until someone deletes it"}>
          <span tabIndex={0} className="inline-flex outline-none">
            <Badge tone={exp.tone}>{exp.label}</Badge>
          </span>
        </Tooltip>
      </TD>
      <TD className="text-fg-3">
        <Time iso={w.created_at} />
        {w.created_by && <span className="block max-w-[180px] truncate text-[11px] text-fg-4">by {w.created_by.email}</span>}
      </TD>
      <TD align="right">
        <Menu>
          <MenuTrigger asChild>
            <Button variant="ghost" square size="sm" aria-label={`Actions for ${w.workspace.name}`} icon={<MoreHorizontal />} />
          </MenuTrigger>
          <MenuContent align="end" className="w-[240px]">
            <MenuItem icon={<ExternalLink />} onSelect={() => void navigate({ to: "/workspaces/$id", params: { id: w.workspace.id } })}>
              Open workspace
            </MenuItem>
            <ActionMenuItem perm="workspaces.test" icon={<Database />} blocked={isRunning(w.sample_data) ? "Sample data is still being added" : null} onSelect={() => open("add")}>
              Add sample data
            </ActionMenuItem>
            <ActionMenuItem perm="workspaces.test" icon={<CalendarClock />} onSelect={() => open("change")}>
              {w.owner?.test_account ? "Extend or renew password" : "Change when it is deleted"}
            </ActionMenuItem>
            <MenuSeparator />
            <ActionMenuItem perm="workspaces.test" icon={<Trash2 />} danger onSelect={() => open("delete")}>
              Delete workspace
            </ActionMenuItem>
          </MenuContent>
        </Menu>
      </TD>
    </TR>
  );
}

function SampleStatus({ s }: { s: AdminSampleData }) {
  if (s.status === "none") return <span className="text-[12px] text-fg-4">None</span>;
  if (s.status === "running")
    return (
      <span className="flex items-center gap-1.5 text-[12px] text-tone-violet">
        <Spinner className="size-3.5" />
        {sampleLine(s)}
      </span>
    );
  const all = countsLine(s.counts);
  return (
    <div className="min-w-0">
      <span className="flex items-center gap-1.5">
        {s.status === "failed" ? <Badge tone="red">Failed</Badge> : <Badge tone="green">Added</Badge>}
        {s.batches > 1 && <span className="text-[11px] text-fg-4">{number(s.batches)} batches</span>}
      </span>
      <Tooltip content={s.status === "failed" ? s.error : all}>
        <span tabIndex={0} className="mt-0.5 block max-w-[260px] truncate text-[11px] text-fg-3 outline-none">
          {s.status === "failed" ? s.error || "The last batch failed." : sampleLine(s)}
        </span>
      </Tooltip>
    </div>
  );
}

/** The owner's credentials, once: the password is never shown again. */
function ResultBanner({ r, running, onDismiss }: { r: Result; running: boolean; onDismiss: () => void }) {
  const ws = r.workspace.workspace;
  return (
    <div role="status" className="animate-rise-in rounded-lg border border-line bg-surface shadow-xs motion-reduce:animate-none">
      <div className="flex items-start gap-3 border-b border-line-soft px-4 py-3">
        <span className="mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-md bg-tone-violet-soft text-tone-violet">
          <FlaskConical className="size-3.5" />
        </span>
        <div className="min-w-0 flex-1">
          <p className="text-[13px] font-medium text-fg">{r.renewed ? `New password for ${ws.name}` : `${ws.name} is ready`}</p>
          <p className="mt-0.5 text-[12px] text-fg-3">
            {r.owner.password ? "Sign in to the app with these details." : "The owner signs in with their own account and password."}
            {running && " Sample data is being added and shows up as it lands."}
          </p>
        </div>
        <Button variant="ghost" square size="sm" aria-label="Dismiss" icon={<X />} onClick={onDismiss} />
      </div>
      <div className="flex flex-col gap-3 px-4 py-3">
        {r.owner.password && (
          <div className="flex items-start gap-2 rounded-md border border-tone-amber-line bg-tone-amber-soft px-3 py-2 text-[12.5px] text-fg">
            <TriangleAlert className="mt-0.5 size-3.5 shrink-0 text-tone-amber" aria-hidden />
            <span>
              Copy the password now: it is shown only once
              {r.owner.password_expires_at ? <> and stops working on {shortDate(r.owner.password_expires_at)}</> : null}. If it is lost, renew it from this page.
            </span>
          </div>
        )}
        <dl className="grid gap-x-4 gap-y-2.5 text-[12.5px] sm:grid-cols-[110px_minmax(0,1fr)] sm:items-center">
          <dt className="text-fg-3">Sign in</dt>
          <dd className="min-w-0">
            <CopyField value={r.sign_in_url} />
          </dd>
          <dt className="text-fg-3">Email</dt>
          <dd className="min-w-0">
            <CopyField value={r.owner.email} />
          </dd>
          {r.owner.password && (
            <>
              <dt className="text-fg-3">Password</dt>
              <dd className="min-w-0 ph-no-capture" data-ph-mask>
                <CopyField value={r.owner.password} />
              </dd>
            </>
          )}
          {r.owner.password_expires_at && (
            <>
              <dt className="text-fg-3">Password expires</dt>
              <dd className="text-fg">{fullDate(r.owner.password_expires_at)}</dd>
            </>
          )}
          <dt className="text-fg-3">Workspace</dt>
          <dd className="text-fg">
            {r.workspace.test_expires_at ? <>Deleted on {fullDate(r.workspace.test_expires_at)}</> : "Kept until someone deletes it"}
          </dd>
        </dl>
        <div className="flex items-center gap-2">
          <Button variant="secondary" asChild icon={<ExternalLink />}>
            <a href={r.sign_in_url} target="_blank" rel="noopener noreferrer">
              Open sign-in page
            </a>
          </Button>
          <Button variant="ghost" onClick={onDismiss}>
            Done
          </Button>
        </div>
      </div>
    </div>
  );
}

// ─── Sample data fields ────────────────────────────────────────────────────

function SampleFields({ size, setSize, kinds, setKinds }: { size: SampleSize; setSize: (s: SampleSize) => void; kinds: SampleKind[]; setKinds: (k: SampleKind[]) => void }) {
  const toggle = (k: SampleKind, on: boolean) => setKinds(orderKinds(on ? [...kinds, k] : kinds.filter((x) => x !== k)));
  const chosen = KINDS.filter((k) => kinds.includes(k.value));
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-col gap-1.5">
        <span className="text-[12.5px] font-medium text-fg" id="sample-size">
          Size
        </span>
        <div role="radiogroup" aria-labelledby="sample-size" className="grid grid-cols-3 gap-1.5">
          {SIZES.map((s) => (
            <button
              key={s.value}
              type="button"
              role="radio"
              aria-checked={size === s.value}
              onClick={() => setSize(s.value)}
              className={cn(
                "flex flex-col items-start gap-0.5 rounded-md border px-2.5 py-2 text-left outline-none transition-[border-color,background-color,box-shadow] duration-150 focus-visible:shadow-[0_0_0_3px_var(--focus)]",
                size === s.value ? "border-fg bg-sunken" : "border-line hover:border-line-strong",
              )}
            >
              <span className="text-[12.5px] font-medium text-fg">{s.label}</span>
              <span className="text-[11px] leading-4 text-fg-3">{s.hint}</span>
            </button>
          ))}
        </div>
      </div>
      <fieldset className="flex flex-col gap-1.5">
        <legend className="mb-1.5 flex w-full items-center text-[12.5px] font-medium text-fg">
          Kinds
          <button
            type="button"
            className="ml-auto text-[11.5px] font-normal text-fg-3 underline decoration-line-strong underline-offset-2 hover:text-fg"
            onClick={() => setKinds(kinds.length === ALL_KINDS.length ? [] : ALL_KINDS)}
          >
            {kinds.length === ALL_KINDS.length ? "Clear all" : "Choose all"}
          </button>
        </legend>
        <div className="grid gap-1 sm:grid-cols-2">
          {KINDS.map((k) => (
            <label key={k.value} className="flex cursor-pointer items-center gap-2.5 rounded-md px-2 py-1.5 text-[12.5px] text-fg hover:bg-hover">
              <Checkbox checked={kinds.includes(k.value)} onCheckedChange={(on) => toggle(k.value, on)} aria-label={k.label} />
              {k.label}
            </label>
          ))}
        </div>
      </fieldset>
      <div className="rounded-md border border-line bg-sunken px-3 py-2.5">
        <p className="label-caps">What will be added</p>
        {chosen.length ? (
          <ul className="mt-1.5 flex flex-col gap-1 text-[12px] leading-[18px] text-fg-2">
            <li className="text-fg">{SIZES.find((s) => s.value === size)!.hint}, in one batch:</li>
            {chosen.map((k) => (
              <li key={k.value} className="flex gap-2">
                <span aria-hidden className="mt-[7px] size-1 shrink-0 rounded-full bg-fg-4" />
                <span>
                  <span className="font-medium text-fg">{k.label}.</span> {kindAdds(k.value, size)}.
                </span>
              </li>
            ))}
            <li className="mt-1 text-[11.5px] text-fg-3">Every address is on the reserved .invalid domain and nothing is sent, emailed or charged.</li>
          </ul>
        ) : (
          <p className="mt-1 text-[12px] text-fg-3">Choose at least one kind.</p>
        )}
      </div>
    </div>
  );
}

// ─── Create ────────────────────────────────────────────────────────────────

function CreateDialog({ billing, onClose, onCreated }: { billing: boolean; onClose: () => void; onCreated: (r: TestWorkspaceResult) => void }) {
  const [name, setName] = useState("");
  const [mode, setMode] = useState<"new" | "existing">("new");
  const [email, setEmail] = useState("");
  const [emailEdited, setEmailEdited] = useState(false);
  const [ownerName, setOwnerName] = useState("");
  const [passwordDays, setPasswordDays] = useState(1);
  const [user, setUser] = useState<AdminUserRow | null>(null);
  const [plan, setPlan] = useState<TestPlan>("pro");
  const [withSample, setWithSample] = useState(true);
  const [size, setSize] = useState<SampleSize>("realistic");
  const [kinds, setKinds] = useState<SampleKind[]>(ALL_KINDS);
  const [deleteChoice, setDeleteChoice] = useState<DeleteChoice>("30");
  const [reason, setReason] = useState("");

  const ownerEmail = emailEdited ? email : name.trim() ? suggestEmail(name, seeded(name)) : "";
  const emailError = mode === "new" && ownerEmail && !isTestEmail(ownerEmail) ? "A new test account's address ends in .invalid, which never receives mail." : undefined;
  const ready =
    !!name.trim() &&
    !!reason.trim() &&
    reason.length <= 500 &&
    (mode === "new" ? isTestEmail(ownerEmail) : !!user) &&
    (!withSample || kinds.length > 0) &&
    passwordDays >= 1 &&
    passwordDays <= 2;

  return (
    <StepUpDialog
      open
      onOpenChange={(o) => !o && onClose()}
      size="lg"
      title="Create test workspace"
      description="A workspace for demos and testing, made like any other and flagged as a test. It stays out of the overview's counts and is deleted on the day you choose."
      confirmLabel="Create test workspace"
      ready={ready}
      explain={(e) =>
        isApiError(e, "test_email_required")
          ? "A new test account needs an address ending in .invalid. To give a real person a test workspace, choose their existing account."
          : isApiError(e, "account_suspended")
            ? "That account is suspended."
            : undefined
      }
      onConfirm={async (code) => {
        const r = await api.testWorkspaces.create({
          code,
          name: name.trim(),
          owner: mode === "new" ? { email: ownerEmail.trim(), ...(ownerName.trim() ? { name: ownerName.trim() } : {}) } : { user_id: user!.id },
          ...(mode === "new" ? { password_days: passwordDays } : {}),
          plan,
          ...(withSample ? { sample: { size, kinds } } : {}),
          delete_after_days: deleteAfterDays(deleteChoice),
          reason: reason.trim(),
        });
        onCreated(r);
      }}
    >
      <Field label="Workspace name">
        {(p) => <TextInput {...p} autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="Acme demo" maxLength={200} autoComplete="off" />}
      </Field>

      <div className="flex flex-col gap-2">
        <span className="text-[12.5px] font-medium text-fg" id="owner-mode">
          Owner
        </span>
        <div role="radiogroup" aria-labelledby="owner-mode" className="inline-flex self-start rounded-md bg-sunken p-0.5">
          {(
            [
              ["new", "New test account"],
              ["existing", "Existing account"],
            ] as const
          ).map(([v, label]) => (
            <button
              key={v}
              type="button"
              role="radio"
              aria-checked={mode === v}
              onClick={() => setMode(v)}
              className={cn(
                "h-6 rounded-[5px] px-2.5 text-[12px] font-medium outline-none transition-colors focus-visible:shadow-[0_0_0_2px_var(--focus)]",
                mode === v ? "bg-surface text-fg shadow-xs" : "text-fg-3 hover:text-fg",
              )}
            >
              {label}
            </button>
          ))}
        </div>
        {mode === "new" ? (
          <div className="grid gap-3 rounded-md border border-line-soft p-3 sm:grid-cols-2">
            <Field label="Email" className="sm:col-span-2" error={emailError} hint="A reserved .invalid address: the account is confirmed and nothing is ever sent to it.">
              {(p) => (
                <TextInput
                  {...p}
                  className="mono"
                  value={ownerEmail}
                  onChange={(e) => {
                    setEmailEdited(true);
                    setEmail(e.target.value);
                  }}
                  placeholder="owner@acme-demo.invalid"
                  autoComplete="off"
                  spellCheck={false}
                />
              )}
            </Field>
            <Field label="Name" optional>
              {(p) => <TextInput {...p} value={ownerName} onChange={(e) => setOwnerName(e.target.value)} placeholder="Alex Morgan" maxLength={200} autoComplete="off" />}
            </Field>
            <Field label="Password lasts" hint="A strong password is generated and shown once. It works for 1 or 2 days.">
              {(p) => <NumberInput id={p.id} aria-describedby={p["aria-describedby"]} value={passwordDays} onChange={setPasswordDays} min={1} max={2} suffix="days" />}
            </Field>
          </div>
        ) : (
          <UserPicker user={user} onChange={setUser} />
        )}
      </div>

      <Field label="Plan" hint={billing ? PLANS.find((p) => p.value === plan)!.hint : "Billing is off on this instance: every workspace is unlimited and the plan is only recorded."}>
        {(p) => <Select id={p.id} aria-label="Plan" value={plan} onChange={setPlan} options={PLANS.map((x) => ({ value: x.value, label: x.label }))} />}
      </Field>

      <div className="flex flex-col gap-3 rounded-md border border-line-soft p-3">
        <label className="flex cursor-pointer items-center justify-between gap-3">
          <span>
            <span className="block text-[12.5px] font-medium text-fg">Add sample data</span>
            <span className="block text-[11.5px] text-fg-3">Added in the background once the workspace exists.</span>
          </span>
          <Switch checked={withSample} onCheckedChange={setWithSample} aria-label="Add sample data" />
        </label>
        {withSample && <SampleFields size={size} setSize={setSize} kinds={kinds} setKinds={setKinds} />}
      </div>

      <Field label="Delete the workspace">
        {(p) => <Select id={p.id} aria-label="Delete the workspace" value={deleteChoice} onChange={setDeleteChoice} options={DELETE_CHOICES} />}
      </Field>

      <Field label="Reason" hint={`${reason.length} / 500. Kept in the admin audit log.`} error={reason.length > 500 ? "Keep it under 500 characters." : undefined}>
        {(p) => <Textarea {...p} value={reason} onChange={(e) => setReason(e.target.value)} placeholder="Sales demo for Acme on Thursday" rows={2} />}
      </Field>
    </StepUpDialog>
  );
}

/** A stable random source per name, so the suggested address doesn't change on every keystroke's render. */
function seeded(name: string) {
  let h = 2166136261;
  for (const c of name) h = Math.imul(h ^ c.charCodeAt(0), 16777619);
  return () => {
    h = Math.imul(h ^ (h >>> 15), 2246822507) >>> 0;
    return (h % 10_000) / 10_000;
  };
}

function UserPicker({ user, onChange }: { user: AdminUserRow | null; onChange: (u: AdminUserRow | null) => void }) {
  const [term, setTerm] = useState("");
  const t = term.trim();
  const found = useQuery({ queryKey: [...qk.users, "pick", t], queryFn: () => api.users.list({ q: t, limit: 6 }), enabled: t.length >= 2, placeholderData: keepPreviousData });
  if (user)
    return (
      <div className="flex items-center gap-2.5 rounded-md border border-line-soft px-3 py-2">
        <Avatar name={user.name || user.email} size="md" />
        <span className="min-w-0 flex-1">
          <span className="block truncate text-[12.5px] font-medium text-fg">{user.name || user.email}</span>
          <span className="mono block truncate text-[11.5px] text-fg-3">{user.email}</span>
        </span>
        <Button variant="ghost" size="xs" onClick={() => onChange(null)}>
          Change
        </Button>
      </div>
    );
  const rows = t.length >= 2 ? (found.data?.data ?? []) : [];
  return (
    <div className="flex flex-col gap-1.5 rounded-md border border-line-soft p-3">
      <SearchInput value={term} onChange={setTerm} placeholder="Search accounts by email, name or id" inputSize="md" />
      <p className="text-[11.5px] text-fg-3">They own the new workspace and sign in with their own password; nothing about their account changes.</p>
      {t.length >= 2 && (
        <ul className="flex flex-col" aria-label="Accounts">
          {rows.map((u) => (
            <li key={u.id}>
              <button
                type="button"
                disabled={!!u.suspended_at}
                onClick={() => onChange(u)}
                className="flex w-full items-center gap-2.5 rounded-md px-2 py-1.5 text-left outline-none hover:bg-hover focus-visible:bg-hover disabled:opacity-50"
              >
                <Avatar name={u.name || u.email} size="sm" />
                <span className="min-w-0 flex-1 truncate text-[12.5px] text-fg">{u.name || u.email}</span>
                <span className="mono truncate text-[11.5px] text-fg-3">{u.email}</span>
                {u.suspended_at && <SuspendedPill />}
              </button>
            </li>
          ))}
          {!found.isFetching && !rows.length && <li className="px-2 py-1.5 text-[12px] text-fg-3">No account matches.</li>}
        </ul>
      )}
    </div>
  );
}

// ─── Add data, change, delete ──────────────────────────────────────────────

function AddDataDialog({ w, onClose, onAdded }: { w: AdminTestWorkspace; onClose: () => void; onAdded: () => void }) {
  const last = w.sample_data;
  const [size, setSize] = useState<SampleSize>(last.size || "small");
  const [kinds, setKinds] = useState<SampleKind[]>(last.kinds.length ? last.kinds : ALL_KINDS);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const add = async () => {
    if (busy || !kinds.length) return;
    setBusy(true);
    setError(null);
    try {
      await api.testWorkspaces.addSample(w.workspace.id, { size, kinds });
      onAdded();
      toast.success(`${batchLine(size, kinds)} is on its way`);
      onClose();
    } catch (e) {
      setBusy(false);
      setError(isApiError(e, "seeding") ? "Sample data is still being added. Try again when it finishes." : errorMessage(e));
    }
  };
  return (
    <Dialog
      open
      onOpenChange={(o) => !o && !busy && onClose()}
      title={`Add sample data to ${w.workspace.name}`}
      description={last.batches ? `Another batch, on top of what is there${last.counts ? ` (${countsLine(last.counts, 3)})` : ""}.` : "Added in the background; the list shows when it is done."}
      size="md"
      footer={
        <>
          <Button variant="ghost" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" loading={busy} disabled={!kinds.length} onClick={() => void add()}>
            Add sample data
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <SampleFields size={size} setSize={setSize} kinds={kinds} setKinds={setKinds} />
        {error && <Notice>{error}</Notice>}
      </div>
    </Dialog>
  );
}

function ChangeDialog({ w, onClose, onChanged }: { w: AdminTestWorkspace; onClose: () => void; onChanged: (r: TestWorkspaceResult, renewed: boolean) => void }) {
  type Choice = DeleteChoice | "same";
  const [choice, setChoice] = useState<Choice>("same");
  const [renew, setRenew] = useState(false);
  const [days, setDays] = useState(1);
  const testAccount = !!w.owner?.test_account;
  const current = w.test_expires_at ? `Keep the current day (${shortDate(w.test_expires_at)})` : "Keep it until deleted (as now)";
  const options: { value: Choice; label: string }[] = [
    { value: "same", label: current },
    ...DELETE_CHOICES.map((c) => ({ value: c.value as Choice, label: c.value === "keep" ? c.label : `${c.label}, from today` })),
  ];
  const ready = choice !== "same" || (renew && days >= 1 && days <= 2);
  return (
    <StepUpDialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={testAccount ? "Extend or renew password" : "Change when it is deleted"}
      description={
        <>
          <span className="font-medium text-fg">{w.workspace.name}</span>{" "}
          {w.test_expires_at ? <>is deleted on {fullDate(w.test_expires_at)}.</> : "is kept until someone deletes it."}
        </>
      }
      confirmLabel="Save"
      ready={ready}
      explain={(e) => (isApiError(e, "not_test_account") ? "The owner has their own account; only a test account's password is renewed." : undefined)}
      onConfirm={async (code) => {
        const r = await api.testWorkspaces.change(w.workspace.id, {
          code,
          ...(choice !== "same" ? { delete_after_days: deleteAfterDays(choice) } : {}),
          ...(renew ? { password_days: days } : {}),
        });
        toast.success(renew ? "New password ready" : "Saved");
        onChanged(r, renew);
      }}
    >
      <Field label="Delete the workspace">
        {(p) => <Select id={p.id} aria-label="Delete the workspace" value={choice} onChange={setChoice} options={options} />}
      </Field>
      {testAccount ? (
        <div className="flex flex-col gap-3 rounded-md border border-line-soft p-3">
          <label className="flex cursor-pointer items-center justify-between gap-3">
            <span>
              <span className="block text-[12.5px] font-medium text-fg">Renew the owner's password</span>
              <span className="block text-[11.5px] text-fg-3">
                A new password for <span className="mono">{w.owner!.email}</span>, shown once. The old one stops working and their sessions end.
              </span>
            </span>
            <Switch checked={renew} onCheckedChange={setRenew} aria-label="Renew the owner's password" />
          </label>
          {renew && (
            <Field label="New password lasts">
              {(p) => <NumberInput id={p.id} value={days} onChange={setDays} min={1} max={2} suffix="days" />}
            </Field>
          )}
        </div>
      ) : (
        <p className="text-[11.5px] text-fg-3">The owner signs in with their own account, so there is no password to renew.</p>
      )}
    </StepUpDialog>
  );
}

function DeleteDialog({ w, onClose, onDeleted }: { w: AdminTestWorkspace; onClose: () => void; onDeleted: () => void }) {
  const [typed, setTyped] = useState("");
  const name = w.workspace.name;
  const owner: ReactNode = w.owner?.test_account ? (
    <>
      {" "}
      The test account <span className="mono text-fg">{w.owner.email}</span> goes with it unless it is in another workspace.
    </>
  ) : null;
  return (
    <StepUpDialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={`Delete ${name}?`}
      description={<>Everything in this test workspace is removed for good.{owner}</>}
      confirmLabel="Delete workspace"
      danger
      ready={typed === name}
      onConfirm={async (code) => {
        await api.workspaces.remove(w.workspace.id, code, typed);
        onDeleted();
        toast.success(`${name} was deleted`);
      }}
    >
      <Field label={<>Type <span className="mono font-medium">{name}</span> to confirm</>}>
        {(p) => <TextInput {...p} autoFocus autoComplete="off" spellCheck={false} value={typed} onChange={(e) => setTyped(e.target.value)} />}
      </Field>
    </StepUpDialog>
  );
}
