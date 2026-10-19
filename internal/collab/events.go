package collab

import (
	"context"
	"errors"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// Event types (contracts/collab.md "Events"). They carry ids only and are scoped to the
// people who see what changed.
const (
	EventChanged  = "collab.changed"
	EventAccounts = "collab.accounts"
)

// publishChanged tells who sees the account and the conversations that they changed, in tx.
// accountID may be nil for conversations of several accounts (each thread's account is
// looked up); folders marks a change of folders or counts.
func (s *service) publishChanged(ctx context.Context, tx pgx.Tx, wid uuid.UUID, accountID *uuid.UUID, threadIDs []uuid.UUID, folders bool) error {
	accounts := []uuid.UUID{}
	if accountID != nil {
		accounts = append(accounts, *accountID)
	} else if len(threadIDs) > 0 {
		rows, err := tx.Query(ctx, `SELECT DISTINCT account_id FROM collab_threads WHERE workspace_id = $1 AND id = ANY($2)`, wid, threadIDs)
		if err != nil {
			return err
		}
		if accounts, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID]); err != nil {
			return err
		}
	}
	users := []uuid.UUID{}
	for _, a := range accounts {
		list, err := accountAudience(ctx, tx, wid, a)
		if err != nil {
			return err
		}
		users = appendNew(users, list...)
	}
	list, err := threadsAudience(ctx, tx, wid, threadIDs)
	if err != nil {
		return err
	}
	users = appendNew(users, list...)
	if threadIDs == nil {
		threadIDs = []uuid.UUID{}
	}
	_, err = events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: EventChanged, Users: users,
		Data: map[string]any{"account_id": accountID, "thread_ids": threadIDs, "folders": folders}})
	return err
}

// publishAccounts tells who sees an account that it changed, plus extra people (the
// audience before a change of sharing), in tx.
func (s *service) publishAccounts(ctx context.Context, tx pgx.Tx, wid, accountID uuid.UUID, extra []uuid.UUID) error {
	users, err := accountAudience(ctx, tx, wid, accountID)
	if err != nil {
		return err
	}
	users = appendNew(users, extra...)
	_, err = events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: EventAccounts, Users: users,
		Data: map[string]any{"account_id": accountID}})
	return err
}

func appendNew(list []uuid.UUID, add ...uuid.UUID) []uuid.UUID {
	for _, id := range add {
		if id != uuid.Nil && !slices.Contains(list, id) {
			list = append(list, id)
		}
	}
	return list
}

// Errors with a fixed answer.
var (
	errAudienceTooLarge = httpx.E(422, "validation_error", "Share with at most 50 teams and 200 people.")
	errUnknownTeam      = httpx.E(422, "validation_error", "A team you chose is not in this workspace.")
	errUnknownMember    = httpx.E(422, "validation_error", "A person you chose is not in this workspace.")
	errLabels           = httpx.E(422, "labels_unsupported", "IMAP accounts have no labels. Move the conversation to a folder instead.")
	errMoveToCategory   = httpx.E(422, "validation_error", "An Outlook category is a label: add it with Label, or move to a folder.")
	errFolderColor      = httpx.E(422, "validation_error", "Only Gmail labels and Outlook categories have colours.")
	errCategoryRename   = httpx.E(422, "rename_unsupported", "Outlook categories cannot be renamed. Create a new one instead.")
	errSpecialFolder    = httpx.E(422, "special_folder", "This folder belongs to the mail provider and cannot be changed.")
	errIMAPOff          = httpx.E(422, "imap_unavailable", "IMAP accounts are not available on this instance.")
	errConflict         = httpx.E(409, "account_conflict", "This address is already connected in this workspace.")
)

// errReconnect means the provider refused the stored grant or password: connect it again.
var errReconnect = errors.New("the account connection was refused. Connect it again")

// errProviderUnavailable is a passing provider failure; the job tries again.
var errProviderUnavailable = errors.New("the mail provider is not responding. We will try again")
