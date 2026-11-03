# admin

The instance admin panel: the operator surface of an opendesk instance (`contracts/admin.md`).
A separate Vite app on the same API (`/v1/admin/*`), with its own sign-in (password and TOTP
every time), sessions and audit log. `web/` stays customer-facing; operator screens live here.

| where | address | build |
| --- | --- | --- |
| opendesk Cloud | `https://admin.opendesk.sh` (Pages project `opendesk-admin`) | `pnpm build:pages` |
| self-hosted | `<APP_URL>/admin/`, served by the binary | `make admin-embed` (base `/admin/`) |

```sh
pnpm install
pnpm dev          # http://localhost:5174, against the API on :8080 (VITE_API_URL)
pnpm dev:mock     # the same on an in-memory admin API with sample data (src/mock), no backend
pnpm typecheck && pnpm lint && pnpm test && pnpm build
```

A development API (`make api`) accepts the panel's dev server origin by default
(`ADMIN_URL=http://localhost:5174`). Make yourself an admin first: sign up in the app, turn on
two-factor authentication, then `go run ./cmd/opendesk admin grant you@example.com`.

Its text is translated like the app's (`src/i18n`: i18next, one JSON catalog per namespace and
language, English, French and German, picked from the browser). A new string goes into
`locales/en/<ns>.json` and into every other language; `src/i18n/i18n.test.ts` checks they match.

The access token is kept in memory only; the refresh token is the `od_admin` cookie
(HttpOnly, SameSite=Strict, path `/v1/admin/auth`). Keep it that way.
