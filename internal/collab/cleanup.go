package collab

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/storage"
)

// Cleanup (contracts/collab.md "Who sees what", "Data"): a member who left, or a deleted
// user, loses their accounts: private ones are deleted with their mail, shared ones are
// kept without an owner for admins to manage. Stored raw messages of deleted mail are
// removed from storage.

const trashBatch = 500

// deleteAccount removes an account and its mail from opendesk, in tx: its stored raw
// messages go to the storage trash (removed by collab.cleanup) and the row is deleted
// with everything that cascades from it. A Google grant is revoked first, best effort and
// briefly. Nothing is deleted at the provider. The caller audits and publishes.
func (s *service) deleteAccount(ctx context.Context, tx pgx.Tx, a *accountRow) error {
	s.revokeGoogle(ctx, a)
	if _, err := tx.Exec(ctx, `INSERT INTO collab_storage_trash (key)
		SELECT raw_key FROM collab_messages WHERE account_id = $1 AND workspace_id = $2 AND raw_key <> ''
		ON CONFLICT DO NOTHING`, a.ID, a.WorkspaceID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM collab_accounts WHERE id = $1 AND workspace_id = $2`, a.ID, a.WorkspaceID)
	return err
}

// cleanup handles accounts whose owner left, then empties the storage trash.
func (s *service) cleanup(ctx context.Context) error {
	rows, err := s.d.DB.Pool.Query(ctx, `SELECT `+accountColumns+` FROM collab_accounts a
		WHERE a.owner_id IS NULL OR NOT EXISTS (SELECT 1 FROM members m WHERE m.workspace_id = a.workspace_id AND m.user_id = a.owner_id)`)
	if err != nil {
		return err
	}
	orphans, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (*accountRow, error) { return scanAccount(r) })
	if err != nil {
		return err
	}
	for _, a := range orphans {
		err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
			// Who saw it before, so they learn it changed.
			before, err := accountAudience(ctx, tx, a.WorkspaceID, a.ID)
			if err != nil {
				return err
			}
			if a.Visibility == VisibilityShared {
				if a.OwnerID == nil {
					return nil // already ownerless
				}
				if _, err := tx.Exec(ctx, `UPDATE collab_accounts SET owner_id = NULL, updated_at = now() WHERE id = $1`, a.ID); err != nil {
					return err
				}
				return s.publishAccounts(ctx, tx, a.WorkspaceID, a.ID, before)
			}
			if err := s.deleteAccount(ctx, tx, a); err != nil {
				return err
			}
			return s.publishAccountGone(ctx, tx, a.WorkspaceID, a.ID, before)
		})
		if err != nil {
			return err
		}
	}
	return s.emptyStorageTrash(ctx)
}

// publishAccountGone tells who saw a deleted account that it is gone.
func (s *service) publishAccountGone(ctx context.Context, tx pgx.Tx, wid, accountID uuid.UUID, users []uuid.UUID) error {
	if len(users) == 0 {
		return nil
	}
	return s.publishAccounts(ctx, tx, wid, accountID, users)
}

// emptyStorageTrash deletes the storage objects listed in collab_storage_trash, in
// batches; a missing object counts as deleted.
func (s *service) emptyStorageTrash(ctx context.Context) error {
	for {
		rows, err := s.d.DB.Pool.Query(ctx, `SELECT key FROM collab_storage_trash ORDER BY created_at LIMIT $1`, trashBatch)
		if err != nil {
			return err
		}
		keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		done := []string{}
		for _, key := range keys {
			if err := s.d.Storage.Delete(ctx, key); err != nil && !errors.Is(err, storage.ErrNotFound) {
				s.log.Warn("collab storage delete failed", "err", err)
				continue
			}
			done = append(done, key)
		}
		if len(done) > 0 {
			if _, err := s.d.DB.Pool.Exec(ctx, `DELETE FROM collab_storage_trash WHERE key = ANY($1)`, done); err != nil {
				return err
			}
		}
		if len(keys) < trashBatch || len(done) == 0 {
			return nil
		}
	}
}
