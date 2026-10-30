-- A destination either routes (the most specific match on its connection wins) or
-- copies (every matching copy posts too). Conditions narrow where a thread starts, and
-- mirror chooses what the thread shows. Existing destinations keep their behaviour.
ALTER TABLE support_channel_destinations
    ADD COLUMN mode text NOT NULL DEFAULT 'route' CHECK (mode IN ('route', 'copy')),
    ADD COLUMN conditions jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(conditions) = 'array'),
    ADD COLUMN mirror text[] NOT NULL DEFAULT '{customer_replies,agent_replies}'
        CHECK (mirror <@ ARRAY['customer_replies', 'agent_replies', 'status_changes', 'assignee_changes', 'priority_changes']::text[]);

-- Conditions make several routes with the same team and priority meaningful; the API
-- refuses a route that repeats another's team, priority and conditions.
DROP INDEX support_channel_destinations_team_any_priority_idx;
DROP INDEX support_channel_destinations_team_priority_idx;
DROP INDEX support_channel_destinations_priority_idx;
DROP INDEX support_channel_destinations_fallback_idx;
