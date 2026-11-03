import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { UserPlus } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { ActionButton } from "@/components/ActionButton";
import { ErrorState } from "@/components/ErrorState";
import { PageBody, PageHeader } from "@/components/layout";
import { AdminRolePill } from "@/components/pills";
import { StepUpDialog } from "@/components/StepUpDialog";
import { Table, TableSkeleton, TD, TH, THead, TR } from "@/components/table";
import { Time } from "@/components/Time";
import { Avatar } from "@/components/ui/avatar";
import { Badge } from "@/components/ui/badge";
import { EmptyState, GhostList } from "@/components/ui/empty";
import { Field, TextInput } from "@/components/ui/input";
import { Segmented } from "@/components/ui/tabs";
import { toast } from "@/components/ui/toast";
import { i18n, inSentence } from "@/i18n";
import { isApiError } from "@/lib/api/client";
import { api } from "@/lib/api/endpoints";
import type { AdminRole, InstanceAdmin } from "@/lib/api/types";
import { q, qk } from "@/lib/queries";
import { ROLE_HINT, ROLE_LABEL, ROLES, roleRank } from "@/lib/permissions";
import { useMe } from "@/lib/session";

const EMAIL = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

function explainGrant(e: unknown): string | undefined {
  if (isApiError(e, "not_found") || isApiError(e, "user_not_found")) return i18n.t("admins:grantError.noAccount");
  if (isApiError(e, "admin_mfa_setup_required") || isApiError(e, "target_mfa_required")) return i18n.t("admins:grantError.mfa");
  if (isApiError(e, "already_admin")) return i18n.t("admins:grantError.already");
  if (isApiError(e, "account_suspended")) return i18n.t("admins:grantError.suspended");
  return undefined;
}

function RolePicker({ value, onChange }: { value: AdminRole; onChange: (r: AdminRole) => void }) {
  const { t } = useTranslation(["admins", "audit", "common"]);
  return (
    <div className="flex flex-col gap-1.5">
      <span className="text-[12.5px] font-medium">{t("col.role")}</span>
      <Segmented value={value} onChange={onChange} options={[...ROLES].reverse().map((r) => ({ value: r, label: t(`common:${ROLE_LABEL[r]}`) }))} />
      <span className="text-[12px] text-fg-3">{t(`common:${ROLE_HINT[value]}`)}</span>
    </div>
  );
}

export default function Admins() {
  const { t } = useTranslation(["admins", "audit", "common"]);
  const me = useMe();
  const qc = useQueryClient();
  const admins = useQuery(q.admins());
  const [granting, setGranting] = useState(false);
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<AdminRole>("support");
  const [editing, setEditing] = useState<{ a: InstanceAdmin; role: AdminRole } | null>(null);
  const [revoking, setRevoking] = useState<InstanceAdmin | null>(null);

  const list = [...(admins.data ?? [])].sort((a, b) => roleRank(b.role) - roleRank(a.role) || a.user.email.localeCompare(b.user.email));
  const supers = list.filter((a) => a.role === "super").length;
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: qk.admins });
    void qc.invalidateQueries({ queryKey: qk.users });
  };

  /** Why a row's admin can't be changed or revoked from here, or null. */
  const lockedReason = (a: InstanceAdmin, action: "change" | "revoke") => {
    if (a.user.id === me.id) return action === "change" ? t("locked.ownRole") : t("locked.self");
    if (a.role === "super" && supers <= 1) return t("locked.lastSuper");
    return null;
  };

  return (
    <>
      <PageHeader
        title={t("title")}
        subtitle={t("subtitle")}
        actions={
          <ActionButton perm="admins.manage" variant="primary" icon={<UserPlus />} onClick={() => setGranting(true)}>
            {t("grant.title")}
          </ActionButton>
        }
      />
      <PageBody>
        {admins.isPending ? (
          <TableSkeleton cols={5} rows={4} />
        ) : admins.isError ? (
          <ErrorState error={admins.error} onRetry={() => void admins.refetch()} />
        ) : !list.length ? (
          <EmptyState
            art="admins"
            ghost={<GhostList variant="people" />}
            title={t("empty.title")}
            description={t("empty.hint")}
          />
        ) : (
          <Table>
            <THead>
              <TH>{t("col.admin")}</TH>
              <TH>{t("col.role")}</TH>
              <TH>{t("col.grantedBy")}</TH>
              <TH>{t("col.since")}</TH>
              <TH />
            </THead>
            <tbody>
              {list.map((a) => (
                <TR key={a.user.id}>
                  <TD>
                    <Link to="/users/$id" params={{ id: a.user.id }} className="group flex min-w-0 items-center gap-2.5">
                      <Avatar name={a.user.name || a.user.email} size="md" />
                      <span className="min-w-0">
                        <span className="flex items-center gap-1.5">
                          <span className="truncate font-medium text-fg group-hover:underline">{a.user.name || a.user.email}</span>
                          {a.user.id === me.id && <Badge tone="outline">{t("you")}</Badge>}
                        </span>
                        {a.user.name && <span className="block truncate text-[11.5px] text-fg-3">{a.user.email}</span>}
                      </span>
                    </Link>
                  </TD>
                  <TD>
                    <AdminRolePill role={a.role} />
                  </TD>
                  <TD className="text-fg-2">{a.granted_by ? a.granted_by.email : <Badge tone="outline">{t("audit:actor.cli")}</Badge>}</TD>
                  <TD className="text-fg-3">
                    <Time iso={a.created_at} />
                  </TD>
                  <TD align="right">
                    <div className="flex items-center justify-end gap-1">
                      <ActionButton perm="admins.manage" size="xs" variant="secondary" blocked={lockedReason(a, "change")} onClick={() => setEditing({ a, role: a.role })}>
                        {t("change.button")}
                      </ActionButton>
                      <ActionButton perm="admins.manage" size="xs" variant="danger" blocked={lockedReason(a, "revoke")} onClick={() => setRevoking(a)}>
                        {t("revoke.button")}
                      </ActionButton>
                    </div>
                  </TD>
                </TR>
              ))}
            </tbody>
          </Table>
        )}
      </PageBody>

      <StepUpDialog
        open={granting}
        onOpenChange={(o) => {
          setGranting(o);
          if (!o) {
            setEmail("");
            setRole("support");
          }
        }}
        title={t("grant.title")}
        description={t("grant.description")}
        confirmLabel={t("grant.confirm")}
        ready={EMAIL.test(email.trim())}
        explain={explainGrant}
        onConfirm={async (code) => {
          const r = await api.admins.grant({ email: email.trim(), role, code });
          refresh();
          toast.success(t("grant.done", { email: r.user.email, role: inSentence(t(`common:${ROLE_LABEL[r.role]}`)) }));
        }}
      >
        <Field label={t("grant.email")}>{(p) => <TextInput {...p} type="email" autoFocus autoComplete="off" value={email} onChange={(e) => setEmail(e.target.value)} placeholder={t("grant.emailPlaceholder")} />}</Field>
        <RolePicker value={role} onChange={setRole} />
      </StepUpDialog>

      <StepUpDialog
        open={!!editing}
        onOpenChange={(o) => !o && setEditing(null)}
        title={editing ? t("change.title", { email: editing.a.user.email }) : t("change.button")}
        description={t("change.description")}
        confirmLabel={t("change.button")}
        ready={!!editing && editing.role !== editing.a.role}
        onConfirm={async (code) => {
          if (!editing) return;
          await api.admins.setRole(editing.a.user.id, editing.role, code);
          refresh();
          toast.success(t("change.done", { role: t(`common:${ROLE_LABEL[editing.role]}`) }));
        }}
      >
        {editing && <RolePicker value={editing.role} onChange={(r) => setEditing({ ...editing, role: r })} />}
      </StepUpDialog>

      <StepUpDialog
        open={!!revoking}
        onOpenChange={(o) => !o && setRevoking(null)}
        title={revoking ? t("revoke.title", { email: revoking.user.email }) : t("revoke.confirm")}
        description={t("revoke.description")}
        confirmLabel={t("revoke.confirm")}
        danger
        onConfirm={async (code) => {
          if (!revoking) return;
          await api.admins.revoke(revoking.user.id, code);
          refresh();
          toast.success(t("revoke.done", { email: revoking.user.email }));
        }}
      />
    </>
  );
}
