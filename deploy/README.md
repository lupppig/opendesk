# Deploying opendesk

opendesk ships two ways from the same code and the same images:

- **Self-hosted**: one machine, one domain, `curl -fsSL https://opendesk.sh/install.sh | sh`.
- **Hosted** (opendesk.sh): AWS ECS Fargate, PlanetScale Postgres over AWS PrivateLink, Cloudflare in front.

```
deploy/
  docker/
    opendesk.Dockerfile        Go binary (API + workers + embedded NATS) with the web app embedded
    realtime.Dockerfile        the Phoenix gateway release (+ entrypoint, healthcheck)
  caddy/Caddyfile              self-host: automatic HTTPS, /socket* -> realtime, rest -> opendesk,
                               on-demand certificates for help center hosts
  self-host/
    .env.template              every self-host setting, documented
    gen-env.sh                 .env with fresh secrets (make selfhost-env)
    opendesk-ctl               status, logs, backup, restore, upgrade
  install/
    install.sh(.sha256)        the one-command installer, published at opendesk.sh/install.sh
    stage-assets.sh            packages what the installer downloads from a release
  cloudflare/
    email-worker/              in.opendesk.sh mail -> POST /v1/inbound/email
    help-worker/               <slug>.opendeskhelp.com and custom domains -> the API's help centers
    pages/                     _headers, runtime config (config.js, config.json, apple-app-site-association)
                               and notes for the three Pages projects
  terraform/                   AWS + Cloudflare + PlanetScale, see terraform/README.md
docker-compose.yml             (repo root) the self-host stack the installer runs
.github/workflows/             ci, release, deploy, security
```

## Architecture (hosted)

```
                                 Cloudflare (DNS, proxy, WAF, TLS)
  browsers ───────────┬───────────────┬───────────────────┬────────────────────┐
                      │               │                   │                    │
               opendesk.sh    docs.opendesk.sh    app.opendesk.sh       api.opendesk.sh
                Pages:site      Pages:docs       Pages:app + widget    gateway.opendesk.sh
                                                                             │
                                                                             ▼
  mail to *@in.opendesk.sh                                ┌──── AWS us-east-1, one VPC, public subnets ────┐
        │                                                 │                                                 │
        ▼                                                 │   ALB (ACM cert, Cloudflare IPs only,           │
  Email Routing catch-all                                 │        idle timeout 600s for WebSockets)        │
        │                                                 │      │ host api.*          │ host gateway.*     │
        ▼                                                 │      ▼                     ▼                    │
  Email Worker ── POST /v1/inbound/email?to=… ──────────► │   ECS opendesk-api      ECS opendesk-gateway   │
  (OD-Inbound-Signature)                                  │   (Go, role=api) ×1-3   (Elixir) ×1-2          │
                                                          │      │                     │  Erlang dist,      │
                                                          │      │   ECS opendesk-worker  Presence across  │
                                                          │      │   (Go, role=worker:   nodes (Cloud Map)  │
                                                          │      │    River jobs, outbox relay)             │
                                                          │      │          │          │                    │
                                                          │      ▼          ▼          ▼                    │
                                                          │   NATS: nats-1, or a cluster of three           │
                                                          │   (ECS + Cloud Map, opendesk.internal)          │
                                                          │                                                 │
                                                          │   S3 attachments · SES (DKIM, MAIL FROM)        │
                                                          │   PrivateLink endpoint (5432, 6432) · KMS · ECR │
                                                          └──────────────────────┬──────────────────────────┘
                                                                                 │ AWS PrivateLink, TLS,
                                                                                 │ never the public internet
                                                                                 ▼
                                                          PlanetScale Postgres (us-east-1, branch "main")
                                                            single node, or HA: one setting in its dashboard
                                                            DATABASE_URL          = app role, PgBouncer 6432
                                                            DATABASE_URL_DIRECT   = app role, direct 5432
                                                                                    (River, relay: LISTEN/NOTIFY)
                                                            DATABASE_URL_MIGRATE  = migrate role (DDL), direct;
                                                                                    the migration task only
                                                          Secrets Manager, CloudWatch alongside
```

Help centers have a zone of their own, `opendeskhelp.com`, on the same Cloudflare
account:

```
  <slug>.opendeskhelp.com ────────────┐
  help.customer.com ── CNAME ──► custom.opendeskhelp.com (Cloudflare for SaaS
                                      │                   custom hostname + certificate)
                                      ▼
                        help worker (route */*), Workers cache
                                      │  OD-Help-Host, OD-Help-Client-IP, OD-Help-Secret
                                      ▼
                        https://api.opendesk.sh ──► the API renders that host's help center
```

The apex and `www` redirect to opendesk.sh. `origin.opendeskhelp.com` is the custom
hostname fallback origin; the worker is the real origin, so it and the other
hostnames are originless `AAAA 100::` records. The API adds and removes custom
hostnames itself with `CLOUDFLARE_SAAS_TOKEN` (contracts/teams.md "Help center
addresses").

Events: a write commits its row and an `events` outbox row in one transaction;
the worker's relay publishes it to NATS `od.events.<wid>`; every gateway node
subscribes and pushes it to the browsers on `workspace:<wid>`; webhooks go out
from River jobs.

### Why there is no NAT Gateway

Tasks run in **public subnets with a public IP**, and their security groups
admit nothing from the internet: the api and gateway accept only the ALB, NATS
only the app tasks, the worker and migrate tasks nothing at all. The ALB itself
accepts only Cloudflare's published IP ranges.

The alternative, private subnets, needs a path out, and opendesk needs the
internet, not only AWS: Google and Microsoft OAuth, Gmail and Graph for
sending replies, customers' SMTP servers and webhook endpoints, and PostHog.
VPC interface endpoints (about $7 a month each, per AZ) would cover ECR, Secrets
Manager, SES and CloudWatch but none of those. A NAT Gateway costs about $33 a
month per AZ plus $0.045 per GB, which is most of what the compute costs at this
size, for no reduction in what can reach the tasks. S3 traffic uses the free
gateway endpoint, and Postgres goes through the PlanetScale PrivateLink
endpoint, so database traffic never leaves the AWS network either.

Move to private subnets with a NAT Gateway when a customer requires fixed
egress IPs (allow-listed webhook sources or SMTP servers): that is a change to
`modules/network` and the services' `assign_public_ip`.

## First-time cloud setup

Do these once, in order. Values in `<angle brackets>` are yours.

### 1. Accounts and tools

- An AWS account with an admin role for Terraform (SSO recommended), the AWS
  CLI, Terraform >= 1.10, Node 24 and pnpm 12.
- A Cloudflare account with the `opendesk.sh` zone active, and the help center
  zone `opendeskhelp.com` active in the same account (the Free plan is enough).
  On that zone, enable Cloudflare for SaaS once: **SSL/TLS > Custom Hostnames >
  Enable**. Terraform sets the fallback origin, which Cloudflare accepts only
  once custom hostnames are enabled. Note the zone id for `help_zone_id`.
- A PlanetScale account. Create the database in its dashboard: **Postgres**,
  region **AWS us-east-1** (the same as `aws_region`; PrivateLink is per
  region), the size you want and single node or high availability (see
  [Sizing and cost](#sizing-and-cost)). Note the organization and database
  names for `terraform.tfvars`. Then create a service token (Settings >
  Service tokens) with access to that database's roles for Terraform.
- A GitHub repository `useopendesk/opendesk` with an environment named
  `production` (Settings > Environments). Restrict it to the `main` branch and
  tags `v*` and,
  if you like, add required reviewers: every deploy runs in it.

### 2. Cloudflare API token

Create one token (My Profile > API Tokens > Create custom token) used by both
Terraform and CI, scoped to the opendesk.sh and opendeskhelp.com zones and the
account:

- Zone (opendesk.sh): DNS Edit, Zone Settings Edit, Zone Edit (for HSTS and
  security settings), Email Routing Rules Edit
- Zone (opendeskhelp.com): Zone Read, DNS Edit, Zone Settings Edit, SSL and
  Certificates Edit (the custom hostname fallback origin), Workers Routes Edit
  (the help worker's routes, set by `wrangler deploy`)
- Account: Cloudflare Pages Edit, Workers Scripts Edit, Email Routing Addresses Edit

The API needs a second, narrower token at runtime, created in step 6.

Note the account id (any zone's overview page, right column).

### 3. Deploy the workers first

Terraform's catch-all rule points at the email worker by name, and Cloudflare
refuses a rule for a worker that does not exist. The help worker is deployed
now too, so its secret can be set in step 6:

```sh
export CLOUDFLARE_API_TOKEN=<token> CLOUDFLARE_ACCOUNT_ID=<account id>
(cd deploy/cloudflare/email-worker && pnpm install --frozen-lockfile && pnpm exec wrangler deploy)
(cd deploy/cloudflare/help-worker && pnpm install --frozen-lockfile && pnpm exec wrangler deploy)
```

Their secrets, `INBOUND_SECRET` and `HELP_PROXY_SECRET`, are set in step 6, once
Terraform has generated them. Until then the help worker answers `502`, and
nothing points at it yet.

### 4. Terraform

Follow `deploy/terraform/README.md`: create the state bucket, copy
`environments/production/terraform.tfvars.example` to `terraform.tfvars`, set
the Cloudflare account id, the `planetscale` organization and database, and
anything else you change, then:

```sh
cd deploy/terraform/environments/production
export PLANETSCALE_SERVICE_TOKEN_ID=<id> PLANETSCALE_SERVICE_TOKEN=<token>
terraform init -backend-config="bucket=opendesk-terraform-state-<account id>" -backend-config="region=us-east-1"
terraform apply
```

This creates the VPC, ALB and certificate, ECR, the ECS cluster and services
(their tasks fail until the first deploy pushes images; that is expected), the
NATS, S3, SES, every secret, the two PlanetScale roles (app and migrate) with
the PrivateLink endpoint that reaches them, and the Cloudflare DNS, Pages
projects, Email Routing and zone settings, and the `opendeskhelp.com` records
(apex, `www` and `*` for the help worker, `custom` and the `origin` fallback
origin, a null MX, SPF and DMARC for a zone that sends no mail) and its zone
settings.

Then close the database's public path. The endpoint adds a private route but
does not remove the public one: in the database's IP restrictions in
PlanetScale, allow only `10.0.0.0/8`, `172.16.0.0/12` and `192.168.0.0/16`.
From your own machine, a connection to the public host must now be refused.

### 5. SES production access

New SES accounts are in the sandbox: they can only send to verified addresses.
Once Terraform's DKIM and MAIL FROM records have verified (SES console >
Identities > opendesk.sh shows "Verified", usually within minutes):

- SES console > Account dashboard > Request production access.
- Use case: transactional (account verification, password resets, invitations,
  ticket notifications to agents). Say that bounces and complaints are
  suppressed by the account-level suppression list and the configuration set
  Terraform created, and that the expected volume is low.
- Approval usually takes a day. Until then only verified recipients get mail.

Ticket replies to customers go out through each workspace's own mailbox (Gmail,
Microsoft 365, or SMTP through its mail server or a sending service it verified
its domain with), never SES: the cloud runs with `MAILBOX_SYSTEM_SEND=false`
(Terraform `mailbox_system_send`), so the API refuses sending through opendesk
and a mailbox still set to it sends nothing until it connects a way to send.
Mail comes in by forwarding, through the email
worker: the cloud runs with `MAILBOX_IMAP=false` (Terraform `mailbox_imap`), so
Google and Microsoft never sync over IMAP and connect send-only. Workspaces with
Collab are the exception: their mailboxes can read the inbox directly through
the Gmail API or Microsoft Graph with the Collab clients (step 7), or receive
over generic IMAP.

### 6. Secrets

Terraform generated `AUTH_SECRET`, `CREDENTIALS_KEY`, `HELP_PROXY_SECRET`,
`INBOUND_SECRET`, `NATS_TOKEN`, `SECRET_KEY_BASE` and the two database URLs into
Secrets Manager as `opendesk/production/<KEY>`. **Never regenerate
`CREDENTIALS_KEY`**: it encrypts every stored mailbox credential.

Give the email worker the inbound secret, and the help worker the help proxy
secret:

```sh
aws secretsmanager get-secret-value --secret-id opendesk/production/INBOUND_SECRET --query SecretString --output text |
  (cd deploy/cloudflare/email-worker && pnpm exec wrangler secret put INBOUND_SECRET)
aws secretsmanager get-secret-value --secret-id opendesk/production/HELP_PROXY_SECRET --query SecretString --output text |
  (cd deploy/cloudflare/help-worker && pnpm exec wrangler secret put HELP_PROXY_SECRET)
```

Help center custom domains: the API adds each verified domain to
`opendeskhelp.com` as a custom hostname. Create its token (My Profile > API
Tokens > Create custom token) with **Zone: SSL and Certificates Edit** on the
`opendeskhelp.com` zone only, and nothing else. Store it, then add
`CLOUDFLARE_SAAS_TOKEN` to `optional_secrets` and apply again, which also sets
`CLOUDFLARE_SAAS_ZONE_ID`:

```sh
scripts/put-secret.sh CLOUDFLARE_SAAS_TOKEN
```

Without it, help centers are served at `<slug>.opendeskhelp.com` and a custom
domain stays `certificate_pending` once its DNS records are found.

Once `curl -sI https://<slug>.opendeskhelp.com` answers from the API, set the
repository variable `HELP_DOMAIN` to `opendeskhelp.com` and run Deploy. It turns on
the help worker's deploy job, gives the app `helpDomain`, and adds the
`/help/<slug>` redirects to app.opendesk.sh. Until it is set, the app's old
`/help/<slug>` links go to the API's own `/help/<slug>`.

### 7. OAuth apps

Sign-in and mailbox connections each have a callback on the API:

| purpose | redirect URI |
| --- | --- |
| sign in | `https://api.opendesk.sh/v1/auth/oauth/<provider>/callback` |
| connect a mailbox | `https://api.opendesk.sh/v1/mailboxes/oauth/<provider>/callback` |

**Google** (console.cloud.google.com):
1. Create a project, then APIs & Services > OAuth consent screen: External,
   app name opendesk, support email, the authorized domain `opendesk.sh`, links
   to the privacy policy and terms on opendesk.sh.
2. Enable the Gmail API (APIs & Services > Library): replies are sent through it.
3. Credentials > Create OAuth client ID > Web application, with both redirect
   URIs above (provider `google`).
4. Scopes (Data Access): `openid email profile` for sign-in; mailbox connections
   request only `https://www.googleapis.com/auth/gmail.send`, to send replies.
   It is a **sensitive** scope: publishing the app needs Google's verification
   once (a demo video of the consent and sending, and the privacy policy), and no
   CASA assessment. Don't add `https://mail.google.com/`: that restricted scope
   is only for a self-hosted instance that syncs mailboxes over IMAP. Until the
   app is verified it works for up to 100 test users you list on the consent
   screen.

**Microsoft** (entra.microsoft.com > App registrations > New registration):
1. Supported account types: accounts in any organizational directory and
   personal Microsoft accounts (`MICROSOFT_TENANT=common`).
2. Redirect URIs (Web): both URIs above with provider `microsoft`.
3. Certificates & secrets > New client secret. Note its expiry and set a
   reminder: an expired secret breaks sign-in and every connected mailbox.
4. API permissions (delegated): Microsoft Graph `openid`, `email`, `profile`,
   `offline_access`, `User.Read`, `Mail.Send`. Mailbox connections request
   `openid email offline_access Mail.Send User.Read`; no Exchange Online
   `IMAP.AccessAsUser.All`, which only a self-hosted instance that syncs
   mailboxes needs.
5. Branding & properties: publisher domain `opendesk.sh`, verified by the file
   at `https://opendesk.sh/.well-known/microsoft-identity-association.json`
   (already published by the site). Publisher verification (linking a Microsoft
   AI Cloud Partner Program account) is recommended, so the consent screen shows
   opendesk as a verified publisher and organizations that allow only verified
   apps can consent.

**GitHub** (optional, sign-in only): Settings > Developer settings > OAuth Apps,
callback `https://api.opendesk.sh/v1/auth/oauth/github/callback`.

Then store the secrets and switch them on:

```sh
scripts/put-secret.sh GOOGLE_CLIENT_SECRET
scripts/put-secret.sh MICROSOFT_CLIENT_SECRET
```

The client ids are not secret: set `google_client_id`, `microsoft_client_id`
(and `github_client_id`) in `terraform.tfvars`, add the secret names to
`optional_secrets`, and `terraform apply`. The same goes for `TURNSTILE_SECRET`
(Cloudflare Turnstile, with `turnstile_site_key`) and `POSTHOG_KEY`.

**Integrations** (optional): Slack and Box only accept apps registered in
advance, and GitHub signs in with a GitHub App (asking for a token without
one). Create a Slack app (api.slack.com/apps), a Box Platform app and a GitHub
App (Issues read and write; Pull requests, Contents and Metadata read; webhook
off; expiring user tokens; any account) with the redirect URL
`https://api.opendesk.sh/v1/integrations/oauth/callback`, set
`mcp_slack_client_id`, `mcp_box_client_id` and `mcp_github_client_id`, and store
`MCP_SLACK_CLIENT_SECRET`, `MCP_BOX_CLIENT_SECRET` and `MCP_GITHUB_CLIENT_SECRET`
the same way. Every other integration registers itself on first sign-in.

**Mailbox sign-in while a provider's review is pending.** Each provider's
mailbox sign-in, which sends replies, has its own switch, separate from user
sign-in: the API reads `GOOGLE_MAILBOX_OAUTH` and `MICROSOFT_MAILBOX_OAUTH` (Terraform
`google_mailbox_oauth`, default `false`, and `microsoft_mailbox_oauth`, default
`true`), and the web app reads the GitHub variable `MAILBOX_OAUTH` (step 11,
default `microsoft`). With a provider off, the mailbox wizard does not offer its
sign-in and the API refuses new connections with `403 mailbox_oauth_disabled`;
mailboxes already connected that way keep sending and can reconnect. Gmail and
Google Workspace mailboxes still receive by forwarding, and send through Gmail's
SMTP server with an app password, or through a sending service. When Google verifies the
app's `gmail.send` scope, set `google_mailbox_oauth = true`, `terraform apply`,
set `MAILBOX_OAUTH` to `google,microsoft` and redeploy Pages.

**IMAP stays off in the cloud.** `MAILBOX_IMAP` (Terraform `mailbox_imap`, and
the GitHub variable `MAILBOX_IMAP` for the web app, step 11) is `false` for the
cloud: mailboxes receive by forwarding, the API answers a Google or Microsoft
`inbound_kind` and a syncing connection with `422 imap_off` (and an IMAP one too,
unless the workspace has Collab), and the wizard offers neither. Leave it off; turning it on would need the restricted
Google scope above, with its verification and yearly CASA assessment.

**Collab mail accounts** (`contracts/collab.md`; the hidden collab plan). A member
of a workspace with Collab connects their own account, which Collab reads and
changes like a mail client: Google with `openid`, `email`, `profile` (the account's
name), `https://www.googleapis.com/auth/gmail.modify` (a **restricted** scope) and `https://www.googleapis.com/auth/gmail.settings.basic`
(a sensitive scope: the account's signature, From name and vacation responder),
Microsoft with Graph `Mail.ReadWrite`, `Mail.Send`, `User.Read`,
`MailboxSettings.ReadWrite` (automatic replies and categories) and `offline_access`,
or any IMAP server. An account connected without the settings scope still syncs its
mail; its settings are read only until it is reconnected. Keep the restricted scope
off the mailbox client: create a separate Google project (and, if you like, a
separate Entra app) for Collab, enable the Gmail API, add the scopes and the
redirect URI `https://api.opendesk.sh/v1/collab/accounts/oauth/google/callback`
(Microsoft: `.../oauth/microsoft/callback`). Support mailboxes of those workspaces
connect with these clients too, to read the inbox directly instead of forwarding,
so add the mailbox redirect URIs as well:
`https://api.opendesk.sh/v1/mailboxes/oauth/google/callback` and
`https://api.opendesk.sh/v1/mailboxes/oauth/microsoft/callback`. Then:

```sh
scripts/put-secret.sh COLLAB_GOOGLE_CLIENT_SECRET
scripts/put-secret.sh COLLAB_MICROSOFT_CLIENT_SECRET
```

and set `collab_google_client_id` / `collab_microsoft_client_id` in
`terraform.tfvars` with the secret names in `optional_secrets`. Without them
Collab uses the mailbox clients, which must then carry these scopes and redirect
URIs too. While the Google project is unverified for the restricted scope, only
its listed test users can connect a Gmail account: enough for the workspaces an
instance admin gives Collab to try (`contracts/admin.md` "Collab access").
Publishing it to everyone needs Google's verification and a yearly CASA
assessment.

### 8. Stripe billing

Billing exists only on opendesk Cloud and is on when `STRIPE_SECRET_KEY` is set
(`contracts/billing.md`); a self-hosted instance never has it. In the Stripe
dashboard (test mode first, then the same in live mode):

1. **Products and prices.** A "Pro" product with a recurring per-seat price in
   USD, $18 per month (`STRIPE_PRICE_PRO`), optionally a yearly price on the same
   product (`STRIPE_PRICE_PRO_YEARLY`), and an "AI top-up" product with a
   one-time $10 price: each top-up adds $10 of AI usage
   (`STRIPE_PRICE_AI_TOPUP`, or the older name `STRIPE_PRICE_CREDITS`, which is
   the name of its Secrets Manager secret). A "Collab" product with a recurring
   per-seat price, $38 per month (`STRIPE_PRICE_COLLAB`), optionally yearly
   (`STRIPE_PRICE_COLLAB_YEARLY`): the hidden collab plan, bought only by
   workspaces an instance admin offered it to (`contracts/billing.md` "Collab").
   Note the `price_...` ids. The app reads amounts from Stripe, so changing a price is a
   new price id, never a code change.
2. **Tax**: nothing to set up. Checkout is a business purchase: it requires the
   company's legal name and billing address and collects its tax ID, and prices
   exclude taxes (`contracts/billing.md` "Tax"). Create the prices with tax
   behavior "exclusive"; Stripe Tax stays off.
3. **Customer portal** (Settings > Billing > Customer portal): allow updating
   payment methods, changing the seat quantity, cancelling at period end, and
   viewing invoices.
4. **Webhook endpoint** (Developers > Webhooks > Add endpoint):
   - URL: `https://api.opendesk.sh/v1/billing/stripe/webhook`
   - Events: `checkout.session.completed`, `customer.subscription.created`,
     `customer.subscription.updated`, `customer.subscription.deleted`,
     `invoice.paid`, `invoice.payment_failed`, `payment_intent.succeeded`,
     `payment_intent.payment_failed` (auto-recharge)
   - Copy the signing secret (`whsec_...`). The API checks every delivery's
     signature with it and records event ids, so Stripe's retries are harmless.
   Cloudflare proxies `api.opendesk.sh`. If the endpoint's delivery log in Stripe
   shows 403s, a Cloudflare challenge is answering instead of the API: add a WAF
   custom rule that skips the security level and Browser Integrity Check for
   the path `/v1/billing/stripe/webhook` (the signature check is the real gate).
   Stripe retries failed deliveries for three days, which covers a deploy.
5. **API key**: a restricted key is enough (write on Checkout Sessions, Customers,
   Customer portal sessions, Subscriptions and PaymentIntents (auto-recharge);
   read on Prices, Products, Invoices and PaymentMethods). `sk_live_...` or `rk_live_...`.

Store them and switch billing on. The AI top-up price id goes in the secret
`STRIPE_PRICE_CREDITS`, which the app still reads when `STRIPE_PRICE_AI_TOPUP`
is unset:

```sh
for k in STRIPE_SECRET_KEY STRIPE_WEBHOOK_SECRET STRIPE_PRICE_PRO STRIPE_PRICE_PRO_YEARLY STRIPE_PRICE_CREDITS; do
  scripts/put-secret.sh "$k"
done
```

(and `STRIPE_PRICE_COLLAB`, optionally `STRIPE_PRICE_COLLAB_YEARLY`, the same way when the collab
plan is sold), then add the names to `optional_secrets` in `terraform.tfvars`, `terraform
apply`, and run the Deploy workflow from the current release tag (target `backend`) so the api and worker
pick up the new task definitions. Test end to end in test mode with
`stripe listen --forward-to https://api.opendesk.sh/v1/billing/stripe/webhook`
against a staging copy, or with the dashboard's "Send test webhook".

### 9. AI providers

The Cloud runs on OpenRouter: one key for generation, Jev decisions and
embeddings (`contracts/ai.md`; models in `ai_model`, `ai_fast_model`, empty for
the app's defaults). Create an OpenRouter API key (openrouter.ai, with a credit
limit), then:

```sh
scripts/put-secret.sh OPENROUTER_API_KEY
```

Add `OPENROUTER_API_KEY` to `optional_secrets` and run Deploy from the current
release tag with target `backend`. It switches AI on
(`AI_PROVIDER=openrouter`) only when it is listed, so a task never starts with a
provider and no key. To call Anthropic directly instead, store and list
`ANTHROPIC_API_KEY` without `OPENROUTER_API_KEY`; embeddings then need
`VOYAGE_API_KEY` (`EMBEDDINGS_DRIVER=voyage`), and without it search falls back
to Postgres full-text search. PlanetScale Postgres supports the `vector` extension that semantic
search uses; the first migration creates it.

### 10. Native apps

The desktop app needs nothing from the Cloud: it loads app.opendesk.sh and
finds the API in `https://app.opendesk.sh/config.json`, which
`write-config.mjs` writes next to `config.js` on every Pages deploy. The iPhone
app (`sh.opendesk.app`) needs three things (`contracts/native.md`):

1. **Push (APNs).** In the Apple Developer account that signs the App Store
   app: Certificates, Identifiers & Profiles > Keys > create a key with Apple
   Push Notifications service enabled, and download its `.p8` (once only). Store
   it and switch push on:

   ```sh
   scripts/put-secret.sh APNS_KEY < AuthKey_<key id>.p8
   ```

   In `terraform.tfvars` set `apns_key_id`, `apns_team_id` (and `apns_topic` if
   the bundle id is not `sh.opendesk.app`), add `APNS_KEY` to
   `optional_secrets`, and apply. The api and worker then get `APNS_KEY`,
   `APNS_KEY_ID`, `APNS_TEAM_ID`, `APNS_TOPIC` and `PUSH_RELAY_SERVE=true`: the
   Cloud sends its own pushes directly and serves the relay
   (`/v1/push/relay/*`) for self-hosted instances that set
   `PUSH_RELAY_URL=https://api.opendesk.sh`. Set `push_relay_serve = false` to
   send only the Cloud's own. Create the key for both Sandbox and Production:
   each device registers with its environment, and debug builds use Sandbox.
2. **App Attest** (the app's stand-in for Turnstile): set
   `app_attest_app_ids = "<team id>.sh.opendesk.app"` in `terraform.tfvars`
   and apply. Without it, the iPhone app cannot pass the human check on login,
   registration and password reset while Turnstile is on.
3. **Associated domains** (passkeys and ticket links): set the `production`
   environment variable `APPLE_APP_IDS` to `<team id>.sh.opendesk.app`. The
   next Pages deploy writes
   `https://app.opendesk.sh/.well-known/apple-app-site-association` with
   `webcredentials` and `applinks` for that id (served as `application/json`
   by `_headers`). Check it with
   `curl -sI https://app.opendesk.sh/.well-known/apple-app-site-association`;
   Apple's CDN fetches it when the app is installed and caches it for a while.

Then run the Deploy workflow (target `all`), so the api and worker pick up the
new task definitions and the app project gets the association file.

### 11. GitHub configuration

In the `production` environment, from `terraform output`:

| kind | name | value |
| --- | --- | --- |
| variable | `AWS_REGION` | `us-east-1` |
| variable | `AWS_DEPLOY_ROLE_ARN` | `github_deploy_role_arn` |
| variable | `ECS_MIGRATE_NETWORK` | `migrate_network_configuration` |
| variable | `CLOUDFLARE_ACCOUNT_ID` | your account id |
| variable | `WEB_POSTHOG_KEY`, `WEB_POSTHOG_HOST`, `TURNSTILE_SITE_KEY` | browser-side keys (optional) |
| variable | `APPLE_APP_IDS` | `<team id>.sh.opendesk.app` for the apple-app-site-association (optional, step 10) |
| variable | `MAILBOX_OAUTH` | mailbox sign-in the wizard offers: `google`, `microsoft`, `google,microsoft` or `none` (default `microsoft`, step 7) |
| variable | `MAILBOX_IMAP` | whether mailboxes may receive over IMAP, matching the API's `MAILBOX_IMAP` (default `false`, step 7) |
| secret | `CLOUDFLARE_API_TOKEN` | the token from step 2 |

Terraform runs in three more environments, `infrastructure-plan`,
`infrastructure` and `infrastructure-approval`, with their own role, state
bucket and provider tokens: `terraform/README.md`, "Applying changes".

Branch protection on `main`: require the **CI status** check and "require
branches to be up to date" (the migration duplicate check depends on it). A tag
ruleset on `v*` (Settings > Rules) restricts creating, moving and deleting
release tags to maintainers, since a release tag deploys production.

### 12. First deploy

Run the Deploy workflow from a release tag (Actions > Deploy > Run workflow,
pick the tag, target `all`), or push a release tag (`git tag v0.1.0 && git push
origin v0.1.0`). It builds arm64 images into ECR tagged with the release, runs
`opendesk migrate` as a one-off task with the migrate role, rolls the api,
worker and gateway services, and deploys the four Pages projects (app, admin
panel, site, docs) and the email worker and the help worker. Check:

```sh
curl -fsS https://api.opendesk.sh/healthz
curl -fsS https://api.opendesk.sh/readyz     # database and NATS connected
curl -sI https://opendeskhelp.com            # 301 to https://opendesk.sh
curl -sI https://<workspace slug>.opendeskhelp.com   # 200 once its help center is published
```

### 13. First release

Tag `v0.1.0` and push the tag. Release builds multi-arch images to
`ghcr.io/useopendesk/{opendesk,realtime}` (make the packages public in the org's
package settings after the first run), creates the GitHub release with the
installer and the self-host files, and redeploys opendesk.sh so
`/install.sh` is that release's installer.

### 14. The first instance admin

The admin panel (`contracts/admin.md`) is at `https://admin.opendesk.sh`, its own
Pages project (`opendesk-admin`), and the API accepts `/v1/admin/*` only from that
origin (`ADMIN_URL`). Two things before anyone signs in:

1. **Turnstile.** Add `admin.opendesk.sh` to the hostnames of the Turnstile widget
   (Cloudflare dashboard > Turnstile > the widget > Hostname management). The panel
   uses the same `TURNSTILE_SITE_KEY` as the app.
2. **The first admin.** Nobody is an admin until one is granted on the instance
   itself. Sign up in the app, turn on two-factor authentication, then run the
   command as a one-off task of the migrate task definition (it reaches Postgres
   over PrivateLink like the deploy's migration):

```sh
cd deploy/terraform/environments/production
aws ecs run-task --cluster opendesk-production --launch-type FARGATE \
  --task-definition "$(terraform output -raw migrate_task_family)" \
  --network-configuration "$(terraform output -raw migrate_network_configuration)" \
  --overrides '{"containerOverrides":[{"name":"migrate","command":["admin","grant","you@example.com","--role","super"]}]}'
# the answer is in the task's log group: terraform output -raw migrate_log_group
```

After that a super admin adds the others from **Admins** in the panel. The same
command with `admin revoke <email>` removes one, and `admin list` lists them.

## How CI and deploys work

- **ci.yml** runs on every PR and push: Go (gofmt, vet, race tests against
  Postgres 17, govulncheck), migrations (no duplicate versions), web
  (typecheck, lint, test, build, pnpm audit), site and docs builds, realtime
  (format, compile with warnings as errors, credo, sobelow, deps.audit, tests),
  both images built and scanned by Trivy (HIGH and CRITICAL with a fix fail
  the build) and then the root compose brought up and checked through Caddy
  (`scripts/selfhost-smoke.sh`), the installer checks, Terraform fmt and
  validate and the plan summary tests (no cloud credentials), and the email and help workers' tests and bundles. Migrations are applied to a fresh
  stock Postgres twice (the second run must be a no-op); production runs
  the same Postgres.
- **deploy.yml** (release tags `v*` without a pre-release suffix, on a commit
  on `main`; a push to `main` deploys only the site and the docs): Terraform
  (plan, then apply; a plan that deletes or
  replaces anything but a task definition waits for approval), images, migrate,
  services, Pages (with the runtime
  config and, when `APPLE_APP_IDS` is set, the apple-app-site-association),
  email worker, help worker (with its routes).
  Migrations run before any service moves, so a migration must work with both
  the old and the new code (add, backfill, then remove in a later deploy).
- **release.yml** (tags `v*`): GHCR images with SBOM and provenance,
  attestations, the GitHub release, and the site refresh.
- **security.yml** (weekly): govulncheck, pnpm audit, mix deps.audit and
  sobelow, Trivy on the published images.

Rollback: re-run the Deploy workflow of an earlier release (Actions > Deploy >
that run > Re-run). Its images are still in ECR (the last 30 are kept), so the
build step is skipped. A migration is not rolled back automatically. Re-running
the whole run also plans that commit's Terraform, which undoes later
infrastructure changes (and waits for approval if that deletes or replaces
anything); to roll back only the code, re-run its "Build and push images" job,
which re-runs the migration and the roll after it.

### Applying changes

Production infrastructure is applied by Deploy, never by hand
(`terraform/README.md`, "Applying changes", has the details and the one-time
setup):

- Terraform code changes by pull request; the next release plans it (the Deploy
  run's summary lists addresses and actions) and applies it before the
  migrations and the service roll. Only code on `main` gets credentials.
  A plan that deletes, replaces or forgets anything other than a task
  definition waits for a reviewer in Actions (environment
  `infrastructure-approval`).
- Settings (`terraform.tfvars`) live in the state bucket, not in git:
  `scripts/tfvars.sh pull`, edit, `scripts/tfvars.sh push`, then run Deploy from
  the current release tag with target `backend`: it applies them and rolls the services onto the task
  definitions Terraform registers. Target `infrastructure` applies them but
  leaves the services on their current revisions.
- Secret values go straight to Secrets Manager with `scripts/put-secret.sh NAME`,
  never through GitHub; a new optional secret also goes in `optional_secrets`.

The first-time setup above applies from a machine with admin AWS credentials;
once the infrastructure environments exist, every later `terraform apply` in
this file means: change the settings or the code as above and let Deploy apply.

## Self-hosting operations

### Install

```sh
curl -fsSL https://opendesk.sh/install.sh | sh
# or, answering up front (cloud-init, Ansible, agents):
curl -fsSL https://opendesk.sh/install.sh | sh -s -- --domain desk.example.com --email you@example.com --yes
```

Before running it, point the domain's DNS (an A and/or AAAA record) at the
machine and open ports 80 and 443. The installer:

1. checks for Docker, and on Linux offers to install it with Docker's own
   script (downloaded to a file, run with sudo only after you say yes);
2. asks for the domain (and an email for certificate notices) unless given;
3. generates every secret and writes `/opt/opendesk/.env` (mode 0600);
4. pins the newest release in `.env`, downloads that release's
   `docker-compose.yml`, `compose.external-proxy.yml`, `Caddyfile`,
   `env.template` and `opendesk-ctl`, and checks each against the release's
   `SHA256SUMS`;
5. pulls the images, starts the stack and waits for the first boot's migrations.

Flags: `--domain`, `--email`, `--proxy` (`caddy` or `external`), `--dir`
(default `/opt/opendesk`), `--version`,
`--no-start`, `--wizard` (also asks about SMTP, attachment storage, inbound
mail, AI and sign-ups), `--ai-key`, `--ai-url`, `--ai-model`, `--ollama`,
`--no-ai`, `--yes`, `--dry-run`, `--help`. Each has an `OPENDESK_*`
variable. Running it again keeps every secret and every setting you changed in
`.env`; it only moves the version pin when you pass `--version`.

To verify it before running it:

```sh
curl -fsSLO https://opendesk.sh/install.sh
curl -fsSLO https://opendesk.sh/install.sh.sha256
sha256sum -c install.sh.sha256
less install.sh && sh install.sh
```

After installing, sign up at your domain: the first account owns the
workspace. Then set `SIGNUP_ENABLED=false` in `.env` if the instance is
private, and `docker compose up -d`.

What runs:

| service | what | exposed |
| --- | --- | --- |
| caddy | automatic HTTPS; `/socket*` to realtime, the rest to opendesk | 80, 443 (and 443/udp for HTTP/3) |
| opendesk | API, workers, web app, embedded NATS on the internal network (token required) | internal only |
| realtime | the gateway, joined to opendesk's NATS | internal only |
| postgres | Postgres 17 with pgvector (semantic search), volume `postgres-data` | internal only |
| s3 (profile `minio`) | S3-compatible attachment storage | internal only |
| smtp-inbound (profile `smtp-inbound`) | publishes the built-in SMTP receiver | 25 |
| ollama, ollama-init (profile `ollama`) | local models for the AI features | internal only |

Everything is configured in `/opt/opendesk/.env`; `env.template` (the same as
`deploy/self-host/.env.template`) documents each setting. After editing it:
`cd /opt/opendesk && docker compose up -d`.

### Day two with opendesk-ctl

```sh
cd /opt/opendesk
./opendesk-ctl status                  # containers, health, version
./opendesk-ctl logs                    # all services; or: logs opendesk | realtime | postgres | caddy
./opendesk-ctl backup                  # into ./backups/<UTC time>/
./opendesk-ctl backup /mnt/backups     # or anywhere else
./opendesk-ctl restore backups/20261011T090000Z
./opendesk-ctl upgrade                 # back up, then move to the newest release
./opendesk-ctl upgrade v1.4.2          # or a specific one
./opendesk-ctl auto-update status      # nightly updates: on or off, schedule, last runs
./opendesk-ctl auto-update off         # or on
```

**Automatic updates** are on after the installer (`--no-auto-update` skips them):
a systemd timer, or a cron entry, runs `opendesk-ctl auto-update run` each night
around 04:00. It upgrades to the newest release of the same major version the
way `upgrade` does, keeps its backups in `backups/auto/` (the newest three), and
never retries a release that failed here. The admin panel's Health page shows
the newest release and each run, and super admins get an email for a new major
version or a failed run (`UPDATE_CHECK=false` stops the server looking).

The same, by hand:

**Back up.** Three things, all needed for a restore:

```sh
cd /opt/opendesk
docker compose exec -T postgres pg_dump -U opendesk -d opendesk -Fc > db.dump
docker cp "$(docker compose ps -q opendesk)":/data - | gzip > data.tar.gz   # attachments, NATS store
cp .env env                                                                 # CREDENTIALS_KEY lives here
```

A database dump without the `.env` it was taken with restores an instance
whose mailbox credentials cannot be decrypted. Keep backups off the machine,
and private: the `.env` copy holds every secret. To schedule it, a root crontab
line such as `17 3 * * * /opt/opendesk/opendesk-ctl backup /var/backups/opendesk`
works, plus whatever prunes old directories there.

**Restore.**

```sh
cd /opt/opendesk
cp <backup>/env .env                          # if this is a new machine
docker compose up -d postgres --wait
docker compose stop opendesk realtime caddy
docker compose exec -T postgres pg_restore -U opendesk -d opendesk --clean --if-exists --no-owner --single-transaction < <backup>/db.dump
gunzip -c <backup>/data.tar.gz | docker run --rm -i --volumes-from "$(docker compose ps -aq opendesk)" alpine:3.22 \
  sh -c 'find /data -mindepth 1 -delete && tar -xf - -C / && chown -R 65532:65532 /data'
docker compose up -d
```

**Upgrade.** Migrations run when opendesk boots, so an upgrade is a new pin,
a pull and a restart. `opendesk-ctl upgrade` pulls the new images, stops the app
and takes a backup, then runs the target release's installer (checked against
its published checksum), which also brings that release's `docker-compose.yml`
and `Caddyfile`. When the new version does not answer its health check within
five minutes, it puts the previous release back, with the backup if migrations
ran. By hand:

```sh
cd /opt/opendesk
sed -i 's/^OPENDESK_VERSION=.*/OPENDESK_VERSION=v1.4.2/' .env
docker compose pull && docker compose up -d
```

Releases do not skip migrations, so upgrading across several versions at once
is fine. Downgrading is not supported once a newer version's migrations ran;
restore the backup taken before the upgrade instead.

**Logs.** `docker compose logs -f --tail=200 opendesk`. Container logs rotate
at 10 MB, five files per service. Request logs never contain query strings,
tokens or bodies, and Caddy's access log drops the `token`, `code`, `state`,
`sig` and `exp` query parameters and the `Authorization`, `Cookie` and
`Sec-WebSocket-Protocol` headers.

### Options

**Platform email.** Out of the box, verification and reset emails are printed
to `docker compose logs opendesk`. For real delivery set `MAIL_DRIVER=smtp` and
the `SMTP_*` values in `.env` (any provider: Postmark, SES SMTP, Mailgun, your
own server) and `MAIL_FROM`.

**Attachments on S3.** AWS S3 or Cloudflare R2: `STORAGE_DRIVER=s3`,
`S3_BUCKET`, `S3_REGION` (`auto` for R2), `S3_ENDPOINT` (R2:
`https://<account>.r2.cloudflarestorage.com`), and the two keys. On the same
machine, the `minio` profile runs an S3-compatible store (RustFS; MinIO no
longer publishes images):

```sh
# .env
COMPOSE_PROFILES=minio
STORAGE_DRIVER=s3
S3_ENDPOINT=https://files.desk.example.com
S3_FORCE_PATH_STYLE=true
S3_BUCKET=opendesk
S3_REGION=us-east-1
S3_ACCESS_KEY_ID=opendesk
S3_SECRET_ACCESS_KEY=<openssl rand -hex 24>
```

Downloads are presigned URLs that browsers open directly, so the endpoint must
be reachable from outside: add a site to the Caddyfile and point
`files.desk.example.com` at the machine.

```
files.desk.example.com {
	reverse_proxy s3:9000
}
```

(`S3_ENDPOINT=http://s3:9000` works for a machine only you use on a LAN.)

**Receiving mail directly.** Forwarding addresses are `<token>@INBOUND_DOMAIN`.
To receive them on this machine: an MX record for `INBOUND_DOMAIN` pointing at
it, port 25 open (many VPS providers block it until asked),
`INBOUND_SMTP_ADDR=:2525` and `smtp-inbound` in `COMPOSE_PROFILES`. Or keep
port 25 closed and have any relay that can POST raw messages (the Cloudflare
worker in `deploy/cloudflare/email-worker` works for a domain on Cloudflare)
deliver to `https://<domain>/v1/inbound/email?to=<rcpt>`, signed with
`INBOUND_SECRET` in the `OD-Inbound-Signature` header (docs: Mailboxes API,
"Inbound email").

**Reading mailboxes over IMAP.** On by default (`MAILBOX_IMAP=true`): besides
forwarding, mailboxes can connect over IMAP and SMTP, and Google and Microsoft
mailboxes can sync instead of connecting send-only, which needs the extra OAuth
scopes in the docs page "Google and Microsoft OAuth". `MAILBOX_IMAP=false`
receives by forwarding only, as opendesk Cloud does; mailboxes that read over
IMAP are kept, stop receiving, and switch to forwarding from their page. With
Collab on (`COLLAB`, `COLLAB_IMAP`, both on by default) generic IMAP stays
available to every workspace, and Google and Microsoft mailboxes can read the
inbox directly through their APIs with the Collab clients; turn `COLLAB_IMAP`
off too to receive by forwarding only.

**Sending through opendesk.** On by default (`MAILBOX_SYSTEM_SEND=true`): a
mailbox without its own way to send can send replies through the platform email
(`MAIL_DRIVER`), from `MAIL_FROM` with the mailbox's name. `MAILBOX_SYSTEM_SEND=false`
makes every mailbox send through Google, Microsoft or SMTP, as opendesk Cloud
does; mailboxes that sent through opendesk are kept, send nothing, and say so on
their page until they connect a way to send.

**AI.** Optional, and off without a provider. Self-hosted instances have no
plans, limits or AI balance; nothing about Stripe applies. Three ways:

- **Anthropic:** `OPENDESK_AI_KEY=sk-ant-... sh install.sh`, or in `.env`
  `AI_PROVIDER=anthropic` and `ANTHROPIC_API_KEY`. Pass keys as variables rather
  than `--ai-key`, so they stay out of shell history.
- **Any OpenAI-compatible endpoint** (OpenAI, Azure OpenAI, vLLM, LM Studio, an
  Ollama you already run): `--ai-url http://10.0.0.5:11434/v1 --ai-model <name>`,
  or `AI_PROVIDER=openai`, `OPENAI_BASE_URL`, `OPENAI_API_KEY`, `AI_MODEL`.
- **Fully local, in this stack:** `--ollama` (or `ollama` in `COMPOSE_PROFILES`
  with the settings the template lists). `ollama-init` pulls `AI_MODEL`
  (default `llama3.1:8b`, about 5 GB) and `nomic-embed-text` for semantic search
  into the `ollama-data` volume on first start; follow it with
  `docker compose logs -f ollama-init`. An 8B model wants 8 GB of free RAM and
  answers slowly on CPU; for a GPU, install the NVIDIA container toolkit and
  add to the `ollama` service in a `docker-compose.override.yml`:

  ```yaml
  services:
    ollama:
      deploy:
        resources:
          reservations:
            devices: [{ driver: nvidia, count: all, capabilities: [gpu] }]
  ```

Semantic search uses pgvector, which the bundled `pgvector/pgvector:pg17`
image has. With an external Postgres without the extension, search falls back
to full-text search.

**Push notifications to the iPhone app.** Off by default; the app works
without them. `PUSH_RELAY_URL=https://api.opendesk.sh` opts in to the opendesk
relay, which sees the instance's address, the notification type and the ticket
number, never ticket text, names or email addresses. Relay requests are signed
with a key derived from `AUTH_SECRET` and checked against
`https://<domain>/v1/push/instance-key`, so the instance must be reachable on
a public https address. An own APNs key (`APNS_KEY`, `APNS_KEY_ID`,
`APNS_TEAM_ID`, `APNS_TOPIC`) sends directly instead, and only reaches an app
built and signed by that key's team. With Turnstile on, `APP_ATTEST_APP_IDS`
lets the iPhone app pass the human check with App Attest. The docs page
"Push notifications" (self-hosting) has the details.

**Help center addresses.** Without more setup each brand's help center is at
`https://<domain>/help/<slug>`. For `https://<slug>.help.example.com`, add a
wildcard DNS record `*.help.example.com` pointing at the machine and set
`HELP_DOMAIN=help.example.com`; old `/help/<slug>` links redirect. Custom domains
(`help.customer.com`) work out of the box: the customer adds a TXT record and a
CNAME to this host (`HELP_CNAME_TARGET`, the host of `OPENDESK_URL` when empty).
Caddy gets every help center certificate on its first visit, after asking
opendesk (`/v1/public/help-domains/tls`) whether the host is one of its help
centers, so no wildcard certificate is needed.

**Behind your own reverse proxy.** `install.sh --proxy external`, or
`COMPOSE_FILE=docker-compose.yml:compose.external-proxy.yml` in `.env`, runs
without Caddy: opendesk is published on `127.0.0.1:8080` and realtime on
`127.0.0.1:4000` (`OPENDESK_BIND`, `OPENDESK_PORT`, `REALTIME_PORT`), and both
join the `opendesk_proxy` network for a proxy in a container. The proxy
terminates TLS, sends `/socket/*` to realtime with WebSocket upgrades and the
rest to opendesk with the `Host` header as is, and gets the help center
certificates: a wildcard for `*.HELP_DOMAIN`, or its own on-demand TLS asking
the same endpoint. The client address is read `TRUSTED_PROXY_HOPS` (default 1)
entries from the right of `X-Forwarded-For`. Examples for nginx, Traefik,
Caddy and Nginx Proxy Manager: "Your own reverse proxy" (self-hosting).

### Troubleshooting

- **No certificate.** `docker compose logs caddy`. DNS must resolve to this
  machine and ports 80 and 443 must be reachable from the internet; an ACME
  rate limit clears within an hour.
- **opendesk restarts at boot.** `docker compose logs opendesk`: it refuses to
  start without `AUTH_SECRET` (>= 32 bytes), `CREDENTIALS_KEY` or `NATS_TOKEN`,
  rather than starting with weak ones.
- **"password authentication failed" on a reinstall.** The `postgres-data`
  volume keeps the password of the install that created it; put that install's
  `.env` back. The installer stops before writing anything when it finds such a
  volume without a `.env`.
- **Realtime updates do not arrive.** `docker compose logs realtime` should
  say `NATS connected to opendesk:4222`; `CHECK_ORIGINS` must contain the URL
  people open (it defaults to `OPENDESK_URL`).

### Try it from a checkout

```sh
make selfhost-env               # .env with fresh secrets for https://localhost
docker compose up -d --build    # builds both images from this checkout
open https://localhost          # Caddy's local CA; the browser asks once
make selfhost-smoke             # the CI check: throwaway project, checks through Caddy, torn down
```

For demo data, make a test workspace (`contracts/admin.md` "Test workspaces") with the
binary's own CLI, which reads the same `.env` as the server. It prints a sign-in link,
a `.invalid` address and a password that is shown once, and deletes the workspace after
`--delete-after` days (`0` keeps it):

```sh
docker compose exec opendesk opendesk admin test-workspace create --name "Acme demo" --plan pro --sample realistic
docker compose exec opendesk opendesk admin test-workspace list
docker compose exec opendesk opendesk admin test-workspace delete acme-demo
```

## Sizing and cost

Two shapes, the same Terraform. Moving between them is a change to
`terraform.tfvars` and one setting in PlanetScale, never a migration.

| | launch (the defaults) | no single point of failure |
| --- | --- | --- |
| api | 1 task, autoscales to 3 | `api = { min = 2, max = 6 }` |
| gateway | 1 task, autoscales to 2 | `gateway = { min = 2, max = 4 }` |
| worker | 1 task on Fargate Spot | `worker = { spot = false }` |
| NATS | 1 standalone server | `nats_nodes = 3` |
| Postgres | PlanetScale single node | high availability (primary and two replicas), in the cluster's settings |
| when | until customers depend on it during their day | from the first customer who would notice a few minutes down |

With one of each, a crashed task is replaced by ECS within a minute or two and
a deploy still rolls without downtime (the new task starts before the old one
stops). A failed database node is replaced by PlanetScale; recent writes are
covered by its backups, not by a replica.

Scale further on what the services report:

- **api CPU** above about 60% most of the day: raise `api.max`, then the task size.
- **Postgres** CPU or memory near the cluster size's limit, or slow queries in
  PlanetScale Insights: the next cluster size (a setting, online).
- **Jobs waiting** (late emails, late AI replies): `worker.min`.
- **Realtime lag** or many sockets per task: `gateway.min`.

Monthly, us-east-1, before traffic:

| item | launch | no single point of failure |
| --- | --- | --- |
| Fargate ARM64 api, worker, gateway (0.5 vCPU, 1 GB each) | $33 (worker on Spot) | $72 |
| Fargate ARM64 NATS (0.25 vCPU, 0.5 GB) | $7 | $22 |
| Public IPv4 addresses (tasks and the ALB's) | $22 | $36 |
| Application Load Balancer, low LCU | $20 | $20 |
| PrivateLink endpoint, 2 subnets | $15 | $15 |
| PlanetScale Postgres, PS-40 (1/2 vCPU, 4 GB) | $28 single node | $83 HA |
| Secrets Manager, KMS, CloudWatch Logs (30 days), ECR | $10 to $15 | $10 to $15 |
| S3, SES, private traffic ($0.01 per GB each side) | usage | usage |
| Anthropic, Voyage | per token; each call is charged to the workspace's AI balance at cost × `AI_USAGE_MARKUP` (`contracts/billing.md`) | usage |
| Stripe | per charge, plus Billing and Tax fees | usage |
| Cloudflare Pages, Email Routing, DNS, proxy | free plan; the Workers free tier (100,000 requests a day) covers the email and help workers at launch, Workers Paid ($5) beyond | $0 |
| Cloudflare for SaaS (help center custom domains) | the first 100 custom hostnames free, then $0.10 each a month | usage |
| **Total** | **about $135 to $145** | **about $260 to $270** |

Other levers:

- **NATS** on Synadia Cloud (`nats_mode = "external"`) removes the NATS tasks.
  That needs NATS credentials-file support in the Go client, which takes only
  a token today.
- A NAT Gateway, if fixed egress IPs are ever required, adds about $33 per AZ
  plus $0.045 per GB, and removes most of the IPv4 charge.
- Compute Savings Plans take about 20% to 50% off Fargate once the size is stable.

Self-hosting needs one machine with 2 vCPU and 4 GB of RAM (about $10 to $25 a
month at most providers) for a team of dozens; Postgres is the part that grows.
