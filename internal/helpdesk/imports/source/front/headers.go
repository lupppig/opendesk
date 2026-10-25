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

	"github.com/useopendesk/opendesk/internal/helpdesk/imports/source"
)

// maxHeaderBytes caps how much of a raw email is read to find its headers.
const maxHeaderBytes = 256 << 10

var msgIDToken = regexp.MustCompile(`<[^<>\s]+>`)

// Threading reads the threading headers of one email from Front's raw copy of it
// (GET /messages/{id} as message/rfc822), for the import's threading phase
// (source.Threader). Front's message listings leave them out. Only the header block is read.
func (s *front) Threading(ctx context.Context, id string) (source.Threading, error) {
	if !strings.HasPrefix(id, "msg_") || strings.ContainsAny(id, "/?#") {
		return source.Threading{}, errors.New("front: malformed message id")
	}
	u, err := s.api.URL("/messages/"+id, nil)
	if err != nil {
		return source.Threading{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return source.Threading{}, err
	}
	req.Header.Set("Accept", "message/rfc822")
	req.Header.Set("User-Agent", "opendesk-import/1 (+https://opendesk.sh)")
	s.auth(req)
	resp, err := s.api.HTTP.Do(req)
	if err != nil {
		return source.Threading{}, &source.Unreachable{Err: err}
	}
	defer resp.Body.Close()
	if err := source.Status(resp, nil); err != nil {
		return source.Threading{}, err
	}
	return parseThreading(io.LimitReader(resp.Body, maxHeaderBytes))
}

// parseThreading reads an email's header block and picks out its threading headers.
func parseThreading(r io.Reader) (source.Threading, error) {
	h, err := textproto.NewReader(bufio.NewReader(r)).ReadMIMEHeader()
	if err != nil && len(h) == 0 {
		return source.Threading{}, err
	}
	var t source.Threading
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
