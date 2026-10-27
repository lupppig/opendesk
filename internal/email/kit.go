package email

import (
	"context"
	"crypto/tls"
	"net"
	"net/mail"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/jhillyerd/enmime/v2"
	xhtml "golang.org/x/net/html"
)

// The kit is what other areas that read and send mail reuse from this one, so a message
// is parsed, cleaned, built and connected for exactly as here (Collab, contracts/collab.md).

// Parsed is a parsed message (ParseMail).
type Parsed = parsedMail

// ParseMail parses a raw RFC 822 message.
func ParseMail(raw []byte) (*Parsed, error) { return parseMail(raw) }

// Header returns a header of a parsed message.
func (pm *parsedMail) Header(key string) string {
	if pm.env == nil {
		return ""
	}
	return pm.env.GetHeader(key)
}

// AddressList returns a header's addresses with their names (To, Cc, Bcc, Reply-To),
// at most 100, each with a valid address.
func (pm *parsedMail) AddressList(key string) []mail.Address {
	out := []mail.Address{}
	if pm.env == nil {
		return out
	}
	list, err := pm.env.AddressList(key)
	if err != nil {
		return out
	}
	for _, a := range list {
		if a == nil || !validAddress(lowerASCII(a.Address)) || len(out) >= 100 {
			continue
		}
		out = append(out, mail.Address{Name: a.Name, Address: lowerASCII(a.Address)})
	}
	return out
}

// Date is the message's Date header, or the zero time.
func (pm *parsedMail) Date() time.Time {
	if pm.env == nil {
		return time.Time{}
	}
	t, err := mail.ParseDate(pm.env.GetHeader("Date"))
	if err != nil {
		return time.Time{}
	}
	return t
}

// Part is an attachment or inline part of a parsed message.
type Part = enmime.Part

// SanitizeHTML is the only way email HTML reaches the database.
func SanitizeHTML(s string) string { return sanitizeHTML(s) }

// SanitizeSettingsHTML cleans HTML people write for their mail (signatures).
func SanitizeSettingsHTML(s string) string { return sanitizeSettingsHTML(s) }

// HTMLToText renders HTML as plain text.
func HTMLToText(s string) string { return htmlToText(s) }

// TextToHTML turns plain text into simple paragraphs.
func TextToHTML(s string) string { return textToHTML(s) }

// RewriteCID points <img src="cid:..."> at urls (bare lower-case content id → URL) in an
// HTML body and returns the body and the content ids it used.
func RewriteCID(body string, urls map[string]string) (string, map[string]bool) {
	doc := parseFragment(body)
	if doc == nil {
		return body, map[string]bool{}
	}
	used := rewriteCID(doc, urls)
	if len(used) == 0 {
		return body, used
	}
	return render(doc), used
}

// NormalizeCID is a content id as RewriteCID matches it.
func NormalizeCID(s string) string { return normalizeCID(s) }

// CleanFilename keeps a safe base name for an attachment.
func CleanFilename(name, contentType string) string { return cleanFilename(name, contentType) }

// BaseSubject drops reply prefixes ("Re:", "Fwd:") and ticket tags.
func BaseSubject(s string) string { return baseSubject(s) }

// NoSubject is the subject of a ticket opened by an email without one.
const NoSubject = "(no subject)"

// NormalizeMsgID returns "<id>" from a header value, or "".
func NormalizeMsgID(v string) string { return normalizeMsgID(v) }

// MsgIDList parses In-Reply-To / References into "<id>" values, oldest first.
func MsgIDList(vals ...string) []string { return msgIDList(vals...) }

// HeaderMessageID is a raw message's Message-ID, synthesized from its content when missing.
func HeaderMessageID(raw []byte) string { return headerMessageID(raw) }

// ValidAddress reports a usable lower-case email address.
func ValidAddress(addr string) bool { return validAddress(addr) }

// OutMessage is an outbound message before encoding (BuildMIME).
type OutMessage = outMessage

// OutAttachment is a file of an OutMessage; ContentID marks an inline image.
type OutAttachment = outAttachment

// BuildMIME encodes m. includeBcc keeps the Bcc header for API transports that read
// recipients from the message (Gmail, Graph).
func BuildMIME(m *OutMessage, includeBcc bool) ([]byte, error) { return buildMIME(m, includeBcc) }

// Guard dials user-supplied mail hosts: public addresses only (unless allowPrivate, for a
// development instance), verified TLS 1.2+, bounded sessions.
type Guard struct{ g *netGuard }

// NewGuard returns a Guard; allowPrivate permits private and loopback targets.
func NewGuard(allowPrivate bool) *Guard { return &Guard{g: &netGuard{allowPrivate: allowPrivate}} }

// AllowPrivate reports whether private targets are allowed (development).
func (g *Guard) AllowPrivate() bool { return g.g.allowPrivate }

// Dial connects to host:port.
func (g *Guard) Dial(ctx context.Context, host string, port int) (net.Conn, error) {
	return g.g.dial(ctx, host, port)
}

// TLSConfig verifies host's certificate with TLS 1.2 or newer.
func (g *Guard) TLSConfig(host string) *tls.Config { return g.g.tlsConfig(host) }

// ValidHost checks a host name typed by a person.
func (g *Guard) ValidHost(host string) error { return g.g.validHost(host) }

// WithDeadline bounds a whole session by d and closes it when ctx ends; call stop after.
func WithDeadline(ctx context.Context, c net.Conn, d time.Duration) (net.Conn, func()) {
	return withDeadline(ctx, c, d)
}

// ErrBlockedAddress is a mail host that is not a public address.
var ErrBlockedAddress = errBlockedAddress

// XOAuth2 is the SASL XOAUTH2 mechanism Gmail and Outlook use for IMAP and SMTP with OAuth.
func XOAuth2(user, token string) sasl.Client { return &xoauth2{user: user, token: token} }

// ParseHTML parses an HTML body into a tree (nil when it cannot be parsed).
func ParseHTML(s string) *xhtml.Node { return parseFragment(s) }

// RenderHTML renders a tree back to HTML.
func RenderHTML(n *xhtml.Node) string { return render(n) }

func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
