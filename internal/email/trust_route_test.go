package email

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/useopendesk/opendesk/internal/config"
	"github.com/useopendesk/opendesk/internal/deps"
)

// TestAuthservFollowsRoute: only servers a message passed through are believed. Mail
// straight from a sender's server believes nothing, and forwarded mail never believes the
// mailbox provider, which did not receive it.
func TestAuthservFollowsRoute(t *testing.T) {
	s := &service{d: &deps.Deps{Cfg: &config.Config{InboundAuthservIDs: []string{"mx.in.test"}}}}
	google := &mailboxRow{InboundKind: KindGoogle}
	cases := []struct {
		via  string
		box  *mailboxRow
		want []string
	}{
		{viaMailbox, google, []string{"mx.in.test", "mx.google.com"}},
		{viaForward, google, []string{"mx.in.test"}},
		{viaDirect, google, nil},
		{viaDirect, &mailboxRow{InboundKind: InboundForwarding}, nil},
		{viaMember, google, nil},
	}
	for _, c := range cases {
		if got := s.trustedAuthserv(c.box, c.via); !slices.Equal(got, c.want) {
			t.Errorf("via %q: %v, want %v", c.via, got, c.want)
		}
	}
	forged := []byte("Authentication-Results: mx.google.com; dmarc=pass header.from=bank.test\r\nFrom: a@bank.test\r\n\r\nbody\r\n")
	if dmarcVerdict(forged, s.trustedAuthserv(google, viaDirect)) != authUnknown {
		t.Fatal("a sender's own results were believed for mail straight from its server")
	}
}

func TestRelayedAddresses(t *testing.T) {
	s := &service{d: &deps.Deps{Cfg: &config.Config{InboundSMTPRelays: []netip.Prefix{
		netip.MustParsePrefix("10.1.0.0/16"), netip.MustParsePrefix("2001:db8::7/128")}}}}
	cases := map[string]bool{"10.1.2.3": true, "10.2.0.1": false, "::ffff:10.1.0.9": true, "2001:db8::7": true, "2001:db8::8": false}
	for ip, want := range cases {
		if got := s.relayed(&net.TCPAddr{IP: net.ParseIP(ip), Port: 25}); got != want {
			t.Errorf("%s: %v, want %v", ip, got, want)
		}
	}
	if (&service{d: &deps.Deps{Cfg: &config.Config{}}}).relayed(&net.TCPAddr{IP: net.ParseIP("10.1.2.3")}) {
		t.Fatal("no relays listed, yet one matched")
	}
}

// TestDirectMailBelievesNoResults: through the built-in receiver from an unlisted address,
// a message's own results neither pass a contact form nor fail it.
func TestDirectMailBelievesNoResults(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox(InboundForwarding, OutboundSystem, nil)
	raw := strings.ReplaceAll(strings.ReplaceAll("Authentication-Results: mx.in.test; dmarc=fail header.from=customer.test\n"+
		rawMail(customer(), m.Email, "Hello", ""), "\r\n", "\n"), "\n", "\r\n")
	id, _, err := w.s.ingest(w.ctx, m, []byte(raw), w.forwardAddr(m, ""), nil, viaDirect)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.s.processInbound(w.ctx, w.wid, id); err != nil {
		t.Fatal(err)
	}
	if w.ticket(w.messageFor(id).TicketID).Spam {
		t.Fatal("results a sender wrote decided the verdict")
	}
}

// TestSMTPCertificateReload: the receiver's certificate is read again when its files
// change, and the last good one stays while new files do not load.
func TestSMTPCertificateReload(t *testing.T) {
	dir := t.TempDir()
	cert, key := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	write := func(at time.Time) []byte {
		cfg, _ := selfSigned(t)
		c := cfg.Certificates[0]
		k, err := x509.MarshalECPrivateKey(c.PrivateKey.(*ecdsa.PrivateKey))
		if err != nil {
			t.Fatal(err)
		}
		for p, b := range map[string][]byte{cert: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Certificate[0]}),
			key: pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: k})} {
			if err := os.WriteFile(p, b, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(p, at, at); err != nil {
				t.Fatal(err)
			}
		}
		return c.Certificate[0]
	}
	f := &certFiles{cert: cert, key: key}
	now := time.Now()
	first := write(now.Add(-time.Hour))
	if c, err := f.get(nil); err != nil || !bytes.Equal(c.Certificate[0], first) {
		t.Fatalf("first load: %v", err)
	}
	second := write(now)
	if c, err := f.get(nil); err != nil || !bytes.Equal(c.Certificate[0], second) {
		t.Fatalf("renewed certificate not read: %v", err)
	}
	if err := os.WriteFile(cert, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := f.get(nil); err != nil || !bytes.Equal(c.Certificate[0], second) {
		t.Fatalf("a broken renewal replaced the last good certificate: %v", err)
	}

	s := &service{d: &deps.Deps{Cfg: &config.Config{InboundDomain: "in.test", InboundSMTPTLSCert: cert, InboundSMTPTLSKey: key}}}
	if srv := s.newSMTPServer("127.0.0.1:0"); srv.TLSConfig == nil || srv.TLSConfig.MinVersion != tls.VersionTLS12 {
		t.Fatal("the receiver offers no STARTTLS with a certificate set")
	}
}
