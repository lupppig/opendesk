# Teams, brands and routing

How work is split between teams, how customers reach them, and what customers see.
Schema: `migrations/000621_teams_brands.up.sql`. Owners: teams, visibility and
routing are backend-helpdesk and backend-core; addresses are backend-email; brands,
widgets and the help center are backend-helpdesk.

- A **brand** is what customers see: a name, colour, logo, a help center and any number of
  **chat widgets** (one per website), plus the **addresses** (mailboxes) that belong to it.
  Every workspace has exactly one default brand, which cannot be deleted.
- A **team** owns work: members, an optional private flag, and its own signature, business
  hours, SLA policies, saved replies, views and assignment method.
- A **ticket** has one brand, at most one team, at most one widget, the address it arrived at
  and the address its replies go out from.

The web app hides what a workspace does not use: team pickers, chips and folders appear
once the workspace has a team, brand pickers and chips once it has a second brand.

## Resource shapes

```ts
type TeamRole = "lead" | "member" | "observer";
type Team = {
  id: string; name: string; description: string /* up to 280 */; color: LabelColor /* default "gray" */;
  private: boolean;
  members: { user_id: string; role: TeamRole }[];
  member_ids: string[] /* every member, any role; kept for older clients */;
  signature_html: string /* design allowlist, takes template variables (contracts/templates.md); "" = the address's signature */;
  business_hours: (BusinessHours & { timezone: string /* IANA, "" = the workspace's */ }) | null /* null = the workspace's */;
  assign_mode: AssignMode | null /* the assignment rule for this team; null = the workspace default */;
  mailbox_ids: string[] /* addresses whose default team this is (Mailbox.team_id) */;
  open_count: number; unassigned_count: number /* open + pending */;
  created_at: string; updated_at: string;
};

type Brand = {
  id: string; name: string /* 1..60 */; slug: string /* help center path, 3..40 [a-z0-9-] */; is_default: boolean;
  color: string /* "#rrggbb" or "" = the workspace brand_color */; locale: string /* BCP 47 or "" */;
  help_center: {
    enabled: boolean; title: string;
    url: string /* where the help center is: the active custom domain, else default_url */;
    default_url: string /* https://<slug>.<HELP_DOMAIN>, or {base}/help/<slug> without HELP_DOMAIN (see "Help center addresses") */;
    domain: HelpDomain | null /* its custom domain, "Help center addresses" */;
  };
  images: { logo: string | null; help_center_logo: string | null; help_center_favicon: string | null };
  default_mailbox_id: string | null /* the brand's oldest active address */;
  mailbox_count: number; widget_count: number; position: number; created_at: string; updated_at: string;
};

type Widget = {
  id: string; brand_id: string; name: string /* 1..60, e.g. the website */; key: string /* wk_..., public */;
  team_id: string | null /* new conversations go to this team */;
  bot_id: string | null /* the bot the AI agent answers as here (contracts/bots.md); null = the default bot; PATCH checks it is a bot of the workspace */;
  settings: WidgetSettings /* contracts/api.md, plus the members below */;
  images: { avatar: string | null; launcher: string | null };
  install: { snippet: string };
  seen_at: string | null /* first visitor session */; position: number; created_at: string; updated_at: string;
};
// Added to WidgetSettings:
//   topics: { id: string; label: string /* 2..40 */; team_id: string | null }[]  at most 8. When not empty
//     the widget asks "What can we help with?" before the first message; the pick routes the conversation.
//   offline_message: string  up to 300; shown while the routed team (else the workspace) is closed. "" = the translated default.
//   show_team: true now shows the widget team's members (available first), else any members.
//   email_follow_up: boolean  default true. A visitor who left an email and is not on the page gets agents' replies by email
//     from the conversation's reply address ("Chat follow-up" below).

type Mailbox = Mailbox /* contracts/api.md */ & {
  brand_id: string;
  aliases: string[] /* at most 20 addresses that deliver into this mailbox */;
  alias_teams: Record<string, string> /* an alias to the team its new tickets go to instead of team_id; none on a private team's address */;
  alias_settings: Record<string, AliasSettings> /* every alias's own name, signature, brand and send_as ("Aliases"; contracts/api.md) */;
  from_name_format: string /* up to 80; {agent} {agent_first} {team} {brand} {mailbox}; default "{agent} at {mailbox}" */;
  private: boolean /* its team is private: only that team's members send from it */;
};

type Ticket = Ticket /* contracts/api.md */ & {
  brand_id: string; widget_id: string | null;
  origin_mailbox_id: string | null /* where the first message arrived */;
  received_at: string /* the exact address the customer wrote to (an alias or the mailbox address); "" otherwise */;
  page_url: string /* chat: the page the conversation started on */;
  routed: { via: "address" | "widget" | "topic" | "rule" | "triage" | "automation" | "agent" | "api";
            rule_id?: string; rule_name?: string; by?: UserRef;
            topic_id?: string; topic_label?: string /* via "topic": the widget topic the visitor picked */ } | null;
  // mailbox_id (existing) is the address replies go out from now.
};

type Message = Message /* contracts/api.md */ & { mailbox_id: string | null /* sent from (outbound) or received at (inbound) */ };

type RoutingRule = {
  id: string; name: string; enabled: boolean; position: number; run_count: number;
  match: "all" | "any"; conditions: RoutingCondition[] /* 1..20 */; team_id: string;
  created_at: string; updated_at: string;
};
type RoutingCondition = {
  field: "received_at" | "mailbox_id" | "brand_id" | "widget_id" | "channel" | "from_email" | "from_domain"
       | "subject" | "body" | "page_url" | "topic" | "contact.field.<key>" | "ai_decide";
  op: "equals" | "not_equals" | "contains" | "not_contains" | "starts_with" | "gt" | "lt";
  value: string; question?: string /* ai_decide, as in automations */;
};

type View = View & { team_id: string | null /* shared with this team's members; shared must be false */ };
type SavedReply = SavedReply & { team_id: string | null /* offered only on this team's tickets; null = everyone */ };
type SlaPolicy = SlaPolicy & { team_id: string | null /* only this team's tickets; null = any */ };
type ApiKey = ApiKey & { private_team_ids: string[]; private_teams: PrivateTeamGrant[] };
type Webhook = Webhook & { private_team_ids: string[]; private_teams: PrivateTeamGrant[] };
/** The same teams as private_team_ids, ordered by name, with whose grant keeps each. */
type PrivateTeamGrant = { id: string; name: string; granted_by: UserRef | null };
type TicketCounts = TicketCounts & {
  teams: Record<string, { open: number; unassigned: number }> /* every team the caller can see */;
  my_teams: { open: number; unassigned: number } /* tickets of the caller's teams */;
};
```

## Endpoints

| method | path | notes |
| --- | --- | --- |
| GET | `/v1/teams` · `/v1/teams/{id}` | members (and keys with `settings:read`). Private teams are listed for everyone (names are not secret); `members` of a private team are listed only to its members and admins |
| POST | `/v1/teams` | admin. `{name, description?, color?, private?, members?: {user_id, role}[], member_ids?, signature_html?, business_hours?, assign_mode?}`. `member_ids` adds them as `member` (a role in `members` wins). Members must be workspace members; viewers can only be `observer` (`422`). `signature_html` up to 64 KB, sanitized like an address signature. Audit `team.created` |
| PATCH | `/v1/teams/{id}` | admin or the team's lead (`403 forbidden` otherwise). Same body; `members`/`member_ids` replace the list; `business_hours: null` and `assign_mode: null` go back to the workspace's. Only admins change `private` (`403 forbidden`). An API key changes `private`, or the members of a private team, only when the team is in its `private_team_ids` (`403 forbidden`), and never creates a private team. A lead's change that leaves the team without a lead is `409 last_lead`. Audit `team.updated {changes, members?: {added, removed, role_changed}}`; making a team private or public is also audit-logged (`team.privacy_changed {from, to}`) |
| DELETE | `/v1/teams/{id}` | admin → 204. A private team that still has tickets (any status) needs `move_to` (`422 move_required`, the detail gives the count), and then all its tickets move, solved and closed too: its tickets never fall back to no team. An API key deletes a private team only when the team is in its `private_team_ids`. `?move_to=<team id>` (`422` if not a team of the workspace) moves its open, pending and on hold tickets, addresses, widgets (and widget topics) and routing rules there; without it they are left without a team and its routing rules are deleted. Moved tickets get `team_changed {from, to, via: "agent"}`, `ticket.updated` (and `ticket.hidden` when `move_to` is private), and are assigned again in the new team; an assignee not on a private `move_to` is unassigned. The first 500 tickets get these one by one; the rest move in one statement with the activity only. Solved and closed tickets keep no team. Its assignment rule and round robin position go with it; automations whose `assign_team` names it are kept and skip that action. Publishes `team.deleted {team_id, move_to}`; audit `team.deleted {name, move_to, tickets_moved}` |
| POST | `/v1/teams/{id}/join` | admin or owner (users only) joining as `observer` → `Team`. Joining again changes nothing. Audit `team.joined`, and a notification `team_joined {team_id, team: {id, name, private}, user: {id, name}, by}` to the team's leads |
| POST | `/v1/teams/{id}/leave` | → 204, also when not a member. The last lead of a team with other members cannot leave (`409 last_lead`). Audit `team.left` |
| CRUD | `/v1/brands`, `/v1/brands/{id}` | read: members; write: admin. Create `{name, slug?, color?, locale?, help_center?: {enabled?, title?}}` (PATCH the same and `position`). `slug` is unique across all brands (`409 slug_taken`), derived from the name when absent; reserved words answer `422 slug_reserved`. DELETE refuses the default brand (`409 default_brand`) and a brand with addresses (`409 brand_in_use`); its widgets, collections and articles (its own and those in its collections) go with it, and its tickets move to the default brand. Audit `brand.created`/`updated`/`deleted`/`default_changed`/`image_updated`/`image_removed` |
| PUT/DELETE | `/v1/brands/{id}/images/{slot}` | admin. `logo`, `help_center_logo`, `help_center_favicon` |
| POST | `/v1/brands/{id}/default` | admin → makes this the default brand |
| PUT | `/v1/brands/{id}/help-domain` | admin `{hostname}` → `Brand`: sets the help center's custom domain ("Help center addresses") and checks it right away. The same hostname again changes nothing; another hostname replaces the domain, which starts over. `422 hostname_invalid` (not a hostname, an IP address, a registrable domain such as `acme.com` rather than a subdomain, or a host of this instance or under `HELP_DOMAIN`), `409 domain_taken` (verified by another brand). Audit `brand.help_domain_set {hostname, previous}` |
| POST | `/v1/brands/{id}/help-domain/check` | admin → `Brand`: checks the domain now (a `failed` one goes back to `pending` and its schedule starts over). 6 per minute per brand (`429`). `404` without a domain |
| DELETE | `/v1/brands/{id}/help-domain` | admin → 204. The help center is again only at `default_url`, and its certificate is released. Audit `brand.help_domain_removed {hostname}` |
| GET | `/v1/widgets/summary` | members (keys: `tickets:read`) → `{data: {id, name, brand_id}[]}`: every widget, for inbox filters |
| CRUD | `/v1/widgets`, `/v1/widgets/{id}` | admin. `GET ?brand_id=`. Create `{brand_id, name, team_id?, settings?}` → `Widget & {secret?, secret_available}`: the first secret is answered only past the owner-level gate of rotating it (a user whose session verified a second factor when they have one, signed in recently); otherwise `secret` is absent, `secret_available` is `false`, and the admin rotates the secret to see one; PATCH `{name?, brand_id?, team_id?, position?, settings?}` merges `settings` keys like `/v1/widget/settings`. Deleting a widget ends its visitor sessions. Audit `widget.created`/`updated`/`deleted`/`secret_rotated`/`image_updated`/`image_removed` |
| POST | `/v1/widgets/{id}/secret/rotate` | owner-level gate (MFA and recent login) → `{secret}` once. Each widget has its own secret; a new widget answers its first secret on create (`secret`, shown once) when the creator passes the same gate |
| PUT/DELETE | `/v1/widgets/{id}/images/{slot}` | admin. `avatar`, `launcher` |
| GET/PATCH | `/v1/widget/settings` | kept: the default brand's first widget |
| CRUD | `/v1/routing-rules`, `/v1/routing-rules/{id}` | admin (keys: `settings:read` / `settings:write`). Create `{name, team_id, conditions, match?, enabled?}`, new rules go last. Audit `routing_rule.created`/`updated`/`deleted` |
| POST | `/v1/routing-rules/reorder` | admin `{ids}`: every rule of the workspace once, in the order they are tried → the rules. `routing_rule.updated` per moved rule, audit `routing_rule.reordered` |
| POST | `/v1/routing/test` | admin `{channel: "email" \| "chat", to?, from?, subject?, body?, widget_id?, topic?, page_url?}` → `{team_id, brand_id, mailbox_id, routed, steps: {rule_id, name, matched: true \| false \| "unknown"}[] , assign_mode}`: where a message like this would go, without creating anything or counting a run. `to` is a mailbox's address, one of its aliases or its forwarding address; `from` finds the contact for `contact.field.*` conditions |
| PATCH | `/v1/tickets/{id}` | also `mailbox_id` (the reply address, an address of the ticket's brand the caller may send from) and `brand_id`. Changing `team_id` may change `mailbox_id` (below). The answer is always the changed ticket; when the change left it where the caller may not see it (a private team they are not on), the response carries `OD-Ticket-Hidden: true` and clients drop it, as later reads are `404` |
| POST | `/v1/tickets/{id}/messages` | also `mailbox_id?`: send this reply from another address the caller may send from; it becomes the ticket's reply address. A reply without `mailbox_id` from a reply address the caller may not send from is `403 reply_address_forbidden`: pick another address |
| GET | `/v1/tickets` | also `brand_id`, `widget_id`, and `team_id` as a comma-separated list of team ids, `mine` (the caller's teams) and `none` (no team), matching any of them (at most 50; views' `filters.team_id` take the same) |

Public routes resolve the brand: `GET /v1/public/help-domains/tls?domain=` answers 200 when this
instance serves that hostname (an active custom domain, or `<slug>.<HELP_DOMAIN>` of a brand whose
help center is on) and 404 otherwise, for a proxy that issues certificates on demand.
`/v1/public/help/{slug}` takes a brand slug (the default brand's
slug is the workspace slug, so existing links work) and lists only that brand's collections and
articles plus shared ones. `/v1/widget/config?key=` and `/v1/widget/session` take any widget's key.
`WidgetConfig` adds `topics: {id, label}[]`, `open: boolean` (the routed team's or workspace's
business hours; always true without hours), `offline_message`, `brand: {name, color}` and
`help_center_url` (the widget's brand). A visitor's conversation list holds that brand's chats only.
With `show_team`, a widget whose team is private shows no faces: a private team's members are never
shown to visitors.

### Widget identity

A visitor signed in with `user_id` and `user_hash` is verified only by the widget whose secret signed
it: each widget session is verified, or not, with that widget's own secret, and the AI agent treats a
visitor as verified only through a verified session of the conversation's own widget. A visitor token
from another widget of the workspace carries the contact over only when it is anonymous (no
`external_id`) or this request verified the same `user_id` with this widget's secret; otherwise the
visitor starts as a new anonymous contact here. The widgets of a workspace share one `user_id`
namespace (`contacts.external_id`): every widget secret vouches for the same ids, so give each widget's
backend the same user ids only when they are the same people, and keep each secret as safe as the
others. The widget's socket token is bound to its brand (`widget_ws` `bid`): the visitor's chats of
other brands, and their live previews, never reach it (contracts/realtime.md).

## Routing

A new ticket gets its team, in order:

1. **Address**: an email gets the team of the alias it was written to (`alias_teams`), else the default
   team of the mailbox it arrived at (`routed.via = "address"`). A private team's address delivers every
   alias to that team. Its brand is the alias's (`alias_settings[alias].brand_id`), else the mailbox's,
   and `brand_id` conditions read that brand.
   **Widget**: a chat gets the topic's team (`topic`), else the widget's team (`widget`).
   **API**: `team_id` from the body (`api`), else the mailbox's team.
2. **Routing rules**, in `position` order: the first enabled rule whose conditions match moves the
   ticket to its team (`rule`, with `rule_id` and `rule_name`), whatever step 1 chose. `run_count`
   counts matches.

   Conditions compare text case-insensitively after trimming: `equals`, `not_equals`, `contains`,
   `not_contains`, `starts_with` (a value is required except for `equals`/`not_equals`). `channel`
   takes `email`, `chat`, `api` or `portal`; `mailbox_id`, `brand_id` and `widget_id` take
   `equals`/`not_equals` with an id of the workspace; `from_domain` is the sender's domain (a leading
   `@` is dropped); `received_at` is the exact address written to; `topic` is the topic id.
   `contact.field.<key>` takes the ops of the field's type, as in automations. `ai_decide` (`gt`/`lt`
   a probability, with a `question`, at most 3 per rule) needs a model call about a stored ticket, so
   it is not answered while routing: a rule whose outcome depends on it does not match, and the test
   reports it `unknown`. Route on an AI question with a `ticket_created` automation (`ai_decide` and
   `assign_team`) instead.
3. **Triage**, when `team` is a triage field and the ticket still has no team (`triage`).
4. Automations run after routing and may still `assign_team` (`automation`).

An email's `received_at` is the first of the envelope recipient, `Delivered-To`, `X-Original-To`,
`To` and `Cc` that is the mailbox's address or one of its aliases, else the mailbox's address (a
forwarding address never is: it stands for the mailbox).

The team's assignment method then picks an assignee (contracts/api.md "Assignment"): the pool is the
team's `lead` and `member` users, never `observer`s or viewers. `team_changed` activities carry
`{from, to, via}`. A reply on an existing ticket threads into it wherever it was sent and never
re-routes; the message records `mailbox_id` and its own `received_at`, and the ticket's `received_at`
stays the first address. Ticket lists and views filter on the ticket's `received_at`.

## Reply address

The reply address is `ticket.mailbox_id`:

- A new email ticket replies from the address it arrived at. When it arrived through an alias,
  `received_at` is the alias and every email on the ticket goes out as the alias ("Aliases"): its
  name and signature, From the alias where the provider sends as it, else From the mailbox address
  with the alias as Reply-To. A forwarding mailbox's Reply-To stays its forwarding address, which
  keeps the thread.
- A chat or API ticket with no address takes, when it first needs one, the brand's address whose
  default team is the ticket's team, else the brand's oldest active address that is not another
  private team's. Changing the brand picks the same way (or none).
- **Moving a ticket to another team**: when the current address's default team is the old team (or
  none) and the brand has an address whose default team is the new team, the reply address switches
  to it. A ticket leaving a private team also leaves that team's address, for the new team's or
  else the brand's (as above), when the brand has one. The `team_changed` activity records
  `mailbox_from`/`mailbox_to`. Otherwise it stays.
- An address belongs to its own brand and to each brand one of its aliases has: a ticket of an
  alias's brand keeps the address as its reply address, and agents may pick it there. Choosing an
  address for a brand (a chat or API ticket, a brand change, a team change) picks among the
  addresses whose own brand it is.
- An agent may pick any address of the ticket's brand they may send from (`PATCH mailbox_id`, or per
  reply). Addresses of a private team are only for that team's members (`403 forbidden`); a reply
  that would go out from such an address without naming another is `403 reply_address_forbidden`.
- Threading headers (`In-Reply-To`, `References`, the `[#n]` subject tag) are kept when the address
  changes, so the customer's thread stays together. A mailbox that reads its inbox through Gmail or
  Microsoft Graph (`oauth_grant: "api"`, contracts/api.md) answers a conversation it read with
  `Re: <the customer's subject>` and no tag, since Gmail and Outlook keep a reply in the conversation
  only when the subject matches; the headers still thread it into the ticket.
- A reply goes out from the message's `mailbox_id` when set, else the ticket's, else the address picked
  as for a chat or API ticket (above); an email never goes out from another private team's address.
  After sending, the message records the mailbox, From address and From name it actually used; a
  ticket with no reply address takes the one its first email went out from.

What the customer sees on every email (replies, auto-replies, CSAT):
- **From**: `from_name_format` filled in, at the reply address. `{agent}` is the author's full name,
  `{agent_first}` the first name, `{team}` the ticket's team (the brand when there is none), `{brand}`
  the brand, `{mailbox}` the alias's `display_name` for mail as an alias that has one, else the
  address's `display_name` (else `name`). Without an author (automations,
  auto-replies, CSAT) the agent variable and the words between it and the next variable are dropped:
  `"{agent} at {mailbox}"` reads `"<mailbox>"`, `"{agent_first} from {team}"` reads `"<team>"`. A
  format with no other variable falls back to `{mailbox}`; separators left at either end are trimmed
  (`"{brand} - {agent}"` reads `"<brand>"`). The AI agent's replies use its bot's name (the mailbox's bot, contracts/bots.md) as the agent.
  Formats are checked and filled by `internal/fromname`, whose vocabulary Collab's From names share
  (contracts/collab.md "Who sends": `{agent}`, `{agent_first}`, `{mailbox}`, `{workspace}`).
- **Signature**: for mail as an alias, the alias's `signature_html`; else the team's, else the
  address's, else the author's personal signature (contracts/templates.md "Signatures"). Signatures, auto-replies and CSAT emails take every template
  variable (`{{ agent.name }}`, `{{ contact.first_name | default: "there" }}`, `{% if agent.phone %}`…,
  contracts/templates.md); `{{ team.name }}` reads the brand without a team, `{{ agent.* }}` are empty
  without an author, `{{ mailbox.email }}` is the alias when replies go out as one. Auto-replies and
  CSAT emails carry the signature too, and every email goes out in its brand's layout.
- Auto-replies are throttled per sender **and address** (4 hours), so writing to two teams gets both.
  Mail from another of the workspace's addresses opens a ticket but gets no auto-reply (contracts/api.md "Email").
- CSAT is the brand's survey email (contracts/templates.md "Email design"); the default asks "How did
  <team or brand> do?" with score buttons in the layout's accent, else the brand's colour.

In chat, the widget header shows the routed team's name under the title once a conversation has one,
with its hours when it is closed.

## Aliases

An alias is another address that delivers into a mailbox (`aliases`, at most 20). Aliases are free on
every plan and never count as mailboxes (contracts/billing.md). Each alias is its own identity
(`alias_settings`), so one mailbox can answer for several projects without another workspace:

- **Team**: `alias_teams`, as in "Routing".
- **Brand**: `brand_id`. New tickets written to the alias take it, and with it its email layout, help
  center links, CSAT survey and logo; `null` follows the mailbox's brand. A brand an alias uses cannot
  be deleted (`409 brand_in_use`), as with an address.
- **Name and signature**: `display_name` is the From name's `{mailbox}` and `{{ mailbox.name }}`;
  `signature_html` comes first in the signature order above. Both apply to every email on a ticket
  written to the alias (replies, auto-replies, CSAT), whichever From address it goes out with.
- **Sending as the alias** (`send_as`): SMTP sends From the alias. Gmail sends From an alias only
  when it is one of the account's verified send-as addresses (`send_as: "verified"`): otherwise Gmail
  would put the account's own address in its place, so the reply goes From the mailbox address with the
  alias as Reply-To. A connection that may read Gmail's send-as list (a sync grant,
  `https://mail.google.com/`, or an api grant, `gmail.modify`) reads it on connecting and reconnecting,
  on `POST /v1/mailboxes/{id}/send-as/check`, and about once a day while the mailbox syncs, and every
  alias takes `verified` or `unverified` from it (`send_as_by: "provider"`). A send-only connection
  (`gmail.send`) cannot read the list: an admin sets `send_as: "verified"` once Gmail sends as the
  address (Gmail Settings > Accounts > Send mail as), `send_as_by: "admin"`. Microsoft sends From an
  alias where the tenant allows it: every alias but an `unverified` one is tried, and a refusal
  (`ErrorSendAsDenied`) marks the alias `unverified` (`send_as_by: "provider"`) and sends that email
  once more From the mailbox address with the alias as Reply-To; later emails go that way at once. An
  admin sets it back to `unknown` to try again. The platform sender always sends From its own address
  with the alias as Reply-To. A connection to another account or provider, or none, sets every
  `send_as` the provider decided back to `unknown` (an admin's word stays). Each message records the
  From address it actually went out with.
- **Discovering aliases**: Gmail's other send-as addresses are listed in `provider_send_as.addresses`
  for an admin to add as aliases; nothing is added on its own, and the usual alias rules apply.
- **Working an alias as its own inbox**: ticket lists and views filter on `received_at`, the inbox's
  address filter lists each mailbox's aliases under it, and a ticket shows the address it was written to.

## Chat follow-up

When an agent (not Ollie, not an automation) posts a public reply on a chat, the visitor gets it by
email too when all of these hold:

- the widget's `email_follow_up` is on (the conversation's widget; for an older chat without one, the
  default brand's first widget);
- the visitor has an email and is not blocked;
- the visitor is not on the page: none of their widget sessions made a request in the last 2 minutes
  when the reply was posted, nor any time after it until the email goes, a minute later.

The API cannot see sockets, so "on the page" is the widget's requests: every visitor request refreshes
the session's `last_seen_at` (at most once a minute), and fetching a conversation's messages and
`POST /v1/widget/conversations/{id}/seen` (which the widget sends while a conversation is on screen and
a reply arrives) refresh it at once. A visitor who only listens on the socket for longer than that
counts as away.

The email goes out like an email reply: from the ticket's reply address, else the brand's address whose
default team is the ticket's team, else the brand's oldest active address that is not another private
team's (none: no email), with the address's
`from_name_format` and the team's or address's signature. The message's `delivery_status` becomes
`pending`, then `sent` or `failed`, and it records its `mailbox_id`, From address and name; a chat with
no reply address takes that one. Follow-ups thread with each other (`In-Reply-To`, `References`, the
`[#n]` subject tag), and the visitor's email answer threads into the chat, where the widget shows it.

## Visibility

A ticket in a **private** team is visible only to:
- the team's `lead`s, `member`s and `observer`s;
- its assignee (who must be on the team: assigning someone else answers `422 not_on_team`);
- API keys and webhooks whose `private_team_ids` include the team. Only a member of the team can add
  it to a key or webhook (`403 forbidden` otherwise); audit `api_key.updated` / `webhook.updated`.
  A webhook granted private teams sends their tickets to its URL, so changing its `url`, adding to its
  `events` or turning it back on also needs a user on every one of those teams (`403 forbidden`).
  A team that becomes public, or is deleted, leaves every key's and webhook's `private_team_ids`:
  making it private again never restores their access.
  Each team of the list is a grant by one lead, member or observer of it (`private_teams[].granted_by`),
  and lasts only while they are on the team. Creating a key or webhook, or a PATCH that sets
  `private_team_ids`, makes the caller the granter of every listed team they are on, taking over a
  grant someone else made; teams they are not on keep their granter. When the granter leaves the
  team, is removed from it or from the workspace, or deletes their account, the team leaves the key's
  or webhook's `private_team_ids` in the same change: audit `api_key.updated` / `webhook.updated
  {private_team_ids, removed_private_team_ids, reason: "granter_left_team"}`, `webhook.updated` is
  published, and the key's sockets close. A role change within the team keeps the grant.

Admins and owners are not exempt: they see a private team's tickets after `POST /v1/teams/{id}/join`.

A private team's tickets are assigned only to its members. Someone who leaves the team, is removed
from it or from the workspace, or is not on it when it becomes private, is unassigned from its open,
pending and on hold tickets (an `assigned {from, to: null}` activity, `ticket.updated`, and the team
assigns them again).

Everyone else gets `404 not_found` for the ticket, its messages, activities, attachments and links,
and it is left out of lists, `counts.views`, search, a contact's and company's tickets, the assistant
and MCP tools, AI context (similar tickets, ticket embeddings for answers on other tickets) and
notifications (a mention of someone who cannot see the ticket notifies nobody). Reports count it in
totals and by team, with no subjects or contacts. The totals of `/v1/tickets/counts` and `teams[].open_count` still count it; `counts.teams` lists only
the teams the caller can see. A notification about a ticket the user can no longer see lists
`ticket: null` and keeps only who acted in `data`, and its push carries no ticket or subject. A
ticket merged into another in a private team (other than its own) records `merged {into: null,
into_number: null}`. Ollie's drafts use only the saved replies the composer offers on that ticket,
and the assistant's workload counts only tickets the caller sees.

Apply it through `internal/domain`: `LoadVisibleTicket` or `CanSee` for one ticket, and for lists
`HiddenTeams(p)` bound into `VisibleSQL(alias, n)` (or `VisibleSQLUser`, which also lets the assignee
see it). A ticket the principal may not see answers like a missing one.

**Moving tickets in and out**: making a team private, or moving a ticket into a private team, sends
`ticket.hidden {ticket_id, team_id}` on `workspace:<wid>` (no other data) so other clients drop it;
moving it out or making the team public sends `ticket.updated` there again. Making a team private or
public sends these for its open, pending and on hold tickets only while there are at most 100; a team
with more sends none, and clients refetch their lists on `team.updated` when `private` changed.
Moving a ticket into a private team unassigns an assignee who is not on it (naming that assignee in
the same request is `422 not_on_team`).

**Realtime**: events about a ticket in a private team are published with that team
(`events.Event.TicketID` looks it up, or `TeamID`; without either, `events.Publish` finds the ticket
in the data: a `ticket_id`, or a ticket, message, activity or notification. Only `ticket.hidden`
opts out). The gateway sends the envelope to
`team:<wid>:<team_id>`, and to `workspace:<wid>` only a stand-in `{id, seq, workspace_id,
type: "event.hidden", visibility: "agents", data: {}, occurred_at}` so `seq` stays contiguous for
everyone (`user:` and `contact:` topics are unaffected). Clients track `seq` on the workspace channel
and apply team envelopes whenever they arrive (they may come before or after their stand-in). The `ws` token carries `teams`:
the private teams the caller may see. Joining `team:<wid>:<tid>` needs `tid` in `teams`. A change to
someone's private teams (membership, join, leave, privacy) publishes `od.control.disconnect` for them
so they reconnect with a fresh token. `GET /v1/events` answers the stand-in for events of private teams the
caller cannot see; webhooks receive them only with the team in `private_team_ids`. On `ticket:<wid>:<tid>`
(Presence, typing, Ollie's live previews) clients join a private team's ticket with `{team_id}`, and the
gateway pushes what is scoped to that team only to sockets that have it (contracts/realtime.md).

## Settings scoped to a team

- **Business hours**: a team's own `business_hours`, else the workspace's. Used for SLA timers on its
  tickets, the widget's `open`, and AI email answers' "outside hours".
- **SLA**: the first enabled policy, in position order, whose `team_id` is the ticket's team or null
  and whose `priority` matches.
- **Saved replies**: the composer (and Ollie's drafts) offer replies with no team and those of the ticket's team. A
  private team's replies are listed only to its members. A team's replies are written by admins and
  the team's leads and members.
- **Views**: `team_id` shares a view with that team's members; admins and the team's leads create them.
  A team view is never `shared` (both is `422`). Members other than leads read it but do not change it.
- **Assignment**: `assign_mode` is the team's rule in `/v1/assignment`.

## Help center addresses

Each brand's help center is a website of its own, rendered by the API binary (`internal/helpdesk/helpsite`)
so search engines and link previews read it.

- **Default address.** With `HELP_DOMAIN` set (opendesk Cloud: `opendeskhelp.com`) it is
  `https://<slug>.<HELP_DOMAIN>`; without it, `{base}/help/<slug>`, where `{base}` is `APP_URL` when
  the binary serves the web app (`SERVE_WEB`) and `API_URL` otherwise. Changing the slug moves it.
- **Custom domain.** A brand may add one subdomain it controls, such as `help.acme.com`. Once it
  is `active`, it is the help center's `url` and the default address answers `301` to it.
- **Every plan**, with no limit beyond one domain per brand (contracts/billing.md).

```ts
type HelpDomain = {
  hostname: string /* lowercase, ASCII (IDNA) */;
  status: "pending" /* waiting for its DNS records or certificate */ | "active" | "failed" /* gave up after 72 hours */;
  problem: "" | "txt_missing" | "cname_missing" | "domain_taken" | "certificate_pending" | "certificate_failed" | "provider_error";
  records: { type: "TXT" | "CNAME"; name: string; value: string; ok: boolean /* at the last check */ }[];
  checked_at: string | null; verified_at: string | null /* both records were found */; created_at: string;
};
```

`records` are what the customer adds at their DNS provider:

| type | name | value |
| --- | --- | --- |
| `TXT` | `_opendesk-verify.<hostname>` | `opendesk-verify=<token>`, a token of this domain alone |
| `CNAME` | `<hostname>` | `HELP_CNAME_TARGET`: `custom.<HELP_DOMAIN>` with Cloudflare, else the host of `APP_URL` |

The TXT record proves the domain is the customer's: a CNAME left pointing here after a domain is
removed never lets another workspace take it, because the new claim gets a new token. A CNAME also
passes when the hostname resolves to the same addresses as the target (flattened records). Once
verified the TXT record is no longer read.

**Checks.** A River job (`help_domain.check`) checks a pending domain right away, then every 2
minutes for an hour, every 15 minutes for a day, and hourly until 72 hours, after which it is
`failed` (POST `.../check` starts over). With both records found the domain is verified: at most one
brand holds a verified hostname, so a second claim answers `domain_taken`. Then:

- with Cloudflare for SaaS (`CLOUDFLARE_SAAS_ZONE_ID` and `CLOUDFLARE_SAAS_TOKEN`), the hostname is
  added to the zone as a custom hostname with an HTTP-validated certificate; the domain is
  `certificate_pending` until Cloudflare reports the hostname and certificate active, then `active`;
- without them, the domain is `active` at once and the instance's proxy issues the certificate on the
  first visit (Caddy on-demand TLS asking `/v1/public/help-domains/tls`).

An active domain is checked every 6 hours: a CNAME that no longer points here, or a certificate
Cloudflare could not renew, sets `problem` while it stays `active`. Removing a domain or deleting its
brand releases the Cloudflare hostname (`help_domain.release`), and a daily sweep releases any the
zone holds that no domain does. Every status change publishes `brand.updated`.

**Serving.** The help center answers on its default address and its active domain:

| path | |
| --- | --- |
| `/` | title, search, topics (collections) and popular articles |
| `/c/<collection slug>` · `/a/<article slug>` | a topic, an article (with the rest of its topic) |
| `/search?q=` | up to 20 published articles |
| `/sitemap.xml` · `/robots.txt` | every published article and topic of the brand |
| `/_help/*` | the stylesheet, script and fonts |
| `POST /_help/views` | `{article_id}` from the page's script: counts an article view |

- `?lang=` picks the interface language (the SUPPORTED web locales), else the brand's locale, else the
  workspace's, else English; pages link their other languages with `hreflang`. Articles are shown as
  written.
- Pages carry a canonical URL on the help center's `url`, Open Graph and JSON-LD (`WebSite` with a
  search action, `BreadcrumbList`, `Article`), and `Cache-Control: public, max-age=60`.
- `<slug>.<HELP_DOMAIN>` and `/help/<slug>/*` on the instance's own hosts answer `301` to the help center's `url` when that
  is elsewhere (`/help/<slug>/c/x` → `/c/x`). A brand whose help center is off, or an unknown host or
  path, is a `404` page.
- Responses carry a CSP of their own: scripts and styles from the page's origin only, images over
  https, `frame-ancestors 'none'`. Article bodies are the stored sanitized HTML.
- Cloud: `*.opendeskhelp.com` and every custom hostname reach a Cloudflare Worker
  (`deploy/cloudflare/help-worker`) that caches pages and forwards to the API with `OD-Help-Host`
  (the hostname asked for), `OD-Help-Client-IP` and `OD-Help-Secret` (`HELP_PROXY_SECRET`, at least
  32 bytes). The API believes the first two only with the secret. The zone apex and `www` redirect
  to the product site.

## Events

| type | data |
| --- | --- |
| `brand.created` / `brand.updated` / `brand.deleted` | `{brand}` / `{brand_id}` |
| `widget.created` / `widget.updated` / `widget.deleted` | `{widget}` / `{widget_id}` (never the secret) |
| `routing_rule.created` / `.updated` / `.deleted` | `{routing_rule}` / `{routing_rule_id}` |
| `ticket.hidden` | `{ticket_id, team_id}` on `workspace:<wid>` only |
| `team.created` / `team.updated` | `{team}`, the full `Team`; a private team's `members` and `member_ids` are always `[]` here (members refetch it) |
| `team.deleted` | `{team_id, move_to: string \| null}` |

## Limits

Brands, widgets and help center domains are not limited by plan (contracts/billing.md: plans differ in mailboxes and
members). At most 50 brands, 50 widgets per brand, 200 teams and 200 routing rules per workspace
(`422 limit_reached`).
