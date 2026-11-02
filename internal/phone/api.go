package phone

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/phone/provider"
)

// Health codes the settings page words in the reader's language (health_code).
const (
	HealthReceiving   = "receiving"           // the webhook is registered and active
	HealthNoWebhook   = "webhook_missing"     // the provider has no webhook for opendesk (or disabled it)
	HealthRefused     = "credentials_refused" // the provider refused the credentials
	HealthUnreachable = "provider_unreachable"
)

var healthDetails = map[string]string{
	HealthReceiving:   "Calls are sent to opendesk.",
	HealthNoWebhook:   "The provider is not sending calls to opendesk. Test the connection to register the webhook again.",
	HealthRefused:     "The provider refused the credentials. Replace them with working ones.",
	HealthUnreachable: "Couldn't reach the provider. Try again in a moment.",
}

// manualDetails word the health of a connection whose webhook an admin adds in the
// provider by hand (Descriptor.ManualWebhook), where they differ.
var manualDetails = map[string]string{
	HealthNoWebhook: "No call has come in yet. Add the webhook URL in the provider, then make a test call.",
}

// healthDetail is how a provider's connection words a health code.
func (s *Service) healthDetail(providerKey, code string) string {
	if d, ok := manualDetails[code]; ok && s.describe(providerKey).ManualWebhook {
		return d
	}
	return healthDetails[code]
}

// PhoneProvider is a provider the instance can connect.
type PhoneProvider struct {
	Key    string               `json:"key"`
	Name   string               `json:"name"`
	Fields []PhoneProviderField `json:"fields"`
	// ManualWebhook: an admin adds the connection's webhook URL in the provider by hand.
	ManualWebhook bool `json:"manual_webhook"`
	// InsightCards: a ringing call can show the caller in the provider's app.
	InsightCards bool `json:"insight_cards"`
}

// PhoneProviderField is a credential the admin pastes.
type PhoneProviderField struct {
	Name   string `json:"name"`
	Label  string `json:"label"`
	Secret bool   `json:"secret"`
}

// PhoneConnectionView is a connection with the code its health detail is worded by.
type PhoneConnectionView struct {
	PhoneConnection
	HealthCode string `json:"health_code" enum:",receiving,webhook_missing,credentials_refused,provider_unreachable"`
}

type phoneConnectionList struct {
	Data []PhoneConnectionView `json:"data"`
}

// Register mounts the phone API and the providers' webhook.
func (s *Service) Register(api huma.API, r chi.Router) {
	tags := []string{"Phone"}
	huma.Register(api, huma.Operation{OperationID: "list-phone-providers", Method: http.MethodGet, Path: "/v1/phone/providers", Tags: tags, Summary: "List phone providers"},
		func(ctx context.Context, _ *struct{}) (*struct {
			Body struct {
				Data []PhoneProvider `json:"data"`
			}
		}, error) {
			if _, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeSettingsRead); err != nil {
				return nil, httpx.Wrap(err)
			}
			out := &struct {
				Body struct {
					Data []PhoneProvider `json:"data"`
				}
			}{}
			out.Body.Data = []PhoneProvider{}
			for _, d := range s.descriptors() {
				p := PhoneProvider{Key: d.Key, Name: d.Name, Fields: []PhoneProviderField{}, ManualWebhook: d.ManualWebhook, InsightCards: d.InsightCards}
				for _, f := range d.Fields {
					p.Fields = append(p.Fields, PhoneProviderField{Name: f.Name, Label: f.Label, Secret: f.Secret})
				}
				out.Body.Data = append(out.Body.Data, p)
			}
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "list-phone-connections", Method: http.MethodGet, Path: "/v1/phone/connections", Tags: tags, Summary: "List phone connections"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body phoneConnectionList }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			list, err := s.listConnections(ctx, p.WorkspaceID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out := &struct{ Body phoneConnectionList }{phoneConnectionList{Data: []PhoneConnectionView{}}}
			for _, c := range list {
				out.Body.Data = append(out.Body.Data, s.view(c, seesWebhook(p)))
			}
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "create-phone-connection", Method: http.MethodPost, Path: "/v1/phone/connections", Tags: tags, Summary: "Connect a phone provider", DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *struct {
			Body struct {
				Provider    string            `json:"provider"`
				Name        string            `json:"name,omitempty" maxLength:"80"`
				Credentials map[string]string `json:"credentials"`
			}
		}) (*struct{ Body PhoneConnectionView }, error) {
			p, err := requireAdminUser(ctx)
			if err != nil {
				return nil, err
			}
			a, ok := s.adapters[strings.TrimSpace(in.Body.Provider)]
			if !ok {
				return nil, httpx.Validation("Unknown phone provider.")
			}
			desc := a.Describe()
			creds := cleanCredentials(desc, in.Body.Credentials)
			if creds == nil {
				return nil, httpx.Validation("Provide every required credential.")
			}
			account, err := a.Validate(ctx, s.http, creds)
			if err != nil {
				s.d.Log.Info("phone connect refused", "provider", desc.Key, "err", err)
				return nil, credentialsRefused(err)
			}
			withAccount(creds, account)
			sealed, err := s.d.Box.SealJSON(creds)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			name := strings.TrimSpace(in.Body.Name)
			if name == "" {
				name = displayname.CleanOr(account.Name, displayname.Workspace, a.Describe().Name)
			} else if name, err = displayname.Validate("Name", name, displayname.Workspace, false); err != nil {
				return nil, httpx.Wrap(err)
			}
			c := PhoneConnection{ID: ids.New(), WorkspaceID: p.WorkspaceID, Provider: desc.Key, Name: name,
				AccountName: displayname.CleanOr(account.Name, displayname.Workspace, ""), Status: "connected", HealthStatus: "unknown",
				ReuseDays: 7, InsightCards: desc.InsightCards, CreatedAt: time.Now().UTC(), credentialsEnc: sealed}
			if desc.ManualWebhook {
				if c.webhookSecretEnc, err = s.newWebhookSecret(); err != nil {
					return nil, httpx.Wrap(err)
				}
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, `INSERT INTO phone_connections (id, workspace_id, provider, name, account_name, credentials_enc, created_by, created_at,
					insight_cards, webhook_secret_enc)
					VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, c.ID, c.WorkspaceID, c.Provider, c.Name, c.AccountName, c.credentialsEnc, p.UserID, c.CreatedAt,
					c.InsightCards, c.webhookSecretEnc); err != nil {
					return err
				}
				if _, err := events.Publish(ctx, tx, events.Event{WorkspaceID: p.WorkspaceID, Type: "phone_connection.created", Admins: true,
					Data: map[string]any{"phone_connection": s.view(c, true)}}); err != nil {
					return err
				}
				return audit.Log(ctx, tx, p, p.WorkspaceID, "phone.connected", "phone_connection", &c.ID, map[string]any{"provider": c.Provider})
			})
			if db.IsUniqueViolation(err, "") {
				return nil, httpx.Conflict("This workspace is already connected to that provider.")
			}
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if c, err = s.reconcile(ctx, c.WorkspaceID, c.ID); err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body PhoneConnectionView }{s.view(c, true)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "update-phone-connection", Method: http.MethodPatch, Path: "/v1/phone/connections/{id}", Tags: tags, Summary: "Update a phone connection"},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body struct {
				Name           *string           `json:"name,omitempty" maxLength:"80"`
				ReuseDays      *int              `json:"reuse_days,omitempty" minimum:"0" maximum:"90" doc:"A call goes on the caller's latest ticket solved within this many days when they have no open one; 0 opens a new ticket instead"`
				AssignAnswered *bool             `json:"assign_answered,omitempty" doc:"An answered call assigns an unassigned ticket to the member who took it"`
				InsightCards   *bool             `json:"insight_cards,omitempty" doc:"A ringing call shows the caller and their tickets in the provider's app"`
				Credentials    map[string]string `json:"credentials,omitempty" doc:"Replacement credentials; the webhook is checked with them"`
			}
		}) (*struct{ Body PhoneConnectionView }, error) {
			p, err := requireAdminUser(ctx)
			if err != nil {
				return nil, err
			}
			c, err := s.workspaceConnection(ctx, p.WorkspaceID, in.ID)
			if err != nil {
				return nil, err
			}
			b := in.Body
			changes := map[string]any{}
			var name string
			if b.Name != nil {
				if name, err = displayname.Validate("Name", strings.TrimSpace(*b.Name), displayname.Workspace, false); err != nil {
					return nil, httpx.Wrap(err)
				}
				changes["name"] = name
			}
			if b.ReuseDays != nil {
				changes["reuse_days"] = *b.ReuseDays
			}
			if b.AssignAnswered != nil {
				changes["assign_answered"] = *b.AssignAnswered
			}
			if b.InsightCards != nil {
				changes["insight_cards"] = *b.InsightCards
			}
			var creds map[string]string
			var sealed []byte
			a, known := s.adapters[c.Provider]
			if len(b.Credentials) > 0 {
				if !known {
					return nil, httpx.Validation("Unknown phone provider.")
				}
				if creds = cleanCredentials(a.Describe(), b.Credentials); creds == nil {
					return nil, httpx.Validation("Provide every required credential.")
				}
				account, err := a.Validate(ctx, s.http, creds)
				if err != nil {
					return nil, credentialsRefused(err)
				}
				withAccount(creds, account)
				if sealed, err = s.d.Box.SealJSON(creds); err != nil {
					return nil, httpx.Wrap(err)
				}
				changes["credentials"] = "replaced"
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				// Changes apply to the connection as stored now, under reconcile's lock, so a
				// test or another change running at the same time never sees a stale webhook.
				cur, err := s.lockConnection(ctx, tx, p.WorkspaceID, c.ID)
				if err != nil {
					return err
				}
				if b.Name != nil {
					cur.Name = name
				}
				if b.ReuseDays != nil {
					cur.ReuseDays = *b.ReuseDays
				}
				if b.AssignAnswered != nil {
					cur.AssignAnswered = *b.AssignAnswered
				}
				if b.InsightCards != nil {
					cur.InsightCards = *b.InsightCards
				}
				if creds != nil {
					old, err := s.credentials(cur)
					if err != nil {
						return err
					}
					if a.Describe().ManualWebhook {
						// Another account's events need a URL of their own: the old account's
						// notification stops being accepted, and the admin adds the new URL.
						if old[provider.AccountIDKey] != creds[provider.AccountIDKey] {
							if cur.webhookSecretEnc, err = s.newWebhookSecret(); err != nil {
								return err
							}
							cur.LastEventAt = nil
						}
					} else if s.leaveOldAccount(ctx, a, cur, old, creds) {
						cur.webhookID, cur.webhookSecretEnc = "", nil
					}
					cur.credentialsEnc = sealed
				}
				if _, err := tx.Exec(ctx, `UPDATE phone_connections SET name = $3, reuse_days = $4, assign_answered = $5, insight_cards = $6,
					credentials_enc = $7, webhook_id = $8, webhook_secret_enc = $9, last_event_at = $10, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
					p.WorkspaceID, cur.ID, cur.Name, cur.ReuseDays, cur.AssignAnswered, cur.InsightCards, cur.credentialsEnc,
					cur.webhookID, cur.webhookSecretEnc, cur.LastEventAt); err != nil {
					return err
				}
				if _, err := events.Publish(ctx, tx, events.Event{WorkspaceID: p.WorkspaceID, Type: "phone_connection.updated", Admins: true,
					Data: map[string]any{"phone_connection": s.view(cur, true)}}); err != nil {
					return err
				}
				action := "phone.updated"
				if creds != nil {
					action = "phone.credentials_rotated"
				}
				c = cur
				return audit.Log(ctx, tx, p, p.WorkspaceID, action, "phone_connection", &c.ID, changes)
			})
			if db.IsNotFound(err) {
				return nil, httpx.NotFound("Phone connection")
			}
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if creds != nil {
				if c, err = s.reconcile(ctx, c.WorkspaceID, c.ID); err != nil {
					return nil, httpx.Wrap(err)
				}
			}
			return &struct{ Body PhoneConnectionView }{s.view(c, true)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "test-phone-connection", Method: http.MethodPost, Path: "/v1/phone/connections/{id}/test", Tags: tags,
		Summary: "Test and repair a phone connection", Description: "Checks the credentials and the provider's webhook, and registers the webhook again when the provider no longer has it. For a provider whose webhook an admin adds by hand, the webhook counts as working once an event came in at its URL."},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct{ Body PhoneConnectionView }, error) {
			p, err := requireAdminUser(ctx)
			if err != nil {
				return nil, err
			}
			c, err := s.workspaceConnection(ctx, p.WorkspaceID, in.ID)
			if err != nil {
				return nil, err
			}
			if c, err = s.reconcile(ctx, c.WorkspaceID, c.ID); err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body PhoneConnectionView }{s.view(c, true)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "rotate-phone-webhook", Method: http.MethodPost, Path: "/v1/phone/connections/{id}/rotate-webhook", Tags: tags,
		Summary: "Replace a phone connection's webhook URL", Description: "For a provider whose webhook an admin adds by hand: gives the connection a webhook URL with a new secret. The old URL stops working at once, so the new one replaces it in the provider."},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct{ Body PhoneConnectionView }, error) {
			p, err := requireAdminUser(ctx)
			if err != nil {
				return nil, err
			}
			c, err := s.workspaceConnection(ctx, p.WorkspaceID, in.ID)
			if err != nil {
				return nil, err
			}
			if !s.describe(c.Provider).ManualWebhook {
				return nil, httpx.Validation("opendesk registers this provider's webhook itself. Test the connection to repair it.")
			}
			secret, err := s.newWebhookSecret()
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if _, err := s.lockConnection(ctx, tx, p.WorkspaceID, c.ID); err != nil {
					return err
				}
				// No event has come in at the new URL yet.
				if _, err := tx.Exec(ctx, `UPDATE phone_connections SET webhook_secret_enc = $3, last_event_at = NULL, updated_at = now()
					WHERE workspace_id = $1 AND id = $2`, p.WorkspaceID, c.ID, secret); err != nil {
					return err
				}
				return audit.Log(ctx, tx, p, p.WorkspaceID, "phone.webhook_rotated", "phone_connection", &c.ID, map[string]any{"provider": c.Provider})
			})
			if db.IsNotFound(err) {
				return nil, httpx.NotFound("Phone connection")
			}
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if c, err = s.reconcile(ctx, c.WorkspaceID, c.ID); err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body PhoneConnectionView }{s.view(c, true)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "delete-phone-connection", Method: http.MethodDelete, Path: "/v1/phone/connections/{id}", Tags: tags,
		Summary: "Disconnect a phone provider", Description: "Removes opendesk's webhook from the provider (one an admin added by hand is theirs to remove) and the connection. Calls already on tickets stay.", DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct{}, error) {
			p, err := requireAdminUser(ctx)
			if err != nil {
				return nil, err
			}
			c, err := s.workspaceConnection(ctx, p.WorkspaceID, in.ID)
			if err != nil {
				return nil, err
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				// Under reconcile's lock, so no webhook it registers outlives the connection.
				cur, err := s.lockConnection(ctx, tx, p.WorkspaceID, c.ID)
				if err != nil {
					return err
				}
				if a, ok := s.adapters[cur.Provider]; ok && cur.webhookID != "" {
					if creds, err := s.credentials(cur); err == nil {
						if err := a.DeleteWebhook(ctx, s.http, creds, cur.webhookID); err != nil {
							// The webhook stays at the provider, which disables it once its events are refused.
							s.d.Log.Warn("phone webhook not removed", "provider", cur.Provider, "connection_id", cur.ID, "err", err)
						}
					}
				}
				s.lineCache.Delete(c.ID)
				tag, err := tx.Exec(ctx, `DELETE FROM phone_connections WHERE workspace_id = $1 AND id = $2`, p.WorkspaceID, c.ID)
				if err != nil {
					return err
				}
				if tag.RowsAffected() == 0 {
					return pgx.ErrNoRows
				}
				if _, err := events.Publish(ctx, tx, events.Event{WorkspaceID: p.WorkspaceID, Type: "phone_connection.deleted", Admins: true,
					Data: map[string]any{"phone_connection_id": c.ID}}); err != nil {
					return err
				}
				return audit.Log(ctx, tx, p, p.WorkspaceID, "phone.disconnected", "phone_connection", &c.ID, map[string]any{"provider": c.Provider})
			})
			if db.IsNotFound(err) {
				return nil, httpx.NotFound("Phone connection")
			}
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{}{}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "list-phone-lines", Method: http.MethodGet, Path: "/v1/phone/lines", Tags: tags,
		Summary: "List phone lines", Description: "The numbers of every connected provider account, with the lines calls already came in on, for automation conditions."},
		func(ctx context.Context, _ *struct{}) (*struct {
			Body struct {
				Data []PhoneLine `json:"data"`
			}
		}, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			lines, err := s.lines(ctx, p.WorkspaceID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out := &struct {
				Body struct {
					Data []PhoneLine `json:"data"`
				}
			}{}
			out.Body.Data = lines
			return out, nil
		})

	r.Post("/v1/phone/hooks/{provider}/{id}", s.webhook)
	// A webhook an admin adds by hand carries its secret in the path (Descriptor.ManualWebhook).
	r.Post("/v1/phone/hooks/{provider}/{id}/{secret}", s.webhook)
}

// PhoneLine is a provider number calls come in on.
type PhoneLine struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
}

// lines lists each connection's numbers from the provider, and the lines seen on calls,
// so the list still shows them when the provider can't be reached.
func (s *Service) lines(ctx context.Context, wid uuid.UUID) ([]PhoneLine, error) {
	out := []PhoneLine{}
	seen := map[string]bool{}
	// A line is known by its id, the value of call_line conditions; one without an id
	// can't be picked.
	add := func(l PhoneLine) {
		key := l.Provider + "\x00" + l.ID
		if l.ID != "" && l.Name != "" && !seen[key] {
			seen[key] = true
			out = append(out, l)
		}
	}
	conns, err := s.listConnections(ctx, wid)
	if err != nil {
		return nil, err
	}
	for _, c := range conns {
		a, ok := s.adapters[c.Provider]
		if !ok {
			continue
		}
		list, err := s.providerLines(ctx, a, c)
		if err != nil {
			s.d.Log.Info("phone lines not listed", "provider", c.Provider, "connection_id", c.ID, "err", err)
			continue
		}
		for _, l := range list {
			add(PhoneLine{ID: l.ID, Name: displayname.CleanOr(l.Name, displayname.Workspace, l.Name), Provider: c.Provider})
		}
	}
	rows, err := s.d.DB.Pool.Query(ctx, `SELECT DISTINCT ON (provider, line_id) line_id, line_name, provider FROM phone_calls
		WHERE workspace_id = $1 AND line_id <> '' AND line_name <> '' ORDER BY provider, line_id, created_at DESC LIMIT 200`, wid)
	if err != nil {
		return nil, err
	}
	seenRows, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (PhoneLine, error) {
		var l PhoneLine
		return l, r.Scan(&l.ID, &l.Name, &l.Provider)
	})
	if err != nil {
		return nil, err
	}
	for _, l := range seenRows {
		add(l)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// providerLines is the connection's numbers at the provider, listed at most once every
// linesTTL per connection and set of credentials.
func (s *Service) providerLines(ctx context.Context, a provider.Adapter, c PhoneConnection) ([]provider.Line, error) {
	key := sha256.Sum256(c.credentialsEnc)
	if v, ok := s.lineCache.Load(c.ID); ok {
		if e := v.(cachedLines); e.creds == key && time.Since(e.at) < linesTTL {
			return e.lines, nil
		}
	}
	creds, err := s.credentials(c)
	if err != nil {
		return nil, err
	}
	list, err := a.Lines(ctx, s.http, creds)
	if err != nil {
		return nil, err
	}
	s.lineCache.Store(c.ID, cachedLines{creds: key, at: time.Now(), lines: list})
	return list, nil
}

// requireAdminUser admits a workspace admin signed in as a person: connecting a phone
// system hands it a way into tickets, so API keys can't.
func requireAdminUser(ctx context.Context) (*auth.Principal, error) {
	p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
	if err != nil {
		return nil, httpx.Wrap(err)
	}
	if p.Kind != auth.KindUser {
		return nil, httpx.Forbidden()
	}
	return p, nil
}

// credentialsRefused is the answer to credentials Validate refused: the provider's own
// reason when it gave one the admin can act on.
func credentialsRefused(err error) error {
	var ar *provider.AccountRefused
	if errors.As(err, &ar) {
		return httpx.E(422, "phone_credentials_refused", ar.Detail)
	}
	return httpx.E(422, "phone_credentials_refused", "The provider refused these credentials.")
}

// withAccount keeps the account's id with the credentials, for the adapter's requests.
func withAccount(creds map[string]string, a provider.Account) {
	if a.ID != "" {
		creds[provider.AccountIDKey] = a.ID
	}
}

// newWebhookSecret is a sealed secret for a webhook URL an admin adds by hand: 32 random
// bytes, base64url, so it is one path segment.
func (s *Service) newWebhookSecret() ([]byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return s.d.Box.Seal([]byte(base64.RawURLEncoding.EncodeToString(b)))
}

func cleanCredentials(d provider.Descriptor, in map[string]string) map[string]string {
	out := map[string]string{}
	for _, f := range d.Fields {
		v := strings.TrimSpace(in[f.Name])
		if v == "" || len(v) > 500 {
			return nil
		}
		out[f.Name] = v
	}
	return out
}

// webhookURL is where the provider sends the connection's events; one an admin adds by
// hand ends with the webhook's secret, "" when it can't be read.
func (s *Service) webhookURL(c PhoneConnection) string {
	u := strings.TrimRight(s.d.Cfg.APIURL, "/") + "/v1/phone/hooks/" + c.Provider + "/" + c.ID.String()
	if s.describe(c.Provider).ManualWebhook {
		secret, err := s.d.Box.Open(c.webhookSecretEnc)
		if err != nil || len(secret) == 0 {
			return ""
		}
		u += "/" + string(secret)
	}
	return u
}

// seesWebhook reports whether p may read a connection's webhook URL: an admin, or an API
// key or AI client that may change settings.
func seesWebhook(p *auth.Principal) bool {
	switch p.Kind {
	case auth.KindAPIKey:
		return p.HasScope(auth.ScopeSettingsWrite)
	case auth.KindUser:
		return p.Role.AtLeast(auth.RoleAdmin) && (p.GrantID == uuid.Nil || p.HasScope(auth.ScopeSettingsWrite))
	}
	return false
}

// view is a connection as a response or an event carries it, with its webhook URL only
// for admins.
func (s *Service) view(c PhoneConnection, admin bool) PhoneConnectionView {
	c.WebhookURL = ""
	if admin {
		c.WebhookURL = s.webhookURL(c)
	}
	v := PhoneConnectionView{PhoneConnection: c}
	for _, details := range []map[string]string{healthDetails, manualDetails} {
		for code, detail := range details {
			if detail == c.HealthDetail {
				v.HealthCode = code
			}
		}
	}
	return v
}

const connectionSelect = `SELECT id, workspace_id, provider, name, account_name, credentials_enc, webhook_id, webhook_secret_enc,
	status, last_error, health_status, health_detail, health_checked_at, reuse_days, assign_answered, insight_cards, last_event_at, created_at
	FROM phone_connections`

func scanConnection(row pgx.Row) (PhoneConnection, error) {
	var c PhoneConnection
	err := row.Scan(&c.ID, &c.WorkspaceID, &c.Provider, &c.Name, &c.AccountName, &c.credentialsEnc, &c.webhookID, &c.webhookSecretEnc,
		&c.Status, &c.LastError, &c.HealthStatus, &c.HealthDetail, &c.HealthCheckedAt, &c.ReuseDays, &c.AssignAnswered, &c.InsightCards,
		&c.LastEventAt, &c.CreatedAt)
	return c, err
}

func (s *Service) listConnections(ctx context.Context, wid uuid.UUID) ([]PhoneConnection, error) {
	rows, err := s.d.DB.Pool.Query(ctx, connectionSelect+` WHERE workspace_id = $1 ORDER BY created_at`, wid)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (PhoneConnection, error) { return scanConnection(r) })
}

func (s *Service) workspaceConnection(ctx context.Context, wid uuid.UUID, raw string) (PhoneConnection, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return PhoneConnection{}, httpx.NotFound("Phone connection")
	}
	c, err := scanConnection(s.d.DB.Pool.QueryRow(ctx, connectionSelect+` WHERE workspace_id = $1 AND id = $2`, wid, id))
	if db.IsNotFound(err) {
		return c, httpx.NotFound("Phone connection")
	}
	if err != nil {
		return c, httpx.Wrap(err)
	}
	return c, nil
}

func (s *Service) credentials(c PhoneConnection) (map[string]string, error) {
	out := map[string]string{}
	return out, s.d.Box.OpenJSON(c.credentialsEnc, &out)
}

// lockConnection serializes the work on one connection that talks to the provider
// (reconcile, a credentials change, disconnecting) for the rest of tx, and reads the
// connection afresh. It is an advisory lock rather than a row lock: incoming events
// write the row (last_event_at) and must not wait on a provider round trip.
func (s *Service) lockConnection(ctx context.Context, tx pgx.Tx, wid, id uuid.UUID) (PhoneConnection, error) {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "phone-connection:"+id.String()); err != nil {
		return PhoneConnection{}, err
	}
	return scanConnection(tx.QueryRow(ctx, connectionSelect+` WHERE workspace_id = $1 AND id = $2`, wid, id))
}

// reconcile checks the credentials and the provider's webhook, registers the webhook when
// the provider has none for this connection, and stores the outcome as the health. One
// reconcile runs at a time per connection, on what is stored when it starts, so two at
// once never register two webhooks.
func (s *Service) reconcile(ctx context.Context, wid, id uuid.UUID) (PhoneConnection, error) {
	var out PhoneConnection
	err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		c, err := s.lockConnection(ctx, tx, wid, id)
		if err != nil {
			return err
		}
		out, err = s.reconcileLocked(ctx, tx, c)
		return err
	})
	return out, err
}

func (s *Service) reconcileLocked(ctx context.Context, tx pgx.Tx, c PhoneConnection) (PhoneConnection, error) {
	a, ok := s.adapters[c.Provider]
	if !ok {
		return s.storeHealth(ctx, tx, c, "error", HealthUnreachable)
	}
	creds, err := s.credentials(c)
	if err != nil {
		return c, err
	}
	account, err := a.Validate(ctx, s.http, creds)
	if err != nil {
		s.d.Log.Info("phone credentials check failed", "provider", c.Provider, "connection_id", c.ID, "err", err)
		if provider.Refused(err) {
			return s.storeHealth(ctx, tx, c, "error", HealthRefused)
		}
		return s.storeHealth(ctx, tx, c, "error", HealthUnreachable)
	}
	if name := displayname.CleanOr(account.Name, displayname.Workspace, ""); name != "" {
		c.AccountName = name
	}
	if a.Describe().ManualWebhook {
		// There is no API to ask about the webhook: it works once an event came in at its
		// URL (the webhook marks the connection receiving then).
		if c.LastEventAt != nil {
			return s.storeHealth(ctx, tx, c, "healthy", HealthReceiving)
		}
		return s.storeHealth(ctx, tx, c, "action_required", HealthNoWebhook)
	}
	if c.webhookID != "" {
		active, err := a.WebhookActive(ctx, s.http, creds, c.webhookID)
		if err != nil {
			s.d.Log.Info("phone webhook check failed", "provider", c.Provider, "connection_id", c.ID, "err", err)
			return s.storeHealth(ctx, tx, c, "error", HealthUnreachable)
		}
		if active {
			return s.storeHealth(ctx, tx, c, "healthy", HealthReceiving)
		}
		// Gone or disabled by the provider: replace it.
		if err := a.DeleteWebhook(ctx, s.http, creds, c.webhookID); err != nil {
			s.d.Log.Info("phone webhook not removed", "provider", c.Provider, "connection_id", c.ID, "err", err)
		}
	}
	hook, err := a.RegisterWebhook(ctx, s.http, creds, s.webhookURL(c))
	if err != nil {
		s.d.Log.Warn("phone webhook not registered", "provider", c.Provider, "connection_id", c.ID, "err", err)
		return s.storeHealth(ctx, tx, c, "action_required", HealthNoWebhook)
	}
	secret, err := s.d.Box.Seal([]byte(hook.Secret))
	if err != nil {
		return c, err
	}
	if _, err := tx.Exec(ctx, `UPDATE phone_connections SET webhook_id = $3, webhook_secret_enc = $4, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, c.WorkspaceID, c.ID, hook.ID, secret); err != nil {
		return c, err
	}
	c.webhookID, c.webhookSecretEnc = hook.ID, secret
	return s.storeHealth(ctx, tx, c, "healthy", HealthReceiving)
}

// leaveOldAccount runs when a connection's credentials are replaced. Credentials of another
// provider account can't see opendesk's webhook in the old one: it is removed there with
// the old credentials, while they are still at hand, so the old account stops sending
// calls, and leaveOldAccount reports true for reconcile to register one in the new
// account. On the same account the webhook stays. A provider that can't be reached leaves
// it to reconcile.
func (s *Service) leaveOldAccount(ctx context.Context, a provider.Adapter, c PhoneConnection, oldCreds, newCreds map[string]string) bool {
	if c.webhookID == "" {
		return false
	}
	ours, err := a.WebhookActive(ctx, s.http, newCreds, c.webhookID)
	if err != nil {
		s.d.Log.Info("phone webhook check failed", "provider", c.Provider, "connection_id", c.ID, "err", err)
		return false
	}
	if ours {
		return false
	}
	if err := a.DeleteWebhook(ctx, s.http, oldCreds, c.webhookID); err != nil {
		// The old account disables it once its events are refused.
		s.d.Log.Warn("phone webhook not removed from the old account", "provider", c.Provider, "connection_id", c.ID, "err", err)
	}
	return true
}

func (s *Service) storeHealth(ctx context.Context, tx pgx.Tx, c PhoneConnection, status, code string) (PhoneConnection, error) {
	now := time.Now().UTC()
	c.HealthStatus, c.HealthDetail, c.HealthCheckedAt = status, s.healthDetail(c.Provider, code), &now
	c.Status, c.LastError = "connected", ""
	if status == "error" {
		c.Status = "error"
	}
	tag, err := tx.Exec(ctx, `UPDATE phone_connections SET status = $3, last_error = '', health_status = $4, health_detail = $5,
		health_checked_at = $6, account_name = $7, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
		c.WorkspaceID, c.ID, c.Status, status, c.HealthDetail, now, c.AccountName)
	if err != nil || tag.RowsAffected() == 0 {
		return c, err
	}
	_, err = events.Publish(ctx, tx, events.Event{WorkspaceID: c.WorkspaceID, Type: "phone_connection.updated", Admins: true,
		Data: map[string]any{"phone_connection": s.view(c, true)}})
	return c, err
}
