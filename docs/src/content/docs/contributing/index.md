---
title: Contributing
description: Set up opendesk for development and send your first change.
---

opendesk is open source under the Apache-2.0 license at [github.com/useopendesk/opendesk](https://github.com/useopendesk/opendesk). Issues, discussions and pull requests are welcome.

## The repository

| Path | What |
| --- | --- |
| `cmd/opendesk`, `internal/` | The Go API and workers, one binary |
| `realtime/` | The Elixir and Phoenix realtime gateway |
| `web/` | The React web app, help center and chat widget (`web/widget/`) |
| `site/` | The marketing site (Astro) |
| `docs/` | This documentation (Astro Starlight) |
| `deploy/` | Dockerfiles, the self-host bundle, Terraform, the Cloudflare Email Worker |
| `contracts/` | The contracts between the parts: API, events, realtime and tokens |
| `migrations/` | SQL migrations, embedded in the binary |
| `brand/` | Logo, icons and design tokens |

Read `AGENTS.md` and the files in `contracts/` before changing an interface between parts.

## Run it locally

You need Go, Node.js with pnpm, Elixir, and Docker for Postgres.

```sh
cp .env.example .env
make dev-db      # Postgres on localhost:55432
make api         # the Go API and workers, with embedded NATS on :4222
make realtime    # the Elixir gateway on :4000
make web         # the web app on :5173
```

`make site` and `make docs` run the marketing site and these docs.

## Conventions

- **Go:** format with `gofmt`, and run `go build ./... && go vet ./...` and `go test ./...` before sending a change. Create tickets and messages only through the shared domain functions.
- **Migrations:** the next six-digit number, with an `.up.sql` and a `.down.sql`.
- **Errors:** return stable error codes, never raw error text, to clients.
- **Events:** writes that others should see publish an event in the same transaction.
- **Web:** monochrome tokens from `brand/tokens.css`, keyboard-first, realtime over polling.
- **Copy:** sentence case, plain words, no em dashes.
- **Security:** keep the [security invariants](/security/). Describe the invariant, never a gap, in code comments, commits and docs.

## Commits and pull requests

Commit messages are one line, such as `feat: add CSAT comment to the ticket sidebar`. Keep pull requests focused, explain why the change is needed, and include tests where they make sense. CI runs the build, tests, linters and dependency audits.

## Documentation

These docs live in `docs/src/content/docs`. Every page has a frontmatter `title` (rendered as the heading) and a `description`. Use sentence case, link internally with trailing slashes (`/api/webhooks/`), and document only behavior the code and contracts define.

```sh
cd docs
pnpm install
pnpm dev
```

## Security issues

Do not open a public issue for a vulnerability. See [Responsible disclosure](/security/disclosure/).
