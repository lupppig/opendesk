package collab

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/google/uuid"
)

// Drafts per provider (contracts/collab.md "Drafts"), and a draft made in Gmail synced
// into the Drafts box with its reply target.

// fakeGmailDrafts is the drafts API of fakeGmail (and its label colours).
type fakeGmailDrafts struct {
	byID    map[string]string // draft id → message id
	next    int
	saves   []string // "POST" or "PUT <id>", then the metadata JSON
	raws    []string // the raw message of each save
	sent    []string // draft ids sent
	deleted []string // draft ids deleted
	colors  []string // "label:bg/text"
}

// recordLocked adds a history record; the caller holds g.mu.
func (g *fakeGmail) recordLocked(kind, id string) {
	g.history++
	msg := map[string]any{"id": id}
	if m, ok := g.msgs[id]; ok {
		msg["threadId"], msg["labelIds"] = m.thread, m.labels
	}
	g.changes = append(g.changes, map[string]any{"id": strconv.Itoa(g.history), kind: []map[string]any{{"message": msg}}})
}

// gmailAllowed are the backgrounds Gmail accepts in this fake (its palette).
var gmailAllowed = []string{"#999999", "#fb4c2f", "#ffad47", "#fad165", "#16a766", "#2da2bb", "#4a86e8", "#285bac", "#a479e2", "#f691b3"}

func (g *fakeGmail) serveDrafts(w http.ResponseWriter, r *http.Request, path string) {
	d := &g.drafts
	if d.byID == nil {
		d.byID = map[string]string{}
	}
	reply := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	draftOut := func(id string) map[string]any {
		m := g.msgs[d.byID[id]]
		return map[string]any{"id": id, "message": map[string]any{"id": m.id, "threadId": m.thread, "labelIds": m.labels}}
	}
	switch {
	case strings.HasPrefix(path, "/upload/gmail/v1/users/me/drafts"):
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || r.URL.Query().Get("uploadType") != "multipart" {
			g.t.Errorf("draft upload: %v %s", err, r.URL.RawQuery)
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		var metaJSON, raw []byte
		for i := 0; ; i++ {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			b, _ := io.ReadAll(part)
			if i == 0 {
				metaJSON = b
			} else {
				raw = b
			}
		}
		var meta struct {
			ID      string            `json:"id"`
			Message map[string]string `json:"message"`
		}
		_ = json.Unmarshal(metaJSON, &meta)
		id := strings.TrimPrefix(strings.TrimPrefix(path, "/upload/gmail/v1/users/me/drafts"), "/")
		kind := "POST"
		if r.Method == "PUT" {
			kind = "PUT " + id
			old, ok := d.byID[id]
			if !ok {
				w.WriteHeader(404)
				return
			}
			g.recordLocked("messagesDeleted", old)
			delete(g.msgs, old)
		} else {
			d.next++
			id = "r" + strconv.Itoa(d.next)
		}
		d.saves = append(d.saves, kind+" "+string(metaJSON))
		d.raws = append(d.raws, string(raw))
		d.next++
		mid := "dm" + strconv.Itoa(d.next)
		thread := meta.Message["threadId"]
		if thread == "" {
			thread = "dt" + strconv.Itoa(d.next)
		}
		g.msgs[mid] = &gmailFakeMsg{id: mid, thread: thread, labels: []string{"DRAFT"}, raw: raw, date: time.Now()}
		g.order = append(g.order, mid)
		d.byID[id] = mid
		g.recordLocked("messagesAdded", mid)
		reply(draftOut(id))
	case r.Method == "GET" && path == "/drafts":
		list := []map[string]any{}
		for id := range d.byID {
			list = append(list, draftOut(id))
		}
		reply(map[string]any{"drafts": list})
	case r.Method == "DELETE" && strings.HasPrefix(path, "/drafts/"):
		id := strings.TrimPrefix(path, "/drafts/")
		mid, ok := d.byID[id]
		if !ok {
			w.WriteHeader(404)
			return
		}
		d.deleted = append(d.deleted, id)
		delete(d.byID, id)
		g.recordLocked("messagesDeleted", mid)
		delete(g.msgs, mid)
		w.WriteHeader(204)
	case r.Method == "POST" && path == "/drafts/send":
		var in struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		mid, ok := d.byID[in.ID]
		if !ok {
			w.WriteHeader(404)
			return
		}
		d.sent = append(d.sent, in.ID)
		delete(d.byID, in.ID)
		m := g.msgs[mid]
		m.labels = []string{"SENT"}
		g.recordLocked("labelsAdded", mid)
		reply(map[string]any{"id": m.id, "threadId": m.thread, "labelIds": m.labels})
	case r.Method == "PATCH" && strings.HasPrefix(path, "/labels/"):
		id := strings.TrimPrefix(path, "/labels/")
		var in struct {
			Color gmailColor `json:"color"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if !slices.Contains(gmailAllowed, in.Color.Background) {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"code":400,"message":"Label color is not on the allowed color palette."}}`))
			return
		}
		d.colors = append(d.colors, id+":"+in.Color.Background+"/"+in.Color.Text)
		for _, l := range g.labels {
			if l["id"] == id {
				l["color"] = map[string]string{"backgroundColor": in.Color.Background, "textColor": in.Color.Text}
				reply(l)
				return
			}
		}
		w.WriteHeader(404)
	default:
		g.t.Errorf("unexpected Gmail drafts call %s %s", r.Method, r.URL.String())
		w.WriteHeader(400)
	}
}

func TestGmailDrafts(t *testing.T) {
	w := newWorld(t)
	g := newFakeGmail(t)
	w.useGmail(g)
	a := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	g.add("m1", "t1", []string{"INBOX", "UNREAD"}, testMail{from: "Frank <frank@example.com>", to: a.Email, subject: "Hi", msgID: "h1@example.com", body: "Hi"})
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	m1 := w.messageByRemote(a, "m1")
	drafts := w.folderByRemote(a, "DRAFT")
	srv := w.api(w.agent)

	// A reply draft: saved at Gmail in the thread, stored in Drafts in its conversation.
	var d MailMessage
	st := doJSON(t, "POST", srv.URL+"/v1/collab/drafts", map[string]any{"account_id": a.ID, "mode": "reply", "message_id": m1.ID,
		"to": []string{"frank@example.com"}, "subject": "", "body_html": "<p>Draft one</p><script>alert(1)</script>"}, &d)
	if st != 200 || !d.Draft || d.DraftID == nil || *d.DraftID != d.ID || d.ThreadID != m1.ThreadID || d.InReplyToID == nil ||
		*d.InReplyToID != m1.ID || !slices.Contains(d.FolderIDs, drafts.ID) || d.Subject != "Re: Hi" || d.SentBy == nil {
		t.Fatalf("create: %d %+v", st, d)
	}
	if strings.Contains(d.BodyHTML, "<script") || strings.Contains(g.drafts.raws[0], "<script") {
		t.Fatal("the draft is not sanitized")
	}
	if !strings.HasPrefix(g.drafts.saves[0], `POST {"message":{"threadId":"t1"}}`) || !strings.Contains(g.drafts.raws[0], "In-Reply-To: <h1@example.com>") {
		t.Fatalf("draft at Gmail: %v", g.drafts.saves)
	}
	first := w.messageByRemote(a, "dm2")
	if first.ID != d.ID || gmailDraftOf(first.Location) != "r1" {
		t.Fatalf("stored draft: %+v %s", first, first.Location)
	}

	// Saved again: drafts.update, a new Gmail message, the same opendesk id.
	var d2 MailMessage
	st = doJSON(t, "PUT", srv.URL+"/v1/collab/drafts/"+d.ID.String(), map[string]any{"to": []string{"frank@example.com", "Gina <gina@example.com>"},
		"subject": "Re: Hi", "body_html": "<p>Draft two</p>"}, &d2)
	if st != 200 || d2.ID != d.ID || !strings.Contains(d2.BodyHTML, "Draft two") || len(d2.To) != 2 || d2.InReplyToID == nil {
		t.Fatalf("update: %d %+v", st, d2)
	}
	if !strings.HasPrefix(g.drafts.saves[1], "PUT r1 ") || !strings.Contains(g.drafts.raws[1], "In-Reply-To: <h1@example.com>") ||
		!strings.Contains(g.drafts.raws[1], "Message-ID: <"+d.MessageID+">") {
		t.Fatalf("update at Gmail: %v\n%s", g.drafts.saves, g.drafts.raws[1])
	}
	// The sync's view of it (old message deleted, new one added) changes nothing.
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if n := w.count(`SELECT count(*) FROM collab_messages WHERE account_id = $1 AND draft`, a.ID); n != 1 {
		t.Fatalf("%d drafts after sync", n)
	}
	if n := w.count(`SELECT count(*) FROM collab_messages WHERE id = $1`, d.ID); n != 1 {
		t.Fatal("the draft lost its id")
	}
	var c CollabCounts
	if doJSON(t, "GET", srv.URL+"/v1/collab/counts", nil, &c); c.Boxes.Drafts != 1 {
		t.Fatalf("drafts count: %+v", c.Boxes)
	}

	// Sent: drafts.send, stored as sent mail in the conversation, the draft gone.
	var sent MailMessage
	st = doJSON(t, "POST", srv.URL+"/v1/collab/drafts/"+d.ID.String()+"/send", map[string]any{"to": []string{"frank@example.com"},
		"subject": "Re: Hi", "body_html": "<p>Final</p>"}, &sent)
	if st != 200 || sent.Draft || sent.DraftID != nil || sent.ThreadID != m1.ThreadID || !slices.Contains(sent.FolderIDs, w.folderByRemote(a, "SENT").ID) ||
		!strings.Contains(sent.BodyHTML, "Final") || sent.SentBy == nil || sent.SentBy.ID != w.agent.UserID {
		t.Fatalf("send: %d %+v", st, sent)
	}
	if slices.Index(g.drafts.sent, "r1") < 0 {
		t.Fatalf("not sent at Gmail: %v", g.drafts.sent)
	}
	if n := w.count(`SELECT count(*) FROM collab_messages WHERE account_id = $1 AND draft`, a.ID); n != 0 {
		t.Fatal("the sent draft is still a draft")
	}
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if n := w.count(`SELECT count(*) FROM collab_messages WHERE thread_id = $1`, m1.ThreadID); n != 2 {
		t.Fatalf("%d messages in the conversation after send and sync", n)
	}

	// New mail, then discarded: deleted at Gmail and here, with its conversation.
	var nd MailMessage
	if st := doJSON(t, "POST", srv.URL+"/v1/collab/drafts", map[string]any{"account_id": a.ID, "mode": "new", "to": []string{},
		"subject": "Plans", "body_html": "<p>Half written</p>"}, &nd); st != 200 || nd.InReplyToID != nil {
		t.Fatalf("new draft: %d %+v", st, nd)
	}
	if st := doJSON(t, "DELETE", srv.URL+"/v1/collab/drafts/"+nd.ID.String(), nil, nil); st != 204 {
		t.Fatalf("discard: %d", st)
	}
	if len(g.drafts.deleted) != 1 || w.count(`SELECT count(*) FROM collab_threads WHERE id = $1`, nd.ThreadID) != 0 {
		t.Fatalf("discard at Gmail: %v", g.drafts.deleted)
	}
	// A sent message is not a draft; sending needs a recipient; viewers and outsiders cannot.
	var e apiError
	if st := doJSON(t, "PUT", srv.URL+"/v1/collab/drafts/"+sent.ID.String(), map[string]any{"to": []string{}, "subject": "x", "body_html": "x"}, &e); st != 404 {
		t.Fatalf("update a sent message: %d", st)
	}
	var nd2 MailMessage
	doJSON(t, "POST", srv.URL+"/v1/collab/drafts", map[string]any{"account_id": a.ID, "mode": "new", "to": []string{}, "subject": "S", "body_html": "<p>x</p>"}, &nd2)
	if st := doJSON(t, "POST", srv.URL+"/v1/collab/drafts/"+nd2.ID.String()+"/send", map[string]any{"to": []string{}, "subject": "S", "body_html": "<p>x</p>"}, &e); st != 422 {
		t.Fatalf("send without recipients: %d", st)
	}
	if st := doJSON(t, "POST", w.api(w.viewer).URL+"/v1/collab/drafts", map[string]any{"account_id": a.ID, "mode": "new", "to": []string{}, "subject": "", "body_html": ""}, &e); st != 403 {
		t.Fatalf("viewer: %d", st)
	}
	if st := doJSON(t, "PUT", w.api(w.agent2).URL+"/v1/collab/drafts/"+nd2.ID.String(), map[string]any{"to": []string{}, "subject": "", "body_html": ""}, &e); st != 404 {
		t.Fatalf("someone who does not see the account: %d", st)
	}
}

func TestGmailDraftMadeInGmail(t *testing.T) {
	w := newWorld(t)
	g := newFakeGmail(t)
	w.useGmail(g)
	a := w.account(w.agent, ProviderGoogle, VisibilityPrivate, Audience{})
	g.add("m1", "t1", []string{"INBOX"}, testMail{from: "frank@example.com", to: a.Email, subject: "Logo?", msgID: "h1@example.com", body: "Logo?"})
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	// A reply drafted in Gmail, with an inline image.
	g.add("gd1", "t1", []string{"DRAFT"}, testMail{from: a.Email, to: "frank@example.com", subject: "Re: Logo?", msgID: "gd1@mail.gmail.com",
		inReplyTo: "h1@example.com", refs: "<h1@example.com>", body: "Here it is", image: true, date: time.Now()})
	g.mu.Lock()
	g.drafts.byID = map[string]string{"r-gmail": "gd1"}
	g.recordLocked("messagesAdded", "gd1")
	g.mu.Unlock()
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	srv := w.api(w.agent)
	var page struct {
		Data []MailThread `json:"data"`
	}
	if st := doJSON(t, "GET", srv.URL+"/v1/collab/threads?box=drafts", nil, &page); st != 200 || len(page.Data) != 1 {
		t.Fatalf("drafts box: %d %+v", st, page)
	}
	var detail MailThreadDetail
	doJSON(t, "GET", srv.URL+"/v1/collab/threads/"+page.Data[0].ID.String(), nil, &detail)
	if len(detail.Messages) != 2 {
		t.Fatalf("messages: %+v", detail.Messages)
	}
	m1, dr := detail.Messages[0], detail.Messages[1]
	if !dr.Draft || dr.DraftID == nil || dr.InReplyToID == nil || *dr.InReplyToID != m1.ID || len(dr.To) != 1 || dr.Subject != "Re: Logo?" ||
		len(dr.Attachments) != 1 || !strings.Contains(dr.BodyHTML, "/v1/collab/attachments/"+dr.ID.String()+"/0?") {
		t.Fatalf("draft from Gmail: %+v", dr)
	}
	old := w.messageByRemote(a, "gd1")

	// Opened in opendesk and saved again: found by its Message-ID, its image kept as a cid: part.
	var d2 MailMessage
	st := doJSON(t, "PUT", srv.URL+"/v1/collab/drafts/"+dr.ID.String(), map[string]any{"to": []string{"frank@example.com"}, "subject": "Re: Logo?",
		"body_html": `<p>Here it is, edited</p>` + dr.BodyHTML[strings.Index(dr.BodyHTML, "<img"):]}, &d2)
	if st != 200 || d2.ID != dr.ID || len(d2.Attachments) != 1 || !d2.Attachments[0].Inline {
		t.Fatalf("update: %d %+v", st, d2)
	}
	raw := g.drafts.raws[len(g.drafts.raws)-1]
	if !strings.HasPrefix(g.drafts.saves[0], "PUT r-gmail ") || !strings.Contains(raw, "\"cid:logo\"") || !strings.Contains(raw, "Content-Id: <logo>") ||
		!strings.Contains(raw, "In-Reply-To: <h1@example.com>") || !strings.Contains(raw, base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nfake-image-bytes"))[:20]) {
		t.Fatalf("saved at Gmail: %v\n%s", g.drafts.saves, raw)
	}
	fresh := w.messageByRemote(a, g.drafts.byID["r-gmail"])
	if fresh.ID != dr.ID || fresh.RawKey == "" || fresh.RawKey != old.RawKey {
		t.Fatalf("stored: %+v", fresh)
	}
	if n := w.count(`SELECT count(*) FROM collab_storage_trash WHERE key = $1`, fresh.RawKey); n != 0 {
		t.Fatal("the new version's raw source is queued for deletion")
	}
	if _, err := w.d.Storage.Get(w.ctx, fresh.RawKey); err != nil {
		t.Fatalf("raw source: %v", err)
	}
}

func TestGraphDrafts(t *testing.T) {
	w := newWorld(t)
	g := newFakeGraph(t)
	w.s.graphBase = g.srv.URL
	a := w.account(w.agent, ProviderMicrosoft, VisibilityPrivate, Audience{})
	g.add("g1", "c1", "f-inbox", false, testMail{from: "frank@example.com", to: a.Email, subject: "Order", msgID: "o1@example.com", body: "Where?"})
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	g1 := w.messageByRemote(a, "g1")
	srv := w.api(w.agent)
	var d MailMessage
	st := doJSON(t, "POST", srv.URL+"/v1/collab/drafts", map[string]any{"account_id": a.ID, "mode": "reply", "message_id": g1.ID,
		"to": []string{"frank@example.com"}, "bcc": []string{"boss@example.com"}, "subject": "Re: Order", "body_html": "<p>Soon</p>"}, &d)
	if st != 200 || d.ThreadID != g1.ThreadID || !slices.Contains(d.FolderIDs, w.folderByRemote(a, "f-drafts").ID) || len(d.Bcc) != 1 {
		t.Fatalf("create: %d %+v", st, d)
	}
	if len(g.drafts) != 1 || !strings.Contains(g.drafts[0], "In-Reply-To: <o1@example.com>") || !strings.Contains(g.drafts[0], "Bcc: <boss@example.com>") {
		t.Fatalf("draft at Microsoft: %v", g.drafts)
	}
	remote := w.messageByID(d.ID).RemoteID
	// Saved again without files: updated in place.
	var d2 MailMessage
	st = doJSON(t, "PUT", srv.URL+"/v1/collab/drafts/"+d.ID.String(), map[string]any{"to": []string{"frank@example.com", "gina@example.com"},
		"subject": "Re: Order", "body_html": "<p>Tomorrow</p>"}, &d2)
	if st != 200 || d2.ID != d.ID || len(d2.To) != 2 || w.messageByID(d.ID).RemoteID != remote || len(g.drafts) != 1 {
		t.Fatalf("update: %d %+v %v", st, d2, g.drafts)
	}
	last := g.patches[len(g.patches)-1]
	if !strings.HasPrefix(last, remote+":") || !strings.Contains(last, "Tomorrow") ||
		!strings.Contains(last, `"address":"gina@example.com"`) {
		t.Fatalf("patch: %s", last)
	}
	// Sent as saved (its source read back from Microsoft): the draft goes, the message keeps its id in Sent Items.
	g.mu.Lock()
	g.msgs[remote] = &graphFakeMsg{id: remote, conv: "c1", folder: "f-drafts", read: true, raw: []byte(g.drafts[0]), date: time.Now()}
	g.mu.Unlock()
	var sent MailMessage
	if st := doJSON(t, "POST", srv.URL+"/v1/collab/drafts/"+d.ID.String()+"/send", nil, &sent); st != 200 || sent.Draft ||
		!slices.Contains(sent.FolderIDs, w.folderByRemote(a, "f-sent").ID) || sent.ThreadID != g1.ThreadID {
		t.Fatalf("send: %d %+v", st, sent)
	}
	if len(g.sentIDs) != 1 || g.sentIDs[0] != remote || w.messageByID(sent.ID).RemoteID != remote {
		t.Fatalf("sent at Microsoft: %v", g.sentIDs)
	}
	// Discarded.
	var nd MailMessage
	doJSON(t, "POST", srv.URL+"/v1/collab/drafts", map[string]any{"account_id": a.ID, "mode": "new", "to": []string{}, "subject": "x", "body_html": "<p>x</p>"}, &nd)
	if st := doJSON(t, "DELETE", srv.URL+"/v1/collab/drafts/"+nd.ID.String(), nil, nil); st != 204 || len(g.cats.deleted) != 1 {
		t.Fatalf("discard: %d %v", st, g.cats.deleted)
	}
}

func TestIMAPDrafts(t *testing.T) {
	w := newWorld(t)
	imapAddr := startIMAP(t)
	be, smtpAddr := startSMTP(t)
	ih, ip := hostPort(t, imapAddr)
	sh, sp := hostPort(t, smtpAddr)
	a := w.account(w.agent, ProviderIMAP, VisibilityPrivate, Audience{})
	mustExec(t, w.d, `UPDATE collab_accounts SET config = $2 WHERE id = $1`, a.ID, accountConfig{IMAPHost: ih, IMAPPort: ip, IMAPTLS: "none",
		IMAPUsername: "user@acme.test", SMTPHost: sh, SMTPPort: sp, SMTPTLS: "none"})
	srvIMAP := dialServer(t, imapAddr)
	srvIMAP.create("Sent")
	srvIMAP.create("Drafts")
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	srv := w.api(w.agent)
	var d MailMessage
	st := doJSON(t, "POST", srv.URL+"/v1/collab/drafts", map[string]any{"account_id": a.ID, "mode": "new", "to": []string{"frank@example.com"},
		"bcc": []string{"boss@example.com"}, "subject": "Visit", "body_html": "<p>First version</p>"}, &d)
	if st != 200 || !slices.Contains(d.FolderIDs, w.folderByRemote(a, "Drafts").ID) {
		t.Fatalf("create: %d %+v", st, d)
	}
	uids, flags := srvIMAP.find("Drafts", d.MessageID)
	if len(uids) != 1 || !slices.Contains(flags[0], imap.FlagDraft) || !slices.Contains(flags[0], imap.FlagSeen) {
		t.Fatalf("at the server: %v %v", uids, flags)
	}
	// Saved again: the new version appended, the old one expunged.
	if st := doJSON(t, "PUT", srv.URL+"/v1/collab/drafts/"+d.ID.String(), map[string]any{"to": []string{"frank@example.com"},
		"bcc": []string{"boss@example.com"}, "subject": "Visit", "body_html": "<p>Second version</p>"}, &d); st != 200 {
		t.Fatalf("update: %d", st)
	}
	if uids, _ := srvIMAP.find("Drafts", d.MessageID); len(uids) != 1 || srvIMAP.count("Drafts") != 1 {
		t.Fatalf("versions at the server: %v", uids)
	}
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if m := w.messageByID(d.ID); !m.Draft || !strings.Contains(m.BodyText, "Second version") || len(parseLocation(m.Location)) != 1 {
		t.Fatalf("after sync: %+v %s", m, m.Location)
	}
	// Sent: SMTP without a Bcc header, a copy in Sent, Drafts empty.
	var sent MailMessage
	if st := doJSON(t, "POST", srv.URL+"/v1/collab/drafts/"+d.ID.String()+"/send", nil, &sent); st != 200 || sent.Draft {
		t.Fatalf("send: %d %+v", st, sent)
	}
	if len(be.data) != 1 || strings.Contains(be.data[0], "Bcc:") || strings.Join(be.to[0], ",") != "frank@example.com,boss@example.com" {
		t.Fatalf("smtp: %v\n%s", be.to, be.data)
	}
	if srvIMAP.count("Drafts") != 0 || srvIMAP.count("Sent") != 1 {
		t.Fatalf("drafts %d sent %d", srvIMAP.count("Drafts"), srvIMAP.count("Sent"))
	}
	if err := w.s.syncAccount(w.ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if n := w.count(`SELECT count(*) FROM collab_messages WHERE account_id = $1 AND message_id = $2`, a.ID, d.MessageID); n != 1 {
		t.Fatalf("%d copies after sync", n)
	}
	// Discarded.
	var nd MailMessage
	doJSON(t, "POST", srv.URL+"/v1/collab/drafts", map[string]any{"account_id": a.ID, "mode": "new", "to": []string{}, "subject": "x", "body_html": "<p>x</p>"}, &nd)
	if st := doJSON(t, "DELETE", srv.URL+"/v1/collab/drafts/"+nd.ID.String(), nil, nil); st != 204 || srvIMAP.count("Drafts") != 0 {
		t.Fatalf("discard: %d, %d left", st, srvIMAP.count("Drafts"))
	}
}

func TestWithoutBcc(t *testing.T) {
	raw := []byte("From: a@x.test\r\nTo: b@x.test\r\nBcc: c@x.test,\r\n d@x.test\r\nSubject: Hi\r\n\r\nBcc: in the body stays\r\n")
	got := string(withoutBcc(raw))
	if strings.Contains(got, "c@x.test") || strings.Contains(got, "d@x.test") || !strings.Contains(got, "Subject: Hi\r\n\r\nBcc: in the body stays") {
		t.Fatalf("withoutBcc: %q", got)
	}
	if !bytes.Equal(withoutBcc([]byte("no header end")), []byte("no header end")) {
		t.Fatal("a message without a header end changed")
	}
}

// messageByID loads a stored message by id.
func (w *world) messageByID(id uuid.UUID) *messageRow {
	w.t.Helper()
	m, err := scanMessage(w.d.DB.Pool.QueryRow(w.ctx, `SELECT `+messageColumns+` FROM collab_messages m WHERE m.id = $1`, id))
	if err != nil {
		w.t.Fatalf("message %s: %v", id, err)
	}
	return m
}
