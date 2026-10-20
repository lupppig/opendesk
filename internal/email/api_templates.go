package email

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/mailer"
	"github.com/useopendesk/opendesk/internal/templates"
)

// Templates and email design (contracts/templates.md).

const (
	maxTemplateHTML = 128 << 10
	maxSubject      = 200
	testsPerHour    = 10
)

// TemplateCatalog is the variable list.
type TemplateCatalog struct {
	Variables []templates.Variable   `json:"variables"`
	Slots     []templates.Slot       `json:"slots"`
	Filters   []templates.FilterSpec `json:"filters"`
	Tags      []templates.TagSpec    `json:"tags"`
}

// EmailTemplates are a brand's templates.
type EmailTemplates struct {
	Layout templates.EmailTemplate `json:"layout"`
	CSAT   templates.EmailTemplate `json:"csat"`
}

type previewBody struct {
	Kind     string                   `json:"kind" enum:"reply,signature,personal_signature,auto_reply,layout,csat"`
	BodyHTML string                   `json:"body_html,omitempty"`
	Subject  string                   `json:"subject,omitempty" maxLength:"200"`
	Options  *templates.LayoutOptions `json:"options,omitempty" doc:"Layout options to preview (kind layout)"`
	BrandID  string                   `json:"brand_id,omitempty"`
	TeamID   string                   `json:"team_id,omitempty"`
	// MailboxID is the sending address whose signature and brand apply.
	MailboxID string `json:"mailbox_id,omitempty"`
	TicketID  string `json:"ticket_id,omitempty" doc:"Fill with this ticket's values instead of samples"`
}

// TemplatePreview is a rendered email.
type TemplatePreview struct {
	Subject  string              `json:"subject"`
	HTML     string              `json:"html"`
	Text     string              `json:"text"`
	Problems []templates.Problem `json:"problems"`
}

type saveTemplateBody struct {
	Subject  *string                  `json:"subject,omitempty" maxLength:"200"`
	BodyHTML *string                  `json:"body_html,omitempty"`
	Options  *templates.LayoutOptions `json:"options,omitempty"`
}

func (s *service) registerTemplateRoutes(api huma.API) {
	tags := []string{"Templates"}

	huma.Register(api, huma.Operation{OperationID: "list-template-variables", Method: http.MethodGet, Path: "/v1/templates/variables",
		Summary: "List template variables", Tags: tags,
		Description: "Every variable templates take, with the workspace's custom fields, the slots of layouts and CSAT emails, and the filters."},
		func(ctx context.Context, _ *struct{}) (*struct{ Body TemplateCatalog }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			vars := append([]templates.Variable{}, templates.Variables...)
			rows, err := s.d.DB.Pool.Query(ctx, `SELECT entity, key, name, description, type = 'multi_select' FROM custom_fields
				WHERE workspace_id = $1 AND NOT archived ORDER BY entity, position, key`, p.WorkspaceID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			fields, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (templates.Variable, error) {
				var v templates.Variable
				var entity, key string
				err := r.Scan(&entity, &key, &v.Label, &v.Description, &v.List)
				v.Key, v.Group = entity+".fields."+key, entity
				if strings.TrimSpace(v.Description) == "" {
					v.Description = "A custom field."
				}
				return v, err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			vars = append(vars, fields...)
			return &struct{ Body TemplateCatalog }{TemplateCatalog{Variables: vars, Slots: templates.Slots, Filters: templates.Filters, Tags: templates.Tags}}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "get-email-templates", Method: http.MethodGet, Path: "/v1/email-templates/{brand_id}",
		Summary: "Get a brand's email templates", Tags: tags},
		func(ctx context.Context, in *struct {
			BrandID string `path:"brand_id"`
		}) (*struct{ Body EmailTemplates }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeSettingsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			brand, err := s.workspaceBrand(ctx, p.WorkspaceID, in.BrandID)
			if err != nil {
				return nil, err
			}
			layout, csat, err := templates.LoadTemplates(ctx, s.d.DB.Pool, p.WorkspaceID, brand)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body EmailTemplates }{EmailTemplates{Layout: layout, CSAT: csat}}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "save-email-template", Method: http.MethodPut, Path: "/v1/email-templates/{brand_id}/{kind}",
		Summary: "Save a brand's email layout or CSAT email", Tags: tags},
		func(ctx context.Context, in *struct {
			BrandID string `path:"brand_id"`
			Kind    string `path:"kind" enum:"layout,csat"`
			Body    saveTemplateBody
		}) (*struct{ Body templates.EmailTemplate }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			brand, err := s.workspaceBrand(ctx, p.WorkspaceID, in.BrandID)
			if err != nil {
				return nil, err
			}
			fields, err := templates.FieldVariables(ctx, s.d.DB.Pool, p.WorkspaceID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			layout, csat, err := templates.LoadTemplates(ctx, s.d.DB.Pool, p.WorkspaceID, brand)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			var subject, body string
			var options []byte
			switch in.Kind {
			case templates.KindLayout:
				o, custom := layout.Options, layout.BodyHTML
				if in.Body.Options != nil {
					o = *in.Body.Options
				}
				if in.Body.BodyHTML != nil {
					custom = *in.Body.BodyHTML
				}
				o, custom, probs := templates.CleanLayout(o, custom, fields)
				if err := templates.Refuse("body_html", probs); err != nil {
					return nil, err
				}
				body = custom
				if options, err = json.Marshal(o); err != nil {
					return nil, httpx.Wrap(err)
				}
			case templates.KindCSAT:
				subject, body = csat.Subject, csat.BodyHTML
				if in.Body.Subject != nil {
					subject = strings.Join(strings.Fields(*in.Body.Subject), " ")
				}
				if in.Body.BodyHTML != nil {
					if len(*in.Body.BodyHTML) > maxTemplateHTML {
						return nil, httpx.Validation("The email must be 128 KB or less.")
					}
					body = templates.SanitizeDesign(*in.Body.BodyHTML)
				}
				if subject == "" {
					subject = templates.DefaultCSATSubject
				}
				if utf8.RuneCountInString(subject) > maxSubject {
					return nil, httpx.Validation("The subject must be 200 characters or less.")
				}
				if err := templates.CheckSaved("subject", subject, templates.KindSubject, fields); err != nil {
					return nil, err
				}
				if err := templates.CheckSaved("body_html", body, templates.KindCSAT, fields); err != nil {
					return nil, err
				}
				options = []byte("{}")
			default:
				return nil, httpx.NotFound("Template")
			}
			var out templates.EmailTemplate
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, `INSERT INTO email_templates (workspace_id, brand_id, kind, subject, body_html, options, updated_by)
					VALUES ($1, $2, $3, $4, $5, $6, $7)
					ON CONFLICT (workspace_id, brand_id, kind) DO UPDATE SET subject = EXCLUDED.subject, body_html = EXCLUDED.body_html,
						options = EXCLUDED.options, updated_by = EXCLUDED.updated_by, updated_at = now()`,
					p.WorkspaceID, brand, in.Kind, subject, body, options, p.ActorUserID()); err != nil {
					return err
				}
				l, c, err := templates.LoadTemplates(ctx, tx, p.WorkspaceID, brand)
				if err != nil {
					return err
				}
				out = l
				if in.Kind == templates.KindCSAT {
					out = c
				}
				return publishTemplate(ctx, tx, p.WorkspaceID, brand, in.Kind)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body templates.EmailTemplate }{out}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "reset-email-template", Method: http.MethodDelete, Path: "/v1/email-templates/{brand_id}/{kind}",
		Summary: "Go back to the default email layout or CSAT email", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			BrandID string `path:"brand_id"`
			Kind    string `path:"kind" enum:"layout,csat"`
		}) (*struct{}, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAdmin, auth.ScopeSettingsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			brand, err := s.workspaceBrand(ctx, p.WorkspaceID, in.BrandID)
			if err != nil {
				return nil, err
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				tag, err := tx.Exec(ctx, `DELETE FROM email_templates WHERE workspace_id = $1 AND brand_id = $2 AND kind = $3`, p.WorkspaceID, brand, in.Kind)
				if err != nil || tag.RowsAffected() == 0 {
					return err
				}
				return publishTemplate(ctx, tx, p.WorkspaceID, brand, in.Kind)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return nil, nil
		})

	huma.Register(api, huma.Operation{OperationID: "preview-template", Method: http.MethodPost, Path: "/v1/templates/preview",
		Summary: "Preview a template as the customer sees it", Tags: tags,
		Description: "Renders without saving: a signature or auto-reply inside its brand's layout, a layout or CSAT email around sample content."},
		func(ctx context.Context, in *struct{ Body previewBody }) (*struct{ Body TemplatePreview }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeTicketsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out, err := s.preview(ctx, p, in.Body)
			if err != nil {
				return nil, err
			}
			return &struct{ Body TemplatePreview }{*out}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "test-template", Method: http.MethodPost, Path: "/v1/templates/test",
		Summary: "Send a template to yourself", Tags: tags,
		Description: "Sends the preview to your own login address, subject prefixed [Test]. 10 an hour."},
		func(ctx context.Context, in *struct{ Body previewBody }) (*struct{ Body TemplatePreview }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeTicketsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			uid := p.ActorUserID()
			if uid == nil || p.ActorAPIKeyID() != nil {
				return nil, httpx.Forbidden()
			}
			if !s.d.Limiter.Allow("template-test:"+uid.String(), testsPerHour, time.Hour) {
				return nil, httpx.RateLimited()
			}
			out, err := s.preview(ctx, p, in.Body)
			if err != nil {
				return nil, err
			}
			var to string
			if err := s.d.DB.Pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, *uid).Scan(&to); err != nil {
				return nil, httpx.Wrap(err)
			}
			if err := s.d.Mailer.Send(ctx, mailer.Mail{To: to, Subject: "[Test] " + out.Subject, HTML: out.HTML, Text: out.Text}); err != nil {
				s.log.Warn("template test not sent", "workspace_id", p.WorkspaceID, "error", err)
				return nil, httpx.E(502, "send_failed", "The test email could not be sent. Try again in a moment.")
			}
			return &struct{ Body TemplatePreview }{*out}, nil
		})
}

func publishTemplate(ctx context.Context, tx pgx.Tx, wid, brand uuid.UUID, kind string) error {
	_, err := events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: "email_template.updated",
		Data: map[string]any{"brand_id": brand, "kind": kind}})
	return err
}

// workspaceBrand parses a brand id of the workspace.
func (s *service) workspaceBrand(ctx context.Context, wid uuid.UUID, raw string) (uuid.UUID, error) {
	id, ok := ids.Parse(raw)
	if !ok {
		return uuid.Nil, httpx.NotFound("Brand")
	}
	var found bool
	if err := s.d.DB.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM brands WHERE workspace_id = $1 AND id = $2)`, wid, id).Scan(&found); err != nil {
		return uuid.Nil, httpx.Wrap(err)
	}
	if !found {
		return uuid.Nil, httpx.NotFound("Brand")
	}
	return id, nil
}

// optionalRef parses an optional id that must name a row of table in the workspace.
func (s *service) optionalRef(ctx context.Context, wid uuid.UUID, raw, table, what string) (*uuid.UUID, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	id, ok := ids.Parse(raw)
	if !ok {
		return nil, httpx.NotFound(what)
	}
	var found bool
	if err := s.d.DB.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+table+` WHERE workspace_id = $1 AND id = $2)`, wid, id).Scan(&found); err != nil {
		return nil, httpx.Wrap(err)
	}
	if !found {
		return nil, httpx.NotFound(what)
	}
	return &id, nil
}

const sampleContent = `<p>Hi {{ contact.first_name | default: "there" }},</p>` +
	`<p>Thanks for getting in touch. This is how your emails look to customers: your layout, signature and footer around each message.</p>` +
	`<p>Let us know if there is anything else we can do.</p>`

// preview renders a template as the customer would get it.
func (s *service) preview(ctx context.Context, p *auth.Principal, b previewBody) (*TemplatePreview, error) {
	wid := p.WorkspaceID
	if len(b.BodyHTML) > maxTemplateHTML {
		return nil, httpx.Validation("The template must be 128 KB or less.")
	}
	ref := templates.Ref{WorkspaceID: wid, AuthorUserID: p.ActorUserID(), Sample: true}
	var err error
	if ref.BrandID, err = s.optionalRef(ctx, wid, b.BrandID, "brands", "Brand"); err != nil {
		return nil, err
	}
	if ref.TeamID, err = s.optionalRef(ctx, wid, b.TeamID, "teams", "Team"); err != nil {
		return nil, err
	}
	if ref.MailboxID, err = s.optionalRef(ctx, wid, b.MailboxID, "mailboxes", "Mailbox"); err != nil {
		return nil, err
	}
	if ref.TeamID != nil {
		hidden, err := domain.HiddenTeams(ctx, s.d.DB.Pool, p)
		if err != nil {
			return nil, httpx.Wrap(err)
		}
		if slices.Contains(hidden, *ref.TeamID) {
			return nil, httpx.NotFound("Team")
		}
	}
	if strings.TrimSpace(b.TicketID) != "" {
		id, ok := ids.Parse(b.TicketID)
		if !ok {
			return nil, httpx.NotFound("Ticket")
		}
		if _, err := domain.LoadVisibleTicket(ctx, s.d.DB.Pool, p, id); err != nil {
			if db.IsNotFound(err) {
				return nil, httpx.NotFound("Ticket")
			}
			return nil, httpx.Wrap(err)
		}
		ref.TicketID, ref.Sample = &id, false
	}
	tc, err := templates.Load(ctx, s.d.DB.Pool, s.d.Cfg, ref)
	if err != nil {
		return nil, httpx.Wrap(err)
	}
	design, err := templates.LoadDesign(ctx, s.d.DB.Pool, wid, tc.BrandID)
	if err != nil {
		return nil, httpx.Wrap(err)
	}
	fields, err := templates.FieldVariables(ctx, s.d.DB.Pool, wid)
	if err != nil {
		return nil, httpx.Wrap(err)
	}
	body := templates.SanitizeDesign(b.BodyHTML)
	out := &TemplatePreview{Problems: []templates.Problem{}}
	number := tc.Values.Text["ticket.number"]
	subject := "Re: " + firstNonEmpty(tc.Values.Text["ticket.subject"], "Your request") + " [#" + number + "]"
	content := tc.Fill(sampleContent)
	signature := tc.Signature()
	var history []templates.HistoryItem
	if ref.TicketID != nil {
		if history, err = loadHistory(ctx, s.d.DB.Pool, wid, *ref.TicketID, nil, 10); err != nil {
			return nil, httpx.Wrap(err)
		}
	} else {
		history = []templates.HistoryItem{{Author: firstNonEmpty(tc.Values.Text["contact.name"], "Jane Cooper"), At: time.Now().Add(-time.Hour),
			HTML: "<p>Hi, I can't sign in to my account since this morning. Could you take a look?</p>"}}
	}
	switch b.Kind {
	case templates.KindReply:
		out.Problems = templates.Check(b.BodyHTML, templates.KindReply, fields)
		content = hd.SanitizeRich(templates.Render(hd.SanitizeRichTemplate(b.BodyHTML), tc.Values, templates.HTML))
	case templates.KindSignature:
		out.Problems = templates.Check(body, templates.KindSignature, fields)
		signature = tc.Fill(body)
	case templates.KindPersonalSignature:
		out.Problems = templates.Check(body, templates.KindPersonalSignature, fields)
		delete(tc.Values.HTML, "agent.signature")
		signature = tc.Fill(body)
	case templates.KindAutoReply:
		out.Problems = append(templates.Check(body, templates.KindAutoReply, fields), templates.Check(b.Subject, templates.KindSubject, fields)...)
		delete(tc.Values.HTML, "agent.signature")
		for _, k := range []string{"agent.name", "agent.first_name", "agent.last_name", "agent.title", "agent.phone", "agent.pronouns", "agent.avatar_url"} {
			delete(tc.Values.Text, k)
		}
		tc.PersonalSignature = ""
		signature = tc.Signature()
		content = tc.Fill(body)
		subject = autoSubject(tc.FillText(b.Subject), tc.Values.Text["ticket.subject"], number)
		history = nil
	case templates.KindLayout:
		o, custom := design.Layout, design.CustomHTML
		if b.Options != nil {
			o = *b.Options
		}
		if strings.TrimSpace(b.BodyHTML) != "" {
			custom = b.BodyHTML
		}
		var probs []templates.Problem
		o, custom, probs = templates.CleanLayout(o, custom, fields)
		out.Problems = probs
		design.Layout, design.CustomHTML = o, custom
	case templates.KindCSAT:
		if strings.TrimSpace(body) == "" {
			body = design.CSATHTML
		}
		subj := firstNonEmpty(b.Subject, design.CSATSubject)
		out.Problems = append(templates.Check(body, templates.KindCSAT, fields), templates.Check(subj, templates.KindSubject, fields)...)
		accent := design.Layout.Accent
		if accent == "" {
			accent = tc.BrandColor
		}
		buttons, _ := templates.SurveyButtons(s.d.Cfg.AppURL+"/csat#t=preview&score=", accent)
		tc.Values.HTML["survey"] = buttons
		content = tc.Fill(body)
		subject = autoSubject(tc.FillText(subj), "", number)
		history = nil
	default:
		return nil, httpx.Validation("kind must be reply, signature, personal_signature, auto_reply, layout or csat")
	}
	mail := tc.RenderEmail(design, templates.Parts{Content: content, ContentText: templates.TextOf(content), Signature: signature, History: history})
	out.Subject, out.HTML, out.Text = subject, mail.HTML, mail.Text
	return out, nil
}

// autoSubject is an automatic email's subject: as written with the ticket number tag, or
// the ticket's reply subject.
func autoSubject(subject, ticketSubject, number string) string {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return "Re: " + firstNonEmpty(ticketSubject, "Your request") + " [#" + number + "]"
	}
	if _, ok := subjectTicketNumber(subject); !ok {
		subject += " [#" + number + "]"
	}
	return subject
}
