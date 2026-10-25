-- The private team of the event a delivery carries: it is sent only while the webhook is
-- still granted that team (contracts/teams.md "Visibility").
ALTER TABLE webhook_deliveries ADD COLUMN team_id uuid;
