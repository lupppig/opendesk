package collab

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
)

func TestDraftMissingOnSingleConnectionPool(t *testing.T) {
	w := newWorld(t)
	connection, err := url.Parse(w.d.DB.Pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	params := connection.Query()
	params.Set("pool_max_conns", "1")
	connection.RawQuery = params.Encode()
	database, err := db.Open(w.ctx, connection.String(), connection.String())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	d := *w.d
	d.DB = database
	s := *w.s
	s.d = &d
	ctx, cancel := context.WithTimeout(w.ctx, time.Second)
	defer cancel()
	_, _, _, unlock, err := s.lockDraft(ctx, w.agent, ids.New().String())
	if unlock != nil {
		unlock()
	}
	var problem *httpx.Error
	if !errors.As(err, &problem) || problem.Status != http.StatusNotFound {
		t.Fatalf("missing draft should return 404 with a one-connection request pool: %v", err)
	}
}
