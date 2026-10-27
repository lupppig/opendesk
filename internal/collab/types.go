package collab

import (
	"time"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/domain"
)

// API shapes (contracts/collab.md "Shapes"). Credentials are never part of them.

// Providers.
const (
	ProviderGoogle    = "google"
	ProviderMicrosoft = "microsoft"
	ProviderIMAP      = "imap"
)

// Visibility of an account.
const (
	VisibilityPrivate = "private"
	VisibilityShared  = "shared"
)

// Folder roles.
const (
	RoleInbox     = "inbox"
	RoleSent      = "sent"
	RoleDrafts    = "drafts"
	RoleArchive   = "archive"
	RoleTrash     = "trash"
	RoleSpam      = "spam"
	RoleStarred   = "starred"
	RoleImportant = "important"
	RoleAll       = "all"
)

// Folder kinds.
const (
	KindFolder = "folder"
	KindLabel  = "label"
)

// Actions on conversations (contracts/collab.md "Actions").
const (
	ActionRead        = "read"
	ActionUnread      = "unread"
	ActionStar        = "star"
	ActionUnstar      = "unstar"
	ActionArchive     = "archive"
	ActionInbox       = "inbox"
	ActionTrash       = "trash"
	ActionSpam        = "spam"
	ActionNotSpam     = "not_spam"
	ActionMove        = "move"
	ActionLabel       = "label"
	ActionUnlabel     = "unlabel"
	ActionDone        = "done"
	ActionNeedsAction = "needs_action"
	// ActionCategorize moves conversations to one of their inbox's categories, in opendesk only.
	ActionCategorize = "categorize"
)

// Actions lists every action, for request validation.
var Actions = []string{ActionRead, ActionUnread, ActionStar, ActionUnstar, ActionArchive, ActionInbox, ActionTrash,
	ActionSpam, ActionNotSpam, ActionMove, ActionLabel, ActionUnlabel, ActionDone, ActionNeedsAction, ActionCategorize}

// Colors are the tone keys an account may use.
var Colors = []string{"gray", "red", "orange", "amber", "green", "teal", "blue", "indigo", "violet", "pink"}

// Audience is who an account or a conversation is shared with.
type Audience struct {
	Everyone bool        `json:"everyone"`
	TeamIDs  []uuid.UUID `json:"team_ids"`
	UserIDs  []uuid.UUID `json:"user_ids"`
}

// Empty reports an audience of nobody.
func (a Audience) Empty() bool { return !a.Everyone && len(a.TeamIDs) == 0 && len(a.UserIDs) == 0 }

// Address is a name and an email address.
type Address struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// IMAPSettings are an IMAP account's servers (never its passwords).
type IMAPSettings struct {
	IMAPHost     string `json:"imap_host"`
	IMAPPort     int    `json:"imap_port"`
	IMAPTLS      string `json:"imap_tls" enum:"tls,starttls"`
	IMAPUsername string `json:"imap_username"`
	SMTPHost     string `json:"smtp_host"`
	SMTPPort     int    `json:"smtp_port"`
	SMTPTLS      string `json:"smtp_tls" enum:"tls,starttls"`
	SMTPUsername string `json:"smtp_username"`
}

type MailAccount struct {
	ID              uuid.UUID       `json:"id"`
	Provider        string          `json:"provider" enum:"google,microsoft,imap"`
	Email           string          `json:"email"`
	Name            string          `json:"name"`
	DisplayName     string          `json:"display_name"`
	Color           string          `json:"color"`
	Owner           *domain.UserRef `json:"owner"`
	Visibility      string          `json:"visibility" enum:"private,shared"`
	SharedWith      Audience        `json:"shared_with"`
	SignatureHTML   string          `json:"signature_html"`
	Status          string          `json:"status" enum:"syncing,active,error,paused"`
	Reconnect       bool            `json:"reconnect"`
	LastError       string          `json:"last_error"`
	LastSyncedAt    *time.Time      `json:"last_synced_at"`
	CanManage       bool            `json:"can_manage"`
	Unread          int             `json:"unread"`
	HandlingEnabled bool            `json:"handling_enabled"`
	IMAP            *IMAPSettings   `json:"imap,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`

	// How its own settings sync with the provider (contracts/collab.md "Account settings").
	SignatureSync     string       `json:"signature_sync" enum:"two_way,read_only,none"`
	SignatureSyncedAt *time.Time   `json:"signature_synced_at"`
	DisplayNameSync   string       `json:"display_name_sync" enum:"two_way,read_only,none"`
	OutOfOffice       *OutOfOffice `json:"out_of_office"`
	OutOfOfficeSync   string       `json:"out_of_office_sync" enum:"two_way,read_only,none"`

	// Identities are the addresses it sends from, its own first (contracts/collab.md "Addresses").
	Identities []MailIdentity `json:"identities"`
	// DefaultFrom is the address new mail goes out from.
	DefaultFrom string `json:"default_from"`

	// History is its latest history import (contracts/collab.md "History"); nil when none was started.
	History *MailHistory `json:"history"`
	// OldestMessageAt is the oldest mail stored from it: its mail is here from then on.
	OldestMessageAt *time.Time `json:"oldest_message_at"`
	// FoldersLeftOut counts an IMAP account's folders past the ones the sync reads.
	FoldersLeftOut int `json:"folders_left_out"`
	// Categories is how its inbox is split into tabs (contracts/collab.md "Categories").
	Categories MailAccountCategories `json:"categories"`
}

// MailHistory is a history import: older mail brought in on request.
type MailHistory struct {
	Status           string          `json:"status" enum:"running,paused,limited,done,canceled,error"`
	Since            *time.Time      `json:"since" doc:"Mail received from then on; null = all mail"`
	IncludeSpamTrash bool            `json:"include_spam_trash"`
	RequestedBy      *domain.UserRef `json:"requested_by"`
	StartedAt        time.Time       `json:"started_at"`
	FinishedAt       *time.Time      `json:"finished_at"`
	Reached          *time.Time      `json:"reached" doc:"The oldest mail it has read"`
	Imported         int             `json:"imported"`
	Present          int             `json:"present" doc:"Already in opendesk"`
	TooLarge         int             `json:"too_large" doc:"Over 30 MB, left at the provider"`
	Unreadable       int             `json:"unreadable" doc:"Could not be parsed"`
	Total            *int            `json:"total" doc:"Messages in scope, when the provider says"`
	LastError        string          `json:"last_error"`
}

// MailIdentity is an address an account sends from: its own (primary, whose From name and
// signature are the account's) or another one (Gmail's send-as addresses, aliases added by
// hand for Microsoft and IMAP).
type MailIdentity struct {
	ID            *uuid.UUID `json:"id" doc:"null for the account's own address"`
	Email         string     `json:"email"`
	DisplayName   string     `json:"display_name" doc:"The From name; empty uses the account's"`
	SignatureHTML string     `json:"signature_html"`
	ReplyTo       string     `json:"reply_to" doc:"Replies go here when set"`
	Primary       bool       `json:"primary"`
	Default       bool       `json:"default" doc:"New mail goes out from it"`
	Source        string     `json:"source" enum:"account,provider,manual"`
	Verification  string     `json:"verification" enum:"accepted,pending"`
	CanSend       bool       `json:"can_send"`
	// How its From name and signature sync with the provider: the primary's as the
	// account's own; Gmail's other addresses pull (Gmail's changes come in, edits stay in
	// opendesk); hand-added ones none.
	SignatureSync     string     `json:"signature_sync" enum:"two_way,read_only,pull,none"`
	DisplayNameSync   string     `json:"display_name_sync" enum:"two_way,read_only,pull,none"`
	SignatureSyncedAt *time.Time `json:"signature_synced_at"`
	// A From name or signature with variables is kept in opendesk: never written to Gmail,
	// and Gmail's changes do not replace it ("Get from Gmail" does).
	DisplayNameLocal bool `json:"display_name_local" doc:"The From name has variables, so Gmail keeps its own"`
	SignatureLocal   bool `json:"signature_local" doc:"The signature has variables, so Gmail keeps its own"`
	// FromName and FilledSignatureHTML are the From name and the signature filled for the
	// caller, as the composer shows them (contracts/collab.md "Addresses").
	FromName            string  `json:"from_name" doc:"The From name (its own, else the account's) filled for the caller"`
	FilledSignatureHTML *string `json:"filled_signature_html" doc:"The signature filled for the caller; null when it has no variables"`
}

// OutOfOffice is an account's automatic reply as its provider keeps it (Gmail's vacation
// responder, Outlook's automatic replies).
type OutOfOffice struct {
	Enabled bool       `json:"enabled"`
	Start   *time.Time `json:"start" nullable:"true" doc:"Replies only to mail received from then on"`
	End     *time.Time `json:"end" nullable:"true" doc:"Replies only to mail received until then"`
	Subject string     `json:"subject" maxLength:"200"`
	// MessageHTML is sanitized with the signature allowlist.
	MessageHTML string `json:"message_html" maxLength:"65536"`
	// ContactsOnly replies only to people in the account's contacts.
	ContactsOnly bool `json:"contacts_only"`
	// InternalOnly replies only to people in the account's own organization (Google
	// Workspace domain, Microsoft 365 tenant).
	InternalOnly bool `json:"internal_only"`
}

type MailFolder struct {
	ID        uuid.UUID `json:"id"`
	AccountID uuid.UUID `json:"account_id"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	Role      *string   `json:"role" enum:"inbox,sent,drafts,archive,trash,spam,starred,important,all"`
	Kind      string    `json:"kind" enum:"folder,label"`
	Unread    int       `json:"unread"`
	Total     int       `json:"total"`
	Color     *string   `json:"color"`
	CanRename bool      `json:"can_rename"`
}

type MailThread struct {
	ID             uuid.UUID        `json:"id"`
	AccountID      uuid.UUID        `json:"account_id"`
	Subject        string           `json:"subject"`
	Snippet        string           `json:"snippet"`
	Participants   []Address        `json:"participants"`
	LastMessageAt  time.Time        `json:"last_message_at"`
	MessageCount   int              `json:"message_count"`
	Unread         bool             `json:"unread"`
	Starred        bool             `json:"starred"`
	HasAttachments bool             `json:"has_attachments"`
	NeedsAction    bool             `json:"needs_action"`
	FolderIDs      []uuid.UUID      `json:"folder_ids"`
	Shared         bool             `json:"shared"`
	Assignees      []domain.UserRef `json:"assignees"`
	CommentCount   int              `json:"comment_count"`
	// ToAddress is the account's address other than its own the conversation was written
	// to or sent from (an alias), newest first; null when none.
	ToAddress *string `json:"to_address"`
	// Category is its category key (contracts/collab.md "Categories"); null when the
	// account's inbox is not split.
	Category *string `json:"category"`
}

type MailMessage struct {
	ID          uuid.UUID        `json:"id"`
	ThreadID    uuid.UUID        `json:"thread_id"`
	AccountID   uuid.UUID        `json:"account_id"`
	MessageID   string           `json:"message_id"`
	From        Address          `json:"from"`
	To          []Address        `json:"to"`
	Cc          []Address        `json:"cc"`
	Bcc         []Address        `json:"bcc"`
	ReplyTo     []Address        `json:"reply_to"`
	Subject     string           `json:"subject"`
	Snippet     string           `json:"snippet"`
	BodyHTML    string           `json:"body_html"`
	BodyText    string           `json:"body_text"`
	SentAt      time.Time        `json:"sent_at"`
	Unread      bool             `json:"unread"`
	Starred     bool             `json:"starred"`
	Draft       bool             `json:"draft"`
	FolderIDs   []uuid.UUID      `json:"folder_ids"`
	Attachments []MailAttachment `json:"attachments"`
	SentBy      *domain.UserRef  `json:"sent_by"`
	// DraftID is set on drafts: the id the /v1/collab/drafts routes take.
	DraftID *uuid.UUID `json:"draft_id"`
	// DraftedByAI is, on a draft an AI client saved, the client's name ("Draft by <name>");
	// null on anything a person wrote or saved last.
	DraftedByAI *string `json:"drafted_by_ai"`
	// InReplyToID is the message of the conversation this one answers (its In-Reply-To), when stored.
	InReplyToID *uuid.UUID `json:"in_reply_to_id"`
	// OwnAddress is the account's address (its own or another) the message was sent from
	// or written to; null when none.
	OwnAddress *string `json:"own_address"`
	// Outgoing is mail the account wrote: sent from opendesk, a draft, or in its Sent or
	// Drafts at the provider. Mail from anyone else, whatever its From says, is not.
	Outgoing bool `json:"outgoing"`
}

type MailAttachment struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	Inline      bool   `json:"inline"`
	ContentID   string `json:"content_id"`
	URL         string `json:"url"`
}

type MailComment struct {
	ID        uuid.UUID       `json:"id"`
	ThreadID  uuid.UUID       `json:"thread_id"`
	Author    *domain.UserRef `json:"author"`
	BodyHTML  string          `json:"body_html"`
	Mentions  []uuid.UUID     `json:"mentions"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// ThreadAccount is the account a conversation belongs to, as its detail shows it.
type ThreadAccount struct {
	ID            uuid.UUID `json:"id"`
	Email         string    `json:"email"`
	Name          string    `json:"name"`
	DisplayName   string    `json:"display_name"`
	Color         string    `json:"color"`
	Provider      string    `json:"provider"`
	SignatureHTML string    `json:"signature_html"`
	// Identities are the addresses it sends from, its own first; DefaultFrom new mail's.
	Identities  []MailIdentity `json:"identities"`
	DefaultFrom string         `json:"default_from"`
}

type MailThreadDetail struct {
	Thread   MailThread    `json:"thread"`
	Account  ThreadAccount `json:"account"`
	Messages []MailMessage `json:"messages"`
	Comments []MailComment `json:"comments"`
	Shares   Audience      `json:"shares"`
	CanAct   bool          `json:"can_act"`
	// Ticket is the ticket the conversation answered or was answered from, when the caller
	// sees it ("Answering tickets").
	Ticket *ThreadTicket `json:"ticket"`
}

// ThreadTicket names a conversation's ticket.
type ThreadTicket struct {
	ID      uuid.UUID `json:"id"`
	Number  int64     `json:"number"`
	Subject string    `json:"subject"`
}

type CollabProviders struct {
	Google    bool `json:"google"`
	Microsoft bool `json:"microsoft"`
	IMAP      bool `json:"imap"`
}

type CollabStatus struct {
	Enabled   bool            `json:"enabled"`
	Reason    *string         `json:"reason" enum:"plan,disabled"`
	Providers CollabProviders `json:"providers"`
	Accounts  int             `json:"accounts"`
	Storage   CollabStorage   `json:"storage"`
}

// CollabStorage is the workspace's Collab storage (contracts/collab.md "Storage").
type CollabStorage struct {
	UsedBytes      int64 `json:"used_bytes"`
	LimitBytes     int64 `json:"limit_bytes" doc:"0 = unlimited"`
	HistoryLimited bool  `json:"history_limited" doc:"At the allowance: history imports wait"`
	Full           bool  `json:"full" doc:"Past twice the allowance: new mail waits at the provider"`
}

// SidebarPin is one favorite in the sidebar.
type SidebarPin struct {
	Kind string `json:"kind" enum:"box,folder,account,view,team,ticket_folder"`
	ID   string `json:"id" maxLength:"100"`
}

type SidebarPrefs struct {
	Order            []string     `json:"order" maxItems:"20"`
	Hidden           []string     `json:"hidden" maxItems:"500"`
	Pinned           []SidebarPin `json:"pinned" maxItems:"30"`
	ExpandedAccounts []string     `json:"expanded_accounts" maxItems:"200"`
	MarkReadOnOpen   bool         `json:"mark_read_on_open"`
	UnifiedInbox     bool         `json:"unified_inbox"`
	// Badges chooses the number beside a row ("box:inbox", "account:<id>", "folder:<id>",
	// "ticket_folder:<id>", "view:<id>", "team:<id>") or a section's default ("section:mail"):
	// unread, total or none for mail rows, count or none for ticket rows.
	Badges map[string]string `json:"badges" required:"false" doc:"Left out: the saved value stays"`
	// MailTabs is the mail page's folder bar, in order: box names and "folder:<id>".
	MailTabs []string `json:"mail_tabs" required:"false" maxItems:"30" doc:"Left out: the saved value stays"`
	// AutoPins are the pins the sidebar added once by itself ("box:inbox": All inboxes, when
	// the member first sees two accounts), so one unpinned is not added again.
	AutoPins []string `json:"auto_pins" required:"false" maxItems:"20" doc:"Left out: the saved value stays"`
}
