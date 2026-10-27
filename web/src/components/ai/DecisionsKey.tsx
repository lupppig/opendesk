// Settings > AI > Provider > Decisions: where triage, automation questions and mail
// categories run (Jev on the workspace's own TypeSafe key, Jev through its OpenRouter
// key, or the instance's engine), and the TypeSafe key itself: tested before it is
// saved, never shown again past its last 4 characters.

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Eye, EyeOff, KeyRound, TriangleAlert } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Row } from "@/components/layout";
import { IconTile } from "@/components/settings2/bits";
import { Button } from "@/components/ui/button";
import { useConfirm } from "@/components/ui/confirm";
import { TextInput } from "@/components/ui/input";
import { toast } from "@/components/ui/toast";
import { errorMessage } from "@/lib/api/client";
import { api } from "@/lib/api/endpoints";
import type { AIDecisionsKey, AIDecisionsRoute, AIProvider, AIProviderTest } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { relLong } from "@/lib/format";
import { qk } from "@/lib/queries";
import { tone } from "@/lib/tones";
import { StatusPill, TestResult } from "./ProviderSection";

export function DecisionsBlock({ route, billing }: { route: AIDecisionsRoute; billing: boolean }) {
  const { t } = useTranslation("ai");
  const qc = useQueryClient();
  const confirm = useConfirm();
  const [editing, setEditing] = useState(false);

  const refresh = (p?: AIProvider) => {
    if (p) qc.setQueryData(qk.aiProvider, p);
    void qc.invalidateQueries({ queryKey: qk.aiProvider });
    void qc.invalidateQueries({ queryKey: qk.aiConfig });
  };
  const remove = useMutation({
    mutationFn: () => api.ai.provider.decisions.remove(),
    onSuccess: () => {
      refresh();
      toast.success(t("provider.decisions_removed"));
    },
  });
  const askRemove = () =>
    confirm.show(t("provider.decisions_remove_body"), () => remove.mutateAsync(), {
      title: t("provider.decisions_remove_title"),
      confirmLabel: t("provider.remove"),
      danger: true,
    });

  const where =
    route.runs_on === "typesafe_key"
      ? t("provider.decisions_typesafe_key")
      : route.runs_on === "provider_key"
        ? route.engine === "jev"
          ? t("provider.decisions_provider_jev")
          : t("provider.decisions_provider_llm")
        : route.runs_on === "instance"
          ? billing
            ? t("provider.decisions_instance")
            : t("provider.decisions_instance_self_hosted")
          : t("provider.decisions_none");

  return (
    <div className="flex flex-col">
      <Row label={t("provider.decisions_title")} description={where} stack={!!route.key || editing}>
        {!route.key && !editing && (
          <Button size="sm" variant="secondary" onClick={() => setEditing(true)}>
            <KeyRound />
            {t("provider.decisions_add")}
          </Button>
        )}
      </Row>
      {route.key && !editing && <SavedDecisionsKey k={route.key} onReplace={() => setEditing(true)} onRemove={askRemove} />}
      {editing && (
        <DecisionsKeyForm
          onCancel={() => setEditing(false)}
          onSaved={(p) => {
            refresh(p);
            setEditing(false);
            toast.success(t("provider.decisions_saved"));
          }}
        />
      )}
    </div>
  );
}

function SavedDecisionsKey({ k, onReplace, onRemove }: { k: AIDecisionsKey; onReplace: () => void; onRemove: () => void }) {
  const { t } = useTranslation("ai");
  const failed = k.status !== "ok";
  const name = t("provider.kind_typesafe");
  return (
    <div className="flex flex-col gap-3 pb-3.5">
      <div className="flex items-start gap-3.5">
        <IconTile tone={failed ? (k.status === "no_funds" ? "amber" : "red") : "gray"} className="size-9">
          <KeyRound />
        </IconTile>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-[12.5px] font-medium text-fg">{t("provider.key_summary", { provider: name, last4: k.last4 })}</span>
            <StatusPill k={k} name={name} when={relLong(k.status_at ?? k.updated_at)} />
          </div>
          <div className="mt-0.5 text-[12px] leading-[18px] text-fg-3">
            {k.created_by ? t("provider.added_by", { name: k.created_by.name, when: relLong(k.created_at) }) : t("provider.added", { when: relLong(k.created_at) })}
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
          <span className="min-w-0 flex-1">{k.status === "no_funds" ? t("provider.decisions_fix_no_funds") : t("provider.decisions_fix_rejected")}</span>
          <Button size="xs" variant="secondary" onClick={onReplace}>
            {t("provider.replace_key")}
          </Button>
        </div>
      )}
    </div>
  );
}

function DecisionsKeyForm({ onCancel, onSaved }: { onCancel: () => void; onSaved: (p: AIProvider) => void }) {
  const { t } = useTranslation("ai");
  const [secret, setSecret] = useState("");
  const [show, setShow] = useState(false);
  const [result, setResult] = useState<AIProviderTest | null>(null);
  const [error, setError] = useState<string | null>(null);
  const input = () => ({ api_key: secret.trim() });

  const test = useMutation({
    mutationFn: () => api.ai.provider.decisions.test(input()),
    onMutate: () => {
      setResult(null);
      setError(null);
    },
    onSuccess: setResult,
    onError: (e) => setError(errorMessage(e, t("provider.save_failed"))),
  });
  const save = useMutation({
    mutationFn: () => api.ai.provider.decisions.set(input()),
    onMutate: () => setError(null),
    onSuccess: onSaved,
    onError: (e) => setError(errorMessage(e, t("provider.save_failed"))),
  });
  const ready = secret.trim().length >= 8;
  const busy = test.isPending || save.isPending;

  return (
    <form
      className="flex flex-col"
      onSubmit={(e) => {
        e.preventDefault();
        if (ready && !busy) save.mutate();
      }}
    >
      <Row label={t("provider.decisions_key")} description={t("provider.decisions_key_hint")} htmlFor="ai-decisions-secret" stack>
        <TextInput
          id="ai-decisions-secret"
          className="mono max-w-[460px]"
          type={show ? "text" : "password"}
          autoComplete="off"
          spellCheck={false}
          autoCapitalize="off"
          autoFocus
          value={secret}
          onChange={(e) => {
            setSecret(e.target.value);
            setResult(null);
            setError(null);
          }}
          trailing={
            <Button type="button" size="xs" variant="ghost" square aria-label={show ? t("provider.hide_key") : t("provider.show_key")} onClick={() => setShow((v) => !v)}>
              {show ? <EyeOff /> : <Eye />}
            </Button>
          }
        />
      </Row>
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
