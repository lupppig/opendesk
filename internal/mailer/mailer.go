// Package mailer sends opendesk's own platform email (verification, password reset,
// invites, notification emails) and renders it with one shared layout (Render).
// Customer replies go through mailboxes, not here.
package mailer

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	sestypes "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"github.com/useopendesk/opendesk/internal/crypto"
)

type Mail struct {
	To      string
	Subject string
	HTML    string
	Text    string
	// Headers are extra headers, such as List-Unsubscribe and References. Names are
	// limited to allowedHeaders.
	Headers map[string]string
}

// allowedHeaders are the extra headers a Mail may carry.
var allowedHeaders = []string{"List-Unsubscribe", "List-Unsubscribe-Post", "References", "In-Reply-To", "X-Entity-Ref-ID"}

// extraHeaders returns m's allowed headers in a fixed order, values flattened to one line.
func extraHeaders(m Mail) [][2]string {
	var out [][2]string
	for _, k := range allowedHeaders {
		if v := HeaderSafe(m.Headers[k]); v != "" {
			out = append(out, [2]string{k, v})
		}
	}
	return out
}

type Mailer interface {
	Send(ctx context.Context, m Mail) error
}

type Options struct {
	Driver   string // log | smtp | ses
	From     string
	Host     string
	Port     int
	Username string
	Password string
	TLS      string // tls | starttls | none
	Region   string
}

func New(ctx context.Context, o Options, log *slog.Logger) (Mailer, error) {
	if _, err := mail.ParseAddress(o.From); err != nil {
		return nil, fmt.Errorf("MAIL_FROM: %w", err)
	}
	switch o.Driver {
	case "", "log":
		return &logMailer{log: log}, nil
	case "smtp":
		if o.Host == "" {
			return nil, errors.New("SMTP_HOST is required for MAIL_DRIVER=smtp")
		}
		return reserved{&smtpMailer{o: o}, log}, nil
	case "ses":
		cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(o.Region))
		if err != nil {
			return nil, err
		}
		return reserved{&sesMailer{client: sesv2.NewFromConfig(cfg), from: o.From}, log}, nil
	}
	return nil, fmt.Errorf("unknown MAIL_DRIVER %q", o.Driver)
}

// Undeliverable reports an address under the reserved .invalid top-level domain (RFC
// 2606), which test accounts use (contracts/admin.md "Test workspaces"): it can never
// receive mail, so nothing is sent to it.
func Undeliverable(addr string) bool {
	a := strings.TrimSpace(addr)
	if p, err := mail.ParseAddress(a); err == nil {
		a = p.Address
	}
	return strings.HasSuffix(strings.ToLower(a), ".invalid")
}

// reserved skips mail to undeliverable addresses instead of handing it to the provider.
type reserved struct {
	Mailer
	log *slog.Logger
}

func (r reserved) Send(ctx context.Context, m Mail) error {
	if Undeliverable(m.To) {
		r.log.Info("platform mail to a reserved .invalid address skipped")
		return nil
	}
	return r.Mailer.Send(ctx, m)
}

type logMailer struct{ log *slog.Logger }

// Send logs the mail in development. The body is logged because it is the only way to
// click a verification link locally; never use this driver in production.
func (l *logMailer) Send(_ context.Context, m Mail) error {
	l.log.Info("mail (log driver)", "to", m.To, "subject", m.Subject, "text", m.Text)
	return nil
}

type sesMailer struct {
	client *sesv2.Client
	from   string
}

func (s *sesMailer) Send(ctx context.Context, m Mail) error {
	var headers []sestypes.MessageHeader
	for _, h := range extraHeaders(m) {
		headers = append(headers, sestypes.MessageHeader{Name: aws.String(h[0]), Value: aws.String(h[1])})
	}
	_, err := s.client.SendEmail(ctx, &sesv2.SendEmailInput{
		FromEmailAddress: aws.String(s.from),
		Destination:      &sestypes.Destination{ToAddresses: []string{m.To}},
		Content: &sestypes.EmailContent{Simple: &sestypes.Message{
			Subject: &sestypes.Content{Data: aws.String(HeaderSafe(m.Subject)), Charset: aws.String("UTF-8")},
			Body: &sestypes.Body{
				Html: &sestypes.Content{Data: aws.String(m.HTML), Charset: aws.String("UTF-8")},
				Text: &sestypes.Content{Data: aws.String(m.Text), Charset: aws.String("UTF-8")},
			},
			Headers: headers,
		}},
	})
	return err
}

type smtpMailer struct{ o Options }

func (s *smtpMailer) Send(ctx context.Context, m Mail) error {
	from, _ := mail.ParseAddress(s.o.From)
	msg := Build(s.o.From, m)
	addr := net.JoinHostPort(s.o.Host, strconv.Itoa(s.o.Port))
	var c *smtp.Client
	var err error
	switch s.o.TLS {
	case "tls":
		c, err = smtp.DialTLS(addr, &tls.Config{ServerName: s.o.Host, MinVersion: tls.VersionTLS12})
	case "none":
		c, err = smtp.Dial(addr)
	default:
		c, err = smtp.DialStartTLS(addr, &tls.Config{ServerName: s.o.Host, MinVersion: tls.VersionTLS12})
	}
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	defer c.Close()
	if s.o.Username != "" {
		if err := c.Auth(sasl.NewPlainClient("", s.o.Username, s.o.Password)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := c.SendMail(from.Address, []string{m.To}, strings.NewReader(msg)); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}
	return c.Quit()
}

// Build renders a multipart/alternative RFC 5322 message.
func Build(from string, m Mail) string {
	boundary := "od-" + crypto.RandomString(24)
	domain := "opendesk.local"
	if a, err := mail.ParseAddress(from); err == nil {
		if _, d, ok := strings.Cut(a.Address, "@"); ok {
			domain = d
		}
	}
	var b strings.Builder
	h := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	h("From", from)
	h("To", m.To)
	h("Subject", mime.QEncoding.Encode("utf-8", HeaderSafe(m.Subject)))
	h("Date", time.Now().UTC().Format(time.RFC1123Z))
	h("Message-ID", "<"+crypto.RandomString(20)+"@"+domain+">")
	h("MIME-Version", "1.0")
	h("Auto-Submitted", "auto-generated")
	for _, x := range extraHeaders(m) {
		h(x[0], x[1])
	}
	h("Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
	b.WriteString("\r\n")
	for _, part := range []struct{ ct, body string }{{"text/plain", m.Text}, {"text/html", m.HTML}} {
		b.WriteString("--" + boundary + "\r\n")
		b.WriteString("Content-Type: " + part.ct + "; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n")
		b.WriteString(strings.ReplaceAll(part.body, "\n", "\r\n") + "\r\n")
	}
	b.WriteString("--" + boundary + "--\r\n")
	return b.String()
}
