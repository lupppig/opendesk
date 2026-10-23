package collab

import (
	"context"
	"encoding/json"
	"net/mail"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/email"
	"github.com/useopendesk/opendesk/internal/ids"
)

// Addresses (identities) an account sends from (contracts/collab.md "Addresses"). The
// account's own address is the primary one, and its From name and signature are the
// account's (display_name, signature_html). The others are collab_identities rows:
//
//   - Gmail: every send-as address Gmail lists (users.settings.sendAs), read with the
//     account's settings (settings.go, one list call) and gone when Gmail no longer lists
//     it. Each one's From name and signature come from Gmail when they change there (the
//     hashes of Gmail's values as last seen decide), and a change made in opendesk is kept
//     in opendesk (SyncPull): Gmail lets apps change only the primary address's settings,
//     the others need a service account with domain-wide authority. The default address
//     for new mail is the same: Gmail's change comes in, a choice here stays here. An
//     address Gmail is still verifying (verificationStatus pending) cannot send.
//   - Microsoft and IMAP: aliases a person adds by hand, kept in opendesk only. Graph has
//     no send-as list on the Collab scopes; sending from one sets the MIME From, and
//     Exchange or the SMTP server decides whether it accepts it.
//
// A message goes out from one of the account's usable addresses only: the one the
// composer chose (from_email), else for a draft saved again its own From, for a reply the
// address the answered message was written to, else the account's default.

// Identity sources.
const (
	SourceAccount  = "account"  // the account's own address
	SourceProvider = "provider" // listed by the provider (Gmail send-as)
	SourceManual   = "manual"   // added by a person (Microsoft, IMAP)
)

// Verification states of an address.
const (
	VerificationAccepted = "accepted"
	VerificationPending  = "pending"
)

// maxIdentities bounds an account's addresses besides its own: the ones Gmail lists are
// read up to it, and hand-added ones stop at maxManualIdentities.
const (
	maxIdentities       = 100
	maxManualIdentities = 50
)

// identitySync is collab_identities.sync_state: what was last seen at the provider.
type identitySync struct {
	SignatureHash     string     `json:"signature_hash,omitempty"`
	SignatureSyncedAt *time.Time `json:"signature_synced_at,omitempty"`
	DisplayNameHash   string     `json:"display_name_hash,omitempty"`
}

// identityRow is a stored address of an account (never its own).
type identityRow struct {
	ID            uuid.UUID
	WorkspaceID   uuid.UUID
	AccountID     uuid.UUID
	Email         string
	DisplayName   string
	SignatureHTML string
	ReplyTo       string
	Source        string
	Verification  string
	Position      int
	Sync          identitySync
	CreatedAt     time.Time
}

const identityColumns = `id, workspace_id, account_id, email, display_name, signature_html, reply_to, source, verification,
	position, sync_state, created_at`

func scanIdentity(row pgx.Row) (*identityRow, error) {
	var x identityRow
	if err := row.Scan(&x.ID, &x.WorkspaceID, &x.AccountID, &x.Email, &x.DisplayName, &x.SignatureHTML, &x.ReplyTo, &x.Source,
		&x.Verification, &x.Position, &x.Sync, &x.CreatedAt); err != nil {
		return nil, err
	}
	return &x, nil
}

// usable reports whether the address may send.
func (x *identityRow) usable() bool { return x.Verification != VerificationPending }

// loadIdentities loads the addresses of the workspace's accounts, in the provider's order.
func loadIdentities(ctx context.Context, q db.Querier, wid uuid.UUID, accountIDs []uuid.UUID) (map[uuid.UUID][]*identityRow, error) {
	out := map[uuid.UUID][]*identityRow{}
	if len(accountIDs) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT `+identityColumns+` FROM collab_identities WHERE workspace_id = $1 AND account_id = ANY($2)
		ORDER BY account_id, position, email`, wid, accountIDs)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (*identityRow, error) { return scanIdentity(r) })
	if err != nil {
		return nil, err
	}
	for _, x := range list {
		out[x.AccountID] = append(out[x.AccountID], x)
	}
	return out, nil
}

// accountIdentities loads one account's addresses.
func accountIdentities(ctx context.Context, q db.Querier, a *accountRow) ([]*identityRow, error) {
	m, err := loadIdentities(ctx, q, a.WorkspaceID, []uuid.UUID{a.ID})
	return m[a.ID], err
}

// ownAddresses is every address of the account, its own first, lower case.
func ownAddresses(a *accountRow, list []*identityRow) []string {
	out := []string{strings.ToLower(a.Email)}
	for _, x := range list {
		out = append(out, x.Email)
	}
	return out
}

// sender is who a message goes out from.
type sender struct {
	Email   string
	Name    string
	ReplyTo string
}

// address is the From header's value.
func (s sender) address() mail.Address { return mail.Address{Name: s.Name, Address: s.Email} }

// primarySender is the account's own address.
func primarySender(a *accountRow) sender {
	return sender{Email: a.Email, Name: a.DisplayName, ReplyTo: a.settings().ReplyTo}
}

// usableSender is the account's usable address email, if it is one.
func usableSender(a *accountRow, list []*identityRow, addr string) (sender, bool) {
	addr = strings.ToLower(strings.TrimSpace(addr))
	if addr == "" {
		return sender{}, false
	}
	if strings.EqualFold(addr, a.Email) {
		return primarySender(a), true
	}
	for _, x := range list {
		if x.Email == addr && x.usable() {
			name := x.DisplayName
			if name == "" {
				name = a.DisplayName // Gmail leaves an address's name empty to use the account's
			}
			return sender{Email: x.Email, Name: name, ReplyTo: x.ReplyTo}, true
		}
	}
	return sender{}, false
}

// defaultSender is the address new mail goes out from: the account's default when it can
// send, else its own.
func defaultSender(a *accountRow, list []*identityRow) sender {
	if s, ok := usableSender(a, list, a.settings().DefaultFrom); ok {
		return s
	}
	return primarySender(a)
}

// ownAddressOf is the account's address a message was sent from or written to: its From
// when that is one of addrs, else the first of addrs in its To, Cc, then where it was
// delivered (Delivered-To, X-Original-To). "" when none.
func ownAddressOf(m *messageRow, addrs []string) string {
	has := func(e string) bool { return slices.Contains(addrs, strings.ToLower(strings.TrimSpace(e))) }
	if has(m.From.Email) {
		return strings.ToLower(m.From.Email)
	}
	for _, list := range [][]Address{m.To, m.Cc} {
		for _, x := range list {
			if has(x.Email) {
				return strings.ToLower(x.Email)
			}
		}
	}
	for _, e := range m.DeliveredTo {
		if has(e) {
			return strings.ToLower(e)
		}
	}
	return ""
}

// replySender is the address a reply to m goes out from: the usable address m was sent
// from or written to, else the account's default.
func replySender(a *accountRow, list []*identityRow, m *messageRow) sender {
	usable := []string{strings.ToLower(a.Email)}
	for _, x := range list {
		if x.usable() {
			usable = append(usable, x.Email)
		}
	}
	if s, ok := usableSender(a, list, ownAddressOf(m, usable)); ok {
		return s
	}
	return defaultSender(a, list)
}

// identitiesOut is the account's addresses for the API, its own first, with each one's
// From name and signature filled for f's member.
func identitiesOut(ctx context.Context, f *filler, a *accountRow, list []*identityRow) ([]MailIdentity, string, error) {
	def := defaultSender(a, list).Email
	st := a.settings()
	google := a.Provider == ProviderGoogle
	out := []MailIdentity{{Email: strings.ToLower(a.Email), DisplayName: a.DisplayName, SignatureHTML: a.SignatureHTML, ReplyTo: st.ReplyTo,
		Primary: true, Default: strings.EqualFold(def, a.Email), Source: SourceAccount, Verification: VerificationAccepted, CanSend: true,
		SignatureSync: signatureSync(a), DisplayNameSync: displayNameSync(a), SignatureSyncedAt: st.SignatureSyncedAt}}
	for _, x := range list {
		id := x.ID
		mode := SyncNone
		if x.Source == SourceProvider {
			mode = SyncPull
		}
		out = append(out, MailIdentity{ID: &id, Email: x.Email, DisplayName: x.DisplayName, SignatureHTML: x.SignatureHTML, ReplyTo: x.ReplyTo,
			Default: x.Email == strings.ToLower(def), Source: x.Source, Verification: x.Verification, CanSend: x.usable(),
			SignatureSync: mode, DisplayNameSync: mode, SignatureSyncedAt: x.Sync.SignatureSyncedAt})
	}
	for i := range out {
		m := &out[i]
		m.DisplayNameLocal = google && nameTemplated(m.DisplayName)
		m.SignatureLocal = google && signatureTemplated(m.SignatureHTML)
		format := m.DisplayName
		if format == "" {
			format = a.DisplayName
		}
		var err error
		if m.FromName, err = f.fromName(ctx, a, format); err != nil {
			return nil, "", err
		}
		if signatureTemplated(m.SignatureHTML) {
			sig, err := f.signature(ctx, m.SignatureHTML, m.Email, m.FromName)
			if err != nil {
				return nil, "", err
			}
			m.FilledSignatureHTML = &sig
		}
	}
	return out, strings.ToLower(def), nil
}

// cleanAddress reads one address ("Ada <ada@x.test>" or bare), lower case; "" when invalid.
func cleanAddress(v string) string {
	addr := strings.ToLower(strings.TrimSpace(v))
	if parsed, err := mail.ParseAddress(v); err == nil {
		addr = strings.ToLower(parsed.Address)
	}
	if !email.ValidAddress(addr) {
		return ""
	}
	return addr
}

// deliveredTo is where a message was delivered: its Delivered-To, X-Original-To and
// X-Forwarded-To addresses, lower case, each once.
func deliveredTo(pm *email.Parsed) []string {
	out := []string{}
	for _, key := range []string{"Delivered-To", "X-Original-To", "X-Forwarded-To"} {
		for _, part := range strings.Split(pm.Header(key), ",") {
			if addr := cleanAddress(part); addr != "" && !slices.Contains(out, addr) && len(out) < 10 {
				out = append(out, addr)
			}
		}
	}
	return out
}

// applyGmailIdentities stores Gmail's send-as addresses other than the account's own
// (list, in Gmail's order) under the account's row lock, in tx: new ones are added, gone
// ones removed, and each one's From name and signature replaced where Gmail's changed
// since last seen there (the hashes), like the account's own (applyRemote). It reports a
// change people see.
func (s *service) applyGmailIdentities(ctx context.Context, tx pgx.Tx, a *accountRow, list []gmailSendAs, now time.Time) (bool, error) {
	existing, err := accountIdentities(ctx, tx, a)
	if err != nil {
		return false, err
	}
	byEmail := map[string]*identityRow{}
	for _, x := range existing {
		byEmail[x.Email] = x
	}
	changed := false
	seen := map[string]bool{}
	for i, sa := range list {
		addr := cleanAddress(sa.SendAsEmail)
		if addr == "" || strings.EqualFold(addr, a.Email) || seen[addr] || len(seen) >= maxIdentities {
			continue
		}
		seen[addr] = true
		verification := VerificationAccepted
		if strings.EqualFold(sa.VerificationStatus, VerificationPending) {
			verification = VerificationPending
		}
		replyTo := cleanAddress(sa.ReplyToAddress)
		x := byEmail[addr]
		if x == nil {
			x = &identityRow{ID: ids.New(), WorkspaceID: a.WorkspaceID, AccountID: a.ID, Email: addr, Source: SourceProvider}
		}
		before := *x
		x.Source, x.Verification, x.ReplyTo, x.Position = SourceProvider, verification, replyTo, i
		// A value with variables stays as the person wrote it (personal.go): Gmail's change
		// is seen (the hash) but does not replace it.
		if h := settingHash(sa.Signature); h != x.Sync.SignatureHash {
			sig := email.SanitizeSettingsHTML(sa.Signature)
			if (x.Sync.SignatureHash != "" || sig != "" || x.SignatureHTML == "") && !signatureTemplated(x.SignatureHTML) {
				x.SignatureHTML = sig
			}
			x.Sync.SignatureHash, x.Sync.SignatureSyncedAt = h, &now
		}
		if h := settingHash(sa.DisplayName); h != x.Sync.DisplayNameHash {
			// Gmail leaves an address's name empty to use the account's: so does opendesk.
			if !nameTemplated(x.DisplayName) {
				x.DisplayName = displayname.Clean(sa.DisplayName, displayname.Workspace)
			}
			x.Sync.DisplayNameHash = h
		}
		if byEmail[addr] == nil {
			if _, err := tx.Exec(ctx, `INSERT INTO collab_identities (id, workspace_id, account_id, email, display_name, signature_html, reply_to,
				source, verification, position, sync_state) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
				x.ID, x.WorkspaceID, x.AccountID, x.Email, x.DisplayName, x.SignatureHTML, x.ReplyTo, x.Source, x.Verification, x.Position, x.Sync); err != nil {
				return false, err
			}
			changed = true
			continue
		}
		if sameIdentity(&before, x) {
			continue
		}
		if err := saveIdentity(ctx, tx, x); err != nil {
			return false, err
		}
		changed = true
	}
	for _, x := range existing {
		if !seen[x.Email] && x.Source == SourceProvider {
			if _, err := tx.Exec(ctx, `DELETE FROM collab_identities WHERE id = $1 AND account_id = $2`, x.ID, a.ID); err != nil {
				return false, err
			}
			changed = true
		}
	}
	return changed, nil
}

// sameIdentity compares what a sync may change.
func sameIdentity(a, b *identityRow) bool {
	ja, _ := json.Marshal(a.Sync)
	jb, _ := json.Marshal(b.Sync)
	return a.DisplayName == b.DisplayName && a.SignatureHTML == b.SignatureHTML && a.ReplyTo == b.ReplyTo && a.Source == b.Source &&
		a.Verification == b.Verification && a.Position == b.Position && string(ja) == string(jb)
}

// saveIdentity writes an address's fields.
func saveIdentity(ctx context.Context, q db.Querier, x *identityRow) error {
	_, err := q.Exec(ctx, `UPDATE collab_identities SET display_name = $3, signature_html = $4, reply_to = $5, source = $6, verification = $7,
		position = $8, sync_state = $9, updated_at = now() WHERE id = $1 AND account_id = $2`,
		x.ID, x.AccountID, x.DisplayName, x.SignatureHTML, x.ReplyTo, x.Source, x.Verification, x.Position, x.Sync)
	return err
}

// usableSenderOf loads the account's addresses and checks addr is one that can send.
func usableSenderOf(ctx context.Context, s *service, a *accountRow, addr string) (sender, bool) {
	idents, err := accountIdentities(ctx, s.d.DB.Pool, a)
	if err != nil {
		return sender{}, false
	}
	return usableSender(a, idents, addr)
}
