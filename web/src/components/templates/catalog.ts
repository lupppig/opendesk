// The template variable list (contracts/templates.md), from the server so the
// app never keeps its own copy, plus helpers to read tags in a template.

import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import type { TemplateGroup, TemplateKind, TemplateSlot, TemplateVariable } from "@/lib/api/types";
import { q } from "@/lib/queries";

export const GROUP_LABELS: Record<TemplateGroup, string> = {
  contact: "Customer",
  company: "Company",
  ticket: "Ticket",
  agent: "Sender",
  team: "Team",
  brand: "Brand",
  mailbox: "Address",
  workspace: "Workspace",
  date: "Date",
};

export const GROUP_ORDER: TemplateGroup[] = ["contact", "company", "ticket", "agent", "team", "brand", "mailbox", "workspace", "date"];

/** A tag as written: `{{ ... }}` or `{% ... %}` (a `<` only when it is not markup). */
export const TAG_RE = /\{\{(?:[^{}<]|<(?![a-zA-Z/!])){1,400}?\}\}|\{%(?:[^{}<]|<(?![a-zA-Z/!])){1,400}?%\}/g;

const BLOCK_WORDS = new Set(["if", "elsif", "unless", "case", "when", "for"]);
const PLAIN_WORDS = new Set(["else", "endif", "endunless", "endcase", "endfor", "endcapture", "endraw", "endcomment", "comment", "raw", "capture", "assign"]);

/** The variable a tag reads first, or null when it reads none. */
export function tagKey(tag: string): string | null {
  const inner = decode(tag.slice(2, -2)).trim();
  if (tag.startsWith("{%")) {
    const m = /^(?:if|elsif|unless|case|when)\s+(?:not\s+)?\(?\s*([a-z_][a-z0-9_.]*)/.exec(inner) ?? /^for\s+[a-z_][a-z0-9_]*\s+in\s+([a-z_][a-z0-9_.]*)/.exec(inner);
    return m && !/^(true|false|empty|blank|nil)$/.test(m[1]) ? m[1] : null;
  }
  const m = /^([a-z_][a-z0-9_]*(?:\.[a-z0-9_]+)*)/.exec(inner);
  return m ? m[1] : null;
}

const decode = (s: string) => s.replace(/&quot;/g, '"').replace(/&#39;/g, "'").replace(/&gt;/g, ">").replace(/&lt;/g, "<").replace(/&amp;/g, "&").replace(/&nbsp;|\u00a0/g, " ");

/** Names a template sets itself: loop names, assign and capture. */
export function localNames(text: string): Set<string> {
  const out = new Set<string>(["forloop"]);
  for (const m of text.matchAll(/\{%\s*(?:for\s+([a-z_][a-z0-9_]*)\s+in|assign\s+([a-z_][a-z0-9_]*)\s*=|capture\s+([a-z_][a-z0-9_]*)\s*%)/g)) {
    out.add(m[1] ?? m[2] ?? m[3]);
  }
  return out;
}

export type Catalog = {
  variables: TemplateVariable[];
  slots: TemplateSlot[];
  /** Every key a template of this kind may read: variables plus its slots. */
  known: Set<string>;
  byKey: Map<string, TemplateVariable>;
  /** The helpers, for "{{ x |" suggestions. */
  filters: { name: string; args: string; description: string }[];
  loading: boolean;
};

/** Slots each kind may use. */
export function slotsFor(kind: TemplateKind | "history_header"): string[] {
  if (kind === "layout") return ["content", "signature", "history"];
  if (kind === "csat") return ["survey"];
  return [];
}

export function useCatalog(kind: TemplateKind = "reply"): Catalog {
  const { data, isLoading } = useQuery(q.templateVariables());
  return useMemo(() => {
    // A kind that takes only some groups (a Collab signature: no ticket, customer, team or brand) offers only those.
    const groups = data?.kind_groups?.[kind];
    const variables = (data?.variables ?? []).filter((v) => !(kind === "personal_signature" && v.key === "agent.signature") && (!groups || groups.includes(v.group)));
    const slots = (data?.slots ?? []).filter((s) => slotsFor(kind).includes(s.key));
    const known = new Set<string>([...variables.map((v) => v.key), ...slots.map((s) => s.key)]);
    return { variables, slots, known, byKey: new Map(variables.map((v) => [v.key, v])), filters: data?.filters ?? [], loading: isLoading };
  }, [data, isLoading, kind]);
}

/** Whether a tag is fine as far as the client can tell (the server checks for real). */
export function tagState(tag: string, known: Set<string> | null, locals?: Set<string>): "var" | "logic" | "bad" {
  const ok = (key: string | null) => !key || !known || known.size === 0 || known.has(key) || !!locals?.has(key.split(".")[0]);
  if (tag.startsWith("{%")) {
    const word = decode(tag.slice(2, -2)).trim().split(/\s+/)[0];
    if (PLAIN_WORDS.has(word)) return "logic";
    if (!BLOCK_WORDS.has(word)) return "bad";
    return ok(tagKey(tag)) ? "logic" : "bad";
  }
  const inner = decode(tag.slice(2, -2)).trim();
  // A quoted text or a number with filters: {{ "x" | upcase }}.
  if (/^(["'\d(-])/.test(inner)) return "var";
  const key = tagKey(tag);
  if (!key) return "bad";
  return ok(key) ? "var" : "bad";
}

/** `{{ key }}` as inserted. */
export const tagFor = (key: string) => `{{ ${key} }}`;
