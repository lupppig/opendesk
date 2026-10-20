package config

import (
	"log/slog"
	"strings"
	"testing"
)

func productionEnv(t *testing.T) {
	t.Setenv("OPENDESK_ENV", "production")
	t.Setenv("AUTH_SECRET", strings.Repeat("s", MinAuthSecretLength))
	t.Setenv("CREDENTIALS_KEY", strings.Repeat("ab", 32))
	t.Setenv("NATS_LISTEN", "127.0.0.1:4222")
}

func TestStripeAPIURLIsDevelopmentOnly(t *testing.T) {
	productionEnv(t)
	if _, err := Load(); err != nil {
		t.Fatalf("production config without STRIPE_API_URL: %v", err)
	}
	t.Setenv("STRIPE_API_URL", "http://127.0.0.1:12111")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "STRIPE_API_URL") {
		t.Fatalf("production with STRIPE_API_URL: want refusal, got %v", err)
	}
	t.Setenv("OPENDESK_ENV", "development")
	c, err := Load()
	if err != nil {
		t.Fatalf("development with STRIPE_API_URL: %v", err)
	}
	if c.StripeAPIURL != "http://127.0.0.1:12111" {
		t.Fatalf("StripeAPIURL = %q", c.StripeAPIURL)
	}
}

func TestPushMode(t *testing.T) {
	productionEnv(t)
	c, err := Load()
	if err != nil || c.PushMode() != "off" || c.APNsTopic != "sh.opendesk.app" {
		t.Fatalf("default: %v %q %q", err, c.PushMode(), c.APNsTopic)
	}
	t.Setenv("PUSH_RELAY_URL", "https://api.opendesk.sh/")
	if c, err = Load(); err != nil || c.PushMode() != "relay" || c.PushRelayURL != "https://api.opendesk.sh" {
		t.Fatalf("relay: %v %q", err, c.PushRelayURL)
	}
	t.Setenv("PUSH_RELAY_URL", "http://relay.example.com")
	if _, err := Load(); err == nil {
		t.Fatal("http relay accepted in production")
	}
	t.Setenv("PUSH_RELAY_URL", "")
	t.Setenv("APNS_KEY_ID", "KEY1234567")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "APNS_KEY") {
		t.Fatalf("partial APNs config: %v", err)
	}
	t.Setenv("APNS_KEY", "-----BEGIN PRIVATE KEY-----")
	t.Setenv("APNS_TEAM_ID", "TEAM123456")
	if c, err = Load(); err != nil || c.PushMode() != "direct" {
		t.Fatalf("direct: %v", err)
	}
	t.Setenv("APNS_KEY", "")
	t.Setenv("APNS_KEY_ID", "")
	t.Setenv("APNS_TEAM_ID", "")
	t.Setenv("PUSH_RELAY_SERVE", "true")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "PUSH_RELAY_SERVE") {
		t.Fatalf("relay without a key: %v", err)
	}
	t.Setenv("PUSH_RELAY_SERVE", "")
	t.Setenv("APP_ATTEST_APP_IDS", "TEAM123456.sh.opendesk.app, ")
	if c, err = Load(); err != nil || len(c.AppAttestAppIDs) != 1 || c.AppAttestDevelopment {
		t.Fatalf("app attest: %v %v", err, c.AppAttestAppIDs)
	}
}

func TestAIUsageNamesFallBack(t *testing.T) {
	productionEnv(t)
	t.Setenv("AI_CREDIT_MARKUP", "2")
	t.Setenv("STRIPE_PRICE_CREDITS", "price_old")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.AIUsageMarkup != 2 || c.StripePriceTopUp != "price_old" {
		t.Fatalf("fallbacks: %v %q", c.AIUsageMarkup, c.StripePriceTopUp)
	}
	t.Setenv("AI_USAGE_MARKUP", "1.25")
	t.Setenv("STRIPE_PRICE_AI_TOPUP", "price_new")
	if c, err = Load(); err != nil || c.AIUsageMarkup != 1.25 || c.StripePriceTopUp != "price_new" {
		t.Fatalf("new names: %v %q %v", c.AIUsageMarkup, c.StripePriceTopUp, err)
	}
	t.Setenv("AI_USAGE_MARKUP", "0.5")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "AI_USAGE_MARKUP") {
		t.Fatalf("markup below 1: %v", err)
	}
}

func TestMailboxOAuthSwitch(t *testing.T) {
	productionEnv(t)
	c, err := Load()
	if err != nil || c.GoogleMailboxOAuth || c.MicrosoftMailboxOAuth {
		t.Fatalf("without clients: %v %v %v", err, c.GoogleMailboxOAuth, c.MicrosoftMailboxOAuth)
	}
	t.Setenv("GOOGLE_MAILBOX_OAUTH", "true")
	if c, _ = Load(); c.GoogleMailboxOAuth {
		t.Fatal("on without a Google client")
	}
	t.Setenv("GOOGLE_MAILBOX_OAUTH", "")
	t.Setenv("GOOGLE_CLIENT_ID", "gid")
	t.Setenv("GOOGLE_CLIENT_SECRET", "gsecret")
	t.Setenv("MICROSOFT_CLIENT_ID", "mid")
	t.Setenv("MICROSOFT_CLIENT_SECRET", "msecret")
	if c, _ = Load(); !c.GoogleMailboxOAuth || !c.MicrosoftMailboxOAuth {
		t.Fatal("configured clients default on")
	}
	for v, want := range map[string]bool{"false": false, "0": false, "off": false, "nope": false, "true": true, "1": true} {
		t.Setenv("GOOGLE_MAILBOX_OAUTH", v)
		if c, _ = Load(); c.GoogleMailboxOAuth != want || !c.MicrosoftMailboxOAuth {
			t.Errorf("GOOGLE_MAILBOX_OAUTH=%q: google %v microsoft %v", v, c.GoogleMailboxOAuth, c.MicrosoftMailboxOAuth)
		}
	}
	t.Setenv("MICROSOFT_MAILBOX_OAUTH", "false")
	if c, _ = Load(); c.MicrosoftMailboxOAuth {
		t.Fatal("MICROSOFT_MAILBOX_OAUTH=false")
	}
	// Mailboxes use the sign-in client unless they have their own.
	t.Setenv("GOOGLE_MAILBOX_OAUTH", "")
	if c, _ = Load(); c.GoogleMailboxClientID != "gid" || c.GoogleMailboxClientSecret != "gsecret" {
		t.Fatalf("mailbox client defaults to sign-in: %q", c.GoogleMailboxClientID)
	}
	t.Setenv("GOOGLE_MAILBOX_CLIENT_ID", "mbid")
	t.Setenv("GOOGLE_MAILBOX_CLIENT_SECRET", "mbsecret")
	if c, _ = Load(); c.GoogleMailboxClientID != "mbid" || c.GoogleMailboxClientSecret != "mbsecret" || c.GoogleClientID != "gid" || !c.GoogleMailboxOAuth {
		t.Fatalf("own mailbox client: %q %q", c.GoogleMailboxClientID, c.GoogleClientID)
	}
	t.Setenv("GOOGLE_MAILBOX_CLIENT_SECRET", "")
	if c, _ = Load(); c.GoogleMailboxOAuth {
		t.Fatal("a mailbox client without its secret must be off")
	}
	// The same for Microsoft: a mailbox-only app leaves Microsoft sign-in off.
	t.Setenv("MICROSOFT_MAILBOX_OAUTH", "")
	t.Setenv("MICROSOFT_CLIENT_ID", "")
	t.Setenv("MICROSOFT_CLIENT_SECRET", "")
	t.Setenv("MICROSOFT_MAILBOX_CLIENT_ID", "mmid")
	t.Setenv("MICROSOFT_MAILBOX_CLIENT_SECRET", "mmsecret")
	if c, _ = Load(); c.MicrosoftMailboxClientID != "mmid" || c.MicrosoftMailboxClientSecret != "mmsecret" || c.MicrosoftClientID != "" || !c.MicrosoftMailboxOAuth {
		t.Fatalf("own Microsoft mailbox app: %q %q", c.MicrosoftMailboxClientID, c.MicrosoftClientID)
	}
}

func TestMailhostISPDBDefault(t *testing.T) {
	productionEnv(t)
	if c, _ := Load(); c.MailhostISPDB {
		t.Fatal("self-host default must be off")
	}
	t.Setenv("STRIPE_SECRET_KEY", "sk_test_x")
	t.Setenv("STRIPE_WEBHOOK_SECRET", "whsec_x")
	t.Setenv("STRIPE_PRICE_PRO", "price_x")
	if c, err := Load(); err != nil || !c.MailhostISPDB {
		t.Fatalf("billing default must be on: %v", err)
	}
	t.Setenv("MAILHOST_ISPDB", "false")
	if c, _ := Load(); c.MailhostISPDB {
		t.Fatal("MAILHOST_ISPDB=false")
	}
}

func TestHelpCenterURL(t *testing.T) {
	cases := []struct {
		c    Config
		want string
	}{
		{Config{AppURL: "https://app.example", APIURL: "https://api.example", HelpDomain: "help.example"}, "https://acme.help.example"},
		{Config{AppURL: "http://localhost:5173", HelpDomain: "help.localhost"}, "http://acme.help.localhost"},
		{Config{AppURL: "https://desk.example", APIURL: "https://desk.example", ServeWeb: true}, "https://desk.example/help/acme"},
		{Config{AppURL: "https://app.example", APIURL: "https://api.example/"}, "https://api.example/help/acme"},
	}
	for _, c := range cases {
		if got := c.c.HelpCenterURL("acme"); got != c.want {
			t.Errorf("HelpCenterURL(%+v) = %s, want %s", c.c, got, c.want)
		}
	}
}

func TestMailboxIMAPDefault(t *testing.T) {
	productionEnv(t)
	if c, _ := Load(); !c.MailboxIMAP {
		t.Fatal("default must be on")
	}
	t.Setenv("MAILBOX_IMAP", "false")
	if c, _ := Load(); c.MailboxIMAP {
		t.Fatal("MAILBOX_IMAP=false")
	}
}

// TestAdminURL: the panel defaults to <APP_URL>/admin, or its dev server on a development
// API that does not serve the web app; its origin is what the admin routes accept.
func TestAdminURL(t *testing.T) {
	productionEnv(t)
	t.Setenv("APP_URL", "https://desk.example.com")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.AdminURL != "https://desk.example.com/admin" || c.AdminOrigin() != "https://desk.example.com" {
		t.Fatalf("default: %q %q", c.AdminURL, c.AdminOrigin())
	}
	t.Setenv("ADMIN_URL", "https://admin.example.com/")
	if c, err = Load(); err != nil || c.AdminOrigin() != "https://admin.example.com" {
		t.Fatalf("set: %v %q", err, c.AdminOrigin())
	}
	t.Setenv("ADMIN_URL", "admin.example.com")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "ADMIN_URL") {
		t.Fatalf("no scheme: want refusal, got %v", err)
	}
	t.Setenv("ADMIN_URL", "")
	t.Setenv("OPENDESK_ENV", "development")
	if c, err = Load(); err != nil || c.AdminURL != "http://localhost:5174" {
		t.Fatalf("development: %v %q", err, c.AdminURL)
	}
}

// TestTrustedProxyHops: a count from 0 to 10, and anything else refuses to boot rather
// than quietly reading the client address from the wrong place.
func TestTrustedProxyHops(t *testing.T) {
	productionEnv(t)
	if c, err := Load(); err != nil || c.ProxyHops != 0 {
		t.Fatalf("default: %v %d", err, c.ProxyHops)
	}
	t.Setenv("TRUSTED_PROXY_HOPS", "2")
	if c, err := Load(); err != nil || c.ProxyHops != 2 {
		t.Fatalf("2: %v %d", err, c.ProxyHops)
	}
	for _, v := range []string{"two", "-1", "11"} {
		t.Setenv("TRUSTED_PROXY_HOPS", v)
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "TRUSTED_PROXY_HOPS") {
			t.Fatalf("%q: want refusal, got %v", v, err)
		}
	}
}

func TestLogLevel(t *testing.T) {
	productionEnv(t)
	c, err := Load()
	if err != nil || c.LogLevel != slog.LevelInfo {
		t.Fatalf("production default: %v %v", c.LogLevel, err)
	}
	t.Setenv("LOG_LEVEL", "debug")
	if c, err = Load(); err != nil || c.LogLevel != slog.LevelDebug {
		t.Fatalf("LOG_LEVEL=debug: %v %v", c.LogLevel, err)
	}
	t.Setenv("LOG_LEVEL", "loud")
	if _, err = Load(); err == nil {
		t.Fatal("LOG_LEVEL=loud was accepted")
	}
}

// TestEnvDefault: without OPENDESK_ENV, only an instance on localhost is development.
func TestEnvDefault(t *testing.T) {
	t.Setenv("OPENDESK_ENV", "")
	t.Setenv("AUTH_SECRET", strings.Repeat("s", MinAuthSecretLength))
	t.Setenv("CREDENTIALS_KEY", strings.Repeat("ab", 32))
	t.Setenv("NATS_LISTEN", "127.0.0.1:4222")
	t.Setenv("APP_URL", "https://desk.example.com")
	t.Setenv("API_URL", "https://desk.example.com")
	c, err := Load()
	if err != nil || !c.IsProduction() {
		t.Fatalf("public URLs: %v %v", c, err)
	}
	t.Setenv("APP_URL", "http://localhost:5173")
	t.Setenv("API_URL", "http://localhost:8080")
	if c, err := Load(); err != nil || c.IsProduction() {
		t.Fatalf("localhost: %v", err)
	}
	t.Setenv("OPENDESK_ENV", "staging")
	if _, err := Load(); err == nil {
		t.Fatal("unknown OPENDESK_ENV accepted")
	}
}
