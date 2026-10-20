package tickets

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/events"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/helpdesk/routing"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/jobs"
	"github.com/useopendesk/opendesk/internal/templates"
)

type ticketOut struct{ Body domain.Ticket }

// patchedTicketOut is PATCH /v1/tickets/{id}: OD-Ticket-Hidden is "true" when the change
// left the ticket where the caller may no longer see it.
type patchedTicketOut struct {
	Hidden string `header:"OD-Ticket-Hidden"`
	Body   domain.Ticket
}

type listIn struct {
	httpx.ListParams
	Status     string `query:"status" doc:"Comma separated statuses"`
	Priority   string `query:"priority" doc:"Comma separated priorities"`
	Severity   string `query:"severity" doc:"Comma separated severities (sev1..sev4), none for tickets without one"`
	Assignee   string `query:"assignee" doc:"me, none or a user id"`
	TeamID     string `query:"team_id" doc:"Comma-separated team ids, mine (the caller's teams) and none: tickets of any of them"`
	MailboxID  string `query:"mailbox_id"`
	ReceivedAt string `query:"received_at" maxLength:"254" doc:"Tickets first written to this address (a mailbox's address or one of its aliases)"`
	BrandID    string `query:"brand_id"`
	WidgetID   string `query:"widget_id"`
	Tag        string `query:"tag" doc:"Label name"`
	Channel    string `query:"channel" enum:"email,chat,api,portal,"`
	ContactID  string `query:"contact_id"`
	ViewID     string `query:"view_id" doc:"Apply a saved view's filters; explicit filters override it"`
	Q          string `query:"q" maxLength:"200" doc:"Full text over subjects and messages, or #number"`
	Spam       string `query:"spam" enum:"true,false,any," doc:"Default false (spam hidden); true lists only spam"`
	Sort       string `query:"sort" maxLength:"60" doc:"last_message_at (default), created_at, priority, severity, due or field.<key>"`
	Order      string `query:"order" enum:"asc,desc," doc:"Default desc, asc for due"`

	fields    []hd.FieldFilter
	fieldsErr error
}

// Resolve reads the field.<key> and field.<key>[op] parameters (custom field filters).
func (in *listIn) Resolve(ctx huma.Context) []error {
	in.fields, in.fieldsErr = hd.FieldFiltersFromQuery(queryOf(ctx))
	return nil
}

type ticketContactInput struct {
	Email string `json:"email" maxLength:"254"`
	Name  string `json:"name,omitempty" maxLength:"200"`
}

type ticketCreateBody struct {
	Subject       string              `json:"subject" minLength:"1" maxLength:"500"`
	ContactID     string              `json:"contact_id,omitempty"`
	Contact       *ticketContactInput `json:"contact,omitempty"`
	Channel       string              `json:"channel,omitempty" enum:"email,api" doc:"Default email when mailbox_id is given, otherwise api"`
	MailboxID     string              `json:"mailbox_id,omitempty"`
	BodyHTML      string              `json:"body_html,omitempty" maxLength:"500000"`
	Kind          string              `json:"kind,omitempty" enum:"reply,note" doc:"Default reply"`
	Priority      string              `json:"priority,omitempty" enum:"low,normal,high,urgent"`
	Severity      string              `json:"severity,omitempty" enum:"sev1,sev2,sev3,sev4"`
	AssigneeID    string              `json:"assignee_id,omitempty"`
	TeamID        string              `json:"team_id,omitempty" doc:"Wins over the address's team; routing rules may still move the ticket"`
	BrandID       string              `json:"brand_id,omitempty" doc:"Default: the mailbox's brand, else the default brand"`
	Tags          []string            `json:"tags,omitempty" maxItems:"50"`
	Status        string              `json:"status,omitempty" enum:"open,pending,on_hold,solved,closed"`
	Cc            []string            `json:"cc,omitempty" maxItems:"20"`
	Bcc           []string            `json:"bcc,omitempty" maxItems:"20"`
	AttachmentIDs []string            `json:"attachment_ids,omitempty" maxItems:"20"`
	Custom        map[string]any      `json:"custom,omitempty"`
	Fields        map[string]any      `json:"fields,omitempty" doc:"Custom field values by key"`
}

// CreateInput is the trusted internal surface for creating a ticket through the
// same validation, routing, event and automation path as POST /v1/tickets.
type CreateInput struct {
	Subject    string
	BodyHTML   string
	Kind       string
	Channel    string
	Priority   string
	TeamID     *uuid.UUID
	AssigneeID *uuid.UUID
}

// CreateTicket creates a ticket for another trusted agent surface. Keep this
// narrow: richer API-only inputs still go through POST /v1/tickets.
func CreateTicket(ctx context.Context, tx pgx.Tx, d *deps.Deps, p *auth.Principal, in CreateInput) (domain.Ticket, error) {
	if len([]rune(strings.TrimSpace(in.Subject))) > 500 {
		return domain.Ticket{}, httpx.Validation("subject must be at most 500 characters")
	}
	if len([]rune(in.BodyHTML)) > 500000 {
		return domain.Ticket{}, httpx.Validation("message must be at most 500000 characters")
	}
	if in.Kind != "" && in.Kind != "reply" && in.Kind != "note" {
		return domain.Ticket{}, httpx.Validation("kind must be reply or note")
	}
	if in.Channel != "" && in.Channel != domain.ChannelEmail && in.Channel != domain.ChannelAPI {
		return domain.Ticket{}, httpx.Validation("channel must be email or api")
	}
	if in.Priority != "" && !slices.Contains(domain.Priorities, in.Priority) {
		return domain.Ticket{}, httpx.Validation("priority must be one of " + strings.Join(domain.Priorities, ", "))
	}
	b := ticketCreateBody{Subject: in.Subject, BodyHTML: in.BodyHTML, Kind: in.Kind, Channel: in.Channel, Priority: in.Priority}
	if in.TeamID != nil {
		b.TeamID = in.TeamID.String()
	}
	if in.AssigneeID != nil {
		b.AssigneeID = in.AssigneeID.String()
	}
	return createTicket(ctx, tx, d, p, b)
}

type ticketPatchBody struct {
	Subject      *string        `json:"subject,omitempty" maxLength:"500"`
	Status       *string        `json:"status,omitempty" enum:"open,pending,on_hold,solved,closed"`
	Priority     *string        `json:"priority,omitempty" enum:"low,normal,high,urgent"`
	Severity     Opt[string]    `json:"severity,omitempty" doc:"sev1, sev2, sev3, sev4, or null to clear"`
	AssigneeID   Opt[string]    `json:"assignee_id,omitempty" doc:"User id, or null to unassign"`
	TeamID       Opt[string]    `json:"team_id,omitempty" doc:"Team id, or null. May switch the reply address (contracts/teams.md)"`
	MailboxID    *string        `json:"mailbox_id,omitempty" doc:"The reply address: an address of the ticket's brand the caller may send from"`
	BrandID      *string        `json:"brand_id,omitempty" doc:"A brand of the workspace; the reply address follows it"`
	SnoozedUntil Opt[time.Time] `json:"snoozed_until,omitempty" doc:"Snooze until, or null to wake"`
	Spam         *bool          `json:"spam,omitempty"`
	Tags         *[]string      `json:"tags,omitempty" maxItems:"50" doc:"Replaces the labels, by name; missing labels are created"`
	AddTags      []string       `json:"add_tags,omitempty" maxItems:"50" doc:"Adds these labels, by name, keeping the others; missing labels are created"`
	RemoveTags   []string       `json:"remove_tags,omitempty" maxItems:"50" doc:"Removes these labels, by name"`
	Custom       map[string]any `json:"custom,omitempty" doc:"Merged into custom; null values remove keys"`
	Fields       map[string]any `json:"fields,omitempty" doc:"Custom field values by key, merged; null clears a field"`
}

func (b ticketPatchBody) toPatch() (Patch, error) {
	p := Patch{Subject: b.Subject, Status: b.Status, Priority: b.Priority, Severity: b.Severity, Spam: b.Spam, Tags: b.Tags, AddTags: b.AddTags,
		RemoveTags: b.RemoveTags, Custom: b.Custom, Fields: b.Fields, Snooze: b.SnoozedUntil}
	var err error
	if p.Assignee, err = optID(b.AssigneeID, "assignee_id"); err != nil {
		return p, err
	}
	if p.Team, err = optID(b.TeamID, "team_id"); err != nil {
		return p, err
	}
	if b.MailboxID != nil {
		id, err := hd.ParseRef(*b.MailboxID, "mailbox_id")
		if err != nil {
			return p, err
		}
		p.Mailbox = &id
	}
	if b.BrandID != nil {
		id, err := hd.ParseRef(*b.BrandID, "brand_id")
		if err != nil {
			return p, err
		}
		p.Brand = &id
	}
	return p, nil
}

func optID(o Opt[string], field string) (Opt[uuid.UUID], error) {
	if !o.Set {
		return Opt[uuid.UUID]{}, nil
	}
	if o.Null || strings.TrimSpace(o.Value) == "" {
		return Opt[uuid.UUID]{Set: true, Null: true}, nil
	}
	id, err := hd.ParseRef(o.Value, field)
	if err != nil {
		return Opt[uuid.UUID]{}, err
	}
	return Opt[uuid.UUID]{Set: true, Value: id}, nil
}

// cleanEmail validates and lowercases an address.
func cleanEmail(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s || len(s) > 254 {
		return "", httpx.Validation(fmt.Sprintf("%q is not a valid email address", hd.Truncate(s, 80)))
	}
	return s, nil
}

func cleanEmails(in []string) ([]string, error) {
	out := []string{}
	for _, s := range in {
		if strings.TrimSpace(s) == "" {
			continue
		}
		e, err := cleanEmail(s)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

func checkCustom(m map[string]any) error {
	raw, _ := json.Marshal(m)
	if len(m) > 50 || len(raw) > 16<<10 {
		return httpx.Validation("custom fields are limited to 50 keys and 16 KB")
	}
	return nil
}

func registerTickets(d *deps.Deps, api huma.API) {
	tags := []string{"Tickets"}

	huma.Register(api, huma.Operation{OperationID: "list-tickets", Method: http.MethodGet, Path: "/v1/tickets",
		Summary: "List tickets", Tags: tags},
		func(ctx context.Context, in *listIn) (*struct{ Body httpx.Page[domain.Ticket] }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeTicketsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if in.fieldsErr != nil {
				return nil, in.fieldsErr
			}
			req := hd.TicketFilters{Status: hd.SplitCSV(in.Status), Priority: hd.SplitCSV(in.Priority), Severity: hd.SplitCSV(in.Severity), Assignee: in.Assignee,
				TeamID: in.TeamID, MailboxID: in.MailboxID, ReceivedAt: in.ReceivedAt, BrandID: in.BrandID, WidgetID: in.WidgetID, Tag: in.Tag, Channel: in.Channel, Q: in.Q, Fields: in.fields}
			f := req
			if in.ViewID != "" {
				view, err := loadView(ctx, d.DB.Pool, p, in.ViewID)
				if err != nil {
					return nil, httpx.Wrap(err)
				}
				f = mergeFilters(view, req)
			}
			if f.Status, err = validEnum(f.Status, domain.Statuses, "status"); err != nil {
				return nil, err
			}
			if f.Priority, err = validEnum(f.Priority, domain.Priorities, "priority"); err != nil {
				return nil, err
			}
			if f.Severity, err = validEnum(f.Severity, hd.SeverityFilterValues, "severity"); err != nil {
				return nil, err
			}
			lq, err := forCaller(ctx, d.DB.Pool, p, listQuery{TicketFilters: f, ContactID: in.ContactID, Spam: in.Spam})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			page, err := listTickets(ctx, d.DB.Pool, p.WorkspaceID, p.ActorUserID(), lq, in.Sort, in.Order, in.Limit, in.Cursor)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body httpx.Page[domain.Ticket] }{page}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "ticket-counts", Method: http.MethodGet, Path: "/v1/tickets/counts",
		Summary: "Sidebar counts", Tags: tags,
		Description: "unassigned and mine count open, pending and on_hold tickets; status counts exclude spam; spam counts spam that is not closed; views counts each visible view; stats breaks each sidebar row down into tickets awaiting a reply, urgent and overdue."},
		func(ctx context.Context, _ *struct{}) (*struct{ Body TicketCounts }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeTicketsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			c, err := ticketCounts(ctx, d.DB.Pool, p)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body TicketCounts }{c}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "create-ticket", Method: http.MethodPost, Path: "/v1/tickets",
		Summary: "Create a ticket", Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *struct{ Body ticketCreateBody }) (*ticketOut, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeTicketsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			var t domain.Ticket
			err = d.DB.Tx(ctx, func(tx pgx.Tx) error {
				var err error
				t, err = createTicket(ctx, tx, d, p, in.Body)
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &ticketOut{t}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "get-ticket", Method: http.MethodGet, Path: "/v1/tickets/{id}",
		Summary: "Get a ticket", Tags: tags, Description: "id is the uuid, #number or number."},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*ticketOut, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleViewer, auth.ScopeTicketsRead)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := resolveVisible(ctx, d.DB.Pool, p, in.ID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			t, err := domain.LoadTicket(ctx, d.DB.Pool, p.WorkspaceID, id)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &ticketOut{t}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "update-ticket", Method: http.MethodPatch, Path: "/v1/tickets/{id}",
		Summary: "Update a ticket", Tags: tags},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body ticketPatchBody
		}) (*patchedTicketOut, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeTicketsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			patch, err := in.Body.toPatch()
			if err != nil {
				return nil, err
			}
			if patch.CreateLabels, err = hd.CanCreateLabels(ctx, d.DB.Pool, p); err != nil {
				return nil, httpx.Wrap(err)
			}
			var t domain.Ticket
			err = d.DB.Tx(ctx, func(tx pgx.Tx) error {
				id, err := resolveVisible(ctx, tx, p, in.ID)
				if err != nil {
					return err
				}
				if err := patch.checkCaller(ctx, tx, p, id); err != nil {
					return err
				}
				if _, err := applyPatch(ctx, tx, d, p.WorkspaceID, id, actorOf(p), patch, applyOpts{Enqueue: true, RequireFields: true}); err != nil {
					return err
				}
				t, err = domain.LoadTicket(ctx, tx, p.WorkspaceID, id)
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			// The change may take the ticket out of the caller's sight (a private team they
			// are not on): it still answers the ticket as changed, flagged so clients drop it.
			out := &patchedTicketOut{Body: t}
			var assignee *uuid.UUID
			if t.Assignee != nil {
				assignee = &t.Assignee.ID
			}
			visible, err := domain.CanSee(ctx, d.DB.Pool, p, t.TeamID, assignee)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if !visible {
				out.Hidden = "true"
			}
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "delete-ticket", Method: http.MethodDelete, Path: "/v1/tickets/{id}",
		Summary: "Delete a ticket", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			ID string `path:"id"`
		}) (*struct{}, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeTicketsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			err = d.DB.Tx(ctx, func(tx pgx.Tx) error {
				id, err := resolveVisible(ctx, tx, p, in.ID)
				if err != nil {
					return err
				}
				return deleteTicket(ctx, tx, p.WorkspaceID, id)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return nil, nil
		})

	type ticketBulkBody struct {
		IDs    []string         `json:"ids" minItems:"1" maxItems:"100"`
		Patch  *ticketPatchBody `json:"patch,omitempty"`
		Delete bool             `json:"delete,omitempty"`
	}
	type ticketBulkResult struct {
		Data    []domain.Ticket `json:"data"`
		Updated int             `json:"updated"`
		Deleted int             `json:"deleted"`
	}
	huma.Register(api, huma.Operation{OperationID: "bulk-tickets", Method: http.MethodPost, Path: "/v1/tickets/bulk",
		Summary: "Update or delete many tickets", Tags: tags,
		Description: "All or nothing: a validation error on any ticket changes none. Ids not in the workspace are refused."},
		func(ctx context.Context, in *struct{ Body ticketBulkBody }) (*struct{ Body ticketBulkResult }, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeTicketsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if (in.Body.Patch == nil) == !in.Body.Delete {
				return nil, httpx.Validation("send either patch or delete: true")
			}
			var patch Patch
			if in.Body.Patch != nil {
				if patch, err = in.Body.Patch.toPatch(); err != nil {
					return nil, err
				}
				if patch.CreateLabels, err = hd.CanCreateLabels(ctx, d.DB.Pool, p); err != nil {
					return nil, httpx.Wrap(err)
				}
			}
			out := ticketBulkResult{Data: []domain.Ticket{}}
			err = d.DB.Tx(ctx, func(tx pgx.Tx) error {
				seen := map[uuid.UUID]bool{}
				for _, raw := range in.Body.IDs {
					id, err := resolveVisible(ctx, tx, p, raw)
					if err != nil {
						return err
					}
					if seen[id] {
						continue
					}
					seen[id] = true
					if in.Body.Delete {
						if err := deleteTicket(ctx, tx, p.WorkspaceID, id); err != nil {
							return err
						}
						out.Deleted++
						continue
					}
					if err := patch.checkCaller(ctx, tx, p, id); err != nil {
						return err
					}
					changes, err := applyPatch(ctx, tx, d, p.WorkspaceID, id, actorOf(p), patch, applyOpts{Enqueue: true, RequireFields: true})
					if err != nil {
						return err
					}
					if len(changes) > 0 {
						out.Updated++
					}
					t, err := domain.LoadTicket(ctx, tx, p.WorkspaceID, id)
					if err != nil {
						return err
					}
					out.Data = append(out.Data, t)
				}
				return nil
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body ticketBulkResult }{out}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "merge-ticket", Method: http.MethodPost, Path: "/v1/tickets/{id}/merge",
		Summary: "Merge a ticket into another", Tags: tags,
		Description: "Moves every message and label into into_id and closes this ticket. Returns the target."},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body struct {
				IntoID string `json:"into_id"`
			}
		}) (*ticketOut, error) {
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeTicketsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			var t domain.Ticket
			err = d.DB.Tx(ctx, func(tx pgx.Tx) error {
				src, err := resolveVisible(ctx, tx, p, in.ID)
				if err != nil {
					return err
				}
				dst, err := resolveVisible(ctx, tx, p, in.Body.IntoID)
				if err != nil {
					return httpx.Validation("into_id is not a ticket in this workspace")
				}
				if err := mergeTickets(ctx, tx, d, p.WorkspaceID, src, dst, actorOf(p)); err != nil {
					return err
				}
				t, err = domain.LoadTicket(ctx, tx, p.WorkspaceID, dst)
				return err
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &ticketOut{t}, nil
		})
}

func validEnum(in, allowed []string, field string) ([]string, error) {
	var out []string
	for _, v := range in {
		ok := false
		for _, a := range allowed {
			ok = ok || a == v
		}
		if !ok {
			return nil, httpx.Validation(field + " must be one of " + strings.Join(allowed, ", "))
		}
		out = append(out, v)
	}
	return out, nil
}

func createTicket(ctx context.Context, tx pgx.Tx, d *deps.Deps, p *auth.Principal, b ticketCreateBody) (domain.Ticket, error) {
	wid := p.WorkspaceID
	actor := actorOf(p)
	nt := domain.NewTicket{WorkspaceID: wid, Subject: b.Subject, Status: b.Status, Priority: b.Priority, Custom: b.Custom}
	if b.Severity != "" {
		if !slices.Contains(domain.Severities, b.Severity) {
			return domain.Ticket{}, httpx.Validation("severity must be one of " + strings.Join(domain.Severities, ", "))
		}
		sev := b.Severity
		nt.Severity = &sev
	}
	if strings.TrimSpace(b.Subject) == "" {
		return domain.Ticket{}, httpx.Validation("subject is required")
	}
	if b.Custom != nil {
		if err := checkCustom(b.Custom); err != nil {
			return domain.Ticket{}, err
		}
	}
	if b.Fields != nil || nt.Status == domain.StatusSolved || nt.Status == domain.StatusClosed {
		defs, err := domain.LoadFieldDefs(ctx, tx, wid, domain.FieldEntityTicket)
		if err != nil {
			return domain.Ticket{}, err
		}
		fields, _, err := domain.MergeFields(defs, map[string]any{}, b.Fields, domain.MemberChecker(ctx, tx, wid))
		if err != nil {
			return domain.Ticket{}, err
		}
		if closing(nt.Status) {
			if err := domain.CheckRequiredOnSolve(defs, fields); err != nil {
				return domain.Ticket{}, err
			}
		}
		nt.Fields = fields
	}

	// Contact.
	var contactEmail *string
	switch {
	case b.ContactID != "":
		id, err := hd.ParseRef(b.ContactID, "contact_id")
		if err != nil {
			return domain.Ticket{}, err
		}
		if err := tx.QueryRow(ctx, `SELECT email FROM contacts WHERE workspace_id = $1 AND id = $2`, wid, id).Scan(&contactEmail); err != nil {
			return domain.Ticket{}, httpx.Validation("contact not found in this workspace")
		}
		nt.ContactID = &id
	case b.Contact != nil:
		email, err := cleanEmail(b.Contact.Email)
		if err != nil {
			return domain.Ticket{}, err
		}
		name, err := displayname.Validate("Contact name", b.Contact.Name, displayname.Person, true)
		if err != nil {
			return domain.Ticket{}, err
		}
		id, _, err := domain.UpsertContactByEmail(ctx, tx, wid, email, name)
		if err != nil {
			return domain.Ticket{}, err
		}
		nt.ContactID, contactEmail = &id, &email
	}

	// Channel and mailbox.
	var mailboxEmail, mailboxName string
	if b.MailboxID != "" {
		id, err := hd.ParseRef(b.MailboxID, "mailbox_id")
		if err != nil {
			return domain.Ticket{}, err
		}
		if err := tx.QueryRow(ctx, `SELECT email, display_name FROM mailboxes WHERE workspace_id = $1 AND id = $2`, wid, id).
			Scan(&mailboxEmail, &mailboxName); err != nil {
			return domain.Ticket{}, httpx.Validation("mailbox not found in this workspace")
		}
		nt.MailboxID = &id
	}
	nt.Channel = b.Channel
	if nt.Channel == "" {
		nt.Channel = domain.ChannelAPI
		if nt.MailboxID != nil {
			nt.Channel = domain.ChannelEmail
		}
	}
	kind := b.Kind
	if kind == "" {
		kind = "reply"
	}
	if nt.Channel == domain.ChannelEmail {
		if nt.MailboxID == nil {
			return domain.Ticket{}, httpx.Validation("an email ticket needs mailbox_id")
		}
		if kind == "reply" && (contactEmail == nil || *contactEmail == "") {
			return domain.Ticket{}, httpx.Validation("an email reply needs a contact with an email address")
		}
	}

	if b.AssigneeID != "" {
		id, err := hd.ParseRef(b.AssigneeID, "assignee_id")
		if err != nil {
			return domain.Ticket{}, err
		}
		if err := hd.CheckMember(ctx, tx, wid, id); err != nil {
			return domain.Ticket{}, err
		}
		nt.AssigneeID = &id
	}
	var askedTeam *uuid.UUID
	if b.TeamID != "" {
		id, err := hd.ParseRef(b.TeamID, "team_id")
		if err != nil {
			return domain.Ticket{}, err
		}
		if err := hd.CheckRef(ctx, tx, wid, "team", id); err != nil {
			return domain.Ticket{}, err
		}
		askedTeam = &id
	}
	var askedBrand *uuid.UUID
	if b.BrandID != "" {
		id, err := hd.ParseRef(b.BrandID, "brand_id")
		if err != nil {
			return domain.Ticket{}, err
		}
		if err := hd.CheckRef(ctx, tx, wid, "brand", id); err != nil {
			return domain.Ticket{}, err
		}
		if nt.MailboxID != nil {
			ok, err := mailboxInBrand(ctx, tx, wid, id, nt.MailboxID)
			if err != nil {
				return domain.Ticket{}, err
			}
			if !ok {
				return domain.Ticket{}, httpx.Validation("mailbox_id must be an address of brand_id")
			}
		}
		askedBrand = &id
	}
	if nt.MailboxID != nil {
		ok, err := domain.CanSendFrom(ctx, tx, p, *nt.MailboxID)
		if err != nil {
			return domain.Ticket{}, err
		}
		if !ok {
			return domain.Ticket{}, httpx.E(403, "forbidden", "Only members of its team can send from this address.")
		}
	}
	brandID, err := domain.ResolveBrand(ctx, tx, wid, askedBrand, nt.MailboxID, nil)
	if err != nil {
		return domain.Ticket{}, err
	}
	nt.BrandID = &brandID
	cc, err := cleanEmails(b.Cc)
	if err != nil {
		return domain.Ticket{}, err
	}
	bcc, err := cleanEmails(b.Bcc)
	if err != nil {
		return domain.Ticket{}, err
	}
	attIDs, err := hd.ParseAttachmentIDs(b.AttachmentIDs)
	if err != nil {
		return domain.Ticket{}, err
	}
	if err := hd.CheckAttachments(ctx, tx, wid, attIDs, hd.Uploader{User: p.ActorUserID(), APIKey: p.ActorAPIKeyID()}); err != nil {
		return domain.Ticket{}, err
	}
	bodyHTML := hd.SanitizeRich(b.BodyHTML)
	bodyText := hd.HTMLToText(bodyHTML)

	// The team (contracts/teams.md "Routing"): the one asked for, else the address's, then
	// the first matching routing rule.
	in := routing.Input{WorkspaceID: wid, Channel: nt.Channel, BrandID: brandID, MailboxID: nt.MailboxID, Subject: nt.Subject,
		Body: bodyText, ContactID: nt.ContactID, TeamID: askedTeam}
	if in.Channel != domain.ChannelEmail {
		in.Channel = domain.ChannelAPI
	}
	if contactEmail != nil {
		in.FromEmail = *contactEmail
	}
	routed, err := routing.Resolve(ctx, tx, in)
	if err != nil {
		return domain.Ticket{}, err
	}
	nt.TeamID, nt.Routed = routed.TeamID, routed.Routed
	if nt.TeamID != nil && nt.Routed == nil {
		nt.Routed = &domain.TicketRouted{Via: "api"}
	}
	if nt.Routed != nil && nt.Routed.Via == "rule" && nt.Routed.RuleID != nil {
		if _, err := tx.Exec(ctx, `UPDATE routing_rules SET run_count = run_count + 1 WHERE workspace_id = $1 AND id = $2`,
			wid, *nt.Routed.RuleID); err != nil {
			return domain.Ticket{}, err
		}
	}
	if err := hd.CheckAssigneeOnTeam(ctx, tx, wid, nt.TeamID, nt.AssigneeID); err != nil {
		return domain.Ticket{}, err
	}

	t, err := domain.CreateTicket(ctx, tx, nt)
	if err != nil {
		return domain.Ticket{}, err
	}
	if len(b.Tags) > 0 {
		create, err := hd.CanCreateLabels(ctx, tx, p)
		if err != nil {
			return domain.Ticket{}, err
		}
		if _, _, err := domain.ApplyTags(ctx, tx, wid, domain.TicketLabels, t.ID, domain.TagOps{Add: b.Tags, Create: create}); err != nil {
			return domain.Ticket{}, err
		}
	}

	if bodyText != "" || len(attIDs) > 0 {
		if kind == "reply" && templates.HasTags(b.BodyHTML) {
			ref := templates.Ref{WorkspaceID: wid, TicketID: &t.ID, AuthorUserID: actor.UserID, MailboxID: nt.MailboxID}
			if bodyHTML, err = hd.FillReply(ctx, tx, d.Cfg, ref, b.BodyHTML); err != nil {
				return domain.Ticket{}, err
			}
			bodyText = hd.HTMLToText(bodyHTML)
		}
		authorType := actor.Type
		if authorType == "ai" {
			authorType = "user" // the assistant writes on the user's behalf, after their approval
		}
		nm := domain.NewMessage{WorkspaceID: wid, TicketID: t.ID, Kind: kind, Direction: "outbound", AuthorType: authorType,
			AuthorUserID: actor.UserID, BodyHTML: bodyHTML, BodyText: bodyText, Subject: t.Subject, AttachmentIDs: attIDs,
			Trigger: jobs.TriggerTicketCreated}
		if actor.Type == "api" {
			nm.AuthorType = "api"
		}
		if kind == "reply" && nt.Channel == domain.ChannelEmail {
			nm.To, nm.Cc, nm.Bcc = []string{*contactEmail}, cc, bcc
			nm.FromEmail, nm.FromName = mailboxEmail, mailboxName
			nm.MailboxID = nt.MailboxID
			nm.DeliveryStatus = "pending"
		}
		msg, err := domain.AddMessage(ctx, tx, d.Jobs, nm)
		if err != nil {
			return domain.Ticket{}, err
		}
		if kind == "note" {
			if err := notifyMentions(ctx, tx, wid, t.ID, msg, actor.UserID); err != nil {
				return domain.Ticket{}, err
			}
		}
	} else if _, err := d.Jobs.InsertTx(ctx, tx, jobs.TicketTriggerArgs{WorkspaceID: wid, TicketID: t.ID,
		Trigger: jobs.TriggerTicketCreated, ActorUserID: actor.UserID}, nil); err != nil {
		return domain.Ticket{}, fmt.Errorf("queue trigger: %w", err)
	}
	if len(b.Tags) > 0 && (kind != "reply" || bodyText == "") {
		if err := domain.PublishTicketUpdated(ctx, tx, wid, t.ID, []string{"tags"}); err != nil {
			return domain.Ticket{}, err
		}
	}
	if nt.AssigneeID != nil && !sameID(actor.UserID, nt.AssigneeID) {
		if err := hd.Notify(ctx, tx, wid, *nt.AssigneeID, hd.NotifyAssigned, &t.ID,
			map[string]any{"by": idOrNil(actor.UserID), "actor_type": actor.Type}); err != nil {
			return domain.Ticket{}, err
		}
	}
	if nt.Status == domain.StatusSolved {
		if err := requestCSAT(ctx, tx, d, wid, t.ID); err != nil {
			return domain.Ticket{}, err
		}
	}
	return domain.LoadTicket(ctx, tx, wid, t.ID)
}

func deleteTicket(ctx context.Context, tx pgx.Tx, wid, id uuid.UUID) error {
	if _, err := tx.Exec(ctx, `UPDATE tickets SET deleted_at = now(), updated_at = now() WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL`,
		wid, id); err != nil {
		return err
	}
	_, err := events.Publish(ctx, tx, events.Event{WorkspaceID: wid, Type: "ticket.deleted", TicketID: &id, Data: map[string]any{"ticket_id": id}})
	return err
}

// mergeTickets moves src's messages and tags into dst and closes src.
func mergeTickets(ctx context.Context, tx pgx.Tx, d *deps.Deps, wid, src, dst uuid.UUID, actor Actor) error {
	if src == dst {
		return httpx.Validation("a ticket cannot be merged into itself")
	}
	// Lock in id order so concurrent merges cannot deadlock.
	first, second := src, dst
	if second.String() < first.String() {
		first, second = second, first
	}
	srcState, dstState := ticketState{}, ticketState{}
	for _, id := range []uuid.UUID{first, second} {
		s, err := lockTicket(ctx, tx, wid, id)
		if err != nil {
			return err
		}
		if id == src {
			srcState = s
		} else {
			dstState = s
		}
	}
	var srcMerged *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT merged_into_id FROM tickets WHERE workspace_id = $1 AND id = $2`, wid, src).Scan(&srcMerged); err != nil {
		return err
	}
	if srcMerged != nil {
		return httpx.Conflict("this ticket was already merged")
	}
	var dstNumber int64
	var dstMerged *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT number, merged_into_id FROM tickets WHERE workspace_id = $1 AND id = $2`, wid, dst).Scan(&dstNumber, &dstMerged); err != nil {
		return err
	}
	if dstMerged != nil {
		return httpx.Validation("the target ticket was itself merged; merge into the ticket it was merged into")
	}
	if _, err := tx.Exec(ctx, `UPDATE messages SET ticket_id = $3 WHERE workspace_id = $1 AND ticket_id = $2`, wid, src, dst); err != nil {
		return err
	}
	// Third-party threads go with their messages, and show on the target for everyone.
	rows, err := tx.Query(ctx, `UPDATE ticket_threads SET ticket_id = $3, updated_at = now() WHERE workspace_id = $1 AND ticket_id = $2 RETURNING id`, wid, src, dst)
	if err != nil {
		return err
	}
	moved, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	for _, id := range moved {
		if _, err := domain.PublishThread(ctx, tx, wid, dst, id, "thread.updated"); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO ticket_tags (ticket_id, tag_id) SELECT $2, tag_id FROM ticket_tags WHERE ticket_id = $1
		ON CONFLICT DO NOTHING`, src, dst); err != nil {
		return err
	}
	// Recompute the target's conversation fields from its messages.
	if _, err := tx.Exec(ctx, `UPDATE tickets t SET
		message_count = s.n, last_message_at = COALESCE(s.last_at, t.last_message_at),
		last_customer_message_at = s.last_customer,
		waiting_since = (SELECT min(m.created_at) FROM messages m WHERE m.workspace_id = $1 AND m.ticket_id = $2
			AND m.kind = 'reply' AND m.direction = 'inbound' AND m.created_at > COALESCE((SELECT max(o.created_at) FROM messages o
			WHERE o.workspace_id = $1 AND o.ticket_id = $2 AND o.kind = 'reply' AND o.direction = 'outbound'), '-infinity')),
		unanswered_count = (SELECT count(*) FROM messages m WHERE m.workspace_id = $1 AND m.ticket_id = $2
			AND m.kind = 'reply' AND m.direction = 'inbound' AND m.created_at > COALESCE((SELECT max(o.created_at) FROM messages o
			WHERE o.workspace_id = $1 AND o.ticket_id = $2 AND o.kind = 'reply' AND o.direction = 'outbound'), '-infinity')),
		last_message_preview = COALESCE(s.preview, t.last_message_preview),
		first_responded_at = LEAST(t.first_responded_at, (SELECT first_responded_at FROM tickets WHERE id = $3)),
		updated_at = now()
		FROM (SELECT count(*) FILTER (WHERE kind = 'reply') AS n,
			max(created_at) FILTER (WHERE kind = 'reply') AS last_at,
			max(created_at) FILTER (WHERE kind = 'reply' AND direction = 'inbound') AS last_customer,
			(SELECT left(regexp_replace(body_text, '\s+', ' ', 'g'), 200) FROM messages
				WHERE workspace_id = $1 AND ticket_id = $2 AND kind = 'reply' ORDER BY created_at DESC, id DESC LIMIT 1) AS preview
			FROM messages WHERE workspace_id = $1 AND ticket_id = $2) s
		WHERE t.workspace_id = $1 AND t.id = $2`, wid, dst, src); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE tickets SET merged_into_id = $3, message_count = 0, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, wid, src, dst); err != nil {
		return err
	}
	closed := domain.StatusClosed
	if _, err := applyPatch(ctx, tx, d, wid, src, actor, Patch{Status: &closed, Snooze: Opt[time.Time]{Set: true, Null: true}}, applyOpts{}); err != nil {
		return err
	}
	// The source's timeline names the target only when everyone who sees the source may see
	// it too: a target in another, private team stays unnamed.
	into := map[string]any{"into": dst.String(), "into_number": dstNumber}
	if dstState.Team != nil && !sameID(dstState.Team, srcState.Team) {
		private, err := hd.TeamPrivate(ctx, tx, wid, dstState.Team)
		if err != nil {
			return err
		}
		if private {
			into = map[string]any{"into": nil, "into_number": nil}
		}
	}
	if _, err := domain.LogActivity(ctx, tx, wid, src, actor.Type, actor.UserID, "merged", into); err != nil {
		return err
	}
	if _, err := domain.LogActivity(ctx, tx, wid, dst, actor.Type, actor.UserID, "merged",
		map[string]any{"from": src.String(), "from_number": srcState.Number}); err != nil {
		return err
	}
	if err := domain.PublishTicketUpdated(ctx, tx, wid, src, []string{"merged_into", "message_count"}); err != nil {
		return err
	}
	return domain.PublishTicketUpdated(ctx, tx, wid, dst, []string{"messages", "tags", "last_message_at"})
}

func queryOf(ctx huma.Context) url.Values {
	u := ctx.URL()
	return u.Query()
}
