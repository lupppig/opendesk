# Phone (`internal/phone`)

A phone connection links a workspace to a cloud phone system. Every call, inbound or
outbound, is documented on the caller's ticket as one call entry in its timeline, kept
current while the call rings, is answered, ends and gets its recording, comments and AI
summary. The end of a call runs the `call_ended` automations, so a team decides with
its own rules what a missed call outside opening hours does. It ships Aircall and Voys.

This is not a support channel (`contracts/support-channels.md`): those mirror tickets
into chat tools where agents work them, and their engine is built around threads, cards
and commands. A phone system is a way customers reach the team, like a mailbox. It is
not an integration (`contracts/integrations.md`) either: those give Ollie tools over
MCP. The phone engine reuses their patterns (sealed credentials, a verified webhook
stored and queued in one transaction, health and repair, admin-only connect routes) in a
small package of its own.

The cloud and self-host use the same code. There is no plan gate.

## Model and API

```ts
type PhoneProvider = { key: string; name: string; fields: { name: string; label: string; secret: boolean }[];
  manual_webhook: boolean;     // an admin adds the webhook URL in the provider by hand (Voys)
  insight_cards: boolean };    // a ringing call can show a card in the provider's app (Aircall)
type PhoneConnection = { id: string; provider: string; name: string; account_name: string;
  status: "connected" | "error"; last_error: string;
  health_status: "unknown" | "healthy" | "action_required" | "error";
  health_code: "" | "receiving" | "webhook_missing" | "credentials_refused" | "provider_unreachable";
  health_detail: string; health_checked_at: string | null;
  reuse_days: number;          // 0..90, default 7 (Placement)
  assign_answered: boolean;    // default false (Agents)
  insight_cards: boolean;      // default true where the provider has cards (Insight cards)
  last_event_at: string | null;
  webhook_url?: string;        // admins only: people with the admin role, API keys and AI clients with settings:write;
                               // a manual webhook's ends with its secret (Security)
  created_at: string };
type PhoneLine = { id: string; name: string; provider: string }; // id: the provider's, what call_line conditions compare
```

| method | path | role | body → response |
| --- | --- | --- | --- |
| GET | `/v1/phone/providers` | viewer, `settings:read` | → `{data: PhoneProvider[]}` |
| GET | `/v1/phone/connections` | viewer, `settings:read` | → `{data: PhoneConnection[]}`; never credentials, `webhook_url` for admins only |
| POST | `/v1/phone/connections` | admin user | `{provider, name?, credentials}` → 201 `PhoneConnection`; 422 `phone_credentials_refused`; 409 when the workspace already has that provider |
| PATCH | `/v1/phone/connections/{id}` | admin user | `{name?, reuse_days?, assign_answered?, insight_cards?, credentials?}` → `PhoneConnection` |
| POST | `/v1/phone/connections/{id}/test` | admin user | checks credentials and webhook, registers the webhook again when it is gone or disabled → `PhoneConnection`. A manual webhook is `receiving` once an event came in at its URL, else `webhook_missing` |
| POST | `/v1/phone/connections/{id}/rotate-webhook` | admin user | manual webhooks only (else 422): a new secret, so a new `webhook_url`; the old one is refused at once, `last_event_at` is cleared and the health is `webhook_missing` until an event comes in at the new one → `PhoneConnection` |
| DELETE | `/v1/phone/connections/{id}` | admin user | removes opendesk's webhook at the provider (a manual one is the admin's to remove), then the connection → 204. Calls already on tickets stay |
| GET | `/v1/phone/lines` | admin, `settings:read` | → `{data: PhoneLine[]}`: the provider's numbers (asked at most every 10 minutes per connection, and again after new credentials) and the lines calls came in on, for `call_line` conditions |
| POST | `/v1/phone/hooks/{provider}/{connectionId}` | provider | the provider's webhook (Security) |
| POST | `/v1/phone/hooks/{provider}/{connectionId}/{secret}` | provider | a manual webhook, verified by its secret (Security) |

"Admin user" routes need a person signed in as an admin: connecting a phone system hands
it a way into tickets, so API keys get `403`. One connection per provider per workspace.

Events, to admins only (`events.Event.Admins`): `phone_connection.created` / `.updated`
`{phone_connection}` (also on every new health state), `phone_connection.deleted`
`{phone_connection_id}`. Audit:
`phone.connected`, `phone.updated`, `phone.credentials_rotated`, `phone.webhook_rotated`, `phone.disconnected`.

## Provider adapter boundary

The engine owns connections, sealed credentials, contact matching, placement, the
timeline, automations and every ticket write. An adapter (`internal/phone/provider`) owns
its provider's protocol only, never imports a helpdesk package and never reads a table:

```go
type Adapter interface {
    Describe() Descriptor // {Key, Name, Fields, ManualWebhook, InsightCards}
    Validate(ctx, *http.Client, credentials) (Account, error) // {ID, Name}
    RegisterWebhook(ctx, *http.Client, credentials, url string) (Webhook, error) // {ID, Secret}
    WebhookActive(ctx, *http.Client, credentials, id string) (bool, error)
    DeleteWebhook(ctx, *http.Client, credentials, id string) error
    Verify(body []byte, secret string) error
    Parse(body []byte) (Event, error) // ErrIgnored for events of no use
    Content(ctx, *http.Client, credentials, kind, callID string) (string, error)
    Lines(ctx, *http.Client, credentials) ([]Line, error)
    PushCard(ctx, *http.Client, credentials, callID string, Card) error
}
```

What a provider can do is data in its `Descriptor`, and an adapter answers
`ErrUnsupported` for what its provider doesn't:

- `ManualWebhook`: the provider has no API for webhooks, so an admin adds the connection's
  `webhook_url` in the provider by hand. The engine makes the webhook's secret (32 random
  bytes, base64url) when it connects, seals it in `webhook_secret_enc` and puts it in the
  URL's last segment; it verifies events by that segment and never calls `RegisterWebhook`,
  `WebhookActive`, `DeleteWebhook` or `Verify`. Health is `action_required` /
  `webhook_missing` until a verified event comes in at the URL (`last_event_at`), which
  makes it `healthy` / `receiving` at once (`phone_connection.updated`). New credentials of
  another account (another `Account.ID`) get a new secret, so the old account's events are
  refused, and wait for an event at the new URL again.
- `InsightCards`: the provider shows a card on a ringing call. Without it no card is
  queued and the app hides the setting.

`Validate` returns the account's name and, when the provider's API needs it in paths, its
id: the engine seals it with the credentials (as `account_id`), for the adapter's later
requests. Credentials the provider accepts but no connection can use (Voys: a partner's
token) are `AccountRefused`, whose detail is the 422's. `Content` fetches the text a
transcript event announced, and a summary event's when the event carries none (an unsigned
provider's): the engine never stores AI text from an unsigned body.

`Event` is normalized (`kind` call, summary or transcript; the provider's call id; `Final`
for the event after which the call no longer changes; `Continues`, the id of a call a
transfer joined to this one) and carries a `Call` snapshot:
direction, stage (ringing, answered, ended), the number, the line, the times, the missed
reason in the provider's words, whether there is a recording or voicemail, links, tags,
comments, the agent (name, email) and the provider's own name for the caller. Adding a
provider means implementing this interface and passing it to `phone.New` in `For`; provider
keys are data, not a database enum. Adapters are compiled in, like support channels'.

## Calls

`phone_calls` holds one row per provider call, unique by `(workspace, provider, call id)`,
with its latest state and the ticket, contact and timeline entry it is documented on.
`phone_inbound_events` holds verified events, normalized, until a week after they were
applied (30 days at most); the raw payload is never stored.

A call's content (number, names, links, tags, comments, summary, transcript) lives on its
ticket, in the call's row and its timeline entry, and goes when the ticket's content goes:
the row cascades with the ticket (`ON DELETE CASCADE`, like `messages` and `activities`),
and a deleted ticket keeps its calls as it keeps its messages. A deleted contact's calls stay
on their tickets, as their messages do (`contact_id` is cleared). The only rows without a
ticket are a blocked contact's, which keep no content (Contacts).

Every verified event is applied by the `phone.inbound` job in one transaction that locks
its event row and its call row, so events of one call apply one at a time and each once.
Providers deliver late, twice and out of order, so a call only moves forward: its stage
never goes back, a time once known stays (the start is the earliest seen), the outcome is
computed from the merged state (voicemail, else answered when someone answered, else
missed, once the call ended), tags and comments are the latest event's. The same events in
any order leave the same call.

A transfer can join two provider calls that go on under one of their ids (Voys). Its event
names the other (`Continues`): the call documented under that id takes the id that goes on,
under both calls' locks (ticket first, as every event), unless a call is already documented
under it. Its later events then reach the same call, entry and ticket. An event that knows
no direction (Voys' recording event) only updates a call already seen; one about an unknown
call is retried like an AI event.

### Contacts

A call's number is normalized to E.164 when it says its country (`+`, `00`; spaces, dots,
dashes, brackets and a trunk `(0)` dropped). Contacts match on the number's digits, written
with or without `00` and with a trunk `(0)` dropped as on the call's side (an index on
`contacts.phone`'s digits); the most recently updated wins.
A new number creates a contact named after the provider's contact for it, else the number,
and publishes `contact.created`. Calls from one new number are serialized, so they make one
contact. A withheld number has no contact.

A blocked contact's calls reach no ticket, as their email doesn't (`internal/email` ignores a
blocked sender): the call's row is kept, with its contact, stage and times but without its
number, names, links, tags, comments, summary or transcript, so its later events are known;
it writes no timeline entry, sends no insight card and runs no automation. A call already on
a ticket when its contact is blocked stays there.

### Placement

A call goes on the contact's open ticket (open, pending or on hold, most recently updated);
else on their latest ticket solved (`solved_at`) within the connection's `reuse_days` (0: never);
else on a new ticket on the `phone` channel, routed like any new ticket
(`routing.Resolve` with channel `phone`, so routing rules can send phone tickets to a team),
named "Call from …" or "Call to …" and running the `ticket_created` automations. Like an
`api` ticket, a phone ticket has no reply address: replies there are recorded, not emailed. Merged,
deleted, spam and closed tickets are never reused. A call is placed once, on its first event,
so the insight card can show its ticket; the ticket moves up (`updated_at`, `ticket.updated`
with `changes: ["calls"]`) when the call lands on it and when it ends.

Calls and their call activities move atomically to the destination when tickets merge.
Call updates lock the ticket before the call and recheck ownership after waiting.
Queued `call_ended` work locks its ticket after its model decision and rechecks
ownership before acting. If a merge moved the call, the run starts again on its
current ticket, using the destination’s visibility and keeping the source closed.

### Timeline

A call is one activity of type `call`, actor `system`, created on its first event and
updated in place as the call moves on (`activity.updated`, only when something it shows
changed):

```ts
type CallData = { call_id: string; provider: string; direction: "inbound" | "outbound";
  stage: "ringing" | "answered" | "ended"; outcome: "" | "answered" | "missed" | "voicemail";
  missed_reason: string; number: string; line: string; line_id: string; caller_name: string;
  agent: UserRef | null; agent_name: string;
  started_at: string | null; answered_at: string | null; ended_at: string | null; duration: number;
  recording: boolean; voicemail: boolean;
  asset_url: string; recording_url: string; voicemail_url: string; links_expire_at: string | null;
  tags: string[]; comments: { id: string; text: string; author?: string; at?: string }[];
  summary: string; transcript: string; outside_hours: boolean };
```

opendesk stores links, never media. `recording_url` and `voicemail_url` are the provider's
direct links and stop working at `links_expire_at`; the app then plays through `asset_url`,
the provider's lasting page, which is also what "Open in Aircall" opens, shown only when
there is one. The provider's API link to the call is not kept, as it is no page for people.
Only https links are kept. The summary is bounded to 4,000 characters, the transcript to 20,000, comments to 20.

### Agents

The provider user who took or made a call maps to the member (owner, admin or agent) with
the same email; the entry shows them (`agent`), else the provider's name for them. With
`assign_answered`, an answered call assigns a ticket nobody has, isn't solved or closed and
whose team the member is on, as the system (`assigned` with `via: "phone"`).

The assignment waits for the call's end, while a new phone ticket is made at the first ring
and runs `ticket_created` like any other: when auto-assignment (round robin, a team's rule) or
a `ticket_created` automation assigns it then, the ticket is no longer unassigned when the
call ends, and `assign_answered` leaves it to whoever got it. A workspace that wants the
person who answered to own phone tickets turns auto-assignment off for them (or for the team
phone tickets are routed to).

### Automations

When a call ends (the provider's final event, once per call) the `call_ended` automations
of its ticket run a minute later, so the tags and comments the team adds right after hanging
up are in the call's facts, which are read when they run. Their conditions (only that
trigger takes them; another trigger with one is refused):

| field | ops | values |
| --- | --- | --- |
| `call_outcome` | equals, not_equals | answered, missed, voicemail |
| `call_direction` | equals, not_equals | inbound, outbound |
| `call_missed_reason` | equals, not_equals | the provider's reason, `[a-z0-9_]` (Aircall: `out_of_opening_hours`, `short_abandoned`, `abandoned_in_ivr`, `abandoned_in_classic`, `no_available_agent`, `agents_did_not_answer`; Voys: `no_answer`, `busy`, `cancelled`, `abandon`, `failed`) |
| `call_time` | equals, not_equals | business_hours, outside_business_hours: the call's start against the business hours of the ticket's team, else the workspace's; hours that are off are never outside |
| `call_line` | equals, not_equals, contains, not_contains | equals and not_equals: the line's id (`PhoneLine.id`, which stays when the line is renamed); contains and not_contains: words of its name |
| `call_tag` | equals, not_equals, contains, not_contains | the call's tags, like `tag` |

Every other condition and every action works as on other triggers, so "a missed inbound
call outside business hours reopens the ticket, adds a note and raises its priority" is an
automation, not code. Tags and comments added after the call ended update its entry but
don't run the automations again. A `run_procedure` action waits up to 15 minutes for the call's
transcript when it has a recording (`contracts/ai.md` "Automations").

### Insight cards

With `insight_cards`, on a provider that has them (Aircall), a ringing inbound call shows the caller's name (linked to their
contact), the ticket the call went on and their three latest other tickets in the
provider's app (Aircall: one card per call). Anyone in the provider's app reads it, so it
names no ticket of a private team and no spam, merged or deleted one. The card is sent by
the `phone.insight_card` job and dropped when it could not go out within three minutes.

### Click to call

With a connection, phone numbers on contacts and in the ticket sidebar are `tel:` links
(a `00` prefix becomes `+`). The operating system hands them to the default phone app:
Aircall Workspace or the Voys app, once it is set as the handler for phone links.

## Aircall

Docs: [API reference](https://developers.aircall.io/api-references),
[webhooks](https://developer.aircall.io/docs/webhooks-overview.md),
[insight cards](https://developer.aircall.io/docs/insight-cards.md),
[call summaries](https://developer.aircall.io/docs/ai-call-summaries.md),
[click to dial](https://developer.aircall.io/docs/implement-click-to-dial.md).

- Credentials: an API ID and API token (Basic authentication), checked with
  `GET /v1/company`, whose name names the connection.
- Webhook: `POST /v1/webhooks` with `custom_name: "opendesk"`, the connection's
  `webhook_url` and the events `call.created`, `call.ringing_on_agent`, `call.answered`,
  `call.hungup`, `call.ended` (final), `call.voicemail_left`, `call.comm_assets_generated`,
  `call.tagged`, `call.untagged`, `call.commented`, `summary.created` and
  `transcription.created`. A company Aircall refuses the AI events for (no AI add-on) gets
  the call events alone. The answer's `webhook_id` is stored, and its `token` sealed.
  opendesk only ever reads, replaces or deletes the webhook it created, by that id; it never
  lists the account's webhooks, so other integrations' webhooks stay untouched.
- New credentials that can't see that webhook are another account's: the webhook is removed
  from the old account with the old credentials before they are replaced, so the old account
  stops sending calls, and one is registered in the new account. A new key of the same
  account keeps the webhook. Everything that talks to the provider about one connection
  (connect, test, a credentials change, disconnect) runs one at a time per connection, under
  a transaction-scoped advisory lock, so two at once never register two webhooks; it is not a
  row lock, so incoming events never wait on it.
- Aircall puts the token in every webhook body; opendesk compares it in constant time.
  Aircall waits five seconds and retries a failed delivery up to 50 times before disabling
  the webhook, so opendesk answers 200 at once (also to events it ignores or doesn't
  understand) and a test registers a disabled webhook again.
- A call's `raw_digits` is its number (`anonymous` when withheld), `number` its line,
  `user` who took or made it, `missed_call_reason` why it was missed, `asset` the lasting
  page for its media, `recording_short_url`/`voicemail_short_url` (three hours) else
  `recording`/`voicemail` (one hour) its direct links, `tags` and `comments` the team's.
  `direct_link` is an API URL (it needs the API key), so it is not kept.
- `transcription.created` announces availability only. The inbound job fetches
  `GET /v1/calls/:call_id/transcription` with the connection credentials after checking
  that the call belongs on a ticket. Fetches use the provider timeout and response cap;
  failures retry up to ten attempts, and rate-limit snoozes stop after 24 hours.
- Insight card: `POST /v1/calls/:id/insight_cards` with a `title` and `shortText` lines.
- Rate limit: Aircall allows a company a set number of requests a minute; a 429 snoozes the
  job until `X-AircallApi-Reset`. opendesk calls the API on connect, test, disconnect, the
  lines list, transcript retrieval and one card per ringing inbound call.

## Voys

Docs: [API specs](https://openapi.voys.dev) (`voipgrid/user`, `voipgrid/callerid_numbers`,
`transcription_storage`), [call notifications](https://help.voys.nl/gespreksnotificaties),
[Voys Intelligence](https://help.voys.nl/voys-intelligence).

- Credentials: a user's API token (`Authorization: Bearer`), from Freedom's personal
  settings. `GET https://api.voys.nl/api/v2/users/auth-context` checks it: a customer's user
  has a `client_uuid`, which is the account's id (`account_id`); a partner's user has none
  and is refused with a 422 that says so. The account is named after the customer
  (`GET /api/v2/user/{uuid}/details`, `client.name`), else "Voys". Transcripts and summaries
  need the user to be an admin with access to call recordings, and Voys Intelligence.
- Webhook: a call notification an admin adds in Freedom (Integratie, Gespreksnotificaties,
  Toevoegen, service Custom, the connection's `webhook_url`, with the summary and
  transcription events ticked). Voys has no API for them and signs nothing: the URL's secret
  is the verification (`ManualWebhook`). opendesk answers 200 at once, also to events it
  ignores; Voys' retries are not documented.
- Events (`version: "v2"`, one POST per step): `created` and `ringing` (which repeats as
  phones start ringing) are ringing; `in-progress` is answered, by the colleague in
  `destination.target` (inbound; their first `user_emails` maps to the member) or `caller`
  (outbound); `warm-transfer`, `cold-transfer` (and `transfer`) are answered too and carry
  `merged_id`, the call they joined, which they continue (`Continues`); a warm transfer's
  target is who took the call over. `ended` is final, with `reason`: `completed`, else the
  missed reason (`no-answer` becomes `no_answer`). The customer's number is `caller.number`
  inbound and `destination.number` outbound; the line is the company number on the other
  side, in E.164 (its id and its name on calls). A number withheld (`anonymous`, empty) has
  no contact. A call between colleagues (Voys marks them outbound) is ignored: its other side
  is an extension (up to six digits, no country) or a user of the account. The event's time
  is its `timestamp`, which the week's limit reads. Voys gives no duration: the entry's talk
  time is from its answer to its end.
- After `ended`, with Voys Intelligence: `{call_id, call_transcription}`, then
  `{call_id, summary}` (a call's last event). Both only announce: the inbound job fetches
  `GET https://api.eu-production.holodeck.voys.nl/transcription-storage/clients/{client_uuid}/calls/{call_id}/transcriptions`
  (text) and `.../summaries` (`{summary}`) with the connection's token, never the body's
  text. `{call_id, recording_url}` sets `recording`: the URL is an API URL that needs the
  token, so it is not kept, and the entry has no player or page to open (`asset_url` stays
  empty).
- Lines: `GET /api/v2/clients/{client_uuid}/callerid_numbers` with
  `Accept: application/json;version=2` (`{number, description}`), ten pages at most.
- Rate limit: Voys answers 429 without a reset; the job snoozes a minute (or its `Retry-After`
  up to two minutes). opendesk calls the API on connect, test, a credentials change,
  the lines list, and two fetches per call with Voys Intelligence.
- No insight cards: Voys shows none from other apps.

## Security and operations

- Credentials (with the account id) and the webhook token or secret are AES-GCM sealed with
  `d.Box` (`phone_connections.credentials_enc`, `.webhook_secret_enc`, in `internal/sealed`).
  Responses, events, errors and logs never include them; adapter errors name the
  operation and the status only.
- The webhook is found by its opaque connection id, body-capped at 1 MiB and verified
  before parsing: with the secret the provider puts in the body (Aircall), or, for a manual
  webhook, the URL's last segment, compared in constant time with the sealed secret. A
  secret segment on a provider that registers its webhook is a 404. Request logs show
  `/v1/phone/hooks/<provider>/<id>/…`, never the secret (`httpx.LogPath`), and the bundled
  Caddy keeps those paths out of its access log. Its URL reaches the connection, so only admins read it (`webhook_url`,
  `phone_connection.*` events). A request that names no connection or fails verification
  counts against its address (30 a minute, then that address gets `429` before any work);
  a request with a secret segment is always checked first and gets the `429` only once it
  fails, because a manual webhook left behind after a rotation or a disconnect keeps posting
  from the provider's addresses, which other workspaces' webhooks share: their verified events
  are never refused for its failures. A verified event counts against its connection (600 a minute). The two are apart, so
  forged requests never use up the provider's own allowance, and ids that name no
  connection add no limiter keys. Nothing in a payload
  decides the workspace, the ticket or anyone's access: the connection does. The stored
  event and its job are written in one transaction, and the event id (a hash of the body)
  makes a provider's retry a no-op. Applied events' ids are kept a week, so an event whose
  own time (Aircall's and Voys' `timestamp`) is more than a week old is acknowledged and
  dropped: a replay of it could no longer be recognized. Voys' AI events carry no time; their
  text is fetched from Voys, so a replay only fetches it again.
- Provider API clients have a 15-second timeout and do not follow redirects. Their base
  URL is an adapter constant, never user input.
- Logs carry ids, event names and errors, never numbers, names or call content.
- A suspended workspace's events are acknowledged and dropped.

## Not yet

Aircall SMS and WhatsApp, two-way contact sync, an OAuth app (Aircall Marketplace) instead of
API keys, Aircall's real-time transcription. For Voys: insight cards (Voys has none),
playable recordings (Voys' recording URL needs the API token), contact sync with its contact
book, and registering the call notification for the admin (Voys has no API for it).
