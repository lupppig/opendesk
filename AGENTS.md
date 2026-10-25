# opendesk agent notes

opendesk is an open-source helpdesk: shared email inboxes, live chat, a knowledge base,
and a public API, with everything updating live across the team. It ships as a hosted
service (opendesk.sh) and as a self-host, and **the two are the same code**.

## System shape

| path | what | where it runs |
| --- | --- | --- |
| `cmd/opendesk`, `internal/` | Go API + workers, one binary, `OPENDESK_ROLE=all\|api\|worker` | `api.opendesk.sh` (AWS ECS) |
| `realtime/` | Elixir/Phoenix realtime gateway; no database | `gateway.opendesk.sh` (AWS ECS) |
| `web/` | React app (Vite, TanStack Router/Query, Tailwind v4), plus the help center and widget | `app.opendesk.sh` (Cloudflare Pages) |
| `web/widget/` | embeddable chat widget bundle | `app.opendesk.sh/widget/v1.js` |
| `admin/` | instance admin panel (React, same stack as `web/`); the operator surface, never a customer's; `contracts/admin.md` | `admin.opendesk.sh` (Cloudflare Pages); self-host: `/admin/` from the binary |
| `desktop/` | Electron shell that loads the instance's web app (notifications, badge, deep links, translucent chrome); `contracts/native.md` | GitHub Releases |
| `ios/` | native SwiftUI iPhone app (`sh.opendesk.app`); `contracts/native.md` | App Store / TestFlight |
| `site/` | Astro marketing site | `opendesk.sh` (Cloudflare Pages) |
| `docs/` | Starlight docs | `docs.opendesk.sh` (Cloudflare Pages) |
| `deploy/` | Dockerfiles, self-host compose + Caddy, Terraform (AWS, Cloudflare), Cloudflare Email Worker | |
| `contracts/` | **the contracts between parts**: API, events, realtime, tokens. Read before changing an interface | |
| `migrations/` | SQL migrations, embedded in the binary, applied at boot | |
| `brand/` | logo, icons, tokens | |
| `qa/` | `pnpm qa`: CI's checks for what a branch changes, run locally; `qa/README.md` | your machine |

Data: Postgres. Hosted = **PlanetScale Postgres**, reached only over AWS PrivateLink
(`DATABASE_URL` is PgBouncer on 6432 for request handlers; `DATABASE_URL_DIRECT` is the
direct connection on 5432 for River and the event relay, because they need session
features like LISTEN/NOTIFY). The services connect as a role that reads and writes data
but cannot change the schema; only the migration task has DDL (`DATABASE_URL_MIGRATE`
in Terraform). Self-hosted = stock Postgres. Never use a provider-only feature.

Events: the transactional outbox (`events` table) → relay → NATS `od.events.<wid>` →
Elixir gateway → browsers; and → webhooks. NATS runs **embedded** in the Go binary for
self-hosters (listening on `NATS_LISTEN` so the gateway can connect) and as a cluster in
the cloud (`NATS_URL`). Background jobs: River on Postgres. No Redis, no Kafka.

Self-hosted = `opendesk` + `realtime` + `postgres` behind Caddy on one domain: `/socket`
→ realtime, everything else → opendesk, which also serves the web app (`SERVE_WEB=true`).

## Go conventions

- Areas own packages: `internal/core` (auth, accounts, workspaces, members, API keys,
  webhooks, audit, notifications, events API, realtime tokens), `internal/email`
  (mailboxes, inbound, outbound), `internal/helpdesk` (tickets, contacts, KB, widget,
  SLA, automations, reports), `internal/billing` (Stripe, plans; Cloud only),
  `internal/ai` (AI features, providers, embeddings) and `internal/integrations`
  (connected products over MCP, ticket links; `contracts/integrations.md`), `internal/supportchannels`
  (Slack, Discord and Telegram threads where agents work tickets; `contracts/support-channels.md`), `internal/admin`
  (the instance admin API `/v1/admin/*`, its own sessions and audit log; `contracts/admin.md`),
  and `internal/collab` (Collab: members' own mail accounts, private or shared, synced both ways
  with Gmail, Microsoft Graph or IMAP; never tickets, except answers to a ticket reply a member
  sent from their own address; `contracts/collab.md`). Plan limits and AI usage go
  through `d.Plan` (`internal/plan`, `contracts/billing.md`); AI through `internal/ai`
  (`contracts/ai.md`). Each exposes `Register`, `RegisterWorkers`, `Periodic`,
  `Background` (see `internal/app`). Shared ticket writes live in `internal/domain`:
  **always** create tickets and messages through `domain.CreateTicket` / `domain.AddMessage`.
- HTTP is huma v2 on chi. Handlers look like:

  ```go
  huma.Register(api, huma.Operation{OperationID: "get-ticket", Method: http.MethodGet,
      Path: "/v1/tickets/{id}", Summary: "Get a ticket", Tags: []string{"Tickets"}},
      func(ctx context.Context, in *struct{ ID string `path:"id"` }) (*struct{ Body domain.Ticket }, error) {
          p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeTicketsRead)
          if err != nil { return nil, httpx.Wrap(err) }
          t, err := domain.LoadTicket(ctx, d.DB.Pool, p.WorkspaceID, id)
          if err != nil { return nil, httpx.Wrap(err) }
          return &struct{ Body domain.Ticket }{t}, nil
      })
  ```
- Return `httpx.Wrap(err)` or an `httpx.E(status, code, detail)`; never a raw error string
  to the client. Unknown errors become a logged 500 with a fixed message and request id.
- Customer-facing text (replies, signatures, auto-replies, layouts, CSAT) is written and
  filled with `internal/templates` (`contracts/templates.md`): one variable list, one-pass
  filling, the design allowlist. Never fill `{{ }}` with string replacement elsewhere.
- Writes that others should see run in one transaction with `events.Publish`. Follow-up
  work is enqueued with `d.Jobs.InsertTx` in that same transaction. Job args live in
  `internal/jobs/args.go`.
- Migrations: `.up.sql` and `.down.sql`, numbered above the highest existing migration
  whatever the area (a lower number is never applied to an existing database). The first
  build used per-area ranges: core `0001xx`, email `0002xx`, helpdesk `0003xx`, billing
  `0004xx`, ai `0005xx`; those are closed.
- Run `gofmt -w` on touched files, then `go build ./... && go vet ./...`.

## Cloud and self-hosted

The cloud is the self-host plus configuration. Billing is on only when `STRIPE_SECRET_KEY`
is set; without it every workspace is `self_hosted` with no limits. AI is on when a provider
key is set (`ANTHROPIC_API_KEY`, or any OpenAI-compatible endpoint, including local models).
Never gate a feature on plan: plans differ only in limits (`contracts/billing.md`). The one
exception is Collab, which is the collab plan's (`plan.Entitlements.Collab`, `d.Plan.RequireCollab`);
a self-hosted instance has it for every workspace unless `COLLAB=false`.
Customer replies go out through each mailbox's own Google, Microsoft or SMTP connection; the
platform mailer (SES on the cloud) sends only platform mail. Sending replies through it is an
instance option (`MAILBOX_SYSTEM_SEND`), off on the cloud.

## Deploying

Merging to `main` deploys opendesk.sh on its own (`.github/workflows/deploy.yml`): Terraform plans and
applies (a new task definition revision applies by itself; deleting or replacing anything else waits for
a reviewer), the images build, migrations run, api, worker and gateway roll, and the Pages projects
deploy. Production settings (`terraform.tfvars`, such as `ai_model`) are not in git: CI plans with the
copy in the state bucket. Change them with `scripts/tfvars.sh pull`, edit, `scripts/tfvars.sh push`;
an apply from a local copy alone is undone by the next deploy (`deploy/terraform/README.md`).

## Security and compliance invariants (CASA Tier 1 / OWASP ASVS L1)

opendesk Cloud connects Google mailboxes send-only (`gmail.send`); IMAP sync, which needs the
restricted `https://mail.google.com/` scope, is an instance option (`MAILBOX_IMAP`) for
self-hosters with their own Google client. Collab mail accounts (`gmail.modify`) connect through
`COLLAB_GOOGLE_CLIENT_ID`, on opendesk Cloud a Google project of its own, so the mailbox
client's consent stays `gmail.send`. The invariants below follow **ADA CASA Tier 1**
(ASVS 4.0.3 Level 1) and hold for every change. The repo is public and self-hosted:
**describe the invariant, never the gap** in comments, commits and docs.

Authentication
- Passwords: Argon2id only (`auth.HashPassword`). `auth.CheckPassword` is the single gate
  for every new or changed password (12..128 chars, refuses the embedded breached list):
  registration, reset, change, invite acceptance.
- Login, registration and password reset are behind Turnstile (`d.Captcha.Verify`), or an
  App Attest proof from the iPhone app, and rate limited per IP and per account (`d.Limiter`).
  Per-account limits on a sign-in factor (password, TOTP, step-up) count in Postgres for the
  whole instance (`d.Limiter.Shared`), and only after the human check. Login answers the same
  for unknown users and wrong passwords, in the same time (`auth.DummyVerify`); a password
  that no longer passes `auth.CheckPassword` signs in with `change_password`.
- A change to how someone signs in (password changed or reset, 2FA on or off, passkey added
  or removed, provider linked) emails them in the same transaction (`notifySecurity`).
  Adding or removing a factor needs a recent sign-in.
- TOTP stores the last consumed step and refuses its replay. Recovery codes are hashed
  and single use.
- Federated identities bind by `(issuer, subject)`. An existing password account is linked
  to a new identity only after its password is presented.
- Owner-level operations (delete workspace, transfer ownership, rotate the widget secret,
  disable 2FA) require `auth.RequireMFA` and a recent login.
- The instance admin panel signs in with password and TOTP every time, has its own sessions
  (`admin_sessions`, purpose `admin`, 12 h cap) and accepts only `ADMIN_URL`'s origin. A
  workspace role never grants an admin permission, and admin routes never accept an app token.
  Operator actions go through `core.Ops`, so they remove and end exactly what the app's own do.

Sessions and tokens
- Every JWT has a `purpose`, verified against the one purpose its consumer accepts
  (`contracts/tokens.md`). HS256 only, `exp` required. `AUTH_SECRET` >= 32 bytes or no boot.
- Refresh tokens rotate on use and are stored hashed. Browsers only ever get them as a
  cookie; a response body carries one only for a native request (`OD-Client`, no `Origin`).
  Push devices are bound to a session and removed with it. Logout, password change, member
  removal and session revocation revoke sessions **and** publish `od.control.disconnect`.
- An access token works only while its session is live and its user not suspended: the
  resolver checks both on every request. Sessions end 30 days after sign-in at most.
- Cookies: HttpOnly, Secure (https), SameSite=Lax or Strict; session cookies are `__Host-`
  prefixed on https. Refresh cookies are read only from an app origin (or no `Origin`).
- Tokens never ride in API URLs: the realtime token goes in `Sec-WebSocket-Protocol` (the
  gateway reads no query token), public token lookups and CSAT take a body. `/v1/`
  responses are `no-store` unless their route says otherwise.
- Every credential (API key, person, widget visitor) is rate limited across the API
  (`httpx.CredentialLimit`, `RateLimit-*` headers); unresolved credentials per address.

Access control
- Every workspace-owned row is read and written with `workspace_id = $1` from the
  Principal, never from the request body. A referenced id (contact, team, tag, mailbox,
  attachment) is checked to belong to the same workspace before use.
- Viewers read only; settings routes need admin; API keys need the matching scope.
- Tickets of a private team (`contracts/teams.md` "Visibility"): every ticket read and write
  goes through domain visibility (`domain.LoadVisibleTicket`/`CanSee` for one ticket,
  `domain.HiddenTeams` with `VisibleSQL`/`VisibleSQLUser` for lists); a hidden ticket is `404`.
  Events about a ticket set `events.Event.TicketID`; live previews and realtime topics carry
  its private team. New tickets get their team through `routing.Resolve`.

Input and output
- Names people choose go through `internal/displayname` (users, workspaces, teams,
  contacts typed by agents). Untrusted names (email headers, IdP claims) use `Clean`.
- Files from outside are type-checked and never run: executables and scripts are refused
  (`hd.IsExecutable`), active types are stored as plain text (`hd.DetectType`), and downloads
  are attachments with `nosniff` and a sandbox CSP (CASA 5.2.1).
- Email HTML and agent HTML are sanitized with bluemonday before storage. Never render
  stored HTML without that.
- Inbound email renders in a sandboxed frame without scripts, and its images from other
  sites (including CSS `url()`) load only when the reader asks (`web/src/lib/sanitize.ts`).
  Collab mail is inbound email: the same sanitizing before storage and the same frame.
- A private Collab account and its mail are visible to the member who connected it alone,
  admins included; Collab events go only to the people who see what changed (`events.Event.Users`).
  Its mail reaches a ticket only by the member's choice: answers to a ticket reply sent from their
  address are copied to that ticket (contracts/collab.md "Answering tickets").
- Events carry only what every receiver may read: a notification goes to its recipient
  (`Users`), settings whose reads need admin to admins (`events.Event.Admins`).
- Webhook and other outbound URLs are HTTPS and refuse private, loopback and link-local
  targets (SSRF), except on a development instance. A webhook gets events only once its
  URL answered the `webhook.verification` challenge (CASA 7.1.2); payloads are signed
  (HMAC-SHA256 with a timestamp). Webhooks we receive are verified alike: Stripe's, and
  inbound email (`OD-Inbound-Signature`, five-minute window).

Errors, logging, data
- No secret, token, password, credential or full DSN in a log line, error or analytics
  event. Request logs never include query strings or bodies. Only typed, safe messages
  (`httpx.Error`, a provider's own refusal) reach a client; anything else is a logged 500.
- Outside a development instance on localhost there are no built-in secrets: `AUTH_SECRET`
  and `CREDENTIALS_KEY` are required.
- Mailbox credentials, OAuth tokens, webhook secrets, TOTP secrets: AES-GCM via `d.Box`. Every
  column that stores `d.Box` output is in `internal/sealed.Columns`, so a replaced
  `CREDENTIALS_KEY` (`CREDENTIALS_KEY_PREVIOUS`) re-encrypts it.
- Security events are audit-logged: login, failed login, 2FA changes, password change,
  member and role changes, API key and webhook changes, mailbox and integration connect/disconnect.
- Users can delete their account and owners their workspace (data removed, not hidden).
- HTTP responses carry `httpx.SecurityHeaders`; the gateway checks `Origin`.
- CI runs `govulncheck`, `pnpm audit`, `mix deps.audit`, CodeQL, gitleaks and a Trivy image
  scan that gates the deploy. Disclosure: `SECURITY.md` and `/.well-known/security.txt`.
- Releases sign `SHA256SUMS` with the release key (`deploy/install/release-signing.pub`,
  secret `RELEASE_SIGNING_KEY`); the installer and `opendesk-ctl` verify it and pin image
  digests. Desktop apps install updates themselves only where the code signature is checked.

## Web conventions (`web/`, `site/`, `docs/`)

- Clean and white-first: layered surfaces (grey chrome and sidebar behind white panels), the
  brand greys from `brand/tokens.css`, ink primary buttons, slightly square radii, Geist +
  Geist Mono. Colour is semantic and soft (tinted pills): status, priority, severity, SLA,
  labels, sentiment, AI (violet). The palette lives in the web tokens; the site matches it.
- In the app the AI is **Ollie** (the violet blob): the assistant, triage, drafts and every AI
  action are attributed to Ollie. Ollie is also the default customer-facing bot; workspaces build
  their own bots (name, look, personality; `contracts/bots.md`), and visitors and email recipients
  see the widget's or mailbox's bot. Bot looks render through the one shared renderer
  (`web/src/lib/botlook.ts`, Go twin in `internal/ai/bots`). Jev (TypeSafe) is a decision model
  behind triage, never a name in the UI.
- Motion is subtle and fast (shared presets in `web/src/lib/motion.ts`), always honours
  reduced motion. Empty pages use the shared EmptyState, never a lone icon and one line.
  A clock never ticks high in the tree: a live duration runs `useNow` in the small component
  that shows it, and app-wide state wakes on a timer for the moment it changes, so nothing
  redraws the shell every second and stutters an animation.
- Light and dark themes. Dense, calm, keyboard-first. Learn from `~/projects/warmbly/web`
  for patterns (drawers, pickers, confirm dialogs, optimistic updates, realtime
  invalidation), but restyle everything to the opendesk palette.
- Never native `confirm`/`alert`, native checkboxes or number spinners. Every popover
  closes on click-away and Escape. A page is routed, linked, titled and prefetched.
  Tooltips open after 200 ms (`TooltipProvider` in `web/src/app/RootLayout.tsx`).
- A conversation's next step is always in reach, never at the end of its scroll: Collab pins
  the comment field, the comment count and the reply buttons under the thread
  (`CommentBar` in `web/src/components/collab/Comments.tsx`). It is one line until it is used,
  keeps an unsent comment per conversation, and steps the reply buttons aside while a reply is
  open. A new view with a conversation and a way to answer it follows the same rule.
- Prefer realtime over polling: apply socket events to the TanStack Query cache.
- Copy: sentence case, plain words, no em dashes.
- The social card (`site/public/og.jpg`, also the docs' and GitHub's) is the hero's app window
  on its painting. After changing that window, run `pnpm og` in `site/` and commit the copies.

## QA on pull requests

`pnpm qa` in `qa/` runs CI's checks for every area the branch changes, on the committed HEAD
(`qa/README.md`). Run it before opening a pull request and after every follow-up commit: fix what
fails, commit, run it again.

Never merge with `--admin` or change rulesets: those are the maintainer's (the hook in
`.claude/settings.json` refuses both).

## Commits

`feat: <specific one-line description>`. One line, no body, no AI attribution footers.
