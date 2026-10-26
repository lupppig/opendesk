package collab

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/email"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/plan"
)

// The HTTP API (contracts/collab.md "Endpoints" and "Who sees what").

// pendingSync skips a test whose path needs a sync-side function that is still a
// placeholder ("not implemented").
func pendingSync(t *testing.T, status int, f func() error) {
	t.Helper()
	if status != http.StatusInternalServerError {
		return
	}
	if err := f(); err != nil && err.Error() == "not implemented" {
		t.Skip("needs the sync side (placeholder still in place)")
	}
}

type apiError struct {
	Status int    `json:"status"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

func TestAPIPrivateAccountInvisible(t *testing.T) {
	w := newWorld(t)
	a := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	tid, _ := w.seedThread(a, "Private hello", w.folder(a, RoleInbox))

	// Its owner sees it.
	owner := w.api(w.agent)
	var mine []MailAccount
	if st := doJSON(t, "GET", owner.URL+"/v1/collab/accounts", nil, &mine); st != 200 || len(mine) != 1 || !mine[0].CanManage {
		t.Fatalf("owner list: %d %+v", st, mine)
	}
	if mine[0].Unread != 1 {
		t.Fatalf("unread = %d, want 1", mine[0].Unread)
	}

	// Nobody else does, the workspace owner included.
	for _, p := range []*auth.Principal{w.owner, w.agent2, w.viewer} {
		srv := w.api(p)
		var list []MailAccount
		if st := doJSON(t, "GET", srv.URL+"/v1/collab/accounts", nil, &list); st != 200 || len(list) != 0 {
			t.Fatalf("list as %s: %d %+v", p.Role, st, list)
		}
		for _, path := range []string{"/v1/collab/accounts/" + a.ID.String() + "/folders", "/v1/collab/threads/" + tid.String(),
			"/v1/collab/threads?account_id=" + a.ID.String()} {
			if st := doJSON(t, "GET", srv.URL+path, nil, nil); st != 404 {
				t.Fatalf("GET %s as %s: %d, want 404", path, p.Role, st)
			}
		}
		if st := doJSON(t, "PATCH", srv.URL+"/v1/collab/accounts/"+a.ID.String(), map[string]any{"name": "Mine now"}, nil); st != 404 {
			t.Fatalf("PATCH as %s: %d, want 404", p.Role, st)
		}
		if st := doJSON(t, "DELETE", srv.URL+"/v1/collab/accounts/"+a.ID.String(), nil, nil); st != 404 {
			t.Fatalf("DELETE as %s: %d, want 404", p.Role, st)
		}
		var page httpx.Page[MailThread]
		if st := doJSON(t, "GET", srv.URL+"/v1/collab/threads?box=all", nil, &page); st != 200 || len(page.Data) != 0 {
			t.Fatalf("threads as %s: %d %+v", p.Role, st, page)
		}
		var counts CollabCounts
		if st := doJSON(t, "GET", srv.URL+"/v1/collab/counts", nil, &counts); st != 200 || counts.Boxes.Inbox != 0 {
			t.Fatalf("counts as %s: %d %+v", p.Role, st, counts)
		}
	}
}

func TestAPISharedAccountVisible(t *testing.T) {
	w := newWorld(t)
	team := w.team("Sales", w.agent2)
	byTeam := w.account(w.agent, ProviderMicrosoft, VisibilityShared, Audience{TeamIDs: []uuid.UUID{team}})
	everyone := w.account(w.agent, ProviderIMAP, VisibilityShared, Audience{Everyone: true})
	tid, _ := w.seedThread(byTeam, "Team mail", w.folder(byTeam, RoleInbox))

	var list []MailAccount
	if st := doJSON(t, "GET", w.api(w.agent2).URL+"/v1/collab/accounts", nil, &list); st != 200 || len(list) != 2 {
		t.Fatalf("team member list: %d %+v", st, list)
	}
	for _, m := range list {
		if m.CanManage {
			t.Fatalf("a team member manages %s", m.Email)
		}
		if m.ID == everyone.ID && m.IMAP != nil {
			t.Fatal("IMAP settings shown to someone who does not manage the account")
		}
	}
	if st := doJSON(t, "GET", w.api(w.agent2).URL+"/v1/collab/threads/"+tid.String(), nil, nil); st != 200 {
		t.Fatalf("team member detail: %d", st)
	}
	// The viewer is not on the team: only the account shared with everyone.
	if st := doJSON(t, "GET", w.api(w.viewer).URL+"/v1/collab/accounts", nil, &list); st != 200 || len(list) != 1 || list[0].ID != everyone.ID {
		t.Fatalf("viewer list: %d %+v", st, list)
	}
	if st := doJSON(t, "GET", w.api(w.viewer).URL+"/v1/collab/threads/"+tid.String(), nil, nil); st != 404 {
		t.Fatalf("viewer detail: %d, want 404", st)
	}
	// The owner sees IMAP settings and manages; an admin manages shared accounts too.
	if st := doJSON(t, "GET", w.api(w.agent).URL+"/v1/collab/accounts", nil, &list); st != 200 || len(list) != 2 {
		t.Fatalf("owner list: %d", st)
	}
	for _, m := range list {
		if m.ID == everyone.ID && m.IMAP == nil {
			t.Fatal("owner does not see the IMAP settings")
		}
	}
	var out MailAccount
	if st := doJSON(t, "PATCH", w.api(w.owner).URL+"/v1/collab/accounts/"+everyone.ID.String(),
		map[string]any{"name": "Support", "color": "teal"}, &out); st != 200 || out.Name != "Support" || out.Color != "teal" {
		t.Fatalf("admin patch: %d %+v", st, out)
	}
	if st := doJSON(t, "PATCH", w.api(w.agent2).URL+"/v1/collab/accounts/"+byTeam.ID.String(), map[string]any{"name": "X"}, nil); st != 403 {
		t.Fatalf("audience patch: %d, want 403", st)
	}
	if n := w.count(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'collab.account_updated'`, w.wid); n != 1 {
		t.Fatalf("audit entries = %d", n)
	}
}

func TestAPIAccountSharingAndDelete(t *testing.T) {
	w := newWorld(t)
	a := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	srv := w.api(w.agent)
	var out MailAccount
	if st := doJSON(t, "PATCH", srv.URL+"/v1/collab/accounts/"+a.ID.String(), map[string]any{"visibility": "shared"}, nil); st != 422 {
		t.Fatalf("shared with nobody: %d, want 422", st)
	}
	if st := doJSON(t, "PATCH", srv.URL+"/v1/collab/accounts/"+a.ID.String(), map[string]any{"visibility": "shared",
		"shared_with": Audience{UserIDs: []uuid.UUID{uuid.New()}}}, nil); st != 422 {
		t.Fatalf("stranger in audience: %d, want 422", st)
	}
	st := doJSON(t, "PATCH", srv.URL+"/v1/collab/accounts/"+a.ID.String(), map[string]any{"visibility": "shared",
		"shared_with": Audience{UserIDs: []uuid.UUID{w.agent2.UserID}}, "signature_html": `<p>Adam<script>x()</script></p>`}, &out)
	if st != 200 || out.Visibility != VisibilityShared || len(out.SharedWith.UserIDs) != 1 || strings.Contains(out.SignatureHTML, "script") {
		t.Fatalf("share: %d %+v", st, out)
	}
	if n := w.count(`SELECT count(*) FROM events WHERE workspace_id = $1 AND type = 'collab.accounts' AND $2 = ANY(user_ids)`, w.wid, w.agent2.UserID); n != 1 {
		t.Fatalf("collab.accounts to the new audience = %d", n)
	}
	// Back to private: the old audience is told too.
	if st := doJSON(t, "PATCH", srv.URL+"/v1/collab/accounts/"+a.ID.String(), map[string]any{"visibility": "private"}, &out); st != 200 || len(out.SharedWith.UserIDs) != 0 {
		t.Fatalf("unshare: %d %+v", st, out)
	}
	if n := w.count(`SELECT count(*) FROM events WHERE workspace_id = $1 AND type = 'collab.accounts' AND $2 = ANY(user_ids)`, w.wid, w.agent2.UserID); n != 2 {
		t.Fatalf("collab.accounts to the old audience = %d", n)
	}
	if st := doJSON(t, "DELETE", w.api(w.agent2).URL+"/v1/collab/accounts/"+a.ID.String(), nil, nil); st != 404 {
		t.Fatalf("delete by someone who no longer sees it: %d", st)
	}
	if st := doJSON(t, "DELETE", srv.URL+"/v1/collab/accounts/"+a.ID.String(), nil, nil); st != 204 {
		t.Fatalf("delete: %d", st)
	}
	if n := w.count(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'collab.account_disconnected'`, w.wid); n != 1 {
		t.Fatalf("disconnect audit = %d", n)
	}
}

// An admin manages a teammate's shared IMAP account but never learns its password: the
// kept password is not sent to a server other than the one it was entered for.
func TestAPIIMAPKeptPasswordStaysWithItsServer(t *testing.T) {
	w := newWorld(t)
	a := w.account(w.agent, ProviderIMAP, VisibilityShared, Audience{Everyone: true})
	cfg := accountConfig{IMAPHost: "imap.acme.test", IMAPPort: 993, IMAPTLS: "tls", IMAPUsername: a.Email,
		SMTPHost: "smtp.acme.test", SMTPPort: 465, SMTPTLS: "tls"}
	mustExec(t, w.d, `UPDATE collab_accounts SET config = $2 WHERE id = $1`, a.ID, cfg)
	srv := w.api(w.owner)
	for name, imap := range map[string]map[string]any{
		"imap host":                 {"imap_host": "imap.elsewhere.test"},
		"smtp host":                 {"smtp_host": "smtp.elsewhere.test"},
		"smtp host, imap password":  {"smtp_host": "smtp.elsewhere.test", "smtp_password": ""},
		"both hosts, smtp password": {"imap_host": "imap.elsewhere.test", "smtp_host": "smtp.elsewhere.test", "smtp_password": "x"},
	} {
		var e apiError
		if st := doJSON(t, "PATCH", srv.URL+"/v1/collab/accounts/"+a.ID.String(), map[string]any{"imap": imap}, &e); st != 422 || !strings.Contains(e.Detail, "password again") {
			t.Fatalf("%s without the password: %d %+v", name, st, e)
		}
	}
	// With the password typed again the new server is tried (and here refuses).
	var e apiError
	if st := doJSON(t, "PATCH", srv.URL+"/v1/collab/accounts/"+a.ID.String(), map[string]any{"imap": map[string]any{
		"imap_host": "127.0.0.1", "imap_port": 1, "password": "typed-again"}}, &e); st != 422 || e.Code != "connection_failed" {
		t.Fatalf("with the password: %d %+v", st, e)
	}
	fresh, err := loadAccount(w.ctx, w.d.DB.Pool, w.wid, a.ID)
	if err != nil || !reflect.DeepEqual(fresh.Config, cfg) {
		t.Fatalf("config changed: %v %+v", err, fresh.Config)
	}
}

func TestAPIThreadShareAndAssign(t *testing.T) {
	w := newWorld(t)
	a := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	inbox := w.folder(a, RoleInbox)
	tid, _ := w.seedThread(a, "Help me with this one", inbox)
	other, _ := w.seedThread(a, "Not shared", inbox)
	owner, bea := w.api(w.agent), w.api(w.agent2)

	var d MailThreadDetail
	if st := doJSON(t, "PUT", owner.URL+"/v1/collab/threads/"+tid.String()+"/shares",
		map[string]any{"shares": Audience{UserIDs: []uuid.UUID{w.agent2.UserID}}}, &d); st != 200 || !d.Thread.Shared || len(d.Shares.UserIDs) != 1 {
		t.Fatalf("share: %d %+v", st, d)
	}
	if st := doJSON(t, "GET", bea.URL+"/v1/collab/threads/"+tid.String(), nil, &d); st != 200 || !d.CanAct || len(d.Messages) != 1 {
		t.Fatalf("shared detail: %d %+v", st, d)
	}
	for _, path := range []string{"/v1/collab/threads/" + other.String(), "/v1/collab/accounts/" + a.ID.String() + "/folders"} {
		if st := doJSON(t, "GET", bea.URL+path, nil, nil); st != 404 {
			t.Fatalf("GET %s: %d, want 404", path, st)
		}
	}
	var page httpx.Page[MailThread]
	if st := doJSON(t, "GET", bea.URL+"/v1/collab/threads?box=shared", nil, &page); st != 200 || len(page.Data) != 1 || page.Data[0].ID != tid {
		t.Fatalf("shared box: %d %+v", st, page)
	}
	if st := doJSON(t, "GET", bea.URL+"/v1/collab/threads", nil, &page); st != 200 || len(page.Data) != 0 {
		t.Fatalf("inbox spans only visible accounts: %d %+v", st, page)
	}
	var counts CollabCounts
	if st := doJSON(t, "GET", bea.URL+"/v1/collab/counts", nil, &counts); st != 200 || counts.Boxes.Shared != 1 || counts.Boxes.Inbox != 0 {
		t.Fatalf("counts: %d %+v", st, counts)
	}
	if n := w.count(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'collab.thread_shared'`, w.wid); n != 1 {
		t.Fatalf("share audit = %d", n)
	}
	// Unshare: she is told once more, then loses it.
	if st := doJSON(t, "PUT", owner.URL+"/v1/collab/threads/"+tid.String()+"/shares", map[string]any{"shares": Audience{}}, &d); st != 200 || d.Thread.Shared {
		t.Fatalf("unshare: %d %+v", st, d)
	}
	if n := w.count(`SELECT count(*) FROM events WHERE workspace_id = $1 AND type = 'collab.changed' AND $2 = ANY(user_ids)`, w.wid, w.agent2.UserID); n != 2 {
		t.Fatalf("collab.changed to Bea = %d, want 2", n)
	}
	if st := doJSON(t, "GET", bea.URL+"/v1/collab/threads/"+tid.String(), nil, nil); st != 404 {
		t.Fatalf("after unshare: %d", st)
	}

	// Assigning lets her see it, and tells her.
	if st := doJSON(t, "PUT", owner.URL+"/v1/collab/threads/"+tid.String()+"/assignees",
		map[string]any{"user_ids": []uuid.UUID{w.viewer.UserID}}, nil); st != 422 {
		t.Fatalf("assign a viewer: %d, want 422", st)
	}
	if st := doJSON(t, "PUT", owner.URL+"/v1/collab/threads/"+tid.String()+"/assignees",
		map[string]any{"user_ids": []uuid.UUID{w.agent2.UserID}}, &d); st != 200 || len(d.Thread.Assignees) != 1 || d.Thread.Assignees[0].ID != w.agent2.UserID {
		t.Fatalf("assign: %d %+v", st, d)
	}
	if st := doJSON(t, "GET", bea.URL+"/v1/collab/threads?box=assigned", nil, &page); st != 200 || len(page.Data) != 1 {
		t.Fatalf("assigned box: %d %+v", st, page)
	}
	if st := doJSON(t, "GET", bea.URL+"/v1/collab/counts", nil, &counts); st != 200 || counts.Boxes.Assigned != 1 || len(counts.Assigned) != 1 || counts.Assigned[a.ID] != 1 {
		t.Fatalf("assigned counts by account: %d %+v", st, counts)
	}
	if n := w.count(`SELECT count(*) FROM notifications WHERE workspace_id = $1 AND user_id = $2 AND type = 'collab_assigned'
		AND data->>'thread_id' = $3 AND data->>'subject' = 'Help me with this one'`, w.wid, w.agent2.UserID, tid.String()); n != 1 {
		t.Fatalf("assigned notifications = %d", n)
	}
	// Assigning again tells nobody twice.
	if st := doJSON(t, "PUT", owner.URL+"/v1/collab/threads/"+tid.String()+"/assignees",
		map[string]any{"user_ids": []uuid.UUID{w.agent2.UserID, w.agent.UserID}}, &d); st != 200 || len(d.Thread.Assignees) != 2 {
		t.Fatalf("assign two: %d", st)
	}
	if n := w.count(`SELECT count(*) FROM notifications WHERE workspace_id = $1 AND type = 'collab_assigned'`, w.wid); n != 1 {
		t.Fatalf("notifications after reassign = %d, want 1 (none to self)", n)
	}
}

func TestAPIViewersCannotAct(t *testing.T) {
	w := newWorld(t)
	a := w.account(w.agent, ProviderGoogle, VisibilityShared, Audience{Everyone: true})
	tid, _ := w.seedThread(a, "Read only", w.folder(a, RoleInbox))
	srv := w.api(w.viewer)
	var d MailThreadDetail
	if st := doJSON(t, "GET", srv.URL+"/v1/collab/threads/"+tid.String(), nil, &d); st != 200 || d.CanAct {
		t.Fatalf("viewer detail: %d can_act=%v", st, d.CanAct)
	}
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/v1/collab/threads/actions", map[string]any{"thread_ids": []uuid.UUID{tid}, "action": "read"}},
		{"POST", "/v1/collab/threads/" + tid.String() + "/comments", map[string]any{"body_html": "<p>hi</p>"}},
		{"PUT", "/v1/collab/threads/" + tid.String() + "/shares", map[string]any{"shares": Audience{Everyone: true}}},
		{"PUT", "/v1/collab/threads/" + tid.String() + "/assignees", map[string]any{"user_ids": []uuid.UUID{}}},
		{"POST", "/v1/collab/accounts/" + a.ID.String() + "/folders", map[string]any{"name": "Clients"}},
		{"POST", "/v1/collab/messages", map[string]any{"account_id": a.ID, "mode": "new", "to": []string{"x@example.com"}, "subject": "s", "body_html": "b"}},
	} {
		if st := doJSON(t, c.method, srv.URL+c.path, c.body, nil); st != 403 {
			t.Fatalf("%s %s as viewer: %d, want 403", c.method, c.path, st)
		}
	}
}

func TestAPICollabRequired(t *testing.T) {
	w := newWorld(t)
	off := *w.d
	off.Plan = &plan.Service{CollabOff: true}
	w.s = newService(&off)
	srv := w.api(w.agent)
	var status CollabStatus
	if st := doJSON(t, "GET", srv.URL+"/v1/collab", nil, &status); st != 200 || status.Enabled || status.Reason == nil || *status.Reason != "disabled" {
		t.Fatalf("status: %d %+v", st, status)
	}
	var e apiError
	for _, path := range []string{"/v1/collab/accounts", "/v1/collab/threads", "/v1/collab/counts", "/v1/collab/sidebar"} {
		if st := doJSON(t, "GET", srv.URL+path, nil, &e); st != 402 || e.Code != "collab_required" {
			t.Fatalf("GET %s: %d %+v", path, st, e)
		}
	}
	// On: enabled, no reason.
	w.s = newService(w.d)
	srv = w.api(w.agent)
	if st := doJSON(t, "GET", srv.URL+"/v1/collab", nil, &status); st != 200 || !status.Enabled || status.Reason != nil {
		t.Fatalf("status on: %d %+v", st, status)
	}
	// An API key never reaches Collab.
	key := *w.agent
	key.Kind, key.UserID, key.APIKeyID = auth.KindAPIKey, uuid.Nil, uuid.New()
	if st := doJSON(t, "GET", w.api(&key).URL+"/v1/collab/accounts", nil, nil); st != 403 {
		t.Fatalf("API key: %d, want 403", st)
	}
}

func TestAPIThreadListBoxesAndPages(t *testing.T) {
	w := newWorld(t)
	a := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	inbox, trash, sent := w.folder(a, RoleInbox), w.folder(a, RoleTrash), w.folder(a, RoleSent)
	var inboxIDs []uuid.UUID
	for i := range 5 {
		tid, _ := w.seedThread(a, fmt.Sprintf("Invoice %d", i), inbox)
		// Distinct times, newest last.
		mustExec(t, w.d, `UPDATE collab_threads SET last_message_at = now() - make_interval(mins => $2) WHERE id = $1`, tid, 10-i)
		inboxIDs = append(inboxIDs, tid)
	}
	trashed, _ := w.seedThread(a, "Old newsletter", trash)
	sentID, _ := w.seedThread(a, "My question", sent)
	archived, _ := w.seedThread(a, "Archived receipt") // Gmail: in no special folder
	mustExec(t, w.d, `UPDATE collab_threads SET unread = false WHERE id = ANY($1)`, []uuid.UUID{sentID, archived})
	srv := w.api(w.agent)

	// Inbox, newest first, two per page.
	var got []uuid.UUID
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 5 {
			t.Fatal("pagination does not end")
		}
		var page httpx.Page[MailThread]
		u := srv.URL + "/v1/collab/threads?limit=2&cursor=" + url.QueryEscape(cursor)
		if st := doJSON(t, "GET", u, nil, &page); st != 200 {
			t.Fatalf("page: %d", st)
		}
		for _, th := range page.Data {
			got = append(got, th.ID)
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(got) != 5 {
		t.Fatalf("inbox = %d threads, want 5", len(got))
	}
	for i := range got {
		if got[i] != inboxIDs[4-i] {
			t.Fatalf("order: got %v, want newest first %v", got, inboxIDs)
		}
	}
	box := func(q string) []uuid.UUID {
		t.Helper()
		var page httpx.Page[MailThread]
		if st := doJSON(t, "GET", srv.URL+"/v1/collab/threads?"+q, nil, &page); st != 200 {
			t.Fatalf("GET ?%s: %d", q, st)
		}
		out := []uuid.UUID{}
		for _, th := range page.Data {
			out = append(out, th.ID)
		}
		return out
	}
	if l := box("box=trash"); len(l) != 1 || l[0] != trashed {
		t.Fatalf("trash: %v", l)
	}
	if l := box("box=sent"); len(l) != 1 || l[0] != sentID {
		t.Fatalf("sent: %v", l)
	}
	// Gmail archives by removing INBOX: what is in no inbox, trash, spam or drafts, sent
	// mail included, is archived.
	if l := box("box=archive"); len(l) != 2 || !slices.Contains(l, archived) || !slices.Contains(l, sentID) {
		t.Fatalf("archive: %v", l)
	}
	if l := box("box=all"); len(l) != 7 {
		t.Fatalf("all: %d, want 7 (not trash)", len(l))
	}
	if l := box("box=unread"); len(l) != 5 {
		t.Fatalf("unread: %d, want 5", len(l))
	}
	if l := box("folder_id=" + trash.ID.String()); len(l) != 1 || l[0] != trashed {
		t.Fatalf("folder: %v", l)
	}
	if l := box("box=all&q=" + url.QueryEscape("Invoice 3")); len(l) != 1 || l[0] != inboxIDs[3] {
		t.Fatalf("search: %v", l)
	}
	if l := box("box=all&q=newsl"); len(l) != 0 {
		t.Fatalf("search outside the box: %v", l)
	}
	if l := box("box=trash&q=newsl"); len(l) != 1 {
		t.Fatalf("subject fallback: %v", l)
	}
	if st := doJSON(t, "GET", srv.URL+"/v1/collab/threads?cursor=nope!", nil, nil); st != 400 {
		t.Fatalf("bad cursor: %d", st)
	}

	// Counts and folders.
	var counts CollabCounts
	if st := doJSON(t, "GET", srv.URL+"/v1/collab/counts", nil, &counts); st != 200 {
		t.Fatalf("counts: %d", st)
	}
	if counts.Boxes.Inbox != 5 || counts.Boxes.Unread != 5 || counts.Accounts[a.ID] != 5 || counts.Folders[inbox.ID] != 5 || counts.Folders[trash.ID] != 1 {
		t.Fatalf("counts: %+v", counts)
	}
	var folders []MailFolder
	if st := doJSON(t, "GET", srv.URL+"/v1/collab/accounts/"+a.ID.String()+"/folders", nil, &folders); st != 200 || len(folders) != 6 {
		t.Fatalf("folders: %d %d", st, len(folders))
	}
	if folders[0].Role == nil || *folders[0].Role != RoleInbox || folders[0].Unread != 5 || folders[0].Total != 5 || folders[0].CanRename {
		t.Fatalf("first folder: %+v", folders[0])
	}
	if l := folders[len(folders)-1]; l.Role == nil || *l.Role != RoleTrash {
		t.Fatalf("trash is not last: %+v", l)
	}
}

func TestAPIFolders(t *testing.T) {
	w := newWorld(t)
	a := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	fake := &fakeProvider{}
	w.useFake(fake)
	srv := w.api(w.agent)
	inbox := w.folder(a, RoleInbox)
	var e apiError
	if st := doJSON(t, "PATCH", srv.URL+"/v1/collab/folders/"+inbox.ID.String(), map[string]any{"name": "Box"}, &e); st != 422 || e.Code != "special_folder" {
		t.Fatalf("rename inbox: %d %+v", st, e)
	}
	if st := doJSON(t, "DELETE", srv.URL+"/v1/collab/folders/"+inbox.ID.String(), nil, nil); st != 422 {
		t.Fatalf("delete inbox: %d", st)
	}
	if st := doJSON(t, "POST", srv.URL+"/v1/collab/accounts/"+a.ID.String()+"/folders", map[string]any{"name": "a/b"}, nil); st != 422 {
		t.Fatalf("slash: %d", st)
	}
	fake.err = &providerError{msg: "the label name is taken", permanent: true}
	if st := doJSON(t, "POST", srv.URL+"/v1/collab/accounts/"+a.ID.String()+"/folders", map[string]any{"name": "Clients"}, &e); st != 502 || e.Code != "provider_error" {
		t.Fatalf("provider refusal: %d %+v", st, e)
	}
	fake.err = nil
	var f MailFolder
	st := doJSON(t, "POST", srv.URL+"/v1/collab/accounts/"+a.ID.String()+"/folders", map[string]any{"name": "Clients"}, &f)
	pendingSync(t, st, func() error { _, err := w.s.upsertFolder(w.ctx, w.d.DB.Pool, a, RemoteFolder{}); return err })
	if st != 200 || f.Path != "Clients" || !f.CanRename {
		t.Fatalf("create: %d %+v", st, f)
	}
	if st := doJSON(t, "PATCH", srv.URL+"/v1/collab/folders/"+f.ID.String(), map[string]any{"name": "Customers"}, &f); st != 200 || f.Name != "Customers" {
		t.Fatalf("rename: %d %+v", st, f)
	}
	tid, _ := w.seedThread(a, "Filed", inbox, w.folder(a, RoleArchive))
	if _, err := w.d.DB.Pool.Exec(w.ctx, `UPDATE collab_threads SET folder_ids = folder_ids || $2::uuid WHERE id = $1`, tid, f.ID); err != nil {
		t.Fatal(err)
	}
	if st := doJSON(t, "DELETE", srv.URL+"/v1/collab/folders/"+f.ID.String(), nil, nil); st != 204 {
		t.Fatalf("delete: %d", st)
	}
	if n := w.count(`SELECT count(*) FROM collab_threads WHERE id = $1 AND $2 = ANY(folder_ids)`, tid, f.ID); n != 0 {
		t.Fatal("the conversation still names the deleted folder")
	}
}

func TestAPIActions(t *testing.T) {
	w := newWorld(t)
	a := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	b := w.account(w.agent2, ProviderGoogle, VisibilityPrivate, Audience{})
	tid, _ := w.seedThread(a, "Act on me", w.folder(a, RoleInbox))
	hidden, _ := w.seedThread(b, "Not yours", w.folder(b, RoleInbox))
	srv := w.api(w.agent)
	if st := doJSON(t, "POST", srv.URL+"/v1/collab/threads/actions", map[string]any{"thread_ids": []uuid.UUID{tid, hidden}, "action": "read"}, nil); st != 404 {
		t.Fatalf("one hidden: %d, want 404 (all or nothing)", st)
	}
	if st := doJSON(t, "POST", srv.URL+"/v1/collab/threads/actions", map[string]any{"thread_ids": []uuid.UUID{tid}, "action": "move"}, nil); st != 422 {
		t.Fatalf("move without folder: %d", st)
	}
	if st := doJSON(t, "POST", srv.URL+"/v1/collab/threads/actions",
		map[string]any{"thread_ids": []uuid.UUID{tid}, "action": "move", "folder_id": w.folder(b, RoleArchive).ID}, nil); st != 422 {
		t.Fatalf("folder of another account: %d", st)
	}
	imap := w.account(w.agent, ProviderIMAP, VisibilityPrivate, Audience{})
	itid, _ := w.seedThread(imap, "IMAP", w.folder(imap, RoleInbox))
	var e apiError
	if st := doJSON(t, "POST", srv.URL+"/v1/collab/threads/actions",
		map[string]any{"thread_ids": []uuid.UUID{itid}, "action": "label", "folder_id": w.folder(imap, RoleArchive).ID}, &e); st != 422 || e.Code != "labels_unsupported" {
		t.Fatalf("label on IMAP: %d %+v", st, e)
	}
	var out struct {
		Threads []MailThread `json:"threads"`
	}
	st := doJSON(t, "POST", srv.URL+"/v1/collab/threads/actions", map[string]any{"thread_ids": []uuid.UUID{tid}, "action": "read"}, &out)
	pendingSync(t, st, func() error {
		tx, err := w.d.DB.Pool.Begin(w.ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(w.ctx)
		return w.s.applyLocal(w.ctx, tx, a, []uuid.UUID{tid}, ActionRead, nil)
	})
	if st != 200 || len(out.Threads) != 1 || out.Threads[0].Unread {
		t.Fatalf("read: %d %+v", st, out)
	}
	if n := w.count(`SELECT count(*) FROM events WHERE workspace_id = $1 AND type = 'collab.changed' AND $2 = ANY(user_ids)`, w.wid, w.agent.UserID); n < 1 {
		t.Fatal("no collab.changed")
	}
}

func TestAPISend(t *testing.T) {
	w := newWorld(t)
	a := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	tid, mid := w.seedThread(a, "Question", w.folder(a, RoleInbox))
	fake := &fakeProvider{}
	w.useFake(fake)
	srv := w.api(w.agent)
	send := func(body map[string]any, out any) int {
		t.Helper()
		return doJSON(t, "POST", srv.URL+"/v1/collab/messages", body, out)
	}
	if st := send(map[string]any{"account_id": a.ID, "mode": "new", "to": []string{}, "subject": "Hi", "body_html": "<p>x</p>"}, nil); st != 422 {
		t.Fatalf("no recipient: %d", st)
	}
	if st := send(map[string]any{"account_id": a.ID, "mode": "new", "to": []string{"not an address"}, "subject": "Hi", "body_html": "x"}, nil); st != 422 {
		t.Fatalf("bad address: %d", st)
	}
	if st := send(map[string]any{"account_id": a.ID, "mode": "reply", "to": []string{"from@example.com"}, "subject": "", "body_html": "x"}, nil); st != 422 {
		t.Fatalf("reply without message: %d", st)
	}
	if st := doJSON(t, "POST", w.api(w.agent2).URL+"/v1/collab/messages", map[string]any{"account_id": a.ID, "mode": "new",
		"to": []string{"x@example.com"}, "subject": "Hi", "body_html": "x"}, nil); st != 404 {
		t.Fatalf("someone else's account: %d", st)
	}
	var e apiError
	fake.err = &providerError{msg: "the recipient address was refused", permanent: true}
	if st := send(map[string]any{"account_id": a.ID, "mode": "new", "to": []string{"x@example.com"}, "subject": "Hi", "body_html": "x"}, &e); st != 502 ||
		e.Code != "send_failed" || e.Detail != "the recipient address was refused" {
		t.Fatalf("refused: %d %+v", st, e)
	}
	if n := w.count(`SELECT count(*) FROM collab_messages WHERE account_id = $1`, a.ID); n != 1 {
		t.Fatalf("a refused message was stored: %d", n)
	}
	fake.err = nil

	var m MailMessage
	st := send(map[string]any{"account_id": a.ID, "mode": "reply", "message_id": mid, "to": []string{"Frank <From@Example.com>"},
		"bcc": []string{"boss@example.com"}, "subject": "", "body_html": `<p>Thanks<script>bad()</script></p>`}, &m)
	if len(fake.sent) != 1 {
		t.Fatalf("provider got %d messages (status %d)", len(fake.sent), st)
	}
	raw := string(fake.sent[0])
	for _, want := range []string{"Subject: Re: Question", "In-Reply-To: <" + mid.String() + "@example.com>", "Bcc: <boss@example.com>", "To: <from@example.com>"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("sent message lacks %q:\n%s", want, raw)
		}
	}
	if strings.Contains(raw, "bad()") {
		t.Fatal("the script was sent")
	}
	if !strings.Contains(raw, "@"+a.Email[strings.Index(a.Email, "@")+1:]+">") {
		t.Fatalf("Message-ID is not at the account's domain:\n%s", raw)
	}
	pendingSync(t, st, func() error {
		_, err := w.s.storeSent(w.ctx, a, nil, SentRef{}, w.agent.UserID, nil)
		return err
	})
	if st != 200 || m.ThreadID != tid || m.SentBy == nil || m.SentBy.ID != w.agent.UserID {
		t.Fatalf("stored: %d %+v", st, m)
	}
}

func TestAPISendRateLimit(t *testing.T) {
	w := newWorld(t)
	a := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	w.useFake(&fakeProvider{err: errors.New("refused")})
	srv := w.api(w.agent)
	body := map[string]any{"account_id": a.ID, "mode": "new", "to": []string{"x@example.com"}, "subject": "Hi", "body_html": "x"}
	for i := range sendPerMinute {
		if st := doJSON(t, "POST", srv.URL+"/v1/collab/messages", body, nil); st != 502 {
			t.Fatalf("send %d: %d", i, st)
		}
	}
	if st := doJSON(t, "POST", srv.URL+"/v1/collab/messages", body, nil); st != 429 {
		t.Fatalf("over the limit: %d", st)
	}
}

func TestAPICommentsAndMentions(t *testing.T) {
	w := newWorld(t)
	a := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	tid, _ := w.seedThread(a, "Tricky customer", w.folder(a, RoleInbox))
	adam, bea := w.api(w.agent), w.api(w.agent2)

	if st := doJSON(t, "POST", adam.URL+"/v1/collab/threads/"+tid.String()+"/comments", map[string]any{"body_html": "<script>x</script>"}, nil); st != 422 {
		t.Fatalf("empty after cleaning: %d", st)
	}
	var c MailComment
	body := fmt.Sprintf(`<p>Can you look, <span data-type="mention" data-id="%s" class="mention">@Bea</span>?<img src=x onerror=alert(1)></p>`, w.agent2.UserID)
	if st := doJSON(t, "POST", adam.URL+"/v1/collab/threads/"+tid.String()+"/comments", map[string]any{"body_html": body}, &c); st != 200 {
		t.Fatalf("comment: %d", st)
	}
	if strings.Contains(c.BodyHTML, "onerror") || len(c.Mentions) != 1 || c.Mentions[0] != w.agent2.UserID || c.Author == nil {
		t.Fatalf("comment: %+v", c)
	}
	// Bea could not see it: the mention shared it with her and told her.
	var d MailThreadDetail
	if st := doJSON(t, "GET", bea.URL+"/v1/collab/threads/"+tid.String(), nil, &d); st != 200 || len(d.Comments) != 1 || d.Thread.CommentCount != 1 {
		t.Fatalf("Bea's detail: %d %+v", st, d)
	}
	if len(d.Shares.UserIDs) != 1 || d.Shares.UserIDs[0] != w.agent2.UserID {
		t.Fatalf("shares: %+v", d.Shares)
	}
	if n := w.count(`SELECT count(*) FROM notifications WHERE workspace_id = $1 AND user_id = $2 AND type = 'collab_mention'
		AND data->>'comment_id' = $3`, w.wid, w.agent2.UserID, c.ID.String()); n != 1 {
		t.Fatalf("mention notifications = %d", n)
	}
	// Only its author edits; its author or an admin deletes.
	if st := doJSON(t, "PATCH", bea.URL+"/v1/collab/comments/"+c.ID.String(), map[string]any{"body_html": "<p>mine</p>"}, nil); st != 403 {
		t.Fatalf("edit by another: %d", st)
	}
	if st := doJSON(t, "PATCH", adam.URL+"/v1/collab/comments/"+c.ID.String(), map[string]any{"body_html": "<p>Edited</p>"}, &c); st != 200 ||
		!strings.Contains(c.BodyHTML, "Edited") || len(c.Mentions) != 0 {
		t.Fatalf("edit: %d %+v", st, c)
	}
	if st := doJSON(t, "DELETE", bea.URL+"/v1/collab/comments/"+c.ID.String(), nil, nil); st != 403 {
		t.Fatalf("delete by another agent: %d", st)
	}
	if st := doJSON(t, "DELETE", w.api(w.owner).URL+"/v1/collab/comments/"+c.ID.String(), nil, nil); st != 404 {
		t.Fatalf("delete by an admin who does not see it: %d, want 404", st)
	}
	if st := doJSON(t, "DELETE", adam.URL+"/v1/collab/comments/"+c.ID.String(), nil, nil); st != 204 {
		t.Fatalf("delete: %d", st)
	}
}

func TestAPISidebar(t *testing.T) {
	w := newWorld(t)
	srv := w.api(w.agent)
	var prefs SidebarPrefs
	if st := doJSON(t, "GET", srv.URL+"/v1/collab/sidebar", nil, &prefs); st != 200 || !prefs.MarkReadOnOpen || !prefs.UnifiedInbox ||
		len(prefs.Order) != 6 || prefs.Order[0] != "favorites" || prefs.Pinned == nil {
		t.Fatalf("defaults: %d %+v", st, prefs)
	}
	in := SidebarPrefs{Order: []string{"mail", "favorites"}, Hidden: []string{"box:spam"}, Pinned: []SidebarPin{{Kind: "box", ID: "inbox"}},
		ExpandedAccounts: []string{uuid.NewString()}, MarkReadOnOpen: false, UnifiedInbox: true}
	if st := doJSON(t, "PUT", srv.URL+"/v1/collab/sidebar", in, &prefs); st != 200 {
		t.Fatalf("put: %d", st)
	}
	prefs = SidebarPrefs{}
	if st := doJSON(t, "GET", srv.URL+"/v1/collab/sidebar", nil, &prefs); st != 200 || prefs.MarkReadOnOpen || len(prefs.Order) != 2 ||
		len(prefs.Pinned) != 1 || prefs.Hidden[0] != "box:spam" {
		t.Fatalf("roundtrip: %d %+v", st, prefs)
	}
	// Someone else's is their own.
	if st := doJSON(t, "GET", w.api(w.agent2).URL+"/v1/collab/sidebar", nil, &prefs); st != 200 || !prefs.MarkReadOnOpen {
		t.Fatalf("other member: %+v", prefs)
	}
	big := SidebarPrefs{Hidden: make([]string, 0, 450)}
	for i := range 450 {
		big.Hidden = append(big.Hidden, "folder:"+uuid.NewString()+strconv.Itoa(i))
	}
	if st := doJSON(t, "PUT", srv.URL+"/v1/collab/sidebar", big, nil); st != 422 {
		t.Fatalf("over 16 KB: %d", st)
	}
	if st := doJSON(t, "PUT", srv.URL+"/v1/collab/sidebar", SidebarPrefs{Hidden: []string{strings.Repeat("x", 101)}}, nil); st != 422 {
		t.Fatalf("long item: %d", st)
	}
}

func TestAPISidebarBadgesAndTabs(t *testing.T) {
	w := newWorld(t)
	srv := w.api(w.agent)
	var prefs SidebarPrefs
	if st := doJSON(t, "GET", srv.URL+"/v1/collab/sidebar", nil, &prefs); st != 200 || prefs.Badges == nil || len(prefs.Badges) != 0 ||
		strings.Join(prefs.MailTabs, ",") != "inbox,starred,sent,drafts,archive" {
		t.Fatalf("defaults: %d %+v", st, prefs)
	}
	folder := "folder:" + uuid.NewString()
	base := func() SidebarPrefs {
		p := defaultSidebar()
		p.Badges = map[string]string{"section:mail": "total", "box:inbox": "none", "account:" + uuid.NewString(): "unread",
			folder: "total", "ticket_folder:mine": "none", "view:" + uuid.NewString(): "overdue", "team:" + uuid.NewString(): "open", "section:teams": "awaiting",
			"ticket_folder:open": "urgent", "section:views": "count"}
		p.MailTabs = []string{"inbox", folder, "unread", "spam"}
		return p
	}
	if st := doJSON(t, "PUT", srv.URL+"/v1/collab/sidebar", base(), &prefs); st != 200 || len(prefs.Badges) != 10 {
		t.Fatalf("put: %d %+v", st, prefs)
	}
	prefs = SidebarPrefs{}
	if st := doJSON(t, "GET", srv.URL+"/v1/collab/sidebar", nil, &prefs); st != 200 || prefs.Badges["section:mail"] != "total" ||
		prefs.Badges[folder] != "total" || prefs.Badges["section:teams"] != "awaiting" || prefs.Badges["ticket_folder:open"] != "urgent" || strings.Join(prefs.MailTabs, ",") != "inbox,"+folder+",unread,spam" {
		t.Fatalf("roundtrip: %d %+v", st, prefs)
	}
	// A client that leaves them out keeps what is saved.
	old := map[string]any{"order": []string{"mail"}, "hidden": []string{}, "pinned": []any{}, "expanded_accounts": []string{},
		"mark_read_on_open": true, "unified_inbox": true}
	if st := doJSON(t, "PUT", srv.URL+"/v1/collab/sidebar", old, &prefs); st != 200 || prefs.Badges["section:mail"] != "total" || len(prefs.MailTabs) != 4 {
		t.Fatalf("left out: %d %+v", st, prefs)
	}
	// No tabs at all is a choice too.
	empty := base()
	empty.MailTabs = []string{}
	if st := doJSON(t, "PUT", srv.URL+"/v1/collab/sidebar", empty, &prefs); st != 200 || prefs.MailTabs == nil || len(prefs.MailTabs) != 0 {
		t.Fatalf("no tabs: %d %+v", st, prefs)
	}

	bad := map[string]func(p *SidebarPrefs){
		"badge value":      func(p *SidebarPrefs) { p.Badges["box:inbox"] = "all" },
		"badge value case": func(p *SidebarPrefs) { p.Badges["team:x"] = "Overdue" },
		"badge prefix":     func(p *SidebarPrefs) { p.Badges["thing:x"] = "none" },
		"badge section":    func(p *SidebarPrefs) { p.Badges["section:workspace"] = "none" },
		"badge empty id":   func(p *SidebarPrefs) { p.Badges["view:"] = "none" },
		"badge key length": func(p *SidebarPrefs) { p.Badges["view:"+strings.Repeat("x", 100)] = "none" },
		"too many badges": func(p *SidebarPrefs) {
			for i := range 301 {
				p.Badges["view:"+strconv.Itoa(i)] = "none"
			}
		},
		"tab box":        func(p *SidebarPrefs) { p.MailTabs = append(p.MailTabs, "shared") },
		"tab folder id":  func(p *SidebarPrefs) { p.MailTabs = append(p.MailTabs, "folder:nope") },
		"tab duplicated": func(p *SidebarPrefs) { p.MailTabs = append(p.MailTabs, "inbox") },
		"too many tabs": func(p *SidebarPrefs) {
			for range 31 {
				p.MailTabs = append(p.MailTabs, "folder:"+uuid.NewString())
			}
		},
	}
	for name, mutate := range bad {
		p := base()
		mutate(&p)
		if st := doJSON(t, "PUT", srv.URL+"/v1/collab/sidebar", p, nil); st != 422 {
			t.Errorf("%s: %d, want 422", name, st)
		}
	}
}

func TestAPIAttachmentSignature(t *testing.T) {
	w := newWorld(t)
	a := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	tid, mid := w.seedThread(a, "With a file", w.folder(a, RoleInbox))
	mustExec(t, w.d, `UPDATE collab_messages SET attachments = $2 WHERE id = $1`, mid,
		[]storedAttachment{{Index: 0, Filename: "a.pdf", ContentType: "application/pdf", Size: 3}})
	srv := w.api(nil) // no session: the signature alone
	get := func(u string) int {
		t.Helper()
		resp, err := http.Get(u)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	good := w.s.attachmentPath(mid, 0)
	exp := strconv.FormatInt(time.Now().Add(-time.Minute).Unix(), 10)
	for name, path := range map[string]string{
		"no signature":   "/v1/collab/attachments/" + mid.String() + "/0",
		"wrong sig":      strings.Replace(good, "sig=", "sig=00", 1),
		"other index":    strings.Replace(good, "/0?", "/1?", 1),
		"other message":  strings.Replace(good, mid.String(), uuid.NewString(), 1),
		"expired":        "/v1/collab/attachments/" + mid.String() + "/0?exp=" + exp + "&sig=" + w.s.attachmentSig(mid, 0, exp),
		"not a uuid":     "/v1/collab/attachments/nope/0?exp=1&sig=00",
		"no such file":   w.s.attachmentPath(mid, 3),
		"negative index": "/v1/collab/attachments/" + mid.String() + "/-1?exp=1&sig=00",
	} {
		if st := get(srv.URL + path); st != 404 {
			t.Fatalf("%s: %d, want 404", name, st)
		}
	}
	// Message shapes carry signed links that pass.
	var d MailThreadDetail
	if st := doJSON(t, "GET", w.api(w.agent).URL+"/v1/collab/threads/"+tid.String(), nil, &d); st != 200 ||
		len(d.Messages[0].Attachments) != 1 || !strings.Contains(d.Messages[0].Attachments[0].URL, "sig=") {
		t.Fatalf("detail attachments: %d %+v", st, d.Messages)
	}
}

// A stored body naming another message's file (mail anyone can send, or a teammate's
// reply) never gets that link signed again: only a message's own inline images are.
func TestAPIAttachmentLinksOfOtherMessagesStayExpired(t *testing.T) {
	w := newWorld(t)
	private := w.account(w.agent2, ProviderGoogle, VisibilityPrivate, Audience{})
	_, secret := w.seedThread(private, "Payroll", w.folder(private, RoleInbox))
	mustExec(t, w.d, `UPDATE collab_messages SET attachments = $2 WHERE id = $1`, secret,
		[]storedAttachment{{Index: 0, Filename: "payroll.pdf", ContentType: "application/pdf", Size: 3}})
	shared := w.account(w.owner, ProviderGoogle, VisibilityShared, Audience{Everyone: true})
	tid, mid := w.seedThread(shared, "Look at this", w.folder(shared, RoleInbox))
	mustExec(t, w.d, `UPDATE collab_messages SET body_html = $2 WHERE id = $1`, mid,
		`<p><img src="http://api.test/v1/collab/attachments/`+secret.String()+`/0?exp=1&amp;sig=00"></p>`)

	var d MailThreadDetail
	if st := doJSON(t, "GET", w.api(w.agent).URL+"/v1/collab/threads/"+tid.String(), nil, &d); st != 200 || len(d.Messages) != 1 {
		t.Fatalf("detail: %d %+v", st, d)
	}
	if !strings.Contains(d.Messages[0].BodyHTML, secret.String()+"/0?exp=1&amp;sig=00") {
		t.Fatalf("another message's link was signed again: %s", d.Messages[0].BodyHTML)
	}
}

func TestAPIAttachmentServe(t *testing.T) {
	w := newWorld(t)
	a := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	_, mid := w.seedThread(a, "Files", w.folder(a, RoleInbox))
	raw, err := email.BuildMIME(&email.OutMessage{From: mail.Address{Address: "from@example.com"}, To: []string{a.Email}, Subject: "Files",
		MessageID: "<files@example.com>", HTML: "<p>hi</p>", Text: "hi", Attachments: []email.OutAttachment{
			{Filename: "report.pdf", ContentType: "application/pdf", Data: []byte("%PDF-1.4 report")},
			{Filename: "pic.png", ContentType: "image/png", Data: []byte("\x89PNG fake")},
		}}, false)
	if err != nil {
		t.Fatal(err)
	}
	key := "collab/" + w.wid.String() + "/" + a.ID.String() + "/" + mid.String() + ".eml"
	if err := w.d.Storage.Put(w.ctx, key, bytes.NewReader(raw), int64(len(raw)), "message/rfc822"); err != nil {
		t.Fatal(err)
	}
	mustExec(t, w.d, `UPDATE collab_messages SET raw_key = $2, attachments = $3 WHERE id = $1`, mid, key, []storedAttachment{
		{Index: 0, Filename: "report.pdf", ContentType: "application/pdf", Size: 15},
		{Index: 1, Filename: "pic.png", ContentType: "image/png", Size: 9}})
	srv := w.api(nil)
	get := func(path string) (*http.Response, string) {
		t.Helper()
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}
	resp, body := get(w.s.attachmentPath(mid, 0))
	if resp.StatusCode != 200 || body != "%PDF-1.4 report" || resp.Header.Get("Content-Type") != "application/pdf" ||
		!strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment") || resp.Header.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("pdf: %d %q %v", resp.StatusCode, body, resp.Header)
	}
	if resp, _ := get(w.s.attachmentPath(mid, 1)); !strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("image without inline=1: %v", resp.Header)
	}
	if resp, body := get(w.s.attachmentPath(mid, 1) + "&inline=1"); resp.StatusCode != 200 || body != "\x89PNG fake" ||
		!strings.HasPrefix(resp.Header.Get("Content-Disposition"), "inline") {
		t.Fatalf("image inline: %d %v", resp.StatusCode, resp.Header)
	}
	if resp, _ := get(w.s.attachmentPath(mid, 0) + "&inline=1"); !strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("a pdf never shows inline: %v", resp.Header)
	}

	// Forwarding with its files attaches them.
	fake := &fakeProvider{}
	w.useFake(fake)
	var m MailMessage
	st := doJSON(t, "POST", w.api(w.agent).URL+"/v1/collab/messages", map[string]any{"account_id": a.ID, "mode": "forward", "message_id": mid,
		"to": []string{"colleague@example.com"}, "subject": "", "body_html": "<p>FYI</p>", "forward_attachments": true}, &m)
	if len(fake.sent) != 1 {
		t.Fatalf("forward: %d, nothing sent", st)
	}
	sent := string(fake.sent[0])
	if !strings.Contains(sent, "Subject: Fwd: Files") || !strings.Contains(sent, "report.pdf") || !strings.Contains(sent, "pic.png") ||
		strings.Contains(sent, "In-Reply-To") {
		t.Fatalf("forward:\n%s", sent)
	}
	if st != 200 || m.ThreadID == w.threadOf(mid) {
		t.Fatalf("a forward starts its own conversation: %d %+v", st, m)
	}
}

// threadOf is a message's conversation.
func (w *world) threadOf(mid uuid.UUID) uuid.UUID {
	w.t.Helper()
	var id uuid.UUID
	if err := w.d.DB.Pool.QueryRow(w.ctx, `SELECT thread_id FROM collab_messages WHERE id = $1`, mid).Scan(&id); err != nil {
		w.t.Fatal(err)
	}
	return id
}

// TestOutgoingFromProvenance: mail is the account's own by where the provider filed it or
// who sent it, never by its From, which a sender writes themselves.
func TestOutgoingFromProvenance(t *testing.T) {
	w := newWorld(t)
	a := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	srv := w.api(w.agent)
	outgoing := func(tid uuid.UUID) (bool, []Address) {
		t.Helper()
		var d MailThreadDetail
		if st := doJSON(t, "GET", srv.URL+"/v1/collab/threads/"+tid.String(), nil, &d); st != 200 || len(d.Messages) != 1 {
			t.Fatalf("thread: %d %d messages", st, len(d.Messages))
		}
		return d.Messages[0].Outgoing, d.Messages[0].Bcc
	}
	self := Address{Name: "Me", Email: a.Email}
	bcc := []Address{{Email: "hidden@example.com"}}
	inbox, mid := w.seedThread(a, "Forged", w.folder(a, RoleInbox))
	mustExec(t, w.d, `UPDATE collab_messages SET from_addr = $2, bcc_addrs = $3 WHERE id = $1`, mid, self, bcc)
	if out, b := outgoing(inbox); out || len(b) != 0 {
		t.Fatalf("mail in the inbox claiming the account's From: outgoing %v, bcc %v", out, b)
	}
	sent, mid := w.seedThread(a, "Sent", w.folder(a, RoleSent))
	mustExec(t, w.d, `UPDATE collab_messages SET from_addr = $2, bcc_addrs = $3 WHERE id = $1`, mid, self, bcc)
	if out, b := outgoing(sent); !out || len(b) != 1 {
		t.Fatalf("mail in Sent: outgoing %v, bcc %v", out, b)
	}
	fromApp, mid := w.seedThread(a, "From opendesk", w.folder(a, RoleInbox))
	mustExec(t, w.d, `UPDATE collab_messages SET sent_by = $2 WHERE id = $1`, mid, w.agent.UserID)
	if out, _ := outgoing(fromApp); !out {
		t.Fatal("mail sent from opendesk is not outgoing")
	}
}
