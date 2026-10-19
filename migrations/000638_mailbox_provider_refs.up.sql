-- Support mailboxes that read their inbox through the Gmail API or Microsoft Graph
-- (contracts/api.md "Mailboxes", "Reading the inbox directly") keep the provider's ids of
-- each message they received or sent: {mailbox_id, account, provider, id, thread}, Gmail's
-- message and thread ids or Graph's message and conversation ids. Replies join that
-- conversation, and the mailbox's tidy-up options act on those messages.
ALTER TABLE inbound_emails ADD COLUMN provider_ref jsonb;
ALTER TABLE messages ADD COLUMN provider_ref jsonb;
