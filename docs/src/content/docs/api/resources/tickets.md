---
title: Tickets
description: List, create, update, merge and bulk edit tickets.
---

Scopes: `tickets:read` to read, `tickets:write` to change.

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/tickets` | List tickets |
| `GET` | `/v1/tickets/counts` | Counts for the sidebar |
| `POST` | `/v1/tickets` | Create a ticket |
| `GET` | `/v1/tickets/{id}` | Get a ticket |
| `PATCH` | `/v1/tickets/{id}` | Update a ticket |
| `DELETE` | `/v1/tickets/{id}` | Delete a ticket (soft delete) |
| `POST` | `/v1/tickets/bulk` | Update or delete many tickets |
| `POST` | `/v1/tickets/{id}/merge` | Merge into another ticket |
| `GET` | `/v1/tickets/{id}/activities` | The ticket's activity, ascending |

Messages are on their own page: [Messages and attachments](/api/resources/messages/).

## The ticket object

```ts
type Ticket = {
  id: string;
  number: number;
  subject: string;
  status: "open" | "pending" | "on_hold" | "solved" | "closed";
  priority: "low" | "normal" | "high" | "urgent";
  severity: "sev1" | "sev2" | "sev3" | "sev4" | null; // business impact, sev1 the most severe
  channel: "email" | "chat" | "api" | "portal";
  mailbox_id: string | null;
  contact: ContactRef | null;
  assignee: UserRef | null;
  team_id: string | null;
  tags: { id: string; name: string; color: LabelColor; group_id: string | null }[];
  sla: { policy_id: string; first_response_due_at: string | null; resolution_due_at: string | null; breached: boolean } | null;
  first_responded_at: string | null;
  solved_at: string | null;
  snoozed_until: string | null;
  last_message_at: string;
  last_customer_message_at: string | null;
  last_message_preview: string;
  message_count: number;
  spam: boolean;
  csat: { score: number; comment: string } | null;
  custom: Record<string, unknown>;
  fields: Record<string, FieldValue>; // custom fields, by key
  created_at: string;
  updated_at: string;
};
```

## List tickets

```sh
curl "https://api.opendesk.sh/v1/tickets?status=open,pending&assignee=none&sort=due&order=asc" \
  -H "Authorization: Bearer od_live_..."
```

| Filter | Values |
| --- | --- |
| `status` | comma-separated statuses |
| `priority` | comma-separated priorities |
| `severity` | comma-separated `sev1` to `sev4`, and `none` for tickets without one |
| `assignee` | `me`, `none` or a user id |
| `team_id`, `mailbox_id`, `contact_id`, `view_id` | ids |
| `tag` | a label name |
| `field.<key>`, `field.<key>[<op>]` | a [custom field filter](/api/resources/labels-fields/#filter-and-sort) |
| `channel` | `email`, `chat`, `api`, `portal` |
| `q` | text search |
| `spam` | `true` or `false` |
| `sort` | `last_message_at`, `created_at`, `priority`, `severity` (sev1 first, none last), `due`, `field.<key>` |
| `order` | `asc` or `desc` |

Plus [`limit` and `cursor`](/api/pagination/).

## Counts

```sh
curl https://api.opendesk.sh/v1/tickets/counts -H "Authorization: Bearer od_live_..."
```

```json
{ "unassigned": 4, "mine": 7, "open": 23, "pending": 9, "on_hold": 2, "solved": 310, "spam": 1, "views": { "<view_id>": 5 } }
```

## Create a ticket

```sh
curl https://api.opendesk.sh/v1/tickets \
  -H "Authorization: Bearer od_live_..." \
  -H "Idempotency-Key: $(uuidgen)" \
  -H "Content-Type: application/json" \
  -d '{
    "subject": "Refund for order 1234",
    "contact": { "email": "ana@example.com", "name": "Ana" },
    "channel": "email",
    "mailbox_id": "0192f0c4-...",
    "body_html": "<p>Hi Ana, your refund is on its way.</p>",
    "kind": "reply",
    "priority": "high",
    "tags": ["billing"],
    "status": "pending"
  }'
```

| Field | Notes |
| --- | --- |
| `subject` | required |
| `contact_id` or `contact` | an existing contact, or `{ email, name }` to find or create one |
| `channel` | `email` or `api` |
| `mailbox_id` | the mailbox to send from, for email tickets |
| `body_html` | the first message |
| `kind` | `reply` or `note` |
| `priority`, `severity`, `assignee_id`, `team_id`, `tags`, `fields`, `status` | optional |

An `email` ticket created with a `reply` sends that reply to the contact from the mailbox. When that mailbox has no way to send (`sending: "system_off"`, see [Mailboxes](/api/resources/mailboxes/#sending-through-opendesk)), the request is `422 mailbox_cannot_send` and no ticket is created.

## Get a ticket

`id` can be the UUID, the number, or `#` and the number (URL-encoded as `%23`).

```sh
curl https://api.opendesk.sh/v1/tickets/1042 -H "Authorization: Bearer od_live_..."
```

## Update a ticket

```sh
curl -X PATCH https://api.opendesk.sh/v1/tickets/1042 \
  -H "Authorization: Bearer od_live_..." \
  -H "Content-Type: application/json" \
  -d '{ "status": "solved", "tags": ["billing", "refund"] }'
```

Fields: `subject`, `status`, `priority`, `severity` (`null` clears), `contact_id` (a contact in this workspace), `assignee_id`, `team_id`, `snoozed_until`, `spam`, `tags` (replaces the set), `add_tags`, `remove_tags`, `fields` (merged, `null` clears), `custom`. Changing `contact_id` changes the recipient of future replies and records `contact_changed`; past messages stay as sent. A live chat stays with the visitor who started it, so changing its contact answers `422`, and a bulk update cannot change contacts. The update publishes `ticket.updated` with `contact_id` in `changes`. See [Labels and custom fields](/api/resources/labels-fields/) for the label rules and value types. Solving a ticket while a required field is empty answers `422 field_required`. Changing `priority`, `severity`, `team_id`, `spam` or labels removes Ollie's pending suggestion for that field (see [Decisions](/api/resources/ai/#decisions)).

## Bulk update

```sh
curl https://api.opendesk.sh/v1/tickets/bulk \
  -H "Authorization: Bearer od_live_..." \
  -H "Content-Type: application/json" \
  -d '{ "ids": ["...", "..."], "patch": { "status": "closed" } }'
```

Send `{ "ids": [...], "delete": true }` to delete instead.

## Merge

```sh
curl https://api.opendesk.sh/v1/tickets/1042/merge \
  -H "Authorization: Bearer od_live_..." \
  -H "Content-Type: application/json" \
  -d '{ "into_id": "1038" }'
```

## Activities

`GET /v1/tickets/{id}/activities` returns the timeline entries that are not messages:

| Type | Data |
| --- | --- |
| `status_changed`, `priority_changed`, `severity_changed`, `assigned` | `{ from, to }`. A `status_changed` with `reason: "sender_unverified"` and `message_id` has `from` equal to `to`: a reply that failed sender checks left the status as it was |
| `team_changed` | |
| `tagged`, `untagged` | `{ tag }` |
| `sla_breached` | `{ kind }` |
| `merged` | `{ into }` |
| `csat_received` | `{ score }` |
| `contact_changed` | `{ from, to }`, each `{ id, name }` (the name, else the email) or `null` |

## Events

`ticket.created`, `ticket.updated` (with a `changes` list), `ticket.deleted` and `activity.created`. See the [events catalog](/api/events/).
