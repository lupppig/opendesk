// Replies on this address's tickets from members' own Collab addresses (contracts/collab.md
// "Answering tickets"): whether they are allowed, where answers go by default and which
// addresses may be used. What is left on "Workspace default" follows Settings → General.
// Only offered with Collab.

import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { Row, Section } from "@/components/layout";
import { Segmented } from "@/components/ui/tabs";
import type { MailboxPersonalReplies } from "@/lib/api/types";
import { q } from "@/lib/queries";

export const PERSONAL_DEFAULTS: MailboxPersonalReplies = { enabled: null, reply_to: null, accounts: "any" };

/** The address's own domain and its aliases', lower case, once each. */
export function mailboxDomains(email: string, aliases: string[] = []): string[] {
  return [...new Set([email, ...aliases].map((a) => a.split("@")[1]?.toLowerCase()).filter((d): d is string => !!d))];
}

export function MailboxPersonalSection({
  value,
  domains,
  onChange,
}: {
  value: MailboxPersonalReplies;
  domains: string[];
  onChange: (v: MailboxPersonalReplies) => void;
}) {
  const { t } = useTranslation("settingsChannels");
  const { data: collab } = useQuery(q.collabStatus());
  const { data: workspace } = useQuery(q.workspace());
  if (!collab?.enabled) return null;
  const wsOn = workspace?.settings.collab_ticket_replies !== false;
  const wsReplyTo = workspace?.settings.collab_ticket_reply_to ?? "address";
  const on = value.enabled ?? wsOn;
  const replyToLabel = (v: "address" | "mailbox") => (v === "address" ? t("personal.replyToAddress") : t("personal.replyToMailbox"));

  return (
    <Section
      title={t("personal.title")}
      description={
        <>
          {t("personal.description")}{" "}
          <Link to="/settings/workspace" className="font-medium text-fg-2 underline decoration-line-strong underline-offset-2 hover:text-fg">
            {t("personal.workspaceLink")}
          </Link>
        </>
      }
    >
      <Row label={t("personal.allowed")} description={on ? t("personal.allowedOn") : t("personal.allowedOff")}>
        <Segmented
          size="xs"
          value={value.enabled === null ? "default" : value.enabled ? "on" : "off"}
          onChange={(v: string) => onChange({ ...value, enabled: v === "default" ? null : v === "on" })}
          options={[
            { value: "default", label: t("personal.workspaceDefault", { value: wsOn ? t("personal.on") : t("personal.off") }) },
            { value: "on", label: t("personal.on") },
            { value: "off", label: t("personal.off") },
          ]}
        />
      </Row>
      {on && (
        <>
          <Row label={t("personal.replyTo")} description={t("personal.replyToHint")}>
            <Segmented
              size="xs"
              value={value.reply_to ?? "default"}
              onChange={(v: string) => onChange({ ...value, reply_to: v === "default" ? null : (v as "address" | "mailbox") })}
              options={[
                { value: "default", label: t("personal.workspaceDefault", { value: replyToLabel(wsReplyTo) }) },
                { value: "address", label: replyToLabel("address") },
                { value: "mailbox", label: replyToLabel("mailbox") },
              ]}
            />
          </Row>
          <Row
            label={t("personal.accounts")}
            description={
              value.accounts === "shared"
                ? t("personal.accountsShared")
                : value.accounts === "domain"
                  ? t("personal.accountsDomain", { domains: domains.join(", ") })
                  : t("personal.accountsAny")
            }
          >
            <Segmented
              size="xs"
              value={value.accounts}
              onChange={(v: MailboxPersonalReplies["accounts"]) => onChange({ ...value, accounts: v })}
              options={[
                { value: "any", label: t("personal.any") },
                { value: "shared", label: t("personal.shared") },
                { value: "domain", label: t("personal.domain", { domain: domains[0] ?? "" }) },
              ]}
            />
          </Row>
        </>
      )}
    </Section>
  );
}
