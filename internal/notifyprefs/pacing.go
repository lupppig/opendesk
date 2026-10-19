package notifyprefs

import (
	"slices"
	"sync/atomic"
	"time"

	"github.com/useopendesk/opendesk/internal/config"
)

// Pacing (contracts/api.md "Notification preferences and emails"): notification emails to
// one person in one workspace form a conversation. The first email after a quiet spell
// waits Delay for the notification to be read. Each email starts a cooldown that grows
// while activity continues (Cooldowns, the last step repeating) and starts over after
// QuietReset with nothing new; whatever arrives meanwhile waits for one email at the end
// of it. Urgent types may cut a cooldown short, UrgentGap after the last email at the
// soonest and UrgentPerDay times a day at most. PerHour and PerDay cap every notification
// email to the person, digests included; past them everything rolls into the next allowed
// email. Nothing is emailed while the person is active in the app there.

// Pacing is the instance's pacing (config.NotifyEmail).
type Pacing config.NotifyEmail

var current atomic.Pointer[Pacing]

// Configure sets the instance's pacing at boot.
func Configure(n config.NotifyEmail) {
	if n.Validate() != nil {
		n = config.NotifyEmailDefaults(false)
	}
	p := Pacing(n)
	current.Store(&p)
}

// Current is the configured pacing, or the relaxed defaults.
func Current() Pacing {
	if p := current.Load(); p != nil {
		return *p
	}
	return Pacing(config.NotifyEmailDefaults(false))
}

// urgentTypes may be emailed before a cooldown ends: someone or something is waiting on
// the person now. Billing problems stop Ollie for customers, and are rare by construction
// (once a month, or once an hour for a failing key), so they are urgent too; the balance
// running low and the widget's monthly cap are expected and wait.
var urgentTypes = []string{"mentioned", "collab_mention", "sla_breached", "ai_handoff", "ai_balance_out", "auto_recharge_failed", "ai_key_failed"}

// Urgent reports whether typ may cut a cooldown short.
func Urgent(typ string) bool { return slices.Contains(urgentTypes, typ) }

// Waiting is one notification waiting for email.
type Waiting struct {
	Created time.Time
	// Due is when it may be emailed at the earliest: Created+Delay, or the digest time.
	Due    time.Time
	Urgent bool
}

// History is what pacing knows about the conversation and the person's recent emails.
type History struct {
	LastEmailed *time.Time
	// Step is the cooldown step of the last email.
	Step        int
	NextAllowed *time.Time
	LastUrgent  *time.Time
	// Sent are the person's notification emails (all workspaces) in the last 24 hours, oldest
	// first; UrgentSent those of them sent early for an urgent notification.
	Sent       []time.Time
	UrgentSent int
	// ActiveAt is when the person was last active in the app in this workspace.
	ActiveAt *time.Time
}

// Decision is when the next email may go and how it counts.
type Decision struct {
	At time.Time
	// Urgent: it goes early, before the cooldown ends, for an urgent notification.
	Urgent bool
	// Step is the cooldown step this email starts.
	Step int
	// Why names the last rule that pushed At later, for logs and tests:
	// due, cooldown, urgent, hour_cap, day_cap or active.
	Why string
}

// Decide says when the waiting notifications may be emailed, for a cadence (instant, hourly
// or daily). waiting must not be empty.
func (p Pacing) Decide(now time.Time, cadence string, waiting []Waiting, h History) Decision {
	d := Decision{Why: "due"}
	due, urgentDue := waiting[0].Due, time.Time{}
	oldest := waiting[0].Created
	for _, w := range waiting {
		if w.Due.Before(due) {
			due = w.Due
		}
		if w.Created.Before(oldest) {
			oldest = w.Created
		}
		if w.Urgent && (urgentDue.IsZero() || w.Due.Before(urgentDue)) {
			urgentDue = w.Due
		}
	}
	d.At = due
	quiet := h.LastEmailed == nil || oldest.Sub(*h.LastEmailed) >= p.QuietReset
	if cadence == CadenceInstant && !quiet {
		d.Step = min(h.Step+1, len(p.Cooldowns)-1)
		if h.NextAllowed != nil && h.NextAllowed.After(d.At) {
			d.At, d.Why = *h.NextAllowed, "cooldown"
		}
		if !urgentDue.IsZero() && h.UrgentSent < p.UrgentPerDay && d.Why == "cooldown" {
			early := later(urgentDue, h.LastEmailed.Add(p.UrgentGap))
			if h.LastUrgent != nil {
				early = later(early, h.LastUrgent.Add(p.UrgentGap))
			}
			if early.Before(d.At) {
				d.At, d.Urgent, d.Why = early, true, "urgent"
			}
		}
	}
	if n := len(h.Sent); n > 0 {
		inHour := 0
		for _, t := range h.Sent {
			if now.Sub(t) < time.Hour {
				inHour++
			}
		}
		if inHour >= p.PerHour {
			// The email that leaves the window frees a place.
			if t := h.Sent[n-p.PerHour].Add(time.Hour); t.After(d.At) {
				d.At, d.Why = t, "hour_cap"
			}
		}
		if n >= p.PerDay {
			if t := h.Sent[n-p.PerDay].Add(24 * time.Hour); t.After(d.At) {
				d.At, d.Why = t, "day_cap"
			}
		}
	}
	if h.ActiveAt != nil && p.ActiveWindow > 0 {
		if t := h.ActiveAt.Add(p.ActiveWindow); t.After(d.At) && t.After(now) {
			d.At, d.Why = t, "active"
		}
	}
	if d.Why != "urgent" {
		d.Urgent = false
	}
	return d
}

// Cooldown is the wait a step starts.
func (p Pacing) Cooldown(step int) time.Duration {
	return p.Cooldowns[min(max(step, 0), len(p.Cooldowns)-1)]
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
