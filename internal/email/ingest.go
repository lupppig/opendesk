package email

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/textproto"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/jobs"
)

// headerMessageID reads the Message-ID of a raw message. When it is missing or
// unusable, a stable one is synthesized from the content so a redelivery (or an IMAP
// refetch) of the same bytes dedupes.
func headerMessageID(raw []byte) string {
	h := readHeader(raw)
	if id := normalizeMsgID(h.Get("Message-Id")); id != "" {
		return id
	}
	sum := sha256.Sum256(raw)
	return "<" + hex.EncodeToString(sum[:16]) + "@synthesized.opendesk>"
}

// readHeader parses just the header block. It tolerates bare LF line endings.
func readHeader(raw []byte) textproto.MIMEHeader {
	end := bytes.Index(raw, []byte("\r\n\r\n"))
	if alt := bytes.Index(raw, []byte("\n\n")); end < 0 || (alt >= 0 && alt < end) {
		end = alt
	}
	if end < 0 {
		end = len(raw)
	}
	r := textproto.NewReader(bufio.NewReader(bytes.NewReader(append(append([]byte{}, raw[:end]...), "\r\n\r\n"...))))
	h, _ := r.ReadMIMEHeader()
	if h == nil {
		h = textproto.MIMEHeader{}
	}
	return h
}

var msgIDRe = regexp.MustCompile(`<([^<>\s]{1,900})>`)

// normalizeMsgID returns "<id>" from a header value, or "" when there is none.
func normalizeMsgID(v string) string {
	v = strings.TrimSpace(dropNUL(v))
	if m := msgIDRe.FindStringSubmatch(v); m != nil {
		return "<" + m[1] + ">"
	}
	if v != "" && !strings.ContainsAny(v, " \t<>") && len(v) <= 900 {
		return "<" + v + ">"
	}
	return ""
}

// msgIDList parses In-Reply-To / References into "<id>" values, oldest first.
func msgIDList(vals ...string) []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range vals {
		for _, m := range msgIDRe.FindAllStringSubmatch(dropNUL(v), -1) {
			id := "<" + m[1] + ">"
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

// ingest stores a raw message, records it in inbound_emails and queues processing. A
// message is a duplicate only when its Message-ID and its content both match one seen
// before at this mailbox, so a redelivery dedupes and a different message reusing the id
// does not. envFrom is the SMTP envelope sender when the transport reports it, else nil.
// via is the route it arrived by (viaMailbox, viaForward, viaDirect). It returns dup=true
// for a message seen before.
func (s *service) ingest(ctx context.Context, m *mailboxRow, raw []byte, rcpt string, envFrom *string, via string) (uuid.UUID, bool, error) {
	return s.ingestRef(ctx, m, raw, rcpt, envFrom, nil, via)
}

// ingestRef is ingest of a message read through the provider's API, recording where it
// lives there (ref; nil for mail from anywhere else).
func (s *service) ingestRef(ctx context.Context, m *mailboxRow, raw []byte, rcpt string, envFrom *string, ref *providerRef, via string) (uuid.UUID, bool, error) {
	msgID := headerMessageID(raw)
	hash := contentHash(raw)
	var exists bool
	if err := s.d.DB.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM inbound_emails WHERE mailbox_id = $1 AND message_id_hdr = $2 AND content_hash = $3)`,
		m.ID, msgID, hash).Scan(&exists); err != nil {
		return uuid.Nil, false, err
	}
	if exists {
		return uuid.Nil, true, nil
	}
	if envFrom != nil {
		v := truncate(strings.ToLower(strings.Trim(strings.TrimSpace(*envFrom), "<>")), 320)
		envFrom = &v
	}
	id := ids.New()
	key := "workspaces/" + m.WorkspaceID.String() + "/inbound/" + id.String() + ".eml"
	if err := s.d.Storage.Put(ctx, key, bytes.NewReader(raw), int64(len(raw)), "message/rfc822"); err != nil {
		return uuid.Nil, false, err
	}
	inserted := false
	err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO inbound_emails (id, workspace_id, mailbox_id, message_id_hdr, raw_storage_key, rcpt_to, content_hash, envelope_from, provider_ref, via)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) ON CONFLICT (mailbox_id, message_id_hdr, content_hash) DO NOTHING`,
			id, m.WorkspaceID, m.ID, msgID, key, truncate(strings.ToLower(rcpt), 320), hash, envFrom, ref, via)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		inserted = true
		if _, err := tx.Exec(ctx, `UPDATE mailboxes SET last_received_at = now() WHERE id = $1`, m.ID); err != nil {
			return err
		}
		_, err = s.d.Jobs.InsertTx(ctx, tx, jobs.ProcessInboundArgs{WorkspaceID: m.WorkspaceID, InboundEmailID: id},
			&river.InsertOpts{Queue: jobs.QueueEmail, MaxAttempts: 5})
		return err
	})
	if err != nil || !inserted {
		_ = s.d.Storage.Delete(context.WithoutCancel(ctx), key)
		if err != nil {
			return uuid.Nil, false, err
		}
		return uuid.Nil, true, nil
	}
	return id, false, nil
}

// splitRecipient parses "<token>[+t<n>]@<domain>" into its token and plus tag.
func splitRecipient(addr string) (token, tag, domain string, ok bool) {
	addr = strings.ToLower(strings.Trim(strings.TrimSpace(addr), "<>"))
	at := strings.LastIndexByte(addr, '@')
	if at <= 0 || at == len(addr)-1 {
		return "", "", "", false
	}
	local, domain := addr[:at], addr[at+1:]
	token, tag, _ = strings.Cut(local, "+")
	if token == "" || len(token) > 64 {
		return "", "", "", false
	}
	return token, tag, domain, true
}

// mailboxForRecipient finds the mailbox a forwarding address belongs to.
func (s *service) mailboxForRecipient(ctx context.Context, rcpt string) (*mailboxRow, error) {
	token, _, domain, ok := splitRecipient(rcpt)
	if !ok || domain != s.inboundDomain() {
		return nil, errUnknownRecipient
	}
	m, err := loadMailboxByToken(ctx, s.d.DB.Pool, token)
	if db.IsNotFound(err) {
		return nil, errUnknownRecipient
	}
	if err != nil {
		return nil, err
	}
	if m.Status == "disabled" {
		return nil, errUnknownRecipient
	}
	return m, nil
}

type recipientError struct{}

func (recipientError) Error() string { return "unknown recipient" }

var errUnknownRecipient error = recipientError{}
