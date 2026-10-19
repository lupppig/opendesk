package collab

import (
	"errors"
	"io"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
	"github.com/google/uuid"
)

// An IMAP and SMTP account against go-imap's in-memory server and a go-smtp server, over
// loopback without TLS (allowed on a development instance only).

func startIMAP(t *testing.T) (addr string) {
	t.Helper()
	mem := imapmemserver.New()
	u := imapmemserver.NewUser("user@acme.test", "pw")
	if err := u.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	mem.AddUser(u)
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		Caps:         imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapMove: {}, imap.CapUIDPlus: {}},
		InsecureAuth: true,
		Logger:       discardLogger{},
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String()
}

type discardLogger struct{}

func (discardLogger) Printf(string, ...any) {}

type smtpBackend struct {
	mu   sync.Mutex
	from []string
	to   [][]string
	data []string
}

func (b *smtpBackend) NewSession(*smtp.Conn) (smtp.Session, error) { return &smtpSession{b: b}, nil }

type smtpSession struct {
	b      *smtpBackend
	authed bool
	from   string
	to     []string
}

func (s *smtpSession) AuthMechanisms() []string { return []string{sasl.Plain} }
func (s *smtpSession) Auth(string) (sasl.Server, error) {
	return sasl.NewPlainServer(func(_, user, pass string) error {
		if user != "user@acme.test" || pass != "pw" {
			return errors.New("bad login")
		}
		s.authed = true
		return nil
	}), nil
}
func (s *smtpSession) Mail(from string, _ *smtp.MailOptions) error {
	if !s.authed {
		return errors.New("login first")
	}
	s.from = from
	return nil
}
func (s *smtpSession) Rcpt(to string, _ *smtp.RcptOptions) error { s.to = append(s.to, to); return nil }
func (s *smtpSession) Data(r io.Reader) error {
	b, _ := io.ReadAll(r)
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	s.b.from, s.b.to, s.b.data = append(s.b.from, s.from), append(s.b.to, s.to), append(s.b.data, string(b))
	return nil
}
func (s *smtpSession) Reset()        {}
func (s *smtpSession) Logout() error { return nil }

func startSMTP(t *testing.T) (*smtpBackend, string) {
	t.Helper()
	be := &smtpBackend{}
	srv := smtp.NewServer(be)
	srv.Domain, srv.AllowInsecureAuth = "localhost", true
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return be, ln.Addr().String()
}

func hostPort(t *testing.T, addr string) (string, int) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range port {
		n = n*10 + int(c-'0')
	}
	return host, n
}

// mailServer is a direct client to the test IMAP server, standing in for a phone.
type mailServer struct {
	t *testing.T
	c *imapclient.Client
}

func dialServer(t *testing.T, addr string) *mailServer {
	t.Helper()
	c, err := imapclient.DialInsecure(addr, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Login("user@acme.test", "pw").Wait(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return &mailServer{t: t, c: c}
}

func (m *mailServer) create(name string) {
	if err := m.c.Create(name, nil).Wait(); err != nil {
		m.t.Fatal(err)
	}
}

func (m *mailServer) append(folder string, raw []byte, when time.Time, flags ...imap.Flag) {
	cmd := m.c.Append(folder, int64(len(raw)), &imap.AppendOptions{Flags: flags, Time: when})
	if _, err := cmd.Write(raw); err != nil {
		m.t.Fatal(err)
	}
	if err := cmd.Close(); err != nil {
		m.t.Fatal(err)
	}
	if _, err := cmd.Wait(); err != nil {
		m.t.Fatal(err)
	}
}

// find returns the UIDs in folder whose Message-ID is msgID, and their flags.
func (m *mailServer) find(folder, msgID string) ([]imap.UID, [][]imap.Flag) {
	if _, err := m.c.Select(folder, nil).Wait(); err != nil {
		m.t.Fatal(err)
	}
	msgs, err := m.c.Fetch(imap.SeqSet{{Start: 1, Stop: 0}}, &imap.FetchOptions{UID: true, Flags: true, Envelope: true}).Collect()
	if err != nil {
		m.t.Fatal(err)
	}
	var uids []imap.UID
	var flags [][]imap.Flag
	for _, x := range msgs {
		if x.Envelope != nil && x.Envelope.MessageID == msgID {
			uids, flags = append(uids, x.UID), append(flags, x.Flags)
		}
	}
	return uids, flags
}

func (m *mailServer) count(folder string) uint32 {
	d, err := m.c.Select(folder, nil).Wait()
	if err != nil {
		m.t.Fatal(err)
	}
	return d.NumMessages
}

func TestIMAPSync(t *testing.T) {
	w := newWorld(t)
	imapAddr := startIMAP(t)
	be, smtpAddr := startSMTP(t)
	ih, ip := hostPort(t, imapAddr)
	sh, sp := hostPort(t, smtpAddr)
	a := w.account(w.agent, ProviderIMAP, VisibilityPrivate, Audience{})
	cfg := accountConfig{IMAPHost: ih, IMAPPort: ip, IMAPTLS: "none", IMAPUsername: "user@acme.test",
		SMTPHost: sh, SMTPPort: sp, SMTPTLS: "none"}
	mustExec(t, w.d, `UPDATE collab_accounts SET config = $2 WHERE id = $1`, a.ID, cfg)
	a = w.reload(a)

	srv := dialServer(t, imapAddr)
	for _, f := range []string{"Sent", "Archive", "Trash", "Projects"} {
		srv.create(f)
	}
	now := time.Now()
	srv.append("INBOX", testMail{from: "Frank <frank@example.com>", to: a.Email, subject: "Visit", msgID: "v1@example.com", body: "When?"}.raw(), now.Add(-3*time.Hour))
	srv.append("Sent", testMail{from: a.Email, to: "frank@example.com", subject: "Re: Visit", msgID: "v2@acme.test", inReplyTo: "v1@example.com",
		refs: "<v1@example.com>", body: "Tuesday"}.raw(), now.Add(-2*time.Hour), imap.FlagSeen)
	both := testMail{from: "pm@example.com", to: a.Email, subject: "Plan", msgID: "p1@example.com", body: "The plan"}.raw()
	srv.append("INBOX", both, now.Add(-time.Hour))
	srv.append("Projects", both, now.Add(-time.Hour))
	srv.append("INBOX", testMail{from: "old@example.com", to: a.Email, subject: "Old", msgID: "old@example.com", body: "Long ago"}.raw(), now.Add(-60*24*time.Hour))

	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if a = w.reload(a); a.Status != "active" {
		t.Fatalf("status %s: %s", a.Status, a.LastError)
	}
	inbox, sent, archive, trash, projects := w.folderByRemote(a, "INBOX"), w.folderByRemote(a, "Sent"), w.folderByRemote(a, "Archive"),
		w.folderByRemote(a, "Trash"), w.folderByRemote(a, "Projects")
	if *inbox.Role != RoleInbox || *sent.Role != RoleSent || *archive.Role != RoleArchive || *trash.Role != RoleTrash || projects.Role != nil {
		t.Fatal("folder roles by name")
	}
	if n := w.count(`SELECT count(*) FROM collab_folders WHERE account_id = $1 AND remote_id IN ('SENT', 'ARCHIVE', 'SPAM', 'DRAFTS')`, a.ID); n != 0 {
		t.Fatal("folders the server does not have are kept")
	}
	// Threading by In-Reply-To; the message in two folders is one message.
	v1, v2 := w.messageByRemote(a, "v1@example.com"), w.messageByRemote(a, "v2@acme.test")
	if v1.ThreadID != v2.ThreadID || !v1.Unread || v2.Unread {
		t.Fatalf("visit thread: %+v / %+v", v1, v2)
	}
	p1 := w.messageByRemote(a, "p1@example.com")
	if len(p1.FolderIDs) != 2 || !sliceHas(p1.FolderIDs, inbox.ID) || !sliceHas(p1.FolderIDs, projects.ID) || len(parseLocation(p1.Location)) != 2 {
		t.Fatalf("p1: %+v %s", p1.FolderIDs, p1.Location)
	}
	if n := w.count(`SELECT count(*) FROM collab_messages WHERE account_id = $1 AND remote_id = 'old@example.com'`, a.ID); n != 0 {
		t.Fatal("mail older than the window was imported")
	}

	// Changes on the phone: read, moved, a new reply; then expunged everywhere.
	uids, _ := srv.find("INBOX", "v1@example.com")
	if err := srv.c.Store(imap.UIDSetNum(uids...), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagSeen}}, nil).Close(); err != nil {
		t.Fatal(err)
	}
	uids, _ = srv.find("Projects", "p1@example.com")
	if _, err := srv.c.Move(imap.UIDSetNum(uids...), "Trash").Wait(); err != nil {
		t.Fatal(err)
	}
	srv.append("INBOX", testMail{from: "frank@example.com", to: a.Email, subject: "Re: Visit", msgID: "v3@example.com", inReplyTo: "v2@acme.test",
		refs: "<v1@example.com> <v2@acme.test>", body: "Great"}.raw(), now)
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if w.messageByRemote(a, "v1@example.com").Unread {
		t.Fatal("read on the phone is still unread")
	}
	p1 = w.messageByRemote(a, "p1@example.com")
	if !sliceHas(p1.FolderIDs, trash.ID) || sliceHas(p1.FolderIDs, projects.ID) || p1.ID == uuid.Nil {
		t.Fatalf("moved on the phone: %+v", p1.FolderIDs)
	}
	v3 := w.messageByRemote(a, "v3@example.com")
	if v3.ThreadID != v1.ThreadID {
		t.Fatal("the reply is in another conversation")
	}
	for _, f := range []string{"INBOX", "Trash"} {
		uids, _ = srv.find(f, "p1@example.com")
		if err := srv.c.Store(imap.UIDSetNum(uids...), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); err != nil {
			t.Fatal(err)
		}
		if err := srv.c.Expunge().Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if n := w.count(`SELECT count(*) FROM collab_messages WHERE account_id = $1 AND remote_id = 'p1@example.com'`, a.ID); n != 0 {
		t.Fatal("a message gone from every folder is kept")
	}

	// Archive: received mail moves to Archive, the sent copy stays in Sent.
	w.act(a, []uuid.UUID{v1.ThreadID}, ActionArchive, nil)
	if uids, _ := srv.find("Archive", "v1@example.com"); len(uids) != 1 {
		t.Fatal("v1 not archived at the server")
	}
	if uids, _ := srv.find("INBOX", "v3@example.com"); len(uids) != 0 {
		t.Fatal("v3 still in the server's INBOX")
	}
	if uids, _ := srv.find("Sent", "v2@acme.test"); len(uids) != 1 {
		t.Fatal("the sent copy moved")
	}
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if m := w.messageByRemote(a, "v1@example.com"); len(m.FolderIDs) != 1 || m.FolderIDs[0] != archive.ID {
		t.Fatalf("after archive and sync: %+v %s", m.FolderIDs, m.Location)
	}
	// Star flags the newest message at the server.
	w.act(a, []uuid.UUID{v1.ThreadID}, ActionStar, nil)
	if _, flags := srv.find("Archive", "v3@example.com"); len(flags) != 1 || !slices.Contains(flags[0], imap.FlagFlagged) {
		t.Fatalf("star at the server: %v", flags)
	}

	// Send over SMTP, then a \Seen copy in Sent; the next sync does not duplicate it.
	prov, err := w.s.provider(w.ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	raw := testMail{from: a.Email, to: "frank@example.com", subject: "Re: Visit", msgID: "v4@acme.test", inReplyTo: "v3@example.com", body: "See you"}.raw()
	ref, err := prov.Send(w.ctx, raw, SendInfo{From: a.Email, Recipients: []string{"frank@example.com", "boss@example.com"}})
	prov.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(be.data) != 1 || strings.Join(be.to[0], ",") != "frank@example.com,boss@example.com" || be.from[0] != a.Email {
		t.Fatalf("smtp: %v %v", be.from, be.to)
	}
	if uids, flags := srv.find("Sent", "v4@acme.test"); len(uids) != 1 || !slices.Contains(flags[0], imap.FlagSeen) {
		t.Fatal("no \\Seen copy in Sent")
	}
	if ref.RemoteID != "v4@acme.test" || len(parseLocation(ref.Location)) != 1 {
		t.Fatalf("ref: %+v", ref)
	}
	if _, err := w.s.storeSent(w.ctx, a, raw, ref, w.agent.UserID, &v1.ThreadID); err != nil {
		t.Fatal(err)
	}
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if n := w.count(`SELECT count(*) FROM collab_messages WHERE account_id = $1 AND message_id = 'v4@acme.test'`, a.ID); n != 1 {
		t.Fatalf("%d copies of the sent message", n)
	}
	if m := w.messageByRemote(a, "v4@acme.test"); m.ThreadID != v1.ThreadID || m.SentBy == nil {
		t.Fatalf("sent: %+v", m)
	}

	// A pass without changes writes and publishes nothing.
	events := func() int {
		return w.count(`SELECT count(*) FROM events WHERE workspace_id = $1 AND type LIKE 'collab.%'`, w.wid)
	}
	before := events()
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if after := events(); after != before {
		t.Fatalf("a quiet pass published %d events", after-before)
	}

	// Raw source of a message without a stored raw comes from the server.
	got, err := w.s.loadRaw(w.ctx, a, w.messageByRemote(a, "v3@example.com"), "")
	if err != nil || !strings.Contains(string(got), "Great") {
		t.Fatalf("loadRaw: %v", err)
	}

	// A refused password needs a reconnect; the connection test says why, safely.
	err = w.s.testIMAP(w.ctx, cfg, a.Email, credentials{Password: "wrong"})
	if err == nil || !isReconnect(err) || strings.Contains(err.Error(), "wrong") {
		t.Fatalf("wrong password: %v", err)
	}
	if err := w.s.testIMAP(w.ctx, cfg, "user@acme.test", credentials{Password: "pw"}); err != nil {
		t.Fatalf("test: %v", err)
	}
}

func TestIMAPFolderRoles(t *testing.T) {
	list := []*imap.ListData{
		{Mailbox: "INBOX", Delim: '.'},
		{Mailbox: "INBOX.Sent Items", Delim: '.'},
		{Mailbox: "Bin", Delim: '.'},
		{Mailbox: "[Gmail]", Delim: '.', Attrs: []imap.MailboxAttr{imap.MailboxAttrNoSelect}},
		{Mailbox: "Mail.Junk", Delim: '.', Attrs: []imap.MailboxAttr{"\\junk"}},
		{Mailbox: "Spam", Delim: '.'},
		{Mailbox: "Clients.Acme", Delim: '.'},
	}
	got := imapFolders(list)
	roles := map[string]string{}
	for _, f := range got {
		roles[f.RemoteID] = f.Role
	}
	if roles["INBOX"] != RoleInbox || roles["INBOX.Sent Items"] != RoleSent || roles["Bin"] != RoleTrash || roles["Mail.Junk"] != RoleSpam ||
		roles["Spam"] != "" || roles["Clients.Acme"] != "" {
		t.Fatalf("roles: %v", roles)
	}
	if _, ok := roles["[Gmail]"]; ok {
		t.Fatal("a \\Noselect folder is listed")
	}
	for _, f := range got {
		if f.RemoteID == "Clients.Acme" && (f.Path != "Clients/Acme" || f.Name != "Acme") {
			t.Fatalf("path: %+v", f)
		}
	}
}
