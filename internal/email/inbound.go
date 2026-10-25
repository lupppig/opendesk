package email

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-smtp"

	"github.com/useopendesk/opendesk/internal/httpx"
)

// InboundSignatureHeader authenticates a message posted by the Cloudflare Email Worker
// (or another relay): "t=<unix seconds>,v1=<hex HMAC-SHA256>" over
// "<t>.<to>.<envelope from>." followed by the raw message, keyed with INBOUND_SECRET.
// A signature older than inboundSignatureTolerance is refused (CASA 7.2).
const InboundSignatureHeader = "OD-Inbound-Signature"

// inboundSignatureTolerance bounds how old (or how far ahead) a signed request may be.
const inboundSignatureTolerance = 5 * time.Minute

// EnvelopeFromHeader carries the SMTP envelope sender from the relay; empty is the null
// sender of a delivery report.
const EnvelopeFromHeader = "OD-Envelope-From"

func writeProblem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(httpx.E(status, code, detail))
}

// SignInbound is the OD-Inbound-Signature value for a message (the relay's side).
func SignInbound(secret string, t int64, to, envelopeFrom string, body []byte) string {
	ts := strconv.FormatInt(t, 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "." + to + "." + envelopeFrom + "."))
	mac.Write(body)
	return "t=" + ts + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

// inboundSignatureOK checks an OD-Inbound-Signature: the HMAC matches (compared in
// constant time) and its timestamp is recent.
func inboundSignatureOK(header, secret, to, envelopeFrom string, body []byte, now time.Time) bool {
	if secret == "" {
		return false
	}
	var ts, sig string
	for _, part := range strings.Split(header, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
		switch k {
		case "t":
			ts = v
		case "v1":
			sig = v
		}
	}
	t, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || sig == "" {
		return false
	}
	if d := now.Sub(time.Unix(t, 0)); d > inboundSignatureTolerance || d < -inboundSignatureTolerance {
		return false
	}
	want := SignInbound(secret, t, to, envelopeFrom, body)
	return subtle.ConstantTimeCompare([]byte(want), []byte("t="+ts+",v1="+strings.ToLower(sig))) == 1
}

// inboundHTTP accepts one raw RFC 822 message for a forwarding address.
func (s *service) inboundHTTP(w http.ResponseWriter, r *http.Request) {
	if s.d.Cfg.InboundSecret == "" {
		writeProblem(w, http.StatusNotFound, "not_found", "Inbound email is not enabled on this instance.")
		return
	}
	signature := r.Header.Get(InboundSignatureHeader)
	if signature == "" {
		writeProblem(w, http.StatusUnauthorized, "unauthenticated", "Missing or wrong inbound signature.")
		return
	}
	rcpt := r.URL.Query().Get("to")
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRawBytes+1))
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig) || len(body) > maxRawBytes:
		writeProblem(w, http.StatusRequestEntityTooLarge, "too_large", "Messages are limited to 30 MB.")
		return
	case err != nil:
		writeProblem(w, http.StatusBadRequest, "bad_request", "Could not read the message.")
		return
	case len(bytes.TrimSpace(body)) == 0:
		writeProblem(w, http.StatusBadRequest, "bad_request", "The message is empty.")
		return
	}
	if !inboundSignatureOK(signature, s.d.Cfg.InboundSecret, rcpt, r.Header.Get(EnvelopeFromHeader), body, time.Now()) {
		writeProblem(w, http.StatusUnauthorized, "unauthenticated", "Missing or wrong inbound signature.")
		return
	}
	m, err := s.mailboxForRecipient(r.Context(), rcpt)
	if errors.Is(err, errUnknownRecipient) {
		writeProblem(w, http.StatusNotFound, "unknown_recipient", "No mailbox uses that forwarding address.")
		return
	}
	if err != nil {
		s.log.Error("inbound lookup", "err", err)
		writeProblem(w, http.StatusServiceUnavailable, "unavailable", "Try again later.")
		return
	}
	var envFrom *string
	if vals, ok := r.Header[http.CanonicalHeaderKey(EnvelopeFromHeader)]; ok && len(vals) > 0 {
		envFrom = &vals[0]
	}
	id, dup, err := s.ingest(r.Context(), m, body, rcpt, envFrom, viaForward)
	if err != nil {
		s.log.Error("inbound ingest", "mailbox_id", m.ID, "err", err)
		writeProblem(w, http.StatusServiceUnavailable, "unavailable", "Try again later.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	out := map[string]any{"duplicate": dup}
	if !dup {
		out["id"] = id
	}
	_ = json.NewEncoder(w).Encode(out)
}

// smtpBackend is the built-in receiver for self-hosters (INBOUND_SMTP_ADDR). It takes
// mail only for existing forwarding addresses at INBOUND_DOMAIN.
type smtpBackend struct{ s *service }

func (b *smtpBackend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	ip := ""
	if addr, ok := c.Conn().RemoteAddr().(*net.TCPAddr); ok {
		ip = addr.IP.String()
	}
	if b.s.d.Limiter != nil && !b.s.d.Limiter.Allow("smtp-in:"+ip, 300, time.Minute) {
		return nil, &smtp.SMTPError{Code: 421, EnhancedCode: smtp.EnhancedCode{4, 7, 0}, Message: "Too many connections, try later"}
	}
	via := viaDirect
	if b.s.relayed(c.Conn().RemoteAddr()) {
		via = viaForward
	}
	return &smtpSession{s: b.s, via: via}, nil
}

type smtpSession struct {
	s       *service
	via     string // viaForward from a listed relay (INBOUND_SMTP_RELAYS), else viaDirect
	envFrom string
	rcpts   []string
	boxes   []*mailboxRow
}

func (ss *smtpSession) Reset()        { ss.envFrom, ss.rcpts, ss.boxes = "", nil, nil }
func (ss *smtpSession) Logout() error { return nil }

func (ss *smtpSession) Mail(from string, _ *smtp.MailOptions) error {
	ss.envFrom = from
	return nil
}

func (ss *smtpSession) Rcpt(to string, _ *smtp.RcptOptions) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m, err := ss.s.mailboxForRecipient(ctx, to)
	if errors.Is(err, errUnknownRecipient) {
		return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 1, 1}, Message: "No such mailbox"}
	}
	if err != nil {
		return &smtp.SMTPError{Code: 451, EnhancedCode: smtp.EnhancedCode{4, 3, 0}, Message: "Temporary failure, try later"}
	}
	for _, b := range ss.boxes {
		if b.ID == m.ID {
			return nil // two plus addresses of one mailbox: deliver once
		}
	}
	ss.rcpts = append(ss.rcpts, strings.ToLower(to))
	ss.boxes = append(ss.boxes, m)
	return nil
}

func (ss *smtpSession) Data(r io.Reader) error {
	raw, err := io.ReadAll(io.LimitReader(r, maxRawBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > maxRawBytes {
		return &smtp.SMTPError{Code: 552, EnhancedCode: smtp.EnhancedCode{5, 3, 4}, Message: "Message too big"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for i, m := range ss.boxes {
		envFrom := ss.envFrom
		if _, _, err := ss.s.ingest(ctx, m, raw, ss.rcpts[i], &envFrom, ss.via); err != nil {
			ss.s.log.Error("smtp ingest", "mailbox_id", m.ID, "err", err)
			return &smtp.SMTPError{Code: 451, EnhancedCode: smtp.EnhancedCode{4, 3, 0}, Message: "Temporary failure, try later"}
		}
	}
	return nil
}

// newSMTPServer builds the receiver. It advertises no AUTH: it only accepts mail
// addressed to forwarding tokens. With INBOUND_SMTP_TLS_CERT and INBOUND_SMTP_TLS_KEY it
// offers STARTTLS (TLS 1.2 or newer), so sending servers can encrypt mail on its way here.
func (s *service) newSMTPServer(addr string) *smtp.Server {
	srv := smtp.NewServer(&smtpBackend{s: s})
	srv.Addr = addr
	srv.Domain = s.inboundDomain()
	srv.MaxMessageBytes = maxRawBytes
	srv.MaxRecipients = 20
	srv.ReadTimeout = 60 * time.Second
	srv.WriteTimeout = 60 * time.Second
	srv.ErrorLog = smtpLogger{s}
	if s.d.Cfg.InboundSMTPTLSCert != "" {
		f := &certFiles{cert: s.d.Cfg.InboundSMTPTLSCert, key: s.d.Cfg.InboundSMTPTLSKey}
		srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: f.get}
	}
	return srv
}

// certFiles is a certificate read from PEM files, read again when either changes, so a
// renewed certificate needs no restart. Until the new files load, the last good one stays.
type certFiles struct {
	cert, key string
	mu        sync.Mutex
	mod       time.Time
	c         *tls.Certificate
}

func (f *certFiles) get(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	var mod time.Time
	for _, p := range []string{f.cert, f.key} {
		st, err := os.Stat(p)
		if err != nil {
			return f.last(err)
		}
		if st.ModTime().After(mod) {
			mod = st.ModTime()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.c != nil && !mod.After(f.mod) {
		return f.c, nil
	}
	c, err := tls.LoadX509KeyPair(f.cert, f.key)
	if err != nil {
		if f.c != nil {
			return f.c, nil
		}
		return nil, err
	}
	f.c, f.mod = &c, mod
	return f.c, nil
}

func (f *certFiles) last(err error) (*tls.Certificate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.c != nil {
		return f.c, nil
	}
	return nil, err
}

type smtpLogger struct{ s *service }

func (l smtpLogger) Printf(format string, v ...any) { l.s.log.Debug("smtp receiver", "msg", format) }
func (l smtpLogger) Println(v ...any)               { l.s.log.Debug("smtp receiver") }

// runSMTP serves until ctx ends.
func (s *service) runSMTP(ctx context.Context, addr string) {
	srv := s.newSMTPServer(addr)
	if srv.TLSConfig != nil {
		if _, err := srv.TLSConfig.GetCertificate(nil); err != nil {
			s.log.Error("smtp receiver certificate", "err", err)
		}
	}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	s.log.Info("smtp receiver listening", "addr", addr, "domain", srv.Domain, "starttls", srv.TLSConfig != nil)
	if err := srv.ListenAndServe(); err != nil && ctx.Err() == nil && !errors.Is(err, smtp.ErrServerClosed) {
		s.log.Error("smtp receiver stopped", "err", err)
	}
}
