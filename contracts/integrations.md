# Integrations (`internal/integrations`)

A workspace connects other products (issue trackers, docs, chat, CRM, billing, files) so
Ollie can look things up and act there: "open a Linear issue for this ticket". Every
integration is a remote **MCP server** (Model Context Protocol, `2025-11-25`); the catalog
is the vendors' own servers, and anything else connects by URL. Same code on the cloud
and on a self-host; no plan gates it.

## Connections

One connection per integration per workspace. An admin connects it once, with the
service's own sign-in (OAuth) or a pasted token; Ollie then acts through that connection,
and every change is approved by, and attributed to, the agent who asked.

```ts
interface Provider { key: string; name: string; category: "issues"|"docs"|"chat"|"crm"|"payments"|"automation"|"files";
  description: string; url: string; auth: "oauth"|"token"; token_help?: string; token_url?: string; starters: Starter[] }
interface Starter { label: string; prompt: string } // one-click asks on a ticket
interface Integration { id: string; provider: string /* catalog key or "custom" */; name: string; host: string;
  url?: string /* catalog only; custom servers show the host, since a path can carry a secret */;
  auth_type: "oauth"|"token"|"none"; status: "pending"|"connected"|"error"; enabled: boolean; last_error: string;
  icon_url: string | null /* custom servers: the server's advertised icon, else its site's; catalog products are drawn by the app */;
  tools: ToolInfo[]; starters: Starter[]; synced_at: string | null; created_at: string }
interface ToolInfo { name: string; title: string; description: string; read_only: boolean; enabled: boolean }
interface Link { id: string; ticket_id: string; integration_id: string | null; provider: string /* key, custom or link */;
  url: string; title: string; actor_type: "user"|"ai"|"api"; created_by: string | null; created_at: string }
```

| method | path | role | body → response |
| --- | --- | --- | --- |
| GET | `/v1/integrations/catalog` | viewer | → `{data: Provider[]}` |
| GET | `/v1/integrations` | viewer | → `{data: Integration[]}` |
| POST | `/v1/integrations` | admin user | `{provider, name?, url? /* custom */, auth? /* custom: oauth (default), token, none */, token?}` → 201 `{integration, authorize_url?}` |
| POST | `/v1/integrations/{id}/authorize` | admin user | → `{integration, authorize_url}` (sign in again) |
| POST | `/v1/integrations/{id}/sync` | admin user | → `Integration` (fetches the tool list again) |
| PATCH | `/v1/integrations/{id}` | admin user | `{name?, enabled?, disabled_tools? /* replaces */, token?}` → `Integration` |
| DELETE | `/v1/integrations/{id}` | admin user | → 204. Credentials are deleted; ticket links stay as plain links |
| GET | `/v1/integrations/oauth/callback` | browser | → 302 `APP_URL/settings/integrations?connected=<id>` or `?error=<code>&id=<id>` |
| GET | `/v1/integrations/oauth/client` | public | opendesk's OAuth client ID metadata document |
| GET | `/v1/tickets/{id}/links` | viewer, `tickets:read` | → `{data: Link[]}` |
| POST | `/v1/tickets/{id}/links` | agent, `tickets:write` | `{url /* https */, title?, integration_id?}` → 201 `Link` (the same URL again answers the existing link) |
| DELETE | `/v1/tickets/{id}/links/{linkId}` | agent, `tickets:write` | → 204. The item in the other product is left as it is |

- Token and open (`none`) connections are checked when created: the tool list is fetched,
  or the request fails with `422 integration_failed`. OAuth connections start `pending` and
  become `connected` on the callback.
- At most 25 connections per workspace (`409`), 30 connection attempts per workspace per hour (`429`),
  50 links per ticket.
- Callback errors: `oauth_state`, `access_denied`, `forbidden` (no longer admin), `not_found`,
  `provider_error`, `sync_failed`.
- API keys cannot connect (`403`): signing in to another service needs a person in a browser.

### Icons

A custom server's icon is looked up in a job (`integrations.icon`) after each successful
sync (connect, sign-in, sync, new token), so it never slows connecting down:

1. The icons the server advertises in its `initialize` result (`serverInfo.icons`). Only
   raster images (PNG, JPEG, GIF, WebP; never SVG), as a base64 `data:` URI or an https URL
   on the server's own registrable domain (`mcp.example.com` may use `cdn.example.com`).
   Icons for any or the light theme come first, then the smallest at least 64 px; at
   most 3 are tried. Legacy HTTP+SSE servers advertise none.
2. Else its site's icon: the server's registrable domain (`example.com`), found like a
   company logo (logo provider, apple-touch-icon, manifest, favicon).

Icons are downloaded without the connection's credentials, through the outbound URL
rules, and re-encoded as PNGs (`/v1/images`). A lookup that finds nothing keeps the
current icon. A new icon publishes `integration.updated`.

## Sign-in (OAuth 2.1 for MCP)

- Discovery: the server's `401` `WWW-Authenticate: Bearer resource_metadata=…` (or the
  well-known protected resource metadata, RFC 9728) names the authorization server;
  its metadata comes from RFC 8414 or OpenID locations. PKCE `S256` is required; a server
  without it is refused.
- Client identity, in order: an operator's pre-registered client (`MCP_<KEY>_CLIENT_ID`,
  `MCP_<KEY>_CLIENT_SECRET`), the client ID metadata document (https instances), dynamic
  client registration (RFC 7591). Slack and Box accept only a pre-registered client.
  GitHub signs in with one when it is set (a GitHub App; its secret is sent as form
  fields) and asks for a token until then: the catalog's `auth` says which.
- The flow uses an `oauth_state` JWT (`data.flow = "integration"`, with the integration id and the
  sealed PKCE verifier) and an `od_integration_oauth` HttpOnly cookie (Path `/v1/integrations/oauth`)
  binding it to the browser. The callback checks the cookie and that the user is still an admin.
- Requests carry `resource` (RFC 8707). Access tokens refresh under a row lock; a refused
  refresh marks the connection `error` ("Reconnect the integration").

## Security

- Every server URL and every endpoint a server names (metadata, authorize, token,
  registration, legacy message endpoints on the same origin only) passes the outbound URL
  rules: https, public addresses only, re-checked at connect time, no redirects, no proxy.
  Development instances also allow loopback.
- Tokens, refresh tokens and client secrets are sealed with `d.Box`; they never appear in
  API answers, events, logs or audit entries. Audit and logs name the host, never the URL.
- Audit: `integration.connected`, `integration.updated`, `integration.disconnected`.
- Responses are capped (4 MiB per message, 200 tools, 32 KiB per tool schema); descriptions
  are clipped.

## Ollie

Connected tools are offered to Ollie (agent role and up) as `<slug>__<tool>` (at most 64
characters), with the integration's name leading the description; at most 120 per run.
Tools turned off in settings are neither offered nor run.

- A tool its server marks `readOnlyHint` runs as a read. Everything else is a write: it is
  proposed with a `Preview` (`system` names the integration, `changes` lists the arguments,
  long text in `body`) and runs only after the user approves.
- Results and errors from a server reach the model inside `<tool_output>` data blocks.
- A write made from a ticket (the page context) whose name reads as creating something,
  and whose result has a link on the provider's hosts, is linked back to the ticket
  (`POST /v1/tickets/{id}/links`, attributed `ai` for the user). The step's `refs` carry
  `{kind: "external", id: <url>}`.

## Events

| type | data |
| --- | --- |
| `integration.created` / `integration.updated` | `{integration: Integration}` |
| `integration.deleted` | `{integration_id}` |
| `ticket_link.created` | `{ticket_id, link: Link}` |
| `ticket_link.deleted` | `{ticket_id, link_id}` |

Activities: `linked{link_id, url, title, provider}`, `unlinked{url, title, provider}`.
