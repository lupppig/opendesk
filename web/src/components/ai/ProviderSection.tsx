// Settings > AI > Provider: run the workspace's AI on the opendesk AI balance (or the
// instance's provider when self-hosted), or on its own key (OpenRouter, an
// OpenAI-compatible endpoint, or Anthropic). A key is tested before it is saved,
// is never shown again past its last 4 characters, and its state ("Rejected by
// OpenRouter 2 minutes ago") comes back live through the ai_key_failed notification.
// Below it, where decisions run and the workspace's own TypeSafe key (DecisionsKey.tsx).

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, Eye, EyeOff, KeyRound, TriangleAlert } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Row, Section } from "@/components/layout";
import { IconTile } from "@/components/settings2/bits";
import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/ui/confirm";
import { TextInput } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Segmented } from "@/components/ui/tabs";
import { toast } from "@/components/ui/toast";
import { errorMessage } from "@/lib/api/client";
import { api } from "@/lib/api/endpoints";
import type { AIKeyKind, AIKeyStatus, AIProvider, AIProviderKey, AIProviderKeyInput, AIProviderTest } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { relLong } from "@/lib/format";
import { q, qk } from "@/lib/queries";
import { tone } from "@/lib/tones";
import { DecisionsBlock } from "./DecisionsKey";

const KINDS: AIKeyKind[] = ["openrouter", "openai", "anthropic"];
const PLACEHOLDER: Record<AIKeyKind, string> = { openrouter: "sk-or-v1-…", openai: "sk-…", anthropic: "sk-ant-…" };

export function ProviderSection({ canEdit }: { canEdit: boolean }) {
  const { t } = useTranslation("ai");
  const { data, isLoading } = useQuery({ ...q.aiProvider(), enabled: canEdit });
  const [editing, setEditing] = useState(false);
  const confirm = useConfirm();
  const qc = useQueryClient();

  const refresh = (p?: AIProvider) => {
    if (p) qc.setQueryData(qk.aiProvider, p);
    void qc.invalidateQueries({ queryKey: qk.aiProvider });
    void qc.invalidateQueries({ queryKey: qk.aiConfig });
    void qc.invalidateQueries({ queryKey: qk.aiModels });
  };
  const remove = useMutation({
    mutationFn: () => api.ai.provider.remove(),
    onSuccess: () => {
      qc.setQueryData<AIProvider>(qk.aiProvider, (old) => (old ? { ...old, mode: "instance", key: null } : old));
      refresh();
      setEditing(false);
      toast.success(t("provider.removed"));
    },
  });

  if (!canEdit) return null;
  const billing = data?.billing ?? true;
  const key = data?.key ?? null;
  const own = editing || !!key;
  const askRemove = () =>
    confirm.show(billing ? t("provider.remove_body") : t("provider.remove_body_self_hosted"), () => remove.mutateAsync(), {
      title: t("provider.remove_title"),
      confirmLabel: t("provider.remove"),
      danger: true,
    });

  return (
    <Section title={t("provider.title")} description={billing ? t("provider.description") : t("provider.description_self_hosted")}>
      {isLoading || !data ? (
        <div className="py-3.5">
          <Skeleton className="h-7 w-64 rounded-md" />
        </div>
      ) : (
        <>
          <Row label={t("provider.runs_on")}>
            <Segmented<"instance" | "own">
              value={own ? "own" : "instance"}
              onChange={(v) => {
                if (v === "own") setEditing(true);
                else if (key) askRemove();
                else setEditing(false);
              }}
              options={[
                { value: "instance", label: billing ? t("provider.balance") : t("provider.instance") },
                { value: "own", label: t("provider.own") },
              ]}
            />
          </Row>
          {!own && (
            <p className="py-3 text-[12px] leading-[18px] text-fg-3">
              {billing ? t("provider.instance_hint", { provider: data.instance_provider }) : t("provider.instance_hint_self_hosted", { provider: data.instance_provider })}
            </p>
          )}
          {key && !editing && <SavedKey k={key} onReplace={() => setEditing(true)} onRemove={askRemove} />}
          {editing && (
            <KeyForm
              onCancel={() => setEditing(false)}
              onSaved={(p) => {
                refresh(p);
                setEditing(false);
                toast.success(t("provider.saved"));
              }}
            />
          )}
          {own && <p className="py-3 text-[11.5px] leading-[17px] text-fg-4">{billing ? t("provider.stays") : t("provider.stays_self_hosted")}</p>}
          {data.decisions && <DecisionsBlock route={data.decisions} billing={billing} />}
        </>
      )}
    </Section>
  );
}

function useProviderName() {
  const { t } = useTranslation("ai");
  return (kind: AIKeyKind) => (kind === "openai" ? t("provider.the_endpoint") : t(`provider.kind_${kind}`));
}

export function SavedKey({ k, onReplace, onRemove }: { k: AIProviderKey; onReplace: () => void; onRemove: () => void }) {
  const { t } = useTranslation("ai");
  const providerName = useProviderName();
  const failed = k.status !== "ok";
  const name = providerName(k.kind);
  const summary =
    k.kind === "openai" && k.base_url_host
      ? t("provider.key_summary_endpoint", { host: k.base_url_host, last4: k.last4 })
      : t("provider.key_summary", { provider: t(`provider.kind_${k.kind}`), last4: k.last4 });
  const when = relLong(k.status_at ?? k.updated_at);
  return (
    <div className="flex flex-col gap-3 py-3.5">
      <div className="flex items-start gap-3.5">
        <IconTile tone={failed ? (k.status === "no_funds" ? "amber" : "red") : "gray"} className="size-9">
          <KeyRound />
        </IconTile>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-[12.5px] font-medium text-fg">{summary}</span>
            <StatusPill k={k} name={name} when={when} />
          </div>
          <div className="mt-0.5 text-[12px] leading-[18px] text-fg-3">
            {k.created_by ? t("provider.added_by", { name: k.created_by.name, when: relLong(k.created_at) }) : t("provider.added", { when: relLong(k.created_at) })}
            {k.model && (
              <>
                {" · "}
                <span className="mono text-[11.5px]">{t("provider.models_line", { model: k.model })}</span>
              </>
            )}
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-1.5">
          <Button size="sm" variant="secondary" onClick={onReplace}>
            {t("provider.replace")}
          </Button>
          <Button size="sm" variant="danger" onClick={onRemove}>
            {t("provider.remove")}
          </Button>
        </div>
      </div>
      {failed && (
        <div role="alert" className={cn("flex items-start gap-2.5 rounded-md px-3 py-2.5 text-[12.5px] leading-[18px]", k.status === "no_funds" ? tone.amber.chip : tone.red.chip)}>
          <TriangleAlert className="mt-px size-3.5 shrink-0" />
          <span className="min-w-0 flex-1">{k.status === "no_funds" ? t("provider.fix_no_funds", { provider: name }) : t("provider.fix_rejected")}</span>
          <Button size="xs" variant="secondary" onClick={onReplace}>
            {t("provider.replace_key")}
          </Button>
        </div>
      )}
    </div>
  );
}

export function StatusPill({ k, name, when }: { k: { status: AIKeyStatus }; name: string; when: string }) {
  const { t } = useTranslation("ai");
  const hue = k.status === "ok" ? "green" : k.status === "no_funds" ? "amber" : "red";
  const label = k.status === "ok" ? t("provider.status_ok") : k.status === "no_funds" ? t("provider.status_no_funds", { provider: name, when }) : t("provider.status_rejected", { provider: name, when });
  return (
    <span className={cn("inline-flex h-5 items-center gap-1.5 rounded-sm px-1.5 text-[11px] font-medium", tone[hue].chip)}>
      <span className={cn("size-1.5 rounded-full", tone[hue].solid)} aria-hidden />
      {label}
    </span>
  );
}

function KeyForm({ onCancel, onSaved }: { onCancel: () => void; onSaved: (p: AIProvider) => void }) {
  const { t } = useTranslation("ai");
  const [kind, setKind] = useState<AIKeyKind>("openrouter");
  const [apiKey, setApiKey] = useState("");
  const [baseUrl, setBaseUrl] = useState("");
  const [model, setModel] = useState("");
  const [fastModel, setFastModel] = useState("");
  const [show, setShow] = useState(false);
  const [result, setResult] = useState<AIProviderTest | null>(null);
  const [error, setError] = useState<string | null>(null);

  const body = (): AIProviderKeyInput => {
    const b: AIProviderKeyInput = { kind, api_key: apiKey.trim() };
    if (kind === "openai") {
      b.base_url = baseUrl.trim();
      b.model = model.trim();
      if (fastModel.trim()) b.fast_model = fastModel.trim();
    }
    return b;
  };
  const changed = <T,>(set: (v: T) => void) => (v: T) => {
    set(v);
    setResult(null);
    setError(null);
  };

  const test = useMutation({
    mutationFn: () => api.ai.provider.test(body()),
    onMutate: () => {
      setResult(null);
      setError(null);
    },
    onSuccess: setResult,
    onError: (e) => setError(errorMessage(e, t("provider.save_failed"))),
  });
  const save = useMutation({
    mutationFn: () => api.ai.provider.set(body()),
    onMutate: () => setError(null),
    onSuccess: onSaved,
    onError: (e) => setError(errorMessage(e, t("provider.save_failed"))),
  });

  const ready = apiKey.trim().length >= 8 && (kind !== "openai" || (baseUrl.trim() !== "" && model.trim() !== ""));
  const busy = test.isPending || save.isPending;
  const suggestions = kind === "openai" && result?.models.length ? result.models.filter((m) => m !== model && m.toLowerCase().includes(model.trim().toLowerCase())).slice(0, 6) : [];

  return (
    <form
      className="flex flex-col"
      onSubmit={(e) => {
        e.preventDefault();
        if (ready && !busy) save.mutate();
      }}
    >
      <Row label={t("provider.kind")} description={t(`provider.kind_${kind}_hint`)} stack>
        <Segmented<AIKeyKind> value={kind} onChange={changed(setKind)} options={KINDS.map((k) => ({ value: k, label: t(`provider.kind_${k}`) }))} />
      </Row>
      {kind === "openai" && (
        <Row label={t("provider.base_url")} description={t("provider.base_url_hint")} htmlFor="ai-key-base" stack>
          <TextInput
            id="ai-key-base"
            className="mono max-w-[460px]"
            inputMode="url"
            autoComplete="off"
            spellCheck={false}
            placeholder="https://api.openai.com/v1"
            value={baseUrl}
            onChange={(e) => changed(setBaseUrl)(e.target.value)}
          />
        </Row>
      )}
      <Row label={t("provider.api_key")} description={t("provider.api_key_hint")} htmlFor="ai-key-secret" stack>
        <TextInput
          id="ai-key-secret"
          className="mono max-w-[460px]"
          type={show ? "text" : "password"}
          autoComplete="off"
          spellCheck={false}
          autoCapitalize="off"
          placeholder={PLACEHOLDER[kind]}
          value={apiKey}
          onChange={(e) => changed(setApiKey)(e.target.value)}
          trailing={
            <Button type="button" size="xs" variant="ghost" square aria-label={show ? t("provider.hide_key") : t("provider.show_key")} onClick={() => setShow((v) => !v)}>
              {show ? <EyeOff /> : <Eye />}
            </Button>
          }
        />
      </Row>
      {kind === "openai" && (
        <>
          <Row label={t("provider.model")} description={t("provider.model_hint")} htmlFor="ai-key-model" stack>
            <div className="flex max-w-[460px] flex-col gap-2">
              <TextInput id="ai-key-model" className="mono" autoComplete="off" spellCheck={false} placeholder="gpt-5.6-sol" value={model} onChange={(e) => changed(setModel)(e.target.value)} />
              {suggestions.length > 0 && (
                <div className="flex flex-wrap items-center gap-1.5 text-[11.5px] text-fg-3">
                  <span>{t("provider.pick_model")}</span>
                  {suggestions.map((m) => (
                    <button
                      key={m}
                      type="button"
                      onClick={() => setModel(m)}
                      className="mono rounded-sm bg-hover px-1.5 py-0.5 text-[11px] text-fg-2 transition-colors hover:bg-active hover:text-fg"
                    >
                      {m}
                    </button>
                  ))}
                </div>
              )}
            </div>
          </Row>
          <Row label={t("provider.fast_model")} description={t("provider.fast_model_hint")} htmlFor="ai-key-fast" stack>
            <TextInput id="ai-key-fast" className="mono max-w-[460px]" autoComplete="off" spellCheck={false} value={fastModel} onChange={(e) => changed(setFastModel)(e.target.value)} />
          </Row>
        </>
      )}
      <div className="flex flex-col gap-3 py-3.5">
        {(result || error) && <TestResult result={result} error={error} />}
        <div className="flex flex-wrap items-center gap-2">
          <Button type="button" variant="secondary" disabled={!ready || busy} loading={test.isPending} onClick={() => test.mutate()}>
            {test.isPending ? t("provider.testing") : t("provider.test")}
          </Button>
          <Button type="submit" variant="primary" disabled={!ready || busy || result?.ok === false} loading={save.isPending}>
            {t("provider.save")}
          </Button>
          <Button type="button" variant="ghost" disabled={busy} onClick={onCancel}>
            {t("provider.cancel")}
          </Button>
        </div>
      </div>
    </form>
  );
}

export function TestResult({ result, error }: { result: AIProviderTest | null; error: string | null }) {
  const { t } = useTranslation("ai");
  const ok = !!result?.ok && !error;
  const text = error ?? (result?.ok ? (result.models.length ? t("provider.test_ok", { count: result.models.length }) : t("provider.test_ok_none")) : result?.message);
  return (
    <div role="status" aria-live="polite" className={cn("flex items-start gap-2 rounded-md px-3 py-2 text-[12.5px] leading-[18px] animate-rise-in", ok ? tone.green.chip : tone.red.chip)}>
      {ok ? <CheckCircle2 className="mt-px size-3.5 shrink-0" /> : <TriangleAlert className="mt-px size-3.5 shrink-0" />}
      <span className="min-w-0">{text}</span>
    </div>
  );
}
