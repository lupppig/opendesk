package imports

import (
	"strings"

	"github.com/useopendesk/opendesk/internal/helpdesk/imports/source"
)

// Postgres text and jsonb cannot hold NUL bytes (U+0000), and source data can carry them,
// most often in email bodies and headers. The writer drops them from every text a ticket
// or article saves, once, before anything reads it. Ids and the links files are fetched
// from are kept as the source has them: they ask the source, and match a ticket, message
// or file to its earlier copy.

func dropNUL(s string) string {
	if strings.IndexByte(s, 0) < 0 {
		return s
	}
	return strings.ReplaceAll(s, "\x00", "")
}

// dropNULs returns list, or a copy of it when a value changed.
func dropNULs(list []string) []string {
	var out []string
	for i, s := range list {
		if c := dropNUL(s); c != s {
			if out == nil {
				out = append([]string(nil), list...)
			}
			out[i] = c
		}
	}
	if out == nil {
		return list
	}
	return out
}

func scrubPerson(p source.Person) source.Person {
	p.Email, p.Name = dropNUL(p.Email), dropNUL(p.Name)
	p.Phone, p.Company, p.Domain = dropNUL(p.Phone), dropNUL(p.Company), dropNUL(p.Domain)
	return p
}

// scrubTicket drops NUL bytes from every text a ticket saves: its ref, link, subject and
// tags, its requester, and its messages' bodies, authors, recipients and file names. It
// copies what it changes, so the source's own values are left as they are.
func scrubTicket(t *source.Ticket) {
	t.Ref, t.URL, t.Subject = dropNUL(t.Ref), dropNUL(t.URL), dropNUL(t.Subject)
	t.Tags = dropNULs(t.Tags)
	if t.Requester != nil {
		p := scrubPerson(*t.Requester)
		t.Requester = &p
	}
	if len(t.Messages) == 0 {
		return
	}
	msgs := make([]source.Message, len(t.Messages))
	for i, m := range t.Messages {
		m.HTML, m.Text = dropNUL(m.HTML), dropNUL(m.Text)
		m.Author = scrubPerson(m.Author)
		m.To, m.Cc = dropNULs(m.To), dropNULs(m.Cc)
		if len(m.Attachments) > 0 {
			files := make([]source.Attachment, len(m.Attachments))
			for j, a := range m.Attachments {
				a.Name, a.ContentType = dropNUL(a.Name), dropNUL(a.ContentType)
				files[j] = a
			}
			m.Attachments = files
		}
		msgs[i] = m
	}
	t.Messages = msgs
}

// scrubArticle drops NUL bytes from an article's title, body and collection.
func scrubArticle(a *source.Article) {
	a.Title, a.HTML, a.Collection = dropNUL(a.Title), dropNUL(a.HTML), dropNUL(a.Collection)
}
