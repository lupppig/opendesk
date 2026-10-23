-- Each alias as its own identity (contracts/teams.md "Aliases"): a From name, a signature and a
-- brand of its own, and whether replies go out From the alias itself.
-- {"<alias>": {"display_name", "signature_html", "brand_id", "send_as", "send_as_by"}}; a key is
-- one of `aliases`, and an alias without settings has no entry.
ALTER TABLE mailboxes ADD COLUMN alias_settings jsonb NOT NULL DEFAULT '{}';

-- The send-as addresses Gmail listed for the connected account, read when the connection may
-- read them (a Google sync or api grant): {"account", "checked_at", "addresses": [{"email",
-- "display_name", "verified"}]}. NULL until read.
ALTER TABLE mailboxes ADD COLUMN provider_send_as jsonb;
