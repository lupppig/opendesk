package collab

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// maxSidebarBytes bounds the stored sidebar preferences.
const maxSidebarBytes = 16 << 10

// maxSidebarItem bounds one id in the sidebar preferences ("folder:<uuid>" and the like).
const maxSidebarItem = 100

// maxSidebarBadges bounds the chosen numbers; maxMailTabs the folder bar's tabs.
const (
	maxSidebarBadges = 300
	maxMailTabs      = 30
)

// defaultMailTabs is the folder bar of a member who never arranged it.
func defaultMailTabs() []string { return []string{"inbox", "starred", "sent", "drafts", "archive"} }

// tabBoxes are the boxes the folder bar can show as tabs.
var tabBoxes = map[string]bool{"inbox": true, "unread": true, "starred": true, "sent": true, "drafts": true,
	"archive": true, "spam": true, "trash": true, "all": true}

// badgePrefixes are the row kinds a number can be chosen for; badgeSections the sections with a default;
// badgeValues the numbers: unread and total for mail rows, count, awaiting, urgent and overdue for ticket
// rows, open for team rows, none for any. A row reads a value it has no such number for as its default.
var (
	badgePrefixes = []string{"box:", "account:", "folder:", "ticket_folder:", "view:", "team:"}
	badgeSections = map[string]bool{"mail": true, "inbox": true, "teams": true, "views": true, "favorites": true}
	badgeValues   = map[string]bool{"unread": true, "total": true, "none": true, "count": true,
		"awaiting": true, "urgent": true, "overdue": true, "open": true}
)

// defaultSidebar is the sidebar of a member who never arranged it.
func defaultSidebar() SidebarPrefs {
	return SidebarPrefs{Order: []string{"favorites", "inbox", "mail", "teams", "views", "workspace"}, Hidden: []string{},
		Pinned: []SidebarPin{}, ExpandedAccounts: []string{}, MarkReadOnOpen: true, UnifiedInbox: true,
		Badges: map[string]string{}, MailTabs: defaultMailTabs()}
}

// normalized fills missing lists so the shape always has arrays.
func (p SidebarPrefs) normalized() SidebarPrefs {
	if p.Order == nil {
		p.Order = defaultSidebar().Order
	}
	if p.Hidden == nil {
		p.Hidden = []string{}
	}
	if p.Pinned == nil {
		p.Pinned = []SidebarPin{}
	}
	if p.ExpandedAccounts == nil {
		p.ExpandedAccounts = []string{}
	}
	if p.Badges == nil {
		p.Badges = map[string]string{}
	}
	if p.MailTabs == nil {
		p.MailTabs = defaultMailTabs()
	}
	return p
}

// badgeKeyOK accepts a row id with a known prefix or a section with a default.
func badgeKeyOK(k string) bool {
	if !sidebarItemOK(k) {
		return false
	}
	if sec, ok := strings.CutPrefix(k, "section:"); ok {
		return badgeSections[sec]
	}
	for _, pre := range badgePrefixes {
		if rest, ok := strings.CutPrefix(k, pre); ok && rest != "" {
			return true
		}
	}
	return false
}

// mailTabOK accepts a box name or "folder:<uuid>".
func mailTabOK(t string) bool {
	if tabBoxes[t] {
		return true
	}
	id, ok := strings.CutPrefix(t, "folder:")
	if !ok {
		return false
	}
	_, err := uuid.Parse(id)
	return err == nil && len(id) == 36
}

// validateSidebar checks the chosen numbers and tabs; the other lists are checked by item.
func validateSidebar(p SidebarPrefs) error {
	if len(p.Badges) > maxSidebarBadges {
		return httpx.Validation("Numbers can be chosen for at most 300 rows.")
	}
	for k, v := range p.Badges {
		if !badgeKeyOK(k) || !badgeValues[v] {
			return httpx.Validation("Each number choice names a sidebar row or section and is unread, total, count or none.")
		}
	}
	if len(p.MailTabs) > maxMailTabs {
		return httpx.Validation("The folder bar holds at most 30 tabs.")
	}
	seen := map[string]bool{}
	for _, t := range p.MailTabs {
		if !mailTabOK(t) || seen[t] {
			return httpx.Validation("Each tab is a box (inbox, unread, starred, sent, drafts, archive, spam, trash, all) or a folder, once.")
		}
		seen[t] = true
	}
	return nil
}

// loadSidebar reads the member's saved preferences, or the defaults.
func (s *service) loadSidebar(ctx context.Context, wid, uid uuid.UUID) (SidebarPrefs, error) {
	var raw []byte
	err := s.d.DB.Pool.QueryRow(ctx, `SELECT prefs FROM collab_sidebar_prefs WHERE workspace_id = $1 AND user_id = $2`,
		wid, uid).Scan(&raw)
	if db.IsNotFound(err) {
		return defaultSidebar(), nil
	}
	if err != nil {
		return SidebarPrefs{}, err
	}
	var stored SidebarPrefs
	if json.Unmarshal(raw, &stored) != nil {
		return defaultSidebar(), nil
	}
	return stored.normalized(), nil
}

type sidebarBody struct {
	Body SidebarPrefs
}

func (s *service) registerSidebarRoutes(api huma.API) {
	collabOp(api, huma.Operation{OperationID: "get-collab-sidebar", Method: http.MethodGet, Path: "/v1/collab/sidebar",
		Summary: "Get my sidebar"},
		func(ctx context.Context, _ *struct{}) (*sidebarBody, error) {
			p, err := s.require(ctx, auth.RoleViewer)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out, err := s.loadSidebar(ctx, p.WorkspaceID, p.UserID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &sidebarBody{out}, nil
		})

	collabOp(api, huma.Operation{OperationID: "put-collab-sidebar", Method: http.MethodPut, Path: "/v1/collab/sidebar",
		Summary: "Save my sidebar"},
		func(ctx context.Context, in *sidebarBody) (*sidebarBody, error) {
			p, err := s.require(ctx, auth.RoleViewer)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			// A client that does not know the numbers or the tabs leaves them as saved.
			if in.Body.Badges == nil || in.Body.MailTabs == nil {
				saved, err := s.loadSidebar(ctx, p.WorkspaceID, p.UserID)
				if err != nil {
					return nil, httpx.Wrap(err)
				}
				if in.Body.Badges == nil {
					in.Body.Badges = saved.Badges
				}
				if in.Body.MailTabs == nil {
					in.Body.MailTabs = saved.MailTabs
				}
			}
			prefs := in.Body.normalized()
			if err := validateSidebar(prefs); err != nil {
				return nil, err
			}
			for _, list := range [][]string{prefs.Order, prefs.Hidden, prefs.ExpandedAccounts} {
				for _, item := range list {
					if !sidebarItemOK(item) {
						return nil, httpx.Validation("Each sidebar item is 1 to 100 characters.")
					}
				}
			}
			for _, pin := range prefs.Pinned {
				if !sidebarItemOK(pin.ID) {
					return nil, httpx.Validation("Each sidebar item is 1 to 100 characters.")
				}
			}
			raw, err := json.Marshal(prefs)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if len(raw) > maxSidebarBytes {
				return nil, httpx.Validation("The sidebar settings are too large.")
			}
			if _, err := s.d.DB.Pool.Exec(ctx, `INSERT INTO collab_sidebar_prefs (workspace_id, user_id, prefs, updated_at)
				VALUES ($1, $2, $3, now())
				ON CONFLICT (workspace_id, user_id) DO UPDATE SET prefs = EXCLUDED.prefs, updated_at = now()`,
				p.WorkspaceID, p.UserID, raw); err != nil {
				return nil, httpx.Wrap(err)
			}
			return &sidebarBody{prefs}, nil
		})
}

func sidebarItemOK(s string) bool {
	if s == "" || len(s) > maxSidebarItem {
		return false
	}
	return !strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}
