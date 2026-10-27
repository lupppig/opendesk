---
title: Search and reports
description: Search across tickets, contacts, articles and wiki pages, and read the reports overview and the agents report.
---

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/search?q=` | Top tickets, contacts, articles and wiki pages |
| `GET` | `/v1/reports/overview?from=&to=` | Metrics for a date range |
| `GET` | `/v1/reports/agents?from=&to=` | Each agent's work for a date range |

## Search

```sh
curl "https://api.opendesk.sh/v1/search?q=refund" -H "Authorization: Bearer od_live_..."
```

```json
{ "tickets": [ ... ], "contacts": [ ... ], "articles": [ ... ], "wiki_pages": [ ... ] }
```

Up to five of each, of the kinds the key's scopes can read. For more tickets, use `GET /v1/tickets?q=`; for more wiki pages, [`GET /v1/wiki/search?q=`](/api/resources/wiki/).

## Reports overview

```sh
curl "https://api.opendesk.sh/v1/reports/overview?from=2026-09-01&to=2026-09-30" \
  -H "Authorization: Bearer od_live_..."
```

```json
{
  "created": 412, "solved": 398, "open_now": 23,
  "first_response_p50_minutes": 14, "resolution_p50_minutes": 185,
  "csat_avg": 4.6, "csat_count": 121, "sla_breached": 6,
  "by_day": [{ "date": "2026-09-01", "created": 15, "solved": 13 }],
  "by_channel": { "email": 301, "chat": 98, "api": 13 },
  "by_severity": { "sev1": 2, "sev2": 9, "sev3": 40, "sev4": 77, "none": 284 },
  "by_agent": [{ "user": { "id": "...", "name": "Sam", "email": "sam@example.com", "avatar_url": "" }, "solved": 120, "first_response_p50_minutes": 11 }]
}
```

See [Reports](/guides/reports/) for what each metric means.

## Agents report

```sh
curl "https://api.opendesk.sh/v1/reports/agents?from=2026-09-01&to=2026-09-30"   -H "Authorization: Bearer od_live_..."
```

```json
{
  "data": [{
    "user": { "id": "...", "name": "Sam", "email": "sam@example.com", "avatar_url": "" },
    "assigned": 131, "solved": 120, "replies": 214, "notes": 37, "replies_per_solved": 1.6,
    "first_response_p50_minutes": 11, "handle_p50_minutes": 142, "online_minutes": null
  }],
  "online_time": "none"
}
```

One row per owner, admin and agent, most solved first, narrowed like the overview by `team_id` and `brand_id`. Online time is personal: `online_minutes` is filled in for every row only for API keys with the `settings:read` scope (`online_time` is then `all`). Signed in, owners and admins get every row and members their own row only (`online_time` is then `self`). See [Agents](/guides/reports/#agents) for what each column counts.
