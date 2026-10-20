# Native apps (desktop and iPhone)

Two native clients sit on the same public API as the web app (`contracts/api.md`):

| app | path | what it is |
| --- | --- | --- |
| Desktop | `desktop/` | Electron shell (macOS, Windows, Linux) that loads the instance's own web app in a window and adds what a browser cannot: OS notifications, a dock/taskbar badge, `opendesk://` deep links, auto-update, a translucent sidebar |
| iPhone | `ios/` | Native SwiftUI app (`sh.opendesk.app`, iOS 26+): inbox, ticket view, reply and note, assign, status, priority, live chat, notifications, Ollie's suggestions, account. Settings and admin stay on the web |

Both work against opendesk Cloud and any self-hosted instance. The instance is chosen at
runtime, never at build time.

## Finding an instance

The user types an address (`opendesk.sh` means Cloud). The client normalizes it to an
origin (adds `https://`; `http://` only for localhost and private addresses in debug
builds) and reads the runtime config the web app already uses:

`GET <app origin>/config.json` → `RuntimeConfig`

```jsonc
{ "apiUrl": "https://api.opendesk.sh", "gatewayUrl": "wss://gateway.opendesk.sh/socket",
  "selfHosted": false, "turnstileSiteKey": "…"?, "mailboxOAuth": ["microsoft"]?, "mailboxImap": false?,
  "helpDomain": "opendeskhelp.com"? }
```

`helpDomain` is `HELP_DOMAIN` (`HELP_DOMAIN` on Pages too): help centers are at
`https://<slug>.<helpDomain>`, else at `/help/<slug>` on the app's host. Clients link a brand's
help center by its `help_center.url` (contracts/teams.md "Help center addresses").

`mailboxOAuth` lists the providers (`google`, `microsoft`) whose sign-in the add-mailbox
flow offers. The self-hosted binary always writes it (from `GOOGLE_MAILBOX_OAUTH` and
`MICROSOFT_MAILBOX_OAUTH`); on Pages it is written only when `MAILBOX_OAUTH` is set. When it is
absent, a client follows `GET /v1/mailboxes/connect-options` alone; when present, a client
offers a provider's sign-in only if both agree. User sign-in is not affected.

`mailboxImap` is `MAILBOX_IMAP` (`MAILBOX_IMAP` on Pages too): whether mailboxes may receive over
IMAP. When false, the add-mailbox flow receives by forwarding only and connects Google and
Microsoft to send replies (`receive: "forwarding"`), and a mailbox that still receives over IMAP
shows `receiving: "imap_off"` (contracts/api.md "Email"). The self-hosted binary always writes it;
on Pages it is written only when `MAILBOX_IMAP` is set, and when it is absent a client follows
`mailbox_imap` from `GET /v1/mailboxes/connect-options`.

It is the JSON twin of `/config.js` (same keys, same values): the self-hosted binary serves
both (`internal/webui`), and on Cloudflare Pages `write-config.mjs` writes both. Cloud's app
origin is `https://app.opendesk.sh`. Then `GET {apiUrl}/v1/auth/config` tells the client
how to sign in (below).

## Sign-in

### Auth config

`GET /v1/auth/config` gains a `native` member:

```ts
type AuthConfig = { /* existing members */
  native: {
    app_attest: boolean;                  // App Attest proofs are accepted in place of Turnstile
    push: "direct" | "relay" | "off";     // how this instance reaches iPhones (see Push)
    relay_url: string | null;             // the relay origin when push = "relay"
  };
};
```

### Native sessions (refresh token in the body)

Browsers, the desktop app included, keep the refresh token in a cookie (one per signed-in
account, `od_rt_<user id hex>`, `contracts/tokens.md`) and never see it. The desktop app loads
the web app, so it can hold several accounts at once like any browser. The iPhone app has no cookie jar it trusts, so it receives the refresh token
in the response body and keeps it in the Keychain.

- A request is a **native request** when it carries `OD-Client: ios/<version>` **and no
  `Origin` header**. Browsers always send `Origin` on `POST`, so script in a page can never
  read a refresh token from a body.
- Every endpoint that issues a session (`/v1/auth/register`, `/login`, `/mfa`,
  `/oauth/exchange`, `/passkey/login/finish`, `/v1/auth/refresh`) answers a native request
  with `refresh_token` in the body (`CoreAuthResult.refresh_token`) and **sets no cookie**.
  Everything else about the session is unchanged: stored hashed, rotates on use, reuse
  outside the grace window revokes it, 30-day cap.
- `POST /v1/auth/refresh` and `POST /v1/auth/logout` accept `{ "refresh_token": "…" }` in the
  body when there is no cookie.
- Sessions record their client (`sessions.client`: `web`, `desktop`, `ios`), taken from
  `OD-Client` (`desktop/<version>` is sent by the desktop shell's user agent suffix
  `opendesk-desktop/<version>`). `GET /v1/me/sessions` items gain `client`.

### App Attest (iPhone stand-in for Turnstile)

Login, registration and password reset stay behind a human check. The web uses Turnstile;
the iPhone app proves it is the genuine app on a genuine device with App Attest. A native
request may send `app_attest` instead of `turnstile_token`; when Turnstile is off on the
instance, neither is needed.

| method | path | body → response |
| --- | --- | --- |
| POST | `/v1/auth/app-attest/challenge` | `{}` → `{challenge}`. 32 random bytes, base64url, single use, 5 minutes. Rate limited per IP |
| POST | `/v1/auth/app-attest/keys` | `{key_id, attestation, challenge}` → 204. Verifies the attestation (below) and stores the key. `400 invalid_attestation` |

A protected request carries:

```jsonc
"app_attest": { "key_id": "<base64>", "assertion": "<base64 CBOR>", "challenge": "<from /challenge>" }
```

The client asserts over `clientData = "opendesk-app-attest/v1\n" + action + "\n" + challenge`
(UTF-8), `action` one of `login`, `register`, `password_forgot`, `password_reset`. A failed
proof is the same `400 captcha_failed` as a failed Turnstile token. Password reset has no
Turnstile on the web (the emailed token is its gate), so its proof is verified when sent and
not required.

Verification (`internal/appattest`, Apple's App Attest server rules):
- Attestation: CBOR `fmt = "apple-appattest"`; `x5c` chains to the Apple App Attestation
  Root CA (embedded); nonce extension (OID `1.2.840.113635.100.8.2`) equals
  `SHA256(authData || SHA256(challenge))`; `key_id == SHA256(credential public key)`;
  `rpIdHash == SHA256(app id)` for an app id in `APP_ATTEST_APP_IDS`; counter 0; AAGUID
  `appattest` + 7 zero bytes (production), or `appattestdevelop` only with
  `APP_ATTEST_DEVELOPMENT=true`.
- Assertion: ECDSA P-256 signature over `SHA256(authData || SHA256(clientData))` with the
  stored key; `rpIdHash` matches the key's app id; counter strictly greater than the stored
  one (then stored). The challenge is consumed on first use, success or not.
- Each key has a budget of protected requests per hour, on top of the per-IP and
  per-account limits that already apply.

`app_attest_keys`: `key_id` (pk), `app_id`, `public_key`, `counter`, `environment`,
`created_at`, `last_used_at`. Keys unused for 180 days are deleted by a periodic job.

### OAuth sign-in from a native client

Google and other providers refuse embedded browsers, so native clients run OAuth in the
system browser (desktop) or `ASWebAuthenticationSession` (iPhone) and get the result back
through the `opendesk://` scheme, protected with PKCE:

1. The client makes `verifier` (43..128 chars) and `challenge = BASE64URL(SHA256(verifier))`
   and opens
   `{apiUrl}/v1/auth/oauth/{provider}/start?client=native&code_challenge=<challenge>&redirect_to=<path>?`.
2. The callback, for a flow started with `client=native`, redirects to
   `opendesk://auth/callback?code=<one-time code>` (or `?error=<code>`) instead of the app.
   No other scheme or redirect target is accepted.
3. The client calls `POST /v1/auth/oauth/exchange` with `{code, code_verifier}`. A code issued
   to a native flow requires the verifier (`400 invalid_code` otherwise, and the code is spent).
   The desktop shell hands `{code, verifier}` to the page, which calls exchange itself so the
   cookie lands in its session; the iPhone app calls it as a native request.

`link_required` answers work as on the web: the client asks for the account password and
calls `/v1/auth/oauth/link`.

Mailbox and integration connections (Gmail, Outlook, MCP tools) are settings work: the
desktop app opens them in the default browser at the same app URL, and the result reaches
the desktop app through realtime events. The iPhone app sends people to the web for them.

### Passkeys

The WebAuthn RP ID is the app host. The iPhone app offers passkeys only where the app is
associated with that host: Cloud (`webcredentials:app.opendesk.sh`, served in
`/.well-known/apple-app-site-association` on the app origin). Self-hosted instances sign in
with password, OAuth or SSO in the app.

## Push (iPhone)

### Devices

| method | path | body → response |
| --- | --- | --- |
| POST | `/v1/me/devices` | `{platform: "ios", token?: string /* APNs token, hex */, relay_token?: string, environment: "production" \| "sandbox", app_version, name}` → `Device`. Exactly one of `token` (push = direct) or `relay_token` (push = relay). Upserts by token; binds the device to the calling session |
| GET | `/v1/me/devices` | → `Device[]` for the user |
| PATCH | `/v1/me/devices/{id}` | `{muted_types?: NotificationType[]}` → `Device` |
| DELETE | `/v1/me/devices/{id}` | → 204 |

```ts
type Device = { id: string; platform: "ios"; name: string; app_version: string; environment: "production" | "sandbox";
  muted_types: string[]; current: boolean /* bound to the caller's session */; created_at: string; last_push_at: string | null };
```

`push_devices` stores the token hashed for lookup and sealed with `d.Box` for sending. A
device belongs to one session (a session keeps one device: a new token replaces the old):
logout, password change (other sessions), password reset, session revocation, refresh token
reuse and account deletion end the session and delete its devices in the same transaction,
so a signed-out phone stops receiving pushes at once. Sessions are account-wide, so removing
a member does not end them; it deletes the member's notifications in that workspace, and a
push is sent only while the recipient is a member, so that workspace's pushes stop at once.
APNs `410 Unregistered` / `BadDeviceToken` / `DeviceTokenNotForTopic` deletes the device.

### Sending

`hd.Notify` (the single place notifications are created) enqueues `jobs.PushNotificationArgs`
in its transaction. The `core` worker loads the notification, sends nothing when the user's
`push` channel for its type is off in that workspace or notifications are paused
(`contracts/api.md` "Notification preferences and emails"), skips devices that muted its type
(a device's `muted_types` mutes that phone only, on top of the channel), computes the badge
(unread notifications across the user's workspaces) and sends one alert per device:

```jsonc
{ "aps": { "alert": { "title-loc-key": "push.<type>.title", "loc-key": "push.<type>.body", "loc-args": ["1234"] },
           "badge": 3, "sound": "default", "thread-id": "<ticket_id>", "category": "TICKET" },
  "od": { "v": 1, "server": "<API_URL>", "workspace_id": "…", "notification_id": "…",
          "ticket_id": "…"?, "ticket_number": 1234?, "type": "customer_replied" } }
```

Text comes from the app's localized strings (`loc-key`), in `ios/Opendesk/Resources/Localizable.xcstrings`:
a title and a body for every notification type (`internal/push` tests that each type of the
catalogue has both). The body may show the ticket number (`%@`, the only `loc-args`); the title
gets no arguments. With `push = "direct"` the alert
also carries `subtitle` = the ticket subject. A notification without a ticket (billing, AI)
has no `loc-args`, no `category`, and `thread-id` = its type. `apns-collapse-id` = notification id,
`apns-push-type: alert`, priority 10 (5 for badge updates). `POST /v1/notifications/read`
enqueues a badge update (`aps.badge` only) to the user's devices.

APNs: token auth (ES256 provider JWT from `APNS_KEY`, `APNS_KEY_ID`, `APNS_TEAM_ID`, renewed
every 50 minutes), HTTP/2 to `api.push.apple.com` or `api.sandbox.push.apple.com` by the
device's environment, topic `APNS_TOPIC` (default `sh.opendesk.app`).

### Modes

- **direct**: the instance has an APNs key (Cloud). It sends itself.
- **relay**: no APNs key, `PUSH_RELAY_URL` set (self-hosted opt-in, e.g.
  `https://api.opendesk.sh`). Pushes go through the opendesk relay, which holds the key for
  the App Store app.
- **off**: neither. The app works without push and says so in its notification settings.

### Relay

Served by an instance with an APNs key and `PUSH_RELAY_SERVE=true` (Cloud). The relay sees
which instance pushes and the notification type and number, never ticket text, names or
email addresses.

| method | path | body → response |
| --- | --- | --- |
| POST | `/v1/push/relay/register` | `{token, environment, instance: "<instance API origin>"}` → `{relay_token}`. Called by the app. `relay_token` is `d.Box`-sealed `{token, environment, instance}`; only the relay can open it. Rate limited per IP |
| POST | `/v1/push/relay/send` | `{relay_token, push: {kind: "alert" \| "badge", type?, notification_id?, ticket_id?, ticket_number?, workspace_id?, badge}}` → 204, `410 gone` when APNs drops the token. Signed (below). Rate limited per instance |
| GET | `/v1/push/instance-key` | on every instance: `{public_key}` (Ed25519, base64url) |

`send` requests carry `OD-Push-Instance: <API_URL>`, `OD-Push-Timestamp: <unix seconds>` and
`OD-Push-Signature: <base64url Ed25519 over timestamp + "\n" + body>`. The relay:
1. opens `relay_token` and requires its `instance` to equal `OD-Push-Instance`;
2. fetches `GET <instance>/v1/push/instance-key` (HTTPS, public addresses only, cached one
   hour) and verifies the signature, refusing timestamps more than 5 minutes off;
3. builds the APNs payload itself from the whitelisted fields (no subtitle) and sends it.

So a leaked relay token is useless to anyone but the instance that registered it. The
instance key is derived from `AUTH_SECRET` (HKDF-SHA256, info `opendesk-push-instance/v1`)
and needs no storage.

## Web Push (browsers)

Browsers receive notifications over Web Push (RFC 8030, 8291, 8292) while no opendesk window is
focused, the tab is closed included. Nothing needs configuring: the instance's P-256 VAPID key pair
is derived from `AUTH_SECRET` (HKDF-SHA256, info `opendesk-web-push-vapid/v1`), so changing
`AUTH_SECRET` ends every browser subscription, and browsers subscribe again on their next visit.
`GET /v1/auth/config` serves the public key as `web_push_key`. The VAPID contact is the API URL
when it is https, else the `MAIL_FROM` address.

| Method | Path | Notes |
| --- | --- | --- |
| PUT | `/v1/me/web-push` | `{endpoint, keys: {p256dh, auth}}` (a `PushSubscription.toJSON()`) → 204. User sessions only. An https endpoint and keys of RFC 8291's sizes, else `422`. Upserts by endpoint and binds the subscription to the calling session, replacing the session's earlier one. Rate limited per user |

- Like a phone, a subscription lives exactly as long as its session: ending the session (logout,
  revoke, password change, refresh token reuse) deletes it, and deleting the user or the session
  row cascades. Endpoint and keys are sealed with `CREDENTIALS_KEY`; the endpoint is hashed for
  lookup.
- `hd.Notify` enqueues the push job for users with a phone or a subscribed browser. The worker
  sends browsers the notifications whose type has the `desktop` channel on, unless notifications
  are paused. The payload is encrypted for the browser (aes128gcm), so it carries text:
  `{id, workspace_id, title, body, url}`, where `title` is the heading the notification's email has,
  `body` is `#<number> <subject>` on one line of at most 120 characters (empty without a ticket),
  and `url` is the email's link into the workspace. `TTL` is one day, `Urgency: high`.
- A push service answering `404` or `410` deletes the subscription. Endpoints are reached through
  the SSRF guard (`internal/netsafe`), like webhooks.
- The web app (`web/src/lib/webPush.ts`) registers `/sw.js` and subscribes once the person has
  allowed notifications (Allow notifications in the notification settings, or a permission given
  before), then saves the subscription on every app start, so a new session gets it. A
  subscription made with another key is replaced. The desktop app never subscribes.
- `/sw.js` shows every push, tagged with the notification id, so the notification the open page
  shows and the pushed one replace each other. When an opendesk window is focused (it shows its
  toast), the push is closed as soon as it is shown: browsers expect each push to show one. A click focuses an open window and navigates it to `url`, or opens one.
- `/manifest.webmanifest` lets browsers install the web app (standalone window, the app icons).

## Desktop shell

- One `BrowserWindow` per instance origin, loading `<app origin>/`. Context isolation,
  sandbox, no Node in the page. Navigation outside the instance's app and API origins opens
  in the default browser. Permissions are granted only for notifications and clipboard.
- User agent suffix `opendesk-desktop/<version>`. The page sees `window.opendeskDesktop`
  (preload, `contextBridge`):

```ts
interface OpendeskDesktop {
  version: string; platform: "darwin" | "win32" | "linux";
  notify(n: { id: string; title: string; body?: string; path?: string /* app path to open */ }): void;
  setBadge(count: number): void;
  oauth(startUrl: string): Promise<{ code: string; verifier: string } | { error: string }>; // runs the native OAuth flow
  openExternal(url: string): void;     // https only
  switchServer(): void;                 // back to the server picker
  onNavigate(cb: (path: string) => void): () => void;  // deep links and notification clicks
  update: {
    state(): UpdateState;
    onChange(cb: (s: UpdateState) => void): () => void;  // every change, progress included
    restart(): void;                      // quit and install; only when ready
    check(): void;                        // look for an update now, quietly
    channel(): "stable" | "beta";
    setChannel(c: "stable" | "beta"): void;  // also the "Get beta updates" menu item
  };
}

type UpdateState =
  | { status: "idle" }                                   // up to date, or not checked yet
  | { status: "checking" }
  | { status: "available"; version: string }             // found; the download starts at once
  | { status: "downloading"; version: string; percent: number }  // 0..100, whole numbers
  | { status: "ready"; version: string; notes_url: string }      // installs on restart or quit
  | { status: "error" };                                 // the last check or download failed
```

- The web app, when `window.opendeskDesktop` exists, sets `<html data-desktop="darwin|win32|linux">`
  and: shows OS notifications through `notify` when the window is not focused (the toasts
  stay for the focused window) and the type's `desktop` channel is on and notifications are not paused, keeps the badge equal to the unread notification count,
  runs OAuth sign-in through `oauth`, opens mailbox and integration connect flows with
  `openExternal`, and lays itself out for the window chrome (translucent sidebar, inset
  content panel, drag regions, room for the macOS traffic lights).
- Without the shell, the web app shows the same notifications through the browser's
  `Notification` once the person has allowed it (Allow notifications in the notification
  settings), under the same rules; a click focuses the tab and opens the notification's path.
- Window: macOS `titleBarStyle: "hiddenInset"`, `vibrancy: "sidebar"`, transparent page
  background behind the sidebar; Windows 11 `backgroundMaterial: "mica"`; Linux opaque.
- Deep links: `opendesk://auth/callback?…` (OAuth), `opendesk://open?path=/inbox/mine/<ticket_id>`.
- Updates: `electron-updater` from GitHub Releases, checked at launch and every 6 hours,
  downloaded in the background. A `desktop-v*` tag publishes a release that is never marked
  Latest (that belongs to the server's `v*` releases) and refreshes the rolling
  `desktop-latest` release, which is the feed installed apps read (generic provider):
  - Channels: stable reads `latest.yml`, `latest-mac.yml`, `latest-linux.yml`,
    `latest-linux-arm64.yml`; beta reads the same names with `beta`. Every prerelease
    (`desktop-v1.2.3-beta.1`) publishes only the beta files and their installers. A stable
    version publishes both, so beta users get any stable version newer than their beta
    (unless a newer beta is out, which then keeps the beta files).
  - Downgrades are never allowed. Someone who turns beta off keeps their beta version until
    a stable version passes it. The channel is stored in the app's settings; a prerelease
    build starts on beta.
  - The page gets the state through `update` and, when `window.opendeskDesktop.update`
    exists, shows a small banner while an update downloads and when it is ready ("opendesk
    1.3.0 is ready. Restart to update", with What's new opening `notes_url` through
    `openExternal`). The shell also shows an OS notification when an update is ready, and
    the app menu has Check for updates (or Restart to update) and Get beta updates.
  - Stable downloads, at fixed names on `desktop-latest` (linked from
    `opendesk.sh/download`): `opendesk-mac.dmg` (universal), `opendesk-windows.exe` (x64 and
    arm64), `opendesk-linux-x64.AppImage`, `opendesk-linux-arm64.AppImage`,
    `opendesk-linux-amd64.deb`, `opendesk-linux-arm64.deb`, and `version.json`:
    `{ "version": "1.3.0", "date": "<ISO 8601>", "notes_url": "<the desktop-v1.3.0 release>" }`.

## Configuration

| env | where | meaning |
| --- | --- | --- |
| `APP_ATTEST_APP_IDS` | API | comma-separated `TEAMID.bundle` app ids whose App Attest proofs are accepted; empty turns App Attest off |
| `APP_ATTEST_DEVELOPMENT` | API | also accept development-environment attestations (debug builds) |
| `APNS_KEY`, `APNS_KEY_ID`, `APNS_TEAM_ID`, `APNS_TOPIC` | API | APNs token auth; with a key, push = direct |
| `PUSH_RELAY_URL` | API (self-hosted) | relay origin; without an APNs key, push = relay |
| `PUSH_RELAY_SERVE` | API (Cloud) | serve `/v1/push/relay/*` (needs an APNs key) |
| `APPLE_APP_IDS` | Pages build | app ids written into `apple-app-site-association` |
