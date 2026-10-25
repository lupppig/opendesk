-- How a message arrived, which decides whose sender checks are believed for it:
-- '' read from the mailbox's own provider or IMAP server, 'forward' through a forwarding
-- address by the signed inbound endpoint or a listed relay, 'direct' straight from the
-- sending server at the built-in SMTP receiver, 'member' read from a member's own Collab
-- account (an answer to a ticket reply sent from it).
ALTER TABLE inbound_emails ADD COLUMN via text NOT NULL DEFAULT '';
