package sample

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/collab"
)

type mailAccountDef struct {
	local, name, color string
	shareKey           string // "": private; "everyone"; else a team key
}

var mailAccountDefs = []mailAccountDef{
	{"", "Work", "blue", ""},
	{"sales", "Sales", "green", "everyone"},
	{"partners", "Partnerships", "violet", "partnerships"},
	{"accounts", "Accounts", "amber", "billing"},
}

// seedCollab adds demo Collab accounts (they never connect to a provider or send) with
// conversations: the owner's own private account first, then shared ones.
func (g *gen) seedCollab(ctx context.Context) error {
	n := min(g.vol.mailAccounts, len(mailAccountDefs))
	for i := range n {
		def := mailAccountDefs[i]
		local, display := def.local, g.name+" "+def.name
		owner := g.owner
		if local == "" {
			local, display = localPart(g.owner.first()), g.owner.name
		}
		in := collab.DemoAccount{OwnerID: owner.id, Email: local + g.suffix() + "@" + g.site, Name: def.name, DisplayName: display,
			Color: def.color, Folders: mailFolders, SignatureHTML: "<p>" + display + "</p>"}
		var audience []*member // who may write from it
		switch def.shareKey {
		case "":
		case "everyone":
			in.Shared = collab.Audience{Everyone: true}
			audience = g.members
		default:
			t := g.teamFor(def.shareKey)
			if t == nil {
				in.Shared = collab.Audience{Everyone: true}
				audience = g.members
			} else {
				in.Shared = collab.Audience{TeamIDs: []uuid.UUID{t.id}}
				audience = t.members
			}
		}
		id, err := collab.CreateDemoAccount(ctx, g.d, g.wid, in)
		if err != nil {
			return fmt.Errorf("add demo mail account: %w", err)
		}
		g.counts.MailAccounts++
		count := g.vol.mailThreads
		if n > 1 {
			count = g.vol.mailThreads * 2 / 5
			if i > 0 {
				count = (g.vol.mailThreads - g.vol.mailThreads*2/5) / (n - 1)
			}
		}
		threads := g.mailThreads(i, collab.Address{Name: display, Email: in.Email}, in.Shared.Empty(), audience, count)
		stored, err := collab.AddDemoThreads(ctx, g.d, g.wid, id, threads)
		g.counts.MailThreads += stored
		if err != nil {
			return fmt.Errorf("add demo mail: %w", err)
		}
	}
	return nil
}

// mailThreads writes an account's conversations: a mix of people and notifications,
// mostly in the inbox, newest ones unread, with a few drafts. On a shared account some
// are assigned or discussed; on a private one a couple are shared on their own.
func (g *gen) mailThreads(acct int, us collab.Address, private bool, audience []*member, count int) []collab.DemoThread {
	if acct == 0 && count > 1 {
		count-- // the new-mail draft below is the last one
	}
	out := make([]collab.DemoThread, 0, count+1)
	writers := audience
	if len(writers) == 0 {
		writers = []*member{g.owner}
	}
	for i := range count {
		st, person := pick(g, mailStories), pick(g, mailPeople)
		if g.chance(0.15) {
			st, person = pick(g, mailNotices), pick(g, mailNotifiers)
		}
		them := collab.Address{Name: person.name, Email: person.email}
		var start time.Time
		if i < max(2, count/6) {
			start = g.when(g.ago(36*time.Hour), g.ago(20*time.Minute))
		} else {
			start = g.when(g.ago(45*day), g.ago(2*time.Hour))
		}
		place := []string{collab.RoleInbox, collab.RoleArchive, "folder", collab.RoleSpam, collab.RoleTrash}[g.weighted(0.62, 0.2, 0.1, 0.04, 0.04)]
		if place == "folder" {
			place = pick(g, mailFolders)
		}
		n := 1
		if !st.automated {
			n = 1 + g.weighted(0.45, 0.35, 0.2)
		}
		v := map[string]string{"first": firstOf(person.name), "company": person.company, "number": fmt.Sprint(g.between(1000, 9999))}
		var t collab.DemoThread
		at := start
		for k := range n {
			if k > 0 {
				next, ok := g.next(at, g.minutes(180, 1.0, 10, 2880))
				if !ok {
					break
				}
				at = next
			}
			subject := g.fill(st.subject, false, v)
			if k > 0 {
				subject = "Re: " + subject
			}
			m := collab.DemoMessage{Subject: subject, Date: at}
			if k%2 == 0 {
				m.From, m.To = them, []collab.Address{us}
				m.HTML = textHTML(g.fill(st.body, false, v))
				m.Folders = []string{place}
			} else {
				m.From, m.To = us, []collab.Address{them}
				m.HTML = textHTML(g.fill(pick(g, mailReplies), false, v))
				m.Folders = []string{collab.RoleSent}
				if place == collab.RoleInbox {
					m.Folders = append(m.Folders, collab.RoleInbox)
				}
				if !private {
					w := pick(g, writers)
					m.SentBy = &w.id
				}
			}
			if k == 0 && !st.automated && g.chance(0.2) {
				other := pick(g, mailPeople)
				if other.email != person.email {
					m.Cc = []collab.Address{{Name: other.name, Email: other.email}}
				}
			}
			t.Messages = append(t.Messages, m)
		}
		last := &t.Messages[len(t.Messages)-1]
		last.Unread = last.From.Email == them.Email && i < max(2, count/5) && place == collab.RoleInbox
		last.Starred = g.chance(0.08)
		switch {
		case private && i < 2 && len(g.teams) > 0:
			t.SharedWith = collab.Audience{TeamIDs: []uuid.UUID{g.teams[0].id}}
		case !private && len(audience) > 0 && g.chance(0.12):
			t.Assignees = []uuid.UUID{pick(g, audience).id}
		}
		if !private && len(audience) > 1 && g.chance(0.1) {
			author := pick(g, audience)
			other := g.otherThan(author)
			t.Comments = append(t.Comments, collab.DemoComment{AuthorID: author.id, BodyHTML: g.fill(pick(g, mailComments), true,
				map[string]string{"mention": mention(other)}), Mentions: []uuid.UUID{other.id}, At: last.Date.Add(20 * time.Minute)})
		}
		out = append(out, t)
	}
	// Drafts: a reply being written in the newest conversation, and new mail not sent yet.
	if len(out) > 0 {
		t := &out[0]
		first := t.Messages[0]
		if at, ok := g.next(t.Messages[len(t.Messages)-1].Date, 25*time.Minute); ok {
			t.Messages = append(t.Messages, collab.DemoMessage{From: us, To: []collab.Address{first.From}, Subject: "Re: " + first.Subject,
				HTML: textHTML(fmt.Sprintf("Thanks, %s. I'll confirm by Friday.", firstOf(first.From.Name))), Date: at, Draft: true,
				SentBy: &g.owner.id, Folders: []string{collab.RoleDrafts}})
		}
	}
	if acct == 0 {
		p := mailPeople[4]
		out = append(out, collab.DemoThread{Messages: []collab.DemoMessage{{From: us, To: []collab.Address{{Name: p.name, Email: p.email}},
			Subject: "Offsite venue options", Date: g.ago(3 * time.Hour), Draft: true, SentBy: &g.owner.id, Folders: []string{collab.RoleDrafts},
			HTML: textHTML("Hi " + firstOf(p.name) + ", here are the three venues I liked for the offsite. The lake house fits everyone and has a good room for the planning day.")}}})
	}
	return out
}

func firstOf(name string) string {
	for i, r := range name {
		if r == ' ' {
			return name[:i]
		}
	}
	return name
}
