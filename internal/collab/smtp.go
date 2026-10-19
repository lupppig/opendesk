package collab

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"strings"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"github.com/useopendesk/opendesk/internal/email"
)

// SMTP for IMAP accounts: TLS (implicit or STARTTLS, verified), through the guard, with
// the account's own login.

// dialSMTP connects to the account's SMTP server, greets it and logs in.
func (s *service) dialSMTP(ctx context.Context, cfg accountConfig, address string, c credentials) (*smtp.Client, func(), error) {
	host, port, mode := cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPTLS
	if host == "" || port <= 0 || port > 65535 {
		return nil, nil, &imapError{msg: "The SMTP server is not configured.", permanent: true}
	}
	if mode == "none" && !s.guard.AllowPrivate() {
		return nil, nil, &imapError{msg: "The SMTP connection must use TLS.", permanent: true}
	}
	raw, err := s.guard.Dial(ctx, host, port)
	if err != nil {
		return nil, nil, dialError("SMTP", err)
	}
	conn, stop := email.WithDeadline(ctx, raw, sessionTimeout)
	var client *smtp.Client
	switch mode {
	case "none":
		client = smtp.NewClient(conn)
	case "starttls":
		client, err = smtp.NewClientStartTLS(conn, s.guard.TLSConfig(host))
		if err != nil {
			_ = conn.Close()
			stop()
			return nil, nil, smtpFailure("starting TLS", err)
		}
	default:
		tc := tls.Client(conn, s.guard.TLSConfig(host))
		if err := tc.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			stop()
			return nil, nil, &imapError{msg: "Could not establish TLS with the SMTP server: " + tlsReason(err) + "."}
		}
		client = smtp.NewClient(tc)
	}
	if mode != "starttls" {
		if err := client.Hello(heloName(s.d.Cfg.APIURL)); err != nil {
			_ = client.Close()
			stop()
			return nil, nil, smtpFailure("greeting", err)
		}
	}
	user := cfg.SMTPUsername
	if user == "" {
		user = cfg.IMAPUsername
	}
	if user == "" {
		user = address
	}
	if err := smtpLogin(client, user, c.smtpPassword()); err != nil {
		_ = client.Close()
		stop()
		return nil, nil, err
	}
	return client, stop, nil
}

func smtpLogin(c *smtp.Client, user, pass string) error {
	if ok, _ := c.Extension("AUTH"); !ok {
		return &imapError{msg: "The SMTP server does not offer login on this connection.", permanent: true}
	}
	var mech sasl.Client
	switch {
	case c.SupportsAuth(sasl.Plain):
		mech = sasl.NewPlainClient("", user, pass)
	case c.SupportsAuth(sasl.Login):
		mech = sasl.NewLoginClient(user, pass)
	default:
		return &imapError{msg: "The SMTP server offers no supported login method (PLAIN or LOGIN).", permanent: true}
	}
	if err := c.Auth(mech); err != nil {
		var se *smtp.SMTPError
		if errors.As(err, &se) && se.Code >= 500 && se.Code != 502 && se.Code != 503 && se.Code != 504 {
			return &imapError{msg: "The SMTP server refused the login: " + serverText(se.Message), auth: true}
		}
		return smtpFailure("login", err)
	}
	return nil
}

// smtpFailure classifies an SMTP error: 5xx is permanent, everything else passing.
func smtpFailure(stage string, err error) error {
	var se *smtp.SMTPError
	if errors.As(err, &se) {
		return &imapError{msg: fmt.Sprintf("The SMTP server refused the message during %s: %d %s", stage, se.Code, serverText(se.Message)),
			permanent: se.Code >= 500}
	}
	if isTimeout(err) {
		return &imapError{msg: "The SMTP server stopped answering during " + stage + "."}
	}
	var cv *tls.CertificateVerificationError
	if errors.As(err, &cv) {
		return &imapError{msg: "The SMTP server's certificate is not valid for its host name.", permanent: true}
	}
	return &imapError{msg: "The SMTP connection dropped during " + stage + "."}
}

// sendSMTP submits a message from the account to its recipients.
func (s *service) sendSMTP(ctx context.Context, cfg accountConfig, address string, c credentials, info SendInfo, raw []byte) error {
	if len(info.Recipients) == 0 {
		return &imapError{msg: "Add at least one recipient.", permanent: true}
	}
	client, stop, err := s.dialSMTP(ctx, cfg, address, c)
	if err != nil {
		return err
	}
	defer stop()
	defer client.Close()
	from := info.From
	if from == "" {
		from = address
	}
	if err := client.SendMail(from, info.Recipients, bytes.NewReader(raw)); err != nil {
		return smtpFailure("sending", err)
	}
	_ = client.Quit()
	return nil
}

// checkSMTP logs in to the account's SMTP server and leaves.
func (s *service) checkSMTP(ctx context.Context, cfg accountConfig, address string, c credentials) error {
	client, stop, err := s.dialSMTP(ctx, cfg, address, c)
	if err != nil {
		return err
	}
	_ = client.Quit()
	_ = client.Close()
	stop()
	return nil
}

// heloName is this instance's host name for EHLO.
func heloName(apiURL string) string {
	host := strings.TrimPrefix(strings.TrimPrefix(apiURL, "https://"), "http://")
	if i := strings.IndexAny(host, ":/"); i >= 0 {
		host = host[:i]
	}
	if host == "" {
		return "localhost"
	}
	return host
}
