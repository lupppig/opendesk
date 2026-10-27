-- The wiki (contracts/wiki.md): the team's internal pages, nested under each other. Members
-- only; never public, never read by the customer-facing AI.

CREATE TABLE wiki_pages (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    parent_id    uuid,
    title        text NOT NULL DEFAULT '',
    icon         text NOT NULL DEFAULT '',
    body_html    text NOT NULL DEFAULT '',
    position     integer NOT NULL DEFAULT 0,
    -- Bumped on every change of title, icon or body: PATCH sends the version it edited.
    version      integer NOT NULL DEFAULT 1,
    created_by   uuid REFERENCES users(id) ON DELETE SET NULL,
    updated_by   uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    search       tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('simple', coalesce(title, '')), 'A') ||
        setweight(to_tsvector('simple', regexp_replace(coalesce(body_html, ''), '<[^>]+>', ' ', 'g')), 'C')
    ) STORED,
    UNIQUE (workspace_id, id),
    -- A parent is a page of the same workspace; removing it removes its subpages.
    FOREIGN KEY (workspace_id, parent_id) REFERENCES wiki_pages(workspace_id, id) ON DELETE CASCADE
);
CREATE INDEX wiki_pages_tree_idx ON wiki_pages(workspace_id, parent_id, position);
CREATE INDEX wiki_pages_parent_idx ON wiki_pages(parent_id) WHERE parent_id IS NOT NULL;
CREATE INDEX wiki_pages_created_by_idx ON wiki_pages(created_by) WHERE created_by IS NOT NULL;
CREATE INDEX wiki_pages_updated_by_idx ON wiki_pages(updated_by) WHERE updated_by IS NOT NULL;
CREATE INDEX wiki_pages_search_idx ON wiki_pages USING gin(search);

-- Snapshots of a page's title, icon and body. One author's edits within a few minutes of
-- each other share one revision (updated_at moves); at most 100 per page are kept.
CREATE TABLE wiki_page_revisions (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    page_id      uuid NOT NULL,
    title        text NOT NULL DEFAULT '',
    icon         text NOT NULL DEFAULT '',
    body_html    text NOT NULL DEFAULT '',
    author_id    uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (workspace_id, page_id) REFERENCES wiki_pages(workspace_id, id) ON DELETE CASCADE
);
CREATE INDEX wiki_page_revisions_page_idx ON wiki_page_revisions(workspace_id, page_id, updated_at DESC);
CREATE INDEX wiki_page_revisions_page_fk_idx ON wiki_page_revisions(page_id);
CREATE INDEX wiki_page_revisions_author_idx ON wiki_page_revisions(author_id) WHERE author_id IS NOT NULL;
