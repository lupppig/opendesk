import { i18n, inSentence } from "@/i18n";
import type { ParseKeys } from "i18next";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Field, Textarea, TextInput } from "@/components/ui/input";
import { Checkbox } from "@/components/ui/checkbox";
import { Switch } from "@/components/ui/switch";
import { CopyField } from "@/components/ui/copy-button";
import { PrivateTeamsField } from "./teams";
import { api } from "@/lib/api/endpoints";
import { errorMessage } from "@/lib/api/client";
import type { Webhook } from "@/lib/api/types";
import { qk, upsertInList } from "@/lib/queries";

/** The events catalog (contracts/events.md), grouped by resource. */
export const eventGroups: { label: ParseKeys<"ui">; events: string[] }[] = [
  { label: "webhookGroups.tickets", events: ["ticket.created", "ticket.updated", "ticket.deleted"] },
  { label: "webhookGroups.messages", events: ["message.created", "message.updated", "message.withdrawn"] },
  { label: "webhookGroups.activity", events: ["activity.created"] },
  { label: "webhookGroups.contacts", events: ["contact.created", "contact.updated", "contact.deleted"] },
  { label: "webhookGroups.companies", events: ["company.created", "company.updated", "company.deleted"] },
  { label: "webhookGroups.labels", events: ["tag.created", "tag.updated", "tag.deleted"] },
  { label: "webhookGroups.mailboxes", events: ["mailbox.created", "mailbox.updated", "mailbox.deleted"] },
  { label: "webhookGroups.members", events: ["member.added", "member.updated", "member.removed"] },
  { label: "webhookGroups.knowledgeBase", events: ["kb_article.created", "kb_article.updated", "kb_article.deleted", "kb_collection.created", "kb_collection.updated", "kb_collection.deleted"] },
  { label: "webhookGroups.changelog", events: ["changelog_entry.created", "changelog_entry.updated", "changelog_entry.deleted"] },
  { label: "webhookGroups.wiki", events: ["wiki_page.created", "wiki_page.updated", "wiki_page.moved", "wiki_page.deleted"] },
  { label: "webhookGroups.savedReplies", events: ["saved_reply.created", "saved_reply.updated", "saved_reply.deleted"] },
  { label: "webhookGroups.views", events: ["view.created", "view.updated", "view.deleted"] },
  { label: "webhookGroups.automations", events: ["automation.created", "automation.updated", "automation.deleted"] },
  { label: "webhookGroups.slaPolicies", events: ["sla_policy.created", "sla_policy.updated", "sla_policy.deleted"] },
  { label: "webhookGroups.teams", events: ["team.created", "team.updated", "team.deleted"] },
];
export const allEvents = eventGroups.flatMap((g) => g.events);

/** The server stores "every event, including ones added later" as ["*"]. */
export function isAllEvents(events: string[]) {
  return events.includes("*") || allEvents.every((e) => events.includes(e));
}

function expandEvents(events: string[]) {
  return events.includes("*") ? allEvents : events;
}

export function eventSummary(events: string[]) {
  if (isAllEvents(events)) return i18n.t("allEvents", { ns: "ui" });
  return events.length === 1 ? events[0] : i18n.t("eventCount", { ns: "ui", count: events.length });
}

function validUrl(v: string): string | null {
  try {
    const u = new URL(v.trim());
    // http is accepted only for a local receiver; the server allows it on development instances only.
    const local = u.hostname === "localhost" || u.hostname === "[::1]" || /^127\.\d+\.\d+\.\d+$/.test(u.hostname);
    if (u.protocol !== "https:" && !(u.protocol === "http:" && local)) return i18n.t("httpsUrl", { ns: "ui" });
    return null;
  } catch {
    return i18n.t("fullUrl", { ns: "ui" });
  }
}

function parseHeaders(raw: string): Record<string, string> | null {
  const headers: Record<string, string> = {};
  for (const line of raw.split("\n").filter(Boolean)) {
    const i = line.indexOf(":");
    const name = line.slice(0, i).trim();
    const value = line.slice(i + 1).trim();
    if (i < 1 || !name || !value || headers[name]) return null;
    headers[name] = value;
  }
  return headers;
}

export function WebhookDialog({ hook, onClose, onCreated }: { hook: Webhook | null; onClose: () => void; onCreated?: (w: Webhook & { secret: string }) => void }) {
  const { t: ui } = useTranslation("ui");
  const { t } = useTranslation("settingsDev");
  const qc = useQueryClient();
  const [url, setUrl] = useState(hook?.url ?? "");
  const [description, setDescription] = useState(hook?.description ?? "");
  const [events, setEvents] = useState<string[]>(hook ? expandEvents(hook.events) : ["ticket.created", "ticket.updated", "message.created"]);
  const [enabled, setEnabled] = useState(hook?.enabled ?? true);
  const [privateTeams, setPrivateTeams] = useState<string[]>(hook?.private_team_ids ?? []);
  const [headers, setHeaders] = useState("");
  const [replaceHeaders, setReplaceHeaders] = useState(!hook);
  const [error, setError] = useState<string | null>(null);
  const [urlError, setUrlError] = useState<string | null>(null);
  const [headersError, setHeadersError] = useState<string | null>(null);
  const all = allEvents.every((e) => events.includes(e));

  const save = useMutation({
    mutationFn: async (customHeaders?: Record<string, string>) => {
      const changedTeams = JSON.stringify(privateTeams) !== JSON.stringify(hook?.private_team_ids ?? []);
      const body = {
        url: url.trim(),
        description: description.trim(),
        events: all ? ["*"] : events,
        enabled,
        ...(customHeaders !== undefined ? { headers: customHeaders } : {}),
        ...(changedTeams ? { private_team_ids: privateTeams } : {}),
      };
      if (hook) return { w: await api.webhooks.update(hook.id, body), secret: null };
      const created = await api.webhooks.create(body);
      return { w: created as Webhook, secret: created.secret };
    },
    onSuccess: ({ w, secret }) => {
      const { secret: _s, ...row } = w as Webhook & { secret?: string };
      upsertInList(qc, qk.webhooks, row as Webhook, { prepend: true });
      if (secret && onCreated) onCreated({ ...(row as Webhook), secret });
      else onClose();
    },
    onError: (e) => setError(errorMessage(e)),
  });

  const submit = () => {
    setError(null);
    setHeadersError(null);
    const ue = validUrl(url);
    setUrlError(ue);
    if (ue) return;
    if (!events.length) return setError(t("webhookForm.eventRequired"));
    const customHeaders = replaceHeaders ? parseHeaders(headers) : undefined;
    if (customHeaders === null) return setHeadersError(t("webhookForm.headersInvalid"));
    save.mutate(customHeaders);
  };

  const toggleGroup = (g: string[], on: boolean) =>
    setEvents((cur) => (on ? [...new Set([...cur, ...g])] : cur.filter((e) => !g.includes(e))));

  return (
    <Dialog
      open
      size="lg"
      onOpenChange={(o) => !o && onClose()}
      title={hook ? t("webhookForm.editTitle") : t("webhookForm.newTitle")}
      description={t("webhookForm.description")}
      footer={
        <>
          <span className="mr-auto text-xs text-danger-fg">{error}</span>
          <Button variant="ghost" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button variant="primary" loading={save.isPending} onClick={submit}>
            {hook ? t("webhookForm.save") : t("webhookForm.create")}
          </Button>
        </>
      }
    >
      <form
        className="flex flex-col gap-4 pb-1"
        onSubmit={(e) => {
          e.preventDefault();
          submit();
        }}
      >
        <Field label={t("webhookForm.url")} error={urlError}>
          {(p) => (
            <TextInput
              {...p}
              autoFocus
              className="mono"
              value={url}
              placeholder="https://example.com/hooks/opendesk"
              onChange={(e) => {
                setUrl(e.target.value);
                setUrlError(null);
              }}
              onBlur={() => url && setUrlError(validUrl(url))}
            />
          )}
        </Field>
        <Field label={t("webhookForm.descriptionLabel")} optional>
          {(p) => <TextInput {...p} value={description} maxLength={200} placeholder={t("webhookForm.descriptionPlaceholder")} onChange={(e) => setDescription(e.target.value)} />}
        </Field>
        {hook?.header_names.length ? <p className="text-[12px] text-fg-3">{t("webhookForm.headersConfigured", { headers: hook.header_names.join(", ") })}</p> : null}
        {hook ? (
          <label className="flex items-center gap-2 text-[12.5px] text-fg-2">
            <Checkbox checked={replaceHeaders} onCheckedChange={setReplaceHeaders} />
            {t("webhookForm.replaceHeaders")}
          </label>
        ) : null}
        {replaceHeaders ? (
          <Field label={t("webhookForm.headers")} optional error={headersError} hint={t("webhookForm.headersHint")}>
            {(p) => (
              <Textarea
                {...p}
                rows={3}
                spellCheck={false}
                className="mono text-[12px]"
                value={headers}
                placeholder="Authorization: Bearer …\nX-Account-ID: acme"
                onChange={(e) => {
                  setHeaders(e.target.value);
                  setHeadersError(null);
                }}
              />
            )}
          </Field>
        ) : null}
        <div className="flex flex-col gap-2">
          <div className="flex items-center justify-between">
            <span className="text-[12.5px] font-medium">
              {t("webhookForm.events")} <span className="mono ml-1 text-[11px] font-normal tabular-nums text-fg-4">{all ? allEvents.length : events.length}/{allEvents.length}</span>
            </span>
            <label className="flex items-center gap-2 text-[12px] text-fg-2">
              <Checkbox checked={all ? true : events.length ? "indeterminate" : false} onCheckedChange={(on) => setEvents(on ? allEvents : [])} aria-label={ui("allEvents")} />
              {t("webhooks.allEvents")}
            </label>
          </div>
          <div className="grid max-h-[300px] grid-cols-1 overflow-y-auto rounded-md border border-line sm:grid-cols-2">
            {eventGroups.map((g) => {
              const on = g.events.filter((e) => events.includes(e)).length;
              return (
                <div key={g.label} className="border-b border-line-soft p-3 sm:odd:border-r">
                  <label className="mb-1.5 flex items-center gap-2 text-[12.5px] font-medium">
                    <Checkbox
                      checked={on === g.events.length ? true : on ? "indeterminate" : false}
                      onCheckedChange={(v) => toggleGroup(g.events, v)}
                      aria-label={ui("allGroupEvents", { group: inSentence(ui(g.label)) })}
                    />
                    {ui(g.label)}
                  </label>
                  <div className="flex flex-col gap-1 pl-6">
                    {g.events.map((e) => (
                      <label key={e} className="mono flex items-center gap-2 text-[11.5px] text-fg-2">
                        <Checkbox checked={events.includes(e)} onCheckedChange={(v) => toggleGroup([e], v)} aria-label={e} />
                        {e}
                      </label>
                    ))}
                  </div>
                </div>
              );
            })}
          </div>
        </div>
        <div className="flex items-center justify-between rounded-md border border-line px-3 py-2.5">
          <div>
            <div className="text-[13px] font-medium">{t("webhookForm.active")}</div>
            <div className="text-[12px] text-fg-3">{t("webhookForm.activeHint")}</div>
          </div>
          <Switch checked={enabled} onCheckedChange={setEnabled} aria-label={ui("webhookActive")} />
        </div>
        <PrivateTeamsField value={privateTeams} onChange={setPrivateTeams} grants={hook?.private_teams} />
        <button type="submit" className="hidden" />
      </form>
    </Dialog>
  );
}

export function SecretDialog({ secret, onClose }: { secret: string | null; onClose: () => void }) {
  const { t: ui } = useTranslation("ui");
  const { t } = useTranslation("settingsDev");
  const verify = `const [t, v1] = header.split(",").map((p) => p.split("=")[1]);
const expected = hmacSha256Hex(secret, \`\${t}.\${rawBody}\`);
const ok = timingSafeEqual(expected, v1) && Math.abs(Date.now() / 1000 - Number(t)) < 300;`;
  return (
    <Dialog
      open={!!secret}
      onOpenChange={(o) => !o && onClose()}
      size="lg"
      title={t("secret.title")}
      description={t("secret.description")}
      footer={
        <Button variant="primary" onClick={onClose}>
          {t("secret.saved")}
        </Button>
      }
    >
      <div className="flex flex-col gap-4 pb-1">
        <CopyField value={secret ?? ""} />
        <div className="flex flex-col gap-2">
          <p className="text-[12.5px] text-fg-2">{t("secret.verify")}</p>
          <CopyField multiline value={`OD-Signature: t=<unix>,v1=<hex hmac_sha256(secret, "<t>.<body>")>`} />
          <CopyField multiline value={verify} />
          <p className="text-[12px] text-fg-3">
            <Trans t={ui} i18nKey="deliveryHeaders" components={{ code: <span className="mono text-fg-2" /> }} />
          </p>
        </div>
      </div>
    </Dialog>
  );
}
