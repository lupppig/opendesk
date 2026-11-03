// Mock decisions (contracts/ai.md "Decisions"): what Jev would answer for a
// ticket, gated by the workspace's confidence threshold. Confidences are
// derived from the ticket id, so a demo is stable across reloads.

import type { AIDecisions, AISuggestions, Activity, Priority, Severity, Ticket } from "@/lib/api/types";
import type { WsData } from "./db";
import { publish } from "./gateway";
import { addActivity, applyPatch, publishTicketUpdated } from "./tickets";
import { nowIso } from "./util";

const rank: Record<Priority, number> = { low: 0, normal: 1, high: 2, urgent: 3 };
const sevRank = (s: Severity | null) => (s ? { sev4: 0, sev3: 1, sev2: 2, sev1: 3 }[s] : -1);

function unit(seed: string): number {
  let h = 2166136261;
  for (let i = 0; i < seed.length; i++) h = Math.imul(h ^ seed.charCodeAt(i), 16777619);
  return ((h >>> 0) % 10_000) / 10_000;
}

/** A stable confidence in [lo, hi) for one field of one ticket. */
export const confidenceOf = (t: Ticket, field: string, lo = 0.55, hi = 0.98) => Math.round((lo + unit(`${t.id}:${field}`) * (hi - lo)) * 100) / 100;

/** The severity Jev reads from the priority and the text. */
export function severityFor(priority: Priority, text: string): Severity {
  if (/(outage|down for everyone|all \d+ users|data loss|nobody can)/i.test(text)) return "sev1";
  return ({ urgent: "sev2", high: "sev3", normal: "sev4", low: "sev4" } as const)[priority];
}

type Proposal = { priority: Priority; severity: Severity; tags: string[]; team_id: string | null; sentiment: string; category: string; language: string };

/**
 * Applies a Jev triage run: decisions at or above the threshold (and allowed by
 * triage_fields, unless `force`) are written as Ollie, the rest become
 * suggestions. Returns the decisions and suggestions stored on the ticket.
 */
export function applyDecisions(
  ws: WsData,
  t: Ticket,
  p: Proposal,
  opts: { via: string; force?: boolean; publish?: boolean; at?: string; confidence?: Partial<Record<"priority" | "severity" | "label" | "team" | "sentiment", number>> } = { via: "auto_triage" },
) {
  const threshold = ws.routing.triageThreshold ?? 0.7;
  const allowed = new Set(opts.force ? ["priority", "severity", "labels", "team"] : ws.routing.triageFields);
  // A workspace without severity: triage neither sets nor suggests it (settings.severity_enabled).
  if (ws.workspace.settings.severity_enabled === false) allowed.delete("severity");
  const at = opts.at ?? nowIso();
  const conf = {
    priority: confidenceOf(t, "priority"),
    severity: confidenceOf(t, "severity", 0.5, 0.96),
    label: confidenceOf(t, "label"),
    team: confidenceOf(t, "team", 0.45, 0.95),
    sentiment: confidenceOf(t, "sentiment", 0.7, 0.99),
    ...opts.confidence,
  };
  const ok = (f: "priority" | "severity" | "labels" | "team", c: number) => allowed.has(f) && (opts.force || c >= threshold);
  const suggestions: AISuggestions = {};
  const sugg = (value: string | string[], c: number) => ({ value, confidence: c, probability: Math.min(0.99, c + 0.08), model: "jev", decided_at: at });

  const applyPriority = rank[p.priority] > rank[t.priority] && ok("priority", conf.priority);
  const applySeverity = sevRank(p.severity) > sevRank(t.severity) && ok("severity", conf.severity);
  const newTags = p.tags.filter((n) => !t.tags.some((x) => x.name.toLowerCase() === n.toLowerCase()));
  const applyTags = newTags.length > 0 && ok("labels", conf.label);
  const applyTeam = !!p.team_id && !t.team_id && ok("team", conf.team);

  if (rank[p.priority] > rank[t.priority] && !applyPriority && allowed.has("priority")) suggestions.priority = sugg(p.priority, conf.priority);
  if (sevRank(p.severity) > sevRank(t.severity) && !applySeverity && allowed.has("severity")) suggestions.severity = sugg(p.severity, conf.severity);
  if (newTags.length && !applyTags && allowed.has("labels")) suggestions.labels = sugg(newTags, conf.label);
  if (p.team_id && !t.team_id && !applyTeam && allowed.has("team")) suggestions.team = sugg(p.team_id, conf.team);

  const decisions: AIDecisions = {
    engine: "jev",
    model: "jev",
    model_version: "jev-1.13.0",
    threshold,
    decided_at: at,
    fields: {
      priority: { value: p.priority, probability: Math.min(0.99, conf.priority + 0.1), confidence: conf.priority, applied: applyPriority },
      severity: { value: p.severity, probability: Math.min(0.99, conf.severity + 0.1), confidence: conf.severity, applied: applySeverity },
      label: { value: p.tags[0] ?? null, probability: Math.min(0.99, conf.label + 0.1), confidence: conf.label, applied: applyTags },
      team: { value: p.team_id, probability: Math.min(0.99, conf.team + 0.1), confidence: conf.team, applied: applyTeam },
      sentiment: { value: p.sentiment, probability: Math.min(0.99, conf.sentiment + 0.05), confidence: conf.sentiment, applied: true },
      category: { value: p.category, probability: 0.81, confidence: 0.74, applied: true },
      language: { value: p.language, probability: 0.97, confidence: 0.96, applied: true },
      spam: { value: false, probability: 0.02, confidence: 0.96, applied: false },
      needs_human: { value: false, probability: 0.18, confidence: 0.64, applied: false },
      sla_risk: { value: Math.round(unit(`${t.id}:sla`) * 100) / 100, probability: 0.6, confidence: 0.5, applied: false },
    },
  };

  const before = ws.activities.length;
  const patch: Parameters<typeof applyPatch>[2] = {};
  if (applyPriority) patch.priority = p.priority;
  if (applySeverity) patch.severity = p.severity;
  if (applyTags) patch.add_tags = newTags;
  if (applyTeam) patch.team_id = p.team_id;
  const { changes } = applyPatch(ws, t, patch, null, false, { lenient: true, createLabels: false });
  // Attribute what was just written to Ollie, with how sure Jev was.
  const confFor: Record<string, number> = { priority_changed: conf.priority, severity_changed: conf.severity, tagged: conf.label, team_changed: conf.team };
  for (const a of ws.activities.slice(before) as Activity[]) {
    a.actor = { type: "ai", user: null };
    a.created_at = at;
    a.data = { ...a.data, via: opts.via, model: "jev", confidence: confFor[a.type] ?? null };
  }
  addActivity(ws, t, "ai_triaged", { category: p.category, sentiment: p.sentiment, language: p.language, priority: p.priority, severity: p.severity, via: opts.via, model: "jev", engine: "jev" }, null, at, false).actor = { type: "ai", user: null };
  t.custom = { ...t.custom, ai_decisions: decisions, ai_suggestions: suggestions, ai_sentiment: p.sentiment, language: p.language, ai_category: p.category, ai_triaged_at: at };
  if (opts.publish) {
    for (const a of ws.activities.slice(before)) publish(ws, "activity.created", { ticket_id: t.id, activity: a });
    publishTicketUpdated(ws, t, [...changes, "custom"]);
  }
  return { decisions, suggestions, changes };
}
