// How mail from an address reads for the person writing it (contracts/collab.md
// "Addresses"): its From name and signature filled for you by the server (POST
// .../identities/preview) while they are typed, with what is wrong with the signature for
// the editor to mark. A shared account's mail carries the name and details of whoever sends.

import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { Info } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { SignatureView } from "@/components/editor/signature";
import { FromFormatField, FromPreviewLine } from "@/components/templates/FromFormatField";
import { api } from "@/lib/api/endpoints";
import type { MailAccount, MailIdentityPreview, MailIdentityPreviewBody } from "@/lib/api/types";
import { collabFromName } from "@/lib/collab/identities";
import { cn } from "@/lib/cn";
import { COLLAB_FROM_VARS } from "@/lib/fromname";
import { useUser, useWorkspace } from "@/lib/session";

function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value);
  useEffect(() => {
    const id = setTimeout(() => setV(value), ms);
    return () => clearTimeout(id);
  }, [value, ms]);
  return v;
}

/** The server's fill of what is typed, for you; the last one stays while the next loads. */
export function useSenderPreview(accountId: string, body: MailIdentityPreviewBody, enabled = true): MailIdentityPreview | undefined {
  const key = useDebounced(JSON.stringify(body), 250);
  const { data } = useQuery({
    queryKey: ["collab", "identity-preview", accountId, key],
    queryFn: () => api.collab.accounts.previewIdentity(accountId, JSON.parse(key) as MailIdentityPreviewBody),
    placeholderData: keepPreviousData,
    staleTime: 30_000,
    enabled,
  });
  return data;
}

/** The From line and the signature as your mail carries them. */
export function SenderPreview({ preview, className }: { preview: MailIdentityPreview | undefined; className?: string }) {
  const { t } = useTranslation("collab");
  const { t: ts } = useTranslation("settings");
  const at = preview?.from.lastIndexOf("<") ?? -1;
  const email = preview && at >= 0 ? preview.from.slice(at + 1, -1) : (preview?.from ?? "");
  return (
    <div role="region" aria-label={ts("signatureEditor.preview")} className={cn("flex flex-col overflow-hidden rounded-lg border border-line bg-sunken", className)}>
      <div className="flex items-baseline gap-2 border-b border-line bg-surface px-2.5 py-1.5">
        <span className="text-[12.5px] font-medium">{ts("signatureEditor.preview")}</span>
        <span className="text-[11.5px] text-fg-4">{t("sender.asYou")}</span>
      </div>
      <div className="flex flex-col gap-2 p-3">
        {preview && <FromPreviewLine label={t("sender.from")} name={preview.from_name} email={email} />}
        <div className="rounded-md border border-line bg-white p-3 text-[13px]">
          {preview?.signature_html.trim() ? <SignatureView html={preview.signature_html} className="dark:p-0" /> : <p className="text-[12.5px] text-[#6b7280]">{ts("signatureEditor.none")}</p>}
        </div>
      </div>
    </div>
  );
}

/** The line under a value with variables on a Gmail account: kept in opendesk, with Get from Gmail when it is saved so. */
export function KeptInOpendesk({ action, className }: { action?: ReactNode; className?: string }) {
  const { t } = useTranslation("collab");
  return (
    <p className={cn("flex flex-wrap items-center gap-x-2 gap-y-1 text-[11.5px] text-fg-3", className)}>
      <Info className="size-3.5 shrink-0 text-fg-4" />
      <span className="min-w-0 flex-1">{t("sender.kept")}</span>
      {action}
    </p>
  );
}

/**
 * A Collab From name (an account's, or an address's where empty uses inherit): plain
 * words or a format naming whoever sends, with the variables offered and the From line as
 * your mail would carry it, live.
 */
export function CollabFromNameField({
  account,
  email,
  value,
  onChange,
  inherit,
  disabled,
  id,
  autoFocus,
  gmail,
}: {
  account: Pick<MailAccount, "name" | "email" | "provider">;
  /** The address it is the From name of. */
  email: string;
  value: string;
  onChange: (v: string) => void;
  /** What an empty value uses (the account's From name). */
  inherit?: string;
  disabled?: boolean;
  id?: string;
  autoFocus?: boolean;
  /** Get from Gmail, shown when the saved value has variables. */
  gmail?: ReactNode;
}) {
  const { t } = useTranslation("collab");
  const me = useUser();
  const ws = useWorkspace();
  const accountName = account.name || account.email;
  const format = value.trim() || inherit || "";
  const name = collabFromName(format, {
    agent: me.name,
    account: accountName,
    workspace: ws?.name ?? "",
  });
  return (
    <FromFormatField
      id={id}
      value={value}
      onChange={onChange}
      vars={COLLAB_FROM_VARS}
      varTitle={(v) => t(`sender.vars.${v as "agent"}`, { name: accountName })}
      ariaLabel={t("addresses.fromName")}
      placeholder={inherit}
      disabled={disabled}
      autoFocus={autoFocus}
    >
      <div className="flex flex-col gap-1.5">
        <div className="rounded-md border border-line bg-sunken/60 px-3 py-2">
          <FromPreviewLine label={t("sender.yourMail")} name={name} email={email} />
        </div>
        {account.provider === "google" && /\{(agent|agent_first|mailbox|workspace)\}/.test(value) && <KeptInOpendesk action={gmail} />}
      </div>
    </FromFormatField>
  );
}
