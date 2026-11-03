DROP INDEX IF EXISTS messages_api_key_idx;
ALTER TABLE messages DROP COLUMN IF EXISTS api_key_id;
DROP INDEX IF EXISTS api_keys_bot_idx;
ALTER TABLE api_keys DROP COLUMN IF EXISTS bot_id;
