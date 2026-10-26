-- Spaces: sidebars people build from the workspaces they belong to (contracts/spaces.md).

-- A person's Spaces, one list per user across workspaces and devices.
CREATE TABLE user_spaces (
    user_id    uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    spaces     jsonb NOT NULL DEFAULT '[]',
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- A workspace's starter Space, published by its admins.
CREATE TABLE workspace_starter_spaces (
    workspace_id uuid PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
    space        jsonb NOT NULL,
    updated_by   uuid REFERENCES users(id) ON DELETE SET NULL,
    updated_at   timestamptz NOT NULL DEFAULT now()
);
