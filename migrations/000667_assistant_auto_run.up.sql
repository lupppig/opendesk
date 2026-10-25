-- Changes an admin lets Ollie make without asking first (contracts/ai.md "Approvals").
-- auto_tools: connected tools that run on their own, kept by tool name across syncs like
-- disabled_tools and ask_tools; ask_tools wins over it.
ALTER TABLE integrations ADD COLUMN auto_tools text[] NOT NULL DEFAULT '{}';

-- auto: the change ran without the user's approval, because the workspace allows it.
-- Such a row is written as it runs and ends done or failed; it is never pending.
ALTER TABLE assistant_actions ADD COLUMN auto boolean NOT NULL DEFAULT false;
