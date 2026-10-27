-- A Collab draft an AI credential saved (contracts/collab.md "AI drafts"): the API key
-- that saved it, so only that key may save it again, and the key's name as it was then,
-- for the "AI draft by" label. Saving the draft as a person clears both.
ALTER TABLE collab_messages
    ADD COLUMN ai_api_key_id uuid REFERENCES api_keys(id) ON DELETE SET NULL,
    ADD COLUMN ai_client     text;
