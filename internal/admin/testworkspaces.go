package admin

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"math/big"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/core"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/jobs"
	"github.com/useopendesk/opendesk/internal/plan"
	"github.com/useopendesk/opendesk/internal/sample"
)

// Test workspaces (contracts/admin.md "Test workspaces") are workspaces an operator makes
// for demos and testing: created through core like any other, flagged test, optionally
// filled with sample data by a background job, owned by a test account whose generated
// password expires, and removed by the hourly cleanup once test_expires_at passes.

// Sample data sizes and kinds (internal/sample).
var (
	SampleSizes = []string{"small", "realistic", "large"}
	SampleKinds = []string{"tickets", "chat", "kb", "team", "collab", "history"}
)

// Test workspace rules.
const (
	// DefaultPasswordDays is how long a generated password works unless asked otherwise.
	DefaultPasswordDays = 30
	// DefaultDeleteAfterDays is when a test workspace is deleted unless asked otherwise.
	DefaultDeleteAfterDays = 30
	maxPasswordDays        = 90
	maxDeleteAfterDays     = 365
	// TestEmailSuffix ends every new test account's address: the reserved .invalid
	// top-level domain never resolves, so nothing is ever delivered to it.
	TestEmailSuffix = ".invalid"
	// seedStale is when a batch still marked running is taken as lost (its job is long
	// past its timeout) and another may start.
	seedStale = time.Hour
	// seedTimeout bounds one batch of sample data.
	seedTimeout = 30 * time.Minute
)

// A "pro" test workspace has no Stripe subscription: unlimited mailboxes and members and
// a monthly AI allowance through the limit overrides, and an AI grant on top.
var (
	testProMonthlyAI = plan.FromUSD(20)
	testProAIGrant   = plan.FromUSD(50)
)

// AdminSampleCounts is what sample data added (internal/sample Counts).
type AdminSampleCounts struct {
	Members      int `json:"members"`
	Teams        int `json:"teams"`
	Contacts     int `json:"contacts"`
	Companies    int `json:"companies"`
	Tickets      int `json:"tickets"`
	Messages     int `json:"messages"`
	Chats        int `json:"chats"`
	Articles     int `json:"articles"`
	Collections  int `json:"collections"`
	MailAccounts int `json:"mail_accounts"`
	MailThreads  int `json:"mail_threads"`
	Views        int `json:"views"`
	SavedReplies int `json:"saved_replies"`
}

func (c *AdminSampleCounts) add(n sample.Counts) {
	c.Members += n.Members
	c.Teams += n.Teams
	c.Contacts += n.Contacts
	c.Companies += n.Companies
	c.Tickets += n.Tickets
	c.Messages += n.Messages
	c.Chats += n.Chats
	c.Articles += n.Articles
	c.Collections += n.Collections
	c.MailAccounts += n.MailAccounts
	c.MailThreads += n.MailThreads
	c.Views += n.Views
	c.SavedReplies += n.SavedReplies
}

// AdminSampleData is workspaces.sample_data: the last batch's status, size and kinds, and
// the counts of every batch that finished.
type AdminSampleData struct {
	Status    string             `json:"status" enum:"none,running,done,failed"`
	Size      string             `json:"size"`
	Kinds     []string           `json:"kinds"`
	Counts    *AdminSampleCounts `json:"counts"`
	Batches   int                `json:"batches"`
	StartedAt *time.Time         `json:"started_at"`
	SeededAt  *time.Time         `json:"seeded_at"`
	Error     string             `json:"error"`
}

func parseSampleData(raw []byte) AdminSampleData {
	var x AdminSampleData
	_ = json.Unmarshal(raw, &x)
	if x.Status == "" {
		x.Status = "none"
	}
	if x.Kinds == nil {
		x.Kinds = []string{}
	}
	return x
}

// running reports a batch in progress that is not lost.
func (x AdminSampleData) running(now time.Time) bool {
	return x.Status == "running" && x.StartedAt != nil && now.Sub(*x.StartedAt) < seedStale
}

type AdminTestOwner struct {
	ID                uuid.UUID  `json:"id"`
	Email             string     `json:"email"`
	Name              string     `json:"name"`
	TestAccount       bool       `json:"test_account"`
	PasswordExpiresAt *time.Time `json:"password_expires_at"`
}

type AdminTestWorkspace struct {
	Workspace     AdminWorkspaceRef     `json:"workspace"`
	Owner         *AdminTestOwner       `json:"owner"`
	CreatedBy     *InstanceAdminGrantor `json:"created_by"`
	Reason        string                `json:"reason"`
	Plan          string                `json:"plan" enum:"free,pro,collab"`
	TestExpiresAt *time.Time            `json:"test_expires_at"`
	SampleData    AdminSampleData       `json:"sample_data"`
	Members       int                   `json:"members"`
	SuspendedAt   *time.Time            `json:"suspended_at"`
	CreatedAt     time.Time             `json:"created_at"`
}

const testWorkspaceCols = `w.id, w.name, w.slug, o.id, o.email, o.name, o.test_account, o.password_expires_at, c.id, c.email,
	COALESCE(w.test_reason, ''), COALESCE(w.test_plan, 'free'), w.test_expires_at, w.sample_data,
	(SELECT count(*) FROM members m WHERE m.workspace_id = w.id), w.suspended_at, w.created_at
	FROM workspaces w
	LEFT JOIN LATERAL (SELECT u.id, u.email, u.name, u.test_account, u.password_expires_at FROM members m JOIN users u ON u.id = m.user_id
		WHERE m.workspace_id = w.id AND m.role = 'owner' ORDER BY m.created_at LIMIT 1) o ON true
	LEFT JOIN users c ON c.id = w.test_created_by`

func scanTestWorkspace(r pgx.Row) (AdminTestWorkspace, error) {
	var x AdminTestWorkspace
	var oid, cid *uuid.UUID
	var oemail, oname, cemail *string
	var otest *bool
	var oexp *time.Time
	var raw []byte
	err := r.Scan(&x.Workspace.ID, &x.Workspace.Name, &x.Workspace.Slug, &oid, &oemail, &oname, &otest, &oexp, &cid, &cemail,
		&x.Reason, &x.Plan, &x.TestExpiresAt, &raw, &x.Members, &x.SuspendedAt, &x.CreatedAt)
	if err != nil {
		return x, err
	}
	if oid != nil {
		x.Owner = &AdminTestOwner{ID: *oid, Email: deref(oemail), Name: deref(oname), TestAccount: otest != nil && *otest, PasswordExpiresAt: oexp}
	}
	if cid != nil {
		x.CreatedBy = &InstanceAdminGrantor{ID: *cid, Email: deref(cemail)}
	}
	x.SampleData = parseSampleData(raw)
	return x, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func loadTestWorkspace(ctx context.Context, q db.Querier, wid uuid.UUID) (AdminTestWorkspace, error) {
	x, err := scanTestWorkspace(q.QueryRow(ctx, `SELECT `+testWorkspaceCols+` WHERE w.id = $1 AND w.test`, wid))
	if db.IsNotFound(err) {
		return x, httpx.NotFound("Test workspace")
	}
	return x, err
}

// ListTestWorkspaces lists test workspaces, oldest first, after the cursor.
func ListTestWorkspaces(ctx context.Context, q db.Querier, after time.Time, afterID uuid.UUID, limit int) ([]AdminTestWorkspace, error) {
	rows, err := q.Query(ctx, `SELECT `+testWorkspaceCols+` WHERE w.test AND (w.created_at, w.id) > ($1, $2)
		ORDER BY w.created_at, w.id LIMIT $3`, after, afterID, limit)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (AdminTestWorkspace, error) { return scanTestWorkspace(r) })
	if list == nil {
		list = []AdminTestWorkspace{}
	}
	return list, err
}

// SampleRequest asks for one batch of sample data.
type SampleRequest struct {
	Size  string   `json:"size" enum:"small,realistic,large"`
	Kinds []string `json:"kinds" minItems:"1" maxItems:"6" doc:"tickets, chat, kb, team, collab, history"`
}

func (r *SampleRequest) check() error {
	if !slices.Contains(SampleSizes, r.Size) {
		return httpx.Validation("size must be small, realistic or large.")
	}
	var kinds []string
	for _, k := range r.Kinds {
		if !slices.Contains(SampleKinds, k) {
			return httpx.Validation("kinds must be among tickets, chat, kb, team, collab and history.")
		}
		if !slices.Contains(kinds, k) {
			kinds = append(kinds, k)
		}
	}
	if len(kinds) == 0 {
		return httpx.Validation("Choose at least one kind of sample data.")
	}
	// In the generator's order, so the same request always reads the same.
	slices.SortFunc(kinds, func(a, b string) int { return slices.Index(SampleKinds, a) - slices.Index(SampleKinds, b) })
	r.Kinds = kinds
	return nil
}

// TestWorkspaceInput is what a test workspace is created with.
type TestWorkspaceInput struct {
	Name string
	// The owner: an existing account by id or address, or a new test account for an
	// address ending in .invalid.
	OwnerEmail  string
	OwnerUserID uuid.UUID
	OwnerName   string
	// PasswordDays is how long a new account's password works; 0 is the default.
	PasswordDays int
	Plan         string
	Sample       *SampleRequest
	// DeleteAfterDays: nil keeps the workspace until it is deleted by hand.
	DeleteAfterDays *int
	Reason          string
	// SeedInline leaves the first batch to the caller (RunSeed) instead of a job.
	SeedInline bool
}

// TestCredentials are a test workspace's owner and, once, the password generated for them.
type TestCredentials struct {
	ID                uuid.UUID  `json:"id"`
	Email             string     `json:"email"`
	NewAccount        bool       `json:"new_account"`
	Password          *string    `json:"password" doc:"Only when generated now; never shown again"`
	PasswordExpiresAt *time.Time `json:"password_expires_at"`
}

type TestWorkspaceResult struct {
	Workspace AdminTestWorkspace `json:"workspace"`
	Owner     TestCredentials    `json:"owner"`
	SignInURL string             `json:"sign_in_url"`
}

func signInURL(d *deps.Deps, email string) string {
	return strings.TrimRight(d.Cfg.AppURL, "/") + "/login?email=" + url.QueryEscape(email)
}

func checkDays(v *int, max int, field string) error {
	if v != nil && (*v < 1 || *v > max) {
		return httpx.Validation(field + " must be between 1 and " + strconv.Itoa(max) + ".")
	}
	return nil
}

func days(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }

// CreateTestWorkspace creates a test workspace and, for a new address, its test account,
// in one transaction: a failure at any step leaves neither behind. The caller has checked
// the actor's permission and step-up.
func CreateTestWorkspace(ctx context.Context, d *deps.Deps, in TestWorkspaceInput, by Actor) (TestWorkspaceResult, error) {
	var out TestWorkspaceResult
	name, err := displayname.Validate("Workspace name", in.Name, displayname.Workspace, false)
	if err != nil {
		return out, err
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" || len([]rune(reason)) > 500 {
		return out, httpx.Validation("Give a reason of 1 to 500 characters.")
	}
	if in.Plan == "" {
		in.Plan = plan.Free
	}
	if in.Plan != plan.Free && in.Plan != plan.Pro && in.Plan != plan.Collab {
		return out, httpx.Validation("plan must be free, pro or collab.")
	}
	if in.PasswordDays == 0 {
		in.PasswordDays = DefaultPasswordDays
	}
	if err := checkDays(&in.PasswordDays, maxPasswordDays, "password_days"); err != nil {
		return out, err
	}
	if err := checkDays(in.DeleteAfterDays, maxDeleteAfterDays, "delete_after_days"); err != nil {
		return out, err
	}
	if in.Sample != nil {
		if err := in.Sample.check(); err != nil {
			return out, err
		}
	}

	// The owner: an existing account, or a new test account.
	var ownerID uuid.UUID
	var ownerEmail string
	email := ""
	if in.OwnerEmail != "" {
		if email = normEmail(in.OwnerEmail); email == "" {
			return out, httpx.E(422, "invalid_email", "Enter a valid email address for the owner.")
		}
	}
	switch {
	case (email == "") == (in.OwnerUserID == uuid.Nil):
		return out, httpx.Validation("Give the owner's email or user_id, not both.")
	case in.OwnerUserID != uuid.Nil:
		err = d.DB.Pool.QueryRow(ctx, `SELECT id, email FROM users WHERE id = $1`, in.OwnerUserID).Scan(&ownerID, &ownerEmail)
	default:
		err = d.DB.Pool.QueryRow(ctx, `SELECT id, email FROM users WHERE email = $1`, email).Scan(&ownerID, &ownerEmail)
	}
	if err != nil && !db.IsNotFound(err) {
		return out, err
	}
	if db.IsNotFound(err) && in.OwnerUserID != uuid.Nil {
		return out, httpx.E(404, "user_not_found", "No account has this id.")
	}
	newAccount := ownerID == uuid.Nil
	var password, hash, ownerName string
	if newAccount {
		if !strings.HasSuffix(email, TestEmailSuffix) {
			return out, httpx.E(422, "test_email_required", "A new test account needs an address ending in .invalid, which never receives mail. "+
				"To give a real person a test workspace, choose their existing account.")
		}
		ownerName = displayname.CleanOr(in.OwnerName, displayname.Person, "")
		if ownerName == "" {
			ownerName = displayname.CleanOr(displayname.FromEmail(email), displayname.Person, "Test owner")
		}
		if password, err = GeneratePassword(); err != nil {
			return out, err
		}
		if hash, err = auth.HashPassword(password); err != nil {
			return out, err
		}
		ownerEmail = email
	}

	now := time.Now().UTC()
	var expires *time.Time
	if in.DeleteAfterDays != nil {
		t := now.Add(days(*in.DeleteAfterDays))
		expires = &t
	}
	passwordExpires := now.Add(days(in.PasswordDays))
	sampleData := AdminSampleData{Status: "none", Kinds: []string{}}
	if in.Sample != nil {
		sampleData = AdminSampleData{Status: "running", Size: in.Sample.Size, Kinds: in.Sample.Kinds, Batches: 1, StartedAt: &now}
	}
	ops := core.NewOps(d)
	actor := &auth.Principal{IP: by.IP}
	var wid uuid.UUID
	err = d.DB.Tx(ctx, func(tx pgx.Tx) error {
		if newAccount {
			if ownerID, err = ops.CreateTestAccount(ctx, tx, email, ownerName, hash, passwordExpires); err != nil {
				return err
			}
		} else {
			var suspended bool
			if err := tx.QueryRow(ctx, `SELECT suspended_at IS NOT NULL FROM users WHERE id = $1 FOR UPDATE`, ownerID).Scan(&suspended); err != nil {
				return err
			}
			if suspended {
				return httpx.E(409, "account_suspended", "This account is suspended.")
			}
		}
		if wid, err = ops.CreateWorkspace(ctx, tx, actor, ownerID, name); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE workspaces SET test = true, test_expires_at = $2, test_reason = $3, test_created_by = $4,
			test_plan = $5, sample_data = $6, updated_at = now() WHERE id = $1`, wid, expires, reason, by.UserID, in.Plan, sampleData); err != nil {
			return err
		}
		if newAccount {
			// The account exists for this workspace: no setup checklist.
			if _, err := tx.Exec(ctx, `UPDATE members SET onboarding_dismissed_at = now() WHERE workspace_id = $1 AND user_id = $2`, wid, ownerID); err != nil {
				return err
			}
		}
		if err := applyTestPlan(ctx, d, tx, wid, in.Plan, expires); err != nil {
			return err
		}
		if in.Sample != nil && !in.SeedInline {
			if _, err := d.Jobs.InsertTx(ctx, tx, jobs.SeedTestWorkspaceArgs{WorkspaceID: wid, Size: in.Sample.Size, Kinds: in.Sample.Kinds, Batch: 1},
				&river.InsertOpts{MaxAttempts: 1}); err != nil {
				return err
			}
		}
		if err := ops.AuditWorkspace(ctx, tx, actor, wid, "workspace.test_created", map[string]any{"plan": in.Plan, "test_expires_at": expires}); err != nil {
			return err
		}
		data := map[string]any{"plan": in.Plan, "owner_id": ownerID, "new_account": newAccount, "delete_after_days": in.DeleteAfterDays,
			"test_expires_at": expires, "reason": reason}
		if newAccount {
			data["password_days"] = in.PasswordDays
		}
		if in.Sample != nil {
			data["sample"] = map[string]any{"size": in.Sample.Size, "kinds": in.Sample.Kinds}
		}
		if err := writeAudit(ctx, tx, by.Type, by.UserID, by.Email, "test_workspace.created", "workspace", wid.String(), data, by.IP); err != nil {
			return err
		}
		out.Workspace, err = loadTestWorkspace(ctx, tx, wid)
		return err
	})
	if err != nil {
		return TestWorkspaceResult{}, err
	}
	out.Owner = TestCredentials{ID: ownerID, Email: ownerEmail, NewAccount: newAccount}
	if newAccount {
		out.Owner.Password, out.Owner.PasswordExpiresAt = &password, &passwordExpires
	} else if out.Workspace.Owner != nil {
		out.Owner.PasswordExpiresAt = out.Workspace.Owner.PasswordExpiresAt
	}
	out.SignInURL = signInURL(d, ownerEmail)
	return out, nil
}

// applyTestPlan gives a test workspace its plan without Stripe (billing on only; without
// billing every workspace is already unlimited and the plan is only recorded).
func applyTestPlan(ctx context.Context, d *deps.Deps, tx pgx.Tx, wid uuid.UUID, p string, until *time.Time) error {
	if !d.Cfg.BillingEnabled() {
		return nil
	}
	switch p {
	case plan.Pro:
		unlimited := 0
		monthly := testProMonthlyAI
		if err := plan.SetOverrides(ctx, tx, wid, plan.Overrides{MaxMailboxes: &unlimited, MaxMembers: &unlimited, MonthlyUSD: &monthly}); err != nil {
			return err
		}
		_, err := plan.Grant(ctx, tx, wid, testProAIGrant, "admin", "admin:test:"+wid.String())
		return err
	case plan.Collab:
		return plan.SetCollabAccess(ctx, tx, wid, plan.CollabAccess{Access: plan.CollabComplimentary, Until: until, Note: "Test workspace"})
	}
	return nil
}

// passwordAlphabet is what generated passwords are made of: letters and digits without
// the ones that read alike (0/O/o, 1/l/I).
var passwordAlphabet = func() string {
	var b strings.Builder
	for _, r := range []struct{ from, to rune }{{'A', 'Z'}, {'a', 'z'}, {'2', '9'}} {
		for c := r.from; c <= r.to; c++ {
			if !strings.ContainsRune("OoIl", c) {
				b.WriteRune(c)
			}
		}
	}
	return b.String()
}()

// GeneratePassword makes a test account's password: four groups of six characters with
// upper and lower case letters and digits (24 characters, about 138 bits), accepted by
// auth.CheckPassword.
func GeneratePassword() (string, error) {
	for {
		var b strings.Builder
		for g := 0; g < 4; g++ {
			if g > 0 {
				b.WriteByte('-')
			}
			for i := 0; i < 6; i++ {
				n, err := rand.Int(rand.Reader, big.NewInt(int64(len(passwordAlphabet))))
				if err != nil {
					return "", err
				}
				b.WriteByte(passwordAlphabet[n.Int64()])
			}
		}
		pw := b.String()
		if strings.IndexFunc(pw, unicode.IsUpper) >= 0 && strings.IndexFunc(pw, unicode.IsLower) >= 0 &&
			strings.IndexFunc(pw, unicode.IsDigit) >= 0 && auth.CheckPassword(pw) == nil {
			return pw, nil
		}
	}
}

// seedFor derives the generator's seed from the workspace id and the batch, so a batch
// can be reproduced and two batches differ.
func seedFor(wid uuid.UUID, batch int) int64 {
	return int64(binary.BigEndian.Uint64(wid[:8])^binary.BigEndian.Uint64(wid[8:])) + int64(batch-1)
}

// QueueSample starts another batch of sample data in a test workspace: 409 not_test for
// any other workspace, 409 seeding while a batch runs. The caller commits tx.
func QueueSample(ctx context.Context, d *deps.Deps, tx pgx.Tx, wid uuid.UUID, req SampleRequest, by Actor) error {
	if err := req.check(); err != nil {
		return err
	}
	var test bool
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT test, sample_data FROM workspaces WHERE id = $1 FOR UPDATE`, wid).Scan(&test, &raw)
	if db.IsNotFound(err) {
		return httpx.NotFound("Workspace")
	}
	if err != nil {
		return err
	}
	if !test {
		return httpx.E(409, "not_test", "Sample data is added to test workspaces only.")
	}
	now := time.Now().UTC()
	x := parseSampleData(raw)
	if x.running(now) {
		return httpx.E(409, "seeding", "Sample data is still being added. Try again when it finishes.")
	}
	x.Status, x.Size, x.Kinds, x.Error, x.StartedAt = "running", req.Size, req.Kinds, "", &now
	x.Batches++
	if _, err := tx.Exec(ctx, `UPDATE workspaces SET sample_data = $2, updated_at = now() WHERE id = $1`, wid, x); err != nil {
		return err
	}
	if _, err := d.Jobs.InsertTx(ctx, tx, jobs.SeedTestWorkspaceArgs{WorkspaceID: wid, Size: req.Size, Kinds: req.Kinds, Batch: x.Batches},
		&river.InsertOpts{MaxAttempts: 1}); err != nil {
		return err
	}
	return writeAudit(ctx, tx, by.Type, by.UserID, by.Email, "test_workspace.sample_added", "workspace", wid.String(),
		map[string]any{"size": req.Size, "kinds": req.Kinds, "batch": x.Batches}, by.IP)
}

// RunSeed adds one batch of sample data and records how it went in sample_data. A batch
// is attempted once: the generator does not undo what it added before an error.
func RunSeed(ctx context.Context, d *deps.Deps, a jobs.SeedTestWorkspaceArgs) error {
	var owner uuid.UUID
	err := d.DB.Pool.QueryRow(ctx, `SELECT m.user_id FROM workspaces w JOIN members m ON m.workspace_id = w.id AND m.role = 'owner'
		WHERE w.id = $1 AND w.test ORDER BY m.created_at LIMIT 1`, a.WorkspaceID).Scan(&owner)
	if db.IsNotFound(err) {
		return nil // deleted meanwhile
	}
	if err != nil {
		return err
	}
	kinds := make([]sample.Kind, len(a.Kinds))
	for i, k := range a.Kinds {
		kinds[i] = sample.Kind(k)
	}
	counts, seedErr := sample.Seed(ctx, d, a.WorkspaceID, sample.Options{Size: sample.Size(a.Size), Kinds: kinds, Seed: seedFor(a.WorkspaceID, a.Batch), OwnerID: owner})
	// Recorded even when the job's time ran out.
	wctx := context.WithoutCancel(ctx)
	err = d.DB.Tx(wctx, func(tx pgx.Tx) error {
		// Generated teammates have reserved .invalid addresses: they are test accounts,
		// left out of the overview and deleted with the workspace.
		if _, err := tx.Exec(wctx, `UPDATE users SET test_account = true WHERE NOT test_account AND email LIKE '%.invalid'
			AND id IN (SELECT user_id FROM members WHERE workspace_id = $1)`, a.WorkspaceID); err != nil {
			return err
		}
		var raw []byte
		err := tx.QueryRow(wctx, `SELECT sample_data FROM workspaces WHERE id = $1 FOR UPDATE`, a.WorkspaceID).Scan(&raw)
		if db.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		x := parseSampleData(raw)
		if seedErr == nil {
			if x.Counts == nil {
				x.Counts = &AdminSampleCounts{}
			}
			x.Counts.add(counts)
		}
		if x.Batches == a.Batch {
			now := time.Now().UTC()
			if seedErr != nil {
				x.Status, x.Error = "failed", truncate(seedErr.Error(), 500)
			} else {
				x.Status, x.Error, x.SeededAt = "done", "", &now
			}
		}
		_, err = tx.Exec(wctx, `UPDATE workspaces SET sample_data = $2 WHERE id = $1`, a.WorkspaceID, x)
		return err
	})
	if err != nil {
		return err
	}
	return seedErr
}

type seedWorker struct {
	river.WorkerDefaults[jobs.SeedTestWorkspaceArgs]
	d *deps.Deps
}

func (w *seedWorker) Timeout(*river.Job[jobs.SeedTestWorkspaceArgs]) time.Duration {
	return seedTimeout
}

func (w *seedWorker) Work(ctx context.Context, job *river.Job[jobs.SeedTestWorkspaceArgs]) error {
	return RunSeed(ctx, w.d, job.Args)
}

// DeleteTestWorkspace removes a workspace like its owner's "Delete workspace", then the
// test accounts among its members that are left in no workspace (and are not admins). It
// returns the removals to finish after the commit and how many accounts went.
func DeleteTestWorkspace(ctx context.Context, tx pgx.Tx, ops *core.Ops, actor *auth.Principal, wid uuid.UUID) ([]core.Removal, int, error) {
	rows, err := tx.Query(ctx, `SELECT m.user_id FROM members m JOIN users u ON u.id = m.user_id WHERE m.workspace_id = $1 AND u.test_account`, wid)
	if err != nil {
		return nil, 0, err
	}
	accounts, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, 0, err
	}
	r, err := ops.DeleteWorkspace(ctx, tx, wid)
	if err != nil {
		return nil, 0, err
	}
	removals := []core.Removal{r}
	n := 0
	for _, uid := range accounts {
		var keep bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM members WHERE user_id = $1) OR EXISTS (SELECT 1 FROM instance_admins WHERE user_id = $1)`,
			uid).Scan(&keep); err != nil {
			return nil, 0, err
		}
		if keep {
			continue
		}
		r, err := ops.DeleteAccount(ctx, tx, actor, uid)
		if err != nil {
			return nil, 0, err
		}
		removals = append(removals, r)
		n++
	}
	return removals, n, nil
}

// expireTestArgs deletes test workspaces past test_expires_at.
type expireTestArgs struct{}

func (expireTestArgs) Kind() string { return "admin.expire_test_workspaces" }

type expireTestWorker struct {
	river.WorkerDefaults[expireTestArgs]
	d *deps.Deps
}

// SystemActor is the instance itself, for the cleanup's entries in the admin audit log.
var SystemActor = Actor{Type: "system", Email: "system"}

func (w *expireTestWorker) Work(ctx context.Context, _ *river.Job[expireTestArgs]) error {
	_, err := ExpireTestWorkspaces(ctx, w.d)
	return err
}

// ExpireTestWorkspaces deletes the test workspaces whose time is up, with the same removal
// as a normal delete, and their orphaned test accounts. It reports how many went.
func ExpireTestWorkspaces(ctx context.Context, d *deps.Deps) (int, error) {
	ops := core.NewOps(d)
	done := 0
	for {
		rows, err := d.DB.Pool.Query(ctx, `SELECT id FROM workspaces WHERE test AND test_expires_at <= now() ORDER BY test_expires_at LIMIT 50`)
		if err != nil {
			return done, err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil || len(ids) == 0 {
			return done, err
		}
		for _, wid := range ids {
			var removals []core.Removal
			var gone bool
			err := d.DB.Tx(ctx, func(tx pgx.Tx) error {
				var due bool
				err := tx.QueryRow(ctx, `SELECT test AND test_expires_at <= now() FROM workspaces WHERE id = $1 FOR UPDATE`, wid).Scan(&due)
				if db.IsNotFound(err) || (err == nil && !due) {
					return nil
				}
				if err != nil {
					return err
				}
				var n int
				if removals, n, err = DeleteTestWorkspace(ctx, tx, ops, &auth.Principal{}, wid); err != nil {
					return err
				}
				gone = true
				return writeAudit(ctx, tx, SystemActor.Type, nil, SystemActor.Email, "test_workspace.expired", "workspace", wid.String(),
					map[string]any{"accounts_deleted": n}, "")
			})
			if err != nil {
				return done, err
			}
			for _, r := range removals {
				ops.Finish(r)
			}
			if gone {
				done++
				d.Log.Info("expired test workspace deleted", "workspace_id", wid)
			}
		}
		if len(ids) < 50 {
			return done, nil
		}
	}
}

// nullableDays is a body field that tells "absent" from null.
type nullableDays struct {
	Set   bool
	Null  bool
	Value int
}

func (n *nullableDays) UnmarshalJSON(b []byte) error {
	n.Set = true
	if string(b) == "null" {
		n.Null = true
		return nil
	}
	return json.Unmarshal(b, &n.Value)
}

func (nullableDays) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: huma.TypeInteger, Nullable: true}
}

type testWorkspaceChangeBody struct {
	Code            string       `json:"code,omitempty" maxLength:"64" doc:"A current authenticator code (or a recovery code) of the acting admin"`
	DeleteAfterDays nullableDays `json:"delete_after_days,omitempty" doc:"1..365 days from now; null keeps the workspace"`
	PasswordDays    *int         `json:"password_days,omitempty" doc:"Renews the owner's password: a new one, working for 1..90 days"`
}

func (s *svc) registerTestWorkspaces(api huma.API) {
	op(s, api, huma.Operation{OperationID: "admin-list-test-workspaces", Method: http.MethodGet, Path: "/v1/admin/test-workspaces",
		Summary: "List test workspaces"},
		func(ctx context.Context, in *struct{ httpx.ListParams }) (*struct {
			Body httpx.Page[AdminTestWorkspace]
		}, error) {
			if _, err := require(ctx, PermRead); err != nil {
				return nil, err
			}
			var cur struct {
				T  time.Time `json:"t"`
				ID uuid.UUID `json:"id"`
			}
			if err := httpx.DecodeCursor(in.Cursor, &cur); err != nil {
				return nil, err
			}
			list, err := ListTestWorkspaces(ctx, s.d.DB.Pool, cur.T, cur.ID, in.Limit+1)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			page := httpx.NewPage(list, in.Limit, func(x AdminTestWorkspace) any { return map[string]any{"t": x.CreatedAt, "id": x.Workspace.ID} })
			return &struct {
				Body httpx.Page[AdminTestWorkspace]
			}{page}, nil
		})

	op(s, api, huma.Operation{OperationID: "admin-create-test-workspace", Method: http.MethodPost, Path: "/v1/admin/test-workspaces",
		Summary: "Create a test workspace", DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *struct {
			Body struct {
				Code  string `json:"code,omitempty" maxLength:"64" doc:"A current authenticator code (or a recovery code) of the acting admin"`
				Name  string `json:"name" maxLength:"200"`
				Owner struct {
					Email  string `json:"email,omitempty" maxLength:"254" doc:"An existing account's address, or a new test account's ending in .invalid"`
					UserID string `json:"user_id,omitempty" maxLength:"36"`
					Name   string `json:"name,omitempty" maxLength:"200" doc:"A new test account's name"`
				} `json:"owner"`
				PasswordDays    *int           `json:"password_days,omitempty" doc:"How long a new account's password works, 1..90 days (default 30)"`
				Plan            string         `json:"plan" enum:"free,pro,collab"`
				Sample          *SampleRequest `json:"sample,omitempty"`
				DeleteAfterDays nullableDays   `json:"delete_after_days,omitempty" doc:"Deleted after 1..365 days (default 30); null keeps it"`
				Reason          string         `json:"reason" minLength:"1" maxLength:"500"`
			}
		}) (*struct{ Body TestWorkspaceResult }, error) {
			a, err := require(ctx, PermWorkspaceTest)
			if err != nil {
				return nil, err
			}
			if err := s.limit("admin:testws:"+a.UserID.String(), 30, time.Hour); err != nil {
				return nil, err
			}
			b := in.Body
			x := TestWorkspaceInput{Name: b.Name, OwnerEmail: b.Owner.Email, OwnerName: b.Owner.Name, Plan: b.Plan, Sample: b.Sample, Reason: b.Reason}
			if b.Owner.UserID != "" {
				if x.OwnerUserID, err = uuid.Parse(b.Owner.UserID); err != nil {
					return nil, httpx.E(404, "user_not_found", "No account has this id.")
				}
			}
			if b.PasswordDays != nil {
				if err := checkDays(b.PasswordDays, maxPasswordDays, "password_days"); err != nil {
					return nil, err
				}
				x.PasswordDays = *b.PasswordDays
			}
			switch {
			case !b.DeleteAfterDays.Set:
				n := DefaultDeleteAfterDays
				x.DeleteAfterDays = &n
			case !b.DeleteAfterDays.Null:
				x.DeleteAfterDays = &b.DeleteAfterDays.Value
			}
			// The code is checked, and its step consumed, before anything is made.
			if err := s.d.DB.Tx(ctx, func(tx pgx.Tx) error { return s.stepUp(ctx, tx, a, b.Code) }); err != nil {
				return nil, httpx.Wrap(err)
			}
			res, err := CreateTestWorkspace(ctx, s.d, x, a.actor())
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			s.d.Log.Info("test workspace created", "workspace_id", res.Workspace.Workspace.ID, "admin_user_id", a.UserID)
			return &struct{ Body TestWorkspaceResult }{res}, nil
		})

	op(s, api, huma.Operation{OperationID: "admin-change-test-workspace", Method: http.MethodPatch, Path: "/v1/admin/test-workspaces/{id}",
		Summary: "Extend a test workspace or renew its owner's password"},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body testWorkspaceChangeBody
		}) (*struct{ Body TestWorkspaceResult }, error) {
			a, err := require(ctx, PermWorkspaceTest)
			if err != nil {
				return nil, err
			}
			wid, err := parseID(in.ID, "Test workspace")
			if err != nil {
				return nil, err
			}
			b := in.Body
			if !b.DeleteAfterDays.Set && b.PasswordDays == nil {
				return nil, httpx.Validation("Give delete_after_days or password_days.")
			}
			if b.DeleteAfterDays.Set && !b.DeleteAfterDays.Null {
				if err := checkDays(&b.DeleteAfterDays.Value, maxDeleteAfterDays, "delete_after_days"); err != nil {
					return nil, err
				}
			}
			if err := checkDays(b.PasswordDays, maxPasswordDays, "password_days"); err != nil {
				return nil, err
			}
			var password, hash string
			if b.PasswordDays != nil {
				if password, err = GeneratePassword(); err != nil {
					return nil, httpx.Wrap(err)
				}
				if hash, err = auth.HashPassword(password); err != nil {
					return nil, httpx.Wrap(err)
				}
			}
			now := time.Now().UTC()
			var out TestWorkspaceResult
			var renewed *uuid.UUID
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if err := s.stepUp(ctx, tx, a, b.Code); err != nil {
					return err
				}
				var testPlan string
				err := tx.QueryRow(ctx, `SELECT COALESCE(test_plan, 'free') FROM workspaces WHERE id = $1 AND test FOR UPDATE`, wid).Scan(&testPlan)
				if db.IsNotFound(err) {
					return httpx.NotFound("Test workspace")
				}
				if err != nil {
					return err
				}
				data := map[string]any{}
				if b.DeleteAfterDays.Set {
					var expires *time.Time
					if !b.DeleteAfterDays.Null {
						t := now.Add(days(b.DeleteAfterDays.Value))
						expires = &t
						data["delete_after_days"] = b.DeleteAfterDays.Value
					} else {
						data["delete_after_days"] = nil
					}
					data["test_expires_at"] = expires
					if _, err := tx.Exec(ctx, `UPDATE workspaces SET test_expires_at = $2, updated_at = now() WHERE id = $1`, wid, expires); err != nil {
						return err
					}
					if testPlan == plan.Collab {
						if err := applyTestPlan(ctx, s.d, tx, wid, plan.Collab, expires); err != nil {
							return err
						}
					}
				}
				if b.PasswordDays != nil {
					cur, err := loadTestWorkspace(ctx, tx, wid)
					if err != nil {
						return err
					}
					if cur.Owner == nil || !cur.Owner.TestAccount {
						return httpx.E(409, "not_test_account", "The owner's own account keeps its own password; only a test account's is renewed.")
					}
					if err := s.ops.SetTestPassword(ctx, tx, a.principal(), cur.Owner.ID, hash, now.Add(days(*b.PasswordDays))); err != nil {
						return err
					}
					renewed = &cur.Owner.ID
					data["password_renewed"], data["password_days"], data["owner_id"] = true, *b.PasswordDays, cur.Owner.ID
				}
				if err := s.audit(ctx, tx, a, "test_workspace.changed", "workspace", wid.String(), data); err != nil {
					return err
				}
				out.Workspace, err = loadTestWorkspace(ctx, tx, wid)
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if o := out.Workspace.Owner; o != nil {
				out.Owner = TestCredentials{ID: o.ID, Email: o.Email, PasswordExpiresAt: o.PasswordExpiresAt}
				out.SignInURL = signInURL(s.d, o.Email)
			}
			if renewed != nil {
				out.Owner.Password = &password
				s.ops.Disconnect(*renewed, uuid.Nil)
			}
			return &struct{ Body TestWorkspaceResult }{out}, nil
		})

	op(s, api, huma.Operation{OperationID: "admin-add-sample-data", Method: http.MethodPost, Path: "/v1/admin/workspaces/{id}/sample-data",
		Summary: "Add sample data to a test workspace", DefaultStatus: http.StatusAccepted},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body SampleRequest
		}) (*struct{ Body AdminTestWorkspace }, error) {
			a, err := require(ctx, PermWorkspaceTest)
			if err != nil {
				return nil, err
			}
			wid, err := parseID(in.ID, "Workspace")
			if err != nil {
				return nil, err
			}
			if err := s.limit("admin:sample:"+a.UserID.String(), 60, time.Hour); err != nil {
				return nil, err
			}
			var out AdminTestWorkspace
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if err := QueueSample(ctx, s.d, tx, wid, in.Body, a.actor()); err != nil {
					return err
				}
				out, err = loadTestWorkspace(ctx, tx, wid)
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body AdminTestWorkspace }{out}, nil
		})
}
