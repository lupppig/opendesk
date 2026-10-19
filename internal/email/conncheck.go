package email

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
)

var timeNow = time.Now

// safeError renders an error for storage and display. The pipeline's own errors are
// written to be shown; anything else gets a generic message.
func safeError(err error) string {
	if err == nil {
		return ""
	}
	var ie *imapError
	var se *sendError
	var ae *apiError
	switch {
	case errors.As(err, &ie):
		return cleanLine(ie.msg, 300)
	case errors.As(err, &se):
		return cleanLine(se.msg, 300)
	case errors.As(err, &ae):
		return cleanLine(ae.msg, 300)
	case errors.Is(err, errReconnect), errors.Is(err, errProviderUnavailable):
		return err.Error()
	case errors.Is(err, context.DeadlineExceeded):
		return "Timed out talking to the mail server."
	}
	msg := err.Error()
	if strings.HasPrefix(msg, "the provider") || strings.HasPrefix(msg, "the Google") || strings.HasSuffix(msg, "is not configured on this instance") {
		return cleanLine(msg, 300)
	}
	return "Something went wrong talking to the mail server."
}

// testInbound checks that new mail can be read.
func (s *service) testInbound(ctx context.Context, m *mailboxRow) error {
	if m.InboundKind == InboundForwarding {
		if s.d.Cfg.InboundSecret == "" && s.d.Cfg.InboundSMTPAddr == "" {
			return &imapError{msg: "Inbound forwarding is not enabled on this instance (set INBOUND_SECRET or INBOUND_SMTP_ADDR)."}
		}
		return nil
	}
	if m.readsAPI() {
		ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		return s.testAPIInbound(ctx, m)
	}
	if ok, err := s.imapReceivesNow(ctx, m); err != nil {
		return err
	} else if !ok {
		return errIMAPOff
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	sess, err := s.dialIMAP(ctx, m)
	if err != nil {
		return err
	}
	defer sess.Close()
	if _, err := sess.c.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return opError("opening INBOX", err)
	}
	return nil
}

// testOutbound checks the SMTP login or the provider token without sending anything.
func (s *service) testOutbound(ctx context.Context, m *mailboxRow) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	switch m.OutboundKind {
	case OutboundSMTP:
		creds, err := s.openCreds(m)
		if err != nil {
			return err
		}
		return s.checkSMTP(ctx, s.mailboxSMTP(m, creds))
	case KindGoogle:
		if m.sendOnly() {
			// gmail.send cannot read the profile; a fresh token proves the grant holds.
			return s.refreshGrant(ctx, m, resIMAP)
		}
		resp, body, err := s.apiCall(ctx, m, resIMAP, func(token string) (*http.Request, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.gmailBase+"/gmail/v1/users/me/profile", nil)
			if err == nil {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			return req, err
		})
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusOK {
			return providerFailure("Gmail", resp.StatusCode, body)
		}
		return nil
	case KindMicrosoft:
		resp, body, err := s.apiCall(ctx, m, resGraph, func(token string) (*http.Request, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.graphBase+"/me?$select=id", nil)
			if err == nil {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			return req, err
		})
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusOK {
			return providerFailure("Microsoft", resp.StatusCode, body)
		}
		return nil
	case OutboundSystem:
		return nil
	}
	return permanent("The mailbox has no way to send email.")
}

// refreshGrant asks the provider for a new access token, which it gives only while the
// mailbox's grant holds.
func (s *service) refreshGrant(ctx context.Context, m *mailboxRow, resource string) error {
	if err := s.expireToken(ctx, m, resource); err != nil {
		return err
	}
	_, err := s.accessToken(ctx, m, resource)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, errReconnect):
		return permanent(err.Error())
	case errors.Is(err, errProviderUnavailable):
		return transient(err.Error())
	}
	return permanent(safeError(err))
}

// mailboxSMTP is the mailbox's own SMTP server as configured.
func (s *service) mailboxSMTP(m *mailboxRow, creds credentials) smtpEndpoint {
	return smtpEndpoint{host: m.Config.SMTPHost, port: m.Config.SMTPPort, mode: m.Config.SMTPTLS, user: m.Config.SMTPUsername,
		pass: creds.smtpPassword(), guarded: true, helo: s.heloName(), allowNoTLS: s.guard.allowPrivate, authRequired: true}
}

// checkSMTP signs in to an SMTP server and leaves without sending anything.
func (s *service) checkSMTP(ctx context.Context, ep smtpEndpoint) error {
	c, stop, err := s.dialSMTP(ctx, ep)
	if err != nil {
		return err
	}
	defer stop()
	defer c.Close()
	if err := s.smtpAuth(c, ep); err != nil {
		return err
	}
	_ = c.Quit()
	return nil
}
