package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/email"
)

// Collab tools (contracts/collab.md "AI drafts"): an API key with collab:draft reads the
// conversations of the member who created it and saves reply drafts for a
// person to review and send. Nothing here sends, marks read or unread, moves, assigns
// or comments; the API refuses those routes to a key whatever a tool asks.

const (
	maxListConversations = 50
	collabBoxes          = "inbox, unread, assigned (to the member), shared (with the member), starred or all"
)

func collabTools() []*tool {
	scopes := []string{auth.ScopeCollabDraft}
	return []*tool{
		{
			Name: "list_collab_conversations", Title: "List mail conversations", Scopes: scopes, ReadOnly: true, Idempotent: true,
			Description: "List the Collab email conversations the member who created your API key sees, newest activity first: " + collabBoxes + ". " +
				"Reading changes nothing: a conversation stays unread for the team. Returns up to 50 (default 20) and next_cursor when there are more.",
			Schema: obj(map[string]any{
				"box":        enum("Which conversations (default inbox).", "inbox", "unread", "assigned", "shared", "starred", "all"),
				"account_id": str("Only this mail account's conversations."),
				"query":      strN("Text to match in subjects, senders and messages.", 1, 200),
				"limit":      integer("How many conversations to return.", 1, maxListConversations, 20),
				"cursor":     str("next_cursor from a previous call with the same filters."),
			}),
			Run: listCollabConversations,
		},
		{
			Name: "get_collab_conversation", Title: "Read a mail conversation", Scopes: scopes, ReadOnly: true, Idempotent: true,
			Description: "Read one Collab conversation: its account and send-from addresses, its messages as plain text in order (drafts marked, " +
				"with drafted_by_ai on drafts an AI key saved) and the team's internal comments. Reading does not mark it read. " +
				"Long conversations keep the latest 50 messages.",
			Schema: obj(map[string]any{"conversation": str("The conversation id.")}, "conversation"),
			Run:    getCollabConversation,
		},
		{
			Name: "draft_collab_reply", Title: "Draft a mail reply for review", Scopes: scopes, Idempotent: true,
			Description: "Save a reply to a Collab conversation as a draft in the mail account. Nothing is sent: the draft waits, labelled as yours, " +
				"for a person to review, edit and send. Replies to the latest message unless message_id names another; recipients follow the " +
				"message unless to or cc are given. Pass draft_id to revise a draft you saved before (a draft a person has saved since is theirs: " +
				"save a new one). Write the body in markdown, without a signature or the quoted original.",
			Schema: obj(map[string]any{
				"conversation": str("The conversation id."),
				"body":         strN("The reply, in markdown.", 1, maxBodyChars),
				"reply_all":    map[string]any{"type": "boolean", "description": "Reply to everyone on the message (default only its sender)."},
				"message_id":   str("The message to answer (an id from get_collab_conversation); default the latest one."),
				"draft_id":     str("A draft_id you saved before, to revise it."),
				"to":           arr("Recipients instead of the message's.", strN("", 3, 254), 20),
				"cc":           arr("Copied addresses instead of the message's.", strN("", 3, 254), 20),
			}, "conversation", "body"),
			Run: draftCollabReply,
		},
	}
}

// --- API shapes ----------------------------------------------------------------------

type mailAddress struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type apiMailThread struct {
	ID            string        `json:"id"`
	AccountID     string        `json:"account_id"`
	Subject       string        `json:"subject"`
	Snippet       string        `json:"snippet"`
	Participants  []mailAddress `json:"participants"`
	LastMessageAt time.Time     `json:"last_message_at"`
	MessageCount  int           `json:"message_count"`
	Unread        bool          `json:"unread"`
	Assignees     []userRef     `json:"assignees"`
	CommentCount  int           `json:"comment_count"`
}

type apiMailMessage struct {
	ID          string        `json:"id"`
	From        mailAddress   `json:"from"`
	To          []mailAddress `json:"to"`
	Cc          []mailAddress `json:"cc"`
	ReplyTo     []mailAddress `json:"reply_to"`
	Subject     string        `json:"subject"`
	BodyText    string        `json:"body_text"`
	SentAt      time.Time     `json:"sent_at"`
	Draft       bool          `json:"draft"`
	DraftID     *string       `json:"draft_id"`
	DraftedByAI *string       `json:"drafted_by_ai"`
	Outgoing    bool          `json:"outgoing"`
	Attachments []struct {
		Filename string `json:"filename"`
		Inline   bool   `json:"inline"`
	} `json:"attachments"`
}

type apiMailDetail struct {
	Thread  apiMailThread `json:"thread"`
	Account struct {
		ID         string `json:"id"`
		Email      string `json:"email"`
		Name       string `json:"name"`
		Identities []struct {
			Email   string `json:"email"`
			CanSend bool   `json:"can_send"`
		} `json:"identities"`
	} `json:"account"`
	Messages []apiMailMessage `json:"messages"`
	Comments []struct {
		Author    *userRef  `json:"author"`
		BodyHTML  string    `json:"body_html"`
		CreatedAt time.Time `json:"created_at"`
	} `json:"comments"`
}

// --- outputs -------------------------------------------------------------------------

type outConversation struct {
	ID            string    `json:"id"`
	AccountID     string    `json:"account_id"`
	Subject       string    `json:"subject"`
	Snippet       string    `json:"snippet,omitempty"`
	Participants  []string  `json:"participants,omitempty"`
	Unread        bool      `json:"unread"`
	Assignees     []string  `json:"assignees,omitempty"`
	MessageCount  int       `json:"message_count"`
	CommentCount  int       `json:"comment_count,omitempty"`
	LastMessageAt time.Time `json:"last_message_at"`
}

type outMail struct {
	ID          string    `json:"id"`
	From        string    `json:"from"`
	To          []string  `json:"to,omitempty"`
	Cc          []string  `json:"cc,omitempty"`
	At          time.Time `json:"at"`
	Outgoing    bool      `json:"outgoing,omitempty"`
	Draft       bool      `json:"draft,omitempty"`
	DraftID     string    `json:"draft_id,omitempty"`
	DraftedByAI string    `json:"drafted_by_ai,omitempty"`
	Attachments []string  `json:"attachments,omitempty"`
	Text        string    `json:"text"`
	Truncated   bool      `json:"truncated,omitempty"`
}

type outComment struct {
	Author string    `json:"author,omitempty"`
	At     time.Time `json:"at"`
	Text   string    `json:"text"`
}

type conversationOut struct {
	Conversation   outConversation `json:"conversation"`
	Account        string          `json:"account"`
	SendsFrom      []string        `json:"sends_from,omitempty"`
	Messages       []outMail       `json:"messages"`
	Comments       []outComment    `json:"team_comments,omitempty"`
	EarlierOmitted int             `json:"earlier_messages_omitted,omitempty"`
	TextSpent      bool            `json:"text_truncated,omitempty"`
}

func addressText(a mailAddress) string {
	if a.Name != "" && !strings.EqualFold(a.Name, a.Email) {
		return a.Name + " <" + a.Email + ">"
	}
	return a.Email
}

func emailsOf(list []mailAddress) []string {
	out := make([]string, 0, len(list))
	for _, a := range list {
		out = append(out, a.Email)
	}
	return out
}

func compactConversation(t apiMailThread) outConversation {
	o := outConversation{ID: t.ID, AccountID: t.AccountID, Subject: t.Subject, Snippet: clip(t.Snippet, 200), Unread: t.Unread,
		MessageCount: t.MessageCount, CommentCount: t.CommentCount, LastMessageAt: t.LastMessageAt}
	for _, p := range t.Participants {
		o.Participants = append(o.Participants, addressText(p))
	}
	for _, u := range t.Assignees {
		o.Assignees = append(o.Assignees, u.Name)
	}
	return o
}

// --- tools ---------------------------------------------------------------------------

func listCollabConversations(ctx context.Context, c *caller, raw json.RawMessage) (any, error) {
	a, err := decode[struct {
		Box       string `json:"box"`
		AccountID string `json:"account_id"`
		Query     string `json:"query"`
		Limit     int    `json:"limit"`
		Cursor    string `json:"cursor"`
	}](raw)
	if err != nil {
		return nil, err
	}
	if err := oneOf(a.Box, "box", "inbox", "unread", "assigned", "shared", "starred", "all"); err != nil {
		return nil, err
	}
	q := url.Values{}
	if a.Box != "" {
		q.Set("box", a.Box)
	}
	if a.AccountID != "" {
		id, err := idRef(a.AccountID, "account_id")
		if err != nil {
			return nil, err
		}
		q.Set("account_id", id)
	}
	if s := strings.TrimSpace(a.Query); s != "" {
		q.Set("q", s)
	}
	limit := a.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > maxListConversations {
		return nil, invalid("limit must be at most 50")
	}
	q.Set("limit", strconv.Itoa(limit))
	if a.Cursor != "" {
		q.Set("cursor", a.Cursor)
	}
	var pg page[apiMailThread]
	if err := c.get(ctx, "/v1/collab/threads", q, &pg); err != nil {
		return nil, err
	}
	out := struct {
		Conversations []outConversation `json:"conversations"`
		NextCursor    *string           `json:"next_cursor,omitempty"`
	}{Conversations: []outConversation{}, NextCursor: pg.NextCursor}
	for _, t := range pg.Data {
		out.Conversations = append(out.Conversations, compactConversation(t))
	}
	return out, nil
}

func loadConversation(ctx context.Context, c *caller, rawID string) (*apiMailDetail, error) {
	id, err := idRef(rawID, "conversation")
	if err != nil {
		return nil, err
	}
	var d apiMailDetail
	if err := c.get(ctx, "/v1/collab/threads/"+id, nil, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func getCollabConversation(ctx context.Context, c *caller, raw json.RawMessage) (any, error) {
	a, err := decode[struct {
		Conversation string `json:"conversation"`
	}](raw)
	if err != nil {
		return nil, err
	}
	d, err := loadConversation(ctx, c, a.Conversation)
	if err != nil {
		return nil, err
	}
	out := conversationOut{Conversation: compactConversation(d.Thread), Account: d.Account.Email, Messages: []outMail{}}
	for _, id := range d.Account.Identities {
		if id.CanSend {
			out.SendsFrom = append(out.SendsFrom, id.Email)
		}
	}
	list := d.Messages
	if len(list) > threadMessages {
		out.EarlierOmitted = len(list) - threadMessages
		list = list[len(list)-threadMessages:]
	}
	// The newest messages matter most: spend the text budget from the end.
	budget := threadChars
	rendered := make([]outMail, len(list))
	for i := len(list) - 1; i >= 0; i-- {
		m := list[i]
		om := outMail{ID: m.ID, From: addressText(m.From), To: emailsOf(m.To), Cc: emailsOf(m.Cc), At: m.SentAt, Outgoing: m.Outgoing, Draft: m.Draft}
		if m.DraftID != nil {
			om.DraftID = *m.DraftID
		}
		if m.DraftedByAI != nil {
			om.DraftedByAI = *m.DraftedByAI
		}
		for _, at := range m.Attachments {
			if !at.Inline {
				om.Attachments = append(om.Attachments, at.Filename)
			}
		}
		n := min(messageChars, budget)
		text := strings.TrimSpace(m.BodyText)
		if utf8.RuneCountInString(text) > n {
			text, om.Truncated = clip(text, n), true
		}
		budget -= utf8.RuneCountInString(text)
		if budget <= 0 {
			budget = 0
			out.TextSpent = true
		}
		om.Text = text
		rendered[i] = om
	}
	out.Messages = rendered
	for _, cm := range d.Comments {
		oc := outComment{At: cm.CreatedAt, Text: clip(email.HTMLToText(cm.BodyHTML), messageChars)}
		if cm.Author != nil {
			oc.Author = cm.Author.Name
		}
		out.Comments = append(out.Comments, oc)
	}
	return out, nil
}

type collabDraftResult struct {
	DraftID      string   `json:"draft_id"`
	Conversation string   `json:"conversation"`
	To           []string `json:"to"`
	Cc           []string `json:"cc,omitempty"`
	Subject      string   `json:"subject"`
	DraftedByAI  string   `json:"drafted_by_ai,omitempty"`
	Note         string   `json:"note"`
}

const collabDraftNote = "saved as a draft, not sent; a person reviews, edits and sends it"

func draftCollabReply(ctx context.Context, c *caller, raw json.RawMessage) (any, error) {
	a, err := decode[struct {
		Conversation string   `json:"conversation"`
		Body         string   `json:"body"`
		ReplyAll     bool     `json:"reply_all"`
		MessageID    string   `json:"message_id"`
		DraftID      string   `json:"draft_id"`
		To           []string `json:"to"`
		Cc           []string `json:"cc"`
	}](raw)
	if err != nil {
		return nil, err
	}
	if len(a.To) > 20 || len(a.Cc) > 20 {
		return nil, invalid("at most 20 to and 20 cc addresses")
	}
	html, err := markdownBody(a.Body)
	if err != nil {
		return nil, err
	}
	d, err := loadConversation(ctx, c, a.Conversation)
	if err != nil {
		return nil, err
	}
	var saved struct {
		DraftID     *string       `json:"draft_id"`
		To          []mailAddress `json:"to"`
		Cc          []mailAddress `json:"cc"`
		Subject     string        `json:"subject"`
		DraftedByAI *string       `json:"drafted_by_ai"`
	}
	if a.DraftID != "" {
		id, err := idRef(a.DraftID, "draft_id")
		if err != nil {
			return nil, err
		}
		var draft *apiMailMessage
		for i := range d.Messages {
			if m := &d.Messages[i]; m.Draft && m.DraftID != nil && *m.DraftID == id {
				draft = m
			}
		}
		if draft == nil {
			return nil, invalid("draft_id is not a draft of this conversation")
		}
		to, cc := emailsOf(draft.To), emailsOf(draft.Cc)
		if a.To != nil {
			to = a.To
		}
		if a.Cc != nil {
			cc = a.Cc
		}
		body := map[string]any{"to": to, "cc": cc, "subject": draft.Subject, "body_html": html}
		if err := c.do(ctx, http.MethodPut, "/v1/collab/drafts/"+id, nil, body, &saved); err != nil {
			return nil, err
		}
	} else {
		orig, err := answered(d, a.MessageID)
		if err != nil {
			return nil, err
		}
		to, cc := replyRecipients(d, orig, a.ReplyAll)
		if a.To != nil {
			to = a.To
		}
		if a.Cc != nil {
			cc = a.Cc
		}
		if len(to)+len(cc) == 0 {
			return nil, invalid("the message has nobody to reply to: pass to")
		}
		mode := "reply"
		if a.ReplyAll {
			mode = "reply_all"
		}
		body := map[string]any{"account_id": d.Account.ID, "mode": mode, "message_id": orig.ID, "to": to, "cc": cc,
			"subject": "", "body_html": html}
		if err := c.do(ctx, http.MethodPost, "/v1/collab/drafts", nil, body, &saved); err != nil {
			return nil, err
		}
	}
	out := collabDraftResult{Conversation: d.Thread.ID, To: emailsOf(saved.To), Cc: emailsOf(saved.Cc), Subject: saved.Subject, Note: collabDraftNote}
	if saved.DraftID != nil {
		out.DraftID = *saved.DraftID
	}
	if saved.DraftedByAI != nil {
		out.DraftedByAI = *saved.DraftedByAI
	}
	return out, nil
}

// answered is the message a reply answers: the one named, else the latest that is not a draft.
func answered(d *apiMailDetail, rawID string) (*apiMailMessage, error) {
	if rawID != "" {
		id, err := idRef(rawID, "message_id")
		if err != nil {
			return nil, err
		}
		for i := range d.Messages {
			if m := &d.Messages[i]; m.ID == id && !m.Draft {
				return m, nil
			}
		}
		return nil, invalid("message_id is not a message of this conversation")
	}
	for i := len(d.Messages) - 1; i >= 0; i-- {
		if !d.Messages[i].Draft {
			return &d.Messages[i], nil
		}
	}
	return nil, invalid("the conversation has no message to reply to")
}

// replyRecipients are who a reply goes to, as the composer picks them: the sender (its
// Reply-To when set), or for mail the account wrote, its recipients; reply all adds the
// others on the message. The account's own addresses are left out.
func replyRecipients(d *apiMailDetail, m *apiMailMessage, all bool) (to, cc []string) {
	own := map[string]bool{strings.ToLower(d.Account.Email): true}
	for _, id := range d.Account.Identities {
		own[strings.ToLower(id.Email)] = true
	}
	seen := map[string]bool{}
	add := func(list *[]string, addrs ...mailAddress) {
		for _, x := range addrs {
			e := strings.ToLower(strings.TrimSpace(x.Email))
			if e == "" || own[e] || seen[e] {
				continue
			}
			seen[e] = true
			*list = append(*list, e)
		}
	}
	to, cc = []string{}, []string{}
	switch {
	case m.Outgoing:
		add(&to, m.To...)
	case len(m.ReplyTo) > 0:
		add(&to, m.ReplyTo...)
	default:
		add(&to, m.From)
	}
	if all {
		if !m.Outgoing {
			add(&cc, m.To...)
		}
		add(&cc, m.Cc...)
	}
	return to, cc
}
