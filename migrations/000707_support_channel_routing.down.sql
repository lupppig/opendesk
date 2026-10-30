-- Only plain routes fit the earlier model: one per team and priority on a connection.
DELETE FROM support_channel_destinations WHERE mode = 'copy' OR conditions <> '[]'::jsonb;

DELETE FROM support_channel_destinations d USING support_channel_destinations o
    WHERE o.connection_id = d.connection_id
      AND o.team_id IS NOT DISTINCT FROM d.team_id
      AND o.priority IS NOT DISTINCT FROM d.priority
      AND o.id < d.id;

CREATE UNIQUE INDEX support_channel_destinations_team_any_priority_idx
    ON support_channel_destinations(connection_id, team_id)
    WHERE team_id IS NOT NULL AND priority IS NULL;

CREATE UNIQUE INDEX support_channel_destinations_team_priority_idx
    ON support_channel_destinations(connection_id, team_id, priority)
    WHERE team_id IS NOT NULL AND priority IS NOT NULL;

CREATE UNIQUE INDEX support_channel_destinations_priority_idx
    ON support_channel_destinations(connection_id, priority)
    WHERE team_id IS NULL AND priority IS NOT NULL;

CREATE UNIQUE INDEX support_channel_destinations_fallback_idx
    ON support_channel_destinations(connection_id)
    WHERE team_id IS NULL AND priority IS NULL;

ALTER TABLE support_channel_destinations
    DROP COLUMN mirror,
    DROP COLUMN conditions,
    DROP COLUMN mode;
