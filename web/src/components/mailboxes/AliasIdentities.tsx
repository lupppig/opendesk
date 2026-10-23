// Each alias of a mailbox as its own identity (contracts/teams.md "Aliases"): its sender
// name, brand, team and signature, whether replies go out From it, a way into its tickets,
// and Gmail's other send-as addresses to add.

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ArrowUpRight, ChevronDown, Plus, RefreshCw } from "lucide-react";
import { useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { BrandPick, Note, TeamPick, useBrands, useTeams } from "@/components/settings2/teams";
import { SignatureEditor } from "@/components/templates/SignatureEditor";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, TextInput } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast";
import { api } from "@/lib/api/endpoints";
import type { AliasSendAs, AliasSettingsInput, Mailbox, TemplatePreviewBody, TemplateProblem } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { relLong } from "@/lib/format";
import { qk, upsertInList } from "@/lib/queries";

/** What the page edits of one alias. */
export type AliasDraft = { display_name: string; signature_html: string; brand_id: string | null; send_as: AliasSendAs };

export function aliasDraftsFor(m: Mailbox): Record<string, AliasDraft> {
  return Object.fromEntries(
    (m.aliases ?? []).map((a) => {
      const s = m.alias_settings?.[a];
      return [a, { display_name: s?.display_name ?? "", signature_html: s?.signature_html ?? "", brand_id: s?.brand_id ?? null, send_as: s?.send_as ?? "unknown" }];
    }),
  );
}

const emptyDraft: AliasDraft = { display_name: "", signature_html: "", brand_id: null, send_as: "unknown" };

/**
 * The alias_settings to save for the aliases kept, or null when nothing changed. A signature
 * goes only where it was edited (the server keeps the stored one), send_as only where the
 * admin changed it: Gmail's list or Microsoft's refusal decide otherwise.
 */
export function aliasSettingsPatch(drafts: Record<string, AliasDraft>, aliases: string[], base: Record<string, AliasDraft>): Record<string, AliasSettingsInput> | null {
  let changed = aliases.some((a) => !base[a]) || Object.keys(base).some((a) => !aliases.includes(a));
  const out: Record<string, AliasSettingsInput> = {};
  for (const a of aliases) {
    const d = drafts[a] ?? emptyDraft;
    const b = base[a] ?? emptyDraft;
    if (d.display_name !== b.display_name || d.signature_html !== b.signature_html || d.brand_id !== b.brand_id || d.send_as !== b.send_as) changed = true;
    out[a] = {
      display_name: d.display_name.trim(),
      brand_id: d.brand_id,
      ...(d.signature_html !== b.signature_html ? { signature_html: d.signature_html } : {}),
      ...(d.send_as !== b.send_as ? { send_as: d.send_as } : {}),
    };
  }
  return changed ? out : null;
}

/** Whether replies to the alias go out From it, as the server decides (contracts/teams.md "Aliases"). */
function sendsAs(m: Mailbox, send: AliasSendAs) {
  if (m.outbound_kind === "smtp") return true;
  if (m.outbound_kind === "google") return send === "verified";
  if (m.outbound_kind === "microsoft") return send !== "unverified";
  return false;
}

/** Every alias, each opening into its own identity. */
export function AliasIdentities({
  mailbox,
  aliases,
  drafts,
  onChange,
  aliasTeams,
  onAliasTeams,
  teamId,
  systemSender,
  problems,
}: {
  mailbox: Mailbox;
  aliases: string[];
  drafts: Record<string, AliasDraft>;
  onChange: (alias: string, d: AliasDraft) => void;
  aliasTeams: Record<string, string>;
  onAliasTeams: (v: Record<string, string>) => void;
  teamId: string | null;
  systemSender: string;
  problems: (alias: string) => TemplateProblem[];
}) {
  const { t } = useTranslation("settingsChannels");
  const [open, setOpen] = useState<string | null>(null);
  if (!aliases.length) return null;
  return (
    <ul className="flex flex-col divide-y divide-line rounded-md border border-line" aria-label={t("aliases.identities")}>
      {aliases.map((a) => (
        <AliasRow
          key={a}
          mailbox={mailbox}
          alias={a}
          value={drafts[a] ?? emptyDraft}
          onChange={(d) => onChange(a, d)}
          team={aliasTeams[a] ?? null}
          onTeam={(id) => {
            const next = { ...aliasTeams };
            if (id && id !== teamId) next[a] = id;
            else delete next[a];
            onAliasTeams(next);
          }}
          teamId={teamId}
          systemSender={systemSender}
          problems={problems(a)}
          open={open === a}
          onToggle={() => setOpen((o) => (o === a ? null : a))}
        />
      ))}
    </ul>
  );
}

function AliasRow({
  mailbox,
  alias,
  value,
  onChange,
  team,
  onTeam,
  teamId,
  systemSender,
  problems,
  open,
  onToggle,
}: {
  mailbox: Mailbox;
  alias: string;
  value: AliasDraft;
  onChange: (d: AliasDraft) => void;
  team: string | null;
  onTeam: (id: string | null) => void;
  teamId: string | null;
  systemSender: string;
  problems: TemplateProblem[];
  open: boolean;
  onToggle: () => void;
}) {
  const { t } = useTranslation("settingsChannels");
  const { multi, brands } = useBrands();
  const { teams } = useTeams();
  const saved = (mailbox.aliases ?? []).includes(alias);
  const mailboxTeam = teams.find((x) => x.id === teamId);
  const brand = brands.find((b) => b.id === (value.brand_id ?? mailbox.brand_id));
  // An alias not saved yet takes Gmail's word from the list the connection read.
  const listed = mailbox.provider_send_as?.readable && !saved ? mailbox.provider_send_as.addresses.find((x) => x.email === alias) : undefined;
  const self = sendsAs(mailbox, listed?.verified ? "verified" : value.send_as);
  const set = (p: Partial<AliasDraft>) => onChange({ ...value, ...p });
  // What the preview's check found wrong with the alias's own signature, before a save refuses it.
  const [checked, setChecked] = useState<TemplateProblem[]>([]);
  const own = value.signature_html.trim() ? checked : [];
  // As a reply to the alias carries it: the alias's own, else the address's.
  const signatureHtml = value.signature_html || mailbox.signature_html;
  const previewBody = useMemo<TemplatePreviewBody>(() => ({ kind: "signature", mailbox_id: mailbox.id, alias, body_html: signatureHtml }), [mailbox.id, alias, signatureHtml]);
  const id = `alias-${alias.replace(/[^a-z0-9]/g, "-")}`;
  return (
    <li>
      <div className="flex items-center gap-2 px-3 py-2">
        <button
          type="button"
          onClick={onToggle}
          aria-expanded={open}
          aria-controls={id}
          aria-label={t("aliases.edit", { alias })}
          className="flex min-w-0 flex-1 items-center gap-2 rounded-sm text-left outline-none focus-visible:shadow-[0_0_0_2px_var(--focus)]"
        >
          <ChevronDown className={cn("size-3.5 shrink-0 text-fg-4 transition-transform duration-150 motion-reduce:transition-none", !open && "-rotate-90")} aria-hidden />
          <span className="mono min-w-0 truncate text-[12.5px] text-fg" title={alias}>
            {alias}
          </span>
          {value.display_name && <span className="hidden min-w-0 truncate text-[12px] text-fg-3 sm:inline">{value.display_name}</span>}
          {multi && brand && brand.id !== mailbox.brand_id && brand.name !== value.display_name && <Badge tone="outline">{brand.name}</Badge>}
        </button>
        {mailbox.outbound_kind !== "system" && (
          <Badge tone={self ? "ok" : "neutral"} className="max-sm:hidden">
            {self ? t("aliases.badgeSelf") : t("aliases.badgeVia", { email: mailbox.email })}
          </Badge>
        )}
        {saved && (
          <Button size="xs" variant="ghost" asChild>
            <Link to="/inbox/$folder/{-$ticketId}" params={{ folder: "open", ticketId: undefined }} search={{ to: alias }}>
              {t("aliases.openTickets")}
              <ArrowUpRight className="size-3" aria-hidden />
            </Link>
          </Button>
        )}
      </div>
      {open && (
        <div id={id} className="flex animate-rise-in flex-col gap-3.5 border-t border-line-soft bg-sunken/40 px-3 py-3.5 motion-reduce:animate-none">
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label={t("aliases.senderName")} hint={t("aliases.senderNameHint")}>
              {({ id: fid }) => (
                <TextInput id={fid} value={value.display_name} maxLength={64} placeholder={mailbox.display_name || mailbox.name} onChange={(e) => set({ display_name: e.target.value })} />
              )}
            </Field>
            {teams.length > 0 && !mailboxTeam?.private && (
              <Field label={t("aliases.team")}>
                {() => (
                  <TeamPick
                    value={team}
                    noneLabel={mailboxTeam ? t("addressing.aliasTeamSame", { name: mailboxTeam.name }) : t("addressing.aliasTeamNone")}
                    onChange={onTeam}
                  />
                )}
              </Field>
            )}
            {multi && (
              <Field label={t("aliases.brand")} hint={t("aliases.brandHint")}>
                {() => <BrandPick value={value.brand_id ?? mailbox.brand_id} onChange={(b) => set({ brand_id: b === mailbox.brand_id ? null : b })} />}
              </Field>
            )}
          </div>
          <SendAsRow mailbox={mailbox} value={value.send_as} onChange={(send_as) => set({ send_as })} systemSender={systemSender} />
          <div className="flex flex-col gap-1.5">
            <div className="flex flex-col gap-0.5">
              <span className="text-[12.5px] font-medium">{t("aliases.signature")}</span>
              <span className="text-[11.5px] leading-4 text-fg-4">{t("aliases.signatureHint")}</span>
            </div>
            <SignatureEditor
              kind="signature"
              value={value.signature_html}
              onChange={(v) => set({ signature_html: v })}
              placeholder={t("aliases.signaturePlaceholder")}
              ariaLabel={t("aliases.signatureLabel", { alias })}
              minHeight={72}
              problems={own.length ? own : problems}
              onProblems={setChecked}
              previewTitle={t("detail.preview")}
              previewHeight={380}
              previewBody={saved ? previewBody : undefined}
            />
          </div>
        </div>
      )}
    </li>
  );
}

/** How replies to the alias go out, and the admin's say where no provider list decides. */
function SendAsRow({ mailbox, value, onChange, systemSender }: { mailbox: Mailbox; value: AliasSendAs; onChange: (v: AliasSendAs) => void; systemSender: string }) {
  const { t } = useTranslation("settingsChannels");
  const email = mailbox.email;
  let text: string;
  let control: ReactNode = null;
  switch (mailbox.outbound_kind) {
    case "smtp":
      text = t("aliases.sendAs.smtp");
      break;
    case "system":
      text = t("aliases.sendAs.system", { sender: systemSender });
      break;
    case "google":
      if (mailbox.provider_send_as?.readable) {
        text = value === "verified" ? t("aliases.sendAs.gmailVerified") : t("aliases.sendAs.gmailUnverified", { email });
      } else {
        text = value === "verified" ? t("aliases.sendAs.gmailClaimed") : t("aliases.sendAs.replyTo", { email });
        control = (
          <label className="flex items-center gap-2 text-[12px] text-fg-2">
            <Switch aria-label={t("aliases.sendAs.gmailClaim")} checked={value === "verified"} onCheckedChange={(on) => onChange(on ? "verified" : "unknown")} />
            {t("aliases.sendAs.gmailClaim")}
          </label>
        );
      }
      break;
    default:
      text = value === "unverified" ? t("aliases.sendAs.microsoftRefused", { email }) : t("aliases.sendAs.microsoft", { email });
      if (value === "unverified")
        control = (
          <Button size="xs" icon={<RefreshCw className="size-3" />} onClick={() => onChange("unknown")}>
            {t("aliases.sendAs.tryAgain")}
          </Button>
        );
  }
  return (
    <div className="flex flex-col gap-1.5">
      <span className="text-[12.5px] font-medium">{t("aliases.sendAs.label")}</span>
      <p className="text-[12px] leading-[18px] text-fg-2">{text}</p>
      {mailbox.outbound_kind === "google" && !mailbox.provider_send_as?.readable && value !== "verified" && (
        <p className="text-[11.5px] leading-4 text-fg-4">{t("aliases.sendAs.gmailHow")}</p>
      )}
      {control}
    </div>
  );
}

/**
 * Gmail's other send-as addresses, offered as aliases to add: read when the connection may
 * read the list, else a plain note that the admin adds them by hand.
 */
export function GmailSuggestions({ mailbox, aliases, onAdd }: { mailbox: Mailbox; aliases: string[]; onAdd: (alias: string, name: string) => void }) {
  const { t } = useTranslation("settingsChannels");
  const qc = useQueryClient();
  const check = useMutation({
    mutationFn: () => api.mailboxes.checkSendAs(mailbox.id),
    onSuccess: (m) => {
      upsertInList(qc, qk.mailboxes, m);
      toast.success(t("aliases.gmail.checked"));
    },
    onError: (e) => toast.error(e),
  });
  const ps = mailbox.provider_send_as;
  if (!ps) return null;
  if (!ps.readable) return <Note>{t("aliases.gmail.sendOnly")}</Note>;
  const offered = ps.addresses.filter((x) => !aliases.includes(x.email) && x.email !== mailbox.email);
  const full = aliases.length >= 20;
  return (
    <div className="flex flex-col gap-2 rounded-md border border-line px-3 py-2.5">
      <div className="flex items-center gap-2">
        <div className="min-w-0 flex-1">
          <div className="text-[12.5px] font-medium">{t("aliases.gmail.title")}</div>
          <p className="text-[11.5px] leading-4 text-fg-3">
            {t("aliases.gmail.hint", { account: mailbox.config.oauth_email ?? mailbox.email })}
            {ps.checked_at && <> {t("aliases.gmail.checkedAt", { when: relLong(ps.checked_at) })}</>}
          </p>
        </div>
        <Button size="xs" icon={<RefreshCw className="size-3" />} loading={check.isPending} onClick={() => check.mutate()}>
          {t("aliases.gmail.check")}
        </Button>
      </div>
      {offered.length === 0 ? (
        <p className="text-[12px] text-fg-3">{t("aliases.gmail.none")}</p>
      ) : (
        <ul className="flex flex-col gap-1" aria-label={t("aliases.gmail.title")}>
          {offered.map((x) => (
            <li key={x.email} className="flex items-center gap-2 text-[12px]">
              <span className="mono min-w-0 truncate text-fg-2">{x.email}</span>
              {x.display_name && <span className="min-w-0 truncate text-fg-4">{x.display_name}</span>}
              <span className="ml-auto shrink-0">
                {x.verified ? (
                  <Button size="xs" variant="ghost" icon={<Plus className="size-3" />} disabled={full} aria-label={t("aliases.gmail.addLabel", { email: x.email })} onClick={() => onAdd(x.email, x.display_name)}>
                    {t("aliases.gmail.add")}
                  </Button>
                ) : (
                  <span className="text-[11.5px] text-fg-4">{t("aliases.gmail.pending")}</span>
                )}
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
