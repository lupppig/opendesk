-- An API key may speak as one of the workspace's bots (contracts/api.md "API keys"): what
-- it writes on tickets is shown and sent as that bot. The key keeps no bot once the bot is
-- gone (deleting a bot a live key speaks as is refused first).
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS bot_id uuid REFERENCES bots(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS api_keys_bot_idx ON api_keys (bot_id) WHERE bot_id IS NOT NULL;

-- The API key that wrote a message, so a message sent through the API names its key.
ALTER TABLE messages ADD COLUMN IF NOT EXISTS api_key_id uuid REFERENCES api_keys(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS messages_api_key_idx ON messages (api_key_id) WHERE api_key_id IS NOT NULL;
