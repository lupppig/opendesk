package ai

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
)

// Untrusted content (customer messages, KB articles, contact fields) goes into delimited
// blocks. The angle brackets inside it are replaced with look-alike characters so the
// content cannot close a block or open a new one, whatever it contains.
var angle = strings.NewReplacer("<", "‹", ">", "›")

// untrusted prepares stored text for a data block: HTML stripped to text upstream,
// delimiters neutralized, invalid UTF-8 dropped.
func untrusted(s string) string {
	return angle.Replace(strings.ToValidUTF8(s, ""))
}

// attr is a value placed inside a block's opening tag.
func attr(s string) string {
	s = strings.Join(strings.Fields(untrusted(s)), " ")
	return strings.ReplaceAll(s, `"`, "'")
}

// block wraps untrusted content in <tag attrs>...</tag>.
func block(tag string, attrs []string, body string) string {
	var b strings.Builder
	b.WriteString("<" + tag)
	for i := 0; i+1 < len(attrs); i += 2 {
		if attrs[i+1] == "" {
			continue
		}
		b.WriteString(" " + attrs[i] + `="` + attr(attrs[i+1]) + `"`)
	}
	b.WriteString(">\n")
	b.WriteString(strings.TrimSpace(body))
	b.WriteString("\n</" + tag + ">")
	return b.String()
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)[:n]
	return strings.TrimSpace(string(r)) + " […]"
}

// messageText is a message body as plain text for a prompt.
func messageText(m domain.Message) string {
	t := strings.TrimSpace(m.BodyText)
	if t == "" {
		t = hd.HTMLToText(m.BodyHTML)
	}
	if len(m.Attachments) > 0 {
		names := make([]string, 0, len(m.Attachments))
		for _, a := range m.Attachments {
			names = append(names, a.Filename)
		}
		t += "\n[attachments: " + strings.Join(names, ", ") + "]"
	}
	return t
}

// role is who wrote a message, as the prompts name it.
func role(m domain.Message) string {
	switch {
	// A side conversation (contracts/api.md "Threads") is the team's with someone outside
	// it: private like a note, but what the other side writes is not the team's word.
	case m.ThreadID != nil && m.Direction == "inbound":
		return "third_party"
	case m.ThreadID != nil:
		return "side_conversation"
	case m.Kind == "note":
		return "internal_note"
	case m.Direction == "inbound":
		return "customer"
	case m.AI:
		return "ai_assistant"
	case m.Author.Type == "user":
		return "agent"
	default:
		return "system"
	}
}

// author is the name a message is shown under. The AI's own replies have none here: the
// role names them, and their from_name is the From name the customer saw ("Ollie at Acme").
func author(m domain.Message) string {
	switch {
	case m.AI && m.Author.User == nil:
		return ""
	case m.Author.User != nil:
		return m.Author.User.Name
	case m.Author.Contact != nil:
		return m.Author.Contact.Name
	case m.FromName != "":
		return m.FromName
	}
	return ""
}

const (
	perMessageChars = 6000
	// threadChars bounds the thread in a prompt (about 15k tokens).
	threadChars = 60000
)

// renderThread renders a conversation for a prompt within budget runes. It always keeps
// the first message (the original request), then as many of the latest messages as fit;
// skipped messages are replaced by a marker and, when one exists, the stored summary.
func renderThread(msgs []domain.Message, summary string, budget int) string {
	if budget <= 0 {
		budget = threadChars
	}
	rendered := make([]string, len(msgs))
	for i, m := range msgs {
		rendered[i] = block("message", []string{"n", strconv.Itoa(i + 1), "from", role(m), "name", author(m),
			"at", m.CreatedAt.UTC().Format("2006-01-02 15:04 UTC")}, clip(untrusted(messageText(m)), perMessageChars))
	}
	if len(rendered) == 0 {
		return block("ticket_thread", nil, "(no messages yet)")
	}
	used := utf8.RuneCountInString(rendered[0])
	keepFrom := len(rendered)
	for i := len(rendered) - 1; i >= 1; i-- {
		n := utf8.RuneCountInString(rendered[i])
		if used+n > budget {
			break
		}
		used += n
		keepFrom = i
	}
	parts := []string{rendered[0]}
	if skipped := keepFrom - 1; skipped > 0 {
		note := fmt.Sprintf("[%d earlier messages omitted for length]", skipped)
		if summary != "" {
			note += "\n" + block("earlier_summary", nil, untrusted(summary))
		}
		parts = append(parts, note)
	}
	parts = append(parts, rendered[keepFrom:]...)
	return block("ticket_thread", []string{"messages", strconv.Itoa(len(msgs))}, strings.Join(parts, "\n"))
}

// lastCustomerText is the latest customer message, for retrieval queries.
func lastCustomerText(msgs []domain.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Kind == "reply" && msgs[i].Direction == "inbound" {
			return messageText(msgs[i])
		}
	}
	return ""
}

// kbSource is a retrieved knowledge base passage.
type kbSource struct {
	ArticleID string  `json:"article_id"`
	Title     string  `json:"title"`
	URL       string  `json:"url"`
	Passage   string  `json:"-"`
	Score     float64 `json:"-"`
}

// KBSource is the contract shape.
type KBSource struct {
	ArticleID string `json:"article_id"`
	Title     string `json:"title"`
	URL       string `json:"url"`
}

func publicSources(in []kbSource) []KBSource {
	out := make([]KBSource, 0, len(in))
	for _, s := range in {
		out = append(out, KBSource{ArticleID: s.ArticleID, Title: s.Title, URL: s.URL})
	}
	return out
}

// renderKB renders retrieved passages with numeric ids the model can cite.
func renderKB(src []kbSource) string {
	if len(src) == 0 {
		return block("knowledge_base", nil, "(no relevant published articles found)")
	}
	parts := make([]string, 0, len(src))
	for i, s := range src {
		parts = append(parts, block("article", []string{"id", strconv.Itoa(i + 1), "title", s.Title, "url", s.URL}, untrusted(s.Passage)))
	}
	return block("knowledge_base", nil, strings.Join(parts, "\n"))
}

// renderTicket renders a ticket's metadata.
func renderTicket(t domain.Ticket) string {
	tags := make([]string, 0, len(t.Tags))
	for _, g := range t.Tags {
		tags = append(tags, g.Name)
	}
	body := "subject: " + untrusted(t.Subject)
	if len(tags) > 0 {
		body += "\ntags: " + untrusted(strings.Join(tags, ", "))
	}
	return block("ticket", []string{"number", strconv.FormatInt(t.Number, 10), "channel", t.Channel, "status", t.Status,
		"priority", t.Priority, "severity", severityAttr(t.Severity), "created", t.CreatedAt.UTC().Format(time.DateOnly)}, body)
}

// renderContact renders what the reply may use about the customer.
func renderContact(c *domain.Contact) string {
	if c == nil {
		return block("customer", nil, "(unknown)")
	}
	lines := []string{"name: " + untrusted(c.Name)}
	if first := firstName(c.Name); first != "" {
		lines = append(lines, "first name: "+untrusted(first))
	}
	if c.Email != nil {
		lines = append(lines, "email: "+untrusted(*c.Email))
	}
	if c.Company != nil && c.Company.Name != "" {
		lines = append(lines, "company: "+untrusted(c.Company.Name))
	}
	lines = append(lines, fmt.Sprintf("previous tickets: %d", max(c.TicketCount-1, 0)))
	return block("customer", nil, strings.Join(lines, "\n"))
}

var looksLikeName = regexp.MustCompile(`^\p{Lu}[\p{L}'’-]{1,30}$`)

// firstName returns a usable first name, or "" when the name looks like an address,
// handle or placeholder.
func firstName(full string) string {
	f := strings.Fields(full)
	if len(f) == 0 || strings.ContainsAny(full, "@0123456789_") {
		return ""
	}
	if looksLikeName.MatchString(f[0]) {
		return f[0]
	}
	return ""
}

var (
	mdLink   = regexp.MustCompile(`\[([^\]\n]{1,300})\]\(((?:https?://|mailto:)[^\s)]+)\)`)
	mdBold   = regexp.MustCompile(`\*\*([^*\n]{1,500})\*\*`)
	mdCode   = regexp.MustCompile("`([^`\n]{1,300})`")
	mdBullet = regexp.MustCompile(`^\s*[-*•]\s+`)
	mdNumber = regexp.MustCompile(`^\s*\d{1,3}[.)]\s+`)
	mdHead   = regexp.MustCompile(`^#{1,6}\s+`)
	htmlLike = regexp.MustCompile(`(?i)<(p|br|ul|ol|li|strong|em|a|h[1-6]|div)[\s>/]`)
	fence    = regexp.MustCompile("(?s)^```[a-zA-Z]*\\s*\\n?(.*?)\\n?```\\s*$")
)

// stripFence removes a ``` fence the model wrapped its whole output in.
func stripFence(s string) string {
	s = strings.TrimSpace(s)
	if m := fence.FindStringSubmatch(s); m != nil {
		return strings.TrimSpace(m[1])
	}
	return s
}

// markdownToHTML converts the light markdown the prompts ask for into HTML, then
// applies the rich-text policy. HTML output from the model is sanitized directly.
func markdownToHTML(md string) string {
	md = stripFence(md)
	if htmlLike.MatchString(md) {
		return hd.SanitizeRich(md)
	}
	var b strings.Builder
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	list := ""
	para := []string{}
	flushPara := func() {
		if len(para) > 0 {
			b.WriteString("<p>" + strings.Join(para, "<br>") + "</p>")
			para = para[:0]
		}
	}
	closeList := func() {
		if list != "" {
			b.WriteString("</" + list + ">")
			list = ""
		}
	}
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		switch {
		case t == "":
			flushPara()
			closeList()
		case mdBullet.MatchString(ln) || mdNumber.MatchString(ln):
			flushPara()
			kind := "ul"
			item := mdBullet.ReplaceAllString(ln, "")
			if mdNumber.MatchString(ln) {
				kind, item = "ol", mdNumber.ReplaceAllString(ln, "")
			}
			if list != kind {
				closeList()
				b.WriteString("<" + kind + ">")
				list = kind
			}
			b.WriteString("<li>" + inlineMD(strings.TrimSpace(item)) + "</li>")
		default:
			closeList()
			if mdHead.MatchString(t) {
				flushPara()
				b.WriteString("<p><strong>" + inlineMD(mdHead.ReplaceAllString(t, "")) + "</strong></p>")
				continue
			}
			para = append(para, inlineMD(t))
		}
	}
	flushPara()
	closeList()
	return hd.SanitizeRich(b.String())
}

func inlineMD(s string) string {
	s = html.EscapeString(s)
	s = mdLink.ReplaceAllStringFunc(s, func(m string) string {
		sub := mdLink.FindStringSubmatch(m)
		return `<a href="` + sub[2] + `">` + sub[1] + `</a>`
	})
	s = mdBold.ReplaceAllString(s, "<strong>$1</strong>")
	s = mdCode.ReplaceAllString(s, "<code>$1</code>")
	return s
}

// plainFromMarkdown is the text version of a markdown draft.
func plainFromMarkdown(md string) string {
	return hd.HTMLToText(markdownToHTML(md))
}

func severityAttr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
