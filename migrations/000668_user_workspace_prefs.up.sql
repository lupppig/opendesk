-- A person's Together settings (contracts/api.md "Workspaces together"): how the web app
-- shows the workspaces they belong to. User-level: one row per person, whatever the
-- workspace. The workspace ids in it only arrange the sidebar; they never grant anything.
CREATE TABLE user_workspace_prefs (
    user_id           uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    -- NULL: the default (together for a person in two or more workspaces).
    mode              text CHECK (mode IN ('together', 'separate')),
    layout            text NOT NULL DEFAULT 'grouped' CHECK (layout IN ('grouped', 'mixed')),
    workspace_order   uuid[] NOT NULL DEFAULT '{}',
    hidden_workspaces uuid[] NOT NULL DEFAULT '{}',
    -- Folded sidebar groups: workspace ids, or 'everywhere'.
    collapsed         text[] NOT NULL DEFAULT '{}',
    -- {hidden: [item id], order: [item id]}, ids qualified by workspace ("<wid>/view:<id>").
    items             jsonb NOT NULL DEFAULT '{}',
    -- The Everywhere lists (assigned to me and all mail, across workspaces).
    everywhere        boolean NOT NULL DEFAULT true,
    updated_at        timestamptz NOT NULL DEFAULT now()
);
