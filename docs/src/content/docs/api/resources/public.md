---
title: Public help center and CSAT
description: Unauthenticated endpoints for the public help center and customer satisfaction ratings.
---

These endpoints need no credentials. They are what the help center and the CSAT page use, and you can use them to build your own.

## Help center

Only published articles are returned.

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/public/help/{workspace_slug}` | `{ title, collections, featured }` |
| `GET` | `/v1/public/help/{workspace_slug}/collections/{slug}` | `{ collection, articles }` |
| `GET` | `/v1/public/help/{workspace_slug}/articles/{slug}` | The article; counts a view |
| `GET` | `/v1/public/help/{workspace_slug}/search?q=` | Matching articles |

```sh
curl https://api.opendesk.sh/v1/public/help/example/search?q=password
```

The rendered help center is served at its own address, not under `/v1` ([Help center addresses](/guides/help-center-addresses/)). For a proxy that issues certificates on demand, `GET /v1/public/help-domains/tls?domain=<hostname>` answers `200` when the instance serves a help center on that hostname and `404` otherwise.

## CSAT

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/v1/public/csat/survey` | The rating request, from `{ token }` |
| `POST` | `/v1/public/csat/rating` | `{ token, score, comment? }` |

The token comes from the link in the rating email. See [CSAT](/guides/csat/).
