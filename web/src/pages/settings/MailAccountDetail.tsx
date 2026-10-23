// One mail account: its name, From name and colour, the signature its mail carries, the
// addresses it sends from (AddressesSection), its automatic reply (each synced with the provider where it can be: AccountSettings), who it
// is shared with, its server settings (IMAP), its sync, and disconnecting it.
// Only whoever connected it (and workspace admins, for a shared one) can change it.

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams } from "@tanstack/react-router";
import { Lock, RefreshCw, Users } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { BackLink, Row, Section, SettingsPage } from "@/components/layout";
import { ColorSwatchPicker } from "@/components/settings2/ColorSwatchPicker";
import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/ui/confirm";
import { EmptyState } from "@/components/ui/empty";
import { TextInput } from "@/components/ui/input";
import { toast } from "@/components/ui/toast";
import { SignatureEditor } from "@/components/templates/SignatureEditor";
import { ACCOUNT_TONES, ProviderMark } from "@/components/collab/bits";
import { FromNameHint, OutOfOfficeSection, SignatureSync } from "@/components/collab/AccountSettings";
import { AddressesSection } from "@/components/collab/AddressesSection";
import { CollabFromNameField, KeptInOpendesk, SenderPreview, useSenderPreview } from "@/components/collab/SenderPreview";
import { ImapFields, imapValid, type ImapForm } from "@/components/collab/ConnectAccount";
import { audienceIsEmpty, ChoiceCard, SharedAudience } from "@/components/collab/pickers";
import { api, type CollabAccountPatch } from "@/lib/api/endpoints";
import type { MailAccount, MailAudience, MailIdentityPreview } from "@/lib/api/types";
import { errorMessage } from "@/lib/api/client";
import { storeAccount, useSettingsAccounts } from "@/lib/collab/hooks";
import { identitySignature } from "@/lib/collab/identities";
import { relLong } from "@/lib/format";
import { COLLAB_FROM_VARS, fromNameProblem } from "@/lib/fromname";
import { qk } from "@/lib/queries";
import { useSession } from "@/lib/session";
import type { Tone } from "@/lib/tones";
import { AccountStatus, useReconnect } from "./MailAccounts";

type Form = { name: string; display_name: string; color: string; signature_html: string; visibility: "private" | "shared"; shared_with: MailAudience };

const formOf = (a: MailAccount): Form => ({
  name: a.name,
  display_name: a.display_name,
  color: a.color,
  signature_html: a.signature_html,
  visibility: a.visibility,
  shared_with: a.shared_with ?? { everyone: false, team_ids: [], user_ids: [] },
});

const imapOf = (a: MailAccount): ImapForm => ({
  email: a.email,
  name: a.name,
  password: "",
  imap_host: a.imap?.imap_host ?? "",
  imap_port: a.imap?.imap_port ?? 993,
  imap_tls: a.imap?.imap_tls ?? "tls",
  imap_username: a.imap?.imap_username ?? "",
  smtp_host: a.imap?.smtp_host ?? "",
  smtp_port: a.imap?.smtp_port ?? 465,
  smtp_tls: a.imap?.smtp_tls ?? "tls",
  smtp_username: a.imap?.smtp_username ?? "",
  smtp_password: "",
});

const sameAudience = (a: MailAudience, b: MailAudience) => a.everyone === b.everyone && a.team_ids.join() === b.team_ids.join() && a.user_ids.join() === b.user_ids.join();

export default function MailAccountDetail() {
  const { t } = useTranslation("collab");
  const { accountId } = useParams({ from: "/app/settings/mail-accounts/$accountId" });
  const navigate = useNavigate();
  const qc = useQueryClient();
  const confirm = useConfirm();
  const me = useSession((s) => s.user?.id);
  const reconnect = useReconnect();
  const { data: accounts, isLoading } = useSettingsAccounts();
  const account = accounts?.find((a) => a.id === accountId);
  const [form, setForm] = useState<Form | null>(null);
  const [imap, setImap] = useState<ImapForm | null>(null);
  const [failure, setFailure] = useState<string | null>(null);

  useEffect(() => {
    if (account && !form) {
      setForm(formOf(account));
      setImap(imapOf(account));
    }
  }, [account, form]);

  const base = useMemo(() => (account ? formOf(account) : null), [account]);
  // A signature or From name that changed at the provider replaces the field while it is not being edited.
  const lastBase = useRef(base);
  // Whether the person changed the signature: only then does a save send it, so opening
  // and saving other settings never writes a signature back to Gmail.
  const [sigEdited, setSigEdited] = useState(false);
  useEffect(() => {
    const prev = lastBase.current;
    lastBase.current = base;
    if (!prev || !base) return;
    setForm((f) => {
      if (!f) return f;
      const next = { ...f };
      if (f.signature_html === prev.signature_html) next.signature_html = base.signature_html;
      if (f.display_name === prev.display_name) next.display_name = base.display_name;
      return next;
    });
  }, [base]);
  const imapBase = useMemo(() => (account ? imapOf(account) : null), [account]);

  const patch = useMemo((): CollabAccountPatch => {
    if (!form || !base || !account) return {};
    const p: CollabAccountPatch = {};
    if (form.name.trim() !== base.name) p.name = form.name.trim();
    if (form.display_name.trim() !== base.display_name) p.display_name = form.display_name.trim();
    if (form.color !== base.color) p.color = form.color;
    if (sigEdited && form.signature_html !== base.signature_html) p.signature_html = form.signature_html;
    if (form.visibility !== base.visibility) p.visibility = form.visibility;
    const aud = form.visibility === "shared" ? (form.shared_with.everyone ? { everyone: true, team_ids: [], user_ids: [] } : form.shared_with) : { everyone: false, team_ids: [], user_ids: [] };
    if (form.visibility === "shared" && (p.visibility || !sameAudience(aud, base.shared_with))) p.shared_with = aud;
    if (account.provider === "imap" && imap && imapBase) {
      const keys = ["imap_host", "imap_port", "imap_tls", "imap_username", "smtp_host", "smtp_port", "smtp_tls", "smtp_username"] as const;
      const changed: Record<string, unknown> = {};
      for (const k of keys) if (imap[k] !== imapBase[k]) changed[k] = imap[k];
      if (imap.password) changed.password = imap.password;
      if (imap.smtp_password) changed.smtp_password = imap.smtp_password;
      if (Object.keys(changed).length) p.imap = changed as CollabAccountPatch["imap"];
    }
    return p;
  }, [form, base, account, imap, imapBase, sigEdited]);
  const dirty = Object.keys(patch).length > 0;
  const audienceOk = !form || form.visibility === "private" || !audienceIsEmpty(form.shared_with);
  const imapOk = !imap || account?.provider !== "imap" || imapValid(imap, false);

  const save = useMutation({
    mutationFn: () => api.collab.accounts.update(accountId, patch),
    onSuccess: (a) => {
      storeAccount(qc, a);
      setForm(formOf(a));
      setImap(imapOf(a));
      setSigEdited(false);
      setFailure(null);
      toast.success(t("detail.saved"));
    },
    onError: (e) => setFailure(errorMessage(e)),
  });

  const sync = useMutation({
    mutationFn: () => api.collab.accounts.sync(accountId),
    onSuccess: () => toast.info(t("detail.syncStarted", { name: account?.name || account?.email || "" })),
    onError: (e) => toast.error(e),
  });

  // The From name and signature as your mail carries them: filled by the server while they
  // are typed; for someone who cannot change them, as they are now.
  const primary = account?.identities?.[0];
  const typed = useSenderPreview(accountId, { display_name: form?.display_name ?? "", signature_html: form?.signature_html ?? "" }, !!form && !!account?.can_manage);
  const preview: MailIdentityPreview | undefined =
    account && !account.can_manage
      ? { from_name: primary?.from_name ?? account.display_name, from: `<${account.email}>`, signature_html: primary ? identitySignature(primary) : account.signature_html, from_name_problem: "", problems: [] }
      : typed;
  const pullName = useMutation({
    mutationFn: () => api.collab.accounts.pullDisplayName(accountId),
    onSuccess: (a) => {
      storeAccount(qc, a);
      setForm((f) => (f ? { ...f, display_name: a.display_name } : f));
      toast.success(t("sender.namePulled"));
    },
    onError: (e) => toast.error(errorMessage(e)),
  });

  if (isLoading || (account && !form))
    return (
      <SettingsPage title={t("detail.back")} back={<BackLink to="/settings/mail-accounts">{t("detail.back")}</BackLink>}>
        <Section>
          <div className="h-40" />
        </Section>
      </SettingsPage>
    );
  if (!account || !form)
    return (
      <SettingsPage title={t("detail.notFound")} back={<BackLink to="/settings/mail-accounts">{t("detail.back")}</BackLink>}>
        <Section flush>
          <EmptyState art="inbox" title={t("detail.notFound")} description={t("thread.notFoundHint")} />
        </Section>
      </SettingsPage>
    );

  const manage = account.can_manage;
  const set = (p: Partial<Form>) => setForm((f) => (f ? { ...f, ...p } : f));
  const disconnect = () =>
    confirm.show(
      t("detail.disconnectText"),
      async () => {
        await api.collab.accounts.remove(account.id);
        storeAccount(qc, { id: account.id, removed: true });
        void qc.invalidateQueries({ queryKey: qk.collab });
        toast.success(t("detail.disconnected", { email: account.email }));
        void navigate({ to: "/settings/mail-accounts", search: {} });
      },
      { title: t("detail.disconnectTitle", { email: account.email }), confirmLabel: t("detail.disconnect"), danger: true },
    );

  return (
    <SettingsPage
      title={
        <span className="flex items-center gap-2.5">
          <ProviderMark provider={account.provider} size="sm" />
          {account.name || account.email}
        </span>
      }
      description={<span className="mono">{account.email}</span>}
      back={<BackLink to="/settings/mail-accounts">{t("detail.back")}</BackLink>}
      actions={
        manage ? (
          <Button variant="primary" disabled={!dirty || !audienceOk || !imapOk || !!fromNameProblem(form.display_name, COLLAB_FROM_VARS)} loading={save.isPending} onClick={() => save.mutate()}>
            {t("detail.save")}
          </Button>
        ) : undefined
      }
    >
      {!manage && <p className="rounded-md bg-sunken px-3 py-2 text-[12px] text-fg-3">{t("detail.readOnly")}</p>}
      {failure && (
        <div role="alert" className="rounded-md border border-[color-mix(in_srgb,var(--danger)_28%,var(--line))] bg-[color-mix(in_srgb,var(--danger)_6%,transparent)] px-3 py-2 text-[12px]">
          <span className="font-medium text-danger-fg">{t("connect.failed")}</span>
          <span className="mono mt-0.5 block break-words text-[11.5px] text-fg-2">{failure}</span>
        </div>
      )}

      <Section title={t("detail.status")}>
        <Row label={t("detail.status")} description={account.last_synced_at ? t("accounts.lastSync", { when: relLong(account.last_synced_at) }) : t("accounts.neverSynced")}>
          <AccountStatus account={account} />
          <Button size="sm" icon={<RefreshCw />} loading={sync.isPending} onClick={() => sync.mutate()}>
            {t("detail.sync")}
          </Button>
        </Row>
        {account.reconnect && manage && (
          <Row label={t("nav.reconnect")} description={t("detail.reconnectHint")}>
            <Button size="sm" variant="primary" onClick={() => void reconnect(account)}>
              {t("accounts.reconnect")}
            </Button>
          </Row>
        )}
        {account.last_error && (
          <Row label={t("detail.lastError")} stack>
            <span className="mono break-words text-[11.5px] text-fg-2">{account.last_error}</span>
          </Row>
        )}
      </Section>

      <Section title={t("detail.general")}>
        <Row label={t("detail.name")} htmlFor="acc-name">
          <TextInput id="acc-name" className="w-[260px]" value={form.name} disabled={!manage} onChange={(e) => set({ name: e.target.value })} placeholder={account.email} />
        </Row>
        <Row
          label={t("detail.fromName")}
          description={
            <>
              {t("detail.fromNameHint")}. {t("sender.varsHint")}
              <FromNameHint account={account} />
            </>
          }
          htmlFor="acc-from"
          stack
        >
          <CollabFromNameField
            id="acc-from"
            account={account}
            email={account.email}
            value={form.display_name}
            disabled={!manage}
            onChange={(display_name) => set({ display_name })}
            gmail={
              manage &&
              primary?.display_name_local && (
                <Button size="sm" loading={pullName.isPending} onClick={() => pullName.mutate()}>
                  {t("detail.getFromGmail")}
                </Button>
              )
            }
          />
        </Row>
        <Row label={t("detail.color")} description={t("detail.colorHint")}>
          <ColorSwatchPicker size="sm" tones={[...ACCOUNT_TONES] as Tone[]} value={form.color as Tone} onChange={(c) => manage && set({ color: c })} label={t("detail.color")} />
        </Row>
      </Section>

      <Section title={t("detail.signature")} description={t("detail.signatureHint")} flush>
        <SignatureSync account={account} manage={manage} onReconnect={() => void reconnect(account)} />
        <div className="p-3">
          <SignatureEditor
            kind="collab_signature"
            value={form.signature_html}
            onChange={(html) => {
              setSigEdited(true);
              set({ signature_html: html });
            }}
            disabled={!manage}
            ariaLabel={t("detail.signature")}
            problems={preview?.problems}
            preview={<SenderPreview preview={preview} />}
          />
          {account.provider === "google" && /\{\{|\{%/.test(form.signature_html) && <KeptInOpendesk className="mt-2" />}
          <p className="mt-2 text-[11.5px] text-fg-4">{t("sender.signatureVars")}</p>
        </div>
      </Section>

      <AddressesSection account={account} manage={manage} />

      <OutOfOfficeSection account={account} manage={manage} onReconnect={() => void reconnect(account)} />

      <Section title={t("detail.sharing")} description={t("detail.sharingHint")}>
        <div role="radiogroup" aria-label={t("detail.sharing")} className="flex flex-col gap-2 py-3.5">
          <ChoiceCard checked={form.visibility === "private"} disabled={!manage || (account.owner?.id !== me && form.visibility !== "private")} onSelect={() => set({ visibility: "private" })} icon={<Lock />} title={t("audience.private")} hint={t("audience.privateHint")} />
          <ChoiceCard
            checked={form.visibility === "shared"}
            disabled={!manage}
            onSelect={() => set({ visibility: "shared", shared_with: audienceIsEmpty(form.shared_with) ? { everyone: true, team_ids: [], user_ids: [] } : form.shared_with })}
            icon={<Users />}
            title={t("audience.shared")}
            hint={t("audience.sharedHint")}
          >
            <SharedAudience value={form.shared_with} onChange={(shared_with) => set({ shared_with })} disabled={!manage} exclude={account.owner?.id} />
          </ChoiceCard>
          {!audienceOk && <p className="text-[11.5px] text-danger-fg">{t("audience.needSomeone")}</p>}
        </div>
      </Section>

      {account.provider === "imap" && manage && imap && (
        <Section title={t("detail.server")} description={t("detail.serverHint")}>
          <div className="py-4">
            <ImapFields form={imap} onChange={(p) => setImap((f) => (f ? { ...f, ...p } : f))} passwordOptional showIdentity={false} />
          </div>
        </Section>
      )}

      {manage && (
        <Section title={t("detail.danger")} danger>
          <Row label={t("detail.disconnect")} description={t("detail.disconnectHint")}>
            <Button variant="danger" onClick={disconnect}>
              {t("detail.disconnect")}
            </Button>
          </Row>
        </Section>
      )}
    </SettingsPage>
  );
}
