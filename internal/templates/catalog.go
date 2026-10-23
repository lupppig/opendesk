package templates

import (
	"slices"
	"strings"
)

// Variable is one entry of the variable list (contracts/templates.md "Variables").
type Variable struct {
	Key         string `json:"key"`
	Group       string `json:"group" enum:"contact,company,ticket,agent,team,brand,mailbox,workspace,date"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Example     string `json:"example"`
	HTML        bool   `json:"html"`
	// List marks a list: loop over it with for, or print it joined with ", ".
	List bool `json:"list"`
}

// Slot is a place in a layout or CSAT email that opendesk fills.
type Slot struct {
	Key         string   `json:"key" enum:"content,signature,history,survey"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	RequiredIn  []string `json:"required_in"`
}

// Kinds of template, which decide the slots a template may use.
const (
	KindReply             = "reply"
	KindSignature         = "signature"
	KindPersonalSignature = "personal_signature"
	KindAutoReply         = "auto_reply"
	KindLayout            = "layout"
	KindCSAT              = "csat"
	KindSubject           = "subject"
	KindHistoryHeader     = "history_header"
	// KindCollabSignature is a Collab account's or address's signature: mail a member writes
	// from their own or a shared account, with no ticket, customer, team or brand.
	KindCollabSignature = "collab_signature"
)

// KindGroups are the variable groups of the kinds that take only some (every other kind
// takes every variable).
var KindGroups = map[string][]string{
	KindCollabSignature: {"agent", "mailbox", "workspace", "date"},
}

// Variables is the built-in list. Custom fields add contact.fields.<key>,
// company.fields.<key> and ticket.fields.<key>.
var Variables = []Variable{
	{"contact.name", "contact", "Name", "The customer's full name.", "Jane Cooper", false, false},
	{"contact.first_name", "contact", "First name", "The first word of the customer's name.", "Jane", false, false},
	{"contact.last_name", "contact", "Last name", "The rest of the customer's name.", "Cooper", false, false},
	{"contact.email", "contact", "Email", "The customer's email address.", "jane@example.com", false, false},
	{"contact.phone", "contact", "Phone", "The customer's phone number.", "+1 555 0100", false, false},
	{"contact.labels", "contact", "Labels", "The customer's labels, a list.", "VIP", false, true},
	{"company.name", "company", "Name", "The customer's company.", "Acme Inc", false, false},
	{"company.domain", "company", "Domain", "The company's web domain.", "acme.com", false, false},
	{"company.labels", "company", "Labels", "The company's labels, a list.", "Enterprise", false, true},
	{"ticket.number", "ticket", "Number", "The ticket number, without the #.", "1042", false, false},
	{"ticket.subject", "ticket", "Subject", "The ticket's subject.", "Can't sign in", false, false},
	{"ticket.status", "ticket", "Status", "Open, Pending, On hold, Solved or Closed.", "Open", false, false},
	{"ticket.priority", "ticket", "Priority", "Low, Normal, High or Urgent.", "Normal", false, false},
	{"ticket.channel", "ticket", "Channel", "Email, Chat, API or Form.", "Email", false, false},
	{"ticket.created_date", "ticket", "Created", "The day the ticket was opened.", "18 October 2026", false, false},
	{"ticket.labels", "ticket", "Labels", "The ticket's labels, a list.", "Billing, Refund", false, true},
	{"agent.name", "agent", "Name", "Who sends the reply. Empty for automations and auto-replies.", "Alex Morgan", false, false},
	{"agent.first_name", "agent", "First name", "The first word of the sender's name.", "Alex", false, false},
	{"agent.last_name", "agent", "Last name", "The rest of the sender's name.", "Morgan", false, false},
	{"agent.title", "agent", "Job title", "From the sender's profile.", "Support engineer", false, false},
	{"agent.phone", "agent", "Phone", "From the sender's profile.", "+1 555 0199", false, false},
	{"agent.pronouns", "agent", "Pronouns", "From the sender's profile.", "they/them", false, false},
	{"agent.avatar_url", "agent", "Picture URL", "The sender's profile picture, for an image in a signature.", "https://api.opendesk.sh/v1/images/…", false, false},
	{"agent.signature", "agent", "Personal signature", "The sender's own signature, placed inside a team or address signature.", "Alex Morgan, Support engineer", true, false},
	{"team.name", "team", "Name", "The ticket's team, or the brand when it has none.", "Billing", false, false},
	{"team.description", "team", "Description", "The team's description.", "Invoices, refunds and plans", false, false},
	{"brand.name", "brand", "Name", "The ticket's brand.", "Acme", false, false},
	{"brand.color", "brand", "Colour", "The brand colour, as #rrggbb.", "#2563EB", false, false},
	{"brand.logo_url", "brand", "Logo URL", "The brand's logo, for an image.", "https://api.opendesk.sh/v1/images/…", false, false},
	{"brand.help_center_url", "brand", "Help center URL", "The brand's help center, when it is on.", "https://help.acme.com", false, false},
	{"mailbox.name", "mailbox", "Name", "The sending address's name.", "Acme Support", false, false},
	{"mailbox.email", "mailbox", "Email", "The address the email goes out from.", "support@acme.com", false, false},
	{"workspace.name", "workspace", "Name", "Your workspace's name.", "Acme", false, false},
	{"date.today", "date", "Today", "Today's date in the workspace's timezone.", "18 October 2026", false, false},
}

// TagSpec describes a {% tag %} for the editor and the docs.
type TagSpec struct {
	Name        string `json:"name"`
	Syntax      string `json:"syntax"`
	Description string `json:"description"`
}

// Tags are the logic tags.
var Tags = []TagSpec{
	{"if", `{% if agent.phone %}…{% elsif agent.title %}…{% else %}…{% endif %}`, "Shows a part only when a condition holds. Compare with ==, !=, >, <, >=, <= and contains; combine with and, or and not; test for empty with == empty."},
	{"unless", `{% unless contact.name %}Hi there{% endunless %}`, "The opposite of if."},
	{"case", `{% case ticket.priority %}{% when "Urgent", "High" %}…{% else %}…{% endcase %}`, "Picks a part by value."},
	{"for", `{% for label in ticket.labels limit: 5 %}{{ label }}{% unless forloop.last %}, {% endunless %}{% else %}No labels{% endfor %}`, "Repeats a part for each item of a list or a range like (1..3), with forloop.index, forloop.first and forloop.last. else shows when the list is empty."},
	{"assign", `{% assign greeting = contact.first_name | default: "there" %}`, "Names a value to use later in the template."},
	{"capture", `{% capture who %}{{ agent.first_name }} from {{ team.name }}{% endcapture %}`, "Names a whole piece of the template."},
	{"comment", `{% comment %}Only we see this{% endcomment %}`, "A note that never reaches the customer."},
	{"raw", `{% raw %}{{ not filled }}{% endraw %}`, "Writes tags as they are, not filled."},
}

// Slots are the places opendesk fills in layouts and CSAT emails.
var Slots = []Slot{
	{"content", "Message", "The message itself.", []string{KindLayout}},
	{"signature", "Signature", "The team's, address's or sender's signature.", nil},
	{"history", "Earlier messages", "The quoted conversation, when the layout includes it.", nil},
	{"survey", "Rating buttons", "The five score buttons of the satisfaction survey.", []string{KindCSAT}},
}

// HistoryVariables are the variables of a quoted message's header.
var HistoryVariables = []Variable{
	{"message.author", "message", "Author", "Who wrote the quoted message.", "Jane Cooper", false, false},
	{"message.date", "message", "Date", "When it was written.", "Sat, 17 Oct 2026 at 14:03", false, false},
}

// groupOf is each built-in variable's group.
var groupOf = func() map[string]string {
	m := map[string]string{}
	for _, v := range Variables {
		m[v.Key] = v.Group
	}
	return m
}()

// takes reports whether a template of kind may read the variable key.
func takes(kind, key string) bool {
	groups, limited := KindGroups[kind]
	if !limited {
		return true
	}
	g, ok := groupOf[key]
	return ok && slices.Contains(groups, g)
}

var builtin = func() map[string]bool {
	m := map[string]bool{}
	for _, v := range Variables {
		m[v.Key] = true
	}
	return m
}()

// slotsFor are the slots a kind may use.
func slotsFor(kind string) []string {
	switch kind {
	case KindLayout:
		return []string{"content", "signature", "history"}
	case KindCSAT:
		return []string{"survey"}
	}
	return nil
}

// IsField reports whether key is a custom field variable (<entity>.fields.<key>).
func IsField(key string) (entity, field string, ok bool) {
	for _, e := range []string{"contact", "company", "ticket"} {
		if rest, found := strings.CutPrefix(key, e+".fields."); found && rest != "" && !strings.Contains(rest, ".") {
			return e, rest, true
		}
	}
	return "", "", false
}

// Known reports whether key is a variable any template may read: built-in or a
// custom field shape.
func Known(key string) bool {
	if builtin[key] {
		return true
	}
	_, _, ok := IsField(key)
	return ok
}

// Check reports what is wrong with src as a template of kind: syntax, unknown variables
// (fields lists the workspace's custom field variables; nil takes any), slots it may not
// use, and required slots it lacks.
func Check(src, kind string, fields map[string]bool) []Problem {
	t := Parse(src)
	out := append([]Problem{}, t.Problems()...)
	allowed := map[string]bool{}
	for _, s := range slotsFor(kind) {
		allowed[s] = true
	}
	seen := map[string]bool{}
	for _, k := range t.Keys() {
		if seen[k] {
			continue
		}
		seen[k] = true
		switch {
		case kind == KindHistoryHeader:
			if k != "message.author" && k != "message.date" {
				out = append(out, Problem{Message: "Use {{ message.author }} and {{ message.date }} here.", Token: "{{ " + k + " }}"})
			}
		case allowed[k]:
		case isSlot(k):
			out = append(out, Problem{Message: "{{ " + k + " }} belongs in " + slotHome(k) + ".", Token: "{{ " + k + " }}"})
		case k == "agent.signature" && kind == KindPersonalSignature:
			out = append(out, Problem{Message: "A personal signature cannot include itself.", Token: "{{ agent.signature }}"})
		case builtin[k] && takes(kind, k):
		case builtin[k], KindGroups[kind] != nil && isFieldKey(k):
			out = append(out, Problem{Message: "{{ " + k + " }} has nothing to read here: mail from an account has no ticket, customer, team or brand." +
				didYouMean(k, candidates(kind, nil)), Token: "{{ " + k + " }}"})
		default:
			if _, _, ok := IsField(k); ok && (fields == nil || fields[k]) {
				continue
			}
			out = append(out, Problem{Message: "There is no variable called " + k + "." + didYouMean(k, candidates(kind, fields)), Token: "{{ " + k + " }}"})
		}
	}
	for _, s := range Slots {
		for _, req := range s.RequiredIn {
			if req == kind && !t.Uses(s.Key) {
				out = append(out, Problem{Message: "This template needs {{ " + s.Key + " }}, where opendesk puts the " + strings.ToLower(s.Label) + ".", Token: ""})
			}
		}
	}
	return out
}

func isSlot(k string) bool {
	for _, s := range Slots {
		if s.Key == k {
			return true
		}
	}
	return false
}

func slotHome(k string) string {
	if k == "survey" {
		return "the satisfaction email"
	}
	return "an email layout"
}

// candidates are the names a template of kind may read, for "Did you mean".
func candidates(kind string, fields map[string]bool) []string {
	out := slotsFor(kind)
	for _, v := range Variables {
		if takes(kind, v.Key) {
			out = append(out, v.Key)
		}
	}
	if _, limited := KindGroups[kind]; limited {
		return out
	}
	for k := range fields {
		out = append(out, k)
	}
	return out
}

func isFieldKey(k string) bool {
	_, _, ok := IsField(k)
	return ok
}
