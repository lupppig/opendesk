package email

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/domain"
)

// Reading a Google or Microsoft inbox through the provider's API (an api grant,
// contracts/api.md "Mailboxes"): Gmail's history of messages added to INBOX, or Microsoft
// Graph's delta of the inbox folder. Each new message's raw source goes through ingest,
// as IMAP and forwarded mail do, which dedupes it and queues its processing. The cursor
// lives in mailboxes.api_sync_state; failures are stored on the mailbox with a safe
// message and backoff (setSyncError), and a refused grant asks for a reconnect.

// apiSyncState is a mailbox's place in its provider's changes.
type apiSyncState struct {
	// Account is the connected account the cursor belongs to; another one starts over.
	Account string `json:"account,omitempty"`
	// HistoryID is Gmail's: the history record the next pass reads after.
	HistoryID string `json:"history_id,omitempty"`
	// Delta is Graph's delta link of the last complete round, Next the next page of the
	// round in progress; Initial marks a round that started from nothing (the import
	// window), Imported how many messages it took so far.
	Delta    string `json:"delta,omitempty"`
	Next     string `json:"next,omitempty"`
	Initial  bool   `json:"initial,omitempty"`
	Imported int    `json:"imported,omitempty"`
	// LabelID is the Gmail id of LabelAccount's "opendesk" label (gmailLabelID); it outlives
	// a cursor that starts over.
	LabelID      string `json:"label_id,omitempty"`
	LabelAccount string `json:"label_account,omitempty"`
}

// apiPagesPerPass bounds the pages of changes one pass reads; the rest comes next pass.
const apiPagesPerPass = 50

// apiError is a provider refusal with a message that is safe to store and show: the
// status and the provider's error code, never its message (it can echo addresses).
type apiError struct {
	msg      string
	status   int
	notFound bool
	gone     bool // Graph refused an expired delta link
}

func (e *apiError) Error() string { return e.msg }

// errAPITooLarge is an answer over the request's limit (a message over maxRawBytes).
var errAPITooLarge = errors.New("the message is too large to import")

// errHistoryGone means Gmail no longer keeps the changes since the stored history id.
var errHistoryGone = &apiError{msg: "Gmail no longer has the changes since the last sync.", notFound: true}

// ingestFailure is storage or database trouble while storing a message: River logs it
// and the next pass tries again, instead of it showing on the mailbox.
type ingestFailure struct{ err error }

func (e *ingestFailure) Error() string { return e.err.Error() }
func (e *ingestFailure) Unwrap() error { return e.err }

// syncAPI runs one pass over a mailbox that reads through the provider's API. A
// suspended workspace's mailbox waits; one whose workspace lost Collab is kept and says so.
func (s *service) syncAPI(ctx context.Context, m *mailboxRow) error {
	suspended, err := domain.WorkspaceSuspended(ctx, s.d.DB.Pool, m.WorkspaceID)
	if err != nil || suspended {
		return err
	}
	a, err := s.access(ctx, s.d.DB.Pool, m.WorkspaceID)
	if err != nil {
		return err
	}
	provider := m.InboundKind
	if !a.collab {
		return s.recordSyncError(ctx, m, errAPINeedsCollab)
	}
	if !s.apiConfigured(provider) {
		return s.recordSyncError(ctx, m, &imapError{msg: "Reading this inbox directly through " + providerLabel(provider) +
			" is not set up on this instance. Forward its mail or connect it again."})
	}
	var raw []byte
	if err := s.d.DB.Pool.QueryRow(ctx, `SELECT api_sync_state FROM mailboxes WHERE workspace_id = $1 AND id = $2`,
		m.WorkspaceID, m.ID).Scan(&raw); err != nil {
		return err
	}
	var st apiSyncState
	_ = json.Unmarshal(raw, &st)
	account := strings.ToLower(m.Config.OAuthEmail)
	if st.Account != account {
		st = apiSyncState{Account: account, LabelID: st.LabelID, LabelAccount: st.LabelAccount}
	}
	switch provider {
	case KindGoogle:
		err = s.syncGmailInbox(ctx, m, &st)
	case KindMicrosoft:
		err = s.syncGraphInbox(ctx, m, &st)
	}
	if err != nil {
		// What was stored so far is not read again.
		if e := s.saveAPIState(ctx, m, st); e != nil {
			return e
		}
		var ingest *ingestFailure
		if errors.As(err, &ingest) {
			return ingest.err
		}
		return s.recordSyncError(ctx, m, err)
	}
	return s.setAPISyncOK(ctx, m, st)
}

// ingestAPI stores one message read from the provider, with where it lives there;
// oversized ones are skipped.
func (s *service) ingestAPI(ctx context.Context, m *mailboxRow, raw []byte, ref *providerRef) error {
	if len(raw) == 0 {
		return nil
	}
	if len(raw) > maxRawBytes {
		s.log.Warn("skipping oversized message", "mailbox_id", m.ID, "bytes", len(raw))
		return nil
	}
	if _, _, err := s.ingestRef(ctx, m, raw, "", nil, ref); err != nil {
		return &ingestFailure{err: err}
	}
	return nil
}

// apiGet reads one provider URL with the mailbox's token and returns a 2xx answer's body.
func (s *service) apiGet(ctx context.Context, m *mailboxRow, label, resource, target string, limit int64, headers map[string]string) ([]byte, error) {
	resp, body, err := s.apiCallLimit(ctx, m, resource, limit, func(token string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		return req, nil
	})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, readFailure(label, resp.StatusCode, body)
	}
	if int64(len(body)) > limit {
		return nil, errAPITooLarge
	}
	return body, nil
}

func (s *service) apiGetJSON(ctx context.Context, m *mailboxRow, label, resource, target string, limit int64, headers map[string]string, out any) error {
	if headers == nil {
		headers = map[string]string{}
	}
	headers["Accept"] = "application/json"
	body, err := s.apiGet(ctx, m, label, resource, target, limit, headers)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return &apiError{msg: label + " returned an unreadable answer."}
	}
	return nil
}

// readFailure turns a provider HTTP error on reading the inbox into a safe, classified error.
func readFailure(label string, status int, body []byte) error {
	var e struct {
		Error json.RawMessage `json:"error"`
	}
	_ = json.Unmarshal(body, &e)
	var detail struct {
		Code   any    `json:"code"`
		Status string `json:"status"`
		Errors []struct {
			Reason string `json:"reason"`
		} `json:"errors"`
	}
	_ = json.Unmarshal(e.Error, &detail)
	reasons := []string{detail.Status}
	if c, ok := detail.Code.(string); ok {
		reasons = append([]string{c}, reasons...) // Graph: "SyncStateNotFound"
	}
	for _, r := range detail.Errors {
		reasons = append(reasons, r.Reason)
	}
	has := func(list ...string) bool {
		for _, r := range reasons {
			for _, want := range list {
				if r != "" && strings.EqualFold(r, want) {
					return true
				}
			}
		}
		return false
	}
	shown := ""
	for _, r := range reasons {
		if r != "" {
			shown = " " + safeCode(r)
			break
		}
	}
	msg := fmt.Sprintf("%s refused the request (%d%s).", label, status, shown)
	switch {
	case status == http.StatusTooManyRequests || status >= 500 || has("rateLimitExceeded", "userRateLimitExceeded", "ApplicationThrottled"):
		return &apiError{msg: label + " is busy. We will try again.", status: status}
	case status == http.StatusGone || has("SyncStateNotFound", "SyncStateInvalid", "resyncRequired"):
		return &apiError{msg: msg, status: status, gone: true}
	case status == http.StatusNotFound || has("ErrorItemNotFound"):
		return &apiError{msg: msg, status: status, notFound: true}
	case status == http.StatusForbidden:
		return &apiError{msg: msg + " Reconnect the mailbox and allow it to read the inbox.", status: status}
	}
	return &apiError{msg: msg, status: status}
}

func isAPINotFound(err error) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.notFound
}

func isAPIGone(err error) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.gone
}

// saveAPIState stores the cursor mid-pass.
func (s *service) saveAPIState(ctx context.Context, m *mailboxRow, st apiSyncState) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	_, err = s.d.DB.Pool.Exec(ctx, `UPDATE mailboxes SET api_sync_state = $3 WHERE workspace_id = $1 AND id = $2`, m.WorkspaceID, m.ID, b)
	return err
}

// setAPISyncOK records a successful pass with its cursor; it publishes mailbox.updated
// only when the mailbox leaves the error state.
func (s *service) setAPISyncOK(ctx context.Context, m *mailboxRow, st apiSyncState) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var prevStatus string
		err := tx.QueryRow(ctx, `UPDATE mailboxes m SET status = CASE WHEN m.status = 'error' THEN 'active' ELSE m.status END,
			last_error = '', sync_failures = 0, next_sync_at = NULL, last_synced_at = now(), api_sync_state = $3, updated_at = now()
			FROM (SELECT status FROM mailboxes WHERE id = $2 FOR UPDATE) old
			WHERE m.workspace_id = $1 AND m.id = $2 RETURNING old.status`, m.WorkspaceID, m.ID, b).Scan(&prevStatus)
		if err != nil {
			return err
		}
		if prevStatus == "error" {
			return s.publishMailbox(ctx, tx, m.WorkspaceID, m.ID, "mailbox.updated")
		}
		return nil
	})
}

// testAPIInbound checks that the inbox can be read without reading any mail.
func (s *service) testAPIInbound(ctx context.Context, m *mailboxRow) error {
	a, err := s.access(ctx, s.d.DB.Pool, m.WorkspaceID)
	if err != nil {
		return err
	}
	if !s.apiReceives(m.InboundKind, a) {
		return errAPINeedsCollab
	}
	var out struct {
		ID string `json:"id"`
	}
	if m.InboundKind == KindMicrosoft {
		return s.apiGetJSON(ctx, m, "Microsoft", resGraph, s.graphBase+"/me/mailFolders/inbox?$select=id", 1<<20, nil, &out)
	}
	return s.apiGetJSON(ctx, m, "Gmail", resIMAP, s.gmailBase+"/gmail/v1/users/me/labels/INBOX", 1<<20, nil, &out)
}

// firstWindow is how far back a round that reads the inbox from scratch starts: nothing
// on the mailbox's first read with sync_start new, else the import window. A round that
// starts over because the provider dropped the cursor reads the window, so nothing that
// arrived since the last pass is missed.
func firstWindow(m *mailboxRow, first bool) time.Duration {
	if first && m.Config.SyncStart == SyncStartNew {
		return 0
	}
	return initialWindow
}

// Gmail.

func (s *service) gmailURL(path string) string { return s.gmailBase + "/gmail/v1/users/me" + path }

// skipGmail reports a message that is not mail to the mailbox: a draft, spam, or one the
// mailbox sent (SENT without INBOX).
func skipGmail(labels []string) bool {
	return slices.Contains(labels, "DRAFT") || slices.Contains(labels, "SPAM") ||
		(slices.Contains(labels, "SENT") && !slices.Contains(labels, "INBOX"))
}

// syncGmailInbox reads the history since the stored id, or, without one (or when Gmail
// no longer keeps it), the inbox of the import window.
func (s *service) syncGmailInbox(ctx context.Context, m *mailboxRow, st *apiSyncState) error {
	window := firstWindow(m, st.HistoryID == "")
	if st.HistoryID != "" {
		err := s.gmailHistory(ctx, m, st)
		if !errors.Is(err, errHistoryGone) {
			return err
		}
		s.log.Info("gmail history expired, reading the inbox again", "mailbox_id", m.ID)
		st.HistoryID = ""
	}
	// The history id is read first so nothing that arrives during the import is missed.
	var prof struct {
		HistoryID string `json:"historyId"`
	}
	if err := s.apiGetJSON(ctx, m, "Gmail", resIMAP, s.gmailURL("/profile"), 1<<20, nil, &prof); err != nil {
		return err
	}
	q := fmt.Sprintf("after:%d", time.Now().Add(-window).Unix())
	ids := []string{}
	token := ""
	for window > 0 && len(ids) < initialMax {
		v := url.Values{"labelIds": {"INBOX"}, "q": {q}, "maxResults": {strconv.Itoa(min(initialMax-len(ids), 500))}}
		if token != "" {
			v.Set("pageToken", token)
		}
		var page struct {
			Messages []struct {
				ID string `json:"id"`
			} `json:"messages"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := s.apiGetJSON(ctx, m, "Gmail", resIMAP, s.gmailURL("/messages?"+v.Encode()), 4<<20, nil, &page); err != nil {
			return err
		}
		for _, msg := range page.Messages {
			if msg.ID != "" && len(ids) < initialMax && !slices.Contains(ids, msg.ID) {
				ids = append(ids, msg.ID)
			}
		}
		if page.NextPageToken == "" || len(page.Messages) == 0 {
			break
		}
		token = page.NextPageToken
	}
	// Newest first from Gmail; stored oldest first so conversations thread in order.
	for i := len(ids) - 1; i >= 0; i-- {
		if err := s.ingestGmail(ctx, m, ids[i]); err != nil {
			return err
		}
	}
	st.HistoryID = prof.HistoryID
	return nil
}

// gmailHistory reads the messages added to INBOX since st.HistoryID and moves it to the
// last history record it handled. At most syncBatch messages are fetched per pass.
func (s *service) gmailHistory(ctx context.Context, m *mailboxRow, st *apiSyncState) error {
	since := st.HistoryID
	token := ""
	fetched := 0
	seen := map[string]bool{}
	for pages := 0; pages < apiPagesPerPass; pages++ {
		v := url.Values{"startHistoryId": {since}, "historyTypes": {"messageAdded"}, "labelId": {"INBOX"}, "maxResults": {"500"}}
		if token != "" {
			v.Set("pageToken", token)
		}
		var page struct {
			History []struct {
				ID            string `json:"id"`
				MessagesAdded []struct {
					Message struct {
						ID       string   `json:"id"`
						LabelIDs []string `json:"labelIds"`
					} `json:"message"`
				} `json:"messagesAdded"`
			} `json:"history"`
			HistoryID     string `json:"historyId"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := s.apiGetJSON(ctx, m, "Gmail", resIMAP, s.gmailURL("/history?"+v.Encode()), 8<<20, nil, &page); err != nil {
			if isAPINotFound(err) {
				return errHistoryGone
			}
			return err
		}
		for _, h := range page.History {
			for _, added := range h.MessagesAdded {
				msg := added.Message
				if msg.ID == "" || seen[msg.ID] || skipGmail(msg.LabelIDs) {
					continue
				}
				seen[msg.ID] = true
				if err := s.ingestGmail(ctx, m, msg.ID); err != nil {
					return err
				}
				fetched++
			}
			if h.ID != "" {
				st.HistoryID = h.ID
			}
			if fetched >= syncBatch {
				return nil // the rest next pass, after this record
			}
		}
		if page.NextPageToken == "" {
			if page.HistoryID != "" {
				st.HistoryID = page.HistoryID
			}
			return nil
		}
		token = page.NextPageToken
	}
	return nil
}

// ingestGmail fetches one message's raw source and stores it. A message gone since, too
// large, or not mail to the mailbox is skipped.
func (s *service) ingestGmail(ctx context.Context, m *mailboxRow, id string) error {
	var msg struct {
		ThreadID string   `json:"threadId"`
		LabelIDs []string `json:"labelIds"`
		Raw      string   `json:"raw"`
	}
	err := s.apiGetJSON(ctx, m, "Gmail", resIMAP, s.gmailURL("/messages/"+url.PathEscape(id)+"?format=raw"),
		maxRawBytes*4/3+64<<10, nil, &msg)
	switch {
	case isAPINotFound(err):
		return nil
	case errors.Is(err, errAPITooLarge):
		s.log.Warn("skipping oversized message", "mailbox_id", m.ID)
		return nil
	case err != nil:
		return err
	}
	if skipGmail(msg.LabelIDs) {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(msg.Raw, "="))
	if err != nil {
		return &apiError{msg: "Gmail returned an unreadable message."}
	}
	return s.ingestAPI(ctx, m, raw, newProviderRef(m, id, msg.ThreadID))
}

// Microsoft Graph.

// graphInboxSelect is what a delta page lists of each message.
const graphInboxSelect = "id,receivedDateTime,isDraft,conversationId"

// syncGraphInbox reads the inbox's delta from the stored link; without one it starts a
// round with the import window. Every page moves the cursor, so an interrupted round
// goes on where it stopped; an expired link starts over.
func (s *service) syncGraphInbox(ctx context.Context, m *mailboxRow, st *apiSyncState) error {
	err := s.graphInboxDelta(ctx, m, st, firstWindow(m, true))
	if !isAPIGone(err) {
		return err
	}
	s.log.Info("graph delta expired, reading the inbox again", "mailbox_id", m.ID)
	*st = apiSyncState{Account: st.Account, LabelID: st.LabelID, LabelAccount: st.LabelAccount}
	return s.graphInboxDelta(ctx, m, st, initialWindow)
}

// graphInboxDelta reads one pass of the delta; a new round reads back window.
func (s *service) graphInboxDelta(ctx context.Context, m *mailboxRow, st *apiSyncState, window time.Duration) error {
	// The token only ever goes to Graph itself.
	ownLink := func(link string) bool { return strings.HasPrefix(link, s.graphBase+"/") }
	target := ""
	switch {
	case st.Next != "" && ownLink(st.Next):
		target = st.Next
	case st.Delta != "" && ownLink(st.Delta):
		target, st.Initial, st.Imported = st.Delta, false, 0
	default:
		since := time.Now().Add(-window).UTC().Format(time.RFC3339)
		target = s.graphBase + "/me/mailFolders/inbox/messages/delta?$select=" + graphInboxSelect +
			"&$filter=" + url.QueryEscape("receivedDateTime ge "+since)
		*st = apiSyncState{Account: st.Account, Initial: true, LabelID: st.LabelID, LabelAccount: st.LabelAccount}
	}
	fetched := 0
	// Immutable ids, so the tidy-up finds a message by the id it was received with.
	headers := map[string]string{"Prefer": "odata.maxpagesize=50, " + graphImmutableIDs}
	for pages := 0; pages < apiPagesPerPass && target != ""; pages++ {
		var page struct {
			Value []struct {
				ID             string    `json:"id"`
				ConversationID string    `json:"conversationId"`
				IsDraft        bool      `json:"isDraft"`
				Removed        *struct{} `json:"@removed"`
			} `json:"value"`
			NextLink  string `json:"@odata.nextLink"`
			DeltaLink string `json:"@odata.deltaLink"`
		}
		if err := s.apiGetJSON(ctx, m, "Microsoft", resGraph, target, 8<<20, headers, &page); err != nil {
			return err
		}
		for _, v := range page.Value {
			if v.Removed != nil || v.IsDraft || v.ID == "" {
				continue
			}
			if st.Initial && st.Imported >= initialMax {
				continue // the first round imports at most initialMax messages
			}
			if err := s.ingestGraph(ctx, m, v.ID, v.ConversationID); err != nil {
				return err
			}
			fetched++
			if st.Initial {
				st.Imported++
			}
		}
		switch {
		case page.NextLink != "" && ownLink(page.NextLink):
			st.Next, target = page.NextLink, page.NextLink
		case page.DeltaLink != "" && ownLink(page.DeltaLink):
			st.Next, st.Delta, st.Initial, st.Imported, target = "", page.DeltaLink, false, 0, ""
		default:
			return &apiError{msg: "Microsoft returned an unreadable answer."}
		}
		if fetched >= syncBatch && !st.Initial {
			return nil // the rest next pass, from the saved page
		}
	}
	return nil
}

// ingestGraph fetches one message's MIME source and stores it.
func (s *service) ingestGraph(ctx context.Context, m *mailboxRow, id, conversation string) error {
	raw, err := s.apiGet(ctx, m, "Microsoft", resGraph, s.graphBase+"/me/messages/"+url.PathEscape(id)+"/$value", maxRawBytes, nil)
	switch {
	case isAPINotFound(err):
		return nil
	case errors.Is(err, errAPITooLarge):
		s.log.Warn("skipping oversized message", "mailbox_id", m.ID)
		return nil
	case err != nil:
		return err
	}
	return s.ingestAPI(ctx, m, raw, newProviderRef(m, id, conversation))
}
