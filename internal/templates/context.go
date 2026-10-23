package templates

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/config"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/domain"
)

// Ref names what a template is filled for. Without a ticket, the contact, company and
// ticket variables read sample values (Sample) or nothing.
type Ref struct {
	WorkspaceID uuid.UUID
	TicketID    *uuid.UUID
	// AuthorUserID is the person sending; BotName the AI agent's name when it sends.
	AuthorUserID *uuid.UUID
	BotName      string
	// MailboxID is the sending address (else the ticket's); MailboxEmail overrides its
	// address (the alias replies go out as). An alias of the address, named here or else the
	// one the ticket was written to, brings its display name and signature
	// (contracts/templates.md "Signatures").
	MailboxID    *uuid.UUID
	MailboxEmail string
	// BrandID and TeamID apply without a ticket, or override the ticket's.
	BrandID *uuid.UUID
	TeamID  *uuid.UUID
	// Sample fills the customer's and the ticket's variables with examples when there is
	// no ticket (previews).
	Sample bool
	Now    time.Time
}

// Context is a filled-in Ref: the values, plus what the email design needs.
type Context struct {
	Values *Values
	// Workspace, brand and ticket facts.
	BrandID    uuid.UUID
	TeamID     *uuid.UUID
	MailboxID  *uuid.UUID
	TicketID   *uuid.UUID
	Location   *time.Location
	BrandColor string // #rrggbb, never empty
	LogoURL    string
	BrandName  string
	// Signatures as stored (not yet filled).
	AliasSignature, TeamSignature, MailboxSignature, PersonalSignature string
}

// Load reads everything the variables need.
func Load(ctx context.Context, q db.Querier, cfg *config.Config, ref Ref) (*Context, error) {
	if ref.Now.IsZero() {
		ref.Now = time.Now()
	}
	c := &Context{Values: &Values{Text: map[string]string{}, HTML: map[string]string{}, Lists: map[string][]string{}, Known: Known}, Location: time.UTC}
	v, lists := c.Values.Text, c.Values.Lists

	var wsName string
	var settings []byte
	if err := q.QueryRow(ctx, `SELECT name, settings FROM workspaces WHERE id = $1`, ref.WorkspaceID).Scan(&wsName, &settings); err != nil {
		return nil, fmt.Errorf("load workspace: %w", err)
	}
	var ws struct {
		BrandColor string `json:"brand_color"`
		Timezone   string `json:"timezone"`
	}
	_ = json.Unmarshal(settings, &ws)
	if loc, err := time.LoadLocation(ws.Timezone); err == nil && ws.Timezone != "" {
		c.Location = loc
	}
	c.Values.Location = c.Location
	v["workspace.name"] = displayname.Displayable(wsName)
	v["date.today"] = ref.Now.In(c.Location).Format("2 January 2006")

	brandID, teamID, mailboxID := ref.BrandID, ref.TeamID, ref.MailboxID
	var contactID *uuid.UUID
	receivedAt := ""
	ticketFields := map[string]any{}
	if ref.TicketID != nil {
		var number int64
		var subject, status, priority, channel string
		var created time.Time
		var fields []byte
		var tBrand uuid.UUID
		var tTeam, tMailbox *uuid.UUID
		err := q.QueryRow(ctx, `SELECT number, subject, status, priority, channel, created_at, fields, brand_id, team_id, mailbox_id, contact_id, received_at
			FROM tickets WHERE workspace_id = $1 AND id = $2`, ref.WorkspaceID, *ref.TicketID).
			Scan(&number, &subject, &status, &priority, &channel, &created, &fields, &tBrand, &tTeam, &tMailbox, &contactID, &receivedAt)
		if err != nil {
			return nil, fmt.Errorf("load ticket: %w", err)
		}
		c.TicketID = ref.TicketID
		v["ticket.number"] = strconv.FormatInt(number, 10)
		v["ticket.subject"] = subject
		v["ticket.status"] = statusWord(status)
		v["ticket.priority"] = humanWord(priority)
		v["ticket.channel"] = channelWord(channel)
		v["ticket.created_date"] = created.In(c.Location).Format("2 January 2006")
		_ = json.Unmarshal(fields, &ticketFields)
		if brandID == nil {
			brandID = &tBrand
		}
		if teamID == nil {
			teamID = tTeam
		}
		if mailboxID == nil {
			mailboxID = tMailbox
		}
	} else if ref.Sample {
		for _, x := range Variables {
			if x.Group != "contact" && x.Group != "company" && x.Group != "ticket" {
				continue
			}
			if x.List {
				lists[x.Key] = strings.Split(x.Example, ", ")
			} else {
				v[x.Key] = x.Example
			}
		}
	}

	if contactID != nil {
		var name, phone string
		var email *string
		var companyID *uuid.UUID
		var fields []byte
		err := q.QueryRow(ctx, `SELECT name, email, phone, company_id, fields FROM contacts WHERE workspace_id = $1 AND id = $2`,
			ref.WorkspaceID, *contactID).Scan(&name, &email, &phone, &companyID, &fields)
		if err != nil && !db.IsNotFound(err) {
			return nil, fmt.Errorf("load contact: %w", err)
		}
		if err == nil {
			name = displayname.Displayable(name)
			v["contact.name"] = name
			v["contact.first_name"], v["contact.last_name"] = splitName(name)
			if email != nil {
				v["contact.email"] = *email
			}
			v["contact.phone"] = phone
			var cf map[string]any
			_ = json.Unmarshal(fields, &cf)
			if err := fillFields(ctx, q, ref.WorkspaceID, domain.FieldEntityContact, cf, v, lists); err != nil {
				return nil, err
			}
			if lists["contact.labels"], err = labels(ctx, q, "contact_tags", "contact_id", *contactID); err != nil {
				return nil, err
			}
			if companyID != nil {
				var cname, cdomain string
				var cfields []byte
				err := q.QueryRow(ctx, `SELECT name, domain, fields FROM companies WHERE workspace_id = $1 AND id = $2`,
					ref.WorkspaceID, *companyID).Scan(&cname, &cdomain, &cfields)
				if err != nil && !db.IsNotFound(err) {
					return nil, fmt.Errorf("load company: %w", err)
				}
				if err == nil {
					v["company.name"], v["company.domain"] = displayname.Displayable(cname), cdomain
					var cof map[string]any
					_ = json.Unmarshal(cfields, &cof)
					if err := fillFields(ctx, q, ref.WorkspaceID, domain.FieldEntityCompany, cof, v, lists); err != nil {
						return nil, err
					}
					if lists["company.labels"], err = labels(ctx, q, "company_tags", "company_id", *companyID); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	if err := fillFields(ctx, q, ref.WorkspaceID, domain.FieldEntityTicket, ticketFields, v, lists); err != nil {
		return nil, err
	}
	if ref.TicketID != nil {
		var err error
		if lists["ticket.labels"], err = labels(ctx, q, "ticket_tags", "ticket_id", *ref.TicketID); err != nil {
			return nil, err
		}
	}

	// The brand: the ticket's, the one named, else the address's, else the default.
	if brandID == nil && mailboxID != nil {
		var b uuid.UUID
		if err := q.QueryRow(ctx, `SELECT brand_id FROM mailboxes WHERE workspace_id = $1 AND id = $2`, ref.WorkspaceID, *mailboxID).Scan(&b); err == nil {
			brandID = &b
		}
	}
	var bName, bColor, logo, slug string
	var hcOn bool
	var host *string
	brandQuery := `SELECT b.id, b.name, b.color, COALESCE(b.images->>'logo', ''), b.slug, b.help_center_enabled,
		(SELECT h.hostname FROM help_domains h WHERE h.brand_id = b.id AND h.status = 'active')
		FROM brands b WHERE b.workspace_id = $1 AND `
	var err error
	if brandID != nil {
		err = q.QueryRow(ctx, brandQuery+`b.id = $2`, ref.WorkspaceID, *brandID).Scan(&c.BrandID, &bName, &bColor, &logo, &slug, &hcOn, &host)
	} else {
		err = q.QueryRow(ctx, brandQuery+`b.is_default`, ref.WorkspaceID).Scan(&c.BrandID, &bName, &bColor, &logo, &slug, &hcOn, &host)
	}
	if err != nil && !db.IsNotFound(err) {
		return nil, fmt.Errorf("load brand: %w", err)
	}
	c.BrandName = displayname.Displayable(bName)
	c.BrandColor = "#0A0A0A"
	for _, col := range []string{bColor, ws.BrandColor} {
		if ValidColor(col) {
			c.BrandColor = col
			break
		}
	}
	c.LogoURL = domain.Avatar(logo).URL()
	v["brand.name"] = c.BrandName
	v["brand.color"] = c.BrandColor
	v["brand.logo_url"] = c.LogoURL
	if hcOn && cfg != nil {
		if host != nil && *host != "" {
			v["brand.help_center_url"] = "https://" + *host
		} else {
			v["brand.help_center_url"] = cfg.HelpCenterURL(slug)
		}
	}

	team := ""
	if teamID != nil {
		var name, desc, sig string
		err := q.QueryRow(ctx, `SELECT name, description, signature_html FROM teams WHERE workspace_id = $1 AND id = $2`,
			ref.WorkspaceID, *teamID).Scan(&name, &desc, &sig)
		if err != nil && !db.IsNotFound(err) {
			return nil, fmt.Errorf("load team: %w", err)
		}
		if err == nil {
			c.TeamID = teamID
			team = displayname.Displayable(name)
			v["team.description"] = desc
			c.TeamSignature = sig
		}
	}
	if team == "" {
		team = c.BrandName
	}
	v["team.name"] = team

	if mailboxID != nil {
		var name, display, email, sig, aliasName, aliasSig string
		var isAlias bool
		alias := strings.ToLower(strings.TrimSpace(firstText(ref.MailboxEmail, receivedAt)))
		err := q.QueryRow(ctx, `SELECT name, display_name, email, signature_html, $3::text = ANY(aliases),
			COALESCE(alias_settings -> $3::text ->> 'display_name', ''), COALESCE(alias_settings -> $3::text ->> 'signature_html', '')
			FROM mailboxes WHERE workspace_id = $1 AND id = $2`,
			ref.WorkspaceID, *mailboxID, alias).Scan(&name, &display, &email, &sig, &isAlias, &aliasName, &aliasSig)
		if err != nil && !db.IsNotFound(err) {
			return nil, fmt.Errorf("load mailbox: %w", err)
		}
		if err == nil {
			c.MailboxID = mailboxID
			if strings.TrimSpace(display) != "" {
				name = display
			}
			if isAlias && strings.TrimSpace(aliasName) != "" {
				name = aliasName
			}
			v["mailbox.name"] = displayname.Displayable(name)
			v["mailbox.email"] = email
			if isAlias {
				v["mailbox.email"] = alias
			}
			c.MailboxSignature = sig
			if isAlias {
				c.AliasSignature = aliasSig
			}
		}
	}
	if ref.MailboxEmail != "" {
		v["mailbox.email"] = ref.MailboxEmail
	}

	switch {
	case ref.AuthorUserID != nil:
		var name, title, phone, pronouns, sig string
		var avatar *string
		err := q.QueryRow(ctx, `SELECT u.name, u.avatar_url, COALESCE(m.title, ''), COALESCE(m.phone, ''), COALESCE(m.pronouns, ''),
			COALESCE(m.signature_html, '') FROM users u LEFT JOIN members m ON m.user_id = u.id AND m.workspace_id = $1 WHERE u.id = $2`,
			ref.WorkspaceID, *ref.AuthorUserID).Scan(&name, &avatar, &title, &phone, &pronouns, &sig)
		if err != nil && !db.IsNotFound(err) {
			return nil, fmt.Errorf("load author: %w", err)
		}
		if err == nil {
			name = displayname.Displayable(name)
			v["agent.name"] = name
			v["agent.first_name"], v["agent.last_name"] = splitName(name)
			v["agent.title"], v["agent.phone"], v["agent.pronouns"] = title, phone, pronouns
			if avatar != nil {
				v["agent.avatar_url"] = domain.Avatar(*avatar).URL()
			}
			c.PersonalSignature = sig
		}
	case strings.TrimSpace(ref.BotName) != "":
		name := displayname.Displayable(ref.BotName)
		v["agent.name"] = name
		v["agent.first_name"], v["agent.last_name"] = splitName(name)
	}
	// A value is never read as a template, not even after it lands in a reply that is
	// filled again on send: braces that would open a tag get a zero-width break.
	for k, val := range v {
		v[k] = Inert(val)
	}
	for k, list := range lists {
		for i, val := range list {
			list[i] = Inert(val)
		}
		lists[k] = list
	}
	if strings.TrimSpace(c.PersonalSignature) != "" {
		// The personal signature takes the variables too, but not itself.
		c.Values.HTML["agent.signature"] = SanitizeForSend(Render(c.PersonalSignature, c.Values, HTML))
	}
	return c, nil
}

// Signature is the signature an email carries, filled in: the alias's it goes out as, else
// the team's, else the address's, else the author's own.
func (c *Context) Signature() string {
	for _, s := range []string{c.AliasSignature, c.TeamSignature, c.MailboxSignature, c.PersonalSignature} {
		if strings.TrimSpace(s) != "" {
			return SanitizeForSend(Render(s, c.Values, HTML))
		}
	}
	return ""
}

// SetMailbox sets the sending address's variables (mailbox.name, mailbox.email) after Load,
// and fills the personal signature again with them: one Load serves each of a Collab
// account's addresses.
func (c *Context) SetMailbox(name, email string) {
	c.Values.Text["mailbox.name"], c.Values.Text["mailbox.email"] = Inert(name), Inert(email)
	delete(c.Values.HTML, "agent.signature")
	if strings.TrimSpace(c.PersonalSignature) != "" {
		c.Values.HTML["agent.signature"] = SanitizeForSend(Render(c.PersonalSignature, c.Values, HTML))
	}
}

// Fill renders src (design HTML) with the context's values, ready to send.
func (c *Context) Fill(src string) string { return SanitizeForSend(Render(src, c.Values, HTML)) }

// FillText renders a subject or other text.
func (c *Context) FillText(src string) string {
	return strings.Join(strings.Fields(Render(src, c.Values, Text)), " ")
}

// fillFields writes an entity's custom field values as their display text.
func fillFields(ctx context.Context, q db.Querier, wid uuid.UUID, entity string, values map[string]any, v map[string]string, lists map[string][]string) error {
	if len(values) == 0 {
		return nil
	}
	defs, err := domain.LoadFieldDefs(ctx, q, wid, entity)
	if err != nil {
		return err
	}
	for key, raw := range values {
		f, ok := defs[key]
		if !ok || raw == nil {
			continue
		}
		if f.Type == domain.FieldMultiSelect {
			lists[entity+".fields."+key] = fieldList(f, raw)
			continue
		}
		v[entity+".fields."+key] = fieldText(ctx, q, wid, f, raw)
	}
	return nil
}

// fieldList is a multi select's option labels.
func fieldList(f domain.CustomField, raw any) []string {
	ids, _ := raw.([]any)
	out := []string{}
	for _, x := range ids {
		id, _ := x.(string)
		for _, o := range f.Options {
			if o.ID == id {
				out = append(out, o.Label)
			}
		}
	}
	return out
}

// labels are the names of a record's labels, in name order.
func labels(ctx context.Context, q db.Querier, table, column string, id uuid.UUID) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT t.name FROM `+table+` x JOIN tags t ON t.id = x.tag_id WHERE x.`+column+` = $1 ORDER BY lower(t.name)`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, displayname.Displayable(n))
	}
	return out, rows.Err()
}

func fieldText(ctx context.Context, q db.Querier, wid uuid.UUID, f domain.CustomField, raw any) string {
	label := func(id string) string {
		for _, o := range f.Options {
			if o.ID == id {
				return o.Label
			}
		}
		return ""
	}
	switch f.Type {
	case domain.FieldSelect:
		s, _ := raw.(string)
		return label(s)
	case domain.FieldMultiSelect:
		list, _ := raw.([]any)
		var out []string
		for _, x := range list {
			if s, ok := x.(string); ok && label(s) != "" {
				out = append(out, label(s))
			}
		}
		return strings.Join(out, ", ")
	case domain.FieldCheckbox:
		if b, _ := raw.(bool); b {
			return "Yes"
		}
		return "No"
	case domain.FieldNumber:
		if n, ok := raw.(float64); ok {
			if n == math.Trunc(n) && math.Abs(n) < 1e15 {
				return strconv.FormatInt(int64(n), 10)
			}
			return strconv.FormatFloat(n, 'f', -1, 64)
		}
	case domain.FieldUser:
		s, _ := raw.(string)
		id, err := uuid.Parse(s)
		if err != nil {
			return ""
		}
		var name string
		if err := q.QueryRow(ctx, `SELECT u.name FROM users u JOIN members m ON m.user_id = u.id AND m.workspace_id = $1 WHERE u.id = $2`,
			wid, id).Scan(&name); err != nil {
			return ""
		}
		return displayname.Displayable(name)
	case domain.FieldDate:
		s, _ := raw.(string)
		if t, err := time.Parse("2006-01-02", s); err == nil {
			return t.Format("2 January 2006")
		}
		return s
	}
	if s, ok := raw.(string); ok {
		return s
	}
	return fmt.Sprint(raw)
}

func splitName(name string) (string, string) {
	first, last, _ := strings.Cut(strings.TrimSpace(name), " ")
	return first, strings.TrimSpace(last)
}

func humanWord(s string) string {
	s = strings.ReplaceAll(s, "_", " ")
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func statusWord(s string) string { return humanWord(s) }

func channelWord(s string) string {
	switch s {
	case domain.ChannelAPI:
		return "API"
	case domain.ChannelPortal:
		return "Form"
	}
	return humanWord(s)
}

var inert = strings.NewReplacer("{{", "{\u200b{", "{%", "{\u200b%")

// Inert keeps text that looks like a template tag from ever being read as one.
func Inert(s string) string {
	if !strings.Contains(s, "{{") && !strings.Contains(s, "{%") {
		return s
	}
	return inert.Replace(s)
}

// firstText is the first non-blank value.
func firstText(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
