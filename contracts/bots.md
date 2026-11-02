# Bots: the AI agent's customer-facing identities

A workspace builds its own bots: who the AI agent is to visitors and email customers, how it
looks and how it talks. Each chat widget and each mailbox answers as one bot; the AI itself is
the same agent (contracts/agent.md) with the same knowledge, tools and guardrails.

- **Ollie** is the in-app assistant (drafts, triage, the assistant panel; contracts/ai.md) and
  the look and name of every workspace's first default bot. In the web app, AI work is Ollie's.
- A **bot** is customer facing: its name, look and personality are what visitors and email
  recipients see. In the web app its messages show the bot's name and avatar with a violet
  "AI" tint, so agents can tell the AI wrote them.

Owner: `internal/ai/bots` (model, validation, look renderer, API); the agent's prompt in
`internal/ai`. Schema: `migrations/000625_bots.up.sql`. The look renderer has one
implementation per language that render identically: `web/src/lib/botlook.ts` (the web app and
the widget bundle) and `internal/ai/bots/render.go` (the public avatar endpoints), checked against
shared fixtures (`web/src/lib/botlook.fixtures.json`).

## Resource

```ts
interface Bot {
  id: string
  name: string               // 1..40, a name people choose (internal/displayname rules)
  role: string               // up to 60, plain text, shown under the name ("Support assistant"); "" = none
  language: string           // BCP 47 ("en", "de", "pt-BR") the bot always writes in; "" = the customer's language
  look: BotLook
  image_url: string | null   // the uploaded picture (look.kind "image"), else null
  avatar_url: string         // a PNG of the look for clients that cannot render it (email, iOS, notifications):
                             //   the uploaded picture, else API_URL/v1/public/bots/{id}/avatar.png?v=<look hash>
  personality: BotPersonality
  is_default: boolean        // exactly one per workspace; answers where nothing else is assigned
  position: number
  usage: { widgets: { id: string; name: string; brand_id: string }[];
           mailboxes: { id: string; name: string; email: string }[] }   // what answers as this bot
  created_at: string; updated_at: string
}

interface BotPersonality {
  tone: "friendly" | "professional" | "playful" | "concise" | "warm" | "custom"   // default "friendly"
  custom_tone: string        // up to 200, plain text; used when tone = "custom"
  instructions: string       // up to 4,000: what this bot should know and do (products, policies, hours, links)
  reply_length: "short" | "medium" | "detailed"   // default "short"
  emoji: "none" | "some" | "lots"                 // default "none"
  greeting: string           // up to 300, plain text with newlines: the bot's opening message in a new
                             //   chat while the agent answers; "" = the widget's greeting
  handoff_message: string    // up to 300: what it says when it passes the conversation to a person; "" = its own words
  blocked_topics: string[]   // at most 20, each 2..100 chars: declined on top of settings.ai.widget_blocked_topics
  sign_off: string           // up to 200, plain text with newlines: how its emails end; "" = its name and the company's
}
```

### The look

A bot's look is one JSON document. Every key is always present (switching `kind` keeps the
rest), every value is from an allow-list or a `#rrggbb` colour, and nothing else is accepted:
no SVG, markup or URLs from users ever reach a renderer.

```ts
interface BotLook {
  kind: "built" | "emoji" | "image"   // image needs an uploaded picture (image_url)
  shape: "blob" | "circle" | "rounded" | "squircle" | "hexagon" | "cloud"
  fill: { type: "solid" | "gradient"; color: string /* #rrggbb */; to: string /* #rrggbb, the gradient's second stop */ }
  eyes: "dots" | "ovals" | "pills" | "happy" | "sleepy" | "sparkle" | "wink" | "glasses" | "star"
  mouth: "none" | "smile" | "grin" | "open" | "cat" | "flat" | "tongue" | "smirk" | "tiny"
  accessory: "none" | "antenna" | "headphones" | "bow" | "cap" | "halo" | "sprout" | "crown"
  accent: string                      // #rrggbb: the bow, cap, headphones and antenna tip
  blush: boolean
  background: "none" | "soft" | "ring"
  emoji: string                       // one emoji (up to 10 code points, 32 bytes): the face when kind = "emoji"; may be ""
}
```

- `built`: the shape in the fill (a gradient runs from `color` at the top left to `to`), a soft
  shine, the eyes and mouth in white or ink (picked from the fill's brightness), optional blush and
  accessory, on the background. `emoji`: the emoji centred on the shape, fill and background.
  `image`: the uploaded picture, cropped to a circle.
- The **Ollie preset** is the built look of the violet blob: `{kind: "built", shape: "blob",
  fill: {type: "gradient", color: "#c9bcff", to: "#4a2bc4"}, eyes: "pills", mouth: "none",
  accessory: "none", accent: "#f59e0b", blush: false, background: "none", emoji: ""}`.
- Rendering is a pure function of the look (viewBox `0 0 64 64`): the same look gives the same SVG
  in the browser, the widget and the server. Clients that animate add CSS only: an idle blink, a
  "thinking" bob while the bot is writing, a small happy bounce when a reply lands, all off under
  `prefers-reduced-motion`.
- **Randomize** (client side): a seeded generator over curated palettes and pairing rules, so a
  roll always looks good; the same seed gives the same look.

## Endpoints

| method | path | role | body → response |
| --- | --- | --- | --- |
| GET | `/v1/bots` | members (keys: `settings:read`) | → `{data: Bot[]}` in `position` order, the default first among equals |
| GET | `/v1/bots/{id}` | members | → `Bot` |
| POST | `/v1/bots` | admin (`settings:write`) | `{name, role?, language?, look?, personality?}` → `201 Bot` (missing keys take the defaults: the Ollie look, a friendly personality); goes last |
| PATCH | `/v1/bots/{id}` | admin | `{name?, role?, language?, look?, personality?}` → `Bot`. `look` is replaced whole; `personality` keys are merged |
| DELETE | `/v1/bots/{id}` | admin | → `204`. `409 bot_is_default` for the default bot; `409 bot_in_use` while a widget or mailbox answers as it or a procedure names it in `bot_ids` (the detail names them) |
| POST | `/v1/bots/{id}/duplicate` | admin | → `201 Bot`: "<name> copy" with the same look (an uploaded picture is not copied: `kind` falls back to `built`), personality, role and language, placed right after |
| POST | `/v1/bots/{id}/default` | admin | → `Bot`: it becomes the default (widgets and mailboxes without a bot answer as it) |
| POST | `/v1/bots/reorder` | admin | `{ids}`: every bot once → `{data: Bot[]}` |
| PUT | `/v1/bots/{id}/image` | admin | multipart `file` (PNG, JPEG, GIF or WebP, 5 MB; the images pipeline, owner type `bot`) → `Bot` with `look.kind = "image"` |
| DELETE | `/v1/bots/{id}/image` | admin | → `Bot`; `look.kind` goes back to `built` when it was `image` |
| GET | `/v1/public/bots/{id}/avatar.svg` | public | the look as SVG (`image/svg+xml`); an uploaded picture redirects (`302`) to it |
| GET | `/v1/public/bots/{id}/avatar.png?s=64\|128\|256` | public | the look as PNG (default 256); an uploaded picture redirects to it. An emoji look shows the bot's initial on its shape (no emoji font on the server) |

- Validation: `422 validation_failed` naming the field for an unknown key or value, a colour that is
  not `#rrggbb`, an emoji that is not one, text with `<`, `>` or control characters (newlines are
  allowed in `greeting`, `handoff_message` and `sign_off`), lengths above. `language` is a BCP 47 tag
  (`[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8}){0,2}`) or "".
- At most 50 bots per workspace (`422 limit_reached`); not limited by plan.
- Avatar endpoints: `Cache-Control: public, max-age=3600` for a rendered look (the URL carries the look hash), a `302`
  to the picture with `max-age=300` for an uploaded one, `X-Content-Type-Options: nosniff`,
  `Content-Security-Policy: default-src 'none'; style-src 'unsafe-inline'; sandbox`. An unknown id is `404`.
- Audit: `bot.created`, `bot.updated` (`{changes: [keys]}`), `bot.deleted`, `bot.duplicated`,
  `bot.default_changed`, `bot.reordered`, `bot.image_updated`, `bot.image_removed`.

## Events

| type | visibility | data |
| --- | --- | --- |
| `bot.created` / `bot.updated` | agents | `{bot: Bot}` (`updated` also for a new default, a reorder, a picture) |
| `bot.deleted` | agents | `{bot_id}` |

Assigning a bot publishes `widget.updated` or `mailbox.updated`; clients refetch `usage` then.

## Default bot

Every workspace has exactly one default bot (a partial unique index). It is created with the
workspace (the Ollie look, named "Ollie") and can be renamed and restyled but not deleted; another
bot can become the default. The migration created it for existing workspaces from what they had:
the name from `settings.widget.bot_name`, the `widget_bot` picture (moved to the bot, `kind:
"image"`), the agent instructions (`settings.ai.widget_instructions`) and the workspace `tone`
(a free-text tone becomes `custom`). A widget whose `bot_name` differed got its own bot (one per
distinct name, a copy of the default with that name). Those old fields were then removed:
`WidgetSettings.bot_name`, the `widget_bot` image slot and `settings.ai.widget_instructions` are gone
from the API, and nothing reads them.

## Assignment

- `Widget.bot_id: string | null` (contracts/teams.md) and `Mailbox.bot_id: string | null`
  (contracts/api.md): the bot that answers there; `null` = the default bot. Set with `PATCH
  /v1/widgets/{id}` / `PATCH /v1/mailboxes/{id}` (admin); the id must be a bot of the workspace
  (`422 validation_failed`). Audit `widget.updated` / `mailbox.updated` with `bot_id`.
- A chat conversation answers as its widget's bot; an email ticket as its mailbox's bot; the
  playground and tests as the bot they name (default: the default bot).

## Where a bot shows

- Widget: `WidgetConfig.bot` is `{id, name, role, avatar_url, look, greeting}` of the widget's bot
  (contracts/api.md). The header shows it while the agent answers; its messages and typing state
  show its avatar and name; `ai.reply.started` carries `bot: {id, name, look, avatar_url}`.
- Messages: an AI reply has `author.bot: {id, name, avatar_url, look} | null` (`Message`, contracts/api.md),
  stored as `messages.bot_id`, so a reply keeps the bot it was sent as. `from_name` is the bot's name.
- Email: the bot's name is `{agent}` in the mailbox's `from_name_format` (default "<bot name> at
  <mailbox name>", contracts/teams.md) and its emails end with its `sign_off`.
- The app, iOS and the desktop app show the bot's name and avatar on its messages with an "AI"
  tint; Ollie stays the name of the in-app assistant and of AI actions (triage, drafts, summaries).

## The prompt

The bot's personality is one delimited block, `<bot_persona>`, inside the agent's guidance
(contracts/agent.md): name, role, tone, reply length, emoji use, language, greeting, handoff line,
email sign-off and the instructions. The fixed system prompt states that the persona shapes voice
and content and never overrides the rules about facts, tools, data and safety, and it comes after
those rules. Blocked topics (the workspace's and the bot's) stay a data block.
