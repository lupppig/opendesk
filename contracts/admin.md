# Instance admin

The instance admin panel is the operator surface of an opendesk instance: the people who run
opendesk Cloud, or the team that runs a self-hosted instance. It is a separate app (`admin/`)
on the same API (`/v1/admin/*`), with its own sign-in, sessions and token purpose. A workspace
role never grants anything here, and an admin session never acts inside a workspace.

| where | panel | API |
| --- | --- | --- |
| opendesk Cloud | `https://admin.opendesk.sh` (Cloudflare Pages, `opendesk-admin`) | `https://api.opendesk.sh/v1/admin/*` |
| self-hosted (`SERVE_WEB=true`) | `<APP_URL>/admin/`, served by the binary | same host |

`ADMIN_URL` is where the panel lives (default `<APP_URL>/admin`; on a development API that does
not serve the web app, the panel's dev server `http://localhost:5174`). Its origin is the only origin
the admin routes accept (CORS and the `Origin` check below). On a single-domain install the panel
shares the app's origin; giving it its own host (`ADMIN_URL=https://admin.desk.example.com`, served
by the proxy in front) keeps a page of the app from ever running on the panel's origin.

## Admins and roles

An instance admin is a user with a row in `instance_admins` (`user_id`, `role`, `granted_by`,
`created_at`). Nobody is an admin until the first one is granted from the command line, on the
machine or task that runs the instance:

```sh
opendesk admin grant you@example.com --role super   # roles: viewer, support, super
opendesk admin list
opendesk admin revoke you@example.com
```

The CLI reads the same configuration as `serve` (`DATABASE_URL`); on opendesk Cloud it runs as a
one-off ECS task. Every CLI change is in the admin audit log with actor `cli`. After that a
`super` admin grants the rest from the panel.

| permission | `viewer` | `support` | `super` |
| --- | --- | --- | --- |
| `read`: every page | yes | yes | yes |
| `users.sessions`: revoke a user's sessions, resend the verification email | | yes | yes |
| `users.suspend`, `workspaces.suspend`: suspend and restore | | yes | yes |
| `workspaces.limits`: limit overrides, AI usage grants, Collab access | | yes | yes |
| `workspaces.test`: create, fill, extend and delete test workspaces (not other workspaces) | | yes | yes |
| `jobs.manage`: retry and cancel background jobs | | yes | yes |
| `users.mfa`: turn off a user's two-factor authentication | | | yes |
| `users.delete`, `workspaces.delete` | | | yes |
| `admins.manage`: grant, change and revoke admins | | | yes |

The role is read from the database on every request, so a revoked or downgraded admin loses
access at once. A `super` admin cannot revoke or downgrade themselves, and the last `super`
admin cannot be removed from the panel (the CLI can).

## Sign-in and sessions

Admin sign-in is **email, password and a TOTP code**, every time: an account without a password
(federated sign-in only) sets one with "Forgot password" first, and an account without two-factor
authentication turns it on in the app first. Passkeys and federated sign-in do not open the panel.

```
POST /v1/admin/auth/login   {email, password, turnstile_token?}
  → 200 {mfa_token}                          purpose admin_mfa, 5 min
  → 401 invalid_credentials                  unknown email or wrong password, same answer and time
  → 403 not_admin                            the password was right, the account is not an admin
  → 403 admin_mfa_setup_required             an admin without two-factor authentication
  → 403 account_suspended
POST /v1/admin/auth/mfa     {mfa_token, code}         code: 6 digits or a recovery code
  → 200 AdminAuthResult, Set-Cookie od_admin=<refresh>
POST /v1/admin/auth/refresh                            the cookie
  → 200 AdminAuthResult, the cookie rotated; 401 unauthenticated clears it;
    409 refresh_in_progress when another tab rotated it in the last 30 s (try again)
POST /v1/admin/auth/logout  → 204, the session revoked and the cookie cleared
GET  /v1/admin/me           → AdminMe
```

- Login is behind Turnstile when the instance has it, rate limited per IP (30 / 15 min) and per
  account (10 / 15 min), and answers an unknown email exactly like a wrong password
  (`auth.DummyVerify`). The TOTP step is limited per user (10 / 15 min) and its replay refused.
- Every `/v1/admin/auth/*` POST must carry `Origin` equal to the origin of `ADMIN_URL`, or it is
  `403 origin_refused`. CORS on `/v1/admin/*` allows only that origin, with credentials; the app's
  `CORS_ORIGINS` are not allowed there.
- Sessions live in `admin_sessions`, apart from the app's `sessions`: signing in to the panel never
  touches the app's cookies or sessions. The refresh token is 32 random bytes, stored hashed,
  rotated on every refresh (a rotated token presented again outside a 30 s grace window revokes
  the session). A session ends **1 hour after its last refresh and 12 hours after sign-in**, whichever
  is first.
- Refresh cookie `od_admin`: HttpOnly, Secure (https), **SameSite=Strict**, `Path=/v1/admin/auth`,
  host-only (no `Domain`). A response body never carries the refresh token.
- Access token: purpose `admin` (contracts/tokens.md), `sub` the user, `sid` the admin session,
  10 minutes, kept in memory by the panel. Every admin route checks that the session is live, the
  user is not suspended and is still an admin, then the route's permission.
- Signing in, an admin's failed sign-in and signing out are in the admin audit log; a failed
  sign-in to an account that is not an admin is a log line without the address.

### Step-up

Turning off a user's two-factor authentication, deleting a user or a workspace, granting,
changing or revoking an admin, and creating or changing a test workspace (which hands out
credentials) carry `code` in the body: a current TOTP code (or a recovery code)
of the acting admin, checked like sign-in. A wrong code is `401 invalid_code`, a missing one
`403 mfa_required`.

## Shapes

```ts
type AdminRole = "viewer" | "support" | "super";
type AdminPermission = "read" | "users.sessions" | "users.suspend" | "users.mfa" | "users.delete"
  | "workspaces.suspend" | "workspaces.limits" | "workspaces.test" | "workspaces.delete" | "jobs.manage" | "admins.manage";

type AdminAuthResult = { access_token: string; expires_in: number; admin: AdminMe };
type AdminMe = { id: string; email: string; name: string; role: AdminRole; permissions: AdminPermission[];
  instance: AdminInstance };
type AdminInstance = { version: string; commit: string; billing_enabled: boolean; ai_enabled: boolean;
  app_url: string; api_url: string; self_hosted: boolean };

type AdminUserRow = { id: string; email: string; name: string; avatar_url: string | null; email_verified: boolean;
  totp_enabled: boolean; has_password: boolean; workspace_count: number; admin_role: AdminRole | null;
  suspended_at: string | null; last_seen_at: string | null; created_at: string };
type AdminUser = AdminUserRow & {
  suspended_reason: string;
  identities: { provider: string; email: string; created_at: string }[];
  passkeys: number;
  memberships: { workspace: AdminWorkspaceRef; role: Role; created_at: string }[];
  sessions: { id: string; client: "web" | "desktop" | "ios"; ip: string; user_agent: string; mfa_verified: boolean;
    created_at: string; last_used_at: string }[];   // live sessions, newest first, at most 50
};

type AdminWorkspaceRef = { id: string; name: string; slug: string };
type AdminWorkspaceRow = AdminWorkspaceRef & { avatar_url: string | null; members: number;
  plan: "free" | "pro" | "collab" | "self_hosted";  // as the entitlements resolve it: complimentary Collab is collab
  status: string /* subscription status */; suspended_at: string | null; last_activity_at: string | null; created_at: string;
  test: boolean /* a test workspace */; test_expires_at: string | null /* the cleanup deletes it then; null = kept */;
  sample_data: AdminSampleData };
type AdminWorkspace = AdminWorkspaceRow & {
  suspended_reason: string;
  members_list: { user: { id: string; email: string; name: string }; role: Role; created_at: string }[];
  pending_invitations: number;
  counts: { tickets: number; open_tickets: number; contacts: number; kb_articles: number; api_keys: number; webhooks: number; brands: number };
  mailboxes: { id: string; name: string; email: string; inbound_kind: string; outbound_kind: string; status: "active" | "error" | "disabled";
    last_error: string; last_synced_at: string | null; created_at: string }[];
  entitlements: { max_mailboxes: number; max_members: number; monthly_ai_usd: number; unlimited_ai: boolean }; // 0 = unlimited, after overrides
  limits: AdminLimits;                       // the overrides themselves
  subscription: { plan: "free" | "pro" | "collab"; status: string; interval: "month" | "year"; seats: number; billed_seats: number;
    current_period_end: string | null; cancel_at_period_end: boolean; stripe_customer_id: string | null; stripe_subscription_id: string | null } | null; // null without billing
  collab: AdminCollab;
  ai: { remaining_usd: number; monthly_usd: number; included_used_usd: number; purchased_usd: number; unlimited: boolean;
    period_start: string; period_end: string;
    month: { charged_usd: number; provider_cost_usd: number; calls: number;
      by_feature: { feature: string; charged_usd: number; provider_cost_usd: number; calls: number }[] } };
  recent_audit: AuditLog[];                  // the workspace's own audit log, last 20 (contracts/api.md)
};
type AdminLimits = { max_mailboxes: number | null; max_members: number | null; monthly_ai_usd: number | null }; // null = the plan's
type AdminCollab = { access: "none" | "offered" | "complimentary"; until: string | null /* complimentary ends then; null = until changed */;
  note: string; enabled: boolean /* the workspace has Collab now (plan.Entitlements.Collab) */; accounts: number /* mail accounts connected */ };

// Test workspaces (see "Test workspaces").
type SampleSize = "small" | "realistic" | "large";
type SampleKind = "tickets" | "chat" | "kb" | "team" | "collab" | "history";
type AdminSampleCounts = { members: number; teams: number; contacts: number; companies: number; tickets: number; messages: number;
  chats: number; articles: number; collections: number; mail_accounts: number; mail_threads: number; views: number; saved_replies: number };
type AdminSampleData = { status: "none" | "running" | "done" | "failed"; size: SampleSize | ""; kinds: SampleKind[] /* the last batch's */;
  counts: AdminSampleCounts | null /* every finished batch added together */; batches: number; started_at: string | null;
  seeded_at: string | null /* the last batch finished then */; error: string /* the last batch's, when it failed */ };
type AdminTestWorkspace = { workspace: AdminWorkspaceRef;
  owner: { id: string; email: string; name: string; test_account: boolean; password_expires_at: string | null } | null;
  created_by: { id: string; email: string } | null /* the admin; null from the command line */; reason: string;
  plan: "free" | "pro" | "collab" /* as asked at creation */; test_expires_at: string | null; sample_data: AdminSampleData;
  members: number; suspended_at: string | null; created_at: string };
type TestWorkspaceResult = { workspace: AdminTestWorkspace;
  owner: { id: string; email: string; new_account: boolean; password: string | null /* only when generated by this request */;
    password_expires_at: string | null };
  sign_in_url: string /* APP_URL + "/login?email=…" */ };

// Test workspaces and test accounts are left out of every count (and of daily signups and
// workspaces); billing never counts them (they have no Stripe subscription). AI usage is the
// instance's real spend and includes theirs.
type AdminSummary = {
  users: { total: number; new_7d: number; new_30d: number; active_7d: number /* seen in the app */ };
  workspaces: { total: number; new_7d: number; new_30d: number; active_7d: number /* with any event */; suspended: number };
  billing: { enabled: boolean; paid_workspaces: number; billed_seats: number; past_due: number } ;
  ai: { month_charged_usd: number; month_provider_cost_usd: number; month_calls: number };
  daily: { date: string /* YYYY-MM-DD, UTC */; signups: number; workspaces: number; ai_charged_usd: number }[]; // last 30 days, oldest first
};

type AdminHealth = {
  version: string; commit: string; role: string; started_at: string;
  database: { ok: boolean; latency_ms: number; schema_version: number; dirty: boolean;
    pool: { total: number; idle: number; in_use: number; max: number } };
  nats: { ok: boolean; embedded: boolean };
  relay: { unpublished: number; oldest_unpublished_s: number | null };
  jobs: { available: number; running: number; retryable: number; scheduled: number; discarded_24h: number;
    oldest_available_s: number | null; by_queue: { queue: string; available: number; running: number; retryable: number }[] };
  mail: { driver: string; inbound_failed_24h: number; inbound_pending: number; mailboxes_in_error: number };
  webhooks: { failing_24h: number };
  config: { billing: boolean; ai_provider: string; decisions_provider: string; embeddings: string; storage: string;
    signup_enabled: boolean; mailbox_imap: boolean; mailbox_system_send: boolean; push: string; help_domain: string };
  updates: AdminUpdates;
  checks: { name: string; status: "ok" | "warn" | "fail"; detail: string }[];   // includes "Updates"
};

type AdminUpdates = {
  check: boolean;                 // UPDATE_CHECK and a release build (vX.Y.Z)
  current: string;                // the running version
  latest: { version: string; url: string /* its GitHub release page */; published_at: string | null } | null;
  available: boolean;             // latest is newer than current
  major: boolean;                 // ... and a new major version, which automatic updates leave alone
  checked_at: string | null; error: string;                 // the last look at GitHub
  previous_version: string; running_since: string | null;   // the version before this one, and since when this one runs
  auto: { reported: boolean /* opendesk-ctl has reported */; enabled: boolean; last_run_at: string | null;
    last_result: "none" | "current" | "updated" | "skipped" | "failed"; last_detail: string };
};

type AdminJob = { id: number; kind: string; queue: string; state: "available" | "running" | "retryable" | "scheduled" | "completed" | "cancelled" | "discarded" | "pending";
  attempt: number; max_attempts: number; args: Record<string, unknown>; last_error: string | null; errors: number;
  created_at: string; scheduled_at: string; attempted_at: string | null; finalized_at: string | null };
type AdminJobStats = { states: Record<string, number>; kinds: { kind: string; available: number; running: number; retryable: number; discarded_24h: number }[] };

type InstanceAdmin = { user: { id: string; email: string; name: string }; role: AdminRole;
  granted_by: { id: string; email: string } | null; totp_enabled: boolean; created_at: string };
type AdminAuditLog = { id: string; actor: { type: "admin" | "cli" | "system" /* the test workspace cleanup */; id: string | null; email: string };
  action: string; target_type: "user" | "workspace" | "admin" | "job" | "instance"; target_id: string | null;
  data: Record<string, unknown>; ip: string; created_at: string };
```

`provider_cost_usd` is the instance's own cost of AI calls. Customers never see it
(contracts/billing.md); the admin API is the operator, and shows it beside what was charged.

## Endpoints

Every route needs `Authorization: Bearer <admin access token>` and the permission listed. They
are left out of the public OpenAPI spec. Lists take `?limit=&cursor=` and answer
`{data, next_cursor}` like the rest of the API.

| method | path | permission | notes |
| --- | --- | --- | --- |
| GET | `/v1/admin/summary` | read | `AdminSummary` |
| GET | `/v1/admin/health` | read | `AdminHealth`; never includes a secret, DSN or key |
| POST | `/v1/admin/updates/check` | read | looks for a new release now (6 / min per admin) → `AdminUpdates`; `409 update_check_off` when the instance does not look |
| GET | `/v1/admin/users` | read | `?q=` email, name or id; `?filter=suspended\|admins\|unverified`; newest first → `Page<AdminUserRow>` |
| GET | `/v1/admin/users/{id}` | read | `AdminUser` |
| POST | `/v1/admin/users/{id}/sessions/revoke` | users.sessions | `{session_id?}`: one session, or every session → 204 |
| POST | `/v1/admin/users/{id}/verification-email` | users.sessions | resends the verification email; `409 already_verified` → 204 |
| POST | `/v1/admin/users/{id}/suspend` | users.suspend | `{reason: 1..500}` → `AdminUser`; `409` for yourself or an admin |
| POST | `/v1/admin/users/{id}/unsuspend` | users.suspend | → `AdminUser` |
| POST | `/v1/admin/users/{id}/mfa/disable` | users.mfa | `{code}` step-up → `AdminUser`; emails the user; `409 mfa_not_enabled` |
| DELETE | `/v1/admin/users/{id}` | users.delete | `{code, confirm_email}` → 204; `422 confirm_email_mismatch`; `409 sole_owner` while they own a workspace with other members; `409` for yourself or an admin |
| GET | `/v1/admin/workspaces` | read | `?q=` name, slug, id or a member's email; `?filter=suspended\|paid\|free\|collab\|test` (paid: a paid Stripe subscription; free: neither that nor complimentary Collab; collab: on the collab plan or with Collab access offered or complimentary; test: test workspaces); newest first → `Page<AdminWorkspaceRow>` |
| GET | `/v1/admin/workspaces/{id}` | read | `AdminWorkspace` |
| POST | `/v1/admin/workspaces/{id}/suspend` | workspaces.suspend | `{reason: 1..500}` → `AdminWorkspace` |
| POST | `/v1/admin/workspaces/{id}/unsuspend` | workspaces.suspend | → `AdminWorkspace` |
| PUT | `/v1/admin/workspaces/{id}/limits` | workspaces.limits | `AdminLimits` (every member; `null` clears one) → `AdminWorkspace` |
| POST | `/v1/admin/workspaces/{id}/ai-grants` | workspaces.limits | `{amount_usd: 0.01..10000, note: 1..200}` → `AdminWorkspace` |
| PUT | `/v1/admin/workspaces/{id}/collab` | workspaces.limits | `{access: "none"\|"offered"\|"complimentary", until?: string \| null, note?: ..200}` → `AdminWorkspace`; `until` is RFC 3339, in the future, complimentary only (`422` otherwise). See "Collab access" |
| DELETE | `/v1/admin/workspaces/{id}` | workspaces.delete (any), workspaces.test (a test workspace) | `{code, confirm_name}` → 204; `422 confirm_name_mismatch`. A test workspace's test accounts go with it (see "Test workspaces") |
| POST | `/v1/admin/workspaces/{id}/sample-data` | workspaces.test | `{size, kinds}` (kinds: 1..6 of the six) → 202 `AdminTestWorkspace`; another batch on top of the last. `409 not_test` for any other workspace, `409 seeding` while a batch runs |
| GET | `/v1/admin/test-workspaces` | read | oldest first → `Page<AdminTestWorkspace>` |
| POST | `/v1/admin/test-workspaces` | workspaces.test | `{code, name, owner: {email, name?} \| {user_id}, password_days?: 1..90 (default 30), plan: "free"\|"pro"\|"collab", sample?: {size, kinds}, delete_after_days?: 1..365 \| null (default 30; null keeps it), reason: 1..500}` → 201 `TestWorkspaceResult`. `422 test_email_required` (a new account's address must end in `.invalid`), `404 user_not_found`, `409 account_suspended`; 30 per admin per hour |
| PATCH | `/v1/admin/test-workspaces/{id}` | workspaces.test | `{code, delete_after_days?: 1..365 \| null, password_days?: 1..90}` (at least one) → `TestWorkspaceResult`, with a new password when `password_days` was given; `409 not_test_account` to renew an owner's own account; `404` for any other workspace |
| GET | `/v1/admin/jobs` | read | `?state=` (default `retryable`), `?kind=`, `?queue=`; newest first → `Page<AdminJob>` |
| GET | `/v1/admin/jobs/stats` | read | `AdminJobStats` |
| POST | `/v1/admin/jobs/{id}/retry` | jobs.manage | runs a retryable, discarded or cancelled job now → `AdminJob` |
| POST | `/v1/admin/jobs/{id}/cancel` | jobs.manage | → `AdminJob` |
| GET | `/v1/admin/admins` | read | `InstanceAdmin[]` |
| POST | `/v1/admin/admins` | admins.manage | `{email, role, code}` → `InstanceAdmin`; `404 user_not_found`, `409 already_admin`, `409 account_suspended`, `409 admin_mfa_setup_required` (the person has no two-factor authentication yet) |
| PATCH | `/v1/admin/admins/{user_id}` | admins.manage | `{role, code}` → `InstanceAdmin`; not your own; `409 last_super` |
| DELETE | `/v1/admin/admins/{user_id}` | admins.manage | `{code}` → 204; ends their admin sessions; not yourself; `409 last_super` |
| GET | `/v1/admin/audit` | read | `?action=`, `?target_id=`, `?actor_id=`; newest first → `Page<AdminAuditLog>` |

Job args are shown with anything that could be a message, a link or a credential replaced by
`"[redacted]"` (keys naming html, text, body, subject, headers, tokens, secrets, passwords, codes,
keys, credentials, content or previews) and strings over 200 characters by their length: system
email jobs carry whole emails with their sign-in links. `last_error` is cut to 2,000 characters.

## What the actions do

- **Suspend a user**: `users.suspended_at` and the reason are set, every session ends (app and panel)
  with `od.control.disconnect`, and no new session starts: sign-in, refresh, passkeys and federated
  sign-in answer `403 account_suspended`. Their workspaces keep working for everyone else.
  Admins are not suspended (revoke the admin first).
- **Suspend a workspace**: `workspaces.suspended_at` and the reason are set, and its members'
  sockets are closed. Every workspace route answers `403 workspace_suspended` to members, API keys
  and AI clients connected over MCP; the widget and the help center answer `404` and visitor
  sessions stop working. Inbound mail is stored and marked `ignored` (`workspace_suspended`); queued
  replies and ticket emails are canceled, not sent; no webhook, automation, SLA run or AI call runs
  for it. Members still sign in and see the workspace in their list with `suspended: true`
  (contracts/api.md `Membership`), where the app shows why and offers their other workspaces.
  Nothing is deleted, and an account can still be deleted. Restoring lets everything run again;
  mail ignored and replies canceled meanwhile are not replayed.
- **Limit overrides** (billing on only): `max_mailboxes` and `max_members` replace the plan's
  (`0` = unlimited), `monthly_ai_usd` replaces the monthly AI allowance. They apply on every plan and
  survive plan changes; `null` returns to the plan's. Without billing every limit is already
  unlimited and the overrides are kept but have no effect.
- **Collab access** (contracts/billing.md "Collab"): the collab plan ($38 per seat per month) is hidden
  until an operator opens it to a workspace. `offered` lets its owners and admins subscribe to it, or
  switch a Pro subscription to it, from the billing page. `complimentary` gives the workspace the collab
  plan without paying (unlimited mailboxes and members, $20 of AI per billed seat, Collab) until `until`,
  or until changed. `none` hides the plan again and ends complimentary access; a subscription already
  on the collab plan is not canceled. The note is the operator's own and stays in the admin audit log
  and on this page; the workspace never sees it. Each change publishes `billing.updated` to the
  workspace (so do limit overrides and AI grants). Without billing the access is stored and has no
  effect: every workspace has Collab unless the instance sets `COLLAB=false`.
- **AI grant**: adds purchased AI usage (`ai_balance_grants`, source `admin`), spent after the
  monthly allowance like a top-up; it never expires.
- **Turn off two-factor authentication**: TOTP and recovery codes are removed (passkeys stay), every
  session of the user ends, and the user is emailed that an administrator did it.
- **Delete a user / a workspace**: the same removal as the user's own "Delete account" and the
  owner's "Delete workspace" (data removed, not hidden; Stripe subscriptions canceled).
- Each action is written to the admin audit log. Actions on a workspace, and on a user's account,
  are also written to the workspace's own audit log (each of the user's workspaces), with no actor
  user and `data.actor = "instance_admin"`, so the workspace's admins see that the operator acted.

## Test workspaces

A test workspace is a workspace an operator makes for a demo or a test: created through core
like any other (default brand and widget, views, labels, SLA policy), flagged (`workspaces.test` = true),
and deleted by the instance on the day the operator chose. It is never a customer's: a real person
is given one only as the owner of their existing account.

- **The owner** is an existing account (`{user_id}`, or the address of an account that exists),
  which is made owner and keeps its own password, or a new **test account**: its address must end
  in `.invalid` (RFC 2606: it never resolves, so nothing can reach it; the panel suggests
  `owner-…@<workspace>.invalid`), it is marked confirmed, `users.test_account` is set, and it gets
  a generated password (24 characters in four groups, upper and lower case and digits without
  look-alikes, Argon2id like every password, accepted by `auth.CheckPassword`) that stops working at
  `users.password_expires_at`. No verification or welcome email is sent, and the setup checklist
  starts dismissed.
- **Passwords are shown once**: in the create (or renew) response, never stored or logged in the
  clear, never in the audit log. A password past its expiry reads as no password: sign-in answers
  `401 invalid_credentials` exactly like an unknown account, in the same time. Renewing
  (`PATCH … {password_days}`) sets a new one and ends the account's sessions and connected apps
  like a password reset; the owner setting a password of their own clears the expiry.
- **The plan** applies without Stripe. With billing on, `pro` sets the limit overrides to unlimited
  mailboxes and members with a $20 monthly AI allowance and grants $50 of AI usage (source `admin`);
  `collab` gives complimentary Collab until the workspace's deletion day (moved with it); `free`
  changes nothing. Without billing every workspace is already unlimited, and the plan is recorded only.
- **Sample data** (`internal/sample`) is added by a background job (`admin.seed_test_workspace`,
  one attempt, 30 minutes) with a seed taken from the workspace id and the batch number, so a batch
  can be reproduced and batches differ. `sample_data` shows it running, then done with the counts
  (every batch added together) or failed with the error. Everything generated is on `.invalid`
  addresses; generated teammates have no password and become test accounts; nothing is sent,
  queued to send, charged or run by automations or AI.
- **No mail leaves for a test account**: the platform mailer skips any `.invalid` recipient.
- **Deletion** uses the normal workspace removal (`core.Ops`), then deletes the workspace's test
  accounts that are left in no other workspace and are not admins. The **cleanup** runs every
  hour: test workspaces past `test_expires_at` are deleted that way, each with
  `test_workspace.expired` in the admin audit log (actor `system`).
- Test workspaces and test accounts are left out of the overview's counts (`AdminSummary`).
- The command line does the same on the machine that runs the instance (actor `cli`), handy for
  local demo data; it adds the sample data at once unless `--queue`:

```sh
opendesk admin test-workspace create --name "Acme demo" --plan pro --sample realistic --delete-after 30
opendesk admin test-workspace list
opendesk admin test-workspace delete <id or slug>
```

## Updates

A release build (`version` is `vX.Y.Z`) with `UPDATE_CHECK` on (the default) keeps one row,
`instance_updates`, current. The `admin.updates` job runs hourly on the workers:

- it records the running version, and the one before it when it changes;
- every 6 hours it reads `https://api.github.com/repos/useopendesk/opendesk/releases/latest`
  (no credentials, a fixed User-Agent, nothing about the instance). No release yet is not an
  error; a failed look is stored as `error` and tried again. The release link is built from the
  validated tag, never taken from the answer;
- it emails the `super` admins (`mail.system`) once per newer release, unless automatic updates
  are on and the release is the same major version (they will install it), and once per failed
  automatic update.

Installing is the host's job. `opendesk-ctl auto-update run` (a nightly systemd timer or cron entry,
`AUTO_UPDATE` in the install's `.env`) upgrades within the same major version, backs up first, and
goes back to the previous version (and the backup, when migrations ran) if the new one does not
answer its health check as the new version. It reports each run through the binary:

```sh
opendesk updates report --enabled=true --result=current|updated|skipped|failed --detail "<one line>"
opendesk updates report --enabled=false          # auto-update on/off: only whether it is on
```

The detail is kept to one plain line of 500 characters. The panel shows a failed run until the
next run, or until a later version starts. Hosted instances set `UPDATE_CHECK=false`: Deploy ships
each release there.

## Admin audit log

`admin_audit_logs`: `actor_user_id` (null for the CLI), `actor_email` (kept after the admin's account
is gone), `action`, `target_type`, `target_id`, `data`, `ip`, `created_at`. It never holds a
password, code, token or message content, and holds no target's name or email: the panel shows the
target while it exists. It is kept for 2 years.

Actions: `login`, `login.failed`, `logout`, `user.sessions_revoked`, `user.verification_sent`,
`user.suspended`, `user.unsuspended`, `user.mfa_disabled`, `user.deleted`, `workspace.suspended`,
`workspace.unsuspended`, `workspace.limits_changed`, `workspace.ai_granted`, `workspace.collab_changed`
(`{access, until, note}`; the workspace's own log gets `{access, until}`), `workspace.deleted` (a test
workspace: `{test, accounts_deleted}`), `test_workspace.created` (`{plan, owner_id, new_account,
password_days, delete_after_days, test_expires_at, sample, reason}`; the workspace's own log gets
`workspace.test_created`), `test_workspace.changed` (`{delete_after_days, test_expires_at,
password_renewed, password_days, owner_id}`), `test_workspace.sample_added` (`{size, kinds, batch}`),
`test_workspace.expired` (`{accounts_deleted}`),
`job.retried`, `job.cancelled`, `admin.granted`, `admin.role_changed`, `admin.revoked`.
