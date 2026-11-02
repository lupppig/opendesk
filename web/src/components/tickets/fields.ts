// Labels (with groups) and custom fields (contracts/api.md "Labels and custom
// fields"): small hooks over the shared queries, the workspace display
// overrides, and helpers for values and the 422 errors these writes answer.

import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { create } from "zustand";
import { i18n } from "@/i18n";
import { isApiError } from "@/lib/api/client";
import type { CustomField, FieldValue, Priority, Tag, TagGroup, Ticket, TicketPatch, TicketStatus } from "@/lib/api/types";
import { q } from "@/lib/queries";
import { isTone, type Tone } from "@/lib/tones";
import { priorityLabel, statusLabel, statusOrder } from "./bits";

export type { CustomField, FieldOption, FieldType, FieldValue, FieldValues } from "@/lib/api/types";
export type LabelRef = Tag;
export type LabelGroup = TagGroup;
export type TicketWithFields = Ticket;
export type FieldPatch = TicketPatch;

export function useLabelGroups() {
  return useQuery(q.tagGroups());
}

export function useTicketFields() {
  return useQuery({
    ...q.customFields(),
    select: (d: CustomField[]) => d.filter((f) => !f.archived && f.entity === "ticket").sort((a, b) => a.position - b.position),
  });
}

/** An agent named a label that doesn't exist while only admins may create labels. */
export const isUnknownLabel = (e: unknown) => isApiError(e, "unknown_label");

type Override = { label?: string; color?: string; hidden?: boolean };
type DisplaySettings = {
  label_creation?: "anyone" | "admins";
  status_labels?: Record<string, Override>;
  priority_labels?: Record<string, Override>;
  send_and_finish_button?: boolean;
  severity_enabled?: boolean;
};

/** The workspace's display overrides for status and priority names and colours, and who may create labels. */
export function useDisplay() {
  useTranslation("indicators");
  const { data } = useQuery(q.workspace());
  const s = (data?.settings ?? {}) as DisplaySettings;
  return {
    labelCreation: s.label_creation ?? "anyone",
    priorityTone: (v: Priority): Tone | undefined => (isTone(s.priority_labels?.[v]?.color) ? (s.priority_labels![v]!.color as Tone) : undefined),
    status: (v: TicketStatus) => s.status_labels?.[v]?.label || statusLabel(v),
    priority: (v: Priority) => s.priority_labels?.[v]?.label || priorityLabel(v),
    statusTone: (v: TicketStatus): Tone | undefined => (isTone(s.status_labels?.[v]?.color) ? (s.status_labels![v]!.color as Tone) : undefined),
    /** Whether the workspace hides the status from the agents' menus. */
    hidden: (v: TicketStatus) => v !== "solved" && !!s.status_labels?.[v]?.hidden,
    /** The statuses an agent may pick, keeping the ticket's current one. */
    statuses: (current?: TicketStatus) => offeredStatuses(s.status_labels, current),
    /** Finishing preserves surveys and reopening, regardless of its display label. */
    finish: "solved" as const,
    /** The words for that action wherever it is offered. */
    finishWords: finishWords(s.status_labels),
    /** Whether the composer shows sending and finishing on a button of its own, next to Send. */
    finishButton: !!s.send_and_finish_button,
    /** Whether the workspace uses ticket severity; off, the app leaves it out (settings.severity_enabled). */
    severity: s.severity_enabled !== false,
  };
}

/**
 * How the UI names finishing a ticket, using the presentation label of solved.
 */
export type FinishVerb = "solve" | "close" | "mark";

export function finishVerb(labels: Record<string, Override> | undefined): FinishVerb {
  const name = labels?.solved?.label?.trim().toLowerCase();
  const solvedNames = [statusLabel("solved"), i18n.t("indicators:status.solved", { lng: "en" })].map((n) => n.toLowerCase());
  if (!name || solvedNames.includes(name)) return "solve";
  // A label that only spells the built-in name, in the agent's language or English, is no rename.
  const builtIn = [statusLabel("closed"), i18n.t("indicators:status.closed", { lng: "en" })].map((n) => n.toLowerCase());
  return builtIn.includes(name) ? "close" : "mark";
}

/** The finishing action's words at each place it shows, after finishVerb; solving keeps its own. */
export function finishWords(labels: Record<string, Override> | undefined) {
  const verb = finishVerb(labels);
  const status = labels?.solved?.label?.trim() ?? "";
  const say = (solve: () => string, close: () => string, mark: () => string) => (verb === "solve" ? solve : verb === "close" ? close : mark)();
  return {
    verb,
    /** The ticket header's button. */
    button: () => say(() => i18n.t("inbox:detail.solve"), () => i18n.t("inbox:finish.close.button"), () => i18n.t("inbox:finish.mark.button", { status })),
    /** The command menu, and the header button's tooltip once it no longer says solve. */
    command: () => say(() => i18n.t("inbox:commands.solve"), () => i18n.t("inbox:finish.close.command"), () => i18n.t("inbox:finish.mark.command", { status })),
    toast: (number: number) => say(() => i18n.t("inbox:toast.solved", { number }), () => i18n.t("inbox:finish.close.toast", { number }), () => i18n.t("inbox:finish.mark.toast", { number, status })),
    /** A list row's hover action. */
    quick: (number: number) => say(() => i18n.t("inbox:quick.solve", { number }), () => i18n.t("inbox:finish.close.quick", { number }), () => i18n.t("inbox:finish.mark.quick", { number, status })),
    /** Empty list tips: finish the focused ticket, and send and finish. */
    tip: () => say(() => i18n.t("inbox:empty.tips.solve"), () => i18n.t("inbox:finish.close.tip"), () => i18n.t("inbox:finish.mark.tip", { status })),
    sendTip: () => say(() => i18n.t("inbox:empty.tips.sendSolve"), () => i18n.t("inbox:finish.close.sendTip"), () => i18n.t("inbox:finish.mark.sendTip", { status })),
    /** The keyboard shortcuts sheet, in the ticket list and in a ticket. */
    listShortcut: () =>
      say(() => i18n.t("navigation:shortcuts.solveFocusedTicket"), () => i18n.t("navigation:shortcuts.closeFocusedTicket"), () => i18n.t("navigation:shortcuts.markFocusedTicket", { status })),
    shortcut: () => say(() => i18n.t("navigation:shortcuts.solve"), () => i18n.t("navigation:shortcuts.closeTicket"), () => i18n.t("navigation:shortcuts.markTicket", { status })),
  };
}

/** The statuses in menu order without the ones the workspace hides; the ticket's current status stays. */
export function offeredStatuses(labels: Record<string, Override> | undefined, current?: TicketStatus): TicketStatus[] {
  return statusOrder.filter((s) => s === "solved" || s === current || !labels?.[s]?.hidden);
}

export function hasValue(f: CustomField, v: FieldValue | undefined | null): boolean {
  if (v === undefined || v === null || v === "") return false;
  if (Array.isArray(v)) return v.length > 0;
  if (f.type === "checkbox") return v === true;
  return true;
}

/** A value as text, for the timeline and filter chips. */
export function fieldText(f: CustomField | undefined, v: unknown, memberName?: (id: string) => string | undefined): string {
  if (v === undefined || v === null || v === "") return "";
  if (!f) return Array.isArray(v) ? v.join(", ") : String(v);
  const opt = (id: string) => f.options.find((o) => o.id === id)?.label ?? id;
  if (f.type === "select") return opt(String(v));
  if (f.type === "multi_select") return (Array.isArray(v) ? v : String(v).split(",")).map((x) => opt(String(x))).join(", ");
  if (f.type === "checkbox") return v === true || v === "true" ? "Yes" : "No";
  if (f.type === "user") return memberName?.(String(v)) ?? String(v);
  return String(v);
}

/** The keys a 422 field_required names (errors[].location = body.fields.<key>). */
export function requiredFieldKeys(e: unknown): string[] {
  if (!isApiError(e, "field_required")) return [];
  return (e.errors ?? []).map((x) => x.location?.match(/^body\.fields\.(.+)$/)?.[1]).filter((k): k is string => !!k);
}

/** Asks the ticket sidebar to open a field's editor (after a refused solve). */
export const useFieldFocus = create<{ key: string | null; at: number }>(() => ({ key: null, at: 0 }));
export const focusField = (key: string) => useFieldFocus.setState({ key, at: Date.now() });

/** Required-to-solve fields this ticket leaves empty, checked before a solve. */
export function missingForSolve(fields: CustomField[] | undefined, t: Ticket): CustomField[] {
  return (fields ?? []).filter((f) => f.required_on_solve && !hasValue(f, t.fields?.[f.key]));
}
