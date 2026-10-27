// Package sealed knows every database column that holds a value sealed with d.Box
// (CREDENTIALS_KEY), and re-encrypts values a previous key sealed with the current one,
// so a key can be replaced without losing what it protects.
package sealed

// Column is one sealed column: a table, its single-column primary key and the bytea
// column that holds d.Box output (nonce || AES-256-GCM ciphertext).
type Column struct {
	Table  string
	Key    string
	Column string
}

// Columns is every sealed column. A new one goes here in the same change that adds it;
// TestRegistryCoversEveryByteaColumn fails until it is here or in NotSealed.
var Columns = []Column{
	{"users", "id", "totp_secret_enc"},                       // TOTP secrets
	{"workspaces", "id", "widget_secret_enc"},                // the first widget's identity secret, from before widgets had rows
	{"widgets", "id", "secret_enc"},                          // widget identity secrets
	{"mailboxes", "id", "credentials_enc"},                   // SMTP/IMAP passwords and OAuth tokens
	{"webhooks", "id", "secret_enc"},                         // webhook signing secrets
	{"webhooks", "id", "headers_enc"},                        // webhook request headers
	{"invitations", "id", "token_enc"},                       // invitation link tokens
	{"push_devices", "id", "token_sealed"},                   // APNs device or relay tokens
	{"web_push_subscriptions", "id", "sealed"},               // Web Push endpoints and keys
	{"idempotency_keys", "id", "body_enc"},                   // stored responses for replays
	{"ai_provider_keys", "workspace_id", "key_enc"},          // BYOK AI provider keys
	{"ai_decision_keys", "workspace_id", "key_enc"},          // BYOK TypeSafe keys for Jev decisions
	{"ai_widget_actions", "id", "args"},                      // tool calls waiting for a visitor
	{"ai_mcp_servers", "id", "secrets_enc"},                  // MCP server credentials and OAuth tokens
	{"ai_mcp_oauth_states", "state_hash", "flow_enc"},        // MCP OAuth flows in progress
	{"integrations", "id", "credentials_enc"},                // integration tokens
	{"integrations", "id", "oauth_enc"},                      // integration OAuth clients
	{"imports", "id", "credentials_enc"},                     // import source credentials
	{"support_channel_connections", "id", "credentials_enc"}, // Slack, Discord and Telegram credentials
	{"collab_accounts", "id", "credentials_enc"},             // Collab mail account credentials
}

// NotSealed are the bytea columns that hold no d.Box output: hashes, public keys and
// identifiers.
var NotSealed = []string{
	"admin_sessions.prev_refresh_hash",
	"admin_sessions.refresh_hash",
	"ai_mcp_oauth_states.nonce_hash",
	"ai_mcp_oauth_states.state_hash",
	"api_keys.key_hash",
	"app_attest_challenges.challenge_hash",
	"app_attest_keys.public_key",
	"auth_throttle.key",
	"idempotency_keys.request_hash",
	"invitations.token_hash",
	"oauth_authorizations.code_hash",
	"oauth_clients.secret_hash",
	"oauth_refresh_tokens.token_hash",
	"one_time_tokens.token_hash",
	"passkeys.aaguid",
	"passkeys.credential_id",
	"passkeys.public_key",
	"push_devices.token_hash",
	"river_job.unique_key",
	"sessions.prev_refresh_hash",
	"sessions.refresh_hash",
	"support_channel_link_codes.code_hash",
	"tickets.csat_token_hash",
	"totp_recovery_codes.code_hash",
	"web_push_subscriptions.endpoint_hash",
	"webhooks.verify_code_hash",
	"widget_sessions.token_hash",
}
