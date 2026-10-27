// Settings > AI clients: use opendesk from Claude, ChatGPT, Cursor, VS Code and
// other MCP clients (contracts/agent.md "opendesk MCP server"). The server URL,
// copyable setup per client, an inline API key for clients that send a header,
// the clients connected with OAuth (disconnect with a confirm), and the tools.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Cable, KeyRound, LogIn, ShieldCheck, Unplug } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Section, SettingsPage } from "@/components/layout";
import { Avatar } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/ui/confirm";
import { CopyField } from "@/components/ui/copy-button";
import { Dialog } from "@/components/ui/dialog";
import { EmptyState } from "@/components/ui/empty";
import { Field, TextInput } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Segmented, Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Tooltip } from "@/components/ui/tooltip";
import { IconTile, ListSkeleton, MonoChip } from "@/components/settings2/bits";
import { MotionList, MotionRow } from "@/components/settings2/motion";
import { SettingsEmpty } from "@/components/settings2/SettingsEmpty";
import { useListMutation } from "@/components/settings2/optimistic";
import { ReauthNotice, isReauthError } from "@/components/settings/ReauthNotice";
import { clientSetups, READ_SCOPES, WRITE_SCOPES, type ClientId, type ClientSetup } from "@/components/aiClients/snippets";
import { api } from "@/lib/api/endpoints";
import { errorMessage } from "@/lib/api/client";
import type { AIClientConnection, AIClientTool, ApiKey } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { fullDate, relLong } from "@/lib/format";
import { q, qk, upsertInList } from "@/lib/queries";
import { atLeast, useRole } from "@/lib/session";
import { tone } from "@/lib/tones";

export default function AiClients() {
  const { t } = useTranslation("aiSetup");
  const admin = atLeast(useRole(), "admin");
  const server = useQuery(q.aiClientServer());
  const url = server.data?.url ?? "";
  // A key made on this page fills the snippets until the page is left; never stored.
  const [key, setKey] = useState<string | null>(null);

  return (
    <SettingsPage wide title={t("clients.title")} description={t("clients.description")}>
      <Section title={t("clients.server")} description={t("clients.serverHint")}>
        <div className="py-3.5">{server.data ? <CopyField value={url} /> : <div className="h-8 animate-pulse rounded-md bg-hover" />}</div>
      </Section>

      {server.data && <ClientSetupSection url={url} admin={admin} apiKey={key} onKey={setKey} />}

      <Connections admin={admin} />

      {server.data && <ToolList tools={server.data.tools} />}
    </SettingsPage>
  );
}

function ClientSetupSection({ url, admin, apiKey, onKey }: { url: string; admin: boolean; apiKey: string | null; onKey: (k: string) => void }) {
  const { t } = useTranslation("aiSetup");
  const setups = useMemo(() => clientSetups(url, apiKey), [url, apiKey]);
  const [tab, setTab] = useState<ClientId>("claude");
  const [creating, setCreating] = useState(false);

  return (
    <Section title={t("clients.setupTitle")} description={t("clients.setupHint")} flush>
      <Tabs value={tab} onValueChange={(v) => setTab(v as ClientId)}>
        <TabsList className="overflow-x-auto">
          {setups.map((s) => (
            <TabsTrigger key={s.id} value={s.id}>
              {s.label}
            </TabsTrigger>
          ))}
        </TabsList>
        {setups.map((s) => (
          <TabsContent key={s.id} value={s.id} className="outline-none focus-visible:shadow-[inset_0_0_0_2px_var(--focus)]">
            <ClientPanel setup={s} admin={admin} apiKey={apiKey} onCreateKey={() => setCreating(true)} />
          </TabsContent>
        ))}
      </Tabs>
      {creating && (
        <CreateKeyDialog
          client={setups.find((s) => s.id === tab)?.label ?? ""}
          onClose={() => setCreating(false)}
          onCreated={(secret) => {
            setCreating(false);
            onKey(secret);
          }}
        />
      )}
    </Section>
  );
}

function ClientPanel({ setup, admin, apiKey, onCreateKey }: { setup: ClientSetup; admin: boolean; apiKey: string | null; onCreateKey: () => void }) {
  const { t } = useTranslation("aiSetup");
  const usesKey = setup.auth === "key" || !!setup.keySnippets || setup.id === "other";
  return (
    <div className="flex flex-col gap-3 px-4 py-4">
      <div className="flex items-start gap-2.5">
        <span className={cn("mt-px inline-flex h-[18px] shrink-0 items-center gap-1 rounded-sm px-1.5 text-[10.5px] font-medium", setup.auth === "oauth" ? tone.green.chip : tone.gray.chip)}>
          {setup.auth === "oauth" ? <LogIn className="size-2.5" /> : <KeyRound className="size-2.5" />}
          {setup.auth === "oauth" ? t("clients.oauthBadge") : t("clients.keyBadge")}
        </span>
        <p className="text-[12.5px] leading-[19px] text-fg-2">{t(`clients.steps.${setup.id}`)}</p>
      </div>
      {setup.snippets.map((s, i) => (
        <CopyField key={i} value={s.value} multiline={s.multiline} />
      ))}
      {setup.keySnippets && (
        <>
          <p className="text-[12px] text-fg-3">{t("clients.steps.claudeCodeKey")}</p>
          {setup.keySnippets.map((s, i) => (
            <CopyField key={i} value={s.value} multiline={s.multiline} />
          ))}
        </>
      )}
      {usesKey && (
        <div className="flex flex-wrap items-center gap-2 rounded-md bg-sunken px-3 py-2 text-[12px] text-fg-3">
          <KeyRound className="size-3.5 text-fg-4" />
          {apiKey ? (
            <span className="flex-1">{t("clients.keyShownOnce")}</span>
          ) : admin ? (
            <>
              <span className="flex-1">{t("clients.keyNeeded")}</span>
              <Button size="xs" onClick={onCreateKey}>
                {t("clients.keyCreate")}
              </Button>
            </>
          ) : (
            <span className="flex-1">{t("clients.keyAskAdmin")}</span>
          )}
        </div>
      )}
      {apiKey && usesKey && <CopyField value={apiKey} />}
    </div>
  );
}

type Expiry = "30" | "90" | "365";

export function CreateKeyDialog({ client, onClose, onCreated }: { client: string; onClose: () => void; onCreated: (secret: string) => void }) {
  const { t } = useTranslation("aiSetup");
  const qc = useQueryClient();
  const [name, setName] = useState(client ? `${client}` : "");
  const [access, setAccess] = useState<"read" | "write">("read");
  const [expiry, setExpiry] = useState<Expiry>("90");
  const [error, setError] = useState<string | null>(null);
  const create = useMutation({
    mutationFn: () =>
      api.apiKeys.create({
        name: name.trim(),
        scopes: access === "read" ? READ_SCOPES : WRITE_SCOPES,
        expires_at: new Date(Date.now() + Number(expiry) * 86400_000).toISOString(),
      }),
    onSuccess: (k) => {
      const { secret, ...row } = k;
      upsertInList(qc, qk.apiKeys, row as ApiKey, { prepend: true });
      onCreated(secret);
    },
    // A key needs a recent sign-in (with 2FA when on): the notice below says why and signs in again.
    onError: (e) => {
      if (!isReauthError(e)) setError(errorMessage(e) || t("clients.keyDialog.error"));
    },
  });
  const submit = () => {
    setError(null);
    if (!name.trim()) return setError(t("clients.keyDialog.nameRequired"));
    create.mutate();
  };
  return (
    <Dialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={t("clients.keyDialog.title")}
      description={t("clients.keyDialog.description")}
      footer={
        <>
          <span className="mr-auto text-xs text-danger-fg" role="alert">
            {error}
          </span>
          <Button variant="ghost" onClick={onClose}>
            {t("clients.keyDialog.cancel")}
          </Button>
          <Button variant="primary" loading={create.isPending} onClick={submit}>
            {t("clients.keyDialog.create")}
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
        <Field label={t("clients.keyDialog.name")}>
          {(p) => (
            <TextInput {...p} autoFocus value={name} maxLength={80} placeholder={t("clients.keyDialog.namePlaceholder")} onChange={(e) => setName(e.target.value)} />
          )}
        </Field>
        <div className="flex flex-col gap-2">
          <div className="flex items-center justify-between gap-3">
            <span className="text-[12.5px] font-medium">{t("clients.keyDialog.access")}</span>
            <Segmented
              size="xs"
              value={access}
              onChange={(v) => setAccess(v as "read" | "write")}
              options={[
                { value: "read", label: t("clients.keyDialog.read") },
                { value: "write", label: t("clients.keyDialog.write") },
              ]}
            />
          </div>
          <p className="text-[12px] text-fg-3">{access === "read" ? t("clients.keyDialog.readHint") : t("clients.keyDialog.writeHint")}</p>
          <div className="flex flex-wrap gap-1">
            {(access === "read" ? READ_SCOPES : WRITE_SCOPES).map((s) => (
              <MonoChip key={s} className={s.endsWith(":write") ? "border-tone-amber-line text-tone-amber" : undefined}>
                {s}
              </MonoChip>
            ))}
          </div>
        </div>
        <Field label={t("clients.keyDialog.expires")}>
          {(p) => (
            <Select<Expiry>
              id={p.id}
              value={expiry}
              onChange={setExpiry}
              options={[
                { value: "30", label: t("clients.keyDialog.in30") },
                { value: "90", label: t("clients.keyDialog.in90") },
                { value: "365", label: t("clients.keyDialog.in365") },
              ]}
            />
          )}
        </Field>
        {isReauthError(create.error) && <ReauthNotice error={create.error} />}
        <button type="submit" className="hidden" />
      </form>
    </Dialog>
  );
}

function Connections({ admin }: { admin: boolean }) {
  const { t } = useTranslation("aiSetup");
  const conns = useQuery(q.aiClientConnections());
  const confirm = useConfirm();
  const revoke = useListMutation<AIClientConnection, string>(
    qk.aiClientConnections,
    (id) => api.aiClients.revoke(id),
    (rows, id) => rows.filter((c) => c.id !== id),
    { errorText: t("clients.revokeError") },
  );
  const rows = conns.data ?? [];

  return (
    <Section
      flush
      title={t("clients.connections")}
      description={admin ? t("clients.connectionsHintAdmin") : t("clients.connectionsHint")}
    >
      {conns.isLoading ? (
        <ListSkeleton rows={2} />
      ) : conns.isError ? (
        <EmptyState
          compact
          lit={3}
          title={t("clients.error")}
          action={<Button onClick={() => void conns.refetch()}>{t("clients.retry")}</Button>}
        />
      ) : !rows.length ? (
        <SettingsEmpty
          art="ai"
          ghost="people"
          title={t("clients.emptyTitle")}
          description={t("clients.emptyBody")}
          tips={[
            { label: t("clients.emptyTipSignIn"), icon: <LogIn /> },
            { label: t("clients.emptyTipRole"), icon: <ShieldCheck /> },
          ]}
          className="min-h-[300px]"
        />
      ) : (
        <MotionList>
          {rows.map((c) => (
            <MotionRow key={c.id}>
              <div className="group flex flex-col gap-2 px-4 py-3 transition-colors hover:bg-sunken md:flex-row md:items-center md:gap-4">
                <div className="flex min-w-0 flex-1 items-center gap-3">
                  <IconTile tone="violet">
                    <Cable />
                  </IconTile>
                  <div className="min-w-0 flex-1">
                    <div className="flex min-w-0 items-center gap-2">
                      <span className="truncate text-[13px] font-medium">{c.client.name}</span>
                      <span className="truncate text-[11.5px] text-fg-4">{c.client.local ? t("clients.onComputer") : c.client.redirect_host}</span>
                    </div>
                    <div className="mt-1 flex flex-wrap items-center gap-1">
                      {c.scopes.map((s) => (
                        <MonoChip key={s} className={s.endsWith(":write") ? "border-tone-amber-line text-tone-amber" : undefined}>
                          {s}
                        </MonoChip>
                      ))}
                    </div>
                  </div>
                </div>
                {admin && (
                  <span className="flex min-w-0 items-center gap-1.5 text-[12px] text-fg-3 max-md:pl-11 md:w-[180px]">
                    <Avatar name={c.user.name} src={c.user.avatar_url} size="xs" />
                    <span className="truncate text-fg-2">{c.mine ? t("clients.you") : c.user.name}</span>
                  </span>
                )}
                <Tooltip content={c.last_used_at ? fullDate(c.last_used_at) : null}>
                  <span className="text-[12px] text-fg-3 max-md:pl-11 md:w-[150px]">
                    {c.last_used_at ? t("clients.lastUsed", { when: relLong(c.last_used_at) }) : null}
                  </span>
                </Tooltip>
                <Button
                  size="xs"
                  variant="ghost"
                  icon={<Unplug className="size-3" />}
                  className="shrink-0 self-start max-md:ml-11 md:self-center"
                  onClick={() =>
                    confirm.show(t("clients.revokeBody"), () => revoke.mutateAsync(c.id), {
                      title: t("clients.revokeTitle", { name: c.client.name }),
                      confirmLabel: t("clients.revoke"),
                      danger: true,
                    })
                  }
                >
                  {t("clients.revoke")}
                </Button>
              </div>
            </MotionRow>
          ))}
        </MotionList>
      )}
    </Section>
  );
}

function ToolList({ tools }: { tools: AIClientTool[] }) {
  const { t } = useTranslation("aiSetup");
  return (
    <Section flush title={t("clients.tools")} description={t("clients.toolsHint")}>
      <ul>
        {tools.map((tl) => (
          <li key={tl.name} className="flex flex-col gap-1.5 border-b border-line-soft px-4 py-2.5 last:border-0 md:flex-row md:items-start md:gap-4">
            <div className="min-w-0 md:w-[200px] md:shrink-0">
              <span className="mono text-[12px] text-fg">{tl.name}</span>
              <span
                className={cn(
                  "ml-2 inline-flex h-[18px] items-center rounded-sm px-1.5 align-middle text-[10.5px]",
                  tl.read_only ? tone.gray.chip : tone.amber.chip,
                )}
              >
                {tl.read_only ? t("clients.readOnly") : t("clients.changes")}
              </span>
            </div>
            <p className="min-w-0 flex-1 text-[12px] leading-[18px] text-fg-3">{firstSentence(tl.description)}</p>
            <div className="flex shrink-0 flex-wrap gap-1">
              {tl.scopes.map((s) => (
                <MonoChip key={s}>{s}</MonoChip>
              ))}
            </div>
          </li>
        ))}
      </ul>
    </Section>
  );
}

/** Tool descriptions are written for models; the first sentence is enough here. */
function firstSentence(s: string) {
  const m = /^.*?[.!?](?=\s|$)/.exec(s.trim());
  return m ? m[0] : s;
}
