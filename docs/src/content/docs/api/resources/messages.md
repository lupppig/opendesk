---
title: Messages and attachments
description: Read a ticket's conversation, send replies and notes, and upload attachments.
---

Scopes: `tickets:read` to read, `tickets:write` to send.

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/tickets/{id}/messages` | All messages, ascending |
| `POST` | `/v1/tickets/{id}/messages` | Send a reply or add a note |
| `PATCH` | `/v1/tickets/{id}/messages/{messageId}` | Edit your own internal note |
| `DELETE` | `/v1/tickets/{id}/messages/{messageId}` | Delete a message, leaving a marker |
| `POST` | `/v1/tickets/{id}/messages/{messageId}/restore` | Admins: bring a deleted message back |
| `POST` | `/v1/tickets/{id}/messages/{messageId}/purge` | Admins: remove a deleted message's content for good |
| `POST` | `/v1/tickets/{id}/messages/{messageId}/retract` | Turn a sent chat reply into an internal note |
| `GET` | `/v1/messages/{id}/original` | The full original, including quoted history |
| `POST` | `/v1/attachments` | Upload a file |
| `GET` | `/v1/attachments/{id}` | Download a file |

## The message object

```ts
type Message = {
  id: string;
  ticket_id: string;
  kind: "reply" | "note";
  direction: "inbound" | "outbound";
  author: { type: "contact" | "user" | "system" | "api"; user: UserRef | null; contact: ContactRef | null };
  body_html: string;
  body_text: string;
  has_quoted: boolean;
  subject: string;
  from_email: string;
  from_name: string;
  to: string[];
  cc: string[];
  bcc: string[];
  attachments: Attachment[];
  delivery_status: "none" | "pending" | "sent" | "failed" | "bounced";
  delivery_error: string;
  reply_to: string;
  auth_failed: boolean;
  form_sender: string;
  created_at: string;
  edited_at: string | null;
  deleted_at: string | null;  // a deleted message stays as a marker
  deleted_by: UserRef | null;
  purged_at: string | null;   // its content was removed for good
  sent_as_reply_id: string | null; // a note sent as a reply, kept as a marker (deleted_at says when)
};
```

`body_html` is sanitized before storage. For inbound email, quoted history is trimmed from the body and `has_quoted` is true; fetch `/v1/messages/{id}/original` for the full text.

Inbound email also carries what the sender checks found. `reply_to` is the `Reply-To` the message named when it differs from `from_email`. `auth_failed` is true when its `From` failed DMARC at a trusted receiving server: treat the message as unverified. `form_sender` is set when the message came through a trusted website contact form (see [Mailboxes](/api/resources/mailboxes/#contact-forms)); `from_email` is then the visitor. The widget never receives these three.

## Send a reply

```sh
curl https://api.opendesk.sh/v1/tickets/1042/messages \
  -H "Authorization: Bearer od_live_..." \
  -H "Content-Type: application/json" \
  -d '{
    "kind": "reply",
    "body_html": "<p>Thanks Ana, this is fixed now.</p>",
    "cc": ["billing@example.com"],
    "attachment_ids": ["0192f0c5-..."],
    "status": "solved"
  }'
```

A reply's [template variables](/guides/email-templates/variables/), such as `{{ contact.first_name }}`, are filled in when it is created, with you as the agent (an API key has none, so the agent variables are empty). The stored message is what the customer gets. Notes are never filled.

`status` sets the ticket status in the same step. A reply on an email ticket is queued for delivery through the mailbox; watch `delivery_status` change from `pending` to `sent` (or `failed` or `bounced`) through the `message.updated` event.

A reply from a mailbox with no way to send (`sending: "system_off"`, see [Mailboxes](/api/resources/mailboxes/#sending-through-opendesk)) is `422 mailbox_cannot_send` and stores nothing:

```json
{ "status": 422, "code": "mailbox_cannot_send", "detail": "This mailbox has no way to send email yet. Connect Google or Microsoft, or add SMTP, in its settings." }
```

## Reply from your own address

With Collab, a signed-in member can send an email reply from one of their own mail accounts instead of the ticket's address. Pass `from_account` with the account's id; `email` picks one of its addresses (left out: the one you last answered this ticket from, else the account's default), and `reply_to` is `address`, `mailbox` (the ticket's address) or any email address (left out: the ticket's address's `personal_replies.reply_to`, else the workspace's `settings.collab_ticket_reply_to`). Each mailbox can override the workspace's rules and limit which addresses work on its tickets (`personal_replies` on [Mailboxes](/api/resources/mailboxes/)). The ticket's reply address stays as it is, and the message carries `from_account`. API keys and AI clients cannot send this way (`403`). The workspace can turn it off (`409 collab_ticket_replies_off`), and it needs Collab (`402 collab_required`). See [Reply from your own address](/guides/inbox-and-tickets/#reply-from-your-own-address).

```json
{
  "kind": "reply",
  "body_html": "<p>I'll check with the warehouse today.</p>",
  "from_account": { "account_id": "0192f0d1-...", "reply_to": "mailbox" }
}
```

## Add a note

```sh
curl https://api.opendesk.sh/v1/tickets/1042/messages \
  -H "Authorization: Bearer od_live_..." \
  -H "Content-Type: application/json" \
  -d '{ "kind": "note", "body_html": "<p>Refund approved by finance.</p>" }'
```

Notes are never sent to the customer.

## Edit a note

Agents can edit their own plain internal notes. Editing takes sanitized `body_html`, sets `edited_at`, adds a timeline entry without the note text, notifies teammates the note now mentions for the first time and publishes `message.updated`. A note in a side conversation and any reply cannot be edited.

## Turn a note into a reply

Send the reply with `from_note_id`, the id of your own internal note without files. The reply is sent now, at the end of the conversation. In the same step the note becomes a marker where it was, with `sent_as_reply_id` pointing at the reply. The note's own words are kept and hidden like a deleted message's, since the reply may have been edited before it went: teammates see the marker, admins can read the note.

## Turn a chat reply into a note

`POST .../retract` turns a reply you sent on a chat ticket into an internal note: it leaves the customer's chat (`message.withdrawn`), but they may already have read it. It no longer counts as the ticket's first response. Admins can do it for any agent's reply. Email is delivered when it is sent, so an email reply returns `422`, and so does a chat reply already emailed to the customer as a follow-up.

## Delete, restore and purge

Deleting leaves a marker in the conversation with who deleted it and when. Teammates see only the marker; admins can still read the message, restore it or remove its content for good.

- **Who may delete**: admins any message, a customer's included. Agents follow the workspace's `settings.message_delete`: `own` (the default: their own internal notes), `all` (any agent's notes and replies) or `off`, unless an admin set a rule of their own (`PATCH /v1/members/{user_id}/permissions`). So deleting a reply the customer saw is an admin's, unless an admin grants `all`. Agents never delete a customer's, a bot's or an automation's message.
- **What a deleted message leaves**: the customer's chat, search, AI and MCP context, the quoted history of later emails, the ticket's count and preview, and the preview of notifications about it. A reply not yet sent is not sent and is marked `failed` ("Deleted before it was sent."). An email already delivered stays in the customer's inbox.
- **Files** of a deleted message open for admins only, through links signed for it while it is deleted; links handed out before stop working.
- **Purge** (admins, after deleting) removes the text, the subject, the files and the original email for good, for what should never have been written down, with what was made from it: notification previews, the text of Ollie's suggested reply and action card, and the ticket's AI summary. The ticket's subject stays, even when it came from that email: rename the ticket if it must change. Events already delivered to webhooks and copies posted to Slack, Discord or Telegram are not recalled. A purge cannot be undone and is recorded in the audit log.

These routes are for teammates signed in to the app: an API key gets `403`. Deleting returns `204` and publishes `message.updated` with the marker, never the text; replaying events (`GET /v1/events`) shows earlier events about it as the marker too.


## Attachments

Upload first, then reference the id when you send:

```sh
curl https://api.opendesk.sh/v1/attachments \
  -H "Authorization: Bearer od_live_..." \
  -F file=@invoice.pdf
```

```json
{ "id": "0192f0c5-...", "filename": "invoice.pdf", "content_type": "application/pdf", "size_bytes": 48211, "inline": false, "url": "..." }
```

Uploaded attachments are unattached until a message that references them is sent. `GET /v1/attachments/{id}` redirects to a short-lived signed URL, or streams the file when the storage driver cannot sign. For `<img>` tags, where you cannot set headers, the `url` is signed (`?exp=&sig=`) and expires within two hours; fetch the message again for a fresh one.
