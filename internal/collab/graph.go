package collab

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Microsoft mail through Microsoft Graph (Mail.ReadWrite, Mail.Send). Every message is in
// exactly one folder; the well-known folders are the special ones. Requests ask for
// immutable ids (Prefer: IdType="ImmutableId"), so a message keeps its id when it moves
// and when a sent draft lands in Sent Items. Conversations are Graph's conversationId.
// Categories (MailboxSettings) are labels besides the folder (categories.go).

// graphWellKnown maps Graph's well-known folder names to roles.
var graphWellKnown = []struct{ name, role string }{
	{"inbox", RoleInbox}, {"sentitems", RoleSent}, {"drafts", RoleDrafts}, {"deleteditems", RoleTrash},
	{"junkemail", RoleSpam}, {"archive", RoleArchive},
}

// graphMaxRequest is Graph's request limit; a MIME body is sent base64 encoded.
const graphMaxRequest = 4 << 20

type graphProvider struct {
	s *service
	a *accountRow
}

var graphHeaders = map[string]string{"Prefer": `IdType="ImmutableId"`}

func (g *graphProvider) do(ctx context.Context, method, target string, in, out any) error {
	if !strings.HasPrefix(target, "http") {
		target = g.s.graphBase + target
	}
	return g.s.callJSON(ctx, g.a, "Microsoft", method, target, in, out, graphHeaders)
}

func (g *graphProvider) Close() {}

type graphFolder struct {
	ID               string `json:"id"`
	DisplayName      string `json:"displayName"`
	ParentFolderID   string `json:"parentFolderId"`
	ChildFolderCount int    `json:"childFolderCount"`
}

// wellKnown resolves the special folders' ids (cached in the account's sync state).
func (g *graphProvider) wellKnown(ctx context.Context) (map[string]string, error) {
	var st graphState
	_ = json.Unmarshal(g.a.SyncState, &st)
	if len(st.Roles) > 0 {
		return st.Roles, nil
	}
	roles := map[string]string{}
	for _, w := range graphWellKnown {
		var f graphFolder
		err := g.do(ctx, http.MethodGet, "/me/mailFolders/"+w.name+"?$select=id", nil, &f)
		if isNotFound(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		roles[f.ID] = w.role
	}
	st.Roles = roles
	if err := g.s.saveAccountState(ctx, g.a, st); err != nil {
		return nil, err
	}
	return roles, nil
}

// graphState is a Microsoft account's sync state: the special folders' ids.
type graphState struct {
	Roles map[string]string `json:"roles,omitempty"` // folder id → role
}

func (g *graphProvider) Folders(ctx context.Context) ([]RemoteFolder, error) {
	roles, err := g.wellKnown(ctx)
	if err != nil {
		return nil, err
	}
	out := []RemoteFolder{}
	var walk func(target, parentPath string, depth int) error
	walk = func(target, parentPath string, depth int) error {
		next := target
		for next != "" && len(out) < maxFolders {
			var page struct {
				Value    []graphFolder `json:"value"`
				NextLink string        `json:"@odata.nextLink"`
			}
			if err := g.do(ctx, http.MethodGet, next, nil, &page); err != nil {
				return err
			}
			for _, f := range page.Value {
				if len(out) >= maxFolders {
					break
				}
				path := f.DisplayName
				if parentPath != "" {
					path = parentPath + "/" + f.DisplayName
				}
				out = append(out, RemoteFolder{RemoteID: f.ID, Name: f.DisplayName, Path: path, Role: roles[f.ID], Kind: KindFolder})
				if f.ChildFolderCount > 0 && depth < 8 {
					if err := walk("/me/mailFolders/"+url.PathEscape(f.ID)+"/childFolders?$top=100&$select=id,displayName,parentFolderId,childFolderCount",
						path, depth+1); err != nil {
						return err
					}
				}
			}
			next = page.NextLink
		}
		return nil
	}
	if err := walk("/me/mailFolders?$top=100&$select=id,displayName,parentFolderId,childFolderCount", "", 0); err != nil {
		return nil, err
	}
	// Categories are the account's labels (categories.go).
	cats, err := g.categories(ctx)
	if err != nil {
		return nil, err
	}
	out = append(out, cats...)
	if len(out) > maxFolders {
		out = out[:maxFolders]
	}
	sortFolders(out)
	return out, nil
}

func (g *graphProvider) CreateFolder(ctx context.Context, name string, parent *RemoteFolder) (RemoteFolder, error) {
	target := "/me/mailFolders"
	path := name
	if parent != nil {
		target = "/me/mailFolders/" + url.PathEscape(parent.RemoteID) + "/childFolders"
		path = parent.Path + "/" + name
	}
	var f graphFolder
	if err := g.do(ctx, http.MethodPost, target, map[string]string{"displayName": name}, &f); err != nil {
		return RemoteFolder{}, err
	}
	return RemoteFolder{RemoteID: f.ID, Name: f.DisplayName, Path: path, Kind: KindFolder}, nil
}

func (g *graphProvider) RenameFolder(ctx context.Context, f RemoteFolder, name string) (RemoteFolder, error) {
	if isCategory(f.RemoteID) {
		return RemoteFolder{}, errCategoryRename
	}
	var out graphFolder
	if err := g.do(ctx, http.MethodPatch, "/me/mailFolders/"+url.PathEscape(f.RemoteID), map[string]string{"displayName": name}, &out); err != nil {
		return RemoteFolder{}, err
	}
	f.Name = out.DisplayName
	if i := strings.LastIndex(f.Path, "/"); i >= 0 {
		f.Path = f.Path[:i+1] + out.DisplayName
	} else {
		f.Path = out.DisplayName
	}
	return f, nil
}

func (g *graphProvider) DeleteFolder(ctx context.Context, f RemoteFolder) error {
	if isCategory(f.RemoteID) {
		return g.deleteCategory(ctx, f)
	}
	err := g.do(ctx, http.MethodDelete, "/me/mailFolders/"+url.PathEscape(f.RemoteID), nil, nil)
	if isNotFound(err) {
		return nil
	}
	return err
}

func (g *graphProvider) FetchRaw(ctx context.Context, ref MessageRef) ([]byte, error) {
	return g.s.call(ctx, g.a, "Microsoft", restRequest{method: http.MethodGet,
		url: g.s.graphBase + "/me/messages/" + url.PathEscape(ref.RemoteID) + "/$value", headers: graphHeaders, limit: maxRawBytes})
}

// Apply makes an action at Microsoft (contracts/collab.md "Actions").
func (g *graphProvider) Apply(ctx context.Context, op ApplyOp) error {
	var patch map[string]any
	var dest string
	switch op.Action {
	case ActionRead, ActionUnread:
		patch = map[string]any{"isRead": op.Action == ActionRead}
	case ActionStar:
		patch = map[string]any{"flag": map[string]string{"flagStatus": "flagged"}}
	case ActionUnstar:
		patch = map[string]any{"flag": map[string]string{"flagStatus": "notFlagged"}}
	case ActionLabel, ActionUnlabel:
		return g.applyCategories(ctx, op)
	default:
		target, err := moveTarget(op)
		if err != nil {
			return err
		}
		dest = target.RemoteID
	}
	for _, m := range op.Messages {
		path := "/me/messages/" + url.PathEscape(m.RemoteID)
		var err error
		if patch != nil {
			err = g.do(ctx, http.MethodPatch, path, patch, nil)
		} else {
			err = g.do(ctx, http.MethodPost, path+"/move", map[string]string{"destinationId": dest}, nil)
		}
		if err != nil && !isNotFound(err) {
			return err
		}
	}
	return nil
}

// moveTarget is the folder a moving action takes messages to.
func moveTarget(op ApplyOp) (RemoteFolder, error) {
	role := ""
	switch op.Action {
	case ActionArchive:
		role = RoleArchive
	case ActionInbox, ActionNotSpam:
		role = RoleInbox
	case ActionTrash:
		role = RoleTrash
	case ActionSpam:
		role = RoleSpam
	case ActionMove:
		if op.Target == nil {
			return RemoteFolder{}, &providerError{msg: "Choose a folder to move to.", permanent: true}
		}
		return *op.Target, nil
	default:
		return RemoteFolder{}, &providerError{msg: "This action is not supported.", permanent: true}
	}
	f, ok := op.Special[role]
	if !ok {
		return RemoteFolder{}, &providerError{msg: "This account has no " + role + " folder.", permanent: true}
	}
	return f, nil
}

// Send creates the message from its MIME source as a draft, then sends it: the sent
// copy keeps the draft's (immutable) id in Sent Items.
func (g *graphProvider) Send(ctx context.Context, raw []byte, info SendInfo) (SentRef, error) {
	encoded := base64.StdEncoding.EncodeToString(raw)
	if len(encoded) > graphMaxRequest {
		return SentRef{}, &providerError{msg: "Microsoft accepts messages up to about 3 MB (attachments included).", permanent: true}
	}
	body, err := g.s.call(ctx, g.a, "Microsoft", restRequest{method: http.MethodPost, url: g.s.graphBase + "/me/messages",
		body: []byte(encoded), contentType: "text/plain", headers: graphHeaders})
	if err != nil {
		return SentRef{}, err
	}
	var draft struct {
		ID             string `json:"id"`
		ConversationID string `json:"conversationId"`
	}
	if err := json.Unmarshal(body, &draft); err != nil || draft.ID == "" {
		return SentRef{}, &providerError{msg: "Microsoft did not accept the message."}
	}
	if err := g.do(ctx, http.MethodPost, "/me/messages/"+url.PathEscape(draft.ID)+"/send", nil, nil); err != nil {
		// Do not leave the unsent draft behind.
		_ = g.do(context.WithoutCancel(ctx), http.MethodDelete, "/me/messages/"+url.PathEscape(draft.ID), nil, nil)
		return SentRef{}, err
	}
	ref := SentRef{RemoteID: draft.ID, ThreadRemoteID: draft.ConversationID}
	roles, err := g.wellKnown(ctx)
	if err == nil {
		for id, role := range roles {
			if role == RoleSent {
				ref.FolderRemoteIDs = []string{id}
			}
		}
	}
	return ref, nil
}

// graphMessage is a message as a delta page lists it.
type graphMessage struct {
	ID               string    `json:"id"`
	ConversationID   string    `json:"conversationId"`
	IsRead           bool      `json:"isRead"`
	IsDraft          bool      `json:"isDraft"`
	ParentFolderID   string    `json:"parentFolderId"`
	ReceivedDateTime time.Time `json:"receivedDateTime"`
	Categories       []string  `json:"categories"`
	Flag             struct {
		FlagStatus string `json:"flagStatus"`
	} `json:"flag"`
	Removed *struct {
		Reason string `json:"reason"`
	} `json:"@removed"`
}

const graphDeltaSelect = "id,conversationId,isRead,isDraft,parentFolderId,receivedDateTime,flag,categories"

// folderDeltaState is a folder's place in Graph's delta: the delta link of its last
// complete round, or the next page of the round in progress (Initial: the round started
// from nothing, with the 30-day window).
type folderDeltaState struct {
	Delta   string `json:"delta,omitempty"`
	Next    string `json:"next,omitempty"`
	Initial bool   `json:"initial,omitempty"`
}

// graphPagesPerPass bounds the delta pages one pass reads of a folder; a longer round
// goes on from its saved next page in the next pass.
const graphPagesPerPass = 200

// syncGraph runs one pass: per folder (special ones first), the changes since its delta
// link; a folder without one starts with the last 30 days. Every page is stored with the
// folder's place, so an interrupted round goes on where it stopped.
func (s *service) syncGraph(ctx context.Context, p *pass, g *graphProvider) error {
	folders := make([]*folderRow, 0, len(p.folders))
	for _, f := range p.folders {
		folders = append(folders, f)
	}
	slices.SortFunc(folders, func(a, b *folderRow) int { return compareFolders(a.remote(), b.remote()) })
	first := p.a.LastSyncedAt == nil
	budget := initialMax
	for _, f := range folders {
		if err := ctx.Err(); err != nil {
			return err
		}
		if f.Kind == KindLabel {
			continue // a category, not a mail folder
		}
		var st folderDeltaState
		_ = json.Unmarshal(f.SyncState, &st)
		target := st.Next
		switch {
		case target != "":
		case st.Delta != "":
			target, st.Initial = st.Delta, false
		default:
			since := time.Now().Add(-initialWindow).UTC().Format(time.RFC3339)
			target = s.graphBase + "/me/mailFolders/" + url.PathEscape(f.RemoteID) + "/messages/delta?$select=" + graphDeltaSelect +
				"&$filter=" + url.QueryEscape("receivedDateTime ge "+since)
			st.Initial = true
		}
		// The first sync imports at most initialMax messages; any other round at most
		// that many per folder and pass (the rest comes with later changes).
		limit := initialMax
		if st.Initial && first {
			limit = budget
		}
		used, err := s.graphFolderDelta(ctx, p, g, f, target, st.Initial, limit)
		if isNotFound(err) || isSyncStateGone(err) {
			// The folder is gone or its delta link expired: start it over next pass.
			if _, err := s.d.DB.Pool.Exec(ctx, `UPDATE collab_folders SET sync_state = '{}' WHERE id = $1`, f.ID); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if st.Initial && first {
			budget = max(budget-used, 0)
		}
	}
	return p.batch(ctx, func(tx pgx.Tx) error { return p.removeOrphans(ctx, tx) })
}

// isSyncStateGone reports Graph refusing an expired delta link.
func isSyncStateGone(err error) bool {
	var pe *providerError
	if !errors.As(err, &pe) {
		return false
	}
	return pe.status == http.StatusGone || strings.Contains(pe.msg, "SyncStateNotFound") || strings.Contains(pe.msg, "resyncRequired")
}

// graphFolderDelta reads a folder's delta pages from target and stores each with the
// folder's new place; limit bounds the new messages imported. It returns how many it
// imported.
func (s *service) graphFolderDelta(ctx context.Context, p *pass, g *graphProvider, f *folderRow, target string, initial bool, limit int) (int, error) {
	imported := 0
	for pages := 0; pages < graphPagesPerPass && target != ""; pages++ {
		var page struct {
			Value     []graphMessage `json:"value"`
			NextLink  string         `json:"@odata.nextLink"`
			DeltaLink string         `json:"@odata.deltaLink"`
		}
		err := s.callJSON(ctx, p.a, "Microsoft", http.MethodGet, target, nil, &page,
			map[string]string{"Prefer": `IdType="ImmutableId", odata.maxpagesize=50`})
		if err != nil {
			return imported, err
		}
		next := folderDeltaState{Next: page.NextLink, Initial: initial && page.NextLink != ""}
		if page.NextLink == "" {
			next = folderDeltaState{Delta: page.DeltaLink}
		}
		n, err := s.applyGraphPage(ctx, p, g, f, page.Value, limit-imported, next)
		if err != nil {
			return imported, err
		}
		imported += n
		if page.NextLink == "" {
			return imported, nil
		}
		target = page.NextLink
	}
	return imported, nil
}

// applyGraphPage stores one delta page of a folder.
func (s *service) applyGraphPage(ctx context.Context, p *pass, g *graphProvider, f *folderRow, list []graphMessage, left int, place folderDeltaState) (int, error) {
	remoteIDs := make([]string, 0, len(list))
	for _, m := range list {
		remoteIDs = append(remoteIDs, m.ID)
	}
	known, err := p.lookupMany(ctx, s.d.DB.Pool, remoteIDs)
	if err != nil {
		return 0, err
	}
	fetched := map[string]*newMessage{}
	for _, m := range list {
		if m.Removed != nil || known[m.ID] != nil || len(fetched) >= left {
			continue
		}
		raw, err := g.FetchRaw(ctx, MessageRef{RemoteID: m.ID})
		if isNotFound(err) {
			continue
		}
		if err == errTooLarge {
			s.log.Info("collab skipping oversized message", "account_id", p.a.ID)
			continue
		}
		if err != nil {
			return 0, err
		}
		fetched[m.ID] = &newMessage{RemoteID: m.ID, ThreadRemoteID: m.ConversationID, Raw: raw, Date: m.ReceivedDateTime}
	}
	err = p.batch(ctx, func(tx pgx.Tx) error {
		for _, m := range list {
			stored := known[m.ID]
			if m.Removed != nil {
				// Moved out of this folder or deleted: it leaves this folder; a message in no
				// folder at the end of the pass is removed.
				if stored != nil && slices.Contains(stored.FolderIDs, f.ID) {
					st := state{Unread: stored.Unread, Starred: stored.Starred, Draft: stored.Draft,
						FolderIDs: slices.DeleteFunc(slices.Clone(stored.FolderIDs), func(id uuid.UUID) bool { return id == f.ID })}
					if err := p.update(ctx, tx, stored, st); err != nil {
						return err
					}
				}
				continue
			}
			folder := f.ID
			if pf, ok := p.folders[m.ParentFolderID]; ok {
				folder = pf.ID
			}
			st := state{Unread: !m.IsRead, Starred: m.Flag.FlagStatus == "flagged", Draft: m.IsDraft, FolderIDs: []uuid.UUID{folder}}
			cats, err := p.categoryIDs(ctx, tx, m.Categories)
			if err != nil {
				return err
			}
			st.FolderIDs = append(st.FolderIDs, cats...)
			if stored != nil {
				if err := p.update(ctx, tx, stored, st); err != nil {
					return err
				}
				continue
			}
			if in := fetched[m.ID]; in != nil {
				in.State = st
				if _, err := p.insert(ctx, tx, in); err != nil {
					return err
				}
			}
		}
		b, _ := json.Marshal(place)
		_, err := tx.Exec(ctx, `UPDATE collab_folders SET sync_state = $2 WHERE id = $1`, f.ID, b)
		return err
	})
	return len(fetched), err
}
