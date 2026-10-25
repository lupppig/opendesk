import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ArrowUpRight } from "lucide-react";
import { useEffect, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { Row, Section, SettingsPage } from "@/components/layout";
import { AiIcon, useCostText } from "@/components/ai/bits";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { Segmented } from "@/components/ui/tabs";
import { Textarea, TextInput } from "@/components/ui/input";
import { toast } from "@/components/ui/toast";
import { SaveBar } from "@/components/settings/SaveBar";
import { UnsavedGuard } from "@/components/settings2/UnsavedGuard";
import { api } from "@/lib/api/endpoints";
import { errorMessage } from "@/lib/api/client";
import type { AIConfig, AIFeature, AISettings } from "@/lib/api/types";
import { q, qk } from "@/lib/queries";
import { atLeast, useRole } from "@/lib/session";
import { cn } from "@/lib/cn";
import { AI_TONE, tone } from "@/lib/tones";
import { IconTile } from "@/components/settings2/bits";
import { ASSISTANT_NAME } from "@/components/assistant/name";
import { ModelPicker } from "@/components/ai/ModelPicker";
import { ProviderSection } from "@/components/ai/ProviderSection";
import { AutoRunSettings } from "@/components/ai/AutoRunSettings";

const PRESET_TONES = ["friendly", "professional", "concise"] as const;
type ToneChoice = (typeof PRESET_TONES)[number] | "custom";

const FEATURE_ORDER: AIFeature[] = ["summarize", "rewrite", "translate", "triage", "copilot", "draft_reply", "answer", "kb_draft", "assistant", "assistant_peer"];

const DEFAULTS: AISettings = {
  enabled: true,
  auto_triage: false,
  auto_summarize: false,
  widget_answers: false,
  assistant: true,
  assistant_peer: false,
  tone: "friendly",
  reply_language: "customer",
  instructions: "",
};

const TONE_MAX = 200;
const INSTRUCTIONS_MAX = 2000;

function toneChoice(tone: string): ToneChoice {
  return (PRESET_TONES as readonly string[]).includes(tone) ? (tone as ToneChoice) : "custom";
}

/** The settings the page saves with its save bar; changes Ollie makes without asking save on their own. */
function drafted(s: AISettings): Omit<AISettings, "assistant_auto"> {
  const { assistant_auto: _auto, ...rest } = s;
  return rest;
}

export default function AiSettings() {
  const { t } = useTranslation("ai");
  const { data: config, isLoading } = useQuery(q.aiConfig());
  const { data: models } = useQuery({ ...q.aiModels(), enabled: !!config?.enabled });
  const assistantName = config?.assistant_name || ASSISTANT_NAME;
  const role = useRole();
  const qc = useQueryClient();
  const available = !!config?.enabled;
  const canEdit = atLeast(role, "admin") && available;

  const base = config?.features ?? DEFAULTS;
  const [draft, setDraft] = useState<AISettings>(base);
  const [custom, setCustom] = useState(toneChoice(base.tone) === "custom");
  // Reset the draft only when what it holds changed, so a switch that saves on its own
  // (AutoRunSettings) keeps the unsaved edits.
  const baseKey = JSON.stringify(drafted(base));
  useEffect(() => {
    if (config?.features) {
      setDraft(config.features);
      setCustom(toneChoice(config.features.tone) === "custom");
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- baseKey stands for config.features
  }, [baseKey]);

  const dirty = JSON.stringify(drafted(draft)) !== baseKey;
  const toneTooLong = draft.tone.length > TONE_MAX;
  const instructionsTooLong = draft.instructions.length > INSTRUCTIONS_MAX;
  const invalid = toneTooLong || instructionsTooLong || (custom && !draft.tone.trim());

  const save = useMutation({
    mutationFn: (d: AISettings) => {
      const patch: Partial<AISettings> = {};
      for (const k of Object.keys(drafted(d)) as (keyof AISettings)[]) if (d[k] !== base[k]) (patch as Record<string, unknown>)[k] = d[k];
      return api.ai.updateSettings(patch);
    },
    onSuccess: (features) => {
      // What this save did not send keeps its latest value in the cache.
      qc.setQueryData<AIConfig | null>(qk.aiConfig, (old) => (old ? { ...old, features: { ...features, assistant_auto: old.features?.assistant_auto ?? features.assistant_auto } } : old));
      toast.success(t("settings.saved"));
    },
    onError: (e) => toast.error(errorMessage(e, t("settings.save_failed"))),
  });

  const set = (patch: Partial<AISettings>) => setDraft((d) => ({ ...d, ...patch }));
  const off = !canEdit || !draft.enabled;

  return (
    <SettingsPage title={t("settings.title")} description={t("settings.description")}>
      <UnsavedGuard when={dirty && !save.isPending} />
      <div className="flex flex-col gap-8">
        {isLoading ? (
          <div className="flex flex-col gap-3">
            <Skeleton className="h-16 rounded-lg" />
            <Skeleton className="h-40 rounded-lg" />
          </div>
        ) : !available ? (
          <NotConfigured />
        ) : (
          !atLeast(role, "admin") && <p className="-mt-3 text-[12.5px] text-fg-3">{t("settings.read_only")}</p>
        )}

        <Section>
          <div className="flex items-center gap-3.5 py-3.5">
            <IconTile tone={available && draft.enabled ? AI_TONE : "gray"} className="size-9 transition-colors duration-200">
              <AiIcon className={cn(!(available && draft.enabled) && "text-current")} />
            </IconTile>
            <div className="min-w-0 flex-1">
              <div className="text-[12.5px] font-medium text-fg">{t("settings.enabled")}</div>
              <div className="mt-0.5 text-[12px] leading-[18px] text-fg-3">{t("settings.enabled_hint")}</div>
            </div>
            <Switch checked={available && draft.enabled} disabled={!canEdit} onCheckedChange={(enabled) => set({ enabled })} aria-label={t("settings.enabled")} />
          </div>
        </Section>

        <Section title={t("settings.assistant_section", { name: assistantName })} description={t("settings.assistant_section_hint", { name: assistantName })}>
          <FeatureRow
            label={t("settings.assistant", { name: assistantName })}
            description={t("settings.assistant_hint", { name: assistantName })}
            cost={config?.costs?.assistant}
            checked={draft.assistant}
            disabled={off}
            onChange={(assistant) => set({ assistant })}
          />
          <FeatureRow
            label={t("settings.assistant_peer", { name: assistantName })}
            description={t("settings.assistant_peer_hint", { name: assistantName })}
            cost={config?.costs?.assistant_peer}
            checked={draft.assistant && !!draft.assistant_peer}
            disabled={off || !draft.assistant}
            onChange={(assistant_peer) => set({ assistant_peer })}
          />
        </Section>

        <AutoRunSettings name={assistantName} disabled={off || !draft.assistant} />

        <Section title={t("settings.automatic")} description={t("settings.automatic_hint")}>
          <FeatureRow
            label={t("settings.auto_triage")}
            description={t("settings.auto_triage_hint")}
            cost={config?.costs?.triage}
            checked={draft.auto_triage}
            disabled={off}
            onChange={(auto_triage) => set({ auto_triage })}
          />
          <FeatureRow
            label={t("settings.auto_summarize")}
            description={t("settings.auto_summarize_hint")}
            cost={config?.costs?.summarize}
            checked={draft.auto_summarize}
            disabled={off}
            onChange={(auto_summarize) => set({ auto_summarize })}
          />
          <FeatureRow
            label={t("settings.widget_answers")}
            description={
              <>
                {t("settings.widget_answers_hint")}{" "}
                <Link to="/settings/ai/behaviour" className="text-fg-2 underline decoration-line-strong underline-offset-2 hover:text-fg">
                  {t("settings.widget_answers_link")}
                </Link>
              </>
            }
            cost={config?.costs?.answer}
            checked={draft.widget_answers}
            disabled={off}
            onChange={(widget_answers) => set({ widget_answers })}
          />
        </Section>

        <Section title={t("settings.writing")} description={t("settings.writing_hint")}>
          <Row label={t("settings.tone")} description={t("settings.tone_hint")} stack>
            <div className={cn("flex flex-col items-start gap-2", off && "pointer-events-none opacity-60")} aria-disabled={off || undefined}>
              <Segmented<ToneChoice>
                value={custom ? "custom" : toneChoice(draft.tone)}
                onChange={(v) => {
                  if (v === "custom") {
                    setCustom(true);
                    set({ tone: toneChoice(base.tone) === "custom" ? base.tone : "" });
                  } else {
                    setCustom(false);
                    set({ tone: v });
                  }
                }}
                options={[
                  { value: "friendly", label: t("tone.friendly") },
                  { value: "professional", label: t("tone.professional") },
                  { value: "concise", label: t("tone.concise") },
                  { value: "custom", label: t("tone.custom") },
                ]}
              />
              {custom && (
                <div className="flex w-full flex-col gap-1">
                  <TextInput
                    value={draft.tone}
                    autoFocus
                    disabled={off}
                    invalid={toneTooLong || !draft.tone.trim()}
                    aria-label={t("settings.custom_tone")}
                    placeholder={t("settings.custom_tone_placeholder")}
                    onChange={(e) => set({ tone: e.target.value })}
                  />
                  <Counter n={draft.tone.length} max={TONE_MAX} />
                </div>
              )}
            </div>
          </Row>
          <Row label={t("settings.reply_language")} description={t("settings.reply_language_hint")}>
            <div className={cn(off && "pointer-events-none opacity-60")} aria-disabled={off || undefined}>
              <Segmented<AISettings["reply_language"]>
                value={draft.reply_language}
                onChange={(reply_language) => set({ reply_language })}
                options={[
                  { value: "customer", label: t("settings.reply_customer") },
                  { value: "workspace", label: t("settings.reply_workspace") },
                ]}
              />
            </div>
          </Row>
          <Row label={t("settings.instructions")} description={t("settings.instructions_hint")} stack>
            <div className="flex flex-col gap-1">
              <Textarea
                value={draft.instructions}
                disabled={off}
                invalid={instructionsTooLong}
                rows={5}
                aria-label={t("settings.instructions")}
                placeholder={t("settings.instructions_placeholder")}
                onChange={(e) => set({ instructions: e.target.value })}
              />
              <Counter n={draft.instructions.length} max={INSTRUCTIONS_MAX} />
            </div>
          </Row>
        </Section>

        {available && <ProviderSection canEdit={canEdit} />}

        {available && config && (
          <Section title={t("settings.model")}>
            <Row label={t("settings.provider")}>
              <span className="mono text-[12px] text-fg-2">{config.provider}</span>
            </Row>
            <Row label={t("settings.main_model")} description={t("settings.main_model_hint")} stack>
              <ModelPicker
                value={draft.model ?? ""}
                onChange={(model) => set({ model })}
                models={models?.data}
                defaultId={models?.default || config.models.default}
                disabled={off}
                aria-label={t("settings.main_model")}
                className="max-w-[460px]"
              />
            </Row>
            <Row label={t("settings.fast_model")} description={t("settings.fast_model_hint")} stack>
              <ModelPicker
                value={draft.fast_model ?? ""}
                onChange={(fast_model) => set({ fast_model })}
                models={models?.data}
                defaultId={models?.fast || config.models.fast}
                disabled={off}
                aria-label={t("settings.fast_model")}
                className="max-w-[460px]"
              />
            </Row>
            <Row label={t("settings.search")} description={config.embeddings ? t("settings.embeddings_on") : t("settings.embeddings_off")}>
              <span className={cn("inline-flex h-5 items-center gap-1.5 rounded-sm px-1.5 text-[11px] font-medium", config.embeddings ? tone[AI_TONE].chip : tone.gray.chip)}>
                <span className={cn("size-1.5 rounded-full", config.embeddings ? tone[AI_TONE].solid : tone.gray.solid)} aria-hidden />
                {config.embeddings ? t("settings.semantic") : t("settings.keyword")}
              </span>
            </Row>
          </Section>
        )}

        {available && config?.own_key && <p className="-mt-4 text-[12px] leading-[18px] text-fg-3">{t("settings.own_key_costs")}</p>}

        {available && config && !config.own_key && Object.keys(config.costs).length > 0 && (
          <Section
            title={t("settings.costs")}
            description={config.metered ? t("settings.costs_metered_hint") : t("settings.costs_hint")}
            actions={
              <Link to="/settings/billing" className="inline-flex items-center gap-1 text-[12.5px] text-fg-2 hover:text-fg">
                {t("settings.billing_link")}
                <ArrowUpRight className="size-3.5" />
              </Link>
            }
            flush
          >
            <table className="w-full text-[12.5px]">
              <tbody>
                {FEATURE_ORDER.filter((f) => config.costs[f] !== undefined).map((f) => (
                  <tr key={f} className="border-b border-line-soft last:border-0">
                    <td className="px-4 py-2.5 text-fg-2">{t(`features.${f}`)}</td>
                    <td className="px-4 py-2.5 text-right">
                      <CostChip usd={config.costs[f]!} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </Section>
        )}
      </div>

      <SaveBar
        dirty={dirty && canEdit}
        saving={save.isPending}
        disabled={invalid}
        onSave={() => save.mutate({ ...draft, tone: draft.tone.trim() })}
        onDiscard={() => {
          setDraft(base);
          setCustom(toneChoice(base.tone) === "custom");
        }}
      />
    </SettingsPage>
  );
}

function FeatureRow({
  label,
  description,
  cost,
  checked,
  disabled,
  onChange,
}: {
  label: string;
  description: React.ReactNode;
  cost: number | undefined;
  checked: boolean;
  disabled: boolean;
  onChange: (v: boolean) => void;
}) {
  const { data: config } = useQuery(q.aiConfig());
  return (
    <Row
      label={label}
      description={
        <>
          {description}
          {cost !== undefined && !config?.own_key && <CostChip usd={cost} className="ml-1.5 align-[1px]" />}
        </>
      }
    >
      <Switch checked={checked} disabled={disabled} onCheckedChange={onChange} aria-label={label} />
    </Row>
  );
}

/** A typical cost in US dollars, in the AI hue: "about $0.0135" when each call is charged on what it uses. */
function CostChip({ usd, className }: { usd: number; className?: string }) {
  const text = useCostText();
  return <span className={cn("mono inline-flex h-[18px] items-center rounded-sm px-1.5 text-[10.5px] tabular-nums", tone[AI_TONE].chip, className)}>{text(usd)}</span>;
}

function Counter({ n, max }: { n: number; max: number }) {
  return (
    <span className={cn("mono self-end text-[11px] tabular-nums", n > max ? "text-danger-fg" : "text-fg-4")}>
      {n.toLocaleString()} / {max.toLocaleString()}
    </span>
  );
}

function NotConfigured() {
  const { t } = useTranslation("ai");
  return (
    <div className="flex animate-rise-in gap-3.5 rounded-lg border border-line bg-surface px-4 py-4 shadow-xs">
      <IconTile tone={AI_TONE}>
        <AiIcon />
      </IconTile>
      <div className="min-w-0 text-[12.5px] leading-5">
        <p className="text-[13px] font-medium text-fg">{t("settings.not_configured")}</p>
        <p className="mt-1 text-fg-3">
          <Trans
            t={t}
            i18nKey="settings.not_configured_hint"
            components={{ code: <code className="mono rounded bg-sunken px-1 py-px text-[11.5px] text-fg-2" /> }}
          />
        </p>
        <a
          href="https://docs.opendesk.sh/self-hosting/ai"
          target="_blank"
          rel="noreferrer"
          className="mt-2 inline-flex items-center gap-1 text-fg-2 underline decoration-line-strong underline-offset-2 hover:text-fg"
        >
          {t("settings.docs_link")}
          <ArrowUpRight className="size-3.5" />
        </a>
      </div>
    </div>
  );
}
