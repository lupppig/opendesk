package collab

import (
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/crypto"
)

// Attachment links (contracts/collab.md "MailAttachment"): signed, valid for an hour,
// usable in <img> and as a download link without a bearer token.

// attachmentKey signs attachment links; it is derived from AUTH_SECRET and differs from
// the key of ticket attachment links.
func (s *service) attachmentKey() []byte {
	return append([]byte("collab-attachment-url:"), s.d.Cfg.AuthSecret...)
}

func (s *service) attachmentSig(messageID uuid.UUID, index int, exp string) string {
	return crypto.HMACHex(s.attachmentKey(), messageID.String()+":"+strconv.Itoa(index)+":"+exp)
}

// attachmentPath is the API path of an attachment with a fresh signature. The expiry is
// rounded to the hour so a body renders with the same links for a while (browser cache),
// and is at least an hour away.
func (s *service) attachmentPath(messageID uuid.UUID, index int) string {
	exp := strconv.FormatInt(time.Now().Add(2*time.Hour).Truncate(time.Hour).Unix(), 10)
	return "/v1/collab/attachments/" + messageID.String() + "/" + strconv.Itoa(index) +
		"?exp=" + exp + "&sig=" + s.attachmentSig(messageID, index, exp)
}

// attachmentURL is the absolute signed URL of a message's attachment (its index in the
// message's attachments).
func (s *service) attachmentURL(messageID uuid.UUID, index int) string {
	return strings.TrimRight(s.d.Cfg.APIURL, "/") + s.attachmentPath(messageID, index)
}

// verifyAttachmentSig checks a link made by attachmentURL: the signature matches and it
// has not expired.
func (s *service) verifyAttachmentSig(messageID uuid.UUID, index int, exp, sig string) bool {
	n, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || time.Now().Unix() > n {
		return false
	}
	if _, err := hex.DecodeString(sig); err != nil || sig == "" {
		return false
	}
	return crypto.EqualHex(s.attachmentSig(messageID, index, exp), sig)
}

var collabAttachmentLink = regexp.MustCompile(`/v1/collab/attachments/([0-9a-f-]{36})/(\d{1,4})\?exp=\d+(&amp;|&)sig=[0-9a-f]+`)

// resignAttachmentLinks refreshes the signed attachment links inside a message's stored
// HTML (its inline images), so a body keeps rendering after the links it was stored with
// expire. Only links to the message's own files are signed again: a stored body is not a
// way to sign links to other messages.
func (s *service) resignAttachmentLinks(messageID uuid.UUID, html string) string {
	if !strings.Contains(html, "/v1/collab/attachments/") {
		return html
	}
	return collabAttachmentLink.ReplaceAllStringFunc(html, func(m string) string {
		sub := collabAttachmentLink.FindStringSubmatch(m)
		id, err := uuid.Parse(sub[1])
		if err != nil || id != messageID {
			return m
		}
		index, err := strconv.Atoi(sub[2])
		if err != nil {
			return m
		}
		fresh := s.attachmentPath(id, index)
		if sub[3] == "&amp;" {
			fresh = strings.Replace(fresh, "&", "&amp;", 1)
		}
		return fresh
	})
}
