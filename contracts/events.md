# Events

Every state change that someone else should see is written as an **event** in the
same database transaction as the change (`events` table = transactional outbox).
Use `events.Publish(ctx, tx, events.Event{...})` from `internal/events`; never write
the table by hand.

`seq` is contiguous **per workspace** (`workspaces.event_seq`, incremented with
`UPDATE ... RETURNING`). A client that sees `seq` jump knows it missed events and
fetches them with `GET /v1/events?since=<last_seq>`.

## Envelope

```jsonc
{
  "id": "0192f0c4-...",          // uuid v7, unique
  "seq": 1042,                    // contiguous per workspace
  "workspace_id": "…",
  "type": "message.created",
  "visibility": "agents",         // "agents" | "public"
  "topics": ["workspace:<wid>", "user:<uid>", "contact:<wid>:<contact_id>"],
  "data": { /* see catalog */ },
  "occurred_at": "2026-10-11T09:00:00Z"
}
```

- `visibility: "public"` events may reach the end customer (widget). Internal notes,
  assignment, tags, SLA and everything else is `"agents"`.
- `topics` decides fan-out in the gateway. Every event includes `workspace:<wid>`, except events
  about a ticket in a private team: those carry `team:<wid>:<tid>` instead (contracts/teams.md "Visibility").
  `events.Publish` finds the ticket in `TicketID`, else in the data: a `ticket_id`, or a value
  naming a ticket (`events.TicketRef`: a ticket, message, activity or notification). Only
  `ticket.hidden` opts out (`Workspace: true`), as it carries nothing but ids.
  Add `user:<uid>` for per-user notifications, and `contact:<wid>:<contact_id>:<brand_id>` (the
  chat's brand) for public events about a chat conversation.

### Scoped to people

An event with `events.Event.Users` belongs to those people alone (Collab mail, contracts/collab.md):
its topics are `private` and one `user:<uid>` per person, never `workspace:<wid>` or a team. The
gateway pushes it in full to those user channels and the `event.hidden` stand-in to `workspace:<wid>`
(so `seq` stays contiguous), `GET /v1/events` replays it to them and the stand-in to everyone else
(`events.user_ids`), and no webhook receives it.

## Transport

1. `events.Relay` (worker role) reads unpublished rows in seq order, publishes each to
   NATS subject **`od.events.<workspace_id>`** (JSON envelope as payload), sets
   `published_at`, and enqueues webhook deliveries.
2. The Elixir gateway subscribes to `od.events.>` and pushes to Phoenix channels.
3. Live previews (the widget agent's streamed reply) go straight to NATS on **`od.live.<workspace_id>`**,
   outside the outbox: no `seq`, not stored, not replayed, not sent to webhooks (`contracts/realtime.md`).
4. Control messages on **`od.control.disconnect`**: `{"user_id": "...", "session_id": "..."?}`
   tells the gateway to close that user's sockets (logout, password change, removal).
5. Rows are kept 7 days for replay, then pruned.

## Catalog

`data` shapes. Objects are the same JSON the REST API returns for that resource.

| type                     | visibility | topics (besides workspace)            | data |
| ------------------------ | ---------- | ------------------------------------- | ---- |
| `ticket.created`         | agents     | —                                     | `{ "ticket": Ticket }` |
| `ticket.updated`         | agents     | `user:<assignee>` when assignee changes | `{ "ticket": Ticket, "changes": ["status","assignee_id","tags","fields",...] }` |
| `ticket.deleted`         | agents     | —                                     | `{ "ticket_id": "..." }` |
| `message.created`        | agents (note) / public (reply on chat ticket) / agents (reply on email ticket) | `contact:<wid>:<cid>:<brand_id>` when public | `{ "ticket_id": "...", "message": Message }` |
| `message.updated`        | agents (public on chat tickets) | `contact:<wid>:<cid>:<brand_id>` when public | `{ "ticket_id": "...", "message": Message }` (delivery status; the widget agent's action card status and visitor rating, `contracts/agent.md`) |
| `activity.created`       | agents     | —                                     | `{ "ticket_id": "...", "activity": Activity }` |
| `thread.created` / `thread.updated` | agents | — | `{ "ticket_id": "...", "thread": Thread }` (contracts/api.md "Threads"): started; a message written or answered, closed or reopened |
| `contact.created` / `contact.updated` / `contact.deleted` | agents | — | `{ "contact": Contact }` / `{ "contact_id" }`. `contact.updated` also when a picture is uploaded, removed or found automatically |
| `company.created` / `company.updated` / `company.deleted` | agents | — | `{ "company": Company }` / `{ "company_id" }` |
| `tag.created` / `tag.updated` / `tag.deleted` | agents | — | `{ "tag": Tag }` / `{ "tag_id" }` |
| `tag.merged`             | agents     | —                                     | `{ "tag_id": "<removed>", "into": Tag }` (records that carried it are updated without per-record events; refetch lists) |
| `tag_group.created` / `tag_group.updated` / `tag_group.deleted` | agents | — | `{ "tag_group": TagGroup }` / `{ "tag_group_id" }` |
| `custom_field.created` / `custom_field.updated` / `custom_field.deleted` | agents | — | `{ "custom_field": CustomField }` / `{ "custom_field_id", "entity", "key" }` |
| `mailbox.created` / `mailbox.updated` / `mailbox.deleted` | agents | — | `{ "mailbox": Mailbox }` / `{ "mailbox_id" }` |
| `member.added` / `member.updated` / `member.removed` | agents | `user:<uid>` | `{ "member": Member }` / `{ "user_id" }`. `member.updated` also on availability, profile and picture changes (in every workspace of that user) |
| `workspace.updated`      | agents     | —                                     | `{ "workspace": Workspace }` (name, settings, logo and brand images) |
| `notification.created`   | agents     | `user:<uid>`                          | `{ "notification": Notification }` |
| `ai_suggested_reply.created` / `ai_suggested_reply.updated` | agents | — | `{ "ticket_id": "...", "suggested_reply": SuggestedReply }` (`contracts/agent.md` "Email"). `created` when the agent suggests a reply on an email ticket (it supersedes the pending one); `updated` when a teammate sends or discards it |
| `ai_mcp_server.created` / `ai_mcp_server.updated` / `ai_mcp_server.deleted` | agents | — | `{ "mcp_server": McpServer }` / `{ "mcp_server_id" }` (`contracts/agent.md`). `updated` also when a sync changes the tools or the status |
| `ai_knowledge.created` / `ai_knowledge.updated` / `ai_knowledge.deleted` | agents | — | `{ "source": KnowledgeSource }` / `{ "source_id" }` (`contracts/agent.md`). `updated` on every status change of a sync |
| `ai_agent_test.created` / `ai_agent_test.updated` / `ai_agent_test.deleted` | agents | — | `{ "test": AgentTest }` / `{ "test_id" }` (`contracts/agent.md` "Tests") |
| `ai_agent_test_run.created` / `ai_agent_test_run.updated` | agents | — | `{ "run": AgentTestRun }` (without `results`): on start, when the job picks it up, after each test and when it ends or is stopped |
| `ai_agent_test_result.updated` | agents | — | `{ "result": AgentTestResult }`: a test of a run finished (`passed`, `failed` or `error`); it becomes that test's `last_result` |
| `ai_agent_review.updated` | agents | — | `{ "message_id", "ticket_id", "outcome": "dismissed" \| "improved" }`: an agent reply left the review queue |
| `billing.updated`        | agents     | —                                     | `Billing` (`contracts/billing.md`): plan, status, seats, limits, usage, AI balance, prices. Sent on subscription changes, seat syncs, AI top-ups and recharges |
| `saved_reply.*`, `view.*`, `automation.*`, `sla_policy.*`, `kb_article.*`, `kb_collection.*`, `team.*`, `webhook.*` | agents | — | `{ "<resource>": Obj }` / `{ "<resource>_id" }` |
| `brand.created` / `brand.updated` / `brand.deleted` | agents | — | `{ "brand": Brand }` / `{ "brand_id" }` |
| `routing_rule.created` / `routing_rule.updated` / `routing_rule.deleted` | agents | — | `{ "routing_rule": RoutingRule }` / `{ "routing_rule_id" }` |
| `widget.created` / `widget.updated` / `widget.deleted` | agents | — | `{ "widget": Widget }` / `{ "widget_id" }` |
| `email_template.updated` | agents | — | `{ "brand_id", "kind": "layout" \| "csat" }` (contracts/templates.md): a brand's email layout or CSAT email was saved or reset; refetch it |
| `bot.created` / `bot.updated` / `bot.deleted` | agents | — | `{ "bot": Bot }` / `{ "bot_id" }` (contracts/bots.md); a new default, a reorder or a new picture is `bot.updated` of each bot it changed |
| `integration.created` / `integration.updated` / `integration.deleted` | agents | — | `{ "integration": Integration }` / `{ "integration_id" }` (`contracts/integrations.md`; never credentials) |
| `support_channel_connection.created` / `support_channel_connection.deleted` | agents | — | `{ "support_channel_connection": Connection }` / `{ "support_channel_connection_id" }` (`contracts/support-channels.md`; never credentials). Deleting one removes its destinations |
| `support_channel_destination.created` / `support_channel_destination.deleted` | agents | — | `{ "support_channel_destination": Destination }` / `{ "support_channel_destination_id" }` |
| `ticket_link.created` / `ticket_link.deleted` | agents | — | `{ "ticket_id", "link": Link }` / `{ "ticket_id", "link_id" }` |
| `import.created` / `import.updated` | agents | — | `{ "import": Import }` (`contracts/imports.md`; never credentials). `updated` on every state change and as it writes, at most once a second and after every page. Imported tickets, messages, contacts and articles publish no events of their own: refetch lists |
| `collab.changed` / `collab.accounts` | agents | scoped to people | ids only (contracts/collab.md "Events") |
| `chat.typing` (not stored; gateway only) | public | `contact:<wid>:<cid>` | `{ "ticket_id": "...", "user": {"id","name"} }` |

Webhooks receive the envelope unchanged (minus `topics`); an event of a private team's ticket (`events.team_id`)
only when the webhook's `private_team_ids` include that team, and no stand-in otherwise. Signed:
`OD-Signature: t=<unix>,v1=<hex hmac_sha256(secret, "<t>.<body>")>`, `OD-Event-Id`, `OD-Event-Type`.

Reorders (`/v1/tags/reorder`, `/v1/tag-groups/reorder`, `/v1/custom-fields/reorder`) publish one `*.updated` per moved row.
Deleting a label or field, merging labels and removing select options change records in bulk without a `ticket.updated` per record.
