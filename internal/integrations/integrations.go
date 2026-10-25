// Package integrations connects a workspace to remote MCP servers (Linear, GitHub,
// Jira, Slack, ...) so Ollie can use their tools (contracts/integrations.md). An admin
// connects a server once for the workspace; reads run directly and every other tool
// waits for the user's approval in the assistant. Credentials are sealed with d.Box,
// every request goes through the SSRF-guarded client, and connects and disconnects are
// audit-logged.
package integrations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/images"
	"github.com/useopendesk/opendesk/internal/netsafe"
)

// Service owns connections and their MCP sessions.
type Service struct {
	d     *deps.Deps
	http  *http.Client
	guard netsafe.Guard

	mu       sync.Mutex
	sessions map[uuid.UUID]*session
}

var (
	servicesMu sync.Mutex
	services   = map[*deps.Deps]*Service{}
)

// For returns the workspace-wide service for d (one per process).
func For(d *deps.Deps) *Service {
	servicesMu.Lock()
	defer servicesMu.Unlock()
	if s := services[d]; s != nil {
		return s
	}
	dev := !d.Cfg.IsProduction()
	s := newService(d, guardedClient(dev), dev)
	services[d] = s
	return s
}

func newService(d *deps.Deps, c *http.Client, dev bool) *Service {
	return &Service{d: d, http: c, guard: netsafe.Guard{Dev: dev}, sessions: map[uuid.UUID]*session{}}
}

// guardedClient is netsafe's client with room for slow tools: the guarded dialer, no
// proxy, no redirects, a minute for the whole exchange.
func guardedClient(dev bool) *http.Client {
	g := netsafe.Guard{Dev: dev}
	d := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			Proxy: nil, DialContext: g.DialContext(d), ForceAttemptHTTP2: true,
			MaxIdleConns: 50, MaxIdleConnsPerHost: 4, IdleConnTimeout: 90 * time.Second,
			TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 50 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Register adds the integration and ticket link routes.
func Register(d *deps.Deps, api huma.API, r chi.Router) {
	s := For(d)
	s.registerRoutes(api, r)
	s.registerLinks(api)
}

// RegisterWorkers adds the icon lookup of custom servers.
func RegisterWorkers(d *deps.Deps, w *river.Workers) {
	river.AddWorker(w, &iconWorker{s: For(d), f: images.NewFetcher(d)})
}

// Periodic and Background: integrations have none.
func Periodic(*deps.Deps) []*river.PeriodicJob { return nil }
func Background(context.Context, *deps.Deps)   {}

// --- storage --------------------------------------------------------------------------

// StoredTool is a server's tool as kept in integrations.tools.
type StoredTool struct {
	Name        string          `json:"name"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
	// ReadOnly is the server's own readOnlyHint; whether Ollie runs the tool without
	// approval is row.readOnly, which also honours the admin's ask_tools.
	ReadOnly bool `json:"read_only"`
}

type row struct {
	ID             uuid.UUID
	WorkspaceID    uuid.UUID
	Provider       string
	Slug           string
	Name           string
	URL            string
	AuthType       string
	Status         string
	CredentialsEnc []byte
	OAuthEnc       []byte
	Tools          []StoredTool
	DisabledTools  []string
	AskTools       []string
	AutoTools      []string
	Enabled        bool
	LastError      string
	Icon           domain.Avatar
	SyncedAt       *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

const rowSelect = `SELECT id, workspace_id, provider, slug, name, url, auth_type, status, credentials_enc, oauth_enc, tools,
	disabled_tools, ask_tools, auto_tools, enabled, last_error, icon, synced_at, created_at, updated_at FROM integrations`

func scanRow(r pgx.Row) (*row, error) {
	var x row
	var tools []byte
	if err := r.Scan(&x.ID, &x.WorkspaceID, &x.Provider, &x.Slug, &x.Name, &x.URL, &x.AuthType, &x.Status, &x.CredentialsEnc,
		&x.OAuthEnc, &tools, &x.DisabledTools, &x.AskTools, &x.AutoTools, &x.Enabled, &x.LastError, &x.Icon, &x.SyncedAt, &x.CreatedAt, &x.UpdatedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(tools, &x.Tools)
	return &x, nil
}

// readOnly says whether a tool runs without approval: its server marks it read-only
// and no admin asked for it to wait for approval.
func (x *row) readOnly(t StoredTool) bool {
	return t.ReadOnly && !slices.Contains(x.AskTools, t.Name)
}

// autoRun says whether a write runs without the user's approval: an admin listed it in
// auto_tools, and it is a write that is not also set to ask first (ask_tools wins).
// A read-only tool needs no approval anyway, so it is never counted as auto-run.
func (x *row) autoRun(t StoredTool) bool {
	return !x.readOnly(t) && slices.Contains(x.AutoTools, t.Name) && !slices.Contains(x.AskTools, t.Name)
}

// knownTools keeps the names in list that are tools of the connection, once each.
func (x *row) knownTools(list []string) []string {
	out := []string{}
	for _, n := range list {
		if slices.ContainsFunc(x.Tools, func(t StoredTool) bool { return t.Name == n }) && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

func loadRow(ctx context.Context, q db.Querier, wid, id uuid.UUID) (*row, error) {
	return scanRow(q.QueryRow(ctx, rowSelect+` WHERE workspace_id = $1 AND id = $2`, wid, id))
}

func listRows(ctx context.Context, q db.Querier, wid uuid.UUID) ([]*row, error) {
	rows, err := q.Query(ctx, rowSelect+` WHERE workspace_id = $1 ORDER BY created_at, id`, wid)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (*row, error) { return scanRow(r) })
}

func (s *Service) openTokens(x *row) (tokens, error) {
	var t tokens
	if len(x.CredentialsEnc) == 0 {
		return t, nil
	}
	err := s.d.Box.OpenJSON(x.CredentialsEnc, &t)
	return t, err
}

func (s *Service) openClient(x *row) (*oauthClient, error) {
	if len(x.OAuthEnc) == 0 {
		return nil, errReauthorize
	}
	var c oauthClient
	if err := s.d.Box.OpenJSON(x.OAuthEnc, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// bearer is a usable access token for the connection, refreshed under a row lock so
// concurrent calls refresh once.
func (s *Service) bearer(ctx context.Context, x *row, force bool) (string, error) {
	if x.AuthType == "none" {
		return "", nil
	}
	t, err := s.openTokens(x)
	if err != nil {
		return "", err
	}
	if x.AuthType == "token" || (!force && t.fresh(time.Now())) {
		if t.bearer() == "" {
			return "", errReauthorize
		}
		return t.bearer(), nil
	}
	var out string
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		locked, err := scanRow(tx.QueryRow(ctx, rowSelect+` WHERE workspace_id = $1 AND id = $2 FOR UPDATE`, x.WorkspaceID, x.ID))
		if err != nil {
			return err
		}
		cur, err := s.openTokens(locked)
		if err != nil {
			return err
		}
		if cur.AccessToken != t.AccessToken && cur.fresh(time.Now()) {
			out = cur.AccessToken // another call refreshed it
			return nil
		}
		if cur.RefreshToken == "" {
			return errReauthorize
		}
		c, err := s.openClient(locked)
		if err != nil {
			return err
		}
		next, err := s.refresh(ctx, c, cur.RefreshToken)
		if err != nil {
			return err
		}
		sealed, err := s.d.Box.SealJSON(next)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE integrations SET credentials_enc = $3, updated_at = now() WHERE workspace_id = $1 AND id = $2`, x.WorkspaceID, x.ID, sealed); err != nil {
			return err
		}
		x.CredentialsEnc = sealed
		out = next.AccessToken
		return nil
	})
	if errors.Is(err, errReauthorize) {
		s.markError(context.WithoutCancel(ctx), x, err.Error())
	}
	return out, err
}

func (s *Service) markError(ctx context.Context, x *row, msg string) {
	_, _ = s.d.DB.Pool.Exec(ctx, `UPDATE integrations SET status = 'error', last_error = $3, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
		x.WorkspaceID, x.ID, clip(msg, 300))
	x.Status, x.LastError = "error", clip(msg, 300)
	s.forget(x.ID)
}

// --- sessions ---------------------------------------------------------------------------

const sessionIdle = 5 * time.Minute

func (s *Service) forget(id uuid.UUID) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

// sessionFor reuses a recent MCP session for the connection, or opens one.
func (s *Service) sessionFor(ctx context.Context, x *row, force bool) (*session, error) {
	tok, err := s.bearer(ctx, x, force)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	cur := s.sessions[x.ID]
	s.mu.Unlock()
	if cur != nil && cur.bearer == tok && !force {
		cur.mu.Lock()
		idle := time.Since(cur.used)
		cur.mu.Unlock()
		if idle < sessionIdle {
			return cur, nil
		}
	}
	sess := newSession(s.http, x.URL, tok)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := sess.open(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.sessions[x.ID] = sess
	s.mu.Unlock()
	return sess, nil
}

// withSession runs fn on a session, signing in again or reopening once when the
// server asks for it.
func (s *Service) withSession(ctx context.Context, x *row, fn func(*session) error) error {
	sess, err := s.sessionFor(ctx, x, false)
	if err == nil {
		err = fn(sess)
	}
	var ua *errUnauthorized
	switch {
	case errors.As(err, &ua) && x.AuthType == "oauth":
		s.forget(x.ID)
		if sess, err = s.sessionFor(ctx, x, true); err == nil {
			err = fn(sess)
		}
	case errors.Is(err, errSessionGone):
		s.forget(x.ID)
		if sess, err = s.sessionFor(ctx, x, false); err == nil {
			err = fn(sess)
		}
	}
	if errors.As(err, &ua) {
		err = errors.New("the server refused the credentials. Reconnect the integration")
		s.markError(context.WithoutCancel(ctx), x, err.Error())
	}
	return err
}

// sync lists the server's tools and stores them; the connection is connected after.
func (s *Service) sync(ctx context.Context, x *row) error {
	var remote []RemoteTool
	err := s.withSession(ctx, x, func(sess *session) error {
		ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		var err error
		remote, err = sess.ListTools(ctx)
		return err
	})
	if err != nil {
		if x.Status != "error" {
			s.markError(context.WithoutCancel(ctx), x, err.Error())
		}
		return err
	}
	x.Tools = storedTools(remote)
	raw, _ := json.Marshal(x.Tools)
	now := time.Now().UTC()
	_, err = s.d.DB.Pool.Exec(ctx, `UPDATE integrations SET tools = $3, status = 'connected', last_error = '', synced_at = $4,
		updated_at = now() WHERE workspace_id = $1 AND id = $2`, x.WorkspaceID, x.ID, raw, now)
	x.Status, x.LastError, x.SyncedAt = "connected", "", &now
	return err
}

// maxSchema bounds one tool's input schema; larger ones are left out.
const maxSchema = 32 << 10

func storedTools(in []RemoteTool) []StoredTool {
	out := make([]StoredTool, 0, len(in))
	seen := map[string]bool{}
	for _, t := range in {
		name := strings.TrimSpace(t.Name)
		if name == "" || len(name) > 128 || seen[name] || len(t.InputSchema) > maxSchema {
			continue
		}
		seen[name] = true
		title := t.Title
		if title == "" && t.Annotations != nil {
			title = t.Annotations.Title
		}
		schema := t.InputSchema
		var probe map[string]any
		if json.Unmarshal(schema, &probe) != nil || probe == nil {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		out = append(out, StoredTool{Name: name, Title: clip(title, 120), Description: clip(t.Description, 1024),
			InputSchema: schema, ReadOnly: t.Annotations != nil && t.Annotations.ReadOnlyHint != nil && *t.Annotations.ReadOnlyHint})
	}
	return out
}

// --- Ollie's view -------------------------------------------------------------------------

// Tool is one connected tool as the assistant offers it to the model.
type Tool struct {
	// Name is unique across a workspace's tools: "<slug>__<tool>".
	Name          string
	Remote        string
	IntegrationID uuid.UUID
	Integration   string // the connection's name, for previews ("Linear")
	Provider      string
	Title         string
	Description   string
	Schema        map[string]any
	// ReadOnly: the tool runs without approval (its server marks it read-only and no
	// admin asked for approval first).
	ReadOnly bool
	// AutoRun: a write an admin lets run without the user's approval (auto_tools, and
	// not on ask_tools). Call checks it again when the call runs.
	AutoRun bool
	Hosts   []string
}

// CallMode is why a call may run: as a read, approved by the user, or as an auto-run write.
type CallMode int

const (
	// CallRead runs without approval and only while the tool is read-only.
	CallRead CallMode = iota
	// CallApproved runs a call the user approved.
	CallApproved
	// CallAuto runs without approval and only while the tool is set to run without asking.
	CallAuto
)

// maxOffered bounds the connected tools offered to the model in one run.
const maxOffered = 120

// Tools are the workspace's enabled tools on connected integrations.
func (s *Service) Tools(ctx context.Context, wid uuid.UUID) ([]Tool, error) {
	rows, err := listRows(ctx, s.d.DB.Pool, wid)
	if err != nil {
		return nil, err
	}
	var out []Tool
	for _, x := range rows {
		if !x.Enabled || x.Status != "connected" {
			continue
		}
		prov, _ := provider(x.Provider)
		for _, t := range x.Tools {
			if slices.Contains(x.DisabledTools, t.Name) || len(out) >= maxOffered {
				continue
			}
			var schema map[string]any
			_ = json.Unmarshal(t.InputSchema, &schema)
			if schema == nil {
				schema = map[string]any{}
			}
			schema["type"] = "object"
			if _, ok := schema["properties"]; !ok {
				schema["properties"] = map[string]any{}
			}
			desc := t.Description
			if desc == "" {
				desc = t.Title
			}
			out = append(out, Tool{Name: toolName(x.Slug, t.Name), Remote: t.Name, IntegrationID: x.ID, Integration: x.Name,
				Provider: x.Provider, Title: firstNonEmpty(t.Title, humanize(t.Name)),
				Description: clip("["+x.Name+"] "+desc, 1100), Schema: schema, ReadOnly: x.readOnly(t), AutoRun: x.autoRun(t), Hosts: prov.Hosts})
		}
	}
	return out, nil
}

// Call runs one tool on its server. A call without the user's approval (CallRead,
// CallAuto) runs only while the workspace has the tool that way now: a tool an admin set
// to ask first never runs as a read, and one taken off auto_tools never runs on its own.
func (s *Service) Call(ctx context.Context, wid, integrationID uuid.UUID, remote string, args json.RawMessage, mode CallMode) (*CallResult, error) {
	x, err := loadRow(ctx, s.d.DB.Pool, wid, integrationID)
	if err != nil {
		if db.IsNotFound(err) {
			return nil, errors.New("this integration was disconnected")
		}
		return nil, err
	}
	if !x.Enabled || x.Status != "connected" {
		return nil, errors.New(x.Name + " is not connected. An admin can reconnect it in Settings, Integrations")
	}
	i := slices.IndexFunc(x.Tools, func(t StoredTool) bool { return t.Name == remote })
	if i < 0 || slices.Contains(x.DisabledTools, remote) {
		return nil, errors.New("this tool is turned off for the workspace")
	}
	if (mode == CallRead && !x.readOnly(x.Tools[i])) || (mode == CallAuto && !x.autoRun(x.Tools[i])) {
		return nil, ErrNeedsApproval
	}
	var res *CallResult
	err = s.withSession(ctx, x, func(sess *session) error {
		ctx, cancel := context.WithTimeout(ctx, 55*time.Second)
		defer cancel()
		var err error
		res, err = sess.CallTool(ctx, remote, args)
		return err
	})
	return res, err
}

// ErrNeedsApproval: the call may not run without the user's approval as the workspace
// has the tool now.
var ErrNeedsApproval = errors.New("this tool needs the user's approval now. Propose it again")

// AutoRun reports whether a connected tool runs without asking as the workspace has it
// now: connected, on, and set to run without asking.
func (s *Service) AutoRun(ctx context.Context, wid, integrationID uuid.UUID, remote string) (bool, error) {
	x, err := loadRow(ctx, s.d.DB.Pool, wid, integrationID)
	if db.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	i := slices.IndexFunc(x.Tools, func(t StoredTool) bool { return t.Name == remote })
	return x.Enabled && x.Status == "connected" && i >= 0 && !slices.Contains(x.DisabledTools, remote) && x.autoRun(x.Tools[i]), nil
}

var nameUnsafe = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// toolName joins a slug and a server's tool name into a model-safe name of at most 64
// characters; long names keep a hash so they stay unique.
func toolName(slug, remote string) string {
	n := slug + "__" + nameUnsafe.ReplaceAllString(remote, "_")
	if len(n) <= 64 {
		return n
	}
	sum := sha256.Sum256([]byte(remote))
	return n[:55] + "_" + hex.EncodeToString(sum[:4])
}

// IsTool reports whether a tool name belongs to an integration.
func IsTool(name string) bool { return strings.Contains(name, "__") }

func humanize(name string) string {
	s := strings.NewReplacer("_", " ", "-", " ").Replace(name)
	if s == "" {
		return name
	}
	r, size := utf8.DecodeRuneInString(s)
	return strings.ToUpper(string(r)) + s[size:]
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// slugFor picks a tool-name prefix unique in the workspace: the provider key, or the
// custom name, with a number when taken.
func slugFor(ctx context.Context, q db.Querier, wid uuid.UUID, base string) (string, error) {
	base = strings.Trim(nameUnsafe.ReplaceAllString(strings.ToLower(base), "_"), "_-")
	base = strings.ReplaceAll(base, "__", "_")
	if len(base) > 20 {
		base = strings.Trim(base[:20], "_-")
	}
	if base == "" {
		base = "custom"
	}
	for i := 1; i <= 50; i++ {
		cand := base
		if i > 1 {
			cand = base + "_" + itoa(i)
		}
		var taken bool
		if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM integrations WHERE workspace_id = $1 AND slug = $2)`, wid, cand).Scan(&taken); err != nil {
			return "", err
		}
		if !taken {
			return cand, nil
		}
	}
	return "", errors.New("too many integrations with this name")
}

func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return itoa(i/10) + string(rune('0'+i%10))
}

// hostOf is a URL's host, for display without the path (which can carry a secret).
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}
