package imports

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"

	"github.com/useopendesk/opendesk/internal/helpdesk/imports/source"
	"github.com/useopendesk/opendesk/internal/httpx"
)

func mustParse(t *testing.T, raw []byte) *csvFile {
	t.Helper()
	f, err := parseCSV(raw)
	if err != nil {
		t.Fatalf("parseCSV: %v", err)
	}
	return f
}

func TestParseCSVEncodings(t *testing.T) {
	plain := "Subject,Body\nHello,World\n"
	want := [][]string{{"Hello", "World"}}

	f := mustParse(t, []byte(plain))
	if !slices.Equal(f.Header, []string{"Subject", "Body"}) || !slices.Equal(f.Rows[0], want[0]) {
		t.Fatalf("plain: %+v", f)
	}

	f = mustParse(t, append([]byte{0xEF, 0xBB, 0xBF}, plain...))
	if f.Header[0] != "Subject" {
		t.Fatalf("utf-8 bom left in header: %q", f.Header[0])
	}

	for _, end := range []unicode.Endianness{unicode.LittleEndian, unicode.BigEndian} {
		enc, _, err := transform.Bytes(unicode.UTF16(end, unicode.UseBOM).NewEncoder(), []byte("Subject\tBody\nCafé ☕\tÜber\n"))
		if err != nil {
			t.Fatal(err)
		}
		if end == unicode.LittleEndian && !(enc[0] == 0xFF && enc[1] == 0xFE) {
			t.Fatalf("expected a little endian bom, got % x", enc[:2])
		}
		f = mustParse(t, enc)
		if !slices.Equal(f.Header, []string{"Subject", "Body"}) {
			t.Fatalf("utf-16 %v header %q", end, f.Header)
		}
		if !slices.Equal(f.Rows[0], []string{"Café ☕", "Über"}) {
			t.Fatalf("utf-16 %v row %q", end, f.Rows[0])
		}
	}

	if _, err := parseCSV([]byte("Subject\n\xff\xfe\xfd broken\n")); err == nil {
		t.Fatal("invalid utf-8 without a bom was accepted")
	}
	if _, err := parseCSV([]byte("Subject,Body\n\n , \n")); !errors.Is(err, errCSVEmpty) {
		t.Fatalf("header only: %v", err)
	}
}

func TestParseCSVSeparators(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"comma", "a,b,c\n1,2,3\n", []string{"1", "2", "3"}},
		{"semicolon", "a;b;c\n1;2,5;3\n", []string{"1", "2,5", "3"}},
		{"tab", "a\tb\tc\n1\t2;x\t3\n", []string{"1", "2;x", "3"}},
		// Separators inside quotes in the header do not count.
		{"quoted header", "\"a,b,c,d\";e;f\n1;2;3\n", []string{"1", "2", "3"}},
	}
	for _, c := range cases {
		f := mustParse(t, []byte(c.raw))
		if !slices.Equal(f.Rows[0], c.want) {
			t.Errorf("%s: row %q, want %q", c.name, f.Rows[0], c.want)
		}
	}
	if got := sniffComma([]byte("x")); got != ',' {
		t.Errorf("default separator %q", got)
	}
}

func TestParseCSVMultilineAndLines(t *testing.T) {
	raw := "ticket id,body,\n" +
		"1,\"first line\nsecond line\nthird\",\n" + // rows start on lines 2..4
		"\n" + // a blank line is skipped
		"2,\"say \"\"hi\"\"\",\n" + // line 6
		"3,short,extra\n" // line 7
	f := mustParse(t, []byte(raw))
	if len(f.Rows) != 3 {
		t.Fatalf("rows %d: %q", len(f.Rows), f.Rows)
	}
	if f.Rows[0][1] != "first line\nsecond line\nthird" {
		t.Fatalf("multiline field %q", f.Rows[0][1])
	}
	if f.Rows[1][1] != `say "hi"` {
		t.Fatalf("escaped quotes %q", f.Rows[1][1])
	}
	if !slices.Equal(f.Lines, []int{2, 6, 7}) {
		t.Fatalf("lines %v", f.Lines)
	}
	if f.Header[2] != "Column 3" {
		t.Fatalf("unnamed column %q", f.Header[2])
	}
}

func TestSuggestMapping(t *testing.T) {
	cases := []struct {
		name   string
		header []string
		want   ImportCSVMapping
	}{
		{"zendesk", []string{"Id", "Subject", "Description", "Requester", "Requester email", "Status", "Priority", "Tags",
			"Created at", "Assignee", "Group", "Organization"},
			ImportCSVMapping{TicketID: "Id", Subject: "Subject", Body: "Description", FromName: "Requester", FromEmail: "Requester email",
				Status: "Status", Priority: "Priority", Tags: "Tags", CreatedAt: "Created at", AssigneeEmail: "Assignee", Team: "Group",
				Company: "Organization"}},
		{"freshdesk", []string{"Ticket ID", "Subject", "Description", "Status", "Priority", "Source", "Agent", "Group",
			"Created time", "Last update time", "Tags", "Full name", "Email", "Company Name"},
			ImportCSVMapping{TicketID: "Ticket ID", Subject: "Subject", Body: "Description", Status: "Status", Priority: "Priority",
				AssigneeEmail: "Agent", Team: "Group", CreatedAt: "Created time", Tags: "Tags", FromName: "Full name", FromEmail: "Email",
				Company: "Company Name"}},
		{"help scout", []string{"Conversation ID", "Customer Name", "Customer Email", "Subject", "Status", "Tags", "Assigned To",
			"Mailbox", "Created At", "Body"},
			ImportCSVMapping{TicketID: "Conversation ID", FromName: "Customer Name", FromEmail: "Customer Email", Subject: "Subject",
				Status: "Status", Tags: "Tags", AssigneeEmail: "Assigned To", Team: "Mailbox", CreatedAt: "Created At",
				Body: "Body"}},
		{"jira", []string{"Summary", "Issue key", "Issue id", "Issue Type", "Status", "Priority", "Assignee", "Reporter",
			"Reporter Email", "Created", "Labels", "Description"},
			ImportCSVMapping{Subject: "Summary", TicketID: "Issue key", Status: "Status", Priority: "Priority", AssigneeEmail: "Assignee",
				FromName: "Reporter", FromEmail: "Reporter Email", CreatedAt: "Created", Tags: "Labels", Body: "Description"}},
		{"headers", []string{"Ticket ID", "Body", "Message-ID", "In-Reply-To", "References"},
			ImportCSVMapping{TicketID: "Ticket ID", Body: "Body", MessageID: "Message-ID", InReplyTo: "In-Reply-To", References: "References"}},
		{"plain", []string{"ticket_id", "from-email", "FROM NAME", "author_type", "message", "date"},
			ImportCSVMapping{TicketID: "ticket_id", FromEmail: "from-email", FromName: "FROM NAME", Author: "author_type", Body: "message",
				CreatedAt: "date"}},
	}
	for _, c := range cases {
		if got := suggestMapping(c.header); got != c.want {
			t.Errorf("%s:\n got %+v\nwant %+v", c.name, got, c.want)
		}
	}
	// One column serves one field: "Email" goes to from_email, so nothing else takes it.
	m := suggestMapping([]string{"Email", "Message"})
	if m.FromEmail != "Email" || m.FromName != "" || m.Body != "Message" {
		t.Errorf("single use %+v", m)
	}
}

func TestMessageIDs(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"<a1@mail.test>", []string{"<a1@mail.test>"}},
		{"  a1@mail.test ", []string{"<a1@mail.test>"}},
		{"<a1@mail.test> <b2@mail.test>\n\t<a1@mail.test>", []string{"<a1@mail.test>", "<b2@mail.test>"}},
		{"a1@mail.test, b2@mail.test; c3@mail.test", []string{"<a1@mail.test>", "<b2@mail.test>", "<c3@mail.test>"}},
		{"12345", nil},   // an export's own message number
		{"<12345>", nil}, // nor bracketed
	}
	for _, c := range cases {
		if got := messageIDs(c.in); !slices.Equal(got, c.want) {
			t.Errorf("messageIDs(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	var many []string
	for i := range 60 {
		many = append(many, fmt.Sprintf("<r%d@mail.test>", i))
	}
	if got := messageIDs(strings.Join(many, " ")); len(got) != maxReferences || got[0] != "<r10@mail.test>" {
		t.Errorf("keeps the latest %d: %q", maxReferences, got[:2])
	}
}

func TestCSVSourceMessageIDs(t *testing.T) {
	raw := "Ticket ID,Body,From email,Author type,Created at,Message ID,In Reply To,References\n" +
		"T1,Hello,dan@customer.test,customer,2024-01-10 08:00,<q1@customer.test>,,\n" +
		"T1,Hi Dan,anna@team.test,agent,2024-01-10 09:00,r1@team.test,<q1@customer.test>,<q1@customer.test>\n" +
		"T1,Thanks,dan@customer.test,customer,2024-01-10 10:00,7,<r1@team.test>,\"<q1@customer.test> <r1@team.test>\"\n"
	f, err := parseCSV([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	m := suggestMapping(f.Header)
	if m.MessageID != "Message ID" || m.InReplyTo != "In Reply To" || m.References != "References" {
		t.Fatalf("mapping %+v", m)
	}
	s := newCSVSource(f, m, "export.csv", "k")
	msgs := s.tickets[0].Messages
	if len(msgs) != 3 {
		t.Fatalf("messages %d", len(msgs))
	}
	if msgs[0].MessageID != "<q1@customer.test>" || msgs[0].InReplyTo != "" || msgs[0].References != nil {
		t.Errorf("first %+v", msgs[0])
	}
	if msgs[1].MessageID != "<r1@team.test>" || msgs[1].InReplyTo != "<q1@customer.test>" || !slices.Equal(msgs[1].References, []string{"<q1@customer.test>"}) {
		t.Errorf("reply %+v", msgs[1])
	}
	if msgs[2].MessageID != "" || msgs[2].InReplyTo != "<r1@team.test>" || len(msgs[2].References) != 2 {
		t.Errorf("third %+v", msgs[2])
	}
}

func TestCheckMapping(t *testing.T) {
	header := []string{"Subject", "Body", "Email"}
	if err := checkMapping(ImportCSVMapping{Body: "Body"}, header); err != nil {
		t.Errorf("body only: %v", err)
	}
	if err := checkMapping(ImportCSVMapping{Subject: "Subject"}, header); err != nil {
		t.Errorf("subject only: %v", err)
	}
	if err := checkMapping(ImportCSVMapping{FromEmail: "Email"}, header); err == nil || !strings.Contains(err.Error(), "subject") {
		t.Errorf("nothing to import: %v", err)
	}
	if err := checkMapping(ImportCSVMapping{Body: "Body", Status: "State"}, header); err == nil || !strings.Contains(err.Error(), `"State"`) {
		t.Errorf("missing column: %v", err)
	}
	if err := checkMapping(ImportCSVMapping{Body: "body"}, header); err == nil {
		t.Error("column names are exact")
	}
}

func TestCSVSourceGroupsRows(t *testing.T) {
	raw := strings.Join([]string{
		"Ticket ID,Subject,Body,From email,From name,Author type,Status,Priority,Tags,Created at,Assignee email,Team,Company",
		"T-1,Login broken,\"<p>Thanks, looking.</p>\",agent@acme.test,Ann Agent,agent,,,,2024-03-01 10:05:00,agent@acme.test,Support,",
		"T-1,,I cannot log in,bob@customer.test,Bob,customer,pending,high,\"vip, login\",2024-03-01 10:00:00,,,Bobco",
		"T-2,Invoice,Where is my invoice?,carol@customer.test,Carol,,,urgent,billing;vip,1709290800,,,",
		"T-1,,Checked the logs,agent@acme.test,Ann Agent,internal note,solved,,login|sso,2024-03-01 10:10:00,,,",
		",,,,,,,,,,,,",
		",No ticket id,Lone row,dave@customer.test,,,,,,,,,",
		"T-3,,,eve@customer.test,,,,,,,,,",
	}, "\n")
	f := mustParse(t, []byte(raw))
	src := newCSVSource(f, suggestMapping(f.Header), "export.csv", "csv:abc")

	if len(src.tickets) != 3 {
		t.Fatalf("tickets %d: %+v", len(src.tickets), src.tickets)
	}
	t1 := src.tickets[0]
	if t1.ID != "T-1" || t1.Subject != "Login broken" || t1.Status != "solved" || t1.Priority != "high" {
		t.Fatalf("t1 %+v", t1)
	}
	if !slices.Equal(t1.Tags, []string{"vip", "login", "sso"}) {
		t.Errorf("t1 tags %q", t1.Tags)
	}
	if len(t1.Messages) != 3 {
		t.Fatalf("t1 messages %d", len(t1.Messages))
	}
	// In time order, not file order.
	if t1.Messages[0].Text != "I cannot log in" || t1.Messages[0].Agent {
		t.Errorf("first message %+v", t1.Messages[0])
	}
	if t1.Messages[1].HTML != "<p>Thanks, looking.</p>" || !t1.Messages[1].Agent || t1.Messages[1].Note || t1.Messages[1].AuthorID != "agent@acme.test" {
		t.Errorf("second message %+v", t1.Messages[1])
	}
	if !t1.Messages[2].Note || !t1.Messages[2].Agent {
		t.Errorf("third message %+v", t1.Messages[2])
	}
	if want := time.Date(2024, 3, 1, 10, 0, 0, 0, time.UTC); !t1.CreatedAt.Equal(want) {
		t.Errorf("t1 created %v", t1.CreatedAt)
	}
	if want := time.Date(2024, 3, 1, 10, 10, 0, 0, time.UTC); !t1.UpdatedAt.Equal(want) {
		t.Errorf("t1 updated %v", t1.UpdatedAt)
	}
	if t1.Requester == nil || t1.Requester.Email != "bob@customer.test" || t1.Requester.Company != "Bobco" {
		t.Errorf("t1 requester %+v", t1.Requester)
	}
	if t1.AssigneeID != "agent@acme.test" || t1.GroupID != "Support" {
		t.Errorf("t1 assignee %q group %q", t1.AssigneeID, t1.GroupID)
	}
	ids := map[string]bool{}
	for _, m := range t1.Messages {
		ids[m.ID] = true
	}
	if len(ids) != 3 {
		t.Errorf("message ids not unique: %v", ids)
	}

	t2 := src.tickets[1]
	if t2.Status != "closed" || t2.Priority != "urgent" || !t2.CreatedAt.Equal(time.Unix(1709290800, 0)) {
		t.Errorf("t2 %+v", t2)
	}
	if !slices.Equal(t2.Tags, []string{"billing", "vip"}) || t2.Requester == nil || t2.Requester.Email != "carol@customer.test" {
		t.Errorf("t2 tags %q requester %+v", t2.Tags, t2.Requester)
	}
	// A row without a ticket id is a ticket of its own.
	t3 := src.tickets[2]
	if t3.ID != "row-7" || t3.Subject != "No ticket id" || len(t3.Messages) != 1 {
		t.Errorf("t3 %+v", t3)
	}
	// The blank row is skipped by the parser; the row with nothing to import is a problem.
	if len(src.problems) != 1 || src.problems[0].Item != "Row 8" {
		t.Errorf("problems %+v", src.problems)
	}

	scan, err := src.Check(t.Context())
	if err != nil || scan.Tickets != 3 || scan.Account.Key != "csv:abc" || scan.HasArticles {
		t.Fatalf("scan %+v %v", scan, err)
	}
	if len(scan.Agents) != 1 || scan.Agents[0].ID != "agent@acme.test" || scan.Agents[0].Name != "Ann Agent" {
		t.Errorf("agents %+v", scan.Agents)
	}
	if len(scan.Groups) != 1 || scan.Groups[0].Name != "Support" {
		t.Errorf("groups %+v", scan.Groups)
	}
	since := time.Date(2024, 3, 1, 10, 30, 0, 0, time.UTC) // T-1 is older; row-7 has no date and stays
	page, err := src.Tickets(t.Context(), "", &since, nil)
	if err != nil || len(page.Items) != 2 || page.Next != "" {
		t.Fatalf("since page %d %q %v", len(page.Items), page.Next, err)
	}
	if _, err := src.Articles(t.Context(), ""); !errors.Is(err, source.ErrUnsupported) {
		t.Errorf("articles %v", err)
	}
}

func TestCSVSourcePages(t *testing.T) {
	var b strings.Builder
	b.WriteString("Subject\n")
	for i := range csvPageSize + 5 {
		fmt.Fprintf(&b, "Ticket %d\n", i)
	}
	f := mustParse(t, []byte(b.String()))
	src := newCSVSource(f, ImportCSVMapping{Subject: "Subject"}, "x.csv", "csv:x")
	p1, _ := src.Tickets(t.Context(), "", nil, nil)
	if len(p1.Items) != csvPageSize || p1.Next == "" {
		t.Fatalf("page 1: %d next %q", len(p1.Items), p1.Next)
	}
	p2, _ := src.Tickets(t.Context(), p1.Next, nil, nil)
	if len(p2.Items) != 5 || p2.Next != "" || p2.Items[4].Subject != fmt.Sprintf("Ticket %d", csvPageSize+4) {
		t.Fatalf("page 2: %d next %q", len(p2.Items), p2.Next)
	}
	// A message without a body takes the subject as its text.
	if p1.Items[0].Messages[0].Text != "Ticket 0" {
		t.Errorf("subject as body %+v", p1.Items[0].Messages[0])
	}
}

func TestCSVStatusPriority(t *testing.T) {
	for in, want := range map[string]string{"Open": "open", "NEW": "open", "In progress": "open", "Pending": "pending",
		"Waiting on Customer": "pending", "On-hold": "on_hold", "snoozed": "on_hold", "Resolved": "solved", "solved": "solved",
		"Closed": "closed", "archived": "closed", "": "closed"} {
		if got := csvStatus(in); got != want {
			t.Errorf("status %q = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"Low": "low", "P4": "low", "Medium": "normal", "normal": "normal", "High": "high",
		"p2": "high", "Urgent": "urgent", "Critical": "urgent", "1": "low", "2": "normal", "3": "high", "4": "urgent", "?": "normal"} {
		if got := csvPriority(in); got != want {
			t.Errorf("priority %q = %q, want %q", in, got, want)
		}
	}
}

func TestParseWhen(t *testing.T) {
	at := time.Date(2024, 3, 5, 14, 30, 0, 0, time.UTC)
	day := time.Date(2024, 3, 5, 0, 0, 0, 0, time.UTC)
	for in, want := range map[string]time.Time{
		"2024-03-05T14:30:00Z":            at,
		"2024-03-05T16:30:00+02:00":       at,
		"2024-03-05T14:30:00.000Z":        at,
		"2024-03-05T14:30:00":             at,
		"2024-03-05 14:30:00":             at,
		"2024-03-05 14:30":                at,
		"2024-03-05 15:30:00 +0100":       at,
		"2024-03-05 14:30:00 UTC":         at,
		"2024/03/05 14:30":                at,
		"03/05/2024 14:30":                at,
		"03/05/2024 2:30 PM":              at,
		"3/5/2024 2:30 PM":                at,
		"05.03.2024 14:30":                at,
		"Mar 5, 2024 2:30 PM":             at,
		"5 Mar 2024 14:30":                at,
		"Tue, 05 Mar 2024 14:30:00 +0000": at,
		"2024-03-05":                      day,
		"  2024-03-05  ":                  day,
		"1709649000":                      at, // unix seconds
		"1709649000000":                   at, // unix milliseconds
	} {
		if got := parseWhen(in); !got.Equal(want) {
			t.Errorf("parseWhen(%q) = %v, want %v", in, got, want)
		}
	}
	for _, in := range []string{"", "yesterday", "42", "1899-01-01", "2999-01-01", "31/31/2024", "99999999999999999"} {
		if got := parseWhen(in); !got.IsZero() {
			t.Errorf("parseWhen(%q) = %v, want zero", in, got)
		}
	}
	if got := parseWhen("2024-03-05 14:30:00 +0100"); got.Location() != time.UTC {
		t.Errorf("not utc: %v", got.Location())
	}
}

func TestAuthorKind(t *testing.T) {
	for in, want := range map[string]string{"agent": "agent", "Agent": "agent", "STAFF": "agent", "outbound": "agent",
		"public_reply": "agent", "true": "agent", "1": "agent", "note": "note", "Internal Note": "note", "private": "note",
		"customer": "customer", "end-user": "customer", "": "customer", "false": "customer", "requester": "customer"} {
		if got := authorKind(in); got != want {
			t.Errorf("authorKind(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFirstWords(t *testing.T) {
	if got := firstWords("  hello\n  world  ", 80); got != "hello world" {
		t.Errorf("short %q", got)
	}
	got := firstWords(strings.Repeat("word ", 40), 30)
	if !strings.HasSuffix(got, "…") || len([]rune(got)) > 31 || strings.Contains(got, "wor…") {
		t.Errorf("cut %q", got)
	}
}

func TestSourceError(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
		detail string
	}{
		{source.Invalid("subdomain", "Enter your account's address."), 422, "validation_failed", "Enter your account's address."},
		{source.ErrAuth, 422, "import_auth_failed", "Zendesk refused these credentials. Check them and try again."},
		{fmt.Errorf("%w: sign in as a Zendesk admin", source.ErrAuth), 422, "import_auth_failed",
			"Zendesk refused these credentials: sign in as a Zendesk admin."},
		{source.ErrNotFound, 422, "import_not_found", "No Zendesk account answers at that address. Check it and try again."},
		{&source.RateLimited{RetryAfter: 90 * time.Second}, 429, "import_rate_limited", "Zendesk asked us to slow down. Try again in 90 seconds."},
		{&source.RateLimited{RetryAfter: 10 * time.Millisecond}, 429, "import_rate_limited", ""},
		{&source.Unreachable{Err: errors.New("dial tcp: timeout")}, 502, "import_unreachable", ""},
		{fmt.Errorf("page 2: %w", &source.RemoteError{Status: 500}), 502, "import_unreachable", ""},
		{errors.New("boom"), 500, "", ""},
	}
	for _, c := range cases {
		err := sourceError(Zendesk, c.err)
		var he *httpx.Error
		var se interface{ GetStatus() int }
		switch {
		case errors.As(err, &he):
			if he.Status != c.status || (c.code != "" && he.Code != c.code) || (c.detail != "" && he.Detail != c.detail) {
				t.Errorf("%v: got %d %s %q", c.err, he.Status, he.Code, he.Detail)
			}
		case errors.As(err, &se):
			if se.GetStatus() != c.status || c.code != "" {
				t.Errorf("%v: got status %d", c.err, se.GetStatus())
			}
		default:
			t.Errorf("%v: not an http error: %T", c.err, err)
		}
	}
	err := sourceError(HelpScout, source.Invalid("app_id", "Enter the app ID."))
	var he *httpx.Error
	if !errors.As(err, &he) || len(he.Errors) != 1 || he.Errors[0].Location != "body.credentials.app_id" {
		t.Errorf("invalid field location: %+v", err)
	}
	if !errors.As(sourceError(HelpScout, source.ErrAuth), &he) || !strings.HasPrefix(he.Detail, "Help Scout ") {
		t.Errorf("source name: %+v", he)
	}
}
