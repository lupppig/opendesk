package email

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/jobs"
)

// A support mailbox that reads its inbox through the Gmail API or Microsoft Graph (an api
// grant) stays the same mailbox in Gmail or Outlook (contracts/api.md "Reading the inbox
// directly"): each email it receives or sends records the provider's ids (providerRef, on
// inbound_emails and messages), replies join the provider's conversation (Gmail's threadId;
// Graph by In-Reply-To and References, which every reply carries), and the mailbox's
// tidy-up options mark read, label and archive its emails there in River jobs that never
// affect the ticket.

// providerRef is where a message lives at the provider.
type providerRef struct {
	MailboxID uuid.UUID `json:"mailbox_id"`
	// Account is the connected account (lowercased) the ids belong to; another account's
	// ids are never used.
	Account  string `json:"account"`
	Provider string `json:"provider"`
	// ID is Gmail's message id or Graph's (immutable) message id; Thread is Gmail's
	// threadId or Graph's conversationId.
	ID     string `json:"id"`
	Thread string `json:"thread,omitempty"`
}

// newProviderRef is a ref to a message of m's connected account.
func newProviderRef(m *mailboxRow, id, thread string) *providerRef {
	return &providerRef{MailboxID: m.ID, Account: strings.ToLower(m.Config.OAuthEmail), Provider: m.oauthProvider(), ID: id, Thread: thread}
}

// graphImmutableIDs asks Graph for ids that stay the same when a message moves folders, so
// an archived message is still found by the id it was received with.
const graphImmutableIDs = `IdType="ImmutableId"`

// providerThread is the provider conversation a reply on the ticket from m joins: the
// latest email of the ticket that m's connected account received (else sent) through the
// provider's API, and its subject. nil unless m sends through the provider's API.
func (s *service) providerThread(ctx context.Context, wid, ticketID, exclude uuid.UUID, m *mailboxRow) (*providerRef, string, error) {
	if !m.sendsAPI() {
		return nil, "", nil
	}
	var ref providerRef
	var subject string
	err := s.d.DB.Pool.QueryRow(ctx, `SELECT provider_ref, subject FROM messages
		WHERE workspace_id = $1 AND ticket_id = $2 AND id <> $3 AND kind = 'reply'
		AND provider_ref->>'mailbox_id' = $4 AND provider_ref->>'account' = $5 AND coalesce(provider_ref->>'thread', '') <> ''
		ORDER BY (direction = 'inbound') DESC, created_at DESC LIMIT 1`,
		wid, ticketID, exclude, m.ID.String(), strings.ToLower(m.Config.OAuthEmail)).Scan(&ref, &subject)
	if db.IsNotFound(err) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	return &ref, subject, nil
}

// providerReplySubject is the subject Gmail and Outlook keep in one conversation: "Re: "
// and the conversation's own subject, without a ticket tag added (they compare subjects).
// "" when that subject is empty.
func providerReplySubject(subject string) string {
	base := strings.Join(strings.Fields(replyPrefixRe.ReplaceAllString(subject, "")), " ")
	if base == "" {
		return ""
	}
	return "Re: " + base
}

// queueImportedTidy marks the email of a new ticket message read and labels it at the
// provider, as the mailbox asks. Called in the transaction that adds the message.
func (s *service) queueImportedTidy(ctx context.Context, tx pgx.Tx, m *mailboxRow, ref *providerRef, ticketID, messageID uuid.UUID) error {
	if ref == nil || ref.MailboxID != m.ID || !m.readsAPI() || !(m.Config.ProviderMarkRead || m.Config.ProviderLabel) {
		return nil
	}
	_, err := s.d.Jobs.InsertTx(ctx, tx, jobs.ProviderTidyArgs{WorkspaceID: m.WorkspaceID, TicketID: ticketID, MessageID: &messageID,
		Action: jobs.ProviderImported}, &river.InsertOpts{Queue: jobs.QueueEmail, MaxAttempts: domain.ProviderTidyAttempts})
	return err
}

type providerTidyWorker struct {
	river.WorkerDefaults[jobs.ProviderTidyArgs]
	s *service
}

func (w *providerTidyWorker) Work(ctx context.Context, job *river.Job[jobs.ProviderTidyArgs]) error {
	err := w.s.providerTidy(ctx, job.Args)
	if err == nil {
		return nil
	}
	// A refusal that trying again will not change (a revoked grant, a refused request) is
	// given up; the provider being busy or unreachable is retried with backoff.
	var se *sendError
	var ae *apiError
	if errors.As(err, &se) && se.permanent || errors.As(err, &ae) && ae.status/100 == 4 && ae.status != http.StatusTooManyRequests {
		w.s.log.Warn("provider tidy-up given up", "workspace_id", job.Args.WorkspaceID, "ticket_id", job.Args.TicketID,
			"action", job.Args.Action, "reason", err.Error())
		return river.JobCancel(err)
	}
	return err
}

// providerTidy applies the tidy-up options of the mailboxes that received the ticket's
// emails (or the one email), for each mailbox as its settings say now.
func (s *service) providerTidy(ctx context.Context, a jobs.ProviderTidyArgs) error {
	if suspended, err := domain.WorkspaceSuspended(ctx, s.d.DB.Pool, a.WorkspaceID); err != nil || suspended {
		return err
	}
	rows, err := s.d.DB.Pool.Query(ctx, `SELECT provider_ref FROM messages
		WHERE workspace_id = $1 AND ticket_id = $2 AND direction = 'inbound' AND provider_ref IS NOT NULL
		AND ($3::uuid IS NULL OR id = $3) ORDER BY created_at`, a.WorkspaceID, a.TicketID, a.MessageID)
	if err != nil {
		return err
	}
	refs, err := pgx.CollectRows(rows, pgx.RowTo[providerRef])
	if err != nil {
		return err
	}
	byMailbox := map[uuid.UUID][]providerRef{}
	var order []uuid.UUID
	for _, r := range refs {
		if r.ID == "" {
			continue
		}
		if _, ok := byMailbox[r.MailboxID]; !ok {
			order = append(order, r.MailboxID)
		}
		byMailbox[r.MailboxID] = append(byMailbox[r.MailboxID], r)
	}
	var acc *access
	for _, id := range order {
		m, err := loadMailbox(ctx, s.d.DB.Pool, a.WorkspaceID, id)
		if db.IsNotFound(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !m.readsAPI() || m.Status == "disabled" {
			continue
		}
		var t tidy
		switch a.Action {
		case jobs.ProviderImported:
			t.markRead, t.label = m.Config.ProviderMarkRead, m.Config.ProviderLabel
		case jobs.ProviderArchive:
			t.archive = m.Config.ProviderArchiveOnSolve
		}
		if !t.markRead && !t.label && !t.archive {
			continue
		}
		if acc == nil {
			v, err := s.access(ctx, s.d.DB.Pool, a.WorkspaceID)
			if err != nil {
				return err
			}
			acc = &v
		}
		if !s.apiReceives(m.InboundKind, *acc) {
			continue
		}
		account := strings.ToLower(m.Config.OAuthEmail)
		var ids []string
		for _, r := range byMailbox[id] {
			if r.Account == account && r.Provider == m.InboundKind && !slices.Contains(ids, r.ID) {
				ids = append(ids, r.ID)
			}
		}
		if len(ids) == 0 {
			continue
		}
		switch m.InboundKind {
		case KindGoogle:
			err = s.gmailTidy(ctx, m, ids, t)
		case KindMicrosoft:
			err = s.graphTidy(ctx, m, ids, t)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// tidy is what to do to a mailbox's emails at the provider.
type tidy struct {
	markRead, label, archive bool
}

// apiSendJSON sends one JSON request to the provider with the mailbox's token; out, when
// set, receives a 2xx answer's body.
func (s *service) apiSendJSON(ctx context.Context, m *mailboxRow, label, resource, method, target string, body any, headers map[string]string, out any) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return err
		}
	}
	resp, answer, err := s.apiCall(ctx, m, resource, func(token string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		return req, nil
	})
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return readFailure(label, resp.StatusCode, answer)
	}
	if out != nil && len(answer) > 0 {
		if err := json.Unmarshal(answer, out); err != nil {
			return &apiError{msg: label + " returned an unreadable answer."}
		}
	}
	return nil
}

// gmailTidy changes the messages' labels in one batchModify per thousand: UNREAD and INBOX
// removed to mark read and archive, the opendesk label added. Messages gone since are left.
func (s *service) gmailTidy(ctx context.Context, m *mailboxRow, ids []string, t tidy) error {
	add, remove := []string{}, []string{}
	if t.markRead {
		remove = append(remove, "UNREAD")
	}
	if t.archive {
		remove = append(remove, "INBOX")
	}
	if t.label {
		id, err := s.gmailLabelID(ctx, m)
		if err != nil {
			return err
		}
		add = append(add, id)
	}
	for len(ids) > 0 {
		n := min(len(ids), 1000)
		err := s.apiSendJSON(ctx, m, "Gmail", resIMAP, http.MethodPost, s.gmailURL("/messages/batchModify"),
			map[string]any{"ids": ids[:n], "addLabelIds": add, "removeLabelIds": remove}, nil, nil)
		if err != nil && !isAPINotFound(err) {
			return err
		}
		ids = ids[n:]
	}
	return nil
}

// gmailLabelID is the id of the account's "opendesk" label, made when it has none. The id
// is kept with the account in mailboxes.api_sync_state; a stale one is looked up again.
func (s *service) gmailLabelID(ctx context.Context, m *mailboxRow) (string, error) {
	account := strings.ToLower(m.Config.OAuthEmail)
	var raw []byte
	if err := s.d.DB.Pool.QueryRow(ctx, `SELECT api_sync_state FROM mailboxes WHERE workspace_id = $1 AND id = $2`,
		m.WorkspaceID, m.ID).Scan(&raw); err != nil {
		return "", err
	}
	var st apiSyncState
	_ = json.Unmarshal(raw, &st)
	if st.LabelID != "" && st.LabelAccount == account {
		return st.LabelID, nil
	}
	find := func() (string, error) {
		var list struct {
			Labels []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"labels"`
		}
		if err := s.apiGetJSON(ctx, m, "Gmail", resIMAP, s.gmailURL("/labels"), 4<<20, nil, &list); err != nil {
			return "", err
		}
		for _, l := range list.Labels {
			if strings.EqualFold(l.Name, providerLabelName) && l.ID != "" {
				return l.ID, nil
			}
		}
		return "", nil
	}
	id, err := find()
	if err != nil {
		return "", err
	}
	if id == "" {
		var made struct {
			ID string `json:"id"`
		}
		err := s.apiSendJSON(ctx, m, "Gmail", resIMAP, http.MethodPost, s.gmailURL("/labels"), map[string]string{"name": providerLabelName,
			"labelListVisibility": "labelShow", "messageListVisibility": "show"}, nil, &made)
		var ae *apiError
		switch {
		case errors.As(err, &ae) && ae.status == http.StatusConflict:
			// Made meanwhile by another job.
			if id, err = find(); err != nil {
				return "", err
			}
		case err != nil:
			return "", err
		default:
			id = made.ID
		}
		if id == "" {
			return "", &apiError{msg: "Gmail returned an unreadable answer."}
		}
	}
	if _, err := s.d.DB.Pool.Exec(ctx, `UPDATE mailboxes SET api_sync_state = api_sync_state || jsonb_build_object('label_id', $3::text, 'label_account', $4::text)
		WHERE workspace_id = $1 AND id = $2`, m.WorkspaceID, m.ID, id, account); err != nil {
		return "", err
	}
	return id, nil
}

// graphTidy marks read and adds the opendesk category in one PATCH per message, and
// archives with a move to the Archive folder. Messages gone since are left.
func (s *service) graphTidy(ctx context.Context, m *mailboxRow, ids []string, t tidy) error {
	headers := map[string]string{"Prefer": graphImmutableIDs}
	for _, id := range ids {
		msgURL := s.graphBase + "/me/messages/" + url.PathEscape(id)
		if t.markRead || t.label {
			patch := map[string]any{}
			if t.markRead {
				patch["isRead"] = true
			}
			if t.label {
				var cur struct {
					Categories []string `json:"categories"`
				}
				err := s.apiGetJSON(ctx, m, "Microsoft", resGraph, msgURL+"?$select=categories", 1<<20, map[string]string{"Prefer": graphImmutableIDs}, &cur)
				if isAPINotFound(err) {
					continue
				}
				if err != nil {
					return err
				}
				if !slices.ContainsFunc(cur.Categories, func(c string) bool { return strings.EqualFold(c, providerLabelName) }) {
					patch["categories"] = append(cur.Categories, providerLabelName)
				}
			}
			if len(patch) > 0 {
				err := s.apiSendJSON(ctx, m, "Microsoft", resGraph, http.MethodPatch, msgURL, patch, headers, nil)
				if isAPINotFound(err) {
					continue
				}
				if err != nil {
					return err
				}
			}
		}
		if t.archive {
			err := s.apiSendJSON(ctx, m, "Microsoft", resGraph, http.MethodPost, msgURL+"/move", map[string]string{"destinationId": "archive"}, headers, nil)
			if err != nil && !isAPINotFound(err) {
				return err
			}
		}
	}
	return nil
}

// providerNames are the names the connected account sends as, for a new connection's
// From name: Gmail's send-as addresses, or the Microsoft account's display name.
type providerNames struct {
	byAddress map[string]string
	primary   string
}

// forAddress is the name for addr: its own send-as name, else the account's.
func (n providerNames) forAddress(addr string) string {
	if v := n.byAddress[strings.ToLower(addr)]; v != "" {
		return v
	}
	return n.primary
}

// fromNames reads the names an api connection sends as. Best effort: a failure leaves the
// From name to the admin.
func (s *service) fromNames(ctx context.Context, provider string, c credentials, account string) providerNames {
	out := providerNames{byAddress: map[string]string{}}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	get := func(target, token string, v any) bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return false
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		resp, err := s.http.Do(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return false
		}
		return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v) == nil
	}
	clean := func(name string) string { return displayname.CleanOr(name, displayname.Workspace, "") }
	switch provider {
	case KindGoogle:
		var list struct {
			SendAs []struct {
				Email       string `json:"sendAsEmail"`
				DisplayName string `json:"displayName"`
				IsPrimary   bool   `json:"isPrimary"`
			} `json:"sendAs"`
		}
		if !get(s.gmailURL("/settings/sendAs"), c.AccessToken, &list) {
			return out
		}
		for _, a := range list.SendAs {
			name := clean(a.DisplayName)
			if name == "" {
				continue
			}
			out.byAddress[strings.ToLower(a.Email)] = name
			if a.IsPrimary || strings.EqualFold(a.Email, account) {
				out.primary = name
			}
		}
	case KindMicrosoft:
		var me struct {
			DisplayName string `json:"displayName"`
		}
		if get(s.graphBase+"/me?$select=displayName", c.GraphAccessToken, &me) {
			out.primary = clean(me.DisplayName)
		}
	}
	return out
}
