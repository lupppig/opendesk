package tickets

import (
	"context"
	"math"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// ReportAgentRow is one member's row of GET /v1/reports/agents (contracts/api.md "Agents report").
type ReportAgentRow struct {
	User                    domain.UserRef `json:"user"`
	Assigned                int            `json:"assigned" doc:"Tickets assigned to them in the range, or created in it assigned to them"`
	Solved                  int            `json:"solved" doc:"Tickets solved in the range that are assigned to them"`
	Replies                 int            `json:"replies" doc:"Public replies they sent in the range"`
	Notes                   int            `json:"notes" doc:"Internal notes they wrote in the range"`
	RepliesPerSolved        *float64       `json:"replies_per_solved" doc:"Their public replies on the tickets they solved, per ticket; null without solved tickets"`
	FirstResponseP50Minutes *float64       `json:"first_response_p50_minutes" doc:"Median first response of their tickets created in the range"`
	HandleP50Minutes        *float64       `json:"handle_p50_minutes" doc:"Median time from the last assignment to them until solved, over their solved tickets"`
	OnlineMinutes           *float64       `json:"online_minutes" doc:"Time in the app with availability online in the range; null when online_time is none"`
}

// ReportAgents is GET /v1/reports/agents.
type ReportAgents struct {
	Data       []ReportAgentRow `json:"data"`
	OnlineTime string           `json:"online_time" enum:"all,self,none" doc:"Whose rows and online time the caller sees: everyone's (admins), their own row only, or every row without online time (API keys without settings:read)"`
}

// A member's work and online time are personal: admins (and API keys with settings:read) see
// everyone's rows, other members their own row only, and other API keys every row without
// online time.
const (
	onlineAll  = "all"
	onlineSelf = "self"
	onlineNone = "none"
)

func onlineScope(ctx context.Context, p *auth.Principal) (string, *uuid.UUID) {
	if _, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsRead); err == nil {
		return onlineAll, nil
	}
	if p.Kind == auth.KindUser {
		return onlineSelf, &p.UserID
	}
	return onlineNone, nil
}

// agentsReport answers one row per member: the owners, admins and agents (with a team
// filter, that team's leads and members), and anyone else with work on the filtered
// tickets in the range. Ticket figures follow the team and brand filter; online time is
// the member's whole time in the app. onlineFor nil reads everyone's online time; with
// scope self only onlineFor's row is answered.
func agentsReport(ctx context.Context, q db.Querier, wid uuid.UUID, fromS, toS string, rf reportFilter, scope string, onlineFor *uuid.UUID) (ReportAgents, error) {
	out := ReportAgents{Data: []ReportAgentRow{}, OnlineTime: scope}
	var rowsFor *uuid.UUID
	if scope == onlineSelf {
		rowsFor = onlineFor
	}
	rf, err := rf.validate()
	if err != nil {
		return out, err
	}
	from, to, _, err := reportPeriod(ctx, q, wid, fromS, toS)
	if err != nil {
		return out, err
	}
	live := `t.workspace_id = $1 AND t.deleted_at IS NULL AND NOT t.spam` + rf.sql(4)
	// Each part reads its tickets once, by the range, and joins activities and messages as
	// sets, so a year on a large workspace stays a few scans rather than a probe per ticket.
	rows, err := q.Query(ctx, `WITH
		-- Tickets created in the range: first assignees and first response.
		c AS MATERIALIZED (
			SELECT t.id, t.assignee_id, t.created_at, t.first_responded_at FROM tickets t
			WHERE `+live+` AND t.created_at >= $2 AND t.created_at < $3),
		-- A ticket's first assigned activity names in from whom it was created assigned to (null:
		-- nobody); a ticket without one still has that first assignee. Activities never predate
		-- their ticket, so the range start bounds them.
		first_asg AS (
			SELECT DISTINCT ON (a.ticket_id) a.ticket_id, a.data ->> 'from' AS uid
			FROM c JOIN activities a ON a.ticket_id = c.id
			WHERE a.workspace_id = $1 AND a.type = 'assigned' AND a.created_at >= $2
			ORDER BY a.ticket_id, a.created_at, a.id),
		-- Assigned: an assigned activity to them in the range, or a ticket created in the range
		-- with them as first assignee.
		asg AS (
			SELECT x.uid, count(*) AS n FROM (
				SELECT a.data ->> 'to' AS uid, a.ticket_id
				FROM activities a JOIN tickets t ON t.id = a.ticket_id
				WHERE a.workspace_id = $1 AND a.type = 'assigned' AND a.created_at >= $2 AND a.created_at < $3 AND `+live+`
				UNION
				SELECT CASE WHEN f.ticket_id IS NULL THEN c.assignee_id::text ELSE f.uid END, c.id
				FROM c LEFT JOIN first_asg f ON f.ticket_id = c.id
			) x WHERE x.uid IS NOT NULL GROUP BY x.uid),
		-- First response, as in the overview's by_agent.
		frt AS (
			SELECT c.assignee_id AS uid, percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM c.first_responded_at - c.created_at) / 60) AS p50
			FROM c WHERE c.assignee_id IS NOT NULL AND c.first_responded_at IS NOT NULL
			GROUP BY c.assignee_id),
		-- Solved: tickets solved in the range and assigned to them.
		s AS MATERIALIZED (
			SELECT t.id, t.assignee_id AS uid, t.created_at, t.solved_at FROM tickets t
			WHERE `+live+` AND t.assignee_id IS NOT NULL AND t.solved_at >= $2 AND t.solved_at < $3),
		-- Their last assignment to the assignee before the solve.
		last_asg AS (
			SELECT a.ticket_id, max(a.created_at) AS at
			FROM s JOIN activities a ON a.ticket_id = s.id AND a.data ->> 'to' = s.uid::text AND a.created_at <= s.solved_at
			WHERE a.workspace_id = $1 AND a.type = 'assigned' AND a.created_at >= (SELECT min(s2.created_at) FROM s s2)
			GROUP BY a.ticket_id),
		-- The assignee's public replies on each since it was created.
		solved_replies AS (
			SELECT s.id, count(*) AS n
			FROM s JOIN messages m ON m.ticket_id = s.id AND m.author_user_id = s.uid AND m.created_at >= s.created_at
			WHERE m.workspace_id = $1 AND m.author_type = 'user' AND m.author_user_id IS NOT NULL AND m.deleted_at IS NULL
				AND m.kind = 'reply' AND m.direction = 'outbound' AND m.created_at >= (SELECT min(s2.created_at) FROM s s2)
			GROUP BY s.id),
		-- Handle time: from the last assignment to them (else the ticket's creation) until solved.
		sol AS (
			SELECT s.uid, count(*) AS n, sum(r.n)::bigint AS replies,
				percentile_cont(0.5) WITHIN GROUP (ORDER BY GREATEST(extract(epoch FROM s.solved_at - COALESCE(h.at, s.created_at)), 0) / 60) AS handle
			FROM s LEFT JOIN last_asg h ON h.ticket_id = s.id LEFT JOIN solved_replies r ON r.id = s.id
			GROUP BY s.uid),
		-- Replies sent and internal notes written in the range; third-party thread messages are neither.
		msg AS (
			SELECT m.author_user_id AS uid,
				count(*) FILTER (WHERE m.kind = 'reply' AND m.direction = 'outbound') AS replies,
				count(*) FILTER (WHERE m.kind = 'note' AND m.thread_id IS NULL) AS notes
			FROM messages m JOIN tickets t ON t.id = m.ticket_id
			WHERE m.workspace_id = $1 AND m.author_type = 'user' AND m.author_user_id IS NOT NULL AND m.deleted_at IS NULL
				AND m.created_at >= $2 AND m.created_at < $3 AND `+live+`
			GROUP BY m.author_user_id),
		-- Online: the part of each stretch inside the range.
		onl AS (
			SELECT p.user_id AS uid, (sum(extract(epoch FROM LEAST(p.ended_at, $3) - GREATEST(p.started_at, $2))) / 60)::float8 AS minutes
			FROM agent_presence p
			WHERE p.workspace_id = $1 AND p.ended_at > $2 AND p.started_at < $3 AND ($6::uuid IS NULL OR p.user_id = $6)
			GROUP BY p.user_id)
		SELECT u.id, u.name, u.email, u.avatar_url, COALESCE(asg.n, 0), COALESCE(sol.n, 0), COALESCE(msg.replies, 0), COALESCE(msg.notes, 0),
			sol.replies, frt.p50, sol.handle, COALESCE(onl.minutes, 0)
		FROM members m JOIN users u ON u.id = m.user_id
		LEFT JOIN asg ON asg.uid = m.user_id::text
		LEFT JOIN sol ON sol.uid = m.user_id
		LEFT JOIN frt ON frt.uid = m.user_id
		LEFT JOIN msg ON msg.uid = m.user_id
		LEFT JOIN onl ON onl.uid = m.user_id
		WHERE m.workspace_id = $1 AND (asg.uid IS NOT NULL OR sol.uid IS NOT NULL OR frt.uid IS NOT NULL OR msg.uid IS NOT NULL
			OR (m.role IN ('owner', 'admin', 'agent') AND ($4 = '' OR EXISTS (
				SELECT 1 FROM team_members tm JOIN teams tt ON tt.id = tm.team_id
				WHERE tt.workspace_id = $1 AND tt.id::text = $4 AND tm.user_id = m.user_id AND tm.role <> 'observer'))))
			AND ($7::uuid IS NULL OR m.user_id = $7)
		ORDER BY 6 DESC, 7 DESC, u.name, u.id LIMIT 500`, wid, from, to, rf.Team, rf.Brand, onlineFor, rowsFor)
	if err != nil {
		return out, err
	}
	out.Data, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (ReportAgentRow, error) {
		var a ReportAgentRow
		var repliesOnSolved *int64
		var frt, handle *float64
		var online float64
		err := r.Scan(&a.User.ID, &a.User.Name, &a.User.Email, &a.User.AvatarURL, &a.Assigned, &a.Solved, &a.Replies, &a.Notes,
			&repliesOnSolved, &frt, &handle, &online)
		if repliesOnSolved != nil && a.Solved > 0 {
			v := float64(*repliesOnSolved) / float64(a.Solved)
			a.RepliesPerSolved = round1(&v)
		}
		a.FirstResponseP50Minutes, a.HandleP50Minutes = round1(frt), round1(handle)
		if scope == onlineAll || (scope == onlineSelf && onlineFor != nil && a.User.ID == *onlineFor) {
			v := math.Max(online, 0)
			a.OnlineMinutes = round1(&v)
		}
		return a, err
	})
	if out.Data == nil {
		out.Data = []ReportAgentRow{}
	}
	return out, err
}

func registerAgentsReport(d *deps.Deps, api huma.API) {
	huma.Register(api, huma.Operation{OperationID: "report-agents", Method: http.MethodGet, Path: "/v1/reports/agents",
		Summary: "Agents report", Tags: []string{"Reports"},
		Description: "One row per member for the range: tickets assigned and solved, replies and notes, replies per solved ticket, " +
			"median first response and handle time, and online time. from, to, team_id and brand_id as in /v1/reports/overview; " +
			"online time is not narrowed by team or brand. Admins (and API keys with settings:read) see every member's row with online time, " +
			"other members only their own row, other API keys every row without online time."},
		func(ctx context.Context, in *struct {
			From    string `query:"from"`
			To      string `query:"to"`
			TeamID  string `query:"team_id" doc:"A team id, or none for tickets without a team"`
			BrandID string `query:"brand_id"`
		}) (*struct{ Body ReportAgents }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeTicketsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			scope, onlineFor := onlineScope(ctx, p)
			if scope == onlineNone {
				// Read nobody's: the zero id has no stretches.
				nobody := uuid.Nil
				onlineFor = &nobody
			}
			res, err := agentsReport(ctx, d.DB.Pool, p.WorkspaceID, in.From, in.To, reportFilter{Team: in.TeamID, Brand: in.BrandID}, scope, onlineFor)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body ReportAgents }{res}, nil
		})
}
