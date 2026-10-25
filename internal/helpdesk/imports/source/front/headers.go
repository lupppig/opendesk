package front

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/textproto"
	"regexp"
	"strings"
	"time"

	"github.com/useopendesk/opendesk/internal/helpdesk/imports/source"
)

// headerWindow is how recent a conversation's last activity must be for its emails'
// threading headers to be read. Each email costs one more request, and a live reply
// almost always answers a recent conversation.
var headerWindow = 180 * 24 * time.Hour

// headerClock is the clock headerWindow is measured from. Tests set it.
var headerClock = time.Now

// maxHeaderBytes caps how much of a raw email is read to find its headers.
const maxHeaderBytes = 256 << 10

var msgIDToken = regexp.MustCompile(`<[^<>\s]+>`)

// Threading is an email's Message-ID, In-Reply-To and References, each as "<id>".
type Threading struct {
	MessageID  string
	InReplyTo  string
	References []string
}

// wantHeaders says the emails of a conversation last active at updated get their
// threading headers read.
func (s *front) wantHeaders(updated time.Time) bool {
	if headerWindow <= 0 || updated.IsZero() {
		return false
	}
	return headerClock().Sub(updated) <= headerWindow
}

// headers reads the threading headers of one email from Front's raw copy of it
// (GET /messages/{id} as message/rfc822). Only the header block is read.
func (s *front) headers(ctx context.Context, id string) (Threading, error) {
	if !strings.HasPrefix(id, "msg_") || strings.ContainsAny(id, "/?#") {
		return Threading{}, errors.New("front: malformed message id")
	}
	u, err := s.api.URL("/messages/"+id, nil)
	if err != nil {
		return Threading{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Threading{}, err
	}
	req.Header.Set("Accept", "message/rfc822")
	req.Header.Set("User-Agent", "opendesk-import/1 (+https://opendesk.sh)")
	s.auth(req)
	resp, err := s.api.HTTP.Do(req)
	if err != nil {
		return Threading{}, &source.Unreachable{Err: err}
	}
	defer resp.Body.Close()
	if err := source.Status(resp, nil); err != nil {
		return Threading{}, err
	}
	return parseThreading(io.LimitReader(resp.Body, maxHeaderBytes))
}

// parseThreading reads an email's header block and picks out its threading headers.
func parseThreading(r io.Reader) (Threading, error) {
	h, err := textproto.NewReader(bufio.NewReader(r)).ReadMIMEHeader()
	if err != nil && len(h) == 0 {
		return Threading{}, err
	}
	var t Threading
	if ids := msgIDToken.FindAllString(h.Get("Message-Id"), 1); len(ids) > 0 {
		t.MessageID = ids[0]
	}
	if ids := msgIDToken.FindAllString(h.Get("In-Reply-To"), 1); len(ids) > 0 {
		t.InReplyTo = ids[0]
	}
	for _, v := range h.Values("References") {
		t.References = append(t.References, msgIDToken.FindAllString(v, -1)...)
	}
	return t, nil
}

// thread fills m's threading headers from Front. A message Front can't give them for
// keeps none: threading is a convenience, so only a rate limit or a cancelled import
// stops the load.
func (s *front) thread(ctx context.Context, m *source.Message) error {
	t, err := s.headers(ctx, m.ID)
	if err != nil {
		var rl *source.RateLimited
		if errors.As(err, &rl) || ctx.Err() != nil {
			return err
		}
		return nil
	}
	m.MessageID, m.InReplyTo, m.References = t.MessageID, t.InReplyTo, t.References
	return nil
}
