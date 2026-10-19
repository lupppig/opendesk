package collab

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/email"
)

// IMAP accounts (contracts/collab.md "Sync"): folders by SPECIAL-USE (else by name), per
// folder UIDVALIDITY, the last UID seen and HIGHESTMODSEQ when the server has CONDSTORE.
// A message is identified by its Message-ID (else a hash of its content), so the same
// message in two folders, or moved between them, is one stored message with a location
// per folder (collab_messages.location: {"imap": [{"f": folder, "v": uidvalidity, "u": uid}]}).
// Every dial goes through the network guard.

// imapError is an IMAP or SMTP failure with a message that is safe to store and show.
type imapError struct {
	msg       string
	auth      bool // the login was refused: reconnect
	permanent bool
}

func (e *imapError) Error() string { return e.msg }

type imapLoc struct {
	Folder   string `json:"f"`
	Validity uint32 `json:"v"`
	UID      uint32 `json:"u"`
}

type imapLocation struct {
	IMAP []imapLoc `json:"imap"`
}

func parseLocation(raw json.RawMessage) []imapLoc {
	var l imapLocation
	_ = json.Unmarshal(raw, &l)
	return l.IMAP
}

func encodeLocation(locs []imapLoc) json.RawMessage {
	if locs == nil {
		locs = []imapLoc{}
	}
	slices.SortFunc(locs, func(a, b imapLoc) int {
		if c := strings.Compare(a.Folder, b.Folder); c != 0 {
			return c
		}
		return int(int64(a.UID) - int64(b.UID))
	})
	b, _ := json.Marshal(imapLocation{IMAP: locs})
	return b
}

// imapFolderState is a folder's cursor (collab_folders.sync_state).
type imapFolderState struct {
	Validity uint32 `json:"v,omitempty"`
	LastUID  uint32 `json:"u,omitempty"`
	ModSeq   uint64 `json:"m,omitempty"`
}

// imapConn is one authenticated IMAP session.
type imapConn struct {
	c    *imapclient.Client
	stop func()
}

func (c *imapConn) close() {
	if c == nil || c.c == nil {
		return
	}
	_ = c.c.Logout().Wait()
	_ = c.c.Close()
	c.stop()
}

// dialIMAP connects with TLS (implicit or STARTTLS, verified, TLS 1.2+; "none" only on a
// development instance) through the guard and logs in.
func (s *service) dialIMAP(ctx context.Context, cfg accountConfig, username, password string) (*imapConn, error) {
	if !s.d.Cfg.CollabIMAP {
		return nil, &imapError{msg: "IMAP accounts are not available on this instance.", permanent: true}
	}
	host, port, mode := cfg.IMAPHost, cfg.IMAPPort, cfg.IMAPTLS
	if host == "" || port <= 0 || port > 65535 {
		return nil, &imapError{msg: "The IMAP server is not configured.", permanent: true}
	}
	if mode == "none" && !s.guard.AllowPrivate() {
		return nil, &imapError{msg: "The IMAP connection must use TLS.", permanent: true}
	}
	raw, err := s.guard.Dial(ctx, host, port)
	if err != nil {
		return nil, dialError("IMAP", err)
	}
	conn, stop := email.WithDeadline(ctx, raw, sessionTimeout)
	fail := func(c interface{ Close() error }, e error) (*imapConn, error) {
		_ = c.Close()
		stop()
		return nil, e
	}
	var c *imapclient.Client
	switch mode {
	case "none":
		c = imapclient.New(conn, nil)
	case "starttls":
		c, err = imapclient.NewStartTLS(conn, &imapclient.Options{TLSConfig: s.guard.TLSConfig(host)})
		if err != nil {
			return fail(conn, &imapError{msg: "Could not start TLS with the IMAP server: " + tlsReason(err) + "."})
		}
	default:
		tc := tls.Client(conn, s.guard.TLSConfig(host))
		if err := tc.HandshakeContext(ctx); err != nil {
			return fail(conn, &imapError{msg: "Could not establish TLS with the IMAP server: " + tlsReason(err) + "."})
		}
		c = imapclient.New(tc, nil)
	}
	if err := c.WaitGreeting(); err != nil {
		return fail(c, &imapError{msg: "The IMAP server did not answer."})
	}
	if c.Caps().Has(imap.CapLoginDisabled) {
		return fail(c, &imapError{msg: "The IMAP server does not allow password login on this connection.", permanent: true})
	}
	if err := c.Login(username, password).Wait(); err != nil {
		return fail(c, imapLoginError(err))
	}
	return &imapConn{c: c, stop: stop}, nil
}

func imapLoginError(err error) error {
	var ie *imap.Error
	if errors.As(err, &ie) {
		text := serverText(ie.Text)
		switch ie.Code {
		case imap.ResponseCodeUnavailable, imap.ResponseCodeLimit:
			return &imapError{msg: "The IMAP server refused the login for now: " + text}
		}
		return &imapError{msg: "The IMAP server refused the login: " + text, auth: true}
	}
	if isTimeout(err) {
		return &imapError{msg: "The IMAP server stopped answering during login."}
	}
	return &imapError{msg: "The IMAP connection dropped during login."}
}

func dialError(label string, err error) error {
	switch {
	case errors.Is(err, email.ErrBlockedAddress):
		return &imapError{msg: "The " + label + " server address is not allowed (it must be a public address).", permanent: true}
	case isTimeout(err):
		return &imapError{msg: "Timed out connecting to the " + label + " server."}
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return &imapError{msg: "The " + label + " server host name does not resolve."}
	}
	return &imapError{msg: "Could not connect to the " + label + " server."}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func tlsReason(err error) string {
	var cv *tls.CertificateVerificationError
	var rh tls.RecordHeaderError
	switch {
	case errors.As(err, &cv):
		return "the certificate is not valid for this host"
	case errors.As(err, &rh):
		return "the server did not speak TLS on this port"
	case isTimeout(err):
		return "timed out"
	}
	return "handshake failed"
}

// opError wraps an IMAP command failure with a safe message.
func opError(what string, err error) error {
	var ie *imap.Error
	if errors.As(err, &ie) {
		return &imapError{msg: "The IMAP server refused " + what + ": " + serverText(ie.Text)}
	}
	if isTimeout(err) {
		return &imapError{msg: "The IMAP server stopped answering during " + what + "."}
	}
	return &imapError{msg: "The IMAP connection dropped during " + what + "."}
}

// imapProvider is an IMAP and SMTP account. It dials when first used; Close logs out.
type imapProvider struct {
	s     *service
	a     *accountRow
	creds credentials
	conn  *imapConn
	delim rune
}

func (s *service) newIMAPProvider(a *accountRow) (*imapProvider, error) {
	creds, err := s.openCreds(a)
	if err != nil {
		return nil, err
	}
	return &imapProvider{s: s, a: a, creds: creds}, nil
}

func (p *imapProvider) session(ctx context.Context) (*imapClientConn, error) {
	if p.conn == nil {
		user := p.a.Config.IMAPUsername
		if user == "" {
			user = p.a.Email
		}
		c, err := p.s.dialIMAP(ctx, p.a.Config, user, p.creds.Password)
		if err != nil {
			return nil, err
		}
		p.conn = c
	}
	return p.conn.c, nil
}

type imapClientConn = imapclient.Client

func (p *imapProvider) Close() {
	p.conn.close()
	p.conn = nil
}

// delimiter is the server's hierarchy separator.
func (p *imapProvider) delimiter(ctx context.Context) (rune, error) {
	if p.delim != 0 {
		return p.delim, nil
	}
	c, err := p.session(ctx)
	if err != nil {
		return 0, err
	}
	list, err := c.List("", "", nil).Collect()
	if err != nil {
		return 0, opError("listing folders", err)
	}
	p.delim = '/'
	if len(list) > 0 && list[0].Delim != 0 {
		p.delim = list[0].Delim
	}
	return p.delim, nil
}

var imapSpecial = map[imap.MailboxAttr]string{
	imap.MailboxAttrSent: RoleSent, imap.MailboxAttrDrafts: RoleDrafts, imap.MailboxAttrTrash: RoleTrash,
	imap.MailboxAttrJunk: RoleSpam, imap.MailboxAttrArchive: RoleArchive, imap.MailboxAttrAll: RoleAll,
	imap.MailboxAttrFlagged: RoleStarred, imap.MailboxAttrImportant: RoleImportant,
}

// imapNames are the folder names that take a role when no folder has it by SPECIAL-USE.
var imapNames = map[string]string{
	"sent": RoleSent, "sent items": RoleSent, "sent mail": RoleSent, "sent messages": RoleSent,
	"drafts": RoleDrafts, "draft": RoleDrafts,
	"trash": RoleTrash, "deleted items": RoleTrash, "deleted messages": RoleTrash, "bin": RoleTrash,
	"junk": RoleSpam, "spam": RoleSpam, "junk e-mail": RoleSpam, "junk email": RoleSpam, "bulk mail": RoleSpam,
	"archive": RoleArchive, "archives": RoleArchive,
}

func (p *imapProvider) Folders(ctx context.Context) ([]RemoteFolder, error) {
	c, err := p.session(ctx)
	if err != nil {
		return nil, err
	}
	var opts *imap.ListOptions
	if c.Caps().Has(imap.CapSpecialUse) {
		opts = &imap.ListOptions{ReturnSpecialUse: true}
	}
	list, err := c.List("", "*", opts).Collect()
	if err != nil {
		return nil, opError("listing folders", err)
	}
	return imapFolders(list), nil
}

// imapFolders maps LIST answers to folders: INBOX, the SPECIAL-USE roles, then names.
func imapFolders(list []*imap.ListData) []RemoteFolder {
	out := []RemoteFolder{}
	has := map[string]bool{}
	for _, l := range list {
		if l == nil || slices.Contains(l.Attrs, imap.MailboxAttrNoSelect) || slices.Contains(l.Attrs, imap.MailboxAttrNonExistent) {
			continue
		}
		path := l.Mailbox
		if l.Delim != 0 && l.Delim != '/' {
			path = strings.ReplaceAll(path, string(l.Delim), "/")
		}
		f := RemoteFolder{RemoteID: l.Mailbox, Name: lastSegment(path, "/"), Path: path, Kind: KindFolder}
		if strings.EqualFold(l.Mailbox, "INBOX") {
			f.Role, f.Name, f.Path = RoleInbox, "Inbox", "Inbox"
		}
		for _, attr := range l.Attrs {
			for special, role := range imapSpecial {
				if f.Role == "" && strings.EqualFold(string(attr), string(special)) {
					f.Role = role
				}
			}
		}
		if f.Role != "" {
			has[f.Role] = true
		}
		out = append(out, f)
	}
	for i := range out {
		if out[i].Role != "" {
			continue
		}
		name := strings.ToLower(out[i].Name)
		if role, ok := imapNames[name]; ok && !has[role] && (!strings.Contains(out[i].Path, "/") || strings.HasPrefix(strings.ToLower(out[i].Path), "inbox/")) {
			out[i].Role = role
			has[role] = true
		}
	}
	sortFolders(out)
	return out
}

func (p *imapProvider) mailboxName(ctx context.Context, name string, parent *RemoteFolder) (string, error) {
	if parent == nil {
		return name, nil
	}
	d, err := p.delimiter(ctx)
	if err != nil {
		return "", err
	}
	return parent.RemoteID + string(d) + name, nil
}

func (p *imapProvider) CreateFolder(ctx context.Context, name string, parent *RemoteFolder) (RemoteFolder, error) {
	c, err := p.session(ctx)
	if err != nil {
		return RemoteFolder{}, err
	}
	full, err := p.mailboxName(ctx, name, parent)
	if err != nil {
		return RemoteFolder{}, err
	}
	if err := c.Create(full, nil).Wait(); err != nil && !alreadyExists(err) {
		return RemoteFolder{}, opError("creating the folder", err)
	}
	path := name
	if parent != nil {
		path = parent.Path + "/" + name
	}
	return RemoteFolder{RemoteID: full, Name: name, Path: path, Kind: KindFolder}, nil
}

func alreadyExists(err error) bool {
	var ie *imap.Error
	return errors.As(err, &ie) && (ie.Code == imap.ResponseCodeAlreadyExists || strings.Contains(strings.ToLower(ie.Text), "exist"))
}

func (p *imapProvider) RenameFolder(ctx context.Context, f RemoteFolder, name string) (RemoteFolder, error) {
	c, err := p.session(ctx)
	if err != nil {
		return RemoteFolder{}, err
	}
	d, err := p.delimiter(ctx)
	if err != nil {
		return RemoteFolder{}, err
	}
	full := name
	if i := strings.LastIndex(f.RemoteID, string(d)); i >= 0 {
		full = f.RemoteID[:i+1] + name
	}
	if err := c.Rename(f.RemoteID, full, nil).Wait(); err != nil {
		return RemoteFolder{}, opError("renaming the folder", err)
	}
	out := RemoteFolder{RemoteID: full, Name: name, Path: name, Kind: KindFolder}
	if i := strings.LastIndex(f.Path, "/"); i >= 0 {
		out.Path = f.Path[:i+1] + name
	}
	return out, nil
}

func (p *imapProvider) DeleteFolder(ctx context.Context, f RemoteFolder) error {
	c, err := p.session(ctx)
	if err != nil {
		return err
	}
	if err := c.Delete(f.RemoteID).Wait(); err != nil {
		var ie *imap.Error
		if errors.As(err, &ie) && ie.Code == imap.ResponseCodeNonExistent {
			return nil
		}
		return opError("deleting the folder", err)
	}
	return nil
}

func (p *imapProvider) FetchRaw(ctx context.Context, ref MessageRef) ([]byte, error) {
	locs := parseLocation(ref.Location)
	if len(locs) == 0 {
		return nil, &providerError{msg: "The message is no longer on the IMAP server.", notFound: true, permanent: true}
	}
	c, err := p.session(ctx)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, loc := range locs {
		sel, err := c.Select(loc.Folder, &imap.SelectOptions{ReadOnly: true}).Wait()
		if err != nil {
			lastErr = opError("opening a folder", err)
			continue
		}
		if sel.UIDValidity != loc.Validity {
			continue
		}
		raw, err := fetchBody(c, loc.UID)
		if err != nil {
			lastErr = err
			continue
		}
		if raw != nil {
			return raw, nil
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, &providerError{msg: "The message is no longer on the IMAP server.", notFound: true, permanent: true}
}

// fetchBody reads one message of the selected folder (nil when it is gone).
func fetchBody(c *imapclient.Client, uid uint32) ([]byte, error) {
	msgs, err := c.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{UID: true,
		BodySection: []*imap.FetchItemBodySection{{Peek: true}}}).Collect()
	if err != nil {
		return nil, opError("fetching a message", err)
	}
	for _, m := range msgs {
		if uint32(m.UID) == uid && len(m.BodySection) > 0 {
			return m.BodySection[0].Bytes, nil
		}
	}
	return nil, nil
}

// Apply makes an action at the IMAP server (contracts/collab.md "Actions").
func (p *imapProvider) Apply(ctx context.Context, op ApplyOp) error {
	if op.Action == ActionLabel || op.Action == ActionUnlabel {
		return errLabels
	}
	c, err := p.session(ctx)
	if err != nil {
		return err
	}
	byFolder := map[string][]uint32{}
	folders := []string{}
	for _, m := range op.Messages {
		for _, loc := range parseLocation(m.Location) {
			if _, ok := byFolder[loc.Folder]; !ok {
				folders = append(folders, loc.Folder)
			}
			byFolder[loc.Folder] = append(byFolder[loc.Folder], loc.UID)
		}
	}
	var store *imap.StoreFlags
	switch op.Action {
	case ActionRead:
		store = &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagSeen}}
	case ActionUnread:
		store = &imap.StoreFlags{Op: imap.StoreFlagsDel, Silent: true, Flags: []imap.Flag{imap.FlagSeen}}
	case ActionStar:
		store = &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagFlagged}}
	case ActionUnstar:
		store = &imap.StoreFlags{Op: imap.StoreFlagsDel, Silent: true, Flags: []imap.Flag{imap.FlagFlagged}}
	}
	if store != nil {
		for _, f := range folders {
			if _, err := c.Select(f, nil).Wait(); err != nil {
				if isNonExistent(err) {
					continue
				}
				return opError("opening a folder", err)
			}
			if err := c.Store(uidSet(byFolder[f]), store, nil).Close(); err != nil {
				return opError("changing flags", err)
			}
		}
		return nil
	}
	target, err := moveTarget(op)
	if err != nil {
		return err
	}
	if op.Action == ActionArchive {
		// A server without an Archive folder gets one.
		if err := c.Create(target.RemoteID, nil).Wait(); err != nil && !alreadyExists(err) {
			return opError("creating the Archive folder", err)
		}
	}
	// Mail stays in Sent and Drafts; everything else of the conversation moves.
	stay := map[string]bool{}
	for role, f := range op.Special {
		if role == RoleSent || role == RoleDrafts {
			stay[f.RemoteID] = true
		}
	}
	for _, f := range folders {
		if f == target.RemoteID || stay[f] {
			continue
		}
		if _, err := c.Select(f, nil).Wait(); err != nil {
			if isNonExistent(err) {
				continue
			}
			return opError("opening a folder", err)
		}
		if _, err := c.Move(uidSet(byFolder[f]), target.RemoteID).Wait(); err != nil {
			return opError("moving messages", err)
		}
	}
	return nil
}

func isNonExistent(err error) bool {
	var ie *imap.Error
	return errors.As(err, &ie) && ie.Code == imap.ResponseCodeNonExistent
}

func uidSet(uids []uint32) imap.UIDSet {
	var set imap.UIDSet
	for _, u := range uids {
		set.AddNum(imap.UID(u))
	}
	return set
}

// Send submits through SMTP (Bcc in the envelope only, never a header), then appends a
// copy to Sent with \Seen, Bcc kept.
func (p *imapProvider) Send(ctx context.Context, raw []byte, info SendInfo) (SentRef, error) {
	if err := p.s.sendSMTP(ctx, p.a.Config, p.a.Email, p.creds, info, withoutBcc(raw)); err != nil {
		return SentRef{}, err
	}
	pm, err := email.ParseMail(raw)
	ref := SentRef{}
	if err == nil {
		ref.RemoteID = imapRemoteID(pm.MessageID, raw)
	} else {
		ref.RemoteID = imapRemoteID("", raw)
	}
	sent, err := scanFolder(p.s.d.DB.Pool.QueryRow(ctx, `SELECT `+folderColumns+` FROM collab_folders WHERE account_id = $1 AND role = 'sent'
		ORDER BY path LIMIT 1`, p.a.ID))
	if err != nil {
		return ref, nil // no Sent folder: the message went out all the same
	}
	ref.FolderRemoteIDs = []string{sent.RemoteID}
	c, err := p.session(ctx)
	if err != nil {
		p.s.log.Info("collab sent copy not appended", "account_id", p.a.ID, "reason", safeError(err))
		return ref, nil
	}
	cmd := c.Append(sent.RemoteID, int64(len(raw)), &imap.AppendOptions{Flags: []imap.Flag{imap.FlagSeen}, Time: time.Now()})
	_, werr := cmd.Write(raw)
	cerr := cmd.Close()
	data, err := cmd.Wait()
	if werr != nil || cerr != nil || err != nil {
		p.s.log.Info("collab sent copy not appended", "account_id", p.a.ID)
		return ref, nil
	}
	if data != nil && data.UID != 0 {
		ref.Location = encodeLocation([]imapLoc{{Folder: sent.RemoteID, Validity: data.UIDValidity, UID: uint32(data.UID)}})
	}
	return ref, nil
}

// syncIMAP runs one pass over the account's folders (at most maxIMAPFolders, the
// special ones first; the virtual All, Flagged and Important folders are not read).
func (s *service) syncIMAP(ctx context.Context, p *pass, ip *imapProvider) error {
	c, err := ip.session(ctx)
	if err != nil {
		return err
	}
	folders := []*folderRow{}
	for _, f := range p.folders {
		if f.Role != nil && (*f.Role == RoleAll || *f.Role == RoleStarred || *f.Role == RoleImportant) {
			continue
		}
		folders = append(folders, f)
	}
	slices.SortFunc(folders, func(a, b *folderRow) int { return compareFolders(a.remote(), b.remote()) })
	if len(folders) > maxIMAPFolders {
		folders = folders[:maxIMAPFolders]
	}
	// Mail in a folder deleted at the server went with it.
	for _, gone := range p.removedFolders {
		known, err := p.imapKnown(ctx, gone)
		if err != nil {
			return err
		}
		err = p.batch(ctx, func(tx pgx.Tx) error {
			for _, m := range known {
				if err := p.setLocs(ctx, tx, m, dropFolder(parseLocation(m.Location), gone)); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	p.removedFolders = nil
	condstore := c.Caps().Has(imap.CapCondStore)
	budget := initialMax
	first := p.a.LastSyncedAt == nil
	for _, f := range folders {
		if err := ctx.Err(); err != nil {
			return err
		}
		limit := initialMax
		if first {
			limit = budget
		}
		used, err := s.syncIMAPFolder(ctx, p, c, f, condstore, limit)
		if err != nil {
			return err
		}
		budget = max(budget-used, 0)
	}
	return p.batch(ctx, func(tx pgx.Tx) error { return p.removeOrphans(ctx, tx) })
}

// imapMeta is what a FETCH told us about one message of a folder.
type imapMeta struct {
	uid       uint32
	messageID string
	flags     []imap.Flag
	size      int64
	date      time.Time
}

func metaOf(m *imapclient.FetchMessageBuffer) imapMeta {
	out := imapMeta{uid: uint32(m.UID), flags: m.Flags, size: m.RFC822Size, date: m.InternalDate}
	if m.Envelope != nil {
		out.messageID = m.Envelope.MessageID
	}
	return out
}

// syncIMAPFolder syncs one folder and returns how many new messages it imported. A new
// folder, or one whose UIDVALIDITY changed, starts over with the last 30 days (at most
// limit messages).
func (s *service) syncIMAPFolder(ctx context.Context, p *pass, c *imapclient.Client, f *folderRow, condstore bool, limit int) (int, error) {
	sel, err := c.Select(f.RemoteID, &imap.SelectOptions{ReadOnly: true, CondStore: condstore}).Wait()
	if err != nil {
		if isNonExistent(err) {
			return 0, nil // gone since the folder list; the next pass drops it
		}
		return 0, opError("opening a folder", err)
	}
	var st imapFolderState
	_ = json.Unmarshal(f.SyncState, &st)
	known, err := p.imapKnown(ctx, f.RemoteID)
	if err != nil {
		return 0, err
	}
	initial := st.Validity == 0 || st.Validity != sel.UIDValidity
	if initial {
		// Every stored UID of this folder is meaningless now.
		err := p.batch(ctx, func(tx pgx.Tx) error {
			for _, m := range known {
				if err := p.setLocs(ctx, tx, m, dropFolder(parseLocation(m.Location), f.RemoteID)); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return 0, err
		}
		known = map[uint32]*storedMsg{}
		st = imapFolderState{Validity: sel.UIDValidity}
	}

	// New messages.
	var uids []uint32
	if sel.NumMessages > 0 {
		if initial {
			data, err := c.UIDSearch(&imap.SearchCriteria{Since: time.Now().Add(-initialWindow)}, nil).Wait()
			if err != nil {
				return 0, opError("searching a folder", err)
			}
			for _, u := range data.AllUIDs() {
				uids = append(uids, uint32(u))
			}
			slices.Sort(uids)
			if len(uids) > limit {
				uids = uids[len(uids)-limit:]
			}
		} else if sel.UIDNext == 0 || uint32(sel.UIDNext) > st.LastUID+1 {
			var set imap.UIDSet
			set.AddRange(imap.UID(st.LastUID+1), 0)
			msgs, err := c.Fetch(set, &imap.FetchOptions{UID: true}).Collect()
			if err != nil {
				return 0, opError("listing new messages", err)
			}
			for _, m := range msgs {
				// "n:*" always answers the newest message, even below n.
				if uint32(m.UID) > st.LastUID {
					uids = append(uids, uint32(m.UID))
				}
			}
			slices.Sort(uids)
			uids = slices.Compact(uids)
			// The oldest first, at most initialMax a pass; the cursor goes on from there.
			if len(uids) > initialMax {
				uids = uids[:initialMax]
			}
		}
	}
	imported := 0
	for len(uids) > 0 {
		n := min(len(uids), syncBatch)
		chunk := uids[:n]
		uids = uids[n:]
		got, err := s.imapImport(ctx, p, c, f, sel.UIDValidity, chunk, &st)
		if err != nil {
			return imported, err
		}
		imported += got
	}
	if initial && sel.UIDNext > 0 && uint32(sel.UIDNext)-1 > st.LastUID {
		// Older mail outside the window is not imported.
		st.LastUID = uint32(sel.UIDNext) - 1
	}

	// Flags of known messages, and the ones gone from the folder.
	if !initial && len(known) > 0 && !(condstore && st.ModSeq != 0 && sel.HighestModSeq == st.ModSeq) {
		if err := s.imapRefresh(ctx, p, c, f, sel.UIDValidity, known, condstore, st.ModSeq); err != nil {
			return imported, err
		}
	}
	if condstore {
		st.ModSeq = sel.HighestModSeq
	}
	b, _ := json.Marshal(st)
	if _, err := s.d.DB.Pool.Exec(ctx, `UPDATE collab_folders SET sync_state = $2 WHERE id = $1`, f.ID, b); err != nil {
		return imported, err
	}
	return imported, nil
}

// imapImport stores a chunk of new UIDs of the folder: a message already stored (the
// same Message-ID in another folder) gains this location; others are fetched and stored.
// The folder's cursor advances in the same transaction.
func (s *service) imapImport(ctx context.Context, p *pass, c *imapclient.Client, f *folderRow, validity uint32, chunk []uint32, st *imapFolderState) (int, error) {
	msgs, err := c.Fetch(uidSet(chunk), &imap.FetchOptions{UID: true, Flags: true, Envelope: true, RFC822Size: true, InternalDate: true}).Collect()
	if err != nil {
		return 0, opError("listing new messages", err)
	}
	metas := []imapMeta{}
	for _, m := range msgs {
		metas = append(metas, metaOf(m))
	}
	slices.SortFunc(metas, func(a, b imapMeta) int { return int(int64(a.uid) - int64(b.uid)) })
	remoteIDs := []string{}
	for _, m := range metas {
		if id := imapRemoteID(m.messageID, nil); id != "" {
			remoteIDs = append(remoteIDs, id)
		}
	}
	stored, err := p.lookupMany(ctx, s.d.DB.Pool, remoteIDs)
	if err != nil {
		return 0, err
	}
	raws := map[uint32][]byte{}
	for _, m := range metas {
		if id := imapRemoteID(m.messageID, nil); id != "" && stored[id] != nil {
			continue
		}
		if m.size > maxRawBytes {
			s.log.Info("collab skipping oversized message", "account_id", p.a.ID)
			continue
		}
		raw, err := fetchBody(c, m.uid)
		if err != nil {
			return 0, err
		}
		if raw != nil {
			raws[m.uid] = raw
		}
	}
	before := p.imported
	err = p.batch(ctx, func(tx pgx.Tx) error {
		for _, m := range metas {
			loc := imapLoc{Folder: f.RemoteID, Validity: validity, UID: m.uid}
			remote := imapRemoteID(m.messageID, raws[m.uid])
			if remote == "" {
				continue
			}
			if sm := stored[remote]; sm != nil {
				locs := append(dropUID(parseLocation(sm.Location), loc), loc)
				st := p.imapState(m.flags, locs)
				if err := p.update(ctx, tx, sm, st); err != nil {
					return err
				}
				continue
			}
			raw := raws[m.uid]
			if raw == nil {
				continue
			}
			locs := []imapLoc{loc}
			in := &newMessage{RemoteID: remote, Raw: raw, Date: m.date, State: p.imapState(m.flags, locs)}
			if _, err := p.insert(ctx, tx, in); err != nil {
				return err
			}
			// A second copy in the same chunk merges into this one.
			sm, err := p.lookup(ctx, tx, remote)
			if err != nil {
				return err
			}
			if sm != nil {
				stored[remote] = sm
			}
		}
		if len(chunk) > 0 && chunk[len(chunk)-1] > st.LastUID {
			st.LastUID = chunk[len(chunk)-1]
		}
		b, _ := json.Marshal(st)
		_, err := tx.Exec(ctx, `UPDATE collab_folders SET sync_state = $2 WHERE id = $1`, f.ID, b)
		return err
	})
	return p.imported - before, err
}

// imapRefresh resyncs the flags of the folder's known messages (only those changed since
// modseq with CONDSTORE), keeps their folders in step with their locations, and drops
// the locations of messages gone from it.
func (s *service) imapRefresh(ctx context.Context, p *pass, c *imapclient.Client, f *folderRow, validity uint32,
	known map[uint32]*storedMsg, condstore bool, modseq uint64) error {
	all := make([]uint32, 0, len(known))
	for u := range known {
		all = append(all, u)
	}
	slices.Sort(all)
	opts := &imap.FetchOptions{UID: true, Flags: true}
	if condstore && modseq > 0 {
		opts.ChangedSince = modseq
	}
	msgs, err := c.Fetch(uidSet(all), opts).Collect()
	if err != nil {
		return opError("reading flags", err)
	}
	flags := map[uint32][]imap.Flag{}
	for _, m := range msgs {
		flags[uint32(m.UID)] = m.Flags
	}
	data, err := c.UIDSearch(&imap.SearchCriteria{UID: []imap.UIDSet{uidSet(all)}}, nil).Wait()
	if err != nil {
		return opError("searching a folder", err)
	}
	present := map[uint32]bool{}
	for _, u := range data.AllUIDs() {
		present[uint32(u)] = true
	}
	return p.batch(ctx, func(tx pgx.Tx) error {
		for _, u := range all {
			m := known[u]
			locs := parseLocation(m.Location)
			if !present[u] {
				if err := p.setLocs(ctx, tx, m, dropUID(locs, imapLoc{Folder: f.RemoteID, UID: u})); err != nil {
					return err
				}
				continue
			}
			st := p.imapState(nil, locs)
			if fl, ok := flags[u]; ok {
				st = p.imapState(fl, locs)
			} else {
				// Unchanged flags; its folders follow its locations.
				st.Unread, st.Starred, st.Draft = m.Unread, m.Starred, m.Draft
			}
			if err := p.update(ctx, tx, m, st); err != nil {
				return err
			}
		}
		return nil
	})
}

// imapKnown loads the stored messages with a location in the folder, by UID.
func (p *pass) imapKnown(ctx context.Context, folder string) (map[uint32]*storedMsg, error) {
	// Containment on the folder name alone.
	probe, _ := json.Marshal(map[string]any{"imap": []map[string]string{{"f": folder}}})
	rows, err := p.s.d.DB.Pool.Query(ctx, `SELECT `+storedColumns+` FROM collab_messages m WHERE m.account_id = $1 AND m.location @> $2::jsonb`,
		p.a.ID, string(probe))
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (*storedMsg, error) { return scanStored(r) })
	if err != nil {
		return nil, err
	}
	out := map[uint32]*storedMsg{}
	for _, m := range list {
		for _, l := range parseLocation(m.Location) {
			if l.Folder == folder {
				out[l.UID] = m
			}
		}
	}
	return out, nil
}

// imapState is a message's state from one copy's flags and all its locations.
func (p *pass) imapState(flags []imap.Flag, locs []imapLoc) state {
	remote := []string{}
	for _, l := range locs {
		remote = append(remote, l.Folder)
	}
	return state{
		Unread:    !hasFlag(flags, imap.FlagSeen),
		Starred:   hasFlag(flags, imap.FlagFlagged),
		Draft:     hasFlag(flags, imap.FlagDraft),
		FolderIDs: p.folderIDs(remote),
		Location:  encodeLocation(locs),
	}
}

// setLocs replaces a message's locations (and so its folders), keeping its flags.
func (p *pass) setLocs(ctx context.Context, tx pgx.Tx, m *storedMsg, locs []imapLoc) error {
	remote := []string{}
	for _, l := range locs {
		remote = append(remote, l.Folder)
	}
	return p.update(ctx, tx, m, state{Unread: m.Unread, Starred: m.Starred, Draft: m.Draft, FolderIDs: p.folderIDs(remote),
		Location: encodeLocation(locs)})
}

func dropFolder(locs []imapLoc, folder string) []imapLoc {
	return slices.DeleteFunc(slices.Clone(locs), func(l imapLoc) bool { return l.Folder == folder })
}

func dropUID(locs []imapLoc, loc imapLoc) []imapLoc {
	return slices.DeleteFunc(slices.Clone(locs), func(l imapLoc) bool { return l.Folder == loc.Folder && l.UID == loc.UID })
}

func hasFlag(flags []imap.Flag, want imap.Flag) bool {
	for _, f := range flags {
		if strings.EqualFold(string(f), string(want)) {
			return true
		}
	}
	return false
}

// testIMAP tries an IMAP account's IMAP and SMTP servers with its credentials; the error
// text is safe to show (422 connection_failed).
func (s *service) testIMAP(ctx context.Context, cfg accountConfig, address string, c credentials) error {
	if !s.d.Cfg.CollabIMAP {
		return errIMAPOff
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	user := cfg.IMAPUsername
	if user == "" {
		user = address
	}
	conn, err := s.dialIMAP(ctx, cfg, user, c.Password)
	if err != nil {
		return err
	}
	_, err = conn.c.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait()
	conn.close()
	if err != nil {
		return opError("opening INBOX", err)
	}
	return s.checkSMTP(ctx, cfg, address, c)
}
