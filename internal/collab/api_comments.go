package collab

import (
	"context"
	"net/http"
	"regexp"
	"slices"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
)

// maxCommentHTML bounds a comment's HTML.
const maxCommentHTML = 20000

// maxMentions bounds the people one comment mentions.
const maxMentions = 50

var mentionAttr = regexp.MustCompile(`data-(?:mention|id)="([0-9a-fA-F-]{36})"`)

type commentBody struct {
	Body MailComment
}

func (s *service) registerCommentRoutes(api huma.API) {
	collabOp(api, huma.Operation{OperationID: "create-collab-comment", Method: http.MethodPost, Path: "/v1/collab/threads/{id}/comments",
		Summary: "Comment on a conversation"},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body struct {
				BodyHTML string      `json:"body_html" minLength:"1" maxLength:"20000"`
				Mentions []uuid.UUID `json:"mentions,omitempty" maxItems:"50"`
			}
		}) (*commentBody, error) {
			p, err := s.require(ctx, auth.RoleAgent)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID, "Conversation")
			if err != nil {
				return nil, err
			}
			t, err := visibleThread(ctx, s.d.DB.Pool, p, id)
			if err != nil {
				return nil, notFound(err, "Conversation")
			}
			body, err := cleanComment(in.Body.BodyHTML)
			if err != nil {
				return nil, err
			}
			cid := ids.New()
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				before, err := threadsAudience(ctx, tx, p.WorkspaceID, []uuid.UUID{t.ID})
				if err != nil {
					return err
				}
				mentions, err := mentioned(ctx, tx, p, body, in.Body.Mentions)
				if err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `INSERT INTO collab_comments (id, workspace_id, thread_id, author_id, body_html, mentions)
					VALUES ($1, $2, $3, $4, $5, $6)`, cid, p.WorkspaceID, t.ID, p.UserID, body, mentions); err != nil {
					return err
				}
				if err := s.mentionPeople(ctx, tx, p, t, cid, body, mentions); err != nil {
					return err
				}
				return s.publishThread(ctx, tx, p.WorkspaceID, t, before)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return s.commentResponse(ctx, p, cid)
		})

	collabOp(api, huma.Operation{OperationID: "update-collab-comment", Method: http.MethodPatch, Path: "/v1/collab/comments/{id}",
		Summary: "Edit my comment"},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body struct {
				BodyHTML string `json:"body_html" minLength:"1" maxLength:"20000"`
			}
		}) (*commentBody, error) {
			p, err := s.require(ctx, auth.RoleAgent)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			c, t, err := s.visibleComment(ctx, p, in.ID)
			if err != nil {
				return nil, err
			}
			if c.AuthorID == nil || *c.AuthorID != p.UserID {
				return nil, httpx.Forbidden()
			}
			body, err := cleanComment(in.Body.BodyHTML)
			if err != nil {
				return nil, err
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				before, err := threadsAudience(ctx, tx, p.WorkspaceID, []uuid.UUID{t.ID})
				if err != nil {
					return err
				}
				// Mentions follow the edited text; only people newly mentioned are told.
				mentions, err := mentioned(ctx, tx, p, body, nil)
				if err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE collab_comments SET body_html = $3, mentions = $4, updated_at = now()
					WHERE workspace_id = $1 AND id = $2`, p.WorkspaceID, c.ID, body, mentions); err != nil {
					return err
				}
				var added []uuid.UUID
				for _, u := range mentions {
					if !slices.Contains(c.Mentions, u) {
						added = append(added, u)
					}
				}
				if err := s.mentionPeople(ctx, tx, p, t, c.ID, body, added); err != nil {
					return err
				}
				return s.publishThread(ctx, tx, p.WorkspaceID, t, before)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return s.commentResponse(ctx, p, c.ID)
		})

	collabOp(api, huma.Operation{OperationID: "delete-collab-comment", Method: http.MethodDelete, Path: "/v1/collab/comments/{id}",
		Summary: "Delete a comment", DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct{}, error) {
			p, err := s.require(ctx, auth.RoleAgent)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			c, t, err := s.visibleComment(ctx, p, in.ID)
			if err != nil {
				return nil, err
			}
			if (c.AuthorID == nil || *c.AuthorID != p.UserID) && !p.Role.AtLeast(auth.RoleAdmin) {
				return nil, httpx.Forbidden()
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, `DELETE FROM collab_comments WHERE workspace_id = $1 AND id = $2`, p.WorkspaceID, c.ID); err != nil {
					return err
				}
				return s.publishChanged(ctx, tx, p.WorkspaceID, &t.AccountID, []uuid.UUID{t.ID}, false)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return nil, nil
		})
}

// cleanComment sanitizes a comment like a note; nothing left is refused.
func cleanComment(raw string) (string, error) {
	if len(raw) > maxCommentHTML {
		return "", httpx.Validation("A comment is at most 20000 characters.")
	}
	body := hd.SanitizeRich(raw)
	if hd.HTMLToText(body) == "" && !mentionAttr.MatchString(body) {
		return "", httpx.Validation("Write a comment.")
	}
	return body, nil
}

// mentioned is who a comment mentions: the listed ids and the editor's mention marks, kept
// to members of the workspace, without the author.
func mentioned(ctx context.Context, tx pgx.Tx, p *auth.Principal, body string, listed []uuid.UUID) ([]uuid.UUID, error) {
	cand := appendNew(nil, listed...)
	for _, m := range mentionAttr.FindAllStringSubmatch(body, -1) {
		if id, ok := ids.Parse(m[1]); ok {
			cand = appendNew(cand, id)
		}
	}
	cand = slices.DeleteFunc(cand, func(id uuid.UUID) bool { return id == p.UserID })
	if len(cand) == 0 {
		return []uuid.UUID{}, nil
	}
	if len(cand) > maxMentions {
		return nil, httpx.Validation("Mention at most 50 people.")
	}
	rows, err := tx.Query(ctx, `SELECT user_id FROM members WHERE workspace_id = $1 AND user_id = ANY($2)`, p.WorkspaceID, cand)
	if err != nil {
		return nil, err
	}
	members, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, err
	}
	// Keep the order they were mentioned in.
	return slices.DeleteFunc(cand, func(id uuid.UUID) bool { return !slices.Contains(members, id) }), nil
}

// mentionPeople shares the conversation with each mentioned member who cannot see it yet,
// then notifies them all, in tx.
func (s *service) mentionPeople(ctx context.Context, tx pgx.Tx, p *auth.Principal, t *threadRow, commentID uuid.UUID, body string, people []uuid.UUID) error {
	if len(people) == 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT u FROM unnest($3::uuid[]) u WHERE NOT EXISTS (SELECT 1 FROM collab_threads t
		WHERE t.workspace_id = $1 AND t.id = $2 AND `+threadVisibleSQL("t", "u")+`)`, p.WorkspaceID, t.ID, people)
	if err != nil {
		return err
	}
	blind, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	if len(blind) > 0 {
		for _, u := range blind {
			if _, err := tx.Exec(ctx, `INSERT INTO collab_shares (id, workspace_id, thread_id, user_id, shared_by)
				VALUES ($1, $2, $3, $4, $5) ON CONFLICT DO NOTHING`, ids.New(), p.WorkspaceID, t.ID, u, p.UserID); err != nil {
				return err
			}
		}
		shares, err := loadAudience(ctx, tx, p.WorkspaceID, nil, &t.ID)
		if err != nil {
			return err
		}
		if err := audit.Log(ctx, tx, p, p.WorkspaceID, "collab.thread_shared", "collab_thread", &t.ID, map[string]any{
			"thread_id": t.ID, "everyone": shares.Everyone, "team_ids": shares.TeamIDs, "user_ids": shares.UserIDs}); err != nil {
			return err
		}
	}
	preview := hd.Excerpt(hd.HTMLToText(body), 140)
	for _, u := range people {
		if err := s.notify(ctx, tx, p.WorkspaceID, u, NotifyMention, map[string]any{"thread_id": t.ID.String(),
			"comment_id": commentID.String(), "by": p.UserID.String(), "subject": t.Subject, "preview": preview}); err != nil {
			return err
		}
	}
	return nil
}

// visibleComment loads a comment on a conversation the caller sees.
func (s *service) visibleComment(ctx context.Context, p *auth.Principal, rawID string) (*commentRow, *threadRow, error) {
	id, err := parseID(rawID, "Comment")
	if err != nil {
		return nil, nil, err
	}
	c, err := scanComment(s.d.DB.Pool.QueryRow(ctx, `SELECT `+commentColumns+` FROM collab_comments WHERE workspace_id = $1 AND id = $2`,
		p.WorkspaceID, id))
	if err != nil {
		return nil, nil, notFound(err, "Comment")
	}
	t, err := visibleThread(ctx, s.d.DB.Pool, p, c.ThreadID)
	if err != nil {
		return nil, nil, notFound(err, "Comment")
	}
	return c, t, nil
}

func (s *service) commentResponse(ctx context.Context, p *auth.Principal, id uuid.UUID) (*commentBody, error) {
	c, err := scanComment(s.d.DB.Pool.QueryRow(ctx, `SELECT `+commentColumns+` FROM collab_comments WHERE workspace_id = $1 AND id = $2`,
		p.WorkspaceID, id))
	if err != nil {
		return nil, httpx.Wrap(err)
	}
	out, err := commentsOut(ctx, s.d.DB.Pool, []*commentRow{c})
	if err != nil {
		return nil, httpx.Wrap(err)
	}
	return &commentBody{out[0]}, nil
}
