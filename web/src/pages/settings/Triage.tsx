// Settings > Triage: whether the AI teammate (Ollie by default) reads each new
// ticket, what it may change, and whether it assigns afterwards
// (contracts/api.md "Triage"). Onboarding links here as /settings/triage.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { ArrowRight, Bot, Gauge, Globe, Smile, Tag, Users } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Row, Section, SettingsPage } from "@/components/layout";
import { PriorityIcon, SeverityChip } from "@/components/indicators";
import { Slider } from "@/components/ui/slider";
import { OllieAvatar, SetByOllie, useAssistantName } from "@/components/tickets/ollie";
import { TagChip } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast";
import { api } from "@/lib/api/endpoints";
import type { DecisionEngine, TriageField, TriageSettings } from "@/lib/api/types";
import { cn } from "@/lib/cn";
import { dur, springs } from "@/lib/motion";
import { q, qk } from "@/lib/queries";
import { atLeast, useRole } from "@/lib/session";
import { AI_TONE, tone } from "@/lib/tones";
import { inSentence } from "@/i18n";

const FIELDS: TriageField[] = ["priority", "severity", "labels", "team"];
const fieldIcon: Record<TriageField, ReactNode> = {
  priority: <Gauge />,
  severity: <SeverityChip severity="sev2" className="h-[14px] min-w-[18px] text-[9px]" />,
  labels: <Tag />,
  team: <Users />,
};

export default function Triage() {
  const { t } = useTranslation("settingsInbox");
  const name = useAssistantName();
  const canEdit = atLeast(useRole(), "admin");
  const qc = useQueryClient();
  const { data: s, isLoading } = useQuery(q.triage());
  const { data: assignment } = useQuery(q.assignment());

  const write = useMutation({
    mutationFn: (b: Partial<Omit<TriageSettings, "available" | "engine">>) => api.triage.update(b),
    onMutate: async (b) => {
      await qc.cancelQueries({ queryKey: qk.triage });
      const prev = qc.getQueryData<TriageSettings>(qk.triage);
      if (prev) qc.setQueryData<TriageSettings>(qk.triage, { ...prev, ...b });
      return { prev };
    },
    onError: (e, _b, ctx) => {
      if (ctx?.prev) qc.setQueryData(qk.triage, ctx.prev);
      toast.error(e, t("triage.saveFailed"));
    },
    onSuccess: (v) => {
      qc.setQueryData(qk.triage, v);
      void qc.invalidateQueries({ queryKey: qk.onboarding });
      void qc.invalidateQueries({ queryKey: qk.aiConfig });
    },
  });

  const off = !!s && !s.available;
  const disabled = !canEdit || off;
  const toggleField = (f: TriageField, on: boolean) => {
    if (!s) return;
    const next = on ? FIELDS.filter((x) => x === f || s.fields.includes(x)) : s.fields.filter((x) => x !== f);
    write.mutate({ fields: next });
  };
  const mode = assignment?.mode ?? "none";

  return (
    <SettingsPage title={t("triage.title")} description={t("triage.description", { name })}>
      {off && (
        <div className={cn("flex animate-rise-in items-center gap-3 rounded-lg border px-4 py-3", tone[AI_TONE].line, tone[AI_TONE].soft)}>
          <span className={cn("flex size-8 shrink-0 items-center justify-center rounded-md bg-surface", tone[AI_TONE].text)}>
            <Bot className="size-4" />
          </span>
          <div className="min-w-0 flex-1">
            <div className="text-[13px] font-medium">{t("triage.unavailable.title")}</div>
            <div className="text-[12px] text-fg-2">{t("triage.unavailable.body", { name })}</div>
          </div>
          <Button asChild variant="primary" iconRight={<ArrowRight className="size-3.5" />}>
            <Link to="/settings/ai">{t("triage.unavailable.action")}</Link>
          </Button>
        </div>
      )}

      <Explainer s={s} name={name} />

      <Section title={t("triage.engine.section")}>
        {s ? (
          <>
            <EngineRow engine={s.engine} name={name} />
            <ThresholdRow
              value={s.threshold}
              llm={s.engine.provider !== "typesafe"}
              disabled={disabled}
              name={name}
              onCommit={(threshold) => threshold !== s.threshold && write.mutate({ threshold })}
            />
          </>
        ) : (
          <div className="flex flex-col gap-3 py-4">
            <Skeleton className="h-5 w-1/2" />
            <Skeleton className="h-5 w-2/3" />
          </div>
        )}
      </Section>

      <fieldset disabled={disabled} className={cn("flex flex-col gap-8 transition-opacity duration-200", off && "opacity-60")}>
        <Section title={t("triage.section")}>
          {isLoading || !s ? (
            <div className="flex flex-col gap-3 py-4">
              <Skeleton className="h-5 w-2/3" />
              <Skeleton className="h-16" />
            </div>
          ) : (
            <>
              <Row label={t("triage.auto.label")} description={t("triage.auto.hint", { name })}>
                <Switch aria-label={t("triage.auto.label")} checked={s.auto_triage} disabled={disabled} onCheckedChange={(auto_triage) => write.mutate({ auto_triage })} />
              </Row>
              <Row label={t("triage.fields.label", { name })} description={t("triage.fields.hint")} stack>
                <div className="grid gap-2 sm:grid-cols-2">
                  {FIELDS.map((f) => (
                    <FieldOption
                      key={f}
                      field={f}
                      checked={s.fields.includes(f)}
                      disabled={disabled}
                      onChange={(on) => toggleField(f, on)}
                      icon={fieldIcon[f]}
                    />
                  ))}
                </div>
                <p className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-[12px] text-fg-3">
                  <span>{t("triage.fields.always")}</span>
                  <span className="inline-flex items-center gap-1">
                    <Smile className="size-3 text-fg-4" />
                    {t("triage.fields.sentiment")}
                  </span>
                  <span className="inline-flex items-center gap-1">
                    <Globe className="size-3 text-fg-4" />
                    {t("triage.fields.language")}
                  </span>
                  <span className="inline-flex items-center gap-1">
                    <Tag className="size-3 text-fg-4" />
                    {t("triage.fields.category")}
                  </span>
                </p>
              </Row>
            </>
          )}
        </Section>

        <Section title={t("triage.after.section")}>
          {s ? (
            <Row
              label={t("triage.after.label")}
              description={
                <>
                  {mode === "none" ? t("triage.after.hintNone") : t("triage.after.hintMode", { mode: inSentence(t(`assignment.modes.${mode}.label`)) })}{" "}
                  <Link to="/settings/assignment" className="text-fg-2 underline decoration-line-strong underline-offset-2 hover:text-fg">
                    {t("triage.after.link")}
                  </Link>
                </>
              }
            >
              <Switch
                aria-label={t("triage.after.label")}
                checked={s.assign_after_triage}
                disabled={disabled}
                onCheckedChange={(assign_after_triage) => write.mutate({ assign_after_triage })}
              />
            </Row>
          ) : (
            <div className="py-4">
              <Skeleton className="h-5 w-1/2" />
            </div>
          )}
        </Section>
      </fieldset>
    </SettingsPage>
  );
}

/** Which engine decides: the decision model, the LLM fallback, or nothing yet. The model is never named. */
function EngineRow({ engine, name }: { engine: DecisionEngine; name: string }) {
  const { t } = useTranslation("settingsInbox");
  const jev = engine.provider === "typesafe";
  const status =
    jev ? (engine.connected ? { text: t("triage.engine.connected"), hue: "green" as const } : { text: t("triage.engine.unreachable"), hue: "amber" as const })
    : engine.provider === "llm" ? { text: t("triage.engine.llm"), hue: "gray" as const }
    : { text: t("triage.engine.none"), hue: "gray" as const };
  return (
    <Row
      label={jev ? t("triage.engine.jev") : engine.provider === "llm" ? t("triage.engine.llm") : t("triage.engine.label")}
      description={jev ? t("triage.engine.jevHint", { name }) : engine.provider === "llm" ? t("triage.engine.llmHint") : t("triage.engine.noneHint", { name })}
    >
      <div className="flex items-center gap-2">
        {engine.model && !jev && (
          <span className="mono hidden text-[11px] text-fg-4 sm:inline" title={t("triage.engine.model")}>
            {engine.model}
          </span>
        )}
        <span key={status.text} className={cn("inline-flex h-[20px] animate-indicator-in items-center gap-1.5 rounded-sm px-1.5 text-[11.5px] font-medium motion-reduce:animate-none", tone[status.hue].chip)}>
          <span className={cn("size-1.5 rounded-full", tone[status.hue].solid)} />
          {status.text}
        </span>
      </div>
    </Row>
  );
}

/** The confidence a decision needs before it is applied; below it, it waits as a suggestion. */
function ThresholdRow({ value, llm, disabled, name, onCommit }: { value: number; llm: boolean; disabled: boolean; name: string; onCommit: (v: number) => void }) {
  const { t } = useTranslation("settingsInbox");
  const [v, setV] = useState(value);
  useEffect(() => setV(value), [value]);
  const pct = Math.round(v * 100);
  return (
    <Row label={t("triage.threshold.label")} description={<>{t("triage.threshold.hint", { name })}{llm && <> {t("triage.threshold.llmNote")}</>}</>} stack>
      <div className="flex items-center gap-3">
        <span className="text-[11.5px] text-fg-4">{t("triage.threshold.eager")}</span>
        <Slider
          value={v}
          min={0.5}
          max={0.99}
          step={0.01}
          disabled={disabled}
          onChange={setV}
          onCommit={(x) => onCommit(Number(x.toFixed(2)))}
          aria-label={t("triage.threshold.label")}
          valueText={(x) => t("triage.threshold.value", { pct: Math.round(x * 100) })}
          className="max-w-[320px] flex-1"
        />
        <span className="text-[11.5px] text-fg-4">{t("triage.threshold.cautious")}</span>
        <span className="mono w-10 text-right text-[12.5px] font-medium tabular-nums">{t("triage.threshold.value", { pct })}</span>
      </div>
    </Row>
  );
}

function FieldOption({
  field,
  checked,
  disabled,
  onChange,
  icon,
}: {
  field: TriageField;
  checked: boolean;
  disabled: boolean;
  onChange: (v: boolean) => void;
  icon: ReactNode;
}) {
  const { t } = useTranslation("settingsInbox");
  return (
    <label
      className={cn(
        "flex cursor-pointer items-start gap-3 rounded-lg border p-3 transition-[background-color,border-color] duration-150",
        checked ? "border-line-strong bg-surface" : "border-line bg-sunken/60 hover:bg-sunken",
        disabled && "cursor-default",
      )}
    >
      <Checkbox checked={checked} disabled={disabled} onCheckedChange={onChange} aria-label={t(`triage.fields.${field}.label`)} className="mt-0.5" />
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-1.5 text-[13px] font-medium [&_svg]:size-3.5 [&_svg]:text-fg-3">
          {icon}
          {t(`triage.fields.${field}.label`)}
        </span>
        <span className="mt-0.5 block text-[12px] leading-[17px] text-fg-3">{t(`triage.fields.${field}.hint`)}</span>
      </span>
    </label>
  );
}

/** A sample ticket showing what triage would set, attributed to the assistant. */
function Explainer({ s, name }: { s?: TriageSettings; name: string }) {
  const { t } = useTranslation("settingsInbox");
  const reduce = useReducedMotion();
  const on = !!s?.auto_triage && !!s.available;
  const lines: { key: string; node: ReactNode }[] = [];
  if (s?.fields.includes("priority"))
    lines.push({
      key: "priority",
      node: (
        <>
          <PriorityIcon priority="high" />
          {t("triage.sample.priority")}
        </>
      ),
    });
  if (s?.fields.includes("severity"))
    lines.push({
      key: "severity",
      node: (
        <>
          <SeverityChip severity="sev2" />
          {t("triage.sample.severity")}
        </>
      ),
    });
  if (s?.fields.includes("labels"))
    lines.push({
      key: "labels",
      node: (
        <>
          <TagChip name={t("triage.sample.label")} color="green" className="text-[11px]" />
          {t("triage.sample.labelAdded")}
        </>
      ),
    });
  lines.push({ key: "sentiment", node: <>{t("triage.sample.sentiment")}</> });
  if (s?.assign_after_triage) lines.push({ key: "assign", node: <>{t("triage.sample.assigned")}</> });

  return (
    <div className="grid animate-rise-in gap-4 rounded-lg border border-line bg-sunken/50 p-4 md:grid-cols-[1fr_300px]">
      <div className="flex min-w-0 gap-3">
        <OllieAvatar size={32} />
        <div className="min-w-0">
          <div className="flex items-center gap-2 text-[13px] font-medium">
            {t("triage.explainer.title", { name })}
            <span
              className={cn(
                "inline-flex h-[18px] items-center gap-1 rounded-sm px-1.5 text-[11px] font-medium transition-colors duration-200",
                on ? tone.green.chip : "bg-hover text-fg-3",
              )}
            >
              <span className={cn("size-1.5 rounded-full", on ? tone.green.solid : "bg-fg-4")} />
              {on ? t("triage.explainer.on") : t("triage.explainer.off")}
            </span>
          </div>
          <p className="mt-1 text-[12.5px] leading-5 text-fg-2">{t("triage.explainer.body", { name })}</p>
        </div>
      </div>
      <div className="rounded-md border border-line-soft bg-surface p-3 shadow-xs" aria-label={t("triage.sample.aria")}>
        <div className="truncate text-[12.5px] font-medium">{t("triage.sample.subject")}</div>
        <div className="mt-0.5 truncate text-[11.5px] text-fg-4">{t("triage.sample.from")}</div>
        <ul className="mt-2.5 flex flex-col gap-1.5 border-t border-line-soft pt-2.5">
          <AnimatePresence initial={false}>
            {lines.map((l) => (
              <motion.li
                key={l.key}
                layout={reduce ? false : "position"}
                initial={reduce ? false : { opacity: 0, y: -4 }}
                animate={{ opacity: 1, y: 0 }}
                exit={reduce ? { opacity: 0, transition: { duration: 0 } } : { opacity: 0, transition: { duration: dur.fast } }}
                transition={springs.row}
                className="flex items-center gap-1.5 text-[11.5px] text-fg-2"
              >
                <OllieAvatar size={14} />
                {l.node}
                <SetByOllie className="ml-auto" />
              </motion.li>
            ))}
          </AnimatePresence>
        </ul>
      </div>
    </div>
  );
}
