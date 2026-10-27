package collab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The provider steps of a history import (contracts/collab.md "History"). Each reads pages
// of older mail from its saved place, skips what is already stored, and stores every page
// with its new place (historyRun.store).

// historyFolders are the account's folders a history import reads, in the sync's order:
// mail folders (not labels), spam and trash only when asked for.
func (r *historyRun) historyFolders() []*folderRow {
	out := []*folderRow{}
	for _, f := range r.p.folders {
		if f.Kind == KindLabel {
			continue
		}
		if f.Role != nil && (*f.Role == RoleSpam || *f.Role == RoleTrash) && !r.h.IncludeSpamTrash {
			continue
		}
		out = append(out, f)
	}
	slices.SortFunc(out, func(a, b *folderRow) int { return compareFolders(a.remote(), b.remote()) })
	return out
}

// inScope reports whether mail received at t is in the import's scope.
func (r *historyRun) inScope(t time.Time) bool {
	return r.h.Since == nil || t.IsZero() || !t.Before(*r.h.Since)
}

// Gmail: messages.list, newest first.

// gmailHistoryCursor is a Gmail import's place: the next page of its listing and, should
// Gmail refuse that page's token, the date to list again from (before it, unix seconds).
type gmailHistoryCursor struct {
	PageToken string `json:"page_token,omitempty"`
	Before    int64  `json:"before,omitempty"`
	Started   bool   `json:"started,omitempty"`
}

// gmailQuery is the import's search: its scope, never chats.
func (r *historyRun) gmailQuery(before int64) string {
	q := []string{"-in:chats"}
	if r.h.Since != nil {
		q = append(q, fmt.Sprintf("after:%d", r.h.Since.Unix()))
	}
	if before > 0 {
		q = append(q, fmt.Sprintf("before:%d", before))
	}
	return strings.Join(q, " ")
}

// listPage lists one page of message ids newest first matching q, and Gmail's estimate
// of how many match.
func (g *gmailProvider) listPage(ctx context.Context, q string, spamTrash bool, token string, n int) ([]string, string, int, error) {
	v := url.Values{"q": {q}, "maxResults": {strconv.Itoa(n)}, "includeSpamTrash": {strconv.FormatBool(spamTrash)}}
	if token != "" {
		v.Set("pageToken", token)
	}
	var page struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
		NextPageToken      string `json:"nextPageToken"`
		ResultSizeEstimate int    `json:"resultSizeEstimate"`
	}
	if err := g.do(ctx, http.MethodGet, "/messages?"+v.Encode(), nil, &page); err != nil {
		return nil, "", 0, err
	}
	ids := make([]string, 0, len(page.Messages))
	for _, m := range page.Messages {
		ids = append(ids, m.ID)
	}
	return ids, page.NextPageToken, page.ResultSizeEstimate, nil
}

// messagesTotal is how many messages the mailbox holds.
func (g *gmailProvider) messagesTotal(ctx context.Context) (int, error) {
	var out struct {
		MessagesTotal int `json:"messagesTotal"`
	}
	err := g.do(ctx, http.MethodGet, "/profile", nil, &out)
	return out.MessagesTotal, err
}

func (s *service) gmailHistory(ctx context.Context, r *historyRun, g *gmailProvider) error {
	var cur gmailHistoryCursor
	_ = json.Unmarshal(r.h.Cursor, &cur)
	for r.more(ctx) {
		var pc pageCounts
		if !cur.Started && r.h.Since == nil {
			n, err := g.messagesTotal(ctx)
			if err != nil {
				return err
			}
			pc.total = &n
		}
		ids, next, estimate, err := g.listPage(ctx, r.gmailQuery(cur.Before), r.h.IncludeSpamTrash, cur.PageToken, historyPage)
		if err != nil && cur.PageToken != "" && gmailRefused(err) {
			// Gmail refused the saved page: list again from the oldest mail reached.
			s.log.Info("collab gmail history page refused, listing again from the date reached", "account_id", r.p.a.ID)
			cur.PageToken = ""
			if r.h.Reached != nil {
				cur.Before = r.h.Reached.Unix() + 1
			}
			continue
		}
		if err != nil {
			return err
		}
		if !cur.Started && pc.total == nil {
			pc.total = &estimate
		}
		known, err := r.p.lookupMany(ctx, s.d.DB.Pool, ids)
		if err != nil {
			return err
		}
		todo := []string{}
		for _, id := range ids {
			if m := known[id]; m != nil {
				pc.present++
				pc.saw(m.SentAt)
				continue
			}
			todo = append(todo, id)
		}
		type got struct {
			m   *gmailMessage
			raw []byte
		}
		res, errs := fetchEach(ctx, todo, func() *gmailProvider { return &gmailProvider{s: s, a: copyAccount(g.a)} },
			func(ctx context.Context, gp *gmailProvider, id string) (got, error) {
				m, raw, err := gp.getRaw(ctx, id)
				return got{m, raw}, err
			})
		fetched := []*newMessage{}
		for i, err := range errs {
			switch {
			case err == nil:
				m := res[i].m
				fetched = append(fetched, &newMessage{RemoteID: todo[i], ThreadRemoteID: m.ThreadID, Raw: res[i].raw, Date: m.date(),
					State: r.p.gmailState(m.LabelIDs), Signals: gmailSignals(m.LabelIDs)})
			case isNotFound(err):
				// Deleted since it was listed.
			case err == errTooLarge:
				s.log.Info("collab skipping oversized message", "account_id", r.p.a.ID)
				pc.tooLarge++
			default:
				return err
			}
		}
		cur.Started, cur.PageToken = true, next
		err = r.store(ctx, pc, cur, func(tx pgx.Tx) error {
			for _, in := range fetched {
				if _, err := r.p.insert(ctx, tx, in); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		if next == "" {
			r.done = true
		}
	}
	return nil
}

// Microsoft: a delta round of each folder's own, whose link then becomes the folder's.

// graphHistoryCursor is a Microsoft import's place: per folder (by remote id) the next
// page of its round, or that the round is over.
type graphHistoryCursor struct {
	Folders map[string]*graphHistoryFolder `json:"folders,omitempty"`
	Counted bool                           `json:"counted,omitempty"`
}

type graphHistoryFolder struct {
	Next string `json:"next,omitempty"`
	Done bool   `json:"done,omitempty"`
}

// graphHistoryStart is the first page of a folder's history round. Its window reaches at
// least as far back as the folder's live round (the connect, less the first sync's 30
// days), so its link can take that round's place; mail before since is listed and left out.
func (s *service) graphHistoryStart(r *historyRun, f *folderRow) string {
	target := s.graphBase + "/me/mailFolders/" + url.PathEscape(f.RemoteID) + "/messages/delta?$select=" + graphDeltaSelect
	if r.h.Since == nil {
		return target
	}
	from := *r.h.Since
	if live := r.p.a.CreatedAt.Add(-initialWindow - graphClockSlack); live.Before(from) {
		from = live
	}
	return target + "&$filter=" + url.QueryEscape("receivedDateTime ge "+from.UTC().Format(time.RFC3339))
}

func (s *service) graphHistory(ctx context.Context, r *historyRun, g *graphProvider) error {
	var cur graphHistoryCursor
	_ = json.Unmarshal(r.h.Cursor, &cur)
	if cur.Folders == nil {
		cur.Folders = map[string]*graphHistoryFolder{}
	}
	folders := r.historyFolders()
	var first pageCounts // what the first page stored also says
	if !cur.Counted {
		cur.Counted = true
		if r.h.Since == nil {
			total := 0
			for _, f := range folders {
				var out struct {
					TotalItemCount int `json:"totalItemCount"`
				}
				err := g.do(ctx, http.MethodGet, "/me/mailFolders/"+url.PathEscape(f.RemoteID)+"?$select=totalItemCount", nil, &out)
				if isNotFound(err) {
					continue
				}
				if err != nil {
					return err
				}
				total += out.TotalItemCount
			}
			first.total = &total
		}
	}
	for _, f := range folders {
		fs := cur.Folders[f.RemoteID]
		if fs == nil {
			fs = &graphHistoryFolder{}
			cur.Folders[f.RemoteID] = fs
		}
		for !fs.Done {
			if !r.more(ctx) {
				return nil
			}
			target := fs.Next
			if target == "" {
				target = s.graphHistoryStart(r, f)
			}
			var page graphDeltaPage
			err := s.callJSON(ctx, r.p.a, "Microsoft", http.MethodGet, target, nil, &page,
				map[string]string{"Prefer": `IdType="ImmutableId", odata.maxpagesize=` + strconv.Itoa(historyPage)})
			pc := first
			first = pageCounts{}
			switch {
			case isNotFound(err):
				fs.Done = true // the folder is gone
				if err := r.store(ctx, pc, cur, nil); err != nil {
					return err
				}
				continue
			case isSyncStateGone(err) && fs.Next != "":
				fs.Next = "" // the round expired: start it over
				if err := r.store(ctx, pc, cur, nil); err != nil {
					return err
				}
				continue
			case err != nil:
				return err
			}
			list := slices.DeleteFunc(page.Value, func(m graphMessage) bool { return m.Removed != nil })
			ids := make([]string, 0, len(list))
			for _, m := range list {
				ids = append(ids, m.ID)
			}
			known, err := r.p.lookupMany(ctx, s.d.DB.Pool, ids)
			if err != nil {
				return err
			}
			todo := []string{}
			byID := map[string]graphMessage{}
			for _, m := range list {
				if k := known[m.ID]; k != nil {
					pc.present++
					pc.saw(k.SentAt)
					continue
				}
				if !r.inScope(m.ReceivedDateTime) {
					continue
				}
				todo = append(todo, m.ID)
				byID[m.ID] = m
			}
			raws, errs := fetchEach(ctx, todo, func() *graphProvider { return &graphProvider{s: s, a: copyAccount(g.a)} },
				func(ctx context.Context, gp *graphProvider, id string) ([]byte, error) {
					return gp.FetchRaw(ctx, MessageRef{RemoteID: id})
				})
			fetched := []*newMessage{}
			cats := map[string][]string{}
			for i, err := range errs {
				switch {
				case err == nil:
					m := byID[todo[i]]
					folder := f.ID
					if pf, ok := r.p.folders[m.ParentFolderID]; ok {
						folder = pf.ID
					}
					fetched = append(fetched, &newMessage{RemoteID: m.ID, ThreadRemoteID: m.ConversationID, Raw: raws[i], Date: m.ReceivedDateTime,
						State: state{Unread: !m.IsRead, Starred: m.Flag.FlagStatus == "flagged", Draft: m.IsDraft, FolderIDs: []uuid.UUID{folder}}})
					cats[m.ID] = m.Categories
				case isNotFound(err):
				case err == errTooLarge:
					s.log.Info("collab skipping oversized message", "account_id", r.p.a.ID)
					pc.tooLarge++
				default:
					return err
				}
			}
			delta := ""
			if page.NextLink == "" {
				fs.Next, fs.Done, delta = "", true, page.DeltaLink
			} else {
				fs.Next = page.NextLink
			}
			err = r.store(ctx, pc, cur, func(tx pgx.Tx) error {
				for _, in := range fetched {
					ids, err := r.p.categoryIDs(ctx, tx, cats[in.RemoteID])
					if err != nil {
						return err
					}
					in.State.FolderIDs = append(in.State.FolderIDs, ids...)
					if _, err := r.p.insert(ctx, tx, in); err != nil {
						return err
					}
				}
				if delta == "" {
					return nil
				}
				// The round read the whole folder: its link is the folder's from now on, so
				// changes to older mail arrive with the sync.
				b, _ := json.Marshal(folderDeltaState{Delta: delta})
				_, err := tx.Exec(ctx, `UPDATE collab_folders SET sync_state = $2 WHERE id = $1`, f.ID, b)
				return err
			})
			if err != nil {
				return err
			}
		}
	}
	r.done = true
	return nil
}

// IMAP: per synced folder, the UIDs below what the sync imported, newest first.

// imapHistoryCursor is an IMAP import's place: per folder (by mailbox name) its
// UIDVALIDITY and the UID the UIDs left are below, or that the folder is done.
type imapHistoryCursor struct {
	Folders map[string]*imapHistoryFolder `json:"folders,omitempty"`
	Counted bool                          `json:"counted,omitempty"`
}

type imapHistoryFolder struct {
	Validity uint32 `json:"v,omitempty"`
	Below    uint32 `json:"b,omitempty"` // 0: not started
	Done     bool   `json:"d,omitempty"`
}

// imapVirtual reports a folder the IMAP sync never reads (Gmail's All, Flagged, Important).
func imapVirtual(f *folderRow) bool {
	return f.Role != nil && (*f.Role == RoleAll || *f.Role == RoleStarred || *f.Role == RoleImportant)
}

// imapHistoryFolders are the folders the sync reads (syncIMAP: the first maxIMAPFolders,
// virtual ones left out), less spam and trash unless asked for.
func (r *historyRun) imapHistoryFolders() []*folderRow {
	all := []*folderRow{}
	for _, f := range r.p.folders {
		if !imapVirtual(f) {
			all = append(all, f)
		}
	}
	slices.SortFunc(all, func(a, b *folderRow) int { return compareFolders(a.remote(), b.remote()) })
	all = all[:min(len(all), maxIMAPFolders)]
	return slices.DeleteFunc(all, func(f *folderRow) bool {
		return f.Role != nil && (*f.Role == RoleSpam || *f.Role == RoleTrash) && !r.h.IncludeSpamTrash
	})
}

// imapHistoryUIDs searches the selected folder for the UIDs in scope at or below upper,
// in ascending order.
func (r *historyRun) imapHistoryUIDs(c *imapClientConn, upper uint32) ([]uint32, error) {
	if upper == 0 {
		return nil, nil
	}
	var set imap.UIDSet
	set.AddRange(1, imap.UID(upper))
	crit := &imap.SearchCriteria{UID: []imap.UIDSet{set}}
	if r.h.Since != nil {
		crit.Since = *r.h.Since
	}
	data, err := c.UIDSearch(crit, nil).Wait()
	if err != nil {
		return nil, opError("searching a folder", err)
	}
	out := []uint32{}
	for _, u := range data.AllUIDs() {
		if uint32(u) <= upper {
			out = append(out, uint32(u))
		}
	}
	slices.Sort(out)
	return out, nil
}

func (s *service) imapHistory(ctx context.Context, r *historyRun, ip *imapProvider) error {
	c, err := ip.session(ctx)
	if err != nil {
		return err
	}
	// The sync just moved the folders' cursors: read them again.
	if err := r.p.reloadFolders(ctx, s.d.DB.Pool); err != nil {
		return err
	}
	var cur imapHistoryCursor
	_ = json.Unmarshal(r.h.Cursor, &cur)
	if cur.Folders == nil {
		cur.Folders = map[string]*imapHistoryFolder{}
	}
	folders := r.imapHistoryFolders()
	var first pageCounts
	if !cur.Counted {
		cur.Counted = true
		total := 0
		for _, f := range folders {
			var live imapFolderState
			_ = json.Unmarshal(f.SyncState, &live)
			if _, err := c.Select(f.RemoteID, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
				if isNonExistent(err) {
					continue
				}
				return opError("opening a folder", err)
			}
			uids, err := r.imapHistoryUIDs(c, live.LastUID)
			if err != nil {
				return err
			}
			known, err := r.p.imapKnown(ctx, f.RemoteID)
			if err != nil {
				return err
			}
			total += len(slices.DeleteFunc(uids, func(u uint32) bool { return known[u] != nil }))
		}
		first.total = &total
	}
	for _, f := range folders {
		fs := cur.Folders[f.RemoteID]
		if fs == nil {
			fs = &imapHistoryFolder{}
			cur.Folders[f.RemoteID] = fs
		}
		if fs.Done {
			continue
		}
		if !r.more(ctx) {
			return nil
		}
		sel, err := c.Select(f.RemoteID, &imap.SelectOptions{ReadOnly: true}).Wait()
		if err != nil {
			if !isNonExistent(err) {
				return opError("opening a folder", err)
			}
			fs.Done = true // gone since the folder list
			if err := r.store(ctx, first, cur, nil); err != nil {
				return err
			}
			first = pageCounts{}
			continue
		}
		var live imapFolderState
		_ = json.Unmarshal(f.SyncState, &live)
		if live.Validity == 0 || live.Validity != sel.UIDValidity {
			return nil // the sync starts this folder over first; the import reads it after
		}
		if fs.Validity != sel.UIDValidity {
			*fs = imapHistoryFolder{Validity: sel.UIDValidity}
		}
		upper := live.LastUID
		if fs.Below > 0 {
			upper = min(upper, fs.Below-1)
		}
		uids, err := r.imapHistoryUIDs(c, upper)
		if err != nil {
			return err
		}
		known, err := r.p.imapKnown(ctx, f.RemoteID)
		if err != nil {
			return err
		}
		uids = slices.DeleteFunc(uids, func(u uint32) bool { return known[u] != nil })
		if len(uids) == 0 {
			fs.Done = true
			if err := r.store(ctx, first, cur, nil); err != nil {
				return err
			}
			first = pageCounts{}
			continue
		}
		for len(uids) > 0 {
			if !r.more(ctx) {
				return nil
			}
			chunk := uids[max(len(uids)-historyPage, 0):] // the newest left
			uids = uids[:len(uids)-len(chunk)]
			ch, err := s.imapFetch(ctx, r.p, c, chunk)
			if err != nil {
				return err
			}
			pc := first
			first = pageCounts{}
			pc.present += ch.present
			pc.tooLarge += ch.tooLarge
			for _, m := range ch.metas {
				pc.saw(m.date)
			}
			fs.Below = chunk[0]
			fs.Done = len(uids) == 0
			err = r.store(ctx, pc, cur, func(tx pgx.Tx) error { return r.p.imapStore(ctx, tx, f, sel.UIDValidity, ch) })
			if err != nil {
				return err
			}
		}
	}
	r.done = true
	return nil
}
