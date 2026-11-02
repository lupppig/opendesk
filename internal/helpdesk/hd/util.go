package hd

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/text/unicode/norm"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// ParseID parses a path or body id; a malformed id is answered as not found.
func ParseID(s, what string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(s))
	if err != nil {
		return uuid.Nil, httpx.NotFound(what)
	}
	return id, nil
}

// ParseRef parses a referenced id in a body; a malformed id is a validation error.
func ParseRef(s, field string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(s))
	if err != nil {
		return uuid.Nil, httpx.Validation(field + " is not a valid id")
	}
	return id, nil
}

// refTables are the tables a body may reference by id, each with workspace_id.
var refTables = map[string]string{
	"contact": "contacts", "company": "companies", "team": "teams", "mailbox": "mailboxes",
	"tag": "tags", "kb_collection": "kb_collections", "sla_policy": "sla_policies",
	"brand": "brands", "widget": "widgets", "ai_procedure": "ai_procedures",
}

// CheckRef confirms id is a row of kind in the workspace, answering 422 otherwise.
func CheckRef(ctx context.Context, q db.Querier, wid uuid.UUID, kind string, id uuid.UUID) error {
	table, ok := refTables[kind]
	if !ok {
		return fmt.Errorf("unknown ref kind %q", kind)
	}
	var exists bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+table+` WHERE workspace_id = $1 AND id = $2)`, wid, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return httpx.Validation(strings.ReplaceAll(kind, "_", " ") + " not found in this workspace")
	}
	return nil
}

// CheckMember confirms uid is a workspace member who can work tickets (not a viewer).
func CheckMember(ctx context.Context, q db.Querier, wid, uid uuid.UUID) error {
	var role string
	err := q.QueryRow(ctx, `SELECT role FROM members WHERE workspace_id = $1 AND user_id = $2`, wid, uid).Scan(&role)
	if db.IsNotFound(err) || role == "viewer" {
		return httpx.Validation("assignee must be an agent in this workspace")
	}
	return err
}

// EnsureTags returns label ids for names, creating missing labels (and publishing tag.created).
func EnsureTags(ctx context.Context, tx pgx.Tx, wid uuid.UUID, names []string) ([]uuid.UUID, error) {
	return domain.EnsureTags(ctx, tx, wid, names)
}

// CleanTagName normalizes a label name: 1..50 characters, no markup.
func CleanTagName(s string) (string, error) { return domain.CleanTagName(s) }

// CanCreateLabels reports whether p may create labels by applying a new name:
// always, unless settings.label_creation is "admins" (then admins and keys with settings:write).
func CanCreateLabels(ctx context.Context, q db.Querier, p *auth.Principal) (bool, error) {
	switch {
	case p.Kind == auth.KindUser && p.Role.AtLeast(auth.RoleAdmin):
		return true, nil
	case p.Kind == auth.KindAPIKey && p.HasScope(auth.ScopeSettingsWrite):
		return true, nil
	}
	return domain.LabelCreationOpen(ctx, q, p.WorkspaceID)
}

var slugStrip = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify makes a URL slug from a title (ASCII letters and digits, dash separated).
func Slugify(s string) string {
	s = strings.ToLower(norm.NFKD.String(s))
	var b strings.Builder
	for _, r := range s {
		if r < 128 {
			b.WriteRune(r)
		}
	}
	out := strings.Trim(slugStrip.ReplaceAllString(b.String(), "-"), "-")
	if len(out) > 80 {
		out = strings.TrimRight(out[:80], "-")
	}
	return out
}

// UniqueSlug returns base, or base-2, base-3... so it is unique in table for the
// workspace. table is a trusted constant. exclude skips the row being updated.
func UniqueSlug(ctx context.Context, q db.Querier, table string, wid uuid.UUID, base string, exclude *uuid.UUID) (string, error) {
	if base == "" {
		base = crypto.RandomString(8)
	}
	slug := base
	for i := 2; i < 1000; i++ {
		var exists bool
		if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+table+` WHERE workspace_id = $1 AND slug = $2 AND ($3::uuid IS NULL OR id <> $3))`,
			wid, slug, exclude).Scan(&exists); err != nil {
			return "", err
		}
		if !exists {
			return slug, nil
		}
		slug = fmt.Sprintf("%s-%d", base, i)
	}
	return base + "-" + crypto.RandomString(6), nil
}

// Ptr returns a pointer to v.
func Ptr[T any](v T) *T { return &v }

// Empty is a body-less response (204 when the operation sets DefaultStatus).
type Empty struct{}
