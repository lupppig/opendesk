package domain

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/db"
)

// TagSelect reads Tag rows (alias g), with usage counts. The ticket count leaves out
// private teams' tickets: everyone reads it, so it tells nobody about tickets they can't see.
const TagSelect = `SELECT g.id, g.name, g.color, g.description, g.group_id, g.position, g.archived, g.applies_to,
	(SELECT count(*) FROM ticket_tags x JOIN tickets t ON t.id = x.ticket_id AND t.deleted_at IS NULL
		WHERE x.tag_id = g.id AND NOT EXISTS (SELECT 1 FROM teams pt WHERE pt.id = t.team_id AND pt.private)),
	(SELECT count(*) FROM contact_tags x WHERE x.tag_id = g.id),
	(SELECT count(*) FROM company_tags x WHERE x.tag_id = g.id),
	g.created_at, g.updated_at
FROM tags g LEFT JOIN tag_groups tg ON tg.id = g.group_id`

// TagOrder orders labels as lists show them: by group, then position, then name.
const TagOrder = ` ORDER BY tg.position NULLS LAST, g.position, lower(g.name), g.id`

func ScanTag(r pgx.Row) (Tag, error) {
	var t Tag
	err := r.Scan(&t.ID, &t.Name, &t.Color, &t.Description, &t.GroupID, &t.Position, &t.Archived, &t.AppliesTo,
		&t.Usage.Tickets, &t.Usage.Contacts, &t.Usage.Companies, &t.CreatedAt, &t.UpdatedAt)
	if t.AppliesTo == nil {
		t.AppliesTo = []string{}
	}
	return t, err
}

// LoadTag loads one label of the workspace.
func LoadTag(ctx context.Context, q db.Querier, wid, id uuid.UUID) (Tag, error) {
	return ScanTag(q.QueryRow(ctx, TagSelect+` WHERE g.workspace_id = $1 AND g.id = $2`, wid, id))
}

const TagGroupSelect = `SELECT tg.id, tg.name, tg.color, tg.exclusive, tg.position,
	(SELECT count(*) FROM tags g WHERE g.group_id = tg.id), tg.created_at, tg.updated_at
FROM tag_groups tg`

func ScanTagGroup(r pgx.Row) (TagGroup, error) {
	var g TagGroup
	err := r.Scan(&g.ID, &g.Name, &g.Color, &g.Exclusive, &g.Position, &g.TagCount, &g.CreatedAt, &g.UpdatedAt)
	return g, err
}

func LoadTagGroup(ctx context.Context, q db.Querier, wid, id uuid.UUID) (TagGroup, error) {
	return ScanTagGroup(q.QueryRow(ctx, TagGroupSelect+` WHERE tg.workspace_id = $1 AND tg.id = $2`, wid, id))
}
