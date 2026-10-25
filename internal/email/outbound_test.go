package email

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
	"github.com/google/uuid"
	"github.com/jhillyerd/enmime/v2"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/ids"
)

// smtpSink is a submission server that requires STARTTLS and AUTH PLAIN.
type smtpSink struct {
	mu         sync.Mutex
	user, pass string
	msgs       []sinkMsg
}

type sinkMsg struct {
	from  string
	rcpts []string
	data  []byte
}

type sinkSession struct {
	s      *smtpSink
	authed bool
	cur    sinkMsg
}

func (k *smtpSink) NewSession(*smtp.Conn) (smtp.Session, error) { return &sinkSession{s: k}, nil }
func (ss *sinkSession) AuthMechanisms() []string                { return []string{sasl.Plain} }
func (ss *sinkSession) Auth(string) (sasl.Server, error) {
	return sasl.NewPlainServer(func(_, user, pass string) error {
		if user == ss.s.user && pass == ss.s.pass {
			ss.authed = true
			return nil
		}
		return &smtp.SMTPError{Code: 535, EnhancedCode: smtp.EnhancedCode{5, 7, 8}, Message: "Username and Password not accepted"}
	}), nil
}
func (ss *sinkSession) Mail(from string, _ *smtp.MailOptions) error {
	if !ss.authed {
		return smtp.ErrAuthRequired
	}
	ss.cur = sinkMsg{from: from}
	return nil
}
func (ss *sinkSession) Rcpt(to string, _ *smtp.RcptOptions) error {
	ss.cur.rcpts = append(ss.cur.rcpts, to)
	return nil
}
func (ss *sinkSession) Data(r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	ss.cur.data = b
	ss.s.mu.Lock()
	ss.s.msgs = append(ss.s.msgs, ss.cur)
	ss.s.mu.Unlock()
	return nil
}
func (ss *sinkSession) Reset()        {}
func (ss *sinkSession) Logout() error { return nil }

func startSink(t *testing.T, w *world) (*smtpSink, int) {
	t.Helper()
	tlsCfg, pool := selfSigned(t)
	w.s.guard.rootCAs = pool
	sink := &smtpSink{user: "support@acme.test", pass: "s3cret-pass"}
	srv := smtp.NewServer(sink)
	srv.Domain = "localhost"
	srv.TLSConfig = tlsCfg
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return sink, l.Addr().(*net.TCPAddr).Port
}

func TestSendMessageSMTP(t *testing.T) {
	w := newWorld(t)
	sink, port := startSink(t, w)
	m := w.mailbox(InboundIMAP, OutboundSMTP, func(m *mailboxRow, c *credentials) {
		m.Config = MailboxConfig{IMAPHost: "127.0.0.1", IMAPPort: 1, IMAPTLS: TLSImplicit, IMAPUsername: "u",
			SMTPHost: "127.0.0.1", SMTPPort: port, SMTPTLS: TLSStart, SMTPUsername: "support@acme.test"}
		m.SignatureHTML = "<p>Acme Support team</p>"
		c.Password = "imap-pass"
		c.SMTPPassword = "s3cret-pass"
	})
	from := customer()
	in := w.receive(m, fixture(t, "new_plain.eml", "{{FROM}}", from, "{{TO}}", "", "{{ID}}", "abc"+crypto.RandomString(6)))
	inMsg := w.messageFor(in)
	tk := w.ticket(inMsg.TicketID)
	mid := w.agentReply(tk.ID, "Unplug it now & call us.", "")
	if w.jobCount("email.send_message", "message_id", mid.String()) != 1 {
		t.Fatal("AddMessage should queue the send")
	}
	if err := w.s.sendMessage(w.ctx, w.wid, mid, false); err != nil {
		t.Fatal(err)
	}
	if len(sink.msgs) != 1 {
		t.Fatalf("sink got %d messages", len(sink.msgs))
	}
	got := sink.msgs[0]
	if got.from != m.Email || len(got.rcpts) != 1 || got.rcpts[0] != from {
		t.Fatalf("envelope %s %v", got.from, got.rcpts)
	}
	env, err := enmime.ReadEnvelope(bytes.NewReader(got.data))
	if err != nil {
		t.Fatal(err)
	}
	wantID := "<m_" + mid.String() + ".t" + itoa(tk.Number) + "@acme.test>"
	checks := map[string]string{
		"From":        `"Anna Agent at Acme Support" <` + m.Email + `>`,
		"Message-Id":  wantID,
		"In-Reply-To": *inMsg.MsgID,
		"Subject":     "Re: Printer is on fire [#" + itoa(tk.Number) + "]",
		"To":          "<" + from + ">",
	}
	for k, v := range checks {
		if g := env.GetHeader(k); g != v {
			t.Errorf("%s = %q, want %q", k, g, v)
		}
	}
	if env.GetHeader("Reply-To") != "" {
		t.Error("IMAP mailboxes take replies at their own address")
	}
	if !strings.Contains(env.GetHeader("References"), *inMsg.MsgID) {
		t.Errorf("References %q", env.GetHeader("References"))
	}
	if !strings.HasPrefix(env.Root.Header.Get("Content-Type"), "multipart/alternative") {
		t.Errorf("content type %q", env.Root.Header.Get("Content-Type"))
	}
	if !strings.Contains(env.HTML, "Unplug it now") || !strings.Contains(env.HTML, "Acme Support team") {
		t.Errorf("html %q", env.HTML)
	}
	if text := strings.ReplaceAll(env.Text, "\r\n", "\n"); !strings.Contains(text, "Unplug it now & call us.") || !strings.Contains(text, "-- \nAcme Support team") {
		t.Errorf("text %q", env.Text)
	}
	msg := w.message(mid)
	if msg.Delivery != "sent" || msg.MsgID == nil || *msg.MsgID != wantID {
		t.Fatalf("delivery %s %v", msg.Delivery, msg.MsgID)
	}
	if w.eventCount("message.updated") != 1 {
		t.Fatal("message.updated not published")
	}
	// The email as it went out is kept for the team to open: the reply, the signature and the
	// customer's message quoted under it.
	if kept := sentCopy(t, w, mid); !strings.Contains(kept, "Unplug it now") || !strings.Contains(kept, "Acme Support team") || !strings.Contains(kept, "wrote:") {
		t.Errorf("kept copy %q", kept)
	}

	// A wrong password is permanent: the message fails and the job is not retried.
	mustExec(t, w.d, `UPDATE mailboxes SET credentials_enc = $2 WHERE id = $1`, m.ID, mustSeal(t, w, credentials{Password: "x", SMTPPassword: "wrong"}))
	mid2 := w.agentReply(tk.ID, "Second try", "")
	err = w.s.sendMessage(w.ctx, w.wid, mid2, false)
	var cancel *river.JobCancelError
	if !errors.As(err, &cancel) {
		t.Fatalf("want JobCancel, got %v", err)
	}
	msg = w.message(mid2)
	if msg.Delivery != "failed" || !strings.Contains(msg.DelivError, "refused the login") || strings.Contains(msg.DelivError, "wrong") {
		t.Fatalf("failed delivery %s %q", msg.Delivery, msg.DelivError)
	}
	if kept := sentCopy(t, w, mid2); kept != "" {
		t.Errorf("a reply that did not go out keeps no copy: %q", kept)
	}
}

// sentCopy is the email kept for message mid as it went out ("" none).
func sentCopy(t *testing.T, w *world, mid uuid.UUID) string {
	t.Helper()
	var kept string
	if err := w.d.DB.Pool.QueryRow(w.ctx, `SELECT full_html FROM messages WHERE id = $1`, mid).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	return kept
}

func mustSeal(t *testing.T, w *world, c credentials) []byte {
	t.Helper()
	b, err := w.s.sealCreds(c)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSendMessageSystemWithAttachments(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox(InboundForwarding, OutboundSystem, nil)
	var sent *envelope
	w.s.rawSend = func(_ context.Context, from string, to []string, raw []byte) error {
		sent = &envelope{from: from, rcpts: to, raw: raw}
		return nil
	}
	from := customer()
	tk, _ := firstTicket(t, w, m, from)
	// An agent reply with one inline image and one file.
	img, file := ids.New(), ids.New()
	for _, a := range []struct {
		id     uuid.UUID
		name   string
		ct     string
		inline bool
		data   string
	}{{img, "logo.png", "image/png", true, "PNGDATA"}, {file, "notes.txt", "text/plain", false, "hello notes"}} {
		key := "workspaces/" + w.wid.String() + "/attachments/" + a.id.String()
		if err := w.d.Storage.Put(w.ctx, key, strings.NewReader(a.data), int64(len(a.data)), a.ct); err != nil {
			t.Fatal(err)
		}
		mustExec(t, w.d, `INSERT INTO attachments (id, workspace_id, filename, content_type, size_bytes, storage_key, inline) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			a.id, w.wid, a.name, a.ct, len(a.data), key, a.inline)
	}
	var mid uuid.UUID
	err := w.d.DB.Tx(w.ctx, func(tx pgxTx) error {
		msg, err := domain.AddMessage(w.ctx, tx, w.d.Jobs, domain.NewMessage{WorkspaceID: w.wid, TicketID: tk.ID, Kind: "reply",
			Direction: "outbound", AuthorType: "user", AuthorUserID: &w.uid, DeliveryStatus: "pending",
			BodyHTML: `<p>See <img src="` + domain.AttachmentURL(img) + `"></p>`, BodyText: "See", AttachmentIDs: []uuid.UUID{img, file}})
		mid = msg.ID
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.s.sendMessage(w.ctx, w.wid, mid, false); err != nil {
		t.Fatal(err)
	}
	if sent == nil || sent.from != "notifications@opendesk.test" || sent.rcpts[0] != from {
		t.Fatalf("system send %+v", sent)
	}
	env, err := enmime.ReadEnvelope(bytes.NewReader(sent.raw))
	if err != nil {
		t.Fatal(err)
	}
	if g := env.GetHeader("From"); g != `"Anna Agent at Acme Support" <notifications@opendesk.test>` {
		t.Errorf("From %q", g)
	}
	wantReply := `"Acme Support" <` + m.Email + ">"
	if g := env.GetHeader("Reply-To"); g != wantReply {
		t.Errorf("Reply-To %q want %q", g, wantReply)
	}
	if !strings.HasPrefix(env.Root.Header.Get("Content-Type"), "multipart/mixed") {
		t.Errorf("content type %q", env.Root.Header.Get("Content-Type"))
	}
	if !strings.Contains(env.HTML, `src="cid:`+img.String()+`@opendesk"`) {
		t.Errorf("inline image not cid: %s", env.HTML)
	}
	if len(env.Attachments) != 1 || env.Attachments[0].FileName != "notes.txt" || string(env.Attachments[0].Content) != "hello notes" {
		t.Errorf("attachments %+v", env.Attachments)
	}
	if len(env.Inlines)+len(env.OtherParts) == 0 {
		t.Error("inline image part missing")
	}
	if w.message(mid).Delivery != "sent" {
		t.Fatal("not marked sent")
	}
	// The kept copy shows the image from its stored attachment, not the email's part.
	if kept := sentCopy(t, w, mid); !strings.Contains(kept, "/v1/attachments/"+img.String()+"?exp=") || strings.Contains(kept, "cid:") {
		t.Errorf("kept copy image: %s", kept)
	}
}

func TestSendTicketEmailAutoReply(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox(InboundForwarding, OutboundSystem, nil)
	var raw []byte
	w.s.rawSend = func(_ context.Context, _ string, _ []string, b []byte) error { raw = b; return nil }
	tk, _ := firstTicket(t, w, m, customer())
	err := w.s.sendTicketEmail(w.ctx, jobsSendTicketEmail(w.wid, tk.ID, "We got it [#"+itoa(tk.Number)+"]", "<p>Thanks</p>"))
	if err != nil {
		t.Fatal(err)
	}
	env, err := enmime.ReadEnvelope(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if env.GetHeader("Auto-Submitted") != "auto-generated" || env.GetHeader("X-Auto-Response-Suppress") != "All" {
		t.Errorf("auto headers missing")
	}
	if !strings.HasPrefix(env.GetHeader("Message-Id"), "<a_") || env.GetHeader("In-Reply-To") == "" {
		t.Errorf("ids %q %q", env.GetHeader("Message-Id"), env.GetHeader("In-Reply-To"))
	}
	// A reply to the auto-reply threads back through our Message-ID format.
	reply := "From: " + *w.ticket(tk.ID).Contact.Email + "\r\nTo: x@y.test\r\nSubject: thanks\r\nIn-Reply-To: " + env.GetHeader("Message-Id") +
		"\r\nMessage-ID: <rr" + crypto.RandomString(5) + "@c.test>\r\n\r\nok\r\n"
	if w.messageFor(w.receive(m, reply)).TicketID != tk.ID {
		t.Fatal("reply to auto-reply did not thread")
	}
}

// fakeProvider serves an OAuth token endpoint plus Gmail or Graph routes.
type fakeProvider struct {
	mu       sync.Mutex
	tokenReq []map[string]string
	handler  func(w http.ResponseWriter, r *http.Request) bool
	srv      *httptest.Server
	access   string
}

func newFakeProvider(t *testing.T) *fakeProvider {
	f := &fakeProvider{access: "access-2"}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			_ = r.ParseForm()
			req := map[string]string{}
			for k := range r.PostForm {
				req[k] = r.PostForm.Get(k)
			}
			f.mu.Lock()
			f.tokenReq = append(f.tokenReq, req)
			n := len(f.tokenReq)
			f.mu.Unlock()
			if req["client_secret"] == "" {
				w.WriteHeader(400)
				return
			}
			if req["refresh_token"] == "revoked" {
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": f.access + "-" + itoa(int64(n)), "refresh_token": "refresh-" + itoa(int64(n)), "expires_in": 3600})
			return
		}
		if f.handler != nil && f.handler(w, r) {
			return
		}
		w.WriteHeader(404)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func TestSendMessageGmailAPI(t *testing.T) {
	w := newWorld(t)
	f := newFakeProvider(t)
	w.s.providers[KindGoogle].tokenURL = f.srv.URL + "/token"
	w.s.gmailBase = f.srv.URL
	var uploaded []byte
	var auth string
	f.handler = func(rw http.ResponseWriter, r *http.Request) bool {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/upload/gmail/v1/users/me/messages/send" && r.URL.Query().Get("uploadType") == "media":
			if r.Header.Get("Content-Type") != "message/rfc822" {
				rw.WriteHeader(400)
				return true
			}
			auth = r.Header.Get("Authorization")
			uploaded, _ = io.ReadAll(r.Body)
			_, _ = rw.Write([]byte(`{"id":"g-1","threadId":"t-1"}`))
			return true
		case r.Method == http.MethodGet && r.URL.Path == "/gmail/v1/users/me/messages/g-1":
			_, _ = rw.Write([]byte(`{"id":"g-1","payload":{"headers":[{"name":"Message-ID","value":"<stamped-by-gmail@mail.gmail.com>"}]}}`))
			return true
		}
		return false
	}
	m := w.mailbox(KindGoogle, KindGoogle, func(m *mailboxRow, c *credentials) {
		m.Config = MailboxConfig{IMAPHost: "imap.gmail.com", IMAPPort: 993, IMAPTLS: TLSImplicit, OAuthEmail: "support@acme.test"}
		c.RefreshToken, c.AccessToken, c.Expiry = "refresh-0", "stale", time.Now().Add(-time.Hour)
	})
	tk, _ := firstTicket(t, w, m, customer())
	mid := w.agentReply(tk.ID, "Via Gmail", "")
	if err := w.s.sendMessage(w.ctx, w.wid, mid, false); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer access-2-1" || !bytes.Contains(uploaded, []byte("Subject: Re: Printer is on fire")) {
		t.Fatalf("upload auth=%q body=%q", auth, uploaded)
	}
	if f.tokenReq[0]["grant_type"] != "refresh_token" || f.tokenReq[0]["refresh_token"] != "refresh-0" || f.tokenReq[0]["scope"] != "" {
		t.Fatalf("refresh request %v", f.tokenReq[0])
	}
	if got := w.message(mid); got.Delivery != "sent" || *got.MsgID != "<stamped-by-gmail@mail.gmail.com>" {
		t.Fatalf("delivery %s %v", got.Delivery, *got.MsgID)
	}
	reloaded, _ := loadMailbox(w.ctx, w.d.DB.Pool, w.wid, m.ID)
	c, _ := w.s.openCreds(reloaded)
	if c.RefreshToken != "refresh-1" || c.AccessToken != "access-2-1" {
		t.Fatalf("rotated tokens not stored: %+v", c.RefreshToken)
	}
	if bytes.Contains(reloaded.CredentialsEnc, []byte("refresh-1")) {
		t.Fatal("credentials stored in the clear")
	}

	// A revoked grant fails the message permanently with a reconnect hint.
	mustExec(t, w.d, `UPDATE mailboxes SET credentials_enc = $2 WHERE id = $1`, m.ID, mustSeal(t, w, credentials{RefreshToken: "revoked"}))
	mid2 := w.agentReply(tk.ID, "again", "")
	_ = w.s.sendMessage(w.ctx, w.wid, mid2, false)
	if got := w.message(mid2); got.Delivery != "failed" || !strings.Contains(got.DelivError, "Reconnect") {
		t.Fatalf("revoked: %s %q", got.Delivery, got.DelivError)
	}
}

func TestSendMessageGraphMIME(t *testing.T) {
	w := newWorld(t)
	f := newFakeProvider(t)
	w.s.providers[KindMicrosoft].tokenURL = f.srv.URL + "/token"
	w.s.graphBase = f.srv.URL
	calls := 0
	var mimeBody []byte
	f.handler = func(rw http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodPost && r.URL.Path == "/me/sendMail" {
			calls++
			if calls == 1 {
				rw.WriteHeader(401) // token revoked mid-flight: refresh once and retry
				return true
			}
			if r.Header.Get("Content-Type") != "text/plain" {
				rw.WriteHeader(400)
				return true
			}
			b, _ := io.ReadAll(r.Body)
			mimeBody, _ = base64.StdEncoding.DecodeString(string(b))
			rw.WriteHeader(http.StatusAccepted)
			return true
		}
		return false
	}
	m := w.mailbox(KindMicrosoft, KindMicrosoft, func(m *mailboxRow, c *credentials) {
		m.Config = MailboxConfig{IMAPHost: "outlook.office365.com", IMAPPort: 993, IMAPTLS: TLSImplicit, OAuthEmail: "support@acme.test"}
		c.RefreshToken, c.AccessToken, c.Expiry = "refresh-0", "imap-token", time.Now().Add(time.Hour)
		c.GraphAccessToken, c.GraphExpiry = "graph-cached", time.Now().Add(time.Hour)
	})
	tk, _ := firstTicket(t, w, m, customer())
	mid := w.agentReply(tk.ID, "Via Graph", "")
	if err := w.s.sendMessage(w.ctx, w.wid, mid, false); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(f.tokenReq) != 1 {
		t.Fatalf("calls=%d token requests=%d", calls, len(f.tokenReq))
	}
	if f.tokenReq[0]["scope"] != "offline_access https://graph.microsoft.com/Mail.Send https://graph.microsoft.com/User.Read" {
		t.Fatalf("graph refresh must ask for the Graph resource only: %q", f.tokenReq[0]["scope"])
	}
	if !bytes.Contains(mimeBody, []byte("Message-ID: <m_"+mid.String())) {
		t.Fatalf("mime %q", mimeBody)
	}
	reloaded, _ := loadMailbox(w.ctx, w.d.DB.Pool, w.wid, m.ID)
	c, _ := w.s.openCreds(reloaded)
	if c.AccessToken != "imap-token" || c.GraphAccessToken != "access-2-1" || c.RefreshToken != "refresh-1" {
		t.Fatalf("per-resource tokens: imap=%q graph=%q", c.AccessToken, c.GraphAccessToken)
	}
	if w.message(mid).Delivery != "sent" {
		t.Fatal("not sent")
	}
}

// TestSendMessageReservedAddress: a reply to a reserved .invalid address (a test workspace's
// sample customer) is never handed to a provider, and reads as sent.
func TestSendMessageReservedAddress(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox(InboundForwarding, OutboundSystem, nil)
	called := false
	w.s.rawSend = func(context.Context, string, []string, []byte) error { called = true; return nil }
	tk, _ := firstTicket(t, w, m, "jordan@acme.example.invalid")
	var mid uuid.UUID
	err := w.d.DB.Tx(w.ctx, func(tx pgxTx) error {
		msg, err := domain.AddMessage(w.ctx, tx, w.d.Jobs, domain.NewMessage{WorkspaceID: w.wid, TicketID: tk.ID, Kind: "reply",
			Direction: "outbound", AuthorType: "user", AuthorUserID: &w.uid, DeliveryStatus: "pending", BodyHTML: "<p>Hi</p>", BodyText: "Hi"})
		mid = msg.ID
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.s.sendMessage(w.ctx, w.wid, mid, false); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("handed a reserved address to the transport")
	}
	if got := w.message(mid).Delivery; got != "sent" {
		t.Fatalf("delivery %s", got)
	}
}
