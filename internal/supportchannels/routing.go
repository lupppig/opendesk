package supportchannels

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/helpdesk/tickets"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// Routing (contracts/support-channels.md "Routing").
const (
	// modeRoute: the most specific matching route on a connection posts the ticket.
	modeRoute = "route"
	// modeCopy: every matching copy posts the ticket as well, such as an escalation channel.
	modeCopy = "copy"

	maxDestinationConditions = 10
	// timeField is the condition on business hours at the moment of the event.
	timeField = "time"
)

const (
	mirrorCustomerReplies = "customer_replies"
	mirrorAgentReplies    = "agent_replies"
	mirrorStatus          = "status_changes"
	mirrorAssignee        = "assignee_changes"
	mirrorPriority        = "priority_changes"
)

var (
	mirrorOptions = []string{mirrorCustomerReplies, mirrorAgentReplies, mirrorStatus, mirrorAssignee, mirrorPriority}
	defaultMirror = []string{mirrorCustomerReplies, mirrorAgentReplies}
	// destinationFields are the ticket facts a destination's conditions read, besides
	// field.<key>, contact.field.<key> and time. Team and priority are its own columns.
	destinationFields = []string{"subject", "from_email", "received_at", "mailbox_id", "brand_id", "severity", "sentiment", "tag", timeField}
)

// RoutingInput chooses where a destination's threads start and what they show.
type RoutingInput struct {
	Mode       *string         `json:"mode,omitempty" enum:"route,copy" doc:"route (default): the most specific matching route on the connection posts the ticket. copy: this channel posts every matching ticket as well, such as an escalation channel."`
	Conditions *[]hd.Condition `json:"conditions,omitempty" maxItems:"10" doc:"Every condition must hold for a ticket's thread to start here. Fields: subject, from_email, received_at, mailbox_id, brand_id, severity, sentiment, tag, field.<key>, contact.field.<key>, and time (equals or not_equals business_hours or outside_business_hours). Replaces the list."`
	Mirror     *[]string       `json:"mirror,omitempty" maxItems:"5" doc:"What the threads show besides the ticket card: customer_replies, agent_replies, status_changes, assignee_changes, priority_changes. Default customer_replies and agent_replies. Replaces the list."`
}

// apply validates the given values, sets them on d and reports the keys it changed.
func (in RoutingInput) apply(ctx context.Context, q db.Querier, wid uuid.UUID, d *destination) ([]string, error) {
	var changed []string
	if in.Mode != nil {
		if !oneOf(*in.Mode, modeRoute, modeCopy) {
			return nil, httpx.Validation("Mode must be route or copy.")
		}
		if *in.Mode != d.Mode {
			d.Mode = *in.Mode
			changed = append(changed, "mode")
		}
	}
	if in.Conditions != nil {
		cs, err := validDestinationConditions(ctx, q, wid, *in.Conditions)
		if err != nil {
			return nil, err
		}
		if !sameConditions(cs, d.Conditions) {
			d.Conditions = cs
			changed = append(changed, "conditions")
		}
	}
	if in.Mirror != nil {
		m, err := cleanMirror(*in.Mirror)
		if err != nil {
			return nil, err
		}
		if !slices.Equal(m, d.Mirror) {
			d.Mirror = m
			changed = append(changed, "mirror")
		}
	}
	return changed, nil
}

func validDestinationConditions(ctx context.Context, q db.Querier, wid uuid.UUID, cs []hd.Condition) ([]hd.Condition, error) {
	if len(cs) > maxDestinationConditions {
		return nil, httpx.Validation(fmt.Sprintf("A destination can have at most %d conditions.", maxDestinationConditions))
	}
	out := make([]hd.Condition, 0, len(cs))
	for _, c := range cs {
		c.Field, c.Op, c.Question = strings.TrimSpace(c.Field), strings.TrimSpace(c.Op), ""
		if _, _, custom := hd.ConditionFieldKey(c.Field); !custom && !slices.Contains(destinationFields, c.Field) {
			return nil, httpx.Validation("Condition field must be one of " + strings.Join(destinationFields, ", ") + ", field.<key> or contact.field.<key>.")
		}
		if c.Field == timeField {
			switch c.Op {
			case "eq":
				c.Op = "equals"
			case "ne":
				c.Op = "not_equals"
			}
			c.Value = strings.TrimSpace(c.Value)
			if !oneOf(c.Op, "equals", "not_equals") || !slices.Contains(hd.CallTimes, c.Value) {
				return nil, httpx.Validation("time conditions use equals or not_equals with business_hours or outside_business_hours.")
			}
			out = append(out, c)
			continue
		}
		v, err := hd.ValidateConditions(ctx, q, wid, []hd.Condition{c})
		if err != nil {
			return nil, err
		}
		out = append(out, v...)
	}
	return out, nil
}

func sameConditions(a, b []hd.Condition) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

// cleanMirror drops repeats and orders the list as mirrorOptions does.
func cleanMirror(in []string) ([]string, error) {
	out := []string{}
	for _, m := range mirrorOptions {
		if slices.Contains(in, m) {
			out = append(out, m)
		}
	}
	for _, m := range in {
		if !slices.Contains(mirrorOptions, m) {
			return nil, httpx.Validation("mirror takes only " + strings.Join(mirrorOptions, ", ") + ".")
		}
	}
	return out, nil
}

// sameRoute reports whether two routes on a connection would always tie.
func sameRoute(a, b destination) bool {
	return a.Mode == modeRoute && b.Mode == modeRoute && a.ConnectionID == b.ConnectionID &&
		uuidPtrEqual(a.TeamID, b.TeamID) && stringPtrEqual(a.Priority, b.Priority) && sameConditions(a.Conditions, b.Conditions)
}

// checkRouteUnique refuses a route that repeats another route's team, priority and
// conditions on the same connection, since only one of them could ever post.
func checkRouteUnique(ctx context.Context, tx pgx.Tx, wid uuid.UUID, d destination) error {
	if d.Mode != modeRoute {
		return nil
	}
	// Serialize route changes on the connection, so two saves can't both pass the check.
	if _, err := tx.Exec(ctx, `SELECT 1 FROM support_channel_connections WHERE workspace_id=$1 AND id=$2 FOR UPDATE`, wid, d.ConnectionID); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT `+destinationColumns+` FROM support_channel_destinations WHERE workspace_id=$1 AND connection_id=$2 AND id<>$3 AND mode='route'`, wid, d.ConnectionID, d.ID)
	if err != nil {
		return err
	}
	others, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (destination, error) {
		var o destination
		return o, r.Scan(destinationScan(&o)...)
	})
	if err != nil {
		return err
	}
	for _, o := range others {
		if sameRoute(d, o) {
			return httpx.Conflict("Another route on this connection already has that team, priority and conditions.")
		}
	}
	return nil
}

func uuidPtrEqual(a, b *uuid.UUID) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func stringPtrEqual(a, b *string) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

// routeCandidate is an enabled destination whose team admits the ticket.
type routeCandidate struct {
	deliveryTarget
	// existing is set when the destination already has the ticket's thread.
	existing bool
}

// eligibility decides which candidates may start a thread, reading the ticket's facts
// and business hours only when a condition needs them.
type eligibility struct {
	s     *Service
	wid   uuid.UUID
	t     domain.Ticket
	at    time.Time
	match func([]hd.Condition) bool
	open  *bool
}

// starts reports whether d may start the ticket's thread: its priority and every
// condition hold. An existing thread keeps following the ticket without them.
func (e *eligibility) starts(ctx context.Context, d destination) (bool, error) {
	if d.Priority != nil && *d.Priority != e.t.Priority {
		return false, nil
	}
	var facts []hd.Condition
	for _, c := range d.Conditions {
		if c.Field != timeField {
			facts = append(facts, c)
			continue
		}
		open, err := e.businessHours(ctx)
		if err != nil {
			return false, err
		}
		now := "outside_business_hours"
		if open {
			now = "business_hours"
		}
		if (c.Value == now) != (c.Op == "equals") {
			return false, nil
		}
	}
	if len(facts) == 0 {
		return true, nil
	}
	if e.match == nil {
		err := e.s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
			m, err := tickets.Matcher(ctx, tx, e.wid, e.t.ID)
			e.match = m
			return err
		})
		if err != nil {
			return false, err
		}
	}
	return e.match(facts), nil
}

// businessHours reports whether the ticket's team (or the workspace) was open when
// the event happened, so a retry decides as the first attempt did. Hours that are
// off read as open.
func (e *eligibility) businessHours(ctx context.Context) (bool, error) {
	if e.open != nil {
		return *e.open, nil
	}
	bh, loc, err := hd.HoursFor(ctx, e.s.d.DB.Pool, e.wid, e.t.TeamID)
	if err != nil {
		return false, err
	}
	open := !bh.Enabled || hd.OpenAt(bh, loc, e.at)
	e.open = &open
	return open, nil
}

// pickTargets returns every copy and, per connection, the one route that posts: a
// team route ahead of the thread the ticket already has, then a priority route, then
// the route with more conditions. A team route beats an existing thread so a move to a
// team with its own route posts there.
func pickTargets(eligible []routeCandidate) []deliveryTarget {
	var out []deliveryTarget
	best := map[uuid.UUID]routeCandidate{}
	var order []uuid.UUID
	for _, c := range eligible {
		if c.Mode == modeCopy {
			out = append(out, c.deliveryTarget)
			continue
		}
		cur, ok := best[c.ConnectionID]
		if !ok {
			order = append(order, c.ConnectionID)
		}
		if !ok || moreSpecific(c, cur) {
			best[c.ConnectionID] = c
		}
	}
	for _, id := range order {
		out = append(out, best[id].deliveryTarget)
	}
	return out
}

func moreSpecific(a, b routeCandidate) bool {
	rank := func(c routeCandidate) []int {
		return []int{boolInt(c.TeamID != nil), boolInt(c.existing), boolInt(c.Priority != nil), len(c.Conditions)}
	}
	if n := slices.Compare(rank(a), rank(b)); n != 0 {
		return n > 0
	}
	return bytes.Compare(a.ID[:], b.ID[:]) < 0
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// shown is which of a ticket's messages a destination's threads show.
type shown struct{ notes, customer, agent bool }

func (d destination) shows() shown {
	return shown{notes: d.IncludeNotes, customer: slices.Contains(d.Mirror, mirrorCustomerReplies), agent: slices.Contains(d.Mirror, mirrorAgentReplies)}
}

func (s shown) message(m *domain.Message) bool {
	switch {
	case m.Kind == "note":
		return s.notes
	case m.Direction == "inbound":
		return s.customer
	}
	return s.agent
}

type ticketUpdatedEvent struct {
	Ticket  domain.Ticket `json:"ticket"`
	Changes []string      `json:"changes"`
}

// activityText is the line a ticket.updated event posts in a thread whose mirror
// asks for its changes, read from the event's own ticket so a late delivery says
// what changed then. Empty when there is nothing to post.
func activityText(data []byte, mirror []string) (string, error) {
	var ev ticketUpdatedEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		return "", fmt.Errorf("decode ticket event: %w", err)
	}
	var parts []string
	if slices.Contains(mirror, mirrorStatus) && slices.Contains(ev.Changes, "status") {
		parts = append(parts, "status is "+label(ev.Ticket.Status))
	}
	if slices.Contains(mirror, mirrorPriority) && slices.Contains(ev.Changes, "priority") {
		parts = append(parts, "priority is "+label(ev.Ticket.Priority))
	}
	if slices.Contains(mirror, mirrorAssignee) && slices.Contains(ev.Changes, "assignee_id") {
		if ev.Ticket.Assignee != nil {
			parts = append(parts, "assigned to "+clip(ev.Ticket.Assignee.Name, 100))
		} else {
			parts = append(parts, "unassigned")
		}
	}
	if len(parts) == 0 {
		return "", nil
	}
	return "Ticket updated: " + strings.Join(parts, ", ") + ".", nil
}

// label turns an enum value such as on_hold into "on hold".
func label(v string) string {
	return strings.ReplaceAll(v, "_", " ")
}
