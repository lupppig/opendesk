package collab

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// folderOrderSQL lists the special folders first, in a fixed order, then the rest by path.
const folderOrderSQL = `CASE f.role WHEN 'inbox' THEN 0 WHEN 'starred' THEN 1 WHEN 'important' THEN 2 WHEN 'sent' THEN 3
	WHEN 'drafts' THEN 4 WHEN 'archive' THEN 5 WHEN 'all' THEN 6 WHEN 'spam' THEN 7 WHEN 'trash' THEN 8 ELSE 9 END, lower(f.path), f.id`

// folderCountsSQL adds a folder's unread and total conversations (alias f).
const folderCountsSQL = `(SELECT count(*) FROM collab_threads t WHERE t.account_id = f.account_id AND t.folder_ids @> ARRAY[f.id] AND t.unread),
	(SELECT count(*) FROM collab_threads t WHERE t.account_id = f.account_id AND t.folder_ids @> ARRAY[f.id])`

func scanFolderOut(row pgx.Row) (MailFolder, error) {
	var f MailFolder
	if err := row.Scan(&f.ID, &f.AccountID, &f.Name, &f.Path, &f.Role, &f.Kind, &f.Color, &f.Unread, &f.Total); err != nil {
		return f, err
	}
	f.CanRename = f.Role == nil
	return f, nil
}

const folderOutColumns = `f.id, f.account_id, f.name, f.path, f.role, f.kind, f.color, ` + folderCountsSQL

func loadFolderOut(ctx context.Context, q db.Querier, wid, id uuid.UUID) (MailFolder, error) {
	return scanFolderOut(q.QueryRow(ctx, `SELECT `+folderOutColumns+` FROM collab_folders f WHERE f.workspace_id = $1 AND f.id = $2`, wid, id))
}

type folderBody struct {
	Body MailFolder
}

type folderNameBody struct {
	Name     string     `json:"name" minLength:"1" maxLength:"100"`
	ParentID *uuid.UUID `json:"parent_id,omitempty" doc:"Create it inside this folder"`
	Kind     string     `json:"kind,omitempty" enum:"folder,label" doc:"Microsoft: label makes an Outlook category. Gmail makes labels either way; IMAP only folders"`
	Color    string     `json:"color,omitempty" enum:"gray,red,orange,amber,green,teal,blue,indigo,violet,pink" doc:"A label's colour, as a tone"`
}

// cleanFolderName trims a folder name and refuses path separators and control characters.
func cleanFolderName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 100 || strings.ContainsAny(s, "/\\") ||
		strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", httpx.Validation("A folder name is 1 to 100 characters, without slashes.")
	}
	return s, nil
}

// errProvider is a provider's refusal of a folder change; its text is safe to show.
func errProvider(err error) error { return httpx.E(502, "provider_error", err.Error()) }

// actableFolder loads a folder of an account the caller sees and acts on.
func (s *service) actableFolder(ctx context.Context, p *auth.Principal, rawID string) (*folderRow, *accountRow, error) {
	id, err := parseID(rawID, "Folder")
	if err != nil {
		return nil, nil, err
	}
	f, err := loadFolder(ctx, s.d.DB.Pool, p.WorkspaceID, id)
	if err != nil {
		return nil, nil, notFound(err, "Folder")
	}
	a, err := visibleAccount(ctx, s.d.DB.Pool, p, f.AccountID)
	if err != nil {
		return nil, nil, notFound(err, "Folder")
	}
	return f, a, nil
}

func (s *service) registerFolderRoutes(api huma.API) {
	collabOp(api, huma.Operation{OperationID: "list-collab-folders", Method: http.MethodGet, Path: "/v1/collab/accounts/{id}/folders",
		Summary: "List an account's folders and labels"},
		func(ctx context.Context, in *accountIDInput) (*struct{ Body []MailFolder }, error) {
			p, err := s.require(ctx, auth.RoleViewer)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID, "Account")
			if err != nil {
				return nil, err
			}
			if _, err := visibleAccount(ctx, s.d.DB.Pool, p, id); err != nil {
				return nil, notFound(err, "Account")
			}
			rows, err := s.d.DB.Pool.Query(ctx, `SELECT `+folderOutColumns+` FROM collab_folders f
				WHERE f.workspace_id = $1 AND f.account_id = $2 ORDER BY `+folderOrderSQL, p.WorkspaceID, id)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (MailFolder, error) { return scanFolderOut(r) })
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if out == nil {
				out = []MailFolder{}
			}
			return &struct{ Body []MailFolder }{out}, nil
		})

	collabOp(api, huma.Operation{OperationID: "create-collab-folder", Method: http.MethodPost, Path: "/v1/collab/accounts/{id}/folders",
		Summary: "Create a folder or label"},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body folderNameBody
		}) (*folderBody, error) {
			p, err := s.require(ctx, auth.RoleAgent)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID, "Account")
			if err != nil {
				return nil, err
			}
			a, err := visibleAccount(ctx, s.d.DB.Pool, p, id)
			if err != nil {
				return nil, notFound(err, "Account")
			}
			name, err := cleanFolderName(in.Body.Name)
			if err != nil {
				return nil, err
			}
			var parent *RemoteFolder
			if in.Body.ParentID != nil {
				pf, err := loadFolder(ctx, s.d.DB.Pool, p.WorkspaceID, *in.Body.ParentID)
				if err != nil || pf.AccountID != a.ID {
					return nil, httpx.Validation("The parent folder is not in this account.")
				}
				r := pf.remote()
				parent = &r
			}
			var n int
			if err := s.d.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM collab_folders WHERE account_id = $1`, a.ID).Scan(&n); err != nil {
				return nil, httpx.Wrap(err)
			}
			if n >= maxFolders {
				return nil, httpx.Validation("This account has as many folders as opendesk keeps.")
			}
			prov, err := s.provider(ctx, a)
			if err != nil {
				return nil, errProvider(err)
			}
			defer prov.Close()
			pctx, cancel := context.WithTimeout(ctx, providerTimeout)
			defer cancel()
			label := in.Body.Kind == KindLabel || a.Provider == ProviderGoogle
			if label && a.Provider == ProviderIMAP {
				return nil, errLabels
			}
			if in.Body.Color != "" && !label {
				return nil, errFolderColor
			}
			var rf RemoteFolder
			if lc, ok := prov.(labelCreator); ok && label {
				if parent != nil {
					return nil, httpx.Validation("An Outlook category cannot be inside a folder.")
				}
				rf, err = lc.CreateLabel(pctx, name, in.Body.Color)
			} else {
				rf, err = prov.CreateFolder(pctx, name, parent)
				if err == nil && in.Body.Color != "" {
					if lc, ok := prov.(labelColorer); ok {
						if colored, cerr := lc.SetLabelColor(pctx, rf, in.Body.Color); cerr == nil {
							rf = colored
						}
					}
				}
			}
			if err != nil {
				return nil, errProvider(err)
			}
			var fid uuid.UUID
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				f, err := s.upsertFolder(ctx, tx, a, rf)
				if err != nil {
					return err
				}
				fid = f.ID
				return s.publishChanged(ctx, tx, p.WorkspaceID, &a.ID, nil, true)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out, err := loadFolderOut(ctx, s.d.DB.Pool, p.WorkspaceID, fid)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &folderBody{out}, nil
		})

	collabOp(api, huma.Operation{OperationID: "rename-collab-folder", Method: http.MethodPatch, Path: "/v1/collab/folders/{id}",
		Summary: "Rename a folder or label, or set a label's colour"},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body struct {
				Name  string `json:"name,omitempty" maxLength:"100"`
				Color string `json:"color,omitempty" enum:"gray,red,orange,amber,green,teal,blue,indigo,violet,pink" doc:"A label's colour, as a tone (Gmail labels and Outlook categories)"`
			}
		}) (*folderBody, error) {
			p, err := s.require(ctx, auth.RoleAgent)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			f, a, err := s.actableFolder(ctx, p, in.ID)
			if err != nil {
				return nil, err
			}
			if f.Role != nil {
				return nil, errSpecialFolder
			}
			b := in.Body
			rename := strings.TrimSpace(b.Name) != "" && strings.TrimSpace(b.Name) != f.Name
			if strings.TrimSpace(b.Name) == "" && b.Color == "" {
				return nil, httpx.Validation("Give a new name or a colour.")
			}
			var name string
			if rename {
				if name, err = cleanFolderName(b.Name); err != nil {
					return nil, err
				}
				if isCategory(f.RemoteID) {
					return nil, errCategoryRename
				}
			}
			if b.Color != "" && (f.Kind != KindLabel || a.Provider == ProviderIMAP) {
				return nil, errFolderColor
			}
			prov, err := s.provider(ctx, a)
			if err != nil {
				return nil, errProvider(err)
			}
			defer prov.Close()
			pctx, cancel := context.WithTimeout(ctx, providerTimeout)
			defer cancel()
			// The provider first, then stored; the next folder refresh brings back what it keeps.
			rf := f.remote()
			if rename {
				if rf, err = prov.RenameFolder(pctx, f.remote(), name); err != nil {
					return nil, errProvider(err)
				}
				if rf.RemoteID == "" {
					rf.RemoteID = f.RemoteID
				}
				if rf.Name == "" {
					rf.Name = name
				}
				if rf.Path == "" {
					rf.Path = rf.Name
					if i := strings.LastIndex(f.Path, "/"); i >= 0 {
						rf.Path = f.Path[:i+1] + rf.Name
					}
				}
			}
			color := f.Color
			if b.Color != "" {
				lc, ok := prov.(labelColorer)
				if !ok {
					return nil, errFolderColor
				}
				colored, err := lc.SetLabelColor(pctx, rf, b.Color)
				if err != nil {
					return nil, errProvider(err)
				}
				if colored.Color != "" {
					color = &colored.Color
				}
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				// The provider may give the folder a new id (an IMAP mailbox is its name): the row keeps its own.
				if _, err := tx.Exec(ctx, `UPDATE collab_folders SET remote_id = $3, name = $4, path = $5, color = $6
					WHERE workspace_id = $1 AND id = $2`, p.WorkspaceID, f.ID, rf.RemoteID, rf.Name, rf.Path, color); err != nil {
					return err
				}
				return s.publishChanged(ctx, tx, p.WorkspaceID, &a.ID, nil, true)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out, err := loadFolderOut(ctx, s.d.DB.Pool, p.WorkspaceID, f.ID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &folderBody{out}, nil
		})

	collabOp(api, huma.Operation{OperationID: "delete-collab-folder", Method: http.MethodDelete, Path: "/v1/collab/folders/{id}",
		Summary: "Delete a folder or label", DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct{}, error) {
			p, err := s.require(ctx, auth.RoleAgent)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			f, a, err := s.actableFolder(ctx, p, in.ID)
			if err != nil {
				return nil, err
			}
			if f.Role != nil {
				return nil, errSpecialFolder
			}
			prov, err := s.provider(ctx, a)
			if err != nil {
				return nil, errProvider(err)
			}
			defer prov.Close()
			pctx, cancel := context.WithTimeout(ctx, providerTimeout)
			defer cancel()
			if err := prov.DeleteFolder(pctx, f.remote()); err != nil {
				return nil, errProvider(err)
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				rows, err := tx.Query(ctx, `UPDATE collab_threads SET folder_ids = array_remove(folder_ids, $2), updated_at = now()
					WHERE account_id = $1 AND folder_ids @> ARRAY[$2::uuid] RETURNING id`, a.ID, f.ID)
				if err != nil {
					return err
				}
				threadIDs, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
				if err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `DELETE FROM collab_folders WHERE workspace_id = $1 AND id = $2`, p.WorkspaceID, f.ID); err != nil {
					return err
				}
				return s.publishChanged(ctx, tx, p.WorkspaceID, &a.ID, threadIDs, true)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return nil, nil
		})
}
