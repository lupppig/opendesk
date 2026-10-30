import { i18n } from "@/i18n";
import type { Action, ActionType, AutomationTrigger, ConditionField, ConditionOp, Sentiment } from "@/lib/api/types";
import type { Tone } from "@/lib/tones";

const tr = i18n.t as unknown as (key: string) => string;

/** A label table read from the settingsInbox catalog on access, so it follows the loaded language. */
function labels<K extends string>(keys: readonly K[], group: string): Record<K, string> {
  const out = {} as Record<K, string>;
  for (const k of keys)
    Object.defineProperty(out, k, {
      get: () => tr(`settingsInbox:${group}.${k}`),
      enumerable: true,
    });
  return out;
}

const triggers = ["ticket_created", "customer_replied", "agent_replied", "ticket_updated", "call_ended"] as const satisfies readonly AutomationTrigger[];

export const triggerLabel: Record<AutomationTrigger, string> = labels(triggers, "triggers");
export const triggerShort: Record<AutomationTrigger, string> = labels(triggers, "triggersShort");

/** Each trigger keeps one hue across the list and the builder. */
export const triggerTone: Record<AutomationTrigger, Tone> = {
  ticket_created: "blue",
  customer_replied: "teal",
  agent_replied: "indigo",
  ticket_updated: "amber",
  call_ended: "green",
};

const plainFields = ["subject", "body", "from_email", "channel", "mailbox_id", "received_at", "assignee_id", "team_id", "brand_id", "widget_id", "priority", "severity", "status", "tag",
  "call_outcome", "call_direction", "call_time", "call_missed_reason", "call_line", "call_tag", "time"] as const;

/** The call of a call_ended automation (contracts/phone.md "Automations"): only that trigger offers them. */
export const callFields: ReadonlySet<ConditionField> = new Set(["call_outcome", "call_direction", "call_time", "call_missed_reason", "call_line", "call_tag"]);
export const callOutcomes = ["missed", "voicemail", "answered"] as const;
export const callDirections = ["inbound", "outbound"] as const;
export const callTimes = ["outside_business_hours", "business_hours"] as const;
/** Aircall's missed_call_reason values. */
export const callReasons = ["out_of_opening_hours", "no_available_agent", "agents_did_not_answer", "abandoned_in_ivr", "abandoned_in_classic", "short_abandoned"] as const;

/** Fields that only make sense once the workspace has teams, or a second brand (contracts/teams.md). */
export const teamFields: ReadonlySet<ConditionField> = new Set(["team_id"]);
export const brandFields: ReadonlySet<ConditionField> = new Set(["brand_id"]);
export const fieldLabel: Record<ConditionField, string> = Object.defineProperties(labels(plainFields, "fields"), {
  sentiment: { get: () => i18n.t("ai:automation.sentiment"), enumerable: true },
  ai_decide: { get: () => i18n.t("ai:automation.ai_decide"), enumerable: true },
}) as Record<ConditionField, string>;

/** Fields and actions that need the instance's AI (contracts/ai.md). */
export const aiFields: ReadonlySet<ConditionField> = new Set(["sentiment", "ai_decide"]);

/** ai_decide: a probability between 0.01 and 0.99 ("0.7"). */
export function validProbability(v: string): boolean {
  const n = Number(v);
  return v.trim() !== "" && Number.isFinite(n) && n >= 0.01 && n <= 0.99;
}
/** ai_decide: a yes/no question of 3..300 characters. */
export function validQuestion(q: string | undefined): boolean {
  const n = (q ?? "").trim().length;
  return n >= 3 && n <= 300;
}
export const aiActions: ReadonlySet<ActionType> = new Set(["ai_triage"]);

export const sentiments: Sentiment[] = ["positive", "neutral", "negative", "frustrated"];
export function sentimentLabel(s: string): string {
  return (sentiments as string[]).includes(s) ? i18n.t(`ai:sentiment.${s as Sentiment}`) : s;
}

export const opLabel: Record<ConditionOp, string> = labels(["contains", "not_contains", "equals", "not_equals", "eq", "gt", "lt", "any", "none"] as const, "ops");

/** Text fields take any operator; enumerated fields only is / is not. */
export function opsFor(field: ConditionField): ConditionOp[] {
  if (field === "sentiment") return ["equals", "not_equals"]; // the API stores "eq" as "equals"
  if (field === "ai_decide") return ["gt", "lt"];
  if (field === "received_at") return ["equals", "not_equals", "contains", "any", "none"];
  if (field === "call_line" || field === "call_tag") return ["equals", "not_equals", "contains", "not_contains"];
  return field === "subject" || field === "body" || field === "from_email" ? ["contains", "not_contains", "any", "none", "equals", "not_equals"] : ["equals", "not_equals"];
}

/** The bounds of an any or none phrase list (contracts/api.md). */
export const MAX_PHRASES = 100;
const MAX_PHRASE = 500;
const MAX_PHRASE_LIST = 5000;

/** any and none hold phrases, one per line: the text contains any, or none, of them. */
export function isPhraseOp(op: string): boolean {
  return op === "any" || op === "none";
}
/** The phrases of an any or none value: one per line, trimmed, empty lines dropped. */
export function phrases(value: string): string[] {
  return value
    .split("\n")
    .map((p) => p.trim())
    .filter(Boolean);
}
/** A phrase list the API takes: 1 to 100 phrases of up to 500 characters, 5000 in all. */
export function validPhrases(value: string): boolean {
  const list = phrases(value);
  return list.length > 0 && list.length <= MAX_PHRASES && list.every((p) => [...p].length <= MAX_PHRASE) && [...list.join("\n")].length <= MAX_PHRASE_LIST;
}

const plainActions = ["set_status", "set_priority", "set_severity", "assign_user", "assign_replier", "assign_team", "set_reply_address", "add_tag", "remove_tag", "set_field", "send_reply", "add_note", "mark_spam", "delete_ticket", "merge_into_open_ticket"] as const;
export const actionLabel: Record<ActionType, string> = Object.defineProperty(labels(plainActions, "actionTypes"), "ai_triage", {
  get: () => i18n.t("ai:automation.ai_triage"),
  enumerable: true,
}) as Record<ActionType, string>;

/** Whether the action's `value` must be filled. set_field may write "" to clear, so it needs only its field. */
export function actionNeedsValue(t: ActionType) {
  return t !== "mark_spam" && t !== "delete_ticket" && t !== "assign_replier" && t !== "ai_triage" && t !== "set_field";
}

/** A new action of a type, with its defaults. */
export function newAction(type: ActionType): Action {
  return type === "merge_into_open_ticket" ? { type, value: String(MERGE_WINDOW_DEFAULT), same_subject: true } : { type, value: "" };
}

/** Actions that run on one trigger only: a merge joins a new ticket to an earlier one. */
const onlyOn: Partial<Record<ActionType, AutomationTrigger>> = { merge_into_open_ticket: "ticket_created" };
/** Whether an action can run on a trigger (contracts/api.md). */
export function actionFitsTrigger(type: ActionType, trigger: AutomationTrigger): boolean {
  const only = onlyOn[type];
  return !only || only === trigger;
}

/** merge_into_open_ticket: how recent the ticket it joins must be, in days (contracts/api.md). */
export const MERGE_WINDOW_DEFAULT = 7;
const MERGE_WINDOW_MAX = 30;
const mergeWindowChoices = [1, 2, 3, 7, 14, 30];
/** A window the API takes: a whole number of days from 1 to 30. */
export function validMergeWindow(v: string): boolean {
  const n = Number(v);
  return /^\d+$/.test(v) && n >= 1 && n <= MERGE_WINDOW_MAX;
}
/** The windows the editor offers, with one saved through the API outside them. */
export function mergeWindows(current: string): number[] {
  const n = Number(current);
  return validMergeWindow(current) && !mergeWindowChoices.includes(n) ? [...mergeWindowChoices, n].sort((a, b) => a - b) : mergeWindowChoices;
}

/** Picker items for a builder list: AI entries only when AI is on, but an entry already in use always shows. */
export function offered<K extends string>(keys: K[], ai: ReadonlySet<K>, aiEnabled: boolean, current?: K): K[] {
  return keys.filter((k) => aiEnabled || !ai.has(k) || k === current);
}
