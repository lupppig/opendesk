import { useQuery } from "@tanstack/react-query";
import { X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { TextInput, Textarea } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Tooltip } from "@/components/ui/tooltip";
import {
  SinglePick,
  TagPick,
  channelItems,
  priorityItems,
  sentimentItems,
  severityFilterItems,
  statusItems,
  useMailboxItems,
  useMemberItems,
} from "@/components/settings2/pickers";
import {
  MAX_PHRASES,
  aiFields,
  brandFields,
  callDirections,
  callOutcomes,
  callReasons,
  callTimes,
  fieldLabel,
  isPhraseOp,
  offered,
  opLabel,
  opsFor,
  phrases,
  teamFields,
  validPhrases,
  validProbability,
  validQuestion,
} from "@/components/settings2/automation";
import { brandItems, teamItems, useBrands, useTeams } from "@/components/settings2/teams";
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
import { AiIcon } from "@/components/ai/bits";
import { formatUsd } from "@/lib/format";
import { useAI } from "@/lib/ai";
import { q } from "@/lib/queries";
import { cn } from "@/lib/cn";
import type { Condition, ConditionField, ConditionOp, FieldCondition, FieldOp } from "@/lib/api/types";

/** A builder rule: a built-in condition, or one on a custom field with its type's operators. */
export type Rule = Condition | FieldCondition;
export const isFieldRule = (c: Rule): c is FieldCondition => isFieldRef(c.field);

export const allFields = Object.keys(fieldLabel) as ConditionField[];

/** Whether a rule still needs its value (or, for ai_decide, its question). */
export const conditionInvalid = (c: Rule) =>
  isFieldRule(c)
    ? !fieldConditionComplete(c)
    : c.field === "ai_decide"
      ? !validProbability(c.value) || !validQuestion(c.question)
      : isPhraseOp(c.op)
        ? !validPhrases(c.value)
        : !c.value.trim();

/** The fields a builder offers: those allow admits, AI fields only when the instance has AI, team and brand
 *  fields once there are teams or brands to pick, and always the current one. */
function useFieldItems(current: string, allow: (f: ConditionField) => boolean) {
  const { enabled } = useAI();
  const { t } = useTranslation("settingsInbox");
  const custom = useFieldConditionItems(current);
  const { teams } = useTeams();
  const { multi } = useBrands();
  const builtIn = offered(allFields, aiFields, enabled, isFieldRef(current) ? undefined : (current as ConditionField))
    .filter(
      (f) =>
        f === current ||
        ((teams.length > 0 || !teamFields.has(f)) && (multi || !brandFields.has(f)) && allow(f)),
    )
    .map((f) => ({
    value: f as string,
    label: fieldLabel[f],
    icon: aiFields.has(f) ? <AiIcon /> : undefined,
    group: custom.length ? t("customFields.ticketBasics") : undefined,
  }));
  return [...builtIn, ...custom];
}

export function RemoveButton({ onClick, label }: { onClick: () => void; label: string }) {
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


export function ConditionRow({
  c,
  first,
  invalid,
  allow,
  onChange,
  onRemove,
}: {
  c: Rule;
  first: boolean;
  invalid: boolean;
  /** The built-in fields this builder offers. */
  allow: (f: ConditionField) => boolean;
  onChange: (c: Rule) => void;
  onRemove: () => void;
}) {
  const { t } = useTranslation("settingsInbox");
  const fieldItems = useFieldItems(c.field, allow);
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
    case "call_outcome":
    case "call_direction":
    case "call_time":
    case "time":
    case "call_missed_reason": {
      const values: readonly string[] = { call_outcome: callOutcomes, call_direction: callDirections, call_time: callTimes, time: callTimes, call_missed_reason: callReasons }[c.field];
      // time reads business hours as a call's time does, and shares its labels.
      const group = c.field === "time" ? "call_time" : c.field;
      return (
        <SinglePick
          items={values.map((v) => ({ value: v, label: ts(`callValues.${group}.${v}` as never) }))}
          value={c.value}
          onChange={onChange}
          placeholder={ts("edit.chooseValue")}
          invalid={invalid}
          width={280}
          className="w-full"
        />
      );
    }
    case "call_line":
      if (c.op === "equals" || c.op === "not_equals") return <LinePick value={c.value} invalid={invalid} onChange={onChange} />;
      return (
        <TextInput inputSize="sm" value={c.value} invalid={invalid} aria-label={ts("edit.valueLabel", { field: fieldLabel[c.field] })} placeholder="B2B" onChange={(e) => onChange(e.target.value)} />
      );
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

/** call_line equals: a provider number, kept by its id and shown by its name (an id no longer listed shows as is). */
function LinePick({ value, invalid, onChange }: { value: string; invalid: boolean; onChange: (v: string) => void }) {
  const { t } = useTranslation("settingsInbox");
  const { data: lines } = useQuery(q.phoneLines());
  const items = (lines ?? []).map((l) => ({ value: l.id, label: l.name }));
  if (value && !items.some((i) => i.value === value)) items.push({ value, label: value });
  return (
    <SinglePick
      items={items}
      value={value}
      onChange={onChange}
      placeholder={t("edit.chooseLine")}
      invalid={invalid}
      width={280}
      className="w-full"
    />
  );
}
