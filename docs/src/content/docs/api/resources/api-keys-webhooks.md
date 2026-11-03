---
title: API keys and webhooks
description: Create API keys and webhook endpoints, inspect deliveries and send test events.
---

Both are settings: an `admin` user, or an API key with `settings:read` and `settings:write`. Creating, changing and deleting either is audit-logged.

| Method | Path | Description |
| --- | --- | --- |
| `GET`, `POST` | `/v1/api-keys` | List or create API keys |
| `GET`, `PATCH`, `DELETE` | `/v1/api-keys/{id}` | |
| `GET`, `POST` | `/v1/webhooks` | List or create webhooks |
| `GET`, `PATCH`, `DELETE` | `/v1/webhooks/{id}` | |
| `GET` | `/v1/webhooks/{id}/deliveries` | Recent deliveries |
| `POST` | `/v1/webhooks/{id}/test` | Send a `webhook.test` event |

## API keys

```ts
type ApiKey = {
  id: string;
  name: string;
  prefix: string;
  scopes: string[];
  last_used_at: string | null;
  expires_at: string | null;
  created_at: string;
  created_by: UserRef | null;
  bot_id: string | null; // the bot the key speaks as on tickets, or null
};
```

```sh
curl https://api.opendesk.sh/v1/api-keys \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "OD-Workspace: $WORKSPACE_ID" \
  -H "Content-Type: application/json" \
  -d '{ "name": "CRM sync", "scopes": ["tickets:read", "contacts:read", "contacts:write"], "expires_at": "2027-01-01T00:00:00Z" }'
```

The response is the key plus `secret`, shown only this once. See [scopes](/api/authentication/#scopes).

### Speak as a bot

When an AI of your own answers customers through the API, give its key a [bot](/guides/bots/) to speak as. Set `bot_id` when you create the key, or later with `PATCH /v1/api-keys/{id}` (`null` takes it off). The key needs `tickets:write`, and the bot must be one of the workspace's.

```sh
curl -X PATCH https://api.opendesk.sh/v1/api-keys/$KEY_ID \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "OD-Workspace: $WORKSPACE_ID" \
  -H "Content-Type: application/json" \
  -d '{ "bot_id": "0192f0c5-..." }'
```

Then every reply and note the key writes is the bot's, with nothing to change in your requests:

- The team sees the bot's name and face with the **AI** label, and which key wrote it (`author.bot`, `author.api_key`).
- An email reply goes out under the bot's name in the address's sender name (`{agent}` in `from_name_format`), marked as automatic mail (`Auto-Submitted`), and ends with the bot's email sign-off. If your text already ends with the sign-off, it is not added twice, so you can leave signing to the bot.
- `{{ agent.name }}` and the other agent variables in a reply are the bot's.
- Messages stay `author.type: "api"`: they are not the built-in AI agent's, so its reports and ratings leave them out.

A bot that a working key speaks as can't be deleted: pick another bot for the key, or revoke it, first.

### Change a key's scopes

Send the new list with `PATCH /v1/api-keys/{id}`: it replaces the key's scopes, and the key's next request already has them. The secret stays the same, so nothing changes where the key is used.

```sh
curl -X PATCH https://api.opendesk.sh/v1/api-keys/$KEY_ID   -H "Authorization: Bearer $ACCESS_TOKEN"   -H "OD-Workspace: $WORKSPACE_ID"   -H "Content-Type: application/json"   -d '{ "scopes": ["tickets:read", "tickets:write"] }'
```

- **Removing a scope** applies at once.
- **Adding a scope** gives a live credential more access, so it asks for what creating a key asks for: a sign-in within the last 12 hours, and the second factor when it is on (`403 reauth_required` / `mfa_required`).
- `collab:draft` drafts replies as the member who created the key: only that member can add it.
- Each change is in the audit log as `api_key.scopes_changed`, with the scopes before and after, and what was added and removed.

In the app: **Settings → API keys**, the key's menu, **Edit permissions**.

## Webhooks

```ts
type Webhook = { id: string; url: string; description: string; events: string[]; enabled: boolean; created_at: string };
type WebhookDelivery = {
  id: string;
  event_id: string;
  event_type: string;
  status: "pending" | "succeeded" | "failed";
  attempts: number;
  response_code: number | null;
  duration_ms: number | null;
  created_at: string;
};
```

```sh
curl https://api.opendesk.sh/v1/webhooks \
  -H "Authorization: Bearer od_live_..." \
  -H "Content-Type: application/json" \
  -d '{ "url": "https://example.com/hooks/opendesk", "description": "Sync to warehouse", "events": ["ticket.created", "message.created"], "enabled": true }'
```

The response includes the signing `secret`, shown once. `events` lists event types, or `["*"]` for all. The URL must be HTTPS and must not point at a private, loopback or link-local address.

See [Webhooks](/api/webhooks/) for payloads, signatures and retries.
