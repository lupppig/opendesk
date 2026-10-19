-- Support mailboxes that read their inbox through the Gmail API or Microsoft Graph
-- (contracts/api.md "Mailboxes", config.oauth_grant "api") keep the provider's cursor here:
-- Gmail's history id, or Graph's delta link of the inbox.
ALTER TABLE mailboxes ADD COLUMN api_sync_state jsonb NOT NULL DEFAULT '{}';
