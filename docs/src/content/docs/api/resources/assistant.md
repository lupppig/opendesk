---
title: Assistant (Ollie)
description: Conversations with Ollie, streamed runs, approvals and tips.
---

Ollie's routes are for signed-in people only: an API key gets `403`. They need AI on the instance and in the workspace, and `settings.ai.assistant` on (`403 assistant_off`). The assistant's display name comes from `assistant_name` in [`GET /v1/ai/config`](/api/resources/ai/).

| Method | Path | Min role | Typical cost | Description |
| --- | --- | --- | --- | --- |
| `GET` | `/v1/assistant/sessions` | viewer | $0 | Your conversations, newest first |
| `POST` | `/v1/assistant/sessions` | viewer | $0 | Start a conversation |
| `GET` | `/v1/assistant/sessions/{id}` | viewer | $0 | Transcript and any pending approval |
| `PATCH` | `/v1/assistant/sessions/{id}` | viewer | $0 | Rename |
| `DELETE` | `/v1/assistant/sessions/{id}` | viewer | $0 | Delete |
| `POST` | `/v1/assistant/sessions/{id}/messages` | viewer | $0.02 per model turn | Send a message and run Ollie |
| `POST` | `/v1/assistant/actions/{id}/decision` | viewer | $0.02 per model turn | Approve or deny a proposed change |
| `GET` | `/v1/assistant/tips` | viewer | $0 | Short tips from queue counts |

On opendesk Cloud each model turn is charged to the AI balance on its real cost, on the workspace's model. The typical cost column is what a turn is charged when that cost is unknown; `costs.assistant` in [`GET /v1/ai/config`](/api/resources/ai/#config) is a typical turn on the workspace's model. On the workspace's own provider key it is $0.

Conversations are private to their user: another user's id answers `404`.

## Send a message

```http
POST /v1/assistant/sessions/{id}/messages
Accept: text/event-stream

{ "text": "Who should take this?", "context": { "page": "ticket", "ticket_id": "0190…" } }
```

`context` describes the page the user is on (`path`, `page`, `folder`, `view_id`, `ticket_id`, `filters`), so "this" means the open ticket. Text is 1 to 8,000 characters.

With `Accept: text/event-stream` the run streams:

| Event | Data |
| --- | --- |
| `session` | The conversation (its title is set from the first message) |
| `text` | `{ "text": "…" }`, a piece of Ollie's markdown answer |
| `tool` | A step: `{ id, tool, label, status, summary?, refs?, action_id? }`, sent when it starts and when it ends |
| `approval` | `{ action_id, call_id, tool, preview, token, expires_at }` |
| `done` | `{ "status": "complete" }` or `{ "status": "awaiting_approval" }` |
| `error` | A problem body; the stream ends |

Without it, the same run comes back as one JSON object: `{ session, text, steps, approval, status }`.

A step's `status` is `running`, `done`, `error`, `awaiting_approval`, `approved`, `denied` or `expired`. `refs` link tickets (`kind: "ticket"`, with `number`), contacts, articles, views and automations.

Errors: `402 ai_balance_empty` before anything is stored, `409 assistant_busy` while the conversation is already running, `429` past 30 messages a minute, `501 ai_unsupported` when the provider has no tool use.

## Approvals

Ollie never changes anything on its own. A change arrives as an `approval` event with a `preview`:

```json
{
  "title": "Update #42",
  "summary": "Refund for duplicate charge",
  "changes": [{ "field": "Assignee", "from": "Unassigned", "to": "Alex Morgan" }],
  "targets": [{ "kind": "ticket", "id": "0190…", "label": "Refund for duplicate charge", "number": 42 }],
  "external": false
}
```

`body` holds the markdown of a reply or note, `total` the number of matching tickets for a bulk change, and `external: true` marks a reply that reaches the customer. A change in a connected product names it in `system`. For a connected tool or a change through the API, `arguments` is everything the change runs with, as indented JSON (a connected tool's arguments exactly as they are sent; for the API, `{method, path, query, body}`), `omitted` counts the fields `changes` and `body` leave out, and `clipped: true` marks a value they cut short.

```http
POST /v1/assistant/actions/{action_id}/decision
Accept: text/event-stream

{ "decision": "approve", "token": "<approval token>" }
```

The token is bound to the action, the user, the sign-in session and the workspace, and lasts 15 minutes. A decision runs once: a second answers `409 approval_not_pending`, a wrong token `403 approval_invalid`. Sending a new message expires the pending change. The response is a run, as above, in which Ollie continues.

An approved change goes through the regular API with the user's permissions. Ticket activities record it with `actor_type: "ai"` and the user in `actor.user`, shown as Ollie, and the audit log records `assistant.action`.

A change the workspace lets run without asking (`settings.ai.assistant_auto` for the built-in ones, an integration's `auto_tools` for connected ones) sends no `approval` event: it runs on the same path straight away, and its step ends `done` with `auto: true` and the `preview` of what it ran. The audit entry carries `data.auto: true`. A reply or anything else marked `external`, and every `api_change`, always asks. At most 20 changes run this way for one message.

## Tips

```json
{ "tips": [{ "key": "sla_due_soon", "text": "3 tickets breach SLA within the hour.", "prompt": "Which tickets breach SLA in the next hour?", "severity": "high", "count": 3 }] }
```

Tips are computed from counts without the model and cost nothing. Keys: `sla_breached`, `sla_due_soon`, `sla_at_risk` (open tickets due within 4 hours whose stored triage `sla_risk` is 0.6 or more), `unassigned_old`, `mine_waiting`.
