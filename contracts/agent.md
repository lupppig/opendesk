# The AI agent (widget), its tools and knowledge

When `settings.ai.widget_answers` is on, the AI is a conversational agent in the chat widget. It
answers visitors from the workspace's knowledge, uses the tools the workspace connects (its own MCP
servers), hands off to the team when it should, and is metered on real usage
(`contracts/billing.md`). Mailboxes can opt in to the same agent for email ("Email" below).
This file is the contract for the agent, its tools (MCP connections),
knowledge sources, guardrails, playground and reports, and for opendesk's own MCP server.

Owners: the agent loop, guardrails, actions, ratings, playground and reports live in `internal/ai`
(`widget*.go`, `agent*.go`); MCP connections in `internal/ai/mcp`; knowledge sources in
`internal/ai/knowledge`; the opendesk MCP server in `internal/core/mcpserver`.

## Identity

The agent speaks as a **bot** (contracts/bots.md): a chat as its widget's bot (`Widget.bot_id`), an
email as its mailbox's bot (`Mailbox.bot_id`), the workspace's default bot (Ollie unless changed)
where none is set. Visitors and email recipients see the bot's name and look; its replies keep the
bot they were sent as (`messages.bot_id`, `author.bot`). In the web app its replies show the bot with
an "AI" tint; activities, triage, drafts and the assistant are always Ollie's.

## When it answers

- Every visitor message in a chat conversation the agent is handling gets one reply. It handles a
  conversation while the ticket is open or pending, unassigned, not spam, not handed off and no team
  member has replied. It runs as a job (`ai.widget_answer`, scheduled 1.5 s after the message so a
  burst of quick messages gets one reply about all of them), never in the request. A job whose
  message is no longer the latest visitor message does nothing.
- After `settings.ai.widget_max_replies` replies in one conversation it hands off.
- It stays silent and the conversation waits for the team, as without the agent, when: AI is off,
  the AI balance is used up (`ai_balance_empty`), the workspace's monthly agent cap is reached
  (`widget_monthly_cap_usd`), or the provider fails after retries.

## What it knows

Each reply is a tool-using model loop (at most 6 model turns per reply) with:
- the conversation: the latest 30 public messages (12,000 characters);
- the top 6 passages for the visitor's latest messages from published KB articles **and** ready
  knowledge sources (below), retrieved up front (embeddings, FTS fallback, the looser draft threshold).
  KB articles are the conversation's brand's and the shared ones (contracts/teams.md: an article takes
  its collection's brand); knowledge sources are workspace-wide. Tickets are never a source;
- the bot's personality (contracts/bots.md "The prompt": name, role, tone, reply length, emoji use,
  language, greeting, handoff line, email sign-off, instructions) in one `<bot_persona>` block; the
  workspace's `widget_blocked_topics` and the bot's `blocked_topics`; the workspace's
  `reply_language` and `instructions` and the workspace name. The persona shapes voice, never the
  rules, tools or blocked topics;
- the visitor: name, whether the session is verified (`user_hash`), and the current page URL when the
  widget reports it.

All of it is in data blocks. The agent answers only from that and from tool results: small talk,
clarifying questions and grounded answers. It never invents prices, policies, order details or
promises; it declines blocked topics politely; it offers a person when it cannot help.

Built-in tools (always offered):

| tool | does |
| --- | --- |
| `search_knowledge` `{query}` | more passages from KB articles and knowledge sources (top 6) |
| `handoff_to_human` `{reason}` | ends the agent's part: the reply text so far is posted (a short note that a person will follow up), then the conversation is handed off |

Plus the workspace's enabled MCP tools (below), named `<server_slug>__<tool>` (at most 64
characters, `[a-zA-Z0-9_-]`).

## Output and posting

The final text is plain markdown, streamed. Citations are `[n]` markers naming the n-th passage;
they are removed from the posted text and the cited articles and source pages are listed as links
under the reply (no list when nothing was cited).

The reply is a normal outbound chat message (`author.type = "system"`, `ai: true`, from name = the
bot name), added under the ticket lock only if nothing changed while the model ran (a new visitor
message, an assignee, a team reply, a handoff); otherwise it is discarded. Activity `ai_answered`
(`actor_type = "ai"`, data `{message_id, sources, tools: [tool names called]}`) records each reply.

Streaming: while generating, the job publishes live events (`contracts/realtime.md`, "Live events")
to the visitor and to agents watching the ticket: `ai.reply.started`, `ai.reply.delta` chunks (raw
markdown with `[n]` markers; clients hide them), `ai.reply.tool` while a tool runs, and
`ai.reply.ended` with the posted `message_id` (or `null`), `handoff` and `action_id`. The posted
message also arrives as a normal `message.created` (the source of truth); live events are a preview
and are never replayed.

## Handoff

- A visitor asking for a person ("talk to a human", and the same in a few languages) hands off
  without a model call; the agent calling `handoff_to_human`, or Jev's `needs_human` at 0.8 or above,
  hands off with a short reply.
- Visitor `POST /v1/widget/conversations/{id}/handoff` (visitor auth, `204`, idempotent; the widget
  offers "Talk to a person" while the agent is handling the conversation) marks the ticket for a
  human: `custom.ai_handoff = true`, `custom.ai_handoff_at`, activity `ai_handoff`, notification type
  `ai_handoff` to the assignee, else the ticket's team, else every non-viewer member, and
  `ticket.updated` with `["custom"]`. When the agent hands over, the ticket also gets
  `custom.ai_handoff_bot` and the activity `data.bot`, the name of the bot that handed over, plus
  `data.bot_id` unless it is the built-in default (`contracts/bots.md`); the inbox shows that bot
  instead of the assistant. The widget learns of a handoff from `ai.reply.ended` or from
  `ai_handoff` on the conversation.

## Visitor actions (confirm before acting)

An MCP tool whose policy is `confirm` is never run by the model alone. When the agent calls it, the
loop stops and the reply is posted with an action card the visitor confirms or declines:

```ts
// On Message (contracts/api.md), public, set only on the agent's messages that carry one:
ai_action: { id: string; title: string /* from the tool's title, else its name */; summary: string /* the agent's one-line description of what will happen */;
  status: "pending" | "confirmed" | "declined" | "expired" | "failed"; tool: string /* display name */ } | null
```

- `POST /v1/widget/conversations/{id}/actions/{action_id}` (visitor auth) `{decision: "confirm" |
  "decline"}` → `204`. It claims the pending action atomically (`409 action_not_pending` after;
  actions expire after 15 minutes or when the visitor writes again). Confirm runs the tool with the
  stored arguments, then the agent continues with the result as a new reply; decline continues with
  "the visitor declined". Either way the action's message is updated (`message.updated`, public, on
  the contact topic too) with the new status. Audit `ai.action` (tool and status, not arguments).
- Arguments are stored encrypted (`d.Box`) in `ai_widget_actions` and never shown to the visitor
  beyond the summary.

## Ratings

Visitors may rate each agent reply: `POST /v1/widget/conversations/{id}/messages/{message_id}/rating`
(visitor auth) `{rating: "up" | "down" | null}` → `204` (null clears). Only `ai: true` messages of
that conversation (`404` otherwise). Stored on the message as `ai_rating` (the visitor sees their own
rating, agents see it in the thread) and published as `message.updated` (public, on the contact
topic too).

## Guardrails (`settings.ai`, `PATCH /v1/ai/settings`)

```ts
widget_blocked_topics: string[]   // at most 20, each 2..100 chars of plain text: topics the agent declines
widget_monthly_cap_usd: number    // US dollars of AI usage per calendar month (UTC) the agent may use (features answer, answer_email, answer_check, agent tools); 0 = no cap; 0..10,000, rounded to cents
widget_collect_email: boolean     // when the visitor has no email, the agent asks for one before handing off, default true
```

The cap counts what usage rows of the agent's features charged this month. Reaching it stops the agent (silent,
the team takes over) and sends one notification `ai_cap_reached` per month to owners and admins.

## Playground (admin)

`POST /v1/ai/agent/playground` tests the agent without a visitor or a ticket.

```ts
// Body
{ messages: { role: "visitor" | "agent"; text: string }[] /* 1..40, the last is the visitor's, each up to 4,000 chars */;
  settings?: Partial<Pick<AISettings, "widget_model" | "widget_blocked_topics" | "tone" | "reply_language" | "instructions">> & {
    bot_id?: string /* answer as this bot of the workspace (422 otherwise); default the default bot */;
    bot?: { name?: string; role?: string; language?: string; personality?: Partial<BotPersonality> } /* unsaved edits, validated like PATCH /v1/bots/{id} */
  } /* unsaved drafts */;
  visitor?: { name?: string; email?: string; verified?: boolean; user_id?: string } }
```

It answers `text/event-stream`: `delta {text}`, `tool {name, status: "running" | "done" | "error",
summary}`, `sources {sources: Source[]}`, then `done {text_html, sources, handoff: boolean, action:
{tool, title, summary} | null, usd: number /* what the reply cost, in US dollars */}`, or `error` (problem JSON). Read-only MCP tools run
for real (with the given visitor in `_meta`); any other tool is not run and comes back as `action`.
Metered as feature `playground` (about $0.03 per reply on the default model, counted toward nothing else).
Rate limited to 30 per user per minute.

```ts
interface Source { kind: "article" | "page" | "file" | "snippet"; id: string; title: string; url: string | null }
```

## Setup (admin)

`GET /v1/ai/agent/setup` (admin, or an API key with `settings:read`) is the agent's setup
checklist, computed from the workspace's data on every call (owner: `internal/ai/agentsetup`,
served from `internal/ai/setup_api.go`):

```jsonc
{ "steps": [ { "id": "ai_on", "title": "Turn on AI", "description": "...", "done": true, "optional": false, "href": "/settings/ai" } ],
  "done_count": 4, "total": 6, "complete": false }  // counts required steps only
```

| id | done when | optional |
| --- | --- | --- |
| `ai_on` | the instance has a provider (or the workspace a working own key) and `settings.ai.enabled` | |
| `identity` | a bot was made or changed: more than one bot, or the default has another name, a picture, instructions or an edit (`href` `/settings/ai/bots`) | |
| `knowledge` | a published KB article or an enabled, ready knowledge source | |
| `tools` | an enabled, connected MCP connection | yes |
| `playground` | a playground reply was recorded (`ai_usage.feature = 'playground'`) | |
| `widget` | a widget session was seen (`workspaces.widget_seen_at`) | |
| `answers_on` | `settings.ai.widget_answers` | |
| `email_answers` | a mailbox has `ai_answers` on; listed only where that setting exists | yes |

Settings > AI agent > Overview shows it until `complete`, then collapsed. The "Get set up"
checklist (`GET /v1/onboarding`) has a `setup_ai_agent` step that is done when this is `complete`.

## Report (admin)

`GET /v1/ai/agent/report?from&to` (admin, `settings:read`; same range rules as `/v1/billing/usage`, default the last
30 days) →

```ts
{ from: string; to: string;
  conversations: number          // chat conversations the agent replied in
  replies: number
  resolved: number               // of those, solved or closed with no team member reply
  handoffs: number
  ratings: { up: number; down: number }
  usd: number                    // AI usage in US dollars: features answer, answer_email, answer_check and agent tools
  by_day: { date: string; conversations: number; replies: number; resolved: number; handoffs: number }[]
  tools: { tool: string; calls: number; errors: number }[]
  gaps: { question: string; count: number }[]  // up to 20 recent visitor questions the agent handed off or could not answer, newest first (first 200 chars)
  channels: { chat: ChannelReport; email: ChannelReport }  // the totals above split by the ticket's channel
}
interface ChannelReport { conversations: number; replies: number; resolved: number; handoffs: number;
  suggested: number       // email only: suggested replies written (draft mode)
  suggested_sent: number  // email only: of those, sent as written by a teammate
}
```

## Email

The agent can also answer email, per mailbox, **off by default**. A mailbox has (`contracts/api.md`
`Mailbox`, `PATCH /v1/mailboxes/{id}`, admin, audit-logged):

```ts
ai_answers: boolean               // Ollie answers emails to this mailbox; turning it on needs AI on (422 ai_off)
ai_answers_mode: "draft" | "send" // draft: a suggested reply a teammate sends, edits or discards; send: Ollie replies by email
ai_answers_outside_hours: boolean // only while business hours are closed: the ticket's team's own hours when it has them, else the workspace's (ignored while those hours are off)
bot_id: string | null             // the bot it answers as (contracts/bots.md); null = the default bot
```

It does not need `widget_answers`; it shares the agent's knowledge, `widget_blocked_topics`,
`widget_model`, `widget_max_replies`, `widget_monthly_cap` and language settings, and answers as the
mailbox's bot (`Mailbox.bot_id`, else the default bot), whose name fills `{agent}` in the From name
and whose `sign_off` ends its emails.

When it answers:
- Inbound (`internal/email`) queues job `ai.email_answer` in the transaction that stores a customer's
  email (a new ticket or a reply), scheduled 30 s later so automations and assignment run first and a
  burst of emails gets one answer (a job whose message is no longer the latest does nothing). It never
  queues for mail that failed sender checks (DMARC, contracts/api.md "Inbound email"), mail inbound
  already drops (bounces, `Auto-Submitted`, `Precedence: bulk/junk/auto_reply`, foreign lists,
  auto-replies, the workspace's own mailboxes), nor for any
  message with `Precedence: list`, `List-Id`, `List-Unsubscribe`, `List-Post`, `X-Auto-Response-Suppress`,
  `X-Autoreply`, `X-Autorespond`, `X-Loop`, `Feedback-ID`, a null `Return-Path`, an out-of-office
  subject, or a no-reply sender (`noreply`, `no-reply`, `donotreply`, `mailer-daemon`, `postmaster`,
  `bounce`, `notifications`, `alerts`, `newsletter`).
- The job answers only while: AI is on, the mailbox has `ai_answers` (and is not disabled), the
  ticket is an open or pending email ticket of that mailbox, unassigned, not spam, not handed off, no
  team member has replied on it, and the sender is not a member of the workspace or one of its
  mailboxes. Each customer message is handled at most once (`ai_widget_answers`, as in chat).
- Loop protection: besides the above, at most 5 agent emails or suggestions per contact per hour
  over all their tickets (then it stays silent), `widget_max_replies` per ticket (counting sent replies
  and pending or discarded suggestions, then it hands off), and the agent's emails carry
  `Auto-Submitted: auto-generated` and `X-Auto-Response-Suppress: All` so autoresponders do not answer.
- It stays silent (the team handles the email) when the AI balance is used up, the monthly cap is reached
  (`ai_cap_reached` as in chat), or the provider fails after retries.

What it writes: the same loop as chat (knowledge, guardrails, at most 6 model turns) with an email
prompt: one complete email (a greeting with the customer's first name, the answer, a sign-off with
the bot name and the workspace name), no chat phrases, `[n]` citations removed and no link list
(passages may be linked inline). The customer block says the channel is email and the sender is
**not verified**, so only read-only tools that need no confirmation and are not `verified_only` are
offered; tool `_meta` carries `verified: false`. Metered as feature `answer_email` (about $0.03
per model turn on the default model), plus `answer_check` for Jev.

Handoff: the agent calling `handoff_to_human`, or Jev's `needs_human` at 0.8 or above (checked before
the model, which is then not called), hands off **without sending anything**: `custom.ai_handoff`,
activity `ai_handoff` (`actor_type = "ai"`), the `ai_handoff` notification and `ticket.updated`, as in
chat. The ticket stays unassigned and waits for the team.

**Send mode**: the reply is an outbound email message (`author.type = "system"`, `ai: true`, from name
= the bot name, `delivery_status: "pending"`) sent by the normal outbound path with the ticket's
threading headers, under "<bot name> at <mailbox name>". It is added under the ticket lock only if
nothing changed while the model ran (else discarded). Activity `ai_answered` with `channel: "email"`.

**Draft mode**: the agent leaves a suggested reply on the ticket instead (activity
`ai_suggested_reply`, event `ai_suggested_reply.created`). A newer one supersedes the pending one.

```ts
interface SuggestedReply {
  id: string; ticket_id: string
  message_id: string             // the customer message it answers
  body_html: string; sources: Source[]; tools: string[]
  status: "pending" | "sent" | "discarded" | "superseded"
  reply_id: string | null        // the sent message
  created_at: string
}
```

| method | path | role | body → response |
| --- | --- | --- | --- |
| GET | `/v1/tickets/{id}/ai/suggested-reply` | viewer (`tickets:read`) | → `{suggested_reply: SuggestedReply \| null}`: the pending one while it answers the ticket's latest message |
| POST | `/v1/tickets/{id}/ai/suggested-reply/{suggestion_id}/send` | agent (`tickets:write`) | → `Message`: sent as written, attributed to the agent like a send-mode reply; activity `ai_suggested_reply_sent` (actor: the teammate); `409 suggestion_not_pending` when it was decided, superseded, or the ticket has a newer message |
| DELETE | `/v1/tickets/{id}/ai/suggested-reply/{suggestion_id}` | agent (`tickets:write`) | → 204, idempotent; nothing is sent |

Sending or discarding publishes `ai_suggested_reply.updated`. Editing is client side: the composer
opens with the suggestion's text and the teammate sends it as their own reply (a team member reply,
so the agent stops answering that ticket).

## Tests (admin)

Saved test conversations check the agent before and after a change of instructions, knowledge or
tools. A test is one to six messages (the last one the visitor's) and what the answer must do.

```ts
interface AgentTest {
  id: string; name: string                 // 1..100, default: the first visitor message
  messages: { role: "visitor" | "agent"; text: string }[]   // 1..6, the last the visitor's, each 1..4,000 chars
  expectations: AgentExpectation[]         // 1..10, no duplicates, not both handoff and no_handoff
  visitor: { name?: string; email?: string; verified?: boolean; user_id?: string } | null   // as in the playground
  last_result: AgentTestResult | null      // the test's result in the latest run that finished it
  created_at: string; updated_at: string
}
interface AgentExpectation {
  kind: "mentions"          // the answer conveys `value` (1..1,000 chars), judged by a model
      | "not_mentions"      // the answer does not convey `value`, judged by a model
      | "handoff" | "no_handoff"   // the agent called handoff_to_human, or did not
      | "uses_tool"         // the agent ran (or, for a confirm tool, asked to run) the tool `value`: "<server_slug>__<tool>"
      | "cites_knowledge"   // the answer cites at least one passage
  value?: string
}
interface AgentTestRun {
  id: string; status: "queued" | "running" | "done" | "failed" | "cancelled"
  draft: boolean                           // ran against unsaved settings
  test_ids: string[]                       // in order
  total: number; passed: number; failed: number; errored: number
  usd: number                              // what the run cost, in US dollars
  error: string | null                     // why a failed run stopped (a fixed, safe message)
  results?: AgentTestResult[]              // GET /v1/ai/agent/test-runs/{id} only
  created_at: string; started_at: string | null; finished_at: string | null
}
interface AgentTestResult {
  run_id: string; test_id: string
  status: "pending" | "running" | "passed" | "failed" | "error"
  answer_html: string                      // the agent's answer, sanitized, citation markers removed
  handoff: boolean; tools: string[] /* display names of tools that ran */; sources: Source[] /* cited */
  action: { tool: string; title: string; summary: string } | null   // a tool call that needs confirmation (never run)
  checks: { kind: AgentExpectation["kind"]; value: string; passed: boolean; reason: string }[]   // one per expectation, in order
  error: string | null                     // the test could not be answered or graded (fixed message)
  usd: number                              // what answering and grading the test cost, in US dollars
  draft: boolean                           // the run used unsaved settings
  previous_status: "passed" | "failed" | "error" | null   // the test's final status in the run before, so newly failing tests stand out
  finished_at: string | null
}
```

| method | path | body → response |
| --- | --- | --- |
| GET | `/v1/ai/agent/tests` | → `{data: AgentTest[]}` (oldest first) |
| POST | `/v1/ai/agent/tests` | `{name?, messages, expectations, visitor?}` → `201 AgentTest` |
| GET | `/v1/ai/agent/tests/{id}` | → `AgentTest` |
| PATCH | `/v1/ai/agent/tests/{id}` | `{name?, messages?, expectations?, visitor?}` (an empty `visitor` clears it) → `AgentTest` |
| DELETE | `/v1/ai/agent/tests/{id}` | → `204` (its results go with it) |
| POST | `/v1/ai/agent/test-runs` | `{test_ids?: string[] /* default: all */, settings?: /* the playground's unsaved drafts */}` → `202 AgentTestRun` |
| GET | `/v1/ai/agent/test-runs` | → `{data: AgentTestRun[]}` (the 20 latest, newest first) |
| GET | `/v1/ai/agent/test-runs/{id}` | → `AgentTestRun` with `results` |
| POST | `/v1/ai/agent/test-runs/{id}/cancel` | → `AgentTestRun` (answered tests keep their results; the rest stay `pending`) |

- Admin only (`settings:read` / `settings:write`); test routes work while AI is off, runs answer
  `404 ai_disabled` without a provider. At most 200 tests per workspace (`422 limit_reached`).
  Creating, changing and deleting tests and starting a run are audit-logged
  (`ai.agent_test_created`, `_updated`, `_deleted`, `ai.agent_test_run_started`).
- One run at a time per workspace (`409 run_in_progress`; a run still queued or running an hour
  after it started is failed as stopped). `422 no_tests` without tests; an unknown test id is `404`.
  At most 60 runs per workspace per hour (`429`).
- A run is a job (`ai.agent_test_run`, one attempt). It answers each test with the playground
  engine: the saved settings with the run's drafts on top, the test's visitor, no ticket;
  read-only tools run, any other tool comes back as `action` and never runs. Up to 3 tests at a
  time, 2 minutes each.
- `handoff`, `no_handoff`, `uses_tool` and `cites_knowledge` are checked from what the agent did.
  `mentions` and `not_mentions` of a test are graded together by one call to the workspace's fast
  model (short structured output; the conversation, answer and expectations are data blocks). An
  answer with no text fails `mentions` and passes `not_mentions` without a call. A test passes
  when every check passes; `error` when it could not be answered or graded.
- Answers and grading are metered as feature `playground` (not counted toward the agent's monthly
  cap). When the AI balance is too low the test errors and the run ends `failed` ("The AI balance
  is too low to finish the run.").
- Progress is live: `ai_agent_test_run.updated` and `ai_agent_test_result.updated`
  (`contracts/events.md`).

## Improve answer (admin)

On an agent reply (in a chat ticket, the playground or the review queue) an admin writes the better
answer. It becomes a Q&A snippet knowledge source the agent answers from at once.

`POST /v1/ai/agent/improve` (admin, `settings:write`)

```ts
// Body
{ question: string /* the visitor's question, 3..4,000 chars */; answer: string /* markdown, 1..4,000 */;
  message_id?: string /* the agent reply being improved: it leaves the review queue */;
  save_test?: boolean /* also save a test: the question, expecting "mentions" the answer's text (first 1,000 chars) */ }
// 201
{ source: { id: string; kind: "snippet"; name: string }; test: AgentTest | null }
```

- The snippet is created exactly like `POST /v1/ai/knowledge` (`ai.knowledge_created`,
  `ai_knowledge.created`, indexed at once). Its question is the visitor's question cut at 300
  characters. The improvement is audit-logged (`ai.agent_answer_improved` with the source, test
  and message ids).
- `message_id` must be an agent reply of the workspace (`404`). The test limit applies to
  `save_test` before anything is created.

## Review queue (admin)

`GET /v1/ai/agent/review` (admin, `settings:read`) → `{data: AgentReviewItem[]}`: up to 50 agent
replies of the last 30 days, newest first, that a visitor rated `down` or after which the agent
handed off (a visitor asking for a person is not listed), without the ones dismissed or improved.

```ts
interface AgentReviewItem {
  id: string                    // the agent reply's message id
  ticket_id: string; ticket_number: number
  reason: "rated_down" | "handoff"
  question: string              // the visitor message before the reply (first 500 chars)
  answer: string                // the reply as text (first 1,000 chars)
  contact_name: string
  created_at: string
}
```

`POST /v1/ai/agent/review/{id}/dismiss` (admin, `settings:write`) → `204`; `404` unless `id` is an
agent reply of the workspace on a ticket the caller may see (contracts/teams.md "Visibility"; the same
for `message_id` in improve). Dismissing and improving publish `ai_agent_review.updated`.

## MCP connections (company tools)

A workspace connects its own remote MCP servers; their tools become the agent's tools. Transport:
MCP Streamable HTTP (protocol `2025-06-18` or newer, negotiated), falling back to the legacy
HTTP+SSE transport. Admin only (`settings:write` for API keys).

```ts
interface McpServer {
  id: string; name: string /* 1..60 */; slug: string /* from name, unique per workspace, used in tool names */
  url: string                    // https (http only on a development instance)
  enabled: boolean
  auth: { kind: "none" | "bearer" | "header" | "oauth"; header_name?: string; has_secret: boolean;
          oauth?: { connected: boolean; scopes: string[]; expires_at: string | null } }
  status: "connected" | "needs_auth" | "error" | "connecting"
  error: string | null           // a fixed, safe message (never the server's body)
  server_info: { name: string; version: string } | null
  tools: McpTool[]
  last_synced_at: string | null; created_at: string; updated_at: string
}
interface McpTool {
  name: string; title: string | null; description: string /* up to 1,000 chars shown */
  input_schema: Record<string, unknown>
  read_only: boolean             // from annotations.readOnlyHint
  destructive: boolean           // annotations.destructiveHint
  enabled: boolean               // default: true for read-only tools, false otherwise
  policy: "auto" | "confirm"     // default: auto for read-only tools, confirm otherwise
  verified_only: boolean         // offered only in verified widget sessions, default false
}
```

| method | path | body → response |
| --- | --- | --- |
| GET | `/v1/ai/mcp-servers` | → `{data: McpServer[]}` |
| POST | `/v1/ai/mcp-servers` | `{name, url, auth: {kind, token? /* bearer */, header_name?, header_value? /* header */}}` → `McpServer` (connects and lists tools; an OAuth-protected server answers `status: "needs_auth"`) |
| PATCH | `/v1/ai/mcp-servers/{id}` | `{name?, url?, enabled?, auth?, tools?: Record<string, Partial<Pick<McpTool, "enabled" \| "policy" \| "verified_only">>>}` → `McpServer`. A new `url` on a bearer or header server needs the token or header value again in `auth` (`422` otherwise): a saved secret never goes to another address |
| DELETE | `/v1/ai/mcp-servers/{id}` | → 204 (revokes OAuth tokens when the server supports revocation) |
| POST | `/v1/ai/mcp-servers/{id}/sync` | → `McpServer` (reconnect, list tools again; tool settings are kept by name) |
| POST | `/v1/ai/mcp-servers/{id}/oauth/start` | → `{url}`: the authorization URL (browser redirect) |
| GET | `/v1/ai/mcp-servers/oauth/callback` | `?state&code` → 302 `APP_URL/settings/ai/tools?connected=<id>` or `?error=<code>` |
| POST | `/v1/ai/mcp-servers/{id}/tools/{name}/test` | `{arguments: object, visitor?: {...}}` → `{content: string, is_error: boolean, ms: number}` (runs the tool once, any policy) |

- Secrets (bearer token, header value, OAuth client secret, access and refresh tokens) are encrypted
  with `d.Box` and never returned. `header_name` must be a valid token and not a hop-by-hop or
  `Host`/`Cookie`/`Content-*` header.
- OAuth follows the MCP authorization spec: protected resource metadata (RFC 9728) from the 401
  `WWW-Authenticate` or `/.well-known/oauth-protected-resource`, authorization server metadata
  (RFC 8414 / OIDC discovery), dynamic client registration (RFC 7591) when no client is configured,
  authorization code with PKCE (S256) and the `resource` parameter (RFC 8707), redirect URI
  `API_URL/v1/ai/mcp-servers/oauth/callback`, state bound to the server, the admin and the workspace
  (10 minutes, single use). Access tokens refresh automatically; a failed refresh sets `needs_auth`.
- Every outbound request (server, metadata, token endpoints) uses the SSRF guard (`internal/netsafe`):
  https, no private, loopback or link-local targets (re-checked per redirect, at most 3), except on a
  development instance. Calls time out after 20 s (listing 15 s); responses over 1 MB are refused;
  tool output given to the model is the text content, truncated to 8,000 characters, inside a
  `<tool_output>` data block.
- Each tool call sends `_meta: {"opendesk/visitor": {verified, user_id?, email?, name?, contact_id,
  conversation_id}, "opendesk/workspace": {id, name}}`. Only `verified: true` sessions carry
  `user_id`/`email` from the verified identity; MCP servers should trust identity only from there,
  never from tool arguments. Tools with `verified_only` are not offered in unverified sessions.
- Changes are audit-logged (`ai.mcp_server_created`, `_updated`, `_deleted`, `_connected`,
  `_disconnected`; never secrets) and publish `ai_mcp_server.created` / `.updated` / `.deleted`
  (agents, data `{mcp_server: McpServer}` / `{mcp_server_id}`).
- At most 10 servers per workspace and 64 enabled tools in total, counted over every server
  (`422 limit_reached`). Tool lists are re-synced hourly (periodic `ai.mcp_sync`, which publishes
  `ai_mcp_server.updated` only when something changed) and on demand. A new read-only tool starts
  enabled only while the workspace is under the tool limit.
- The slug is set at creation from the name: lowercase letters, digits and `_`, at most 24
  characters, `_2`, `_3`… when taken. `POST` answers `201`.
- Errors: `422 invalid_url`, `422 unknown_tool` (a tool name in a PATCH that the server does not
  list); `oauth/start` answers `409 not_oauth`, `403` for API keys (it needs a person in a
  browser), and `422` `discovery_failed`, `pkce_unsupported`, `registration_unsupported` or
  `registration_failed`. The callback's `?error=` is one of `oauth_state`, `access_denied`,
  `forbidden`, `not_found`, `provider_error`, `internal`.
- A server with no auth that answers `401` with a Bearer challenge (or none) becomes `kind:
  "oauth"`, `needs_auth`. `connecting` is not visible: creating a server connects before saving.
- The OAuth `state` is single use and is also bound to the starting browser by an HttpOnly cookie
  (`od_mcp_oauth`, path `/v1/ai/mcp-servers/oauth`), so `oauth/start` is called with credentials.
  The callback checks that the user is still an admin, the server URL is unchanged, and the
  RFC 9207 `iss` when sent. Requests to registration, token and revocation endpoints never follow
  redirects; credentials are sent only to the server's own origin. Audit entries record the URL's
  host only.
- Tool calls are metered only through the model turns around them. Each call of a visitor
  conversation is recorded in `ai_tool_calls` (tool display name, ok, duration; never arguments or
  results) for the report; playground calls are not recorded.

## Knowledge sources

Besides KB articles, the agent (and drafts and copilot) answer from knowledge sources:

```ts
interface KnowledgeSource {
  id: string; kind: "website" | "file" | "snippet"; name: string /* 1..100 */
  status: "pending" | "syncing" | "ready" | "error"; error: string | null
  enabled: boolean                       // used for answers, default true
  website?: { url: string; max_pages: number /* 1..500, default 100 */; include: string[]; exclude: string[] /* path prefixes, at most 20 each */; recrawl: "off" | "weekly" }
  file?: { filename: string; content_type: string; size_bytes: number }
  snippet?: { question: string /* 3..300 */; answer: string /* 1..4,000, markdown */ }
  doc_count: number; char_count: number; last_synced_at: string | null; created_at: string; updated_at: string
}
interface KnowledgeDoc { id: string; title: string; url: string | null; chars: number; updated_at: string }
```

| method | path | body → response |
| --- | --- | --- |
| GET | `/v1/ai/knowledge` | `?kind` → `{data: KnowledgeSource[]}` |
| POST | `/v1/ai/knowledge` | `{kind: "website", name?, website}` or `{kind: "snippet", name?, snippet}` → `KnowledgeSource` (a website starts syncing) |
| POST | `/v1/ai/knowledge/files` | multipart `file` (PDF, DOCX, TXT, Markdown, HTML, CSV; 20 MB) and `name?` → `KnowledgeSource` |
| GET | `/v1/ai/knowledge/{id}` | → `KnowledgeSource` |
| PATCH | `/v1/ai/knowledge/{id}` | `{name?, enabled?, website?, snippet?}` → `KnowledgeSource` |
| DELETE | `/v1/ai/knowledge/{id}` | → 204 (documents and embeddings removed) |
| POST | `/v1/ai/knowledge/{id}/sync` | → `KnowledgeSource` (re-crawl or re-extract; `409` while syncing) |
| GET | `/v1/ai/knowledge/{id}/docs` | `?cursor&limit` → `{data: KnowledgeDoc[], next_cursor}` |
| DELETE | `/v1/ai/knowledge/{id}/docs/{doc_id}` | → 204 (excluded from future crawls of that source) |

Admin only for writes, viewer for reads (`settings:read` / `settings:write`); the routes work while
AI is off, so sources can be prepared first. Limits for every workspace: 50 sources, 5,000
documents, 20 million characters (`422 knowledge_limit`). `recrawl` defaults to `weekly`.

A source answers once it has synced at least once (`last_synced_at` set) and is enabled, so a
website stays answerable during its weekly recrawl. Long files are split into documents of 100,000
characters ("name (part n of m)"); page text is cut at 100,000 characters. A document whose
embedding failed stays searchable by full text and is retried by the hourly upkeep job
(`ai.knowledge_maintain`).

- Website crawl (job `ai.knowledge_sync`): same origin as `url` (and its `www.` twin), `robots.txt`
  honoured for user agent `opendeskBot`, `sitemap.xml` first, then links breadth-first, `include` /
  `exclude` path prefixes, at most `max_pages` pages, one request per second per host, 10 s timeout,
  2 MB per page, HTML and plain text only. Each page's main text is extracted (scripts, styles,
  navigation, headers, footers and cookie banners dropped) with its `<title>`. SSRF guarded like
  MCP. User agent `opendeskBot/1.0 (+https://opendesk.sh/bot)`. `recrawl: "weekly"` re-syncs
  weekly (periodic job); unchanged pages (same `content_hash`) are not re-embedded.
- Files are stored like attachments (`internal/storage`), text extracted in the job (PDF text
  layer, DOCX paragraphs, CSV rows as lines); a file with no extractable text ends in `error`
  ("No text found in this file").
- Snippets are one document each (the question as title).
- Documents are chunked and embedded like articles (`ai_embeddings` kind `source`, ref = doc id),
  with an FTS fallback; disabling a source excludes it from retrieval at once. Indexing is not
  charged.
- Status changes publish `ai_knowledge.updated` (agents, `{source: KnowledgeSource}`), plus
  `.created` / `.deleted`. Creating and deleting are audit-logged (`ai.knowledge_created`,
  `ai.knowledge_deleted`).

## opendesk MCP server

opendesk itself is an MCP server, so AI clients (Claude, Cursor, VS Code, any MCP client) can work
the helpdesk: `POST {API_URL}/mcp` (Streamable HTTP, stateless, JSON responses; `GET` and `DELETE`
answer `405`, as the spec allows for a server without sessions). Auth: `Authorization: Bearer <API key>` (`contracts/api.md` API keys)
or an OAuth access token (below); every tool runs through the public API in process as that
principal, so it gets exactly its scopes, role checks, validation, events, activities and audit
entries. Writes are attributed to the key, or to the user who connected the client.

Tools (offered only when the key has the scope):

| tool | scope | does |
| --- | --- | --- |
| `search_tickets` | `tickets:read` | filters (status, assignee, team, label, priority, text, dates), at most 50 |
| `get_ticket` | `tickets:read` | a ticket with its thread (notes included), team, brand, `received_at` and the reply address |
| `reply_to_ticket` | `tickets:write` | a public reply (`body` markdown) |
| `add_note` | `tickets:write` | an internal note |
| `update_ticket` | `tickets:write` | status, priority, severity, assignee, team, labels |
| `create_ticket` | `tickets:write` | a ticket for a contact email |
| `search_contacts` / `get_contact` | `contacts:read` | |
| `search_knowledge` | `kb:read` | KB articles and knowledge sources |
| `create_kb_draft` | `kb:write` | a draft article |
| `get_report` | `tickets:read` + `settings:read` | the overview report for a range |

Tool annotations mark read-only and destructive tools. Output is JSON text. No `Mcp-Session-Id` is
issued. The server checks `Origin` (browser requests must come from
`CORS_ORIGINS`), is rate limited like the API (per key or per grant), and answers `401` with
`WWW-Authenticate: Bearer realm="opendesk", resource_metadata="{API_URL}/.well-known/oauth-protected-resource/mcp"`
(plus `error="invalid_token"` when a credential was sent) without a valid one.

### OAuth (connectors without an API key)

opendesk is the authorization server for its MCP server, following the MCP authorization spec, so
connectors (Claude, ChatGPT) connect with a sign-in instead of a key. Owner: `internal/core/mcpserver`
(`oauth.go`, `oauth_consent.go`); grant scopes and revocation in `internal/core/oauthgrant`.

| endpoint | does |
| --- | --- |
| `GET /.well-known/oauth-protected-resource` and `.../oauth-protected-resource/mcp` | Protected Resource Metadata (RFC 9728): `resource` = `{API_URL}/mcp`, `authorization_servers` = [`{API_URL}`], `scopes_supported` |
| `GET /.well-known/oauth-authorization-server` | Authorization Server Metadata (RFC 8414); issuer `{API_URL}` |
| `POST /oauth/register` | Dynamic Client Registration (RFC 7591), JSON |
| `GET /oauth/authorize` | starts the authorization code flow |
| `POST /oauth/token` | `authorization_code` and `refresh_token` grants, form-encoded |
| `POST /oauth/revoke` | revocation (RFC 7009) |

The metadata, registration, token and revocation endpoints allow any origin (no credentials).
Errors are RFC 6749 JSON (`{error, error_description}`); nothing on these routes logs a code,
token, secret or verifier.

- **Registration**: anonymous, 20 per address per hour. `redirect_uris` (1 to 10) must be `https`,
  or `http` on `127.0.0.1`, `localhost` or `[::1]` for native apps (any port at authorize time,
  RFC 8252); no fragments, credentials or wildcards. `token_endpoint_auth_method` is `none`
  (public clients), `client_secret_post` or `client_secret_basic` (the default; the secret is
  shown once and stored hashed). `client_name` goes through `displayname.CleanOr` (a name that
  reads as a link becomes "MCP client"). Unused clients are deleted after 30 days.
- **Authorize**: `response_type=code`, `client_id`, `redirect_uri`, `state` (passed through),
  `code_challenge` with `code_challenge_method=S256` (required; `plain` is refused), `scope`
  (space-separated; unknown scopes are ignored, none means all of them) and `resource` (RFC 8707;
  must be `{API_URL}/mcp` when sent). An unknown client or redirect URI is never redirected to: the
  browser goes to the app's consent page with `?error=`. Other errors redirect to the client with
  `error`, `error_description`, `state` and `iss`. A valid request is stored for 10 minutes and the
  browser goes to `{APP_URL}/oauth/consent?request=<id>`.
- **Consent** (web app, signed-in session required): `GET /v1/oauth/requests/{id}` shows the
  client's name, its redirect host (and whether it is an app on this computer), the requested
  scopes and the user's workspaces, each with the scopes their role there allows.
  `POST /v1/oauth/requests/{id}/approve` `{workspace_id}` creates the **grant** (audit
  `oauth.grant_created`) and answers `{redirect_to}`: the redirect URI with `code`, `state` and
  `iss`. `POST /v1/oauth/requests/{id}/deny` answers `{redirect_to}` with `error=access_denied`.
  A request is decided once (`410 request_expired` after that or after 10 minutes).
- **Codes**: 32 random bytes, stored hashed, single use, 60 seconds, bound to the client, the
  redirect URI, the resource and the PKCE challenge. Any attempt spends the code; a code presented
  after it was used revokes its grant.
- **Tokens**: the access token is a JWT with purpose `mcp_access` (`contracts/tokens.md`; HS256,
  1 hour, `aud` the resource, `sub` the user, `wid`, `gid` the grant, `scope`). The refresh token
  (`od_mcpr_...`, 30 days) rotates on every use and is stored hashed; presenting a rotated one
  again revokes the grant (audit `oauth.grant_revoked`, reason `refresh_reused`). A refresh may
  narrow `scope`, never widen it. Connecting the same client to the same workspace again replaces
  the earlier grant.
- **Scopes**: `tickets:read`, `tickets:write`, `contacts:read`, `contacts:write`, `kb:read`,
  `kb:write`, `settings:read` (never `settings:write` or `realtime`). On every MCP request the
  grant is loaded: the tools offered and the API checks use the user's **current** role and the
  granted scopes together (viewers read only; agents and up also write tickets, contacts and
  articles). Account routes and routes API keys may not call refuse a grant.
- **Revocation**: the user (or an admin) disconnects the client in Settings > AI clients, the client
  calls `/oauth/revoke` with its refresh or access token, the member is removed from the
  workspace, or the user's password changes or is reset (every grant of the user ends, like their
  other sessions). All are audit-logged as `oauth.grant_revoked` with a reason. Revocation takes
  effect on the next request.

### Settings > AI clients

`GET /v1/ai-clients/server` (members) describes the server for setup screens: `url`,
`resource_metadata`, `authorization_server`, `scopes` and every tool with its scopes.
`GET /v1/ai-clients/connections` lists connected clients (your own; admins see every member's):
client name, redirect host, user, scopes, created and last used. `DELETE
/v1/ai-clients/connections/{id}` disconnects one. Clients that send a header instead of OAuth
(Cursor, VS Code, Claude Desktop through `mcp-remote`) use an API key with the scopes they need.
