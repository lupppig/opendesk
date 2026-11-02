package ai

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
)

// Procedures (contracts/agent.md "Procedures"): admin-written instructions for one kind
// of conversation. The agent sees each enabled procedure's name and "use this when" line
// and reads the instructions with use_procedure when the conversation matches.

const (
	maxProcedures          = 50
	procedureNameRunes     = 80
	procedureWhenMinRunes  = 3
	procedureWhenRunes     = 300
	procedureInstructRunes = 8000
	// agentProcedureList is how many procedures one conversation lists, newest-updated first.
	agentProcedureList = 30
)

// Procedure channels: where the agent may pick a procedure by its when line.
const (
	procedureChat  = "chat"
	procedureEmail = "email"
)

// Procedure is Procedure from contracts/agent.md.
type Procedure struct {
	ID           uuid.UUID       `json:"id"`
	Name         string          `json:"name"`
	When         string          `json:"when" doc:"Use this when: how the agent recognises a conversation it applies to"`
	Instructions string          `json:"instructions" doc:"Markdown: what the agent does"`
	Enabled      bool            `json:"enabled"`
	Channels     []string        `json:"channels" doc:"chat, email: where the agent may pick it by its when line; empty = only when an automation runs it"`
	BotIDs       []uuid.UUID     `json:"bot_ids" doc:"Empty = every bot; else only conversations answered as one of these bots"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
	UpdatedBy    *domain.UserRef `json:"updated_by"`
}

// ProcedureInput is POST /v1/ai/procedures.
type ProcedureInput struct {
	Name         string       `json:"name" maxLength:"400"`
	When         string       `json:"when" maxLength:"1200"`
	Instructions string       `json:"instructions" maxLength:"32000"`
	Enabled      *bool        `json:"enabled,omitempty" doc:"Default true"`
	Channels     *[]string    `json:"channels,omitempty" maxItems:"2" doc:"Default [\"chat\", \"email\"]"`
	BotIDs       *[]uuid.UUID `json:"bot_ids,omitempty" maxItems:"50" doc:"Default []: every bot"`
}

// ProcedurePatch is PATCH /v1/ai/procedures/{id}; absent members keep their value.
type ProcedurePatch struct {
	Name         *string      `json:"name,omitempty" maxLength:"400"`
	When         *string      `json:"when,omitempty" maxLength:"1200"`
	Instructions *string      `json:"instructions,omitempty" maxLength:"32000"`
	Enabled      *bool        `json:"enabled,omitempty"`
	Channels     *[]string    `json:"channels,omitempty" maxItems:"2"`
	BotIDs       *[]uuid.UUID `json:"bot_ids,omitempty" maxItems:"50"`
}

// ProcedureRef is a procedure the agent used, as replies, activities and test results
// record it.
type ProcedureRef struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// procedureRefs is never null in JSON.
func procedureRefs(in []ProcedureRef) []ProcedureRef {
	if in == nil {
		return []ProcedureRef{}
	}
	return in
}

type procedureList struct {
	Data []Procedure `json:"data"`
}

var (
	errProcedureNotFound = httpx.NotFound("Procedure")
	errProcedureLimit    = httpx.E(http.StatusUnprocessableEntity, "limit_reached", "A workspace can have at most 50 procedures.")
)

func (s *Service) registerProcedures(api huma.API) {
	tags := []string{"AI"}
	read := func(ctx context.Context) (*auth.Principal, error) {
		p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsRead)
		return p, httpx.Wrap(err)
	}
	write := func(ctx context.Context) (*auth.Principal, error) {
		p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
		return p, httpx.Wrap(err)
	}

	huma.Register(api, huma.Operation{OperationID: "ai-list-procedures", Method: http.MethodGet, Path: "/v1/ai/procedures",
		Summary: "List procedures", Description: "The AI agent's procedures, oldest first.", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body procedureList }, error) {
			p, err := read(ctx)
			if err != nil {
				return nil, err
			}
			list, err := loadProcedures(ctx, s.d.DB.Pool, p.WorkspaceID, nil)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body procedureList }{procedureList{Data: list}}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "ai-create-procedure", Method: http.MethodPost, Path: "/v1/ai/procedures",
		Summary: "Create a procedure", Description: "Instructions the agent follows when a conversation matches `when`.",
		Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *struct{ Body ProcedureInput }) (*struct{ Body Procedure }, error) {
			p, err := write(ctx)
			if err != nil {
				return nil, err
			}
			pr, err := s.createProcedure(ctx, p, in.Body)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body Procedure }{pr}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "ai-get-procedure", Method: http.MethodGet, Path: "/v1/ai/procedures/{id}",
		Summary: "Get a procedure", Tags: tags},
		func(ctx context.Context, in *struct {
			ID uuid.UUID `path:"id"`
		}) (*struct{ Body Procedure }, error) {
			p, err := read(ctx)
			if err != nil {
				return nil, err
			}
			pr, err := loadProcedure(ctx, s.d.DB.Pool, p.WorkspaceID, in.ID)
			if err != nil {
				return nil, procedureErr(err)
			}
			return &struct{ Body Procedure }{pr}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "ai-update-procedure", Method: http.MethodPatch, Path: "/v1/ai/procedures/{id}",
		Summary: "Update a procedure", Tags: tags},
		func(ctx context.Context, in *struct {
			ID   uuid.UUID `path:"id"`
			Body ProcedurePatch
		}) (*struct{ Body Procedure }, error) {
			p, err := write(ctx)
			if err != nil {
				return nil, err
			}
			pr, err := s.updateProcedure(ctx, p, in.ID, in.Body)
			if err != nil {
				return nil, procedureErr(err)
			}
			return &struct{ Body Procedure }{pr}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "ai-delete-procedure", Method: http.MethodDelete, Path: "/v1/ai/procedures/{id}",
		Summary: "Delete a procedure", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			ID uuid.UUID `path:"id"`
		}) (*struct{}, error) {
			p, err := write(ctx)
			if err != nil {
				return nil, err
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				var name string
				if err := tx.QueryRow(ctx, `DELETE FROM ai_procedures WHERE workspace_id = $1 AND id = $2 RETURNING name`, p.WorkspaceID, in.ID).Scan(&name); err != nil {
					return err
				}
				if err := audit.Log(ctx, tx, p, p.WorkspaceID, "ai.procedure_deleted", "ai_procedure", &in.ID, map[string]any{"name": name}); err != nil {
					return err
				}
				_, err := events.Publish(ctx, tx, events.Event{WorkspaceID: p.WorkspaceID, Type: "ai_procedure.deleted", Admins: true,
					Data: map[string]any{"procedure_id": in.ID}})
				return err
			})
			if err != nil {
				return nil, procedureErr(err)
			}
			return nil, nil
		})
}

func procedureErr(err error) error {
	if db.IsNotFound(err) {
		return errProcedureNotFound
	}
	return httpx.Wrap(err)
}

// --- Validation -------------------------------------------------------------------------

// cleanProcedureName is a name people choose: control characters dropped, spaces folded,
// 1..80 characters.
func cleanProcedureName(s string) (string, error) {
	s = displayname.Normalize(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(s, "")))
	if s == "" || utf8.RuneCountInString(s) > procedureNameRunes {
		return "", httpx.Validation("name must be 1 to 80 characters")
	}
	return s, nil
}

// cleanProcedureWhen is the "use this when" line: plain text on one line, 3..300 characters.
func cleanProcedureWhen(s string) (string, error) {
	s = strings.Join(strings.FieldsFunc(strings.ToValidUTF8(s, ""), func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || unicode.In(r, unicode.Cf)
	}), " ")
	if n := utf8.RuneCountInString(s); n < procedureWhenMinRunes || n > procedureWhenRunes {
		return "", httpx.Validation("when must be 3 to 300 characters")
	}
	return s, nil
}

// cleanProcedureInstructions is markdown of 1..8,000 characters: line breaks and tabs kept,
// other control characters dropped.
func cleanProcedureInstructions(s string) (string, error) {
	s = strings.ReplaceAll(strings.ToValidUTF8(s, ""), "\r\n", "\n")
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) || (unicode.In(r, unicode.Cf) && r != '\u200d') {
			return -1
		}
		return r
	}, s))
	if s == "" || utf8.RuneCountInString(s) > procedureInstructRunes {
		return "", httpx.Validation("instructions must be 1 to 8000 characters")
	}
	return s, nil
}

// cleanProcedureChannels keeps chat and email once each, in that order.
func cleanProcedureChannels(in []string) ([]string, error) {
	out := []string{}
	for _, c := range []string{procedureChat, procedureEmail} {
		if slices.Contains(in, c) {
			out = append(out, c)
		}
	}
	for _, c := range in {
		if c != procedureChat && c != procedureEmail {
			return nil, httpx.Validation("channels may be chat and email")
		}
	}
	return out, nil
}

// cleanProcedureBots checks that every id is a bot of the workspace.
func cleanProcedureBots(ctx context.Context, q db.Querier, wid uuid.UUID, in []uuid.UUID) ([]uuid.UUID, error) {
	out := []uuid.UUID{}
	for _, id := range in {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		return out, nil
	}
	var n int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM bots WHERE workspace_id = $1 AND id = ANY($2)`, wid, out).Scan(&n); err != nil {
		return nil, err
	}
	if n != len(out) {
		return nil, httpx.Validation("bot_ids must be bots of this workspace")
	}
	return out, nil
}

// --- Store ------------------------------------------------------------------------------

func (s *Service) createProcedure(ctx context.Context, p *auth.Principal, in ProcedureInput) (Procedure, error) {
	name, err := cleanProcedureName(in.Name)
	if err != nil {
		return Procedure{}, err
	}
	when, err := cleanProcedureWhen(in.When)
	if err != nil {
		return Procedure{}, err
	}
	instructions, err := cleanProcedureInstructions(in.Instructions)
	if err != nil {
		return Procedure{}, err
	}
	channels := []string{procedureChat, procedureEmail}
	if in.Channels != nil {
		if channels, err = cleanProcedureChannels(*in.Channels); err != nil {
			return Procedure{}, err
		}
	}
	enabled := in.Enabled == nil || *in.Enabled
	wid := p.WorkspaceID
	var out Procedure
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		botIDs := []uuid.UUID{}
		if in.BotIDs != nil {
			if botIDs, err = cleanProcedureBots(ctx, tx, wid, *in.BotIDs); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1::text || ':ai_procedures'))`, wid); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM ai_procedures WHERE workspace_id = $1`, wid).Scan(&n); err != nil {
			return err
		}
		if n >= maxProcedures {
			return errProcedureLimit
		}
		id := ids.New()
		if _, err := tx.Exec(ctx, `INSERT INTO ai_procedures (id, workspace_id, name, when_text, instructions, enabled, channels, bot_ids, created_by, updated_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)`, id, wid, name, when, instructions, enabled, channels, botIDs, p.ActorUserID()); err != nil {
			return err
		}
		if out, err = loadProcedure(ctx, tx, wid, id); err != nil {
			return err
		}
		if err := audit.Log(ctx, tx, p, wid, "ai.procedure_created", "ai_procedure", &id, map[string]any{"name": name}); err != nil {
			return err
		}
		return publishProcedure(ctx, tx, wid, "ai_procedure.created", out)
	})
	return out, err
}

func (s *Service) updateProcedure(ctx context.Context, p *auth.Principal, id uuid.UUID, in ProcedurePatch) (Procedure, error) {
	wid := p.WorkspaceID
	var out Procedure
	err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		cur, err := loadProcedure(ctx, tx, wid, id, "FOR UPDATE OF r")
		if err != nil {
			return err
		}
		changes := []string{}
		if in.Name != nil {
			name, err := cleanProcedureName(*in.Name)
			if err != nil {
				return err
			}
			if name != cur.Name {
				cur.Name, changes = name, append(changes, "name")
			}
		}
		if in.When != nil {
			when, err := cleanProcedureWhen(*in.When)
			if err != nil {
				return err
			}
			if when != cur.When {
				cur.When, changes = when, append(changes, "when")
			}
		}
		if in.Instructions != nil {
			ins, err := cleanProcedureInstructions(*in.Instructions)
			if err != nil {
				return err
			}
			if ins != cur.Instructions {
				cur.Instructions, changes = ins, append(changes, "instructions")
			}
		}
		if in.Enabled != nil && *in.Enabled != cur.Enabled {
			cur.Enabled, changes = *in.Enabled, append(changes, "enabled")
		}
		if in.Channels != nil {
			ch, err := cleanProcedureChannels(*in.Channels)
			if err != nil {
				return err
			}
			if !slices.Equal(ch, cur.Channels) {
				cur.Channels, changes = ch, append(changes, "channels")
			}
		}
		if in.BotIDs != nil {
			bs, err := cleanProcedureBots(ctx, tx, wid, *in.BotIDs)
			if err != nil {
				return err
			}
			if !slices.Equal(bs, cur.BotIDs) {
				cur.BotIDs, changes = bs, append(changes, "bot_ids")
			}
		}
		if len(changes) == 0 {
			out = cur
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE ai_procedures SET name = $3, when_text = $4, instructions = $5, enabled = $6, channels = $7, bot_ids = $8,
			updated_by = $9, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
			wid, id, cur.Name, cur.When, cur.Instructions, cur.Enabled, cur.Channels, cur.BotIDs, p.ActorUserID()); err != nil {
			return err
		}
		if out, err = loadProcedure(ctx, tx, wid, id); err != nil {
			return err
		}
		if err := audit.Log(ctx, tx, p, wid, "ai.procedure_updated", "ai_procedure", &id, map[string]any{"name": out.Name, "changes": changes}); err != nil {
			return err
		}
		return publishProcedure(ctx, tx, wid, "ai_procedure.updated", out)
	})
	return out, err
}

// publishProcedure tells admins, the only ones who may read procedures.
func publishProcedure(ctx context.Context, tx pgx.Tx, wid uuid.UUID, typ string, pr Procedure) error {
	_, err := events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: typ, Admins: true, Data: map[string]any{"procedure": pr}})
	return err
}

const procedureSelect = `SELECT r.id, r.name, r.when_text, r.instructions, r.enabled, r.channels, r.bot_ids, r.created_at, r.updated_at,
	u.id, u.name, u.email, u.avatar_url
FROM ai_procedures r LEFT JOIN users u ON u.id = r.updated_by`

func scanProcedures(rows pgx.Rows) ([]Procedure, error) {
	defer rows.Close()
	out := []Procedure{}
	for rows.Next() {
		var pr Procedure
		var uid *uuid.UUID
		var uname, uemail, uavatar *string
		if err := rows.Scan(&pr.ID, &pr.Name, &pr.When, &pr.Instructions, &pr.Enabled, &pr.Channels, &pr.BotIDs, &pr.CreatedAt, &pr.UpdatedAt,
			&uid, &uname, &uemail, &uavatar); err != nil {
			return nil, err
		}
		if uid != nil {
			pr.UpdatedBy = &domain.UserRef{ID: *uid, Name: deref(uname), Email: deref(uemail), AvatarURL: domain.Avatar(deref(uavatar))}
		}
		if pr.Channels == nil {
			pr.Channels = []string{}
		}
		if pr.BotIDs == nil {
			pr.BotIDs = []uuid.UUID{}
		}
		out = append(out, pr)
	}
	return out, rows.Err()
}

// loadProcedures returns a workspace's procedures (or the one id), oldest first.
func loadProcedures(ctx context.Context, q db.Querier, wid uuid.UUID, id *uuid.UUID, lock ...string) ([]Procedure, error) {
	rows, err := q.Query(ctx, procedureSelect+` WHERE r.workspace_id = $1 AND ($2::uuid IS NULL OR r.id = $2)
		ORDER BY r.created_at, r.id `+strings.Join(lock, " "), wid, id)
	if err != nil {
		return nil, err
	}
	return scanProcedures(rows)
}

func loadProcedure(ctx context.Context, q db.Querier, wid, id uuid.UUID, lock ...string) (Procedure, error) {
	list, err := loadProcedures(ctx, q, wid, &id, lock...)
	if err != nil {
		return Procedure{}, err
	}
	if len(list) == 0 {
		return Procedure{}, pgx.ErrNoRows
	}
	return list[0], nil
}

// agentProcedures are the enabled procedures the agent may pick in this channel as this
// bot: at most 30, newest-updated first.
func agentProcedures(ctx context.Context, q db.Querier, wid uuid.UUID, channel string, bot uuid.UUID) ([]Procedure, error) {
	rows, err := q.Query(ctx, procedureSelect+` WHERE r.workspace_id = $1 AND r.enabled AND $2 = ANY(r.channels)
		AND (cardinality(r.bot_ids) = 0 OR $3 = ANY(r.bot_ids))
		ORDER BY r.updated_at DESC, r.id LIMIT $4`, wid, channel, bot, agentProcedureList)
	if err != nil {
		return nil, err
	}
	return scanProcedures(rows)
}

// renderProcedures lists procedures for the agent's guidance: id, name and when line.
// They are admin-written, still angle-escaped so they read as one unit.
func renderProcedures(ps []Procedure) string {
	if len(ps) == 0 {
		return ""
	}
	var b strings.Builder
	for _, p := range ps {
		b.WriteString(block("procedure", []string{"id", p.ID.String(), "name", p.Name}, untrusted(p.When)))
		b.WriteString("\n")
	}
	return block("procedures", nil, strings.TrimSpace(b.String()))
}

// procedureInstructions is what use_procedure returns.
func procedureInstructions(p Procedure) string {
	return block("procedure_instructions", []string{"id", p.ID.String(), "name", p.Name}, untrusted(p.Instructions))
}
