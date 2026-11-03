package collab

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// The AI queue (contracts/collab.md "AI queue"): an API key with collab:draft asks for
// the next conversation that needs a reply, holds it with a claim that expires on its own,
// then saves a draft (which ends the claim) or hands it back. Only what the key's member
// sees and may act on is offered, and only on shared accounts with handling on: a
// conversation in Needs action with no draft yet (a person's or an AI's) and no live
// claim. A draft does not clear Needs action, so the conversation waits there, "AI draft
// by <key name>", until a person sends or discards it.

const (
	claimDefault = 5 * time.Minute
	claimMin     = time.Minute
	claimMax     = 15 * time.Minute
	// claimsPerMinute bounds the claims one key asks for.
	claimsPerMinute = 60
	maxSkipReason   = 500
)

var (
	errNotAIKey    = httpx.E(403, "ai_queue_keys_only", "The AI queue is for API keys with the collab:draft scope.")
	errClaimLost   = httpx.E(409, "claim_lost", "This claim has expired or passed to another holder. Claim again.")
	errClaimTokens = httpx.E(422, "validation_failed", "claim_token is for an AI key's reply in the conversation it claimed.")
)

// claimAttempts is how many candidates claimNext tries when another claim wins the race.
const claimAttempts = 3

// AIClaim is a conversation the caller's key holds until ExpiresAt.
type AIClaim struct {
	ConversationID uuid.UUID `json:"conversation_id"`
	// Token names this claim: the draft that reports back carries it (claim_token).
	Token     uuid.UUID `json:"claim_token"`
	ClaimedAt time.Time `json:"claimed_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// claimLockKey is the advisory lock of conversation $1's claim: a fenced draft holds it
// from its check to its stored result, and a claim of the conversation skips it meanwhile.
const claimLockKey = `hashtextextended('collab_ai_claim:' || $1::text, 0)`

type aiClaimOut struct {
	Body struct {
		// Claim is nil when nothing is waiting.
		Claim        *AIClaim          `json:"claim"`
		Conversation *MailThreadDetail `json:"conversation"`
	}
}

// queueEligibleSQL is the predicate of a conversation (alias t) a key ($3) whose member is
// $2 may be offered, in workspace $1. The Needs action box holds only shared accounts
// with handling on.
func queueEligibleSQL() string {
	return `t.workspace_id = $1 AND t.account_id IN ` + visibleAccountsSQL("$2") + ` AND ` + boxSQL(BoxNeedsAction, "") + `
		AND NOT EXISTS (SELECT 1 FROM collab_messages dm WHERE dm.thread_id = t.id AND dm.draft)
		AND NOT EXISTS (SELECT 1 FROM collab_ai_claims c JOIN api_keys ck ON ck.id = c.api_key_id
			WHERE c.thread_id = t.id AND c.expires_at > now() AND ck.revoked_at IS NULL)
		AND NOT EXISTS (SELECT 1 FROM collab_ai_skips sk WHERE sk.thread_id = t.id AND sk.api_key_id = $3
			AND sk.last_message_at >= t.last_message_at)`
}

func (s *service) registerQueueRoutes(api huma.API) {
	collabOp(api, huma.Operation{OperationID: "claim-collab-ai-conversation", Method: http.MethodPost, Path: "/v1/collab/ai-queue/claim",
		Summary: "Claim the next conversation for an AI key to draft"},
		func(ctx context.Context, in *struct {
			Body *struct {
				AccountID  *uuid.UUID `json:"account_id,omitempty" doc:"Only this account's conversations"`
				TTLSeconds int        `json:"ttl_seconds,omitempty" minimum:"0" maximum:"900" doc:"How long the claim holds (60 to 900; default 300)"`
				Categories []string   `json:"categories,omitempty" maxItems:"8" doc:"Only conversations in these categories (keys; primary also takes accounts not split), once sorted"`
			}
		}) (*aiClaimOut, error) {
			p, err := s.requireQueueKey(ctx)
			if err != nil {
				return nil, err
			}
			if !s.d.Limiter.Allow("collab-ai-claim:"+p.APIKeyID.String(), claimsPerMinute, time.Minute) {
				return nil, httpx.RateLimited()
			}
			f := queueFilter{}
			ttl := claimDefault
			if in.Body != nil {
				if in.Body.TTLSeconds != 0 {
					ttl = min(max(time.Duration(in.Body.TTLSeconds)*time.Second, claimMin), claimMax)
				}
				f.account = in.Body.AccountID
				if f.categories, err = queueCategories(in.Body.Categories); err != nil {
					return nil, err
				}
			}
			if f.account != nil {
				if _, err := visibleAccount(ctx, s.d.DB.Pool, p, *f.account); err != nil {
					return nil, notFound(err, "Account")
				}
			}
			claim, err := s.claimNext(ctx, p, f, ttl)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out := &aiClaimOut{}
			if claim == nil {
				return out, nil
			}
			detail, err := s.threadDetail(ctx, s.d.DB.Pool, p, claim.ConversationID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out.Body.Claim, out.Body.Conversation = claim, &detail
			return out, nil
		})

	collabOp(api, huma.Operation{OperationID: "release-collab-ai-claim", Method: http.MethodPost,
		Path: "/v1/collab/ai-queue/claims/{id}/release", Summary: "Hand a claimed conversation back", DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			ID   string `path:"id" doc:"The conversation id"`
			Body *struct {
				Skip   bool   `json:"skip,omitempty" doc:"Do not offer it to this key again until new mail arrives"`
				Reason string `json:"reason,omitempty" maxLength:"500" doc:"Why, for example not confident; kept with the skip"`
			}
		}) (*struct{}, error) {
			p, err := s.requireQueueKey(ctx)
			if err != nil {
				return nil, err
			}
			id, err := parseID(in.ID, "Conversation")
			if err != nil {
				return nil, err
			}
			skip, reason := false, ""
			if in.Body != nil {
				skip, reason = in.Body.Skip, strings.TrimSpace(in.Body.Reason)
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				var last time.Time
				err := tx.QueryRow(ctx, `DELETE FROM collab_ai_claims c USING collab_threads t
					WHERE c.thread_id = $1 AND c.api_key_id = $2 AND c.workspace_id = $3 AND c.expires_at > now() AND t.id = c.thread_id
					RETURNING t.last_message_at`, id, p.APIKeyID, p.WorkspaceID).Scan(&last)
				if err != nil {
					return err
				}
				if !skip {
					return nil
				}
				_, err = tx.Exec(ctx, `INSERT INTO collab_ai_skips (thread_id, api_key_id, workspace_id, reason, last_message_at)
					VALUES ($1, $2, $3, $4, $5)
					ON CONFLICT (thread_id, api_key_id) DO UPDATE SET reason = EXCLUDED.reason,
						last_message_at = EXCLUDED.last_message_at, created_at = now()`,
					id, p.APIKeyID, p.WorkspaceID, clipRunes(reason, maxSkipReason), last)
				return err
			})
			if err != nil {
				// No live claim of this key: expired, never claimed, or another key's.
				return nil, notFound(err, "Claim")
			}
			return nil, nil
		})
}

// requireQueueKey is requireDrafter for an API key only, with an agent's role: a person
// works their inbox and never needs the queue.
func (s *service) requireQueueKey(ctx context.Context) (*auth.Principal, error) {
	p, err := s.requireDrafter(ctx, auth.RoleAgent)
	if err != nil {
		return nil, httpx.Wrap(err)
	}
	if !viaAIClient(p) {
		return nil, errNotAIKey
	}
	return p, nil
}

// claimNext claims the conversation that has waited longest among those the key may be
// offered, or returns nil. Concurrent claims skip each other's candidate rows, a claim
// replaces only an expired one (or a revoked key's), and a conversation whose fenced draft
// is being saved is skipped, so a conversation has one holder at a time. A claim that
// loses a race tries the next candidate, up to claimAttempts.
func (s *service) claimNext(ctx context.Context, p *auth.Principal, f queueFilter, ttl time.Duration) (*AIClaim, error) {
	for range claimAttempts {
		c, raced, err := s.claimOnce(ctx, p, f, ttl)
		if err != nil || !raced {
			return c, err
		}
	}
	return nil, nil
}

// queueFilter narrows what a claim is offered: one account, and categories.
type queueFilter struct {
	account    *uuid.UUID
	categories []string
}

// queueCategories checks a claim's categories: keys, at most maxCategories, without
// repeats. None means every category.
func queueCategories(in []string) ([]string, error) {
	if len(in) > maxCategories {
		return nil, httpx.Validation("At most 8 categories.")
	}
	var out []string
	for _, c := range in {
		c = strings.TrimSpace(c)
		if !categoryKeyRe.MatchString(c) {
			return nil, httpx.Validation("A category is a category key, such as primary.")
		}
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out, nil
}

// queueCategorySQL is the predicate of a conversation (alias t) in one of the categories
// of SQL parameter k, read as the inbox tabs read it: a conversation whose key its account
// no longer has, and one of an account not split, is primary. One waiting to be sorted
// (category_pending) is held back, so it is not offered as primary before it is filed.
func queueCategorySQL(k string) string {
	return `NOT t.category_pending AND EXISTS (SELECT 1 FROM collab_accounts qa WHERE qa.id = t.account_id
		AND (CASE WHEN t.category = ANY(qa.category_keys) THEN t.category ELSE '` + categoryPrimary + `' END) = ANY(` + k + `::text[]))`
}

// claimOnce is one attempt of claimNext; raced reports a candidate taken meanwhile.
func (s *service) claimOnce(ctx context.Context, p *auth.Principal, f queueFilter, ttl time.Duration) (*AIClaim, bool, error) {
	var out *AIClaim
	raced := false
	err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		args := []any{p.WorkspaceID, p.UserID, p.APIKeyID}
		where := queueEligibleSQL()
		if f.account != nil {
			args = append(args, *f.account)
			where += ` AND t.account_id = $` + strconv.Itoa(len(args))
		}
		if len(f.categories) > 0 {
			args = append(args, f.categories)
			where += ` AND ` + queueCategorySQL(`$`+strconv.Itoa(len(args)))
		}
		var id uuid.UUID
		err := tx.QueryRow(ctx, `SELECT t.id FROM collab_threads t WHERE `+where+`
			ORDER BY t.last_message_at ASC, t.id ASC LIMIT 1 FOR UPDATE OF t SKIP LOCKED`, args...).Scan(&id)
		if db.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		// A fenced draft of the conversation being saved holds its claim's lock.
		var free bool
		if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(`+claimLockKey+`)`, id).Scan(&free); err != nil {
			return err
		}
		if !free {
			raced = true
			return nil
		}
		var c AIClaim
		err = tx.QueryRow(ctx, `INSERT INTO collab_ai_claims (thread_id, workspace_id, api_key_id, token, claimed_at, expires_at)
			VALUES ($1, $2, $3, $5, now(), now() + make_interval(secs => $4))
			ON CONFLICT (thread_id) DO UPDATE SET api_key_id = EXCLUDED.api_key_id, token = EXCLUDED.token,
				claimed_at = EXCLUDED.claimed_at, expires_at = EXCLUDED.expires_at
			WHERE collab_ai_claims.expires_at <= now()
				OR NOT EXISTS (SELECT 1 FROM api_keys k WHERE k.id = collab_ai_claims.api_key_id AND k.revoked_at IS NULL)
			RETURNING thread_id, token, claimed_at, expires_at`, id, p.WorkspaceID, p.APIKeyID, ttl.Seconds(), uuid.New()).
			Scan(&c.ConversationID, &c.Token, &c.ClaimedAt, &c.ExpiresAt)
		if db.IsNotFound(err) {
			raced = true // claimed meanwhile
			return nil
		}
		if err != nil {
			return err
		}
		out = &c
		return nil
	})
	return out, raced, err
}

// fenceClaim checks that token names the key's live claim on conversation threadID and
// holds the claim's lock until unlock is called: a queue worker's draft is checked before
// the provider is called, and nobody can take the claim over until the draft is stored
// (which ends the claim). A worker whose claim expired or passed on is refused.
func (s *service) fenceClaim(ctx context.Context, p *auth.Principal, threadID, token uuid.UUID) (func(), error) {
	lockCtx, cancel := context.WithTimeout(ctx, draftLockWait)
	defer cancel()
	tx, err := s.d.DB.Locks.Begin(lockCtx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return nil, errDraftBusy
		}
		return nil, httpx.Wrap(err)
	}
	unlock := func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }
	// It waits its turn behind a claim being made, or another draft carrying this token
	// (which, once stored, ends the claim: this one is then refused).
	if _, err := tx.Exec(lockCtx, `SELECT set_config('lock_timeout', $1, true)`,
		strconv.FormatInt(draftLockWait.Milliseconds(), 10)+"ms"); err != nil {
		unlock()
		return nil, httpx.Wrap(err)
	}
	if _, err := tx.Exec(lockCtx, `SELECT pg_advisory_xact_lock(`+claimLockKey+`)`, threadID); err != nil {
		unlock()
		var pg *pgconn.PgError
		if (errors.As(err, &pg) && pg.Code == "55P03") || (errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil) {
			return nil, errDraftBusy
		}
		return nil, httpx.Wrap(err)
	}
	var live bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM collab_ai_claims c JOIN api_keys k ON k.id = c.api_key_id
		WHERE c.thread_id = $1 AND c.api_key_id = $2 AND c.token = $3 AND c.workspace_id = $4
			AND c.expires_at > now() AND k.revoked_at IS NULL)`, threadID, p.APIKeyID, token, p.WorkspaceID).Scan(&live); err != nil {
		unlock()
		return nil, httpx.Wrap(err)
	}
	if !live {
		unlock()
		return nil, errClaimLost
	}
	return unlock, nil
}

// endClaims ends claims on the conversation of a draft just saved, in tx. A person's
// draft ends any claim: they took the conversation on, and the holder's fenced draft is
// refused. A key's draft ends only that key's own claim: a late or manual draft by
// another key never ends the current holder's.
func endClaims(ctx context.Context, tx pgx.Tx, messageID uuid.UUID, ai *aiDrafter) error {
	if ai != nil {
		_, err := tx.Exec(ctx, `DELETE FROM collab_ai_claims WHERE api_key_id = $2
			AND thread_id = (SELECT thread_id FROM collab_messages WHERE id = $1)`, messageID, ai.APIKeyID)
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM collab_ai_claims WHERE thread_id = (SELECT thread_id FROM collab_messages WHERE id = $1)`, messageID)
	return err
}
