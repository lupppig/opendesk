package email

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/domain"
)

func TestMailboxHandlersShareImportLockOrder(t *testing.T) {
	for _, method := range []string{http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			w := newWorld(t)
			m := w.mailbox(InboundForwarding, OutboundSystem, nil)
			srv := w.api(w.admin)
			ctx, cancel := context.WithTimeout(w.ctx, 5*time.Second)
			defer cancel()
			importTx, err := w.d.DB.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer importTx.Rollback(context.Background())
			var pid int
			if err := importTx.QueryRow(ctx, `UPDATE workspaces SET ticket_seq=ticket_seq+1 WHERE id=$1 RETURNING pg_backend_pid()`, w.wid).Scan(&pid); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				req, err := http.NewRequestWithContext(ctx, method, srv.URL+"/v1/mailboxes/"+m.ID.String(), bytes.NewBufferString(`{"name":"Updated"}`))
				if err != nil {
					done <- err
					return
				}
				req.Header.Set("Content-Type", "application/json")
				resp, err := srv.Client().Do(req)
				if err == nil {
					defer resp.Body.Close()
					io.Copy(io.Discard, resp.Body)
					if resp.StatusCode >= 300 {
						err = fmt.Errorf("%s returned %d", method, resp.StatusCode)
					}
				}
				done <- err
			}()
			for {
				var blocked bool
				if err := w.d.DB.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, pid).Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("handler finished before import: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(time.Millisecond):
				}
			}
			if _, err := domain.CreateTicket(ctx, importTx, domain.NewTicket{WorkspaceID: w.wid, Subject: "Imported", Channel: domain.ChannelEmail, MailboxID: &m.ID, Imported: true, CreatedAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
			if err := importTx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMailboxWritesShareImportLockOrder(t *testing.T) {
	w := newWorld(t)
	m := w.mailbox(InboundForwarding, OutboundSystem, nil)
	ctx, cancel := context.WithTimeout(w.ctx, 5*time.Second)
	defer cancel()

	importTx, err := w.d.DB.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer importTx.Rollback(ctx)
	if _, err := importTx.Exec(ctx, `UPDATE workspaces SET ticket_seq = ticket_seq + 1 WHERE id = $1`, w.wid); err != nil {
		t.Fatal(err)
	}

	mailboxLocked := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- w.d.DB.Tx(ctx, func(tx pgx.Tx) error {
			if err := lockWorkspaceForMailboxWrite(ctx, tx, w.wid); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `SELECT id FROM mailboxes WHERE workspace_id = $1 AND id = $2 FOR UPDATE`, w.wid, m.ID); err != nil {
				return err
			}
			close(mailboxLocked)
			return nil
		})
	}()

	select {
	case <-mailboxLocked:
		t.Fatal("mailbox write locked the mailbox before the workspace")
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := domain.CreateTicket(ctx, importTx, domain.NewTicket{WorkspaceID: w.wid, Subject: "Imported", Channel: domain.ChannelEmail,
		MailboxID: &m.ID, Imported: true, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := importTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
