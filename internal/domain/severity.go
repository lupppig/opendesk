package domain

import (
	"context"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/db"
)

// SeverityOnSQL is whether the workspace uses ticket severity (settings severity_enabled,
// default true; contracts/api.md "Workspaces"). It expects alias w (workspaces).
const SeverityOnSQL = `COALESCE((w.settings ->> 'severity_enabled')::boolean, true)`

// SeverityOn reports whether the workspace uses ticket severity. With it off, the app
// leaves severity out and AI never sets or proposes one; the API still reads and writes
// the field, and tickets keep the severity they have.
func SeverityOn(ctx context.Context, q db.Querier, wid uuid.UUID) (bool, error) {
	var on bool
	err := q.QueryRow(ctx, `SELECT `+SeverityOnSQL+` FROM workspaces w WHERE w.id = $1`, wid).Scan(&on)
	return on, err
}

// WithoutSeverity is a copy of a tool's JSON schema for a workspace that does not use
// severity: no property named severity at any depth, no set_severity in an enum, and
// severity out of the descriptions' lists of fields.
func WithoutSeverity(schema map[string]any) map[string]any {
	out, _ := withoutSeverity(schema).(map[string]any)
	return out
}

func withoutSeverity(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, val := range x {
			if k == "properties" {
				if props, ok := val.(map[string]any); ok {
					kept := make(map[string]any, len(props))
					for name, p := range props {
						if name != "severity" {
							kept[name] = withoutSeverity(p)
						}
					}
					m[k] = kept
					continue
				}
			}
			if d, ok := val.(string); ok && k == "description" {
				m[k] = WithoutSeverityWords(d)
				continue
			}
			m[k] = withoutSeverity(val)
		}
		return m
	case []string:
		return slices.DeleteFunc(slices.Clone(x), func(s string) bool { return s == "severity" || s == "set_severity" })
	case []any:
		out := make([]any, 0, len(x))
		for _, e := range x {
			out = append(out, withoutSeverity(e))
		}
		return out
	}
	return v
}

// WithoutSeverityWords takes severity out of a tool description's lists of fields
// ("status, priority, severity, assignee" and "priority or severity").
func WithoutSeverityWords(s string) string {
	return strings.NewReplacer("severity, ", "", " or severity", "").Replace(s)
}
