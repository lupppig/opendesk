# AI

opendesk's AI runs through one provider interface (`internal/ai`). Providers:
`openrouter` (Cloud: one `OPENROUTER_API_KEY` for chat models, Jev and embeddings; defaults
`AI_MODEL=anthropic/claude-sonnet-5.5`, `AI_FAST_MODEL=anthropic/claude-haiku-5.5`),
`anthropic` (defaults `AI_MODEL=claude-sonnet-5-5`, `AI_FAST_MODEL=claude-haiku-4-5-20251001`),
`openai` (any OpenAI-compatible endpoint via `OPENAI_BASE_URL`: OpenAI, Azure, Ollama,
vLLM, LM Studio, so self-hosters can run fully local), or `none`. Unset, `AI_PROVIDER` is
`openrouter` when `OPENROUTER_API_KEY` is set, else `anthropic` with `ANTHROPIC_API_KEY`, else
`openai` with `OPENAI_API_KEY`, else `none`. Embeddings (semantic search, answer retrieval):
`openrouter` (default with an OpenRouter key, `openai/text-embedding-3-small`), `voyage`, `openai`,
or `none` (falls back to Postgres FTS).

OpenRouter (`OPENROUTER_BASE_URL`, default `https://openrouter.ai/api`): chat is
`POST /v1/chat/completions` (OpenAI-compatible, tools supported), embeddings `POST /v1/embeddings`,
the model list `GET /v1/models`, Jev `POST /alpha/decisions`. Every request sends
`provider: {data_collection: "deny"}` so it is routed only to providers that do not retain or train on
prompts, plus the attribution headers `HTTP-Referer: APP_URL` and `X-Title: opendesk`. Prompt caching
uses `cache_control` content parts (honoured by Anthropic models, ignored by others). Each response's
`usage.cost` (USD) is what the call is metered on (`contracts/billing.md`).

When the provider is `none`, `GET /v1/ai/config` reports `enabled: false` and the web app
hides AI controls; every other AI route answers `404 ai_disabled`, except triage, which also runs on
a decision engine alone (below).

Decisions (typed answers with calibrated probabilities: triage, the widget relevance check,
`ai_decide` automation conditions) run through one more interface, `internal/ai/decide`
(`Decider`: Choice, Score and Noul questions, several per call). `DECISIONS_PROVIDER`:
`openrouter` (**Jev** through OpenRouter's Decisions API with `OPENROUTER_API_KEY`, `TYPESAFE_MODEL`
default `~typesafe/jev-latest`), `typesafe` (Jev direct from TypeSafe, `TYPESAFE_API_KEY`,
`TYPESAFE_API_URL` default `https://api.typesafe.ai`, `TYPESAFE_MODEL` default `jev-latest`; the
Vercel AI Gateway works with `TYPESAFE_API_URL=https://ai-gateway.vercel.sh/typesafe` and
`TYPESAFE_MODEL=typesafe-ai/jev`), `llm` (the configured AI provider's fast model answers the same
questions as structured output, with self-reported probabilities and no calibrated confidence) or
`none`. Unset, it is `typesafe` when `TYPESAFE_API_KEY` is set, else `openrouter` when
`OPENROUTER_API_KEY` is set, else `llm` when an AI provider is configured, else `none`. Both Jev
routes send the same request (below) and are the same engine (`jev`). Jev is the
model, not a persona: everything it decides is shown as Ollie. Without a TypeSafe key every feature
keeps working on the LLM path; nothing is gated on Jev.

Every call: `d.Plan.Reserve` → model → `d.Plan.Spend` with the call's real cost (see
`contracts/billing.md`: `Reserve` checks the AI balance covers the call's estimate, priced on the
chosen model). A workspace on its own provider key skips `Reserve` and is recorded at $0
("Provider per workspace" below). Prompts carry only the data the feature needs. Customer content is sent to
the configured provider only; provider responses are not used for training (Anthropic API default;
on OpenRouter, `data_collection: "deny"`). No prompt or completion
text is logged; usage rows store counts only. Workspace admins can turn AI features off
(`settings.ai`).

## Provider per workspace

A workspace runs its AI on the instance's provider (the default; on Cloud, charged to the workspace's AI balance in US dollars)
or on its own key. Owners and admins choose in Settings > AI. Kinds:

| kind | key | where calls go |
| --- | --- | --- |
| `openrouter` | an OpenRouter API key | the instance's `OPENROUTER_BASE_URL`, same `data_collection: "deny"` routing and headers |
| `openai` | an OpenAI-compatible base URL and key | `{base_url}/chat/completions`; `base_url` is https on a public address (the SSRF guard used for webhooks and MCP servers: private, loopback and link-local targets refused, checked again at connect time, no redirects, no proxy), except on a development instance, which also allows loopback http. `model` is required |
| `anthropic` | an Anthropic API key | the Anthropic Messages API (`ANTHROPIC_BASE_URL` when set) |

- The key is sealed with `d.Box` (`ai_provider_keys`, one row per workspace) and never returned or
  logged: the API shows the kind, the base URL host and the last 4 characters.
- Routing: with a key, **every LLM call** of the workspace uses it: drafts, summaries, copilot,
  rewrite, translate, KB drafts, LLM triage and the `llm` decision engine, the widget agent and its
  playground, Ollie (the assistant and its connected tools), and the email agent. No call falls back to
  the instance's provider.
- Stays on the instance: **embeddings** (semantic search, retrieval; not charged, as before) and
  **Jev decisions** (triage, the widget relevance check, `ai_decide`), which keep their small cost
  to the AI balance. If Jev cannot run because the AI balance is too low, triage runs on the workspace's key instead.
- Models: `GET /v1/ai/models` lists the provider's models (OpenRouter `GET /v1/models`, the endpoint's
  `GET /models`, Anthropic `GET /v1/models`; cached an hour), with `usd_per_mtok: null` and `typical_reply_usd: null` (the workspace pays its provider). The key's
  `model` and `fast_model` are the defaults (OpenRouter `anthropic/claude-sonnet-5.5` and
  `anthropic/claude-haiku-5.5`, Anthropic `claude-sonnet-5-5` and `claude-haiku-4-5-20251001` when
  empty). `settings.ai` model choices must be one of these (`422` otherwise); a stored choice the
  provider does not list falls back to the key's default.
- Billing: calls on the key charge $0 and are never refused for the AI balance; usage rows
  are still written with `micro_usd = 0` and `source = "own_key"` (contracts/billing.md).
- Failures: a 401 or 403 from the provider marks the key `rejected`; a 402 (or an OpenAI
  `insufficient_quota`) marks it `no_funds`. The call fails like a provider outage (`502
  ai_key_rejected` / `502 ai_key_no_funds`; automatic features skip as for `ai_unavailable`), and owners
  and admins get the in-app notification `ai_key_failed` at most once an hour. The next successful call
  sets the state back to `ok`. Replacing the key resets it.
- Audit: `ai.provider_key_set`, `ai.provider_key_replaced`, `ai.provider_key_removed` with `{kind,
  base_url_host?}`, never the key. Removing the key returns the workspace to the instance's provider.

| method | path | min role | body → response |
| --- | --- | --- | --- |
| GET | `/v1/ai/provider` | admin (`settings:read`) | → `AIProvider` |
| POST | `/v1/ai/provider/test` | admin (`settings:write`) | `AIProviderKeyInput` → `AIProviderTest`; nothing is stored. 20 tests or saves per workspace per minute (`429`) |
| PUT | `/v1/ai/provider` | admin (`settings:write`) | `AIProviderKeyInput` → `AIProvider`; tests the key first (`422 provider_test_failed` with the test's message), then stores or replaces it |
| DELETE | `/v1/ai/provider` | admin (`settings:write`) | → 204 |

```ts
type AIKeyKind = "openrouter" | "openai" | "anthropic"
interface AIProvider { mode: "instance" | "own_key"; instance_provider: string; billing: boolean /* instance calls are charged to the AI balance, in US dollars */;
  key: { kind: AIKeyKind; base_url_host: string; last4: string; model: string; fast_model: string;
    status: "ok" | "rejected" | "no_funds"; status_at: string | null; created_by: UserRef | null; created_at: string; updated_at: string } | null }
interface AIProviderKeyInput { kind: AIKeyKind; api_key: string /* 8..500 printable ASCII, write only */;
  base_url?: string /* openai only */; model?: string /* required for openai */; fast_model?: string }
interface AIProviderTest { ok: boolean; reason?: "invalid_key" | "no_funds" | "unreachable" | "busy" | "model_unavailable";
  message?: string /* plain words for the admin; never the provider's response */; models: string[] }
```

The test is a tiny call: the provider's model list (on OpenRouter also `GET /v1/key`, whose exhausted
`limit_remaining` reads as `no_funds`); an endpoint without a model list gets a one-line completion on
`model`. A base URL that fails the SSRF guard is `422 validation_failed`.

Untrusted content (customer emails, chat, KB) is always placed in delimited data blocks and the
system prompt instructs the model to treat it as data. Model output is sanitized with the
same bluemonday rich-text policy before it is shown or stored, and AI never sends a message to a
customer by itself except the widget answer bot (opt-in, labelled as AI, with a handoff button).

## Workspace settings (`settings.ai`)

```ts
interface AISettings {
  enabled: boolean                 // master switch, default true when the instance has AI
  auto_triage: boolean             // run triage on new tickets, default false
  triage_fields: ("priority" | "severity" | "labels" | "team")[] // what triage may change, default priority, severity, labels (set through /v1/triage)
  triage_threshold: number         // 0.5..0.99, default 0.7: decisions below it become suggestions (set through /v1/triage)
  assign_after_triage: boolean     // assign a ticket after auto-triage, attributed to ai (contracts/api.md "Triage"), default false
  auto_summarize: boolean          // keep a summary on tickets with 5+ messages, default false
  widget_answers: boolean          // the AI agent answers chat widget conversations, default false
  assistant: boolean               // Ollie, the assistant panel, default true
  tone: string                     // "friendly" | "professional" | "concise" | free text up to 200 chars; drafts and rewrites (the agent uses its bot's tone, contracts/bots.md)
  reply_language: "customer" | "workspace"  // reply in the customer's language (default) or the workspace's
  instructions: string             // extra guidance for drafts, up to 2,000 chars
  model: string                    // drafts, summaries, copilot, KB drafts, Ollie; "" = the instance default (AI_MODEL)
  fast_model: string               // rewrite, translate, LLM triage and decisions; "" = AI_FAST_MODEL
  widget_model: string             // the widget agent; "" = `model`
  widget_max_replies: number       // 1..100, default 20: AI replies in one conversation before it hands off to the team
}
```

`model`, `fast_model` and `widget_model` must be "" or an `id` from `GET /v1/ai/models`
(`422 validation_failed` otherwise). A stored model the instance no longer offers falls back to the
instance default without an error. Choosing a model is never gated on plan: a costlier model simply
costs more of the AI balance per call (each call is charged on its real cost, contracts/billing.md).

`PATCH /v1/ai/settings` merges the given keys into `settings.ai` (others are kept), answers
`422 validation_failed` for an unknown `reply_language`, a `tone` over 200 characters or
containing `<`, `>` or control characters, or `instructions` over 2,000 characters (control
characters other than newline and tab are stripped), and writes the audit entry
`ai.settings_updated` with the changed keys (not their values). `reply_language: "workspace"`
means English unless `instructions` name another language. `widget_max_replies` outside 1..100 is
`422`. What the agent should know and how it sounds are its bot's personality (contracts/bots.md).

## Models

The instance offers a list of models a workspace may choose from: `AI_MODELS` (comma-separated
model ids), else, on OpenRouter, a curated default list (`anthropic/claude-sonnet-5.5`,
`anthropic/claude-haiku-5.5`, `anthropic/claude-opus-5.5`, `openai/gpt-5.6-sol`,
`openai/gpt-5.6-luna`, `google/gemini-3.8-flash`, `deepseek/deepseek-v4.1-flash`, `z-ai/glm-5.3-flash`), else just
`AI_MODEL` and `AI_FAST_MODEL`. `AI_MODEL` and `AI_FAST_MODEL` are always in the list. On OpenRouter,
names, context lengths and per-token prices are read from `GET /v1/models` (cached for an hour;
an id OpenRouter does not list is dropped, except the two defaults). Other providers list ids only.

```ts
// GET /v1/ai/models (viewer, settings:read) → { data: AIModel[]; default: string; fast: string }
interface AIModel {
  id: string                        // e.g. "anthropic/claude-sonnet-5.5"
  name: string                      // e.g. "Claude Sonnet 5.5" (the id when unknown)
  vendor: string                    // e.g. "Anthropic" ("" when unknown)
  context_length: number | null
  // US dollars per million tokens as the workspace pays them (the instance markup included when
  // billing is on; the provider's price on a self-hosted instance); null when the price is unknown
  usd_per_mtok: { input: number; output: number } | null
  // what a typical draft reply (4,000 input, 500 output tokens) costs on this model, in US dollars; null when the price is unknown
  typical_reply_usd: number | null
}
// Every US dollar value is a number with up to 6 decimals.
```

## Endpoints (owner area: ai)

| method | path | min role | typical cost (default models) | body → response |
| --- | --- | --- | --- | --- |
| GET | `/v1/ai/config` | viewer | $0 | `{enabled, provider, models: {default, fast} /* the workspace's effective choices */, embeddings: boolean, features: AISettings, costs: Record<string, number> /* what a typical call of each feature costs on the workspace's chosen models, in US dollars (contracts/billing.md "Typical costs"); on its own key, LLM features are 0 (the workspace pays its provider) and Jev features keep their cost */, metered: boolean /* each call is charged on its real cost */, assistant_name: "Ollie", decisions: DecisionEngine /* contracts/api.md */, own_key: boolean /* LLM calls run on the workspace's key ("Provider per workspace") */}`; `provider` and `models` are the workspace's |
| GET | `/v1/ai/models` | viewer | $0 | → `{data: AIModel[], default, fast}` ("Models" above; on the workspace's own key, its provider's models) |
| PATCH | `/v1/ai/settings` | admin | $0 | `Partial<AISettings>` → `AISettings` |
| POST | `/v1/tickets/{id}/ai/draft` | agent | $0.03 | `{instructions?: string, tone?: string}` → `{body_html, body_text, sources: KBSource[]}`; uses the thread, contact, saved replies and the top KB matches |
| POST | `/v1/tickets/{id}/ai/summary` | agent | $0.01 | `{}` → `{summary: string, key_points: string[], sentiment: Sentiment, updated_at}`; stored on the ticket (`custom.ai_summary`) and published as `ticket.updated` |
| POST | `/v1/tickets/{id}/ai/ask` | viewer | $0.02 | `{question: string}` → `{answer_html, sources: KBSource[]}` (copilot) |
| POST | `/v1/tickets/{id}/ai/triage` | agent | $0.01 | `{apply: boolean}` → `Triage`; `apply` sets tags/priority/severity with `automation`-style activities attributed to `ai` |
| DELETE | `/v1/tickets/{id}/ai/suggestions/{field}` | agent | $0 | → 204; dismisses Ollie's suggestion for `field` (`priority`, `severity`, `labels`, `team`, `spam`). Accepting is setting the field (PATCH the ticket), which removes the suggestion |
| POST | `/v1/ai/rewrite` | agent | $0.01 | `{html: string, mode: "friendlier"\|"more_formal"\|"shorter"\|"longer"\|"fix_grammar"\|"simplify", instructions?: string}` → `{html}` |
| POST | `/v1/ai/translate` | agent | $0.01 | `{html?: string, text?: string, target: string /* BCP 47 */}` → `{html?, text?, source_language}` |
| POST | `/v1/kb/articles/ai/draft` | agent | $0.05 | `{ticket_ids?: string[] /* at most 10 */, topic?: string /* up to 300 chars */}` → `{title, body_html, excerpt}` (not saved) |
| GET | `/v1/ai/search` | viewer | $0 | `?q` semantic search over tickets and articles (embeddings), FTS fallback → same shape as `/v1/search` |

`{id}` is a ticket uuid, `#123` (URL-encoded `%23123`) or `123`. API keys need the scope
of the data the route reads or writes: `tickets:read` (summary, ask, search),
`tickets:write` (draft, triage, rewrite, translate), `kb:write` (KB draft),
`settings:read` / `settings:write` (config, settings). `/v1/ai/search` returns only the
sections the key can read.

Errors, all `application/problem+json`:

| status | code | when |
| --- | --- | --- |
| 404 | `ai_disabled` | the instance has no AI provider |
| 403 | `ai_off` | `settings.ai.enabled` is false |
| 402 | `ai_balance_empty` | `d.Plan.Reserve` refused: the AI balance does not cover the call's estimate; the model was not called |
| 503 | `ai_busy` | the provider is rate limited or overloaded after retries |
| 502 | `ai_unavailable` | the provider failed or timed out |
| 502 | `ai_bad_output` | the model returned output that failed validation |
| 422 | `ai_refused` | the model declined the request |
| 502 | `ai_key_rejected` | the workspace's own key was refused by its provider (401, 403) |
| 502 | `ai_key_no_funds` | the workspace's own provider account is out of funds or quota |

A failed call costs nothing: the AI balance is charged only after the feature succeeded, and a
usage row (`ai_usage`, counts only) is written with every spend. Without billing (self-host)
usage rows are still written; there is no balance. Each successful call also sends the
analytics event `ai_feature_used {feature, model}` (no content).

Streaming: `draft`, `ask` and `rewrite` also accept `Accept: text/event-stream` and stream
`event: delta` `{text}` chunks, then `event: done` with the full JSON response. The call is
charged once, at `done`. Delta text is the raw model text (markdown for draft and ask, HTML
for rewrite) for a live preview only; render and insert the sanitized `done` payload. An
error before the first event is a normal problem+json response with its status (so `402
ai_balance_empty` arrives as JSON even on a streaming request); an error after the stream started
is `event: error` with the problem object as data, and the stream ends.

```ts
type Sentiment = "positive" | "neutral" | "negative" | "frustrated"
interface Triage { tags: string[]; suggested_tags: string[]; priority: "low" | "normal" | "high" | "urgent";
  severity: Severity | null; team_id: string | null;
  sentiment: Sentiment; language: string /* BCP 47, "und" when unknown */; category: Category;
  summary: string /* one line, at most 300 chars; empty on Jev, which does not write text */; applied: boolean;
  decisions: AIDecisions; suggestions: AISuggestions /* what this run left below the threshold */ }
type Category = "question" | "problem" | "bug" | "billing" | "account" | "feature_request" | "feedback"
  | "sales" | "shipping" | "refund" | "cancellation" | "spam" | "other"
interface KBSource { article_id: string; title: string; url: string /* {help center url}/a/{slug}: the ticket's brand's help center (contracts/teams.md "Help center addresses") */ }
```

Triage output is validated, never trusted: `tags` are at most 3 of the workspace's existing
tags (canonical names); `suggested_tags` are at most 2 new, cleaned, lowercase names; an
unknown priority becomes `normal`, sentiment `neutral`, category `other`, language `und`.
`apply` (and auto-triage, and the automation action) only ever **raises** priority and severity
(an empty severity is set), so AI never lowers a person's choice. Applying adds the tags, sets `custom.ai_sentiment`,
`custom.language`, `custom.ai_category`, `custom.ai_triaged_at`, and logs activities with
`actor_type = "ai"` (`tagged`, `priority_changed`, `ai_triaged`). Only the explicit API
`apply` creates `suggested_tags` as tags (never when `settings.label_creation` is `"admins"`; labels whose `applies_to` lacks tickets are skipped, and an exclusive label group keeps one label, the last applied, contracts/api.md); auto-triage and the automation action store them
in `custom.ai_suggested_tags` for an agent to accept.

A summary also sets `custom.ai_sentiment`. `custom.ai_summary` is
`{text, key_points, sentiment, updated_at, message_count}`.

Messages and tickets also get `language` detection for free when triage runs
(`custom.language`), which the web app uses for a "Translate" affordance.

## Decisions

Triage is one decision call (speculative fan-out): every question below goes in one request about
one state, and code uses the answers that apply. The state is JSON data, never instructions:
`{ticket: {subject, channel, created_at, messages: [{from, text}] /* the latest 6 public messages,
2,000 chars each */, summary?}, contact: {name, email_domain, company, ticket_count}, sla: {policy,
first_response_due_in_minutes, resolution_due_in_minutes} | null}`. Questions tell Jev to treat
the state as customer-supplied data and judge it, never follow it.

| key | type | options | applied to |
| --- | --- | --- | --- |
| `priority` | Score | low, normal, high, urgent, each a descriptive rubric | `priority` (only raised) |
| `severity` | Score | sev4, sev3, sev2, sev1, impact rubrics | `severity` (set, or only raised) |
| `category` | Choice | `Category` | `custom.ai_category` |
| `type_label` | Choice | the labels of the workspace's exclusive group named "Type", plus `none` | that label |
| `label` | Choice | the workspace's other ticket labels (at most 254), plus `none` | that label |
| `sentiment` | Choice | `Sentiment` | `custom.ai_sentiment` |
| `language` | Choice | 24 common BCP 47 tags plus `und` | `custom.language` |
| `spam` | Noul | | suggestion only (AI never marks spam itself) |
| `needs_human` | Noul | | recorded |
| `team` | Choice | the workspace's teams (at most 254), plus `none` | `team_id` when empty |
| `sla_risk` | Score | quick answer .. major incident | recorded; Ollie's `sla_at_risk` tip |

Questions whose options are empty (no Type group, no labels, no teams) are left out.

Confidence gating: Choice and Score answers carry Jev's `confidence` (0..1, from the shape of the
distribution); a Noul's is `|2p - 1|`. A decision is applied when the workspace allows the field
(`triage_fields`) and its confidence is at or above `triage_threshold` (default 0.7); otherwise it
is stored as a suggestion. Spam is a suggestion when its probability is at least 0.8. LLM triage
has no calibrated confidence: its decisions record `confidence: null` and apply as before. An
explicit `apply` from an agent applies every decision regardless of confidence.

```ts
type DecisionField = "priority" | "severity" | "category" | "type_label" | "label" | "sentiment" | "language"
  | "spam" | "needs_human" | "team" | "sla_risk"
interface Decision { value: string | boolean | number | null /* a label/team id for type_label, label, team; sla_risk 0..1 */;
  probability: number | null /* of the chosen value, or of yes */; confidence: number | null; applied: boolean }
interface AIDecisions { engine: "jev" | "llm"; model: string /* "jev", or the LLM model */; model_version?: string /* e.g. "jev-1.13.0" */;
  threshold: number; decided_at: string; fields: Partial<Record<DecisionField, Decision>> }
type SuggestionField = "priority" | "severity" | "labels" | "team" | "spam"
interface Suggestion { value: string | string[] | boolean /* labels: names; team: id */; confidence: number | null;
  probability: number | null; model: string; decided_at: string }
type AISuggestions = Partial<Record<SuggestionField, Suggestion>>
```

The latest run is stored as `custom.ai_decisions` and pending suggestions as
`custom.ai_suggestions` (published with `ticket.updated`, changes `["custom"]`). Activities of applied
decisions are attributed to `ai` with data `{via, model: "jev", confidence}` (LLM: `{via, model}`),
rendered "Ollie set severity to Sev 2". A suggestion goes away when anyone sets that field, or on
`DELETE /v1/tickets/{id}/ai/suggestions/{field}`; a new triage run replaces them.

Jev API (TypeSafe, documented at docs.typesafe.ai): `POST {TYPESAFE_API_URL}/v1/systemone`, or on
OpenRouter `POST {OPENROUTER_BASE_URL}/alpha/decisions` (same body, plus `provider: {data_collection:
"deny"}`; the response adds `usage.cost` in USD), with `Authorization: Bearer <key>` and `{state, model, questions: {<id>: {type: "choice" | "score" | "noul",
instructions, criteria}}}` → `{model, answers: {<id>: {type, choice? | score? | noul?, probabilities?,
legend?, confidence?}}, usage: {input_tokens, output_tokens}}`. Choice criteria is a map of option to
rubric (at most 255; options are sent in a fixed order); Score criteria an ordered array of 2..10
levels; Noul criteria `{true, false}`. Requests time out after 15 s and are retried (3 attempts) on
408, 429, 5xx and 529 with backoff honouring `Retry-After` / `retry-after-ms`; `401` and `422` are
not retried. Errors never carry the key, the request or the response body.

## Ollie (the assistant)

The AI has one name, **Ollie**, exposed once as `assistant_name` in `GET /v1/ai/config`
(`ai.AssistantName` in Go). The web app and emails read it from there. Everything the AI
does is attributed to Ollie: the assistant, auto-triage, widget answers and AI automation
actions. Activities with `actor_type = "ai"` render as "Ollie set priority to High" with the
Ollie mark as the actor avatar; when the AI acted for a person (`actor.user` set), the line
still names Ollie.

Ollie is an agent loop over tool use (Anthropic tool use; OpenAI-compatible `tools` as the
fallback; a provider without tool support answers `501 ai_unsupported`). It needs a signed-in
user (`kind = user`; API keys get `403`), viewer or above, AI on and `settings.ai.assistant`
on (`403 assistant_off`). Conversations belong to one user in one workspace.

| method | path | typical cost | body → response |
| --- | --- | --- | --- |
| GET | `/v1/assistant/sessions` | $0 | `?limit` → `{data: Session[]}` (the caller's, newest first) |
| POST | `/v1/assistant/sessions` | $0 | `{title?}` → `Session` |
| GET | `/v1/assistant/sessions/{id}` | $0 | → `{session, items: Item[], approval: Approval \| null}` |
| PATCH | `/v1/assistant/sessions/{id}` | $0 | `{title}` → `Session` |
| DELETE | `/v1/assistant/sessions/{id}` | $0 | → 204 |
| POST | `/v1/assistant/sessions/{id}/messages` | $0.02 per model turn | `{text /* 1..8000 */, context?: PageContext}` → run (below) |
| POST | `/v1/assistant/actions/{id}/decision` | $0.02 per model turn | `{decision: "approve" \| "deny", token}` → run |
| GET | `/v1/assistant/tips` | $0 | → `{tips: Tip[]}` (counts only, never the model; 30/min, `Cache-Control: private, max-age=60`) |

```ts
interface Session { id: string; title: string; created_at: string; updated_at: string }
interface PageContext { path?: string; page?: "inbox"|"view"|"ticket"|"contacts"|"kb"|"reports"|"settings"|"other";
  folder?: string; view_id?: string; ticket_id?: string; filters?: Record<string, string> }
interface Step { id: string; tool: string; label: string;
  status: "running"|"done"|"error"|"awaiting_approval"|"approved"|"denied"|"expired";
  summary?: string; refs?: Ref[]; action_id?: string }
interface Ref { kind: "ticket"|"contact"|"article"|"view"|"automation"|"member"|"external" /* id is the https link */; id: string; label: string; number?: number }
interface Preview { title: string; summary?: string; changes?: {field: string; from: string; to: string}[];
  body?: string /* markdown, a reply or note */; targets?: Ref[]; total?: number; external?: boolean /* reaches a customer */;
  system?: string /* the connected product the change runs in, e.g. "Linear" */ }
interface Approval { action_id: string; call_id: string; tool: string; preview: Preview; token: string; expires_at: string }
interface Item { role: "user"|"assistant"; text: string; steps: Step[]; context?: PageContext }
interface Tip { key: string; text: string; prompt: string; severity: "high"|"medium"|"low"; count: number }
// keys: sla_due_soon, sla_breached, sla_at_risk (open tickets due within 4 hours whose stored triage
// sla_risk is 0.6 or more), unassigned_old, mine_waiting
```

A run answers `text/event-stream` when asked (`Accept: text/event-stream`), otherwise one
JSON `{session, text, steps, approval, status}`. Events, in order:

| event | data |
| --- | --- |
| `session` | `Session` (messages only; the title is set from the first message) |
| `text` | `{text}`, a delta of Ollie's markdown |
| `tool` | `Step`, sent when a tool starts and again when it ends (same `id`) |
| `approval` | `Approval` |
| `done` | `{status: "complete" \| "awaiting_approval"}` |
| `error` | problem JSON; the stream ends |

Limits: 30 messages per user per minute (`429`), at most 8 model steps and one write
proposal per run, one run per conversation at a time (`409 assistant_busy`). The AI balance is
checked before anything is stored (`402 ai_balance_empty`).

Tools. Reads run directly; writes only propose. Every tool runs **through the public API
in process, as the user**, so it gets exactly the user's role checks, validation, events
and activities:

- read: `search_tickets`, `get_ticket` (with its thread), `search_contacts`, `search_kb`,
  `list_views` (views and queue counts), `get_report`, `list_labels_and_fields`, `list_team`
  (members and open workload).
- write (agent+): `update_ticket` (assignee, status, priority, labels, fields),
  `bulk_update_tickets` (resolved to exact ticket ids when proposed, at most 50),
  `add_note`, `draft_reply` (sent only on approval; `external: true`), `create_kb_draft`
  (saved as a draft), `create_view` (personal); admin: `create_automation_draft` (saved
  disabled). Tools the user's role cannot use are not offered to the model.
- the rest of the app (viewer+): `find_api` searches the documented operations (BM25 over summaries, paths, tags
  and field names, with everyday synonyms; the best two in full, the rest one line each; `"METHOD /path"` returns
  that one in full), `api_get` reads one (`fields` keeps only the named fields, of each item for a list), and
  `api_change` proposes one POST, PUT, PATCH or DELETE (a write, `preview.changes` = each body field, `from` = the
  value the same path reads now). The system prompt names the API's areas. Out of reach: `/v1/admin`, `/v1/auth`,
  `/v1/assistant`, public, visitor, device and server routes; writes to credentials, sessions, devices, API keys,
  billing, the AI provider, imports, owner-only operations (deleting the workspace or the account, transfer, widget
  secrets) and ticket messages (`draft_reply` and `add_note` instead). A body with a credential-named field
  (secret, token, password, API key) is refused, and such values are `[hidden]` in every answer the model reads.
  Writes that reach someone outside (invitations, Collab mail, test sends) set `preview.external`.
- connected (agent+): the workspace's integrations' MCP tools, named `<slug>__<tool>`
  (`contracts/integrations.md`). Server-marked read-only tools are reads; every other one
  is a write with `preview.system` set.

Approvals:
- A write is stored as an `assistant_actions` row (`pending`, 15 minutes) and the client
  gets an `assistant_approval` token (`contracts/tokens.md`) bound to the action, the
  user, the sign-in session and the workspace. The token reaches only the browser,
  never the model, so a run cannot approve itself.
- A decision claims the row atomically (`pending` and not expired), so it runs once
  (`409 approval_not_pending` after); a wrong token is `403 approval_invalid`.
- A new message expires a pending proposal; the model is told it was not made.
- An approved write runs with the user's permissions, attributed `actor_type = "ai"` with
  `actor.user` = the user, and is audit-logged as `assistant.action` (tool and status, not
  content) with `data.actor = "ai"`. POSTs carry the idempotency key `assistant-<action id>`.

Data: tool outputs are wrapped in `<tool_output>` data blocks and the page in a
`<page_context>` block, with the same delimiter escaping as every other feature.
Conversations are stored per user (`assistant_sessions`, `assistant_turns`); usage rows
hold counts only.

## Automations

Condition `{field: "ai_decide", op: "gt" | "lt", value: "0.7", question: "Is the customer at risk of
churning?"}`: a Noul asked of the decision engine about the ticket (the same state as triage). It holds when
the probability of yes is above (`gt`) or below (`lt`) `value` (0.01..0.99). The question is 3..300
characters of plain text written by an admin. Every `ai_decide` question of one trigger run is asked in one
call (about $0.01, feature `decide`) and answers are reused for the rest of the run. Without a decision engine,
AI off, an AI balance too low or a failed call, an `ai_decide` condition does not hold (the automation does not run).
At most 3 `ai_decide` conditions per automation.

New automation action `{type: "ai_triage", value: ""}` (about $0.01) and condition
`{field: "sentiment", op: "eq" | "equals" | "not_equals", value: Sentiment}` (reads
`custom.ai_sentiment`; `eq` is stored as `equals`). When `settings.ai.auto_triage` is on,
triage runs on `ticket_created` before other automations, so a sentiment condition on the
same trigger sees its result. Auto-triage skips spam and already triaged tickets. When AI is
off, the AI balance is used up or the provider fails, auto-triage and the `ai_triage` action are
skipped (logged) and the rest of the automation runs. The action calls the fast model inside
the trigger's transaction.

## Widget agent

The conversational AI agent in the chat widget, its tools (company MCP servers), knowledge
sources, guardrails, playground and reports are specified in `contracts/agent.md`.

## Events

`ai.usage` is not an event. Summaries and triage results arrive through `ticket.updated`
(changes include `"custom"`). Bot answers arrive as normal `message.created`.

## Embeddings

Table `ai_embeddings(workspace_id, kind 'article'|'ticket', ref_id, chunk, content_hash,
embedding vector)`, created only when the `vector` extension is available (PlanetScale, Neon and the
self-host `pgvector/pgvector:pg17` image have it; on stock Postgres without it, semantic
search uses FTS). A worker (`ai.embed`) indexes published articles on publish and tickets on
solve. Dimension is fixed per model and recorded; a model change triggers a reindex.

Content is split into chunks of about 800 tokens (3,200 characters) with a 400-character
overlap, split at paragraph, then sentence, then word boundaries; at most 40 chunks per article
and 6 per ticket. Article chunks are prefixed with the title. A ticket document is its
subject, stored summary and public messages. `content_hash` is `sha256(model, text)`;
unchanged content is not re-embedded. Unpublishing or deleting an article removes its rows within seconds (an `ai.embed` job); a deleted
ticket's rows go at the next maintenance run. Embedding models: Voyage `voyage-3.5`, OpenAI
`text-embedding-3-small` (or `EMBEDDINGS_MODEL`).

A periodic job (`ai.maintain`, every 5 minutes) removes rows of other models and of deleted
content, enqueues published articles and tickets solved in the last 90 days that are
missing or stale, and, for workspaces with `auto_summarize`, enqueues `ai.summarize` for
tickets with 5+ messages, quiet for 2 minutes, whose summary is older than the last message.

Search uses brute-force cosine distance per workspace (no ANN index; workspaces are small
enough) fused with full-text results by reciprocal rank (k = 60). Without embeddings, KB
retrieval for drafts and the widget uses FTS with keyword coverage thresholds instead of a
similarity threshold.

KB retrieval for a ticket (drafts, ask, the widget and email agents) reads the ticket's brand's
articles and the shared ones (no brand of their own or of their collection) and links them on
that brand's help center; without a ticket (playground, KB drafts) it reads every brand's.
Ticket embeddings serve `GET /v1/ai/search` only, which leaves out private teams' tickets the
caller cannot see (contracts/teams.md "Visibility"); no answer or draft uses another ticket
as context. Every `/v1/tickets/{id}/ai/*` route answers `404` for a ticket the caller cannot see.

## Models and prompts

Drafts, summaries, copilot, KB drafts and Ollie use the workspace's `model` (default `AI_MODEL`);
the widget agent uses `widget_model` (default `model`); LLM triage, decisions, rewrite and translate
use `fast_model` (default `AI_FAST_MODEL`). JSON features use structured outputs on Anthropic and
`json_schema` (falling back to `json_object`, then plain) on OpenAI-compatible endpoints.
System prompts are fixed text and cached (Anthropic prompt caching); workspace guidance
(tone, language, instructions) is a second cached system block; ticket data is the user
turn. Data blocks are XML-like tags whose content has `<` and `>` replaced by `‹` and `›`,
so content cannot close or open a block. Requests time out (90 s, 4 min streaming) and are
retried on 429, 5xx and 529 with backoff honouring `Retry-After`.

## Voice

Everything the AI writes for a customer reads like a capable person on the team typed it
(`internal/ai/voice.go`). The prompts that write to a customer from scratch (drafts, the widget and
email agents, Ollie's `draft_reply`) carry the writing rules: contractions, active voice, varied
sentence length, specifics over vague claims, straight to the point. They and the prompts that edit
or write customer-facing text (rewrite, KB drafts) carry the bans: em dashes, AI vocabulary ("delve",
"leverage", "robust"...), stock openers and closers, hedging filler, negative parallelism, stacked
apologies. The rules are written for English; other languages follow their spirit.

The output is then tidied without a model, with transforms that never change meaning: a dash joining
clauses (an em dash, or a spaced en dash or `--`) becomes a comma, except in code and in Cyrillic text,
where a dash is grammar; an unspaced en dash is a range and stays. Whole stock sentences ("Thank you
for reaching out!", "I hope this helps.") are removed from drafts and agent answers, never a longer
sentence that starts the same way, and never the whole reply. Translation, summaries and Ollie's
answers to teammates are left as written. Word swaps and forced contractions stay in the prompt only.

