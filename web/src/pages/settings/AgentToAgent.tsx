// Agent-to-agent (contracts/ai.md "Asking a teammate"): whether teammates' Ollie may ask the
// caller's about their own mail, who may ask, which accounts it draws on and how much it may
// share, the caller's own sharing rules, and the log of asks made to them.

import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ArrowRight, Globe, Users } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Row, Section, SettingsPage } from "@/components/layout";
import { AiIcon } from "@/components/ai/bits";
import { ASSISTANT_NAME } from "@/components/assistant/name";
import { ProviderMark } from "@/components/collab/bits";
import { AudienceLists, ChoiceCard } from "@/components/collab/pickers";
import { IconTile } from "@/components/settings2/bits";
import { Avatar } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty";
import { Textarea } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { Segmented } from "@/components/ui/tabs";
import { toast } from "@/components/ui/toast";
import { Tooltip } from "@/components/ui/tooltip";
import { api } from "@/lib/api/endpoints";
import type { PeerAsk, PeerMode, PeerResult, PeerSettings } from "@/lib/api/types";
import { PEER_MODES, PEER_RULES_MAX, peerInput, peerOutcome, withAccountMode, withAllAccounts, withDefaultMode } from "@/lib/assistantPeer";
import { cn } from "@/lib/cn";
import { fullDate, relShort } from "@/lib/format";
import { q, qk } from "@/lib/queries";
import { atLeast, useRole, useUser } from "@/lib/session";
import { AI_TONE, tone, type Tone } from "@/lib/tones";

const MUTATION_KEY = ["assistant-peer", "update"] as const;

/** PUT the whole settings, shown in the cache first; a failure puts the last answer back. */
function useSavePeer() {
  const qc = useQueryClient();
  const { t } = useTranslation("settings");
  return useMutation({
    mutationKey: MUTATION_KEY,
    mutationFn: (next: PeerSettings) => api.assistantPeer.put(peerInput(next)),
    onMutate: async (next) => {
      await qc.cancelQueries({ queryKey: qk.assistantPeer });
      const prev = qc.getQueryData<PeerSettings>(qk.assistantPeer);
      qc.setQueryData(qk.assistantPeer, next);
      return { prev };
    },
    onError: (e, _next, ctx) => {
      if (ctx?.prev) qc.setQueryData(qk.assistantPeer, ctx.prev);
      toast.error(e, t("peer.saveFailed"));
      void qc.invalidateQueries({ queryKey: qk.assistantPeer });
    },
    onSuccess: (res) => {
      // A later change still in flight wins; its own answer lands after it.
      if (qc.isMutating({ mutationKey: MUTATION_KEY }) <= 1) qc.setQueryData(qk.assistantPeer, res);
    },
  });
}

export default function AgentToAgent() {
  const { t } = useTranslation("settings");
  const { data: config } = useQuery(q.aiConfig());
  const name = config?.assistant_name || ASSISTANT_NAME;
  const { data, isLoading, error } = useQuery(q.assistantPeer());
  const save = useSavePeer();
  const change = (next: PeerSettings) => save.mutate(next);

  return (
    <SettingsPage title={t("peer.title")} description={t("peer.description", { name })}>
      {isLoading ? (
        <div className="flex flex-col gap-3">
          <Skeleton className="h-16 rounded-lg" />
          <Skeleton className="h-40 rounded-lg" />
          <Skeleton className="h-28 rounded-lg" />
        </div>
      ) : !data ? (
        <Section>
          <EmptyState compact title={t("peer.unavailable")} description={error ? undefined : t("peer.offHint")} />
        </Section>
      ) : !data.available ? (
        <Off name={name} />
      ) : (
        <>
          <MainSwitch settings={data} name={name} onChange={change} />
          <PeopleSection settings={data} name={name} onChange={change} />
          <AccountsSection settings={data} name={name} onChange={change} />
          <RulesSection settings={data} name={name} onChange={change} />
          <AsksSection name={name} />
        </>
      )}
    </SettingsPage>
  );
}

function Off({ name }: { name: string }) {
  const { t } = useTranslation("settings");
  const admin = atLeast(useRole(), "admin");
  return (
    <Section>
      <EmptyState
        compact
        art="ai"
        title={t("peer.offTitle")}
        description={admin ? t("peer.offAdmin", { name }) : t("peer.offHint")}
        action={
          admin ? (
            <Button asChild size="sm" iconRight={<ArrowRight className="size-3.5" />}>
              <Link to="/settings/ai">{t("peer.openAi")}</Link>
            </Button>
          ) : undefined
        }
      />
    </Section>
  );
}

function MainSwitch({ settings, name, onChange }: { settings: PeerSettings; name: string; onChange: (s: PeerSettings) => void }) {
  const { t } = useTranslation("settings");
  const on = settings.enabled;
  return (
    <Section>
      <div className="flex items-center gap-3.5 py-3.5">
        <IconTile tone={on ? AI_TONE : "gray"} className="size-9 transition-colors duration-200">
          <AiIcon className={cn(!on && "text-current")} />
        </IconTile>
        <div className="min-w-0 flex-1">
          <div className="text-[12.5px] font-medium text-fg">{t("peer.enabled", { name })}</div>
          <div className="mt-0.5 text-[12px] leading-[18px] text-fg-3">{t("peer.enabledHint", { name })}</div>
        </div>
        <Switch checked={on} onCheckedChange={(enabled) => onChange({ ...settings, enabled })} aria-label={t("peer.enabled", { name })} />
      </div>
    </Section>
  );
}

function PeopleSection({ settings, name, onChange }: { settings: PeerSettings; name: string; onChange: (s: PeerSettings) => void }) {
  const { t } = useTranslation("settings");
  const me = useUser();
  const { data: members } = useQuery(q.members());
  const { data: teams } = useQuery(q.teams());
  const chosen = settings.share_with === "chosen";
  const audience = { everyone: false, user_ids: settings.audience.users.map((u) => u.id), team_ids: settings.audience.teams.map((x) => x.id) };
  const setAudience = (userIds: string[], teamIds: string[]) => {
    const known = new Map(settings.audience.users.map((u) => [u.id, u]));
    const users = userIds.map((id) => {
      const m = members?.find((x) => x.user.id === id)?.user;
      return m ? { id, name: m.name, email: m.email, avatar_url: m.avatar_url } : (known.get(id) ?? { id, name: "", email: "" });
    });
    const teamNames = new Map(settings.audience.teams.map((x) => [x.id, x.name]));
    const list = teamIds.map((id) => ({ id, name: teams?.find((x) => x.id === id)?.name ?? teamNames.get(id) ?? "" }));
    onChange({ ...settings, audience: { users, teams: list } });
  };
  return (
    <section className="animate-rise-in">
      <div className="mb-3">
        <h2 className="text-[13px] font-semibold tracking-[-0.01em]">{t("peer.people")}</h2>
        <p className="mt-0.5 text-[12px] leading-[18px] text-fg-3">{t("peer.peopleHint", { name })}</p>
      </div>
      <div role="radiogroup" aria-label={t("peer.shareWith")} className="flex flex-col gap-2">
        <ChoiceCard
          checked={!chosen}
          onSelect={() => onChange({ ...settings, share_with: "everyone" })}
          icon={<Globe />}
          title={t("peer.everyone")}
          hint={t("peer.everyoneHint")}
        />
        <ChoiceCard
          checked={chosen}
          onSelect={() => onChange({ ...settings, share_with: "chosen" })}
          icon={<Users />}
          title={t("peer.chosen")}
          hint={t("peer.chosenHint")}
        >
          <AudienceLists value={audience} onChange={(a) => setAudience(a.user_ids, a.team_ids)} exclude={me?.id} />
        </ChoiceCard>
      </div>
    </section>
  );
}

function useModeOptions() {
  const { t } = useTranslation("settings");
  return PEER_MODES.map((m) => ({ value: m, label: t(`peer.mode.${m}`) }));
}

function AccountsSection({ settings, name, onChange }: { settings: PeerSettings; name: string; onChange: (s: PeerSettings) => void }) {
  const { t } = useTranslation("settings");
  const modes = useModeOptions();
  const scope = settings.all_accounts ? "all" : "chosen";
  return (
    <Section title={t("peer.accounts")} description={t("peer.accountsHint", { name })}>
      {settings.accounts.length === 0 ? (
        <EmptyState
          compact
          art="inbox"
          title={t("peer.noAccounts")}
          description={t("peer.noAccountsHint")}
          action={
            <Button asChild size="sm" iconRight={<ArrowRight className="size-3.5" />}>
              <Link to="/settings/mail-accounts" search={{}}>
                {t("peer.connect")}
              </Link>
            </Button>
          }
        />
      ) : (
        <>
          <Row label={t("peer.useFrom")} description={t("peer.modesHint")}>
            <Segmented<"all" | "chosen">
              value={scope}
              onChange={(v) => onChange(withAllAccounts(settings, v === "all"))}
              options={[
                { value: "all", label: t("peer.allAccounts") },
                { value: "chosen", label: t("peer.chosenAccounts") },
              ]}
            />
          </Row>
          {settings.accounts.map((a) => (
            <Row
              key={a.id}
              label={
                <span className="flex min-w-0 items-center gap-2">
                  <ProviderMark provider={a.provider} />
                  <span className="truncate">{a.name || a.email}</span>
                </span>
              }
              description={a.name && a.name !== a.email ? a.email : undefined}
            >
              <Select<PeerMode>
                size="sm"
                className="w-[168px]"
                aria-label={t("peer.modeFor", { account: a.email })}
                value={a.mode}
                options={modes}
                onChange={(m) => onChange(withAccountMode(settings, a.id, m))}
              />
            </Row>
          ))}
          {settings.all_accounts && (
            <Row label={t("peer.newAccounts")} description={t("peer.newAccountsHint")}>
              <Select<PeerMode>
                size="sm"
                className="w-[168px]"
                aria-label={t("peer.newAccounts")}
                value={settings.default_mode}
                options={modes}
                onChange={(m) => onChange(withDefaultMode(settings, m))}
              />
            </Row>
          )}
        </>
      )}
    </Section>
  );
}

function RulesSection({ settings, name, onChange }: { settings: PeerSettings; name: string; onChange: (s: PeerSettings) => void }) {
  const { t } = useTranslation("settings");
  const [text, setText] = useState(settings.rules);
  useEffect(() => setText(settings.rules), [settings.rules]);
  const tooLong = text.length > PEER_RULES_MAX;
  const commit = (v: string) => {
    if (v.trim() === settings.rules.trim() || v.length > PEER_RULES_MAX) return;
    onChange({ ...settings, rules: v.trim() });
  };
  const suggestion = t("peer.rulesSuggestion");
  return (
    <Section
      title={t("peer.rules")}
      description={t("peer.rulesHint", { name })}
      actions={
        !text.trim() ? (
          <Button size="xs" variant="ghost" onClick={() => commit(suggestion)}>
            {t("peer.useSuggestion")}
          </Button>
        ) : undefined
      }
    >
      <div className="flex flex-col gap-1 py-3.5">
        <Textarea
          value={text}
          rows={4}
          invalid={tooLong}
          aria-label={t("peer.rules")}
          placeholder={suggestion}
          onChange={(e) => setText(e.target.value)}
          onBlur={() => commit(text)}
        />
        <span className={cn("mono self-end text-[11px] tabular-nums", tooLong ? "text-danger-fg" : "text-fg-4")}>
          {text.length.toLocaleString()} / {PEER_RULES_MAX.toLocaleString()}
        </span>
      </div>
    </Section>
  );
}

const RESULT_TONE: Record<PeerResult, Tone> = { answered: "green", declined: "amber", not_shared: "gray", failed: "red" };

function AsksSection({ name }: { name: string }) {
  const { t } = useTranslation("settings");
  const asks = useInfiniteQuery(q.assistantPeerAsks());
  const list = asks.data?.pages.flatMap((p) => p.data) ?? [];
  return (
    <Section title={t("peer.asks")} description={t("peer.asksHint", { name })} flush>
      {asks.isLoading ? (
        <div className="flex flex-col gap-2 p-4">
          <Skeleton className="h-10 rounded-md" />
          <Skeleton className="h-10 rounded-md" />
        </div>
      ) : list.length === 0 ? (
        <EmptyState compact art="ai" title={t("peer.noAsks")} description={t("peer.noAsksHint", { name })} />
      ) : (
        <ul>
          {list.map((a) => (
            <AskRow key={a.id} ask={a} />
          ))}
          {asks.hasNextPage && (
            <li className="flex justify-center border-t border-line-soft py-2">
              <Button size="xs" variant="ghost" loading={asks.isFetchingNextPage} onClick={() => void asks.fetchNextPage()}>
                {t("peer.showMore")}
              </Button>
            </li>
          )}
        </ul>
      )}
    </Section>
  );
}

function AskRow({ ask }: { ask: PeerAsk }) {
  const { t } = useTranslation("settings");
  const who = ask.asker.name || ask.asker.email;
  const outcome = peerOutcome(ask.result, ask.sources);
  return (
    <li className="flex gap-3 border-b border-line-soft px-4 py-3 last:border-0" data-ask={ask.result}>
      <Avatar name={who} src={ask.asker.avatar_url} size="md" className="mt-0.5" />
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-center gap-2 text-[12.5px]">
          <span className="truncate font-medium text-fg">{who}</span>
          <span className={cn("inline-flex h-[18px] shrink-0 items-center rounded-sm px-1.5 text-[11px] font-medium", tone[RESULT_TONE[ask.result]].chip)}>
            {t(`peer.result.${outcome.key}`, { count: outcome.count })}
          </span>
          <Tooltip content={fullDate(ask.created_at)}>
            <span className="ml-auto shrink-0 text-[11.5px] tabular-nums text-fg-4">{relShort(ask.created_at)}</span>
          </Tooltip>
        </div>
        <p className="mt-1 text-[12.5px] leading-[18px] text-fg-2 wrap-anywhere">{ask.question}</p>
        {ask.answer && <p className="mt-1 line-clamp-3 text-[12px] leading-[18px] text-fg-3 wrap-anywhere">{ask.answer}</p>}
      </div>
    </li>
  );
}
