package email

import (
	"bytes"
	"fmt"
	"mime"
	"net/mail"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/jhillyerd/enmime/v2"
)

// parsedMail is the part of an inbound message the pipeline uses.
type parsedMail struct {
	env        *enmime.Envelope
	MessageID  string
	InReplyTo  string
	References []string
	Subject    string
	FromAddr   string
	FromName   string
	ReplyTo    string
	ReplyName  string
	To         []string
	Cc         []string
	HTML       string
	Text       string
	Parts      []*enmime.Part // attachments and inline parts
}

func parseMail(raw []byte) (*parsedMail, error) {
	env, err := enmime.ReadEnvelope(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	pm := &parsedMail{env: env, HTML: dropNUL(env.HTML), Text: dropNUL(env.Text)}
	pm.MessageID = normalizeMsgID(env.GetHeader("Message-Id"))
	if pm.MessageID == "" {
		pm.MessageID = headerMessageID(raw)
	}
	irt := msgIDList(env.GetHeader("In-Reply-To"))
	if len(irt) > 0 {
		pm.InReplyTo = irt[0]
	}
	pm.References = msgIDList(env.GetHeader("References"))
	if len(pm.References) > 50 {
		pm.References = pm.References[len(pm.References)-50:]
	}
	pm.Subject = strings.TrimSpace(strings.Map(dropControl, env.GetHeader("Subject")))
	if a := firstAddress(env, "From"); a != nil {
		pm.FromAddr, pm.FromName = strings.ToLower(a.Address), dropNUL(a.Name)
	}
	if a := firstAddress(env, "Reply-To"); a != nil {
		pm.ReplyTo, pm.ReplyName = strings.ToLower(a.Address), dropNUL(a.Name)
	}
	pm.To = addresses(env, "To")
	pm.Cc = addresses(env, "Cc")
	seen := map[*enmime.Part]bool{}
	for _, list := range [][]*enmime.Part{env.Inlines, env.Attachments, env.OtherParts} {
		for _, p := range list {
			if p == nil || seen[p] || len(p.Content) == 0 || strings.HasPrefix(p.ContentType, "multipart/") {
				continue
			}
			seen[p] = true
			pm.Parts = append(pm.Parts, p)
		}
	}
	return pm, nil
}

// dropNUL removes NUL bytes, which Postgres text cannot hold and mail can carry (a stray
// byte in a text part, an encoded name): nothing an inbound email saves keeps one, so such
// an email becomes a ticket like any other.
func dropNUL(s string) string {
	if strings.IndexByte(s, 0) < 0 {
		return s
	}
	return strings.ReplaceAll(s, "\x00", "")
}

func firstAddress(env *enmime.Envelope, key string) *mail.Address {
	list, err := env.AddressList(key)
	if err == nil && len(list) > 0 && validAddress(strings.ToLower(list[0].Address)) {
		return list[0]
	}
	// Fall back to a lenient parse of the raw header.
	v := env.GetHeader(key)
	if m := looseAddrRe.FindString(v); m != "" && validAddress(strings.ToLower(m)) {
		name := strings.TrimSpace(strings.Trim(strings.SplitN(v, "<", 2)[0], `" `))
		if strings.Contains(name, "@") {
			name = ""
		}
		return &mail.Address{Name: name, Address: m}
	}
	return nil
}

var looseAddrRe = regexp.MustCompile(`[^\s<>"',;:]+@[^\s<>"',;:]+\.[^\s<>"',;:]+`)

func addresses(env *enmime.Envelope, key string) []string {
	list, err := env.AddressList(key)
	if err != nil {
		return []string{}
	}
	out := []string{}
	for _, a := range list {
		if addr := strings.ToLower(a.Address); validAddress(addr) && len(out) < 50 {
			out = append(out, addr)
		}
	}
	return out
}

// sender is who wrote the message: the From address, which is who the contact is and
// what every threading check compares. Reply-To is only where the sender asks replies to
// go (replyTo).
func (pm *parsedMail) sender() (addr, name string) {
	return pm.FromAddr, pm.FromName
}

// replyTo is the Reply-To address when it differs from From, else "".
func (pm *parsedMail) replyTo() string {
	if pm.ReplyTo == "" || strings.EqualFold(pm.ReplyTo, pm.FromAddr) || !validAddress(pm.ReplyTo) {
		return ""
	}
	return pm.ReplyTo
}

func (pm *parsedMail) header(key string) string { return pm.env.GetHeader(key) }

func (pm *parsedMail) headerValues(key string) []string {
	if pm.env == nil {
		return nil
	}
	return pm.env.GetHeaderValues(key)
}

// noiseReason returns why a message is machine-generated (and must not open a ticket
// or trigger replies), or "". null is whether the envelope sender is the null sender.
func noiseReason(pm *parsedMail, ownAddrs []string, mailboxEmail string, null bool) string {
	from := pm.FromAddr
	for _, a := range ownAddrs {
		if from != "" && strings.EqualFold(from, a) {
			return "from_own_mailbox"
		}
	}
	if systemSender(pm) || null && isDeliveryReport(pm) {
		return "bounce"
	}
	if v := strings.ToLower(strings.TrimSpace(pm.header("Auto-Submitted"))); v != "" && v != "no" {
		return "auto_submitted"
	}
	switch strings.ToLower(strings.TrimSpace(pm.header("Precedence"))) {
	case "bulk", "junk", "auto_reply":
		return "precedence"
	case "list":
		// A group address that redistributes the customer's own support mail (Google
		// Groups) marks it as list mail; only foreign lists are noise.
		if !ownList(pm.header("List-Id"), mailboxEmail) {
			return "precedence"
		}
	}
	if pm.header("X-Autoreply") != "" || pm.header("X-Autorespond") != "" {
		return "auto_reply"
	}
	if pm.header("X-Auto-Response-Suppress") != "" && looksAutomatic(pm) {
		return "auto_reply"
	}
	return ""
}

// looksAutomatic narrows X-Auto-Response-Suppress, which Outlook also sets on some
// person-sent mail: it counts together with an auto-reply subject or OOF markers.
func looksAutomatic(pm *parsedMail) bool {
	s := strings.ToLower(pm.Subject)
	for _, p := range []string{"automatic reply", "auto reply", "auto-reply", "autoreply", "out of office", "out of the office",
		"abwesenheit", "réponse automatique", "respuesta automática", "risposta automatica"} {
		if strings.Contains(s, p) {
			return true
		}
	}
	return pm.header("X-MS-Exchange-Generated-Message-Source") != "" || pm.header("X-MS-Exchange-Inbox-Rules-Loop") != ""
}

func ownList(listID, mailboxEmail string) bool {
	if listID == "" || mailboxEmail == "" {
		return false
	}
	want := strings.ToLower(strings.Replace(mailboxEmail, "@", ".", 1))
	id := strings.ToLower(listID)
	if m := msgIDRe.FindStringSubmatch(id); m != nil {
		id = m[1]
	}
	return strings.TrimSpace(id) == want
}

// systemSender reports mail from a mail system's own address, which never reads replies.
func systemSender(pm *parsedMail) bool {
	local, _, _ := strings.Cut(pm.FromAddr, "@")
	switch strings.ToLower(local) {
	case "mailer-daemon", "postmaster":
		return true
	}
	return false
}

// isDeliveryReport reports a message shaped as a delivery status notification (RFC 3464).
// It is a bounce only with the null envelope sender.
func isDeliveryReport(pm *parsedMail) bool {
	ct := pm.header("Content-Type")
	mt, params, err := mime.ParseMediaType(ct)
	return err == nil && mt == "multipart/report" && strings.EqualFold(params["report-type"], "delivery-status")
}

var (
	ourMsgIDRe     = regexp.MustCompile(`<[ma]_([0-9a-f-]{36})\.t(\d{1,18})@[^<>\s]+>`)
	bodyMsgIDRe    = regexp.MustCompile(`(?im)^message-id:\s*(<[^<>\s]+>)`)
	dsnStatusRe    = regexp.MustCompile(`(?im)^status:\s*([245]\.\d{1,3}\.\d{1,3})`)
	dsnDiagRe      = regexp.MustCompile(`(?im)^diagnostic-code:\s*(?:smtp;\s*)?(.+)$`)
	dsnRecipientRe = regexp.MustCompile(`(?im)^final-recipient:\s*(?:rfc822;\s*)?(\S+)`)
)

// bounceRefs lists the Message-IDs a bounce refers to: its threading headers plus any
// Message-ID header quoted in the returned message.
func bounceRefs(pm *parsedMail, raw []byte) []string {
	ids := append([]string{}, pm.References...)
	if pm.InReplyTo != "" {
		ids = append(ids, pm.InReplyTo)
	}
	body := raw
	if i := bytes.Index(raw, []byte("\r\n\r\n")); i >= 0 {
		body = raw[i:]
	}
	for _, m := range bodyMsgIDRe.FindAllSubmatch(body, 20) {
		ids = append(ids, string(m[1]))
	}
	for _, m := range ourMsgIDRe.FindAll(body, 20) {
		ids = append(ids, string(m))
	}
	return msgIDList(strings.Join(ids, " "))
}

// bounceDetail is a short, safe description of why delivery failed.
func bounceDetail(raw []byte) string {
	var parts []string
	if m := dsnRecipientRe.FindSubmatch(raw); m != nil {
		parts = append(parts, "recipient "+cleanLine(string(m[1]), 120))
	}
	if m := dsnStatusRe.FindSubmatch(raw); m != nil {
		parts = append(parts, "status "+string(m[1]))
	}
	if m := dsnDiagRe.FindSubmatch(raw); m != nil {
		parts = append(parts, cleanLine(string(m[1]), 200))
	}
	if len(parts) == 0 {
		return "The recipient's mail server returned the message."
	}
	return "The recipient's mail server returned the message: " + strings.Join(parts, "; ")
}

func cleanLine(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return truncate(strings.TrimSpace(s), n)
}

var subjectTagRe = regexp.MustCompile(`\[#(\d{1,18})\]`)

// subjectTicketNumber reads a "[#123]" tag.
func subjectTicketNumber(subject string) (int64, bool) {
	m := subjectTagRe.FindStringSubmatch(subject)
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	return n, err == nil && n > 0
}

// plusTicketNumber reads "t123" from a "+t123" address tag.
func plusTicketNumber(tag string) (int64, bool) {
	if len(tag) < 2 || tag[0] != 't' {
		return 0, false
	}
	n, err := strconv.ParseInt(tag[1:], 10, 64)
	return n, err == nil && n > 0
}

var replyPrefixRe = regexp.MustCompile(`(?i)^\s*((re|aw|sv|antw|vs|ref|odp|fwd?|fw|tr|wg|rv|enc)\s*(\[\d+\])?\s*:\s*)+`)

// baseSubject drops reply prefixes and ticket tags.
func baseSubject(s string) string {
	s = subjectTagRe.ReplaceAllString(s, "")
	s = replyPrefixRe.ReplaceAllString(s, "")
	return strings.Join(strings.Fields(s), " ")
}

// replySubject is "Re: <subject> [#n]".
func replySubject(subject string, number int64, reply bool) string {
	base := baseSubject(subject)
	if base == "" {
		base = "Your request"
	}
	out := fmt.Sprintf("%s [#%d]", base, number)
	if reply {
		out = "Re: " + out
	}
	return out
}

// cleanFilename keeps a safe base name for a stored attachment.
func cleanFilename(name, contentType string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`/\:*?"<>|`, r) {
			return -1
		}
		return r
	}, name)
	name = strings.Trim(name, ". ")
	if name == "" {
		name = "attachment"
		if exts, _ := mime.ExtensionsByType(contentType); len(exts) > 0 {
			name += exts[0]
		}
	}
	return truncate(name, 200)
}
