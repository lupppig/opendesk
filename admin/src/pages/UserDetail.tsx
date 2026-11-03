import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { Ban, ChevronDown, LogOut, Mail, RotateCcw, ScrollText, ShieldOff, Trash2 } from "lucide-react";
import { useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { ActionButton, ActionMenuItem } from "@/components/ActionButton";
import { IdChip, None } from "@/components/bits";
import { ErrorState } from "@/components/ErrorState";
import { BackLink, Banner, DetailSkeleton, KV, PageBody, PageColumn, PageHeader, Section } from "@/components/layout";
import { AdminRolePill, FlagPill, SuspendedPill } from "@/components/pills";
import { ReasonDialog } from "@/components/ReasonDialog";
import { StepUpDialog } from "@/components/StepUpDialog";
import { Table, TD, TH, THead, TR } from "@/components/table";
import { Time } from "@/components/Time";
import { Avatar } from "@/components/ui/avatar";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/ui/confirm";
import { EmptyState } from "@/components/ui/empty";
import { Field, TextInput } from "@/components/ui/input";
import { Menu, MenuContent, MenuSeparator, MenuTrigger } from "@/components/ui/menu";
import { toast } from "@/components/ui/toast";
import { useDocumentTitle } from "@/app/RootLayout";
import { i18n } from "@/i18n";
import { isApiError } from "@/lib/api/client";
import { api } from "@/lib/api/endpoints";
import type { AdminSession, AdminUser } from "@/lib/api/types";
import { humanize, number } from "@/lib/format";
import { q, qk } from "@/lib/queries";
import { useMe } from "@/lib/session";

export default function UserDetail() {
  const { t } = useTranslation(["users", "admins", "audit", "common", "health", "shell", "workspaces"]);
  const { id } = useParams({ from: "/shell/users/$id" });
  const user = useQuery(q.user(id));
  useDocumentTitle(user.data?.email);

  return (
    <>
      <PageHeader title={<BackLink to="/users">{t("list.title")}</BackLink>} />
      <PageBody>
        {user.isPending ? (
          <DetailSkeleton />
        ) : user.isError ? (
          <ErrorState
            error={user.error}
            onRetry={() => void user.refetch()}
            action={
              <Button variant="ghost" asChild>
                <Link to="/users">{t("detail.allUsers")}</Link>
              </Button>
            }
          />
        ) : (
          <UserView u={user.data} />
        )}
      </PageBody>
    </>
  );
}


/** "Chrome on macOS" from a user agent, or the raw string cut short. */
function deviceOf(ua: string) {
  const browser = /Edg\//.test(ua) ? "Edge" : /Firefox\//.test(ua) ? "Firefox" : /Chrome\//.test(ua) ? "Chrome" : /Safari\//.test(ua) ? "Safari" : /opendesk/i.test(ua) ? "opendesk" : "";
  const os = /iPhone|iPad/.test(ua) ? "iOS" : /Mac OS X|Macintosh/.test(ua) ? "macOS" : /Windows/.test(ua) ? "Windows" : /Android/.test(ua) ? "Android" : /Linux/.test(ua) ? "Linux" : "";
  if (browser && os) return i18n.t("users:detail.deviceOn", { browser, os });
  return browser || os || ua.slice(0, 60) || i18n.t("users:detail.unknownDevice");
}

function UserView({ u }: { u: AdminUser }) {
  const { t } = useTranslation(["users", "admins", "audit", "common", "health", "shell", "workspaces"]);
  const client = (c: AdminSession["client"]) => t(`detail.client.${c}`, { defaultValue: c });
  const me = useMe();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const confirm = useConfirm();
  const [dialog, setDialog] = useState<null | "suspend" | "mfa" | "delete">(null);
  const [confirmEmail, setConfirmEmail] = useState("");
  const self = u.id === me.id;
  const suspendBlocked = self ? t("detail.blocked.suspendSelf") : u.admin_role ? t("detail.blocked.suspendAdmin") : null;

  const apply = (next: AdminUser) => {
    qc.setQueryData(qk.user(u.id), next);
    void qc.invalidateQueries({ queryKey: qk.users });
  };
  const reload = () => qc.invalidateQueries({ queryKey: qk.user(u.id) });

  const resend = async () => {
    try {
      await api.users.resendVerification(u.id);
      toast.success(t("detail.toast.verificationSent", { email: u.email }));
    } catch (e) {
      if (isApiError(e, "already_verified")) {
        toast.info(t("detail.toast.alreadyVerified"));
        void reload();
      } else toast.error(e);
    }
  };

  const restore = () =>
    confirm.show(
      <>
        <Trans t={t} i18nKey="detail.restore.text" values={{ email: u.email }} components={{ b: <span className="font-medium text-fg" /> }} />
      </>,
      async () => {
        apply(await api.users.unsuspend(u.id));
        toast.success(t("detail.toast.restored"));
      },
      { title: t("detail.restore.title"), confirmLabel: t("common:actions.restore") },
    );

  const signOutAll = () =>
    confirm.show(
      <>
        <Trans t={t} i18nKey="detail.signOutAll.text" values={{ email: u.email }} components={{ b: <span className="font-medium text-fg" /> }} />
      </>,
      async () => {
        await api.users.revokeSessions(u.id);
        await reload();
        toast.success(t("detail.toast.signedOutAll"));
      },
      { title: t("detail.signOutAll.title"), confirmLabel: t("detail.signOutAll.button"), danger: true },
    );

  const signOutOne = async (s: AdminSession) => {
    try {
      await api.users.revokeSessions(u.id, s.id);
      qc.setQueryData<AdminUser>(qk.user(u.id), (prev) => (prev ? { ...prev, sessions: prev.sessions.filter((x) => x.id !== s.id) } : prev));
      toast.success(t("detail.toast.sessionSignedOut"));
    } catch (e) {
      toast.error(e);
    }
  };

  return (
    <PageColumn>
      <div className="flex flex-wrap items-start gap-4">
        <Avatar name={u.name || u.email} src={u.avatar_url} size="xl" />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h1 className="truncate text-[18px] font-semibold tracking-[-0.02em]">{u.name || u.email}</h1>
            {u.admin_role && <AdminRolePill role={u.admin_role} />}
            {u.suspended_at && <SuspendedPill />}
            {self && <Badge tone="outline">{t("admins:you")}</Badge>}
          </div>
          <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
            {u.name && <span className="text-[12.5px] text-fg-2">{u.email}</span>}
            <IdChip label={t("common:id")} value={u.id} />
            <span className="text-[11.5px] text-fg-3">
              <Trans t={t} i18nKey="detail.joinedSeen" components={{ joined: <Time iso={u.created_at} />, seen: <Time iso={u.last_seen_at} /> }} />
            </span>
          </div>
        </div>
        <div className="flex items-center gap-1.5">
          <Button variant="ghost" asChild icon={<ScrollText />}>
            <Link to="/audit" search={{ target: u.id }}>
              {t("shell:nav.audit")}
            </Link>
          </Button>
          <Menu>
            <MenuTrigger asChild>
              <Button variant="secondary" iconRight={<ChevronDown />}>
                {t("detail.actions")}
              </Button>
            </MenuTrigger>
            <MenuContent className="w-[260px]">
              {!u.email_verified && (
                <ActionMenuItem perm="users.sessions" icon={<Mail />} onSelect={() => void resend()}>
                  {t("detail.resendVerification")}
                </ActionMenuItem>
              )}
              <ActionMenuItem perm="users.sessions" icon={<LogOut />} blocked={u.sessions.length ? null : t("detail.sessions.empty")} onSelect={signOutAll}>
                {t("detail.signOutAll.button")}
              </ActionMenuItem>
              {u.suspended_at ? (
                <ActionMenuItem perm="users.suspend" icon={<RotateCcw />} onSelect={restore}>
                  {t("detail.restore.button")}
                </ActionMenuItem>
              ) : (
                <ActionMenuItem perm="users.suspend" icon={<Ban />} blocked={suspendBlocked} onSelect={() => setDialog("suspend")}>
                  {t("detail.suspend.button")}
                </ActionMenuItem>
              )}
              <MenuSeparator />
              <ActionMenuItem
                perm="users.mfa"
                icon={<ShieldOff />}
                blocked={!u.totp_enabled ? t("detail.blocked.mfaOff") : self ? t("detail.blocked.mfaSelf") : null}
                onSelect={() => setDialog("mfa")}
              >
                {t("detail.mfa.button")}
              </ActionMenuItem>
              <ActionMenuItem perm="users.delete" icon={<Trash2 />} danger blocked={self ? t("detail.blocked.deleteSelf") : null} onSelect={() => setDialog("delete")}>
                {t("detail.delete.button")}
              </ActionMenuItem>
            </MenuContent>
          </Menu>
        </div>
      </div>

      {u.suspended_at && (
        <Banner title={<Trans t={t} i18nKey="detail.suspendedSince" components={{ time: <Time iso={u.suspended_at} /> }} />}>
          {u.suspended_reason ? <span className="whitespace-pre-wrap">{u.suspended_reason}</span> : t("common:noReason")} {t("detail.suspendedHint")}
        </Banner>
      )}

      <div className="grid items-start gap-5 lg:grid-cols-[minmax(0,1fr)_340px]">
        <div className="flex min-w-0 flex-col gap-5">
          <Section title={t("list.col.workspaces")} count={number(u.memberships.length)} flush>
            {u.memberships.length ? (
              <Table>
                <THead>
                  <TH>{t("workspaces:list.col.workspace")}</TH>
                  <TH>{t("admins:col.role")}</TH>
                  <TH>{t("list.col.joined")}</TH>
                </THead>
                <tbody>
                  {u.memberships.map((m) => (
                    <TR key={m.workspace.id}>
                      <TD>
                        <Link to="/workspaces/$id" params={{ id: m.workspace.id }} className="group flex min-w-0 items-center gap-2.5">
                          <Avatar name={m.workspace.name} square size="sm" />
                          <span className="min-w-0">
                            <span className="block truncate font-medium text-fg group-hover:underline">{m.workspace.name}</span>
                            <span className="mono block truncate text-[11px] text-fg-4">{m.workspace.slug}</span>
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
              <EmptyState compact art="workspaces" title={t("detail.noWorkspaces")} description={t("detail.noWorkspacesHint")} />
            )}
          </Section>

          <Section
            title={t("detail.sessions.title")}
            count={number(u.sessions.length)}
            flush
            actions={
              u.sessions.length > 0 && (
                <ActionButton perm="users.sessions" size="xs" variant="danger" onClick={signOutAll}>
                  {t("detail.signOutAll.button")}
                </ActionButton>
              )
            }
          >
            {u.sessions.length ? (
              <Table>
                <THead>
                  <TH>{t("detail.sessions.device")}</TH>
                  <TH>{t("audit:col.ip")}</TH>
                  <TH>{t("detail.sessions.signedIn")}</TH>
                  <TH>{t("detail.sessions.lastUsed")}</TH>
                  <TH />
                </THead>
                <tbody>
                  {u.sessions.map((s) => (
                    <TR key={s.id}>
                      <TD>
                        <span className="block font-medium text-fg" title={s.user_agent}>
                          {deviceOf(s.user_agent)}
                        </span>
                        <span className="flex items-center gap-1.5 text-[11.5px] text-fg-3">
                          {client(s.client)}
                          {s.mfa_verified && <Badge tone="green">{t("flag.twoFactor")}</Badge>}
                        </span>
                      </TD>
                      <TD mono className="text-[11.5px] text-fg-2">
                        {s.ip || "–"}
                      </TD>
                      <TD className="text-fg-3">
                        <Time iso={s.created_at} />
                      </TD>
                      <TD className="text-fg-2">
                        <Time iso={s.last_used_at} />
                      </TD>
                      <TD align="right">
                        <ActionButton perm="users.sessions" size="xs" variant="ghost" onClick={() => void signOutOne(s)}>
                          {t("detail.sessions.signOut")}
                        </ActionButton>
                      </TD>
                    </TR>
                  ))}
                </tbody>
              </Table>
            ) : (
              <EmptyState compact art="users" title={t("detail.sessions.empty")} description={t("detail.sessions.emptyHint")} />
            )}
          </Section>
        </div>

        <div className="flex min-w-0 flex-col gap-5">
          <Section title={t("detail.account.title")}>
            <KV
              items={[
                [t("detail.account.email"), <FlagPill on={u.email_verified} yes={t("flag.verified")} no={t("flag.unverified")} warnOff />],
                [t("detail.account.twoFactor"), <FlagPill on={u.totp_enabled} yes={t("health:on")} no={t("health:off")} />],
                [t("detail.account.password"), <FlagPill on={u.has_password} yes={t("detail.account.set")} no={t("common:none")} />],
                [t("detail.account.passkeys"), u.passkeys ? <span className="mono tabular-nums">{number(u.passkeys)}</span> : <None />],
                [t("detail.account.adminRole"), u.admin_role ? <AdminRolePill role={u.admin_role} /> : <None />],
              ]}
            />
          </Section>
          <Section title={t("detail.identities.title")} count={number(u.identities.length)}>
            {u.identities.length ? (
              <ul className="flex flex-col gap-2">
                {u.identities.map((i) => (
                  <li key={`${i.provider}:${i.email}`} className="flex items-center gap-2 text-[12.5px]">
                    <Badge tone="neutral">{humanize(i.provider)}</Badge>
                    <span className="min-w-0 flex-1 truncate text-fg-2">{i.email}</span>
                    <Time iso={i.created_at} className="text-[11.5px] text-fg-4" />
                  </li>
                ))}
              </ul>
            ) : (
              <p className="text-[12.5px] text-fg-3">{t("detail.identities.none")}</p>
            )}
          </Section>
        </div>
      </div>

      <ReasonDialog
        open={dialog === "suspend"}
        onOpenChange={(o) => setDialog(o ? "suspend" : null)}
        title={t("detail.suspend.title", { email: u.email })}
        description={t("detail.suspend.description")}
        confirmLabel={t("detail.suspend.button")}
        onConfirm={async (reason) => {
          apply(await api.users.suspend(u.id, reason));
          toast.success(t("detail.toast.suspended"));
        }}
      />

      <StepUpDialog
        open={dialog === "mfa"}
        onOpenChange={(o) => setDialog(o ? "mfa" : null)}
        title={t("detail.mfa.title")}
        description={t("detail.mfa.description", { email: u.email })}
        confirmLabel={t("detail.mfa.confirm")}
        danger
        onConfirm={async (code) => {
          apply(await api.users.disableMfa(u.id, code));
          toast.success(t("detail.toast.mfaOff"));
        }}
      />

      <StepUpDialog
        open={dialog === "delete"}
        onOpenChange={(o) => {
          setDialog(o ? "delete" : null);
          if (!o) setConfirmEmail("");
        }}
        title={t("detail.delete.title", { email: u.email })}
        description={u.memberships.length ? t("detail.delete.descriptionLeaves", { count: u.memberships.length, n: number(u.memberships.length) }) : t("detail.delete.description")}
        confirmLabel={t("detail.delete.button")}
        danger
        ready={confirmEmail.trim().toLowerCase() === u.email.toLowerCase()}
        explain={(e) =>
          isApiError(e, "sole_owner") ? t("detail.delete.soleOwner") : undefined
        }
        onConfirm={async (code) => {
          await api.users.remove(u.id, code, confirmEmail.trim());
          qc.removeQueries({ queryKey: qk.user(u.id) });
          void qc.invalidateQueries({ queryKey: qk.users });
          void qc.invalidateQueries({ queryKey: qk.summary });
          toast.success(t("detail.toast.deleted", { email: u.email }));
          void navigate({ to: "/users" });
        }}
      >
        <Field label={<Trans t={t} i18nKey="common:ui.typeToConfirm" values={{ text: u.email }} components={{ v: <span className="mono font-medium" /> }} />}>
          {(p) => <TextInput {...p} autoFocus autoComplete="off" spellCheck={false} value={confirmEmail} onChange={(e) => setConfirmEmail(e.target.value)} />}
        </Field>
      </StepUpDialog>
    </PageColumn>
  );
}
