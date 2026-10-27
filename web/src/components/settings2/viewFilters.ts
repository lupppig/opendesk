// The view editor's filter rows that take an operator: label (any of, none of, empty),
// team, mailbox and channel (is, is not). Each operator writes its own key of
// TicketFilters (contracts/api.md): tag / tag_not / untagged, team_id / team_not,
// mailbox_id / mailbox_not, channel / channel_not.

import type { Channel, TicketFilters } from "@/lib/api/types";

export type FilterOp = "is" | "not" | "empty";
export type OpField = "tag" | "team" | "mailbox" | "channel";

/** The operators each row offers, in order. */
export const fieldOps: Record<OpField, FilterOp[]> = {
  tag: ["is", "not", "empty"],
  team: ["is", "not"],
  mailbox: ["is", "not"],
  channel: ["is", "not"],
};

/** A comma-separated list (label names, team or mailbox ids) as an array. */
export const splitList = (s: string | undefined): string[] =>
  (s ?? "")
    .split(",")
    .map((x) => x.trim())
    .filter(Boolean);

/** A list back to its comma-separated form, or undefined when empty. */
export const joinList = (xs: string[]): string | undefined => (xs.length ? xs.join(",") : undefined);

/** The operator a row shows for f: the one whose key is set, "is" when none is. */
export function filterOp(f: TicketFilters, field: OpField): FilterOp {
  switch (field) {
    case "tag":
      return f.untagged ? "empty" : f.tag_not && !f.tag ? "not" : "is";
    case "team":
      return f.team_not && !f.team_id ? "not" : "is";
    case "mailbox":
      return f.mailbox_not && !f.mailbox_id ? "not" : "is";
    case "channel":
      return f.channel_not?.length && !f.channel ? "not" : "is";
  }
}

/** Whether the row has a value under any of its operators. */
export function hasFilter(f: TicketFilters, field: OpField): boolean {
  switch (field) {
    case "tag":
      return !!(f.tag || f.tag_not || f.untagged);
    case "team":
      return !!(f.team_id || f.team_not);
    case "mailbox":
      return !!(f.mailbox_id || f.mailbox_not);
    case "channel":
      return !!(f.channel || f.channel_not?.length);
  }
}

/** f with the row cleared under every operator. */
export function clearField(f: TicketFilters, field: OpField): TicketFilters {
  switch (field) {
    case "tag":
      return { ...f, tag: undefined, tag_not: undefined, untagged: undefined };
    case "team":
      return { ...f, team_id: undefined, team_not: undefined };
    case "mailbox":
      return { ...f, mailbox_id: undefined, mailbox_not: undefined };
    case "channel":
      return { ...f, channel: undefined, channel_not: undefined };
  }
}

/** f with the row switched to op, carrying the chosen values over where the new operator can hold them. */
export function withOp(f: TicketFilters, field: OpField, op: FilterOp): TicketFilters {
  const out = clearField(f, field);
  switch (field) {
    case "tag": {
      const names = joinList([...splitList(f.tag), ...splitList(f.tag_not)]);
      if (op === "empty") return { ...out, untagged: true };
      return op === "not" ? { ...out, tag_not: names } : { ...out, tag: names };
    }
    case "team": {
      const teams = joinList([...splitList(f.team_id), ...splitList(f.team_not)]);
      return op === "not" ? { ...out, team_not: teams } : { ...out, team_id: teams };
    }
    case "mailbox": {
      const boxes = [...splitList(f.mailbox_id), ...splitList(f.mailbox_not)];
      return op === "not" ? { ...out, mailbox_not: joinList(boxes) } : { ...out, mailbox_id: boxes[0] };
    }
    case "channel": {
      const channels = [...(f.channel ? [f.channel] : []), ...(f.channel_not ?? [])];
      return op === "not" ? { ...out, channel_not: channels.length ? [...new Set(channels)] : undefined } : { ...out, channel: channels[0] as Channel | undefined };
    }
  }
}

/** The filters a view is saved with: only the keys that are set. */
export function cleanFilters(f: TicketFilters): TicketFilters {
  const out: TicketFilters = {};
  if (f.status?.length) out.status = f.status;
  if (f.priority?.length) out.priority = f.priority;
  if (f.severity?.length) out.severity = f.severity;
  if (f.assignee) out.assignee = f.assignee;
  if (f.team_id) out.team_id = f.team_id;
  if (f.team_not) out.team_not = f.team_not;
  if (f.mailbox_id) out.mailbox_id = f.mailbox_id;
  if (f.mailbox_not) out.mailbox_not = f.mailbox_not;
  if (f.contact_not) out.contact_not = f.contact_not;
  if (f.company_not) out.company_not = f.company_not;
  if (f.received_at) out.received_at = f.received_at;
  if (f.tag) out.tag = f.tag;
  if (f.tag_not) out.tag_not = f.tag_not;
  if (f.untagged) out.untagged = true;
  if (f.channel) out.channel = f.channel;
  if (f.channel_not?.length) out.channel_not = f.channel_not;
  if (f.q?.trim()) out.q = f.q.trim();
  return out;
}
