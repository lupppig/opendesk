# Tokens and credentials

All JWTs are **HS256**, signed with `AUTH_SECRET` (min 32 bytes; the Go API and the
Elixir gateway both refuse to boot below that). Every JWT carries a `purpose` claim
and a consumer accepts exactly one purpose. `exp` is required.

| purpose      | issued by                     | lifetime | accepted by                     |
| ------------ | ----------------------------- | -------- | ------------------------------- |
| `access`     | `/v1/auth/*` login + refresh  | 15 min   | Go API (Bearer)                 |
| `ws`         | `POST /v1/realtime/token`     | 5 min    | Elixir gateway (socket connect) |
| `widget_ws`  | `POST /v1/widget/session`     | 5 min    | Elixir gateway (socket connect) |
| `mfa`        | login when TOTP is enabled    | 5 min    | `POST /v1/auth/mfa`             |
| `oauth_state`| OAuth start endpoints (sign-in, mailboxes, integrations; `data.flow` tells them apart) | 10 min | the matching OAuth callback |
| `passkey_login` | `POST /v1/auth/passkey/login/begin` | 5 min | `POST /v1/auth/passkey/login/finish` |
| `passkey_register` | `POST /v1/me/passkeys/register/begin` | 5 min | `POST /v1/me/passkeys/register/finish` |
| `assistant_approval` | a Ollie run's `approval` event | 15 min | `POST /v1/assistant/actions/{id}/decision` |
| `mcp_access` | `POST /oauth/token` (MCP OAuth, `contracts/agent.md`) | 1 h | the MCP server, `{API_URL}/mcp` (Bearer), when `aud` is the MCP resource |
| `notification_unsubscribe` | notification emails (`contracts/api.md` "Notification preferences and emails") | 90 days | `POST /v1/notifications/unsubscribe` and `/unsubscribe/one-click` |
| `admin_mfa` | `POST /v1/admin/auth/login` (contracts/admin.md) | 5 min | `POST /v1/admin/auth/mfa` |
| `admin`      | `POST /v1/admin/auth/mfa` and `/refresh` | 10 min | `/v1/admin/*` only (Bearer); the admin session (`sid`) is checked on every request |

## Claims

```jsonc
// access
{ "purpose": "access", "sub": "<user_id>", "sid": "<session_id>", "mfa": true, "iat": 0, "exp": 0 }

// access.mfa is true when the session passed a second factor: TOTP or a recovery code
// after the first factor, or a user-verified passkey on its own.

// passkey_login / passkey_register: jti names the pending WebAuthn ceremony stored
// server-side (challenge, expiry); finishing deletes it, so each token works once.
// passkey_register is bound to the user (sub) and the session (sid) that began it.
{ "purpose": "passkey_login", "jti": "<uuid>", "iat": 0, "exp": 0 }
{ "purpose": "passkey_register", "sub": "<user_id>", "sid": "<session_id>", "jti": "<uuid>", "iat": 0, "exp": 0 }
{ "purpose": "assistant_approval", "sub": "<user_id>", "sid": "<session_id>", "wid": "<workspace_id>", "jti": "<action_id>", "iat": 0, "exp": 0 }

// admin: an instance admin panel session (contracts/admin.md); never accepted by app routes,
// and an access token is never accepted by /v1/admin/*.
{ "purpose": "admin", "sub": "<user_id>", "sid": "<admin_session_id>", "jti": "<uuid>", "iat": 0, "exp": 0 }
{ "purpose": "admin_mfa", "sub": "<user_id>", "jti": "<uuid>", "iat": 0, "exp": 0 }

// notification_unsubscribe: turns off notification emails from one workspace for one user. Harmless to
// replay (it only switches email off), so it is not single use.
{ "purpose": "notification_unsubscribe", "sub": "<user_id>", "wid": "<workspace_id>", "jti": "<uuid>", "iat": 0, "exp": 0 }

// mcp_access: an AI client acting for a user in one workspace. aud is the MCP resource
// ({API_URL}/mcp, RFC 8707); gid is the grant, checked on every request (revoked grants and
// ended memberships stop working at once); scope is space-separated and is bounded again by
// the user's current role.
{ "purpose": "mcp_access", "sub": "<user_id>", "wid": "<workspace_id>", "gid": "<grant_id>",
  "scope": "tickets:read kb:read", "aud": ["https://api.opendesk.sh/mcp"], "iss": "<API_URL>", "jti": "<uuid>", "iat": 0, "exp": 0 }

// ws (agent / API-key socket)
{ "purpose": "ws", "sub": "<user_id or api_key_id>", "kind": "user" | "api_key",
  "wid": "<workspace_id>", "role": "owner|admin|agent|viewer",
  "name": "Anna", "avatar": "https://...", "teams": ["<private team id>"], "sid": "<session_id>" /* users */,
  "jti": "<uuid>", "iat": 0, "iat_ms": 0, "exp": 0 }
// teams: the private teams whose topics the socket may join (contracts/teams.md "Visibility"); omitted when none
// sid: the session the token was issued to (users only). iat_ms: the issue time in ms, taken before the API
// reads what the claims say. The gateway refuses a token whose sub or sid was revoked (od.control.disconnect)
// at or after it (contracts/realtime.md "Connect"); without iat_ms it compares iat * 1000

// ws for a person in several workspaces (POST /v1/realtime/token?all_workspaces=true, users only):
// the token's own workspace as above, plus the others they are in that are not suspended, each with
// their role and the private teams they may see there. One socket then follows them all.
{ "purpose": "ws", "sub": "<user_id>", "kind": "user", "wid": "<focus workspace>", "role": "owner", "teams": [],
  "workspaces": { "<other workspace id>": { "role": "viewer", "teams": ["<private team id>"] } }, "...": "..." }
// workspaces: at most 25 others and 50 private teams in all (the gateway refuses more than 50 and 200);
// past that a workspace is left out and the app refreshes it when it is opened instead of live.
// An API key's, an OAuth grant's or the assistant's token never carries it. Gateways that predate
// it read wid, role and teams alone, so the token still works as a one-workspace token.

// widget_ws (end customer in the chat widget)
{ "purpose": "widget_ws", "sub": "<contact_id>", "wid": "<workspace_id>", "bid": "<brand_id>" /* the widget's brand: the socket receives only that brand's chats */, "jti": "<uuid>", "iat": 0, "exp": 0 }
```

The gateway never touches the database: everything it needs to authorize a join is
in the claims.

## Opaque credentials (Go API only, stored hashed with SHA-256)

| credential      | format                          | where                                            |
| --------------- | ------------------------------- | ------------------------------------------------ |
| refresh token   | 32 random bytes, base64url      | one cookie per signed-in account, `__Host-od_rt_<user id as 32 hex chars>`: HttpOnly, Secure, SameSite=Lax, Path=`/`, host-only. With `COOKIE_DOMAIN` it is `__Secure-od_rt_<hex>`, Path=`/v1/auth`, Domain=`COOKIE_DOMAIN`; on plain http (development) the bare `od_rt_<hex>`, Path=`/v1/auth`. A bare cookie set by an earlier version is still read and replaced by the prefixed one the next time it is set (see "Several accounts in one browser"). Only a request without `Origin` or from an app origin (`CORS_ORIGINS`) has its refresh cookies read. Native requests (below): `refresh_token` in the response body instead, sent back as `{refresh_token}` to `/v1/auth/refresh` and `/v1/auth/logout` |
| admin refresh token | 32 random bytes, base64url | cookie `__Host-od_admin` (plain http: `od_admin`, Path=`/v1/admin/auth`): HttpOnly, Secure (https), SameSite=Lax, Path=`/`, host-only; rotates on use, a session ends 1 h after its last refresh and 12 h after sign-in (contracts/admin.md). Never in a body |
| App Attest challenge | 32 random bytes, base64url, single use, 5 min | `POST /v1/auth/app-attest/challenge`; consumed by the first key registration or proof that names it, success or not |
| push device token | APNs token (hex) or relay token | `POST /v1/me/devices`; stored SHA-256 hashed for lookup and sealed with `d.Box` for sending |
| API key         | `od_live_` + 32 bytes base64url | `Authorization: Bearer od_live_...`              |
| visitor token   | `od_vis_` + 32 bytes base64url  | `Authorization: Visitor od_vis_...` (widget API) |
| one-time tokens | 32 bytes base64url              | email links (verify, reset, invite, oauth login) |
| MCP OAuth code  | 32 bytes base64url, single use, 60 s | `code` on the client's redirect; exchanged with the PKCE verifier |
| MCP refresh token | `od_mcpr_` + 32 bytes base64url, 30 days, rotates on use | `POST /oauth/token`; a rotated one presented again revokes the grant |
| MCP client secret | `od_mcps_` + 32 bytes base64url | confidential clients from `POST /oauth/register` |

## Several accounts in one browser

A browser can be signed in to up to **5** accounts at once (different users), like Linear. Each account has its own
refresh cookie, `od_rt_<user id hex>`, with the attributes above; nothing else about a session changes (stored hashed,
rotates on use, reuse outside the 30 s grace window revokes it, 30-day cap whether used or idle).

- **Issuing.** Every session-issuing answer to a browser (register, login, `mfa`, `oauth/exchange`, `oauth/link`,
  passkey login) sets that account's cookie and leaves the other accounts' cookies alone. If the browser already held a
  session for that account, that session is revoked (with `od.control.disconnect`) and its cookie replaced. Signing in
  to a sixth account answers `409 too_many_accounts` and creates nothing (a registration is rolled back); signing in
  again to a held account is always allowed. Cookies that no longer name a live session of their account are cleared.
- **A cookie belongs to its account.** The session behind `od_rt_<id>` must be that user's; a cookie whose session
  belongs to anyone else is refused and nothing rotates.
- **Refresh** (`POST /v1/auth/refresh {user_id}`) rotates only the named account's cookie. Without `user_id` it refreshes
  the account whose session was used most recently. A refused refresh clears only that account's cookie; a replay
  inside the grace window (another tab just rotated it) answers 401 without touching the cookie.
- **Listing** (`POST /v1/auth/accounts`) reads every cookie without rotating anything and answers the accounts with
  live sessions, in the order they were added; ended ones are left out and their cookies cleared.
- **Logout** signs out of the access token's account (or `user_id` when there is no valid access token) and clears its
  cookie; `{all: true}` ends every session the browser holds. Each ended session is audit-logged (`logout`) and
  publishes `od.control.disconnect`. Password change, session revocation and member removal act per account, as before.
- **Legacy cookie.** The single `od_refresh` cookie of earlier versions is still read: a refresh or listing that uses
  it moves its value to `od_rt_<id>` (a refresh also rotates it) and clears `od_refresh`, so nobody is signed out by
  the upgrade. No answer sets `od_refresh` any more.
- **The web app** keeps access tokens in memory, one per account, and persists only ids in `localStorage`: the active
  account (`od-account`) and each account's last workspace (`od-ws:<user id>`). Switching account clears the whole
  query cache and restarts realtime, so no data of one account renders under another. Switching workspace
  within an account keeps the cache: every key of workspace data starts with its workspace, and a query
  fetches from the workspace its key names (contracts/api.md "Several workspaces").

## Native apps (`contracts/native.md`)

- **Native request**: `OD-Client: ios/<version>` and no `Origin` header. Browsers always send `Origin` on `POST`, so
  page script can never read a refresh token from a body. Every session-issuing answer to a native request carries
  `refresh_token` in the body and sets no cookie; the session is otherwise the same (stored hashed, rotates on use,
  reuse outside the 30 s grace window revokes it, 30-day cap).
- **App Attest key**: an ECDSA P-256 key attested by Apple (`internal/appattest`, the Apple App Attestation Root CA
  embedded), stored in `app_attest_keys` with its app id, environment and counter. Each proof is an assertion over
  `"opendesk-app-attest/v1\n" + action + "\n" + challenge`; the counter must grow. Keys unused for 180 days are deleted.
- **Relay token**: `base64url(d.Box.Seal({t: APNs token, e: environment, i: instance API URL}))`, minted by the
  relay's `/v1/push/relay/register`. Only the relay can open it, and it delivers only for requests signed by `i`.
- **Instance key**: Ed25519, seed = HKDF-SHA256(`AUTH_SECRET`, no salt, info `opendesk-push-instance/v1`); not stored,
  changes with `AUTH_SECRET`. Public half at `GET /v1/push/instance-key`. Relay requests carry
  `OD-Push-Instance: <API_URL>`, `OD-Push-Timestamp: <unix seconds>` and
  `OD-Push-Signature: base64url(Ed25519(timestamp + "\n" + body))`; the relay fetches the key over HTTPS from public
  addresses only, caches it an hour, and refuses timestamps more than 5 minutes off.
- **APNs provider token**: ES256 JWT (`iss` = `APNS_TEAM_ID`, `kid` = `APNS_KEY_ID`, signed with `APNS_KEY`), renewed
  every 50 minutes. Never logged; device tokens never appear in log lines or errors.

Passwords: Argon2id only (`m=64MiB, t=3, p=2`, 16-byte salt, 32-byte key), PHC string format.

## Requests

- Agent requests: `Authorization: Bearer <access_jwt>` plus `OD-Workspace: <workspace_id>`
  for workspace-scoped routes. The API checks membership and resolves the role.
- API keys are bound to one workspace; `OD-Workspace` is ignored for them.
- Widget requests: `Authorization: Visitor <token>`; the workspace comes from the session.
