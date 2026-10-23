package email

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/events"
)

// Inbound and outbound kinds.
const (
	InboundForwarding = "forwarding"
	InboundIMAP       = "imap"
	KindGoogle        = "google"
	KindMicrosoft     = "microsoft"
	OutboundSMTP      = "smtp"
	OutboundSystem    = "system"
)

// OAuth grants a Google or Microsoft mailbox holds (MailboxConfig.OAuthGrant). A send
// grant sends only (gmail.send, Mail.Send) and the mailbox receives by forwarding; a sync
// grant also reads the inbox over IMAP; an api grant reads the inbox through the Gmail
// API or Microsoft Graph and sends through them, with the Collab OAuth client. A
// connection stored without a grant is a sync one.
const (
	GrantSend = "send"
	GrantSync = "sync"
	GrantAPI  = "api"
)

// ClientCollab marks tokens issued to the Collab OAuth client (MailboxConfig.OAuthClient);
// without it they belong to the mailbox client.
const ClientCollab = "collab"

// Receiving states (Mailbox.Receiving).
const (
	ReceivingOK      = "ok"
	ReceivingIMAPOff = "imap_off"
)

// Sending states (Mailbox.Sending).
const (
	SendingOK        = "ok"
	SendingSystemOff = "system_off"
)

// Where the first sync of a mailbox that reads its inbox (over IMAP, or through the Gmail
// API or Microsoft Graph) starts (MailboxConfig.SyncStart): the last 7 days of the inbox
// (the default), or only mail that arrives from then on, for a team whose history came
// over by import.
const (
	SyncStartRecent = "recent"
	SyncStartNew    = "new"
)

// Whether mail as an alias goes out From the alias itself (AliasSettings.SendAs):
// verified when the provider lists it as a verified send-as address or an admin said the
// provider sends as it; unverified when the provider does not list it or refused it; unknown
// until either happens.
const (
	SendAsVerified   = "verified"
	SendAsUnverified = "unverified"
	SendAsUnknown    = "unknown"
)

// Who decided an alias's send_as (AliasSettings.SendAsBy): the provider (Gmail's send-as
// list, or a refusal) or an admin; "" while unknown.
const (
	SendAsByProvider = "provider"
	SendAsByAdmin    = "admin"
)

// AliasSettings make an alias its own identity (contracts/teams.md "Aliases"): mail written
// to it is answered with its From name, signature and brand, From the alias where the
// provider sends as it.
type AliasSettings struct {
	DisplayName   string     `json:"display_name" doc:"The From name's {mailbox} and {{ mailbox.name }} for mail to this alias; \"\" = the mailbox's"`
	SignatureHTML string     `json:"signature_html" doc:"The signature of mail to this alias, before the team's and the address's; \"\" = none of its own"`
	BrandID       *uuid.UUID `json:"brand_id" doc:"The brand new tickets written to this alias take; null = the mailbox's"`
	SendAs        string     `json:"send_as" enum:"verified,unverified,unknown" doc:"Whether replies go out From the alias (verified) or From the mailbox address with the alias as Reply-To"`
	SendAsBy      string     `json:"send_as_by" doc:"provider, admin, or \"\" while unknown"`
}

// isZero reports settings that say nothing beyond the defaults.
func (a AliasSettings) isZero() bool {
	return a.DisplayName == "" && a.SignatureHTML == "" && a.BrandID == nil && (a.SendAs == "" || a.SendAs == SendAsUnknown)
}

// ProviderSendAs is what the Google connection says about sending as other addresses.
type ProviderSendAs struct {
	// Readable is true when the connection may read Gmail's send-as list (a sync or api
	// grant); a send-only grant cannot.
	Readable  bool            `json:"readable"`
	CheckedAt *time.Time      `json:"checked_at"`
	Addresses []SendAsAddress `json:"addresses" doc:"Gmail's send-as addresses of the connected account, other than its own"`
}

// SendAsAddress is one of Gmail's send-as addresses.
type SendAsAddress struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Verified    bool   `json:"verified"`
}

// storedSendAs is mailboxes.provider_send_as: Gmail's list as last read for account.
type storedSendAs struct {
	Account   string          `json:"account"`
	CheckedAt time.Time       `json:"checked_at"`
	Addresses []SendAsAddress `json:"addresses"`
}

// TLS modes for IMAP and SMTP.
const (
	TLSImplicit = "tls"
	TLSStart    = "starttls"
	TLSNone     = "none"
)

// MailboxConfig holds the non-secret connection settings (contracts/api.md Mailbox.config).
type MailboxConfig struct {
	IMAPHost     string `json:"imap_host,omitempty"`
	IMAPPort     int    `json:"imap_port,omitempty"`
	IMAPUsername string `json:"imap_username,omitempty"`
	IMAPTLS      string `json:"imap_tls,omitempty" enum:"tls,starttls,none"`
	SMTPHost     string `json:"smtp_host,omitempty"`
	SMTPPort     int    `json:"smtp_port,omitempty"`
	SMTPUsername string `json:"smtp_username,omitempty"`
	SMTPTLS      string `json:"smtp_tls,omitempty" enum:"tls,starttls,none"`
	OAuthEmail   string `json:"oauth_email,omitempty"`
	// OAuthGrant is what the Google or Microsoft connection may do: send (send only,
	// the mailbox receives by forwarding), sync (read the inbox over IMAP and send) or
	// api (read the inbox through the provider's API and send). Set by the connection,
	// never by a request.
	OAuthGrant string `json:"oauth_grant,omitempty" enum:"send,sync,api"`
	// OAuthClient is the OAuth client the connection's tokens belong to: collab (the
	// Collab client, an api grant), or empty for the mailbox client. Set by the
	// connection, never by a request.
	OAuthClient string `json:"oauth_client,omitempty" enum:"collab"`
	// MailHost is who hosts the mailbox's mail (mailhost.Host values, e.g. gmail,
	// google_workspace, yahoo, other), for labels and connection fixes.
	MailHost string `json:"mail_host,omitempty" maxLength:"32"`
	// Tidy-up at the provider for a mailbox that reads its inbox through the Gmail API or
	// Microsoft Graph (readsAPI), all off by default; other mailboxes keep them off.
	// ProviderMarkRead marks an email read there once it became a ticket message,
	// ProviderArchiveOnSolve archives a ticket's emails there when it is solved or closed,
	// ProviderLabel labels imported emails "opendesk" (Gmail label, Outlook category).
	ProviderMarkRead       bool `json:"provider_mark_read,omitempty"`
	ProviderArchiveOnSolve bool `json:"provider_archive_on_solve,omitempty"`
	ProviderLabel          bool `json:"provider_label,omitempty"`
	// SyncStart is where the first sync reads from: recent (the last 7 days, the default)
	// or new (only mail that arrives from then on). It applies when the mailbox starts
	// reading its inbox: on connecting, and again when it moves to another account or server.
	SyncStart string `json:"sync_start,omitempty" enum:"recent,new"`
}

// providerLabelName is the Gmail label or Outlook category ProviderLabel applies.
const providerLabelName = "opendesk"

// withoutTidy is c with the provider tidy-up options off.
func (c MailboxConfig) withoutTidy() MailboxConfig {
	c.ProviderMarkRead, c.ProviderArchiveOnSolve, c.ProviderLabel = false, false, false
	return c
}

// Mailbox is the API shape. Credentials are never part of it.
type Mailbox struct {
	ID                uuid.UUID     `json:"id"`
	Name              string        `json:"name"`
	Email             string        `json:"email"`
	DisplayName       string        `json:"display_name"`
	InboundKind       string        `json:"inbound_kind" enum:"forwarding,imap,google,microsoft"`
	OutboundKind      string        `json:"outbound_kind" enum:"smtp,google,microsoft,system"`
	ForwardingAddress string        `json:"forwarding_address"`
	Config            MailboxConfig `json:"config"`
	TeamID            *uuid.UUID    `json:"team_id"`
	SignatureHTML     string        `json:"signature_html"`
	AutoReplyEnabled  bool          `json:"auto_reply_enabled"`
	AutoReplySubject  string        `json:"auto_reply_subject"`
	AutoReplyHTML     string        `json:"auto_reply_html"`
	// Ollie answers emails to this mailbox (contracts/agent.md "Email"), off by default.
	AIAnswers             bool   `json:"ai_answers"`
	AIAnswersMode         string `json:"ai_answers_mode" enum:"draft,send"`
	AIAnswersOutsideHours bool   `json:"ai_answers_outside_hours"`
	// BrandID is the brand the address belongs to (contracts/teams.md).
	BrandID uuid.UUID `json:"brand_id"`
	// Aliases are other addresses that deliver into this mailbox.
	Aliases []string `json:"aliases"`
	// AliasTeams are the aliases whose new tickets go to a team of their own instead of TeamID.
	AliasTeams map[string]uuid.UUID `json:"alias_teams" doc:"An alias to the team its new tickets go to, instead of team_id"`
	// AliasSettings are each alias's own identity; every alias has an entry.
	AliasSettings map[string]AliasSettings `json:"alias_settings" doc:"Each alias's From name, signature, brand and whether replies go out From it"`
	// ProviderSendAs is Gmail's send-as list for a Google mailbox; admins only, null otherwise.
	ProviderSendAs *ProviderSendAs `json:"provider_send_as"`
	// FromNameFormat is the From name of its emails: {agent}, {agent_first}, {team}, {brand}, {mailbox}.
	FromNameFormat string `json:"from_name_format"`
	// TrustedFormSenders are the website contact forms whose authenticated mail is from the
	// visitor its Reply-To names: exact addresses or "@domain" entries. Admins only.
	TrustedFormSenders []string `json:"trusted_form_senders"`
	// BotID is the bot the AI agent answers and signs as here (contracts/bots.md); nil = the default bot.
	BotID *uuid.UUID `json:"bot_id"`
	// Private is true when its team is private: only that team's members send from it.
	Private bool `json:"private"`
	// Receiving is imap_off when the mailbox receives over IMAP and that is off for its
	// workspace (MAILBOX_IMAP, or Collab for generic IMAP): it is kept but not polled
	// until it switches to forwarding.
	Receiving string `json:"receiving" enum:"ok,imap_off"`
	// Sending is system_off when the mailbox sends through the platform mailer and this
	// instance has that off (MAILBOX_SYSTEM_SEND): it sends nothing until it connects
	// Google, Microsoft or SMTP.
	Sending      string     `json:"sending" enum:"ok,system_off"`
	Status       string     `json:"status" enum:"active,error,disabled"`
	LastError    string     `json:"last_error"`
	LastSyncedAt *time.Time `json:"last_synced_at"`
	// LastReceivedAt is when mail last arrived, by any route; nil before the first.
	LastReceivedAt *time.Time `json:"last_received_at"`
	CreatedAt      time.Time  `json:"created_at"`
}

// mailboxRow is the stored mailbox, including fields the API never returns.
type mailboxRow struct {
	Mailbox
	WorkspaceID     uuid.UUID
	ForwardingToken string
	CredentialsEnc  []byte
	UIDValidity     int64
	LastUID         int64
	SyncFailures    int
	// sendAs is Gmail's send-as list as last read, nil before.
	sendAs *storedSendAs
}

// credentials is the sealed secret blob in mailboxes.credentials_enc.
type credentials struct {
	Password     string `json:"password,omitempty"`
	SMTPPassword string `json:"smtp_password,omitempty"`

	// OAuth. For Google one access token covers IMAP and the Gmail API. Microsoft
	// tokens are per resource: AccessToken is for outlook.office.com (IMAP) and
	// GraphAccessToken for graph.microsoft.com (sendMail).
	RefreshToken     string    `json:"refresh_token,omitempty"`
	AccessToken      string    `json:"access_token,omitempty"`
	Expiry           time.Time `json:"expiry,omitempty"`
	GraphAccessToken string    `json:"graph_access_token,omitempty"`
	GraphExpiry      time.Time `json:"graph_expiry,omitempty"`
}

func (c credentials) smtpPassword() string {
	if c.SMTPPassword != "" {
		return c.SMTPPassword
	}
	return c.Password
}

const mailboxColumns = `id, workspace_id, name, email, display_name, inbound_kind, forwarding_token, outbound_kind,
	config, credentials_enc, team_id, signature_html, auto_reply_enabled, auto_reply_subject, auto_reply_html,
	status, last_error, last_synced_at, imap_uid_validity, imap_last_uid, sync_failures, created_at,
	ai_answers, ai_answers_mode, ai_answers_outside_hours, brand_id, aliases, from_name_format,
	EXISTS (SELECT 1 FROM teams pt WHERE pt.id = mailboxes.team_id AND pt.private), trusted_form_senders, bot_id, alias_teams,
	last_received_at, alias_settings, provider_send_as`

func scanMailbox(row pgx.Row) (*mailboxRow, error) {
	var m mailboxRow
	err := row.Scan(&m.ID, &m.WorkspaceID, &m.Name, &m.Email, &m.DisplayName, &m.InboundKind, &m.ForwardingToken,
		&m.OutboundKind, &m.Config, &m.CredentialsEnc, &m.TeamID, &m.SignatureHTML, &m.AutoReplyEnabled,
		&m.AutoReplySubject, &m.AutoReplyHTML, &m.Status, &m.LastError, &m.LastSyncedAt, &m.UIDValidity, &m.LastUID,
		&m.SyncFailures, &m.CreatedAt, &m.AIAnswers, &m.AIAnswersMode, &m.AIAnswersOutsideHours, &m.BrandID, &m.Aliases,
		&m.FromNameFormat, &m.Private, &m.TrustedFormSenders, &m.BotID, &m.AliasTeams, &m.LastReceivedAt,
		&m.AliasSettings, &m.sendAs)
	if err != nil {
		return nil, err
	}
	if m.Aliases == nil {
		m.Aliases = []string{}
	}
	if m.TrustedFormSenders == nil {
		m.TrustedFormSenders = []string{}
	}
	if m.AliasTeams == nil {
		m.AliasTeams = map[string]uuid.UUID{}
	}
	if m.AliasSettings == nil {
		m.AliasSettings = map[string]AliasSettings{}
	}
	return &m, nil
}

// loadMailbox loads one mailbox scoped to the workspace.
func loadMailbox(ctx context.Context, q db.Querier, wid, id uuid.UUID) (*mailboxRow, error) {
	return scanMailbox(q.QueryRow(ctx, `SELECT `+mailboxColumns+` FROM mailboxes WHERE workspace_id = $1 AND id = $2`, wid, id))
}

// loadMailboxForUpdate locks the row; used when rotating OAuth tokens.
func loadMailboxForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*mailboxRow, error) {
	return scanMailbox(tx.QueryRow(ctx, `SELECT `+mailboxColumns+` FROM mailboxes WHERE id = $1 FOR UPDATE`, id))
}

// loadMailboxByToken finds the mailbox that owns a forwarding token.
func loadMailboxByToken(ctx context.Context, q db.Querier, token string) (*mailboxRow, error) {
	return scanMailbox(q.QueryRow(ctx, `SELECT `+mailboxColumns+` FROM mailboxes WHERE forwarding_token = $1`, token))
}

// addresses are the mailbox's own addresses, lowercased: its address, the connected
// account's and its aliases.
func (m *mailboxRow) addresses() []string {
	out := []string{strings.ToLower(m.Email)}
	if m.Config.OAuthEmail != "" {
		out = append(out, strings.ToLower(m.Config.OAuthEmail))
	}
	for _, a := range m.Aliases {
		out = append(out, strings.ToLower(a))
	}
	return out
}

// receivesIMAP reports an inbound kind that may read the mailbox over IMAP: generic
// IMAP, or a Google or Microsoft connection (unless it reads through the API, readsAPI).
func receivesIMAP(kind string) bool {
	return kind == InboundIMAP || kind == KindGoogle || kind == KindMicrosoft
}

// readsAPI reports a Google or Microsoft mailbox that reads its inbox through the Gmail
// API or Microsoft Graph (an api grant): it is polled over HTTPS and never dialled over IMAP.
func (m *mailboxRow) readsAPI() bool {
	return (m.InboundKind == KindGoogle || m.InboundKind == KindMicrosoft) && m.Config.OAuthGrant == GrantAPI
}

// sendsAPI reports a mailbox that sends through the Gmail API or Microsoft Graph with an
// api grant: its replies can join the provider's conversation (providerThread).
func (m *mailboxRow) sendsAPI() bool {
	return m.Config.OAuthGrant == GrantAPI && (m.OutboundKind == KindGoogle || m.OutboundKind == KindMicrosoft) &&
		m.OutboundKind == m.oauthProvider()
}

// readsIMAP reports a mailbox that reads its inbox over IMAP: generic IMAP, or a Google
// or Microsoft sync connection.
func (m *mailboxRow) readsIMAP() bool {
	return receivesIMAP(m.InboundKind) && !m.readsAPI()
}

// polled reports a mailbox the sync job reads (over IMAP or the provider's API).
func (m *mailboxRow) polled() bool {
	return m.readsIMAP() || m.readsAPI()
}

// oauthProvider is the provider whose grant the mailbox holds: the one it receives
// through, else the one it sends through; "" without one.
func (m *mailboxRow) oauthProvider() string {
	switch {
	case m.InboundKind == KindGoogle || m.InboundKind == KindMicrosoft:
		return m.InboundKind
	case m.OutboundKind == KindGoogle || m.OutboundKind == KindMicrosoft:
		return m.OutboundKind
	}
	return ""
}

// sendOnly reports a grant that sends but cannot read the mailbox.
func (m *mailboxRow) sendOnly() bool {
	return m.Config.OAuthGrant == GrantSend
}

// noIMAPGrant reports a grant without IMAP consent: send, or api (which reads through
// the provider's API instead).
func (m *mailboxRow) noIMAPGrant() bool {
	return m.Config.OAuthGrant == GrantSend || m.Config.OAuthGrant == GrantAPI
}

// alias returns addr when it is one of the mailbox's aliases (not its own address).
func (m *mailboxRow) alias(addr string) string {
	addr = strings.ToLower(strings.TrimSpace(addr))
	if addr == "" || strings.EqualFold(addr, m.Email) {
		return ""
	}
	for _, a := range m.Aliases {
		if strings.EqualFold(a, addr) {
			return addr
		}
	}
	return ""
}

// displayName is what {mailbox} reads: the display name, else the name.
func (m *mailboxRow) displayName() string {
	if d := strings.TrimSpace(m.DisplayName); d != "" {
		return d
	}
	return m.Name
}

// aliasSettings are the settings of one of the mailbox's aliases (zero for "" or another address).
func (m *mailboxRow) aliasSettings(alias string) AliasSettings {
	if alias = m.alias(alias); alias == "" {
		return AliasSettings{}
	}
	return m.AliasSettings[alias]
}

// nameFor is what {mailbox} reads for mail as alias: the alias's display name, else the
// mailbox's.
func (m *mailboxRow) nameFor(alias string) string {
	if d := strings.TrimSpace(m.aliasSettings(alias).DisplayName); d != "" {
		return d
	}
	return m.displayName()
}

// sendsAs reports whether mail as alias goes out From the alias itself: always over SMTP;
// through Gmail only for a verified send-as address (Gmail would put the account's own
// address in its place); through Microsoft unless Microsoft refused it (a refusal falls
// back to the mailbox address, deliverAs); never through the platform sender.
func (m *mailboxRow) sendsAs(alias string) bool {
	if m.alias(alias) == "" {
		return false
	}
	switch m.OutboundKind {
	case OutboundSMTP:
		return true
	case KindGoogle:
		return m.aliasSettings(alias).SendAs == SendAsVerified
	case KindMicrosoft:
		return m.aliasSettings(alias).SendAs != SendAsUnverified
	}
	return false
}

// readsSendAs reports a connection that may read Gmail's send-as list: a Google sync grant
// (https://mail.google.com/) or api grant (gmail.modify). A send grant (gmail.send) cannot.
func (m *mailboxRow) readsSendAs() bool {
	return m.oauthProvider() == KindGoogle && m.Config.OAuthEmail != "" && (m.Config.OAuthGrant == GrantSync || m.Config.OAuthGrant == GrantAPI)
}

// sendAsList is Gmail's list as last read for the connected account, nil when it was not
// read or belongs to another account.
func (m *mailboxRow) sendAsList() *storedSendAs {
	if m.sendAs == nil || !strings.EqualFold(m.sendAs.Account, m.Config.OAuthEmail) || !m.readsSendAs() {
		return nil
	}
	return m.sendAs
}

func (s *service) forwardingAddress(token string) string {
	return token + "@" + s.inboundDomain()
}

func (s *service) inboundDomain() string {
	return strings.ToLower(strings.TrimSpace(s.d.Cfg.InboundDomain))
}

// api returns the public shape. Non-admin readers do not see connection settings. a is
// what the mailbox's workspace may use (s.access).
func (s *service) api(m *mailboxRow, withConfig bool, a access) Mailbox {
	out := m.Mailbox
	out.ForwardingAddress = s.forwardingAddress(m.ForwardingToken)
	out.Receiving = ReceivingOK
	if m.readsIMAP() && !s.imapReceives(m, a) {
		out.Receiving = ReceivingIMAPOff
	}
	out.Sending = SendingOK
	if !s.canSend(m) {
		out.Sending = SendingSystemOff
	}
	if out.Config.OAuthGrant == "" && m.oauthProvider() != "" {
		out.Config.OAuthGrant = GrantSync
	}
	out.AliasSettings = make(map[string]AliasSettings, len(m.Aliases))
	for _, a := range m.Aliases {
		st := m.AliasSettings[a]
		if st.SendAs == "" {
			st.SendAs = SendAsUnknown
		}
		if st.SendAs == SendAsUnknown {
			st.SendAsBy = ""
		}
		out.AliasSettings[a] = st
	}
	out.ProviderSendAs = nil
	if withConfig && m.oauthProvider() == KindGoogle {
		ps := &ProviderSendAs{Readable: m.readsSendAs(), Addresses: []SendAsAddress{}}
		if l := m.sendAsList(); l != nil {
			at := l.CheckedAt
			ps.CheckedAt = &at
			for _, a := range l.Addresses {
				if !strings.EqualFold(a.Email, m.Email) && !strings.EqualFold(a.Email, m.Config.OAuthEmail) {
					ps.Addresses = append(ps.Addresses, a)
				}
			}
		}
		out.ProviderSendAs = ps
	}
	if !withConfig {
		out.Config = MailboxConfig{}
		out.TrustedFormSenders = []string{}
	}
	return out
}

// canSend reports whether the mailbox has a way to send: anything but the platform
// mailer while this instance has that off.
func (s *service) canSend(m *mailboxRow) bool {
	return m.OutboundKind != OutboundSystem || s.systemSend
}

func (s *service) openCreds(m *mailboxRow) (credentials, error) {
	var c credentials
	if err := s.d.Box.OpenJSON(m.CredentialsEnc, &c); err != nil {
		return c, errors.New("mailbox credentials could not be opened")
	}
	return c, nil
}

func (s *service) sealCreds(c credentials) ([]byte, error) {
	return s.d.Box.SealJSON(c)
}

// publishMailbox publishes mailbox.created / mailbox.updated with the reloaded row.
func (s *service) publishMailbox(ctx context.Context, tx pgx.Tx, wid, id uuid.UUID, typ string) error {
	m, err := loadMailbox(ctx, tx, wid, id)
	if err != nil {
		return err
	}
	a, err := s.access(ctx, tx, wid)
	if err != nil {
		return err
	}
	// Every member sees the mailbox, but only as a non-admin reads it: config and trusted
	// senders stay with GET /v1/mailboxes for admins.
	_, err = events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: typ, Data: map[string]any{"mailbox": s.api(m, false, a)}})
	return err
}

// newForwardingToken makes "<workspace-slug-prefix>-<random>", lowercase, no '+'.
func newForwardingToken(slug string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(slug) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
				b.WriteByte('-')
			}
		}
		if b.Len() >= 20 {
			break
		}
	}
	prefix := strings.Trim(b.String(), "-")
	if prefix == "" {
		prefix = "desk"
	}
	return prefix + "-" + crypto.RandomString(8)
}

// emailDomain returns the domain of an address, or "" when it has none.
func emailDomain(addr string) string {
	if i := strings.LastIndexByte(addr, '@'); i >= 0 {
		return strings.ToLower(addr[i+1:])
	}
	return ""
}

// setSyncError records a failed sync with a safe message and schedules the next try
// with exponential backoff. It publishes mailbox.updated when the visible state changed.
func (s *service) setSyncError(ctx context.Context, m *mailboxRow, msg string) error {
	failures := m.SyncFailures + 1
	backoff := time.Minute << min(failures-1, 6) // 1m .. 64m
	if backoff > time.Hour {
		backoff = time.Hour
	}
	msg = truncate(msg, 300)
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var prevStatus, prevErr string
		err := tx.QueryRow(ctx, `UPDATE mailboxes m SET status = CASE WHEN m.status = 'disabled' THEN m.status ELSE 'error' END,
			last_error = $3, sync_failures = $4, next_sync_at = now() + $5::interval, updated_at = now()
			FROM (SELECT status, last_error FROM mailboxes WHERE id = $2 FOR UPDATE) old
			WHERE m.workspace_id = $1 AND m.id = $2 RETURNING old.status, old.last_error`,
			m.WorkspaceID, m.ID, msg, failures, fmt.Sprintf("%d seconds", int(backoff.Seconds()))).Scan(&prevStatus, &prevErr)
		if err != nil {
			return err
		}
		if prevStatus != "error" || prevErr != msg {
			return s.publishMailbox(ctx, tx, m.WorkspaceID, m.ID, "mailbox.updated")
		}
		return nil
	})
}

// setSyncOK records a successful sync; it publishes mailbox.updated only when the
// mailbox leaves the error state.
func (s *service) setSyncOK(ctx context.Context, m *mailboxRow, validity, lastUID int64) error {
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var prevStatus string
		err := tx.QueryRow(ctx, `UPDATE mailboxes m SET status = CASE WHEN m.status = 'error' THEN 'active' ELSE m.status END,
			last_error = '', sync_failures = 0, next_sync_at = NULL, last_synced_at = now(),
			imap_uid_validity = $3, imap_last_uid = $4, updated_at = now()
			FROM (SELECT status FROM mailboxes WHERE id = $2 FOR UPDATE) old
			WHERE m.workspace_id = $1 AND m.id = $2 RETURNING old.status`, m.WorkspaceID, m.ID, validity, lastUID).Scan(&prevStatus)
		if err != nil {
			return err
		}
		if prevStatus == "error" {
			return s.publishMailbox(ctx, tx, m.WorkspaceID, m.ID, "mailbox.updated")
		}
		return nil
	})
}

// saveProgress persists the UID cursor mid-pass so a failure later in the batch
// does not re-fetch what was already ingested.
func (s *service) saveProgress(ctx context.Context, m *mailboxRow, validity, lastUID int64) error {
	_, err := s.d.DB.Pool.Exec(ctx, `UPDATE mailboxes SET imap_uid_validity = $3, imap_last_uid = $4 WHERE workspace_id = $1 AND id = $2`,
		m.WorkspaceID, m.ID, validity, lastUID)
	return err
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
