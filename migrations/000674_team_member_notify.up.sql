-- Whether a lead or member hears about the team's new tickets and the replies on its
-- tickets nobody has (contracts/api.md "Notification preferences and emails"). It goes with
-- the membership: leaving a team and joining again starts from on.
ALTER TABLE team_members ADD COLUMN notify boolean NOT NULL DEFAULT true;
