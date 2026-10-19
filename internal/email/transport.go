package email

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/mail"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	sestypes "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"github.com/useopendesk/opendesk/internal/email/mailcause"
)

// sendError is a delivery failure with a message that is safe to store and show.
// Permanent failures are not retried.
type sendError struct {
	msg       string
	permanent bool
	// reason (mailcause.Reason*) and detail, the server's own words, explain a
	// failed connection check; detail is scrubbed before anyone sees it.
	reason string
	detail string
}

func (e *sendError) Error() string { return e.msg }

func permanent(msg string) error { return &sendError{msg: msg, permanent: true} }
func transient(msg string) error { return &sendError{msg: msg} }

// graphMaxRequest is Graph's sendMail request limit; the MIME body is base64 encoded.
const graphMaxRequest = 4 << 20

// envelope is what a transport needs: the MAIL FROM, every recipient and the message.
type envelope struct {
	from  string
	rcpts []string
	raw   []byte
}

// smtpEndpoint is one SMTP server to submit to.
type smtpEndpoint struct {
	host, mode   string
	port         int
	user, pass   string
	guarded      bool // user-supplied host: SSRF guard applies
	helo         string
	allowNoTLS   bool
	authRequired bool
}

// sendSMTP submits through an SMTP server with TLS (implicit or STARTTLS, verified).
func (s *service) sendSMTP(ctx context.Context, ep smtpEndpoint, env *envelope) error {
	c, stop, err := s.dialSMTP(ctx, ep)
	if err != nil {
		return err
	}
	defer stop()
	defer c.Close()
	if err := s.smtpAuth(c, ep); err != nil {
		return err
	}
	if err := c.SendMail(env.from, env.rcpts, bytes.NewReader(env.raw)); err != nil {
		return smtpFailure("sending", err)
	}
	_ = c.Quit()
	return nil
}

func (s *service) dialSMTP(ctx context.Context, ep smtpEndpoint) (*smtp.Client, func(), error) {
	if ep.mode == TLSNone && !ep.allowNoTLS {
		return nil, nil, &sendError{msg: "The SMTP connection must use TLS.", permanent: true, reason: mailcause.ReasonCleartext}
	}
	var raw net.Conn
	var err error
	if ep.guarded {
		raw, err = s.guard.dial(ctx, ep.host, ep.port)
	} else {
		d := &net.Dialer{Timeout: dialTimeout}
		raw, err = d.DialContext(ctx, "tcp", net.JoinHostPort(ep.host, strconv.Itoa(ep.port)))
	}
	if err != nil {
		var ie *imapError
		if errors.As(dialError("SMTP", err), &ie) {
			return nil, nil, &sendError{msg: ie.msg, permanent: errors.Is(err, errBlockedAddress), reason: ie.reason, detail: ie.detail}
		}
		return nil, nil, transient("Could not connect to the SMTP server.")
	}
	conn, stop := withDeadline(ctx, raw, sessionTimeout)
	tlsCfg := s.guard.tlsConfig(ep.host)
	var c *smtp.Client
	switch ep.mode {
	case TLSNone:
		c = smtp.NewClient(conn)
	case TLSStart:
		c, err = smtp.NewClientStartTLS(conn, tlsCfg)
		if err != nil {
			_ = conn.Close()
			stop()
			if se := smtpFailure("starting TLS", err).(*sendError); se.reason == mailcause.ReasonProtocol {
				se.reason = mailcause.ReasonTLS
				return nil, nil, se
			}
			return nil, nil, smtpFailure("starting TLS", err)
		}
	default:
		tc := tls.Client(conn, tlsCfg)
		if err := tc.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			stop()
			return nil, nil, &sendError{msg: "Could not establish TLS with the SMTP server: " + tlsReason(err), reason: mailcause.ReasonTLS, detail: tlsReason(err)}
		}
		c = smtp.NewClient(tc)
	}
	if ep.mode != TLSStart { // NewClientStartTLS has already greeted
		if err := c.Hello(ep.helo); err != nil {
			_ = c.Close()
			stop()
			return nil, nil, smtpFailure("greeting", err)
		}
	}
	return c, stop, nil
}

func (s *service) smtpAuth(c *smtp.Client, ep smtpEndpoint) error {
	if ep.user == "" {
		if ep.authRequired {
			return permanent("Enter the SMTP username and password.")
		}
		return nil
	}
	if ok, _ := c.Extension("AUTH"); !ok {
		if ep.authRequired {
			return &sendError{msg: "The SMTP server does not offer login on this connection.", permanent: true,
				reason: mailcause.ReasonAuthUnavailable, detail: "server doesn't support AUTH"}
		}
		return nil
	}
	var mech sasl.Client
	switch {
	case c.SupportsAuth(sasl.Plain):
		mech = sasl.NewPlainClient("", ep.user, ep.pass)
	case c.SupportsAuth(sasl.Login):
		mech = sasl.NewLoginClient(ep.user, ep.pass)
	default:
		return &sendError{msg: "The SMTP server offers no supported login method (PLAIN or LOGIN).", permanent: true,
			reason: mailcause.ReasonAuthUnavailable, detail: "authentication mechanism not supported"}
	}
	if err := c.Auth(mech); err != nil {
		var se *smtp.SMTPError
		if errors.As(err, &se) && se.Code >= 500 {
			reason := mailcause.ReasonAuthRefused
			if se.Code == 504 || se.Code == 502 || se.Code == 503 {
				reason = mailcause.ReasonProtocol // the mechanism, not the password
			}
			return &sendError{msg: "The SMTP server refused the login: " + truncate(cleanLine(se.Message, 200), 200), permanent: true,
				reason: reason, detail: smtpDetail(se)}
		}
		return smtpFailure("login", err)
	}
	return nil
}

// smtpFailure classifies an SMTP error: 5xx is permanent, everything else is retried.
func smtpFailure(stage string, err error) error {
	var se *smtp.SMTPError
	if errors.As(err, &se) {
		msg := fmt.Sprintf("The SMTP server refused the message during %s: %d %s", stage, se.Code, cleanLine(se.Message, 200))
		if se.Code >= 500 {
			return &sendError{msg: msg, permanent: true, reason: mailcause.ReasonProtocol, detail: smtpDetail(se)}
		}
		return &sendError{msg: msg, reason: mailcause.ReasonTemporary, detail: smtpDetail(se)}
	}
	if isTimeout(err) {
		return &sendError{msg: "The SMTP server stopped answering during " + stage + ".", reason: mailcause.ReasonTimeout}
	}
	var cv *tls.CertificateVerificationError
	if errors.As(err, &cv) {
		return &sendError{msg: "The SMTP server's certificate is not valid for its host name.", permanent: true,
			reason: mailcause.ReasonTLS, detail: "the certificate is not valid for this host"}
	}
	return &sendError{msg: "The SMTP connection dropped during " + stage + ".", reason: mailcause.ReasonProtocol}
}

// smtpDetail is a server reply as the server said it: "535 5.7.8 text".
func smtpDetail(se *smtp.SMTPError) string {
	out := strconv.Itoa(se.Code)
	if e := se.EnhancedCode; e != smtp.NoEnhancedCode && e != (smtp.EnhancedCode{}) {
		out += fmt.Sprintf(" %d.%d.%d", e[0], e[1], e[2])
	}
	return truncate(out+" "+cleanLine(se.Message, 400), 400)
}

// apiCall runs one provider HTTPS request, refreshing the token once on 401.
func (s *service) apiCall(ctx context.Context, m *mailboxRow, resource string, build func(token string) (*http.Request, error)) (*http.Response, []byte, error) {
	return s.apiCallLimit(ctx, m, resource, 1<<20, build)
}

// apiCallLimit is apiCall reading at most limit bytes of the answer (limit+1 tells a
// longer one apart).
func (s *service) apiCallLimit(ctx context.Context, m *mailboxRow, resource string, limit int64,
	build func(token string) (*http.Request, error)) (*http.Response, []byte, error) {
	for attempt := 0; attempt < 2; attempt++ {
		token, err := s.accessToken(ctx, m, resource)
		if err != nil {
			if errors.Is(err, errReconnect) {
				return nil, nil, permanent(err.Error())
			}
			if errors.Is(err, errProviderUnavailable) {
				return nil, nil, transient(err.Error())
			}
			return nil, nil, permanent(safeError(err))
		}
		req, err := build(token)
		if err != nil {
			return nil, nil, err
		}
		resp, err := s.http.Do(req)
		if err != nil {
			return nil, nil, transient("The email provider did not answer.")
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, limit+1))
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			if err := s.expireToken(ctx, m, resource); err != nil {
				return nil, nil, err
			}
			continue
		}
		return resp, body, nil
	}
	return nil, nil, permanent("The email provider refused the mailbox token. Reconnect the mailbox.")
}

// expireToken forgets a cached access token the provider just refused.
func (s *service) expireToken(ctx context.Context, m *mailboxRow, resource string) error {
	c, err := s.openCreds(m)
	if err != nil {
		return err
	}
	if resource == resGraph && m.oauthProvider() == KindMicrosoft {
		c.GraphExpiry = time.Time{}
	} else {
		c.Expiry = time.Time{}
	}
	sealed, err := s.sealCreds(c)
	if err != nil {
		return err
	}
	if _, err := s.d.DB.Pool.Exec(ctx, `UPDATE mailboxes SET credentials_enc = $2 WHERE id = $1`, m.ID, sealed); err != nil {
		return err
	}
	m.CredentialsEnc = sealed
	return nil
}

// providerFailure turns a provider HTTP error into a safe, classified error.
func providerFailure(provider string, status int, body []byte) error {
	var e struct {
		Error struct {
			Code    any    `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &e)
	detail := cleanLine(e.Error.Message, 200)
	msg := fmt.Sprintf("%s refused the message (%d)", provider, status)
	if detail != "" {
		msg += ": " + detail
	}
	switch {
	case status == http.StatusTooManyRequests || status >= 500:
		return transient(msg)
	case status == http.StatusForbidden:
		return permanent(msg + ". Reconnect the mailbox and grant the send permission.")
	}
	return permanent(msg)
}

// sendGmail sends through users.messages.send (an upload, up to 35 MB). Gmail also
// files the reply in the mailbox's Sent folder. With threadID (a Gmail thread of the
// mailbox's account) the message joins that conversation: the upload is multipart, with
// the thread in its metadata. It returns the Message-ID the recipient sees, which Gmail
// may have re-stamped, and, for an api grant, where the sent message lives.
func (s *service) sendGmail(ctx context.Context, m *mailboxRow, raw []byte, threadID string) (string, *providerRef, error) {
	target := s.gmailBase + "/upload/gmail/v1/users/me/messages/send?uploadType=media"
	contentType, payload := "message/rfc822", raw
	if threadID != "" {
		var b bytes.Buffer
		mw := multipart.NewWriter(&b)
		meta, err := json.Marshal(map[string]string{"threadId": threadID})
		if err != nil {
			return "", nil, err
		}
		for _, part := range []struct {
			ct   string
			body []byte
		}{{"application/json; charset=UTF-8", meta}, {"message/rfc822", raw}} {
			w, err := mw.CreatePart(textproto.MIMEHeader{"Content-Type": {part.ct}})
			if err != nil {
				return "", nil, err
			}
			if _, err := w.Write(part.body); err != nil {
				return "", nil, err
			}
		}
		if err := mw.Close(); err != nil {
			return "", nil, err
		}
		target = s.gmailBase + "/upload/gmail/v1/users/me/messages/send?uploadType=multipart"
		contentType, payload = "multipart/related; boundary="+mw.Boundary(), b.Bytes()
	}
	resp, body, err := s.apiCall(ctx, m, resIMAP, func(token string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", contentType)
		return req, nil
	})
	if err != nil {
		return "", nil, err
	}
	if resp.StatusCode/100 != 2 {
		return "", nil, providerFailure("Gmail", resp.StatusCode, body)
	}
	var sent struct {
		ID       string `json:"id"`
		ThreadID string `json:"threadId"`
	}
	// A send grant cannot read the sent message back; replies then thread through our
	// own Message-ID, the reply address tag and the [#n] subject.
	if json.Unmarshal(body, &sent) != nil || sent.ID == "" || m.sendOnly() {
		return "", nil, nil
	}
	var ref *providerRef
	if m.sendsAPI() {
		ref = newProviderRef(m, sent.ID, sent.ThreadID)
	}
	return s.gmailStampedID(ctx, m, sent.ID), ref, nil
}

func (s *service) gmailStampedID(ctx context.Context, m *mailboxRow, id string) string {
	resp, body, err := s.apiCall(ctx, m, resIMAP, func(token string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.gmailBase+"/gmail/v1/users/me/messages/"+url.PathEscape(id)+
			"?format=metadata&metadataHeaders=Message-ID", nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		return req, nil
	})
	if err != nil || resp.StatusCode != http.StatusOK {
		return ""
	}
	var msg struct {
		Payload struct {
			Headers []struct{ Name, Value string } `json:"headers"`
		} `json:"payload"`
	}
	if json.Unmarshal(body, &msg) != nil {
		return ""
	}
	for _, h := range msg.Payload.Headers {
		if strings.EqualFold(h.Name, "Message-ID") {
			return normalizeMsgID(h.Value)
		}
	}
	return ""
}

// sendGraph sends through Microsoft Graph POST /me/sendMail with a MIME body. Exchange
// files it in Sent Items and stamps its own Message-ID; replies still thread through
// References and the [#n] subject tag.
func (s *service) sendGraph(ctx context.Context, m *mailboxRow, raw []byte) error {
	encoded := base64.StdEncoding.EncodeToString(raw)
	if len(encoded) > graphMaxRequest {
		return permanent("Microsoft accepts messages up to about 3 MB (attachments included) through this connection.")
	}
	resp, body, err := s.apiCall(ctx, m, resGraph, func(token string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.graphBase+"/me/sendMail", strings.NewReader(encoded))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "text/plain")
		return req, nil
	})
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return providerFailure("Microsoft", resp.StatusCode, body)
	}
	return nil
}

var (
	sesOnce   sync.Once
	sesClient *sesv2.Client
	sesErr    error
)

// sendSystem sends through the platform transport (MAIL_DRIVER) with our own MIME,
// so threading headers and Reply-To survive.
func (s *service) sendSystem(ctx context.Context, env *envelope) error {
	if s.rawSend != nil {
		if err := s.rawSend(ctx, env.from, env.rcpts, env.raw); err != nil {
			return transient("The platform mailer failed: " + safeError(err))
		}
		return nil
	}
	cfg := s.d.Cfg
	switch cfg.MailDriver {
	case "smtp":
		return s.sendSMTP(ctx, smtpEndpoint{host: cfg.SMTPHost, port: cfg.SMTPPort, mode: cfg.SMTPTLS, user: cfg.SMTPUsername,
			pass: cfg.SMTPPassword, helo: s.heloName(), allowNoTLS: true}, env)
	case "ses":
		sesOnce.Do(func() {
			c, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion(cfg.SESRegion))
			if err != nil {
				sesErr = err
				return
			}
			sesClient = sesv2.NewFromConfig(c)
		})
		if sesErr != nil {
			return transient("The platform mailer is not available.")
		}
		_, err := sesClient.SendEmail(ctx, &sesv2.SendEmailInput{
			FromEmailAddress: &env.from,
			Destination:      &sestypes.Destination{ToAddresses: env.rcpts},
			Content:          &sestypes.EmailContent{Raw: &sestypes.RawMessage{Data: env.raw}},
		})
		if err != nil {
			return transient("The platform mailer refused the message.")
		}
		return nil
	default:
		s.log.Info("system email (log driver)", "recipients", len(env.rcpts), "bytes", len(env.raw))
		return nil
	}
}

// systemFrom is the platform sender address (MAIL_FROM).
func (s *service) systemFrom() mail.Address {
	if a, err := mail.ParseAddress(s.d.Cfg.MailFrom); err == nil {
		return *a
	}
	return mail.Address{Address: "notifications@localhost"}
}

func (s *service) heloName() string {
	if u, err := url.Parse(s.d.Cfg.APIURL); err == nil && u.Hostname() != "" && u.Hostname() != "localhost" {
		return u.Hostname()
	}
	return "opendesk.localhost"
}
