package collab

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// maxSignatureHTML bounds an account's signature.
const maxSignatureHTML = 64 << 10

type accountBody struct {
	Body MailAccount
}

type accountIDInput struct {
	ID string `path:"id"`
}

// imapPatch changes an IMAP account's servers; omitted fields and passwords are kept.
type imapPatch struct {
	IMAPHost     *string `json:"imap_host,omitempty" maxLength:"253"`
	IMAPPort     *int    `json:"imap_port,omitempty" minimum:"1" maximum:"65535"`
	IMAPTLS      *string `json:"imap_tls,omitempty" enum:"tls,starttls"`
	IMAPUsername *string `json:"imap_username,omitempty" maxLength:"254"`
	Password     *string `json:"password,omitempty" maxLength:"1024" doc:"IMAP password (write-only)"`
	SMTPHost     *string `json:"smtp_host,omitempty" maxLength:"253"`
	SMTPPort     *int    `json:"smtp_port,omitempty" minimum:"1" maximum:"65535"`
	SMTPTLS      *string `json:"smtp_tls,omitempty" enum:"tls,starttls"`
	SMTPUsername *string `json:"smtp_username,omitempty" maxLength:"254"`
	SMTPPassword *string `json:"smtp_password,omitempty" maxLength:"1024" doc:"SMTP password when it differs from the IMAP one (write-only)"`
}

type updateAccountInput struct {
	ID   string `path:"id"`
	Body struct {
		Name          *string    `json:"name,omitempty" maxLength:"254" doc:"How the sidebar names it; \"\" resets to the address"`
		DisplayName   *string    `json:"display_name,omitempty" maxLength:"200" doc:"The From name of its mail; may name the sender: {agent}, {agent_first}, {mailbox}, {workspace}"`
		Color         *string    `json:"color,omitempty" enum:"gray,red,orange,amber,green,teal,blue,indigo,violet,pink"`
		SignatureHTML *string    `json:"signature_html,omitempty"`
		Visibility    *string    `json:"visibility,omitempty" enum:"private,shared"`
		SharedWith    *Audience  `json:"shared_with,omitempty"`
		IMAP          *imapPatch `json:"imap,omitempty"`
		DefaultFrom   *string    `json:"default_from,omitempty" maxLength:"254" doc:"The address new mail goes out from: the account's own or one of its usable addresses"`
	}
}

// errConnection is a refused IMAP or SMTP sign-in; detail is safe to show.
func errConnection(detail string) error { return httpx.E(422, "connection_failed", detail) }

func (s *service) registerAccountRoutes(api huma.API) {
	collabOp(api, huma.Operation{OperationID: "list-collab-accounts", Method: http.MethodGet, Path: "/v1/collab/accounts",
		Summary: "List the mail accounts I see"},
		func(ctx context.Context, in *struct {
			Manage bool `query:"manage" doc:"Also the shared accounts of people who left, for admins to manage"`
		}) (*struct{ Body []MailAccount }, error) {
			p, err := s.require(ctx, auth.RoleViewer)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			orphans := in.Manage && p.Role.AtLeast(auth.RoleAdmin)
			rows, err := s.d.DB.Pool.Query(ctx, `SELECT `+accountColumns+` FROM collab_accounts a
				WHERE a.workspace_id = $1 AND (`+accountVisibleSQL("a", "$2")+`
					OR ($3 AND a.visibility = 'shared' AND a.owner_id IS NULL))
				ORDER BY (a.owner_id IS NOT DISTINCT FROM $2) DESC, a.created_at, a.id`, p.WorkspaceID, p.UserID, orphans)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (*accountRow, error) { return scanAccount(r) })
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out, err := s.accountsOut(ctx, s.d.DB.Pool, p, list)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &struct{ Body []MailAccount }{out}, nil
		})

	collabOp(api, huma.Operation{OperationID: "update-collab-account", Method: http.MethodPatch, Path: "/v1/collab/accounts/{id}",
		Summary: "Change a mail account"},
		func(ctx context.Context, in *updateAccountInput) (*accountBody, error) {
			p, err := s.require(ctx, auth.RoleViewer)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID, "Account")
			if err != nil {
				return nil, err
			}
			a, err := manageableAccount(ctx, s.d.DB.Pool, p, id)
			if err != nil {
				return nil, notFound(err, "Account")
			}
			b := in.Body
			var changed []string
			if b.Name != nil {
				name := strings.TrimSpace(*b.Name)
				if name == "" || strings.EqualFold(name, a.Email) {
					name = a.Email
				} else if name, err = displayname.Validate("Name", name, displayname.Workspace, false); err != nil {
					return nil, httpx.Wrap(err)
				}
				if name != a.Name {
					a.Name, changed = name, append(changed, "name")
				}
			}
			if b.DisplayName != nil {
				dn, err := cleanFromName("display_name", *b.DisplayName)
				if err != nil {
					return nil, err
				}
				if dn != a.DisplayName {
					a.DisplayName, changed = dn, append(changed, "display_name")
				}
			}
			if b.Color != nil && *b.Color != a.Color {
				if !slices.Contains(Colors, *b.Color) {
					return nil, httpx.Validation("Choose one of the account colours.")
				}
				a.Color, changed = *b.Color, append(changed, "color")
			}
			if b.SignatureHTML != nil {
				sig, err := cleanSignature(*b.SignatureHTML)
				if err != nil {
					return nil, err
				}
				if sig != a.SignatureHTML {
					a.SignatureHTML, changed = sig, append(changed, "signature_html")
				}
			}

			// Sharing.
			sharing := false
			var audience Audience
			visibility := a.Visibility
			if b.Visibility != nil {
				visibility = *b.Visibility
			}
			if visibility == VisibilityPrivate && a.OwnerID == nil {
				return nil, httpx.Validation("An account whose owner left stays shared. Disconnect it instead.")
			}
			if visibility != a.Visibility || b.SharedWith != nil {
				sharing = true
				if visibility == VisibilityShared {
					if b.SharedWith != nil {
						audience = *b.SharedWith
					} else {
						audience, err = loadAudience(ctx, s.d.DB.Pool, p.WorkspaceID, &a.ID, nil)
						if err != nil {
							return nil, httpx.Wrap(err)
						}
					}
					if audience, err = checkAudience(ctx, s.d.DB.Pool, p.WorkspaceID, audience); err != nil {
						return nil, httpx.Wrap(err)
					}
					if audience.Empty() {
						return nil, httpx.Validation("Choose who to share the account with.")
					}
				}
				if visibility != a.Visibility {
					changed = append(changed, "visibility")
				}
				changed = append(changed, "shared_with")
				a.Visibility = visibility
			}

			// IMAP servers: tried before anything is saved.
			var sealed []byte
			if b.IMAP != nil {
				if a.Provider != ProviderIMAP {
					return nil, httpx.Validation("Only IMAP accounts have server settings.")
				}
				cfg, creds, err := s.patchIMAP(a, b.IMAP)
				if err != nil {
					return nil, err
				}
				if err := s.testIMAP(ctx, cfg, a.Email, creds); err != nil {
					return nil, errConnection(safeError(err))
				}
				if sealed, err = s.sealCreds(creds); err != nil {
					return nil, httpx.Wrap(err)
				}
				a.Config = cfg
				changed = append(changed, "imap")
			}

			// The default address for new mail: checked before anything is written.
			if b.DefaultFrom != nil {
				if _, ok := usableSenderOf(ctx, s, a, *b.DefaultFrom); !ok {
					return nil, httpx.Validation("Choose one of the account's addresses that can send.")
				}
			}

			// A signature or From name that syncs two-way is made at Gmail first: a refusal
			// saves nothing (502 provider_error).
			pushed, err := s.pushAccountSettings(ctx, a, changed)
			if err != nil {
				return nil, err
			}
			newDefault := ""
			if b.DefaultFrom != nil {
				if newDefault, err = s.chooseDefault(ctx, s.d.DB.Pool, a, *b.DefaultFrom); err != nil {
					return nil, err
				}
				if newDefault != "" {
					changed = append(changed, "default_from")
				}
			}

			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				before, err := accountAudience(ctx, tx, p.WorkspaceID, a.ID)
				if err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE collab_accounts SET name = $3, display_name = $4, color = $5, signature_html = $6,
					visibility = $7, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
					p.WorkspaceID, a.ID, a.Name, a.DisplayName, a.Color, a.SignatureHTML, a.Visibility); err != nil {
					return err
				}
				if err := s.recordAccountPatch(ctx, tx, a.ID, pushed, changed); err != nil {
					return err
				}
				if newDefault != "" {
					if err := s.recordDefault(ctx, tx, a, newDefault); err != nil {
						return err
					}
				}
				if sealed != nil {
					// Working settings end a refused connection; a sync starts now. The new
					// servers replace the connection settings; the settings sync state stays.
					cfg := a.Config
					cfg.Settings = nil
					if _, err := tx.Exec(ctx, `UPDATE collab_accounts SET credentials_enc = $3, reconnect = false, last_error = '',
						sync_failures = 0, status = CASE WHEN status = 'error' THEN 'syncing' ELSE status END, next_sync_at = now(),
						config = $4::jsonb || jsonb_strip_nulls(jsonb_build_object('settings', config->'settings'))
						WHERE workspace_id = $1 AND id = $2`, p.WorkspaceID, a.ID, sealed, cfg); err != nil {
						return err
					}
					if err := s.queueSync(ctx, tx, a.ID); err != nil {
						return err
					}
				}
				if sharing {
					if err := saveAudience(ctx, tx, p.WorkspaceID, &a.ID, nil, audience, p.UserID); err != nil {
						return err
					}
				}
				if len(changed) > 0 {
					if err := audit.Log(ctx, tx, p, p.WorkspaceID, "collab.account_updated", "collab_account", &a.ID,
						map[string]any{"fields": changed}); err != nil {
						return err
					}
				}
				return s.publishAccounts(ctx, tx, p.WorkspaceID, a.ID, before)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			fresh, err := loadAccount(ctx, s.d.DB.Pool, p.WorkspaceID, a.ID)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			out, err := s.accountOut(ctx, s.d.DB.Pool, p, fresh)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &accountBody{out}, nil
		})

	collabOp(api, huma.Operation{OperationID: "delete-collab-account", Method: http.MethodDelete, Path: "/v1/collab/accounts/{id}",
		Summary: "Disconnect a mail account", DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *accountIDInput) (*struct{}, error) {
			p, err := s.require(ctx, auth.RoleViewer)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID, "Account")
			if err != nil {
				return nil, err
			}
			a, err := manageableAccount(ctx, s.d.DB.Pool, p, id)
			if err != nil {
				return nil, notFound(err, "Account")
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				before, err := accountAudience(ctx, tx, p.WorkspaceID, a.ID)
				if err != nil {
					return err
				}
				if err := audit.Log(ctx, tx, p, p.WorkspaceID, "collab.account_disconnected", "collab_account", &a.ID,
					map[string]any{"provider": a.Provider}); err != nil {
					return err
				}
				if err := s.deleteAccount(ctx, tx, a); err != nil {
					return err
				}
				return s.publishAccounts(ctx, tx, p.WorkspaceID, a.ID, before)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return nil, nil
		})

	collabOp(api, huma.Operation{OperationID: "sync-collab-account", Method: http.MethodPost, Path: "/v1/collab/accounts/{id}/sync",
		Summary: "Sync a mail account now", DefaultStatus: http.StatusAccepted},
		func(ctx context.Context, in *accountIDInput) (*struct{}, error) {
			p, err := s.require(ctx, auth.RoleViewer)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			id, err := parseID(in.ID, "Account")
			if err != nil {
				return nil, err
			}
			if _, err := visibleAccount(ctx, s.d.DB.Pool, p, id); err != nil {
				return nil, notFound(err, "Account")
			}
			if err := s.queueSync(ctx, s.d.DB.Pool, id); err != nil {
				return nil, httpx.Wrap(err)
			}
			return nil, nil
		})
}

// patchIMAP applies new IMAP settings to an account's, keeping omitted fields and passwords.
func (s *service) patchIMAP(a *accountRow, in *imapPatch) (accountConfig, credentials, error) {
	cfg := a.Config
	creds, err := s.openCreds(a)
	if err != nil {
		return cfg, creds, httpx.Wrap(err)
	}
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = strings.TrimSpace(*v)
		}
	}
	set(&cfg.IMAPHost, in.IMAPHost)
	set(&cfg.IMAPTLS, in.IMAPTLS)
	set(&cfg.IMAPUsername, in.IMAPUsername)
	set(&cfg.SMTPHost, in.SMTPHost)
	set(&cfg.SMTPTLS, in.SMTPTLS)
	set(&cfg.SMTPUsername, in.SMTPUsername)
	if in.IMAPPort != nil {
		cfg.IMAPPort = *in.IMAPPort
	}
	if in.SMTPPort != nil {
		cfg.SMTPPort = *in.SMTPPort
	}
	if in.Password != nil && *in.Password != "" {
		creds.Password = *in.Password
	}
	if in.SMTPPassword != nil {
		creds.SMTPPassword = *in.SMTPPassword // "" uses the IMAP password again
	}
	cfg.IMAPHost, cfg.SMTPHost = strings.ToLower(cfg.IMAPHost), strings.ToLower(cfg.SMTPHost)
	for _, h := range []string{cfg.IMAPHost, cfg.SMTPHost} {
		if err := s.guard.ValidHost(h); err != nil {
			return cfg, creds, httpx.Validation("Enter the IMAP and SMTP server names.")
		}
	}
	tlsOK := func(v string) bool { return v == "tls" || v == "starttls" }
	if !tlsOK(cfg.IMAPTLS) || !tlsOK(cfg.SMTPTLS) || cfg.IMAPPort < 1 || cfg.IMAPPort > 65535 || cfg.SMTPPort < 1 || cfg.SMTPPort > 65535 {
		return cfg, creds, httpx.Validation("Enter each server's port and security.")
	}
	if creds.Password == "" {
		return cfg, creds, httpx.Validation("Enter the account password.")
	}
	// A kept password only ever goes to the server it was entered for: a new server
	// needs the password typed again.
	freshIMAP := in.Password != nil && *in.Password != ""
	freshSMTP := in.SMTPPassword != nil && *in.SMTPPassword != "" || creds.SMTPPassword == "" && freshIMAP
	if !strings.EqualFold(cfg.IMAPHost, a.Config.IMAPHost) && !freshIMAP ||
		!strings.EqualFold(cfg.SMTPHost, a.Config.SMTPHost) && !freshSMTP {
		return cfg, creds, httpx.Validation("Enter the password again to use it with a new server.")
	}
	return cfg, creds, nil
}
