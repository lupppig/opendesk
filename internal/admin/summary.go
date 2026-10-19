package admin

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/plan"
)

type AdminSummaryCounts struct {
	Total    int `json:"total"`
	New7d    int `json:"new_7d"`
	New30d   int `json:"new_30d"`
	Active7d int `json:"active_7d"`
}

type AdminSummaryWorkspaces struct {
	AdminSummaryCounts
	Suspended int `json:"suspended"`
}

type AdminSummaryBilling struct {
	Enabled        bool `json:"enabled"`
	PaidWorkspaces int  `json:"paid_workspaces"`
	BilledSeats    int  `json:"billed_seats"`
	PastDue        int  `json:"past_due"`
}

type AdminSummaryAI struct {
	MonthChargedUSD      float64 `json:"month_charged_usd"`
	MonthProviderCostUSD float64 `json:"month_provider_cost_usd"`
	MonthCalls           int     `json:"month_calls"`
}

type AdminSummaryDay struct {
	Date         string  `json:"date"`
	Signups      int     `json:"signups"`
	Workspaces   int     `json:"workspaces"`
	AIChargedUSD float64 `json:"ai_charged_usd"`
}

type AdminSummary struct {
	Users      AdminSummaryCounts     `json:"users"`
	Workspaces AdminSummaryWorkspaces `json:"workspaces"`
	Billing    AdminSummaryBilling    `json:"billing"`
	AI         AdminSummaryAI         `json:"ai"`
	Daily      []AdminSummaryDay      `json:"daily"`
}

func (s *svc) registerSummary(api huma.API) {
	op(s, api, huma.Operation{OperationID: "admin-summary", Method: http.MethodGet, Path: "/v1/admin/summary", Summary: "Instance overview"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body AdminSummary }, error) {
			if _, err := require(ctx, PermRead); err != nil {
				return nil, err
			}
			sum, err := s.summary(ctx)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body AdminSummary }{sum}, nil
		})
}

func (s *svc) summary(ctx context.Context) (AdminSummary, error) {
	q := s.d.DB.Pool
	var out AdminSummary
	u, w := &out.Users, &out.Workspaces
	if err := q.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE created_at > now() - interval '7 days'),
		count(*) FILTER (WHERE created_at > now() - interval '30 days'), count(*) FILTER (WHERE last_seen_at > now() - interval '7 days')
		FROM users WHERE NOT test_account`).Scan(&u.Total, &u.New7d, &u.New30d, &u.Active7d); err != nil {
		return out, err
	}
	if err := q.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE created_at > now() - interval '7 days'),
		count(*) FILTER (WHERE created_at > now() - interval '30 days'), count(*) FILTER (WHERE suspended_at IS NOT NULL),
		(SELECT count(DISTINCT e.workspace_id) FROM events e JOIN workspaces t ON t.id = e.workspace_id AND NOT t.test
			WHERE e.created_at > now() - interval '7 days')
		FROM workspaces WHERE NOT test`).Scan(&w.Total, &w.New7d, &w.New30d, &w.Suspended, &w.Active7d); err != nil {
		return out, err
	}
	out.Billing.Enabled = s.d.Cfg.BillingEnabled()
	if out.Billing.Enabled {
		if err := q.QueryRow(ctx, `SELECT count(*) FILTER (WHERE plan IN ('pro', 'collab') AND status IN ('active', 'trialing', 'past_due')),
			COALESCE(sum(seats) FILTER (WHERE plan IN ('pro', 'collab') AND status IN ('active', 'trialing', 'past_due')), 0)::bigint,
			count(*) FILTER (WHERE plan IN ('pro', 'collab') AND status = 'past_due') FROM subscriptions`).
			Scan(&out.Billing.PaidWorkspaces, &out.Billing.BilledSeats, &out.Billing.PastDue); err != nil {
			return out, err
		}
	}
	now := time.Now().UTC()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	var charged plan.Micro
	if err := q.QueryRow(ctx, `SELECT COALESCE(sum(micro_usd), 0)::bigint, COALESCE(sum(cost_usd), 0)::float8, count(*) FROM ai_usage WHERE created_at >= $1`, month).
		Scan(&charged, &out.AI.MonthProviderCostUSD, &out.AI.MonthCalls); err != nil {
		return out, err
	}
	out.AI.MonthChargedUSD, out.AI.MonthProviderCostUSD = usd(charged), round6(out.AI.MonthProviderCostUSD)
	rows, err := q.Query(ctx, `WITH days AS (
			SELECT d::date AS day, d AT TIME ZONE 'UTC' AS start FROM generate_series(date_trunc('day', now() AT TIME ZONE 'UTC') - interval '29 days',
				date_trunc('day', now() AT TIME ZONE 'UTC'), interval '1 day') AS d)
		SELECT to_char(days.day, 'YYYY-MM-DD'),
			(SELECT count(*) FROM users WHERE NOT test_account AND created_at >= days.start AND created_at < days.start + interval '1 day'),
			(SELECT count(*) FROM workspaces WHERE NOT test AND created_at >= days.start AND created_at < days.start + interval '1 day'),
			(SELECT COALESCE(sum(micro_usd), 0)::bigint FROM ai_usage WHERE created_at >= days.start AND created_at < days.start + interval '1 day')
		FROM days ORDER BY days.day`)
	if err != nil {
		return out, err
	}
	out.Daily, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (AdminSummaryDay, error) {
		var d AdminSummaryDay
		var m plan.Micro
		err := r.Scan(&d.Date, &d.Signups, &d.Workspaces, &m)
		d.AIChargedUSD = usd(m)
		return d, err
	})
	return out, err
}
