---
title: Admin panel
description: Run your instance from the admin panel at /admin, with its own sign-in, roles and audit log.
---

The admin panel is for the people who run an opendesk instance, not for the people who work in it. You find people and workspaces, end a stolen session, suspend an account or a workspace that abuses the instance, raise a workspace's limits, retry background jobs and check the instance's health. Every action is in the panel's own audit log.

A self-hosted instance serves it at `https://desk.example.com/admin/`. It talks to the same API as the app but has its own sign-in and sessions: being an owner of a workspace gives no access to it, and signing in to it never signs you in or out of the app.

## The first admin

Nobody is an admin until you make someone one from the command line, on the machine that runs opendesk. The person signs up in the app first.

```sh
cd /opt/opendesk
docker compose exec opendesk /opendesk admin grant you@example.com
docker compose exec opendesk /opendesk admin list
```

After that, a super admin adds the others from **Admins** in the panel. `opendesk admin revoke <email>` removes an admin and ends their panel sessions, even when nobody can sign in.

## Signing in

The panel asks for your email, password and a code from your authenticator app every time. Before you can sign in:

- Turn on two-factor authentication in the app, under **Settings > Security**.
- Have a password. If you only sign in with Google, Microsoft, GitHub or SSO, set one with **Forgot password** on the sign-in page.

A panel session ends an hour after you last used it, and 12 hours after you signed in whatever you do. Turning off a user's two-factor authentication, deleting a user or a workspace and changing the admins ask for a fresh code.

## Roles

| Role | Can |
| --- | --- |
| Viewer | See every page |
| Support | Also sign users out, resend the confirmation email, suspend and restore users and workspaces, change a workspace's limits and grant AI usage, retry and cancel jobs |
| Super | Also turn off a user's two-factor authentication, delete users and workspaces, and add, change and remove admins |

The role is checked on every request, so removing or downgrading an admin takes effect at once. There is always at least one super admin.

## Language

The panel follows your browser's language, like the app: English, French or German. Any other language shows English. What the API itself writes, such as the names and details of the health checks, stays in English.

## Suspending

**Suspending a user** signs them out everywhere and stops them signing in. Their workspaces keep working for everyone else.

**Suspending a workspace** pauses it without deleting anything. Its members see a page that says the workspace is suspended. Its API keys, chat widget and help center stop answering. Inbound mail is kept but not turned into tickets, and queued replies, webhooks, automations and AI stop. Restoring it lets everything run again; mail that arrived meanwhile and replies that were queued are not replayed.

Both show up in the workspace's own audit log, so its admins know the instance's operator acted.

## Limits and AI usage

On an instance without billing every workspace already has unlimited use, so limits only matter on an instance that runs billing. There, you can give a workspace more mailboxes, more members or a larger monthly AI allowance than its plan, or grant it AI usage that never expires.

## Updates

The **Health** page shows the version the instance runs, the newest release, and whether automatic updates are on and how their last run went. When a newer release is out it gives the command to run, and super admins get an email about it. See [Backups and upgrades](/self-hosting/backups-and-upgrades/#update-notices).

## Its own address

On one domain the panel shares the app's origin. To give it a host of its own, point a second hostname at the same opendesk, have your proxy send it to `/admin/`, and set the panel's address:

```sh
ADMIN_URL=https://admin.desk.example.com
```

The admin API answers only that origin. If you use Turnstile, add the hostname to the widget's allowed domains in Cloudflare.

## Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `ADMIN_URL` | `APP_URL` + `/admin` | Where the panel is. Its origin is the only one the admin API accepts. |
| `ADMIN_ENV_LABEL` | empty | A short label in the panel's top bar, such as `Production` or `Staging`. |
