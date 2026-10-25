// Package imports brings a team's history in from another helpdesk or a CSV file
// (contracts/imports.md). A source (package source) reads the other product; this package
// checks it, runs the import as a resumable River job, writes tickets through
// domain.CreateTicket and domain.AddMessage as quiet history, records every row it wrote by
// its source id, and undoes it on request.
package imports

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/helpdesk/imports/source"
	"github.com/useopendesk/opendesk/internal/helpdesk/imports/source/freshdesk"
	"github.com/useopendesk/opendesk/internal/helpdesk/imports/source/front"
	"github.com/useopendesk/opendesk/internal/helpdesk/imports/source/helpscout"
	"github.com/useopendesk/opendesk/internal/helpdesk/imports/source/intercom"
	"github.com/useopendesk/opendesk/internal/helpdesk/imports/source/zendesk"
	"github.com/useopendesk/opendesk/internal/jobs"
	"github.com/useopendesk/opendesk/internal/plan"
)

// Sources, by key.
const (
	Zendesk   = "zendesk"
	HelpScout = "helpscout"
	Freshdesk = "freshdesk"
	Intercom  = "intercom"
	Front     = "front"
	CSV       = "csv"
)

// Statuses.
const (
	StatusRunning   = "running"
	StatusPaused    = "paused"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCanceled  = "canceled"
	StatusUndoing   = "undoing"
	StatusUndone    = "undone"
)

// Phases, in order.
const (
	PhaseArticles  = "articles"
	PhaseTickets   = "tickets"
	PhaseThreading = "threading"
	PhaseDone      = "done"
)

// sourceNames are how people know the products.
var sourceNames = map[string]string{Zendesk: "Zendesk", HelpScout: "Help Scout", Freshdesk: "Freshdesk", Intercom: "Intercom", Front: "Front", CSV: "CSV"}

// factories build the API sources; CSV is read from its stored file (csv.go).
var factories = map[string]source.Factory{
	Zendesk:   zendesk.New,
	HelpScout: helpscout.New,
	Freshdesk: freshdesk.New,
	Intercom:  intercom.New,
	Front:     front.New,
}

// Service is the imports area. Tests replace Factories and Options.
type Service struct {
	d *deps.Deps
	// Factories build sources by key (factories, unless a test replaces one).
	Factories map[string]source.Factory
	// Options reach the sources' APIs.
	Options source.Options
	// fileLimits, when set, replaces the plan's file limits (tests).
	fileLimits func(plan.Entitlements) fileLimits
}

// New returns the service for d.
func New(d *deps.Deps) *Service {
	f := make(map[string]source.Factory, len(factories))
	for k, v := range factories {
		f[k] = v
	}
	return &Service{d: d, Factories: f, Options: source.Options{Dev: !d.Cfg.IsProduction()}}
}

var svc *Service

// Register adds the routes.
func Register(d *deps.Deps, api huma.API, r chi.Router) {
	s := service(d)
	s.registerRoutes(api, r)
}

// RegisterWorkers adds the run, undo and sweep workers.
func RegisterWorkers(d *deps.Deps, w *river.Workers) {
	s := service(d)
	river.AddWorker(w, &runWorker{s: s})
	river.AddWorker(w, &undoWorker{s: s})
	river.AddWorker(w, &sweepWorker{s: s})
}

// Periodic sweeps credentials and files no longer needed, hourly.
func Periodic(d *deps.Deps) []*river.PeriodicJob {
	return []*river.PeriodicJob{river.NewPeriodicJob(river.PeriodicInterval(time.Hour),
		func() (river.JobArgs, *river.InsertOpts) { return jobs.ImportSweepArgs{}, nil }, &river.PeriodicJobOpts{RunOnStart: true})}
}

func service(d *deps.Deps) *Service {
	if svc == nil || svc.d != d {
		svc = New(d)
	}
	return svc
}

// ImportOptions are what an import brings over and how (contracts/imports.md ImportOptions).
type ImportOptions struct {
	Tickets          bool       `json:"tickets"`
	Articles         bool       `json:"articles"`
	Attachments      bool       `json:"attachments"`
	Since            *string    `json:"since" doc:"Only tickets created on or after this day (YYYY-MM-DD)" nullable:"true"`
	MailboxID        *uuid.UUID `json:"mailbox_id" doc:"Email tickets reply from this mailbox" nullable:"true"`
	Tag              string     `json:"tag" maxLength:"50" doc:"Label added to every imported ticket; empty for none"`
	Statuses         string     `json:"statuses" enum:"keep,closed"`
	ArticlesAsDrafts bool       `json:"articles_as_drafts"`
}

// ImportMapping maps source agents to members and groups to teams; csv maps columns to fields.
type ImportMapping struct {
	Agents ImportIDMap       `json:"agents" nullable:"false"`
	Groups ImportIDMap       `json:"groups" nullable:"false"`
	CSV    *ImportCSVMapping `json:"csv,omitempty"`
}

// ImportIDMap maps source ids to members or teams. A null value maps to none: an agent keeps
// their name only, a group gets no team.
type ImportIDMap map[string]*uuid.UUID

// Schema lets a value be null, as the contract says.
func (ImportIDMap) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: huma.TypeObject, AdditionalProperties: &huma.Schema{Type: huma.TypeString, Format: "uuid", Nullable: true}}
}

// ImportCSVMapping names the column of each field (contracts/imports.md CsvMapping).
type ImportCSVMapping struct {
	TicketID      string `json:"ticket_id,omitempty" maxLength:"200"`
	Subject       string `json:"subject,omitempty" maxLength:"200"`
	Body          string `json:"body,omitempty" maxLength:"200"`
	FromEmail     string `json:"from_email,omitempty" maxLength:"200"`
	FromName      string `json:"from_name,omitempty" maxLength:"200"`
	Author        string `json:"author,omitempty" maxLength:"200"`
	Status        string `json:"status,omitempty" maxLength:"200"`
	Priority      string `json:"priority,omitempty" maxLength:"200"`
	Tags          string `json:"tags,omitempty" maxLength:"200"`
	CreatedAt     string `json:"created_at,omitempty" maxLength:"200"`
	AssigneeEmail string `json:"assignee_email,omitempty" maxLength:"200"`
	Team          string `json:"team,omitempty" maxLength:"200"`
	Company       string `json:"company,omitempty" maxLength:"200"`
	// The email headers of each message, so replies to imported history thread into it.
	MessageID  string `json:"message_id,omitempty" maxLength:"200"`
	InReplyTo  string `json:"in_reply_to,omitempty" maxLength:"200"`
	References string `json:"references,omitempty" maxLength:"200"`
}

// ImportAccount is the source account an import reads.
type ImportAccount struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// ImportTotals are what the source said it has; nil is unknown.
type ImportTotals struct {
	Tickets  *int `json:"tickets"`
	Articles *int `json:"articles"`
}

// ImportCounts are what an import has done so far, over all its runs.
type ImportCounts struct {
	Tickets     int `json:"tickets"`
	Updated     int `json:"updated"`
	Messages    int `json:"messages"`
	Contacts    int `json:"contacts"`
	Companies   int `json:"companies"`
	Articles    int `json:"articles"`
	Attachments int `json:"attachments"`
	Skipped     int `json:"skipped"`
	Failed      int `json:"failed"`
	// Threaded is how many emails the threading phase has read, of ThreadTotal.
	Threaded    int `json:"threaded"`
	ThreadTotal int `json:"thread_total"`
}

// ImportProblem is something an import could not bring over.
type ImportProblem struct {
	At      time.Time `json:"at"`
	Item    string    `json:"item"`
	Message string    `json:"message"`
}

// maxProblems is how many problems an import keeps.
const maxProblems = 50

// Import is the API shape (contracts/imports.md). Credentials never appear in it.
type Import struct {
	ID               uuid.UUID       `json:"id"`
	Source           string          `json:"source" enum:"zendesk,helpscout,freshdesk,intercom,front,csv"`
	Account          ImportAccount   `json:"account"`
	Status           string          `json:"status" enum:"running,paused,completed,failed,canceled,undoing,undone"`
	Phase            string          `json:"phase" enum:"articles,tickets,threading,done"`
	Run              int             `json:"run"`
	Options          ImportOptions   `json:"options"`
	Mapping          ImportMapping   `json:"mapping"`
	Totals           ImportTotals    `json:"totals"`
	Counts           ImportCounts    `json:"counts"`
	Problems         []ImportProblem `json:"problems"`
	LastError        string          `json:"last_error"`
	NeedsCredentials bool            `json:"needs_credentials"`
	WaitingUntil     *time.Time      `json:"waiting_until"`
	CanSync          bool            `json:"can_sync"`
	StartedBy        *domain.UserRef `json:"started_by"`
	StartedAt        time.Time       `json:"started_at"`
	FinishedAt       *time.Time      `json:"finished_at"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

// row is an imports row with what the API never shows.
type row struct {
	Import
	WorkspaceID  uuid.UUID
	AccountKey   string
	Generation   int
	Cursor       map[string]string
	Credentials  []byte
	StorageKey   *string
	Failures     int
	UpdatedSince *time.Time
	StartedByID  *uuid.UUID
}

const rowCols = `i.id, i.workspace_id, i.source, i.account_key, i.account_name, i.account_url, i.status, i.phase, i.run,
	i.generation, i.options, i.mapping, i.cursor, i.credentials_enc, i.storage_key, i.totals, i.counts, i.problems,
	i.last_error, i.needs_credentials, i.failures, i.waiting_until, i.updated_since, i.started_by, i.started_at,
	i.finished_at, i.created_at, i.updated_at, u.id, u.name, u.email, u.avatar_url`

const rowFrom = ` FROM imports i LEFT JOIN users u ON u.id = i.started_by`

func scanRow(r pgx.Row) (row, error) {
	var x row
	var opts, mapping, cursor, totals, counts, problems []byte
	var uID *uuid.UUID
	var uName, uEmail, uAvatar *string
	err := r.Scan(&x.ID, &x.WorkspaceID, &x.Source, &x.AccountKey, &x.Account.Name, &x.Account.URL, &x.Status, &x.Phase, &x.Run,
		&x.Generation, &opts, &mapping, &cursor, &x.Credentials, &x.StorageKey, &totals, &counts, &problems,
		&x.LastError, &x.NeedsCredentials, &x.Failures, &x.WaitingUntil, &x.UpdatedSince, &x.StartedByID, &x.StartedAt,
		&x.FinishedAt, &x.CreatedAt, &x.UpdatedAt, &uID, &uName, &uEmail, &uAvatar)
	if err != nil {
		return x, err
	}
	for _, p := range []struct {
		raw []byte
		v   any
	}{{opts, &x.Options}, {mapping, &x.Mapping}, {cursor, &x.Cursor}, {totals, &x.Totals}, {counts, &x.Counts}, {problems, &x.Problems}} {
		if len(p.raw) > 0 {
			if err := json.Unmarshal(p.raw, p.v); err != nil {
				return x, fmt.Errorf("import %s: %w", x.ID, err)
			}
		}
	}
	if x.Cursor == nil {
		x.Cursor = map[string]string{}
	}
	if x.Problems == nil {
		x.Problems = []ImportProblem{}
	}
	if x.Mapping.Agents == nil {
		x.Mapping.Agents = map[string]*uuid.UUID{}
	}
	if x.Mapping.Groups == nil {
		x.Mapping.Groups = map[string]*uuid.UUID{}
	}
	if uID != nil {
		x.StartedBy = &domain.UserRef{ID: *uID, Name: deref(uName), Email: deref(uEmail), AvatarURL: domain.Avatar(deref(uAvatar))}
	}
	x.CanSync = x.Source != CSV && len(x.Credentials) > 0 && (x.Status == StatusCompleted || x.Status == StatusRunning || x.Status == StatusPaused)
	return x, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func loadRow(ctx context.Context, q db.Querier, wid, id uuid.UUID) (row, error) {
	return scanRow(q.QueryRow(ctx, `SELECT `+rowCols+rowFrom+` WHERE i.workspace_id = $1 AND i.id = $2`, wid, id))
}

// lockRow loads an import for a change, holding its row until tx ends.
func lockRow(ctx context.Context, tx pgx.Tx, wid, id uuid.UUID) (row, error) {
	return scanRow(tx.QueryRow(ctx, `SELECT `+rowCols+rowFrom+` WHERE i.workspace_id = $1 AND i.id = $2 FOR UPDATE OF i`, wid, id))
}

// publish announces an import's new state (import.created / import.updated).
func publish(ctx context.Context, tx pgx.Tx, typ string, x row) error {
	_, err := events.Publish(ctx, tx, events.Event{WorkspaceID: x.WorkspaceID, Type: typ, Data: map[string]any{"import": x.Import}})
	return err
}

// defaultTag is the label imported tickets get unless the user picks another.
func defaultTag(src string) string { return src + "-import" }

// sourceName is how people know a source.
func sourceName(src string) string {
	if n, ok := sourceNames[src]; ok {
		return n
	}
	return strings.ToUpper(src)
}
