---
title: Security hardening
description: A checklist for running opendesk securely on your own infrastructure.
---

opendesk ships with secure defaults. This checklist covers what is up to you as the operator.

## Before going live

- `OPENDESK_ENV=production`. This turns on the boot checks for secrets.
- `AUTH_SECRET` and `CREDENTIALS_KEY` are random, 32 bytes or more, and backed up somewhere private.
- The site is served over HTTPS only. Caddy handles certificates and redirects.
- `SIGNUP_ENABLED=false` once your team has accounts. People you invite can still join.
- Platform email (`MAIL_DRIVER`) is configured, so password resets work.
- `TURNSTILE_SITE_KEY` and `TURNSTILE_SECRET` are set if the instance is on the public internet.

## Network

- Expose only ports 80 and 443 (and 25 if you use the built-in SMTP receiver).
- Keep Postgres, NATS (`NATS_LISTEN`) and the gateway's port on the private network. When embedded NATS must listen beyond localhost, set `NATS_TOKEN`; production refuses to boot without it.
- Behind a proxy of your own, set `TRUSTED_PROXY_HOPS` to the number of proxies that append to `X-Forwarded-For`, and publish opendesk's ports only to them, so client IPs used for rate limits and audit logs cannot be spoofed. `TRUST_PROXY=true` is only for a proxy that sets the headers itself, like the bundled Caddy. See [Your own reverse proxy](/self-hosting/reverse-proxy/#client-addresses).
- Set `CHECK_ORIGINS` on the gateway to the origin of your opendesk web app. The chat widget uses its own endpoint, which takes only widget tokens.
- Set the widget's allowed origins in settings.

## Secrets

- `.env` is mode `0600`. Keep it that way, and keep backups private.
- Rotate OAuth client secrets before they expire (Microsoft secrets have a fixed lifetime).
- Rotate `INBOUND_SECRET` in opendesk and the Email Worker together.
- Never rotate `CREDENTIALS_KEY`: it would make every stored mailbox credential unreadable.

## Accounts

- Turn on two-factor authentication for every owner and admin. Owner-level operations require it.
- Review members and API keys regularly, and give API keys only the scopes they need, with an expiry.
- Review the audit log (**Settings → Audit log**) for logins, failed logins, two-factor changes, role changes and API key, webhook and mailbox changes.

## Updates

- Upgrade promptly when a release mentions security fixes: `opendesk-ctl upgrade`.
- Keep the host OS and Docker patched.
- Watch [security advisories](https://github.com/useopendesk/opendesk/security/advisories) on GitHub.

## What opendesk does for you

- Passwords hashed with Argon2id, checked against a breached list, 12 to 128 characters.
- Sign-in rate limited per IP and per account, with uniform responses for unknown users.
- Mailbox credentials, OAuth tokens, webhook and TOTP secrets encrypted with AES-GCM.
- Security headers on every response, sanitized HTML, and webhook URLs restricted to public HTTPS targets.
- No secrets, tokens or request bodies in logs.

See [Security controls](/security/) for the full list.
