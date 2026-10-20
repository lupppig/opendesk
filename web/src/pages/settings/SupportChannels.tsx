import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Hash, Link2, MessagesSquare, Plus, Sparkles, StickyNote, Trash2 } from "lucide-react";
import { useId, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { AppMark } from "@/components/appmark";
import { Section, SettingsPage } from "@/components/layout";
import { ListSkeleton, StatePill } from "@/components/settings2/bits";
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
import type { SupportChannelConnection, SupportChannelDestination, SupportChannelProvider } from "@/lib/api/types";
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
  };
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
            ]}
            className="min-h-[300px]"
          />
        ) : (
          <div className="divide-y divide-line-soft">
            {connections.map((connection) => {
              const provider = providers?.find((p) => p.key === connection.provider);
              const routes = destinations?.filter((d) => d.connection_id === connection.id) ?? [];
              const failing = connection.status === "error";
              return (
                <div key={connection.id} className="px-4 py-3.5">
                  <div className="flex items-start gap-3">
                    <AppMark app={connection.provider} name={provider?.name ?? connection.provider} />
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="text-[13px] font-medium">{connection.name}</span>
                        <span className="text-[12px] text-fg-3">{provider?.name ?? connection.provider}</span>
                        <StatePill state={failing ? "failing" : "active"}>{t(failing ? "supportChannels.status.error" : "supportChannels.status.connected")}</StatePill>
                      </div>
                      <p className="mt-0.5 text-[12px] text-fg-3">{connection.external_account_name || connection.external_account_id}</p>
                      {connection.last_error && <p className="mt-1 text-[12px] text-danger-fg">{connection.last_error}</p>}
                      <div className="mt-2 max-w-[620px]">
                        <p className="mb-1 text-[11.5px] text-fg-4">{t("supportChannels.connections.webhook")}</p>
                        <CopyField value={connection.webhook_url} />
                      </div>
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
                      <div className="flex shrink-0 gap-1.5">
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

      {connecting && <ConnectDialog provider={connecting} onClose={() => setConnecting(null)} />}
      {routing && <DestinationDialog connectionId={routing} onClose={() => setRouting(null)} />}
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

function ConnectDialog({ provider, onClose }: { provider: SupportChannelProvider; onClose: () => void }) {
  const { t } = useTranslation("settingsChannels");
  const copy = useProviderCopy();
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [credentials, setCredentials] = useState<Record<string, string>>({});
  const [error, setError] = useState("");
  const connect = useMutation({
    mutationFn: () => api.supportChannels.connect({ provider: provider.key, name: name.trim() || undefined, credentials }),
    onSuccess: (row) => {
      upsertInList(qc, qk.supportChannelConnections, row, { prepend: true });
      toast.success(t("supportChannels.connect.connected", { name: provider.name }));
      onClose();
    },
    onError: (e) => setError(errorMessage(e, t("supportChannels.connect.error", { name: provider.name }))),
  });
  const valid = provider.fields.every((field) => credentials[field.name]?.trim());
  return (
    <Dialog
      open
      onOpenChange={(open) => !open && onClose()}
      title={t("supportChannels.connect.title", { name: provider.name })}
      description={copy.setup(provider)}
      footer={
        <>
          <Button onClick={onClose}>{t("supportChannels.connect.cancel")}</Button>
          <Button variant="primary" disabled={!valid} loading={connect.isPending} onClick={() => connect.mutate()}>
            {t("supportChannels.connect.submit")}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <Field label={t("supportChannels.connect.name")} optional>
          {(props) => <TextInput {...props} value={name} maxLength={80} placeholder={provider.name} onChange={(e) => setName(e.target.value)} />}
        </Field>
        {provider.fields.map((field) => (
          <Field key={field.name} label={copy.field(field)} hint={copy.help(provider, field)}>
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
        {error && (
          <p role="alert" className="text-[12px] text-danger-fg">
            {error}
          </p>
        )}
      </div>
    </Dialog>
  );
}

function DestinationDialog({ connectionId, onClose }: { connectionId: string; onClose: () => void }) {
  const { t } = useTranslation("settingsChannels");
  const qc = useQueryClient();
  const notesId = useId();
  const { data: teams } = useQuery(q.teams());
  const { data: destinations } = useQuery(q.supportChannelDestinations());
  const [team, setTeam] = useState("all");
  const [channelId, setChannelId] = useState("");
  const [channelName, setChannelName] = useState("");
  const [includeNotes, setIncludeNotes] = useState(false);
  const [error, setError] = useState("");
  const options = useMemo(() => {
    const used = new Set((destinations ?? []).filter((d) => d.connection_id === connectionId).map((d) => d.team_id ?? "all"));
    return [
      ...(!used.has("all") ? [{ value: "all", label: t("supportChannels.destination.allTeams") }] : []),
      ...(teams ?? []).filter((x) => !x.private && !used.has(x.id)).map((x) => ({ value: x.id, label: x.name })),
    ];
  }, [connectionId, destinations, teams, t]);
  const selected = options.some((x) => x.value === team) ? team : options[0]?.value;
  const create = useMutation({
    mutationFn: () =>
      api.supportChannels.addDestination({
        connection_id: connectionId,
        team_id: selected === "all" ? undefined : selected,
        external_channel_id: channelId.trim(),
        external_channel_name: channelName.trim() || undefined,
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
          <Button variant="primary" disabled={!selected || !channelId.trim()} loading={create.isPending} onClick={() => create.mutate()}>
            {t("supportChannels.destination.submit")}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <Field label={t("supportChannels.destination.team")}>
          {({ id }) => <Select id={id} value={selected} onChange={setTeam} options={options} placeholder={t("supportChannels.destination.teamPlaceholder")} />}
        </Field>
        <Field label={t("supportChannels.destination.channel")} hint={t("supportChannels.destination.channelHint")}>
          {(props) => <TextInput {...props} value={channelId} maxLength={200} onChange={(e) => setChannelId(e.target.value)} />}
        </Field>
        <Field label={t("supportChannels.destination.displayName")} optional>
          {(props) => (
            <TextInput
              {...props}
              value={channelName}
              maxLength={100}
              placeholder={t("supportChannels.destination.displayNamePlaceholder")}
              onChange={(e) => setChannelName(e.target.value)}
            />
          )}
        </Field>
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
