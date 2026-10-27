// A mail account's addresses (contracts/collab.md "Addresses"): its own and the ones it
// sends as. Gmail's come from Gmail: their From name and signature follow Gmail's changes,
// and an edit here is kept in opendesk (Gmail lets apps change its main address alone).
// Microsoft's and IMAP's are added here by hand. Each one can be edited in a drawer, made
// the default for new mail, and (hand-added ones) removed.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AtSign, MoreHorizontal, Plus, RefreshCw } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Section } from "@/components/layout";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/ui/confirm";
import { Drawer } from "@/components/ui/dialog";
import { EmptyState } from "@/components/ui/empty";
import { TextInput } from "@/components/ui/input";
import { Menu, MenuContent, MenuItem, MenuTrigger } from "@/components/ui/menu";
import { toast } from "@/components/ui/toast";
import { Tooltip } from "@/components/ui/tooltip";
import { SignatureEditor } from "@/components/templates/SignatureEditor";
import { CollabFromNameField, KeptInOpendesk, SenderPreview, useSenderPreview } from "./SenderPreview";
import { api, type CollabIdentityBody } from "@/lib/api/endpoints";
import { errorMessage } from "@/lib/api/client";
import type { MailAccount, MailIdentity } from "@/lib/api/types";
import { storeAccount } from "@/lib/collab/hooks";
import { identitiesOf } from "@/lib/collab/identities";
import { isEmail } from "@/lib/collab/recipients";
import { relLong } from "@/lib/format";
import { COLLAB_FROM_VARS, fromNameProblem } from "@/lib/fromname";
import { qk } from "@/lib/queries";
import { SyncLine } from "./AccountSettings";

type Editing = { mode: "add" } | { mode: "edit"; identity: MailIdentity };

export function AddressesSection({ account, manage }: { account: MailAccount; manage: boolean }) {
  const { t } = useTranslation("collab");
  const qc = useQueryClient();
  const confirm = useConfirm();
  const [editing, setEditing] = useState<Editing | null>(null);
  const list = identitiesOf(account);
  const byHand = account.provider !== "google";

  const makeDefault = useMutation({
    mutationFn: (email: string) => api.collab.accounts.update(account.id, { default_from: email }),
    onSuccess: (a, email) => {
      storeAccount(qc, a);
      toast.success(t("addresses.defaultSet", { email }));
    },
    onError: (e) => toast.error(errorMessage(e)),
  });
  // Gmail's addresses now, for one just added there.
  const refresh = useMutation({
    mutationFn: () => api.collab.accounts.refreshIdentities(account.id),
    onSuccess: (a) => {
      storeAccount(qc, a);
      const added = identitiesOf(a).filter((i) => !list.some((x) => x.email === i.email)).length;
      toast.success(added ? t("addresses.checked", { count: added }) : t("addresses.checkedNone"));
    },
    onError: (e) => toast.error(errorMessage(e)),
  });
  const remove = (i: MailIdentity) =>
    confirm.show(
      t("addresses.removeText"),
      async () => {
        const a = await api.collab.accounts.removeIdentity(account.id, i.id!);
        storeAccount(qc, a);
        toast.success(t("addresses.removed", { email: i.email }));
      },
      { title: t("addresses.removeTitle", { email: i.email }), confirmLabel: t("addresses.remove"), danger: true },
    );

  return (
    <Section
      title={t("addresses.title")}
      description={t(`addresses.hint_${account.provider}`)}
      actions={
        !manage ? undefined : byHand ? (
          <Button size="sm" icon={<Plus />} onClick={() => setEditing({ mode: "add" })}>
            {t("addresses.add")}
          </Button>
        ) : (
          <Button size="sm" icon={<RefreshCw />} loading={refresh.isPending} onClick={() => refresh.mutate()}>
            {t("addresses.check")}
          </Button>
        )
      }
      flush
    >
      <ul aria-label={t("addresses.title")} className="flex flex-col">
        {list.map((i) => (
          <li key={i.email} className="flex min-h-12 items-center gap-3 border-b border-line-soft px-4 py-2.5 last:border-b-0">
            <span className="flex size-7 shrink-0 items-center justify-center rounded-md bg-sunken text-fg-3">
              <AtSign className="size-3.5" />
            </span>
            <span className="min-w-0 flex-1">
              <span className="flex flex-wrap items-center gap-1.5">
                <span className="mono truncate text-[12.5px] text-fg">{i.email}</span>
                {i.primary && <Badge>{t("addresses.primary")}</Badge>}
                {i.default && <Badge tone="blue">{t("addresses.default")}</Badge>}
                {i.verification === "pending" && (
                  <Tooltip content={t("addresses.pendingHint")}>
                    <span className="flex">
                      <Badge tone="amber">{t("addresses.pending")}</Badge>
                    </span>
                  </Tooltip>
                )}
              </span>
              <span className="mt-0.5 block truncate text-[11.5px] text-fg-3">
                {i.primary ? t("addresses.primaryHint") : `${i.display_name ? i.from_name || i.display_name : t("addresses.usesAccountName")} · ${t(i.source === "provider" ? "addresses.sync_pull" : "addresses.sync_none")}`}
              </span>
              {i.verification === "pending" && <span className="mt-0.5 block text-[11.5px] text-tone-amber">{t("addresses.pendingHint")}</span>}
            </span>
            {manage && (
              <span className="flex shrink-0 items-center gap-1">
                {!i.primary && (
                  <Button size="sm" variant="ghost" onClick={() => setEditing({ mode: "edit", identity: i })}>
                    {t("addresses.edit")}
                  </Button>
                )}
                {(i.can_send && !i.default) || i.source === "manual" ? (
                  <Menu>
                    <MenuTrigger asChild>
                      <Button size="sm" variant="ghost" square icon={<MoreHorizontal />} aria-label={t("addresses.actions", { email: i.email })} />
                    </MenuTrigger>
                    <MenuContent align="end">
                      {i.can_send && !i.default && <MenuItem onSelect={() => makeDefault.mutate(i.email)}>{t("addresses.makeDefault")}</MenuItem>}
                      {i.source === "manual" && i.id && (
                        <MenuItem danger onSelect={() => remove(i)}>
                          {t("addresses.remove")}
                        </MenuItem>
                      )}
                    </MenuContent>
                  </Menu>
                ) : null}
              </span>
            )}
          </li>
        ))}
      </ul>
      {byHand && list.length === 1 && (
        <EmptyState
          compact
          art="generic"
          title={t("addresses.emptyTitle")}
          description={t("addresses.emptyHint")}
          action={
            manage ? (
              <Button size="sm" icon={<Plus />} onClick={() => setEditing({ mode: "add" })}>
                {t("addresses.add")}
              </Button>
            ) : undefined
          }
        />
      )}
      {editing && <AddressDrawer account={account} editing={editing} onClose={() => setEditing(null)} />}
    </Section>
  );
}

/** Adding a hand-made address, or editing one: its From name, reply-to and signature. */
function AddressDrawer({ account, editing, onClose }: { account: MailAccount; editing: Editing; onClose: () => void }) {
  const { t } = useTranslation("collab");
  const qc = useQueryClient();
  // The address as last saved or brought from Gmail.
  const [current, setCurrent] = useState(editing.mode === "edit" ? editing.identity : null);
  const [email, setEmail] = useState("");
  const [name, setName] = useState(current?.display_name ?? "");
  const [replyTo, setReplyTo] = useState(current?.reply_to ?? "");
  const [signature, setSignature] = useState(current?.signature_html ?? "");
  // Whether the person changed the signature: only then does a save send it.
  const [sigEdited, setSigEdited] = useState(false);
  // Remounts the editor with a signature brought from Gmail.
  const [sigVersion, setSigVersion] = useState(0);
  const [failure, setFailure] = useState<string | null>(null);
  const manual = !current || current.source === "manual";
  const fromGmail = current?.source === "provider";
  const suggestions = useQuery({
    queryKey: qk.of("collab", "identity-suggestions", account.id),
    queryFn: () => api.collab.accounts.identitySuggestions(account.id),
    enabled: !current && account.provider === "microsoft",
    staleTime: 60_000,
  });

  const body = (): CollabIdentityBody => {
    const b: CollabIdentityBody = {};
    if (!current || name.trim() !== current.display_name) b.display_name = name.trim();
    if (sigEdited && (!current || signature !== current.signature_html)) b.signature_html = signature;
    if (manual && (!current || replyTo.trim() !== current.reply_to)) b.reply_to = replyTo.trim();
    return b;
  };
  const dirty = !current || Object.keys(body()).length > 0;
  const valid = (current || isEmail(email)) && (!replyTo.trim() || isEmail(replyTo)) && !fromNameProblem(name, COLLAB_FROM_VARS);
  // Its From name and signature as your mail would carry them, while they are typed.
  const preview = useSenderPreview(account.id, { email: current?.email ?? (isEmail(email) ? email.trim() : undefined), display_name: name, signature_html: signature });

  const save = useMutation({
    mutationFn: () => (current ? api.collab.accounts.updateIdentity(account.id, current.id!, body()) : api.collab.accounts.addIdentity(account.id, { email: email.trim(), ...body() })),
    onSuccess: (a) => {
      storeAccount(qc, a);
      toast.success(current ? t("addresses.saved", { email: current.email }) : t("addresses.added", { email: email.trim().toLowerCase() }));
      onClose();
    },
    onError: (e) => setFailure(errorMessage(e)),
  });
  // Gmail's signature for this address now, in place of the one here.
  const pull = useMutation({
    mutationFn: () => api.collab.accounts.pullIdentitySignature(account.id, current!.id!),
    onSuccess: (a) => {
      storeAccount(qc, a);
      const next = a.identities.find((i) => i.email === current?.email);
      if (next) {
        setCurrent(next);
        setSignature(next.signature_html);
        setSigEdited(false);
        setSigVersion((v) => v + 1);
      }
      toast.success(t("detail.signaturePulled"));
    },
    onError: (e) => toast.error(errorMessage(e)),
  });

  // Gmail's From name for this address now, in place of one with variables.
  const pullName = useMutation({
    mutationFn: () => api.collab.accounts.pullIdentityDisplayName(account.id, current!.id!),
    onSuccess: (a) => {
      storeAccount(qc, a);
      const next = a.identities.find((i) => i.email === current?.email);
      if (next) {
        setCurrent(next);
        setName(next.display_name);
      }
      toast.success(t("sender.namePulled"));
    },
    onError: (e) => toast.error(errorMessage(e)),
  });

  const title = current ? t("addresses.editTitle", { email: current.email }) : t("addresses.addTitle");
  return (
    <Drawer open onOpenChange={(o) => !o && onClose()} title={title} width={600}>
      <form
        className="flex min-h-full flex-col"
        onSubmit={(e) => {
          e.preventDefault();
          if (e.target !== e.currentTarget) return;
          if (dirty && valid) save.mutate();
        }}
      >
        <div className="flex flex-1 flex-col gap-4 p-5">
          <div>
            <h2 className="text-[13px] font-semibold tracking-[-0.01em]">{title}</h2>
            <SyncLine
              sync={current?.signature_sync ?? "none"}
              className="mt-1.5"
              actions={
                fromGmail && (
                  <>
                    {current?.signature_synced_at && <span className="text-fg-4">{t("detail.syncedAt", { when: relLong(current.signature_synced_at) })}</span>}
                    <Button size="sm" loading={pull.isPending} onClick={() => pull.mutate()}>
                      {t("detail.getFromGmail")}
                    </Button>
                  </>
                )
              }
            >
              {t(fromGmail ? "addresses.syncLine_pull" : "addresses.syncLine_none")}
            </SyncLine>
          </div>
          {failure && (
            <p role="alert" className="rounded-md border border-[color-mix(in_srgb,var(--danger)_28%,var(--line))] bg-[color-mix(in_srgb,var(--danger)_6%,transparent)] px-3 py-2 text-[12px] text-danger-fg">
              {failure}
            </p>
          )}
          {!current && (
            <label className="flex flex-col gap-1.5">
              <span className="text-[12.5px] font-medium">{t("addresses.email")}</span>
              <TextInput data-autofocus value={email} onChange={(e) => setEmail(e.target.value)} placeholder="sales@example.com" className="mono" />
              <span className="text-[11.5px] text-fg-4">{t("addresses.emailHint")}</span>
              {!!suggestions.data?.addresses.length && (
                <span className="mt-1 flex flex-wrap items-center gap-1.5 text-[11.5px] text-fg-3">
                  {t("addresses.suggestions")}
                  {suggestions.data.addresses.map((s) => (
                    <button key={s} type="button" onClick={() => setEmail(s)} className="mono rounded-sm bg-hover px-1.5 py-0.5 text-fg-2 hover:text-fg">
                      {s}
                    </button>
                  ))}
                </span>
              )}
            </label>
          )}
          <div className="flex flex-col gap-1.5">
            <label htmlFor="address-from" className="text-[12.5px] font-medium">
              {t("addresses.fromName")}
            </label>
            <CollabFromNameField
              id="address-from"
              account={account}
              email={current?.email ?? (email.trim().toLowerCase() || "sales@example.com")}
              value={name}
              onChange={setName}
              inherit={account.display_name}
              autoFocus={!!current}
              gmail={
                fromGmail &&
                current?.display_name_local && (
                  <Button size="sm" loading={pullName.isPending} onClick={() => pullName.mutate()}>
                    {t("detail.getFromGmail")}
                  </Button>
                )
              }
            />
            <span className="text-[11.5px] text-fg-4">
              {t("addresses.fromNameHint", { name: account.display_name || account.email })} {t("sender.varsHint")}
            </span>
          </div>
          {manual ? (
            <label className="flex flex-col gap-1.5">
              <span className="text-[12.5px] font-medium">{t("addresses.replyTo")}</span>
              <TextInput value={replyTo} onChange={(e) => setReplyTo(e.target.value)} placeholder={email || current?.email} className="mono" />
              <span className="text-[11.5px] text-fg-4">{t("addresses.replyToHint")}</span>
            </label>
          ) : (
            current?.reply_to && <p className="text-[11.5px] text-fg-3">{t("addresses.replyToGmail", { email: current.reply_to })}</p>
          )}
          <div className="flex flex-col gap-1.5">
            <span className="text-[12.5px] font-medium">{t("addresses.signature")}</span>
            <SignatureEditor
              key={sigVersion}
              kind="collab_signature"
              value={signature}
              onChange={(html) => {
                setSigEdited(true);
                setSignature(html);
              }}
              ariaLabel={t("addresses.signature")}
              minHeight={120}
              problems={preview?.problems}
              preview={<SenderPreview preview={preview} />}
            />
            {fromGmail && /\{\{|\{%/.test(signature) && <KeptInOpendesk />}
            <span className="text-[11.5px] text-fg-4">
              {t("addresses.signatureHint")} {t("sender.signatureVars")}
            </span>
          </div>
        </div>
        <div className="sticky bottom-0 flex items-center justify-end gap-2 border-t border-line bg-sunken px-5 py-3">
          <Button type="button" variant="ghost" onClick={onClose}>
            {t("addresses.cancel")}
          </Button>
          <Button type="submit" variant="primary" disabled={!dirty || !valid} loading={save.isPending}>
            {current ? t("addresses.save") : t("addresses.add")}
          </Button>
        </div>
      </form>
    </Drawer>
  );
}
