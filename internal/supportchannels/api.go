package supportchannels

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/jobs"
	"github.com/useopendesk/opendesk/internal/supportchannels/channel"
)

const maxWebhookBody = 1 << 20

func (s *Service) register(api huma.API, r chi.Router) {
	tags := []string{"Support channels"}
	huma.Register(api, huma.Operation{OperationID: "list-support-channel-providers", Method: http.MethodGet, Path: "/v1/support-channels/providers", Tags: tags, Summary: "List support channel providers"},
		func(ctx context.Context, _ *struct{}) (*struct {
			Body struct {
				Data []channel.Descriptor `json:"data"`
			}
		}, error) {
			if _, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeSettingsRead); err != nil {
				return nil, httpx.Wrap(err)
			}
			out := &struct {
				Body struct {
					Data []channel.Descriptor `json:"data"`
				}
			}{}
			out.Body.Data = s.registry.List()
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "discover-support-channel-accounts", Method: http.MethodPost, Path: "/v1/support-channels/providers/{provider}/accounts", Tags: tags, Summary: "Discover provider accounts"},
		func(ctx context.Context, in *struct {
			Provider string `path:"provider"`
			Body     struct {
				Credentials map[string]string `json:"credentials"`
			}
		}) (*struct {
			Body struct {
				Data []channel.Account `json:"data"`
			}
		}, error) {
			if _, err := requireAdminUser(ctx); err != nil {
				return nil, httpx.Wrap(err)
			}
			a, ok := s.registry.Get(strings.TrimSpace(in.Provider))
			if !ok {
				return nil, httpx.Validation("Unknown support channel provider.")
			}
			discoverer, ok := a.(channel.AccountDiscoverer)
			if !ok {
				return nil, httpx.Validation("This provider does not have selectable accounts.")
			}
			items, err := discoverer.DiscoverAccounts(ctx, s.http, discoveryCredentials(a.Describe(), in.Body.Credentials))
			if err != nil {
				return nil, httpx.E(422, "support_channel_discovery_failed", "Couldn't load accounts from the provider.")
			}
			for i := range items {
				items[i].Name = displayname.CleanOr(items[i].Name, displayname.Workspace, items[i].ID)
			}
			sort.Slice(items, func(i, j int) bool {
				if items[i].Name == items[j].Name {
					return items[i].ID < items[j].ID
				}
				return items[i].Name < items[j].Name
			})
			out := &struct {
				Body struct {
					Data []channel.Account `json:"data"`
				}
			}{}
			out.Body.Data = items
			if out.Body.Data == nil {
				out.Body.Data = []channel.Account{}
			}
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "list-support-channel-connections", Method: http.MethodGet, Path: "/v1/support-channels/connections", Tags: tags, Summary: "List support channel connections"},
		func(ctx context.Context, _ *struct{}) (*struct {
			Body struct {
				Data []connection `json:"data"`
			}
		}, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			list, err := s.listConnections(ctx, p.WorkspaceID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out := &struct {
				Body struct {
					Data []connection `json:"data"`
				}
			}{}
			out.Body.Data = list
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "create-support-channel-connection", Method: http.MethodPost, Path: "/v1/support-channels/connections", Tags: tags, Summary: "Connect a support channel", DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *struct {
			Body struct {
				Provider    string            `json:"provider"`
				Name        string            `json:"name,omitempty" maxLength:"80"`
				Credentials map[string]string `json:"credentials"`
			}
		}) (*struct{ Body connection }, error) {
			p, err := requireAdminUser(ctx)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			a, ok := s.registry.Get(strings.TrimSpace(in.Body.Provider))
			if !ok {
				return nil, httpx.Validation("Unknown support channel provider.")
			}
			credentials := cleanCredentials(a.Describe(), in.Body.Credentials)
			if credentials == nil {
				return nil, httpx.Validation("Provide every required credential.")
			}
			account, err := a.Validate(ctx, s.http, credentials)
			if err != nil {
				return nil, httpx.E(422, "support_channel_failed", "Couldn't connect to the provider.")
			}
			account.Name = displayname.CleanOr(account.Name, displayname.Workspace, a.Describe().Name)
			sealed, err := s.d.Box.SealJSON(credentials)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			name := strings.TrimSpace(in.Body.Name)
			if name == "" {
				name = account.Name
			} else if name, err = displayname.Validate("Name", name, displayname.Workspace, false); err != nil {
				return nil, httpx.Wrap(err)
			}
			x := connection{ID: ids.New(), WorkspaceID: p.WorkspaceID, Provider: a.Describe().Key, Name: name, ExternalAccountID: account.ID, ExternalAccountName: account.Name, CredentialsEnc: sealed, Status: "connected", CreatedAt: time.Now().UTC()}
			x.WebhookURL = s.webhookURL(x)
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, `INSERT INTO support_channel_connections (id,workspace_id,provider,name,external_account_id,external_account_name,credentials_enc,created_by,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, x.ID, x.WorkspaceID, x.Provider, x.Name, x.ExternalAccountID, x.ExternalAccountName, x.CredentialsEnc, p.UserID, x.CreatedAt); err != nil {
					return err
				}
				if _, err := events.Publish(ctx, tx, events.Event{WorkspaceID: p.WorkspaceID, Type: "support_channel_connection.created", Data: map[string]any{"support_channel_connection": x}}); err != nil {
					return err
				}
				return audit.Log(ctx, tx, p, p.WorkspaceID, "support_channel.connected", "support_channel", &x.ID, map[string]any{"provider": x.Provider, "account": x.ExternalAccountID})
			})
			if db.IsUniqueViolation(err, "") {
				return nil, httpx.Conflict("That provider account is already connected.")
			}
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			x = s.reconcileConnection(ctx, x, credentials)
			x.CredentialsEnc = nil
			return &struct{ Body connection }{x}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "rotate-support-channel-credentials", Method: http.MethodPatch, Path: "/v1/support-channels/connections/{id}", Tags: tags, Summary: "Rotate support channel credentials"},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body struct {
				Name        string            `json:"name,omitempty" maxLength:"80"`
				Credentials map[string]string `json:"credentials"`
			}
		}) (*struct{ Body connection }, error) {
			p, err := requireAdminUser(ctx)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			c, err := s.loadWorkspaceConnection(ctx, p.WorkspaceID, id)
			if db.IsNotFound(err) {
				return nil, httpx.NotFound("Support channel")
			}
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			a, ok := s.registry.Get(c.Provider)
			if !ok {
				return nil, httpx.Validation("Unknown support channel provider.")
			}
			credentials := cleanCredentials(a.Describe(), in.Body.Credentials)
			if credentials == nil {
				return nil, httpx.Validation("Provide every required credential.")
			}
			account, err := a.Validate(ctx, s.http, credentials)
			if err != nil {
				return nil, httpx.E(422, "support_channel_failed", "Couldn't connect to the provider.")
			}
			if account.ID != c.ExternalAccountID {
				return nil, httpx.Validation("The credentials must belong to the same provider account.")
			}
			account.Name = displayname.CleanOr(account.Name, displayname.Workspace, a.Describe().Name)
			sealed, err := s.d.Box.SealJSON(credentials)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			name := strings.TrimSpace(in.Body.Name)
			if name == "" {
				name = c.Name
			} else if name, err = displayname.Validate("Name", name, displayname.Workspace, false); err != nil {
				return nil, httpx.Wrap(err)
			}
			now := time.Now().UTC()
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				tag, err := tx.Exec(ctx, `UPDATE support_channel_connections SET name=$3,external_account_name=$4,credentials_enc=$5,credentials_updated_at=$6,updated_at=$6 WHERE workspace_id=$1 AND id=$2`, p.WorkspaceID, c.ID, name, account.Name, sealed, now)
				if err != nil {
					return err
				}
				if tag.RowsAffected() == 0 {
					return pgx.ErrNoRows
				}
				c.Name, c.ExternalAccountName, c.CredentialsEnc, c.CredentialsUpdatedAt = name, account.Name, sealed, now
				if _, err = events.Publish(ctx, tx, events.Event{WorkspaceID: p.WorkspaceID, Type: "support_channel_connection.updated", Data: map[string]any{"support_channel_connection": c}}); err != nil {
					return err
				}
				return audit.Log(ctx, tx, p, p.WorkspaceID, "support_channel.credentials_rotated", "support_channel", &c.ID, map[string]any{"provider": c.Provider, "account": c.ExternalAccountID})
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			c = s.reconcileConnection(ctx, c, credentials)
			c.CredentialsEnc = nil
			return &struct{ Body connection }{c}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "test-support-channel-connection", Method: http.MethodPost, Path: "/v1/support-channels/connections/{id}/test", Tags: tags, Summary: "Test and repair a support channel connection"},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct{ Body connection }, error) {
			p, err := requireAdminUser(ctx)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			c, err := s.loadWorkspaceConnection(ctx, p.WorkspaceID, id)
			if db.IsNotFound(err) {
				return nil, httpx.NotFound("Support channel")
			}
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			credentials, err := s.credentials(c)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			c = s.reconcileConnection(ctx, c, credentials)
			c.CredentialsEnc = nil
			return &struct{ Body connection }{c}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "discover-support-channel-destinations", Method: http.MethodGet, Path: "/v1/support-channels/connections/{id}/destinations", Tags: tags, Summary: "Discover provider destinations"},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct {
			Body struct {
				Data []AvailableDestination `json:"data"`
			}
		}, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			c, err := s.loadWorkspaceConnection(ctx, p.WorkspaceID, id)
			if db.IsNotFound(err) {
				return nil, httpx.NotFound("Support channel")
			}
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			credentials, err := s.credentials(c)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			items, err := s.discoverDestinations(ctx, c, credentials)
			if err != nil {
				return nil, httpx.E(502, "support_channel_discovery_failed", "Couldn't load channels from the provider.")
			}
			out := &struct {
				Body struct {
					Data []AvailableDestination `json:"data"`
				}
			}{}
			out.Body.Data = make([]AvailableDestination, 0, len(items))
			for _, item := range items {
				out.Body.Data = append(out.Body.Data, AvailableDestination{
					ID: item.ID, Name: item.Name, Kind: item.Kind, Available: item.Available,
				})
			}
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "delete-support-channel-connection", Method: http.MethodDelete, Path: "/v1/support-channels/connections/{id}", Tags: tags, Summary: "Disconnect a support channel", DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct{}, error) {
			p, err := requireAdminUser(ctx)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				tag, err := tx.Exec(ctx, `DELETE FROM support_channel_connections WHERE workspace_id=$1 AND id=$2`, p.WorkspaceID, id)
				if err != nil {
					return err
				}
				if tag.RowsAffected() == 0 {
					return pgx.ErrNoRows
				}
				if _, err = events.Publish(ctx, tx, events.Event{WorkspaceID: p.WorkspaceID, Type: "support_channel_connection.deleted", Data: map[string]any{"support_channel_connection_id": id}}); err != nil {
					return err
				}
				return audit.Log(ctx, tx, p, p.WorkspaceID, "support_channel.disconnected", "support_channel", &id, nil)
			})
			if db.IsNotFound(err) {
				return nil, httpx.NotFound("Support channel")
			}
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{}{}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "list-support-channel-destinations", Method: http.MethodGet, Path: "/v1/support-channels/destinations", Tags: tags, Summary: "List support channel destinations"},
		func(ctx context.Context, _ *struct{}) (*struct {
			Body struct {
				Data []destination `json:"data"`
			}
		}, error) {
			p, e := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeSettingsRead)
			if e != nil {
				return nil, httpx.Wrap(e)
			}
			rows, e := s.d.DB.Pool.Query(ctx, `SELECT id,connection_id,team_id,external_channel_id,external_channel_name,enabled,include_notes,created_at FROM support_channel_destinations WHERE workspace_id=$1 ORDER BY created_at`, p.WorkspaceID)
			if e != nil {
				return nil, httpx.Wrap(e)
			}
			list, e := pgx.CollectRows(rows, func(r pgx.CollectableRow) (destination, error) {
				var d destination
				e := r.Scan(&d.ID, &d.ConnectionID, &d.TeamID, &d.ExternalChannelID, &d.ExternalChannelName, &d.Enabled, &d.IncludeNotes, &d.CreatedAt)
				return d, e
			})
			if e != nil {
				return nil, httpx.Wrap(e)
			}
			out := &struct {
				Body struct {
					Data []destination `json:"data"`
				}
			}{}
			out.Body.Data = list
			if list == nil {
				out.Body.Data = []destination{}
			}
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "create-support-channel-destination", Method: http.MethodPost, Path: "/v1/support-channels/destinations", Tags: tags, Summary: "Route a team to a provider channel", DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *struct {
			Body struct {
				ConnectionID        string `json:"connection_id"`
				TeamID              string `json:"team_id,omitempty"`
				ExternalChannelID   string `json:"external_channel_id" maxLength:"200"`
				ExternalChannelName string `json:"external_channel_name,omitempty" maxLength:"100"`
				IncludeNotes        bool   `json:"include_notes,omitempty" doc:"Mirror internal notes too. Off by default: only customer and agent replies are posted."`
			}
		}) (*struct{ Body destination }, error) {
			p, e := requireAdminUser(ctx)
			if e != nil {
				return nil, httpx.Wrap(e)
			}
			cid, e := parseID(in.Body.ConnectionID)
			if e != nil {
				return nil, e
			}
			var team *uuid.UUID
			if in.Body.TeamID != "" {
				id, e := parseID(in.Body.TeamID)
				if e != nil {
					return nil, e
				}
				var private bool
				e = s.d.DB.Pool.QueryRow(ctx, `SELECT private FROM teams WHERE workspace_id=$1 AND id=$2`, p.WorkspaceID, id).Scan(&private)
				if db.IsNotFound(e) {
					return nil, httpx.NotFound("Team")
				}
				if e != nil {
					return nil, httpx.Wrap(e)
				}
				if private {
					return nil, httpx.Validation("Private teams cannot be mirrored to external channels.")
				}
				team = &id
			}
			if strings.TrimSpace(in.Body.ExternalChannelID) == "" {
				return nil, httpx.Validation("Enter the provider channel ID.")
			}
			d := destination{ID: ids.New(), ConnectionID: cid, TeamID: team, ExternalChannelID: strings.TrimSpace(in.Body.ExternalChannelID), ExternalChannelName: cleanExternalName(in.Body.ExternalChannelName), Enabled: true, IncludeNotes: in.Body.IncludeNotes, CreatedAt: time.Now().UTC()}
			inserted := false
			e = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				tag, err := tx.Exec(ctx, `INSERT INTO support_channel_destinations(id,workspace_id,connection_id,team_id,external_channel_id,external_channel_name,include_notes,created_at) SELECT $1,$2,$3,$4,$5,$6,$7,$8 WHERE EXISTS(SELECT 1 FROM support_channel_connections WHERE workspace_id=$2 AND id=$3)`, d.ID, p.WorkspaceID, d.ConnectionID, d.TeamID, d.ExternalChannelID, d.ExternalChannelName, d.IncludeNotes, d.CreatedAt)
				if err != nil {
					return err
				}
				inserted = tag.RowsAffected() > 0
				if !inserted {
					return nil
				}
				if _, err := events.Publish(ctx, tx, events.Event{WorkspaceID: p.WorkspaceID, Type: "support_channel_destination.created", Data: map[string]any{"support_channel_destination": d}}); err != nil {
					return err
				}
				return audit.Log(ctx, tx, p, p.WorkspaceID, "support_channel.destination_created", "support_channel_destination", &d.ID, map[string]any{"connection_id": d.ConnectionID, "team_id": d.TeamID, "include_notes": d.IncludeNotes})
			})
			if db.IsUniqueViolation(e, "") {
				return nil, httpx.Conflict("That team or provider channel already has a destination.")
			}
			if e != nil {
				return nil, httpx.Wrap(e)
			}
			if !inserted {
				return nil, httpx.NotFound("Support channel")
			}
			return &struct{ Body destination }{d}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "delete-support-channel-destination", Method: http.MethodDelete, Path: "/v1/support-channels/destinations/{id}", Tags: tags, Summary: "Remove a support channel destination", DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct{}, error) {
			p, err := requireAdminUser(ctx)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				tag, err := tx.Exec(ctx, `DELETE FROM support_channel_destinations WHERE workspace_id=$1 AND id=$2`, p.WorkspaceID, id)
				if err != nil {
					return err
				}
				if tag.RowsAffected() == 0 {
					return pgx.ErrNoRows
				}
				if _, err := events.Publish(ctx, tx, events.Event{WorkspaceID: p.WorkspaceID, Type: "support_channel_destination.deleted", Data: map[string]any{"support_channel_destination_id": id}}); err != nil {
					return err
				}
				return audit.Log(ctx, tx, p, p.WorkspaceID, "support_channel.destination_deleted", "support_channel_destination", &id, nil)
			})
			if db.IsNotFound(err) {
				return nil, httpx.NotFound("Support channel destination")
			}
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{}{}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "create-support-channel-link-code", Method: http.MethodPost, Path: "/v1/me/support-channel-link-code", Tags: tags, Summary: "Create a one-time external account link code", DefaultStatus: http.StatusCreated},
		func(ctx context.Context, _ *struct{}) (*struct {
			Body struct {
				Code      string    `json:"code"`
				ExpiresAt time.Time `json:"expires_at"`
			}
		}, error) {
			p, e := requireUser(ctx, auth.RoleAgent, "")
			if e != nil {
				return nil, httpx.Wrap(e)
			}
			code := strings.ToUpper(crypto.RandomString(10))
			exp := time.Now().UTC().Add(10 * time.Minute)
			e = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, `DELETE FROM support_channel_link_codes WHERE expires_at<=now() OR (workspace_id=$1 AND user_id=$2)`, p.WorkspaceID, p.UserID); err != nil {
					return err
				}
				_, err := tx.Exec(ctx, `INSERT INTO support_channel_link_codes(code_hash,workspace_id,user_id,expires_at) VALUES($1,$2,$3,$4)`, crypto.HashToken(code), p.WorkspaceID, p.UserID, exp)
				return err
			})
			if e != nil {
				return nil, httpx.Wrap(e)
			}
			out := &struct {
				Body struct {
					Code      string    `json:"code"`
					ExpiresAt time.Time `json:"expires_at"`
				}
			}{}
			out.Body.Code, out.Body.ExpiresAt = code, exp
			return out, nil
		})

	r.Post("/v1/support-channels/hooks/{provider}/{id}", s.webhook)
}

// requireAdminUser admits a workspace admin signed in as a person. Connecting a
// bot account, routing tickets to external chats and linking identities hand
// ticket access to another service, so API keys can't manage them.
func requireAdminUser(ctx context.Context) (*auth.Principal, error) {
	return requireUser(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
}

// requireUser admits a person with at least role. scope bounds an AI client's
// OAuth grant; "" refuses grants as well as API keys.
func requireUser(ctx context.Context, role auth.Role, scope string) (*auth.Principal, error) {
	p, err := auth.RequireWorkspace(ctx, role, scope)
	if err != nil {
		return nil, httpx.Wrap(err)
	}
	if p.Kind != auth.KindUser {
		return nil, httpx.Forbidden()
	}
	return p, nil
}

func cleanCredentials(d channel.Descriptor, input map[string]string) map[string]string {
	out := map[string]string{}
	for _, f := range d.Fields {
		v := strings.TrimSpace(input[f.Name])
		if v == "" && f.Generated {
			v = crypto.RandomString(48)
		}
		if v == "" {
			return nil
		}
		out[f.Name] = v
	}
	return out
}

func discoveryCredentials(d channel.Descriptor, input map[string]string) map[string]string {
	out := map[string]string{}
	for _, f := range d.Fields {
		if v := strings.TrimSpace(input[f.Name]); v != "" && !f.Generated && !f.Discovered {
			out[f.Name] = v
		}
	}
	return out
}
func parseID(raw string) (uuid.UUID, error) {
	id, e := uuid.Parse(raw)
	if e != nil {
		return uuid.Nil, httpx.NotFound("Support channel")
	}
	return id, nil
}
func (s *Service) webhookURL(c connection) string {
	return strings.TrimRight(s.d.Cfg.APIURL, "/") + "/v1/support-channels/hooks/" + c.Provider + "/" + c.ID.String()
}
func (s *Service) listConnections(ctx context.Context, wid uuid.UUID) ([]connection, error) {
	rows, e := s.d.DB.Pool.Query(ctx, connectionSelect+` WHERE workspace_id=$1 ORDER BY created_at`, wid)
	if e != nil {
		return nil, e
	}
	list, e := pgx.CollectRows(rows, func(r pgx.CollectableRow) (connection, error) {
		var c connection
		e := r.Scan(connectionScan(&c)...)
		c.WebhookURL = s.webhookURL(c)
		return c, e
	})
	if list == nil {
		list = []connection{}
	}
	return list, e
}

func (s *Service) webhook(w http.ResponseWriter, r *http.Request) {
	id, e := uuid.Parse(chi.URLParam(r, "id"))
	if e != nil {
		http.NotFound(w, r)
		return
	}
	provider := chi.URLParam(r, "provider")
	c, e := s.loadConnection(r.Context(), id)
	if e != nil || c.Provider != provider {
		http.NotFound(w, r)
		return
	}
	a, ok := s.registry.Get(provider)
	if !ok {
		http.NotFound(w, r)
		return
	}
	body, e := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	if e != nil {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}
	cr, e := s.credentials(c)
	if e != nil {
		s.d.Log.Error("support channel credentials", "connection_id", c.ID, "err", e)
		w.WriteHeader(500)
		return
	}
	if e = a.Verify(r.Header, body, cr); e != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	result, e := a.Parse(r.Header, body)
	if e != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	e = s.persistWebhook(r.Context(), c, result)
	if e != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	s.markWebhookHealthy(r.Context(), c)
	if result.ContentType != "" {
		w.Header().Set("Content-Type", result.ContentType)
	}
	status := result.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	if len(result.Body) > 0 {
		_, _ = w.Write(result.Body)
	}
}

func (s *Service) persistWebhook(ctx context.Context, c connection, result channel.WebhookResult) error {
	if err := s.storeDiscovered(ctx, c, result.Destinations); err != nil {
		return err
	}
	return s.persistInbound(ctx, c, result.Events)
}

func (s *Service) persistInbound(ctx context.Context, c connection, items []channel.Inbound) error {
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		for i := range items {
			item := &items[i]
			if item.EventID == "" {
				return errors.New("support channel event has no id")
			}
			if item.AccountID == "" {
				item.AccountID = c.ExternalAccountID
			}
			raw, err := json.Marshal(item)
			if err != nil {
				return err
			}
			tag, err := tx.Exec(ctx, `INSERT INTO support_channel_inbound_events(connection_id,external_event_id,payload) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, c.ID, item.EventID, raw)
			if err != nil {
				return err
			}
			if tag.RowsAffected() > 0 {
				if _, err = s.d.Jobs.InsertTx(ctx, tx, jobs.SupportChannelInboundArgs{ConnectionID: c.ID, ExternalEventID: item.EventID}, &river.InsertOpts{MaxAttempts: 8}); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (s *Service) loadConnection(ctx context.Context, id uuid.UUID) (connection, error) {
	var c connection
	e := s.d.DB.Pool.QueryRow(ctx, connectionSelect+` WHERE id=$1`, id).Scan(connectionScan(&c)...)
	return c, e
}

const connectionSelect = `SELECT id,workspace_id,provider,name,external_account_id,external_account_name,credentials_enc,status,last_error,health_status,health_detail,health_checked_at,credentials_updated_at,created_at FROM support_channel_connections`

func connectionScan(c *connection) []any {
	return []any{&c.ID, &c.WorkspaceID, &c.Provider, &c.Name, &c.ExternalAccountID, &c.ExternalAccountName, &c.CredentialsEnc, &c.Status, &c.LastError, &c.HealthStatus, &c.HealthDetail, &c.HealthCheckedAt, &c.CredentialsUpdatedAt, &c.CreatedAt}
}
func (s *Service) credentials(c connection) (map[string]string, error) {
	out := map[string]string{}
	return out, s.d.Box.OpenJSON(c.CredentialsEnc, &out)
}
