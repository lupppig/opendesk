package collab

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Gmail through the Gmail API (gmail.modify). Gmail keeps mail under labels: its system
// labels are the special folders (INBOX inbox, SENT sent, DRAFT drafts, TRASH trash, SPAM
// spam, STARRED starred, IMPORTANT important) and its user labels are labels. UNREAD is
// the unread flag; CHAT and the CATEGORY_* tabs are not shown.
//
// Gmail has no Archive label: archiving removes INBOX, and archived mail is in no folder
// but its user labels. So a Gmail account has no folder with the role archive, and its
// "archive" box is the conversations in none of the folders whose role is inbox, trash,
// spam or drafts. Gmail mail is only removed when Gmail reports it deleted (history),
// never because it is in no folder.

var gmailRoles = map[string]string{
	"INBOX": RoleInbox, "SENT": RoleSent, "DRAFT": RoleDrafts, "TRASH": RoleTrash, "SPAM": RoleSpam,
	"STARRED": RoleStarred, "IMPORTANT": RoleImportant,
}

type gmailProvider struct {
	s *service
	a *accountRow
}

func (g *gmailProvider) url(path string) string { return g.s.gmailBase + "/gmail/v1/users/me" + path }

func (g *gmailProvider) do(ctx context.Context, method, path string, in, out any) error {
	return g.s.callJSON(ctx, g.a, "Gmail", method, g.url(path), in, out, nil)
}

func (g *gmailProvider) Close() {}

type gmailLabel struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Color *struct {
		BackgroundColor string `json:"backgroundColor"`
	} `json:"color"`
}

func (l gmailLabel) remote() (RemoteFolder, bool) {
	if l.Type == "system" {
		role, ok := gmailRoles[l.ID]
		if !ok {
			return RemoteFolder{}, false
		}
		name := gmailSystemName(l.ID)
		return RemoteFolder{RemoteID: l.ID, Name: name, Path: name, Role: role, Kind: KindFolder}, true
	}
	if strings.HasPrefix(l.ID, "CATEGORY_") || l.ID == "UNREAD" || l.ID == "CHAT" || l.Name == "" {
		return RemoteFolder{}, false
	}
	f := RemoteFolder{RemoteID: l.ID, Name: lastSegment(l.Name, "/"), Path: l.Name, Kind: KindLabel}
	if l.Color != nil {
		f.Color = l.Color.BackgroundColor
	}
	return f, true
}

func gmailSystemName(id string) string {
	switch id {
	case "DRAFT":
		return "Drafts"
	case "SPAM":
		return "Spam"
	}
	return strings.ToUpper(id[:1]) + strings.ToLower(id[1:])
}

func lastSegment(path, sep string) string {
	if i := strings.LastIndex(path, sep); i >= 0 && i < len(path)-1 {
		return path[i+len(sep):]
	}
	return path
}

func (g *gmailProvider) Folders(ctx context.Context) ([]RemoteFolder, error) {
	var out struct {
		Labels []gmailLabel `json:"labels"`
	}
	if err := g.do(ctx, http.MethodGet, "/labels", nil, &out); err != nil {
		return nil, err
	}
	list := []RemoteFolder{}
	for _, l := range out.Labels {
		if f, ok := l.remote(); ok {
			list = append(list, f)
		}
	}
	sortFolders(list)
	return list, nil
}

func (g *gmailProvider) CreateFolder(ctx context.Context, name string, parent *RemoteFolder) (RemoteFolder, error) {
	full := name
	if parent != nil {
		full = parent.Path + "/" + name
	}
	var l gmailLabel
	in := map[string]string{"name": full, "labelListVisibility": "labelShow", "messageListVisibility": "show"}
	if err := g.do(ctx, http.MethodPost, "/labels", in, &l); err != nil {
		return RemoteFolder{}, err
	}
	f, ok := l.remote()
	if !ok {
		return RemoteFolder{}, &providerError{msg: "Gmail did not create the label."}
	}
	return f, nil
}

func (g *gmailProvider) RenameFolder(ctx context.Context, f RemoteFolder, name string) (RemoteFolder, error) {
	full := name
	if i := strings.LastIndex(f.Path, "/"); i >= 0 {
		full = f.Path[:i+1] + name
	}
	var l gmailLabel
	if err := g.do(ctx, http.MethodPatch, "/labels/"+url.PathEscape(f.RemoteID), map[string]string{"name": full}, &l); err != nil {
		return RemoteFolder{}, err
	}
	out, ok := l.remote()
	if !ok {
		return RemoteFolder{}, &providerError{msg: "Gmail did not rename the label."}
	}
	return out, nil
}

func (g *gmailProvider) DeleteFolder(ctx context.Context, f RemoteFolder) error {
	err := g.do(ctx, http.MethodDelete, "/labels/"+url.PathEscape(f.RemoteID), nil, nil)
	if isNotFound(err) {
		return nil
	}
	return err
}

// gmailMessage is a message as users.messages.get answers.
type gmailMessage struct {
	ID           string   `json:"id"`
	ThreadID     string   `json:"threadId"`
	LabelIDs     []string `json:"labelIds"`
	HistoryID    string   `json:"historyId"`
	InternalDate string   `json:"internalDate"`
	Raw          string   `json:"raw"`
}

func (m *gmailMessage) date() time.Time {
	ms, err := strconv.ParseInt(m.InternalDate, 10, 64)
	if err != nil || ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// getRaw reads one message with its raw source. A message over maxRawBytes answers
// errTooLarge.
func (g *gmailProvider) getRaw(ctx context.Context, id string) (*gmailMessage, []byte, error) {
	body, err := g.s.call(ctx, g.a, "Gmail", restRequest{method: http.MethodGet,
		url: g.url("/messages/" + url.PathEscape(id) + "?format=raw"), limit: maxRawBytes*4/3 + 64<<10})
	if err != nil {
		return nil, nil, err
	}
	var m gmailMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, nil, &providerError{msg: "Gmail returned an unreadable message."}
	}
	raw, err := base64.URLEncoding.DecodeString(padBase64(m.Raw))
	if err != nil {
		if raw, err = base64.RawURLEncoding.DecodeString(strings.TrimRight(m.Raw, "=")); err != nil {
			return nil, nil, &providerError{msg: "Gmail returned an unreadable message."}
		}
	}
	m.Raw = ""
	return &m, raw, nil
}

func padBase64(s string) string {
	if n := len(s) % 4; n != 0 {
		s += strings.Repeat("=", 4-n)
	}
	return s
}

func (g *gmailProvider) FetchRaw(ctx context.Context, ref MessageRef) ([]byte, error) {
	_, raw, err := g.getRaw(ctx, ref.RemoteID)
	return raw, err
}

// profileHistoryID is the mailbox's current history id.
func (g *gmailProvider) profileHistoryID(ctx context.Context) (string, error) {
	var out struct {
		HistoryID string `json:"historyId"`
	}
	if err := g.do(ctx, http.MethodGet, "/profile", nil, &out); err != nil {
		return "", err
	}
	return out.HistoryID, nil
}

// listIDs lists message ids newest first matching q (and a label), at most max.
func (g *gmailProvider) listIDs(ctx context.Context, q, label string, max int) ([]string, error) {
	out := []string{}
	token := ""
	for len(out) < max {
		v := url.Values{"q": {q}, "maxResults": {strconv.Itoa(min(max-len(out), 500))}, "includeSpamTrash": {"true"}}
		if label != "" {
			v.Set("labelIds", label)
		}
		if token != "" {
			v.Set("pageToken", token)
		}
		var page struct {
			Messages []struct {
				ID string `json:"id"`
			} `json:"messages"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := g.do(ctx, http.MethodGet, "/messages?"+v.Encode(), nil, &page); err != nil {
			return nil, err
		}
		for _, m := range page.Messages {
			out = append(out, m.ID)
		}
		if page.NextPageToken == "" || len(page.Messages) == 0 {
			break
		}
		token = page.NextPageToken
	}
	return out, nil
}

// gmailChange is what history says about one message: its current labels, or deleted.
type gmailChange struct {
	ID       string
	ThreadID string
	LabelIDs []string
	Labels   bool // LabelIDs is known
	Added    bool
	Deleted  bool
}

// errHistoryGone means the stored history id is older than Gmail keeps: sync from scratch.
var errHistoryGone = &providerError{msg: "Gmail no longer has the changes since the last sync.", notFound: true}

// history reads the changes since a history id, in order, and the newest history id.
func (g *gmailProvider) history(ctx context.Context, since string) ([]gmailChange, string, error) {
	changes := []gmailChange{}
	index := map[string]int{}
	latest := since
	token := ""
	for pages := 0; pages < 50; pages++ {
		v := url.Values{"startHistoryId": {since}, "maxResults": {"500"},
			"historyTypes": {"messageAdded", "messageDeleted", "labelAdded", "labelRemoved"}}
		if token != "" {
			v.Set("pageToken", token)
		}
		var page struct {
			History []struct {
				MessagesAdded   []struct{ Message gmailMessage } `json:"messagesAdded"`
				MessagesDeleted []struct{ Message gmailMessage } `json:"messagesDeleted"`
				LabelsAdded     []struct{ Message gmailMessage } `json:"labelsAdded"`
				LabelsRemoved   []struct{ Message gmailMessage } `json:"labelsRemoved"`
			} `json:"history"`
			HistoryID     string `json:"historyId"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := g.do(ctx, http.MethodGet, "/history?"+v.Encode(), nil, &page); err != nil {
			if isNotFound(err) {
				return nil, "", errHistoryGone
			}
			return nil, "", err
		}
		note := func(m gmailMessage, added, deleted bool) {
			i, ok := index[m.ID]
			if !ok {
				i = len(changes)
				index[m.ID] = i
				changes = append(changes, gmailChange{ID: m.ID})
			}
			c := &changes[i]
			if m.ThreadID != "" {
				c.ThreadID = m.ThreadID
			}
			if m.LabelIDs != nil {
				c.LabelIDs, c.Labels = m.LabelIDs, true
			}
			c.Added = c.Added || added
			if deleted {
				c.Deleted = true
			} else if added {
				c.Deleted = false
			}
		}
		for _, h := range page.History {
			for _, x := range h.MessagesAdded {
				note(x.Message, true, false)
			}
			for _, x := range h.LabelsAdded {
				note(x.Message, false, false)
			}
			for _, x := range h.LabelsRemoved {
				note(x.Message, false, false)
			}
			for _, x := range h.MessagesDeleted {
				note(x.Message, false, true)
			}
		}
		if page.HistoryID != "" {
			latest = page.HistoryID
		}
		if page.NextPageToken == "" {
			break
		}
		token = page.NextPageToken
	}
	return changes, latest, nil
}

// gmailState is a message's state from its labels.
func (p *pass) gmailState(labels []string) state {
	return state{
		Unread:    slices.Contains(labels, "UNREAD"),
		Starred:   slices.Contains(labels, "STARRED"),
		Draft:     slices.Contains(labels, "DRAFT"),
		FolderIDs: p.folderIDs(labels),
	}
}

// Apply makes an action at Gmail (contracts/collab.md "Actions").
func (g *gmailProvider) Apply(ctx context.Context, op ApplyOp) error {
	if len(op.Messages) == 0 {
		return nil
	}
	if op.Action == ActionTrash {
		threads := []string{}
		for _, m := range op.Messages {
			if m.ThreadRemoteID != "" && !slices.Contains(threads, m.ThreadRemoteID) {
				threads = append(threads, m.ThreadRemoteID)
			}
		}
		for _, t := range threads {
			if err := g.do(ctx, http.MethodPost, "/threads/"+url.PathEscape(t)+"/trash", nil, nil); err != nil && !isNotFound(err) {
				return err
			}
		}
		return nil
	}
	add, remove := gmailLabelChange(op)
	if len(add) == 0 && len(remove) == 0 {
		return nil
	}
	msgIDs := []string{}
	for _, m := range op.Messages {
		msgIDs = append(msgIDs, m.RemoteID)
	}
	for len(msgIDs) > 0 {
		n := min(len(msgIDs), 1000)
		in := map[string]any{"ids": msgIDs[:n], "addLabelIds": add, "removeLabelIds": remove}
		if err := g.do(ctx, http.MethodPost, "/messages/batchModify", in, nil); err != nil {
			return err
		}
		msgIDs = msgIDs[n:]
	}
	return nil
}

// gmailLabelChange is the labels an action adds and removes.
func gmailLabelChange(op ApplyOp) (add, remove []string) {
	add, remove = []string{}, []string{}
	switch op.Action {
	case ActionRead:
		remove = append(remove, "UNREAD")
	case ActionUnread:
		add = append(add, "UNREAD")
	case ActionStar:
		add = append(add, "STARRED")
	case ActionUnstar:
		remove = append(remove, "STARRED")
	case ActionArchive:
		remove = append(remove, "INBOX")
	case ActionInbox:
		add = append(add, "INBOX")
		remove = append(remove, "TRASH", "SPAM")
	case ActionSpam:
		add = append(add, "SPAM")
		remove = append(remove, "INBOX")
	case ActionNotSpam:
		add = append(add, "INBOX")
		remove = append(remove, "SPAM")
	case ActionMove:
		if op.Target != nil {
			add = append(add, op.Target.RemoteID)
			if op.Target.RemoteID != "INBOX" {
				remove = append(remove, "INBOX")
			}
		}
	case ActionLabel:
		if op.Target != nil {
			add = append(add, op.Target.RemoteID)
		}
	case ActionUnlabel:
		if op.Target != nil {
			remove = append(remove, op.Target.RemoteID)
		}
	}
	return add, remove
}

// Send sends through users.messages.send (multipart upload, up to 35 MB) in the
// conversation's thread; Gmail files the sent copy under SENT.
func (g *gmailProvider) Send(ctx context.Context, raw []byte, info SendInfo) (SentRef, error) {
	meta := map[string]string{}
	if info.ThreadRemoteID != "" {
		meta["threadId"] = info.ThreadRemoteID
	}
	metaJSON, _ := json.Marshal(meta)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	h := textproto.MIMEHeader{}
	h.Set("Content-Type", "application/json; charset=UTF-8")
	pw, _ := mw.CreatePart(h)
	_, _ = pw.Write(metaJSON)
	h = textproto.MIMEHeader{}
	h.Set("Content-Type", "message/rfc822")
	pw, _ = mw.CreatePart(h)
	_, _ = pw.Write(raw)
	_ = mw.Close()
	resp, err := g.s.call(ctx, g.a, "Gmail", restRequest{method: http.MethodPost,
		url:  g.s.gmailBase + "/upload/gmail/v1/users/me/messages/send?uploadType=multipart",
		body: body.Bytes(), contentType: "multipart/related; boundary=" + mw.Boundary()})
	if err != nil {
		return SentRef{}, err
	}
	var sent gmailMessage
	if err := json.Unmarshal(resp, &sent); err != nil || sent.ID == "" {
		return SentRef{}, nil
	}
	labels := sent.LabelIDs
	if len(labels) == 0 {
		labels = []string{"SENT"}
	}
	return SentRef{RemoteID: sent.ID, ThreadRemoteID: sent.ThreadID, FolderRemoteIDs: labels}, nil
}

// syncGmail runs one pass: a first sync without a history id (or when Gmail no longer
// has it), else the history since it.
func (s *service) syncGmail(ctx context.Context, p *pass, g *gmailProvider) error {
	var st struct {
		HistoryID string `json:"history_id"`
	}
	_ = json.Unmarshal(p.a.SyncState, &st)
	if st.HistoryID != "" {
		changes, latest, err := g.history(ctx, st.HistoryID)
		if err == nil {
			if err := s.applyGmailChanges(ctx, p, g, changes); err != nil {
				return err
			}
			return s.saveAccountState(ctx, p.a, map[string]any{"history_id": latest})
		}
		if err != errHistoryGone {
			return err
		}
		s.log.Info("collab gmail history expired, syncing from scratch", "account_id", p.a.ID)
	}
	// The history id is read first so nothing that changes during the import is missed.
	historyID, err := g.profileHistoryID(ctx)
	if err != nil {
		return err
	}
	q := fmt.Sprintf("after:%d", time.Now().Add(-initialWindow).Unix())
	ids := []string{}
	for _, label := range []string{"INBOX", "SENT", ""} {
		if len(ids) >= initialMax {
			break
		}
		list, err := g.listIDs(ctx, q, label, initialMax)
		if err != nil {
			return err
		}
		for _, id := range list {
			if len(ids) < initialMax && !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	changes := make([]gmailChange, 0, len(ids))
	for _, id := range ids {
		changes = append(changes, gmailChange{ID: id, Added: true})
	}
	if err := s.applyGmailChanges(ctx, p, g, changes); err != nil {
		return err
	}
	return s.saveAccountState(ctx, p.a, map[string]any{"history_id": historyID})
}

// applyGmailChanges stores what changed: known messages get their labels (read again
// when history did not carry them), new ones are fetched and stored, deleted ones removed.
func (s *service) applyGmailChanges(ctx context.Context, p *pass, g *gmailProvider, changes []gmailChange) error {
	// At most initialMax new messages per pass, the newest; older ones in a larger burst
	// (old mail moved back to the inbox in bulk) are left out.
	all := make([]string, 0, len(changes))
	for _, c := range changes {
		all = append(all, c.ID)
	}
	stored, err := p.lookupMany(ctx, s.d.DB.Pool, all)
	if err != nil {
		return err
	}
	skip := map[string]bool{}
	fresh := 0
	for i := len(changes) - 1; i >= 0; i-- {
		if c := changes[i]; !c.Deleted && stored[c.ID] == nil {
			if fresh++; fresh > initialMax {
				skip[c.ID] = true
			}
		}
	}
	for len(changes) > 0 {
		n := min(len(changes), syncBatch)
		chunk := changes[:n]
		changes = changes[n:]
		remoteIDs := make([]string, 0, len(chunk))
		for _, c := range chunk {
			remoteIDs = append(remoteIDs, c.ID)
		}
		known, err := p.lookupMany(ctx, s.d.DB.Pool, remoteIDs)
		if err != nil {
			return err
		}
		// Network first, then one transaction for the chunk.
		fetched := map[string]*newMessage{}
		for i := range chunk {
			c := &chunk[i]
			if c.Deleted || (known[c.ID] == nil && skip[c.ID]) {
				continue
			}
			if known[c.ID] != nil && c.Labels {
				continue
			}
			if known[c.ID] != nil {
				// Known: its labels are enough.
				var m gmailMessage
				err := g.do(ctx, http.MethodGet, "/messages/"+url.PathEscape(c.ID)+"?format=minimal", nil, &m)
				if isNotFound(err) {
					c.Deleted = true
					continue
				}
				if err != nil {
					return err
				}
				c.LabelIDs, c.Labels = m.LabelIDs, true
				continue
			}
			m, raw, err := g.getRaw(ctx, c.ID)
			if isNotFound(err) {
				c.Deleted = true // gone before we read it
				continue
			}
			if err == errTooLarge {
				s.log.Info("collab skipping oversized message", "account_id", p.a.ID)
				continue
			}
			if err != nil {
				return err
			}
			c.LabelIDs, c.Labels, c.ThreadID = m.LabelIDs, true, m.ThreadID
			if known[c.ID] == nil {
				fetched[c.ID] = &newMessage{RemoteID: c.ID, ThreadRemoteID: m.ThreadID, Raw: raw, Date: m.date()}
			}
		}
		err = p.batch(ctx, func(tx pgx.Tx) error {
			for _, c := range chunk {
				m := known[c.ID]
				switch {
				case c.Deleted && m != nil:
					if err := p.remove(ctx, tx, m); err != nil {
						return err
					}
				case c.Deleted || !c.Labels:
				case m != nil:
					if err := p.update(ctx, tx, m, p.gmailState(c.LabelIDs)); err != nil {
						return err
					}
				case fetched[c.ID] != nil:
					in := fetched[c.ID]
					in.State = p.gmailState(c.LabelIDs)
					if _, err := p.insert(ctx, tx, in); err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}
