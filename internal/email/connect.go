package email

// Connecting a mailbox (contracts/api.md "Mailboxes"): what this instance offers
// (connect-options), where a domain's mail is hosted (detect), and the connection
// check that names a cause and a fix when a server refuses a sign-in.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/email/mailcause"
	"github.com/useopendesk/opendesk/internal/email/mailhost"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// Detection limits: per signed-in user, and how long an answer is remembered.
const (
	detectPerMinute = 30
	detectCacheTTL  = 6 * time.Hour
	detectCacheMax  = 5000
	checkTimeout    = 50 * time.Second
)

// mailboxTestResult is one side of a connection check.
type mailboxTestResult struct {
	OK     bool   `json:"ok"`
	Error  string `json:"error"`
	Detail string `json:"detail,omitempty" doc:"The server's own reply, without addresses, IP addresses or tokens"`
}

// mailboxCheck is a whole connection check.
type mailboxCheck struct {
	Inbound  mailboxTestResult `json:"inbound"`
	Outbound mailboxTestResult `json:"outbound"`
	Cause    *mailcause.Cause  `json:"cause" doc:"Why the check failed and what fixes it; null when it passed or the reason is not known"`
	SMTPPort int               `json:"smtp_port,omitempty" doc:"Set when the configured SMTP port did not answer and this one did; the mailbox uses it from now on"`
}

type mailboxTestOutput struct {
	Body mailboxCheck
}

// connectionProblem is the 422 a create with verify answers when the check fails:
// the problem body plus the check's legs and cause.
type connectionProblem struct {
	Status    int               `json:"status"`
	Title     string            `json:"title"`
	Detail    string            `json:"detail,omitempty"`
	Code      string            `json:"code,omitempty"`
	RequestID string            `json:"request_id,omitempty"`
	Inbound   mailboxTestResult `json:"inbound"`
	Outbound  mailboxTestResult `json:"outbound"`
	Cause     *mailcause.Cause  `json:"cause"`
}

func (e *connectionProblem) Error() string  { return e.Detail }
func (e *connectionProblem) GetStatus() int { return e.Status }
func (e *connectionProblem) ContentType(ct string) string {
	if ct == "application/json" {
		return "application/problem+json"
	}
	return ct
}

func connectionFailed(c mailboxCheck) error {
	detail := "The connection check failed."
	if c.Cause != nil {
		detail = strings.TrimSuffix(c.Cause.Title, ".") + "."
	}
	return &connectionProblem{Status: http.StatusUnprocessableEntity, Title: http.StatusText(http.StatusUnprocessableEntity),
		Code: "mailbox_connection_failed", Detail: detail + " Nothing was saved.", Inbound: c.Inbound, Outbound: c.Outbound, Cause: c.Cause}
}

type providerOption struct {
	Configured bool `json:"configured" doc:"The provider's OAuth client is set up on this instance"`
	OAuth      bool `json:"oauth" doc:"New mailboxes may connect with this provider's sign-in"`
	APISync    bool `json:"api_sync" doc:"This workspace may connect the provider to read the inbox directly through its API (receive: api): it has Collab and the provider's Collab OAuth client is set up"`
}

type connectOptions struct {
	Google     providerOption `json:"google"`
	Microsoft  providerOption `json:"microsoft"`
	Forwarding struct {
		Enabled bool   `json:"enabled" doc:"Forwarded mail is received (INBOUND_SECRET or INBOUND_SMTP_ADDR)"`
		Domain  string `json:"domain" doc:"The domain forwarding addresses live on"`
	} `json:"forwarding"`
	MailboxIMAP  bool   `json:"mailbox_imap" doc:"This workspace's mailboxes may receive over generic IMAP: the instance has MAILBOX_IMAP on, or the workspace has Collab. Off, mailboxes receive by forwarding or, with api_sync, through the provider's API"`
	InstanceIMAP bool   `json:"instance_imap" doc:"The instance has MAILBOX_IMAP on: Google and Microsoft may also sync over IMAP (receive: sync)"`
	SystemSend   bool   `json:"system_send" doc:"Mailboxes may send through opendesk (MAILBOX_SYSTEM_SEND). Off, every mailbox sends through Google, Microsoft or SMTP"`
	SystemSender string `json:"system_sender" doc:"The address replies come from when a mailbox sends through opendesk"`
}

// detectInput takes the address in the body: an address is personal data, kept out of URLs.
type detectInput struct {
	Body struct {
		Email string `json:"email" maxLength:"254" doc:"The mailbox address"`
	}
}

type detection struct {
	Domain         string             `json:"domain"`
	Host           string             `json:"host" doc:"Who hosts the domain's mail (a config.mail_host value); empty when unknown"`
	Label          string             `json:"label" doc:"The host's display name"`
	Source         string             `json:"source" enum:"known,mx,autoconfig,ispdb,srv,none" doc:"How the host was found"`
	Settings       *mailhost.Settings `json:"settings" doc:"The IMAP and SMTP servers; null when not known"`
	PasswordAuth   string             `json:"password_auth" enum:"app_password,password,oauth_only,unsupported" doc:"How a password signs in on the host"`
	AppPasswordURL string             `json:"app_password_url" doc:"Where an app password is created; empty when the host has none"`
	OAuth          *string            `json:"oauth" enum:"google,microsoft" doc:"The sign-in the host connects with; null for none"`
}

func (s *service) registerConnectRoutes(api huma.API) {
	tags := []string{"Mailboxes"}

	huma.Register(api, huma.Operation{OperationID: "get-mailbox-connect-options", Method: http.MethodGet, Path: "/v1/mailboxes/connect-options",
		Summary: "How mailboxes can connect", Description: "The sign-ins, forwarding and sending this instance offers for a new mailbox.", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body connectOptions }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			a, err := s.access(ctx, s.d.DB.Pool, p.WorkspaceID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			cfg := s.d.Cfg
			var out connectOptions
			out.Google = providerOption{Configured: s.providers[KindGoogle].configured(), OAuth: s.oauthConnect(KindGoogle), APISync: s.apiSync(KindGoogle, a)}
			out.Microsoft = providerOption{Configured: s.providers[KindMicrosoft].configured(), OAuth: s.oauthConnect(KindMicrosoft), APISync: s.apiSync(KindMicrosoft, a)}
			out.Forwarding.Enabled = cfg.InboundSecret != "" || cfg.InboundSMTPAddr != ""
			out.Forwarding.Domain = s.inboundDomain()
			out.MailboxIMAP = s.imapAllowed(a)
			out.InstanceIMAP = s.mailboxIMAP
			out.SystemSend = s.systemSend
			out.SystemSender = s.systemFrom().Address
			return &struct{ Body connectOptions }{out}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "detect-mailbox-host", Method: http.MethodPost, Path: "/v1/mailboxes/detect",
		Summary:     "Detect a mailbox's provider and servers",
		Description: "Reads the domain's MX records and autoconfig files to name its mail host, its IMAP and SMTP servers and how a password signs in there.",
		Tags:        tags},
		func(ctx context.Context, in *detectInput) (*struct{ Body detection }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, "")
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if p.Kind != auth.KindUser {
				return nil, httpx.Forbidden()
			}
			if !s.d.Limiter.Allow("mailbox-detect:"+p.UserID.String(), detectPerMinute, time.Minute) {
				return nil, httpx.RateLimited()
			}
			addr := strings.ToLower(strings.TrimSpace(in.Body.Email))
			if !validAddress(addr) {
				return nil, httpx.Validation("Enter a valid email address.")
			}
			return &struct{ Body detection }{toDetection(s.detector.Detect(ctx, emailDomain(addr)))}, nil
		})
}

func toDetection(d mailhost.Detection) detection {
	out := detection{Domain: d.Domain, Host: string(d.Host), Label: d.Host.Label(), Source: d.Source, Settings: d.Settings,
		PasswordAuth: string(d.PasswordAuth), AppPasswordURL: d.AppPasswordURL}
	if p := mailhost.OAuthProvider(d.Host); p != "" {
		out.OAuth = &p
	}
	return out
}

// oauthConnect reports whether a new mailbox may connect with provider's sign-in.
func (s *service) oauthConnect(provider string) bool {
	if !s.providers[provider].configured() {
		return false
	}
	switch provider {
	case KindGoogle:
		return s.d.Cfg.GoogleMailboxOAuth
	case KindMicrosoft:
		return s.d.Cfg.MicrosoftMailboxOAuth
	}
	return false
}

// prepareMailbox records who hosts the mailbox and, when the request carries
// passwords, cleans them: a Google app password loses the spaces Google prints in
// it, any other password only its surrounding whitespace.
func prepareMailbox(m *mailboxRow, creds *credentials, passwords bool) {
	m.Config.MailHost = mailHostFor(m)
	if !passwords {
		return
	}
	google := mailhost.Host(m.Config.MailHost).Google()
	creds.Password = cleanPassword(google, creds.Password)
	creds.SMTPPassword = cleanPassword(google, creds.SMTPPassword)
}

// mailHostFor is who hosts the mailbox: its servers when they name a provider, else
// what the client detected, else what its kind and address say.
func mailHostFor(m *mailboxRow) string {
	stored := m.Config.MailHost
	if !mailhost.Valid(stored) {
		stored = ""
	}
	server := ""
	if m.InboundKind == InboundIMAP {
		server = m.Config.IMAPHost
	}
	if server == "" && m.OutboundKind == OutboundSMTP {
		server = m.Config.SMTPHost
	}
	if h := mailhost.FromServer(server); h != mailhost.Unknown {
		return string(mailhost.Refine(h, emailDomain(m.Email)))
	}
	return string(mailhost.ForMailbox(stored, m.InboundKind, "", m.Email))
}

func cleanPassword(google bool, p string) string {
	if google {
		compact := strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, p)
		if mailhost.LooksLikeGoogleAppPassword(compact) {
			return compact
		}
	}
	return strings.TrimSpace(p)
}

// beforeCreate prepares a new mailbox and, with verify, signs in to its IMAP and
// SMTP servers before anything is stored. A port that answered in place of the
// configured one is kept.
func (s *service) beforeCreate(ctx context.Context, m *mailboxRow, creds *credentials, verify bool) error {
	prepareMailbox(m, creds, true)
	if m.InboundKind == InboundIMAP && creds.Password == "" {
		return httpx.Validation("Enter the IMAP password.")
	}
	if m.OutboundKind == OutboundSMTP && creds.smtpPassword() == "" {
		return httpx.Validation("Enter the SMTP password.")
	}
	if !verify || (m.InboundKind != InboundIMAP && m.OutboundKind != OutboundSMTP) {
		return nil
	}
	sealed, err := s.sealCreds(*creds)
	if err != nil {
		return httpx.Wrap(err)
	}
	m.CredentialsEnc = sealed
	c := s.checkMailbox(ctx, m, *creds, true)
	if c.SMTPPort != 0 {
		m.Config.SMTPPort, m.Config.SMTPTLS = c.SMTPPort, TLSStart
	}
	if !c.Inbound.OK || !c.Outbound.OK {
		return connectionFailed(c)
	}
	return nil
}

// verifiedPatch is what a PATCH with verify learned: whether it signed in, and the
// SMTP port that answered in place of the configured one on that host.
type verifiedPatch struct {
	checked  bool
	smtpHost string
	smtpPort int
}

// verifyPatch signs in to the servers a PATCH changes before anything is stored:
// the IMAP server (while this instance receives over IMAP) when the mailbox moves to
// IMAP or its settings or password change, the SMTP server when its settings or
// password change or replies move to it. A refusal is connectionFailed; a PATCH that
// changes neither checks nothing.
func (s *service) verifyPatch(ctx context.Context, wid, id uuid.UUID, in *updateMailboxInput) (verifiedPatch, error) {
	var out verifiedPatch
	m, err := loadMailbox(ctx, s.d.DB.Pool, wid, id)
	if err != nil {
		return out, wrapMailboxErr(err)
	}
	creds, err := s.openCreds(m)
	if err != nil {
		return out, httpx.Wrap(err)
	}
	before, beforeCreds := *m, creds
	if changes, _ := applyPatch(m, &creds, in); len(changes) == 0 {
		return out, nil
	}
	if err := s.checkInboundChange(ctx, s.d.DB.Pool, wid, before.InboundKind, m.InboundKind); err != nil {
		return out, err
	}
	if err := s.checkOutboundChange(before.OutboundKind, m.OutboundKind); err != nil {
		return out, err
	}
	if err := s.validateMailbox(m, creds); err != nil {
		return out, err
	}
	prepareMailbox(m, &creds, in.Body.Password != nil || in.Body.SMTPPassword != nil)
	c, a := before.Config, m.Config
	imapOn := s.mailboxIMAP
	if !imapOn && m.InboundKind == InboundIMAP {
		a, err := s.access(ctx, s.d.DB.Pool, wid)
		if err != nil {
			return out, httpx.Wrap(err)
		}
		imapOn = s.imapAllowed(a)
	}
	inbound := imapOn && m.InboundKind == InboundIMAP && (before.InboundKind != InboundIMAP ||
		!strings.EqualFold(a.IMAPHost, c.IMAPHost) || a.IMAPPort != c.IMAPPort ||
		a.IMAPTLS != c.IMAPTLS || a.IMAPUsername != c.IMAPUsername || creds.Password != beforeCreds.Password)
	outbound := m.OutboundKind == OutboundSMTP && (before.OutboundKind != OutboundSMTP || !strings.EqualFold(a.SMTPHost, c.SMTPHost) ||
		a.SMTPPort != c.SMTPPort || a.SMTPTLS != c.SMTPTLS || a.SMTPUsername != c.SMTPUsername || creds.smtpPassword() != beforeCreds.smtpPassword())
	if !inbound && !outbound {
		return out, nil
	}
	if m.CredentialsEnc, err = s.sealCreds(creds); err != nil {
		return out, httpx.Wrap(err)
	}
	check := s.checkLegs(ctx, m, creds, inbound, outbound)
	if !check.Inbound.OK || !check.Outbound.OK {
		return out, connectionFailed(check)
	}
	out.checked, out.smtpHost, out.smtpPort = true, m.Config.SMTPHost, check.SMTPPort
	return out, nil
}

// testMailbox runs the connection check on a stored mailbox and keeps an SMTP port
// that answered in place of the configured one.
func (s *service) testMailbox(ctx context.Context, m *mailboxRow) (*mailboxTestOutput, error) {
	creds, err := s.openCreds(m)
	if err != nil {
		return nil, httpx.Wrap(err)
	}
	out := &mailboxTestOutput{Body: s.checkMailbox(ctx, m, creds, false)}
	if out.Body.SMTPPort != 0 {
		m.Config.SMTPPort, m.Config.SMTPTLS = out.Body.SMTPPort, TLSStart
		err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `UPDATE mailboxes SET config = $3, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
				m.WorkspaceID, m.ID, m.Config); err != nil {
				return err
			}
			return s.publishMailbox(ctx, tx, m.WorkspaceID, m.ID, "mailbox.updated")
		})
		if err != nil {
			return nil, wrapMailboxErr(err)
		}
	}
	return out, nil
}

// checkMailbox signs in on both sides at once. serversOnly skips forwarding, which
// has nothing to sign in to.
func (s *service) checkMailbox(ctx context.Context, m *mailboxRow, creds credentials, serversOnly bool) mailboxCheck {
	return s.checkLegs(ctx, m, creds, !serversOnly || m.InboundKind == InboundIMAP, !serversOnly || m.OutboundKind == OutboundSMTP)
}

// checkLegs signs in on the sides asked for, at once; a side not asked for passes.
func (s *service) checkLegs(ctx context.Context, m *mailboxRow, creds credentials, inbound, outbound bool) mailboxCheck {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	var out mailboxCheck
	var inErr, outErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if inbound {
			inErr = s.testInbound(ctx, m)
		}
	}()
	go func() {
		defer wg.Done()
		switch {
		case !outbound:
		case m.OutboundKind == OutboundSMTP:
			out.SMTPPort, outErr = s.checkSMTPFallback(ctx, s.mailboxSMTP(m, creds))
		default:
			outErr = s.testOutbound(ctx, m)
		}
	}()
	wg.Wait()

	var in mailcause.Input
	in.MailHost, in.Password = m.Config.MailHost, creds.Password
	out.Inbound, in.IMAP = checkLeg("IMAP", inErr, m.Config.IMAPHost, m.Config.IMAPPort)
	smtpPort := m.Config.SMTPPort
	if out.SMTPPort != 0 {
		smtpPort = out.SMTPPort
	}
	out.Outbound, in.SMTP = checkLeg("SMTP", outErr, m.Config.SMTPHost, smtpPort)
	if (!in.IMAP.OK && in.IMAP.Reason != "") || (!in.SMTP.OK && in.SMTP.Reason != "") {
		c := mailcause.Classify(in)
		out.Cause = &c
	}
	return out
}

// checkLeg is one side's result, and what the cause classifier reads from it.
func checkLeg(name string, err error, host string, port int) (mailboxTestResult, mailcause.Leg) {
	leg := mailcause.Leg{Name: name, OK: err == nil, Host: host, Port: port}
	if err == nil {
		return mailboxTestResult{OK: true}, leg
	}
	leg.Reason, leg.Detail = failureOf(err)
	return mailboxTestResult{Error: safeError(err), Detail: mailcause.Scrub(leg.Detail)}, leg
}

// failureOf reads the reason and the server's words from an IMAP or SMTP failure.
func failureOf(err error) (reason, detail string) {
	var ie *imapError
	var se *sendError
	switch {
	case errors.As(err, &ie):
		return ie.reason, ie.detail
	case errors.As(err, &se):
		return se.reason, se.detail
	case errors.Is(err, context.DeadlineExceeded):
		return mailcause.ReasonTimeout, ""
	}
	return "", ""
}

// checkSMTPFallback checks the SMTP server; when implicit TLS on 465 does not
// answer, it tries 587 with STARTTLS on the same server, since many networks block
// 465 and leave 587 open. It returns 587 when that is the port that worked.
func (s *service) checkSMTPFallback(ctx context.Context, ep smtpEndpoint) (int, error) {
	err := s.checkSMTP(ctx, ep)
	if err == nil || ep.port != smtpFallback.from || ep.mode != TLSImplicit || !silent(err) {
		return 0, err
	}
	alt := ep
	alt.port, alt.mode = smtpFallback.to, TLSStart
	altErr := s.checkSMTP(ctx, alt)
	if altErr == nil {
		return alt.port, nil
	}
	if silent(altErr) {
		return 0, err
	}
	// 587 answered: what it said is the real problem.
	var se *sendError
	if errors.As(altErr, &se) {
		cp := *se
		cp.msg = "Port 465 did not answer. On port 587: " + se.msg
		return 0, &cp
	}
	return 0, altErr
}

// smtpFallback is the port pair checkSMTPFallback moves between (tests move it).
var smtpFallback = struct{ from, to int }{465, 587}

// silent: the server did not answer at all (not a refusal, a bad name or a blocked address).
func silent(err error) bool {
	reason, detail := failureOf(err)
	return (reason == mailcause.ReasonTimeout || reason == mailcause.ReasonUnreachable) && detail != "no such host" && detail != "lookup failed"
}
