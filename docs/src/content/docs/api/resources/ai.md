---
title: AI
description: Draft, summarize, ask, triage, rewrite, translate and search with the AI endpoints.
---

When no AI provider is configured, `GET /v1/ai/config` reports `enabled: false` and every other AI route returns `404 ai_disabled`, except triage, which also runs on a decision engine alone. On Cloud, AI routes are charged to the workspace's AI balance in US dollars, and return `402 ai_balance_empty` when the balance does not cover the call's estimate (the model is not called). See [Billing](/api/resources/billing/#ai-usage).

| Method | Path | Min role | Typical cost | Description |
| --- | --- | --- | --- | --- |
| `GET` | `/v1/ai/config` | viewer | $0 | Provider, models and settings |
| `GET` | `/v1/ai/models` | viewer | $0 | The models the workspace can choose, with their prices |
| `PATCH` | `/v1/ai/settings` | admin | $0 | Update workspace AI settings |
| `POST` | `/v1/tickets/{id}/ai/draft` | agent | $0.03 | Draft a reply |
| `POST` | `/v1/tickets/{id}/ai/summary` | viewer | $0.01 | Summarize a ticket |
| `POST` | `/v1/tickets/{id}/ai/ask` | viewer | $0.02 | Ask about a ticket |
| `POST` | `/v1/tickets/{id}/ai/triage` | agent | $0.01 | Triage, optionally applying the result |
| `DELETE` | `/v1/tickets/{id}/ai/suggestions/{field}` | agent | $0 | Dismiss Ollie's suggestion for `priority`, `severity`, `labels`, `team` or `spam` |
| `POST` | `/v1/ai/rewrite` | agent | $0.01 | Rewrite HTML |
| `POST` | `/v1/ai/translate` | agent | $0.01 | Translate HTML or text |
| `POST` | `/v1/kb/articles/ai/draft` | agent | $0.05 | Draft an article (not saved) |
| `GET` | `/v1/ai/search?q=` | viewer | $0 | Semantic search, same shape as `/v1/search` |

Each call is charged on its real cost on the workspace's chosen model: the provider's cost times the instance markup (1.5 on opendesk Cloud). The typical cost column is what a call is charged when that cost is unknown. `costs` in [`GET /v1/ai/config`](#config) gives a typical call of each feature on the workspace's chosen models. A failed call costs nothing, and calls on the workspace's own provider key cost $0.

## Config

```json
{
  "enabled": true,
  "provider": "openrouter",
  "models": { "default": "anthropic/claude-sonnet-5.5", "fast": "anthropic/claude-haiku-5.5" },
  "embeddings": true,
  "features": { "enabled": true, "auto_triage": false, "auto_summarize": false, "widget_answers": false, "assistant": true, "tone": "friendly", "reply_language": "customer", "instructions": "" },
  "costs": { "summarize": 0.0135, "rewrite": 0.00042, "translate": 0.000495, "triage": 0.01, "decide": 0.01, "answer_check": 0.001, "copilot": 0.018, "draft_reply": 0.0195, "answer": 0.021, "answer_email": 0.0225, "playground": 0.021, "kb_draft": 0.0465, "assistant": 0.024 },
  "metered": true,
  "assistant_name": "Ollie",
  "decisions": { "provider": "typesafe", "model": "jev-latest", "connected": true }
}
```

`costs` is what a typical call of each feature costs on the workspace's chosen models, in US dollars (numbers with up to 6 decimals). Features decided by Jev (`triage`, `decide`, `answer_check`) keep a fixed typical cost. `metered` is true: each call is charged on its real cost.

`decisions` is the workspace's decision engine: `typesafe` (Jev by TypeSafe), `llm` (the configured AI model) or `none`. `decisions_own_key` is true when decisions run on the workspace's own OpenRouter or TypeSafe key (or the AI model on its own key); their `costs` are then 0.

`PATCH /v1/ai/settings` accepts any subset of `features`. `tone` is `friendly`, `professional`, `concise` or free text up to 200 characters; `instructions` up to 2,000 characters. `model`, `fast_model` and `widget_model` are `""` (the instance default) or an `id` from `GET /v1/ai/models`. `widget_monthly_cap_usd` is what the AI agent may spend a calendar month (UTC), in US dollars: 0 to 10,000, rounded to cents, 0 for no cap.

## Models

`GET /v1/ai/models` lists the models the workspace can choose, and the instance defaults:

```json
{
  "data": [
    { "id": "anthropic/claude-sonnet-5.5", "name": "Claude Sonnet 5.5", "vendor": "Anthropic", "context_length": 1000000,
      "usd_per_mtok": { "input": 3, "output": 15 }, "typical_reply_usd": 0.0195 },
    { "id": "anthropic/claude-haiku-5.5", "name": "Claude Haiku 5.5", "vendor": "Anthropic", "context_length": 200000,
      "usd_per_mtok": { "input": 0.15, "output": 0.75 }, "typical_reply_usd": 0.000975 }
  ],
  "default": "anthropic/claude-sonnet-5.5",
  "fast": "anthropic/claude-haiku-5.5"
}
```

- `usd_per_mtok` is the price per 1 million input and output tokens in US dollars, as the workspace pays it: the provider's list price times the instance markup (1.5 on opendesk Cloud).
- `typical_reply_usd` is what a typical draft reply (4,000 input and 500 output tokens) costs on the model.
- Both are `null` when the price is unknown, and on the workspace's own provider key, where the workspace pays its provider.

Choosing a model is never gated on plan: a pricier model is charged more per call.

## Draft a reply

```sh
curl https://api.opendesk.sh/v1/tickets/1042/ai/draft \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "OD-Workspace: $WORKSPACE_ID" \
  -H "Content-Type: application/json" \
  -d '{ "instructions": "Offer a refund", "tone": "friendly" }'
```

```json
{ "body_html": "<p>Hi Ana, ...</p>", "body_text": "Hi Ana, ...", "sources": [{ "article_id": "...", "title": "Refund policy", "url": "https://..." }] }
```

## Summary, ask and triage

- `summary` returns `{ summary, key_points, sentiment, updated_at }` and stores it on the ticket, published as `ticket.updated`.
- `ask` takes `{ question }` and returns `{ answer_html, sources }`.
- `triage` takes `{ apply }` and returns `{ tags, suggested_tags, priority, severity, team_id, sentiment, language, category, summary, applied, decisions, suggestions }`. With `apply: true` it applies every decision regardless of confidence: labels, and priority and severity, which are only ever raised. `summary` is empty on Jev, which does not write text.

Sentiment is `positive`, `neutral`, `negative` or `frustrated`. Languages are BCP 47 tags.

## Decisions

Ollie decides triage using Jev, TypeSafe's decision model, when the instance has a TypeSafe key, and with the configured AI model otherwise. Every question is asked in one call:

| Key | Type | Applied to |
| --- | --- | --- |
| `priority` | Score: low, normal, high, urgent | `priority`, only raised |
| `severity` | Score: sev4, sev3, sev2, sev1 | `severity`, set or only raised |
| `category` | Choice | `custom.ai_category` |
| `type_label` | Choice over the labels of an exclusive group named "Type" | that label |
| `label` | Choice over the other ticket labels | that label |
| `sentiment` | Choice | `custom.ai_sentiment` |
| `language` | Choice over common BCP 47 tags | `custom.language` |
| `spam` | Yes or no | a suggestion only |
| `needs_human` | Yes or no | recorded |
| `team` | Choice over the workspace's teams | `team_id`, when empty |
| `sla_risk` | Score | recorded, used by Ollie's `sla_at_risk` tip |

A decision applies when the workspace allows the field (`triage_fields`) and its confidence is at or above `triage_threshold` (default 0.7, set through [`/v1/triage`](/api/resources/pictures-assignment/)). Otherwise it is kept as a suggestion. Spam is suggested when its probability is at least 0.8. Decisions from the AI model have no calibrated confidence, record `confidence: null` and apply as before.

The latest run is stored as `custom.ai_decisions`, and pending suggestions as `custom.ai_suggestions`, both published with `ticket.updated`:

```ts
interface Decision { value: string | boolean | number | null; probability: number | null; confidence: number | null; applied: boolean }
interface AIDecisions { engine: "jev" | "llm"; model: string; model_version?: string; threshold: number; decided_at: string;
  fields: Partial<Record<"priority" | "severity" | "category" | "type_label" | "label" | "sentiment" | "language"
    | "spam" | "needs_human" | "team" | "sla_risk", Decision>> }
interface Suggestion { value: string | string[] | boolean; confidence: number | null; probability: number | null; model: string; decided_at: string }
type AISuggestions = Partial<Record<"priority" | "severity" | "labels" | "team" | "spam", Suggestion>>
```

Accept a suggestion by setting the field with `PATCH /v1/tickets/{id}`, which removes it. Dismiss it with `DELETE /v1/tickets/{id}/ai/suggestions/{field}`. A new triage run replaces them. Activities of applied decisions are attributed to `ai`, with data `{ via, model: "jev", confidence }`.

## Rewrite and translate

```sh
curl https://api.opendesk.sh/v1/ai/rewrite \
  -H "Authorization: Bearer $ACCESS_TOKEN" -H "OD-Workspace: $WORKSPACE_ID" \
  -H "Content-Type: application/json" \
  -d '{ "html": "<p>we cant do that</p>", "mode": "more_formal" }'
```

Modes: `friendlier`, `more_formal`, `shorter`, `longer`, `fix_grammar`, `simplify`. `translate` takes `{ html?, text?, target }` and returns `{ html?, text?, source_language }`.

## Streaming

`draft`, `ask` and `rewrite` stream when you send `Accept: text/event-stream`. The response is a series of `delta` events carrying text chunks, then one `done` event carrying the same JSON the non-streaming call returns. The call is charged once, at `done`.

```
event: delta
data: {"text":"Hi Ana, "}

event: delta
data: {"text":"thanks for your patience."}

event: done
data: {"body_html":"<p>Hi Ana, thanks for your patience.</p>","body_text":"Hi Ana, thanks for your patience.","sources":[]}
```

```sh
curl -N https://api.opendesk.sh/v1/tickets/1042/ai/draft \
  -H "Accept: text/event-stream" \
  -H "Authorization: Bearer $ACCESS_TOKEN" -H "OD-Workspace: $WORKSPACE_ID" \
  -H "Content-Type: application/json" -d '{}'
```

## Widget handoff

A visitor can ask for a person after an AI answer with `POST /v1/widget/conversations/{id}/handoff` (visitor token). See [AI features](/guides/ai/#chat-answers).
