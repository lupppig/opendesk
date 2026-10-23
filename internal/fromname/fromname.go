// Package fromname fills From name formats: a From name written with variables that name
// who sends the mail, like "{agent} at {mailbox}" (contracts/teams.md "What the customer
// sees", contracts/collab.md "Addresses"). The helpdesk's addresses and Collab's share one
// vocabulary; each takes the variables that mean something where it sends from.
package fromname

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/useopendesk/opendesk/internal/httpx"
)

// MaxLen bounds a format, in characters.
const MaxLen = 80

// The variables, one vocabulary across the product.
const (
	Agent      = "agent"       // the sender's full name
	AgentFirst = "agent_first" // the first word of it
	Team       = "team"        // the ticket's team, else the brand (helpdesk)
	Brand      = "brand"       // the brand (helpdesk)
	Mailbox    = "mailbox"     // the address's name: a mailbox's display_name, a Collab account's name
	Workspace  = "workspace"   // the workspace's name (Collab)
)

// Helpdesk are the variables of a helpdesk mailbox's from_name_format; Collab those of a
// Collab account's or address's From name.
var (
	Helpdesk = []string{Agent, AgentFirst, Team, Brand, Mailbox}
	Collab   = []string{Agent, AgentFirst, Mailbox, Workspace}
)

var varRe = regexp.MustCompile(`\{([a-z_]+)\}`)

// Names are what a format is filled with.
type Names struct {
	Agent     string // the sender's full name, a bot's name, or "" without one
	Team      string // the ticket's team, else ""
	Brand     string
	Mailbox   string
	Workspace string
}

// AgentFirst is the first word of the sender's name.
func (n Names) AgentFirst() string {
	first, _, _ := strings.Cut(strings.TrimSpace(n.Agent), " ")
	return first
}

func (n Names) teamOrBrand() string {
	if n.Team != "" {
		return n.Team
	}
	return n.Brand
}

// HasVars reports whether s uses any of vars.
func HasVars(s string, vars []string) bool {
	for _, m := range varRe.FindAllStringSubmatch(s, -1) {
		if contains(vars, m[1]) {
			return true
		}
	}
	return false
}

// Clean validates a format for field: spaces collapsed, up to MaxLen characters, only vars,
// no markup or control characters. "" stays "" (the caller's default).
func Clean(field, s string, vars []string) (string, error) {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return "", nil
	}
	if utf8.RuneCountInString(s) > MaxLen {
		return "", httpx.Validation(field + " must be 80 characters or less")
	}
	if strings.ContainsAny(s, "<>") || strings.IndexFunc(s, unicode.IsControl) >= 0 {
		return "", httpx.Validation(field + " cannot contain < or > characters")
	}
	rest := varRe.ReplaceAllStringFunc(s, func(m string) string {
		if contains(vars, m[1:len(m)-1]) {
			return ""
		}
		return m
	})
	if strings.ContainsAny(rest, "{}") {
		return "", httpx.Validation(field + " takes only the variables " + list(vars))
	}
	return s, nil
}

// Fill fills format with n, reading only vars. Without a sender the agent variable and the
// words between it and the next variable are dropped; a format left with no variable, or
// filled to nothing, reads the mailbox. Separators left at either end are trimmed.
func Fill(format string, vars []string, n Names) string {
	type seg struct {
		text   string
		isVar  bool
		isUser bool
	}
	var segs []seg
	last := 0
	for _, loc := range varRe.FindAllStringSubmatchIndex(format, -1) {
		name := format[loc[2]:loc[3]]
		if !contains(vars, name) {
			continue
		}
		if loc[0] > last {
			segs = append(segs, seg{text: format[last:loc[0]]})
		}
		segs = append(segs, seg{text: name, isVar: true, isUser: name == Agent || name == AgentFirst})
		last = loc[1]
	}
	if last < len(format) {
		segs = append(segs, seg{text: format[last:]})
	}
	if strings.TrimSpace(n.Agent) == "" {
		kept := segs[:0:0]
		others := false
		skip := false
		for _, sg := range segs {
			switch {
			case sg.isUser:
				skip = true
				continue
			case sg.isVar:
				skip = false
				others = true
			case skip:
				continue
			}
			kept = append(kept, sg)
		}
		if !others {
			return n.Mailbox
		}
		segs = kept
	}
	var b strings.Builder
	for _, sg := range segs {
		if !sg.isVar {
			b.WriteString(sg.text)
			continue
		}
		switch sg.text {
		case Agent:
			b.WriteString(n.Agent)
		case AgentFirst:
			b.WriteString(n.AgentFirst())
		case Team:
			b.WriteString(n.teamOrBrand())
		case Brand:
			b.WriteString(n.Brand)
		case Mailbox:
			b.WriteString(n.Mailbox)
		case Workspace:
			b.WriteString(n.Workspace)
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	out = strings.TrimFunc(out, func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune("-–—|,:;·/", r) })
	if utf8.RuneCountInString(out) > 200 {
		out = string([]rune(out)[:200])
	}
	if out == "" {
		return n.Mailbox
	}
	return out
}

func contains(list []string, v string) bool {
	for _, k := range list {
		if k == v {
			return true
		}
	}
	return false
}

// list reads vars as "{a}, {b} and {c}".
func list(vars []string) string {
	out := make([]string, len(vars))
	for i, v := range vars {
		out[i] = "{" + v + "}"
	}
	if len(out) < 2 {
		return strings.Join(out, "")
	}
	return strings.Join(out[:len(out)-1], ", ") + " and " + out[len(out)-1]
}
