# Cloudflare Pages projects

Four direct-upload Pages projects, created by Terraform
(`deploy/terraform/modules/cloudflare`) with their custom domains. They are **not**
connected to git: `deploy.yml` builds each one in GitHub Actions and uploads the
output with `wrangler pages deploy`. Cloudflare never builds anything, so the build
runs with the same Node, pnpm and lockfile as CI.

| Project | Source | Build (in the source dir) | Output | Domain |
| --- | --- | --- | --- | --- |
| `opendesk-site` | `site/` (Astro) | `pnpm install --frozen-lockfile && pnpm build` | `site/dist` | `opendesk.sh` |
| `opendesk-docs` | `docs/` (Starlight) | `pnpm install --frozen-lockfile && pnpm build` | `docs/dist` | `docs.opendesk.sh` |
| `opendesk-app` | `web/` (Vite) | `pnpm install --frozen-lockfile && pnpm build` | `web/dist` | `app.opendesk.sh` |
| `opendesk-admin` | `admin/` (Vite) | `pnpm install --frozen-lockfile && pnpm build:pages` | `admin/dist` | `admin.opendesk.sh` |

The files in this directory are copied into the app build output before upload,
unless that build already ships its own. Every project also gets the site's
`security.txt` (`site/public/.well-known/security.txt`) at `/.well-known/security.txt`.

## opendesk-site

The site owns its headers (`site/public/_headers`, with the CSP hashes filled in
by `site/scripts/postbuild.mjs`) and publishes the self-host installer itself:
`site/scripts/prebuild.mjs` copies `deploy/install/install.sh` and its checksum
into `public/`. Once a release exists, `deploy.yml` replaces both in `dist/` with
that release's copies, so `opendesk.sh/install.sh` and the newest release's
`install.sh.sha256` always agree:

```sh
(cd site && pnpm install --frozen-lockfile && pnpm build)
gh release download --pattern install.sh --pattern install.sh.sha256 --dir site/dist --clobber
(cd site/dist && sha256sum -c install.sh.sha256)
wrangler pages deploy site/dist --project-name opendesk-site --branch main
```

## opendesk-docs

The docs own their headers (`docs/public/_headers`): a CSP with the hashes of the
build's inline scripts, filled in by `docs/scripts/postbuild.mjs` like the site's.

```sh
(cd docs && pnpm install --frozen-lockfile && pnpm build)
wrangler pages deploy docs/dist --project-name opendesk-docs --branch main
```

## opendesk-app

The web app, the help center and the widget (`/widget/v1.js`) in one build.

```sh
(cd web && pnpm install --frozen-lockfile && pnpm build)
cp deploy/cloudflare/pages/app/_headers deploy/cloudflare/pages/app/_redirects web/dist/
node deploy/cloudflare/pages/app/write-config.mjs web/dist
wrangler pages deploy web/dist --project-name opendesk-app --branch main
```

Runtime config: the self-hosted binary answers `/config.js` and `/config.json`
itself. On Pages, `write-config.mjs` writes the same files: `config.js`
(`window.__OPENDESK__ = {apiUrl, gatewayUrl, posthogKey?, posthogHost?,
turnstileSiteKey?, selfHosted: false, mailboxOAuth?}`) for the web app, and `config.json`, the
same object as JSON, which the desktop and iPhone apps read to find the API
(`contracts/native.md`). With `APPLE_APP_IDS` set it also writes
`.well-known/apple-app-site-association`: `webcredentials` (passkeys on
app.opendesk.sh) and `applinks` (ticket links `/inbox/*/<id>`, `/views/*/<id>`
and `/tickets/<id>` open in the iPhone app) for those app ids. Env vars:

| Env | Default |
| --- | --- |
| `APP_API_URL` | `https://api.opendesk.sh` |
| `APP_GATEWAY_URL` | `wss://gateway.opendesk.sh/socket` |
| `WEB_POSTHOG_KEY` | unset (no analytics) |
| `WEB_POSTHOG_HOST` | unset |
| `TURNSTILE_SITE_KEY` | unset |
| `APPLE_APP_IDS` | unset (no apple-app-site-association). Comma-separated `TEAMID.bundle` |
| `MAILBOX_OAUTH` | unset (the wizard follows the API). `google`, `microsoft`, `google,microsoft` or `none`: the providers whose mailbox sign-in the wizard offers. deploy.yml sets it from the repository variable, default `microsoft`. Keep it in step with the API's `GOOGLE_MAILBOX_OAUTH` and `MICROSOFT_MAILBOX_OAUTH` |

Test it with `node --test deploy/cloudflare/pages/app/write-config.test.mjs`.

Headers (`app/_headers`) match what the Go binary sends for the same files:

- every path: CSP (`connect-src` the API, the gateway, PostHog and Turnstile;
  `frame-ancestors 'none'`), `X-Frame-Options: DENY`, nosniff, HSTS, referrer policy
- `/assets/*`: `public, max-age=31536000, immutable` (content-hashed)
- `/widget/*`: `Access-Control-Allow-Origin: *`, `max-age=300`, no `X-Frame-Options`
- `/config.js`, `/config.json`: `no-store`
- `/.well-known/apple-app-site-association`: `Content-Type: application/json`
  (the file has no extension), `max-age=3600`
- HTML: Pages' default `max-age=0, must-revalidate`

SPA fallback needs no rule: an output with no top-level `404.html` makes Pages serve
`index.html` for every path that is not a file.

## opendesk-admin

The instance admin panel (`contracts/admin.md`), on its own origin so no page of the app
ever runs where an admin session lives. `pnpm build:pages` builds it with base `/`, copies
`admin/_headers` (CSP: scripts from itself and Turnstile, API calls to the API only,
`X-Robots-Tag: noindex`), `_routes.json` and `_worker.js` (missing assets answer an
uncached 404, as for the app) and writes `config.js`
(`window.__OPENDESK_ADMIN__ = {apiUrl, appUrl, turnstileSiteKey?, envLabel?}`) with
`admin/write-config.mjs`:

```sh
(cd admin && pnpm install --frozen-lockfile && ADMIN_ENV_LABEL=Production pnpm build:pages)
wrangler pages deploy admin/dist --project-name opendesk-admin --branch main
```

| Env | Default |
| --- | --- |
| `ADMIN_API_URL` | `https://api.opendesk.sh` |
| `ADMIN_APP_URL` | `https://app.opendesk.sh` |
| `TURNSTILE_SITE_KEY` | unset (no Turnstile widget). Add `admin.opendesk.sh` to the widget's hostnames |
| `ADMIN_ENV_LABEL` | unset. Shown in the panel's top bar |

Test it with `node --test deploy/cloudflare/pages/admin/write-config.test.mjs`. A
self-hosted binary serves the same panel at `<APP_URL>/admin/` (`make admin-embed`).

## Previews

Because the projects are direct upload, a preview is just another branch:

```sh
wrangler pages deploy web/dist --project-name opendesk-app --branch pr-123
# → https://pr-123.opendesk-app.pages.dev
```

A preview of the app talks to production unless `APP_API_URL` and `APP_GATEWAY_URL`
point elsewhere when `write-config.mjs` runs, and the API only accepts its own
`CORS_ORIGINS`, so previews of the app are for the UI, not for signing in.

## Credentials

`deploy.yml` uses a Cloudflare API token scoped to the account with
**Cloudflare Pages: Edit** and **Workers Scripts: Edit** (for the email and help
workers), and on the `opendeskhelp.com` zone **Zone: Read** and **Workers Routes:
Edit** (the help worker's routes), stored as the `CLOUDFLARE_API_TOKEN` secret,
plus `CLOUDFLARE_ACCOUNT_ID` as a variable.

`app/_redirects` sends old help center links, `app.opendesk.sh/help/<slug>/...`,
to `https://<slug>.opendeskhelp.com/...` with a `301`.
