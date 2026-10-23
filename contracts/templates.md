# Templates, signatures and email design

What every customer-facing message is written with: one template language, one list of
variables, signatures built from the team's and the agent's data, and a designed email layout
per brand. Schema: `migrations/000633_templates.up.sql`. Owners: the engine and variables are
`internal/templates` (shared); email design, previews and test sends are backend-email;
member profiles are backend-core; replies are backend-helpdesk.

## The language

Templates are HTML (or plain text, for subjects) with two kinds of tags, following Liquid.
Anything else is copied as is.

```
{{ contact.first_name }}                              a variable
{{ contact.first_name | default: "there" | upcase }}  filters, left to right (Filters below)
{% if a %}…{% elsif b %}…{% else %}…{% endif %}       conditions; also {% unless %}
{% case ticket.priority %}{% when "Urgent", "High" %}…{% else %}…{% endcase %}
{% for label in ticket.labels limit: 5 offset: 1 reversed %}…{% else %}empty{% endfor %}
{% for i in (1..3) %}…{% endfor %}                    ranges
{% assign greeting = contact.first_name | default: "there" %}
{% capture line %}…{% endcapture %}
{% comment %}…{% endcomment %}  {% raw %}{{ kept as written }}{% endraw %}
```

- Names are lower case dotted paths. Spaces inside the braces are optional. Operands are a
  variable, a quoted string (`"…"` or `'…'`), a number, `true`/`false`, `empty`/`blank`/`nil`, or a
  range `(a..b)`.
- Some variables are lists (`ticket.labels`, `contact.labels`, `company.labels`, multi-select fields;
  `split` makes one). A list written as a value reads as its items joined with `", "`.
- Conditions: a value on its own (true when not empty; a list, when it has items), comparisons
  `==`, `!=`, `>`, `<`, `>=`, `<=` (numbers compare as numbers, text ignores case) and `contains` (a list
  item, or a substring), joined with `and`/`or` (`and` binds tighter), `not`, and parentheses.
- Loops expose `forloop.index`, `index0`, `rindex`, `rindex0`, `length`, `first`, `last`. Loop names
  are known only inside their loop; `assign` and `capture` names after they are set.
- Limits: blocks nest at most 24 deep; a loop goes over at most 100 items and one fill runs at
  most 1,000 loop bodies; a fill writes at most 512 KB.
- Values are HTML-escaped in HTML templates and copied as text in subjects. Variables marked HTML
  (`agent.signature`) and slots insert sanitized HTML.
- Filling is one pass: a value is never read again as a template, so a customer named
  `{{agent.name}}` stays literally that. Values that look like tags carry a zero-width break
  between the braces, so they stay inert even when a filled reply is filled again on send.
- An unknown variable, or a tag that does not parse, is left as written when a message is filled
  (a block that reads an unknown variable is kept whole), so code samples with braces survive.
  Saving a template refuses them instead (`422 validation_failed`, with each problem in `errors`).
- Inside an attribute, use single quotes for strings: `href="{{ brand.help_center_url }}"`,
  `style="color: {{ brand.color | default: '#0A0A0A' }}"`.

### Filters

Text: `default`, `upcase`, `downcase`, `capitalize`, `titlecase`, `strip`, `truncate: n[, end]`,
`truncatewords: n[, end]`, `append`, `prepend`, `replace: a, b`, `replace_first: a, b`, `remove`,
`slice: start[, length]`, `url_encode`. Lists: `split`, `join[: sep]`, `first`, `last`, `size`, `sort`,
`uniq`. Numbers: `plus`, `minus`, `times`, `divided_by`, `round[: digits]`. Dates: `date: "<strftime>"`
(`%Y %y %m %d %e %B %b %A %a %H %I %l %M %p %Z %j`), reading `"now"`, ISO dates and dates like
"18 October 2026", in the workspace's timezone. A filter with the wrong number of values is a problem.

## Variables

`GET /v1/templates/variables` lists them, with the workspace's custom fields, so clients never
keep their own list. Every template takes every variable; a value that does not apply is empty
(use `default` or `{% if %}`). The one exception is a Collab signature (`collab_signature`, a mail
account's or address's, contracts/collab.md "Who sends"): mail from an account has no ticket,
customer, team or brand, so it takes the `agent`, `mailbox`, `workspace` and `date` groups only
(`kind_groups`), and a save that uses another is refused; editors offer only those.

| group | variables |
| --- | --- |
| contact | `contact.name`, `contact.first_name`, `contact.last_name`, `contact.email`, `contact.phone`, `contact.labels` (list), `contact.fields.<key>` |
| company | `company.name`, `company.domain`, `company.labels` (list), `company.fields.<key>` |
| ticket | `ticket.number`, `ticket.subject`, `ticket.status`, `ticket.priority`, `ticket.channel`, `ticket.created_date`, `ticket.labels` (list), `ticket.fields.<key>` |
| agent | `agent.name`, `agent.first_name`, `agent.last_name`, `agent.title`, `agent.phone`, `agent.pronouns`, `agent.avatar_url`, `agent.signature` (HTML) |
| team | `team.name`, `team.description` |
| brand | `brand.name`, `brand.color`, `brand.logo_url`, `brand.help_center_url` |
| mailbox | `mailbox.name`, `mailbox.email` |
| workspace | `workspace.name` |
| date | `date.today` |

- `agent.*` is the reply's author: the person sending it, the AI agent's bot name for Ollie's
  replies (no title or phone), and empty for automations, auto-replies and CSAT emails.
- `team.name` reads the brand when the ticket has no team (as before). `mailbox.email` is the
  alias when replies go out as one; `mailbox.name` is the alias's `display_name` for mail as an
  alias that has one (contracts/teams.md "Aliases"), else the address's `display_name`, else `name`.
- `ticket.status` and `ticket.priority` read as words ("On hold", "Urgent"); `ticket.created_date`
  and `date.today` are dates like "18 October 2026" in the workspace's timezone.
- Custom fields read as shown: a select's label, a multi select a list of its labels, a checkbox
  "Yes" or "No", a user field the member's name. Archived fields are not listed but still fill.
- `brand.color` is `#rrggbb` (the brand's, else the workspace's `brand_color`, else `#0A0A0A`).
  `brand.logo_url` and `agent.avatar_url` are public image URLs, or empty.

```ts
type TemplateVariable = {
  key: string; group: "contact" | "company" | "ticket" | "agent" | "team" | "brand" | "mailbox" | "workspace" | "date";
  label: string; description: string; example: string; html: boolean; list: boolean;
};
type TemplateFilter = { name: string; group: "text" | "list" | "number" | "date"; args: string; description: string; example: string };
type TemplateTag = { name: string; syntax: string; description: string };
type TemplateSlot = { key: "content" | "signature" | "history" | "survey"; label: string; description: string; required_in: TemplateKind[] };
type TemplateKind = "reply" | "signature" | "personal_signature" | "auto_reply" | "layout" | "csat" | "subject" | "collab_signature";
// GET /v1/templates/variables (viewer+)
type TemplateCatalog = { variables: TemplateVariable[]; slots: TemplateSlot[]; filters: TemplateFilter[]; tags: TemplateTag[];
  kind_groups: Partial<Record<TemplateKind, TemplateVariable["group"][]>> };  // the groups of kinds that take only some
```

## Where templates are filled

| where | filled when | notes |
| --- | --- | --- |
| agent replies (web, API, Ollie drafts an agent sends) | the reply is created | the stored message is what the customer got |
| saved replies | inserted in the composer (`POST /v1/tickets/{id}/render`), and again on send | the agent sees real words, not tags |
| third-party thread messages (contracts/api.md "Threads") | the message is created, and saved replies when inserted (`render` with `thread`) | without a ticket: the customer's and the ticket's variables stay empty |
| automation `send_reply` | the action runs | no agent |
| team, address and personal signatures | each email is sent | |
| Collab account and address signatures | for the member writing: in the accounts response (`filled_signature_html`) and the composer | stored as written; `agent.*` is the writer, `mailbox.*` the address and its filled From name |
| mailbox auto-reply (subject and body) | it is sent | no agent |
| CSAT email (subject and body) | it is sent | per brand, below |
| email layout | each email is sent | per brand, below |

Internal notes are never filled (a thread's messages, stored as notes, go out as email). Chat replies are filled like email replies.

## Signatures

Which signature an email carries: for mail as an alias (a ticket written to it), the alias's
`signature_html` (contracts/teams.md "Aliases"); else the ticket team's `signature_html`, else the
sending address's, else the author's personal signature. Alias, team and address signatures can place the personal one with
`{{ agent.signature }}`, so a company signature can wrap each person's own lines. Signatures take
every variable; the personal signature cannot contain `{{ agent.signature }}`.

Every signature is edited in one editor: a designed one (tables, inline styles, sized images) opens
as HTML, its tags kept as typed, and is never simplified unless the person chooses to; a save sends
it only when it changed. Wherever the app shows a signature (the reply composer's preview, the
editors' previews) it shows it as recipients get it, its layout and image sizes kept.

Each member has a profile per workspace that feeds `agent.*`:

```ts
type MemberProfile = {
  title: string /* up to 80 */; phone: string /* up to 40 */; pronouns: string /* up to 40 */;
  signature_html: string /* sanitized, up to 64 KB; "" = none */;
};
// Member gains: profile: MemberProfile
```

`PATCH /v1/members/{user_id}/profile` (`user_id` may be `me`; members edit their own, admins
anyone's) takes any subset of the fields. It publishes `member.updated`.

Signatures, auto-replies, layouts and CSAT templates are sanitized with the design allowlist
(email HTML plus tables, inline `style` with safe properties, `width`/`height`/`bgcolor`/`valign`,
`mailto:` and `tel:` links). Agent replies and saved replies keep the rich-text allowlist.

## Email design (per brand)

Every email to a customer (replies, chat follow-ups, auto-replies, CSAT) is rendered in its
brand's layout. A brand without one uses the default (plain, no logo, the previous message quoted).

```ts
type EmailLayoutOptions = {
  mode: "simple" | "custom";
  style: "plain" | "branded";       // plain reads like a personal email; branded adds a card, header and accent
  logo: boolean;                    // the brand's logo on top
  accent: string;                   // "#rrggbb" or "" = brand.color; links and the branded accent
  font: "system" | "sans" | "serif";
  footer_html: string;              // design allowlist, takes variables; "" = none
  history: "none" | "last" | "all"; // quote the previous message, or the whole conversation (up to 10)
  history_header: string;           // text; {{ message.author }} and {{ message.date }}; "" = "On {{ message.date }}, {{ message.author }} wrote:"
};
type EmailTemplate = {
  brand_id: string; kind: "layout" | "csat";
  subject: string;                  // csat only; "" = the default
  body_html: string;                // layout: the custom layout (mode custom); csat: the email body
  options: EmailLayoutOptions | {}; // layout only
  is_default: boolean;              // true when the brand has not changed it (the fields carry the default)
  updated_at: string | null;
};
```

- Custom layouts are a whole email body with the slots `{{ content }}` (required), `{{ signature }}`
  and `{{ history }}`, plus any variable. opendesk adds the document around it.
- The CSAT body must contain `{{ survey }}`, the five score buttons in the accent colour.
  The default reads "How did {{ team.name }} do?" with the request's number and subject.
- Quoted history holds earlier public replies of the ticket (never notes), newest first, without
  their images. Only replies carry history; auto-replies and CSAT emails do not.
- The plain-text part follows the same parts: content, signature, footer, then quoted history as
  `> ` lines.

Endpoints (backend-email):

- `GET /v1/email-templates/{brand_id}` (viewer+) returns `{ layout: EmailTemplate, csat: EmailTemplate }`.
- `PUT /v1/email-templates/{brand_id}/{kind}` (admin) saves `{ subject?, body_html?, options? }`;
  problems are `422`. Publishes `email_template.updated` `{ brand_id, kind }`.
- `DELETE /v1/email-templates/{brand_id}/{kind}` (admin) goes back to the default; publishes the same event.
- `POST /v1/templates/preview` (agent+) renders without saving:
  `{ kind, body_html?, subject?, options?, brand_id?, team_id?, mailbox_id?, ticket_id? }` →
  `{ subject, html, text, problems: { message: string; token: string }[] }`. `html` is the whole email
  as the customer would see it: a signature or auto-reply inside its brand's layout, a layout or
  CSAT email around sample content. Values come from `ticket_id` (a ticket the caller can see),
  else sample data, with the caller as the agent. Referenced ids must be the workspace's.
- `POST /v1/templates/test` (agent+) takes the same body and sends the rendered email to the caller's
  own login address through the platform mailer, subject prefixed `[Test]`. Limited to 10 an hour
  per user (`429`).

Ticket endpoints (backend-helpdesk; the ticket must be visible, `404` otherwise):

- `GET /v1/tickets/{id}/variables?mailbox_id=` (agent+) → `{ values: Record<string, string>, signature_html: string }`:
  every variable's value for this ticket with the caller as the agent, and the signature their
  reply from `mailbox_id` (else the ticket's address) would carry.
- `POST /v1/tickets/{id}/render` (agent+) `{ body_html, thread?: boolean }` → `{ body_html }`: filled
  and sanitized with the rich-text allowlist, for inserting saved replies. `thread` fills for a
  third-party thread: the ticket's brand, team and address and the caller, never the customer's or
  the ticket's details.
