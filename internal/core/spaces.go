package core

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/collab"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// Spaces: sidebars a person builds from the workspaces they belong to, and each workspace's
// starter Space (contracts/spaces.md).

const (
	maxSpaces            = 20
	maxSpaceSections     = 20
	maxSectionItems      = 60
	maxSpaceItems        = 300
	maxSpacesBytes       = 64 << 10
	maxSpaceName         = 40
	maxSpaceSectionTitle = 40
	maxSpaceItemName     = 64
	maxSpaceItemID       = 100
	// maxSpacesBody bounds what is read; the stored form is held to maxSpacesBytes.
	maxSpacesBody = 256 << 10
)

// spaceColors are the colours a Space can take.
var spaceColors = []string{"blue", "violet", "teal", "green", "amber", "red", "pink", "grey"}

// spaceIcons are the icons a Space can show (lucide names); "" shows the name's first letter.
var spaceIcons = []string{"sun", "moon", "star", "inbox", "mail", "headphones", "users", "briefcase", "rocket",
	"flame", "zap", "heart", "coffee", "flag", "bookmark", "layers", "target", "globe", "bell", "leaf",
	"home", "chart", "shield", "sparkles"}

// spaceItemIDs are the ids of the item kinds with a fixed set; the other kinds name a uuid.
var spaceItemIDs = map[string][]string{
	"section":       collab.SidebarSectionIDs,
	"ticket_folder": {"unassigned", "mine", "open", "pending", "solved", "spam"},
	"box":           {"inbox", "shared", "assigned"},
	"page":          {"contacts", "knowledge", "reports"},
	"everywhere":    {"mine", "mail"},
}

// spaceEverywhere is the kind of the Everywhere lists, the one kind of no workspace (workspace_id "").
const spaceEverywhere = "everywhere"

var spaceUUIDKinds = []string{"team", "view", "account", "folder"}

var spaceSectionIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)

// CoreSpace is the contract shape Space.
type CoreSpace struct {
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	Icon     string             `json:"icon"`
	Color    string             `json:"color" enum:"grey,blue,violet,teal,green,amber,red,pink"`
	Marks    bool               `json:"marks"`
	Sections []CoreSpaceSection `json:"sections"`
}

// CoreSpaceSection is the contract shape SpaceSection.
type CoreSpaceSection struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Collapsed bool            `json:"collapsed"`
	Items     []CoreSpaceItem `json:"items"`
}

// CoreSpaceItem is the contract shape SpaceItem.
type CoreSpaceItem struct {
	WorkspaceID string `json:"workspace_id"`
	Kind        string `json:"kind" enum:"section,ticket_folder,team,view,box,account,folder,page,everywhere"`
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Badge       string `json:"badge,omitempty" enum:"unread,total,count,awaiting,urgent,overdue,open,none"`
}

// coreSpaceIn is a Space as a client sends it: color, icon and marks may be left out.
type coreSpaceIn struct {
	ID       string               `json:"id" maxLength:"36"`
	Name     string               `json:"name" maxLength:"200"`
	Icon     string               `json:"icon,omitempty" maxLength:"40"`
	Color    string               `json:"color,omitempty" maxLength:"20" doc:"Left out: grey"`
	Marks    *bool                `json:"marks,omitempty" doc:"Left out: true"`
	Sections []coreSpaceSectionIn `json:"sections" maxItems:"20"`
}

type coreSpaceSectionIn struct {
	ID        string            `json:"id" maxLength:"40"`
	Title     string            `json:"title,omitempty" maxLength:"200"`
	Collapsed bool              `json:"collapsed,omitempty"`
	Items     []coreSpaceItemIn `json:"items" maxItems:"60"`
}

type coreSpaceItemIn struct {
	WorkspaceID string `json:"workspace_id,omitempty" maxLength:"36" doc:"Required in your Spaces (\"\" for an everywhere row); set to the workspace in its starter Space"`
	Kind        string `json:"kind" maxLength:"20"`
	ID          string `json:"id" maxLength:"100"`
	Name        string `json:"name,omitempty" maxLength:"300"`
	Badge       string `json:"badge,omitempty" maxLength:"20"`
}

type coreSpacesBody struct {
	Spaces []coreSpaceIn `json:"spaces" maxItems:"20"`
}

type coreSpacesOut struct {
	Spaces []CoreSpace `json:"spaces"`
	Saved  bool        `json:"saved" doc:"false: nothing saved yet (no Spaces)"`
}

type coreStarterOut struct {
	Space *CoreSpace `json:"space"`
}

// validSpaceUUID accepts a uuid in its 36-character form and returns it canonical.
func validSpaceUUID(s string) (string, bool) {
	if len(s) != 36 {
		return "", false
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return "", false
	}
	return id.String(), true
}

// validateSpace checks one Space against the limits of contracts/spaces.md and returns its stored
// form. workspace, when set, replaces every item's workspace_id (a starter Space).
func validateSpace(in coreSpaceIn, workspace *uuid.UUID) (CoreSpace, error) {
	out := CoreSpace{Icon: in.Icon, Color: in.Color, Marks: in.Marks == nil || *in.Marks,
		Sections: []CoreSpaceSection{}}
	id, ok := validSpaceUUID(in.ID)
	if !ok {
		return out, httpx.Validation("Each Space needs an id that is a uuid.")
	}
	out.ID = id
	if utf8.RuneCountInString(displayname.Normalize(in.Name)) > maxSpaceName {
		return out, httpx.Validation("A Space's name must be 40 characters or less.")
	}
	name, err := displayname.Validate("A Space's name", in.Name, displayname.Workspace, false)
	if err != nil {
		return out, err
	}
	out.Name = name
	if out.Icon != "" && !slices.Contains(spaceIcons, out.Icon) {
		return out, httpx.Validation("A Space's icon must be empty or one of the listed icons.")
	}
	if out.Color == "" {
		out.Color = "grey"
	}
	if !slices.Contains(spaceColors, out.Color) {
		return out, httpx.Validation("A Space's colour must be grey, blue, violet, teal, green, amber, red or pink.")
	}
	if len(in.Sections) > maxSpaceSections {
		return out, httpx.Validation("A Space holds at most 20 sections.")
	}
	sectionIDs := map[string]bool{}
	total := 0
	for _, sec := range in.Sections {
		if !spaceSectionIDRe.MatchString(sec.ID) {
			return out, httpx.Validation("Each section needs an id of 1 to 40 letters, digits, dashes or underscores.")
		}
		if sectionIDs[sec.ID] {
			return out, httpx.Validation("Each section id appears once in its Space.")
		}
		sectionIDs[sec.ID] = true
		if utf8.RuneCountInString(displayname.Normalize(sec.Title)) > maxSpaceSectionTitle {
			return out, httpx.Validation("A section's title must be 40 characters or less.")
		}
		title, err := displayname.Validate("A section's title", sec.Title, displayname.Workspace, true)
		if err != nil {
			return out, err
		}
		if len(sec.Items) > maxSectionItems {
			return out, httpx.Validation("A section holds at most 60 rows.")
		}
		total += len(sec.Items)
		if total > maxSpaceItems {
			return out, httpx.Validation("A Space holds at most 300 rows.")
		}
		oSec := CoreSpaceSection{ID: sec.ID, Title: title, Collapsed: sec.Collapsed, Items: make([]CoreSpaceItem, 0, len(sec.Items))}
		for _, it := range sec.Items {
			item, err := validateSpaceItem(it, workspace)
			if err != nil {
				return out, err
			}
			oSec.Items = append(oSec.Items, item)
		}
		out.Sections = append(out.Sections, oSec)
	}
	return out, nil
}

func validateSpaceItem(in coreSpaceItemIn, workspace *uuid.UUID) (CoreSpaceItem, error) {
	out := CoreSpaceItem{Kind: in.Kind, Badge: in.Badge}
	if in.Kind == spaceEverywhere {
		// The Everywhere lists span every workspace: no workspace of their own, and none in a starter.
		if workspace != nil {
			return out, httpx.Validation("A starter Space's rows are all of its workspace: an everywhere row can't be one.")
		}
		if in.WorkspaceID != "" {
			return out, httpx.Validation("An everywhere row belongs to no workspace: its workspace_id is \"\".")
		}
	} else if workspace != nil {
		out.WorkspaceID = workspace.String()
	} else {
		wid, ok := validSpaceUUID(in.WorkspaceID)
		if !ok {
			return out, httpx.Validation("Each row needs the id of one of your workspaces.")
		}
		out.WorkspaceID = wid
	}
	if in.ID == "" || len(in.ID) > maxSpaceItemID {
		return out, httpx.Validation("Each row needs an id of 1 to 100 characters.")
	}
	if fixed, ok := spaceItemIDs[in.Kind]; ok {
		if !slices.Contains(fixed, in.ID) {
			return out, httpx.Validation("Each " + kindLabel(in.Kind) + " row needs one of the ids the app knows.")
		}
		out.ID = in.ID
	} else if slices.Contains(spaceUUIDKinds, in.Kind) {
		id, ok := validSpaceUUID(in.ID)
		if !ok {
			return out, httpx.Validation("Each " + kindLabel(in.Kind) + " row needs an id that is a uuid.")
		}
		out.ID = id
	} else {
		return out, httpx.Validation("Each row's kind is section, ticket_folder, team, view, box, account, folder, page or everywhere.")
	}
	if in.Name != "" {
		if utf8.RuneCountInString(displayname.Normalize(in.Name)) > maxSpaceItemName {
			return out, httpx.Validation("A row's name must be 64 characters or less.")
		}
		name, err := displayname.Validate("A row's name", in.Name, displayname.Workspace, false)
		if err != nil {
			return out, err
		}
		out.Name = name
	}
	if out.Badge != "" && !collab.BadgeValue(out.Badge) {
		return out, httpx.Validation("A row's number is unread, total, count, awaiting, urgent, overdue, open or none.")
	}
	return out, nil
}

func kindLabel(kind string) string {
	switch kind {
	case "ticket_folder":
		return "inbox folder"
	case "box":
		return "Collab box"
	case "account":
		return "mail account"
	case "folder":
		return "mail folder"
	case spaceEverywhere:
		return "everywhere"
	}
	return kind
}

// memberWorkspaces is every workspace uid is a member of.
func memberWorkspaces(ctx context.Context, q db.Querier, uid uuid.UUID) (map[string]bool, error) {
	rows, err := q.Query(ctx, `SELECT workspace_id FROM members WHERE user_id = $1`, uid)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(list))
	for _, id := range list {
		out[id.String()] = true
	}
	return out, nil
}

// withoutLeft drops the items of workspaces the user is no longer a member of; their sections stay.
func withoutLeft(spaces []CoreSpace, member map[string]bool) []CoreSpace {
	for i := range spaces {
		if spaces[i].Sections == nil {
			spaces[i].Sections = []CoreSpaceSection{}
		}
		for j := range spaces[i].Sections {
			sec := &spaces[i].Sections[j]
			kept := []CoreSpaceItem{}
			for _, it := range sec.Items {
				if it.Kind == spaceEverywhere || member[it.WorkspaceID] {
					kept = append(kept, it)
				}
			}
			sec.Items = kept
		}
	}
	return spaces
}

// loadStarter reads a workspace's starter Space; nil when it has none.
func loadStarter(ctx context.Context, q db.Querier, wid uuid.UUID) (*CoreSpace, error) {
	var raw []byte
	err := q.QueryRow(ctx, `SELECT space FROM workspace_starter_spaces WHERE workspace_id = $1`, wid).Scan(&raw)
	if db.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var sp CoreSpace
	if err := json.Unmarshal(raw, &sp); err != nil {
		return nil, err
	}
	if sp.Sections == nil {
		sp.Sections = []CoreSpaceSection{}
	}
	return &sp, nil
}

// loadSpaces is GET /v1/me/spaces: the saved Spaces without the items of workspaces left, or none.
func (s *svc) loadSpaces(ctx context.Context, q db.Querier, uid uuid.UUID) (coreSpacesOut, error) {
	var raw []byte
	err := q.QueryRow(ctx, `SELECT spaces FROM user_spaces WHERE user_id = $1`, uid).Scan(&raw)
	if db.IsNotFound(err) {
		return coreSpacesOut{Spaces: []CoreSpace{}}, nil
	}
	if err != nil {
		return coreSpacesOut{}, err
	}
	var spaces []CoreSpace
	if err := json.Unmarshal(raw, &spaces); err != nil {
		return coreSpacesOut{}, err
	}
	if spaces == nil {
		spaces = []CoreSpace{}
	}
	member, err := memberWorkspaces(ctx, q, uid)
	if err != nil {
		return coreSpacesOut{}, err
	}
	return coreSpacesOut{Spaces: withoutLeft(spaces, member), Saved: true}, nil
}

func (s *svc) registerSpaces(api huma.API) {
	tags := []string{"Spaces"}

	register(s, api, huma.Operation{OperationID: "list-my-spaces", Method: http.MethodGet, Path: "/v1/me/spaces",
		Summary: "List your Spaces", Tags: tags,
		Description: "Your sidebars across your workspaces. With none saved, one generated Space per workspace (`saved: false`)."},
		func(ctx context.Context, _ *struct{}) (*struct{ Body coreSpacesOut }, error) {
			p, err := auth.RequireUser(ctx)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out, err := s.loadSpaces(ctx, s.d.DB.Pool, p.UserID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body coreSpacesOut }{out}, nil
		})

	register(s, api, huma.Operation{OperationID: "replace-my-spaces", Method: http.MethodPut, Path: "/v1/me/spaces",
		Summary: "Replace your Spaces", Tags: tags, MaxBodyBytes: maxSpacesBody},
		func(ctx context.Context, in *struct{ Body coreSpacesBody }) (*struct{ Body coreSpacesOut }, error) {
			p, err := auth.RequireUser(ctx)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if len(in.Body.Spaces) > maxSpaces {
				return nil, httpx.Validation("You can keep at most 20 Spaces.")
			}
			spaces := make([]CoreSpace, 0, len(in.Body.Spaces))
			seen := map[string]bool{}
			for _, sp := range in.Body.Spaces {
				out, err := validateSpace(sp, nil)
				if err != nil {
					return nil, httpx.Wrap(err)
				}
				if seen[out.ID] {
					return nil, httpx.Validation("Each Space id appears once.")
				}
				seen[out.ID] = true
				spaces = append(spaces, out)
			}
			raw, err := json.Marshal(spaces)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if len(raw) > maxSpacesBytes {
				return nil, httpx.Validation("Your Spaces are too large to save. Remove some rows or Spaces.")
			}
			var out coreSpacesOut
			err = s.tx(ctx, func(tx pgx.Tx) error {
				member, err := memberWorkspaces(ctx, tx, p.UserID)
				if err != nil {
					return err
				}
				for _, sp := range spaces {
					for _, sec := range sp.Sections {
						for _, it := range sec.Items {
							if it.Kind != spaceEverywhere && !member[it.WorkspaceID] {
								return httpx.Validation("Each row must be of a workspace you are a member of.")
							}
						}
					}
				}
				if _, err := tx.Exec(ctx, `INSERT INTO user_spaces (user_id, spaces, updated_at) VALUES ($1, $2, now())
					ON CONFLICT (user_id) DO UPDATE SET spaces = EXCLUDED.spaces, updated_at = now()`, p.UserID, raw); err != nil {
					return err
				}
				out = coreSpacesOut{Spaces: spaces, Saved: true}
				return nil
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body coreSpacesOut }{out}, nil
		})

	register(s, api, huma.Operation{OperationID: "get-starter-space", Method: http.MethodGet, Path: "/v1/sidebar/starter",
		Summary: "Get the workspace's starter Space", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body coreStarterOut }, error) {
			p, err := requireUserWorkspace(ctx, auth.RoleViewer)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			sp, err := loadStarter(ctx, s.d.DB.Pool, p.WorkspaceID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body coreStarterOut }{coreStarterOut{Space: sp}}, nil
		})

	register(s, api, huma.Operation{OperationID: "replace-starter-space", Method: http.MethodPut, Path: "/v1/sidebar/starter",
		Summary: "Publish the workspace's starter Space", Tags: tags, MaxBodyBytes: maxSpacesBody,
		Description: "Every row is of this workspace: each item's `workspace_id` is set to it."},
		func(ctx context.Context, in *struct{ Body coreSpaceIn }) (*struct{ Body coreStarterOut }, error) {
			p, err := requireUserWorkspace(ctx, auth.RoleAdmin)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			wid := p.WorkspaceID
			sp, err := validateSpace(in.Body, &wid)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			raw, err := json.Marshal(sp)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if len(raw) > maxSpacesBytes {
				return nil, httpx.Validation("The starter Space is too large to save. Remove some rows.")
			}
			err = s.tx(ctx, func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, `INSERT INTO workspace_starter_spaces (workspace_id, space, updated_by, updated_at)
					VALUES ($1, $2, $3, now())
					ON CONFLICT (workspace_id) DO UPDATE SET space = EXCLUDED.space, updated_by = EXCLUDED.updated_by, updated_at = now()`,
					wid, raw, p.UserID); err != nil {
					return err
				}
				return audit.Log(ctx, tx, p, wid, "sidebar.starter_updated", "workspace", &wid,
					map[string]any{"name": sp.Name, "sections": len(sp.Sections)})
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body coreStarterOut }{coreStarterOut{Space: &sp}}, nil
		})

	register(s, api, huma.Operation{OperationID: "delete-starter-space", Method: http.MethodDelete, Path: "/v1/sidebar/starter",
		Summary: "Remove the workspace's starter Space", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, _ *struct{}) (*struct{}, error) {
			p, err := requireUserWorkspace(ctx, auth.RoleAdmin)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			wid := p.WorkspaceID
			err = s.tx(ctx, func(tx pgx.Tx) error {
				tag, err := tx.Exec(ctx, `DELETE FROM workspace_starter_spaces WHERE workspace_id = $1`, wid)
				if err != nil || tag.RowsAffected() == 0 {
					return err
				}
				return audit.Log(ctx, tx, p, wid, "sidebar.starter_removed", "workspace", &wid, nil)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return nil, nil
		})
}
