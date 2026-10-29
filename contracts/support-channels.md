# Support channels (`internal/supportchannels`)

Support channels mirror email tickets into agent-facing chat products. Version 1 ships
Slack, Discord and Telegram. The orchestration is provider-neutral so WhatsApp, Signal,
Teams and other products can be added as adapters without changing ticket behavior.
This is separate from `internal/integrations`: integrations expose MCP tools to Ollie,
while support channels are places where people work tickets.

The cloud and self-host use the same code. There is no plan gate.

## Model and API

```ts
type Provider = { key: string; name: string; description: string; configures: boolean;
  fields: { name: string; label: string; secret: boolean; generated?: boolean;
    discovered?: boolean; help?: string }[] };
type Connection = { id: string; provider: string; name: string;
  external_account_id: string; external_account_name: string; external_account_handle: string;
  status: "connected"|"error"; last_error: string;
  health_status: "unknown"|"healthy"|"action_required"|"error";
  health_detail: string; health_checked_at?: string; webhook_url: string; created_at: string };
type AvailableDestination = { id: string; name: string; kind?: string; available: boolean };
type Destination = { id: string; connection_id: string; team_id: string|null;
	priority: "low"|"normal"|"high"|"urgent"|null;
  external_channel_id: string; external_channel_name: string; enabled: boolean;
  include_notes: boolean; message_limit: number;
  ollie_summary: boolean; ollie_warnings: boolean; ollie_personality: string;
  ollie_length: "brief"|"standard"|"detailed"; created_at: string };
type OllieSettings = { actions: ("reply"|"note"|"assign_self"|"status"|"priority"|"create_ticket")[] };
type Delivery = { event_seq: number; event_type: string; ticket_number?: number;
  status: "queued"|"retrying"|"delivered"|"failed"; attempts: number;
  last_error: string; created_at: string; updated_at: string; delivered_at?: string };
type Operations = { window_started_at: string; generated_at: string;
  connections: { total: number; healthy: number; unhealthy: number; alerted: number };
  deliveries: { queued: number; retrying: number; delivered: number; failed: number;
    average_latency_ms: number; maximum_latency_ms: number; oldest_pending_at?: string };
  webhooks: { rejected: number; duplicates_suppressed: number } };
```

| method | path | role | body → response |
| --- | --- | --- | --- |
| GET | `/v1/support-channels/providers` | viewer, `settings:read` | → `{data: Provider[]}` |
| POST | `/v1/support-channels/providers/{provider}/accounts` | admin user | `{credentials}` → `{data: {id,name}[]}` for providers with selectable accounts |
| GET | `/v1/support-channels/connections` | viewer, `settings:read` | → `{data: Connection[]}` |
| POST | `/v1/support-channels/connections` | admin user | `{provider,name?,credentials}` → 201 `Connection` |
| PATCH | `/v1/support-channels/connections/{id}` | admin user | `{name?,credentials}` → `Connection`, credentials must identify the same provider account |
| POST | `/v1/support-channels/connections/{id}/test` | admin user | reconciles setup and health → `Connection` |
| GET | `/v1/support-channels/connections/{id}/destinations` | admin user | discovers → `{data: AvailableDestination[]}` |
| DELETE | `/v1/support-channels/connections/{id}` | admin user | → 204, including its destinations and thread mappings |
| GET | `/v1/support-channels/destinations` | viewer, `settings:read` | → `{data: Destination[]}` |
| GET | `/v1/support-channels/deliveries` | admin, `settings:read` | `limit?`, `cursor?` → `Page<Delivery>` |
| GET | `/v1/support-channels/operations` | admin, `settings:read` | → `Operations`, a rolling 24-hour view |
| POST | `/v1/support-channels/deliveries/{event_seq}/retry` | admin user | → 202 `{event_seq,status:"queued"}` for a failed event |
| POST | `/v1/support-channels/destinations` | admin user | `{connection_id,team_id?,priority?,external_channel_id,external_channel_name?,include_notes?,message_limit?,ollie_summary?,ollie_warnings?,ollie_personality?,ollie_length?}` → 201 `Destination` |
| PATCH | `/v1/support-channels/destinations/{id}` | admin user | `{ollie_summary?,ollie_warnings?,ollie_personality?,ollie_length?}` → `Destination` |
| GET | `/v1/support-channels/ollie` | viewer, `settings:read` | → `OllieSettings` |
| PUT | `/v1/support-channels/ollie` | admin user | `OllieSettings` → `OllieSettings`, replaces the list |
| DELETE | `/v1/support-channels/destinations/{id}` | admin user | → 204, including its thread mappings |
| POST | `/v1/me/support-channel-link-code` | agent user | → 201 `{code,expires_at}` |
| POST | `/v1/support-channels/hooks/{provider}/{connectionId}` | signed provider | provider payload → provider acknowledgement |

"Admin user" and "agent user" routes need a person signed in with that role: they hand
ticket access to another service, so API keys get `403` (as integrations' connect routes
do). An AI client's OAuth grant may manage connections and destinations with
`settings:write`, never create a link code.

A destination maps one provider connection and an optional public team and priority to an
external channel. A null team matches any team; a null priority matches any priority.
For one connection, the most specific match wins in this order: team and priority, team,
priority, then the all-team/all-priority fallback. This keeps an existing team route ahead
of a new priority-only route. Every matching connection receives the event, so a team can
be mirrored to Slack, Discord and Telegram at the same time. A connection can have one
destination per team/priority combination, and an external channel can appear only once on
that connection. Once a thread exists, priority changes keep updating that thread while
its destination is enabled and still matches the ticket's team. Priority routing selects
the destination for the first post. A move to a team with its own destination posts to
that team's destination.

Private teams cannot have a destination. The dispatcher also rejects any event marked
with private-team visibility, even if old or manually written data contains a mapping.
Version 1 mirrors email tickets only.

`include_notes` (default `false`) decides whether internal notes reach the destination.
Without it, only customer and agent replies are posted, and Ollie reads only the public
conversation when it answers in that destination's threads, so a note can't surface there
through an answer either.

## Ollie

Ollie works in every mapped ticket thread while the workspace assistant is on. Each
destination sets how:

- `ollie_summary` (default on; off for destinations created before it existed, since it
  uses AI credits): once a new thread's history is posted, a job posts Ollie's summary:
  what the customer needs, the steps they say they already tried, their sentiment, the
  request's urgency with a short reason, and the SLA target. It reads only what the thread
  may show (notes only with `include_notes`), never runs for a private team, and is posted
  once per thread (`summary_posted_at`, with a processing lease). It carries quick controls:
  **Suggest a reply** (`suggest`), **What should I do next?** (`ask` with the key `next`)
  and, when Ollie reads the ticket as more urgent than its priority, **Set priority**, the
  card's own `priority` control. An `ask` control carries only a key (`tried`, `next`)
  that the engine maps to a fixed question, so a provider payload never writes the prompt.
- `ollie_warnings` (default on): a two-minute worker posts one warning per SLA stage in a
  thread whose open ticket's next target (first response, then resolution) is within 30
  minutes, and again once it is missed. The thread remembers the stage it warned about
  (`sla_warning`: kind, stage and due time), so a new target warns again and a failed post
  is retried by the next run. A delivery posts one warning when the ticket's stored
  sentiment (`custom.ai_sentiment`) turns `negative` or `frustrated`, and forgets it once
  the sentiment recovers, so a later change warns again.
- `ollie_length` (`brief`, `standard`, `detailed`) sets the length of Ollie's answers,
  suggestions and summaries in the destination's threads and its general channel.
- `ollie_personality` (up to 500 characters, no `<` or `>`, control characters other than
  newlines dropped) is how Ollie sounds there. It reaches the model as delimited data that
  shapes tone and wording only and never changes the rules or the tools.

Destination changes are audit logged (`support_channel.destination_updated` with the
changed keys) and publish `support_channel_destination.updated`.

A suggestion drafts a reply to the customer's latest message with no tools offered, so it
cannot send anything or change the ticket. It is posted as "Suggested reply, not sent" and
kept in the agent's conversation history: asking Ollie to send it goes through the normal
`reply` action, which waits for confirmation.

`OllieSettings.actions` are the ticket changes Ollie may choose when it reads a message
(default: all of them). Ollie is offered only those tools; a tool call for anything else
is refused without a change, and a pending change the workspace stopped allowing is
refused when it is confirmed. Explicit commands and card controls are the agent's own and
are never limited by this list. Ticket creation outside a ticket thread needs
`create_ticket`. Saving the list is audit logged (`support_channel.ollie_updated`) and
publishes `support_channel_ollie.updated`.

## Provider adapter boundary

The engine owns connections, encrypted credentials, routing, idempotency, retries,
identity linking, authorization and every ticket mutation. An adapter owns only its
provider protocol:

```go
type Adapter interface {
    Describe() Descriptor
    Validate(context.Context, *http.Client, map[string]string) (Account, error)
    Verify(http.Header, []byte, map[string]string) error
    Parse(http.Header, []byte) (WebhookResult, error)
    CreateThread(context.Context, *http.Client, map[string]string, string, Ticket) (Thread, error)
    Post(context.Context, *http.Client, map[string]string, string, string) error
    Update(context.Context, *http.Client, map[string]string, string, Ticket) error
}
```

Adapters with native controls also implement `InteractiveAdapter`. The engine gives it a
provider-neutral message and actions; the adapter renders Block Kit, Discord components or
a Telegram inline keyboard (`PostInteractive`) and removes a message's controls while keeping
its text (`ClearActions`). Parsing accepts only opendesk's own controls (`assign_self`,
`status`, `priority`, `confirm`, `cancel`, `suggest`, `ask`); any other callback, such as the card's "Open in
opendesk" link (`opendesk:open`), is acknowledged without a command. A control's `Inbound`
names the message it sat on (`SourceMessageID`), which is how the engine tells a card action
from a typed command. Action payloads carry only a validated command and value. The engine
derives the ticket from the stored thread, reloads the linked member and checks ticket
visibility before every mutation. Adapters that can answer one person privately implement
`Responder` with the `Inbound.Respond` handle: Slack's `response_url` (ephemeral, only to
`hooks.slack.com`), a Discord interaction's ephemeral follow-up, Telegram's
`answerCallbackQuery`. Errors and notices such as "link your account" go that way when the
action came with a handle, and into the thread otherwise. Every Telegram callback is answered
by the worker once the action ran.

Adapters implement `RichAdapter` to post, update and remove mirrored ticket messages and
return the provider message id. A rich message contains bounded plain text, safe attachment
metadata and freshly signed opendesk download URLs. Storage keys never cross the boundary.
An adapter implements `AttachmentDownloader` when agents can add provider files to an
explicit `reply` or `note`. The engine downloads with the connection's credentials, limits
the whole message to 20 files and 25 MB, cleans the names, sniffs the bytes, refuses programs
and scripts, stores each file through `internal/storage`, then calls the normal ticket write.

A provider that delivers chat over a long-lived connection also implements
`Subscriber`: `Subscribe(ctx, client, credentials, Stream) error` runs one subscription,
hands events to `Stream.Emit` and calls `Stream.Connected` once the provider accepts the
session. The engine reconnects after an error with backoff, except a `FatalError`, whose
`Reason` becomes the connection's `last_error`. `Thread.ChannelID` names the channel a
thread's messages arrive from when it isn't the destination channel.

Onboarding is split into optional capabilities so providers implement only what their
API supports: `Discoverer` lists destinations, `Configurer` registers provider-side
webhooks or commands, and `HealthChecker` verifies that setup. Signed webhook results may
also carry discovered destinations for providers that cannot list them. Credential
rotation validates that the replacement credentials belong to the same external account,
then updates the sealed value in place; destinations and ticket threads keep their ids.
Generated and discovered fields (Telegram's webhook secret, Discord's server) keep their
values unless replaced. Long-lived subscribers restart when `credentials_updated_at` changes.

Every setup and test validates the credentials first and refreshes the account's name and
`external_account_handle` (the bot's `@username` where people address it by one). A
provider-wide setting, such as Discord's interactions endpoint or Telegram's webhook, is
one per bot: `Configure` leaves it alone when it is another existing connection's webhook
(`Setup.Foreign`) and returns `channel.ErrInUse`, and the connection shows
`action_required` until that connection is removed or a separate bot is used.

An adapter is registered in `NewRegistry` at boot. It never imports a helpdesk package or
reads a helpdesk table. `Inbound` is the normalized command envelope and
`WebhookResult` carries both normalized actions and the provider's immediate protocol
acknowledgement. This supports handshakes such as Slack URL verification and Discord
interaction responses without provider branches in the engine. Adding a provider means
implementing this interface, registering it and adding its settings metadata. It does
not require a schema change: provider keys are data, not a database enum.

Google Chat uses app authentication with the `chat.bot` scope, which limits the app to
spaces it belongs to. For a standard Chat app (not a Google Workspace add-on), the HTTP
connection URL and authentication audience are the connection's webhook URL. The inbound
request is accepted only when its Google-signed OIDC token has that audience and the
`chat@system.gserviceaccount.com` identity. Add the Chat app to each space used as a
destination.

Adapters are compiled into the binary. They are plugin-shaped Go packages, not Go
runtime `.so` plugins, which keeps builds portable and self-hosting predictable.

## Delivery

Committed `ticket.created`, `ticket.updated` and `message.created` events enqueue one
dispatcher job through the event relay, for workspaces with at least one enabled
destination only: the relay looks those up with one indexed query per batch of events, and
none for a batch without ticket events. For each matching destination:

1. `(destination_id,event_seq)` claims the delivery. A completed claim makes replay a no-op.
2. `(destination_id,ticket_id)` claims the external thread before network I/O, so two
   workers cannot create two threads. The thread is then keyed by
   `(connection_id, external_channel_id, external_thread_id)`, where the channel is the one
   the provider delivers the thread's messages from (the destination channel, or the thread
   itself on Discord, whose threads are channels). Thread ids are only unique within a chat
   on some providers (Telegram topic ids), so inbound messages are always matched by channel
   and thread together.
3. The adapter creates or updates the ticket root and posts message activity with safe
   attachment links. It records provider message ids so later note edits, deletions and
   restores stay synchronized with the provider copy.
4. The delivery is marked complete. Provider failures set the connection to `error` and
   River retries; the next successful delivery clears the error. A card that can't be edited
   is retried without touching the connection's status. An edit that changes nothing
   (Telegram's "message is not modified") or lands in an archived Discord thread counts as done.

The settings delivery view records queued, retrying, delivered and failed event dispatches.
A dispatch remains in the view after River exhausts its attempts, so administrators can
inspect and retry it. Retrying a failed event enqueues it in the same transaction as the
state change and audit entry. Successful destination deliveries remain idempotent when that
event is replayed. The view shows recent events to administrators; provider responses and
credentials are not exposed there.

The reliability view summarizes those dispatch states over the last 24 hours, their
end-to-end latency from relay queueing through provider delivery, the oldest pending
dispatch, provider connection health, rejected signed-webhook attempts and provider event
IDs suppressed as duplicates. Metrics are workspace-scoped and available only to admins.
Webhook counters are hourly buckets tied to the connection, retained for 30 days, and
disappear with it. Metric recording is best effort and never rejects an otherwise valid
provider event.

A connection entering `action_required` or `error` records the start of one unhealthy
episode. If it remains unhealthy for 15 minutes, a five-minute worker sends one
`support_channel_unhealthy` notification to every owner and admin. It is not repeated for
that episode. A healthy check or signed webhook clears the episode and alert, so a later
failure can alert again. The settings page keeps an alerted state visible until recovery.

The root is an editable ticket card showing the ticket number, subject, customer, assignee,
status, priority and opendesk URL. It offers Assign to me, status and priority controls. A
per-thread rendering lease and event sequence keep an older delivery from overwriting a newer
card. When a thread is first created, opendesk posts the five most recent public messages from
before the event that created it, in chronological order (and notes only when `include_notes`
is on). Messages from the last 15 seconds before that event, including the event's own
message, are left out: their own deliveries are still to come and post them in full. The
thread records the event (`history_event_seq`) and the ids of the messages the history holds,
so an earlier message event that runs late posts only a message the history left out.
Messages identify customer replies, agent replies and, where the destination includes
them, internal notes. Stored HTML is never sent to a provider; adapters receive bounded
plain text. Each destination chooses a limit from 500 to 3,500 characters. When the text is
longer, the provider gets the bounded text and a **View in opendesk** link to the complete
ticket. Attachment links are signed again for every attempt, so a retry never reuses an
expired event URL.

An explicit provider `reply` or `note` may carry files. Provider-side edits and deletions
are matched by the recorded source message id. An edit or deletion of the member's own note
uses the same authorship, visibility, deletion-policy, activity and event path as the API.
A provider edit or deletion never changes a customer-facing reply, because it may already
be in the customer's inbox. Providers which do not report the actor for a deletion do not
apply that deletion. Telegram reports edits but not deletions through its Bot API.

## Conversations, commands and identity

Linked agents can talk to Ollie inside a mapped ticket thread. They can also ask Ollie
to create a ticket from an enabled destination's general channel or an unrelated thread.
The engine gives Ollie the visible ticket and its messages when there is one, plus a bounded per-agent conversation history.
Ticket content is delimited as untrusted data. Ollie may answer a question or emit one
of the typed actions below; generated text is never executed as a command.

| command | effect |
| --- | --- |
| `link <code>` | consumes the signed-in agent's 10-minute, one-use code and links the external user |
| `reply <text>` | sends a public ticket reply through the ticket's normal mailbox path |
| `note <text>` | adds an internal note |
| `assign` / `assign_self` | assigns the linked agent |
| `status <open|pending|on_hold|solved|closed>` | changes status |
| `priority <low|normal|high|urgent>` | changes priority |
| Ollie `create_ticket {subject,description?}` | creates a new ticket through the normal ticket service (channel `api`); Ollie answers with its number and link. It gets no thread, since version 1 mirrors email tickets only |

Natural-language ticket creation, customer replies and closing a ticket, when Ollie proposes
them, wait for an explicit `confirm` or `cancel` response and expire after ten minutes. So does
closing from the ticket card. A typed command (`reply`, `status closed`, ...) is deliberate and
runs at once. The preview is a native provider message with Confirm and Cancel controls bound
to that pending action by a random nonce; typing the words remains a fallback. While one
change waits, another preview is refused. Buttons are answered without the model: the
matching nonce applies or discards the change and removes the preview's controls, and the
answer is posted in the thread. A button whose change is gone (stale, already used, expired)
says so privately and loses its controls; one whose change waits for someone else says so and
keeps them. A card change answers with the card's own edit, not a message. Drafting a reply
does not send it. Notes, self-assignment, status and priority changes can run immediately
after the model emits their validated schema. A pending action and the ticket mutation are consumed in
one transaction, so two confirmation events cannot apply it twice. A change Ollie makes
runs with the linked agent's role and checks and is attributed to Ollie on their behalf
(`auth.Principal.Assistant`: activity actor `ai` with the agent as `user_id`, as for the
in-app assistant); an explicit command is the agent's own.

Ticket changes and explicit commands run only inside a mapped ticket thread. Ticket
creation may start from a message explicitly directed at Ollie in any enabled destination
and uses that destination's team as its routing hint. Ordinary general-channel chatter is
ignored. A message is directed when the provider says so (a Slack or Discord mention, a
Discord command) or when its `Inbound.Mention` is the connection's own handle (a Telegram
message opening with the bot's `@username`, or a reply to the bot's message). While a
ticket waits for confirmation, the same person's next message in that thread reaches Ollie
without a mention where the provider delivers it. Before every action, the engine reloads the linked user, workspace
membership, suspension state and role. Existing-ticket actions load the ticket through
`domain.LoadVisibleTicket`. Replies use `tickets.PostMessage`; changes use
`tickets.ApplyPatch`; creation uses `tickets.CreateTicket`. The normal sanitizing, mailbox,
signature, event, automation, notification and audit behavior therefore remains the
single implementation. If Ollie or the workspace assistant is off, explicit commands
continue to work.

In Slack an agent mentions the bot inside the ticket thread. Telegram accepts ordinary
text inside the ticket's forum topic. Discord accepts a bot mention inside the ticket
thread over its Gateway connection; `/ask` is an interactions-only fallback. Explicit
syntax remains `@opendesk reply ...` in a Slack thread, application commands in Discord,
and `/reply ...` in Telegram.

Slack slash commands never carry a thread, so they name the ticket by number:
`/opendesk 123 status solved`, `/opendesk #123 reply ...` or `/opendesk 123 what changed?`.
The engine first requires a linked agent, then resolves the number to that ticket's
mirrored thread on the same connection (preferring the channel the command came from)
and runs the command exactly as if it were written in that thread, through the same
authorization path. A number without a mirrored thread answers "That ticket is not
available." `/opendesk link <code>` needs no number.

## Provider setup

- **Slack:** bot token and signing secret. Grant `chat:write`, `app_mentions:read`,
  `channels:read`, `groups:read`, `channels:history`, `groups:history` and `files:read`,
  then invite the bot to each selectable channel.
  Slack does not let a bot token change its own app configuration, so the health state is
  `action_required` and tells an admin to point Event Subscriptions, Interactivity & Shortcuts
  and the `/opendesk` slash command at `webhook_url`, then test them in Slack. Subscribe to
  `app_mention`, `message.channels` and `message.groups` bot events.
  Apps connected
  before the ticket controls existed lack Interactivity: they return to `action_required`
  with the setup steps until a signed callback from one of the controls arrives
  (`WebhookResult.Control`); other callbacks don't clear that step.
- **Discord:** bot token and interactions public key. opendesk lists the servers where the
  bot is installed, then lists text channels in the selected server. Install the bot with
  thread/message access and enable the privileged Message Content intent. opendesk sets
  the Interactions Endpoint URL and registers the commands above plus
  `/ask` through Discord's API. The endpoint is one per Discord application: an endpoint
  that is not another opendesk connection's is replaced, so a bot that also serves another
  backend needs a bot of its own for opendesk. Only missing commands are registered; a
  command of the same name already on the server is left as it is. Worker processes maintain one leased Gateway connection per Discord connection,
  so multiple workers do not handle the same message. A dropped connection resumes its
  session where Discord allows it; otherwise the worker reconnects with exponential
  backoff and jitter, from five seconds up to five minutes. Close codes that mean the
  provider refused the bot (4004 authentication failed, 4010 to 4014 shard, version and
  intents errors) stop the subscription and set the connection to `error` with a reason
  for the admin, instead of retrying. A successful session sets it back to `connected`.
- **Telegram:** bot token. opendesk generates and seals the webhook secret, calls
  `setWebhook` with `webhook_url`, and subscribes to `message` and `my_chat_member`.
  It also subscribes to `edited_message` for note edits and `callback_query` for the ticket
  and confirmation controls. Telegram's Bot API does not send message deletions. Connections
  made before that are marked (`reconfigure`) and set up again once by a worker at boot,
  retried hourly while the provider refuses.
  Add the bot as an administrator to a forum-enabled group; that signed membership update
  makes the group selectable without entering its id. Disable privacy mode with BotFather
  so the bot receives ordinary topic messages.

## Security and operations

- Provider credentials are AES-GCM sealed with `d.Box`; API responses, events, errors and
  logs never include them. The Telegram Bot API carries the bot token in its URL, so its
  transport errors name the method and cause only, never the URL, and mask the token.
  Connect, disconnect, destination changes and linking an external account (which lets it
  act as the agent) are audit logged: `support_channel.connected`, `.disconnected`,
  `.destination_created`, `.destination_deleted`, `.identity_linked`, and
  `.identity_unlinked` for the identity a new link replaces. One external account links to
  one agent per connection and one agent to one external account.
- Webhooks are found by an opaque connection UUID, body-capped at 1 MiB, then authenticated
  before parsing: Slack HMAC with a five-minute timestamp window, Discord Ed25519, Telegram
  constant-time secret-token comparison.
- Inbound event persistence and River enqueue share one transaction. Provider event IDs
  deduplicate retries. Link codes are random, stored hashed, expire after ten minutes and
  are consumed in the identity transaction.
- Conversation history and pending actions are scoped to a connection, external channel,
  external thread and linked external user. A renewable processing lease serializes that
  agent's messages, and only the ten most recent turns are retained. Provider adapters only emit normalized
  chat text; they never call the model or ticket services.
- Every inbound action resolved to a ticket receives a monotonic sequence from that ticket's
  database lane in the same transaction that persists the signed provider event. A worker may
  process only the head: the first earlier command that is unprocessed, still stored and under
  ten minutes old. The lane moves past any other (a connection's deleted events, a job River
  discarded), so nothing stalls a ticket for longer than that; a command it moved past still
  runs if River retries it, without ordering. A command waiting for the head, or for its
  sender's conversation lease, snoozes without spending a River attempt. The head's lease
  outlasts the job's timeout, and a failed command is retried before any later Slack, Discord
  or Telegram command for that ticket. Every path that doesn't ask for a retry finishes the
  event, including one whose provider is no longer registered.
- Provider API clients have a 15-second timeout and do not follow redirects. Their URLs
  are adapter constants, never user input.
- Provider posts suppress link previews and mass mentions, so customer-controlled
  subjects and message text cannot notify an external channel or fetch arbitrary links.
  Attachment links use the API's short-lived signed URLs and may stop opening after about
  two hours; the durable `View in opendesk` link remains available on mirrored messages
  that include attachments.

Events: `support_channel_connection.created {support_channel_connection}`,
`support_channel_connection.updated {support_channel_connection}` (rotated credentials, a
renamed account, a new health state from a test or the first signed webhook),
`support_channel_connection.deleted {support_channel_connection_id}`,
`support_channel_destination.created {support_channel_destination}`,
`support_channel_destination.updated {support_channel_destination}`,
`support_channel_destination.deleted {support_channel_destination_id}` and
`support_channel_ollie.updated {support_channel_ollie}`.
