package collab

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/email"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/ids"
	"github.com/useopendesk/opendesk/internal/templates"
)

// Routes of an account's addresses (identities.go, contracts/collab.md "Addresses"):
// Gmail's come from Gmail, and their From name and signature are changed in opendesk only
// (Gmail lets apps change the primary address alone); Microsoft's and IMAP's are added
// and removed by hand and live in opendesk only.

var (
	errIdentitiesFromGmail = httpx.E(422, "identities_from_provider", "Add and remove send-as addresses in Gmail's settings (Accounts, Send mail as). They show here within 15 minutes, or at once with Check Gmail.")
	errIdentitiesByHand    = httpx.E(422, "identities_by_hand", "This account's addresses are added here by hand.")
	errIdentityConflict    = httpx.E(409, "identity_conflict", "The account already has this address.")
	errIdentityReplyTo     = httpx.E(422, "validation_failed", "Gmail keeps this address's reply-to. Change it in Gmail's settings.")
)

type identityInput struct {
	Email         string  `json:"email" maxLength:"254"`
	DisplayName   *string `json:"display_name,omitempty" maxLength:"200" doc:"The From name; empty uses the account's. May name the sender: {agent}, {agent_first}, {mailbox}, {workspace}"`
	SignatureHTML *string `json:"signature_html,omitempty"`
	ReplyTo       *string `json:"reply_to,omitempty" maxLength:"254" doc:"Replies go here; empty: to the address itself"`
}

type identityPatchInput struct {
	ID         string `path:"id"`
	IdentityID string `path:"identity_id"`
	Body       struct {
		DisplayName   *string `json:"display_name,omitempty" maxLength:"200" doc:"The From name; empty uses the account's. May name the sender: {agent}, {agent_first}, {mailbox}, {workspace}"`
		SignatureHTML *string `json:"signature_html,omitempty"`
		ReplyTo       *string `json:"reply_to,omitempty" maxLength:"254" doc:"Hand-added addresses only"`
	}
}

type identityPreviewInput struct {
	Email         string `json:"email,omitempty" maxLength:"254" doc:"The address; empty is the account's own"`
	DisplayName   string `json:"display_name" maxLength:"200" doc:"The From name as typed; empty uses the account's"`
	SignatureHTML string `json:"signature_html,omitempty"`
}

// IdentityPreview is an address's From name and signature filled for the caller.
type IdentityPreview struct {
	FromName      string `json:"from_name"`
	From          string `json:"from" doc:"The From header: \"Name <address>\""`
	SignatureHTML string `json:"signature_html"`
	// FromNameProblem says what is wrong with the From name; "" when nothing.
	FromNameProblem string              `json:"from_name_problem"`
	Problems        []templates.Problem `json:"problems" doc:"What is wrong with the signature"`
}

type identityPathInput struct {
	ID         string `path:"id"`
	IdentityID string `path:"identity_id"`
}

func (s *service) registerIdentityRoutes(api huma.API) {
	collabOp(api, huma.Operation{OperationID: "add-collab-identity", Method: http.MethodPost, Path: "/v1/collab/accounts/{id}/identities",
		Summary: "Add an address a mail account sends from", Description: "Microsoft and IMAP accounts; Gmail's come from Gmail."},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body identityInput
		}) (*accountBody, error) {
			p, a, err := s.manageable(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			if a.Provider == ProviderGoogle {
				return nil, errIdentitiesFromGmail
			}
			addr := cleanAddress(in.Body.Email)
			if addr == "" {
				return nil, httpx.Validation("Enter the address to send from.")
			}
			if strings.EqualFold(addr, a.Email) {
				return nil, errIdentityConflict
			}
			x := &identityRow{ID: ids.New(), WorkspaceID: p.WorkspaceID, AccountID: a.ID, Email: addr, Source: SourceManual,
				Verification: VerificationAccepted}
			if _, err := applyIdentityFields(x, in.Body.DisplayName, in.Body.SignatureHTML, in.Body.ReplyTo); err != nil {
				return nil, err
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if _, err := loadAccountForUpdate(ctx, tx, a.ID); err != nil {
					return err
				}
				var n int
				if err := tx.QueryRow(ctx, `SELECT count(*) FROM collab_identities WHERE account_id = $1`, a.ID).Scan(&n); err != nil {
					return err
				}
				if n >= maxManualIdentities {
					return httpx.Validation("An account sends from at most 50 addresses besides its own.")
				}
				tag, err := tx.Exec(ctx, `INSERT INTO collab_identities (id, workspace_id, account_id, email, display_name, signature_html, reply_to,
					source, verification, position, sync_state) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, '{}')
					ON CONFLICT (account_id, email) DO NOTHING`,
					x.ID, x.WorkspaceID, x.AccountID, x.Email, x.DisplayName, x.SignatureHTML, x.ReplyTo, x.Source, x.Verification, n)
				if err != nil {
					return err
				}
				if tag.RowsAffected() == 0 {
					return errIdentityConflict
				}
				if err := audit.Log(ctx, tx, p, p.WorkspaceID, "collab.account_updated", "collab_account", &a.ID,
					map[string]any{"fields": []string{"identities"}, "identity_id": x.ID, "added": true}); err != nil {
					return err
				}
				return s.publishAccounts(ctx, tx, p.WorkspaceID, a.ID, nil)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return s.accountReply(ctx, p, a.ID)
		})

	collabOp(api, huma.Operation{OperationID: "refresh-collab-identities", Method: http.MethodPost, Path: "/v1/collab/accounts/{id}/identities/refresh",
		Summary: "Read a mail account's addresses from Gmail now", Description: "Gmail accounts: its send-as list and settings, read at once instead of at the next settings read."},
		func(ctx context.Context, in *accountIDInput) (*accountBody, error) {
			p, a, err := s.manageable(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			if a.Provider != ProviderGoogle {
				return nil, errIdentitiesByHand
			}
			if !a.Config.Demo {
				remote, readErr, err := s.refreshSettings(ctx, a, (&gmailProvider{s: s, a: a}).readSettings)
				if err != nil {
					return nil, httpx.Wrap(err)
				}
				if remote.identities == nil {
					return nil, errSettingsProvider(readErr)
				}
			}
			return s.accountReply(ctx, p, a.ID)
		})

	collabOp(api, huma.Operation{OperationID: "update-collab-identity", Method: http.MethodPatch, Path: "/v1/collab/accounts/{id}/identities/{identity_id}",
		Summary: "Change an address a mail account sends from", Description: "Saved in opendesk. A Gmail address's From name and signature are replaced by Gmail's when they change there."},
		func(ctx context.Context, in *identityPatchInput) (*accountBody, error) {
			p, a, x, err := s.manageableIdentity(ctx, in.ID, in.IdentityID)
			if err != nil {
				return nil, err
			}
			b := in.Body
			if b.ReplyTo != nil && x.Source == SourceProvider && cleanAddress(*b.ReplyTo) != x.ReplyTo {
				return nil, errIdentityReplyTo
			}
			changed, err := applyIdentityFields(x, b.DisplayName, b.SignatureHTML, b.ReplyTo)
			if err != nil {
				return nil, err
			}
			if len(changed) == 0 {
				return s.accountReply(ctx, p, a.ID)
			}
			// Kept in opendesk, Gmail's included: the hashes of Gmail's values stay as last
			// read, so its next change there still replaces this one.
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if _, err := loadAccountForUpdate(ctx, tx, a.ID); err != nil {
					return err
				}
				if err := saveIdentity(ctx, tx, x); err != nil {
					return err
				}
				if err := audit.Log(ctx, tx, p, p.WorkspaceID, "collab.account_updated", "collab_account", &a.ID,
					map[string]any{"fields": []string{"identities"}, "identity_id": x.ID, "identity_fields": changed}); err != nil {
					return err
				}
				return s.publishAccounts(ctx, tx, p.WorkspaceID, a.ID, nil)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return s.accountReply(ctx, p, a.ID)
		})

	collabOp(api, huma.Operation{OperationID: "pull-collab-identity-signature", Method: http.MethodPost,
		Path:    "/v1/collab/accounts/{id}/identities/{identity_id}/signature/pull",
		Summary: "Replace an address's signature with its Gmail one"},
		func(ctx context.Context, in *identityPathInput) (*accountBody, error) {
			p, a, x, err := s.manageableIdentity(ctx, in.ID, in.IdentityID)
			if err != nil {
				return nil, err
			}
			if x.Source != SourceProvider {
				return nil, errSignatureUnsupported
			}
			sa, err := (&gmailProvider{s: s, a: a}).sendAsOf(ctx, x.Email)
			if err != nil {
				return nil, errSettingsProvider(err)
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if _, err := loadAccountForUpdate(ctx, tx, a.ID); err != nil {
					return err
				}
				// Read again under the account's lock: a sync may have changed it meanwhile.
				x, err := scanIdentity(tx.QueryRow(ctx, `SELECT `+identityColumns+` FROM collab_identities WHERE id = $1 AND account_id = $2`, x.ID, a.ID))
				if err != nil {
					return notFound(err, "Address")
				}
				now := time.Now().UTC()
				x.SignatureHTML = email.SanitizeSettingsHTML(sa.Signature)
				x.Sync.SignatureHash, x.Sync.SignatureSyncedAt = settingHash(sa.Signature), &now
				if err := saveIdentity(ctx, tx, x); err != nil {
					return err
				}
				if err := audit.Log(ctx, tx, p, p.WorkspaceID, "collab.account_updated", "collab_account", &a.ID,
					map[string]any{"fields": []string{"identities"}, "identity_id": x.ID, "identity_fields": []string{"signature_html"}, "from_provider": true}); err != nil {
					return err
				}
				return s.publishAccounts(ctx, tx, p.WorkspaceID, a.ID, nil)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return s.accountReply(ctx, p, a.ID)
		})

	collabOp(api, huma.Operation{OperationID: "pull-collab-identity-display-name", Method: http.MethodPost,
		Path:    "/v1/collab/accounts/{id}/identities/{identity_id}/display-name/pull",
		Summary: "Replace an address's From name with its Gmail one"},
		func(ctx context.Context, in *identityPathInput) (*accountBody, error) {
			p, a, x, err := s.manageableIdentity(ctx, in.ID, in.IdentityID)
			if err != nil {
				return nil, err
			}
			if x.Source != SourceProvider {
				return nil, errNameUnsupported
			}
			sa, err := (&gmailProvider{s: s, a: a}).sendAsOf(ctx, x.Email)
			if err != nil {
				return nil, errSettingsProvider(err)
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if _, err := loadAccountForUpdate(ctx, tx, a.ID); err != nil {
					return err
				}
				x, err := scanIdentity(tx.QueryRow(ctx, `SELECT `+identityColumns+` FROM collab_identities WHERE id = $1 AND account_id = $2`, x.ID, a.ID))
				if err != nil {
					return notFound(err, "Address")
				}
				// Gmail's empty name uses the account's, as in the sync.
				x.DisplayName = displayname.Clean(sa.DisplayName, displayname.Workspace)
				x.Sync.DisplayNameHash = settingHash(sa.DisplayName)
				if err := saveIdentity(ctx, tx, x); err != nil {
					return err
				}
				if err := audit.Log(ctx, tx, p, p.WorkspaceID, "collab.account_updated", "collab_account", &a.ID,
					map[string]any{"fields": []string{"identities"}, "identity_id": x.ID, "identity_fields": []string{"display_name"}, "from_provider": true}); err != nil {
					return err
				}
				return s.publishAccounts(ctx, tx, p.WorkspaceID, a.ID, nil)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return s.accountReply(ctx, p, a.ID)
		})

	collabOp(api, huma.Operation{OperationID: "preview-collab-identity", Method: http.MethodPost, Path: "/v1/collab/accounts/{id}/identities/preview",
		Summary: "Fill an address's From name and signature for you", Description: "Without saving: what your mail from the address would carry, and what is wrong with them."},
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body identityPreviewInput
		}) (*struct{ Body IdentityPreview }, error) {
			p, a, err := s.manageable(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			out, err := s.previewIdentity(ctx, p, a, in.Body)
			if err != nil {
				return nil, err
			}
			return &struct{ Body IdentityPreview }{out}, nil
		})

	collabOp(api, huma.Operation{OperationID: "remove-collab-identity", Method: http.MethodDelete, Path: "/v1/collab/accounts/{id}/identities/{identity_id}",
		Summary: "Remove an address a mail account sends from", Description: "Hand-added addresses; Gmail's go when Gmail no longer lists them."},
		func(ctx context.Context, in *identityPathInput) (*accountBody, error) {
			p, a, x, err := s.manageableIdentity(ctx, in.ID, in.IdentityID)
			if err != nil {
				return nil, err
			}
			if x.Source == SourceProvider {
				return nil, errIdentitiesFromGmail
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if _, err := s.withSettings(ctx, tx, a.ID, func(_ *accountRow, st *settingsState) bool {
					if st.DefaultFrom == x.Email {
						st.DefaultFrom = "" // new mail goes from the account's own address again
					}
					return true
				}); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `DELETE FROM collab_identities WHERE workspace_id = $1 AND id = $2`, p.WorkspaceID, x.ID); err != nil {
					return err
				}
				if err := audit.Log(ctx, tx, p, p.WorkspaceID, "collab.account_updated", "collab_account", &a.ID,
					map[string]any{"fields": []string{"identities"}, "identity_id": x.ID, "removed": true}); err != nil {
					return err
				}
				return s.publishAccounts(ctx, tx, p.WorkspaceID, a.ID, nil)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return s.accountReply(ctx, p, a.ID)
		})

	collabOp(api, huma.Operation{OperationID: "suggest-collab-identities", Method: http.MethodGet, Path: "/v1/collab/accounts/{id}/identities/suggestions",
		Summary: "Addresses the provider knows for a mail account", Description: "Microsoft: the account's other addresses from its profile, when Microsoft shares them; else none."},
		func(ctx context.Context, in *accountIDInput) (*struct {
			Body struct {
				Addresses []string `json:"addresses"`
			}
		}, error) {
			_, a, err := s.manageable(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			out := &struct {
				Body struct {
					Addresses []string `json:"addresses"`
				}
			}{}
			out.Body.Addresses = []string{}
			if a.Provider != ProviderMicrosoft || a.Config.Demo {
				return out, nil
			}
			idents, err := accountIdentities(ctx, s.d.DB.Pool, a)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			pctx, cancel := context.WithTimeout(ctx, providerTimeout)
			defer cancel()
			list, err := (&graphProvider{s: s, a: a}).proxyAddresses(pctx)
			if err != nil {
				// Suggestions are a convenience: the reason is logged, and there are none.
				s.log.Info("collab address suggestions not read", "account_id", a.ID, "reason", safeError(err))
				return out, nil
			}
			own := ownAddresses(a, idents)
			for _, addr := range list {
				if !slices.Contains(own, addr) && !slices.Contains(out.Body.Addresses, addr) {
					out.Body.Addresses = append(out.Body.Addresses, addr)
				}
			}
			return out, nil
		})
}

// previewIdentity fills a From name and signature as typed, for the caller.
func (s *service) previewIdentity(ctx context.Context, p *auth.Principal, a *accountRow, b identityPreviewInput) (IdentityPreview, error) {
	out := IdentityPreview{Problems: []templates.Problem{}}
	if len(b.SignatureHTML) > maxSignatureHTML {
		return out, httpx.Validation("The signature is too long.")
	}
	addr := cleanAddress(b.Email)
	if addr == "" {
		addr = strings.ToLower(a.Email)
	}
	format := strings.TrimSpace(b.DisplayName)
	if format == "" {
		format = a.DisplayName
	}
	if _, err := cleanFromName("display_name", format); err != nil {
		var e *httpx.Error
		if !errors.As(err, &e) {
			return out, httpx.Wrap(err)
		}
		out.FromNameProblem = e.Detail
		format = ""
	}
	f := s.fillerFor(s.d.DB.Pool, p.WorkspaceID, p.UserID)
	name, err := f.fromName(ctx, a, format)
	if err != nil {
		return out, httpx.Wrap(err)
	}
	sig := templates.Protect(b.SignatureHTML, email.SanitizeSettingsHTML)
	out.Problems = append(out.Problems, templates.Check(sig, templates.KindCollabSignature, nil)...)
	if out.SignatureHTML, err = f.signature(ctx, sig, addr, name); err != nil {
		return out, httpx.Wrap(err)
	}
	out.FromName = name
	out.From = addr
	if name != "" {
		out.From = name + " <" + addr + ">"
	}
	return out, nil
}

// manageableIdentity reads the path ids: the account the caller manages and its address.
func (s *service) manageableIdentity(ctx context.Context, rawAccount, rawIdentity string) (*auth.Principal, *accountRow, *identityRow, error) {
	p, a, err := s.manageable(ctx, rawAccount)
	if err != nil {
		return nil, nil, nil, err
	}
	id, err := parseID(rawIdentity, "Address")
	if err != nil {
		return nil, nil, nil, err
	}
	x, err := scanIdentity(s.d.DB.Pool.QueryRow(ctx, `SELECT `+identityColumns+` FROM collab_identities
		WHERE workspace_id = $1 AND account_id = $2 AND id = $3`, p.WorkspaceID, a.ID, id))
	if err != nil {
		return nil, nil, nil, notFound(err, "Address")
	}
	return p, a, x, nil
}

// applyIdentityFields checks and sets an address's From name, signature (sanitized with
// the signature allowlist) and reply-to, and returns the fields that changed.
func applyIdentityFields(x *identityRow, name, signature, replyTo *string) ([]string, error) {
	var changed []string
	if name != nil {
		dn, err := cleanFromName("display_name", *name)
		if err != nil {
			return nil, err
		}
		if dn != x.DisplayName {
			x.DisplayName, changed = dn, append(changed, "display_name")
		}
	}
	if signature != nil {
		sig, err := cleanSignature(*signature)
		if err != nil {
			return nil, err
		}
		if sig != x.SignatureHTML {
			x.SignatureHTML, changed = sig, append(changed, "signature_html")
		}
	}
	if replyTo != nil {
		rt := ""
		if strings.TrimSpace(*replyTo) != "" {
			if rt = cleanAddress(*replyTo); rt == "" {
				return nil, httpx.Validation("Check the reply-to address.")
			}
		}
		if rt != x.ReplyTo {
			x.ReplyTo, changed = rt, append(changed, "reply_to")
		}
	}
	return changed, nil
}

// chooseDefault checks a new default address for new mail (the account PATCH's
// default_from) and returns it (lower case; "" = unchanged). It is kept in opendesk for
// every provider: Gmail's isDefault is a setting of the address made default, which Gmail
// lets apps change only on the primary one. Gmail's own change still comes in
// (applyRemote, DefaultHash).
func (s *service) chooseDefault(ctx context.Context, q db.Querier, a *accountRow, want string) (string, error) {
	idents, err := accountIdentities(ctx, q, a)
	if err != nil {
		return "", httpx.Wrap(err)
	}
	snd, ok := usableSender(a, idents, want)
	if !ok {
		return "", httpx.Validation("Choose one of the account's addresses that can send.")
	}
	addr := strings.ToLower(snd.Email)
	if strings.EqualFold(defaultSender(a, idents).Email, addr) {
		return "", nil
	}
	return addr, nil
}

// recordDefault stores a chosen default address under the row lock, in tx.
func (s *service) recordDefault(ctx context.Context, tx pgx.Tx, a *accountRow, addr string) error {
	_, err := s.withSettings(ctx, tx, a.ID, func(row *accountRow, st *settingsState) bool {
		st.DefaultFrom = addr
		if strings.EqualFold(addr, row.Email) {
			st.DefaultFrom = ""
		}
		return true
	})
	return err
}

// proxyAddresses reads the account's other addresses from its profile: proxyAddresses
// (returned only when selected), whose "smtp:" entries are its aliases and "SMTP:" its
// primary address. Not verified against every tenant: Microsoft documents it on work and
// school accounts, personal accounts have none, and a refusal means no suggestions.
func (g *graphProvider) proxyAddresses(ctx context.Context) ([]string, error) {
	var me struct {
		ProxyAddresses []string `json:"proxyAddresses"`
	}
	if err := g.do(ctx, http.MethodGet, "/me?$select=proxyAddresses", nil, &me); err != nil {
		return nil, err
	}
	out := []string{}
	for _, p := range me.ProxyAddresses {
		if len(p) > 5 && strings.EqualFold(p[:5], "smtp:") {
			if addr := cleanAddress(p[5:]); addr != "" && !slices.Contains(out, addr) && len(out) < maxManualIdentities {
				out = append(out, addr)
			}
		}
	}
	return out, nil
}
