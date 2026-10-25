-- Tools an admin wants approved before they run, even when their server marks them
-- read-only (contracts/integrations.md). Kept by tool name across syncs, like
-- disabled_tools.
ALTER TABLE integrations ADD COLUMN ask_tools text[] NOT NULL DEFAULT '{}';
