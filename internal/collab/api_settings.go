package collab

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/audit"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/email"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// Routes and writes of an account's own settings (settings.go, contracts/collab.md
// "Account settings"): the provider is written first, and nothing is saved when it refuses.

var (
	errSignatureUnsupported = httpx.E(422, "signature_sync_unsupported", "Only Gmail accounts share their signature with opendesk.")
	errOutOfOfficeReadOnly  = httpx.E(422, "out_of_office_read_only", "Reconnect the account to change its automatic reply from opendesk.")
	errOutOfOfficeNone      = httpx.E(422, "out_of_office_unsupported", "This account's provider does not share its automatic reply with opendesk.")
)

// errSettingsProvider is the provider refusing a settings write or read: its reason, safe.
func errSettingsProvider(err error) error {
	return httpx.E(http.StatusBadGateway, "provider_error", safeError(err))
}

type outOfOfficeInput struct {
	ID   string `path:"id"`
	Body OutOfOffice
}

func (s *service) registerSettingsRoutes(api huma.API) {
	collabOp(api, huma.Operation{OperationID: "pull-collab-signature", Method: http.MethodPost, Path: "/v1/collab/accounts/{id}/signature/pull",
		Summary: "Replace a mail account's signature with its Gmail one"},
		func(ctx context.Context, in *accountIDInput) (*accountBody, error) {
			p, a, err := s.manageable(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			if a.Provider != ProviderGoogle {
				return nil, errSignatureUnsupported
			}
			sa, err := (&gmailProvider{s: s, a: a}).sendAs(ctx)
			if err != nil {
				return nil, errSettingsProvider(err)
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if _, err := s.withSettings(ctx, tx, a.ID, func(row *accountRow, st *settingsState) bool {
					now := time.Now().UTC()
					row.SignatureHTML = email.SanitizeSettingsHTML(sa.Signature)
					st.SignatureHash, st.SignatureSyncedAt = settingHash(sa.Signature), &now
					return true
				}); err != nil {
					return err
				}
				if err := audit.Log(ctx, tx, p, p.WorkspaceID, "collab.account_updated", "collab_account", &a.ID,
					map[string]any{"fields": []string{"signature_html"}, "from_provider": true}); err != nil {
					return err
				}
				return s.publishAccounts(ctx, tx, p.WorkspaceID, a.ID, nil)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return s.accountReply(ctx, p, a.ID)
		})

	collabOp(api, huma.Operation{OperationID: "set-collab-out-of-office", Method: http.MethodPut, Path: "/v1/collab/accounts/{id}/out-of-office",
		Summary: "Set a mail account's automatic reply", Description: "Written to the provider first; nothing is saved when it refuses."},
		func(ctx context.Context, in *outOfOfficeInput) (*accountBody, error) {
			p, a, err := s.manageable(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			switch outOfOfficeSync(a) {
			case SyncNone:
				return nil, errOutOfOfficeNone
			case SyncReadOnly:
				return nil, errOutOfOfficeReadOnly
			}
			o := in.Body
			o.MessageHTML = email.SanitizeSettingsHTML(o.MessageHTML)
			for _, t := range []**time.Time{&o.Start, &o.End} {
				if *t != nil {
					v := (*t).UTC().Truncate(time.Second)
					*t = &v
				}
			}
			if o.Start != nil && o.End != nil && !o.Start.Before(*o.End) {
				return nil, httpx.Validation("The automatic reply must end after it starts.")
			}
			var remote remoteSettings
			switch a.Provider {
			case ProviderGoogle:
				v, err := (&gmailProvider{s: s, a: a}).putVacation(ctx, o)
				if err != nil {
					return nil, errSettingsProvider(err)
				}
				ooo := v.outOfOffice()
				remote.ooo, remote.oooHash = &ooo, v.hash()
			case ProviderMicrosoft:
				if o.Enabled && (o.Start == nil) != (o.End == nil) {
					return nil, httpx.Validation("Outlook needs both a start and an end to schedule an automatic reply.")
				}
				o.Subject = "" // Outlook's replies have no subject of their own
				ar, err := (&graphProvider{s: s, a: a}).putAutoReplies(ctx, o)
				if err != nil {
					return nil, errSettingsProvider(err)
				}
				ooo := ar.outOfOffice()
				remote.ooo, remote.oooHash = &ooo, ar.hash()
			}
			err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
				if _, err := s.withSettings(ctx, tx, a.ID, func(row *accountRow, st *settingsState) bool {
					st.OutOfOfficeHash = "" // what the provider answered is stored, whatever was seen before
					return applyRemote(row, st, remote, time.Now().UTC())
				}); err != nil {
					return err
				}
				if err := audit.Log(ctx, tx, p, p.WorkspaceID, "collab.account_updated", "collab_account", &a.ID,
					map[string]any{"fields": []string{"out_of_office"}}); err != nil {
					return err
				}
				return s.publishAccounts(ctx, tx, p.WorkspaceID, a.ID, nil)
			})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return s.accountReply(ctx, p, a.ID)
		})
}

// manageable reads the path id and the account the caller manages.
func (s *service) manageable(ctx context.Context, rawID string) (*auth.Principal, *accountRow, error) {
	p, err := s.require(ctx, auth.RoleViewer)
	if err != nil {
		return nil, nil, httpx.Wrap(err)
	}
	id, err := parseID(rawID, "Account")
	if err != nil {
		return nil, nil, err
	}
	a, err := manageableAccount(ctx, s.d.DB.Pool, p, id)
	if err != nil {
		return nil, nil, notFound(err, "Account")
	}
	return p, a, nil
}

// accountReply answers with the account as it is now.
func (s *service) accountReply(ctx context.Context, p *auth.Principal, id uuid.UUID) (*accountBody, error) {
	a, err := loadAccount(ctx, s.d.DB.Pool, p.WorkspaceID, id)
	if err != nil {
		return nil, httpx.Wrap(err)
	}
	out, err := s.accountOut(ctx, s.d.DB.Pool, p, a)
	if err != nil {
		return nil, httpx.Wrap(err)
	}
	return &accountBody{out}, nil
}

// pushedSettings is what a change of the account made at the provider: the hashes of the
// provider's answer, so the next read does not take it back as a change.
type pushedSettings struct {
	signatureHash   string
	displayNameHash string
}

// pushAccountSettings makes a changed signature and From name at Gmail first, when they sync
// two-way (a and changed are the account after the change and the changed fields). Read-only
// and opendesk-only settings are saved locally only.
func (s *service) pushAccountSettings(ctx context.Context, a *accountRow, changed []string) (pushedSettings, error) {
	var out pushedSettings
	if a.Provider != ProviderGoogle || signatureSync(a) != SyncTwoWay {
		return out, nil
	}
	body := map[string]string{}
	if slices.Contains(changed, "signature_html") {
		body["signature"] = a.SignatureHTML // sanitized
	}
	if slices.Contains(changed, "display_name") {
		body["displayName"] = a.DisplayName
	}
	if len(body) == 0 {
		return out, nil
	}
	sa, err := (&gmailProvider{s: s, a: a}).patchSendAs(ctx, body)
	if err != nil {
		return out, errSettingsProvider(err)
	}
	if _, ok := body["signature"]; ok {
		out.signatureHash = settingHash(sa.Signature)
	}
	if _, ok := body["displayName"]; ok {
		out.displayNameHash = settingHash(sa.DisplayName)
	}
	return out, nil
}

// recordAccountPatch stores, in the PATCH's tx, what the change made at the provider and
// that a person chose the From name.
func (s *service) recordAccountPatch(ctx context.Context, tx pgx.Tx, id uuid.UUID, pushed pushedSettings, changed []string) error {
	nameEdited := slices.Contains(changed, "display_name")
	if pushed == (pushedSettings{}) && !nameEdited {
		return nil
	}
	_, err := s.withSettings(ctx, tx, id, func(_ *accountRow, st *settingsState) bool {
		if pushed.signatureHash != "" {
			now := time.Now().UTC()
			st.SignatureHash, st.SignatureSyncedAt = pushed.signatureHash, &now
		}
		if pushed.displayNameHash != "" {
			st.DisplayNameHash = pushed.displayNameHash
		}
		if nameEdited {
			st.DisplayNameEdited = true
		}
		return true
	})
	return err
}
