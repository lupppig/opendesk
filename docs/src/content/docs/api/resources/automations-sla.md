---
title: Automations and SLA
description: Manage automation rules and SLA policies through the API.
---

Both are settings: they need an `admin` user, or an API key with `settings:read` and `settings:write`.

| Method | Path | Description |
| --- | --- | --- |
| `GET`, `POST` | `/v1/automations` | List or create automations |
| `GET`, `PATCH`, `DELETE` | `/v1/automations/{id}` | |
| `GET`, `POST` | `/v1/sla-policies` | List or create SLA policies |
| `GET`, `PATCH`, `DELETE` | `/v1/sla-policies/{id}` | |

## Objects

```ts
type Automation = {
  id: string;
  name: string;
  trigger: "ticket_created" | "customer_replied" | "agent_replied" | "ticket_updated";
  conditions: { field: "subject" | "body" | "from_email" | "channel" | "mailbox_id" | "priority" | "severity" | "status" | "tag"
                     | "sentiment" | "ai_decide" | `field.${string}` | `contact.field.${string}`;
                op: "contains" | "not_contains" | "equals" | "not_equals" | "any" | "none" | FieldOp;
                value: string /* up to 500 characters; any / none: phrases, one per line */;
                question?: string /* ai_decide only: 3..300 characters of plain text */ }[];
  actions: { type: "set_status" | "set_priority" | "set_severity" | "assign_user" | "assign_team" | "add_tag" | "remove_tag"
                 | "send_reply" | "add_note" | "mark_spam" | "delete_ticket" | "merge_into_open_ticket" | "ai_triage" | "set_field";
                 value: string;
                 field?: string /* set_field: the ticket field key */;
                 same_subject?: boolean /* merge_into_open_ticket: default true */ }[];
  enabled: boolean;
  position: number;
  run_count: number;
};
// any / none on subject, body, from_email, received_at and tag: the text contains any, or none, of the
// phrases in value, one per line (1..100 phrases of up to 500 characters, 5000 in all; case-insensitive).
// severity: equals / not_equals with sev1..sev4 or "none". set_severity: sev1..sev4, or "" to clear.
// ai_decide: gt / lt with value a probability 0.01..0.99; at most 3 per automation; all of one
// trigger run are asked in one call (charged once to the AI balance). It does not hold when no decision engine answers.

type SlaPolicy = {
  id: string;
  name: string;
  priority: Priority | null;
  first_response_minutes: number;
  resolution_minutes: number;
  enabled: boolean;
  position: number;
};
```

Conditions on `field.<key>` (the ticket's custom field) and `contact.field.<key>` (its contact's) take the [operators of the field's type](/api/resources/labels-fields/#filter-and-sort); `equals` and `not_equals` mean `eq` and `ne`. `set_field` writes `value` as a string: `"12.5"`, `"true"`, `"2026-10-11"`, comma separated option ids, or `""` to clear. It is checked when the automation is saved, and skipped at run time if the field was archived or deleted since. `add_tag` respects exclusive label groups.

`merge_into_open_ticket` runs on `ticket_created` only (another trigger is `422`). Its `value` is a window in days, `"1"` to `"30"` (`""` stores `"7"`), and `same_subject` (stored, `true` by default) requires the same subject once reply prefixes and ticket tags are dropped. It merges the new ticket into the contact's most recent earlier open, pending or on hold ticket as [`POST /v1/tickets/{id}/merge`](/api/resources/tickets/) does, by actor `automation`; see [Merge follow-ups](/guides/automations/#merge-follow-ups) for which tickets qualify.

See [Automations](/guides/automations/) and [SLA policies](/guides/sla-policies/) for how they behave.

## Create an automation

```sh
curl https://api.opendesk.sh/v1/automations \
  -H "Authorization: Bearer od_live_..." \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Outages are urgent",
    "trigger": "ticket_created",
    "conditions": [{ "field": "subject", "op": "contains", "value": "outage" }],
    "actions": [{ "type": "set_priority", "value": "urgent" }, { "type": "add_tag", "value": "incident" }],
    "enabled": true
  }'
```

## Create an SLA policy

```sh
curl https://api.opendesk.sh/v1/sla-policies \
  -H "Authorization: Bearer od_live_..." \
  -H "Content-Type: application/json" \
  -d '{ "name": "Urgent", "priority": "urgent", "first_response_minutes": 30, "resolution_minutes": 240, "enabled": true }'
```

`priority: null` applies the policy to any priority.

## Events

`automation.*` and `sla_policy.*`.
