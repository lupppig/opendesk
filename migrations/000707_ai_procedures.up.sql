-- Procedures (contracts/agent.md "Procedures"): scoped instructions the AI agent picks by
-- their "use this when" line, each switched on and off alone.
CREATE TABLE ai_procedures (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name          text NOT NULL,
    when_text     text NOT NULL,
    -- Markdown, written by admins.
    instructions  text NOT NULL,
    enabled       boolean NOT NULL DEFAULT true,
    -- Where the agent may pick it by its when_text: 'chat', 'email'; empty = only when an automation runs it.
    channels      text[] NOT NULL DEFAULT '{chat,email}',
    -- Empty = every bot; else only conversations answered as one of these bots (a bot in use here cannot be deleted).
    bot_ids       uuid[] NOT NULL DEFAULT '{}',
    created_by    uuid REFERENCES users(id) ON DELETE SET NULL,
    updated_by    uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ai_procedures_ws_idx ON ai_procedures (workspace_id, updated_at DESC);

-- The procedures the agent used for a test's answer: [{id, name}].
ALTER TABLE ai_agent_test_results ADD COLUMN procedures jsonb NOT NULL DEFAULT '[]';
