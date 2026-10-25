package imports

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/helpdesk/imports/source"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/jobs"
)

// errRunning answers a second import while one runs, is paused or is being undone.
var errRunning = httpx.E(http.StatusConflict, "import_running", "Another import is in progress. Finish, cancel or undo it first.")

func requireAdminUser(ctx context.Context) (*auth.Principal, error) {
	p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
	if err != nil {
		return nil, httpx.Wrap(err)
	}
	if p.Kind != auth.KindUser {
		return nil, httpx.Forbidden() // the flow pastes another product's credentials: a person does it
	}
	return p, nil
}

func parseID(raw string) (uuid.UUID, error) {
	id, ok := ids.Parse(raw)
	if !ok {
		return uuid.Nil, httpx.NotFound("Import")
	}
	return id, nil
}

func notFound(err error) error {
	if db.IsNotFound(err) {
		return httpx.NotFound("Import")
	}
	return err
}

// importCheckBody names a source and what reads it: credentials, or a CSV upload and its mapping.
type importCheckBody struct {
	Source      string            `json:"source" enum:"zendesk,helpscout,freshdesk,intercom,front,csv"`
	Credentials map[string]string `json:"credentials,omitempty" doc:"The source's fields (contracts/imports.md); never stored by a check"`
	UploadID    *uuid.UUID        `json:"upload_id,omitempty" doc:"CSV only"`
	Mapping     *ImportCSVMapping `json:"mapping,omitempty" doc:"CSV only; default the mapping guessed from the headers"`
}

type importCreateBody struct {
	Source      string            `json:"source" enum:"zendesk,helpscout,freshdesk,intercom,front,csv"`
	Credentials map[string]string `json:"credentials,omitempty"`
	UploadID    *uuid.UUID        `json:"upload_id,omitempty" doc:"CSV only"`
	Options     *ImportOptions    `json:"options,omitempty"`
	Mapping     *ImportMapping    `json:"mapping,omitempty"`
}

// ImportCSVUpload is an uploaded file ready to map (contracts/imports.md CsvUpload).
type ImportCSVUpload struct {
	UploadID  uuid.UUID         `json:"upload_id"`
	Filename  string            `json:"filename"`
	Rows      int               `json:"rows"`
	Columns   []ImportCSVColumn `json:"columns"`
	Suggested ImportCSVMapping  `json:"suggested"`
	Preview   []ImportSample    `json:"preview"`
}

type ImportCSVColumn struct {
	Name    string   `json:"name"`
	Samples []string `json:"samples"`
}

type importUploadForm struct {
	File huma.FormFile `form:"file" required:"true" doc:"A .csv file, at most 50 MB"`
}

const maxUploadBody = maxCSVBytes + 1<<20

func limitBody(api huma.API) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		r, w := humachi.Unwrap(ctx)
		if r.ContentLength > maxUploadBody {
			_ = huma.WriteErr(api, ctx, http.StatusRequestEntityTooLarge, "CSV files can be at most 50 MB.")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxUploadBody)
		next(ctx)
	}
}

type importOut struct{ Body Import }

func (s *Service) registerRoutes(api huma.API, _ chi.Router) {
	tags := []string{"Imports"}

	huma.Register(api, huma.Operation{OperationID: "check-import-source", Method: http.MethodPost, Path: "/v1/imports/check",
		Summary: "Check an import source", Tags: tags,
		Description: "Signs in to the source with the credentials and answers what it has: the account, counts, agents and groups matched to members and teams, and sample tickets. Nothing is stored."},
		func(ctx context.Context, in *struct{ Body importCheckBody }) (*struct{ Body ImportCheck }, error) {
			p, err := requireAdminUser(ctx)
			if err != nil {
				return nil, err
			}
			if !s.d.Limiter.Allow("import:check:"+p.WorkspaceID.String(), 30, time.Hour) {
				return nil, httpx.E(http.StatusTooManyRequests, "rate_limited", "Too many checks. Try again later.")
			}
			var src source.Source
			if in.Body.Source == CSV {
				if in.Body.UploadID == nil {
					return nil, httpx.Validation("upload_id is required for a CSV file")
				}
				up, f, err := s.loadUpload(ctx, p.WorkspaceID, *in.Body.UploadID)
				if err != nil {
					return nil, err
				}
				m := suggestMapping(f.Header)
				if in.Body.Mapping != nil {
					m = *in.Body.Mapping
				}
				if err := checkMapping(m, f.Header); err != nil {
					return nil, httpx.Validation(err.Error())
				}
				src = newCSVSource(f, m, up.filename, "csv:"+up.sha)
			} else {
				if src, err = s.build(in.Body.Source, in.Body.Credentials); err != nil {
					return nil, err
				}
			}
			out, _, err := s.check(ctx, p.WorkspaceID, in.Body.Source, src)
			if err != nil {
				return nil, err
			}
			return &struct{ Body ImportCheck }{out}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "upload-import-csv", Method: http.MethodPost, Path: "/v1/imports/csv",
		Summary: "Upload a CSV file to import", Tags: tags, DefaultStatus: http.StatusCreated, MaxBodyBytes: maxUploadBody,
		Middlewares: huma.Middlewares{limitBody(api)},
		Description: "UTF-8 or UTF-16 with a byte order mark, separated by commas, semicolons or tabs, with a header row. Answers its columns, a few values of each and the mapping guessed from the headers."},
		func(ctx context.Context, in *struct {
			RawBody huma.MultipartFormFiles[importUploadForm]
		}) (*struct{ Body ImportCSVUpload }, error) {
			if in.RawBody.Form != nil {
				defer func(f *multipart.Form) { _ = f.RemoveAll() }(in.RawBody.Form)
			}
			p, err := requireAdminUser(ctx)
			if err != nil {
				return nil, err
			}
			data := in.RawBody.Data()
			if data == nil || !data.File.IsSet || data.File.File == nil {
				return nil, httpx.Validation("file is required")
			}
			defer data.File.Close()
			if !s.d.Limiter.Allow("import:upload:"+p.WorkspaceID.String(), 20, time.Hour) {
				return nil, httpx.E(http.StatusTooManyRequests, "rate_limited", "Too many uploads. Try again later.")
			}
			out, err := s.upload(ctx, p, data.File)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body ImportCSVUpload }{out}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "list-imports", Method: http.MethodGet, Path: "/v1/imports",
		Summary: "List imports", Description: "Newest first.", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct {
			Body struct {
				Data []Import `json:"data"`
			}
		}, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			rows, err := s.d.DB.Pool.Query(ctx, `SELECT `+rowCols+rowFrom+` WHERE i.workspace_id = $1 ORDER BY i.created_at DESC LIMIT 100`, p.WorkspaceID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) { return scanRow(r) })
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out := &struct {
				Body struct {
					Data []Import `json:"data"`
				}
			}{}
			out.Body.Data = []Import{}
			for _, x := range list {
				out.Body.Data = append(out.Body.Data, x.Import)
			}
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "get-import", Method: http.MethodGet, Path: "/v1/imports/{id}",
		Summary: "Get an import", Tags: tags},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*importOut, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			x, err := loadRow(ctx, s.d.DB.Pool, p.WorkspaceID, id)
			if err != nil {
				return nil, httpx.Wrap(notFound(err))
			}
			return &importOut{x.Import}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "create-import", Method: http.MethodPost, Path: "/v1/imports",
		Summary: "Start an import", Tags: tags, DefaultStatus: http.StatusCreated,
		Description: "Starts at once and runs in the background; `import.updated` events follow its progress."},
		func(ctx context.Context, in *struct{ Body importCreateBody }) (*importOut, error) {
			p, err := requireAdminUser(ctx)
			if err != nil {
				return nil, err
			}
			x, err := s.create(ctx, p, in.Body)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &importOut{x}, nil
		})

	type idInput struct {
		ID string `path:"id"`
	}
	action := func(op, path, summary, desc string, fn func(context.Context, *auth.Principal, uuid.UUID) (Import, error)) {
		huma.Register(api, huma.Operation{OperationID: op, Method: http.MethodPost, Path: path, Summary: summary, Description: desc, Tags: tags},
			func(ctx context.Context, in *idInput) (*importOut, error) {
				p, err := requireAdminUser(ctx)
				if err != nil {
					return nil, err
				}
				id, err := parseID(in.ID)
				if err != nil {
					return nil, err
				}
				x, err := fn(ctx, p, id)
				if err != nil {
					return nil, httpx.Wrap(notFound(err))
				}
				return &importOut{x}, nil
			})
	}
	action("pause-import", "/v1/imports/{id}/pause", "Pause an import", "It finishes the ticket in hand, then stops until resumed.", s.pause)
	action("cancel-import", "/v1/imports/{id}/cancel", "Cancel an import", "What is already imported stays; undo removes it.", s.cancel)
	action("sync-import", "/v1/imports/{id}/sync", "Bring over what changed",
		"Imports tickets created or updated in the source since the last run, and their new messages.", s.sync)
	action("undo-import", "/v1/imports/{id}/undo", "Undo an import",
		"Removes the tickets, contacts, companies, labels and articles the import created. Pause or cancel a running import first.", s.startUndo)

	huma.Register(api, huma.Operation{OperationID: "resume-import", Method: http.MethodPost, Path: "/v1/imports/{id}/resume",
		Summary: "Resume an import", Tags: tags, Description: "Goes on where it stopped. New credentials must reach the same account."},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body *struct {
				Credentials map[string]string `json:"credentials,omitempty"`
			}
		}) (*importOut, error) {
			p, err := requireAdminUser(ctx)
			if err != nil {
				return nil, err
			}
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			var creds map[string]string
			if in.Body != nil {
				creds = in.Body.Credentials
			}
			x, err := s.resume(ctx, p, id, creds)
			if err != nil {
				return nil, httpx.Wrap(notFound(err))
			}
			return &importOut{x}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "delete-import-credentials", Method: http.MethodDelete, Path: "/v1/imports/{id}/credentials",
		Summary: "Remove an import's credentials", Tags: tags, Description: "After this the import cannot bring over changes."},
		func(ctx context.Context, in *idInput) (*importOut, error) {
			p, err := requireAdminUser(ctx)
			if err != nil {
				return nil, err
			}
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			x, err := s.change(ctx, p, id, func(tx pgx.Tx, x *row) error {
				if x.Status == StatusRunning {
					return httpx.Conflict("Pause the import before removing its credentials.")
				}
				if _, err := tx.Exec(ctx, `UPDATE imports SET credentials_enc = NULL, updated_at = now() WHERE id = $1`, x.ID); err != nil {
					return err
				}
				return audit.Log(ctx, tx, p, x.WorkspaceID, "import.credentials_removed", "import", &x.ID, auditData(*x))
			})
			if err != nil {
				return nil, httpx.Wrap(notFound(err))
			}
			return &importOut{x}, nil
		})
}

// build makes a source from pasted credentials, checking their shape.
func (s *Service) build(src string, creds map[string]string) (source.Source, error) {
	f, ok := s.Factories[src]
	if !ok {
		return nil, httpx.Validation("source must be zendesk, helpscout, freshdesk, intercom, front or csv")
	}
	if len(creds) > 10 {
		return nil, httpx.Validation("credentials has too many fields")
	}
	out, err := f(source.Credentials(creds), s.Options)
	if err != nil {
		return nil, sourceError(src, err)
	}
	return out, nil
}

func auditData(x row) map[string]any {
	return map[string]any{"source": x.Source, "account": x.Account.Name}
}

// upload stores a CSV file after reading it once, so a file that cannot be imported is
// refused before it is kept.
func (s *Service) upload(ctx context.Context, p *auth.Principal, f huma.FormFile) (ImportCSVUpload, error) {
	if f.Size > maxCSVBytes {
		return ImportCSVUpload{}, httpx.E(http.StatusRequestEntityTooLarge, "too_large", "CSV files can be at most 50 MB.")
	}
	filename := hd.CleanFilename(f.Filename)
	if ext := strings.ToLower(filepath.Ext(filename)); ext != ".csv" && ext != ".tsv" && ext != ".txt" {
		return ImportCSVUpload{}, httpx.E(http.StatusUnprocessableEntity, "file_type_refused", "Upload a .csv file. Spreadsheets can save one with File, then Download or Export, as CSV.")
	}
	raw, err := io.ReadAll(io.LimitReader(f.File, maxCSVBytes+1))
	if err != nil {
		return ImportCSVUpload{}, err
	}
	if len(raw) > maxCSVBytes {
		return ImportCSVUpload{}, httpx.E(http.StatusRequestEntityTooLarge, "too_large", "CSV files can be at most 50 MB.")
	}
	if len(raw) == 0 {
		return ImportCSVUpload{}, httpx.Validation("the file is empty")
	}
	parsed, err := parseCSV(raw)
	if err != nil {
		return ImportCSVUpload{}, httpx.E(http.StatusUnprocessableEntity, "csv_unreadable", err.Error())
	}
	sum := sha256.Sum256(raw)
	sha := hex.EncodeToString(sum[:])
	id := ids.New()
	key := "workspaces/" + p.WorkspaceID.String() + "/imports/" + id.String() + ".csv"
	if err := s.d.Storage.Put(ctx, key, bytes.NewReader(raw), int64(len(raw)), "text/csv"); err != nil {
		return ImportCSVUpload{}, err
	}
	if _, err := s.d.DB.Pool.Exec(ctx, `INSERT INTO import_uploads (id, workspace_id, storage_key, filename, sha256, size_bytes, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, id, p.WorkspaceID, key, filename, sha, len(raw), p.ActorUserID()); err != nil {
		_ = s.d.Storage.Delete(context.WithoutCancel(ctx), key)
		return ImportCSVUpload{}, err
	}
	out := ImportCSVUpload{UploadID: id, Filename: filename, Rows: len(parsed.Rows), Columns: []ImportCSVColumn{}, Suggested: suggestMapping(parsed.Header), Preview: []ImportSample{}}
	for i, h := range parsed.Header {
		col := ImportCSVColumn{Name: h, Samples: []string{}}
		for _, rec := range parsed.Rows {
			if len(col.Samples) >= 3 {
				break
			}
			if i < len(rec) {
				if v := strings.TrimSpace(rec[i]); v != "" {
					col.Samples = append(col.Samples, hd.Excerpt(v, 80))
				}
			}
		}
		out.Columns = append(out.Columns, col)
	}
	if checkMapping(out.Suggested, parsed.Header) == nil {
		cs := newCSVSource(parsed, out.Suggested, filename, "csv:"+sha)
		out.Preview = samples(ctx, cs)
	}
	return out, nil
}

type upload struct {
	id       uuid.UUID
	key      string
	filename string
	sha      string
}

// loadUpload reads a workspace's upload and parses it.
func (s *Service) loadUpload(ctx context.Context, wid, id uuid.UUID) (upload, *csvFile, error) {
	up := upload{id: id}
	err := s.d.DB.Pool.QueryRow(ctx, `SELECT storage_key, filename, sha256 FROM import_uploads WHERE workspace_id = $1 AND id = $2`, wid, id).
		Scan(&up.key, &up.filename, &up.sha)
	if db.IsNotFound(err) {
		return up, nil, httpx.E(http.StatusNotFound, "not_found", "The uploaded file is gone. Upload it again.")
	}
	if err != nil {
		return up, nil, httpx.Wrap(err)
	}
	f, err := s.readCSV(ctx, up.key)
	if errors.As(err, new(uploadReadError)) {
		return up, nil, httpx.Wrap(err)
	}
	if err != nil {
		return up, nil, httpx.E(http.StatusUnprocessableEntity, "csv_unreadable", err.Error())
	}
	return up, f, nil
}

// defaultOptions are what an import brings when the request names nothing.
func defaultOptions(src string) ImportOptions {
	return ImportOptions{Tickets: true, Articles: src != CSV, Attachments: true, Tag: defaultTag(src), Statuses: "keep"}
}

// cleanOptions checks options against the workspace.
func cleanOptions(ctx context.Context, q db.Querier, wid uuid.UUID, o ImportOptions) (ImportOptions, error) {
	if o.Statuses == "" {
		o.Statuses = "keep"
	}
	if o.Statuses != "keep" && o.Statuses != "closed" {
		return o, httpx.Validation("options.statuses must be keep or closed")
	}
	if !o.Tickets && !o.Articles {
		return o, httpx.Validation("Choose tickets, articles or both to import.")
	}
	if o.Since != nil {
		v := strings.TrimSpace(*o.Since)
		if v == "" {
			o.Since = nil
		} else if _, err := time.Parse(time.DateOnly, v); err != nil {
			return o, httpx.Validation("options.since must be a day, YYYY-MM-DD")
		} else {
			o.Since = &v
		}
	}
	if o.Tag = strings.TrimSpace(o.Tag); o.Tag != "" {
		t, err := domain.CleanTagName(o.Tag)
		if err != nil {
			return o, err
		}
		o.Tag = t
	}
	if o.MailboxID != nil {
		var ok bool
		if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM mailboxes WHERE workspace_id = $1 AND id = $2)`, wid, *o.MailboxID).Scan(&ok); err != nil {
			return o, err
		}
		if !ok {
			return o, httpx.Validation("options.mailbox_id is not a mailbox of this workspace")
		}
	}
	return o, nil
}

// cleanMapping checks that every member and team the mapping names is the workspace's.
func cleanMapping(ctx context.Context, q db.Querier, wid uuid.UUID, m ImportMapping) (ImportMapping, error) {
	if m.Agents == nil {
		m.Agents = map[string]*uuid.UUID{}
	}
	if m.Groups == nil {
		m.Groups = map[string]*uuid.UUID{}
	}
	if len(m.Agents) > 5000 || len(m.Groups) > 1000 {
		return m, httpx.Validation("the mapping is too large")
	}
	var users, teams []uuid.UUID
	for _, v := range m.Agents {
		if v != nil && !slices.Contains(users, *v) {
			users = append(users, *v)
		}
	}
	for _, v := range m.Groups {
		if v != nil && !slices.Contains(teams, *v) {
			teams = append(teams, *v)
		}
	}
	var n int
	if len(users) > 0 {
		if err := q.QueryRow(ctx, `SELECT count(*) FROM members WHERE workspace_id = $1 AND user_id = ANY($2)`, wid, users).Scan(&n); err != nil {
			return m, err
		}
		if n != len(users) {
			return m, httpx.Validation("mapping.agents names someone who is not a member of this workspace")
		}
	}
	if len(teams) > 0 {
		if err := q.QueryRow(ctx, `SELECT count(*) FROM teams WHERE workspace_id = $1 AND id = ANY($2)`, wid, teams).Scan(&n); err != nil {
			return m, err
		}
		if n != len(teams) {
			return m, httpx.Validation("mapping.groups names a team that is not in this workspace")
		}
	}
	return m, nil
}

// create checks the source once more and starts the import.
func (s *Service) create(ctx context.Context, p *auth.Principal, in importCreateBody) (Import, error) {
	wid := p.WorkspaceID
	opts := defaultOptions(in.Source)
	if in.Options != nil {
		opts = *in.Options
	}
	if in.Source == CSV {
		opts.Articles = false
	}
	opts, err := cleanOptions(ctx, s.d.DB.Pool, wid, opts)
	if err != nil {
		return Import{}, err
	}
	mapping := ImportMapping{}
	if in.Mapping != nil {
		mapping = *in.Mapping
	}
	if mapping, err = cleanMapping(ctx, s.d.DB.Pool, wid, mapping); err != nil {
		return Import{}, err
	}
	var active bool
	if err := s.d.DB.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM imports WHERE workspace_id = $1 AND status IN ('running', 'paused', 'undoing'))`,
		wid).Scan(&active); err != nil {
		return Import{}, err
	}
	if active {
		return Import{}, errRunning
	}

	var src source.Source
	var sealed []byte
	var up upload
	if in.Source == CSV {
		if in.UploadID == nil {
			return Import{}, httpx.Validation("upload_id is required for a CSV file")
		}
		var f *csvFile
		if up, f, err = s.loadUpload(ctx, wid, *in.UploadID); err != nil {
			return Import{}, err
		}
		if mapping.CSV == nil {
			m := suggestMapping(f.Header)
			mapping.CSV = &m
		}
		if err := checkMapping(*mapping.CSV, f.Header); err != nil {
			return Import{}, httpx.Validation(err.Error())
		}
		src = newCSVSource(f, *mapping.CSV, up.filename, "csv:"+up.sha)
	} else {
		mapping.CSV = nil
		if src, err = s.build(in.Source, in.Credentials); err != nil {
			return Import{}, err
		}
		if sealed, err = s.d.Box.SealJSON(in.Credentials); err != nil {
			return Import{}, err
		}
	}
	scan, err := src.Check(ctx)
	if err != nil {
		return Import{}, sourceError(in.Source, err)
	}
	if !scan.HasArticles {
		opts.Articles = false
	}
	if !opts.Tickets && !opts.Articles {
		return Import{}, httpx.Validation("This account has no help center, so choose tickets to import.")
	}
	var totals ImportTotals
	if scan.Tickets >= 0 && opts.Tickets {
		n := scan.Tickets
		totals.Tickets = &n
	}
	if scan.Articles >= 0 && opts.Articles {
		n := scan.Articles
		totals.Articles = &n
	}
	phase := PhaseTickets
	if opts.Articles {
		phase = PhaseArticles
	}
	id := ids.New()
	var out Import
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		var key *string
		if in.Source == CSV {
			// The import owns the file now; the upload row goes.
			tag, err := tx.Exec(ctx, `DELETE FROM import_uploads WHERE workspace_id = $1 AND id = $2`, wid, up.id)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return httpx.E(http.StatusNotFound, "not_found", "The uploaded file is gone. Upload it again.")
			}
			key = &up.key
		}
		_, err := tx.Exec(ctx, `INSERT INTO imports (id, workspace_id, source, account_key, account_name, account_url, status, phase,
			options, mapping, credentials_enc, storage_key, totals, counts, started_by)
			VALUES ($1,$2,$3,$4,$5,$6,'running',$7,$8,$9,$10,$11,$12,$13,$14)`,
			id, wid, in.Source, scan.Account.Key, hd.Truncate(scan.Account.Name, 200), scan.Account.URL, phase, opts, mapping, sealed, key,
			totals, ImportCounts{}, p.ActorUserID())
		if db.IsUniqueViolation(err, "imports_one_active_uq") {
			return errRunning
		}
		if err != nil {
			return err
		}
		x, err := loadRow(ctx, tx, wid, id)
		if err != nil {
			return err
		}
		if in.Source == CSV {
			if cs, ok := src.(*csvSource); ok && len(cs.problems) > 0 {
				x.Problems = cs.problems
				if len(x.Problems) > maxProblems {
					x.Problems = x.Problems[:maxProblems]
				}
				x.Counts.Skipped = len(cs.problems)
				if _, err := tx.Exec(ctx, `UPDATE imports SET problems = $2, counts = $3 WHERE id = $1`, id, x.Problems, x.Counts); err != nil {
					return err
				}
			}
		}
		if err := s.enqueueRun(ctx, tx, x); err != nil {
			return err
		}
		if err := audit.Log(ctx, tx, p, wid, "import.started", "import", &id, auditData(x)); err != nil {
			return err
		}
		if err := publish(ctx, tx, "import.created", x); err != nil {
			return err
		}
		out = x.Import
		return nil
	})
	return out, err
}

// change runs fn on an import under its row lock, then announces it.
func (s *Service) change(ctx context.Context, p *auth.Principal, id uuid.UUID, fn func(pgx.Tx, *row) error) (Import, error) {
	var out Import
	err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		x, err := lockRow(ctx, tx, p.WorkspaceID, id)
		if err != nil {
			return err
		}
		if err := fn(tx, &x); err != nil {
			return err
		}
		next, err := loadRow(ctx, tx, p.WorkspaceID, id)
		if err != nil {
			return err
		}
		out = next.Import
		return publish(ctx, tx, "import.updated", next)
	})
	return out, err
}

func (s *Service) pause(ctx context.Context, p *auth.Principal, id uuid.UUID) (Import, error) {
	return s.change(ctx, p, id, func(tx pgx.Tx, x *row) error {
		if x.Status != StatusRunning {
			return httpx.Conflict("Only a running import can be paused.")
		}
		_, err := tx.Exec(ctx, `UPDATE imports SET status = 'paused', waiting_until = NULL, updated_at = now() WHERE id = $1`, x.ID)
		return err
	})
}

func (s *Service) cancel(ctx context.Context, p *auth.Principal, id uuid.UUID) (Import, error) {
	var file *string
	out, err := s.change(ctx, p, id, func(tx pgx.Tx, x *row) error {
		if x.Status != StatusRunning && x.Status != StatusPaused {
			return httpx.Conflict("Only a running or paused import can be canceled.")
		}
		file = x.StorageKey
		// Nothing will read the source again: its credentials and file go now.
		if _, err := tx.Exec(ctx, `UPDATE imports SET status = 'canceled', generation = generation + 1, finished_at = now(),
			waiting_until = NULL, credentials_enc = NULL, storage_key = NULL, updated_at = now() WHERE id = $1`, x.ID); err != nil {
			return err
		}
		return audit.Log(ctx, tx, p, x.WorkspaceID, "import.canceled", "import", &x.ID, auditData(*x))
	})
	if err == nil && file != nil {
		_ = s.d.Storage.Delete(context.WithoutCancel(ctx), *file)
	}
	return out, err
}

func (s *Service) resume(ctx context.Context, p *auth.Principal, id uuid.UUID, creds map[string]string) (Import, error) {
	var sealed []byte
	if len(creds) > 0 {
		x, err := loadRow(ctx, s.d.DB.Pool, p.WorkspaceID, id)
		if err != nil {
			return Import{}, err
		}
		if x.Source == CSV {
			return Import{}, httpx.Validation("CSV imports have no credentials")
		}
		// Fields left empty keep their earlier value, so an expired token is all that is retyped.
		if len(x.Credentials) > 0 {
			var old map[string]string
			if err := s.d.Box.OpenJSON(x.Credentials, &old); err == nil {
				merged := map[string]string{}
				for k, v := range old {
					merged[k] = v
				}
				for k, v := range creds {
					if strings.TrimSpace(v) != "" {
						merged[k] = v
					}
				}
				creds = merged
			}
		}
		src, err := s.build(x.Source, creds)
		if err != nil {
			return Import{}, err
		}
		scan, err := src.Check(ctx)
		if err != nil {
			return Import{}, sourceError(x.Source, err)
		}
		if scan.Account.Key != x.AccountKey {
			return Import{}, httpx.Validation("These credentials reach another " + sourceName(x.Source) + " account. Use credentials for " + x.Account.Name + ".")
		}
		if sealed, err = s.d.Box.SealJSON(creds); err != nil {
			return Import{}, err
		}
	}
	return s.change(ctx, p, id, func(tx pgx.Tx, x *row) error {
		if x.Status != StatusPaused && x.Status != StatusFailed {
			return httpx.Conflict("Only a paused or failed import can be resumed.")
		}
		if sealed == nil && x.Source != CSV && len(x.Credentials) == 0 {
			return httpx.E(http.StatusUnprocessableEntity, "import_auth_failed", "Enter the credentials again to resume.")
		}
		if x.Source == CSV && x.StorageKey == nil {
			return httpx.Conflict("The CSV file is no longer stored. Upload it again in a new import.")
		}
		if sealed == nil {
			sealed = x.Credentials
		}
		var gen int
		err := tx.QueryRow(ctx, `UPDATE imports SET status = 'running', generation = generation + 1, credentials_enc = $2,
			needs_credentials = false, failures = 0, last_error = '', waiting_until = NULL, finished_at = NULL, updated_at = now()
			WHERE id = $1 RETURNING generation`, x.ID, sealed).Scan(&gen)
		if db.IsUniqueViolation(err, "imports_one_active_uq") {
			return errRunning
		}
		if err != nil {
			return err
		}
		x.Generation = gen
		return s.enqueueRun(ctx, tx, *x)
	})
}

func (s *Service) sync(ctx context.Context, p *auth.Principal, id uuid.UUID) (Import, error) {
	return s.change(ctx, p, id, func(tx pgx.Tx, x *row) error {
		if x.Status != StatusCompleted || x.Source == CSV || len(x.Credentials) == 0 {
			return httpx.Conflict("Only a completed import that still has its credentials can bring over changes.")
		}
		phase := PhaseTickets
		if x.Options.Articles {
			phase = PhaseArticles
		}
		var gen int
		// Tickets updated a little before the last run began are looked at again: the
		// source's clock and ours need not agree.
		err := tx.QueryRow(ctx, `UPDATE imports SET status = 'running', run = run + 1, generation = generation + 1, phase = $2,
			cursor = '{}', updated_since = started_at - interval '10 minutes', started_at = now(), finished_at = NULL,
			failures = 0, last_error = '', waiting_until = NULL, updated_at = now() WHERE id = $1 RETURNING generation`, x.ID, phase).Scan(&gen)
		if db.IsUniqueViolation(err, "imports_one_active_uq") {
			return errRunning
		}
		if err != nil {
			return err
		}
		x.Generation = gen
		if err := s.enqueueRun(ctx, tx, *x); err != nil {
			return err
		}
		return audit.Log(ctx, tx, p, x.WorkspaceID, "import.synced", "import", &x.ID, auditData(*x))
	})
}

func (s *Service) startUndo(ctx context.Context, p *auth.Principal, id uuid.UUID) (Import, error) {
	return s.change(ctx, p, id, func(tx pgx.Tx, x *row) error {
		switch x.Status {
		case StatusRunning:
			return httpx.Conflict("Pause or cancel the import before undoing it.")
		case StatusUndoing, StatusUndone:
			return httpx.Conflict("This import is already undone.")
		}
		var gen int
		err := tx.QueryRow(ctx, `UPDATE imports SET status = 'undoing', generation = generation + 1, credentials_enc = NULL,
			waiting_until = NULL, updated_at = now() WHERE id = $1 RETURNING generation`, x.ID).Scan(&gen)
		if db.IsUniqueViolation(err, "imports_one_active_uq") {
			return errRunning
		}
		if err != nil {
			return err
		}
		if _, err := s.d.Jobs.InsertTx(ctx, tx, jobs.ImportUndoArgs{ImportID: x.ID, Generation: gen}, &river.InsertOpts{MaxAttempts: 10}); err != nil {
			return err
		}
		return audit.Log(ctx, tx, p, x.WorkspaceID, "import.undone", "import", &x.ID, auditData(*x))
	})
}
