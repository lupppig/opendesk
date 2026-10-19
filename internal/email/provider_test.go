package email

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/jobs"
)

// runTidy runs the workspace's queued provider tidy-ups and returns how many ran.
func (w *world) runTidy() int {
	w.t.Helper()
	rows, err := w.d.DB.Pool.Query(w.ctx, `SELECT id, args FROM river_job WHERE kind = $1 AND args->>'workspace_id' = $2 ORDER BY id`,
		jobs.ProviderTidyArgs{}.Kind(), w.wid.String())
	if err != nil {
		w.t.Fatal(err)
	}
	type job struct {
		id   int64
		args jobs.ProviderTidyArgs
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (job, error) {
		var j job
		var raw []byte
		if err := r.Scan(&j.id, &raw); err != nil {
			return j, err
		}
		return j, json.Unmarshal(raw, &j.args)
	})
	if err != nil {
		w.t.Fatal(err)
	}
	for _, j := range list {
		if err := w.s.providerTidy(w.ctx, j.args); err != nil {
			w.t.Fatalf("tidy %s: %v", j.args.Action, err)
		}
		mustExec(w.t, w.d, `DELETE FROM river_job WHERE id = $1`, j.id)
	}
	return len(list)
}

// queueArchive is what a ticket's move to solved does in its transaction.
func (w *world) queueArchive(ticketID uuid.UUID) {
	w.t.Helper()
	if err := w.d.DB.Tx(w.ctx, func(tx pgx.Tx) error {
		return domain.QueueProviderArchive(w.ctx, tx, w.d.Jobs, w.wid, ticketID)
	}); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) tidyJobs(action string) int {
	return w.count(`SELECT count(*) FROM river_job WHERE kind = $1 AND args->>'workspace_id' = $2 AND args->>'action' = $3`,
		jobs.ProviderTidyArgs{}.Kind(), w.wid.String(), action)
}

// messageRef is the provider ref of the message whose provider id is pid.
func (w *world) messageRef(pid string) (uuid.UUID, uuid.UUID, providerRef) {
	w.t.Helper()
	var mid, tid uuid.UUID
	var ref providerRef
	if err := w.d.DB.Pool.QueryRow(w.ctx, `SELECT id, ticket_id, provider_ref FROM messages WHERE workspace_id = $1 AND provider_ref->>'id' = $2`,
		w.wid, pid).Scan(&mid, &tid, &ref); err != nil {
		w.t.Fatalf("message with provider id %s: %v", pid, err)
	}
	return mid, tid, ref
}

// multipartParts reads a multipart/related upload into its parts' bodies.
func multipartParts(t *testing.T, contentType string, body []byte) [][]byte {
	t.Helper()
	mt, params, err := mime.ParseMediaType(contentType)
	if err != nil || mt != "multipart/related" {
		t.Fatalf("content type %q", contentType)
	}
	r := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var out [][]byte
	for {
		p, err := r.NextPart()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(p)
		out = append(out, b)
	}
}

// TestGmailSameMailbox: a Google mailbox that reads through the Gmail API takes its From
// name from the account, records each email's Gmail ids, replies in the customer's Gmail
// thread, and marks read, labels (one label, made once) and archives as its options say.
func TestGmailSameMailbox(t *testing.T) {
	w := newWorld(t)
	f := newFakeProvider(t)
	g := &fakeGmail{profile: "100", labels: map[string][]string{}, threads: map[string]string{}, raw: map[string][]byte{}}
	type upload struct {
		query, contentType string
		body               []byte
	}
	var mu sync.Mutex
	var uploads []upload
	var modifies []map[string][]string
	var labels []map[string]string
	labelsMade := 0
	f.handler = func(rw http.ResponseWriter, r *http.Request) bool {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.URL.Path == "/userinfo":
			_, _ = rw.Write([]byte(`{"email":"desk@acme.test","email_verified":true}`))
		case r.URL.Path == "/gmail/v1/users/me/settings/sendAs":
			_, _ = rw.Write([]byte(`{"sendAs":[{"sendAsEmail":"other@acme.test","displayName":"Someone Else"},
				{"sendAsEmail":"desk@acme.test","displayName":"Acme Help Desk","isPrimary":true}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/upload/gmail/v1/users/me/messages/send":
			uploads = append(uploads, upload{r.URL.RawQuery, r.Header.Get("Content-Type"), readAll(r)})
			_, _ = rw.Write([]byte(`{"id":"s-1","threadId":"th-g1"}`))
		case r.URL.Path == "/gmail/v1/users/me/messages/s-1":
			_, _ = rw.Write([]byte(`{"payload":{"headers":[{"name":"Message-ID","value":"<s-1@mail.gmail.com>"}]}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/gmail/v1/users/me/labels":
			_ = json.NewEncoder(rw).Encode(map[string]any{"labels": labels})
		case r.Method == http.MethodPost && r.URL.Path == "/gmail/v1/users/me/labels":
			var b map[string]string
			_ = json.Unmarshal(readAll(r), &b)
			labelsMade++
			labels = append(labels, map[string]string{"id": "Label_7", "name": b["name"]})
			_, _ = rw.Write([]byte(`{"id":"Label_7","name":"opendesk"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/gmail/v1/users/me/messages/batchModify":
			var b map[string][]string
			_ = json.Unmarshal(readAll(r), &b)
			modifies = append(modifies, b)
			rw.WriteHeader(http.StatusNoContent)
		default:
			return g.serve(rw, r)
		}
		return true
	}
	w.collabClient(f, KindGoogle)
	w.s.gmailBase = f.srv.URL
	srv := w.api(w.admin)

	// Connecting names the sender as Gmail does for the address.
	u, cookie := startOAuth(t, srv.URL, "google", map[string]any{"name": "Desk", "receive": "api"})
	loc := callback(t, srv.URL, "google", u.Query().Get("state"), "code-1", cookie)
	id, err := uuid.Parse(loc.Query().Get("connected"))
	if err != nil {
		t.Fatalf("redirect %s", loc)
	}
	m, _ := loadMailbox(w.ctx, w.d.DB.Pool, w.wid, id)
	if m.DisplayName != "Acme Help Desk" {
		t.Fatalf("display name %q", m.DisplayName)
	}

	// The options are off until an admin turns them on.
	if m.Config.ProviderMarkRead || m.Config.ProviderArchiveOnSolve || m.Config.ProviderLabel {
		t.Fatalf("options on by default: %+v", m.Config)
	}
	var got Mailbox
	if code := doJSON(t, http.MethodPatch, srv.URL+"/v1/mailboxes/"+id.String(), map[string]any{"config": map[string]any{
		"provider_mark_read": true, "provider_archive_on_solve": true, "provider_label": true}}, &got); code != 200 ||
		!got.Config.ProviderMarkRead || !got.Config.ProviderArchiveOnSolve || !got.Config.ProviderLabel {
		t.Fatalf("patch: %d %+v", code, got.Config)
	}
	if n := w.count(`SELECT count(*) FROM audit_logs WHERE workspace_id = $1 AND action = 'mailbox.updated' AND data ? 'provider_options'`, w.wid); n != 1 {
		t.Fatalf("audit %d", n)
	}

	// Two emails arrive; each records its Gmail ids and queues its tidy-up.
	g.inbox = []string{"g2", "g1"}
	g1ID := "g1-" + crypto.RandomString(6)
	g.raw["g1"], g.raw["g2"] = customerMail(t, m.Email, g1ID), customerMail(t, m.Email, "g2-"+crypto.RandomString(6))
	g.threads["g1"], g.threads["g2"] = "th-g1", "th-g2"
	if err := w.s.syncMailbox(w.ctx, id); err != nil {
		t.Fatal(err)
	}
	if n := w.processAll(m); n != 2 {
		t.Fatalf("tickets %d", n)
	}
	if n := w.count(`SELECT count(*) FROM inbound_emails WHERE mailbox_id = $1 AND provider_ref->>'thread' LIKE 'th-g%'`, id); n != 2 {
		t.Fatalf("inbound refs %d", n)
	}
	_, tk1, ref := w.messageRef("g1")
	if ref.Thread != "th-g1" || ref.Account != "desk@acme.test" || ref.Provider != KindGoogle || ref.MailboxID != id {
		t.Fatalf("ref %+v", ref)
	}
	_, tk2, _ := w.messageRef("g2")
	if w.tidyJobs(jobs.ProviderImported) != 2 {
		t.Fatalf("tidy jobs %d", w.tidyJobs(jobs.ProviderImported))
	}
	if n := w.runTidy(); n != 2 {
		t.Fatalf("ran %d", n)
	}
	if labelsMade != 1 || len(modifies) != 2 {
		t.Fatalf("labels made %d, modifies %v", labelsMade, modifies)
	}
	for _, mod := range modifies {
		if len(mod["ids"]) != 1 || strings.Join(mod["removeLabelIds"], ",") != "UNREAD" || strings.Join(mod["addLabelIds"], ",") != "Label_7" {
			t.Fatalf("modify %v", mod)
		}
	}
	if st := w.apiState(m); st.LabelID != "Label_7" || st.LabelAccount != "desk@acme.test" || st.HistoryID == "" {
		t.Fatalf("state %+v", st)
	}

	// A reply joins the customer's Gmail thread, with the subject Gmail keeps in it.
	mid := w.agentReply(tk1, "Unplug it now.", "")
	if err := w.s.sendMessage(w.ctx, w.wid, mid, false); err != nil {
		t.Fatal(err)
	}
	if len(uploads) != 1 || !strings.Contains(uploads[0].query, "uploadType=multipart") {
		t.Fatalf("uploads %+v", uploads)
	}
	parts := multipartParts(t, uploads[0].contentType, uploads[0].body)
	if len(parts) != 2 || strings.TrimSpace(string(parts[0])) != `{"threadId":"th-g1"}` {
		t.Fatalf("parts %q", parts)
	}
	if !bytes.Contains(parts[1], []byte("In-Reply-To: <"+g1ID+"@mail.customer.test>")) ||
		!bytes.Contains(parts[1], []byte("Subject: Re: Printer is on fire\r\n")) || !bytes.Contains(parts[1], []byte("Unplug it now.")) {
		t.Fatalf("message %q", parts[1])
	}
	if sent := w.message(mid); sent.Delivery != "sent" || *sent.MsgID != "<s-1@mail.gmail.com>" {
		t.Fatalf("delivery %s %v", sent.Delivery, sent.MsgID)
	}
	if _, _, sentRef := w.messageRef("s-1"); sentRef.Thread != "th-g1" {
		t.Fatalf("sent ref %+v", sentRef)
	}

	// Solving archives the ticket's email, and only that one.
	w.queueArchive(tk1)
	if w.tidyJobs(jobs.ProviderArchive) != 1 || w.runTidy() != 1 {
		t.Fatal("no archive job")
	}
	last := modifies[len(modifies)-1]
	if strings.Join(last["ids"], ",") != "g1" || strings.Join(last["removeLabelIds"], ",") != "INBOX" || len(last["addLabelIds"]) != 0 {
		t.Fatalf("archive %v", last)
	}
	// With archiving off, solving queues nothing.
	got = Mailbox{}
	if code := doJSON(t, http.MethodPatch, srv.URL+"/v1/mailboxes/"+id.String(), map[string]any{"config": map[string]any{
		"provider_archive_on_solve": false}}, &got); code != 200 || got.Config.ProviderArchiveOnSolve || !got.Config.ProviderLabel {
		t.Fatalf("patch off: %d %+v", code, got.Config)
	}
	w.queueArchive(tk2)
	if w.tidyJobs(jobs.ProviderArchive) != 0 {
		t.Fatal("archived with the option off")
	}

	// A reconnect keeps the options and a From name the admin chose.
	mustExec(t, w.d, `UPDATE mailboxes SET display_name = 'Our Desk' WHERE id = $1`, id)
	u, cookie = startOAuth(t, srv.URL, "google", map[string]any{"mailbox_id": id.String()})
	callback(t, srv.URL, "google", u.Query().Get("state"), "code-2", cookie)
	after, _ := loadMailbox(w.ctx, w.d.DB.Pool, w.wid, id)
	if after.DisplayName != "Our Desk" || !after.Config.ProviderMarkRead || !after.Config.ProviderLabel || after.Config.ProviderArchiveOnSolve {
		t.Fatalf("reconnected %q %+v", after.DisplayName, after.Config)
	}
}

// TestGraphSameMailbox: a Microsoft mailbox that reads through Graph asks for immutable
// ids, records each email's Graph ids, replies with the customer's message in In-Reply-To
// and References and the conversation's subject, and marks read, adds the category and
// archives as its options say.
func TestGraphSameMailbox(t *testing.T) {
	w := newWorld(t)
	f := newFakeProvider(t)
	var mu sync.Mutex
	var prefer []string
	var graphSent []byte
	var patches []map[string]any
	var moves []map[string]string
	raws := map[string][]byte{}
	f.handler = func(rw http.ResponseWriter, r *http.Request) bool {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.URL.Path == "/me/mailFolders/inbox/messages/delta":
			prefer = append(prefer, r.Header.Get("Prefer"))
			_, _ = rw.Write([]byte(`{"value":[{"id":"a","conversationId":"conv-a"}],
				"@odata.deltaLink":"` + f.srv.URL + `/me/mailFolders/inbox/messages/delta?$deltatoken=d1"}`))
		case r.URL.Path == "/me/messages/a/$value":
			_, _ = rw.Write(raws["a"])
		case r.Method == http.MethodGet && r.URL.Path == "/me/messages/a":
			_, _ = rw.Write([]byte(`{"categories":["Blue category"]}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/me/messages/a":
			var b map[string]any
			_ = json.Unmarshal(readAll(r), &b)
			patches = append(patches, b)
			_, _ = rw.Write([]byte(`{}`))
		case r.Method == http.MethodPost && r.URL.Path == "/me/messages/a/move":
			var b map[string]string
			_ = json.Unmarshal(readAll(r), &b)
			moves = append(moves, b)
			rw.WriteHeader(http.StatusCreated)
			_, _ = rw.Write([]byte(`{"id":"a"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/me/sendMail":
			graphSent, _ = base64.StdEncoding.DecodeString(string(readAll(r)))
			rw.WriteHeader(http.StatusAccepted)
		default:
			return false
		}
		return true
	}
	w.collabClient(f, KindMicrosoft)
	w.s.graphBase = f.srv.URL
	m := w.mailbox(KindMicrosoft, KindMicrosoft, func(m *mailboxRow, c *credentials) {
		m.Email = "ops-" + crypto.RandomString(5) + "@acme.test"
		m.Config = MailboxConfig{OAuthEmail: m.Email, OAuthGrant: GrantAPI, OAuthClient: ClientCollab,
			ProviderMarkRead: true, ProviderArchiveOnSolve: true, ProviderLabel: true}
		c.RefreshToken, c.GraphAccessToken, c.GraphExpiry = "refresh-0", "graph-tok", time.Now().Add(time.Hour)
	})
	aID := "a-" + crypto.RandomString(6)
	raws["a"] = customerMail(t, m.Email, aID)
	if err := w.s.syncMailbox(w.ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if n := w.processAll(m); n != 1 {
		t.Fatalf("tickets %d", n)
	}
	if len(prefer) != 1 || !strings.Contains(prefer[0], `IdType="ImmutableId"`) {
		t.Fatalf("prefer %q", prefer)
	}
	_, tk, ref := w.messageRef("a")
	if ref.Thread != "conv-a" || ref.Provider != KindMicrosoft {
		t.Fatalf("ref %+v", ref)
	}
	if w.runTidy() != 1 || len(patches) != 1 {
		t.Fatalf("patches %v", patches)
	}
	cats, _ := patches[0]["categories"].([]any)
	if patches[0]["isRead"] != true || len(cats) != 2 || cats[1] != providerLabelName {
		t.Fatalf("patch %v", patches[0])
	}

	// The reply names the customer's message, so Outlook keeps it in the conversation.
	mid := w.agentReply(tk, "Unplug it now.", "")
	if err := w.s.sendMessage(w.ctx, w.wid, mid, false); err != nil {
		t.Fatal(err)
	}
	irt := "<" + aID + "@mail.customer.test>"
	if !bytes.Contains(graphSent, []byte("In-Reply-To: "+irt)) || !bytes.Contains(graphSent, []byte("References: "+irt)) ||
		!bytes.Contains(graphSent, []byte("Subject: Re: Printer is on fire\r\n")) {
		t.Fatalf("mime %q", graphSent)
	}
	if w.message(mid).Delivery != "sent" {
		t.Fatal("not sent")
	}

	w.queueArchive(tk)
	if w.runTidy() != 1 || len(moves) != 1 || moves[0]["destinationId"] != "archive" {
		t.Fatalf("moves %v", moves)
	}
}

// TestProviderOptionsOff: with the options off nothing is asked of the provider; other
// mailboxes take none of it, and their replies are as before.
func TestProviderOptionsOff(t *testing.T) {
	w := newWorld(t)
	f := newFakeProvider(t)
	g := &fakeGmail{profile: "100", labels: map[string][]string{}, threads: map[string]string{}, raw: map[string][]byte{}}
	var mu sync.Mutex
	var tidyCalls int
	var uploads []string
	var uploadBody []byte
	f.handler = func(rw http.ResponseWriter, r *http.Request) bool {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/labels") || strings.HasSuffix(r.URL.Path, "/batchModify"):
			tidyCalls++
			rw.WriteHeader(500)
		case r.Method == http.MethodPost && r.URL.Path == "/upload/gmail/v1/users/me/messages/send":
			uploads = append(uploads, r.URL.Query().Get("uploadType"))
			uploadBody = readAll(r)
			_, _ = rw.Write([]byte(`{"id":"s-9","threadId":"th-9"}`))
		default:
			return g.serve(rw, r)
		}
		return true
	}
	w.collabClient(f, KindGoogle)
	p := w.s.providers[KindGoogle]
	p.clientID, p.clientSecret, p.tokenURL = "gid", "gsecret", f.srv.URL+"/token"
	w.s.gmailBase = f.srv.URL
	srv := w.api(w.admin)

	m := w.mailbox(KindGoogle, KindGoogle, func(m *mailboxRow, c *credentials) {
		m.Email = "quiet-" + crypto.RandomString(5) + "@acme.test"
		m.Config = MailboxConfig{OAuthEmail: m.Email, OAuthGrant: GrantAPI, OAuthClient: ClientCollab}
		c.RefreshToken, c.AccessToken, c.Expiry = "refresh-0", "tok", time.Now().Add(time.Hour)
	})
	g.inbox = []string{"q1"}
	g.raw["q1"], g.threads["q1"] = customerMail(t, m.Email, "q1-"+crypto.RandomString(6)), "th-q1"
	if err := w.s.syncMailbox(w.ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if n := w.processAll(m); n != 1 {
		t.Fatalf("tickets %d", n)
	}
	qmid, tk, _ := w.messageRef("q1")
	w.queueArchive(tk)
	if w.tidyJobs(jobs.ProviderImported)+w.tidyJobs(jobs.ProviderArchive) != 0 {
		t.Fatal("queued a tidy-up with the options off")
	}
	// Even a job queued earlier does nothing once they are off.
	for _, a := range []jobs.ProviderTidyArgs{{WorkspaceID: w.wid, TicketID: tk, MessageID: &qmid, Action: jobs.ProviderImported},
		{WorkspaceID: w.wid, TicketID: tk, Action: jobs.ProviderArchive}} {
		if err := w.s.providerTidy(w.ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	if tidyCalls != 0 {
		t.Fatalf("%d provider calls with the options off", tidyCalls)
	}

	// A forwarding mailbox cannot turn them on; turning them off is a no-op.
	fw := w.mailbox(InboundForwarding, OutboundSystem, nil)
	r := rawJSON(t, http.MethodPatch, srv.URL+"/v1/mailboxes/"+fw.ID.String(), map[string]any{"config": map[string]any{"provider_mark_read": true}})
	if r.code != 422 || !strings.Contains(r.body, "Gmail and Outlook options") {
		t.Fatalf("forwarding: %d %s", r.code, r.body)
	}
	if code := doJSON(t, http.MethodPatch, srv.URL+"/v1/mailboxes/"+fw.ID.String(), map[string]any{"config": map[string]any{"provider_label": false}}, nil); code != 200 {
		t.Fatalf("off on forwarding: %d", code)
	}

	// A Google mailbox that syncs over IMAP: no Gmail thread, the usual tagged subject,
	// and no archive even with the option stored.
	sm := w.mailbox(KindGoogle, KindGoogle, func(m *mailboxRow, c *credentials) {
		m.Config = MailboxConfig{IMAPHost: "imap.gmail.com", IMAPPort: 993, IMAPTLS: TLSImplicit, OAuthEmail: m.Email, OAuthGrant: GrantSync}
		c.RefreshToken, c.AccessToken, c.Expiry = "refresh-1", "tok", time.Now().Add(time.Hour)
	})
	stk, _ := firstTicket(t, w, sm, customer())
	mustExec(t, w.d, `UPDATE messages SET provider_ref = jsonb_build_object('mailbox_id', $2::text, 'account', $3::text, 'provider', 'google', 'id', 'x1', 'thread', 'th-x')
		WHERE ticket_id = $1 AND direction = 'inbound'`, stk.ID, sm.ID.String(), strings.ToLower(sm.Email))
	mustExec(t, w.d, `UPDATE mailboxes SET config = config || '{"provider_archive_on_solve": true}' WHERE id = $1`, sm.ID)
	mid := w.agentReply(stk.ID, "Over IMAP", "")
	if err := w.s.sendMessage(w.ctx, w.wid, mid, false); err != nil {
		t.Fatal(err)
	}
	if len(uploads) != 1 || uploads[0] != "media" || !bytes.Contains(uploadBody, []byte("[#"+itoa(stk.Number)+"]")) {
		t.Fatalf("sync mailbox upload %v %q", uploads, uploadBody)
	}
	if n := w.count(`SELECT count(*) FROM messages WHERE id = $1 AND provider_ref IS NOT NULL`, mid); n != 0 {
		t.Fatal("recorded a provider ref for a mailbox that does not read through the API")
	}
	w.queueArchive(stk.ID)
	if w.tidyJobs(jobs.ProviderArchive) != 0 || tidyCalls != 0 {
		t.Fatal("archived for a mailbox that does not read through the API")
	}
}
