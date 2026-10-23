package supportchannels

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/ai"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/jobs"
	"github.com/useopendesk/opendesk/internal/supportchannels/channel"
	"github.com/useopendesk/opendesk/internal/supportchannels/providers/telegram"
)

// fakeAdapter records what the engine sends a provider with native controls
// and private answers.
type fakeAdapter struct {
	posts       []string
	interactive []channel.Message
	cleared     []string
	responses   []string
}

func (*fakeAdapter) Describe() channel.Descriptor {
	return channel.Descriptor{Key: "fake", Name: "Fake"}
}
func (*fakeAdapter) Validate(context.Context, *http.Client, map[string]string) (channel.Account, error) {
	return channel.Account{}, nil
}
func (*fakeAdapter) Verify(http.Header, []byte, map[string]string) error { return nil }
func (*fakeAdapter) Parse(http.Header, []byte) (channel.WebhookResult, error) {
	return channel.WebhookResult{}, nil
}
func (*fakeAdapter) CreateThread(context.Context, *http.Client, map[string]string, string, channel.Ticket) (channel.Thread, error) {
	return channel.Thread{}, errors.New("not used")
}
func (f *fakeAdapter) Post(_ context.Context, _ *http.Client, _ map[string]string, _, text string) error {
	f.posts = append(f.posts, text)
	return nil
}
func (*fakeAdapter) Update(context.Context, *http.Client, map[string]string, string, channel.Ticket) error {
	return nil
}
func (f *fakeAdapter) PostInteractive(_ context.Context, _ *http.Client, _ map[string]string, _ string, m channel.Message) (channel.MessageRef, error) {
	f.interactive = append(f.interactive, m)
	return channel.MessageRef{ID: "preview"}, nil
}
func (f *fakeAdapter) ClearActions(_ context.Context, _ *http.Client, _ map[string]string, in channel.Inbound) error {
	f.cleared = append(f.cleared, in.SourceMessageID)
	return nil
}
func (f *fakeAdapter) Respond(_ context.Context, _ *http.Client, _ map[string]string, _, text string) error {
	f.responses = append(f.responses, text)
	return nil
}

type workflow struct {
	e      *hdtest.Env
	ws     hdtest.WS
	s      *Service
	fake   *fakeAdapter
	c      connection
	ticket domain.Ticket
}

func newWorkflow(t *testing.T) *workflow {
	e := hdtest.New(t)
	ws := e.NewWorkspace()
	var ticket domain.Ticket
	if err := e.D.DB.Tx(e.Ctx, func(tx pgx.Tx) error {
		var err error
		ticket, err = domain.CreateTicket(e.Ctx, tx, domain.NewTicket{WorkspaceID: ws.ID, Subject: "Printer", Channel: domain.ChannelEmail})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	fake := &fakeAdapter{}
	registry, err := NewRegistry(fake)
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{d: e.D, registry: registry, http: http.DefaultClient, ai: ai.For(e.D)}
	creds, err := e.D.Box.SealJSON(map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	c := connection{ID: ids.New(), WorkspaceID: ws.ID, Provider: "fake", ExternalAccountID: "T1"}
	destinationID := ids.New()
	e.Exec(`INSERT INTO support_channel_connections(id,workspace_id,provider,name,external_account_id,credentials_enc)
		VALUES($1,$2,'fake','Support','T1',$3)`, c.ID, ws.ID, creds)
	e.Exec(`INSERT INTO support_channel_destinations(id,workspace_id,connection_id,external_channel_id)
		VALUES($1,$2,$3,'C1')`, destinationID, ws.ID, c.ID)
	e.Exec(`INSERT INTO support_channel_threads(id,workspace_id,connection_id,destination_id,ticket_id,external_channel_id,external_thread_id,history_posted_at)
		VALUES($1,$2,$3,$4,$5,'C1','10.1',now())`, ids.New(), ws.ID, c.ID, destinationID, ticket.ID)
	for external, user := range map[string]uuid.UUID{"U1": ws.Agent.ID, "U2": ws.Admin.ID} {
		e.Exec(`INSERT INTO support_channel_identities(id,workspace_id,connection_id,external_user_id,user_id) VALUES($1,$2,$3,$4,$5)`,
			ids.New(), ws.ID, c.ID, external, user)
	}
	return &workflow{e: e, ws: ws, s: s, fake: fake, c: c, ticket: ticket}
}

var eventCounter int

// run persists one inbound action as a signed webhook would and processes it.
func (w *workflow) run(t *testing.T, in channel.Inbound) {
	t.Helper()
	eventCounter++
	in.EventID = fmt.Sprintf("E%d", eventCounter)
	in.ExternalChannelID, in.ExternalThreadID = "C1", "10.1"
	if err := w.s.persistInbound(w.e.Ctx, w.c, []channel.Inbound{in}); err != nil {
		t.Fatal(err)
	}
	if err := w.s.processInbound(w.e.Ctx, jobs.SupportChannelInboundArgs{ConnectionID: w.c.ID, ExternalEventID: in.EventID}); err != nil {
		t.Fatal(err)
	}
}

func (w *workflow) status(t *testing.T) (string, string) {
	t.Helper()
	var status, priority string
	w.e.Scalar(`SELECT status FROM tickets WHERE id=$1`, &status, w.ticket.ID)
	w.e.Scalar(`SELECT priority FROM tickets WHERE id=$1`, &priority, w.ticket.ID)
	return status, priority
}

func TestCardCloseAsksAndOnlyItsOwnConfirmApplies(t *testing.T) {
	w := newWorkflow(t)
	card := channel.Inbound{ExternalUserID: "U1", Command: "status", Value: "closed", SourceMessageID: "card", Respond: "callback"}
	w.run(t, card)
	if len(w.fake.interactive) != 1 || len(w.fake.interactive[0].Actions) != 2 {
		t.Fatalf("preview: %+v", w.fake.interactive)
	}
	nonce := w.fake.interactive[0].Actions[0].Value
	if nonce == "" || w.fake.interactive[0].Actions[1].Value != nonce {
		t.Fatalf("controls are not bound to the preview: %+v", w.fake.interactive[0].Actions)
	}
	if status, _ := w.status(t); status == domain.StatusClosed {
		t.Fatal("the card closed the ticket without a confirmation")
	}
	if !slices.Equal(w.fake.responses, []string{""}) {
		t.Fatalf("the control was not acknowledged once: %q", w.fake.responses)
	}

	// A second preview waits for the first.
	w.fake.responses = nil
	w.run(t, card)
	if len(w.fake.interactive) != 1 || len(w.fake.responses) != 1 || !strings.Contains(w.fake.responses[0], "waiting") {
		t.Fatalf("second preview: %+v %q", w.fake.interactive, w.fake.responses)
	}

	// Someone else's click leaves the buttons for the person who asked.
	w.fake.responses = nil
	w.run(t, channel.Inbound{ExternalUserID: "U2", Command: "confirm", Text: "confirm", Value: nonce, SourceMessageID: "preview", Respond: "callback"})
	if len(w.fake.cleared) != 0 || len(w.fake.responses) != 1 || !strings.Contains(w.fake.responses[0], "Only the person") {
		t.Fatalf("other person: cleared=%q responses=%q", w.fake.cleared, w.fake.responses)
	}

	// A stale button applies nothing, says so privately and loses its buttons.
	w.fake.responses = nil
	w.run(t, channel.Inbound{ExternalUserID: "U1", Command: "confirm", Text: "confirm", Value: "stale", SourceMessageID: "old", Respond: "callback"})
	if status, _ := w.status(t); status == domain.StatusClosed {
		t.Fatal("a stale confirm applied the waiting change")
	}
	if !slices.Equal(w.fake.cleared, []string{"old"}) || len(w.fake.responses) != 1 || !strings.Contains(w.fake.responses[0], "isn't waiting") {
		t.Fatalf("stale: cleared=%q responses=%q", w.fake.cleared, w.fake.responses)
	}

	w.fake.posts, w.fake.cleared = nil, nil
	w.run(t, channel.Inbound{ExternalUserID: "U1", Command: "confirm", Text: "confirm", Value: nonce, SourceMessageID: "preview", Respond: "callback"})
	if status, _ := w.status(t); status != domain.StatusClosed {
		t.Fatalf("status = %s after confirming", status)
	}
	if !slices.Equal(w.fake.cleared, []string{"preview"}) || len(w.fake.posts) != 1 || !strings.Contains(w.fake.posts[0], "closed") {
		t.Fatalf("confirm: cleared=%q posts=%q", w.fake.cleared, w.fake.posts)
	}

	// The same button again finds nothing waiting.
	w.fake.responses = nil
	w.run(t, channel.Inbound{ExternalUserID: "U1", Command: "confirm", Text: "confirm", Value: nonce, SourceMessageID: "preview", Respond: "callback"})
	if len(w.fake.responses) != 1 || !strings.Contains(w.fake.responses[0], "isn't waiting") {
		t.Fatalf("second confirm: %q", w.fake.responses)
	}
}

func TestCardChangesAnswerWithTheCardAndTypedCommandsRunAtOnce(t *testing.T) {
	w := newWorkflow(t)
	w.run(t, channel.Inbound{ExternalUserID: "U1", Command: "priority", Value: "urgent", Text: "urgent", SourceMessageID: "card", Respond: "callback"})
	if _, priority := w.status(t); priority != "urgent" {
		t.Fatalf("priority = %s", priority)
	}
	if len(w.fake.posts) != 0 {
		t.Fatalf("a card change posted %q", w.fake.posts)
	}

	// Typed, closing is deliberate: no preview.
	w.run(t, channel.Inbound{ExternalUserID: "U1", Command: "status", Value: "closed", Text: "closed"})
	if status, _ := w.status(t); status != domain.StatusClosed {
		t.Fatalf("typed close: status = %s", status)
	}
	if len(w.fake.interactive) != 0 || !slices.Equal(w.fake.posts, []string{"Done in opendesk."}) {
		t.Fatalf("typed close: interactive=%+v posts=%q", w.fake.interactive, w.fake.posts)
	}

	// Errors and unlinked senders are answered privately.
	w.fake.posts = nil
	w.run(t, channel.Inbound{ExternalUserID: "U-nobody", Command: "priority", Value: "low", SourceMessageID: "card", Respond: "callback"})
	if len(w.fake.posts) != 0 || len(w.fake.responses) != 2 || !strings.Contains(w.fake.responses[1], "Link your opendesk account") {
		t.Fatalf("unlinked: posts=%q responses=%q", w.fake.posts, w.fake.responses)
	}
}

func TestTypedReplyIsNotPreviewed(t *testing.T) {
	w := newWorkflow(t)
	w.run(t, channel.Inbound{ExternalUserID: "U1", Command: "reply", Text: "On it", Value: "On it"})
	if len(w.fake.interactive) != 0 {
		t.Fatalf("a typed reply was previewed: %+v", w.fake.interactive)
	}
	var pending bool
	w.e.Scalar(`SELECT EXISTS(SELECT 1 FROM support_channel_conversations WHERE connection_id=$1 AND pending_action IS NOT NULL)`, &pending, w.c.ID)
	if pending {
		t.Fatal("a typed reply waits for confirmation")
	}
}

func TestAnEventWhoseProviderIsGoneIsFinished(t *testing.T) {
	w := newWorkflow(t)
	w.e.Exec(`UPDATE support_channel_connections SET provider='retired' WHERE id=$1`, w.c.ID)
	w.c.Provider = "retired"
	w.run(t, channel.Inbound{ExternalUserID: "U1", Command: "priority", Value: "high"})
	var unfinished int
	w.e.Scalar(`SELECT count(*) FROM support_channel_inbound_events WHERE connection_id=$1 AND processed_at IS NULL`, &unfinished, w.c.ID)
	var next int64
	w.e.Scalar(`SELECT next_process_seq FROM support_channel_ticket_lanes WHERE ticket_id=$1`, &next, w.ticket.ID)
	if unfinished != 0 || next != 2 {
		t.Fatalf("unfinished = %d, next = %d", unfinished, next)
	}
}

func TestTicketLaneOrdersCommandsAndHealsPastLostOnes(t *testing.T) {
	w := newWorkflow(t)
	s, e := w.s, w.e
	var items []channel.Inbound
	for i := 1; i <= 5; i++ {
		items = append(items, channel.Inbound{EventID: "L" + strings.Repeat("x", i), ExternalChannelID: "C1", ExternalThreadID: "10.1", ExternalUserID: "U1", Command: "priority", Value: "high"})
	}
	if err := s.persistInbound(e.Ctx, w.c, items); err != nil {
		t.Fatal(err)
	}
	var first, last int64
	e.Scalar(`SELECT ticket_command_seq FROM support_channel_inbound_events WHERE connection_id=$1 AND external_event_id='Lx'`, &first, w.c.ID)
	e.Scalar(`SELECT ticket_command_seq FROM support_channel_inbound_events WHERE connection_id=$1 AND external_event_id='Lxxxxx'`, &last, w.c.ID)
	if first != 1 || last != 5 {
		t.Fatalf("command sequences = %d..%d", first, last)
	}
	busy := func(seq int64) {
		t.Helper()
		if _, err := s.claimTicketCommand(e.Ctx, w.ticket.ID, seq); !errors.Is(err, errTicketCommandBusy) {
			t.Fatalf("claim %d = %v, want busy", seq, err)
		}
	}
	claim := func(seq int64) {
		t.Helper()
		claimed, err := s.claimTicketCommand(e.Ctx, w.ticket.ID, seq)
		if err != nil || !claimed {
			t.Fatalf("claim %d = %v, %v", seq, claimed, err)
		}
	}
	busy(2)
	claim(1)
	busy(1) // its own lease is live
	if err := s.finishInbound(e.Ctx, jobs.SupportChannelInboundArgs{ConnectionID: w.c.ID, ExternalEventID: "Lx"}); err != nil {
		t.Fatal(err)
	}
	// 2 was lost with its connection's events, 3 waited too long (River gave up):
	// neither holds 4 back. 4 still waits for nothing else.
	e.Exec(`DELETE FROM support_channel_inbound_events WHERE connection_id=$1 AND external_event_id='Lxx'`, w.c.ID)
	e.Exec(`UPDATE support_channel_inbound_events SET received_at=now()-interval '1 hour' WHERE connection_id=$1 AND external_event_id='Lxxx'`, w.c.ID)
	claim(4)
	busy(5)
	// The abandoned 3 still runs if River retries it, without the lane.
	if claimed, err := s.claimTicketCommand(e.Ctx, w.ticket.ID, 3); err != nil || claimed {
		t.Fatalf("passed-over claim = %v, %v", claimed, err)
	}
	if err := s.finishInbound(e.Ctx, jobs.SupportChannelInboundArgs{ConnectionID: w.c.ID, ExternalEventID: "Lxxx"}); err != nil {
		t.Fatal(err)
	}
	busy(5) // finishing 3 left 4's lease alone
	if err := s.finishInbound(e.Ctx, jobs.SupportChannelInboundArgs{ConnectionID: w.c.ID, ExternalEventID: "Lxxxx"}); err != nil {
		t.Fatal(err)
	}
	claim(5)
}

func TestRecentHistoryLeavesTheNewestMessagesToTheirOwnEvents(t *testing.T) {
	w := newWorkflow(t)
	e := w.e
	now := time.Now().UTC()
	add := func(body string, at time.Time) domain.Message {
		var m domain.Message
		if err := e.D.DB.Tx(e.Ctx, func(tx pgx.Tx) error {
			var err error
			m, err = domain.AddMessage(e.Ctx, tx, e.D.Jobs, domain.NewMessage{WorkspaceID: w.ws.ID, TicketID: w.ticket.ID, Kind: "reply", Direction: "inbound",
				AuthorType: "contact", BodyHTML: "<p>" + body + "</p>", BodyText: body, CreatedAt: at})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return m
	}
	old := add("The printer is offline", now.Add(-time.Hour))
	latest := add(strings.Repeat("Full first message. ", 40), now)
	trigger, _ := json.Marshal(map[string]any{"message": latest})
	before := historyCutoff("message.created", trigger, now.Add(time.Second)).Add(-historySettle)
	text, posted, err := w.s.recentHistory(e.Ctx, w.ws.ID, w.ticket.ID, false, before)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "The printer is offline") || strings.Contains(text, "Full first message") || !slices.Equal(posted, []uuid.UUID{old.ID}) {
		t.Fatalf("history = %q, ids = %v", text, posted)
	}
	e.Exec(`UPDATE support_channel_threads SET history_message_ids=$2 WHERE ticket_id=$1`, w.ticket.ID, posted)
	var destinationID uuid.UUID
	e.Scalar(`SELECT destination_id FROM support_channel_threads WHERE ticket_id=$1`, &destinationID, w.ticket.ID)
	for m, want := range map[*domain.Message]bool{&old: true, &latest: false} {
		data, _ := json.Marshal(map[string]any{"message": m})
		got, err := w.s.inHistory(e.Ctx, destinationID, w.ticket.ID, data)
		if err != nil || got != want {
			t.Fatalf("inHistory(%s) = %v, %v", m.BodyText[:10], got, err)
		}
	}
}

func TestSlackAppsShowSetupUntilAControlArrives(t *testing.T) {
	w := newWorkflow(t)
	e := w.e
	e.Exec(`UPDATE support_channel_connections SET health_status='action_required',health_detail=$2 WHERE id=$1`, w.c.ID, awaitingControls)
	c, err := w.s.loadConnection(e.Ctx, w.c.ID)
	if err != nil {
		t.Fatal(err)
	}
	w.s.markWebhookHealthy(e.Ctx, c, false)
	var health string
	e.Scalar(`SELECT health_status FROM support_channel_connections WHERE id=$1`, &health, w.c.ID)
	if health != "action_required" {
		t.Fatalf("an event callback cleared the interactivity step: %s", health)
	}
	w.s.markWebhookHealthy(e.Ctx, c, true)
	e.Scalar(`SELECT health_status FROM support_channel_connections WHERE id=$1`, &health, w.c.ID)
	if health != "healthy" {
		t.Fatalf("a control callback left health %s", health)
	}
}

func TestMarkedTelegramConnectionsSubscribeToControlsOnce(t *testing.T) {
	e := hdtest.New(t)
	ws := e.NewWorkspace()
	id := ids.New()
	webhook := "http://api.test/v1/support-channels/hooks/telegram/" + id.String()
	var updates []string
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_, _ = rw.Write([]byte(`{"ok":true,"result":{"id":77,"first_name":"Desk","username":"deskbot"}}`))
		case strings.HasSuffix(r.URL.Path, "/setWebhook"):
			calls++
			var in struct {
				AllowedUpdates []string `json:"allowed_updates"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			updates = in.AllowedUpdates
			_, _ = rw.Write([]byte(`{"ok":true,"result":true}`))
		case strings.HasSuffix(r.URL.Path, "/getWebhookInfo"):
			_, _ = rw.Write([]byte(`{"ok":true,"result":{"url":"` + webhook + `"}}`))
		default:
			http.NotFound(rw, r)
		}
	}))
	defer server.Close()
	registry, err := NewRegistry(telegram.Adapter{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{d: e.D, registry: registry, http: server.Client()}
	creds, _ := e.D.Box.SealJSON(map[string]string{"bot_token": "1:abc", "webhook_secret": "secret"})
	e.Exec(`INSERT INTO support_channel_connections(id,workspace_id,provider,name,external_account_id,credentials_enc,reconfigure)
		VALUES($1,$2,'telegram','Desk','77',$3,true)`, id, ws.ID, creds)
	if left := s.reconfigureMarked(e.Ctx); left != 0 {
		t.Fatalf("left = %d", left)
	}
	if calls != 1 || !slices.Contains(updates, "callback_query") {
		t.Fatalf("setWebhook calls = %d, updates = %v", calls, updates)
	}
	s.reconfigureMarked(e.Ctx)
	var marked bool
	e.Scalar(`SELECT reconfigure FROM support_channel_connections WHERE id=$1`, &marked, id)
	if calls != 1 || marked {
		t.Fatalf("reconfigured again: calls = %d, marked = %v", calls, marked)
	}
}

func TestConfirmationMessageCarriesOnlyItsNonce(t *testing.T) {
	message := confirmationChannelMessage("Review this reply", "n0nce")
	if len(message.Actions) != 2 || message.Actions[0].Command != "confirm" || message.Actions[1].Command != "cancel" {
		t.Fatalf("actions: %+v", message.Actions)
	}
	for _, action := range message.Actions {
		if action.Value != "n0nce" {
			t.Fatalf("action is not bound to its preview: %+v", action)
		}
	}
	if a, b := newNonce(), newNonce(); a == b || len(a) < 16 || strings.Contains(a, ":") {
		t.Fatalf("nonces %q %q", a, b)
	}
}
