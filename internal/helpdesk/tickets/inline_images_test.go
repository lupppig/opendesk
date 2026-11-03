package tickets_test

import (
	"html"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
	"github.com/useopendesk/opendesk/internal/helpdesk/tickets"
	"github.com/useopendesk/opendesk/internal/helpdesk/widget"
)

// TestInlineImagesAfterTheInstanceMoves: a message stored while the instance had another
// address (API_URL) shows its own inline images at the current address, signed again, and
// leaves other images as they were stored.
func TestInlineImagesAfterTheInstanceMoves(t *testing.T) {
	// Uploads are served by the widget area.
	e := hdtest.New(t, tickets.Register, widget.Register)
	ws := e.NewWorkspace()
	agent := ws.Agent.As(ws)
	tk := createTicket(t, e, ws, map[string]any{"kind": "note"})
	upload := func(name string) domain.Attachment {
		var a domain.Attachment
		e.Upload("/v1/attachments", agent, name, []byte("\x89PNG "+name)).Want(t, 201).JSON(t, &a)
		return a
	}
	own, other := upload("logo.png"), upload("chart.png")
	// The other file is another message's.
	e.Do("POST", "/v1/tickets/"+tk.ID.String()+"/messages", agent, map[string]any{"kind": "note", "body_html": "<p>Chart.</p>",
		"attachment_ids": []string{other.ID.String()}}).Want(t, 201)

	// The body as an import or inbound email stored it, when the instance was at old-desk.
	oldLink := func(id uuid.UUID) string {
		return "https://old-desk.example.com/v1/attachments/" + id.String() + "?exp=1&amp;sig=00"
	}
	foreign := `<img src="https://cdn.example.org/logo.png">`
	otherImg := `<img src="` + oldLink(other.ID) + `">`
	body := `<p>Hi</p><img src="` + oldLink(own.ID) + `">` + otherImg + foreign
	if err := e.D.DB.Tx(e.Ctx, func(tx pgx.Tx) error {
		m, err := domain.AddMessage(e.Ctx, tx, e.D.Jobs, domain.NewMessage{WorkspaceID: ws.ID, TicketID: tk.ID, Kind: "reply", Direction: "inbound",
			AuthorType: "contact", BodyHTML: body, BodyText: "Hi", FromEmail: "carla@customer.test", FromName: "Carla"})
		if err != nil {
			return err
		}
		_, err = tx.Exec(e.Ctx, `UPDATE attachments SET message_id = $2 WHERE id = $1`, own.ID, m.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	var msgs struct{ Data []domain.Message }
	e.Do("GET", "/v1/tickets/"+tk.ID.String()+"/messages", agent, nil).Want(t, 200).JSON(t, &msgs)
	var got string
	for _, m := range msgs.Data {
		if m.Direction == "inbound" {
			got = m.BodyHTML
		}
	}
	src := regexp.MustCompile(`<img src="(http://api\.test/v1/attachments/` + own.ID.String() + `\?[^"]+)">`).FindStringSubmatch(got)
	if src == nil {
		t.Fatalf("own inline image not at the current address: %s", got)
	}
	link, err := url.Parse(html.UnescapeString(src[1]))
	if err != nil || !domain.VerifyAttachmentSig(own.ID, link.Query().Get("exp"), link.Query().Get("sig"), false) {
		t.Fatalf("own inline image not signed again: %s (%v)", src[1], err)
	}
	if r := e.Do("GET", link.RequestURI(), hdtest.Auth{}, nil); r.Status != 200 && r.Status != 302 {
		t.Fatalf("the fresh link does not open the file: %d", r.Status)
	}
	// Another message's file and another site's image are left as they were stored.
	if !strings.Contains(got, otherImg) || !strings.Contains(got, foreign) || strings.Count(got, "old-desk.example.com") != 1 {
		t.Fatalf("rewrote an image that is not the message's own: %s", got)
	}
}
