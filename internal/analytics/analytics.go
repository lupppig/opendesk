// Package analytics sends product events to PostHog. It is a no-op without POSTHOG_KEY,
// so self-hosted instances report nothing unless the operator opts in. Event names,
// identifiers and properties are constrained at this shared boundary.
package analytics

import (
	"log/slog"
	"math"
	"reflect"
	"strings"

	"github.com/google/uuid"
	"github.com/posthog/posthog-go"
)

type sink interface {
	Enqueue(posthog.Message) error
	Close() error
}

type Client struct {
	ph  sink
	log *slog.Logger
}

func New(key, host string, log *slog.Logger) *Client {
	c := &Client{log: log}
	if key == "" {
		return c
	}
	ph, err := posthog.NewWithConfig(key, posthog.Config{Endpoint: host})
	if err != nil {
		if log != nil {
			log.Warn("posthog disabled", "err", err)
		}
		return c
	}
	c.ph = ph
	return c
}

func (c *Client) Enabled() bool { return c != nil && c.ph != nil }

// Capture records a schema-approved event for a user, grouped by workspace.
// Unknown events, identifiers, properties and property values are not forwarded.
func (c *Client) Capture(distinctID, workspaceID, event string, props map[string]any) {
	if !c.Enabled() {
		return
	}
	distinctID, ok := analyticsID(distinctID)
	if !ok {
		c.dropped(event, "distinct_id")
		return
	}
	if workspaceID != "" {
		workspaceID, ok = canonicalUUID(workspaceID)
		if !ok {
			c.dropped(event, "workspace_id")
			return
		}
	}
	properties, refused, ok := safeProperties(event, props)
	if !ok {
		c.dropped(event, "")
		return
	}
	for _, name := range refused {
		c.dropped(event, name)
	}
	msg := posthog.Capture{DistinctId: distinctID, Event: event, Properties: properties}
	if workspaceID != "" {
		msg.Groups = posthog.NewGroups().Set("workspace", workspaceID)
	}
	if err := c.ph.Enqueue(msg); err != nil && c.log != nil {
		c.log.Debug("posthog enqueue", "err", err)
	}
}

// dropped logs what the schema refused, by name only, so a new event or property
// that was never added to eventSchemas shows up instead of vanishing.
func (c *Client) dropped(event, field string) {
	if c.log != nil {
		c.log.Warn("analytics: not sent", "event", event, "field", field)
	}
}

// Exception records a generic exception occurrence without accepting error text.
func (c *Client) Exception(distinctID string) {
	c.Capture(distinctID, "", "$exception", map[string]any{"$exception_type": "server_error"})
}

func (c *Client) Close() {
	if c.Enabled() {
		_ = c.ph.Close()
	}
}

type propertyValidator func(any) (any, bool)

var eventSchemas = map[string]map[string]propertyValidator{
	"user_signed_up": {
		"method":  enumProperty("password", "google", "microsoft", "github", "oidc"),
		"invited": boolProperty,
	},
	"user_logged_in": {
		"method": enumProperty("password", "google", "microsoft", "github", "oidc", "passkey"),
		"mfa":    boolProperty,
	},
	"workspace_created": {},
	"member_invited": {
		"role": enumProperty("admin", "agent", "viewer"),
	},
	"api_key_created": {
		"scope_count": nonNegativeCountProperty,
		"expires":     boolProperty,
	},
	"ai_client_connected": {
		"scope_count": nonNegativeCountProperty,
	},
	"webhook_created": {
		"all_events":  boolProperty,
		"event_count": nonNegativeCountProperty,
	},
	"plan_limit_hit": {
		"resource": enumProperty("mailboxes", "members"),
	},
	"ai_feature_used": {
		"feature": enumProperty("answer", "answer_check", "answer_email", "assistant", "copilot", "decide",
			"draft_reply", "kb_draft", "mail_categories", "mail_categorize", "playground", "rewrite", "summarize", "translate", "triage"),
		"own_key": boolProperty,
	},
	"ai_top_up_purchased": {
		"usd": amountProperty,
	},
	"ai_recharged": {
		"usd": amountProperty,
	},
	"subscription_plan_changed": {
		"from":     enumProperty("pro", "collab"),
		"to":       enumProperty("pro", "collab"),
		"interval": enumProperty("month", "year"),
		"seats":    nonNegativeCountProperty,
	},
	"subscription_started": {
		"plan":     enumProperty("pro", "collab"),
		"interval": enumProperty("month", "year"),
		"seats":    nonNegativeCountProperty,
	},
	"subscription_canceled": {
		"status":   enumProperty("trialing", "active", "past_due", "unpaid", "canceled", "incomplete", "incomplete_expired", "paused"),
		"interval": enumProperty("month", "year"),
		"seats":    nonNegativeCountProperty,
	},
	"$exception": {
		"$exception_type": enumProperty("server_error"),
	},
}

// safeProperties retains only known, flat values for known events, and names the
// properties it refused.
func safeProperties(event string, props map[string]any) (posthog.Properties, []string, bool) {
	schema, ok := eventSchemas[event]
	if !ok {
		return nil, nil, false
	}
	out := posthog.NewProperties()
	var refused []string
	for name, value := range props {
		validate, ok := schema[name]
		if !ok {
			refused = append(refused, name)
			continue
		}
		if safe, ok := validate(value); ok {
			out.Set(name, safe)
		} else {
			refused = append(refused, name)
		}
	}
	return out, refused, true
}

func analyticsID(id string) (string, bool) {
	prefix := ""
	if strings.HasPrefix(id, "workspace:") {
		prefix = "workspace:"
		id = strings.TrimPrefix(id, prefix)
	}
	id, ok := canonicalUUID(id)
	if !ok {
		return "", false
	}
	return prefix + id, true
}

func canonicalUUID(id string) (string, bool) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return "", false
	}
	return parsed.String(), true
}

func enumProperty(allowed ...string) propertyValidator {
	values := make(map[string]struct{}, len(allowed))
	for _, value := range allowed {
		values[value] = struct{}{}
	}
	return func(value any) (any, bool) {
		// Named string types (auth.Role) are strings too.
		v := reflect.ValueOf(value)
		if !v.IsValid() || v.Kind() != reflect.String {
			return nil, false
		}
		text := v.String()
		_, ok := values[text]
		return text, ok
	}
}

func boolProperty(value any) (any, bool) {
	boolean, ok := value.(bool)
	return boolean, ok
}

func nonNegativeCountProperty(value any) (any, bool) {
	count, ok := value.(int)
	return count, ok && count >= 0
}

func amountProperty(value any) (any, bool) {
	amount, ok := value.(float64)
	if !ok || math.IsNaN(amount) || math.IsInf(amount, 0) || amount < 0 {
		return nil, false
	}
	return amount, true
}
