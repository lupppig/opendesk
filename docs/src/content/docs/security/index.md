---
title: Security controls
description: The security invariants opendesk is built to, aligned with OWASP ASVS Level 1 and CASA Tier 1.
---

opendesk reads mail through restricted Google scopes: Collab mail accounts and support mailboxes that read their inbox directly use `gmail.modify`, and a self-hosted instance can sync Gmail mailboxes over IMAP. So opendesk is built to the App Defense Alliance **CASA Tier 1** controls (OWASP ASVS 4.0.3 Level 1). The controls below are invariants: every change to opendesk must keep them. Because the hosted service and the self-hosted install are the same code, they hold in both.

Each CASA requirement and how opendesk meets it is listed in [CASA controls](/security/casa/).

See [Email security and data controls](/security/email-data-controls/) for attachment scanning, DLP, retention, and legal-hold responsibilities.

## Authentication

- Passwords are hashed with **Argon2id** (64 MiB memory, 3 iterations, 2 lanes, 16-byte salt).
- Every new or changed password, at registration, reset, change and invitation, must be 12 to 128 characters and is refused if it appears in a list of breached passwords.
- Login, registration and password reset are protected by a bot challenge (Cloudflare Turnstile, or an App Attest proof from the iPhone app) and rate limited per IP and per account.
- Per-account limits on passwords, two-factor codes and passkeys are counted across every server and survive restarts, and count only attempts that passed the bot challenge, so nobody can lock an account out without solving one per try. Two-factor codes are also capped per day.
- Login answers identically, in the same time, for an unknown account and a wrong password.
- Two-factor authentication uses TOTP. A code cannot be replayed. Recovery codes are stored hashed and work once.
- Federated sign-in (Google, Microsoft, GitHub, OIDC) binds identities by issuer and subject. An existing password account is linked to a new identity only after its password is presented.
- Owner-level operations (deleting a workspace, transferring ownership, rotating the widget secret, disabling two-factor) require two-factor authentication and a recent sign-in.
- People are told by email when their password is reset or changed, two-factor authentication is turned on or off, a passkey is added or removed, or a sign-in provider is linked to their account.

## Sessions and tokens

- Every token has a single **purpose** (access, realtime, widget, two-factor, OAuth state), and each consumer accepts exactly one. Tokens are signed with HS256, always expire, and the signing secret must be at least 32 bytes or the service does not start.
- Access tokens live 15 minutes, and every request checks that the session behind its token is still live and its account not suspended. Refresh tokens rotate on every use and are stored hashed.
- A session lasts at most 30 days, used or idle; then the person signs in again. Instance admin sessions end after an hour without use and 12 hours after sign-in.
- Logout, password change, member removal and session revocation revoke the sessions **and** close their live connections.
- Session cookies are HttpOnly, Secure and SameSite (Lax for the app, Strict for the admin panel), and carry the `__Host-` prefix, so only the server's own host can set them. Refresh cookies are honoured only on requests from the app's own origin.
- API keys, visitor tokens and one-time email links are random, at least 32 bytes, and stored only as hashes.

## Access control

- Every workspace-owned record is read and written scoped to the workspace of the authenticated principal, never one taken from the request body.
- Any referenced record (contact, team, tag, mailbox, attachment) is checked to belong to the same workspace before it is used.
- Roles are enforced on every route: viewers read only, settings need an admin, owner operations need the owner. API keys are limited to their scopes.

## Input and output

- Uploaded files are checked against their declared type, executables and scripts are refused, and anything that could run in a browser (HTML, SVG, scripts) is stored as plain text. Every file, uploaded or received by email, is downloaded as a file with `nosniff` and a sandbox policy, never rendered or run.
- Email HTML and agent-written HTML are sanitized before they are stored, and stored HTML is only ever rendered sanitized.
- Names from untrusted sources (email headers, identity provider claims) are cleaned before display.
- Webhook and other outbound URLs must be HTTPS and are refused when they point at private, loopback or link-local addresses (SSRF protection). The check runs on the address actually connected to, so DNS rebinding cannot get around it, and redirects are not followed.
- Every HTTP response carries security headers. The realtime gateway checks the browser `Origin`.

## Data protection

- Mailbox passwords, OAuth tokens, webhook secrets and TOTP secrets are encrypted at rest with **AES-256-GCM**.
- Traffic from browsers, the apps, integrations and mail servers uses TLS 1.2 or later, with HSTS. On opendesk Cloud the database connection is TLS with the server's certificate verified, and the hops inside the private network (load balancer to services, NATS) are reachable only from the services that need them.
- Logs never contain secrets, tokens, passwords, credentials or full connection strings. Request logs never include query strings or bodies.
- A Google or Microsoft connection asks only for what its mode needs: a support mailbox sends only (`gmail.send`, `Mail.Send`) unless it reads its inbox directly or the instance syncs it over IMAP; a Collab mail account reads, changes and sends its mail (`gmail.modify`, `Mail.ReadWrite`, `Mail.Send`) and, when the person grants it, its settings. No connection deletes mail for good, except a draft written in opendesk that its author discards; deleting a folder of an IMAP account deletes it on that server, as in any mail app.
- A private Collab mail account and its mail are visible only to the person who connected it, workspace admins included; a shared one only to the people and teams it is shared with. Collab mail is never sent to AI providers. Live updates about it go only to the people who can see what changed.
- Gmail data obtained through Google APIs is used only to provide the helpdesk and Collab features the user enabled, in line with the Google API Services User Data Policy, including its Limited Use requirements.

## Accountability

Security events are written to the audit log: logins, failed logins, two-factor changes, password changes, member and role changes, API key and webhook changes, and mailbox connections and disconnections. Instance admin actions have an audit log of their own. On opendesk Cloud, load balancer connection logs (no URLs, so no one-time values) and a CloudTrail record of every infrastructure API call are kept as well.

## Deletion

Users can delete their account, and owners can delete their workspace. Data is removed, not hidden.

## Supply chain

Continuous integration runs `govulncheck` for Go, `pnpm audit` for JavaScript, `mix deps.audit` and Sobelow for Elixir, CodeQL static analysis of the Go and TypeScript code, a secret scan of the whole history, and a Trivy scan of every container image; a production image with a fixable high or critical finding is not deployed. The scans repeat weekly. Release files are published with SHA-256 checksums and build provenance; the checksum list is signed with the opendesk release key, and the installer and `opendesk-ctl` check that signature before trusting any file, then pin the container images to their published digests. Desktop releases are code signed (and notarized on macOS), their signatures are checked before they are published, and the app installs updates itself only where it can check that signature (macOS and Windows).

## Reporting a vulnerability

See [Responsible disclosure](/security/disclosure/), [`SECURITY.md`](https://github.com/useopendesk/opendesk/blob/main/SECURITY.md) and `/.well-known/security.txt` on every opendesk.sh host.
