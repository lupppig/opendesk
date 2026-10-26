package email

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/useopendesk/opendesk/internal/ai/bots"
	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/jobs"
	"github.com/useopendesk/opendesk/internal/templates"
)

const maxSettingsHTML = 64 << 10

type mailboxBody struct {
	Body Mailbox
}

type createMailboxInput struct {
	Body struct {
		Name             string                        `json:"name" minLength:"1" maxLength:"64"`
		Email            string                        `json:"email" maxLength:"254"`
		DisplayName      string                        `json:"display_name,omitempty" maxLength:"64"`
		InboundKind      string                        `json:"inbound_kind" enum:"forwarding,imap,google,microsoft"`
		OutboundKind     string                        `json:"outbound_kind" enum:"smtp,google,microsoft,system"`
		Config           MailboxConfig                 `json:"config,omitempty"`
		Password         string                        `json:"password,omitempty" maxLength:"1024" doc:"IMAP password (write-only)"`
		SMTPPassword     string                        `json:"smtp_password,omitempty" maxLength:"1024" doc:"SMTP password when it differs from the IMAP one (write-only)"`
		TeamID           *uuid.UUID                    `json:"team_id,omitempty"`
		SignatureHTML    string                        `json:"signature_html,omitempty"`
		AutoReplyEnabled bool                          `json:"auto_reply_enabled,omitempty"`
		AutoReplySubject string                        `json:"auto_reply_subject,omitempty" maxLength:"200"`
		AutoReplyHTML    string                        `json:"auto_reply_html,omitempty"`
		BrandID          *uuid.UUID                    `json:"brand_id,omitempty" doc:"The brand the address belongs to; default: the default brand"`
		Aliases          []string                      `json:"aliases,omitempty" maxItems:"20" doc:"Other addresses that deliver into this mailbox"`
		AliasTeams       map[string]uuid.UUID          `json:"alias_teams,omitempty" doc:"An alias to the team its new tickets go to, instead of team_id"`
		AliasSettings    map[string]aliasSettingsInput `json:"alias_settings,omitempty" doc:"Each alias's From name, signature and brand"`
		FromNameFormat   string                        `json:"from_name_format,omitempty" maxLength:"200" doc:"{agent}, {agent_first}, {team}, {brand}, {mailbox}; default \"{agent} at {mailbox}\""`
		Verify           bool                          `json:"verify,omitempty" doc:"Sign in to the IMAP and SMTP servers first; a refusal is 422 mailbox_connection_failed and nothing is saved"`
	}
}

type mailboxConfigPatch struct {
	IMAPHost     *string `json:"imap_host,omitempty"`
	IMAPPort     *int    `json:"imap_port,omitempty" minimum:"1" maximum:"65535"`
	IMAPUsername *string `json:"imap_username,omitempty"`
	IMAPTLS      *string `json:"imap_tls,omitempty" enum:"tls,starttls,none"`
	SMTPHost     *string `json:"smtp_host,omitempty"`
	SMTPPort     *int    `json:"smtp_port,omitempty" minimum:"1" maximum:"65535"`
	SMTPUsername *string `json:"smtp_username,omitempty"`
	SMTPTLS      *string `json:"smtp_tls,omitempty" enum:"tls,starttls,none"`
	SyncStart    *string `json:"sync_start,omitempty" enum:"recent,new" doc:"Where the first sync reads from when the mailbox starts reading its inbox: the last 7 days (recent) or only new mail"`
	// Tidy-up at the provider, for a mailbox that reads its inbox through Google or Microsoft.
	ProviderMarkRead       *bool `json:"provider_mark_read,omitempty" doc:"Mark emails read in Gmail or Outlook once they become ticket messages"`
	ProviderArchiveOnSolve *bool `json:"provider_archive_on_solve,omitempty" doc:"Archive a ticket's emails in Gmail or Outlook when it is solved or closed"`
	ProviderLabel          *bool `json:"provider_label,omitempty" doc:"Label emails \"opendesk\" in Gmail (an Outlook category) once they become ticket messages"`
}

// turnsTidyOn reports a patch that turns a provider tidy-up option on.
func (c *mailboxConfigPatch) turnsTidyOn() bool {
	on := func(b *bool) bool { return b != nil && *b }
	return c != nil && (on(c.ProviderMarkRead) || on(c.ProviderArchiveOnSolve) || on(c.ProviderLabel))
}

type updateMailboxInput struct {
	ID   string `path:"id"`
	Body struct {
		Name             *string             `json:"name,omitempty" minLength:"1" maxLength:"64"`
		Email            *string             `json:"email,omitempty" maxLength:"254"`
		DisplayName      *string             `json:"display_name,omitempty" maxLength:"64"`
		InboundKind      *string             `json:"inbound_kind,omitempty" enum:"forwarding,imap,google,microsoft" doc:"forwarding drops the IMAP sign-in; imap needs its settings and password; google and microsoft connect with the OAuth start route"`
		OutboundKind     *string             `json:"outbound_kind,omitempty" enum:"smtp,google,microsoft,system"`
		Config           *mailboxConfigPatch `json:"config,omitempty"`
		Password         *string             `json:"password,omitempty" maxLength:"1024" doc:"IMAP password (write-only)"`
		SMTPPassword     *string             `json:"smtp_password,omitempty" maxLength:"1024" doc:"SMTP password (write-only)"`
		TeamID           *string             `json:"team_id,omitempty" doc:"Team id, or an empty string to clear"`
		SignatureHTML    *string             `json:"signature_html,omitempty"`
		AutoReplyEnabled *bool               `json:"auto_reply_enabled,omitempty"`
		AutoReplySubject *string             `json:"auto_reply_subject,omitempty" maxLength:"200"`
		AutoReplyHTML    *string             `json:"auto_reply_html,omitempty"`
		Status           *string             `json:"status,omitempty" enum:"active,disabled"`
		// Ollie answers emails to this mailbox (contracts/agent.md "Email").
		AIAnswers             *bool   `json:"ai_answers,omitempty" doc:"Ollie answers emails to this mailbox; needs AI on"`
		AIAnswersMode         *string `json:"ai_answers_mode,omitempty" enum:"draft,send" doc:"draft: a suggested reply a teammate sends; send: Ollie replies"`
		AIAnswersOutsideHours *bool   `json:"ai_answers_outside_hours,omitempty" doc:"Only outside business hours (while business hours are on)"`
		// Addresses and brands (contracts/teams.md).
		BrandID        *string                        `json:"brand_id,omitempty" doc:"Move the address to another brand of the workspace"`
		Aliases        *[]string                      `json:"aliases,omitempty" maxItems:"20" doc:"Replaces the aliases"`
		AliasTeams     *map[string]uuid.UUID          `json:"alias_teams,omitempty" doc:"Replaces them: an alias to the team its new tickets go to. Removing an alias drops its entry"`
		AliasSettings  *map[string]aliasSettingsInput `json:"alias_settings,omitempty" doc:"Replaces each alias's From name, signature and brand (an alias left out has none of its own); signature_html and send_as left out are kept. Removing an alias drops its entry"`
		FromNameFormat *string                        `json:"from_name_format,omitempty" maxLength:"200" doc:"\"\" resets to \"{agent} at {mailbox}\""`
		// Website contact forms whose authenticated mail is from the visitor in Reply-To.
		TrustedFormSenders *[]string `json:"trusted_form_senders,omitempty" maxItems:"50" doc:"Replaces the list: email addresses or @domain entries"`
		// Replies from members' own Collab addresses on its tickets (contracts/collab.md "Answering tickets").
		PersonalReplies *personalRepliesInput `json:"personal_replies,omitempty" doc:"Replaces them: enabled and reply_to left out or null follow the workspace; accounts any (default), shared or domain"`
		Verify          bool                  `json:"verify,omitempty" doc:"When the IMAP or SMTP settings, a password or the way replies are sent change, sign in to the changed servers first; a refusal is 422 mailbox_connection_failed and nothing is saved"`

		// The bot the AI agent answers as here (contracts/bots.md).
		BotID bots.Assignment `json:"bot_id,omitempty" doc:"A bot of the workspace, or null for the default bot"`
	}
}

// personalRepliesInput replaces a mailbox's PersonalReplies; what is left out follows the workspace.
type personalRepliesInput struct {
	Enabled  *bool   `json:"enabled,omitempty"`
	ReplyTo  *string `json:"reply_to,omitempty" enum:"address,mailbox"`
	Accounts string  `json:"accounts,omitempty" enum:"any,shared,domain"`
}

type idInput struct {
	ID string `path:"id"`
}

// mailboxUsage is what removing a mailbox touches: the tickets that reply from it.
type mailboxUsage struct {
	Tickets int `json:"tickets" doc:"Tickets the caller can see whose reply address is this mailbox"`
	Open    int `json:"open" doc:"Of those, the ones open, pending or on hold"`
}

func parseID(s string) (uuid.UUID, error) {
	id, ok := ids.Parse(s)
	if !ok {
		return uuid.Nil, httpx.NotFound("Mailbox")
	}
	return id, nil
}

func (s *service) registerMailboxRoutes(api huma.API) {
	tags := []string{"Mailboxes"}
	s.registerSendAsRoutes(api)

	huma.Register(api, huma.Operation{OperationID: "list-mailboxes", Method: http.MethodGet, Path: "/v1/mailboxes",
		Summary: "List mailboxes", Tags: tags},
		func(ctx context.Context, in *struct{ httpx.ListParams }) (*struct{ Body httpx.Page[Mailbox] }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			var cur struct {
				C time.Time `json:"c"`
				I uuid.UUID `json:"i"`
			}
			if err := httpx.DecodeCursor(in.Cursor, &cur); err != nil {
				return nil, err
			}
			limit := in.Limit
			if limit <= 0 {
				limit = 50
			}
			rows, err := s.d.DB.Pool.Query(ctx, `SELECT `+mailboxColumns+` FROM mailboxes WHERE workspace_id = $1
				AND ($2::uuid IS NULL OR (created_at, id) > ($3, $2)) ORDER BY created_at, id LIMIT $4`,
				p.WorkspaceID, nullUUID(cur.I), cur.C, limit+1)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (*mailboxRow, error) { return scanMailbox(r) })
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			a, err := s.access(ctx, s.d.DB.Pool, p.WorkspaceID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			admin := canSeeConfig(p)
			out := make([]Mailbox, 0, len(list))
			for _, m := range list {
				out = append(out, s.api(m, admin, a))
			}
			page := httpx.NewPage(out, limit, func(m Mailbox) any {
				return map[string]any{"c": m.CreatedAt, "i": m.ID}
			})
			return &struct{ Body httpx.Page[Mailbox] }{page}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "get-mailbox", Method: http.MethodGet, Path: "/v1/mailboxes/{id}",
		Summary: "Get a mailbox", Tags: tags},
		func(ctx context.Context, in *idInput) (*mailboxBody, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			m, err := loadMailbox(ctx, s.d.DB.Pool, p.WorkspaceID, id)
			if err != nil {
				return nil, wrapMailboxErr(err)
			}
			a, err := s.access(ctx, s.d.DB.Pool, p.WorkspaceID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &mailboxBody{s.api(m, canSeeConfig(p), a)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "create-mailbox", Method: http.MethodPost, Path: "/v1/mailboxes",
		Summary: "Create a mailbox", Description: "Forwarding and IMAP mailboxes. Google and Microsoft mailboxes are connected with the OAuth start route.",
		Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *createMailboxInput) (*mailboxBody, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			b := in.Body
			a, err := s.access(ctx, s.d.DB.Pool, p.WorkspaceID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := s.checkReceive(b.InboundKind, a); err != nil {
				return nil, err
			}
			if b.OutboundKind == OutboundSystem && !s.systemSend {
				return nil, systemSendOff()
			}
			if b.InboundKind == KindGoogle || b.InboundKind == KindMicrosoft {
				return nil, httpx.Validation("Connect " + providerLabel(b.InboundKind) + " mailboxes with POST /v1/mailboxes/oauth/" + b.InboundKind + "/start.")
			}
			m := &mailboxRow{WorkspaceID: p.WorkspaceID}
			m.ID = ids.New()
			m.Name, m.Email, m.DisplayName = b.Name, b.Email, b.DisplayName
			m.InboundKind, m.OutboundKind, m.Config, m.TeamID = b.InboundKind, b.OutboundKind, b.Config, b.TeamID
			// The Google or Microsoft connection sets these, never a request.
			m.Config.OAuthEmail, m.Config.OAuthGrant, m.Config.OAuthClient = "", "", ""
			m.SignatureHTML, m.AutoReplyEnabled, m.AutoReplySubject, m.AutoReplyHTML = b.SignatureHTML, b.AutoReplyEnabled, b.AutoReplySubject, b.AutoReplyHTML
			m.Aliases, m.FromNameFormat, m.AliasTeams = b.Aliases, b.FromNameFormat, b.AliasTeams
			m.AliasSettings, _ = mergeAliasSettings(nil, b.AliasSettings)
			if b.BrandID != nil {
				m.BrandID = *b.BrandID
			}
			m.Status = "active"
			creds := credentials{Password: b.Password, SMTPPassword: b.SMTPPassword}
			if err := s.validateMailbox(m, creds); err != nil {
				return nil, err
			}
			if err := s.beforeCreate(ctx, m, &creds, b.Verify); err != nil {
				return nil, err
			}
			sealed, err := s.sealCreds(creds)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if err := checkTeam(ctx, tx, p.WorkspaceID, m.TeamID); err != nil {
					return err
				}
				if err := checkAliasTeams(ctx, tx, m, true); err != nil {
					return err
				}
				if err := checkAddressing(ctx, tx, m); err != nil {
					return err
				}
				if err := checkAliasBrands(ctx, tx, m); err != nil {
					return err
				}
				if err := s.d.Plan.CheckMailbox(ctx, tx, p.WorkspaceID); err != nil {
					return err
				}
				var slug string
				if err := tx.QueryRow(ctx, `SELECT slug FROM workspaces WHERE id = $1`, p.WorkspaceID).Scan(&slug); err != nil {
					return err
				}
				if err := s.insertMailbox(ctx, tx, m, slug, sealed); err != nil {
					return err
				}
				if err := s.publishMailbox(ctx, tx, p.WorkspaceID, m.ID, "mailbox.created"); err != nil {
					return err
				}
				if err := audit.Log(ctx, tx, p, p.WorkspaceID, "mailbox.connected", "mailbox", &m.ID, map[string]any{
					"name": m.Name, "email": m.Email, "inbound_kind": m.InboundKind, "outbound_kind": m.OutboundKind}); err != nil {
					return err
				}
				return s.enqueueSync(ctx, tx, m)
			})
			if err != nil {
				return nil, wrapMailboxErr(err)
			}
			out, err := loadMailbox(ctx, s.d.DB.Pool, p.WorkspaceID, m.ID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &mailboxBody{s.api(out, true, a)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "update-mailbox", Method: http.MethodPatch, Path: "/v1/mailboxes/{id}",
		Summary: "Update a mailbox", Tags: tags},
		func(ctx context.Context, in *updateMailboxInput) (*mailboxBody, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			if pr := in.Body.PersonalReplies; pr != nil {
				if pr.Accounts != "" && pr.Accounts != PersonalAny && pr.Accounts != PersonalShared && pr.Accounts != PersonalDomain {
					return nil, httpx.Validation("personal_replies.accounts must be any, shared or domain")
				}
				if pr.ReplyTo != nil && *pr.ReplyTo != "address" && *pr.ReplyTo != "mailbox" {
					return nil, httpx.Validation("personal_replies.reply_to must be address, mailbox or null")
				}
			}
			// The sign-in check runs before the transaction: it can take as long as a
			// slow server, and no row stays locked meanwhile.
			var verified verifiedPatch
			if in.Body.Verify {
				if verified, err = s.verifyPatch(ctx, p.WorkspaceID, id, in); err != nil {
					return nil, err
				}
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if err := lockWorkspaceForMailboxWrite(ctx, tx, p.WorkspaceID); err != nil {
					return err
				}
				m, err := scanMailbox(tx.QueryRow(ctx, `SELECT `+mailboxColumns+` FROM mailboxes WHERE workspace_id = $1 AND id = $2 FOR UPDATE`, p.WorkspaceID, id))
				if err != nil {
					return err
				}
				creds, err := s.openCreds(m)
				if err != nil {
					return err
				}
				prevInbound, prevOutbound := m.InboundKind, m.OutboundKind
				prevAliasSettings := m.AliasSettings
				changes, resetSync := applyPatch(m, &creds, in)
				if in.Body.AliasSettings != nil {
					merged, claimed := mergeAliasSettings(m.AliasSettings, *in.Body.AliasSettings)
					if len(claimed) > 0 && m.readsSendAs() {
						return httpx.Validation("Gmail's own send-as list decides which aliases this mailbox sends as. Add the address in Gmail, then check again.")
					}
					m.AliasSettings = merged
				}
				if !sameAliasSettings(prevAliasSettings, m.AliasSettings) {
					changes = append(changes, "alias_settings")
				}
				if in.Body.Config.turnsTidyOn() && !m.readsAPI() {
					return httpx.Validation("Gmail and Outlook options are only for a mailbox that reads its inbox through Google or Microsoft.")
				}
				if err := s.checkInboundChange(ctx, tx, p.WorkspaceID, prevInbound, m.InboundKind); err != nil {
					return err
				}
				if err := s.checkOutboundChange(prevOutbound, m.OutboundKind); err != nil {
					return err
				}
				if in.Body.BotID.Set {
					bot, err := bots.ParseRef(ctx, tx, p.WorkspaceID, in.Body.BotID.Value)
					if err != nil {
						return err
					}
					if !bots.Same(bot, m.BotID) {
						m.BotID = bot
						changes = append(changes, "bot_id")
					}
				}
				if len(changes) == 0 {
					return nil
				}
				if err := s.validateMailbox(m, creds); err != nil {
					return err
				}
				prepareMailbox(m, &creds, in.Body.Password != nil || in.Body.SMTPPassword != nil)
				if verified.smtpPort != 0 && m.OutboundKind == OutboundSMTP && strings.EqualFold(m.Config.SMTPHost, verified.smtpHost) {
					m.Config.SMTPPort, m.Config.SMTPTLS = verified.smtpPort, TLSStart
				}
				if err := checkTeam(ctx, tx, p.WorkspaceID, m.TeamID); err != nil {
					return err
				}
				if err := checkAliasTeams(ctx, tx, m, contains(changes, "alias_teams") || contains(changes, "team_id")); err != nil {
					return err
				}
				if err := checkAddressing(ctx, tx, m); err != nil {
					return err
				}
				if err := checkAliasBrands(ctx, tx, m); err != nil {
					return err
				}
				applySendAsList(m)
				if m.AIAnswers && contains(changes, "ai_answers") {
					if err := s.checkAIOn(ctx, tx, p.WorkspaceID); err != nil {
						return err
					}
				}
				sealed, err := s.sealCreds(creds)
				if err != nil {
					return err
				}
				reactivate := in.Body.Status != nil && *in.Body.Status == "active"
				_, err = tx.Exec(ctx, `UPDATE mailboxes SET name = $3, email = $4, display_name = $5, outbound_kind = $6, config = $7,
					inbound_kind = $25,
					credentials_enc = $8, team_id = $9, signature_html = $10, auto_reply_enabled = $11, auto_reply_subject = $12,
					auto_reply_html = $13, status = $14,
					last_error = CASE WHEN $15 THEN '' ELSE last_error END,
					sync_failures = CASE WHEN $15 THEN 0 ELSE sync_failures END,
					next_sync_at = CASE WHEN $15 THEN NULL ELSE next_sync_at END,
					imap_uid_validity = CASE WHEN $16 THEN 0 ELSE imap_uid_validity END,
					imap_last_uid = CASE WHEN $16 THEN 0 ELSE imap_last_uid END,
					api_sync_state = CASE WHEN $16 THEN '{}'::jsonb ELSE api_sync_state END,
					ai_answers = $17, ai_answers_mode = $18, ai_answers_outside_hours = $19,
					brand_id = $20, aliases = $21, from_name_format = $22, trusted_form_senders = $23, bot_id = $24,
					alias_teams = $26, alias_settings = $27, personal_replies = $28,
					updated_at = now() WHERE workspace_id = $1 AND id = $2`,
					p.WorkspaceID, id, m.Name, m.Email, m.DisplayName, m.OutboundKind, m.Config, sealed, m.TeamID,
					m.SignatureHTML, m.AutoReplyEnabled, m.AutoReplySubject, m.AutoReplyHTML, m.Status,
					reactivate || resetSync || contains(changes, "credentials"), resetSync,
					m.AIAnswers, m.AIAnswersMode, m.AIAnswersOutsideHours, m.BrandID, m.Aliases, m.FromNameFormat, m.TrustedFormSenders, m.BotID,
					m.InboundKind, m.AliasTeams, m.AliasSettings, m.PersonalReplies)
				if err != nil {
					return err
				}
				if err := s.publishMailbox(ctx, tx, p.WorkspaceID, id, "mailbox.updated"); err != nil {
					return err
				}
				data := map[string]any{"changes": changes}
				if contains(changes, "brand_id") {
					data["brand_id"] = m.BrandID
				}
				if contains(changes, "aliases") {
					data["aliases"] = m.Aliases
				}
				if contains(changes, "alias_teams") {
					data["alias_teams"] = m.AliasTeams
				}
				if contains(changes, "alias_settings") {
					data["alias_settings"] = aliasAudit(m)
				}
				if contains(changes, "trusted_form_senders") {
					data["trusted_form_senders"] = m.TrustedFormSenders
				}
				if contains(changes, "bot_id") {
					data["bot_id"] = m.BotID
				}
				if contains(changes, "personal_replies") {
					data["personal_replies"] = m.PersonalReplies
				}
				if contains(changes, "provider_options") {
					data["provider_options"] = map[string]any{"mark_read": m.Config.ProviderMarkRead,
						"archive_on_solve": m.Config.ProviderArchiveOnSolve, "label": m.Config.ProviderLabel}
				}
				if contains(changes, "ai_answers") || contains(changes, "ai_answers_mode") || contains(changes, "ai_answers_outside_hours") {
					data["ai_answers"] = map[string]any{"enabled": m.AIAnswers, "mode": m.AIAnswersMode, "outside_hours": m.AIAnswersOutsideHours}
				}
				if contains(changes, "inbound_kind") || contains(changes, "outbound_kind") || contains(changes, "config") || contains(changes, "credentials") {
					// How the mailbox connects; never a password.
					conn := map[string]any{"inbound_kind": m.InboundKind, "outbound_kind": m.OutboundKind, "verified": verified.checked}
					if m.InboundKind == InboundIMAP {
						conn["imap"] = map[string]any{"host": m.Config.IMAPHost, "port": m.Config.IMAPPort, "tls": m.Config.IMAPTLS}
					}
					if m.OutboundKind == OutboundSMTP {
						conn["smtp"] = map[string]any{"host": m.Config.SMTPHost, "port": m.Config.SMTPPort, "tls": m.Config.SMTPTLS}
					}
					data["connection"] = conn
				}
				if err := audit.Log(ctx, tx, p, p.WorkspaceID, "mailbox.updated", "mailbox", &id, data); err != nil {
					return err
				}
				if m.Status == "active" {
					return s.enqueueSync(ctx, tx, m)
				}
				return nil
			})
			if err != nil {
				return nil, wrapMailboxErr(err)
			}
			out, err := loadMailbox(ctx, s.d.DB.Pool, p.WorkspaceID, id)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			a, err := s.access(ctx, s.d.DB.Pool, p.WorkspaceID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &mailboxBody{s.api(out, true, a)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "delete-mailbox", Method: http.MethodDelete, Path: "/v1/mailboxes/{id}",
		Summary: "Delete a mailbox", Description: "Removes the mailbox, its credentials and its stored raw mail. Its tickets stay.",
		Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *idInput) (*struct{}, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			var keys []string
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if err := lockWorkspaceForMailboxWrite(ctx, tx, p.WorkspaceID); err != nil {
					return err
				}
				var name, addr string
				if err := tx.QueryRow(ctx, `SELECT name, email FROM mailboxes WHERE workspace_id = $1 AND id = $2 FOR UPDATE`,
					p.WorkspaceID, id).Scan(&name, &addr); err != nil {
					return err
				}
				rows, err := tx.Query(ctx, `SELECT raw_storage_key FROM inbound_emails WHERE workspace_id = $1 AND mailbox_id = $2 AND raw_storage_key <> ''`, p.WorkspaceID, id)
				if err != nil {
					return err
				}
				if keys, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `DELETE FROM mailboxes WHERE workspace_id = $1 AND id = $2`, p.WorkspaceID, id); err != nil {
					return err
				}
				if _, err := events.Publish(ctx, tx, events.Event{WorkspaceID: p.WorkspaceID, Type: "mailbox.deleted",
					Data: map[string]any{"mailbox_id": id}}); err != nil {
					return err
				}
				return audit.Log(ctx, tx, p, p.WorkspaceID, "mailbox.deleted", "mailbox", &id, map[string]any{"name": name, "email": addr})
			})
			if err != nil {
				return nil, wrapMailboxErr(err)
			}
			go s.deleteObjects(keys)
			return nil, nil
		})

	huma.Register(api, huma.Operation{OperationID: "get-mailbox-usage", Method: http.MethodGet, Path: "/v1/mailboxes/{id}/usage",
		Summary: "Count a mailbox's tickets", Description: "How many tickets the caller can see reply from this mailbox, and how many of them are open, pending or on hold. Deleting the mailbox keeps them, without this reply address.",
		Tags: tags},
		func(ctx context.Context, in *idInput) (*struct{ Body mailboxUsage }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			if _, err := loadMailbox(ctx, s.d.DB.Pool, p.WorkspaceID, id); err != nil {
				return nil, wrapMailboxErr(err)
			}
			hidden, err := domain.HiddenTeams(ctx, s.d.DB.Pool, p)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			var out mailboxUsage
			if err := s.d.DB.Pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE t.status IN ('open', 'pending', 'on_hold'))
				FROM tickets t WHERE t.workspace_id = $1 AND t.mailbox_id = $2 AND t.deleted_at IS NULL AND `+domain.VisibleSQL("t", 3),
				p.WorkspaceID, id, hidden).Scan(&out.Tickets, &out.Open); err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body mailboxUsage }{out}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "test-mailbox", Method: http.MethodPost, Path: "/v1/mailboxes/{id}/test",
		Summary: "Test a mailbox connection", Description: "Checks the IMAP login and the SMTP login or provider token, and names the cause and a fix when one fails. Nothing is sent.",
		Tags: tags},
		func(ctx context.Context, in *idInput) (*mailboxTestOutput, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID)
			if err != nil {
				return nil, err
			}
			m, err := loadMailbox(ctx, s.d.DB.Pool, p.WorkspaceID, id)
			if err != nil {
				return nil, wrapMailboxErr(err)
			}
			return s.testMailbox(ctx, m)
		})
}

func canSeeConfig(p *auth.Principal) bool {
	if p.Kind == auth.KindAPIKey {
		return true // settings:read
	}
	return p.Role.AtLeast(auth.RoleAdmin)
}

func nullUUID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func wrapMailboxErr(err error) error {
	switch {
	case db.IsUniqueViolation(err, "mailboxes_workspace_id_email_key"):
		return httpx.Conflict("A mailbox with that address already exists in this workspace.")
	case db.IsNotFound(err):
		return httpx.NotFound("Mailbox")
	}
	return httpx.Wrap(err)
}

func checkTeam(ctx context.Context, q db.Querier, wid uuid.UUID, team *uuid.UUID) error {
	if team == nil {
		return nil
	}
	var ok bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM teams WHERE workspace_id = $1 AND id = $2)`, wid, *team).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return httpx.Validation("team_id does not exist in this workspace")
	}
	return nil
}

// checkAliasTeams checks that the aliases' teams are teams of the workspace. A private team's
// address delivers every alias to that team, so a mailbox of a private team takes no alias
// teams; strict is false when neither its team nor its alias teams changed.
func checkAliasTeams(ctx context.Context, q db.Querier, m *mailboxRow, strict bool) error {
	if len(m.AliasTeams) == 0 || !strict {
		return nil
	}
	teams := make([]uuid.UUID, 0, len(m.AliasTeams))
	for _, t := range m.AliasTeams {
		if !slices.Contains(teams, t) {
			teams = append(teams, t)
		}
	}
	var found int
	var private bool
	if err := q.QueryRow(ctx, `SELECT (SELECT count(*) FROM teams WHERE workspace_id = $1 AND id = ANY($2)),
		EXISTS (SELECT 1 FROM teams WHERE workspace_id = $1 AND id = $3 AND private)`,
		m.WorkspaceID, teams, m.TeamID).Scan(&found, &private); err != nil {
		return err
	}
	if found != len(teams) {
		return httpx.Validation("alias_teams names a team that does not exist in this workspace")
	}
	if private {
		return httpx.Validation("A private team's address delivers all its aliases to that team.")
	}
	return nil
}

// cleanAliasTeams lowercases the aliases of alias_teams; each must be one of aliases.
func cleanAliasTeams(in map[string]uuid.UUID, aliases []string) (map[string]uuid.UUID, error) {
	out := make(map[string]uuid.UUID, len(in))
	for a, team := range in {
		a = strings.ToLower(strings.TrimSpace(a))
		if !slices.Contains(aliases, a) {
			if len(a) > 80 {
				a = a[:80]
			}
			return nil, httpx.Validation("alias_teams: " + a + " is not an alias of this mailbox")
		}
		out[a] = team
	}
	return out, nil
}

func sameAliasTeams(a, b map[string]uuid.UUID) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[strings.ToLower(strings.TrimSpace(k))]; !ok || w != v {
			return false
		}
	}
	return true
}

// checkAddressing checks what a mailbox shares with the rest of the workspace: its brand is
// one of the workspace's (unset: the default brand), and no other mailbox uses one of its
// aliases or has its address as an alias.
func checkAddressing(ctx context.Context, tx pgx.Tx, m *mailboxRow) error {
	if m.BrandID == uuid.Nil {
		if err := tx.QueryRow(ctx, `SELECT id FROM brands WHERE workspace_id = $1 AND is_default`, m.WorkspaceID).Scan(&m.BrandID); err != nil {
			return fmt.Errorf("default brand: %w", err)
		}
	} else {
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM brands WHERE workspace_id = $1 AND id = $2)`, m.WorkspaceID, m.BrandID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return httpx.Validation("brand_id is not a brand of this workspace")
		}
	}
	all := append([]string{strings.ToLower(m.Email)}, m.Aliases...)
	var taken bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM mailboxes WHERE workspace_id = $1 AND id <> $2
		AND (lower(email::text) = ANY($3) OR aliases && $4))`, m.WorkspaceID, m.ID, m.Aliases, all).Scan(&taken); err != nil {
		return err
	}
	if taken {
		return httpx.Validation("Another mailbox in this workspace already uses one of these addresses.")
	}
	return nil
}

func sameAliases(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(strings.TrimSpace(a[i]), b[i]) {
			return false
		}
	}
	return true
}

// insertMailbox inserts with a fresh forwarding token, retrying on the rare collision.
func (s *service) insertMailbox(ctx context.Context, tx pgx.Tx, m *mailboxRow, slug string, sealed []byte) error {
	if m.BrandID == uuid.Nil {
		// A new address belongs to the default brand unless another was chosen.
		if err := tx.QueryRow(ctx, `SELECT id FROM brands WHERE workspace_id = $1 AND is_default`, m.WorkspaceID).Scan(&m.BrandID); err != nil {
			return fmt.Errorf("default brand: %w", err)
		}
	}
	if m.Aliases == nil {
		m.Aliases = []string{}
	}
	if m.AliasTeams == nil {
		m.AliasTeams = map[string]uuid.UUID{}
	}
	if m.AliasSettings == nil {
		m.AliasSettings = map[string]AliasSettings{}
	}
	if m.FromNameFormat == "" {
		m.FromNameFormat = defaultFromNameFormat
	}
	for attempt := 0; ; attempt++ {
		m.ForwardingToken = newForwardingToken(slug)
		_, err := tx.Exec(ctx, `SAVEPOINT mbx_insert`)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO mailboxes (id, workspace_id, name, email, display_name, inbound_kind, forwarding_token,
			outbound_kind, config, credentials_enc, team_id, signature_html, auto_reply_enabled, auto_reply_subject, auto_reply_html, status,
			brand_id, aliases, from_name_format, alias_teams, alias_settings)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)`,
			m.ID, m.WorkspaceID, m.Name, m.Email, m.DisplayName, m.InboundKind, m.ForwardingToken, m.OutboundKind, m.Config,
			sealed, m.TeamID, m.SignatureHTML, m.AutoReplyEnabled, m.AutoReplySubject, m.AutoReplyHTML, m.Status,
			m.BrandID, m.Aliases, m.FromNameFormat, m.AliasTeams, m.AliasSettings)
		if err == nil {
			_, err = tx.Exec(ctx, `RELEASE SAVEPOINT mbx_insert`)
			return err
		}
		if !db.IsUniqueViolation(err, "mailboxes_forwarding_token_key") || attempt >= 4 {
			return err
		}
		if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT mbx_insert`); err != nil {
			return err
		}
	}
}

// enqueueSync queues an immediate sync for a mailbox that is polled: one that reads
// through the provider's API, or over IMAP while that is on for its workspace.
func (s *service) enqueueSync(ctx context.Context, tx pgx.Tx, m *mailboxRow) error {
	if !m.polled() || m.Status == "disabled" || s.d.Jobs == nil {
		return nil
	}
	if m.readsIMAP() && !s.mailboxIMAP {
		a, err := s.access(ctx, tx, m.WorkspaceID)
		if err != nil {
			return err
		}
		if !s.imapReceives(m, a) {
			return nil
		}
	}
	_, err := s.d.Jobs.InsertTx(ctx, tx, jobs.SyncMailboxArgs{MailboxID: m.ID}, syncInsertOpts())
	return err
}

func syncInsertOpts() *river.InsertOpts {
	// Completed syncs must not block the next poll. ByQueue also versions the
	// uniqueness key so completed jobs created with the old defaults stop blocking
	// syncs immediately after an upgrade.
	return &river.InsertOpts{Queue: jobs.QueueEmail, MaxAttempts: 1, UniqueOpts: river.UniqueOpts{
		ByArgs: true, ByQueue: true,
		ByState: []rivertype.JobState{
			rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRetryable,
			rivertype.JobStateRunning, rivertype.JobStateScheduled,
		},
	}}
}

// applyPatch merges a PATCH into m and creds and returns the changed field names and
// whether the IMAP cursor must restart (a different server or account).
func applyPatch(m *mailboxRow, creds *credentials, in *updateMailboxInput) (changes []string, resetSync bool) {
	b := in.Body
	set := func(name string, dst *string, v *string) {
		if v != nil && *v != *dst {
			*dst = *v
			changes = append(changes, name)
		}
	}
	set("name", &m.Name, b.Name)
	if b.Email != nil && !strings.EqualFold(*b.Email, m.Email) {
		m.Email = *b.Email
		changes = append(changes, "email")
	}
	set("display_name", &m.DisplayName, b.DisplayName)
	prevInbound, prevOutbound, prevProvider := m.InboundKind, m.OutboundKind, m.oauthProvider()
	set("inbound_kind", &m.InboundKind, b.InboundKind)
	if m.InboundKind != prevInbound {
		resetSync = true
		if m.InboundKind == InboundForwarding && m.Status == "error" {
			m.Status = "active" // the error was the IMAP sync's
		}
	}
	set("outbound_kind", &m.OutboundKind, b.OutboundKind)
	set("signature_html", &m.SignatureHTML, b.SignatureHTML)
	set("auto_reply_subject", &m.AutoReplySubject, b.AutoReplySubject)
	set("auto_reply_html", &m.AutoReplyHTML, b.AutoReplyHTML)
	set("status", &m.Status, b.Status)
	if b.AutoReplyEnabled != nil && *b.AutoReplyEnabled != m.AutoReplyEnabled {
		m.AutoReplyEnabled = *b.AutoReplyEnabled
		changes = append(changes, "auto_reply_enabled")
	}
	if b.AIAnswers != nil && *b.AIAnswers != m.AIAnswers {
		m.AIAnswers = *b.AIAnswers
		changes = append(changes, "ai_answers")
	}
	if b.AIAnswersMode != nil && (*b.AIAnswersMode == "draft" || *b.AIAnswersMode == "send") {
		set("ai_answers_mode", &m.AIAnswersMode, b.AIAnswersMode)
	}
	if b.AIAnswersOutsideHours != nil && *b.AIAnswersOutsideHours != m.AIAnswersOutsideHours {
		m.AIAnswersOutsideHours = *b.AIAnswersOutsideHours
		changes = append(changes, "ai_answers_outside_hours")
	}
	if b.TeamID != nil {
		var next *uuid.UUID
		if *b.TeamID != "" {
			if id, ok := ids.Parse(*b.TeamID); ok {
				next = &id
			} else {
				next = &uuid.Nil // refused by checkTeam
			}
		}
		if (next == nil) != (m.TeamID == nil) || next != nil && m.TeamID != nil && *next != *m.TeamID {
			m.TeamID = next
			changes = append(changes, "team_id")
		}
	}
	if b.BrandID != nil {
		next := uuid.Max // not a brand: refused by checkAddressing
		if id, ok := ids.Parse(*b.BrandID); ok {
			next = id
		}
		if next != m.BrandID {
			m.BrandID = next
			changes = append(changes, "brand_id")
		}
	}
	if b.Aliases != nil && !sameAliases(*b.Aliases, m.Aliases) {
		m.Aliases = *b.Aliases
		changes = append(changes, "aliases")
	}
	if b.AliasTeams != nil && !sameAliasTeams(*b.AliasTeams, m.AliasTeams) {
		m.AliasTeams = *b.AliasTeams
		changes = append(changes, "alias_teams")
	} else if b.AliasTeams == nil && b.Aliases != nil {
		// A removed alias takes its team with it.
		kept := map[string]uuid.UUID{}
		for a, team := range m.AliasTeams {
			for _, x := range m.Aliases {
				if strings.EqualFold(strings.TrimSpace(x), a) {
					kept[a] = team
				}
			}
		}
		if len(kept) != len(m.AliasTeams) {
			m.AliasTeams = kept
			changes = append(changes, "alias_teams")
		}
	}
	if b.Aliases != nil {
		// A removed alias takes its settings with it.
		keepAliasSettings(m)
	}
	if b.PersonalReplies != nil {
		in := b.PersonalReplies
		if next := (PersonalReplies{Enabled: in.Enabled, ReplyTo: in.ReplyTo, Accounts: in.Accounts}).Normalized(); !samePersonalReplies(next, m.PersonalReplies) {
			m.PersonalReplies = next
			changes = append(changes, "personal_replies")
		}
	}
	if b.TrustedFormSenders != nil && !sameAliases(*b.TrustedFormSenders, m.TrustedFormSenders) {
		m.TrustedFormSenders = *b.TrustedFormSenders
		changes = append(changes, "trusted_form_senders")
	}
	if b.FromNameFormat != nil {
		v := *b.FromNameFormat
		if strings.TrimSpace(v) == "" {
			v = defaultFromNameFormat
		}
		set("from_name_format", &m.FromNameFormat, &v)
	}
	if c := b.Config; c != nil {
		before := m.Config
		cfg := &m.Config
		setS := func(dst *string, v *string) {
			if v != nil {
				*dst = strings.TrimSpace(*v)
			}
		}
		setI := func(dst *int, v *int) {
			if v != nil {
				*dst = *v
			}
		}
		setS(&cfg.IMAPHost, c.IMAPHost)
		setI(&cfg.IMAPPort, c.IMAPPort)
		setS(&cfg.IMAPUsername, c.IMAPUsername)
		setS(&cfg.IMAPTLS, c.IMAPTLS)
		setS(&cfg.SMTPHost, c.SMTPHost)
		setI(&cfg.SMTPPort, c.SMTPPort)
		setS(&cfg.SMTPUsername, c.SMTPUsername)
		setS(&cfg.SMTPTLS, c.SMTPTLS)
		setS(&cfg.SyncStart, c.SyncStart)
		if *cfg != before {
			changes = append(changes, "config")
			if !strings.EqualFold(cfg.IMAPHost, before.IMAPHost) || !strings.EqualFold(cfg.IMAPUsername, before.IMAPUsername) {
				resetSync = true
			}
		}
		// The tidy-up options are settings of their own, not the connection's.
		setB := func(dst *bool, v *bool) {
			if v != nil {
				*dst = *v
			}
		}
		setB(&cfg.ProviderMarkRead, c.ProviderMarkRead)
		setB(&cfg.ProviderArchiveOnSolve, c.ProviderArchiveOnSolve)
		setB(&cfg.ProviderLabel, c.ProviderLabel)
		if cfg.ProviderMarkRead != before.ProviderMarkRead || cfg.ProviderArchiveOnSolve != before.ProviderArchiveOnSolve ||
			cfg.ProviderLabel != before.ProviderLabel {
			changes = append(changes, "provider_options")
		}
	}
	credsChanged := false
	if prevInbound == InboundIMAP && m.InboundKind != InboundIMAP && creds.Password != "" {
		// The IMAP password is not kept once mail stops being read over IMAP; when it
		// also signed in to SMTP, it stays as the SMTP password.
		if m.OutboundKind == OutboundSMTP && creds.SMTPPassword == "" {
			creds.SMTPPassword = creds.Password
		}
		creds.Password, credsChanged = "", true
	}
	if prevProvider != "" && m.oauthProvider() != prevProvider {
		// Nor is a Google or Microsoft grant once neither side uses it.
		if creds.RefreshToken != "" || creds.AccessToken != "" || creds.GraphAccessToken != "" {
			creds.RefreshToken, creds.AccessToken, creds.Expiry = "", "", time.Time{}
			creds.GraphAccessToken, creds.GraphExpiry = "", time.Time{}
			credsChanged = true
		}
		m.Config.OAuthEmail, m.Config.OAuthGrant, m.Config.OAuthClient = "", "", ""
		// Nor what that provider said about sending as each alias.
		m.AliasSettings = forgetProviderSendAs(m.AliasSettings)
	} else if prevProvider != "" && m.Config.OAuthGrant == "" {
		m.Config.OAuthGrant = GrantSync // stored before grants were recorded
	}
	if prevOutbound == OutboundSMTP && m.OutboundKind != OutboundSMTP {
		// The SMTP password is not kept once replies stop going through that server.
		if creds.SMTPPassword != "" {
			creds.SMTPPassword, credsChanged = "", true
		}
		if m.InboundKind == InboundForwarding && creds.Password != "" {
			creds.Password, credsChanged = "", true
		}
	}
	if b.Password != nil && *b.Password != creds.Password {
		creds.Password = *b.Password
		credsChanged = true
	}
	if b.SMTPPassword != nil && *b.SMTPPassword != creds.SMTPPassword {
		creds.SMTPPassword = *b.SMTPPassword
		credsChanged = true
	}
	if credsChanged {
		changes = append(changes, "credentials")
	}
	if b.Status != nil && *b.Status == "active" && !contains(changes, "status") {
		// Re-enabling an errored mailbox clears its error and retries now.
		changes = append(changes, "status")
	}
	return changes, resetSync
}

// validateMailbox normalizes and checks a mailbox before it is stored.
func (s *service) validateMailbox(m *mailboxRow, creds credentials) error {
	name, err := displayname.Validate("Name", m.Name, displayname.Workspace, false)
	if err != nil {
		return httpx.Wrap(err)
	}
	m.Name = name
	if m.DisplayName, err = displayname.Validate("Display name", m.DisplayName, displayname.Workspace, true); err != nil {
		return httpx.Wrap(err)
	}
	m.Email = strings.ToLower(strings.TrimSpace(m.Email))
	if !validAddress(m.Email) {
		return httpx.Validation("Enter a valid email address.")
	}
	if strings.EqualFold(emailDomain(m.Email), s.inboundDomain()) {
		return httpx.Validation("The mailbox address cannot be on the inbound forwarding domain.")
	}
	m.SignatureHTML = sanitizeSettingsHTML(m.SignatureHTML)
	m.AutoReplyHTML = sanitizeSettingsHTML(m.AutoReplyHTML)
	if len(m.SignatureHTML) > maxSettingsHTML || len(m.AutoReplyHTML) > maxSettingsHTML {
		return httpx.Validation("Signature and auto-reply are limited to 64 KB.")
	}
	m.AutoReplySubject = strings.TrimSpace(strings.Map(dropControl, m.AutoReplySubject))
	for _, t := range []struct{ field, src, kind string }{
		{"signature_html", m.SignatureHTML, templates.KindSignature},
		{"auto_reply_html", m.AutoReplyHTML, templates.KindAutoReply},
		{"auto_reply_subject", m.AutoReplySubject, templates.KindSubject},
	} {
		if err := templates.CheckSaved(t.field, t.src, t.kind, nil); err != nil {
			return err
		}
	}
	if m.Aliases, err = s.cleanAliases(m.Email, m.Aliases); err != nil {
		return err
	}
	if m.AliasTeams, err = cleanAliasTeams(m.AliasTeams, m.Aliases); err != nil {
		return err
	}
	if err := cleanAliasSettings(m); err != nil {
		return err
	}
	if m.FromNameFormat, err = cleanFromNameFormat(m.FromNameFormat); err != nil {
		return err
	}
	if m.TrustedFormSenders, err = cleanFormSenders(m.TrustedFormSenders, append([]string{m.Email}, m.Aliases...)); err != nil {
		return err
	}
	cfg := &m.Config
	if !m.readsAPI() {
		// Only a mailbox that reads through the provider's API tidies up there.
		*cfg = cfg.withoutTidy()
	}

	switch m.InboundKind {
	case InboundForwarding:
		cfg.IMAPHost, cfg.IMAPPort, cfg.IMAPUsername, cfg.IMAPTLS = "", 0, "", ""
	case InboundIMAP:
		if err := s.normalizeEndpoint(&cfg.IMAPHost, &cfg.IMAPPort, &cfg.IMAPTLS, "IMAP", 993, 143); err != nil {
			return err
		}
		if cfg.IMAPUsername = strings.TrimSpace(cfg.IMAPUsername); cfg.IMAPUsername == "" {
			cfg.IMAPUsername = m.Email
		}
		if creds.Password == "" {
			return httpx.Validation("Enter the IMAP password.")
		}
	case KindGoogle, KindMicrosoft:
		if cfg.OAuthGrant == GrantAPI {
			// It reads through the provider's API: no IMAP server.
			cfg.IMAPHost, cfg.IMAPPort, cfg.IMAPUsername, cfg.IMAPTLS = "", 0, "", ""
			break
		}
		// OAuth tokens only ever go to the provider's own server.
		cfg.IMAPHost, cfg.IMAPPort, cfg.IMAPTLS = s.providers[m.InboundKind].imapHost, 993, TLSImplicit
		if cfg.IMAPUsername == "" {
			cfg.IMAPUsername = cfg.OAuthEmail
		}
	}
	switch m.OutboundKind {
	case OutboundSMTP:
		if err := s.normalizeEndpoint(&cfg.SMTPHost, &cfg.SMTPPort, &cfg.SMTPTLS, "SMTP", 465, 587); err != nil {
			return err
		}
		if cfg.SMTPUsername = strings.TrimSpace(cfg.SMTPUsername); cfg.SMTPUsername == "" {
			cfg.SMTPUsername = m.Email
		}
		if creds.smtpPassword() == "" {
			return httpx.Validation("Enter the SMTP password.")
		}
	case KindGoogle, KindMicrosoft:
		// Only a mailbox that holds the provider's grant sends through it.
		if m.oauthProvider() != m.OutboundKind || creds.RefreshToken == "" {
			return httpx.Validation("Sending through " + providerLabel(m.OutboundKind) + " needs a mailbox connected with " + providerLabel(m.OutboundKind) + ".")
		}
		cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUsername, cfg.SMTPTLS = "", 0, "", ""
	case OutboundSystem:
		cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUsername, cfg.SMTPTLS = "", 0, "", ""
	default:
		return httpx.Validation("Unknown outbound kind.")
	}
	if creds.RefreshToken == "" {
		cfg.OAuthGrant, cfg.OAuthClient = "", ""
	}
	return nil
}

// checkInboundChange refuses a change of how a mailbox receives that a PATCH cannot
// make: to IMAP while that is off for the workspace, or onto a Google or Microsoft
// sign-in, which only the OAuth start route connects.
func (s *service) checkInboundChange(ctx context.Context, q db.Querier, wid uuid.UUID, prev, next string) error {
	if next == prev {
		return nil
	}
	if receivesIMAP(next) {
		a, err := s.access(ctx, q, wid)
		if err != nil {
			return err
		}
		if err := s.checkReceive(next, a); err != nil {
			return err
		}
	}
	if next == KindGoogle || next == KindMicrosoft {
		return httpx.Validation("Connect " + providerLabel(next) + " mailboxes with POST /v1/mailboxes/oauth/" + next + "/start.")
	}
	return nil
}

// checkReceive refuses an inbound kind that reads over IMAP while that is off for the
// workspace: generic IMAP per workspace, Google and Microsoft per instance.
func (s *service) checkReceive(kind string, a access) error {
	switch {
	case kind == InboundIMAP && !s.imapAllowed(a):
		return imapOff()
	case (kind == KindGoogle || kind == KindMicrosoft) && !s.mailboxIMAP:
		return imapOff()
	}
	return nil
}

// checkOutboundChange refuses a move to sending through the platform mailer while this
// instance has it off. A mailbox that already sends through it keeps it until it moves.
func (s *service) checkOutboundChange(prev, next string) error {
	if next != prev && next == OutboundSystem && !s.systemSend {
		return systemSendOff()
	}
	return nil
}

// normalizeEndpoint defaults the port and TLS mode and checks the host.
func (s *service) normalizeEndpoint(host *string, port *int, mode *string, label string, tlsPort, startPort int) error {
	*host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(*host), "."))
	if *host == "" {
		return httpx.Validation("Enter the " + label + " server host.")
	}
	if err := s.guard.validHost(*host); err != nil {
		if errors.Is(err, errBlockedAddress) {
			return httpx.Validation("The " + label + " server must be a public address.")
		}
		return httpx.Validation(label + " host: " + err.Error())
	}
	switch *mode {
	case "":
		switch *port {
		case 0, tlsPort:
			*mode = TLSImplicit
		default:
			*mode = TLSStart
		}
	case TLSImplicit, TLSStart:
	case TLSNone:
		if !s.guard.allowPrivate {
			return httpx.Validation("The " + label + " connection must use TLS.")
		}
	default:
		return httpx.Validation("Unknown " + label + " TLS mode.")
	}
	if *port == 0 {
		if *mode == TLSImplicit {
			*port = tlsPort
		} else {
			*port = startPort
		}
	}
	if *port < 1 || *port > 65535 {
		return httpx.Validation("The " + label + " port must be between 1 and 65535.")
	}
	return nil
}

func validAddress(s string) bool {
	if s == "" || len(s) > 254 || strings.ContainsAny(s, " \t\r\n<>\"") {
		return false
	}
	a, err := mail.ParseAddress(s)
	return err == nil && strings.EqualFold(a.Address, s) && strings.Contains(emailDomain(s), ".")
}

func dropControl(r rune) rune {
	if r < 0x20 || r == 0x7f {
		return -1
	}
	return r
}

func (s *service) deleteObjects(keys []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	for _, k := range keys {
		if err := s.d.Storage.Delete(ctx, k); err != nil {
			s.log.Warn("delete stored raw mail", "err", err)
		}
	}
}
