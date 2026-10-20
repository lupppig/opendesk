# Inbound email worker

Forwarding addresses look like `<token>@in.opendesk.sh`. Cloudflare Email Routing
receives every message for `in.opendesk.sh` through a catch-all rule and hands it to
this worker, which POSTs the raw message to the API:

```
POST https://api.opendesk.sh/v1/inbound/email?to=<recipient>
OD-Inbound-Signature: t=<unix seconds>,v1=<hex HMAC-SHA256 keyed with INBOUND_SECRET>
OD-Envelope-From: <envelope sender>
Content-Type: message/rfc822
```

`OD-Envelope-From` is the SMTP envelope sender (empty for the null sender of a
delivery report); the API counts a delivery report as a bounce only with the null
sender. The worker does not check senders itself: Email Routing writes its SPF, DKIM and
DMARC results into the message under the authserv-id `mx.cloudflare.net`, and the API
believes them because `INBOUND_AUTHSERV_ID` lists that id.

The catch-all rule, the MX records for `in.opendesk.sh` and the worker binding are
managed by Terraform (`deploy/terraform/modules/cloudflare`). This directory is only
the worker code.

## Behaviour

- The message is read once (Email Routing caps messages at 25 MiB) and the same
  bytes are sent on every attempt.
- Up to 4 attempts, 30 s each, with backoff of about 250 ms, 1 s and 3 s (25% jitter)
  on network errors, 408, 429 and 5xx. A `Retry-After` of up to 10 s is honoured; a
  longer one ends the run early.
- 400, 404 (unknown forwarding address) and 413 reject the message permanently, so
  the sender gets a bounce with a short reason.
- Anything else, including running out of attempts or a missing `INBOUND_SECRET`,
  throws. Email Routing treats that as a temporary failure and the sending server
  retries later, so no mail is lost while the API is down.
- Logs carry the recipient domain, size, status and attempt number. Never the
  secret, the address or the body.

## Deploy

```sh
pnpm install --frozen-lockfile
# Same value as the API's INBOUND_SECRET (Secrets Manager: opendesk/production/INBOUND_SECRET)
aws secretsmanager get-secret-value --secret-id opendesk/production/INBOUND_SECRET \
  --query SecretString --output text | pnpm exec wrangler secret put INBOUND_SECRET
pnpm run deploy
```

`deploy.yml` runs `wrangler deploy` on every push to main; the secret is set once and
only changes when `INBOUND_SECRET` is rotated (rotate both together).

## Test

```sh
pnpm test        # unit tests, no network
pnpm typecheck
```

Locally, `wrangler dev` exposes a test endpoint for email handlers. Point it at a local
API first:

```sh
printf 'INBOUND_SECRET=dev-secret\n' > .dev.vars
pnpm exec wrangler dev --var INBOUND_URL:http://localhost:8080/v1/inbound/email
curl -X POST 'http://localhost:8787/cdn-cgi/handler/email?from=a@example.com&to=t0k3n@in.opendesk.sh' \
  --data-binary @message.eml
```
