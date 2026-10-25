# opendesk realtime gateway

Phoenix Channels over WebSocket for opendesk (`gateway.opendesk.sh`, or `/socket` on a
self-hosted domain). It pushes workspace events from NATS to browsers, tracks who is on a
ticket (Presence) and relays typing indicators. It has **no database and no Redis**:
everything it needs to authorize a join is in the token's claims.

Specs: [`contracts/realtime.md`](../contracts/realtime.md),
[`contracts/tokens.md`](../contracts/tokens.md), [`contracts/events.md`](../contracts/events.md).

## Run

```sh
mix deps.get
PORT=4001 NATS_URL=nats://127.0.0.1:4222 mix run --no-halt   # dev: dev AUTH_SECRET, any Origin
mix test                                                        # unit and channel tests
mix check                                                       # format, compile, credo, sobelow, deps.audit, test
MIX_ENV=prod mix release                                        # _build/prod/rel/realtime/bin/realtime start
```

In dev the defaults match `go run ./cmd/opendesk serve`: the same development
`AUTH_SECRET`, and NATS on `127.0.0.1:4222` (the Go binary's embedded server, `NATS_LISTEN`).

## Environment

| variable | default | |
| --- | --- | --- |
| `PORT` | `4000` | HTTP/WebSocket port. Plain HTTP; TLS terminates at the ALB or Caddy. |
| `AUTH_SECRET` | dev default outside prod | HS256 key shared with the Go API. **Required in prod, at least 32 bytes, or the gateway refuses to boot.** |
| `SECRET_KEY_BASE` | generated outside prod | Required in prod, at least 64 bytes (`openssl rand -base64 48`). |
| `CHECK_ORIGINS` | any origin outside prod | **Required in prod.** Comma-separated browser origins allowed to open the agents' socket (`/socket`; the widget socket `/socket/widget` allows any origin), `scheme://host[:port]` (e.g. `https://app.opendesk.sh`), or `//host` for any scheme. Requests without an `Origin` (non-browser clients) are allowed; their token authorizes them. |
| `NATS_URL` | `nats://127.0.0.1:4222` | `nats://host:port` or `tls://host:port`. |
| `NATS_TOKEN` | none | NATS auth token, if the server requires one. |
| `CLUSTER_DNS` | none | DNS name with one A record per gateway task (ECS service discovery). Turns on clustering. |
| `RELEASE_COOKIE` | none | Erlang distribution cookie. Required when `CLUSTER_DNS` is set; the same secret on every node. |
| `NODE_IP` | detected | This task's private IP for the node name (`realtime@<ip>`), from ECS task metadata or `hostname -i`. An entrypoint may instead set `RELEASE_NODE` itself. |
| `DIST_PORT_MIN` / `DIST_PORT_MAX` | `9100` / `9155` | Distribution port range when clustered (plus epmd on 4369). |
| `RELEASE_TMP` | release dir | Set to a writable path (`/tmp`) on a read-only root filesystem. |
| `POSTHOG_KEY` | none | Turns on PostHog metrics and error reports. |
| `POSTHOG_HOST` | `https://us.i.posthog.com` | PostHog ingestion host. |
| `MAX_SOCKETS_PER_SUB` | `20` | Concurrent sockets per token subject (user, API key or contact), cluster-wide. |
| `MAX_SOCKETS_PER_IP` | `200` | Concurrent sockets per client IP, cluster-wide. |
| `MAX_SOCKET_AGE` | `3600` | Seconds an agents' socket (users and API keys) lives before the gateway closes it (code 1001) and the client reconnects with a fresh token. Each socket takes a random 80 to 100 % of it. `0` for no limit. The widget's socket has none. |
| `TRUSTED_PROXY_HOPS` | `0` | Proxies in front of the gateway that append to `X-Forwarded-For`. `0` uses the TCP peer. Behind one ALB or Caddy set `1`; behind Cloudflare plus an ALB, `2`. |

Blank variables count as unset.

## Client usage (web app and widget)

Use the `phoenix` npm package. Fetch a fresh token before every connect (they live 5 minutes
and only need to be valid at connect): agents `POST /v1/realtime/token`, the widget
`POST /v1/widget/session`.

```ts
import { Socket, Presence } from "phoenix";

// GATEWAY_URL = "wss://gateway.opendesk.sh/socket" (self-host: "wss://<domain>/socket")

// Agents: ws token, origin checked against CHECK_ORIGINS
const socket = new Socket(GATEWAY_URL, {
  // A function, so every reconnect sends a token fetched just before it.
  params: () => ({ token: currentToken }),
});
socket.connect();

socket.channel(`workspace:${wid}`).on("event", applyEnvelope);       // every event of the workspace
socket.channel(`user:${userId}`).on("event", applyEnvelope);         // notifications for me
const ticket = socket.channel(`ticket:${wid}:${ticketId}`);           // while a ticket is open
const presence = new Presence(ticket);                                // presence_state / presence_diff
presence.onSync(() => presence.list((userId, { metas }) => metas[0])); // {user_id, name, avatar, state, online_at}
ticket.on("typing", ({ user_id, name }) => {});                       // another agent typing
ticket.on("customer_typing", ({ ticket_id, contact_id }) => {});      // the customer typing
ticket.push("presence", { state: "replying" });                       // "viewing" | "replying" | "idle"
ticket.push("typing", {});                                            // the other agents on the ticket
// On a chat, the visitor sees it through the API: POST /v1/tickets/{id}/typing

// Widget: widget_ws token, any origin, its own contact topic only
const widget = new Socket(GATEWAY_URL + "/widget", { params: () => ({ token: widgetToken }) });
widget.connect();                                                     // -> /socket/widget/websocket
const chat = widget.channel(`contact:${wid}:${contactId}`);
chat.on("event", applyEnvelope);                                      // public events only
chat.on("typing", ({ ticket_id, user: { id, name } }) => {});         // an agent typing
// The visitor typing: POST /v1/widget/conversations/{id}/typing (the API checks it is theirs)
```

- Joins reply `{server_time}`; unauthorized joins reply `{reason: "forbidden"}`. Ids in
  topics are lowercase UUIDs.
- `"event"` is the envelope from `contracts/events.md` without `topics`. Track the last
  `seq` per workspace; on reconnect, or when `seq` is not `last + 1`, call
  `GET /v1/events?since=<last>` first.
- Presence and typing pushes share a budget of 10 per second per socket (burst 20). Over it,
  the first push gets `{reason: "rate_limited"}` and later ones are dropped silently, so send
  `typing` at most every couple of seconds while the user types.
- On logout or revocation the server closes the socket; the client's reconnect then needs a
  new token, which the API refuses for a revoked session. A token fetched before the revocation
  is refused too.
- An agents' socket closes with code 1001 at its maximum age (`MAX_SOCKET_AGE`); the client
  reconnects with a fresh token, as after any drop.

## How it works

**Two sockets.** `/socket` (`UserSocket`) is the agents' socket: `ws` tokens only, the
`CHECK_ORIGINS` check, topics `workspace:`, `user:`, `ticket:`. `/socket/widget`
(`WidgetSocket`) is the chat widget's, embedded on customer sites: any Origin, `widget_ws`
tokens only, and only `contact:` topics are routed (the channel then requires
`contact:<wid>:<sub>` of the token). A token on the wrong socket is refused with 403. Both
share serializer, frame cap, connect throttle and socket caps (`RealtimeWeb.SocketAuth`).

**Auth.** Each socket's `connect/3` verifies the `token` param: HS256 only (`alg: none`,
HS384/HS512 and asymmetric algs are refused), `exp` required and in the future, `nbf`
honoured, `purpose` `ws` or `widget_ws`, UUID `sub` and `wid`. The token itself is not kept:
assigns are `kind` (`:user`, `:api_key`, `:contact`), `sub`, `wid`, `role`, `name`,
`avatar`, `teams`, `brand`, `sid` and `issued_ms`. The socket id is `user_socket:<sub>`. Socket
and join logging is off, so the query string never reaches a log.

**Revocations.** Each node keeps the `od.control.disconnect` messages of the last six minutes
(the token lifetime plus a minute) in ETS (`Realtime.Revocations`), by subject for a user-wide
one and by session for a session's, with their time (`at`, or when the node received it). A
token whose `sub` or `sid` was revoked at or after its issue time (`iat_ms`, else `iat * 1000`)
is refused (`revoked`). The agents' socket also schedules its own close at its maximum age, so
the claims it holds are at most `MAX_SOCKET_AGE` old.

**Fan-out and once-only delivery.** Every node subscribes to `od.events.>` and
`od.control.disconnect` itself, with **no queue group**, and broadcasts with
`local_broadcast` to the sockets connected to that node. Each socket lives on exactly one
node, so each client receives each event exactly once, and in NATS order, which keeps
`seq` order per workspace. The alternative (a queue group plus a cluster-wide broadcast)
was rejected: consecutive events of a workspace could go to different nodes and reach
clients out of order, and a node that loses its cluster link would silently drop events for
everyone else. The outbox relay delivers at least once, so each node also remembers the
highest `seq` it broadcast per workspace and drops repeats (`Realtime.Fanout.Dedupe`).

Routing for an envelope on `od.events.<wid>` (the subject and `workspace_id` must agree):
`workspace:<wid>` always; each `user:<uid>` in `topics`; each `contact:<wid>:<cid>` in
`topics` only when `visibility` is `public` and the contact topic is in the same workspace.
`od.control.disconnect` `{user_id, session_id?, at?}` records the revocation and broadcasts
`disconnect` to `user_socket:<user_id>`, which closes every socket of that subject on that node
(each node gets the message).

**Presence and typing** between agents on a ticket are client-originated, so they use the
normal cluster-wide `Phoenix.PubSub` broadcast and `Phoenix.Presence` (CRDT, replicated across
nodes). Typing between an agent and a visitor goes through the API, which checks the ticket, and
arrives on `od.live.<wid>` (`agent.typing`, `customer.typing`); the gateway pushes it to the one
topic it is for, filtered by brand or team like any live event.

**NATS connection.** `Realtime.Nats.Consumer` owns the `gnat` connection, linked with exits
trapped: when NATS is down the gateway stays up and keeps serving sockets while it
reconnects with exponential backoff (0.5 s doubling to 30 s, with jitter) and subscribes
again. Messages are handled one at a time in that process.

**Clustering.** With `CLUSTER_DNS` set, `libcluster`'s `DNSPoll` connects nodes named
`realtime@<ip>`; `rel/env.sh.eex` names each node from its task IP and pins the distribution
port range. Allow TCP 4369 and 9100-9155 between gateway tasks only (security group). Distribution
traffic is not encrypted, so keep it inside the VPC. A single node needs nothing.

**Limits.** Concurrent sockets are counted with `:pg` groups (per subject, per IP),
replicated across the cluster and cleaned up when a socket process exits. Connects per IP
(2/s, burst 30), joins per socket (10/s, burst 50) and pushes per socket (10/s, burst 20)
are token buckets in ETS. WebSocket frames are capped at 16 KB.

**Ops.** `GET /health` returns `200 ok`. Logs are JSON in prod with an allowlist of
metadata, and never include tokens, payloads or message content. With `POSTHOG_KEY`, a
`realtime_metrics` event per node per minute carries counters only (sockets open,
connects and rejections by reason, events, duplicates, NATS state), and crashes are
reported by exception type and stack frames only, never messages or state.

## Tests

- `mix test`: auth (purpose, algs, expiry, claims, short secret, prod boot config), Origin,
  joins for every topic, fan-out rules, dedupe, presence, typing, rate limits, socket caps,
  disconnect, revocations and the socket age. NATS is injected: `Realtime.Fanout.handle/3` takes the subject and body.
- `NATS_TEST_URL=nats://127.0.0.1:4222 mix test --only nats`: the consumer against a real
  NATS server.
- `test/e2e/`: `node e2e.mjs` drives a running gateway with the `phoenix` client against the
  Go API's outbox and embedded NATS (see the header of the file for its environment; set
  `GATEWAY_URL_B` to a second clustered node to check cross-node behaviour). `caps.mjs`
  checks the per-subject socket cap.
