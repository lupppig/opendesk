// Package domain holds the resource shapes from contracts/api.md and the ticket writes
// shared by the email pipeline and the helpdesk API. Every query is workspace-scoped.
package domain

import (
	"time"

	"github.com/google/uuid"
)

type UserRef struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	AvatarURL Avatar    `json:"avatar_url"`
}

type ContactRef struct {
	ID        uuid.UUID `json:"id"`
	Email     *string   `json:"email"`
	Name      string    `json:"name"`
	AvatarURL Avatar    `json:"avatar_url"`
}

type CompanyRef struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Domain    string    `json:"domain"`
	AvatarURL Avatar    `json:"avatar_url"`
}

type Contact struct {
	ID           uuid.UUID      `json:"id"`
	Email        *string        `json:"email"`
	Name         string         `json:"name"`
	Phone        string         `json:"phone"`
	AvatarURL    Avatar         `json:"avatar_url"`
	AvatarSource *string        `json:"avatar_source" enum:"upload,gravatar,widget"`
	Company      *CompanyRef    `json:"company"`
	ExternalID   *string        `json:"external_id"`
	Notes        string         `json:"notes"`
	Custom       map[string]any `json:"custom"`
	Tags         []TagRef       `json:"tags"`
	Fields       map[string]any `json:"fields"`
	Blocked      bool           `json:"blocked"`
	TicketCount  int            `json:"ticket_count"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// Tag is a label definition (contracts/api.md Tag).
type Tag struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`
	Color       string     `json:"color" enum:"gray,red,orange,amber,yellow,lime,green,teal,cyan,blue,indigo,violet,purple,pink,rose"`
	Description string     `json:"description"`
	GroupID     *uuid.UUID `json:"group_id"`
	Position    int        `json:"position"`
	Archived    bool       `json:"archived"`
	AppliesTo   []string   `json:"applies_to"`
	Usage       TagUsage   `json:"usage"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type TagUsage struct {
	Tickets   int `json:"tickets"`
	Contacts  int `json:"contacts"`
	Companies int `json:"companies"`
}

// TagGroup is a label group; exclusive groups allow one label per record.
type TagGroup struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Color     *string   `json:"color"`
	Exclusive bool      `json:"exclusive"`
	Position  int       `json:"position"`
	TagCount  int       `json:"tag_count"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type TicketSLA struct {
	PolicyID           uuid.UUID  `json:"policy_id"`
	FirstResponseDueAt *time.Time `json:"first_response_due_at"`
	ResolutionDueAt    *time.Time `json:"resolution_due_at"`
	Breached           bool       `json:"breached"`
}

type CSAT struct {
	Score   int    `json:"score"`
	Comment string `json:"comment"`
}

// Ticket statuses, priorities and channels.
const (
	StatusOpen    = "open"
	StatusPending = "pending"
	StatusOnHold  = "on_hold"
	StatusSolved  = "solved"
	StatusClosed  = "closed"

	ChannelEmail  = "email"
	ChannelChat   = "chat"
	ChannelAPI    = "api"
	ChannelPortal = "portal"
)

var (
	Statuses   = []string{StatusOpen, StatusPending, StatusOnHold, StatusSolved, StatusClosed}
	Priorities = []string{"low", "normal", "high", "urgent"}
	// Severities are ordered most severe first; a ticket may have none (nil).
	Severities = []string{"sev1", "sev2", "sev3", "sev4"}
	Channels   = []string{ChannelEmail, ChannelChat, ChannelAPI, ChannelPortal}
)

type Ticket struct {
	ID        uuid.UUID   `json:"id"`
	Number    int64       `json:"number"`
	Subject   string      `json:"subject"`
	Status    string      `json:"status" enum:"open,pending,on_hold,solved,closed"`
	Priority  string      `json:"priority" enum:"low,normal,high,urgent"`
	Severity  *string     `json:"severity" enum:"sev1,sev2,sev3,sev4" nullable:"true"`
	Channel   string      `json:"channel" enum:"email,chat,api,portal"`
	MailboxID *uuid.UUID  `json:"mailbox_id"`
	Contact   *ContactRef `json:"contact"`
	Assignee  *UserRef    `json:"assignee"`
	TeamID    *uuid.UUID  `json:"team_id"`
	// BrandID is the brand the customer reached; WidgetID the chat widget a chat started in.
	BrandID  uuid.UUID  `json:"brand_id"`
	WidgetID *uuid.UUID `json:"widget_id"`
	// OriginMailboxID and ReceivedAt are where the first message arrived (ReceivedAt is the
	// exact address, which may be an alias). MailboxID is the address replies go out from.
	OriginMailboxID       *uuid.UUID     `json:"origin_mailbox_id"`
	ReceivedAt            string         `json:"received_at"`
	PageURL               string         `json:"page_url"`
	Routed                *TicketRouted  `json:"routed"`
	Tags                  []TagRef       `json:"tags"`
	Fields                map[string]any `json:"fields"`
	SLA                   *TicketSLA     `json:"sla"`
	FirstRespondedAt      *time.Time     `json:"first_responded_at"`
	SolvedAt              *time.Time     `json:"solved_at"`
	SnoozedUntil          *time.Time     `json:"snoozed_until"`
	LastMessageAt         time.Time      `json:"last_message_at"`
	LastCustomerMessageAt *time.Time     `json:"last_customer_message_at"`
	// WaitingSince is when the customer's unanswered messages began; nil once replied to.
	WaitingSince *time.Time `json:"waiting_since"`
	// UnansweredCount is how many public messages the customer sent since the last reply.
	UnansweredCount    int            `json:"unanswered_count"`
	LastMessagePreview string         `json:"last_message_preview"`
	MessageCount       int            `json:"message_count"`
	Spam               bool           `json:"spam"`
	CSAT               *CSAT          `json:"csat"`
	Custom             map[string]any `json:"custom"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
}

// TicketRouted records why a ticket is in its team (contracts/teams.md "Routing").
type TicketRouted struct {
	Via      string     `json:"via" enum:"address,widget,topic,rule,triage,automation,agent,api"`
	RuleID   *uuid.UUID `json:"rule_id,omitempty"`
	RuleName string     `json:"rule_name,omitempty"`
	By       *UserRef   `json:"by,omitempty"`
	// TopicID and TopicLabel are the widget topic the visitor picked, when it decided the team.
	TopicID    string `json:"topic_id,omitempty"`
	TopicLabel string `json:"topic_label,omitempty"`
}

type Attachment struct {
	ID          uuid.UUID `json:"id"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	Inline      bool      `json:"inline"`
	URL         string    `json:"url"`
}

type MessageAuthor struct {
	Type    string      `json:"type" enum:"contact,user,system,api"`
	User    *UserRef    `json:"user"`
	Contact *ContactRef `json:"contact"`
	// Bot is the bot an AI reply was sent as (contracts/bots.md); null otherwise.
	Bot *BotRef `json:"bot"`
}

type Message struct {
	ID             uuid.UUID     `json:"id"`
	TicketID       uuid.UUID     `json:"ticket_id"`
	Kind           string        `json:"kind" enum:"reply,note"`
	Direction      string        `json:"direction" enum:"inbound,outbound"`
	Author         MessageAuthor `json:"author"`
	BodyHTML       string        `json:"body_html"`
	BodyText       string        `json:"body_text"`
	HasQuoted      bool          `json:"has_quoted"`
	Subject        string        `json:"subject"`
	FromEmail      string        `json:"from_email"`
	FromName       string        `json:"from_name"`
	To             []string      `json:"to"`
	Cc             []string      `json:"cc"`
	Bcc            []string      `json:"bcc"`
	Attachments    []Attachment  `json:"attachments"`
	DeliveryStatus string        `json:"delivery_status" enum:"none,pending,sent,failed,bounced"`
	DeliveryError  string        `json:"delivery_error"`
	// AI marks a reply written by the widget AI agent.
	AI bool `json:"ai"`
	// AIAction is an action the agent asks the visitor to confirm (contracts/agent.md).
	AIAction *AIAction `json:"ai_action"`
	// AIRating is the visitor's rating of an agent reply.
	AIRating *string `json:"ai_rating" enum:"up,down"`
	// MailboxID is the address the message was sent from (outbound) or received at (inbound).
	MailboxID *uuid.UUID `json:"mailbox_id"`
	// ReplyTo is the Reply-To an inbound email named when it differs from FromEmail, else "".
	ReplyTo string `json:"reply_to"`
	// AuthFailed marks an inbound email whose From failed DMARC at a trusted receiving server.
	AuthFailed bool `json:"auth_failed"`
	// FormSender is the trusted contact form address an inbound email came through; FromEmail
	// is then the visitor its Reply-To named. "" otherwise.
	FormSender string `json:"form_sender"`
	// ReceivedAt is the address of ours an inbound email was written to: its mailbox's
	// address or one of its aliases. "" otherwise.
	ReceivedAt string `json:"received_at"`
	// ThreadID is the third-party thread a note belongs to (contracts/api.md "Threads"), else null.
	ThreadID  *uuid.UUID `json:"thread_id"`
	CreatedAt time.Time  `json:"created_at"`
	// EditedAt is set when a plain internal note was edited.
	EditedAt *time.Time `json:"edited_at"`
}

// AIAction is the card on an agent message that asks the visitor to confirm a tool call.
type AIAction struct {
	ID      uuid.UUID `json:"id"`
	Title   string    `json:"title"`
	Summary string    `json:"summary"`
	Status  string    `json:"status" enum:"pending,confirmed,declined,expired,failed"`
	Tool    string    `json:"tool"`
}

type ActivityActor struct {
	Type string   `json:"type"`
	User *UserRef `json:"user"`
}

type Activity struct {
	ID        uuid.UUID      `json:"id"`
	TicketID  uuid.UUID      `json:"ticket_id"`
	Actor     ActivityActor  `json:"actor"`
	Type      string         `json:"type"`
	Data      map[string]any `json:"data"`
	CreatedAt time.Time      `json:"created_at"`
}

// SeverityRank orders severities for comparison: sev1 is 4, sev4 is 1, none is 0.
func SeverityRank(s *string) int {
	if s == nil {
		return 0
	}
	switch *s {
	case "sev1":
		return 4
	case "sev2":
		return 3
	case "sev3":
		return 2
	case "sev4":
		return 1
	}
	return 0
}

// Keys of Ticket.Custom written by AI decisions (contracts/ai.md "Decisions").
const (
	CustomAIDecisions   = "ai_decisions"
	CustomAISuggestions = "ai_suggestions"
)

// EventTicketID scopes events carrying a ticket to its private team (events.TicketRef).
func (t Ticket) EventTicketID() *uuid.UUID { id := t.ID; return &id }

// EventTicketID scopes events carrying a message to its ticket's private team.
func (m Message) EventTicketID() *uuid.UUID { id := m.TicketID; return &id }

// EventTicketID scopes events carrying an activity to its ticket's private team.
func (a Activity) EventTicketID() *uuid.UUID { id := a.TicketID; return &id }
