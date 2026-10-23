package collab

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/email"
	"github.com/useopendesk/opendesk/internal/fromname"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/templates"
)

// What mail carries of the member who writes it (contracts/collab.md "Addresses"): an
// address's From name may be a format naming the sender ("{agent} at Margin Support",
// internal/fromname, the helpdesk's vocabulary), and its signature may take the sender's,
// the address's, the workspace's and the date's template variables (internal/templates,
// kind collab_signature). Both are stored as written and filled for the member writing:
// for the composer (MailIdentity.from_name and filled_signature_html, for the caller), and
// the From name again when the mail goes out, for the member sending it. A value with
// variables is never written to Gmail (settings.go).

// nameTemplated reports whether a From name is a format.
func nameTemplated(v string) bool { return fromname.HasVars(v, fromname.Collab) }

// signatureTemplated reports whether a signature takes variables.
func signatureTemplated(v string) bool { return templates.HasTags(v) }

// templatedValue reports whether a setting's value has variables: for the field names of
// the account and its addresses (display_name, signature_html).
func templatedValue(field, v string) bool {
	if field == "display_name" {
		return nameTemplated(v)
	}
	return signatureTemplated(v)
}

// cleanFromName checks a From name: plain words follow the rules of names people choose
// (internal/displayname); one with variables is a format, checked like the helpdesk's (up
// to 80 characters, only {agent}, {agent_first}, {mailbox} and {workspace}, no < or >).
func cleanFromName(field, v string) (string, error) {
	if strings.ContainsAny(v, "{}") {
		out, err := fromname.Clean(field, v, fromname.Collab)
		if err != nil {
			return "", err
		}
		return out, nil
	}
	out, err := displayname.Validate(field, v, displayname.Workspace, true)
	if err != nil {
		return "", httpx.Wrap(err)
	}
	return out, nil
}

// cleanSignature sanitizes a signature with the signature allowlist and refuses variables
// a Collab signature cannot read.
func cleanSignature(v string) (string, error) {
	if len(v) > maxSignatureHTML {
		return "", httpx.Validation("The signature is too long.")
	}
	// Tags are kept through the allowlist, so a variable in a link (tel:{{ agent.phone }}) stays.
	sig := templates.Protect(v, email.SanitizeSettingsHTML)
	if err := templates.CheckSaved("signature_html", sig, templates.KindCollabSignature, nil); err != nil {
		return "", err
	}
	return sig, nil
}

// filler fills From names and signatures for one member. What it reads (the member, the
// workspace) is read once, on the first value with variables.
type filler struct {
	s    *service
	q    db.Querier
	wid  uuid.UUID
	user uuid.UUID
	tc   *templates.Context
}

func (s *service) fillerFor(q db.Querier, wid, user uuid.UUID) *filler {
	return &filler{s: s, q: q, wid: wid, user: user}
}

func (f *filler) load(ctx context.Context) error {
	if f.tc != nil {
		return nil
	}
	user := f.user
	tc, err := templates.Load(ctx, f.q, f.s.d.Cfg, templates.Ref{WorkspaceID: f.wid, AuthorUserID: &user})
	if err != nil {
		return err
	}
	f.tc = tc
	return nil
}

// mailboxName is what {mailbox} reads: the account's name, else its address.
func mailboxName(a *accountRow) string {
	if n := displayname.Displayable(a.Name); n != "" {
		return n
	}
	return a.Email
}

// fromName fills an address's From name (format) of account a for the member.
func (f *filler) fromName(ctx context.Context, a *accountRow, format string) (string, error) {
	if !nameTemplated(format) {
		return format, nil
	}
	if err := f.load(ctx); err != nil {
		return "", err
	}
	v := f.tc.Values.Text
	return fromname.Fill(format, fromname.Collab, fromname.Names{Agent: v["agent.name"], Mailbox: mailboxName(a), Workspace: v["workspace.name"]}), nil
}

// signature fills an address's signature for the member: mailbox.email is the address,
// mailbox.name its From name filled.
func (f *filler) signature(ctx context.Context, sig, addr, name string) (string, error) {
	if !signatureTemplated(sig) {
		return sig, nil
	}
	if err := f.load(ctx); err != nil {
		return "", err
	}
	f.tc.SetMailbox(name, addr)
	return f.tc.Fill(sig), nil
}
