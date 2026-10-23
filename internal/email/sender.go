package email

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/fromname"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/templates"
)

// What the customer sees on every email (contracts/teams.md "Reply address"): the From
// name is the address's from_name_format filled in, the signature is the team's or else
// the address's, and both take the ticket's variables.

const (
	defaultFromNameFormat = "{agent} at {mailbox}"
	maxAliases            = 20
)

// senderNames are the values a From name is filled with: Agent (the author's full name,
// Ollie's bot name, or "" without an author), Team (the ticket's team, else ""), Brand,
// and Mailbox (the address's display_name, else its name).
type senderNames = fromname.Names

// cleanFromNameFormat validates a from_name_format: up to 80 characters, only the known
// variables, no markup or control characters. "" is the default format.
func cleanFromNameFormat(s string) (string, error) {
	s, err := fromname.Clean("from_name_format", s, fromname.Helpdesk)
	if err != nil || s != "" {
		return s, err
	}
	return defaultFromNameFormat, nil
}

// fromName fills format (internal/fromname): without an author the agent variable and the
// words between it and the next variable are dropped; a format left with no variable reads
// the mailbox.
func fromName(format string, n senderNames) string {
	if strings.TrimSpace(format) == "" {
		format = defaultFromNameFormat
	}
	return fromname.Fill(format, fromname.Helpdesk, n)
}

// cleanAliases normalizes a mailbox's aliases: at most 20 distinct valid addresses, none
// the mailbox's own address and none on the inbound forwarding domain.
func (s *service) cleanAliases(own string, list []string) ([]string, error) {
	if len(list) > maxAliases {
		return nil, httpx.Validation("A mailbox can have at most 20 aliases.")
	}
	out := make([]string, 0, len(list))
	for _, a := range list {
		a = strings.ToLower(strings.TrimSpace(a))
		if !validAddress(a) {
			return nil, httpx.Validation("Enter valid email addresses for the aliases.")
		}
		if strings.EqualFold(emailDomain(a), s.inboundDomain()) {
			return nil, httpx.Validation("An alias cannot be on the inbound forwarding domain.")
		}
		if strings.EqualFold(a, own) {
			return nil, httpx.Validation("An alias cannot be the mailbox's own address.")
		}
		for _, b := range out {
			if b == a {
				return nil, httpx.Validation("Aliases must not repeat.")
			}
		}
		out = append(out, a)
	}
	return out, nil
}

// aliasSettingsInput is one alias's settings in a create or PATCH body.
type aliasSettingsInput struct {
	DisplayName   string     `json:"display_name,omitempty" maxLength:"64" doc:"The From name's {mailbox} for mail to this alias; \"\" = the mailbox's"`
	SignatureHTML *string    `json:"signature_html,omitempty" doc:"Design allowlist, template variables, up to 64 KB; \"\" = none of its own; left out keeps it"`
	BrandID       *uuid.UUID `json:"brand_id,omitempty" doc:"A brand of the workspace for new tickets written to this alias; null = the mailbox's"`
	SendAs        string     `json:"send_as,omitempty" enum:"verified,unverified,unknown" doc:"Left out keeps it. Where the connection cannot read Gmail's send-as list: verified = the provider sends as this address"`
}

// mergeAliasSettings is cur after a body replaced it with in: display names and brands as
// given (an alias left out has none of its own), a signature as given or, left out, as it
// was, send_as as given where it changes (the admin said so), else as it was. It also
// returns the aliases whose send_as the body changed.
func mergeAliasSettings(cur map[string]AliasSettings, in map[string]aliasSettingsInput) (map[string]AliasSettings, []string) {
	out := map[string]AliasSettings{}
	for a, st := range cur {
		if st.SendAs != "" && st.SendAs != SendAsUnknown {
			out[a] = AliasSettings{SendAs: st.SendAs, SendAsBy: st.SendAsBy}
		}
	}
	var claimed []string
	for a, v := range in {
		a = strings.ToLower(strings.TrimSpace(a))
		st := out[a]
		st.DisplayName, st.BrandID = v.DisplayName, v.BrandID
		if v.SignatureHTML != nil {
			st.SignatureHTML = *v.SignatureHTML
		} else {
			st.SignatureHTML = cur[a].SignatureHTML
		}
		if v.SendAs != "" && v.SendAs != firstNonEmpty(st.SendAs, SendAsUnknown) {
			st.SendAs, st.SendAsBy = v.SendAs, SendAsByAdmin
			claimed = append(claimed, a)
		}
		out[a] = st
	}
	return out, claimed
}

// sameAliasSettings compares stored alias settings.
func sameAliasSettings(a, b map[string]AliasSettings) bool {
	norm := func(m map[string]AliasSettings) map[string]AliasSettings {
		out := map[string]AliasSettings{}
		for k, v := range m {
			if v.SendAs == "" || v.SendAs == SendAsUnknown {
				v.SendAs, v.SendAsBy = "", ""
			}
			if !v.isZero() {
				out[k] = v
			}
		}
		return out
	}
	x, y := norm(a), norm(b)
	if len(x) != len(y) {
		return false
	}
	for k, v := range x {
		w, ok := y[k]
		if !ok || v.DisplayName != w.DisplayName || v.SignatureHTML != w.SignatureHTML || v.SendAs != w.SendAs ||
			v.SendAsBy != w.SendAsBy || (v.BrandID == nil) != (w.BrandID == nil) || v.BrandID != nil && *v.BrandID != *w.BrandID {
			return false
		}
	}
	return true
}

// keepAliasSettings drops the settings of aliases the mailbox no longer has.
func keepAliasSettings(m *mailboxRow) {
	for a := range m.AliasSettings {
		if m.alias(a) == "" {
			delete(m.AliasSettings, a)
		}
	}
}

// cleanAliasSettings normalizes a mailbox's alias settings: each key one of its aliases,
// display names as any name people choose, signatures sanitized with the design allowlist
// and checked like the address's own, and no entry that says nothing.
func cleanAliasSettings(m *mailboxRow) error {
	out := make(map[string]AliasSettings, len(m.AliasSettings))
	for a, st := range m.AliasSettings {
		a = strings.ToLower(strings.TrimSpace(a))
		if m.alias(a) == "" {
			return httpx.Validation("alias_settings: " + truncate(a, 80) + " is not an alias of this mailbox")
		}
		name, err := displayname.Validate("Alias display name", st.DisplayName, displayname.Workspace, true)
		if err != nil {
			return httpx.Wrap(err)
		}
		st.DisplayName = name
		st.SignatureHTML = sanitizeSettingsHTML(st.SignatureHTML)
		if len(st.SignatureHTML) > maxSettingsHTML {
			return httpx.Validation("An alias signature is limited to 64 KB.")
		}
		if err := templates.CheckSaved("alias_settings."+a+".signature_html", st.SignatureHTML, templates.KindSignature, nil); err != nil {
			return err
		}
		switch st.SendAs {
		case "", SendAsUnknown:
			st.SendAs, st.SendAsBy = "", ""
		case SendAsVerified, SendAsUnverified:
			if st.SendAsBy != SendAsByProvider {
				st.SendAsBy = SendAsByAdmin
			}
		default:
			return httpx.Validation(`send_as must be "verified", "unverified" or "unknown"`)
		}
		if !st.isZero() {
			out[a] = st
		}
	}
	m.AliasSettings = out
	return nil
}

// checkAliasBrands checks that every alias brand is a brand of the mailbox's workspace; a
// brand that is the mailbox's own is stored as none, so the alias follows the mailbox.
func checkAliasBrands(ctx context.Context, tx pgx.Tx, m *mailboxRow) error {
	var brands []uuid.UUID
	for a, st := range m.AliasSettings {
		if st.BrandID == nil {
			continue
		}
		if *st.BrandID == m.BrandID {
			st.BrandID = nil
			if st.isZero() {
				delete(m.AliasSettings, a)
			} else {
				m.AliasSettings[a] = st
			}
			continue
		}
		brands = append(brands, *st.BrandID)
	}
	if len(brands) == 0 {
		return nil
	}
	var found int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM brands WHERE workspace_id = $1 AND id = ANY($2)`,
		m.WorkspaceID, uniqueIDs(brands)).Scan(&found); err != nil {
		return err
	}
	if found != len(uniqueIDs(brands)) {
		return httpx.Validation("alias_settings names a brand that does not exist in this workspace")
	}
	return nil
}

func uniqueIDs(list []uuid.UUID) []uuid.UUID {
	seen := map[uuid.UUID]bool{}
	out := []uuid.UUID{}
	for _, id := range list {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// applySendAsList sets send_as of every alias from Gmail's send-as list, when the
// connection reads it: verified for an address Gmail lists as verified, else unverified.
// The list decides over an admin's word.
func applySendAsList(m *mailboxRow) {
	l := m.sendAsList()
	if l == nil {
		return
	}
	if m.AliasSettings == nil {
		m.AliasSettings = map[string]AliasSettings{}
	}
	for _, a := range m.Aliases {
		st := m.AliasSettings[a]
		st.SendAs, st.SendAsBy = SendAsUnverified, SendAsByProvider
		for _, x := range l.Addresses {
			if strings.EqualFold(x.Email, a) && x.Verified {
				st.SendAs = SendAsVerified
			}
		}
		m.AliasSettings[a] = st
	}
}

// forgetProviderSendAs is settings without what a provider said about sending as each
// alias (an admin's word stays).
func forgetProviderSendAs(in map[string]AliasSettings) map[string]AliasSettings {
	out := make(map[string]AliasSettings, len(in))
	for a, st := range in {
		if st.SendAsBy == SendAsByProvider {
			st.SendAs, st.SendAsBy = "", ""
		}
		if !st.isZero() {
			out[a] = st
		}
	}
	return out
}

// aliasAudit is what mailbox.updated records of the alias settings: each alias's name,
// brand and send_as, and whether it has a signature (not the signature itself).
func aliasAudit(m *mailboxRow) map[string]any {
	out := map[string]any{}
	for _, a := range m.Aliases {
		st := m.AliasSettings[a]
		out[a] = map[string]any{"display_name": st.DisplayName, "brand_id": st.BrandID, "send_as": firstNonEmpty(st.SendAs, SendAsUnknown),
			"signature": st.SignatureHTML != ""}
	}
	return out
}

// agentName is a person's name as shown to customers.
func agentName(name string) string { return displayname.Displayable(name) }
