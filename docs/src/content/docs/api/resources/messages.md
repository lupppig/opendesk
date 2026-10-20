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
| `DELETE` | `/v1/tickets/{id}/messages/{messageId}` | Delete your own internal note; admins can delete any note |
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

## Add a note

```sh
curl https://api.opendesk.sh/v1/tickets/1042/messages \
  -H "Authorization: Bearer od_live_..." \
  -H "Content-Type: application/json" \
  -d '{ "kind": "note", "body_html": "<p>Refund approved by finance.</p>" }'
```

Notes are never sent to the customer.

## Edit or delete a note

Agents can edit or delete their own plain internal notes. Admins can delete any internal note. A note in a side conversation and any reply are immutable through these routes. Editing takes sanitized `body_html` and sets `edited_at`; deleting removes the note and its attachment records, and removes stored files when no other attachment uses them. Both actions add a timeline entry that identifies the actor without including the note text. Editing notifies teammates the note now mentions for the first time.

Editing and deleting are for teammates signed in to the app: an API key gets `403`, and a note written with an API key can be deleted by an admin.

Edits publish `message.updated`; deletion returns `204` and publishes `message.deleted`.

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
