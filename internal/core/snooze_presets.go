package core

import (
	"regexp"
	"strconv"

	"github.com/useopendesk/opendesk/internal/httpx"
)

// SnoozePreset is one of the times a ticket's snooze menu offers (settings.snooze_presets).
// The app works the time out on the agent's own clock when the menu opens: "hours" is the
// top of the hour at least that many hours away, "days" that many days from today at time,
// "week" next Monday at time.
type SnoozePreset struct {
	Kind  string `json:"kind" enum:"hours,days,week" doc:"hours: in so many hours, on the hour; days: in so many days at time; week: next Monday at time"`
	Hours int    `json:"hours,omitempty" doc:"kind hours: 1 to 168"`
	Days  int    `json:"days,omitempty" doc:"kind days: 1 to 365"`
	Time  string `json:"time,omitempty" doc:"kind days and week: the time of day, HH:MM on the 24-hour clock"`
}

// MaxSnoozePresets is how many times the snooze menu may offer.
const MaxSnoozePresets = 8

var snoozeTimeRe = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// validSnoozePresets checks the snooze menu's times and returns them as stored. An empty
// list stores as empty, which the app reads as its defaults (in 1 hour, in 4 hours,
// tomorrow at 09:00, next week at 09:00).
func validSnoozePresets(in []SnoozePreset) ([]map[string]any, error) {
	if len(in) > MaxSnoozePresets {
		return nil, httpx.Validation("snooze_presets may have at most " + strconv.Itoa(MaxSnoozePresets) + " times.")
	}
	out := make([]map[string]any, 0, len(in))
	for _, p := range in {
		switch p.Kind {
		case "hours":
			if p.Hours < 1 || p.Hours > 168 || p.Days != 0 || p.Time != "" {
				return nil, httpx.Validation("a snooze_presets time of kind hours needs hours from 1 to 168, and no days or time.")
			}
			out = append(out, map[string]any{"kind": p.Kind, "hours": p.Hours})
		case "days":
			if p.Days < 1 || p.Days > 365 || p.Hours != 0 || !snoozeTimeRe.MatchString(p.Time) {
				return nil, httpx.Validation("a snooze_presets time of kind days needs days from 1 to 365 and a time like 09:00, and no hours.")
			}
			out = append(out, map[string]any{"kind": p.Kind, "days": p.Days, "time": p.Time})
		case "week":
			if p.Hours != 0 || p.Days != 0 || !snoozeTimeRe.MatchString(p.Time) {
				return nil, httpx.Validation("a snooze_presets time of kind week needs a time like 09:00, and no hours or days.")
			}
			out = append(out, map[string]any{"kind": p.Kind, "time": p.Time})
		default:
			return nil, httpx.Validation("snooze_presets kinds must be hours, days or week.")
		}
	}
	return out, nil
}
