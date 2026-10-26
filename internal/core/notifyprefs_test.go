package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/jobs"
	"github.com/useopendesk/opendesk/internal/notifyprefs"
)

// notifyFixture is an owner with a confirmed address, a workspace and a teammate (Maya).
type notifyFixture struct {
	e     *testEnv
	c     *client
	email string
	uid   uuid.UUID
	wid   uuid.UUID
	maya  uuid.UUID
	brand uuid.UUID
}

func newNotifyFixture(t *testing.T, tag string) *notifyFixture {
	e := newEnv(t)
	c, email := e.signup(tag)
	f := &notifyFixture{e: e, c: c, email: email, uid: e.userID(email)}
	f.wid = uuid.MustParse(c.newWorkspace("Margin " + tag))
	f.exec(`UPDATE users SET email_verified_at = now() WHERE id = $1`, f.uid)
	f.exec(`DELETE FROM river_job WHERE args->>'to' = $1`, email) // the sign-up's confirmation email
	_, f.maya = e.memberOf(f.wid, tag+"maya", "agent")
	f.exec(`UPDATE users SET name = 'Maya Chen' WHERE id = $1`, f.maya)
	if err := e.d.DB.Pool.QueryRow(context.Background(), `SELECT id FROM brands WHERE workspace_id = $1 AND is_default`, f.wid).Scan(&f.brand); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *notifyFixture) exec(sql string, args ...any) {
	f.e.t.Helper()
	if _, err := f.e.d.DB.Pool.Exec(context.Background(), sql, args...); err != nil {
		f.e.t.Fatalf("%s: %v", sql, err)
	}
}

func (f *notifyFixture) count(sql string, args ...any) int {
	f.e.t.Helper()
	var n int
	if err := f.e.d.DB.Pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		f.e.t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// ticket inserts a ticket; team and assignee may be nil.
func (f *notifyFixture) ticket(subject string, team, assignee *uuid.UUID) uuid.UUID {
	id := ids.New()
	f.exec(`INSERT INTO tickets (id, workspace_id, number, subject, channel, team_id, assignee_id, brand_id, last_message_at)
		VALUES ($1, $2, (SELECT COALESCE(max(number), 0) + 1 FROM tickets WHERE workspace_id = $2), $3, 'api', $4, $5, $6, now())`,
		id, f.wid, subject, team, assignee, f.brand)
	return id
}

// notify runs hd.Notify for the owner, as Maya.
func (f *notifyFixture) notify(typ string, ticket *uuid.UUID, data map[string]any) uuid.UUID {
	f.e.t.Helper()
	if data == nil {
		data = map[string]any{"by": f.maya.String()}
	}
	err := f.e.d.DB.Tx(context.Background(), func(tx pgx.Tx) error {
		return hd.Notify(context.Background(), tx, f.wid, f.uid, typ, ticket, data)
	})
	if err != nil {
		f.e.t.Fatal(err)
	}
	var id uuid.UUID
	if err := f.e.d.DB.Pool.QueryRow(context.Background(), `SELECT id FROM notifications WHERE user_id = $1 AND workspace_id = $2
		ORDER BY created_at DESC, id DESC LIMIT 1`, f.uid, f.wid).Scan(&id); err != nil {
		f.e.t.Fatal(err)
	}
	return id
}

// send runs the email job for the owner as if at now.
func (f *notifyFixture) send(now time.Time) {
	f.e.t.Helper()
	err := f.e.d.DB.Tx(context.Background(), func(tx pgx.Tx) error {
		return f.e.s.sendNotificationEmail(context.Background(), tx, f.wid, f.uid, now)
	})
	if err != nil {
		f.e.t.Fatal(err)
	}
}

type queuedMail struct {
	Subject string
	HTML    string
	Text    string
	Headers map[string]string
}

func (f *notifyFixture) mails() []queuedMail {
	f.e.t.Helper()
	rows, err := f.e.d.DB.Pool.Query(context.Background(), `SELECT args FROM river_job WHERE kind = 'mail.system' AND args->>'to' = $1 ORDER BY id`, f.email)
	if err != nil {
		f.e.t.Fatal(err)
	}
	raws, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
	if err != nil {
		f.e.t.Fatal(err)
	}
	var out []queuedMail
	for _, raw := range raws {
		var m queuedMail
		_ = json.Unmarshal(raw, &m)
		out = append(out, m)
	}
	return out
}

func later() time.Time { return time.Now().Add(notifyprefs.Current().Delay + time.Minute) }

// TestNotificationPreferencesAPI: defaults by role, per-workspace types, account-wide
// schedule, validation.
func TestNotificationPreferencesAPI(t *testing.T) {
	f := newNotifyFixture(t, "prefsapi")
	r := f.c.do("GET", "/v1/me/notification-preferences", nil)
	if r.Status != 200 {
		t.Fatalf("get: %d %s", r.Status, r.Raw)
	}
	if r.str("email_cadence") != "instant" || r.Body["digest_hour"] != float64(9) || r.Body["email_verified"] != true || r.Body["delay_minutes"] != float64(5) {
		t.Fatalf("defaults: %s", r.Raw)
	}
	types := map[string]map[string]any{}
	for _, v := range r.Body["types"].([]any) {
		m := v.(map[string]any)
		types[m["type"].(string)] = m
	}
	if len(types) != len(notifyprefs.Types) {
		t.Fatalf("owner types: %d", len(types))
	}
	if a := types["assigned"]; a["email"] != true || a["push"] != true || a["desktop"] != true {
		t.Fatalf("assigned defaults: %v", a)
	}
	if j := types["team_joined"]; j["email"] != false || j["push"] != false || j["desktop"] != true {
		t.Fatalf("team_joined defaults: %v", j)
	}
	if b := types["ai_balance_out"]; b["email"] != true || b["push"] != false || b["group"] != "ai_billing" {
		t.Fatalf("billing defaults: %v", b)
	}

	// An agent does not receive billing notifications, so cannot set them.
	agent, _ := f.e.memberOf(f.wid, "prefsagent", "agent")
	ar := agent.do("GET", "/v1/me/notification-preferences", nil)
	for _, v := range ar.Body["types"].([]any) {
		if v.(map[string]any)["group"] == "ai_billing" {
			t.Fatalf("agent sees billing types: %s", ar.Raw)
		}
	}
	if r := agent.do("PATCH", "/v1/me/notification-preferences", map[string]any{"types": []any{map[string]any{"type": "ai_balance_out", "email": false}}}); r.Status != 422 {
		t.Fatalf("agent billing type: %d", r.Status)
	}

	r = f.c.do("PATCH", "/v1/me/notification-preferences", map[string]any{"email_cadence": "daily", "digest_hour": 8, "timezone": "Europe/Budapest",
		"types": []any{map[string]any{"type": "customer_replied", "email": false}, map[string]any{"type": "team_joined", "push": true}}})
	if r.Status != 200 || r.str("email_cadence") != "daily" || r.str("timezone") != "Europe/Budapest" || r.Body["digest_hour"] != float64(8) {
		t.Fatalf("patch: %d %s", r.Status, r.Raw)
	}
	ch, err := notifyprefs.Channel(context.Background(), f.e.d.DB.Pool, f.wid, f.uid, "customer_replied")
	if err != nil || ch.Email || !ch.Push || !ch.Desktop {
		t.Fatalf("customer_replied channels: %+v %v", ch, err)
	}
	// Types are per workspace; the schedule is the account's.
	other := uuid.MustParse(f.c.newWorkspace("Second"))
	ch, _ = notifyprefs.Channel(context.Background(), f.e.d.DB.Pool, other, f.uid, "customer_replied")
	if !ch.Email {
		t.Fatal("a preference leaked into another workspace")
	}
	if r := f.c.do("GET", "/v1/me/notification-preferences", nil); r.str("email_cadence") != "daily" {
		t.Fatalf("cadence is account-wide: %s", r.Raw)
	}

	for _, bad := range []map[string]any{
		{"timezone": "Mars/Olympus"},
		{"types": []any{map[string]any{"type": "nope", "email": true}}},
		{"email_cadence": "weekly"},
		{"digest_hour": 24},
		{"paused_until": time.Now().Add(40 * 24 * time.Hour).Format(time.RFC3339)},
	} {
		if r := f.c.do("PATCH", "/v1/me/notification-preferences", bad); r.Status != 422 {
			t.Fatalf("%v: %d %s", bad, r.Status, r.Raw)
		}
	}

	until := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	if r := f.c.do("PATCH", "/v1/me/notification-preferences", map[string]any{"paused_until": until.Format(time.RFC3339)}); r.str("paused_until") == "" {
		t.Fatalf("pause: %s", r.Raw)
	}
	if r := f.c.do("PATCH", "/v1/me/notification-preferences", map[string]any{"paused_until": ""}); r.Body["paused_until"] != nil {
		t.Fatalf("resume: %s", r.Raw)
	}

	// Teams: the ones the caller leads or works in, on until turned off; an observer's or
	// another's membership is neither listed nor changed.
	f.c.workspace = f.wid.String()
	billing, legal, mayas := uuid.New(), uuid.New(), uuid.New()
	f.exec(`INSERT INTO teams (id, workspace_id, name) VALUES ($1, $4, 'Billing'), ($2, $4, 'Legal'), ($3, $4, 'Maya''s')`, billing, legal, mayas, f.wid)
	f.exec(`INSERT INTO team_members (team_id, user_id, role) VALUES ($1, $3, 'member'), ($2, $3, 'observer'), ($4, $5, 'member')`, billing, legal, f.uid, mayas, f.maya)
	r = f.c.do("GET", "/v1/me/notification-preferences", nil)
	if teams := r.Body["teams"].([]any); len(teams) != 1 || teams[0].(map[string]any)["name"] != "Billing" || teams[0].(map[string]any)["notify"] != true {
		t.Fatalf("teams: %s", r.Raw)
	}
	r = f.c.do("PATCH", "/v1/me/notification-preferences", map[string]any{"teams": []any{
		map[string]any{"id": billing, "notify": false}, map[string]any{"id": mayas, "notify": false}}})
	if teams := r.Body["teams"].([]any); r.Status != 200 || teams[0].(map[string]any)["notify"] != false {
		t.Fatalf("turn a team off: %d %s", r.Status, r.Raw)
	}
	if n := f.count(`SELECT count(*) FROM team_members WHERE NOT notify AND user_id = $1`, f.maya); n != 0 {
		t.Fatal("changed another member's team")
	}

	// The test email goes out whatever the preferences, to a confirmed address only.
	if r := f.c.do("POST", "/v1/me/notification-preferences/test", nil); r.Status != 204 {
		t.Fatalf("test email: %d %s", r.Status, r.Raw)
	}
	if m := f.mails(); len(m) != 1 || !strings.HasPrefix(m[0].Subject, "Test: ") || m[0].Headers["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" {
		t.Fatalf("test mail: %+v", m)
	}
	unverified, _ := f.e.memberOf(f.wid, "prefsunverified", "agent")
	if r := unverified.do("POST", "/v1/me/notification-preferences/test", nil); r.Status != 422 || r.str("code") != "email_unverified" {
		t.Fatalf("unverified test email: %d %s", r.Status, r.Raw)
	}
}

// TestNotificationEmailSingle: one notification waits, then gets its own email, once.
func TestNotificationEmailSingle(t *testing.T) {
	f := newNotifyFixture(t, "notifsingle")
	tid := f.ticket("SAML with Okta", nil, &f.uid)
	nid := f.notify(hd.NotifyAssigned, &tid, nil)
	if f.count(`SELECT count(*) FROM notifications WHERE id = $1 AND email_due_at BETWEEN now() + interval '4 minutes' AND now() + interval '6 minutes'`, nid) != 1 {
		t.Fatal("email not scheduled after the delay")
	}
	f.send(time.Now())
	if len(f.mails()) != 0 {
		t.Fatal("emailed before the delay")
	}
	f.send(later())
	m := f.mails()
	if len(m) != 1 {
		t.Fatalf("mails: %d", len(m))
	}
	if !strings.HasPrefix(m[0].Subject, "Maya Chen assigned you #") || !strings.HasSuffix(m[0].Subject, ": SAML with Okta") {
		t.Fatalf("subject %q", m[0].Subject)
	}
	if !strings.Contains(m[0].Text, "/inbox/mine/"+tid.String()+"?ws="+f.wid.String()) || !strings.Contains(m[0].Headers["References"], tid.String()) {
		t.Fatalf("link or threading: %s %v", m[0].Text, m[0].Headers)
	}
	if f.count(`SELECT count(*) FROM notifications WHERE id = $1 AND emailed_at IS NOT NULL`, nid) != 1 {
		t.Fatal("emailed_at not set")
	}
	// At most once: running again, even with the sweep, sends nothing new.
	f.send(later())
	if err := f.e.s.sweepNotificationEmails(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.mails()) != 1 {
		t.Fatal("emailed twice")
	}
}

// TestNotificationEmailReadFirst: a notification read in the app is never emailed.
func TestNotificationEmailReadFirst(t *testing.T) {
	f := newNotifyFixture(t, "notifread")
	tid := f.ticket("Refund", nil, &f.uid)
	nid := f.notify(hd.NotifyAssigned, &tid, nil)
	if r := f.c.do("POST", "/v1/notifications/read", map[string]any{"ids": []string{nid.String()}}); r.Status != 204 {
		t.Fatalf("read: %d", r.Status)
	}
	f.send(later())
	if len(f.mails()) != 0 {
		t.Fatal("emailed a read notification")
	}
}

// TestNotificationEmailBatch: several waiting notifications become one email, including
// one not due yet; the sweep queues one job per user and workspace.
func TestNotificationEmailBatch(t *testing.T) {
	f := newNotifyFixture(t, "notifbatch")
	t1 := f.ticket("SAML with Okta", nil, &f.uid)
	t2 := f.ticket("Export to CSV", nil, &f.uid)
	f.notify(hd.NotifyAssigned, &t1, nil)
	f.notify(hd.NotifyMentioned, &t1, map[string]any{"by": f.maya.String(), "preview": "<b>can you</b> check the metadata?"})
	f.notify(hd.NotifyCustomerReplied, &t2, map[string]any{"contact": map[string]any{"name": "Dana Lee"}, "preview": "Still missing"})
	f.exec(`UPDATE notifications SET email_due_at = now() - interval '1 minute' WHERE id = (SELECT id FROM notifications
		WHERE user_id = $1 ORDER BY created_at, id LIMIT 1)`, f.uid)
	if err := f.e.s.sweepNotificationEmails(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := f.e.s.sweepNotificationEmails(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := f.count(`SELECT count(*) FROM river_job WHERE kind = $1 AND args->>'user_id' = $2`, jobs.NotificationEmailArgs{}.Kind(), f.uid.String()); n != 1 {
		t.Fatalf("queued jobs: %d", n)
	}
	w := &notificationEmailWorker{s: f.e.s}
	if err := w.Work(context.Background(), &river.Job[jobs.NotificationEmailArgs]{JobRow: &rivertype.JobRow{},
		Args: jobs.NotificationEmailArgs{UserID: f.uid, WorkspaceID: f.wid}}); err != nil {
		t.Fatal(err)
	}
	m := f.mails()
	if len(m) != 1 || m[0].Subject != "Maya Chen mentioned you #1 and 2 more" {
		t.Fatalf("batch: %+v", m)
	}
	if strings.Contains(m[0].HTML, "<b>can you</b>") || !strings.Contains(m[0].HTML, "&lt;b&gt;can you&lt;/b&gt;") {
		t.Fatal("snippet not escaped")
	}
	if m[0].Headers["References"] != "" {
		t.Fatal("a batch about two tickets carries one ticket's thread")
	}
	if f.count(`SELECT count(*) FROM notifications WHERE user_id = $1 AND emailed_at IS NULL`, f.uid) != 0 {
		t.Fatal("not all marked emailed")
	}
}

// TestNotificationEmailRechecks: preferences, visibility, membership, pause and a confirmed
// address are checked again at send time.
func TestNotificationEmailRechecks(t *testing.T) {
	f := newNotifyFixture(t, "notifrecheck")

	// A ticket that moved to a private team the user is not on.
	team := ids.New()
	f.exec(`INSERT INTO teams (id, workspace_id, name, private) VALUES ($1, $2, 'Legal', false)`, team, f.wid)
	tid := f.ticket("Contract question", &team, &f.uid)
	hidden := f.notify(hd.NotifyAssigned, &tid, nil)
	f.exec(`UPDATE tickets SET assignee_id = $2 WHERE id = $1`, tid, f.maya)
	f.exec(`UPDATE teams SET private = true WHERE id = $1`, team)
	f.send(later())
	if len(f.mails()) != 0 {
		t.Fatal("emailed a ticket the user can no longer see")
	}
	if f.count(`SELECT count(*) FROM notifications WHERE id = $1 AND email_due_at IS NULL AND emailed_at IS NULL`, hidden) != 1 {
		t.Fatal("hidden notification still waiting")
	}

	// Email turned off for the type after it was scheduled.
	open := f.ticket("Open", nil, &f.uid)
	f.notify(hd.NotifyCustomerReplied, &open, map[string]any{})
	if r := f.c.do("PATCH", "/v1/me/notification-preferences", map[string]any{"types": []any{map[string]any{"type": "customer_replied", "email": false}}}); r.Status != 200 {
		t.Fatalf("patch: %d", r.Status)
	}
	f.send(later())
	if len(f.mails()) != 0 {
		t.Fatal("emailed a type whose email is off")
	}
	// A notification of that type made now is not even scheduled.
	off := f.notify(hd.NotifyCustomerReplied, &open, map[string]any{})
	if f.count(`SELECT count(*) FROM notifications WHERE id = $1 AND email_due_at IS NULL`, off) != 1 {
		t.Fatal("scheduled despite email off")
	}

	// Paused.
	f.notify(hd.NotifyAssigned, &open, nil)
	f.exec(`INSERT INTO notification_settings (user_id, paused_until) VALUES ($1, now() + interval '1 day')
		ON CONFLICT (user_id) DO UPDATE SET paused_until = EXCLUDED.paused_until`, f.uid)
	f.send(later())
	if len(f.mails()) != 0 {
		t.Fatal("emailed while paused")
	}
	f.exec(`DELETE FROM notification_settings WHERE user_id = $1`, f.uid)

	// Unconfirmed address.
	f.notify(hd.NotifyAssigned, &open, nil)
	f.exec(`UPDATE users SET email_verified_at = NULL WHERE id = $1`, f.uid)
	f.send(later())
	if len(f.mails()) != 0 {
		t.Fatal("emailed an unconfirmed address")
	}
	f.exec(`UPDATE users SET email_verified_at = now() WHERE id = $1`, f.uid)

	// No longer a member (the workspace's other owner removed them).
	f.notify(hd.NotifyAssigned, &open, nil)
	f.exec(`DELETE FROM members WHERE workspace_id = $1 AND user_id = $2`, f.wid, f.uid)
	f.send(later())
	if len(f.mails()) != 0 {
		t.Fatal("emailed a removed member")
	}
}

// TestNotificationEmailDigest: a daily cadence waits for the digest hour, and a cadence
// change moves what is already waiting.
func TestNotificationEmailDigest(t *testing.T) {
	f := newNotifyFixture(t, "notifdigest")
	tid := f.ticket("SAML with Okta", nil, &f.uid)
	nid := f.notify(hd.NotifyAssigned, &tid, nil)
	if r := f.c.do("PATCH", "/v1/me/notification-preferences", map[string]any{"email_cadence": "daily", "digest_hour": 7, "timezone": "America/New_York"}); r.Status != 200 {
		t.Fatalf("patch: %d %s", r.Status, r.Raw)
	}
	var due time.Time
	if err := f.e.d.DB.Pool.QueryRow(context.Background(), `SELECT email_due_at FROM notifications WHERE id = $1`, nid).Scan(&due); err != nil {
		t.Fatal(err)
	}
	ny, _ := time.LoadLocation("America/New_York")
	if l := due.In(ny); l.Hour() != 7 || l.Minute() != 0 || !due.After(time.Now()) || due.After(time.Now().Add(24*time.Hour)) {
		t.Fatalf("daily due %v", due.In(ny))
	}
	f.send(due.Add(-time.Minute))
	if len(f.mails()) != 0 {
		t.Fatal("digest sent before its hour")
	}
	f.notify(hd.NotifyMentioned, &tid, nil)
	f.send(due.Add(time.Minute))
	m := f.mails()
	if len(m) != 1 || !strings.Contains(m[0].HTML, "Your daily digest") {
		t.Fatalf("digest: %+v", m)
	}
	// Off clears what waits.
	f.notify(hd.NotifyAssigned, &tid, nil)
	f.c.do("PATCH", "/v1/me/notification-preferences", map[string]any{"email_cadence": "off"})
	if f.count(`SELECT count(*) FROM notifications WHERE user_id = $1 AND email_due_at IS NOT NULL AND emailed_at IS NULL`, f.uid) != 0 {
		t.Fatal("off left emails waiting")
	}
}

// TestNotificationUnsubscribe: the email's token turns off that workspace's notification
// emails, by the page or by RFC 8058 one-click; other tokens are refused.
func TestNotificationUnsubscribe(t *testing.T) {
	f := newNotifyFixture(t, "notifunsub")
	tid := f.ticket("SAML with Okta", nil, &f.uid)
	f.notify(hd.NotifyAssigned, &tid, nil)
	f.send(later())
	m := f.mails()
	if len(m) != 1 {
		t.Fatalf("mails: %d", len(m))
	}
	lu := m[0].Headers["List-Unsubscribe"]
	if !strings.HasPrefix(lu, "<http://api.test/v1/notifications/unsubscribe/one-click?token=") || m[0].Headers["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" {
		t.Fatalf("headers: %v", m[0].Headers)
	}
	u, err := url.Parse(strings.Trim(lu, "<>"))
	if err != nil {
		t.Fatal(err)
	}
	token := u.Query().Get("token")
	if !strings.Contains(m[0].Text, "/unsubscribe?token="+url.QueryEscape(token)) {
		t.Fatal("footer link missing")
	}

	anon := f.e.client()
	r := anon.do("POST", "/v1/notifications/unsubscribe", map[string]any{"token": token})
	if r.Status != 200 || r.str("workspace", "id") != f.wid.String() || r.str("email") != f.email {
		t.Fatalf("unsubscribe: %d %s", r.Status, r.Raw)
	}
	all, err := notifyprefs.All(context.Background(), f.e.d.DB.Pool, f.wid, f.uid)
	if err != nil {
		t.Fatal(err)
	}
	for typ, ch := range all {
		if ch.Email {
			t.Fatalf("%s email still on", typ)
		}
		if typ == "assigned" && !ch.Push {
			t.Fatal("unsubscribe changed the phone channel")
		}
	}

	// One-click, as a mail provider sends it.
	f.c.do("PATCH", "/v1/me/notification-preferences", map[string]any{"types": []any{map[string]any{"type": "assigned", "email": true}}})
	req, _ := http.NewRequest("POST", f.e.srv.URL+"/v1/notifications/unsubscribe/one-click?token="+url.QueryEscape(token), strings.NewReader("List-Unsubscribe=One-Click"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if ch, _ := notifyprefs.Channel(context.Background(), f.e.d.DB.Pool, f.wid, f.uid, "assigned"); res.StatusCode != 200 || ch.Email {
		t.Fatalf("one-click: %d %+v", res.StatusCode, ch)
	}

	// Other purposes, garbage and foreign workspaces are refused.
	access, _, _ := f.e.d.Tokens.Sign(auth.Claims{Purpose: auth.PurposeAccess, Workspace: f.wid.String()}, f.uid.String(), time.Minute)
	stranger, _, _ := f.e.d.Tokens.Sign(auth.Claims{Purpose: auth.PurposeNotificationUnsubscribe, Workspace: ids.New().String()}, f.uid.String(), time.Minute)
	for _, bad := range []string{access, stranger, "nope", ""} {
		if r := anon.do("POST", "/v1/notifications/unsubscribe", map[string]any{"token": bad}); r.Status != 400 {
			t.Fatalf("bad token: %d %s", r.Status, r.Raw)
		}
	}
}

// TestNotificationEmailPacing: after an email, what comes next waits for the cooldown and
// goes as one email; an urgent one may go sooner; activity in the app and the instance's
// send rate hold emails without losing any.
func TestNotificationEmailPacing(t *testing.T) {
	f := newNotifyFixture(t, "notifpace")
	pace := notifyprefs.Current()
	t1 := f.ticket("SAML with Okta", nil, &f.uid)
	t2 := f.ticket("Export to CSV", nil, &f.uid)
	f.notify(hd.NotifyAssigned, &t1, nil)
	first := later()
	f.send(first)
	if len(f.mails()) != 1 {
		t.Fatal("first email not sent")
	}
	// Two more right after: held for the cooldown, the sweep leaves the member alone.
	f.notify(hd.NotifyCustomerReplied, &t2, map[string]any{"contact": map[string]any{"name": "Dana Lee"}, "preview": "Still missing"})
	f.notify(hd.NotifyAssigned, &t2, nil)
	f.send(first.Add(time.Second))
	if len(f.mails()) != 1 {
		t.Fatal("sent inside the cooldown")
	}
	if n := f.count(`SELECT count(*) FROM notification_email_state WHERE user_id = $1
		AND abs(extract(epoch FROM next_check_at - $2::timestamptz)) < 0.01`, f.uid, first.Add(pace.Cooldown(0))); n != 1 {
		t.Fatal("hold not recorded at the end of the cooldown")
	}
	f.exec(`UPDATE notifications SET email_due_at = now() - interval '1 minute' WHERE user_id = $1 AND emailed_at IS NULL`, f.uid)
	f.exec(`UPDATE notification_email_state SET next_check_at = now() + interval '10 minutes' WHERE user_id = $1`, f.uid)
	before := f.count(`SELECT count(*) FROM river_job WHERE kind = $1 AND args->>'user_id' = $2`, jobs.NotificationEmailArgs{}.Kind(), f.uid.String())
	if err := f.e.s.sweepNotificationEmails(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := f.count(`SELECT count(*) FROM river_job WHERE kind = $1 AND args->>'user_id' = $2`, jobs.NotificationEmailArgs{}.Kind(), f.uid.String()); n != before {
		t.Fatal("the sweep queued a member on hold")
	}
	// At the end of the cooldown both go in one email, and the next cooldown is longer.
	second := first.Add(pace.Cooldown(0))
	f.send(second)
	m := f.mails()
	if len(m) != 2 || !strings.Contains(m[1].Text, "and 1 more") && !strings.Contains(m[1].Subject, "and 1 more") {
		t.Fatalf("after cooldown: %d %q", len(m), m[len(m)-1].Subject)
	}
	if n := f.count(`SELECT count(*) FROM notification_email_state WHERE user_id = $1 AND step = 1
		AND abs(extract(epoch FROM next_allowed_at - $2::timestamptz)) < 0.01`, f.uid, second.Add(pace.Cooldown(1))); n != 1 {
		t.Fatal("cooldown did not grow")
	}
	// An urgent one cuts the longer cooldown short, at the urgent gap.
	f.notify(hd.NotifySLABreached, &t1, map[string]any{"kind": "first_response"})
	f.exec(`UPDATE notifications SET email_due_at = $2 WHERE user_id = $1 AND emailed_at IS NULL`, f.uid, second)
	f.send(second.Add(pace.UrgentGap))
	if m = f.mails(); len(m) != 3 || !strings.Contains(m[2].Text, "NEEDS YOU NOW") && !strings.Contains(m[2].Subject, "missed") {
		t.Fatalf("urgent: %d %q", len(m), m[len(m)-1].Subject)
	}
	if n := f.count(`SELECT count(*) FROM notification_email_log WHERE user_id = $1 AND urgent`, f.uid); n != 1 {
		t.Fatal("urgent email not counted")
	}

	// Active in the app: held until the active window passes.
	f.exec(`DELETE FROM notification_email_state WHERE user_id = $1`, f.uid)
	f.exec(`DELETE FROM notification_email_log WHERE user_id = $1`, f.uid)
	f.notify(hd.NotifyAssigned, &t1, nil)
	at := later()
	f.exec(`UPDATE members SET active_at = $3 WHERE workspace_id = $1 AND user_id = $2`, f.wid, f.uid, at.Add(-time.Minute))
	f.send(at)
	if len(f.mails()) != 3 {
		t.Fatal("emailed while active in the app")
	}
	f.exec(`UPDATE members SET active_at = NULL WHERE workspace_id = $1 AND user_id = $2`, f.wid, f.uid)

	// The instance's send rate: full, so the email waits a minute; nothing is dropped.
	f.exec(`INSERT INTO notification_email_log (user_id, workspace_id, sent_at, notifications)
		SELECT $1, $2, $3, 1 FROM generate_series(1, $4)`, f.maya, f.wid, at.Add(-10*time.Second), pace.InstancePerMinute)
	f.send(at)
	if len(f.mails()) != 3 || f.count(`SELECT count(*) FROM notifications WHERE user_id = $1 AND emailed_at IS NULL AND email_due_at IS NOT NULL`, f.uid) != 1 {
		t.Fatal("instance rate not held, or the notification was dropped")
	}
	f.send(at.Add(2 * time.Minute))
	if len(f.mails()) != 4 {
		t.Fatal("held email never sent")
	}
}

// TestNotificationResubscribe: turning email back on restores the choices from before the
// unsubscribe link, not "all on".
func TestNotificationResubscribe(t *testing.T) {
	f := newNotifyFixture(t, "notifresub")
	// A choice before: mentions off, customer replies explicitly on.
	if r := f.c.do("PATCH", "/v1/me/notification-preferences", map[string]any{"types": []any{
		map[string]any{"type": "mentioned", "email": false}, map[string]any{"type": "customer_replied", "email": true}}}); r.Status != 200 {
		t.Fatalf("patch: %d %s", r.Status, r.Raw)
	}
	token, err := f.e.s.unsubscribeToken(f.uid, f.wid)
	if err != nil {
		t.Fatal(err)
	}
	anon := f.e.client()
	for range 2 { // twice: the second must not overwrite what was before with "all off"
		if r := anon.do("POST", "/v1/notifications/unsubscribe", map[string]any{"token": token}); r.Status != 200 {
			t.Fatalf("unsubscribe: %d %s", r.Status, r.Raw)
		}
	}
	r := f.c.do("GET", "/v1/me/notification-preferences", nil)
	if r.str("unsubscribed_at") == "" {
		t.Fatalf("unsubscribed_at missing: %s", r.Raw)
	}
	r = f.c.do("POST", "/v1/me/notification-preferences/resubscribe", nil)
	if r.Status != 200 || r.str("unsubscribed_at") != "" {
		t.Fatalf("resubscribe: %d %s", r.Status, r.Raw)
	}
	all, err := notifyprefs.All(context.Background(), f.e.d.DB.Pool, f.wid, f.uid)
	if err != nil {
		t.Fatal(err)
	}
	if !all["assigned"].Email || all["mentioned"].Email || !all["customer_replied"].Email {
		t.Fatalf("not restored: %+v", all)
	}
	// Turning one email on by hand also ends the unsubscribe.
	anon.do("POST", "/v1/notifications/unsubscribe", map[string]any{"token": token})
	f.c.do("PATCH", "/v1/me/notification-preferences", map[string]any{"types": []any{map[string]any{"type": "assigned", "email": true}}})
	if f.count(`SELECT count(*) FROM notification_unsubscribes WHERE user_id = $1`, f.uid) != 0 {
		t.Fatal("manual turn-on kept the unsubscribe")
	}
}

// TestPushFollowsPreferences: the phone channel and the pause stop pushes before any
// device's own mutes apply.
func TestPushFollowsPreferences(t *testing.T) {
	e := newEnv(t)
	fake := withAPNs(t, e)
	phone, _, _ := e.nativeUser("pushprefs")
	wid := phone.newWorkspace("Push Prefs")
	uid := phone.do("GET", "/v1/me", nil).str("user", "id")
	if r := phone.do("POST", "/v1/me/devices", map[string]any{"platform": "ios", "token": hexToken(t), "environment": "production", "app_version": "1", "name": "iPhone"}); r.Status != 200 {
		t.Fatalf("device: %d %s", r.Status, r.Raw)
	}
	if r := phone.do("PATCH", "/v1/me/notification-preferences", map[string]any{"types": []any{map[string]any{"type": "customer_replied", "push": false}}}); r.Status != 200 {
		t.Fatalf("patch: %d %s", r.Status, r.Raw)
	}
	nid, _ := e.notify(t, wid, uid, "customer_replied")
	if err := runPush(t, e, jobs.PushNotificationArgs{WorkspaceID: uuid.MustParse(wid), UserID: uuid.MustParse(uid), NotificationID: nid}); err != nil {
		t.Fatal(err)
	}
	if fake.count() != 0 {
		t.Fatal("pushed with the phone channel off")
	}
	phone.do("PATCH", "/v1/me/notification-preferences", map[string]any{"paused_until": time.Now().Add(time.Hour).Format(time.RFC3339)})
	nid, _ = e.notify(t, wid, uid, "assigned")
	if err := runPush(t, e, jobs.PushNotificationArgs{WorkspaceID: uuid.MustParse(wid), UserID: uuid.MustParse(uid), NotificationID: nid}); err != nil {
		t.Fatal(err)
	}
	if fake.count() != 0 {
		t.Fatal("pushed while paused")
	}
	phone.do("PATCH", "/v1/me/notification-preferences", map[string]any{"paused_until": ""})
	if err := runPush(t, e, jobs.PushNotificationArgs{WorkspaceID: uuid.MustParse(wid), UserID: uuid.MustParse(uid), NotificationID: nid}); err != nil {
		t.Fatal(err)
	}
	if fake.count() != 1 {
		t.Fatalf("pushes after resume: %d", fake.count())
	}
}
