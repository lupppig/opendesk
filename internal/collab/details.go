package collab

import (
	"bytes"
	"mime"
	"net/mail"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// How a message came, read from its source: who sent it on the wire, who signed it, whether
// the receiving server could verify the sender, and the headers themselves. Only what the
// receiving server stamped counts as a check (trustedAuthResults); text a sender writes into
// its own headers never reads as verified.

// MailHeader is one header of a message's source, unfolded and decoded.
type MailHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// MessageDetails answers GET /v1/collab/messages/{id}/details.
type MessageDetails struct {
	MailedBy   string       `json:"mailed_by" doc:"The envelope sender's domain"`
	SignedBy   []string     `json:"signed_by" doc:"Domains whose DKIM signature the receiving server verified"`
	Via        string       `json:"via" doc:"The domain it came through, when that is not the sender's own"`
	SPF        string       `json:"spf" doc:"The receiving server's SPF result, empty when it reported none"`
	DKIM       string       `json:"dkim"`
	DMARC      string       `json:"dmarc"`
	Encryption string       `json:"encryption" enum:"tls,none," doc:"How it reached the receiving server, empty when unknown"`
	Warning    string       `json:"warning" enum:"spoofed,unverified," doc:"spoofed: DMARC failed; unverified: neither SPF nor DKIM passed"`
	Headers    []MailHeader `json:"headers"`
}

const (
	maxHeaders     = 300
	maxHeaderValue = 8 << 10
)

// sourceHeaders reads the header block of a raw message, in order, unfolded and with
// encoded words decoded.
func sourceHeaders(raw []byte) []MailHeader {
	block := raw
	if i := bytes.Index(raw, []byte("\r\n\r\n")); i >= 0 {
		block = raw[:i]
	} else if i := bytes.Index(raw, []byte("\n\n")); i >= 0 {
		block = raw[:i]
	}
	lines := strings.Split(strings.ReplaceAll(string(block), "\r\n", "\n"), "\n")
	dec := mime.WordDecoder{}
	out := []MailHeader{}
	for _, l := range lines {
		if l == "" {
			continue
		}
		if (l[0] == ' ' || l[0] == '\t') && len(out) > 0 {
			last := &out[len(out)-1]
			if len(last.Value) < maxHeaderValue {
				last.Value += " " + strings.TrimSpace(l)
			}
			continue
		}
		name, value, ok := strings.Cut(l, ":")
		if !ok || strings.ContainsAny(name, " \t") || len(out) >= maxHeaders {
			continue
		}
		out = append(out, MailHeader{Name: name, Value: strings.TrimSpace(value)})
	}
	for i := range out {
		if d, err := dec.DecodeHeader(out[i].Value); err == nil {
			out[i].Value = d
		}
		if len(out[i].Value) > maxHeaderValue {
			out[i].Value = out[i].Value[:maxHeaderValue]
		}
		out[i].Value = strings.ToValidUTF8(out[i].Value, "�")
	}
	return out
}

// authResult is one Authentication-Results header, read.
type authResult struct {
	ServID  string
	Methods map[string]authMethod // spf, dkim, dmarc: the first of each; dkim prefers a pass
	Signers []string              // header.d (or header.i's domain) of every passing DKIM signature
}

type authMethod struct {
	Result string
	Props  map[string]string
}

var commentRe = regexp.MustCompile(`\([^()]*\)`)

func parseAuthResults(v string) authResult {
	for range 4 { // nested comments, innermost first
		v = commentRe.ReplaceAllString(v, " ")
	}
	out := authResult{Methods: map[string]authMethod{}}
	parts := strings.Split(v, ";")
	first := strings.TrimSpace(parts[0])
	// Microsoft leaves out the authserv-id: the first part is already a result.
	if !strings.Contains(first, "=") {
		if f := strings.Fields(first); len(f) > 0 {
			out.ServID = strings.ToLower(f[0])
		}
		parts = parts[1:]
	}
	for _, p := range parts {
		fields := strings.Fields(p)
		if len(fields) == 0 {
			continue
		}
		method, result, ok := strings.Cut(fields[0], "=")
		if !ok {
			continue
		}
		method, result = strings.ToLower(method), strings.ToLower(result)
		m := authMethod{Result: result, Props: map[string]string{}}
		for _, f := range fields[1:] {
			if k, val, ok := strings.Cut(f, "="); ok {
				m.Props[strings.ToLower(k)] = strings.Trim(val, `"`)
			}
		}
		if method == "dkim" && result == "pass" {
			if d := signerDomain(m.Props); d != "" && !slices.Contains(out.Signers, d) {
				out.Signers = append(out.Signers, d)
			}
		}
		if prev, seen := out.Methods[method]; !seen || (method == "dkim" && prev.Result != "pass" && result == "pass") {
			out.Methods[method] = m
		}
	}
	return out
}

func signerDomain(props map[string]string) string {
	if d := props["header.d"]; d != "" {
		return strings.ToLower(d)
	}
	if i := props["header.i"]; i != "" {
		_, d, _ := strings.Cut(i, "@")
		return strings.ToLower(d)
	}
	return ""
}

// trustedAuthResults is the receiving server's own Authentication-Results, or false when it
// left none. A sender can write the header into its mail too, so one counts only when it
// sits above a Received line, which every server the mail passed through prepends (a header
// the sender wrote is below all of them), and for Gmail only when it names mx.google.com.
// The topmost such header is the receiver's.
func trustedAuthResults(h []MailHeader, provider string) (authResult, bool) {
	for i, x := range h {
		if !strings.EqualFold(x.Name, "Authentication-Results") {
			continue
		}
		ar := parseAuthResults(x.Value)
		if provider == "google" && ar.ServID != "mx.google.com" {
			continue
		}
		if slices.ContainsFunc(h[i+1:], func(b MailHeader) bool { return strings.EqualFold(b.Name, "Received") }) {
			return ar, true
		}
		return authResult{}, false
	}
	return authResult{}, false
}

var tlsRe = regexp.MustCompile(`(?i)\b(e?smtpsa?|utf8smtpsa?|lmtps)\b|version=tls|\(using tls|\btls1_[0-3]\b`)

// encryption reads the first Received line with a sending host: the hop into the
// receiving server. Internal hops without one ("by ... with SMTP id") are skipped.
func encryption(h []MailHeader) string {
	for _, x := range h {
		if !strings.EqualFold(x.Name, "Received") || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(x.Value)), "from ") {
			continue
		}
		if tlsRe.MatchString(x.Value) {
			return "tls"
		}
		return "none"
	}
	return ""
}

func header(h []MailHeader, name string) string {
	for _, x := range h {
		if strings.EqualFold(x.Name, name) {
			return x.Value
		}
	}
	return ""
}

// orgDomain is a domain's registrable part (mail.ramp.com → ramp.com).
func orgDomain(d string) string {
	d = strings.ToLower(strings.Trim(strings.TrimSpace(d), "."))
	if o, err := publicsuffix.EffectiveTLDPlusOne(d); err == nil {
		return o
	}
	return d
}

func addrDomain(addr string) string {
	if a, err := mail.ParseAddress(addr); err == nil {
		addr = a.Address
	}
	addr = strings.Trim(strings.TrimSpace(addr), "<>")
	if _, d, ok := strings.Cut(addr, "@"); ok {
		return strings.ToLower(d)
	}
	return ""
}

// messageDetails reads a message's source. fromAccount is mail the account sent: its
// checks belong to the receiver, so none show, and only it keeps its Bcc header.
func messageDetails(raw []byte, provider, fromEmail string, fromAccount bool) MessageDetails {
	all := sourceHeaders(raw)
	out := MessageDetails{SignedBy: []string{}, Headers: make([]MailHeader, 0, len(all))}
	for _, x := range all {
		if !fromAccount && strings.EqualFold(x.Name, "Bcc") {
			continue
		}
		out.Headers = append(out.Headers, x)
	}
	if fromAccount {
		return out
	}
	out.Encryption = encryption(all)
	ar, ok := trustedAuthResults(all, provider)
	if !ok {
		return out
	}
	spf, dkim, dmarc := ar.Methods["spf"], ar.Methods["dkim"], ar.Methods["dmarc"]
	out.SPF, out.DKIM, out.DMARC = spf.Result, dkim.Result, dmarc.Result
	out.SignedBy = append(out.SignedBy, ar.Signers...)
	mailedBy := spf.Props["smtp.mailfrom"]
	if strings.Contains(mailedBy, "@") {
		mailedBy = addrDomain(mailedBy)
	}
	if mailedBy == "" {
		mailedBy = addrDomain(header(all, "Return-Path"))
	}
	out.MailedBy = strings.ToLower(mailedBy)

	from := orgDomain(addrDomain(fromEmail))
	switch {
	case out.DMARC == "fail":
		out.Warning = "spoofed"
	case spf.Result != "pass" && len(ar.Signers) == 0 && (spf.Result != "" || dkim.Result != ""):
		out.Warning = "unverified"
	}
	// "via": signed and sent by a domain that is not the sender's own.
	if from != "" {
		own := out.MailedBy != "" && orgDomain(out.MailedBy) == from
		for _, s := range ar.Signers {
			own = own || orgDomain(s) == from
		}
		if !own {
			if len(ar.Signers) > 0 {
				out.Via = orgDomain(ar.Signers[0])
			} else if out.MailedBy != "" {
				out.Via = orgDomain(out.MailedBy)
			}
		}
	}
	return out
}
