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
interface ToolInfo { name: string; title: string; description: string;
  read_only: boolean /* runs without approval: read_only_hint and not ask_first */;
  read_only_hint: boolean /* the server marks it read-only (its readOnlyHint) */;
  ask_first: boolean /* an admin set it to wait for approval */;
  auto_run: boolean /* a write that runs without the user's approval: on auto_tools, not read-only, not ask_first */; enabled: boolean }
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
| PATCH | `/v1/integrations/{id}` | admin user | `{name?, enabled?, disabled_tools? /* replaces */, ask_tools? /* replaces */, auto_tools? /* replaces */, token?}` → `Integration` |
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
  its metadata comes from RFC 8414 or OpenID locations, and counts only when its `issuer` is the
  one it was fetched for (a trailing slash aside; RFC 8414 3.3). PKCE `S256` is required; a server
  without it is refused.
- Client identity, in order: an operator's pre-registered client (`MCP_<KEY>_CLIENT_ID`,
  `MCP_<KEY>_CLIENT_SECRET`), the client ID metadata document (https instances), dynamic
  client registration (RFC 7591). Slack and Box accept only a pre-registered client.
  GitHub signs in with one when it is set (a GitHub App; its secret is sent as form
  fields) and asks for a token until then: the catalog's `auth` says which.
- The flow uses an `oauth_state` JWT (`data.flow = "integration"`, with the integration id and the
  sealed PKCE verifier) and an `od_integration_oauth` HttpOnly cookie (Path `/v1/integrations/oauth`)
  binding it to the browser. The callback checks the cookie and that the user is still an admin.
  A callback carrying `iss` (RFC 9207) must name the connection's issuer, and one from a server whose
  metadata says `authorization_response_iss_parameter_supported` must carry it; otherwise the code
  is never exchanged (`?error=oauth_state`).
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
Tools turned off in settings are neither offered nor run. `disabled_tools`, `ask_tools` and
`auto_tools` keep only names of the connection's tools, and outlast a sync.

- A tool its server marks `readOnlyHint` runs as a read, unless an admin set it to ask first
  (`ask_tools`; the server's hint stays visible as `read_only_hint`). Everything else is a
  write: it is proposed with a `Preview` (`system` names the integration, `changes` lists up
  to 12 arguments, long text in `body`) and runs only after the user approves. The preview's
  `arguments` is the complete call as it is sent, indented; `omitted` counts the arguments
  `changes` and `body` leave out and `clipped` marks a value they cut short.
- Arguments are a JSON object of at most 64 KB that names each key once. They are stored
  as their own text, so the approved call sends exactly what the preview showed.
- Whether a tool may run without approval is checked again when it runs: a call made as a
  read is refused once the tool is turned off or set to ask first.
- Runs without asking: a write on `auto_tools` runs as soon as Ollie proposes it, on the path of an
  approved change (`contracts/ai.md` "Approvals": an `assistant_actions` row with `auto`, the
  `assistant.action` audit entry with `data.auto = true`). Only a write counts: a tool that is
  read-only needs no approval anyway, and `ask_tools` wins over `auto_tools` for a tool on both.
  A change whose preview reaches someone outside (`external`) still asks. Whether it may run so is
  checked again just before it runs, and the call itself is refused once it no longer may.
- Results and errors from a server reach the model inside `<tool_output>` data blocks.
- A write made from a ticket (the page context) whose name reads as creating something,
  and whose result has a link on the provider's hosts, is linked back to the ticket
  (`POST /v1/tickets/{id}/links`, attributed `ai` for the user). The step's `refs` carry
  `{kind: "external", id: <url>}`.
- A procedure an automation runs (`contracts/ai.md` "Automations") calls reads, and writes on
  `auto_tools` only (`CallAuto`); a create-style write's link on the provider's hosts is linked to its
  ticket the same way, `actor_type: "ai"` with no user.

## Events

| type | data |
| --- | --- |
| `integration.created` / `integration.updated` | `{integration: Integration}` |
| `integration.deleted` | `{integration_id}` |
| `ticket_link.created` | `{ticket_id, link: Link}` |
| `ticket_link.deleted` | `{ticket_id, link_id}` |

Activities: `linked{link_id, url, title, provider}`, `unlinked{url, title, provider}`.

## Ticket panels (`internal/integrations/panels`)

A ticket panel shows, beside the ticket, what another product knows about the ticket's
contact: read-only, fetched when the ticket is open, never blocking it. It is not an MCP
connection and gives Ollie no tool. Two kinds:

- `shopify`: the contact's customer and last orders in a Shopify store, found by the
  contact's email (Admin GraphQL API `2026-10`). The admin creates an app in Shopify's Dev
  Dashboard with the `read_customers` and `read_orders` scopes (`read_all_orders` to see orders
  older than 60 days), installs it on the shop and pastes its client ID and secret. An app made
  in Shopify admin before 2026 (a "legacy custom app") can instead paste its Admin API access
  token: Shopify no longer lets anyone create those.
- `http`: a URL of the workspace's own (an ERP, billing, a CRM). OpenDesk posts the ticket, its
  contact and the order or invoice numbers it finds in the ticket to it, and shows the blocks it
  answers: cards, labelled values, lists and notes, with dates, money and badges the app formats.
  The workspace's code decides what to show; OpenDesk learns no product.

```ts
interface TicketPanel { id: string; kind: "shopify" | "http"; name: string;
  host: string /* shopify: the shop's myshopify.com domain; http: the URL's host only, since a path can carry a secret */;
  auth?: "client_credentials" | "access_token" /* shopify: how OpenDesk gets its Admin API token */;
  client_id?: string /* shopify client_credentials: the app's client ID, not a secret */;
  fields: ShopifyField[] /* shopify: what tickets show, in catalogue order; the defaults until an admin chooses. http: [] */;
  orders_shown?: number /* shopify: how many of the last orders show, 1 to 5, default 5 */;
  reference_pattern?: string /* http: the RE2 pattern that finds ticket.references ("The http request"); "" finds none */;
  enabled: boolean; position: number; status: "connected" | "error" /* error: the last lookup found the credentials refused */;
  last_error: string; created_at: string; updated_at: string }
interface TicketPanelData { panel_id: string; kind: "shopify" | "http";
  state: "ok" | "empty" /* nothing about this contact */ | "no_contact" /* shopify: the contact has no email */ | "error";
  message?: string /* error: a few words, never a URL */; customer?: Customer; orders?: Order[] /* shopify */;
  sections?: Section[] /* http, a version 1 answer */; blocks?: Block[] /* http, a version 2 answer, cleaned ("The http answer") */;
  fetched_at: string }
/* A field the panel does not show is left out of Customer and Order, and its list is empty. */
interface Customer { name: string; url: string /* in the store's admin */; email?: string /* the store's, field email */;
  orders_count?: number; total_spent?: string /* decimal */; currency?: string /* total_spent's */; tags: string[] }
interface Order { id: string; number: string /* "#1042" */; url: string /* in the store's admin */; created_at: string;
  total: string /* decimal, after refunds */; currency: string;
  financial_status?: string /* lowercase: paid, pending, refunded, partially_refunded, … */;
  fulfillment_status?: string /* lowercase: fulfilled, unfulfilled, partially_fulfilled, … */; cancelled: boolean;
  cancelled_at?: string; refunded?: string /* decimal, what was refunded; absent when nothing was */;
  discount_codes: string[];
  shipping?: { city?: string; country?: string /* ISO 3166-1 alpha-2 */; method?: string /* as the checkout named it */ }
    /* absent when the order has neither an address nor a method; never the street */;
  items: { title: string; variant?: string; quantity: number; price?: string /* unit, before discounts, decimal, in the order's currency */ }[];
  tracking: { company?: string; number?: string; url?: string /* https only */ }[] }
interface Section { title: string; items: { label: string; value: string; url?: string /* https only */ }[] }
type ShopifyField = "tags" | "email" | "orders_count" | "total_spent" /* the customer's */
  | "financial_status" | "fulfillment_status" | "line_items" | "line_prices" | "shipping" | "discounts" | "refunds" | "tracking" /* each order's */;
```

What a Shopify panel shows is the admin's choice from that catalogue. The customer's name and link,
and each order's number, date, total and cancellation, always show. Until an admin chooses, a panel
shows every field but `email` (the ticket's contact's own address) and 5 orders, as it always has;
`ticket_panels.fields` is NULL until then, and a later default applies to such panels by itself.
`line_prices` needs `line_items`. `shipping` is the destination's city and country and the
shipping method; `discounts` the discount codes; `refunds` the refunded amount; `tracking` the
fulfilments' parcels. A field left out is not asked of Shopify, nor told.

Orders and customers have the same shape whatever the store, so another commerce source plugs
into the same section.

| method | path | role | body → response |
| --- | --- | --- | --- |
| GET | `/v1/ticket-panels` | viewer, `tickets:read` | → `{data: TicketPanel[]}` in `position` order |
| POST | `/v1/ticket-panels` | admin user | `{kind, name?, shop? /* shopify: domain, handle or admin URL */, client_id?, client_secret? /* shopify: an app */, token? /* shopify: in place of the app */, fields?, orders_shown? /* shopify; 422 on http */, url?, reference_pattern? /* http; 422 on shopify */}` → 201 `{panel, secret? /* http: the signing secret, shown once */}`, `Cache-Control: no-store` |
| PATCH | `/v1/ticket-panels/{id}` | admin user | `{name? /* trimmed, 1 to 64 characters, internal/displayname's rules: 422 invalid_name */, enabled?, position?, client_secret?, client_id? /* shopify: a new secret keeps the client ID */, token? /* shopify */, fields? /* shopify: the whole list; an unknown field, or line_prices without line_items: 422 */, orders_shown? /* shopify, 1 to 5 */, url?, reference_pattern? /* http: the default pattern stores the default, "" none; 422 when it does not compile, compiles to more than 64 instructions or matches an empty text */}` → `TicketPanel` |
| POST | `/v1/ticket-panels/{id}/rotate-secret` | admin user | → `{panel, secret}` (http; the old secret stops at once) |
| DELETE | `/v1/ticket-panels/{id}` | admin user | → 204 |
| GET | `/v1/tickets/{id}/panels/{panelId}` | viewer, `tickets:read` | `?refresh=true` asks the product again; `?locale=fr`, the reader's interface language as a lowercase base language tag (`[a-z]{2,3}`, anything else ignored), is told to an http panel → `TicketPanelData`. A hidden or missing ticket, or a panel that is off or gone: `404` |

- Shopify credentials are a client ID and secret or a token, never both (`422`). They are checked
  when saved: a token is asked for with the app's (below), its `scope` must hold `read_customers`
  and `read_orders` (or their `write_` scopes), then `query { shop { name } }`; the shop's name
  becomes the panel's name unless one is given: `422 panel_failed` otherwise. An http URL passes the outbound
  URL rules: `422 invalid_url`. At most 10 panels per workspace (`409`), 30 new panels an hour (`429`).
- API keys never connect (`403`): pasting another product's credentials needs a person.
- A lookup is one request within 3 s. Its answer is kept for a minute (a failure for 15 s; an http
  answer may choose 0 to 600 s with `cache_seconds`) per panel and contact (and ticket,
  references and `locale`, for http), and a changed panel never answers from before the change; `refresh` skips
  the copy. A panel makes at most 120 lookups a minute that the copy did
  not answer. What an http request carries beyond the ticket (`references`, `emails`, `phone`) is
  read from the ticket once per version of the panel and the ticket, kept for a minute, and that
  reading counts as the lookup it leads to: a reading the copy does not keep is refused like a
  lookup when the 120 are spent, before the ticket is read. A workspace's http panels make at most
  60 such readings a minute together (a new version, an expired copy or `refresh`), counted in each
  API process; past them a reading is refused the same way, and a kept reading still answers. A
  new message, a deleted one or a note sent as a reply changes the ticket's `updated_at`, so the
  next open reads it again; a changed
  phone or an edited or restored note shows within the minute, or at once with `refresh`,
  which reads it again. A failure is `state: "error"` with a 200, never an error status: the ticket shows a
  quiet note. Refused credentials (Shopify `401`, `403` or `ACCESS_DENIED`; the URL's `401` or
  `403`) also mark the panel `error` with `last_error` and publish `ticket_panel.updated`; a
  lookup that works again, new credentials or a new URL marks it `connected`. A timeout or an outage
  leaves the status as it is.
- Shopify app tokens (the client credentials grant): `POST https://<shop>/admin/oauth/access_token`,
  form-encoded `grant_type=client_credentials`, `client_id`, `client_secret`, answers
  `{access_token, scope, expires_in}` (24 hours, no refresh token). Each API process keeps a
  panel's token in memory, asks for a new one 5 minutes before its end, and one lookup asks while
  the others wait for it. A token refused before its end (the app reinstalled) is asked for again
  once. A refused client ID or secret (`400`, `401`, `403`) or `shop_not_permitted` (Shopify gives
  these tokens only for a shop of the app's own organization) marks the panel `error`.
- Shopify: the first customer whose default email is the contact's (an exact, case-insensitive
  match of the phrase search); none is `empty`. The panel's `orders_shown` orders, newest first,
  with up to 10 lines each (unit price before discounts), their fulfilments' tracking, discount
  codes, the refunded amount (`totalRefundedSet`), the shipping method (`shippingLine.title`) and
  the destination's city and country (`shippingAddress`; never the street). One query, built from
  the panel's fields: each field adds its own part (`tags`, `numberOfOrders`, `amountSpent`,
  `displayFinancialStatus`, `displayFulfillmentStatus`, `lineItems`, `originalUnitPriceSet`,
  `shippingLine` and `shippingAddress`, `discountCodes`, `totalRefundedSet`, `fulfillments`), and
  `email` reads nothing more, since the match needs it. With every field and 5 orders its requested
  cost is about 350 points (Shopify allows 1,000 per query); lines and tracking are most of it.
  An app without access to customer addresses gets them refused field by field: the orders show
  without a destination and the panel stays `connected`. Links go to `admin.shopify.com/store/<handle>`: the customer and each order by
  the number of its global id (`gid://shopify/Order/123` → `/orders/123`).
- The integrations catalog in settings lists Shopify beside the MCP products, and a card for a
  panel from a URL: both open the panel's own dialog, since a panel is a `ticket_panels` row and
  not an integration (it gives Ollie no tool). A connected Shopify panel shows as connected there
  and leads to its row.
- In settings, each panel's menu renames it (`PATCH {name}`; an unchanged name sends nothing),
  then replaces a Shopify panel's credentials and chooses its fields, or changes an http panel's
  URL, its references and its signing secret, and removes it.

### The http request

`POST <url>` with `Content-Type: application/json`, `User-Agent: opendesk-panels/<version>`,
`OD-Panel-Id` and `OD-Signature: t=<unix>,v1=<hex>`, the HMAC-SHA256 of `"<t>.<body>"` with the
panel's secret: the scheme of outgoing webhooks (`contracts/events.md`), verified the same way.

```json
{ "panel_id": "…", "workspace_id": "…", "refresh": false, "locale": "fr",
  "ticket": { "id": "…", "number": 1042, "subject": "Where is my order S00042?",
              "references": ["S00042", "INV/2026/00012"] },
  "contact": { "id": "…", "email": "ada@example.com", "name": "Ada Buyer",
               "emails": ["ada@example.com"], "phone": "+32 470 12 34 56" } }
```

- `contact.id` is null, `email` empty and `emails` `[]` for a ticket without a contact. `emails` is
  every address the contact is known by (the contact's own first, then the other addresses it wrote
  to this ticket from, 5 at most); `phone` is left out when the contact has none.
- `ticket.references` is what the panel's `reference_pattern` finds in the ticket's subject, then
  its 50 newest messages that every reader of the ticket reads (replies and notes, never a deleted
  message or a note sent as a reply), newest first: the first 20,000 characters of each, 100,000
  in all, and the first 20 matches. Each once, at most 10 of 64 characters. A pattern with a group
  sends the group, else the whole match. Message bodies are never sent. The default, `#\d{3,}\b|\b[A-Z]{1,4}[-/]?(?:\d{2,4}[-/])?\d{3,}\b`,
  needs a `#` or 1 to 4 capitals before the digits (`#1042`, `S00042`, `SO-2026-0042`,
  `INV/2026/00012`, `PO4471`), so years, amounts, postcodes and phone numbers are not references. An
  admin sets the pattern (RE2 syntax, 200 characters at most and 64 instructions compiled, never one
  that matches an empty text: `422`); `""` sends `[]`, and saving the default stores NULL so a later
  default applies.
- `refresh` is true when an agent asked again (the panel's refresh button, `?refresh=true`).
- `locale` is the reader's interface language, a lowercase base language tag (`"en"`, `"fr"`,
  `"de"`), so the URL can write its labels and notes in it: the web app sends the language it
  shows (`?locale=`). It is left out when the reader's app does not say (an API key, an older
  app; a member has no saved language): answer in your default. Dates, money and badges need
  nothing, since the app formats them.
- Fields are only ever added: ignore the ones you don't know.

### The http answer

Version 2 answers `blocks`; an answer without `blocks` and without `version` 2 or more is version 1
and answers `sections`, shown as they always were. Both are at most 64 KB.

```ts
interface Answer { version?: 2; blocks: Block[]; cache_seconds?: number /* 0 to 600; 0: not kept */ }
type Block =
  | { type: "card"; id?: string | number /* the record across answers */; title: string; url?: string; subtitle?: string;
      badges?: Badge[]; links?: Link[]; collapsed?: boolean /* starts folded to its header */; fields?: Field[];
      lines?: Line[]; footer?: Field[] }
  | { type: "fields"; title?: string; items: Field[] }
  | { type: "list"; title?: string; items: { title: string; url?: string; subtitle?: string; value?: Value; badge?: Badge; links?: Link[] }[] }
  | { type: "note"; text: string; tone?: Tone };
interface Field { label: string; value: Value; url?: string }
interface Link { label: string /* the button's name and tooltip */; url: string /* https */; icon?: LinkIcon }
type LinkIcon = "external-link" | "store" | "shopping-cart" | "receipt" | "file-text" | "truck" | "package" | "user"
  | "building" | "phone" | "mail" | "credit-card" | "map-pin" /* generic */
  | "shopify" | "stripe" | "paypal" | "square" | "linear" | "github" | "atlassian" | "sentry" | "notion" | "asana"
  | "monday" | "slack" | "discord" | "telegram" | "intercom" | "close" | "box" | "zapier" | "zendesk" | "helpscout"
  | "freshdesk" | "odoo" /* the products whose logo the app ships */;
interface Line { quantity?: number; title: string; subtitle?: string; value?: Value; url?: string }
interface Badge { text: string; tone: Tone }
type Tone = "neutral" | "info" | "success" | "warning" | "danger";
type Value = string | number | boolean
  | { type: "text"; text: string }
  | { type: "date"; value: string /* YYYY-MM-DD, or a time: RFC 3339, or without an offset in UTC */; relative?: boolean }
  | { type: "money"; amount: string | number /* a decimal: 129.00 */; currency: string /* ISO 4217 */ }
  | { type: "badge"; text: string; tone: Tone };
```

- `card`: one record (an order, an invoice, a subscription, a deal): a title that may link, a
  subtitle, badges, labelled fields, lines (an order's products) and a footer (a total). `fields`:
  labelled values. `list`: rows (invoices, tickets, contracts). `note`: one short sentence ("No
  subscription.").
- `links` on a card or a list row: the same record in other tools (the shop, the ERP, the carrier),
  shown as small buttons after the title, which keeps its own `url`. Each button is named and
  tipped by its `label` and opens a new tab. Its `icon` is drawn by the app: a generic mark or the
  product's shipped logo; without one, or with a name this version does not know, the button shows
  the label's first letter. Apps never load a picture or favicon from the link's site.
- Every card with something under its header folds and opens: an arrow in its header (a button
  named by the card's title, with `aria-expanded`), or a click on the header outside its links and
  buttons. `collapsed: true` makes a card start folded to its header (title, links, subtitle,
  badges), so a customer with many orders reads at a glance: send the newest or the late order open
  and older ones folded. Anything but `true` starts open, and a card with nothing under its header
  has nothing to fold.
- Once the agent folds or opens a card, it stays that way for that ticket and panel while the app
  runs: a refresh, a re-fetch or a newer answer does not undo it, while a card they did not touch
  follows each answer's `collapsed`. Apps know a card by its `id` (a string or a number, up to 80
  characters; longer is dropped, never cut), or by its title without one, so send an `id` when a
  card's title can change between answers (in another `locale`, with a status in it). The choice is
  kept in memory, never stored: after a reload the answer's `collapsed` applies again.
- The app formats dates in the agent's locale and timezone (a day as it is, never shifted) and, with
  `relative`, adds "in 3 days" or "5 days ago"; money in the agent's locale; a badge's tone is the
  app's own colour (info blue, success green, warning amber, danger red).
- What is shown: 10 blocks; 30 fields in a card or a `fields` block; 20 lines; 6 footer fields; 4
  badges on a card; 5 links on a card or a row; 20 list rows. Text is plain, control characters
  dropped: titles and labels 80 characters, line and row titles and subtitles 160, values 500,
  badges and link labels 40, notes 280. Never HTML.
- What does not read is left out, never the whole answer: a block or value type this version does not
  know, a date or an amount that does not read, a currency that is not three letters, a field without
  a value, a card, line or row without a title, a link without a label or a url, an empty block. An
  unknown tone is `neutral`; an unknown link icon is left out. A `url` that is not https (http on a
  development instance) is dropped; links open in a new tab and are never fetched.
- The server cleans the answer once: `TicketPanelData.blocks` has the shape above with every value an
  object (`{type: "text", text}` for a string, number or boolean, written as given), dates as
  `YYYY-MM-DD` or an RFC 3339 time in UTC, amounts as decimal strings, currencies in capitals,
  a card's `id` as a string and every tone set. Clients only render it.
- `cache_seconds` keeps the answer that long for the panel, ticket, contact, references and `locale`
  (default 60; above 600 is 600), so a reader never gets a copy written in another language. A failure is kept 15 s. A new reference in the ticket asks again.
- Version 1: `{ sections: [{ title, items: [{ label, value, url? }] }] }`. 10 sections of 30 items;
  title and label 80 characters, value 500 (a number or boolean as written).
- `204` or `404`: nothing about this contact (`empty`); an answer whose blocks all drop is `empty`
  too. `401` or `403`: refused credentials. Any other status, a timeout or an answer that is not
  this JSON: `error`.

```json
{ "version": 2, "cache_seconds": 120, "blocks": [
  { "type": "card", "id": "so-42", "title": "Sales order S00042", "url": "https://erp.example/so/42",
    "badges": [ { "text": "Supplier late", "tone": "warning" } ],
    "links": [ { "label": "Open in the shop", "url": "https://admin.shopify.com/store/lumen/orders/5042", "icon": "shopify" } ],
    "fields": [ { "label": "Supplier date", "value": { "type": "date", "value": "2026-11-12", "relative": true } } ],
    "lines": [ { "quantity": 2, "title": "Arco floor lamp", "value": { "type": "money", "amount": "1890.00", "currency": "EUR" } } ],
    "footer": [ { "label": "Total", "value": { "type": "money", "amount": "3780.00", "currency": "EUR" } } ] },
  { "type": "card", "id": "so-35", "title": "Sales order S00035", "collapsed": true, "badges": [ { "text": "Delivered", "tone": "success" } ],
    "footer": [ { "label": "Total", "value": { "type": "money", "amount": "1150.00", "currency": "EUR" } } ] },
  { "type": "note", "text": "No open invoice." } ] }
```

### Security

- Client secrets, tokens and signing secrets are sealed with `d.Box` (`ticket_panels.secret_enc`:
  a shopify panel with a `client_id` seals the app's client secret, one without a token); Shopify's
  24-hour tokens are only kept in memory. None of them appear in
  answers, events, logs or audit entries. Requests go through the outbound URL rules at connect
  time (https, public addresses only, no redirects, no proxy; loopback on development instances).
- Audit: `ticket_panel.connected`, `ticket_panel.updated` (`changes` names the fields: `name`, `enabled`, `position`, `url`,
  `credentials` for new Shopify credentials, `secret` for a rotation, `fields`, `orders_shown`, `reference_pattern`), `ticket_panel.disconnected`,
  with the kind and host.
- The ticket is read through domain visibility: a contact on a ticket of a private team the
  reader is not on is never looked up.

### Events

| type | data |
| --- | --- |
| `ticket_panel.created` / `ticket_panel.updated` | `{panel: TicketPanel}` |
| `ticket_panel.deleted` | `{panel_id}` |
