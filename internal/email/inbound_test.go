package email

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-smtp"
	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/crypto"
)

func postInbound(t *testing.T, base, secret, to, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/inbound/email?to="+url.QueryEscape(to), strings.NewReader(body))
	if secret != "" {
		req.Header.Set(InboundSignatureHeader, SignInbound(secret, time.Now().Unix(), to, "", []byte(body)))
	}
	req.Header.Set("Content-Type", "message/rfc822")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestInboundHTTP(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox(InboundForwarding, OutboundSystem, nil)
	srv := w.api(nil)
	raw := fixture(t, "new_plain.eml", "{{FROM}}", customer(), "{{TO}}", w.forwardAddr(m, ""), "{{ID}}", crypto.RandomString(8))

	if code, _ := postInbound(t, srv.URL, "", w.forwardAddr(m, ""), raw); code != http.StatusUnauthorized {
		t.Fatalf("no secret: %d", code)
	}
	if code, _ := postInbound(t, srv.URL, "wrong", w.forwardAddr(m, ""), raw); code != http.StatusUnauthorized {
		t.Fatalf("wrong secret: %d", code)
	}
	// A signature is good for five minutes, and the secret itself is never enough.
	stale, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/inbound/email?to="+url.QueryEscape(w.forwardAddr(m, "")), strings.NewReader(raw))
	stale.Header.Set(InboundSignatureHeader, SignInbound("test", time.Now().Add(-10*time.Minute).Unix(), w.forwardAddr(m, ""), "", []byte(raw)))
	if resp, err := http.DefaultClient.Do(stale); err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("stale signature: %v %v", resp, err)
	}
	bare, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/inbound/email?to="+url.QueryEscape(w.forwardAddr(m, "")), strings.NewReader(raw))
	bare.Header.Set("OD-Inbound-Secret", "test")
	if resp, err := http.DefaultClient.Do(bare); err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bare secret: %v %v", resp, err)
	}
	if code, _ := postInbound(t, srv.URL, "test", "nobody-x@in.test", raw); code != http.StatusNotFound {
		t.Fatalf("unknown rcpt: %d", code)
	}
	if code, _ := postInbound(t, srv.URL, "test", m.ForwardingToken+"@other.test", raw); code != http.StatusNotFound {
		t.Fatalf("wrong domain: %d", code)
	}
	code, out := postInbound(t, srv.URL, "test", strings.ToUpper(w.forwardAddr(m, "t7")), raw)
	if code != http.StatusAccepted || out["duplicate"] != false {
		t.Fatalf("accept: %d %v", code, out)
	}
	id, _ := uuid.Parse(out["id"].(string))
	var rcpt string
	if err := w.d.DB.Pool.QueryRow(w.ctx, `SELECT rcpt_to FROM inbound_emails WHERE id = $1`, id).Scan(&rcpt); err != nil || rcpt != strings.ToLower(w.forwardAddr(m, "t7")) {
		t.Fatalf("rcpt %q %v", rcpt, err)
	}
	if w.jobCount("email.process_inbound", "inbound_email_id", id.String()) != 1 {
		t.Fatal("processing not queued")
	}
	if code, out := postInbound(t, srv.URL, "test", w.forwardAddr(m, ""), raw); code != http.StatusAccepted || out["duplicate"] != true {
		t.Fatalf("dup: %d %v", code, out)
	}
	big := strings.Repeat("a", maxRawBytes+10)
	if code, _ := postInbound(t, srv.URL, "test", w.forwardAddr(m, ""), big); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("too large: %d", code)
	}
}

func TestSMTPReceiver(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox(InboundForwarding, OutboundSystem, nil)
	srv := w.s.newSMTPServer("127.0.0.1:0")
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })

	c, err := smtp.Dial(l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Mail("sender@customer.test", nil); err != nil {
		t.Fatal(err)
	}
	err = c.Rcpt("someone-else@in.test", nil)
	var se *smtp.SMTPError
	if !errors.As(err, &se) || se.Code != 550 {
		t.Fatalf("unknown rcpt should be 550, got %v", err)
	}
	if err := c.Rcpt("someone@other.test", nil); !errors.As(err, &se) || se.Code != 550 {
		t.Fatalf("foreign domain should be 550, got %v", err)
	}
	if err := c.Rcpt(w.forwardAddr(m, ""), nil); err != nil {
		t.Fatal(err)
	}
	wc, err := c.Data()
	if err != nil {
		t.Fatal(err)
	}
	from := customer()
	raw := fixture(t, "new_plain.eml", "{{FROM}}", from, "{{TO}}", w.forwardAddr(m, ""), "{{ID}}", crypto.RandomString(8))
	_, _ = wc.Write([]byte(strings.ReplaceAll(raw, "\n", "\r\n")))
	if err := wc.Close(); err != nil {
		t.Fatal(err)
	}
	_ = c.Quit()

	var id uuid.UUID
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := w.d.DB.Pool.QueryRow(w.ctx, `SELECT id FROM inbound_emails WHERE mailbox_id = $1`, m.ID).Scan(&id); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if id == uuid.Nil {
		t.Fatal("smtp message not ingested")
	}
	if err := w.s.processInbound(w.ctx, w.wid, id); err != nil {
		t.Fatal(err)
	}
	if msg := w.messageFor(id); msg.FromEmail != from || !bytes.Contains([]byte(msg.BodyText), []byte("printer")) {
		t.Fatalf("message %+v", msg)
	}
}

// TestSignInboundMatchesWorker: the API and the Cloudflare Email Worker sign alike (the
// worker's test pins the same value).
func TestSignInboundMatchesWorker(t *testing.T) {
	got := SignInbound("s3cret", 1700000000, "t0k3n@in.opendesk.sh", "sender@example.com", []byte("Subject: hi\r\n\r\nhello"))
	if got != "t=1700000000,v1=c030b4033566ca50f8d5b887d26b405893d76b89ebaac855375d1978999d9e47" {
		t.Fatalf("signature %s", got)
	}
	if !inboundSignatureOK(got, "s3cret", "t0k3n@in.opendesk.sh", "sender@example.com", []byte("Subject: hi\r\n\r\nhello"), time.Unix(1700000060, 0)) {
		t.Fatal("own signature refused")
	}
	if inboundSignatureOK(got, "s3cret", "other@in.opendesk.sh", "sender@example.com", []byte("Subject: hi\r\n\r\nhello"), time.Unix(1700000060, 0)) {
		t.Fatal("signature accepted for another recipient")
	}
}
