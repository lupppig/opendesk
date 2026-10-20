import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AtSign, Hash, KeyRound, Link2, MessagesSquare, Plus, RefreshCw, Sparkles, StickyNote, Trash2 } from "lucide-react";
import { useId, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { AppMark } from "@/components/appmark";
import { Section, SettingsPage } from "@/components/layout";
import { type ItemState, ListSkeleton, StatePill } from "@/components/settings2/bits";
import { SettingsEmpty } from "@/components/settings2/SettingsEmpty";
import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/ui/confirm";
import { CopyField } from "@/components/ui/copy-button";
import { Dialog } from "@/components/ui/dialog";
import { Field, TextInput } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast";
import { errorMessage } from "@/lib/api/client";
import { api } from "@/lib/api/endpoints";
import type { SupportChannelAccount, SupportChannelConnection, SupportChannelDestination, SupportChannelProvider } from "@/lib/api/types";
import { q, qk, upsertInList } from "@/lib/queries";
import { atLeast, useRole } from "@/lib/session";

// Providers and credential fields with their own copy; another provider shows
// the name, description and labels its adapter describes.
const PROVIDERS = ["slack", "discord", "telegram"] as const;
type ProviderKey = (typeof PROVIDERS)[number];
const FIELDS = ["bot_token", "signing_secret", "public_key", "guild_id", "webhook_secret"] as const;
type FieldKey = (typeof FIELDS)[number];
const knownProvider = (k: string): k is ProviderKey => (PROVIDERS as readonly string[]).includes(k);
const knownField = (k: string): k is FieldKey => (FIELDS as readonly string[]).includes(k);

function useProviderCopy() {
  const { t } = useTranslation("settingsChannels");
  return {
    description: (p: SupportChannelProvider) => (knownProvider(p.key) ? t(`supportChannels.provider.${p.key}.description`) : p.description),
    setup: (p: SupportChannelProvider) => (knownProvider(p.key) ? t(`supportChannels.provider.${p.key}.setup`) : p.description),
    field: (f: SupportChannelProvider["fields"][number]) => (knownField(f.name) ? t(`supportChannels.field.${f.name}`) : f.label),
    help: (p: SupportChannelProvider, f: SupportChannelProvider["fields"][number]) => (knownProvider(p.key) ? undefined : f.help),
    noChannels: (key: string) => (knownProvider(key) ? t(`supportChannels.provider.${key}.noChannels`) : t("supportChannels.destination.none")),
  };
}

// One state for a connection: a refused subscription first, then its last check.
type StateKey = "supportChannels.status.error" | `supportChannels.health.${SupportChannelConnection["health_status"]}`;
function connectionState(c: SupportChannelConnection): { state: ItemState; key: StateKey } {
  if (c.status === "error") return { state: "failing", key: "supportChannels.status.error" };
  switch (c.health_status) {
    case "healthy":
      return { state: "active", key: "supportChannels.health.healthy" };
    case "action_required":
      return { state: "pending", key: "supportChannels.health.action_required" };
    case "error":
      return { state: "failing", key: "supportChannels.health.error" };
    default:
      return { state: "disabled", key: "supportChannels.health.unknown" };
  }
}

export default function SupportChannels() {
  const { t } = useTranslation("settingsChannels");
  const role = useRole();
  const admin = atLeast(role, "admin");
  const canLink = atLeast(role, "agent");
  const qc = useQueryClient();
  const confirm = useConfirm();
  const copy = useProviderCopy();
  const { data: providers } = useQuery(q.supportChannelProviders());
  const { data: connections, isLoading } = useQuery(q.supportChannelConnections());
  const { data: destinations } = useQuery(q.supportChannelDestinations());
  const { data: teams } = useQuery(q.teams());
  const [connecting, setConnecting] = useState<SupportChannelProvider | null>(null);
  const [routing, setRouting] = useState<string | null>(null);
  const [rotating, setRotating] = useState<SupportChannelConnection | null>(null);
  const [link, setLink] = useState<{ code: string; expires_at: string } | null>(null);

  const disconnect = useMutation({
    mutationFn: api.supportChannels.disconnect,
    onSuccess: (_, id) => {
      qc.setQueryData<SupportChannelConnection[]>(qk.supportChannelConnections, (old) => old?.filter((x) => x.id !== id));
      qc.setQueryData<SupportChannelDestination[]>(qk.supportChannelDestinations, (old) => old?.filter((x) => x.connection_id !== id));
      toast.success(t("supportChannels.disconnected"));
    },
    onError: (e) => toast.error(e, t("supportChannels.disconnectError")),
  });
  const removeDestination = useMutation({
    mutationFn: api.supportChannels.removeDestination,
    onSuccess: (_, id) => {
      qc.setQueryData<SupportChannelDestination[]>(qk.supportChannelDestinations, (old) => old?.filter((x) => x.id !== id));
      toast.success(t("supportChannels.destinationRemoved"));
    },
    onError: (e) => toast.error(e, t("supportChannels.destinationRemoveError")),
  });
  const createLink = useMutation({
    mutationFn: api.supportChannels.linkCode,
    onSuccess: setLink,
    onError: (e) => toast.error(e, t("supportChannels.linkError")),
  });
  const testConnection = useMutation({
    mutationFn: api.supportChannels.test,
    onSuccess: (row) => {
      upsertInList(qc, qk.supportChannelConnections, row);
      if (row.status !== "error" && row.health_status === "healthy") toast.success(t("supportChannels.health.tested"));
      else toast.info(t("supportChannels.health.testedAttention"));
    },
    onError: (e) => toast.error(e, t("supportChannels.health.testError")),
  });

  return (
    <SettingsPage
      wide
      title={t("supportChannels.title")}
      description={t("supportChannels.description")}
      actions={
        canLink ? (
          <Button icon={<Link2 />} onClick={() => createLink.mutate()} loading={createLink.isPending}>
            {t("supportChannels.linkAccount")}
          </Button>
        ) : undefined
      }
    >
      <Section flush title={t("supportChannels.connections.title")} description={t("supportChannels.connections.description")}>
        {isLoading ? (
          <ListSkeleton rows={3} />
        ) : !connections?.length ? (
          <SettingsEmpty
            art="settings"
            title={t("supportChannels.empty.title")}
            description={t("supportChannels.empty.body")}
            tips={[
              { label: t("supportChannels.empty.tipThreads"), icon: <MessagesSquare /> },
              { label: t("supportChannels.empty.tipOllie"), icon: <Sparkles /> },
              { label: t("supportChannels.empty.tipCreate"), icon: <AtSign /> },
            ]}
            className="min-h-[300px]"
          />
        ) : (
          <div className="divide-y divide-line-soft">
            {connections.map((connection) => {
              const provider = providers?.find((p) => p.key === connection.provider);
              const routes = destinations?.filter((d) => d.connection_id === connection.id) ?? [];
              const state = connectionState(connection);
              const healthy = state.state === "active";
              const manual = provider ? !provider.configures : true;
              const account = [connection.external_account_handle, connection.external_account_name].filter(Boolean).join(" · ") || connection.external_account_id;
              return (
                <div key={connection.id} className="px-4 py-3.5">
                  <div className="flex items-start gap-3">
                    <AppMark app={connection.provider} name={provider?.name ?? connection.provider} />
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="text-[13px] font-medium">{connection.name}</span>
                        <span className="text-[12px] text-fg-3">{provider?.name ?? connection.provider}</span>
                        <StatePill state={state.state}>{t(state.key)}</StatePill>
                      </div>
                      <p className="mt-0.5 text-[12px] text-fg-3">{account}</p>
                      {connection.last_error ? (
                        <p className="mt-1 text-[12px] text-danger-fg">{connection.last_error}</p>
                      ) : (
                        !healthy && connection.health_detail && <p className="mt-1 text-[12px] text-fg-2">{connection.health_detail}</p>
                      )}
                      {manual && admin && (
                        <SetupSteps provider={connection.provider} webhookUrl={connection.webhook_url} done={healthy} />
                      )}
                      {!routes.length && admin && (
                        <p className="mt-2 text-[12px] text-fg-3">
                          {t("supportChannels.noDestinations")}{" "}
                          <Button variant="link" size="xs" className="text-[12px]" onClick={() => setRouting(connection.id)}>
                            {t("supportChannels.addFirstDestination")}
                          </Button>
                        </p>
                      )}
                      {!!routes.length && (
                        <div className="mt-2 flex flex-wrap gap-1.5">
                          {routes.map((route) => {
                            const team = teams?.find((x) => x.id === route.team_id);
                            const name = route.external_channel_name || route.external_channel_id;
                            return (
                              <span key={route.id} className="inline-flex h-6 items-center gap-1 rounded-md bg-sunken pl-2 pr-1 text-[11.5px] text-fg-2">
                                <Hash className="size-3 text-fg-4" aria-hidden />
                                {name}
                                <span className="text-fg-4">· {team?.name ?? t("supportChannels.allTeams")}</span>
                                {route.include_notes && (
                                  <span className="inline-flex items-center gap-0.5 text-fg-4">
                                    · <StickyNote className="size-3" aria-hidden />
                                    {t("supportChannels.withNotes")}
                                  </span>
                                )}
                                {admin && (
                                  <button
                                    type="button"
                                    className="ml-0.5 flex size-5 items-center justify-center rounded text-fg-4 hover:bg-hover hover:text-danger-fg"
                                    aria-label={t("supportChannels.removeDestinationLabel", { name })}
                                    onClick={() =>
                                      confirm.show(t("supportChannels.removeDestinationBody", { name }), () => removeDestination.mutateAsync(route.id), {
                                        title: t("supportChannels.removeDestinationTitle"),
                                        confirmLabel: t("supportChannels.remove"),
                                        danger: true,
                                      })
                                    }
                                  >
                                    <Trash2 className="size-3" />
                                  </button>
                                )}
                              </span>
                            );
                          })}
                        </div>
                      )}
                    </div>
                    {admin && (
                      <div className="flex max-w-[260px] shrink-0 flex-wrap justify-end gap-1.5">
                        <Button
                          icon={<RefreshCw />}
                          loading={testConnection.isPending && testConnection.variables === connection.id}
                          onClick={() => testConnection.mutate(connection.id)}
                        >
                          {t("supportChannels.health.test")}
                        </Button>
                        <Button square icon={<KeyRound />} aria-label={t("supportChannels.rotate.label", { name: connection.name })} onClick={() => setRotating(connection)} />
                        <Button onClick={() => setRouting(connection.id)}>{t("supportChannels.addDestination")}</Button>
                        <Button
                          variant="danger"
                          square
                          aria-label={t("supportChannels.disconnectLabel", { name: connection.name })}
                          icon={<Trash2 />}
                          onClick={() =>
                            confirm.show(t("supportChannels.disconnectBody", { name: connection.name }), () => disconnect.mutateAsync(connection.id), {
                              title: t("supportChannels.disconnectTitle"),
                              confirmLabel: t("supportChannels.disconnect"),
                              danger: true,
                            })
                          }
                        />
                      </div>
                    )}
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </Section>

      <Section title={t("supportChannels.providers.title")} description={admin ? t("supportChannels.providers.descriptionAdmin") : t("supportChannels.providers.description")}>
        <div className="grid gap-3 py-3 sm:grid-cols-3">
          {(providers ?? []).map((provider) => (
            <button
              key={provider.key}
              type="button"
              disabled={!admin}
              onClick={() => setConnecting(provider)}
              className="rounded-lg border border-line bg-surface p-3 text-left transition-colors hover:border-line-strong hover:bg-hover disabled:cursor-default disabled:opacity-60"
            >
              <div className="flex items-center gap-2.5">
                <AppMark app={provider.key} name={provider.name} size="sm" />
                <span className="flex-1 text-[13px] font-medium">{provider.name}</span>
                {admin && <Plus className="size-3.5 text-fg-3" aria-hidden />}
              </div>
              <p className="mt-1.5 text-[12px] leading-[18px] text-fg-3">{copy.description(provider)}</p>
            </button>
          ))}
        </div>
      </Section>

      {connecting && <ConnectDialog provider={connecting} onClose={() => setConnecting(null)} onConnected={(row) => setRouting(row.id)} />}
      {rotating && (
        <RotateDialog
          connection={rotating}
          provider={providers?.find((p) => p.key === rotating.provider)}
          onClose={() => setRotating(null)}
        />
      )}
      {routing && (
        <DestinationDialog
          connectionId={routing}
          provider={connections?.find((c) => c.id === routing)?.provider ?? ""}
          onClose={() => setRouting(null)}
        />
      )}
      <Dialog
        open={!!link}
        onOpenChange={(open) => !open && setLink(null)}
        title={t("supportChannels.link.title")}
        description={t("supportChannels.link.description", { code: link?.code ?? "" })}
        footer={
          <Button variant="primary" onClick={() => setLink(null)}>
            {t("supportChannels.link.done")}
          </Button>
        }
      >
        <CopyField value={link?.code ?? ""} />
      </Dialog>
    </SettingsPage>
  );
}

// Where a provider can't be set up by opendesk (Slack), the steps an admin takes
// there, with the address to paste. Folded away once the provider has called in.
function SetupSteps({ provider, webhookUrl, done }: { provider: string; webhookUrl: string; done: boolean }) {
  const { t } = useTranslation("settingsChannels");
  const [open, setOpen] = useState(!done);
  // Only Slack needs steps of its own; any other provider without a Configurer gets the generic one.
  const steps: unknown = provider === "slack" ? t("supportChannels.provider.slack.steps", { returnObjects: true }) : null;
  const list = Array.isArray(steps) ? (steps as string[]) : [t("supportChannels.setup.generic")];
  if (!open)
    return (
      <Button variant="link" size="xs" className="mt-1.5 text-[12px] text-fg-3" onClick={() => setOpen(true)}>
        {t("supportChannels.setup.show")}
      </Button>
    );
  return (
    <div className="mt-2.5 max-w-[620px] rounded-lg border border-line-soft bg-sunken px-3 py-2.5">
      <div className="flex items-center justify-between gap-2">
        <p className="text-[12px] font-medium">{t("supportChannels.setup.title")}</p>
        {done && (
          <Button variant="link" size="xs" className="text-[12px] text-fg-3" onClick={() => setOpen(false)}>
            {t("supportChannels.setup.hide")}
          </Button>
        )}
      </div>
      <div className="mt-2">
        <p className="mb-1 text-[11.5px] text-fg-4">{t("supportChannels.connections.webhook")}</p>
        <CopyField value={webhookUrl} />
      </div>
      <ol className="mt-2.5 flex list-decimal flex-col gap-1 pl-4 text-[12px] leading-[18px] text-fg-2 marker:text-fg-4">
        {list.map((step) => (
          <li key={step}>{step}</li>
        ))}
      </ol>
      {!done && <p className="mt-2 text-[11.5px] text-fg-3">{t("supportChannels.setup.waiting")}</p>}
    </div>
  );
}

function ConnectDialog({ provider, onClose, onConnected }: { provider: SupportChannelProvider; onClose: () => void; onConnected: (row: SupportChannelConnection) => void }) {
  const { t } = useTranslation("settingsChannels");
  const copy = useProviderCopy();
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [credentials, setCredentials] = useState<Record<string, string>>({});
  const [accounts, setAccounts] = useState<SupportChannelAccount[] | null>(null);
  const [error, setError] = useState("");
  const connect = useMutation({
    mutationFn: () => api.supportChannels.connect({ provider: provider.key, name: name.trim() || undefined, credentials }),
    onSuccess: (row) => {
      upsertInList(qc, qk.supportChannelConnections, row, { prepend: true });
      toast.success(t("supportChannels.connect.connected", { name: provider.name }));
      onClose();
      onConnected(row);
    },
    onError: (e) => setError(errorMessage(e, t("supportChannels.connect.error", { name: provider.name }))),
  });
  const fields = provider.fields.filter((field) => !field.generated && !field.discovered);
  const discoveredFields = provider.fields.filter((field) => field.discovered);
  const discover = useMutation({
    mutationFn: () => api.supportChannels.accounts(provider.key, credentials),
    onSuccess: (result) => {
      setAccounts(result.data);
      setError("");
      // A bot in one server has nothing to choose.
      if (result.data.length === 1) for (const field of discoveredFields) setCredentials((old) => ({ ...old, [field.name]: result.data[0].id }));
    },
    onError: (e) => setError(errorMessage(e, t("supportChannels.connect.discoveryError", { name: provider.name }))),
  });
  const baseValid = fields.every((field) => credentials[field.name]?.trim());
  const valid = baseValid && discoveredFields.every((field) => credentials[field.name]?.trim());
  const setCredential = (key: string, value: string) => {
    setCredentials((old) => {
      const next = { ...old, [key]: value };
      if (!provider.fields.find((field) => field.name === key)?.discovered) {
        for (const field of discoveredFields) delete next[field.name];
      }
      return next;
    });
    if (!provider.fields.find((field) => field.name === key)?.discovered) setAccounts(null);
  };
  return (
    <Dialog
      open
      onOpenChange={(open) => !open && onClose()}
      title={t("supportChannels.connect.title", { name: provider.name })}
      description={copy.setup(provider)}
      footer={
        <>
          <Button onClick={onClose}>{t("supportChannels.connect.cancel")}</Button>
          {discoveredFields.length > 0 && accounts === null ? (
            <Button variant="primary" disabled={!baseValid} loading={discover.isPending} onClick={() => discover.mutate()}>
              {t("supportChannels.connect.findAccounts")}
            </Button>
          ) : (
            <Button variant="primary" disabled={!valid} loading={connect.isPending} onClick={() => connect.mutate()}>
              {t("supportChannels.connect.submit")}
            </Button>
          )}
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <Field label={t("supportChannels.connect.name")} optional>
          {(props) => <TextInput {...props} value={name} maxLength={64} placeholder={provider.name} onChange={(e) => setName(e.target.value)} />}
        </Field>
        {fields.map((field) => (
          <Field key={field.name} label={copy.field(field)} hint={copy.help(provider, field)}>
            {(props) => (
              <TextInput
                {...props}
                type={field.secret ? "password" : "text"}
                autoComplete="off"
                value={credentials[field.name] ?? ""}
                onChange={(e) => setCredential(field.name, e.target.value)}
              />
            )}
          </Field>
        ))}
        {accounts !== null &&
          discoveredFields.map((field) => (
            <Field key={field.name} label={copy.field(field)} hint={copy.help(provider, field)}>
              {({ id }) => (
                <div className="flex gap-2">
                  <Select
                    id={id}
                    value={credentials[field.name] || undefined}
                    onChange={(value) => setCredential(field.name, value)}
                    options={accounts.map((account) => ({ value: account.id, label: account.name || account.id }))}
                    placeholder={t("supportChannels.connect.accountPlaceholder")}
                  />
                  <Button square icon={<RefreshCw />} aria-label={t("supportChannels.connect.refreshAccounts")} loading={discover.isPending} onClick={() => discover.mutate()} />
                </div>
              )}
            </Field>
          ))}
        {accounts !== null && accounts.length === 0 && <p className="text-[12px] text-fg-3">{t("supportChannels.connect.noAccounts")}</p>}
        {error && (
          <p role="alert" className="text-[12px] text-danger-fg">
            {error}
          </p>
        )}
      </div>
    </Dialog>
  );
}

function RotateDialog({ connection, provider, onClose }: { connection: SupportChannelConnection; provider?: SupportChannelProvider; onClose: () => void }) {
  const { t } = useTranslation("settingsChannels");
  const copy = useProviderCopy();
  const qc = useQueryClient();
  const [credentials, setCredentials] = useState<Record<string, string>>({});
  const [error, setError] = useState("");
  const fields = provider?.fields.filter((field) => !field.generated && !field.discovered) ?? [];
  const rotationCredentials = () => {
    const out = { ...credentials };
    for (const field of provider?.fields ?? []) if (field.discovered) out[field.name] = connection.external_account_id;
    return out;
  };
  const rotate = useMutation({
    mutationFn: () => api.supportChannels.rotate(connection.id, { credentials: rotationCredentials() }),
    onSuccess: (row) => {
      upsertInList(qc, qk.supportChannelConnections, row);
      toast.success(t("supportChannels.rotate.rotated"));
      onClose();
    },
    onError: (e) => setError(errorMessage(e, t("supportChannels.rotate.error"))),
  });
  const valid = !!provider && fields.every((field) => credentials[field.name]?.trim());
  return (
    <Dialog
      open
      onOpenChange={(open) => !open && onClose()}
      title={t("supportChannels.rotate.title", { name: connection.name })}
      description={t("supportChannels.rotate.description")}
      footer={
        <>
          <Button onClick={onClose}>{t("supportChannels.rotate.cancel")}</Button>
          <Button variant="primary" disabled={!valid} loading={rotate.isPending} onClick={() => rotate.mutate()}>
            {t("supportChannels.rotate.submit")}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        {fields.map((field) => (
          <Field key={field.name} label={copy.field(field)} hint={provider ? copy.help(provider, field) : undefined}>
            {(props) => (
              <TextInput
                {...props}
                type={field.secret ? "password" : "text"}
                autoComplete="off"
                value={credentials[field.name] ?? ""}
                onChange={(e) => setCredentials((old) => ({ ...old, [field.name]: e.target.value }))}
              />
            )}
          </Field>
        ))}
        {error && <p role="alert" className="text-[12px] text-danger-fg">{error}</p>}
      </div>
    </Dialog>
  );
}

function DestinationDialog({ connectionId, provider, onClose }: { connectionId: string; provider: string; onClose: () => void }) {
  const { t } = useTranslation("settingsChannels");
  const copy = useProviderCopy();
  const qc = useQueryClient();
  const notesId = useId();
  const { data: teams } = useQuery(q.teams());
  const { data: destinations } = useQuery(q.supportChannelDestinations());
  const available = useQuery(q.supportChannelAvailableDestinations(connectionId));
  const [team, setTeam] = useState("all");
  const [channelId, setChannelId] = useState("");
  const [includeNotes, setIncludeNotes] = useState(false);
  // When the bot can't list channels (missing scopes, an older bot), its id still works.
  const [manual, setManual] = useState(false);
  const [error, setError] = useState("");
  const options = useMemo(() => {
    const used = new Set((destinations ?? []).filter((d) => d.connection_id === connectionId).map((d) => d.team_id ?? "all"));
    return [
      ...(!used.has("all") ? [{ value: "all", label: t("supportChannels.destination.allTeams") }] : []),
      ...(teams ?? []).filter((x) => !x.private && !used.has(x.id)).map((x) => ({ value: x.id, label: x.name })),
    ];
  }, [connectionId, destinations, teams, t]);
  const selected = options.some((x) => x.value === team) ? team : options[0]?.value;
  const usedChannels = new Set((destinations ?? []).filter((item) => item.connection_id === connectionId).map((item) => item.external_channel_id));
  const channelOptions = (available.data ?? [])
    .filter((item) => !usedChannels.has(item.id))
    .map((item) => ({ value: item.id, label: item.name ? `#${item.name}` : item.id }));
  const selectedChannel = available.data?.find((item) => item.id === channelId && !usedChannels.has(item.id));
  const manualId = channelId.trim();
  const channelReady = manual ? !!manualId && !usedChannels.has(manualId) : !!selectedChannel;
  const create = useMutation({
    mutationFn: () =>
      api.supportChannels.addDestination({
        connection_id: connectionId,
        team_id: selected === "all" ? undefined : selected,
        external_channel_id: manual ? manualId : channelId.trim(),
        external_channel_name: selectedChannel?.name,
        include_notes: includeNotes,
      }),
    onSuccess: (row) => {
      upsertInList(qc, qk.supportChannelDestinations, row);
      toast.success(t("supportChannels.destination.added"));
      onClose();
    },
    onError: (e) => setError(errorMessage(e, t("supportChannels.destination.error"))),
  });
  return (
    <Dialog
      open
      onOpenChange={(open) => !open && onClose()}
      title={t("supportChannels.destination.title")}
      description={t("supportChannels.destination.description")}
      footer={
        <>
          <Button onClick={onClose}>{t("supportChannels.destination.cancel")}</Button>
          <Button variant="primary" disabled={!selected || !channelReady} loading={create.isPending} onClick={() => create.mutate()}>
            {t("supportChannels.destination.submit")}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <Field label={t("supportChannels.destination.team")}>
          {({ id }) => <Select id={id} value={selected} onChange={setTeam} options={options} placeholder={t("supportChannels.destination.teamPlaceholder")} />}
        </Field>
        {manual ? (
          <Field label={t("supportChannels.destination.channelId")} hint={t("supportChannels.destination.channelIdHint")}>
            {(props) => <TextInput {...props} value={channelId} autoComplete="off" spellCheck={false} onChange={(e) => setChannelId(e.target.value)} />}
          </Field>
        ) : (
          <Field label={t("supportChannels.destination.channel")} hint={t("supportChannels.destination.channelHint")}>
            {({ id }) => (
              <div className="flex gap-2">
                <Select
                  id={id}
                  value={channelOptions.some((x) => x.value === channelId) ? channelId : undefined}
                  onChange={setChannelId}
                  options={channelOptions}
                  placeholder={available.isLoading ? t("supportChannels.destination.loading") : t("supportChannels.destination.channelPlaceholder")}
                />
                <Button
                  square
                  icon={<RefreshCw />}
                  aria-label={t("supportChannels.destination.refresh")}
                  loading={available.isFetching}
                  onClick={() => available.refetch()}
                />
              </div>
            )}
          </Field>
        )}
        {!manual && !available.isLoading && !available.isError && !channelOptions.length && <p className="text-[12px] leading-[17px] text-fg-3">{copy.noChannels(provider)}</p>}
        {!manual && available.isError && <p className="text-[12px] text-danger-fg">{errorMessage(available.error, t("supportChannels.destination.loadError"))}</p>}
        <Button
          variant="link"
          size="xs"
          className="-mt-2 self-start text-[12px] text-fg-3"
          onClick={() => {
            setManual(!manual);
            setChannelId("");
          }}
        >
          {manual ? t("supportChannels.destination.pickFromList") : t("supportChannels.destination.enterId")}
        </Button>
        <div className="flex items-start justify-between gap-4">
          <label htmlFor={notesId} className="min-w-0">
            <span className="block text-[13px] font-medium">{t("supportChannels.destination.notes")}</span>
            <span className="mt-0.5 block text-[12px] leading-[17px] text-fg-3">{t("supportChannels.destination.notesHint")}</span>
          </label>
          <Switch id={notesId} checked={includeNotes} onCheckedChange={setIncludeNotes} />
        </div>
        {error && (
          <p role="alert" className="text-[12px] text-danger-fg">
            {error}
          </p>
        )}
      </div>
    </Dialog>
  );
}
