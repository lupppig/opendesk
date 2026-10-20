package core

import (
	"context"
	"strings"
	"testing"
)

// TestInvitationLinks: an admin copies or resends the link an invitation email carried; a
// copied link opens sign-up but does not prove the address, and an invitation from before
// tokens were sealed gets a new link.
func TestInvitationLinks(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner, _ := e.signup("owner")
	wid := owner.newWorkspace("Links Inc")
	verified := func(email string) bool {
		t.Helper()
		var v bool
		if err := e.d.DB.Pool.QueryRow(ctx, `SELECT email_verified_at IS NOT NULL FROM users WHERE email = $1`, email).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	invite := func(tag string) (id, email, token string) {
		t.Helper()
		email = uniqueEmail(t, tag)
		r := owner.do("POST", "/v1/invitations", map[string]any{"email": email, "role": "agent"})
		if r.Status != 201 {
			t.Fatalf("invite: %d %s", r.Status, r.Raw)
		}
		_, text := e.lastMail(email)
		return r.str("id"), email, tokenFromText(text, "/invite?token=")
	}

	// Copy: the same link the email carried, and signing up with it confirms nothing.
	id, email, tok := invite("copied")
	link := owner.do("POST", "/v1/invitations/"+id+"/link", nil)
	if link.Status != 200 || link.str("url") != "http://app.test/invite?token="+tok {
		t.Fatalf("link: %d %s", link.Status, link.Raw)
	}
	for i := 0; i < 3; i++ {
		if r := owner.do("POST", "/v1/invitations/"+id+"/resend", nil); r.Status != 200 {
			t.Fatalf("resend %d: %d %s", i, r.Status, r.Raw)
		}
	}
	if _, text := e.lastMail(email); !strings.Contains(text, "/invite?token="+tok) {
		t.Fatal("the resent email carries another link")
	}
	if r := owner.do("POST", "/v1/invitations/"+id+"/resend", nil); r.Status != 429 {
		t.Fatalf("fourth resend in an hour: %d", r.Status)
	}
	agent := e.client()
	r := agent.do("POST", "/v1/auth/register", map[string]any{"email": email, "password": goodPassword, "name": "Copied Link", "invite_token": tok})
	if r.Status != 200 {
		t.Fatalf("register: %d %s", r.Status, r.Raw)
	}
	agent.token = r.str("access_token")
	if verified(email) {
		t.Error("a copied link confirmed the address")
	}
	if subj, _ := e.lastMail(email); subj != "Confirm your email" {
		t.Errorf("no confirmation email: %s", subj)
	}
	if acc := agent.do("POST", "/v1/invitations/accept", map[string]any{"token": tok}); acc.Status != 200 {
		t.Fatalf("accept: %d %s", acc.Status, acc.Raw)
	}
	if verified(email) {
		t.Error("accepting a copied link confirmed the address")
	}
	agent.workspace = wid
	if r := agent.do("POST", "/v1/invitations/"+id+"/link", nil); r.Status != 403 {
		t.Errorf("an agent copied a link: %d", r.Status)
	}

	// A link that only went by email still confirms the address.
	_, email2, tok2 := invite("emailed")
	r = e.client().do("POST", "/v1/auth/register", map[string]any{"email": email2, "password": goodPassword, "name": "Emailed", "invite_token": tok2})
	if r.Status != 200 || !verified(email2) {
		t.Fatalf("emailed link: %d verified=%v", r.Status, verified(email2))
	}

	// An invitation without a sealed token gets a new one; the old link stops working.
	id3, _, tok3 := invite("legacy")
	if _, err := e.d.DB.Pool.Exec(ctx, `UPDATE invitations SET token_enc = NULL WHERE id = $1`, id3); err != nil {
		t.Fatal(err)
	}
	link3 := owner.do("POST", "/v1/invitations/"+id3+"/link", nil)
	fresh := tokenFromText(link3.str("url"), "token=")
	if link3.Status != 200 || fresh == "" || fresh == tok3 {
		t.Fatalf("legacy link: %s", link3.Raw)
	}
	if r := e.client().do("POST", "/v1/invitations/lookup", map[string]any{"token": tok3}); r.Status != 404 {
		t.Errorf("old link still works: %d", r.Status)
	}
	if r := e.client().do("POST", "/v1/invitations/lookup", map[string]any{"token": fresh}); r.Status != 200 {
		t.Errorf("new link: %d %s", r.Status, r.Raw)
	}

	// Another workspace's invitation is not found.
	other, _ := e.signup("other")
	other.newWorkspace("Elsewhere")
	if r := other.do("POST", "/v1/invitations/"+id3+"/link", nil); r.Status != 404 {
		t.Errorf("foreign invitation: %d", r.Status)
	}
}
