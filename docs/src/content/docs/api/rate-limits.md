---
title: Rate limits
description: How opendesk limits request rates and the headers that tell you where you stand.
---

opendesk limits how fast clients can call the API, to keep the service fast for everyone. Limits apply per credential (an API key, a signed-in person or a chat widget visitor): 1,200 requests a minute, of which at most 600 may change something (`POST`, `PUT`, `PATCH`, `DELETE`). Sign-in, registration and password reset are additionally limited per IP address and per account.

## Headers

Every response to an authenticated request carries the IETF `RateLimit` headers:

| Header | Meaning |
| --- | --- |
| `RateLimit-Limit` | Requests allowed in the current window. |
| `RateLimit-Remaining` | Requests left in the current window. |
| `RateLimit-Reset` | Seconds until the window resets. |

These headers are exposed to browsers through CORS, so browser clients can read them too.

## When you are limited

A limited request returns `429` with code `rate_limited`:

```json
{
  "status": 429,
  "title": "Too Many Requests",
  "detail": "Too many requests. Wait a moment and try again.",
  "code": "rate_limited"
}
```

Wait for `RateLimit-Reset` seconds, then retry. Spread bulk work over time, and use [`POST /v1/tickets/bulk`](/api/resources/tickets/#bulk-update) instead of one request per ticket.

## Avoiding limits

- Use [webhooks](/api/webhooks/) or [realtime](/api/realtime/) instead of polling for changes.
- Request the largest page size (`limit=100`) when listing.
- Cache data that rarely changes, such as tags, members and views.

Self-hosted instances apply the same limits per process.
