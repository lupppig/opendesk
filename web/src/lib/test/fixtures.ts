import { setWorkspaceId } from "../api/client";
import type { Envelope, Ticket } from "../api/types";

/** The focus workspace of these fixtures: env() events belong to it, so qk keys built in a test read theirs. */
export const FOCUS = "w1";
setWorkspaceId(FOCUS);

export function ticket(over: Partial<Ticket> = {}): Ticket {
  return {
    id: "t1",
    number: 1,
    subject: "Hello",
    status: "open",
    priority: "normal",
    severity: null,
    channel: "email",
    mailbox_id: null,
    contact: null,
    assignee: null,
    team_id: null,
    brand_id: "b1",
    widget_id: null,
    origin_mailbox_id: null,
    received_at: "",
    page_url: "",
    routed: null,
    tags: [],
    fields: {},
    sla: null,
    first_responded_at: null,
    solved_at: null,
    snoozed_until: null,
    last_message_at: "2026-10-01T10:00:00Z",
    last_customer_message_at: null,
    waiting_since: null,
    unanswered_count: 0,
    last_message_preview: "",
    message_count: 1,
    spam: false,
    csat: null,
    custom: {},
    created_at: "2026-10-01T10:00:00Z",
    updated_at: "2026-10-01T10:00:00Z",
    ...over,
  };
}

export function env(seq: number, over: Partial<Envelope> = {}): Envelope {
  return {
    id: `evt_${seq}`,
    seq,
    workspace_id: "w1",
    type: "ticket.updated",
    visibility: "agents",
    topics: ["workspace:w1"],
    data: {},
    occurred_at: "2026-10-01T10:00:00Z",
    ...over,
  };
}
