// A mailbox's addressing (contracts/teams.md "Addresses"): its brand, its
// aliases and their teams, the From name customers see and the team its new tickets go to.
// Shared by the add-mailbox wizard and the mailbox page.

import { Lock } from "lucide-react";
import { useTranslation } from "react-i18next";
import { ChipEditor } from "@/components/agent/ChipEditor";
import { BrandPick, Note, TeamPick, useBrands, useTeams } from "@/components/settings2/teams";
import { FromFormatField, FromPreviewLine } from "@/components/templates/FromFormatField";
import { fillFromName as fillFormat, fromNameProblem as formatProblem, HELPDESK_FROM_VARS } from "@/lib/fromname";
import { useUser } from "@/lib/session";

export const DEFAULT_FROM = "{agent} at {mailbox}";
const emailRe = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

type Vars = { agent: string; team: string; brand: string; mailbox: string };

/** The From name for a format (lib/fromname), with or without an author. */
export function fillFromName(format: string, v: Vars, withAgent: boolean): string {
  return fillFormat(format || DEFAULT_FROM, { ...v, agent: withAgent ? v.agent : "" }, HELPDESK_FROM_VARS);
}

export function fromNameProblem(format: string): "long" | "chars" | "variable" | null {
  return formatProblem(format, HELPDESK_FROM_VARS);
}

/** The From name format with variable chips and a live preview, with and without an author. */
export function FromNameField({
  value,
  onChange,
  email,
  mailboxName,
  teamId,
  brandId,
}: {
  value: string;
  onChange: (v: string) => void;
  email: string;
  mailboxName: string;
  teamId: string | null;
  brandId: string | null | undefined;
}) {
  const { t } = useTranslation("settingsChannels");
  const me = useUser();
  const { teams } = useTeams();
  const { brands, defaultBrand } = useBrands();
  const brand = brands.find((b) => b.id === brandId) ?? defaultBrand;
  const vars: Vars = { agent: me.name || "Maya Chen", team: teams.find((x) => x.id === teamId)?.name ?? "", brand: brand?.name ?? "", mailbox: mailboxName || t("addressing.previewMailbox") };
  const addr = email || "support@example.com";
  return (
    <FromFormatField value={value} onChange={onChange} vars={HELPDESK_FROM_VARS} varTitle={(v) => t(`addressing.vars.${v as "agent"}`)} ariaLabel={t("addressing.fromName")} placeholder={DEFAULT_FROM}>
      <div className="flex flex-col gap-1 rounded-md border border-line bg-sunken/60 px-3 py-2">
        <FromPreviewLine label={t("addressing.previewReply")} name={fillFromName(value, vars, true)} email={addr} />
        <FromPreviewLine label={t("addressing.previewAuto")} name={fillFromName(value, vars, false)} email={addr} />
      </div>
    </FromFormatField>
  );
}

/** Other addresses that deliver into this mailbox. */
export function AliasesField({ value, onChange, email }: { value: string[]; onChange: (v: string[]) => void; email: string }) {
  const { t } = useTranslation("settingsChannels");
  return (
    <ChipEditor
      mono
      values={value}
      onChange={onChange}
      max={20}
      normalize={(v) => {
        const x = v.trim().toLowerCase();
        return emailRe.test(x) ? x : null;
      }}
      validate={(v) => (v === email.trim().toLowerCase() ? t("addressing.aliasSame") : null)}
      placeholder="help@company.com"
      aria-label={t("addressing.aliases")}
      empty={t("addressing.noAliases")}
    />
  );
}

/**
 * The alias teams to save: only aliases that still exist, and none on a private
 * team's address (it delivers every alias to that team).
 */
export function aliasTeamsFor(value: Record<string, string>, aliases: string[], teamPrivate: boolean): Record<string, string> {
  if (teamPrivate) return {};
  return Object.fromEntries(aliases.filter((a) => value[a]).map((a) => [a, value[a]]));
}

export function sameAliasTeams(a: Record<string, string>, b: Record<string, string>) {
  const ka = Object.keys(a);
  return ka.length === Object.keys(b).length && ka.every((k) => a[k] === b[k]);
}

/** Where new tickets to each alias go: the mailbox's team, or one of their own. */
export function AliasTeamsField({
  aliases,
  value,
  onChange,
  teamId,
}: {
  aliases: string[];
  value: Record<string, string>;
  onChange: (v: Record<string, string>) => void;
  /** The mailbox's own team. */
  teamId: string | null;
}) {
  const { t } = useTranslation("settingsChannels");
  const { teams } = useTeams();
  if (!aliases.length || !teams.length) return null;
  const team = teams.find((x) => x.id === teamId);
  if (team?.private) {
    return (
      <Note tone="violet" icon={<Lock className="mt-0.5 size-3.5 shrink-0" aria-hidden />}>
        {t("addressing.aliasTeamsPrivate", { name: team.name })}
      </Note>
    );
  }
  const set = (alias: string, id: string | null) => {
    const next = { ...value };
    if (id && id !== teamId) next[alias] = id;
    else delete next[alias];
    onChange(next);
  };
  return (
    <div className="flex flex-col gap-1.5">
      <span className="text-[12px] text-fg-3">{t("addressing.aliasTeams")}</span>
      <ul className="flex flex-col divide-y divide-line rounded-md border border-line">
        {aliases.map((a) => (
          <li key={a} className="flex items-center gap-3 px-3 py-1.5">
            <span className="mono min-w-0 flex-1 truncate text-[12px] text-fg-2" title={a}>
              {a}
            </span>
            <TeamPick
              className="w-[220px]"
              value={value[a] ?? null}
              noneLabel={team ? t("addressing.aliasTeamSame", { name: team.name }) : t("addressing.aliasTeamNone")}
              onChange={(id) => set(a, id)}
            />
          </li>
        ))}
      </ul>
    </div>
  );
}

/** "New tickets to this address go to", with a note when the team is private. */
export function MailboxTeamField({ value, onChange, className }: { value: string | null; onChange: (id: string | null) => void; className?: string }) {
  const { t } = useTranslation("settingsChannels");
  const { teams } = useTeams();
  const team = teams.find((x) => x.id === value);
  return (
    <div className="flex flex-col gap-2">
      <TeamPick className={className} value={value} noneLabel={t("addressing.noTeam")} onChange={onChange} />
      {team?.private && (
        <Note tone="violet" icon={<Lock className="mt-0.5 size-3.5 shrink-0" aria-hidden />}>
          {t("addressing.privateTeam", { name: team.name })}
        </Note>
      )}
    </div>
  );
}

/** The brand picker, shown only once there is a second brand. */
export function MailboxBrandField({ value, onChange, className }: { value: string | null | undefined; onChange: (id: string) => void; className?: string }) {
  const { multi, defaultBrand } = useBrands();
  if (!multi) return null;
  return <BrandPick className={className} value={value || defaultBrand?.id} onChange={onChange} />;
}
