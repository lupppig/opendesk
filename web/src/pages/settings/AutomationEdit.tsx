import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams } from "@tanstack/react-router";
import { Plus, Trash2, X } from "lucide-react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { BackLink, NoAccess, SettingsPage } from "@/components/layout";
import { Button } from "@/components/ui/button";
import { Field, TextInput, Textarea } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { EmptyState } from "@/components/ui/empty";
import { Tooltip } from "@/components/ui/tooltip";
import { useConfirm } from "@/components/ui/confirm";
import { toast } from "@/components/ui/toast";
import { RichEditor } from "@/components/editor/RichEditor";
import { TemplateEditor } from "@/components/templates/TemplateEditor";
import { UnsavedGuard } from "@/components/settings2/UnsavedGuard";
import {
  SinglePick,
  TagPick,
  TriggerIcon,
  channelItems,
  priorityItems,
  sentimentItems,
  severityFilterItems,
  severityItems,
  statusItems,
  triggerItems,
  useMailboxItems,
  useMemberItems,
  useTeamItems,
} from "@/components/settings2/pickers";
import { rowSpring } from "@/components/settings2/motion";
import {
  MAX_PHRASES,
  actionFitsTrigger,
  actionLabel,
  actionNeedsValue,
  aiActions,
  aiFields,
  brandFields,
  fieldLabel,
  isPhraseOp,
  mergeWindows,
  newAction,
  offered,
  opLabel,
  opsFor,
  phrases,
  teamFields,
  validMergeWindow,
  validPhrases,
  validProbability,
  validQuestion,
} from "@/components/settings2/automation";
import { brandItems, teamItems, useBrands, useTeams } from "@/components/settings2/teams";
import { AiIcon, useCostText } from "@/components/ai/bits";
import { formatUsd } from "@/lib/format";
import { useAI } from "@/lib/ai";
import { useTranslation } from "react-i18next";
import { api } from "@/lib/api/endpoints";
import { errorMessage } from "@/lib/api/client";
import type { Action, ActionType, Automation, AutomationTrigger, Condition, ConditionField, ConditionOp, FieldCondition, FieldOp } from "@/lib/api/types";
import {
  FieldValueInput,
  fieldConditionComplete,
  fieldOpsFor,
  isFieldRef,
  parseFieldRef,
  useAutomationFields,
  useFieldConditionItems,
  useFieldOpLabel,
  type FieldRef,
} from "@/components/settings2/fieldRules";
import { typeIcon } from "@/components/settings2/fields/model";
import { q, qk, removeFromList, upsertInList } from "@/lib/queries";
import { atLeast, useRole } from "@/lib/session";
import { cn } from "@/lib/cn";
import { tone, type Tone } from "@/lib/tones";

/** A builder rule: a built-in condition, or one on a custom field with its type's operators. */
type Rule = Condition | FieldCondition;
const isFieldRule = (c: Rule): c is FieldCondition => isFieldRef(c.field);

type Draft = {
  name: string;
  trigger: AutomationTrigger;
  conditions: Rule[];
  actions: Action[];
  enabled: boolean;
};

const blank: Draft = {
  name: "",
  trigger: "ticket_created",
  conditions: [{ field: "subject", op: "contains", value: "" }],
  actions: [{ type: "add_tag", value: "" }],
  enabled: true,
};

const allFields = Object.keys(fieldLabel) as ConditionField[];
const allActions = Object.keys(actionLabel) as ActionType[];

/** Builder lists; AI fields and actions only when the instance has AI, unless already in use. */
function useFieldItems(current: string) {
  const { enabled } = useAI();
  const { t } = useTranslation("settingsInbox");
  const custom = useFieldConditionItems(current);
  const { teams } = useTeams();
  const { multi } = useBrands();
  const builtIn = offered(allFields, aiFields, enabled, isFieldRef(current) ? undefined : (current as ConditionField))
    .filter((f) => f === current || ((teams.length > 0 || !teamFields.has(f)) && (multi || !brandFields.has(f))))
    .map((f) => ({
    value: f as string,
    label: fieldLabel[f],
    icon: aiFields.has(f) ? <AiIcon /> : undefined,
    group: custom.length ? t("customFields.ticketBasics") : undefined,
  }));
  return [...builtIn, ...custom];
}
function useActionItems(current: ActionType, trigger: AutomationTrigger) {
  const { enabled, cost } = useAI();
  const costText = useCostText();
  const { all } = useAutomationFields();
  const hasTicketFields = all.some((f) => f.entity === "ticket" && !f.archived);
  return offered(allActions, aiActions, enabled, current)
    .filter((a) => a !== "set_field" || hasTicketFields || current === "set_field")
    .filter((a) => actionFitsTrigger(a, trigger) || a === current)
    .map((a) => {
      const n = aiActions.has(a) ? cost("triage") : null;
      return {
        value: a,
        label: actionLabel[a],
        icon: aiActions.has(a) ? <AiIcon /> : undefined,
        hint: n !== null ? costText(n) : undefined,
      };
    });
}

const isEmptyHtml = (h: string) => !h || h.replace(/<[^>]+>/g, "").trim() === "";

export default function AutomationEdit() {
  const { automationId } = useParams({
    from: "/app/settings/automations/$automationId",
  });
  const isNew = automationId === "new";
  const role = useRole();
  const { data: all, isLoading } = useQuery({
    ...q.automations(),
    enabled: atLeast(role, "admin"),
  });
  const existing = all?.find((a) => a.id === automationId);
  const qc = useQueryClient();
  const navigate = useNavigate();
  const confirm = useConfirm();
  const { t } = useTranslation("settingsInbox");
  const reduce = useReducedMotion();
  // Stable keys for builder rows, so a removed row folds away instead of its neighbour.
  const seq = useRef(0);
  const [condKeys, setCondKeys] = useState<number[]>([]);
  const [actKeys, setActKeys] = useState<number[]>([]);

  const initial: Draft | null = useMemo(() => {
    if (isNew) return blank;
    if (!existing) return null;
    return {
      name: existing.name,
      trigger: existing.trigger,
      conditions: existing.conditions as Rule[],
      actions: existing.actions,
      enabled: existing.enabled,
    };
  }, [isNew, existing]);

  const [draft, setDraft] = useState<Draft | null>(initial);
  const [showErrors, setShowErrors] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  useEffect(() => {
    if (initial && !draft) setDraft(initial);
  }, [initial, draft]);
  if (draft && condKeys.length !== draft.conditions.length) setCondKeys(draft.conditions.map(() => seq.current++));
  if (draft && actKeys.length !== draft.actions.length) setActKeys(draft.actions.map(() => seq.current++));

  const dirty = !!draft && !!initial && !saved && JSON.stringify(draft) !== JSON.stringify(initial);

  const save = useMutation({
    mutationFn: (d: Draft) => {
      const body = {
        ...d,
        // The API type narrows ops to the built-in set; field rules carry their own (contracts/api.md).
        conditions: d.conditions as Condition[],
        name: d.name.trim(),
        position: existing?.position ?? all?.length ?? 0,
      };
      return isNew ? api.automations.create(body) : api.automations.update(automationId, body);
    },
    onSuccess: (a: Automation) => {
      upsertInList(qc, qk.automations, a);
      toast.success(isNew ? t("edit.created") : t("edit.saved"));
      setSaved(true);
      if (isNew)
        setTimeout(
          () =>
            void navigate({
              to: "/settings/automations/$automationId",
              params: { automationId: a.id },
              replace: true,
            }),
        );
      else
        setDraft({
          name: a.name,
          trigger: a.trigger,
          conditions: a.conditions as Rule[],
          actions: a.actions,
          enabled: a.enabled,
        });
    },
    onError: (e) => setError(errorMessage(e)),
  });
  useEffect(() => setSaved(false), [existing]);

  if (!atLeast(role, "admin"))
    return (
      <SettingsPage title={t("edit.fallbackTitle")}>
        <NoAccess what="automations" />
      </SettingsPage>
    );

  const back = <BackLink to="/settings/automations">{t("edit.back")}</BackLink>;

  if (!isNew && !isLoading && !existing)
    return (
      <SettingsPage title={t("edit.fallbackTitle")} back={back}>
        <EmptyState
          lit={3}
          title={t("edit.notFound")}
          description={t("edit.notFoundHint")}
          action={
            <Button size="sm" onClick={() => void navigate({ to: "/settings/automations" })}>
              {t("edit.backToList")}
            </Button>
          }
        />
      </SettingsPage>
    );
  if (!draft)
    return (
      <SettingsPage title={t("edit.fallbackTitle")} back={back}>
        <div className="flex flex-col gap-5" aria-busy="true">
          <div className="skeleton h-8 w-2/3 rounded-md" />
          <div className="skeleton h-16 rounded-lg" />
          <div className="skeleton h-28 rounded-lg" />
          <div className="skeleton h-20 rounded-lg" />
        </div>
      </SettingsPage>
    );

  const set = (patch: Partial<Draft>) => setDraft((d) => (d ? { ...d, ...patch } : d));
  const condInvalid = (c: Rule) =>
    isFieldRule(c)
      ? !fieldConditionComplete(c)
      : c.field === "ai_decide"
        ? !validProbability(c.value) || !validQuestion(c.question)
        : isPhraseOp(c.op)
          ? !validPhrases(c.value)
          : !c.value.trim();
  /** A phrase list past the API's bounds; an empty one is a missing value. */
  const overPhrases = (c: Rule) => !isFieldRule(c) && isPhraseOp(c.op) && phrases(c.value).length > 0 && !validPhrases(c.value);
  /** An action that cannot run on the chosen trigger (a merge runs only when a ticket is created). */
  const offTrigger = (a: Action) => !actionFitsTrigger(a.type, draft.trigger);
  const actInvalid = (a: Action) =>
    offTrigger(a) ||
    (a.type === "set_field"
      ? !a.field
      : a.type === "merge_into_open_ticket"
        ? !validMergeWindow(a.value)
        : actionNeedsValue(a.type) && (a.type === "send_reply" || a.type === "add_note" ? isEmptyHtml(a.value) : !a.value.trim()));
  const problems: string[] = [];
  if (!draft.name.trim()) problems.push(t("edit.problems.name"));
  if (!draft.actions.length) problems.push(t("edit.problems.action"));
  if (draft.conditions.some((c) => condInvalid(c) && !overPhrases(c))) problems.push(t("edit.problems.condition"));
  if (draft.conditions.some(overPhrases)) problems.push(t("edit.problems.phrases"));
  if (draft.actions.some((a) => actInvalid(a) && !offTrigger(a))) problems.push(t("edit.problems.actionValue"));
  if (draft.actions.some(offTrigger)) problems.push(t("edit.problems.mergeTrigger"));

  const rowMotion = {
    layout: reduce ? false : ("position" as const),
    initial: reduce ? false : { opacity: 0, y: -4 },
    animate: { opacity: 1, y: 0 },
    exit: reduce ? { opacity: 0, transition: { duration: 0 } } : { opacity: 0, transition: { duration: 0.12 } },
    transition: rowSpring,
  };
  const addCondition = () => {
    setCondKeys((k) => [...k, seq.current++]);
    set({
      conditions: [...draft.conditions, { field: "subject", op: "contains", value: "" }],
    });
  };
  const removeCondition = (i: number) => {
    setCondKeys((k) => k.filter((_, j) => j !== i));
    set({ conditions: draft.conditions.filter((_, j) => j !== i) });
  };
  const addAction = () => {
    setActKeys((k) => [...k, seq.current++]);
    set({ actions: [...draft.actions, { type: "add_tag", value: "" }] });
  };
  const removeAction = (i: number) => {
    setActKeys((k) => k.filter((_, j) => j !== i));
    set({ actions: draft.actions.filter((_, j) => j !== i) });
  };

  const submit = () => {
    setError(null);
    setShowErrors(true);
    if (problems.length) return;
    save.mutate(draft);
  };

  return (
    <SettingsPage
      back={back}
      title={isNew ? t("edit.newTitle") : draft.name || t("edit.fallbackTitle")}
      description={isNew ? t("edit.newHint") : existing ? t("edit.ran", { n: existing.run_count.toLocaleString() }) : undefined}
      actions={
        <>
          {!isNew && existing && (
            <Button
              variant="ghost"
              icon={<Trash2 className="size-3.5" />}
              onClick={() =>
                confirm.show(
                  <>
                    <span className="font-medium text-fg">{existing.name}</span> {t("automations.deleteBody")}
                  </>,
                  async () => {
                    await api.automations.remove(existing.id);
                    removeFromList(qc, qk.automations, existing.id);
                    setSaved(true);
                    toast.success(t("edit.deleted"));
                    setTimeout(() => void navigate({ to: "/settings/automations" }));
                  },
                  {
                    title: t("automations.deleteTitle"),
                    confirmLabel: t("common.delete"),
                    danger: true,
                  },
                )
              }
            >
              {t("common.delete")}
            </Button>
          )}
          <Button variant="primary" loading={save.isPending} onClick={submit} disabled={!isNew && !dirty}>
            {isNew ? t("edit.create") : t("edit.save")}
          </Button>
        </>
      }
    >
      <UnsavedGuard when={dirty && !save.isPending} />

      <div className="flex items-end gap-4">
        <Field label={t("edit.nameLabel")} className="flex-1" error={showErrors && !draft.name.trim() ? t("edit.problems.name") : undefined}>
          {(p) => (
            <TextInput
              {...p}
              autoFocus={isNew}
              value={draft.name}
              maxLength={80}
              placeholder={t("edit.namePlaceholder")}
              onChange={(e) => set({ name: e.target.value })}
            />
          )}
        </Field>
        <label
          className={cn(
            "flex h-8 items-center gap-2.5 rounded-md px-2.5 text-[12.5px] font-medium transition-colors",
            draft.enabled ? tone.green.chip : "bg-hover text-fg-3",
          )}
        >
          <span className={cn("size-1.5 rounded-full", draft.enabled ? tone.green.solid : "bg-fg-4")} aria-hidden />
          {draft.enabled ? t("edit.on") : t("edit.off")}
          <Switch checked={draft.enabled} onCheckedChange={(enabled) => set({ enabled })} aria-label={t("edit.enabled")} />
        </label>
      </div>

      <div className="flex flex-col">
        <Step n={1} title={t("edit.when")} hue="blue">
          <div className="flex flex-wrap items-center gap-2.5 px-4 py-3">
            <TriggerIcon trigger={draft.trigger} />
            <Select
              size="sm"
              value={draft.trigger}
              onChange={(trigger) => set({ trigger })}
              options={triggerItems()}
              className="w-[240px]"
              aria-label={t("edit.trigger")}
            />
          </div>
        </Step>

        <Step
          n={2}
          hue="amber"
          title={draft.conditions.length ? t("edit.ifAll") : t("edit.everyTicket")}
          hint={draft.conditions.length ? undefined : t("edit.noConditions")}
        >
          <AnimatePresence initial={false}>
            {draft.conditions.map((c, i) => (
              <motion.div key={condKeys[i] ?? `c${i}`} {...rowMotion} className="border-b border-line-soft">
                <ConditionRow
                  c={c}
                  first={i === 0}
                  invalid={showErrors && condInvalid(c)}
                  onChange={(nc) =>
                    set({
                      conditions: draft.conditions.map((x, j) => (j === i ? nc : x)),
                    })
                  }
                  onRemove={() => removeCondition(i)}
                />
              </motion.div>
            ))}
          </AnimatePresence>
          <div className="px-3 py-2">
            <Button size="xs" variant="ghost" icon={<Plus className="size-3" />} onClick={addCondition}>
              {t("edit.addCondition")}
            </Button>
          </div>
        </Step>

        <Step n={3} title={t("edit.then")} hue="green" last>
          <AnimatePresence initial={false}>
            {draft.actions.map((a, i) => (
              <motion.div key={actKeys[i] ?? `a${i}`} {...rowMotion} className="border-b border-line-soft">
                <ActionRow
                  a={a}
                  trigger={draft.trigger}
                  invalid={showErrors && actInvalid(a)}
                  onChange={(na) =>
                    set({
                      actions: draft.actions.map((x, j) => (j === i ? na : x)),
                    })
                  }
                  onRemove={() => removeAction(i)}
                />
              </motion.div>
            ))}
          </AnimatePresence>
          {!draft.actions.length && <p className={cn("px-4 pt-3 text-[12.5px]", showErrors ? "text-danger-fg" : "text-fg-3")}>{t("edit.problems.action")}</p>}
          <div className="px-3 py-2">
            <Button size="xs" variant="ghost" icon={<Plus className="size-3" />} onClick={addAction}>
              {t("edit.addAction")}
            </Button>
          </div>
        </Step>
      </div>

      {(error || (showErrors && problems.length > 0)) && (
        <div role="alert" className={cn("-mt-3 flex animate-rise-in flex-col gap-1 rounded-lg border px-3.5 py-3 text-[12.5px]", tone.red.line, tone.red.soft)}>
          {error && <p className="text-danger-fg">{error}</p>}
          {showErrors &&
            problems.map((p) => (
              <p key={p} className="flex items-center gap-2 text-fg-2">
                <span className={cn("size-1.5 rounded-full", tone.red.solid)} />
                {p}
              </p>
            ))}
        </div>
      )}
    </SettingsPage>
  );
}

/** One card in the flow, hung on a thin vertical rail with a step dot. */
function Step({ n, title, hint, children, last, hue }: { n: number; title: string; hint?: string; children: ReactNode; last?: boolean; hue: Tone }) {
  return (
    <div className="relative pb-5 pl-9 last:pb-0">
      {!last && <span aria-hidden className="absolute bottom-0 left-[11px] top-7 w-px bg-line" />}
      <span
        aria-hidden
        className={cn("mono absolute left-0 top-2 flex size-6 items-center justify-center rounded-md text-[11px] font-semibold", tone[hue].chip)}
      >
        {n}
      </span>
      <div className="mb-2 flex h-[38px] items-center gap-2">
        <h2 className="text-[13.5px] font-semibold tracking-[-0.01em]">{title}</h2>
        {hint && <span className="text-[12px] text-fg-3">{hint}</span>}
      </div>
      <div className="overflow-hidden rounded-lg border border-line bg-surface shadow-xs">{children}</div>
    </div>
  );
}

function RemoveButton({ onClick, label }: { onClick: () => void; label: string }) {
  return (
    <Tooltip content={label}>
      <button
        type="button"
        aria-label={label}
        onClick={onClick}
        className="flex size-7 shrink-0 items-center justify-center rounded-md text-fg-4 hover:bg-hover hover:text-fg"
      >
        <X className="size-3.5" />
      </button>
    </Tooltip>
  );
}

function ConditionRow({
  c,
  first,
  invalid,
  onChange,
  onRemove,
}: {
  c: Rule;
  first: boolean;
  invalid: boolean;
  onChange: (c: Rule) => void;
  onRemove: () => void;
}) {
  const { t } = useTranslation("settingsInbox");
  const fieldItems = useFieldItems(c.field);
  const { find } = useAutomationFields();
  const opLabelFor = useFieldOpLabel();
  const custom = isFieldRule(c) ? find(c.field as FieldRef) : undefined;
  const fieldOps = isFieldRule(c) ? fieldOpsFor(custom?.type) : [];
  const builtInOps = isFieldRule(c) ? [] : opsFor(c.field);

  const pickField = (v: string) => {
    if (isFieldRef(v)) {
      const f = find(v);
      const ops = fieldOpsFor(f?.type);
      onChange({ field: v, op: isFieldRule(c) && ops.includes(c.op) ? c.op : ops[0], value: "" });
    } else {
      const field = v as ConditionField;
      const nextOps = opsFor(field);
      const cur = isFieldRule(c) ? undefined : c.op;
      if (field === "ai_decide") onChange({ field, op: "gt", value: "0.7", question: "" });
      else onChange({ field, op: cur && nextOps.includes(cur) ? cur : nextOps[0], value: "" });
    }
  };

  // A phrase list is taller than the row: the row lines up on its first line.
  const list = !isFieldRule(c) && isPhraseOp(c.op);
  return (
    <div className={cn("flex flex-wrap gap-2 px-4 py-2.5", list ? "items-start" : "items-center")}>
      <span className={cn("w-9 shrink-0 text-[12px] text-fg-4", list && "leading-7")}>{first ? t("edit.if") : t("edit.and")}</span>
      <SinglePick
        items={fieldItems}
        value={c.field}
        onChange={pickField}
        placeholder={t("edit.field")}
        width={260}
        className="w-[160px]"
        renderValue={
          custom
            ? () => (
                <span className="flex min-w-0 items-center gap-1.5">
                  {parseFieldRef(c.field as FieldRef).entity === "contact" && <span className="text-fg-3">{t("customFields.contactPrefix")}</span>}
                  <span className="truncate">{custom.name}</span>
                </span>
              )
            : undefined
        }
      />
      {isFieldRule(c) ? (
        <Select<FieldOp>
          size="sm"
          value={c.op}
          onChange={(op) => onChange({ ...c, op, value: op === "set" || op === "not_set" ? "" : c.value })}
          options={fieldOps.map((o) => ({ value: o, label: opLabelFor(o, custom?.type) }))}
          className="w-[150px]"
          aria-label={t("edit.operator")}
        />
      ) : (
        <Select<ConditionOp>
          size="sm"
          value={c.op}
          // Leaving a phrase list keeps its first phrase as the single value.
          onChange={(op) => onChange({ ...c, op, value: isPhraseOp(c.op) && !isPhraseOp(op) ? (phrases(c.value)[0] ?? "") : c.value })}
          options={builtInOps.map((o) => ({ value: o, label: opLabel[o] }))}
          className="w-[150px]"
          aria-label={t("edit.operator")}
        />
      )}
      <div className="min-w-[180px] flex-1">
        {isFieldRule(c) ? (
          <FieldValueInput field={custom} op={c.op} value={c.value} invalid={invalid} onChange={(value) => onChange({ ...c, value })} />
        ) : (
          <ConditionValue c={c} invalid={invalid} onChange={(value) => onChange({ ...c, value })} />
        )}
      </div>
      <RemoveButton onClick={onRemove} label={t("edit.removeCondition")} />
      {!isFieldRule(c) && c.field === "ai_decide" && <DecideQuestion c={c} invalid={invalid} onChange={onChange} />}
    </div>
  );
}

/** ai_decide: the admin's yes/no question, asked of Ollie about the ticket. */
function DecideQuestion({ c, invalid, onChange }: { c: Condition; invalid: boolean; onChange: (c: Rule) => void }) {
  const { t } = useTranslation("ai");
  const { cost } = useAI();
  const n = cost("decide");
  return (
    <div className="basis-full pl-11">
      <Textarea
        rows={2}
        maxLength={300}
        value={c.question ?? ""}
        invalid={invalid && !validQuestion(c.question)}
        aria-label={t("automation.ai_decide_question")}
        placeholder={t("automation.ai_decide_placeholder")}
        onChange={(e) => onChange({ ...c, question: e.target.value.replace(/[\r\n]+/g, " ") })}
        className="min-h-[52px] text-[12.5px]"
      />
      <p className="mt-1 text-[11.5px] text-fg-3">
        {t("automation.ai_decide_hint")}
        {n !== null && <span className="mono ml-1.5 whitespace-nowrap text-[11px] text-fg-4">{t("automation.per_run", { cost: formatUsd(n, { precise: true }) })}</span>}
      </p>
    </div>
  );
}

function ConditionValue({ c, invalid, onChange }: { c: Condition; invalid: boolean; onChange: (v: string) => void }) {
  const { t } = useTranslation("ai");
  useTranslation("indicators");
  const { t: ts } = useTranslation("settingsInbox");
  const { t: tt } = useTranslation("settingsTeams");
  const mailboxes = useMailboxItems();
  const members = useMemberItems();
  const { teams } = useTeams();
  const { brands } = useBrands();
  const { data: widgets } = useQuery({ ...q.widgets(), enabled: c.field === "widget_id" });
  if (isPhraseOp(c.op)) {
    const n = phrases(c.value).length;
    return (
      <div>
        <Textarea
          rows={3}
          value={c.value}
          invalid={invalid && !validPhrases(c.value)}
          aria-label={ts("edit.phrasesLabel", { field: fieldLabel[c.field] })}
          placeholder={ts("edit.phrasesPlaceholder")}
          onChange={(e) => onChange(e.target.value)}
          className={cn("min-h-[64px] text-[12.5px]", (c.field === "from_email" || c.field === "received_at") && "mono")}
        />
        <p className={cn("mt-1 text-[11.5px]", n > MAX_PHRASES ? "text-danger-fg" : "text-fg-3")}>{ts("edit.phrasesCount", { count: n, max: MAX_PHRASES })}</p>
      </div>
    );
  }
  switch (c.field) {
    case "status":
      return <SinglePick items={statusItems()} value={c.value} onChange={onChange} placeholder={ts("edit.chooseStatus")} invalid={invalid} className="w-full" />;
    case "priority":
      return (
        <SinglePick items={priorityItems()} value={c.value} onChange={onChange} placeholder={ts("edit.choosePriority")} invalid={invalid} className="w-full" />
      );
    case "severity":
      return (
        <SinglePick items={severityFilterItems()} value={c.value} onChange={onChange} placeholder={ts("edit.chooseSeverity")} invalid={invalid} width={300} className="w-full" />
      );
    case "ai_decide": {
      const p = Number(c.value);
      return (
        <div className="flex items-center gap-2">
          <TextInput
            inputSize="sm"
            inputMode="decimal"
            value={c.value}
            invalid={invalid && !validProbability(c.value)}
            aria-label={t("automation.ai_decide_probability")}
            placeholder="0.7"
            onChange={(e) => onChange(e.target.value.replace(/[^0-9.]/g, "").slice(0, 4))}
            className="mono w-[72px]"
          />
          {validProbability(c.value) && <span className="text-[12px] text-fg-3">{t("automation.ai_decide_percent", { pct: Math.round(p * 100) })}</span>}
        </div>
      );
    }
    case "channel":
      return (
        <SinglePick items={channelItems()} value={c.value} onChange={onChange} placeholder={ts("edit.chooseChannel")} invalid={invalid} className="w-full" />
      );
    case "mailbox_id":
      return (
        <SinglePick
          items={mailboxes}
          value={c.value}
          onChange={onChange}
          placeholder={ts("edit.chooseMailbox")}
          invalid={invalid}
          width={300}
          className="w-full"
        />
      );
    case "team_id":
      return (
        <SinglePick
          items={[{ value: "none", label: tt("scoped.noTeam") }, ...teamItems(teams)]}
          value={c.value}
          onChange={onChange}
          placeholder={tt("common.chooseTeam")}
          invalid={invalid}
          className="w-full"
        />
      );
    case "assignee_id":
      return (
        <SinglePick
          items={[{ value: "none", label: ts("views.unassigned") }, ...members]}
          value={c.value}
          onChange={onChange}
          placeholder={ts("edit.chooseTeammate")}
          invalid={invalid}
          className="w-full"
        />
      );
    case "brand_id":
      return <SinglePick items={brandItems(brands)} value={c.value} onChange={onChange} placeholder={tt("common.chooseBrand")} invalid={invalid} className="w-full" />;
    case "widget_id":
      return (
        <SinglePick
          items={[{ value: "none", label: tt("scoped.noWidget") }, ...(widgets ?? []).map((w) => ({ value: w.id, label: w.name, hint: brands.length > 1 ? brands.find((b) => b.id === w.brand_id)?.name : undefined }))]}
          value={c.value}
          onChange={onChange}
          placeholder={tt("scoped.chooseWidget")}
          invalid={invalid}
          className="w-full"
        />
      );
    case "tag":
      return <TagPick value={c.value} onChange={onChange} placeholder={ts("edit.chooseTag")} invalid={invalid} onCreate={onChange} className="w-full" />;
    case "sentiment":
      return (
        <SinglePick
          items={sentimentItems()}
          value={c.value}
          onChange={onChange}
          placeholder={t("automation.choose_sentiment")}
          invalid={invalid}
          className="w-full"
        />
      );
    default:
      return (
        <TextInput
          inputSize="sm"
          value={c.value}
          invalid={invalid}
          aria-label={ts("edit.valueLabel", { field: fieldLabel[c.field] })}
          placeholder={c.field === "from_email" ? "@acme.com" : c.field === "received_at" ? "legal@acme.com" : c.field === "subject" ? "refund" : "invoice"}
          onChange={(e) => onChange(e.target.value)}
          className={c.field === "from_email" || c.field === "received_at" ? "mono" : undefined}
        />
      );
  }
}

function ActionRow({
  a,
  trigger,
  invalid,
  onChange,
  onRemove,
}: {
  a: Action;
  trigger: AutomationTrigger;
  invalid: boolean;
  onChange: (a: Action) => void;
  onRemove: () => void;
}) {
  const { t } = useTranslation("ai");
  useTranslation("indicators");
  const { t: ts } = useTranslation("settingsInbox");
  const { cost } = useAI();
  const actionItems = useActionItems(a.type, trigger);
  const members = useMemberItems();
  const teams = useTeamItems();
  const mailboxes = useMailboxItems();
  const { t: tt } = useTranslation("settingsTeams");
  const rich = a.type === "send_reply" || a.type === "add_note";
  const set = (value: string) => onChange({ ...a, value });

  let control: ReactNode = null;
  switch (a.type) {
    case "set_status":
      control = (
        <SinglePick
          items={statusItems().filter((s) => s.value !== "closed")}
          value={a.value}
          onChange={set}
          placeholder={ts("edit.chooseStatus")}
          invalid={invalid}
          className="w-full"
        />
      );
      break;
    case "set_priority":
      control = (
        <SinglePick items={priorityItems()} value={a.value} onChange={set} placeholder={ts("edit.choosePriority")} invalid={invalid} className="w-full" />
      );
      break;
    case "set_severity":
      control = (
        <SinglePick items={severityItems()} value={a.value} onChange={set} placeholder={ts("edit.chooseSeverity")} invalid={invalid} width={300} className="w-full" />
      );
      break;
    case "assign_user":
      control = (
        <SinglePick items={members} value={a.value} onChange={set} placeholder={ts("edit.chooseTeammate")} invalid={invalid} width={280} className="w-full" />
      );
      break;
    case "assign_team":
      control = <SinglePick items={teams} value={a.value} onChange={set} placeholder={ts("edit.chooseTeam")} invalid={invalid} className="w-full" />;
      break;
    case "set_reply_address":
      control = (
        <div className="flex flex-col gap-1">
          <SinglePick items={mailboxes} value={a.value} onChange={set} placeholder={ts("edit.chooseMailbox")} invalid={invalid} width={300} className="w-full" />
          <span className="text-[11.5px] text-fg-4">{tt("scoped.replyAddressHint")}</span>
        </div>
      );
      break;
    case "add_tag":
    case "remove_tag":
      control = (
        <TagPick
          value={a.value}
          onChange={set}
          placeholder={ts("edit.chooseTag")}
          invalid={invalid}
          onCreate={a.type === "add_tag" ? set : undefined}
          className="w-full"
        />
      );
      break;
    case "mark_spam":
      control = <span className="text-[12px] text-fg-3">{ts("edit.markSpamHint")}</span>;
      break;
    case "delete_ticket":
      control = <span className="text-[12px] text-fg-3">{ts("edit.deleteTicketHint")}</span>;
      break;
    case "merge_into_open_ticket":
      control = <MergeControl a={a} onChange={onChange} />;
      break;
    case "set_field":
      control = <SetFieldControl a={a} invalid={invalid} onChange={onChange} />;
      break;
    case "ai_triage": {
      const n = cost("triage");
      control = (
        <span className="text-[12px] text-fg-3">
          {t("automation.ai_triage_hint")}
          {n !== null && <span className="mono ml-1.5 whitespace-nowrap text-[11px] text-fg-4">{t("automation.per_run", { cost: formatUsd(n, { precise: true }) })}</span>}
        </span>
      );
      break;
    }
  }

  return (
    <div className="px-4 py-2.5">
      <div className="flex flex-wrap items-center gap-2">
        <span className="w-9 shrink-0 text-[12px] text-fg-4">{ts("edit.do")}</span>
        <SinglePick
          items={actionItems}
          value={a.type}
          onChange={(v) => onChange(newAction(v as ActionType))}
          placeholder={ts("edit.action")}
          className="w-[190px]"
          width={240}
        />
        <div className="min-w-[180px] flex-1">{control}</div>
        <RemoveButton onClick={onRemove} label={ts("edit.removeAction")} />
      </div>
      {rich && (
        <div
          className={cn(
            "ml-11 mt-2 rounded-md border focus-within:border-fg-3 focus-within:shadow-[0_0_0_3px_var(--focus)]",
            invalid ? "border-danger" : "border-line",
            a.type === "add_note" && "bg-note",
          )}
        >
          {a.type === "send_reply" ? (
            <TemplateEditor kind="reply" framed={false} value={a.value} onChange={set} placeholder={ts("edit.replyPlaceholder")} minHeight={72} ariaLabel={ts("edit.replyText")} />
          ) : (
            <RichEditor value={a.value} onChange={set} placeholder={ts("edit.notePlaceholder")} contentClassName="min-h-[72px]" ariaLabel={ts("edit.noteText")} />
          )}
        </div>
      )}
      {a.type === "send_reply" && <p className="ml-11 mt-1.5 text-[11.5px] leading-4 text-fg-4">{ts("edit.replyVariablesHint")}</p>}
    </div>
  );
}

/** merge_into_open_ticket: how recent the ticket it joins must be, and whether it needs the same subject. */
function MergeControl({ a, onChange }: { a: Action; onChange: (a: Action) => void }) {
  const { t } = useTranslation("settingsInbox");
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex flex-wrap items-center gap-3">
        <Select
          size="sm"
          value={a.value}
          onChange={(value) => onChange({ ...a, value })}
          options={mergeWindows(a.value).map((n) => ({ value: String(n), label: t("edit.mergeWindow", { count: n }) }))}
          className="w-[170px]"
          aria-label={actionLabel.merge_into_open_ticket}
        />
        <label className="flex items-center gap-2 text-[12.5px] text-fg-2">
          <Switch checked={a.same_subject !== false} onCheckedChange={(same_subject) => onChange({ ...a, same_subject })} />
          {t("edit.mergeSameSubject")}
        </label>
      </div>
      <span className="text-[11.5px] text-fg-4">{t("edit.mergeHint")}</span>
    </div>
  );
}

/** set_field: which ticket field, then its value ("" clears it). */
function SetFieldControl({ a, invalid, onChange }: { a: Action; invalid: boolean; onChange: (a: Action) => void }) {
  const { t } = useTranslation("settingsInbox");
  const { all } = useAutomationFields();
  const ticketFields = all.filter((f) => f.entity === "ticket" && (!f.archived || f.key === a.field));
  const field = ticketFields.find((f) => f.key === a.field);
  return (
    <div className="flex flex-wrap items-center gap-2">
      <SinglePick
        items={ticketFields.map((f) => ({ value: f.key, label: f.name, icon: <span className="flex text-fg-3">{typeIcon[f.type]}</span>, keywords: [f.key] }))}
        value={a.field ?? null}
        onChange={(key) => onChange({ ...a, field: key, value: "" })}
        placeholder={t("customFields.chooseField")}
        invalid={invalid}
        width={260}
        className="w-[170px]"
      />
      {field && (
        <>
          <span className="text-[12px] text-fg-4">{t("customFields.to")}</span>
          <div className="min-w-[160px] flex-1">
            <FieldValueInput field={field} value={a.value} onChange={(value) => onChange({ ...a, value })} allowClear />
          </div>
        </>
      )}
    </div>
  );
}
