package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Token purposes. A consumer accepts exactly one; see contracts/tokens.md.
const (
	PurposeAccess     = "access"
	PurposeWS         = "ws"
	PurposeWidgetWS   = "widget_ws"
	PurposeMFA        = "mfa"
	PurposeOAuthState = "oauth_state"
	// PurposePasskeyLogin and PurposePasskeyRegister reference a pending WebAuthn
	// ceremony by jti; each ceremony is finished at most once.
	PurposePasskeyLogin    = "passkey_login"
	PurposePasskeyRegister = "passkey_register"
	// PurposeAssistantApproval approves one proposed assistant action (jti), for the
	// user (sub) and session (sid) it was proposed to; the action runs at most once.
	PurposeAssistantApproval = "assistant_approval"
	// PurposeMCPAccess is an OAuth access token for the MCP server: aud is the MCP
	// resource URL, gid the grant it was issued under, scope the granted scopes.
	PurposeMCPAccess = "mcp_access"
	// PurposeNotificationUnsubscribe turns off notification emails from one workspace (wid)
	// for one user (sub); it rides in every notification email.
	PurposeNotificationUnsubscribe = "notification_unsubscribe"
	// PurposeAdmin is an instance admin panel access token: sub the user, sid the admin
	// session (contracts/admin.md). Only /v1/admin/* accepts it.
	PurposeAdmin = "admin"
	// PurposeAdminMFA carries a panel sign-in from the password step to the code step.
	PurposeAdminMFA = "admin_mfa"
)

const (
	AccessTTL     = 15 * time.Minute
	RefreshTTL    = 30 * 24 * time.Hour
	WSTTL         = 5 * time.Minute
	MFATTL        = 5 * time.Minute
	OAuthStateTTL = 10 * time.Minute
	PasskeyTTL    = 5 * time.Minute
	MCPAccessTTL  = time.Hour
	// UnsubscribeTTL keeps an email's unsubscribe link working for a while after it arrives.
	UnsubscribeTTL = 90 * 24 * time.Hour
	// AdminTTL is an admin panel access token's lifetime.
	AdminTTL = 10 * time.Minute
)

// Claims is the union of fields used across purposes.
type Claims struct {
	Purpose   string `json:"purpose"`
	SessionID string `json:"sid,omitempty"`
	MFA       bool   `json:"mfa,omitempty"`
	Kind      string `json:"kind,omitempty"` // ws: user | api_key
	Workspace string `json:"wid,omitempty"`
	Role      string `json:"role,omitempty"`
	Name      string `json:"name,omitempty"`
	Avatar    string `json:"avatar,omitempty"`
	// Teams are the private teams a ws token may join (contracts/teams.md).
	Teams []string `json:"teams,omitempty"`
	// IssuedAtMs is a ws token's issue time in milliseconds: the gateway refuses a token
	// issued at or before a revocation of its subject or session (contracts/realtime.md).
	IssuedAtMs int64 `json:"iat_ms,omitempty"`
	// Workspaces (ws, users only) are the other workspaces the socket may join besides wid,
	// by id, with the user's role and private teams in each (contracts/tokens.md).
	Workspaces map[string]WSWorkspace `json:"workspaces,omitempty"`
	// Brand binds a widget_ws token to the brand of the widget it was issued for.
	Brand string `json:"bid,omitempty"`
	// Free-form payload for oauth_state (provider, redirect, mailbox flow).
	Data map[string]string `json:"data,omitempty"`
	// mcp_access: the OAuth grant and its space-separated scopes.
	Grant string `json:"gid,omitempty"`
	Scope string `json:"scope,omitempty"`
	jwt.RegisteredClaims
}

// WSWorkspace is one workspace of a ws token's workspaces claim.
type WSWorkspace struct {
	Role  string   `json:"role"`
	Teams []string `json:"teams,omitempty"`
}

// A ws token's workspaces claim holds at most this many workspaces and private teams in
// all, which keeps the token within what the gateway accepts. Past either, a workspace is
// left out of the claim: the app refreshes it when it is opened instead of live.
const (
	WSMaxWorkspaces = 25
	WSMaxTeams      = 50
)

type Tokens struct{ secret []byte }

func NewTokens(secret []byte) *Tokens { return &Tokens{secret: secret} }

// Sign issues a token with the given purpose, subject and lifetime.
func (t *Tokens) Sign(c Claims, subject string, ttl time.Duration) (string, time.Time, error) {
	if c.Purpose == "" {
		return "", time.Time{}, errors.New("token purpose required")
	}
	now := time.Now()
	exp := now.Add(ttl)
	c.Subject = subject
	c.IssuedAt = jwt.NewNumericDate(now)
	c.ExpiresAt = jwt.NewNumericDate(exp)
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(t.secret)
	return s, exp, err
}

// Verify checks signature (HS256 only), expiry and that the purpose matches.
func (t *Tokens) Verify(token, purpose string) (*Claims, error) {
	var c Claims
	_, err := jwt.ParseWithClaims(token, &c, func(*jwt.Token) (any, error) { return t.secret, nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
	if err != nil {
		return nil, err
	}
	if c.Purpose == "" || c.Purpose != purpose {
		return nil, errors.New("token purpose mismatch")
	}
	return &c, nil
}
