package domain

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/db"
)

var (
	apiURL        string
	attachmentKey []byte
)

// Configure sets the public API base and the key that signs attachment links. Call once at boot.
func Configure(publicAPIURL string, secret []byte) {
	apiURL = strings.TrimRight(publicAPIURL, "/")
	attachmentKey = append([]byte("attachment-url:"), secret...)
}

// systemSending: mailboxes with outbound_kind "system" send through the platform mailer
// (MAILBOX_SYSTEM_SEND).
var systemSending = true

// ConfigureSending sets whether mailboxes may send through the platform mailer. Call once at boot.
func ConfigureSending(system bool) { systemSending = system }

// AttachmentURL is a link valid for 24h so <img> tags and downloads work without a bearer token.
func AttachmentURL(id uuid.UUID) string {
	exp := strconv.FormatInt(time.Now().Add(24*time.Hour).Truncate(time.Hour).Unix(), 10)
	return apiURL + "/v1/attachments/" + id.String() + "?exp=" + exp + "&sig=" + crypto.HMACHex(attachmentKey, id.String()+":"+exp)
}

// VerifyAttachmentSig checks a link made by AttachmentURL.
func VerifyAttachmentSig(id uuid.UUID, exp, sig string) bool {
	n, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || time.Now().Unix() > n {
		return false
	}
	if _, err := hex.DecodeString(sig); err != nil {
		return false
	}
	return crypto.EqualHex(crypto.HMACHex(attachmentKey, id.String()+":"+exp), sig)
}

// TicketSelect is the column list ScanTicket reads. Append WHERE/ORDER clauses using alias t.
var TicketSelect = `SELECT t.id, t.number, t.subject, t.status, t.priority, t.severity, t.channel, t.mailbox_id, t.team_id,
	t.sla_policy_id, t.first_response_due_at, t.resolution_due_at, t.first_responded_at, t.solved_at, t.snoozed_until,
	t.last_message_at, t.last_customer_message_at, t.waiting_since, t.unanswered_count, t.last_message_preview, t.message_count, t.spam,
	t.csat_score, t.csat_comment, t.custom, t.fields, t.created_at, t.updated_at,
	t.brand_id, t.widget_id, t.origin_mailbox_id, t.received_at, t.page_url, t.routed,
	c.id, c.email, c.name, c.avatar_url,
	u.id, u.name, u.email, u.avatar_url,
	` + TagRefsSQL("ticket_tags", "lt.ticket_id = t.id") + `
FROM tickets t
LEFT JOIN contacts c ON c.id = t.contact_id
LEFT JOIN users u ON u.id = t.assignee_id`

// ScanTicket scans one row of TicketSelect.
func ScanTicket(row pgx.Row) (Ticket, error) {
	var t Ticket
	var slaID *uuid.UUID
	var frDue, resDue *time.Time
	var csatScore *int16
	var csatComment string
	var cID *uuid.UUID
	var cEmail, cName, cAvatar *string
	var uID *uuid.UUID
	var uName, uEmail, uAvatar *string
	var tags []byte
	err := row.Scan(&t.ID, &t.Number, &t.Subject, &t.Status, &t.Priority, &t.Severity, &t.Channel, &t.MailboxID, &t.TeamID,
		&slaID, &frDue, &resDue, &t.FirstRespondedAt, &t.SolvedAt, &t.SnoozedUntil,
		&t.LastMessageAt, &t.LastCustomerMessageAt, &t.WaitingSince, &t.UnansweredCount, &t.LastMessagePreview, &t.MessageCount, &t.Spam,
		&csatScore, &csatComment, &t.Custom, &t.Fields, &t.CreatedAt, &t.UpdatedAt,
		&t.BrandID, &t.WidgetID, &t.OriginMailboxID, &t.ReceivedAt, &t.PageURL, &t.Routed,
		&cID, &cEmail, &cName, &cAvatar, &uID, &uName, &uEmail, &uAvatar, &tags)
	if err != nil {
		return t, err
	}
	if cID != nil {
		t.Contact = &ContactRef{ID: *cID, Email: cEmail, Name: deref(cName), AvatarURL: Avatar(deref(cAvatar))}
	}
	if uID != nil {
		t.Assignee = &UserRef{ID: *uID, Name: deref(uName), Email: deref(uEmail), AvatarURL: Avatar(deref(uAvatar))}
	}
	if slaID != nil {
		now := time.Now()
		open := t.Status != StatusSolved && t.Status != StatusClosed
		breached := open && ((resDue != nil && now.After(*resDue)) ||
			(t.FirstRespondedAt == nil && frDue != nil && now.After(*frDue)))
		t.SLA = &TicketSLA{PolicyID: *slaID, FirstResponseDueAt: frDue, ResolutionDueAt: resDue, Breached: breached}
	}
	if csatScore != nil {
		t.CSAT = &CSAT{Score: int(*csatScore), Comment: csatComment}
	}
	if t.Custom == nil {
		t.Custom = map[string]any{}
	}
	if t.Fields == nil {
		t.Fields = map[string]any{}
	}
	t.Tags = []TagRef{}
	if err := json.Unmarshal(tags, &t.Tags); err != nil {
		return t, err
	}
	return t, nil
}

// LoadTicket loads one ticket, scoped to the workspace. Deleted tickets are not found.
func LoadTicket(ctx context.Context, q db.Querier, wid, id uuid.UUID) (Ticket, error) {
	return ScanTicket(q.QueryRow(ctx, TicketSelect+` WHERE t.workspace_id = $1 AND t.id = $2 AND t.deleted_at IS NULL`, wid, id))
}

// MessageSelect is the column list scanMessage reads.
const MessageSelect = `SELECT m.id, m.ticket_id, m.kind, m.direction, m.author_type, m.body_html, m.body_text,
	m.full_html <> '', m.subject, m.from_email, m.from_name, m.to_emails, m.cc_emails, m.bcc_emails,
	m.delivery_status, m.delivery_error, m.ai, m.ai_action, m.ai_rating, m.mailbox_id, m.reply_to, m.auth_failed,
	m.form_sender, m.received_at, m.thread_id, m.created_at,
	u.id, u.name, u.email, u.avatar_url,
	c.id, c.email, c.name, c.avatar_url,
	bt.id, bt.name, bt.avatar, bt.look,
	COALESCE((SELECT json_agg(json_build_object('id', a.id, 'filename', a.filename, 'content_type', a.content_type,
		'size_bytes', a.size_bytes, 'inline', a.inline) ORDER BY a.created_at)
		FROM attachments a WHERE a.message_id = m.id), '[]')
FROM messages m
LEFT JOIN users u ON u.id = m.author_user_id
LEFT JOIN contacts c ON c.id = m.author_contact_id
LEFT JOIN bots bt ON bt.id = m.bot_id`

func ScanMessage(row pgx.Row) (Message, error) {
	var m Message
	var uID, cID *uuid.UUID
	var uName, uEmail, uAvatar, cEmail, cName, cAvatar *string
	var atts []byte
	var bID *uuid.UUID
	var bName, bAvatar *string
	var bLook []byte
	err := row.Scan(&m.ID, &m.TicketID, &m.Kind, &m.Direction, &m.Author.Type, &m.BodyHTML, &m.BodyText,
		&m.HasQuoted, &m.Subject, &m.FromEmail, &m.FromName, &m.To, &m.Cc, &m.Bcc,
		&m.DeliveryStatus, &m.DeliveryError, &m.AI, &m.AIAction, &m.AIRating, &m.MailboxID, &m.ReplyTo, &m.AuthFailed,
		&m.FormSender, &m.ReceivedAt, &m.ThreadID, &m.CreatedAt,
		&uID, &uName, &uEmail, &uAvatar, &cID, &cEmail, &cName, &cAvatar, &bID, &bName, &bAvatar, &bLook, &atts)
	if err != nil {
		return m, err
	}
	if uID != nil {
		m.Author.User = &UserRef{ID: *uID, Name: deref(uName), Email: deref(uEmail), AvatarURL: Avatar(deref(uAvatar))}
	}
	if cID != nil {
		m.Author.Contact = &ContactRef{ID: *cID, Email: cEmail, Name: deref(cName), AvatarURL: Avatar(deref(cAvatar))}
	}
	if bID != nil {
		m.Author.Bot = &BotRef{ID: *bID, Name: deref(bName), AvatarURL: BotAvatarURL(*bID, Avatar(deref(bAvatar)), bLook), Look: bLook}
	}
	m.Attachments = []Attachment{}
	if err := json.Unmarshal(atts, &m.Attachments); err != nil {
		return m, err
	}
	for i := range m.Attachments {
		m.Attachments[i].URL = AttachmentURL(m.Attachments[i].ID)
	}
	m.BodyHTML = ResignAttachmentLinks(m.BodyHTML)
	for _, s := range []*[]string{&m.To, &m.Cc, &m.Bcc} {
		if *s == nil {
			*s = []string{}
		}
	}
	return m, nil
}

func LoadMessage(ctx context.Context, q db.Querier, wid, id uuid.UUID) (Message, error) {
	return ScanMessage(q.QueryRow(ctx, MessageSelect+` WHERE m.workspace_id = $1 AND m.id = $2`, wid, id))
}

// ListMessages returns a ticket's messages oldest first. publicOnly drops internal notes.
func ListMessages(ctx context.Context, q db.Querier, wid, ticketID uuid.UUID, publicOnly bool) ([]Message, error) {
	rows, err := q.Query(ctx, MessageSelect+` WHERE m.workspace_id = $1 AND m.ticket_id = $2 AND (NOT $3 OR m.kind = 'reply')
		ORDER BY m.created_at, m.id`, wid, ticketID, publicOnly)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Message, error) { return ScanMessage(r) })
	if out == nil {
		out = []Message{}
	}
	return out, err
}

var ContactSelect = `SELECT ct.id, ct.email, ct.name, ct.phone, ct.avatar_url, ct.avatar_source, ct.external_id, ct.notes, ct.custom,
	ct.blocked, ct.created_at, ct.updated_at, co.id, co.name, co.domain, co.avatar_url,
	(SELECT count(*) FROM tickets tk WHERE tk.contact_id = ct.id AND tk.deleted_at IS NULL),
	ct.fields, ` + TagRefsSQL("contact_tags", "lt.contact_id = ct.id") + `
FROM contacts ct LEFT JOIN companies co ON co.id = ct.company_id`

func ScanContact(row pgx.Row) (Contact, error) {
	var c Contact
	var coID *uuid.UUID
	var coName, coDomain, coAvatar *string
	var tags []byte
	err := row.Scan(&c.ID, &c.Email, &c.Name, &c.Phone, &c.AvatarURL, &c.AvatarSource, &c.ExternalID, &c.Notes, &c.Custom,
		&c.Blocked, &c.CreatedAt, &c.UpdatedAt, &coID, &coName, &coDomain, &coAvatar, &c.TicketCount, &c.Fields, &tags)
	if err != nil {
		return c, err
	}
	if c.Fields == nil {
		c.Fields = map[string]any{}
	}
	c.Tags = []TagRef{}
	if err := json.Unmarshal(tags, &c.Tags); err != nil {
		return c, err
	}
	if coID != nil {
		c.Company = &CompanyRef{ID: *coID, Name: deref(coName), Domain: deref(coDomain), AvatarURL: Avatar(deref(coAvatar))}
	}
	if c.Custom == nil {
		c.Custom = map[string]any{}
	}
	return c, nil
}

func LoadContact(ctx context.Context, q db.Querier, wid, id uuid.UUID) (Contact, error) {
	return ScanContact(q.QueryRow(ctx, ContactSelect+` WHERE ct.workspace_id = $1 AND ct.id = $2`, wid, id))
}

// LoadUserRef loads a user's public fields.
func LoadUserRef(ctx context.Context, q db.Querier, id uuid.UUID) (UserRef, error) {
	u := UserRef{ID: id}
	err := q.QueryRow(ctx, `SELECT name, email, avatar_url FROM users WHERE id = $1`, id).Scan(&u.Name, &u.Email, &u.AvatarURL)
	return u, err
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// PublicMessage is the view of a message a customer may see: no agent emails,
// recipients, delivery internals or sender checks.
func PublicMessage(m Message) Message {
	if m.Author.User != nil {
		u := *m.Author.User
		u.Email = ""
		m.Author.User = &u
	}
	m.To, m.Cc, m.Bcc = []string{}, []string{}, []string{}
	m.DeliveryError = ""
	m.MailboxID = nil
	m.ReplyTo, m.AuthFailed, m.FormSender, m.ReceivedAt = "", false, "", ""
	m.ThreadID = nil
	if m.Direction == "outbound" {
		m.FromEmail = ""
	}
	return m
}

var attachmentLink = regexp.MustCompile(`/v1/attachments/([0-9a-f-]{36})\?exp=\d+(&amp;|&)sig=[0-9a-f]+`)

// ResignAttachmentLinks refreshes the signed attachment links inside stored HTML
// (inline images), so bodies keep rendering after the original links expire.
func ResignAttachmentLinks(html string) string {
	if !strings.Contains(html, "/v1/attachments/") {
		return html
	}
	return attachmentLink.ReplaceAllStringFunc(html, func(m string) string {
		sub := attachmentLink.FindStringSubmatch(m)
		id, err := uuid.Parse(sub[1])
		if err != nil {
			return m
		}
		fresh := strings.TrimPrefix(AttachmentURL(id), apiURL)
		if sub[2] == "&amp;" {
			fresh = strings.Replace(fresh, "&", "&amp;", 1)
		}
		return fresh
	})
}

// WorkspaceSuspended reports whether an instance admin suspended the workspace
// (contracts/admin.md). Background work for it stops while it is.
func WorkspaceSuspended(ctx context.Context, q db.Querier, wid uuid.UUID) (bool, error) {
	var suspended bool
	err := q.QueryRow(ctx, `SELECT suspended_at IS NOT NULL FROM workspaces WHERE id = $1`, wid).Scan(&suspended)
	if db.IsNotFound(err) {
		return false, nil
	}
	return suspended, err
}
