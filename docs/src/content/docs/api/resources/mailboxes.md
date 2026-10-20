---
title: Mailboxes
description: Create, test and connect mailboxes, and post raw inbound email.
---

Mailboxes are settings: an `admin` user, or an API key with `settings:read` and `settings:write`.

| Method | Path | Description |
| --- | --- | --- |
| `GET`, `POST` | `/v1/mailboxes` | List or create mailboxes |
| `GET`, `PATCH`, `DELETE` | `/v1/mailboxes/{id}` | |
| `POST` | `/v1/mailboxes/{id}/test` | Test inbound and outbound |
| `GET` | `/v1/mailboxes/connect-options` | How this instance lets mailboxes connect |
| `POST` | `/v1/mailboxes/oauth/{provider}/start` | Start a Google or Microsoft connection |
| `GET` | `/v1/mailboxes/oauth/{provider}/callback` | OAuth redirect target |
| `POST` | `/v1/inbound/email` | Receive a raw email (forwarding) |

## The mailbox object

```ts
type Mailbox = {
  id: string;
  name: string;
  email: string;
  display_name: string;
  inbound_kind: "forwarding" | "imap" | "google" | "microsoft";
  outbound_kind: "smtp" | "google" | "microsoft" | "system";
  receiving: "ok" | "imap_off"; // read-only
  sending: "ok" | "system_off"; // read-only
  forwarding_address: string;
  config: {
    imap_host?: string;
    imap_port?: number;
    imap_username?: string;
    imap_tls?: "tls" | "starttls" | "none";
    smtp_host?: string;
    smtp_port?: number;
    smtp_username?: string;
    smtp_tls?: "tls" | "starttls" | "none";
    oauth_email?: string;
  };
  team_id: string | null;
  signature_html: string;
  auto_reply_enabled: boolean;
  auto_reply_subject: string;
  auto_reply_html: string;
  status: "active" | "error" | "disabled";
  last_error: string;
  last_synced_at: string | null;
  created_at: string;
  trusted_form_senders: string[];
};
```

Passwords and tokens are write-only: they are accepted on create and update, encrypted, and never returned.

- `inbound_kind` is how mail comes in: `forwarding` to `forwarding_address`, or read directly over IMAP (`imap`, or `google` and `microsoft` for a mailbox connected to sync).
- `outbound_kind` is how replies go out: `smtp` through the mailbox's mail server or a sending service, `google` or `microsoft` through Gmail or Microsoft for a mailbox connected with that sign-in, or `system` through the instance's own mailer, from its sending address with the mailbox's name. `system` is available only where the instance allows it (below).
- `receiving` is `imap_off` for a mailbox that reads over IMAP while the instance has receiving over IMAP turned off (`MAILBOX_IMAP=false`, as on opendesk Cloud). It receives nothing until you switch it to forwarding (below). Otherwise `ok`.
- `sending` is `system_off` for a mailbox with `outbound_kind` `system` while the instance has sending through opendesk turned off (`MAILBOX_SYSTEM_SEND=false`, as on opendesk Cloud). It sends nothing until you switch it to SMTP or connect Google or Microsoft (below). Otherwise `ok`.
- `signature_html`, `auto_reply_subject` and `auto_reply_html` take [template variables](/guides/email-templates/variables/), filled when each email is sent. The signature and auto-reply are up to 64 KB each. An unknown variable or a tag that does not parse is `422 validation_failed`.

## Receiving over IMAP

Every mailbox can receive by forwarding. Reading a mailbox over IMAP is an instance option for self-hosted instances, on by default; opendesk Cloud has it off. While it is off:

- Creating a mailbox with `inbound_kind` `imap`, `google` or `microsoft`, or changing a mailbox to one, is `422 imap_off`.
- A Google or Microsoft connection with `receive: "sync"` is `422 imap_off`.

Switch a mailbox that reads over IMAP to forwarding with `PATCH`, then forward its mail to `forwarding_address`:

```sh
curl -X PATCH https://api.opendesk.sh/v1/mailboxes/0192f0c4-... \
  -H "Authorization: Bearer od_live_..." \
  -H "Content-Type: application/json" \
  -d '{ "inbound_kind": "forwarding" }'
```

## Sending through opendesk

Every mailbox can send through Google, Microsoft or SMTP. Sending through the instance's own mailer (`outbound_kind` `system`) is an instance option for self-hosted instances, on by default; opendesk Cloud has it off. While it is off:

- Creating a mailbox with `outbound_kind` `system`, or changing a mailbox to it, is `422 system_send_off`.
- A mailbox already on `system` keeps it, with `sending: "system_off"`, and its other settings stay editable. An email reply from it is `422 mailbox_cannot_send` and stores nothing (see [Messages](/api/resources/messages/#send-a-reply)). Automations skip their email replies, Ollie's email answers wait as suggested replies, and auto-replies and satisfaction surveys are not sent. A reply queued before the setting changed fails with that reason.

Switch such a mailbox to SMTP with `PATCH`, or connect Google or Microsoft with `mailbox_id` (below):

```sh
curl -X PATCH https://api.opendesk.sh/v1/mailboxes/0192f0c4-... \
  -H "Authorization: Bearer od_live_..." \
  -H "Content-Type: application/json" \
  -d '{
    "outbound_kind": "smtp",
    "config": { "smtp_host": "smtp.resend.com", "smtp_port": 465, "smtp_username": "resend", "smtp_tls": "tls" },
    "smtp_password": "re_..."
  }'
```

## Contact forms

Website contact forms usually send `From` an address of the site with the visitor in `Reply-To`. List those addresses in `trusted_form_senders` (an exact address, or `@domain` for every address at that domain, at most 50) and a form message that passed DMARC is from the visitor: the visitor becomes the contact and replies go to them. Mail from a listed address that did not pass DMARC is handled like any other mail.

```sh
curl -X PATCH https://api.opendesk.sh/v1/mailboxes/0192f0c4-... \
  -H "Authorization: Bearer od_live_..." \
  -H "Content-Type: application/json" \
  -d '{ "trusted_form_senders": ["website@example.com"] }'
```

The list replaces the stored one. A domain of a public email provider (such as `@gmail.com`) is refused; list the form's exact address instead.

## Create a mailbox

A forwarding mailbox that sends through your SMTP server:

```sh
curl https://api.opendesk.sh/v1/mailboxes \
  -H "Authorization: Bearer od_live_..." \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Support",
    "email": "support@example.com",
    "display_name": "Example Support",
    "inbound_kind": "forwarding",
    "outbound_kind": "smtp",
    "config": { "smtp_host": "smtp.example.com", "smtp_port": 587, "smtp_username": "support@example.com", "smtp_tls": "starttls" },
    "smtp_password": "smtp-app-password"
  }'
```

Read `forwarding_address` from the response and forward the support address's mail to it. The SMTP server can be a sending service such as Resend or Postmark that has verified the address's domain. Where the instance allows it, `"outbound_kind": "system"`, without SMTP settings, sends through opendesk instead; elsewhere, as on opendesk Cloud, it is `422 system_send_off`.

On an instance that receives over IMAP, an IMAP and SMTP mailbox:

```sh
curl https://api.opendesk.sh/v1/mailboxes \
  -H "Authorization: Bearer od_live_..." \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Support",
    "email": "support@example.com",
    "display_name": "Example Support",
    "inbound_kind": "imap",
    "outbound_kind": "smtp",
    "config": {
      "imap_host": "imap.example.com", "imap_port": 993, "imap_username": "support@example.com", "imap_tls": "tls",
      "smtp_host": "smtp.example.com", "smtp_port": 587, "smtp_username": "support@example.com", "smtp_tls": "starttls"
    },
    "password": "imap-app-password",
    "smtp_password": "smtp-app-password"
  }'
```

## Test

```sh
curl -X POST https://api.opendesk.sh/v1/mailboxes/0192f0c4-.../test -H "Authorization: Bearer od_live_..."
```

```json
{ "inbound": { "ok": true, "error": "" }, "outbound": { "ok": false, "error": "535 authentication failed" } }
```

## Connect Google or Microsoft

`POST /v1/mailboxes/oauth/{provider}/start` with `{ name, email?, mailbox_id?, receive? }` returns `{ url }`. Send the user's browser to that URL. After consent, the provider redirects to the callback, which redirects to `APP_URL/settings/mailboxes?connected=<id>`. Pass `mailbox_id` to reconnect an existing mailbox. `provider` is `google` or `microsoft`.

`receive` is how the mailbox gets its mail:

| `receive` | Creates | Asks the provider for |
| --- | --- | --- |
| `"forwarding"` (default) | `inbound_kind: "forwarding"`, `outbound_kind` `"google"` or `"microsoft"` | Sending only: Gmail `gmail.send`, or Microsoft Graph `Mail.Send` and `User.Read` |
| `"sync"` | `inbound_kind` and `outbound_kind` `"google"` or `"microsoft"` | Reading over IMAP as well. `422 imap_off` while receiving over IMAP is off |

```json
{ "name": "Support", "email": "support@example.com", "receive": "forwarding" }
```

A send-only connection cannot read the mailbox: forward its mail to `forwarding_address`.

## Connect options

`GET /v1/mailboxes/connect-options` answers how this instance lets mailboxes connect, as the add-mailbox wizard shows it:

```json
{
  "google": { "configured": true, "oauth": true },
  "microsoft": { "configured": true, "oauth": true },
  "forwarding": { "enabled": true, "domain": "in.opendesk.sh" },
  "mailbox_imap": false,
  "system_send": false,
  "system_sender": "noreply@opendesk.sh"
}
```

`configured` is whether the provider's OAuth client is set up, and `oauth` whether new mailboxes may connect with it. `mailbox_imap` is whether mailboxes may receive over IMAP. `system_send` is whether mailboxes may send through opendesk, from `system_sender` with the mailbox's name; while it is `false`, every mailbox sends through Google, Microsoft or SMTP.

## Inbound email

`POST /v1/inbound/email?to=<recipient>` accepts a raw RFC 822 message as the body and returns `202`. Each request is signed with the server's `INBOUND_SECRET`: `OD-Inbound-Signature: t=<unix seconds>,v1=<hex>`, where the hex is the HMAC-SHA256 of `<t>.<recipient>.<envelope sender>.` followed by the raw message. A signature older than five minutes is refused. This is what the Cloudflare Email Worker sends; self-hosters can call it from their own mail pipeline. See [Email setup](/self-hosting/email/).

```sh
TO="acme-x7k2@in.example.com"; FROM="sender@example.com"; T=$(date +%s)
SIG=$( { printf '%s.%s.%s.' "$T" "$TO" "$FROM"; cat message.eml; } | openssl dgst -sha256 -hmac "$INBOUND_SECRET" -hex | sed 's/^.* //')
curl "https://desk.example.com/v1/inbound/email?to=$TO" \
  -H "OD-Inbound-Signature: t=$T,v1=$SIG" \
  -H "OD-Envelope-From: $FROM" \
  -H "Content-Type: message/rfc822" \
  --data-binary @message.eml
```

## Events

`mailbox.created`, `mailbox.updated`, `mailbox.deleted`.
