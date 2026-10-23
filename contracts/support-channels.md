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
  external_channel_id: string; external_channel_name: string; enabled: boolean;
  include_notes: boolean; created_at: string };
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
| POST | `/v1/support-channels/destinations` | admin user | `{connection_id,team_id?,external_channel_id,external_channel_name?,include_notes?}` → 201 `Destination` |
| DELETE | `/v1/support-channels/destinations/{id}` | admin user | → 204, including its thread mappings |
| POST | `/v1/me/support-channel-link-code` | agent user | → 201 `{code,expires_at}` |
| POST | `/v1/support-channels/hooks/{provider}/{connectionId}` | signed provider | provider payload → provider acknowledgement |

"Admin user" and "agent user" routes need a person signed in with that role: they hand
ticket access to another service, so API keys get `403` (as integrations' connect routes
do). An AI client's OAuth grant may manage connections and destinations with
`settings:write`, never create a link code.

A destination maps one provider connection and one public team to an external channel.
`team_id: null` is that connection's fallback. A specific team mapping wins over the
fallback. Every matching connection receives the event, so one team can be mirrored to
Slack, Discord and Telegram at the same time. A connection can have only one destination
per team and an external channel can appear only once on that connection.

Private teams cannot have a destination. The dispatcher also rejects any event marked
with private-team visibility, even if old or manually written data contains a mapping.
Version 1 mirrors email tickets only.

`include_notes` (default `false`) decides whether internal notes reach the destination.
Without it, only customer and agent replies are posted, and Ollie reads only the public
conversation when it answers in that destination's threads, so a note can't surface there
through an answer either.

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
`status`, `priority`, `confirm`, `cancel`); any other callback, such as the card's "Open in
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
3. The adapter creates or updates the ticket root and posts text-only message activity.
4. The delivery is marked complete. Provider failures set the connection to `error` and
   River retries; the next successful delivery clears the error. A card that can't be edited
   is retried without touching the connection's status. An edit that changes nothing
   (Telegram's "message is not modified") or lands in an archived Discord thread counts as done.

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
plain text.

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
  `channels:read` and `groups:read`, then invite the bot to each selectable channel.
  Slack does not let a bot token change its own app configuration, so the health state is
  `action_required` and tells an admin to point Event Subscriptions, Interactivity & Shortcuts
  and the `/opendesk` slash command at `webhook_url`, then test them in Slack. Apps connected
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
  It also subscribes to `callback_query` for the ticket and confirmation controls. Connections
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

Events: `support_channel_connection.created {support_channel_connection}`,
`support_channel_connection.updated {support_channel_connection}` (rotated credentials, a
renamed account, a new health state from a test or the first signed webhook),
`support_channel_connection.deleted {support_channel_connection_id}`,
`support_channel_destination.created {support_channel_destination}` and
`support_channel_destination.deleted {support_channel_destination_id}`.
