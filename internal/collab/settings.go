package collab

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/useopendesk/opendesk/internal/displayname"
	"github.com/useopendesk/opendesk/internal/email"
)

// An account's own settings, synced with its provider (contracts/collab.md "Account
// settings"): the signature and From name (Gmail sendAs; Microsoft's From name is read
// from its profile, Graph has no signature), and the automatic reply (Gmail's vacation
// responder, Outlook's automatic replies).
//
// The provider wins: during the account sync, on the first one and then at most every
// settingsInterval, each setting is read and compared with a hash of the value last seen
// there. A different hash replaces opendesk's copy; the same hash changes nothing, so what
// opendesk itself just wrote (whose answer's hash is stored with it) is never pulled back.
// Writes go to the provider first and are saved only when it accepts them.

// Sync modes of a setting.
const (
	SyncTwoWay   = "two_way"   // read from the provider, and changes in opendesk are made there
	SyncReadOnly = "read_only" // read from the provider; opendesk cannot change it there
	SyncNone     = "none"      // lives in opendesk only (or, for the automatic reply, not available)
)

// settingsInterval is how often the sync reads an account's settings.
const settingsInterval = 15 * time.Minute

// settingsState is collab_accounts.config.settings.
type settingsState struct {
	// CheckedAt is the last read of the provider's settings (tried, whether or not it worked).
	CheckedAt *time.Time `json:"checked_at,omitempty"`
	// SignatureHash is the hash of the provider's signature as last seen.
	SignatureHash     string     `json:"signature_hash,omitempty"`
	SignatureSyncedAt *time.Time `json:"signature_synced_at,omitempty"`
	// DisplayNameHash is the hash of the provider's From name as last seen (Gmail).
	DisplayNameHash string `json:"display_name_hash,omitempty"`
	// DisplayNameEdited: a person changed the From name in opendesk, so Microsoft's
	// profile name no longer replaces it.
	DisplayNameEdited bool `json:"display_name_edited,omitempty"`
	// OutOfOffice is the automatic reply as last read from (or written to) the provider.
	OutOfOffice     *OutOfOffice `json:"out_of_office,omitempty"`
	OutOfOfficeHash string       `json:"out_of_office_hash,omitempty"`
}

func (a *accountRow) settings() settingsState {
	if a.Config.Settings == nil {
		return settingsState{}
	}
	return *a.Config.Settings
}

// accountHasScope reports whether the account's grant carries scope. An account whose
// scopes were never recorded has the mail scopes only.
func accountHasScope(a *accountRow, scope string) bool {
	return a.Config.Scopes != nil && hasScope(a.Config.Scopes, scope)
}

// signatureSync: Gmail two-way with gmail.settings.basic, else read only; Graph has no
// signature API, and IMAP none at all.
func signatureSync(a *accountRow) string {
	if a.Provider != ProviderGoogle {
		return SyncNone
	}
	if accountHasScope(a, googleSettingsScope) {
		return SyncTwoWay
	}
	return SyncReadOnly
}

// displayNameSync: Gmail like the signature; Microsoft's profile name is read until a
// person changes the From name in opendesk; IMAP none.
func displayNameSync(a *accountRow) string {
	switch a.Provider {
	case ProviderGoogle:
		return signatureSync(a)
	case ProviderMicrosoft:
		if a.settings().DisplayNameEdited {
			return SyncNone
		}
		return SyncReadOnly
	}
	return SyncNone
}

// outOfOfficeSync: Gmail two-way with gmail.settings.basic, else read only (gmail.modify
// reads it); Outlook needs MailboxSettings (ReadWrite two-way, Read read only); IMAP none.
func outOfOfficeSync(a *accountRow) string {
	switch a.Provider {
	case ProviderGoogle:
		return signatureSync(a)
	case ProviderMicrosoft:
		if accountHasScope(a, msMailboxSettingsScope) {
			return SyncTwoWay
		}
		if accountHasScope(a, "https://graph.microsoft.com/MailboxSettings.Read") {
			return SyncReadOnly
		}
	}
	return SyncNone
}

// applySettings fills an account's settings fields for the API.
func applySettings(m *MailAccount, a *accountRow) {
	st := a.settings()
	m.SignatureSync, m.DisplayNameSync, m.OutOfOfficeSync = signatureSync(a), displayNameSync(a), outOfOfficeSync(a)
	m.SignatureSyncedAt = st.SignatureSyncedAt
	if m.OutOfOfficeSync != SyncNone && st.OutOfOffice != nil {
		ooo := *st.OutOfOffice
		m.OutOfOffice = &ooo
	}
}

// settingHash is the hash a provider value is compared by.
func settingHash(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16])
}

// remoteSettings is what one read of the provider found; nil parts were not read.
type remoteSettings struct {
	signature   *string // Gmail's raw signature HTML
	displayName *string // Gmail's sendAs name, or Microsoft's profile name
	ooo         *OutOfOffice
	oooHash     string
}

// syncSettings reads the account's settings when due, as part of its sync: on the first
// one, then at most every settingsInterval. Failures are logged and never fail the sync.
func (s *service) syncSettings(ctx context.Context, prov Provider) {
	var a *accountRow
	var read func(context.Context) (remoteSettings, error)
	switch pr := prov.(type) {
	case *gmailProvider:
		a, read = pr.a, pr.readSettings
	case *graphProvider:
		a, read = pr.a, pr.readSettings
	default:
		return
	}
	if st := a.settings(); st.CheckedAt != nil && time.Since(*st.CheckedAt) < settingsInterval {
		return
	}
	remote, err := read(ctx)
	if err != nil {
		// The reason only: never a value read or sent.
		s.log.Info("collab settings sync failed", "account_id", a.ID, "workspace_id", a.WorkspaceID, "reason", safeError(err))
	}
	err = s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		changed, err := s.withSettings(ctx, tx, a.ID, func(row *accountRow, st *settingsState) bool {
			now := time.Now().UTC()
			st.CheckedAt = &now
			return applyRemote(row, st, remote, now)
		})
		if err != nil || !changed {
			return err
		}
		return s.publishAccounts(ctx, tx, a.WorkspaceID, a.ID, nil)
	})
	if err != nil && ctx.Err() == nil {
		s.log.Warn("collab settings save failed", "account_id", a.ID, "err", err)
	}
}

// applyRemote takes what the provider has where it changed since last seen there, and
// reports a change people see.
func applyRemote(a *accountRow, st *settingsState, r remoteSettings, now time.Time) bool {
	changed := false
	if r.signature != nil {
		if h := settingHash(*r.signature); h != st.SignatureHash {
			sig := email.SanitizeSettingsHTML(*r.signature)
			// The first read keeps a signature written in opendesk when the provider has none.
			if st.SignatureHash != "" || sig != "" || a.SignatureHTML == "" {
				a.SignatureHTML = sig
			}
			st.SignatureHash, st.SignatureSyncedAt, changed = h, &now, true
		}
	}
	if r.displayName != nil {
		name := displayname.Clean(*r.displayName, displayname.Workspace)
		switch a.Provider {
		case ProviderGoogle:
			if h := settingHash(*r.displayName); h != st.DisplayNameHash {
				// Gmail leaves the name empty to use the Google account's own.
				if name != "" {
					a.DisplayName = name
				}
				st.DisplayNameHash, changed = h, true
			}
		case ProviderMicrosoft:
			if !st.DisplayNameEdited && name != "" && name != a.DisplayName {
				a.DisplayName, changed = name, true
			}
		}
	}
	if r.ooo != nil && r.oooHash != st.OutOfOfficeHash {
		ooo := *r.ooo
		ooo.MessageHTML = email.SanitizeSettingsHTML(ooo.MessageHTML)
		st.OutOfOffice, st.OutOfOfficeHash, changed = &ooo, r.oooHash, true
	}
	return changed
}

// withSettings changes an account's settings state (and its signature and From name) under
// the row lock, in tx, and reports what fn reports.
func (s *service) withSettings(ctx context.Context, tx pgx.Tx, id uuid.UUID, fn func(a *accountRow, st *settingsState) bool) (bool, error) {
	a, err := loadAccountForUpdate(ctx, tx, id)
	if err != nil {
		return false, err
	}
	st := a.settings()
	changed := fn(a, &st)
	b, err := json.Marshal(st)
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `UPDATE collab_accounts SET config = jsonb_set(config, '{settings}', $2::jsonb), signature_html = $3,
		display_name = $4, updated_at = now() WHERE id = $1`, id, b, a.SignatureHTML, a.DisplayName)
	return changed, err
}
