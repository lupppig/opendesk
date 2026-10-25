package imports

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/email"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/helpdesk/imports/source"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/plan"
)

// Limits on what one message brings.
const (
	maxBodyHTML       = 1 << 20
	maxBodyText       = 200_000
	maxRecipients     = 50
	maxMessages       = 2000
	articleExcerptLen = 200
)

// errStopped means the import was paused, canceled or moved on to another generation
// while a write waited: nothing was written.
var errStopped = errors.New("import stopped")

// writer writes one run's tickets and articles. It checks the mapping against the
// workspace once, and keeps what it looked up for the rest of the run.
type writer struct {
	s   *Service
	x   *row
	src source.Source
	// agents are the source's agents by id, for their email.
	agents   map[string]source.Agent
	members  map[uuid.UUID]bool
	byEmail  map[string]uuid.UUID // member user ids by lowercased email
	teams    map[uuid.UUID]bool
	mailbox  *uuid.UUID
	contacts map[string]uuid.UUID
	tags     map[string]*uuid.UUID // lowercased name -> label id; nil = cannot be used
	// lastPublish is when progress was last announced.
	lastPublish time.Time
	// limits are the plan's file limits; used is what imports already copied into the
	// workspace, read when there is a limit on it.
	limits fileLimits
	used   int64
	// copyUntil is when the run stops copying files: a ticket whose files take longer goes
	// on in the next run. Zero is no end.
	copyUntil time.Time
}

func (s *Service) newWriter(ctx context.Context, x *row, src source.Source, scan source.Scan) (*writer, error) {
	w := &writer{s: s, x: x, src: src, agents: map[string]source.Agent{}, members: map[uuid.UUID]bool{}, byEmail: map[string]uuid.UUID{},
		teams: map[uuid.UUID]bool{}, contacts: map[string]uuid.UUID{}, tags: map[string]*uuid.UUID{}}
	for _, a := range scan.Agents {
		w.agents[a.ID] = a
	}
	rows, err := s.d.DB.Pool.Query(ctx, `SELECT m.user_id, lower(u.email) FROM members m JOIN users u ON u.id = m.user_id WHERE m.workspace_id = $1`, x.WorkspaceID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id uuid.UUID
		var email string
		if err := rows.Scan(&id, &email); err != nil {
			rows.Close()
			return nil, err
		}
		w.members[id] = true
		w.byEmail[email] = id
	}
	rows.Close()
	tids, err := pgxCollect[uuid.UUID](s.d.DB.Pool.Query(ctx, `SELECT id FROM teams WHERE workspace_id = $1`, x.WorkspaceID))
	if err != nil {
		return nil, err
	}
	for _, id := range tids {
		w.teams[id] = true
	}
	e, err := s.d.Plan.Entitlements(ctx, s.d.DB.Pool, x.WorkspaceID)
	if err != nil {
		return nil, err
	}
	w.limits = fileLimitsOf(e)
	if s.fileLimits != nil {
		w.limits = s.fileLimits(e)
	}
	if w.limits.total > 0 {
		if err := s.d.DB.Pool.QueryRow(ctx, `SELECT COALESCE(sum(size_bytes), 0)::bigint FROM attachments
			WHERE workspace_id = $1 AND imported`, x.WorkspaceID).Scan(&w.used); err != nil {
			return nil, err
		}
	}
	if x.Options.MailboxID != nil {
		var ok bool
		if err := s.d.DB.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM mailboxes WHERE workspace_id = $1 AND id = $2)`,
			x.WorkspaceID, *x.Options.MailboxID).Scan(&ok); err != nil {
			return nil, err
		}
		if ok {
			w.mailbox = x.Options.MailboxID
		}
	}
	return w, nil
}

func pgxCollect[T any](rows pgx.Rows, err error) ([]T, error) {
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[T])
}

// agentUser is the member an agent maps to: the mapping's choice when it made one (null
// keeps the name only), else the member with the agent's email.
func (w *writer) agentUser(id, email string) *uuid.UUID {
	if id != "" {
		if v, ok := w.x.Mapping.Agents[id]; ok {
			if v != nil && w.members[*v] {
				return v
			}
			return nil
		}
		if a, ok := w.agents[id]; ok && email == "" {
			email = a.Email
		}
	}
	if uid, ok := w.byEmail[strings.ToLower(strings.TrimSpace(email))]; ok && email != "" {
		return &uid
	}
	return nil
}

// team is the team a source group maps to, or none.
func (w *writer) team(group string) *uuid.UUID {
	if group == "" {
		return nil
	}
	if v, ok := w.x.Mapping.Groups[group]; ok && v != nil && w.teams[*v] {
		return v
	}
	return nil
}

// record is what an import wrote for a source id.
type record struct {
	localID   uuid.UUID
	updatedAt *time.Time
}

func (w *writer) lookup(ctx context.Context, q db.Querier, kind, externalID string) (*record, error) {
	var r record
	err := q.QueryRow(ctx, `SELECT local_id, source_updated_at FROM import_records
		WHERE workspace_id = $1 AND source = $2 AND account_key = $3 AND kind = $4 AND external_id = $5`,
		w.x.WorkspaceID, w.x.Source, w.x.AccountKey, kind, externalID).Scan(&r.localID, &r.updatedAt)
	if db.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (w *writer) remember(ctx context.Context, q db.Querier, kind, externalID string, localID uuid.UUID, created bool, updatedAt *time.Time) error {
	_, err := q.Exec(ctx, `INSERT INTO import_records (workspace_id, source, account_key, kind, external_id, local_id, import_id, created, source_updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (workspace_id, source, account_key, kind, external_id) DO UPDATE SET local_id = EXCLUDED.local_id,
			source_updated_at = COALESCE(EXCLUDED.source_updated_at, import_records.source_updated_at)`,
		w.x.WorkspaceID, w.x.Source, w.x.AccountKey, kind, externalID, localID, w.x.ID, created, updatedAt)
	return err
}

// begin locks the import for one write and confirms it still runs in this generation.
func (w *writer) begin(ctx context.Context, tx pgx.Tx) error {
	var status string
	var gen int
	if err := tx.QueryRow(ctx, `SELECT status, generation FROM imports WHERE id = $1 FOR UPDATE`, w.x.ID).Scan(&status, &gen); err != nil {
		return err
	}
	if status != StatusRunning || gen != w.x.Generation {
		return errStopped
	}
	return nil
}

// progress stores the counts and problems with the write, and announces them at most
// once a second.
func (w *writer) progress(ctx context.Context, tx pgx.Tx, force bool) error {
	if _, err := tx.Exec(ctx, `UPDATE imports SET counts = $2, problems = $3, failures = 0, waiting_until = NULL, updated_at = now()
		WHERE id = $1`, w.x.ID, w.x.Counts, w.x.Problems); err != nil {
		return err
	}
	w.x.Failures, w.x.WaitingUntil = 0, nil
	if !force && time.Since(w.lastPublish) < time.Second {
		return nil
	}
	w.lastPublish = time.Now()
	x, err := loadRow(ctx, tx, w.x.WorkspaceID, w.x.ID)
	if err != nil {
		return err
	}
	return publish(ctx, tx, "import.updated", x)
}

func (w *writer) problem(item, msg string) {
	w.x.Problems = addProblem(w.x.Problems, ImportProblem{At: time.Now().UTC(), Item: item, Message: msg})
}

func addProblem(list []ImportProblem, p ImportProblem) []ImportProblem {
	list = append([]ImportProblem{p}, list...)
	if len(list) > maxProblems {
		list = list[:maxProblems]
	}
	return list
}

// copied is an attachment stored here for a source attachment.
type copied struct {
	id     uuid.UUID
	inline bool
	url    string // the source's link, rewritten in the body
}

// fileLimits are what imports copy of one message's files and of the workspace's in all
// (plan.Entitlements); zero is unlimited. Every file is also within hd.MaxAttachmentBytes.
type fileLimits struct {
	perMessage   int
	messageBytes int64
	total        int64
	// upgrade says a paid plan raises them.
	upgrade bool
}

func fileLimitsOf(e plan.Entitlements) fileLimits {
	return fileLimits{perMessage: e.ImportFilesPerMessage, messageBytes: e.ImportMessageBytes, total: e.ImportBytes,
		upgrade: e.BillingEnabled && e.Plan == plan.Free}
}

// hint ends a problem about a limit a paid plan raises.
func (l fileLimits) hint() string {
	if l.upgrade {
		return " A paid plan raises this limit."
	}
	return ""
}

var (
	// errFull means a file does not fit in what is left of its message's or the
	// workspace's limit.
	errFull = errors.New("over the import's file limits")
	// errOutOfTime means a run spent its time copying a ticket's files. The next run goes
	// on with the files copied so far.
	errOutOfTime = errors.New("the run is out of time")
)

// room is what is left of limit after used; unlimited when limit is zero.
func room(limit, used int64) int64 {
	if limit <= 0 {
		return math.MaxInt64
	}
	return max(limit-used, 0)
}

// copyFiles stores the attachments of the messages that will be written, before the
// write's transaction. A file that cannot be copied is a problem; a rate limit or a
// refused credential stops the ticket so it is tried again, without copying again the
// files it already has. Files past the plan's limits are left out, one problem per
// message and limit.
func (w *writer) copyFiles(ctx context.Context, t *source.Ticket, msgs []source.Message) (map[string][]copied, error) {
	out := map[string][]copied{}
	if !w.x.Options.Attachments {
		return out, nil
	}
	item := "Ticket " + t.Ref
	lim := w.limits
	for _, m := range msgs {
		files := m.Attachments
		if extra := len(files) - lim.perMessage; lim.perMessage > 0 && extra > 0 {
			w.problem(item, fmt.Sprintf("%d %s past a message's first %d %s left out.%s", extra, plural(extra, "file", "files"),
				lim.perMessage, plural(extra, "was", "were"), lim.hint()))
			files = files[:lim.perMessage]
		}
		var size int64 // what the message's files take so far
		overMessage, overTotal := 0, 0
		for i, a := range files {
			if a.Size > hd.MaxAttachmentBytes {
				w.problem(item, fmt.Sprintf("%s is over 25 MB, so it was left out.", hd.CleanFilename(a.Name)))
				continue
			}
			msgRoom, totalRoom := room(lim.messageBytes, size), room(lim.total, w.used)
			c, n, err := w.copyFile(ctx, m.ID+"/"+cmp.Or(a.ID, strconv.Itoa(i)), a, msgRoom, totalRoom)
			if errors.Is(err, errFull) {
				if msgRoom <= totalRoom {
					overMessage++
				} else {
					overTotal++
				}
				continue
			}
			if err != nil {
				if stopping(err) {
					return nil, err
				}
				w.problem(item, fmt.Sprintf("%s could not be copied: %s", hd.CleanFilename(a.Name), plain(err)))
				continue
			}
			size += n
			c.inline, c.url = a.Inline, a.URL
			out[m.ID] = append(out[m.ID], c)
			w.x.Counts.Attachments++
		}
		if overMessage > 0 {
			w.problem(item, fmt.Sprintf("%d %s left out: one message brings up to %s of files.%s",
				overMessage, plural(overMessage, "file was", "files were"), sizeText(lim.messageBytes), lim.hint()))
		}
		if overTotal > 0 {
			// Once the workspace's limit is reached every later file is left out: it is said once.
			msg := fmt.Sprintf("Files were left out from here on: imports bring up to %s of files into a workspace.%s",
				sizeText(lim.total), lim.hint())
			if !slices.ContainsFunc(w.x.Problems, func(p ImportProblem) bool { return p.Message == msg }) {
				w.problem(item, msg)
			}
		}
	}
	return out, nil
}

// plural is one when n is 1, else many.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// sizeText is a limit in MB, or in GB when it is whole gigabytes.
func sizeText(n int64) string {
	if n >= 1<<30 && n%(1<<30) == 0 {
		return fmt.Sprintf("%d GB", n>>30)
	}
	return fmt.Sprintf("%d MB", n>>20)
}

// copyFile stores one file that fits in msgRoom and totalRoom, and remembers it under key
// (the message and the file) so a ticket tried again finds it instead of copying it again.
// It answers the file and its size.
func (w *writer) copyFile(ctx context.Context, key string, a source.Attachment, msgRoom, totalRoom int64) (copied, int64, error) {
	if c, n, ok, err := w.copiedBefore(ctx, key); err != nil {
		return copied{}, 0, err
	} else if ok {
		// It already counts against the workspace's limit.
		if n > msgRoom {
			return copied{}, 0, errFull
		}
		return c, n, nil
	}
	limit := min(msgRoom, totalRoom)
	if limit == 0 || a.Size > limit {
		return copied{}, 0, errFull
	}
	var data []byte
	if a.Data != nil {
		data = a.Data
	} else {
		if !w.copyUntil.IsZero() && time.Now().After(w.copyUntil) {
			return copied{}, 0, errOutOfTime
		}
		rc, err := w.src.Download(ctx, a)
		if err != nil {
			return copied{}, 0, err
		}
		defer rc.Close()
		if data, err = io.ReadAll(io.LimitReader(rc, min(int64(hd.MaxAttachmentBytes), limit)+1)); err != nil {
			return copied{}, 0, err
		}
	}
	if len(data) > hd.MaxAttachmentBytes {
		return copied{}, 0, errors.New("it is over 25 MB")
	}
	if int64(len(data)) > limit {
		return copied{}, 0, errFull
	}
	if len(data) == 0 {
		return copied{}, 0, errors.New("the file is empty")
	}
	att, err := hd.SaveUpload(ctx, w.s.d.Storage, w.s.d.DB.Pool, hd.Upload{WorkspaceID: w.x.WorkspaceID, Filename: a.Name,
		Size: int64(len(data)), Body: bytes.NewReader(data), Imported: true})
	if err != nil {
		return copied{}, 0, err
	}
	w.used += att.SizeBytes
	if err := w.remember(ctx, w.s.d.DB.Pool, "file", key, att.ID, true, nil); err != nil {
		return copied{}, 0, err
	}
	return copied{id: att.ID}, att.SizeBytes, nil
}

// copiedBefore finds a file an earlier run copied under key and no message holds yet.
// One that is gone (unattached uploads are purged after a day) is copied again.
func (w *writer) copiedBefore(ctx context.Context, key string) (copied, int64, bool, error) {
	r, err := w.lookup(ctx, w.s.d.DB.Pool, "file", key)
	if err != nil || r == nil {
		return copied{}, 0, false, err
	}
	var n int64
	err = w.s.d.DB.Pool.QueryRow(ctx, `SELECT size_bytes FROM attachments WHERE workspace_id = $1 AND id = $2 AND message_id IS NULL`,
		w.x.WorkspaceID, r.localID).Scan(&n)
	if db.IsNotFound(err) {
		return copied{}, 0, false, nil
	}
	if err != nil {
		return copied{}, 0, false, err
	}
	return copied{id: r.localID}, n, true, nil
}

// stopping reports errors that stop the run rather than one item: the source's rate
// limit, refused credentials, the source not answering, our own shutdown, the run's time.
func stopping(err error) bool {
	var rl *source.RateLimited
	var un *source.Unreachable
	var re *source.RemoteError
	return errors.As(err, &rl) || errors.Is(err, source.ErrAuth) || errors.As(err, &un) || errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errStopped) || errors.Is(err, errOutOfTime) || (errors.As(err, &re) && re.Status >= 500)
}

// plain is an error in words a person can read in a list of problems.
func plain(err error) string {
	var he *httpx.Error
	if errors.As(err, &he) {
		return he.Detail
	}
	var re *source.RemoteError
	if errors.As(err, &re) {
		return fmt.Sprintf("the source answered %d", re.Status)
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return "it could not be saved here" // the cause is logged, never shown
	}
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}

// outcome is what writing one ticket did.
type outcome int

const (
	outcomeCreated outcome = iota
	outcomeUpdated
	outcomeUnchanged
	outcomeSkipped
)

// writeTicket writes one source ticket, or skips it when the source marks it to leave out.
// Ticket writes share a transaction with the import's progress.
func (w *writer) writeTicket(ctx context.Context, t *source.Ticket) (outcome, error) {
	if t.Skip {
		// A sync hears of a ticket the source has since trashed or merged away: the
		// copy an earlier run made is closed, not left open.
		if w.x.UpdatedSince != nil {
			return outcomeSkipped, w.retire(ctx, t)
		}
		return outcomeSkipped, nil
	}
	prev, err := w.lookup(ctx, w.s.d.DB.Pool, "ticket", t.ID)
	if err != nil {
		return 0, err
	}
	if prev != nil && prev.updatedAt != nil && !t.UpdatedAt.IsZero() && !t.UpdatedAt.After(*prev.updatedAt) {
		return outcomeUnchanged, nil
	}
	if !t.Loaded {
		if err := w.src.Load(ctx, t); err != nil {
			return 0, err
		}
	}
	if t.Skip {
		if prev != nil {
			return outcomeSkipped, w.retire(ctx, t)
		}
		return outcomeSkipped, nil
	}
	scrubTicket(t)
	msgs := t.Messages
	if len(msgs) > maxMessages {
		w.problem("Ticket "+t.Ref, fmt.Sprintf("Only the first %d messages were imported.", maxMessages))
		msgs = msgs[:maxMessages]
	}
	if prev != nil {
		// Only messages no run wrote yet need their files.
		var fresh []source.Message
		for _, m := range msgs {
			r, err := w.lookup(ctx, w.s.d.DB.Pool, "message", m.ID)
			if err != nil {
				return 0, err
			}
			if r == nil {
				fresh = append(fresh, m)
			}
		}
		msgs = fresh
	}
	counts := w.x.Counts
	files, err := w.copyFiles(ctx, t, msgs)
	if err != nil {
		w.x.Counts = counts
		return 0, err
	}
	var result outcome
	err = w.s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		if err := w.begin(ctx, tx); err != nil {
			return err
		}
		var err error
		result, err = w.writeTicketTx(ctx, tx, t, msgs, files)
		if err != nil {
			return err
		}
		return w.progress(ctx, tx, false)
	})
	if err != nil {
		// Nothing of the ticket was written: its counts go, and so do the contacts and
		// labels the run remembered, since the ones it created in the write are gone too.
		w.x.Counts = counts
		w.contacts, w.tags = map[string]uuid.UUID{}, map[string]*uuid.UUID{}
	}
	return result, err
}

func (w *writer) writeTicketTx(ctx context.Context, tx pgx.Tx, t *source.Ticket, msgs []source.Message, files map[string][]copied) (outcome, error) {
	wid := w.x.WorkspaceID
	prev, err := w.lookup(ctx, tx, "ticket", t.ID)
	if err != nil {
		return 0, err
	}
	var updatedAt *time.Time
	if !t.UpdatedAt.IsZero() {
		u := t.UpdatedAt
		updatedAt = &u
	}
	var ticketID uuid.UUID
	var contactID *uuid.UUID
	result := outcomeCreated
	if prev != nil {
		var cid *uuid.UUID
		err := tx.QueryRow(ctx, `SELECT contact_id FROM tickets WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL`, wid, prev.localID).Scan(&cid)
		if db.IsNotFound(err) {
			// Deleted here since: it stays deleted.
			return outcomeUnchanged, w.remember(ctx, tx, "ticket", t.ID, prev.localID, false, updatedAt)
		}
		if err != nil {
			return 0, err
		}
		ticketID, contactID, result = prev.localID, cid, outcomeUpdated
	} else {
		if contactID, err = w.contact(ctx, tx, t.Requester); err != nil {
			return 0, err
		}
		channel := t.Channel
		if !slices.Contains(domain.Channels, channel) {
			channel = domain.ChannelEmail
		}
		priority := t.Priority
		if !slices.Contains(domain.Priorities, priority) {
			priority = "normal"
		}
		var mailbox *uuid.UUID
		if channel == domain.ChannelEmail {
			mailbox = w.mailbox
		}
		subject := strings.TrimSpace(t.Subject)
		if subject == "" && len(msgs) > 0 {
			subject = firstWords(firstNonEmpty(msgs[0].Text, hd.HTMLToText(msgs[0].HTML)), 80)
		}
		created := t.CreatedAt
		if created.IsZero() && len(msgs) > 0 {
			created = msgs[0].CreatedAt
		}
		nt, err := domain.CreateTicket(ctx, tx, domain.NewTicket{WorkspaceID: wid, Subject: subject, Channel: channel, Priority: priority,
			MailboxID: mailbox, ContactID: contactID, AssigneeID: w.agentUser(t.AssigneeID, ""), TeamID: w.team(t.GroupID),
			Custom:   map[string]any{"import": map[string]any{"source": w.x.Source, "id": t.ID, "ref": t.Ref, "url": t.URL, "import_id": w.x.ID}},
			Imported: true, CreatedAt: created})
		if err != nil {
			return 0, err
		}
		ticketID = nt.ID
	}

	var requesterEmail string
	if t.Requester != nil {
		requesterEmail = strings.ToLower(strings.TrimSpace(t.Requester.Email))
	}
	added := 0
	for _, m := range msgs {
		if prev != nil {
			r, err := w.lookup(ctx, tx, "message", m.ID)
			if err != nil {
				return 0, err
			}
			if r != nil {
				continue
			}
		}
		nm, ok, err := w.message(ctx, tx, ticketID, contactID, requesterEmail, m, files[m.ID], t.Subject, t.Channel == "email")
		if err != nil {
			return 0, err
		}
		if !ok {
			continue
		}
		msg, err := domain.AddMessage(ctx, tx, w.s.d.Jobs, nm)
		if err != nil {
			return 0, err
		}
		if inline := inlineIDs(files[m.ID]); len(inline) > 0 {
			if _, err := tx.Exec(ctx, `UPDATE attachments SET inline = true WHERE workspace_id = $1 AND message_id = $2 AND id = ANY($3)`,
				wid, msg.ID, inline); err != nil {
				return 0, err
			}
		}
		if err := w.remember(ctx, tx, "message", m.ID, msg.ID, true, nil); err != nil {
			return 0, err
		}
		added++
	}
	w.x.Counts.Messages += added

	if prev != nil {
		// The source may have assigned the ticket since an earlier run; one it leaves
		// unassigned keeps whoever has it here.
		if a := w.agentUser(t.AssigneeID, ""); a != nil {
			if _, err := tx.Exec(ctx, `UPDATE tickets SET assignee_id = $3 WHERE workspace_id = $1 AND id = $2 AND assignee_id IS DISTINCT FROM $3`,
				wid, ticketID, *a); err != nil {
				return 0, err
			}
		}
	}
	if err := w.settle(ctx, tx, ticketID, t); err != nil {
		return 0, err
	}
	if err := w.label(ctx, tx, ticketID, t); err != nil {
		return 0, err
	}
	if err := w.remember(ctx, tx, "ticket", t.ID, ticketID, result == outcomeCreated, updatedAt); err != nil {
		return 0, err
	}
	switch result {
	case outcomeCreated:
		w.x.Counts.Tickets++
	case outcomeUpdated:
		w.x.Counts.Updated++
	}
	return result, nil
}

func inlineIDs(cs []copied) []uuid.UUID {
	var out []uuid.UUID
	for _, c := range cs {
		if c.inline {
			out = append(out, c.id)
		}
	}
	return out
}

// message turns a source message into a quiet one for AddMessage. ok is false for a
// message with nothing in it.
func (w *writer) message(ctx context.Context, tx pgx.Tx, ticketID uuid.UUID, requester *uuid.UUID, requesterEmail string,
	m source.Message, files []copied, subject string, emailTicket bool) (domain.NewMessage, bool, error) {
	body, full, text := w.body(m, files, subject, emailTicket && !m.Note)
	if text == "" && len(files) == 0 && !strings.Contains(body, "<img") {
		return domain.NewMessage{}, false, nil
	}
	created := m.CreatedAt
	if created.IsZero() || created.After(time.Now()) {
		created = time.Now().UTC()
	}
	email := strings.ToLower(strings.TrimSpace(m.Author.Email))
	if !looksLikeEmail(email) {
		email = ""
	}
	nm := domain.NewMessage{WorkspaceID: w.x.WorkspaceID, TicketID: ticketID, Kind: "reply", BodyHTML: body, BodyText: text, FullHTML: full,
		FromEmail: email, FromName: displayname.CleanOr(m.Author.Name, displayname.Person, ""), To: recipients(m.To), Cc: recipients(m.Cc),
		CreatedAt: created, Imported: true, InReplyTo: m.InReplyTo, References: m.References}
	if m.MessageID != "" {
		// A Message-ID another message already holds (the live email, or a row repeating
		// it) stays with that one.
		var taken bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM messages WHERE workspace_id = $1 AND email_message_id = $2)`,
			w.x.WorkspaceID, m.MessageID).Scan(&taken); err != nil {
			return nm, false, err
		}
		if !taken {
			id := m.MessageID
			nm.EmailMessageID = &id
		}
	}
	for _, f := range files {
		nm.AttachmentIDs = append(nm.AttachmentIDs, f.id)
	}
	switch {
	case m.Agent || m.Note:
		nm.Direction, nm.AuthorType = "outbound", "user"
		if m.Note {
			nm.Kind = "note"
		}
		if m.Agent {
			nm.AuthorUserID = w.agentUser(m.AuthorID, email)
			if nm.FromName == "" {
				if a, ok := w.agents[m.AuthorID]; ok {
					nm.FromName = displayname.CleanOr(a.Name, displayname.Person, "")
					if nm.FromEmail == "" {
						nm.FromEmail = strings.ToLower(a.Email)
					}
				}
			}
		}
		if nm.AuthorUserID != nil {
			u, err := domain.LoadUserRef(ctx, tx, *nm.AuthorUserID)
			if err != nil {
				return nm, false, err
			}
			nm.FromName, nm.FromEmail = u.Name, u.Email
		}
	default:
		nm.Direction, nm.AuthorType = "inbound", "contact"
		nm.AuthorContactID = requester
		if email != "" && email != requesterEmail {
			id, err := w.contact(ctx, tx, &source.Person{Email: email, Name: m.Author.Name})
			if err != nil {
				return nm, false, err
			}
			nm.AuthorContactID = id
		}
		if nm.FromEmail == "" {
			nm.FromEmail = requesterEmail
		}
	}
	return nm, true, nil
}

// body is a message's sanitized HTML and its text, with the source's links to copied
// files pointing at the copies. For an email reply (quote) whose quoted history or
// signature can be cut, the way an inbound email's is, body and text are what the author
// wrote and full is the whole original, so the ticket shows the reply with the rest a
// click away; otherwise full is "".
func (w *writer) body(m source.Message, files []copied, subject string, quote bool) (body, full, text string) {
	raw := m.HTML
	if strings.TrimSpace(raw) == "" {
		raw = hd.TextToHTML(m.Text)
	}
	for _, f := range files {
		if f.url == "" {
			continue
		}
		u := domain.AttachmentURL(f.id)
		raw = strings.ReplaceAll(raw, f.url, u)
		if esc := html.EscapeString(f.url); esc != f.url {
			raw = strings.ReplaceAll(raw, esc, html.EscapeString(u))
		}
	}
	body = hd.SanitizeEmailHTML(raw)
	text = hd.HTMLToText(body)
	if strings.TrimSpace(m.HTML) == "" {
		text = strings.TrimSpace(m.Text)
	}
	if quote {
		if visible, f, plain := email.SplitQuoted(raw, m.Text, subject); f != "" {
			body, full, text = visible, f, plain
		}
	}
	if len(body) > maxBodyHTML {
		body = hd.SanitizeEmailHTML(hd.TextToHTML(hd.Truncate(text, maxBodyHTML/4)))
	}
	return body, full, hd.Truncate(text, maxBodyText)
}

func recipients(in []string) []string {
	out := []string{}
	for _, r := range in {
		r = strings.ToLower(strings.TrimSpace(r))
		if looksLikeEmail(r) && !slices.Contains(out, r) {
			out = append(out, r)
		}
		if len(out) >= maxRecipients {
			break
		}
	}
	return out
}

// retire closes the copy of a ticket the source has since left out (trashed, spam, or
// merged into another). A ticket never imported, or already solved or closed, is left be.
func (w *writer) retire(ctx context.Context, t *source.Ticket) error {
	prev, err := w.lookup(ctx, w.s.d.DB.Pool, "ticket", t.ID)
	if err != nil || prev == nil {
		return err
	}
	_, err = w.s.d.DB.Pool.Exec(ctx, `UPDATE tickets SET status = 'closed',
		solved_at = COALESCE(solved_at, now()), closed_at = COALESCE(closed_at, now()),
		waiting_since = NULL, unanswered_count = 0, snoozed_until = NULL, updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL AND status NOT IN ('solved', 'closed')`,
		w.x.WorkspaceID, prev.localID)
	return err
}

// settle sets what history says about a ticket once its messages are in: its status
// and when it was solved and closed, its satisfaction rating and its last change.
func (w *writer) settle(ctx context.Context, tx pgx.Tx, id uuid.UUID, t *source.Ticket) error {
	status := t.Status
	if w.x.Options.Statuses == "closed" || !slices.Contains(domain.Statuses, status) {
		status = domain.StatusClosed
	}
	updated := t.UpdatedAt
	if updated.IsZero() || updated.After(time.Now()) {
		updated = time.Now().UTC()
	}
	var csat *int
	if t.CSAT >= 1 && t.CSAT <= 5 {
		c := t.CSAT
		csat = &c
	}
	_, err := tx.Exec(ctx, `UPDATE tickets SET status = $3,
		solved_at = CASE WHEN $3 IN ('solved', 'closed') THEN COALESCE($4, solved_at, $5) END,
		closed_at = CASE WHEN $3 = 'closed' THEN COALESCE(closed_at, $5) END,
		waiting_since = CASE WHEN $3 IN ('solved', 'closed') THEN NULL ELSE waiting_since END,
		unanswered_count = CASE WHEN $3 IN ('solved', 'closed') THEN 0 ELSE unanswered_count END,
		snoozed_until = NULL, csat_score = COALESCE($6, csat_score),
		updated_at = GREATEST(updated_at, $5)
		WHERE workspace_id = $1 AND id = $2`, w.x.WorkspaceID, id, status, t.SolvedAt, updated, csat)
	return err
}

var tagUnsafe = regexp.MustCompile(`[^\p{L}\p{N} _\-./:&+#]+`)

// label adds the import's label and the ticket's own labels. A label the workspace cannot
// take (too many labels, one that does not apply to tickets) is left out.
func (w *writer) label(ctx context.Context, tx pgx.Tx, ticketID uuid.UUID, t *source.Ticket) error {
	names := []string{}
	if w.x.Options.Tag != "" {
		names = append(names, w.x.Options.Tag)
	}
	names = append(names, t.Tags...)
	for _, raw := range names {
		id, err := w.tag(ctx, tx, raw)
		if err != nil {
			return err
		}
		if id == nil {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO ticket_tags (ticket_id, tag_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, ticketID, *id); err != nil {
			return err
		}
	}
	return nil
}

func (w *writer) tag(ctx context.Context, tx pgx.Tx, raw string) (*uuid.UUID, error) {
	name := strings.TrimSpace(tagUnsafe.ReplaceAllString(raw, "-"))
	name = strings.Trim(hd.Truncate(name, 50), " -")
	clean, err := domain.CleanTagName(name)
	if err != nil {
		return nil, nil
	}
	key := strings.ToLower(clean)
	if id, ok := w.tags[key]; ok {
		return id, nil
	}
	var id uuid.UUID
	var applies bool
	err = tx.QueryRow(ctx, `SELECT id, cardinality(applies_to) = 0 OR 'tickets' = ANY(applies_to) FROM tags
		WHERE workspace_id = $1 AND lower(name) = lower($2) AND NOT archived ORDER BY name = $2 DESC LIMIT 1`, w.x.WorkspaceID, clean).Scan(&id, &applies)
	switch {
	case err == nil && applies:
		w.tags[key] = &id
		return &id, nil
	case err == nil:
		w.tags[key] = nil
		return nil, nil
	case !db.IsNotFound(err):
		return nil, err
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM tags WHERE workspace_id = $1`, w.x.WorkspaceID).Scan(&n); err != nil {
		return nil, err
	}
	if n >= domain.MaxLabels {
		w.tags[key] = nil
		w.problem("Label "+clean, fmt.Sprintf("The workspace has %d labels, the most it can have, so this one was left out.", domain.MaxLabels))
		return nil, nil
	}
	if id, err = domain.CreateLabel(ctx, tx, w.x.WorkspaceID, clean); err != nil {
		return nil, err
	}
	if err := w.remember(ctx, tx, "tag", key, id, true, nil); err != nil {
		return nil, err
	}
	w.tags[key] = &id
	return &id, nil
}

// contact finds or creates the contact for a source person: by what an earlier write
// recorded, then by email, else a new contact with their name only.
func (w *writer) contact(ctx context.Context, tx pgx.Tx, p *source.Person) (*uuid.UUID, error) {
	if p == nil {
		return nil, nil
	}
	email := strings.ToLower(strings.TrimSpace(p.Email))
	if !looksLikeEmail(email) {
		email = ""
	}
	key := strings.TrimSpace(p.ID)
	if key == "" && email != "" {
		key = "email:" + email
	}
	name := displayname.CleanOr(p.Name, displayname.Person, "")
	if key == "" {
		return nil, nil
	}
	if id, ok := w.contacts[key]; ok {
		return &id, nil
	}
	if r, err := w.lookup(ctx, tx, "contact", key); err != nil {
		return nil, err
	} else if r != nil {
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM contacts WHERE workspace_id = $1 AND id = $2)`, w.x.WorkspaceID, r.localID).Scan(&ok); err != nil {
			return nil, err
		}
		if ok {
			w.contacts[key] = r.localID
			return &r.localID, nil
		}
	}
	var id uuid.UUID
	var created bool
	var err error
	if email != "" {
		if id, created, err = domain.ImportContactByEmail(ctx, tx, w.x.WorkspaceID, email, name, p.Phone); err != nil {
			return nil, err
		}
	} else {
		if name == "" {
			name = "Customer"
		}
		id, created = ids.New(), true
		if _, err := tx.Exec(ctx, `INSERT INTO contacts (id, workspace_id, name, phone) VALUES ($1, $2, $3, $4)`,
			id, w.x.WorkspaceID, name, hd.Truncate(strings.TrimSpace(p.Phone), 40)); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(p.Company) != "" {
		cid, err := w.company(ctx, tx, p.Company, p.Domain)
		if err != nil {
			return nil, err
		}
		if cid != nil {
			if _, err := tx.Exec(ctx, `UPDATE contacts SET company_id = $3 WHERE workspace_id = $1 AND id = $2 AND company_id IS NULL`,
				w.x.WorkspaceID, id, *cid); err != nil {
				return nil, err
			}
		}
	}
	if err := w.remember(ctx, tx, "contact", key, id, created, nil); err != nil {
		return nil, err
	}
	if created {
		w.x.Counts.Contacts++
	}
	w.contacts[key] = id
	return &id, nil
}

var domainPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// company finds a company by domain or name, or creates it.
func (w *writer) company(ctx context.Context, tx pgx.Tx, rawName, rawDomain string) (*uuid.UUID, error) {
	name := displayname.CleanOr(rawName, displayname.Workspace, "")
	if name == "" {
		return nil, nil
	}
	dom := strings.ToLower(strings.TrimSpace(rawDomain))
	if !domainPattern.MatchString(dom) || len(dom) > 253 {
		dom = ""
	}
	key := "name:" + strings.ToLower(name)
	if r, err := w.lookup(ctx, tx, "company", key); err != nil {
		return nil, err
	} else if r != nil {
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM companies WHERE workspace_id = $1 AND id = $2)`, w.x.WorkspaceID, r.localID).Scan(&ok); err != nil {
			return nil, err
		}
		if ok {
			return &r.localID, nil
		}
	}
	var id uuid.UUID
	created := false
	err := tx.QueryRow(ctx, `SELECT id FROM companies WHERE workspace_id = $1 AND (($2 <> '' AND domain = $2) OR lower(name) = lower($3))
		ORDER BY ($2 <> '' AND domain = $2) DESC LIMIT 1`, w.x.WorkspaceID, dom, name).Scan(&id)
	if db.IsNotFound(err) {
		id, created = ids.New(), true
		_, err = tx.Exec(ctx, `INSERT INTO companies (id, workspace_id, name, domain) VALUES ($1, $2, $3, $4)`, id, w.x.WorkspaceID, name, dom)
	}
	if err != nil {
		return nil, err
	}
	if err := w.remember(ctx, tx, "company", key, id, created, nil); err != nil {
		return nil, err
	}
	if created {
		w.x.Counts.Companies++
	}
	return &id, nil
}

// writeArticle writes a help center article (and its collection) unless an earlier run
// did. Articles are not announced one by one: lists refresh from import.updated.
func (w *writer) writeArticle(ctx context.Context, a source.Article) error {
	scrubArticle(&a)
	counts := w.x.Counts
	err := w.s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		if err := w.begin(ctx, tx); err != nil {
			return err
		}
		if r, err := w.lookup(ctx, tx, "article", a.ID); err != nil || r != nil {
			return err
		}
		wid := w.x.WorkspaceID
		var collection *uuid.UUID
		if name := strings.TrimSpace(a.Collection); name != "" {
			id, err := w.collection(ctx, tx, name)
			if err != nil {
				return err
			}
			collection = id
		}
		title := hd.Truncate(strings.Join(strings.Fields(a.Title), " "), 200)
		if title == "" {
			title = "Untitled article"
		}
		body := hd.SanitizeRich(a.HTML)
		status := "draft"
		if a.Published && !w.x.Options.ArticlesAsDrafts {
			status = "published"
		}
		slug, err := hd.UniqueSlug(ctx, tx, "kb_articles", wid, hd.Slugify(title), nil)
		if err != nil {
			return err
		}
		created, updated := a.CreatedAt, a.UpdatedAt
		if created.IsZero() || created.After(time.Now()) {
			created = time.Now().UTC()
		}
		if updated.Before(created) {
			updated = created
		}
		id := ids.New()
		if _, err := tx.Exec(ctx, `INSERT INTO kb_articles (id, workspace_id, collection_id, title, slug, excerpt, body_html, status,
			published_at, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8, CASE WHEN $8 = 'published' THEN $10::timestamptz END, $9, $10)`,
			id, wid, collection, title, slug, hd.Excerpt(hd.HTMLToText(body), articleExcerptLen), body, status, created, updated); err != nil {
			return err
		}
		if status == "published" {
			if err := hd.EnqueueAIEmbed(ctx, tx, w.s.d.Jobs, w.s.d.Cfg, wid, "article", id); err != nil {
				return err
			}
		}
		if err := w.remember(ctx, tx, "article", a.ID, id, true, nil); err != nil {
			return err
		}
		w.x.Counts.Articles++
		return w.progress(ctx, tx, false)
	})
	if err != nil {
		w.x.Counts = counts
	}
	return err
}

func (w *writer) collection(ctx context.Context, tx pgx.Tx, rawName string) (*uuid.UUID, error) {
	name := hd.Truncate(strings.Join(strings.Fields(rawName), " "), 100)
	key := "name:" + strings.ToLower(name)
	if r, err := w.lookup(ctx, tx, "collection", key); err != nil {
		return nil, err
	} else if r != nil {
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM kb_collections WHERE workspace_id = $1 AND id = $2)`, w.x.WorkspaceID, r.localID).Scan(&ok); err != nil {
			return nil, err
		}
		if ok {
			return &r.localID, nil
		}
	}
	var id uuid.UUID
	created := false
	err := tx.QueryRow(ctx, `SELECT id FROM kb_collections WHERE workspace_id = $1 AND lower(name) = lower($2) LIMIT 1`, w.x.WorkspaceID, name).Scan(&id)
	if db.IsNotFound(err) {
		slug, err := hd.UniqueSlug(ctx, tx, "kb_collections", w.x.WorkspaceID, hd.Slugify(name), nil)
		if err != nil {
			return nil, err
		}
		id, created = ids.New(), true
		if _, err := tx.Exec(ctx, `INSERT INTO kb_collections (id, workspace_id, name, slug, position)
			VALUES ($1, $2, $3, $4, (SELECT COALESCE(max(position) + 1, 0) FROM kb_collections WHERE workspace_id = $2))`,
			id, w.x.WorkspaceID, name, slug); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if err := w.remember(ctx, tx, "collection", key, id, created, nil); err != nil {
		return nil, err
	}
	return &id, nil
}
