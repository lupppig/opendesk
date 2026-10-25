---
title: Email security and data controls
description: What opendesk checks, and where scanning, DLP, retention, and legal holds belong.
---

opendesk handles helpdesk email and Collab mail. It does not provide antivirus scanning, data loss prevention (DLP), configurable retention schedules, or legal holds. The hosted and self-hosted services use the same application behavior. This page separates built-in controls from controls supplied by mail providers and operators.

## Attachment scanning and quarantine

opendesk does not scan attachments for malware or provide quarantine, release, or scanner-unavailable workflows. File type checks and safe browser rendering reduce specific risks, but they do not establish that a file is clean.

| Attachment path | Built-in behavior | Where to apply malware scanning |
| --- | --- | --- |
| Files uploaded through the app or API | opendesk rejects known executable and script types by filename or file signature. It stores active document types so browsers will not render them. | Scan files before users upload them, or use a controlled upload gateway. |
| Attachments received in helpdesk email | opendesk stores each file with the type read from its contents, keeping active types such as HTML and SVG as plain text, and serves it as a download. | Scan at the mail provider or before forwarding mail into opendesk. A custom inbound pipeline can post mail to the [inbound email API](/api/resources/mailboxes/#inbound-email). |
| Mail synced into Collab | opendesk reads mail from Gmail, Microsoft, or IMAP and stores a local copy. Files are served as downloads. Older mail brought in by a history import on opendesk Cloud keeps only its text and sanitized HTML locally; its files are read from the provider when opened. | Apply provider-side scanning before sync. Collab reads provider mail directly, so a gateway in front of the helpdesk forwarding address does not cover Collab. |

Attachment endpoints return files with browser protections. The app sanitizes email HTML, renders it in a sandboxed frame, and waits for the reader before loading remote images. These controls limit how content runs or renders in a browser; they are not antivirus scanning. See [security controls](/security/) for the file and rendering controls.

opendesk has no scanner plugin or fail-open/fail-closed setting. If an upstream scanner is unavailable, its provider or gateway decides whether to hold, reject, or deliver the message.

## Outbound data loss prevention

opendesk does not inspect message text or attachments against organization rules, redact matches, or block and release a message. The connected mailbox provider or SMTP server sends helpdesk replies and Collab mail. Apply outbound DLP at that provider or at an SMTP relay the mailbox uses. See [self-hosted email setup](/self-hosting/email/#outbound-replies) for available sending paths.

## Retention and deletion

opendesk has no configurable retention schedule or legal-hold feature for helpdesk tickets or Collab mail. Ticket deletion and Collab sync have different effects:

Helpdesk email becomes a workspace ticket and follows ticket visibility. Collab stores personal or shared mailbox copies. A private account is visible only to its connector; a shared account follows its configured audience. When a member answers a ticket from their own Collab address, answers to that reply are copied into the ticket and from then on follow ticket visibility. No other mail from the account reaches the ticket.

| Data | What happens in opendesk | What remains outside opendesk |
| --- | --- | --- |
| Helpdesk ticket | `DELETE /v1/tickets/{id}` soft-deletes the ticket. This hides it from normal ticket queries; it is not a purge or a legal hold. | The ticket operation does not delete copies held by mail providers. External archives and backups follow their own retention rules. |
| Collab account | Disconnecting an account removes its local mail and queues stored raw messages for storage cleanup. | The messages are not deleted from the provider. Google grant revocation is best effort. |
| Collab message | On the next sync, opendesk removes a message gone from every provider folder and queues its stored raw message for cleanup. | Provider retention, archive, and legal-hold behavior remain with the provider. opendesk does not copy or verify those controls. |

## Exporting records

The helpdesk API can read tickets, messages, original inbound messages, and attachments, subject to normal access checks. It does not create one immutable archive or a legal-hold export. Collab mail can be read only through user-session routes. API keys and MCP clients cannot access it. Collab has no dedicated export endpoint. If you need an archive or hold, use your mail provider or records archive. Confirm it covers the records before deleting tickets or disconnecting Collab accounts. See [tickets](/api/resources/tickets/) and [messages and attachments](/api/resources/messages/).

Event replay records default to seven days. Self-hosted operators can change this with `EVENT_RETENTION`. This setting does not apply to tickets, messages, or attachments. [Backups and upgrades](/self-hosting/backups-and-upgrades/) explains what self-hosted backups contain and how restore works.

## Hosted and self-hosted responsibilities

The application does not add scanning, DLP, retention, or legal-hold controls in either deployment. On opendesk Cloud, opendesk operates the app and inbound forwarding route; the app has no per-workspace scanner setting. Use provider-side scanning before forwarding mail or connecting a mailbox. For self-hosted instances, the operator chooses mail routing, provider settings, storage, and backups. Operators can put a scanner before the support-mail route or use a custom [inbound email pipeline](/self-hosting/email/#option-c-your-own-pipeline). Mail read directly from Gmail, Microsoft, or IMAP still needs provider-side controls.
