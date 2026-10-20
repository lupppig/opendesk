package emails

import (
	"time"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/mailer"
	"github.com/useopendesk/opendesk/internal/notifyprefs"
)

// Preview is one email rendered with sample data (cmd/emailpreview).
type Preview struct {
	Name  string // file name without extension
	Title string // what it is, for the gallery
	Mail  mailer.Mail
}

// Previews renders every email opendesk sends, with made-up data.
func Previews(s Site, now time.Time) []Preview {
	wid := uuid.MustParse("0192f0c4-0000-7000-8000-00000000a11a")
	team := uuid.MustParse("0192f0c4-0000-7000-8000-00000000beef")
	const ws = "Margin"
	const token = "sample-unsubscribe-token"
	loc, err := time.LoadLocation("Europe/Budapest")
	if err != nil {
		loc = time.UTC
	}
	batch := func(cadence string, notes []Note, more int) Batch {
		return Batch{WorkspaceID: wid, Workspace: ws, Cadence: cadence, Notes: notes, More: more, Location: loc, Now: now, UnsubscribeToken: token, Urgent: notifyprefs.Urgent}
	}
	t1095, t1102, t1110, t1121 := uuid.MustParse("0192f0c4-0000-7000-8000-000000001095"), uuid.MustParse("0192f0c4-0000-7000-8000-000000001102"),
		uuid.MustParse("0192f0c4-0000-7000-8000-000000001110"), uuid.MustParse("0192f0c4-0000-7000-8000-000000001121")
	maya := map[string]any{"name": "Maya Chen"}
	dana := map[string]any{"name": "Dana Lee"}
	ago := func(m int) time.Time { return now.Add(-time.Duration(m) * time.Minute) }
	ticket := func(typ string, id uuid.UUID, num int64, subject string, data map[string]any, at time.Time) Note {
		return Note{ID: uuid.New(), Type: typ, TicketID: &id, TicketNumber: num, TicketSubject: subject, Data: data, CreatedAt: at}
	}
	plain := func(typ string, data map[string]any) Note {
		return Note{ID: uuid.New(), Type: typ, Data: data, CreatedAt: ago(3)}
	}

	assigned := ticket("assigned", t1095, 1095, "SAML with Okta", map[string]any{"actor": maya}, ago(6))
	mentioned := ticket("mentioned", t1095, 1095, "SAML with Okta", map[string]any{"actor": maya,
		"preview": "@Alex can you check the Okta metadata URL? The customer says the ACS URL changed after the domain move."}, ago(8))
	replied := ticket("customer_replied", t1102, 1102, "Export to CSV skips archived tickets", map[string]any{"contact": dana,
		"preview": "Thanks, that worked for the open ones. The archived tickets from March are still missing from the file though."}, ago(4))
	sla := ticket("sla_breached", t1110, 1110, "Refund for a double charge", map[string]any{"kind": "first_response"}, ago(12))
	newTicket := ticket("new_ticket", t1121, 1121, "Cannot connect a Gmail inbox", map[string]any{"channel": "email", "contact": map[string]any{"name": "Sam Ortiz"}}, ago(1))
	handoff := ticket("ai_handoff", t1121, 1121, "Cannot connect a Gmail inbox", map[string]any{"via": "contact", "contact": map[string]any{"name": "Sam Ortiz"}}, ago(15))

	// What piles up during a cooldown on opendesk Cloud and goes out as one email.
	t1131, t1134, t1135, t1136, t1137 := uuid.MustParse("0192f0c4-0000-7000-8000-000000001131"), uuid.MustParse("0192f0c4-0000-7000-8000-000000001134"),
		uuid.MustParse("0192f0c4-0000-7000-8000-000000001135"), uuid.MustParse("0192f0c4-0000-7000-8000-000000001136"), uuid.MustParse("0192f0c4-0000-7000-8000-000000001137")
	replied2 := ticket("customer_replied", t1102, 1102, "Export to CSV skips archived tickets", map[string]any{"contact": dana,
		"preview": "Also, could the export include the tags? We filter on them in our spreadsheet."}, ago(2))
	assigned2 := ticket("assigned", t1131, 1131, "Invoice address on the Pro plan", map[string]any{"actor": maya}, ago(21))
	replied3 := ticket("customer_replied", t1131, 1131, "Invoice address on the Pro plan", map[string]any{"contact": map[string]any{"name": "Priya Nair"},
		"preview": "Our finance team needs the VAT number on it as well, is that possible?"}, ago(17))
	cooldown := []Note{replied, replied2, assigned2, replied3, mentioned}
	bulk := []Note{
		ticket("assigned", t1134, 1134, "Widget does not load on Safari 17", map[string]any{"actor": maya}, ago(9)),
		ticket("assigned", t1135, 1135, "Webhook retries after a 410", map[string]any{"actor": maya}, ago(9)),
		ticket("assigned", t1136, 1136, "Change the help center domain", map[string]any{"actor": maya}, ago(9)),
		ticket("assigned", t1137, 1137, "Two seats billed after removing a member", map[string]any{"actor": maya}, ago(9)),
		replied,
	}
	busy := []Note{
		sla, handoff, newTicket, mentioned, replied, replied2, assigned2, replied3, bulk[0], bulk[1], bulk[2], bulk[3],
		plain("team_joined", map[string]any{"team": map[string]any{"id": team.String(), "name": "Billing", "private": true}, "user": map[string]any{"name": "Alex Kim"}}),
		plain("ai_balance_low", map[string]any{"remaining_usd": 1.84}),
	}

	out := []Preview{
		{Name: "account-verify-email", Title: "Confirm your email", Mail: VerifyEmail(s, s.App("/verify", "token", "sample"))},
		{Name: "account-reset-password", Title: "Reset your password", Mail: ResetPassword(s, s.App("/reset", "token", "sample"))},
		{Name: "account-invitation", Title: "Invitation to a workspace", Mail: Invitation(s, Invite{Inviter: "Maya Chen", Workspace: ws, Role: "agent", Link: s.App("/invite", "token", "sample")})},
		{Name: "account-2fa-disabled", Title: "Two-factor authentication turned off", Mail: TwoFactorDisabled(s)},
		{Name: "account-password-changed", Title: "Password changed", Mail: SecurityNotice(s, PasswordChanged, "")},
		{Name: "account-signup-attempt", Title: "Sign-up with a taken address", Mail: SignupAttempt(s)},
		{Name: "account-passkey-added", Title: "Passkey added", Mail: SecurityNotice(s, PasskeyAdded, "Passkey: MacBook Touch ID")},
		{Name: "billing-payment-failed", Title: "Payment failed", Mail: PaymentFailed(s, ws, wid.String())},
		{Name: "notify-assigned", Title: "Notification: assigned", Mail: Notifications(s, batch("instant", []Note{assigned}, 0))},
		{Name: "notify-new-ticket", Title: "Notification: new unassigned ticket", Mail: Notifications(s, batch("instant", []Note{newTicket}, 0))},
		{Name: "notify-mentioned", Title: "Notification: mentioned", Mail: Notifications(s, batch("instant", []Note{mentioned}, 0))},
		{Name: "notify-customer-replied", Title: "Notification: customer replied", Mail: Notifications(s, batch("instant", []Note{replied}, 0))},
		{Name: "notify-sla-breached", Title: "Notification: SLA breached", Mail: Notifications(s, batch("instant", []Note{sla}, 0))},
		{Name: "notify-ai-handoff", Title: "Notification: handoff to a person", Mail: Notifications(s, batch("instant", []Note{handoff}, 0))},
		{Name: "notify-team-joined", Title: "Notification: team joined", Mail: Notifications(s, batch("instant", []Note{plain("team_joined", map[string]any{
			"team": map[string]any{"id": team.String(), "name": "Billing", "private": true}, "user": map[string]any{"name": "Alex Kim"}})}, 0))},
		{Name: "notify-ai-cap-reached", Title: "Notification: chat agent monthly cap", Mail: Notifications(s, batch("instant", []Note{plain("ai_cap_reached", map[string]any{"cap_usd": 50.0})}, 0))},
		{Name: "notify-ai-key-failed", Title: "Notification: AI key refused", Mail: Notifications(s, batch("instant", []Note{plain("ai_key_failed", map[string]any{"provider": "openai", "reason": "rejected"})}, 0))},
		{Name: "notify-ai-balance-low", Title: "Notification: AI balance low", Mail: Notifications(s, batch("instant", []Note{plain("ai_balance_low", map[string]any{"remaining_usd": 1.84})}, 0))},
		{Name: "notify-ai-balance-out", Title: "Notification: AI balance used up", Mail: Notifications(s, batch("instant", []Note{plain("ai_balance_out", map[string]any{"remaining_usd": 0.0})}, 0))},
		{Name: "notify-auto-recharge-failed", Title: "Notification: auto-recharge failed", Mail: Notifications(s, batch("instant", []Note{plain("auto_recharge_failed", map[string]any{"reason": "card_declined"})}, 0))},
		{Name: "notify-import-finished", Title: "Notification: import finished", Mail: Notifications(s, batch("instant", []Note{plain("import_finished", map[string]any{
			"import_id": "0192f0c4-0000-7000-8000-00000000c0de", "source": "zendesk", "source_name": "Zendesk", "account": "Acme", "status": "completed",
			"tickets": 142, "updated": 0, "articles": 30, "failed": 2, "error": ""})}, 0))},
		{Name: "notify-import-needs-credentials", Title: "Notification: import needs new credentials", Mail: Notifications(s, batch("instant", []Note{plain("import_finished", map[string]any{
			"import_id": "0192f0c4-0000-7000-8000-00000000c0de", "source": "helpscout", "source_name": "Help Scout", "account": "Acme", "status": "needs_credentials",
			"tickets": 61, "updated": 0, "articles": 0, "failed": 0, "error": "Help Scout refused the credentials. Enter new ones to go on where it stopped."})}, 0))},
		{Name: "notify-batch", Title: "Several notifications at once", Mail: Notifications(s, batch("instant", []Note{replied, assigned, mentioned, sla}, 0))},
		{Name: "notify-after-cooldown", Title: "Five that waited out a cooldown", Mail: Notifications(s, batch("instant", cooldown, 0))},
		{Name: "notify-bulk-assigned", Title: "Several tickets assigned at once", Mail: Notifications(s, batch("instant", bulk, 0))},
		{Name: "notify-busy", Title: "A busy stretch, urgent ones first", Mail: Notifications(s, batch("instant", busy, 0))},
		{Name: "digest-hourly", Title: "Hourly digest", Mail: Notifications(s, batch("hourly", []Note{replied, assigned, mentioned, sla, handoff}, 0))},
	}
	var day []Note
	for i := range 20 {
		n := []Note{replied, mentioned, assigned, sla, handoff}[i%5]
		n.ID = uuid.New()
		n.CreatedAt = now.Add(-time.Duration(30+i*55) * time.Minute)
		day = append(day, n)
	}
	out = append(out,
		Preview{Name: "digest-daily", Title: "Daily digest (more than 20)", Mail: Notifications(s, batch("daily", day, 7))},
	)
	test := batch("instant", SampleNotes(now), 0)
	test.Test = true
	out = append(out, Preview{Name: "notify-test", Title: "Test email from settings", Mail: Notifications(s, test)})
	return out
}
