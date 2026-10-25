package collab

import (
	"context"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/db"
)

// Mail for a teammate's Ollie (contracts/ai.md "Asking a teammate"). The owner of the mail
// consented to it, account by account; the excerpts reach only the model that answers on
// the owner's behalf, never the teammate who asked.

// Peer share modes of an account.
const (
	PeerVerbatim = "verbatim"
	PeerSummary  = "summary"
	PeerNone     = "none"
)

// Peer excerpt caps.
const (
	PeerMaxThreads = 8
	peerMaxChars   = 1500
	peerMaxTerms   = 12
)

// PeerExcerpt is one conversation for the answering model: no ids, addresses, links or
// attachments.
type PeerExcerpt struct {
	Subject string
	From    string
	At      time.Time
	Text    string
	// Mode is the account's share mode: verbatim or summary.
	Mode string
}

// OwnAccounts lists the Collab accounts a member owns in the workspace: theirs alone, not
// those they see through someone else's share.
func OwnAccounts(ctx context.Context, q db.Querier, wid, owner uuid.UUID) ([]OwnAccount, error) {
	rows, err := q.Query(ctx, `SELECT id, email, name, display_name, provider FROM collab_accounts
		WHERE workspace_id = $1 AND owner_id = $2 ORDER BY created_at, id`, wid, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OwnAccount
	for rows.Next() {
		var a OwnAccount
		var display string
		if err := rows.Scan(&a.ID, &a.Email, &a.Name, &display, &a.Provider); err != nil {
			return nil, err
		}
		if display != "" {
			a.Name = display
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// OwnAccount is one of a member's own Collab accounts.
type OwnAccount struct {
	ID       uuid.UUID
	Email    string
	Name     string
	Provider string
}

// PeerExcerpts searches the owner's own accounts among those in modes whose mode is not
// none, for the conversations that best match query, at most n (PeerMaxThreads). Mail in
// spam or trash is left out. Each excerpt is the conversation's latest messages as plain
// text, addresses and links removed, capped.
func PeerExcerpts(ctx context.Context, q db.Querier, wid, owner uuid.UUID, modes map[uuid.UUID]string, query string, n int) ([]PeerExcerpt, error) {
	allowed := make([]uuid.UUID, 0, len(modes))
	for id, m := range modes {
		if m == PeerVerbatim || m == PeerSummary {
			allowed = append(allowed, id)
		}
	}
	terms := peerTerms(query)
	if len(allowed) == 0 || len(terms) == 0 {
		return nil, nil
	}
	n = min(max(n, 1), PeerMaxThreads)
	tsq := strings.Join(terms, " or ")
	rows, err := q.Query(ctx, `SELECT t.id, t.account_id, t.subject, t.last_message_at FROM collab_threads t
		JOIN collab_accounts a ON a.id = t.account_id
		WHERE t.workspace_id = $1 AND a.workspace_id = $1 AND a.owner_id = $2 AND a.id = ANY($3)
			AND t.search @@ websearch_to_tsquery('simple', $4)
			AND NOT EXISTS (SELECT 1 FROM collab_folders f WHERE f.id = ANY(t.folder_ids) AND f.role IN ('spam', 'trash'))
		ORDER BY ts_rank(t.search, websearch_to_tsquery('simple', $4)) DESC, t.last_message_at DESC, t.id DESC
		LIMIT $5`, wid, owner, allowed, tsq, n)
	if err != nil {
		return nil, err
	}
	type hit struct {
		id, account uuid.UUID
		subject     string
		at          time.Time
	}
	var hits []hit
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.id, &h.account, &h.subject, &h.at); err != nil {
			rows.Close()
			return nil, err
		}
		hits = append(hits, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]PeerExcerpt, 0, len(hits))
	for _, h := range hits {
		ex := PeerExcerpt{Subject: peerClean(h.subject, 200), At: h.at, Mode: modes[h.account]}
		mrows, err := q.Query(ctx, `SELECT from_addr, body_text, snippet, sent_at FROM collab_messages
			WHERE workspace_id = $1 AND thread_id = $2 AND NOT draft ORDER BY sent_at DESC, id DESC LIMIT 3`, wid, h.id)
		if err != nil {
			return nil, err
		}
		var parts []string
		for mrows.Next() {
			var from Address
			var body, snippet string
			var at time.Time
			if err := mrows.Scan(&from, &body, &snippet, &at); err != nil {
				mrows.Close()
				return nil, err
			}
			name := peerClean(from.Name, 80)
			if name == "" {
				name = "someone"
			}
			if ex.From == "" {
				ex.From = name
			}
			if body == "" {
				body = snippet
			}
			parts = append(parts, name+", "+at.UTC().Format("2006-01-02")+": "+peerClean(body, peerMaxChars))
		}
		mrows.Close()
		if err := mrows.Err(); err != nil {
			return nil, err
		}
		// Oldest of the latest messages first, the whole capped.
		for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
			parts[i], parts[j] = parts[j], parts[i]
		}
		ex.Text = clipRunes(strings.Join(parts, "\n"), peerMaxChars)
		out = append(out, ex)
	}
	return out, nil
}

var (
	peerAddress = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	peerLink    = regexp.MustCompile(`(?i)\b(?:https?://|www\.)\S+`)
)

// peerClean is text without addresses or links, on one line per paragraph, capped.
func peerClean(s string, n int) string {
	s = peerLink.ReplaceAllString(s, "[link]")
	s = peerAddress.ReplaceAllString(s, "[address]")
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	kept := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.Join(strings.Fields(l), " ")
		// Quoted earlier messages repeat what the excerpt already has.
		if l == "" || strings.HasPrefix(l, ">") {
			continue
		}
		kept = append(kept, l)
	}
	return clipRunes(strings.Join(kept, " "), n)
}

// peerStop are words a question has that say nothing about which mail answers it.
var peerStop = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`a an and are as at be by can could did do does for from had has have her his how i if in is it its
		me my of on or our she so that the their them they this to was we were what when where which who whom why will with would
		you your about any ask tell know please there been get got than then into out up down just also find let`) {
		peerStop[w] = true
	}
}

// peerTerms are the search words of a question: letters and digits, without stop words,
// at most peerMaxTerms.
func peerTerms(q string) []string {
	var out []string
	seen := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(q), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len([]rune(w)) < 2 || peerStop[w] || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
		if len(out) == peerMaxTerms {
			break
		}
	}
	return out
}
